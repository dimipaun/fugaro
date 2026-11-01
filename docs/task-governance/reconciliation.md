# Task governance and periodic run reviews — reconciliation

**Status: decisions D320–D333 answered by the owner on 2026-10-26 (D320 not the recommendation);
the answers are the approval of this reconciliation. Nothing is implemented and no task is
approved:** each task of [implementation-plan.md](implementation-plan.md) needs its own explicit go.
Checked against `main` at `d5898f9b8` (`release: v0.11.4`) on 2026-10-25, re-checked after merging
`main` at `dbce05608` on 2026-10-26. Where this
document disagrees with [spec.md](spec.md) (task governance, 2026-10-22) or
[spec-periodic-reviews.md](spec-periodic-reviews.md) (periodic reviews, 2026-10-25), this
document wins.

Spec references: **G§n** is a section of the governance spec (G§7.k is its rollout step k); **P**
is the periodic-reviews request.

## 1. The owner's ruling already given (2026-10-25)

On the shape of the periodic reviews, the owner agreed:

1. A user's reports land in **their own repository**, redacted.
2. Central submission of learnings is **opt-in and approved by a person**, never automatic.
3. Users **schedule** the reviews themselves.
4. Fugaro **runs no service** for any of it.

Every finding and decision below is written inside that ruling.

## 2. Headline: spec concept → what the repository already has

| Spec concept | What the repository already has | Gap left |
| --- | --- | --- |
| G§1 review-scope sanity check | Review range narrowed to the run's own commits (`internal/runner/diffbase.go` `narrowReviewRange`, `ownCommitsBase`, `gitops.Repo.OwnCommits`; #1073, #1082), the run's real base (#840), a huge-diff prompt note (`internal/runner/prompts.go` `DiffSize.Huge`, `hugeDiffNote`), a 256 KiB review-input cut (`internal/runner/reviewinput.go` `maxReviewDiffBytes`), the run's changed files recorded (`internal/runner/changedfiles.go` `recordChangedFiles`, `runstore.ChangedFiles`) | No comparison of review input against implement's edits, no pause, no recorded mismatch (F1–F3) |
| G§2 phase budgets | Per-run dollar cap and lease (`budget.per_run_usd`, `internal/gateway/lease.go`), per-mode budget fractions of the cap (`config.RigorBudgetFractions`, rigor plan T6.1), per-run token cap (`agent.max_run_tokens` → halt `token_cap`), one stage timeout for every stage (`config.Timeouts`), review rounds (`agent.review_rounds`, default `config.DefaultReviewRounds` = 2) | No per-stage dollar, call or time budget; no review:implement ratio (F4–F7) |
| G§3 expectation envelope, tiers | Rigor modes (poc, startup, pro, critical, hotfix) set a budget fraction; recipes carry `max_rounds` only; `fugaro budget preview` gives a worst-case batch figure | No difficulty tier, no expected cost, duration or scope on a task (F8, F9) |
| G§4 checkpoints, escalation | "Checkpoints" are branch pushes (`internal/runner/checkpoint.go`), not decision points. A halt ends the run with a draft PR; steering is a trusted PR comment and a `--pr` follow-up (`internal/followup/trust.go`) | No pause inside a running job, no channel into one (F10, F11) |
| G§5 tripwires | Gateway counts calls per stage (`gateway.StageReport.Calls`) and logs each call with `agent_id` (`internal/gateway/log.go`); the review prompt asks not to fan out on a huge diff | No fan-out cap, loop, scope-drift or idle detection (F12–F14) |
| G§5a spend caps | Per-repo and per-project (global) daily caps, `fugaro budget set --global/--repo --daily`, halts `repo_daily_cap`/`global_daily_cap`, launch precheck refusing a batch with no room (`internal/budget/precheck.go` `Precheck`), kill switches | No 80% warning (F15) |
| G§5a retry policy | Nothing retries a started run automatically: Cloud Run `max_retries = 0`, Batch `MaxRetryCount: 0`; `fugaro run --retry` only launches a stored task that never started | The spec's "one automatic retry" would add automation that does not exist (F16) |
| G§5a instruction pre-flight | Nothing | F17 |
| G§6 telemetry per task | `result.json` (`runstore.Record`): `Stages[]` (`StageTiming`: duration, model dollars, notional), `Cost` (model, notional, compute, by model, by route), `Reviews[]` (round, tier, verdict, findings), `Halt`, `Rigor`, `Recipe`, `ChangedFiles` | Call counts and sub-agent counts per stage are not on the record (F18) |
| G§6 dashboard, outlier sweep | `fugaro report` (spend by day/week/month/year/repo/model/person), `fugaro runs stats --by user`, `fugaro stats runs` (per-recipe percentiles, owner-only), `fugaro diagnose`, `fugaro watch`; the `working` skill's "Inspect the expensive runs daily" and "Look at a run when it passes half its cap" sections; the owner's private daily script (prototype, not in the tree) | No report that joins cost, stage calls, review rounds and halts; no outlier rule (F19–F23) |
| P daily/weekly/monthly/yearly reports, committed | `report.PeriodKey` already defines UTC day, ISO week, month and year; `docs/backlog/` is a precedent for Fugaro writing Markdown into a user's tree | No committed report tree, no generator (F24–F27) |
| P pattern watching | Nothing | F28 |
| P skills for every user | `plugin/skills/working/SKILL.md` already prescribes a daily inspection; skills are versioned with the CLI and pinned by `fugaro update-skills`/`fugaro upgrade --local` | Weekly/monthly/yearly cadence, the report command, the submission step (F29) |
| P central submission of learnings | Issue forms (`bug_report.yml`, `feature_request.yml`), Discussions enabled, CLA rule for outside PRs (`CONTRIBUTING.md`) | No learning channel; licence of submitted text unsettled (F30–F32) |

## 3. Findings

Classes: **Wrong** (spec contradicts code), **Exists** (already there, maybe under another name),
**Gap** (code forces a case the spec is silent on), **Rule** (conflicts with a ruling, rules file
or doc), **Open** (the spec's own unanswered question).

### Review scope and phase budgets (G§1, G§2)

- **F1 — Exists (largely).** G§1's incident (review diffing against `main` on an integration
  branch) is fixed twice: the review diffs against the run's real base (#840), and after implement
  the range is narrowed to the run's own commits (`internal/runner/diffbase.go`
  `narrowReviewRange` → `ownCommitsBase` → `gitops.Repo.OwnCommits`, #1073; shown in the report,
  #1082). The narrowing only ever narrows and is skipped on git older than 2.44. **Consequence:**
  G§7.1 is mostly delivered; what remains is the *check* and its record, not the fix.
- **F2 — Gap.** Nothing compares the review input (`reviewDiffSize` → `DiffSize`) with what
  implement edited. The changed-file list exists but is recorded at finalize
  (`recordChangedFiles`), after review. **Consequence:** the check needs implement's file set
  taken at the end of implement (cheap: `git diff --name-only` over the run's own commits) and a
  rule for "much larger". Where the range was narrowed to the run's own commits, a mismatch means a
  bug in the narrowing, so the check is a cheap invariant there. But `narrowReviewRange` keeps the
  wide range **by design** on a git older than 2.44 (a repository's own image), when its own
  commits cannot be read, and when the narrowed range holds no fewer commits; a follow-up and a
  review run keep the pull request's range. In those cases the comparison is not applicable and must
  never stop a run (plan T5.2, T7.3).
- **F3 — Gap.** The spec says "pause and send a checkpoint report". There is no pause (F10). The
  only stopping action a running job has is a halt with a draft PR. **Consequence:** the check's
  action is *record + skip or narrow* or *halt*, decided in D329.
- **F4 — Gap.** No per-stage budget exists. `gateway.StageReport` already holds per-stage
  `Calls`, `Used`, `Notional` and `Tokens`, and the gateway sees every call's stage, so a stage
  budget in dollars or calls is enforceable **at the gateway** with the same refusal path a cap
  uses. **Consequence:** phase budgets are a gateway change plus a new halt reason, not a new
  component.
- **F5 — Gap (oauth).** Under `agent.auth: oauth` no call goes through the gateway: there are no
  `model call` lines and no live per-call count (the `working` skill says so). The runner only
  learns the stage's figure from the agent's `result` line when the stage ends. **Consequence:**
  live phase budgets, the ratio guardrail and fan-out caps cover gateway runs only; oauth runs get
  post-hoc flags in reports. The spec does not know this split (D331).
- **F6 — Gap.** The ratio guardrail "review calls > N × implementation calls" breaks on a run
  whose implement made few calls (a one-line change: 12 calls × 5 = 60) and on review-only runs
  (`mode: review`, no implement). **Consequence:** the rule needs a floor (max(floor, k ×
  implement)), as G§2 itself writes for the review budget, and is undefined for review runs.
- **F7 — Exists, partly.** Wall-clock limits exist but are one value for every stage
  (`config.Timeouts`: total 90m default, stage min(40m, total), verify, finalize reserve), and the
  backend's job timeout is total + `backend.TaskTimeoutSlack`. A timeout ends the run as failed,
  not halted. **Consequence:** per-phase time budgets are a refinement of `timeouts.stage`, not a
  new mechanism.

### Expectations, checkpoints, escalation (G§3, G§4)

- **F8 — Gap / Rule.** Two dials would act on the same per-run cap: rigor modes already scale it
  (`config.RigorBudgetFractions`: hotfix 0.4, poc 0.55, startup 0.7, pro 0.85; critical is absent from the map, which means the whole cap;
  floored at `RigorBudgetFloorUSD`, applied in `internal/runner/policy.go` via
  `config.RigorBudgetCap`), and G§3's tiers would set an expected cost. **Consequence:** a tier
  must be an *expectation* (what triggers a checkpoint) and never a second cap, or the two
  multiply (D330).
- **F9 — Gap.** "The orchestrator" in G§3/G§4 is, in Fugaro today, the user's own agent driving
  the CLI through the skills (or a person); the central orchestrator is plan only
  (`docs/central-orchestrator/README.md`: "plan only; no code is implemented"). A task carries no
  tier or expectation (`internal/task`). **Consequence:** the envelope is a new optional task field
  set by the launcher, validated, recorded on the run record and shown by `diagnose`; the launcher
  is the skill-driven agent.
- **F10 — Wrong (premise).** G§4 assumes a job can pause and wait for the orchestrator's
  Continue/Redirect/Stop. A running job cannot receive instructions:
  `docs/central-orchestrator/reconciliation.md` records that the job has no pause and that nothing
  escalates on a timer; prompts and claims are only planned there (part P7). The checkpoints in
  `internal/runner/checkpoint.go` are branch pushes. **Consequence:** a "checkpoint" today can only
  be **halt + checkpoint report + follow-up**: the run stops with a draft PR and a written report;
  Continue = a `--pr N` follow-up with a raised budget, Redirect = a follow-up with corrective
  instructions, Stop = do nothing. A paused job would also be billed while idle (D329).
- **F11 — Open (G open question 2).** Escalation delivery: a halted run already surfaces in
  `fugaro runs ls`, `fugaro watch` and the console, and its PR is a draft. Push to a phone exists
  only as the mobile mailbox (`internal/mailbox`), which does not talk to running jobs.
  **Consequence:** the first slice needs no new channel (D329).

### Tripwires and safeguards (G§5, G§5a)

- **F12 — Gap (fan-out).** Each gateway call carries Claude Code's `x-claude-code-agent-id`
  (`internal/gateway/forward.go` → `callLog.agentID`), so distinct sub-agents per stage are
  countable at the gateway. Refusing a sub-agent's calls mid-flight makes the agent see errors, not
  a clean stop. **Consequence:** the enforceable form is "halt the run when a stage passes N
  sub-agents", gateway runs only (D332).
- **F13 — Gap (loops, idle).** Loop detection (same test failing X times, edits back and forth,
  near-identical prompts) and idle spin need tool-level events. The relay sees tool calls
  (`internal/agent/relay.go`, `tool <name>: <summary>` lines), but nothing aggregates them.
  **Consequence:** these are heuristics with false positives; they belong in reports first and in
  the runner only after the report shows they are reliable (D332).
- **F14 — Gap (scope drift).** "Edits outside expected scope" needs an expected scope (F9) and a
  path match. Rigor's `--path`/`task.Spec.Paths` and `doublestar` path rules already exist.
  **Consequence:** scope drift reuses rigor's path machinery once the envelope carries a scope.
- **F15 — Exists, mostly.** G§7.2 "daily and per-project spend caps" exists: per-repo and
  per-project daily caps, the per-run cap, kill switches, and `budget.Precheck` refusing a launch
  or a whole batch that has no room ("pause new dispatches" at 100%). What is missing is the
  **80% warning**. **Consequence:** G§7.2 shrinks to one warning line (R5).
- **F16 — Wrong (premise).** G§5a's retry policy says "at most one automatic retry". Fugaro has
  **zero** automatic retries of a started run (Cloud Run `max_retries = 0`, Batch
  `MaxRetryCount: 0`, "a Fugaro run is not idempotent"); the `working` skill says a failed run is
  not retried and a person decides. Inside a run, implement is re-run once in a fresh session after
  a recovered error, and stages resume after credential waits (`maxCredentialResumes` = 3).
  **Consequence:** the policy is already stricter than the spec; only the "note on why the first
  attempt failed" is new, and it belongs in the skill (R4).
- **F17 — Gap.** Instruction pre-flight (G§5a) has no home: there is no step between bootstrap and
  implement where the agent can object, and no channel for the objection (F10). **Consequence:** it
  can only be a short pre-implement stage that halts with a checkpoint report, so it depends on
  D329 and costs one model call per run (D333).

### Telemetry, reports and patterns (G§6, P)

- **F18 — Gap.** Per-stage call counts are known to the gateway (`StageReport.Calls`) but
  `runstore.StageTiming` records only name, start, duration, model dollars, model and notional.
  Sub-agent counts are not recorded anywhere. The owner's prototype and the `working` skill count
  them by grepping `fugaro logs`. **Consequence:** reports built on logs break in two ways: the
  Fugaro log bucket keeps **30 days** (`deploy/terraform/gcp/modules/installation/logging.tf`,
  `retention_days = 30`), so a monthly or yearly report cannot recount them; and oauth runs log no
  `model call` lines. Recording `calls` and `agents` per stage on the record is the prerequisite of
  every report and every self-calibration (R1).
- **F19 — Exists (daily review).** The daily review the owner wants is already the `working`
  skill's "Inspect the expensive runs daily": the dearest 10–20% of the last 24 hours, read with
  `diagnose` and `logs`, looking at review vs implement calls, fan-outs, wrong diff base, repeated
  commands, and writing each lesson into the project's `CLAUDE.md`. The "Look at a run when it
  passes half its cap" section is a manual checkpoint at 50%. **Consequence:** P's daily report is
  the codified, committed form of an existing skill section, and the same thing as G§6's outlier
  sweep (R2).
- **F20 — Exists, under another name.** `fugaro report` already groups by `day`, `week`, `month`
  and `year` (`report.Dim` `ByDay`/`ByWeek`/`ByMonth`/`ByYear`; `report.PeriodKey`: UTC days, ISO
  weeks starting Monday named `YYYY-Www`, UTC calendar months and years) — but **spend only**:
  model, notional, compute, runs, calls and tokens per period, read from `spendDaily` in Firestore.
  It knows nothing of outcomes, halts, stages or review rounds. **Consequence:** the period
  boundaries are settled (R3); the periodic review is a new command joining spend with run records,
  not an extension of `report`'s data source.
- **F21 — Rule.** The prototype script (owner's private `cost-inspect.sh`) parses the human table
  of `fugaro runs ls` with `awk`. `fugaro runs ls --json` and `fugaro diagnose --json` exist.
  **Consequence:** the product generator reads records through the store, never a table.
- **F22 — Rule.** `fugaro stats runs` is refused in a coding agent's session
  (`agentMarker`, "reading them is the owner's step") and prints no identifier by design (D96/D97
  of the website work). `fugaro report` and `fugaro runs stats` are not refused, but `report`
  needs `roles/datastore.viewer` on the Firebase project. **Consequence:** a skill-driven review
  must use commands an agent may run, and the new command must state whether it is an owner-only
  path (D320).
- **F23 — Gap.** Identifiers in today's outputs: `fugaro report --by person` prints the stored
  requester address (an email); `runs ls` has a `BY` column; `runs stats` groups by person. Redaction
  code in the tree is for **secrets** only (`internal/agent/redact.go` `RedactFunc`, `fugaro logs`).
  Nothing redacts emails, repository names or run IDs. **Consequence:** a committed report needs
  its own redaction rules, tested (D323).
- **F24 — Gap (retention).** Run records live **90 days** (`runs/` lifecycle,
  `bucket_lifecycle.runs_days` = 90 in every fixture); logs 30 days; `spendDaily` documents are
  kept by the rollover. **Consequence:** a yearly report cannot be recomputed from records; it must
  roll up committed monthly reports (plus `fugaro report --by month` for spend). The committed
  reports are therefore not just reference: they are the **only long-term history** of outcomes
  and stage behaviour (D326).
- **F25 — Gap (where and who).** The spec says "saved and committed to the tree" and names no
  path, author or trigger. The precedent is `docs/backlog/`: one Markdown file per item, a
  generated index (`fugaro backlog index`), written by runs only where the repository opts in.
  **Consequence:** D320 (who generates and commits) and D321 (where and how big).
- **F26 — Gap (scope of a report).** A Fugaro project has many repositories; a report committed
  into one repository would name the others and their costs. **Consequence:** D322.
- **F27 — Rule (this repository is public).** `dimipaun/fugaro` is public. Fugaro's own reports
  committed here would publish the owner's spend, run IDs and PR numbers; the
  no-real-names rule forbids teammates' names and their projects. **Consequence:** D324.
- **F28 — Open (patterns).** P says "watch for patterns … and report back" and defines none. G§6
  names candidate metrics (cost, calls, duration, review-to-implementation ratio, sub-agent count)
  and a robust outlier rule (3 MAD). There is no tier history to compute MAD per tier, and no
  state file. **Consequence:** D325 (outlier rule) and D326 (pattern rule and state).
- **F29 — Exists (distribution).** Skills are versioned with the CLI: `plugin/.claude-plugin/plugin.json`
  `version`, a `fugaro-skill … fugaro-version=` header on every skill file, kept equal to the tag by
  `scripts/bump-plugin-version.sh`; `fugaro update-skills` and `fugaro upgrade --local` pin the
  marketplace ref in the repository's `.claude/settings.json`, and commands warn on a stale pin.
  `plugin/skills_lint_test.go` checks every command and flag a skill names against the CLI and holds
  size budgets (`TestLintSizeBudget`, `TestLintByteBudget`). **Consequence:** "update our skills so
  everybody does this" ships with the next release through the existing channel; the `working`
  skill's size budget limits how much text it can take (R6).
- **F30 — Gap (submission channel).** Issue forms exist (`bug_report.yml`,
  `feature_request.yml`, `blank_issues_enabled: false`), and Discussions are enabled. There is no
  form for a learning. **Consequence:** D327.
- **F31 — Rule (licensing).** `CONTRIBUTING.md` "Licensing of contributions": outside **pull
  requests** need the CLA before merge. A learning submitted as an issue or discussion is not a
  pull request, yet incorporating its text into the MIT skills is a contribution in substance.
  **Consequence:** D328; the legal reading is the owner's to confirm.
- **F32 — Gap (abuse).** A public intake that feeds official skills is a prompt-injection path into
  every user's agent: skill text is instructions. **Consequence:** submissions are never pasted;
  a maintainer rewrites a learning into a skill change that goes through the normal PR and review
  (R7).
- **F33 — Gap (self-calibration).** G§6's rolling window (last 20 tasks or 14 days) per tier fits
  inside the 90-day record lifetime, so it can be computed from records — once records carry a tier
  (F9) and stage calls (F18). It needs no committed history. **Consequence:** self-calibration
  depends on D330 and R1, not on the periodic reports.
- **F34 — Gap (scheduling).** Fugaro's own periodic work is Cloud Scheduler jobs inside an
  installation (`history_rollover` daily, `history_sweep`, `coordinator_sweep`, `reaper_sweep`,
  the per-repo image check, `preload_rebuild`); none runs or reviews agent tasks. The backlog item
  `docs/backlog/20261021-02-hotfix-followup-scheduler.md` records the earlier reasoning against new
  triggers ("a scheduler that stops is invisible"). **Consequence:** consistent with the owner's
  ruling, the product ships a command and the skill tells the user how to schedule it (their cron,
  CI or agent routine); no installation-side scheduler is added (R8).

### Found after the owner's answers (2026-10-26)

- **F35 — Gap.** A recipe step never runs an arbitrary command: `recipe.Step.Command` is a
  `CheckCommand` (`build`, `test` or `lint`, a `commands.*` key of `fugaro.yaml`), and
  `docs/recipes.md` says a recipe never names a command. **Consequence:** D320's run needs a new step
  kind, not a recipe of existing ones.
- **F36 — Rule.** Every recipe ends in exactly one senior `review` step (`internal/recipe/recipe.go`
  refuses otherwise: "review must appear exactly once"; `docs/recipes.md`: "A recipe cannot produce
  a ready pull request without a senior review"), so no pull request is opened without a model review stage. Artifact runs, the one mode
  with no model stage, never commit or push (`docs/artifact-runs.md`). **Consequence:** a report run
  costs one review stage; D320's model part is that review.
- **F37 — Exists.** A deterministic step that commits and skips implement already exists: the
  autofix gate (`internal/runner/gate.go` `runAutofix`, `gateResult.Fixed`; `gateResult.Clean`
  ends with no PR). **Consequence:** the `digest` step follows that shape.
- **F38 — Exists / Gap.** The job's account reads and writes its own repository's `runs/<slug>/`
  prefix (`deploy/terraform/gcp/modules/workflow/sa.tf`, `gcp.BucketCondition`); project jobs get the
  slug-less condition (`gcp.ProjectBucketCondition`, `deploy/terraform/gcp/modules/project/sa.tf`),
  which reaches every repository, as the owner accepted on 2026-10-17. Job accounts get nothing in the Firebase project
  (`deploy/terraform/gcp/modules/firebase/iam.tf`), so no `spendDaily` read. **Consequence:** no
  new grant; the report's figures come from run records only; repository isolation on a project job
  is the runner's own check; listing is U8 and is avoided.

## 4. Resolutions (one defensible answer; not questions)

- **R1 — Record calls and sub-agents per stage.** Add `calls` and `agents` to
  `runstore.StageTiming` (`calls` from `gateway.StageReport.Calls`, absent without a gateway, so on
  an oauth run, never 0; `agents` from the sub-agent tool calls the relay counts on the stage's own
  stream, for every auth mode, the gateway's distinct agent IDs never recorded because a client
  claims them), with the schema in `schemas/`. Every
  report and every guardrail reads the record, not the logs (F5, F18, F24).
- **R2 — The daily report is the outlier sweep.** One daily report serves P's "daily" and G§6's
  "outlier sweep"; the `working` skill's daily-inspection section is rewritten around the command
  rather than duplicated (F19).
- **R3 — The clock is `fugaro report`'s.** UTC days, ISO weeks (Monday, `YYYY-Www`), UTC calendar
  months and years, via `report.PeriodKey`. A report covers a **closed** period only; a run belongs
  to the period it **finished** in (a run still going at the boundary is in the next report). A
  daily report is written for yesterday, never today (F20).
- **R4 — Retry policy stays at zero automatic retries.** The spec's "one automatic retry" is not
  adopted; the skill adds the spec's real ask: a relaunch states why the first attempt failed and
  what changed (F16).
- **R5 — Spend caps: add only the 80% warning.** `fugaro budget show`, `fugaro run`'s launch
  output and `fugaro watch` print a warning when a daily cap's counter passes 80%; no new cap
  (F15).
- **R6 — Skills ship through the existing versioned channel.** The cadence and the report command
  go into `plugin/skills/working` (moving the long how-to into a `reference/` file to keep the
  size budget), shipped with the release and picked up by `fugaro upgrade --local` (F29).
- **R7 — A submitted learning is never pasted into a skill.** A maintainer rewrites it into a
  normal PR with review; the submission is input, not text that ships (F32).
- **R8 — No Fugaro-side scheduler.** The product ships the command; the user schedules it
  (F34; the owner's ruling, §1).

## 5. Decisions for the owner (D320–D333)

The owner answered every row on 2026-10-26. D320's answer is **not the recommendation**; its
consequences are followed through in §5.1 and in the plan's Slice 3.

| # | Question | Recommendation and reason | Alternative and its cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D320** | Who generates and commits a periodic report? | **A deterministic, read-only CLI command (`fugaro runs digest --period day\|week\|month\|year`; this row first called it `fugaro review`, and D327's row used the same name, §5.1) writes the Markdown file locally; a person, or their agent under the skill, commits it through an ordinary PR.** No model call, so a report costs nothing; it runs where the person's credentials are; the agent may run it (it prints no email, D323). | **A Fugaro run produces the report PR.** Costs a model run for deterministic work, and the job's service account would need read access to other runs' records (UNVERIFIED U2). **A model-written report.** Richer diagnosis, but non-reproducible and paid. | **2026-10-26: a Fugaro run makes the PR — not the recommendation.** Why: the owner wants the report delivered as a run's pull request. Followed through in §5.1. |
| **D321** | Where do reports live in a user's repository, and how is growth bounded? | **`docs/fugaro-reviews/<YYYY>/` with one file per period (`2026-10-25.md`, `2026-W43.md`, `2026-10.md`, `2026.md`) and a generated index, like `docs/backlog/`; a daily report is capped (top 10 runs, under ~8 KiB).** About 420 small files a year; reviewable in PRs; greppable by the next agent. | **An orphan branch `fugaro-reviews`.** No PR noise, but unreviewed and invisible to agents reading the default branch. **Commit weekly and up only, daily printed.** Fewer files, but departs from the spec's "each … committed". | **2026-10-26: the recommendation** — `docs/fugaro-reviews/<YYYY>/`, one file per period, a generated index, the daily cap. Why: reviewable and greppable on the default branch, like `docs/backlog/`. |
| **D322** | What does a report committed into one repository cover? | **That repository's runs only, by default.** A project-wide report (all repositories) is printed, or committed only with an explicit flag into a repository the person names. A public repository must not name a project's private repositories. | **Project-wide by default.** One file shows the whole project, but leaks other repositories' names and costs into whichever repository receives it. | **2026-10-26: the recommendation** — this repository's runs only. Why: a report never names another repository of the project. |
| **D323** | What may a committed report contain? | **Allowed:** run IDs, PR numbers, statuses, halt reasons, recipe, rigor mode, stage durations, calls and sub-agents, dollars (billed and notional apart), review rounds and verdicts, the diff base. **Never:** emails or account names (the launcher becomes `person-1`, `person-2` within one report, or `automated`), task text, log excerpts, credential names, other repositories' names (D322). Tested like `fugaro stats runs`' no-identifier test. | **Include the launcher's address.** Lets a team see who spends, but commits personal data into history forever. **Drop run IDs too.** Safer, but the reader can no longer open the run with `fugaro diagnose`. | **2026-10-26: the recommendation** — IDs and numbers, no people. Why: run IDs keep `diagnose` usable; a person never enters history. |
| **D324** | Do Fugaro's own reports go into this public repository? | **Yes, under `docs/fugaro-reviews/`, with D323's rules and dollars kept**: it dogfoods the feature and the figures already inform `website/data/typical-costs.yaml`. | **No; keep them in a private repository of the owner's.** Keeps spend private, but the product's main example of the feature is invisible and the website's figures lose their source. | **2026-10-26: the recommendation** — yes, in this public repository under D323's rules. Why: dogfood the feature and source the website's figures. |
| **D325** | What selects a run into the daily report (the outlier rule)? | **Start with the skill's rule (dearest 10–20% by cost, at least 3) plus fixed flags: review calls > 5 × max(implement calls, 20), sub-agents > 3 in a stage, a halt, more than one fix round.** Simple, explainable, no history needed. Add G§6's 3-MAD rule per tier once 20+ runs per tier exist. | **3 MAD per tier from day one.** Robust in principle, but there are no tiers yet (D330) and small samples make MAD noisy. | **2026-10-26: the recommendation** — top cost plus the fixed flags; MAD per tier later. Why: explainable, needs no history. |
| **D326** | What is a "pattern", and where is its state? | **A pattern is a fixed, documented rule over consecutive periods** (e.g. a flag that recurs in 3 of the last 4 weekly reports; review share of cost rising 3 months running), computed by the command from the **committed reports' machine-readable block** (a fenced JSON summary in each file). The weekly, monthly and yearly reports list the patterns found. No separate state file. | **A `trends.json` state file.** One place to read, but a second artifact to keep in sync and merge-conflict on. **Model-detected patterns.** Finds more, costs money, not reproducible. | **2026-10-26: the recommendation** — fixed rules; state in the committed reports' JSON block. Why: one artifact, reproducible. |
| **D327** | What is the central submission channel? | **`fugaro runs digest --submit-draft` prints a redacted learning draft; the person opens it with `gh issue create --web` against a new "Skill learning" issue form.** Never automatic; a person reads and sends. Spam is handled by GitHub's own tools and a label for triage. | **A Discussions category.** Lower ceremony, but no required fields (consent, redaction confirmation). **Accept PRs to `plugin/skills`.** Direct, but needs the CLA and still exposes the injection path (R7). | **2026-10-26: the recommendation** — a redacted draft plus a "Skill learning" issue form opened by the person. Why: never automatic, required fields. |
| **D328** | Under what licence is a submitted learning used? | **The issue form carries a required checkbox: the submitter licenses the text under MIT and confirms it holds nothing confidential; maintainers rewrite rather than paste (R7).** The owner confirms the legal reading. | **Require the CLA for submissions.** Clean, but a heavy step for a tip, so few will submit. **Treat issues as ideas, no grant.** Easiest, but leaves the incorporated text's provenance unclear. | **2026-10-26: the recommendation** — an MIT checkbox on the form plus a maintainer rewrite; the legal reading stays the owner's to confirm (U6). Why: light for the submitter, clear provenance. |
| **D329** | What is a "checkpoint" when a job cannot pause? | **Halt + checkpoint report + follow-up:** the run stops with a draft PR and writes the G§4 report (done, why over, plan, estimate, hard instructions) into the record and the PR; Continue/Redirect = a `--pr N` follow-up with a raised budget or new instructions; Stop = nothing; escalation is the halted row in `runs ls`, `watch` and the console. | **An in-job pause awaiting input.** Matches the spec's words, but needs the central orchestrator's prompt and claim machinery (planned, part P7), bills the job while idle, and meets the job's timeout. | **2026-10-26: the recommendation** — halt + checkpoint report + follow-up. Why: no pause exists and an idle job bills. |
| **D330** | How does the expectation envelope relate to rigor modes and caps? | **A tier (`trivial\|small\|medium\|large`, optional, set by the launcher) sets the *expected* cost and time that trigger the checkpoint (D329); the cap stays the rigor-scaled per-run cap.** Defaults from G§3 live in the project layer, tightenable per repository; G open question 1 (model vs history) is answered by self-calibration from records (F33) once tiers are recorded. | **Tier sets the cap.** One dial, but it fights rigor's budget fractions and makes a mis-tiered task fail at a cap meant as a backstop. | **2026-10-26: the recommendation** — the tier is an expectation only; the cap stays rigor-scaled. Why: one cap, no second dial on it. |
| **D331** | Where are phase budgets and the ratio guardrail enforced? | **In the gateway, per stage, in dollars and calls; time by a per-stage timeout.** Gateway runs only; oauth runs get the same rules as report flags (F5). Budgets configurable in the project layer, tightenable per repository, not per recipe (recipes cannot loosen caps). | **In the runner from the agent's result line.** Covers oauth, but only after the stage ends: too late to stop a runaway stage. | **2026-10-26: the recommendation** — in the gateway per stage, gateway runs only; oauth runs get report flags. Why: the only place that can stop a stage live. |
| **D332** | Which tripwires stop a run, and which only report? | **Stop (halt with D329's report): sub-agents per stage over the cap (3 on trivial/small) and the review-scope mismatch (F2). Report only, for now: loops, idle spin, scope drift.** Promote a heuristic to a stop after reports show it is reliable. | **All tripwires stop from the start.** Matches the spec, but heuristic loop detection will halt legitimate runs, and a gate that fires wrongly gets switched off. | **2026-10-26: the recommendation** — stop on fan-out and on the review-scope mismatch; report loops, idle spin and scope drift. Why: no false halts from heuristics. |
| **D333** | Is instruction pre-flight built, and when? | **Defer until D329's halt-with-report exists; then a short pre-implement check on `small` and larger tiers only.** One extra model call per run, and no channel to use before D329. | **Build it now as a prompt line in implement.** Cheap, but the agent has nowhere to send the flag, so it is advice, not a gate. | **2026-10-26: the recommendation** — deferred until D329 exists. Why: a flag needs somewhere to go. |

### 5.1 D320 followed through

The owner chose the alternative: a Fugaro run makes the report's pull request. What the tree then
forces (F35–F38):

- **The figures stay deterministic.** No recipe step can run an arbitrary command (F35) and no model
  may write a figure (G§6, D323), so the report is produced by a **new runner-side `digest` step**
  (the plan's T3.1–T3.3) that calls the same `internal/digest` library the CLI uses, commits with the
  runner's identity and skips implement, as the autofix gate does today. The model's part is not
  committing (the runner commits and opens the PR) but the **one senior review every recipe must end
  with** (F36). A later stage that edits a report file is reverted to the generated bytes.
- **A model cost per report run.** One review stage per run, per repository, per day the user
  schedules it. That conflicts with the recommendation's "no model call"; the owner accepted it with
  this answer, and the digest of those runs will show the figure (open item O3).
- **What the run can read.** U2 is resolved: the job's account already holds
  `roles/storage.objectUser` on its own repository's `runs/`, `cache/` and `locks/` prefixes
  (`deploy/terraform/gcp/modules/workflow/sa.tf` `google_storage_bucket_iam_member.job`, condition
  from `gcp.BucketCondition` over `gcp.JobBucketPrefixes`), so on a **per-repository job** it can
  read every run record of its own repository and none of another's. On a **project job** the
  condition is `gcp.ProjectBucketCondition`, slug-less, and reaches every repository's `runs/` — a
  reach the owner accepted on 2026-10-17 (`deploy/terraform/gcp/modules/project/sa.tf`) — so there
  D322's "this repository only" rests on the digest step's own slug check, which the plan tests.
  Project jobs are the **default** for new deployments, so this is the normal case, not the
  exception; see O7 for forged figures. No
  new grant. **Listing** the prefix is most likely refused (a list is a bucket-level call; the
  condition matches object names; `docs/design/bucket-iam.md` V7 leaves a prefix-limited list
  unverified): U8. The plan never lists in the job: the launcher, which already lists runs for
  `fugaro runs ls` with the person's credentials, writes the run IDs into the task, and the step
  reads each record by name (plan T3.6, T3.2). The job has no Firestore access (F38), so the
  report's dollars come from the run records, not `spendDaily`.
- **Who triggers it.** R8 stands: no Fugaro-side scheduler. The trigger is the user's own routine,
  cron, CI job or agent session running `fugaro run --recipe run-digest "periodic run review"`; the
  step writes every closed period not yet in the tree (with a seven-day catch-up), so a missed day
  is filled by the next run.
- **The command's name.** D320's row called it `fugaro review` first — it is the row that named the
  command at all — and D327's row used the same name for its submission half; the plan names it
  `fugaro runs digest` (and `--submit-draft`), because "review" already means a code review in
  Fugaro (`mode: review`, the review stage). The name is a plan choice, not a decision. **The reason
  has since grown a second half:** the cloud senior reviewer design
  ([docs/cloud-senior-review/](../cloud-senior-review/README.md)) claims `fugaro review` for its own
  command — `fugaro review --pr N`, which launches an independent review job (its **O2** ruling of
  2026-11-01, and its reconciliation's D880) — so the digest command could not take that name even if
  it wanted to. Both rows above are spelled the new way; the two spellings of `fugaro review` that
  survived in them were stale, not a second command.
- **The terminal path stays.** `fugaro runs digest` (plan T2.3) writes the same file locally, so a
  user who does not want a model cost per report can commit it by hand; the skill names both.

### The smallest useful first slice

R1 (calls and sub-agents per stage on the record) + D321/D323 (the digest command writing a
redacted file in the repository) + R2/R6 (the `working` skill rewritten around it); D320's report
run (plan Slice 3) is built on top of the same library. It
needs no runner behaviour change beyond recording, replaces the owner's private script, and is the
data every later governance step (G§7.3–7.9) and self-calibration read. G§7.1's remaining check
(F2) and R5's warning are small and can ride in parallel.

## 6. Open tensions and open items

- **T1 — Reports as history vs repository scope.** F24 makes committed reports the only long-term
  history; D322 keeps a report to one repository. A project-wide yearly view then needs either
  every repository's reports or a project-wide report somewhere (D322's flag). Rule once both are
  answered.
- **T2 — Public dogfood vs privacy.** D324 (publish Fugaro's own reports) and D323 (what a report
  may hold) interact: if D324 is "yes", D323's allowed list is what the public sees.
- **O1 — The prototype's reading guide cites #840 as the review-base fix**; the narrowing that
  matters now is #1073/#1082. The product text should cite the current mechanism.
- **O2 — `bootstrapRigor`'s comment says the mode "acts on nothing yet"**, stale since T6.1
  (reported by research; not this work's to fix).
- **T3 — D320 vs "no model call".** The owner's answer puts a model review stage on every report
  run (F36). The plan keeps the figures deterministic and keeps the terminal path (`fugaro runs
  digest`) so the owner can compare. For the owner to confirm once the figure is known (O3).
- **O3 — The report run's cost and budget.** Each run pays one review stage under the repository's
  per-run cap; the owner may want the run-digest recipe's review on a cheaper model or first-line
  tier. Not decided here.
- **O4 — Who triggers.** The user's own routine (R8). The skill (plan T4.1) says how; nothing in the
  product reminds a user who never schedules it.
- **O6 — Two readings the plan makes of answered decisions, for the owner to confirm.** D323's
  "the diff base" is rendered as a yes/no ("the review read the pull request's own base"), never as
  the recorded branch name, which is free text a task could steer (plan G1). G§2's fix-up budget is
  "a fraction of implementation budget": the plan makes the fix stage's budget a fraction of the
  implement stage's budget, as the spec says, and the other stages fractions of the per-run cap
  (plan T7.1). D332's "stop on fan-out" is applied to untiered runs too, with a default of 5
  sub-agents per stage (3 on trivial and small tiers), so the spec's own incident stops.
- **O7 — Forged figures on project jobs.** Project jobs are the default for new deployments
  (`docs/plans/2026-10-14-project-centric.md`: the per-repository path is "unreachable for new
  deployments"; `docs/git-providers.md` for Bitbucket), and on a project job any run can write a
  record under another repository's prefix (`SECURITY.md`; the reach the owner accepted on
  2026-10-17). The digest renders no text from a record (plan G1), but a planted record's **numbers**
  would reach a public report (D324). The plan does not detect forged records; the owner's
  acceptance of that reach is read here as extending to the report's figures, for the owner to
  confirm.
- **O8 — Continue after a checkpoint.** D329's Continue is a `--pr N` follow-up, which gets fresh
  stage budgets and a fresh per-run cap like any run; `fugaro run` has no flag to raise them for one
  run, and nothing counts a "second overrun" (G§4 requires a person then). The plan documents both
  in the skill (T6.2) and builds neither; for the owner to confirm or ask for them.
- **O5 — Left out of the plan, for the owner to confirm.** G§4's optional 50% soft check-in (a job
  cannot pause, and a halt at half the expectation would stop every slow medium task; the `working`
  skill's half-cap look stays the manual form), and G§5's idle-spin and edits-back-and-forth
  tripwires (nothing records file changes per model call, so there is no data). The loop tripwire
  the plan builds is the repeated identical command (plan T1.4), report only (D332).
- **O9 — The review-scope flag's rule.** T1.4 flags a run whose `review_scope` was narrowed to its
  own commits and then moved off them (`why_not` = `range-moved`) with a non-empty, untruncated
  `outside`. That is D332's reading: a non-applicable `outside` is a fact about the run and never a
  mismatch, so the wider rule the plan first wrote (any non-empty `outside`) would fire on nearly
  every run. The owner may still want the wider rule; nothing here is decided.

## 7. UNVERIFIED

- **U1** — The brief's "`fugaro watch` COST WATCH": no section of that name was found in
  `internal/watch` or anywhere in the tree. `watch` shows a project TOTAL bar (counted, spent,
  notional, burn rate), per-repository rows and a per-run `SPENT` column.
- **U2 — resolved 2026-10-26.** The job's account reads its own repository's `runs/<slug>/` prefix
  (F38); not another repository's.
- **U3** — Whether Claude Code's `result` line reports a call or sub-agent count usable for oauth
  runs (R1's oauth half).
- **U4** — That `x-claude-code-agent-id` distinguishes every sub-agent from the main session
  reliably (F12).
- **U5** — The Cloud Run job's maximum task timeout, which bounds D329's alternative.
- **U6** — Whether a required checkbox on an issue form is a sufficient licence grant (D328): the
  owner's, and counsel's, to confirm.
- **U8** — Whether the conditional `objectUser` grant lets a job **list** `runs/<slug>/` (a list is
  a bucket-level call, the runner lists nothing today, and `docs/design/bucket-iam.md` V7 leaves a
  prefix-limited list unverified). Likely refused; the plan does not need it (the launcher's
  manifest), and Check 135 records the answer.
- **U7** — The owner's private cost-lessons log was not readable; findings rely on `docs/`, the
  skills and the owner's prototype script and its 2026-10-25 report.
