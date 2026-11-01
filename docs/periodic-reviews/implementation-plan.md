# Periodic run reviews: implementation plan

**Nothing here is approved.** Every task is written to the recommended answer of a decision the owner
has not answered (D960–D971, [reconciliation.md](reconciliation.md) §3). Each task touches at most 12
files, names its tests, starts with a red step and ends at the gates below.

## Order, and what this plan does not own

- **The loop itself is shipped** (F1–F13). No task here rewrites the digest, the recipe, the runner's
  step, the layout, the flags, the draft or the issue form.
- **`plugin/**` is owned** by another 0.15.0 work stream (the `docs/plugin-user-scope` design, PR
  #1488, moves the skills to user scope and rewrites the setup skill's session-start behaviour).
  R3 touches three skill files and their tests; it waits for the owner's go on that directory, and
  R4 waits for R3 because the skill's §3 lists the rules.
- **`website/**` is owned**, including `website/data/typical-costs.yaml`. R5 names the one line its
  provenance needs and changes nothing there.
- **`internal/runner/runner.go`, `internal/cli/root.go`, `internal/cli/api.go` and everything under
  `ee/`** are owned or off-limits: no task touches them. R1 adds a flag to an existing subcommand, so
  it needs no registration in `root.go`.

| Level | Tasks | Needs |
| --- | --- | --- |
| 0 | R0 | nothing |
| 1 | R1 | R0 |
| 2 | R2, R4 | R1 (R4 also R3) |
| 3 | R3 | R1, and the owner's go on `plugin/**` |
| 4 | R6 | R1–R4 |
| — | R5 | the owner, at their terminal; no agent step |

## Gates (every task)

`go build ./... && gofmt -l . && go vet ./...` (gofmt prints nothing); the task's named tests with
`GOFLAGS=-p=2 go test ./internal/<pkg>/ -run '<names>' -count=1`; `GOFLAGS=-p=2 go test
./internal/cli/ -run Docs -count=1 -timeout 40m` when a doc changed; `go test
./internal/eeboundary/... -count=1`. Never the whole `internal/cli` or `internal/runner` package.

## Shared files

| File | Tasks | Why they meet there |
| --- | --- | --- |
| `internal/digest/due.go` (new) | R0, R1 | R0 writes it; R1 reads it |
| `internal/runner/digest.go` | R0 | the constants and the function move out of it |
| `internal/cli/runs_digest.go` | R1, R2 | R1 adds `--due`; R2 calls the same reading for doctor |
| `internal/digest/patterns.go` | R4 | the fifth rule |
| `plugin/skills/working/reference/periodic-reviews.md` | R3, R4 | R3 adds the due signal and the schedule; R4 adds the rule to the list |
| `docs/recipes.md` | R1, R6 | the `--due` pointer, then the checklist line |
| `docs/periodic-reviews/README.md` | R6 | the status line, once a task lands |

## R0. The due computation moves into `internal/digest`

One implementation of "which periods has this repository closed and not reported", read by a report
run and by a CLI read (F6, F17). Nothing about the runner's behaviour changes: the same days, the
same weekday and month-boundary rules, the same refusal of a period the layout cannot name.

- **Interface:** `func DuePeriods(now time.Time) []Period` in `internal/digest/due.go`, with the
  catch-up window `DueCatchUpDays = 7` — the same value `internal/runner`'s `digestCatchUpDays`
  holds today (F6), moved rather than duplicated.
- **Files (4):** `internal/digest/due.go` (new), `internal/digest/due_test.go` (new),
  `internal/runner/digest.go` (the call site; the constant and the function go),
  `internal/runner/digest_test.go` (its existing due-period test moves and keeps its cases).
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

## R2. The `doctor` line (D963)

`reviews-due`, from the same reading as R1: an info line when the newest report the checkout holds is
more than one period old, a warning line when a period has closed and has no report at all, and
silence when the checkout holds no report folder and no report is due (a repository that has never
reviewed is told once, not on every run). Offline. It belongs to neither the update group nor the
security group (`internal/cli/doctor_render.go:100`, `:118`), so it needs no change there.

- **Files (3):** `internal/cli/doctor_reviews.go` (new: the check and its one-line fix),
  `internal/cli/doctor_reviews_test.go` (new), `internal/cli/runs_digest.go` (only if the reading
  needs a second entry point to be callable from doctor; search before writing one).
- **Red step:** `TestDoctorReviewsDueWarnsWhenAClosedPeriodHasNoReport` fails while the check does
  not exist, and it fails for the reason the task exists: today a user runs `doctor`, sees a clean
  screen and never learns that the cadence lapsed.
- **Tests:** `TestDoctorReviewsDueWarnsWhenAClosedPeriodHasNoReport`,
  `TestDoctorReviewsDueIsInfoWhenTheNewestIsOld`, `TestDoctorReviewsDueIsSilentWhenNothingIsDue`,
  `TestDoctorReviewsDueIsOffline`, `TestDoctorReviewsDueNamesTheCommand`.
- **SEC:** none.

## R3. The skills carry the signal and the schedule (D965). **Owned elsewhere**

The `setup` skill gains one step: it prints the three ways to schedule the reviews — a cron job or CI
workflow of the repository, an agent routine, a scheduled report run — with the exact command for
each, and writes nothing without the user's word. The `working` skill's reference gains the due
signal (R1) where it describes the cadence, and keeps the sentence that an agent never sets up a
schedule on the user's behalf.

- **Files (4):** `plugin/skills/setup/SKILL.md`, `plugin/skills/working/SKILL.md`,
  `plugin/skills/working/reference/periodic-reviews.md`, `plugin/periodic_reviews_skill_test.go`
  (the pointer stays; the new sentences are pinned: the schedule is printed, never written, and the
  due signal is named with its command).
- **Red step:** the new plugin assertion fails while the setup skill names no review cadence — which
  is the state of `main` today (F18).
- **Tests:** the plugin package's own tests, run as `GOFLAGS=-p=2 go test ./plugin/ -count=1`, plus
  `pluginskillcheck`'s CLI-coverage test: every command and flag a skill names must exist, which is
  what keeps `--due` and the skill's sentence from drifting apart.
- **SEC:** none.

## R4. The fifth pattern rule (D968)

`cost_per_run_rising`: the billed cost per run rose in each of the last five committed monthly
reports. Same shape as the four (`RuleNames` order, period keys read oldest first, one figure per
period as an exact decimal string), same vocabulary, and a report that fires it still says only what
it read — a signal to look, never a verdict.

- **Files (3):** `internal/digest/patterns.go`, `internal/digest/patterns_test.go`,
  `plugin/skills/working/reference/periodic-reviews.md` (§3's rule list, with R3).
- **Red step:** `TestPatternCostPerRunRisingNeedsFiveMonthlyReports` fails while the rule does not
  exist; the boundary cases are what the task is for — a flat month breaks the run, a missing month
  is not a zero, and a period key the package did not write is in no series.
- **Tests:** `TestPatternCostPerRunRisingNeedsFiveMonthlyReports`,
  `TestPatternCostPerRunRisingStopsAtAFlatMonth`,
  `TestPatternCostPerRunRisingIgnoresAPeriodItDidNotWrite`,
  `TestPatternRuleOrderIsStable` (the JSON and the Markdown list the rules in `RuleNames` order).
- **Hermetic:** summaries written in the test, no folder, no cloud.
- **SEC:** none.

## R5. Fugaro's own reports (D961). **The owner's, at their terminal**

No agent step and no test: this is the one thing the design asks the owner to do, and it is the
registered Check 136, which is **NOT RUN** (`docs/gcp-live-checklist.md:764`, `:10612`). Run once
against a release carrying the finalize guard, on a sandbox repository first (`fugaro run --recipe
run-digest "periodic run review"`), then against this repository under D323's rules. What follows
from it is a pull request adding `docs/fugaro-reviews/` here, and — separately, owned by whoever
holds `website/**` — the one-line change to `website/data/typical-costs.yaml` naming the committed
reports as the figures' source, which D324 claims and which is not yet true (F14).

The design deliberately proposes **no new check**: check numbers live in the register
(`docs/gcp-live-checklist.md`), which this work does not own, and every behaviour R0–R4 adds is a
local read that a hermetic test covers.

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

## Release slot

**CLI 0.15.0, not earlier.** Why: R1 adds a flag and a versioned JSON document, R2 adds a doctor
line, and R4 adds a pattern name — three additions to surfaces a release notes, none of them a fix;
0.14.2 is the follow-up tag for the 0.14.x line. Nothing here needs a cloud change, a policy
migration, a new image or a new secret, so **no `### For operators` entry and no `fugaro update`
step**: the HARD RULE's "one command" is untouched, and an installation that upgrades to 0.15.0 gets
the due signal and the doctor line from `brew upgrade` alone. R3 rides with the release that carries
`plugin/**`'s own 0.15.0 change, because the plugin pin and the skills must ship together; R5 is the
owner's whenever they run it, and it is not gated by a release.
