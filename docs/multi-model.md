# Running a non-Anthropic model (M10)

A run's coder can be a model served by OpenRouter (the first one is `deepseek/deepseek-v4-flash`) while Claude Code stays the harness and Claude reviews. Fugaro sends the model traffic through its own gateway, which picks the upstream by the request's model ID, attaches the provider's key (the agent never holds it) and prices every call. Nothing is translated: OpenRouter's Anthropic-compatible endpoint receives the same Messages request Claude Code sends (assumed until Check 25: that the endpoint exists at that path, accepts `Authorization: Bearer` and answers in the Anthropic stream format). The design and its open assumptions are in [design/m10-multi-model.md](design/m10-multi-model.md); the live check that settles them is Check 25 of [gcp-live-checklist.md](gcp-live-checklist.md).

This is an experiment, not a default: `agent.models` stays Claude unless a repository names a provider model, and a cheaper run is never "more ready" than a Claude one, because the Claude review alone decides readiness. The provider traffic goes through the same gateway as Claude's, so pinning, the dollar caps and the kill switches hold. The **owner** enables it, in the project's local config, because it sends a repository's code to a third party; a repository's `fugaro.yaml` cannot add a provider, a URL or a price, only name a model the owner has opened to it. Treat the first runs as measurements: the cost to reach a Claude-approved pull request is the number that matters.

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
    require_zdr: true                 # default true: price the endpoints that hold no data
    price_policy: cheapest            # cheapest (default) | highest: which end of that tier's range to charge
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

Then run `fugaro init --repo` in each repository that is listed, so its job carries the provider list and mounts the key's variable. A repository not in `allow_data_to` gets neither, and a run on it that names a provider model is refused. Removing a repository from `allow_data_to` takes effect only after `fugaro init --repo` is run again for it (the list and the key's mount live in the job's configuration until then). **A version upgrade does not refresh this copy**: `fugaro upgrade`'s cloud step compares the deployed job's whole copy of the local project config (`providers`, `model_prices`, budget settings, the region's compute price override and the GitHub App ID) with what the local config would write now and, when they differ, prints a note naming the fields and this same command; it never runs `fugaro init --repo` itself, so its Terraform confirmations stay yours. `fugaro upgrade --check` makes no cloud call, so it cannot tell and says so instead. It checks each workflow's default-account job; a named-account job ([design/named-oauth-tokens.md](design/named-oauth-tokens.md)) is updated by the same `fugaro init --repo`, not checked separately.

**Provider models require `budget.mode: enforce`.** Provider calls are real dollars and `observe` never refuses a call, so a run (and `fugaro validate`) that names a provider model under `observe` or `off` is refused, naming the rule. Set `budget.mode: enforce` and `budget.per_run_usd` in the local project config and run `fugaro init --repo`; `fugaro budget set --global --mode enforce` (which needs the global `--daily` and `--per-run` caps, and asks you to type the project's name to lower it again) makes the shared database enforce too.

**`require_zdr` and `price_policy` price an OpenRouter route from Fugaro's built-in catalog** (generated from OpenRouter's public API; `fugaro budget prices` shows each row's tier and bound, and `fugaro doctor` says how old the catalog is). A model OpenRouter serves at several endpoints has a price *range*, not one price: `require_zdr: true` (the default) prices the endpoints that hold no data, `false` prices every endpoint the model is served by, and `price_policy` picks the end of that set's range — `cheapest` (the default) or `highest`. Neither switch chooses the endpoint OpenRouter serves the request from, so a call can still be billed at the other end of the range: `price_policy: highest` is the honest setting when you would rather over-count than under-count, and it is one of the two answers `fugaro diagnose` gives when the provider bills more than the charge (the other is a wrong `model_prices`). `require_zdr` is a price, not an enforcement — Fugaro cannot read your account's data policy, which is where zero-data-retention is actually decided (§1), and `fugaro doctor` says so. A model the catalog does not price, and every route that is not OpenRouter, is priced by `model_prices`; a `require_zdr: true` route on a model with no ZDR-eligible endpoint is refused rather than priced at the retaining endpoints. **Both fields price a tier, so an under-charge is possible, and an under-charge is dollars, not only accuracy**: the caps and the ledger are computed from the charge, so it makes a cap bind later than the ledger claims (the account-side credit limit of §1 is the only hard bound). Changing either takes effect on the next run only after `fugaro init --repo` is run again, like every other provider field.

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

The catalog recipes `standard` (a first-line loop by the coder's model, then one senior round) and `solo` (the coder's model also reviews) package the common shapes; see [recipes.md](recipes.md).

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

Provider models are real dollars: the charge is the table price of the reported usage plus the route fee. **What is measured:** on a streamed call the gateway merges the usage every `message_start` and `message_delta` event reports (the largest each field ever claimed; cumulative counts never lower a call's charge) and decides whether the call settles from that usage only once the stream ends (`message_stop`, or a clean end of a non-streaming body) — not from `message_start` alone. A provider may report an all-zero usage at `message_start` and put the real, cumulative counts only in `message_delta` (OpenRouter routing to `deepseek/deepseek-v4.1-flash`, served by DeepInfra, did exactly this in a captured run: `message_start`'s `usage.input_tokens` was `0` with every cache field `null`, and `message_delta` carried `input_tokens:32`, `cache_read_input_tokens:0`, `output_tokens:47`); the gateway reads that as 32 input and 47 output tokens, not as a reply that said nothing. Zero input alongside a real cache read (heavy caching) is the same: priced from whatever was reported, never read as silence — **but only once `input_tokens` has been reported at all, even as an explicit `0`.** A usage object that never includes the `input_tokens` key in any event, OWNER RULING, has left input out entirely, not merely reported it as zero: that settles at the reservation regardless of a real cache read elsewhere in the same usage, the same as main did (a round of this PR briefly read a cache read alone as enough, with or without the key; that reading was reverted). Zero input (and no cache, or the key never reported) alongside a real output count is the general case: a real request always sends some input, so a provider whose usage never names one, cache included, simply left the field out, and the call settles at the reservation like any other missing field, whatever output it produced.

**What is estimated, and the fail-safe:** when the merged usage never names an input, cache read or cache write count at all — a real request always sends some input, so a provider whose usage is silent on all three left the field out, whatever output (or text) the response held — the call is charged its full reservation instead (`usage_unparsed` in the run's report, a WARN log line per call). So is any call whose usage the gateway could not read at all: a response with no usage event, one with a negative count (never trusted, whatever a later event claims), a `message_delta` whose usage didn't parse, or a stream that broke before it finished (partial settlement then charges the reported input and cache plus the reserved output — see `TestPartialSettlementCarriesFee`, `TestCutProviderStreamChargesPartial`). The WARN line (`budget: usage unparsed`) names one `reason`, from a closed set, and the event type names seen, never the provider's own text:

| reason | meaning |
|---|---|
| `no_usage_event` | no event (or body) ever carried a usage object |
| `input_zero_no_cache` | usage was read, but input and every cache field were zero or missing: a real request always sends some input, so the provider left it out |
| `negative_count` | a reported count was below zero |
| `malformed_usage` | a `message_start` or a `message_delta` carried a usage object the gateway could not parse |
| `stream_incomplete` | the stream never reached `message_stop` (and never said enough to partially settle either) |
| `no_output_count` | input (or cache) was real, but no `message_delta` ever named an output count |
| `not_readable` | the response body's encoding isn't one the gateway decodes |

**This also touches the Claude route, as a side effect, not a redesign of it.** The negative-count and malformed-usage guards above (and the `budget: usage unparsed` WARN line itself) live in code both routes share, so two Claude-route outcomes changed along with the provider-route fix: a `message_delta` the gateway can't parse, or one with a negative count, arriving *after* `message_stop` (the call already read as complete) now settles at the reservation on the Claude route too, where it previously settled at usage from whatever the stream said before that event — stricter for the malformed case, and a fix for an existing undercharge for the negative one (the negative count was floored to zero by `usage()`, the same bug the provider-route fix closes). "Can't parse" is the same unparseable-usage path whether the cause is a JSON type mismatch, a `message_delta` whose `usage` is `null` or missing the key entirely, or an event whose own name doesn't match `message_delta` at all (silently skipped, so nothing merges and the call is left exactly as it was before that event) — all three read the same way once the gateway can't trust what follows `message_stop`. `TestClaudeMalformedDeltaAfterStopChargesReservation` and `TestClaudeNegativeDeltaAfterStopChargesReservation` (`internal/gateway/ledger_test.go`) pin the two outcomes above. There is a third Claude-route difference: a negative count in ANY `message_start` (a duplicate or late one included) now marks the call broken on both routes, so it settles at the reservation (main let a later valid duplicate `message_start` overwrite the negative one and settled at usage). These three are the **only** Claude-route differences from main, and each is stricter: the call settles at the reservation where main settled at usage. The reservation is a strict upper bound on every allowed shape, so the only case where it is LOWER than what main charged is a report impossible for the request (a usage above the call's own worst case, e.g. output 1e11 with `max_tokens` 1000): the review's 259 such Claude-route probes and 691 such provider-route probes are all of that kind. An unreadable `message_start` is a provider-route-only fix (`TestClaudeMalformedMessageStartChargesReservation` pins that the Claude route is unaffected — `message_delta` is already gated on `started`, which an unreadable `message_start` never sets, so main's behavior holds either way), and the `input_tokens`-key ruling above applies to both routes identically (the Claude route never read a bare cache read as enough in the first place). Otherwise the Claude route is unchanged: same counts, same rates, same settlement for every shape it saw before. The one purely cosmetic change is that the WARN line can now fire for a Claude call too, whenever one settles unparsed (which could already happen, e.g. `TestIncompleteUsageChargesReservation`'s cases) — a new log line, never a new charge.

When a response carries no cache fields at all, all input is charged at the full input rate (never split into a cheaper cache rate it didn't report). Both this and the fail-safe above err high, never low, **relative to the true cost of the call** — not relative to what a more conservative earlier version of this code would have charged for the same response. Read naively, "never low" could look broken: reading usage from a shape the gateway used to fall back to the reservation for (zero input with a delta's real counts; a usage-bearing delta that arrives before `message_start`; input split across more than one delta; data-only SSE lines with no `event:` name; `message_start` with no `usage` object at all, followed by a delta that has one; `cache_creation_input_tokens` alone with input 0; a non-streaming body with `input_tokens` explicitly `0` and a real cache read; a stream with no `message_start` at all (text, then a delta carrying usage, then stop); a `message_start` whose usage lacks the `input_tokens` key or has it `null`, followed by a delta that carries the key; a `message_delta` after `message_stop` that names real input when the start had no usage (settles **partial**: reported input plus the reserved output); a stream cut after a delta that named real input, which settles **partial** — reported input plus the *reserved* output, not the full reservation) always charges *less* than the reservation fallback did, re-confirmed by running all eight (plus the two reverted shapes below) against both this branch and `main` directly. Every one of those is the fix working as intended: the reservation was never the true cost, only the safe upper bound the gateway fell back to when it could not read the usage; once it can, it charges what was actually reported (or, for a cut stream, the reported input and cache plus the output's own safe upper bound), which is always at or below the reservation, never below the true cost itself.

Two shapes a prior round of this fix also charged less than main for are reverted, by design, to match main exactly rather than charge less: a `message_start` whose usage object fails to unmarshal (a JSON type mismatch, not a missing field) now sets the same broken flag an unreadable `message_delta` already did, so a later delta can no longer settle the call from whatever it alone reported; and a usage object — in any event, stream or whole body — that never includes the `input_tokens` key at all, however real a cache read elsewhere in it, settles at the reservation, not from the cache read alone (the explicit `input_tokens: 0` case above is the one that still settles from usage; the key's total absence does not). Both are confirmed, probe by probe (a matrix of over 114,000 cases per run of the review), to charge at least what main did, except where main charged a usage above the reservation (an impossible report): there the reservation is charged instead, which is still at or above the true cost of any real call. Whether OpenRouter's responses carry usage and cache fields in the general case (beyond the one captured run above) is assumed until Check 25.

`fugaro diagnose <run>` shows:

- `Cost:` the run's total, as for any run;
- `Route:` the spend by route (`openrouter`; `unattributed` may include late Claude spend);
- `Reported:` what the provider said the calls cost, beside what was charged (that the response carries a cost at all is assumed until Check 25). The table price settles; a large, lasting gap means a wrong `model_prices` entry, or a fallback you did not pin: fix the price, or the account's provider settings;
- `Warning:` the unverified-price warning, until you set the real price;
- a `usage_unparsed` count above zero: some calls settled at their reservation instead of their usage; the run's log carries the reason and event types for each.

`result.json` has `cost.model_by`, `cost.route_by`, `cost.reported_usd` and `cost.usage_unparsed`, and each entry of its `stages` array carries `model_usd` and `model` too: what that stage's calls cost (the gateway's figure when the gateway ran, else the result event's own `total_cost_usd`) and the model the stage was pinned to, so spend can be read per stage rather than only per run. `model_usd` is the stage's whole spend, the background model's calls included; `model` is what the config pinned, never what the upstream served, so it does not account for `model_usd` on its own — a stage that also called the background model, or one the upstream served under another name, is only fully explained by the run's own `cost.model_by` and the `model call` log lines below. The one precise relation, to the micro-dollar, is

```
sum(stages[].model_usd) + cost.model_by["unattributed"] == cost.model_usd
```

— a gateway call that settles after its stage's wait ran out is counted in `cost.model_usd` and in `cost.model_by`'s `unattributed` only, in no stage, which is the whole of the difference. Records written before 0.8.0 have neither field. The runner's `model call` log lines carry `route`, `serving_model`, `priced_as`, `settled` and `reported_micros`. `serving_model` different from the pin means OpenRouter served something else (that the response names the serving model is assumed until Check 25).

**On the next DeepSeek run, check `fugaro diagnose <run>` for two different things.** First, `usage_unparsed` should read `0`; if it is still above zero, the log's `reason` and `events` on the WARN line say why (the real SSE shape above was captured from one live run, not exhaustively, so it may be a shape the parser doesn't yet tolerate, not necessarily the one fixed here). Second, and separately, **the cost itself will be wrong unless `model_prices` names the served model.** The captured run's `message_start` named `deepseek/deepseek-v4.1-flash`, which the embedded table has no row for (it only has the placeholder `deepseek/deepseek-v4-flash`, a different model ID — see §2); a served model the table doesn't know prices at the table's **maximum** rates, about 100x too high for a model this cheap (`priced_as: max` in the log, a warning naming both models). The owner must add a `model_prices` entry for whatever model is actually served, not only the one pinned: prices are provider-specific even for the same model (at the time of writing, Together/Fireworks/Parasail price `deepseek-v4.1-flash` around $0.30 / $1.20 per M input/output tokens, DeepInfra around $0.14 / $0.42) — verify the real number on the provider's own model page before setting it, the figures here are not a quote.

## 7. Known limits

- `fugaro report --by route` does not exist yet (the history has no route split); `--by model` works only with spend history on, and shows a subscription model's dollars under NOTIONAL~, never MODEL $.
- The provider key is in the runner's memory (the D2 residual of the gateway): use the per-project credit limit. Besides `/proc/<runner>/environ`, a prompt-injected agent can reach the job service account's metadata token, and that account has `secretAccessor` on the mounted provider secret, so it can read the key itself. The credit limit on the key is the real backstop; the gateway's pins and caps do not bound a key the agent holds.
- A provider route passes only these top-level body fields (`routedKeys` in `internal/gateway/forward.go`): `model`, `max_tokens`, `messages`, `system`, `stream`, `tools`, `tool_choice`, `temperature`, `top_p`, `top_k`, `stop_sequences`, `metadata`, `thinking`, `output_config`. `metadata` may hold only `user_id` (Claude Code sends `metadata.user_id`, a device-hash identifier, on every request and the gateway never rewrites a body, so the provider sees it; the impact is low). Any other field, or any other key inside `metadata`, is refused as a violation, because it could bill outside token pricing or override the account's data policy: OpenRouter's `plugins`, `provider`, `models`, `route`, `transforms`, `usage`, `web_search_options`, and also Messages API fields the budget doesn't price on this path (`context_management`, a top-level `cache_control`, `output_format`, `speed`, `service_tier`, `inference_geo`, `container`, `mcp_servers`). Whether a request that Claude Code builds for a provider model carries one of these is recorded by Check 25; the list is widened only on that live evidence.
- Claude Code features that a compatible endpoint may not support (extended thinking, beta headers, server tools) surface as the upstream's error and fail the stage; which of them break is assumed until Check 25 records it. The same holds for token counting: the local 404 for a provider model is a design choice, and how OpenRouter's `count_tokens` answers is assumed until Check 25.
- **`context_management` is refused before any provider ever saw a request (sandbox run `edgeappinc-fugarosandbox-27be4c45d773e3b9/20261013-013356-9966`, $0 cost).** Measured locally against the real Claude Code **2.1.296** pointed at a recording localhost server (a dummy key, a throwaway config dir, no cloud call): for a provider model, Claude Code's first request carries top-level `context_management` (`{"edits":[{"type":"clear_thinking_20251015","keep":"all"}]}`) whenever `thinking` (`{"type":"adaptive","display":"omitted"}`) is on, which it is by default; the session's first request also carries a top-level `safeguards` key. Neither is in `routedKeys`, so the gateway's existing refusal (above) caught it exactly as designed — nothing about that guard changed. The base image here pins Claude Code **2.1.283** (`images/base/Dockerfile`), not the 2.1.296 measured; the shapes below are assumed to carry over but were not re-measured against 2.1.283.

  The fix: the runner now sets `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1` and `MAX_THINKING_TOKENS=0` in the agent's environment for any stage whose role model (coder/reviewer) or background model is a provider's (`agent.ProviderRouteVars`, applied in `stagePins`, `internal/runner/agentenv.go`). Measured effect of each knob alone and together:

  | Setting | Effect on the request |
  |---|---|
  | `MAX_THINKING_TOKENS=0` | Drops `thinking` **and** `context_management`; `safeguards` stays on the first request. |
  | `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1` | Drops `context_management` and `safeguards`; `thinking` stays as `{"type":"adaptive"}` (itself a `routedKeys` field, so allowed alone). |
  | `CLAUDE_CODE_DISABLE_ADAPTIVE_THINKING=1` | No measured effect on the body. |
  | Both of the first two set | The body is exactly `{max_tokens, messages, metadata:{user_id}, model, output_config:{effort:"high"}, stream, system (3 text blocks, two with block-level cache_control), tools (20 custom tools)}` with `anthropic-beta: claude-code-20250219,interleaved-thinking-2025-05-14,mid-conversation-system-2026-04-07,effort-2025-11-24` and an `x-api-key` header — every key of it is in `routedKeys`. An all-Claude stage gets neither variable: its request is byte-identical to before this fix. |

  An all-Claude stage never sees either variable (`internal/agent/settings_test.go`'s `TestProviderRouteVars` and `internal/runner/agentenv_internal_test.go`'s `TestStagePinsProviderRouteVars` cover both directions, and a mixed run with one stage of each kind; `internal/runner/provider_run_test.go`'s `TestProviderStageDisablesThinkingVars` covers it end to end).

  **OWNER RULING: a stage counts as "provider" if any model its Claude Code process can call does, not only its own role model.** With a provider coder and no `agent.models.background` set, `config.EffectiveBackground` (§4) defaults the background model to the coder's own provider model, for every stage, including a Claude reviewer's: that reviewer's own Claude Code process still makes background calls on the provider route, and those would be refused the same way without the two variables. So **a Claude reviewer whose background model defaults to the provider coder runs without thinking; pin `agent.models.background` to a Claude model to keep it** on the reviewer. This is deliberate, not a bug: the gateway refuses the field on any request on a provider route, whichever role made it, so the variables have to follow every model a stage's process can reach, not just its own pin. `internal/runner/agentenv_internal_test.go`'s `TestStagePinsProviderRouteVars` covers both the default (background unset: the reviewer stage gets the variables too) and the pinned-away case (background explicitly Claude: it does not).

  **Open question for Check 25, not yet settled:** with both variables set, Claude Code's `messages` array can carry an item with `"role":"system"` (a mid-conversation system message, a Claude Code beta turned on by the `anthropic-beta` header above). The gateway does not validate a message's `role` at all, so it is forwarded unchanged (`TestRoutedAcceptsNoThinkingClaudeCodeShape`, `internal/gateway/route_test.go`) — this is not a gap being left open, it is simply untouched by this fix. Whether **OpenRouter's compatible endpoint** accepts a `role: "system"` message mid-conversation (rather than only as the top-level `system` field) is unverified: no live call has been made. Check 25 step 4 must record what the endpoint does with it; nothing here assumes an answer either way.
  - The two variables only reach Claude Code's environment and the managed settings file's `env`; they say nothing about a setting outside `env`, such as a repository's or the agent's own `.claude/settings.json` carrying `"alwaysThinkingEnabled": true`. Whether that setting overrides `MAX_THINKING_TOKENS=0` (or interacts with it some other way) was not measured. If it does turn thinking back on, the request grows `context_management` again, and the gateway's existing allowlist refuses it (above): the call fails with no spend, the same `field "context_management" is not allowed on a provider route` error as before this fix, not a silent bypass. This fails closed, not open; nobody has confirmed whether `alwaysThinkingEnabled` even survives next to a provider-model pin, since `checkSettingsRouting` (`internal/runner/agentenv.go`) does not refuse it (it is not a routing key).

## Direct to the vendor

A model family does not have to go through OpenRouter. The same `providers:` block (§2) accepts a second entry pointed straight at the vendor's own Anthropic-compatible endpoint instead:

```yaml
providers:
  openrouter:
    kind: anthropic-compat
    base_url: https://openrouter.ai/api
    auth: bearer
    secret: openrouter-api-key
    models: ["qwen/*"]
    allow_data_to: [edgeappinc/fugarosandbox]
  deepseek:
    kind: anthropic-compat
    base_url: https://api.deepseek.com/anthropic   # the vendor's own endpoint, not openrouter.ai
    auth: bearer
    secret: deepseek-api-key
    models: ["deepseek/*"]
    allow_data_to: [edgeappinc/fugarosandbox]
```

There is no separate "direct" knob: the `providers:` map is the only knob there is. The provider entry that claims a model is its route: to switch a model family between OpenRouter and its vendor, move its pattern from one entry to the other and run `fugaro init --repo`. Providers may not overlap (`fugaro validate` refuses two entries claiming the same model), so a family belongs to exactly one route at a time, and Claude models never go through a provider: `claude-*` and `anthropic/*` are unclaimable (§2), and always go direct to Anthropic regardless of what `providers:` lists.

The account-side settings of §1 (no fallbacks, the data policy) are OpenRouter's own; a vendor's own endpoint has its own account-side settings to review, which are not enumerated here.

**Unverified until Check 33:** that a vendor's own Anthropic-compatible endpoint (DeepSeek's, Moonshot's) accepts the gateway's forwarded request bodies byte for byte and reports usage the way the gateway reads it. Check 25 (§1) settles this for OpenRouter only; Check 33 of [gcp-live-checklist.md](gcp-live-checklist.md) is the equivalent for a vendor's own endpoint, and is optional and user-run.
