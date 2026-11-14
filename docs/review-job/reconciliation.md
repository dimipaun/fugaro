# Reconciliation: the review job (option C) against the code

Checked against `origin/main` at `2988c2d64`, 2026-11-14. Paths and symbols are cited with the line they sit on at
that commit (line numbers drift: re-find the symbol before relying on one). **Where this page disagrees with
[spec.md](spec.md), this page wins.** The owner could not be asked while this was written: every decision below that
the owner has not ruled on is an **open decision** with a recommendation, never an answer.

## Assumptions

- **A1.** "Review" means `fugaro review --pr N` and the same review launched through `fugaro run --recipe review-only`
  being one product behaviour (the owner's feedback, spec.md).
- **A2.** "Provider and account" is the axis `config.ProjectJobAccounts` and `config.ReviewVariantAccount` already
  define: the default account is spelled `default` in a review variant's name.
- **A3.** The implicit job is the one the merged piece 1 deploys (`implicitReviewVariant`), and the plan builds on it
  rather than redesigning it.
- **A4.** "Merged" below means on `origin/main` at the commit above; "live" means a run that is going now
  (`fugaro runs ls --json`, 2026-11-14 13:27 UTC).

## What the repository already has

| Spec concept | What exists |
| --- | --- |
| the review-only recipe | `internal/recipe/catalog/review-only.yaml`; the launcher's constant `reviewOnlyRecipe` in `internal/cli/review.go` (line 66) |
| the review-only job variant of a shared project job | `reviewVariant` and `addReviewVariant` in `internal/infra/projectjobs.go` (lines 359, 329); the service account `credsvc.ReviewServiceAccountID` (`internal/credsvc/review.go`, line 102) |
| the implicit default review-only job (piece 1) | `implicitReviewVariant` (`internal/infra/projectjobs.go`, line 496), `ImplicitReviewProfile` (line 420, which is `projectpolicy.DefaultProfileName`), `ImplicitReviewJobName`, `CarriedProjectReviewJobs` (`internal/infra/tfvars.go`, line 1322, keeps the job in the plan `prepare` rewrites) |
| the credentials service's review table | `credentialsServiceReviewJobAccounts` (`internal/cli/credentials_serve.go`, line 903) reads the deployment's `FUGARO_REVIEW_JOB_ACCOUNTS`; `infra.CredentialsReviewJobAccounts` (`internal/infra/tfvars.go`, line 801) writes it, the implicit row included |
| the service's pairing of a review caller | `pairReview` (`internal/credsvc/review.go`, line 392): the table, then `s.cfg.Repos[id.Slug]`, then `admittedAccounts` (`internal/credsvc/cascade.go`, line 265) |
| the launcher's resolution | `reviewJobFor` (`internal/cli/review.go`, line 261), called at `internal/cli/run.go` line 5243 after `launchProjectJobFor` |
| the runner's placement | `matchJobName` (`internal/runner/projectjob.go`, line 313), `projectJobCandidates` (line 359), `checkJobClass` (line 258), `cannotPlaceJob` (line 459) |
| the Bitbucket review class | merged #2526 (the service lets the review class use the Bitbucket proxy, read-only: `internal/credsvc/bitbucketproxy.go`, `ReadOnly: id.JobClass == ClassReview`, line 164) and #2527 (the runner of a review variant opens Bitbucket through the proxy) |

## Findings

- **F1 (Exists).** Piece 1 is merged: PR #2523 (`cebdbe628`). It emits one implicit review-only job whenever the
  credentials service is on, a single GitHub App ID is recorded, and no coding job's variant already is that job; its
  tests are `TestImplicitReviewJobIsDeployedWithoutProfiles` and `TestImplicitReviewJobNeedsARecordedAppID`
  (`internal/infra/projectjobs_test.go`, lines 2169, 2403). The spec's piece 1 is done and RJ1 records it.
- **F2 (Gap).** The spec says "per (provider, account)", the code deploys exactly **one** row: the github provider and
  the default account (`implicitReviewVariant`, which builds a single twin from `githubProvider` and the axis's empty
  account). A named account, the project's Anthropic key and its provider key (`config.AnthropicKeyAccount`,
  `config.ProviderKeyAccount`) have a review variant only where a profile derives one. Piece 3's brief asks the
  launcher to resolve "(provider, the review's account)", which with one deployed row means the default account only.
  See D2385.
- **F3 (Gap, load-bearing for piece 2).** The runner cannot place a run in the implicit job. `matchJobName` matches
  `CLOUD_RUN_JOB` against `projectJobCandidates(p)`, which is built from the published policy's profiles alone; with no
  profile there is no candidate, the name then falls to the per-repository comparison and fails, and
  `cannotPlaceJob` halts the run ("neither a project job of this project's policy nor the per-repository job of this
  run"). So until piece 2 lands, a review the launcher puts on the implicit job halts at its first step. This is the
  piece the spec calls "checkJobClass agrees"; the actual change is in placement (`matchJobName` and
  `ProjectJobOfPolicy`, line 383, which the launcher also uses), and `checkJobClass` follows from the candidate's
  class.
- **F4 (Gap).** The credentials service already carries the implicit row, through the deployment's table, and says so
  in its own comment on `ReviewJobAccountsFor` (`internal/credsvc/review.go`, line 183: the two sets "differ by ONE row
  on purpose"). What piece 2 still owes is the other gate: `admittedAccounts` returns the empty set when the
  repository's admissions carry no credentials or the shared node holds no account, and `pairReview` then refuses
  ("the calling account is not admitted to the run identity's repository"). Whether a project with no profiles has the
  default account admitted to a repository is U2.
- **F5 (Exists).** The refusal the owner meant is in `reviewJobFor` (`internal/cli/review.go`, line 263, a line
  earlier than the brief's `:262`); piece 3 is live (run `20261114-130709-a709`, no pull request yet) and removes it for
  GitHub. Its brief forbids touching `internal/cli/project_job.go` and `launch_budget.go`.
- **F6 (Gap).** The implicit job is GitHub only: `implicitReviewVariant` sets `providers.EnvGitHubAppID` and returns
  false without a recorded GitHub App ID, and `ProjectJobs` returns nil with no profile. Bitbucket's service and runner
  halves are merged (the table above), but a Bitbucket project with no profile deploys no review job, so
  `fugaro review --pr N` on Bitbucket still refuses. The Bitbucket piece is therefore the implicit job for provider
  bitbucket (and its launcher resolution), not the service.
- **F7 (Rule).** The owner's suspicion that Bitbucket needs workspace-level tokens is already answered upstream, in a
  different plan: `docs/bitbucket-token-types` rules (D2350, D2355, answered 2026-11-13 as recommended) that the
  bundle holds any mix of repository, project and workspace tokens and the proxy serves each request with the narrowest
  covering one, and #2526's brief fixes that a workspace token must still be paired to the ONE repository of the run
  identity. So a shared review job needs no workspace token **in particular**: it needs the proxy to hold some token
  covering the repository, which is that plan's selection (its T4 and T8). The two plans must not each invent a token
  rule; the review plan only depends on that plan's selector (RJ7's dependency).
- **F8 (Rule, open tension).** `docs/shared-jobs-default` (plan merged, round-4 review fixes #2580) makes
  every project run on shared project jobs through a default profile (its F11: today "no code writes a default
  profile"). Once that lands, every project has a coding job whose review variant IS the implicit job's name
  (`ImplicitReviewProfile` is `DefaultProfileName`). `implicitReviewVariant` is built to absorb that (its collision
  gate; deleting the job would be refused by `RefuseProjectJobDeletes`, `internal/cli/init.go` line 2622), but the two
  specs describe the same end state by different roads. See T1.
- **F9 (Gap).** Piece 4 is smaller than it reads: `CarriedProjectReviewJobs` already keeps the implicit job through
  `prepare`, and `TestUpdateLeavesARunInFlightRunning` (`internal/cli/update_in_flight_test.go`, line 52) pins the
  in-flight rule for the update. What is not pinned is "an installation at the previous version with no implicit job
  converges to having it in one `fugaro update`, with a run in flight", and the documents (release notes
  `### For operators`, the skill, the `docs/` page naming the old refusal) do not yet say review works without profiles.
- **F10 (Gap).** The implicit job's task timeout is the constant `implicitReviewJobTimeout` (3 h, the default total);
  the launcher's `projectJobReviewFor(profile, provider, account, coding.Timeout)` (`internal/cli/project_job.go`,
  line 1055) takes the coding job's timeout, and with no coding job there is none. A repository whose workflow sets a
  shorter or longer `timeout_class` is reviewed under the constant. Judged low impact; see D2387.
- **F11 (Rule).** The two review paths stay different by design where they must: `fugaro run --recipe review-only`
  runs in the repository's own job (it mounts the repository's secrets), `fugaro review` runs in the isolated review
  job. The owner's rule is that both *work*, not that they are the same container. No finding; recorded so a task does
  not collapse them.

## Hard questions

- **Trust boundary.** The implicit job has its own service account and mounts nothing; the shared and review account
  sets must stay disjoint (`TestSharedAndReviewSetsAreDisjoint`, `internal/credsvc/sharedjobs_test.go`, line 370). A
  placement change in the runner (F3) must keep `checkJobClass` refusing a coding run in a review job and the reverse.
  Piece 2 touches credentials: L3 with a mutation check (make the runner place any unknown job name as the implicit job
  and expect the per-repository-job and foreign-job refusals to fail).
- **Migration.** Piece 1 already converges (F9); the open work is the previous-version convergence test, and a managed
  update must apply it behind its gate.
- **What is smallest and useful first.** Pieces 1 and 3 together make GitHub review work **if** the runner places the
  job, so piece 2 is the true second step and the end-to-end live check (Check 610) is the proof.

## Decisions

Number block taken: **D2380–D2399** (D2350–D2379 are the token-types plan's; the highest D on `origin/main` and in open
pull requests' documents was D2379). Answers given are the owner's own words from spec.md; empty cells are open.

| ID | Question | Recommendation | Answer |
| --- | --- | --- | --- |
| D2380 | Which fix for "review refuses on a project with no shared job"? | Option C, one review-only job per (provider, account) | **Answered 2026-11-13 by the owner: option C**, over A (a sibling per repository job) and B (a default profile) |
| D2381 | Piece order and review levels | pieces 1 to 4 in that order, L3/L3/L2/L2 | **Answered 2026-11-13 by the owner**; piece 3 was launched ahead of piece 2 (run `a709`), see D2384 |
| D2382 | Trace Bitbucket and the catalog-image mapping after piece 1 | yes | **Answered 2026-11-13 by the owner**; the trace is F6, F7 and U3 |
| D2383 | Does Bitbucket review need a workspace-level token? | No token type in particular: the proxy's narrowest-covering selection (token-types D2355) serves any mix; the review job holds none | **OPEN**; the owner suspected yes, F7 says the token-types plan already makes it moot. Confirm with Check 191 |
| D2384 | May piece 3 (launcher) merge before piece 2 (runner placement)? | Yes: nothing ships between them (no user is on a build between two merges), but the pull request body says a review on the implicit job halts until piece 2 | **OPEN** |
| D2385 | Which accounts get an implicit job: the default account only (today), or one per account the installation admits? | Default only now; add named-account rows only when a project with no profile can name an account (`config.ProjectJobAccounts` on a profile-less project yields one) | **OPEN** |
| D2386 | When does the Bitbucket implicit job (RJ7) build: now, or after the token-types selector (T8) and the shared-jobs default's Bitbucket deployer (S1) land? | After, because it needs the proxy's token to be selected and `fugaro review` on a default profile may then need no implicit job | **OPEN** |
| D2387 | The implicit job's timeout: the 3 h constant, or the repository's workflow's `timeout_class` carried by the launcher? | The constant (a review is bounded as a default repository is); a workflow-specific bound is a later change | **OPEN** |
| D2388 | A baked or catalog-image repository is reviewed in the base image: is that sufficient? | Yes while the review recipe only reads the checkout; revisit when a review runs the repository's tests | **OPEN**; U1 |

## Open tensions

- **T1.** Option C (the implicit job, `ImplicitReviewProfile` = the default profile) and `docs/shared-jobs-default` (every
  project on shared jobs by a default profile, whose coding job derives the same variant) reach one end state by two
  roads. Not contradictory (F8: the collision gate keeps one job), but whichever lands second must prove the first
  keeps working, and a profile named like the default profile with a different provider is not covered by either spec.
  Recommendation: keep both; add one test in whichever lands second.
- **T2.** The owner's levels (L3/L3/L2/L2) review piece 3 at L2 although it decides which job a review runs on, and the
  refusal removed is a safety refusal (a review must never start on a coding job). The brief's mutation check covers
  it; no change recommended, flagged for the owner.

## Open items

D2383 to D2388 and T1, T2 are listed in the pull request's Open items.

## UNVERIFIED

- **U1.** The base-image review path for baked-image repositories (the owner's own list names it). Settled by Check 610
  on a repository with a baked workflow; the owner runs it.
- **U2.** That a project with no profiles admits the default account to a repository (F4). Settled by reading what
  `fugaro project policy publish` writes for a profile-less project and by Check 610.
- **U3.** The default account when the project's auth is an API key or Vertex (the owner's list names it). Settled by
  Check 610 on such an installation.
- **U4.** A live `terraform apply` with no project jobs (the golden plan test exists, a real apply is the owner's).
- **U5.** The line numbers above were read on this commit and will drift.
