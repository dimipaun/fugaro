# The page

**Status: design approved 2026-10-31, every decision answered as recommended; the CLI tasks C0-C4 queue for 0.15.0 and the console tasks K1-K5 for 0.117.0; the INPUT BOX of the terminal waits for the coordinator front door (P7/P8) and its own independent security review; the read-only transcript pane ships first (D827).**

The Coordinator tab answers one question first — **what needs me now** — and then, below it, four
quieter ones: what the fleet looks like, how far each plan has come, what the jobs did, and what a
particular pull request says. Everything on it is a read. The page has exactly one write
affordance, and it is the launch the console already has (design §5.1 rule 11, V2.5.1): a queue
item may open the launch confirmation with its subject filled in, and nothing on this page starts
a run on its own.

Two rules the concurrent console run is writing into `.claude/rules/console.md` and
`docs/design/desktop-console.md` shape the whole drawing, and this page is the first that has to
obey both at once:

- **The progressive-rendering rule.** Each section renders when *its own* read answers. The reads
  are started together and nothing waits for all of them: a slow plan read (the checkout walk) does
  not hold the action queue, and the job list never holds anything.
- **The jobs rule.** Jobs are never in the first paint, every job read is bounded by a `--since`,
  the read is asynchronous, and what is on screen is cached and stamped **"as of HH:MM"**.

## 1. The layout

One mocked page, every section, a pull request row expanded, and the transcript pane. The mock is
the whole page as it would look on a 120-column window, top to bottom, with the operator's own
actions in `[ brackets ]`.

```
+-------------------------------------------------------------------------------------------+
| fugaro console          installation: aurora        coordinator: on   read 11:20 (cached) |
+-------------------------------------------------------------------------------------------+
|  Live  |  Graph  |  Spend  |  Projects  |  Coordinator                                      |
+-------------------------------------------------------------------------------------------+
|                                                                                           |
|  NEEDS YOU NOW                                                          as of 11:20       |
|  +-------------------------------------------------------------------------------------+  |
|  | PR    #1458  owner/repo-a  "docs: the console auto-update plan as shipped"          |  |
|  |              open, not a draft, every run behind it finished 41m ago                |  |
|  |                                                            [ Open PR ]              |  |
|  +-------------------------------------------------------------------------------------+  |
|  | PR    #1460  owner/repo-b  "SECURITY: the update-converge refusal"                  |  |
|  |              open, not a draft, every run behind it finished 2h ago                 |  |
|  |                                                            [ Open PR ]              |  |
|  +-------------------------------------------------------------------------------------+  |
|  | JOB   run 20261031-101502-9f3c  owner/repo-a  #1456                                 |  |
|  |              halted: waiting for a person (stage checkout, 10:15)                   |  |
|  |                                                            [ Open run ]             |  |
|  +-------------------------------------------------------------------------------------+  |
|  | JOB   run 20261030-220114-77bd  owner/repo-b  no PR                                 |  |
|  |              ended failed: infra_error                                              |  |
|  |                                                            [ Open run ]  [ Retry ]  |  |
|  +-------------------------------------------------------------------------------------+  |
|  | PLAN  console-coordinator-v2 / C2  "the action queue"                               |  |
|  |              ready: no run joined, every dependency done                            |  |
|  |                                                            [ Launch ]               |  |
|  +-------------------------------------------------------------------------------------+  |
|  | BUDGET  owner/repo-c reached its daily cap: 120.00 / 120.00 USD                     |  |
|  |              the fleet is not killed; this repository is refused new runs           |  |
|  |                                                            [ Open Spend ]           |  |
|  +-------------------------------------------------------------------------------------+  |
|                                                                                           |
|  4 items need you   1 blind spot: owner/repo-b state unread   [ Re-check ]                 |
|                                                                                           |
+-------------------------------------------------------------------------------------------+
|                                                                                           |
|  FLEET                                                        counted 412.60 / cap 600.00  |
|  4 repositories   7 records in flight   2 file collisions   1 stale   1 unread PR          |
|                                                                                           |
+-------------------------------------------------------------------------------------------+
|                                                                                           |
|  PLANS                                            checkout: /home/person-1/src/aurora      |
|  console-coordinator-v2    12 tasks    3 open    9 closed   [#########.......]  75%        |
|     next ready: C2, C3                                                                     |
|  fleet-quotas               5 tasks    5 open    0 closed   [..............]   0%        |
|     next ready: Q1                                                                         |
|                                                                                           |
+-------------------------------------------------------------------------------------------+
|                                                                                           |
|  JOBS    last 24 h   41 runs   38 succeeded   2 failed   1 running        as of 11:20      |
|  [ Load jobs ]        (nothing below this line is read until you press it)                 |
|                                                                                           |
|  - - - - - - - - - - - after [ Load jobs ] - - - - - - - - - - - - - - - - - - - - - - -    |
|  |  >  20261031-114805-c1a2  owner/repo-a  #1458  succeeded   4m12s   1.83 USD       |     |
|  |  v  20261031-101502-9f3c  owner/repo-a  #1456  failed      halted   1.02 USD       |     |
|  |        stage checkout  |  12 files changed  |  started 10:15  |  ended 10:41        |     |
|  |        [ Open run ]  [ Open PR ]  [ Copy run id ]                                  |     |
|  |  >  20261030-220114-77bd  owner/repo-b  no PR  failed      infra_error   0.94 USD  |     |
|  |  >  20261030-184402-31de  owner/repo-a  #1453  succeeded   6m02s   2.41 USD        |     |
|                                                                                           |
|                                              [ Older 50 ]    41 of 41 shown                |
|                                                                                           |
+-------------------------------------------------------------------------------------------+
|                                                                                           |
|  PR  #1458   owner/repo-a                                    [ Open in browser ]          |
|  docs: the console auto-update plan as shipped in console 0.116.0                          |
|  ------------------------------------------------------------------------------------      |
|  description (plain text, first 20 lines)                            [ Show all ]          |
|  ## What                                                                                   |
|  The console checks the release feed at launch and every six hours, and a download          |
|  is quiet -- the console keeps working while it happens.                                    |
|  ## Why                                                                                     |
|  An operator who is never told a version is on disk is an operator running the old one.     |
|  ...                                                                                        |
|                                                                                           |
+-------------------------------------------------------------------------------------------+
|                                                                                           |
|  COORDINATOR                                            transcript - read only             |
|                                                                                           |
|  you   > what is in flight on owner/repo-a?                                                 |
|  agent   7 records, 2 in flight: #1458 and #1460. #1458 touches cmd/fugaro and             |
|          docs/plans/. #1460 touches deploy/terraform. They collide on nothing.             |
|  you   > which repositories has the sweep not reconciled?                                    |
|  agent   owner/repo-b: stale since 2026-10-31T09:02:11Z, the host refused the read.        |
|                                                                                           |
|  >  the coordinator's front door is not built yet: the input is disabled (D820).            |
|                                                                                           |
+-------------------------------------------------------------------------------------------+
```

## 2. What the page shows, and what it reads

Every read below is a **spawn of the CLI**, which is the console's only seam to anything (design
§3.1). The console holds no credential of its own and stores none. The sizes and times are
readings, not estimates: where this design does not have one, the cell says **`(unmeasured)`** and
the profiling task **C0** in [implementation-plan.md](implementation-plan.md) fills it. A figure in
this table that P4 falsifies is corrected here rather than argued with.

| Figure | Value | Where it came from |
| --- | --- | --- |
| `fugaro report --json` | about **13 s** | measured for the Spend screen's read |
| `fugaro runs ls --json --since 24h` | about **5.8 s** | measured for the 24-hour window |
| `fugaro coordinator status --from-store --all --json` | about **724 KB** | measured for a 4-repository installation |

### 2.1 The header

**Installation**, from `fugaro config list --json` (`installation.list`) — the picker's own read,
which already carries the project name, the GCP project, the region and the remembered checkouts.
**Operational lifetime**: whether the coordinator is switched on and when a sweep last wrote, from
the same `coordinator.status` document the page already reads (`repos[].stored` and
`repos[].stale`). The console cannot tell a coordinator that is running from one switched off after
its last sweep, and the label says **"read HH:MM"** rather than "current" for that reason — the
sentence the existing screen already carries (`COORDINATOR_NO_AGE_WORD`).

### 2.2 The action queue

Four kinds of item, each with **one** button. The queue is the first thing that paints, and it is
built from four reads that are started together and render independently, in the order they answer.
An item whose read has not answered yet shows its skeleton and no count.

| Item | The read behind it | Flags | Size | Time |
| --- | --- | --- | --- | --- |
| **A pull request waiting for review or merge** | `fugaro coordinator status --from-store --all --json` (`coordinator.status`, `ee/coordinator/cli/coordinator_view.go` `runCoordinatorStatusFromStore`) | `--from-store --all --json`, fixed words of the row | ~724 KB | `(unmeasured)` |
| **A halted or failed job** | `fugaro runs ls --json --since 24h --limit 20` (`runs.list` + the two new flags of D818) | `--since 24h --limit 20` | `(unmeasured)` | about 5.8 s at 24 h, plus the parse |
| **A plan task ready to launch** | `fugaro plan status --json --all` (`plan.status`, `internal/cli/plan_status.go`) | `--all`, from the row's own declared argument | `(unmeasured)` | `(unmeasured)` — it walks the checkout and spawns one `gh` per joined pull request, up to 32 |
| **A budget alert** | `fugaro budget show --json` (`budget.show`) | `--repo` per repository, `--all` for the fleet | `(unmeasured)` | `(unmeasured)` |

**Which pull requests are "waiting for you" is computed, never looked up.** A record is in the queue
when all three hold, and each is a field the stored view already carries:

- `pr.state == "open"` and `pr.draft == false` — an open, non-draft pull request (`CoordinatorPullRequest`
  in `console/src/shared/schema.ts`; the CLI writes both from the host's own answer, `PullRequest` in
  `ee/coordinator/view.go`);
- `in_flight == false` on the record — no run behind it is still going;
- the record has at least one run — a pull request the store holds no run for is a pull request
  somebody opened by hand, and the queue says so separately rather than offering it a button.

**Two things the queue deliberately does not say.** It does not claim the pull request is *ready to
merge*: whether a review passed, whether CI is green and whether the base moved are not facts any
document the console reads carries, and a queue that coloured a row "merge now" would be inventing
them. It does not say how long the pull request has waited in wall-clock terms beyond the newest
run's finish, because the stored view carries no time of its own (the sweep's document is a function
of its inputs — `ee/coordinator/sweep.go`), and an age derived from when the console read it would
be an age of the wrong thing.

**The buttons, and what each one is:**

| Button | What it does | Write? |
| --- | --- | --- |
| **Open PR** | Opens the pull request in the operator's browser, through the console's one external-link path. The URL is built, never read (reconciliation D815) | no |
| **Open run** | Moves to the Run screen for that run id; the Run screen's own reads (`run.diagnose`, `run.logs`) do the rest | no |
| **Retry** | Opens the launch confirmation (rule 11's window) with the failed run's repository and plan task filled in and the run id freshly minted by the main process. It is a **new launch through the approved argv**, not a `run --retry` | the launch, which is already the console's one write |
| **Launch** | The same confirmation, with `--plan-task=<id>` filled in from the queue item | the launch |
| **Open Spend** | Moves to the Spend screen. The queue never calls `budget kill` or `budget resume`: those are the CLI's, behind a typed confirmation, on the never-reachable list | no |

### 2.3 The blind-spot line

Under the queue, one line, drawn from the same `coordinator.status` document: how many repositories
the sweep has marked stale, and how many pull requests a sweep could not read (`unread` in
`CoordinatorView`). It has no button, because no button on this page can fix either: the sweep is a
scheduled Cloud Run job (`deploy/terraform/gcp/modules/installation/coordinator.tf`), and the
console may not start it. What it has instead is the Re-check control, which re-reads the document.
The alternative — a queue item with a disabled button — is refused in reconciliation D813.

### 2.4 Fleet totals

Four numbers and one sum, and every one of them is a **count off a document**, never a sum over a
page of rows:

| Figure | Source |
| --- | --- |
| repositories | `repos[].repo`'s length in the `coordinator.status` document |
| records in flight | `in_flight` over every `records[]` of every repository |
| file or service collisions | the documents' own `collisions[]` lengths, added across repositories — and shown as a fleet figure only with the sentence that a collision is a *pair* in one repository, never a count of affected work |
| stale repositories, unread pull requests | §2.3 |
| counted dollars and the cap | `fugaro budget show --json`'s `global` block — the same read the rail already makes for its one figure |

**`fugaro report --json` is not on this page.** It is the money read that breaks spend down by
repository, person and model, and it takes about **13 s**: too slow for a first paint, and the two
figures the totals need are in `budget show`. The Spend screen keeps it.

### 2.5 Per-plan progress

One row per plan in the installation's checkout, from `fugaro plan status --json --all`:
`plans[].tasks`, `plans[].closed`, `plans[].open` and, under them, the tasks whose
`nodes[].state == "ready"` — the six states are `internal/plan/state.go`'s, and `ready` is the
CLI's own answer to "no run joined and every dependency done", which is exactly the queue's Launch
subject. The bar is drawn from the CLI's two figures rather than recomputed, so a screen and the
CLI cannot disagree about a plan's progress.

The section **needs the installation's recorded checkout**: `plan status` walks `docs/` under the
spawn's own working directory, which is the checkout the picker remembers
(`configListCheckout`, `internal/cli/config_list.go`). An installation with no checkout gets no
plan rows and the console's own sentence about why, never an empty bar.

### 2.6 The job list: expandable, pageable, on demand

Nothing here is read until the operator presses **Load jobs**. That is the jobs rule, and it is also
the honest thing: the read is about 5.8 s at 24 hours, and a first paint that spent it would be a
page that does nothing until it is done.

**The read, and the control behind it:**

| Control | Exact argv the console spawns |
| --- | --- |
| first page | `fugaro runs ls --json --since 24h --limit 50` |
| older page | `fugaro runs ls --json --since 24h --limit 50 --after 20261030-184402-31de` |
| window control | the same argv with `--since 6h`, `--since 7d` or `--repo owner/repo-a` |

`--limit` and `--after` do not exist yet (`newLsCmd`, `internal/cli/runs.go`). Both are new argv and
therefore an allow-list change with its own review (`.claude/rules/console.md`); D818 recommends
them over client-side paging and gives the cost of the alternative. `--after` names the **last row
the operator was shown** and returns the rows after it in the listing's own order — a cursor rather
than an offset, because runs arrive at the head while a person pages down, and an offset would
show a run twice or skip one.

**A page is not a total.** The `runs` count, the succeeded/failed/running split and the header line
stay those of the **whole window**, not of the rows on screen: `--limit` bounds what the CLI prints,
never what it counts. Whether the CLI can hold that promise today is F5 in
[reconciliation.md](reconciliation.md) and the red step of task C2.

**Expansion** is the run row opening in place: the stage, the changed-file count, the start and end
stamps, the cost, and the halt reason when there is one — every one of them a field `runview.Row`
already carries (`internal/runview/runview.go`). **Paging** is the Older 50 control, which reads
the next page and appends. **Caching** is the "as of HH:MM" stamp on the section header: a page
loaded at 11:20 says 11:20 until it is reloaded or the operator changes a control, and the section
never re-reads on its own while the operator is reading it.

### 2.7 A pull request, expanded

A row of the action queue or a `#NNNN` in the job list expands into this block. Two reads, and the
second only when the operator asks for it:

| What | Source | When |
| --- | --- | --- |
| the number, the title, the branch, the draft flag, the state, the runs behind it | the `coordinator.status` document already on screen — no new read | immediately, on expand |
| the **description** | `fugaro coordinator show --repo owner/repo-a --pr 1458 --json`, a **new** allow-listed read (D816) | on **Show all**, or on expand if the operator's setting asks for it |

**Why the description needs a read of its own.** The stored view carries no body: the coordinator's
`PullRequest` (`ee/coordinator/view.go`) holds `Number`, `URL`, `Title`, `Branch`, `Draft` and
`State`, and the sweep drops the body its provider reader already has (`gitprov.PRInfo.Body`). The
three ways to put a body on this page — store it in the sweep's document, read it on demand, or show
the title alone — are D816, with the recommendation being the on-demand read, because the body is
unbounded text written by whoever opened the pull request and storing one per record would put that
text inside the document every reader of the view fetches.

**How it is drawn.** As **text**: through the console's own sanitizer
(`sanitizeText`, `console/src/shared/sanitize.ts`) with the `sentence` class, into a text node, in a
clipped block of the first 20 lines with a Show all control. **Not** as markdown: the console has no
markdown renderer, no markdown dependency, and adding one would be a new dependency plus a parser
over untrusted text plus the link question that rule 2 answers with "no". The description's own
URLs are drawn as text like everything else in it, and no element is built from them.

**What it says when it cannot read.** The read asks the host through the coordinator's own
read-only installation token, which the CLI mints from the installation's GitHub App with the
operator's own credentials — the console still stores nothing. On an installation configured with
the credentials service (`FUGARO_CREDENTIALS_SERVICE_URL`) that path refuses outright
(`coordinatorReader`, `ee/coordinator/cli/coordinator.go`), which is the documented behaviour of the
stored-view read's sibling. The block then draws the CLI's own sentence and offers the Open PR
button instead of an expand that would fail.

### 2.8 The transcript pane, and the terminal

The bottom pane has two states and one design, and the second state is not built:

- **read-only transcript (shippable first, D827).** The pane draws what the fleet's orchestrators
  have declared — the coordinator's intent records — and the stored view's answers to a small set of
  fixed questions the console composes itself. No input control, no service, no model. It answers
  "who is touching what", which is the half of the owner's request that needs nothing new on the
  cloud side.
- **terminal (design only, D820–D826).** A line the operator types travels to the coordinator's MCP
  front door (`docs/coordinator/implementation-plan.md` P8, D197) through an allow-listed `fugaro`
  command, never a shell and never a pty. The full design, its authentication, its audit log, its
  rate limits and its security model are [reconciliation.md](reconciliation.md) §5.

The pane's answers are drawn as **plain text, never markup and never a link the operator did not
ask for** — that is P8's own rule (its finding #28), and it is also rule 2 and rule 7 of the console
design.

## 3. What the page refuses to claim

- **No freshness for the stored view.** Its document carries no time by design. The pane is stamped
  with when the *console read it*, and says so.
- **No "ready to merge".** Not a fact any document here holds (§2.2).
- **No fleet-wide collision count as if it were a work item.** A collision is a pair in one
  repository.
- **No spend figure from a page of rows.** Every money number comes from the budget read.
- **No button that writes anything the console was not already allowed to write.** The one write is
  the launch, and the queue's Retry is that launch.
