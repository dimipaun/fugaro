# Periodic run reviews: implementation plan

**Status: design answered by the owner 2026-11-04, ruled on again 2026-11-05, and its decisions
D1430–D1449 answered 2026-11-07 with all tasks approved 2026-11-07 (owner: "All tasks approved.
Jobmax").** **Plan only when this was written; part of Part I has landed since — R0–R4, R6 and R13,
each named with its commit in [README.md](README.md)'s status line — and the reports job has not.**

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
  **R12, R7, R11, R8, R10**; E2 (D322/G2 is a default, not a prohibition) — R8, R9; E3 (18 months on
  `reports/`) — **R12** (the module's rule) and **R7** (the Go member); E4 (one project-level writer)
  — **R12, R13, R7 and R11**; E5 (committing is opt-in and
  separate) — **R9**; E6 (the designated repo or folder is a separate design item) — R9 depends on
  it, **Open items**; E7 (the rules read the stored reports) — R4 and R11; E8 (`fugaro runs digest`
  generates to the bucket) — **R10**.
- **What the owner decided on 2026-11-05 after this round's review**, and where each answer is
  written: the job's listing is a narrow grant on `runs/`, verified first by an owner-run live check
  (**R12**, "The listing check"); `reports/` has **one writer, the job**, and launchers read it
  (**R12**, "IAM, exactly"; R9's and R10's SEC lines); enabling either job is **a setting `fugaro
  update` writes**, not a `gcloud` command (**R13**, "The jobs' paused setting", and the release
  slot). One question the
  answers leave is **Open items**: how `fugaro runs digest` writes to the bucket at all, now that
  the job is the only writer of `reports/`.

The other nine decisions of 2026-11-04 were not singled out and the recommendation stands for each.
What the 2026-11-04 refresh *does* change is in [reconciliation.md](reconciliation.md) §4: a class for
R2's check (M9), a sentence in the spec's §9 (M1, M2), the sequencing behind `plugin/**` (M6) and the
release slot (M8). Each task touches at most 12 files, names its tests, starts with a red step and
ends at the gates below.

## Order, and what this plan does not own

- **The loop itself is shipped** (F1–F13). No task here rewrites the report's content, the recipe, the
  runner's step, the layout, the flags, the draft or the issue form. Two tasks do change where a
  report is written: **R11 adds the writer the rulings ask for** beside the shipped one, and **R10
  changes `fugaro runs digest`'s default target** (E8) without touching what it writes.
- **The job is four tasks, not one**, re-cut twice by review. **R12** is what lives in Terraform and
  in the guard: the module's `reports.tf` (the job, its Scheduler job, its grants, its alert), the
  `reports/` lifecycle rule, the reports account's own principal kind, and the two-prefix bucket
  condition the grants carry. **R13** is the setting that turns either job on. **R7** is the Go plan
  `fugaro update` applies — the spec, the render and the five installation goldens it regenerates.
  **R11** is the job's code: the command, its loop over the repositories and the stored reader.
  They are split because the halves are gated differently — R12 is Terraform an owner-run check has
  to settle first, R7 is the plan that only runs once the module exists, R11 is code a hermetic test
  covers — and because **each cut was a file list over this plan's own cap of 12**: the split of the
  original R7 into R7/R11 was the first, and the always-present `reports` node regenerating five
  `internal/infra/testdata/installation-*.tfvars.json` goldens is five files by itself, which is what
  re-cut R7 a second time. **R12 is what can be built at once**; R7 waits for it and for R13, and R11
  waits for R7 and R4.
- **`plugin/**` is owned, and it is being renamed** by the `docs/plugin-user-scope` design (PR
  #1488, which moves the skills to user scope and rewrites the setup skill's session-start
  behaviour). **R3's rule list and R6's §7 sentence touch it, so both are sequenced after
  #1488 lands**: the same file has to be edited once, at the path it will have. R3 also waits for R4,
  because the skill's §3 lists the rules and R4 is what adds the fifth, and #1488's rename is what
  R3's own citations follow.
  `fugaro upgrade`'s `pin` step is what keeps the skill and the flag in one release
  ([reconciliation.md](reconciliation.md) §4, M6).
- **`website/**` is owned**, including `website/data/typical-costs.yaml`. R5 names the one line its
  provenance needs and changes nothing there.
- **`internal/runner/runner.go`, `internal/cli/root.go`, `internal/cli/api.go` and everything under
  `ee/`** are owned or off-limits: no task touches them. R1 adds a flag to an existing subcommand, so
  it needs no registration in `root.go`; R11 registers `runs reports` beside its siblings in
  `internal/cli/runs.go`, which no other work stream holds.
- **`deploy/terraform/gcp/modules/installation/`** is edited by R12 alone (the reports job, its
  account, its schedule, its alert and the `reports/` lifecycle rule), and the roots by R7. No other
  task in this plan touches Terraform, so each of the two has one writer.
- **`docs/gcp-live-checklist.md` is not touched by any task**: the register is another work stream's
  file, the design takes no check number (README, "Numbers this work holds"), and the two live
  checks (R12's listing check and R7's own) are **named**, not numbered — the task that writes each
  takes the number free at its merge
  ([reconciliation.md](reconciliation.md) §5, "What the rulings take"). **Amended 2026-11-07:** the
  file is **touched**, by Check 340 alone — the task that implemented it (§P8) wrote the register's
  row and its `## Check 340` section, so R12's listing check has a number and a section now and R7's
  own stays **named** until its task takes the number free at its merge
  ([reconciliation.md](reconciliation.md) §5's amendment; the `Checks 340–349` row of
  [README.md](README.md)).

| Level | Tasks | Needs |
| --- | --- | --- |
| 0 | R0, R12, R13 | R0: nothing. R12: **the listing check's result** (its own owner-run live check, before the grants are written) — and nothing in this plan. R13: nothing |
| 1 | R1, R7 | R1: R0. R7: R12 (the module, the conditions and the guard), R13 (the two `paused` settings) and, through R12, the listing check's result |
| 2 | R2, R4 | R1 |
| 3 | R3, R11 | R3: R4 (the skill's rule list names the rules R4 adds) and #1488 landed; R11: R7 (the job's spec and its own assignment) and R4 (the rules' source, E7) |
| 4 | R6, R8, R9 | R6: R1–R4, R7 and R12, and #1488; R8: R11 (the job's own loop); R9: R11 (what it stored) **and the designated-repo design item (E6, Open items)** |
| 5 | R10 | R8 (the writer it writes through), R1 and R2 (`internal/cli/runs_digest.go`) — and **its bucket write is blocked on Open item 5**, the way R9 is blocked on E6 |
| — | R5 | the owner, at their terminal; no agent step |

**No level holds two writers of one file**, and the shared-files table below is the check. Every file
two or more tasks touch is split across levels for exactly that reason: `internal/digest/due.go` (R0
writes it at 0, R1 reads it at 1), `internal/cli/runs_digest.go` (R1, R2 and R10 — levels 1, 2 and 5),
`internal/digest/patterns.go` and `internal/digest/patterns_test.go` (R4's rule at 2, R11's source
change at 3), `internal/digest/store.go` (R11 the reader, R8 the aggregate's writer, R10 the command's
own write path — levels 3, 4 and 5), `internal/cli/runs_reports.go` (R11 at 3, R8 at 4),
`internal/infra/tfvars.go` (R13 renders the setting at 0, R7 renders the `reports` node at 1),
`plugin/skills/working/reference/periodic-reviews.md` (R3 at 3, R6 at 4) and `docs/recipes.md` (R1 at
1, R10 at 5 — **R6 is not one of them**: its three files are `CONTRIBUTING.md`, the README's status
line and the skill's §7, and nothing else). **R10 is therefore alone at level 5**, and not at level 4
beside R8 or R6. The four-way split of the old R7 is the other half of the check: the Terraform
module, `internal/backend/gcp/names.go` and `internal/infra/tf/cover.go` are R12's alone, the
installation's own options and goldens are R7's alone, and the setting's files are R13's alone.

**Two chains are the longest, and both are named.** The first is **R0 → R1 → R4 → R11 → R8 → R10**,
and it is the rulings that lengthened it: before 2026-11-05 the longest chain was three levels
(R0 → R1 → R6). **R11 is what everything new waits on** — R8 writes the aggregate in its own loop, R9
commits what it stored, R10 makes the bucket the shipped command's default target through that writer
— so a schedule enabled before R11 lands has nothing to run, which is the other reason the whole of
R11 has to be in the release that ships the plan. The second is **R0 → R1 → R4 → R3 → R6**, one edge
shorter in the plan and **gated on a pull request this plan does not own (#1488)**, so in wall-clock
it can be the longer of the two; **it ends at R6**, which is a leaf — nothing in this plan depends on
it, and R10 does not wait on it. `R12 → R7 → R11` is the third chain and the shortest: it starts at
level 0, so it joins the first one at R11 rather than lengthening it.

## Gates (every task)

`go build ./... && gofmt -l . && go vet ./...` (gofmt prints nothing); the task's named tests with
`GOFLAGS=-p=2 go test ./internal/<pkg>/ -run '<names>' -count=1`; `GOFLAGS=-p=2 go test
./internal/cli/ -run Docs -count=1 -timeout 40m` when a doc changed; `go test
./internal/eeboundary/... -count=1`. Never the whole `internal/cli` or `internal/runner` package.
**R7 adds one**: `go test -tags terraform ./internal/infra/... -run '<names>' -count=1`, because the
job, its account, its schedule and the `reports/` lifecycle rule are Terraform the update plans, and
the convergence test is a Terraform-tagged one. R12's own Terraform is planned by that same tagged
run — the module has no test of its own that a hermetic test can hold — and `terraform fmt
-check -recursive deploy/terraform` is CI's first gate on it.

## Shared files

| File | Tasks | Why they meet there |
| --- | --- | --- |
| `internal/digest/due.go` (new) | R0, R1 | R0 writes it; R1 reads it |
| `internal/runner/digest.go` | R0 | the constants and the function move out of it |
| `internal/cli/runs_digest.go` | R1, R2, R10 | R1 adds `--due`; R2 calls the same reading for doctor; R10 changes the default target (E8) — levels 1, 2 and 5 |
| `internal/digest/patterns.go` | R4, R11 | R4 adds the fifth rule; R11 changes where the summaries it reads come from (E7) — levels 2 and 3 |
| `internal/digest/patterns_test.go` | R4, R11 | R4's rule cases; R11's `TestPatternsReadTheStoredReports` |
| `internal/digest/store.go` (new) | R11, R8, R10 | R11 writes the bucket-backed reader; R8 adds the aggregate's writer; R10 is the command's default path into it — levels 3, 4 and 5, one writer per level |
| `internal/cli/runs_reports.go` (new) | R11, R8 | R11 writes the job's loop over the repositories; R8 writes the aggregate after it — levels 3 and 4 |
| `internal/cli/runs.go` | R11 | the one line that registers `runs reports` beside `runs digest` |
| `internal/infra/tfvars.go` | R13, R7 | R13 renders the two `paused` settings into the installation's options; R7 renders the `reports` node and `bucket_lifecycle.reports_days` — levels 0 and 1 |
| `internal/infra/testdata/installation-accounts.tfvars.json`, `-budget-job`, `-budget`, `-coordinator`, `-credentials` | R7 | the five golden renders every installation shape carries: an always-present `reports` node regenerates all five |
| `internal/backend/gcp/names.go` | R12 | the two-prefix condition's one definition, and the titles the guard recognises (the file exists: the builder is added beside `ProjectBucketCondition`) |
| `internal/infra/tf/cover.go` | R12 | the reports account's own principal kind, and the grants it must refuse |
| `deploy/terraform/gcp/modules/installation/reports.tf`, `variables.tf`, `bucket.tf` | R12 | the job, its account, its Scheduler job, its grants, **its own alert** and the `reports/` lifecycle rule |
| `deploy/terraform/gcp/roots/installation/` | R7 | the `reports` variable and its pass-through to the module |
| `internal/config/layer.go`, `schemas/project-layer.schema.json`, `internal/settings/registry.go`, `docs/config-management/README.md`, `docs/config-management/reconciliation.md`, `internal/cli/docs_config_management_test.go` | R13 | the `jobs:` block, its rows, its schema and the three count pins that move — all level 0 |
| `plugin/skills/working/reference/periodic-reviews.md` | R3, R6 | R3 adds the due signal, the schedule and the rule list; R6 adds §7's sentences — levels 3 and 4 |
| `docs/recipes.md` | R1, R10 | the `--due` pointer and the checklist line (R1), and the sentence E8 changes (R10) — levels 1 and 5; **R6 does not touch it** |
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
one schedule they add is Fugaro's own project job (R12, R7), in the project Fugaro manages, with no git
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
  the lint's CLI-coverage rule — `plugin/skills_lint_test.go`'s `TestPluginSkillsLintClean` and
  `TestSkillFilesQuoteCommands`, which fail on a command or a flag a skill names that the CLI does not
  answer to, with the paid flags' own half in `ee/plugincheck/skills_test.go`'s
  `TestSkillsNamePaidFlagsThatExist` — which is what keeps `--due` and the skill's sentence from
  drifting apart.
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
  written here; **R11 is where the summaries it reads come from change** — the stored reports in the
  bucket, and the committed ones if they are there — which is why R11 sits a level above and edits
  the same file, once.

## R5. Fugaro's own reports (D961). **The owner's, at their terminal**

No agent step and no test: this is the one thing the design asks the owner to do, and it is the
registered Check 136, which is **NOT RUN** (`docs/gcp-live-checklist.md:785`, its section `:10643`).
Run once against a release carrying the finalize guard, on a sandbox repository first
(`fugaro run --recipe run-digest "periodic run review"`), then against this repository under D323's
rules. **Amended after the owner's ruling of 2026-11-05 (§5, E1/E5):** the reports this produces land
in this installation's `reports/` prefix, not in the tree, and they reach this public repository only
as the **opt-in commit run** (R9) — which needs this repository to be the project's designated one
(Open items). What follows from it is that pull request, and — separately, owned by whoever holds
`website/**` — the one-line change to `website/data/typical-costs.yaml` naming the stored reports as
the figures' source, which D324 claims and which is not yet true (F14).

The design takes **no check number**: check numbers live in the register
(`docs/gcp-live-checklist.md`), which this work does not own, and every behaviour R0–R4, R8, R9, R10
and R11 adds is a local read or a local write that a hermetic test covers. **R12 and R7 are the two
tasks whose behaviour cannot be covered hermetically**, so they name two live checks (R12, "The
listing check", and R7, "The job's own live check") and each takes the number free at the merge of the
task that writes it.

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

## R12. The reports job in Terraform, and its grants, exactly (E1, E3, E4)

The rulings' centrepiece, and the one piece this plan builds first. One job in the project, on the
0.18.0 catalog check job's pattern (`deploy/terraform/gcp/modules/installation/catalogcheck.tf`): its
own narrowly granted account, its own Scheduler job, **created paused**. This task is the module and
the guard — the condition the two grants carry, the account's own principal kind, the `reports/`
lifecycle rule. The Go plan `fugaro update` applies is R7, the setting that turns it on is R13, and
the code that gives it its work is R11. Nothing about it is a model call and nothing about it is a
commit.

- **What it is.** The job is `fugarorpt-reports` and **its name must start with `fugarorpt-`**, for
  the reason `fugarochk-` exists: `fugaro runs ls` and every listing count the `fugaro-` ones as
  workflow jobs, and the alert policies match the prefix (`internal/infra/catalogcheck.go:44`). The
  account is `fugaro-reports`, the Scheduler job `fugaro-reports` **in the installation's own
  scheduler region** — `scheduler_region`, the one the catalog check's Scheduler job carries
  (`modules/installation/catalogcheck.tf:191`), not the installation's `region` — and its schedule
  ships paused.
- **IAM, exactly (E4), and the FULL condition the owner's answer needs.** **One conditioned binding per
  prefix, and the two clause kinds are joined by `||`, never by `&&`:** a conjunction is the shape an
  implementer reaches for first and it matches nothing — a `get` request carries no `objectListPrefix`
  and a `list` request names no object, so `A && B` refuses every request the job makes. The shape is
  the one `gcp.ProjectBucketCondition` already builds, per-prefix clauses joined with `" || "`
  (`internal/backend/gcp/names.go:537`, each clause `projectBucketPrefixClause`, `:547`), with one of
  the clauses the listing attribute instead of `resource.name`:

  ```
  roles/storage.objectViewer on runs/:
    resource.name.startsWith("projects/_/buckets/<b>/objects/runs/") ||
    api.getAttribute('storage.googleapis.com/objectListPrefix', '').startsWith("<list prefix>")

  roles/storage.objectUser on reports/:
    resource.name.startsWith("projects/_/buckets/<b>/objects/reports/") ||
    api.getAttribute('storage.googleapis.com/objectListPrefix', '').startsWith("<list prefix>")
  ```

  The `resource.name` half covers the **reads** — the records the reports summarise — and, on
  `reports/`, the `get`, write, overwrite and delete under the lifecycle; the listing half is what
  grants the **listing**, because the job lists `runs/<slug>/` to find a repository's records and
  R11's stored reader lists `reports/<slug>/<YYYY>/` the way today's reader lists a checkout's folder
  twice over (`internal/cli/runs_digest.go:438` and `:466`, both `os.ReadDir`), with the year roll-up
  listing the same prefix again.
  **`<list prefix>` is deliberately not spelled here.** The attribute's value form — whether it
  carries the request's own prefix (`runs/`) or the full `projects/_/buckets/<b>/objects/runs/` path —
  is exactly what the owner-run listing check below settles: it is **unverified offline** (bucket-iam
  §4, V7), **no code in this repo reads the attribute today** (grepping the tree finds it in the
  design alone), and the wrong form is a grant that lists nothing. The check fixes the value before
  R12's Terraform lands, and the builder takes it as its one argument. **The listing half's own scope
  is `runs/` and not one repository's `runs/<slug>/`**, which is what the job needs and what the
  mechanism can express: the job summarises every repository and so lists each slug's records under
  the one prefix — the narrowness the owner asked for is "the listing prefixes and nothing else", not
  a per-slug listing.

  **Neither half alone is enough, and that is the finding the round before this one fixed.** A
  condition on `resource.name` **grants no listing** (`docs/design/bucket-iam.md` §2.3; live check 7
  recorded a 403 on listing `runs/` for a job account), and a condition carrying only
  `objectListPrefix` **cannot read an object** — so a grant with one half and not the other is a job
  that fails on its first run, and an implementer who hits that 403 is one step from the bucket-wide
  viewer the owner refused. The prefix-limited listing mechanism is **unverified in this repo**
  (bucket-iam §4, V7); **the owner ruled it is verified first**, by an owner-run check and not assumed
  (below), and **R12's Terraform waits for its result**. **One definition, as with every other
  condition:** the two clause kinds are built by a new builder in `internal/backend/gcp/names.go`
  beside `ProjectBucketCondition` and `CatalogReadCondition` — a condition that differs in one byte is
  another binding, and `cover.go` compares them byte for byte — and the module consumes the titles
  and expressions as data, the way `catalog_check` carries `definitions_condition`.
  **No git credential, no model credential, no secret**: the account holds no project role, the job
  mounts no secret, and its env carries the platform variables and `FUGARO_REPORTS_JOB_SPEC` and
  nothing else. **The launcher side is a read, not a second writer:** a launcher's unconditioned
  bucket-wide `objectViewer` already covers the new prefix, so a person can read a stored report and
  **nothing new is granted to a person** — the launcher's own write condition
  (`gcp.LauncherBucketCondition`, `docs/design/bucket-iam.md` §4, H5) covers `runs/` and `tokens/`
  and stays as it is: `reports/` is **not** added to it, because the job is the one writer (ruling 3).
  Operators keep the `objectAdmin` they hold bucket-wide. **The guard's own kind comes with it**:
  `internal/infra/tf/cover.go` gives each project account a principal kind of its own
  (`toCatalogCheck`, `CatalogCheckAccountID`, `TestCatalogCheckAccountIsItsOwnPrincipalKind`), and
  the reports account gets `toReports` beside it, so no `toAccounts` row can be extended to it and
  every other role of it is refused and asks the owner.
- **The retention rule (E3).** `bucket.tf` gains **its fifth `lifecycle_rule`** for `reports/` (the
  bucket carries four today: `runs/`'s age, `cache/`'s two, and the sealed token's); the Go side of
  the same setting, `bucket_lifecycle.reports_days = 548` (18 months, in whole days as its members
  are — `internal/infra/tfvars.go:757`), is R7's. `runs/` keeps its 90 (`F24`, untouched).
- **Files (7):** `internal/backend/gcp/names.go` (the two clauses, one definition, and their titles),
  `internal/backend/gcp/names_test.go` (the clause tests below),
  `internal/infra/tf/cover.go` (the `toReports` kind and `ReportsAccountID`),
  `internal/infra/tf/cover_grants_test.go` (`TestReportsAccountIsItsOwnPrincipalKind` and the
  condition's own cases), `deploy/terraform/gcp/modules/installation/reports.tf` (new: the job, its
  account, its Scheduler job, its two grants and **the reports job's own alert**),
  `deploy/terraform/gcp/modules/installation/variables.tf` (the module's `reports` variable) and
  `deploy/terraform/gcp/modules/installation/bucket.tf` (the fifth rule). **`alert.tf` is not
  opened**: the catalog check's alert is there (`alert.tf:55`) and moving it would replace a live
  resource, so the reports job's alert is written beside the reports job.
- **Red step:** `TestReportsReadConditionCarriesBothClauses` and
  `TestReportsWriteConditionCarriesBothClauses` fail while `names.go` has no builder for them, and
  `TestReportsConditionJoinsItsClausesWithOr` fails against the `&&` shape — which is the red step
  for the reason this task exists: a condition joined the wrong way passes a "both halves are
  written" review and matches nothing, and a condition with one half passes a "listing works" review
  and fails the job's first run.
  `TestReportsAccountIsItsOwnPrincipalKind` fails while the guard has no kind for the new account.
- **Tests:** `TestReportsReadConditionCarriesBothClauses`, `TestReportsWriteConditionCarriesBothClauses`,
  `TestReportsConditionJoinsItsClausesWithOr` (the built expression contains `" || "` and no `" && "`,
  and is byte for byte `ProjectBucketCondition`'s join over the two clause kinds),
  `TestReportsConditionTitlesAreBuiltFromTheAccount`,
  `TestReportsReadConditionCoversNoOtherPrefix` (`tokens/`, `cache/` and a wider prefix are refused),
  `TestReportsAccountIsItsOwnPrincipalKind`. The module's own plan is asserted by R7's Terraform-tagged
  run and by CI's `terraform fmt -check -recursive deploy/terraform`.
- **Hermetic:** the Go tests are hermetic; the module has no test that a hermetic test can hold, and
  the tagged plan R7 runs is its check.
- **Reviewer:** opus + mandatory independent review (the IAM grants and the condition shape are the
  whole of what this task lets near a project's cloud).
- **The listing check, named and not numbered, and R12 depends on it.** The owner ruled this round
  that the prefix-limited listing is verified **first**: the check is the owner's, **no agent
  performs any step of it**, and it settles R12's first Terraform plan. The owner, at their terminal,
  on the sandbox installation, signs in as the project and grants a throwaway service account **both
  conditioned grants** — `roles/storage.objectViewer` on `runs/` and `roles/storage.objectUser` on
  `reports/`, each with the two clause halves above joined by `||` — then, as that account:
  `gcloud storage ls --recursive gs://<bucket>/runs/<slug>/` (must list), `gcloud storage ls
  --recursive gs://<bucket>/reports/` (must list), reading one object under `runs/` with
  `gcloud storage cat` (must read), and the refusals — `gcloud storage cat` of a `tokens/` object,
  `gcloud storage ls gs://<bucket>/` (the **root listing**, which the two prefixes must not reach),
  `gcloud storage ls gs://<bucket>/tokens/` and `gcloud storage ls gs://<bucket>/cache/` (all four
  must be refused). **The check is also what settles `<list prefix>`:** the owner tries each candidate
  form — the request's own prefix (`runs/`) and the full `projects/_/buckets/<b>/objects/runs/` path —
  and the one under which `ls gs://<bucket>/runs/<slug>/` and `ls gs://<bucket>/reports/` both list,
  with the root listing still refused, is the form R12's builder writes. A form the attribute does not
  carry lists nothing, which is why the check runs before the Terraform lands rather than the value
  being reasoned about here. The throwaway account is removed afterwards. **If any of it fails, the plan stops and asks the owner** — the bucket-wide
  viewer a listing needs when the condition cannot carry one is **not** planned as a fallback here,
  because it would also read `tokens/` and `cache/`.

## R13. The jobs' paused setting, in the layer (E4)

Enabling a job is **a setting `fugaro update` writes**, not a `gcloud` command: the project layer
gains a `jobs:` block, **`jobs.reports.paused`** and **`jobs.catalog_check.paused`**, each defaulting
to `true`. `internal/infra` renders each into the installation, and the modules apply `paused` from
them. **Two reasons this is its own task:** a `gcloud scheduler jobs resume` is reverted by the next
`fugaro update`, because the module always applies `paused` — and the 0.18.0 catalog check has the
same gap today from the other side, `paused` being a Terraform variable with **no field in
`CatalogCheckSpec`** (`internal/infra/catalogcheck.go`) for a run to set it from, so its own operator
note offers no command that sticks. This task closes both: the field, the layer, the rows and the
counts.

- **Which layer, and what the merge does with `false`.** The key lives in the **project layer**, as a
  top-level block like `governance:` and `ab:` — and for the same reason those are top-level: the
  `defaults:` overlay is replace-not-tighten, so a key under it could be replaced by a repository's
  own value instead of only tightened. A repository's `fugaro.yaml` has **no** such key, and its
  decoding refuses unknown fields, so the two never meet: the only value in play is the operator's,
  and **`paused: false` — the loosening direction — is the operator's alone**, which is exactly what
  a schedule the project runs should be. The layers meet in the plan, not in a merge conflict.
- **Whether `fugaro settings set` may write it is not decided here** — the generic path writes the
  allowlist of `internal/cli/settings_set.go` and nothing else, so a new layer key is **refused by
  `settings set` until someone says otherwise**, and saying yes moves the allowlist's own count pin
  (`docs/config-management/README.md:47`, "143 of the registry's 323 rows"). **Open items carries
  it**; what this task ships is the key, refused by the generic path, written by hand in the project
  layer.
- **And what the managed gate makes of the value it writes is Open item 7**, not decided here: the
  gate's Scheduler rule reads the job's name and its account and not its `paused` value, so a
  `true → false` flip in a managed project is a schedule an agent session's `update` could start
  without the owner. R13 ships no gate rule and, if the answer is to add one, it lands in this task's
  own release rather than as an operator step.
- **Its own files, and the pins that move.** Registry rows `layer.jobs.reports.paused` and
  `layer.jobs.catalog_check.paused` (`internal/settings`' own rule: a `config.ProjectLayer` field
  without a row fails `TestRegistryCoversEveryConfigField`), the schema, and the three count pins
  that move with them — `docs/config-management/README.md:47`,
  `docs/config-management/reconciliation.md:22` and
  `internal/cli/docs_config_management_test.go:99` (`cfmRegistryRows`).
- **Files (11):** `internal/config/layer.go` (the `Jobs` block), `schemas/project-layer.schema.json`
  (its shape), `internal/settings/registry.go` (the two rows),
  `internal/settings/registry_test.go` (the coverage test that fails without them),
  `internal/cli/docs_config_management_test.go` (`cfmRegistryRows`),
  `docs/config-management/README.md` and `docs/config-management/reconciliation.md` (the two prose
  pins), `internal/infra/catalogcheck.go` (the `Paused` field the catalog check never had, set where
  the spec is built), `internal/infra/catalogcheck_test.go` (its test), `internal/infra/tfvars.go`
  (the two installation options the setting travels in) and `internal/cli/init.go` (where the update
  path reads the layer and passes it to `internal/infra`).
- **Red step:** `TestRegistryCoversEveryConfigField` fails on the new `ProjectLayer` field with no
  row, and `TestCatalogCheckSpecCarriesThePausedSetting` fails while the catalog check's spec still
  has no field for it.
- **Tests:** `TestRegistryCoversEveryConfigField`, `TestProjectLayerJobsPausedDefaultsTrue`,
  `TestCatalogCheckSpecCarriesThePausedSetting`, `TestCatalogCheckSpecCarriesPausedFalse`,
  `TestProjectLayerRefusesAJobsKeyItDoesNotKnow`.
- **Hermetic:** a layer document written in the test, no cloud.
- **Reviewer:** opus + mandatory independent review (the setting is the only thing in this plan that
  turns a schedule on).

## R7. The reports job's Go plan and the update (E1, E3, E4)

The plan `fugaro update` applies, built on R12's module and R13's setting: the spec type, the env
document the job reads, the `reports` node and the five installation goldens an always-present node
regenerates. **R12 is where the job, its grants and the lifecycle rule are; R7 is what renders
them**, and R11 is the code that gives the job its work.

- **`fugaro update` plans it (HARD RULE).** `internal/infra/reportsjob.go` (the spec type, the env
  document `FUGARO_REPORTS_JOB_SPEC` and its version) and the `reports` node in
  `internal/infra/tfvars.go`; the pass-through in `roots/installation/` — variables and `main.tf` —
  into R12's module variable. An installation at the release before this one **converges in one
  `fugaro update`**: the plan creates the job, its account, the Scheduler job and the bucket's fifth
  rule, and asks for the typed project name where it plans the reports grants, as the catalog check's
  own update does.
- **The retention rule's Go half (E3).** `bucket_lifecycle` gains **`reports_days = 548`** (18 months,
  in whole days as its members are — `internal/infra/tfvars.go:757`), matching the `reports/` rule
  R12 writes in the module; `runs/` keeps its 90 (`F24`, untouched).
- **Files (11):** `internal/infra/reportsjob.go` (new: the spec type, the env document and its
  version), `internal/infra/reportsjob_test.go` (new: the untagged tests below),
  `internal/infra/reportsjob_terraform_test.go` (new: **the tagged convergence test alone** — a build
  tag applies to a whole file, so tagging `reportsjob_test.go` would put every untagged test in it
  behind the tag and the tagged test takes a file of its own), `internal/infra/tfvars.go` (the
  `reports` node and
  `bucket_lifecycle.reports_days`), `deploy/terraform/gcp/roots/installation/variables.tf` (the
  `reports` variable) and `main.tf` (its pass-through), and **the five goldens the always-present
  node regenerates** — `internal/infra/testdata/installation-accounts.tfvars.json`, `-budget-job`,
  `-budget`, `-coordinator` and `-credentials`.
- **Red step:** `TestUpdatePlansTheReportsJobPaused` fails while the node is not rendered,
  `TestBucketLifecycleCarriesReportsAtEighteenMonths` fails while the lifecycle member is missing,
  and the convergence test fails while the plan would replace the installation's state rather than
  add to it.
- **Tests:** `TestUpdatePlansTheReportsJobPaused`, `TestBucketLifecycleCarriesReportsAtEighteenMonths`,
  `TestReportsJobAccountHoldsNoSecretAndNoProjectRole`, `TestReportsJobNameIsNotAWorkflowJob`,
  `TestReportsSchedulerJobIsInTheSchedulerRegion`, and the HARD RULE's own convergence test —
  **`TestInstallationAtThePreviousVersionConvergesToTheReportsJob`**: an installation at the previous
  version, with the tfvars and the stored state that release wrote, converges in one `fugaro update`,
  with the reports job created **paused** — and **still paused after a second update until
  `jobs.reports.paused` says otherwise** — and no second command, no deletion and no manual repair.
  **The same test asserts the other job too**, because the setting is one change and the owner's
  answer named both: the 0.18.0 catalog check job and its Scheduler job are **kept, not replaced**,
  and stay paused across two updates until `jobs.catalog_check.paused` is false, which is the
  convergence the HARD RULE asks for both jobs at once.
  It is a `-tags terraform` test, the way `internal/infra/installation_terraform_test.go` runs the
  real plan; the convergence claim's own shape is
  `TestInstallationAtThePreviousVersionConvergesToTheMix` (`internal/infra/retire_test.go:1939`),
  which plans through Go and is **not** Terraform-tagged.
- **Hermetic:** the untagged tests run against the module's own fixtures and a temp checkout; the
  Terraform-tagged test in its own file runs the real plan.
- **Reviewer:** opus + mandatory independent review (the plan changes what `fugaro update` writes for
  every installation, and the goldens are the promise that an existing one converges).
- **The job's own live check, named and not numbered.** It is the owner's too, and it takes the
  number free at the merge of the task that writes it (§5's closing note, "What the rulings take, and
  what they leave", the register's rule for a work that reserved none —
  `docs/run-limit-carry-over/README.md:55`): on the sandbox installation, the owner runs
  `fugaro update` (it plans the reports job paused), turns it on **by the setting**
  (`jobs.reports.paused: false`, then `fugaro update` again), waits for one firing and reads, with
  `gcloud storage ls gs://<bucket>/reports/**`, one stored report per repository for the closed
  period plus the aggregate, byte-identical to what `fugaro runs digest` writes for the same period;
  a second `fugaro update` with the setting untouched leaves the schedule running, which is what the
  `gcloud` line this plan used to carry could not do. Nothing about the job is enabled by default and
  **nothing here is run by an agent**.

## R11. The job's command and the stored reader (E1, E7)

The job's code half — R12 and R7 are what plan it, R13 is what turns it on, and this is what runs:
the subcommand, the loop that writes the reports, and the reader every writer's summaries come from.
Hermetic throughout — it touches no cloud API the fake does not model.

- **Its command.** `fugaro runs reports --job`, registered beside its sibling in
  `internal/cli/runs.go:232` (not `root.go`, which another work stream holds). It reads its whole
  assignment from **`FUGARO_REPORTS_JOB_SPEC`** — a versioned JSON document on the
  `FUGARO_CATALOG_CHECK_SPEC` shape (`internal/infra/catalogcheck.go:29`), which R7 declares: the
  project, the bucket, the repository slugs and the periods to close — and **refuses to start without
  it**. The release's own `*Since` constant (`internal/cli/recipes_skew.go:303` is `digestSince`'s)
  gains the release that ships it, so a skill or a recipe that names `runs reports` cannot reach a
  CLI without it.
- **What it writes.** For each closed period it loops over the project's repositories and writes
  `reports/<slug>/<YYYY>/<key>.md`, then the aggregate (R8) at `reports/<YYYY>/<key>.md`. **A report
  key is the period**; the bytes are `internal/digest`'s own writer, so F2's byte-identical rule holds
  across writers. **A rerun compares before it writes**: present and equal means skip, different means
  overwrite, which is what makes a re-fired schedule harmless.
- **The stored reader (E7).** `internal/digest/store.go` is the bucket-backed read of the stored
  reports, and `patterns.go`'s caller reads them through it instead of the committed folder: the
  fifth rule and the four shipped ones then see the bucket's series, and the committed ones too where
  they exist. The aggregate keeps no rules in the first slice (R8).
- **Files (8):** `internal/cli/runs_reports.go` (new), `internal/cli/runs_reports_test.go` (new),
  `internal/digest/store.go` (new), `internal/digest/store_test.go` (new),
  `internal/digest/patterns.go` (the caller changes source, once), `internal/digest/patterns_test.go`
  (`TestPatternsReadTheStoredReports`), `internal/cli/runs.go` (the registration line),
  `internal/cli/recipes_skew.go` (the release's own `*Since` constant, beside `digestSince`).
- **Red step:** `TestRunsReportsWritesEachRepositorysReportIntoTheBucket` fails while the subcommand
  does not exist (`unknown command "reports"`), and `TestRunsReportsRefusesToStartWithoutItsSpec`
  fails against an implementation that falls back to the checkout's own config — the job has no
  checkout and no local config, and a fallback would be a writer that writes somewhere nobody named.
- **Tests:** `TestRunsReportsWritesEachRepositorysReportIntoTheBucket`,
  `TestRunsReportsRefusesToStartWithoutItsSpec`, `TestRunsReportsSkipsAnIdenticalStoredReport`,
  `TestRunsReportsOverwritesAReportWhosePeriodChanged`,
  `TestRunsReportsWritesTheYearFromTheStoredMonths`, `TestPatternsReadTheStoredReports`.
- **Hermetic:** a `gcpfake` bucket and a fake clock; no real plan, no network.
- **SEC:** the command writes with the job account's own credentials and nothing else: no git
  credential, no model credential, no secret, and the account's `reports/` write is the only write it
  holds. It runs in the container, where the checkout is the run's and no person's terminal is in
  reach.
- **Reviewer:** opus + mandatory independent review (the command that becomes the writer of a new
  prefix in the project's bucket).

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
- **Files (3):** `internal/cli/runs_reports.go` (R11's file: the aggregate step, after the loop),
  `internal/cli/runs_reports_test.go`, `internal/digest/store.go` (R11's: the aggregate's own shape).
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

- **What the rulings fix about it:** the source (`reports/` in the project's bucket), the target (the
  project's designated repository or folder — **E6, Open items**: no key is invented here), the shape
  (per-repository reports under per-repository subfolders, the aggregate beside them, the index
  regenerated over the folder the layout names), and the outcome (one pull request, the run's existing
  identity, no report run and no `digest` step of its own).
- **Whose credentials read the source, which the owner settled this round:** **the starting person's
  launcher account, not the run's job account.** A job account reaches `runs/`, `cache/` and `locks/`
  only (`docs/design/bucket-iam.md` §3), and the owner ruled that **no other job account gets
  `reports/`** — the reports job is its one writer. A launcher's unconditioned bucket-wide
  `objectViewer` does reach it, so the read is the person's, on the launch path, and how the bytes
  reach the run is part of what the designated-repo design item settles (the recipe's own first step,
  or the launcher handing them in). Nothing here needs a git credential beyond the run's own.
- **What is not written until the designated-repo design item lands:** the recipe's name,
  `fugaro.yaml` keys if any, the read's own shape and the file list. **The task is blocked on that
  design item, and this plan does not guess its shape** — which is why R11 is the only task behind it,
  and nothing at all waits on it.
- **Files (to be completed by that design item):** a recipe under `internal/recipe/catalog/` and its
  runner step — itself a `docs/periodic-reviews` decision the design item's files settle.
- **Reviewer:** opus + mandatory independent review (the only path in this design that writes into a
  repository under the user's identity).

## R10. `fugaro runs digest` generates to the bucket (E8)

The command keeps its report and changes its default target: **the bucket**, with `--dir` still
writing locally for a person who wants the files in a checkout.

- **What changes:** the default when `--dir` is absent (the stored report's path under `reports/`,
  through R11's writer), the command's `Short`, its `Long`, the header comment that states the
  "repository's own docs tree" default (`internal/cli/runs_digest.go:26`), and
  `docs/recipes.md`'s sentence about where a report lands. **What does not:** the layout, the four
  periods and the closed-period refusal, `--stdout`, `--json`, `--submit-draft`, the byte-identical
  rule between writers (F2), and **`fugaro runs digest` still commits nothing** (D964, E5).
- **The read the owner ruled in this round:** a person reads a **stored** report back with this same
  command, under their own launcher credentials — which hold the bucket-wide `objectViewer` and **no
  write on `reports/` at all**, because the job is its one writer. The flag is this task's to name
  (the shape is `--stored <period>`, printing what `--stdout` prints); `--dir` keeps its meaning and
  gains nothing.
- **Files (4):** `internal/cli/runs_digest.go`, `internal/cli/runs_digest_test.go` (the default
  target and the stored read), `docs/recipes.md`, and `internal/digest/store.go` (the write path
  beside R11's reader — the file R8 has just finished its own side of, which is what puts this task a
  level above it; **Open items decides whether it is a write path at all**).
- **Why it is last:** it touches three files earlier tasks hold — `internal/cli/runs_digest.go` (R1
  and R2), `docs/recipes.md` (R1) and `internal/digest/store.go` (R11 and R8) — so it sits alone at
  **level 5**, above every one of them, and the shipped command's default changes once, at the end.
  **R6 is not one of them**: R6's three files do not include `docs/recipes.md`, so it gates nothing
  here and this task does not wait on it.
- **Blocked, in one half, and the plan says so where the level is read.** The **stored read** is the
  launcher's and needs nothing new; the **write** into `reports/` has no grant that does not make
  every developer a second writer, which is **Open item 5** — so the task's bucket-default half is
  blocked on that answer the way R9 is blocked on E6, while its `--dir` path, its stored read and its
  `Short`/`Long`/recipes wording are not.
- **Red step:** `TestRunsDigestWritesToTheBucketByDefault` fails while the command still writes into
  the checkout without `--dir`, and `TestRunsDigestStillWritesLocallyWithDir` fails if `--dir` is
  quietly dropped — the person's path is a feature, not a fallback.
- **Tests:** `TestRunsDigestWritesToTheBucketByDefault`, `TestRunsDigestReadsAStoredReport`,
  `TestRunsDigestStillWritesLocallyWithDir`, `TestRunsDigestNeverCommits` (the existing refusal,
  re-pinned), `TestRunsDigestJSONNamesTheStoredPath`.
- **SEC, corrected this round.** The earlier line said this command's bucket access was "the same read
  and write `runs ls` and `runs digest` already do" — **it is not, in either direction.** `runs
  digest` writes only into the checkout today, and a launcher's write grant is `runs/` and `tokens/`
  (`gcp.LauncherBucketCondition`, `docs/design/bucket-iam.md` §4, H5): a launcher writing `reports/`
  gets a 403, and **granting it would make every developer a second writer of `reports/`, which is
  exactly what ruling 3 refuses**. So the stored **read** is the launcher's (and needs nothing new —
  their viewer already covers the prefix), and the **write** into `reports/` is **Open items**: the
  first slice's `--dir` write, which is a write into the caller's own checkout, is unaffected either
  way.
- **Reviewer:** opus + mandatory independent review (it changes where a shipped command writes by
  default).

## Open items

Seven questions the rulings raise and **this plan does not decide** — four from the rulings of
2026-11-05, two opened by the answers the review of round 3 drew, and one by the review of round 4 —
each one written where the task that meets it says so:

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
5. **How `fugaro runs digest` writes to the bucket at all (E8, and this round's answer about
   `reports/`).** E8 makes the bucket the command's default target; the owner's answer this round
   makes **the job the only writer of `reports/`** and gives launchers **read only**, so the command
   cannot write there with the caller's own credentials — a launcher write would be a second writer,
   which ruling 3 refuses. The readings: the command's default stays the checkout and the bucket is
   a **read** (which is a wording change in E8, and R10's stored read is useful either way), or a
   launcher write grant on `reports/` returns (a second writer, against ruling 3), or the write is
   handed to the job (a mechanism this plan does not design, and one that would need a way to ask
   the job for one period). **Not decided here**; R10's `--dir` path and its stored read are
   unaffected, and the task is written so the answer is a default and one grant row.
6. **Whether `fugaro settings set` may write `jobs.*.paused` (E4, and this round's review).** The
   generic path writes the allowlist of `internal/cli/settings_set.go` and nothing else, so the new
   layer key is **refused by `settings set`** as R13 ships it — which is the safe default, not a
   decision: saying yes adds `layer.jobs.reports.paused` (and its sibling) to the allowlist and moves
   that list's own count pin (`docs/config-management/README.md:47`, "143 of the registry's 323
   rows"). **R13 ships the refused state**; the answer is one allowlist line and one count.
7. **Whether the managed gate reads a Scheduler `paused` flip as a loosening (E4, and the review of
   round 4).** R13 makes enabling a job a setting `fugaro update` writes — and in a **managed** project
   an `update` prompts for nothing, the gate judging the plan instead and refusing a loosening "in its
   own words" (`docs/releases/v0.19.0.md`, `### Highlights`). But the gate's own rule for a Scheduler
   job is its name and the account it runs as (`internal/infra/tf/managed_cover.go:930`), and **neither
   of those changes when `paused` goes from `true` to `false`** — so `jobs.reports.paused: false` is,
   on today's tables, not a loosening the gate sees, and an agent session's `update` could start a
   schedule in the owner's project without the owner. Whether that flip should be read as one (a
   schedule that begins spending is a change the owner should make, in a project where the gate
   otherwise asks nothing), or whether "the deployer holds what the allowlist admits" is answer enough,
   is **not decided here**. R13 ships no gate rule for it; the answer is one rule in
   `managed_cover.go` — or none at all.

## Release slot

**CLI 0.20.0.** 0.15.0, 0.16.0, 0.17.0, 0.18.0 and 0.19.0 have all shipped since this design was
written, and the rulings of 2026-11-05 make this a cloud change, so **0.20.0 is the first release
this work can ride** ([reconciliation.md](reconciliation.md) §4, M8). The slot said 0.19.0 until this
round: that release shipped on `main` while the plan was open and carries none of this work
(`docs/releases/v0.19.0.md`). **It now has to carry the whole of R12, R13, R7 and R11**, because a
schedule without the job that reads it is worse than no schedule. The release notes'
`### Highlights` carry the two sentences the rulings are: **the reports are written into your
project's runs bucket, and committing them anywhere is your choice** (`fugaro runs reports --job`
behind a paused schedule, and `fugaro runs digest` writing to the bucket by default).

**The release's `### For operators` entry, and it is the HARD RULE's whole upgrade** — two numbered
lines, and the second is a "when you want it", not a step:

1. "Upgrade the CLI and run `fugaro update` in each checkout: it brings the installation (Terraform,
   the reports job, its account, its paused schedule and the `reports/` lifecycle rule) and the
   checkout to this release. It asks for the typed project name where it plans the reports grants."
2. "**Both jobs are created paused, and `update` is what turns them on.** Set
   `jobs.reports.paused: false` (and `jobs.catalog_check.paused: false` for the catalog check) in the
   project layer and run `fugaro update` again; the schedule then runs until the setting says
   otherwise. Nothing else is needed, and nothing is committed anywhere until you run the opt-in
   commit (R9)."

**One command, and no second step.** The reports job is `fugaro update`'s plan — an installation at
the previous version converges in that one run
(`TestInstallationAtThePreviousVersionConvergesToTheReportsJob`, R7), with the job created, the
account granted and the schedule stopped. Turning a schedule on is not an upgrade step, and it is a
**setting**, not a `gcloud` command: `gcloud scheduler jobs resume` is
undone by the next `fugaro update`, and the 0.18.0 catalog check job's own operator line has the same
gap from the other side (its `paused` is a Terraform variable with no field in `CatalogCheckSpec` for
a run to set) — **R13 closes both**, and `fugaro doctor`'s update reading is what tells the owner the
update is due. The plugin pin — `fugaro upgrade`'s
`pin` step — is what puts the matching plugin and skills into a checkout
(`internal/cli/upgrade.go:80`, `:86`). That is exactly why **R1, R3 and R10 ship in one release**: a
skill that names `--due`, or a skill that says where a report lands, must not reach a CLI whose
behaviour differs, and the pin is what decides that. R3 and R6's §7 sentence are also sequenced behind
#1488's rename of `plugin/**`. **R9 waits for the designated-repo design item (E6)** and is the one
task here that is not gated by a release. R5 is the owner's whenever they run it.

---

## Part II — the 0.22.0 slice: the reports job and the failures report (2026-11-07)

**Status: plan only when this was written; every task in both parts is approved 2026-11-07 (owner:
"All tasks approved. Jobmax").** **Of this part, FC3 has landed; the rest of it, and the reports job
this part starts from, have not.** **Part I is everything above this heading**, the cut written to the
owner's rulings E1–E8. This part is written to the owner's decision
of **2026-11-07** that the periodic reviews/reports ride **0.22.0 "Sloth"**
(`docs/release-streams.yaml`: the CLI stream's `current` is `0.20.3` and its `next` is `Rhino`, so
0.21.0 is Rhino's and 0.22.0 is Sloth's). It is a **release plan over two works that are already
cut**, not a second cut of either. Its decisions are **D1430–D1449** ([reconciliation.md](reconciliation.md)
§6), each answered as recommended on 2026-11-07; its checks are **Check 340–349** (§P8). Every path and
symbol it names was read against `main` at `56c91f66` on 2026-11-07.

### P1. What this part is, and what it does not re-cut

**Two designs meet in this release, and neither is rewritten here.**

| Work | Its cut, which stands | Where it is written |
| --- | --- | --- |
| **the reports job** (the rulings E1–E8) | **R12, R13, R7, R11, R8, R10**, with **R9** blocked on E6 — and **R4** (the fifth rule) and the shipped loop (**R0–R3, R6**) as its prerequisites | this file, Part I |
| **the failures report** (`docs/design/failure-classes.md` §6) | **FC1–FC8**, with that design's own D1–D9, O1–O6, levels L0–L5, critical path and shared-files table | `docs/design/failure-classes.md` §10 |

**What this part adds is the three things neither document can state alone**, one section each:

1. **the union's order** (§P2, §P3): one leveling over both works, and the proof that it costs
   neither work a re-cut.
2. **the seam** (§P4, §P5): the files the two meet in, the one file list that is wrong about a file
   it writes, and the commit and reviewer tier every task carries — which Part I does not state per
   task.
3. **the release** (§P6, §P7, §P8): the slot and its `### For operators` text, the critical path, and
   the owner-run checks the union owes.

**It re-cuts nothing, deliberately.** `docs/catalog-build-context/README.md:56` states the rule this
part follows — "**This folder does not silently become a second plan for one feature**" — and here the
argument runs the other way too: two documents already own these two works, and a third statement of
either would be a plan that can disagree with the one a run picks up. **R12, R13, R7, R11, R8, R9 and
R10 are the reports job's cut and are cited by number; FC1–FC8 are the failures report's cut and are
cited by number.** Where this part disagrees with either about the code it says so in
[reconciliation.md](reconciliation.md) §6, and **there is exactly one such disagreement, and it is
about one file** (**F27**, §P5): the generated `website/content/cli-reference.md` is written by four
tasks of the release, and **three of the four file lists omit it** — R1's (its new `--due` flag),
R11's (`fugaro runs reports`) and R10's (the command's rewritten wording, its stored read's flag and
its new default target).
**One decision adds a file to a task's list the same way, and §P4's table carries it:** D1449's
recommendation has FC8 write `plugin/skills/working/reference/periodic-reviews.md` §3 — the reader's
copy of the rules' list — beside the rules' document, and FC8's own list names the document and not
the skill file.
**§P4's shared-files table is read off both owning documents' file lists rather than asserted**, so
a file two or more tasks of the release write belongs in it whether or not either document's own
shared-files table names it — `schemas/schemas_test.go` is FC1's at level 0 and R1's at level 1, and
it is named in both owning lists.

**What is not in this part at all.** **R5** (Fugaro's own reports) is the owner's, at their terminal,
with no agent step. The **`plugin/**` rename (#1488)** that R3, R6 and FC8 wait on is named where it
orders a task (§P3) and is not this part's to sequence. **[spec.md](spec.md) is not rewritten**: it
keeps its 2026-11-04 text, is read through [reconciliation.md](reconciliation.md) §5 and §6, and gains
no sentence from this part.

### P2. The slices

| Slice | Tasks | What it delivers |
| --- | --- | --- |
| **P — the reports job** | R12, R13, R7, R11, R8, R10 (R9 blocked; R0–R4, R6 are its prerequisites) | one project-level scheduled job that writes every repository's report and the project aggregate into `reports/`, the setting that turns it on, the update that plans it, the stored reader the rules read, and the command's new default target |
| **F — the failures report** | FC1–FC8 | `failure_class` on the record, one writer for the terminal status, the classifier, the legacy reader, `runs ls --group-by failure_class`, the digest's Failures table and its two rules |
| **X — the seam** | none (§P4) | the ordering, the one file F27 corrects three file lists over, and the commit and reviewer tier each task carries |

**The seam has no task of its own, and that is the finding rather than an omission.** §P3 shows the
two works interleave into one leveling without a single task moving, and §P4 shows every file they
share already has one writer per level. What the seam needs is not work but *order*, and the leveling
is the order. The one thing the task lists get wrong — three of the four lists that write the
generated CLI reference do not name it — is corrected in §P5 as a file, not as a task.

### P3. The union's levels

**One leveling, and neither work moves.** A level's tasks may run concurrently; every task sits below
all the tasks it depends on; no level holds two writers of one file. The two works' own levelings
(Part I, "Order, and what this plan does not own", and the design's §10.1) merge unchanged:

| Level | the reports job | the failures report | Why the two can share it |
| --- | --- | --- | --- |
| **0** | R0, R12, R13 | FC1 | the reports job writes `internal/digest/due.go`, `internal/backend/gcp/names.go`, `internal/infra/tf/cover.go`, `deploy/terraform/gcp/modules/installation/**` and the setting's own files; FC1 writes `internal/runstore/runstore.go`, `schemas/result.schema.json`, `internal/cli/runs_test.go` and `internal/cli/docs_pr_state_test.go` — no file is both |
| **1** | R1, R7 | FC2 | R1 holds `internal/cli/runs_digest.go`, which R2 and R10 take at 2 and 5; R7 holds `internal/infra/tfvars.go`, which R13 took at 0; FC2 holds `internal/runner/runner.go` and the package's other FC2–FC4 files — **not `internal/runner/digest.go` or `digest_test.go`, which R0 took at 0** — and `internal/cli/docs_pr_state_test.go`, which FC1 took at 0 |
| **2** | R2, R4 | FC3 | R4 holds `internal/digest/patterns.go`, which R11 takes at 3 and FC7 at 4; FC3 holds `internal/runner/runner.go`, which FC2 took at 1 and FC4 takes at 3 |
| **3** | R3, R11 | FC4, FC5 | R11 holds `internal/cli/runs.go`, which FC6 takes at 4; R3 holds `plugin/skills/working/reference/periodic-reviews.md`, which R6 takes at 4 — and FC8's own skill file, `plugin/skills/working/reference/diagnose.md`, is not one of R3's four; FC5 holds `internal/cli/runs_test.go`, which FC1 took at 0 and FC6 takes at 4 |
| **4** | R6, R8, R9 | FC6, FC7 | R8 holds `internal/digest/store.go`, which R11 took at 3 and R10 takes at 5; FC7 holds `internal/digest/patterns.go`, which R4 took at 2 and R11 at 3 |
| **5** | R10 | FC8 | the two are the last of their chains, and they share no file |

**No task moves a level, and that is a property of the two designs rather than a coincidence.** The
reports job's tasks live in `internal/infra`, `deploy/terraform/**`, `internal/digest` and
`internal/cli`; the failures report's live in `internal/runner` — its `runner.go`, `refused.go`,
`outcome.go`, `halt.go`, `prflow.go` and the FC2–FC4 test files, **not the `digest.go` and
`digest_test.go` R0 holds** — `internal/runstore`, `internal/runview`, `schemas/` and the same two
packages. The two therefore meet in **eighteen files**
(§P4) — nineteen with the table's one conditional row — and every one of the eighteen already had a
level of its own in one work or the other — which
is why the union adds **no edge that re-levels anything**: the only cross-work edges are **R11 (3) →
FC7 (4)**, stated in §P6, and the shared files' own ordering. **A union that had to re-level would be
a union that had to re-cut**, and the point of this table is that it does not. **D1437's
recommendation follows from the table:** since the leveling costs neither work a re-cut, the reports
job ships with the **whole of FC1–FC8** in 0.22.0 rather than with the field alone — a job storing
reports under the 18-month retention while nothing read the class would write a series nothing
reads.

### P4. Shared files, over the union

**Every file two or more tasks of the release write**, with its writers' levels. The rule is Part I's
and the design's own: **no level holds two writers of one file.**

| File | Writers (level) | Why they meet there |
| --- | --- | --- |
| `internal/digest/patterns.go`, `patterns_test.go` | R4 (2), R11 (3), FC7 (4) | R4 adds the fifth rule; R11 changes where the summaries come from (E7); FC7 adds the two class rules and their constants |
| `internal/digest/store.go`, `store_test.go` (new) | R11 (3), R8 (4), R10 (5) | R11 writes the stored reader; R8 the aggregate's own shape; R10 the write path beside it |
| `internal/cli/runs_reports.go`, `runs_reports_test.go` (new) | R11 (3), R8 (4) | R11 writes the job's loop; R8 the aggregate step after it |
| `internal/cli/runs.go` | R11 (3), FC6 (4) | R11 registers `runs reports` beside its siblings; FC6 adds `--group-by` to `runs ls` |
| `internal/cli/runs_test.go` | FC1 (0), FC5 (3), FC6 (4) | the record's field cases, the legacy reader's, and the grouping's |
| `internal/cli/runs_digest.go`, `runs_digest_test.go` | R1 (1), R2 (2), R10 (5) | the flag and the refusal set, the doctor line's reading, and the default target |
| `internal/cli/docs_pr_state_test.go` | FC1 (0), FC2 (1) | the census pin FC1 extends and FC2 rewrites |
| `schemas/schemas_test.go` | FC1 (0), R1 (1) | FC1's enum check against `failure.Classes()`, and R1's new due schema loading and validating through the same file |
| `internal/runner/runner.go` | FC2 (1), FC3 (2), FC4 (3) | `recordEnd`, the structural classes, the message classifier |
| `internal/runner/refused.go` | FC2 (1), FC3 (2) | `endRefused`'s call and the push rejection's class |
| `internal/infra/tfvars.go` | R13 (0), R7 (1) | the two `paused` settings, then the `reports` node and `reports_days` |
| `plugin/skills/working/reference/periodic-reviews.md` | R3 (3), R6 (4), and FC8 (5) if D1449 puts the rules' list there | the rule list and the report's place, §7's two sentences, and the two class rules' names |
| `docs/periodic-reviews/README.md` | R6 (4), and FC8 (5) if D1449 puts the rules' list there | R6 rewrites the status line; this is the one candidate for FC8's fifth file that already has a writer, which is why the row is listed under both answers (D1449) |
| `docs/recipes.md` | R1 (1), R10 (5) | the `--due` pointer, and the sentence E8 changes |
| `website/content/cli-reference.md` (generated) | R1 (1), R11 (3), FC6 (4), R10 (5) | `runs digest`'s new `--due` row in the flags table, `fugaro runs reports`, `runs ls --group-by`, and `runs digest`'s rewritten `Short`/`Long`, its stored read's flag and its new default target — four regenerations, one per commit (F27) |

**The table is the check, and it is read against §P3 rather than asserted.** It is built from the two
owning documents' own **file lists**, not from their shared-files tables: a file the rule counts is
one two or more tasks write, wherever it was first written down, and `schemas/schemas_test.go` —
FC1's at level 0 and R1's at level 1 — is in both owning lists and in neither of their shared-files
tables. **Eighteen of the files below are written by two or more tasks unconditionally and the
nineteenth, `docs/periodic-reviews/README.md`, is one only under the answer to D1449 that sends
FC8's fifth file there** — the row carries its own condition, and it is the only row whose being a
shared file at all an open answer decides: `plugin/skills/working/reference/periodic-reviews.md`'s
third writer (FC8 at 5) is D1449's recommendation too, but R3 at 3 and R6 at 4 make that row shared
whichever way the answer goes. Every row's levels are distinct; the widest row is
`website/content/cli-reference.md`, **four writers over four levels** — the one file the release's own
generator, not a person, decides the content of (F27) — and **six rows carry three writers each**,
every one of them over three levels: `patterns.go`/`patterns_test.go`, `store.go`/`store_test.go`,
`runs_test.go`, `runs_digest.go`/`runs_digest_test.go`, `runner.go` and `periodic-reviews.md`. **The
one place a glob could have hidden a collision is the one worth stating plainly:** the design's
§10.2 row `internal/digest/*.go` (FC7, L4) resolves to FC7's own six files
(`digest.go`, `render.go`, `patterns.go`, `digest_test.go`, `patterns_test.go`, `testdata`) and
**not** to `internal/digest/store.go`, which R11 creates at 3 and R8 and R10 write at 4 and 5. Read
as a literal glob it would put R8 and FC7 in one level on one file; read as its own file list it does
not, and this row is where the union says which.

**Two files a reader may expect here are not, and the reason is the same in both cases:** a file with
one writer is not a shared file. `internal/digest/render.go`, `digest.go` and `internal/digest/testdata`
are FC7's alone — the reports job writes its reports through `internal/digest`'s own writer and adds
no cell to them (R11) — and `internal/runstore/runstore.go` and `schemas/result.schema.json` are
FC1's alone, because the job's spec, its env document and its IAM add no field to the record.

**One file is written after every level, and it is not a task's:** `docs/releases/vX.Y.Z.md` is FC8's
at 5 (its `### For operators` entry for the class) and the release's own file afterwards
(`scripts/release.sh`, from a clean clone). The two are the same file written once each, a release
apart in time rather than a level apart, and §P7 is the second one's text.

### P5. The tasks, in the union's shape

**Each block is the union's own summary of a task its owning document states in full**, and the
fields this part adds are the **level** (§P3), the **commit** — which Part I does not state per task —
and the file **F27** adds to three of the lists. **The owning section wins on everything else.** Two rules keep that
from being a formality:

- **The FC blocks' files and commands are the design's own §10 lines, quoted rather than restated.**
  A restatement that drops one file or narrows one `-run` pattern makes a gate pass vacuously — a
  command that names the wrong test matches nothing and still exits zero — which is the one failure a
  plan's commands must not have. Where a block below departs from the design's wording, it says so.
- **The P blocks' commands are this part's**, because Part I states each task's named tests but no
  command per task; each pattern below is checked against that task's own list in Part I, and the
  owning section remains the list of record for the files.

#### Level 0 — R0, R12, R13, FC1

**R0 — the due computation moves into `internal/digest` (slice P).** Part I, `## R0`.
- **Files (4):** `internal/digest/due.go` and `due_test.go` (new), `internal/runner/digest.go` (the
  call site; `digestCatchUpDays` and `digestDuePeriods` go), `internal/runner/digest_test.go`.
- **Interfaces:** `digest.DuePeriods` and the catch-up constant, moved with their cases.
- **Tests, and the command:** the existing due-period test moves and keeps its cases, and the
  runner's own red test — `TestDigestStepCatchesUpSevenDays`, which Part I's red step calls red
  until the call site moves — is the one `-run 'Due'` alone would not reach —
  `GOFLAGS=-p=2 go test ./internal/digest ./internal/runner -run 'Due|CatchesUp' -count=1`.
- **Depends on:** nothing. **Red step:** `TestDuePeriodsIsTheClosedWindow` fails while
  `digest.DuePeriods` does not exist. **Reviewer:** standard. **Commit:**
  `refactor(digest): the due computation, where both readers can reach it`.

**R12 — the reports job in Terraform, and its grants, exactly (slice P).** Part I, `## R12`.
- **Files (7):** `internal/backend/gcp/names.go` and `names_test.go`, `internal/infra/tf/cover.go`
  and `cover_grants_test.go`, `deploy/terraform/gcp/modules/installation/reports.tf`, `variables.tf`
  and `bucket.tf`.
- **Interfaces:** the two prefix-limited conditions (both clause halves joined by `||`),
  `ReportsAccountID` as its own principal kind, the module's `reports` variable, and the bucket's
  fifth `lifecycle_rule` (18 months).
- **Tests, and the command:** `GOFLAGS=-p=2 go test ./internal/backend/gcp ./internal/infra/tf -run
  'Reports' -count=1`.
- **Depends on:** **Check 340's result** (§P8) — nothing in either work may be enabled before the
  grant is proven. **Red step:** the condition tests fail while `names.go` has no builder, and the
  join test fails against the `&&` shape. **Reviewer:** opus + mandatory independent review. **Commit:**
  `feat(infra): the reports job, its account and its grants`.

**R13 — the jobs' paused setting, in the layer (slice P).** Part I, `## R13`.
- **Files (11):** `internal/config/layer.go`, `schemas/project-layer.schema.json`,
  `internal/settings/registry.go` and `registry_test.go`, `internal/cli/docs_config_management_test.go`,
  `docs/config-management/README.md` and `reconciliation.md`, `internal/infra/catalogcheck.go` and
  `catalogcheck_test.go`, `internal/infra/tfvars.go`, `internal/cli/init.go`.
- **Interfaces:** `ProjectLayer.Jobs` and its two `paused` keys, the two registry rows, and the
  `Paused` field the catalog check's spec never had.
- **Tests, and the command:** `GOFLAGS=-p=2 go test ./internal/settings ./internal/infra ./internal/config
  -run 'Registry|Jobs|CatalogCheck' -count=1`.
- **Depends on:** nothing. **Red step:** `TestRegistryCoversEveryConfigField` fails on the new field
  with no row. **Reviewer:** opus + mandatory independent review. **Commit:**
  `feat(config): jobs.reports.paused and jobs.catalog_check.paused`.

**FC1 — the vocabulary, the field and its schema (slice F).** The design, `### FC1`.
- **Files (6), the design's list:** the new `failure` package (or `internal/runstore/failure.go`),
  `internal/runstore/runstore.go`, `schemas/result.schema.json`, `internal/cli/runs_test.go`,
  **`schemas/schemas_test.go`**, `internal/cli/docs_pr_state_test.go`. **This is the list §P1 and
  §P4 rely on**: `schemas/schemas_test.go` is in it, and §P4 has a row for it.
- **Interfaces:** `failure.Class` (a string type), `failure.Classes()` (the one slice), and the
  per-class properties read from one table.
- **Tests, and the command — the design's, verbatim:** `TestResultSchemaFailureClassesAgreeWithGo`,
  `TestResultSchemaAcceptsRunnerRecords`;
  `GOFLAGS=-p=2 go test ./internal/runstore ./schemas -run 'FailureClass|Accepts' -count=1`.
- **Depends on:** nothing. **Red step:** the design's — write the schema's enum from the Go slice
  before the slice exists. **Reviewer:** standard. **Commit:**
  `feat(runstore): the failure_class vocabulary and the record's field`.

#### Level 1 — R1, R7, FC2

**R1 — `fugaro runs digest --due [--json]` (slice P).** Part I, `## R1`.
- **Files (6 — 5 in Part I, plus the F27 correction):** `internal/cli/runs_digest.go`,
  `internal/cli/runs_digest_due_test.go` (new),
  `schemas/runs-digest-due.schema.json` (new), `schemas/schemas_test.go`, `docs/recipes.md`, and
  **`website/content/cli-reference.md`** — `--due` is a new row in the flags table the reference
  prints for `runs digest`, and Part I's list does not name the file (F27).
- **Interfaces:** `--due` and `--json` on the shipped subcommand, and the due document's schema.
  **By D1444's recommendation the flag gains no class line** — `--due` says a period has no report,
  and the class is a reading of a report that exists — so the due document's shape is R1's own and
  FC7 does not touch it.
- **Tests, and the command:** `GOFLAGS=-p=2 go test ./internal/cli ./schemas -run 'RunsDigestDue'
  -count=1` — Part I's eight named tests, and the schema test the file list's
  `schemas/schemas_test.go` adds for the due document, which is the one package `-run` pattern that
  reaches both.
- **Depends on:** R0. **Red step:** `TestRunsDigestDueNamesTheMissingPeriods` fails while the flag
  does not exist. **Reviewer:** standard. **Commit:**
  `feat(cli): runs digest --due names the closed periods a checkout has no report for`.

**R7 — the reports job's Go plan and the update (slice P).** Part I, `## R7`.
- **Files (11):** `internal/infra/reportsjob.go`, `reportsjob_test.go` and
  `reportsjob_terraform_test.go` (new; the tagged convergence test alone, because a build tag applies
  to a whole file), `internal/infra/tfvars.go`, the installation's own options, the five
  `internal/infra/testdata/installation-*.tfvars.json` goldens and the roots.
- **Interfaces:** `infra.ReportsJobSpec`, the env document and its version, and the `reports` node
  with `reports_days`.
- **Tests, and the command:** `GOFLAGS=-p=2 go test -tags terraform ./internal/infra/... -run
  'ReportsJob|BucketLifecycle|InstallationAtThePreviousVersionConverges' -count=1`.
- **Depends on:** R12 (the module, the conditions and the guard) and R13 (the two `paused` settings),
  and through R12 on Check 340. **Red step:** `TestUpdatePlansTheReportsJobPaused` fails while the
  node is not rendered. **Reviewer:** opus + mandatory independent review. **Commit:**
  `feat(infra): fugaro update plans the reports job, paused`.

**FC2 — one writer for the terminal status and class (slice F).** The design, `### FC2`.
- **Files (11, under the plan's twelve-file cap), the design's list:** `internal/runner/runner.go`,
  `gate.go`, `refused.go`, `followup.go`, `reviewmode.go`, `artifact.go`, `reviewartifact.go`,
  `shadow.go`, `failure_class_internal_test.go`, **`docs/design/failure-classes.md`** (§1's census
  sentence is rewritten) and `internal/cli/docs_pr_state_test.go`.
- **Interfaces:** `func (r *run) recordEnd(status runstore.Status, outcome runstore.Outcome, class
  failure.Class, reason string)` — the one writer the thirty-six statements become. **By D1433's
  recommendation the reports job's own run is a run like any other**, so its class goes through this
  same writer rather than a special case for the machinery.
- **Tests, and the command — the design's, verbatim:** `TestRecordEndIsTheOnlyWriterOfATerminalStatus`,
  `TestUnclassifiedIsWrittenWhereNoClassIsChosen`, `TestHaltedRunCarriesItsHaltsClass`;
  `GOFLAGS=-p=2 go test ./internal/runner -run 'RecordEnd|Unclassified|HaltedRun' -count=1`.
- **Depends on:** FC1. **Red step:** the design's — the guard fails the moment the field is added
  beside the thirty-six statements. **Reviewer:** **independent (SEC)** — the reviewer asks whether a
  class or a status can be written anywhere else, and whether the last-writer rule is the only rule.
  **Commit:** `feat(runner): one writer for a run's terminal status and class`.

#### Level 2 — R2, R4, FC3

**R2 — the `doctor` line (slice P).** Part I, `## R2`.
- **Files (3):** `internal/cli/doctor_reviews.go` and `doctor_reviews_test.go` (new), and
  `internal/cli/runs_digest.go` only if the reading needs a second entry point — search before
  writing one.
- **Interfaces:** the reviews-due check, its class and its one-line fix.
- **Tests, and the command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'DoctorReviewsDue' -count=1`.
- **Depends on:** R1. **Red step:** `TestDoctorReviewsDueWarnsWhenAClosedPeriodHasNoReport` fails
  while the check does not exist. **Reviewer:** standard. **Commit:**
  `feat(doctor): the reviews-due line, and the class it prints in`.

**R4 — the fifth pattern rule (slice P).** Part I, `## R4`.
- **Files (2):** `internal/digest/patterns.go` and `patterns_test.go`. The skill's §3 rule list is
  R3's, one level above.
- **Interfaces:** `RuleCostPerRunRising` and its place in `RuleNames`.
- **Tests, and the command:** `GOFLAGS=-p=2 go test ./internal/digest -run 'PatternCostPerRunRising|
  PatternRuleOrderIsStable' -count=1`.
- **Depends on:** R1. **Red step:** `TestPatternCostPerRunRisingNeedsFiveMonthlyReports` fails while
  the rule does not exist. **Reviewer:** standard. **Commit:**
  `feat(digest): cost_per_run_rising, the fifth rule`.

**FC3 — the classes the runner knows structurally (slice F).** The design, `### FC3`.
- **Files (6), the design's list:** `internal/runner/outcome.go` (the four `Decide` refusals),
  `halt.go` (`haltClass`), `runner.go` (`verify`, `runCommits`, the `config_invalid` refusals),
  `refused.go` (`branch_moved`), `prflow.go` (`git_provider_error`), and their test files.
- **Interfaces:** `failure.ClassForVerify(rec verify.Record, sha string)` and `haltClass(HaltReason)`.
- **Tests, and the command — the design's, verbatim:** `TestDecideRefusalsCarryTheirClass`,
  `TestHaltReasonsMapToTheirFamily`, `TestConfigRefusalIsConfigInvalid`;
  `GOFLAGS=-p=2 go test ./internal/runner -run 'DecideRefusals|HaltReasons|ConfigRefusal' -count=1`.
- **Depends on:** FC2. **Red step:** the design's — every class assertion fails against a tree where
  the class is `unclassified`. **Reviewer:** standard. **Commit:**
  `feat(runner): the classes the runner knows from its own state`.

#### Level 3 — R3, R11, FC4, FC5

**R3 — the skills carry the signal and the schedule (slice P).** Part I, `## R3`. **Sequenced after
#1488.**
- **Files (4):** `plugin/skills/setup/SKILL.md`, `plugin/skills/working/SKILL.md`,
  `plugin/skills/working/reference/periodic-reviews.md`, `plugin/periodic_reviews_skill_test.go`.
- **Interfaces:** the printed schedule, the due signal with its command, the report's place, and the
  fifth rule's name in §3.
- **Tests, and the command:** `GOFLAGS=-p=2 go test ./plugin/ -count=1`, plus the lint's CLI-coverage
  rule (`plugin/skills_lint_test.go`, with the paid flags' half in `ee/plugincheck/skills_test.go`).
- **Depends on:** R4, and #1488 landed. **Red step:** the new plugin assertion fails while the setup
  skill names no review cadence. **Reviewer:** standard. **Commit:**
  `docs(skills): the cadence, the due signal and the five rules`.

**R11 — the job's command and the stored reader (slice P).** Part I, `## R11`.
- **Files (9 — 8 in Part I, plus the F27 correction):** `internal/cli/runs_reports.go` and
  `runs_reports_test.go` (new), `internal/digest/store.go` and `store_test.go` (new),
  `internal/digest/patterns.go` and `patterns_test.go`, `internal/cli/runs.go`,
  `internal/cli/recipes_skew.go`, and **`website/content/cli-reference.md`**.
- **The F27 correction, said plainly:** `website/content/cli-reference.md` is generated
  (`go run ./ee/clidoc`) and `ee/clidoc/cliref_test.go`'s `TestCliReferenceIsCurrent` requires the
  committed file to match the command tree. R11 registers `fugaro runs reports`, so R11 changes that
  file; the design names the file only in **FC6's list at L4**, which is a different level and
  therefore no collision, but **R11's own file list omits it** — so `TestCliReferenceIsCurrent` is
  red in R11's commit unless that commit regenerates the file. **The file is added to R11's list and
  regenerated in R11's commit; R1's and R10's lists omit it the same way, and their commits
  regenerate it too** (§P4's row is the whole of it).
- **Interfaces:** `fugaro runs reports --job`, the stored reader `internal/digest/store.go` returns
  the summaries from, and the `*Since` constant beside `digestSince`.
- **Tests, and the command:** `GOFLAGS=-p=2 go test ./internal/cli ./internal/digest -run
  'RunsReports|PatternsReadTheStored' -count=1`.
- **Depends on:** R7 (the job's spec and its own assignment) and R4 (the rules' source, E7). **Red
  step:** `TestRunsReportsWritesEachRepositorysReportIntoTheBucket` fails while the subcommand does
  not exist. **Reviewer:** opus + mandatory independent review. **Commit:**
  `feat(cli): runs reports --job, and the reader the rules read`.

**FC4 — the classifier over the agent's own message (slice F).** The design, `### FC4`.
- **Files (4), the design's list:** `internal/runner/runner.go` (`stageFailureClass`, the phrase
  tables, the `res.IsError` case), `internal/runner/stageerror_internal_test.go` (or the existing
  `stageerror_test.go`), and — for the security property — `internal/runner/redaction_test.go`.
- **Interfaces:** `func stageFailureClass(auth string, res agent.Result) failure.Class`.
- **Tests, and the command — the design's, verbatim:** `TestStageFailureClassNamesTheAuthRefusal`,
  `TestStageFailureClassNamesTheStreamFailure`, `TestStageFailureClassFallsBackWithoutText`,
  **`TestStageFailureClassCopiesNoText`** (the class, printed, contains no byte of the message);
  `GOFLAGS=-p=2 go test ./internal/runner -run 'StageFailureClass' -count=1`.
- **Depends on:** FC2. **Red step:** the design's — the three tests fail against a classifier that
  reads no text. **Reviewer:** **independent (SEC)** — the reviewer asks whether every byte of the
  message stays outside the record, and whether anything acts on the class. **Commit:**
  `feat(runner): classify a stage's own error message, without copying it`.

**FC5 — the legacy reader (slice F).** The design, `### FC5`.
- **Files (4), the design's list:** `internal/runview/runview.go` (`FailureClass` on the row,
  `legacyClass`), its test file, `internal/runview/launched_by_test.go`'s neighbour for the guard
  (`TestRowCarriesTheClassTheRecordHolds`), and `internal/cli/runs_test.go`.
- **Interfaces:** `func ClassOf(rec *runstore.Record) failure.Class` — the one function the row and
  the digest both call: the field when it is there, the fixed table when it is not. **D1436** is why
  this task is not optional for the job's first stored reports: most records a first report reads
  predate the field, so the reading from `reason` is what gives them a class at all.
- **Tests, and the command — the design's, verbatim:** `TestLegacyClassFromReasonTable`,
  `TestLegacyUnmatchedReasonIsUnclassified`, `TestLegacyClassIsNeverWrittenBack` (the record's bytes
  after a read are unchanged); `GOFLAGS=-p=2 go test ./internal/runview -run Legacy -count=1`.
- **Depends on:** FC1, FC3. **Red step:** the design's — the table's own tests fail against a
  function that returns the class only when the field is present. **Reviewer:** standard. **Commit:**
  `feat(runview): read a legacy record's class from its reason`.

#### Level 4 — R6, R8, R9, FC6, FC7

**R6 — the learnings' two ends, documented (slice P).** Part I, `## R6`. **Sequenced after #1488.**
- **Files (3):** `CONTRIBUTING.md`, `docs/periodic-reviews/README.md` (the status line), and
  `plugin/skills/working/reference/periodic-reviews.md` (§7).
- **Interfaces:** none — two sentences and a status line.
- **Tests, and the command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs|Backlog' -count=1`.
- **Depends on:** R1–R4, R7 and R12, and #1488. **Red step:** none of its own; its pin is the Docs
  gate. **Reviewer:** standard. **Commit:**
  `docs: the learnings' two ends — the backlog item and the skill sentence`.

**R8 — the project aggregate (slice P).** Part I, `## R8`.
- **Files (3):** `internal/cli/runs_reports.go` and `runs_reports_test.go` (R11's files), and
  `internal/digest/store.go` (R11's: the aggregate's own shape).
- **Interfaces:** the aggregate step after the loop, and the aggregate's shape in the store — which
  by **D1432**'s recommendation carries the period's **Failures table** beside its figures, and still
  names no rule: a table of what happened is figures, and Part I's "no pattern rules in the first
  slice" (E7) stands. **What the figures are about is D1433's and D1448's answer:** the aggregate
  counts the **repositories'** runs, not the reports job's own — the job is the machinery that
  reports the project's work, so its run is in the record and out of the total. **And it is not the
  reader of record beside the listing (D1434):** the stored report is the period's frozen figures and
  `runs ls --group-by` is the window's live ones, which is why the report's header names the period
  it closed.
- **Tests, and the command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'RunsReports|Aggregate'
  -count=1`.
- **Depends on:** R11. **Red step:**
  `TestRunsReportsWritesTheProjectAggregateBesideTheRepositoryReports` fails while the job writes the
  repository reports and stops. **Reviewer:** opus + mandatory independent review. **Commit:**
  `feat(cli): the project aggregate, beside the repository reports`.

**R9 — the opt-in commit (slice P). BLOCKED.** Part I, `## R9`. **Its task is approved — every task in
both parts is, as of 2026-11-07 — and the block is not an approval question:** it waits for the
designated-repo design item (**E6**), which is an **Open item** of this design and not one of the
decisions answered on 2026-11-07, so no approved task can lift it. It therefore has **no commit
message and no file list of record yet** — Part I writes
`Files (to be completed by that design item)`. **What it commits is already decided, and neither
decision is a task's:** the stored reports go in **as they are, table and all** (**D1441**), and a
stored report **may name run ids** — the id is what makes a flagged row actionable with `fugaro
diagnose RUN`, and E1 changed where a report lives rather than what it carries (**D1442**).
**Reviewer:** opus + mandatory independent review (the only path in this design that writes into a
repository). **Commit:** none until E6 is answered; the release does not wait for it (§P6).

**FC6 — `runs ls --group-by failure_class` (slice F).** The design, `### FC6`.
- **Files (6), the design's list:** `internal/cli/runs.go` (the flag, `lsOptions`, `lsDoc.Groups`,
  the refusals), `internal/cli/runview`-facing helpers, `internal/cli/runs_test.go`,
  `internal/cli/runid_test.go`, a golden under `internal/cli/testdata`, and
  `website/content/cli-reference.md` (**regenerated, not edited**).
- **Interfaces:** the `groups` block of `lsDoc`, and `--group-by`'s closed value list.
- **Tests, and the command — the design's, verbatim:** `TestRunsGroupByFailureClassNamesEveryClass`,
  `TestRunsGroupByRefusesAPage`, and the JSON case — one test per property, named in the task's own
  commit; `GOFLAGS=-p=2 go test ./internal/cli -run 'RunsGroupBy' -count=1`, then
  `go run ./ee/clidoc` and the CLI-reference test (F27).
- **Depends on:** FC5. **Red step:** the design's — the grouping test fails against a listing with no
  block. **Reviewer:** standard. **Commit:** `feat(cli): group runs ls by failure class`.

**FC7 — the Failures table and the two rules (slice F).** The design, `### FC7`.
- **Files (6), the design's list:** `internal/digest/digest.go` (`Failures`), `render.go`
  (`FailuresTable`, the summary block), `patterns.go` (the two rules and their constants),
  `digest_test.go`, `patterns_test.go`, `internal/digest/testdata`. **Not `internal/digest/store.go`**
  — see §P4.
- **Interfaces:** `FailuresTable`, the summary's `failures` block, and `failure_class_high` and
  `failure_class_new` with their constants. **Three decisions land here and none adds a rule:** the
  thresholds are one constants block in `internal/digest`, read by a stored series and a committed
  one alike (**D1446**); a class this build does not know prints as `unclassified`, the closed-cell
  rule FC5's reader already applies (**D1435**); and the table is a total by construction — a period
  is closed — so `runs ls --group-by`'s refusals to page and to window are the listing's own and the
  report needs none (**D1447**).
- **Tests, and the command — the design's, verbatim:** `TestDigestFailuresTableIsClosed`,
  `TestFailureClassHighNeedsItsWeeks`, `TestFailureClassNewIgnoresBlocksWithoutTheField`;
  `GOFLAGS=-p=2 go test ./internal/digest -count=1` (the whole package, which is what the design's
  own line says).
- **Depends on:** FC5 — **and, in the union, R11 (3)**, because the series these two rules read comes
  from R11's stored reader (E7). **Red step:** the design's — the two rule tests fail with the four
  rules alone. **Reviewer:** standard. **Commit:**
  `feat(digest): a period's failures by class, and two rules`.

#### Level 5 — R10, FC8

**R10 — `fugaro runs digest` generates to the bucket (slice P).** Part I, `## R10`. **Its bucket
write is blocked on Open item 5**, the way R9 is blocked on E6; what is not blocked is the default
target's own change.
- **Files (5 — 4 in Part I, plus the F27 correction):** `internal/cli/runs_digest.go`,
  `runs_digest_test.go`, `docs/recipes.md`, `internal/digest/store.go`, and
  **`website/content/cli-reference.md`** — the command's `Short`/`Long`, its stored read's flag and
  its default target are what the reference prints for it, and Part I's list does not name the file
  (F27).
- **Interfaces:** the default target, `--dir` for a checkout, and the stored read — which prints what
  `--stdout` prints, so once FC7 lands the Failures table is in it (**D1440**).
- **Tests, and the command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'RunsDigest' -count=1`.
- **Depends on:** R8, R1 and R2. **Red step:** `TestRunsDigestWritesToTheBucketByDefault` fails while
  the command still writes into the repository's own tree. **Reviewer:** opus + mandatory independent
  review. **Commit:** `feat(cli): runs digest generates to the bucket by default`.

**FC8 — the documents, the contract and the skills (slice F).** The design, `### FC8`.
- **Files (6), the design's list:** `docs/design/v1.md` (§4.6's field), `console/docs/json-contract.md`
  (two rows), `docs/task-governance/reconciliation.md` **or** `docs/periodic-reviews/reconciliation.md`
  — the one that owns the rule list, which **D1449** names as this folder's, so the recommendation is
  the second — `plugin/skills/working/reference/diagnose.md` (one sentence, only if O1's answer wants
  a person to use the grouping), `internal/cli/docs_desktop_console_test.go` (the contract's pins, if
  a row moves), and `docs/releases/vX.Y.Z.md`'s `### For operators`.
- **Interfaces:** none — it writes no behaviour.
- **Tests, and the command — the design's, verbatim:** the pins that already read those documents
  (`TestDocsDesktopConsoleContractNames…`, `TestDocs` over the changed design);
  `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs' -count=1`.
- **Depends on:** FC6, FC7. **Red step:** none of its own; its gate is the Docs run. **Reviewer:**
  standard. **Commit:** `docs: the failure class on the record, the contract and the report`.

### P6. The critical path

**The union's longest chain is the reports job's, and the failures report does not lengthen it.**
Part I's chain is **R0 → R1 → R4 → R11 → R8 → R10**, six tasks over six levels. The failures report's
own chain is **FC1 → FC2 → FC3 → FC5 → FC6 → FC8**, also six tasks over six levels, and it runs
*beneath* the first rather than after it: it starts at level 0 beside R0 and ends at level 5 beside
R10. **The works meet in three places, and only one of them is a dependency rather than an
ordering:**

- **R11 (3) → FC7 (4).** FC7's two rules read the summaries R11's stored reader returns (E7), so
  FC7's own test of a stored series needs that reader to exist. The design states FC7's dependency as
  FC5 alone; **in the union it also waits for R11**, and this is the one edge neither document can
  state by itself.
- **R4 (2) → FC7 (4)** through `internal/digest/patterns.go`: the fifth rule and the two class rules
  are one `RuleNames` slice and one constants block, so the file's own order is R4, then R11, then
  FC7. That is the shared-files rule doing its work rather than a new dependency.
- **R11 (3) → FC6 (4)** through `website/content/cli-reference.md`: the file is generated from the
  command tree, so its four writers regenerate it each in their own commit (R1 at 1, R11 at 3, FC6 at
  4, R10 at 5) and the levels are what keep the bytes right at each merge — an ordering, not a
  dependency, and the reason **D1438** is four regenerations rather than one.

**The long pole is FC2**, the design's own reading: thirty-six statements across eight files in
`internal/runner` become one call, in a package whose suite costs about 25 minutes — which is why
FC2's test is a guard over the tree and not a fixture per site. **R12 is the union's widest gate**:
it waits on an owner-run check (Check 340, §P8), and neither work's schedule may be enabled before
its grants are proven. **R9 is off the path entirely** — blocked on E6, it is not in the release, and
nothing in either work depends on it.

### P7. Release slot, and the operator text

**CLI 0.22.0 "Sloth",** as the owner decided on 2026-11-07. The slot is not this plan's to choose and
the plan takes it as given; what the plan owes is that the release is **one release** (§P3 shows it
can be) and that its operator step is the HARD RULE's whole upgrade.

**The `### For operators` entry is two lines — one command, and one thing to turn on:**

1. "Upgrade the CLI and run `fugaro update` in each checkout. It brings the installation (Terraform,
   the reports job, its account, its paused schedule and the `reports/` lifecycle rule) and the
   checkout to this release, and it asks for the typed project name where it plans the reports
   grants."
2. "**Both jobs are created paused, and `fugaro update` is what turns them on.** Set
   `jobs.reports.paused: false` — and `jobs.catalog_check.paused: false` for the catalog check — in
   the project layer and run `fugaro update` again. Nothing is committed anywhere until you run the
   opt-in commit, which is a separate run (R9)."

**The class field owes no operator step at all, and that is the HARD RULE's other half.**
`failure_class` is additive on `result.json` and on `runs ls --json` (the design's §7 and §10.3): it
publishes no policy node, moves no schema version, changes no image contract and changes no plugin
pin, so an installation at the previous version converges with no migration and **no backfill** — the
class of a run that predates the field is read from its `reason` (FC5), and that reading is never
written back. **The plugin pin is the one thing that must move with the CLI**: R1, R3 and R10 ship in
one release because a skill that names `--due`, or one that says where a report lands, must not reach
a CLI whose behaviour differs (Part I, "Release slot"), and `fugaro upgrade`'s `pin` step is what
decides that.

### P8. The owner-run checks

**Check 340, Check 341 and Check 342 are this round's; 343–349 are reserved and unused.** All three
are the owner's: **no agent performs any step of them.** They are claimed rather than reserved, and
the claim is against the register's own census, taken on 2026-11-07: **the highest written
`## Check` section is 290**; 291 (`docs/image-env`), 292 (`docs/catalog-build-context`), 300–309
(`docs/stateless-orchestrator`) and 310–314 (`docs/cli-output-console-graph`) are claimed on `main`,
and 330 is claimed by `docs/api-key-review`, which was an open head at commit `c3562215` when this
census was taken and **has merged to `main` since, as `0dd2b5f2` (#1819)** — so 330 is a claim on
`main` now and still not a section in the register.
**Nothing writes 315–329, 331–339 or 343–349, and the only claim above this block is 360**, which
is `docs/console-managed-screens`'s — a work in flight with this one that has merged to `main` since
(`7d617d27`, #1830), taking its block above 340–349 rather than inside it.
`docs/gcp-live-checklist.md` is another work stream's file,
so **this folder names the numbers and writes no section**: the task that implements each check
writes its row and its section under the number free at that merge, which is the register's own rule
for a work that reserved none (`docs/gcp-live-checklist.md:586` — "which is the register's rule for a
work that reserved none" — and `docs/run-limit-carry-over/README.md:55` is the same rule read from a
work that took none). Two of the three replace
the two live checks Part I "names and does not number" (R12's listing check and R7's own firing) —
the numbering is this part's, the checks are Part I's. **Amended 2026-11-07:** the census above is
the reading the numbers were taken at, and Check 340 is written since: its task (§P8) wrote the
register's row and its `## Check 340` section, so the highest **written** `## Check` section is
**340**, 341 and 342 stay claims until their own tasks write them, and this part's first check is
one that names a section rather than only a number (the `Checks 340–349` row of
[README.md](README.md); [reconciliation.md](reconciliation.md) §5's amendment).

**Check 340 — the prefix-limited listing grant (R12's, and it gates R12).** The owner proves that the
job's read grant lists `runs/<slug>/` and `reports/`, reads one `runs/` object, and is refused the
root listing and a `tokens/` read and a `tokens/` or `cache/` listing — which is what settles
`storage.googleapis.com/objectListPrefix`'s own value form, the attribute `docs/design/bucket-iam.md`
records as unverified (V7). **If it fails, R12's Terraform does not land and the plan stops and
asks** (E4). Owner-run, **NOT RUN**.

**Check 341 — the reports job's first firing, and what it stored.** The owner sets
`jobs.reports.paused: false`, runs `fugaro update`, lets the schedule fire once and reads what
landed: one report per repository under `reports/`, the project aggregate beside them, and the
`reports/` objects still there after the run records' own 90 days — which is the 18-month rule
(E3) doing what it is for. It also settles the question R10 is blocked on (Open item 5): **whether
`fugaro runs digest` can write to `reports/` at all**, since E4 makes the job its only writer.
Owner-run, **NOT RUN**.

**Check 342 — the failures report reproduces a window a person built by hand.** The owner takes a
window they already know the causes of — the design's own seven-day table is one — and checks that
`fugaro runs ls --group-by failure_class` and the Failures table in the period's report agree with
it, including the runs whose class the legacy reader read from `reason` (FC5). It is the one check
that says whether the vocabulary means what the design says it means. Owner-run, **NOT RUN**.

### P9. What is deliberately not in this part

- **The console.** The contract's two rows are FC8's; a console column or chip is a product decision
  and a second release stream (`console-vX.Y.Z`), so it is named and left to its own task (D1439).
- **The yearly report's roll-up of the class table** is stated as a shape (D1443) and is not a task
  in this release: the yearly report already rolls twelve stored monthlies up, and the table rides
  that roll-up rather than adding one.
- **`docs/periodic-reviews/spec.md`.** Not rewritten, here or anywhere: it keeps its 2026-11-04 text
  and is read through [reconciliation.md](reconciliation.md) §5 and §6.
- **The two `report`-shaped rules' behaviour in a period that predates the field.** D1431 is the
  decision, and until it is answered the plan reads "a summary without the block is not read".
- **The shipped `run-digest` recipe's own report.** No task here edits the recipe: it writes through
  `internal/digest`'s own writer, so the Failures table reaches its report the moment FC7 lands, and
  **D1445**'s recommendation is that it does (Open item 4).
- **A backfill of `failure_class` over stored records.** Deliberately absent: the record is 90 days
  old at most, the reading is not written back (FC5), and a backfill would be a cloud write no
  operator step asks for (the HARD RULE).

### P10. Gates

**Part I's gates apply to every task above, unchanged** ("Gates (every task)"): `go build ./...`,
`gofmt -l .` printing nothing, `go vet ./...`, the task's own named tests, and the Docs run for any
task that touches a pinned document. Four additions, and they are the union's:

- **`go run ./ee/clidoc` after R1, R11, FC6 and R10, each in its own commit** —
  `TestCliReferenceIsCurrent` compares the committed file to the command tree, and the four tasks add
  a flag, register a subcommand, add a flag, and rewrite a command's own text and add its stored
  read's flag (F27, §P4).
- **`GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1` after FC1, FC2, R13, FC8 and the release
  entry** — `-run Docs` reaches every `TestDocs…` in `internal/cli`, and those read
  `docs/design/failure-classes.md` (`TestDocsFailureClassDesignPinsTheVocabulary`,
  `TestDocsFailureClassCensusMatchesTheRunner`, `TestDocsFailureClassDesignCitationsResolve`),
  `docs/design/v1.md`, `console/docs/json-contract.md` and `docs/config-management/**`. **FC1 and FC2
  both write the pin itself** (`internal/cli/docs_pr_state_test.go`), FC2 also rewrites the design's
  §1 census sentence, R13 writes the config-management documents, and FC8 writes the design, the
  contract and the release note. **R13's and R6's Docs run is Part I's own rule** ("when a doc
  changed") and **FC8's own command is the Docs run already** (the design's §10), while **FC1's and
  FC2's commands run no `./internal/cli` package at all** — which is why this bullet states the gate
  for the two of them rather than leaving it to Part I's sentence.
- **`GOFLAGS=-p=2 go test ./internal/cli -run Recipes -count=1` after R1 and R10** —
  `docs/recipes.md` is pinned by `TestRecipesGuideMatchesTheCLI`, **whose name `-run Docs` does not
  match**, and both tasks write the file: R1's `--due` sentence and R10's sentence about where a
  report lands.
- **A task whose file list carries a test its own command does not reach runs it too** — FC1 and FC5
  write `internal/cli/runs_test.go` (the record's field cases, and the legacy reader's) and FC6 writes
  `internal/cli/runid_test.go` (the default-window guard that reads the same constants), and none of
  the three tests is matched by its task's own command — FC6's `-run 'RunsGroupBy'` is the flag's own
  tests, not the guard's. Each therefore adds `GOFLAGS=-p=2 go test ./internal/cli -run '<the names
  its commit adds>' -count=1` beside the design's command: the design names those tests in the task's
  own commit rather than in §10, which is why this gate names the package and not a pattern.
- **The release itself is not a task and has no level**: it is `scripts/release.sh 0.22.0` from a
  clean clone ([docs/release.md](../../docs/release.md)), after every level, with §P7's entry as its
  `### For operators`.
