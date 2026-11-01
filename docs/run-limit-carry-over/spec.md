# Per-launch limits and a chain budget: the spec

Written to the **recommended** answer of each decision (D901–D920, [reconciliation.md](reconciliation.md)
§3). A decision answered the other way changes the paragraph that names it. Repository names are
placeholders (`owner/repo-a`, `owner/repo-b`); so are run ids and account names.

## 1. The rule this serves

A run's ceilings are set before the launch, from the owner's ceiling, the default branch's `fugaro.yaml`
and the run's own `fugaro.yaml`, merged **tighten-only** (`internal/runner/policy.go:65`,
`internal/policy/merge.go:44`). The engine is the judge of every number; a caller only ever asks.

Two consequences the brief names follow from that, and this design keeps both:

- **A per-launch limit tightens and never loosens.** A launcher — a person at a terminal, the console, an
  orchestrator — may ask for *less* than the policy allows and for nothing more. The engine resolves the
  effective policy at bootstrap and merges the launch's request under it (D903).
- **The engine reads what was already spent.** A restart's remaining budget is `limit − used`, where
  `used` is read from the run records by the runner and by the launcher, never supplied by the caller
  (D907, D908). A caller that could state its own usage could state zero.

## 2. Explicit per-launch limits

Four dimensions, one flag each (D902), all optional, all tighten-only:

| Flag | Dimension | What it bounds |
| --- | --- | --- |
| `--max-usd X` | dollars | the chain's model spend, in USD |
| `--max-tokens N` | tokens | the chain's token total |
| `--max-time D` | wall time | the chain's elapsed run time, in Go duration form |
| `--max-review-rounds N` | review rounds | the chain's review rounds |

They are carried in `task.json`'s `overrides` block, beside the `total_timeout` that is there today
(`internal/task/task.go:753`), so they travel with the task the job reads and are visible in the record
(D901). The runner merges them as one further `policy.Layer`, **after** the run's own `fugaro.yaml`, so
the tighten-only rule is unchanged and no new merge rule is written (D904).

`--total-timeout` **keeps its present meaning** (D903): it is a timeout, it may raise the run's own clock
up to `OverrideCap` (`internal/cli/run.go:2344`), and it is not one of the four. `--max-time` is the new,
tighten-only one. A launch that gives both is accepted; the run's clock is `min(--total-timeout,
--max-time remaining)`, and the report says which one bound it.

**A value that would loosen is refused, not dropped.** The repository layer's looser values are dropped
and recorded in the policy record's `ignored` list (`internal/runstore/runstore.go:865`), which is right
for a file a team wrote once. It is wrong for a number a caller typed at a terminal one second ago: a
silent drop tells the caller their limit took effect when it did not. So the launcher resolves the
effective policy *before* the launch, and a `--max-*` above the effective limit is a `userErr` naming
both numbers and the layer that set the lower one (D903).

**A per-launch limit that cannot reach the job is not written.** `task.Parse` decodes with
`DisallowUnknownFields` (`internal/task/task.go:863`), so a job image whose runner predates these fields
refuses the whole task. The launcher therefore gates a launch that writes any of them behind
`checkImageSince` (`internal/cli/recipes_skew.go:700`) with this work's own `*Since` constant, exactly as
the project layer, the named tokens, the title, the plan task, the rigor request and the governance block
are gated today (`internal/cli/run.go:727-801`). A refused launch names `fugaro image refresh`, and that
command stays the operator's, in their own terminal — never a step `fugaro update` hides (D901).

## 3. What identifies a chain

A **chain** is the set of runs that share one unit of work and one budget (D905). Three links exist
today, and this design uses them rather than inventing a fourth:

1. **The pull request.** A follow-up (`--pr N`) already records `follow_up.pr` and `follow_up.previous_run`
   (`internal/runstore/runstore.go:922`), and the launcher resolves the PR's runs out of the runs bucket
   (`docs/design/v1.md` §4.4). A chain is the PR's runs.
2. **The task.** A `--retry` relaunches a stored task as it is (`internal/cli/run.go:327`); the task's
   `run_id` and the batch label are the chain's identity when there is no PR yet.
3. **A new `--continues RUN-ID`.** For a chain with no PR — a halted run that opened nothing (`outcome:
   none`, `docs/design/v1.md` §4.5) — the launcher names the run it continues explicitly (D905).

The chain id is the **root run id** of the link: the first run of the PR's branch (`fugaro/<run-id>`, the
branch name the root run's id is read from), the retried task's `run_id`, or the run `--continues` names.
It is derived by the engine from the records, never taken from the caller as a string to trust.

## 4. Where the chain ledger lives, and how remaining is computed

**The run records are the ledger** (D906). Each run's `result.json` already carries what the chain needs:
`cost_usd` and `cost.model_usd` (`internal/runstore/runstore.go:655`), `stages[].model_usd` and
`duration_s` (`:531`), `budget.granted_micros` and `budget.released_micros` (`:847`), `reviews[].round`
(`:146`), `halt` (`:684`), `follow_up.previous_run` (`:924`), `started_at` and `deadline` (`:667`). No new
store, no new node, no second source of truth.

`remaining` is computed in one place, `runstore`-side, and read by two callers:

```
remaining(dimension) = chain_limit(dimension) − Σ over the chain's records of used(record, dimension)
```

- **dollars**: the record's settled model dollars (`cost.model_usd`), not the lease it was granted
  (`budget.granted_micros` is what was *reserved*; `internal/gateway/gateway.go:201`). A run that halted
  mid-call settles its in-flight calls, so the settled figure is the real one.
- **tokens**: the record's token total — **a field that does not exist today** (F8). The runner counts
  `r.tokens` in memory (`internal/runner/halt.go:139`) and never writes it. The design **adds** it to
  `result.json` as a new member (`tokens`, an object of input/output/cache totals); a member is only ever
  added within version 1 (the no-semantic-field-changes rule), and the schema gains it in the same task
  (D911).
- **time**: the **larger** of the record's own elapsed time and the sum of its stages' `duration_s`, so a
  doctored `started_at` cannot shrink the figure (D920). The record's own elapsed time is
  `finished_at − started_at` for a settled run, and `now − started_at` for a run still running (the
  launcher reads it from the row it already has, `internal/runview/runview.go:74`), and each stage's
  `duration_s` is already in the record (`internal/runstore/runstore.go:531`).
- **review rounds**: the count of `reviews[]` entries, whose `round` the runner already writes
  (`internal/runstore/runstore.go:146`).

**The caller never supplies `used`** (D908). A caller-supplied usage number is accepted only as a
*further tightening*: a `--used-usd X` that is **larger** than what the engine read raises the charge, and
one that is smaller is refused, naming both figures. That keeps the one honest use of the knob (an
orchestrator that knows about spend the records do not, such as a sibling chain) without giving a caller a
way to understate.

**The engine applies `remaining`, not `limit`, to the run.** The runner reads the chain's records at
bootstrap, computes `remaining`, and merges it as the last tighten-only layer over the four dimensions
(§2). So the gateway's per-run cap for the restart *is* `remaining`, and the token cap and the clock
follow the same figure. Nothing in the gateway changes: it still enforces one cap, it is simply handed a
smaller one (D919).

## 5. The halted-run writeback case

A halted run still finalizes and writes back: `finalize` makes no model call, and `writeback` sets
`stage: writeback` and saves the record first (`internal/runner/lockcache.go:315`). Three cases, and the
design settles each (D910):

1. **The record is there and its halt is recorded.** The chain counts the record's usage as any other
   run's. A halt at the cap is exactly the case carry-over exists for: the restart's `remaining` is small
   or zero, and a zero remaining is a **refusal at launch**, not a run that starts and halts at once.
2. **The run halted and the writeback did not finish** — the container died between the halt and `save`,
   or the record is unreadable. The usage is **unknown, never zero** (D909). The launcher refuses the
   continuation unless the caller passes an explicit `--used-*` that covers it, and the message names the
   run whose record could not be read. This is the sweeper's own rule applied to the read side: an
   outstanding amount "stays counted in the day's counters (errs high, never low)"
   (`internal/budget/sweep.go:28-29`).
3. **The halt's own writeback is the thing the cap held up** — the run was halted by the gateway and the
   *record write* is all that ran afterwards. This is not a special case and gets no special rule: the
   writeback makes no model call and is not charged, so the record's cost is the last stage's settled
   cost, and the chain reads it as case 1.

A halted run that opened **no** PR (`outcome: none`) has no PR link, so its continuation is `--continues
RUN-ID` (§3), and the launcher says so when `--pr` finds nothing.

## 6. Interaction with the launch paths that exist

- **`--pr` follow-ups.** The follow-up already inherits `overrides.total_timeout` from the PR's runs and
  nothing else (`docs/design/v1.md` §4.4). It now also inherits the chain's *remaining* limits, because
  the chain is the PR (§3). A `--max-*` given on the follow-up is merged under the inherited remaining,
  tighten-only, so a follow-up cannot re-open what the chain spent (D912). A follow-up that names no
  `--max-*` gets exactly the remaining amounts, which is the behaviour the owner asked for: the restart
  does not start from zero.
- **`--retry`.** A retry relaunches the stored task as it is (`internal/cli/run.go:327`) and takes no
  flags, so it cannot carry new limits; it inherits the stored task's, and the chain is the stored
  `run_id`. Because the stored task is written once, a retry's `remaining` is computed at *its* launch
  from the chain's records, exactly as a follow-up's is (D913).
- **`--attempts` (best-of-N).** A batch launches N independent runs of one task and refuses `--pr`,
  `--run-id` and `--retry` (`internal/cli/run.go:284`). The batch is **N chains, not one** (D913): the
  attempts are alternatives, not continuations, so each gets the full per-launch limit, and the *batch*
  is bounded by the existing `batchBudgetPrecheck`, which leases `N × budget.MinLaunchHeadroom` once
  before any attempt (`internal/cli/launch_budget.go:316`, `internal/budget/precheck.go:12`). A per-launch
  limit on a batch applies to every attempt; the batch's total bound is the budget database's, unchanged.
- **The central orchestrator and the coordinator.** `docs/central-orchestrator/` is on `main` as a plan
  only (its own words: *"plan only; no code is implemented"*), and the coordinator's launch path is the
  intent writer, which `launchRun` already calls so a launch declares itself without a second command
  (`ee/coordinator/intent.go:20`). The coordinator is advisory: it takes nothing back, verifies nothing
  and authorises nothing by a declared string (`ee/coordinator/intent.go:34`). So the limits ride the
  **launch**, not the intent: an orchestrator passes `--max-*` and `--continues` as ordinary launch
  arguments, and the intent record stays a declaration (D914). Nothing in `ee/coordinator/` changes.

## 7. The CLI and JSON surface

**Flags** (§2) on `fugaro run`. The console's spawn contract is one exact argv with a closed never-table
that already refuses `--total-timeout`, `--pr`, `--retry`, `--no-budget-check` and `--attempts`
(`docs/design/desktop-console.md:211,220`), so **the console passes none of the new flags** and no console
task is in this plan (D915). The coordinator, when it launches, is a CLI caller and may pass them.

**`fugaro runs show RUN [--json]` is new.** `fugaro runs` has `ls`, `stats` and `digest` and no `show`
(`internal/cli/runs.go:107`), and `diagnose` is the run's failure view rather than its budget view. The new
subcommand prints, for one run: the chain it belongs to, each dimension's limit, used and remaining, the
source of each limit, and, when the run is halted, the halt. `diagnose` gains the same block for a halted
run, because a halted run's first question is "how much is left" (D915).

**The JSON contract** (D916). Additions only, within the existing versions:

| Document | Member added | Read by |
| --- | --- | --- |
| `result.json` (`schemas/result.schema.json`) | `tokens` (input/output/cache totals) | the chain read, `runs show`, `diagnose`, the console's run detail |
| `task.json` (`schemas/task.schema.json`) | `overrides.max_usd`, `.max_tokens`, `.max_time`, `.max_review_rounds` | the runner at bootstrap |
| `runs show --json` (new document) | `chain`, `limits[]` (`dimension`, `limit`, `used`, `remaining`, `source`) | the console, the coordinator, an orchestrator |
| `fugaro run --json`'s reply (`launchResult`) | `chain` (the root run id) and `limits[]`, the same shape | the console's launch reply decoder, which ignores keys it does not know |

No member is renamed, retyped or removed, and no version is bumped: every one of these is an addition to a
document whose version is 1 (`internal/task/task.go:880`, `schemas/result.schema.json`).

## 8. Rigor modes and the cloud senior reviewer

**The rigor modes' loop defaults are defaults, not caps** (`internal/rigor/rounds.go:33`: POC 1, Startup
2, Pro 3, Critical the widest, Hotfix 1, held under `minRoundCeiling` 2 by the owner's O3 ruling of
2026-10-27). `--max-review-rounds` therefore sits **under** the mode's ceiling, never above it: the mode's
number is a ceiling on what the *recipe* asked for, and a per-launch limit is a ceiling on what the
*chain* may spend, so the run's rounds are `min(recipe rounds, mode ceiling, chain remaining)` (D917). A
`--max-review-rounds` above the mode's ceiling is refused with the mode's number named, because a caller
that could exceed it would be buying rounds the mode exists to deny. A `--mode` raise and a
`--max-review-rounds` together are accepted and compose in that order.

**The cloud senior review** (`docs/cloud-senior-review/`, open PR #1472, holding D880–D899) adds a second
job — an independent reviewer on a named account — whose verdict a coordinator may act on. It is not on
`main`, so nothing here depends on it. When it ships, its job is a **separate chain** with its own limits
unless the owner rules otherwise: it is not a continuation of the coding run (D918). The one thing this
design fixes for it now is the shape: a review job that must not spend past a chain's remaining passes
`--continues <the coding run>` and gets the same carry-over every other caller gets, with no change to
this design.

## 9. The threat model

The trust boundary is `docs/design/v1.md` §6.1: **the run is the untrusted party**, and a launcher is a
person the owner has already allowed to spend. This work moves the boundary in exactly one place — it lets
a launcher ask for limits — so the threats are about what a *caller* can claim, and about what the *chain*
can hide.

| # | Threat | Why it fails, or what this design adds |
| --- | --- | --- |
| T1 | **A caller understates what was used** so the restart gets a fresh budget | `used` is read from the run records by the engine (§4). A caller's `--used-*` is accepted only when it *raises* the charge (D908). A caller that states a smaller figure is refused, naming both |
| T2 | **A caller forges a run record** with a smaller cost or a later `started_at` | The records live in the project's runs bucket, which a launcher may write only its own run's prefix under, and which the reader lists by the chain's own link (§3). A record the reader cannot place in the chain is not counted, and a chain with an uncounted record refuses (T3). Forging a *sibling's* record needs the write grant the bucket's rules do not give a launcher |
| T3 | **A chain split dodges the per-run cap** — a task restarted as a "new" chain that shares no link | The chain is derived from the link, and the link is what the launcher must name: a `--continues` the records cannot confirm (no such run, or a run not in this repository) is refused. A launcher that names *no* link gets a fresh chain, which is the honest case the per-run cap already bounds — and the **daily and project caps still bind**, because the budget database's counters are per (repository, day) and per (project, day) regardless of any chain (`internal/budget/model.go:208-266`) |
| T4 | **Clock skew** makes `finished_at − started_at` smaller than the real elapsed time | The runner already reads the database's clock rather than a machine's (`internal/budget/session.go:276`), and `started_at` is the run's own recorded bootstrap time. A clock that reads *early* only makes the chain's time look shorter by the skew, which is bounded by the platform's clock sync; a clock that reads late charges the chain more. The design takes the **larger** of the record's own elapsed time and the sum of its stages' `duration_s`, so a doctored `started_at` cannot shrink the figure (D920) |
| T5 | **A lease is never released**, so the chain reads less than it spent | `remaining` reads **settled** dollars (`cost.model_usd`), not the lease (`budget.granted_micros`), so an unreleased lease does not understate the chain. The budget database's own counters are the conservative side and are the sweeper's business, which never releases and never errs low (`internal/budget/sweep.go:28-29`) |
| T6 | **The gateway refuses a call the chain thought it could afford** | The gateway is still the judge. `remaining` is merged as the run's cap (§4), so a call that does not fit is refused with `run_cap` and halts the run (`internal/gateway/gateway.go:195`). The chain treats that halt as any other: the usage is what the record settled (§5) |
| T7 | **A caller passes a limit above the policy** and the run spends more than the owner allowed | The merge is tighten-only and the launcher refuses a loosening value rather than dropping it (§2, D903). The runner merges the same layer again at bootstrap, so a launch that somehow wrote a looser value is still held to the policy |

**Out of scope, stated so a reader does not read them as covered:** the run records are not signed, so a
party that can already write the runs bucket can lie to the chain read (T2 is about the grants, not about
cryptography); the daily caps are enforced by the budget database's rules and not by this work; and an
orchestrator that launches with `--no-budget-check` still skips the launch pre-check, which this design
does not change (the run is held to every limit anyway).

## 10. What this design does not do

- It adds **no** new ceiling, cap or default of its own: every number is one the owner, the team or the
  branch already set.
- It does not touch the gateway's ledger, the lease, the sweeper or the budget rules (D919).
- It does not make the console pass a limit; the console's never-table keeps `--total-timeout` and gains
  nothing (D915).
- It does not change `--total-timeout`'s meaning, and it does not deprecate it.
- It does not decide anything about `ee/`: the coordinator's intent record is unchanged (D914).
