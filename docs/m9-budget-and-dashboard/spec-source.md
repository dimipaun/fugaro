# Fugaro — Spec: Task Loop, Budget Guardrails, Live Dashboard & Spend History

**Repo:** `dimipaun/fugaro` (MIT) · **Runtime:** GCP Cloud Run jobs · **State:** Firebase RTDB (live) + Firestore (history)

---

## 1. Goals

- Run many coding-agent tasks in parallel in the cloud, off the laptop.
- Each task ends as either a **mergeable PR** or a **clearly labelled failure**. No babysitting, no respins.
- Spend is **bounded by hard caps** (per project and global) that cannot be exceeded, with a fleet-wide kill switch.
- Spend and agent activity are **visible live**, grouped by project, and **recorded historically** for reporting.

**Core cost principle:** fan out cheap, funnel expensive. Cheap models do the bulk of token volume; deterministic checks filter for free; the premium reviewer only sees clean candidates, with lean context (diff + task, never the whole repo).

---

## 2. Task Configuration

Every task is dispatched with two independently set models.

```json
{
  "taskId": "t_20260929_0142",
  "project": "project-a",
  "repo": "github.com/org/project-a",
  "baseBranch": "main",
  "instructions": "Fix the race condition in the order sync worker.",
  "coder": {
    "provider": "deepseek",
    "model": "<cheap-coder-model>",
    "maxTokensPerCall": 8000
  },
  "reviewer": {
    "provider": "anthropic",
    "model": "claude-sonnet-5-5",
    "maxTokensPerCall": 4000
  },
  "maxReviewRounds": 3,
  "mode": "single"
}
```

Model IDs must be **pinned explicitly**. Never rely on a provider or tool default, which can silently change to a more expensive tier.

Model prices live in a config table (per 1M tokens, input and output) used for cost estimation and accounting:

```json
{
  "pricing": {
    "claude-sonnet-5-5":   { "inputPerM": 0.0, "outputPerM": 0.0 },
    "<cheap-coder-model>": { "inputPerM": 0.0, "outputPerM": 0.0 }
  }
}
```

---

## 3. Task Lifecycle (one Cloud Run job = one full run)

The entire coder/reviewer loop runs **inside a single task** to avoid the setup and cold-start cost of respinning.

```
PROVISIONING
  → clone repo, create branch, open PR as DRAFT
CODING
  → coder model implements the task
COMPILING → TESTING → LINTING        (deterministic, zero model tokens)
  → on failure: back to CODING with the error output (does NOT count as a review round)
REVIEWING
  → reviewer model gets: task instructions + diff only
  → posts ONE structured findings comment on the PR
  → verdict: APPROVE | REJECT
     APPROVE → READY
     REJECT and round < 3 → back to CODING with findings as next instruction
     REJECT and round = 3 → FAILED
READY   → PR flipped from draft to ready for review/merge
FAILED  → PR stays DRAFT, final comment lists unresolved issues
HALTED  → budget cap or kill switch hit; PR stays DRAFT with a "halted" comment
```

### 3.1 Rules

- The reviewer **never sees code that doesn't compile or pass tests**.
- Maximum **3 review rounds**. Compile/test retries are separate and should have their own cap (e.g. 5) to prevent a coder stuck in a build-fail loop from burning tokens.
- PR stays **draft for the whole run**. Draft → ready is the only "human, look at this" signal.
- A run never loops forever and never flips to ready unless the reviewer approved.

### 3.2 Review Comment Format

One structured comment per round, machine-parseable so the coder can consume it:

```markdown
## Fugaro Review — Round 2 of 3
**Verdict:** REJECT
**Reviewer:** claude-sonnet-5-5

### Blocking
1. `src/sync/worker.ts:88` — lock released before the write completes; race still possible.
2. No test covers concurrent sync of the same order.

### Non-blocking
- Rename `tmpRes` to something descriptive.

<!-- fugaro:findings {"verdict":"REJECT","blocking":2,"nonBlocking":1} -->
```

Final failure comment (round 3 rejected):

```markdown
## Fugaro — Gave up after 3 review rounds
PR left as draft. Unresolved blocking issues:
1. ...
```

---

## 4. Parallelism Modes

| Mode | When | Behaviour |
|---|---|---|
| **Wide** | Independent work (unrelated bugs, isolated test files, boilerplate across modules) | High concurrency |
| **Narrow** | Coupled work (shared files, dependent changes) | Low concurrency limit (default 3–4) |
| **Redundant** | A single hard task | N cheap coder attempts on the same task; best passing candidate goes to review |

Concurrency limit is set **per batch**. Parallelism should match task independence, not available capacity.

---

## 5. Budget Guardrails (Firebase RTDB)

### 5.1 Model

- **Nested caps:** a per-project cap and a global cap across all projects. Every model call must pass **both**.
- **Kill switches:** one global (halts the whole fleet) and one per project (halts one project only).
- **Periods:** each cap has a per-run limit and a per-day limit, so a sane run repeated many times still gets stopped.
- **Fail closed:** if a job can't reach the budget node, it does not spend.
- **Atomic updates:** all counter changes use RTDB transactions, so concurrent jobs can't both read "90 spent" and both proceed.

### 5.2 Node Structure

```json
{
  "budget": {
    "global": {
      "killSwitch": false,
      "daily":  { "date": "2026-09-29", "spentUsd": 41.27, "limitUsd": 150.00 },
      "limitPerRunUsd": 100.00
    },
    "projects": {
      "project-a": {
        "killSwitch": false,
        "daily": { "date": "2026-09-29", "spentUsd": 18.90, "limitUsd": 60.00 },
        "limitPerRunUsd": 40.00
      },
      "project-b": {
        "killSwitch": false,
        "daily": { "date": "2026-09-29", "spentUsd": 22.37, "limitUsd": 60.00 },
        "limitPerRunUsd": 40.00
      }
    }
  },
  "runs": {
    "r_20260929_01": {
      "project": "project-a",
      "startedAt": 1759140000000,
      "spentUsd": 6.12
    }
  }
}
```

The `daily` block resets when `date` rolls over (handled inside the transaction: if `date` ≠ today, treat `spentUsd` as 0 and set the new date — after the end-of-day history job in §8 has archived it).

### 5.3 Reserve → Call → Reconcile

The cap must be enforced **before** a call, not discovered after. Each call reserves its worst-case cost, then corrects to actual.

```ts
import { getDatabase } from "firebase-admin/database";

const db = getDatabase();

function worstCaseCost(model: string, inputTokens: number, maxOutput: number): number {
  const p = pricing[model];
  return (inputTokens / 1e6) * p.inputPerM + (maxOutput / 1e6) * p.outputPerM;
}

// Atomically add `amount` to a daily counter if it stays within limit.
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

export async function guardedCall(project: string, runId: string, model: string,
                                  inputTokens: number, maxOutput: number,
                                  call: () => Promise<{ inTok: number; outTok: number }>) {
  const today = new Date().toISOString().slice(0, 10);
  const snap = await db.ref("budget").get();          // throws on network failure → fail closed
  const b = snap.val();
  if (!b || b.global.killSwitch || b.projects[project]?.killSwitch) throw new Halted("kill switch");

  const reserve = worstCaseCost(model, inputTokens, maxOutput);

  // Project first, then global; roll back project if global refuses.
  if (!(await tryReserve(`budget/projects/${project}`, reserve, today))) throw new Halted("project cap");
  if (!(await tryReserve(`budget/global`, reserve, today))) {
    await adjust(`budget/projects/${project}`, -reserve, today);
    throw new Halted("global cap");
  }

  let actual = 0;
  try {
    const usage = await call();
    const p = pricing[model];
    actual = (usage.inTok / 1e6) * p.inputPerM + (usage.outTok / 1e6) * p.outputPerM;
    return usage;
  } finally {
    const delta = actual - reserve;               // usually negative: refund unused reservation
    await adjust(`budget/projects/${project}`, delta, today);
    await adjust(`budget/global`, delta, today);
    await db.ref(`runs/${runId}/spentUsd`).transaction((s) => (s ?? 0) + actual);
  }
}
```

Per-run caps are checked the same way against `runs/<runId>/spentUsd` and `limitPerRunUsd`.

Jobs also hold a **live listener** on both kill switches, so flipping one aborts in-flight work at the next step rather than waiting for the next call.

### 5.4 Security Rules (sketch)

- Jobs authenticate with a service account; only jobs and the owner can write `budget/*`.
- Only the owner can write `limitUsd`, `limitPerRunUsd`, and `killSwitch`.

---

## 6. Agent Registry (RTDB)

Each job registers on start and deregisters on stop. Crashed or killed jobs are removed automatically via `onDisconnect`, so no ghost agents.

```json
{
  "agents": {
    "t_20260929_0142": {
      "project": "project-a",
      "task": "Fix race condition in order sync worker",
      "status": "reviewing",
      "round": 2,
      "coderModel": "<cheap-coder-model>",
      "reviewerModel": "claude-sonnet-5-5",
      "prUrl": "https://github.com/org/project-a/pull/412",
      "startedAt": 1759140000000,
      "updatedAt": 1759140734000,
      "spentUsd": 1.84
    }
  }
}
```

`status` ∈ `provisioning | coding | compiling | testing | linting | reviewing | ready | failed | halted`

Registration uses the client SDK in the job (onDisconnect requires a live client connection):

```ts
const ref = db.ref(`agents/${taskId}`);
await ref.onDisconnect().remove();   // register cleanup FIRST
await ref.set({ project, task, status: "provisioning", startedAt: Date.now(), updatedAt: Date.now() });

// On each phase change:
await ref.update({ status: "testing", updatedAt: Date.now() });

// Normal exit:
await ref.remove();
```

A stale-agent sweep (e.g. `updatedAt` older than 10 minutes) is a cheap secondary safety net.

---

## 7. Live Dashboard (`fugaro watch`)

A terminal UI subscribed to `budget`, `agents`, and `runs`. Purely a window: pressing **Esc** detaches listeners and exits; cloud jobs keep running.

### 7.1 Layout (grouped by project)

```
FUGARO ─ live                                          Esc to exit
GLOBAL   $41.27 / $150.00 today   ▓▓▓▓▓░░░░░░░░  27%   burn $0.82/min

▸ project-a   $18.90 / $60.00   ▓▓▓▓░░░░░░  32%   burn $0.31/min
    t_0142  Fix race in sync worker       reviewing  r2/3   $1.84
    t_0143  Add pagination to /orders     testing           $0.42
    t_0147  Refactor voucher service      coding            $0.19

▸ project-b   $22.37 / $60.00   ▓▓▓▓░░░░░░  37%   burn $0.51/min   ⚠ fast
    t_0150  Migrate auth middleware       compiling         $0.77
    t_0151  Tests for billing module      coding            $0.12

k  kill all    K  kill project    ↑↓ select    Esc exit
```

### 7.2 Behaviour

- Global total at top, then one block per project: spend vs cap, burn rate, and that project's agents with live status.
- **Burn rate** = spend delta over a rolling window (e.g. last 5 minutes) computed client-side from counter updates. Highlight when it exceeds a threshold.
- Highlight agents stuck in one status beyond a threshold (e.g. compiling > 10 min).
- Keybindings for the global and per-project kill switches, with a confirm prompt.

---

## 8. Spend History & Reporting (Firestore)

RTDB stays for live state; history lives in Firestore, which suits querying and aggregation.

### 8.1 Daily Record (source of truth)

One document per project per day. Collection `spendDaily`, doc ID `<project>_<YYYY-MM-DD>`:

```json
{
  "project": "project-a",
  "date": "2026-09-29",
  "spentUsd": 57.14,
  "runs": 23,
  "tasksReady": 17,
  "tasksFailed": 4,
  "tasksHalted": 2,
  "byModel": {
    "<cheap-coder-model>": 21.40,
    "claude-sonnet-5-5": 35.74
  }
}
```

Recording `byModel` shows the coder/reviewer cost split, which directly tests whether the fan-out-cheap design is working.

### 8.2 Rollups

Weekly, monthly, and yearly figures are **computed on read** from daily records, not maintained live. Store the finest grain once; everything coarser is derived and can never drift out of sync.

```ts
const docs = await firestore.collection("spendDaily")
  .where("project", "==", "project-a")
  .where("date", ">=", "2026-09-01")
  .where("date", "<=", "2026-09-30")
  .get();
const monthTotal = docs.docs.reduce((s, d) => s + d.data().spentUsd, 0);
```

### 8.3 End-of-Day Job

A small scheduled job (Cloud Scheduler → Cloud Run) at day end:

1. Read each project's `budget/projects/<p>/daily` from RTDB.
2. Write the `spendDaily` document to Firestore (idempotent via the deterministic doc ID).
3. The RTDB daily counters roll over naturally on the next date (§5.2).

Task outcome counts and `byModel` totals are accumulated per day by jobs as they finish, then included in the daily document.

---

## 9. Open Items

- Choose and pin the default coder model after a short A/B across cheap providers.
- Set initial cap values per project and globally.
- Compile/test retry cap value (proposed: 5).
- Burn-rate alert threshold and stuck-agent threshold.
- Whether to add optional inline PR comments later, on top of the summary comment.
