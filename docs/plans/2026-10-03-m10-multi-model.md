# M10: Multi-Model Runs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax. Briefs to implementers stay short: the task text plus the Review Focus line that applies.

**Goal:** A run's coder (and optionally reviewer and background) can be a non-Anthropic model reached through an Anthropic-compatible endpoint (OpenRouter first), through the existing gateway: pinned, reserved, settled, capped and killable like any Claude call, with no provider key in the agent's environment and no spend in any test.

**Spec:** [m10-multi-model.md](../design/m10-multi-model.md), [m9-budget-and-dashboard.md](../design/m9-budget-and-dashboard.md) §5, §11, [m9-spec-v3-reconciliation.md](../design/m9-spec-v3-reconciliation.md) (M10).

## What is built and what this reuses

| Need | Already there |
|---|---|
| Gateway proxy, pins, reservation, settlement | `internal/gateway` (`Options`, `Upstream{Kind: anthropic|vertex}`, `handleMessages`, `forward`, `fromUsage`, `usageTee`); bodies never rewritten |
| Upstream URL safety | `checkBaseURL` (https, loopback http), `upstreamClient` |
| Pins and allow-list rules | `config.CheckPins`, `CheckAllowed`, `policy` ceiling, `pricing.IsAlias` |
| Prices | `internal/pricing` (`Table`, `Embedded`, owner `FUGARO_MODEL_PRICES` overrides), `fugaro budget prices` |
| Fake upstream | `internal/gateway/anthropicfake` |
| Secrets and env | `config.ReservedSecrets`, agent env scrubbing of the Anthropic key, managed settings file |
| Accounting/history | `byModel`, `result.json` cost (`model_by`, `priced_as`), `report --by model` |

**Gaps:** one upstream per gateway, keyed by kind; no per-model route; no provider config or key; no route fee; usage with no cache fields or no usage at all is not a defined case; `agent.auth` validation knows nothing of providers.

## Decisions already made (binding)

User rulings from M9: multi-model is M10 with its own design; Anthropic models stay direct (cache); the rest via OpenRouter; caps count model dollars only; `oauth` stays uncapped and Anthropic-only; stay in Go; no backward compatibility (reshape directly, re-run init); never use or ask for an API key; live tests sandbox-only and run by the user. Recommended here (design §0): no protocol translation; route by model inside the existing gateway; owner decides which repositories may send code to which provider; table price caps money, provider-reported cost is recorded not trusted; first slice is OpenRouter + one DeepSeek model.

## Review Focus

**Per-task review (security- or money-critical): T3, T4, T5, T6.** Everything else is reviewed once on the branch at the end (user's token-economy rule).

1. **A provider key reaching the agent, a log, a report.** T3/T4: `TestAgentEnvHasNoProviderKey`, `TestUpstreamErrorEchoingKeyIsRedacted`, `TestAgentAuthHeaderNeverForwarded`, `TestGatewayTokenNeverSentUpstream`.
2. **Code sent to a provider the owner did not approve.** T2: `TestRepoCannotAddProvider`, `TestAllowDataToRequired`, `TestNoCallBeforeRefusal`.
3. **An uncapped or under-charged call.** T5/T6: `TestFeeInReservation`, `TestMissingUsageChargesReservation`, `TestNoCacheFieldsChargesFullInput`, `TestUnpricedModelRefused`, `TestUnknownServingModelPricedMax`, `TestReportedCostNeverSettles`.
4. **A request outside the pins, or routed to the wrong upstream.** T3: `TestRouteByModel`, `TestUnpinnedModelStill400`, `TestVariantSuffixPinRefused`, `TestNoCrossHostRedirect`.

## File Structure

| Path | Role |
|---|---|
| `internal/config/providers.go` (new), `config.go`, `validate.go` | `providers` block (local config), strict decode, overlap and `allow_data_to`, `agent.auth` rules |
| `internal/policy` | provider IDs under `allowed_models` (explicit IDs only) |
| `internal/gateway/route.go` (new), `gateway.go`, `forward.go` | route table, per-upstream credential and header, fee, missing-usage rule |
| `internal/gateway/anthropicfake` (or `compatfake`) | configurable compat upstream |
| `internal/pricing` | starter row(s) with `unverified`, source and date, fee helper |
| `internal/runner`, `internal/cli` | secret mounting, env scrub, pins to env, bootstrap refusal, diagnose, `budget prices` columns |
| `internal/runstore`, `schemas/result.schema.json` | `route`, `reported` (additive) |
| `docs/design/v1.md`, `README.md`, `docs/gcp-live-checklist.md` | docs and live item |

## Task dependency graph and lanes

| Task | Size | Lane | Depends on | Review |
|---|---|---|---|---|
| T1 provider config: decode, validate, overlap, `agent.auth` rules | M | A | none | end |
| T2 repository policy: `allow_data_to`, pins for provider IDs, bootstrap refusal | M | A | T1 | end |
| T3 gateway routes: route table, per-upstream credential, header hygiene, redirects **(critical)** | L | B | none | own |
| T4 secrets and env: provider key mount, redaction, agent env scrub **(critical)** | M | A | T1 | own |
| T5 pricing: starter row, `unverified`, fee in reservation and settlement **(critical)** | M | B | T3 | own |
| T6 settlement rules: missing usage, missing cache fields, reported-cost record **(critical)** | M | B | T3, T5 | own |
| T7 compat-fake with the knobs (used by T3, T5, T6; built first, in lane B) | M | B | none | end |
| T8 runner wiring: pins to env, background default, end-to-end with fake `claude` | M | A | T2, T3, T4, T5 | end |
| T9 CLI: `budget prices` source/verified, `diagnose` route and reported vs charge, `report` model rows | S | A | T6, T8 | end |
| T10 docs: design sync into v1.md, README, setup guide (account-side OpenRouter policy, per-project credit limit) | S | B | T8 | end |
| T11 live check on the sandbox, user-run with the user's own key | S | user | all | n/a |

Order: T7 first (the fake is the harness for the rest), then T1 and T3 in parallel, T2/T4/T5, T6, T8, T9, T10, T11.

### Task 7 (M, lane B): Compat-fake

**Files:** `internal/gateway/anthropicfake/` (extend) or `compatfake/`, tests.

- A fake Messages upstream with knobs: required auth header (`x-api-key` or bearer), omit cache fields, omit usage, add a `cost` field, rename the serving model, 401/429/5xx, cut the stream, record the headers and body it received. Hand-written fixtures from docs, marked `unverified`.
- [ ] **Failing tests first:** `TestFakeRecordsAuthHeader`, `TestFakeOmitsUsage`, `TestFakeCutsStream`.
- [ ] Commit: `gateway: a compat upstream fake`

### Task 1 (M, lane A): Provider config

**Files:** `internal/config/providers.go`, `config.go`, `validate.go`, tests.

- `providers.<name>` with `kind: anthropic-compat`, `base_url` (https; loopback http for tests only), `auth`, `secret`, `route_fee_pct`, `models` (patterns), `allow_data_to`. Strict decode. Overlapping patterns across providers rejected. A provider-routed model requires `agent.auth: api-key` (message names `oauth`/`vertex` limits).
- [ ] **Failing tests first:** `TestProviderDecodeStrict`, `TestProviderOverlapRejected`, `TestProviderHTTPRefusedExceptLoopback`, `TestOAuthWithProviderModelRefused`, `TestVertexWithProviderModelRefused`, `TestFeeBounds`.
- [ ] Commit: `config: provider routes for non-Anthropic models`

### Task 2 (M, lane A): Repository policy and bootstrap refusal

**Files:** `internal/config/pins.go`, `policy`, `internal/runner` bootstrap, tests.

- A provider ID in the pins needs: claimed by a provider, repo in `allow_data_to`, priced, explicit (no alias, no variant suffix `:free|:nitro|:online|...`), on `allowed_models` if set. Refused **before any call**, naming the rule. A repository's `fugaro.yaml` can never add or alter a provider.
- [ ] **Failing tests first:** `TestRepoCannotAddProvider`, `TestAllowDataToRequired`, `TestVariantSuffixPinRefused`, `TestUnpricedModelRefused`, `TestNoCallBeforeRefusal`.
- [ ] Commit: `config: provider models need owner approval`

### Task 3 (L, lane B): Gateway routes **(critical: own review)**

**Files:** `internal/gateway/route.go`, `gateway.go`, `forward.go`, tests against the fake.

- `Options.Routes`: model pattern to `Upstream{Kind: compat, BaseURL, Credential, AuthHeader}`; Claude models keep the existing upstream. Chosen per request from the parsed model **after** the pin check. The agent's `x-api-key`/`authorization` are dropped and the route credential attached; the gateway token is never sent upstream. HTTP client refuses redirects to another host. No change to body or other headers. `count_tokens` for a compat route is answered locally (no forward) unless the route proves support.
- [ ] **Failing tests first:** `TestRouteByModel`, `TestUnpinnedModelStill400`, `TestAgentAuthHeaderNeverForwarded`, `TestGatewayTokenNeverSentUpstream`, `TestNoCrossHostRedirect`, `TestBodyUnchangedOnCompatRoute`, `TestKillSwitchCancelsProviderStream`.
- [ ] Commit: `gateway: route a pinned model to its provider`

### Task 4 (M, lane A): Secrets and env **(critical: own review)**

**Files:** `config.ReservedSecrets`, runner env building, redactor, init/secrets path, tests.

- Provider secret names reserved; mounted into the runner only; scrubbed from the agent env; redactor learns the value; upstream error text scanned before logging.
- [ ] **Failing tests first:** `TestAgentEnvHasNoProviderKey`, `TestUpstreamErrorEchoingKeyIsRedacted`, `TestWorkflowSecretCannotShadowProviderSecret`, `TestKeyNotInResultJSON`.
- [ ] Commit: `runner: provider keys stay out of the agent`

### Task 5 (M, lane B): Pricing and fee **(critical: own review)**

**Files:** `internal/pricing`, gateway reservation and settlement, tests.

- Starter row(s) for the first model, **unverified** with source and date and a pin-time warning (owner `model_prices` always wins; owner supplies the real numbers). `route_fee_pct` multiplies the worst case and the charge. Unknown serving model on a route: highest rate of the route's pattern, `priced_as: max`.
- [ ] **Failing tests first:** `TestFeeInReservation`, `TestFeeInSettlement`, `TestUnverifiedPriceWarns`, `TestOverrideBeatsEmbedded`, `TestUnknownServingModelPricedMax`.
- [ ] Commit: `pricing: provider prices and the route fee`

### Task 6 (M, lane B): Settlement rules **(critical: own review)**

**Files:** `internal/gateway/forward.go` (`fromUsage`), `runstore`, schema, tests.

- No cache fields: all input at the full input rate. No usage on a completed stream: charge the reservation, `settled: reserved`. Provider-reported cost recorded as `reported`, never settles (`TestReportedCostNeverSettles`); `route` recorded per call. `result.json` additive.
- [ ] **Failing tests first:** `TestNoCacheFieldsChargesFullInput`, `TestMissingUsageChargesReservation`, `TestReportedCostNeverSettles`, `TestUnknownEventsSkipped`, `TestStreamWithNoEventsChargedAfterStart`.
- [ ] Commit: `gateway: settle provider calls conservatively`

### Task 8 (M, lane A): Runner wiring

**Files:** `internal/runner`, tests with fake `claude` and the compat-fake.

- Pins to `--model` and the `ANTHROPIC_DEFAULT_*`/small-fast pins; background defaults to the coder's model with a provider coder; per-route settings fragment (empty at first). End to end: a `deepseek/...` run ends in a PR, cost exact, no key in the agent env, a halt works.
- [ ] **Failing tests first:** `TestProviderRunEndsInPR`, `TestBackgroundDefaultsToCoder`, `TestCostExactToTheMicro`, `TestHaltOnProviderRun`.
- [ ] Commit: `runner: runs on a provider model`

### Task 9 (S, lane A): CLI

`budget prices` source (embedded|override) and verified columns; `diagnose` route and `reported` vs `charge` with a warning above 10 %; `report --by model` rows carry the route.
- [ ] **Failing tests first:** `TestPricesShowsSourceAndVerified`, `TestDiagnoseShowsReportedVsCharge`.
- [ ] Commit: `cli: show routes and where a price came from`

### Task 10 (S, lane B): Docs

Fold the design into `v1.md`, README config section, the setup guide (OpenRouter account-side provider policy, a credit-limited key per Fugaro project), and the live checklist item.
- [ ] Commit: `docs: multi-model runs`

### Task 11 (S, user): Live check

Sandbox repository only, user-created credit-limited OpenRouter key, one cheap task. It replaces the hand fixtures with a recorded real response and confirms design §13. Not run by an agent; no key is ever given to an agent.

## Done when

All tasks green under `./.superpowers/heavy.sh`, per-task reviews of T3-T6 clean, the end-of-branch review clean, the design's §13 items either verified from a recording or left as named warnings, and the user's live check (T11) recorded.
