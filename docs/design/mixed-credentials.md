# Mixed credentials and a stronger senior reviewer in a cheap-coder loop

Status: draft for the owner's review, 2026-10-14, corrected after an independent review against the code. Nothing here is built. Related: [m10-multi-model.md](m10-multi-model.md) (provider routes, §7 auth modes, §11a two-tier review), [named-oauth-tokens.md](named-oauth-tokens.md), [recipes.md](recipes.md), [m9-budget-and-dashboard.md](m9-budget-and-dashboard.md) (D2).

## 1. Summary and recommendation

The cloud loop runs a cheap coder (DeepSeek through OpenRouter). The senior review stays outside it today, because the in-loop DeepSeek reviewer's "ship" is weak. This design asks how to put a stronger senior reviewer **inside** the loop. Three ways:

- **Path A: Claude through an Anthropic API key, behind the gateway.** Works today with the code as it is. Real dollars, capped by the gateway, no new trust exposure. Needs an Anthropic API key, docs and a live check.
- **Path C: a stronger model through OpenRouter.** Any model OpenRouter serves as the senior reviewer, behind the same route table and gateway as the coder. One provider key for both roles. **Not possible today**: the config refuses it for Claude models (§4), so it needs a small, deliberate change to an M10 invariant. For a non-Claude senior (GPT, Gemini) it needs no such change.
- **Path B: the subscription OAuth token for the reviewer stage only.** Needs infra and runner changes (§5). No dollar cost, but the gateway cannot cap it and the token shares a job with a cheap model that reads untrusted repository content.

Owner rulings (2026-10-14): B is worth its residual risk because Fugaro will work mostly with trusted repositories, and an explicit opt-in is enough (no separate reviewer job). The senior reviewer is built through whichever of A and C the owner has a credential for; the owner has an OpenRouter key and subscription OAuth tokens, no Anthropic API key. This design recommends: (1) a **non-Claude senior through OpenRouter first**, because it needs no code change and is the cheapest test of the idea; (2) **C for Claude models** once the owner rules on the M10 invariant in §4; (3) **A** only if the owner gets an Anthropic key; (4) **B** as the separate, larger piece of work, behind its opt-in.

## 2. What exists today (verified in the code)

- `agent.auth` is **one value per workflow**: `oauth`, `api-key` or `vertex`. A provider-routed model (DeepSeek) needs `api-key`; `oauth` or `vertex` with a provider pin is refused at config time (`internal/config/providers.go`, `CheckProviderAuth`).
- With `api-key`, the runner starts the gateway with the **Anthropic upstream** and the repository's `ANTHROPIC_API_KEY` (`internal/runner/gateway.go`, `startGateway`), plus **provider routes** for the providers the pinned models name. The real key is not put in the agent's environment (`internal/agent/env.go`). `internal/infra/spec.go` mounts the `anthropic-api-key` secret for **every** `api-key` workflow, and `env.go` fails the run with "secret ANTHROPIC_API_KEY is not set" if it is empty or shorter than 4 bytes. The repository's current all-DeepSeek `fugaro.yaml` works because that secret exists.
- `oauth` is never proxied; the token secret is mounted, and per-account workflow variant jobs are built, **only** when `agent.auth` is `oauth`. The key that names the account is `agent.oauth_token` (repository and override scope, 0.6.5 or later), checked against `claude_accounts.<name>.allow_data_to`.
- The runner writes **one managed settings file** that points every Claude Code process at the gateway and refuses settings that route around it (`internal/runner/agentenv.go`, `writeManagedSettings`, `checkSettingsRouting`).
- **Claude goes direct, by rule.** `ClaudePatterns` (`claude-*` and `anthropic/*`) are refused as provider model patterns (`CheckModelPattern`), and `pricing.IsAlias` treats the `anthropic/` prefix as an alias, so `model_prices` and pins reject an `anthropic/<id>` key (`internal/pricing/alias.go`, `overrides.go`). M10's reason (decision 2, spec §5.2): Claude's prompt-cache markers are handled on the direct path.
- The stage split exists: `agent.models.coder` and `.reviewer`, the `standard` recipe (a cheap loop, then the senior review, with bounce-back), and `first_line_review: auto`, which runs the cheap first-line review when a provider serves the coder and none serves the reviewer.

## 3. Path A: Claude through an Anthropic API key

```yaml
agent:
  auth: api-key
  models:
    coder: deepseek/deepseek-v4.1-flash
    reviewer: claude-sonnet-5-5
```

with `agent.recipe: standard`. DeepSeek implements, a first-line DeepSeek review and fix loop runs, then the Sonnet senior review decides readiness; a rejection bounces back to the cheap loop. Verified against the code: `CheckProviderAuth` passes for `api-key`, `first_line_review: auto` fires for a provider coder with a Claude reviewer, and `claude-sonnet-5-5` is in the embedded price table ($2 input, $10 output per million tokens; verify against the list price at build time).

**Prerequisites (all existing behavior, to document):**

1. `budget.mode: enforce` with a per-run cap (`CheckProviderGateway`).
2. If `budget.allowed_models` is set, it lists both models.
3. `agent.models.background` defaults to the provider coder, so Claude Code's background requests during the reviewer stage still go to DeepSeek through the gateway; the doc says so and how to pin it.
4. A real Anthropic API key in the repository's `anthropic-api-key` secret, which the owner types.

**Missing:** the user docs, the cost guidance (Sonnet reads the diff at list rates in real dollars, bounded by the per-run cap; a ceiling is `review_rounds` times the diff size at those rates), a gateway test with both upstreams in one run (check `internal/runner` and `internal/gateway` provider tests first), and one sandbox live check. No new trust exposure: the gateway holds the key; the D2 residual is unchanged.

## 4. Path C: a stronger model through OpenRouter

```yaml
# local operator config
providers:
  openrouter:
    models: ["deepseek/*", "openai/gpt-5*"]   # a non-Claude senior: valid today
model_prices:
  openai/gpt-5.5: { input_per_m: ..., output_per_m: ... }   # cache_read is a multiplier of input_per_m (default 0.1), not a price
# fugaro.yaml
agent:
  auth: api-key
  models: { coder: deepseek/deepseek-v4.1-flash, reviewer: openai/gpt-5.5 }
```

**A non-Claude senior (GPT, Gemini, a larger DeepSeek or Qwen) works with the existing rules**: the route table, pins, prices and gateway need nothing new, and a different model family reviewing a DeepSeek diff is a better second opinion than the same family. Two things still apply: an existing, at least 4-byte `ANTHROPIC_API_KEY` secret (§2: a placeholder the owner types is enough when no `claude-*` model is pinned, since the gateway sends it upstream only for a Claude model), and `allow_data_to` listing the repository for the provider.

**A Claude senior through OpenRouter (`anthropic/claude-sonnet-5.5`) is refused today**, in three places: `ClaudePatterns` rejects a provider claiming `anthropic/*`, `IsAlias` rejects the key in `model_prices`, and the pin checks reject the ID as an alias. Allowing it is a design change to an M10 invariant, and needs the owner's explicit ruling first. The change would be: an opt-in route entry that claims one explicit `anthropic/<id>` (not a pattern), with `IsAlias` and the pin checks accepting exactly the IDs a route claims, and the alias tests changed on purpose. The cache-marker reasoning (M10 decision 2) is the open technical question: OpenRouter forwards Anthropic's `cache_control`, but whether Claude Code's cache markers survive the Anthropic-compatible endpoint is to be measured, not assumed; the live check compares `cache_read` tokens between a direct and an OpenRouter run.

Facts from OpenRouter's public API, 2026-10-14 (to re-verify at build time; the lists change): `anthropic/claude-sonnet-5.5` $2/$10 per million tokens, `anthropic/claude-opus-5.5` $4/$20, `anthropic/claude-fable-5.1` $10/$50; Anthropic models are served by Amazon Bedrock and Google on the zero-data-retention endpoint list. The route fee (`route_fee_pct`, 5.5 today) is added to every charge.

## 5. Path B: the OAuth token for the reviewer stage only

**Proposed shape (names tentative):**

```yaml
agent:
  auth: api-key
  models: { coder: deepseek/deepseek-v4.1-flash, reviewer: claude-sonnet-5-5 }
  stage_auth: { reviewer: oauth }   # new; only "reviewer" and only "oauth" in the first slice
  oauth_token: bmb                  # the existing key, now naming the reviewer's account
```

**What it takes (larger than a flag):**

- **Infra:** mount the OAuth token secret for an `api-key` workflow that sets `stage_auth`, and build the per-account variant jobs for it (today both happen only for `agent.auth: oauth`); `fugaro init --repo` re-run and an upgrade note for deployed jobs.
- **Runner:** one gateway, one managed settings file and one `spec.Auth` switch exist today. The reviewer stage needs its own spec: Claude Code started without the gateway base URL and with the token, the managed settings file absent or rewritten for that stage, restored at stage end, and a Claude-only model set for it (the background and haiku pins are DeepSeek ids with no meaning on a direct stage). The gateway sees no call from the stage, so the stage is recorded as notional dollars with `credential: oauth` in `result.json`.
- **Validation:** `CheckProviderAuth` accepts exactly `api-key` plus `stage_auth.reviewer: oauth`; `reviewer: coder` and `coder: reviewer` recipes are refused with it (the coder would need the token); `claude_accounts.<name>.allow_data_to` is checked at launch.
- **Budget:** uncapped by the gateway; the stage is bounded only by `agent.max_budget_usd`, which is **Claude Code's own estimate** (accurate for a Claude model, unlike for DeepSeek, where it is about 25 times too high).

## 6. Security: the residual risk, stated plainly

In Path B the subscription token and a cheap model that reads untrusted repository text share one job for the whole run. The token reaches the runner as a mounted secret in the runner's environment, and the coder runs as the same OS user, so a prompt-injected coder can read it from `/proc/<runner>/environ`; the job's service account also keeps `secretAccessor` on the secret, and the metadata server is reachable. This is the accepted D2 residual. What B adds: the stolen credential is an **account-wide subscription token usable anywhere**, with no spend cap and shared rate limits with the owner's interactive use, not a key held in the gateway.

Mitigations, honestly:

1. **Opt-in, trusted repositories only.** Enforced by validation as far as config can: `claude_accounts.allow_data_to`, and refusing `stage_auth` where the workflow takes follow-ups from public-repository comments (`followup.allow_public`). It is only as strong as who can push branches and comment.
2. **Stage-scoped environment** keeps the token out of the coder's own environment and logs. It does **not** narrow the `/proc` attack above.
3. **Fetching the token at stage start** shortens the window but does not close it: the service account keeps access.
4. **A separate job execution for the reviewer**, with its own container and service account, is the only mitigation that closes the gap. Not required by the owner's ruling; an optional hardening for repositories with outside contributors.

The SECURITY.md known-gaps list gets one line for B if it ships. Path A needs none. Path C changes the data-flow scope: `allow_data_to` on the provider decides which repositories may send code to it.

## 7. Comparison

| | A: Anthropic key | C: OpenRouter | B: OAuth reviewer stage |
|---|---|---|---|
| Dollar cost of the senior review | real, list rates | real, list rates plus the route fee (5.5%) | none (subscription) |
| Capped by the gateway | yes | yes | no; only Claude Code's own estimate |
| Credential exposure | gateway holds the key | gateway holds the key | token in the job for the whole run |
| Extra secret | an Anthropic API key | none (a placeholder for `ANTHROPIC_API_KEY`) | the OAuth token |
| Senior model | Claude | non-Claude today; Claude after the §4 ruling | Claude |
| New code | docs, one live check, a gateway test | none for a non-Claude senior; for Claude: `ClaudePatterns`, `IsAlias`, pin checks and their tests | infra mounts and variant jobs, per-stage runner spec, validation, budget marking |
| Rollback | change two config lines | change two config lines | remove `stage_auth` |

## 8. Decided and open

Decided (owner, 2026-10-14): B is worth the residual risk; opt-in is enough; the separate-job reviewer is not required; the senior is built through whichever of A and C has a credential.

Open, for the owner's ruling before a plan is written:

1. **May a provider route claim one explicit `anthropic/<id>`** (the §4 change to M10's "Claude goes direct" invariant), or does Claude stay direct-only and C is limited to non-Claude seniors?
2. Is a non-Claude senior (GPT or Gemini through OpenRouter) acceptable as the first step, before any Claude senior?
3. Should `stage_auth` generalize later to the coder role? Recommendation: no.

## 9. Testing and live checks

- **A and C (non-Claude):** a gateway test with both upstreams in one run (a Claude ID and a provider ID, each charged at its own rates), after checking whether one exists; the sandbox live check runs a real review and compares cost lines. For a Claude senior through OpenRouter, the live check compares `cache_read` tokens against a direct run.
- **C (Claude), if ruled in:** `CheckModelPattern`, `IsAlias`, pin and price validation tests, with the existing alias case changed on purpose, and a test that an unclaimed `anthropic/<id>` is still refused.
- **B, if built:** validation tests (the one permitted mix and every refusal); infra tests that `SecretMounts` mounts the token for `api-key` plus `stage_auth` and builds the variant jobs; a runner test that the reviewer process has the token, no gateway URL and a restored managed settings file at stage end, while the coder's environment is the reverse; a budget test that the stage is notional and the cap still binds the other stages; one sandbox run with the owner's token.

## 10. Phases

1. Docs and the live check for a **non-Claude senior through OpenRouter** (no Go change expected beyond what the check finds), and for A if the owner gets an Anthropic key.
2. The §4 change and C for Claude, if the owner rules it in.
3. B: infra, runner per-stage spec, validation, with mitigations 1 and 2.
4. Optional hardening: the separate-job reviewer, only for repositories with outside contributors.
