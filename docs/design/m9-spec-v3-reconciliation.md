# M9: reconciling spec v3 with the design

*Status: 2026-10-01. Source: [m9-spec-source-v3.md](m9-spec-source-v3.md) (verbatim, the user's v3; v2 is [m9-spec-source.md](m9-spec-source.md)). Design: [m9-budget-and-dashboard.md](m9-budget-and-dashboard.md), decisions D1-D18. Claims about OpenRouter, DeepSeek, Qwen, Kimi, their prices and caching are **from the spec, unverified**.*

## Rulings (binding)

| # | Ruling | Effect on the design |
|---|---|---|
| 1 | Each Fugaro project keeps its own Firebase project | D3 stays (revised 2026-10-04: the Firebase project may be the installation's own GCP project; still one per Fugaro project). v3's single RTDB with a global cap across projects is not adopted. A "global" cap is the Fugaro project's own total. Org-wide aggregation comes later and read-only |
| 2 | Caps count model dollars only | D7 stays. v3 §7.4 (a 60 s heartbeat charging compute into the caps) is not adopted. Compute is reported separately as `computeUsd` and shown in `watch` and history |
| 3 | Multi-model work is deferred to **M10**, with its own design | See below |
| 4 | D15 revised: draft PR after the first push | New milestone **M9e** (below) |
| 5 | The registry stays heartbeats plus a sweeper | D12 stays. No `onDisconnect` |
| 6 | Stay in Go | A harness interface and the guard are Go if M10 needs them. The gateway already enforces by proxy, so harnesses need no guard calls |
| 7 | `oauth` stays uncapped for dollars | D2/A1 stay. It is still subject to kill switches, the token cap and the D14 backend-outage grace halt |

## What v3 adds and where it lands

| v3 | Where |
|---|---|
| Per-slot models and pinned IDs (§2.1) | Already M9a (D8, §2.1, §5.2), Claude models only (D10) |
| Pricing table, cache read/write prices (§2.3) | Already M9a (§5.2 embedded prices, owner overrides). The OpenRouter `routingFeePct` is M10 |
| Reserve, call, reconcile; nested caps; fail closed; kill switches; per-run and daily caps (§7.1-7.3) | Already M9a/M9b, by gateway proxy and per-run tokens instead of an in-process guard. Fail closed is D14 |
| Per-run and per-project caps | Already M9a (per-run) and M9b (daily, kill switches) |
| Agent registry (§8) | M9b, heartbeats and a sweeper (ruling 5). Its cost split is `computeUsd` plus model dollars |
| Live dashboard (§9) | M9c. The all-in figure becomes model dollars with `computeUsd` beside it, not summed into the caps |
| Spend history (§10) | M9d. `spendDaily` gets `tokensUsd` and `computeUsd`; `infraAdjustmentUsd` reconciliation against the billing export is optional and later |
| Two-stage loop with a cheap loop first (§3) | Not adopted as such. M9 has the coder, review rounds and fix rounds; the cheap/senior split is M10 |
| Draft PR from the start, flip to ready (§3.1) | M9e |
| Structured findings per round with a marker (§3.3) | Format in the final report (M9f), not per-comment (see below) |
| Secrets in Secret Manager (§5.6) | Already so in the installation |

## M10 (deferred, own design)

Everything in v3 that needs a non-Claude model or a second route: the OpenRouter route; DeepSeek, Qwen and Kimi harnesses; the harness plugin interface (§4); the cheap and senior two-stage loop (§3); wide, narrow and redundant modes (§6); `difficulty`; cache-hit alerts (§5.4); per-project provider allowlists (§5.5); per-slot `route`. M9 does not block these: the price table and per-stage models are keyed by model ID already.

New gateway work M10 implies:

- An **OpenAI-compatible endpoint** next to the Anthropic Messages one, with its own request, stream and usage parsing (`prompt_tokens_details.cached_tokens`, per the spec, unverified).
- Using a provider-reported **cost field** (the spec says OpenRouter returns a cost that includes its fee; unverified) as the actual cost, falling back to the price table plus a routing fee.
- Per-route keys and base URLs per stage, and an allowlist check at pin time (D8 extended).
- A cache-hit ratio per call and an alert when it collapses.

## M9e: draft PR at the first push (D15, revised)

Small milestone. Behaviour:

- After the first implement stage passes verification and the branch is pushed, open a **draft** PR.
- Update its description at each stage boundary. Flip to ready at the end.
- A halt before the branch exists opens no PR (D9). A halt after the first push leaves the draft with a `halted` comment.
- The history job's sweeper marks the stale drafts of crashed runs.
- **Live test:** confirm that a Bitbucket draft does not notify the assigned reviewer.

**Rulings and the task plan:** [v1.md §4.2a](v1.md) and [the M9e plan](../plans/2026-10-03-m9e-early-draft-pr.md). Draft means no reviewers and no labels until the PR is ready (reviewers on a draft could notify a real reviewer early). Rebase-before-opening stays out of M9e. The sweeper (history job) has no git credentials, so it does not edit PRs: it marks the run `crashed`, and the draft's own status line (with its update time) plus `fugaro diagnose` show the stale draft.

Code areas: `internal/runner` (finalize, and `EnsurePR` by PR number so later stages update the existing PR; `followup.go` for follow-ups) and the `internal/gitprov` adapters (`github`, `bitbucket`, `fake`: draft creation, draft-to-ready, description update, comment). The old M9e (verify gate and structured findings) is now **M9f**.

## v3 items not adopted, or done differently

- **In-process `SpendGuard` in the harness (TS).** Enforcement is the gateway proxy (D2, D11); code stays Go.
- **A single RTDB with a global cap across projects.** Each project has its own Firebase project (D3).
- **Infra (Cloud Run) cost inside the caps and a 60 s accrual heartbeat.** Compute is reported, never capped (D7).
- **`onDisconnect` registry cleanup.** Not available over REST; heartbeats plus a sweeper (D12).
- **A comment per review with a hidden `<!-- fugaro:findings -->` marker.** Bitbucket hides HTML comments, so a marker is not a reliable carrier there. The findings go in the final report and `result.json`; no per-round PR comments.
- **An API-key-only assumption.** `oauth` is supported (A1): no dollar cap, but kill switches, the token cap and the D14 grace halt apply.
- **Client-side budget code with a service-account RTDB.** Per-run Firebase tokens and database rules (D1).

## v3 open items worth tracking

- **Wall-clock timeout per task**, as a backstop independent of spend (the spec proposes it). Not in M9; candidate for M9a/M9b since the run cap does not bound a stuck, cheap run.
- **Rebase before opening the PR**, with a bounce back on conflict (proposed in the spec). Fits M9e, where the PR now opens earlier.
- **Cap values**: initial per-run, daily and kill thresholds per project; set by observing in `observe` mode.
- **Alert thresholds**: burn rate, stuck agent (status unchanged beyond a limit), cache-hit collapse (M10).
- **Cheap-loop exit at round 3** (go on to the senior or fail): moot until M10.
- **Model IDs and prices**: pin and verify (A11).
- **Coder A/B, provider allowlists, going direct to a provider**: M10.
