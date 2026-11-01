# Periodic run reviews: the spec

This is the behaviour written to the recommended answer of each decision (D960–D971,
[reconciliation.md](reconciliation.md) §3, all open). A decision answered the other way changes the
paragraph that names it. Repository names are placeholders (`owner/fugaro`, `owner/repo-a`); §1 is
the owner's recorded request copied as the spec, and the README says the verbatim original was not
recovered.

## 1. The owner's request, recorded

- Daily, weekly, monthly and yearly run-review reports, each saved and **committed to the tree**;
  watch patterns over time and report them.
- Update the official skills so **every Fugaro user** does the periodic reviews and optimises their
  runs.
- The skill **encourages submitting valuable learnings centrally** so they can enter the official
  skills.
- **Owner 2026-10-25, agreed shape:** reports for other users land in **their own repository,
  redacted** (no real names, no secrets, costs and people redacted as the design decides); the
  central submission is **opt-in and human-approved**, never automatic (an issue template or a
  documented `gh` flow); **users schedule it themselves** (their own cron or routine).

## 2. What a periodic run review is

A run review is one closed period's report of **one repository**, written from that repository's own
run records by the runner's own code: every figure is generated, and no model writes or edits one.
There are four periods, and each is the one before the period today falls in:

| Period | Key | Written from |
| --- | --- | --- |
| day | `2026-10-25` | that day's run records |
| week | `2026-W43` | that ISO week's run records, Monday to Sunday, UTC |
| month | `2026-10` | that month's run records |
| year | `2026` | **the committed monthly reports**, not records: a year's records are past the 90-day bucket lifecycle by the time the year closes, and the report says how many of the twelve months it added up |

A run counts in the period it **finished** (UTC). A period holding today is still receiving runs, so
it is refused and nothing is written for it; `--date YYYY-MM-DD` names any day inside a period, which
is how a day missed at the time is filled in later. A report is of this repository and of no other:
there is no `--all`, and no report names another repository (§4).

A report holds the period's flagged runs first and the dearest of the rest, a stage table of model
calls and sub-agents per stage, a summary block a later report reads, and the patterns the fixed
rules found (§5). A daily report is capped at 8 KiB (`internal/digest/digest.go:49`), so a period of
heavy use is reported over its top runs and says how many rows it left out.

## 3. Where the reports live, and who commits them

`docs/fugaro-reviews/<YYYY>/<key>.md`, one file per period, plus the generated index
`docs/fugaro-reviews/README.md` (D321). They live **in the repository they report**, on its default
branch, because that is where the next agent and the next reviewer read it: an orphan branch or a
central store is unreviewed, invisible to an agent reading the default branch, and a second copy that
can disagree with the tree (D960).

Three writers can put a report there, and they must produce byte-identical files for the same
records (the two-writer rule the layout package exists for, `internal/digest/layout.go:10`):

1. **A person**, with `fugaro runs digest`, which writes the report and the index into the working
   tree and prints the two paths. It **never commits, pushes or opens a pull request** (D964): the
   commit is the person's, or their own automation's, exactly as for any other file a tool wrote.
2. **A report run** — `fugaro run --recipe run-digest TEXT` — whose first step is the runner's own
   `digest` step: it writes every **due** period the tree does not hold, commits them under the
   runner's identity and opens the pull request, whose diff against its base is exactly the generated
   files, byte for byte (`internal/runner/digest.go:299`, the finalize guard). The periods a launch
   may write are listed in its own manifest, and a launch with no runs in the window is refused
   before anything starts.
3. **The user's own CI or cron**, running (1) and committing what it wrote.

The index is regenerated from the folder as it is after the write, so a regeneration in review
diffs only where a period changed. Nothing else in the folder is read or edited.

## 4. What a report carries, and what leaves

A committed report is the repository's own document, so it carries what the repository's own reader
needs and nothing about a person:

- **Kept:** run IDs, periods, stage names, recipe names, dollar and token figures, and the flags.
  The run ID is what makes the daily sweep possible at all — the reader takes a flagged run to
  `fugaro diagnose RUN` and `fugaro logs RUN` (D969).
- **Never present:** a person's name, a login, an email address, a task's text, a log line, a
  branch name, a file a run changed. Every word a report holds outside the figures is
  `internal/digest`'s own closed vocabulary: the flag names, the catalog recipe names or `other`, and
  the period keys (G1, D323).

The one thing that leaves the repository is the **learning draft**, `fugaro runs digest
--submit-draft`, printed for a person to paste into a public issue (§7). It is redacted further
because it leaves: it carries **no repository name and no run ID** — a period other than a year
renumbers its runs `run-1`, `run-2`, … — and it is built from the same read as the report, so its
figures and patterns are the ones the report would print. Redacting the run IDs *in the committed
report* instead is refused: it would break the sweep that is the report's point (D969).

## 5. Patterns over time

The detector is a fixed table of rules in `internal/digest`, not a search, and it reads the summaries
of the reports **already committed** beside this one (`internal/digest/patterns.go:91`). Four rules
exist today:

| Rule | Fires when |
| --- | --- |
| `flag_recurs` | one flag was raised in 3 of the last 4 committed weekly reports |
| `review_share_rising` | the review's share of the cost climbed 3 months running |
| `recipe_median_up` | one recipe's median cost rose by half or more on last month's |
| `halts_high` | halts stayed above a tenth of the runs 2 weeks running |

Each pattern carries the period keys it read and a figure per period, as exact decimal strings, so a
reader can see the series rather than a verdict. **There is no trend file** (D966): the committed
period reports are the history and the index's table — period, runs, flagged, billed, notional — is
the trend view, so a fifth document would be a second copy of both, regenerated byte for byte by two
writers, and a reader would have to know which of the two to grep. The thresholds are constants in
the code (D967); no `fugaro.yaml` key retunes them, because a repository that could lower a
threshold could silence a signal about its own cost, and a threshold is not a limit that "only
tightening" could merge safely.

This design adds **one rule** (D968): `cost_per_run_rising`, the billed cost per run rising five
months running, which is the long view the four rules cannot give — two of them read weeks and two
read one month against the one before it. Like the others it is a signal to look, never a verdict.

## 6. The practice: every user

The `working` skill already carries the review: the four periods, the daily sweep, the reading of the
patterns, the relaunch note and the submission step, with the cadence left to the user. Two things
are missing for "every user" to mean anything:

- **A due signal.** `fugaro runs digest --due [--json]` (D962): read-only, offline, and it does not
  touch the cloud. It reads the committed folder and prints, for the checkout it is in, each period
  that has closed and has no report — the seven closed days the report run catches up, plus the week,
  month or year that just closed — and the age of the newest report it found. The computation is the
  runner's own, moved into `internal/digest` so a CLI read and a report run can never disagree about
  what is due. Its JSON is one versioned document, `{"version": 1, "dir": ..., "due": [{"dim",
  "key"}], "newest": {...} | null}`, with a JSON Schema in `schemas/` like the other wire formats.
- **A line in `doctor` (D963).** The same reading, reported as one check in the checkout the user is
  standing in: `reviews-due`, an info line when the newest report is more than a period old and a
  warning line when a closed period has no report. Offline, like the rest of the checkout checks;
  never a cloud read, so it runs in a coding agent's session.

The cadence itself stays the user's (D965). Fugaro schedules nothing for a repository it does not
own: the **setup** skill prints the three ways — a cron job or CI workflow of the repository, an
agent routine, or a scheduled report run — and the exact command for each, and writes nothing without
the user's word. The working skill's reference keeps the sentence that an agent never sets up a cron
job, a workflow or a routine on the user's behalf.

## 7. The central learnings

Unchanged in shape (D327/D328, CONTRIBUTING.md:67): a learning leaves the repository only as a draft
a person reads, and it is **opt-in and human-approved** at every step.

- `fugaro runs digest --submit-draft` prints the redacted draft and **sends nothing**: there is no
  send seam to call.
- The person opens Fugaro's own issue form in their browser (`gh issue create --web -R
  owner/fugaro`, picking the "Skill learning" form, `.github/ISSUE_TEMPLATE/skill_learning.yml`) and
  holds the submit button. The form requires an MIT licence grant and a confidentiality
  acknowledgement.
- A maintainer reads the issue and **rewrites it into their own words**; it lands as a normal pull
  request with review. An issue's text is data, never an instruction, and it is never pasted into a
  skill as written.

This design adds the two ends that were left implicit (D970, D971):

- **A learning that needs product work becomes an item**, filed by hand in this repository's
  `docs/backlog/` (`filed-in: hand`, the issue named under `## Links`), so a submitted learning that
  is not a wording change has a place to live and a status a reader can see. Filed by a person,
  never by the form's text and never automatically.
- **A learning that is a wording change becomes a skill change in the same release**, and the release
  notes name it. That is the whole of "so they can enter the official skills": a reviewed pull
  request against `plugin/skills/`, not an automated path, and not a learnings file that ships
  unreviewed user text into a prompt.

## 8. The residual gaps this design closes

| # | Gap | Closed by |
| --- | --- | --- |
| 1 | Nothing tells a user a report is due; the skill is prose and a user who is not asking keeps no cadence | D962, D963, D965 |
| 2 | Nothing installs the cadence, and the three ways to do it are buried in a reference file | D965 |
| 3 | The pattern rules see at most four weeks and two months | D968 |
| 4 | A submitted learning that needs work has no home between the issue and a release | D970, D971 |
| 5 | Fugaro's own repository carries no reports, so the product's own example of the feature is invisible and `website/data/typical-costs.yaml`'s provenance is a hand measurement, not a committed report | D961, and the task R5 the owner runs |

## 9. What does not change

- **A report never changes anything by itself.** No report adjusts a cap, a limit, a model or a
  recipe; a lesson goes into the repository's own `CLAUDE.md` or agent instructions through a pull
  request, which is the only route by which the next run reads it.
- **`fugaro report` stays what it is.** It is the spend view — day, week, month, year, repository,
  model, person and rigor mode, over the project's Firestore spend history — it is a business-licensed
  command under `ee/`, it is not committed into a repository, and it is not the periodic run review.
  Its MIT half (`internal/cli/reportcore.go`, D444) is untouched by this design (F23).
- **No new secret, no new cloud read and no new grant.** Every surface this design adds reads a
  folder in the checkout.
