# Periodic run reviews: implementation plan

**Status: design answered by the owner 2026-11-04 and ruled on again 2026-11-05; plan only; nothing
implemented; no task approved.**

Every task is written to the owner's answers of 2026-11-04 (D960–D971,
[reconciliation.md](reconciliation.md) §3) **and to the owner's rulings of 2026-11-05**
([reconciliation.md](reconciliation.md) §5, E1–E8), which replace the in-repo writing with a
project-level reports job and an opt-in commit. **The rulings did not change the three answers the
owner gave in words**; D965 keeps its answer (the user installs their repository's cadence) and the
one schedule the rulings add is Fugaro's own project job, paused (E4). Each answer with its why:

- **D965 — the user installs the cadence for their repository; Fugaro writes no workflow and no
  crontab into a repository it does not own.** *(why: such a schedule would commit into a repository
  Fugaro does not own, under the user's identity)* — R3, and unchanged by the rulings.
- **D968 — yes, one longer window, `cost_per_run_rising` over five months.** *(why: the four rules
  see at most four weeks and one month against the month before)* — R4.
- **D969 — the report keeps its run IDs and the draft renumbers them.** *(why: the report's reader
  acts on a flagged row with `fugaro diagnose RUN`, and the draft's reader is a public issue)* — no
  task: §4 of the spec states what already holds.
- **The 2026-11-05 rulings, and what each one moves here:** E1 (the reports move to the bucket) —
  **R7, R8, R10**; E2 (D322/G2 is a default, not a prohibition) — R8, R9; E3 (18 months on
  `reports/`) — R7; E4 (one project-level writer) — R7; E5 (committing is opt-in and separate) —
  **R9**; E6 (the designated repo or folder is a separate design item) — R9 depends on it, **Open
  items**; E7 (the rules read the stored reports) — R4 and R7; E8 (`fugaro runs digest` generates to
  the bucket) — **R10**.

The other nine decisions of 2026-11-04 were not singled out and the recommendation stands for each.
What the 2026-11-04 refresh *does* change is in [reconciliation.md](reconciliation.md) §4: a class for
R2's check (M9), a sentence in the spec's §9 (M1, M2), the sequencing behind `plugin/**` (M6) and the
release slot (M8). Each task touches at most 12 files, names its tests, starts with a red step and
ends at the gates below.

## Order, and what this plan does not own

- **The loop itself is shipped** (F1–F13). No task here rewrites the report's content, the recipe, the
  runner's step, the layout, the flags, the draft or the issue form. Two tasks do change where a
  report is written: **R7 adds the writer the rulings ask for** beside the shipped one, and **R10
  changes `fugaro runs digest`'s default target** (E8) without touching what it writes.
- **`plugin/**` is owned, and it is being renamed** by the `docs/plugin-user-scope` design (PR
  #1488, which moves the skills to user scope and rewrites the setup skill's session-start
  behaviour). **R3, R4's rule list and R6's §7 sentence touch it, so all three are sequenced after
  #1488 lands**: the same file has to be edited once, at the path it will have. R3 also waits for R4,
  because the skill's §3 lists the rules and R4 is what adds the fifth, and #1488's rename is what
  R3's own citations follow.
  `fugaro upgrade`'s `pin` step is what keeps the skill and the flag in one release
  ([reconciliation.md](reconciliation.md) §4, M6).
- **`website/**` is owned**, including `website/data/typical-costs.yaml`. R5 names the one line its
  provenance needs and changes nothing there.
- **`internal/runner/runner.go`, `internal/cli/root.go`, `internal/cli/api.go` and everything under
  `ee/`** are owned or off-limits: no task touches them. R1 adds a flag to an existing subcommand, so
  it needs no registration in `root.go`; R7 and R8 register `runs reports` beside its siblings in
  `internal/cli/runs.go`, which no other work stream holds.
- **`deploy/terraform/gcp/modules/installation/`** is edited by R7 alone (the reports job, its
  account, its schedule and the `reports/` lifecycle rule). No other task in this plan touches
  Terraform, so the module has one writer.
- **`docs/gcp-live-checklist.md` is not touched by any task**: the register is another work stream's
  file, the design takes no check number (README, "Numbers this work holds"), and R7's live check is
  **named**, not numbered — the task that writes it takes the number free at its merge
  ([reconciliation.md](reconciliation.md) §5, "What the rulings take").

| Level | Tasks | Needs |
| --- | --- | --- |
| 0 | R0 | nothing |
| 1 | R1 | R0 |
| 2 | R2, R4 | R1 |
| 3 | R3, R7 | R3: R4 (the skill's rule list names the rules R4 adds) and #1488 landed; R7: R0 and R4 (the rules' source, E7) |
| 4 | R8, R10 | R8: R7 (the job's own loop); R10: R7 (the bucket writer) and R2 (the same file, once R2 has finished with it) |
| 5 | R6, R9 | R6: R1–R4 and R7, and #1488; R9: R7 **and the designated-repo design item (E6, Open items)** |
| — | R5 | the owner, at their terminal; no agent step |

**No level holds two writers of one file.** The two rows that moved are why: R4 (level 2) adds the
fifth rule to `internal/digest/patterns.go`, and R7 (level 3) changes where the summaries it reads
come from — the same file, one level apart; and R10 (level 4) edits `internal/cli/runs_digest.go`
after R1 and R2 (levels 1 and 2) have finished with it. The shared-files table below is the check.

## Gates (every task)

`go build ./... && gofmt -l . && go vet ./...` (gofmt prints nothing); the task's named tests with
`GOFLAGS=-p=2 go test ./internal/<pkg>/ -run '<names>' -count=1`; `GOFLAGS=-p=2 go test
./internal/cli/ -run Docs -count=1 -timeout 40m` when a doc changed; `go test
./internal/eeboundary/... -count=1`. Never the whole `internal/cli` or `internal/runner` package.
**R7 adds one**: `go test -tags terraform ./internal/infra/... -run '<names>' -count=1`, because the
job, its account, its schedule and the `reports/` lifecycle rule are Terraform the update plans, and
the convergence test is a Terraform-tagged one.

## Shared files

| File | Tasks | Why they meet there |
| --- | --- | --- |
| `internal/digest/due.go` (new) | R0, R1 | R0 writes it; R1 reads it |
| `internal/runner/digest.go` | R0 | the constants and the function move out of it |
| `internal/cli/runs_digest.go` | R1, R2, R10 | R1 adds `--due`; R2 calls the same reading for doctor; R10 changes the default target (E8) |
| `internal/digest/patterns.go` | R4, R7 | R4 adds the fifth rule; R7 changes where the summaries it reads come from (E7) |
| `internal/digest/store.go` (new) | R7, R8, R10 | R7 writes the bucket-backed reader; R8 adds the aggregate's writer; R10 is the command's default path into it |
| `internal/cli/runs_reports.go` (new) | R7, R8 | R7 writes the job's loop over the repositories; R8 writes the aggregate after it |
| `internal/cli/runs.go` | R7 | the one line that registers `runs reports` beside `runs digest` |
| `internal/infra/tfvars.go`, `internal/infra/testdata/installation-*.tfvars.json` | R7 | the new `reports` node and the fixtures that carry every installation shape |
| `deploy/terraform/gcp/modules/installation/bucket.tf`, `variables.tf`, `roots/installation/` | R7 | the `reports/` lifecycle rule and the job's variable |
| `plugin/skills/working/reference/periodic-reviews.md` | R3, R6 | R3 adds the due signal, the schedule and the rule list; R6 adds §7's sentences |
| `docs/recipes.md` | R1, R6, R10 | the `--due` pointer, the checklist line, and the sentence E8 changes (R10) |
| `docs/periodic-reviews/README.md` | R6 | the status line, once a task lands |

## R0. The due computation moves into `internal/digest`

One implementation of "which periods has this repository closed and not reported", read by a report
run and by a CLI read (F6, F17). Nothing about the runner's behaviour changes: the same days, the
same weekday and month-boundary rules, the same refusal of a period the layout cannot name.

- **Interface:** `func DuePeriods(now time.Time) []Period` in `internal/digest/due.go`, with the
  catch-up window `DueCatchUpDays = 7` — the same value `internal/runner`'s `digestCatchUpDays`
  holds today (F6), moved rather than duplicated.
- **Files (4):** `internal/digest/due.go` (new), `internal/digest/due_test.go` (new),
  `internal/runner/digest.go` (the call site; the constant and the function go — `digestCatchUpDays`
  `:70`, `digestDuePeriods` `:425`), `internal/runner/digest_test.go` (its existing due-period test
  moves and keeps its cases).
- **Red step:** `TestDuePeriodsIsTheClosedWindow` fails while `digest.DuePeriods` does not exist; the
  runner's own test is red until the call site moves, which is the point — one implementation.
- **Tests:** `TestDuePeriodsIsTheClosedWindow` (seven closed days, in order),
  `TestDuePeriodsAddsTheWeekOnAMonday`, `TestDuePeriodsAddsTheMonthAndYearOnTheFirst`,
  `TestDuePeriodsSkipsADateItCannotName`.
- **Hermetic:** a fixed clock, no file, no cloud.

## R1. `fugaro runs digest --due [--json]` (D962)

Read-only and offline: it reads the committed folder in the checkout it runs in and never opens the
cloud. `--period`, `--date`, `--repo`, `--stdout` and `--submit-draft` are refused beside it, each by
name; `--dir` and `--json` are accepted. It writes nothing and it prints no report.

- **Output:** the human form names the periods that are due (`2026-10-25 (day)`, …), the newest
  report the folder holds and how many days old it is, and, when nothing is due, one line saying so.
  `--json` prints one document, `{"version": 1, "dir": "docs/fugaro-reviews", "due": [{"dim", "key"}],
  "newest": {"key", "dim", "days_ago"} | null}` — members are only ever added within a version, and
  it has a JSON Schema in `schemas/` like the other wire formats (the rule the console's contract
  document states).
- **Files (5):** `internal/cli/runs_digest.go` (the flag, the refusal set and the read),
  `internal/cli/runs_digest_due_test.go` (new), `schemas/runs-digest-due.schema.json` (new),
  `schemas/schemas_test.go` (the schema loads and a fixture validates), `docs/recipes.md` (one
  sentence in the `run-digest` row: how to ask a checkout whether its reports are due).
- **Red step:** `TestRunsDigestDueNamesTheMissingPeriods` fails while the flag does not exist (the
  command exits with `unknown flag: --due`), and `TestRunsDigestDueIsOffline` — the cloud seams panic
  if reached — fails against an implementation that opens the runs bucket for the repository's name.
- **Tests:** `TestRunsDigestDueNamesTheMissingPeriods`, `TestRunsDigestDueOnAnEmptyFolder`,
  `TestRunsDigestDueSaysTheNewestReportAge`, `TestRunsDigestDueWithNothingDue`,
  `TestRunsDigestDueIgnoresAFileThatIsNotAReport`, `TestRunsDigestDueIsOffline`,
  `TestRunsDigestDueRefusesTheReportFlags`, `TestRunsDigestDueJSONMatchesSchema`.
- **Hermetic:** `t.TempDir()` with a hand-written folder, the package's own clock variable
  (`digestNow`, `internal/cli/runs_digest.go:50`) replaced, no cloud.
- **SEC:** none. It reads a folder in the checkout and prints what is missing from it; it opens no
  credential and reaches nothing.
- **The 2026-11-05 rulings re-open one thing about this task and change nothing in it:** the reports
  the signal counts sit in the bucket by default now (E8), so whether `--due` keeps reading the
  checkout's folder (this task as written), reads the bucket too, or reports the two separately is an
  **Open item**, not decided here. The task as written stays writable and useful either way: it is
  the offline reading, and a person who writes with `--dir` (E8) is exactly its reader.

## R2. The `doctor` line (D963)

`reviews-due`, from the same reading as R1: an info line when the newest report the checkout holds is
more than one period old, a warning line when a period has closed and has no report at all, and
silence when the checkout holds no report folder and no report is due (a repository that has never
reviewed is told once, not on every run). Offline.

**The check names its class, and that is part of the task** ([reconciliation.md](reconciliation.md)
§4, M9). `doctor` prints the `actionable`, `update`, `decision` and `security` classes, counts the
measurements without printing them, and files every unclassified info check under `audit` — written,
counted in the pointer line and never shown. So the warning is `doctorClassActionable` and the info
line is `doctorClassDecision` (`internal/cli/doctor_render.go:29-60`), and a task that stops at "it
prints a line" ships a line nobody sees. Neither class is the update group or the security group, so
`doctor_render.go` itself is unchanged.

- **Files (3):** `internal/cli/doctor_reviews.go` (new: the check, its class and its one-line fix),
  `internal/cli/doctor_reviews_test.go` (new), `internal/cli/runs_digest.go` (only if the reading
  needs a second entry point to be callable from doctor; search before writing one).
- **Red step:** `TestDoctorReviewsDueWarnsWhenAClosedPeriodHasNoReport` fails while the check does
  not exist, and it fails for the reason the task exists: today a user runs `doctor`, sees a clean
  screen and never learns that the cadence lapsed. `TestDoctorReviewsDueIsInTheDefaultView` is the
  second red: a check with no class is in `audit`, so the line is written, counted and not printed.
- **Tests:** `TestDoctorReviewsDueWarnsWhenAClosedPeriodHasNoReport`,
  `TestDoctorReviewsDueIsInfoWhenTheNewestIsOld`, `TestDoctorReviewsDueIsSilentWhenNothingIsDue`,
  `TestDoctorReviewsDueIsInTheDefaultView`, `TestDoctorReviewsDueIsOffline`,
  `TestDoctorReviewsDueNamesTheCommand`.
- **SEC:** none.

## R3. The skills carry the signal and the schedule (D965). **Owned elsewhere, and renamed by #1488**

D965 is **answered by the owner on 2026-11-04 as recommended: the user installs the cadence, and
Fugaro writes no workflow and no crontab.** **The rulings of 2026-11-05 do not reopen it** (E4): the
one schedule they add is Fugaro's own project job (R7), in the project Fugaro manages, with no git
credential and nothing to commit, which ships paused. The `setup` skill gains one step: it prints the
three ways to schedule the reviews — a cron job or CI workflow of the repository, an agent routine, a
scheduled report run — with the exact command for each, and writes nothing without the user's word.
The `working` skill's reference gains the due signal (R1) where it describes the cadence, the fifth
rule in §3's list (R4), and — after the rulings of 2026-11-05 (**E1, E7, E8**) — the sentence that
says where a report is written now: the project's runs bucket, with `--dir` for a checkout and the
opt-in commit as the way one reaches a repository. It keeps the sentence that an agent never sets up
a schedule on the user's behalf — which §2 of that reference already states in full ("Fugaro schedules
nothing. The cadence is the user's own") — so **this task adds the signal, the rule's name, the
report's place and the setup step, not a new rule about who schedules.**

- **Files (4):** `plugin/skills/setup/SKILL.md`, `plugin/skills/working/SKILL.md`,
  `plugin/skills/working/reference/periodic-reviews.md`, `plugin/periodic_reviews_skill_test.go`
  (the pointer stays; the new sentences are pinned: the schedule is printed, never written, the due
  signal is named with its command, and the report's place is named as the bucket).
- **Red step:** the new plugin assertion fails while the setup skill names no review cadence — which
  is the state of `main` today (F18).
- **Tests:** the plugin package's own tests, run as `GOFLAGS=-p=2 go test ./plugin/ -count=1`, plus
  `pluginskillcheck`'s CLI-coverage test: every command and flag a skill names must exist, which is
  what keeps `--due` and the skill's sentence from drifting apart.
- **SEC:** none.

## R4. The fifth pattern rule (D968, answered by the owner 2026-11-04 as recommended: yes)

`cost_per_run_rising`: the billed cost per run rose in each of the last five committed monthly
reports. Same shape as the four (`RuleNames` order, period keys read oldest first, one figure per
period as an exact decimal string), same vocabulary, and a report that fires it still says only what
it read — a signal to look, never a verdict.

- **Files (2):** `internal/digest/patterns.go` (the rule) and `internal/digest/patterns_test.go`.
  **The skill's §3 rule list is R3's, one level above**: the same file cannot have two writers in one
  level, and the list is a sentence about the rules rather than a rule.
- **Red step:** `TestPatternCostPerRunRisingNeedsFiveMonthlyReports` fails while the rule does not
  exist; the boundary cases are what the task is for — a flat month breaks the run, a missing month
  is not a zero, and a period key the package did not write is in no series.
- **Tests:** `TestPatternCostPerRunRisingNeedsFiveMonthlyReports`,
  `TestPatternCostPerRunRisingStopsAtAFlatMonth`,
  `TestPatternCostPerRunRisingIgnoresAPeriodItDidNotWrite`,
  `TestPatternRuleOrderIsStable` (the JSON and the Markdown list the rules in `RuleNames` order).
- **Hermetic:** summaries written in the test, no folder, no cloud.
- **SEC:** none.
- **The 2026-11-05 rulings move this task's source (E7) and not its rule.** The rule itself is
  written here; **R7 is where the summaries it reads come from change** — the stored reports in the
  bucket, and the committed ones if they are there — which is why R7 sits a level above and edits the
  same file, once.

## R5. Fugaro's own reports (D961). **The owner's, at their terminal**

No agent step and no test: this is the one thing the design asks the owner to do, and it is the
registered Check 136, which is **NOT RUN** (`docs/gcp-live-checklist.md:764`, its section `:10621`).
Run once against a release carrying the finalize guard, on a sandbox repository first
(`fugaro run --recipe run-digest "periodic run review"`), then against this repository under D323's
rules. **Amended after the owner's ruling of 2026-11-05 (§5, E1/E5):** the reports this produces land
in this installation's `reports/` prefix, not in the tree, and they reach this public repository only
as the **opt-in commit run** (R9) — which needs this repository to be the project's designated one
(Open items). What follows from it is that pull request, and — separately, owned by whoever holds
`website/**` — the one-line change to `website/data/typical-costs.yaml` naming the stored reports as
the figures' source, which D324 claims and which is not yet true (F14).

The design takes **no check number**: check numbers live in the register
(`docs/gcp-live-checklist.md`), which this work does not own, and every behaviour R0–R4, R8, R9 and
R10 adds is a local read or a local write that a hermetic test covers. **R7 is the one task whose
behaviour cannot be covered hermetically**, so it names a live check (R7, "The live check, named") and
that check takes the number free at the merge of the task that writes it.

## R6. The learnings' two ends, documented (D970, D971)

Two sentences, in the places a person meets them: a learning that needs product work is filed by hand
in `docs/backlog/` (`filed-in: hand`, the issue under `## Links`); a learning that is a wording change
is landed by a maintainer as a pull request against `plugin/skills/` in the same release, and the
release notes name it.

- **Files (3):** `CONTRIBUTING.md` (the paragraph at `:67` gains both sentences),
  `docs/periodic-reviews/README.md` (the status line: which tasks landed),
  `plugin/skills/working/reference/periodic-reviews.md` (§7, with R3).
- **Red step:** none of its own; it is a documentation task, and its pin is the Docs gate below.
  **Said plainly:** this task's "test" is the docs pin gate, not a new test, and no test is added for
  two sentences that state a rule people follow.
- **Tests:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs|Backlog' -count=1 -timeout 40m` — the
  backlog folder's own pins must still hold, and `CONTRIBUTING.md` is read by the Docs tests.

## R7. The reports job: one project-level writer into the bucket (E1, E3, E4, E7)

The rulings' centrepiece, and the first task in this plan that touches the cloud. One job in the
project, on the 0.18.0 catalog check job's pattern
(`deploy/terraform/gcp/modules/installation/catalogcheck.tf`, `internal/infra/catalogcheck.go`): its
own narrowly granted account, **shipped paused**, planned by `fugaro update`. Nothing about it is a
model call and nothing about it is a commit.

- **What it is.** The job is `fugarorpt-reports` and **its name must start with `fugarorpt-`**, for
  the reason `fugarochk-` exists: `fugaro runs ls` and every listing count the `fugaro-` ones as
  workflow jobs, and the alert policies match the prefix (`internal/infra/catalogcheck.go:44`,
  `alert.tf:55`). The account is `fugaro-reports`, the Scheduler job `fugaro-reports` in the
  installation's region, and **`paused` defaults to `true`** exactly as the catalog job's does
  (`internal/infra/spec.go:340`).
- **Its command:** `fugaro runs reports --job`, registered beside its sibling in
  `internal/cli/runs.go:232` (not `root.go`, which another work stream holds). It reads its whole
  assignment from **`FUGARO_REPORTS_JOB_SPEC`** — a versioned JSON document on the
  `FUGARO_CATALOG_CHECK_SPEC` shape (`internal/infra/catalogcheck.go:23`): the project, the bucket,
  the repository slugs and the periods to close — and **refuses to start without it**. The release's
  own `*Since` constant (`internal/cli/recipes_skew.go:303` is `digestSince`'s) gains the release that
  ships it, so a skill or a recipe that names `runs reports` cannot reach a CLI without it.
- **What it writes.** For each closed period it loops over the project's repositories and writes
  `reports/<slug>/<YYYY>/<key>.md`, then the aggregate (R8) at `reports/<YYYY>/<key>.md`. **A report
  key is the period**; the bytes are `internal/digest`'s own writer, so F2's byte-identical rule holds
  across writers. **A rerun compares before it writes**: present and equal means skip, different means
  overwrite, which is what makes a re-fired schedule harmless.
- **IAM, exactly (E4).** Read on the run records (`runs/` of the project's bucket, under the same
  condition the project job's own read carries) and **write on `reports/` only** — object create and
  delete under a condition naming that prefix, and no bucket-level role. **No git credential, no
  model credential, no secret**: the account holds no project role, the job mounts no secret, and its
  env carries the platform variables and `FUGARO_REPORTS_JOB_SPEC` and nothing else. Whatever the
  aggregate needs beyond that is Open items, not a grant invented here.
- **The retention rule (E3).** `bucket_lifecycle` gains **`reports_days = 548`** (18 months, in whole
  days as its members are — `internal/infra/tfvars.go:757`) and `bucket.tf` a fourth
  `lifecycle_rule` matching `reports/`; `runs/` keeps its 90 (`F24`, untouched).
- **`fugaro update` plans it (HARD RULE).** `internal/infra/reportsjob.go` (the spec type, the env
  document and its version) and the `reports` node in `internal/infra/tfvars.go`; the module's
  `reports.tf` and its `variables.tf` entry; the pass-through in `roots/installation/`. An
  installation at the release before this one **converges in one `fugaro update`** — the plan creates
  the job, its account, the Scheduler job and the bucket's fourth rule, and asks for the typed project
  name where it plans the reports grants, as the catalog check's own update does.
- **Files (12, the Terraform and fixture rows grouped):** `internal/cli/runs_reports.go` (new),
  `internal/cli/runs_reports_test.go` (new),
  `internal/cli/runs.go` (the registration line) + `internal/cli/recipes_skew.go` (the release's own
  `*Since` constant, beside `digestSince`), `internal/digest/store.go` (new: the summaries
  read from the stored reports, E7), `internal/digest/store_test.go` (new),
  `internal/digest/patterns.go` (the caller changes source, once), `internal/infra/reportsjob.go`
  (new), `internal/infra/reportsjob_test.go` (new), `internal/infra/tfvars.go` (the node, the
  lifecycle member, and the fixtures under `internal/infra/testdata/` its test compares),
  `deploy/terraform/gcp/modules/installation/reports.tf` (new),
  `deploy/terraform/gcp/modules/installation/variables.tf` + `bucket.tf` + `alert.tf` (the variable,
  the lifecycle rule and the alert prefix), `deploy/terraform/gcp/roots/installation/variables.tf` +
  `main.tf`.
- **Red step:** `TestRunsReportsWritesEachRepositorysReportIntoTheBucket` fails while the subcommand
  does not exist (`unknown command "reports"`), and `TestRunsReportsRefusesToStartWithoutItsSpec`
  fails against an implementation that falls back to the checkout's own config — the job has no
  checkout and no local config, and a fallback would be a writer that writes somewhere nobody named.
- **Tests:** `TestRunsReportsWritesEachRepositorysReportIntoTheBucket`,
  `TestRunsReportsRefusesToStartWithoutItsSpec`, `TestRunsReportsSkipsAnIdenticalStoredReport`,
  `TestRunsReportsOverwritesAReportWhosePeriodChanged`,
  `TestRunsReportsWritesTheYearFromTheStoredMonths`, `TestPatternsReadTheStoredReports`,
  `TestUpdatePlansTheReportsJobPaused`, `TestBucketLifecycleCarriesReportsAtEighteenMonths`,
  `TestReportsJobAccountHoldsNoSecretAndNoProjectRole`,
  `TestReportsJobNameIsNotAWorkflowJob`, and the HARD RULE's own convergence test —
  **`TestInstallationAtThePreviousVersionConvergesToTheReportsJob`** (`-tags terraform`, the shape of
  `internal/infra/retire_test.go:1939`): an installation at the previous version, with the tfvars and
  the stored state that release wrote, converges in one `fugaro update`, with the job created paused
  and no second command, no deletion and no manual repair.
- **Hermetic:** a `gcpfake` bucket and a fake clock for the command; the Terraform-tagged tests run
  the real plan against the module's own fixtures.
- **Reviewer:** opus + mandatory independent review (an IAM grant, a bucket lifecycle rule and a
  credential-less job: the three things this plan lets near a project's cloud, and the account is
  narrower than any other job's).
- **The live check, named and not numbered.** It is the owner's, no agent runs any step of it, and it
  takes the number free at the merge of the task that writes it (§5's closing note, "What the rulings
  take, and what they leave", the register's rule for a work that reserved none —
  `docs/run-limit-carry-over/README.md:55`): on the sandbox
  installation, the owner runs `fugaro update` (it plans the reports job paused), enables the
  schedule — `gcloud scheduler jobs resume fugaro-reports --location <region>` — waits for one firing
  and reads, with `gcloud storage ls gs://<bucket>/reports/**`, one stored report per repository for
  the closed period plus the aggregate, byte-identical to what `fugaro runs digest` writes for the
  same period. Nothing about the job is enabled by default and **nothing here is run by an agent**.

## R8. The project aggregate (E1, E2, E4)

One document beside the repository reports, written by the same job after them, in the same run: the
project's period, across every repository — which is the one place E2 relaxes D322/G2.

- **What it holds.** The project's runs, flags and dollars for the period, each repository's line,
  and the summary block the later aggregate reads; the figures come from the repositories' own summary
  blocks and from the budget database's day nodes, read as `fugaro runs summary` already reads them
  (`internal/cli/runs_summary.go`) — **Open items carries what the ruling's IAM sentence says about
  that second read**, and the task is written so that either answer is a source swap.
- **No pattern rules in the first slice.** The aggregate states the figures and names no rule and no
  pattern (E7): the rules read per-repository series, and a cross-repository series has no threshold
  anybody has chosen.
- **No person, ever.** The aggregate names repositories, periods, recipes, stages and figures — the
  closed vocabulary of §4 — and carries no address, no task text and no log line (G1, D323). **What
  E2 relaxes is one thing only: it names every repository.**
- **Files (3):** `internal/cli/runs_reports.go` (the aggregate step, after the loop),
  `internal/cli/runs_reports_test.go`, `internal/digest/store.go` (the aggregate's own shape).
- **Red step:** `TestRunsReportsWritesTheProjectAggregateBesideTheRepositoryReports` fails while the
  job writes the repository reports and stops.
- **Tests:** `TestRunsReportsWritesTheProjectAggregateBesideTheRepositoryReports`,
  `TestTheAggregateNamesEveryRepositoryAndNoPerson`,
  `TestTheAggregateCarriesNoPatternRules`, `TestTheAggregateIsSkippedWhenItIsUnchanged`.
- **Hermetic:** a fake bucket, a fixed clock, hand-written summary blocks.
- **Reviewer:** opus + mandatory independent review (the one document that names every repository,
  written into the project's bucket).

## R9. The opt-in commit: reports into the designated repository or folder (E5). **Blocked**

One normal Fugaro run — the push path and the identity runs already have — pulls the stored reports
into the project's designated repository or folder and opens a pull request. Nothing else needs a git
credential, and **no code repository gets a report committed unless it is itself the designated
project repo**.

- **What the rulings fix about it:** the source (`reports/` in the project's bucket, read with the
  run's own credentials, not the job's), the target (the project's designated repository or folder —
  **E6, Open items**: no key is invented here), the shape (per-repository reports under per-repository
  subfolders, the aggregate beside them, the index regenerated over the folder the layout names), and
  the outcome (one pull request, the run's existing identity, no report run and no `digest` step of
  its own).
- **What is not written until the designated-repo design item lands:** the recipe's name,
  `fugaro.yaml` keys if any, and the file list. **The task is blocked on that design item, and this
  plan does not guess its shape** — which is why it takes no level before it and why its row in the
  level table is last.
- **Files (to be completed by that design item):** a recipe under `internal/recipe/catalog/` and its
  runner step — itself a `docs/periodic-reviews` decision the design item's files settle.
- **Reviewer:** opus + mandatory independent review (the only path in this design that writes into a
  repository under the user's identity).

## R10. `fugaro runs digest` generates to the bucket (E8)

The command keeps its report and changes its default target: **the bucket**, with `--dir` still
writing locally for a person who wants the files in a checkout.

- **What changes:** the default when `--dir` is absent (the stored report's path under `reports/`,
  through R7's writer), the command's `Short`, its `Long`, the header comment that states the
  "repository's own docs tree" default (`internal/cli/runs_digest.go:21`), and
  `docs/recipes.md`'s sentence about where a report lands. **What does not:** the layout, the four
  periods and the closed-period refusal, `--stdout`, `--json`, `--submit-draft`, the byte-identical
  rule between writers (F2), and **`fugaro runs digest` still commits nothing** (D964, E5).
- **Files (4):** `internal/cli/runs_digest.go`, `internal/cli/runs_digest_test.go` (the default
  target), `docs/recipes.md`, and `internal/digest/store.go` (the write path, if R7's reader needs a
  writer's twin).
- **Red step:** `TestRunsDigestWritesToTheBucketByDefault` fails while the command still writes into
  the checkout without `--dir`, and `TestRunsDigestStillWritesLocallyWithDir` fails if `--dir` is
  quietly dropped — the person's path is a feature, not a fallback.
- **Tests:** `TestRunsDigestWritesToTheBucketByDefault`,
  `TestRunsDigestStillWritesLocallyWithDir`, `TestRunsDigestNeverCommits` (the existing refusal,
  re-pinned), `TestRunsDigestJSONNamesTheStoredPath`.
- **SEC:** none of its own: it writes with the caller's own credentials to the project's bucket, the
  same read and write `runs ls` and `runs digest` already do.
- **Reviewer:** opus + mandatory independent review (it changes where a shipped command writes by
  default).

## Open items

Four questions the rulings raise and **this plan does not decide**, each one written where the task
that meets it says so:

1. **The project's designated repository or folder (E6, ruling 5).** The place a project may name for
   project-level material — the cross-repo backlog filing design already speaks of it
   (`docs/backlog/20261021-01-cross-repo-backlog-filing.md`, D29). **R9 depends on it.** No config key
   is invented for it here, and whether it is a repository plus a folder, whether it may be public,
   and who may set it are its own design's questions.
2. **The aggregate's second read (E4).** The ruling names the budget database's figures as the
   aggregate's second source and the account's IAM as "read on the run records and write on `reports/`
   only". Either the account gains a third, equally narrow read on the budget database's day nodes, or
   the first slice's aggregate is built from the repositories' summary blocks alone. **R8 is written
   so that either answer is a source swap.**
3. **The `--due` signal's source (E8, D962).** The reports live in the bucket by default now; whether
   the due signal keeps reading the checkout's folder, also reads the bucket (and so stops being
   offline, which is D962's own alternative and its cost), or reports the two separately is not
   decided. **R1 as written stays the offline reading.**
4. **The `run-digest` recipe (E1).** The shipped recipe writes the reports into the repository it was
   launched in (F5). The rulings do not name it; rerouting it through the bucket, or leaving it as a
   repository owner's own explicit choice, is a decision this plan does not take.

## Release slot

**CLI 0.19.0.** 0.15.0, 0.16.0 and 0.17.0 have all shipped since this design was written,
`docs/config-management`'s own plan is 0.18.0, and the rulings of 2026-11-05 make this a cloud
change, so **0.19.0 is still the first release this work can ride**
([reconciliation.md](reconciliation.md) §4, M8) — and it now has to carry the whole of R7, because a
schedule without the job that reads it is worse than no schedule. The release notes' `### Highlights`
carry the two sentences the rulings are: **the reports are written into your project's runs bucket,
and committing them anywhere is your choice** (`fugaro runs reports --job` behind a paused schedule,
and `fugaro runs digest` writing to the bucket by default).

**The release's `### For operators` entry, and it is the HARD RULE's whole upgrade** — two numbered
lines, and the second is a "when you want it", not a step:

1. "Upgrade the CLI and run `fugaro update` in each checkout: it brings the installation (Terraform,
   the reports job, its account, its paused schedule and the `reports/` lifecycle rule) and the
   checkout to this release. It asks for the typed project name where it plans the reports grants."
2. "**The reports job is created paused.** Enable its schedule when you want the periodic reports
   written: `gcloud scheduler jobs resume fugaro-reports --location <region>`. Nothing else is needed,
   and nothing is committed anywhere until you run the opt-in commit (R9)."

**One command, and no second step.** The reports job is `fugaro update`'s plan — an installation at
the previous version converges in that one run
(`TestInstallationAtThePreviousVersionConvergesToTheReportsJob`, R7), with the job created, the
account granted and the schedule stopped. Resuming a schedule the owner chose to have is not an
upgrade step: it is the same shape the 0.18.0 catalog check job's own operator line has ("the catalog
check job is created paused. Enable its schedule when you want nightly rebuilds"), and `fugaro
doctor`'s update reading is what tells the owner the update is due. The plugin pin — `fugaro upgrade`'s
`pin` step — is what puts the matching plugin and skills into a checkout
(`internal/cli/upgrade.go:80`, `:86`). That is exactly why **R1, R3 and R10 ship in one release**: a
skill that names `--due`, or a skill that says where a report lands, must not reach a CLI whose
behaviour differs, and the pin is what decides that. R3 and R6's §7 sentence are also sequenced behind
#1488's rename of `plugin/**`. **R9 waits for the designated-repo design item (E6)** and is the one
task here that is not gated by a release. R5 is the owner's whenever they run it.
