# Per-launch limits and a chain budget: reconciliation against the code

Read against `origin/main` at `3d6ed04d` and the 15 pull requests open on 2026-11-01. Line numbers are
that commit's. Each finding is **Exists**, **Missing** or **Conflicts**, with its consequence for the spec
or the plan.

## 1. What exists

**F1: Exists. `fugaro run`'s flag set.** `newRunCmd` registers `--total-timeout`, `--tier`, `--mode`,
`--no-budget-check`, `--pr`, `--attempts`, `--retry`, `--recipe`, `--token`, `--plan-task` and the task
flags (`internal/cli/run.go:271-289`). There is **no flag for a USD cap, a token cap or a review-round
cap**, and `taskFlags` (`:292`) is the list a `--pr` follow-up excludes. *Consequence:* the four flags of
spec §2 are new; `--total-timeout` is the only one that exists and it keeps its meaning.

**F2: Conflicts. Two existing `overrides` members look like two of the four, and mean something else.**
`task.Overrides` (`internal/task/task.go:753`) carries `review_rounds`, `max_budget_usd`, `model` and
`total_timeout`, and `Spec.Apply` (`internal/task/task.go:1177-1184`) writes the first two into the running
config: `review_rounds` becomes `c.Agent.ReviewRounds`, **this run's** review-round count, and
`max_budget_usd` becomes `c.Agent.MaxBudgetUSD`, the **per-stage** `claude --max-budget-usd`
(`internal/agent/agent.go:102`). Neither has a flag that writes it, and neither has the scope this design
needs — a per-**run** round count and a per-**stage** dollar ceiling, not a per-**chain** limit. *Consequence:*
the four new members take **new names** (`max_usd`, `max_tokens`, `max_time`, `max_review_rounds`), because
reusing `max_budget_usd` or `review_rounds` for a chain-scoped number would change a field's meaning under
the rule that a field whose meaning must change gets a new name. The plan already does this (L2). The two
existing members keep their meaning and their readers untouched.

**F3: Conflicts. The per-run dollar cap is the job's environment, not a launch argument.**
`MaxRunUSDEnv` is `FUGARO_MAX_RUN_USD` (`internal/runner/spend.go:23`), read by `spendFromEnv` (`:93`), and
handed to the gateway as `Options.Cap` (`internal/runner/gateway.go:138`). The value is written into the
job when `fugaro setup` creates it and re-converged by `fugaro update`. *Consequence:* a per-launch cap cannot ride the job
environment; it must ride `task.json`, which is the one per-launch carrier the runner reads (spec §2).

**F4: Exists. The merge is tighten-only, in one place.** `policy.Merge` (`internal/policy/merge.go:44`)
takes a ceiling and any number of later layers, keeps the smaller positive cap and the stricter mode, and
records a looser value in `Ignored`. The runner calls it once at bootstrap with the owner's ceiling, the
default branch's file and the run's own file (`internal/runner/policy.go:65`), and never recomputes it.
*Consequence:* a per-launch limit is one more layer, and no new merge rule is written (spec §2, D904).

**F5: Exists. The halt vocabulary.** `runstore.HaltReason` (`internal/runstore/runstore.go:44`) holds
`run_cap`, `repo_daily_cap`, `global_daily_cap`, `no_cap`, `kill_switch`, `token_cap`, the budget- and
credential-unavailable reasons and the governance tripwires; `Halt` carries `reason`, `scope`, `at` and
`detail` (`:108`). *Consequence:* the chain's carry-over reads a halt it already understands; no new
reason is added by this work.

**F6: Exists. The gateway's cap is a lifetime cap with a lease.** `Options.Mode` is `Enforce` or `Observe`
(`internal/gateway/gateway.go:53`), and with a lease the gateway asks for money through `Lease.Grant`
(`internal/gateway/lease.go:20`), sized between `minLease` and `maxLease` (`internal/budget/lease.go:18-19`) by `:107`.
`Session.Admit` refuses a run whose database has no cap at all, with `no_cap` or a kill switch
(`internal/budget/session.go:501`). *Consequence:* the chain budget changes nothing here; it hands the
gateway a smaller cap (spec §4, D919).

**F7: Exists. The record already carries most of a chain ledger.** `Record` (`internal/runstore/runstore.go:610`)
carries `cost_usd` (`:655`), `cost` with `model_usd` and the compute split (`internal/runstore/cost.go:12`),
`stages[]` with `model_usd` and `duration_s` (`:531`), `budget.granted_micros` and `released_micros`
(`:847`), `reviews[].round` (`:146`), `halt` (`:684`), `follow_up.pr` and `.previous_run` (`:922`),
`started_at` and `deadline` (`:667`), and `policy.sources` / `.ignored` (`:859-865`). *Consequence:*
carry-over is a read over records that already exist, not a new store (spec §4, D906).

**F8: Missing. The record carries no token total.** The runner counts a run's tokens in memory
(`r.tokens`, `internal/runner/halt.go:137`) and checks the cap at stage boundaries (`:143`), but the value
is never written: `schemas/result.schema.json:97-98` has `cache_read_tokens` and `cache_write_tokens`
only, and those are gateway-only sums. *Consequence:* a token carry-over needs a new `result.json` member
(spec §4, D911). This is the one place the design must extend a wire format, and it extends it by adding a
member within version 1.

**F9: Missing. `fugaro runs` has no `show`.** `newRunsCmd` adds `ls`, `stats` and `digest`
(`internal/cli/runs.go:107-112`); `diagnose` is the failure view of one run
(`internal/cli/diagnose.go:126-129`). *Consequence:* the used-vs-remaining view needs a new subcommand, or
a new block on `diagnose` (spec §7, D915).

**F10: Exists. The PR link is already resolved from the runs bucket.** A follow-up's task gets `branch`,
`pr`, `previous_run`, `ref`, `workflow`, `task`, `requested_by`, `batch` and `overrides.total_timeout`
only, and the CLI refuses a PR whose runs disagree on the branch (`docs/design/v1.md` §4.4). *Consequence:*
the PR chain's root run id is already derivable (`fugaro/<run-id>`) with no new resolution (spec §3).

**F11: Exists. `--retry` and `--attempts` are already exclusive.** `--retry` launches a stored task as it
is and takes none of the task flags (`internal/cli/run.go:327-334`); `--attempts` refuses `--pr`,
`--run-id` and `--retry` (`:284`, `:300-308`). *Consequence:* a retry cannot carry new limits and a batch
cannot be a chain (spec §6, D913).

**F12: Exists. The console's launch is one exact argv with a closed never-table.**
`docs/design/desktop-console.md:211` is the launch row; `:220` is the never-table that names `--retry`,
`--pr`, `--attempts`, `--batch`, `--no-budget-check`, `--token`, `--mode`, `--total-timeout` and every
other `run` argv. *Consequence:* the console passes none of the new flags and no console task is in this
plan (spec §7, D915).

**F13: Exists. The coordinator's launch path is the intent writer, and it is advisory.**
`launchRun` calls the same writer `fugaro coordinator report` uses, so a launch declares itself without a
second command (`ee/coordinator/intent.go:20`), and the writer "takes nothing back… verifies nothing and
authorises nothing by" the declared string (`:34`). *Consequence:* the limits ride the launch, not the
intent, and `ee/coordinator/` is untouched (spec §6, D914).

**F14: Exists. The daily and project caps are the database's, and the sweeper errs high.**
`GlobalCaps`, `DefaultCaps`, `RepoCaps` and `Caps.PerRun()` (`internal/budget/model.go:208-294`) hold the
per-run, per-repository-day and per-project-day caps; `MinLaunchHeadroom` is the least daily headroom a
launch needs (`internal/budget/precheck.go:12`), and the sweeper "never releases anything… errs high,
never low" (`internal/budget/sweep.go:28-29`). *Consequence:* a chain split is bounded by the day caps
whatever the chain read says, and the unknown-usage case takes the sweeper's own direction (spec §5, §9).

**F15: Exists. The rigor modes' round ceiling is a table under a floor.** `roundCeilings`
(`internal/rigor/rounds.go:33`) is POC 1, Startup 2, Pro 3, Critical the widest, Hotfix 1, and
`RoundCeiling` (`:51`) holds each under `minRoundCeiling` 2 — the owner's O3 ruling of 2026-10-27
(`docs/rigor-modes/reconciliation.md:2126`). *Consequence:* `--max-review-rounds` composes under the mode's
number and never above it (spec §8, D917).

**F16: Missing on `main`. The cloud senior review is an open design, not code.** `docs/cloud-senior-review/`
is on **PR #1472** only, holding D880–D899 and claiming **Check 125**; it adds a second job with its own
named account. This is a *placement* finding rather than a gap in what this work needs: nothing in this
design depends on it. *Consequence:* its job is a separate chain unless the owner rules otherwise
(spec §8, D918).

**F17: Exists. A halted run still writes back.** `finalize` makes no model call, and `writeback`
(`internal/runner/lockcache.go:315`) sets `stage: writeback` and saves the record before the session and
the caches. *Consequence:* a halted run's usage is normally readable, and the writeback itself is not
charged (spec §5).

**F18: Exists. `--total-timeout` may raise the run's clock.** `parseTotalTimeout`
(`internal/cli/run.go:2354`) accepts up to `OverrideCap` (`:2344`) and writes `overrides.total_timeout`
(`:1306`, `:1557`). *Consequence:* `--total-timeout` cannot be the tighten-only wall-time limit; `--max-time`
is new, and the two compose as `min` (spec §2).

**F19: Exists. The D-number register is a dated reading.** `docs/coordinator/README.md` §"The numbers this
work holds" is the one table that allocates D-numbers, and its own row says so: *"the highest number
written anywhere in `docs/` at `c6009b5f` is D192"*. Later works read it at their own base commit and take
a block above everything in use (`docs/models-commands/README.md`, `docs/rigor-command/README.md`).
*Consequence:* this work reads the tree and the open pull requests rather than the register (README
§"Numbers this work holds").

## 2. What is missing, and what conflicts

- **Missing:** a launch flag for any of the four dimensions except time (F1); a token total in the record
  (F8); `runs show` (F9); any engine read of "what a chain already used" (F7 exists as data, not as a
  read).
- **Conflicts:** the per-run cap's carrier is the job environment, which a launch cannot write (F3); the
  one existing per-launch limit may raise (F18), so the new family is separate rather than a reuse.
- **The image skew.** `task.Parse` decodes with `DisallowUnknownFields` (`internal/task/task.go:863`), so a
  job image whose runner predates the new `overrides` members refuses the whole task. Every other field
  added to a task is gated by `checkImageSince` (`internal/cli/recipes_skew.go:700`) against a `*Since`
  release constant (`titlesSince` `:348`, `rigorRequestSince` `:511`, `governanceSince` `:531`), and the
  gates are called in one block in `launchRun` (`internal/cli/run.go:727-801`). A new field that skips the
  gate is a launch that fails in the container, which the HARD RULE forbids.

## 3. Decisions D901–D920

**Every one is open.** The owner has answered none; the recommendation comes first, the alternative and
its cost second. The spec is written to the recommendation.

| D | Question | Recommendation | The alternative, and its cost | Answer |
| --- | --- | --- | --- | --- |
| **D901** | Where is an explicit per-launch limit carried? | **In `task.json`'s `overrides`, gated by `checkImageSince` like every other task field (F3, F2).** | A new job environment variable through the backend: Cloud Run's `jobs.run` override does not carry env, and a value outside the task would be invisible in the record and to every reader | **open** |
| **D902** | One flag per dimension, or one bundle? | **One flag per dimension (`--max-usd`, `--max-tokens`, `--max-time`, `--max-review-rounds`).** Each has its own unit, its own refusal and its own ceiling; a bundle cannot name which dimension a refusal is about. | A single `--limit usd=5,tokens=900000`: one parser for four units, a typo in one dimension silently sets none, and the help text cannot state a default per dimension | **open** |
| **D903** | A value that would loosen: dropped or refused? | **Refused, naming both numbers and the layer that set the lower one.** A caller typed it one second ago; a silent drop tells them their limit took effect when it did not (F4). `--total-timeout` keeps its meaning and is not one of the four (F18). | Reuse `policy.Merge` and record the drop in `ignored`, as a repository file's looser value is: right for a file a team wrote once, a lie for a launch argument | **open** |
| **D904** | What does a launch tighten against? | **The effective policy the launcher resolves before the launch, merged again by the runner at bootstrap.** Two reads of one rule, so a launch that wrote a looser value is still held to the policy. | Trust the launch's own resolution and skip the runner's merge: one read, and a task written by hand or by an older CLI reaches the runner unchecked | **open** |
| **D905** | What identifies a chain? | **The link the launcher names: the PR (`--pr`), the stored task (`--retry`), or a new `--continues RUN-ID` for a chain with no PR.** The chain id is the **root run id**, derived by the engine from the records (F10). | A new explicit `--chain ID` string: a caller-chosen identity is a caller-chosen split, and two callers can name one chain or two for the same work | **open** |
| **D906** | Where does the chain ledger live? | **The run records (F7).** They already carry the cost, the stages, the rounds, the halt and the `previous_run` link, and every reader already reads them. | The budget database: a new node, a rules change, a second source of truth that can disagree with the record, and a write path for a launcher that today writes only its own run's prefix | **open** |
| **D907** | How is remaining computed? | **`remaining = chain limit − Σ settled usage over the chain's records`, read by the engine.** Settled dollars (`cost.model_usd`), not the lease (`granted_micros`, which is a reservation) (F6, F7). | Read the lease: a reservation is money held, not money spent, and a lease released at Close would make the chain read *less* than it spent | **open** |
| **D908** | May a caller supply "used"? | **Only as a further tightening: `--used-*` larger than the engine's read raises the charge; smaller is refused, naming both.** Keeps the honest orchestrator case (spend the records do not hold) and closes the understatement hole (T1). | Accept it as given: a caller states zero and the chain starts fresh, which is the hole this work exists to close | **open** |
| **D909** | A chain record that cannot be read: zero or unknown? | **Unknown, never zero: refuse the continuation unless the caller's `--used-*` covers it, naming the run.** The sweeper's own direction, "errs high, never low" (F14). | Treat a missing record as zero: one deleted object restores the whole chain's budget, which is a cheaper attack than forging one | **open** |
| **D910** | The halted-run writeback case | **No special rule: a halted run counts what its record settled, and a halt at the cap makes the restart's remaining small or zero, so a zero remaining is a refusal at launch (F17, spec §5).** | A grace that lets a halted chain restart with a fixed top-up: a number nothing measures, and a chain that can always restart is a chain with no cap | **open** |
| **D911** | The token total the record does not carry | **Add `tokens` to `result.json` (input/output/cache totals) in the same task that writes the token cap, within version 1 (F8).** An addition, so the console accepts any newer CLI on the contract. | Derive it from the stages' figures: the stage timings carry dollars, not tokens, so the derivation does not exist; or from `cache_read_tokens`/`cache_write_tokens`, which are gateway-only and exclude the input and output tokens the cap counts | **open** |
| **D912** | `--pr` follow-ups | **A follow-up inherits the chain's *remaining* limits and merges its own `--max-*` under them, tighten-only (F10).** A follow-up that names none gets the remaining amounts, which is the behaviour the owner asked for. | Inherit the limits as the previous run recorded them: the restart then gets the full cap again, which is the hole | **open** |
| **D913** | `--retry` and `--attempts` | **A retry is the same chain (its stored `run_id`), computed at its own launch; a batch is N chains, each with the full per-launch limit, bounded by the existing `batchBudgetPrecheck` (F11, F14).** | One chain for the batch: the attempts are alternatives, not continuations, and a shared budget would halt attempt 2 because attempt 1 was expensive | **open** |
| **D914** | The orchestrator and the coordinator's launch path | **The limits ride the launch arguments; the coordinator's intent record is unchanged and stays advisory (F13).** An orchestrator is a CLI caller like any other. | Put the limits in the intent record: the coordinator takes nothing back and authorises nothing, so a limit there would bind nobody, and `ee/` would gain a second place a budget is stated | **open** |
| **D915** | The CLI and display surface | **`fugaro runs show RUN [--json]` is new (F9), `diagnose` gains the same block for a halted run, and the console passes none of the new flags (F12).** | Put it on `runs ls`: a listing cannot show per-dimension limits without a column per dimension, and the chain read is per run | **open** |
| **D916** | The JSON contract | **Additions only, within the existing versions: `result.json` gains `tokens`; `task.json`'s `overrides` gains the four members; `runs show --json` is a new document; `launchResult` gains `chain` and `limits[]` (F1, F7, F8).** No member is renamed, retyped or removed and no version is bumped. | A new version for the run record: the console's contract accepts a newer CLI on the same version, and a bump would make an older console refuse a document it could read | **open** |
| **D917** | Under the rigor modes' loop defaults | **`--max-review-rounds` composes *under* the mode's ceiling: `min(recipe rounds, mode ceiling, chain remaining)`, and a value above the mode's number is refused (F15).** The mode's number is a ceiling on the recipe's ask; the limit is a ceiling on the chain. | Let the limit raise the mode's ceiling: a caller would buy rounds the mode exists to deny, and O3's floor would be bypassed by a flag | **open** |
| **D918** | Under the cloud senior reviewer | **A separate chain with its own limits unless the owner rules otherwise; a review job that must not outspend a chain passes `--continues` and gets the same carry-over (F16).** | Fold the reviewer into the coding chain's budget: it is a different job on a different account with a different purpose, and the design is not merged yet | **open** |
| **D919** | The gateway's lease and the daily caps | **Nothing changes: the chain budget hands the gateway a smaller cap and the database's day caps keep binding (F6, F14).** | Teach the gateway the chain: a second budget in the enforcement path, in the one component whose correctness the whole spend story rests on | **open** |
| **D920** | Clock skew in the chain's time read | **Take the larger of the record's `finished_at − started_at` and the sum of its stages' `duration_s`, so a doctored `started_at` cannot shrink the figure (T4).** | Trust `started_at` alone: a record whose start is moved forward reports less elapsed time, and the chain's clock budget grows | **open** |

## 4. Open items this work found and did not decide

**O1 — Whether a chain's limits should survive a merged PR.** A chain is the PR's runs (D905); when the PR
merges, the link is gone and a later run on the same task is a new chain. Whether that is right, or
whether a merged chain should carry into a follow-up branch, is a product question this design does not
answer.

**O2 — Whether the console should ever show a chain.** The console reads `runs show --json` (§7) like any
other document, so it *can*; whether the Spend screen should join a chain's runs into one row is the
console's own decision (`docs/console-coordinator-v2/`), and no console task is in this plan.

**O3 — What `fugaro update` must do.** Adding `overrides` members to `task.json` is additive, and a job
image that knows them is what the gate (D901) requires. Whether `fugaro update` should *also* re-converge
an existing job's environment, the way the models work's E2 does for provider copies, is not this
design's: nothing here changes a job's environment (F3), so there is nothing for `update` to carry.

## 5. UNVERIFIED

| # | Claim | Why it is not verified here |
| --- | --- | --- |
| U1 | That Cloud Run's `jobs.run` override cannot carry environment variables | Read off the Go client's API surface and the backend seam, not against the live API; the recommendation does not depend on it (D901 recommends the task either way) |
| U2 | That a launcher's write grant on the runs bucket is its own run's prefix only | Read from the rules' shape and `docs/design/v1.md` §6.1, not from a deployed rules file; T2's conclusion assumes it |
| U3 | The exact cost of the `runs show` read over a long chain | `docs/console-coordinator-v2/spec.md:131` measures `runs ls --json --since 24h` at about 5.8 s; a chain read is one listing plus the chain's records, unmeasured |
