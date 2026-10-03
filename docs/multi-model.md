# Running a non-Anthropic model (M10)

A run's coder can be a model served by OpenRouter (the first one is `deepseek/deepseek-v4-flash`) while Claude Code stays the harness and Claude reviews. Fugaro sends the model traffic through its own gateway, which picks the upstream by the request's model ID, attaches the provider's key (the agent never holds it) and prices every call. Nothing is translated: OpenRouter's Anthropic-compatible endpoint receives the same Messages request Claude Code sends (assumed until Check 25: that the endpoint exists at that path, accepts `Authorization: Bearer` and answers in the Anthropic stream format). The design and its open assumptions are in [design/m10-multi-model.md](design/m10-multi-model.md); the live check that settles them is Check 25 of [gcp-live-checklist.md](gcp-live-checklist.md).

This is an experiment, not a default. Treat the first runs as measurements: the cost to reach a Claude-approved pull request is the number that matters.

## 1. On the OpenRouter account (before anything in Fugaro)

Fugaro never rewrites a request body, so what the gateway cannot ask for per request has to be set on the account.

- **A key per Fugaro project, with a credit limit.** Create it for this project only and set its credit limit at OpenRouter to what you are willing to lose. The key sits in the runner's process like the Anthropic key does, and a compromised agent in the same container could read it from `/proc`; the credit limit is the real backstop (the same stance as the workspace limit on an Anthropic key). Fugaro's dollar caps bound a well-behaved run, not a hostile one.
- **Provider preferences: no fallbacks.** In the account's settings pin the providers that may serve the model and turn **fallbacks off**. Otherwise OpenRouter may serve another model or provider than the one you priced (assumed until Check 25); Fugaro then charges the dearer rate and flags `priced_as: max`, but only the account setting prevents it.
- **Data policy.** Set the account's data-collection and zero-data-retention preferences (for example deny data collection) there. A repository's code is sent to whichever provider serves the request, so this is a decision about the code, made once, by the owner.
- **The route fee.** Note the fee your account pays on top of the model price; it goes into `route_fee_pct` below.
- **The real prices** of the model, from its OpenRouter page.

## 2. The project config (owner only)

In `~/.config/fugaro/projects/<project>.yaml` (the local config; a repository's `fugaro.yaml` cannot set any of this):

```yaml
providers:
  openrouter:
    kind: anthropic-compat            # the only kind
    base_url: https://openrouter.ai/api   # https; http only on a loopback host (tests)
    auth: bearer                      # bearer | x-api-key
    secret: openrouter-api-key        # the secret's NAME; never the key
    route_fee_pct: 5.5                # 0 to 50, added to every charge and reservation
    models: ["deepseek/*"]            # exact IDs or a prefix ending in *; providers may not overlap, and may not claim Claude models
    allow_data_to: [edgeappinc/fugarosandbox, dimipaun/fugaro]   # repositories whose code may go here; none when absent
model_prices:
  deepseek/deepseek-v4-flash:         # example numbers: use the real ones
    input_per_m: 0.14
    output_per_m: 0.28
    cache_read: 0.1                   # a multiplier of input_per_m (default 0.1), not a price
budget:
  mode: enforce                       # a provider model needs the gateway in enforce mode: observe never refuses a call
  per_run_usd: 2                      # enforce needs a per-run cap
```

Then run `fugaro init --repo` in each repository that is listed, so its job carries the provider list and mounts the key's variable. A repository not in `allow_data_to` gets neither, and a run on it that names a provider model is refused. Removing a repository from `allow_data_to` takes effect only after `fugaro init --repo` is run again for it (the list and the key's mount live in the job's configuration until then).

**Provider models require `budget.mode: enforce`.** Provider calls are real dollars and `observe` never refuses a call, so a run (and `fugaro validate`) that names a provider model under `observe` or `off` is refused, naming the rule. Set `budget.mode: enforce` and `budget.per_run_usd` in the local project config and run `fugaro init --repo`; `fugaro budget set --global --mode enforce` (which needs the global `--daily` and `--per-run` caps, and asks you to type the project's name to lower it again) makes the shared database enforce too.

**Set `model_prices` yourself.** The embedded `deepseek/deepseek-v4-flash` row is a placeholder (1 / 4 / 0.1 dollars per million tokens), marked unverified: no price was read from OpenRouter (the real prices, and that OpenRouter reports usage the way the gateway reads it, are assumed until Check 25). A pin on it is accepted with a warning (`fugaro validate`, `diagnose`) and the cap counts that guess. Your `model_prices` entry replaces it. `fugaro budget prices` has a `VERIFIED` column and shows the source of an unverified row. When you leave out the cache fields, they default to the usual multiples of `input_per_m` (never to 0).

## 3. The key

From a checkout of the repository (any listed repository), paste or pipe the key; it is read only from stdin, never an argument, and never printed:

```bash
fugaro secrets set openrouter-api-key
```

It is refused for a repository the provider does not list in `allow_data_to`. The key is mounted into the runner's environment only (as `FUGARO_PROVIDER_KEY_OPENROUTER_API_KEY`) and, for workflows with `agent.auth: api-key`; the agent's own environment never has it, and it is registered with the log redactor. A workflow's own `secrets:` may not use the provider's secret name.

## 4. The repository's `fugaro.yaml`

```yaml
agent:
  auth: api-key
  models:
    coder: deepseek/deepseek-v4-flash
    reviewer: claude-sonnet-5-5
    background: claude-haiku-4-5      # optional: with a provider coder it defaults to the coder's model
  first_line_review: auto             # auto | on | off
  first_line_rounds: 1                # 1 to 3
```

`fugaro validate` checks the config-side rules above against the local config (a provider that claims the model, `allow_data_to`, `agent.auth`, the gateway being on, variants, prices); it cannot see whether the key is mounted or well formed, which only the runner refuses. With a provider coder and a Claude reviewer, `first_line_review: auto` makes the coder's model review and fix its own work first (`review_first` stages, recorded with `tier: first`); the Claude `review` always runs afterwards and alone decides whether the pull request is ready. The first line is priced as the provider model, so it adds cents; it can only add cost, never replace the senior review.

## 5. What is refused, and why

Each is an `infra_error` before any model call, naming the pin and the rule (and `fugaro validate` reports the same):

- **No provider claims the model**, or the provider's `allow_data_to` does not name this repository. Code only goes where the owner said it may.
- **`agent.auth` is `oauth` or `vertex`.** An OAuth token is a Claude subscription credential and is never sent elsewhere, and a run does not mix credentials. Use `api-key`.
- **The gateway is off or only observing** (`budget.mode: off` or `observe`). The gateway is what holds the key and prices the calls, and only `enforce` refuses a call that would go over a cap.
- **A variant suffix (`:free`, `:nitro`, `:online`) or an alias.** A variant is another price and routing; pins are exact upstream IDs.
- **No price** for the model (add it under `model_prices`), or an entry in `budget.allowed_models` or the pins that the owner's allow-list does not carry.
- **A provider key that is not mounted**, or has whitespace, a control character or non-ASCII, or fewer than 4 bytes.

At run time the gateway also refuses a request whose model is not pinned for the stage, and answers token counting for a provider model with a local 404 (Claude Code then estimates itself).

## 6. Reading the cost

Provider models are real dollars: the charge is the table price of the reported usage plus the route fee. When a response carries no usage at all, the call is charged its full reservation; when it carries no cache fields, all input is charged at the full input rate. Both err high. Whether OpenRouter's responses carry usage and cache fields is assumed until Check 25.

`fugaro diagnose <run>` shows:

- `Cost:` the run's total, as for any run;
- `Route:` the spend by route (`openrouter`; `unattributed` may include late Claude spend);
- `Reported:` what the provider said the calls cost, beside what was charged (that the response carries a cost at all is assumed until Check 25). The table price settles; a large, lasting gap means a wrong `model_prices` entry, or a fallback you did not pin: fix the price, or the account's provider settings;
- `Warning:` the unverified-price warning, until you set the real price.

`result.json` has `cost.model_by`, `cost.route_by` and `cost.reported_usd`; the runner's `model call` log lines carry `route`, `serving_model`, `priced_as`, `settled` and `reported_micros`. `serving_model` different from the pin means OpenRouter served something else (that the response names the serving model is assumed until Check 25).

## 7. Known limits

- `fugaro report --by route` does not exist yet (the history has no route split); `--by model` works only with spend history on.
- The provider key is in the runner's memory (the D2 residual of the gateway): use the per-project credit limit. Besides `/proc/<runner>/environ`, a prompt-injected agent can reach the job service account's metadata token, and that account has `secretAccessor` on the mounted provider secret, so it can read the key itself. The credit limit on the key is the real backstop; the gateway's pins and caps do not bound a key the agent holds.
- A provider route passes only these top-level body fields (`routedKeys` in `internal/gateway/forward.go`): `model`, `max_tokens`, `messages`, `system`, `stream`, `tools`, `tool_choice`, `temperature`, `top_p`, `top_k`, `stop_sequences`, `metadata`, `thinking`, `output_config`. `metadata` may hold only `user_id` (Claude Code sends `metadata.user_id`, a device-hash identifier, on every request and the gateway never rewrites a body, so the provider sees it; the impact is low). Any other field, or any other key inside `metadata`, is refused as a violation, because it could bill outside token pricing or override the account's data policy: OpenRouter's `plugins`, `provider`, `models`, `route`, `transforms`, `usage`, `web_search_options`, and also Messages API fields the budget doesn't price on this path (`context_management`, a top-level `cache_control`, `output_format`, `speed`, `service_tier`, `inference_geo`, `container`, `mcp_servers`). Whether a request that Claude Code builds for a provider model carries one of these is recorded by Check 25; the list is widened only on that live evidence.
- Claude Code features that a compatible endpoint may not support (extended thinking, beta headers, server tools) surface as the upstream's error and fail the stage; which of them break is assumed until Check 25 records it. The same holds for token counting: the local 404 for a provider model is a design choice, and how OpenRouter's `count_tokens` answers is assumed until Check 25.
