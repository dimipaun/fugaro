# Per-launch limits and a chain budget: reconciliation against the code

Read against `origin/main` at `385510e3` (the merge this branch carries) and the open pull requests of
2026-11-01. Line numbers are that commit's. Each finding is **Exists**, **Missing** or **Conflicts**, with its consequence for the spec
or the plan. **F1–F19** are the draft's; **F20–F22** are the ledger, the two ways a run record can be written,
and the raise of an exhausted chain — all three read off the tree and settled by the owner's 2026-11-01
answers.

## 1. The findings

**F1: Exists. `fugaro run`'s flag set.** `newRunCmd` registers `--total-timeout` (`:276`), `--tier`, `--mode`,
`--no-budget-check`, `--pr`, `--attempts`, `--retry`, `--recipe`, `--token`, `--plan-task` and the task
flags, from `f := cmd.Flags()` at `internal/cli/run.go:266` through `addCloudFlags` at `:288`. There is **no
flag for a USD cap, a token cap or a review-round cap**, and `taskFlags` (`:292`) is the list a `--pr`
follow-up excludes. *Consequence:* the four flags of
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
`MaxRunUSDEnv` is `FUGARO_MAX_RUN_USD`, the const's own line in the block at `internal/runner/spend.go:21-27`
(`:23`; `BudgetModeEnv` is the line above it, `:22`), read by `spendFromEnv` (`:93`, its declaration line),
and
handed to the gateway as `Options.Cap` (`internal/runner/gateway.go:138`). The value is written into the
job when `fugaro setup` creates it and re-converged by `fugaro update`. *Consequence:* a per-launch cap cannot ride the job
environment; it must ride `task.json`, which is the one per-launch carrier the runner reads (spec §2).

**F4: Conflicts. The merge is tighten-only, but its layer has no wall-time or review-rounds member, and
its third layer has no name of its own.** `policy.Merge` (`internal/policy/merge.go:44`) takes a ceiling and
any number of later layers, keeps the smaller positive cap and the stricter mode, and records a looser value
in `Ignored`; the runner calls it once at bootstrap with the owner's ceiling, the default branch's file and
the run's own file (`internal/runner/policy.go:65`). But `policy.Layer` (`internal/policy/policy.go:423`)
carries `Mode`, `PerRunUSD`, `PerDayUSD`, `MaxRunTokens`, the two output limits, `AllowedModels`, `ABRate`,
`Governance` and `ReviewFloor` — **no wall-time member and no review-rounds member** — and `layerName`
(`internal/policy/merge.go:27-35`) returns `SourceBranch` for **every** index past 1. *Consequence:* a
launch/chain layer is **not** free. It needs a `Layer` member per new dimension and its own name in
`layerName`, or `runs show` would print a chain limit whose source reads `branch` — a source that is not
where the number came from. L3's `merge.go` entry is therefore unconditional, not conditional on a search
(spec §2, D1044, spec §7).

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
gateway a smaller cap (spec §4, D1059).

**F7: Exists. The run record carries the chain's shape, and it is the fallback ledger.** `Record`
(`internal/runstore/runstore.go:610`) carries `cost_usd` (`:655`), `cost` with `model_usd` and the compute
split (`internal/runstore/cost.go:12`), `stages[]` with `model_usd` and `duration_s` (`:531`),
`budget.granted_micros` and `released_micros` (`:847`), `reviews[].round` (`:146`), `halt` (`:684`),
`follow_up.pr` and `.previous_run` (`:922`), `started_at` and `deadline` (`:667`), and `policy.sources` /
`.ignored` (`:859-865`). *Consequence:* the record is where the chain's **links**, its **rounds**, its
**clock** and its **halt** come from — but not where its money does (D1046, spec §4). It is the fallback
ledger only when the budget is off, and then the chain is **UNENFORCED** (F20).

**F8: Missing. The record carries no *whole-run* token total — but the ledger does.** The budget database's
per-run ledger has a `tokens` leaf (`internal/budget/model.go:338`, increase-only at
`internal/budget/rules/rules.json.tmpl:87`), so with the budget on the token dimension needs **no new node
and no new field**: it reads `/runs/<slug>/<run>/tokens` like the money (F20). What does not exist is a
token figure in `result.json`: `schemas/result.schema.json:97-98` has `cache_read_tokens` and
`cache_write_tokens` only, and those are gateway-only cache sums that exclude the input and output tokens a
cap counts, while the runner's own total (`r.tokens`, `internal/runner/halt.go:139`) is in memory and never
written. *Consequence:* the **fallback** ledger cannot enforce a token limit and must say so rather than
approximate one (spec §4, §7, D1051). No wire format is extended by this work.

**F9: Missing. `fugaro runs` has no `show`.** `newRunsCmd` adds `ls`, `stats` and `digest`
(`internal/cli/runs.go:107-112`); `diagnose` is the failure view of one run
(`internal/cli/diagnose.go:126-129`). *Consequence:* the used-vs-remaining view needs a new subcommand, or
a new block on `diagnose` (spec §7, D1055).

**F10: Exists. The PR link is already resolved from the runs bucket.** A follow-up's task gets `branch`,
`pr`, `previous_run`, `ref`, `workflow`, `task`, `requested_by`, `batch` and `overrides.total_timeout`
only, and the CLI refuses a PR whose runs disagree on the branch (`docs/design/v1.md` §4.4). *Consequence:*
the PR chain's root run id is already derivable (`fugaro/<run-id>`) with no new resolution (spec §3).

**F11: Exists. `--retry` and `--attempts` are already exclusive.** `--retry` launches a stored task as it
is and takes none of the task flags (`internal/cli/run.go:327-334`); `--attempts` refuses `--pr`,
`--run-id` and `--retry` (`:284`, `:300-308`). *Consequence:* a retry cannot carry new limits and a batch
cannot be a chain (spec §6, D1053).

**F12: Exists. The console's launch is one exact argv with a closed never-table.**
`docs/design/desktop-console.md:211` is the launch row; `:220` is the never-table that names `--retry`,
`--pr`, `--attempts`, `--batch`, `--no-budget-check`, `--token`, `--mode`, `--total-timeout` and every
other `run` argv. *Consequence:* the console passes none of the new flags and no console task is in this
plan (spec §7, D1055).

**F13: Exists. The coordinator's launch path is the intent writer, and it is advisory.**
`launchRun` calls the same writer `fugaro coordinator report` uses, so a launch declares itself without a
second command (`ee/coordinator/intent.go:20`), and the writer "takes nothing back… verifies nothing and
authorises nothing by" the declared string (`:34`). *Consequence:* the limits ride the launch, not the
intent, and `ee/coordinator/` is untouched (spec §6, D1054).

**F14: Exists. The daily and project caps are the database's, and the sweeper errs high.**
`GlobalCaps`, `DefaultCaps`, `RepoCaps` and `Caps.PerRun()` (`internal/budget/model.go:208-294`) hold the
per-run, per-repository-day and per-project-day caps; `MinLaunchHeadroom` is the least daily headroom a
launch needs (`internal/budget/precheck.go:12`), and the sweeper "never releases anything… errs high,
never low" (`internal/budget/sweep.go:28-29`). *Consequence:* a chain split is bounded by the day caps
whatever the chain read says, and the unknown-usage case takes the sweeper's own direction (spec §5, §9).

**F15: Conflicts. `minRoundCeiling` is a floor under two of the table's five rows, and the ceiling a mode
spends is what `RoundCeiling` returns.** The table (`internal/rigor/rounds.go:31-38`) is POC 1, Startup 2,
Pro 3, Critical the widest, Hotfix 1, and `RoundCeiling` (`:51-56`) is
`min(max(n, minRoundCeiling), widestRoundCeiling)` with `minRoundCeiling` 2 and `widestRoundCeiling` 10. So
**POC's 1 and Hotfix's 1 are lifted to the floor and Pro's 3 and Critical's 10 are not**: the ceilings in
force today are **POC 2, Startup 2, Pro 3, Critical 10, Hotfix 2**. An earlier draft of the spec said "every
row is at or below the floor", which is false for two of the five rows, and the corrected claim is the
narrower one — quote **`RoundCeiling`'s output**, never the table's row. *Consequence:* `--max-review-rounds`
composes under that output and the engine's own refusal names it (spec §8, D1057). **The owner's per-level
loop defaults** — given 2026-11-01 — are **poc 1/1, startup 2/2, pro 3/3, critical 3/5, hotfix 1/1**:
**defaults only**, recorded as the owner gave them. **They are not O3's answer, and O3 is not contradictory
with them:** the rigor modes' own record settles O3 ("whether a mode may reduce the review round ceiling
below today's default of 2") with the owner's 2026-10-27 ruling that **the floor stays 2
(`minRoundCeiling`)** (`docs/rigor-modes/reconciliation.md` §8). A level whose default is 1 therefore runs
at **2** today, the floor `RoundCeiling` clamps it to, and whether the *floor* moves is an edit of
`minRoundCeiling` in `internal/rigor/rounds.go` that the **rigor work** owns. Nothing here moves with it:
this design quotes `RoundCeiling`'s output, so the figures in F15 are the same either way. Per run the
rounds are the level's default unless the recipe sets them, capped by that output and by the chain's
remaining (spec §8).

**F16: Missing on `main`. The cloud senior review is an open design, not code.** `docs/cloud-senior-review/`
is on **PR #1472** only, holding D880–D899 and claiming **Check 125**; it adds a second job with its own
named account, and its review-fix loop is a second reviewer round. This is a *placement* finding rather than
a gap in what this work needs: nothing here depends on it. *Consequence:* per the owner's answer of
2026-11-01, that job **joins the PR's chain** rather than opening a chain of its own, and the chain's
`--max-review-rounds` is what bounds its review-fix loop (spec §8, D1058). **#1472 is where the tie-in is
declared from its own side:** that design lists "a cap on the fix↔review loop" among the things it does not do
(its finding **F27**), so this design is where the cap lands — and it needs one thing from that one, that the
review job **define the record its verdict is written to**, because the chain counts rounds as the
`reviews[]` entries the runner writes (`internal/runstore/runstore.go:146`) and an iteration that lands
nowhere the chain can read is an iteration the cap never sees (spec §8).

**F17: Exists. A halted run still writes back.** `finalize` makes no model call, and `writeback`
(`internal/runner/lockcache.go:315`) sets `stage: writeback` and saves the record before the session and
the caches. *Consequence:* a halted run's usage is normally readable, and the writeback itself is not
charged (spec §5).

**F18: Exists. `--total-timeout` may raise the run's clock.** `parseTotalTimeout`
(`internal/cli/run.go:2354`, its declaration) accepts up to `OverrideCap` (`:2350`, the const; its comment
starts at `:2344`) and writes `overrides.total_timeout`
(`:1306`, `:1557`). *Consequence:* `--total-timeout` cannot be the tighten-only wall-time limit; `--max-time`
is new, and the two compose as `min` (spec §2).

**F19: Exists. The D-number register is a dated reading.** `docs/coordinator/README.md` §"The numbers this
work holds" is the one table that allocates D-numbers, and its own row says so: *"the highest number
written anywhere in `docs/` at `c6009b5f` is D192"*. Later works read it at their own base commit and take
a block above everything in use (`docs/models-commands/README.md`, `docs/rigor-command/README.md`).
*Consequence:* this work reads the tree and the open pull requests rather than the register (README
§"Numbers this work holds").

**F20: Exists. The budget database keeps a per-run ledger, and every leaf of it is increase-only.**
`RunLedger` (`internal/budget/model.go:332-347`) is `/runs/<slug>/<run>` and holds `reserved`, `released`,
`spent`, `notional`, `overrun`, `tokens`, `exp`, `crashed` and `byTier`. The rules
(`internal/budget/rules/rules.json.tmpl:56-91`) give each leaf its own write rule and no ancestor one, and
**every numeric leaf validates `NEW >= OLD`** — `reserved`, `released`, `spent`, `byTier/*/spent`,
`notional`, `overrun` and `tokens` alike; `exp` is write-once (`!data.exists()`); `crashed` is not in the
rules at all, because the sweeper writes it with database admin rights. Two further ties bind the leaves:
`released + spent <= reserved` and `spent + released <= reserved`, so a release may take at most the
unsettled amount, and `reserved` must move with the day counter by the same delta (`COUNTERS_FOLLOW`). The
rules' own identity test (`RUN`, `internal/budget/rules/rules.go:580`) requires
`auth.token.fs == $slug && auth.token.fr == $run`, so a token speaks only for the one run it was minted
for. *Consequence, and it is the load-bearing one:* `Outstanding()` = `Reserved − Released` **cannot be pushed
below `Spent` by any writer the rules bind** — every leaf is increase-only and a release is capped at
`reserved − spent` — so no run token, and no token a launcher minted, can lower the figure (spec §4, T3).
Two corrections belong beside it, and both are in the spec: **the floor is not the whole story**, because
`roles/firebasedatabase.admin` bypasses the rules (that is IAM, not a rule) and is held by budget admins
(`deploy/terraform/gcp/modules/firebase/iam.tf:53-59`), by the history account (`:81-85`) and by the
coordinator's writer (`:138-143`), each of which can write a ledger down; and **`RunLedger` has no leaf that
says a run is closed**, so there is no settled state to branch on and the read is `Outstanding()` in every
case (D1046, spec §4). *The read is the launcher's:* `runs/$slug/$run` is readable on `RUN` alone
(`internal/budget/rules/rules.json.tmpl:56-57`) and `RUN` requires
`auth.token.fs == $slug && auth.token.fr == $run` (`internal/budget/rules/rules.go:580`), so **a run's token
reads its own ledger and no sibling's** — while launchers and operators already hold
`roles/firebasedatabase.viewer` (`modules/firebase/iam.tf:44-50`), which reads every path: the **launcher**
performs the chain read and the runner re-reads only its own (spec §3; plan L1).

**F21: Conflicts. Who can write the run record, and who can write the ledger, are different questions.**
The launcher's grant is `roles/storage.objectUser` on the runs bucket conditioned on `runs/` **and**
`tokens/` (`deploy/terraform/gcp/modules/installation/iam.tf:172-190`, the condition at `:186`), so a
launcher **may write, overwrite and delete run records under `runs/`** — it is not confined to its own run's
prefix. A job's own
account is `roles/storage.objectUser` conditioned on `runs/<slug>/`, `cache/<slug>/` and `locks/<slug>/`
(`deploy/terraform/gcp/modules/workflow/sa.tf:22-35`), and per `docs/design/v1.md` §6.1 **"the condition
names objects, so it also denies listing the bucket… The runner never lists"** — and the agent shares the
container, so it reaches everything the job's account does. *Consequence:* two things the earlier draft got
wrong, both now fixed. (1) The chain read cannot be a **listing**: the launcher resolves the chain's run ids
from what it can read and passes them, and the runner reads each record **by name** (spec §3, §4; plan
L0/L5). (2) The run records are **not** a trustworthy carrier either — a launcher can forge one, and the
chain's **membership and its limit** live in exactly those records (D1046) — so a chain is a bound on an
**honest** orchestrator's runaway spend and not on a hostile launcher, and the only hard bounds are the
per-run and daily caps, which are the database's rules (spec §4, §9, T2/T4/T5; U2).

**F22: Conflicts. Raising an exhausted chain is an edit of a record, not a mechanism this work must add.**
Every cap in the system today is either the
owner's (the database, `fugaro budget set`) or the launcher's (a launch argument), and a chain limit is a
**launcher's** number (D1045). Because that number lives in the **root launch's `task.json`** (D1046), raising
an exhausted chain is a **rewrite of that stored task**, carrying the reason for the raise — no new command,
no new node and no operator-only write path. *What that costs, said plainly because the alternative is a claim
the tree does not support:* the launcher holds `objectUser` on the `runs/` and `tokens/` prefixes (F21) and
can rewrite that task exactly as an operator can, so this is a **convention the installation states**, not a
bound the platform enforces —
an honest launcher raises by that path and records why, and the per-run and daily caps stay hard
either way (spec §4, §6; D1061).

## 2. What is missing, and what conflicts

**Three statuses, one per bullet below, and a bullet's status is its first words:** *missing*, *conflicts*,
and *settled — no longer a conflict*. Only the first two are work; the third is a question this folder
raised, the owner answered on 2026-11-01, and nothing further is needed for.

- **Missing.** A launch flag for any of the four dimensions except time (F1); a whole-run token total in
  the fallback record (F8); `runs show` (F9); any engine read of "what a chain already used" (F7 exists as
  data, not as a read); and a **carrier** for a chain's membership and its limit, which this design answers
  with the run records themselves and no new node (F22, D1046).
- **Conflicts.** The per-run cap's carrier is the job environment, which a launch cannot write (F3); the
  one existing per-launch limit may raise (F18), so the new family is separate rather than a reuse; the
  policy layer has no member for two of the four dimensions and its third layer has no name of its own
  (F4); two of the rigor table's five rows sit below a floor, so *`RoundCeiling`'s output* is the ceiling and
  not the row (F15); and the run records are neither listable by the runner nor writable by the launcher alone
  (F21).
- **Settled — no longer a conflict, by the owner's answer (2026-11-01): the ledger question.** The budget
  database already holds a per-run ledger whose every leaf is increase-only (F20), so the *money and token*
  figures of a chain have an answer in the tree and need no new node, no rules change and no new wire field.
  Nothing is left to decide or to build for it; the reading is carried into the spec (spec §4). *What the
  answer does not claim*, recorded with it so nobody reads more into it than it says: the chain is **not
  enforced**. Its membership and its limit live in the run records, which a launcher or a compromised
  container can rewrite, so the chain bounds an honest orchestrator and the hard bounds stay the per-run and
  daily caps (spec §4, §9).
- **The image skew.** `task.Parse` decodes with `DisallowUnknownFields` (`internal/task/task.go:863`), so a
  job image whose runner predates the new `overrides` members refuses the whole task. Every other field
  added to a task is gated by `checkImageSince` (`internal/cli/recipes_skew.go:700`) against a `*Since`
  release constant (`titlesSince` `:348`, `rigorRequestSince` `:511`, `governanceSince` `:531`), and the
  gates are called in one block in `launchRun` (`internal/cli/run.go:727-801`). A new field that skips the
  gate is a launch that fails in the container, which the HARD RULE forbids.

## 3. Decisions D1041–D1061

**Every one is answered.** The owner answered all of them on 2026-11-01, **as recommended**, in the brief
that carries this fix round; the Answer column records that. The spec is written to the answer.

**The block is renumbered at merge.** This work's earlier draft held D901–D920, which open pull requests
now hold too (#1481 holds D901–D914, #1487 D920–D934, #1488 D940–D959, #1490 D960–D979, #1491/#1492
D980–D1010 and #1496 D1011–D1040), so the block moves to the first free one, **D1041**. It is
**D1041–D1061: 21 decisions** — the 20 of the draft plus **D1061**, the raise of an exhausted chain, which
the owner's second answer settles in the run record rather than in a new mechanism. The re-check is recorded
in [README.md](README.md) §"Numbers this work holds"; the open pull requests' numbers are the launcher's
2026-11-01 reading, and a merger re-reads them.

| D | Question | Answer | Why, and what the alternative cost |
| --- | --- | --- | --- |
| **D1041** | Where is an explicit per-launch limit carried? | **In `task.json`'s `overrides`, gated by `checkImageSince` like every other task field.** A chain limit exists **only** when it is set this way, at the **root** launch of the chain; a launch that names none gets today's per-run caps, per run. | Cloud Run's `jobs.run` override does not carry env, and a value outside the task would be invisible in the record and to every reader (F3) |
| **D1042** | One flag per dimension, or one bundle? | **One flag per dimension (`--max-usd`, `--max-tokens`, `--max-time`, `--max-review-rounds`).** | A single `--limit usd=5,tokens=900000` needs one parser for four units, a typo in one dimension silently sets none, and the help text cannot state a default per dimension |
| **D1043** | A value that would loosen: dropped or refused? | **Refused, naming both numbers and the layer that set the lower one.** `--total-timeout` keeps its meaning and is not one of the four. | Reusing `policy.Merge`'s `Ignored` drop is right for a file a team wrote once and a lie for a launch argument typed a second ago (F4, F18) |
| **D1044** | What does a launch tighten against? | **The effective policy the launcher resolves before the launch, merged again by the runner at bootstrap.** | Trusting the launch's own resolution is one read instead of two, and a task written by hand or by an older CLI reaches the runner unchecked |
| **D1045** | What identifies a chain? | **The link the launcher names: the PR (`--pr`), the stored task (`--retry`), or `--continues RUN-ID` for a chain with no PR.** The chain id is the **root run id**. `--continues` is refused with `--pr`, `--retry` and `--attempts`. | A caller-chosen `--chain ID` string is a caller-chosen split: two callers can name one chain or two for the same work (F10, F11) |
| **D1046** | Where does the chain's "used" come from, and where does the chain itself live? | **Two answers, kept apart.** The **money and token** figures come from the budget database's **per-run ledger** (F20): `used = Outstanding()` = `Reserved − Released` (`internal/budget/model.go:351`), **always** — there is no closed/unclosed branch, and no "settled" state to read. A chain's **membership and its limit** live in the **run records** (the root `task.json` `overrides`, the PR/retry/`--continues` link), and **0.15.0 adds no node and no rule to the database**. With the budget off the records are the figures too, and that chain is **UNENFORCED**. | The other reading is a claim the tree does not support: a launcher — and the agent that shares its container — can write and delete records (F21), so the *chain* is a convention and only the per-run and daily caps are hard bounds. Saying so is the decision (spec §4, §9) |
| **D1047** | How is remaining computed? | **`remaining = chain limit − Σ used over the chain's runs`**, with the *used* figure of D1046. | Reading the lease's `reserved` alone: a reservation is money held, not spent, and a released lease would make the chain read *less* than it spent (F6) |
| **D1048** | May a caller supply "used"? | **Only as a further tightening: `--used-*` larger than the engine's read raises the charge; smaller is refused, naming both.** | Accepting it as given lets a caller state zero and start the chain fresh (T1) |
| **D1049** | A chain's ledger that cannot be read: zero or unknown? | **Never zero: the run is charged its full per-run cap** — the most it could have been holding — and a caller's `--used-*` may only raise that charge. | A missing ledger read as zero restores the whole chain's budget, which is cheaper than forging one (F14); refusing the continuation outright would turn one failed read into a stopped chain, and the cap is the figure that favours the bound (spec §4, §5) |
| **D1050** | The halted-during-writeback case | **No special rule.** A restart charges `Outstanding()` — what the halted run still holds — which is exactly the "held" figure the cap's own message names (`internal/budget/lease.go:211`). A halt at the cap therefore leaves a small or zero remaining, and a zero remaining is a refusal at launch. | A grace that lets a halted chain restart with a fixed top-up is a number nothing measures, and a chain that can always restart is a chain with no cap (F17) |
| **D1051** | The token dimension's figure | **The ledger's `tokens` leaf (F20) — no new node, no rules change, no new field.** In the budget-off **fallback** the token dimension is **not measured**: the record has no whole-run total, so the fallback says so instead of approximating from cache sums. | A new `result.json` member: it would be a second token figure that can disagree with the one the rules enforce, for a dimension the ledger already carries |
| **D1052** | `--pr` follow-ups | **A follow-up inherits the chain's *remaining* limits and merges its own `--max-*` under them, tighten-only.** | Inheriting the limits as the previous run recorded them gives the restart the full cap again, which is the hole |
| **D1053** | `--retry` and `--attempts` | **A retry is the same chain (its stored `run_id`), computed at its own launch; a batch is N chains, each with the full per-launch limit, bounded by the existing `batchBudgetPrecheck`.** | One chain for a batch: the attempts are alternatives, not continuations, and a shared budget would halt attempt 2 because attempt 1 was expensive (F11, F14) |
| **D1054** | The orchestrator and the coordinator's launch path | **The limits ride the launch arguments; the coordinator's intent record is unchanged and stays advisory.** An orchestrator is a CLI caller like any other. | A limit in the intent record binds nobody: the coordinator takes nothing back and authorises nothing (F13) |
| **D1055** | The CLI and display surface | **`fugaro runs show RUN [--json]` is new, `diagnose` gains the same block for a halted run, and the console passes none of the new flags.** The `source` of each limit is printed from the policy layer's own name (F4). | A column per dimension on `runs ls`: the chain read is per run, and a listing cannot carry it (F9, F12) |
| **D1056** | The JSON contract | **Additions only, within the existing versions: `task.json`'s `overrides` gains the four members; `runs show --json` is a new document; `launchResult` gains `chain` and `limits[]`. No `result.json` change at all (D1051).** | A new version for the run record would make an older console refuse a document it could read |
| **D1057** | Under the rigor modes' round limits | **Refuse only against the chain's own limit, never against the mode's.** Per run the rounds are the level's default unless the recipe sets them, capped by **`RoundCeiling`'s output** and by the chain's remaining, and a refusal names that output — today POC 2, Startup 2, Pro 3, Critical 10, Hotfix 2 (F15). | Refusing a `--max-review-rounds` above the mode's number would refuse a launch the engine would run, and would make a per-launch limit a second copy of the mode; quoting the table's row instead of the function's output would refuse against a number no run spends |
| **D1058** | The cloud senior reviewer | **A review job on PR N joins the PR's chain by its PR link, and the chain's `--max-review-rounds` counts senior-review iterations** — it is what bounds that design's review-fix loop. It does not open a chain of its own. | Folding the reviewer into the coding chain by *budget merge* rather than by link: it is a different job on a different account, and the link is what the chain already means (F16) |
| **D1059** | The gateway's lease and the daily caps | **Nothing changes: the chain hands the gateway a smaller cap and the database's day caps keep binding.** | Teaching the gateway the chain puts a second budget in the enforcement path, in the one component the whole spend story rests on (F6, F14) |
| **D1060** | Clock skew in the chain's time read | **Take the larger of the record's elapsed time and the sum of its stages' `duration_s`.** | Trusting `started_at` alone lets a record whose start is moved forward report less elapsed time |
| **D1061** | How does an exhausted chain get raised? | **By rewriting the root launch's `task.json` `overrides`, with the reason for the raise recorded** — the limit lives in that record (D1046), so no new command, no new node and no operator-only write path is needed. **Stated with it, because it is the honest reading: a launcher can do the same.** | Calling it operator-only would be a claim the platform does not enforce: a launcher holds `objectUser` on the `runs/` and `tokens/` prefixes (F21) and can rewrite the task itself, so the rule is a **convention the installation states** — an honest launcher raises through that path and records why, and the per-run and daily caps stay hard either way (F22, spec §4, §6) |

## 4. Open items this work found and did not decide

**O1 — Whether a chain's limits should survive a merged PR.** A chain is the PR's runs (D1045); when the PR
merges, the link is gone and a later run on the same task is a new chain. Whether that is right, or
whether a merged chain should carry into a follow-up branch, is a product question this design does not
answer.

**O2 — Whether the console should ever show a chain.** The console reads `runs show --json` (§7) like any
other document, so it *can*; whether the Spend screen should join a chain's runs into one row is the
console's own decision (`docs/console-coordinator-v2/`), and no console task is in this plan.

**O3 — What `fugaro update` must do.** Adding `overrides` members to `task.json` is additive, and a job
image that knows them is what the gate (D1041) requires. Nothing here changes a job's environment (F3), so
there is nothing for `update` to carry.

**O4 — How a chain's `remaining` reaches a job whose budget is off.** The budget-off fallback reads the run
records (D1046) and is UNENFORCED, so a chain limit in that mode is a report and not a stop. Whether that
mode should refuse a chain limit outright, rather than honour it without enforcing it, is a product
question this design does not answer. The spec states the mode and its limit plainly (spec §4).

## 5. UNVERIFIED

| # | Claim | Why it is not verified here |
| --- | --- | --- |
| U1 | That Cloud Run's `jobs.run` override cannot carry environment variables | Read off the Go client's API surface and the backend seam, not against the live API; the answer does not depend on it (D1041 carries the limits in the task either way) |
| U2 | ~~That a launcher's write grant on the runs bucket is its own run's prefix only~~ | **Withdrawn.** The grant's own condition (`deploy/terraform/gcp/modules/installation/iam.tf:172-190`, `:186`) names `runs/` and `tokens/` as **prefix matches**, not one run's prefix, so a launcher can write and delete any repository record under `runs/`. F21 records the fact and D1046 is what it costs the design |
| U3 | The exact cost of the `runs show` read over a long chain | `docs/console-coordinator-v2/spec.md:131` measures `runs ls --json --since 24h` at about 5.8 s; a chain read is one resolution plus the chain's ledgers, unmeasured |
| U4 | ~~Whether a chain's ledger read may cross runs~~ | **Settled, and no longer an open item.** `runs/$slug/$run` carries `.read: "RUN"` and nothing else (`internal/budget/rules/rules.json.tmpl:56-57`), and `RUN` requires `auth.token.fs == $slug && auth.token.fr == $run` (`internal/budget/rules/rules.go:580`), so **a run's token cannot read a sibling's ledger** — `USER` reaches only `spend/<day>/runs/$slug/$run`. The read is therefore the **launcher's**, which already holds `roles/firebasedatabase.viewer` (`deploy/terraform/gcp/modules/firebase/iam.tf:44-50`) and reads every path, and the runner re-reads only its **own** ledger plus the chain's records (spec §3, §4; plan L1). The earlier draft called this an admin read and made it the plan's one blocking pre-work item; both were wrong — the launcher holds the **viewer** role, not the admin one — and the answer changes **who performs the read, not one figure and not one test** |
