# The run index contract

C-INDEX of [implementation-plan.md](implementation-plan.md). This file fixes the
shapes `internal/runindex` declares: the two Firestore collections, every field
and its Firestore type, the sort key and the composite indexes that serve it,
the RTDB overlay a read merges in (D3202), the writer lease with the reindex
cursor (F39, D3215), and the three `--json` additions the readers make.

**Status: the contract, and the package that declares it.** `internal/runindex`
holds `Entry`, `Summary`, `Filter`, `Cursor`, `Page` and the `Reader` interface
(C-INDEX); the queries, the reader, the writer, the indexes and the wiring are
RI1–RI5, IR1–IR6 and I-INDEX. Until I-INDEX lands, the commands compile against
the package's stub (`internal/runindex/stub.go`), which reads nothing and says
the index is incomplete, so no reader prints an empty list as the truth.

Where this file and the plan disagree, the plan wins and this file is wrong.

## What the index is

Today every list the console draws is history-linear: `runs ls` lists every run
in its window, `plan status` reads 90 days of run objects, `runs stats` reads up
to `userStatsMaxRuns` (5,000) of them (F1, F2, F32). The index replaces those
reads with one Firestore query and one document, whatever the history behind
them.

It is written by the history job's sweep alone (D3201): that job already holds
`datastore.user` and read on the runs bucket, so the index costs no new IAM, and
no other service account reads it (F8). A person's CLI reads it as the person,
which is why the read is a query the CLI makes and not a service the console
calls (D3216: the console never reads Firestore).

## The collections

| Path | What it is | Written by |
| --- | --- | --- |
| `runIndex/<run id>` | one run, one document; the document id **is** the run id | the history job's sweep and its `--reindex` mode (RI4, RI5) |
| `runIndexMeta/summary` | the installation's counts, its completeness and the estimate table | the same, as deltas on a fold and recomputed on a reindex |
| `runIndex/lease` (RTDB) | the index writer's lease, and the reindex cursor while a walk is unfinished | the job; `fugaro update` and `fugaro runs reindex` reset the cursor (RI5) |

Two things are deliberately not in Firestore. The **plan nodes and edges** come
from the plan files and the orchestrator state as they do today (D3223): the
index holds runs and the summary, and a plan is not copied into it. The **merge
state of a pull request** is not something the history job can know — it holds
no forge credential — so it stays where it is, in `plan status`'s bounded `gh`
lookups (F11, F37).

The two documents are read by Fugaro's own binaries and by nothing else, so
neither is a published schema: the summary carries `version` (below) because the
history job's image may lag the CLI that reads it, and the entry carries the
run's own records, whose versions are the record's and the launch's.

## The run document

`runIndex/<run id>`, as `runindex.Entry`. The document id is the run id, which
is what makes this one collection rather than one per repository: a run id is
minted with the moment of its launch (`task.NewRunID`, `20060102-150405-xxxx`)
and two repositories of one installation do not mint the same one, so a point
read needs no slug beside it.

| Field | Firestore type | Written from | Read by |
| --- | --- | --- | --- |
| `run_id` | string | the run id, and the document's own id | every reader |
| `slug` | string | the repository's storage key, `runs/<slug>/` | `runs ls`, `runs stats` (their scope) |
| `repo` | string | the task's `owner/name`, with the slug as the fallback a reader shows where the task recorded none | the row's display name |
| `updated_at` | timestamp | the record's `finished_at`, else its `started_at`, else the launch's `launched_at`, else the run id's mint time (`runstore.RunTime`) | **the sort key**; the `--since` window |
| `status` | string | `result.json`'s `status`, one of `runstore.Statuses`; absent when the record settled none | `--state`, the active set, `runs stats`, the estimate table |
| `plan_task` | string | `launch.json`'s `plan_task` | `plan status`, `graph` (the join) |
| `pr` | integer | the record's `pr.number`, else the task's pull request | `--pr` |
| `requested_by` | string | the task's `requested_by` | `--mine` |
| `launched_by` | string | `launch.json`'s `launched_by` | `runs stats` (its per-user rows) |
| `batch` | string | the task's `batch` | `--batch` |
| `tier` | string | `result.json`'s `tier.tier` | the estimate table (its groups) |
| `rigor` | string | `result.json`'s `rigor`, read through `RigorRecord.LevelOf` | the estimate table (its groups) |
| `started_at`, `finished_at` | timestamp | the record's own window; both absent when the record carries none | the estimate table (its durations) |
| `workflow`, `title`, `recipe`, `oauth_account`, `previous_run` | string | the run's task, joined with the record as `runview.Join` joins them | `runs ls` |
| `follow_up` | boolean | the task's own follow-up fields | `runs ls` |
| `follow_up_of` | map | the task's `follow_up_of` | `runs ls` |
| `launch` | map | `launch.json`, whole (`runstore.Launch`) | every reader that prints a row |
| `record` | map | `result.json`, whole (`runstore.Record`) | every reader that prints a row |

The fields above the two record blocks are the index's own: the keys it is
queried, ordered and joined by, and the few facts of the run's task a listing
prints. The task itself is **not** in the document — its prompt is the largest
thing about a run and no reader of the index shows it — which is why the
handful of task-derived row fields are projected beside the records. The
records are carried whole for the reason the plan's "a field never changes
meaning" rule gives: a field either record gains later arrives without the
index's shapes changing, and a row a reader builds from an entry is the row it
built from the bucket.

A run whose `result.json` cannot be read carries no `record`: the entry is still
written, because the run exists and its task and launch do. A reader that has to
tell "corrupt" from "never finalized" reads the bucket, as the listing's own
`record unreadable` row already does — it is not this reader's job to invent the
distinction.

**Every entry carries a sort key**, and the last fallback is what a run that was
claimed and never launched has: a `task.json` and nothing else, so neither a
record nor a launch to take a moment from. It is the run id's own mint time
(`runstore.RunTime`), which every run id names. Without it such an entry would
have no `updated_at` and would fall outside **every** window, and `runs stats`'s
`unlaunched` — the window's count of runs that never started — would be zero
under the index while the record read counts them: that read's window is the run
id's mint time too (`runstore.ListRunIDs`, which is also why the two counts are
the same set and not merely two counts).

### The cost and compute figures the readers take

`record.cost` is `runstore.Cost` as `result.json` carries it, and its members
are the record's own (that file is their definition; nothing here re-spells
them). What the index's readers take from it is fixed here:

| Field | Firestore type | Who reads it and how |
| --- | --- | --- |
| `record.cost.model_usd` | double | `runs stats`, the row's model figure |
| `record.cost.model_notional_usd` | double | `runs stats`: the subscription's part, through `Cost.ModelSubscriptionUSD` |
| `record.cost.model_basis` | string | the fallback `Cost.ModelSubscriptionUSD`/`AllNotional` already carry for a record written before C1.16 — read through those methods and **never compared directly** (P12, D140; `internal/runstore/cost_readers_guard_test.go` holds the rule over every reader in the tree) |
| `record.cost.compute_usd`, `record.cost.compute_estimated` | double, boolean | the row's compute figure and whether it is an estimate at all |
| `record.cost.total_usd` | double | the estimate table's cost samples (`estimate.Build`) |
| `record.cost.model_by`, `route_by`, `reported_by` | map of double | the row's own cost block, printed as it stands |
| `record.cost.model_tokens` | map of map of integer | the row's token counts; `fugaro estimate` reprices a run from them |
| `record.cost.cache_read_tokens`, `cache_write_tokens` | integer | the row's cache totals (design §3's prompt-caching risk) |
| `record.cost.service_tiers` | array of string | the row's own list of unmultiplied service tiers |
| `record.cost.unreconciled`, `usage_unparsed`, `catalog_stale` | double, integer, boolean | the row's own cost block |

Nothing else in the record is a figure the summary or a reader aggregates: the
summary's money is summed from these by the fold, in integer micro-dollars
(below), and no reader adds a double.

## The summary document

`runIndexMeta/summary`, as `runindex.Summary`. It is the document a reader
reads when it wants a figure rather than a page: the totals `runs ls` prints
beside its rows, the completeness every listing has to know before it prints a
short list, and the estimate table `plan status` draws from (F13, F15).

| Field | Firestore type | Meaning |
| --- | --- | --- |
| `version` | integer | the document's schema version; `1` for this release (`runindex.Version`) |
| `complete` | boolean | true once a walk has reached the end of history (D3215, RI5) |
| `folded_through` | timestamp | the moment the index is folded through; absent before the first fold |
| `updated_at` | timestamp | when the summary was last written |
| `runs` | integer | every run the index carries |
| `states` | map of integer | the runs by how they ended: one key per `runstore.Statuses` value (`running`, `succeeded`, `failed`, `unreviewed`, `infra_error`, `cancelled`, `halted`), every key present |
| `spent_micros` | integer | the model money the caps count, in micro-dollars |
| `notional_micros` | integer | the part of the model figure a subscription paid, in micro-dollars; never added to `spent_micros` |
| `compute_micros` | integer, absent when no run's compute is known | what the runs whose compute was priced came to |
| `compute_runs` | integer | how many runs `compute_micros` covers |
| `compute_not_estimated` | integer | how many runs carry no compute figure: "not estimated" is not "free" |
| `estimate` | map | the estimate table, in `estimate.Table`'s own shape (`schema_version`, `written_at`, `groups`) |

A run whose record settled no status is in `runs` and in no state, so `runs` is
at least the sum of `states`, and a state count is never negative — the same two
rules `fugaro runs summary` already publishes. The money is integer
micro-dollars for the same reason that document uses them: a fold moves the
figures by an exact amount, and a float sum drifts.

## The sort key, the cursor and the composite indexes

**The order is `updated_at` descending, then the run id.** Every page of every
reader is in it, and a cursor is a position in it. Since the document id is the
run id, the tiebreak is Firestore's own `__name__` order, which is ascending —
so of two runs whose `updated_at` is the same, the smaller run id comes first,
and a page continues from the value pair `(updated_at, run_id)`.

`runindex.Cursor` is that position: `updated_at` (absent for the reindex walk,
whose order is the run id alone), `slug` (set by the reindex walk alone) and
`run_id` (required). `Encode` renders it as an opaque token and `DecodeCursor`
reads one back, refusing anything else — a walk that read a bad token as "start
at the beginning" would page a listing from its first row for ever, and a
reindex that did would re-walk history it had already folded.

`runs ls --after` stays a **run id** on the wire, as the console's allow-list
checks it (IR1): the reader resolves it to that run's sort key — from its index
document, or from the overlay for a run the index does not carry yet — and
starts after it. `Filter.After` is that run id, and `Page.Next` is the cursor
the caller hands back as the next run id.

Firestore indexes every single field automatically, so a query ordered by
`updated_at` alone needs nothing. A query that also filters needs a composite
index, and these are the ones the index's readers make — the closed list RI2
declares as `google_firestore_index` resources. Firestore appends the document
name to each as its final sort key.

| Fields (in order) | The query it serves |
| --- | --- |
| `slug` ASC, `updated_at` DESC | one repository's runs, newest first: `runs ls`, `runs stats` |
| `status` ASC, `updated_at` DESC | the active set across the installation (D3203) |
| `slug` ASC, `status` ASC, `updated_at` DESC | a repository's runs in one state: `--state` |
| `plan_task` ASC, `updated_at` DESC | one plan task's runs: `plan status`, `graph` |
| `slug` ASC, `plan_task` ASC, `updated_at` DESC | a plan's runs in one repository: `plan status` |
| `pr` ASC, `updated_at` DESC | one pull request's runs, unbounded by repository (D3204) |
| `requested_by` ASC, `updated_at` DESC | one identity's runs, unbounded by repository |
| `batch` ASC, `updated_at` DESC | one batch's runs, unbounded by repository |
| `slug` ASC, `pr` ASC, `updated_at` DESC | one repository's pull request: `runs ls --pr`, which needs exactly one repository (D3204) |
| `slug` ASC, `requested_by` ASC, `updated_at` DESC | one identity's runs in the repositories a listing resolved: `runs ls --mine` |
| `slug` ASC, `batch` ASC, `updated_at` DESC | one batch's runs in the repositories a listing resolved: `runs ls --batch` |

`Slugs` is what decides which of a pair above a read needs, and a listing always
sets it: `runs ls` resolves it from the checkout's repositories, from `--repo`,
or — under `--all` — by listing every slug of the installation, and `--pr`
refuses anything but exactly one repository (`internal/cli/runs.go`). So `--pr`,
`--mine` and `--batch` each arrive with a `slug` bound beside their own, and
each is served by the row that names both.

A filter the index does not serve is a filter a reader must not apply after the
page was cut — a page shorter than the caller asked for is the failure this
whole package exists to prevent. A new equality filter therefore adds its row
here and its resource in RI2 in the same change.

## The overlay (D3202)

A 15-minute writer leaves a run that finished minutes ago out of the index, so
every read adds what changed since the index's own stamp. The overlay is the
RTDB the installation already has:

- `agents/<slug>/<run>` — the live registry (`budget.AgentEntry`): a run in
  flight has a node here, and its `updatedAt` says how fresh that account is.
- `outcomes/<day>/<slug>/<run>` — the settled outcome (`budget.Outcome`), one
  node per finished run, under the day it finished.

The reader reads the live set and the outcome days at or after `folded_through`,
in parallel with the query, and merges them by **run id**, which is what the
index is keyed by. The read is bounded by the fold's own freshness — about
fifteen minutes of activity, not by history (F9).

**The overlay supplies a run's existence, its liveness and its status, and
nothing else.** The two nodes carry more than that — `AgentEntry` names a
`title`, a `prUrl`, a `repo` and a `requestedBy`, and `Outcome` names a
`requestedBy` — and none of it is read: both nodes are written by the run's own
service account, so a title or a pull request from there would be a value the
run chose about itself (F9). The run id and the slug are the **path's** own and
not the node's contents, which is why they may come from it.

Where the index and the overlay both speak about one run, **the index wins
unless its own account is unsettled**: an entry whose status is `running` (or
which carries none) yields to an overlay that says the run ended, because the
fold has not read that change yet; an entry that carries a settled status is the
fold's own reading of the record the run wrote and is not overridden. An overlay
row the index does not carry is listed with its run id, its slug from the path
and its status, and with no title, no pull request, no identity and no cost:
there is nothing else to show, and inventing one would be worse than showing
less.

Two bounds the overlay inherits and must not break: the rollover prunes an
outcome day only after the fold has read it (`PruneAfter`, 8 days, RI4), and a
stamp older than that sets `complete: false` and restarts the walk (RI5).

## The writer lease and the reindex cursor (F39)

The index has one writer at a time: the sweep and a reindex must not fold the
same run twice, and a reindex that overlaps a sweep would double-count the
summary's deltas. The lease is `runIndex/lease` in the same RTDB, taken the way
every other lease in this tree is — a node holding a holder and the server's
time of the write, written with an ETag compare-and-swap (`rtdb.ServerNow`,
`PutIfMatch`), so two writers that both read a free lease cannot both take it.

| Field | Type | Meaning |
| --- | --- | --- |
| `holder` | string | who holds it: the execution's name, or the CLI's own identity for a reset |
| `at` | timestamp | the server time of the write, never a local clock's |
| `expires_at` | timestamp | `at` plus **60 minutes**: twice the longest execution that may hold it, the rollover's own `1800s` (`deploy/terraform/gcp/modules/installation/history.tf`, whose Scheduler call overrides the job's `600s`), so a dead execution's lease lapses (RI5) |
| `cursor` | string, absent when no walk is unfinished | the reindex walk's position, `runindex.Cursor.Encode` |

A reindex at 100x is about a million reads, far past the job's 600 s timeout
(F39), so the walk is resumable rather than long: the cursor is written beside
the lease, and the walk continues from it. Every sweep that finds an unfinished
cursor walks it further within its own timeout, so the walk advances with no
execution started by the job and no timeout override — the job's role cannot
start one (F9). A sweep that finds a **fresh** lease does not touch the cursor:
another writer is walking. While a reindex holds the lease a regular sweep still
does its crash sweep (D12) and skips only the fold. The reindex sets
`folded_through` to the walk's **start**, so the next fold picks up every run
that changed during the walk, and `complete` stays false until a walk reaches
the end (D3215).

`fugaro update` starts the first reindex after its Terraform apply, and `fugaro
runs reindex` starts one again; both reset the cursor first, and neither waits
on a run in flight (RI5, U3).

## The `--json` additions

Three documents gain fields, and every one of them is additive: a reader that
does not know a field loses nothing, and no existing field changes meaning
(CLAUDE.md's rule, and the plan's).

**`runs ls --json`** (`internal/cli/runs.go`'s `lsDoc`, contract §5) gains:

```json
{
  "index_complete": false,
  "folded_through": "2026-11-22T04:15:00Z"
}
```

- `index_complete` — boolean, always present: whether the index has ever
  finished a walk. While it is false the text listing prints one line saying the
  index is still building, and no reader prints a short list as the whole truth.
- `folded_through` — RFC 3339 timestamp, present only when the index has folded:
  how fresh the indexed rows are. Absent before the first fold, never a zero
  timestamp.

**`plan status --json`** (`internal/cli/plan_status.go`'s `planStatusDoc`,
contract §6.12) gains the same two fields, and, under `--no-merges` alone:

```json
{
  "index_complete": true,
  "folded_through": "2026-11-22T04:15:00Z",
  "merges_read": false
}
```

- `merges_read` — boolean `false`, present **only** under `--no-merges`
  (D3217 as amended): the read made no `gh` call, so every task's merge state is
  unknown and `closed` and `mark` carry nothing. The default read keeps its
  merge lookups — at most `planMaxLookups` (32), which is bounded and not
  history — so `closed`, `mark` and `--all` keep the meaning every console
  already installed draws them with (F37). `--no-merges` is the new console's
  first paint: the canvas before merge state, with its own section for the
  decorated read that follows (IR4).

**`runs stats --json`** (contract §6.10) gains `index_complete` and nothing
else (IR6): its rows come from index entries in its window.

I-INDEX copies these three additions into `console/docs/json-contract.md`; the
shapes above are what it copies, and a change to them changes that document in
the same release.

## What the index does not carry

Some fields of a `runs ls --json` row are facts of the read rather than of the
run, and no index can hold them:

| Row field | Where it comes from |
| --- | --- |
| `created` | the run id's own mint time (`runstore.RunTime`), so the reader computes it |
| `terminal`, `settled` | the entry's status, and the overlay's liveness |
| `pr_state` | the read's own window: a pull request's state is a fact about all of its runs, and `internal/cli`'s `markPRStates` computes it over the rows a read holds |
| `image_age_s` | the record's `started_at` minus its `image.built_at`, both in the entry |
| `derived` | the *backend's* account of a run the record never settled (a reclaimed Spot VM). The index cannot hold it — the fold reads the run's objects, not the execution — so the readers that print it today must keep their source or say it is gone. This is IR1's decision, and it is the one row field the index cannot answer. |
| `problem` | why a read could not follow the run at all (an unreadable object). A read that got its row from the index has no such problem to report. |

`Entry` is the run's own records and the keys derived from them. Nothing in it
is a decision: the launcher's `plan_task`, the task's `title` and the two
identity fields are display text a run's own service account can write, so a
reader shape-checks and sanitises them exactly as `runview` does today
(`task.PlanTaskProblem`, `safetext.Field`), and nothing authorises or admits
anything by them.

## Verified against the code

The claims this file rests on, checked against `main` at `b3a20c43a` when it was
written:

- F39's "far past the history job's 600 s timeout" is right: the job's execution
  timeout is `600s` (`deploy/terraform/gcp/modules/installation/history.tf`), and
  the CLI's own sweep deadline is tighter still (`historyTimeout`, 8 minutes,
  `internal/cli/history.go`). The lease's 60 minutes is twice the longest
  execution that may hold it, the rollover's `1800s`, as RI5 says.
- The overlay's two nodes are `agents/<slug>/<run>` and
  `outcomes/<day>/<slug>/<run>` (`internal/budget/model.go`), and `AgentEntry`
  and `Outcome` carry the identity and title fields the overlay must not read.
- `runstore.Statuses` is the seven-value vocabulary the summary's `states` keys
  are, and `PruneAfter` is 8 days (`internal/budget/rollover.go`).
- `planMaxLookups` is 32 (`internal/cli/plan_status.go`), `userStatsMaxRuns` is
  5,000 (`internal/cli/runs_stats.go`) and `RUNS_LIST_MAX_LIMIT` is 200
  (`console/src/main/ipc.ts`) — the figures the index makes unnecessary.
- `estimate.Build` reads a record's `status`, `started_at`, `finished_at`,
  `tier.tier`, `rigor` (through `LevelOf`) and `cost.total_usd`, which is why
  the entry projects exactly those beside the record.
- `runs ls` sets `Filter.Slugs` in every listing — the checkout's repositories,
  `--repo`, or, under `--all`, every slug of the installation
  (`lsSlugs`, `internal/cli/runs.go`) — and refuses `--pr` with anything but
  exactly one repository, which is why `--pr`, `--mine` and `--batch` each need
  the composite index that names `slug` beside their own field.
