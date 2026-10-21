# Central orchestrators — the reconciliation

**Status: reconciliation only; nothing is implemented and no plan is approved.** This
directory holds the owner's spec for **central orchestrators** (`spec.md`, verbatim), the
reconciliation of that spec against the tree at `origin/main` **`2f8b4dca`** — the merge base
this branch sits on — and the questions the reconciliation could not answer on its own, put
to the owner as **D1–D26**, each with its recommendation first. **Where this document
disagrees with `spec.md`, it wins.** No Go, no TypeScript, no Terraform, no rules change and
no live action follows from it — the plan's guard test (P0,
`internal/cli/docs_central_orchestrator_test.go`) is the one piece of Go this work has
written, and it implements no part of the orchestrator. **Six of the twenty-six decisions —
D1, D2, D3, D6, D12 and D19 — were answered by the owner on 2026-10-20, each as its
recommendation, and an answer is not an approval** (reconciliation §5, §6). **Owner: Dimi
Paun. Date: 2026-10-20.**

Every path and symbol below was read in this tree at `2f8b4dca`, and each is cited by path
and **symbol** — never by line number, because the lines move while a document sits in
review. Two sources are **not on `main`** and are cited with that stated: the always-on
orchestrator design (**PR #632**, branch `pr632`, at `docs/mobile/design/always-on-orchestrator.md`)
and the rigor modes documents (**PR #773**, branch `pr773`, at `docs/rigor-modes/`). Where a
citation names a document rather than a symbol, it names the heading or the phrase the
sentence turns on, for the reason `docs/coordinator/README.md` gives for its own citations.

## 0. Corrections to the brief this work was written from

- **C1 — "docs only" means what it says.** The brief this work was written from asks for a
  spec, a reconciliation and a plan, and says no code, no Terraform and no workflow is
  touched. The pull request that wrote these three documents added four Markdown files under
  `docs/central-orchestrator/` and nothing else. In particular it wrote **no guard test**:
  the folder-per-work convention in this tree is a guard package beside the CLI —
  `ls internal/cli/docs_*_test.go | wc -l` is **49** at `2f8b4dca`, from
  `internal/cli/docs_artifact_runs_test.go` to `internal/cli/docs_windows_cli_test.go` — and
  this folder's own guard was listed as **P0** in the implementation plan instead, to be added
  by a later code-bearing pull request. **That pull request is the one that landed P0**:
  `internal/cli/docs_central_orchestrator_test.go` is in the tree now — the fiftieth such file
  (`ls internal/cli/docs_*_test.go | wc -l` is **50** on this branch), and the one piece of Go
  this work has written — and it added no feature code, so this correction still holds of
  everything the orchestrator would have to be built from. Nothing else needed registering:
  there is no
  docs-folder registry (`internal/clidoc/` holds the CLI reference generator and
  `internal/cli/docs_base_test.go` the shared helpers), so no existing test names this folder
  and none fails for its absence.
- **C2 — "Firebase" is two stores in this tree, and the spec's word covers both.** The
  budget database is the **Realtime Database**, read through `internal/rtdb/`
  (`internal/rtdb/client.go`'s `Get`, `GetETag`, `PutIfMatch`, `Patch`, `ServerNow`, and
  `internal/rtdb/stream.go`'s `Stream` / `Event`).
  Firestore is a separate, hand-written REST client (`internal/firestore/client.go` `Get`,
  `Patch`, `Query`, `Delete`) used for the budget's daily rollover
  (`internal/budget/rollover.go` `Roller`) and nothing else. The spec's lease is in "Firebase"
  and its session memory is in Firestore; both are named explicitly in D1 and D11 below.
- **C3 — "the security service" is a document, not a service.** `docs/credentials-service/`
  says it twice: *"plan only; nothing is implemented"* (its `README.md` status line) and
  *"Boundary: the service stays secrets-only. Other always-on features (e.g. the phone client
  backend) go in sibling services that call it"* (`spec.md`, "Shape" and "Boundary"). Every
  dependence the spec places on it — *"project-level keys or OAuth references from the
  security service"*, *"a project service identity with its own cap and credentials, held by
  the security service"*, *"its own named credential, issued and revocable through the
  security service"* — is a dependence on unbuilt work. This is the single largest ordering
  fact in this reconciliation, and it is **#21** below.
- **C4 — and so is "the coordinator".** `docs/coordinator/` is *"reconciliation only; nothing
  is implemented and no plan is approved"* (`README.md`), and its spec is explicit that the
  coordinator never acts: *"Advisory, not authoritative … It never blocks or reorders
  merges"* (`spec.md`). The spec's *"Every orchestrator, local or central, reports to the
  coordinator in the same way"* therefore describes a reporting path with no reader.
- **C5 — the spec's own follow-up is not on `main`.** The bullet *"update
  fugaro-mobile-app-spec.md so QR pairing is described as discovery only"* names a file the
  tree does not have. The mobile spec is `docs/mobile/functional-spec-v0.1.md`, the rulings
  that already fixed the confusion are D165, D166, G12 and T5 in
  `docs/mobile/functional-spec-reconciliation.md`, and the exact edits it still needs are
  listed in **§4** below and are **not made** by this pull request.
- **C6 — the rigor modes work has no escalation timeouts.** The brief says PR #773 carries
  *"escalation timeouts by rigor mode"*. It does not: `docs/rigor-modes/spec.md`,
  `reconciliation.md`, `implementation-plan.md` and `README.md` (PR #773) contain no
  mode-keyed timeout, no escalation concept, and no notification channel; its D15 explicitly
  declines to build an event source — *"it does not build a scheduler"*, *"any event source
  that reacts to a merge"* is deferred — and its D13 repeats the standing rule that *"Fugaro
  opens PRs and never deploys"*. So the spec's *"Escalation timeouts (resolved): … Hotfix
  waits 5 minutes, critical 15 minutes, and the other modes 1 hour"* introduces a table that
  has no home yet. The mode **names** match (`POC`, `Startup`, `Pro`, `Critical`, `Hotfix`)
  and the per-project/repo/area precedence does exist as a plan (**D6**, **D8** of the rigor
  modes work, resolving `max(project default, path floors, task request)` twice — at bootstrap
  and again against `runstore.Record.ChangedFiles`). The timeouts are **D8** below.
- **C7 — the brief's parenthetical, "spec's own follow-up says QR pairing is discovery only",
  is right, and it is one of five places the spec and the tree describe identity differently.**
  The others are the allowlist's key (an email in the spec, a **device node** today), the
  sign-in methods (Google and Microsoft SSO in the spec, **custom tokens only** today), the
  merge authority (the spec has the orchestrator carrying PRs *"through to merge"*, the tree
  never merges), and the resident agent (the spec puts the agent in the service, PR #632 and
  mobile D115 keep it out of the resident process). Findings **#27**–**#34**, **#46** and
  **#56** below.

## 1. The spec's concepts against what the repository has

| The spec says | What the tree has, at `2f8b4dca` | Verdict |
| --- | --- | --- |
| "one Cloud Run service per central orchestrator" | Every Cloud Run workload in `deploy/terraform/gcp/modules/` is `google_cloud_run_v2_job`; `internal/infra/tf/cover.go`'s `allowedTypes` lists `google_cloud_run_v2_job` and `google_cloud_run_v2_job_iam_member` and **not** `google_cloud_run_v2_service` | **Wrong today; a decision the code enforces** (#1, D19) |
| "Clients connect directly over WebSocket" | No WebSocket client or server anywhere in the tree; the CLI's only HTTP server is the loopback gateway (`internal/gateway/gateway.go`'s `Start`, `127.0.0.1:0`) | **Gap** (#3, D5) |
| "the orchestrator holds a lease in Firebase and renews it on a heartbeat" | No lease record; a launch claim (`internal/runstore/launch.go` `Claim`), a branch lock (`internal/lock/lock.go` `Holder`) and a per-run budget heartbeat (`internal/budget/registry.go` `Session.heartbeat`) | **Wrong as written / buildable** (#6–#14, D1–D3) |
| "all shepherding state must live in Firebase" (open item) | Runs bucket objects carry the shepherding record; RTDB carries the budget heartbeat and the counters; Firestore carries the daily rollover | **Answered in §2** |
| "Every job has an owner, which determines the spending cap" | Caps are keyed to the repository slug and the project-global node (`internal/budget/model.go` `Caps`, `RepoCaps`, `GlobalCaps`, `Caps.PerRun`); no cap is keyed to a person or a service identity | **Wrong** (#18, D18) |
| "the credentials … from the security service" | `docs/credentials-service/` is a document; the run's own budget identity exists and is sealed (`internal/budget/token/` `Seal`, `Unseal`, `Claims`, `UID`) | **Gap, blocked** (#21, D4) |
| "carries PRs through to merge" | `docs/design/v1.md` §2, non-goals: *"Auto-merge. Fugaro never merges."* `internal/gitprov/gitprov.go`'s `Provider` has `EnsurePR`, `ApplyReady`, `PullRequest`, `Comments` and no merge | **Wrong** (#22, D18) |
| "approving a merge" as a locked action | The only merge path is the paired-device one: `shared/confirm/index.js` `DESTRUCTIVE_VERBS`, the rules' `action` node, `internal/mailbox/message.go` `Action`; its executor is plan task P6.7 and is not built | **Exists as schema only** (#23, D7) |
| "checks the verified email against the project allowlist" | The allowlist is the **device node** `config/admin/devices/$uid` (`internal/budget/rules/rules.go` `userClaim`; mobile D166); keyed to a Firebase uid, not an email | **Wrong** (#31, D6) |
| "Google SSO and Microsoft SSO are supported, and email and password …" | No public sign-in provider exists: `internal/cli/init_firebase.go`'s `refusePublicSignUp` refuses the configuration, and `internal/infra/identityplatform.go`'s `PublicSignUp` is what reads it; the web key is restricted to `identitytoolkit`/`securetoken` (`deploy/terraform/gcp/modules/firebase/firebase.tf`) | **Wrong** (#32, D6) |
| "phone app push and desktop notifications" | Push is deferred (mobile D121, *"no push in v1"*); `docs/mobile/design/push-notifications.md` is design-only and its sender would be the history job | **Gap** (#34, D21) |
| "one shared session … saved to Firestore after every turn" | The conversation is per **run**, in the runs bucket (`internal/runstore/session.go` `SessionMeta`, `PutSession`, `ReadSession`); Firestore holds `meta` and `spendDaily` only (`internal/infra/firestore.go`) | **Gap** (#52, D11) |
| "a Firestore TTL policy" | The Firestore client has no TTL API (`internal/firestore/client.go`) and no Terraform resource in `deploy/terraform/` sets `ttl_config` | **Gap** (#53, D11) |
| "raw history is periodically archived to the project's Git repo" | Fugaro writes to Git only through PRs and the job's git credential; there is no committer that is not a run | **Gap** (#54, D25) |
| "an agent … reads the logs, analyses the problem, proposes a fix" | A run is headless with every permission prompt off (`internal/agent/agent.go` `Args`' `--dangerously-skip-permissions`; `internal/runner/prompts.go` `SystemPrompt`) | **Exists** (#35, D4) |
| "Every orchestrator … reports to the coordinator" | No coordinator code, store or endpoint exists | **Gap** (#51, D14) |
| "Off by default … scale-to-zero" | The budget backend is off by default too, and the managed-plan cost gate already reads a service's `min_instance_count` (`internal/infra/tf/managed_cost.go`) | **Exists in shape** (#4, D13) |

## 2. The spec's own open item, answered from the code

The spec asks, under Handoff: *"Open item: check how much state the orchestrator persists
today."* Below is the answer, in the terms the code uses. The short version: **the
shepherding record of a run already lives outside the agent, in the runs bucket, and the
only thing in Firebase about a live run is its budget heartbeat.** What does not exist is
anything a *second* orchestrator could pick up: no prompt, no decision, no approval, no
session-level conversation and no lease.

**Where a run's state lives today**

| Where | Object / node | Written by | What it is |
| --- | --- | --- | --- |
| Runs bucket | `task.json` | `internal/runstore/runstore.go` `Store.CreateTask`, `WriteTask` | the input spec (`task.Spec`) |
| Runs bucket | `launch.json` | `internal/runstore/launch.go` `Store.WriteLaunch`, `Launch` | proof of launch: backend, execution, job, log URL, `LaunchedBy` |
| Runs bucket | `launching` | `internal/runstore/launch.go` `Store.Claim`, `ClaimKey`, `Claim` | the launch claim: `Holder`, `At` |
| Runs bucket | `result.json` | `internal/runstore/runstore.go` `Store.CreateRecord`, `Record`; `internal/runstore/replace.go` `ReplaceRecordIf` | the run record; ownership is its `Execution` field |
| Runs bucket | `cancel` | `internal/runstore/runstore.go` `Store.RequestCancel`, `CancelRequested` | the cancel marker |
| Runs bucket | `session/session.json`, `session/<id>.jsonl` | `internal/runstore/session.go` `PutSession`, `ReadSession`, `SessionMeta` | the run's Claude Code conversation, for a follow-up's `--resume` |
| Runs bucket | `comments.json` | `internal/runner/followup.go` | the follow-up's PR comment snapshot (`followup.Snapshot`) |
| Runs bucket | `locks/<slug>/<branch-hash>` | `internal/lock/lock.go` `Acquire`, `Holder`, `Key` | one-run-per-branch: `RunID`, `Execution`, `ExpiresAt` |
| Runs bucket | `tokens/<slug>/<run>/budget-token` | `internal/budget/token/object.go` `PutObject`, `TakeObject`; `internal/cli/launch_budget.go` `mintBudgetToken` | the sealed budget identity (`token.Claims`, `token.ObjectKey`) |
| RTDB | `agents/<slug>/<run>` | `internal/budget/registry.go` `Session.Start`, `heartbeat`, `Finish` | liveness and the run's last action (`budget.AgentEntry`) |
| RTDB | `runs/<slug>/<run>` | `internal/budget/lease.go` `Session.grant`, `release`, `report` | the run's lifetime ledger (`budget.RunLedger`) |
| RTDB | `spend/<day>/…` | the same lease path | the day's counters (`budget.Counters`) |
| RTDB | `outcomes/<day>/<slug>/<run>` | `internal/budget/registry.go` `Session.Finish`; `internal/budget/sweep.go` `Sweeper.remove` | the run's one outcome (`budget.Outcome`) |
| RTDB | `config/…` | `internal/cli/budget.go`; `internal/budget/kill.go` `SetKill` | caps, limits, mode, kill switches |
| Firestore | `spendDaily/<date>_<slug>` | `internal/budget/rollover.go` `Roller` | the durable daily history (`budget.DayRecord`) |
| Firestore | `meta` | `internal/infra/firestore.go` | the installation marker |

**The five things the spec needs, and where they are**

1. **A lease.** Does not exist as a record. The nearest is the launch claim: one per run, held
   for `runstore.ClaimTTL` (10 minutes), judged stale against the **holder's own clock**
   (`internal/cli/run.go` `takeClaim` computes `now.Sub(cur.At) < claimTTL` with a local
   `time.Now()`), taken over with a generation-matched `blobx.ReplaceIf` — never
   delete-then-create — and released early only on a refused launch (`internal/cli/run.go`
   `releaseClaim`). The code states its own assumption, and it is a skew bound:
   *"Every CLI's clock must be within claimTTL - holdTimeout (5m) of every other's … The
   object's server-side update time would narrow that … and is not used. If the assumption
   fails, the result is a double launch, which the runner's duplicate-execution check
   contains."* The holder is `host/os.Getpid()/rand` (`internal/cli/run.go` `claimHolder`),
   **not an identity**.
2. **A shepherd.** Does not exist. `internal/runview/runview.go`'s `Row.LaunchedBy` is
   explicitly untrusted display text; the budget token's `RB` claim is *"the launcher's own,
   self-asserted name … attribution only, nothing in the rules trusts it"*
   (`internal/cli/launch_budget.go` `mintBudgetToken`).
3. **A prompt or a decision.** Does not exist in Go. `internal/mailbox/message.go` defines
   `Message`, `Action`, `Confirmation`, `Heartbeat` and the validators `ValidateMessage` /
   `ValidateHeartbeat`, and **no Go file imports the package** — it is the Go mirror of the
   JavaScript mailbox schema, kept so the rules and the phone agree. There is no Go prompt
   queue, no decision store, no escalation store and no push store.
4. **A shared conversation.** Does not exist. The only conversation storage is per run
   (`internal/runstore/session.go`), accompanied by `internal/agent/session.go`'s
   `SessionDir`, `ValidSessionID`, `ErrNoSession` and resumed through `internal/agent/agent.go`'s
   `Request.SessionID` / `Resume` and `internal/runner/session.go`'s `restoreSession`.
5. **A liveness heartbeat.** Exists, for a run, in RTDB: `internal/budget/registry.go`'s
   `Session.Start` / `heartbeat` / `Finish` patch `agents/<slug>/<run>`, and
   `internal/budget/sweep.go`'s `DefaultStaleAfter` sweeps entries whose heartbeat stopped.
   It is a **liveness** record, not a lease: nothing takes a job over when it goes stale; the
   sweeper only marks `crashed` and deletes the entry.

**The second half of the invariant already exists.** Two orchestrators cannot both be the
same run's *runner*: `internal/runner/runner.go`'s `claimRecord` makes one execution the owner
of `result.json`, `internal/runner/lockcache.go`'s `ErrDuplicateExecution` exits the loser
without writing anything, and ownership is decided by
`internal/backend/codec.go`'s `SameExecution` (parsed execution names, not string equality).
The spec's *"one job, one shepherd"* is therefore about the **new** thing — the shepherding
seat above the run — and not about the run itself.

## 3. Findings

Each finding is classified **Wrong / Exists / Gap / Rule / Open**, cites a path and a symbol,
and states its consequence. Numbers are this work's own and are never reused.

### A. The service surface and Cloud Run

**#1 — There is no Cloud Run *service* in this repository, and the installation's own resource allow-list refuses the type. — Wrong.**
Every Cloud Run workload in `deploy/terraform/gcp/modules/` is `google_cloud_run_v2_job`
(`modules/project/job.tf`, `modules/workflow/job.tf`, `modules/repo/check.tf`,
`modules/installation/history.tf`). A repo-wide search for `google_cloud_run_v2_service`
finds no resource, only cost-rule rows (`internal/infra/tf/managed_cost.go`) and a state
account mapping (`internal/managed/stateaccounts.go`). The deciding symbol is
`internal/infra/tf/cover.go`'s `allowedTypes`: it lists `google_cloud_run_v2_job` and
`google_cloud_run_v2_job_iam_member`, and a plan holding any other type is refused by
`Cover.NotCovered` with *"a resource type outside the installation's modules"*, which makes
`internal/cli/init_review.go`'s `notCovered` ask for a typed confirmation. `managedTypes` in
`internal/infra/tf/managed_cover.go` is the narrower list for managed projects — **14 entries
to `allowedTypes`' 29**, every one of them also an entry there, as that file's own comment
says (*"Every entry is also an entry of allowedTypes"*, held by its
`TestManagedAllowedTypesIsDecided`) — so a service type has to be admitted to **both**.
*Consequence:* the spec's one Cloud Run service per central orchestrator is, in this tree, a
change to a list that exists precisely to make such additions deliberate. It is **D19**, and
the reconciliation treats it as a prerequisite rather than a detail. The coordinator's own
reconciliation reached the same fact as its finding **#2** (its #1 is the
project-and-installation finding).

**#2 — The CLI's only HTTP surface is a loopback listener inside a job. — Exists.**
`internal/gateway/gateway.go`'s `Start` (a package-level function, not a method) listens on
`127.0.0.1:0` (`lc.Listen(ctx, "tcp", "127.0.0.1:0")`) and the returned `*Server`'s `URL`
returns that loopback URL. There is
no ingress, no public endpoint and no service account that serves requests in this tree.

**#3 — There is no WebSocket anywhere, in either direction. — Gap.**
No WebSocket client or server exists in the tree; the only `websocket` strings are test fences
(`mobile/src/demo/safety.test.js`, `console/test/unit/spawnlog.test.ts`). The RTDB client's
realtime transport is outbound SSE (`internal/rtdb/stream.go`'s `Stream` and `Event`), and the
console's renderer cannot open a socket at all (#30).

**#4 — The cost gate already understands a service's scale shape. — Exists.**
`internal/infra/tf/managed_cost.go`'s tables read `google_cloud_run_v2_service` with its
`template.0.scaling.0.min_instance_count`, so a managed plan holding a service is priced
rather than refused for being unknown. *Consequence:* the "scale-to-zero" half of the spec has
a precedent to follow and needs no new cost machinery (D13).

**#5 — The trust boundary is persons and groups, and reach-based membership is refused. — Rule.**
`deploy/terraform/gcp/modules/compute/variables.tf`'s `launchers` / `operators` validations
require `user:`/`group:` (and `serviceAccount:` for operators) and refuse `domain:`,
`allUsers` and `allAuthenticatedUsers`; `internal/cli/doctor_launcher.go`'s `publicCanStart`
flags any job startable by `allUsers`/`allAuthenticatedUsers`. `internal/cli/init_members.go`'s
`defaultMembers` derives members from `authenticatedUser`, which requires a **verified email**
on the CLI's own ADC path. *Consequence:* a shared, internet-facing orchestrator must be
authorised by its own allowlist and never by project-wide reach; the console/phone identity
work (D5, D6) is where that lives, and IAM alone cannot express it.

### B. The lease, and one job, one shepherd

**#6 — "The orchestrator holds a lease in Firebase" has no referent: there is no lease record, and the launch claim is a different thing. — Wrong.**
`internal/runstore/launch.go`'s `Claim` (`Holder`, `At`) is the launch claim: it exists to
make *launching* at-most-once, it is never deleted after a successful launch, and it carries
no identity. The branch lock (`internal/lock/lock.go` `Holder{RunID, Execution, ExpiresAt}`)
is per branch, not per job-shepherd. The budget's "lease" (`internal/budget/lease.go`
`leaseSource.Grant`) is an amount of money. Nothing in the tree says who is shepherding a job.

**#7 — The takeover protocol the spec needs already exists in two places, and both are generation-matched compare-and-set. — Exists.**
`internal/cli/run.go`'s `takeClaim` reads a held claim **once**, judges staleness on that
content, and takes it over with `blobx.ReplaceIf` matched to that same generation —
*"never delete-then-create, so two takers can't both win"*; the pattern to copy, and the
reason D1 recommends a generation/version field rather than a timestamp-only record.
`internal/lock/lock.go`'s `Takeover` / `ErrHolderChanged` does the same for the branch lock
and adds the one signal a launcher cannot forge: `Stale` requires
`holder.Execution != "" && executionTerminal`, confirmed by the backend.

**#8 — `internal/rtdb` has conditional writes but **no transaction**. — Rule.**
`internal/rtdb/client.go`'s `GetETag` (an `X-Firebase-ETag` read) and `PutIfMatch`
(`If-Match`, returning `ErrPrecondition`) are the compare-and-set; `Patch` is an atomic
multi-location update (*"Either all of them are written or, if any is denied, none"*). There
is no `Transaction` function anywhere in the package, and no server-timestamp write transform.
*Consequence:* a lease that must do read-modify-write atomically is built from `GetETag` +
`PutIfMatch` or from a single `Patch` whose rule equation is the check — never from a
transaction (D1, D2).

**#9 — The RTDB client knows the database's clock, and the ownership paths do not use it. — Exists / Open.**
`internal/rtdb/client.go`'s `ServerNow` is *"the database server's current time: the Date
header of the response to the most recently sent request … advanced by the local monotonic
time elapsed since that response arrived"*. Its users are the budget day and the watch view
(`internal/budget/session.go` `Session.now` — *"now is the database's clock when it has told
one, else the local clock"*; `internal/watch/source.go` `Supervisor.now`). The claim and the
lock use `time.Now()`, with the stated 5-minute skew assumption (#6, §2). *Consequence:* the
central orchestrator's lease can be skew-free **because** `ServerNow` exists — and the
existing claim cannot be retrofitted to it without changing a protocol two CLIs already share
(D3).

**#10 — There is no fencing token, no epoch and no generation field on any ownership record. — Gap.**
The only fencing primitive is the object generation: `internal/blobx/blobx.go`'s `Create`,
`ReplaceIf` (*"overwrites key with data only if it is still generation gen"*) and `DeleteIf`,
plus `internal/runstore/replace.go`'s `RecordVersion` / `ReadRecordVersion` /
`ReplaceRecordIf` for `result.json`. A stolen-but-not-yet-expired lease cannot be fenced by a
number today (D2).

**#11 — The existing heartbeat is a liveness record with an independent sweeper, and its clock handling is explicit about skew. — Exists.**
`internal/budget/registry.go`'s `Session.heartbeat` patches the registry entry;
`internal/budget/sweep.go`'s `DefaultStaleAfter` is *"how long an entry may go without a
heartbeat"*; `futureSkew` bounds how far ahead of the sweeper's clock a heartbeat may be.
*Consequence:* the grace-period mechanism has a working model to copy, but it currently
**deletes nothing but the entry** — it never hands work to anyone (D2).

**#12 — The spec's grace period and the heartbeat interval are the only clock inputs, and 10 minutes is already the number the tree uses. — Exists.**
`runstore.ClaimTTL` is 10 minutes; the mailbox's consumer lease in the mobile design is 10
minutes with a 5-minute clock-skew band (`shared/lease/index.js`, `takeLease`, `CLAIM_TTL`,
`MAX_CLOCK_SKEW`, cited in PR #632's §3.1 and §4.9). Three unrelated mechanisms now propose
10 minutes; D2 recommends one number, one source.

**#13 — A run cannot be attached to, and cannot be resumed live. — Gap.**
`internal/cli/run_attach.go`'s `attachLaunch`, `attachInputs`, `attachJudge` attach **files**
(`--attach`), refused unless the launch resolves to a `mode: artifact` recipe
(`checkAttachRecipe`); a repeated `--run-id` reports `already-launched`
(`internal/cli/run.go` `existingLaunch`, and `waitForLaunch` waits for a racing launcher's
`launch.json`). *Consequence:* the spec's *"Return … can take the job back or leave it with
the central one"* assumes a job object that can change hands mid-flight; the run itself is
fire-and-forget (D15).

**#14 — What does exist is a conversation that can be resumed, and it is stored per run. — Exists.**
`internal/agent/agent.go`'s `Request.SessionID` / `Resume` (the CLI passes `--resume <id>`),
`internal/runner/session.go`'s `restoreSession`, `internal/followup/prompt.go`'s
`PromptData.Resumed`, and the stored files in `internal/runstore/session.go`. *Consequence:*
the raw material for "state in Firebase, not in the local agent's context" is a session file,
not a conversation store — the shared session is new work (D11).

**#15 — Nothing records who took a job over, and the one attribution the tree carries is display text. — Gap.**
`internal/runview/runview.go`'s `Row.LaunchedBy` is what `fugaro ls` and the console show, and
`internal/cli/launch_budget.go`'s `mintBudgetToken` says what it is: the token's `RB` claim is
*"the launcher's own, self-asserted name … attribution only, nothing in the rules trusts it"*.
There is no record of a hand-off and no place to write one, which is the spec's *"an audit
trail"* half of Multi-user access. *Consequence:* the lease's holder must be an identity the
**service** authenticated (D5) and not a name copied out of a token, and the audit trail is a
store this work has to build (D7).

**#16 — A lease's staleness signal is weaker than the branch lock's, and that is a property of the two mechanisms rather than an oversight. — Rule.**
`internal/lock/lock.go`'s `Stale` needs more than a timer: `holder.Execution != ""` **and**
`executionTerminal`, a fact the backend confirms — `internal/runner/runner.go`'s
`errOwnerUnknown` is the case where it cannot be confirmed, and the lock refuses to take over
without it. A lease has no such oracle: the only signal is that heartbeats stopped
(`internal/budget/registry.go`'s `Session.heartbeat`, `internal/budget/sweep.go`'s
`DefaultStaleAfter`). *Consequence:* D2's grace period is judged on the heartbeat alone and a
takeover cannot ask *"has the run actually ended"*; that is why the recommendation is a
generation compare-and-set and not a terminality check, and why the branch lock should stay
where it is rather than be generalised into the lease.

**#17 — A run's lifecycle is the run's to write, so an orchestrator's authority over a live job is a request, not a state. — Exists.**
`result.json` is written by the run: `internal/runstore/runstore.go`'s `Store.CreateRecord` /
`Record`, guarded for the owner by `internal/runstore/replace.go`'s `ReplaceRecordIf` and
`internal/backend/codec.go`'s `SameExecution` (`internal/runner/runner.go`'s `claimRecord`).
The one lever from outside is the cancel marker — `internal/runstore/runstore.go`'s
`RequestCancel` / `CancelRequested` — which is **a request the run acts on**, not a state the
requester sets. *Consequence:* the spec's *"take the job back"* and *"carrying PRs through"*
both assume a component that drives a job; today the only components that can are the run and
its backend, so an orchestrator can launch, request a cancel and read (D7, D15).

### C. Ownership, caps and credentials

**#18 — No cap is keyed to a person or a service identity. — Wrong.**
`internal/budget/model.go`'s `Caps` reads `/config`: `GlobalCaps`, `DefaultCaps`, `RepoCaps`
and `Limits`, with `Caps.PerRun()`, `Caps.RepoDaily()`, `Caps.GlobalDaily()`,
`Caps.MaxReserve()`. `internal/budget/evaluate.go`'s `Evaluate` is *"the pure mirror of the
database rules"* and `internal/budget/precheck.go`'s `Precheck` is the launch gate; both key
on the slug. The one per-identity cap is the local file's committed `per_day_usd`
(`internal/budget/evaluate.go` `Write.PolicyCap`, `CommittedDaily`) — *"the rules cannot see
git, so only the client enforces it"*. *Consequence:* *"Every job has an owner, which
determines the spending cap"* needs a new key (D18).

**#19 — A run's Firebase identity is its own uid, minted per run and revoked when it ends. — Exists.**
`internal/budget/token/token.go`'s `Claims`, `UID(slug, run)` = `r~<slug>~<run>`, `ErrRevoked`
(*"identity revoked"*); minted by `internal/budget/token/mint.go`'s `Mint` via `Signer` /
`IAMSigner`, sealed and unsealed on `main` (`internal/budget/token/seal.go` `Seal`, `Unseal`;
`internal/budget/token/object.go` `PutObject`, `TakeObject`). *Consequence:* a **long-lived**
service has no such identity, and the coordinator's reconciliation already recorded that
consequence for its own case; the same holds here (D4, D18).

**#20 — The seal's own limits are documented, and they bound what a service may be given. — Rule.**
`SECURITY.md` states what a launcher can reach, that a run is an untrusted agent, that
*"/proc/<pid>/environ"* reaches the seal key, and that a shared project job's account reaches
every repository's bucket objects. `docs/design/budget-token-channel.md` is the design. Any
credential handed to a central orchestrator inherits this analysis (D4).

**#21 — Every credential path the spec names runs through unbuilt work. — Gap.**
`docs/credentials-service/` is plan-only (`README.md`), its D134 gives a run the *existing*
budget identity, D135 routes credentials through the in-runner **gateway**, D143 makes the
service *"secrets-only"*, D145 lists what it holds (model credentials and the GitHub App key)
and **excludes** workflow secrets, and D147 makes it opt-in per installation. `docs/design/project-centric.md`
§7's "OAuth tokens (phase 5)" is the one place a **reference** is described — *"A record
{account, owner, secret ID, allowed profiles/repositories} in the policy … It holds no
value"* — and `docs/project-claude-accounts.md` §10 calls today's carrier *"a stopgap"* whose
blast radius is the account. *Consequence:* *"project-level keys or OAuth references from the
security service"* is a promise about a service that does not exist, and the automated
submitter's *"named credential, issued and revocable through the security service"* is a
capability the service's own plan does not have (it has no issuance API for third parties) —
D4 and D18.

**#22 — Fugaro never merges, and the spec's orchestrator does. — Wrong.**
`docs/design/v1.md` §2, non-goals: *"Auto-merge. Fugaro never merges."*; the provider
interface `internal/gitprov/gitprov.go`'s `Provider` exposes `EnsurePR`, `ApplyReady`,
`PullRequest`, `Comments` — no merge; the CLI registers no merge command
(`internal/cli/root.go`). *Consequence:* the spec's opening sentence (*"carries PRs through to
merge"*) and its action lock (*"approving a merge"*) require the owner to lift a standing
non-goal, or the central orchestrator joins the existing rule (D18).

**#23 — The only merge path in the tree is a paired device's spoken confirmation, and half of it is unbuilt. — Exists (schema) / Gap.**
`shared/confirm/index.js`'s `initialState` / `step` / `readBack` and `DESTRUCTIVE_VERBS`,
with the rule *"one confirmation authorises exactly one action"*; the rules' `action` node
validates `verb ∈ {cancel, merge, confirm}` and `head_sha` for a merge
(`internal/budget/rules/rules.json.tmpl`); `internal/mailbox/message.go`'s `Action`,
`Confirmation`, `ValidateMessage`, `actionField` say *"the consumer runs `fugaro cancel RUN`
or `gh pr merge <n> --repo <owner/name>` from these fields and nothing else"*. The consumer is
plan task P6.7 and `fugaro pair` is P6.6, both unbuilt (`internal/cli/project_cmd.go`: *"'fugaro
pair' is plan task P6.6 and is not built"*); the MAC exists only in JavaScript
(`shared/mailbox-schema/mac.js` `macOf`, `verifyMac`). *Consequence:* a central orchestrator's
merge authority would be a **third** path unless it reuses this one (D7, D18).

**#24 — The rules' actor model is the device node, and it is what makes a merge write possible at all. — Rule.**
`internal/budget/rules/rules.go`'s `userClaim` requires `auth.token.mu == auth.uid` **and**
`root.child('config/admin/devices').child(auth.uid).exists()`; `MAILBOX` adds `$mb == auth.uid`;
`MACOK` checks a tag's shape, *"never its value … P6.7 is what verifies one"*.
*Consequence:* any "authorised person" decision in the orchestrator has to say which claim it
means (D6).

**#25 — "Reviewer" exists three times in the tree and none of them is a merge authority. — Rule.**
`internal/followup/trust.go`'s `Trust` / `Allows` / `isSelf` (who may steer a follow-up —
*"an author listed in the base branch's followup.trusted who is also a collaborator … and
never the pull request's author"*), backed by `internal/config/config.go`'s `Followup.Trusted`;
`internal/config/config.go`'s `PRSettings.Reviewers` (GitHub review requests, applied by
`internal/gitprov/github/github.go`'s `ApplyReady`); `internal/config/config.go`'s
`RoleReviewer` (an LLM stage). The rigor modes work's D8 makes the trusted list the answer to
*who* may lower a floor and a merged config change the only mechanism (PR #773).
*Consequence:* the spec's *"the reviewer"* as a per-job field has three candidate meanings and
must name one (D18).

**#26 — The review-only mode already carries the tree's strongest author gate, and it is not a merge gate. — Exists.**
`SECURITY.md`'s paragraph on `mode: review` (`review-only`, `pick`): trusted-author-only, by
the author's **live** `author_association`, with `followup.trusted` explicitly playing no
part.

### D. Interaction, clients and notifications

**#27 — QR pairing is already ruled discovery-only in the mobile work; the *spec* that says so is the functional spec, and it still says too much. — Exists / Open.**
`docs/mobile/functional-spec-v0.1.md` §4.1 *"Adding a project: QR pairing"* carries *"Only
someone who already has access to the project (CLI or console) can generate a pairing code"*
— an authorisation claim inside the discovery section — and §4.2 *"Authentication and
authorisation"* carries *"Because anyone can authenticate with Firebase"*, which the mobile
reconciliation's G12 corrects: `internal/cli/init_firebase.go`'s `refusePublicSignUp`
refuses email, anonymous, phone and every default IdP, so *"no public sign-in provider;
custom tokens only"* (T5). D165 (two tokens bound to a phone-held key), D166 (the allowlist is
the device nodes) and G15/G16 (the pairing flow) are the rulings. **§4 lists the exact edits
and this pull request makes none of them** (D22).

**#28 — The phone app has no server component, no HTTP and no socket, and reads the database directly under a user-claim grant. — Gap.**
`mobile/package.json`'s dependencies contain no Firebase SDK and no networking library;
`mobile/src/feed/index.js` says of the feed *"A port. There is no database client in this
directory, and there must not be"*; `mobile/src/feed/reader.js`'s `FeedReader` takes an
injected subscribe port, and `mobile/src/feed/paths.js`'s `FAMILIES` are
`config/caps`, `config/kill`, `spend`, `mailbox` with `agents`, `outcomes`, `runs` and
`fugaro` in `DENIED_FAMILIES`; `mobile/src/mailbox/client.js`'s `createMailboxClient` is the
app's only writer; `mobile/src/demo/safety.test.js`'s `RULES` fence the network outright —
`fetch(`, `XMLHttpRequest`, `WebSocket`, `sendBeacon`, `axios`, and any `https?://` in a
non-test file. *Consequence:* *"the phone app … uses
the same protocol"* means adding a network client to an app built to have none, and the app
cannot see jobs today at all (D5, D12).

**#29 — The console is a sidecar with no network client and a CSP that forbids one. — Rule.**
`console/src/main/protocol.ts`'s `CONTENT_SECURITY_POLICY` carries `connect-src 'none'` —
*"the renderer never talks to the network, because every byte it draws arrives over IPC from
the main process"* — delivered as a real header through `serveAppRequest` on the `app://`
scheme; `console/src/main/ipc.ts`'s `CONSOLE_OPERATIONS` / `channelForOperation` /
`ALLOWED_OPERATION_FLAGS` and `console/src/preload/index.ts`'s `EXPOSED_OPERATIONS` make the
IPC surface a fixed list; `console/src/main/spawn.ts`'s `SPAWN_COMMANDS` / `argvFor` /
`startSidecar` bound what may be spawned. `docs/design/desktop-console.md` §5.1 rule 3 and
rule 6 are the design's statement of both. *Consequence:* the console can reach a WebSocket
only by a deliberate change to a load-bearing property — or through the CLI it already spawns
(`console/docs/json-contract.md`: `fugaro watch --json` is line-delimited JSON) (D5).

**#30 — The console's data plane is `fugaro watch --json`, and watch is read-only apart from an identity-gated kill. — Exists.**
`internal/cli/watch.go`'s `newWatchCmd` / `runWatch` / `watchStream` stream four RTDB paths
through `internal/watch/source.go`'s `Supervisor` (`paths()`: `config`,
`budget.PathSpendGlobal(day)`, `spend/<day>/repos`, `agents`), plus queued runs
(`internal/cli/watch_queued.go` `startQueuedSource`) and PR state
(`internal/watch.NewPRState`). The one write is `watchExec` / `watch.Execute` against
`budget.KillDB`, gated on `watchActor`, which is a name and not a credential (*"cannot tell
who you are: set user in the local config or git config user.email; nothing was changed"*).
*Consequence:* the console's "chat" surface is new, and the one existing write path is a
model for how not to authorise (D5).

**#31 — The allowlist is a device node keyed to a Firebase uid, not an email. — Wrong.**
Mobile D166: *"The allowlist is the device nodes under `config/admin/devices/$uid`"*; the
rules' `userClaim` reads exactly that node; `config/admin` is `false` to read and to write
(docs/gcp-live-checklist.md, Check 81's statement of the deployed document). Revocation is
deleting the node. *Consequence:* *"checks the verified email against the project allowlist"*
is a different authorisation model from the one the phone already uses, and the spec says the
two match (D6).

**#32 — No public sign-in provider exists, and the platform refuses to enable one. — Wrong.**
`internal/cli/init_firebase.go`'s `refusePublicSignUp` — the CLI's own refusal, reading the
configuration through `internal/infra/identityplatform.go`'s `PublicSignUp`;
`deploy/terraform/gcp/modules/firebase/firebase.tf`'s `google_apikeys_key.web` restricted to
`identitytoolkit.googleapis.com` and `securetoken.googleapis.com`; `docs/mobile/functional-spec-reconciliation.md`'s
G12 and T5. *Consequence:* Google SSO, Microsoft SSO and email+password are a reversal of a
standing platform rule, not a configuration (D6).

**#33 — Email verification is checked in exactly one place, and it is the CLI's own ADC path. — Exists (partial).**
`internal/cli/init_members.go`'s `authenticatedUser` / `userEmail` call
`https://openidconnect.googleapis.com/v1/userinfo` and require `email_verified`, refusing
service accounts — *"authenticatedUser is the email of the person the Application Default
Credentials belong to"*. Nothing in the phone, console or rules path checks a verified email,
because nothing signs in with an email. There is **no email-domain restriction and no
per-project sign-in-method policy anywhere** in the tree or in the mobile documents.
*Consequence:* the spec's last two auth bullets are entirely new surface (D6).

**#34 — Push is deferred, and "Fugaro's own notifications" has no sender. — Gap.**
Mobile D121: *"no push in v1"*; `docs/mobile/design/push-notifications.md` is *"design only;
nothing is implemented"*, its proposed sender is the history job
(`modules/installation/history.tf`'s `google_cloud_run_v2_job.history` plus its Scheduler
jobs), and its device-token node is to be *"a sibling of D166's device node,
`config/admin/push/$uid`"*. `docs/credentials-service/reconciliation.md`'s D141 defers alerts
*"once a notification channel and phase 5's owner record exist"*. *Consequence:* the
escalation path in the spec ends at a notification channel that does not exist (D21).

**#35 — A run is headless and cannot ask anything. — Exists / Gap.**
`internal/agent/agent.go`'s `Args` emits `-p --output-format stream-json --verbose
--dangerously-skip-permissions`; `internal/runner/prompts.go`'s `SystemPrompt` tells the agent
*"You are running unattended inside Fugaro, an ephemeral cloud worker. Nobody will answer
questions"*; `internal/agent/settings.go`'s `WriteManagedSettings` adds only a
`permissions.deny` for `deniedWebTools`. *Consequence:* *"When the agent needs input … it
raises a prompt"* describes a capability the runner deliberately removed, and *"the job pauses
only where it must"* has no pause (D8).

### E. Prompts, callbacks, escalation

**#36 — The Go mailbox is a schema with no importer, and it is the tree's only prompt/answer model. — Rule.**
`internal/mailbox/message.go`'s `Message`, `Kinds`, `Action`, `Confirmation`, `Heartbeat`,
`Agent`, `ValidateMessage`, `ValidateHeartbeat`, `CanonicalBytes`; no other Go package imports
it. The working implementation is JavaScript plus RTDB rules: `shared/mailbox/`,
`shared/mailbox-schema/`, `shared/prompts/`, `shared/lease/`, `shared/presence/`, and
`internal/budget/rules/rules.go`'s `MACOK`, `MAX`-length checks, `CONSUMER`, `PAIRING`.
`docs/mobile/implementation-plan.md`'s P6.7 owns the Go verifier and executor.
*Consequence:* a Go-side prompt/answer surface in the orchestrator can reuse this vocabulary
or invent a second one; D8 recommends reuse.

**#37 — There is no inbound HTTP surface to sign, verify or replay-protect. — Gap.**
The gateway is loopback (#2); there is no webhook receiver, no HMAC verifier in Go (#23) and
no HTTP endpoints at all. The spec's callback contract is new surface in both directions (D9).

**#38 — The prompt-queue policy the tree has is the mobile functional spec's, and it is a design. — Exists (doc).**
`docs/mobile/functional-spec-v0.1.md` §3.2 *"Voice prompts from agents"*, "Queueing policy":
*"Default: queue. Prompts are held rather than interrupting the user. Only the job waiting on
the answer pauses"*, with blocking issues interrupting immediately, and a TTL of 72 hours
(D118 as amended).

**#39 — Nothing in the tree escalates on a timer, and rigor mode is not a timer source. — Wrong.**
See C6. The rigor modes work defines findings handling per mode and a deterministic Hotfix
gate (its D12, D14, D16), refuses to build a scheduler (D15), and never mentions an
escalation timeout. `internal/localcfg/localcfg.go`'s `BurnAlertUSDPerHour` is a **display**
threshold passed to `internal/watch/build.go`'s `BurnAlertPerHour`, and a cap-proximity
threshold is explicitly recorded as absent — `docs/design/desktop-console-v2.md`'s **F29**,
*"cap alerts have data and no threshold"*, which **D35** then answers by putting the
threshold in the app rather than in the project record.
*Consequence:* *"Escalation timeouts depend on rigor mode"* is a proposal to the rigor modes
work as much as to this one (D8).

**#40 — What does escalate today is a person reading a PR: the trusted-comment follow-up. — Exists.**
`internal/cli/followup.go`'s `resolvePR` / `prSpec` / `storedFollowUp` / `recheckFollowUp`,
`internal/followup/trust.go`'s `Trust`, `internal/followup/select.go`'s `Select`,
`internal/followup/prompt.go`'s `ImplementPrompt`. The rigor modes work's D15 records an
automatic follow-up task for a Hotfix, printed rather than scheduled.

### F. Alerts

**#41 — No alert ingest exists: no webhook, no fingerprint, no de-duplication, no rate limit, no circuit breaker. — Gap.**
There is no Sentry **integration** anywhere in the tree — the only two Sentry strings are
both in tests and neither is code that could receive an alert: `SENTRY_DSN` is one of the
ordinary secret names `internal/config/reservedenv_test.go` asserts the reserved-environment
check must **allow** (it is not a name Fugaro reads), and `@sentry/react-native` is one of the
`COLLECTING_SDKS` `website/test/privacy.mjs` carries — *"the SDKs whose presence would make
the page's disclosure false"* — which is a privacy assertion about the app's dependency list,
not a client. There is also no inbound endpoint (#37). The nearest
once-only primitives are the rules' create-if-absent outcome
(`internal/budget/rules/rules.json.tmpl`, `!data.exists()`; `internal/budget/sweep.go`'s
`Sweeper.remove` — *"create only, so a recorded outcome is kept"*) and
`internal/budget/token/object.go`'s `PutObject` (`ErrObjectExists` — *"a replacement is an
identity swap"*). *Consequence:* every bullet of Alert storm protection is new (D10).

**#42 — There is no automated-jobs budget, and no record carries a trigger kind. — Gap.**
`internal/budget/model.go` has `GlobalCaps`, `DefaultCaps`, `RepoCaps` and `Limits` only; no
record distinguishes an alert-triggered job from a developer's (`budget.AgentEntry` carries
`RequestedBy`, which is a name, not a class). `schemas/fugaro.schema.json`'s `budget` block
has `mode`, `per_run_usd`, `per_day_usd`, `allowed_models` and nothing per-trigger.
*Consequence:* *"automated jobs have their own spending cap"* is a new axis on a cap model
that is keyed by repository (D10, D18).

**#43 — The kill switch is the only fleet-wide stop, and it is a human's. — Exists.**
`internal/budget/kill.go`'s `SetKill` (*"the one writer of a switch"*), `checkKills`,
`watchKill`, on `budget.PathKillGlobal` and `budget.PathKillRepo(slug)`. *Consequence:* the
circuit breaker's manual reset has a precedent to imitate, but the breaker itself is
automatic and new (D10).

### G. Prompt injection safeguards

**#44 — The runner already treats a run as untrusted, but it does not mark untrusted *input*. — Exists (partial).**
`SECURITY.md` says *"a run is already the untrusted party"* and, for review mode, that *"a
trusted author's own prompt injection (CLAUDE.md, a commit message, the diff itself) is still
code execution under `--dangerously-skip-permissions`"*. `internal/safetext/safetext.go`'s
`OneLine`, `MultiLine`, `Text`, `Field`, `FieldList` sanitise **display**, not a prompt
boundary. *Consequence:* *"clearly marked as data in the agent's context"* needs a convention
the tree does not have, and it must be written where the agent's prompt is built
(`internal/runner/prompts.go` `SystemPrompt`) (D4).

**#45 — A "restricted toolset" has one precedent, and it is narrow. — Exists (partial).**
`internal/agent/settings.go`'s `WriteManagedSettings` writes a managed settings file whose only
rule denies `deniedWebTools` (`WebSearch`, `WebFetch`) when a gateway is on. There is no
per-run tool allow-list, no per-run secret narrowing and no path-based tool refusal.

**#46 — "They see only the secrets the fix needs" is not expressible today: secrets follow the job (profile), not the fix. — Wrong.**
`docs/design/project-centric.md` §4.1 and `SECURITY.md`'s accepted D4/D13 paragraphs: one job
per (profile, provider, account), a shared project job's account holds the whole of `runs/`,
`cache/` and `locks/` and reads `tokens/` — *"a run on a shared job reaches every repository's
objects under those three prefixes"*, accepted by the owner on 2026-10-17.
`docs/project-claude-accounts.md` §10: *"every run of an account's job holds that account's
token, whatever the run does and whoever launched it — the account is the blast radius."*
*Consequence:* narrowing an alert job's secrets is a change to the profile model, not a flag
(D4).

**#47 — "No auto-merge, even in hotfix" is consistent with the tree and with the rigor modes work. — Exists.**
`docs/design/v1.md` §2 (#22); the rigor modes work's D13 (*"Fugaro opens PRs and never
deploys"*) and D11 (a Hotfix opens **ready**, under D12's deterministic gate, which is not a
merge); PR #632's Q11 answer: *"no merge in the cloud for the first phase-2 release"*.
*Consequence:* the spec's "Later improvement" (pre-building the hotfix) is out of v1 by its own
words, and the tree agrees (D16).

### H. Session memory

**#48 — Firestore is a server-side store: no client SDK may read or write it. — Rule.**
`internal/infra/firestore.go`'s `FirestoreRules` is `allow read, write: if false;` — *"nothing
is readable or writable by a client SDK. Only IAM principals … reach the database"* — and the
location is `FirestoreLocation` (`us-east5`), with `FirestoreMetaCollection` (`meta`) and
`FirestoreHistory` (`spendDaily`). *Consequence:* the shared session is reachable by the
service account only, so the console and the phone read it through the service, never directly
(D11).

**#49 — Firestore has no TTL, no transactions and no listeners in this tree. — Gap.**
`internal/firestore/client.go`'s surface is `Get`, `Patch` (with `MustExist`, `MustNotExist`,
`IfUpdateTime`, `Clear`), `Delete`, `Query`, `ListCollectionIDs`, `GetDatabase`,
`CreateDatabase`; there is no TTL field API, no transaction and no change stream, and no
Terraform resource under `deploy/terraform/` sets `ttl_config`. *Consequence:* the spec's
"expiry … enforced by a Firestore TTL policy" is one field plus one resource the repo does not
have, and the "only eligible once archived" rule cannot be enforced by a TTL policy at all —
a TTL policy deletes on time, not on a condition (D11).

**#50 — "Archive to the project's Git repo" has no committer that is not a run. — Gap.**
Fugaro reaches Git through a job's git credential and the GitHub App
(`internal/gitprov/`, `docs/git-providers.md`), and a run ends in a PR that a human
merges (#22). There is no service-side commit path, and a service that pushed an archive
branch would be the first Fugaro component to write a branch outside a run (D25).

### I. The neighbours: coordinator, always-on orchestrator, phase 2

**#51 — The coordinator is advisory and unbuilt, and this spec gives it a job it has no way to do. — Gap.**
`docs/coordinator/README.md` (*"reconciliation only; nothing is implemented and no plan is
approved"*), `docs/coordinator/spec.md` (*"Advisory, not authoritative … It never blocks or
reorders merges"*), and its reconciliation's finding **#2** (there is no Cloud Run service;
its #1 is the project-and-installation one). Its
Q10 was answered **(A): one resident service per installation, with the coordinator as a role
it plays**, and its Q4 (A) leaves the model credential out of v1. *Consequence:*
*"Every orchestrator … reports to the coordinator in the same way"* needs a reporting contract
against a service that does not exist, and the two designs must not each invent the identity
model (D14).

**#52 — The always-on orchestrator design (PR #632) is the close sibling, and it disagrees with this spec in one place that matters. — Open.**
`docs/mobile/design/always-on-orchestrator.md` (PR #632; **not on `main`**) designs *"One
Cloud Run service per installation, in the installation's own GCP project, `min-instances =
0`, request-based billing, woken by a Cloud Scheduler tick"*, running *"as a dedicated `orch`
service account with the launcher's grants and no model credential of its own"*, where *"The
agent does not run in the resident service. A task is a Cloud Run Job execution"*, and where
the resident process is *"two functions and no more"* — the consumer and the launcher. Its
§6 rule 4 is *"No model credential of its own, ever"*. **This spec puts the agent, its
session, its prompts and its memory in the resident service.** Its answers of 2026-10-19 (Q1–Q13)
include Q11 (c): no merge in the cloud for the first phase-2 release.
*Consequence:* the two documents cannot both be built; **D20** decides which, and the
reconciliation's recommendation is to keep the agent in a job (PR #632's shape) with this
spec's session and protocol layered on top, because that is also mobile D115's condition
(#53).

**#53 — Mobile D115's conditions for phase 2 are already written, and this spec must meet them or be ruled against them. — Rule.**
`docs/mobile/reconciliation.md`'s D115 (owner's answer 2026-10-17): *"decide phase 2 later —
after phase 1 ships and after managed-projects' D55–D78 gate"*, and its recommendation: the
model credential lives in a secret manager **in the installation's own project**, the service
runs as a dedicated identity with a narrow IAM role, it is **behind the same launch claim
(`runstore.Claim`) and the same budget caps**, and the app is unchanged.
`docs/mobile/implementation-plan.md`'s P5.1 is **UNSCHEDULED** and **SECURITY-SENSITIVE**
*"because it holds a model credential in the cloud … and it holds `fugaro run` authority"*.
*Consequence:* the central orchestrator **is** P5.1's question; shipping it without a ruling
decides D115 by accident, which is exactly what the coordinator's Q10 was written to avoid
(D20).

**#54 — The unit differs: per project in this spec, per installation in PR #632 — and the two are the same object. — Rule.**
`docs/design/v1.md` §1: a Fugaro **project** and an **installation** are the same deployment
unit, as the coordinator's finding #1 recorded for its own collision. *Consequence:* a wording
collision, not a conflict — but the spec's *"A project can have zero, one or several"* central
orchestrators multiplies services in a unit that has one today (D19).

**#55 — A stale citation that this work would land on top of. — Exists.**
`docs/coordinator/README.md` and `docs/coordinator/reconciliation.md` (its #29 and its Related
list) cite the always-on orchestrator design as `docs/design/always-on-orchestrator.md`. PR
#632 places it at `docs/mobile/design/always-on-orchestrator.md`; `docs/design/` on `main` has
no such file. *Consequence:* whichever lands second leaves two dead path citations in
`docs/coordinator/`; a one-line fix for a later pull request (an item, O2).

### J. Release and ordering

**#56 — 0.10.0 is full. — Exists.**
The console's macOS and Linux builds ship for 0.10.0 (`docs/windows-cli/README.md`'s context,
and the release commit *"console: a Linux build (AppImage and .deb, unsigned) for 0.10.0"*);
the credentials service is *"the first product slice"* of the product work and *"waits for
0.9.0"* (`docs/credentials-service/README.md`). *Consequence:* the spec has no release, and
D17 recommends **0.11.0 or later**, behind the credentials service.

**#57 — The ordering is forced by the credential path. — Rule.**
The central orchestrator needs (a) a Cloud Run service type (#1), (b) an identity model (#21,
#31–#33), (c) a credential path that does not put a model key in a long-lived process (#52,
#53), and (d) an alert-ingest and notification path (#34, #41). The credentials service is
(d)'s neighbour and (c)'s owner; the rigor modes work owns the mode table (C6). *Consequence:*
the implementation plan's slices are ordered by these dependencies, and Slice 0 is the part
that depends on nothing.

## 4. The exact edits the mobile spec needs — listed, and not made

The spec's Follow-up bullet asks for `fugaro-mobile-app-spec.md` to describe QR pairing as
discovery only with Firebase Auth as the identity layer. **That file does not exist** (C5).
The file it means is `docs/mobile/functional-spec-v0.1.md`. The exact edits, for a later
pull request that owns them (D22) — **none is made here**:

1. **§4.1 "Adding a project: QR pairing", the sentence** *"It displays a QR code containing
   the project's Firebase configuration and a short-lived, one-time pairing token."* — say
   that the QR carries the project's Firebase configuration and a one-shot pairing token that
   **grants no access by itself**, and that the identity comes from sign-in (§4.2).
2. **§4.1, the sentence** *"Only someone who already has access to the project (CLI or
   console) can generate a pairing code."* — move it to §4.2 as an authorisation statement,
   or delete it: the CLI-only pairing rule of D165 and plan task P6.6 already carries it, and
   in §4.1 it re-imports the conflation the spec is trying to remove.
3. **§4.2 "Authentication and authorisation", the sentence** *"Because anyone can authenticate
   with Firebase, each project keeps an allowlist of approved users."* — replace the premise:
   no public sign-in provider is enabled (`internal/cli/init_firebase.go`
   `refusePublicSignUp`; G12, T5), and the allowlist is the **device nodes**
   `config/admin/devices/$uid` (D166), not a list of users.
4. **§4.2, the sentence** *"Firebase security rules deny access to anyone not on the
   allowlist, regardless of successful sign-in."* — keep, and name the node: `USER` requires
   `config/admin/devices/<uid>` to exist (`internal/budget/rules/rules.go` `userClaim`).
5. **§5 "Summary of v1 decisions", the two bullets** *"Projects added via QR pairing; no
   discovery."* and *"Firebase Auth plus per-project allowlist enforced by security rules."*
   — add one clause to the first: pairing is **discovery**, not identity.
6. **§6 "Open questions", the bullet** *"How allowlist entries are managed (CLI command,
   console UI, both) and whether pairing auto-adds the scanning user."* — D166 already
   answers the mechanism (admin-only device nodes, written by P6.6); the *who* is still open
   and should be re-asked against a central orchestrator whose access set may differ from the
   phone's device set (D6).

The central orchestrator's own spec adds two things this list must not silently absorb: a
**per-project sign-in-method policy** and an **optional email-domain restriction**, neither of
which exists anywhere in the tree or in the mobile documents (#32, #33) — they belong to D6,
and if D6 is answered yes, the mobile spec needs a seventh edit.

## 5. Decisions D1–D26

Each decision states the **recommendation first**, then the alternative and its cost. **Six of
the twenty-six are answered — D1, D2, D3, D6, D12 and D19 — each as its recommendation, by
the owner on 2026-10-20, and each answer is recorded under the decision it answers and tabled
in §6. The other twenty — D4–D5, D7–D11, D13–D18 and D20–D26 — are open.** An answered
decision is still **not an approval**: no part of the plan starts on one until its own
decisions are answered and the owner has passed the gate at the end of
[implementation-plan.md](implementation-plan.md). D-numbers here are **folder-local** (O1).

### D1 — Where a lease lives, and what its record holds
**Recommendation: in the Realtime Database, at `orchestrators/<slug>/<job>/lease`, a record
`{holder, holder_uid, generation, renewed_at, expires_at, mode}` written with `GetETag` +
`PutIfMatch` and never with a whole-node `Put`** — because `internal/rtdb` has no transaction
(#8), because the RTDB is where the budget identity and the rules that could fence a lease
already live, and because a lease in the runs bucket would inherit the launcher's broad reach
(SECURITY.md). The alternative is the runs bucket next to `locks/` and `launching`
(`internal/lock/lock.go`, `internal/runstore/launch.go`): cheaper, reuses `blobx.ReplaceIf`
exactly, but every launcher can write it and a lease is a shepherding seat, not a launch
guard. **Cost of the recommendation:** a new RTDB subtree and a rules change, so a
`RulesVersion` bump and a golden regeneration.

**Answered 2026-10-20 — the recommendation, as written above.** The owner's answer: the lease
lives in the **Realtime Database** (`orchestrators/<slug>/<job>/lease`), written with an ETag
compare-and-put — `GetETag` + `PutIfMatch` — and **never a whole-node put**, with the rules
version bumped. Why: `internal/rtdb` has no transaction (#8), the RTDB is where the budget
identity and the rules that could fence a lease already live, and a lease in the runs bucket
would inherit every launcher's reach (SECURITY.md).

**Corrected 2026-10-21, by P3 — the shape the record ships with, and why it is not the one
above.** The record is `{holder, holder_uid, generation, renewed_at, mode, pr}`, and the
difference is two fields. It carries **no `expires_at`**, because D3's own answered
recommendation has the writer store no absolute deadline it computes itself: the grace is
applied by each reader to `renewed_at`, on the database's clock, so a stored deadline would be
a second source of truth for the same question — and the one the writer's clock produced,
which is the input D3 removes. It carries **`pr`**, which the shape above omits and D2's answered
sub-question asks for: the lease is per job *with the PR recorded on the lease*. The
generation, the renewed-at stamp and the mode are the fields written above, and the ETag
compare-and-put is the one written above, never a whole-node put.

### D2 — One job, one shepherd: the invariant, the fencing token and the grace period
**Recommendation: the lease is the invariant; a takeover is a compare-and-set on
(`generation`, `expires_at`), the winner increments `generation`, and every later action the
old shepherd attempts carries the generation it holds and is refused by the rules when it is
stale.** Fencing is the generation field, not a clock (#10). The grace period is
`renewed_at + grace` where `renewed_at` is written **by the writer's own clock** and the
reader's comparison uses the **database's clock** (`internal/rtdb/client.go` `ServerNow`,
#9) — a lease is the one place the tree should stop trusting `time.Now()`. Recommended
numbers: heartbeat 60 s, grace 10 minutes (the number `runstore.ClaimTTL`, `shared/lease`'s
`CLAIM_TTL` and the spec all already use, #12), both per project. The alternative —
reuse the stale-heartbeat sweep and let the *liveness* record decide (`internal/budget/sweep.go`
`DefaultStaleAfter`): cost, the sweeper deletes entries and hands work to nobody, so the
takeover would still need a loser-notification path.
*Sub-question for the owner:* whether the lease is per **job** (the run) or per **PR** (the
spec says *"two orchestrators never act on the same PR"*) — a job's PR can outlive the run,
and the branch lock (`internal/lock/lock.go`) is already per branch. Recommendation: **per
job, with the PR recorded on the lease**, and the branch lock left where it is.

**Answered 2026-10-20 — the recommendation, as written above.** The owner's answer: the
invariant is a **fencing token** — the **generation** — with a **compare-and-set takeover**;
the lease is **per job, with the PR recorded on the lease**; expiry is judged on the
**database's clock** (`ServerNow`); the heartbeat is **60 s** and the grace **10 minutes**,
both per project; and the **branch lock stays where it is**. Why: fencing is the generation
field and not a clock (#10), `ServerNow` is the one clock this tree can trust (#9), and the
stale-heartbeat sweeper that was the alternative hands work to nobody (#11).

**Corrected 2026-10-21, by P3.** The compare-and-set the lease ships with is on the
**generation** and the node's **ETag**, not on a stored `expires_at`: there is no such field
(D1's correction above), the grace is read as `renewed_at + grace` on the database's clock, and
the fencing token is the generation either way — which is the decision as answered.

### D3 — Clock skew, and who may hold a lease
**Recommendation: the lease's expiry is judged on the database's clock; the writer's lease
record stores no absolute deadline it computes itself; and the holders are named identities,
never `host/pid/rand`** (`internal/cli/run.go` `claimHolder`, #6). The alternative — the
claim's model, where *"Every CLI's clock must be within claimTTL - holdTimeout (5m)"* (#6) —
costs nothing to write but makes the invariant a machine-configuration property; the central
orchestrator has several machines and no operator to keep them in step.

**Answered 2026-10-20 — the recommendation, as written above.** The owner's answer: **the
database clock decides**; **the writer stores no absolute deadline** it computed itself; and
the holders are **named identities, never `host/pid/rand`**. Why: the launch claim's own
assumption — *"every CLI's clock must be within claimTTL - holdTimeout (5m)"* (#6) — makes the
invariant a property of every machine's configuration, and a central orchestrator has several
machines and no operator to keep them in step.

### D4 — Where the orchestrator's agent credentials come from, and the injection boundary
**Recommendation: the agent runs in a Cloud Run job, not in the resident service, and the job
holds the credential exactly as a run does today** — the sealed budget identity
(`internal/budget/token/`, on `main`) plus, when it exists, the credentials service
(`docs/credentials-service/`). The resident service holds **no model credential** (PR #632
§6 rule 4, mobile D115 #53). Alert content is untrusted **text**: it is passed to the job as
an input under a documented data boundary, never as instructions (the convention has to be
written where the prompt is built — `internal/runner/prompts.go` `SystemPrompt`, #44); an
alert job's toolset is narrowed at the level `internal/agent/settings.go`'s
`WriteManagedSettings` already writes (#45); and it **never merges** (#22, #47), in hotfix
mode included. *The parts this decision cannot promise today, and must say so:* a per-fix
secret narrowing is not expressible (#46) — the recommendation is that an alert job runs on a
**dedicated profile** whose account covers only the repositories alerts may touch, which is
the only narrowing the profile model offers. **Cost:** one job per profile per alert class,
and the honest statement that the account, not the fix, is the blast radius
(`docs/project-claude-accounts.md` §10).

### D5 — The WebSocket protocol and its authentication
**Recommendation: one protocol, three clients (CLI, console, phone), authenticated by a
Firebase Auth ID token presented at connection, with the same token re-checked on every
submission**, and the server verifying the token's **claims** (`mu`-style device claim or the
project's own claim), the device node's existence, and the project the token was minted for —
never the socket's origin. The console reaches it **through the CLI it already spawns**
(#29), not by adding a network client to a renderer whose CSP says `connect-src 'none'`.
**Alternative:** a second local socket owned by the CLI (`docs/design/desktop-console.md`
§3.3's Option C, *"a local read-only API served by the CLI"*, rejected for v1 there) — cost:
the console's designed evolution arrives early and unbuilt. **Cost of the recommendation:**
the console's chat surface is a CLI-mediated surface, so the CLI must be running for the
console to chat; and the phone needs a WebSocket client it does not have (#28).

### D6 — The allowlist, the sign-in methods, and the domain restriction
**Recommendation: keep the device node as the authorisation record (D166) and add the email
to it as a field the *administrator* writes, rather than making the allowlist an email list**;
treat "verified email" as a property checked at pairing/administration time, not at every
connection. Then answer three sub-questions explicitly: (a) whether a project may accept
Google or Microsoft SSO at all — the tree's rule is custom tokens only
(`internal/cli/init_firebase.go`'s `refusePublicSignUp`), and lifting it is a change to the
configuration that fires the refusal rather than a policy
flag (#32); (b) whether an email-domain restriction is enforced at sign-in or at allowlist
administration (recommendation: **at administration**, which needs no change to the sign-in
platform); (c) whether an unverified email is refused at pairing (recommendation: **yes**,
using the check `internal/cli/init_members.go`'s `authenticatedUser` already performs for the
CLI, #33). **Alternative:** adopt the spec's wording literally — an email allowlist plus
per-project sign-in-method policy plus domain restriction — cost: it reverses a standing
platform rule (#32), makes the phone's authorisation model and the orchestrator's differ, and
touches the rules whose generation is already at 3.

**Answered 2026-10-20 — the recommendation, as written above.** The owner's answer: the
allowlist is **the phone's device node plus an administered email field**, the **domain rule is
enforced at administration**, an **unverified email is refused at pairing**, and the
**custom-tokens-only sign-in rule is unchanged**. Why: the device node is the one authorisation
record the phone and the orchestrator can share (D166, #31), and enforcing the domain at
administration needs no change to a platform that refuses public sign-up (#32).

### D7 — The shared session and the action locks
**Recommendation: one shared session per central orchestrator, as the spec says, with the
**action locks kept out of the session** and expressed as prompts that carry an owner** —
because the tree's only authorisation for a consequential action is the job's owner plus an
explicit, recorded override, and the mailbox already models exactly that
(`internal/mailbox/message.go` `Action` / `Confirmation`, `shared/confirm/index.js`
`DESTRUCTIVE_VERBS` — *"one confirmation authorises exactly one action"*, #23). Concretely:
each action carries `verb`, `target`, and for a merge a `head_sha`, and the executor re-reads
the target before acting (`internal/mailbox/message.go` `actionField`). **Alternative:** a
"lock" object per job in RTDB that a client takes before acting — cost: a second mutual
exclusion mechanism beside the lease, with no reader that needs it, and a lock is not an
approval.

### D8 — Prompt routing, claiming, escalation, and where the timeouts live
**Recommendation: route by submitter — a person's job to that person first, an automated
job to its callback if it has one and to humans immediately if it does not — with the prompt
recorded per job in RTDB, claimed by a compare-and-set (first answer wins, everyone else sees
who answered), and the escalation timeout read from a **project-level** table keyed by the
rigor mode's five names (`POC`, `Startup`, `Pro`, `Critical`, `Hotfix`)**. The spec's numbers
(5/15/60 minutes) are accepted as the starting values, and they are written **here** because
`docs/rigor-modes/` has none and is the wrong owner for them: its mode is resolved per run,
per repository and per area (its D5/D6), while an escalation timeout is a property of the
**prompt**, and a prompt can outlive the run that raised it. The alternative is to put the
table in the rigor modes work — cost: that work would then own a notification policy, which
its D15 deliberately declines to build. **Also recommended:** the orchestrator never pages
anyone (the spec's own scope line), and a timeout that nobody answers escalates to the
project allowlist **once** — not repeatedly.

### D9 — The signed callback contract
**Recommendation: HMAC-SHA-256 over a canonical byte string, in the shape the mailbox already
uses** (`shared/mailbox-schema/mac.js`'s `macOf` over `canonicalBytes`, D171) — because the
tree has one signature convention and inventing a second is a security review of its own; a
`prompt_id` in the envelope; a timestamp plus a **replay window** the receiver enforces by
keeping the seen `(prompt_id, timestamp)` pairs for the window; a short timeout (recommend
60 s) after which the prompt escalates to humans; and the answer accepted at the orchestrator's
HTTP endpoint **only** with a valid tag and only for a prompt still open. The tag shape check
exists in the rules (`MACOK`, *"checks a tag's shape, never its value"*), and the verifier is
unbuilt (#23, #36). **Alternative:** the provider's own scheme, one per provider — cost: a
verifier per provider, and no shared vocabulary with the phone.

### D10 — De-duplication, rate limit, the automated budget, and the circuit breaker
**Recommendation: key an alert job on the fingerprint (the provider's issue id) and make
"attach to the open job" the default**; put the per-hour automated-job allowance and the
automated daily cap in **new nodes under `config/`** (`config/caps/automated/…`) rather than
reusing `RepoCaps` (#18, #42), so a developer's cap and an alert's cap cannot be confused; and
implement the breaker as a **stored state** (`config/breaker/<slug>`: open/closed, the reason
and the window) that only a human clears, in the shape `internal/budget/kill.go`'s `SetKill`
already uses (#43), with **one** summary notification on opening. The alternative is to
enforce the rate limit in the service's memory — cost: it is lost on every cold start, which
is a scale-to-zero service's normal state. **Sub-question:** whether the automated budget is
per project or per submitter identity — recommendation: **per project**, with the submitter
recorded, because the spec's own words are *"automated jobs have their own spending cap,
separate from developers' caps"* (one cap), while *"every job has an owner"* is about
attribution and routing.

### D11 — Session memory: what is stored, where, and when it may be deleted
**Recommendation: the shared conversation is stored in Firestore, one document per
message, in a collection named by the orchestrator, reachable only by the service's own
service account** (`internal/infra/firestore.go` `FirestoreRules`, #48); each message carries
an `expires_at` field and the raw history is archived **first**; the TTL policy is a
`google_firestore_field` (`ttl_config`) added to Terraform, and it is **only a backstop** —
because a TTL policy deletes on time and cannot express *"only once archived"* (#49), the rule
must be enforced by the writer (a message is written with an expiry **only when** its archive
is recorded). The compacted summary lives as long as the session. **Alternative:** keep the
session in the RTDB, where the rules and the identity already are — cost: the RTDB is a
budget database with a rules generation and a live spender; a chat log in it widens the blast
radius of a rules error, and Firestore is the store the tree already keeps durable history in
(`internal/budget/rollover.go` `Roller`).

### D12 — The command surface
**Recommendation: `fugaro orchestrator add|rm|ls` as project-level commands reading and
writing the project policy and the installation's resources, with `add` refused unless it can
name the identity, the allowlist and the cap, and no `attach` until D5 is settled**; `attach`
then opens the WebSocket session as the signed-in person. **Alternative:** make `add|rm|ls`
local-config commands that only shape a Terraform plan — cost: it does not match how every
other project-level thing works (`fugaro project policy publish`, `fugaro budget set`,
`internal/cli/budget.go`, `internal/projectpolicy/`), and *"per-project allowlist of who may
use which central orchestrator"* would have no writer.

**Answered 2026-10-20 — the recommendation, as written above.** The owner's answer:
`fugaro orchestrator add|rm|ls` are **project-level commands** in the shape of
`fugaro budget set`, `add` is **refused unless it can name the identity, the allowlist and the
cap**, and **`attach` comes only after D5**. Why: it is how every other project-level thing
works (`internal/cli/budget.go`, `internal/projectpolicy/`), and it is what gives the
per-project allowlist a writer.

### D13 — Cost and scale-to-zero
**Recommendation: one service, `min-instances = 0`, request-based billing, a wake path that
is an event rather than a poll** (an alert webhook, a phone request, or a scheduled tick only
if D2's grace period needs one) — the shape `docs/mobile/design/always-on-orchestrator.md`
§4.1 already prices (PR #632) — and **no free-tier assumption**: the design's own note that
*"the documented free allowance is per billing account, not per installation"* is the honest
statement. **Alternative:** `min-instances = 1` — cost: an always-billing service per project
per orchestrator, for a session that is idle almost always. **Recommendation's cost, stated:**
a cold start per wake, so the session must be restorable from Firestore (D11) and the
protocol must tolerate a first-connect delay.

### D14 — How this relates to the coordinator
**Recommendation: the orchestrator reports to the coordinator as a **writer of the
project-wide record** and takes nothing back from it** — because the coordinator is advisory
by its own spec (`docs/coordinator/spec.md`) and has no plan (#51) — and the reporting
contract is defined once, in the coordinator's documents, rather than invented here. The
orchestrator's reports are: what jobs it launched or shepherded, what prompts are open, and
what the lease holders are. **Alternative:** let the central orchestrator *be* the coordinator
(PR #632's Q10 (A): one resident service per installation with the coordinator as a role it
plays) — cost: it decides a coordinator question in an orchestrator document, and the
coordinator's model credential answer (its Q4 (A), none in v1) would then bind this work.

### D15 — How this relates to the local orchestrator, and who sees a lost lease
**Recommendation: the local orchestrator is the CLI (`fugaro run` / `watch` / `diagnose`)
and notices a lost lease the way it notices everything else — through state, not a
subscription** — the lease record carries the holder, and `fugaro watch` gains the lease in
its existing stream (`internal/watch/source.go` `Supervisor.paths()`), so a developer sees
takeover where they already look. The local side's "take the job back" is an **explicit
takeover** (a command), never automatic, because automatic return would oscillate with D2's
auto-takeover. **Alternative:** a push to the local CLI — cost: the CLI has no server and no
notification channel (#34), and a CLI that is not running cannot be pushed to. *Open
sub-question:* whether the returned shepherd must re-take the lease through the same
compare-and-set (recommendation: **yes, always**, so the generation stays monotonic).

### D16 — What is v1 and what is not
**Recommendation: v1 is the deterministic, credential-free half — identity and the allowlist,
the lease and takeover, the shared session and its storage, the prompt/claim/escalation
surface, and the management commands — and it launches **`fugaro run`-shaped jobs** with no
model credential of its own and **no merge**.** Out of v1: alert-triggered jobs with a
destination-level credential (waiting on the credentials service, #21), the pre-built hotfix
(the spec's own words: *"Out of scope for v1"*, #47), pre-built alert jobs, and any merge in
the cloud (PR #632's Q11 (c)). **Alternative:** ship the alert path first — cost: it is the
one part that needs the unbuilt security service and the unbuilt push channel, and it is the
part with the largest threat model.

### D17 — Which release
**Recommendation: 0.11.0 or later, and not before the credentials service's first slice**
(#56, #57): 0.10.0 is full, and the credential path this spec depends on is that service's.
The alternative — squeeze a read-only, credential-free slice into 0.10.0 — costs nothing to
plan and buys a service whose deployment surface (D19) is not yet decided.

### D18 — The merge authority
**Recommendation: the central orchestrator does not merge, and its "action lock" set drops
`merge` for v1** — the tree's non-goal stands (`docs/design/v1.md` §2), and the one merge
path in the tree (a paired device's spoken confirmation, #23) is unbuilt. If the owner wants
merge in the cloud later, it is a decision of its own, with a GitHub App and its revocation
(a later decision by PR #632's own words). **Alternative:** lift the non-goal here — cost:
the spec's threat model then has a component that can merge any PR of any admitted
repository, and the "no auto-merge even in hotfix" rule becomes a property of a mode rather
than of the platform.

### D19 — Per installation or per project, and one service per orchestrator
**Recommendation: follow the tree's unit — the installation (a Fugaro project) — and treat
"per central orchestrator" as *one service per installation in v1*, with several
orchestrators as a later scaling question**; the resource type (`google_cloud_run_v2_service`)
is added to `internal/infra/tf/cover.go`'s `allowedTypes` and `internal/infra/tf/managed_cover.go`'s
`managedTypes` **deliberately**, with its own typed confirmation and its cost row
(`internal/infra/tf/managed_cost.go` already prices the type). **Alternative:** one service
per named orchestrator from the start — cost: N services' worth of grants, secrets and cost
rows for a feature whose sessions are idle, and a policy surface (`fugaro orchestrator add`)
that now multiplies infrastructure.

**Answered 2026-10-20 — the recommendation, as written above.** The owner's answer: **one
Cloud Run service per installation in v1**, and the service type is **admitted deliberately**
to the Terraform cover lists — `internal/infra/tf/cover.go`'s `allowedTypes` and
`internal/infra/tf/managed_cover.go`'s `managedTypes` — with **its own typed confirmation and
its own cost row**. Why: the installation is the tree's unit (a Fugaro project, #54), and one
service per named orchestrator buys N grants, N secrets and N cost rows for sessions that are
idle almost always.

**P1 landed the admission on 2026-10-21; the confirmation the answer names lands with P4.**
The type is on both lists, so a plan holding a service is judged rather than refused for a
type nobody had decided on — and no service is covered by the run's one typed confirmation
yet: the checks on what a service may say (its image, its ingress, whether its invoker check
is on, the account it runs as, its scaling) are the module's, and the module is P4, so until
it lands `attributes` refuses every service by name. A step that applies such a plan therefore
asks **its own** typed confirmation, which is the wording above and what
`internal/cli/init_review.go`'s review path shows ("this step is not covered by the run
confirmation: …; it asks for its own"). P4 is where those checks are written, and where what a
service plan asks once they exist is decided. The managed list is a separate question with the
opposite answer: a type **off** it is refused outright rather than sent to a confirmation —
that gate's refusal is final, and `internal/managed` returns it before anything is typed —
which is why the type is decided on both lists rather than on one.

### D20 — Where the agent runs
**Recommendation: the agent runs in a **job**, and the resident service is a consumer plus a
launcher** — PR #632's split (§4.2), which is also mobile D115's condition (#53) and the only
shape in which the credential question has an answer today. This spec's session, memory,
prompt routing and protocol are then the **layer above** that split: the session belongs to
the orchestrator, the turn belongs to a job. **Alternative:** the agent runs in the service,
as the spec's Session-memory section implies — cost: a long-lived process holding a model
credential, which is exactly the decision mobile D115 deferred and the coordinator's Q10 was
written not to decide by accident.

### D21 — Notifications
**Recommendation: v1 uses only channels that exist or are already designed — a marker in the
shared session, the console's existing feed, and the CLI — and the phone push path waits on
`docs/mobile/design/push-notifications.md` (P5.2, unscheduled, and D121's *"no push in v1"*, #34).
**Alternative:** build the push sender now — cost: a device-token store, a sender identity and
a retention question, i.e. a second product.

### D22 — The mobile spec's edits
**Recommendation: a separate, small pull request applies §4's six (+1) edits to
`docs/mobile/functional-spec-v0.1.md`, and this work makes none of them** — the mobile
folder's guard test (`internal/cli/docs_mobile_plan_test.go` and the functional-spec pins)
would otherwise be redrawn by an orchestrator pull request. **Alternative:** fold them in
here — cost: a docs-only central-orchestrator change edits another work's pinned text, and
its guard fails in the same commit.

### D23 — The D-number namespace
**Recommendation: keep D1–D26 folder-local and distinct by folder, as
`docs/windows-cli/` and `docs/rigor-modes/` do** (their O6 and O7), and cite the decision
with its folder. The register is `docs/coordinator/README.md`'s "The numbers this work
holds": D1–D192 are taken and **D193 onward is free** — a cell that is **stale as read
against `main`**, where `docs/mobile/voice-session/` writes **D201–D220** and leaves D193–D200
unused, so the highest written number is **D220** and the next free block is **D221 onward**
(§7 reads the register by name; **O1** records it). **Alternative:** move this work to the
next free block — cost: renumbering with no other benefit, unless the owner wants one global
sequence.

### D24 — The live-check number
**Recommendation: this work claims **no** check number now, and states its owner-run checks
by name; the number is taken by whichever task writes the check** (the convention
`docs/design/voice.md` §5.3 states, quoted by `docs/mobile/voice-session/`: *"the phase that
ships the check writes them"* — **it is not in the register itself**, which states the
numbering *rule* and not who writes a section), because
the register's claims are unsettled (§7). **Alternative:** claim "the next free
number" now — cost: exactly the collision the register's second rule exists for (*"whichever
of two open claims merges second renumbers itself"*).

### D25 — The archive to Git
**Recommendation: v1 archives nothing, and the spec's *"archived to the project's Git repo"*
becomes a stated non-goal for v1** — there is no committer that is not a run (#50), and
giving the service a branch-pushing credential is a decision of its own with an App and a
revocation. The session's durable record is Firestore (D11), and the run records stay where
they are (the runs bucket). **Alternative:** a service-owned archive branch — cost: a new
git-writing identity, a repository the service may write, and a merge rule for what happens
when the archive conflicts.

### D26 — The presence indicator
**Recommendation: presence is derived from the connection, not stored** — the service knows
who is connected on the socket it already holds, and the indicator is a field on the session
state, never a new RTDB node. **Alternative:** the mailbox's signed heartbeat model
(`shared/presence/`, D117) — cost: a write per beat per client from the phone (#28), for a
fact the socket already carries.

## 6. For the owner — the answered six, and the open twenty

**Six of these are answered and twenty are open, and none of it is an approval.** The six —
**D1, D2, D3, D6, D12 and D19** — were answered by the owner on 2026-10-20, each as the
recommendation the plan was written to, and each is recorded under its own decision in §5.
**D4–D5, D7–D11, D13–D18 and D20–D26 are still open**, and no part they block starts on one.
Each line is the recommendation first.

| # | Question | Recommendation (first) | Alternative, and its cost | Answer, 2026-10-20 |
| --- | --- | --- | --- | --- |
| D1 | Where the lease lives | RTDB, `GetETag` + `PutIfMatch`, new subtree | the runs bucket beside `locks/`: every launcher can write it | **ANSWERED — the recommendation** |
| D2 | The invariant, fencing and grace | generation CAS + `ServerNow`; heartbeat 60 s, grace 10 min | reuse `budget/sweep.go`'s stale heartbeat: hands work to nobody | **ANSWERED — the recommendation** |
| D3 | Clock and holders | database clock; named identities | the claim's 5-minute skew assumption, on N machines | **ANSWERED — the recommendation** |
| D4 | Credentials and the injection boundary | agent in a job, no model credential in the service; dedicated profile for alert jobs | per-fix secret narrowing: not expressible (#46) | **open** |
| D5 | WebSocket protocol and auth | one protocol, ID token at connect **and** per submission; console through the CLI | a CLI local API (console Option C) arriving early | **open** |
| D6 | Allowlist, sign-in methods, domains | device node + email as an administered field; domain rule at administration | the spec literally: an email allowlist, SSO on, per-project methods — reverses `refusePublicSignUp` | **ANSWERED — the recommendation** |
| D7 | Shared session and action locks | one session; actions are prompts with an owner, re-read before acting | a lock object per job: a second mutex, and a lock is not an approval | **open** |
| D8 | Routing, claiming, escalation timeouts | per-submitter routing; the mode-keyed table lives here (5/15/60) | put it in the rigor modes work: it declines to own notifications (its D15) | **open** |
| D9 | Callback contract | HMAC-SHA-256 in the mailbox's canonical form, replay window, 60 s timeout | a per-provider scheme: a verifier each, no shared vocabulary | **open** |
| D10 | Alerts: de-dup, rate limit, budget, breaker | fingerprint + attach; new `config/caps/automated` + `config/breaker/<slug>`; one summary | in-memory limits: lost on every cold start | **open** |
| D11 | Session memory and TTL | Firestore, service-account only, expiry written only after archive; `ttl_config` as backstop | RTDB: widens the budget rules' blast radius | **open** |
| D12 | Command surface | `fugaro orchestrator add|rm|ls` project-level; `attach` after D5 | local-config-only commands: no writer for the allowlist | **ANSWERED — the recommendation** |
| D13 | Cost and scale-to-zero | one service, `min-instances = 0`, event wake | `min-instances = 1`: an always-billing service per project | **open** |
| D14 | The coordinator | the orchestrator reports; takes nothing back | one service with the coordinator as a role (PR #632 Q10 A): binds this work to the coordinator's Q4 | **open** |
| D15 | The local orchestrator and the lost lease | seen in `watch`'s stream; return is explicit, through the same CAS | a push to the CLI: no server, no channel | **open** |
| D16 | What v1 is | the deterministic half; no alert credential, no merge, no pre-built hotfix | the alert path first: needs the unbuilt service and push | **open** |
| D17 | Release | 0.11.0 or later, after the credentials service's first slice | a slice in 0.10.0: a service whose surface is undecided | **open** |
| D18 | Merge authority | no merge in v1 | lift the non-goal: a cloud component that can merge any PR | **open** |
| D19 | Unit and service count | per installation, one service in v1; add the type to both allow-lists deliberately | one service per named orchestrator: N grants, N secrets, N cost rows | **ANSWERED — the recommendation** |
| D20 | Where the agent runs | in a job; the service consumes and launches | in the service: a long-lived model credential, deciding D115 | **open** |
| D21 | Notifications | session marker, console feed, CLI; push waits on P5.2 | build push now: a second product | **open** |
| D22 | The mobile spec's edits | a separate pull request applies §4's list | fold them in: redraws another work's pinned text | **open** |
| D23 | D-numbers | folder-local D1–D26 | move to D221–D246, the next free block (§7, O1) | **open** |
| D24 | The check number | claim none; the writing task takes it | claim "the next free number" now | **open** |
| D25 | Archive to Git | a stated non-goal for v1 | a service-owned archive branch: a new git identity | **open** |
| D26 | Presence | derived from the connection | the signed heartbeat model: a write per beat per phone | **open** |

## 7. The numbers this work would hold

**D-numbers.** This folder's decisions are **D1–D26**, **folder-local and distinct by
folder**, which is the convention `docs/windows-cli/` (its D1–D16) and `docs/rigor-modes/`
(its D1–D19) chose, each recording the same collision. No D-number is allocated by this work —
the decisions are the owner's to answer, and **answering a decision allocates nothing**
(O1, D23).

**The register, read by name against `main`.** The register is
`docs/coordinator/README.md`'s "The numbers this work holds", and its own table is stale, so
it is read here by name and corrected here rather than quoted. It records **D1–D192** as taken
— the highest *it* names, written at its own base `c6009b5f` — with **D79–D89** deliberately
unused, and **D193 onward** as free. **That is no longer the tree's state.** Reading the tree
at `main` now:

- `docs/mobile/voice-session/` — merged on `main` in `1d325928` — writes **D201–D220**: its
  `README.md` says *"decisions D201–D220"*, all open, and its `reconciliation.md` says of the
  gap beside them, *"this work starts at D201 and leaves D193–D200 alone"* — because the brief
  it was written from records **D200** as a ruling *"that this tree cannot cite"*. So
  **D201–D220 are written and taken by that work**, and **D193–D200 are unused** — and by the
  register's own rule, *"an unused number is not a free number"*, that gap is not free either.
- The blocks below D193 are unchanged, and the highest D-number written anywhere under `docs/`
  is therefore **D220**.
- **The next free block is D221 onward, not D193 onward.** The register's own table still says
  D193 onward, and correcting *that* table is the coordinator's folder's change rather than
  this work's: it is recorded as **O1** below, which is where this folder's reading of the
  register lives.

**Check numbers.** This work proposes **no** number (D24). The register's claims, read name
by name for this document:

| Numbers | Whose | State |
| --- | --- | --- |
| 1–18 | the live checklist's own early work | **summary rows only**, no `## Check N` section |
| 19–47 | the live checklist | **written** as sections (39's family lives in the retire-image-mirror plan) |
| 48–56 | the project-centric plan | reserved |
| 57 | the compute backend | a summary row; its section lives in `docs/design/compute-backend.md` |
| 58 | the artifact-runs work | reserved |
| 59a/59b | the console plan | reserved |
| 60 | the budget-token channel | allocated by the owner; **Check 60 is written** in the register |
| 61 | project-centric phase 6 (the monorepo folder export) | allocated |
| 62–63 | console v2 | reserved |
| 64–66 | the voice work | 64 written; **65 and 66 held** |
| 67–68 | managed projects | reserved |
| 69a–69c | the website work | allocated |
| 70a–70c | the website redesign | proposed; 70a–70c written |
| 71–79 | the website work's held range | **held, no check written** — `docs/mobile/README.md` reads them as unused and not taken |
| 80–89 | the mobile work (Task P4.2) | **80, 83–88 unwritten; 81, 82, 89 written** |
| 90–99 | the credentials service | reserved; none written |
| 100–104 | the mobile functional spec | approved 2026-10-18 (`docs/mobile/implementation-plan.md`), still written as *proposed* in `docs/mobile/README.md` |
| 105–109 | the mobile functional spec | reserved and unused |
| 110 | **two claims, neither written**: the P5.3 design `docs/mobile/design/voice-evaluation-switch.md` (its Q4 answer) and the always-on orchestrator design in **PR #632** (`docs/mobile/design/always-on-orchestrator.md` §7) | **collision, settled at merge by the register's rule** |
| 111 | docs/setup-wizard | **written** in the register (Check 111) |
| 112 | docs/windows-cli | **proposed**, on `main` |
| 113–118 | docs/mobile/voice-session | **numbered, not written**, and only after yielding 112 |
| 119 | docs/rigor-modes (**PR #773**) | **taken** by that work's plan, not written |

So the next free number depends on how the open claims settle: the register's own rule is
that *"whichever of two open claims merges second renumbers itself"*, and 110 is claimed
twice. This work's owner-run checks are therefore named and not numbered, and the task that
writes one takes the number then (§7 of the implementation plan).

## 8. Items found while planning, kept here so they are not lost

Four items are about documents rather than about code, and they are recorded rather than
acted on. The decisions are §5's; these are smaller than decisions and one of them is not
this work's to make.

- **O1 — the D-number namespace, and the register's own stale table.** This work's **D1–D26**
  and [docs/setup-wizard/](../setup-wizard/)'s D1–D12 are numerically the same range and are
  told apart only by their folder, as [docs/windows-cli/](../windows-cli/)'s D1–D16 and
  `docs/rigor-modes/`'s D1–D19 already are. The register
  ([docs/coordinator/README.md](../coordinator/README.md), "The numbers this work holds")
  records D1–D192 as taken, **D79–D89 as deliberately unused** — *"an unused number is not a
  free number"* — and **D193 onward as free**. **Read against `main` by name (§7), that last
  cell is stale**: `docs/mobile/voice-session/` (merged in `1d325928`) writes **D201–D220** and
  leaves **D193–D200** unused, so the highest number written anywhere under `docs/` is
  **D220**, and the next free block is **D221 onward** — which is the block this work would
  take if the owner prefers one sequence, not D193–D218 as this folder's earlier draft of this
  item said. Correcting the register's own table belongs to the coordinator's folder, and this
  work does not edit it. **Recommendation: keep the local namespace** (**D23**); renumbering is
  mechanical and changes nothing but the numbers.
- **O2 — a stale citation this work would land on top of.**
  [docs/coordinator/README.md](../coordinator/README.md) and
  [docs/coordinator/reconciliation.md](../coordinator/reconciliation.md) (its finding #29 and
  its reading list) cite the always-on orchestrator design as
  `docs/design/always-on-orchestrator.md`. PR #632 places that document at
  `docs/mobile/design/always-on-orchestrator.md`, and `docs/design/` on `main` has no such
  file. **A one-line fix for a later pull request, in the coordinator's folder** — the
  coordinator's own index already says *"there is no link to it here"*, so the path appears in
  prose twice and in no link.
- **O3 — the spec's Follow-up names a file that does not exist.**
  *"update fugaro-mobile-app-spec.md"* — the mobile spec is
  [docs/mobile/functional-spec-v0.1.md](../mobile/functional-spec-v0.1.md) (C5). The six edits
  it still needs are §4's list, and **D22** proposes that a separate pull request applies
  them. Whoever writes that pull request also has to decide whether the spec's Follow-up
  bullet is corrected in `spec.md` — **it is not, here**: `spec.md` is verbatim and its typo
  list is empty.
- **O4 — Check 110 is claimed twice and written by nobody.** The voice evaluation switch
  (`docs/mobile/design/voice-evaluation-switch.md`, its Q4 answer) and the always-on
  orchestrator design (**PR #632**, its §7 and Q5) both name it. The register's rule settles
  it at merge time — *"whichever of two open claims merges second renumbers itself"* — and
  this work takes **no** number, which is **D24**.

## 9. UNVERIFIED

| # | What is unverified | Why | What would settle it |
| --- | --- | --- | --- |
| U1 | That a Cloud Run **service** can be admitted to the installation's Terraform without breaking `fugaro setup`'s confirmation model | `internal/infra/tf/cover.go`'s `allowedTypes` is a code constant; no service exists to try | adding the type to `allowedTypes` (P1, 2026-10-21): a plan holding a service is refused by the checks the type owes, and by name, rather than as a type nobody had decided on, and until P4 writes those checks no service is covered — so the plan asks its own typed confirmation, as the answer above says — and an owner-run plan — the second half is still unverified |
| U2 | WebSocket support through the Cloud Run front end (upgrade headers, idle timeout, maximum connection duration) | the credentials service's plan records the same class as unverified for streaming (`docs/credentials-service/reconciliation.md` U5, its Check 91) | an owner-run live check on a deployed service |
| U3 | RTDB `If-Match`/ETag behaviour under concurrent writers at the rate a lease implies (one heartbeat a minute per job, plus takeovers) | `internal/rtdb/client.go`'s `PutIfMatch` is used for admin writes (*"ETags work on one location: this is for single-node admin writes"*), not for a hot path | an emulator test plus a live check |
| U4 | Whether the RTDB rules can express the lease's generation check without a `RulesVersion` bump colliding with another work's | the rules template is generated and versioned (`internal/budget/rules/rules.go`'s template, `internal/projectpolicy/version.go`'s `RulesVersion`) | writing the rule and regenerating the golden |
| U5 | That a Firestore TTL policy's deletion is late enough to coexist with an archive step | the client has no TTL API and the repo has no `ttl_config`; a TTL policy is asynchronous on the platform's side | a live experiment on a throwaway project |
| U6 | The real error shape of a Firebase Auth ID token that is valid but whose uid has no device node, over a WebSocket connect | the rules path is proven for RTDB reads (Check 81) and not for a socket handshake | a live check |
| U7 | Whether the console can reach the orchestrator without weakening `connect-src 'none'` in a way the design accepts | the CSP is byte-identical in two places and asserted by `console/test/unit/window.test.ts` | a design decision and a console test |
| U8 | Whether a job's agent can be given a *narrower* toolset than `internal/agent/settings.go`'s `deniedWebTools` allows, without `--dangerously-skip-permissions` becoming per-tool | the runner passes every prompt off by design | a prototype and an owner-run check |
| U9 | What Sentry's webhook actually sends (its signature header, its retry behaviour, its issue-id field) | no Sentry integration exists anywhere in the tree | reading the provider's documentation, and a live webhook |
| U10 | Whether an alert job on a dedicated profile actually narrows secrets as D4 assumes | `docs/design/project-centric.md` §4.1's profile model has not been exercised for this purpose | an owner-run check on a real profile |
| U11 | Whether a shared session in Firestore can be listed and compacted within the runtime's memory and the request timeout at the sizes a project reaches | nothing in the tree stores a conversation in Firestore | a load probe |
| U12 | The exact number of live checks this work needs, and their collision settlement | see §7 | reading the register at merge time |
| U13 | Whether `fugaro watch`'s stream can carry the lease without a second stream or a rules change | `internal/watch/source.go`'s `Supervisor.paths()` is a fixed four | writing it |

## 10. Glossary

- **Installation / project.** The same deployment unit: one GCP project with one runs bucket,
  one Firebase project and one project policy (`docs/design/v1.md` §1; the coordinator's
  finding #1).
- **Orchestrator (this spec's sense).** The role that launches a job, shepherds it and (per
  D18, not today) would carry its PR. The **local orchestrator** is the CLI on a developer's
  machine; the **central orchestrator** is the cloud service this spec describes.
- **Shepherd.** This reconciliation's word for an orchestrator *holding the lease* of a job.
  It is not in the code and not in the spec; it is used here so that "the orchestrator" and
  "the process holding the seat" can be told apart.
- **Lease.** The ownership record (D1). Not the launch claim (`internal/runstore/launch.go`
  `Claim`), not the branch lock (`internal/lock/lock.go` `Holder`), and not the budget's
  money lease (`internal/budget/lease.go` `leaseSource`).
- **Resident service.** The always-on cloud process: in PR #632's design the consumer plus the
  launcher, holding no model credential (`docs/mobile/design/always-on-orchestrator.md` §0.1,
  §4.2). This spec's "central orchestrator" is that process **plus** an agent, session and
  prompt surface (D20).
- **Coordinator.** The per-project, advisory service of `docs/coordinator/`; unbuilt.
- **Security service.** `docs/credentials-service/`'s plan-only Cloud Run service that holds
  model credentials and the GitHub App key; unbuilt (C3).
- **Prompt.** A question, permission or decision an agent raises, tied to a job; the mailbox's
  `prompt`/`answer` kinds are the tree's vocabulary for it
  (`internal/mailbox/message.go` `Kinds`).
- **Callback.** The optional webhook an automated submitter gives so prompts can be delivered
  to it instead of to a human (D9).
- **Breaker.** The stored state that stops automated launches after a rate-limit or budget
  event, cleared by a human (D10); the shape is `internal/budget/kill.go`'s `SetKill`.
- **Fingerprint.** The provider's stable issue id, used to attach new events to an open job
  instead of starting a second one (D10).
- **Device node.** The authorisation record `config/admin/devices/$uid` (D166), which is the
  tree's allowlist today (#31).
