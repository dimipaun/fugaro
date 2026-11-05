# The stateless orchestrator: the spec against the code

**Status: reconciliation answered by the owner 2026-11-04 — D1220-D1243 in full, not only the first draft's D1220-D1240: the round-1 questions D1241, D1242 and D1243 are answered below, so the only thing this section still asks the owner to rule is T6 (Q2's offer against the managed no-prompt rule). Plan in [implementation-plan.md](implementation-plan.md). Nothing implemented, no task approved.**
Checked against `origin/main` at `5998d67c7` (`feat(config): the workflow_secrets block ... (W0) (#1710)`), merged into this branch,
on **2026-11-04**. The open pull requests were read with `gh pr list` and `gh pr diff` the same day. Every path and symbol below was
re-read at that commit; a citation by symbol is held where a line would move.

[spec.md](spec.md) is the owner's spec verbatim (the base spec plus its "Orchestrator daemon" addendum). It needed no typo fixes; it was
only renamed from `fugaro-stateless-orchestrator-spec.md`. Two of its own cross-references are to documents that are not in this repo under
those names ("fugaro-coordinator-spec", "fugaro-mcp-server-spec"); they are `docs/coordinator/spec.md` and `docs/mcp-server/spec.md`.

## 0. The number block

The block this folder uses is **D1220-D1249**, confirmed by the owner on 2026-11-04. Reading:

- `git grep` over `origin/main` for `D13[1-9][0-9]` finds nothing, and `D1310` is the highest number any open pull request writes (#1711,
  workspaces). `D1320-D1349`, the block the brief named, is therefore free.
- **But `D1220-D1259` is already named for this work** on `origin/main` in about ten places, the first six of which are: `docs/mcp-server/README.md` (the census table),
  `docs/mcp-server/reconciliation.md`, `docs/workspaces/README.md`, `docs/plugin-user-scope/README.md`, `docs/binary-allow-rule/README.md`
  and `docs/periodic-reviews/README.md`. No file on `main` writes a decision in that range, and the only open pull request touching it,
  #1712, writes `D1260`/`D1261` (workflow secrets), i.e. above it. Taking `D1320` would leave 40 numbers permanently unused and make the
  census tables wrong. **This design takes D1220-D1249**; the owner confirmed it (section 4).

## 1. Headline table: spec concept to what the repo already has

| Spec concept | What the repo has | Class |
| --- | --- | --- |
| State in **Firestore** (agent doc, task docs, events, transactions, TTL, listeners) | Firestore exists but is **deny-all**, holds only `spendDaily` history, and its REST client has **no transactions**. Live state, CAS, streaming and the whole Watch feed are in the **Realtime Database** (F1-F4) | Wrong / Gap |
| Single-writer via Firestore transactions | `rtdb.GetETag` + `PutIfMatch` (HTTP if-match) is the repo's CAS, in production in the kill switch and the mailbox lease (F5) | Exists (other store) |
| Append-only event log with ULID ids and a cursor | None as a log. Run progress is level state: `/agents/<slug>/<run>` heartbeat entries in RTDB plus `result.json`, `launch.json` and the claim in the runs bucket (F6) | Gap |
| Worker heartbeats and "started/progress/completed/failed" events | Runs already write a heartbeat registry entry under their own run-scoped RTDB identity (`budget/registry.go`, rules `RUN` macro) (F7) | Exists |
| Task-scoped token for a job, issued by the credentials service | The run's RTDB write grant is already scoped to its own `$slug/$run`; the credentials service ships dormant and is about model and git credentials (F7) | Exists (different mechanism) |
| Eventarc / Pub-Sub / Firestore trigger to wake the loop | None: no Eventarc or Pub/Sub API in any Terraform module; Cloud Run jobs run on Scheduler or an API call (F8) | Gap |
| Fugaro MCP server with a state tool group | Hosted MCP server is **designed** (`docs/mcp-server`, M1-M10), six tools, **not on `main`**: no `mcpauth` code at `5998d67c7` (F9, F10) | Gap |
| `spawn_task` as "the single launch path" with caps | Decided already as D931: one pre-launch path shared with `fugaro run` (claim + `budget.Precheck`) (F11) | Exists (design) |
| Spawn guardrails: spend, concurrency, rate, idempotency, kill switch | Spend and kill switch: `budget.Precheck`, `budget.SetKill`, enforced by the DB rules. Idempotency: the launch claim. Per-person `max_parallel`. No per-project running cap, no spawn-rate cap, no batch cap (F12) | Exists / Gap |
| Coordinator writes Firestore directly; deterministic code + LLM only for judgement | The coordinator exists as `ee/coordinator` with its **RTDB** `coordinator/` subtree (D195) and a sweep; P7/P8 (the resident service) are not started (F13) | Exists (other store) |
| Mirror task events into RTDB for Watch | Watch already reads RTDB directly (`watch.Start` over four streams); there is nothing to mirror if state is in RTDB (F3) | Wrong premise |
| Decision agents, model pinned per decision type | Reviewer routing by changed path is an owner rule (security-sensitive paths always Opus); the cloud senior reviewer design (`docs/cloud-senior-review`) is the nearest mechanism (F15) | Rule / Exists (design) |
| Local daemon under launchd | No daemon, launchd unit, systemd unit or service installer in the CLI (git grep of `launchd` finds only console and docs text) (F16) | Gap |
| Session-start hook that injects state | The plugin has no `SessionStart` hook (`git grep SessionStart -- plugin internal/claudeplugin` is empty); one is designed in `docs/plugin-user-scope` (F17) | Gap |
| Replaces "cron sweep + monitors", launcher agents | Those scripts are in the owner's scratchpad and memory, **not in the repo** (F18) | UNVERIFIED |
| Supersedes the coordinator spec's auto-compact | The coordinator spec text says so (`docs/coordinator/spec.md` section 9); the central-orchestrator spec says the same under "Session memory" (F19) | Open |

## 2. Findings

### Store and transactions

**F1. Wrong. "State lives in Firestore" is not what the installation has been built around.** The installation's live store is the Realtime
Database (`internal/budget/model.go` package doc; node paths in the same file). Firestore is a second store whose database is created by
`fugaro update`'s REST step, not Terraform (`deploy/terraform/terraform_test.go:1194` `TestNoFirestoreDatabaseResource` forbids a
`google_firestore_*` resource); `internal/cli/init_firebase.go:638` `firestoreStep` creates it in `us-east5` (permanent, never
changeable: the confirmation text asserted in `internal/cli/init_firestore_test.go:56`), with **deny-all rules**
(`init_firestore_test.go:43` `denyAllReformatted`) and a `meta/installation` mark. `docs/coordinator/reconciliation.md` #6 and #7 already
corrected the same assumption for the coordinator spec, and `docs/central-orchestrator/spec.md` ("Session memory") repeats it.
*Consequence:* choosing Firestore means a second store gains a writer, a rules change (today deny-all, and the shared safety property that
no client can read it), an IAM grant, a Firestore client with transactions and a fake. Choosing RTDB reuses what exists (D1220).

**F2. Gap. The Firestore client cannot do what the spec leans on.** `internal/firestore/client.go` package doc lists `Get`, `Patch` (an
upsert with an updateMask), `Delete`, a one-field range `Query`, `GetDatabase` and `CreateDatabase`; one attempt per call, 30 s bound, no
retry, **no transaction, no listener, no batch commit, no multi-document write**. Preconditions it does have are per-document
(`MustNotExist`, `IfUpdateTime`, used at `internal/budget/rollover.go:511` and `:526`). The spec's step 8 ("one transaction: patch, action
ids, cursor") is multi-document in its own storage layout (tasks and decisions are subcollection docs), so it cannot be built on this
client without new REST work (UNVERIFIED that the REST `documents:commit` with preconditions would suffice: see U1).

**F3. Wrong premise. There is nothing to mirror for Watch.** The spec says Watch "stays on Realtime Database for now; the coordinator
mirrors task events into it". `internal/watch/source.go` `Start` supervises four streams of the project's RTDB and
`internal/watch/json.go` defines `fugaro watch --json`; runs already write their own state to `/agents/<slug>/<run>` (F7). A mirror is only
needed if task state is moved out of RTDB. Under D1220's recommendation the mirror, spec Next step 6's last clause and step 9's "retire the
mirror" all disappear.

**F4. Exists. The shared writer and rules.** `ee/coordinator/state.go` already documents the rule the spec wants: a new top-level
subtree (`coordinator/project/`, `coordinator/repos/<slug>/`), deterministic IDs, written only by one account, rules scoped so a signed-in
person reads and nobody else writes (D195, answered 2026-10-21). **The precedent does not carry to a person-written subtree.** The coordinator's
subtree is `.write: false` with **one** service-account writer (the admin IAM bypasses the rules for it); the rules do not bind that writer,
the account's identity does. An orchestrator state node written by a person's own credential has no such single identity: see the bound below
(trust boundary) and D1241. Rules are generated in
`internal/budget/rules/rules.go` (macros `RUN`, `USER`, ...), with emulator tests under `internal/budget/rules/`.

**F5. Exists. Single-writer is compare-and-swap, not transactions.** `internal/rtdb/client.go:178` `GetETag`, `:209` `PutIfMatch` (a 412 is
`ErrPrecondition`, retried a bounded number of times, never forced), used by `budget.SetKill` (`internal/budget/kill.go:189`) and by the
mailbox heartbeat lease (`internal/mailbox/lease.go`, ruling M1: "the lease IS the heartbeat ... no fourth node"). A single node holding
`{rev, cursor, state}` and written with `PutIfMatch` gives the spec's `base_rev` check atomically. *Limit:* one RTDB node is one atomic
unit; per-task documents are separate nodes and cannot be committed with the cursor together except through RTDB's multi-path `Patch`
(`rtdb.Client.Patch`, `client.go:234`), which is atomic for the paths it updates but has no if-match. See F23.

### Events and the cursor

**F6. Gap, and probably unnecessary. There is no event log; what exists is level state.** A run's facts are: the launch claim
(`runstore.Store.ClaimKey`, `ClaimTTL = 10 min`), `launch.json`, the registry entry under `/agents`, `result.json` (read by
`watch/finished.go` `FinishedRun`) and the queued scan (`watch/queued.go`). `fugaro runs ls` and `watch --json` already fold these into one
view. A level-triggered daemon ("compare desired state to the view, act on the difference") needs **no cursor, no replay and no event
ordering**, and is idempotent by construction. The spec's event log earns its place for two things level state cannot give: a human-written
`user.message` and an audit of the daemon's own decisions. Both are small. *Consequence:* the ordering and crash questions of section 3
mostly vanish in the first slice (D1229).

**F7. Exists. Worker reporting and scoped identity.** The run registry's heartbeat lives in `internal/budget/registry.go` (`goSafely(bctx,
"heartbeat", ...)`), the path is `budget.PathAgent` (`internal/budget/model.go:133`), and the rules' `RUN` macro (`rules.go:19`, "TOKEN of the
run that owns $slug/$run") is exactly "a job can post only about its own task". Nothing needs the credentials service for this; that
service's scope is model and forge credentials (`.claude/rules/credentials-service.md`).

**F8. Gap. Event-triggered Cloud Run is not provisioned.** `git grep` over `deploy/terraform/gcp` for `eventarc` and `pubsub` finds only the
custom-role permission list; the installation's API list names `run`, `cloudscheduler`, `storage`, `secretmanager`, `artifactregistry`,
`cloudbuild`, `logging`, `monitoring`. The only scheduled job is the history sweep (`modules/installation/history.tf`: Cloud Scheduler
calling the job, an own service account, no credential). Event-driven wake-up means a new API surface, Eventarc triggers, IAM and Terraform
tests (and `fugaro update` convergence for an installed project: the hard rule). Scheduler polling is the existing pattern. A *local*
daemon needs none of this: it reads RTDB over SSE (`rtdb.Client.Stream`, which resumes with a full `put` of the current value, so a missed
change is never lost).

### The MCP server and the tools

**F9. Gap. The hosted MCP server is not built.** `docs/mcp-server` is the design (answered 2026-11-04, plan M1-M10, recommended slot
0.20.0). At `5998d67c7` there is no `mcpauth`, no tool registry, no public coordinator service: the only matches for `mcpauth` outside
`docs/` are the docs test. Whether M1-M10 are being built in open work was not visible as merged code; see U2. The state tool group
therefore has no home to land in yet, and it **cannot be demonstrated end to end before M7 (registry), M9 (spawn)**.

**F10. Conflict. The spec's tool table collides with the MCP design's six tools.** Side by side:

| Spec tool | MCP design (`docs/mcp-server/spec.md` section 5) | Reading |
| --- | --- | --- |
| `spawn_task(spec)` | `spawn_task`, scope `tasks:write`, one launch path (D931) | Same tool; the MCP version's rules win (idempotency claim, `Precheck`, no ceiling widening) |
| `cancel_task(id)` | `cancel_task`, writes the same cancel marker as `fugaro cancel` | Same |
| `list_tasks(filter)` | `list_tasks` (recent runs of the project) | Same name, **different meaning**: the spec's tasks are orchestrator work items (`t-042`), the MCP's are runs. A tool whose name is shared and whose subject differs is the "field changes meaning" trap in CLAUDE.md |
| `get_task(id)` | `get_task_status`, `get_result` | Different names for runs |
| `retry_task(id)` | none (a retry is a new `spawn_task`, or `fugaro run --pr N`) | Spec-only |
| `get_events`, `get_state`, `apply_patch` | none | The state group |
| `get_artifact(path)` | `stream_logs` (recorded progress) | Overlap, different shapes |

The MCP design also states the scope rule: **two scopes in v1** (`tasks:read`, `tasks:write`; D927 refuses more), and its section 11
explicitly leaves the state group's names and whether it needs a scope "to that design's to say". `apply_patch` is not a task write: it
changes goal, facts and decisions. *Consequence:* the state group needs its own scopes (reopening D927) and a rename of the work-item
"task" (D1224).

**F11. Exists (design). One launch path.** `docs/mcp-server/spec.md` section 5 and D931: `spawn_task` takes the launch claim
(`internal/runstore/launch.go`, `ClaimKey`) and runs `budget.Precheck` (`internal/budget/precheck.go:58`, called from
`internal/cli/launch_budget.go`) before it starts anything. The spec's "`spawn_task` is the single launch path, so caps are enforced in one
place" is already that decision; only the spec's extra guardrails are new (F12).

**F12. Exists / Gap. The guardrail table, row by row.**

| Spec guardrail | Today |
| --- | --- |
| Spend, project and token caps | **Exists**: RTDB caps and `config/mode`, `Precheck`, rules-enforced; MCP tokens carry limits (M1) |
| Kill switch | **Exists**: `budget.SetKill` over `config/kill/global` and per scope. The spec's "admin command" is the existing command |
| Idempotency key on every spawn | **Exists for runs**: the launch claim makes one launch one run; `ClaimTTL` 10 min bounds it. Per-action keys are new (D1231) |
| Concurrency per project and per agent | **Gap**: `max_parallel` is the CLI's per-person limit (referenced in `internal/backend/backend.go:40` and `internal/backend/batch/reap.go`); no per-project running cap found. `projectpolicy.ConcurrencyPerToken` (`internal/projectpolicy/policy.go:534`) counts runs sharing a *credential*, a different thing |
| Max spawns per cycle / per minute | **Gap** |
| Batch approval (default 50) | **Gap**; the repo's governance work (`docs/task-governance`) is the nearest and lives in `ee/` |

**F13. Exists (other store). The coordinator.** `ee/coordinator` (`state.go`, `sweep.go`, `project.go`, `intent.go`) is the deterministic
coordinator the spec describes, writing RTDB; the resident service (P7/P8) is not started. The spec's "deterministic code is the coordinator,
the LLM only for judgement" is already the coordinator's posture. The only new part is the LLM decision call on top.

### Hosting, placement, rules

**F14. Rule. Placement under `ee/`.** `.claude/rules/ee.md`: nothing outside `ee/` imports `ee/`; `ee/` carries the Subscription licence;
governance, report and the coordinator already live there. The local single-user daemon replaces what the owner runs by hand and is a
person-level convenience, so it belongs in core (MIT) unless the owner wants it licensed; the **shared** pieces (multi-developer handoff,
central orchestrators, batch approval, workspace caps) are ee (D1222). Whichever side, the state-schema package is a core package only if
nothing in it needs `ee/`; it must not import it.

**F15. Rule / Exists (design). Decision agents.** The owner's routing rule (documented for reviewers: docs/tests/design to the light
reviewer, anything touching credentials, writes, guards, the managed gate, Terraform/IAM, the agent environment, prompts, recipes,
CLAUDE.md, AGENTS.md, `.claude/**`, plugin skills or CI is security-sensitive) is the addendum's "changes to agent prompts, recipes,
CLAUDE.md, AGENTS.md or CI workflows always count as security-sensitive" and is decided **by changed paths, never by how the change
describes itself**. That classifier is deterministic code and must be implemented as code, not as an instruction to the model. The cloud
senior review design (`docs/cloud-senior-review`, answered, not built) is the existing mechanism for "a fresh Opus agent reads a pushed
branch and writes a versioned verdict artifact the coordinator acts on" and is the template for a cloud decision agent.

**F16. Gap. A local daemon.** There is no background process, service installer or `launchd`/`systemd` unit in the CLI. The closest
long-running local pieces are `fugaro watch` (a TUI, foreground) and the mailbox consumer (`internal/mailbox`, foreground, with an RTDB
heartbeat lease that is the right single-instance fence for a daemon). The Windows CLI work (`docs/windows-cli`) means a macOS-only daemon is
a stated platform limit.

**F17. Gap. Session-start hook.** No `SessionStart` hook ships in the plugin (`git grep SessionStart -- plugin internal/claudeplugin`
returns nothing), and **`docs/plugin-user-scope` designs no hook either** (`git grep -i hook -- docs/plugin-user-scope` is empty): its nudge is a
**skill sentence** that runs `doctor --offline` (D945-D947, D952: "the session-start check never reaches the network"). So there is no hook slot
to share: a hook is a new surface, defined by Claude Code's plugin hook configuration (to be confirmed against the Claude Code plugin reference by
O10's red step; **not** `plugin/skills/hooks/session-start`, which is not where plugin hooks live), and D1237's "one slot shared" is read as "one
printer: the same skill sentence or hook, ordered after the update nudge". And `state --brief` **reads the RTDB**, a network read that D952
forbids for the nudge: **D1243 answered it on 2026-11-04 — the session-start print reads a local cache the daemon writes (no network, D952
holds), and `state --brief` stays the explicit command that reads the node**. Note
the plugin's tests check every command and flag a skill names against the CLI: a new `fugaro orchestrator ...` command is a plugin-test
change in the same PR.

**F18. UNVERIFIED. The scripts the daemon replaces are not in the repo.** The "never idle" rule, its Monitor watcher
(`watch-pipeline.sh`, polling `fugaro ls` and `gh pr list`) and the hourly sweep exist in the owner's scratchpad and memory
(`pipeline-keepalive.md`, `feedback-token-cost-cuts-2026-11-03.md`); `git grep` finds `finished.sh` and `queue.txt` nowhere in the tree
except an unrelated fixture scrubber. What the daemon must replace is therefore known from the owner's description only (U3).

**F19. Open. "Supersedes fugaro-coordinator-spec's auto-compact."** The text lives in `docs/coordinator/spec.md` section 9 and
`docs/central-orchestrator/spec.md` "Session memory", both owner-verbatim files. The coordinator reconciliation already rules them
`Wrong` on the store; auto-compact is a safety net in a session, which this design removes only if the resident coordinator becomes a
per-event cycle (D1236 asks who amends them).

**F20. Rule. No agent handles a credential, and an agent never judges its own gate.** `docs/mcp-server/spec.md` section 9 (a tool never
returns a credential), `docs/managed-agent-converge` (the gate is deterministic; "no judgment calls by the agent"), and
`initflow.AgentRefusal` / `CanConfirm` (`internal/initflow/stage.go:440`, `:617`: a coding agent's session applies nothing).
*Consequence:* a decision agent's patch must never be able to widen a ceiling, answer a decision addressed to a person, or turn a refusal
into a launch.

**F21. Rule. A field never changes meaning.** The spec's `task` (work item), `status`, `rigor` and `owner` fields would be new wire
shapes read by the console, mobile and the plugin. They need new names where an existing run-record or MCP field is similar (`status` is
the run record's, `owner` is a person-class concept in task governance) and a schema in `schemas/` (CLAUDE.md: wire formats decode
strictly and have JSON Schemas).

**F22. Rule. `fugaro update` is the whole upgrade.** A new RTDB subtree, rules change or Firestore collection is a rules/Terraform change
that `fugaro update` must converge on its own with a test for an installation at the previous version, and a release's `### For operators`
is "run `fugaro update`". This is a cost line for any store choice and a small one for RTDB (the rules generator already owns the
subtrees) versus Firestore (rules deploy plus an IAM grant).

**F23. Gap. The spec's atomic "apply patch + action ids + cursor" has no RTDB analogue for per-task docs.** If tasks are separate nodes
the commit is not atomic with the rev. Two honest shapes: (a) one node per orchestrator holding the whole structured state (it is capped by
design at about 4k tokens, so it is small: well under any RTDB node limit) and written with `PutIfMatch`; (b) per-task nodes plus a
multi-path `Patch` with the rev check done in a read-then-check, which has a race. (a) is recommended (D1220).

## 3. The hard questions

### Trust boundary: who writes state, and what a hostile writer reaches

The spec's rule is "the LLM never writes state directly; code validates and applies a typed patch". That is right and incomplete. The
people and processes that can reach the state:

| Writer | Reaches through | What a compromised one can do |
| --- | --- | --- |
| The local daemon (a person's laptop process) | its ADC / person credentials to RTDB (an OAuth Bearer, which is IAM and **bypasses the rules**) | Everything the person can: that is the ceiling. A compromised **laptop session** = the person; state integrity cannot exceed it, so the cloud-side checks (kill switch, `Precheck`, per-run cap, rules) must be the real bound, not the daemon's own validation |
| An interactive Claude session | the same tools, via MCP or a CLI | `apply_patch` with attacker text from a PR title, log line or a pasted page. The text reaches `facts`, `summary` and `decisions`, which re-enter every later cycle's prompt: **stored prompt injection**, persistent across cycles |
| A decision agent | its structured output | The same as the above plus `spawn_task` actions: a loop of spawns |
| A cloud job | its own run-scoped RTDB entry only (F7) | Falsify its own heartbeat and status; it cannot touch other runs or the state doc |
| A token holder via hosted MCP | the front door's scopes | Bounded by M1's narrowing token limits; read-all/write-own is stricter than the two existing scopes |

Design rules this implies (carried to the decisions): (1) typed ops only, **no free text field that is later treated as an instruction**:
`facts` and `summary` are data, shown to the model fenced as untrusted, never above the system prompt; (2) **a decision addressed to a person
(`waiting_on: user`) is resolvable only by a person-class identity** (paired device / console), never by an agent token or by `apply_patch`
from a session that could be a hostile model: otherwise a prompt-injected agent approves its own merge (D1225); (3) **`goal_set` and any op
that widens a ceiling is person-only**; (4) **spawn from a cycle is capped by code** (count per cycle, per hour, per day, plus the existing
spend caps), and a cycle's `spawn_task` may only launch a repository and recipe already named in a task the person approved, never a new
destination (D1226); (5) the state document carries a per-field provenance (`by`: person id, session, daemon, decision-agent) so an audit can
say who wrote a fact; (6) untrusted string values are length-capped and stripped of control characters (the repo's `internal/safetext`
exists for this, and `watch/finished.go` already cleans bucket text before display).

**What the rules do and do not bind (corrected after the round-1 security review).** The first draft said the RTDB rules bind the writer; they do
not. People reach the database with an OAuth Bearer: launchers and operators hold only `roles/firebasedatabase.viewer`, admins
`roles/firebasedatabase.admin` (`deploy/terraform/gcp/modules/firebase/iam.tf`, the `viewer` and `admin` grants), and **IAM bypasses `.write` and
`.validate`** (`internal/mailbox/lease.go`'s own comment: the writer "runs on the operator's ADC, which the store does not hold to the mailbox's
`.write` claims or `.validate` lists"). The rules macro `USER` is the **paired phone's `mu` claim**, not a CLI person, and any launcher can mint a
custom token through `signJwt` (`iam.tf`, the `minter` grant), so a claim-based "write own" rule can be forged by a launcher. Consequences:
(a) **validation of the state's shape is client-side** (O0's validator, the same code in the daemon and the CLI); a rules `.validate` defends only
against a job or token identity, never against a person's CLI; (b) **write-own is not enforceable by the rules** for a CLI person; (c) the real
bounds on money are outside the state node: the launch claim, `Precheck`, the kill switch and the spend caps; (d) a viewer-role person cannot
write the node by Bearer at all, so the node needed a writer identity before O2: **D1241 was answered on 2026-11-04 as (a), the recommendation — the
person's own credential writes `orchestrator/`, validation is client-side only, and the rules refuse only a job or a token identity; O2 claims no
write-own guarantee and its gate is lifted.** What the rules *can* still do:
refuse a run-scoped job identity and refuse an oversize or malformed value from a token-based writer.

**Person-only is a mitigation, not a barrier.** `internal/initflow/stage.go` (`AgentAdvice`) and `internal/cli/init.go` both say the agent check is
"a mitigation, not a barrier": `env -u CLAUDECODE fugaro orchestrator patch` or a direct node write (Bearer, no rules) passes it. So G2's CLI
half (D1225) is **not enforced by an agent-session check**; **P1 is resolved as negative**. The honest bound: person-only ops (approve, `goal_set`,
resolving a `waiting_on: person` decision) require at least a **Typed confirmation at a real terminal** (`initflow.CanConfirm(Typed)`, the control
the repo's other person-only steps use), which stops the accident and most sessions; it is itself beatable by a pty wrapper (`script`, `expect`), so "a session that cannot type" is overstated; the **residual** is stated: an adversary running
as the person at a terminal is the person. **D1242 was answered on 2026-11-04: the terminal Typed confirmation stands for now, and a paired-device
channel comes later** (the recommendation). The later channel may reshape O0's `Approval`, so nothing here fixes that shape: no task and no test is
gated on it, and no task builds the paired-device channel.

**Single-writer.** In RTDB, `PutIfMatch` on one state node plus a daemon lease (the mailbox heartbeat-lease pattern, F5/F16) gives one
daemon per orchestrator id and a rejected write for any stale one. The spec's "Both modes coexist; transactions keep them from
clobbering each other" is correct for state and silent about **side effects**: two cycles can each decide the same spawn from the same
stale read; only the committed intent (a compare-and-swap of the state) and the per-run-id launch claim stop the second. The lease and the state ETag
are separate nodes: **the state compare-and-swap, not the lease, is what serialises intents**, and a daemon that lost its lease can still win a
`PutIfMatch` after a re-read, so `Save` re-checks the lease before each retry (plan O3, G9). Idempotency keys must therefore be derived, not generated
(D1231).

**One approval covers the first attempt and at most one automatic retry (round-2 M1, M2; owner ruling 2026-11-04, the recommendation not taken).** A
session's `work_item_upsert` must not be able to turn one approval into many launches. `Phase`, `RunID`, `Attempts` and the `Intents` are written only by
the **Daemon** actor; a session's upsert that carries them is refused. `Approved` is written only by `work_item_approve`, never by an upsert, because the
digest is unkeyed, and **it carries the attempts it covers**: the attempt the person approved, and after a first `failed` at most **one** automatic retry
of that same approved spec, which reuses the approval only while `SpecDigest(item)` still equals the approval's digest. Committing the retry's intent
spends the allowance, and only then is `Approved` cleared in the same `PutIfMatch` (the first intent clears nothing). A **second** failure needs a
person's new approval. What this widens is one approval's reach: from one run to two, which is what the spec's own table wanted ("Task failed, first
time: Code retries"). The caps, `Precheck` and the kill switch still bound it — the retry counts toward the per-cycle, per-minute, per-hour, per-day and
running caps like any other launch — and a failure whose cause is a **refusal** (a failed `Precheck`, the kill switch, a spend cap, a validation refusal)
is not retried at all. The circuit breaker is unchanged. The digest is one `SpecDigest(WorkItem)` over every
field except a named bookkeeping list (`Phase`, `RunID`, `Attempts`, `Note`, `Approved`), so a field added later is digested by default (a reflective
test pins the list). The approve op carries the digest the person saw and is refused if it no longer equals the current one; the Typed confirmation
shows the digested spec. State size: the ~4k-token target needs a retention rule, so finished items, resolved decisions and old `Intents`/briefs are
archived out of the state node past a cap (O0), except that an `Intent` younger than the longest cap window is never archived (the caps are derived from intents), and a node still over the cap then stops the daemon spawning.

### Cost of the cycle loop

Not measurable from the repo; what is known:

- The owner's own measurement (memory, 2026-11-03): the long coordinator session plus a half-hourly sweep cost about **396.5M tokens in 7
  days (about 1.4M per sweep)**, because each sweep re-read a huge context, and general-purpose subagents were 71% of spend. That is the
  baseline the design is arguing against.
- A stateless cycle's input is bounded by design: system prompt + state (target under 4k tokens) + events (batch cap 50, payloads short),
  so a cycle should be of the order of **10k input tokens, 1k output** (a design target, **UNVERIFIED**: the real size depends on the
  system prompt, which is unwritten, and on whether the cycle uses tools; U4).
- Prompt caching helps only the fixed prefix; a cycle every few minutes outlives the cache TTL, so assume uncached reads of the prefix
  (not verified for the model in use).
- **Most cycles should not call the model at all.** The coordinator spec's own table (heartbeat, progress, first failure, normal
  completion handled by code) says so. A daemon that wakes the LLM on every tick re-creates the sweep's cost at a smaller constant.

Runaway protection, in order: (1) a deterministic gate decides whether a model call is needed at all (D1221); (2) a **count cap** on model
calls per hour and per day and a **circuit breaker** after K consecutive failed or empty-diff cycles, which pauses the daemon and writes a
`pending_decisions` item for the person (owner lesson 22: a "hard $5" wording made a coder stop with no commits and an empty diff got a
"ship" review; a cycle's failure must be a visible state, not a silent no-op); (3) the existing spend caps and kill switch (cloud runs a
cycle spawns are bounded by `Precheck`); (4) a `max_output_tokens` pin per decision model (owner lesson 21: a model whose maximum output is
under 32000 broke every review until `agent.max_output_tokens.reviewer` was set). A local daemon uses the person's own Claude login: Fugaro's
gateway budget does not cover it, so (2) is the only dollar control there; this is a decision (D1228).

### Event-log ordering and idempotency

If an event log exists (D1229): ULIDs from independent writers are not totally ordered (millisecond resolution plus a random part, and
laptop clocks disagree), so **order is by the server-assigned sequence**, not by id; RTDB gives `ServerNow` (`rtdb.Client.ServerNow`) and a
push key whose prefix is a server timestamp. At-least-once delivery means the handler is keyed by event id; a **deterministic id** (run id
plus transition, e.g. `<run>:finished`) makes a producer's retry a write to the same key rather than a duplicate. The mailbox ledger
(`internal/mailbox/ledger.go`) is this repository's worked example of dedupe-before-act with a persisted record.

### The cursor, and a crash between apply and cursor advance

The spec's step 7-8 executes actions, then commits patch + action ids + cursor. A crash after the launch and before the commit re-runs the
cycle, re-issues the action, and the action id is "recorded in state" only on commit, so the id does not yet exist to dedupe. For a spawn
that is **money**. Two repairs:

1. **Intent first, at-most-once** (mailbox rule: "the record of a delivery is on disk before the action it asked for runs"): commit the
   intent `{action id, minted run id, launch-spec digest}` with a `PutIfMatch` of state, then launch **under that recorded run id**, then commit
   the result. **Correction after the round-1 and round-2 security reviews (what `launchRun` in `internal/cli/run.go` does):**
   `runstore.Store.ClaimKey()` is `<run prefix>launching`, one claim **per run id**, and run ids are random (`task.NewRunID`). `launchRun` starts a
   run **at most once, however many CLIs race on the same run id**: it returns "already-launched" if `launch.json` (or `result.json`'s execution)
   exists; otherwise it takes the claim with create-if-absent; the claim "is never deleted after a launch" (only a definitive `backend.ErrRejected`,
   a cancel or a refusal before the start releases it); a stale claim (older than `ClaimTTL`, 10 minutes) is taken over by an overwrite matched to
   the generation read, so two takers cannot both win; a fresh claim held by someone else makes the loser wait for `launch.json` or fail "still in
   flight". `--run-id` ("repeating one reports its launch instead of starting another"), `createTask` ("a repeated run ID must repeat its task") and
   `--retry` ("launches the stored task that never started") already make a launch idempotent per run id. **So the per-run-id claim IS the
   concurrency fence** (two daemons, a stale claim, a retry), and the corrected recipe is **re-issue, never mint again**: the intent records the minted run id before the
   start; recovery **re-issues the same launch under that recorded run id through `launchRun` (the `--retry` path when `task.json` exists)**, which reports
   "already-launched" if the start happened or launches exactly once if it did not. An "adopt only, never issue" recovery would leave an item stuck
   in `running` forever (and holding a running-cap slot) if the crash fell between `task.json` and the start. It never mints a second run id. **Before `task.json` exists `--retry` cannot be used** (`retrySpec` in `internal/cli/run.go` refuses "has no task.json"), and rebuilding the task from the item's *current* spec is a check-then-write hole: a session edit after the intent was committed would launch an unapproved spec under the old intent. So recovery recreates the task under the recorded run id only when `SpecDigest(item)` equals the intent's digest and the approval still stands; otherwise the intent is marked abandoned (nothing started).
2. **Level-triggered**: no cursor at all; the next cycle re-derives "task t-042 has no run" and acts again, with the per-run-id claim as the fence
   and the committed intent as the record that a run id was already minted for that attempt.

The cursor is then only a performance device (skip events already folded) and its advance can be the same `PutIfMatch` as the state
write. A cursor that moved without the effect (the reverse crash) loses an event, which is why dead-lettering and a periodic full
reconciliation against the run view must exist even in the event model (D1229).

### Cloud Run job timeouts and quotas

Known in the repo: the job's per-task timeout is a Terraform variable (`modules/workflow/job.tf:31`, `workflow.task_timeout_s`, whole
seconds, at least 1), and the backends add `TaskTimeoutSlack = 2 min` (`internal/backend/backend.go:29`) to account for it. The launch
claim lasts 10 minutes (`runstore.ClaimTTL`). The Firestore client bounds each call at 30 s with no retry.
**UNVERIFIED** (not testable from this container): Cloud Run's current maximum job task timeout; the per-project and per-region job and
execution quotas; Eventarc and Pub/Sub trigger latency and delivery guarantees for a job target; the Firestore per-document sustained write
rate the spec cites ("about one write per second per object" is the spec's figure for GCS, the Firestore document limit is similar but not
confirmed here); RTDB write throughput for one node. A cycle that runs as a Cloud Run job also pays cold start and the repo's container
image pull; it must be given a timeout shorter than the cycle interval. See U5.

### Managed-mode fit

`docs/managed-agent-converge` is built (0.18.0): in a managed project the agent itself runs converge and publish commands behind a fixed
deterministic gate, with no judgment calls. The daemon is a second kind of "agent that acts without a prompt". Rules that follow: it must not
call any gated converge command; decision agents run with the same deny rules a coding agent's session has (`initflow.AgentRefusal`); and
it must not be used to supply the "go" a managed gate withholds. A cycle may *report* "an update is due" and nothing more. Managed mode also
shows the owner's tolerance: the answer to "the agent should not prompt me" was a **fixed gate**, not agent discretion, and a state-patch
design that lets an agent decide what to approve is on the wrong side of that precedent.

### ee/ placement

See F14. A practical split: core holds the state schema, the typed patch validator and the single-user local daemon (no dependency on
`ee/`); `ee/` holds the shared parts that need identity and governance (an agent owned by the project, handoff between developers, batch
approval, per-workspace caps). Nothing in core may import `ee/`; the daemon's optional ee features must be registered through
`ee/register` the way the coordinator's CLI is.

### Migration of the owner's current scripts

What exists today (from memory, not the repo): a Monitor watcher polling `fugaro ls` and `gh pr list` every 150 s; an hourly sweep prompt
that re-reads the session; `queue.txt` and `finished.sh` (not found in the tree); launcher agents (planner, console launcher) that are
resumed. The daemon replaces the polling (code) and the re-reading (state), not the **judgement** of "which task next, which review model,
merge or fix". A safe migration: (1) the daemon runs **observe-only** (shadow) for a week, printing what it would do next to what the
sweep did; (2) the first thing it takes over is the pure bookkeeping (launch queued tasks whose dependencies merged, mark finished runs,
nudge on stuck); (3) merge decisions stay with the person or the orchestrator session until the decision agents have a measured record;
(4) the owner's existing session migration (spec "Migration") is one patch dump through `apply_patch`, and **that dump is the first
stored-prompt-injection surface**: validate it with the same code, cap it.

### Smallest useful first slice

No model call, no Firestore, no MCP, no cloud: a `fugaro orchestrator` local daemon (core, launchd/systemd user unit, DB lease) that (1)
reads the project's run view the way `watch --json` does, (2) keeps a small structured state file/node (tasks, dependencies, decisions) in
RTDB written with `PutIfMatch`, (3) launches queued tasks through the same path as `fugaro run` when dependencies are met and capacity is
free, with a per-hour spawn cap and the kill switch, (4) writes a notification when a run finishes or a decision is needed, (5) prints a
compact `get_state` for a session-start hook. That alone retires the Monitor watcher and the sweep's *polling*, which is the cost driver
the owner named, and defers every question that needs a model or a new public surface. Decision agents, the hosted MCP state group, event
log, summary versioning and the cloud daemon are later slices.

### Overlap with MCP's tools and with Watch

MCP: the state group is additive to the six tools only if its names do not collide (F10), it needs scopes D927 refused (so D927 is reopened
for it), and it cannot ship before M7/M9. Until then the same operations exist as CLI commands with `--json`, which is how the plugin's
skills drive Fugaro today. Watch: with RTDB state, Watch and the orchestrator share one database and one SSE stream; Watch can render the
orchestrator's `pending_decisions` with no mirror (F3). With Firestore state there are two stores and a mirror to build, own and retire.

## 4. DECISIONS

Numbers **D1220-D1249** (section 0). Recommendation first; the cost of the alternative is stated; the answer column is the owner's.

| # | Question | Recommendation and reason | Alternative and its cost | Answer |
| --- | --- | --- | --- | --- |
| D1220 | Where does orchestrator state live: the existing **Realtime Database** or the spec's **Firestore**? | **RTDB**, one state node per orchestrator under a new top-level `orchestrator/` subtree (like D195's `coordinator/`), written with `PutIfMatch`. Reuses CAS, SSE and Watch, the rules generator and emulator tests; no mirror, no new IAM, no new store to converge (F1-F5, F22, F23). | Firestore: needs a transaction-capable client (the present one has none), a rules change from deny-all, an IAM grant, a fake, and a Watch mirror to build and later retire; buys TTL policies, richer queries and per-document scaling. Reversible later only by a migration. | **Answered 2026-11-04.** RTDB, new `orchestrator/` subtree written with `PutIfMatch`: the recommendation. Why: reuses CAS, SSE, Watch and the rules generator; no second store to converge. |
| D1221 | What is the first slice? | A **local, deterministic daemon with no model call**: run-view reader, small structured state, dependency-driven launch through the `fugaro run` path with a spawn cap, notifications, a `get_state` print (section 3, "Smallest useful first slice"). It retires the sweep and the Monitor, the owner's named cost. | Build the spec's full cycle (model + event log + MCP tools) first: weeks more before the first saving, and every unsolved question below blocks it. | **Answered 2026-11-04.** First slice is the local deterministic daemon, no model call: the recommendation. Why: it retires the sweep and the Monitor, the cost the owner named. |
| D1222 | Core (MIT) or `ee/`? | **Core** for the state schema, patch validator and single-user local daemon; **`ee/`** for shared agents, handoff between developers, batch approval and workspace caps, registered through `ee/register` (F14). | All in `ee/`: one licence line, but the single-user convenience the owner wants today would need the Subscription build, and the core could not test it with `ee/` deleted. | **Answered 2026-11-04.** Core for schema, validator and the single-user daemon; `ee/` for shared agents, handoff, batch approval and workspace caps: the recommendation. Why: the single-user convenience must test with `ee/` deleted. |
| D1223 | Until the hosted MCP server exists, how does a session reach the state? | **CLI commands with `--json`** (`fugaro orchestrator state / events / patch`) taught by the plugin skill, as every other Fugaro operation is; the MCP state group follows the MCP plan's M7/M9. | Wait for MCP: nothing usable for months (F9). Or build a local stdio MCP server: a second surface the MCP design deliberately avoided (D931). | **Answered 2026-11-04.** CLI commands with `--json` (`fugaro orchestrator state / events / patch`) before MCP: the recommendation. Why: how every other Fugaro operation is driven, and MCP is unbuilt. |
| D1224 | Tool and field naming against the MCP design | Adopt the MCP names for runs (`spawn_task`, `cancel_task`, `list_tasks`, `get_task_status`) and **rename the spec's work item** (e.g. `item`/`work_item`) so "task" stays a run; state group `get_state`, `get_events`, `apply_patch`; `retry_task` becomes a `spawn_task` with the same idempotency key family (F10, F21). | Keep the spec's names: two `list_tasks` with different subjects, a field-meaning collision CLAUDE.md forbids. | **Answered 2026-11-04.** Adopt the MCP run tool names, rename the work item, state group `get_state` / `get_events` / `apply_patch`: the recommendation. Why: one name must keep one meaning (F21). |
| D1225 | Who may resolve a decision addressed to a person, and who may call `goal_set`? | **Only a person-class identity** (paired device, console, the CLI as the signed-in person); an agent token or a session's `apply_patch` may **add** decisions and propose resolutions but never close a `waiting_on: user` item or set the goal. | Agent may resolve any decision: a prompt-injected session approves its own merge; fixing that later changes the wire shape. | **Answered 2026-11-04.** Only a person-class identity resolves a person's decisions and sets `goal_set`: the recommendation. Why: a prompt-injected session must not approve its own merge. |
| D1226 | What may a cycle spawn? | Only a repository, recipe and rigor **already named in an approved work item**, through the single launch path, capped per cycle, per hour and per day by code, plus the existing spend caps; a new destination needs a person. | Free-form `spawn_task` from a model: one injected log line reaches money. Cost of the cap: legitimate fan-out is slower. | **Answered 2026-11-04.** A cycle spawns only already-approved work items through the single launch path, capped: the recommendation. Why: one injected log line must not reach money. |
| D1227 | New scopes for the state group (reopens D927's two-scope rule)? | **Yes: `state:read` and `state:write`**, `write` also gated by D1225; every token read-all/write-own as the spec says, but a token holding only `tasks:*` cannot touch state. | Reuse `tasks:write` for `apply_patch`: a spawn-capable token silently becomes a state-editing token and cannot be narrowed later without breaking it. | **Answered 2026-11-04.** New scopes `state:read` and `state:write`, write gated by D1225: the recommendation. Why: a `tasks:write` token must not silently become a state editor. |
| D1228 | What runs a decision agent, and what bounds its cost? | **Local `claude -p` first** on the person's own login, each call with a fixed brief, `--max-turns`, a pinned `max_output_tokens`, a **count cap** (proposal: 6 calls per hour, 40 per day) and a circuit breaker after 3 consecutive empty or failed cycles that pauses the daemon and raises a decision for the person. Cloud variant later on the cloud senior reviewer's pattern. | A Fugaro cloud run per decision: gateway-budgeted and isolated, but each cycle pays container start and a job slot; do it only for the security-sensitive class. Numbers are proposals, not measured (U4). | **Answered 2026-11-04.** Local `claude -p` with hard bounds (max-turns, pinned `max_output_tokens`, 6 per hour and 40 per day, circuit breaker after 3 failed or empty cycles): the recommendation. Why: the only dollar control on a person's own login. Numbers are proposals (U4). |
| D1229 | Event log or level-triggered reconciliation? | **Level-triggered in v1**: each tick derives actions from state vs the run view (idempotent, no cursor, no replay). Keep a small append-only **decision/audit log** (deterministic ids) only for the daemon's decisions and `user.message`; add a cursor when a producer needs one. | The spec's full event stream with ULIDs and a cursor: more machinery, an ordering problem across clocks, a dead-letter path, and the crash window below. | **Answered 2026-11-04.** Level-triggered v1 plus a small audit log with deterministic ids: the recommendation. Why: no cursor, no replay, no ordering problem in the first slice. |
| D1230 | Event ordering key, if a log exists | **Server-assigned** (RTDB push key / `ServerNow`), **deterministic ids** (`<run>:<transition>`) for producer retries; the cursor is a performance device, never the source of truth. | ULIDs minted by writers: laptop clocks make order ambiguous. | **Answered 2026-11-04.** Deterministic ids and a server-assigned order: the recommendation. Why: laptop clocks make writer-minted ULIDs ambiguous. |
| D1231 | Order of effect and commit | **Intent first**: commit the action intent (derived id: work item + attempt, **plus the minted run id and the launch-spec digest**) with `PutIfMatch`, then launch under that recorded run id, then commit the outcome. At-most-once for money; recovery re-issues the same launch under the recorded run id through `launchRun`/`--retry` (it reports an existing launch or launches once; it never mints a second run id), and the per-run-id launch claim is the concurrency fence (round-1 finding 3, corrected by round-2 W1). | The spec's act-then-commit: a crash between spawns twice because the id is not recorded yet. | **Answered 2026-11-04.** Intent first, launch under the derived id, then commit: the recommendation. Why: at-most-once for money across a crash. |
| D1232 | Platforms for the local daemon | **macOS launchd and Linux systemd user unit** installed by `fugaro orchestrator install`, one instance by an RTDB heartbeat lease (the mailbox lease pattern); Windows out of v1 (`docs/windows-cli`). A sleeping laptop is fine: cloud runs continue and the next tick catches up (level state). | macOS only (cheapest, matches the owner's machine); or all three (a service-manager abstraction to test per OS). | **Answered 2026-11-04.** macOS launchd and Linux systemd user unit, one instance by an RTDB heartbeat lease, Windows out of v1: the recommendation. Why: the mailbox lease is the existing fence; `docs/windows-cli` is not ready. |
| D1233 | Keep `summary_replace`? | **No in v1.** State is structured (items, decisions, facts); the human-readable summary is **generated** from them on read, never written by a model. Removes the narrative field a hostile or confused cycle can drift or poison, and the whole summary-versioning chain (spec Next step 7). | Keep it with version history and a periodic rebuild: more code, and a persistent prompt-injection carrier that re-enters every later prompt. | **Answered 2026-11-04.** Drop `summary_replace`: the recommendation. Why: no model-written narrative that re-enters every later prompt. |
| D1234 | Where is the model pinned per decision type, and who classifies "security-sensitive"? | The classifier is **deterministic code over changed paths** (the owner's reviewer-routing rule, F15), not model judgement and not configurable downward; the model name per class is a local config key, tightening-only against any project layer. | Model picks its own tier or a project may lower it: a security change reviewed by Sonnet because the description said "docs". | **Answered 2026-11-04.** Security-sensitive is deterministic code over changed paths, never configurable downward; the model per class is a tightening-only local key: the recommendation. Why: the class must not depend on how a change describes itself. |
| D1235 | Migration of the owner's scripts | **Shadow mode for about a week** (daemon prints what it would do beside the sweep), then it takes over bookkeeping only (launch when dependencies merged, mark finished, stuck nudge); merge and review-model choices stay with the person until decision agents have a record. The one-time session dump goes through the same validated, capped `apply_patch`. | Cut over at once: no comparison, and a bad rule launches real runs on the owner's installation (`project belong` must not be touched in live steps). | **Answered 2026-11-04.** **NOT THE RECOMMENDATION: the owner chose to cut over at once, with no shadow week.** The daemon replaces the owner's sweep, watcher and queue scripts immediately. Why: the owner chose it; no further reason was given. The consequence is under OPEN TENSIONS (section 6). |
| D1236 | Amending the owner-verbatim coordinator and central-orchestrator specs (auto-compact, Firestore) | **Do not edit them.** Add an "amended by `docs/stateless-orchestrator`" note to each design's README, as other designs do. Whether the coordinator's resident service becomes a per-event cycle is a P7/P8 question for its own design. | The owner edits the originals: authoritative but loses the record of what was asked. | **Answered 2026-11-04.** Do not edit the owner-verbatim coordinator and central-orchestrator specs; add an "amended by" note to each README: the recommendation. Why: keeps the record of what was asked. |
| D1237 | Session-start hook (F17: `docs/plugin-user-scope` has no hook; read as one printer, not a shared slot) | **One hook slot shared with `docs/plugin-user-scope`'s session-start nudge**: print a short `get_state` summary and any pending decision; `CLAUDE.md` instruction alone for v1 is the fallback. | A second hook: two things that print at start and must be ordered; or no hook: the session starts blind to state, the problem this design is for. | **Answered 2026-11-04.** One session-start hook slot shared with `docs/plugin-user-scope`'s nudge: the recommendation. Why: two printers at start would need ordering. |
| D1238 | Managed projects | The daemon and its decision agents **never run a gated converge or publish command**; they may report "an update is due" and nothing else. | Let a managed project's daemon call the gate: ties the agent's judgment to the converge gate the owner deliberately made deterministic. | **Answered 2026-11-04.** The daemon and its decision agents never run a gated converge or publish command and report "update due" only: the recommendation. Why: the managed gate stays deterministic. |
| D1239 | Spawn-guardrail defaults not in the repo (project running cap, per-minute spawn rate, batch size) | Put them in the **project policy as tightening-only** fields with conservative defaults (proposal: running cap = the person's `max_parallel`, batch 10 rather than the spec's 50); decided in the plan's slice that needs them. | The spec's 50: a looping cycle can launch 50 real runs before any person is asked. | **Answered 2026-11-04.** Project-policy fields, tightening-only, conservative defaults (running cap = `max_parallel`, batch 10, a per-minute spawn rate): the recommendation. Why: a looping cycle must not launch 50 runs before anyone is asked. |
| D1240 | Release slot | **0.21.0 or later**, after the MCP chain (0.20.0 slot) for anything that needs the hosted tools; the local deterministic daemon (D1221) is independent of MCP and can ship earlier if the owner wants the saving first. | Same window as MCP: both are security-heavy and both touch the rules and `fugaro update`. | **Answered 2026-11-04.** The local daemon may ship earlier; the MCP-dependent parts are 0.21.0 or later: the recommendation. Why: the daemon is independent of the MCP chain. |
| D1241 | Who writes the `orchestrator/` state node, before O2? | **The person's own credential** (option (a)): the CLI and the daemon write it with the person's ADC, O0's validator is the only shape check, and the rules refuse a run-scoped job identity and a token writer's oversize or malformed value. Reason: the rules do not bind a person's OAuth write at all (IAM bypasses `.write`), so the honest design claims no write-own guarantee rather than buying one that cannot be given (F4, trust boundary). | (b) A service-account writer behind the person's CLI — the coordinator's shape: a new identity to provision, grant and converge through `fugaro update`, and a new place the credential is read from, for a guarantee the rules still would not give a person's own CLI. (c) A custom-token claim the rules check: any launcher can mint it through `signJwt`, so it is a mitigation only. | **Answered 2026-11-04.** (a), the person's own credential, validation client-side, the rules refusing only job and token identities, no write-own guarantee claimed: the recommendation. Why: write-own is not enforceable by the rules for a CLI person. O2's gate is lifted (section 6). |
| D1242 | A paired-device channel for person decisions: are approval, `goal_set` and a person decision resolved only from the paired phone or console? | **Not in v1: the Typed confirmation at a real terminal stands**, and a paired-device channel comes later as work of its own. Reason: the Typed confirmation stops the accident and most sessions, and no task in this plan needs the device channel, so requiring it would gate all of slice 1 on a surface nothing builds. | Require the paired device from v1: every person-only op (approve, `goal_set`, resolving a `waiting_on: person` decision) waits on a new channel — pairing, MAC, a prompt on the phone — and its shape fixes O0's `Approval` before slice 1 ships. The cost of deferring is the residual already stated: an adversary running as the person at a terminal is the person. | **Answered 2026-11-04.** The terminal Typed confirmation for now, a paired-device channel later: the recommendation. Why: it stops the accident, and no task here needs the device channel. The later channel may reshape O0's `Approval`; **no task is gated on it** (section 6). |
| D1243 | The session-start print versus D952's no-network rule: what does the print read? | **A local cache the daemon writes**: the print reads that file, shows its freshness stamp, and reaches no network, so D952 holds for the one check it governs. `state --brief` stays the explicit command that reads the node. Reason: the alternative changes a standing no-network rule for a print that runs at the start of every session, on the person's behalf, unasked (F17). | Amend D952 for this one read: the session-start check reaches the RTDB on the person's behalf at the start of every session, which is the thing D952 refuses, and a check that fails offline — the state a nudge must survive. The cost of the cache is staleness, which the freshness stamp makes visible, and a writer in the daemon (O8 gains it). | **Answered 2026-11-04.** The local cache the daemon writes: the recommendation. Why: D952's no-network rule holds at session start. **O8 gains the cache writer; O10 depends on O8 (level 4)** (section 6). |

D1244-D1249 are reserved and unused. The owner confirmed the block D1220-D1249 on 2026-11-04.

**The owner answered all of D1220-D1243 on 2026-11-04.** Twenty-three of the twenty-four are the recommendation; **D1235 is not** (cut over at once instead of a shadow week), and the plan's own questions carry two more owner rulings that are not the recommendation: **the retry rule (one automatic retry under the same approval)** and **Q2 (`fugaro update` offers to install the daemon)** — both in the plan's section 6, with Q2's tension in section 6 below.

## 5. UNVERIFIED

- **U1.** Whether the Firestore REST `documents:commit` and `beginTransaction`, with the per-document preconditions the client already
  wraps, would give the spec's multi-document atomic commit. Known from Google's public API, not exercised here; the repo's client does not
  call them.
- **U2.** Whether M1-M10 of the MCP plan are in flight on a branch: `origin/main` has no `mcpauth` code and none of the open pull requests
  listed on 2026-11-04 carries it. The brief says it is being built; the first tasks may sit on a long-lived branch not checked out here.
- **U3.** The owner's current scripts: `watch-pipeline.sh`, `queue.txt`, `finished.sh`, the sweep text. They are described in memory only
  and are not in the repository; what the daemon must replace is taken from that description.
- **U4.** The token and dollar cost of a cycle and of a decision call (about 10k in, 1k out is a design target, not a measurement); prompt
  cache behaviour at a cycle interval; the proposed call-count caps.
- **U5.** Cloud Run limits: the current maximum job task timeout, per-project and per-region execution quotas, Eventarc or Pub/Sub trigger
  latency and delivery guarantee for a Cloud Run job, and Firestore and RTDB sustained write rates. This container cannot reach the public
  documentation (the limitation `docs/coordinator/reconciliation.md` records as U1).
- **U6.** Whether the owner wants the single-user daemon under the Subscription licence (D1222's alternative); counsel's reading of what is
  "business" is not in the repo for this feature.
- **U7.** Whether the "hosted" and "cloud orchestrator" in the addendum are meant to be the central orchestrator (`docs/central-orchestrator`,
  plan only, D1-D26 open) or the coordinator's resident service (`ee/coordinator`, P7/P8 not started); the addendum treats them as one
  ("like the coordinator").
- **U8.** Live behaviour: nothing in this reconciliation was run against a real installation (RTDB, Firestore or Cloud Run). No owner check
  number is claimed.
- **U9.** Which surface would deliver a push (Q3). Verified against `main`: the repository carries **no** push implementation. The phone's push is
  design-only (`docs/mobile/design/push-notifications.md`, Task P5.2, Phase 5 unscheduled; D121's "no push in v1", answered 2026-10-17), and the
  app depends only on Firebase analytics and crash reporting. The console's notification is a design gap (`docs/design/desktop-console-v2.md` F27)
  whose D33 answer — derived from the watch feed by the console's own reader, off by default — is unbuilt (V2.7.3), and nothing under `console/src`
  uses Electron's `Notification` API. What does exist is the RTDB **mailbox** the phone polls (`mailbox/<mb>/{inbox,outbox,heartbeat}`, every write
  MAC-checked by the rules; pairing D165/D166): a foreground channel, not a push. So "the mobile push the owner already has" is not verifiable from
  this tree and is read as a setup outside it, the way F18 reads the owner's scripts.

## 6. OPEN TENSIONS (reported, not resolved)

**T1. D1235 cut-over at once, and the rollback the owner ruled (answered 2026-11-04).** The owner chose no shadow week, so the daemon replaces
the sweep, the Monitor watcher and the queue scripts the day it is switched on. A bad launch rule then launches real runs with no comparison
period against the sweep. The mitigations that remain are D1226's approved-items-only spawn, D1239's caps (running cap, batch, per-minute
rate), the existing spend caps and the kill switch. **The owner ruled the rollback on 2026-11-04, and it is not the plan's open item any more:
the old scripts are kept 14 days and the rollback is documented and run by hand — stop the daemon, re-arm the sweep, the watcher and the queue
scripts. No rollback command is built** (the recommendation). The plan carries the manual steps as an owner-run item (O11) and Check 302's "scripts
still in place" never means both launching at once: the per-run-id claim is shared by nothing, so the scripts' launching stops before the daemon starts.

**T2. D1224 against the hosted MCP design's deferral of `spawn_task`.** D1224 renames `retry_task` to `spawn_task`, but the hosted MCP design
was amended the same day to defer `spawn_task` out of its first slice (H2: it needs `signJwt`, a runs-bucket write and
`runWithOverrides`). The orchestrator's launch in v1 is therefore the **local single launch path** (`fugaro run`), not an MCP tool, and
the MCP state group must not depend on `spawn_task`.

**T3. D1227's new scopes need the MCP design to add them** (D927 amended). That edit belongs to the `docs/mcp-server` folder and is a
later task, not this one.

**T4. The MCP server is unbuilt and its threat model was just re-reviewed** (an amendment to `docs/mcp-server` is in flight, run
20261104-224808-fe80). The MCP-dependent slice (slice 3 of the plan) waits for it.

**T5. The workspaces design reuses the MCP server** and was amended the same day; anything this design says about workspace caps
(D1222's `ee/` part) waits on that design as well.

**T6. Q2 against the managed no-prompt rule (OPEN; the owner is asked to rule, and the plan does not resolve it).** The owner answered Q2 on
2026-11-04 **not** as recommended: `fugaro update` **offers** to install the daemon, rather than leaving `fugaro orchestrator install` as a separate
opt-in. That collides with two standing rules, and the plan follows the answer while stating the collision rather than deciding it:
(1) installing persistence (a launchd or systemd unit) must still be **refused under an agent marker**, so the offer happens only at a real terminal
and never in a coding agent's session — that part is not in doubt; (2) the owner's standing ruling is **no prompts on a managed project** (terminal
included), so the question is what the offer does in a managed project. **Reading one:** `update` installs the unit silently on a human's machine,
because asking is the prompt the rule forbids. **Reading two:** `update` never offers it there, because installing a unit is a change the person did
not ask for and the no-prompt rule was about not blocking, not about acting unasked. **Recommendation (not applied):** the offer appears only in a
non-managed project, or when the person has already run `fugaro orchestrator install` once; a managed project's `update` re-renders and restarts an
**already installed** unit and never installs a new one. **Unanswered: the owner rules; O9 lists the work and builds nothing until then.**

**Round-3 open items (reported; two of the three are now ruled).** (1) **Answered 2026-11-04 (D1242): no task is gated on the paired-device channel.** It names no gated task because approvals are consumed in O6 and O8 and the terminal Typed confirmation stands for now; the later channel may reshape O0's `Approval`, and nothing here fixes that shape. (2) **Answered 2026-11-04, not the recommendation: the spec's retry row stands — an approval covers the first attempt and at most one automatic retry of the same approved spec.** The owner allowed the retry the spec's table wanted ("Task failed, first time: Code retries"); the plan had made one approval cover one attempt. `spec.md` is not edited; the correction is that one approval can now launch two runs, bounded by the caps, `Precheck` and the kill switch (section 3, "One approval covers the first attempt and at most one automatic retry"). (3) An ambiguous launch error stays in flight up to `ClaimTTL` (10 minutes, `launchRun`'s contract; the runner's `ErrDuplicateExecution` is the second fence), and a daemon's repeated "still in flight" ticks must not count toward the circuit breaker.

**Round-1 owner questions (all three answered 2026-11-04; the plan's gates on them are lifted).**

- **D1241. Who writes the state node, before O2?** **Answered as (a), the recommendation:** the person's own credential, validation client-side only, the rules refusing only job and token identities, and O2 claiming no write-own guarantee. Why: the rules do not bind a person's OAuth write (IAM bypasses `.write`), so (b) a service-account writer buys a guarantee the rules still would not give, at the price of an identity to provision and converge, and (c) a claim-checked custom token is mintable by any launcher through `signJwt`, so a mitigation only. **O2's gate is lifted** (the plan's O2 no longer waits on this answer).
- **D1242. A paired-device channel for person decisions.** **Answered, the recommendation:** the terminal Typed confirmation stands for now and a paired-device channel comes later. Why: it stops the accident and most sessions, and no task here needs the device channel; requiring it would gate all of slice 1 on a surface nothing builds. The residual stays stated: the Typed confirmation is beatable by a pty wrapper and `env -u CLAUDECODE` passes the agent check, so it stops an accident, not an adversary running as the person. The later channel may reshape O0's `Approval`; **no task and no test is gated on it**.
- **D1243. The session-start print versus D952.** **Answered, the recommendation:** the print reads a **local cache the daemon wrote**, so D952's no-network rule holds at session start and the cache's freshness stamp is what tells the person how stale it is. Why: the alternative changes a standing no-network rule for a check that runs at every session start. **O8 gains the cache writer and O10 depends on O8 (level 4)**; `state --brief` stays the explicit read of the node.

## 7. What this reconciliation does not do

It writes no code, no Terraform and no rules change, and it does not approve any task. It keeps one rule from the folders it
reads: **no agent handles a credential**, and the daemon is not allowed to turn a refusal into an action.
