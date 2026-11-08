<!-- SPDX-License-Identifier: LicenseRef-Fugaro-Subscription -->
<!-- See console/LICENSE.md for the terms. -->
# The JSON contract the console reads

Tasks 0.1 and 0.2 of
[docs/plans/2026-10-15-desktop-console.md](../../docs/plans/2026-10-15-desktop-console.md).
This document is the console's whole data plane written down: every `--json`
document the console consumes, field by field, with the symbol on `main` that
proves each one. The design is
[docs/design/desktop-console.md](../../docs/design/desktop-console.md).

Sections 1 to 9 are Task 0.1's: the shapes, field by field. **§10 is Task
0.2's**: what the one long-lived command in the set — `watch --json` — does
over time, how it ends, and what a process that spawns it must therefore do.
`TestDocsDesktopConsoleContractSettlesTheFeed` in the same Go file pins §10's
facts the way `TestDocsDesktopConsoleContractNamesEveryWatchField` pins §2's
fields.

**The rule this document exists to obey: every field below is read off the
code, never assumed from the design.** A field invented here rather than read
from a struct tag or an `Encode` call is a defect, and that is the review
criterion for this file (the plan's Review Focus, Phase 0). The Go test
`TestDocsDesktopConsoleContractNamesEveryWatchField` in
`internal/cli/docs_desktop_console_test.go` pins the `watch --json` half of the
list to `internal/watch/json.go`'s own struct tags, so a rename there fails a
test here rather than silently breaking a screen.

**The console adds no reader.** Everything below is emitted by a command the
console spawns (design D1, `FugaroSource`). Where a screen would need a field
no command emits, the answer is a CLI PR, not a console reader — §7 lists the
seven gaps the design records, each settled against the code here.

**Compatibility rule** (owner, 2026-10-30). This contract only grows. Adding a
field is compatible, and a console older than the CLI ignores a key it does not
know. Removing a field or changing its type is a format change and bumps the
document's format version. **A field never changes meaning: if its semantics
must change, it is renamed**, so a new meaning always arrives under a new name
that an older reader ignores rather than misreads. The console's decoders rely
on this rule (`console/src/shared/schema.ts`), and it is what lets a console
answer `ok` for a CLI newer than it (`console/src/shared/version.ts`).

**One bounded exception, named in full** (owner, 2026-10-28). Where a release
has been *decided* and its wire keys are known, the console accepts a key
**ahead of the CLI that writes it** — not because a command emits it, but so
that the day a command starts to, the console reads it rather than ignoring it
(before 2026-10-30 its decoders refused every such document whole as
`unknown-key`). A key may be named this way only if:
(i) it is named in its shape's own field table or paragraph in this document;
(ii) it is read as **optional** — absent is `n/a`, never `0` — and drawn by no
screen until the release that writes it; and (iii) it is listed in
`approvedAheadOfTheCLI` in `internal/cli/docs_desktop_console_test.go`, which
is what keeps the guard below honest: every wire name in a field table outside
that list is still refused as a field invented from the design.
`TestDocsDesktopConsoleContractNamesNoInventedField` also fails if a listed key
later becomes a real JSON key of a shape here, so the list cannot outlive its
reason. **The list is empty today**: `leased_usd` (§2) left it when the CLI
started writing the key, so it now stands on the structs like every other field
— a console still reads it as optional, because an older CLI writes no such key.
`compute_usd` (§3) never needed an entry: it was already a real key of
`report --json` (§4), and `budget show --compute` writes it now.

## 1. The five availability states

The design's first UI rule (§6.1 rule 1): **every number carries an
availability state, and a missing value is never an implicit zero.** The
states are:

| State | On screen | What it means |
|---|---|---|
| **measured** | the number | A counter the CLI read. |
| **notional** | the number, after `~$` | A subscription's list price: never billed, never capped (`JSONTotal.NotionalUSD`'s own comment, `internal/watch/json.go`). |
| **estimated** | the number, marked | Compute, an estimate by design (`JSONLine.ComputeEstimated`, `ee/report/render.go`). |
| **unavailable** | `n/a` | The document says the value is not known. A nil pointer, an absent key, `[]`. |
| **gap** | a broken line / hollow bar | The stream stopped. Never interpolated across. |

The columns below say which field needs which state. The recurring shapes:

- **`*float64` / `*int64` / `*string` with no `omitempty`** (the `usdPtr` and
  `jsonLine` conventions): a **null** on the wire means *unavailable*, and a
  present number means *measured*. `daily_cap_usd`, `percent`, `spent_usd`,
  `age_seconds` and `notional_micros` are all this shape.
- **`omitempty` on a number** is how a numeric key can be *absent* instead of
  `null`, and an absent number is never *unavailable*: it means the CLI left the
  key out on purpose, which is a figure the shape has no reason to carry rather
  than a gap. `compute_usd` is the one flag-conditioned key (§3): absent unless
  `budget show --compute` asked for it, and a figure the command cannot give is
  written `null`, never left out. §2's `pr_number` (on `JSONRun` and on
  `JSONFinished`) is the same rule for a run that has recorded no pull request
  — absent, never `0`, which no repository numbering from 1 issues — as is §3's
  `showRun` `round`, an int whose `0` a run in its first round omits. Which
  numbers a shape leaves out is its own rows' business; a **pointer** number
  whose row carries no `omitempty` is always present, `null` when it is not
  known, and a plain number that can be `0` and mean nothing carries its own
  flag instead (`JSONBurn.Known`, below).
- **`omitempty` on a string or a slice**: an **absent key** means *none* or
  *empty*, which is a fact rather than a gap.
- **`bool` flags that say "I don't know"**: `JSONBurn.Known` is the one that
  matters — `burn.known: false` makes `usd_per_min` **unavailable**, never a
  measured `0`.

## 2. `fugaro watch --json` — the live feed

**Command:** `internal/cli/watch.go` `newWatchCmd` / `runWatch` /
`watchStream` / `WatchDeps.emit`.
**Document:** `internal/watch/json.go` `JSONDoc`, built by
`BuildJSON(project string, v View) JSONDoc` in `internal/watch/json.go`.

Emitted **compact, one object per line** (`json.NewEncoder(d.Out).Encode(...)`,
`WatchDeps.emit`, `internal/cli/watch.go`), which is what makes the feed
line-delimited JSON — one of exactly two line-delimited documents the console
reads, the other being `logs RUN --json` (§6.2, §9).

### `JSONDoc` (`internal/watch/json.go`)

| Field | Type | Unit | omitempty | Availability state |
|---|---|---|---|---|
| `project` | string | — | no | measured (`d.LC.Name`) |
| `mode` | string | — | no | **`observe` or `enforce`, never `""`** — `watch.Build` seeds `budget.ModeObserve` and switches only on `budget.ModeEnforce` (`watch.Build`, `internal/watch/build.go`), so an unset `/config/mode` reads `observe` here. `budget show --json` (§3) is the one document that can carry `""` |
| `day` | string | `YYYY-MM-DD`, UTC | no | measured — `View.Day`; `""` before the first day is set (`watch.Build`, `internal/watch/build.go`) |
| `now` | string | RFC 3339, UTC | no | measured |
| `connection` | `JSONConn` | — | no | see below |
| `total` | `JSONTotal` | — | no | see below |
| `repos` | `[]JSONRepo` | — | no | `[]` when none (BuildJSON seeds `[]JSONRepo{}`) — **empty, not unavailable** |
| `runs` | `[]JSONRun` | — | no | `[]` when none |
| `finished` | `[]JSONFinished` | — | no | `[]` when none; **always unfiltered** (`--all`/`--keep`/`--keep-count` bound only the plain and interactive views) |
| `ready` | `[]JSONFinished` | — | no | `[]` when none; the finished runs whose `outcome` is `ready` (`ReadyRowsOf`), unfiltered |
| `queued_note` | string | — | **yes** | absent when the runs bucket was read; present = the bucket could not be read for queued rows, already sanitized (`JSONDoc.QueuedNote`) |

**`mode` per command**, because the two documents that carry it differ on
exactly one input — an unset `/config/mode`. Both spell the code's own values
(`budget.ModeObserve`, `budget.ModeEnforce`):

| Command | Values `mode` can carry |
|---|---|
| `watch --json` | `observe`, `enforce` — `Build` seeds `observe` and switches only on `enforce`, so an unset `/config/mode` reads `observe` here (`watch.Build`) |
| `budget show --json` | `observe`, `enforce`, `""` — an unset (or unrecognised) `/config/mode` reads `""` and appends a warning (`runBudgetShow`, `internal/cli/budget.go`) |

A console that treated `watch --json`'s `mode` as possibly `""` would carry a
comparison the feed never needs; a console that treated `budget show`'s as
never `""` would miss the warning.

### `JSONConn` (`internal/watch/json.go`) — the connectivity state

| Field | Type | Unit | omitempty | Availability state |
|---|---|---|---|---|
| `state` | string | — | no | `live`, `stale`, `offline`, `refused` or `polling` (`connNames`; the constants are `ConnLive`… in `internal/watch`) |
| `reason` | string | — | **yes** | why, when there is one |
| `age_seconds` | float64 | seconds | no | measured |

`polling` is the no-Firebase mode's own indicator (design §6.3); `refused` is a
permission refusal, which `watchStream` emits once and then returns `dbErr`
(`internal/cli/watch.go`).

### `JSONTotal` (`internal/watch/json.go`)

| Field | Type | Unit | omitempty | Availability state |
|---|---|---|---|---|
| `counted_usd` | float64 | USD | no | measured |
| `spent_usd` | float64 | USD | no | measured |
| `notional_usd` | float64 | USD | no | **notional** — never billed, never capped, never summed into a real total |
| `leased_usd` | float64 | USD | no | measured — **`BuildJSON` writes it**, so it is always present and `0` when nothing is leased (the console still reads it as optional, for a CLI older than the release that added it). The **outstanding** leased dollars: `counted_usd − spent_usd`, floored at zero (`watch.LeasedUSD`, `internal/watch/json.go`), where `max_reserve_usd` (§3) is the **ceiling** on one lease. The label is *leased, not yet spent*, never "live leases": the sweeper never releases a lease (`internal/budget/sweep.go`), so a crashed run's stays in the figure for the rest of the day |
| `daily_cap_usd` | `*float64` | USD | no | **null = unavailable** (no cap set) |
| `percent` | `*float64` | 0–100 | no | **null = unavailable**, together with `daily_cap_usd` (`usdPtr`) |
| `max_parallel` | `*int` | count | no | **null = unknown** — the concurrent-run cap a launch is checked against (`checkMaxParallel`, `internal/cli/run.go`): the selected config's `max_parallel` as the config layer resolved it, which no budget node holds, so `internal/cli/watch.go` (`maxParallelOf`) sets it rather than `BuildJSON`. The resolution is why a config file that names none still reads a number: `localcfg` gives it the engine's default **20**, and 20 is then what the launcher compares against, so the document reports the resolved cap rather than guessing one. Null is the shape for a writer with no config to read — never `0`, which a cap bar would draw as a limit of none. **No view reads it yet**: the console's decoder declares the key — `'max_parallel'` in `TOTAL_KEYS` (`console/src/shared/schema.ts`), pinned against this struct by `console/test/unit/schema.test.ts` — so a document carrying it decodes, and the scrub tool replaces it with an invented cap where `shared/digest` accepts it and produces nothing from it. The bar it is for is [the review-levels plan's V1.2](../../docs/review-levels-ab-jobmax/implementation-plan.md), the Live screen's concurrent-runs cap bar, which has not landed; what that bar must draw for an unknown value is `n/a`, never a full bar |
| `burn` | `JSONBurn` | — | no | see below |
| `kill` | `*JSONKill` | — | no | **null = not killed** |
| `runs` | int | count | no | measured |
| `run_hours` | float64 | hours | no | measured |

### `JSONBurn` (`internal/watch/json.go`)

| Field | Type | Unit | omitempty | Availability state |
|---|---|---|---|---|
| `known` | bool | — | no | **the availability state of `usd_per_min`** |
| `usd_per_min` | float64 | USD/min | no | measured when `known`, **unavailable when not** — read `known` first |
| `fast` | bool | — | no | the fast-burn alert (`Burn.Fast`) |

The window is `watch.BurnWindow` (**5 minutes**, `internal/watch/state.go`),
the design's §6.2 screen-1 rule; Task 2.1 asserts the console's constant cannot
drift from it.

### `JSONKill` (`internal/watch/json.go`)

| Field | Type | Unit | omitempty | Availability state |
|---|---|---|---|---|
| `by` | string | — | **yes** | who, when known |
| `at` | string | RFC 3339 | **yes** | when, when known (`jsonKill`) |
| `reason` | string | — | **yes** | why |

### `JSONRepo` (`internal/watch/json.go`)

| Field | Type | Unit | omitempty | Availability state |
|---|---|---|---|---|
| `slug` | string | — | no | the wire key, `safetext.Strip`ped |
| `repo` | string | `owner/name` | no | measured |
| `counted_usd` / `spent_usd` | float64 | USD | no | measured |
| `notional_usd` | float64 | USD | no | **notional** |
| `leased_usd` | float64 | USD | no | measured — as on `JSONTotal`, written by `BuildJSON` and read as optional for an older CLI. The **outstanding** leased dollars of this repository, never the ceiling; the same *leased, not yet spent* caveat, and the same floor at zero |
| `daily_cap_usd` / `percent` | `*float64` | USD / 0–100 | no | **null = unavailable** |
| `burn` | `JSONBurn` | — | no | as above |
| `kill` | `*JSONKill` | — | no | **null = not killed** |

**Naming, because two lease-flavoured money figures now meet here.** The
CLI's ledger says `Reserved` (`budget.RunLedger`), and §3 already carries
`max_reserve_usd`, which the Spend panel draws as "largest single lease (max
reserve)". `leased_usd` is the **outstanding** side of that pair — the amount a
lease currently holds — and `max_reserve_usd` is the **ceiling** on any one
lease. Neither is the other, and neither is a cap: a cap is `daily_cap_usd`,
and §3's `global`/`showRepo` carry the rest.

**How the console reads it — one rule, stated once.** Absent and `null` are
**unavailable** (`n/a`, never `0` — a document with nothing to say about a lease
has not leased nothing), which is the shape a CLI older than the key's release
writes; a finite, non-negative number is **measured**. Any other value — a
string, a boolean, a negative — is handled **the way the decoder that owns the
document handles a shape it cannot read**, which is the whole of the rule:

  * the **closed** decoders (the console's `src/shared/schema.ts`, and the shared
    package's `shared/digest`, consumed by the mobile app today) refuse the
    document by the field's wire path (`total.leased_usd`, `repos[0].leased_usd`).
    That is deliberate: a value of a shape the decoder does not expect — or a
    negative one, which no money field of this contract can hold — is a document
    that decoder does not understand, not one it merely has not learned to read
    (an unknown key *name*, by contrast, is ignored: the compatibility rule above);
  * `budget show`'s caps reader is the console's **tolerant** reader: it refuses
    no document at all, and draws `n/a` for every number it cannot read (§3).
    So a `compute_usd` that is not a number is `n/a` there — not a second rule,
    but the same one in the one place that cannot refuse.

The rule above is about the values a document can carry, and a non-finite
number is not one of them: JSON has no `NaN` or `Infinity` literal, and Go's
encoder refuses to marshal either, so no document a command emits can hold one.
The decoders still answer differently if one reaches them —
`src/shared/schema.ts`'s `optionalMoneyAt` draws it `n/a`, while **in the shared
package's digest a non-finite number is refused** (`RANGE`,
`shared/digest/index.js`) — which is a fact about their code, not a case a screen
reading a document will meet.

**No console release draws it yet**: the Live screen's redesign (console
0.114.0) is the reader.

### `JSONRun` (`internal/watch/json.go`) — a live or queued run

| Field | Type | Unit | omitempty | Availability state |
|---|---|---|---|---|
| `run` | string | — | no | measured |
| `slug` | string | — | no | measured |
| `repo` | string | `owner/name` | no | measured |
| `title` | string | — | no | **job-written, already sanitized** (`safetext`/`oneLine`/`clip`) — the display layer re-sanitizes (design §5.1 rule 7) |
| `stage` | string | — | no | measured; `""` when the registry has none |
| `round` | string | — | no | measured — **a string here**, unlike `budget show --json`'s `round` (an int) |
| `verify` | string | — | no | measured |
| `models` | string | — | no | measured, a pre-rendered pair |
| `recipe` | string | — | **yes** | absent before the run reports one |
| `auth` | string | — | no | the run's auth mode, from the live registry entry. `oauth` means `spent_usd` is notional: such a run has no gateway, so `budget.AgentEntry.Spent` is its whole notional total (`snapshotEntry`, `internal/budget/registry.go`), while a cascade run's row carries the billed part its gateway charged and leaves its tier-A dollars to the notional counters (`notional_usd`) |
| `notional` | bool | — | no | **the availability state of `spent_usd`**: true when `spent_usd` is a subscription's list price, which for a live row is `auth == "oauth"` (`internal/watch/build.go`). It is a fact about this **live** row, never a reading of a recorded run's `model_basis`: a recorded run's split is `model_notional_usd` (C1.16/P12 — a cascade run the plan served in full has an api-list basis and billed nothing) |
| `spent_usd` | `*float64` | USD | no | **null = unavailable** |
| `age_seconds` | `*float64` | seconds | no | **null = unavailable** |
| `health` | string | — | no | `ok`, `silent` or `lost` (`HealthSilent` = 60 s, `HealthLost` = 180 s — `SilentAfter`/`LostAfter`, `internal/watch/build.go`) |
| `deadline` | string | — | no | `ok`, `near` or `over` — amber past 80% of the stage deadline |
| `halted` | string | — | **yes** | why, when halted |
| `queued` | bool | — | **yes** | a run known only from the runs bucket (`MergeQueued`) |
| `stuck` | bool | — | **yes** | queued past `QueuedStuckAfter` (**10 min**, `internal/watch/queued.go`) |
| `workflow` | string | — | **yes** | from `task.json`, queued runs only |
| `requested_by` | string | — | **yes** | from `task.json`, queued runs only — **a person's address**: the Spend screen's person view is off by default (D15) |
| `job` | string | — | **yes** | the Cloud Run job this run is in, from the run's own `launch.json` (`runstore.Launch.Job`) — the same read that produces the queued rows, so a live row has it too. Absent when the reader has no `launch.json` for the run. Under the project-centric model this is the **shared** job of a (profile, provider, account), not one repository's. **Display text, not an authority**: `launch.json` is writable by every launcher and, on a shared job, by every run (SECURITY.md, D4), so the value is what that object says, stripped of control characters — never a name to act on |
| `profile` / `provider` / `account` | string | — | **yes** | the (profile, provider, account) the job's name was **computed from**, matched against the project's published policy — never parsed back out of the name (R2-5), so a `job` no profile computes gets none of them. `profile` and `provider` are present together or absent together; `account` is present only beside them and only for a job of a named Claude account — a profile that lists `claude_accounts` gets **one job per account** beside its no-account job (`config.ProjectJobAccounts`), so a run on a named account's job carries that account's name, and a run on the profile's no-account job carries none. An absent `account` is **not** "this job mounts no credential": the no-account job holds the default account's token when the profile lists `default` and none otherwise (`config.ProjectJobTokenAccount`), so what is empty is the account axis of the job's name, never a claim about the run's credential. All three are absent for a run on a **per-repository** job (a baked workflow, a Bitbucket repository, an installation that has not migrated), whose name is no project job's. `provider` here is the **profile's** provider, not a general stamp of the repository's: it is the reason the field exists on the two run shapes but does not close §7's gap 7 |
| `launched_by` | string | — | **yes** | who started the run, from the run's own `launch.json` (`runstore.Launch.LaunchedBy`, **CLI 0.9.0**) — a person's address, or the identity an automated launch runs as (a service account, a CI runner). It is the **full recorded identity**: the text views show the short form a column has room for (`runview.LauncherShort` — the part before an address's `@`, the whole value for anything else). Absent when the record has none — one whose `launch.json` the reader does not have, or a launch built without a launcher (the recovery backfill from `result.json`, a local run with no cloud execution, or the synthetic launch `fugaro logs` builds) — **never a value the CLI invents**. **Display text, not an authority**: `launch.json` is writable by every launcher (SECURITY.md, D4), the value is sanitised and capped like every other bucket-derived string (`safetext.Field`, 512 runes), and no policy, admission or budget rule reads it |
| `plan_task` | string | — | **yes** | which task of which plan the run says it works on, from the run's own `launch.json` (`runstore.Launch.PlanTask`): `<plan>/<task>`, the id the plan's own graph spells its node with, recorded at launch by `fugaro run`'s own plan-task flag (the CLI's launch flag: §8 lists the flags the console itself spawns, and this one is not among them). **CLI 0.11.2** — the release that ships it (`internal/cli/recipes_skew.go`'s `planTaskSince`), which the release checklist verifies before the tag. Absent when the launch recorded none. **Display text, not an authority**: `launch.json` is writable by every launcher (SECURITY.md, D4), so this is a **join hint the console's graph may use, never an authorization input** — no policy, admission or budget rule reads it, and a run whose id matches no node is shown unjoined rather than refused. Sanitised and capped like every other bucket-derived string (`safetext.Field`), and shape-checked where it is written and where the CLI reads it (`task.PlanTaskProblem`: `runview.Join` and `watch.MergeFacts` drop an id that fails it, so the key is absent) |
| `pr_number` | int | number | **yes** | the pull request number the run has recorded, **whenever it has one** — running or finished, because a draft PR opens early in a run. It is the run's own number: `runview.Row.RecordPR`, the `record_pr` of `runs ls --json` (`internal/runview/runview.go`), read off the registry entry's `prUrl` — which the run writes from that same record the moment its draft PR opens — with `internal/watch`'s own anchored GitHub pattern (`ghPRURL`, the reading `MergeNow` and `PRRepo` do before they build `gh`'s argv), so no new bucket or network read is made for a live row. **The CLI release after 0.11.6.** Absent rather than `0` when there is none — a repository that numbers its pull requests from 1 makes `0` no number at all — and absent too when the URL is not one that pattern reads (a Bitbucket repository), which is "no number recorded", never a guess. **A number, never a link**: the console builds no link from a feed value (design §5.1 rule 2) |
| `credential` | string | — | **yes** | the **name** — never the value — of the credential the run's cascade is currently spending on (`budget.AgentEntry.Credential`, written by the run itself under the rules of C1.3; the credentials service chooses it). **CLI 0.10.0.** Absent when the run has not reported one, which is **"not recorded", never "no credential"**: a run that has not written the field is one this reader cannot account for, not one spending on nothing. A name a run wrote, so it is sanitised like every other database-derived string here, and **never an authority** — no policy, admission or budget rule reads it. A console that wants to say which account a run is on must not treat the field as proof that the run is on that account now (the run reports it as it switches) |
| `waiting_until` | string | RFC 3339, UTC | **yes** | when a run waiting for a credential expects to resume at (`budget.AgentEntry.WaitingUntil`, epoch ms, D152's "waiting, expected resume time"). **CLI 0.10.0.** Present **only while the run is still waiting**: presence is the state, so a reader that sees the field knows the run is held up rather than spending, and one that wants a countdown compares it with `now`. The text views show the same fact: the plain table as `waiting for a credential, expected resume T`, and the screen's FLAGS column as `waiting` with the instant on the run's detail lines (its column is 30 columns wide at its widest, and the sentence is 59 — a clip would eat the instant and any flag behind it) |

**A live run carries no link and no URL** — no `pr_url`, no `pr` (design §4.3
gap 6, §5.1 rule 2). The console renders no link for a live row; the run-detail
screen reads `diagnose RUN --json` for the one run whose detail is open. It does
carry `pr_number`, which is the number and not a way to open anything. It is
accepted, not read yet (GV2.3b): the graph screen that will show it beside a
running node from the feed alone, before the next `plan status` refresh tells it
the same number through `record_pr`, has not landed.

### `JSONFinished` (`internal/watch/json.go`) — a finished run

| Field | Type | Unit | omitempty | Availability state |
|---|---|---|---|---|
| `run` | string | — | no | measured |
| `slug` / `repo` | string | — / `owner/name` | no | measured |
| `title` | string | — | no | **job-written, sanitized** |
| `status` | string | — | no | the runstore status: `succeeded`, `failed`, `halted`, `cancelled`, `infra_error` |
| `outcome` | string | — | no | `ready`, `none`, … ; `ready` is what fills `ready[]` |
| `failed` | bool | — | no | measured |
| `pr_url` | string | URL | **yes** | **absent when the run has no PR — do not build a link from it** (gap 7) |
| `pr_number` | int | number | **yes** | **absent (0) when the run has no PR** |
| `job` | string | — | **yes** | as on `JSONRun`: a finished run's row is a run's row, and a reader following a run from `runs[]` into `finished[]`/`ready[]` (`jsonFinished`) must not lose the job it ran on |
| `profile` / `provider` / `account` | string | — | **yes** | as on `JSONRun` (`account` present only on a named account's job, absent on the profile's no-account job and, with the other two, on a per-repository job) |
| `launched_by` | string | — | **yes** | as on `JSONRun` (**CLI 0.9.0**): a finished run's row is a run's row, and a reader following a run from `runs[]` into `finished[]`/`ready[]` (`jsonFinished`) must not lose who launched it either. Absent when the launch record has none |
| `plan_task` | string | — | **yes** | as on `JSONRun` (**CLI 0.11.2**): a finished run's row is a run's row, so a reader following a run from `runs[]` into `finished[]`/`ready[]` (`jsonFinished`) keeps the plan task it claims. Absent when the launch record has none |
| `age_seconds` | float64 | seconds | no | measured |

### The three facts the whole feed turns on

These are also asserted by name in `TestDocsDesktopConsoleContractStatesTheThreeFeedFacts`.

**(i) One document per change, compact, at most one a second.**
`watchStream` (`internal/cli/watch.go`) emits when the model is
`dirty` **and** at least `gap` has passed since the last document, and in
`--json` mode `gap = watchJSONGap` = **`time.Second`** (`internal/cli/watch.go`).
The first document waits for all four streams or `watchFirstFrame` (**5 s**,
`internal/cli/watch.go`); a `refused` connection is the one case that
bypasses those tests — it emits immediately if the refusal arrives *during* the
stream, and the ordinary refusal emits nothing at all, because it is caught
before the stream starts (§10.5). The encoder is `json.NewEncoder(d.Out)` with **no `SetIndent`** —
one of the two compact, one-line-per-document commands in the console's set
(`logs RUN --json` is the other, §6.2).

**(ii) `--repo` narrows the JSON too.** `WatchDeps.emit` calls
`watch.FilterRepo(v, d.RepoKey)` **before** encoding,
so `watch --json --repo R` carries only `R`'s repository block and runs;
`FilterRepo` is `internal/watch/json.go`. What *is* unfiltered is the other
axis: `finished[]` and `ready[]` ignore `--all`/`--keep`/`--keep-count`. The
cobra help text still says "--json is always unfiltered" (it means the
finished-run axis), so **the code is the authority here, not the help string**,
and the console remembers that a `--repo` feed is narrowed.

**(iii) The synthetic and degraded modes emit different things.**
`watchDegradedDoc` (`internal/cli/watch.go`) is `lsDoc` **embedded** plus
two fields:

| Field | Type | omitempty | Meaning |
|---|---|---|---|
| (everything `lsDoc` has) | — | — | `project`, `runs[]`, `totals`, `warnings[]` — see §5 |
| `degraded` | bool | no | always `true` in this mode |
| `notice` | string | no | `degradedNotice` = `"no budget backend: showing run records only"` |

**Nothing in it has a cap, a burn rate, a kill state or a repository block**,
because none of those exist without the database. A screen that expects
`JSONDoc` must not render zeros for the missing half; the `degraded` key is the
discriminator (Task 2.0's `schema.ts`).

**And the degraded path writes a line that is not JSON to stdout**, on the same
stream as the documents: `runWatchDegraded` prints
`"%s\nrun records unavailable (retrying): %s\n"` (`internal/cli/watch.go`,
with `degradedNotice` first and the `safetext.Strip`ped error second) whenever
a read fails and it is not `--once`. **The console's reader must skip any line
that is not a JSON object** and surface it as a header warning rather than a
parse error or a crash (design §2.1, §4.3 gap 1).

## 3. `fugaro budget show --json`

**Command:** `internal/cli/budget.go` `newBudgetShowCmd` / `runBudgetShow`.
**Document:** `budgetShowDoc`, indented two
spaces (`enc.SetIndent("", "  ")`, `internal/cli/budget.go`).

| Field | Type | Unit | omitempty | Availability state |
|---|---|---|---|---|
| `project` | string | — | no | the Fugaro project |
| `gcp_project` | string | — | no | measured |
| `mode` | string | — | no | `observe` / `enforce` / `""` when unset; `""` also appends a warning. **This is the only document that can carry `""`** — see §2's `mode`, where `watch --json` normalises an unset mode to `observe` |
| `day` | string | `YYYY-MM-DD`, UTC | no | measured (`budget.DayDate`) |
| `max_reserve_usd` | `*float64` | USD | no | **null = unavailable**. The **ceiling** on any one lease (`budget.Limits.MaxReserveMicros`), which the Spend panel draws as "largest single lease (max reserve)"; §2's `leased_usd` is the **outstanding** amount a lease currently holds, and is not this |
| `global` | `showGlobal` | — | no | below |
| `defaults` | `showDefaults` | — | no | below |
| `repos` | `[]showRepo` | — | no | `[]` when none — **empty, not unavailable** |
| `running` | `[]showRun` | — | no | `[]` when none |
| `warnings` | `[]string` | — | no | `[]` when none; CLI-worded, shown verbatim |

`showGlobal`: `daily_cap_usd` `*float64`, `per_run_cap_usd`
`*float64`, `counted_usd`/`spent_usd` float64, `notional_usd` float64
(**notional**), `leased_usd` float64 (no `omitempty` — the same figure §2's
`leased_usd` carries, computed the same way, `runBudgetShow` through
`watch.LeasedUSD`; it is *leased, not yet spent*, and `0` when nothing is
leased), `headroom_usd` `*float64` (**null = unavailable**),
`kill` `*showKill` (null = not killed). All caps are **null = unavailable** and
never `0`.

**`spent_usd` is the billed figure, on `global` and on every row alike**: the
money the gateway charged, under the counter it was counted in. A subscription's
part is notional, so a cascade run's tier-A dollars are in `notional_usd` and
only its charged part is here (§2's `auth`/`notional` state the same split for a
live row, off the registry entry rather than the counters). **A Spend caps row
reads `spent_usd` as billed and there is no `billed_usd`**: the key does not
exist, is not planned, and a second name for the same counter would be a way for
the two to drift apart.

`showDefaults`: `repo_daily_cap_usd`, `repo_per_run_cap_usd`,
both `*float64`.

`showRepo`: `slug`, `repo` (`omitempty`),
`daily_cap_usd` `*float64`, `daily_cap_source` (string, no `omitempty` —
**`repo`, `default` or `none`**), `per_run_cap_usd` `*float64`,
**`per_run_cap_source`** (string, no `omitempty` — `repo`, `default`, `global`
or `none`; `perRunCapSource`), `counted_usd`, `spent_usd`,
`notional_usd`, `compute_usd` (below), `leased_usd` float64 (no `omitempty`, as
on `showGlobal` and floored the same way: the repository's own outstanding
lease), `headroom_usd` `*float64`, `kill` `*showKill`,
`running` `[]showRun`. **`per_run_cap_source` is the "binding scope named"
the design's §6.2 Spend screen draws** — it is already in the document and
needs no CLI change (§4.3's "not a gap" note).

**`compute_usd` is written by `budget show --compute` and by nothing else**
(the flag landed on 2026-10-29; the owner approved the key on 2026-10-28).
It is the **only flag-conditioned key** in this document: without the flag the
row carries **no such key at all**, so a command without it writes byte for
byte what it always did — `internal/cli/computeFigure` is why the key can be
absent and null and not one or the other, and `TestShowComputeIsOptIn` pins
both halves. With the flag, the key is a `*float64` of the repository's
compute dollars for the day: a **number** is the estimate
(`runstore.Cost.ComputeUSD`, the same account §4 reports — and, like
`leased_usd`, already a real wire name, on `report --json`'s rows and on
`runstore.Cost`), and **null** is *unavailable* (`n/a`, never `0`): no run of
the day recorded an estimate, the run records that did cannot be added up to a
number (`internal/cli/repoCompute` leaves the row unknown rather than write a
non-finite figure `json.Marshal` refuses), or the runs bucket could not be read
— the last two append a warning saying so, and the command still succeeds. The
console reads it as **optional** — an absent key, `null` and a non-number all
read as unavailable, because this document's reader refuses nothing (§2 states
why that is the same rule and not a second one).

Three facts the number's reader has to know, all of them the CLI's own words
in the command's long help. **(i) It is an estimate by design**, priced from
each run's own price table, so a VM run's figure leaves the VM's boot disk and
Cloud NAT out (design compute-backend.md §5.2, C17). **(ii) It is the day's
finished runs only, attributed to the UTC day each run *started* on** — a run
minted before midnight and started after it counts to the day it started, and a
run still in flight has recorded no result yet — so **today's figure is always
partial** and a screen must not draw it as a closed day. **(iii) The read is
narrowed to the rows this document carries**: one listing of the bucket's
repositories, then, for each repository shown, a listing of its runs minted
since the previous day began and a record read for each of those — a run's
start day is only known once its record is read, so the reader looks a day
wider than the day it reports. That cost is why `--compute` is opt-in: the rail
and the Project screen call this same command and pay for no bucket read. The
figure reaches a person too: with the flag the text view prints `compute:` per
repository, `n/a` where the JSON says `null`. **The console's own `budget.show`
row may pass it too — opt-in, on the Spend screen's caps read alone and only to
a CLI at or above `BUDGET_COMPUTE_CLI_FLOOR` (`0.13.0`, the release that ships
the flag; `budgetComputeSupported`, `console/src/shared/version.ts`), so an
older CLI is read with the key absent and handed no argv it would refuse: the
Spend caps panel draws no compute cell at all for a document that carries no
`compute_usd`, rather than a column of `n/a` — while the rail and the Project
screen pass nothing.**

`showRun`: `slug`, `run`, `repo`°, `workflow`°, `title`°,
`stage`°, `round` (**int**, `omitempty` — unlike `watch`'s string),
`auth`°, `coder`°, `reviewer`°, `spent_usd` float64 (no `omitempty`),
`pr_url`°, `halted`°, `started_at`°, `updated_at`° (RFC 3339 strings), `stale`
bool°. `°` = `omitempty`.

`showKill`: `by`°, `at`°, `reason`° — the same shape as
`JSONKill`.

**Flags, read off the cobra command** (`newBudgetShowCmd`): `--repo`, `--all`,
`--json`, `--compute`, plus `addCloudFlags`' `--config`, `--project`,
`--gcp-project`, `--region`.

## 4. `fugaro report --json`

**Command:** `ee/report/cli/report.go` `NewReportCmd` (the command moved to `ee/` in E2.2) / `runReport`.
**Document:** `ee/report/render.go` `JSONDoc`, written
by **`report.JSON(w io.Writer, rep Report, m Meta) error`** —
the exported writer, not a `cli`-side struct. Indented two spaces
(`report.JSON`, `ee/report/render.go`). A schema note also lives above the type
(`render.go`'s comment block: `by`, `since`, `until`, `weeks`, `rows`,
`totals`, `warnings`).

`JSONDoc`: `project`, `source`, `degraded` bool, `notice`°,
`by` (string: `day`/`week`/`month`/`year`/`repo`/`model`/`person`/`mode`),
`since`, `until` (UTC dates), `weeks` (**always the literal
`"iso-utc-monday"`** — the ISO week convention is declared, not inferred),
`rows` `[]JSONLine`, `totals` `JSONLine`, `warnings` `[]string`.

`JSONLine`: `key`, `start`°, `end`°, `partial` bool, `unreadable`° bool,
`model_micros` `*int64`, `model_usd` `*string`, `notional_micros` `*int64`,
`notional_usd` `*string`, `compute_micros` `*int64`, `compute_usd` `*string`,
`compute_estimated` bool, `run_hours` `*float64`, `runs` int64, `calls` int64,
`tokens` `*JSONTokens`°.

- **`*_usd` are exact decimal strings of the integer `*_micros`** (`USD6`), not
  floats: the console parses the string, and micros is the integer of record.
- **`model_micros`/`model_usd` are null exactly when the row is `unreadable`**
  — a by-mode row for which the CLI has no readable run record at all
  (`Row.Unreadable`, `ee/report/report.go`; only `--by mode` sets it):
  every run in it either has a `result.json` the build cannot read, or has not
  written one yet. The row still counts its `runs`, and null is *unavailable*,
  never a measured `0`. Every other row writes both keys, so a number there is
  a measurement, and `unreadable` is omitted.
- **The console reads `unreadable` and draws such a row as `n/a`**
  (`SpendRow.unreadable`, `rowOf` in
  `console/src/renderer/screens/spend.ts`). The key is read as a boolean and an
  absent one is `false`, so a measured row is unchanged; an unreadable row's
  model, notional and compute cells are `n/a`, never `$0.00`, and the row line
  adds the console's own sentence `no readable record` beside them
  (`UNREADABLE_MARKER`). Such a cell is the console's own reading,
  `unavailable(NO_RECORD_REASON)` (`microsReading`), not `readNumber`'s
  sentence for a missing key: the key is present and `null` on purpose, and the
  cell's `title` says `no readable record for these runs`. The sentence names
  the fact a corrupt record and a run that has not written one share —
  "unreadable" alone would misname the in-flight run, the usual `none` row of a
  window that reaches into today. The row's `runs` is still drawn: the run
  launched.
  **The totals line is read the same way** (`SpendTotals.unreadable`,
  `totalsOf`), so a window in which every row is unreadable draws a `TOTAL` of
  `n/a`s with the same sentence rather than the `$0.00` that sums nothing.
- **`notional_micros`/`notional_usd` are null when not tracked** —
  `w.NotionalTracked` (`jsonLine`). That is *unavailable*,
  and it is **notional**, never added to a real total (design §6.2, "one
  account of cost").
- **`compute_micros`/`compute_usd` are null unless `ComputeState ==
  ComputeKnown`**, and a present one is **estimated** (`compute_estimated`
  true). Null = *unavailable*, never `0`.
- **`run_hours` follows `compute_micros`** — set only when `ComputeState ==
  ComputeKnown` (`jsonLine`), so it is null for the same rows.
  The key carries **no `omitempty`**, so it is always present and **null**
  rather than absent when the hours are not known: read it as
  *unavailable*, never as a measured `0`. **Two paths null it, and only one of
  them is degraded**: a degraded `report` (`reportDegraded`), which builds on
  the run records and sets `rep.NoHours = true`; and `--by mode`
  (`reportByMode`), which also reads the run records and sets it — so a
  document with `degraded` `false` and `notice` absent can carry no
  `run_hours` at all. Read the key, never the `degraded` flag, as the fact
  about the hours.
- **`tokens` is absent entirely when not tracked** (`TokensTracked`).
- **`partial`** is the design's `(partial)` marker: the day has no final
  Firestore document. A day beyond the retained window is a **gap**, not a row
  of zeros.
- **`--by mode` is the second shape whose `source` is `"run-records"`, and it is
  not degraded** (`reportByMode`). A run's rigor mode is written in its own run
  record and in no rollup of the spend history, so this one breakdown reads the
  runs bucket whatever the history holds: `degraded` is `false` and `notice` is
  absent, and the provenance is in `source` and in `warnings[]`. A console must
  not read `source: "run-records"` as meaning history is missing.
  - A row's `key` is a mode — `poc`, `startup`, `pro`, `critical`, `hotfix` —
    or `none` for a run whose record carries no mode, **or carries one this
    build does not know**: the raw text of a record is never a key, so a future
    or invented mode reads as `none` rather than as a word the console has no
    class for.
  - Every run counts **once**, on its launch day, so `runs` is the row's run
    count and `--since`/`--until` bound it by that day. A run the CLI has no
    readable record for — the bucket holds a `result.json` this build cannot
    read, or holds none because the run has not written one — did launch: it
    counts once in `runs` and adds nothing to the money columns. A row made
    only of such runs is marked **`unreadable: true`** and its money keys are
    **null** — *unavailable*, never the measured `0` the sums would carry
    (`n/a` in the table and the CSV) — while a row holding one readable record
    keeps its sums and carries no mark.
  - **The `totals` row follows the rows the same way**: it is `unreadable`, and
    its money keys null, exactly when every row it sums is. A total over a
    measured row keeps that row's figures, so a report whose window mixes the
    two reads a `TOTAL` short of what the unreadable runs spent; `warnings[]`
    says how many runs hold a `result.json` the read could not use, so the
    reader is told the figure is short rather than left to take it for the
    window's whole spend. A run that has not written one yet — in flight, which
    every window reaching into the last minutes holds — is counted and marked
    in its row like the corrupt one and is **not** warned about: that is
    routine, and a warning on it would fire on every report.
  - `calls` is `0` on this grouping's rows, as on `--by person`'s: a run record
    carries no call count, and the column is not drawn. A window holding more
    runs than the read opens is reported to its newest runs, and `warnings[]`
    says so.

`JSONTokens`: `in`, `out`, `cache_read`, `cache_write`, all
int64, none `omitempty`.

**Flags** (`NewReportCmd`, `ee/report/cli/report.go`): `--repo`, `--since`, `--until`, `--by`, `--json`,
`--csv` (the allow-list excludes `--csv`), plus the cloud flags.

## 5. `fugaro runs ls --json` (and the degraded feed's body)

**Command:** `internal/cli/runs.go` `newLsCmd` / `runLs`.
**Document:** `lsDoc`, indented (`printRows`), and
**embedded** by `watchDegradedDoc` (§2, fact (iii)).

| Field | Type | omitempty | Availability state |
|---|---|---|---|
| `project` | string | no | the Fugaro project |
| `runs` | `[]runview.Row` | no | `[]` when none |
| `totals` | `runview.Totals` | no | below |
| `warnings` | `[]string` | no | the lines a human listing prints before its table |

`runview.Row` (`internal/runview/runview.go`) is the run shape behind
**both** `runs ls --json` and `diagnose --json`'s `row`. The fields the console
reads:

| Field | Type | omitempty | Note |
|---|---|---|---|
| `run`, `run_id`, `repo` | string | no | |
| `slug` | — | — | **not a field**: `Row` has no `slug`; the slug is the row's own bucket key |
| `workflow` | string | **yes** | |
| `title` | string | **yes** | the run's own job title (`task.Spec.TitleLine`: the title flag of `fugaro run`, else the task's first line) — the field `fugaro watch` names a run by, absent when the task has neither; **task-derived, not sanitized** (the task-text fallback is raw) — the display layer sanitizes it (design §5.1 rule 7) |
| `oauth_account` | string | **yes** | the named Claude account |
| `recipe`, `config_sha256` | string | **yes** | |
| `project_layer` | `*runstore.ProjectLayerRecord` | **yes** | absent before 0.6.0 |
| `status`, `stage`, `reason` | string | `status` no, other two yes | |
| `failure_class` | `failure.Class` | **yes** | why the run did not end `succeeded`, as one value of the closed vocabulary of `docs/design/failure-classes.md` §3: the class the runner wrote beside the terminal status, and — for a record written before the field shipped — the reader's reading of the record's own `reason` (`runview.ClassOf`). **A reading and never written back**: the bucket keeps the bytes it had, so the same record read twice reads the same. Absent for a run that did not fail — a `succeeded` run, a run still going and a run that never started name no cause — and for a row the read side ended itself: a preemption, a lost execution and a cancel before launch, none of which left a record that ever settled, so nothing named a cause and the row's own account of what happened to it is `derived`; and `error`, the row for a run whose execution could not be followed at all, whose record the row may not claim to have read. `reason` keeps its text and its meaning byte for byte, and a value a reader does not know reads as `unclassified`, so the vocabulary may grow inside the field |
| `derived` | string | **yes** | **what the read side derived, and not a status of its own** — the CLI's 2026-10-21 ruling. Written only on a row whose record was never finalized and whose Cloud Batch job the platform preempted: `status` stays `failed` (the vocabulary every reader already switches on) and `derived` carries the fact (`preempted`, `runview.DerivedPreempted`), with `reason` saying so in a sentence. Absent for every other row, which is what keeps the shape additive. See **the derived status** below |
| `halt` | `*runstore.Halt` | **yes** | why a run halted |
| `batch`, `requested_by` | string | **yes** | `requested_by` is a person's address |
| `launched_by` | string | **yes** | who started the run, from its `launch.json` (**CLI 0.9.0**); absent when the record has none. The same value `JSONRun`/`JSONFinished` carry, and display text only — see `JSONRun`'s row |
| `plan_task` | string | **yes** | the plan task the run claims, from its `launch.json` (**CLI 0.11.2**); absent when the record has none. The same value `JSONRun`/`JSONFinished` carry, and display text only — see `JSONRun`'s row |
| `pr_url` | string | **yes** | the URL as the host gave it |
| `execution`, `log_url` | string | **yes** | `log_url` is what `logs RUN --url` prints |
| `created` | time.Time | no | RFC 3339 |
| `cost` | `runstore.Cost` | no | model/compute dollars: `model_usd` is the **whole** model figure and `model_notional_usd` the subscription's part of it (C1.16, P12 — a mixed cascade run's `model_basis` is `api-list` for the whole run, so the basis is only the fallback for a row written before the split existed) |
| `image_age_s` | `*int64` | **yes** | null = unknown |
| `terminal`, `settled` | bool | no | |
| `branch`, `outcome` | string | **yes** | |
| `task_pr`, `record_pr`, `pr` | int | **yes** | **`pr` is the run's PR number on this shape** (design gap 6's second spelling) |
| `pushed` bool, `pushed_head`° | | | |
| `started_at` | `*time.Time` | **yes** | null = not started |
| `base_branch`, `previous_run` | string | **yes** | |
| `follow_up` | bool | no | |
| `draft_fallback` | bool | **yes** | the host has no draft PRs, so the PR is an ordinary one with `[DRAFT]` in its title |
| `pr_status_at` | `*time.Time` | **yes** | when the runner last wrote the PR's status section — **null = never** |
| `stale_draft` | bool | **yes** | the record is not final but its draft's status section is old, or the execution is gone: the run probably died |
| `artifact_count` | int | **yes** | what an artifact run's record says it delivered; absent for every run that has none, and the count `runs ls`'s `ARTIFACTS` column shows — its total is `artifact_bytes` |
| `artifact_bytes` | int64 | **yes** | the recorded total size of those artifacts (a negative recorded size counts as none) |
| `changed_files` | `*runstore.ChangedFiles` | **yes** | the paths the run changed, as the run itself recorded them (design D27, console v2 plan V2.1.2): `paths`, `total` and `truncated` below. **Absent for a run that recorded none** — every record written before the field existed, and every run that died before finalize — and absence means **"no record of what this run changed"**, never "it changed nothing" (the `-` cell of `runs ls`'s `FILES` column) |
| `rigor` | `*runstore.RigorRecord` | **yes** | the mode the run resolved, the layer that won and the areas that matched (docs/rigor-modes D22, D24, D25): `mode`, `from`, `areas`, `at` and `revision` below. **Absent for a run that resolved none** — a record written before the field existed, a run that died before bootstrap, or a block that cannot be read as a decision — and absence means **"no record of this run's rigor"**, never "it resolved nothing" (the `-` cell of `runs ls`'s `MODE` column) |
| `owner_decisions` | `*runstore.OwnerDecisions` | **yes** | what the run's review left for the repository's owner to settle (OD1): `decisions` (each one `file`° and `summary`, below) and `total`. **Absent for a run that left none** — every run whose review named no such finding, every run that stopped for its own reason, and every record written before the field existed — and absence means **"nothing is pending"**, never "the review had nothing to say" (the row's `reason` says the same sentence, and the `halt.reason` of `owner_decision` is the other half of the same fact where the loop stopped on the finding) |

**`rigor` is the mode the run resolved, and it is read from the base branch.**
The block is one decision (docs/rigor-modes D24), carrying the mode, the layer
that decided it (`area`, `repo`, `project`, `default` or `request` — never
`reached`, which is the resolver's answer to a later moment asking again and
which the fold replaces with the earlier, narrower reason), every area rule
that matched with the mode it sets and the moment it was first applied at
(`bootstrap`, `round` or `finalize` — D22's three moments) and the revision the
configuration was read from. The mode's configuration comes from the **base
branch** for every run kind (D25), so a run's other keys may come from the
checked-out ref while its rigor comes from what it will merge into: `revision`
is how a reader tells those apart, and it is written either way. The block is
D22's monotone fold — a later moment never lowers the mode, and the moment it
names is the last one that changed the block.

| Field | Type | omitempty | Note |
|---|---|---|---|
| `mode` | string | no | the resolved mode: `poc`, `startup`, `pro`, `critical` or `hotfix` (`pro` is Fugaro's built-in default, D19) |
| `from` | string | no | the layer that decided it; **never `reached`**, which is the resolver holding an earlier moment's mode (D22) — the fold keeps the layer that actually set it (D24's "the layer that won"), so a block that says `reached` is not one this runner writes and `Bounded` refuses it |
| `areas` | `[]object` | **yes** | every rule that matched, **strictest first** (D23) and at most `rigor.MaxAreas` = **100** of them, each `name` (the rule's own name), `mode` (the mode that rule sets) and `at` (the moment it was first applied). **Absent when none matched** — a reader must not see "there were rules" where the answer is "there were none". The order is one the record establishes, not one it is handed: `Applied` sorts when it folds a moment in and `Bounded` sorts a block the job wrote, so the first entry is the rule whose mode decided — the one `runs ls`'s `MODE` column names |
| `at` | string | no | the moment of the resolution the block records: the last one that changed it |
| `revision` | string | **yes** | where the configuration was read from, `origin/<base branch>`; absent when the run could not name it |

**The reader bounds it again.** The block is job-writable, so `runview.Join`
does not pass it through as written: `RigorRecord.Bounded` refuses a block
whose mode, layer or moment is outside its vocabulary — the row then carries no
block, the same answer as a record that has none — and re-applies the caps
(`rigor.MaxAreas` rules, `rigor.MaxAreaName` = 40 runes a name, 120 runes of
revision), dropping an area whose own mode or moment is unreadable, and sorting
the rules strictest first before it caps them, so the rules that survive are the
strictest ones and the first of them is the rule a reader names. A tampered
`result.json` therefore reaches the console as no block or as a bounded one,
never as a value no reader can check.

**`owner_decisions` is what the run is waiting on, and it is a count and a list
together.** A review finding whose only remedies are a decision the run cannot
make — a licence or legal clearance, a product or policy choice, a step only the
owner can run — is marked by the reviewer (`needs_owner`) and is never handed to
a fixer. When every finding of the deciding round is one of those, the run halts
with `halt.reason: "owner_decision"` (a reason of its own, beside the five
governance ones) and its pull request stays a draft. A deciding round that left
one beside a fixable finding, or that shipped while carrying one, ends no halt —
the loop ran to its own end — but a draft for the same reason: the block is on
the record, `reason` is the same waiting sentence, and readiness is blocked
above every verdict, so a console that reads `outcome: "draft"` and a block
together is reading one fact. Either way the list is:

| Field | Type | omitempty | Note |
|---|---|---|---|
| `decisions` | `[]object` | no | the pending decisions, **`[]` when the block is present but empty — never null**; each one `file`° (the path the finding names) and `summary` (one sentence, clipped to `runstore.MaxOwnerDecisionText` = **200** runes) |
| `total` | int | no | **how many decisions the review really left**, so a truncated block still says how big the set it is part of was |

**`total` is the count and `decisions` is the list, and neither replaces the
other.** The block is capped at `runstore.MaxOwnerDecisions` = **100** when it is
written, and an entry without a sentence is not stored at all; the reader bounds
it **again** (`runview.Join`, `OwnerDecisions.Bounded`), because the record is an
object the job can write, and a block whose every sentence is unreadable reads as
no block — the run's own `reason`, and its `halt.reason` where it halted on them,
still says it is waiting. The count a reader
shows is `total`, never `len(decisions)`.

**The derived status (`derived`), and why it is not a status.** A Cloud Batch
run on a Spot VM can be preempted: the platform reclaims the VM, the job ends
`FAILED`, and no component ever finalizes the run's record — the budget lease
expires by its own timeout and the history sweep settles the cost, so there is
nothing that could write the outcome down. The owner's ruling of 2026-10-21 is
that **nothing gains a new permission for this**: the read side derives it when
the run is read (`runview.Join`), from the run's own objects plus the Batch
job's own report (`statusEvents`, or the task state's reserved preemption exit
code — `internal/backend/batch`'s `preemptExitCode`), and the derived status is
**never written back** anywhere.

So the row reads `status: "failed"`, `derived: "preempted"`, and `reason` is
`runview.ReasonPreempted`. The three properties a reader needs:

* **`status` is unchanged.** `failed` is what a reader switches on, what the
  Runs screens draw and what `runs ls`'s table column carries; the derivation
  is a second key beside it. A row is never *only* `derived`: an absent key
  means nothing was derived, and `derived` is never `""`.
* **`derived` is drawn beside the status, never instead of it.** `runs ls`'s
  table and `fugaro diagnose` print the status and the derivation together
  (`failed (preempted)`), the way a halt already prints its reason
  (`halted (run_cap)`); the JSON keeps both keys separate so a script never has
  to parse the decorated cell. The console reads `status` and `reason` today
  and may render the marker from `derived` when it does.
* **A finalized record always wins, and an unreadable job never derives.**
  A run whose record exists is never derived, however its job reads; a job
  whose state cannot be read, or whose state is not a terminal `FAILED`, stays
  exactly as recorded — a live run is never made to look failed by a read the
  reader could not complete.

`derived` is one of the string keys the console does not sanitize: it is an
enum the CLI writes from its own table, like `status`, `outcome` and `mode`
(`NON_TEXT_STRING_KEYS`, `console/src/shared/schema.ts`), never text a job
wrote.

**`changed_files` is the run's own account, and it is bounded.** The runner
reads the paths of the run's own commits (`origin/<base>...HEAD` for a first
run, its start commit for a follow-up — `gitops.ChangedFiles`) and stores at
most `runstore.MaxChangedFiles` = **200** of them, or
`runstore.MaxChangedPathBytes` = **16 KiB** of path, whichever comes first:

| Field | Type | omitempty | Note |
|---|---|---|---|
| `paths` | `[]string` | no | sorted, as git listed them; **`[]` when the run changed no path — never null** |
| `total` | int | no | **how many paths the run really changed**, so a truncated record still says how big the set it is part of was |
| `truncated` | bool | **yes** | set **only** when `paths` is shorter than `total` — a console that renders a truncated set as the whole set is showing a number the record does not support |

**Paths only.** No contents, no diff, no commit message, no line counts: a
path can name a client, a person or a project, so the record holds the least
that answers "did these two runs touch the same file". A rename is recorded as
the delete and the add it is, because a path that moved is a path two runs can
collide on.

**The reader bounds it again.** The record is an object the job can write, so
`runview.Join` does not pass the set through as written: `ChangedFiles.Bounded`
drops empty paths, re-applies both caps, never lets `total` fall below the
paths kept, and sets `truncated` when the record said so or when `paths` is
short of `total`. A tampered record therefore reaches the console bounded and
with its truncation flagged, never larger or dressed as whole.

`runview.Totals`: `runs` int, `model_usd` float64,
`model_notional_usd` float64 (**notional** — kept apart from `model_usd` by
`Sum`), `compute_usd` float64 (**estimated**),
`total_usd` float64, `compute_not_estimated` int (**the count of rows whose
compute is unknown: their `compute_usd` 0 means "not estimated", so the sums
are a lower bound** — the console renders that, it does not hide it).

**Flags** (`f := cmd.Flags()`, `newLsCmd`): `--repo`, `--all`, `--mine`, `--since`, `--batch`,
`--limit`, `--after`, `--state`, `--pr`, `--watch`, `--interval` (hidden), `--json`, plus the cloud
flags. The allow-list excludes `--watch` (it is `watch` under another name).

**`--limit=N` and `--after=RUN-ID` are the page and its cursor, and they are the two
flags the console's `runs ls` row adds to the list above** (D818, the console
coordinator-v2 plan's C2). `--limit=N` prints the **newest** `N` rows of the window;
`--after=RUN-ID` starts the listing after that run, in the listing's own order, so a
page is what the previous page's last row hands on — the two are the console's one
way to walk a window a row at a time. **Both travel in the `=` form**, one word each,
the value inside the flag's own word and never a word of its own, so a value can never
be read as a flag (`--after=--watch` is a cursor the grammar below refuses, not a
second flag on this argv); and **both are held before a child exists**: the limit is a
positive integer of at most `RUNS_LIST_MAX_LIMIT` (**200**, this console's own
ceiling rather than the command's — `--limit 0` is the command's own "every row",
which is the read the page exists to avoid asking for), and the cursor is a run id in
the `run_id` grammar of this very section, which is the id a page prints. An id the
window does not hold stays the command's refusal and not this console's business: only
the command knows what its window held.

**A page is not a total: `totals` is the whole window's, never the page's.** `--limit`
shortens what is printed and shortens nothing else — the window is read either way, so
a page costs what the window costs — and therefore `totals.runs`, the counts behind it
and every dollar figure in it stay the window's however small the page is. A reader
that divided one of those figures by the rows it drew would print a number this
document does not carry, which is the one wrong answer the two flags could produce.

## 6. The other documents

### 6.1 `fugaro diagnose RUN --json`

**Command:** `internal/cli/diagnose.go` `newDiagnoseCmd` (`Use: "diagnose RUN"`),
indented (`printDiagnosis`). **Document:** `Diagnosis`
(`internal/cli/diagnose.go`) — a **flat** struct with no `MarshalJSON`, so its keys are
exactly its own tags and nothing is nested under a `process` or `checks` key:

| Field | Type | omitempty | Note |
|---|---|---|---|
| `project` | string | no | the Fugaro project |
| `row` | `runview.Row` | no | §5 — this is where a live run's PR number and `pr_url` come from (design gap 6). *Note 2026-10-27:* the live row of `watch --json` now carries `pr_number` too (GV2.5), a draft PR's number parsed from the run's PR URL with the GitHub URL pattern; `pr_url` is still only here |
| `failure_class` | `failure.Class` | **yes** | why the run did not end `succeeded`, as one value of the closed vocabulary of `docs/design/failure-classes.md` §3 (D1460): the same value `row.failure_class` carries (§5), on this shape because diagnose is the command a person runs to be told why the run stopped, and a reader of this document is never sent into the row for it. Absent for a run that did not fail, and for a row the read side ended itself (§5) |
| `halt` | `*runstore.Halt` | **yes** | why a halted run was halted |
| `verify` | `[]verify.Record` | **yes** | `result.json`'s verify records |
| `failed`, `flaky` | `[]string` | **yes** | the last test record's failed and flaky tests |
| `findings` | `[]runner.Finding` | **yes** | the last review's findings; one element is `severity`, `file` and `summary` and, when its transcript carried a score, the four optional properties **`likelihood`**, **`impact`**, **`fix_cost`** and **`class`** (rigor D3, read back here by T4.1) — the keys of an element, never of `Diagnosis` itself. The three axes are 1–5 and `class` is `runner.VerdictSchema`'s enum. Each is absent when the transcript did not have it, so a finding from a transcript written before the score existed — every transcript a run made before rigor mode shipped — carries none of the four and reads in the document exactly as it always did |
| `agent_message` | string | **yes** | capped at `agentMessageBytes` = 4 KiB |
| `log_tail` | `[]string` | **yes** | the newest `diagnoseLogLines` = 30 lines |
| `report_path` | string | no | |
| `artifact_paths` | `[]string` | **yes** | one `runs/<slug>/<run>/artifacts/<name>` path per artifact the record lists, in the record's own order: an artifact run has no pull request, so this is where its deliverable is named. The record's list only, never a bucket listing, and a name `fugaro artifacts get` would refuse is still named here (`fugaro artifacts ls` shows it with the reason) |
| `draft_note` | string | **yes** | a line about the run's draft PR, when it needs one |
| `follow_up` | `*runstore.FollowUp` | **yes** | what a follow-up acted on; absent for a first run |
| `comments_path` | string | **yes** | where the comments it was given are stored |
| `owner_ruled` | string | **yes** | the repository owner's ruling this run worked from (OD1): the text the launcher appended to the run's brief verbatim — the ruling its own launch was given, or, for a follow-up launched without one, the one the run before it worked from, which it inherits — so the run the console diagnoses is one that was told the answer. Absent for a run that worked from no ruling: every run but a follow-up that answered an `owner_decisions` block, and every follow-up after a run that worked from none. **The run's own bytes**, exactly as the fields above. Redacted and clipped to `task.MaxOwnerRuledBytes` = 4 KiB on read, and held to `task.ValidateOwnerRuled` when a follow-up inherits it: a record whose ruling is not text a brief may hold (blank, a control character, over the bound) is read as a run that worked from none, and its `owner_ruled_decisions` with it. The decisions that ruling answered are the record's `owner_ruled_decisions`, and the rulings this run worked from behind it the record's `owner_ruled_earlier` — neither of which this shape carries: the console shows the run's own ruling, and the chain is the reviewers' |
| `route_by`, `model_by` | `map[string]float64` | **yes** | the run's spend by provider route and by model (a Claude call has no route) |
| `stages` | `[]runstore.StageTiming` | **yes** | the run's per-stage record (governance T1.1–T1.3), in the record's own order and bounded at `diagnoseStages` = 128: each entry is `name`, `started_at`, `duration_s`, `calls`°, `agents`°, `model_usd`°, `model`° and `model_notional_usd`°. `calls` and `agents` are **pointers**, so a stage nobody counted has the key absent, which is not the same fact as a stage counted at zero. `model_usd` is what the stage's calls cost, `model_notional_usd` the notional part of it (D140). The stages are the run's whole model spend but for gateway spend that settled after its stage's wait ran out, which is in `model_by["unattributed"]` and in no stage: exactly, `sum(stages[].model_usd) + model_by["unattributed"] == row.cost.model_usd`. **That identity is why the bound is where it is**: it holds only for the whole list, so `diagnoseStages` sits above the loop's own maximum (87 stages under the config limits, plus the provision and render stages) and never truncates a run the loop can make — it guards against the record a hand can edit, not against the loop. Absent when the record has no stages, and when there is no record at all |
| `reviews` | `[]runstore.ReviewSummary` | **yes** | the record's review rounds, in the record's own order and bounded at `diagnoseReviews` = 96: each entry is `round`, `tier`, `verdict`, `findings` and, when the round recorded one, `bounce`°, `head`°, **`model`°** and **`effort`°** (CLI 0.16.0). `tier` is `first`, `senior` or `adversarial`, and `verdict` is the reviewer's own word or `none` for a round that returned nothing parseable. `model` and `effort` are what **reviewed** the round — the stage's pinned model and the reasoning effort that reached the stage — and the pair with `head`, the commit the round read, tells a reader which model the record says reviewed which head: these are the record's own unsigned bytes, recorded and not verified, so they are a hint for a person and never an input to a decision. `model`, `effort` and `head` are **absent** in a round that recorded none, which is every review written before CLI 0.16.0, so a reader tests the key rather than the empty string. Every string in the list is redacted, `model` and `effort` included: they are the record's bytes, which a hand can edit. The bound sits above the loop's own maximum (77 rounds), so it guards against the record a hand can edit, never against the loop; a run that reached it prints the first `diagnoseReviews` rounds and then the line `… at most 96 review rounds are printed`, which says the list is cut and not how many rounds it left out. Absent when the record has no reviews, and when there is no record at all |
| `reported_usd`, `charge_usd` | float64 | **yes** | what providers said the calls cost, beside what the table charged — **absent (0) when no provider reported one**, so the pair is read together |
| `reported_warning` | string | **yes** | set when the two differ by more than `reportedGapPct` percent |
| `pin_warnings` | `[]string` | **yes** | the model prices the run's cap counted on a guess |
| `tolerated` | `*runstore.ToleratedFindings` | **yes** | the findings the run's resolved mode let through, in the reviewer's order (rigor T3.4): `findings` (one entry per finding — `title`, `label`, `mode`, `likelihood`, `impact`, `fix_cost`, `class`) and `total`, how many findings the mode really tolerated, never below the entries listed. The block is bounded at `runstore.MaxTolerated` = 100 when it is written and **again on read** — the record is an object a hand can edit — and every string in it is redacted, the enums included (they are the write path's vocabulary, which a hand-edited record need not respect). Absent — never an empty block — for a run that tolerated nothing |
| `checkpoint` | `*runstore.Checkpoint` | **yes** | the report a governance stop left (governance T6.1, read here by T6.2; D329, G7): `trigger`, one of the governance halt reasons (`stage_budget`, `review_ratio`, `fanout`, `review_scope`, `expected_exceeded`), `why_over`, the runner's own words for the stop, `estimate_usd`, the model spend the record had reached (notional dollars included, like `cost_usd`), and the stopping agent's `done`, `next` and `hard_instructions`. The last three are **absent until the run's checkpoint stage answers** (T7.3), so a missing key is an answer nobody was asked for — never a claim that there was nothing to say. Each text field is clipped to `runstore.MaxCheckpointText` = 500 runes when it is written and **again on read**, and every string in it is redacted, `trigger` included (a hand-edited record need not respect either). Absent for a run that took no governance halt — a budget or credential halt carries none — and absent, rather than an empty block, for the `"checkpoint": {}` a hand can write |

**`runs ls --json` deliberately does not carry `stages`, `reviews` or
`tolerated`.** All three are the record's, and the list is one document for a
whole window of runs: a per-stage, a per-round and a per-finding payload on
every row would multiply the list's cost to print and to read, for a screen that
draws one run at a time. `diagnose RUN --json` is where the console reads them.

**Every string from the run is redacted** (`Diagnosis`'s own comment), and the
console reads `row` and the keys above.

Flags: `--json` plus the cloud flags.

### 6.2 `fugaro logs RUN --json` and `fugaro logs RUN --url`

**Command:** `internal/cli/logs.go` `newLogsCmd` / `runLogs`. **Two argvs, and
they cannot be combined**: `runLogs` refuses `--url` with `--json` or
`--follow`.

- `logs RUN --json` prints **one compact `logLine` object per line** — not an
  indented document: `printLogEntry` marshals each entry with
  `json.Marshal` and writes it with a trailing newline, the same one-object-
  per-line shape as the `watch` feed, and the other of the two line-delimited
  documents the console reads. `logLine`: `time` (RFC 3339),
  `severity`, `stage`°, `stream`°, `event`°, `message` (no `omitempty` on
  time/severity/message). Every field is **redacted** (`redactEntry`,
  `redactEntry`), and `message` is job-written text: the display layer
  re-sanitizes it. **The console caps the window**: it reads a bounded number
  of lines with a timeout and never passes `--follow` (design §2.3, §5.1;
  Task 2.3 asserts the cap and that no spawn carries `--follow`).
- `logs RUN --url` prints **one line of plain text** — the Cloud console link
  (`row.log_url` above). It is a value the CLI produced rather than a job, and
  it still goes through the `https:`-and-allow-list check before
  `openExternal` sees it (design §5.1 rule 2).

### 6.3 `fugaro cancel RUN --json`

**Not on the console's allow-list** — `cancel` is a write (design §4.2) and v1
has no write path. It is recorded here because §7's gap list and the design's
allow-list "never" table both name it, and because its document is the shape a
future v1.1 button would consume. `cancelResult`:
`project`, `run`, `status`, `marker` bool, `hard` bool, `pr`°, `no_pr`° bool,
`lock_held`° bool. Its status constants (`cancelFinalized`, `cancelCancelled`,
`cancelNotLaunched`, `cancelLaunching`, `cancelUnfinalized`,
`cancelAlreadyFinished`) are the file's own.

**Its document is compact — one object per line**, the `logs RUN --json` shape
(§6.2) rather than the indented one every command in the console's set emits:
`printCancel` marshals the struct with `json.Marshal` and
writes it with a trailing newline. A v1.1 button that parsed it as an indented
document would read nothing; `cancel` is one of the three commands this file
describes that are line-delimited, and the only one outside the console's set
(§9.2).

### 6.4 `fugaro doctor --json`

**Command:** `internal/cli/doctor.go` `newDoctorCmd`, indented. **Document:**
`doctorOutput`: `ok` bool,
`checks` `[]doctorCheck`, `project`° (`doctorProject`), `fugaro_yaml`°
(`doctorFugaroYAML`), `secrets`° (**`[]secretEntry` — names and metadata only,
never a value**), `plugin` (`pluginwire.Report`), `release`° (`releaseInfo`:
the binary's tag and commit), `error`° (**why doctor could not run its checks —
`--json` prints an object on every outcome**, so the console always gets
parseable JSON).

`doctorCheck`: `id`, `ok` bool, `problem`°, `fix`°,
`severity`°. **The environment view renders `problem` and `fix` verbatim**, as
the CLI worded them — including the "not on the `PATH` the console gives the
CLI" class of finding the design's §3.4 rule 5 describes.

**Three check ids are new in CLI 0.10.0** (credentials-service C1.21), all
`severity: "warning"` — so all three fail `--strict` and none is an `ok: false`
about the installation: **`creds-broken`**, naming every Claude account the
database's `creds/marks` marks broken (the service saw a 401), with the fix
`fugaro accounts clear NAME`; **`creds-unknown-state`**, naming every account
whose mark carries a state this release does not read (a newer release's hold,
or one written by hand) with that state word — such a mark is held (`Usable`
fails closed) and `accounts clear` refuses it, so the remedy is
`fugaro upgrade`; and **`creds-rules`**, when the published policy
has the cascade switched on (`routing.claude.via: cascade`) and
`config/limits/credsRules` is missing, unreadable, hand-edited or of another
generation than the binary's. An unreadable `creds/` is an `info` check
instead, so "could not read" is never rendered as "nothing is broken"; that
note covers both of the marks findings, which are answered by one read of the
subtree.

**Flags** (`newDoctorCmd`): `--dir` (default `.`), `--plugin`, `--strict`,
`--json`, **`--check-github-app`** — the last of which reads the App's private
key out of Secret Manager and is therefore unreachable by the console by
argument list, not by command name (design §5.1's second table). The console
passes `--dir D` and **never `--strict`** (its failure mode is the checkout's
plugin wiring, not the environment the console draws).

### 6.5 `fugaro accounts ls --json`

**Command:** `internal/cli/accounts.go` `newAccountsLsCmd`, indented
(`printAccounts`). **Document:** `accountsListDoc`:
`repo` string, `accounts` `[]string`, `complete` bool,
`note`° (why selection failed), `states`° and `state_note`°. **`complete: false` means the project config
could not be selected — the screen renders `unavailable`, never "no
accounts"** (the type's own doc comment is the authority). Flags: `--repo`
(always passed explicitly), `--json`, plus the cloud flags. The **names** are
local and offline — "never calls Secret Manager" — but **the state column is
not**: when the selected project has a Firebase budget backend, the marks are
read from that database as the person (ADC, an admin's read of `creds/marks`),
which is what makes `states` a value rather than an assumption. **That read is
bounded — five seconds for the whole of it, one attempt per call, no retry**
(`accountsStateBudget`, CLI 0.10.0) — so a database that is unreachable rather
than refusing (a laptop offline, a firewall that drops) costs the screen
seconds, not the per-call timeout twice, and the command answers with the names
and a `state_note` naming the bound. A project with no backend, by contrast,
makes no call at all: it stays offline and answers with the names and a
`state_note` of its own.

**`states` (CLI 0.10.0, credentials-service C1.21)** is every listed account's
credential state, keyed by account name, and it is **absent exactly when no
mark could be read** — then `state_note` says why (no Firebase budget backend,
a database that is down, a read the person's rules refused). The two are one
answer: a reader must **not** render "every account is available" from an
absent `states`, because that is a claim about marks this command did not read.
Each value is `{"state": …}` plus `until`° — the state word
`internal/creds` stores, spelled as it stores it (`available`, `exhausted`,
`backed_off`, `broken`; a state a newer release wrote is passed through
verbatim rather than rounded to `available`), and, for an exhaustion, the
instant the hold lapses in RFC 3339. An account with no node, one whose node
says `available` and one whose mark an operator cleared all carry `available`:
they are the same credential (`creds.Mark`'s own doc). **Names and states only
— never a token, a secret ID or a prefix of one (F27).**

The text view of the same command is the plan's column — `available`,
`exhausted until T`, `backed off`, `broken` — and falls back to one bare name
per line when there is no state to show, which is the shape a console that
passes `--json` never sees.

### 6.5a `fugaro accounts clear NAME` — the operator's write

**Command:** `internal/cli/accounts.go` `newAccountsClearCmd`. It is the one
command in this document that **writes the budget database**, and it is the
reason §9 rule 1's "never" list must stay exhaustive: the console does not
spawn it. It writes exactly one node, `creds/marks/NAME`, with
`creds.Cleared()`, acting as the person (ADC, an admin of the database), and
the write is conditional (`creds.RTDBStore.UpdateMark` → `PutIfMatch`) so a
mark the service changed at the same moment is re-read rather than
overwritten: the callback clears the mark only while its state is still the one
the confirmation named, so a `broken` mark the service recorded while the
operator was reading the confirmation of an `exhausted` one is refused rather
than cleared (both are clearable, but they are two different holds). Flags: `--yes` (skips the typed confirmation of the project's
name), plus the cloud flags. It prints one line naming the account and the
state it cleared, and **no value of any kind**. A `broken` or `exhausted` mark
is clearable; a `backed_off` one is refused (a probe clears it, C1.12) and a
state this Fugaro does not know is refused rather than overwritten. `NAME` must
be `default`, a `claude_accounts` name of the selected local config, or a name
the database already carries a mark for; a name with none of the three is
refused, so a typo cannot leave a mark node behind. The third case is not an
edge: `claude_accounts` is per-checkout and never published (the shared config
strips it), while the marks belong to the project, so a mark can name an
account this checkout does not list — and §6.4's `creds-broken` finding names
exactly those marks, so its fix has to be runnable for every one of them. The
config read is the **local** one, never the shared one. The command refuses no
coding agent's session — it approves nothing and records no owner decision, and
its neighbours are `fugaro budget set`/`kill`, not `fugaro allow-delete` and
`fugaro managed flag` — but it does refuse `GODEBUG=http2debug` before it opens
anything, like every command that opens the cloud.


### 6.6 `fugaro secrets ls --json`

**Command:** `internal/cli/secrets.go` `newSecretsLsCmd`, indented
(`printSecrets`, `internal/cli/secrets.go`). **The document is a bare JSON array** — `enc.Encode(entries)`
in `printSecrets`, not an object with a wrapper key. Each element is
`secretEntry`: `name`° (the logical name, from the
`fugaro_secret` label), `scope`° (`repo` / `project` / a project secret with a
repo override), `allowed`° (`[]string`, `--project-wide` only), `missing`° (`true` on a name a job of the repository needs that has no enabled version — `--repo` only; such a row has an empty `id` and no `created`), and the
embedded **`gcp.SecretInfo`**, whose own
JSON keys are `id`, `created` (RFC 3339), `labels`°, `versions` (the count of
versions not destroyed) and `latest`° (the newest enabled version, if any) —
**names, labels and version counts only; `createTime` and `versionCount` are
the upstream Secret Manager API's names, not keys this document emits**. **No
value can appear**: the command "has no access call at all". Flags: `--repo`
**or** `--project-wide` (always one of them, passed explicitly — with neither
the command resolves the checkout's origin), `--json`, plus the cloud flags.

### 6.7 `fugaro version`

**Command:** `internal/cli/root.go` `newVersionCmd`, **`cobra.NoArgs` with no
`Flags()` at all**. It prints **one line of plain text** — the `Version`
variable (`Version`, `var Version = "dev"`), i.e. the release tag of a
built binary. **The subcommand has no `--json` and no flag of any kind**, which
is why the handshake is the one spawn that carries no `--project` (§8). The
root command does answer `--version` and `-v` now, printing the same line
through the same template (`internal/cli/root.go`'s `versionTemplate`), and a
leading one of those is the whole command — the argv boundary reduces it to the
flag before cobra resolves anything, so `fugaro --version run` prints the
version and runs nothing — but
**the handshake stays `fugaro version`**: the console's argv is pinned to the
subcommand every CLI version in its supported range has, while the root flag is
newer than some of them. The console parses the human line, pinned by a test
against the real output (design §4.3 gap 2).

### 6.8 `fugaro config list --json`

**Command:** `internal/cli/config_list.go` `newConfigListCmd`, a subcommand of
`fugaro config` (`internal/cli/configcmd.go`). **Flags:** `--json`. **It takes
no cloud flags** — `--project`, `--config`, `--gcp-project` and `--region` are
absent on purpose: this is a local read of the operator's own project configs,
it opens no bucket and it needs no installation selected, so it works outside a
checkout and with no config at all (an empty list).

**What it closes:** v1 design §4.3 gap 5 — the read D13 itself names, and the
one the installation picker waits on. It enumerates
`internal/localcfg/localcfg.go`'s `Projects(getenv)` and reads each one with
`LoadProject`, so the list is the CLI's own enumeration and the app still never
opens `~/.config/fugaro` (§3.4 rule 7).

**Who reads it.** Since the owner's design ruling of 2026-10-25 it is the
installation rail's read: the dashboard's `installation.list` operation (one of
the surface's twenty-five, `CONSOLE_OPERATIONS` in `src/main/ipc.ts`), answered by
the same dispatcher the picker window uses. The console reads two of the
`warnings` lines by their wording, to mark the rail's rows
(`src/main/installationstate.ts`): `<path> could not be read: <reason>`, matched
to an installation when the path before that marker ends in `projects/<name>.yaml`
(with `\` read as `/`, for native Windows; a path the reason mentions does not
count), and `<name>'s shared config was last checked <age> ago, over
the <window> the reader refreshes it within`. A change to either sentence in
`internal/cli/config_list.go` is a change to this contract. Every other line is
shown verbatim in the row's issue view and is not interpreted. A row whose `name` is not
the CLI's own project-name grammar (`config.ProjectNameRE`) is not used at all:
the console builds commands from the name, so it skips the row and adds a
warning line naming it.

The document:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `installations` | array | never | never | every project config, in the order `Projects` returns them; `[]` when there are none |
| `warnings` | array | never | never | one line per installation that could not be read; `[]` when there are none |

and one entry of `installations`:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `name` | string | never | never | the project name, i.e. the config file's own name |
| `gcp_project` | string | never | never | `""` when the config could not be read (the warning says why) |
| `region` | string | never | never | `""` when the config could not be read |
| `shared_merged` | bool | never | never | whether this machine holds the installation's published shared config |
| `shared_checked_at` | string | never | **yes** | RFC 3339 UTC; null exactly when `shared_merged` is false |
| `base_branch` | string | **yes** (an older CLI) | never | the installation's own recorded base branch — its project config's top-level `base_branch`; `""` when it records none |
| `checkouts` | array | **yes** (an older CLI) | never | the repositories of this project this machine remembers a checkout of, one entry per path; `[]` when none is remembered |

and one entry of `installations[].checkouts`:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `repository` | string | never | never | `<provider>/<owner>/<name>`, the identity the path was recorded under |
| `path` | string | never | never | the absolute path, made printable and bounded (control and format characters escaped, at most 200 runes) |
| `base_branch` | string | **yes** (an older CLI) | never | the recorded base branch of this repository — `repos.<owner/name>.base_branch` in the installation's project config; `""` when that entry is absent or records none |
| `enrolled` | bool | never | never | whether the installation's project config enrolls `repository` in its own `repos:` map — **a local fact**, false when that config could not be read |
| `valid` | bool | never | never | whether the path is still a checkout of this project and this repository **and that repository is enrolled**, as of this read |
| `reason` | string | **yes** | never | why it is not, one of `missing`, `not_a_checkout`, `unreadable`, `other_project`, `other_repository`, `not_enrolled`, `not_representable`; **absent exactly when `valid` is true** |

`enrolled` is reported separately from `valid` on purpose: it is the one part of
the verdict that is a fact of a **local file** rather than of the directory the
path names, it is the only part that is meaningful when the path itself is gone,
and a reader that wants to explain "this project does not own this repository"
must be able to say that without also claiming the directory is not a checkout.
It is **not** a cloud fact: it is the operator's own `repos:` map in
`projects/<name>.yaml`, read locally. The published allow-list is the cloud's
copy of the same decision (project policy publish seeds it from this map) and is
**not** read here — this command opens no bucket and needs no credentials.

**`base_branch` is on both rows because a launch is cut from a branch and the
window that confirms it shows that branch** (design §5.1 rule 11: "the branch
the window shows is the branch the run is cut from"). The CLI resolves that
branch through one merged order (`internal/cli/basebranch.go`'s
`launchBaseBranch`, `config.EffectiveBaseBranch`), whose **first** layer is the
working directory's checkout of the repository — and a console launch never has
one, because its working directory is the app's own empty directory. The two
layers a launch can both read and be cut from are the installation's top-level
`base_branch` and the repository's `repos.<owner/name>.base_branch` — the two
fields the tables above carry — so the document carries exactly those two and no
third: a
repository for which neither is recorded is one the console refuses to launch
from, with a refusal naming the repository and those two settings, and
`git.base_branch` in a checkout's `fugaro.yaml` is **never** the answer — an
operator can set it without moving this row, and a branch read from it would be
a branch the run is not cut from. Both are read off the machine's own project
config, like everything else here: the published policy node carries the same
project default for the runner and this command does not open a bucket to read
it. Neither field is about the path a row names — they are properties of the
installation and of the repository — and each is `""` rather than absent when
the config records none, because absent is this document's word for "an older
CLI" and a reader must keep the two apart.

**`checkouts` is what makes the first run zero-configuration** (v1 design D13
and §3.4 rule 7; console v2 plan V2.1.1): the installation list and the local
checkouts of each installation arrive in one document, so the picker and the
checkouts cannot disagree about which installation is which. The rows are
**local state and nothing else** — the paths are never published to the runs
bucket, never merged into the shared config, and never printed by any other
command. They come from a machine-local state file
(`$XDG_CONFIG_HOME/fugaro/state/checkouts.json`, in a directory of its own
beside the project configs and not among them: the `state` directory is created
0700 and the file 0600, and the operator's config home itself is never
chmodded), written best effort by the commands that resolve a checkout's
project and are **not** reads the console spawns — `fugaro validate`,
`fugaro run`, `fugaro update --repo` and the rest — bounded to 20 repositories
per project and 5 paths per repository, **and to 1000 rows across the whole
document**. A repository the project config does not enroll is **not
recorded at all**: an un-enrolled clone must not push a real repository out of
the per-project bound, and a row for it could only ever be `not_enrolled`. The
bounds are kept on the write and **applied again on this read**, because the
state file is one this process did not write: a hand-edited one, or one written
by a machine that has since cloned far more, is trimmed to the newest rows and
the trimming is named in `warnings` rather than presented as the whole file. **The reads §5.1 allows never record,
by the console's hand or the operator's**: `fugaro doctor` and
`fugaro accounts ls` resolve a checkout too, but a read writes nothing, and
the CLI cannot tell who spawned it, so the rule is by command path
(`consoleReadPaths`, `internal/cli/project.go`). A checkout only ever used
with `fugaro doctor` or `fugaro runs ls` is therefore never remembered, and
the console's list is shorter than the machine's directories: it is what the
write path has seen, not an inventory of the disk.

**Every path is re-validated on this read, and the read never writes.** A path
that has moved or been deleted, that is no longer a git checkout's own root,
whose `fugaro.yaml` names another project, or whose origin is another
repository is reported with `valid: false` and its `reason` — never dropped,
because the write path is what prunes and a directory that comes back is a
checkout again. A repository the project config does not enroll is
`not_enrolled`; a path that had to be escaped or truncated for printing, or
that is not absolute or does not carry a well-formed identity, is
`not_representable` — **the printed string names a different directory than the
stored one, so it must never be presented as a path that works** — and so is a
path under the machine's temporary directory, which this machine does not
record any more and does not read: a scratchpad a coding agent made is a live
checkout, and the row is a working directory the console must not be sent to. A
state file that cannot be read (not JSON, another version, over its size bound,
not a regular file) is a warning naming it and `checkouts: []` for every
installation — never a silent "there are none", and never a listing that fails.

**One row carries one reason, and which one it is is fixed.** In order:

1. `not_representable` — decided from the row's own strings, **before anything
   touches the disk**: the path as printed is not the path as stored, the path
   is not absolute, the identity is not a provider and an `owner/name`, or the
   path is under the machine's temporary directory. A row that fails here is
   never examined further, so a relative path that happens to exist under the
   reader's working directory is still `not_representable` and not a valid
   checkout, and a path under the temporary directory is `not_representable`
   whether it is there or not, and whatever it is a checkout of.
2. `missing` — the path is not a directory that exists.
3. `unreadable` — it is there, and its `fugaro.yaml` cannot be read.
4. `not_a_checkout` — it is not a git checkout's own root.
5. `other_project` — its `fugaro.yaml` names another project (or none).
6. `other_repository` — its origin is another repository than the row's, or an
   origin git config rewrites.
7. `not_enrolled` — everything above holds and the project does not enroll the
   repository.

The order is part of the contract because the console renders the reason: a row
is reported as the *first* thing wrong with it, and `not_enrolled` — the last
of the seven — is therefore what a row gets when nothing else is.

**What a reader may do with a row — one rule, and the whole rule.** A row
carrying `valid: true` is a string from this document and the absolute path of
a git checkout this machine has, of this project and this repository, which the
project enrolls. The console may use such a path for exactly one thing: **as
the working directory of a read-only CLI command from §5.1's exact-argv
allow-list**, spawned with the path as a directory and nothing else. It may
show it to the operator, and it may offer it as a pre-fill the operator
confirms. It may **not** join, open, stat, copy, delete or execute anything at
that path itself, may not pass the string as a flag or an argument to any other
command, and may not treat it as a working directory for anything that writes.
**A row with `valid: false` — whatever its `reason` — is never used for
anything, not even as a working directory**: it is where a checkout was, or a
path this document cannot spell, not where work can be done. This is §3.4 rule
7, and it is what the two halves above mean together: the CLI re-validates
because it, and not the app, is the thing allowed to look at the disk.

**`<provider>` in `repository` is the one the origin's host gives, never the
one the checkout's own `fugaro.yaml` claims.** A checkout is remembered and
re-validated under the provider whose host its git origin is on (`github.com` →
`github`, `bitbucket.org` → `bitbucket`), so a `fugaro.yaml` that says
`bitbucket` over a `github.com` origin appears as `github/owner/name` — a
copied config or a re-pointed checkout is not a `bitbucket/...` repository that
no provider serves. **The file's `git.provider` is never used to name a row,
not even for a host no provider lives on** — a GitHub Enterprise or
self-hosted Bitbucket box — because that file is written by the checkout: a
clone on an arbitrary host (`https://git.evil.example/acme/app.git`, with
`git.provider: github` and `project: aurora` in its `fugaro.yaml`) would
otherwise be recorded, and re-validated, as the enrolled `github/acme/app`,
handing the console a valid path for a repository it does not own. **A host no
provider lives on is therefore recorded as nothing at all**, and a row an older
fugaro left for one is reported `other_repository` rather than valid. This is the rule a repository is *known* by (a provider
knows a repository only when that provider's host is its origin's host), and it
is what makes the pair safe to build a provider-host link from (design §5.1
rule 2): a link built from the file's claim would point at the wrong host, on a
name the operator does not own. The run path holds the two sources to agreement
and fails the run when they disagree (`docs/git-providers.md`); a checkout is
remembered best effort, where the origin is the source that decides.

**The rows are strings off this machine's disk**, so every one of them is
escaped and bounded like the rest of this contract: a repository identity or a
path carrying a control character, a bidi override or 400 bytes of padding
arrives escaped and marked. **Such a row is `not_representable` and `valid:
false`**: the escaping and the truncation are for printing, and the string that
prints is not the string the checkpoint was recorded at, so it names a
different directory than the one on disk. A reader renders every row as text
and nothing else; what it may *use* is the single rule above, and it applies to
`valid: true` rows only (§3.4 rule 7).

**An installation whose config cannot be read is still listed, by name, with a
warning** — a picker that offered a name and then failed silently is worse than
one that says what it could not read. **Its `checkouts` are listed anyway, and
every one of them is `enrolled: false` and `valid: false`** — enrolment is a
fact of that very file, and a file that cannot be read enrolls nothing — **but
the `reason` is the row's own, not `not_enrolled`**: the reason is the first
thing the read finds wrong with the row, in the fixed order above, so a path
that is gone is `missing` and a row whose path is sound is `not_enrolled`. The
two fields answer two questions,
and a reader that wants "this project does not own this repository" reads
`enrolled: false` — which is true of every row here — rather than the reason.
Which directory holds this project's checkout is a fact of this machine, not of
the file that could not be parsed, so the row — the repository and the path —
is still there to show the operator. The reader fails closed: no row of such an
installation may be used for anything.

**Two writers racing lose one of the two updates.** The state file is written
by a load-modify-write that is not locked, under a rename: a reader therefore
never sees half a file — the file is whole or it is the previous one — but a
pair of concurrent writes can lose one of them. Nothing is corrupted and
nothing is corrupted by it: the loser's checkout is recorded again the next
time a command runs in it. This is why the field is a convenience for the first
run and not an inventory: **a reader must not treat a missing row as "this
machine does not have that checkout"**.

**The document is a named set of fields and it is deliberately not a marshalled
`localcfg.Config`.** That struct carries the operator's providers and their base
URLs, the named Claude accounts, the reserved project secrets, the Terraform
block and the whole repository list — none of which the picker needs, and all of
which would join this document the day a field is added to the struct. A test
fails if a field outside the six above reaches the output, and if either type
ever becomes (or embeds) `localcfg.Config`.

**`checkouts` is optional to the reader.** The per-installation `checkouts` array
(PR #735) is newer than the command, and **the console treats it as optional**:
absent, null and a malformed entry are each an empty list rather than a refusal,
because a console that refused a document over a field it only *offers* would stop
listing installations on the day the field is added. Where it is present the
console offers the `path` of each entry carrying `valid: true` **and**
`enrolled: true` (an absolute path; a `valid: false` entry is never offered,
whatever its `reason`, and neither is a row this installation's own config does
not enroll) — **one entry per repository**, because a repository with several
remembered directories would otherwise fill a menu and hide the others — and
nothing else: a directory the document never
named is refused by name, so the list is the CLI's own enumeration even at the
point of use. An installation listed with no `checkouts` is drawn with no way to
set one, which is the truthful state — the console does not invent a directory.
A CLI that emits no `checkouts` key at all is a CLI that does not know about
checkouts, while an empty array means this CLI knows and this machine remembers
none: the two are not the same fact, even though both leave the console nothing to
offer. There is no version field to bump: this document is read whole, and the
key's absence is the signal.

**Version skew.** The command is new, so an older CLI does not have it at all: the
spawn fails (or the CLI answers with its own `unknown command` sentence), and **the
console draws the rows its own store holds beside the CLI's refusal** — an
installation the operator already chose does not disappear because today's binary
cannot enumerate it — with the CLI's sentence on the screen and **no** onboarding
screen, since this is not a first run. The handshake (§6.7) is what tells the
console which case it is in. The install-CLI onboarding screen is drawn only when no
`fugaro` could be *resolved* (rule 4), which is a different answer.

### 6.9 `fugaro plan graph --json`

**Command:** `internal/cli/plan.go` `newPlanGraphCmd`, the only subcommand of
`fugaro plan`. **Flags:** `--plan <path>` (the plan document the graph sits
beside; with none, the one graph artifact under `docs/plans` is read, and more
than one is refused and listed rather than picked), `--done <ids>`
(comma-separated or repeated: the tasks whose work is done, which is what
"ready" is computed against — see below), `--json`. **It takes no cloud
flags**, for the same reason as §6.8: it reads a file in the checkout.

**What it reads:** the plan-side graph artifact (v2 design D25 and F6/F33),
`<plan>.graph.json` beside `<plan>.md`, parsed and validated by
`internal/plan/`. The console never opens the file — it spawns this command and
reads the document, like every other figure (D1, §3.4 rule 7).

The document:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `schema_version` | number | never | never | `1`, `2` or `3` today; a document carrying another number is **refused**, not rendered |
| `plan` | string | never | never | the plan the artifact came from, as the artifact names it |
| `stream` | string | **yes** | **yes** | the release stream the plan ships in: `cli`, `console` or `mobile` (GD7). Absent, or null, when the plan does not say — and **refused** when it names another. A **schema-2** field, so a v1 artifact carrying one is refused rather than read |
| `nodes` | array | never | never | the tasks; `[]` is a plan with no tasks yet |
| `edges` | array | never | never | the dependencies; `[]` is a plan with no dependencies yet |
| `decoupling` | array | **yes** | never | one entry per edge the contract-first decoupling pass broke, below. A **schema-3** field, absent when the planner broke nothing — so a v1 or v2 artifact, and a v3 one whose plan kept every edge, prints no such key. Every entry is checked against the graph: the contract is a `contract` node, the integration is an `integration` node that depends on both endpoints of the edge the entry records, and that edge is **not** still in `edges` |
| `contract_changes` | array | **yes** | never | the change requests raised against contracts that have already merged, below. A **schema-3** field, absent when there are none |
| `levels` | number | never | never | how many levels the plan has — the longest chain of dependencies plus one; `0` for a plan with no tasks |
| `critical_path` | object | never | never | the longest chain of tasks in the plan, below |
| `bottleneck` | string | never | **yes** | the task the critical path runs through; null only when the plan has no tasks |

one entry of `nodes`:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `id` | string | never | never | the planner's own name for the task; unique in the document |
| `title` | string | never | never | what a person reads |
| `run` | string | never | **yes** | the run this task belongs to; null when it belongs to none |
| `version` | string | never | **yes** | the target version; null when the plan does not say. In a **v2** artifact it is `MAJOR.MINOR` or `MAJOR.MINOR.PATCH` — `0.10`, or the patch box `0.10.1` a release such as 0.10.1 is — and anything else is **refused**; a v1 artifact's version is free text and is read as written |
| `footprint` | object | never | never | the expected paths, below |
| `kind` | string | **yes** | never | what this node is: `task` (the default, and every node of a v1 or v2 artifact), `contract`, `integration` or `contract_change`. A **schema-3** field, and a value outside those four is **refused**. A `contract` node carries the stubs and the literal files the graph holds every other task against; an `integration` node is the one that depends on both sides of a broken edge |
| `size` | number | **yes** | **yes** | the task's weight relative to the others, greater than `0`; absent or null means `1`. A **schema-3** field, and the figure the accepted/refused comparison of a break is weighed with rather than a task count. `0` or a negative number is **refused** |
| `stubs` | array of strings | **yes** | never | the whole files a `contract` node leaves for its dependents to compile against, each a literal repository-relative path — never a glob, never absolute, never with a `..` element, and each one named **literally** in that node's own `footprint.paths`. A **schema-3** field, on a `contract` node and nowhere else; anything else is **refused** |
| `level` | number | never | never | the longest chain of dependencies *ending* at this task: `0` for a task that waits on nothing, otherwise one more than the deepest task it waits for |
| `done` | bool | never | never | the task is in the `--done` set the read was given. **A fact the caller stated, not one the CLI derived**: a finished run is not the same claim as a done task (see below) |
| `ready` | bool | never | never | the task is not done and **every task it needs is done**. This is the field a launch may act on (design F7) |
| `blocked` | bool | never | never | the task is not done and at least one task it needs is not done |
| `critical_path_length` | number | never | never | the longest chain of tasks *starting* at this one, counting it: `1` for a task nothing waits on |
| `downstream` | number | never | never | how many tasks wait on this one, directly or through others; the raw count D26 keeps as the cheap fallback beside the critical path |

**A task is done, ready or blocked — exactly one of the three, always.** The
three are fields rather than a rule a screen applies, so two screens colour one
node the same way; `ready` and `blocked` are both false exactly when `done` is
true.

the `critical_path` object:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `length` | number | never | never | the chain's length **in tasks**; `0` for a plan with no tasks |
| `tasks` | array | never | never | the tasks along the chain, in the order they can be done; `[]` for a plan with no tasks. Ties are broken by id, so one graph has one critical path |

one entry of `edges`:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `task` | string | never | never | the node that depends |
| `needs` | string | never | never | the node it depends on |
| `reason` | string | never | never | why; a document with an edge and no reason is refused |

and the `footprint` object:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `prediction` | bool | never | never | **always `true`** — a footprint is a prediction, and the label travels with the data (D28) |
| `paths` | array | never | never | glob patterns matched with doublestar; `[]` is a task that predicts it touches nothing |

one entry of `decoupling` (schema 3):

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `contract` | string | never | never | the id of the `contract` node the edge was broken through |
| `was` | object | never | never | the edge as it stood before the pass broke it, below |
| `integration` | string | never | never | the id of the `integration` node that depends on **both** endpoints of `was`, and one contract's entries all name the same one |
| `reason` | string | never | never | why the break is right; an entry with no reason is refused |

the `was` object:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `task` | string | never | never | the node that depended |
| `needs` | string | never | never | the node it depended on. The pair must **not** still be an entry of `edges`: an edge that is both broken and there is a document disagreeing with itself |

one entry of `contract_changes` (schema 3):

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `change` | string | never | never | the id of the `contract_change` node that carries the work |
| `contract` | string | never | never | the id of the `contract` node the change is against |
| `raised_by` | string | never | never | the id of the node that found the change |
| `reason` | string | never | never | what has to change and why; an entry with no reason is refused |
| `affects` | array of strings | never | never | the ids of the tasks the change reaches, each a node of this graph |

**Four refusals, and they are refusals rather than partial renders:** a document
from a schema version this build does not read (the message names both
versions), a malformed one, one whose edges name a node that is not there or
whose ids repeat, and **one with a cycle** — every "ready" and "blocked" answer
is a function of the graph, so a cyclic graph has none to give. A refused graph
is no document at all: the command exits non-zero and prints nothing to stdout,
so a renderer never has half a graph to draw.

Schema 3 refuses the same way over what it added, because it is untrusted plan
text: a `kind` outside the four, a `size` of `0` or less, a `stub` that is a
glob or that the contract's own footprint does not name, a `contract` node whose
footprint names no literal file, a `decoupling` entry whose integration node does
not depend on both sides of the edge it records, an edge that is recorded as
broken and is still in `edges`, a `contract_change` naming a node that is not
there, and a footprint that covers a merged contract's file from outside the
contract and the change requests against it — **however that path is spelled**,
because `./internal/contract/types.go`, `internal//contract/types.go` and
`internal/dep/../contract/types.go` all name the one file, and a rule that read
only the literal spelling would be a rule a respelling steps around. And a
footprint **pattern** that is not a checkout path at all is refused by name, so
it can never be read as the contract's file: a **backslash** — doublestar's
escape character, where `types\.go` is a pattern for `types.go` — and a **leading
slash**, which makes the pattern absolute. The console shows the CLI's sentence
in each case, like every other refusal here.

**What the read computes, and why the done set is passed in.** `level`, `done`,
`ready`, `blocked`, `critical_path_length`, `downstream`, `levels`,
`critical_path` and `bottleneck` are **not in the artifact**: the planner cannot
know them, and a planner that guessed them would be a second answer to a
question the CLI already answers (design F7). They are computed in Go, beside
the read, so two consoles reading the same graph and the same facts get the same
answer — which is the whole point of "ready" being a field rather than a screen's
rule, since ready is also what a launch is allowed to act on.

The one thing the read cannot compute for itself is **which tasks are done**, so
`--done` states it. A task is done when its work is done; a *run* finishing is a
different claim — a run can finish failed, or finish with a pull request nobody
has reviewed — and this read is local (it takes no cloud flag and opens no
bucket), so it never sees a run's state in the first place. The definition of
readiness is the CLI's; the fact is the caller's, and the document echoes it per
task in `done`, so a renderer never has to remember what it passed. An id that is
not a task of the graph, or an empty one, is **refused rather than ignored**:
an unknown id would otherwise read exactly like a task nobody has done.

**The footprint is advisory and it is a prediction; the measured set is a
different document.** What a task *expected* to touch is this field; what a run
*did* touch is the run's own record (D27), written after the fact. The console
labels the first as a prediction wherever it shows it, and the label is in the
document rather than in the renderer.

**Version skew.** Both the command and the document's `schema_version` answer
this, and they answer different halves: an older CLI has no `plan graph` at all,
and a newer *planner* can write a `schema_version` the CLI refuses. The console
shows the CLI's refusal sentence rather than a graph.

**Three schema versions are read, and each older one is not a second dialect.**
Schema **2** added the document's `stream` and the shape a node's `version` has;
schema **3** added the node's `kind`, `size` and `stubs`, the document's
`decoupling` and `contract_changes`, and the rules that hold them together.
Schema **1** is a plan written before any of them, and it stays a document this
build renders — its `version` is the planner's own words and is read as written,
because the shape is the newer schema's promise rather than a retroactive one. A
v1 artifact carrying a `stream`, a v1 or v2 artifact carrying any schema-3 field,
or a v2 artifact whose `version` is not `MAJOR.MINOR[.PATCH]`, is **refused**
rather than read with the difference dropped: a file and a read that disagree
about which stream a plan ships in is worse than a refusal. The `version` shape
holds at **every** version from 2 on, so a v3 graph's versions are checked too.
A **4** is a graph from a newer planner, and it is the refusal the version
number exists for.

**The computed fields are additive, and they are not the artifact's.** They are
fields of the *read's* document and not of the artifact: the artifact carries the
field set above and nothing else — the nine computed keys are not among them, and
a planner that wrote one would be refused by name — so `--done` is a flag on the
read rather than a field a planner writes. A console built before
these keys existed ignores them and draws the graph it drew before; a console
built after them must not require them of a CLI that predates them — an older
CLI's document has no `level`, no `ready` and no `critical_path`, and that
absence is the signal (the availability rule, §1), not an empty graph.

### 6.10 `fugaro runs stats --json` (**CLI 0.9.0**)

**Command:** `internal/cli/runs_stats.go` `newRunsStatsCmd` / `runRunsStats`.
**Document:** `userStatsDoc`, indented (`writeUserStats`), both in
`internal/cli/runs_stats.go` — cited by symbol, not by line: an indented
document is written by one function and a line number here rots at the next
insertion above it. **The console spawns it** — design §5.1 rule 6's
`runs stats --json` row, the console's `runs.stats` operation — as the data
behind the Spend screen's by-person view, and the console passes `--since`
and nothing else. **`--all-users` is not on the spawn list**, which is what
keeps the view the CLI resolved out of a screen's hands: an operator of the
installation sees every user's rows, a plain launcher sees their own row
alone, and an identity the installation's own lists do not place stays
private. It is read-only —
the figures come from the run records (`launch.json`'s `launched_by`, and
`result.json`'s outcome and settled cost), never from the budget database, so
there is no write path behind it and no counter a screen has to reconcile.

| Field | Type | omitempty | Availability state |
|---|---|---|---|
| `project` | string | no | the Fugaro project, the marker's name |
| `by` | string | no | the breakdown asked for: `"user"`, the only value the command publishes |
| `view` | string | no | `"all"` or `"own"`: which rows the caller is shown. **The ruling, not a preference** — an operator of the installation (the identity the local config's `user:`/git names, listed in its `terraform.operators`) sees `"all"`; a plain launcher gets `"own"` unless they pass `--all-users`; an identity the lists do not place stays `"own"`. `"own"` with `rows: []` is a real state, not a failure: see the note below |
| `you` | string | no | the identity the view was resolved for, in the same domain a row's key is. `""` is *unavailable*, not anonymous: the CLI could not tell who the caller is — no identity at all, or one with nothing printable in it — which `--all-users` gets past. It is `"unknown"` only when the caller's own identity literally says so, never as a stand-in for "no key of its own", because that word is a group of runs rather than a person. A screen must not read `you` as a row it can always find in `rows` (see below) |
| `operator` | bool | no | whether the installation's own lists place `you` as an operator. A screen must not render `"all"` as "you are an operator": `--all-users` produces `"all"` with `operator: false` |
| `since` | string | no | the window's first UTC day (`YYYY-MM-DD`); `""` (unbounded) for `--since 0` |
| `until` | string | no | the window's last UTC day. Both are UTC days, like `runs ls`'s window |
| `max_runs` | int | no | the bound the read ran under (`--max-runs`, default 5000) |
| `truncated` | bool | no | **the window held more runs than `max_runs`**: every figure is over its newest `max_runs` alone. The bound is on the read and not on a person: it is applied before the view is, so a private view can be over fewer of the caller's own runs than `max_runs` suggests. A console must show this the way it shows a gap, never as the window's own total |
| `rows` | `[]userstats.Row` | no | `[]` when none. One row per (user, currency) — dollars are never added across currencies |
| `totals` | `[]userstats.Row` | no | one per currency, in currency order, with `user` `""` |
| `unlaunched` | int | no | runs in the window with neither a launch nor a record (never launched, cancelled before launch, or a stale claim). They never started: they are in no row and no total. It is the window's count, over everyone's runs, so it is **not** the caller's own in the `"own"` view any more than `truncated` is |
| `notes` | `[]string` | no | the lines the human table prints before and after its own: whose numbers these are, what the money counts, and what the read left out. The document and the table say the same things |

**The `"own"` view filters a read that is not its own.** It holds the runs
whose `launch.json` recorded the caller's own identity — the recorded text
itself, `launch.json`'s `launched_by`, the identity `fugaro run` records at
launch (not the `requested_by` that `runs ls --mine` compares) — rather than
every run under the key `you` names. The two differ when a key is shared: identities
that sanitise to the same text are one row, and the other person's runs are
not the caller's own, so a row in `"own"` can be smaller than the same key's
row with `--all-users`. The row a private view does show is keyed by `you`,
since every run in it recorded the caller's identity and that identity's key
is `you`.

`truncated` and `unlaunched` are the read's, computed over everyone's runs in
the window before the caller's row is picked out of it — they are counts of
runs, and a run that never launched has no launcher to attribute. So in
`"own"` a screen reads them as facts about the window, not about the person: a
truncated private view may cover fewer of the caller's runs than `max_runs`,
and `unlaunched` is not a count of runs they started. `"own"` with `rows: []` is
likewise a state of its own, not an error: a window the caller started nothing
in, or a caller whose identity has no key of its own — nothing printable in it,
or a text that reads as `"unknown"` itself. The unattributed row is every run
of the window that recorded no launcher, so it is never shown as a person's
own; the human output says so in a note, and `--all-users` still shows it with
everyone else's.

An identity that cannot be resolved **at all** — the local config's `user:`
empty and `git config user.email` answering nothing — is neither of those: the
command is **refused** instead, with no document at all and an error naming
`--all-users`, the one way to the full table that never needed to know who the
caller is. There is no `"own"` document to describe for it, which is why the
refusal is stated here rather than as a third state of `rows`.

`userstats.Row` is the group shape
behind **both** `rows` and `totals`; the counts are an embedded `Counts`
(`Counts`), so they are flattened into the same object on the wire.
**The counts add up**, which is how a screen reads the shape:
`runs == succeeded + failed + halted + infra_error + other + no_record`.

| Field | Type | omitempty | Note |
|---|---|---|---|
| `user` | string | no | the launcher the run's own `launch.json` recorded, sanitised to one printable line and capped at 128 runes; `"unknown"` (report's `budget.UnknownPerson` spelling) when the run recorded none — never guessed from the task, the record or the account it ran under. **Untrusted display text**: a service account or a CI identity is a row like a person's, and nothing is authorised by this string. **Two identities that sanitise to the same text share a row**: the 128-rune cap is one way to the collision and characters the sanitising drops (`alice` and `alice` + U+200B) the other, so a row is a key rather than a person. The `"own"` view does not follow the key — see the note below |
| `currency` | string | no | the unit the money columns are in; `"USD"` today (`CurrencyUSD`) |
| `runs` | int | no | every run of the group in the window, averaged or not |
| `succeeded`, `failed`, `halted`, `infra_error` | int | no | the four settled statuses, spelled as `result.json` spells them |
| `other` | int | no | a record that settled as none of the four: still running, or cancelled |
| `no_record` | int | no | **no usable `result.json`**: not finalised yet, or the object cannot be read. Its status and its money are unknown — never a measured `0` |
| `counted_usd` | float64 | no | the group's **model** dollars that count toward the caps: the money part of a run's model figure, `model_usd` minus `model_notional_usd` (C1.16/P12 — **not** a reading of `model_basis`, which the CLI's own `runs stats` help spells out). Compute is attributed to nobody, here or in the budget database's own person view |
| `notional_usd` | float64 | no | the part of the group's model figure a **subscription** paid: the whole figure of a run on `agent.auth oauth`, and the tier-A dollars of a cascade run, whose basis is `api-list` like any api-key run's. **Notional** — counted toward no cap and nobody's bill, kept apart from `counted_usd` by construction and never summed with it. The basis is the fallback for a record written before the breakdown existed |
| `counted_runs` | int | no | the sample `avg_counted_usd` is over: the runs whose record settled a real-dollar cost. Published because an average without its sample is a figure a reader cannot weigh |
| `avg_counted_usd` | float64 | no | `counted_usd / counted_runs`, rounded to the cent; `0` with `counted_runs` `0`, which is *unavailable* ("no run to average"), not `$0.00 a run` |

**Flags** (`newRunsStatsCmd`): `--by`, `--repo`, `--all`, `--since`,
`--max-runs`, `--all-users`, `--json`, plus the cloud flags.

### 6.11 `fugaro backlog list --json` (**CLI 0.11.0**)

**Command:** `internal/cli/backlog.go` `newBacklogListCmd` / `runBacklogList`.
**Document:** `backlogListDoc`, indented (`runBacklogList`), both in
`internal/cli/backlog.go` — cited by symbol, not by line: an indented document
is written by one function and a line number here rots at the next insertion
above it. This is the second command in this document the console **does not
spawn yet**: it is the data behind the backlog screen (CLI 0.11.0), and the
section is here so the screen that follows has a shape read off the code rather
than assumed from a design.

It is read-only, and in the strongest sense this document uses: the rows come
from the Markdown item files under the folder `--dir` names (`docs/backlog/` by
default), never from a network, a database, a credential or an installation,
and `list` **writes nothing at all**. The command that writes is
`fugaro backlog index`, and it is not in the console's set: it rewrites the
folder's own `README.md`, which is derived state a pull request regenerates
rather than merges (rigor modes D26–D28).

| Field | Type | omitempty | Availability state |
|---|---|---|---|
| `dir` | string | no | the folder the items were read from, exactly as `--dir` named it (`docs/backlog` by default). The document echoes the argument rather than resolving it, so a screen can show which folder it is looking at |
| `items` | `[]backlogRow` | no | `[]` when the folder holds no item. One row per item, in the index's own order — sorted by `id`, which is to say by the day the item was filed |
| `open` | int | no | the open items' count: the first figure of the index's count line |
| `closed` | int | no | the closed items' count: the second figure |
| `dropped` | int | no | the dropped items' count: the third figure. The three are the index's own figures, so a screen's totals and the Markdown's count line cannot disagree |

`backlogRow` is one item as the index lists it. The names are the index's own
columns, so the Markdown and the document cannot drift apart about what a row
says.

| Field | Type | omitempty | Note |
|---|---|---|---|
| `id` | string | no | the item's id: the day it was filed (`YYYYMMDD`) and a two-digit sequence that day, so two pull requests filing at once mint two ids rather than one conflict. It is also the first two segments of the file's name, so an id resolves to a file without a lookup |
| `title` | string | no | the filer's own words, one line. **Untrusted display text**: a filer writes it, nothing is authorised by it, and a screen renders it as text |
| `mode` | string | no | the resolved mode the filing run ran under; `"none"` for an item a person filed by hand, which is not a mode the CLI knows — it is the absence of one |
| `area` | string | no | the matched areas, strictest first, comma-separated; `"project"` or `"repo"` for an item about a whole layer |
| `status` | string | no | `"open"`, `"closed"` or `"dropped"` — the three words the format allows and no fourth |
| `filed_by` | string | no | the filing run's launcher, or the login of the person who filed it by hand. **Untrusted display text**, like `title` |
| `filed_in` | string | no | `"run:<run id>"`, `"pr:<number>"` or `"hand"`. The third is a person's filing, which has no run and no recorded pull request behind it and says so rather than spelling a run id nobody minted |
| `date` | string | no | the item's own `date` field, a UTC day (`YYYY-MM-DD`), never a timestamp and never a clock read at render time — so the same folder renders to the same bytes |
| `path` | string | no | the file the row was read from, `<dir>/<id>-<slug>.md`. It is a path in the checkout the CLI was run in, not a URL: a screen must not link it as one |

**Flags** (`newBacklogListCmd`): `--dir`, `--json`.

### 6.12 `fugaro plan status --json` (**the CLI release after 0.11.4**)

**Command:** `internal/cli/plan_status.go` `newPlanStatusCmd` / `runPlanStatus`.
**Document:** `planStatusDoc`, indented (`printPlanStatus`), both in
`internal/cli/plan_status.go` — cited by symbol, not by line, for §6.11's
reason. This is the third command in this document the console did not spawn
when the section was written, and **the console spawns it now**: plan GV2.1 put
it on the surface (`plan.status` in `console/src/main/ipc.ts`), as the data
behind the graph screen's per-task state (console v2 plan GV1.6). The section
stays here for §6.11's reason — the screen reads a shape off the code rather
than assuming one from a design. Its argv is design §5.1 rule 6's
`plan status --json` row: at most `--all` (GD6's "everything" toggle), plus
`--project <name>` and `--config <path>` like every other cloud row, and **no
path** — the checkout reaches the CLI as the spawn's own working directory, from
the installation's record and never from the renderer. The console asks for it
at most once a minute per installation and arguments.

**It is a cloud read, unlike `plan graph`.** A task's state is a *run's* state,
and a run lives in the runs bucket of the selected project — so this command
carries `--project`, `--config`, `--gcp-project` and `--region` like `runs ls`,
and the console must pass `--project <name>` on every spawn (§8). What it reads
is the checkout's plans joined to **the checkout's own repository's** runs: the
plans it prints are the working directory's, and a run of another repository
naming the same task is not theirs to claim. It writes nothing — the remembered
checkouts included: `plan status` is one of the console's reads
(`consoleReadPaths`), so resolving the checkout records nothing.

**What it reads is bounded, and the lookups are bounded more tightly, because
they are what a console pays for.** The runs it lists are every run the bucket
still holds — the ninety-day window `fugaro run --pr` reads with, which is the
bucket's own lifecycle, since a run older than that has been deleted with its
run and can join nothing. Every run in that window is read. What is scoped is
the merge lookup below: it spawns the operator's `gh`, one spawn per distinct
pull request, and it is asked only about the pull requests of the runs that
**join at least one plan here**, at most **32** per read, the newest runs'
first; a pull request past the cap has no answer (`merge unknown`, and a
warning says how many). The answers are not kept between invocations (that
cache lives in `internal/watch`'s process, and this command is one process). A
repository whose history is full of runs naming no task of any plan in this
checkout spends no lookup on them.

The document:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `repo` | string | never | never | the checkout's own repository, `owner/name`, taken from its `origin`; this is what scopes the join. **`""` means unknown** — the checkout has no readable origin — and then no run is joined at all, which is why an empty string is never "every repository" |
| `plans` | array | never | never | one entry per graph artifact found, in path order; `[]` when the checkout has no plan. An artifact whose id another one already took is not here — it is a warning instead (below) |
| `totals` | object | never | never | every plan's figures added up, below. A header line a screen draws before the plans |
| `streams` | array | never | never | the release streams catalogue, in the file's own order, below; `[]` when the catalogue could not be read (`warnings` says why, and a release's name and version are then simply absent) |
| `warnings` | array | never | never | the lines a human read prints before its table, below; `[]` when there is nothing to say |

one entry of `plans`:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `id` | string | never | never | the join's plan id, derived from where the artifact sits: the artifact's directory under `docs/` — `docs/setup-wizard/implementation-plan.graph.json` is `setup-wizard` — or, for `docs/plans/`, the file's stem: that one is `plans/2026-10-15-desktop-console-v2`. It is the half of `plan_task` before the slash |
| `artifact` | string | never | never | the file the graph was read from, relative to the working directory. A checkout path, not a URL: a screen must not link it as one |
| `plan` | string | never | never | the plan the artifact names for itself, which is not the id: the id is a path and this is the planner's own words. **Untrusted display text** |
| `stream` | string | **yes** | **yes** | the stream the plan ships in (`cli`, `console`, `mobile`), as §6.9's artifact carries it — absent or null when the plan does not say |
| `nodes` | array | never | never | the tasks, below. **Merged and cancelled tasks are not here unless `--all`**, and the three figures below count them either way |
| `releases` | array | never | never | one entry per target version the graph names, oldest first, below; `[]` when no task carries a version |
| `tasks` | number | never | never | how many tasks the plan carries, listed or collapsed |
| `closed` | number | never | never | the tasks a screen collapses by default: **merged** (`done` with a confirmed merge) and `cancelled`. A task whose merge is unknown is **not** closed — the screen keeps asking about it |
| `open` | number | never | never | `tasks - closed`. The three are stated here rather than left to a renderer to add up: `nodes` is filtered by `--all`, and a header drawn from it would read differently in the two reads |

one entry of `nodes`:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `id` | string | never | never | the task's id in the graph, which is the half of `plan_task` after the slash |
| `title` | string | never | never | what a person reads. **Untrusted display text** (repository content) |
| `state` | string | never | never | one of the six words below. **The whole answer**, so a screen colours a node from this and not from a rule of its own |
| `closed` | bool | never | never | this task is one of the plan's `closed` |
| `level` | number | never | never | the same field §6.9's node carries |
| `critical` | bool | never | never | the task is on the plan's critical path (§6.9's `critical_path.tasks`), so a screen can highlight the chain rather than recompute it |
| `needs` | array of strings | **yes** | never | the ids of the tasks this task needs, from the plan's own edges (`edges[]`'s `task`/`needs` pairs of §6.9), **sorted by id and de-duplicated**; absent when the task needs nothing. It lists every needed task **whether or not that task is in `nodes` in this read** — a closed task is filtered out without `--all` — because it is the graph's edge and not a statement about the read. **The graph screen's edges come from here**, so this is the field a cross-release or a critical-path edge is drawn from |
| `release` | string | **yes** | never | the target version, `MAJOR.MINOR` or `MAJOR.MINOR.PATCH`, as the artifact spells it; absent when the task names none |
| `run` | string | **yes** | never | the **suffix** of the run this task is joined to — the four characters after the last dash of a run id, which is the part a person reads out loud, and the short form `fugaro watch` shows a run by (`internal/task`'s `ShortRun`; where two shown rows collide it is longer, and `DisplayRuns` extends it); `runs ls` prints the whole id |
| `stage` | string | **yes** | never | the stage that run is in. **Job-written display text**: it arrives sanitised (`safetext.Field`) and is rendered as text |
| `pr_number` | number | **yes** | never | the pull request number this task shows, **whenever the run has recorded one** — running or finished. It is the run's `record_pr` (F9) when it has one, the run's own number otherwise, and it is absent rather than `0` when there is none: a repository that numbers its pull requests from 1 makes `0` no number at all. It is never a link (§5.1 rule 2) |
| `mark` | string | **yes** | never | `"merge unknown"`, and nothing else today: the task's run recorded a ready pull request and no lookup could say whether it merged (below). A screen shows it beside the state, never instead of it |

one entry of `releases`:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `version` | string | never | never | the target version, as the artifact spells it |
| `name` | string | **yes** | never | the release name the catalogue spends on this version (`Camel`), counted from the stream's own place — the version the stream is at carries the name before `next`, and each further minor is one name further along. Absent for a version past the list, one of another major line, or a plan whose stream is unknown |
| `stream` | string | **yes** | never | the stream the name comes from, when one was found |
| `finished` | bool | never | never | the stream has already shipped this release: the catalogue's `current` is at or past it |
| `total` | number | never | never | how many tasks target this version |
| `done` | number | never | never | how many of them are `done` with a confirmed merge |
| `closed` | number | never | never | how many are closed (the plan's own rule, above) |
| `open` | number | never | never | `total - closed` |

one entry of `streams` (the catalogue, GV1.4 — the same file
`scripts/release.sh` reads):

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `name` | string | never | never | `cli`, `console` or `mobile` |
| `theme` | string | never | never | `animals`, `fruits` or `birds`: the theme the stream's names come from |
| `names` | array | never | never | the names, in the order they are spent |
| `current` | string | never | never | the version the stream is at; `0.0.0` says it has shipped nothing |
| `next` | string | never | never | the name its next minor takes |
| `tag_prefix` | string | never | never | `v`, `console-v` or `mobile-v` |

**`totals`**: `plans`, `tasks`, `closed` and `open`, the same four figures over
every plan the read printed.

**The six states, defined.** `state` is the whole answer for a task, and it is
the CLI's, not a screen's rule:

- `running` — a live run is joined to the task (a status of `running`, `pending`
  or `launching`), and `stage` comes from it. The task is being worked on now.
- `done` — a joined run's pull request is **merged**, according to the
  operator's own `gh` (GitHub only, fail-open); or a finished run recorded a
  ready or reviewed pull request and **no lookup could answer at all**, and then
  `mark` is `merge unknown`. Either way the work is in.
- `cancelled` — the latest run was cancelled **and it is the task's only run**.
  The work was dropped.
- `blocked` — at least one task it needs is not `done`. It cannot start yet.
- `ready` — every task it needs is `done` and no run has claimed it. **This is
  the field a launch may act on** (design F7).
- `not-started` — everything else: the needs are `done` and a run exists that
  settled nothing. It has been started and is not running.

**Merged means merged — and where nothing can say, the read fails open.** The
merge answer comes from the operator's own authenticated `gh`
(`internal/watch`, `PRState.MergeNow`), asked about exactly the URLs that match
its anchored `https://github.com/<owner>/<repo>/pull/<n>` pattern; every other
URL — a Bitbucket one, one with anything appended to it — is never handed to a
subprocess. A URL that matches but names **another repository** than the
checkout's is not asked about either, and answers *not merged*: a run's
`pr_url` is job-written, and a job must not mark its own task done by pointing
at somebody else's merged pull request. Where there is no answer (no `gh`, a
Bitbucket repository, a failed or timed-out lookup, one past the cap), a task
whose run's record says its pull request is ready shows `done` with the mark,
rather than staying open forever: a screen that waited for an answer nobody can
give would be a screen that never settles. A `done` **with** the mark is **not** counted closed — only a confirmed merge is
(GD6).

**The join rules, and they are the CLI's.** (i) A run joins a task by the
`plan_task` its `launch.json` recorded — never by a title, and never by the
plan's own `run` field, which is the planner's note rather than a launch's
claim — and only when that value names *this* plan and a task of it. (ii) A run
joins only a plan of **its own repository**: a run from another repository
naming the same id is ignored. (iii) Of several runs on one task, the latest
launched wins for `state`; a follow-up that was given no `plan_task` of its own
joins through the pull request it follows, because it is the same work
continued. (iv) The join is **display-only**: it never makes another plan's node
`ready`, and nothing in this document authorises anything.

**`plan_task` is display text and this read treats it as such.** The value
arrives from `internal/runview` already shape-checked and sanitised (§2's row:
`runview.Join` drops an id `task.PlanTaskProblem` refuses), so this command never
opens `launch.json`, never reads a raw job-written string, and never prints one.
`launch.json` is writable by every launcher (SECURITY.md, D4), which is why a
run whose claim names no plan task of this checkout joins nothing rather than
being trusted with a path, a command or an identity.

**The walk stays inside `docs/`, and that is a rule of this read rather than an
implementation detail** (the plan's constraint C2, probed by GV2.1): the
artifacts are read where they sit, **a symlink anywhere under `docs/` ends the
read instead of being followed** (including a `docs/` that is itself a link out
of the checkout), the artifact count is **capped at 64** so a checkout that
fills the directory cannot be read without end, and the per-artifact limit is
§6.9's mebibyte. A checkout that plants a link to another directory is refused,
not read. An artifact must be a **regular file** as well: a fifo, a socket or a
device sitting at a `*.graph.json` name is refused rather than opened, because
opening one is a read that may never return.

**A refused plan is a warning, not an empty document.** A graph this build will
not render — a future schema version, a malformed artifact, a cycle — is
skipped: the refusal's own sentence goes to **stderr**, and `warnings` carries a
fixed sentence of ours naming the artifact, so a screen has something to show
without the document ever carrying text a job wrote. A read that finds no plan
at all is `plans: []` and one sentence on stdout, not an error. **Two artifacts
that would be one plan id are refused the same way**: the id is the artifact's
path, so `docs/plans/foo.graph.json` and `docs/plans/foo/bar.graph.json` both
give `plans/foo`, and joining them as one plan would let the second's tasks
share the first's runs and totals. The first artifact in path order keeps the
id, the second is not listed, and a fixed sentence of ours names both. A
catalogue that cannot be read is a warning too, and then every release has no
`name`: a catalogue dates a release, and it is not what a task's state is made
of.

**Version skew.** Every field is additive: a console built before this section
draws what it drew before, an older CLI has no `plan status` at all — the
console shows the screen as unavailable (the availability rule, §1) rather than
an error loop — and the `streams` array is the one part that changes meaning
with the *repository* rather than with the CLI: a CLI older than the catalogue
reads no `plan status` at all, and the file's own refusals are the CLI's to
report.

**Flags** (`newPlanStatusCmd`): `--all` (list the closed tasks too instead of
counting them per release), `--json`, and the cloud flags `--project`,
`--config`, `--gcp-project`, `--region`.

### 6.13 `fugaro coordinator status --from-store --all --json` (Bitbucket credentials T1.2)

**Command:** `ee/coordinator/cli/coordinator.go` `newCoordinatorStatusCmd` / `runCoordinatorStatus`.
**Document:** `coordinator.StoredProjectView`, indented (`writeCoordinatorJSON`), with
the read itself in `runCoordinatorStatusFromStore` and `coordinatorStoredView`
(`ee/coordinator/cli/coordinator_view.go`) — cited by symbol, not by line, for §6.11's
reason. This is the console's own read (`coordinator.status` in
`console/src/main/ipc.ts`) and design §5.1 rule 6's
`coordinator status --from-store --all --json` row: `--from-store` and `--all`
are **words of the row and not arguments**, so no renderer value reaches them,
and `--repo` and `--since` are deliberately out — the repository set and the
window are the CLI's answers rather than the screen's. The console asks for it
once per project view, plus `--project <name>` and `--config <path>` like every
other cloud row (§8).

**It is the one read of this set that asks nothing of the host.** Without
`--from-store` the same command reads each repository through the coordinator's
own credential — an installation token minted from the installation's GitHub App
— and it refuses outright under `FUGARO_CREDENTIALS_SERVICE_URL`. With
`--from-store` it opens no host and holds no App ID, no App key and no Bitbucket
credential: it reads the view the sweep already stored under
`coordinator/repos/<slug>/` in the project's database, through the caller's own
credentials — `roles/firebasedatabase.viewer` is what admits that read, the IAM
role launchers and operators hold. Everything such a document says about a pull
request is what the sweep read **once**, which is a fact a screen must not
present as current (below).

**And it is the one read here that answers rather than refusing for a repository
the coordinator has never touched.** A repository with no stored node is not a
failure and not an empty document: the command joins the runs bucket alone
(`coordinator.Join` with no pull requests) and the document says `"stored":
false`. What decides between the two halves is **the database and never the
local config**: a teammate's config is the *published* one, and
`localcfg.Config.Shared` drops Terraform, which is where the coordinator's
switch lives — so a read gated on the config would report the coordinator off on
every machine but the owner's.

**The envelope is the selection.** With `--all` the document is
`{"repos": [ … ]}`, one entry per repository the local config lists, and the
views are **not** compared across repositories: a service is a path's shape in
one tree, so two repositories' paths say nothing about each other
(`coordinatorRepos`). Without `--all` the document is the single `RepoView`
itself, with no wrapper — a reader that always expects `repos` would fail on
the one-repository read, and §8's argv is the `--all` form.

The document:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `repos` | array | never | never | one `RepoView` per selected repository, in the order `coordinatorRepos` produced them (sorted, so the same config gives the same document). **It is never `[]` here**: `--all` on a config that lists no repository is **refused** (`coordinatorRepos`' `userErr`, "the local config lists no repository: add one to its repos"), so that caller gets an error and a non-zero exit rather than a document. `--all` is the argv the console passes, so an empty array is a shape this read does not print |

one entry of `repos`:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `repo` | string | never | never | the repository this node is about, `owner/name`. **Untrusted display text**: it comes from the local config, and a screen draws it as text and never as a link, a path or an attribute |
| `provider` | string | never | never | the host the repository is on, `github` or `bitbucket`: the caller's own config for it, else its checkout's `fugaro.yaml`, which is the same resolution the node's slug is keyed by (D814). It is **not** read off `pr.url`, which is a field a job wrote and not a fact about the repository. **`""` is a real answer and not a gap** — a machine that records no provider for the repository and has no checkout of it has none to give, and a screen draws the number as text rather than as a link for it. The key is written even when it is empty, so `""` is told apart from a document older than this field — and the console's decoder reads a document that has no such key at all, as `""` rather than as skew, because the field is additive |
| `stored` | bool | never | never | **the flag this whole read turns on.** `true` means the document's `view` is what the sweep last stored; `false` means the coordinator has never reconciled this repository and `view` is the runs half alone. It is a bool and not the presence of `view`, because both halves carry one |
| `view` | object | never | never | the view itself, below |
| `stale` | object | **yes** | **yes** | the sweep's marker for this repository, as stored, below. Absent on a view `status` computes and on a repository whose last sweep was clean. **Absent and null both mean "no marker"**, and a screen has one branch for the two |

one `view`:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `repo` | string | never | never | the same repository path as the node's own `repo`, carried by the view because the view is also written where the node is not |
| `records` | array | never | never | one `Record` per pull request the join found and per in-flight run that opened none, in a **fixed order** the view sets rather than the reader: work still in flight first, then by pull request number, then by run ID. `[]` is a fact — either the repository has no work, or (with `stored: false`) the window in `--since` held no run — and the two are told apart by `stored`, never by this array's length |
| `collisions` | array | never | never | every pair of in-flight records that share a file or a service, in `records`' own order rather than sorted by label: by the earlier record, then by the later. `[]` means no two in-flight records touch the same path or service |
| `unread` | array | **yes** | **yes** | every pull request whose host state this pass could not read, sorted by number, below. Written by the sweep alone: **absent from a view `status` computes**, and absent means the empty list rather than a gap |
| `omitted` | number | **yes** | **yes** | how many records the document's bound left out, and `omitted_collisions` how many collisions. Both are the sweep's keys and are absent from a computed view. The records dropped are the **finished** ones whose newest run is oldest — never one in flight while a finished one is kept |
| `omitted_collisions` | number | **yes** | **yes** | see `omitted` |

one entry of `records`:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `pr` | object | **yes** | **yes** | the pull request this record is about, below; **nil for an in-flight run that has opened none**. Absent and null are the same answer, and a screen draws "no pull request" rather than an empty one |
| `runs` | array | never | never | the runs behind it, newest first, below. **`[]` is a fact and is never dropped**: a pull request the store holds no run for is still a pull request somebody has open, and the view lists it as having no run behind it |
| `branch` | string | **yes** | **yes** | the branch the record declares: the pull request's source branch when the host answered, else the newest run's own branch. `""` is a record that declares none |
| `release` | string | **yes** | **yes** | the target release the record declares, read from a branch that names one (`coordinator.ReleaseOf`). `""` is the ordinary case and not a gap: most work targets no release |
| `in_flight` | bool | never | never | whether the work is still in flight, and **the only thing a collision is computed over** |
| `files` | array | never | never | every path the runs behind the record say they changed |
| `services` | array | never | never | every service those paths belong to (`coordinator.ServiceOf`) |

one entry of `runs`:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `id` | string | never | never | the run's id, as `fugaro run` wrote it. **Untrusted display text** and never a path |
| `pr` | number | **yes** | **yes** | the pull request number the run is on. **Absent when the run is on none** — a run that has not opened one yet, or one that produced none at all — because the wire omits it (`Run.PR` is `json:"pr,omitempty"`; the struct's `0` is Go's internal spelling of "none" and never a key on the wire). The console therefore never sees a `0` here, and its decoder refuses one: a pull request number below `1` is not a number it will draw |
| `status` | string | **yes** | **yes** | the run record's own word (`"running"`, `"succeeded"`, …). **`""` for a run whose object the store does not hold**, which is not the same as a status the console does not know |
| `outcome` | string | **yes** | **yes** | the record's outcome word, absent when the run has not written one |
| `terminal` | bool | **yes** | **yes** | whether the run has finished. A run with no readable record is **not** terminal, which is the safe direction: it is still in flight, and the view says so rather than guessing |
| `branch` | string | **yes** | **yes** | the run's own branch; `base_branch` the branch its pull request targets |
| `base_branch` | string | **yes** | **yes** | see `branch` |
| `declared_by` | string | **yes** | **yes** | the launcher the run's own launch record wrote at launch time — the local config's `user:`, else git's `user.email`, which a launcher writes about itself and nothing verifies. It is carried as a **declaration** and is never an identity: no field of this document promises the string names the person, the account or the machine the run actually ran as, and **nothing may authorise or admit anything by it** |
| `files` | array | never | never | the paths the run's record says it changed. **An empty list is the answer "no path", never a missing answer** — the store tells "recorded no set" from "recorded an empty one" and this view does not, because both answer the one question it asks of a run's paths — may two records collide on one? — with no |
| `files_truncated` | bool | **yes** | **yes** | the record's own admission that the set it stored is short of the set it changed (`runstore.ChangedFiles`). **A truncated set is not a whole one**, and a view that presented it as one would under-report a collision |

the `pr` of a record:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `number` | number | never | never | the pull request's own number. It is a number to **show**: no screen builds a link from it (design §5.1 rule 2, §4.3's gap 7) |
| `url` | string | **yes** | **yes** | the host's URL for it, which the sweep read once. **Carried as data and not drawn**: the project view builds no element from it — not as text, and never as a link (design §5.1 rule 2) — because it is a hostile field like any other |
| `title` | string | **yes** | **yes** | **untrusted text written by whoever opened the pull request.** It is carried as data and is never a decision: a renderer sanitises it before it reaches a line or a cell (`safetext`), the way every other surface that draws a pull request's own words does |
| `branch` | string | **yes** | **yes** | the source branch the host reports |
| `draft` | bool | **yes** | **yes** | whether the host says the pull request is a draft |
| `state` | string | **yes** | **yes** | the host's own answer (`"open"`, `"merged"`, `"closed"`). **`""` is the host not answering** — a pull request a run names and the reader did not return — and the record then falls back to whether the runs behind it have finished |

one entry of `collisions`:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `a`, `b` | string | never | never | the two labels of the pair, in the order the view produced them. They are labels a person reads, not ids: a record's label is its pull request reference or its newest run's id (`coordinator.Record.Label`) |
| `files` | array | never | never | the paths both records touch |
| `services` | array | never | never | the services both records touch |

one entry of `unread` (the sweep's):

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `number` | number | never | never | the pull request whose host state could not be read |
| `reason` | string | never | never | **why, as the console's own sentence**: the sweep writes it, and it names no credential and no job-written string |
| `carried` | bool | **yes** | **yes** | whether the state shown beside it is the **last one a sweep did read**, rather than nothing. It is the difference "stale but known" and "unknown", and a screen that dropped it would present a carried state as a fresh one |
| `failures` | number | **yes** | **yes** | how many consecutive passes have failed to read it |
| `retry_after` | string | **yes** | **yes** | an RFC 3339 instant, written `omitzero`: the host's own back-off, absent when it gave none. It is a **stamp**, and a screen must not turn it into a countdown this document does not carry |

the `stale` of a repository (the sweep's):

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `since` | string | never | never | an RFC 3339 instant: when the marker was written. **It is printed exactly as stored, with no age computed from it.** The stored view carries no time by design (`coordinator.Stale` is a function of its inputs), so the view beside this marker and the marker itself are not on a common scale, and "stale for three hours" would be a claim about when the sweep last ran that the document cannot support |
| `reason` | string | never | never | why the sweep could not keep the view current, as the sweep's own sentence |

**Version skew.** Every field is additive: a console built before this section
draws what it drew before, and a CLI older than the `--from-store` flag has no
such read at all — the console shows the screen as unavailable (the availability
rule, §1) rather than an error loop. The one part that changes meaning with the
**repository** rather than with the CLI is `unread`, `omitted` and
`omitted_collisions`: they are the sweep's, so a view `status` computes and a
view an older sweep stored simply do not carry them, and absent is the empty
list rather than a gap.

**Flags** (`newCoordinatorStatusCmd`): `--from-store` (print the view the sweep
stored, with no host read and no key, falling back to the runs alone when none
is stored), `--all` (every repository the local config lists, one view each),
`--repo` and `--since` (the single-repository read and the runs window, which
the console does not pass), `--json`, and the cloud flags `--project`,
`--config`, `--gcp-project` and `--region`.

### 6.14 `fugaro run --json` — the launch (design §5.1 rule 11)

**Command:** `internal/cli/run.go` (`newRunCmd`), printed by `printLaunch` with
`json.Encoder` and `SetIndent("", "  ")`: **one indented object**, the shape
every command in the console's set prints (§9.2). **This was the one write in the
whole console, and it is the first of the two it has now** — the operation
`run.launch`, the row V2.5.1a added to the IPC allow-list (2026-10-28/29), the
surface's twenty-fourth; the settings edit (§6.20) is the second — and it is one
of the two documents below that a write produces rather than a read. The argv the console builds from the form's
five fields, and the confirmation window the main process owns, are design §5.1
rule 11's and are not repeated here; what this section settles is the shape of
the answer those fields come back as.

**Document:** `launchResult` —

`project`, `run`, `repo`, `run_id`, `branch`, `ref`°, `execution`, `log_url`°,
`status`, `pr`° (**int**), `previous_run`°, `recipe`°, `project_layer`°.
`°` = `omitempty` (as in §3). `recipe` is `launchRecipe`: `name`, `source`.
`project_layer` is `launchProjectLayer`: `sha256`, `generation`° (**int64** —
which one layer the stored task actually carries, and never its text).

**Four of the fields are the launch's own identity, and the fifth is a version
of it the console built.** `project` is the Fugaro project the launch was cut
for. `run` and `run_id` are the same run id under the CLI's two keys, and it is
the id the console generated for that one confirmation window, not one the form
could propose. `branch` is the branch the run's work goes on — a new run's, or,
for a follow-up, the pull request's — and `ref`° is the ref a new run's branch
is cut from, which its review measures the work against; `ref` is absent for a
follow-up and a review run, whose review reads each pull request's own base. And
**`repo` is the CLI's canonical `owner/name`**, not the `<provider>/<owner>/<name>`
identity the installation's records carry: the conversion between the two is the
console's own (`launchrules.launchRepoArgument`), and the main process checks
this field against the value it asked for before the reply is believed.

**`status` is `launched` or `already-launched` and nothing else.** The second is
the CLI's own claim already holding this run id, and the design draws it as a
**fact rather than as an error** (D23). `pr`° and `previous_run`° are a
follow-up's pull request and the run it continues; `execution` is the execution
the run is in; and `log_url`° is where the CLI prints the run's logs — **carried
as text and never drawn as a link** (design §5.1 rule 2).

**A launch that fails prints no document at all.** The CLI's refusals — an
empty or oversized task, a title or a plan task past its cap, a budget
pre-check, a claim already held, a credential it could not use — are returned as
errors: `run` exits non-zero with the CLI's own sentence on stderr, which the
console draws as text (`sanitizeRefusal`). So the reply this section documents is
the *successful* launch's, and a screen that waited for a `status` field on the
failure path would wait for a document no command prints. The one exception is
inside the CLI, not on this surface: a batch launch prints a batch document once
per attempt (`printBatch`), and the console never asks for one — the batch flag
is on rule 11's excluded-flag list — so that shape is not this operation's.

**The decoder is closed, and it is the contract's enforcer.** The main process
reads this document with `decodeLaunchResult`
(`console/src/main/launchrules.ts`): the keys must be exactly the ones above, the
types are the ones the CLI writes, `status` must be one of the two words, and
**an unknown key or a wrong type refuses the whole reply** rather than being
ignored (rule 11). Every string is kept as text and bounded — nothing here is
read as a sentence, a path or a link — and the renderer mirrors that decoder
(`console/src/renderer/screens/launch.ts`'s `launchOutcomeOf`), so a reply with
a key of its own draws the console's one "unreadable reply" sentence and never a
partial set of facts. A replayed answer — a second click on the window's own
button, or a re-sent call — is the app's own in-memory record of the first
`launchResult` and reaches no child at all (design §5.1 rule 11).

### 6.15 `fugaro coordinator show --repo <owner/name> --pr <n> --json` (coordinator v2 C3)

**Command:** `ee/coordinator/cli/coordinator_show.go` `newCoordinatorShowCmd` /
`runCoordinatorShow`. **Document:** `coordinator.PRDocument`
(`ee/coordinator/view.go`), printed by `writeCoordinatorJSON` — the same
indented-object shape as every other read in this set (§9.2). The read behind it
is `Project.PullRequest` (`ee/coordinator/project.go`), beside the view's own
`Project.PullRequests`.

**Why it is a read of its own.** The stored view (§6.13) carries no body, by
design: a body is unbounded text written by whoever opened the pull request, and
one per record would ride in the document every reader of the view fetches
(reconciliation F3, D816's (a)). The shape that carries one is therefore a read
of one pull request, and the console's row that draws a description reads here.
The document is **one flat object**, never wrapped: there is one pull request in
it and no list to enumerate.

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `number` | number | never | never | the pull request's own number, as the host gave it. It is a number to **show**: no screen builds a link from it (design §5.1 rule 2) |
| `url` | string | **yes** | **yes** | the host's own address for it. **Carried as data and never drawn**: not as text, and never as a link (design §5.1 rule 2), because it is a hostile field like any other. Absent when the host answered none, which is a real answer and not a gap |
| `title` | string | **yes** | **yes** | **untrusted text written by whoever opened the pull request.** Carried as data and never a decision: a renderer sanitises it before it reaches a line or a cell (`safetext`) |
| `body` | string | **yes** | **yes** | the description the host holds, **as the host holds it** — untrusted text of the same kind as `title` and the reason this read exists. It is the one field here that is many lines: a renderer sanitises it as a block (`safetext.MultiLine`) and never parses it, and **nothing in it is a link, a path, a command or a decision**. `""` is the host answering with an empty description, which is not the same as the key being absent |
| `state` | string | **yes** | **yes** | the host's own word (`"open"`, `"merged"`, `"closed"`), never a derivation from the runs behind the pull request. It is **not** normalised into a boolean and not mapped onto §6.13's `state`, which the view computes over many records |
| `draft` | bool | **yes** | **yes** | whether the host says the pull request is a draft. The adapter answers the same way where the host has no real drafts, so the field is the host's answer as the CLI reads the host |
| `branch` | string | **yes** | **yes** | the source branch the host reports |
| `base` | string | **yes** | **yes** | the branch it targets **now**, which a retargeted pull request makes different from the one it was opened against. It is the host's current answer and not a record of the launch |
| `author` | string | **yes** | **yes** | the host's own **account id** for whoever opened it — GitHub's numeric user id, Bitbucket's `account_id`. It is an account id and never a display name, it is a **string even where it is numeric**, and **nothing may authorise or admit anything by it**. A screen that wants a name resolves it itself or draws the id |

**Every field is the host's own answer and nothing is derived from it.** A field
the host did not answer is **absent** rather than empty or guessed: the document
says what the host said, and a screen tells "the host said nothing" from "the
host said an empty string" by the key's presence, not by its value. **There is no
update stamp**: `updated` is not carried, because the CLI's own pull-request read
(`gitprov.PRInfo`) has no such field to carry — the document stops where the
host's answer the CLI holds stops, and a timestamp invented here would be a
figure no command emitted.

**A pull request the host does not have is a refusal, not an empty document.**
The command prints **no document at all** and exits non-zero with the host's own
status in the sentence on stderr (a 404 for a number the repository has no pull
request for): the failure is the host's, so the exit code is **2**. An empty
document would draw a pull request with no description, which is a different
fact from "the host has no such pull request", and a screen that rendered one
would be showing work that does not exist.

**The credential is the coordinator's own, and it is read-only.** The read goes
through the same path as §6.13's project-wide read (`coordinatorReader`): one
GitHub App installation token per repository, minted from the installation's App
with the coordinator's named permission set — **metadata read and pull requests
read, no `write` of any kind** (Q3 answered (A)) — cached for the token's life
and revoked when the command is done. There is no second credential path, and
**no credential of the host's reaches the document, an error or a log line**;
the provider's own error text is redacted with the values the read held before it
is reported.

**The refusals.** With `FUGARO_CREDENTIALS_SERVICE_URL` set, the installation's
credentials service holds the App's key and this command holds none, so it
refuses **naming the service** and exits **1** before it reads the App's ID or
key, opens a provider or mints anything (reconciliation F9). A repository on a
host this read does not serve is refused the way the project-wide view refuses
it — **Bitbucket, whose credential is one repository's access token and cannot
be widened to a project** (reconciliation Q12 (A), finding #30) — and the
sentence names the reason rather than the repository; that is also exit **1**.
A `--repo` that is not `owner/name` or a `--pr` that is not a pull request number
is refused before any read is made, exit **1**. **Exit codes: 1 for a refusal of
the caller's own environment or argv, 2 for a failure of the host.**

**The provider is the repository's own**, resolved the way §6.13's `provider`
field is (C1): the local config's entry for the repository, else its checkout's
`fugaro.yaml`. A screen that has just drawn a `provider` from §6.13 and asks for
a description here is asking the same question of the same resolution.

**The console's row is built on this section.** It is coordinator v2 C3's console
half: the design's §5.1 table carries `coordinator show --json --repo <owner/name>
--pr <n>` as the `coordinator.show` / `coordinatorShow` operation
(`console/src/main/ipc.ts`), whose two arguments are the `repository` and
`pullRequest` kinds and are both required — the argv above is the whole of what a
renderer can influence about the child, and there is no key of that schema for a
token, a host, a path or a flag. The decoder written against the table above is
`decodeCoordinatorPullRequest` (`console/src/shared/schema.ts`): `number` is the
one key the document must carry and the one that must be a whole number, the
other eight are carried as the host's own text (or, for `draft`, its flag), an
absent or `null` key reads as absent rather than as a value, and a key none of
the nine is does not reach a screen. A screen that draws a description reads
this operation and treats `body` as the untrusted block it is.

**Version skew.** Every field is additive. A CLI older than this command has no
such read at all, and the console shows the section as unavailable (the
availability rule, §1) rather than an error loop. A field added later is a field
an older console does not draw; a console that requires one must refuse the
document rather than fill it in.

**Flags** (`newCoordinatorShowCmd`): `--repo` (the repository, `owner/name`),
`--pr` (the pull request number), `--json`, and the cloud flags `--project`,
`--config`, `--gcp-project` and `--region`.

### 6.16 `fugaro coordinator intents [--all | --repo <owner/name>] [--since <d>] --json` (coordinator v2 C4)

**Command:** `ee/coordinator/cli/coordinator_intents.go` `newCoordinatorIntentsCmd` /
`runCoordinatorIntents`. **Document:** `coordinator.RepoIntents` and
`coordinator.StoredProjectIntents` (`ee/coordinator/state.go`), printed by
`writeCoordinatorJSON` — the same indented-object shape as every other read in
this set (§9.2). The read behind it is `Store.ReadRepoIntents`, beside
`Store.ReadRepoView` and through the same store.

**Why it is a read of its own.** The declared intents are a second document from
the stored view (§6.13), written by a different writer: `coordinator report` and
every launch's own declarer put one record per declaration under the
repository's `intents/` node (P4), and the sweep prunes them. The stored view
joins runs and pull requests and names no declaration at all, so the read-only
pane of reconciliation §5.8 draws the two documents side by side: what the fleet
has declared, and what is in flight. **Nothing in this document is derived**: the
fields are the record's own, as the node holds them.

**The document is one object per repository, and one list with `--all`.** One
repository (the default, or `--repo`) prints the repository object alone, and
`--all` wraps them:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `repos` | array of repository objects | never | never | every repository the selection names, **in that order** (the local config's repositories, sorted), never in the order the reads finished. The key exists only in `--all`'s document: one repository is printed as the object itself, so a reader never has to unwrap a single-element list |

Each repository object is:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `repo` | string | never | never | the repository the caller asked about, `owner/name`, **as the caller spelled it**: the node's key is a slug (a hash of provider and name) and cannot be inverted, so an absent node carries no name to read back. Carried as data and never drawn as a link (design §5.1 rule 2) |
| `provider` | string | never | never | the git provider kind the caller resolved for this repository — the local config's entry, else the checkout's `fugaro.yaml` — one of `config.Providers`' two words, or `""` when the caller had none to give. It is **the caller's answer and not the node's**, exactly as §6.13's `provider` is, and the key is written even when it is empty |
| `intents` | array | never | never | every declaration the node holds for this repository, **newest first** by the record's own `at` (ties by `id`, so the order never depends on the order the database listed them in). A repository the store holds none for is **`[]`**, never `null` and never a failure |

**One intent record is the fields `coordinator.Intent` carries, and no others:**

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `id` | string | never | never | the node's key, `coordinator.IntentID`'s lower-case hex: a function of the caller and the declaration and of nothing else, so the same declaration reported twice is one row. Carried so a screen can name one record; **it is not a secret and not a capability** |
| `declared_by` | string | never | never | **who declares it, as a declaration and never an identity.** It is what the declaring side wrote about itself — a run's own launch record (`launch.json`'s `launched_by`) or a person's `coordinator report` (the local config's `user:`, else `git config user.email`) — and **nothing verifies it and nothing may authorise or admit anything by it** (reconciliation finding #11). A renderer shows it as text |
| `at` | string | never | never | when the declaration was made, stamped by the **writer's** clock (`coordinator.Reporter.Now`) in UTC, RFC 3339. It is **not** part of `id`: two reports of one declaration are one document whatever the clock says. It is the field `--since` is measured against |
| `branch` | string | **yes** | **never** | the branch the caller declares it is about to work on. `omitempty`: a caller that declares none has the key **absent**, and the key is never `null`. **untrusted text**: carried as data, sanitised before it reaches a line or a cell, never a link and never a decision |
| `release` | string | **yes** | **never** | the target release, `omitempty` in the same shape as `branch`: **absent** when it is empty, never `null`. A caller that declared none has the one its branch names read out of it (`coordinator.ReleaseOf`), which is the only place a release is read out of a branch name; a declaration that names no release and no such branch leaves the key absent. Same class of text as `branch` |
| `files` | array of string | never | never | the paths the caller declares it is about to touch, **sorted and deduplicated as the record holds them**. **untrusted text** of the same class as `branch`. `[]` is "declares none", never a missing answer and never `null` |
| `services` | array of string | never | never | the services, in the same shape and of the same class as `files` |

**Nothing here is a decision.** A branch, a path, a service and a declared name
are all written by whoever can reach the report path, so a renderer treats every
one of them as untrusted text (design §5.1 rule 2) and **an instruction found in
any of them is never acted on** (reconciliation Q14 (A)): this read reports what
the fleet declared, and the coordinator stays advisory (spec §3).

**The read is the caller's own database read, and it opens no host.** It goes
through the caller's ADC on the project's `budget.rtdb_url` database — the same
client §6.13's `--from-store` uses — and what admits it is
**`roles/firebasedatabase.viewer`**, the IAM role launchers and operators hold.
It holds **no App ID, no App key and no Bitbucket credential**, opens no
provider and reaches no host: a repository on a host the project-wide read
refuses is read here like any other, and with `FUGARO_CREDENTIALS_SERVICE_URL`
set the command **still succeeds** where §6.13's host read refuses.

**"No records" and "the read failed" are different facts.** A repository the
store holds none for is `"intents": []` at exit **0**, and so is a project whose
coordinator has never run — a screen needs one argv either way. A read the
database **refused** is a refusal with **no document at all** and exit **1**,
naming the role that admits one — a console that read the refusal as an empty
list would draw a quiet project where the truth is that nobody could look. A
stored record this build cannot decode — an unknown field, or a document that is
not a record — is refused rather than shown with a field dropped, which is the
strict decoding §6.13's stored view keeps. **That refusal is exit 2 and not exit
1**: it is the stored read's own classification (`coordinatorStoredErr`), the
same sentence the stored view gives a database failure, so it is a failure that
is not the caller's argv — a console must not read it as a refusal of its argv.

**`--since` bounds the declarations by the record's own `at`**, and its default
is `0`: every declaration the store holds. The node is already bounded — the
sweep keeps the newest few hundred records and prunes by age — so a window that
defaulted to hiding the rest would be a fact a reader never sees.

**The console's row landed in C4c.** This section is the CLI's half of
coordinator v2 C4, and the console's half is the same task's part c: the spawn
row, the operation, the preload row and the decoder. Where §5.1's table and the
allow-lists are is where a row is admitted, and what they admit is **one argv and
only one** — `coordinator intents --all --json`, the fixed words of
`console/src/main/spawn.ts`'s row, with an empty argument schema
(`console/src/main/ipc.ts`) so that no field a renderer sends can reach the
child. This document states what the CLI prints; it authorises no spawn, and the
row it now has is the `--all` document alone: the single-repository form below is
a shape no console row asks for, and the decoder refuses it.

**Version skew.** Every field is additive. A CLI older than this command has no
such read at all, and the console shows the section as unavailable (the
availability rule, §1) rather than an error loop. A field added later is a field
an older console does not draw; a console that requires one must refuse the
document rather than fill it in.

**Flags** (`newCoordinatorIntentsCmd`): `--repo` (the repository, `owner/name`),
`--all` (every repository the local config lists), `--since` (the window,
measured against `at`), `--json`, and the cloud flags `--project`, `--config`,
`--gcp-project` and `--region`. `--all` with `--repo` is refused rather than
resolved, the way §6.13's selection refuses it.

### 6.17 `fugaro runs summary --json` (**CLI 0.16.0**)

**Command:** `internal/cli/runs_summary.go` `newRunsSummaryCmd` / `runRunsSummary`.
**Document:** `summaryDoc`, with its `summaryRepo` rows and its `summaryTotals`,
indented (`writeSummary`), all in `internal/cli/runs_summary.go` — cited by
symbol, not by line, for §6.11's reason. This is the fourth command in this
document the console did not spawn when the section was written, and **the
console spawns it now**: console 0.119.0 put it on the surface (`runs.summary`
in `console/src/main/ipc.ts`), the console's half of B4, as the read the
Project, Coordinator and Spend screens' totals are built on. Its argv is design
§5.1 rule 6's `runs summary --json` row: at most one `--since=<window>` word,
from the console's own closed set. The section stays here for §6.11's reason —
the screens read a shape off the code rather than assuming one from a design.

**Why it exists: a totals read that opens no run.** `runs ls` (§5) answers by
reading the runs bucket — one run object per row, its task and its result — and
that is what makes it a listing and what makes it slow. This read answers from
the budget database's day nodes (`spend`, `outcomes`, `runs`: the counters the
caps are enforced against) and from the spend history's daily documents — the
same two sources §4's `report` reads — over the database's own window of eight
days, a few hundred kilobytes, in about a second. **Neither the runs bucket nor
the compute backend is opened**: no run's task, result or transcript is read,
which is why the compute figure is the archived one alone (its own row below).
It is a command of its own rather than a totals block on `runs ls --json`
because the two count different things from different sources — here a run's
day is the UTC day the budget recorded its spend, there a row is an object in a
bucket — and two totals under one document name would leave a reader unable to
say which basis a figure has.

**A run is counted on the UTC day the budget recorded its spend**: the day the
rollover files it under and the day its money is counted on. The counts
therefore agree with the budget and **not necessarily with `runs ls`**, whose
rows are the runs bucket's. A run whose outcome has not settled yet is counted
in `runs` and in no state, so `runs` is at least the sum of `states`.

**The document is one object, and the window is a span of UTC days ending
today.**

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `v` | int | never | never | the document's version, `1` today (`summaryDocVersion`). A consumer that does not know a version refuses the document rather than reading it as the one it does |
| `project` | string | never | never | the selected project's name: the installation whose budget database and spend history were read |
| `since` | string | never | never | the window's first UTC day, `YYYY-MM-DD` — a date, never a timestamp and never a clock read at render time. `--since 7d` is today and the six days before it, so this is six days before `until` |
| `until` | string | never | never | the window's last UTC day, which is today |
| `repositories` | array of repository rows | never | never | one row per repository either source named in the window, **sorted by `slug`**; `[]` when nothing ran in it. A repository the sources do not mention is not a row: nothing lists the runs bucket to find one |
| `totals` | object | never | never | the same figures as a repository row, without the two names — the figures table below |
| `days_unknown` | array of string | never | never | the window's UTC days, ascending, `YYYY-MM-DD`, that **neither source answered for**: their runs and their spend are unknown and are counted in no figure here, so a screen shows the window as partly unknown rather than as a small one. `[]` when both sources answered every day |
| `notes` | array of string | never | never | what the figures count and what the read could not cover, one line each: the two sources, the day a run is counted on, a source that could not answer with its reason, and how much of the window is unknown. A renderer shows them as text, as it shows any other reason string |

**A repository row carries the figures and the two names the sources hold:**

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `slug` | string | never | never | the storage key both sources file the repository under — a hash of provider and name, so it is not a name to draw. It is the row's own key and the sort order |
| `repo` | string | never | never | the repository's `owner/name`, as the day records carry it (the name `fugaro budget set --repo` writes). It falls back to the slug for a repository nothing recorded a display name for, so a renderer treats it as text and never as a link without checking it |

**The figures are the same six keys on a repository row and on `totals`:**

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `runs` | int | never | never | how many runs the window holds, each counted once on its own day. At least the sum of `states` |
| `states` | object | never | never | the counts by state. **All six of `succeeded`, `failed`, `halted`, `cancelled`, `infra_error` and `other` are always present**, zero included, so a consumer reads a count without a nil check; a run whose outcome names a status outside the six is counted in `other` rather than dropped or given a key of its own |
| `spent_micros` | int | never | never | the model money the caps count, in micro-dollars — the budget database's own figure, so nothing is rounded on the way out |
| `notional_micros` | int | never | never | the part of the model figure a subscription paid, in micro-dollars: it counts toward the plan's limits and is nobody's bill. **It is separate from `spent_micros`, and the two are never summed** |
| `compute_micros` | int or **null** | never | **yes** | what the runs whose compute was priced came to, in micro-dollars. **null when the row has runs and no run's compute is known** — "not estimated" is not "free", and a renderer draws it as `n/a` — and `0` when the row has no run at all, which is a fact. This read opens no bucket, so a live day's compute is never estimated here |
| `compute_runs` | int | never | never | how many runs `compute_micros` covers: at most `runs`. A figure covering fewer runs than the window holds is a **lower bound** a consumer can see, and the reason a row's compute is not read as the window's whole |

**A day nothing answered for is named, never counted as zero.** The budget
database holds the days it has not pruned — today and the eight before it
(`budget.PruneAfter`) — and the rollover archives each of them as one daily
document in the spend history. So a day inside the database's window with no
node is a day nothing ran on, **which is known**, and it is not in
`days_unknown`; a day older than that window is the history's to answer, and a
day the history holds no document for is a day nothing ran on too, as long as
the history answered at all. Only a day **neither** source could answer for is
listed there, and a window whose days are all unknown is refused rather than
printed: a document of zeros would read as "nothing ran", which is the opposite
of what is known.

**"Nothing ran" and "the read failed" are different facts.** Three states a
project may be in are not failures — no Firebase budget backend, no Firestore
database, no history mark — and each is reported as itself: the database's own
days are counted, the older days are named in `days_unknown`, and a `notes`
line says which of the three it was, because a screen needs a document either
way. A read a source **refused** or lost is a failure only when the other could
not answer the window either: then there is **no document at all**, exit **1**
for a refusal (naming the role that admits the read) and exit **2** for an
outage — a console that read either as an empty summary would draw a quiet
project where the truth is that nobody could look. When one source did answer,
the days the other could not answer for are in `days_unknown` and the note
names the reason, so the figures a screen gets are the ones that are known and
the gap is visible rather than filled with zeros.

**The window is always bounded.** `--since` takes the durations `runs ls` and
`runs stats` take (`7d`, `36h`, `90m`), and a span with no lower bound — `0` —
is **refused** (exit 1), because a summary is per UTC day and the whole history
is not a window: the refusal names `--since 7d`, and `--since 365d` for the
last year, as the way to ask. A window longer than ten years is refused too,
since the spend history is not built for one.

**Version skew.** Every field is additive within `v: 1` (D784). A CLI older
than this command has no such read at all, and the console shows the section as
unavailable (the availability rule, §1) rather than an error loop. A field
added later is a field an older console does not draw; a console that requires
one must refuse the document rather than fill it in.

**The console's row has landed.** This section is the CLI's half of the read,
and the console's half is in the allow-lists' code rather than here: the spawn
row (design §5.1 rule 6's), the operation (`runs.summary`) and the decoder
(`decodeRunsSummary`, `console/src/shared/schema.ts`) were added by console
0.119.0. Nothing here authorises a spawn on its own — §5.1's table and the
allow-lists are where a row is admitted — and this document still describes the
CLI's side of the contract.

**Flags** (`newRunsSummaryCmd`): `--repo` (one repository, `owner/name`),
`--since` (the window, default `7d`), `--json`, and the cloud flags
`--project`, `--config`, `--gcp-project` and `--region`.

### 6.18 `fugaro models ls --json` (**CLI 0.16.0**)

**Command:** `internal/cli/models_ls.go` `newModelsLsCmd` / `runModelsLs`.
**Document:** `modelsLsDoc`, with its `modelsCatalog`, its `modelsRoute`,
`modelsModel` and `modelsRepo` rows, and the `priceCatalog` a catalog-priced
row carries — all in `internal/cli/models_ls.go` except `priceCatalog`, which
is `internal/cli/budget.go`'s and is the same shape `budget prices --json`
prints on a row (D794). Cited by symbol, not by line, for §6.11's reason. This
is the fifth command in this document the console did not spawn when its
section was written, and **the console spawns it now**: console 0.119.0 put it
on the surface (`models.list` in `console/src/main/ipc.ts`), the main-process
half of the read-only models panel the owner ruled on 2026-11-02. Its argv is
design §5.1 rule 6's `models ls --json` row, and it is that row's whole argv:
the command takes **no argument at all** (`cobra.NoArgs`), so there is nothing
on it a renderer fills. The section stays here for §6.11's reason — a screen
reads a shape off the code rather than assuming one from a design.

**Why it exists: what a project can run, read offline.** The command answers
the project's own configuration, this binary's price table and the price
catalog, and nothing else: **it reaches no bucket, no database and no host** —
the sentence is the command's own (`newModelsLsCmd`'s `Long`), and it is the
reason the panel can be drawn before any credential exists. What it reads is
the four questions a user asks: which routes a call may go down, which models a
run can name today and what each costs, which repositories each model may be
sent to, and what each recorded repository pins. It is not `budget prices --json`
(§6.4's sibling read, which prints the price table alone) and not `config list
--json` (§6.8): it is the project's routes and allow-lists joined to the
prices, which is what makes a route's fee or a repository's refusal visibly
somebody's configuration rather than a number this binary chose.

**A repository this machine has no checkout for is unknown, never guessed at**
(D793). The `USAGE` rows are read out of each recorded repository's
`fugaro.yaml` at the base branch it runs against, from the checkout this
machine remembers for it; a repository with no remembered checkout, or one
whose checkout does not hold that ref, carries a `state` that says so and no
pins. The published policy's `base_branch` is **not** consulted — reading it is
a cloud read, and this command reaches nothing — so a project whose default
lives only there reads as `main` here.

**The document is one object.**

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `version` | int | never | never | the document's version, `1` today (`modelsLsDocVersion`, pinned by `schemas/models-ls.schema.json`). A consumer that does not know a version refuses the document rather than reading it as the one it does |
| `project` | string | **yes** | never | the selected project's name. `omitempty`, so no project config selected leaves it out — and the `routes` then hold the direct route alone |
| `catalog` | object | **yes** | never | the built-in provider price catalog's provenance, taken from the first catalog-priced row (`catalogProvenanceOf`): one header for every row it prices. **Absent when no row is catalog-priced** — that is "no catalog-priced model here", not "no catalog" |
| `routes` | array of route rows | never | never | the direct route first, then the owner's providers in name order. **Never empty**: the direct route is always a row |
| `models` | array of model rows | never | never | every model a run can name today, in ID order: the price table's rows, the exact IDs a route claims, the `model_prices` keys, the project's `allowed_models` entries and every model a recorded repository pins. A model with no price row is a row here too, with the reason it has none |
| `repositories` | array of repository rows | never | never | one row per repository the selected project records, in name order, `[]` when it records none. A repository this machine has no checkout for is a row with a `state`, never a missing row |

**The catalog block is the built-in provider price catalog's own provenance:**

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `source` | string | never | never | the catalog URL the prices came from. **It is untrusted text**, a URL a generated catalog names |
| `checked_at` | string | never | never | the date the catalog was last checked, `YYYY-MM-DD` — a date, never a timestamp |
| `age_days` | int | never | never | how many days ago that was, as the read computed it |
| `stale` | bool | never | never | `true` when `age_days` is past the catalog's 30-day rule (design D10). Every row priced from this catalog carries the same four facts; this block states them once for all of them (`modelsCatalogOf`) |

**A route row is the project's provider entry, or the direct route.**

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `name` | string | never | never | the provider entry's name, or the direct route's own (`config.RouteDirect`). It is the key a model row's `route` names, and the row's sort order |
| `kind` | string | **yes** | never | the provider entry's `kind`; `anthropic` on the direct route |
| `base_url` | string | **yes** | never | the entry's own base URL; the Anthropic API's own on the direct route. **It is untrusted text**, a URL somebody's config names: a screen draws it as text and never as a link |
| `host` | string | **yes** | never | the host of `base_url` (`hostOf`), or the URL itself when it parses to no host, so a base URL that names none is shown as written rather than blanked. It is what a reader recognises the route by |
| `models` | array of string | never | never | the exact model IDs the route claims, in the entry's order; the direct route's are the embedded table's keys, sorted. A claimed ID may be a prefix pattern (`claude-*`), which is the entry's own spelling and is drawn as text |
| `route_fee_pct` | number | **yes** | never | what is added to every charge on the route, in percent (`float64`, `omitempty`). **Absent when the route charges none**, and a consumer reads that as `0`: a route that adds nothing charges nothing, which is a fact rather than an unknown — unlike a model's price below, where the same absence is an unknown. **A `null` is not this key's shape**: absence is the CLI's only spelling of "no fee", so a consumer refuses an explicit `null` rather than folding it into the same `0`, which would hide a shape change behind the one default this document has |
| `allow_data_to` | array of string | never | never | the repositories whose code may be sent here, as written, or `["*"]` for a route open to every repository. `[]` when the route names none and is not open |
| `open_to_every_repo` | bool | never | never | the entry's own switch. On the direct route it is `true`: no `allow_data_to` governs a call to Anthropic, whose credential the owner stores for the project rather than per repository |
| `note` | string | **yes** | never | what the entry's own keys do not say: `require_zdr: ...` on a ZDR entry, or `routing.claude.via: ...` on the direct route when the project routes a Claude ID elsewhere. It is untrusted text |

**A model row is one model a run can name.**

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `id` | string | never | never | the model ID as a run names it (a `fugaro.yaml`'s pin, a `model_prices` key, a route's claim). It is the row's own key and the sort order |
| `route` | string | never | never | the name of the route a run would send it down, or `""` when **no route serves it** — the empty string is a fact here, and a screen draws it as "no route" rather than as a missing field |
| `source` | string | never | never | where the price comes from, one of `embedded` (this binary's Claude table), `catalog` (the built-in provider price catalog), `local` (the owner's `model_prices`) or `none` (**the model has no price row at all**). A consumer reads this before it reads a price |
| `input_per_m` | number | **yes** | never | the input price per million tokens (`float64`), or the row's own `priceCatalog.tier_input_max_per_m` when a stale catalog is charged at the top of its tier. **Absent when the price is zero or unknown**: `omitempty`, and `source` is what tells the two apart — a screen that read an absent price as `0` would draw a model nothing prices as a free one |
| `output_per_m` | number | **yes** | never | the output price per million tokens, under the same rule as `input_per_m` |
| `catalog` | object | **yes** | never | a catalog-priced row's own provenance: the `priceCatalog` below. Absent on every other row |
| `allowed_to` | array of string | never | never | the repositories this model may be used by, as the rules compute them, `["*"]` for every repository and **`[]` when none may**: a model no route serves, or one the project's `budget.allowed_models` leaves out. `[]` is a refusal to use, never a missing list |
| `refused` | string | **yes** | never | why the model has no price, when it has none (`table.Refusal`, or the fallback sentence naming `model_prices`). It is the string a launch's own refusal is built on, and a screen draws it as text |
| `note` | string | **yes** | never | the row's spread or its price's own qualification (`entry.CatalogRange()`). It is untrusted text |

**A catalog-priced row's provenance is `budget prices --json`'s own nine
keys** — the same `priceCatalog` shape, so a console that reads one read's
provenance reads both:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `tier` | string | never | never | `zdr` or `all`: the tier the price was taken from |
| `bound` | string | never | never | the end of that tier's range charged, `min` (the cheapest) or `max` (the dearest — whether `price_policy` asked for it or the catalog is stale) |
| `endpoints` | int | never | never | how many endpoints the model is served by. The published schema bounds this at `1` and the catalog loader refuses a zero, so a zero should not arrive; a consumer reads it with a floor of zero all the same, because the figure is drawn as written and refusing it would take the whole read down over a number nothing is computed from |
| `zdr_endpoints` | int | never | never | how many of them are ZDR-eligible (the `zdr` tier's own count) |
| `source` | string | never | never | the catalog URL the price came from |
| `checked_at` | string | never | never | the date the catalog was last checked, `YYYY-MM-DD` |
| `stale` | bool | never | never | `true` when that date is older than the catalog's 30-day rule (design D10); a stale catalog is charged at the dearest end of the tier's range |
| `tier_input_max_per_m` | number | never | never | the dearest input price per million tokens the tier prices: the top of the range `bound` was taken from, and so the most a call served from the dearest endpoint can be billed (design D6, P11) |
| `tier_output_max_per_m` | number | never | never | the same for output |

**A repository row is one recorded repository's pins.**

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `name` | string | never | never | the repository's `owner/name` as the project records it |
| `state` | string | never | never | how the read went: `ok`, `unknown (no checkout on this machine)` when this machine remembers none, or the parser's or the policy's own reason when the checkout's `fugaro.yaml` could not be read at the base branch. **The pins are absent whenever it is not `ok`** — never filled from a guess |
| `coder` | string | **yes** | never | `agent.models.coder`'s model, resolved (`Agent.ModelFor`) |
| `reviewer` | string | **yes** | never | the senior review's model, resolved |
| `background` | string | **yes** | never | the file's own `agent.models.background` |
| `background_effective` | string | **yes** | never | the model Claude Code's background requests actually reach (`config.EffectiveBackground`). **Present only when it differs from `background`**, so a consumer draws `background_effective` when it has one and `background` otherwise |
| `problems` | array of string | **yes** | never | what a launch would refuse this repository on, one line each (`config.CheckProviderPolicy`, the same check a run makes): a pin no route serves, a model the project's `allowed_models` leaves out. `omitempty`, so a repository with nothing wrong carries no key at all |

**Version skew.** Every field is additive within `version: 1` (D784), and the
document is under `schemas/models-ls.schema.json` with `additionalProperties`
true at every level. A CLI older than this command has no such read at all, and
the console shows the panel as unavailable (the availability rule, §1) rather
than an error loop. A field added later is a field an older console does not
draw; a console that requires one must refuse the document rather than fill it
in. **A console must not read an absent `input_per_m` or `output_per_m` as
zero**: that is the one skew in this document that draws a wrong figure instead
of a missing one.

**Nothing here is a spend.** Every figure in this document is a **price** — what
a call would cost — and no key is money already spent. The document names no
bucket, no budget database and no run, which is why no key here is a figure the
caps are enforced against, and why a console shows it as somewhere to go rather
than as money that moved.

**Never on this surface: the writes.** The `models` command group's `add`, `rm`
and `use` subcommands change the project's or the machine's own configuration,
and `search` takes a positional `<text>` no console read needs. The console's
allow-lists refuse all four by name — the three writes as commands and all four
as words (`NEVER_REACHABLE`, `console/src/main/ipc.ts`) — so this section's
document is the group's whole surface on the dashboard.

**The console's row has landed.** This section is the CLI's half of the read,
and the console's half is in the allow-lists' code rather than here: the spawn
row (design §5.1 rule 6's `models ls --json`), the operation (`models.list`)
and the decoder (`decodeModelsList`, `console/src/shared/schema.ts`) were added
by console 0.119.0, with the CLI floor `MODELS_LIST_CLI_FLOOR` (`0.16.0`) in
`console/src/shared/version.ts` — this read landed after v0.15.0, so a console
that spawns it before that version gets no such command. Nothing here
authorises a spawn on its own — §5.1's table and the allow-lists are where a
row is admitted — and this document still describes the CLI's side of the
contract.

**Flags** (`newModelsLsCmd`): `--json`, and the cloud flags `--project`,
`--config`, `--gcp-project` and `--region`. **It takes no argument of its own**
(`cobra.NoArgs`): the console's argv is `models ls --json` plus the project
flags and nothing else.

### 6.19 `fugaro settings --json` (**CLI 0.16.0**)

**Command:** `internal/cli/settings.go` `newSettingsCmd` / `runSettings`, the
read whose `--json` document this section describes; `internal/cli/settings_render.go`
`writeSettingsJSON` prints it, and `internal/cli/settings_read.go` builds the
rows. Cited by symbol, not by line, for §6.11's reason. **The console spawns
the read since the settings-edit plan's C1** (operation `settings.read`,
`console/src/main/ipc.ts`, design §5.1 rule 6's `settings --json --project <name>
[--config <path>]` row), and it is that row's whole argv. Unlike §6.17 and
§6.18 above, this row carries **no console-side CLI floor**: C1 adds none, so a
CLI older than 0.16.0 answers the read with its own refusal (a 0.15.x `fugaro
settings` has no `--project`), which the section draws as the read's own reason.

**Why it exists: every setting, what decides it, and what a write would have to
respect.** The command answers one row per registry key: the value a run gets,
the layer that decided it, the built-in default and its constant, and the state
of the read. It writes nothing — no file, no cache, no cloud object — so it
runs anywhere, a coding agent's session included (D850), and it is the read the
**settings-edit feature** is built on: the three writability members below are
what lets a console draw an edit without keeping a copy of the generic path's
own key allowlist, which is the copy that drifts the day a key is added to the
registry (D1102).

**The document is one object.**

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `version` | int | never | never | the document's version, `1` today (`settingsDocVersion`, pinned by `schemas/settings.schema.json`). Members are only ever added within a version: a renamed or re-meant member is a new name and the old one stays, and removing or retyping one bumps this number (D848) |
| `project` | string | never | never | the project the read was aimed at — the selected project's name, or the empty string when the read resolved none |
| `generated_at` | string | never | never | when the read ran, `RFC3339` |
| `sources` | array of layer rows | never | never | **the layers this read touched**, one row each. A layer that was not read is not listed, so a consumer never sees a source that answered nothing |
| `settings` | array of row rows | never | never | one row per key, in the registry's own order |

**A source row is one layer the read touched.**

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `layer` | string | never | never | the layer's own name (`settings.Layers`, plus the levels below) |
| `read_at` | string | **yes** | never | when that layer was read, `RFC3339` |
| `duration_ms` | int | never | never | how long its read took |
| `status` | string | never | never | what came of it |
| `note` | string | **yes** | never | why, when the status needs one |

**A settings row is one key.**

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `key` | string | never | never | the registry key, dotted (`agent.review_rounds`). It is the row's identity and the sort order, and it is the name every other member describes |
| `scope` | string | never | never | which scope the row answers for: the project's own keys, or a repository's under `--repo`/`--all-repos`, where the same key is a row per scope |
| `value` | string | **yes** | never | the value a run gets. **Absent on a `sensitive: true` row and on no other row**: an absent member is the one spelling a consumer cannot mistake for the value, and the row's own text (`set`, `unset`) is what the human table prints in its place |
| `wins` | string | never | never | the layer that decided the row, in the read's own vocabulary: `default`, `project`, `profile`, `repo`, `flag`, `local`, `shared`, `policy`, `caps` or `env`, plus the two levels `job` and `branch` that `WINS` may name and no `--layer` selects. `default` means **no layer decides it** and the row prints its default |
| `default` | object | never | never | the built-in default: the value and the constant that holds it, or the sentence saying what unset means. **Exactly one of the two is set** |
| `state` | string | never | never | the read's own words for the row: a stale layer's age (`stale 3h`), or `unread: <what could not be read>`. The console's argv deliberately omits `--offline`, so an unreachable layer says so and the row is drawn as unread rather than as a value |
| `sensitive` | bool | **yes** | never | `omitempty`, so it appears only on a sensitive row. **The console drops such a row whole** — no key, no value, no control (D1111, the owner's ruling of 2026-11-01) — which is why the `value` member above is absent for one |
| `writable` | bool | never | never | whether the **generic path** may write this key (`settings set KEY --layer <L> --yes -- <VALUE>`). Written on every row — a consumer branches on the value, never on the member's presence |
| `owner` | string | never | never | the command family that writes the key when the generic path may not (`settingsFamilyCommands`, `internal/cli/settings_set.go`: `fugaro models`, `fugaro rigor set`, `fugaro rigor floor`, `fugaro accounts`, `fugaro secrets`, `fugaro project`), or `""` when there is none. `writable` and `owner` are answers about the same question, and a row with neither is a row no command writes |
| `target_layer` | string | never | never | the layer a write with **no** `--layer` lands in: the narrowest of `repo`, `project` and `local` the key may be set in (`settingsDefaultLayer`, `internal/cli/settings_set.go`), and the layer the console passes back as `--layer` — what the window showed is what is spawned (D1105). **It is a fact about the key and not about the generic path's allowlist**, so it is **non-empty on a row the generic path refuses**: `mailbox.keychain_item` is `writable: false` with `owner: fugaro secrets` and `target_layer: "local"`. `""` is a key none of the three layers holds, which is a row no edit may target — a document from a CLI older than D1102 carries no such member at all, and it reads the same — and **empty is not a layer a screen can pass**: such a row gains no pencil, whatever `writable` says |
| `levels` | array of level rows | **yes** | never | one row per layer that sets the key, for `--why`. Absent when the read did not compute them |

**The default block is the built-in default, and exactly one of its two
spellings is set:**

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `value` | string | **yes** | never | the value the loaders leave in place |
| `source` | string | **yes** | never | the qualified Go constant holding that value |
| `unset` | string | **yes** | never | the sentence saying what **unset means**, where the answer is a decision rather than a value. It is text, and a screen draws it as a sentence |

**A level row is one layer that sets the key** (`--why`):

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `layer` | string | never | never | the layer's own name |
| `value` | string | never | never | what that layer gives the key |
| `outcome` | string | never | never | that layer's part in the result (`wins`, and the tightening or union rules of the keys whose layers combine) |

**The three writability members are the whole point of the console's half**
(D1102), and they are the reason no list of the generic path's keys is kept in
`console/`: `writable` says whether the generic path may write the key at all,
`target_layer` names the layer a write with no `--layer` lands in — the
narrowest of `repo`, `project` and `local` the key may be set in, which the
console passes back as `--layer` — and `owner` names the family that does write
the key when the generic path may not — in S5's own words, so the sentence the
screen draws ("`written by <owner>, not by the generic path`") is the CLI's
refusal sentence and not the console's invention. **The first and the second are
independent**: `target_layer` is a fact about the key, so a row the generic path
refuses still names a layer and is refused by `writable: false`; the empty
`target_layer` is a key none of the three layers holds, and there a screen has
no layer to pass at all. A screen reads the three and draws a control from them;
it never decides for itself which keys are editable.

**A `sensitive` row's value is never in this document.** `settingsJSON` writes
no `value` member for one at all, and the human table prints `set`/`unset`
(`sensitiveText`) in place of the value — the set/unset fact and nothing a
consumer could mistake for the value. The console's own rule is one step
further: the row is **dropped whole**, so not even the key is drawn.

**The write is an operation of the console, and it is the console's second
write** (the settings-edit plan's C3, design §5.1 rule 12, SECURITY-SENSITIVE
because a new write always is). `settings.edit` drives the pair on the argv
§5.1's write row spells and nothing else of the group: the dry run's is
`settings set|unset KEY --layer <L> --dry-run --json --project <name> [--config
<path>] [-- <VALUE>]`, and the confirmed write is the same line with `--yes` in
place of `--dry-run --json` — the flag is on **both** phases, because the
console's child has no terminal to answer the CLI's ordinary y/N with, and what
the dry run's classification decides is the console's own window's shape and not
the argv (§6.20). **Neither `--layer` nor a phase word is a renderer's**: the
layer is this read's own `target_layer`, held in the console's main process, and
the two phase spellings are that process's own — so no payload of a renderer's
can name a layer the CLI did not offer, which is the whole of D1105.

**The group's other words stay off this surface.** `settings get` reads a single
key this screen does not need (the row set is the whole document); the group's
positional keys and globs, its `--repo`, `--all-repos`, `--layer`, `--grep`,
`--changed`, `--why` and `--offline` flags are not words of either row, because
they narrow the table by a rule no screen control sets; and `--add`/`--remove`
is a form no control can choose, since this document carries no member saying a
key is list-shaped. **A refusal is not a document**: the CLI exits non-zero with
its own sentence and prints none of these, so a consumer branches on the exit
and draws that sentence verbatim — the console does, and **no write is ever
retried silently** (D1112): the write's own compare-and-swap refuses an edit
built on bytes of the target file that moved (it does not re-measure the dry run's classification: see the limits in desktop-console.md rule 12), and its `Read again` control re-runs the read rather
than repeating the write.

**Flags** (`newSettingsCmd`): `--json`, the aiming pair `--project` and
`--config` (`settingsAimFlags`), and the read's own `--repo`, `--all-repos`,
`--layer`, `--grep`, `--changed`, `--why` and `--offline`. **It does not carry
the other two cloud flags** — `--gcp-project` and `--region` are `addCloudFlags`'
and this command does not call it — so it is the one row of §8's inventory
whose pair is the command's own flag group rather than that helper's, and the
console's argv is unaffected either way: neither word was ever on it.
**The console's argv is `settings --json` plus the two project flags and
nothing else** (design §5.1 rule 6): the command takes arbitrary positional
arguments, and the row's own `args` is empty, so no renderer names a key, a
scope or a layer.

### 6.20 `fugaro settings set|unset … --dry-run --json` — the write's dry run

**Command:** `internal/cli/settings.go`'s `newSettingsCmd` reaching
`internal/cli/settings_set.go`'s write path; `settingsDryRun.document` builds
the document and `writeSettingsWriteDoc` prints it. Cited by symbol, not by
line, for §6.11's reason. **The console spawns it since the settings-edit plan's
C3** (operation `settings.edit`, design §5.1 rule 12), and it is **not** the
console's read of the settings: §6.19's document is the table the pencil is
drawn from, and this one is about **one edit of one key**.

**Why it exists: one edit, classified by the CLI's own code, before a window is
shown or a byte is written.** The dry run writes nothing — no file, not even the
backup a real write takes — so a console can show the operator what an edit
would do and let the CLI decide whether the edit widens a limit, and it does the
deciding so the console never re-implements it (D1106): the members below are
what the confirmation window and the write are built over, and the window's key,
old value and shape come from this document and from nowhere else.

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `version` | int | never | never | the document's version, `1` today (`settingsWriteDocVersion`, pinned by `schemas/settings-write.schema.json`). Members are only ever added within a version (D848) |
| `key` | string | never | never | the key the edit is about, printed through `safetext.Field`. A consumer reads it to check that the document is about **the** edit it asked for: the window draws the key from here and writes the layer from the read's own row, so a document answering about another key is refused before any window exists |
| `layer` | string | never | never | the layer the edit would write, `local`, `project` or `repo` (`settingsWriteLayers`). A console cannot name it: it is §6.19's `target_layer`, and this member is the check that the child was aimed where the read said |
| `path` | string | never | never | the file the edit would write, printed through `safetext.Field`. It is a path of the operator's machine: a consumer may show it and may never spawn anything built from it |
| `changed` | bool | never | never | the write path's own comparison of the two byte strings — the same comparison that decides whether a write would happen at all — so the document cannot claim a change the write path did not see |
| `widening` | bool | never | never | the write path's own classification, from `settingsCeilingKey`, `settingsLoosens`, `settingsListSubset` and `settingsTypedWrite` (D1106). **A consumer never re-derives it**: a form the CLI cannot compare counts as a widening, and a re-implementation would answer *narrowing* where the CLI answers *widening* — the one direction a console must not get wrong |
| `why` | string | never | never | the CLI's own sentence for a widening (`settingsTypedWhy`), empty for a narrowing: the reason the CLI will demand a typed project name, shown beside the fields it explains |
| `old` | string | never | never | the value the TARGET layer's own file holds for the key before the edit, which is not the value a run gets (the winning layer decides that). Empty when that layer's file holds none |
| `new` | string | never | never | the value the edit would write. Empty for an unset, which is the fact that makes the pair meaningful: `changed` is the CLI's comparison and not a consumer's reading of these two strings |
| `refusal` | string | never | never | **empty in every document this CLI prints.** A refusal is the CLI's own sentence with a non-zero exit and **no document at all**, which is what a consumer branches on (D1106, D1112); the member is written out because the shape is the contract, and a consumer reads it by name |
| `operation` | string | unless the write is one that has a word of its own | never | **which edit this is, where the write has a word of its own for it** — the three the schema's enum carries: `add-entry` (`settingsEntryAdd`) and `remove-entry` (`settingsEntryRemove`) for the entry pair (D1164), and **`remove-secret`** for `fugaro secrets rm NAME` (`secretsRmDryRun`, D1171), which takes away the entry the name has — `project_secrets.NAME` for a reserved name, **`workflow_secrets.NAME` for a workflow secret** (D1260) — *and* the Secret Manager container behind it. It is the write path's own word and a consumer **never re-derives it**: `key`, `old` and `new` alone cannot say which of the three the CLI decided, because a map's removal and an object list's read alike in them. `remove-secret` is the one value §6.20's own pair never writes — that command is `settings set\|unset`, and this section is its dry run — and it is also the value that carries **no** entry member below: what it removes is addressed by `key` alone |
| `entry_key` | string | unless the write is an entry write | never | **the field the entry is addressed by**, printed through `safetext.Field`: an object list's own key field — `name`, for `workflows.*.secrets`, the pair's only keyed list (`settingsEntryBlocks`) — and the member's own name for a map, whose entry *is* one member. It is an address, not a value a form is drawn from |
| `entry_value` | string | unless the write is an entry write that carries one — absent for a map's `remove-entry` too | never | that field's value — the key field's value for an object list, the member's value for a map's add — printed through `safetext.Field`. **A map's `remove-entry` leaves it out**: its address is the member's name and nothing else, what the member held is in `old` as its own `field=value` pair, and a document omits a member it has no value for rather than writing an empty one |
| `entry_fields` | `map[string]string` | unless the write is an object list's `add-entry` | never | the members the entry carries **beside** the field that names it, keyed by member name, both sides printed through `safetext.Field`. **Only an object list's `add-entry` has any**: a map's entry is one member and carries none, and no removal carries any. The table spells it the one way a cell carries an entry — the pairs sorted by field and joined with `, ` (`settingsEntryFieldsString`) — which is the same spelling `old` and `new` carry an entry in |

**Version 1 has grown four optional members, and a scalar write's document is
the shape it has always had** (D1164, under D848's rule that a version gains
only members a document may leave out): they are `omitempty`, and the entry pair
writes them — `settingsEntryAdd` and `settingsEntryRemove` set the three the
entry carries, `operation` is the fourth, `secretsRmDryRun` writes it alone, and
it carries none of the other three, because what `remove-secret` takes away is
addressed by `key`. A map's `add-entry` sets no `entry_fields` either: a map's
entry *is* the one member `entry_key` names.
**The console's own row builds no entry write at all** — `settings set KEY
--layer <L>` carries one key and one value, and `--add-entry`/`--remove-entry`
are not words of it (design §5.1 rule 6, and no screen control can choose a form
whose row would have to say a key is map- or list-shaped). A consumer that pins
this version therefore **ignores these four and reads the ten members it has
always read**: an entry write's document reaches no window this console draws,
and a reader that meets one in the CLI's own output finds the entry's address in
the entry's own terms rather than inferring it from `key`.

**Every string is printed through `safetext.Field`**, for §6.19's reason: a key,
a path and a value are as much the file's and the command line's as they are the
program's, and a console draws them as text rather than as anything a renderer
built.

**The table shape carries the same facts.** Without `--json` the command prints
one `FIELD`/`VALUE` row per member, in this document's own order, so the two
shapes cannot drift into two contracts.

**The phase words are exclusive, and that is the command's own rule**
(`settingsDryRunFlags`, `askConflict`). `--json` without `--dry-run` is refused
("`--json` is the dry run's document"), `--dry-run` with `--yes` is refused too
("a dry run says what the edit would do and writes nothing, `--yes` answers a
write"), and so is `--dry-run` with **`--ask`**, the slot's third word: `--ask`
is about the confirmations a write is given, which a dry run has none of and
asks for none of (a managed project writes on the gate's verdict and asks
nothing, and `--ask` is the flag that puts those questions back), and `--ask`
beside `--yes` is refused as well, because the two are the slot's two answers to
the same question. **A console passes exactly one of the two spellings this
document is about** — `--dry-run --json` or `--yes` — and the CLI is what makes
that a property rather than a convention: `--ask` is not a word of the console's
argv (design §5.1 rule 6), because the questions it restores are asked at a
terminal and the console's child, whose stdin is ignored, is given `--yes`
instead. **The layer is never guessed by the console** either: the CLI itself falls back to the narrowest layer when `--layer` is
omitted, but the console always names one (`argvFor` refuses a settings edit without it), the layer must be
one `settingsWriteLayers` names, and a layer outside them is a refusal rather
than a document. The command is the same pair §6.19's paragraph describes, so the
`--`-terminated value, the two spellings the console passes and the layer's
provenance are written down there and not twice here.

### 6.21 `fugaro managed status --json` — a managed project's marks, its trail and its last update

**Command:** `internal/cli/managed_status.go` `newManagedStatusCmd` /
`managedStatus`, with the document built as `managedStatusDoc`, its trail read
by `managedStatusTrail` and the last update's retirement read by
`managedStatusRetirementsOf`. Cited by symbol, not by line, for §6.11's reason.
**It is the read the managed screens are built on**, and it is a command of its
own rather than two members on doctor's document (the managed-screens plan's
CM2, D1473): doctor's `--json` is a list of `id`/`ok`/`problem`/`fix` checks
with no room in it for records, while a screen wants one closed document about
one project — which is what the coordinator's stored read (§6.13) already is
for the coordinator. **The console's own rule 6 row for it —
`managed status --json --project <name> [--config <path>]` — is that plan's
CM3, and the row is on the table**: the design's §5.1 table carries it, the
CLI's `consoleReadPaths` carries the command, and the console spawns it as
`managed.status` (CM4). The shape is written down here because that row and the
screen built on it (CM5) are what it is read against, and a shape that is not
written down is a shape that drifts.

**Why the CLI's own facts and never its prose** (D1472). The managed state is
available today elsewhere only as doctor's `managed-audit` check, whose answer
is two sentences (`problem` and `fix`); a screen that parsed them would break on
the next rewording of either, silently, and would draw a *wrong* state rather
than nothing. So every fact a screen draws is a member below, computed by the
read that already computes it — `managed.ReadMarkFacts`, the one reader of the
marker, whose `MarkFacts` carries the version, the reason and the generation
(D1474) — and by the audit family's one parser, `managed.ListAuditRecords`,
which is the reader the record's own writer ships. Nothing here is a second
parse of either.

**The document is one object.**

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `project` | string | never | never | the selected project's name, as the project config spells it |
| `gcp_project` | string | never | never | the GCP project the marks were read from |
| `managed` | bool | never | never | the marks' own answer: true only when the creation fact in the runs bucket and the `fugaro_managed` label on the project **agree**, which is the read a managed apply makes too. It is a fact about the project and never about the machine that asked |
| `reason` | string | never | never | the read's own sentence for a project that is **not** managed, and `""` when it is (D1474). Bounded text, sanitised by the read. A screen shows it beside `managed`, never instead of it |
| `version` | int | never | never | the creation fact's version (`1`, `2` or `3`), and `0` when the read believed no object. `0` is a fact about the project — an unmanaged one has no creation fact — and not an unknown about the read |
| `generation` | string | never | never | the creation fact object's own generation, in decimal, and `""` when no object was placed. **A string and not a number**, spelling the record's own `marker_generation`: an object's generations start at `1`, so a `0` here would be read as a generation rather than as the absence of one |
| `audit` | object | never | **yes** | the bounded subset below — the newest record of the window — and **null** when the window holds no record, or when the project is not managed and the trail was not read at all. Null is not "nothing happened": `audit_state` is what tells a read that did not answer from a day with nothing in it |
| `audit_day` | string | never | never | the UTC day `audit_count` counts and `audit` belongs to, spelled as the family spells a date directory (`2006-01-02`). **`""` exactly when no record was found in the window**, or when the trail was not read: a day with nothing in it is no day to name |
| `audit_count` | int | never | never | how many records are filed under `audit_day`, and `0` in both of the cases above |
| `audit_state` | string | never | never | the sentence for a read that **did not answer**, and `""` for one that did. Bounded text. It is set only for a managed project: a project that is not managed files no record at all (D1025), so there is no trail to read and no state to report about it |
| `retirements` | object | never | **yes** | the project's entry in **this machine's record of its last plain update** — the entry below plus the record's `version` — and **null** when this machine has recorded no update for the project. Null is **not** "nothing was retired": that is an entry whose `retire` is `[]`, and the two are different facts about the machine (D1484). It is a local read and it is made whatever `managed` says, because a project that is not managed runs updates too |
| `retirements_state` | string | never | never | the sentence for a record that **could not be read** — JSON this build cannot parse, a byte count over its bound, a file that is not a regular file, or a version this build does not write — and `""` for one that was. Bounded text. It is `audit_state`'s rule one subject over: a record that could not be read drawn as "no update recorded yet" would be a claim about the machine made out of a failure to read it. Unlike `audit_state` it is not gated on `managed` |

**The `audit` member is the bounded subset, and the bound is the point**
(D1476):

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `at` | string | never | never | when the command ran, in UTC, RFC 3339 to the second. It is the record's own time and never the listing's order |
| `command` | string | never | never | the command as a person types it (`fugaro config publish`), which is the record's own name for it and not its argv. Bounded text |
| `verdict` | string | never | never | the gate's own answer about the judged plan: `admitted` or `refused`. **A refusal is a record an owner reads most**, so it is drawn beside an admission and never instead of it |
| `identity` | string | never | never | the account the write ran as: the deployer account for a managed apply, the operator's own elsewhere. Bounded text |
| `plan_digest` | string | never | never | the sha256 of the exact document the gate judged, so a reader compares the record against a plan rather than believing a summary of it |

**What is never in it, and it is the whole of the bound: the argv.** The record
itself carries the argv the write ran as, the rules the gate fired and the marks
that were in force; none of them reaches this document. The argv is the argv of
a session — a path, a root name or a title an operator did not mean to publish —
on a surface anybody at the machine can read, and the five members above are
also what makes a row stable across CLI versions (D1476).

**Two days, and the day that answered** (D1477). The trail is the newest record
of the current UTC day, and of the previous day **only when today holds
nothing**: the shape doctor's check already reads, so a session that ran an
apply twenty minutes before midnight is still the last one an operator reads
about. `audit_day` names the day the answer came from — today, yesterday, or
nothing at all — and `audit_count` counts that day's records, so the two are
read as a pair. There is no date control and no paging: a date the operator
picks would be a second word on the spawn row, a new bound on the read and an
empty state for a day with nothing in it, for a trail the two-day window already
answers.

**An unread trail is a state, and never an empty day.** A listing that failed —
a refused grant, an outage, an object the parser refuses — is an unknown, and an
unknown drawn as `audit_count: 0` is the silent pass: "nothing was recorded" and
"the records could not be read" look identical in a count, and only the second
is a reason to look further. So a read that did not answer carries
`audit_state`, with the five members above empty and **no `audit` at all** — a
figure beside a state would be read as an answer — while an empty day is
`audit: null` with `audit_state: ""`. **A read that did not answer is not an
exit**: the command prints the document and exits zero, because the state is the
answer. Only a read that could not be made at all — the marks, which are the
document's whole subject — is a non-zero exit with no document.

**It never says "revoked"** (D1475). A run-time fact of a write gate is
persisted nowhere, so a project whose marker or label was taken off is *not
managed*, which is a different claim from a mark that was revoked; a screen that
drew the second from the first would be showing a state the CLI does not have.

**A project that is not managed is not read for a trail at all.** `audit`,
`audit_day`, `audit_count` and `audit_state` are null, `""`, `0` and `""`: a
managed project is the only one that files a record (D1025), so an object left
in the bucket for a project whose marks do not say so is not a fact about that
project, and reading it anyway would let a trail outlive the marks that justify
it.

**The last update's retirement is in this document too** (the managed-screens
plan's CM8; F21, D1484, D1485). The Project tab draws what the last plain
`fugaro update` retired by itself and the runs it walked past while it did, and
nothing else on the machine remembers that once the run is over: the summary is
printed once and the plan file is overwritten by the next update. The record of
it is CM1's own document on disk (`localcfg.UpdateProject`,
`$XDG_CONFIG_HOME/fugaro/state/update.json`, beside the remembered checkouts),
and this is where a screen reads it — **the same argv the marks are read with**,
so a console spawns nothing extra for the retirement, which is what F21 means by
filing the record as one of the managed screens' subjects. It is a **local**
read, and the marks do not gate it: a project that is not managed runs updates
too. **The entry is the record's own type**, embedded rather than retyped into a
second struct with the same tags, so a member the record gains arrives here with
this section moving or the contract test failing rather than as a row a screen
has been told nothing about.

**The three answers are three.** An entry is the record and its `version`. Null
with no state is "this machine has recorded no update for the project" — a
record file that is not there is an empty document and not an error. Null **with**
a state is a record that is there and could not be read, and the state is the
whole of why the member exists.

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `version` | int | never | never | the version of the record the entry was read from (`localcfg.UpdateRecordVersion`, `1` today). It is not a member of the record's entry: a consumer compares it against the shape it knows before it draws a row, and an entry of a version it does not know never reaches it — that answer is `retirements_state` |
| `at` | string | never | never | when the run **settled its retirement**, as the record spells it: RFC 3339 with the offset the update ran in, and nanoseconds when it has them. It is **not** UTC: the record is written on the operator's own machine, from that machine's clock and zone, and this document carries the entry rather than re-spelling it — so a consumer draws it as it stands and never assumes a zone (`audit`'s `at` is the CLI's own UTC rendering, and the two are not the same claim). It is not the end of the command either, which may go on to build images and write the local config: the record's subject is the retirement, and `at` dates that |
| `retire` | array | never | never | the retired addresses, in the run's own order, and the rows below. **`[]` is "the last update retired nothing"** — a set that was written — and never null, which is the record's own spelling |
| `runs_in_flight` | int | **yes** | never | how many runs the registry held when the retirement was settled. **Absent when the number is not known** — the project has no budget backend, or the read did not answer — and never `0` in its place: a run that could not be counted must not be drawn as a run that was not there (D1483) |

**One `retire` row is one address, and what a screen may draw of it is D1485's
closed list.** The nine members are the record's own, member for member with
`infra.Retirement` (and with the run's own `--json` rows), because the record is
a document the console already knows how to draw. A screen draws seven of them:
`job` and `region` are the members the later state-removal steps need, and they
are carried here for the same reason the record holds them — the document is the
record, and a screen that reads the keys it draws and only those leaves them in
no cell. **A secret is drawn as its name and never as a value**: the record
holds a container's `logical` name and its Secret Manager `secret_id`, both
identifiers, and no secret's contents are in this document at all.

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `address` | string | never | never | the Terraform address that leaves the state |
| `kind` | string | never | never | what it was: `secret container`, `workflow job`, `check job`, `IAM member`, … |
| `cause` | string | never | never | the config change that retired it, in the user's words |
| `kept` | string | **yes** | never | what survives the retirement, and **absent when nothing does**. A secret container's promise is `infra.KeptSecret`: the address leaves Terraform's state and nothing deletes the container or its versions |
| `logical` | string | **yes** | never | a secret container's logical name, absent otherwise |
| `secret_id` | string | **yes** | never | its Secret Manager **ID — a name, not a value** — absent otherwise |
| `job` | string | **yes** | never | a job's Cloud Run job name, absent otherwise |
| `region` | string | **yes** | never | its region, absent otherwise |
| `protected` | bool | never | never | whether an ordinary apply can delete this address at all. It is not implied by the address: a secret container and the registry carry `prevent_destroy`, and a job whose state says `deletion_protection = true` does too |

**The retirement half is the `--json` document's, and the text rendering is
unchanged by it.** What a person reads from this command at a terminal is the
marks and the trail: the update that wrote the record printed its own retirement
summary when it ran, and a screen is what this half is for.

**Flags** (`newManagedStatusCmd`): `--json`, and the aiming pair `--project
<name>` and `--config <path>` that every project read carries. `--gcp-project
<id>` and `--region <r>` come with `addCloudFlags` and are not part of the
console's row. The command takes no argument, writes no cloud object and keeps
no cache of its own, and needs no checkout to answer — so it runs anywhere, a
coding agent's session included.

**It is in the console's read set, and that is one local file.** The command
resolves the selected project the way every cloud command does: `selectProject`
resolves the working directory as a checkout on the way to the installation, and
that resolution remembers the checkout it ran in (`recordCheckout`,
`internal/cli/project.go`) unless the command's own path stands on the read-only
list that stops it (`consoleReadPaths`, `internal/cli/project.go`, §6.8).
`managed status` **is** on that list: CM3 added it in the same change as the
design's §5.1 rule 6 row for it, because a `consoleReadPaths` entry whose argv
the design's own table does not allow fails the test that reads that table back
out of the design. So a console that spawns it — and `managed.status` does
(CM4) — writes no `$XDG_CONFIG_HOME/fugaro/state/checkouts.json` in an enrolled
checkout: the state file §6.8's `checkouts` reads keeps the directory a person
opened, and no cloud object is written either.

### 6.22 `fugaro graph --json` — the dependency graph, and its stream

**Command:** `ee/graph/cli/graph.go`, registered through `RegisterCommand`
(`ee/register/graph.go`) — the graph plan's CG3. It is a **paid command** like
the Coordinator tab's, because its `all` scope is the coordinator's stored view
(D1362, F26); the plan scope the Graph already draws stays where it is.
**Document:** `graph.Document`, `graph.SourceState`, `graph.Node` and
`graph.Edge` (`ee/graph/document.go`), with the node key built by
`graph.NodeKey` and the `--source` value parsed by `graph.ParseScope`
(`ee/graph/key.go`). Cited by symbol, not by line, for §6.11's reason.

**`fugaro graph --json`, its stream and the console's one-shot read have
landed; the console's live half has not.** CG1 (the document, the key and the
scope pattern), CG3 (`graph --json`, the coordinator scope), CG7 (`graph
--watch --json`, the stream) and CG9a (the console's own read of this document —
the `graph.read` spawn row, its operation and bridge method, and the decoder
`decodeGraphDocument`) are on `main`, so the command, its `--source` set, every
field below and every rule of the stream — a whole document per change, the
one-second floor, the keep-alive, a `stream_id` with a rising `seq`, a scope that
is down as a state — are what the released CLI writes. The one exit is a
**refusal**, which is a read this CLI may not make (below); a listen the server
*withdrew* is not one of them — that scope goes `down` and the child stays alive
with its last graph and its clock, which is the rule §10.6 gives `watch` for the
same event and the rule the graph plan's CG7 pins for this scope, so a withdrawn
feed is a state a screen ages rather than a child it waits to exit. The
console's subscription to that stream, and the
drawing of what it carries (CG9b, CG11), are the tasks that follow: the read the
console makes today is the one-shot one. The fields are read off
`ee/graph/document.go`'s struct tags and the rules off the plan's H1 to H5, so a
task that finds the code disagreeing with this section has found a defect in one
of the two.

**The section is numbered §6.22, and that is not a typo.** §6.21 is already
claimed for another command's document: `docs/console-managed-screens/implementation-plan.md`
gives it to `fugaro managed status --json` (its CM2 writes it), so the graph's
takes the next number free rather than the claim written down first.

**It needs no checkout, and that is the point of the read.** `plan status`
(§6.12) and `plan graph` (§6.9) read the plans of the installation's checkout and
show a card when there is none (F10); `graph` asks the project's database what
the coordinator's sweep stored — the read §6.13 already makes, through the
caller's own credentials (`roles/firebasedatabase.viewer`) — so a scope paints
with no checkout at all. **The one fact it does take from the local config is
each repository's provider**, which that repository's slug is keyed by (§6.13's
`provider` row is the same resolution): a `repos` entry that leaves `provider`
out has no provider to find unless the working directory *is* that repository's
checkout, so in a console child it is refused with the sentence that names the
entry to set — a refusal of the whole read, with no document, which the console
answers as it answers any other (T3, keep the plan scope and show the sentence),
and never a `down` or `unreadable` state. Onboarding writes every repository's
provider, so this is the hand-edited entry and not the ordinary one. **The plan
scope keeps its checkout and its card**; the new scopes are what the dropdown
adds beside it (D1356).

**`--source` selects the scope, and the set is closed.** `all` is the default and
is the union of the scopes that exist — today the coordinator alone —
`coordinator` is the stored view, and `orchestrator:<id>` and `local:<id>` are
**refused with "no orchestrator is installed in this build"** until CG4 and CG5
register their readers: an honest refusal, never an empty graph, because a screen
must be able to tell "nothing here" from "not asked". The value reaches argv from
the renderer, so the CLI refuses everything but the closed set below **before it
reads anything** (H1), and the console sends only a value a previous read listed
(F21):

| Value | What it names |
| --- | --- |
| `all` | every scope that exists: today the coordinator's stored view, and the orchestrator scopes as they are built |
| `coordinator` | the coordinator's stored view: every repository it has reconciled (§6.13), with no checkout |
| `orchestrator:<id>` | one orchestrator's node under `orchestrator/` — **refused today**, CG4's reader |
| `local:<id>` | one local orchestrator, read through its node and its cache file — **refused today**, CG5's reader |

An id is one lower-case letter or digit, then up to 39 more of those or a hyphen,
and nothing else. So a leading `-` (which would reach argv as a flag), a path
separator, an uppercase letter, whitespace, a `.`, an `@` and a 41-character id
are each refused, by `graph.ParseScope`'s anchored pattern.

The document:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `version` | int | never | never | the document's own shape version, `1` today (`graph.DocumentVersion`). **A field's meaning never changes under it** (the rule at the top of this document): adding a field is compatible, removing or retyping one bumps the version, and a field whose meaning must change gets a new name. A reader reads the version first and refuses one it does not know rather than guessing — and the Go side reads the document **strictly**, so an unknown key is an error and never a key quietly dropped (`ParseDocument`) |
| `stream_id` | string | never | never | the stream this document belongs to. **`""` in a one-shot read** — the value that answers "not a stream" — and a value drawn once per `--watch` child, printed in every document that child writes |
| `seq` | number | never | never | the document's place in its stream: **`0` in a one-shot read**, and `1`, `2`, … within one `stream_id` in a stream, rising by one per document. It is never compared across `stream_id`s |
| `generated_at` | string | never | never | RFC 3339; when the document was assembled. A **clock**, not an as-of: what a scope's data is worth is `sources[].as_of`, and a screen that draws an age draws it from that and never from this |
| `sources` | array | never | never | one entry per scope the document covers, below. `[]` is a fact — the document covers nothing — and never a null: an empty list is written as an array (`MarshalJSON`), and a `null` read back is the empty list it means |
| `nodes` | array | never | never | the graph's nodes, below. `[]` is a fact — the scope has nothing to draw — and the scope's own state sits beside it in `sources`, so a screen tells "empty" from "down" (H3) |
| `edges` | array | never | never | the relations between nodes, by `key`, below. **The coordinator's stored view has no edges, so a document drawn from it alone carries `[]`** (F17); the orchestrator scopes' `depends_on` are what fills it (CG5) |
| `warnings` | array | never | never | what the read could not do, as sentences fit to print; `[]` is the ordinary case. Sanitised with `safetext.Field` like every other string here |

one entry of `sources`:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `source` | string | never | never | the source this state is about (`graph.Scope.String`): `coordinator`, `orchestrator:<id>` or `local:<id>`, and **never `all`** — `all` is a selector and not a source, so a read that selected it (`graph --json`'s default) writes one entry per scope that exists, each named for itself, which today is one `coordinator` entry. It is the source half of every `key` that came from it, and a value a console may send back as `--source` |
| `state` | string | never | never | one of the four words below. **The whole answer**, so a screen draws it as a word and never as a colour alone (H3, H10) |
| `as_of` | string | never | **yes** | RFC 3339; when this source's data was last known good. **Null when there is no such time**, which is a source that has never read: null is not "now", and a screen must not draw an age from it — it draws one from this field when it is set, and never from `generated_at`. **The coordinator's stored view carries no time of its own** — it is a function of its inputs, which is why the Coordinator screen claims no age for it (CG2) — so a caller that read the views without knowing when the sweep stored them has none to give and prints null, and **`graph --json`, a one-shot read, is that caller**: its coordinator scope has a null `as_of` and is never stale by age. The stream (CG7) is the caller that knows its own read's time and can fill it; the sweep's own marker is a separate fact and not this field |
| `reason` | string | never | never | why, in words, when the state is not `up`; `""` when it is. The read's own sentence, sanitised like every other string here, and the field a screen shows **beside** the state rather than instead of it |

**The four states, defined.** A scope that is down is a **state and not an
exit** (H3): the read answered, and this entry is its answer.

- `up` — the source was read and its data is what it is. `reason` is `""`.
- `down` — the source answered and has nothing stored. **The coordinator's
  scope is down when the project lists repositories and the sweep has stored a
  view for none of them** (CG2; `CoordinatorSource.Nodes`): the read succeeded,
  so this is neither a refusal nor `unreadable`, and what the document still
  carries is the half that needs no stored view — each repository's runs — so
  `nodes` is **not** empty and `reason` is the read's own sentence, `the
  coordinator has no stored view for any of the project's repositories`. A
  screen draws those nodes with the state beside them, never an empty graph.
  The other scopes are down when they cannot be reached at all: a withdrawn
  RTDB stream, a local orchestrator whose node is gone. Either way the state is
  one scope's: the other scopes keep their figures, and a stream keeps
  streaming.
- `stale` — the source answered with data known to be old. The coordinator's
  scope is stale when the sweep's own marker stands beside a view it could not
  recompute (CG2's `RepoView.Stale`): that marker is the age fact the stored read
  carries, and its `reason` is the scope's. The other way is a caller that
  supplied an `as_of` older than twice the sweep period (the sweep is the
  15-minute Scheduler job, F16; CG2), and the instant it is old as of is then
  that `as_of` — which a one-shot read, `graph --json`'s, does not supply, so for
  it `stale` is the marker alone.
- `unreadable` — the source answered with something this build cannot read. It
  is a state of the source, never a refusal of the whole read. The coordinator's
  scope answers it in two cases, and they are one class: the database holds a
  view, or a stale marker, that its strict decoding refuses — an unknown field, a
  value of another shape — or it holds a record whose identity this build cannot
  carry into a `key`, because sanitising it would rewrite it and a rewritten key
  is one two things can share (D1356, `Sanitise`); the scope says it cannot read
  the answer rather than printing a key that could merge two nodes. `reason` is
  the refusal's own sentence, so a screen says what it could not read rather than
  showing a graph with records quietly missing. The scope's `nodes` is **empty**
  in both cases: a screen draws the state and the reason, never a partial graph.

**A refused read is the other case, and it is terminal.** An invalid `--source`
(H1), or a scope this build does not have (`orchestrator:` and `local:` before
CG4 and CG5), is an error with a non-zero exit and **no document at all**, naming
the command — which is what the console reads as "this CLI has no graph" and
answers by keeping the plan scope and a refusal sentence (T3, CG10). A repository
whose `repos` entry names no provider is refused the same way (above), and so is
a database the scope's read could not reach — an IAM refusal, a connection that
failed: those reads made no answer at all, where `unreadable` is an answer this
build cannot read and a `down` source is one that answered with nothing stored.
A refusal and a `down` source are told apart by exactly that: one is a process
that printed nothing and failed, the other is a document with the state in it.

one entry of `nodes`:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `key` | string | never | never | `<source>:<id>`, built by `graph.NodeKey` — the one place the join happens, so a reader that takes a key apart and a writer that builds one cannot drift. **Unique within one source and not across them**: `coordinator:42` and `local:o1:42` are two nodes, and nothing dedupes across sources (D1356). **A reader takes a key apart with the `source` and `id` beside it and never by splitting on the first colon**, because a local scope's own string carries one: the key `local:o1:42` is source `local:o1` and id `42` |
| `source` | string | never | never | the scope the node came from, as `sources[].source` spells it: the identity half of the node and not prose, refused rather than sanitised for `id`'s reason |
| `kind` | string | never | never | one of `pull_request`, `run`, `task` (`graph.NodePullRequest`, `NodeRun`, `NodeTask`) — the kinds this document knows today. The coordinator scope produces the first two: its pull requests and the runs joined to them (CG2). `task` is the orchestrator scopes' (CG5) |
| `id` | string | never | never | the source's own id for the thing: the other half of `key`, and the pull request's number, the run's id or the task's id as that source writes it. **Identity, not prose**: `source` and `id` are the two fields the read refuses rather than sanitises, because `safetext.Field` is lossy and two raw ids that collapse to one string would otherwise share one key (D1356) |
| `title` | string | never | never | what a person reads. **Untrusted display text**: sanitised with `safetext.Field` on construction, and re-sanitised by the console's own class before a renderer sees it |
| `repo` | string | never | never | the repository the node belongs to, `owner/name`. **Untrusted display text**: a screen draws it as text and never as a link, a path or an attribute |
| `state` | string | never | never | the source's own word for the node's state — what a pull request, a run or a task carries in the source it came from, carried through with no translation here, because the document has no vocabulary of its own. `""` is a source with no state to give, not a gap |
| `release` | string | never | never | the release the node belongs to, as the source spells it — the coordinator's stored records each carry one (`coordinator.Record`) — which is what groups the coordinator scope's nodes. `""` means the node names no release |
| `updated` | string | never | **yes** | RFC 3339; when the node last changed. **Null when the source does not say**, and null is not "just now" |

one entry of `edges`:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `from` | string | never | never | the `key` of the node the relation starts at |
| `to` | string | never | never | the `key` of the node it ends at |
| `kind` | string | never | never | the source's own word for the relation (`depends_on` is the one the orchestrator state carries, F14); the document defines no vocabulary of its own. An edge whose two keys name no node in the same document is drawn as nothing rather than as a node of its own |

**`graph --watch --json`: whole snapshots, one per change.** The stream prints
whole documents rather than event deltas (D1360, F19). The first snapshot arrives
within a bounded time, then one document per change, at most one a second
(`watch --json`'s own floor, `watchJSONGap`), each written **compact, one object
per line** — so §10's guarantees and the console's reader apply to it unchanged:
a slow reader makes the child block and never drops a document (§10.3), and a
closed stdout ends the child on its next write (§10.4). **A lost document needs
no repair**: the next one is the whole graph. §9 rule 2 counts the
line-delimited commands of the console's own set, and the graph is not in that
set yet — the spawn row that puts it there is CG9b's, and that task moves the
count with its row.

**`stream_id` and `seq`, and why both.** Each child draws a `stream_id` once and
prints it in every document it writes; `seq` starts at `1` and rises by one per
document within that child, and it is **not comparable across children** — so the
console compares a `seq` only inside one `stream_id`, and treats a new
`stream_id` as a whole new snapshot whatever numbers the two carry (H4, F20). A
one-shot read carries `stream_id: ""` and `seq: 0`, the two values that answer
"this is not a stream" without a flag a reader has to be told about.

**A keep-alive, because a quiet graph prints nothing.** A hard-killed console
(SIGKILL, a crash) leaves a child that exits on its **next stdout write** (§10.4,
F11), which for a graph that does not change could be minutes: so the stream
writes a keep-alive document **on a timer — every 30 seconds** — carrying the
same `stream_id`, the next `seq` and the sources' states (H4, F20). There is no
`keep_alive` field: the tick is an ordinary document of this shape, and a reader
applies it like any other. A reconnected RTDB stream prints a **full snapshot**
and the next `seq` rather than a delta — the client restarts its own subscription
and each connect begins with a full `put` of the subtree (`rtdb.Client.Stream`)
— so a reader never has to ask what it missed.

**One subscription per scope.** The child subscribes to **one** `coordinator/`
subtree per scope it covers, shared across that scope's nodes rather than opened
once per node (H6, F12): an RTDB stream is a held connection, and the console
runs this one only while the Graph tab is visible and counts it in
`MAX_LIVE_FEEDS` (CG9b, CG11).

### 6.23 `fugaro security paths --json` — the list in force, and the answer per path

**Command:** `internal/cli/security.go` `newSecurityCmd` / `newSecurityPathsCmd`,
with the document built as `securityPathsDoc`. Cited by symbol, not by line, for
§6.11's reason. **It is the reader an agent calls instead of guessing** what a
repository's runs treat as security-sensitive (the security-classification
design's SC3, D1512). It is a command of its own rather than a verb under
`config` for D1512's own reason: `config`'s subcommands read the repository's
configuration, while this reads the resolution a run is governed by, whose
base-branch half no `config` verb reaches at all.

**The document is one object.**

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `project` | string | never | never | the project the repository's own `fugaro.yaml` names at `base`, and `""` when it names none — or when the file could not be read at all, which is `error`'s case |
| `repo` | string | never | never | owner/name of the checkout this answered for. The command takes no `--repo`: the list is a file at a ref of the checkout it is standing in, and a name would be a repository whose refs this process cannot read |
| `base` | string | never | never | the branch the list was read at: the ref the next run from this checkout is cut from, resolved through `config.EffectiveBaseBranch` |
| `floor_source` | string | never | never | the layer the engine's own rows carry in `security.patterns`: `floor`. It is what lets a consumer take the floor's rows without scanning the list for a name, and it is `floor` in every answer this slice can give (D1504) |
| `security` | object | never | never | the list in force, below |
| `checked` | array | never | **yes** | one answer per `--check` path, in the order they were given, and **null** when the command was asked for the list alone: a caller that named no path gets no answers, rather than an empty array that reads like "nothing is sensitive" |
| `error` | string | never | never | why the repository's own file could not be read at `base`: this checkout holds no such ref, holds no `fugaro.yaml` at it, or holds one with no place in it for a project name. Bounded text that **names a ref or a line and never a value read out of the file**, and `""` when the read answered |

**`security` is the list in force:**

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `patterns` | array | never | never | the rows, floor first, in the order a run matches them |
| `count` | int | never | never | how many rows `patterns` carries. It is a member rather than `len(patterns)` at each reader, because a consumer that draws a figure wants one number to draw |

**One row of `patterns`:**

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `name` | string | never | never | the row's name: what the run record, the pull request body and `fugaro watch` print when they say which rule decided (D24) |
| `match` | string | never | never | the row's glob, repository-relative, in the doublestar dialect the whole tree matches with (D1502) |
| `category` | string | never | never | one of `credentials`, `guards`, `targets`, `infrastructure`, `prompts`, `CI`, `artifacts` (`sensitivity.Categories`; the seventh word is that design's own, D1500) |
| `source` | string | never | never | the layer the row came from: `floor`, `project` or `repo` (D1512). A project's row and a repository's row are added to the list, never subtracted from it (D1506) |

**One entry of `checked`:**

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `path` | string | never | never | the path **as it was asked for**, never a cleaned or rewritten spelling of it: the answer is keyed by what the caller typed |
| `sensitive` | bool | never | never | whether the list in force treats the path as security-sensitive, and the fail-closed rule below is the whole of it |
| `matched` | object | never | **yes** | the row that decided the answer — `{name, category, source}`, the same three words a `patterns` row carries — and **null** when no row of the list in force matched. Null is a real answer and not a missing one: the list says nothing about this path |

**`sensitive` fails closed, and that is the field's whole rule** (F22, D1512). It
is true when a row of the list in force matched the path — the list is a
**positive** one, and this field answers what it says, not what it leaves out —
and true when the reader cannot place the path at all: a spelling that is not a
clean repository-relative path means more than one path, or names none inside
the repository, and no list can speak about it. The spellings are the ones
`TestSecurityPathsCheckFailsClosedOnAnUnplaceablePath` holds, drawn from
`sensitivity`'s own table (`pattern_test.go`'s
`TestUnplaceablePathMatchesNoRuleAndClassifiesSensitive`): `/etc/passwd` is
absolute, `docs/../website/src/x.md` escapes, the empty string is no path at
all, and a bare `.` and a bare `..` are the repository root as a directory
reference and the tree above it — the pair the matcher's own rule *accepts*,
since `path.Clean` leaves both exactly as they are, and that this command
refuses where it composes the answer. The two are told apart by `matched`:
`sensitive: true` with `matched: null` is exactly that unplaceable case, and
`docs/pending-workflows/console-tests.yml` is `true` **with** the floor's
`pending-workflows` row.

The classifier's class is deliberately **not** a third input, and this is the
rule's one non-obvious half. `sensitivity.Classify` answers `Sensitive` for every
path outside its routine trees as well, which on any real repository is most of
the tree: `internal/cli/registry.go`, `internal/gitops/push.go` and
`shared/lib.go` all sit in no routine tree and on no floor row. Asking it as well
would make this field the classifier's complement rather than the list's answer,
and would tell the agent reading it that a change forces level 3 when the run
does not: **a run escalates on a row of the list in force that matches its diff
and on nothing else** (SC6, F5, F7), which is the same ground Check 370's own
pair stands on — `docs/recipes.md` matches no row and is `false`, while the
`docs/pending-workflows/**` row makes the file staged there `true`. So a
placeable path no row matches is `false` whatever tree it sits in, and
`TestSecurityPathsCheckAnswersPerPath`'s fourth path is that case, asserted
against the class as well as against the answer so the two rules cannot quietly
agree.

**The base branch, never the working copy** (F14, D1518). The list in force is
read at the base branch, so a change can never carry the rule that governs its
own review — the exploit the ask exists to close. The file is read out of the
checkout's **own object database** (`readFugaroYAMLAtRef`: git `cat-file`, never
the network and never the working tree), and the base branch's *name* is resolved
the way every launcher resolves it — the working tree's own `git.base_branch`,
else the local config's, else main — because the ref is what the read needs
before it can happen at all. That first layer is read out of the file's own text
(`config.GitBaseBranchOf`) rather than through the launcher's
`checkoutBaseBranch`, whose value comes from the file resolved over the published
project layer — a resolution that opens the layer's bucket (`checkoutParse` →
`findLayer`) and writes the local layer cache. Nothing is lost by it: a project
layer cannot set `git.base_branch` at all (`config.LayerGit` carries `provider`
and `pr` and nothing else), so the file's own text and the resolution name the
same branch, and `fugaro models ls`'s own base-branch read makes the same offline
choice. The layer this does **not** read is the published policy's `base_branch`
(the launcher's layer 3): that one is a database read, so a project whose default
lives only there reads as `main` here — the one case in which a run could be cut
from a branch this document does not name. **Nothing is fetched**: a ref this
checkout does not hold is the `error` above rather than a guess at another
document.

**A read that did not answer is not an exit, and it is not a silent floor
either.** The one thing this slice reads at the ref is the repository's own
`fugaro.yaml`, for `project`; a repository that has none at its base — one being
onboarded — still gets an answer, because the list in force is the floor alone
here and the floor is compiled in. So the document is printed with `error` set
and the command exits zero, §6.21's own rule for a state that is the answer, and
the table a person reads says the same thing on stderr beside it: what it printed
is the floor, and the repository's own rows are not in it.

**What is not in it, and it is the whole of the bound: the session.** The
document carries the caller's own `--check` paths and nothing else of the machine
it ran on — no environment, no path outside the repository, no argv beyond the
flags it was given. The two values it does read are the repository's own and both
are named members above: `project`, out of the repository's `fugaro.yaml` at the
base ref, and `base`, the branch that file's own resolution names. **Nothing in
it is a credential**: a row is a name, a glob and a category, and a checked path
is the path the caller typed.

**Flags** (`newSecurityPathsCmd`): `--check PATH` (repeatable) and `--json`, and
nothing else. It carries no cloud flags — no `--project`, no `--config` — because
it reads one file at one ref of the checkout it runs in: it wants no project, no
credentials and **no read of its own that leaves the machine**. That last one is
a property of the code and not only of the flags, and it has a test of its own:
`TestSecurityPathsOpensNoBucket` holds the project layer's opener
(`layerBucketOpener`) and the runs bucket's (`sharedBucketOpener`) to a failure
if either is reached, with an anchored checkout and a selected project config —
the two things `findLayer` needs before it reads anything. It is **not** on the
console's spawn list, and that is D1514's deferral rather than an oversight: the
console draws no screen from this document yet, and this section exists because
§6 is where a document a console could read is written down.

**This slice is the floor alone** (D1504, D1519). No project key and no
repository key exists yet, so every row's `source` is `floor` and there is one
layer to merge; SC7 and SC8 add the two keys, SC9 re-points this read at the one
resolution, and the shape above does not move for any of them.

## 7. The CLI gaps, settled against the code

The console ships **without any of these** and must work, visibly degraded
(design §4.3, D19). Each is a fact of this tree, with the symbol that proves
it. **The list was seven and it is now six: gap 5 is closed** by
`fugaro config list --json` (§6.8, console v2 plan V2.1.1). Its row is kept
below as the state the console was written against rather than deleted, so a
reader who finds the old count in the v1 design's §4.3 and in its plan finds
the closing here. **Gap 6 is half closed, not closed** — its `pr_number` half
landed 2026-10-27 (§2, GV2.5) and its URL half has not — so the count of six
does not move, and its row carries the closure in place, as gap 5's does.

| # | The gap | Proved by | What the console does meanwhile |
|---|---|---|---|
| 1 | The degraded path prints a **plain sentence to stdout** on the same stream as the documents | `runWatchDegraded`, `internal/cli/watch.go` (`"%s\nrun records unavailable (retrying): %s\n"`) | Skip any line that is not a JSON object; show it as a header warning (§2, fact (iii)) |
| 2 | `version` has **no `--json`** and no flags at all | `newVersionCmd`, `internal/cli/root.go` — `cobra.NoArgs`, no `Flags()` | Parse the human line, pinned by a test |
| 3 | **`project show --json` does not exist on this tree** — there is no `fugaro project` command at all | no `newProjectCmd` in `internal/cli`; `docs/plans/2026-10-14-project-centric.md` phase 1 (PR #390) ships `project show`, `project policy publish` and `project repos add` — and only that one verb of the `repos` group comes with phase 1: the plan's phase 4 (Enrollment and migration, Task 4.1) is where `project repos ls`, `rm` and `sync` ship — **and no `project ls`** | The Project screen renders the caps, kill state and repositories it can see, and says the policy object is not readable yet |
| 4 | **No schema version in the `watch --json` document** | `JSONDoc` (`internal/watch/json.go`) — no version field | Infer from the CLI version and from which shape arrived (the `degraded` key) |
| 5 | **Nothing enumerates installations** | `fugaro config` is `example\|show\|layer\|init\|publish` (`internal/cli/configcmd.go`); no `fugaro project` command exists; that phase's commands describe the *selected* project's policy and repositories, not the machine's installations | The operator adds each installation by hand (D13): binary picker, typed project name, optional checkout directory. **Closed 2026-10-18: `fugaro config list --json` exists (§6.8), so the typed name becomes a pick from the list of names the CLI prints — the field stays for an installation the operator adds before the CLI is updated** |
| 6 | **No PR URL on a run that has not finished** | `JSONRun` carries `repo` and no `pr_url`/`pr` — `pr_url` exists only on `JSONFinished`, and `runview.Row` spells the pair `pr_url` + `pr` — and it does carry `pr_number` (§2), for a run that has recorded a draft PR | A live row shows no link; the detail screen takes it from `diagnose RUN --json`'s `row`. **Half closed 2026-10-27: `pr_number` landed** (GV2.5, §2) — the run's own `record_pr`, read from the registry entry the live row is already built from, so the graph screen shows a running node's number from the feed alone. The URL half is still open, so the row stays in this list and the link is still rebuilt from `diagnose` |
| 7 | **No provider a console can build a link from** | `runview.Row` and `diagnose`'s row carry no provider, and `doctor --json`'s `doctorOutput` reports none. `JSONRun` and `JSONFinished` now do carry `provider` (phase 3, Task 3.5), and it is the right answer for the runs that have one — a shared project job's profile pins the provider its runs clone from — but it is **absent on every per-repository job**, which in phase 3 is every Bitbucket repository and every baked workflow. The kinds are `github`/`bitbucket`/`fake` (`internal/task/task.go`'s `Slug`, which hashes provider and repository together **one way**, so a slug cannot be inverted) | **No PR link at all** (design §5.1 rule 2): repository, number and `pr_url` shown as text, never handed to `openExternal`. A GitHub-shaped guess is wrong for `edgeappinc/fugarosandbox`, which is Bitbucket — and that run is exactly one of the runs whose `provider` is absent |

**And no shape that describes a run has a `repository` field** — the repository
is `repo` on every one of them. (§6.8's `checkouts` rows are the one place this
contract spells the word: there it names a repository identity, not a run's
repository, and nothing builds a link from it.) **`watch --json`'s run and finished rows carry a
`provider`; nothing else does, and it is the job profile's, not a stamp for
every repository.** So a link rebuilt as `https://github.com/<repo>/pull/<n>`
is still not merely useless but wrong (design §5.1 rule 2), and **gap 7 stays
open**: the console's detail screen reads `diagnose RUN --json`, whose row has
the field no more than it did, and a Bitbucket run's own feed rows have none
either. Closing it means the provider on the row the console actually builds
from — a change this document does not make.

Two things that look like gaps and are not: `budget show --json` already
carries the effective per-run cap with its binding scope
(`showRepo.per_run_cap_source`, §3), and `secrets ls` already has a
metadata-only form (§6.6).

## 8. The flag inventory the spawn contract turns on

Read off each cobra command — `addCloudFlags` —
and **not** off this document or the design:

| Command | `--config`, `--project` (and `--gcp-project`, `--region`) |
|---|---|
| `watch` | yes (`newWatchCmd`) |
| `budget show` | yes (`newBudgetShowCmd`) |
| `report` | yes (`newReportCmd`) |
| `runs ls` | yes (`newLsCmd`) |
| `runs stats` | yes (`newRunsStatsCmd`, §6.10) |
| `diagnose` | yes (`newDiagnoseCmd`) |
| `logs` | yes (`newLogsCmd`) |
| `secrets ls` | yes (`secretsOptions.addFlags`) |
| `accounts ls` | yes (`newAccountsLsCmd`) |
| `settings` | yes (`newSettingsCmd`, §6.19) — the settings read (the settings-edit plan's C1): the pair is **`settingsAimFlags`' own** rather than `addCloudFlags`', so it carries `--project` and `--config` and **not** `--gcp-project` or `--region`. It is the console's third read of the Project tab, and its argv is the command, `--json` and those two words and nothing else |
| `doctor` | yes (`newDoctorCmd`) |
| **`version`** | **no — it takes no flags at all** (`internal/cli/root.go`; the root's `--version`/`-v` is the root's own, not this subcommand's — §6.7) |
| **`config list`** | **no — it reads the operator's local project configs, not a cloud** (`internal/cli/config_list.go`, §6.8) |
| **`plan graph`** | **no — it reads the graph artifact in the checkout, not a cloud** (`internal/cli/plan.go`, §6.9) |
| `plan status` | yes (`newPlanStatusCmd`, §6.12) — a task's state is a *run's* state, so it reads the runs bucket too |
| `coordinator status` | yes (`newCoordinatorStatusCmd`, §6.13) — with `--from-store` it is the one read here that asks nothing of the host (the stored view, through the caller's own credentials), and it is a cloud read all the same: the view lives in the project's database, so it carries `--project` and `--config` like the rest |
| `coordinator show` | yes (`newCoordinatorShowCmd`, §6.15) — the pull request read (coordinator v2 C3, D816): `--repo` and `--pr` are its own two arguments, both required, and it carries `--project` and `--config` like the rest because it resolves the repository's provider the way the project view does. **The second row here whose child mints a credential of the host's** — the first is `run`'s below, whose child is a run and mints the write-capable token it pushes and opens the pull request with; this row's is the coordinator's own read-only installation token, revoked when the command is done — so its argv is the whole of what a renderer may influence about it |
| `coordinator intents` | yes (`newCoordinatorIntentsCmd`, §6.16) — the declarations read (coordinator v2 C4, D827): it has **no argument of its own** (`--all` and `--json` are the console row's fixed words), and it carries `--project` and `--config` like the rest because it resolves the local config the way the project view does. **The one row here whose child asks nothing of the host at all**: the declarations are the caller's own database node, read through the same credentials §6.13's stored read uses, so there is no App key, no installation token and no provider anywhere in it |
| `settings set\|unset` | yes (`newSettingsCmd`, §6.20) — **the second row here that writes** (the settings-edit plan's C3, design §5.1 rule 12): the dry run and the confirmed write are the same command with the same aiming pair, and the layer written is §6.19's `target_layer` rather than a flag a renderer chose |
| **`run`** | yes (`newRunCmd`, §6.14) — **the other row here that writes**: the launch (design §5.1 rule 11) is cut for a project like every cloud command, so it carries `--project` and `--config` too, and the task itself arrives on stdin rather than as a flag |

So the console passes `--project <name>` (and `--config <path>` when the
installation is not the ambient one) on **every spawn except the handshake**,
which runs `fugaro version` bare (never the root's `--version`, which older
CLIs in the console's supported range do not have — §6.7). A console that
appended `--project` to `version` would fail its own version check on the first
launch, on every machine. `docs/gcp-live-checklist.md`'s Check 43 guard states
the same exception. `TestDocsDesktopConsoleContractNamesTheFlagInventory` asserts this
table against the live constructors.

## 9. What the console may do with all this

1. **Every document in the console's set is read-only** — with **two
   exceptions, and both are writes the console's own confirmation windows
   answer**: the launch (§6.14, design §5.1 rule 11), which was the console's
   **first write**, and the settings edit (§6.20, design §5.1 rule 12, the
   settings-edit plan's C3), which is its **second write**. The allow-list is by
   exact argv (design §5.1 rule 6), and both are on it: `run.launch` and
   `settings.edit` are the two operations of the surface that write (V2.5.1a,
   2026-10-28/29, took the surface to twenty-four; K2a's
   `link.openPullRequest` took it to twenty-five and writes nothing, and C4c's
   `coordinator.intents` took it to twenty-six and writes nothing — **the
   counts in this parenthesis are the history up to that commit, and rule 5's
   thirty-one is today's**), and no other argv that writes is reachable
   from a renderer. Two further commands this file
   describes are writes, and **neither is in the console's set**: `fugaro cancel
   RUN --json` (§6.3), which is on the design's "never" table and which no v1
   console spawns, and `fugaro accounts clear NAME` (§6.5a, credentials-service
   C1.21), the operator's own write to the cascade's state. Nothing else here
   names a write.
2. **Exactly two of the commands in the console's set are line-delimited:
   `watch --json` and `logs RUN
   --json`** (§2, §6.2 — the feed through
   `json.NewEncoder` with no `SetIndent`, the log lines through
   `json.Marshal`). **Every other command in that set is indented over several
   lines** — `version` is the exception that is no document at all: it prints
   one plain line (§6.7) — so a line-based parser is right for those two and
   wrong for the rest of the set. The one command outside the set that is line-delimited too
   is `fugaro cancel RUN --json` (§6.3, a write): compact, one object per line,
   the same parser as `logs`.
3. **A null, an absent key and an empty array are three different things**, and
   each maps to a different availability state (§1). A `0` is never a
   substitute for any of them.
4. **Job-written text arrives already sanitized by the CLI and is re-sanitized
   at the display layer** (design §5.1 rule 7): `title`, `message`, `note`,
   `warnings[]`, `queued_note`, `reason`, `halted`.

## 10. What `watch --json` guarantees a long-lived child process

Task 0.2 of the plan, and the design's §12 item 2 settled by reading the code
rather than assuming it. §2 pins the feed's *shape*; this section pins its
*behaviour* — the one command the console runs as a stream rather than as a
one-shot read, and the one whose failure modes decide whether the sidecar
(D1, option A) spawns one child or supervises several. Every claim names the
symbol it was read from on `main`, and §10.11 is the answer the task exists
for.

### 10.1 One document per change, on a one-second floor

`WatchDeps.emit` (`internal/cli/watch.go`) writes one document per call —
`json.NewEncoder(d.Out).Encode(watch.BuildJSON(d.LC.Name, v))` — and
`watchStream` calls it when `refused || (ready && dirty && (last.IsZero() ||
time.Since(last) >= gap))`. A refusal is the one case that bypasses the
readiness, change and gap tests and is emitted unconditionally (§10.5); every
other document waits for them. Under `--json` the gap is `watchJSONGap`, one
second, whatever `--interval` says: `--interval` sets the supervisor's polling
period (`watch.Options.Interval`), not the document rate.

`dirty` is set by a `put`/`patch` event on any of the four streams, by
`UpdDay`, by `UpdPolling`, by a change in the queued/finished rows read from
the runs bucket, and by a change in the connection kind (`v.Conn.Kind !=
lastConn`). `UpdTick` alone never sets it — but a `dirty` set by an earlier
update and not yet emitted stays set until a document goes out, which is what
makes polling emit two documents per poll (§10.10).

Three consequences a consumer must hold to:

- **A document is a sampled whole, not a delta.** Two events inside one second
  produce one document carrying the complete current state. The console renders
  the newest document and never accumulates; nothing is lost by dropping an
  older one.
- **Silence means "live and unchanged", not "the feed is dead".** An idle
  project emits nothing at all. The console's own clock, plus `now`,
  `connection.state` and `connection.age_seconds` from the last document, is
  what tells it the data is old.
- **A connection-state change is announced.** When a stream goes quiet past
  `watch.StaleAfter` (45 s) or fails, the next tick builds a view whose
  `v.Conn.Kind` differs from `lastConn`, which sets `dirty` and emits — so the
  console is *told* the data went `stale` or `offline` instead of inferring it.

### 10.2 The first document arrives within `watchFirstFrame` plus a tick

`ready` is `seen == [4]bool{true, true, true, true} || time.Since(started) >=
watchFirstFrame` (5 s): all four streams have delivered a `put` of `"/"`, or
five seconds have passed. The supervisor's first update is `UpdDay`
(`Supervisor.run`, `internal/watch/source.go`), which sets `dirty` immediately,
so a child that can read nothing at all still writes one frame — the "offline
frame" `watchFirstFrame`'s own comment names.

Those five seconds bound the clock, not the arrival. `ready` is evaluated only
when an update arrives (`watchStream`'s loop body runs per update, not on a
timer of its own), updates come from the supervisor's `Tick` — 1 s — while no
stream says anything, and `started` is set in `watchStream` *after* `watch.Start`
(`runWatch`), so the first tick that finds `time.Since(started)` past
`watchFirstFrame` comes about a tick after that deadline. **The bound is
`watchFirstFrame` + `watch.Options.Tick`, about 6 s**, and the console may
expect a first document within that and must not need one sooner.

### 10.3 A slow reader makes the child block; it never drops a document

The pipe is not a queue with a policy: it is a queue with a **stall**. A full
pipe blocks `json.NewEncoder(d.Out).Encode`, `watchStream` stops draining
`Supervisor.Updates()`, and `Supervisor.emit`'s `out` channel — `make(chan
Update, 64)`, `watch.Start` — fills; the supervisor's own loop then blocks in
`Supervisor.emit`, and behind it the per-stream goroutines block feeding
`streamPhase`'s unbuffered `in` channel. Nothing is
discarded, and nothing grows without bound: the only queues are `watch.Start`'s
64 slots, `rtdb.Client.Stream`'s 16 (`internal/rtdb/stream.go`), and the
kernel's pipe buffer.

A stall longer than `rtdb.Client`'s `c.idle` timeout (90 s) reaches the
database. `streamOnce` resets that idle timer only as it consumes lines, so a
blocked consumer stops feeding the timer, the connection is cancelled, the
stream ends with an `error` event, `runStream` backs off (`backMin`, 1 s,
doubling to `backMax`, 30 s) and reconnects — and **a reconnection starts with
a `put` of `"/"` holding the whole current value**, so a change made while the
stream was down is not lost. The cost of a slow reader is latency and
reconnects, not lost data. The console must still drain the pipe promptly, and
its stalled-reader budget is measured in seconds, not minutes.

### 10.4 A closed stdout ends the child — on its next write, and by a signal

The console's own death closes the pipe. The child's next write to file
descriptor 1 raises `SIGPIPE`, and Go's runtime does not ignore it for
descriptors 1 and 2: the default disposition kills the process, and the parent
that spawned it sees **a signal, not an exit code** — `os.ProcessState`
reports `SIGPIPE` (`WaitStatus.Signal()`), Node's `child.on('exit')` reports
`signal: 'SIGPIPE'` with `code: null`, and `141` is only the shell's `128 + 13`
for the same event. The console must read it as the ordinary "the feed ended
because I stopped reading" case, never as a CLI error code.

**That is a write, though, and a quiet feed writes nothing.** An idle project
emits no document (§10.1), so a child whose reader has died can sit in
`watchStream` indefinitely without ever touching descriptor 1 — there is no
timer that ends it. **SIGPIPE is therefore not a shutdown mechanism: the
console closes the pipe *and* terminates the process** (§10.12), because
"nothing was written" and "nothing to write" look the same from outside.

If the write instead returns an error (a stdout that is not a pipe),
`watchStream` returns that error as it came — it is not wrapped with
`remote()` — and `ExitCode` maps an error that is not an `*ExitError` to
`ExitUserError`: `main` prints `fugaro: …` to stderr and exits 1. Exit 2 is
not reachable from this path, so a console reading the status sees 0, 1 or a
signal.

### 10.5 A refused read is terminal, and it usually announces itself with no document at all

There are two refusals, and the ordinary one writes nothing:

1. **Before the stream starts.** `runWatch` calls `checkDBProject(ctx, db, lc)`
   (`internal/cli/budget.go`) before it starts the supervisor, and that reads
   `/fugaro/project` to prove the database is this project's. A permission
   error there, a database with no marker, and a marker naming another project
   all return `dbErr(...)`/`userErr(...)` — `ExitUserError` (1) — **with no
   document emitted and no stream ever opened**. Zero documents is the shape of
   the ordinary refusal.
2. **During the stream.** A permission error arriving as a stream `error`
   event reaches `State.conn` (`internal/watch/state.go`), which maps it to
   `ConnRefused`; `watchStream`'s `refused` test emits that document **before**
   the `ready`/`dirty`/gap test — unconditionally — and then returns
   `dbErr(d.LC, rtdb.ErrPermission, false)`. The supervisor reaches `phaseHalt`
   on the same event (`streamPhase`, `internal/watch/source.go`), but the CLI
   returns as soon as it sees `ConnRefused`, so the process exits 1 rather than
   halting.

In both, `main` prints `fugaro: project <name>'s budget database refused the
read: you need the Viewer role …` to stderr and exits 1. **The console must
not treat "one document, then exit 1" as the refusal's shape**: it is zero
documents in the ordinary case and one in the rarer one, and the two are told
apart by the exit and the stderr sentence, not by the count.

There is no in-child retry after a refusal: `haltPhase` keeps only the clock.
**Restarting the child before the identity or the role changes refuses again
in the same way**, so the console shows the CLI's own sentence and stops
rather than looping (design §6.3's last row). `--once` differs in shape and
not in outcome: it emits no document at all and fails with the same sentence.

### 10.6 A dead or withdrawn stream is not an exit

A non-permission failure does not end the child. Three consecutive `error`
events with the same cause (`cause`) make `streamPhase` call `probe` and fall
back to `pollPhase`, which re-reads the four paths every `Options.Interval`
(5 s by default) and tries the stream again every `SSERetry` (60 s) through
`phaseTrial`. A `cancel` event — the server withdrawing the listen — ends the
stream phase in `phaseHalt`, which keeps only the clock running: **the child
stays alive, and its view stays `offline` (`State.conn`'s `otherErr` branch),
indefinitely.** The console must not wait for an exit to learn the feed is
dead; it reads `connection.state` and ages the data itself.

`refused` is the one connection state that does *not* linger: the supervisor
halts on a permission error, but the CLI returns on `ConnRefused` and the
process exits 1 (§10.5). A console that waits for `connection.state` to leave
`refused` waits for a process that is already gone.

### 10.7 UTC midnight is a gap, not a zero

`streamPhase`'s tick compares `budget.Day(s.now())` (`internal/budget/model.go`)
with the day it is streaming. When it changes, it cancels the day pair and
waits for both streams to close (`dayWG.Wait()`) **before** opening the new
day's pair (`openDay`), and `rollDay` emits `UpdDay` in between. `State.Handle`'s
`UpdDay` case calls `SetDay`, which empties both spend trees (`rtdb.Tree.Reset`)
and the burn windows; `watchStream` treats `UpdDay` as dirty.

So the rollover emits a document whose `day` is the new UTC date and whose
counters are all zero — `total.counted_usd` 0, every repository's
`counted_usd` 0, `burn.known` false — **a document that is not a measurement**,
and the day's real numbers follow once the new pair delivers its `put` of
`"/"`. The repository list is rebuilt from `/agents` and the kill switches, so
a repository known only from `/spend/<day>/repos` disappears from `repos` until
then. A consumer must read a changed `day` as the design's *gap* state and
break the series there, and must not draw the zero. The burn rate stays unknown
for `BurnWarmup` (60 s) after the rollover, because `SetDay` restarted the
window.

### 10.8 The degraded mode is a second shape, and one of its lines is not JSON

When the selected installation has no `budget.rtdb_url`, `runWatch` calls
`runWatchDegraded` instead. Its document is `watchDegradedDoc` — `lsDoc` plus
`degraded` and `notice` (§2 (iii)) — with no `connection`, no caps, no burn and
no kill state, and its run list is bounded by `degradedSince` (24 hours). It is
emitted every `watchPlainInterval` (10 s) or `--interval`, unconditionally,
whether or not anything changed.

Its error path writes a plain sentence to **stdout**, on the same stream as the
documents: `fmt.Fprintf(out, "%s\nrun records unavailable (retrying): %s\n",
degradedNotice, safetext.Strip(err.Error()))`, where `out` is
`cmd.OutOrStdout()` and `degradedNotice` is `no budget backend: showing run
records only`. In `--json` mode too — `clear` is false there, so no ANSI
escapes are written. The reader's rule is therefore not a precaution but a
requirement: **skip any line that is not a JSON object**, surface it as a
header warning, and never treat it as a parse error or a crash. Everything
else human — `printProjectHeader`'s `project: <name> (GCP <id>)`, the update
nag, error lines — goes to stderr, which is not the document stream.

### 10.9 The thresholds, for a consumer that is not a terminal

| Constant | Value | What it means to the console |
|---|---|---|
| `watchJSONGap` | 1 s | The floor between two documents. A ceiling on the feed's rate, never a promise of one. |
| `watchFirstFrame` | 5 s | The four streams' head start before the first document, readable or not. `ready` is re-evaluated only when an update arrives, so the document can be up to one `Tick` later: the bound is about 6 s (§10.2). |
| `watchPlainInterval` | 10 s | The degraded mode's period, and the default `--interval` for plain frames. |
| `watchDegradedDoc`'s window | 24 h | `degradedSince`: how far back the degraded document's run list reaches. Not a cap and not a total. |
| `watch.StaleAfter` | 45 s | Per-stream silence, keep-alives counting as events, after which `connection.state` is `stale`. The console's own stale banner (design §6.3) is the same number, and the child announces the transition. |
| `watch.SilentAfter` | 60 s | Per-run silence after which a run's `health` is `silent` (`internal/watch/build.go`). A property of the run, not of the connection. |
| `watch.LostAfter` | 180 s | The same, for `lost`. |
| `watch.BurnWindow` / `BurnWarmup` | 5 min / 60 s | The burn rate's window and the span it needs before `burn.known` is true. A restarted window — a reconnect, a rollover — reports `known: false`, not a rate of zero. |
| `watch.Options.Tick` | 1 s | The supervisor's clock: how quickly a stale, offline or day-rolled view becomes visible, and how often the loop re-evaluates `ready` (§10.2). Not how quickly a cancellation is noticed — that is the loop's own `select` on `ctx.Done()`, which fires at once (§10.12). |
| `watch.Options.Interval` | 5 s | The polling period, and `--interval` when given. |
| `watch.Options.SSERetry` | 60 s | How often polling tries the stream again. |
| `rtdb.Client` idle | 90 s | The SSE silence that ends a connection (§10.3). |
| `backMin` / `backMax` | 1 s / 30 s | The stream's reconnect backoff, doubling between them. |

### 10.10 How many documents arrive per minute, in each mode

| Mode | Rate | Notes |
|---|---|---|
| Live SSE (`watch --json`) | at most 60/min, and as few as **0** | One per change, floored at 1 s. An idle, live project is silent; a connection-state change always emits (§10.1). The first document lands within `watchFirstFrame` plus a tick, about 6 s (§10.2). |
| Polling (`--poll`, or after three same-cause stream errors) | **two per poll**, so about 24/min at the 5 s default | `pollOnce` reads the four paths and delivers each as a `put` of `"/"`. All four land inside one 1-second window, so the first emits and the rest only re-set `dirty`; the next `Tick`, a second later, flushes that flag and emits the second. Two documents per poll, not one, and not one per read. |
| Degraded (no `budget.rtdb_url`) | one per `watchPlainInterval`: 6/min at the 10 s default | Unconditional, changed or not, forever (§10.8). |
| `--once` | exactly one, then exit | A bounded read by plain GETs (`watchOnce`), not a stream. |

A refusal is the one mode with no rate: no document at all in the ordinary
case, one in the in-stream case, then exit 1 (§10.5).

### 10.11 Does option A need a supervisor process? Yes

The console owns the child's lifecycle, and four facts above are why:

1. **The child exits on a refusal** (§10.5) — usually having written nothing at
   all. Something has to decide not to restart it, and to put the CLI's own
   sentence on screen.
2. **The child does not exit on a dead stream** (§10.6). A hung, offline child is
   indistinguishable from a working one by its liveness alone, so the console
   needs its own staleness clock — `connection.state` and `age_seconds` from
   the documents it has, against its own time.
3. **A closed pipe does not end an idle child** (§10.4). `SIGPIPE` needs a
   write, so the console must terminate the process itself rather than rely on
   the pipe closing.
4. **A stalled reader is a real failure mode, not a hypothetical one** (§10.3):
   the console must drain stdout continuously, or the feed reconnects and the
   numbers go stale for reasons of the console's own making.

One child, supervised: one `watch --json` per selected installation (D13), with
`--once` reserved for bounded reads. Nothing here needs several `watch`
children — the documents already carry the whole project — so the supervisor
supervises one process and restarts it with backoff, never on a refusal.

### 10.12 How the child ends when the console asks it to

`cmd/fugaro/main.go`'s `signalContext` is
`signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM,
syscall.SIGHUP)`: **the first `SIGINT`, `SIGTERM` or `SIGHUP` cancels the
command's context and nothing else.** `watchStream`'s `<-ctx.Done()` case
returns nil, `runWatch` returns it, and `main` — which only calls `os.Exit` on
a non-nil error — falls off the end: **the child exits 0.** `context.AfterFunc`
then resets the handler, so a *second* signal gets the default action and the
process dies by that signal instead. `SIGKILL` cannot be caught at all.

So the console's shutdown is: close the pipe, send `SIGTERM`, and expect exit 0
promptly — not on a tick. `watchStream`'s loop blocks in a `select` whose other
case is `<-ctx.Done(): return nil`, so a running child notices the cancellation
the moment it fires. The one child that cannot is the stalled reader of §10.3,
blocked inside `d.emit`'s `Encode` with a full pipe, where no tick would help
it; that is what the `SIGKILL` is for. The exit code says nothing about why the
feed ended — 0 is both "the console asked" and "`ctx` was cancelled for any
other reason" — which is why the console's own record of the shutdown, not the
child's status, is what it reports. (`SIGTERM` is also how Cloud Run asks a
task to stop, which is what the function's comment says.)

### 10.13 The reader rules that follow

1. Split stdout on newlines and decode each line as one JSON object; **skip a
   line that is not** (§10.8).
2. Render the newest document; never accumulate deltas (§10.1).
3. Silence is not death: use `now`, `connection.state`, `connection.age_seconds`
   and the console's own clock (§10.1, §10.6).
4. A changed `day` is a break in the series, never a zero (§10.7).
5. A missing field is `unavailable`, never `0` (§1).
6. `--repo` narrows the document: `WatchDeps.emit` calls `watch.FilterRepo`, so
   `repos` carries the filtered repository alone and `len(repos)` is not the
   project's repository count while the Live screen is filtered (§2 (ii)).
7. Expect the child to end — on a refusal (0 or 1 document, exit 1), on a
   signal, or when the console kills it — and never loop on a refusal
   (§10.4, §10.5, §10.12).
8. Never `--follow`, and never `--once` for the live feed: the allow-list is
   design §5.1's table, and the console reads a bounded window or a stream.
