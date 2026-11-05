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
  **R7, R11, R8, R10**; E2 (D322/G2 is a default, not a prohibition) — R8, R9; E3 (18 months on
  `reports/`) — R7; E4 (one project-level writer) — R7 and R11; E5 (committing is opt-in and
  separate) — **R9**; E6 (the designated repo or folder is a separate design item) — R9 depends on
  it, **Open items**; E7 (the rules read the stored reports) — R4 and R11; E8 (`fugaro runs digest`
  generates to the bucket) — **R10**.
- **What the owner decided on 2026-11-05 after this round's review**, and where each answer is
  written: the job's listing is a narrow grant on `runs/`, verified first by an owner-run live check
  (R7, "The listing check"); `reports/` has **one writer, the job**, and launchers read it (R7,
  "IAM, exactly"; R9's and R10's SEC lines); enabling either job is **a setting `fugaro update`
  writes**, not a `gcloud` command (R7, "The setting", and the release slot). One question the
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
- **R7 and R11 are the two halves of what was one task**, split after the review of this plan: R7 is
  the job's Terraform and infra (the job, its account, its Scheduler job, its grants, the guard's
  principal kind for the account, the `reports/` lifecycle rule, the setting that enables it and the
  `fugaro update` that plans them), and R11 is its code (the command, its loop over the repositories
  and the stored reader). They are split because the two halves are gated differently — R7 is
  Terraform a `fugaro update` plans and an owner-run check has to settle first, R11 is code a
  hermetic test covers — and because one task listing 16 files broke this plan's own cap of 12.
  **R7 can be built at once**; R11 waits for it, and for R4.
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
- **`deploy/terraform/gcp/modules/installation/`** is edited by R7 alone (the reports job, its
  account, its schedule, its alert and the `reports/` lifecycle rule). No other task in this plan
  touches Terraform, so the module has one writer.
- **`docs/gcp-live-checklist.md` is not touched by any task**: the register is another work stream's
  file, the design takes no check number (README, "Numbers this work holds"), and R7's two live
  checks are **named**, not numbered — the task that writes each takes the number free at its merge
  ([reconciliation.md](reconciliation.md) §5, "What the rulings take").

| Level | Tasks | Needs |
| --- | --- | --- |
| 0 | R0, R7 | R0: nothing. R7: **the listing check's result** (its own owner-run live check, before its Terraform lands) — and nothing in this plan |
| 1 | R1 | R0 |
| 2 | R2, R4 | R1 |
| 3 | R3, R11 | R3: R4 (the skill's rule list names the rules R4 adds) and #1488 landed; R11: R7 (the job's spec and its own assignment) and R4 (the rules' source, E7) |
| 4 | R6, R8, R9 | R6: R1–R4 and R7, and #1488; R8: R11 (the job's own loop); R9: R11 (what it stored) **and the designated-repo design item (E6, Open items)** |
| 5 | R10 | R8 (the writer it writes through), R1 and R2 (`internal/cli/runs_digest.go`) and R6 (`docs/recipes.md`) |
| — | R5 | the owner, at their terminal; no agent step |

**No level holds two writers of one file.** Three files have three writers each, and each is ordered
the same way: `internal/digest/store.go` (R11 the reader, R8 the aggregate's writer, R10 the command's
own write path — levels 3, 4 and 5), `internal/cli/runs_digest.go` (R1, R2, R10 — levels 1, 2 and 5)
and `docs/recipes.md` (R1, R6, R10 — levels 1, 4 and 5). **R10 is therefore alone at level 5**, and
not at level 4 beside R8: level 4 is where R6 sits, the second writer of `docs/recipes.md`, so a
level-4 R10 would trade one collision for another. The split of the old R7 is the other half of the
check: `internal/digest/patterns.go` is R4's and R11's, one level apart, and the Terraform module,
`internal/infra/tf/cover.go` and the installation's Go spec are R7's alone. The shared-files table
below is the check.

**The critical path is R0 → R1 → R4 → R11 → R8 → R10**, and it is the rulings that lengthened it:
before 2026-11-05 the longest chain was three levels (R0 → R1 → R6). **R11 is what everything new
waits on** — R8 writes the aggregate in its own loop, R9 commits what it stored, R10 makes the bucket
the shipped command's default target through that writer — so a schedule enabled before R11 lands has
nothing to run, which is the other reason the whole of R11 has to be in the release that ships the
plan. **R7 is off the path entirely**: it waits on the listing check and on nothing else, so the
Terraform half can land first, and R6 — which needs R7 and R3 but not R11 — leaves the path at level
4 rather than holding it to level 5.

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
| `internal/cli/runs_digest.go` | R1, R2, R10 | R1 adds `--due`; R2 calls the same reading for doctor; R10 changes the default target (E8) — levels 1, 2 and 5 |
| `internal/digest/patterns.go` | R4, R11 | R4 adds the fifth rule; R11 changes where the summaries it reads come from (E7) — levels 2 and 3 |
| `internal/digest/patterns_test.go` | R4, R11 | R4's rule cases; R11's `TestPatternsReadTheStoredReports` |
| `internal/digest/store.go` (new) | R11, R8, R10 | R11 writes the bucket-backed reader; R8 adds the aggregate's writer; R10 is the command's default path into it — levels 3, 4 and 5, one writer per level |
| `internal/cli/runs_reports.go` (new) | R11, R8 | R11 writes the job's loop over the repositories; R8 writes the aggregate after it — levels 3 and 4 |
| `internal/cli/runs.go` | R11 | the one line that registers `runs reports` beside `runs digest` |
| `internal/infra/tfvars.go`, `internal/infra/testdata/installation-*.tfvars.json` | R7 | the new `reports` node, the `paused` members, and the fixtures that carry every installation shape |
| `internal/infra/tf/cover.go` | R7 | the reports account's own principal kind, and the grants it must refuse |
| `deploy/terraform/gcp/modules/installation/bucket.tf`, `variables.tf`, `catalogcheck.tf`, `roots/installation/` | R7 | the `reports/` lifecycle rule, the setting's pass-through and the job's variable |
| `plugin/skills/working/reference/periodic-reviews.md` | R3, R6 | R3 adds the due signal, the schedule and the rule list; R6 adds §7's sentences — levels 3 and 4 |
| `docs/recipes.md` | R1, R6, R10 | the `--due` pointer, the checklist line, and the sentence E8 changes (R10) — levels 1, 4 and 5 |
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
and R11 adds is a local read or a local write that a hermetic test covers. **R7 is the one task whose
behaviour cannot be covered hermetically**, so it names two live checks (R7, "The listing check" and
"The job's own live check") and each takes the number free at the merge of the task that writes it.

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

## R7. The reports job's Terraform and infra: one project-level writer into the bucket (E1, E3, E4)

The rulings' centrepiece, and the half of it this plan builds at once. One job in the project, on the
0.18.0 catalog check job's pattern (`deploy/terraform/gcp/modules/installation/catalogcheck.tf`,
`internal/infra/catalogcheck.go`): its own narrowly granted account, **shipped paused behind a
setting**, planned by `fugaro update`. Nothing about it is a model call and nothing about it is a
commit; the code that gives it its work is R11.

- **What it is.** The job is `fugarorpt-reports` and **its name must start with `fugarorpt-`**, for
  the reason `fugarochk-` exists: `fugaro runs ls` and every listing count the `fugaro-` ones as
  workflow jobs, and the alert policies match the prefix (`internal/infra/catalogcheck.go:44`,
  `alert.tf:55`). The account is `fugaro-reports`, the Scheduler job `fugaro-reports` **in the
  installation's own scheduler region** — `scheduler_region`, the one the catalog check's Scheduler
  job carries (`modules/installation/catalogcheck.tf:191`), not the installation's `region` — and its
  schedule ships paused.
- **The setting (E4).** Enabling a job is **a setting `fugaro update` writes**, not a `gcloud`
  command: the project layer gains a `jobs:` block, **`jobs.reports.paused`** and
  **`jobs.catalog_check.paused`**, each defaulting to `true` (paused). `internal/infra` renders them
  into the root's `reports` node and into the catalog check's own variable, and the modules apply
  `paused` from them. **Two reasons this is the task for it:** a `gcloud scheduler jobs resume` is
  reverted by the next `fugaro update`, because the module always applies `paused` — and the 0.18.0
  catalog check has the same gap today from the other side, `paused` being a Terraform variable with
  **no field in `CatalogCheckSpec`** (`internal/infra/catalogcheck.go`) for a run to set it from, so
  its own operator note offers no command that sticks. R7 adds that field and wires it, for both
  jobs. Registry rows `layer.jobs.reports.paused` and `layer.jobs.catalog_check.paused` are part of
  the change (`internal/settings`' own rule: a `config.ProjectLayer` field without a row fails
  `TestRegistryCoversEveryConfigField`), and the registry's pinned counts move with them —
  `docs/config-management/README.md:47`, `docs/config-management/reconciliation.md:22` and
  `internal/cli/docs_config_management_test.go:99` (`cfmRegistryRows`), which the task updates in the
  same change.
- **Its command, and what it writes:** R11's. R7's job carries the assignment in its environment
  (`FUGARO_REPORTS_JOB_SPEC`) and the grants below; nothing about the report's content is R7's.
- **IAM, exactly (E4), and what the owner decided about it.** The account holds
  **`roles/storage.objectViewer` on `runs/`**, so it can list `runs/<slug>/` and read the records it
  summarises, and **`roles/storage.objectUser` on `reports/`**, so it can read a stored report back,
  write it, overwrite it and delete it under the `reports/` lifecycle. A condition on
  `resource.name` **grants no listing** (`docs/design/bucket-iam.md` §2.3; live check 7 recorded a 403
  on listing `runs/` for a job account), so the listing half needs the prefix-limited mechanism
  `storage.googleapis.com/objectListPrefix` — **unverified in this repo** (bucket-iam §4, V7) — and
  **the owner ruled it is verified before R7's Terraform lands, by an owner-run check, not assumed**
  (below). **No git credential, no model credential, no secret**: the account holds no project role,
  the job mounts no secret, and its env carries the platform variables and `FUGARO_REPORTS_JOB_SPEC`
  and nothing else. **The launcher side is a read, not a second writer:** a launcher's unconditioned
  bucket-wide `objectViewer` already covers the new prefix, so a person can read a stored report and
  **nothing new is granted to a person** — the launcher's own write condition
  (`gcp.LauncherBucketCondition`, `docs/design/bucket-iam.md` §4, H5) covers `runs/` and `tokens/`
  and stays as it is: `reports/` is **not** added to it, because the job is the one writer (ruling 3).
  Operators keep the `objectAdmin` they hold bucket-wide. The
  guard's own kind comes with it: `internal/infra/tf/cover.go` gives each project account a principal
  kind of its own (`toCatalogCheck`, `CatalogCheckAccountID`,
  `TestCatalogCheckAccountIsItsOwnPrincipalKind`), and the reports account gets `toReports` beside it,
  so no `toAccounts` row can be extended to it and every other role of it is refused and asks the
  owner.
- **The retention rule (E3).** `bucket_lifecycle` gains **`reports_days = 548`** (18 months, in whole
  days as its members are — `internal/infra/tfvars.go:757`) and `bucket.tf` **its fifth
  `lifecycle_rule`** (the bucket carries four today: `runs/`'s age, `cache/`'s two, and the sealed
  token's), matching `reports/`; `runs/` keeps its 90 (`F24`, untouched).
- **`fugaro update` plans it (HARD RULE).** `internal/infra/reportsjob.go` (the spec type, the env
  document and its version) and the `reports` node in `internal/infra/tfvars.go`; the module's
  `reports.tf` and its `variables.tf` entry; the pass-through in `roots/installation/`. An
  installation at the release before this one **converges in one `fugaro update`** — the plan creates
  the job, its account, the Scheduler job and the bucket's fifth rule, and asks for the typed project
  name where it plans the reports grants, as the catalog check's own update does.
- **Files (12):** `internal/infra/reportsjob.go` (new: the spec type, the env document and its
  version, and the setting's two render paths), `internal/infra/reportsjob_test.go` (new: the names
  below that are not the guard's, and the convergence test),
  `internal/infra/tf/cover.go` (the `toReports` kind and `ReportsAccountID`),
  `internal/infra/tf/cover_grants_test.go` (`TestReportsAccountIsItsOwnPrincipalKind`),
  `internal/infra/catalogcheck.go` (the `paused` field the catalog check never had),
  `internal/infra/tfvars.go` (the `reports` node, the two `paused` members, and the fixtures under
  `internal/infra/testdata/` its test compares),
  `deploy/terraform/gcp/modules/installation/reports.tf` (new: the job, its Scheduler job, its
  grants and its alert), `deploy/terraform/gcp/modules/installation/variables.tf`,
  `deploy/terraform/gcp/modules/installation/bucket.tf` (the fifth rule),
  `deploy/terraform/gcp/modules/installation/catalogcheck.tf` (the setting's pass-through to the
  catalog check's own job — the alert for both jobs lives in `reports.tf` beside them, so `alert.tf`
  is not opened), `deploy/terraform/gcp/roots/installation/variables.tf` and `main.tf`.
- **Red step:** `TestUpdatePlansTheReportsJobPaused` fails while the setting is not wired (the job's
  `paused` is missing from the rendered tfvars, and the module's default keeps it paused with no way
  to say otherwise), `TestCatalogCheckSpecCarriesThePausedSetting` fails while the catalog check's
  spec still has no field for it, and the convergence test fails while the plan would replace the
  installation's state rather than add to it.
- **Tests:** `TestReportsAccountIsItsOwnPrincipalKind` (`internal/infra/tf`),
  `TestUpdatePlansTheReportsJobPaused`, `TestCatalogCheckSpecCarriesThePausedSetting`,
  `TestBucketLifecycleCarriesReportsAtEighteenMonths`,
  `TestReportsJobAccountHoldsNoSecretAndNoProjectRole`,
  `TestReportsJobNameIsNotAWorkflowJob`, `TestReportsSchedulerJobIsInTheSchedulerRegion`, and the
  HARD RULE's own convergence test —
  **`TestInstallationAtThePreviousVersionConvergesToTheReportsJob`**: an installation at the previous
  version, with the tfvars and the stored state that release wrote, converges in one `fugaro update`,
  with the job created **paused** — and **still paused after a second update until
  `jobs.reports.paused` says otherwise** — and no second command, no deletion and no manual repair.
  It is a `-tags terraform` test, the way `internal/infra/installation_terraform_test.go` runs the
  real plan; the convergence claim's own shape is
  `TestInstallationAtThePreviousVersionConvergesToTheMix` (`internal/infra/retire_test.go:1939`),
  which plans through Go and is **not** Terraform-tagged.
- **Hermetic:** the Go tests run against the module's own fixtures; the Terraform-tagged test runs the
  real plan.
- **Reviewer:** opus + mandatory independent review (an IAM grant, a bucket lifecycle rule and a
  credential-less job: the three things this plan lets near a project's cloud, and the account is
  narrower than any other job's).
- **The listing check, named and not numbered, and R7 depends on it.** The owner ruled this round
  that `objectListPrefix` is verified **first**: the check is the owner's, **no agent performs any
  step of it**, and it settles R7's first Terraform plan. The owner, at their terminal, on the
  sandbox installation, signs in as the project and grants a throwaway service account
  `roles/storage.objectViewer` on the runs bucket **with the prefix condition**
  (`storage.googleapis.com/objectListPrefix` for `runs/`), then, as that account, runs
  `gcloud storage ls --recursive gs://<bucket>/runs/<slug>/` (it must list) and
  `gcloud storage ls gs://<bucket>/tokens/` and `gcloud storage ls gs://<bucket>/cache/` (both must
  be refused), and removes the throwaway account afterwards. **If the listing half fails, the plan
  stops and asks the owner** — the bucket-wide viewer a listing needs when the condition cannot carry
  one is **not** planned as a fallback here, because it would also read `tokens/` and `cache/`.
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

R7's code half: the subcommand the job runs, the loop that writes the reports, and the reader every
writer's summaries come from. Hermetic throughout — it touches no cloud API the fake does not model.

- **Its command.** `fugaro runs reports --job`, registered beside its sibling in
  `internal/cli/runs.go:232` (not `root.go`, which another work stream holds). It reads its whole
  assignment from **`FUGARO_REPORTS_JOB_SPEC`** — a versioned JSON document on the
  `FUGARO_CATALOG_CHECK_SPEC` shape (`internal/infra/catalogcheck.go:23`), which R7 declares: the
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
- **Why it is last:** it touches three files earlier tasks hold — `internal/cli/runs_digest.go` (R1,
  R2), `docs/recipes.md` (R1, R6) and `internal/digest/store.go` (R11, R8) — so it sits alone at
  **level 5**, above every one of them, and the shipped command's default changes once, at the end.
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

Five questions the rulings raise and **this plan does not decide** — four from the rulings of
2026-11-05 and one the answers to this round's review open — each one written where the task that
meets it says so:

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

## Release slot

**CLI 0.19.0.** 0.15.0, 0.16.0 and 0.17.0 have all shipped since this design was written,
`docs/config-management`'s own plan is 0.18.0, and the rulings of 2026-11-05 make this a cloud
change, so **0.19.0 is still the first release this work can ride**
([reconciliation.md](reconciliation.md) §4, M8) — and it now has to carry the whole of R7 and R11,
because a schedule without the job that reads it is worse than no schedule. The release notes'
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
a run to set) — **R7 closes both**, and `fugaro doctor`'s update reading is what tells the owner the
update is due. The plugin pin — `fugaro upgrade`'s
`pin` step — is what puts the matching plugin and skills into a checkout
(`internal/cli/upgrade.go:80`, `:86`). That is exactly why **R1, R3 and R10 ship in one release**: a
skill that names `--due`, or a skill that says where a report lands, must not reach a CLI whose
behaviour differs, and the pin is what decides that. R3 and R6's §7 sentence are also sequenced behind
#1488's rename of `plugin/**`. **R9 waits for the designated-repo design item (E6)** and is the one
task here that is not gated by a release. R5 is the owner's whenever they run it.
