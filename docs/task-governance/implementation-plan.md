# Implementation plan — task governance and periodic run reviews

## Status

This plan builds [spec.md](spec.md) (task governance, 2026-10-22) and
[spec-periodic-reviews.md](spec-periodic-reviews.md) (periodic run reviews, 2026-10-25) **as
amended by [reconciliation.md](reconciliation.md)**, whose decisions D320–D333 the owner answered on
2026-10-26. **Nothing in it is implemented. No task is approved:** the answers settle questions, and
each task still needs the owner's own explicit go.

The answers that shape the plan:

- **D320 (not the recommendation): a Fugaro run makes the report's pull request.** The report
  itself is still produced deterministically (no model writes a figure) by a runner-side step inside
  the run; the runner commits it, and the run's model part is the one senior review every recipe
  ends with. This adds a model review stage per report run; the job's existing grant already reads
  its own repository's records (listing is U8, Check 135); the trigger is the user's own (R8).
- **D321** `docs/fugaro-reviews/<YYYY>/`, one file per period, a generated index, a size cap.
- **D322** a report covers the repository it is committed into, nothing else.
- **D323** IDs and numbers, no people.
- **D324** Fugaro's own reports go into this public repository under D323's rules.
- **D325** top cost plus fixed flags; **D326** fixed pattern rules, state in the committed reports.
- **D327/D328** a redacted draft, a "Skill learning" issue form with an MIT checkbox, a maintainer
  rewrite.
- **D329** a checkpoint is halt + checkpoint report + follow-up.
- **D330** a tier is an expectation, never a cap; **D331** stage budgets in the gateway, gateway
  runs only; **D332** stop on fan-out and on the review-scope mismatch, report the rest; **D333**
  instruction pre-flight deferred (no task here).
- **R1–R8** of the reconciliation stand as written.

## 0. What this plan does not do

- No Fugaro-side scheduler, service or Cloud Scheduler job for the reviews (R8): the user's own
  routine, cron or agent launches the report run.
- No in-job pause, no channel into a running job (D329).
- No automatic retry (R4) and no automatic change to any threshold or tier default (G§6: "nothing
  changes automatically without approval").
- No instruction pre-flight (D333, deferred until Slice 6 exists and the owner asks for it).
- No 50% soft check-in (G§4's optional one): a job cannot pause, and a halt at half the expectation
  would stop every medium task that is merely slow. The `working` skill's half-cap look stays the
  manual form.
- No idle-spin or edits-back-and-forth tripwire: nothing records file changes per model call, so
  there is no data to flag them from. The loop tripwire this plan builds is the repeated identical
  command (T1.4); scope drift is a digest flag once tasks carry an expected scope (T8.3).
- No live cloud action by an agent. Checks 135–139 are the owner's.

## 1. The slices, in one table

| Slice | What it ships | Useful alone because |
| --- | --- | --- |
| **1 — the record** | `calls` and `agents` per stage on `result.json` (R1) | every later report and guardrail reads it; logs expire in 30 days |
| **2 — the digest** | `fugaro runs digest --period day\|week\|month\|year`: a deterministic, redacted Markdown report of this repository's runs, with patterns from earlier reports | replaces the owner's private script; usable from a terminal the day it merges |
| **3 — the report run** | a built-in `run-digest` recipe whose deterministic `digest` step writes the due reports inside a Fugaro run and whose one senior review decides the PR (D320); no new grant (the job already reads its own repository's prefix) | the owner's chosen delivery; the user schedules `fugaro run --recipe run-digest` |
| **4 — the practice** | the `working` skill rewritten around the digest and its cadence, the submission draft, the issue form, the contribution note (R2, R6, D327, D328) | every user gets it with the next release |
| **5 — small guards** | the 80% cap warning (R5); the review-scope mismatch recorded (F2) | each is a one-file change with its own value |
| **6 — checkpoints** | halt + checkpoint report on the record and the PR, the governance halt reasons, `diagnose` and the skill (D329) | the stopping point every governance trip uses |
| **7 — stage budgets and fan-out** | per-stage call and dollar budgets and the review ratio in the gateway, the fan-out stop, the review-scope stop (D331, D332) | governance steps G§7.3 and G§7.4 |
| **8 — expectations** | the optional tier on a task, its expected-cost checkpoint, self-calibration suggestions in the digest (D330, F33) | governance steps G§7.6 and G§7.9 |

**The smallest useful first slice** is Slice 1 + Slice 2 + T4.1: the record, the command, and the
skill telling every user to run it. Slice 3 (the owner's run-based delivery) follows on top of it.

## 2. Check numbers this plan takes

From the block **Checks 135–144** (README). The register is `docs/gcp-live-checklist.md`, read by
`docs/coordinator/README.md` §"The numbers this work holds"; the task that writes a check adds its
register line in the same commit.

| Check | What it measures | Written by | Run by |
| --- | --- | --- | --- |
| **135** | Read-only: a per-repository job account reads its own repository's run record by name and is refused a sibling repository's; whether it may list its prefix is recorded for U8 (the plan never lists) | T3.5 | the owner, sandbox installation only |
| **136** | A `run-digest` run in the sandbox repository opens a pull request adding the due `docs/fugaro-reviews/<YYYY>/` files; no address in them; the figures equal `fugaro runs ls --json` for the same day | T3.5 | the owner, sandbox only |
| **137** | A sandbox run with a deliberately tiny review stage budget halts with `stage_budget`, a draft PR and a checkpoint report on the record and in the PR | T7.5 | the owner, sandbox only |
| **138** | A sandbox run under `governance.max_subagents: 3` whose review fans out to four sub-agents halts with `fanout` | T7.5 | the owner, sandbox only |
| **139** | Read-only, before T7.2 merges (U4): in an existing sandbox gateway run's logs, the main session's `model call` lines carry an empty `agent_id`, each sub-agent a distinct one, and background-model calls none — so a distinct-agent count is a sub-agent count | T3.5 | the owner, sandbox only |

**140–144** stay reserved and unused.

## 3. Conventions every task follows

Only the ones this plan's areas trigger; `CLAUDE.md` and the rules files hold the rest.

1. **Docs are tested.** A task that changes a pinned doc runs `GOFLAGS=-p=2 go test ./internal/cli
   -run Docs -count=1` and keeps the pinned fact, wording changed in both places.
2. **Goldens are regenerated.** A new command or flag regenerates
   `website/content/cli-reference.md` with `go run ./internal/clidoc`
   (`internal/clidoc` `TestCliReferenceIsCurrent`), never by hand.
3. **Skills track the CLI.** A task that adds a command a skill names changes the skill in the same
   PR; `plugin/skills_lint_test.go` checks commands and flags and holds the 400-line skill and
   300-line reference budgets.
4. **Schemas move with the wire.** A new `result.json` field or halt reason changes
   `schemas/result.schema.json` in the same commit; `schemas/schemas_test.go`
   (`TestResultSchemaHaltReasonsAgreeWithGo`) holds the halt enum.
5. **Hermetic tests.** No network, no home directory, no cloud: `internal/gcpfake`,
   `internal/testutil`, `t.TempDir()`. Terraform changes are tested with `-tags terraform`.
6. **No secret-shaped literal** and **no real person or organisation name** in any fixture, report
   sample or doc: fixtures use `person-1`, `owner/repo-a`, run IDs of the `20260101-000000-abcd`
   shape.
7. **Never edit `.github/workflows/`.** `.github/ISSUE_TEMPLATE/` is not a workflow and may be
   changed in a PR (T4.3).
8. **Steps after a deploy** go in the release notes' `### For operators` section in the same PR,
   labelled per installation or per repository (T3.4: per repository; T7.4: per installation).
   Slice 3 adds no grant and needs no `fugaro update`.
9. **Comments explain why.** No TODOs, no section-header comments.
10. **Targeted tests only**, `GOFLAGS=-p=2`; `internal/runner` and `internal/cli` are never run
    whole by a task.

## 4. Global constraints and review focus

| # | Constraint | Pinned by |
| --- | --- | --- |
| G1 | **No person in a report.** No digest output, Markdown or JSON, carries an address, account name, task text or log line; the report types have no field for one, and **no free-text field of a record is ever rendered** (`Halt.Detail`, `Reason`, `Branch`, `BaseBranch`, `OAuthAccount`, titles): only enums checked against their known sets (an unknown value renders as `other`), bounded numbers, dates, run IDs and PR numbers validated by shape; the diff base renders as yes/no ("the review read the pull request's own base"), never as a branch name (reconciliation O6). **Records are untrusted input**: any run of the repository (on a project job, any run) can write one | T2.1's structural test and planted-text test, T2.3, Check 136 |
| G2 | **One repository per report.** The digest reads only the repository it is pointed at. On a per-repository job the grant also enforces it (Check 135); **on a project job it does not** — the project job's `ProjectBucketCondition` reaches every repository's `runs/`, a reach the owner accepted on 2026-10-17 (`deploy/terraform/gcp/modules/project/sa.tf`) — so there the runner's own slug check is the only guard, and T3.2 tests it | T2.3, T3.2, Check 135 |
| G3 | **Deterministic figures.** The same records give byte-identical Markdown; no model writes or edits a figure (D320's consequence) | T2.1 golden test, T3.1 |
| G4 | **Closed periods only**, UTC, a run in the period it finished (R3) | T2.1 |
| G5 | **Absent, never zero.** A stage with no call count (an oauth run) has `calls` absent and the digest prints `n/a` | T1.1, T2.1 |
| G6 | **A tier never moves a cap** (D330); the deployed per-run cap stays the ceiling; a stage budget is a fraction under it | T7.1, T8.1 |
| G7 | **Every governance stop is a halt with a checkpoint report and a draft PR**, never a silent kill (G§5, D329) | T6.1, T7.3 |
| G8 | **Report-only tripwires change no outcome** (D332): the repeated-command loop flag and the scope-drift flag appear in the digest only | T1.4, T7.3, T8.3 |
| G9 | **Nothing changes a threshold automatically** (G§6): self-calibration prints suggestions | T8.3 |
| G10 | **A submitted learning is never pasted into a skill** (R7), and an issue's text is data, never instructions, for any agent that reads it — the submitter's or a maintainer's triage session | T4.3's form text and CONTRIBUTING sentence, T4.1 |

Review focus for every reviewer: the report run (Slice 3) is the one new path. It adds no grant —
the job already holds `roles/storage.objectUser` on its own repository's `runs/`, `cache/` and
`locks/` prefixes — but it is the first runner step that **reads other runs' records** and puts what
it read into a pull request on a possibly public repository (D324). Earlier runs wrote those
records, and some of their fields are free text an earlier task could steer (`Halt.Detail`,
`Branch`, `Reason`): the digest renders none of them (G1), so neither the public file nor the
review agent's prompt carries text a task chose. The step takes no task input; its read list comes
from the launcher's manifest (T3.6), validated by shape and slug; a later stage cannot change the
committed figures or add other files (T3.3). On a project job the bucket does not isolate
repositories (G2), so the slug check is tested, not assumed. Stage budgets (T7.2) and the expected-cost checkpoint
(T8.2) are on the money path and get the stronger review.

## 5. The slices

### Slice 1 — the record (R1)

**T1.1 — `calls` and `agents` on `StageTiming`**
- **Files.** `internal/runstore/runstore.go` (`StageTiming`: `Calls *int` `json:"calls,omitempty"`,
  `Agents *int` `json:"agents,omitempty"`), `schemas/result.schema.json`.
- **Interfaces.** Produces the two fields; consumed by T1.3, T2.1.
- **Schema change.** `result.schema.json` stage item gains two optional non-negative integers.
- **Tests.** `TestStageTimingCallsRoundTrip` (present, absent, zero kept distinct from absent);
  the schema test accepts both shapes.
- **Command.** `GOFLAGS=-p=2 go test ./internal/runstore ./schemas -run 'StageTiming|Schema' -count=1`
- **Red step.** The round-trip test does not compile (no field).
- **Depends on.** nothing. **Reviewer.** standard. **OWNER-RUN.** no.
- **Commit.** `runstore: a stage records its model calls and sub-agents`

**T1.2 — the gateway counts distinct agents per stage**
- **Files.** `internal/gateway/gateway.go` (`StageReport.Agents int`), the per-stage accumulator in
  the same package that fills `Calls` (keyed on `callLog.agentID` from `x-claude-code-agent-id`).
- **Interfaces.** Produces `StageReport.Agents`; consumed by T1.3 and T7.2.
- **Tests.** `TestStageReportCountsDistinctAgents`: three calls from two agent IDs plus the main
  session count 2 sub-agents; an empty agent ID is the main session.
- **Command.** `GOFLAGS=-p=2 go test ./internal/gateway -run StageReport -count=1`
- **Red step.** The field is missing.
- **Depends on.** nothing. **Reviewer.** standard.
- **Commit.** `gateway: a stage report counts its sub-agents`

**T1.3 — the runner records them, gateway or not**
- **Files.** `internal/runner/cost.go` (`stageTiming` takes the stage's sub-agent count),
  `internal/runner/runner.go` (the one `stageTiming` call passes it), `internal/agent/relay.go` and
  `internal/agent/agent.go` (`Result.SubAgents`: tool-use blocks naming the sub-agent tool, counted
  by the relay for every auth mode).
- **Interfaces.** Consumes T1.1, T1.2.
- **Tests.** `TestStageTimingRecordsGatewayCalls`, `TestStageTimingOAuthHasNoCalls` (calls absent,
  agents from the relay), `TestRelayCountsSubAgents`.
- **Command.** `GOFLAGS=-p=2 go test ./internal/runner ./internal/agent -run 'StageTiming|SubAgents' -count=1 -timeout 30m`
- **Red step.** `TestStageTimingRecordsGatewayCalls` fails: `calls` absent.
- **Depends on.** T1.1, T1.2. **Reviewer.** standard.
- **Commit.** `runner: every stage's record carries its calls and sub-agents`

**T1.4 — the repeated-command counter, and the two flags it feeds**
- **Files.** `internal/agent/relay.go` (per stage, the most times one identical shell command ran),
  `internal/agent/agent.go` (`Result.Repeats`), `internal/runner/cost.go` (`StageTiming.Repeats`),
  `internal/runstore/runstore.go`, `schemas/result.schema.json`, `internal/digest/digest.go` (two
  report-only flags: a stage with `repeats` ≥ 5, the loop tripwire; a run whose `review_scope.outside`
  (T5.2) is non-empty, the review-scope flag).
- **Tests.** `TestRelayCountsRepeatedCommand`, `TestDigestFlagsRepeatsAndReviewScope`.
- **Command.** `GOFLAGS=-p=2 go test ./internal/agent ./internal/runner ./internal/digest ./schemas -run 'Repeat|ReviewScope|Schema' -count=1 -timeout 30m`
- **Red step.** No stage records repeats; the digest flags neither.
- **Depends on.** T1.3, T2.1, T5.2, and T6.1 (`runstore.go`, an earlier level). **Reviewer.** standard.
- **Commit.** `runner: a stage records its most repeated command, and the digest flags it`

### Slice 2 — the digest (D321–D326, R2, R3)

**T2.1 — `internal/digest`: build, flag, render**
- **Files.** new `internal/digest/digest.go`, `internal/digest/render.go`,
  `internal/digest/digest_test.go`, `internal/digest/testdata/` (golden Markdown).
- **Interfaces.** `Build(recs []runstore.Record, p Period) Digest`; `Period` reuses
  `report.PeriodKey`'s clock; `Markdown(d) []byte` with a fenced `json` summary block; the `Digest`
  type has **no field for a person** (G1).
- **Rules.** Selection: dearest 15% by billed + notional cost, at least 3, at most 10 rows (D321,
  D325). Flags: review calls > 5 × max(implement calls, 20); a stage with more than 3 sub-agents; a
  halt (its reason enum, never its detail); more than one fix round. The review-scope and
  repeated-command flags come with T1.4, the scope-drift flag with T8.3. Billed and notional in
  separate columns. Size cap 8 KiB, rows dropped from the cheapest with a note.
- **Tests.** `TestDigestGolden` (fixed records → byte-identical file), `TestDigestHasNoPersonField`
  (reflect over the types), `TestDigestRendersNoRecordText` (**reflect-driven over every string
  field of `runstore.Record`, nested ones included**, so a field added later is covered: each is set
  to an address and an instruction-shaped sentence, and neither appears in the output),
  `TestDigestUnknownEnumRendersOther` (a forged `status`, `halt.reason` or stage name), `TestDigestClosedPeriodsUTC` (ISO week 2026-W53 across the year end; a
  run finishing at 23:59:59 vs 00:00:00), `TestDigestAbsentCallsPrintNA`, `TestDigestSizeCap`.
- **Command.** `GOFLAGS=-p=2 go test ./internal/digest -count=1`
- **Red step.** The package does not exist; the golden test fails.
- **Depends on.** T1.1 (fields to read). **Reviewer.** standard.
- **Commit.** `digest: a period's runs as a redacted, deterministic report`

**T2.2 — patterns from committed reports (D326)**
- **Files.** new `internal/digest/patterns.go`, `internal/digest/patterns_test.go`.
- **Interfaces.** `Patterns(prev []Summary, cur Summary) []Pattern`, reading the JSON blocks of
  earlier files; rules are a fixed table: a flag recurring in 3 of the last 4 weeks; review share of
  cost rising 3 months running; median cost per recipe up 50% month over month; halts above 10% of
  runs two weeks running. A weekly, monthly or yearly report lists them; a yearly report rolls up
  the twelve monthly summaries (F24).
- **Tests.** One test per rule, a negative for each, `TestYearlyRollsUpMonthlySummaries`,
  `TestPatternsIgnoreMalformedBlock` (a hand-edited file is skipped and named, not fatal).
- **Command.** `GOFLAGS=-p=2 go test ./internal/digest -run Pattern -count=1`
- **Red step.** No rule fires on the recurring-flag fixture.
- **Depends on.** T2.1. **Reviewer.** standard.
- **Commit.** `digest: fixed pattern rules over the committed reports`

**T2.3 — `fugaro runs digest`**
- **Files.** new `internal/cli/runs_digest.go`, new `internal/cli/runs_digest_test.go`,
  `internal/cli/runs.go` (`AddCommand`), `website/content/cli-reference.md` (regenerated).
- **Interfaces.** `fugaro runs digest --period day|week|month|year [--date YYYY-MM-DD] [--repo
  owner/name] [--dir docs/fugaro-reviews] [--stdout] [--json]`; reads the repository's records
  through the store `runs ls` uses (bounded like `runs stats`), writes
  `<dir>/<YYYY>/<key>.md` and regenerates `<dir>/README.md` (index). Not refused in an agent session
  (it prints no person, F22). One repository only (G2): no `--all`.
- **Tests.** `TestRunsDigestWritesPeriodFile`, `TestRunsDigestOneRepositoryOnly`,
  `TestRunsDigestRefusesOpenPeriod` (today), `TestRunsDigestIndexRegenerated`, and
  `TestCliReferenceIsCurrent`.
- **Command.** `go run ./internal/clidoc && GOFLAGS=-p=2 go test ./internal/cli ./internal/clidoc -run 'RunsDigest|CliRef' -count=1 -timeout 30m`
- **Red step.** The command is unknown.
- **Depends on.** T2.1, T2.2. **Reviewer.** standard.
- **Commit.** `cli(runs): digest writes a period's report for this repository`

### Slice 3 — the report run (D320)

D320's answer meets four facts of the tree (reconciliation F35–F38): a recipe step never names an
arbitrary command (`Step.Command` is `build`, `test` or `lint`); every recipe ends in exactly one
senior `review` step, so **every pull request passes a model review stage**; artifact runs never
commit; and the job's account already reads and writes its own repository's `runs/<slug>/` prefix
(`roles/storage.objectUser` under `gcp.BucketCondition`), so **no new grant is needed** for reading
a record by name. **Listing** the prefix is another matter: a list is a bucket-level call, the
condition matches object names, and `docs/design/bucket-iam.md` (V7) records that a prefix-limited
list would need an unverified request attribute — so the job most likely cannot list (U8). The plan
therefore never lists in the job: the **launcher**, which runs with the person's credentials and
already lists runs for `fugaro runs ls`, puts the run IDs into the task (T3.6), and the step reads
each record by name. The
precedent for a deterministic step that commits and skips implement is the autofix gate
(`internal/runner/gate.go`: `runAutofix` commits with the runner's identity, and `gateResult.Fixed`
skips implement; `gateResult.Clean` ends the run with no PR).

So the report run is a **built-in recipe whose first step is a runner-side, deterministic `digest`
step**: it writes every closed period's report not yet in the tree (yesterday; last ISO week on a
Monday; last month on the 1st; last year on 1 January; at most 7 missed days caught up), commits it
with the runner's identity, skips implement and hands the pull request to one senior review. No
model writes a figure (G3); the model's part is the review that decides ready or draft. The user's
routine launches it: `fugaro run --recipe run-digest "periodic run review"` (R8).

**T3.1 — the `digest` step kind and the `run-digest` recipe**
- **Files.** `internal/recipe/recipe.go` (`StepDigest` = `"digest"`: allowed only first, only in
  `mode: implement`, only followed by `review`), new `internal/recipe/catalog/run-digest.yaml`
  (`steps: [digest: {}, review: {max_rounds: 1}]`), `schemas/recipe.schema.json`,
  `internal/recipe/recipe_test.go`.
- **Tests.** `TestDigestStepFirstOnly`, `TestDigestStepNeedsReview`, `TestRunDigestInCatalog`,
  the recipe schema test.
- **Command.** `GOFLAGS=-p=2 go test ./internal/recipe ./schemas -run 'Digest|Catalog|Schema' -count=1`
- **Red step.** `digest` is an unknown step kind.
- **Depends on.** nothing. **Reviewer.** standard.
- **Commit.** `recipe: a digest step and the run-digest recipe`

**T3.2 — the runner's digest step**
- **Files.** new `internal/runner/digest.go`, new `internal/runner/digest_test.go`,
  `internal/runner/runner.go` (dispatch the step where the gate is dispatched, and one call at
  finalize into `digest.go`, which T3.3 fills).
- **Interfaces.** Consumes `internal/digest` (T2.1, T2.2), the task's `digest_runs` (T3.6) and the
  run's own runstore reader, reading each listed record **by name** under its own repository's
  prefix only (G2: an ID of another shape or another slug is refused, which is the only guard on a
  project job); produces committed files under `docs/fugaro-reviews/` and a `digest` stage on the
  record. Nothing due → the run ends with no PR, like a clean gate. No list call. A period is
  **due** only when its file is neither on the default branch nor in an open pull request of a
  `run-digest` run (read through the run's own provider), so an unmerged report is not written twice.
  Every record is untrusted input (G1): an unknown enum renders as `other`, an out-of-range number
  drops the record and is counted in the report.
- **Tests.** `TestDigestStepWritesDueFiles` (gcpfake bucket with records of two repositories and a
  manifest naming both: only the run's own repository's are read), `TestDigestStepRefusesForeignSlug`
  (a project-job run whose bucket would allow the read still refuses it),
  `TestDigestStepNeverLists` (the fake bucket fails any list call), `TestDigestStepNothingDueNoPR`,
  `TestDigestStepCatchesUpSevenDays`, `TestDigestStepCommitsAsRunner`,
  `TestDigestStepSkipsPeriodInOpenPR`, `TestDigestRunReviewScopeNotApplicable`,
  `TestDigestStepCountsDroppedRecords`.
- **Command.** `GOFLAGS=-p=2 go test ./internal/runner -run DigestStep -count=1 -timeout 30m`
- **Red step.** The runner refuses the unknown step.
- **Depends on.** T3.1, T2.2, T3.6.
- **Reviewer.** opus + mandatory independent review (a new read path inside the job).
- **Commit.** `runner: the digest step writes the due reports and skips implement`

**T3.6 — the launcher's manifest**
- **Files.** `internal/task/task.go` (`DigestRuns []string` `json:"digest_runs,omitempty"`, accepted
  only with a recipe whose first step is `digest`), `schemas/task.schema.json`, new
  `internal/cli/run_digest_manifest.go` and its test, `internal/cli/run.go` (one call where the task
  is assembled).
- **Interfaces.** On `fugaro run --recipe run-digest`, the launcher lists this repository's runs of
  the last 40 days (a month plus the seven-day catch-up; the runs bucket keeps 90), newest first,
  at most 5000, and writes their IDs into the task. It refuses a second `run-digest` launch for the
  repository while one is running (the same listing). Produces what T3.2 reads.
- **Tests.** `TestDigestManifestThisRepositoryOnly`, `TestDigestManifestBounded`,
  `TestDigestLaunchRefusedWhileOneRuns`,
  `TestDigestRunsRefusedWithoutDigestRecipe`, the task schema test.
- **Command.** `GOFLAGS=-p=2 go test ./internal/task ./internal/cli ./schemas -run 'DigestManifest|DigestRuns|Schema' -count=1 -timeout 30m`
- **Red step.** `digest_runs` is an unknown task field (strict decode).
- **Depends on.** T3.1. **Reviewer.** opus + mandatory independent review (it decides what a job
  reads).
- **Commit.** `run: a digest run's launcher names the records it may read`

**T3.3 — the report is not the model's to edit**
- **Files.** `internal/runner/digest.go` (in the finalize call T3.2 added: before the PR is pushed or marked ready, the run's diff
  against its base must be exactly the generated files under `docs/fugaro-reviews/`, byte for byte:
  a change to a report is reverted to the generated bytes, a file anywhere else is removed, and if
  either fails the PR stays a draft with the reason). A review's findings never reach a fix that
  edits files outside that path.
- **Tests.** `TestDigestFilesEditedByFixAreRestored`, `TestDigestRunAddsNoOtherFile` (a fix stage
  that writes `CLAUDE.md` and a skill file: both are gone from the PR),
  `TestDigestFilesRestoreFailureDrafts`.
- **Command.** `GOFLAGS=-p=2 go test ./internal/runner -run DigestFiles -count=1 -timeout 30m`
- **Red step.** A fix stage's edit to a report survives into the PR.
- **Depends on.** T3.2 (same file, later level). **Reviewer.** opus + mandatory independent review.
- **Commit.** `runner: the reports a digest run commits stay byte for byte the generator's`

**T3.4 — docs for the recipe**
- **Files.** `docs/recipes.md` (the catalog row, what the digest step is and is not),
  `docs/releases/` next notes (`### For operators`, per repository: schedule `fugaro run --recipe
  run-digest` yourself; each run costs one review stage).
- **Tests.** `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Depends on.** T3.1. **Reviewer.** standard.
- **Commit.** `docs(recipes): run-digest, a report run you schedule yourself`

**T3.5 — Checks 135, 136 and 139**
- **Files.** `docs/gcp-live-checklist.md` (two sections), `docs/coordinator/README.md` (register line).
- **OWNER-RUN.** **Check 135**, read-only on the bucket, on a **per-repository** job: the owner first
  grants themself `roles/iam.serviceAccountTokenCreator` on the job account (the same step the
  register's earlier impersonation checks take) and removes it at the end; impersonating the sandbox
  repository's job account, the owner reads one of its own run records by name
  (`gcloud storage cat gs://<runs-bucket>/runs/<slug>/<run-id>/result.json
  --impersonate-service-account=<job-account>`), which must succeed, and one of a sibling
  repository's the same way, which must be refused; then records, for U8, whether `gcloud storage ls`
  of the own prefix is allowed (the plan does not need it). A project job is out of the check: its
  reach is the owner's accepted one (G2). **Check 136**, after T3.3: in the sandbox repository, `fugaro run --recipe run-digest
  "periodic run review"`, then `fugaro runs ls --watch` until it settles, then read the PR: one file per due period, no address; the
  run count and dollars equal those of `fugaro runs ls --json --since 72h` filtered by finish date,
  and two sampled rows' stage figures equal `fugaro diagnose RUN --json`. **Check 139**, read-only, before T7.2 merges (U4): the owner reads an existing sandbox gateway
  run with a sub-agent (`fugaro logs RUN | grep 'model call'`) and confirms that the main session's
  lines carry an empty `agent_id`, each sub-agent a distinct one, and the background model's calls
  none. No agent runs any step.
- **Depends on.** T3.1 (the section names the recipe). **Reviewer.** standard.
- **Commit.** `docs(checks): 135, 136 and 139, the report run's read, its pull request, and what an agent ID is`

### Slice 4 — the practice (R2, R4, R6, R7, D327, D328)

**T4.1 — the `working` skill: periodic reviews**
- **Files.** `plugin/skills/working/SKILL.md` (the "Inspect the expensive runs daily" section
  becomes a short "Review your runs" section naming `fugaro runs digest` and the cadence), new
  `plugin/skills/working/reference/periodic-reviews.md` (the cadence, how to schedule it yourself —
  your cron, CI or agent routine running `fugaro runs digest` or launching the report run — how to
  read a report and its patterns, where to write a lesson, the R4 relaunch note, and the submission
  step), `plugin/routing_skill_test.go` or a new `plugin/periodic_reviews_skill_test.go`.
- **Tests.** The lint suite (`TestPluginSkillsLintClean`, size budgets); a new test that the skill
  names `fugaro runs digest` and never tells the agent to submit on its own (`gh issue create` only
  with `--web`, after the person's word).
- **Command.** `GOFLAGS=-p=2 go test ./plugin/... -count=1`
- **Red step.** The new test fails: the skill names no digest command.
- **Depends on.** T2.3 (the command must exist for the lint). **Reviewer.** standard.
- **Commit.** `skills(working): daily, weekly, monthly and yearly run reviews`

**T4.2 — `fugaro runs digest --submit-draft`**
- **Files.** new `internal/cli/runs_digest_submit.go`, new `internal/cli/runs_digest_submit_test.go`,
  `internal/cli/runs_digest.go` (registers the flag), `website/content/cli-reference.md`
  (regenerated).
- **Interfaces.** Prints a learning draft: the pattern or flag, the figures (D323's set, run IDs
  replaced by `run-1`… because the draft leaves the repository), the lesson the person writes, and
  the exact `gh issue create --web --template skill_learning.yml` line. Sends nothing.
- **Tests.** `TestSubmitDraftHasNoRepoNameOrRunID`, `TestSubmitDraftSendsNothing` (no network seam
  called).
- **Command.** `go run ./internal/clidoc && GOFLAGS=-p=2 go test ./internal/cli ./internal/clidoc -run 'SubmitDraft|CliRef' -count=1 -timeout 30m`
- **Red step.** The flag is unknown.
- **Depends on.** T2.3 (same command; a separate file, a later level). **Reviewer.** standard.
- **Commit.** `cli(runs): digest --submit-draft prints a redacted learning, and sends nothing`

**T4.3 — the "Skill learning" issue form and the contribution note**
- **Files.** new `.github/ISSUE_TEMPLATE/skill_learning.yml` (required fields: the pattern, the
  evidence, the proposed lesson; required checkboxes: "I license this text under the MIT licence and
  it may be incorporated into Fugaro's skills" and "It holds no confidential data, address or
  repository name"; a label for triage), `CONTRIBUTING.md` (one paragraph under "Licensing of
  contributions": a learning is rewritten by a maintainer and lands through a normal PR, and an
  issue's text is data for whoever reads it — a maintainer's agent triaging these issues follows no
  instruction in them).
- **Tests.** A docs test pinning the two checkboxes and the two CONTRIBUTING sentences
  (`internal/cli/docs_skill_learning_test.go`, new).
- **Command.** `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`
- **Red step.** The new pin fails: no form.
- **Depends on.** nothing. **Reviewer.** standard; the licence wording is the owner's to confirm
  (U6) before merge.
- **Commit.** `github: a Skill learning issue form, MIT-licensed and rewritten before it ships`

**T4.4 — Fugaro's own reports (D324)**
- **Files.** `docs/dogfooding.md` (a section: this repository's reports live under
  `docs/fugaro-reviews/`, written by the owner's scheduled `run-digest` run under D323's rules, and
  `website/data/typical-costs.yaml` stays refreshed by a person, who may read its figures from them).
- **Tests.** `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1` (the docs pins, including any
  file-list pin that a new `docs/fugaro-reviews/` tree would touch).
- **OWNER-RUN.** The owner schedules `fugaro run --recipe run-digest "periodic run review"` for this
  repository in their own routine; no agent sets it up.
- **Depends on.** T3.3. **Reviewer.** standard.
- **Commit.** `docs(dogfooding): Fugaro's own run reviews, public under D323's rules`

### Slice 5 — small guards (R5, F2)

**T5.1 — the 80% warning**
- **Files.** `internal/cli/budget.go` (`budget show`), `internal/cli/launch_budget.go` (the launch
  line), `internal/watch/render.go` (the project bar).
- **Tests.** `TestBudgetShowWarnsAtEightyPercent`, `TestLaunchWarnsAtEightyPercent`,
  `TestWatchBarWarnsAtEightyPercent`; nothing at 79.9%.
- **Command.** `GOFLAGS=-p=2 go test ./internal/cli ./internal/watch -run EightyPercent -count=1 -timeout 30m`
- **Red step.** No warning at 80%.
- **Depends on.** nothing. **Reviewer.** standard.
- **Commit.** `budget: warn at 80% of a daily cap`

**T5.2 — record the review-scope comparison**
- **Files.** `internal/runner/diffbase.go` (inside `narrowReviewRange`, which already runs right
  after implement: take implement's file set and compare it with the review range's files),
  `internal/runstore/runstore.go` (`ReviewScope{applicable, why_not, implement_files, review_files,
  outside []string capped}`), `schemas/result.schema.json`.
  The comparison applies only where the range was narrowed to the run's own commits, or equals them
  by construction. It is **not applicable**, recorded with the reason and never as a mismatch, when
  `narrowReviewRange` keeps the wide range by design (git older than 2.44, its own commits
  unreadable, no fewer commits), for a follow-up and a review run (they keep the pull request's
  range), and for a run that skipped implement (an autofix `Fixed` gate; the digest run, T3.2).
- **Tests.** `TestReviewScopeRecordsOutsideFiles`, `TestReviewScopeEqualRecordsNothingOutside`,
  `TestReviewScopeNotApplicableWhenRangeKeptWide` (old git, unreadable commits),
  `TestReviewScopeNotApplicableToFollowUpReviewAndAutofix`.
- **Command.** `GOFLAGS=-p=2 go test ./internal/runner ./internal/runstore ./schemas -run 'ReviewScope|Schema' -count=1 -timeout 30m`
- **Red step.** The record has no `review_scope`.
- **Depends on.** T1.1 (both edit `runstore.go`/the schema: a later level). **Reviewer.** standard.
- **Commit.** `runner: the record says what review read beyond what implement wrote`

### Slice 6 — checkpoints (D329)

**T6.1 — halt with a checkpoint report**
- **Files.** `internal/runstore/runstore.go` (`Checkpoint{trigger, done, why_over, next, estimate_usd,
  hard_instructions}` on `Record`; halt reasons `stage_budget`, `review_ratio`, `fanout`,
  `review_scope`, `expected_exceeded`), `schemas/result.schema.json`, `internal/runner/halt.go`
  (`haltReasonText` for each; one `governanceHalt` path that writes the report into the record and
  opens the draft), `internal/runner/prflow.go` (the checkpoint in the PR's status section),
  `internal/runner/prompts.go` (the short instruction the stopping agent answers to fill
  `done`/`next`/`hard_instructions`). The runner fills `trigger`, `why_over` and `estimate_usd` from
  the record itself; the agent's one answer is a single call through the gateway's checkpoint
  allowance (T7.2), so a run halted by the **per-run cap** or a daily cap gets no model call and those
  three fields stay empty; a failed call leaves them empty and never blocks the halt.
- **Tests.** `TestGovernanceHaltWritesCheckpoint`, `TestGovernanceHaltDraftsPR`,
  `TestCheckpointCallFailureStillHalts`, `TestResultSchemaHaltReasonsAgreeWithGo`.
- **Command.** `GOFLAGS=-p=2 go test ./internal/runner ./internal/runstore ./schemas -run 'Governance|Checkpoint|HaltReasons' -count=1 -timeout 30m`
- **Red step.** No governance halt reason exists; the schema test fails once the Go enum grows.
- **Depends on.** T5.2 (same files, later level). **Reviewer.** opus + mandatory independent
  review (it halts runs and writes the PR).
- **Commit.** `runner: a governance stop is a halt with a checkpoint report`

**T6.2 — read and continue a checkpoint**
- **Files.** `internal/cli/diagnose.go` (prints the checkpoint), `plugin/skills/working/reference/diagnose.md`
  (a row per new halt reason: Continue = a `--pr N` follow-up, Redirect = a follow-up with the
  corrective task, Stop = nothing, a second overrun = the person decides), `plugin/` tests.
- **Tests.** `TestDiagnosePrintsCheckpoint`; the lint suite.
- **Command.** `GOFLAGS=-p=2 go test ./internal/cli ./plugin/... -run 'Diagnose|Lint' -count=1 -timeout 30m`
- **Red step.** `diagnose` prints no checkpoint.
- **Depends on.** T6.1, T4.1 (both touch the working skill's tree: later level). **Reviewer.** standard.
- **Commit.** `diagnose: a checkpoint, and the three ways on from it`

### Slice 7 — stage budgets and fan-out (D331, D332)

**T7.1 — the configuration**
- **Files.** `internal/config/config.go`, `internal/config/policy.go`, `internal/config/validate.go`,
  `internal/config/layer.go` (the project layer), `internal/policy/merge.go` (the tighten-only
  merge, beside `tightenCap`), `internal/projectpolicy/policy.go` (the published project layer),
  `schemas/fugaro.schema.json`, `schemas/project-layer.schema.json`.
- **Keys and defaults.** `governance.review_ratio` (N, default 5) and `governance.review_floor_calls`
  (default 20); `governance.stage_budget.implement` (default 0.7 of the per-run cap),
  `.review` (default 0.5 of the cap, **first-line and senior review together**: `review_first` counts
  as review for the budget and the ratio), `.fix` (default 0.5 **of the implement stage's budget**,
  G§2's "a fraction of implementation budget"); `governance.stage_tokens.*` and
  `governance.stage_timeout.*` (optional, unset by default, the latter under `timeouts.stage`);
  `governance.max_subagents` (**unset by default: no fan-out stop until a value or a tier sets one**;
  T8.1 sets 3 for `trivial` and `small`, D332). An artifact run's `plan` and `render` stages have no
  governance budget. Project layer sets, repository may only tighten; no recipe key (D331).
- **Tests.** `TestGovernanceTightenOnly`, `TestGovernanceStrictDecode`, `TestGovernanceDefaults`
  (no stage default reaches the cap; no fan-out stop by default), schema tests.
- **Command.** `GOFLAGS=-p=2 go test ./internal/config ./internal/policy ./internal/projectpolicy ./schemas -run 'Governance|Schema' -count=1`
- **Red step.** Unknown key `governance`.
- **Depends on.** nothing. **Reviewer.** standard.
- **Commit.** `config: governance budgets, tighten-only`

**T7.2 — the gateway enforces them**
- **Files.** `internal/gateway/gateway.go` (`Stage` gains the limits; counters in the stage state),
  `internal/gateway/forward.go` (the refusal path the cap uses), `internal/gateway/lease.go` if the
  stage budget is a sub-lease, `internal/runner/gateway.go` (`beginGatewayStage` passes the
  resolved `governance.*` limits into `gateway.Stage`: the wiring from the config to the gateway).
- **Behaviour.** Per stage: refuse and raise a halt when the stage's dollars, calls or tokens pass
  its budget, when review calls pass max(floor, N × implement calls), or when the stage's distinct
  sub-agents pass the maximum. A governance halt refuses every later call of the stopped stage,
  in-flight sub-agents included. The checkpoint call (T6.1) is **not** an allowance any caller can
  spend: the runner first stops the agent process, then opens a dedicated `checkpoint` stage with
  `BeginStage`, which admits exactly one call, from a **fresh** session given the runner's own
  summary (bounded input, small `max_output_tokens`), reserved like any call and refused if the
  per-run cap has no room; never after a cap halt. Gateway runs only: an oauth run's governance
  halts (`review_scope`, a stage timeout) get no checkpoint call, and the runner-filled fields stand
  alone. Oauth runs are flagged by the digest.
- **Tests.** `TestStageBudgetHalts`, `TestStageTokensHalt`, `TestReviewRatioHaltsAboveFloorOnly`,
  `TestFanoutHalts`, `TestReviewOnlyRunHasNoRatio` (F6), `TestCheckpointAllowanceOneCallWithinCap`,
  `TestNoCheckpointAllowanceAfterCapHalt`, `TestCheckpointStageRefusesStoppedStageCalls`,
  `TestReviewFirstCountsAsReview`, `TestStageLimitsWiredFromConfig`.
- **Command.** `GOFLAGS=-p=2 go test ./internal/gateway ./internal/runner -run 'StageBudget|StageTokens|ReviewRatio|Fanout|CheckpointAllowance|StageLimits' -count=1 -timeout 30m`
- **Red step.** A stage over its budget is served.
- **Depends on.** T1.2, T7.1. **Reviewer.** opus + mandatory independent review (money path).
- **Commit.** `gateway: stage budgets, the review ratio and the fan-out stop`

**T7.3 — the runner turns gateway stops and the review-scope mismatch into checkpoints**
- **Files.** `internal/runner/gateway.go` (map the new gateway halts), `internal/runner/diffbase.go`
  (a non-empty `review_scope.outside` before review halts with `review_scope`; a not-applicable
  scope (T5.2) never halts).
- **Tests.** `TestGatewayStageHaltBecomesCheckpoint`, `TestReviewScopeMismatchHaltsBeforeReview`,
  `TestReviewScopeNotApplicableNeverHalts` (wide range kept by design, follow-up, review run,
  autofix `Fixed`; the digest run's case is T3.2's),
  `TestReportOnlyTripwiresDoNotHalt` (G8: a stage with the repeated-command flag of T1.4 runs on).
- **Command.** `GOFLAGS=-p=2 go test ./internal/runner -run 'StageHalt|ReviewScope|ReportOnly' -count=1 -timeout 30m`
- **Red step.** The gateway's stop ends the run as failed, not halted.
- **Depends on.** T6.1, T7.2, T1.4 (the flag the G8 test uses). **Reviewer.** opus + mandatory
  independent review.
- **Commit.** `runner: governance stops become checkpoints`

**T7.4 — operators and docs**
- **Files.** `docs/releases/` next notes (`### For operators`: the defaults now stop runs; how to
  loosen in the project layer, per installation), `docs/recipes.md` (budgets are not a recipe key),
  `plugin/skills/setup/reference/decisions.md` (the governance block).
- **Tests.** `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`; the plugin lint.
- **Depends on.** T7.3. **Reviewer.** standard.
- **Commit.** `docs: governance budgets, for operators`

**T7.5 — Checks 137 and 138**
- **Files.** `docs/gcp-live-checklist.md` (two sections), `docs/coordinator/README.md` (register
  line).
- **OWNER-RUN.** The owner runs, in the sandbox repository only: `fugaro run --task-file
  <a task file for a review-heavy change, under a project layer with a tiny review stage budget>`,
  waits for it to settle (`fugaro runs ls`), and `fugaro diagnose RUN`; then a task whose review is
  asked to fan out to four sub-agents, under `governance.max_subagents: 3` (no tier needed). No
  agent runs any step.
- **Depends on.** T7.3. **Reviewer.** standard.
- **Commit.** `docs(checks): 137 and 138, the stage budget and the fan-out stop`

**T7.6 — per-stage timeouts**
- **Files.** `internal/runner/runner.go` (a stage's own `governance.stage_timeout` under
  `timeouts.stage`; when it runs out, the stage stops through T6.1's `governanceHalt` with reason
  `stage_budget` and the detail "time", instead of failing the run).
- **Tests.** `TestStageTimeoutHaltsWithCheckpoint`, `TestStageTimeoutNeverAboveTimeoutsStage`.
- **Command.** `GOFLAGS=-p=2 go test ./internal/runner -run StageTimeout -count=1 -timeout 30m`
- **Red step.** A stage past its own timeout keeps running until `timeouts.stage`.
- **Depends on.** T6.1, T7.1. **Reviewer.** standard.
- **Commit.** `runner: a stage's own timeout is a checkpoint`

### Slice 8 — expectations (D330, F33)

**T8.1 — the tier on a task**
- **Files.** `internal/task/task.go` (`Tier string`, `ExpectedScope []string`),
  `schemas/task.schema.json`, `internal/cli/run.go` (`--tier`), `internal/runstore/runstore.go` and
  `schemas/result.schema.json` (recorded), `internal/config/defaults.go` (G§3's defaults: $0.25/10m, $1/30m, $3/1.5h, $8/4h),
  `internal/config/config.go` and `internal/policy/merge.go` (`governance.tiers.<tier>.{expected_usd,
  expected_minutes, max_subagents}` in the project layer, tighten-only per repository, D330; D332's
  "3 on trivial and small"), `schemas/fugaro.schema.json`, `schemas/project-layer.schema.json`,
  `website/content/cli-reference.md`.
- **Tests.** `TestTaskTierValidated`, `TestTierRecorded`, `TestTierNeverRaisesCap` (G6),
  `TestTierDefaultsFromProjectLayer`, `TestTierMaxSubagents`.
- **Command.** `go run ./internal/clidoc && GOFLAGS=-p=2 go test ./internal/task ./internal/cli ./internal/clidoc -run 'Tier|CliRef' -count=1 -timeout 30m`
- **Red step.** `--tier` unknown.
- **Depends on.** T7.1 (`internal/config`), T6.1 (`runstore.go`). **Reviewer.** standard.
- **Commit.** `task: an optional tier, an expectation and never a cap`

**T8.2 — the expected-cost checkpoint**
- **Files.** `internal/gateway/gateway.go` (a run-level expectation threshold beside T7.2's
  budgets), `internal/runner/gateway.go`.
- **Behaviour.** At 100% of the tier's expected cost the run halts with `expected_exceeded` and a
  checkpoint (D329); no tier, no threshold.
- **Tests.** `TestExpectedCostHalts`, `TestNoTierNoExpectation`.
- **Command.** `GOFLAGS=-p=2 go test ./internal/gateway ./internal/runner -run Expected -count=1 -timeout 30m`
- **Depends on.** T8.1, T7.3. **Reviewer.** opus + mandatory independent review.
- **Commit.** `gateway: a tier's expected cost is a checkpoint`

**T8.3 — self-calibration in the digest**
- **Files.** new `internal/digest/calibrate.go`, `internal/digest/calibrate_test.go`,
  `internal/digest/render.go` (the suggestions section), `internal/digest/digest.go` (the
  scope-drift flag: changed files outside the task's `ExpectedScope`, report only, G8).
- **Behaviour.** Per tier, over the last 20 runs or 14 days (whichever is fewer), the median
  actual/expected ratio for cost and duration; governance-halted runs left out; a weekly report
  prints suggested defaults within bounds (0.5×–2×). Nothing is applied (G9).
- **Tests.** `TestCalibrationExcludesGovernanceHalts`, `TestCalibrationBounds`,
  `TestCalibrationAppliesNothing`, `TestScopeDriftFlagOnly`.
- **Command.** `GOFLAGS=-p=2 go test ./internal/digest -run Calibrat -count=1`
- **Depends on.** T8.1, T2.2. **Reviewer.** standard.
- **Commit.** `digest: suggest tier defaults from the last twenty runs`

## 6. Parallel execution

### Edges (from the Depends on lines)

| Task | Depends on |
| --- | --- |
| T1.1, T1.2, T3.1, T4.3, T5.1, T7.1 | — |
| T1.3 | T1.1, T1.2 |
| T1.4 | T1.3, T2.1, T5.2, T6.1 |
| T2.1 | T1.1 |
| T2.2 | T2.1 |
| T2.3 | T2.1, T2.2 |
| T3.2 | T3.1, T2.2, T3.6 |
| T3.3 | T3.2 |
| T3.4, T3.5, T3.6 | T3.1 |
| T4.1, T4.2 | T2.3 |
| T4.4 | T3.3 |
| T5.2 | T1.1 |
| T6.1 | T5.2 |
| T6.2 | T6.1, T4.1 |
| T7.2 | T1.2, T7.1, T3.5 (Check 139 written and run) |
| T7.3 | T6.1, T7.2, T1.4 |
| T7.4, T7.5 | T7.3 |
| T7.6 | T6.1, T7.1 |
| T8.1 | T7.1, T6.1 |
| T8.2 | T8.1, T7.3 |
| T8.3 | T8.1, T2.2 |

### Levels (tasks in one level run concurrently)

| Level | Tasks | Note |
| --- | --- | --- |
| **L0** | T1.1, T1.2, T3.1, T4.3, T5.1, T7.1 | six independent starts |
| **L1** | T1.3, T2.1, T5.2, T3.4, T3.5, T3.6 | Checks 135 and 139 can be run by the owner once T3.5 merges |
| **L2** | T2.2, T6.1, T7.2 | T7.2 also waits for Check 139's answer |
| **L3** | T1.4, T2.3, T3.2 | T7.6 and T8.1 are ready here but wait a level: T7.6 shares `internal/runner/runner.go` with T3.2; T8.1 shares `internal/runstore/runstore.go` and `schemas/result.schema.json` with T1.4 and `website/content/cli-reference.md` with T2.3 |
| **L4** | T3.3, T4.1, T7.3, T7.6, T8.1 | T4.2 is ready here but waits a level: it shares `website/content/cli-reference.md` with T8.1 |
| **L5** | T4.2, T4.4, T6.2, T7.4, T7.5, T8.2, T8.3 | |

**The smallest useful first slice** (T1.1–T1.3, T2.1–T2.3, T4.1) is done at L4.

**Critical path (six levels, every edge a Depends-on line):** T1.1 → T5.2 → T6.1 → T1.4 → T7.3 → T8.2.
Two of its edges (T5.2 → T6.1, T6.1 → T1.4) only order writes to one file; the others are
interfaces.
What is sequential, and why: T1.1, T5.2, T6.1, T1.4 and T8.1 all write
`internal/runstore/runstore.go` and `schemas/result.schema.json`; T7.2 → T7.3 → T8.2 is the gateway
interface the runner consumes; T2.1 → T2.2 → T2.3 and T3.1 → T3.6 → T3.2 → T3.3 are build
dependencies.

### Shared files (re-derived from every task's Files line: no level has two writers of one file)

| File | Writers (level) |
| --- | --- |
| `internal/runstore/runstore.go`, `schemas/result.schema.json` | T1.1 (L0), T5.2 (L1), T6.1 (L2), T1.4 (L3), T8.1 (L4) |
| `internal/gateway/gateway.go` | T1.2 (L0), T7.2 (L2), T8.2 (L5) |
| `internal/gateway/forward.go`, `internal/gateway/lease.go` | T7.2 (L2) |
| `internal/runner/gateway.go` | T7.2 (L2), T7.3 (L4), T8.2 (L5) |
| `internal/runner/cost.go`, `internal/agent/relay.go`, `internal/agent/agent.go` | T1.3 (L1), T1.4 (L3) |
| `internal/runner/diffbase.go` | T5.2 (L1), T7.3 (L4) |
| `internal/runner/halt.go`, `internal/runner/prflow.go`, `internal/runner/prompts.go` | T6.1 (L2) |
| `internal/runner/runner.go` | T1.3 (L1), T3.2 (L3), T7.6 (L4) |
| `internal/runner/digest.go` (new) | T3.2 (L3), T3.3 (L4) |
| `internal/digest/digest.go` (new) | T2.1 (L1), T1.4 (L3), T8.3 (L5) |
| `internal/digest/render.go` (new) | T2.1 (L1), T8.3 (L5) |
| `internal/digest/patterns.go` (new) | T2.2 (L2) |
| `internal/digest/calibrate.go` (new) | T8.3 (L5) |
| `internal/cli/runs.go` | T2.3 (L3) |
| `internal/cli/runs_digest.go` (new) | T2.3 (L3), T4.2 (L5) |
| `internal/cli/runs_digest_submit.go` (new) | T4.2 (L5) |
| `internal/cli/run_digest_manifest.go` (new) | T3.6 (L1) |
| `internal/cli/run.go` | T3.6 (L1), T8.1 (L4) |
| `internal/task/task.go`, `schemas/task.schema.json` | T3.6 (L1), T8.1 (L4) |
| `internal/cli/diagnose.go` | T6.2 (L5) |
| `internal/cli/budget.go`, `internal/cli/launch_budget.go`, `internal/watch/render.go` | T5.1 (L0) |
| `website/content/cli-reference.md` (generated) | T2.3 (L3), T8.1 (L4), T4.2 (L5) |
| `internal/recipe/recipe.go`, `internal/recipe/catalog/run-digest.yaml` (new), `schemas/recipe.schema.json` | T3.1 (L0) |
| `internal/config/config.go`, `internal/policy/merge.go`, `schemas/fugaro.schema.json`, `schemas/project-layer.schema.json` | T7.1 (L0), T8.1 (L4) |
| `internal/config/policy.go`, `internal/config/validate.go`, `internal/config/layer.go`, `internal/projectpolicy/policy.go` | T7.1 (L0) |
| `internal/config/defaults.go` | T8.1 (L4) |
| `plugin/skills/working/SKILL.md`, `reference/periodic-reviews.md` (new), the new plugin test | T4.1 (L4) |
| `plugin/skills/working/reference/diagnose.md` | T6.2 (L5) |
| `plugin/skills/setup/reference/decisions.md` | T7.4 (L5) |
| `docs/recipes.md`, `docs/releases/<next>.md` | T3.4 (L1), T7.4 (L5) |
| `docs/gcp-live-checklist.md`, `docs/coordinator/README.md` | T3.5 (L1), T7.5 (L5) |
| `docs/dogfooding.md` | T4.4 (L5) |
| `.github/ISSUE_TEMPLATE/skill_learning.yml` (new), `CONTRIBUTING.md`, `internal/cli/docs_skill_learning_test.go` (new) | T4.3 (L0) |

A change to any task's **Files** re-checks this table.

## 7. Release actions

- Slice 3 (T3.4): per repository, the user schedules `fugaro run --recipe run-digest` themselves;
  each run costs one senior review stage. No installation step: the job's grant is unchanged.
- Slice 7 (T7.4): per installation, the governance defaults now stop runs; the project layer
  loosens them up to the per-run cap, and `fugaro update` publishes it.
- Every other slice changes no deployed resource.

## 8. Risks

- **U8 (listing under the conditional grant)** no longer blocks anything: the job never lists (T3.6's
  manifest); Check 135 records the answer for the record.
- **Project jobs** do not isolate repositories in the bucket (G2); the digest step's slug check is
  the only guard there, and it is tested (T3.2).
- **Report-run cost** (D320): a daily run's review stage is a model cost per repository per day;
  the digest of those runs shows it, and the owner may revisit D320 with that figure.
- **False halts** from stage budgets (T7.2) on legitimately large tasks: the defaults (T7.1:
  implement 0.7 and review 0.5 of the per-run cap, fix 0.5 of implement's budget, no fan-out stop
  without a value or a tier) leave no stage able to spend the whole cap alone, the checkpoint keeps the work on a draft, and Checks 137–138 exercise them.
