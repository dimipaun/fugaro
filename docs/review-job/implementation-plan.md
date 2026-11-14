# Implementation plan: the review job (option C)

Builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md) (findings F1 to F11, decisions D2380 to
D2388, open tensions T1 and T2). **This plan approves nothing: every task not already merged or live needs the owner's
explicit go.** Number block taken: decisions D2380 to D2399, checks 610 to 619 (610 is used here). The answers that
shaped it: option C over A and B (D2380); pieces in the order 1 to 4, then Bitbucket, at levels L3/L3/L2/L2 (D2381).

The decoupling pass is **on** (`fugaro settings planning.decoupling --json --repo dimipaun/fugaro` reads `true`). No
edge was worth breaking: the one interface the pieces share, the implicit job's triple and name, is already merged
code (`infra.ImplicitReviewJobName`, `credsvc.ReviewJobAccountFor`), so no contract task is needed and the graph records
no decoupling. Rigor mode per path was not read; every task touching the credentials service, the runner's job
placement, Terraform or IAM is marked opus with an independent review.

## Status as of 2026-11-14

Pieces 1 and 3 are in runs or merged; see the tasks. RJ1, RJ6 and RJ7 are merged (3 of 10). RJ3 is live (run
`20261114-130709-a709`). RJ2, RJ4, RJ5, RJ8, RJ9 and RJ10 are not started and unapproved.

## Conventions

- **Always**: a targeted `go test ./<pkg>/ -run '<names>' -count=1` with `GOFLAGS=-p=2`; `gofmt -l .` empty; a docs
  change runs `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`; comments explain why.
- **Ask first**: any change to the credentials service's pairing, the policy's admissions, a Terraform module or a
  wire shape (`.claude/rules/credentials-service.md`, `.claude/rules/terraform.md`).
- **Never**: a secret-shaped literal; a live cloud action by an agent; a review started on a coding job; a field that
  changes meaning; editing `.github/workflows/` (stage under `docs/pending-workflows/`); a hostile agent's name for its
  own job taken as its class (`checkJobClass` reads the policy's computation, never a suffix).
- **Hard rule (CLAUDE.md)**: `fugaro update` is the whole upgrade and is safe with runs in flight; RJ4 pins it.
- Path rules loaded: `.claude/rules/credentials-service.md` (RJ2, RJ8), `.claude/rules/terraform.md` (RJ4, RJ8).

## Global constraints and review focus

| Input or failure mode | Pinned by |
| --- | --- |
| A foreign or hand-made job name must still halt in the runner after the implicit job is placeable | RJ2 |
| A coding run must not be placed in the review job, nor a review in the shared job | RJ2 (mutation check) |
| The default account must be admitted to the repository by the policy, not by the table alone | RJ2 |
| A review is never launched on a coding job when no review job exists (refusal sentences stay) | RJ3 |
| An installation at the previous version converges to having the implicit job with a run in flight | RJ4 |
| The Bitbucket review reads one repository only and writes nothing | RJ6 (merged), RJ8 |

## Slice 1: GitHub review works with no profile (pieces 1 to 4)

### RJ1: piece 1, the implicit review-only job (merged)
- Acceptance: with the credentials service on and one GitHub App ID recorded, an installation with no profiles deploys one
  review-only job, its own service account, no mount, and the row the credentials service admits it by; an
  installation with profiles deploys the plan it deployed before.
- Files: `internal/infra/projectjobs.go`, `internal/infra/tfvars.go`, `internal/infra/projectjobs_test.go`, the
  cover and IAM pins.
- Depends on: none.
- Level: L3, opus with an independent review.
- Status: **merged**, #2523 (`cebdbe628`), run `20261113-021818-58aa` and its follow-up `20261113-132712-96ca`.

### RJ2: piece 2, the runner places the implicit job and the policy admits its account
- Acceptance: a run whose Cloud Run job is the implicit review-only job, on a project that published no profile,
  is placed as a review run (profile `ImplicitReviewProfile`, provider github, account default), not halted by
  `cannotPlaceJob`; `ProjectJobOfPolicy` answers the same triple for the launcher; `pairReview` admits the default
  account to a repository only where the policy's admissions do (resolve U2 first and record the answer in the task's
  pull request); every other unknown job name still halts, and a coding run is still refused in the review job.
- Files: `internal/runner/projectjob.go`, `internal/runner/projectjob_internal_test.go`,
  `internal/credsvc/review.go`, `internal/credsvc/review_test.go` (and `internal/credsvc/cascade.go` only if U2 shows
  an admission change is needed).
- Interfaces: consumes `infra.ImplicitReviewJobName` and `credsvc.ReviewJobAccountFor` (merged); produces the placement
  RJ3 and RJ8 launch against.
- Tests: `TestMatchJobNamePlacesTheImplicitReviewJob`, `TestForeignJobStillHalts`,
  `TestCoderIsRefusedInTheImplicitReviewJob`, `TestPairReviewAdmitsTheDefaultAccountWithNoProfiles`.
- Command: `GOFLAGS=-p=2 go test ./internal/runner/ -run 'ImplicitReview|ForeignJob|CheckJobClass' -count=1 && GOFLAGS=-p=2 go test ./internal/credsvc/ -run 'PairReview|ReviewAccounts|SharedAndReview' -count=1`
- Red step: the placement test fails first with the `cannotPlaceJob` sentence (F3).
- Mutation check: make placement accept any unknown job name as the implicit job; the foreign-job and the
  per-repository-job tests must fail.
- Depends on: RJ1 (merged; behaviour, it writes the job and the table row this places and pairs).
- Commit: `runner, credsvc: a review run in the implicit review-only job is placed and paired`
- Reviewer tier: opus, independent review (L3).

### RJ3: piece 3, the launcher resolves a review onto the implicit job
- Acceptance: with no shared project job, `reviewJobFor` resolves the implicit review-only job for GitHub when the
  credentials service is configured and the job is deployed; no credentials service, a non-admitted account, a still-going
  coding run, an older binary and Bitbucket keep their refusals; a repository that resolves a shared job keeps its
  profile-derived variant.
- Files: `internal/cli/review.go`, `internal/cli/review_test.go` (a docs pin only if one names the removed sentence).
- Tests, command and mutation check: as the run's brief states (unconditional resolution must fail the no-credentials-service
  test).
- Depends on: RJ1 (merged; the name and the deployed row).
- Level: L2.
- Status: **live**, run `20261114-130709-a709` (no pull request yet). **Launch note (D2384):** it may merge before RJ2;
  a review on the implicit job then halts at the runner until RJ2 lands, which the pull request says.
- Reviewer tier: opus, independent review (a safety refusal moves).

### RJ4: piece 4a, the migration converges with a run in flight
- Acceptance: an installation at the previous version (no implicit job in its tfvars, credentials service on) gets
  the job from one `fugaro update`, with a run in flight left running and its records, ledger and counters untouched;
  a second `update` is a no-op.
- Files: `internal/cli/update_in_flight_test.go` (or `internal/cli/update_review_job_test.go` (new)),
  `docs/design/update-in-flight.md` (its row for the implicit job).
- Tests: `TestUpdateAddsTheImplicitReviewJobAndLeavesARunInFlightRunning`, plus the existing
  `TestUpdateLeavesARunInFlightRunning` run by name.
- Command: `GOFLAGS=-p=2 go test ./internal/cli/ -run 'UpdateLeavesARunInFlightRunning|UpdateAddsTheImplicitReviewJob' -count=1`
- Red step: with `CarriedProjectReviewJobs` removed from the update path the new test fails.
- Depends on: RJ1 (merged; behaviour).
- Level: L2; reviewer tier opus (an `update` step on an existing installation).

### RJ5: piece 4b, the documents say review works with no profile
- Acceptance: the release notes' `### For operators` is "run `fugaro update`" plus what it asks, with the implicit job
  named; the `fugaro:working` skill and the page that described the old refusal no longer say a review needs profiles;
  the plugin tests that check every command and flag still pass.
- Files: `docs/releases/` (the open release's notes), `plugin/skills/working/SKILL.md`, the docs page naming the
  refusal, and the docs pins that read them (the help text in `internal/cli/review.go` belongs to RJ3, so no level holds
  two writers of that file).
- Tests: `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1` and the `plugin/` tests.
- Depends on: RJ2 and RJ3 (behaviour: the documents state what works), RJ4 (the operator sentence).
- Level: L2; reviewer tier opus (a plugin skill and a recipe-adjacent document).

## Slice 2: Bitbucket (the traced piece)

### RJ6: Bitbucket review B-adm, the service lets the review class read through the proxy (merged)
- Status: **merged**, #2526 (`e5cfe4126`); read-only, paired to one repository.
- Level: L3. Depends on: none.

### RJ7: Bitbucket review B-run, the runner of a review variant opens Bitbucket through the proxy (merged)
- Status: **merged**, #2527 (`6bcb58a53`). Level: L3. Depends on: RJ6.

### RJ8: the implicit Bitbucket review-only job and its launcher resolution
- Acceptance: with the credentials service on and a Bitbucket token held by the proxy, an installation with a Bitbucket
  repository and no shared Bitbucket job deploys one review-only job for provider bitbucket (no mount, own service
  account, never colliding with a profile-derived variant), the runner places a run in it, and `fugaro review --pr N`
  resolves onto it instead of refusing with its Bitbucket sentence.
- Files: `internal/infra/projectjobs.go`, `internal/runner/projectjob.go`, `internal/cli/review.go` and their tests
  (the provider becomes a parameter where it is the constant `githubProvider` today).
- Tests: a golden plan with a Bitbucket repository and no profiles; the collision test; the placement test; the
  launcher test; mutation check as RJ2's.
- Depends on: RJ2 and RJ3 (the same files, and the placement and resolution it generalises), and the token-types
  plan's selector (`docs/bitbucket-token-types` T4 and T8) as a launch gate, since the job holds no token and the proxy
  must pick one (D2383, D2386). Not started; its timing is an open decision.
- Level: L3; reviewer tier opus.

## Slice 3: the live checks (owner-run)

### RJ9: Check 610, a review on a repository with no profile and a baked workflow
- Owner-run: the owner runs one command from a script a later task provides (the sandbox GitHub repository with no
  `profiles:` block): `fugaro review --pr N`. It answers U1, U2, U3 and U4. No agent performs any step.
- Depends on: RJ2, RJ3, RJ4.

### RJ10: Check 611, the same on a Bitbucket repository
- Owner-run: as RJ9 on the Bitbucket sandbox repository, after Check 191's open state is closed. Depends on: RJ8.

## Parallel execution

Edges (the reason each is kept):

- RJ2 needs RJ1: it places and pairs what RJ1 deploys (behaviour, and RJ1 is merged).
- RJ3 needs RJ1: the name and the deployed row (merged).
- RJ4 needs RJ1: it pins the convergence of what RJ1 deploys (merged).
- RJ5 needs RJ2, RJ3, RJ4: the documents describe behaviour.
- RJ7 needs RJ6: the runner half reads the service half's read-only class (merged).
- RJ8 needs RJ2 and RJ3: they own the same files, so two tasks writing one file must be ordered.
- RJ9 needs RJ2, RJ3 and RJ4; RJ10 needs RJ8.

Levels (as `fugaro plan graph` numbers them, RJ1 included): **L0** RJ1 and RJ6 (merged); **L1** RJ2, RJ3 (live), RJ4
and RJ7 (merged) run at once; **L2** RJ5, RJ8 (once its gate allows) and RJ9; **L3** RJ10. Critical path, as the read
reports it: RJ1, RJ2, RJ8, RJ10 (4 tasks). Sequential only where a file or behaviour forces it: RJ8 after RJ2 and RJ3.

Shared files: `internal/runner/projectjob.go` is written by RJ2 (L1) and RJ8 (L2); `internal/cli/review.go` by RJ3
(L1) and RJ8 (L2); `internal/infra/projectjobs.go` by RJ8 alone (RJ1 is merged). No level holds two writers of one
file.

Edges broken by contract: none (the interface is merged code). The gain: none to report; the graph's read prints no
broken edge.

## Considered and declined

- A `-review` sibling per per-repository job (option A) and a default profile (option B): the owner chose C.
- A contract task for the implicit job's triple: it is merged code, a contract would only restate it.

## Checks the plan takes

Check 610 (RJ9, owner-run) and Check 611 (RJ10, owner-run), from the reserved block 610 to 619; each answers the UNVERIFIED
items in the reconciliation.

## After-deploy steps

RJ5 writes the release note: `### For operators` is "run `fugaro update`" and says it adds the review-only job without
a profile; no other step.
