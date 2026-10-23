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

## 1. The five availability states

The design's first UI rule (§6.1 rule 1): **every number carries an
availability state, and a missing value is never an implicit zero.** The
states are:

| State | On screen | What it means |
|---|---|---|
| **measured** | the number | A counter the CLI read. |
| **notional** | `NOTIONAL~` | A subscription's list price: never billed, never capped (`JSONTotal.NotionalUSD`'s own comment, `internal/watch/json.go`). |
| **estimated** | the number, marked | Compute, an estimate by design (`JSONLine.ComputeEstimated`, `internal/report/render.go`). |
| **unavailable** | `n/a` | The document says the value is not known. A nil pointer, an absent key, `[]`. |
| **gap** | a broken line / hollow bar | The stream stopped. Never interpolated across. |

The columns below say which field needs which state. The recurring shapes:

- **`*float64` / `*int64` / `*string` with no `omitempty`** (the `usdPtr` and
  `jsonLine` conventions): a **null** on the wire means *unavailable*, and a
  present number means *measured*. `daily_cap_usd`, `percent`, `spent_usd`,
  `age_seconds`, `notional_micros`, `compute_usd` are all this shape.
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
| `daily_cap_usd` | `*float64` | USD | no | **null = unavailable** (no cap set) |
| `percent` | `*float64` | 0–100 | no | **null = unavailable**, together with `daily_cap_usd` (`usdPtr`) |
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
| `daily_cap_usd` / `percent` | `*float64` | USD / 0–100 | no | **null = unavailable** |
| `burn` | `JSONBurn` | — | no | as above |
| `kill` | `*JSONKill` | — | no | **null = not killed** |

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
| `auth` | string | — | no | `oauth` means `spent_usd` is notional |
| `notional` | bool | — | no | **the availability state of `spent_usd`** |
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
| `credential` | string | — | **yes** | the **name** — never the value — of the credential the run's cascade is currently spending on (`budget.AgentEntry.Credential`, written by the run itself under the rules of C1.3; the credentials service chooses it). **CLI 0.10.0.** Absent when the run has not reported one, which is **"not recorded", never "no credential"**: a run that has not written the field is one this reader cannot account for, not one spending on nothing. A name a run wrote, so it is sanitised like every other database-derived string here, and **never an authority** — no policy, admission or budget rule reads it. A console that wants to say which account a run is on must not treat the field as proof that the run is on that account now (the run reports it as it switches) |
| `waiting_until` | string | RFC 3339, UTC | **yes** | when a run waiting for a credential expects to resume at (`budget.AgentEntry.WaitingUntil`, epoch ms, D152's "waiting, expected resume time"). **CLI 0.10.0.** Present **only while the run is still waiting**: presence is the state, so a reader that sees the field knows the run is held up rather than spending, and one that wants a countdown compares it with `now`. The text views show the same fact: the plain table as `waiting for a credential, expected resume T`, and the screen's FLAGS column as `waiting` with the instant on the run's detail lines (its column is 30 columns wide at its widest, and the sentence is 59 — a clip would eat the instant and any flag behind it) |

**A live run carries no PR field at all** — no `pr_url`, no `pr_number`, no
`pr` (design §4.3 gap 6, §5.1 rule 2). The console renders no link for a live
row; the run-detail screen reads `diagnose RUN --json` for the one run whose
detail is open.

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
| `max_reserve_usd` | `*float64` | USD | no | **null = unavailable** |
| `global` | `showGlobal` | — | no | below |
| `defaults` | `showDefaults` | — | no | below |
| `repos` | `[]showRepo` | — | no | `[]` when none — **empty, not unavailable** |
| `running` | `[]showRun` | — | no | `[]` when none |
| `warnings` | `[]string` | — | no | `[]` when none; CLI-worded, shown verbatim |

`showGlobal`: `daily_cap_usd` `*float64`, `per_run_cap_usd`
`*float64`, `counted_usd`/`spent_usd` float64, `notional_usd` float64
(**notional**), `headroom_usd` `*float64` (**null = unavailable**),
`kill` `*showKill` (null = not killed). All caps are **null = unavailable** and
never `0`.

`showDefaults`: `repo_daily_cap_usd`, `repo_per_run_cap_usd`,
both `*float64`.

`showRepo`: `slug`, `repo` (`omitempty`),
`daily_cap_usd` `*float64`, `daily_cap_source` (string, no `omitempty` —
**`repo`, `default` or `none`**), `per_run_cap_usd` `*float64`,
**`per_run_cap_source`** (string, no `omitempty` — `repo`, `default`, `global`
or `none`; `perRunCapSource`), `counted_usd`, `spent_usd`,
`notional_usd`, `headroom_usd` `*float64`, `kill` `*showKill`,
`running` `[]showRun`. **`per_run_cap_source` is the "binding scope named"
the design's §6.2 Spend screen draws** — it is already in the document and
needs no CLI change (§4.3's "not a gap" note).

`showRun`: `slug`, `run`, `repo`°, `workflow`°, `title`°,
`stage`°, `round` (**int**, `omitempty` — unlike `watch`'s string),
`auth`°, `coder`°, `reviewer`°, `spent_usd` float64 (no `omitempty`),
`pr_url`°, `halted`°, `started_at`°, `updated_at`° (RFC 3339 strings), `stale`
bool°. `°` = `omitempty`.

`showKill`: `by`°, `at`°, `reason`° — the same shape as
`JSONKill`.

**Flags, read off the cobra command** (`newBudgetShowCmd`): `--repo`, `--all`,
`--json`, plus `addCloudFlags`' `--config`, `--project`, `--gcp-project`,
`--region`.

## 4. `fugaro report --json`

**Command:** `internal/cli/report.go` `newReportCmd` / `runReport`.
**Document:** `internal/report/render.go` `JSONDoc`, written
by **`report.JSON(w io.Writer, rep Report, m Meta) error`** —
the exported writer, not a `cli`-side struct. Indented two spaces
(`report.JSON`, `internal/report/render.go`). A schema note also lives above the type
(`render.go`'s comment block: `by`, `since`, `until`, `weeks`, `rows`,
`totals`, `warnings`).

`JSONDoc`: `project`, `source`, `degraded` bool, `notice`°,
`by` (string: `day`/`week`/`month`/`year`/`repo`/`model`/`person`),
`since`, `until` (UTC dates), `weeks` (**always the literal
`"iso-utc-monday"`** — the ISO week convention is declared, not inferred),
`rows` `[]JSONLine`, `totals` `JSONLine`, `warnings` `[]string`.

`JSONLine`: `key`, `start`°, `end`°, `partial` bool,
`model_micros` int64, `model_usd` string, `notional_micros` `*int64`,
`notional_usd` `*string`, `compute_micros` `*int64`, `compute_usd` `*string`,
`compute_estimated` bool, `run_hours` `*float64`, `runs` int64, `calls` int64,
`tokens` `*JSONTokens`°.

- **`*_usd` are exact decimal strings of the integer `*_micros`** (`USD6`), not
  floats: the console parses the string, and micros is the integer of record.
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
  *unavailable*, never as a measured `0`. A degraded `report` is the whole
  document's null case — `reportDegraded` builds on the run records and sets
  `rep.NoHours = true` (`reportDegraded`), and `source` says
  `"run-records"`, so every row's `run_hours` is null (there is no flag for
  this; the path is chosen by whether spend history is enabled).
- **`tokens` is absent entirely when not tracked** (`TokensTracked`).
- **`partial`** is the design's `(partial)` marker: the day has no final
  Firestore document. A day beyond the retained window is a **gap**, not a row
  of zeros.

`JSONTokens`: `in`, `out`, `cache_read`, `cache_write`, all
int64, none `omitempty`.

**Flags** (`newReportCmd`, `internal/cli/report.go`): `--repo`, `--since`, `--until`, `--by`, `--json`,
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
| `derived` | string | **yes** | **what the read side derived, and not a status of its own** — the CLI's 2026-10-21 ruling. Written only on a row whose record was never finalized and whose Cloud Batch job the platform preempted: `status` stays `failed` (the vocabulary every reader already switches on) and `derived` carries the fact (`preempted`, `runview.DerivedPreempted`), with `reason` saying so in a sentence. Absent for every other row, which is what keeps the shape additive. See **the derived status** below |
| `halt` | `*runstore.Halt` | **yes** | why a run halted |
| `batch`, `requested_by` | string | **yes** | `requested_by` is a person's address |
| `launched_by` | string | **yes** | who started the run, from its `launch.json` (**CLI 0.9.0**); absent when the record has none. The same value `JSONRun`/`JSONFinished` carry, and display text only — see `JSONRun`'s row |
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
`--pr`, `--watch`, `--interval` (hidden), `--json`, plus the cloud flags. The
allow-list excludes `--watch` (it is `watch` under another name).

## 6. The other documents

### 6.1 `fugaro diagnose RUN --json`

**Command:** `internal/cli/diagnose.go` `newDiagnoseCmd` (`Use: "diagnose RUN"`),
indented (`printDiagnosis`). **Document:** `Diagnosis`
(`internal/cli/diagnose.go`) — a **flat** struct with no `MarshalJSON`, so its keys are
exactly its own tags and nothing is nested under a `process` or `checks` key:

| Field | Type | omitempty | Note |
|---|---|---|---|
| `project` | string | no | the Fugaro project |
| `row` | `runview.Row` | no | §5 — this is where a live run's PR number and `pr_url` come from (design gap 6) |
| `halt` | `*runstore.Halt` | **yes** | why a halted run was halted |
| `verify` | `[]verify.Record` | **yes** | `result.json`'s verify records |
| `failed`, `flaky` | `[]string` | **yes** | the last test record's failed and flaky tests |
| `findings` | `[]runner.Finding` | **yes** | the last review's findings |
| `agent_message` | string | **yes** | capped at `agentMessageBytes` = 4 KiB |
| `log_tail` | `[]string` | **yes** | the newest `diagnoseLogLines` = 30 lines |
| `report_path` | string | no | |
| `artifact_paths` | `[]string` | **yes** | one `runs/<slug>/<run>/artifacts/<name>` path per artifact the record lists, in the record's own order: an artifact run has no pull request, so this is where its deliverable is named. The record's list only, never a bucket listing, and a name `fugaro artifacts get` would refuse is still named here (`fugaro artifacts ls` shows it with the reason) |
| `draft_note` | string | **yes** | a line about the run's draft PR, when it needs one |
| `follow_up` | `*runstore.FollowUp` | **yes** | what a follow-up acted on; absent for a first run |
| `comments_path` | string | **yes** | where the comments it was given are stored |
| `route_by`, `model_by` | `map[string]float64` | **yes** | the run's spend by provider route and by model (a Claude call has no route) |
| `reported_usd`, `charge_usd` | float64 | **yes** | what providers said the calls cost, beside what the table charged — **absent (0) when no provider reported one**, so the pair is read together |
| `reported_warning` | string | **yes** | set when the two differ by more than `reportedGapPct` percent |
| `pin_warnings` | `[]string` | **yes** | the model prices the run's cap counted on a guess |

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
| `checkouts` | array | **yes** (an older CLI) | never | the repositories of this project this machine remembers a checkout of, one entry per path; `[]` when none is remembered |

and one entry of `installations[].checkouts`:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `repository` | string | never | never | `<provider>/<owner>/<name>`, the identity the path was recorded under |
| `path` | string | never | never | the absolute path, made printable and bounded (control and format characters escaped, at most 200 runes) |
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
stored one, so it must never be presented as a path that works**. A state file
that cannot be read (not JSON, another version, over its size bound, not a
regular file) is a warning naming it and `checkouts: []` for every
installation — never a silent "there are none", and never a listing that fails.

**One row carries one reason, and which one it is is fixed.** In order:

1. `not_representable` — decided from the row's own strings, **before anything
   touches the disk**: the path as printed is not the path as stored, the path
   is not absolute, or the identity is not a provider and an `owner/name`. A
   row that fails here is never examined further, so a relative path that
   happens to exist under the reader's working directory is still
   `not_representable` and not a valid checkout.
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

**Version skew.** The command is new, so an older CLI does not have it at all:
the spawn fails and the console shows its "this CLI cannot list installations"
state, which is the form D13 accepted for v1. The handshake (§6.7) is what tells
the console which case it is in. **`checkouts` is newer than the command**: a
CLI that lists installations and emits no `checkouts` key at all is a CLI that
does not know about checkouts, and the console shows the installation without
them (its checkout directory stays whatever the operator stored in D13's
form) — an empty array means this CLI knows, and this machine remembers none.
The two are not the same state and the reader must not conflate them. There is
no version field to bump: this document is read whole, and the key's absence is
the signal.

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
| `schema_version` | number | never | never | `1` today; a document carrying another number is **refused**, not rendered |
| `plan` | string | never | never | the plan the artifact came from, as the artifact names it |
| `nodes` | array | never | never | the tasks; `[]` is a plan with no tasks yet |
| `edges` | array | never | never | the dependencies; `[]` is a plan with no dependencies yet |
| `levels` | number | never | never | how many levels the plan has — the longest chain of dependencies plus one; `0` for a plan with no tasks |
| `critical_path` | object | never | never | the longest chain of tasks in the plan, below |
| `bottleneck` | string | never | **yes** | the task the critical path runs through; null only when the plan has no tasks |

one entry of `nodes`:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `id` | string | never | never | the planner's own name for the task; unique in the document |
| `title` | string | never | never | what a person reads |
| `run` | string | never | **yes** | the run this task belongs to; null when it belongs to none |
| `version` | string | never | **yes** | the target version; null when the plan does not say |
| `footprint` | object | never | never | the expected paths, below |
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

**Four refusals, and they are refusals rather than partial renders:** a document
from a schema version this build does not read (the message names both
versions), a malformed one, one whose edges name a node that is not there or
whose ids repeat, and **one with a cycle** — every "ready" and "blocked" answer
is a function of the graph, so a cyclic graph has none to give. A refused graph
is no document at all: the command exits non-zero and prints nothing to stdout,
so a renderer never has half a graph to draw.

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

**The computed fields are additive, and `schema_version` stays `1`.** They are
fields of the *read's* document and not of the artifact: a graph artifact is
still schema version 1 with the artifact's own field set — the nine computed
keys above are not among them, and a planner that wrote one would be refused by
name — so what a planner writes and what an older CLI accepts are unchanged, and
`--done` is a flag on the read rather than a field a planner writes. A console built before
these keys existed ignores them and draws the graph it drew before; a console
built after them must not require them of a CLI that predates them — an older
CLI's document has no `level`, no `ready` and no `critical_path`, and that
absence is the signal (the availability rule, §1), not an empty graph.

### 6.10 `fugaro runs stats --json` (**CLI 0.9.0**)

**Command:** `internal/cli/runs_stats.go` `newRunsStatsCmd` / `runRunsStats`.
**Document:** `userStatsDoc`, indented (`writeUserStats`), both in
`internal/cli/runs_stats.go` — cited by symbol, not by line: an indented
document is written by one function and a line number here rots at the next
insertion above it. This is the one command in this document the console
**does not spawn yet**: it is the data behind the Spend screen's by-person
view (CLI 0.9.0), and the section is here so the screen that follows has a
shape read off the code rather than assumed from a design. It is read-only —
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

## 7. The CLI gaps, settled against the code

The console ships **without any of these** and must work, visibly degraded
(design §4.3, D19). Each is a fact of this tree, with the symbol that proves
it. **The list was seven and it is now six: gap 5 is closed** by
`fugaro config list --json` (§6.8, console v2 plan V2.1.1). Its row is kept
below as the state the console was written against rather than deleted, so a
reader who finds the old count in the v1 design's §4.3 and in its plan finds
the closing here.

| # | The gap | Proved by | What the console does meanwhile |
|---|---|---|---|
| 1 | The degraded path prints a **plain sentence to stdout** on the same stream as the documents | `runWatchDegraded`, `internal/cli/watch.go` (`"%s\nrun records unavailable (retrying): %s\n"`) | Skip any line that is not a JSON object; show it as a header warning (§2, fact (iii)) |
| 2 | `version` has **no `--json`** and no flags at all | `newVersionCmd`, `internal/cli/root.go` — `cobra.NoArgs`, no `Flags()` | Parse the human line, pinned by a test |
| 3 | **`project show --json` does not exist on this tree** — there is no `fugaro project` command at all | no `newProjectCmd` in `internal/cli`; `docs/plans/2026-10-14-project-centric.md` phase 1 (PR #390) ships `project show`, `project policy publish` and `project repos add` — and only that one verb of the `repos` group comes with phase 1: the plan's phase 4 (Enrollment and migration, Task 4.1) is where `project repos ls`, `rm` and `sync` ship — **and no `project ls`** | The Projects screen renders the caps, kill state and repositories it can see, and says the policy object is not readable yet |
| 4 | **No schema version in the `watch --json` document** | `JSONDoc` (`internal/watch/json.go`) — no version field | Infer from the CLI version and from which shape arrived (the `degraded` key) |
| 5 | **Nothing enumerates installations** | `fugaro config` is `example\|show\|layer\|init\|publish` (`internal/cli/configcmd.go`); no `fugaro project` command exists; that phase's commands describe the *selected* project's policy and repositories, not the machine's installations | The operator adds each installation by hand (D13): binary picker, typed project name, optional checkout directory. **Closed 2026-10-18: `fugaro config list --json` exists (§6.8), so the typed name becomes a pick from the list of names the CLI prints — the field stays for an installation the operator adds before the CLI is updated** |
| 6 | **No PR field on a run that has not finished** | `JSONRun` carries `repo` and no `pr_url`/`pr_number`; they exist only on `JSONFinished`; `runview.Row` spells the pair `pr_url` + `pr` | A live row shows no link; the detail screen takes it from `diagnose RUN --json`'s `row` |
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
| `diagnose` | yes (`newDiagnoseCmd`) |
| `logs` | yes (`newLogsCmd`) |
| `secrets ls` | yes (`secretsOptions.addFlags`) |
| `accounts ls` | yes (`newAccountsLsCmd`) |
| `doctor` | yes (`newDoctorCmd`) |
| **`version`** | **no — it takes no flags at all** (`internal/cli/root.go`; the root's `--version`/`-v` is the root's own, not this subcommand's — §6.7) |
| **`config list`** | **no — it reads the operator's local project configs, not a cloud** (`internal/cli/config_list.go`, §6.8) |
| **`plan graph`** | **no — it reads the graph artifact in the checkout, not a cloud** (`internal/cli/plan.go`, §6.9) |

So the console passes `--project <name>` (and `--config <path>` when the
installation is not the ambient one) on **every spawn except the handshake**,
which runs `fugaro version` bare (never the root's `--version`, which older
CLIs in the console's supported range do not have — §6.7). A console that
appended `--project` to `version` would fail its own version check on the first
launch, on every machine. `docs/gcp-live-checklist.md`'s Check 43 guard states
the same exception. `TestDocsDesktopConsoleContractNamesTheFlagInventory` asserts this
table against the live constructors.

## 9. What the console may do with all this

1. **Every document in the console's set is read-only**, and the allow-list is
   by exact argv (design §5.1 rule 6). Two commands this file describes are
   writes, and neither is in the console's set: `fugaro cancel RUN --json`
   (§6.3), which is on the design's "never" table and which no v1 console
   spawns, and `fugaro accounts clear NAME` (§6.5a, credentials-service C1.21),
   the operator's own write to the cascade's state. Nothing else here names a
   write.
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
