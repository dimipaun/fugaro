# Per-launch limits and a chain budget: the spec

Written to the **owner's answers** (D1041–D1061, [reconciliation.md](reconciliation.md) §3 — every one
answered 2026-11-01 as recommended). Repository names are placeholders (`owner/repo-a`, `owner/repo-b`); so
are run ids and account names.

## 1. The rule this serves

A run's ceilings are set before the launch, from the owner's ceiling, the default branch's `fugaro.yaml`
and the run's own `fugaro.yaml`, merged **tighten-only** (`internal/runner/policy.go:65`,
`internal/policy/merge.go:44`). The engine is the judge of every number; a caller only ever asks.

Two consequences the brief names follow from that, and this design keeps both:

- **A per-launch limit tightens and never loosens.** A launcher — a person at a terminal, the console, an
  orchestrator — may ask for *less* than the policy allows and for nothing more. The engine resolves the
  effective policy at bootstrap and merges the launch's request under it (D1044).
- **The engine reads what was already spent.** A restart's remaining budget is `limit − used`, where
  `used` is read by the engine — by the **launcher** from the chain's ledgers, and by the **runner** for its
  own run (§3, §4) — and never supplied by the caller (D1047, D1048). A caller that could state its own usage
  could state zero.

## 2. Explicit per-launch limits

Four dimensions, one flag each (D1042), all optional, all tighten-only:

| Flag | Dimension | What it bounds |
| --- | --- | --- |
| `--max-usd X` | dollars | the chain's model spend, in USD |
| `--max-tokens N` | tokens | the chain's token total |
| `--max-time D` | wall time | the chain's elapsed run time, in Go duration form |
| `--max-review-rounds N` | review rounds | the chain's review rounds, senior-review iterations included |

They are carried in `task.json`'s `overrides` block, beside the `total_timeout` that is there today
(`internal/task/task.go:753`), so they travel with the task the job reads and are visible in the record
(D1041).

**A chain limit exists only when one is set.** A launch that names none gets **no chain**: today's per-run
caps apply per run, exactly as they do now, and nothing is accumulated. A chain begins at the **root**
launch that sets one, and only that launch's task carries it. This is the point of the owner's second
answer — the feature is opt-in, so no existing workflow changes behaviour on the day it ships.

**A limit that would loosen is refused, not dropped.** The repository layer's looser values are dropped and
recorded in the policy record's `ignored` list (`internal/runstore/runstore.go:865`), which is right for a
file a team wrote once. It is wrong for a number a caller typed at a terminal one second ago: a silent drop
tells the caller their limit took effect when it did not. So the launcher resolves the effective policy
*before* the launch, and a `--max-*` above the effective limit is a `userErr` naming both numbers and the
layer that set the lower one (D1043).

`--total-timeout` **keeps its present meaning** (D1043): it is a timeout, it may raise the run's own clock
up to `OverrideCap` (`internal/cli/run.go:2350`), and it is not one of the four. `--max-time` is the new,
tighten-only one. A launch that gives both is accepted; the run's clock is `min(--total-timeout,
--max-time remaining)`, and the report says which one bound it.

**The merge needs a layer member and a name, not just a call.** `policy.Layer`
(`internal/policy/policy.go:423`) has **no wall-time member and no review-rounds member** today, and
`layerName` (`internal/policy/merge.go:27-35`) calls every layer past the first two `branch`. So this work
adds the two members and gives the launch/chain layer **its own source name**, or `runs show` would print a
chain limit whose source reads `branch` — a place the number did not come from (F4, D1055).

**A per-launch limit that cannot reach the job is not written.** `task.Parse` decodes with
`DisallowUnknownFields` (`internal/task/task.go:863`), so a job image whose runner predates these fields
refuses the whole task. The launcher therefore gates a launch that writes any of them behind
`checkImageSince` (`internal/cli/recipes_skew.go:700`) with this work's own `*Since` constant, exactly as
the project layer, the named tokens, the title, the plan task, the rigor request and the governance block
are gated today (`internal/cli/run.go:727-801`). A refused launch names `fugaro image refresh`, and that
command stays the operator's, in their own terminal — never a step `fugaro update` hides (D1041).

## 3. What identifies a chain

A **chain** is the set of runs that share one unit of work and one budget (D1045). Three links exist today,
and this design uses them rather than inventing a fourth:

1. **The pull request.** A follow-up (`--pr N`) already records `follow_up.pr` and `follow_up.previous_run`
   (`internal/runstore/runstore.go:922`), and the launcher resolves the PR's runs out of the runs bucket
   (`docs/design/v1.md` §4.4). A chain is the PR's runs.
2. **The task.** A `--retry` relaunches a stored task as it is (`internal/cli/run.go:327`); the task's
   `run_id` and the batch label are the chain's identity when there is no PR yet.
3. **`--continues RUN-ID`.** For a chain with no PR — a halted run that opened nothing (`outcome: none`,
   `docs/design/v1.md` §4.5) — the launcher names the run it continues explicitly.

**`--continues` is refused with `--pr`, `--retry` and `--attempts`.** Those three are three different ways
to name a chain's previous run, and a launch that names two of them has not said which chain it is in. The
refusal is the shape `--attempts` already uses for its three (`internal/cli/run.go:284`, `:300-308`).

The chain id is the **root run id** of the link: the first run of the PR's branch (`fugaro/<run-id>`, the
branch name the root run's id is read from), the retried task's `run_id`, or the run `--continues` names.
It is derived by the engine from the records, never taken from the caller as a string to trust.

**The launcher reads the chain; the runner reads its own run.** The job's own service account is bound to
`runs/<slug>/`, `cache/<slug>/` and `locks/<slug>/`, and that condition names objects, so it **also denies
listing the bucket** — the design says so in as many words: *"The runner never lists"*
(`docs/design/v1.md` §6.1). Two facts follow from the rules and the IAM, and they decide the read:

- `/runs/<slug>/<run>` is readable on `RUN` alone (`internal/budget/rules/rules.json.tmpl:56-57`), and `RUN`
  requires `auth.token.fs == $slug && auth.token.fr == $run` (`internal/budget/rules/rules.go:580`), so **a
  run's token reads its own ledger and no sibling's**.
- A launcher and an operator already hold `roles/firebasedatabase.viewer`
  (`deploy/terraform/gcp/modules/firebase/iam.tf:44-50`), which reads **every** path.

So the **launcher** resolves the chain's run ids and reads each one's ledger with its viewer role; the
**runner** reads each chain **record by name** (the ids the launcher resolved, under `runs/<slug>/`) and
re-reads **only its own** ledger (F21; plan L0, L1, L5). A chain read is a list of names, not a listing, and
it is not a read the job can perform for a run that is not itself.

## 4. Where "used" comes from, and where the chain is recorded

**The budget database's per-run ledger is the source of "used"** (D1046, F20). With the budget on, every
run's spend is already in `/runs/<slug>/<run>` as `RunLedger{Reserved, Released, Spent, Notional, Overrun,
Tokens, Exp, Crashed, ByTier}` (`internal/budget/model.go:332-347`), and the rules keep every leaf of it
**increase-only** (`internal/budget/rules/rules.json.tmpl:56-91`: each numeric leaf validates `NEW >= OLD`,
`exp` is write-once, and `released + spent <= reserved` holds beside `spent + released <= reserved`).
"Used" for one run of the chain is the amount that run still **holds**:

```
used(run) = Outstanding() = Reserved − Released        (internal/budget/model.go:351)
```

and it is that figure **always**, with no second one for a ledger that settled. A run that settled normally
released everything it did not spend (`released = reserved − spent`), so `Outstanding()` *is* its `Spent`
there; a run that did not — the container died between the halt and the release, the sweeper has not reached
it, the ledger is `Crashed` — holds more than it spent, which is the direction the sweeper takes: *"an
outstanding amount stays counted in the day's counters (errs high, never low)"*
(`internal/budget/sweep.go:28-29`). One formula covers both cases, and it is why this design never has to
decide whether a ledger is **closed**: `RunLedger` carries no leaf that says so, and the earlier draft's
"closed or unclosed" branch named a state the code does not have (F20).

**The rules bind every identity that carries a run's or a user's token, and no other.** Every leaf is
increase-only and a release is capped at `reserved − spent`, so `Reserved − Released >= Spent` in every state
those rules allow: for a run token, or a token a launcher minted for one of its runs, the settled figure is a
floor no write moves down (F20, T3). **`roles/firebasedatabase.admin` bypasses the rules entirely** — that is
IAM, not a rule — and it is held by budget admins
(`deploy/terraform/gcp/modules/firebase/iam.tf:53-59`), by the history account (`:81-85`) and by the
coordinator's writer (`:138-143`), each of which can write a ledger down if it chooses. The ledger is
therefore the best source the installation has, not an oracle: what this design relies on is that no **run**
and no **launcher** can lower the figure (T3), and that is the only claim it makes.

**Where the chain is recorded, and what that costs.** A chain's **membership** and its **limit** live in the
**run records**: the limit in the root launch's `task.json` `overrides` (§2), the link in the record's
`follow_up.pr` and `follow_up.previous_run` or in the stored task's `run_id` (§3; F7, F21). This work adds
**no node and no rule** to the budget database (D1046): the database is read for numbers it already keeps,
and nothing about a chain is written there — so nothing about a chain is *enforced* there either. The design
states that rather than implying otherwise:

- **A launcher — or the agent that shares its container — can drop a predecessor from the chain, or rewrite
  the limit up.** It **can** write and delete records under `runs/`: its grant is `objectUser` on the `runs/`
  and `tokens/` prefixes, not on one run's
  (`deploy/terraform/gcp/modules/installation/iam.tf:172-187`, F21), and the chain read sees exactly the
  records it is given (T2, T4).
- **Only the per-run cap and the daily and project caps are hard bounds**, because those are the database's
  own rules. A chain limit bounds an **honest orchestrator's** runaway spend, which is the failure it exists
  to close; it is not a defence against a hostile launcher, and this design does not sell it as one (T4, T5).
- **The chain's time and review-round dimensions are advisory.** They are read off the records (the table
  below), no rule anywhere enforces them — chain-scoped or per run — and `runs show` prints them as advisory
  beside the money.

`remaining` is computed in one place, `runstore`-side, and read by two callers:

```
remaining(dimension) = chain_limit(dimension) − Σ over the chain's runs of used(run, dimension)
```

| Dimension | The figure | Where it comes from |
| --- | --- | --- |
| dollars | `Outstanding()` = `Reserved − Released` | the ledger's leaves, as above |
| tokens | `Tokens` | the ledger's `tokens` leaf (`internal/budget/model.go:338`) — **no new node, no rules change, no new field** (D1051, F8) |
| time | `max(elapsed, Σ stages[].duration_s)` | the **run records** — `finished_at − started_at`, or `now − started_at` while it runs (`internal/runview/runview.go:74`); each stage's `duration_s` is in the record (`internal/runstore/runstore.go:531`) (D1060) |
| review rounds | the count of the chain's `reviews[]` entries | the **run records** — the runner already writes `round` (`internal/runstore/runstore.go:146`) |

The two record-backed dimensions are record-backed on purpose: the ledger keeps money and tokens, and a
clock and a round count are not money. Neither is smaller than the work done — time takes the **larger** of
two readings (D1060) and a round count is a count of entries the runner wrote — but neither has a rule behind
it either, which is why §4's third bullet calls both **advisory**.

**The run records are the fallback, and the fallback is UNENFORCED.** With the budget **off** there is no
ledger at all, so a chain limit can only be read off the run records. Two things follow, and the design
states both rather than glossing them:

- The dollar figure is the **record's** figure, and a launcher can write and delete run records (F21), so it
  is not a number the rules enforce.
- The **token** dimension has **no** figure there: `result.json` carries only `cache_read_tokens` and
  `cache_write_tokens` (`schemas/result.schema.json:97-98`), gateway-only cache sums that exclude the input
  and output tokens a cap counts, and the runner's own total is in memory (`r.tokens`,
  `internal/runner/halt.go:139`) and never written. So a budget-off chain **does not measure tokens** and
  says so; it does not approximate one from the cache sums (D1051, F8).

A chain limit in the budget-off mode is therefore a **report and not a stop**, marked **UNENFORCED** in
`runs show` and in the launch reply. An operator who needs a stop turns the budget on; that is the mode's
existing meaning and this design does not change it.

**A ledger that cannot be read is charged at that run's full per-run cap** (D1049). "Unreadable" is the read
failing — the node absent, the call erroring — and it is not the same as a ledger that reads zero: a run whose
ledger cannot be read is charged the **largest** amount it could have been holding, that run's own per-run cap
(the effective `PerRunUSD` and its token twin), so a chain never reads an unreadable predecessor as cheaper
than that run may have been. There is no separate "unknown" state carried into `remaining` and no launch that
has to be refused for it: the charge is a number, it favours the cap, and a caller may only raise it
(`--used-*`, below). It is the sweeper's own direction applied to a read that failed rather than to a lease
that was never released.

**The caller never supplies `used`** (D1048). A caller-supplied usage number is accepted only as a *further
tightening*: a `--used-usd X` that is **larger** than what the engine read raises the charge, and one that is
smaller is refused, naming both figures. That keeps the one honest use of the knob — an orchestrator that
knows about spend the chain's own ledgers do not, such as a sibling chain — without giving a caller a way to
understate.

**The engine applies `remaining`, not `limit`, to the run.** The **launcher** computes the chain's `remaining`
before the launch — it is the identity that can read the chain's ledgers (§3) — and it rides the launch's
`task.json`; the **runner** merges it at bootstrap as the last tighten-only layer over the four dimensions
(§2), re-reading its **own** ledger and the chain's records and merging only downward: a figure it cannot read
is the launch layer's, never a larger one. So the gateway's per-run cap for the restart *is* `remaining`, and
the token cap and the clock follow the same figure. Nothing in the gateway changes: it still enforces one cap,
it is simply handed a smaller one (D1059).

**A chain that has spent its limit is raised by rewriting the root launch's task, with its reason recorded**
(D1061, F22). No new command, no new node and no operator-only write path: a chain limit lives in the root
run's `task.json` `overrides` (§2), so raising it is an edit of that stored task — the root run's record under
`runs/$slug/` — carrying the reason for the raise, which the record keeps the way its policy record already
keeps the reason a looser value was dropped (`policy.ignored`, `internal/runstore/runstore.go:865`). **Said
plainly, because the alternative is a claim this design cannot support: a launcher can do the same.** It holds
`objectUser` on the `runs/` prefix (F21) and can rewrite that task exactly as an operator can, so the rule is
a **convention the installation states**, not a bound the platform enforces — an honest launcher raises an
exhausted chain by that path and records why, and a hostile one never needed permission. What is hard stays
unchanged: the per-run cap and the daily and project caps are the database's own rules (§4's second bullet),
and a raise moves none of them.

## 5. The halted-during-writeback case

A halted run still finalizes and writes back: `finalize` makes no model call, and `writeback`
(`internal/runner/lockcache.go:315`) sets `stage: writeback` and saves the record before the session and the
caches (F17). The writeback does not touch the ledger. Three cases, and the design settles each (D1050):

1. **The ledger reads.** The chain counts that run's `Outstanding()`, as it counts every other run's, and the
   restart's `remaining` is small or zero; a zero remaining is a **refusal at launch**, not a run that starts
   and halts at once.
2. **The ledger cannot be read** — the container died between the halt and the release and the node is gone,
   or the read itself fails. The usage is **never zero** (D1049): the run is charged its **full per-run cap**,
   the most it could have been holding (§4), and a caller's `--used-*` may only raise that charge.
3. **The halt was the cap itself, mid-call.** This is where the "$5 held, $0.25 needed" message comes from —
   and that message is **not about spend**. It is the per-run cap compared against the lease's **outstanding**
   amount: `run cap %s reached (%s held, %s needed)`, where "held" is `Reserved − Released`
   (`internal/budget/lease.go:211`, the guard that builds it at `:210-216`, mirroring
   `internal/budget/evaluate.go:192`). A restart therefore charges exactly the number that message names,
   because `Outstanding()` **is** the held amount (§4) — the money the day counters are still counting. It is
   not a special rule: case 3 is case 1 or case 2, and §4's one formula settles it either way.

A halted run that opened **no** PR (`outcome: none`) has no PR link, so its continuation is `--continues
RUN-ID` (§3), and the launcher says so when `--pr` finds nothing.

## 6. Interaction with the launch paths that exist

- **`--pr` follow-ups.** The follow-up already inherits `overrides.total_timeout` from the PR's runs and
  nothing else (`docs/design/v1.md` §4.4). It now also inherits the chain's *remaining* limits, because the
  chain is the PR (§3). A `--max-*` given on the follow-up is merged under the inherited remaining,
  tighten-only, so a follow-up cannot re-open what the chain spent (D1052). A follow-up that names no
  `--max-*` gets exactly the remaining amounts, which is the behaviour the owner asked for: the restart does
  not start from zero.
- **`--retry`.** A retry relaunches the stored task as it is (`internal/cli/run.go:327`) and takes no flags,
  so it cannot carry new limits; it inherits the stored task's, and the chain is the stored `run_id`. Because
  the stored task is written once, a retry's `remaining` is computed at *its* launch from the chain's
  ledgers, exactly as a follow-up's is (D1053).
- **`--attempts` (best-of-N).** A batch launches N independent runs of one task and refuses `--pr`,
  `--run-id` and `--retry` (`internal/cli/run.go:300-308`, the check `--attempts` makes of its three
  neighbours). The batch is **N chains, not one** (D1053): the
  attempts are alternatives, not continuations, so each gets the full per-launch limit, and the *batch* is
  bounded by the existing `batchBudgetPrecheck`, which leases `N × budget.MinLaunchHeadroom` once before any
  attempt (`internal/cli/launch_budget.go:316`, `internal/budget/precheck.go:12`). A per-launch limit on a
  batch applies to every attempt; the batch's total bound is the budget database's, unchanged.
- **The central orchestrator and the coordinator.** `docs/central-orchestrator/` is on `main` as a plan only,
  and the coordinator's launch path is the intent writer, which `launchRun` already calls so a launch
  declares itself without a second command (`ee/coordinator/intent.go:20`). The coordinator is advisory: it
  takes nothing back, verifies nothing and authorises nothing by a declared string
  (`ee/coordinator/intent.go:34`). So the limits ride the **launch**, not the intent: an orchestrator passes
  `--max-*` and `--continues` as ordinary launch arguments, and the intent record stays a declaration
  (D1054). Nothing in `ee/coordinator/` changes.
- **Raising an exhausted chain.** Not a launch path at all (D1061, §4): the chain's own record carries the
  limit, so the raise is an edit of the root run's stored task with its reason recorded, and the next launch
  reads the new number. No `fugaro run` flag reaches it.

## 7. The CLI and JSON surface

**Flags** (§2) on `fugaro run`. The console's spawn contract is one exact argv with a closed never-table
that already refuses `--total-timeout`, `--pr`, `--retry`, `--no-budget-check` and `--attempts`
(`docs/design/desktop-console.md:211,220`), so **the console passes none of the new flags** and no console
task is in this plan (D1055). The coordinator, when it launches, is a CLI caller and may pass them.

**`fugaro runs show RUN [--json]` is new.** `fugaro runs` has `ls`, `stats` and `digest` and no `show`
(`internal/cli/runs.go:107`), and `diagnose` is the run's failure view rather than its budget view. The new
subcommand prints, for one run: the chain it belongs to, each dimension's limit, used and remaining, the
**source** of each limit — from the policy layer's own name, which is why §2 needs one (F4) — whether the
chain is **ENFORCED or UNENFORCED** (§4), and, when the run is halted, the halt. `diagnose` gains the same
block for a halted run, because a halted run's first question is "how much is left" (D1055).

**The JSON contract** (D1056). Additions only, within the existing versions:

| Document | Member added | Read by |
| --- | --- | --- |
| `task.json` (`schemas/task.schema.json`) | `overrides.max_usd`, `.max_tokens`, `.max_time`, `.max_review_rounds` | the runner at bootstrap |
| `runs show --json` (new document) | `chain`, `enforced`, `limits[]` (`dimension`, `limit`, `used`, `remaining`, `source`) | the console, the coordinator, an orchestrator |
| `fugaro run --json`'s reply (`launchResult`) | `chain` (the root run id), `enforced` and `limits[]`, the same shape | the console's launch reply decoder, which ignores keys it does not know |

**`result.json` does not change at all** (D1051): the ledger carries the token total, so nothing is added to
a run's record for this work. No member is renamed, retyped or removed, and no version is bumped
(`internal/task/task.go:880`, `schemas/result.schema.json`).

## 8. Rigor modes and the cloud senior reviewer

**The rigor table's rows are not the ceilings a mode runs at** (F15). `RoundCeiling`
(`internal/rigor/rounds.go:51-56`) is `min(max(n, minRoundCeiling), widestRoundCeiling)` with `minRoundCeiling`
**2** and `widestRoundCeiling` 10, and the table's rows (`:31-38`) are POC 1, Startup 2, Pro 3, Critical the
widest, Hotfix 1: **two of the five (POC's 1 and Hotfix's 1) sit below the floor and are lifted to 2; Pro's 3
and Critical's 10 sit above it and are untouched.** An earlier draft said "every row is at or below the floor",
which is false, and the claim that matters is the narrower one: **the ceiling a mode spends is what
`RoundCeiling` returns** — today **POC 2, Startup 2, Pro 3, Critical 10, Hotfix 2** — quoted as an example of
that function's output today, never as a table of its own.

**The owner's per-level loop defaults** (the rigor modes' open question **O3**, answered 2026-11-01): **poc
1/1, startup 2/2, pro 3/3, critical 3/5, hotfix 1/1** — **defaults only**, recorded as the owner gave them;
the rigor work that implements O3 names the pair. What this design takes from the answer is the rule rather
than the numbers: **per run, the rounds are the level's default unless the recipe sets them**, capped by
`RoundCeiling`'s output and by the chain's remaining.

`--max-review-rounds` therefore **refuses only against the chain's own limit, never against the mode's**
(D1057): the mode's number is already the engine's clamp, and a per-launch limit that refused against it
would be a second copy of the mode that could also drift from it. Per run, the rounds are

```
rounds(run) = min(the recipe's rounds when it sets them, else the level's default,
                  RoundCeiling(level)'s output,
                  the chain's remaining)
```

and the engine's own refusal — the one that exists today — names **`RoundCeiling`'s output** (today POC 2,
Startup 2, Pro 3, Critical 10, Hotfix 2), never the table's row. A `--mode` raise and a
`--max-review-rounds` together are accepted and compose in that order.

**A review job joins the PR's chain** (D1058). The cloud senior review (`docs/cloud-senior-review/`, open PR
#1472) adds a second job — an independent reviewer on a named account — whose verdict a coordinator may act
on. A review job launched on PR N joins **the PR's chain** by its PR link (§3) rather than opening a chain of
its own: it is reviewing that PR's work, on that PR's budget, and the chain is exactly the set of runs that
share one unit of work. And the chain's **`--max-review-rounds` counts senior-review iterations** — each
verdict that sends the coding run back for another fix is a review round of that chain, which is what bounds
that design's review-fix loop. **#1472 lists exactly that cap among the things it does not do** — "a cap on
the fix↔review loop" is one of its own open items (its finding **F27**) — so this design is where the cap
lands, and it asks one thing of that design: **the review job must define the record its verdict is written
to**, because a chain counts rounds as the `reviews[]` entries the runner writes
(`internal/runstore/runstore.go:146`), and an iteration that lands nowhere the chain can read is an iteration
the cap never sees. That design is not
on `main`, so nothing here depends on it; what this design fixes now is the shape it will land in.

## 9. The threat model

The trust boundary is the container, not the process (`docs/design/v1.md` §6.1): **the agent shares the
container with the runner and reaches everything the job's service account reaches.** This work moves the
boundary in exactly one place — it lets a launcher ask for limits — so the threats are about what a
*launcher* can claim, what a *record* can lie about, and what the *ledger* cannot.

| # | Threat | Why it fails, or what this design adds |
| --- | --- | --- |
| T1 | **A caller understates what was used** so the restart gets a fresh budget | `used` is read by the engine from the ledger (§4). A caller's `--used-*` is accepted only when it *raises* the charge (D1048). A caller that states a smaller figure is refused, naming both |
| T2 | **The launcher forges a run record** with a smaller cost, deletes one, or drops a predecessor from its chain | It **can**: its grant is `objectUser` on the `runs/` and `tokens/` prefixes, not on one run's (`deploy/terraform/gcp/modules/installation/iam.tf:172-187`, F21), and the chain's membership lives in exactly those records (§4). What that buys it is a chain that reads **cheaper**, and saying so is the honest limit of this design: with the budget on, the *money* figure is the database's, so a forged cost changes no figure the engine reads — but a dropped predecessor is a chain the engine cannot see, and the per-run and daily caps are what stop the run that follows (D1046, T4) |
| T3 | **A launcher writes its own chain's ledger down**, with a token it minted for one of its runs | It cannot, for as long as the rules bind it. Every leaf is increase-only and `released + spent <= reserved` (`internal/budget/rules/rules.json.tmpl:56-91`), so `Reserved − Released >= Spent` in every reachable state and `Outstanding()` never falls below the settled spend (F20). A launcher holds `signJwt` and can mint a token for any `(slug, run)` (`docs/design/v1.md` §6.1, M9b known limits (2)) — and the most that buys it is to *raise* its own chain's charge. **`firebasedatabase.admin` is not bound by the rules at all** — it is IAM — and it is held by budget admins (`deploy/terraform/gcp/modules/firebase/iam.tf:53-59`), by the history account (`:81-85`) and by the coordinator's writer (`:138-143`), none of which is a launcher (`:44-50` gives launchers the **viewer** role) |
| T4 | **A chain split** dodges the limit — a task restarted as a "new" chain that shares no link | The chain is derived from the link, and the link is what the launcher must name: a `--continues` the records cannot confirm (no such run, or a run not in this repository) is refused. A launcher that names *no* link gets a fresh chain — and **a launcher is trusted in this design**, because it can already mint a token for any run and can launch a first run with no limit at all (§2), so a hostile launcher is out of scope here rather than defeated by this. What the link closes is an **honest** orchestrator's runaway restart loop; what stays hard is the per-run cap and the **daily and project caps**, whose counters are per (repository, day) and per (project, day) whatever any chain says (`internal/budget/model.go:249-266`) |
| T5 | **The agent** (which shares the container) understates the chain it is in | It holds no launcher credential and no budget admin, so it can move no hard bound — but the chain's membership and limit are run records, and the job's own account writes records under `runs/<slug>/` (`deploy/terraform/gcp/modules/workflow/sa.tf:22-35`), so a compromised container **can** drop a predecessor from its chain and rewrite a limit written there. That is the residual `docs/design/v1.md` §6.1 already documents, and it is why §4 says in as many words that a chain bounds an **honest** orchestrator: what the container cannot reach is the per-run, daily and project caps |
| T6 | **A run's lease is never released**, or its run crashed, so the chain reads less than it spent | `used` is `Outstanding()` = `Reserved − Released` (§4) — the amount the run still holds — so an unreleased lease charges the chain what was held, never less. The sweeper's own direction — it "never releases anything… errs high, never low" (`internal/budget/sweep.go:28-29`) — is the read side's too |
| T7 | **Clock skew** makes the chain's time look shorter | The design takes the **larger** of the record's elapsed time and the sum of its stages' `duration_s` (D1060). The runner already reads the database's clock rather than a machine's (`internal/budget/session.go:276`), and a clock that reads early shortens the figure only by the platform's own skew |
| T8 | **The gateway refuses a call the chain thought it could afford** | The gateway is still the judge. `remaining` is merged as the run's cap (§4), so a call that does not fit is refused and halts the run: with a lease the message is built at `internal/budget/lease.go:211` (`run cap %s reached (%s held, %s needed)`), and with the budget off it is built at `internal/gateway/ledger.go:57` from `Options.Cap` (`:52`, `run cap %s reached (%s spent, %s needed)`). The chain treats that halt as any other (§5) |
| T9 | **A caller passes a limit above the policy** and the run spends more than the owner allowed | The merge is tighten-only and the launcher refuses a loosening value rather than dropping it (§2, D1043). The runner merges the same layer again at bootstrap, so a launch that somehow wrote a looser value is still held to the policy |
| T10 | **A budget-off chain is read as if it were enforced** | It is not: the fallback is marked **UNENFORCED** in the record, in `runs show` and in the launch reply, and its token dimension is reported as unmeasured rather than approximated (§4, D1051) |

**Out of scope, stated so a reader does not read them as covered:** the run records are not signed, a launcher
can rewrite them (T2) and the agent shares the container that writes its own repository's (T5) — so the
chain's **membership and its limit are a convention, not a bound**: the hard bounds are the per-run cap and
the daily and project caps, which are the database's rules, and with the budget off a chain is a report rather
than a stop, which §4 says rather than hides; the daily caps are enforced by the budget database's rules and
not by this work; an orchestrator that launches with `--no-budget-check` still skips the launch pre-check,
which this design does not change (the run is held to every limit anyway); and the residual in
`docs/design/v1.md` §6.1 — a compromised container can read the gateway token and the real key — is unchanged
by anything here.

## 10. What this design does not do

- It adds **no** new ceiling, cap or default of its own: every number is one the owner, the team or the
  branch already set, and a chain limit exists only when a launch sets one (§2).
- It adds **no node, no rule and no wire field** to the budget database or to `result.json` (D1046, D1051).
- It does not touch the gateway's ledger, the lease, the sweeper or the budget rules (D1059).
- It does not make the console pass a limit; the console's never-table keeps `--total-timeout` and gains
  nothing (D1055).
- It does not change `--total-timeout`'s meaning, and it does not deprecate it.
- It does not decide anything about `ee/`: the coordinator's intent record is unchanged (D1054).
