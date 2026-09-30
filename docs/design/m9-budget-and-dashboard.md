# Fugaro M9 — Budget guardrails, live dashboard and spend history

*Status: design proposal for review · 2026-09-30 · Not an implementation plan. Source spec: [m9-spec-source.md](m9-spec-source.md). Base design: [v1.md](v1.md) (sections are cited as §n).*

This proposal maps the source spec onto Fugaro as it exists after M6, picks an approach for each open question, and lists the decisions the user still has to make. Four decisions are already made and binding. This document doesn't reopen them, but it does state what follows from them:

1. **Firebase holds the state.** Live state (budget counters, the agent registry, kill switches) goes in the **Realtime Database (RTDB)**, and history (daily spend records) in **Firestore**, both in a Firebase project the user creates.
2. **Spend is enforced per model call** by a **model gateway in the container**, which reserves before each call, makes the call, then reconciles the reservation with the real cost. The gateway covers **API-key and Vertex** auth only. Dollar caps are enforced only where billing is real. `oauth` runs keep the per-stage `--max-budget-usd`, advisory dollars and time caps.
3. **The `redundant` parallelism mode** (N attempts at one task) is out of scope.
4. **M6 is done.** v1.md is the base design.

---

## 0. Summary of recommendations

- **One Fugaro repository is one budget "project".** Caps nest as repository ⊂ global. Each has a per-day cap and a per-run cap. There is a global kill switch and one per repository. The day is UTC.
- **The gateway lives in the runner process** (`fugaro exec`), on `127.0.0.1`. Claude Code reaches it through `ANTHROPIC_BASE_URL` (API key) or `ANTHROPIC_VERTEX_BASE_URL` with `CLAUDE_CODE_SKIP_VERTEX_AUTH=1` (Vertex). The gateway holds the real credential, and the agent's environment no longer does.
- **Reservation happens at two levels.** Every call reserves its worst-case cost against a **local lease**, exactly and in process. The leases are what get reserved against the shared counters. This cuts RTDB traffic about 20-fold, and a call never waits on Firebase unless its lease runs dry. The caps still hold, because what is counted against a cap is the leased amount, which is never less than what could be spent.
- **A small budget service** (a Cloud Run service, `fugaro-budget`) **makes every write to RTDB and Firestore.** Jobs have no Firebase IAM at all. They call the service with their Google-signed ID token, and the service maps each caller's service account to its repository. This is the only option that stops a compromised job from lowering another repository's counters or the global one (§6.4). It reverses v1's "no long-running control-plane service" non-goal. The service scales to zero and costs about nothing.
- **Counters are dated nodes** (`/spend/<YYYY-MM-DD>`), not one `daily` node that rolls over. Nothing needs resetting, and the end-of-day archive has no race at midnight.
- **`halted` becomes a new run status.** The stage stops, finalize runs as it does for any failure, the PR is a draft, and the report says what halted the run and how to resume. Halted is additive to the `result.json` schema.
- **The kill switch reaches jobs by polling** the service's heartbeat, with at most 15 s of latency. Jobs never hold an RTDB listener.
- **`fugaro watch`** is a Bubble Tea TUI fed by RTDB's REST event stream, read with the viewer's own IAM. Without Firebase it falls back to `ls` data.
- **History** is one Firestore document per repository per day, written idempotently by the budget service when Cloud Scheduler calls it. `fugaro report` computes weeks, months and years on read.
- **M9 is opt-in.** With no `budget:` block in the local config, everything behaves as it does today. An `observe` mode accounts for spend but never refuses a call, so the caps can be calibrated before they are enforced.
- **Split the milestone.** M9a ships the gateway, per-stage pinned models and an in-process per-run cap, with no Firebase. M9b adds the budget service, RTDB counters, kill switches and `fugaro budget`. M9c adds `fugaro watch`. M9d adds Firestore history and `fugaro report`. The spec's task-loop changes are a separate, optional M9e.

**The biggest caveat.** The container isn't a trust boundary against its own agent (§6.1). A prompt-injected agent can read the real credential from the runner's `/proc`, or, on Vertex, take the service account's token from the metadata server, and call the model directly, around the gateway. So the in-container gateway bounds a **runaway or honest** agent absolutely, but a **compromised** agent only as far as provider-side limits reach. §11 gives the backstops and the path to making the caps hard.

---

## 1. Goals and non-goals

**Goals**

- A hard dollar cap on model spend per run and per day, for each repository and for the whole installation. It is checked before each call, never discovered afterwards, and holds across any number of concurrent runs.
- A global kill switch and one per repository. Each takes effect on every run of its scope within about 15 seconds.
- **Fail closed.** When the budget state can't be reached, no new model spend is authorized beyond what is already leased.
- **Pinned models.** Every stage names an explicit model ID, and the gateway refuses any model the run didn't pin or that has no price.
- A live, terminal view of spend, caps, burn rate and running agents, grouped by repository.
- A durable daily history (spend, runs, outcomes, split by model), with weekly, monthly and yearly figures derived on read.
- One consistent account of cost across `result.json`, `ls`, the PR report, the live counters and the history.

**Non-goals (M9)**

- The `redundant` mode, and per-batch concurrency limits (`max_parallel` stays the only concurrency control).
- Non-Anthropic coder models (§2.3). Anthropic's gateway guide says Claude Code isn't supported on non-Claude models behind any gateway.
- Draft PRs from the start of a run, and posting review comments on the PR each round (§2.4). Both are deferred.
- A web dashboard (Firebase Hosting stays unused).
- Dollar caps for `oauth` runs, and routing subscription tokens through the gateway (§5.8).
- Capping compute spend. Compute stays estimated and reported, but not capped (Decision D7).
- Making caps hold against a compromised agent in the container. That needs the credential moved out of the container (§11).

---

## 2. How the spec maps onto Fugaro

| Spec concept | Fugaro today | M9 proposal |
|---|---|---|
| `project` | Nothing. A repository (`owner/name`, slug §3.2) is the unit of jobs, IAM and state | **A project is a repository**, keyed by its slug. Grouping repositories under one cap is a later addition (a `group` field on the cap) |
| `taskId` | Run ID `YYYYMMDD-HHMMSS-<hex>` | The run ID. The registry key is `<slug>/<run-id>` |
| `coder` / `reviewer` models | One `agent.model` for every stage | Per-stage models: `agent.models.coder` for implement and fix, `agent.models.reviewer` for review (§2.1) |
| `provider` | Implied by `agent.auth` (`vertex`, `api-key`, `oauth`) | Unchanged in M9: the auth mode *is* the provider. Non-Anthropic providers are deferred (§2.3) |
| `maxTokensPerCall` | None. Claude Code picks `max_tokens` | `agent.max_output_tokens` for each role, passed as `CLAUDE_CODE_MAX_OUTPUT_TOKENS`. The gateway refuses a request above it |
| Pinned IDs and a price table | Claude Code's own `total_cost_usd` | Price table embedded in the binary, with owner overrides. An unpriced or unpinned model is refused (§5.2) |
| `maxReviewRounds` | `agent.review_rounds` (default 2) | Unchanged. The spec's 3 is a per-repository setting |
| COMPILING → TESTING → LINTING, deterministic | The agent runs `fugaro verify`, and §4.2 gates readiness on the record | Keep. Optionally add a verify gate before review (§2.4, M9e) |
| APPROVE / REJECT | `ship` / `changes` (§4.1) | Keep Fugaro's names. The spec's names are only labels |
| Draft PR from the start | PR opened at finalize | Deferred (§2.4) |
| Structured review comment each round | Findings go to the fix prompt, counts to `result.json`, and the final report | Adopt the machine-readable format **in the final report**, one section per round (§2.4) |
| HALTED | None | New status `halted` (§5.9) |
| RTDB `budget` / `runs` / `agents` | GCS only (§3.3) | Dated counters, caps, kill switches and the registry, all written by the budget service (§6.2) |
| `onDisconnect` | n/a | Not available from Go (§6.5). Heartbeats plus a sweeper instead |
| Firestore `spendDaily` | None | As in the spec, keyed by slug, with the doc ID `<date>_<slug>` (§9) |
| Wide / narrow modes | `max_parallel` soft limit per CLI | Out of scope |

### 2.1 Per-stage models: the minimal config change

The repository file (`fugaro.yaml`, controlled by the repository's writers) says *which* models each stage uses. It can never say *how much* may be spent. Caps and prices belong to the owner (§4, §5.2), because a repository that could set its own price or cap could lift its own limit.

```yaml
agent:
  auth: api-key                  # vertex | api-key | oauth (unchanged)
  model: claude-sonnet-5-5       # existing: the default for every role
  models:                        # new, optional; each falls back to agent.model
    coder: claude-sonnet-5-5     # implement and fix (the resumed session)
    reviewer: claude-opus-5-5    # review (a fresh session each round)
    background: <pinned-small-model>  # Claude Code's background requests
  max_output_tokens:             # new, optional; per call, per role
    coder: 16000
    reviewer: 8000
  review_rounds: 3
  max_budget_usd: 25             # unchanged: per stage, through claude --max-budget-usd
```

- **Pinning is enforced only when the budget is on.** Then `fugaro validate` (and the runner at bootstrap) refuse a role without an explicit model ID. They also refuse aliases (`sonnet`, `opus`), since Claude Code resolves those to whatever is current, and any ID missing from the effective price table. With the budget off, the current behaviour stays: an empty `agent.model` means Claude Code's default.
- **The runner pins every model Claude Code might pick, not just the main one.** For each stage it passes `--model <role model>` (as today), and sets `ANTHROPIC_DEFAULT_OPUS_MODEL`, `ANTHROPIC_DEFAULT_SONNET_MODEL` and `CLAUDE_CODE_SUBAGENT_MODEL` to the role's model, and `ANTHROPIC_DEFAULT_HAIKU_MODEL` to `background`. So an alias in a repository's `.claude/agents/*.md` (`model: opus`) resolves to a pinned ID. The gateway's allow-list is the backstop (§5.2). All these variables are already reserved (the `ANTHROPIC_` and `CLAUDE_CODE_` prefixes, §5.1), so a workflow secret can't override them.
- **`task.json` overrides** (§5.3) keep `model`, meaning the coder model, and can't add a model that the effective price table lacks.
- **Vertex IDs** can differ from Anthropic's (for example a version suffix). Each price table entry lists its aliases per provider, and the gateway prices by the model the response reports (§5.5).

### 2.2 Why a gateway and not Claude Code's own cost

Claude Code reports `total_cost_usd` only in the result event at the **end of a stage**, and `--max-budget-usd` is checked by Claude Code itself per process. Neither can refuse a call because of spend happening elsewhere in the fleet. The gateway sees every request and its streamed usage, so it can reserve before a call and account exactly afterwards. Claude Code's figure becomes a cross-check: the runner logs a warning when the two differ by more than 5% for a stage.

### 2.3 Non-Anthropic coder models

Several vendors offer Anthropic-compatible Messages endpoints, and a gateway could route a model name to one. Anthropic's gateway documentation, however, says it "doesn't support routing Claude Code to non-Claude models through any gateway". That rules out reliability:

- Claude Code sends the full Claude API capability set to an `ANTHROPIC_BASE_URL` gateway: adaptive thinking, `context_management`, `output_config`, beta tool fields, `cache_control`. A compatible endpoint that rejects any of these gives hard 400s. Setting `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1` removes some of them, but not adaptive thinking.
- Cost accounting would rest entirely on the gateway's price table and on the vendor's `usage` fields, whose caching semantics differ.
- It needs a provider registry in the owner's config (base URL, credential secret, price table), a reserved secret per provider, and per-model routing in the gateway.

**Recommendation:** leave it out of M9 and run the spec's A/B test first. If a cheap coder is wanted sooner, a Claude small model as `coder` gives most of the "fan out cheap" saving with no compatibility risk (Decision D10).

### 2.4 Lifecycle conflicts with the spec, and what to adopt

| # | Spec rule | Conflict with Fugaro | Recommendation |
|---|---|---|---|
| 1 | Open the PR as a draft at PROVISIONING | Finalize opens it (§4.1). Opening it early needs an empty commit pushed at bootstrap. `pr` would then be set early on a first run (today only a follow-up does that). A crash would leave an empty draft. Labels and reviewers would be notified at the start. And every finalize and follow-up path (§4.4) was built and live-checked around a PR that doesn't exist until finalize | **Defer.** `fugaro watch` shows the run live, which covers most of what an early PR would give |
| 2 | One structured findings comment on the PR each round | Fugaro posts once, at finalize. Its marker rules (§4.4) would need extending, and each comment is a provider call during stages | **Adopt the format, not the timing.** The final report gets one section per round, in the spec's layout with `<!-- fugaro:findings {...} -->`, and `result.json` gets the findings. Per-round comments can come later, together with draft-from-start |
| 3 | The reviewer never sees code that fails to compile or test | The review runs whatever the agent committed. Readiness is gated on the verify record (§4.2), not the review | **Adopt as M9e (optional).** Before each review, the runner checks for a passing, clean `verify test` on HEAD. If there is none, it runs a fix stage with "no passing verified test run on HEAD", counted against a separate cap, `agent.verify_retries` (default 3). That fix doesn't count as a review round. After the cap: draft, with the reason |
| 4 | The reviewer gets only the task and the diff | The review is a fresh Claude Code session with tools, which can read the repository | **Keep.** Leaner context is a prompt choice (`agent.review`). An optional `review_context: diff` could send the diff inline with tools disabled. That goes in the backlog, not M9 |
| 5 | Give up at `round = max` → FAILED, PR stays draft | Same as §4.2 | Already true |
| 6 | HALTED → draft PR with a "halted" comment | No such status | **Adopt** (§5.9) |

---

## 3. Architecture

```
 local ─ fugaro watch / budget / report ──(user IAM: REST + SSE)──► RTDB  ◄──(admin)── fugaro-budget (Cloud Run service)
                   │                                                        ▲   │        │
                   └──(user IAM)──► Firestore ◄─────────(rollover)──────────┼───┘        │
                                                                            │            │
 Cloud Scheduler ──(OIDC, fugaro-scheduler)──► POST /v1/rollover ───────────┘            │
                                                                                         │ ID token (job SA)
 Cloud Run job ─ fugaro exec (runner) ── gateway 127.0.0.1:P ── heartbeat/lease/usage ───┘
                     │                        ▲      │
                     └─ claude -p ────────────┘      └──► api.anthropic.com  |  Vertex AI (streamRawPredict)
```

- **Gateway.** A `net/http` handler inside `fugaro exec`, started at bootstrap when the job has `FUGARO_BUDGET` set. It holds the lease, the price table and the real credential. It forwards requests byte for byte and streams responses back unbuffered.
- **Budget service.** `fugaro budget serve`, running as the service account `fugaro-budget`. It is the only writer to RTDB and Firestore. It holds an in-memory cache of the day's ledger and writes with ETag-guarded transactions.
- **RTDB.** Holds caps and kill switches (written by the owner's CLI), dated spend counters, leases, and the agent registry.
- **Firestore.** Holds `spendDaily`.
- **CLI.** Reads RTDB (watch, `budget show`) and Firestore (report) with the user's own IAM. Owners write caps and kill switches directly with theirs (§6.4).

---

## 4. The budget model

**Scopes.** `global`, and `repos/<slug>`. A call must pass every scope that applies:

| Cap | Where it is set | What it bounds |
|---|---|---|
| `global.dailyUsd` | `fugaro budget set --global --daily` | All repositories' leased plus spent, per UTC day |
| `global.perRunUsd` | `--global --per-run` | Any one run's leased plus spent, over its lifetime. The default for every repository |
| `repos/<slug>.dailyUsd` | `--repo R --daily` | This repository per day. A repository without one uses `defaults.repoDailyUsd`. With neither, the repository has **no budget, and its runs halt** (fail closed) |
| `repos/<slug>.perRunUsd` | `--repo R --per-run` | Per run. The effective limit is `min(repo, global)` |

- **Kill switches.** `kill/global` and `kill/repos/<slug>`, each `{on, by, at, reason}`. `on` halts every run in scope and refuses new leases and new launches.
- **Fail closed.** No lease, no call. §12 covers what happens when the service is unreachable.
- **Atomic.** The service updates a whole day's ledger in one ETag-guarded transaction (§6.3), so two jobs can't both see "90 spent" and proceed.
- **The day is the UTC date** on which a lease was granted. A call straddling midnight counts towards the day its lease came from (Decision D5).
- **Counted amount** = `spentUsd + leasedUsd` (outstanding leases) for every scope. Leases are what make the cap hold before reconciliation.
- **What counts.** Model dollars on `api-key` and `vertex`. The `oauth` notional figure is recorded as `notionalUsd` and never counts against a dollar cap. Compute isn't capped (Decision D7).

---

## 5. Enforcement: the gateway

### 5.1 Where it sits and what the agent sees

| Auth | Agent environment (M9) | Gateway upstream and credential |
|---|---|---|
| `api-key` | `ANTHROPIC_BASE_URL=http://127.0.0.1:<port>`, `ANTHROPIC_API_KEY=<random per-run gateway token>`. **The real key is no longer in the agent's environment** | `https://api.anthropic.com`, with `x-api-key` from the runner's `ANTHROPIC_API_KEY` |
| `vertex` | `CLAUDE_CODE_USE_VERTEX=1`, `ANTHROPIC_VERTEX_BASE_URL=http://127.0.0.1:<port>/v1`, `CLAUDE_CODE_SKIP_VERTEX_AUTH=1`, `CLOUD_ML_REGION`, `ANTHROPIC_VERTEX_PROJECT_ID` | `https://<region>-aiplatform.googleapis.com` (or the global endpoint), with a bearer token from the metadata server |
| `oauth` | Unchanged (`CLAUDE_CODE_OAUTH_TOKEN`). No gateway | n/a |

- **Endpoints allowed** (per Anthropic's gateway guide): `POST /v1/messages` and `/v1/messages/count_tokens`. On Vertex: `:rawPredict`, `:streamRawPredict` and `count-tokens:rawPredict` under `…/publishers/anthropic/models/{model}`. `HEAD /api/hello` gets a 200 without being forwarded. Anything else gets a 404 and is logged. Token counting is free, and is forwarded without a reservation.
- **Forwarding follows the guide.** The gateway forwards `anthropic-version`, `anthropic-beta` and the body **unchanged**. It never rewrites a body: rewriting breaks header and body pairing and preserved-thinking checks. It streams without buffering, keep-alive pings included. It passes `retry-after`, `x-should-retry` and `anthropic-ratelimit-unified-*` through, and forwards error bodies unmodified.
- **It parses the request body for three fields only:** `model` (from the URL on Vertex), `max_tokens`, and whether any `cache_control` asks for a 1-hour TTL. It parses the SSE stream for `message_start`, `message_delta` and `error` events only.
- **The repository's own `.claude/settings.json`** could set `env.ANTHROPIC_BASE_URL`, or on Vertex point Claude Code straight at Vertex with the metadata server's credentials. That would bypass the gateway. The base image therefore installs a **managed settings file** (`/etc/claude-code/managed-settings.json`, written by the runner at bootstrap, which runs before the agent). It sets the gateway variables at the highest precedence. *Assumption A6: managed-settings `env` takes precedence over project settings.*
- **Headers the gateway uses for attribution:** `x-claude-code-session-id` and `x-claude-code-agent-id`, logged and stored with the per-model usage, so a stage's subagents are visible.

### 5.2 Model pinning and the price table

- **Allow-list per stage.** When a stage starts, the runner tells the gateway its role's models: `{role model, background model}`. A request for any other model gets `400` with `{"type":"error","error":{"type":"invalid_request_error","message":"fugaro: model X is not pinned for stage review"}}`, is logged, and fails the stage as a **configuration error** (`failed`, not `halted`). **Reject, not rewrite** (Decision D8): rewriting `model` changes what the user asked for, and can break thinking and cache continuity.
- **Price table.** It is embedded in the binary (`internal/pricing`), with each entry's source and the date it was checked, like the compute prices (§10.1). Per model it lists `inputPerM`, `outputPerM`, `cacheWrite5mMult`, `cacheWrite1hMult`, `cacheReadMult` (per model: the docs list 0.1×, with exceptions such as 0.05× and 0.025×), an optional long-context tier (`over: <tokens>`, with its own rates), `webSearchPer1k`, and provider aliases.
- **Owner overrides.** `model_prices` in the local config, validated like `compute_prices`, reach the jobs and the service as `FUGARO_MODEL_PRICES` (JSON) through the tfvars, the same way as `FUGARO_COMPUTE_PRICES`. Only people who run `fugaro init` can change them, never a repository.
- **Unknown serving model.** When a response names a model that isn't in the table (for example after a server-side fallback), the call is charged at the table's **highest** rates and flagged `priced_as: max`.

### 5.3 Two-level reservation (the algorithm)

**Level 1: per call, in process.** The gateway holds `lease{id, day, granted, used, reserved, expires}`. `free = granted - used - reserved`.

```
on request(model, body, maxTokens):
  if killed or halted: respond 403 {"fugaro: budget halted: <reason>"}, x-should-retry: false
  w := worstCase(model, len(body), maxTokens, wants1hCache)
  for free < w:                                   # top up; see level 2
      g := service.lease(run, need = max(w - free, leaseSize()))   # may block ≤ unreachable_grace
      if g.refused: halt(g.reason); respond 403 as above
  reserved += w                                   # a mutex; parallel calls reserve independently
  stream upstream → client, tee-parsing usage
  actual := price(usage, servingModel)            # §5.5
  reserved -= w; used += actual
  if actual > w: debt(actual - w)                 # can't happen with the bytes bound; recorded if it does
  queue usage{model, tokens, actual, session, agent} for the next report
```

**Level 2: leases, in the service.** One ETag transaction on `/spend/<day>` checks every scope (§6.3):

```
lease(repo, run, need):
  if kill.global.on or kill.repos[repo].on           → refuse kill_switch
  caps missing for repo (and no default)             → refuse no_cap
  for scope in [global, repos/repo, runs/repo/run]:
      counted := spent + leased (+ the run's spend on the previous day, for a run scope)
      if counted + need > limit(scope)               → refuse <scope>_cap, unless need can shrink (below)
  leased += need in every scope; leases/<id> = {repo, run, amount: need, expires: now + 10m}
```

- **Lease size shrinks near a cap.** `leaseSize = clamp(5% × min headroom across the scopes, $0.25, $2.00)`, and never less than the call's worst case. When `need` exceeds the headroom but a single call's worst case fits, the service grants the headroom. This avoids refusing early merely because leases are chunky.
- **Refusals are real.** A request is refused only when even one worst-case call doesn't fit after counting outstanding leases. Before halting, the gateway retries once after 20 s, since other runs' leases may be returned (a repository with many parallel runs near its cap).
- **Reporting.** Every 15 s (the heartbeat), and at stage end and run end, the gateway reports `usage[]` against its lease. The service moves `actual` from `leased` to `spent` in every scope, and adds `byModel`, tokens and `calls`.
- **Returning.** At run end, and when a lease expires locally, the unused part goes back. A lease the service sees expire without a report (a crashed job) is **charged in full** to `spent` with `unreconciled: true`. A report arriving later (within 24 h, for the lease's day) refunds the difference. So a crash can only ever over-count, never under-count.
- **Traffic.** Say 20 concurrent runs, each making a call every 5 s at an average of $0.05. That is 4 calls/s, or $0.20/s. With $2 leases that is about 0.1 lease ops/s, plus 1.3 heartbeats/s, instead of the spec's roughly 24 RTDB transactions/s (4 calls × 6 transactions each).

### 5.4 The worst-case estimate

`worstCase = inTokUB × inputRate × cacheMult + maxTokens × outputRate + (web search allowed ? searchCap × perSearch : 0)`

- **`inTokUB` = the request body length in bytes.** For text, a token is at least one byte, and JSON framing and base64 images make bytes over-count further, so this is a safe upper bound with no tokenizer. It over-reserves by about 3–4×, but the lease absorbs that and reconciliation refunds it.
- **`cacheMult`** is `cacheWrite1hMult` when the body asks for a 1-hour TTL, else `cacheWrite5mMult` when there is any `cache_control`, else 1. Assuming every input token is a cache write is the worst case.
- **The long-context tier** applies when `inTokUB` exceeds the tier threshold. That is conservative: it may price a request at the higher rate when the real count is under the threshold.
- **`maxTokens`** comes from the body, and is refused above the role's `max_output_tokens` when that is set. Thinking tokens are billed as output, and are inside `max_tokens`.
- **A tighter estimator can come later.** Per conversation: the previous call's actual input total plus the new bytes, falling back to the byte count after a compaction. Build it only if refusals near a cap prove it necessary. It isn't in M9.

### 5.5 Reconciling the streamed Messages API

- **Usage.** `message_start.message.usage` carries `input_tokens`, `cache_creation_input_tokens` (split into `cache_creation.ephemeral_5m_input_tokens` and `ephemeral_1h_input_tokens`), `cache_read_input_tokens` and `output_tokens`. Each `message_delta.usage` is **cumulative**, and may repeat or update the input fields and add `server_tool_use.web_search_requests`. The final figure for each field is the one from the last event carrying it, else from `message_start`.
- **Pricing.** `input × in + 5m × in × m5 + 1h × in × m1h + read × in × mr + output × out + searches × perSearch`, at the tier for the input total and the **serving** model named in `message_start` (or `message_delta`).
- **Non-streaming responses** carry the same `usage` in the body.

| Case | Charge |
|---|---|
| Stream completed (`message_delta` with a `stop_reason`) | The actual usage |
| Upstream error before `message_start` (4xx, 429, 529, connection refused) | 0. *Assumption A3: failed requests aren't billed* |
| Error event or disconnect after `message_start` | Input from `message_start`, plus the **reserved** output (`maxTokens`). Output generated before a break is billed, and the count may never arrive |
| Client (Claude Code) cancelled mid-stream, or a stage kill | As above. The gateway cancels the upstream request at once |
| Gateway crashed (the whole runner died) | The lease is charged in full (§5.3) |
| Claude Code retries (429/529, a dropped stream) | Each attempt is its own request, with its own reservation. The gateway never retries on its own |
| Parallel calls (subagents) | Each reserves independently from the lease, under a mutex. Top-ups are serialized so there is only one outstanding lease request |
| `count_tokens` | Not reserved, not charged |

### 5.6 Rules that tie the stages to the gateway

- **Per-stage `--max-budget-usd`** stays as defense in depth. It is Claude Code's own per-process check.
- **The run's gateway cap** (`perRunUsd`) is checked by the service against the run's leases. Without the service (M9a), it is checked in process against `FUGARO_MAX_RUN_USD`.
- **Verify notifies the gateway.** `fugaro verify` tells the gateway (`POST /fugaro/verify {kind, phase}` on the loopback address, authenticated with the per-run token) when it starts and ends, so the registry can show `implement · test` in place of the spec's COMPILING and TESTING. This is cosmetic: failures are ignored.

### 5.7 Budget modes

The local config's `budget.enforce` has three settings:

- **`off`**: no gateway. The same as having no `budget:` block.
- **`observe`**: the gateway runs, accounts, leases (never refused) and reports, but only logs what would have halted. **Use it to calibrate the caps.**
- **`enforce`**: everything in this section.

### 5.8 `oauth` runs

No gateway (binding decision 2). Claude Code's documentation says that with `ANTHROPIC_BASE_URL` set and no gateway credential, the claude.ai login stays active and "gateways that pass this traffic on to Anthropic must forward the OAuth capability". So it is technically possible. The terms are the problem. Claude Code's legal page says OAuth "is designed to support ordinary use of Claude Code", and that "developers may not collect, store, or intermediate Claude.ai credentials or session tokens". A gateway that holds and forwards the token is plausibly "intermediation". **Before building anything here, check** (Assumption A1): whether the owner running their own subscription token through their own loopback gateway, for their own runs, is permitted; whether the unattended cloud use of `claude setup-token` that M4 already relies on is itself within the terms; and whether "ordinary, individual usage" limits apply to parallel fleet runs.

For `oauth` runs, M9 gives:

- **Kill switches.** Through the heartbeat, the same as other runs.
- **Registry entries.** Also through the heartbeat.
- **Time caps.** As today.
- **Per-stage `--max-budget-usd`.** As today, notional.
- **Advisory dollars.** At the end of each stage the runner reports Claude Code's notional `total_cost_usd` as `notionalUsd` in the counters, and watch and report show it separately.
- **An optional token cap.** `agent.max_run_tokens`, checked at stage boundaries from the result event's usage.

### 5.9 Halted: the runner, `result.json` and §4.2

- **Mechanism.** A refusal or kill cancels the stage's context with the cause `ErrHalted{reason}`, the same pattern as `ErrCancelled` (`internal/runner/budget.go`). The process group is killed. `StageError` returns `halted during <stage>: <reason>`, and the loop ends. **Finalize runs normally**: it makes no model calls, so a kill during finalize or writeback is ignored, and the run finishes its PR.
- **Status `halted`,** with the outcome:
  - `draft` when the branch exists: the push and draft PR as for `failed`.
  - `none` when the run stopped at bootstrap: `run/start` refused before the lock or the branch, so no branch and no PR. Also for a follow-up that stops before its push.
- **Precedence.** The first cause wins: cancelled or halted, whichever cancelled the stage.
- **Readiness (§4.2).** A halted run is never ready. It can halt only by losing a stage, so no final passing review can follow. A run whose review already said `ship`, killed during finalize, stays ready: nothing more is spent.
- **Exit code 0.** A deliberate outcome was recorded, including a halt at bootstrap. That differs from `infra_error` (exit 2), so Cloud Run doesn't show a policy stop as an infrastructure failure (Decision D9).
- **The report.** A heading, `## Fugaro — Halted: <reason>`: "repository daily cap $60.00 reached at 14:32 UTC; this run spent $4.12. PR left as draft. After `fugaro budget resume`/`set`, continue with `fugaro run --pr N`." It carries the usual marker. A halted run pushed its work, so its PR can be followed up (§4.4). Nothing in `runview.Pushed` changes.
- **Schema** (`schemas/result.schema.json`, still `version: 1`, and every change is additive):
  - `status` gains `halted`.
  - A new `halt: {reason: "kill_switch"|"run_cap"|"repo_daily_cap"|"global_daily_cap"|"no_cap"|"budget_unavailable", scope: "global"|"repo"|"run", at, detail}`.
  - `cost` gains `model_source: "gateway"|"claude-code"`, `model_by: {<model>: usd}` and `unreconciled: bool`.
- **Old CLIs** show `halted` as a terminal status: `runview` passes any non-running record status through, and marks it terminal. Nothing validates records against the schema when it reads them. `fugaro:status` and `fugaro:diagnose` learn the word.
- **Launch pre-check.** `fugaro run` reads the caps and kill switches (with viewer IAM) and refuses (exit 1) when its repository is killed, has no cap, or has less than `$0.25` of daily headroom. If RTDB can't be read, the launch is refused with the reason (fail closed). `--no-budget-check` skips this client-side check only: the runner's check remains.

### 5.10 Kill switch delivery

Jobs have no RTDB access (§6.4), so they can't hold the spec's live listener. Every heartbeat (15 s), lease and usage response carries `{kill, reason}`. When it is set, the runner halts the stage at once, and the gateway refuses new calls and cancels in-flight streams. The worst-case latency is about 15 s plus the time to kill a process group (10 s SIGTERM grace). The alternative, a server-sent-event stream from the service to each job, keeps a Cloud Run request open for the whole run (instance time about equal to the jobs' own) to gain about 10 s. It isn't worth it. The heartbeat interval is configurable down to 5 s (`budget.heartbeat`).

---

## 6. Firebase

### 6.1 Project, APIs, region and IAM

- **Project.** The recommendation is **to add Firebase to the installation's GCP project** (Decision D3): IAM, service accounts, Terraform state, log isolation and `fugaro init` all stay in one place. A separate Firebase project the user created is supported through `budget.firebase_project`, at the cost of cross-project grants: the budget service account needs roles there, and so do launchers.
- **APIs.** `firebase.googleapis.com`, `firebasedatabase.googleapis.com`, `firestore.googleapis.com`, and `firebaserules.googleapis.com` (Firestore rules). Run and Scheduler are already enabled.
- **Region.** RTDB is offered **only** in `us-central1`, `europe-west1` and `asia-southeast1`, and its location can't be changed later. Choose the one nearest the jobs: `us-central1` for a US-east installation, about 20–30 ms away, which is negligible next to model latency. Firestore can be regional in the jobs' own region (`us-east5` is listed) or the `nam5` multi-region. The recommendation is **the installation region**, falling back to `nam5` where Firestore doesn't offer it (Decision D4).
- **IAM.** Every grant is an `*_iam_member`, as in §8.1:

| Principal | Grants |
|---|---|
| `fugaro-budget` (new service account, display name `Fugaro budget`) | `roles/firebasedatabase.admin`, `roles/datastore.user`, `roles/iam.serviceAccountViewer` (to read job accounts' display names, §6.4) |
| Each job account | `roles/run.invoker` on `fugaro-budget` only (repository module). **No Firebase or Datastore role** |
| `fugaro-scheduler` | `roles/run.invoker` on `fugaro-budget` |
| Launchers and operators | `roles/firebasedatabase.viewer`, `roles/datastore.viewer` |
| Budget admins (project owners and editors implicitly; plus the optional `terraform.budget_admins`) | `roles/firebasedatabase.admin`, which is what writes caps and kill switches |

### 6.2 Data model

**RTDB** (keys escaped: RTDB forbids `.`, `$`, `#`, `[`, `]` and `/`. Slugs are safe as they are, and model IDs are escaped with `%2E` and the like):

```
/config                                  # written only by budget admins (the CLI); read by the service and viewers
  caps/global          {dailyUsd, perRunUsd}
  caps/defaults        {repoDailyUsd, repoPerRunUsd}
  caps/repos/<slug>    {dailyUsd, perRunUsd, repo: "owner/name"}
  kill/global          {on, by, at, reason}
  kill/repos/<slug>    {on, by, at, reason}
/spend/<YYYY-MM-DD>                      # one ETag-guarded ledger per UTC day; written only by the service
  global               {spentUsd, leasedUsd, notionalUsd, calls}
  repos/<slug>         {spentUsd, leasedUsd, notionalUsd, calls, runs,
                        outcomes/{succeeded, failed, halted, cancelled, infra_error},
                        byModel/<model>/{usd, in, out, cacheRead, cacheWrite}}
  runs/<slug>/<run>    {spentUsd, leasedUsd, notionalUsd}
  leases/<id>          {slug, run, amountUsd, expiresAt}
  archived             <timestamp, set by the rollover>
/agents/<slug>/<run>                     # the registry; written only by the service
  {repo, workflow, title, stage, round, verify, roles/{coder, reviewer}, auth,
   prUrl, startedAt, stageStartedAt, stageDeadline, updatedAt, spentUsd, halted}
```

- **`title`** is the first line of the task, clipped to 80 characters and redacted by the runner. Launchers can already read every `task.json`, so this reveals nothing new.
- **Per-run cap across midnight.** It is the run's entry today plus its entry yesterday. Runs are at most 24 h long (`OverrideCap`), so two days always suffice.
- **Retention.** Day nodes older than 8 days are deleted by the rollover, once archived.

**Firestore** (the `(default)` Native-mode database):

```
spendDaily/<YYYY-MM-DD>_<slug>
  {repo, slug, date, spentUsd, notionalUsd, computeUsd, unreconciledUsd, calls, runs,
   outcomes{succeeded, failed, halted, cancelled, infra_error},
   byModel{<model>: {usd, in, out, cacheRead, cacheWrite}}, capDailyUsd, archivedAt, version: 1}
```

The global figure is the sum over repositories, computed on read. There is no composite index: `fugaro report` queries a range of document IDs (`>= "<from>_"`, `< "<to+1>_"`), which is at most repositories × days (about 7,300 a year for 20 repositories), and filters by repository on the client.

### 6.3 Transactions over REST

The Firebase Admin Go SDK's RTDB client is REST only (no `onDisconnect`, no persistent connection). The service doesn't need the SDK at all: a small REST client (`internal/rtdb`, roughly 300 lines) does the following:

- **Reads.** `GET <db>/<path>.json` with `X-Firebase-ETag: true`, which returns the ETag.
- **Conditional writes.** `PUT` with `if-match: <etag>`. A 412 means re-read and retry, at most 8 times, with jitter.
- **Multi-path updates.** `PATCH` for writes that don't need a transaction (the registry).
- **Streaming.** `GET` with `Accept: text/event-stream`, carrying `put`, `patch`, `keep-alive`, `cancel` and `auth_revoked`. Watch uses it.
- **Authentication.** The service's OAuth access token (scopes `firebase.database` and `userinfo.email`). A request authenticated like this bypasses the rules.

Each lease or usage operation is one transaction on `/spend/<day>`, a few KB to a few tens of KB. The service runs with `max-instances=1` and a process mutex, so conflicts happen only across a revision rollout. The ETag keeps even that correct. *Assumption A4: conditional `PUT` with `if-match` is supported, and the day node stays small enough; above about 256 KB, move `leases` and `runs` into their own ledger per day.*

### 6.4 Security: who can write what

The Admin SDK, and any IAM-authenticated REST call, bypasses RTDB rules. So the question is which identity a job holds.

| Option | How | Can a compromised job lower others' counters or the global one? | Lift a cap? | Moving parts | Verdict |
|---|---|---|---|---|---|
| **A. Per-run custom tokens and rules** | The launcher (or the job) mints a Firebase custom token with `{repo, run}` claims. The job uses the RTDB client with rules limiting it to its paths | **Yes.** Refunding reservations needs *decrements* of the global and repository counters, which rules can't tell apart from theft. Minting also needs `signBlob` on a signing account: given to launchers, that lets every launcher mint owner claims; done by the job itself, the agent mints whatever it likes | No (rules) | Rules language, token minting, refresh (one hour) | Rejected |
| **B. Budget service (recommended)** | Jobs call `fugaro-budget` with a Google-signed ID token (audience = service URL) from the metadata server. The service maps the caller to a repository and performs every write itself | **No.** A job can only ask for leases and report usage for its own repository. The service computes every counter | No: caps are under `/config`, which the service never writes, and only admins' IAM reaches | One Cloud Run service, one scheduler target | **Adopt** |
| **C. IAM on RTDB instances** | Jobs get `firebasedatabase.admin` through IAM conditions, perhaps one instance per repository | Yes for the global counter, which every job must write. IAM on RTDB is per instance, not per path | Yes, if caps share the instance | Several instances, conditions | Rejected |

**How B identifies the caller.** Cloud Run IAM (`run.invoker`) admits only job accounts and `fugaro-scheduler`. The service then verifies the ID token (issuer, audience, expiry, `email_verified`) and looks up the account's display name, `Fugaro job <slug> <workflow>` (the §3.2 ownership mark; only IAM admins can set it), caching it for an hour. It then requires the account to be in the installation's project. A caller that doesn't map to a repository gets a 403.

- **Run identity** isn't bound: a job can claim any run ID of its own repository. That is the per-repository boundary §6.1 already accepts.
- **Owner writes** go directly from the CLI over REST with the owner's IAM (`firebasedatabase.admin`). The service doesn't take part, so a compromise of the service can't lift caps unless it writes `/config`, which its code never does. Its role could be narrowed further if Firebase offers a path-scoped role (it doesn't, to our knowledge).
- **Rules** are `{".read": false, ".write": false}`: no client SDK access at all. `fugaro init` deploys them over REST (`PUT /.settings/rules.json`), because the Firebase Rules API and Terraform don't manage RTDB rules. Firestore rules deny everything too (`google_firebaserules_ruleset` and `_release`).

### 6.5 The agent registry without `onDisconnect`

- **Writes.** The runner's `run/start` creates the entry. Every heartbeat updates `stage`, `round`, `verify`, `spentUsd` and `updatedAt`. `run/finish` deletes it and adds the outcome counts.
- **Crashed runs.** Their heartbeats stop. Watch marks an entry **silent** after 60 s without one, and **lost** after 3 minutes. The service's sweeper, which runs on each heartbeat at most once a minute, and the rollover delete entries not updated for 15 minutes, charging their leases in full (§5.3).
- **The alternative,** deriving the registry from Cloud Run executions as `ls` does, is authoritative about "is it running", but costs a Cloud Run listing per refresh, knows nothing about stage or spend, and puts watch on the backend API. The recommendation is to keep the registry in the service. Watch can press `l` to cross-check a lost entry against `ls` data (Decision D12).

### 6.6 Terraform

- **Installation module** (new `firebase.tf`, gated by `enable_budget`):
  - `google_project_service` for the APIs in §6.1.
  - `google_firebase_project`.
  - `google_firebase_database_instance` (`DEFAULT_DATABASE` or `USER_DATABASE`, region per §6.1).
  - `google_firestore_database` (`(default)`, `FIRESTORE_NATIVE`, `delete_protection_state = DELETE_PROTECTION_ENABLED`, and `prevent_destroy`).
  - `google_firebaserules_ruleset` and `google_firebaserules_release` for Firestore.
  - The `fugaro-budget` service account.
  - `google_cloud_run_v2_service` `fugaro-budget`: ingress all, IAM-authenticated, `max_instance_count = 1`, env `FUGARO_RTDB_URL`, `FUGARO_MODEL_PRICES` and the project.
  - The Scheduler job `fugaro-budget-rollover` (`30 0 * * *` UTC, OIDC as `fugaro-scheduler`).
  - The IAM of §6.1.
  - Outputs: `budget_service_url`, `rtdb_url`, `firestore_database`.
- **The providers.** At the time of writing, `google_firebase_project` and `google_firebase_database_instance` are documented as **google-beta** resources. The root adds `hashicorp/google-beta`, pinned like `google = 8.4.0`, and the lock file gets its hashes. The environment allowlist is unchanged (§8.2). *Assumption A5.*
- **Repository module.** The job account gets `run.invoker` on `fugaro-budget`. The job gets `FUGARO_BUDGET=<service URL>`, `FUGARO_BUDGET_MODE`, `FUGARO_MODEL_PRICES` and `FUGARO_MAX_RUN_USD` (the M9a local cap).
- **The service's image.** `fugaro budget serve` runs the `fugaro` binary. The recommendation is a small distroless image, `fugaro-service`, published next to the base images from M7. Until then `fugaro init` builds it with Cloud Build into `fugaro-base` (only operators write there) and pins it by digest (Decision D11).
- **Marks and discovery.** The service, service account, instance and Scheduler job carry `fugaro=managed` (the service account by its display name). Discovery adopts a marked instance and refuses an unmarked one of the same name, as §8.2 does.

### 6.7 `fugaro init` changes

- **Installation.** `fugaro init --budget [--budget-mode observe|enforce] [--rtdb-region R] [--firestore-location L] [--firebase-project P]`:
  - Discovery: an existing Firebase project and its default RTDB instance and Firestore database are **adopted** when they are empty or carry our marks. An RTDB instance can't carry labels, so its root `/fugaro/mark` is the mark, written by init. A non-empty database without the mark is refused.
  - After the apply, init deploys the RTDB rules and writes `/fugaro/mark`.
  - `--budget-admin` (repeatable) fills `terraform.budget_admins`.
  - The guard (§8.2) lists the Firestore database and the RTDB instance as `prevent_destroy`.
- **Repository.** `fugaro init --repo` picks up the budget variables from the installation's outputs. The first init after the budget is enabled prints `fugaro budget set --repo <owner/name> --daily …` when the repository has no cap and no default exists (only an admin can run it).
- **Local config (§5.4).** Written by `init`:

  ```yaml
  budget:
    mode: enforce                        # off | observe | enforce
    firebase_project: my-project         # default: project
    rtdb_url: https://my-project-default-rtdb.firebaseio.com
    firestore_database: "(default)"
    service_url: https://fugaro-budget-<hash>-<region>.a.run.app
    heartbeat: 15s
    unreachable_grace: 3m
    per_run_usd: 20                      # M9a's in-process cap; with the service, min(this, the caps)
  model_prices: {}                       # owner overrides (§5.2)
  ```

  Endpoint overrides for fakes (`endpoints.rtdb`, `endpoints.firestore`, `endpoints.budget`) follow the existing `endpoints.*` rules.

### 6.8 Cost at expected scale

Assume 20 repositories, about 100 runs a day of about 45 minutes each, 5 people watching for 2 hours a day:

- **RTDB.** Storage stays under 1 MB. Downloads are the watchers' streams (patches of about 200 B, about 1 per second while busy) plus the service's re-reads on conflict: under 1 GB/month, about $1.
- **Firestore.** About 600 writes and a few thousand reads a month: within the free tier, $0.
- **Budget service.** Heartbeats: 100 runs × 180 = 18k requests/day, about 0.6M/month, within the 2M free requests. CPU at about 20 ms each is about 3.4 vCPU-hours a month, within the free tier. About $0–2.
- **Scheduler.** One more job, about $0.10/month.
- **Total:** under $5/month.

---

## 7. `fugaro watch`

- **Library: Bubble Tea** (with `lipgloss` and `bubbles`, MIT licensed, pure Go, widely used), plus `teatest` for golden tests. The alternatives are `tview`, which is heavier and has its own widget model, and a plain ANSI redraw like `ls --watch`, which can't do selection, confirmation dialogs or resizing cleanly. This is the first TUI dependency, so it is pinned and reviewed like the rest of `go.mod` (Decision D13).
- **Data.** Three SSE streams, using the viewer's token (ADC with the `firebase.database` and `userinfo.email` scopes, Assumption A2): `/config`, `/spend/<today>` (re-subscribed at UTC midnight) and `/agents`. The token is refreshed on `auth_revoked` or after 50 minutes. If a stream stays down for more than 30 s, the header shows `⚠ live data stale 42s`.
- **Layout** (as in the spec, with Fugaro's terms):

  ```
  FUGARO ─ live                                    UTC 14:32   q/Esc exit
  GLOBAL  $41.27 (+$3.10 leased) / $150.00 today  ▓▓▓▓▓░░░░░░░ 29%  burn $0.82/min
  ▸ owner/app-a   $18.90 / $60.00  ▓▓▓▓░░░░░░ 32%  burn $0.31/min
      0142  Fix race in sync worker    review r2/3      sonnet→opus   $1.84  4m
      0143  Add pagination to /orders  implement · test               $0.42  1m
  ▸ owner/app-b   KILLED by owner 14:20 "runaway"      $22.37 / $60.00
      0150  Migrate auth middleware    halted (kill switch)           $0.77
  k kill all  K kill repo  r/R resume  ↑↓ select  enter details  l check lost  q exit
  ```

- **Burn rate.** A client-side rolling 5-minute window over `spentUsd + leasedUsd` samples, taken from the stream's events. It shows `—` until 60 s of data exists. It is highlighted when above `watch.burn_alert` (default: the daily cap spread over 8 hours).
- **Stuck agents.** Silent or lost (§6.5). A stage running past 80% of its `stageDeadline` is shown in amber.
- **Kill keys.** `k` asks "Halt ALL runs in the installation? Type `kill`". `K` names the selected repository and asks for a `y` confirmation. Resuming (`r`/`R`) needs typing the repository's name, or `resume` for global. Writes use the viewer's own IAM. Without `firebasedatabase.admin`, the key shows "you are not a budget admin" and does nothing.
- **Untrusted text.** Titles, reasons and PR URLs come from jobs (through the service). Watch strips every control and escape character and clips each field, so a compromised job can't inject terminal escape sequences.
- **Without Firebase** (no `budget:` block, or `--no-live`), watch polls the `ls` data every 10 s: the same grouping, each run's cost from `result.json`, no caps, no burn rate, and the kill keys disabled with a hint.
- **Detaching.** `q` or `Esc` closes the streams. Nothing in the cloud changes.

## 8. `fugaro budget`

| Command | Behaviour | Needs |
|---|---|---|
| `budget show [--repo R \| --all] [--json]` | Caps, today's spent and leased, notional, kill states, headroom, and active runs by repository | Viewer |
| `budget set (--global \| --defaults \| --repo R) [--daily USD] [--per-run USD] [--clear]` | Writes `/config/caps/…` with an ETag, and shows the old and new values. **Raising** a cap needs the confirmation (typing the project ID, or `--yes`). Lowering it doesn't | Admin |
| `budget kill (--all \| --repo R) [--reason TEXT]` | Sets `kill/…` with `by` (the git email, or `user`), `at` and `reason`. No typed confirmation, since it is the safe direction. Prints how many runs are in scope | Admin |
| `budget resume (--all \| --repo R)` | Clears the switch, after the typed confirmation | Admin |
| `budget prices [--json]` | The effective price table (embedded plus overrides), with sources and dates | None |
| `budget serve` | The service (hidden, for the container) | n/a |

- **Who is a budget admin.** Only budget admins (Decision D6). Launchers can't kill. Letting launchers kill but not resume would need the kill to go through the service with an allow-list: easy to add later.
- **Validation.** Caps must be finite, non-negative and at most $100,000. `--per-run` can't exceed `--daily` in the same scope.

## 9. History and `fugaro report`

- **Rollover.** Cloud Scheduler calls `POST /v1/rollover` at 00:30 UTC; only `fugaro-scheduler` may call it. For each day in the last 7 without `archived`, the service first charges leases that expired without a report (all have expired by then: the TTL is 10 minutes). It then writes `spendDaily/<day>_<slug>` with `Set`, which is idempotent through the fixed ID, sets `archived`, and deletes day nodes older than 8 days. It can be re-run or back-filled with `?day=`.
- **Why dated nodes.** The spec's single `daily` node that rolls over inside the next transaction races the archive job: if the first call after midnight lands before the job, yesterday's total is overwritten before it was copied.
- **Outcomes and models.** `outcomes` are counted at `run/finish`, for the day the run ended. `byModel` is counted at usage-report time, for the day of the lease. `computeUsd` is the runner's estimate, sent at `run/finish` (§10).
- **Missing data.** A run that never called `run/finish` (a crash) is counted as `infra_error` by the sweeper.
- **`fugaro report`:** `[--repo R | --all] [--week | --month | --year | --from D --to D] [--by day|week|month|repo|model] [--json]`. It reads Firestore (viewer IAM) and adds today's partial figures from RTDB, marked `(partial)`. Weeks are ISO weeks in UTC. The totals separate billed, notional and compute estimate, as §10.1 does. `--by model` shows the coder/reviewer split, which is the spec's test of "fan out cheap".

## 10. One account of cost

| Figure | Source of truth | Also shown in |
|---|---|---|
| Model dollars for a run (`api-key`, `vertex`, gateway on) | The gateway's ledger for the run | `result.json` `cost.model_usd` (`model_source: gateway`), the PR report, `ls`, `/spend/*/runs` |
| Model dollars without the gateway (`oauth`, or budget off) | Claude Code's `total_cost_usd` (as today) | The same, `model_source: claude-code`. `oauth` stays `model_basis: subscription`, notional |
| Model dollars against caps | RTDB `/spend/<day>` (spent + leased, including unreconciled crash charges) | watch, `budget show` |
| Daily history | Firestore `spendDaily`, copied from the RTDB ledger | `report` |
| Compute | Estimated, as §10.1 (the backend's billed duration × price) | Unchanged. It is also sent at `run/finish` into `computeUsd` (estimate), never capped |

- **The two can differ.** `ls --since 1d` sums `result.json` records; `report` sums the ledger. They differ only by crash charges and unreconciled leases, and `report` shows those separately (`unreconciledUsd`).
- **`cost_usd`** (the compatibility figure) keeps being the model figure, now from the gateway when it is on.
- **Compute price overrides and their provenance** are unchanged. The backlog's `compute_basis` item fits naturally beside `model_source`.

## 11. Threat model

**Assume a run's agent is compromised** (by prompt injection through repository content or a follow-up comment, §6.1). It runs as the same user as the runner and gateway, can reach the metadata server, and has unrestricted egress.

| Question | Answer with the M9 design |
|---|---|
| Read other repositories' budgets or registry entries? | **No.** Jobs have no Firebase or Datastore IAM. The service answers only about the caller's own repository, returning granted or refused, its own headroom and the kill state |
| Lift a cap or clear a kill switch? | **No.** `/config` is writable only with `firebasedatabase.admin`, which no job account holds, and the service never writes it |
| Lower the global or another repository's counters? | **No.** Every counter change is computed by the service from its own lease ledger. A job can't cause a decrement beyond its own lease |
| Drain the global daily cap (a denial of service)? | **Up to its repository's daily cap:** it can take leases and never report, or report falsely. Size repository caps so that their sum across a few misbehaving repositories leaves room. The service logs a lease-to-usage ratio anomaly |
| Spend model money around the gateway? | **Yes, which is the key residual risk.** On `api-key`, the real key is in the runner's environment (`/proc/<pid>/environ`, same uid). On `vertex`, the job account holds `roles/aiplatform.user` and the metadata server gives it a token |
| Tamper with the gateway in memory? | Same uid: `ptrace` may be possible, depending on the sandbox. Treated as the row above |
| Spoof sibling runs of the same repository? | Yes (run IDs aren't bound). The same per-repository boundary as §6.1 |
| Inject into watch? | Titles and reasons are sanitized (§7) |

**What would make the caps hard,** in order of cost:

1. **Provider-side backstops, with no code.** For `api-key`, one Anthropic Console **workspace per repository**, each with its own **spend limit**: a hard monthly cap the provider enforces (Assumption A7). For `vertex`, request and token **quotas** per project and model (a rate, not dollars), plus the existing billing-budget alerts. Recommend documenting both at M9b.
2. **A gateway sidecar** (`api-key` only). A second container in the job holds the key, so the agent's container never has it. *Assumption A8: Cloud Run jobs support sidecars and keep the secret mount per container.* This doesn't help on Vertex, where both containers share the service account.
3. **An external gateway.** The budget service (or a sibling) proxies the model traffic, and job accounts lose `aiplatform.user` and the key. That makes the caps hard for both auth modes, at the cost of a streaming proxy on Cloud Run (a long request per call, billed by instance time) and a larger blast radius. This is the post-M9 hardening path (Decision D2 records the choice).

## 12. Failure modes

| Failure | Effect | Behaviour |
|---|---|---|
| Budget service or RTDB unreachable, run in flight | The gateway spends its current lease (already counted). A top-up retries with backoff for `unreachable_grace` (3 min), and heartbeats keep retrying | After the grace: **halted**, reason `budget_unavailable`, draft PR. Fail closed |
| Unreachable at bootstrap | `run/start` fails | Retries for the grace period, then `halted` with outcome `none`, exit 0 |
| Unreachable at launch | The launch pre-check can't read the caps | `fugaro run` refuses (exit 2, a remote failure). `--no-budget-check` lets the runner decide |
| `oauth` run, service unreachable | No leases involved, but heartbeats (the kill switch) fail | The same grace, then halted (Decision D14: fail closed for every auth mode once the budget is on) |
| Daily image check and rebuilds | They make no model calls and don't use the gateway | **Unaffected.** No budget IAM, no dependency |
| `fugaro verify`, finalize, writeback | No model calls | Unaffected. A halted run still pushes and opens its draft PR |
| Service restarts during a transaction | The ETag write either landed or didn't. The gateway retries idempotently: lease and usage requests carry a client ID, and the service keeps a small de-duplication set in the day node | No double counting |
| Job killed hard (task timeout, OOM) | No `run/finish`; the lease is outstanding | The sweeper charges the lease in full and counts `infra_error`. A late report refunds within 24 h |
| Firestore down during the rollover | The day isn't archived | The next rollover retries (7-day lookback). RTDB keeps 8 days |
| Clock skew between a job and the service | Leases use the service's time and the service's day | Jobs never compute the day |
| Price table stale (a list price changed) | Over- or under-accounting | Prices show their check date. `budget prices` warns after 90 days. The Claude Code cross-check (5%) flags drift per stage |
| Watch loses its stream | The view goes stale | Header warning, and reconnection with backoff. Nothing in the cloud is affected |

## 13. Backward compatibility and rollout

- **Off by default.** With no `budget:` block, `init` enables nothing, jobs get no `FUGARO_BUDGET`, the runner starts no gateway, and the agent's environment stays exactly as today (golden test). `watch` degrades (§7), and `budget` and `report` say "budget isn't set up (fugaro init --budget)".
- **Schema.** Additive only (§5.9). `result.json` stays at version 1. The `fugaro.yaml` keys `agent.models`, `agent.max_output_tokens`, `agent.verify_retries` and `agent.max_run_tokens` are optional. An older runner reading a newer `fugaro.yaml` refuses unknown keys (strict decoding), so **repositories adopt the new keys only after their images carry the M9 runner.** The onboarding skill and `validate` say so.
- **Image dependency.** The gateway lives in the runner, which is in the base image. A repository gets it through a derived-image rebuild (`fugaro image build`, or the daily check's `base` trigger once M7 publishes images). A job with `FUGARO_BUDGET` whose runner predates M9 ignores the variable, so `init --repo` **refuses to enable enforcement** for a workflow whose image record names a runner older than M9, and says to rebuild.
- **Rollout order.**
  1. `init --budget --budget-mode observe`.
  2. Set generous caps.
  3. Rebuild the images.
  4. Watch a week of `report --by model` and `budget show`.
  5. Set the real caps.
  6. `--budget-mode enforce`.
- **Rollback.** `--budget-mode off`, then re-init. Firebase data stays (`prevent_destroy`). `--forget` gains the budget resources in its phase-2 state removal.

## 14. Testing strategy

- **Gateway (unit).** Against an `httptest` fake upstream that streams scripted SSE:
  - `message_start` and `message_delta` usage, including cumulative deltas and updated input.
  - Pings, errors mid-stream, disconnects, 429/529 with `retry-after`, non-streaming bodies, Vertex paths, `count_tokens`.
  - A fallback serving model.
  - Checks that the bytes on the client side equal the upstream's (no rewriting) and that nothing is buffered (a slow upstream is still flushed incrementally).
  - The table of §5.5, row by row.
  - A property test: for random call sequences, `used ≤ granted` at every step.
- **Pricing.** Golden tests per model: cache multipliers, the long-context tier, web search, unknown model at max rates, and override parsing.
- **Budget service.** Against **an in-process RTDB fake** (`internal/gcpfake` gains `rtdb.go`: `GET`, `PUT`/`PATCH`/`DELETE`, ETag and `if-match`, SSE events, and a switch to fail a write after it landed). Test the lease algorithm and every refusal reason, near-cap shrinking, crash charging and late refunds, idempotent retries, the two-day per-run sum, and the rollover's idempotency.
- **Concurrency.** 50 goroutines racing leases against 3 caps never exceed any cap, with the fake injecting 412s.
- **Identity.** Forged, expired, wrong-audience and unmapped tokens are refused, using a local JWKS.
- **Firestore.** Behind a narrow interface (`SpendStore`) with an in-memory fake. The Firestore emulator (Java) runs only under a `firestore` build tag in CI, like `docker`.
- **Runner.** With the fake `claude` taught to call the gateway through `ANTHROPIC_BASE_URL`:
  - A halt mid-implement gives a draft PR, `status: halted` and the report.
  - A halt at bootstrap gives outcome `none` and exit 0.
  - Kill during review stops within one heartbeat.
  - Kill during finalize is ignored.
  - An unpinned model means `failed`.
  - `observe` never halts.
  - The agent's environment has no real key (a secret scan like M6's).
  - Managed settings are written before the first stage.
- **CLI.** `budget` against the RTDB fake. `watch` with `teatest` golden frames (grouping, burn, silent and lost, kill confirmation, sanitized hostile titles, the degraded mode). `report` against the fake store. `ls` and `diagnose` with `halted` records.
- **Terraform.** `validate` and `tflint` with google-beta. The tfvars golden files, and `init` discovery against `gcpfake` for Firebase adoption and refusal.
- **Live.** New checks in `gcp-live-checklist.md`:
  - Observe mode on the sandbox.
  - An enforced $0.50 per-run cap halting a real run.
  - A global kill during a run.
  - The rollover writing a document.
  - `watch` against the real RTDB.

## 15. First-cut task breakdown

Sizes: **S** is about a day or less, **M** a few days, **L** about a week.

**M9a — the gateway, pinned models and a local per-run cap (no Firebase)**

1. `internal/pricing`: the table, aliases, tiers, overrides and `FUGARO_MODEL_PRICES`. **M**
2. `internal/gateway`: proxy, SSE usage tee, worst-case bound, local lease (static, from `FUGARO_MAX_RUN_USD`), allow-list, Vertex path. **L**
3. Agent environment per auth mode with the gateway, and managed settings; no real key in the agent's environment. **M**
4. `fugaro.yaml`: `agent.models`, `max_output_tokens`, pinning validation when the budget is on, the per-stage pin variables. **M**
5. Runner: `ErrHalted`, the `halted` status and `halt` block, the report section, exit code, `runview` and the schema. **M**
6. `cost.model_source` and `model_by`, and the Claude Code cross-check. **S**
7. Local config `budget:` (`mode`, `per_run_usd`), `model_prices`, and the tfvars and job environment. **S**
8. Tests (the §14 gateway and runner rows) and docs (v1 §4.5, §4.6, §5, §10 updates). **M**

**M9b — the budget service, RTDB counters and kill switches**

9. `internal/rtdb`: the REST client (ETag, PUT/PATCH, SSE), and the `gcpfake` RTDB. **M**
10. `internal/budget`: the ledger and lease algorithm, sweeper and identity mapping, and `fugaro budget serve`. **L**
11. Gateway top-up leases, usage reports, heartbeat and kill, and `run/start` and `run/finish`. **M**
12. `fugaro budget show/set/kill/resume/prices`, and the `run` pre-check. **M**
13. Terraform: Firebase, RTDB, Firestore (created now for M9d), the service and its IAM, google-beta; `init --budget`, discovery and adoption, rules deployment, the service image. **L**
14. Live checks and the docs (a new §, gcp-setup.md). **M**

**M9c — `fugaro watch`**

15. The TUI model and views, SSE feeds, burn and stuck detection, kill keys, sanitizing, the degraded mode, and golden tests. **L**

**M9d — history and reports**

16. `/v1/rollover` with Firestore writes, the Scheduler job, the lookback and pruning. **M**
17. `fugaro report` (ranges, grouping, the partial day, JSON). **M**

**M9e — optional task-loop alignment**

18. The verify gate before review and `agent.verify_retries`. **M**
19. Structured findings in the final report, and `reviews[].findings` detail in `result.json`. **S**

---

## 16. Decisions needed from the user

| # | Decision | Options | Recommendation and consequences |
|---|---|---|---|
| D1 | **Who writes budget state** | A: custom tokens and rules · **B: the budget service** · C: IAM per instance | **B.** It is the only option where a compromised job can't lower shared counters. It reverses v1's "no control-plane service" non-goal: one Cloud Run service at about $0/month, and a new image to publish |
| D2 | **How hard must the caps be** | **Guardrail now (the in-container gateway, plus the provider-side backstops of §11)** · a sidecar for `api-key` in M9 · the external gateway in M9 | **Guardrail now**, with the backstops documented and the external gateway as the post-M9 hardening item. The consequence: caps stop runaway agents absolutely, but a compromised agent only by provider-side limits |
| D3 | Firebase project | **Add Firebase to the installation's project** · a separate project the user created | **Same project:** one IAM model and one `init`. A separate one works through `budget.firebase_project`, but needs cross-project grants |
| D4 | Regions | RTDB: **us-central1** / europe-west1 / asia-southeast1 · Firestore: **the installation region** / nam5 | RTDB is forced to one of three, so pick the nearest. It can't be moved later |
| D5 | Budget day | **UTC** · one installation-wide time zone | **UTC:** no DST days of 23 or 25 hours, and it matches run IDs. A local zone is a later field |
| D6 | Who may kill and resume | **Budget admins only** · launchers may kill (not resume) through the service | **Admins only** in M9. Kill-for-launchers is a small later addition |
| D7 | What caps count | **Model dollars only** · model plus estimated compute | **Model only.** Compute is small (about $0.40 a run), estimated, and not per call |
| D8 | An unpinned model in a request | **Reject (the stage fails)** · rewrite to the pinned model | **Reject.** Anthropic's gateway guide warns against body rewrites. Pinning environment variables make aliases resolve correctly, so a rejection means a real misconfiguration |
| D9 | Halted before the branch exists | **`halted`, outcome `none`, no PR, exit 0** · a draft PR anyway · `infra_error` | **No PR:** nothing was done, so a draft would be noise. Exit 0 keeps policy stops out of the infrastructure-failure signals |
| D10 | Non-Anthropic coder models | **Defer (A/B first)** · experimental in M9 | **Defer.** Anthropic doesn't support it. Use a small Claude model as `coder` for now |
| D11 | The budget service's image | **Dedicated distroless `fugaro-service`** · reuse a base image | **Dedicated** (under 20 MB, no toolchain). Until M7, `init` builds it into `fugaro-base` |
| D12 | Registry source | **Service heartbeats plus a sweeper** · derived from Cloud Run executions | **Heartbeats.** It carries stage, round and spend. Executions are the cross-check (`l` in watch) |
| D13 | TUI library | **Bubble Tea** · tview · a plain redraw | **Bubble Tea.** It is testable with `teatest`, and it is the first TUI dependency |
| D14 | Fail closed for `oauth` runs | **Yes: halt after the grace, like the others** · keep running without heartbeats | **Yes.** One invariant: with the budget on, no run continues unseen. The consequence: a budget outage stops subscription runs too |
| D15 | Draft PR from the start, per-round PR comments, the verify gate | **Defer, defer, optional M9e** · adopt all in M9 | **As recommended in §2.4.** The spec's comment format goes into the final report now |

## 17. Assumptions to verify before building

| # | Assumption | How to check | If it fails |
|---|---|---|---|
| A1 | Anthropic's terms allow (a) unattended cloud use of an owner's own `claude setup-token` (M4 already relies on this), and (b) whether a loopback gateway holding that token would be "intermediation" | Read the Consumer Terms and Claude Code's legal page. Ask Anthropic (the legal page says to contact sales) | (b): no gateway for `oauth`, which is the plan anyway. (a): an existing risk to raise with the user regardless of M9 |
| A2 | RTDB REST accepts a user ADC token from `gcloud auth application-default login` (cloud-platform scope), or needs the `firebase.database` and `userinfo.email` scopes | Try against the sandbox instance | Document the `--scopes` login, or have `budget` and `watch` mint a scoped token through `gcloud auth print-access-token --scopes` |
| A3 | Requests that fail before `message_start` (4xx, 429, 529) aren't billed; interrupted streams bill the tokens generated | Anthropic docs and support, then a live check comparing the Console usage | Charge failed requests' input too (a price-table flag) |
| A4 | RTDB REST supports a conditional `PUT` with `if-match` at our node sizes and rates | Sandbox test with the fake's semantics mirrored | Split the ledger per day (leases and runs separate), or move the ledger to Firestore transactions (a change to decision 1) |
| A5 | `google_firebase_project` and `google_firebase_database_instance` are google-beta only at the pinned version | Provider docs at pin time | Create the RTDB instance through its management REST API in `init` instead of Terraform |
| A6 | Claude Code's managed settings `env` overrides project `.claude/settings.json` `env`, including `ANTHROPIC_BASE_URL` and the Vertex variables | Test with the real `claude` in the base image | Without it, a repository's settings file can bypass the gateway (Vertex most of all). Then consider refusing repositories whose settings set `ANTHROPIC_*` or `CLAUDE_CODE_*` |
| A7 | Anthropic Console offers per-workspace spend limits usable as a hard backstop per repository | Console docs | Document billing alerts only |
| A8 | Cloud Run jobs support sidecar containers with per-container secret mounts | Cloud Run docs | The sidecar option in §11 drops out |
| A9 | Claude Code sends `max_tokens` at or below `CLAUDE_CODE_MAX_OUTPUT_TOKENS`, and the stream's usage fields are as documented through Vertex `streamRawPredict` | A recorded live session through the gateway on each auth mode | Reserve with the model's maximum output and log |
| A10 | Every model call Claude Code makes (auxiliary, compaction, subagents, classifiers) goes through `ANTHROPIC_BASE_URL`. The documented exceptions (fast-mode availability, WebFetch's domain check) are free | Gateway log versus Claude Code's own `total_cost_usd` in a live run | Budget the gap as overhead per stage, or block direct egress later |
| A11 | Prices in the embedded table are current for the pinned models, including per-model cache-read multipliers and long-context tiers | Anthropic pricing page and the Vertex pricing page on the build date | Update the table. Owner overrides cover the gap |

## 18. Suggested milestone split

- **M9a — the gateway and pinning (no Firebase).** It delivers exact per-call model accounting, pinned models per stage, no real key in the agent's environment, a per-run cap enforced in process, and the `halted` status. It is useful on its own: it closes the "silent default model" and "runaway single run" risks.
- **M9b — shared caps and kill switches.** The budget service, RTDB dated ledger, leases, daily caps per repository and globally, kill switches, `fugaro budget`, the launch pre-check, observe mode and the registry heartbeats. This is where the spec's §5 is met. Firebase is introduced here: Firestore is created but unused until M9d.
- **M9c — `fugaro watch`.** A read-mostly consumer of M9b's data. It can ship any time after M9b.
- **M9d — history.** The rollover, `spendDaily`, `fugaro report`. It is independent of M9c.
- **M9e (optional) — task-loop alignment.** The verify gate before review, and structured per-round findings in the report.
- **Later (not M9).** The external gateway (hard caps against a compromised agent), non-Anthropic coders after an A/B, draft-from-start PRs and per-round comments, the `redundant` mode, per-batch concurrency, kill for launchers, and a local-time budget day.

Each slice is its own implementation plan under `docs/plans/`, written after this design is approved.
