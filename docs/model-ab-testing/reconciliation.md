# Model A/B testing: the reconciliation

**Status: reconciliation only; nothing is implemented; no decision answered; no task approved.**
Checked against `origin/main` at `c192e8766` (2026-10-28 21:54 -0400), re-checked at `029af537f` (two later merges, neither touching a cited path) and the docs of the 22 PRs open
on 2026-10-29. Written 2026-10-29. Where this document disagrees with [spec.md](spec.md), it wins.

Every finding cites a path and a symbol (never a line number). Every decision below is a row with a
recommendation, the alternative and its cost, and an **empty owner's-answer cell**: the owner has not
been asked yet. Nothing in this folder approves a task.

## 0. What the spec assumes, and what the repository has

| Spec concept | What `main` has today | Finding |
| --- | --- | --- |
| Rigor levels POC, Startup, Pro, Critical, Hotfix | `internal/rigor` (`Modes`, `Default = Pro`), `rigor:` in `fugaro.yaml` and the project layer, `--mode` on `fugaro run`, the resolved mode in `Record.Rigor` | F1 Exists |
| Job difficulty | the expectation tier: `internal/tier` (`trivial`, `small`, `medium`, `large`), `fugaro run --tier`, `Record.Tier` | F2 Exists, F17 Gap |
| "What it would have cost on model X" reports | **not built**: `fugaro estimate` is go-to-market task G1.3 (plan only; `internal/cli/estimate.go` absent); tokens per model (`Cost.ModelTokens`, G1.1) are not recorded | F3 Wrong |
| Daily, weekly and monthly cost reports | `fugaro report --by day\|week\|month\|…\|mode` (`internal/report`, `report.ByMode` reads run records), and the periodic digest `fugaro runs digest --period day\|week\|month\|year` (`internal/digest`, the `run-digest` recipe) | F4 Exists, F5 Rule |
| A job runs on the default and on a challenger | `fugaro run --attempts N` (2–4 identical runs under one batch label, budget pre-checked for all N at once in `batchBudgetPrecheck`, a read that reserves nothing) and the `pick` recipe (`--recipe pick --pr A --pr B`, `runner.PickPrompt`); the task wire's `Overrides.Model` (`task.Spec.Apply`) that no CLI flag writes | F6 Exists, F7 Gap |
| Only one result kept for merge | every code run pushes `fugaro/<run-id>`, may open an early draft PR (`config.PRSettings.EarlyDraft`, default on) and pushes checkpoints (`run.checkpointPush`); no code-run mode publishes nothing | F8 Gap |
| The orchestrator agent judges with a skill | no orchestrator agent exists: central orchestrators are plan-only (`docs/central-orchestrator/`, D1–D26 open; the `central-orchestrator` branch holds only `internal/lease`), and the "local orchestrator" is each developer's own agent | F9 Wrong |
| Blind judging | `pick` reads the PR checkouts, git history and branch names; nothing anonymises | F10 Gap |
| Hard signals | `Record.Verify` (build, test, lint), `Record.Reviews` (round, verdict, findings), `Record.Tolerated`, `Record.Halt`, `Record.Stages` (`calls`, `agents`, `repeats`), `Record.ReviewScope`, `Record.Cost.ModelBy`, start and finish times | F11 Exists |
| Later rework or reverts | follow-up runs on a PR (`Record.FollowUp.PreviousRun`), PR state through the coordinator's join; nothing reads reverts | F12 Gap |
| Per-project aggregation in the coordinator | the coordinator is a deterministic sweep job (`internal/coordinator`, `Sweeper.Sweep`), opt-in, moving to `ee/coordinator` (ee-reorg E2.1); every developer's runs of an installation already land in one runs bucket | F13 Exists, F14 Rule |
| Per-group minimum sample | `internal/stats.MinRuns = 20` suppresses a group below 20 runs (`fugaro stats runs`) | F15 Exists |

## 1. Findings

**F1 Exists: the rigor levels.** The five names are `rigor.Modes` (`poc`, `startup`, `pro`,
`critical`, `hotfix`), resolved once per run (`internal/rigor/resolve.go`) and recorded in
`Record.Rigor`. `fugaro report --by mode` already groups by it from run records. *Consequence:* the
grouping the spec asks for needs no new vocabulary; both arms of a pair must resolve the **same** mode,
or the pair is invalid (a challenger launched with a different `--mode` or `--path` set would resolve
differently).

**F2 Exists: a difficulty class.** `internal/tier` is a closed four-word vocabulary written on the
task (`task.Spec.Tier`, `fugaro run --tier`) and recorded in `Record.Tier`. Under ee-reorg D521 the
field and the vocabulary stay core; the governance policy that acts on them moves to `ee/`.
*Consequence:* the spec's open question "how job difficulty is classified" has an ex-ante answer the
launcher already writes, when it is written (D564).

**F3 Wrong: the "what it would have cost on model X" reports do not exist.** The spec's Goal treats
them as "today's" reports. They are the go-to-market plan's G1.1–G1.3 (`docs/go-to-market/`, plan only,
nothing approved). The record holds dollars per model (`Cost.ModelBy`) and cache token counts, not
input/output tokens per model. *Consequence:* the A/B work is the first cost comparison, not a
correction of an estimate; the spec's "record tokens" is met in dollars and wall time unless G1.1 lands
(the plan does not depend on it).

**F4 Exists: the daily, weekly and monthly reports.** Two surfaces: `fugaro report` (spend, read from
Firestore day records; `--by mode` from run records) and the periodic digest (`internal/digest`,
committed Markdown under `docs/fugaro-reviews/`, `digest.MaxBytes` 8 KiB, no person and no free text,
G1). *Consequence:* A/B rows get a report of their own, a new dedicated command, `fugaro ab` (D570, T1; the digest section is dropped, T2).

**F5 Rule: the licence line runs through the reports.** The owner's 2026-10-29 ruling moves
`fugaro report` and the coordinator under `ee/` (ee-reorg D530, E2.1, E2.2) and keeps the digest MIT
(ee-reorg §0, T11); paid sections join the MIT digest through a section hook (ee-reorg D528), and an
untagged build stubs paid commands with a capability statement (D522) and prints no licence notice
(D523, not the recommendation there). *Consequence:* where the A/B code lives is a decision (D550), and
any `ee/` part waits for the ee-reorg work it extends (E1.1's command registry, E1.5's `ee/register`, E2.2's `ee/report`). E1.1's launch hook only declares a run to the coordinator; it cannot start a second run, so a launch-planner hook, called before the champion's task is written, is new core work (plan A3.0).

**F6 Exists: best-of-N.** `fugaro run --attempts N` launches 2–4 identical runs as a batch and pre-checks
headroom for all N before any starts (`batchBudgetPrecheck` in `internal/cli/launch_budget.go`, which
calls `budget.Precheck`: a read through a `Reader`, nothing is reserved, and a refusal refuses the whole
batch); each run takes its own lease when it starts. `pick` then compares their PRs. *Consequence:* the
pair launch can reuse the batch label and the n = 2 pre-check, but a refused pair must fall back to the
champion's own single pre-check, and nothing at launch stops a dearer challenger from using headroom
the champion or another developer's run needs later (D573); but `--attempts` runs are identical (same model) and each opens a PR, so it is not
the A/B mechanism as is (D551, D554).

**F7 Gap: no per-run model choice on the command line.** `task.Overrides.Model` exists on the wire and
`task.Spec.Apply` honours it (all stages when `agent.models` has no roles; only the coder when it
does), but no CLI flag writes it. *Consequence:* `Overrides` holds one `Model` string and cannot carry roles, so
writing it would change the reviewer too in a repository with no `agent.models` roles. The challenger's
model travels in the task's `ab.challenger` instead, and the runner applies it to the coder role only
(D555).

**F8 Gap: no code run without side effects.** A code run pushes its branch with
`--force-with-lease` (`gitops.Push`), may open an early draft (`config.PRSettings.EarlyDraft`), pushes checkpoints
fast-forward (`run.checkpointPush`), posts PR comments and the status section. `mode: artifact` and
`mode: review` publish nothing but refuse `implement`. *Consequence:* a challenger that must not reach
the developer (spec: "only one result is kept for merge") needs a new publish switch in the runner:
the trust boundary of slice A2 (D554).

**F9 Wrong: the orchestrator agent the spec names does not exist.** Central orchestrators are
plan-only and, per the 2026-10-27 rulings recorded in ee-reorg's spec (A6), start in `ee/`. The only
orchestrator today is the developer's own agent driving the CLI through the plugin skills. A judge
there runs on the developer's machine, on whatever model that developer uses, unrecorded, and it
launched both runs, so it knows which model wrote which output. *Consequence:* "judged by the
orchestrator with a skill" cannot be blind or reproducible as written; the judge has to be a Fugaro
run (D552, D569).

**F10 Gap: nothing anonymises an output.** What can tell a judge which model wrote a change: the
branch `fugaro/<run-id>` and the run ID (both lead to the record, whose `stages[].model` names the
model), commit messages and trailers the harness writes, `pr.md` and the PR body's status section
(cost figures), the run's `report.md` comment, the size and style of the change itself, and, for
`pick`, the full checkout with history. The challenger writes the very diff it is judged on, so the diff can carry an instruction to the judge.
*Consequence:* blindness is a property of the judge's input,
built by an anonymiser that hands over only the two diffs against one base, relabelled A/B by a
seeded swap; style stays a residual leak (D552, D553, U2); the judge is a single tool-less model call (no checkout, no agent, no shell) whose answer is decoded from a closed vocabulary, anything else a tie.

**F11 Exists: the hard signals.** Per run: `Record.Verify` (build, test, lint at a head SHA; there is
no separate type-check kind), `Record.Reviews` (rounds, verdict, finding count, bounces),
`Record.Tolerated` (the findings the mode let through), `Record.Halt` (cap, token cap, governance),
`Record.Outcome`, `Record.Stages` with `calls`, `agents`, `repeats` and per-stage dollars,
`Record.ReviewScope`, wall time from `StartedAt`/`FinishedAt`. *Consequence:* a hard-signal score is
computable from records alone, without a model call (D561). Caveat: the review signals are produced by
the run's own reviewer; in this repository that is `deepseek/deepseek-v4-pro` (`fugaro.yaml`
`agent.models.reviewer`), a weaker same-run judge, so review findings are a comparable signal only when
both arms share the reviewer (D555).

**F12 Gap: post-merge signals exist for one arm only.** A follow-up run on a PR records
`FollowUp.PreviousRun`, and the coordinator joins PR state (merged, closed). Reverts are read by
nothing. The arm that opens no PR has no merge, no follow-up and no revert. *Consequence:* "later
rework or reverts" cannot enter a **paired** comparison; at most it calibrates the judge on the kept
arm later (D562).

**F13 Exists: pooling across developers.** All runs of an installation write to one runs bucket
(`runs/<repo-slug>/<run-id>/`), and `fugaro report` reads the installation's Firestore and records.
*Consequence:* project-level pooling needs no coordinator; the coordinator is one optional store, not
a prerequisite (D559).

**F14 Rule: aggregates and people.** The digest's rule G1 (no person, no free text) and `internal/stats`'
"no identifier can reach the output" are the repository's standing privacy line; `fugaro report --by
person` exists for spend. *Consequence:* an A/B report that could rank developers' tasks or models by
person is a new surface; the recommendation keeps the person dimension out (D560).

**F15 Exists: a minimum-sample convention.** `stats.MinRuns = 20`, and a group below it is suppressed,
not approximated. *Consequence:* the A/B minimum reuses the convention (D563).

**F16 Gap: the per-run cap censors the dearer arm.** A run halts when its next call's worst case would
pass `budget.per_run_usd` (`docs/how-to/budgets.md`), and rigor modes scale it
(`config.RigorBudgetFractions`). The owner's runs use a low per-run cap. A dearer challenger under the
same cap halts more often, so its quality is censored, not measured. *Consequence:* a halt is an
outcome of its own in the score, and the cap the challenger gets is a decision (D567).

**F17 Gap: the tier is optional and is governance.** Most runs carry no tier, and tier-driven limits
are `ee/` policy after the move. *Consequence:* grouping by tier leaves an "untiered" group, which may
be the largest; difficulty by an ex-post measure is endogenous to the arm (D564).

**F18 Gap: the challenger may send code somewhere new.** A provider model is routed only for
repositories in that provider's `allow_data_to` and only with `budget.mode: enforce`
(`config.CheckProviderPolicy`, `docs/multi-model.md` §2); the job carries the provider key's mount
only after `fugaro update --repo`. A challenger on a provider the repository does not already use would
widen where its code goes. *Consequence:* challengers are limited to models the run could already use,
named by the owner, never by `fugaro.yaml` (D556).

**F19 Gap: the two arms may start from different commits.** Each run records `StartSHA`; two runs
launched at once from the same ref usually, but not always, start from the same commit.
*Consequence:* both arms are launched at one resolved commit, and a pair whose `StartSHA`s
differ is invalid (D571).

**F20 Gap: A/B spend skews other figures.** Shadow runs count in the daily cap, in `fugaro report`, in
the digest's run counts and in the planned per-tier calibration (task-governance T8.3).
*Consequence:* records must say they are A/B arms so every consumer can include or exclude them
(D568).

**F21 Open: the spec's three open questions.** Default sampling rate and cadence (D557), difficulty
classification (D564), the A/B overhead budget per project (D566).

**F22 Gap: governance limits apply to both arms or the pair is unfair.** Stage budgets, the review
ratio and the fan-out stop (task-governance T7.1–T7.3, born in `ee/` per ee-reorg D527) halt a run
mid-way. *Consequence:* both arms resolve the same governance limits; in an untagged build both
ignore them (D522), so the comparison stays fair in either edition (D568).

## 2. Resolutions (one defensible answer; not questions)

- **R1.** Both arms are separate runs with separate IDs, branches and records, joined by a pair ID in
  a new `ab` block on the task and the record. Nothing writes one branch from two runs.
- **R2.** The pair is invalid, and excluded from every figure, when the arms' resolved rigor mode,
  tier, recipe or `StartSHA` differ, or when either arm never started.
- **R3.** "Reporting, not switching" holds: no code writes a default. The report's JSON carries the
  recommendation object (group, champion, challenger, interval, rule fired) as the seam for the
  spec's "Later" auto-flip; no writer of it is planned.
- **R4.** Automatic challenger selection is out of scope; the challenger list is the owner's.
- **R5.** No prices or commercial terms appear in this work; utility uses the run's own recorded
  dollars.

## 3. Decisions (owner's answers of 2026-10-29 recorded)

| # | Question | Recommendation and why | Alternative and its cost | Owner's answer |
| --- | --- | --- | --- | --- |
| **D550** | Which parts are MIT and which `ee/`? | **Core keeps the wire and the mechanism; `ee/` takes the policy (the D521 line).** MIT: the task's and record's `ab` block, the project layer's `ab:` keys, the schemas, the runner's shadow behaviour (`ab.publish: none`) and its patch upload (both arms), and generic hooks (a launch planner, a registered recipe step and its launcher manifest). `ee/ab`: the sampler, the challenger admission, the pair launch, scoring, statistics, the utility rule, the `fugaro ab` command, the anonymiser and the judge step. An untagged build decodes `ab` fields, never samples | **All MIT:** A/B becomes a free feature, against the ruling that cost reports are paid. **All `ee/`, runner included:** the runner's side-effect switch lives behind a hook in the tagged image only, and a source build cannot run a shadow arm someone else launched | **2026-10-29: Core plumbing MIT, the policy in `ee/ab`** (the recommendation). It keeps the D521 line without making the runner's shadow switch paid. |
| **D551** | What is the smallest useful first slice? | **Shadow-only, hard signals only:** a sampled task launches a challenger with `publish: none` (no push, no PR), both records carry the pair, and `fugaro ab` (D570) scores pairs from records; no model judge. Useful alone: it answers "cheaper and not worse on tests, review and halts" per mode | **`--attempts 2` plus `pick`, with a per-attempt model:** almost no runner code, but two PRs per sampled task reach the developer, and `pick` is neither blind nor independent. **The full spec at once:** the judge's leak questions (U2) block the cost data | **2026-10-29: Shadow challenger, hard signals only** (the recommendation). It answers cost and not-worse-on-tests with no judge leak questions. |
| **D552** | Who judges quality? | **v1: no model judge (hard signals, D561). Slice A5: an independent blind judge run** (`ab-judge` recipe) on a model the owner names (D553), reading only the anonymised bundle (two diffs against the base, relabelled by a seeded swap), judged twice with the order swapped; disagreement is a tie | **The local orchestrator's skill (the spec's wording):** unrecorded, varies per developer, and it launched both arms so it is not blind (F9). **The in-job reviewer:** same run, same family as one arm, and in this repository a flash-class model (F11) | **2026-10-29: Hard signals in v1, a blind judge run in slice A5** (the recommendation). A judge needs the anonymiser and U2's check first. |
| **D553** | Which model is the judge, and is blindness enough against same-family bias? | **An owner-named `ab.judge_model` in the project layer, recorded on the verdict; the report shows the judge's family (a Claude ID is `anthropic`; a provider model's family is its ID's vendor prefix, `deepseek/…` is `deepseek`) beside each result and flags groups where judge and one arm share a family.** Blindness removes labels, not style (U2), so the flag is the honest mitigation | **Always a third family:** no third family may be allowed for the repository (F18). **No judge ever:** quality reduces to hard signals, which miss design quality on POC/Startup work that passes tests | **2026-10-29: An owner-named `ab.judge_model`, with the family flag** (the recommendation). Blindness removes labels not style, so the flag is the honest mitigation. |
| **D554** | Which arm reaches the developer? | **Always the champion (the current default); the challenger is a shadow** with `publish: none`. The developer's flow is unchanged and the result never waits for a judge | **The judge's winner opens the PR:** the PR waits for both arms and the judge, and a weak judge picks the code. **Both open PRs, loser closed:** two PRs, CI twice, review noise, and the closed PR is a side effect | **2026-10-29: Always the champion reaches the developer** (the recommendation). The developer's flow stays unchanged and never waits for a judge. |
| **D555** | What differs between the arms? | **The coder model only; the reviewer, recipe, mode, tier and caps are the same.** The runner applies `ab.challenger` to the coder role only, never through `Overrides.Model` (which would change the reviewer too when no roles are set, F7), which keeps review findings comparable | **The whole model set:** answers "which stack", but the reviewer then judges itself and review signals stop being comparable | **2026-10-29: The coder model only** (the recommendation). The shared reviewer keeps review signals comparable. |
| **D556** | Which challengers may run, and who names them? | **Only models in the project layer's `ab.challengers`, set by the owner; each must pass the same checks a run's model does (`allowed_models`, a price, `CheckProviderPolicy` for the repository, the job already mounting the route's key) at launch, and again in the runner at bootstrap (a replayed or hand-edited task is checked too); a failing one is skipped at launch with a closed reason on the launcher's stderr and in `fugaro run --json`, and halts the shadow in the runner.** No new egress | **`fugaro.yaml` may name challengers:** a repository author can send that repository's code to a provider the owner opened only to others; F18 | **2026-10-29: Owner-named in the project layer, never `fugaro.yaml`** (the recommendation). A repository author must not open a provider the owner closed to that repository. |
| **D557** | Default sampling rate and cadence? | **`ab.rate` in the project layer, default 0 (off), at most 0.10; sampled by a hash of the run ID below the rate (deterministic, auditable), at launch, continuously; plus `ab.max_pairs_per_day` (default 5) per repository, counted by the launcher from today's challenger records under that repository's prefix of the runs bucket: a soft cap, since two developers launching at once can both pass it (overshoot bounded by concurrent launches; no new shared counter)** | **A fixed default rate (for example 5%) on every installation:** doubles a share of everyone's spend without an opt-in. **A cadence (one batch a week):** fewer, clumped samples, easier budgeting | **2026-10-29: Off by default, at most 0.10 by run-ID hash, at most 5 pairs per repository per day** (the recommendation). No opt-in spend and a deterministic, auditable sample. |
| **D558** | Which runs are eligible? | **First runs of a code task (default-mode recipe, no `--pr`, `--retry`, `--attempts`, `--run-id` (a chosen ID could steer the hash), review, artifact or digest recipe), modes POC to Critical. Hotfix excluded:** it is rare, time-bound, and its quality bar ("smallest safe change") is not what a pair measures | **Include Hotfix (the spec lists it):** a shadow never delays the fix, but the group may never reach a minimum sample. **Include follow-ups:** the pair then compares fixes to different first attempts | **2026-10-29: First runs of code tasks only** (the recommendation). Follow-ups and chosen run IDs would compare unlike attempts or steer the hash. |
| **D559** | Where does the A/B data live? | **In what exists: the `ab` block on both run records, both arms' patches under their own run prefixes (`runs/<slug>/<run-id>/ab/patch.diff`), the judge's verdicts under the judge run's own prefix (never written into another run's), aggregates computed at report time from records (as `--by mode` is).** No new store | **An aggregate written by the coordinator sweep to its RTDB subtree:** a stored project view the console can read, but the coordinator is opt-in and `ee/`. **A Firestore rollup:** faster reports, a new writer and rules change | **2026-10-29: Run records and the runs bucket** (the recommendation). No new store or writer. |
| **D560** | May A/B results be broken down by person? | **No: groups are mode × tier × model pair only; the requester is never read** (the digest's G1 line) | **A person dimension:** shows whose tasks favour which model, and becomes a performance surface across developers | **2026-10-29: No per-person breakdown** (the recommendation). It would turn the report into a performance surface across developers. |
| **D561** | Which hard signals, and how weighted per mode? | **A lexicographic score per pair, no invented weights:** (1) halt or no final head = loss; (2) build, test and lint pass at the final head (`Record.Verify`; a repository's type check runs inside its build or lint command); (3) review verdict ready vs not; (4) blocking findings in the last round that the mode does not tolerate; (5) fewer iterations (review rounds + fix stages + gate bounces). Per mode: POC uses (1)–(2); Startup (1)–(3); Pro (1)–(4); Critical (1)–(5) and requires the judge (slice A5) before reporting a recommendation. A tie on all used signals is a tie | **A weighted numeric sum:** one number per run, but the weights are invented and every weight is a decision. **Judge-only:** loses the anchoring the spec asks for | **2026-10-29: Ranked hard signals; Critical needs the judge** (the recommendation). No invented weights, and Critical's bar needs a quality read. |
| **D562** | Post-merge rework and reverts? | **Out of the paired comparison (F12). The kept arm's follow-up count is recorded and shown per group as context only** | **Open the challenger's PR too, to observe it:** the side effects D554 avoids. **Track reverts by scanning git history:** a new reader of the repository's history for a signal one arm can never have | **2026-10-29: Rework as context only** (the recommendation). It is observable for the kept arm alone, so it cannot enter a paired comparison. |
| **D563** | Statistics? | **Paired, deterministic: per group, wins/losses/ties per pair; an exact sign test on decisive pairs with a Clopper–Pearson interval; at least 20 decisive pairs (`stats.MinRuns`) before any conclusion; Holm's correction across the groups of one report; cost as the median of paired cost ratios.** No random numbers, so the same records give the same report | **Bayesian Beta posterior:** reads well at small samples, needs a prior per group. **Bootstrap intervals:** needs a seed to stay reproducible | **2026-10-29: Sign test, Clopper-Pearson, Holm, at least 20 decisive pairs** (the recommendation). Deterministic, reproducible and consistent with `stats.MinRuns`. |
| **D564** | How is difficulty classified? | **Ex ante only: the task's tier when written, otherwise `untiered`; groups are mode × tier.** No ex-post measure, since both arms' sizes differ | **The champion's diff size as a class:** fills the untiered group, but conditions the groups on one arm's outcome. **A model classifier at launch:** a model call per sampled task and a new place a task's text goes | **2026-10-29: Task tier set before the run** (the recommendation). An ex-post measure depends on the arm. |
| **D565** | The utility ("fuzzy logic") rule? | **A three-region rule with a per-mode exchange rate in the project layer:** recommend the challenger when it is not worse (interval's lower bound on loss rate below a tolerance) and cheaper; or better (lower bound of win rate above 0.5) and its median extra cost per pair is within the mode's rate; otherwise keep. "Not worse": the Clopper–Pearson upper bound of the challenger's loss share among decisive pairs is below 0.6. The rate is the extra median cost per pair as a fraction of the champion's median cost; defaults POC 0, Startup 0.25, Pro 0.5, Critical 2.0, owner-editable in the project layer. Testable, explainable | **A fuzzy inference system (membership functions, rule base):** the spec's word, but opaque and every function is an untested parameter | **2026-10-29: Three-region rule with extra-cost limits POC 0, Startup 0.25, Pro 0.5, Critical 2.0** (the recommendation). Testable and explainable, unlike an untested fuzzy rule base. |
| **D566** | The A/B overhead budget per project? | **A count cap (`ab.max_pairs_per_day`, D557) plus the existing caps: each challenger and judge run has the per-run cap and counts in the daily cap; before the champion's task is written, the launcher pre-checks the pair the way `--attempts` does (a read, `budget.Precheck` with `2 × budget.MinLaunchHeadroom`; a per-run cap cannot be the amount, since `Evaluate` refuses any lease above the per-run cap) plus D573's margin and, when either refuses, launches the champion alone, unmarked, on today's single pre-check** | **A dollar ledger for A/B spend (`ab.daily_usd`):** an exact ceiling, but a new counter in the budget database and its rules (a money-path change) | **2026-10-29: Daily pair count plus the existing caps** (the recommendation). No money-path change in the budget database. |
| **D567** | Which per-run cap does the challenger get? | **The champion's, unchanged; a halt is a loss and the report shows the halt rate per arm** (F16): it answers "does it work within what we pay per run" | **Scale the cap by the price ratio:** measures quality without censoring, but spends more on exactly the arm being doubted | **2026-10-29: The champion's per-run cap; a halt is a loss** (the recommendation). It measures quality within what a run is allowed to cost. |
| **D568** | Governance, tripwires and A/B spend in other figures? | **Both arms resolve the same governance limits (F22); a governance halt is a loss; a shadow arm's halt opens no draft and no checkpoint follow-up. Records marked `ab.arm: challenger` are excluded from tier calibration (T8.3) and counted apart in the digest; `fugaro ab` (D570) lists them, while the spend groupings (`--by day`, `repo`, `model`, …, read from day records that carry no arm) and the daily cap include shadow spend, stated in the report's notes. No budget-database change in v1** | **Count shadow runs like any run:** simpler, but skews calibration and per-repo cost toward the A/B share | **2026-10-29: Same governance limits; challengers counted apart and out of tier calibration** (the recommendation). Shadow runs must not skew calibration or per-repository cost. |
| **D569** | Who launches the judge, and when? | **A scheduled `ab-judge` run per repository, launched by the owner's routine like `run-digest`: the launcher lists the complete, unjudged pairs and hands the run a manifest of their patch objects (the `digestManifest` pattern); one judge run per repository at a time** (slice A5) | **The coordinator sweep launches judge runs:** automatic, but the sweep then holds launch rights and spends money. **The developer's agent after each pair:** per-developer variance, F9 | **2026-10-29: The judge on a schedule like the digest** (the recommendation). The sweep then holds no launch rights and spends nothing. |
| **D570** | Where do results appear? | **`fugaro report --by ab` (table, CSV, JSON with the recommendation object, R3) and an A/B section in the digest through the D528 hook; no console view in v1** | **A new command `fugaro ab report`:** a cleaner name, one more command for the plugin checks and the reference | **2026-10-29: a new dedicated command, `fugaro ab`** (**not the recommendation**; the recommendation was `report --by ab` plus a digest section). The owner chose the cleaner name; T1-T3 in section 4 are resolved, and the digest section is dropped (T2). |
| **D571** | When does the challenger start, from which commit? | **At the same launch, both at one commit: the launcher resolves the ref to a commit once and launches both arms at it; pairs whose recorded `StartSHA`s still differ are invalid (R2)** | **After the champion finishes:** same commit for free, but results arrive late and the pair's wall times run at different load | **2026-10-29: One commit, pinned at sampling time** (the recommendation). The pair is comparable and drifted pairs drop under R2. |
| **D572** | Should the plan reserve the post-v1 seam now? | **Only the recommendation object in the report JSON (R3) and the `ab` block's `challenger_source: owner` value; nothing that writes a default or picks a challenger** | **Build a dormant auto-flip writer:** code with no owner go and no test reaching production | **2026-10-29: Only a recommendation object in the report output** (the recommendation). The seam is reserved without code that writes a default. |
| **D573** | Runtime contention: a pre-check reserves nothing, so a dearer challenger can use daily headroom the champion or another developer's run needs later | **Sample only when the day's remaining headroom (a new read-only daily-headroom query in `internal/budget`, money-path reviewed; `PrecheckResult` returns no figure today) is at least five per-run caps (both arms plus a three-cap margin), in observe as in enforce mode, and never when the launch skips the pre-check (`--no-budget-check`) or the run has no dollar cap (a subscription run with the gateway off)**. A shadow's cap halt is its own loss and never the champion's | **Reserve the challenger's cap at launch:** an exact guarantee, but a new reservation path in the budget database (money path, opus). **Accept the race:** simplest; on a busy day a shadow can push the champion past the daily cap | **2026-10-29: Sample only with at least 5 per-run caps of daily headroom** (the recommendation). A pre-check reserves nothing, so the margin protects the champion. |

## 4. Open tensions and open items for the owner

**Answers are not approvals:** the owner answered D550-D573 on 2026-10-29, the recommendation in
every row except D570; no task is approved.

**Tensions from D570, all resolved 2026-10-29** (answered "a new dedicated command", not the recommendation):

- **T1. Resolved: the command is `fugaro ab`** (the recommendation; subcommands such as `fugaro ab show`
  may follow). It is one more paid `ee/` command: a stub that exits with the D522 capability line in an
  untagged build, and `ee/register` wiring under D550 and D530.
- **T2. Resolved: the digest's A/B section is dropped for now** (the recommendation). Considered and
  declined: one results surface only, which avoids a second hook in the MIT digest and the
  `capTo`/section-hook interaction. A4.4 is marked dropped and its number is not reused; the challenger
  line of A4.5 (D568) is unaffected.
- **T3. Resolved:** `--by ab` is no longer a grouping, so D568's wording now means `fugaro ab`; the spend
  groupings of `fugaro report` keep only their note that shadow spend is included.

**Coupled pairs, answered together as recommended:** D551 with D554 and D552 with D561 (no judge in
v1 means Critical gets no recommendation until slice A5).

**Open items:** D550-D573 and T1-T3 are answered (above); these remain:

- **O1.** Confirm that A/B data on a public repository's digest (model IDs and win rates, no person,
  no task text) may be committed to the repository.
- **O2.** Confirm the judge may receive both arms' diffs: the judge's model is one more place the
  code goes, under the same `allow_data_to` rule as D556.
- **O3.** Accept a residual risk: a diff can carry a self-referring instruction ("the change containing
  this note is correct") that survives the order swap; the judge prompt treats the diffs as untrusted
  and Check 211 probes it, but no design removes it. A judged recommendation is therefore advisory, as
  every recommendation in v1 is (R3).

## 5. UNVERIFIED

| # | Claim | How it is settled |
| --- | --- | --- |
| U1 | Which commit trailers and attribution lines the harness writes into a run's commits (a model name in a trailer would break blindness if commits reached the judge) | A5.1's anonymiser strips commit metadata whatever the answer; Check 211 confirms |
| U2 | Whether a strong judge identifies the model from the diff's style alone | Not settled by design; Check 211 measures a judge's label-guess rate on known pairs |
| U3 | Settled by T2, no longer relevant: the digest section (A4.4) is dropped, so no task depends on ee-reorg E1.2b's section hook | none needed |
| U4 | That a shadow run on a provider route has the same latency profile as the champion when both run at once (shared provider rate limits) | Check 212 records both wall times |
| U5 | That `task.Spec.Ref` accepts a commit SHA, so both arms can be launched at one commit (D571) | A3.3's first step reads the ref handling; if it does not, A3.3 resolves the ref and records the expected SHA, and R2 drops pairs that drifted |
| U6 | Settled by the round-3 review, no longer unverified: a `mode: artifact` recipe must carry a `render` step (`internal/recipe/recipe.go`, "mode: artifact takes one render step"), and `render` runs the repository's `commands.render` in a checkout, so the judge cannot be an artifact run without running repository-authored code beside both diffs | A5.2 adds a recipe mode of its own (`evaluate`: no checkout, no push, no PR, no git token, no repository command, one registered step) |
