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
`BuildJSON(project string, v View) JSONDoc` (`json.go:148`).

Emitted **compact, one object per line** (`json.NewEncoder(d.Out).Encode(...)`,
`WatchDeps.emit`, `internal/cli/watch.go`), which is what makes the feed
line-delimited JSON — one of exactly two line-delimited documents the console
reads, the other being `logs RUN --json` (§6.2, §9).

### `JSONDoc` (`internal/watch/json.go:97`)

| Field | Type | Unit | omitempty | Availability state |
|---|---|---|---|---|
| `project` | string | — | no | measured (`d.LC.Name`) |
| `mode` | string | — | no | **`observe` or `enforce`, never `""`** — `watch.Build` seeds `budget.ModeObserve` and switches only on `budget.ModeEnforce` (`internal/watch/build.go:256`), so an unset `/config/mode` reads `observe` here. `budget show --json` (§3) is the one document that can carry `""` |
| `day` | string | `YYYY-MM-DD`, UTC | no | measured — `View.Day`; `""` before the first day is set (`internal/watch/build.go:165`) |
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
| `watch --json` | `observe`, `enforce` — `Build` seeds `observe` and switches only on `enforce`, so an unset `/config/mode` reads `observe` here (`internal/watch/build.go:256`) |
| `budget show --json` | `observe`, `enforce`, `""` — an unset (or unrecognised) `/config/mode` reads `""` and appends a warning (`internal/cli/budget.go:457`) |

A console that treated `watch --json`'s `mode` as possibly `""` would carry a
comparison the feed never needs; a console that treated `budget show`'s as
never `""` would miss the warning.

### `JSONConn` (`internal/watch/json.go:24`) — the connectivity state

| Field | Type | Unit | omitempty | Availability state |
|---|---|---|---|---|
| `state` | string | — | no | `live`, `stale`, `offline`, `refused` or `polling` (`connNames`, `json.go:145`; the constants are `ConnLive`… in `internal/watch`) |
| `reason` | string | — | **yes** | why, when there is one |
| `age_seconds` | float64 | seconds | no | measured |

`polling` is the no-Firebase mode's own indicator (design §6.3); `refused` is a
permission refusal, which `watchStream` emits once and then returns `dbErr`
(`internal/cli/watch.go`).

### `JSONTotal` (`internal/watch/json.go:30`)

| Field | Type | Unit | omitempty | Availability state |
|---|---|---|---|---|
| `counted_usd` | float64 | USD | no | measured |
| `spent_usd` | float64 | USD | no | measured |
| `notional_usd` | float64 | USD | no | **notional** — never billed, never capped, never summed into a real total |
| `daily_cap_usd` | `*float64` | USD | no | **null = unavailable** (no cap set) |
| `percent` | `*float64` | 0–100 | no | **null = unavailable**, together with `daily_cap_usd` (`usdPtr`, `json.go:122`) |
| `burn` | `JSONBurn` | — | no | see below |
| `kill` | `*JSONKill` | — | no | **null = not killed** |
| `runs` | int | count | no | measured |
| `run_hours` | float64 | hours | no | measured |

### `JSONBurn` (`internal/watch/json.go:18`)

| Field | Type | Unit | omitempty | Availability state |
|---|---|---|---|---|
| `known` | bool | — | no | **the availability state of `usd_per_min`** |
| `usd_per_min` | float64 | USD/min | no | measured when `known`, **unavailable when not** — read `known` first |
| `fast` | bool | — | no | the fast-burn alert (`Burn.Fast`) |

The window is `watch.BurnWindow` (**5 minutes**, `internal/watch/state.go:31`),
the design's §6.2 screen-1 rule; Task 2.1 asserts the console's constant cannot
drift from it.

### `JSONKill` (`internal/watch/json.go:12`)

| Field | Type | Unit | omitempty | Availability state |
|---|---|---|---|---|
| `by` | string | — | **yes** | who, when known |
| `at` | string | RFC 3339 | **yes** | when, when known (`jsonKill`, `json.go:134`) |
| `reason` | string | — | **yes** | why |

### `JSONRepo` (`internal/watch/json.go:42`)

| Field | Type | Unit | omitempty | Availability state |
|---|---|---|---|---|
| `slug` | string | — | no | the wire key, `safetext.Strip`ped |
| `repo` | string | `owner/name` | no | measured |
| `counted_usd` / `spent_usd` | float64 | USD | no | measured |
| `notional_usd` | float64 | USD | no | **notional** |
| `daily_cap_usd` / `percent` | `*float64` | USD / 0–100 | no | **null = unavailable** |
| `burn` | `JSONBurn` | — | no | as above |
| `kill` | `*JSONKill` | — | no | **null = not killed** |

### `JSONRun` (`internal/watch/json.go:54`) — a live or queued run

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
| `health` | string | — | no | `ok`, `silent` or `lost` (`HealthSilent` = 60 s, `HealthLost` = 180 s — `SilentAfter`/`LostAfter`, `internal/watch/build.go:14`) |
| `deadline` | string | — | no | `ok`, `near` or `over` — amber past 80% of the stage deadline |
| `halted` | string | — | **yes** | why, when halted |
| `queued` | bool | — | **yes** | a run known only from the runs bucket (`MergeQueued`) |
| `stuck` | bool | — | **yes** | queued past `QueuedStuckAfter` (**10 min**, `internal/watch/queued.go:17`) |
| `workflow` | string | — | **yes** | from `task.json`, queued runs only |
| `requested_by` | string | — | **yes** | from `task.json`, queued runs only — **a person's address**: the Spend screen's person view is off by default (D15) |

**A live run carries no PR field at all** — no `pr_url`, no `pr_number`, no
`pr` (design §4.3 gap 6, §5.1 rule 2). The console renders no link for a live
row; the run-detail screen reads `diagnose RUN --json` for the one run whose
detail is open.

### `JSONFinished` (`internal/watch/json.go:83`) — a finished run

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
| `age_seconds` | float64 | seconds | no | measured |

### The three facts the whole feed turns on

These are also asserted by name in `TestDocsDesktopConsoleContractStatesTheThreeFeedFacts`.

**(i) One document per change, compact, at most one a second.**
`watchStream` (`internal/cli/watch.go:332`) emits when the model is
`dirty` **and** at least `gap` has passed since the last document, and in
`--json` mode `gap = watchJSONGap` = **`time.Second`** (`internal/cli/watch.go:33`).
The first document waits for all four streams or `watchFirstFrame` (**5 s**,
`internal/cli/watch.go:36`); a `refused` connection is the one case that
bypasses those tests — it emits immediately if the refusal arrives *during* the
stream, and the ordinary refusal emits nothing at all, because it is caught
before the stream starts (§10.5). The encoder is `json.NewEncoder(d.Out)` with **no `SetIndent`** —
one of the two compact, one-line-per-document commands in the console's set
(`logs RUN --json` is the other, §6.2).

**(ii) `--repo` narrows the JSON too.** `WatchDeps.emit` calls
`watch.FilterRepo(v, d.RepoKey)` **before** encoding (`internal/cli/watch.go:260`),
so `watch --json --repo R` carries only `R`'s repository block and runs;
`FilterRepo` is `internal/watch/json.go:192`. What *is* unfiltered is the other
axis: `finished[]` and `ready[]` ignore `--all`/`--keep`/`--keep-count`. The
cobra help text still says "--json is always unfiltered" (it means the
finished-run axis), so **the code is the authority here, not the help string**,
and the console remembers that a `--repo` feed is narrowed.

**(iii) The synthetic and degraded modes emit different things.**
`watchDegradedDoc` (`internal/cli/watch.go:399`) is `lsDoc` **embedded** plus
two fields:

| Field | Type | omitempty | Meaning |
|---|---|---|---|
| (everything `lsDoc` has) | — | — | `project`, `runs[]`, `totals`, `warnings[]` — see §5 |
| `degraded` | bool | no | always `true` in this mode |
| `notice` | string | no | `degradedNotice` = `"no budget backend: showing run records only"` (`internal/cli/watch.go:38`) |

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
**Document:** `budgetShowDoc` (`internal/cli/budget.go:310`), indented two
spaces (`enc.SetIndent("", "  ")`, `budget.go:580`).

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

`showGlobal` (`budget.go:258`): `daily_cap_usd` `*float64`, `per_run_cap_usd`
`*float64`, `counted_usd`/`spent_usd` float64, `notional_usd` float64
(**notional**), `headroom_usd` `*float64` (**null = unavailable**),
`kill` `*showKill` (null = not killed). All caps are **null = unavailable** and
never `0`.

`showDefaults` (`budget.go:268`): `repo_daily_cap_usd`, `repo_per_run_cap_usd`,
both `*float64`.

`showRepo` (`budget.go:273`): `slug`, `repo` (`omitempty`),
`daily_cap_usd` `*float64`, `daily_cap_source` (string, no `omitempty` —
**`repo`, `default` or `none`**), `per_run_cap_usd` `*float64`,
**`per_run_cap_source`** (string, no `omitempty` — `repo`, `default`, `global`
or `none`; `perRunCapSource`, `budget.go:293`), `counted_usd`, `spent_usd`,
`notional_usd`, `headroom_usd` `*float64`, `kill` `*showKill`,
`running` `[]showRun`. **`per_run_cap_source` is the "binding scope named"
the design's §6.2 Spend screen draws** — it is already in the document and
needs no CLI change (§4.3's "not a gap" note).

`showRun` (`budget.go:239`): `slug`, `run`, `repo`°, `workflow`°, `title`°,
`stage`°, `round` (**int**, `omitempty` — unlike `watch`'s string),
`auth`°, `coder`°, `reviewer`°, `spent_usd` float64 (no `omitempty`),
`pr_url`°, `halted`°, `started_at`°, `updated_at`° (RFC 3339 strings), `stale`
bool°. `°` = `omitempty`.

`showKill` (`budget.go:233`): `by`°, `at`°, `reason`° — the same shape as
`JSONKill`.

**Flags, read off the cobra command** (`budget.go:211`): `--repo`, `--all`,
`--json`, plus `addCloudFlags`' `--config`, `--project`, `--gcp-project`,
`--region`.

## 4. `fugaro report --json`

**Command:** `internal/cli/report.go` `newReportCmd` / `runReport`.
**Document:** `internal/report/render.go` `JSONDoc` (`render.go:200`), written
by **`report.JSON(w io.Writer, rep Report, m Meta) error` (`render.go:235`)** —
the exported writer, not a `cli`-side struct. Indented two spaces
(`render.go:246`). A schema note also lives above the type
(`render.go`'s comment block: `by`, `since`, `until`, `weeks`, `rows`,
`totals`, `warnings`).

`JSONDoc`: `project`, `source`, `degraded` bool, `notice`°,
`by` (string: `day`/`week`/`month`/`year`/`repo`/`model`/`person`),
`since`, `until` (UTC dates), `weeks` (**always the literal
`"iso-utc-monday"`** — the ISO week convention is declared, not inferred),
`rows` `[]JSONLine`, `totals` `JSONLine`, `warnings` `[]string`.

`JSONLine` (`render.go:173`): `key`, `start`°, `end`°, `partial` bool,
`model_micros` int64, `model_usd` string, `notional_micros` `*int64`,
`notional_usd` `*string`, `compute_micros` `*int64`, `compute_usd` `*string`,
`compute_estimated` bool, `run_hours` `*float64`, `runs` int64, `calls` int64,
`tokens` `*JSONTokens`°.

- **`*_usd` are exact decimal strings of the integer `*_micros`** (`USD6`), not
  floats: the console parses the string, and micros is the integer of record.
- **`notional_micros`/`notional_usd` are null when not tracked** —
  `w.NotionalTracked` (`jsonLine`, `render.go:214`). That is *unavailable*,
  and it is **notional**, never added to a real total (design §6.2, "one
  account of cost").
- **`compute_micros`/`compute_usd` are null unless `ComputeState ==
  ComputeKnown`**, and a present one is **estimated** (`compute_estimated`
  true). Null = *unavailable*, never `0`.
- **`run_hours` follows `compute_micros`** — set only when `ComputeState ==
  ComputeKnown` (`jsonLine`, `render.go:214`), so it is null for the same rows.
  The key carries **no `omitempty`**, so it is always present and **null**
  rather than absent when the hours are not known: read it as
  *unavailable*, never as a measured `0`. A degraded `report` is the whole
  document's null case — `reportDegraded` builds on the run records and sets
  `rep.NoHours = true` (`internal/cli/report.go:414`), and `source` says
  `"run-records"`, so every row's `run_hours` is null (there is no flag for
  this; the path is chosen by whether spend history is enabled).
- **`tokens` is absent entirely when not tracked** (`TokensTracked`).
- **`partial`** is the design's `(partial)` marker: the day has no final
  Firestore document. A day beyond the retained window is a **gap**, not a row
  of zeros.

`JSONTokens` (`render.go:192`): `in`, `out`, `cache_read`, `cache_write`, all
int64, none `omitempty`.

**Flags** (`report.go:81`): `--repo`, `--since`, `--until`, `--by`, `--json`,
`--csv` (the allow-list excludes `--csv`), plus the cloud flags.

## 5. `fugaro runs ls --json` (and the degraded feed's body)

**Command:** `internal/cli/runs.go` `newLsCmd` / `runLs`.
**Document:** `lsDoc` (`runs.go:63`), indented (`runs.go:529`), and
**embedded** by `watchDegradedDoc` (§2, fact (iii)).

| Field | Type | omitempty | Availability state |
|---|---|---|---|
| `project` | string | no | the Fugaro project |
| `runs` | `[]runview.Row` | no | `[]` when none |
| `totals` | `runview.Totals` | no | below |
| `warnings` | `[]string` | no | the lines a human listing prints before its table |

`runview.Row` (`internal/runview/runview.go:57`) is the run shape behind
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
| `halt` | `*runstore.Halt` | **yes** | why a run halted |
| `batch`, `requested_by` | string | **yes** | `requested_by` is a person's address |
| `pr_url` | string | **yes** | the URL as the host gave it |
| `execution`, `log_url` | string | **yes** | `log_url` is what `logs RUN --url` prints |
| `created` | time.Time | no | RFC 3339 |
| `cost` | `runstore.Cost` | no | model/compute dollars and the spend basis — *notional* when `ModelBasis == BasisSubscription` |
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

`runview.Totals` (`runview.go:436`): `runs` int, `model_usd` float64,
`model_notional_usd` float64 (**notional** — kept apart from `model_usd` by
`Sum`, `runview.go:448`), `compute_usd` float64 (**estimated**),
`total_usd` float64, `compute_not_estimated` int (**the count of rows whose
compute is unknown: their `compute_usd` 0 means "not estimated", so the sums
are a lower bound** — the console renders that, it does not hide it).

**Flags** (`f := cmd.Flags()`, `runs.go:100`): `--repo`, `--all`, `--mine`, `--since`, `--batch`,
`--pr`, `--watch`, `--interval` (hidden), `--json`, plus the cloud flags. The
allow-list excludes `--watch` (it is `watch` under another name).

## 6. The other documents

### 6.1 `fugaro diagnose RUN --json`

**Command:** `internal/cli/diagnose.go` `newDiagnoseCmd` (`Use: "diagnose RUN"`),
indented (`printDiagnosis`, `diagnose.go:445`). **Document:** `Diagnosis`
(`diagnose.go:40`) — a **flat** struct with no `MarshalJSON`, so its keys are
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
  indented document: `printLogEntry` (`logs.go:210`) marshals each entry with
  `json.Marshal` and writes it with a trailing newline, the same one-object-
  per-line shape as the `watch` feed, and the other of the two line-delimited
  documents the console reads. `logLine` (`logs.go:33`): `time` (RFC 3339),
  `severity`, `stage`°, `stream`°, `event`°, `message` (no `omitempty` on
  time/severity/message). Every field is **redacted** (`redactEntry`,
  `logs.go:199`), and `message` is job-written text: the display layer
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
future v1.1 button would consume. `cancelResult` (`internal/cli/cancel.go:45`):
`project`, `run`, `status`, `marker` bool, `hard` bool, `pr`°, `no_pr`° bool,
`lock_held`° bool. Its status constants (`cancelFinalized`, `cancelCancelled`,
`cancelNotLaunched`, `cancelLaunching`, `cancelUnfinalized`,
`cancelAlreadyFinished`) are the file's own.

**Its document is compact — one object per line**, the `logs RUN --json` shape
(§6.2) rather than the indented one every command in the console's set emits:
`printCancel` (`cancel.go:474`) marshals the struct with `json.Marshal` and
writes it with a trailing newline. A v1.1 button that parsed it as an indented
document would read nothing; `cancel` is one of the three commands this file
describes that are line-delimited, and the only one outside the console's set
(§9.2).

### 6.4 `fugaro doctor --json`

**Command:** `internal/cli/doctor.go` `newDoctorCmd`, indented
(`doctor.go:276`). **Document:** `doctorOutput` (`doctor.go:127`): `ok` bool,
`checks` `[]doctorCheck`, `project`° (`doctorProject`), `fugaro_yaml`°
(`doctorFugaroYAML`), `secrets`° (**`[]secretEntry` — names and metadata only,
never a value**), `plugin` (`pluginwire.Report`), `release`° (`releaseInfo`:
the binary's tag and commit), `error`° (**why doctor could not run its checks —
`--json` prints an object on every outcome**, so the console always gets
parseable JSON).

`doctorCheck` (`doctor.go:42`): `id`, `ok` bool, `problem`°, `fix`°,
`severity`°. **The environment view renders `problem` and `fix` verbatim**, as
the CLI worded them — including the "not on the `PATH` the console gives the
CLI" class of finding the design's §3.4 rule 5 describes.

**Flags** (`doctor.go:195`): `--dir` (default `.`), `--plugin`, `--strict`,
`--json`, **`--check-github-app`** — the last of which reads the App's private
key out of Secret Manager and is therefore unreachable by the console by
argument list, not by command name (design §5.1's second table). The console
passes `--dir D` and **never `--strict`** (its failure mode is the checkout's
plugin wiring, not the environment the console draws).

### 6.5 `fugaro accounts ls --json`

**Command:** `internal/cli/accounts.go` `newAccountsLsCmd`, indented
(`accounts.go:159`). **Document:** `accountsListDoc` (`accounts.go:60`):
`repo` string, `accounts` `[]string`, `complete` bool,
`note`° (why selection failed). **`complete: false` means the project config
could not be selected — the screen renders `unavailable`, never "no
accounts"** (the type's own doc comment is the authority). Flags: `--repo`
(always passed explicitly), `--json`, plus the cloud flags. Local and offline:
"never calls Secret Manager".

### 6.6 `fugaro secrets ls --json`

**Command:** `internal/cli/secrets.go` `newSecretsLsCmd`, indented
(`secrets.go:668`). **The document is a bare JSON array** — `enc.Encode(entries)`
(`secrets.go:669`), not an object with a wrapper key. Each element is
`secretEntry` (`secrets.go:486`): `name`° (the logical name, from the
`fugaro_secret` label), `scope`° (`repo` / `project` / a project secret with a
repo override), `allowed`° (`[]string`, `--project-wide` only), `missing`° (`true` on a name a job of the repository needs that has no enabled version — `--repo` only; such a row has an empty `id` and no `created`), and the
embedded **`gcp.SecretInfo`** (`internal/backend/gcp/secrets.go:40`), whose own
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
variable (`root.go:13`, `var Version = "dev"`), i.e. the release tag of a
built binary. There is **no `--json` and no flag of any kind**, which is why
the handshake is the one spawn that carries no `--project` (§8), and why the
root command sets no cobra `Version` field (`fugaro --version` fails). The
console parses the human line, pinned by a test against the real output
(design §4.3 gap 2).

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

**An installation whose config cannot be read is still listed, by name, with a
warning** — a picker that offered a name and then failed silently is worse than
one that says what it could not read.

**The document is a named set of fields and it is deliberately not a marshalled
`localcfg.Config`.** That struct carries the operator's providers and their base
URLs, the named Claude accounts, the reserved project secrets, the Terraform
block and the whole repository list — none of which the picker needs, and all of
which would join this document the day a field is added to the struct. A test
fails if a field outside the five above reaches the output, and if either type
ever becomes (or embeds) `localcfg.Config`.

**Version skew.** The command is new, so an older CLI does not have it at all:
the spawn fails and the console shows its "this CLI cannot list installations"
state, which is the form D13 accepted for v1. The handshake (§6.7) is what tells
the console which case it is in.

### 6.9 `fugaro plan graph --json`

**Command:** `internal/cli/plan.go` `newPlanGraphCmd`, the only subcommand of
`fugaro plan`. **Flags:** `--plan <path>` (the plan document the graph sits
beside; with none, the one graph artifact under `docs/plans` is read, and more
than one is refused and listed rather than picked), `--json`. **It takes no
cloud flags**, for the same reason as §6.8: it reads a file in the checkout.

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

one entry of `nodes`:

| Key | Type | Absent | Null | Notes |
| --- | --- | --- | --- | --- |
| `id` | string | never | never | the planner's own name for the task; unique in the document |
| `title` | string | never | never | what a person reads |
| `run` | string | never | **yes** | the run this task belongs to; null when it belongs to none |
| `version` | string | never | **yes** | the target version; null when the plan does not say |
| `footprint` | object | never | never | the expected paths, below |

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

**The footprint is advisory and it is a prediction; the measured set is a
different document.** What a task *expected* to touch is this field; what a run
*did* touch is the run's own record (D27), written after the fact. The console
labels the first as a prediction wherever it shows it, and the label is in the
document rather than in the renderer.

**Version skew.** Both the command and the document's `schema_version` answer
this, and they answer different halves: an older CLI has no `plan graph` at all,
and a newer *planner* can write a `schema_version` the CLI refuses. The console
shows the CLI's refusal sentence rather than a graph.

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
| 4 | **No schema version in the `watch --json` document** | `JSONDoc`, `internal/watch/json.go:97` — no version field | Infer from the CLI version and from which shape arrived (the `degraded` key) |
| 5 | **Nothing enumerates installations** | `fugaro config` is `example\|show\|layer\|init\|publish` (`internal/cli/configcmd.go`); no `fugaro project` command exists; that phase's commands describe the *selected* project's policy and repositories, not the machine's installations | The operator adds each installation by hand (D13): binary picker, typed project name, optional checkout directory. **Closed 2026-10-18: `fugaro config list --json` exists (§6.8), so the typed name becomes a pick from the list of names the CLI prints — the field stays for an installation the operator adds before the CLI is updated** |
| 6 | **No PR field on a run that has not finished** | `JSONRun` (`json.go:54`) carries `repo` and no `pr_url`/`pr_number`; they exist only on `JSONFinished` (`json.go:91-92`); `runview.Row` (`runview.go:57`) spells the pair `pr_url` + `pr` (`runview.go:109`) | A live row shows no link; the detail screen takes it from `diagnose RUN --json`'s `row` |
| 7 | **No provider on any shape** | `JSONRun`, `JSONFinished` and `runview.Row` each carry `repo` and no provider or origin host; `doctor --json`'s `doctorOutput` reports none; the kinds are `github`/`bitbucket`/`fake` (`internal/task/task.go`'s `Slug`, which hashes provider and repository together **one way**, so a slug cannot be inverted) | **No PR link at all** (design §5.1 rule 2): repository, number and `pr_url` shown as text, never handed to `openExternal`. A GitHub-shaped guess is wrong for `edgeappinc/fugarosandbox`, which is Bitbucket |

**And no shape anywhere has a `repository` field** — the repository is `repo`
on every one of them — and **no shape carries a provider**, which is why a
link rebuilt as `https://github.com/<repo>/pull/<n>` is not merely useless but
wrong (design §5.1 rule 2).

Two things that look like gaps and are not: `budget show --json` already
carries the effective per-run cap with its binding scope
(`showRepo.per_run_cap_source`, §3), and `secrets ls` already has a
metadata-only form (§6.6).

## 8. The flag inventory the spawn contract turns on

Read off each cobra command — `addCloudFlags` (`internal/cli/cloud.go:51`) —
and **not** off this document or the design:

| Command | `--config`, `--project` (and `--gcp-project`, `--region`) |
|---|---|
| `watch` | yes (`internal/cli/watch.go:170`) |
| `budget show` | yes (`internal/cli/budget.go:229`) |
| `report` | yes (`internal/cli/report.go:88`) |
| `runs ls` | yes (`internal/cli/runs.go:111`) |
| `diagnose` | yes (`internal/cli/diagnose.go:98`) |
| `logs` | yes (`internal/cli/logs.go:58`) |
| `secrets ls` | yes (`internal/cli/secrets.go:74`, via `secretsOptions.addFlags`) |
| `accounts ls` | yes (`internal/cli/accounts.go:43`) |
| `doctor` | yes (`internal/cli/doctor.go:195`) |
| **`version`** | **no — it takes no flags at all** (`internal/cli/root.go`) |
| **`config list`** | **no — it reads the operator's local project configs, not a cloud** (`internal/cli/config_list.go`, §6.8) |
| **`plan graph`** | **no — it reads the graph artifact in the checkout, not a cloud** (`internal/cli/plan.go`, §6.9) |

So the console passes `--project <name>` (and `--config <path>` when the
installation is not the ambient one) on **every spawn except the handshake**,
which runs `fugaro version` bare. A console that appended `--project` to
`version` would fail its own version check on the first launch, on every
machine. `docs/gcp-live-checklist.md`'s Check 43 guard states the same
exception. `TestDocsDesktopConsoleContractNamesTheFlagInventory` asserts this
table against the live constructors.

## 9. What the console may do with all this

1. **Every document in the console's set is read-only**, and the allow-list is
   by exact argv (design §5.1 rule 6). The one command this file describes that
   is a write is `fugaro cancel RUN --json` (§6.3), which is on the design's
   "never" table and which no v1 console spawns; nothing else here names a
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
