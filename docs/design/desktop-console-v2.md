# The console, v2: reconciling the owner's second spec

*Status: 2026-10-15. Source: [desktop-console-spec-v2.md](desktop-console-spec-v2.md) — the owner's text, committed verbatim. The design it amends: [desktop-console.md](desktop-console.md), decisions D1–D20, every one of them answered by the owner on 2026-10-15. The plan it amends: [2026-10-15-desktop-console.md](../plans/2026-10-15-desktop-console.md), phases 0–5, of which phase 0 is merged. The plan this document produces: [2026-10-15-desktop-console-v2.md](../plans/2026-10-15-desktop-console-v2.md). **Nothing in v2 is approved or built** — the plan carries an owner approval gate and every task in it is gated on the answers to D21–D38 below.*

## 0. What this document is, and how its claims were checked

The owner's second console spec asks for six things the first one did not: **project discovery** over every project in the home config, including projects owned by others; a task **dependency graph**; a **command channel** to orchestrating agents through the realtime database; **overlap detection** over the files jobs changed; a **version roadmap** with drag-and-drop; and an **independence-maximizing planning skill** that feeds the graph. The rest of it — the review inbox, desktop notifications, per-job controls, spending cap alerts, spending graphs and live tasks — is the v1 spec restated, and is reconciled as such.

The shape of this document is this tree's own. A spec is committed **verbatim** as a source ([project-centric-spec-source.md](project-centric-spec-source.md), [m9-spec-source-v3.md](m9-spec-source-v3.md), [setup-and-skills-spec-source.md](setup-and-skills-spec-source.md)); a **reconciliation** reads it against the tree and rules on every difference ([m9-spec-v3-reconciliation.md](m9-spec-v3-reconciliation.md) is the precedent, and its "Rulings (binding)" table is the shape §3 copies); the **plan** carries the tasks that follow from the rulings. This document is the middle one.

**Every claim about the code was checked against `origin/main` at `a573870`.** Each finding cites a **file and a symbol**, never a bare line number: a line number is a fact about one commit, a symbol is a fact about the code, and this tree's review convention asks for the second. Every path cited below exists on `origin/main` at that commit.

**Where the owner's brief and this tree disagree about a number, the tree is quoted and the disagreement is recorded** rather than resolved silently — §5.2 does that for the release train and §5.3 for the live-check numbers, and both are corrections to the *brief*, not to the spec.

**The verdict, in one paragraph.** Four of the six new features have no substrate in this tree, and a fifth is a write path that D2 and D14 forbid. The **dependency graph** needs a machine-readable task graph, and the only planner artifact that exists is `.fugaro/render-plan.json`, written by a `mode: artifact` run's plan step and read by nobody (`internal/runner/artifact.go`'s `renderPlanPath`); **overlap detection** needs the set of files a branch changed, and no document the console can read carries it (`internal/runview/runview.go`'s `Row` has `pr_url` and no paths); the **sessions registry** needs an identity for an orchestrating agent, and the registry that exists — `/agents/<slug>/<run>` — is written by a *running job about itself* and dies with the run (`internal/budget/model.go`'s `AgentEntry`, `internal/budget/registry.go`'s `Session`); the **version roadmap** needs a project record to write to, and the owner's own end state for that record is the project-centric plan's phase 6, which is reserved and **not built**; and the **command channel** is a write to a realtime-database queue that does not exist and that nothing consumes, in an app whose whole safety argument is that it spawns a read-only allow-list of exact argument vectors. What *does* exist is what the spec itself names as the alternative path: `fugaro run`, `fugaro run --retry` and `fugaro cancel`, each with its own confirmation, and each already carrying the claim semantics that make "two agents can't start the same task twice" true at the level that actually protects anyone — **a launch**, not a task.

## 1. Findings

Each finding is classified **Exists** (the tree has it, and the spec should be read as naming it), **Wrong** (the spec, or a document in this tree, says something the code contradicts), **Gap** (nothing in the tree does it), **Rule** (not a fact but a constraint the tree's existing decisions impose on how the feature may be built), or **Open** (a question this document cannot answer from the tree alone).

### Project discovery and layout

**F1 — the home config does enumerate the installations; no command prints them. (Exists / Gap)**

The spec's "reads the global Fugaro config in the home directory and lists every project the user has set up" is already true in Go and unreachable from outside the process. `internal/localcfg/localcfg.go` has `ProjectsDir` (`$XDG_CONFIG_HOME/fugaro/projects`, else under `~/.config`), `ProjectPath`, `Projects(getenv)` (an `os.ReadDir` of `projects/*.yaml` returning the names) and `LoadProject`. Two commands already call it: `internal/cli/doctor.go` and `internal/cli/init.go` — the latter so that a bare `fugaro init` can offer the installations it found. `internal/localcfg/select.go`'s `SelectError` carries a `Projects` field, so a refusal to pick a project lists them too.

What does **not** exist is any command that prints that list as a document. The console may only spawn commands that exist, and only with arguments on its allow-list (design [desktop-console.md](desktop-console.md) §5.1 rule 3), so "the sidebar has one entry per project" needs a new read — v1's gap 5 — and nothing else about the layout needs anything at all.

**F2 — the v1 design's gap 5 is wrong as written. (Wrong)**

`desktop-console.md` §4.3 gap 5 says that "nothing on this tree enumerates installations". As stated that is false: `localcfg.Projects` enumerates them, `doctor` and `init` use it, and `SelectError.Projects` prints them in a refusal. What is true, and what the gap should say, is that **no command exposes them as a document** — the difference matters because the first version reads as "this needs new machinery in `localcfg`" when it needs a new command over machinery that already exists. The correction is recorded here rather than edited into v1: v1 is merged, its decisions are answered, and a finding that rewrites history is worth less than one that says what changed.

**F3 — "including projects owned by others" is a credential question, not a listing question. (Gap / Rule)**

The list of projects in the home config is the list of projects *this operator has been set up for*. It is not a directory of projects that exist, and there is no such directory: a Fugaro installation is a GCP project with a runs bucket and a realtime database, and finding one you have never been told about would mean enumerating someone else's GCP projects, which no credential on the machine permits and no part of this design wants.

A project owned by someone else reaches this operator the way it reaches the CLI today: **by name**, through the config that project *publishes* to its own runs bucket. `internal/localcfg/shared.go` has `(*Config).Shared()` (the local config's view of the published one), `MergeShared` (published values filling in what the local config does not set) and `SameInstallation` (whether the published config is the same installation the local config thinks it is); `internal/localcfg/sharedcache.go` caches the fetched copy for `SharedFreshFor` (24 h) and lets it stand in for an unreachable bucket for `SharedOfflineFor` (7 days). The fetch is authorised by whatever the operator already holds on that project's bucket — `internal/cli/cloud.go`'s `cloudOptions`/`addCloudFlags` add `--gcp-project` for exactly the case where the project name is not enough to find the installation.

So the spec's sentence is achievable, but only as: **the sidebar lists the projects in the home config, and a project owned by someone else appears there only because the operator added it** — which is the same rule the v1 design's D13 states as "the app picks an installation per project". A console that invented a wider list would be inventing access. See **D31**.

**F4 — "jobs running, blocked, needs attention" has one state out of three. (Gap / Rule)**

`running` exists and is well documented: the live registry is `/agents/<slug>/<run>` (`internal/budget/model.go`'s `AgentEntry`, `PathAgent`), the feed is `internal/watch/json.go`'s `BuildJSON` with `JSONRun` and `JSONFinished`, and the row type behind `fugaro runs ls` is `internal/runview/runview.go`'s `Row`. `blocked` and `needs attention` are **not states anywhere in this tree** — no document carries either word, and nothing computes them. The nearest thing that exists is a *rate* alert: `internal/localcfg/localcfg.go`'s `(*Config).BurnAlert()` (from `watch.burn_alert_usd_per_hour`) feeding `internal/cli/watch.go`'s `watch.Config{BurnAlertPerHour: …}`, and the launch claim object `internal/runstore/launch.go`'s `Claim` (a run that has been claimed but not started is arguably "needs attention", but nothing surfaces it that way).

**Rule:** a status dot may only be a *colouring of a field a document carries*. A dot whose state is computed in the app from something other than a document is the same defect the v1 design's rule 2 names for `pr_url` — a display that asserts a fact no document states. `blocked` needs the graph (F6) and `needs attention` needs a definition before it can be a colour; until then the sidebar shows running and not-running, and says so.

**F5 — "total spend across projects" is a sum no single installation can back. (Gap / Rule)**

Spend lives per installation: `internal/budget/model.go` has `PathSpendGlobal`, `PathSpendRepo` and `PathSpendRun` under the installation's realtime database, `PathCapsRepo` for the caps, and the history job writes Firestore's `spendDaily` for the same installation. `fugaro budget show` prints one installation's figures (`internal/cli/budget.go`'s `budgetShowDoc`, whose `Project` and `GCPProject` fields say *which* installation they are about), and `fugaro report` reads the same installation's history (`internal/report/report.go`'s `Build`).

There is no cross-installation total anywhere, and the reason is not laziness: two installations' figures are two different currencies of trust — one is read live from a database this operator can read, the other may be a cached copy of a config fetched up to 7 days ago (`SharedOfflineFor`) with spend figures that are not in the shared config at all. **Rule:** the Overview shows one figure **per installation**, labelled with the installation it came from, and a summed total appears only where one installation's own documents back it. See **D32**.

### Dependency graph

**F6 — there is no planner artifact with task dependencies. (Gap)**

Nothing in this tree produces, stores or reads a task dependency graph. The plans under `docs/plans/` are prose task lists: `2026-10-15-desktop-console.md` ends with a hand-written "cross-task dependencies" list, and `2026-10-14-project-centric.md` orders its phases by hand in the same way. That is a real dependency graph, written by a person, in a format only a person reads.

The one machine-readable planner artifact is `mode: artifact`'s: `internal/runner/artifact.go`'s `renderPlanPath` is the constant `.fugaro/render-plan.json`, written by a run's plan step (`runArtifactMode`, `runRender`) and described in its own prompt as something to read and then throw away — the plan document is about *render* steps, not tasks, and it carries no dependencies and no footprints. `internal/runstore/artifact.go`'s `RenderRecord` records what the render did; `internal/runstore/runstore.go` carries it as `Render *RenderRecord`. None of that is a task graph.

So the graph is **new work with no precedent to extend**, and it needs three decisions rather than one: where the artifact lives, who writes it, and what reads it. See **D25**.

**F7 — two of the four colours are computable today and two are not. (Gap)**

`done` and `running` come from documents that exist (`JSONFinished`, `JSONRun`, `AgentEntry`). `ready` and `blocked` are properties *of the graph* — a node with no unfinished dependency is ready — and since the graph does not exist (F6), neither does either colour. This is not a UI detail: "ready" is also what the launch action (F8) is allowed to act on, so a console that guesses "ready" is a console that can launch a task whose dependency is half-built.

**F8 — "launch selected ready tasks" is a write, and "fan out everything ready" is an unbounded one. (Gap / Rule)**

Launching exists: `fugaro run` (`internal/cli/run.go`'s `launchResult`, whose `status` is `launched` or `already-launched`), with `--task-file` for a task handed over as a file and `--retry` for "the stored task of RUN … that never started". But every launch is a **write** and a **claim**: `internal/runstore/launch.go`'s `(*Store).Claim` writes the `launching` object (`ClaimKey`) holding `{holder, at}` with `ClaimTTL` of 10 minutes, so that a second launcher of the same run gets `already-launched` instead of a second container. The console is read-only in v1 (D2), and D14 says read-only is enforced by the exact-argument allow-list **alone** — which means the allow-list is the whole fence, and a launch cannot be added to it without deciding that the fence has moved.

**Rule:** "fan out everything ready" is not a bigger button, it is a different risk class from "launch this one": it multiplies a write by a number the app computes, so the *app* decides how many containers run. If any fan-out exists in v2 it must be a single CLI command that takes the plan, applies the caps and the kill switch itself, and reports what it refused — never a loop of launches in the app. See **D22**.

### Command channel to orchestrators

**F9 — the queue contradicts D2 and D14 outright. (Wrong)**

"Console writes commands (e.g. start tasks 4 and 7) to a command queue in the realtime database" is a **write to a project's database by the desktop app**. That is exactly what D2 ruled out ("strictly read-only v1, with kill/resume/cancel deferred to the CLI's own confirmed commands") and what D14's answer makes structural: *every identity may be an admin, and read-only is enforced by an exact-argument allow-list alone*. An allow-list over argument vectors can express "this app may run `fugaro runs ls --json` and nothing else"; it cannot express "this app may write this one path in your database", because the app would hold a credential that can write all of them. Both answers are recorded in `desktop-console.md` §10 (D2, D14) and both are restated as Global Constraints in `2026-10-15-desktop-console.md`.

**F10 — there is no command queue, and nothing that would read one. (Gap)**

The queue the spec names does not exist in any form. The realtime database's paths in this tree are the budget and registry ones (`internal/budget/model.go`: `PathCapsRepo`, `PathSpendGlobal`, `PathSpendRepo`, `PathSpendRun`, `PathAgent`) plus the config and outcome paths the design's §2 table lists; no prefix holds commands, and no Go code polls one. What plays the queue's role today is the **runs bucket**, and it is a different design: `runs/<slug>/<run>/task.json` is written by the launcher and consumed by the Cloud Run job, `launching` is the claim, and the reader is `internal/backend/gcp`'s job path, not a poller.

The difference matters for the spec's own sentence: "orchestrating agents listen via a hook or a Fugaro command polled between turns". There is **no hook mechanism** in `plugin/` (the plugin is skills, not callbacks) and **no `fugaro` command that polls anything** — the command list in `internal/cli/root.go` is `version`, `validate`, `config`, `verify`, `exec`, `image`, `run`, `recipes`, `runs`, `logs`, `diagnose`, `cancel`, `secrets`, `accounts`, `init`, `budget`, `watch`, `report`, `update-skills`, `upgrade`, `doctor`. The nearest long-lived reader in the tree is `fugaro watch --json` (`internal/cli/watch.go`'s `--json` help: "print one JSON document per change instead of a screen"), which the console already owns — but it *reads*, and a queue needs a reader that *acts*.

**F11 — what a queue would cost, said plainly. (Rule)**

The v1 design's whole safety story is that the app is a spawner of read-only commands. A queue changes five things at once, and each is a decision the owner has to make rather than an implementation detail:

- **Who may write it.** The runs-bucket grant this tree settled on is per-prefix and shared by every run of a profile ([bucket-iam.md](bucket-iam.md)'s end-state table and its H2 alternative). A queue path in the same installation inherits that shape: whoever may write a command may write *any* command, including one addressed to a repository they do not own.
- **What runs it.** Nothing today. A queue needs a consumer that is always on, holds the project's credentials, and is not a Cloud Run job launched by a claim — that is a new service, in a tree whose every job is launched with a claim and dies.
- **Replay.** The launch path has a claim, a TTL and an `already-launched` answer (`Claim`, `ClaimTTL`, `ClaimKey`). A realtime-database command has none of the three: no ack, no expiry, no dedupe, and a consumer that reconnects re-reads. The claim is not a detail that can be added later — it is the only reason "two agents can't start the same task twice" is true today.
- **Authorization.** D14's answer is that every identity may be an admin and the allow-list is the fence. A write path makes the app an identity with a *project* credential, and there is then no fence left to describe.
- **The budget kill switch.** `internal/budget/kill.go`'s `SetKill` is the operator's stop, and `internal/budget/sweep.go` is what enforces it against spend. A launcher must consult it **before** launching, and `fugaro run` already has `--no-budget-check` as the explicit way to say "I know". A queue that launches jobs has to answer the same question in the same place, or the kill switch becomes advisory for exactly the path the console controls.

**F12 — the alternative the spec itself names is the thing that exists. (Exists)**

"Alternative path: console launches ready jobs directly via the Fugaro CLI; the agent sees them appear in the graph." Every part of that sentence has a counterpart in this tree: the launch is `fugaro run` (with `--task-file`, `--retry`, `--pr`, `--recipe`, `--json`), the confirmation semantics are `fugaro cancel`'s (`internal/cli/cancel.go`'s `cancelResult`, whose constants distinguish the outcomes a cancel can have), and "the agent sees them appear in the graph" is the run registry the agent's own budget session writes (`internal/budget/registry.go`'s `Session.Start`/`Update`, `AgentEntry`). What is missing is only the graph itself (F6) and the console's permission to write (F9).

**F13 — "polled between turns" has no command behind it. (Gap)**

There is no `fugaro` command that a long-running agent can poll for instructions, and adding one is not a small thing: it is the read half of the queue in F11, and it needs the same answer about who may write what it reads. The honest interim is that an agent between turns can run `fugaro runs ls --json` (`internal/cli/runs.go`'s `lsDoc`, `--json` at the same file) and see what is running — a **read**, which is what the console can do too. See **D21**.

### Sessions and orchestrators

**F14 — nothing registers an orchestrating session, and the registry that exists is a different thing. (Gap)**

The spec asks each orchestrator to "register a session with an ID, label, and project". The registry in this tree is `/agents/<slug>/<run>`: `internal/budget/model.go`'s `PathAgent` and `AgentEntry`, written through `internal/budget/registry.go`'s `Session` (`Start`, `Update`, `Finish`, with `goSafely` around the fire-and-forget writes) and leased in `internal/budget/lease.go` (whose `AddNotional` is what feeds the repository's and project's notional counters). It is **written by a running job about itself**, keyed by run, and it stops when the run stops.

An orchestrating agent is not a run: it has no container, no Cloud Run job, no slug binding and no budget token. The per-run identity this tree mints — `internal/budget/token`'s object at `runs/<slug>/<run>/budget-token` with claims `{fs, fr, fx, fp, rb}` — is exactly what a session would need and exactly what a session does not have. So a sessions registry is **new identity work**, not a new view over an existing table, and it is the single most expensive thing in this spec after the queue.

**F15 — the claim the spec wants exists, at the level of a launch rather than a task. (Exists / Gap)**

"Tasks are claimed before starting, so two agents (or an agent and the user) can't start the same task twice." Two claims exist and both are about runs, not tasks:

- the launcher's: `internal/runstore/launch.go`'s `(*Store).Claim` writing `launching` (`ClaimKey`) with `ClaimTTL`, read back by `ReadClaim`, so a second launcher sees `already-launched` in `launchResult.status`;
- the runner's: `internal/runner/lockcache.go`'s `ErrDuplicateExecution` ("another execution already owns this run"), with `internal/lock`'s branch lock beneath it so that two executions cannot push the same branch.

Both are the *right* answer to the question they answer. Neither prevents two agents from starting **the same task** in two sessions, because a task is prose in a plan file (F6) — there is no object to claim. Task-level claiming therefore arrives with the graph, not before it, and it should be built as a **claim on the graph's node**, with the same shape as `launching`: a holder, a timestamp, a TTL. See **D24**.

**F16 — "one graph per session" needs a per-session artifact that has no home. (Open)**

If sessions exist, each has its own view of the work, which means the graph is per-session rather than per-project — and then the question "where does a per-session graph live" has three possible answers (a file in the orchestrator's checkout, a path in the project's realtime database, a local file the console reads) with different trust properties, and the tree does not pick one for the owner. It is recorded as Open and folded into **D24**.

### Overlap detection

**F17 — no document the console can read carries the files a branch changed. (Gap, and the hard one)**

This is the finding most likely to be underestimated. `internal/runview/runview.go`'s `Row` — the row behind `fugaro runs ls` — carries `repo`, `branch`, `base_branch`, `pushed`, `pushed_head`, `pr`, `pr_url`, `requested_by` and `oauth_account`, and **no changed-files field**. `internal/watch/json.go`'s `JSONRun` and `JSONFinished` likewise. `internal/runstore/runstore.go`'s record carries the outcome, the render record and the artifacts, not paths. The files a run changed are known exactly once, transiently, and are then thrown away: `internal/followup/prompt.go`'s `PromptData.DiffStat` (clipped to `maxDiffStatBytes`, 4 KiB) is built in `internal/gitops/rejected.go` for the follow-up prompt and is never stored.

So overlap detection cannot be a console-side computation over existing reads. It needs a **new recorded fact** — the set of paths a run's branch touched — written by the run (which knows) into the run's own record (which is read). That is a CLI change with a schema, a size bound and a rules/deploy consequence, and it is v2's second-largest piece of work after the graph. See **D27**.

**F18 — "expected footprint per task" has nothing to attach to. (Gap)**

The planner cannot add a footprint to a task that is not an object (F6), and the planning skill has no such output today (F31). The footprint is therefore part of the graph artifact's schema, not a separate feature — and it is **advisory by construction**, because a predicted path set is a prediction. See **D28**.

**F19 — "sessions grouped by repo" depends on sessions. (Gap)**

Follows from F14. What exists today is the repo grouping in the feed itself: `JSONRun`/`Row` carry `repo`, and the runs bucket is keyed `runs/<slug>/<run>`.

**F20 — the console gets changed files from the CLI or not at all. (Rule)**

The tempting shortcut is a GitHub token in the app: read the PR's files list and be done. It is the wrong answer for three independent reasons, and the third is the one that would bite. First, a token in the app is a **second credential** with a second trust boundary, in a design whose §5.1 rules say the app holds no credential of its own and whose §7.4 says nothing leaves the machine. Second, **the provider is not knowable** from any document the console reads — the v1 design's rule 2 spells this out: `pr_url` is a hostile field written by the run's own container, `internal/task/task.go`'s `Slug` hashes provider and repository together one way, and no JSON shape carries the provider, so the console cannot even tell whether a given run is on GitHub or Bitbucket Cloud. Third, the app would be **reading a third party's API about the operator's own repositories**, which is a new egress path in an app whose CSP is `connect-src 'none'` and which has no network client at all.

**F21 — "soft warnings, not locks" is the existing model. (Rule)**

`internal/lock` is a branch lock held *by a run* to stop two executions pushing the same branch, and it is not the console's to take: a console that locked branches would be a second writer to the same lock space. Overlap detection is a *report*, and the design's rule that the app writes nothing (except the opt-in spawn log, D20) means the report is a view, not a state.

### Version roadmap

**F22 — the project record the spec calls the source of truth does not exist yet. (Gap / Open)**

"Source of truth is the Fugaro project (stored with project state, e.g. in the project Git repo)." That is the owner's own end state, and this tree has it as a **planned** thing: `docs/plans/2026-10-14-project-centric.md`'s phase 6 is the backing-repository export — the `fugaro project export` command, whose owner-run check is **Check 56** in the same plan's reservations — and it is reserved and **not built**. Until it lands there is no project Git repository to hold a task's target version, and no document anywhere carries one: nothing in `internal/` has a version field per task, and the only versions in the tree are the CLI's own (`internal/cli`'s `Version`, `docs/releases/`, and the plugin's `fugaro-version` markers, e.g. `plugin/skills/parallelism/SKILL.md`'s `fugaro-version=0.7.5`).

Two interim options exist and both are cheap; the choice is the owner's (**D29**): the version lives in the plan artifact beside the task (works today, no new infrastructure, dies with the plan file), or the roadmap view waits for phase 6 (one source of truth, nothing to migrate, and the view cannot ship until then).

**F23 — a drag is a write. (Rule)**

"Drag tasks between versions" edits the project record. Under D2/D14 that is the same class of thing as the queue (F9): the app would hold a credential that writes the operator's project state. The shape that fits this tree is the one the v1 design already uses for its deferred write actions — the console shows the **exact command** that would make the change, the operator runs it, and the CLI's own confirmation is the fence. See **D30**.

**F24 — "dependency-aware: flags a task scheduled earlier than something it depends on" needs the graph. (Gap)**

Follows from F6. It is a pure function of the graph and the version column, so it is cheap *once* the graph exists and impossible before.

**F25 — "no GitHub milestones or Jira sync" is already this tree's position. (Exists / Rule)**

Nothing in this tree reads GitHub milestones or Jira; the provider story is [git-providers.md](../git-providers.md) and the only provider client is `internal/gitops`. Worth recording as a rule the console inherits rather than as a thing to build: the roadmap's source of truth is Fugaro's own record, and the absence of a sync is a design decision, not an unfinished feature.

### Additional features (all wanted)

**F26 — the review inbox exists as data, and its links are governed by rule 2. (Exists / Rule)**

Finished jobs with their numbers and their `pr_url` are in the feed (`JSONFinished`, `Row`). The link is the governed part: the v1 design's rule 2 says the console **opens no PR link at all** until a document carries the repository's provider (its gap 7), because a GitHub-shaped guess is wrong on Bitbucket Cloud and dangerous where the name is someone else's. So the inbox ships as rows with copyable text, and it starts opening links the day gap 7 lands — no new decision here, only the note that "PR links" in the spec means "the PR number and URL as text" in the first version.

**F27 — desktop notifications are a new mechanism, with a new surface. (Gap)**

Nothing in this tree notifies a user about a job. The one thing that ever speaks up is `internal/updatecheck/updatecheck.go` — a daily `GET` against the releases endpoint, cached under `$XDG_CACHE_HOME/fugaro`, at most one nag a day (`WarnTTL`, `CheckTTL`, `FetchTimeout`), disabled by `FUGARO_NO_UPDATE_CHECK=1`, by `update_check: false` and automatically in several contexts — and it is a version nag, not an event feed. The nearest *live* signal is the burn alert the watch already computes (`BurnAlert()` into `watch.Config{BurnAlertPerHour: …}`).

A desktop notification needs three things this tree does not have: a long-lived reader (the console has one), a rule about **what may appear in a notification** (an OS notification renders outside the app's window, outside its CSP, and may be shown on a locked screen — so the content must be a fact from a document and never a string from a hostile field, which is the `pr_url` rule again), and a definition of "needs input", which does not exist as a state (F4). See **D33**.

**F28 — per-job controls map cleanly: one read, two writes. (Exists / Rule)**

- **live logs** — a read: `fugaro logs` (`internal/cli/logs.go`'s `logLine`), or the watch feed's log lines.
- **cancel** — a write, and it exists: `fugaro cancel RUN` (`internal/cli/cancel.go`'s `cancelResult`), with the CLI's own typed confirmation and its grace semantics.
- **retry** — a write, and it exists: `fugaro run --retry RUN`, "launch the stored task of RUN … that never started" (`internal/cli/run.go`).

So the mapping is complete and there is nothing to invent — which is exactly why D2 deferred these three to "the CLI's own confirmed commands". **Rule:** if the console gets them, it gets them as *spawns of those commands with their own confirmations*, shown with the argv it built (the opt-in spawn log, D20, is what makes that auditable), never as a console-side re-implementation. A console-side cancel would have to write the run's `cancel` marker itself, which is the write surface D2 keeps out. See **D34**.

**F29 — cap alerts have data and no threshold. (Exists / Gap)**

Caps and spend are readable: `fugaro budget show`'s `budgetShowDoc` (`internal/cli/budget.go`), the realtime-database paths in `internal/budget/model.go` (`PathCapsRepo`, `PathSpendGlobal`, `PathSpendRepo`, `PathSpendRun`), and Firestore's `spendDaily` for history. What does not exist is any notion of "approaching": the tree's one alert is a **rate** — `watch.burn_alert_usd_per_hour`, read by `(*Config).BurnAlert()` and passed into `watch.Config` — not a fraction of a cap. So "warn as a project approaches its cap" needs a threshold, and the question is where the threshold lives: a local setting of the app (a read-side computation over figures the CLI already prints) or a new field in the project record (a rules change, a deploy, and a version skew, for a display). See **D35**.

**F30 — spending graphs and live tasks exist. (Exists)**

`fugaro report` (`internal/report/report.go`'s `Build`, and the documents in `internal/report/render.go`: `JSONDoc`, `JSONLine`, `JSONTokens`) is the history; `fugaro runs ls --json` (`lsDoc`) and the watch feed are the live tasks. Both are reads the console's allow-list can already name, and both are already in the v1 design's data-source table.

### The independence-maximizing planning skill

**F31 — the skill exists and its budget is the constraint. (Exists / Gap)**

The planning and routing skills under `plugin/skills/` are `parallelism`, `routing`, `setup`, `upgrade` and `working`. The one that owns fan-out is **`parallelism`** (`plugin/skills/parallelism/SKILL.md`, whose description is "Plan work that could be split across several Fugaro runs: wide for independent work, narrow for coupled work, redundant attempts for one hard task"), and at 47 lines and 3245 bytes it is nowhere near its budget — the constraint is not space, it is the *shape* of the output.

The lint budgets are in `plugin/skills_lint_test.go`: `maxSkillLines` 400 and `maxSkillBytes` 24576 for a `SKILL.md`, `maxReferenceLines` 300 and `maxReferenceBytes` 16384 for any other skill file, plus frontmatter rules (`name`, a `description` with "use when", 40–1024 characters). So a new skill costs a new directory, a new frontmatter, and a second place where fan-out advice lives and drifts. See **D36**.

**F32 — "cut dependencies with small interfaces/contracts" is already how this repo plans, by hand. (Exists)**

The console plan's own "cross-task dependencies" section and the project-centric plan's phase ordering are exactly this technique, performed by a person writing prose. The skill's contribution is to make the *output* machine-readable, not to invent the technique.

**F33 — "feeding the console directly" must not become a file the app reads. (Rule)**

Two fences stand in the way and both are load-bearing. The v1 design's data-plane rule is that **the CLI is the only data plane** — the console reads documents a `fugaro` command printed, never a file it found. And `console/README.md`'s Go/console boundary, enforced by `internal/cli/docs_console_boundary_test.go`'s allow-list, keeps the Go module and the console from importing each other. A planner artifact the console opened directly would be the first exception to both. So the skill writes the graph **where the plan lives**, and the CLI grows a command that prints it — see **D25**.

## 2. What this leaves to decide

The v1 design's decisions D1–D20 are answered and unchanged; nothing below reopens them. D21 onward continue the numbering, and each row carries a recommendation and, in the last column, what the alternative costs. **None of them is answered yet.**

### The four load-bearing ones

**D21 — the command channel: does the console ever write a queue?** Recommended: **no, in any slice**. The console shells out to the CLI's own commands (`fugaro run`, `fugaro run --retry`, `fugaro cancel`), each with the CLI's own confirmation, and there is no queue, no poller and no new consumer. The alternative — the spec's queue — costs a write surface with no claim, no TTL and no ack (F11); a path every run of a profile can reach under the current grant shape; a new always-on consumer that does not exist; and it voids D14, because the allow-list can no longer be the whole fence. If the owner ever wants a queue, the shape that fits this tree is **not** a realtime-database path: it is a `fugaro` command that writes a **task the launcher already knows how to consume** (`runs/<slug>/<run>/task.json`-shaped, claimed through `internal/runstore/launch.go`'s `Claim`), so the claim, the TTL and the budget check stay where they are.

**D25 — where the dependency graph lives, who writes it, and what reads it.** Recommended: a **plan-side artifact plus a CLI read**. The planning skill writes a graph document **beside the plan it is planning** (the plan is already the artifact a person writes and reviews), and the CLI grows a read-only command that prints it — the console then reads it the only way it reads anything, over the allow-list. The graph's schema is the thing to get right and it is small: nodes are tasks with an id, a title, a state source and a **footprint** (D28); edges are dependencies; and the document carries the plan it came from and a version so that a console can refuse a graph it does not understand (the version-skew handshake the v1 plan already builds in phase 3). The alternative — the graph in the realtime database, written by the orchestrators — costs a rules deployment per schema change, a write path in the agents (F11's list applies verbatim), and a graph that two sessions can disagree about with no way to tell which is newer.

**D29 — the version roadmap's source of truth.** Recommended: **both, in order.** Today the target version lives in the plan artifact beside the task, which works with no new infrastructure and gives the roadmap view something real to draw; when the project-centric plan's phase 6 (`fugaro project export`, Check 56) lands, the version moves into the project record and the plan artifact's copy becomes a projection. The console reads it through the CLI in both cases, so the view does not change when the source does. The alternative — waiting for phase 6 — costs the roadmap view until phase 6 ships, which is the longest pole in the project-centric plan; the alternative — the version only ever in the plan file — costs the owner the "stored with project state" property the spec asks for, and it means a plan that is deleted takes its roadmap with it.

**D31 — project discovery, and what "has access to" may mean.** Recommended: **the home config is the list, and the CLI prints it.** The v1 gap 5 is closed by a read-only command that enumerates `localcfg.Projects` as a document (F1); the sidebar is that list; a project owned by someone else appears because the operator added it, and its data is read with the operator's own credentials through the published shared config (`(*Config).Shared()`, `MergeShared`, `SameInstallation`, and the cache's `SharedFreshFor`/`SharedOfflineFor`). The alternative — the app reads the home config itself — costs the app a second parser of a two-format file (local and published, merged by `MergeShared`), contradicts §3.4 rule 7 (the app never reads anyone's config), and puts the merge logic in the one place that cannot be tested against the Go one.

### The rest

| # | Question | Recommendation | The alternative, and what it costs | Owner's answer |
|---|---|---|---|---|
| **D22** | Does v2's first slice contain any write at all? | **One**: launching, through `fugaro run`, behind the CLI's own confirmation and the budget check — a console that cannot start work is a dashboard. Everything else stays read-only. | Staying strictly read-only (D2) costs the "launch ready tasks" half of the spec, which is the feature the graph exists to serve. A second write (cancel, retry, drag) costs the argument that the allow-list is the fence, one write at a time. | — |
| **D23** | When the console launches, whose claim is it? | The CLI's, unchanged: `internal/runstore/launch.go`'s `Claim` writes `launching`, and `already-launched` in `launchResult.status` is shown as a **fact** ("an agent already started this"), not as an error. | A console-side claim is two claims, one of which the runner does not know about; the only guard that would still hold is `internal/runner/lockcache.go`'s `ErrDuplicateExecution`, and by then a container has been paid for. | — |
| **D24** | A sessions/orchestrators registry: build one? | **Not yet, and not as a new identity.** A "session" is a label on runs the console already sees (`Row.requested_by`, `AgentEntry`), and a real registry waits until an orchestrator has an identity of its own. If one is built, it is a claim with a TTL, in the shape of `launching`, written by the agent through a `fugaro` command — never by the console. | A console-written registry is a registry the console must authenticate to, and D14 says every identity may be an admin; a per-session graph (F16) then needs a per-session home, which is a second storage decision taken in a hurry. | — |
| **D26** | Bottleneck ranking: critical path or raw downstream count? | **Critical path**, computed in Go beside the graph read so two consoles cannot disagree, with the raw count available as the cheap fallback when a footprint has no duration to weigh. | Raw count costs a "bottleneck" that is really just a hub: a node with six one-line dependents outranks one with three dependents that are each a week of work, and the ranking is the feature. | — |
| **D27** | Where does overlap detection get changed files? | **From the run's own record, written by the run.** The runner knows the paths it changed; it records the set (bounded, path-only, no contents) in the run's record, and a `--json` read exposes it. Overlap is then a comparison of two recorded sets. | A provider token in the app: a second credential (F20), an unknown provider (no document carries it), a new egress path in an app with `connect-src 'none'`, and it makes the feature depend on the third party rather than on the run. | — |
| **D28** | What shape is a task's footprint? | **Glob-shaped paths, advisory, and labelled as a prediction** in the graph document — `internal/gitops`-style matching on the console's side only for display, with the *warning* computed where the graph is read. | Exact paths are brittle enough that the planner will stop writing them; no footprint costs the pre-launch warning the spec asks for, and leaves only the post-hoc overlap of D27. | — |
| **D30** | Drag-and-drop between versions. | **Read-only roadmap with a "copy the change" affordance**: the console shows the exact command that would make the edit, the operator runs it, the CLI confirms. This is the shape v1 already chose for its deferred write actions. | The console editing the plan file in the operator's checkout: the app would hold a write path into the user's **working tree**, which is a worse thing to own than a write to a database — it can clobber uncommitted work, and no confirmation dialog makes that safe. | — |
| **D32** | Cross-installation spend in the Overview. | **One figure per installation**, each labelled with the installation it came from and with how fresh it is; a summed total only where a single installation's own documents back it. | A grand total across installations silently mixes a live figure with one up to 7 days old (`SharedOfflineFor`), and it is a number no installation can be asked to confirm — the console would be the only place it exists, which is exactly the property this repo's designs avoid. | — |
| **D33** | Desktop notifications. | **Derived from the watch feed by the console's own reader**, off by default, content limited to facts a document carries, never a string from a hostile field, and never a link the console would not open (rule 2). | A push service (FCM) costs a new service, a new credential and a notification that leaves the machine, against §7.4; and a notification rendered outside the window is outside the CSP, so its content is a security decision and not a styling one. | — |
| **D34** | Per-job controls. | **Spawns of `fugaro cancel` and `fugaro run --retry`**, with the CLI's own confirmations, the argv shown to the operator, and the spawn log (D20) as the audit. Live logs stay a read (`fugaro logs`). | A console-side cancel costs a write of the run's cancel marker from the app — the write surface D2 exists to keep out — and a console-side retry would have to re-implement `--retry`'s "never started" rule, which lives in the CLI's record. | — |
| **D35** | Where does the cap-alert threshold live? | **A local setting of the app**, applied to figures the CLI already prints (`budgetShowDoc`, `PathCapsRepo`/`PathSpend*`); the existing `watch.burn_alert_usd_per_hour` rate alert is left exactly as it is. | A new field in the project record costs a rules change, a deploy and a version skew for a display; and two thresholds (a project field and an app setting) is how a user ends up with alerts they cannot turn off. | — |
| **D36** | New planning skill, or upgrade `parallelism`? | **Upgrade `parallelism`**, adding one reference file for the graph/footprint format if the SKILL.md budget gets tight — it is 47 lines of a 400-line budget (`plugin/skills_lint_test.go`), so the room exists. | A new skill costs a second frontmatter, a second lint surface, and two files that will drift about the same subject; it also splits "how wide should this be" from "how independent can this be", which are one question. | — |
| **D37** | Release placement. | **Post-0.8.0, and unscheduled until the owner approves v2.** The console is its own artifact published from the CLI's own tag (D11); v2's first shippable version is whatever release follows the one that carries the v1 console's phase 4. | Attaching v2 to a named release now costs a commitment the tree cannot back: the release train recorded in this repo is R4 of the project-centric plan (§5.2), and v2's own tasks are not written yet. | — |
| **D38** | Sequencing against the v1 console plan. | **v1 phases 0–5 are unchanged and v2 adds later phases** (§4), each independently mergeable, with the graph and the changed-files record before anything that draws them. | Re-cutting the v1 plan around v2 costs a merged plan its stability for work that is not approved; putting v2's reads before v1's shell costs a console that cannot display them. | — |

## 3. Release placement and sequencing

**The console is its own artifact published from the CLI's own `vX.Y.Z` tag** — D11, restated in the v1 plan's "Releases" paragraph, which is what [release.md](../release.md)'s "one tag releases everything" means once a second deliverable exists. Its first shippable version is whatever release carries the v1 plan's phase 4 (macOS packaging, Check 59). **v2 changes none of that** and adds no release: it is post-0.8.0 work whose first shippable version is whatever tag follows the v1 console's first one (D37).

**What v1 keeps, unchanged.** All of the v1 design's decisions, all ten of §5.1's hard rules, and every task of the v1 plan: phase 0 (merged), 1, 2, 3, 4 and the unscheduled phase 5. Nothing in v2 rewrites a v1 task, and nothing in v2 is a prerequisite for one.

**What v2 adds, as later phases** (the tasks are in [2026-10-15-desktop-console-v2.md](../plans/2026-10-15-desktop-console-v2.md)):

| Phase | What it adds | Gated on |
|---|---|---|
| **V2.1 — the reads the sidebar needs** | the installation list as a document (F1, D31); the run record's changed-file set (F17, D27) | D31, D27 |
| **V2.2 — the graph** | the graph artifact and its schema (F6, D25); the footprint (F18, D28); the read that prints it; critical-path ranking (D26) | D25, D28, D26 |
| **V2.3 — the graph on screen** | the graph view, the four colours (F7), the ready/blocked computation where the graph is read | V2.2 |
| **V2.4 — overlap** | the comparison and its soft warnings (F17, F21) | V2.1's record, D27 |
| **V2.5 — writes, if the owner says yes** | launching through the CLI (F8, D22, D23), then cancel and retry (F28, D34), then the roadmap's copy-the-change (F23, D30) | D22 and, for each write, its own answer |
| **V2.6 — the version roadmap** | the version column (F22, D29) and the dependency-aware warning (F24) | D29, V2.2 |
| **V2.7 — the rest of the spec** | the review inbox (F26), notifications (F27, D33), cap alerts (F29, D35) | D33, D35 |
| **V2.8 — the planning skill** | the upgraded `parallelism` and its output format (F31–F33, D36) | D36; and it can land **before** the console reads anything, since its output is a file a person can read too |

**Sequencing notes worth stating.** The skill (V2.8) has no dependency on the console at all and is the cheapest useful piece of the whole spec — it makes plans better for readers who never install the app. The two *reads* (V2.1) come before everything that draws them, because a view built on an absent field is the one mistake this kind of work reliably makes. The writes (V2.5) come last and each needs its own answer, because D22 is the decision that moves the fence and it should be taken once, with the reads already shipped and testable.

## 4. The spec's typos, and its file name

**The spec is committed verbatim and nothing in it was fixed** ([desktop-console-spec-v2.md](desktop-console-spec-v2.md)). Reading it line by line, **there is no unambiguous misspelling in it**. Four things a reader might flag, none of which was changed:

1. **"Fugaro" / "fugaro".** The spec writes the product name "Fugaro" and never the binary's name; this tree uses both ("Fugaro" in prose, `fugaro` for the command), so the spec's usage is the tree's usage and not a typo.
2. **"tmog.org"** — the same site the v1 design names as the visual inspiration ("the task manager at tmog.org"). Recorded because it is the one proper noun in the spec that is not this project's.
3. **"0.7.0, 0.7.5, 0.8.0"** as example versions — all three are real: `docs/releases/` holds `v0.7.0.md` and `v0.7.5.md`, and 0.8.0 is the next release train (§5.2). They are examples, not a commitment, and the spec does not say which release any of them is.
4. **"the realtime database"** — this tree says "the Firebase Realtime Database" or "RTDB" and treats the path space as a documented surface ([m9-budget-and-dashboard.md](m9-budget-and-dashboard.md), [bucket-iam.md](bucket-iam.md)). The spec's looser phrasing is not wrong; it is the phrase that makes F10 worth writing down.

**On the file name.** This tree's convention for a verbatim spec source is `<feature>-spec-source.md` — `project-centric-spec-source.md`, `m9-spec-source.md`, `m9-spec-source-v3.md`, `setup-and-skills-spec-source.md`. This one is committed as **`desktop-console-spec-v2.md`**, the name the owner's brief gives, and the deviation is recorded rather than silently corrected; if the owner prefers the convention, the rename is a one-line change to this document's and the plan's links.

## 5. Corrections to the brief that accompanied the spec

The brief that carried the spec made four factual claims about this repository that `origin/main` does not support. They are recorded here because a plan built on them would be wrong, and because the honest form of "the tree says otherwise" is a citation.

**5.1 — "the console design 59" is right, and the v1 design's check number is real.** `2026-10-15-desktop-console.md`'s check register reserves **58** for PR #393 (open, not on `main`) and **59** for itself, written as two sections 59a and 59b because the second needs a published feed to run. `docs/design/artifact-runs.md` also writes a Check 58 for itself (its §11 is "Check 58, in outline"), which is the same contention the console plan's register describes and resolves the same way: **whichever of the two merges second renumbers**.

**5.2 — "0.8.0 is cut after phases 1 and 2" is not in this tree.** The release train recorded in this repository is **R4 of `docs/plans/2026-10-14-project-centric.md`**: phase 0 lands on `main` and **phases 1–6 land on `next`, released together as 0.8.0**. The highest release in `docs/releases/` is `v0.7.5.md`, so 0.8.0 is indeed the next train — but it is the *whole* project-centric milestone, not a cut after two of its phases. The spec's example versions and this document's D37 are consistent with the tree's version of that fact, not the brief's.

**5.3 — "the budget-token channel 60, the monorepo folder export 61" do not exist on `origin/main`.** The highest live-check section written in `docs/gcp-live-checklist.md` is **`## Check 47`** and the highest summary row is **`| 57 |`**; 48–56 are reserved in the project-centric plan's phase table, 57 is written as a row whose section is elsewhere, 58 is #393's, and 59 is the console plan's. **No `Check 60` and no `Check 61` appear anywhere in `docs/`**, and neither "the budget-token channel" nor "the monorepo folder export" is a reserved number: the token channel is **phase 3b** of the project-centric plan (a design task, with no check number), and the nearest thing to a "folder export" is that plan's phase 6 `fugaro project export`, whose check is **Check 56**. This document therefore reserves **60** for v2's owner-run check (§4 of the plan), states the reservation rule the tree already uses, and records that if the owner holds 60 and 61 for work not in this tree, v2's number moves to **62** — the same "whichever merges second renumbers" rule, applied honestly to a reservation this tree cannot see.

## 6. UNVERIFIED

| # | Claim | Why it is unverified | What would settle it |
|---|---|---|---|
| U1 | That a realtime-database command queue can be made safe with rules alone | This design's argument against the queue (F11) is an argument about claims, consumers and replay, not about Firebase rules; the rules could in principle be written, and nothing in this tree models them | a design that names the consumer, the claim and the ack, and a rules test — not a probe |
| U2 | What an orchestrating session's identity would be | There is no session anywhere in the tree (F14), and the per-run token (`{fs, fr, fx, fp, rb}`) is minted by a launcher for a job | the answer to D24, which needs the owner |
| U3 | The size of a changed-file set for a large run, and whether a bounded record is enough | Nothing records one today (F17), so there is no measurement | V2.1's task, on the sandbox installation |
| U4 | Whether the plan artifact is the right home for the graph, or whether the graph needs its own file | It depends on how the skill's output is reviewed (D25/D36) and on whether a plan's task list stays prose | the owner's answer to D25, and one plan written with the skill |
| U5 | Whether `fugaro project export` (Check 56) will carry per-task versions when it lands | Phase 6 is reserved and not built; its design does not name a version field | the project-centric plan's phase 6 design |
| U6 | What the four colours should do for a task whose state is unknown | "needs attention" has no definition in this tree (F4) | a definition, which is a decision and not a measurement |
| U7 | Whether the OS notification surface on macOS can be constrained to a fact-only body in practice | No notification mechanism exists here to test (F27), and the platform's behaviour is not stated in this tree | V2.7's task, on a real Mac, owner-run |
