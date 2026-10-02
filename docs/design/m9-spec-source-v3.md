# Fugaro — Spec v3: Task Loop, Harnesses, Routing, Budget Guardrails, Dashboard & Spend History

**Repo:** `dimipaun/fugaro` (MIT) · **Runtime:** GCP Cloud Run jobs · **Routing:** Anthropic direct + OpenRouter for everything else · **State:** Firebase RTDB (live) + Firestore (history)

---

## What Changed in v3

This document replaces v1 and v2 in full. It is the single source of truth; do not combine it with earlier versions.

1. **Split routing (§5).** v2 routed every model through OpenRouter. v3 sends **Anthropic models directly to Anthropic** and sends **everything else through OpenRouter**. The reason is prompt caching: routing Claude Code through OpenRouter has a documented history of cache markers not surviving the gateway, which silently multiplies token cost on agent loops. Caching matters most on the most expensive model, the senior reviewer.
2. **Route per model slot (§2.1).** Each slot now carries a `route` (`anthropic-direct` or `openrouter`), and the model ID uses that route's namespace.
3. **Cloud Run infra cost (§7.4, new).** Job compute (vCPU-seconds and memory-seconds) is tracked, counted against the budget caps, and reported separately from token cost. In practice it runs at roughly 10–20% of model cost.
4. **Infra cost shown everywhere spend appears.** The agent registry (§8), the dashboard (§9) and the daily records (§10) now split cost into `tokensUsd`, `infraUsd` and an all-in `spentUsd`.
5. **Secrets (§5.6, new)** are documented as living in GCP Secret Manager.
6. **Current status (§0, new).** Fugaro has run end to end with Claude only; the two-stage loop is the main piece still to build.

### Carried over from v2

- Two-stage review loop: cheap loop first, then a senior gate (§3).
- Three model slots per task (§2).
- Harness plugin interface, pairing each model with its native harness (§4).
- Model defaults: DeepSeek V4 Flash, V4 Pro for hard tasks, Sonnet 5.5 as senior reviewer (§2.2).

---

## 0. Current Status

- **Proven:** a full end-to-end run on Cloud Run, using a single model (Claude via Claude Code).
- **Already in place:** secrets in GCP Secret Manager.
- **Still to build and prove:** the two-stage cheap/senior loop, the DeepSeek harness routed through OpenRouter, the RTDB budget guardrails, the dashboard, history recording, and infra cost tracking.
- **First milestone:** one real task that goes through both stages and reaches READY, with token and infra spend appearing correctly on the dashboard.

---

## 1. Goals

- Run many coding-agent tasks in parallel in the cloud, off the laptop.
- Each task ends as either a **mergeable PR** or a **clearly labelled failure**, with no babysitting and no respins.
- Spend is **bounded by hard caps** (per project and global) that cannot be exceeded, with kill switches.
- Spend and agent activity are **visible live**, grouped by project, and **recorded historically** for reporting.
- Models and harnesses are **swappable by configuration**, so Fugaro can move to whichever model is cheapest and still capable at the time.

**Core cost principle:** fan out cheap, funnel expensive.
- Cheap models do almost all token volume, including the first rounds of review.
- Deterministic checks filter for free.
- The senior reviewer sees each task about once, with lean context: the diff and the task, never the whole repo.

---

## 2. Task Configuration

### 2.1 Task Definition

Every task is dispatched with three independently set model slots.

```json
{
  "taskId": "t_20261001_0142",
  "project": "project-a",
  "repo": "github.com/org/project-a",
  "baseBranch": "main",
  "instructions": "Fix the race condition in the order sync worker.",
  "difficulty": "normal",
  "coder": {
    "harness": "deepseek-native",
    "route": "openrouter",
    "model": "deepseek/deepseek-v4-flash",
    "temperature": 1.0,
    "maxTokensPerCall": 8000
  },
  "cheapReviewer": {
    "harness": "review-basic",
    "route": "openrouter",
    "model": "deepseek/deepseek-v4-flash",
    "maxTokensPerCall": 4000
  },
  "seniorReviewer": {
    "harness": "review-anthropic",
    "route": "anthropic-direct",
    "model": "claude-sonnet-5-5",
    "maxTokensPerCall": 4000
  },
  "limits": {
    "maxBuildRetries": 5,
    "maxCheapReviewRounds": 3,
    "maxSeniorBounceBacks": 1
  },
  "mode": "single"
}
```

Model IDs use the namespace of their route: **Anthropic API IDs** for `anthropic-direct` (e.g. `claude-sonnet-5-5`) and **OpenRouter model strings** for `openrouter` (e.g. `deepseek/deepseek-v4-flash`). They must be **pinned explicitly**. Never rely on a provider or tool default, because defaults can silently move to a more expensive tier. Check the exact IDs against Anthropic's and OpenRouter's model lists before deploying.

### 2.2 Model Defaults

| Slot | Default | Notes |
|---|---|---|
| Coder | DeepSeek V4 Flash | Cheapest serious coder and strongest on DeepSeek's coding benchmarks. DeepSeek recommends `temperature: 1.0`. |
| Coder (hard tasks) | DeepSeek V4 Pro | Used when `difficulty: "hard"`. Better for multi-file, reasoning-heavy patches; still far below frontier pricing. |
| Cheap reviewer | DeepSeek V4 Flash | Catches the obvious issues in early rounds. |
| Senior reviewer | Claude Sonnet 5.5 | One final judgement on correctness and mergeability. |

**A/B candidates for the coder slot after launch:** a Qwen coder-tier model, and Kimi (Moonshot AI), which is built for parallel sub-agent work.

**Comparison metric:** the cost to reach a senior-approved PR, together with the number of rounds it took. Price per token alone is not the metric, because a cheap model that needs more rounds can cost more per finished task.

**Data note:** DeepSeek, Qwen and Kimi are China-based providers. Decide per project whether its code may be sent to them. Projects with sensitive or client code can be restricted to an allowlist of providers (§5.5).

### 2.3 Pricing Table

Pricing is used for pre-call cost estimates (§7.3). Actual cost is taken from what the provider reports (§7.3).

```json
{
  "pricing": {
    "deepseek/deepseek-v4-flash": { "inputPerM": 0.0, "outputPerM": 0.0 },
    "deepseek/deepseek-v4-pro":   { "inputPerM": 0.0, "outputPerM": 0.0 },
    "claude-sonnet-5-5":           { "inputPerM": 0.0, "outputPerM": 0.0,
                                     "cacheReadPerM": 0.0, "cacheWritePerM": 0.0 }
  },
  "routingFeePct": { "openrouter": 5.5, "anthropic-direct": 0 },
  "infra": {
    "vcpuSecondUsd": 0.0,
    "gibSecondUsd": 0.0
  }
}
```

Fill in current prices from Anthropic, OpenRouter and Cloud Run pricing for the job's region. Set the OpenRouter `routingFeePct` to the fee that actually applies to the account. Direct Anthropic traffic carries no routing fee.

---

## 3. Task Lifecycle: the Two-Stage Loop

One Cloud Run job runs one full task. The whole loop runs inside that job, which avoids the setup and cold-start cost of respinning.

### 3.1 Flow

```
PROVISIONING
  → clone repo, create branch, open PR as DRAFT

┌─ STAGE 1: CHEAP LOOP ───────────────────────────────────────────┐
│ CODING              cheap coder implements the task or findings │
│ BUILDING            compile → test → lint (zero model tokens)   │
│   fail → back to CODING with the error output                   │
│          (counts toward maxBuildRetries, not review rounds)     │
│   maxBuildRetries exceeded → FAILED                             │
│ SELF_REVIEWING      cheap reviewer sees the task and diff only  │
│   REJECT and cheapRound < 3 → back to CODING with findings      │
│   APPROVE, or cheapRound = 3 → go to Stage 2                    │
└─────────────────────────────────────────────────────────────────┘

┌─ STAGE 2: SENIOR GATE ──────────────────────────────────────────┐
│ SENIOR_REVIEWING    senior reviewer sees the task, the diff,    │
│                     and the last cheap-review findings          │
│   APPROVE → READY                                               │
│   REJECT and bounceBacks < 1 → new Stage 1 cheap loop, with     │
│                                senior findings as instructions  │
│   REJECT and bounceBacks = 1 → FAILED                           │
└─────────────────────────────────────────────────────────────────┘

READY    PR flipped from draft to ready for review
FAILED   PR stays DRAFT; a final comment lists unresolved issues
HALTED   budget cap or kill switch hit; PR stays DRAFT with a "halted" comment
```

### 3.2 Rules

- **No model reviews code that fails to compile or fails tests.** Deterministic checks always run first.
- **Cheap loop:** at most 3 cheap review rounds per pass.
  - If the cheap reviewer still rejects at round 3, the task **still goes to the senior gate**, carrying the cheap reviewer's unresolved findings. This gives the senior reviewer a chance to overrule a reviewer that is being too strict, rather than failing the task outright.
  - *Alternative to decide (§11):* fail immediately at cheap round 3 to save the senior call.
- **Senior gate:** at most 2 senior reviews per task, meaning the initial review plus 1 bounce-back. A bounce-back starts a new cheap loop with a fresh round counter.
- **Worst case per task:** 2 cheap loops × 3 cheap rounds, plus 2 senior reviews, plus the build retries. This bound is used for the per-run cap (§7).
- **The PR stays draft for the whole run.** Switching from draft to ready is the only signal that a human should look at it, and only senior approval can trigger it.
- **Every exit path is defined.** A run cannot loop forever, and it cannot become ready without senior approval.

### 3.3 Review Comment Format

There is one structured comment per review, readable by people and parseable by the coder. Cheap and senior reviews use the same format and are labelled by stage.

```markdown
## Fugaro Review — Cheap Round 2 of 3
**Verdict:** REJECT
**Reviewer:** deepseek/deepseek-v4-flash

### Blocking
1. `src/sync/worker.ts:88` — lock released before the write completes; race still possible.
2. No test covers concurrent sync of the same order.

### Non-blocking
- Rename `tmpRes` to something descriptive.

<!-- fugaro:findings {"stage":"cheap","round":2,"verdict":"REJECT","blocking":2,"nonBlocking":1} -->
```

```markdown
## Fugaro Review — Senior Review 1 of 2
**Verdict:** APPROVE
**Reviewer:** claude-sonnet-5-5

No blocking issues. Change is correct and covered by tests.

<!-- fugaro:findings {"stage":"senior","round":1,"verdict":"APPROVE","blocking":0,"nonBlocking":0} -->
```

The final comment on failure:

```markdown
## Fugaro — Did not pass senior review
PR left as draft after 2 senior reviews. Unresolved blocking issues:
1. ...
```

**Reviewer prompt guidance:**
- The senior reviewer is told the cheap loop has already run. Its job is correctness, subtle bugs, and whether the change is mergeable, not style nitpicks.
- The cheap reviewer is told to concentrate on obvious defects, missing tests, and whether the code follows the task instructions.

---

## 4. Harness Abstraction (new)

Fugaro is **harness-agnostic by design**. Every agent harness sits behind one plugin interface.

### 4.1 Interface

```ts
export interface HarnessContext {
  workdir: string;            // checked-out repo
  route: "anthropic-direct" | "openrouter";
  model: string;              // ID in the route's namespace
  baseUrl: string;            // https://api.anthropic.com or https://openrouter.ai/api/v1
  apiKey: string;             // loaded from Secret Manager for the chosen route
  temperature?: number;
  maxTokensPerCall: number;
  guard: SpendGuard;          // every model call MUST go through guard (§7.3)
  onStatus: (status: AgentStatus) => Promise<void>;
  signal: AbortSignal;        // fires on kill switch or cap hit
}

export interface CodeResult {
  diff: string;
  summary: string;
  usage: { inputTokens: number; outputTokens: number; costUsd: number };
}

export interface ReviewResult {
  verdict: "APPROVE" | "REJECT";
  blocking: string[];
  nonBlocking: string[];
  commentMarkdown: string;
  usage: { inputTokens: number; outputTokens: number; costUsd: number };
}

export interface CoderHarness {
  id: string;                               // e.g. "claude-code", "deepseek-native", "pi"
  code(task: string, findings: string | null, ctx: HarnessContext): Promise<CodeResult>;
}

export interface ReviewerHarness {
  id: string;                               // e.g. "review-basic", "review-anthropic"
  review(task: string, diff: string, priorFindings: string | null,
         ctx: HarnessContext): Promise<ReviewResult>;
}
```

### 4.2 Rules

- **Launch with one coder harness and one reviewer harness.** The seam is designed now, and further harnesses (OpenAI's, the open-source Pi harness, DeepSeek's own) are added later as plugins.
- **Pair each model with its native harness where possible.** Claude Code can technically be pointed at DeepSeek through an Anthropic-compatible endpoint. However, a harness is tuned for its native model's prompting and tool-call format, and a foreign model tends to make rougher tool calls and need more retries. Treat cross-pairings as experiments, not defaults.
- **Harnesses never call models directly.** Every call goes through `ctx.guard`, so budget enforcement cannot be bypassed by a plugin.
- **Harnesses honour `ctx.signal`,** stopping work promptly when a kill switch or cap fires.
- **The reviewer harness can be simple:** a single prompt and response with structured output parsing. It needs no agent loop, because it only reads the diff.
  - `review-basic` calls an OpenAI-compatible endpoint (OpenRouter) and is used for the cheap reviewer.
  - `review-anthropic` calls the Anthropic Messages API directly with explicit `cache_control` on the stable prefix (the system prompt and task). This means a bounce-back senior review reuses the cache.
- **Route is a per-slot setting, independent of the harness.** OpenRouter only carries the request; the harness still runs the agent loop. A harness can be pointed at either route by changing `baseUrl` and `apiKey`.

---

## 5. Routing: Anthropic Direct, Everything Else via OpenRouter

### 5.1 The Rule

| Model family | Route | Harness |
|---|---|---|
| Anthropic (Sonnet, Opus) | **Direct to Anthropic** | Claude Code for coding; `review-anthropic` for review |
| DeepSeek, Qwen, Kimi, others | **OpenRouter** | Native harness for the model (e.g. DeepSeek's); `review-basic` for review |

OpenRouter is the transport, not a harness. The two layers stack: the harness runs the agent loop, and the route decides which endpoint receives the calls.

### 5.2 Why Anthropic Goes Direct

- **Prompt caching is the largest cost lever in an agent loop.** Each step resends a large prefix that has barely changed, and a cache read costs about 0.1x the normal input price.
- **Caching through OpenRouter is unreliable for Claude.** Routing Claude through OpenRouter, especially through OpenAI-compatible code paths, has a well-documented history of cache markers not reaching Anthropic. When that happens, caching stops working without any error, and token cost multiplies. One public report measured about 13x.
- **The risk lands on the most expensive model.** The senior reviewer (and Claude Code, if used as a coder) is where losing the cache would cost the most.
- **Going direct costs nothing extra.** There is no routing fee, and caching works natively.

### 5.3 Why Everything Else Goes Through OpenRouter

- **One endpoint and one key** for DeepSeek, Qwen, Kimi and others. A coder A/B test is a change to one string.
- **Automatic provider fallback,** and **cost reporting per request.**
- **Caching works for DeepSeek through OpenRouter,** with cache reads at about 0.1x. The caveat is that DeepSeek builds its cache on a best-effort basis and it can take a few seconds, so a very fast follow-up call may miss the cache. That is acceptable at the cheap tier.
- **The routing fee is about 5.5–8%**, accepted during the A/B phase. Revisit going direct to a provider once one cheap model is chosen and runs at steady high volume. Because the route is a per-slot setting, that change is configuration only.

### 5.4 Validation Before Fleet Use

Each pairing of harness and route is validated once before it is trusted in the fleet:

1. Tool calls make a clean round trip (no malformed tool-call or response parsing errors).
2. Cache hits are confirmed on repeated requests with the same prefix:
   - Anthropic reports this in `usage.cache_read_input_tokens`.
   - OpenRouter reports it in `usage.prompt_tokens_details.cached_tokens`, with DeepSeek's `prompt_cache_hit_tokens` as a fallback.
3. The reported cost matches what is expected from the pricing table.

Fugaro logs the cache-hit ratio for each call. A ratio that drops to around zero on a long loop raises an alert, because it is the signal that caching has silently failed.

### 5.5 Provider Policy per Project

```json
{
  "projects": {
    "project-a": { "allowedProviders": ["anthropic", "deepseek", "qwen", "moonshotai"] },
    "client-x":  { "allowedProviders": ["anthropic"] }
  }
}
```

- Fugaro rejects any task whose model slots name a provider that the project does not allow.
- For traffic through OpenRouter, provider-routing preferences are set so that a fallback cannot reroute to a provider that isn't allowed.
- Direct Anthropic traffic never touches the gateway.

### 5.6 Secrets

All credentials live in **GCP Secret Manager** and are read by the job at startup:

- the Anthropic API key (direct route)
- the OpenRouter API key
- the GitHub token (clone, push, PR operations)
- Firebase service account credentials

The job's service account is granted access only to the secrets it needs. Harnesses receive keys through `HarnessContext` and never read Secret Manager themselves.

---

## 6. Parallelism Modes

| Mode | When to use | Behaviour |
|---|---|---|
| **Wide** | Independent work: unrelated bugs, isolated test files, boilerplate across modules | High concurrency |
| **Narrow** | Coupled work: shared files, dependent changes | Low concurrency limit (default 3–4) |
| **Redundant** | A single hard task | N cheap coder attempts at the same task; the best candidate that passes the cheap loop goes to the senior gate |

- The concurrency limit is set **per batch**. Parallelism should match how independent the tasks are, not how much capacity is available.
- Redundant mode keeps senior spend at roughly one review per task, because only one candidate reaches the senior gate.

---

## 7. Budget Guardrails (Firebase RTDB)

### 7.1 Model

- **Nested caps.** There is a per-project cap and a global cap across all projects. Every model call must pass **both**.
- **Kill switches.** A global switch halts the whole fleet; a per-project switch halts only that project.
- **Two periods.** Each cap has a per-run limit and a per-day limit. This means a reasonable run that is repeated many times is still stopped.
- **Fail closed.** If a job cannot reach the budget node, it does not spend.
- **All-in spend.** The caps count token cost **and** Cloud Run infra cost (§7.4).
- **Atomic updates.** All counter changes use RTDB transactions, so two concurrent jobs cannot both read "90 spent" and both proceed.

### 7.2 Node Structure

```json
{
  "budget": {
    "global": {
      "killSwitch": false,
      "daily":  { "date": "2026-10-01", "spentUsd": 41.27, "limitUsd": 150.00 },
      "limitPerRunUsd": 100.00
    },
    "projects": {
      "project-a": {
        "killSwitch": false,
        "daily": { "date": "2026-10-01", "spentUsd": 18.90, "limitUsd": 60.00 },
        "limitPerRunUsd": 40.00
      },
      "project-b": {
        "killSwitch": false,
        "daily": { "date": "2026-10-01", "spentUsd": 22.37, "limitUsd": 60.00 },
        "limitPerRunUsd": 40.00
      }
    }
  },
  "runs": {
    "r_20261001_01": {
      "project": "project-a",
      "startedAt": 1759300000000,
      "tokensUsd": 5.31,
      "infraUsd": 0.81,
      "spentUsd": 6.12
    }
  }
}
```

**Daily rollover:** when the `daily.date` stored in a node is not today, the transaction treats `spentUsd` as 0 and writes today's date. This happens after the end-of-day history job (§10) has archived the previous day.

### 7.3 Reserve → Call → Reconcile

The cap is enforced **before** each call, not discovered afterwards. Each call reserves its worst-case cost first, then corrects the counters to the actual cost.

The actual cost is taken from the provider's usage data. OpenRouter returns a cost that includes its fee; for Anthropic, the cost is computed from the reported usage, including cache read and write tokens. If neither is available, the cost is computed from token counts and the pricing table, plus the route's `routingFeePct`.

```ts
import { getDatabase } from "firebase-admin/database";

const db = getDatabase();

function worstCaseCost(route: Route, model: string, inputTokens: number, maxOutput: number): number {
  const p = pricing[model];
  const base = (inputTokens / 1e6) * p.inputPerM + (maxOutput / 1e6) * p.outputPerM; // assumes no cache hit
  return base * (1 + routingFeePct[route] / 100);
}

// Atomically add `amount` to a daily counter if it stays within the limit.
async function tryReserve(path: string, amount: number, today: string): Promise<boolean> {
  const res = await db.ref(`${path}/daily`).transaction((d) => {
    if (d == null) return; // abort: missing config → fail closed
    const spent = d.date === today ? d.spentUsd : 0;
    if (spent + amount > d.limitUsd) return; // abort: over cap
    return { ...d, date: today, spentUsd: spent + amount };
  });
  return res.committed;
}

async function adjust(path: string, delta: number, today: string) {
  await db.ref(`${path}/daily`).transaction((d) => {
    if (d == null) return d;
    const spent = d.date === today ? d.spentUsd : 0;
    return { ...d, date: today, spentUsd: Math.max(0, spent + delta) };
  });
}

export async function guardedCall(
  project: string, runId: string, route: Route, model: string,
  inputTokens: number, maxOutput: number,
  call: () => Promise<{ inTok: number; outTok: number; costUsd?: number }>
) {
  const today = new Date().toISOString().slice(0, 10);
  const snap = await db.ref("budget").get();        // throws on network failure → fail closed
  const b = snap.val();
  if (!b || b.global.killSwitch || b.projects[project]?.killSwitch) throw new Halted("kill switch");

  const reserve = worstCaseCost(route, model, inputTokens, maxOutput);

  // Reserve on the project first, then global; roll back the project if global refuses.
  if (!(await tryReserve(`budget/projects/${project}`, reserve, today))) throw new Halted("project cap");
  if (!(await tryReserve(`budget/global`, reserve, today))) {
    await adjust(`budget/projects/${project}`, -reserve, today);
    throw new Halted("global cap");
  }

  let actual = 0;
  try {
    const usage = await call();
    if (usage.costUsd != null) {
      actual = usage.costUsd;                        // provider-reported (OpenRouter includes its fee)
    } else {
      const p = pricing[model];
      actual = ((usage.inTok / 1e6) * p.inputPerM + (usage.outTok / 1e6) * p.outputPerM)
               * (1 + routingFeePct[route] / 100);   // cached-token pricing applied when reported
    }
    return usage;
  } finally {
    const delta = actual - reserve;                  // usually negative: refund the unused reservation
    await adjust(`budget/projects/${project}`, delta, today);
    await adjust(`budget/global`, delta, today);
    await db.ref(`runs/${runId}/tokensUsd`).transaction((s) => (s ?? 0) + actual);
    await db.ref(`runs/${runId}/spentUsd`).transaction((s) => (s ?? 0) + actual);
  }
}
```

- **Per-run caps** are checked the same way, against `runs/<runId>/spentUsd` and `limitPerRunUsd`.
- **Kill switches are watched live.** Jobs hold a listener on both switches, which aborts `ctx.signal` (§4.1). Flipping a switch stops in-flight work at the next step rather than waiting for the next call.

### 7.4 Cloud Run Infra Cost (new)

Cloud Run bills compute in **vCPU-seconds and GiB-seconds**. That is roughly 10–20% of model cost, so it is tracked, counted against the caps, and kept as a separate field from token cost.

**Live accrual:**
- At startup, each job reads its own allocation: vCPU count and memory in GiB.
- A heartbeat runs every 60 seconds and charges the elapsed interval through the same reserve-and-reconcile flow as tokens:

```ts
const infraRatePerSec = vcpu * pricing.infra.vcpuSecondUsd + memGiB * pricing.infra.gibSecondUsd;

setInterval(async () => {
  const now = Date.now();
  const cost = ((now - lastTick) / 1000) * infraRatePerSec;
  lastTick = now;
  // Same nested caps as tokens: project, global, per-run.
  if (!(await tryReserve(`budget/projects/${project}`, cost, today()))) return abort("project cap (infra)");
  if (!(await tryReserve(`budget/global`, cost, today()))) {
    await adjust(`budget/projects/${project}`, -cost, today());
    return abort("global cap (infra)");
  }
  await db.ref(`runs/${runId}/infraUsd`).transaction((s) => (s ?? 0) + cost);
  await db.ref(`runs/${runId}/spentUsd`).transaction((s) => (s ?? 0) + cost);
  await db.ref(`agents/${taskId}/infraUsd`).transaction((s) => (s ?? 0) + cost);
}, 60_000);
```

**What this achieves:**
- A task that runs long without making progress (for example, stuck on a flaky test) pushes toward the cap through infra cost alone, even when it is making few model calls.
- On exit, a final partial tick is charged.

**Ground truth:**
- The live figure is an estimate.
- For reporting, the end-of-day job can reconcile against actual Cloud Run billing, using the billing export to BigQuery when it is enabled.
- Any difference is recorded as `infraAdjustmentUsd` in the daily record.

### 7.5 Security Rules (sketch)

- Jobs authenticate with a service account. Only jobs and the owner can write to `budget/*`, `runs/*` and `agents/*`.
- Only the owner can write `limitUsd`, `limitPerRunUsd` and `killSwitch`.

---

## 8. Agent Registry (RTDB)

Each job registers itself when it starts and deregisters when it stops. A job that crashes or is killed is removed automatically through `onDisconnect`, so no ghost agents remain on the dashboard.

```json
{
  "agents": {
    "t_20261001_0142": {
      "project": "project-a",
      "task": "Fix race condition in order sync worker",
      "status": "self_reviewing",
      "cheapRound": 2,
      "seniorRound": 0,
      "coderModel": "deepseek/deepseek-v4-flash",
      "cheapReviewerModel": "deepseek/deepseek-v4-flash",
      "seniorReviewerModel": "claude-sonnet-5-5",
      "harness": "deepseek-native",
      "prUrl": "https://github.com/org/project-a/pull/412",
      "startedAt": 1759300000000,
      "updatedAt": 1759300734000,
      "tokensUsd": 0.71,
      "infraUsd": 0.13,
      "spentUsd": 0.84
    }
  }
}
```

The possible `status` values are `provisioning`, `coding`, `building`, `self_reviewing`, `senior_reviewing`, `ready`, `failed` and `halted`.

Registration uses the client SDK inside the job, because `onDisconnect` requires a live client connection:

```ts
const ref = db.ref(`agents/${taskId}`);
await ref.onDisconnect().remove();   // register cleanup FIRST
await ref.set({ project, task, status: "provisioning", startedAt: Date.now(), updatedAt: Date.now() });

// On each phase change:
await ref.update({ status: "building", updatedAt: Date.now() });

// Normal exit:
await ref.remove();
```

As a secondary safety net, a sweep removes stale agents, for example any whose `updatedAt` is more than 10 minutes old.

---

## 9. Live Dashboard (`fugaro watch`)

The dashboard is a terminal UI subscribed to `budget`, `agents` and `runs`. It is only a view: pressing **Esc** detaches the listeners and exits, and the cloud jobs keep running.

### 9.1 Layout (grouped by project)

```
FUGARO ─ live                                              Esc to exit
GLOBAL   $41.27 / $150.00 today   ▓▓▓▓▓░░░░░░░░  27%   burn $0.82/min

▸ project-a   $18.90 / $60.00   ▓▓▓▓░░░░░░  32%   burn $0.31/min
    t_0142  Fix race in sync worker      self-review  c2/3      $0.84
    t_0143  Add pagination to /orders    senior       s1/2      $1.12
    t_0147  Refactor voucher service     coding                 $0.19

▸ project-b   $22.37 / $60.00   ▓▓▓▓░░░░░░  37%   burn $0.51/min   ⚠ fast
    t_0150  Migrate auth middleware      building               $0.77
    t_0151  Tests for billing module     coding                 $0.12

k  kill all    K  kill project    ↑↓ select    Esc exit
```

### 9.2 Behaviour

- **Layout.** The global total is shown at the top. Below it is one block per project, showing spend against cap, burn rate, and that project's agents with their live status.
- **All-in spend.** Every figure on the dashboard is all-in, meaning tokens plus infra. The global line also shows the split, for example `tokens $35.10 · infra $6.17`.
- **Round counters.** Each agent shows `cN/3` during the cheap loop and `sN/2` during the senior gate.
- **Burn rate.** This is the change in spend over a rolling window (for example, the last 5 minutes), computed in the client from counter updates. It is highlighted when it exceeds a threshold.
- **Stuck agents.** An agent that stays in one status beyond a threshold (for example, `building` for more than 10 minutes) is highlighted.
- **Kill switches.** Keybindings trigger the global and per-project kill switches, with a confirmation prompt.

---

## 10. Spend History & Reporting (Firestore)

RTDB holds the live state. History is kept in Firestore, which is better suited to querying and aggregation.

### 10.1 Daily Record (source of truth)

There is one document per project per day, in the collection `spendDaily`, with document ID `<project>_<YYYY-MM-DD>`:

```json
{
  "project": "project-a",
  "date": "2026-10-01",
  "spentUsd": 57.14,
  "tokensUsd": 49.82,
  "infraUsd": 7.32,
  "infraAdjustmentUsd": 0.00,
  "runs": 23,
  "tasksReady": 17,
  "tasksFailed": 4,
  "tasksHalted": 2,
  "byModel": {
    "deepseek/deepseek-v4-flash": 21.40,
    "claude-sonnet-5-5": 35.74
  },
  "byStage": {
    "coding": 14.10,
    "cheapReview": 7.30,
    "seniorReview": 35.74
  },
  "avgCheapRounds": 1.8,
  "avgSeniorReviews": 1.1,
  "cacheHitRatio": { "claude-sonnet-5-5": 0.82, "deepseek/deepseek-v4-flash": 0.64 },
  "routingFeesUsd": 1.18
}
```

- **`byStage` and the round averages** show directly whether the two-stage design is working, and they are the basis of the coder A/B comparison.
- **`routingFeesUsd`** covers OpenRouter traffic only. It shows how much the fee costs in practice, which feeds the decision on whether to go direct to a provider later.
- **`tokensUsd` and `infraUsd`** show the split between model cost and compute cost, so `spentUsd` is their sum.
- **`cacheHitRatio`** confirms that caching is actually working on each route.

### 10.2 Rollups

Weekly, monthly and yearly figures are **computed when read**, from the daily records, rather than maintained live. Only the finest grain is stored; everything coarser is derived from it, so the totals can never drift out of sync.

```ts
const docs = await firestore.collection("spendDaily")
  .where("project", "==", "project-a")
  .where("date", ">=", "2026-10-01")
  .where("date", "<=", "2026-10-31")
  .get();
const monthTotal = docs.docs.reduce((s, d) => s + d.data().spentUsd, 0);
```

### 10.3 End-of-Day Job

A small scheduled job (Cloud Scheduler triggering Cloud Run) runs at the end of each day:

1. Read each project's `budget/projects/<p>/daily` from RTDB.
2. Write the `spendDaily` document to Firestore. The write is idempotent because the document ID is deterministic.
   - When the Cloud Run billing export is available, reconcile infra cost against it and record any difference as `infraAdjustmentUsd`.
3. The RTDB daily counters then roll over on the next date (§7.2).

Throughout the day, jobs accumulate outcome counts, the `byModel` and `byStage` totals, and the round counts as they finish. These are then included in the daily document.

---

## 11. Open Items

- **Build and prove the two-stage loop** end to end on one real task (§0).
- **Model IDs and prices.** Pin the exact Anthropic and OpenRouter model IDs, and fill in token and Cloud Run prices (§2.3).
- **Cheap-loop exit behaviour.** Decide whether a task still rejected at cheap round 3 goes to the senior gate (the default here) or fails immediately (§3.2).
- **First coder harness.** Choose it; native DeepSeek is preferred for a DeepSeek coder (§4.2).
- **Validate each harness and route pairing,** including the cache-hit check (§5.4).
- **Coder A/B.** Compare DeepSeek V4 Flash, a Qwen coder-tier model and Kimi, measuring the cost to reach a senior-approved PR.
- **Provider allowlists.** Set them for each project (§5.5).
- **Cap values.** Set initial caps per project and globally, now including infra.
- **Alert thresholds.** Set the burn-rate threshold, the stuck-agent threshold, and the threshold for a cache-hit ratio that has collapsed.
- **Proposed: a wall-clock timeout per task,** as a backstop independent of spend.
- **Proposed: rebase before opening the PR,** to handle stale bases when several tasks merge to `main`. On a conflict, bounce back to the cheap loop.
- **Later:** decide whether to add inline PR comments alongside the summary comment.
- **Later:** decide whether to go direct to a provider once volume on one cheap model is steady (§5.3).
