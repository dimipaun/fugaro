# Fugaro M9 — Budget guardrails, live dashboard and spend history

*Status: design, all decisions settled 2026-09-30 (§16). Not an implementation plan. Source spec: [m9-spec-source.md](m9-spec-source.md). Base design: [v1.md](v1.md) (sections cited as §n).*

This document maps the source spec onto Fugaro as it stands after M6. The user has made these decisions, and they bind the design:

1. **Firebase holds the state.** Live state (budget counters, the agent registry, kill switches) goes in the **Realtime Database (RTDB)**. History (daily spend records) goes in **Firestore**.
2. **A model gateway in the container enforces spend on every model call.** It reserves the worst-case cost first, then makes the call, then reconciles to the actual cost. It covers **API-key and Vertex** auth only, so dollar caps are enforced only where billing is real. `oauth` is never routed through a proxy (§5.8).
3. **The redundant (N-attempt) mode is out of scope.**
4. **M6 is done.** [v1.md](v1.md) is the base design.
5. **The settled decisions D1–D15 in §16:**
   - **Budget writes.** Jobs write directly to RTDB with per-run Firebase tokens that the launcher mints, and database rules bound what each token can do. There is no budget service.
   - **Caps are a guardrail for now.** Making them hold against a compromised agent is deferred.
   - **Firebase regions.** RTDB goes in `us-central1`. Firestore goes in `us-east5`, or `nam5` where that isn't available.
   - **The budget day is UTC**, and caps count model dollars only.

---

## 0. Summary

- **Budget scope.** A *project* in the spec's sense is one Fugaro **repository**. Caps nest: repository inside global, and each level has a per-day and a per-run cap. There is a global kill switch and one per repository. The day is UTC. "Global" is the installation: each installation has its own dedicated Firebase project (D3, §6.0).
- **The gateway runs inside the runner** (`fugaro exec`, part of the base image's binary) and listens on `127.0.0.1`.
  - Claude Code reaches it through `ANTHROPIC_BASE_URL` (API key) or `ANTHROPIC_VERTEX_BASE_URL` with `CLAUDE_CODE_SKIP_VERTEX_AUTH=1` (Vertex).
  - The gateway holds the real credential; the agent's environment no longer does.
  - It rejects any model the stage didn't pin.
- **Reservation happens at two levels.**
  - Each call reserves its worst-case cost against a **local lease**, exactly and in-process.
  - Leases are reserved against the RTDB counters, in one atomic multi-path write per lease.
  - A call never waits on Firebase unless its lease is exhausted.
- **Per-run Firebase identity.**
  - `fugaro run` (the launcher) mints a Firebase **custom token** for the run by signing it through IAM `signJwt` as a signer service account that holds no roles.
  - A job can never mint its own token: no job account holds `signJwt`.
  - Database rules tie every change to a shared counter to an equal change of the caller's own run ledger, bounded per write and by the caps. §6.4 shows the rules and the attack cases they deny.
- **The worst case for a compromised run token** is that it pushes its repository's and the global day counters up by at most its per-run cap, in steps no larger than the per-write bound. It cannot lower anyone else's counters, cannot exceed a daily cap, cannot touch caps or kill switches, and does nothing once the token has expired.
- **Counters are integer micro-dollars in dated day nodes**, so no rollover race is possible.
- **Kill switches reach jobs through an RTDB event-stream listener**, within seconds.
- **Halted runs.** `halted` is a new run status. It is additive to the `result.json` schema. A halted run gets a draft PR with a halted report, or no PR at all if it stopped before its branch existed (exit 0).
- **`fugaro watch`** is a Bubble Tea TUI fed by RTDB's event stream. **History** is written by an end-of-day **history job** with its own small image. That job also sweeps stale registry entries against Cloud Run executions. **`fugaro report`** derives weeks, months and years on read.
- **M9 is opt-in.** With no `budget:` block, behaviour is exactly as it is today. An `observe` mode calibrates caps before `enforce` turns them on.
- **Slices.**
  - **M9a:** gateway, pinned models, per-run cap in-process, and `halted`, with no Firebase.
  - **M9b:** Firebase counters, daily caps, kill switches and `fugaro budget`.
  - **M9c:** `fugaro watch`.
  - **M9d:** Firestore history and `fugaro report`.
  - **M9e (optional):** changes to the task loop.

**The residual risk (D2, accepted).** The container is not a trust boundary against its own agent (§6.1). A prompt-injected agent can read the real credential from the runner's `/proc`, or on Vertex use the metadata server's token, and call the model **around** the gateway. The caps bound a runaway or honest agent absolutely. A compromised one is bounded only by provider-side limits. §11 describes the later hardening: an external gateway.

---

## 1. Goals and non-goals

**Goals**

- **Hard caps on model spend,** per run and per day, for each repository and globally. Caps are enforced before each call and hold across any number of concurrent runs.
- **Kill switches:** a global one and one per repository, each effective within seconds.
- **Fail closed:** after a 3-minute grace without the budget backend, runs halt, whatever their auth mode (D14).
- **Pinned models** for every stage. The gateway refuses any unpinned or unpriced model.
- **A live terminal view** of spend, caps, burn rate and running agents, grouped by repository.
- **A durable daily history,** with weekly, monthly and yearly figures derived on read.
- **One consistent account of cost** across `result.json`, `ls`, the PR report, the live counters and the history.

**Non-goals (M9)**

- **Deferred parallelism controls.** The redundant mode and per-batch concurrency are out. `max_parallel` stays.
- **Non-Claude models** (D10). Anthropic's gateway guide says it doesn't support Claude Code routed to non-Claude models.
- **Changes to the PR flow.** PRs stay drafts opened at finalize. There are no per-round PR comments (D15).
- **A web dashboard.**
- **Dollar caps, or any proxy, for `oauth` runs.**
- **Capping compute cost** (D7).
- **Caps that hold against a compromised agent in the container** (D2: later).

---

## 2. How the spec maps onto Fugaro

| Spec concept | Fugaro today | M9 |
|---|---|---|
| `project` | Nothing. A repository (slug §3.2) is the unit of jobs, IAM and state | **A repository**, keyed by its slug. Grouping repositories is a later `group` field on caps |
| `taskId` | Run ID `YYYYMMDD-HHMMSS-<hex>` | The run ID. Nodes are keyed `<slug>/<run-id>` |
| `coder` / `reviewer` | One `agent.model` for every stage | `agent.models.coder` (implement and fix) and `agent.models.reviewer` (review) (§2.1) |
| `provider` | Implied by `agent.auth` | Unchanged: the auth mode is the provider. Claude models only (D10) |
| `maxTokensPerCall` | None | `agent.max_output_tokens` per role, passed as `CLAUDE_CODE_MAX_OUTPUT_TOKENS`. The gateway refuses any request above it |
| Pinned IDs and a price table | Claude Code's `total_cost_usd` | An embedded price table plus owner overrides. Unpinned or unpriced models are rejected (§5.2) |
| `maxReviewRounds` | `agent.review_rounds` | Unchanged |
| Deterministic compile, test and lint | The agent runs `fugaro verify`, and §4.2 gates readiness on it | Kept. An optional verify gate before review is M9e |
| APPROVE / REJECT | `ship` / `changes` | Fugaro's names are kept |
| Draft PR from the start | PR opened at finalize | **Keep finalize-time PRs** (D15) |
| A structured comment per round | Findings go to the fix prompt and the final report | **The spec's structured format goes into the final report**, one section per round (D15) |
| HALTED | None | New status `halted` (§5.9) |
| RTDB `budget` / `runs` / `agents` | GCS only | Caps, kill switches, dated counters, run ledgers and the registry (§6.2) |
| `onDisconnect` | n/a | Not available over REST. Heartbeats plus a sweeper, cross-checked with Cloud Run executions (D12) |
| Firestore `spendDaily` | None | As the spec describes (§9) |

### 2.1 Per-stage models: the minimal config change

The repository's `fugaro.yaml`, which its writers control, says *which* models to use. It can never say *how much* may be spent: caps and prices belong to the owner. Otherwise a repository could lift its own limit.

```yaml
agent:
  auth: api-key                  # vertex | api-key | oauth (unchanged)
  model: claude-sonnet-5-5       # existing; the default for every role
  models:                        # new, optional; each falls back to agent.model
    coder: claude-sonnet-5-5     # implement and fix
    reviewer: claude-opus-5-5    # review
    background: <pinned-small-model>   # Claude Code's background requests
  max_output_tokens: { coder: 16000, reviewer: 8000 }   # new, optional, per call
  max_run_tokens: 0              # new, optional; the token cap for oauth runs (§5.8)
  review_rounds: 3
  max_budget_usd: 25             # unchanged: per stage, passed as claude --max-budget-usd
```

- **When the budget is on, every role must name an explicit model ID.** `validate` and the runner refuse any role that doesn't. They also refuse aliases such as `sonnet` or `opus`, and IDs that aren't in the effective price table. With the budget off, today's behaviour is unchanged.
- **The runner pins everything Claude Code might pick.** For each stage it passes `--model <role>` and sets:
  - `ANTHROPIC_DEFAULT_OPUS_MODEL`, `ANTHROPIC_DEFAULT_SONNET_MODEL` and `CLAUDE_CODE_SUBAGENT_MODEL` to the role's model;
  - `ANTHROPIC_DEFAULT_HAIKU_MODEL` to `background`.

  A `model: opus` in a repository's `.claude/agents/*.md` therefore still resolves to a pinned ID. These variables are already reserved (§5.1), so no workflow secret can override them.
- **The `model` override in `task.json`** means the coder model. It must be in the price table.

### 2.2 Why a gateway rather than Claude Code's cost figure

Claude Code reports `total_cost_usd` only when a stage ends, and `--max-budget-usd` applies to one process. Neither can refuse a call because of spend elsewhere in the fleet. The gateway sees every request and its streamed usage. Claude Code's figure becomes a cross-check: the runner warns when the two differ by more than 5% in a stage.

### 2.3 Lifecycle: what M9 adopts from the spec

| Spec rule | M9 |
|---|---|
| Open the PR as a draft at the start | **Not adopted** (D15). An early PR needs an empty commit and a push at bootstrap, it would leave empty drafts behind when a run crashes, and it reworks finalize and the M6 follow-up paths that were checked live. `fugaro watch` gives the live view instead |
| A structured findings comment per round | **The format is adopted, in the final report**: one section per round, with `<!-- fugaro:findings {...} -->`, and the findings go into `result.json` too |
| The reviewer never sees code that doesn't compile | Optional M9e: before review, check for a passing, clean `verify test` on HEAD. Without one, run a fix stage that doesn't count as a review round, capped by `agent.verify_retries` (default 3) |
| Give up after the last round: FAILED, draft PR | Already the case (§4.2) |
| HALTED: draft PR with a halted comment | Adopted (§5.9) |

---

## 3. Architecture

```
 local ─ fugaro run ── signJwt (signer SA) ──► custom token {slug, run, exp} ──► runs/<slug>/<run>/budget-token (GCS)
       ─ fugaro watch / budget / report ──(user IAM, REST + SSE)──► RTDB, Firestore
                                                                    ▲         ▲
 Cloud Run job ─ fugaro exec ── gateway 127.0.0.1 ──(run's ID token)┘         │
                     │            │                                             │
                     └─ claude -p ┘   ──► api.anthropic.com | Vertex AI          │
 Cloud Scheduler ─► history job (own image): rollover RTDB → Firestore, sweep ──┘
```

- **The gateway** is a `net/http` handler inside `fugaro exec`. It starts at bootstrap when `FUGARO_BUDGET_MODE` is set, and holds the lease, the price table and the real credential. It forwards requests byte for byte and streams responses back without buffering.
- **The run's Firebase identity** comes from a custom token that the launcher minted. The runner exchanges it for an ID token at bootstrap and keeps the ID token and refresh token only in memory.
- **The history job** is a Cloud Run job in the installation project with its own distroless image (D11), running `fugaro budget history`. Cloud Scheduler starts it daily for the rollover and every 15 minutes for the sweep. **It is a scheduled, admin-privileged job, not a long-running service.** It is the one exception to "no budget service": it holds `firebasedatabase.admin` on the FP. The optional budget service remains a documented later hardening (§11).
- **RTDB and Firestore** live in the installation's own Firebase project (FP), which is a separate GCP project (§6.0). The runs bucket, jobs, the history job and Scheduler stay in the installation project.

---

## 4. The budget model

| Cap | Set with | Bounds |
|---|---|---|
| `global.dailyUsd` | `fugaro budget set --global --daily` | The sum over all of the installation's repositories of reserved minus released per UTC day |
| `global.perRunUsd` | `--global --per-run` | Each run's lifetime reserved minus released, and the default for every repository |
| `repos/<slug>.dailyUsd` | `--repo R --daily` | This repository per day. A repository without one uses `defaults.repoDailyUsd`. With neither, it has **no budget and halts** (fail closed) |
| `repos/<slug>.perRunUsd` | `--repo R --per-run` | Per run. The effective cap is `min(repo, global)` |
| `limits.maxReserveMicros` | `--max-reserve` | The most one write may reserve (default $5). This bounds each step of a compromised token |

- **Kill switches:** `kill/global` and `kill/repos/<slug>`, each `{on, by, at, reason}`.
- **What a cap counts:** reserved minus released, in integer **micro-dollars (µ$)**. That is spent plus outstanding. Rules compare exact integers, so floating-point equality never matters.
- **The day** is the UTC epoch day number, `floor(now / 86 400 000)`, with a human date stored alongside. A lease belongs to the day it was granted on.
- **What counts toward caps:** model dollars on `api-key` and `vertex`. `oauth` notional dollars are recorded as `notional` and never capped. Compute cost is not capped (D7).

---

## 5. Enforcement: the gateway

### 5.1 What the agent sees

| Auth | Agent environment | Gateway upstream and credential |
|---|---|---|
| `api-key` | `ANTHROPIC_BASE_URL=http://127.0.0.1:<port>` and `ANTHROPIC_API_KEY=<random per-run gateway token>`. **The real key is removed from the agent's environment** | `https://api.anthropic.com`, with the runner's key |
| `vertex` | `CLAUDE_CODE_USE_VERTEX=1`, `ANTHROPIC_VERTEX_BASE_URL=http://127.0.0.1:<port>/v1`, `CLAUDE_CODE_SKIP_VERTEX_AUTH=1`, `CLOUD_ML_REGION` and `ANTHROPIC_VERTEX_PROJECT_ID` | The regional or global Vertex endpoint, with a token from the metadata server |
| `oauth` | Unchanged. **Never proxied** (§5.8) | n/a |

- **Allowed endpoints.** Per Anthropic's gateway guide:
  - `POST /v1/messages` and `/v1/messages/count_tokens`;
  - on Vertex, `:rawPredict`, `:streamRawPredict` and `count-tokens:rawPredict`;
  - `HEAD /api/hello` gets a local 200.

  Anything else returns 404. `count_tokens` is free and needs no reservation.
- **Forwarding follows the guide.**
  - `anthropic-version`, `anthropic-beta` and the body pass through unchanged, and **bodies are never rewritten**.
  - Responses stream without buffering, including keep-alive pings.
  - `retry-after`, `x-should-retry`, `anthropic-ratelimit-unified-*` and error bodies are passed through.
- **What the gateway parses.** From requests: `model` (on Vertex, from the URL), `max_tokens` and any 1-hour `cache_control`. From the stream: the `message_start`, `message_delta` and `error` events.
- **Attribution.** `x-claude-code-session-id` and `x-claude-code-agent-id` are logged with each call's usage.
- **Stopping the repository's settings from bypassing the gateway.** The repository's `.claude/settings.json` could set `env.ANTHROPIC_BASE_URL`, or on Vertex point Claude Code straight at Vertex. The runner therefore writes a **managed settings file** (`/etc/claude-code/managed-settings.json`) before the first stage, holding the gateway variables at the highest precedence (A6).

### 5.2 Model pinning and the price table

- **An allow-list per stage:** `{role model, background model}`. Any other model gets `400 invalid_request_error: "fugaro: model X is not pinned for stage review"`, and **the stage fails** (`failed`, a configuration error, not `halted`), per D8. Requests are never rewritten.
- **The price table** is embedded (`internal/pricing`), with its source and check date. Per model it holds:
  - `inputPerM` and `outputPerM`;
  - the multipliers `cacheWrite5m` (1.25×), `cacheWrite1h` (2×) and `cacheRead`. The read multiplier is per model: the docs give 0.1× with per-model exceptions such as 0.05×.
  - an optional long-context tier, `webSearchPer1k`, and aliases for each provider.
- **Owner overrides:** `model_prices` in the local config, passed to jobs and the history job as `FUGARO_MODEL_PRICES`, the same way `FUGARO_COMPUTE_PRICES` is. Repositories can't set prices.
- **An unknown serving model** (for example after a server-side fallback) is charged at the table's highest rates, flagged `priced_as: max`.

### 5.3 Two-level reservation

**Level 1: each call, in-process.** The gateway holds `lease{day, granted, used, reserved}`, and `free = granted − used − reserved`.

```
on request(model, body, maxTokens):
  if killed or halted: 403 "fugaro: budget halted: <reason>", x-should-retry: false
  w := worstCase(model, len(body), maxTokens, wants1hCache)          # §5.4
  while free < w: topUp(max(w − free, leaseSize()))                   # level 2; may halt
  reserved += w                                                       # mutex; parallel calls are independent
  stream upstream → client, tee-parsing usage
  actual := price(usage, servingModel)                                # §5.5
  reserved −= w; used += actual; queue usage for the next report
```

**Level 2: leases against RTDB, with the run's own token.** One atomic multi-path `PATCH` at the root (§6.3) makes four writes:

- **The run's ledger for the day:** `reserved += L`.
- **The run's lifetime ledger:** `reserved += L`.
- **The repository's day counter:** `counted += L`.
- **The global day counter:** `counted += L`.

The rules (§6.4) accept the write only if all of these hold:

- the token is valid and names this run;
- neither kill switch is on;
- `L ≤ maxReserve`;
- the run's lifetime `reserved − released ≤ perRun`;
- the new repository counter is within its daily cap;
- the new global counter is within the global daily cap;
- both counter deltas equal `L` exactly.

A refused write is re-read and evaluated locally, which tells a stale read (retry) apart from a real refusal (halt with the reason). See §6.3.

- **Lease size shrinks near a cap:** `clamp(5% of the smallest headroom, $0.25, $2.00)`, and never less than the call's worst case. When `L` doesn't fit but `w` does, the gateway asks for `w`. Before halting on a refusal, it retries once after 20 s, since other runs' releases may have landed.
- **Usage reports.** Every 15 s (the heartbeat), and at the end of each stage and of the run, the gateway writes `spent += actual`, `byModel` and the call counts. Moving spend from outstanding to spent doesn't change `counted`.
- **Releases.** At run end, and on a lease the gateway gives up, `released += unused`, and the repository and global `counted` fall by exactly that amount in the same write. The rules bound this by the run's own outstanding amount: `spent + released ≤ reserved`.
- **Crashes.** A crashed run releases nothing. Its outstanding amount stays counted, which errs high and never low. The sweeper marks the run `crashed` (§6.5).
- **Traffic.** Take 20 concurrent runs, each making a call every 5 s at $0.05. With $2 leases that is about 0.1 lease writes/s, plus 1.3 heartbeat writes/s. The spec's per-call transactions would need about 24/s.

### 5.4 The worst-case estimate

`worstCase = bodyBytes × inputRate × cacheMult + maxTokens × outputRate + (web search enabled ? searchCap × perSearch : 0)`

- **Body bytes bound the input tokens from above:** a token is at least one byte, and JSON and base64 only make bytes over-count more. This over-reserves by roughly 3–4×; the lease absorbs it and reconciliation refunds it.
- **`cacheMult`** is 2× if the request asks for a 1-hour cache TTL, else 1.25× if there is any `cache_control`, else 1.
- **The long-context tier** applies when the byte bound crosses its threshold.
- **`maxTokens`** comes from the body, and is refused if it's above the role's `max_output_tokens`. Thinking tokens are output tokens and fall within `max_tokens`.

### 5.5 Reconciling against the stream

- **Where usage comes from.** `message_start.message.usage` gives `input_tokens`, `cache_creation_input_tokens` (split into `cache_creation.ephemeral_5m_input_tokens` and `…_1h_…`), `cache_read_input_tokens` and `output_tokens`. `message_delta.usage` is **cumulative** and may update the input fields and add `server_tool_use.web_search_requests`. The last value of each field wins.
- **What sets the price.** Calls are priced at the **serving** model's rates and the tier for the input total.

| Case | Charge |
|---|---|
| The stream completes | Actual usage |
| An upstream error before `message_start` | 0 (A3) |
| An error or disconnect after `message_start` | Input tokens plus the **reserved** output tokens |
| The client or a stage kill cancels the call | As above, and the upstream request is cancelled |
| The runner dies | Nothing is released, so the outstanding amount stays counted |
| Claude Code retries (429, 529, a dropped stream) | Each attempt is a new request with its own reservation. The gateway never retries |
| Parallel subagent calls | Each reserves independently. Top-ups are serialized |

### 5.6 Other ties between the stages and the gateway

- **Per-stage `--max-budget-usd` stays** as defence in depth.
- **Where the per-run cap is checked.** In M9a it's in-process, against `FUGARO_MAX_RUN_USD` from the local config. From M9b the rules check the minimum of that and the RTDB caps.
- **`fugaro verify` tells the gateway when it starts and ends** (loopback, with the per-run gateway token). The registry then shows `implement · test`. This is cosmetic.

### 5.7 Budget modes

The local config's `budget.mode`:

- **`off`**: no gateway, the same as having no `budget:` block;
- **`observe`**: account and lease in RTDB, but **caps are advisory**. Would-be cap halts are logged and never refused; the observe rules variant drops only the cap checks. **Kill switches and fail-closed still apply:** an observe run halts on a kill, and halts when the backend is unreachable after the 3-minute grace (D14). So observe relaxes caps only, which is why it keeps its name. For behaviour with no budget at all, use `off`.
- **`enforce`**: everything in this document.

### 5.8 What `oauth` runs get (A1: the user accepted the risk)

`oauth` stays as it is today. Its traffic never passes through the gateway or any proxy.

| Mechanism | `oauth` runs |
|---|---|
| Gateway, reservation, per-call allow-list | **None** |
| Dollar caps (per run and daily) | **Not enforced.** Advisory only: at each stage end the runner records Claude Code's notional `total_cost_usd` as `notional` on the run and the repository and global day nodes (the rules allow increases only, with no cap check), and `modelUsage` for `byModel` |
| Per-stage `--max-budget-usd` | **Enforced by Claude Code**, in notional dollars (as today) |
| Token cap | **Enforced at stage boundaries:** `agent.max_run_tokens`, summed from each stage's result `usage` (input, cache and output tokens). A run can overshoot by at most one stage. Crossing it halts the run (`halt.reason: token_cap`) |
| Time caps | Stage and total timeouts, as today |
| Model pinning | `--model` and the pin variables (§2.1). There is no gateway backstop, so a model name that escapes pinning isn't blocked |
| Kill switches | **Enforced**, through the same RTDB listener |
| Registry, history, watch | Yes, with a `notional` label |
| Fail closed (D14) | Yes: halt after the 3-minute grace |

### 5.9 Halted: the runner, `result.json` and readiness (§4.2)

- **How a run halts.** A refusal, a kill or a token cap cancels the stage context with `ErrHalted{reason}`. This is the same pattern as `ErrCancelled`. The process group is killed, the loop ends, and **finalize runs normally**: it makes no model calls, so a kill during finalize or writeback is ignored.
- **Status `halted`,** with one of two outcomes:
  - `draft`: the branch exists. The run pushes and opens a draft PR.
  - `none`: the run stopped at bootstrap before the lock and branch (a kill switch, no cap, an expired token, the backend unreachable after the grace), or it was a follow-up that stopped before its push. **No PR is opened, and the exit code is 0** (D9).
- **Precedence.** Whichever cause cancelled the stage first wins, cancel or halt. A halted run is never ready.
- **The report** starts `## Fugaro — Halted: <reason>`, followed by the cap or switch, the time, this run's spend, and how to continue (`fugaro budget …`, then `fugaro run --pr N`). A halted run pushed its work, so follow-ups work as in §4.4.
- **Schema changes** (`result.json` stays at version 1, and every change is additive):
  - `status` gains `halted`;
  - a new `halt: {reason, scope, at, detail}`, where `reason` is one of `kill_switch`, `run_cap`, `repo_daily_cap`, `global_daily_cap`, `no_cap`, `token_cap`, `budget_unavailable` or `budget_token_expired`;
  - `cost` gains `model_source: gateway|claude-code`, `model_by` and `unreconciled`.
- **Older CLIs** show `halted` as a terminal status: `runview` passes through any non-running status and marks it terminal.
- **Launch pre-check.** `fugaro run` refuses (exit 1) when the repository is killed, has no cap, or has less than $0.25 of daily headroom. When it can't read RTDB it exits 2, since it fails closed. `--no-budget-check` skips only this client-side check.

### 5.10 How the kill switch reaches a run

- **Listening.** The runner holds one REST event stream on `config/kill` (its rules allow reading `kill/global` and its own repository's switch), authenticated with the run's ID token. It reconnects when it sees `auth_revoked` or when the token expires, about hourly.
- **Stopping.** On `on: true`, the runner halts the stage and the gateway cancels its in-flight streams. Latency is seconds, plus the 10-second SIGTERM grace.
- **Polling as a backstop.** The heartbeat write re-reads the switch every 15 s, in case the stream is silently stale.

---

## 6. Firebase

### 6.0 Where Firebase lives (D3, settled)

**Each installation gets its own dedicated Firebase project (FP).** One GCP project is one Fugaro installation, which has exactly one FP. All users of that installation share it.

- **"Global" means the installation,** and the installation's repositories are the spec's "projects".
- **There is no live state across installations.** Aggregating across an organization comes later and is read-only: for example, each installation's history job exports its daily `spendDaily` roll-up to one shared place.

**What creating the FP takes.**

1. **The user creates it,** as a new GCP project.
   - This needs `resourcemanager.projects.create` on the parent. We recommend the same organization or folder as the installation, so the same org policies apply. A user without an organization creates it without a parent.
   - Only people who hold that right can do this. `fugaro init` never creates projects.
2. **The user links a billing account** (Blaze plan). This needs `billing.resourceAssociations.create` on the billing account (Billing Account User) and on the project.
   - `fugaro init` never enables billing (§8.2), and that rule stays.
   - Blaze is needed because Spark's hard cap of 100 concurrent RTDB connections would make a busy fleet fail closed.
   - The user adds a billing budget alert.
3. **`fugaro init --firebase <fp-id>` adopts that project.**
   - Discovery checks the project exists, has billing, and is empty or carries our mark (§6.7).
   - A Terraform root then enables Firebase on it (`google_firebase_project`, google-beta) and creates everything in §6.6.

   We rejected having `init` create the project itself through `google_project`. That would need project-creation and billing rights on the person running init, and it would break init's rule of never enabling billing.
4. **Org policies.** Nothing in the design creates service-account keys: signing goes through `signJwt`, so a "disable key creation" policy is fine. A domain-restricted-sharing policy must allow the installation's members.

**Considered and rejected:**

- **V2: Firebase inside the installation's own GCP project.** Rejected because the user wants a separate project.
- **V3: one FP shared by several installations.** Rejected because it adds a shared blast radius, an installation level in the rules, and cross-installation admin ambiguity. Cross-installation views come later, read-only, as described above.

### 6.1 APIs, region and IAM

- **APIs on the FP:** `firebase`, `firebasedatabase`, `firestore`, `firebaserules` (for Firestore rules) and `identitytoolkit` plus `securetoken` (for custom-token sign-in). `iamcredentials` must be enabled where the signer account lives.
- **Regions (D4).** RTDB goes in `us-central1`: RTDB offers only `us-central1`, `europe-west1` and `asia-southeast1`, and a location can't be changed later. Firestore goes in `us-east5`, which Firestore lists as a regional location, with `nam5` as the fallback for installations elsewhere.
- **IAM.** Each grant is an `*_iam_member` in the Firebase root (§6.6). Every grant to an installation principal is cross-project, onto the FP.

| Principal | Grants |
|---|---|
| `fugaro-token-signer` (a new account in the FP, display name `Fugaro token signer`) | **No roles at all.** It exists only as the key that signs custom tokens |
| Launchers and operators | `roles/firebasedatabase.viewer` and `roles/datastore.viewer` on the FP, plus the custom role **`fugaroTokenMinter`** (`iam.serviceAccounts.signJwt` only) **on the signer account**. That is narrower than `serviceAccountTokenCreator`, which would also let them mint access tokens |
| `fugaro-history` (the history job's account) | `roles/firebasedatabase.admin`, `roles/datastore.user` and `roles/firebaseauth.admin` (to delete expired run users) on the FP. In the installation project, `fugaroLauncher` (`run.executions.list`, for the cross-check) |
| `fugaro-scheduler` | `roles/run.invoker` on the history job |
| Job accounts | **Nothing on the FP.** They hold a Firebase ID token, which is not an IAM identity |
| Budget admins (D6) | `roles/firebasedatabase.admin` and `roles/datastore.viewer` on the FP, granted by the Firebase root to: the installation project's `roles/owner` and `roles/editor` members, which `init --firebase` reads from the installation's IAM policy and passes as tfvars; and `terraform.budget_admins`. The FP's own owners and editors (at least the person who created it) hold admin implicitly |
| **Who can't** change caps or kill switches | Launchers and operators who aren't listed (they get the viewer role and the minter role only); job accounts; `fugaro-scheduler`. The history account holds `firebasedatabase.admin` for its sweep and rollover. Its code never writes `/config`, but IAM can't enforce that: a compromise of the history image or its account could lift caps (§11) |

### 6.2 Data model

**RTDB.** Amounts are integers in µ$. Days are UTC epoch day numbers. Keys are escaped (`.` becomes `%2E`).

```
/config                                    # admins only (IAM REST); run tokens read own slice
  caps/global          {dailyMicros, perRunMicros}
  caps/defaults        {repoDailyMicros, repoPerRunMicros}
  caps/repos/<slug>    {dailyMicros, perRunMicros, repo}
  limits               {maxReserveMicros}
  kill/global          {on, by, at, reason}
  kill/repos/<slug>    {on, by, at, reason}
/runs/<slug>/<run>                         # lifetime ledger; the run's token only
  {reserved, released, spent, notional, overrun, tokens, exp}
/spend/<day>/global                        # {counted, spent, notional, calls}
/spend/<day>/repos/<slug>                  # {counted, spent, notional, calls, byModel/<m>/{micros,in,out,cr,cw}}
/spend/<day>/runs/<slug>/<run>             # {reserved, released, spent}: the run's share of the day
/spend/<day>/meta                          # {date: "YYYY-MM-DD", archived}  (the history job)
/agents/<slug>/<run>                       # the registry, written by its run
  {repo, workflow, title, stage, round, verify, coder, reviewer, auth, prUrl,
   startedAt, stageStartedAt, stageDeadline, updatedAt, spent, halted}
/outcomes/<day>/<slug>/<run>               # {status}: written once at run end; counted by the history job
```

- **`title`** is the task's first line, clipped to 80 characters and redacted. Launchers already read every `task.json`.
- **Day nodes older than 8 days** are deleted by the history job once they're archived.

**Firestore** (the `(default)` database):

`spendDaily/<YYYY-MM-DD>_<slug>` holds `{repo, slug, date, spentUsd, notionalUsd, computeUsd, unreconciledUsd, overrunUsd, calls, runs, outcomes{succeeded, failed, halted, cancelled, infra_error}, byModel{…}, capDailyUsd, archivedAt, version: 1}`.

The global figure is computed on read. There is no composite index: queries use document-ID ranges.

### 6.3 Transactions over REST

- **What the Go SDK can't do.** The Firebase Admin Go SDK's RTDB client is REST-only, with no `onDisconnect`, and it authenticates as an admin. Runs need their own ID token, so Fugaro ships a small REST client of its own (`internal/rtdb`, about 350 lines). It does:
  - `GET` (with `X-Firebase-ETag: true` when an ETag is wanted);
  - `PUT` with `if-match`;
  - multi-path `PATCH` at the root;
  - streaming (`Accept: text/event-stream`, with the events `put`, `patch`, `keep-alive`, `cancel` and `auth_revoked`);
  - authentication with `auth=<ID token>` for runs, or an OAuth `access_token` for people and the history job.
- **Where ETags fit.** ETag conditional writes work on **one location**, and need that whole location read first. A lease touches four nodes, and reading their common ancestor would expose other repositories' counters. So ETags serve only single-node admin writes (`budget set` on a cap node).
- **The lease write uses rules as its compare-and-set.** The write carries **absolute** new values, computed from what the client just read. The rules demand `N(counter) − O(counter) == N(own run) − O(own run)`, where `O` is the stored value and `N` the value after the write. If any other run changed the shared counter in between, the delta check fails and the write is denied, so a stale write can never land. One multi-path update commits or fails as a whole (A4).
- **Telling a stale read from a refusal.** Both come back as `401 Permission denied`. The client then re-reads the five nodes involved (its run, the repository and global day counters, its caps, the kill switches) and evaluates the same predicates locally:
  - if they pass, the read was stale: retry, up to 8 times with jitter;
  - if they fail, it's a refusal, with the local reason;
  - after 8 stale retries, treat it as `budget_unavailable` (contention).
- **Contention.** Every lease in the fleet writes the global counter. That is about 0.1–1 writes/s at the expected scale, well within what optimistic retry handles.

### 6.4 Security: per-run tokens and database rules (D1)

**Minting (the launcher only).**

- `fugaro run` and `run --retry`, after the launch claim and before `jobs.run`, build a custom-token JWT:
  - `iss` and `sub` are the signer's email; `aud` is Identity Toolkit's audience;
  - `uid` is `r~<slug>~<run-id>`;
  - `claims` is `{fs: <slug>, fr: <run-id>, fx: <expiry in ms>}`;
  - `exp` is `iat + 1h`.
- **Who signs.** It is signed through the IAM Credentials `signJwt` call **as `fugaro-token-signer`**, which needs `fugaroTokenMinter` on that account. **No job account holds `signJwt`**, so a job can never mint a token.
- **Lifetime.** Firebase custom tokens are valid for **one hour at most** (A12), so the run deadline is enforced by the `fx` claim instead. `fx = launch time + task timeout (jobs.get: total + 2m) + 1h queueing allowance + 5m slack`. The rules require `auth.token.fx > now`. An ID token refreshes itself, but it is useless after `fx`, and the history job deletes the run's user two days later.
- **Delivery.** The token is written as `runs/<slug>/<run>/budget-token`, create-if-absent, before the launch. **It is not an env override,** because `jobs.run` request bodies can reach audit logs that every project log viewer can read.
  - At bootstrap the runner reads the object, **deletes it**, and exchanges the token through `accounts:signInWithCustomToken`. That needs the FP's web API key, which isn't secret but should be restricted to Identity Toolkit and Secure Token. The job gets it in its environment through the tfvars.
  - An exchange attempted more than an hour after minting fails: the run halts, `budget_token_expired`, outcome `none`, and the message says to launch again.
  - The token and ID token are registered with the redactor.
- **Refresh versus re-minting.** The run legitimately keeps its identity alive with the Firebase **refresh token** (Secure Token API). Refreshing keeps the same uid and claims, so the run can outlive the one-hour custom token without minting anything.
  - **Mint-on-start was considered and rejected.** A run can't mint its own custom token at bootstrap. `signJwt` can't be scoped by claims, so a job account holding it could mint tokens for any run, any repository, or any `fx`. Making that safe needs a minting endpoint that checks the caller's ID token and execution, which is exactly the budget service D1 rejected.
  - **What queued runs cost.** A run queued for more than an hour halts (`budget_token_expired`), and `fugaro run --retry` can't relaunch it, because it has launched. Launching again costs a new run ID.
- **Who can reach the token.** Launchers, and other runs of the same repository during the queue window, can read the object. §6.1 already accepts the repository as the boundary between runs. A token taken this way acts only as *that* run, within its caps.

**The rules.** The rules file is generated from a Go template (`internal/budget/rules`) and deployed by `fugaro init` with `PUT /.settings/rules.json`. Terraform doesn't manage RTDB rules. The fragments below use four macros; the generator expands each into `root.child(…)` and `newData.parent()…` chains:

- **`O(p)`**: the stored value at `p`, or 0 if absent.
- **`N(p)`**: the value at `p` after the write.
- **`RUN`**: `auth != null && auth.token.fs == $slug && auth.token.fr == $run && auth.token.fx > now`.
- **`TODAY`**: `$day == '' + ((now - now % 86400000) / 86400000)`.

**Rules in outline:**

```
/config/…            .read: caps/global, caps/defaults, limits, kill/global → any valid run token;
                      caps/repos/$slug, kill/repos/$slug → a token with fs == $slug.  .write: false (admins use IAM, which bypasses rules)
/runs/$slug/$run     .read: RUN   .write: RUN && newData.exists()
  day        .validate: the day node this write touches (today, or yesterday for spent/released only); $day below = N(day)
  reserved   .validate: N ≥ O  &&  N − O ≤ limits.maxReserveMicros
                         && (N == O || (TODAY-lease && kill/global.on != true && kill/repos/$slug.on != true))
                         && N − N(released) ≤ min(caps/repos/$slug.perRunMicros ?? defaults, caps/global.perRunMicros)
                         && N − O == N(spend/$day/runs/$slug/$run/reserved) − O(same)      # moves with the day share
  released   .validate: N ≥ O && N + N(spent) ≤ N(reserved)
                         && N − O == N(spend/$day/runs/$slug/$run/released) − O(same)
  spent      .validate: N ≥ O && N + N(released) ≤ N(reserved)
  notional, overrun, tokens   .validate: N ≥ O && N − O ≤ limits.maxReserveMicros
/spend/$day/runs/$slug/$run   .write: RUN && ($day is today or yesterday) && newData.exists()
  reserved   .validate: N ≥ O && (N == O || TODAY)
  released   .validate: N ≥ O && N + N(spent) ≤ N(reserved)
  (both)     .validate: let d = (N(reserved) − O(reserved)) − (N(released) − O(released));
                        N(../../repos/$slug/counted) − O(same) == d  &&  N(../../global/counted) − O(same) == d
                        # the share can't move unless both shared counters move by exactly d, and vice versa below
/spend/$day/repos/$slug/counted   .write: auth.token.fs == $slug && auth.token.fx > now
  .validate: N − O == (N(../../runs/$slug/<fr>/reserved) − O(same)) − (N(../../runs/$slug/<fr>/released) − O(same))
             && (N ≤ O || N ≤ caps/repos/$slug.dailyMicros ?? caps/defaults.repoDailyMicros)
/spend/$day/global/counted        .write: auth.token.fx > now
  .validate: the same delta equation, using the caller's fs/fr, && (N ≤ O || N ≤ caps/global.dailyMicros)
/spend/$day/{global,repos/$slug}/{spent,notional,calls,byModel/…}
  .validate: N ≥ O && N − O ≤ limits.maxReserveMicros × (the calls-field limit for calls)
/agents/$slug/$run   .write: RUN   .validate: every string ≤ 200 characters, and the fields and types fixed
/outcomes/$day/$slug/$run   .write: RUN && !data.exists()   (written once)
```

Where a counter's cap is missing, the value comes back `null`. `N ≤ null` is false, so the write is denied: the rules fail closed.

**What the rules deny.** Take a run A of repository X, with caps global daily $150, X daily $60, per-run $20 and `maxReserve` $5:

| # | Attempt by A's token | Why the rules deny it |
|---|---|---|
| 1 | Lower `spend/d/global/counted` by $5, changing nothing else | The global counter's delta must equal A's own `reserved − released` delta, which here is 0 |
| 2 | Release $5 and lower global and X by $5, with only $2 outstanding | The `released` rule: `released + spent ≤ reserved` fails. **Refunds are bounded by the run's own outstanding reservation** |
| 3 | Write run B's ledger, or `repos/Y/counted` | `.write` needs `fr == B`, or `fs == Y` |
| 4 | Reserve $6 in one write | `N − O ≤ maxReserve` |
| 5 | Reserve past $20 over the run's life | The per-run check on `/runs/X/A/reserved` |
| 6 | Reserve with X at $59 of $60 | The repository counter would exceed the daily cap |
| 7 | Reserve against tomorrow's or yesterday's node, to dodge today's cap | Only `TODAY` allows an **increase** of `reserved`. Old days allow only `spent` and `released` |
| 8 | Raise its own `reserved` without raising the shared counters | The run's `reserved` must move with its day share, and each shared counter's delta is checked against that share, so a write that leaves them unchanged fails. In the other direction, a counter can only change by exactly the run's delta |
| 9 | Set `config/kill/*/on` to false, or raise a cap | `/config` has `.write: false`. Only admins write it, over IAM REST |
| 10 | Reserve after the kill switch is on | The `reserved` rule checks both switches |
| 11 | Use the token after its deadline | `auth.token.fx > now` fails everywhere |
| 12 | Delete its own ledger to reset the per-run check | `.write` requires `newData.exists()` (validation rules don't run on a delete) |

**The worst case for a compromised run token, in numbers.** With the caps above:

- **Raise its repository's and the global day counters by at most $20** over its lifetime (the per-run cap), in steps of at most $5, and never past $60 for X or $150 globally.
- **Lower a shared counter only by releasing its own outstanding reservation.** Its net contribution is `reserved − released ≥ spent ≥ 0`, so it can never take a counter below where it found it.
- **Read** the global and X day counters, X's and the global caps and switches, and its own nodes. It reads nothing of other repositories.
- **Write** its own registry entry (the text is sanitized in watch) and `notional`, `overrun` and `tokens`, which are increase-only and bounded per write. These fields feed no cap.
- **Nothing** after `fx`.

**Limits of the rules:**

- **Denial of service.** k compromised concurrent runs can fill at most `k × perRun` of the global day.
- **Reports aren't checked against real usage.** The rules can't verify that `spent` matches real usage, which is D2's residual.
- **Launchers can mint tokens for arbitrary run IDs.** Each can fill its per-run cap, so a launcher can exhaust the counters (denial of service). A launcher can't spend real money, though: launchers hold no model credential.

**A budget service stays documented as later hardening.** A Cloud Run service that owns every write would remove all token and rules logic from jobs, and could host the external gateway (§11).

### 6.5 The agent registry without `onDisconnect`

- **The registry entry's life.**
  - At bootstrap, after the token exchange, the runner creates its entry.
  - A heartbeat every 15 s updates the stage, round, verify step, spend and `updatedAt`.
  - At run end the runner writes `outcomes/<day>/<slug>/<run>` and deletes its entry.
- **How watch flags a run.** It shows an entry as **silent** after 60 s without a heartbeat, and as **lost** after 3 minutes.
- **The sweeper.** The history job runs every 15 minutes (D12). It lists the installation's Cloud Run executions (as `ls` does) and removes the registry entries of executions that have ended or no longer exist. It records `crashed` on their `/runs` ledgers, whose outstanding amounts stay counted, and writes an `infra_error` outcome. It also deletes the auth users of runs older than 2 days.

### 6.6 Terraform

- **The Firebase root's content:**
  - the APIs (§6.1);
  - `google_firebase_project` and `google_firebase_database_instance`, both **google-beta** (A5);
  - `google_firestore_database`, Native mode, with `DELETE_PROTECTION_ENABLED` and `prevent_destroy`;
  - Firestore rules that deny everything (`google_firebaserules_ruleset` and `google_firebaserules_release`);
  - the signer account, the `fugaroTokenMinter` role and its grants;
  - the history job's account, its job (its own image, D11) and two Scheduler jobs (`30 0 * * *` for the rollover, `*/15 * * * *` for the sweep), running as `fugaro-scheduler`;
  - the grants in §6.1;
  - in the repository module, the jobs' environment: `FUGARO_BUDGET_MODE`, `FUGARO_RTDB_URL`, `FUGARO_FIREBASE_API_KEY`, `FUGARO_MODEL_PRICES` and `FUGARO_MAX_RUN_USD`.
- **Marks.** The instance can't carry labels, so `init` writes `/fugaro/mark` into the database and discovery checks it. Everything else carries `fugaro=managed`, or its account display name.
- **A third Terraform root, `roots/firebase`,** with its state at `fugaro/firebase` in the installation's state bucket, which is operator-only as in §8.1.
  - **Providers.** Its providers (`google` and `google-beta`, pinned) target the FP, with `project`, `billing_project` and `user_project_override`.
  - **Inputs.** It receives, as tfvars, the installation's outputs: launchers, operators, `budget_admins`, the discovered owners and editors, the history account's email and the FP ID. There is no remote-state coupling, as in §8.1.
  - **What it holds.** Everything that lives in the FP: the APIs, the Firebase project, RTDB, Firestore, Firestore rules, the signer and the minter role, and every grant onto the FP.
- **The installation root** (gated by `enable_budget`) holds the history account, the history job and its two Scheduler jobs, and the history account's `fugaroLauncher` grant. Its inputs include the Firebase root's outputs (`rtdb_url`, `firebase_api_key`, `token_signer`, `firestore_database`).
- **The repository root** passes the jobs' budget environment from the same outputs. Job accounts get **no** grant on the FP.

### 6.7 `fugaro init`

- **Adoption and marks:**
  - **Discovery adopts** an existing FP's default RTDB instance and `(default)` Firestore database when they're empty or carry our mark. It **refuses** unmarked data, and refuses a Firestore database whose location isn't the one D4 chose.
  - **After the apply,** init deploys the RTDB rules and the mark.
  - **`--budget-mode observe|enforce`** sets the mode. `--budget-admin` repeats, and fills `terraform.budget_admins`.
  - **The guard** adds the database and instance to its `prevent_destroy` list.
  - **`init --repo`** refuses `enforce` for a workflow whose image record names a pre-M9 runner.
- **`fugaro init --firebase <fp-id>`** runs three applies, each with its own plan and confirmation:
  1. **The installation root,** which creates the history account, so the account exists before it is granted anything.
  2. **The Firebase root,** then the RTDB rules deployment and the mark.
  3. **The installation root again,** which deploys the history job with the Firebase outputs.

  Then init writes the local config. Each repository then needs `fugaro init --repo` to pick up the job environment.
- **Who runs it.** It needs the same rights as `fugaro init` on the installation, plus IAM administration on the FP (Owner, or Firebase Admin with Project IAM Admin, Service Account Admin and Role Admin).
- **Discovery on the FP** refuses a project that:
  - doesn't exist;
  - has no billing;
  - already has Firebase with unmarked RTDB data or a non-empty unmarked Firestore database;
  - has a Firestore location other than D4's.
- **Changing owners.** Re-run `init --firebase` after changing the installation's owners or editors, so the FP's admin grants follow them. The guard lists a removed admin grant under "⚠ Review these first".
- **Local config:**

  ```yaml
  budget:
    mode: enforce                    # off | observe | enforce
    firebase_project: my-fugaro-fp   # the installation's dedicated Firebase project (D3)
    rtdb_url: https://<instance>.firebaseio.com
    firestore_database: "(default)"
    firebase_api_key: <web-api-key>  # not a secret; restricted to identitytoolkit and securetoken
    token_signer: fugaro-token-signer@<fp>.iam.gserviceaccount.com
    heartbeat: 15s
    unreachable_grace: 3m
    per_run_usd: 20                  # M9a in-process cap; with M9b the effective cap is min(this, RTDB caps)
  model_prices: {}
  ```

### 6.8 Cost at expected scale

These estimates assume 20 repositories, about 100 runs a day of about 45 minutes each, and 5 people watching for 2 hours a day.

- **RTDB.** Under 1 MB stored. Downloads come from the jobs' kill-switch streams (nearly idle), their re-reads, and the watch streams: under 2 GB a month, about $2. Concurrent connections are about 20 jobs plus a few viewers. The Spark plan's hard limit of 100 connections would make a busy fleet fail closed, so **use Blaze**, with a billing budget alert.
- **Firestore.** About 600 writes a month, which is within the free tier.
- **Firebase Auth.** Custom-token sign-in is free at this volume (about 3,000 users a month, deleted after two days).
- **History job.** About 3,000 short executions a month, a few dollars at most.
- **Scheduler.** Two more jobs, $0.20 a month.
- **Total:** under $10 a month, billed to the FP's billing account; the history job's executions bill to the installation's. A new project costs nothing by itself.

---

## 7. `fugaro watch`

- **Bubble Tea** (D13), with `lipgloss`, `bubbles` and `teatest` for golden frames. These are pinned and reviewed as new dependencies.
- **Data comes from three event streams** under the viewer's IAM token (A2): `/config`, `/spend/<today>` (re-subscribed at UTC midnight) and `/agents`. After more than 30 s without events, the header shows `⚠ live data stale`.
- **Layout.** This follows the spec: a global line, then each repository with its cap bar and burn rate, then its runs with stage, round, models, spend and age.
  - A killed repository shows `KILLED by <who> <time> "<reason>"`.
  - `oauth` runs show their spend as notional.
- **Burn rate** is computed client-side over a rolling 5-minute window of `counted`, and highlighted above `watch.burn_alert` (default: the daily cap spread over 8 hours).
- **Stuck runs.** A run is flagged silent or lost (§6.5), or amber past 80% of its stage deadline. `l` cross-checks a lost run against Cloud Run executions.
- **Keys.**
  - `k` kills everything, after you type `kill`.
  - `K` kills the selected repository, after you confirm with `y`.
  - `r` and `R` resume, after you type the repository name, or `resume` for global.
  - Writes use the viewer's IAM. Without admin rights, the key shows "you are not a budget admin".
- **Untrusted text.** Every job-written field is stripped of control and escape characters and clipped, so a compromised run can't inject terminal escapes.
- **Without Firebase,** watch polls `ls` data every 10 s: runs grouped the same way, with each run's cost from `result.json`, no caps, and the kill keys disabled.

## 8. `fugaro budget`

| Command | Behaviour | Needs |
|---|---|---|
| `budget show [--repo R \| --all] [--json]` | Caps, today's counted and spent, notional spend, kill switches, headroom and active runs | Viewer |
| `budget set (--global \| --defaults \| --repo R) [--daily USD] [--per-run USD] [--max-reserve USD] [--clear]` | An ETag-guarded `PUT` of the cap node, showing old and new values. **Raising** a cap needs typed confirmation (or `--yes`); lowering one doesn't | Admin (D6) |
| `budget kill (--all \| --repo R) [--reason TEXT]` | Sets the switch, recording who, when and why. Takes effect within seconds | Admin |
| `budget resume (--all \| --repo R)` | Clears the switch, after typed confirmation | Admin |
| `budget prices [--json]` | The effective price table, with sources and check dates. Warns when a check date is over 90 days old | None |
| `budget history --rollover \| --sweep` | The history job's modes (hidden) | History account |

Caps must be finite, non-negative and at most $100,000, and a per-run cap can't exceed the daily cap in the same scope. An admin is someone Terraform granted `roles/firebasedatabase.admin` on the FP (§6.1): the installation's owners and editors, plus `terraform.budget_admins`.

## 9. History and `fugaro report`

- **The daily rollover** runs at 00:30 UTC. For each day in the last 7 days that isn't archived, the history job:
  1. counts `outcomes/<day>`;
  2. reads `spend/<day>`;
  3. adds `computeUsd` from the runs' `result.json` records, since the job can read the runs bucket;
  4. `Set`s `spendDaily/<date>_<slug>`, which is idempotent because the document ID is deterministic;
  5. marks the day archived;
  6. prunes day nodes older than 8 days.

  It can be rerun, and `--day` backfills.
- **Unreconciled spend.** Crashed runs' outstanding amounts go into `unreconciledUsd`, and gateway `overrun` into `overrunUsd`.
- **`fugaro report`** takes `[--repo R | --all] [--week | --month | --year | --from D --to D] [--by day|week|month|repo|model] [--json]`.
  - It reads Firestore with viewer IAM, and adds today's partial data from RTDB, marked `(partial)`.
  - Weeks are ISO weeks in UTC.
  - Totals separate billed, notional and estimated compute.
  - `--by model` shows the coder/reviewer split.

## 10. One account of cost

| Figure | Source of truth | Also shown in |
|---|---|---|
| A run's model dollars (`api-key`, `vertex` with the budget on) | The gateway's per-run ledger | `result.json` `cost.model_usd` (`model_source: gateway`), the PR report, `ls`, and `/runs/<slug>/<run>.spent` |
| Model dollars without a gateway (`oauth`, or the budget off) | Claude Code's `total_cost_usd` | The same places, with `model_source: claude-code`. `oauth` stays `model_basis: subscription` (notional) |
| Dollars counted against caps | RTDB `counted` (spent plus outstanding) | watch, `budget show` |
| History | Firestore, copied from RTDB | `report` |
| Compute | An estimate, as in §10.1 | Unchanged, plus `computeUsd` in the history. Never capped |

`ls` totals and `report` differ only by unreconciled and overrun amounts, which `report` shows separately. `cost_usd` remains the model figure.

## 11. Threat model

The attacker is a compromised agent in run A. It runs as the same user as the runner, can reach the metadata server, and has unrestricted network egress. It can read A's Firebase ID token and refresh token from the runner's process, and the custom token too if it gets there before the runner deletes it at bootstrap.

| Question | Answer |
|---|---|
| Read other repositories' budgets or registry entries? | **No.** The rules scope reads to its own slug, global and itself (§6.4) |
| Lift a cap or clear a kill switch? | **No.** `/config` is IAM-admin only |
| Lower shared counters or other runs' counters? | **No.** It can only release its own outstanding reservation (denial table rows 1–3 and 8) |
| Exhaust the daily caps (denial of service)? | **By at most its per-run cap** (e.g. $20 of a $150 global day) |
| Mint tokens for other runs? | **No.** No job account holds `signJwt` |
| Steal another run's token? | Only a queued run of the **same repository**, during its queue window (the bucket object). The same per-repository boundary as §6.1 |
| Spend money around the gateway? | **Yes. This is D2's accepted residual.** The API key is in the runner's environment; on Vertex, the metadata server's token works directly |
| Inject into watch? | No. The text is sanitized |
| A compromised history job, or its image | It holds `firebasedatabase.admin` on the FP, so it **could** lift caps or clear switches. This is mitigated by: its own distroless image, pinned by digest in the base registry (which only operators write); no model credential; and no agent code in the job. It is the most privileged new identity M9 adds |

**Backstops now:**

- **`api-key`:** one Anthropic Console workspace per repository, each with its own spend limit (A7), which the provider enforces.
- **`vertex`:** per-model quotas plus the existing billing-budget alerts.

**Later hardening (D2):**

1. A gateway sidecar for `api-key`, so the agent's container never holds the key (A8).
2. **An external gateway:** a budget service that proxies model traffic and owns every Firebase write. It replaces the per-run tokens and would make the caps hard for both API-key and Vertex, since job accounts would then need neither the key nor `aiplatform.user`.

## 12. Failure modes

| Failure | Behaviour |
|---|---|
| RTDB unreachable with runs in flight (the "budget backend", D14) | The gateway spends the lease it already holds, which is already counted. A top-up or heartbeat retries with backoff for **3 minutes**, then the run **halts** (`budget_unavailable`) with a draft PR. This applies to **all auth modes**: `oauth` runs halt too |
| Unreachable at bootstrap | The token exchange or the first registry write retries for 3 minutes, then the run halts with outcome `none`, exit 0 |
| Unreachable at launch | `fugaro run` exits 2. `--no-budget-check` leaves it to the runner |
| Identity Toolkit unreachable at bootstrap | The same as RTDB being unreachable |
| Custom token older than 1 h at bootstrap (queued too long) | Halted, `budget_token_expired`, outcome `none`. The message says to launch again |
| **Image checks and rebuilds, `verify`, finalize, writeback** | **Unaffected.** None of them makes model calls or uses the budget |
| Contention (8 stale retries) | Treated like an unreachable backend, with the same grace |
| Hard kill (a task timeout or out-of-memory) | Nothing is released. The sweeper records `crashed` and `infra_error`, and the amount stays counted, erring high |
| Firestore down during the rollover | Retried next day (7-day lookback), and RTDB keeps 8 days |
| Clock skew | The rules use the server's `now`. The client takes the server's time from the `Date` header |
| Price table stale | Check dates, a warning after 90 days, and the Claude Code cross-check |
| Watch loses its stream | A header warning and reconnection. Nothing in the cloud is affected |

## 13. Backward compatibility and rollout

- **Off by default.** With no `budget:` block, no gateway runs and no token is minted. The agent's environment is exactly as today, which a golden test pins. `watch` degrades; `budget` and `report` explain how to set it up.
- **Additive schemas.** `result.json` stays at version 1. The new `fugaro.yaml` keys are optional. Older runners decode `fugaro.yaml` strictly and so refuse unknown keys: **repositories should adopt the new keys only after their images carry the M9 runner.** `init --repo` refuses `enforce` for a pre-M9 image.
- **The rollout.**
  1. Create the FP and link billing (§6.0). Run `fugaro init --firebase <fp-id> --budget-mode observe`.
  2. Set generous caps.
  3. Rebuild the images.
  4. Watch a week of `report --by model` and `budget show`.
  5. Set the real caps and `enforce`.
- **Rollback.** Set `--budget-mode off` and re-init. The data is kept (`prevent_destroy`).

## 14. Testing strategy

- **Gateway.** Tested against an `httptest` upstream with scripted server-sent events:
  - every row of §5.5, plus pings, errors mid-stream, 429 and 529, non-streaming responses, Vertex paths, `count_tokens` and fallback models;
  - byte equality (no rewriting) and no buffering;
  - a property test that `used ≤ granted` at every step.
- **Pricing.** Golden tests per model: cache multipliers, tiers, web search, the maximum-rate fallback for unknown models, and overrides.
- **Rules.** Tested against the **Firebase RTDB emulator** in CI, pinned `firebase-tools` behind a `firebase` build tag, and driven from Go over REST with unsigned emulator ID tokens that carry `fs`, `fr` and `fx` (A13):
  - every row of the denial table (§6.4) as a named test;
  - the allowed paths: reserve, report, release, notional, heartbeat, registry;
  - the observe-mode rules;
  - `TODAY` at midnight, using the emulator's clock where it has one, else checked through the generated expression;
  - a concurrency test: 50 goroutines leasing against 3 caps never exceed any of them, and stale writes are denied and retried.

  The generated rules also have a golden file.
- **Client and runner logic.** Tested against an **in-process RTDB fake** in `internal/gcpfake`. It implements the REST subset, ETags, multi-path atomicity and server-sent events, but not rules: rules are the emulator's job. The runner cases:
  - a halt mid-implement leaves a draft PR, `halted` status and the report;
  - a halt at bootstrap gives `none` and exit 0;
  - a kill during review is seen through the stream;
  - a kill during finalize is ignored;
  - an unpinned model makes the stage fail;
  - an `oauth` token cap is enforced;
  - an expired custom token halts the run;
  - the token object is deleted after it's read;
  - the agent's environment has no real key (secret scan);
  - managed settings are in place before the first stage.
- **Minting.** `signJwt` goes through `gcpfake/iam`. The tests check the claims, and that `--retry` mints a fresh token.
- **Firestore.** Behind a `SpendStore` interface, with an in-memory fake. The emulator runs under the `firebase` tag.
- **CLI.** `budget` against the fake. `watch` golden frames: grouping, burn rate, silent and lost runs, kill confirmation, hostile titles, degraded mode. `report`. `ls` and `diagnose` with `halted` runs.
- **Terraform and init.** `validate` and `tflint` with google-beta, tfvars golden files, and discovery and adoption against `gcpfake`.
- **Live checks** (additions to `gcp-live-checklist.md`):
  - a week of observe mode;
  - a $0.50 per-run cap halting a real run;
  - a global kill during a run;
  - a token expiry test;
  - a rollover document written;
  - watch against the real RTDB;
  - a rules smoke test, in which a real token is denied a write to another run's ledger.

## 15. First-cut task breakdown

Sizes: **S** is up to a day, **M** a few days, **L** about a week.

**M9a: gateway, pinned models, per-run cap, halted (no Firebase)**

1. `internal/pricing`: the table, aliases, tiers and overrides. **M**
2. `internal/gateway`: the proxy, the usage tee, the worst-case estimate, a static local lease from `FUGARO_MAX_RUN_USD`, the allow-list and the Vertex paths. **L**
3. The agent environment for each auth mode with the gateway, and managed settings (A6). **M**
4. `fugaro.yaml`: `agent.models`, `max_output_tokens`, `max_run_tokens`, pinning validation and the pin variables. **M**
5. The runner: `ErrHalted`, the `halted` status and `halt` block, the report, the exit code, `runview`, the schema, and the `oauth` token cap. **M**
6. The `cost` fields `model_source` and `model_by`, and the cross-check. **S**
7. The local config's `budget.mode` and `per_run_usd`, and `model_prices`, through the tfvars into the jobs' environment. **S**
8. Tests and docs (v1 §4.5, §4.6, §5, §10). **M**

**M9b: Firebase counters, daily caps, kill switches, `fugaro budget`**

9. `internal/rtdb`: the REST client (ETags, multi-path `PATCH`, server-sent events, both kinds of authentication) and the `gcpfake` RTDB. **M**
10. Minting (`signJwt` through the signer), the token object, the exchange and refresh at bootstrap, and redaction. **M**
11. The rules generator and its templates, and the emulator test suite with the denial table. **L**
12. Lease top-ups with rules as compare-and-set, the local classification of refusals, usage reports, releases, the kill-switch stream, heartbeats and the registry. **L**
13. `fugaro budget show/set/kill/resume/prices`, and the launch pre-check. **M**
14. Terraform for the Firebase root and state (§6.6). The `init --firebase` flow: discovery and adoption of the FP, the owner and editor discovery, the three applies, the rules deployment, the mark, and the local config. **L**
15. The history job's image (D11), its sweep mode, and its Scheduler job. **M**
16. Live checks and docs. **M**

**M9c: `fugaro watch`**

17. The TUI, its streams, burn rate, stuck detection, kill keys, sanitizing, degraded mode and golden frames. **L**

**M9d: history and reports**

18. The rollover mode (Firestore writes, lookback, pruning, compute from `result.json`) and its Scheduler job. **M**
19. `fugaro report`. **M**

**M9e: task loop (optional)**

20. The verify gate before review and `agent.verify_retries`. **M**
21. Structured findings per round in the final report and `result.json`. **S**

---

## 16. Decisions (settled 2026-09-30)

| # | Decision | Chosen | Consequences |
|---|---|---|---|
| D1 | Who writes budget state | **Per-run Firebase custom tokens, minted by the launcher, bounded by database rules.** No budget service; only a scheduled, admin-privileged history and sweeper job | Rules carry the security (§6.4) and need the emulator in CI. Launchers need `signJwt` on the signer. A compromised run can fill up to its per-run cap (denial of service). A budget service stays as later hardening |
| D2 | How hard the caps are | **A guardrail now. An external gateway later** | A compromised agent can spend around the gateway. The backstops are provider-side (§11) |
| D3 | Where Firebase lives | **A dedicated FP per installation (V1).** The user creates the project and links billing; `fugaro init --firebase` adopts it. V2 and V3 were rejected | "Global" is the installation. Cross-project grants onto the FP, a third Terraform state, and separate billing. Organization-wide aggregation comes later and read-only |
| D4 | Regions | **RTDB `us-central1`. Firestore `us-east5`** (listed as supported; `nam5` elsewhere) | RTDB can't be moved later. About 25 ms from `us-east5` jobs, which is negligible next to model latency |
| D5 | The budget day | **UTC** | Epoch-day keys. No days of 23 or 25 hours |
| D6 | Budget admins | **The installation's owners and editors, plus an optional `budget_admins` list.** Terraform grants them `firebasedatabase.admin` on the FP | Launchers, operators and job accounts can't change caps or switches. The owner list is discovered at `init`, so re-run `init --firebase` after changing owners |
| D7 | What caps count | **Model dollars only** | Compute is reported, never capped |
| D8 | Unpinned models | **Rejected. The stage fails** | No rewriting of request bodies. A misconfiguration shows up as `failed`, not `halted` |
| D9 | A halt before the branch exists | **`halted`, no PR, exit 0** | Keeps policy stops out of the infrastructure-error signals |
| D10 | Coder models | **Claude models only.** Non-Claude coders are deferred | A small Claude model can serve as a cheap coder |
| D11 | Packaging | **The gateway is in the `fugaro` binary in the base image. The history job has its own small image** | The gateway reaches a repository through a derived-image rebuild. The history image is built by `init` until M7 publishes images |
| D12 | The registry | **Heartbeats, plus a sweeper cross-checked against Cloud Run executions** | The history job sweeps every 15 minutes |
| D13 | The TUI library | **Bubble Tea** | The first TUI dependency |
| D14 | Budget backend unreachable | **Halt after a 3-minute grace, for every auth mode and every budget mode, `observe` included.** Image checks and rebuilds are unaffected | An outage of RTDB or Identity Toolkit stops even `oauth` runs and observe runs. `off` is the only mode that ignores the backend |
| D15 | The PR flow | **PRs stay opened at finalize. The structured review format goes into the final report** | No draft PR at start, no per-round comments |
| A1 | `oauth` and Anthropic's terms | **The user accepts the risk.** `oauth` stays as it is and is never proxied. Dollar caps apply to API-key and Vertex only | §5.8 lists exactly what `oauth` runs get |

## 17. Assumptions to verify before building

| # | Assumption | Blocks | If false |
|---|---|---|---|
| A2 | RTDB REST accepts a user's ADC token (cloud-platform scope), or needs the `firebase.database` and `userinfo.email` scopes | M9b (`budget`), M9c | Document a login with those scopes, or use `gcloud auth print-access-token --scopes` |
| A3 | Requests that fail before `message_start` are not billed, and interrupted streams bill the tokens already generated | M9a (reconcile table) | Charge input tokens on failures |
| A4 | A REST multi-path `PATCH` at the root is atomic, and its rules are checked against the fully merged `newData` (reachable through `parent()`) | **M9b** (the whole lease design) | Rolling per-day nodes with a single-location ETag `PUT`, and repository-partitioned counters. The global cap would then need a budget service |
| A5 | `google_firebase_project` and `google_firebase_database_instance` are google-beta only | M9b (Terraform) | Create the instance through its REST API in `init` |
| A6 | Claude Code's managed settings `env` overrides the project's `.claude/settings.json` `env` | **M9a hardening** (the Vertex bypass) | Refuse repositories whose settings set `ANTHROPIC_*` or `CLAUDE_CODE_*` |
| A7 | Anthropic Console offers a spend limit per workspace | M9b docs (the backstop) | Document alerts only |
| A8 | Cloud Run jobs support sidecars with a secret mounted into one container only | Later hardening | Drop the sidecar option |
| A9 | `max_tokens` never exceeds `CLAUDE_CODE_MAX_OUTPUT_TOKENS`, and usage fields are as documented through Vertex `streamRawPredict` | M9a | Reserve the model's maximum output |
| A10 | Every billed call Claude Code makes goes through the base URL. The documented direct calls (fast-mode availability, the WebFetch domain check) are free | M9a | Budget the difference as per-stage overhead |
| A11 | The embedded prices are current (per-model cache-read multipliers, long-context tiers) | M9a | Update them. Overrides cover the gap |
| A12 | Custom tokens are valid for at most 1 h, their custom claims appear in `auth.token`, any account in the FP can sign them, and ID tokens refresh until the user is deleted | **M9b** | Only the design of `fx` and delivery changes |
| A13 | The RTDB emulator enforces rules like production, and accepts unsigned tokens with custom claims over REST | M9b (the rules tests) | Run the rules tests against a dedicated test FP instead |
| A14 | `'' + <integer arithmetic>` in the rules yields the plain decimal day number, so `TODAY` works | **M9b** | Use a rolling current-day node whose rollover must archive the old values (enforced by rules) |
| A15 | Identity Toolkit sign-in with custom tokens needs no sign-in provider enabled, and costs nothing at this volume | M9b | Enable the provider, and check whether Identity Platform charges per monthly active user |

## 18. Suggested milestone split

- **M9a: gateway, pinned models, per-run cap, `halted` (no Firebase).** Exact accounting per call, pinned models per stage, no real key in the agent's environment, a per-run cap enforced in-process, the `oauth` token cap, and the new status. It is useful on its own.
- **M9b: Firebase counters, daily caps, kill switches, `fugaro budget`.** The dedicated FP per installation (`init --firebase`), per-run tokens and rules, leases, observe mode, the registry and the sweeper.
- **Later, read-only: organization-wide roll-up.** Each installation's history job exports `spendDaily` to one shared place. There is no shared live state.
- **M9c: `fugaro watch`.** Only needs M9b's data.
- **M9d: Firestore history and `fugaro report`.** Independent of M9c.
- **M9e (optional): the verify gate and structured findings.**
- **Later:** the external gateway or budget service (D2 hardening), non-Claude coders, drafts at the start and per-round comments, the redundant mode, per-batch concurrency, and letting launchers use the kill switch.
