# Reconciliation

**Where this disagrees with [spec.md](spec.md), this document wins.** Every finding below was read
off `origin/main` at `6edb2b24` — and, for the allow-list, off the 18 pull requests open on
2026-10-31 — with `grep`, offset reads and `git show`. A finding names the file and the symbol it
was read from. Numbers are cited by symbol rather than by long line ranges, because a line range in
a dated document is a range that rots.

## 1. What the coordinator's store carries today

**F1. The stored view does not say which provider a repository is on.** `coordinator.View.Repo`
(`ee/coordinator/view.go`) is `"owner/name"`, and `coordinator.RepoView.Repo` (`ee/coordinator/state.go`)
is documented as "the repository the caller asked about, `owner/name`, as the caller spelled it" —
the node's key is a **slug**, and the slug is a one-way hash of the provider and the name
(`internal/task`'s `Slug`, the fact design §5.1 rule 2 already records). A reader holding
`owner/repo-a` and `#1458` therefore cannot tell a GitHub pull request from a Bitbucket one, and
**`github.com/owner/repo-a/pull/1458` and `bitbucket.org/owner/repo-a/pull-requests/1458`** are the
two different links a clickable number has to choose between. Nothing else the console reads carries
it either: not `runview.Row` (`internal/runview/runview.go`), not `JSONRun` (`internal/watch/json.go`),
not `plan.StateNode` (`internal/plan/state.go`). The one document that does carry a provider is
`fugaro config list --json`, and only per **checkout** — `configListCheckout.Repository` is
`"<provider>/<owner>/<name>"` (`internal/cli/config_list.go`) — so it answers for the repositories
this machine has a checkout of and for no others.

**F2. The pull request's `url` is carried and deliberately not drawn.** `coordinator.PullRequest.URL`
is in the document, and the console refuses it on purpose: `console/src/renderer/screens/coordinator.ts`
says the `url` "is **not drawn at all**", and design §5.1 rule 2 makes that a rule rather than a
choice — `pr_url` arrives from a container the console does not own, so "the host is on the
allow-list" is not the same statement as "this is this pull request". The console's own decoder
carries the field (`COORDINATOR_PR_KEYS` in `console/src/shared/schema.ts`) and no renderer reads
it. **A clickable number must be built, and building it needs a provider F1 says the document does
not have.**

**F3. The stored view carries no pull request body.** `coordinator.PullRequest` holds `Number`,
`URL`, `Title`, `Branch`, `Draft`, `State` — and nothing else. The provider layer has the body
(`gitprov.PRInfo.Body`, `internal/gitprov/gitprov.go`, "GitHub's body, Bitbucket's description;
untrusted text"), and the sweep's reader drops it on the way into the view. So the owner's "see
their description inline" has **no source today**: it needs either a new field in the stored
document or a new read.

**F4. The stored view carries no halt.** `coordinator.Run` holds `ID`, `PR`, `Status`, `Outcome`,
`Terminal`, `Branch`, `BaseBranch`, `DeclaredBy`, `Files`, `FilesTruncated`. The thing a person most
needs to see — a run **halted** and waiting for them — is `runview.Row.Halt` (`*runstore.Halt`) and
appears in no coordinator document. The view can say a run's status and outcome and whether it is
terminal; it cannot say "this one is waiting for you".

**F5. `runs ls` reads the whole window and prints all of it.** `newLsCmd` (`internal/cli/runs.go`)
declares `--repo`, `--all`, `--mine`, `--since`, `--batch`, `--pr`, `--watch`, `--interval`, `--json`
and the cloud group's flags — **no `--limit`, no offset, no cursor**. `lsDoc` prints every row it
read, and `runview.Totals` is computed over those rows. About **5.8 s** and, for a 24-hour window on
a busy installation, a document of `(unmeasured)` size. There is consequently no way for the console
to ask for the newest 50 runs, and no way to ask for the next 50.

**F6. The coordinator document is one 724 KB reply.** `fugaro coordinator status --from-store --all
--json` returns every repository the local config lists, each with its records, collisions and
unread list, in one document — about **724 KB** measured. The console decodes it strictly
(`decodeCoordinatorStatus`, `console/src/shared/schema.ts`) and holds it whole. That is fine as the
action queue's source and wrong as a paging source: there is nothing to page, and no cursor to page
with.

**F7. Everything the page needs that the view does not carry is a *read*, never a guess.** The
consistency rule this document keeps: no figure on the page may be derived from a document that does
not hold it. "Ready to merge" is not derivable (no review state, no CI state, no base-moved fact in
any document); an age for the stored view is not derivable (the document is a function of its inputs
and carries no clock — `ee/coordinator/sweep.go`); a fleet-wide collision count as a work item is
not derivable (a collision is a pair in one repository, `coordinator.Collision`).

## 2. What the CLI can and cannot read

**F8. The CLI has no per-pull-request read.** `NewCoordinatorCmd` (`ee/coordinator/cli/coordinator.go`)
adds exactly three subcommands — `status`, `report`, `sweep`. There is no command that prints a
single pull request as the host has it (title, body, state, checks), and no `fugaro pr …` family
anywhere in `internal/cli`. F3's body has no transport.

**F9. The read that would fetch one holds a credential the console does not have — and that is the
point.** `runCoordinatorStatusFromStore` (`ee/coordinator/cli/coordinator_view.go`) is the one
coordinator read that opens no host and holds no App ID, no App key and no Bitbucket credential: it
reads the database through the caller's own ADC (`roles/firebasedatabase.viewer`). Its sibling
`coordinatorReader` mints the coordinator's own **read-only** installation token per repository
(`github.CoordinatorTokenPermissions`) from the GitHub App key in Secret Manager, read with the
operator's own credentials, and it **refuses outright under `FUGARO_CREDENTIALS_SERVICE_URL`**. A
new host read therefore inherits both properties: the console still stores no credential, and the
read is unavailable on an installation configured with the credentials service.

**F10. `plan status` is the only source of "ready", and it needs a checkout.** The six task states
are `internal/plan/state.go`'s, and `StateReady` is documented as "a task with no run joined at all
and every dependency done" — exactly the action queue's Launch subject. The read walks `docs/` under
the **spawn's own working directory**, which is the installation's recorded checkout (`plan.status`
in `console/src/main/spawn.ts`; `createBoundSource`), so an installation with no checkout has no
per-plan progress at all.

**F11. `runs stats` is not a fleet summary.** It is the closest thing to one, and it is scoped to
the caller: `docs/design/desktop-console.md` §2.1 records that "an operator of the installation sees
every user, a plain launcher sees their own row alone". A launcher reading their own row would draw
a fleet total that is one person's. The fleet's run figures therefore come from the coordinator
document's records, or from a `runs ls` the operator asked for — never from `runs stats`.

**F12. Nobody may start the sweep from here.** The coordinator is a scheduled Cloud Run job
(`deploy/terraform/gcp/modules/installation/coordinator.tf`, opt-in through `fugaro update
--firebase-project --coordinator`). No CLI command lets a client trigger a tick, and `coordinator
sweep` is the job's own entry point, run by Cloud Scheduler.

**F13. The money read is slow.** `fugaro report --json` is about **13 s**; `budget show --json` is
the rail's own read and carries `global.counted_usd`, the caps and every `kill` block. The first
paint's two money figures come from `budget show` and never from `report`.

## 3. What the allow-list permits

**F14. The console's reads are exact argv, and the coordinator's row is one fixed string.**
`SPAWN_COMMANDS` (`console/src/main/spawn.ts`) holds 16 rows, one per operation; the coordinator's is
`['coordinator', 'status', '--from-store', '--all', '--json']` with `args: []`, so **no renderer
value reaches it** — `--from-store` and `--all` are words of the row and not arguments.
`CONSOLE_OPERATIONS` (`console/src/main/ipc.ts`) lists the matching operation, and
`assertSpawnSurface` fails a table where either side is missing an entry. `.claude/rules/console.md`
makes every new command, flag or argument security-sensitive: an independent review, a pinning test
in `console/test/unit/`, and the §5.1 table in `docs/design/desktop-console.md` updated in the same
change.

**F15. The console's one write is rule 11's launch, word for word.** The `run.launch` row is `run
--task-file - --json --run-id <id> --repo <owner/name> [--title=<T>] [--plan-task=<P>]`, with the run
id minted by the main process (`runIdFlag`) and the task arriving on stdin. `--retry` is on the
row's own exclusion list and on a never-reachable row of its own: **the console cannot spawn `run
--retry`**, and "retry" on this page has to mean a new launch or nothing.

**F16. The budget writers stay out.** `budget set`'s raise, `budget kill` and `budget resume` are
the CLI's typed-confirmation writes and sit on the never-reachable table. A budget alert therefore
has no fixing button; the queue's one button is a navigation.

**F17. The console's writes are three files and the spawn log, and nothing else.** Rule 10: the app
writes the spawn log when asked, the installations file and `updates.json`. The spawn log
(`console/src/main/spawnlog.ts`) records exactly four fields — `ts`, `argv`, `cwd`, `project` — is
opt-in, and is the only audit-shaped file the console has. §5's audit design is built on it rather
than on a new file, because a new file is a change to rule 10.

**F18. The console renders untrusted text through a closed set of classes, and has no markdown.**
`TEXT_CLASSES` (`console/src/shared/sanitize.ts`) is a closed set, `WIRE_TEXT_CLASSES`
(`console/src/shared/schema.ts`) maps every wire key to one of them or refuses the guess, and rule 7
is "job-written text is text". There is no markdown renderer and no markdown dependency anywhere in
`console/`.

## 4. The page's decisions (D810–D819)

Each decision states the recommendation, the answer applied in [spec.md](spec.md), and what the
alternative costs. **The owner answered all of them on 2026-10-31, every one as recommended.**

| # | Question | Recommendation | Cost of the alternative | Answer |
| --- | --- | --- | --- | --- |
| **D810** | Where does the action queue get "halted jobs"? | **A bounded `runs ls` read, filtered on the console side.** `fugaro runs ls --json --since 24h --limit 20`, started in parallel with the other queue reads, filtered to rows with a `halt`, or a terminal `status` that is not a success. It renders when it arrives (the progressive-rendering rule) and it is not the job list: the list is §2.6's on-demand, paged section | Add `halt` to `coordinator.Run` and have the sweep carry it. That is a wire-shape addition to a document the sweep writes (allowed — adding a field is fine), but it changes the **stored** document every reader decodes, needs the sweep's own red step, and only pays off after the next tick: a run that halts at 11:19 is invisible until the sweep runs. The bounded read shows it in 5.8 s. A third option — a `--halted` flag on `runs ls` — is a new argv for a filter the console can apply correctly today; take it later only if the window proves too wide (O1) | **Answered 2026-10-31: the recommendation.** |
| **D811** | What does **Retry** do? | **It opens the launch confirmation with the failed run's repository and plan task prefilled and a fresh run id.** It is rule 11's argv and nothing else, so D22's "one write" is untouched | A real `run --retry`: it would be a **second launch argv**, its own never-reachable surgery, its own §5.1 paragraph and its own independent review — for a control whose difference from a new launch is that it reuses a branch. Two ways to start a run, one of them reachable from a queue item, is a fence rule 6 draws and this page would move | **Answered 2026-10-31: the recommendation.** |
| **D812** | What does a **budget alert** offer? | **A navigation to Spend, and nothing else.** The alert is *drawn* when `budget show --json` reports a repository daily cap reached, or a `kill` block, and the queue states which of the two it is: a killed fleet refuses everything, a reached repository cap refuses that repository | Admitting `budget kill` / `budget resume` here. That removes the typed confirmation the CLI puts on them, puts a money write behind a button on a queue, and makes the console a write path in two places. It is the one alternative in this table that would be a security regression rather than a cost | **Answered 2026-10-31: the recommendation.** |
| **D813** | Are stale repositories and unread pull requests action-queue items? | **No — a blind-spot line under the queue, with no button.** No button on this page can fix either (F12). The line is drawn from the same document and says what the console cannot see | A queue item with a disabled button, or a button that asks the CLI to tick the sweep. The second needs a CLI command that starts a cloud job, which is a new write and a new argv; the first is a control that exists only to explain itself | **Answered 2026-10-31: the recommendation.** |
| **D814** | How does the console learn a repository's provider? | **Add `provider` to `coordinator.RepoView`** (`ee/coordinator/state.go`), written by `coordinatorStoredView` from the same `coordinator.Repo` it already builds the slug from. It is an **additive** field, so no format version moves; it needs `COORDINATOR_REPO_KEYS` (`console/src/shared/schema.ts`) and the Go test that pins those key lists against the structs updated in the same change. **No allow-list change**: the argv is unchanged and the console's row already carries it | Inferring the provider from `pr.url` — refused outright by rule 2 and F2: a URL field a job wrote is not a fact about the repository. Or joining `config list --json`'s checkouts — which answers only for repositories this machine has a checkout of (F1) and would silently draw no link for the rest | **Answered 2026-10-31: the recommendation.** |
| **D815** | How is the pull request URL produced? | **Built, in the main process, from `(provider, repo, number)`**, by a pure function beside `validateExternalUrl` (`console/src/main/window.ts`): `https://github.com/<owner>/<name>/pull/<n>` for GitHub, `https://bitbucket.org/<owner>/<name>/pull-requests/<n>` for Bitbucket. The owner and name must match a strict pattern, the number must be a positive integer, the provider must be one of the two known kinds, and the result is handed to **`openExternalValidated`** — the console's one call to `shell.openExternal`, whose host allow-list already carries both hosts. Anything else is refused and drawn as text | Passing the document's `url` through. That is exactly the case rule 2 was written about ("a host check is the second gate, never the first"), and the gate it lacks is the first one | **Answered 2026-10-31: the recommendation.** |
| **D816** | Where does the inline description come from? | **A new allow-listed read**, `fugaro coordinator show --repo <owner/name> --pr <number> --json` — the pull request as the host has it, read through the coordinator's own read-only installation token. The console stores no credential; the CLI mints it from the App key with the operator's own ADC, exactly as the project-wide read does (F9). Fetched on **Show all**, cached with the pane's "as of HH:MM", and unavailable under the credentials service, where the block draws the CLI's sentence instead | **(a) Store the body in the sweep's document.** Every record's body would ride in the 724 KB document every reader fetches, unbounded text written by whoever opened the pull request, growing a document whose whole point is that it is small enough to read in one call. **(b) No description at all** — the title alone, which is what the stored view already has: free, and the owner asked for the description. **(c)** A `--body` flag on a widened `coordinator status`: the same read would then hold a credential and lose F9's "the one read that needs nothing of the host" | **Answered 2026-10-31: the recommendation.** |
| **D817** | How is the description drawn? | **As plain text**, through `sanitizeText` with the `sentence` class, in a clipped block with Show all. Never markdown | A markdown renderer: a new dependency, a parser over untrusted text, a decision about links inside it (which rule 2 answers with "no"), and a second sanitizer to keep in step with the first. The whole benefit is headings and emphasis in a description nobody reads twice | **Answered 2026-10-31: the recommendation.** |
| **D818** | How is the job list paged? | **On the CLI side**, with two new flags on `runs ls`, both security-sensitive allow-list changes: `--limit N` and `--after <run-id>`, the latter a **cursor** naming the last row shown, returning the rows after it in the listing's own order. `totals` stays over the whole window: a page is not a total (F5). First page `fugaro runs ls --json --since 24h --limit 50`; next page the same argv plus `--after 20261030-184402-31de` | **Client-side paging**: the console would have to read and hold the whole window to page it — about 5.8 s and `(unmeasured)` bytes before the first row, on a page whose whole design is that jobs are read on demand. It also has no cursor, so it would re-read everything for each page. An **offset** (`--offset N`) is cheaper to build and wrong under a head that grows: a run that arrives between page one and page two shifts every later row, and the operator sees one twice or skips one | **Answered 2026-10-31: the recommendation.** |
| **D819** | What is the first paint's money and count source? | **`budget show --json`** for the dollars and the caps (the rail's own read, F13), the **coordinator document** for records, in-flight, collisions, stale and unread, and **`plan status`** for the plans. **`report` is not on this page**: it is 13 s | Reading `report` for the fleet's per-repository money. Thirteen seconds of a first paint for figures the budget read already carries — and the Spend screen, where that breakdown belongs, keeps it | **Answered 2026-10-31: the recommendation.** |

## 5. The terminal: the design, and its decisions (D820–D828)

**This section is the design the owner asked for. It builds nothing, and no part of it may be built
before an independent review of the whole of it.**

### 5.1 What "the agent" is

There are two candidates, and they are not the same thing:

- **The coordinator's front door** — the service of `docs/coordinator/implementation-plan.md` **P7**
  (D196: the coordinator's own `google_cloud_run_v2_service`, its own service account,
  scale-to-zero, per-person Cloud Run IAM invoker) and the MCP front door of **P8** (D197:
  `report_intent`, `check_conflicts`, `get_project_state` / `get_release_status`, `record_override`).
  **Neither is built.** `deploy/terraform/gcp/modules/coordinator/` does not exist, and
  `NewCoordinatorCmd` (`ee/coordinator/cli/coordinator.go`) adds exactly `status`, `report` and
  `sweep`. The coordinator holds **no model credential** (Q4 A, recorded in P7): it is a
  deterministic service that answers from the view and the record.
- **A run's own agent** — `internal/agent` running `claude -p` inside the job container, behind
  `internal/gateway`, which holds the model credential and enforces the budget. Its stdin and
  stdout are inside a Cloud Run job execution. There is **no channel from a desktop app to it**,
  and building one would mean a pty into a container holding a model credential.

So the terminal's interlocutor is the coordinator's front door, and the terminal cannot exist before
P7 and P8 do. That is the first thing the owner has to decide, and it is D820.

### 5.2 How a typed line would reach it

**Through an allow-listed `fugaro` command, never a raw shell and never a pty.**

```
fugaro coordinator ask --json            (new; the line arrives on stdin)
```

- The console spawns the CLI — the only seam it has (`console/src/main/spawn.ts`), on an exact argv,
  with `shell: false` and an array argv. The typed line is **not** an argv word: it goes on the
  child's **stdin**, which is the same channel rule 11's launch uses for the task
  (`--task-file -`), and which keeps the operator's own words out of the process table and out of
  the spawn log's `argv`.
- The CLI mints the Google ID token for the coordinator's URL from the operator's own ADC (P7's
  credential half) and calls the service over HTTPS. `gcloud auth print-identity-token` is never
  typed by a person and never spawned by the console.
- **No pty is ever opened.** A pty to the user's machine would be a shell; a pty to the cloud would
  be an interactive session inside a container whose credential the console must not hold. The
  terminal is a **request and a reply**, drawn as a transcript.

### 5.3 Authentication, and who may use it

- **Cloud Run IAM invoker, one person per member**: no `allUsers`, no `allAuthenticatedUsers`, no
  `domain:` — the rule `internal/cli/init_review.go`'s `reviewScreen` already refuses in a plan and
  `doctor_launcher.go` reports. The token carries the service's audience and the service checks it.
- **On the console side the identity is the operator's own**, the one the rest of the app already
  uses: the installation the picker chose, and that machine's ADC. The console stores no
  credential and holds no key.
- **A person who is not an invoker gets a refusal**, and the pane draws the CLI's own sentence — the
  same shape every other refused read on the console has.

### 5.4 What it may do

| Class | Tools | Fence |
| --- | --- | --- |
| **Read** | `get_project_state`, `get_release_status`, `check_conflicts` | One read more; no new argv for the *service* (the CLI argv is new), no write anywhere |
| **Advisory write** | `report_intent` (P4's writer, the same one `coordinator report` and the launch's declarer use) | Writes the coordinator's subtree and nothing else; takes nothing back |
| **Recorded write** | `record_override` (the override log, P8: written, shown, never fed back into a prompt) | A write, shown |
| **Refused** | anything that launches a run, moves the budget, writes to the host, or reaches the credentials service | These are the launch (rule 11, its own screen and its own confirmation) and the budget writers (`budget kill`, `budget resume`, typed confirmation, never-reachable). **A terminal that could spend money is D22's "one write" undone**, and it is refused here rather than left to a prompt |

### 5.5 The audit log

Two records, and neither is a new file (F17):

- **The console's.** The existing opt-in spawn log records the spawn: `ts`, `argv`, `cwd`,
  `project`. Because the typed line travels on stdin, the log holds **that a call was made, from
  which installation, at what time** — and not the operator's words. That is deliberate: a log that
  captured the line would put free text the operator typed on their own disk unasked, which is a
  different act from logging an argv. The pane's own transcript is on screen and is not persisted by
  the console.
- **The service's.** The coordinator writes its own record server-side, in the same shape as the
  override log (D197), which is where a record of *what was asked and answered* belongs: the
  service is the thing with the data, the credential check and the retention rule.

### 5.6 Rate limits

Enforced on the **service**, not the client, because the client is the thing being limited: a
per-person call rate, a stricter one for the write tools (`report_intent`, `record_override`), and a
cap on answer size. The console adds a client-side in-flight cap of one call and a timeout, so a
slow answer cannot queue three behind it. The numbers are **`(unmeasured)`** and belong to a
profiling pass on the built service; proposing them now would be inventing a budget for a machine
that does not exist.

### 5.7 What the page shows

One transcript pane: each entry the operator's line (as they typed it), the service's answer, and
the time — the answer drawn as **plain text, never markup, and never a link the operator did not ask
for** (P8's own finding #28; console rules 2 and 7). The pane is stamped "as of HH:MM" like every
other cached read, and a refused call is drawn as the CLI's sentence.

### 5.8 The alternative: a read-only transcript pane first

**Ship the pane before the service exists, with no input control.** What it can show without any new
cloud side:

- the **stored view** the page already reads — what is in flight, what collides, what is stale;
- the **declared intents**: `coordinator/repos/<slug>/intents`, written by `coordinator report`
  (`ee/coordinator/intent.go`'s `Intent`, keyed by a deterministic `IntentID`) and by every launch's
  own declarer (`ee/coordinator/cli/declare.go`). **The console cannot read these today** — no row
  in `SPAWN_COMMANDS` and no CLI command prints them; `coordinator status --from-store` reads the
  view and the stale marker and nothing else.

So the read-only pane needs **one small new read** — `fugaro coordinator intents --all --json`,
allow-listed — and nothing else: no service, no model, no ID token, no write. Its **cost** is that
it answers "who is touching what" and not "what should I do": it is not a conversation, and it does
not answer the owner's sentence. Its **benefit** is that it needs no fence moved at all, and it is
useful on every installation whose coordinator is switched on — including every installation where
the front door will never be built.

### 5.9 The security model

**The terminal is a second write path after the launch, and it moves the fence rule 6 draws twice**
— once for the new argv the console would spawn, and once for the write tools the service would
expose. What follows from that:

- **An independent review of the whole terminal is mandatory** before any of it is built: the argv,
  the stdin channel, the token mint, the invoker members, the tool set, the audit records, the rate
  limits, and the refusal path. This is the same requirement `.claude/rules/console.md` puts on any
  new argv, with a wider subject.
- **Every `coordinator` argv the console may not spawn gets a row** on the never-reachable table, in
  the same change that admits the one it may — rule 6's two halves, asserted by
  `assertReachableSurface` and `console/test/unit/ipc.test.ts`.
- **The tool set is the fence.** A tool the service does not expose cannot be reached by a clever
  line, and the refusals of §5.4 are a property of the tool set rather than of a prompt.
- **The page never becomes a credential holder**: no token, no key, no session in the renderer or in
  `userData`. The identity is the operator's own ADC, used by the CLI.

### 5.10 The terminal's decisions

| # | Question | Recommendation | Cost of the alternative | Answer |
| --- | --- | --- | --- | --- |
| **D820** | Which agent does the terminal talk to? | **The coordinator's front door (P7/P8), and nothing else.** It holds no model credential, it answers from the view and the record, and a reply is a document rather than a transcript of a reasoning model | A run's own agent: it needs a channel into a job container holding the model credential, which is a pty into the cloud and a credential in a Chromium process — the disqualifier §3.2 of the console design already wrote down. **And the honest fact: the front door does not exist yet, so choosing it means the terminal waits for P7 and P8** | **Answered 2026-10-31: the recommendation.** |
| **D821** | The transport | **One allow-listed argv, `fugaro coordinator ask --json`, with the line on stdin** (§5.2). No shell, no pty, no renderer-built argv | A shell: the console would have to build a command from the operator's text, which is the one thing its whole design refuses. A pty: §5.2 | **Answered 2026-10-31: the recommendation.** |
| **D822** | Who may use it | **Per-person Cloud Run IAM invoker plus an audience-checked ID token** (§5.3). The console's identity is the operator's own ADC | A project-wide member, or a `domain:` — the rule `init_review.go` refuses in a plan and `doctor_launcher.go` reports. It would make the terminal reachable by anyone in the organisation with a token, for a service whose read tools return the installation's whole state | **Answered 2026-10-31: the recommendation.** |
| **D823** | What it may do in the first release | **The read tools only** (`get_project_state`, `get_release_status`, `check_conflicts`). `report_intent` and `record_override` come later, each in its own change with its own review; **anything reaching the launch or the budget path is refused for good** | Read and write tools in one release: it doubles the review surface and it puts the first write behind a text box, where the operator cannot see what the line will write before it does | **Answered 2026-10-31: the recommendation.** |
| **D824** | The audit log and the typed line | **The line on stdin; the console's spawn log records the argv and the time and not the words; the service records what was asked and answered in its own log** (§5.5) | The line as an argv word: it would be in the process table, in the spawn log's `argv`, and in every `ps` a person runs — and the spawn log would then hold free text nobody asked it to keep. A separate console-side audit file: a fourth file the app writes, which rule 10 does not allow without its own review | **Answered 2026-10-31: the recommendation.** |
| **D825** | Rate limits | **Server-side, per person, stricter for the write tools, with a client-side in-flight cap of one and a timeout** (§5.6). The numbers are `(unmeasured)` and are set by a profiling pass on the built service | Console-side only: the client is the thing being limited, and a second console (or `curl` with a minted token) would not be limited at all | **Answered 2026-10-31: the recommendation.** |
| **D826** | The pane's content and rendering | **The operator's line, the answer, the time; plain text, never markup, never a link** (§5.7), with the answer's size capped by the service | Drawing markdown or links: P8's finding #28 answers this, and it is the same answer rule 2 and rule 7 give | **Answered 2026-10-31: the recommendation.** |
| **D827** | Ship the read-only transcript pane first? | **Yes** (§5.8): the declared intents plus the stored view, no input control, no service, one small allow-listed read | Waiting for the front door: every installation with a coordinator switched on gets nothing until P7 and P8 ship, for a pane that can be useful now. The cost is real and should be stated plainly: **a read-only pane does not answer "a terminal to talk to the agent"** | **Answered 2026-10-31: the recommendation.** |
| **D828** | The security model | **An independent review of the whole terminal before anything is built, every refused argv on the never-reachable table, and the refusals as a property of the tool set** (§5.9) | Building the read-only pane without the review: defensible, and it is what D827 recommends — but the review is what has to happen before the **input box** exists, not before the pane does | **Answered 2026-10-31: the recommendation.** |

## 6. Where this design needs a new argv or a new write

The complete list, because every line of it is a security-sensitive change.

| # | The argv | New? | Kind | Review |
| --- | --- | --- | --- | --- |
| 1 | `fugaro runs ls --json --since 24h --limit 50` | `--limit` is new on the `runs.list` row | read | yes (D818) |
| 2 | `fugaro runs ls --json --since 24h --limit 50 --after <run-id>` | `--after` is new | read | yes (D818) |
| 3 | `fugaro coordinator show --repo <owner/name> --pr <n> --json` | new command, new row | read, **holds a host credential** | yes (D816) |
| 4 | `fugaro coordinator intents --all --json` | new command, new row | read | yes (D827) |
| 5 | `fugaro coordinator ask --json` | new command, new row, line on stdin | **read and write, reaches a service** | **independent review of the whole terminal** (D820–D828) |
| 6 | `run --task-file - --json --run-id <id> --repo <owner/name> --plan-task=<id>` | **no** — rule 11's row, already allow-listed | the console's one write | the queue's Retry and Launch are this, unchanged (D811) |

## 7. Open items

- **O1.** Whether a `--halted` (or `--state`) filter on `runs ls` is needed after D810's bounded read
  is measured: if more than a handful of halts sit outside the newest 20 rows of a 24-hour window,
  the console-side filter is not enough and the filter becomes a real argv. Nothing may be built on
  the answer before it is measured.
- **O2.** Whether the inline description needs a size cap the CLI applies (the host's body is
  unbounded) or whether the console's 20-line clip is enough. It becomes a decision the moment the
  read of D816 exists and returns a real description: a body of 200 KB is one the console parses to
  draw 20 lines.
- **O3.** Whether the front door (P7/P8) ships at all in this product's next year. D827 is
  deliberately designed so that the answer does not block the pane.
