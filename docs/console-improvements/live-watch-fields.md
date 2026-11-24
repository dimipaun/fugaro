# The Watch fields, and where Live draws them

Every field of `fugaro watch --json` — every `json:"…"` member of `JSONDoc` and of each of its row
types in `internal/watch/json.go` — against the Live pane, the way the spec's "the Live pane conveys
at least what Fugaro Watch shows" ([spec.md](spec.md) §2) can be checked rather than assumed.

**Reconciliation F1 is why this file exists.** The Live pane already draws from `watch --json` and
follows Watch's ordering, short IDs and state words (`console/test/unit/live-parity.test.ts`); its
columns are the Flight Deck's own on purpose (`internal/watch/console_parity_test.go`). What was
missing is a table of Watch's fields against Live's, so "at least what Watch shows" could only be
believed. This is that table, and its test is
`console/test/unit/live-watch-fields.test.ts` (plan **LP1**, decision **D2901**).

The test reads `internal/watch/json.go` as text and walks the recorded parity fixture
(`console/test/fixtures/watch-parity.json`), so:

- a member the CLI adds and the fixture does not carry — `omitempty`, or a view this recording does
  not fill — still fails the test, because the struct is what the check enumerates, not the fixture;
- a row that draws nothing must say `not drawn` **with a reason**, and a row that claims a drawing
  must name a class `console/src/renderer/screens/live.ts` really draws;
- a member with no row is a **gap this table names**, one that becomes a task by amendment and never
  a silent addition.

Fields are named as the wire paths `live.ts` itself uses: `runs[].stage`, `total.kill.by`,
`repos[].burn.fast`. The element column names the class a test or the stylesheet can find the drawing
by, with `data-*` where the class is shared and the attribute is what says which thing it is.

## The table

| Watch field | Live element |
| --- | --- |
| `project` | `live-project` |
| `mode` | `live-mode` |
| `day` | `live-day` |
| `now` | not drawn — read for the histories' stamps, the red zone's cutoff and the silence mark; the only timestamp on screen is the feed's own (`live-since-at`) |
| `connection` | `live-connection` |
| `total` | `live-fleet` |
| `repos` | `live-repo` |
| `runs` | `live-runs` |
| `drift` | not drawn — the CLI's own price-drift alarm (R6, F30); the console's decoder does not read the key at all, so no screen can |
| `finished` | not drawn — decoded but never read: the review inbox is fed by `ready[]` alone, and the rest of `finished[]` is a history this pane deliberately is not |
| `ready` | `live-review` |
| `queued_note` | `live-note` |
| `connection.state` | `live-connection` (and its `data-state`) |
| `connection.reason` | `live-connection-reason` |
| `connection.age_seconds` | `live-connection-age` |
| `total.counted_usd` | `live-counted` |
| `total.spent_usd` | `live-fleet-key` (the cap legend's Spent row, drawn only when `leased_usd` splits the money; with no split the same money is the `live-counted` figure) |
| `total.notional_usd` | `live-total-notional` |
| `total.leased_usd` | `live-fleet-key` (the Leased row of the cap legend, `n/a` when the document carries none) |
| `total.daily_cap_usd` | `live-cap` |
| `total.percent` | `live-percent` |
| `total.max_parallel` | `live-fleet-cap-limit` |
| `total.burn` | `live-fleet-burn` |
| `total.kill` | `live-kill` |
| `total.runs` | not drawn — the decoder (`totalsOf` in `console/src/shared/schema.ts`) does not read the key; the runs a reader can count are the tables' rows (`live-runs`) |
| `total.run_hours` | not drawn — the decoder does not read the key |
| `total.burn.known` | not drawn — it chooses between the rate and `n/a`; the figure itself is `live-fleet-burn` |
| `total.burn.usd_per_min` | `live-fleet-burn` |
| `total.burn.fast` | not drawn — the console reads `known` and `usd_per_min` of the fleet's burn; `data-fast` is a repository's (`live-burn-unit`) |
| `total.kill.by` | `live-kill-by` |
| `total.kill.at` | `live-kill-at` |
| `total.kill.reason` | `live-kill-reason` |
| `repos[].slug` | `live-repo` (as its `data-key`) |
| `repos[].repo` | `live-repo-name` |
| `repos[].counted_usd` | `live-repo-counted` |
| `repos[].spent_usd` | not drawn — decoded and carried on the row, but the head draws `notional_usd` under the word `spent` (the `~` is what makes that honest); a repository's spent-within-a-lease is not a figure this screen holds |
| `repos[].notional_usd` | `live-repo-notional` |
| `repos[].leased_usd` | not drawn — decoded, and `buildLiveModel` does not even read it: the three-way money split belongs to the fleet panel |
| `repos[].daily_cap_usd` | `live-repo-cap` |
| `repos[].percent` | `live-repo-percent` |
| `repos[].burn` | `live-burn-rate` |
| `repos[].kill` | `live-kill` |
| `repos[].burn.known` | not drawn — it chooses between the rate and `n/a`; the figure itself is `live-burn-rate` |
| `repos[].burn.usd_per_min` | `live-burn-rate` |
| `repos[].burn.fast` | `live-burn-unit` (as its `data-fast`) |
| `repos[].kill.by` | `live-kill-by` |
| `repos[].kill.at` | `live-kill-at` |
| `repos[].kill.reason` | `live-kill-reason` |
| `runs[].run` | `live-run-id` (with `data-run-id`, the row's door) |
| `runs[].slug` | not drawn — the row keeps it to group runs under their repository and to disambiguate the drawn ending; the door is `runs[].run` and the repository's name is `repos[].repo` |
| `runs[].repo` | not drawn — decoded and never read: the name beside the table is `repos[].repo` |
| `runs[].title` | `live-run-title` |
| `runs[].stage` | `live-run-stage` |
| `runs[].round` | `live-run-round` |
| `runs[].verify` | not drawn — decoded and carried on the row, and no cell draws it; the pipeline line says the stage and the round |
| `runs[].models` | `live-run-models` |
| `runs[].recipe` | not drawn — decoded and carried on the row, and no cell draws it |
| `runs[].auth` | `live-run-auth` |
| `runs[].mode` | not drawn — the decoder does not read the key; the rigor chip (`live-run-mode`) draws the join out of `runs ls --json` (`runs[].rigor.mode`), which is another document's field |
| `runs[].from` | not drawn — the decoder does not read the key |
| `runs[].source` | not drawn — the decoder does not read the key; what the run's running stage goes out on, in place of the auth column, is the visibility plan's VS1 |
| `runs[].notional` | not drawn — it marks the spend reading `~$`; the figure is `live-run-spend` |
| `runs[].spent_usd` | `live-run-spend` |
| `runs[].age_seconds` | `live-run-age` |
| `runs[].health` | `live-run-flag` (as its `data-flag`; the chip `live-run-chip` carries the word when the health *is* the row's state) |
| `runs[].deadline` | `live-run-flag` |
| `runs[].halted` | not drawn — decoded and never read |
| `runs[].queued` | `live-run-queued` (the row's class, with `data-queued`) |
| `runs[].stuck` | `live-run-stuck` (the row's class, and a `live-run-flag`) |
| `runs[].workflow` | not drawn — decoded and carried on the row, and no cell draws it |
| `runs[].requested_by` | not drawn — decoded and carried on the row, and no cell draws it |
| `runs[].job` | not drawn — the decoder does not read the key |
| `runs[].profile` | not drawn — the decoder does not read the key |
| `runs[].provider` | not drawn — the decoder does not read the key |
| `runs[].account` | not drawn — the decoder does not read the key |
| `runs[].launched_by` | `live-run-started-by` |
| `runs[].plan_task` | not drawn — the decoder does not read the key |
| `runs[].pr_number` | not drawn — decoded and never read by the Live screen; the number a reader sees is a finished run's (`live-review-number`) |
| `runs[].credential` | not drawn — the decoder does not read the key |
| `runs[].waiting_until` | not drawn — the decoder does not read the key |
| `drift[].model` | not drawn — `drift[]` is not decoded (see `drift` above) |
| `drift[].reported_usd` | not drawn — `drift[]` is not decoded |
| `drift[].table_lo_usd` | not drawn — `drift[]` is not decoded |
| `drift[].table_hi_usd` | not drawn — `drift[]` is not decoded |
| `drift[].factor` | not drawn — `drift[]` is not decoded |
| `drift[].direction` | not drawn — `drift[]` is not decoded |
| `finished[].run` | not drawn — `finished[]` is not read; a ready run's door is `live-review-run-id` |
| `finished[].slug` | not drawn — `finished[]` is not read |
| `finished[].repo` | not drawn — `finished[]` is not read; a ready run's repository is `live-review-repo` |
| `finished[].title` | not drawn — `finished[]` is not read; a ready run's title is `live-review-run-title` |
| `finished[].status` | not drawn — decoded and never read: whether a finished run is ready is the CLI's own conclusion, arrived as `ready[]` |
| `finished[].outcome` | not drawn — decoded and never read, for the reason `finished[].status` gives |
| `finished[].failed` | not drawn — decoded and never read |
| `finished[].pr_url` | not drawn — `finished[]` is not read; a ready run's pull request is `live-review-url` |
| `finished[].pr_number` | not drawn — `finished[]` is not read; a ready run's number is `live-review-number` |
| `finished[].age_seconds` | not drawn — `finished[]` is not read; a ready run's age is `live-review-age` |
| `finished[].job` | not drawn — the decoder (`FinishedView`) does not read the key |
| `finished[].profile` | not drawn — the decoder does not read the key |
| `finished[].provider` | not drawn — the decoder does not read the key |
| `finished[].account` | not drawn — the decoder does not read the key |
| `finished[].launched_by` | not drawn — the decoder does not read the key |
| `finished[].plan_task` | not drawn — the decoder does not read the key |
| `finished[].resume_at` | not drawn — the decoder does not read the key |
| `finished[].resumed_by` | not drawn — the decoder does not read the key |
| `ready[].run` | `live-review-run-id` (with `data-run-id`) |
| `ready[].slug` | not drawn — the row keeps the repository's display name (`ready[].repo`) and not the slug |
| `ready[].repo` | `live-review-repo` |
| `ready[].title` | `live-review-run-title` |
| `ready[].status` | not drawn — decoded and never read: the CLI already decided this run is ready |
| `ready[].outcome` | not drawn — decoded and never read, for the reason `ready[].status` gives |
| `ready[].failed` | not drawn — decoded and never read |
| `ready[].pr_url` | `live-review-url` |
| `ready[].pr_number` | `live-review-number` |
| `ready[].age_seconds` | `live-review-age` |
| `ready[].job` | not drawn — the decoder (`FinishedView`) does not read the key |
| `ready[].profile` | not drawn — the decoder does not read the key |
| `ready[].provider` | not drawn — the decoder does not read the key |
| `ready[].account` | not drawn — the decoder does not read the key |
| `ready[].launched_by` | not drawn — the decoder does not read the key |
| `ready[].plan_task` | not drawn — the decoder does not read the key |
| `ready[].resume_at` | not drawn — the decoder does not read the key |
| `ready[].resumed_by` | not drawn — the decoder does not read the key |

## The gaps this table names

Each row says `not drawn` with its reason, and the reasons fall into three kinds. None of them is a
silent omission: each is a field Watch prints (or the CLI writes) that Live does not, and each is a
candidate for a task by amendment rather than something this table lets pass.

1. **Not decoded at all** — the CLI writes it and the console never reads the key: the whole of
   `drift[]`; `total.runs` and `total.run_hours`; `runs[]`'s `mode`, `from`, `source`, `job`,
   `profile`, `provider`, `account`, `plan_task`, `credential` and `waiting_until`; `finished[]` and
   `ready[]`'s `job`, `profile`, `provider`, `account`, `launched_by`, `plan_task`, `resume_at` and
   `resumed_by`. A screen that wants one of these needs the decoder first.
2. **Decoded and never drawn** — the console reads it and no cell shows it: `runs[]`'s `verify`,
   `recipe`, `workflow`, `requested_by`, `halted`, `repo` and `pr_number`; `repos[].spent_usd` and
   `repos[].leased_usd`; `total.burn.fast`; and, on both finished lists, `slug`, `status`, `outcome`
   and `failed`.
3. **Drawn only through another field** — the value is not a cell of its own: `now` (the histories'
   stamps), `runs[].notional` (the `~$` on the spent reading), and `total.burn.known` /
   `repos[].burn.known` (the choice between a reading and `n/a`).

The gaps are the honest half of the parity claim: Live draws what Watch shows *that this build
reads*, and everything it does not is written down here rather than left for a reader to discover by
comparing two windows.
