# Mixed credentials and a stronger senior reviewer in a cheap-coder loop

Status: draft for the owner's review, 2026-10-14, corrected after an independent review against the code. Nothing here is built. Related: [m10-multi-model.md](m10-multi-model.md) (provider routes, §7 auth modes, §11a two-tier review), [named-oauth-tokens.md](named-oauth-tokens.md), [recipes.md](recipes.md), [m9-budget-and-dashboard.md](m9-budget-and-dashboard.md) (D2).

## 1. Summary and recommendation

The cloud loop runs a cheap coder (DeepSeek through OpenRouter). The senior review stays outside it today, because the in-loop DeepSeek reviewer's "ship" is weak. This design asks how to put a stronger senior reviewer **inside** the loop. Three ways:

- **Path A: Claude through an Anthropic API key, behind the gateway.** Works today with the code as it is. Real dollars, capped by the gateway, no new trust exposure. Needs an Anthropic API key, docs and a live check.
- **Path C: a stronger model through OpenRouter.** Any model OpenRouter serves as the senior reviewer, behind the same route table and gateway as the coder. One provider key for both roles. **Not possible today**: the config refuses it for Claude models (§4), so it needs a small change to an M10 invariant, which the owner has approved as an explicit opt-in. For a non-Claude senior (GPT, Gemini) it needs no such change.
- **Path B: the subscription OAuth token for the reviewer stage only.** Needs infra and runner changes (§5). No dollar cost, but the gateway cannot cap it and the token shares a job with a cheap model that reads untrusted repository content.

Owner rulings (2026-10-14): B is worth its residual risk because Fugaro will work mostly with trusted repositories, and an explicit opt-in is enough (no separate reviewer job). The senior reviewer is built through whichever of A and C the owner has a credential for; the owner has an OpenRouter key and subscription OAuth tokens, no Anthropic API key. This design recommends: (1) a **non-Claude senior through OpenRouter first**, because it needs no code change and is the cheapest test of the idea; (2) **C for Claude models**, now that the owner has ruled the M10 invariant is relaxed by an explicit opt-in (§4); (3) **A** only if the owner gets an Anthropic key; (4) **B** as the separate, larger piece of work, behind its opt-in.

## 2. What exists today (verified in the code)

- `agent.auth` is **one value per workflow**: `oauth`, `api-key` or `vertex`. A provider-routed model (DeepSeek) needs `api-key`; `oauth` or `vertex` with a provider pin is refused at config time (`internal/config/providers.go`, `CheckProviderAuth`).
- With `api-key`, the runner starts the gateway with the **Anthropic upstream** and the repository's `ANTHROPIC_API_KEY` (`internal/runner/gateway.go`, `startGateway`), plus **provider routes** for the providers the pinned models name. The real key is not put in the agent's environment (`internal/agent/env.go`). `internal/infra/spec.go` writes the `anthropic-api-key` mount into the spec for **every** `api-key` workflow. *(2026-10-15: PR #371 changed what happens to it — `internal/infra/readiness.go` drops the mount when the secret has no enabled version (`OptionalSecret`, `:74`), `internal/agent/env.go:92-105` wants the key only with the gateway off, and `fugaro init --repo` asks for one only when a call of the run reaches Anthropic. So the key is optional and no placeholder is needed; the one refusal left is a key that is present and shorter than 4 bytes.)* The repository's current all-DeepSeek `fugaro.yaml` works because that secret exists.
- `oauth` is never proxied; the token secret is mounted, and per-account workflow variant jobs are built, **only** when `agent.auth` is `oauth`. The key that names the account is `agent.oauth_token` (repository and override scope, 0.6.5 or later), checked against `claude_accounts.<name>.allow_data_to`.
- The runner writes **one managed settings file** that points every Claude Code process at the gateway and refuses settings that route around it (`internal/runner/agentenv.go`, `writeManagedSettings`, `checkSettingsRouting`).
- **Claude goes direct, by rule.** `ClaudePatterns` (`claude-*` and `anthropic/*`) are refused as provider model patterns (`CheckModelPattern`). *(2026-10-15: this bullet read "`pricing.IsAlias` treats the `anthropic/` prefix as an alias, so `model_prices` and pins reject an `anthropic/<id>` key". The pricing half of §4's opt-in is now built: `IsAlias` accepts a well-formed `anthropic/<id>` (`internal/pricing/alias.go`), so a pin and a `model_prices` key may name one exact ID; a route entry may claim that same ID, and an `anthropic/<id>` no route names is refused by the config gate rather than by the alias rule (`CheckProviderPolicy`, `internal/config/providers.go`). Every pattern spelling — `claude-*`, `anthropic/*`, `anthropic/claude-*` — is still refused.)* M10's reason (decision 2, spec §5.2): Claude's prompt-cache markers are handled on the direct path.
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

**A Claude senior through OpenRouter (`anthropic/claude-sonnet-5.5`) was refused before the opt-in**, in three places: `ClaudePatterns` rejected a provider claiming `anthropic/*`, `IsAlias` rejected the key in `model_prices`, and the pin checks rejected the ID as an alias. The owner ruled on 2026-10-14 that the rule is relaxed with an explicit opt-in in config. The change is: an opt-in route entry that claims one explicit `anthropic/<id>` (not a pattern), with the pin checks accepting exactly the IDs a route claims, and the alias tests changed on purpose. (It is built in a narrower shape than this paragraph proposed — the price package stayed claim-blind, so `IsAlias` itself knows nothing about claims: see "What shipped" below.) **Where the entry lives** (owner ruling, 2026-10-14): in the local operator config or in the shared project layer, so a project lead can enable it without editing local config; a repository's own `fugaro.yaml` can choose the model (`agent.models.reviewer`) but cannot add or enable a route, and `allow_data_to` on the provider still decides which repositories may send code. The plumbing is wider than the config check: the gateway's `validateRoutes` shares `CheckModelPattern`, and the claimed IDs must reach the pricing package (`IsAlias`, `CheckPins`, `CheckProviderPolicy`). With a provider-served senior, `first_line_review: auto` turns the first-line review **off** (it is on only when the coder is provider-served and the reviewer is not), so a Path C senior runs without the cheap first line unless `first_line_review: on` is set. The cache-marker reasoning (M10 decision 2) is the open technical question: OpenRouter forwards Anthropic's `cache_control`, but whether Claude Code's cache markers survive the Anthropic-compatible endpoint is to be measured, not assumed; **Check 46** of [gcp-live-checklist.md](../gcp-live-checklist.md) is the live check that compares `cache_read` (and `cache_write_5m`) tokens and the charge between a direct and an OpenRouter run, and it has not been run, so the question stays open until it has.

**What shipped (2026-10-15, PR #387), and in what shape.** The ruling above is built, and not in the shape the paragraph above describes: **the claim is one literal `anthropic/<id>` route entry, and the entry itself is the whole permission** (`config.CheckClaudeClaim`, `internal/config/providers.go` — one explicit provider model ID under the prefix, no wildcard, no separate claimed-ID list and no switch). **The pricing package stayed claim-blind**: `IsAlias` keeps its signature and accepts a well-formed `anthropic/<id>` as a real model ID (`internal/pricing/alias.go`, with `IsProviderModelID` exporting the grammar so the claim rule and the price grammar cannot drift), so `model_prices` may key one and `CheckPins` needs no providers argument; the gate on **pinning** is `internal/config`'s, in `CheckProviderPolicy`, which asks its own providers map whether a route claims the ID. `anthropic/*`, `anthropic/claude-*`, a bare `claude-*`, a `:` variant, a `[1m]` spelling and an over-length ID are refused exactly as before, and an ID no route claims is refused at bootstrap and by `fugaro validate`. The implementation plan is [2026-10-14-openrouter-senior-reviewer.md](../plans/2026-10-14-openrouter-senior-reviewer.md), and its tasks divide like this: **Tasks 4 and 5 are merged** (PR #387 — the price half, `IsAlias`/`IsProviderModelID`, and the config half, the claim rule and the refusals); **Task 6 is in flight** (the end-to-end run and its refusals, PR #394), as are **Task 7** (the claim in the shared project layer, PR #397) and **Task 8** (these docs, PR #398); **Tasks 1 and 2 are not written yet** (the user-doc section for a non-Claude senior, and the two tests Phase 1 is for), and **Tasks 3 and 9** are the maintenance rule for Checks 45 and 46, whose sections are in [gcp-live-checklist.md](../gcp-live-checklist.md) and which the owner has not run. What shipped is the operator-config half of the place ruling in §8: the operator names the ID in a route's `models`, and a repository's own `fugaro.yaml` can still choose the model but never enable a route.

Facts from OpenRouter's public API, 2026-10-14 (to re-verify at build time; the lists change): `anthropic/claude-sonnet-5.5` $2/$10 per million tokens, `anthropic/claude-opus-5.5` $4/$20, `anthropic/claude-fable-5.1` $10/$50; Anthropic models are served by Amazon Bedrock and Google on the zero-data-retention endpoint list. The route fee (`route_fee_pct`) is added to every charge; the code default is 0, and 5.5 is the value in the owner's operator config. A route that reports no cache fields is charged at the full input rate, so a senior with worse caching costs more as well as risking different behavior; `fugaro budget preview` (#351) gives a ceiling.

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

1. **Opt-in, trusted repositories only.** Enforced by validation as far as config can: `claude_accounts.allow_data_to`, and refusing `stage_auth` where the workflow takes follow-ups from public-repository comments (`followup.allow_public`), together with review mode's trusted-author rule and SECURITY.md's fork handling. `allow_public` does not gate the first run on a public repository, so the gate is only as strong as who can push branches and comment.
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
| Extra secret | an Anthropic API key | no new secret. *(2026-10-15: this cell read "the existing `anthropic-api-key` secret must hold a placeholder of at least 4 bytes"; PR #371 removed the requirement — the key is optional, the mount is dropped when the secret has no enabled version, and nothing prompts. A key that is present and shorter than 4 bytes is still refused, `internal/agent/env.go:101-102`.)* | the OAuth token |
| Senior model | Claude | non-Claude today; Claude after the §4 ruling | Claude |
| New code | docs, one live check, a gateway test | none for a non-Claude senior; for Claude: `ClaudePatterns`, `IsAlias`, pin checks and their tests | infra mounts and variant jobs, per-stage runner spec, validation, budget marking |
| Rollback | change two config lines | change two config lines | remove `stage_auth` |

## 8. Decided and open

Decided (owner, 2026-10-14): B is worth the residual risk; opt-in is enough; the separate-job reviewer is not required; the senior is built through whichever of A and C has a credential. **M10's "Claude goes direct" rule is relaxed by an explicit opt-in in config**: a provider route may claim a Claude model through OpenRouter only when the operator config names it explicitly (§4); with no such entry the rule is unchanged and everything is refused as today. **No decision here is still open, but only one half of the opt-in is built, so this list is not "implemented" as a whole.** The plan is [2026-10-14-openrouter-senior-reviewer.md](../plans/2026-10-14-openrouter-senior-reviewer.md) (PR #359). **Shipped:** the operator-config half — the claim is one literal `anthropic/<id>` route entry, the price table stays claim-blind, the gate is `internal/config`'s (PR #387, 2026-10-15, the shape §4's "What shipped" records). **In flight, unmerged:** the shared-project-layer half (Task 7, PR #397), the run-level tests (Task 6, PR #394) and these docs (Task 8, PR #398) — until the layer half lands, the claim reaches a run through the operator config only, and §4's paragraph about the second place is the design's, not the tree's. The opt-in's place is decided the same day: the route entry lives in the local operator config or the shared project layer, one explicit `anthropic/<id>` and not a pattern; a repository can choose the model but cannot enable a route.

Open, still for the owner, and the plan's remaining tasks and live checks do not wait on any of them:

1. Is a non-Claude senior (GPT or Gemini through OpenRouter) acceptable as the first step, before any Claude senior? The owner rules it; **Check 45** is the by-hand comparison the ruling is made from, and it has not been run.
2. Should `stage_auth` generalize later to the coder role? Recommendation: no.
3. §4's own question, kept open on purpose: do Claude Code's prompt-cache markers survive the Anthropic-compatible endpoint? **Check 46** measures it — `cache_read`/`cache_write_5m` and the charge, the same task run routed against direct — and it has not been run.

## 9. Testing and live checks

- **A and C (non-Claude):** a gateway test with both upstreams in one run (a Claude ID and a provider ID, each charged at its own rates), after checking whether one exists; the sandbox live check runs a real review and compares cost lines. For a Claude senior through OpenRouter, the live check (**Check 46**) compares `cache_read` tokens against a direct run.
- **C (Claude):** `CheckModelPattern`, `IsAlias`, pin and price validation tests, with the existing alias case changed on purpose, and a test that an unclaimed `anthropic/<id>` is still refused.
- **B, if built:** validation tests (the one permitted mix and every refusal); infra tests that `SecretMounts` mounts the token for `api-key` plus `stage_auth` and builds the variant jobs; a runner test that the reviewer process has the token, no gateway URL and a restored managed settings file at stage end, while the coder's environment is the reverse; a budget test that the stage is notional and the cap still binds the other stages; one sandbox run with the owner's token.

## 10. Phases

1. Docs and the live check for a **non-Claude senior through OpenRouter** (no Go change expected beyond what the check finds), and for A if the owner gets an Anthropic key.
2. The §4 change (the explicit opt-in) and C for Claude.
3. B: infra, runner per-stage spec, validation, with mitigations 1 and 2.
4. Optional hardening: the separate-job reviewer, only for repositories with outside contributors.
