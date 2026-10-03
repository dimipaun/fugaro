# Fugaro M10: multi-model runs (non-Anthropic backends)

*Status: 2026-10-03, design check and plan. Sources: [m9-budget-and-dashboard.md](m9-budget-and-dashboard.md) (gateway, §5, §11), [m9-spec-v3-reconciliation.md](m9-spec-v3-reconciliation.md) (ruling 3: multi-model is M10), the user's spec v3 ([m9-spec-source-v3.md](m9-spec-source-v3.md) §2, §4, §5, §7). Claims about OpenRouter, DeepSeek, Qwen and Kimi (endpoints, prices, caching, usage fields) are **from the spec or from memory and unverified**; every one is an item in §13 to check against a recorded real response before it is relied on. The plan is [2026-10-03-m10-multi-model.md](../plans/2026-10-03-m10-multi-model.md).*

## 0. Summary and recommendations

1. **Keep Claude Code as the harness; do not translate protocols.** Claude Code speaks the Anthropic Messages API. OpenRouter and DeepSeek both advertise Anthropic-compatible endpoints (to verify). The gateway already proxies `/v1/messages` and never rewrites bodies. M10 adds an **upstream kind** (base URL, credential, auth header) and a **per-model route table**, not a translator. A translating gateway (Anthropic to OpenAI chat) is a large, bug-prone surface for money-critical code; rejected unless no compatible endpoint exists for a wanted model (then it is a later, separate decision).
2. **Routing is by model ID, inside the existing gateway.** One gateway, several upstreams; the pinned model of the stage picks the route. Claude models keep going direct (cache markers: spec §5.2), everything else through the one configured compatible endpoint.
3. **The agent never holds a provider key.** Same as `api-key` today: the agent gets the per-run gateway token as its API key; the real key lives only in the runner (D2 residual unchanged, and no worse).
4. **The owner, not the repository, decides which providers may see code.** Providers and their keys are in the local config. A repository's `fugaro.yaml` can only narrow (`allowed_models`), never enable a provider. This is a data-egress decision (code goes to a third party), so it is a ceiling the owner sets.
5. **Money: one account, two kinds of price.** Prices come from the table (`model_prices`, plus an embedded starter set marked unverified); a provider-reported cost is recorded beside it but the table price is what reserves and caps, until the report is proven to match. Route fee (OpenRouter) is a per-provider percentage applied to the charge, never hidden.
6. **`oauth` stays Anthropic-only** (a subscription token cannot go anywhere else, and oauth is never proxied, D2). `vertex` stays Claude-on-Vertex. Non-Anthropic models require `agent.auth: api-key`, which is a validation rule, not a silent fallback.
7. **Review is two-tier when the coder is a provider model:** a first-line review run with the coder's model, whose findings go through a fix stage first, then a senior review on Claude whose verdict alone decides readiness (§11a, plan T12).
8. **First slice: OpenRouter, one non-Claude model (DeepSeek), coder role only, reviewer and background pinned to the same model or to Claude, tested entirely against a fake upstream.** Then a live check on the sandbox with a user-provided key, run by the user.

## 1. Goals and non-goals

Two-tier review (a first-line review by the coder model, then a senior review on Claude) is **in scope** as the last task, T12 (§11a); it is not a Phase 3 item.

Goals: a run whose coder (and optionally reviewer) is a non-Anthropic model; every existing guarantee (pinning, reservation and settlement, leases, kill switches, caps, `fugaro budget prices`, history, `report`) holds unchanged for those calls; no key in the agent's environment; a clear, honest cost account.

Non-goals (M10): other harnesses (Codex, Pi, DeepSeek-native; spec §4) and the harness plugin seam; wide/narrow/redundant modes and `difficulty` (spec §6), which are a separate orchestration feature; a cache-hit alert (later phase); direct-to-provider (non-OpenRouter) routes beyond what the same mechanism gives for free; a protocol translator; infra-cost reconciliation.

## 2. How Claude Code talks to a non-Anthropic backend

| Option | What it is | Verdict |
|---|---|---|
| A. Provider-compatible endpoint | Claude Code sends Anthropic Messages JSON to `ANTHROPIC_BASE_URL`; the provider (OpenRouter's Anthropic-compatible `/api/v1/messages`, DeepSeek's `/anthropic`) accepts it and answers in the Anthropic stream format | **Chosen.** Zero translation; the gateway already parses `message_start`/`message_delta` usage |
| B. Translate in the gateway | Anthropic to OpenAI chat and back (streaming, tool calls, thinking, cache markers) | Rejected for M10: new correctness surface, usage mapping and tool-call quirks, all in the money path |
| C. Third-party translator (LiteLLM, claude-code-router) in the container | Extra process | Rejected: another dependency holding the key, not under the gateway's accounting |

Mechanics (unchanged from §5.1 of the M9 design): the runner sets `ANTHROPIC_BASE_URL=http://127.0.0.1:<port>` and `ANTHROPIC_API_KEY=<gateway token>`; the managed settings file stops the repository overriding it. What changes is only the gateway's side: the upstream for the request's model is chosen from the route table, the gateway attaches that upstream's credential in the header the upstream wants (`x-api-key` for DeepSeek's Anthropic endpoint; `Authorization: Bearer` for OpenRouter; **to verify**), and drops the agent's own auth header (already so for Anthropic).

Things a foreign model changes for Claude Code, and the answers:

- **Model names.** Pins are exact upstream IDs (`deepseek/deepseek-v4-flash` on OpenRouter). The runner sets `--model` and the `ANTHROPIC_DEFAULT_{OPUS,SONNET,HAIKU}_MODEL`/`ANTHROPIC_SMALL_FAST_MODEL` pins to the pinned IDs so **no request names a model outside the stage's pins** (today's per-stage allow-list, D8). The background (Haiku-class) model must be pinned to a model on a route the run is allowed; with a non-Claude coder the default is the coder's own model (cheapest safe choice, one price), overridable.
- **Features that may not exist upstream:** `count_tokens` (the gateway answers a local estimate or a free 200 instead of forwarding, to verify), extended thinking, 1h cache TTL, `anthropic-beta` headers, web search/fetch server tools, image input. Rule: the gateway forwards headers unchanged as today; a feature the upstream rejects surfaces as the upstream's error, passed through. The runner turns off what is known to break per route (for example server tools, `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`, `DISABLE_PROMPT_CACHING` only if the route proves not to cache) through a per-route settings fragment. **To verify per model, recorded as fixtures.**
- **Tool-call quality.** Claude Code is tuned for Claude; foreign models make rougher tool calls (spec §4.2). Hence the quality bar in §12 and "experiment, not default" in the product framing: `agent.models` stays Claude by default.

## 3. Config surface

Principle: **who can set what** follows M9a.1 (owner ceiling, repository only tightens).

**Local config (owner only; the CLI's config, passed to jobs like `FUGARO_MODEL_PRICES`):**

```yaml
providers:
  openrouter:
    kind: anthropic-compat          # the only kind in M10
    base_url: https://openrouter.ai/api     # https required; http only on loopback (tests)
    auth: bearer                    # bearer | x-api-key
    secret: openrouter-api-key      # logical secret name (Secret Manager); never the value
    route_fee_pct: 5.5              # added to every charge on this provider; default 0
    models: ["deepseek/*", "qwen/*", "moonshotai/*"]   # IDs this provider serves; no overlap between providers
    allow_data_to: [edgeappinc/fugarosandbox, dimipaun/fugaro]      # Fugaro-project/repository slugs allowed to send code here; absent = none
model_prices:
  deepseek/deepseek-v4-flash: {input_per_m: 0.00, output_per_m: 0.00, cache_read: 0.1}   # owner fills real prices
```

(Key names follow the existing `model_prices` and policy spellings; the exact schema is T1's first test, written against `internal/config` conventions.)

**Repository `fugaro.yaml` (unchanged keys):** `agent.models.coder|reviewer|background`, `agent.model`, `budget.allowed_models`. A non-Anthropic ID there is accepted only when (a) a provider's `models` pattern claims it, (b) that provider's `allow_data_to` names this repository, (c) it passes `CheckPins` (explicit, priced, not an alias) and `CheckAllowed`. Otherwise the run refuses at bootstrap, before any call, naming the rule. A repository cannot add a provider, a base URL, a key or a price (`TestRepoCannotAddProvider`).

**`agent.auth`:** non-Anthropic pins require `api-key`; with `oauth` or `vertex` the validation message says why and names the alternatives. `vertex` + a provider-routed model is refused in M10 (mixed credentials); `api-key` + Claude pins + a provider-routed reviewer is allowed (phase 2).

**Tasks:** `fugaro run --model X` stays the task override of the coder pin; it passes through the same checks.

## 4. Secrets

- The provider key is a logical secret (Secret Manager, like `anthropic-api-key`), mounted into the **runner process only**, exactly as the Anthropic key is today. `ReservedSecrets` gains the provider secret names (`openrouter-api-key`), so a workflow's own secrets cannot shadow them.
- `fugaro init` and `fugaro secrets set` (existing flow) store it; the key is never in `fugaro.yaml`, in the local config file's values, in the job's plain env, in `result.json`, or in logs (the runner's redactor gets the value; the gateway logs only the route name and a key fingerprint of at most 4 characters).
- The key is mounted into every workflow of a repository the provider's `allow_data_to` names (case-insensitively), on `api-key` only, not only into workflows whose pins name the provider's models. Limitation: the job spec sees only the checked-in agent block, while a run's models come from the branch's `fugaro.yaml` and a task's `--model`, unknown at deploy time; gating on the visible pins would break a run that pins a provider model later. The key is the runner's only, and the repository was already allowed to send code to that provider.
- A key with whitespace, a control character or a non-ASCII character, or under 4 bytes, is refused at start (the error names the variable, never the value).
- The runner removes the key from the agent's environment, same code path as `ANTHROPIC_API_KEY` (`TestAgentEnvHasNoProviderKey`, extended to every provider secret).
- The agent has unrestricted egress and the key is in the runner's process memory: **the D2 residual is unchanged** (a compromised agent in the same container can read the runner's environment via /proc). Backstop for M10 is a **separate OpenRouter key per Fugaro project with a credit limit set at OpenRouter** (the analogue of A7's workspace spend limit), documented in the setup guide. The sidecar (A8) hardening remains later and now covers both keys.

## 5. Pricing and budget accounting

**What reserves and caps: the table.** The reservation formula (§5.3-5.4) is price-only and needs no change: `worstCase = bodyBytes x inputRate x cacheMult + maxTokens x outputRate`. The route fee multiplies it (`x (1 + fee/100)`) so a capped run can never overspend by the fee. A model with no table price cannot be pinned (existing `CheckPins`); an unknown *serving* model (OpenRouter's fallback to another provider's model) is charged at the route's highest listed rate for the pattern, flagged `priced_as: max` (existing mechanism).

**Where prices come from.** (1) Owner `model_prices` (always wins). (2) An embedded starter set in `internal/pricing` for the first-slice model, each row marked `unverified` with a source URL and check date, and **a warning at pin time while unverified**; it is not trusted to cap money until the owner confirms it. (3) `fugaro budget prices` (existing, shows table + overrides) gains a source column (`embedded|override`) and a verified/unverified flag. A **price check command is out of scope**: OpenRouter lists prices over an API, but fetching them from a running job is a new network dependency; a later nicety.

**Real vs notional dollars.** Current model: `api` runs = real dollars (price-table charge), `oauth` = notional. Non-Anthropic API runs are **real dollars**: the charge is what the gateway computes from usage and the table, plus the route fee. It is recorded per call as `charge` and, when the upstream reports one (OpenRouter's usage cost field, to verify), as `reported`. `result.json` cost gains `model_by` entries for the new IDs (existing field) and `route`; a persistent, large `reported` vs `charge` difference is surfaced in `diagnose` (and warns in the log), never auto-adopted. Why not trust `reported` immediately: the number is not on every response shape, it includes provider fallbacks we did not pin, and adopting it before an observed week would make the cap depend on an unverified field. Phase 3 can switch to `reported` as the settled charge where proven, keeping the table for the worst case.

**Cache accounting.** The Anthropic usage fields (`cache_creation_input_tokens`, `cache_read_input_tokens`) may be absent or zero on a compatible endpoint even when the provider caches (spec §5.4). Conservative default: when a route reports no cache fields, all input is charged at the full input rate (over-charges, never under). The route's `cache_read` multiplier applies only when the field appears. A fixture test pins both shapes.

**A call can settle above its reservation.** The reservation is the pinned model's worst case plus the fee. If the provider serves a dearer model than the pin (a fallback), the call is charged at the dearer of the two rates, so its settled amount can exceed what was reserved. The overshoot is counted after the call (`overrun`, as for a Claude call whose usage beats its estimate); it cannot be refused beforehand because the served model is unknown until the response.

**Settlement** (§5.5) is unchanged: complete stream = actual usage; error before `message_start` = 0; after = input plus reserved output; cancel and disconnect as today. Retry is the client's. One addition: a non-Anthropic upstream that streams **no usage at all** (a completed stream without `message_start` usage) is charged the full reservation and logged `settled: reserved`, so a silent upstream cannot be free.

**Leases, caps, halts, kill switches, history, `report --by model`:** unchanged; they see dollars and a model ID. `spendDaily`/`byModel` already key by model ID; `report` adds a `route` column from M10's per-call log field (phase 2).

## 6. Pinning and per-model allowlists

- Per-stage pin set: unchanged `{role model, background model}`; the request's `model` must equal a pin or the call gets the existing `400 fugaro: model X is not pinned for stage S` and the stage fails as a configuration error. The comparison is exact on the upstream ID; the `sameModel` alias logic is Claude-specific and is **not** extended to provider IDs (no `:free`, `:nitro`, `:online` suffix aliasing: OpenRouter variant suffixes change routing and price; a pin carrying one is refused at bootstrap: `TestPinWithVariantSuffixRefused`).
- `allowed_models` (policy) already applies to explicit IDs; it now also governs provider IDs. Pattern entries are not added (explicit IDs only, matching D8).
- **OpenRouter provider routing:** the gateway cannot rewrite bodies (D8), so provider preferences (`provider.order`, `allow_fallbacks: false`, `data_collection: deny`, ZDR) are set **account-side** in OpenRouter's settings, which the setup guide states as a prerequisite, or through headers if a documented header form exists (to verify). Spec §5.5's per-provider allowlist is therefore enforced at two levels: Fugaro's `allow_data_to` (which model IDs a repository may use) and the OpenRouter account's own provider policy (who serves them). A response whose reported serving provider is unexpected is logged and flagged; it is not blocked in M10.

## 7. Auth modes

| `agent.auth` | Anthropic models | Provider models (M10) |
|---|---|---|
| `api-key` | direct, gateway | via gateway, provider key |
| `vertex` | Vertex, gateway | **refused** (M10), message explains |
| `oauth` | unproxied, uncapped (unchanged) | **never** (the token is a subscription credential; a run cannot mix) |

`oauth` runs with a provider pin fail validation at config time, not at the first call.

## 8. Failure modes

| Failure | Behaviour |
|---|---|
| Provider key missing, rejected (401/403) | Upstream error passed through; first occurrence logs `provider auth failed (route R)` once; the stage fails as configuration, not budget; no retry loop (Claude Code's retries each reserve and settle at 0 before `message_start`) |
| Rate limit (429) / provider outage | Passed through with `retry-after`; charged 0 (A3). Claude Code retries; a sustained outage ends in the stage's own timeout, a draft PR as today |
| Upstream silently serves another model (fallback) | `priced_as: max` for the route pattern, flagged; account-side `allow_fallbacks: false` is the real fix |
| Usage missing from a completed stream | Charge the reservation (§5) and flag |
| Provider-reported cost differs from the table | Logged and shown in `diagnose`; the table stays the charge (§5) |
| Provider rejects a Claude Code feature (thinking, beta header, tool shape) | Error passed through; the stage fails with the upstream message; per-route settings fragment is where a known incompatibility is switched off |
| Stream shape differs (no `ping`, no `message_start` fields) | The existing parser tolerates absent optional fields; unknown event types are skipped; a stream with no recognised events is a protocol error charged as "after start" (reserved output) |
| Gateway upstream URL not https | Refused at start (existing `checkBaseURL`; http only on loopback, for the fake) |
| Data goes where it must not | `allow_data_to` check at bootstrap; no call is made |
| Price missing/unverified | Missing refuses the pin; unverified warns at pin time and in `diagnose` |

## 9. Security

| Threat | Answer |
|---|---|
| Key in the agent's environment | Removed (existing path, tested for every provider secret); gateway attaches it |
| Repository points Claude Code at another host | Managed settings (A6) keep the gateway URL; a repository cannot add providers or base URLs |
| Repository sends code to a provider the owner did not approve | `allow_data_to` ceiling; refused at bootstrap |
| SSRF through `base_url` | Owner-set only; https required; no redirects followed to another host (client `CheckRedirect` refuses cross-host) |
| Header injection / key leak to the wrong upstream | The credential is attached per route; request headers from the agent (`x-api-key`, `authorization`) are dropped, so the gateway token never reaches an upstream and a key never reaches the agent |
| Key in logs, errors, `result.json` | Redactor gets the value at start; upstream error bodies are passed to the agent as today but scanned for the key before logging (`TestUpstreamErrorEchoingKeyIsRedacted`) |
| Cost bypass | Same D2 residual as today (runner holds the key; mitigation: provider-side credit limit per Fugaro project) |
| Response-borne prompt injection / exotic stream | The gateway does not interpret content; usage parsing only |
| Another run's lease/token confusion | Unchanged; per-run gateway token, loopback only |

## 10. Testing without real spend

- **`internal/gateway/anthropicfake` already exists** (a fake Anthropic upstream). M10 adds a **configurable compat-fake**: it speaks the Messages API with a chosen auth header, can omit cache fields, omit usage, add a provider cost field, rename the serving model, return 401/429/5xx and cut the stream. Fixtures under `testdata/` are recorded **by hand from public docs, then replaced by recordings the user makes** from a real call (T11, the user's key, never ours).
- Gateway tests: route selection by model, per-upstream credential and dropped agent auth, fee applied to reservation and settlement, missing-usage charge, unverified-price warning, variant-suffix refusal, no cross-host redirect, key redaction.
- Config tests: provider block decode (strict), overlap rejection, repository cannot add a provider, `allow_data_to`, `oauth`/`vertex` refusal, pin checks for provider IDs.
- Runner/end-to-end with the fake `claude` and the compat-fake: a run whose coder is `deepseek/...` ends in a PR, `result.json` cost correct to the micro-dollar, agent env has no key, kill switch halts a provider stream.
- **No cloud calls and no real key in CI.** The single live check is user-run (T11).

## 11. Quality bar

The user's spec says foreign models are "experiments, not defaults" and that the A/B measure is "cost to reach a senior-approved PR". M10 provides the measurement hooks (per-model cost in `report`, outcome and review counts per run already exist) and no automatic quality gate. Proposed default (user to confirm): a non-Anthropic coder run is **always draft-or-ready by the same §4.2 rule** (tests verified plus review), with the **senior reviewer on Claude** (the first-line review by the coder model comes before it, §11a); no run is ever "more ready" because it is cheaper.

## 11a. Two-tier review (T12)

Today (v1 §4) the stage machine is `implement, review(1), [fix, review(2)] ... finalize`; the review role takes `agent.models.reviewer`, the coder role (implement and fix) takes `agent.models.coder`, and `agent.review_rounds` bounds review/fix cycles. A cheap coder makes a cheap first look worthwhile: let the same model clean up what it can see before Claude is paid to read the diff.

**Shape.** `implement ─► review_first(1..k) ─► fix ─► ... ─► review(1) ─► [fix ─► review(2)] ... ─► finalize`.

- **Stage `review_first`** is a review in a fresh session with the same review prompt and verdict schema as `review`, but its role is **coder** (`StageRole("review_first")` returns `RoleCoder`), so it runs on `agent.models.coder`, uses the coder's pins and `max_output_tokens.coder`, and costs coder rates. It is a new stage name in the stage machine, in `result.json` (`reviews[].tier: "first"|"senior"`) and in transcripts (`transcripts/review_first-<n>.jsonl`).
- **Config (repository `fugaro.yaml`, never adds a provider or data path, so needs no owner approval):** `agent.first_line_review: auto | on | off` (default `auto`) and `agent.first_line_rounds: 1` (default 1, max 3; counted apart from `review_rounds`). `auto` means on exactly when the coder's model is routed to a provider (not Anthropic) and the reviewer's is not; an Anthropic coder gets no first line (same family, little to gain, the cost is real). `on` forces it with any coder; `off` disables it. A branch's `fugaro.yaml` can set `on` or raise the rounds, so it never widens providers or data, though it can raise spend: it can raise spend, but always under the budget caps.
- **How verdicts combine.** The first-line verdict never decides readiness. Its `changes` findings go to a `fix` stage (the coder, resuming S1, as today) and the first line runs again, up to `first_line_rounds`; its `ship`, or its rounds running out, hands over to the senior `review(1)`, which is **always run** and whose verdict is the only one the readiness rule (v1 §4.1 finalize, "last review verdict was ship") reads. Senior `changes` go to `fix` and `review(n+1)` as today, bounded by `review_rounds`. First-line findings are not shown to the senior reviewer (a fresh, uncontaminated read), but their count and the fixes made are in `result.json`.
- **A failed first line is not a failed run.** An unparseable or errored first-line stage is recorded (`verdict: "none"`, `findings: 0`) with a log line saying why, skipped, and the senior review runs; a provider outage in a first-line stage must not strand a run that Claude could finish. A budget halt still ends the run as today.
- **Budget.** Both tiers draw on the one run cap, the daily caps and the stage `max_budget_usd`; the first line is priced as the provider model it runs on, so its cost is small, and `report --by model` shows it under the coder's ID. The only added exposure is up to `first_line_rounds` extra coder stages plus the fixes they trigger, each reserved and settled by the gateway like any stage. The token cap (`max_run_tokens`) counts these stages too. Not charged separately: the senior review runs whether or not the first line did, so the first line can only add cost, never remove the senior's.
- **Recommendation: default `auto`** (on only for a non-Anthropic coder). Reasons: it is where the spec's measure ("cost to reach a senior-approved PR") has something to win, since a provider model is cheap enough that an extra pass costs cents and may save a Claude fix round; it changes nothing for existing Claude-only runs (no cost, no behavior change, no new surprise); and it keeps one decision for the owner (pick a provider coder) instead of two. Not default-on for everyone: for a Claude coder the first line adds a full review at senior prices to save a fix round that the senior review already triggers. Phase 3's orchestration modes build on this stage; they are not part of it.

## 12. Phases

**Phase 1 (the first slice, shippable alone):** `providers` config and validation; gateway upstream kind `anthropic-compat` with the route table, credential attach, fee, missing-usage rule; one provider (OpenRouter), one model (DeepSeek V4 Flash) with owner-confirmed prices; secrets and env hygiene; `fugaro budget prices` source/verified column; diagnose shows route and `reported` vs `charge`; compat-fake and tests; docs and the live checklist item. Coder, reviewer and background may all be the provider model or Claude (the route table already routes per request model, so a mixed run costs nothing extra).

**Phase 2:** more models on the same provider (Qwen, Kimi), per-route settings fragments for proven incompatibilities, `report --by route`, cache-hit ratio per call and a collapse alert, a second provider kind only if one is needed (DeepSeek direct is the same `anthropic-compat` with a different block).

**Phase 3 (needs the user's decision):** adopt provider-reported cost as the settled charge; orchestration modes (wide/narrow/redundant, `difficulty`); other harnesses. (The two-tier review is T12 in phase 2's tail, §11a.)

## 13. Assumptions to verify (before or during the first slice; none blocks the plan)

1. OpenRouter's Anthropic-compatible endpoint path, auth header, streaming shape, `usage` fields (cache fields, cost), and `count_tokens` support.
2. DeepSeek's Anthropic-compatible endpoint (same questions); only if chosen as a direct route.
3. Which Claude Code features break against a compat endpoint (beta headers, thinking, tool shapes, background model use) with a recorded run, not a guess.
4. Real prices for the first-slice model, and the fee that applies to the user's OpenRouter account.
5. Whether provider-preference controls can be set through headers (otherwise account-side only).

## 14. Decided (the user's rulings, 2026-10-03)

1. **Provider and model:** OpenRouter, and `deepseek/deepseek-v4-flash` as the first model (coder). The first slice never sends code to anything else.
2. **Data policy:** `allow_data_to` lists `edgeappinc/fugarosandbox` and `dimipaun/fugaro`. EdgeWeb (`edgeappinc/edgeweb`) is **not** listed and so never sends code to a provider model.
3. **Reviewer:** two-tier (§11a): a first-line review by the coder model, then the senior review on Claude, which alone decides readiness.
4. **Prices and fee:** the owner supplies real prices and the account's route fee (we do not guess them); the embedded starter row stays `unverified`.
5. **Live check:** a user-run sandbox-only run with a user-created, credit-limited OpenRouter key (we never see it).
