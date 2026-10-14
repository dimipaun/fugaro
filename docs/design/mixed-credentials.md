# Mixed credentials and a Sonnet senior reviewer in a cheap-coder loop

Status: draft for the owner's review, 2026-10-14. Nothing here is built. Related: [m10-multi-model.md](m10-multi-model.md) (provider routes, §7 auth modes, §11a two-tier review), [named-oauth-tokens.md](named-oauth-tokens.md), [recipes.md](recipes.md), [m9-budget-and-dashboard.md](m9-budget-and-dashboard.md) (D2).

## 1. Summary and recommendation

The cloud loop runs a cheap coder (DeepSeek through OpenRouter). The senior review stays outside it today, because the in-loop DeepSeek reviewer's "ship" is weak. This design asks how to put a Claude Sonnet senior reviewer **inside** the loop. Three ways:

- **Path A: an Anthropic API key behind the gateway.** Mostly works today. Real dollars, capped by the gateway, no new trust exposure. Needs a price check, docs, a recipe choice and a live check.
- **Path C: a stronger model through OpenRouter** (the owner's idea, 2026-10-14). The senior reviewer is any model OpenRouter serves (Claude Sonnet, Opus or Fable, GPT, Gemini), behind the same route table and gateway as the DeepSeek coder, with the one OpenRouter key. Real dollars, capped, one credential, no new code beyond prices and a live check.
- **Path B: the subscription OAuth token for the reviewer stage only** ("mixed credentials"). New code. No dollar cost, but the gateway cannot cap it and the token shares a container with a cheap model that reads untrusted repository content.

Owner ruling (2026-10-14): build both. B is worth its residual risk because Fugaro will work mostly with trusted repositories, and an explicit opt-in is enough (no separate reviewer job). C (or A) goes first because it needs no new code; the paths are independent.

## 2. What exists today (verified in the code)

- `agent.auth` is **one value per workflow**: `oauth`, `api-key` or `vertex` (`internal/config/providers.go`, `CheckProviderAuth`). A provider-routed model (DeepSeek) needs `api-key`; `oauth` with a provider pin is refused at config time ("a run does not mix credentials").
- With `api-key`, the runner starts the gateway with the **Anthropic upstream** and the repository's `ANTHROPIC_API_KEY` (`internal/runner/gateway.go`, `startGateway`), plus per-model **provider routes**. The real key is not put in the agent's environment; the agent gets a gateway token (`internal/agent/env.go`). So one `api-key` run can already call DeepSeek for some stages and Anthropic for others, routed by model ID.
- `oauth` is **never proxied** (`auth: oauth never goes through the gateway`): its calls are uncapped by the gateway and recorded as notional dollars.
- The role/stage model split exists: `agent.models.coder` and `agent.models.reviewer`, the `standard` recipe (a cheap loop, then the senior review, with bounce-back), and `first_line_review: auto`, which turns the cheap first-line review on when a provider serves the coder and none serves the reviewer (M10 §11a).
- `claude_accounts.<name>.allow_data_to` already gates which repositories may use a named OAuth token (`named-oauth-tokens.md`).

## 3. Path A: Sonnet through an Anthropic API key

**Config (no new keys):**

```yaml
agent:
  auth: api-key
  models:
    coder: deepseek/deepseek-v4.1-flash
    reviewer: claude-sonnet-5-5
```

with the `standard` recipe (`agent.recipe: standard`). Flow: DeepSeek implements, a DeepSeek first-line review and fix loop runs, then the Sonnet senior review decides readiness. A senior rejection bounces back to the cheap loop.

**What is missing:**

1. **A live check** (sandbox only): one run with this config proves the gateway routes both upstreams in one run, the Anthropic key stays out of the agent's environment, and the cost lines show both models. Number: the next free in `docs/gcp-live-checklist.md`.
2. **Cost guidance** in the user docs: Sonnet reads the whole diff at `claude-sonnet-5-5` list rates (the price table, `internal/pricing/table.go`), in real dollars, charged to the same per-run and daily caps. A rough ceiling is `review_rounds` times the diff size at those rates; the doc gives the formula, not a promise.
3. **Secrets:** the repository's `anthropic-api-key` secret must be set (`fugaro secrets set`), which the owner types.
4. **Optional later:** a catalog recipe only if `standard` plus pins proves awkward. This design adds none, to keep one way to do it.

**Risk:** none beyond today's api-key runs. The gateway holds the key; the D2 residual (a compromised agent reading the runner's `/proc`) is unchanged and already accepted for api-key runs.

## 3b. Path C: a stronger senior reviewer through OpenRouter

**Config (no new keys):** the provider route that already serves DeepSeek also lists the senior model, and the pins name it:

```yaml
# local operator config
providers:
  openrouter:
    models: ["deepseek/*", "anthropic/*"]
model_prices:
  anthropic/claude-sonnet-5.5: { input_per_m: 2, output_per_m: 10, cache_read: 0.2 }
# fugaro.yaml
agent:
  auth: api-key
  models:
    coder: deepseek/deepseek-v4.1-flash
    reviewer: anthropic/claude-sonnet-5.5
```

Facts checked against OpenRouter's public API on 2026-10-14: `anthropic/claude-sonnet-5.5` is listed at $2 input and $10 output per million tokens (the same as the direct list price), `anthropic/claude-opus-5.5` at $4 and $20, `anthropic/claude-fable-5.1` at $10 and $50. The ZDR endpoint list serves Anthropic models through Amazon Bedrock and Google, so the account's zero-data-retention-only privacy setting keeps working (to re-verify at build time; the list changes).

**Why it matters:** one credential (the OpenRouter key the DeepSeek coder already uses) serves both roles, so no Anthropic API key is needed, and the owner can choose the senior from any provider's models (a different model family reviews a DeepSeek diff, which is also a better second opinion than the same family). The gateway meters and caps it like DeepSeek: per-run and daily caps hold, in real dollars.

**Costs and caveats:**

- OpenRouter's route fee (`route_fee_pct`, 5.5 today) is added to every charge, so Sonnet through OpenRouter costs about 5.5% more than direct.
- `model_prices` must be keyed by the OpenRouter ID (`anthropic/claude-sonnet-5.5`, with a dot), which differs from the direct ID (`claude-sonnet-5-5`); an unpriced model is refused as with any provider model.
- `allow_data_to` on the provider decides which repositories may send code to it; adding `anthropic/*` to `models` widens what the existing route serves, so the owner's local config review applies.
- To verify at the first live check: whether a run whose models all route through OpenRouter still needs the repository's `ANTHROPIC_API_KEY` secret (the gateway's default upstream reads it, `internal/runner/gateway.go`; the current all-DeepSeek runs work, so the answer is probably a placeholder or an existing value), and that Claude Code accepts a Claude model served by OpenRouter's Anthropic-compatible endpoint with its tool use and caching intact.
- Extended thinking and Anthropic-only beta features may differ through OpenRouter; the live check runs a real review, not only a ping.

## 4. Path B: the OAuth token for the reviewer stage only

**Why:** the owner's Claude subscription has headroom that costs no dollars; a per-stage OAuth credential would let the senior review use it while the cheap stages stay metered through the gateway.

**Proposed shape (names are tentative):**

```yaml
agent:
  auth: api-key
  models:
    coder: deepseek/deepseek-v4.1-flash
    reviewer: claude-sonnet-5-5
  stage_auth:
    reviewer: oauth        # new: only "reviewer" and only "oauth" in the first slice
  claude_account: bmb      # the existing key, now naming the reviewer's token
```

**Mechanics:**

- The gateway still serves every stage except the reviewer's. For the reviewer's stage (and its scoped re-reviews), the runner starts Claude Code **without** the gateway base URL, with `CLAUDE_CODE_OAUTH_TOKEN` set and the Anthropic key absent, so the call goes straight to Anthropic.
- The token is in the environment of the reviewer process only, not the coder's. It is read at stage start from the runner's own environment (the job already mounts the secret), not placed in the coder processes' env.
- **Budget:** the gateway sees no call from this stage, so the stage is **uncapped by Fugaro**. Claude Code's own `--max-budget-usd` (already used per stage) bounds a single stage; the stage's cost is recorded as notional, like any oauth run. The reservation system does not apply; `result.json` marks the stage `credential: oauth`.
- **Validation:** `CheckProviderAuth` learns that `api-key` plus `stage_auth.reviewer: oauth` is the one permitted mix; `oauth` as the workflow auth with a provider coder stays refused. `claude_accounts.<name>.allow_data_to` must list the repository, checked at launch.
- **Recipes:** `roles: { reviewer: coder }` and `coder: reviewer` conflict with a reviewer-only credential (the coder would need the OAuth token): validation refuses them when `stage_auth.reviewer` is set.

## 5. Security: the residual risk, stated plainly

In Path B the subscription token and a cheap model that reads untrusted repository text live in **one container** for the whole run. A prompt-injected coder is the same OS user as the runner, so it can read the runner's environment through `/proc`, and it can reach the metadata server; this is the accepted D2 residual (the container is not a trust boundary against its own agent). What it adds in B: the stolen credential is a **subscription token usable anywhere**, not a key held in the gateway, and the gateway's caps do not bound it. Mitigations, in increasing cost:

1. Opt-in per workflow, **trusted repositories only**; never for runs that read fork PRs (`review.allow_forks`), enforced by validation.
2. Stage-scoped environment injection (above): narrows exposure to the runner's own memory and environ.
3. Fetch the token from Secret Manager at stage start instead of mounting it at job start: shortens the window but the metadata server stays reachable, so this narrows rather than closes.
4. **Run the reviewer in a separate job execution** with its own container and service account, so the coder never shares a container with the token. Closes the gap; costs a second cold start and an inter-job hand-off of the diff. Not in the first slice.

The SECURITY.md known-gaps list gets one line for B if it ships. Path A needs none.

## 6. Comparison

| | A: Anthropic API key | B: OAuth reviewer stage | C: OpenRouter |
|---|---|---|---|
| Dollar cost of the senior review | real, list rates | none (subscription) | real, list rates plus the route fee (5.5%) |
| Capped by the gateway | yes | no; only `--max-budget-usd` | yes |
| Credential exposure | gateway holds the key | token in the job for the whole run | gateway holds the key |
| Extra secret needed | an Anthropic API key | the OAuth token | none (the OpenRouter key already exists) |
| Choice of senior model | Claude only | Claude only | any model OpenRouter serves |
| New code | docs, one live check | validation, runner stage env, budget marking, docs, live check | prices, docs, one live check |
| Rollback | change two config lines | remove `stage_auth` | change two config lines |

## 7. Decided and open

Decided (owner, 2026-10-14):

1. Design the paths; build B (below) and, of A and C, the one the owner has a credential for. The owner's credentials today are an OpenRouter key and subscription OAuth tokens, no Anthropic API key, so C comes before A; A stays documented.
2. B is worth the residual risk of §5: Fugaro will mostly work with trusted repositories.
3. For B, an explicit opt-in is enough. The separate-job reviewer (§5 mitigation 4) is not required, and stays an optional hardening for repositories with outside contributors.

Open: should `stage_auth` generalize later to the coder role (an oauth coder with a provider reviewer)? Recommendation: no; the reviewer is the case that has a cost reason.

## 8. Testing and live checks

- Path A: config tests already cover the validation; add a gateway test with both upstreams in one run if none exists (a Sonnet-ID call and a DeepSeek-ID call, each charged at its own rates). Live check: the sandbox run in §3.1.
- Path B (if built): validation tests (one permitted mix, refusals for the other mixes and for `reviewer: coder` recipes); a runner test that the reviewer process's environment has the token and no gateway URL while the coder's has the reverse; a test that a `forks` run refuses it; a budget test that the stage is recorded as notional and the run's cap still binds the other stages. Live check: one sandbox run, the owner's token, no real repository.

## 9. Phases

1. C (and A if wanted): the live check, the cost doc, the user doc. No Go change expected beyond what the live check finds.
2. B: validation and runner stage env, with mitigations 1 (opt-in, trusted repositories only, refused with fork runs) and 2 (stage-scoped environment). Its live check is the sandbox run of §8.
3. Optional hardening: the separate-job reviewer (§5 mitigation 4), only for repositories with outside contributors.
