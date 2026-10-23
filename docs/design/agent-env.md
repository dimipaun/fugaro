# A generic, safe way to set the agent's environment

Status: design for review (2026-10-13, revised 2026-10-13 after `next`
caught up to `main`'s 0.6.6), docs only — nothing in this design has been
built. Source: the owner's request, after the first live provider-model run
(`docs/multi-model.md`, Check 25) failed because Claude Code sends
`context_management` when thinking is on and the gateway refuses it on a
provider route: the field is not in `routedKeys`, the allow-list of
top-level fields a provider-routed request may carry
(`internal/gateway/forward.go:39-49`, checked at `forward.go:102-106` and
enforced only for provider routes at `forward.go:460-461`; the narrower,
Anthropic-direct `context_management`-edit filter, `contextManagement`,
`forward.go:224-253`, is a related but separate check — see §0). The
docs record of this failure and its fix is `docs/multi-model.md` §7
("Known limits"), not §5 as an earlier draft of this design said.

**This fix has since shipped, and this revision is rebased on it.** PR #305
("provider models: Claude Code gets thinking and experimental betas
disabled on provider-route stages") added `agent.ProviderRouteVars()`
(`internal/agent/env.go:219-237`) and wired it into `stagePins`
(`internal/runner/agentenv.go:48-66`) via `stageOnProviderRoute`
(`agentenv.go:68-78`); `main` released it as 0.6.6 and `next` has it as of
`a029193` ("forward-merge main 0.6.6 into next..."), merged into this
branch below. **Nothing about this design's goal changes**: `fugaro.yaml`
still has no way to set an agent environment variable at all
(`workflows.<n>.secrets[].env` only *names* a workflow secret's variable,
from a deny-list of dangerous names, `internal/config/validate.go:60-80`,
not an allow-list of anything Claude Code reads), and `providers.<name>`
still has no way for the owner to override or extend what the runner sets
for a provider route — `ProviderRouteVars()`'s two variables are hard-coded
in Go, not configurable. **What changes is the shape of §3**: it no longer
proposes a new, parallel `DefaultProviderAgentEnv` mechanism; it proposes
*generalizing the mechanism that already shipped* — `agent.ProviderRouteVars()`
becomes the seed of an owner-overridable default, computed in the same
place (`stagePins`) that already computes it today, so one result keeps
reaching both channels `stagePins`'s caller already sends it to (§0). The
owner asked whether to add a generic mechanism and approved queuing this
design; this revision answers with the real shipped code in view instead of
inventing a parallel one. Everything below is read from the code at this
branch's base (after merging `origin/next`) and cited by file; nothing was
run.

**A second, independent precedent for the same gap.** `docs/gcp-setup.md:257`
already tells an operator: "If Claude Code's tool search is on for a
repository with many MCP tools it sends a server tool too: set
`ENABLE_TOOL_SEARCH=false` **in the agent's environment**." There is no way
to do that today. `ENABLE_TOOL_SEARCH` is not in `baseEnv`
(`internal/agent/env.go:15-25`), not a workflow secret (secrets are for
credentials, and `ReservedEnv` would refuse most of the routing-adjacent
names anyway), and not settable any other way. The docs already assume a
mechanism this design is the first attempt to build.

## 0. What exists today

- **`internal/agent/env.go`'s `BuildEnv`** builds the agent's whole
  environment from the runner's own (`parent`), allow-listing a fixed
  `baseEnv` (PATH, locale, proxies, a handful of build-tool variables),
  copying declared workflow secrets, adding the auth variables (vertex,
  api-key, oauth), then `spec.Set` (Fugaro's own, e.g. `FUGARO_STATE_DIR`),
  and — **last, so nothing declared or inherited can override it**
  (`env.go:133-140`, the comment says so explicitly) — the gateway's
  routing variables when the gateway is on. This ordering is the one
  invariant every design below must not weaken. `agent.auth` has a fourth
  value, `cascade` ([credentials-service](../credentials-service/spec.md)):
  it adds no credential variable of its own — the credentials service
  carries that run's credential — and `BuildEnv`'s arm for it is that
  work's Task C1.19, so a `cascade` run is refused here today with
  `unknown agent auth`.
- **Per-stage variables (model pins, and now the provider-route fix) go
  into both the process environment and the managed settings file — not
  `BuildEnv`.** `stagePins` (`internal/runner/agentenv.go:48-66`) computes
  one `map[string]string` per stage: `agent.PinVars` (the role's model,
  background model, output limit) plus, when `stageOnProviderRoute(model,
  bg)` (`agentenv.go:68-78`) is true, `agent.ProviderRouteVars()`
  (`internal/agent/env.go:219-237`:
  `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1`, `MAX_THINKING_TOKENS=0`).
  **That one result, `pins`, reaches two places**, both in
  `internal/runner/runner.go`'s per-stage code: `r.writeManagedSettings(pins)`
  merges it into the managed settings file's `env`
  (`agentenv.go:85-104`, the `for k, v := range pins { vars[k] = v }` loop),
  and `req.Env = gitops.WithVars(r.env, pins)` (`runner.go:1574`) merges the
  *same* `pins` into the Claude Code process's own environment for that
  stage — `pins` is the final write in both channels, but each starts from
  a *different* base: the process channel's base is `r.env` (`BuildEnv`'s
  bootstrap-time result, built once, `runner.go:1084-1091`), while the
  managed-settings channel's base is `writeManagedSettings`'s own, freshly
  computed `agent.GatewayVars(...)` result (from `r.parentEnv()`, the
  runner's raw environment — never `r.env`), recomputed every stage. §3's
  precedence discussion covers exactly why this difference doesn't let
  anything leak between the two. `stageOnProviderRoute` checks **both** the
  stage's own
  role model and the run's effective background model
  (`config.EffectiveBackground`, `internal/config/providers.go:377-385`),
  because with no `agent.models.background` set, the background model
  defaults to the coder's own provider model for every stage, including a
  Claude reviewer's — documented as the **OWNER RULING** in
  `docs/multi-model.md` §7: "a stage counts as 'provider' if any model its
  Claude Code process can call does, not only its own role model." A
  design that forgets this (keys only the role model) silently drops the
  fix on every reviewer stage whose background defaults to the provider.
  The managed settings file is Claude Code's highest-precedence settings
  source (`internal/agent/settings.go:11-14`: "neither a repository's nor a
  user's settings file can override them") and exists **only when the
  gateway is on** (`writeManagedSettings` returns nil with no gateway,
  `agentenv.go:85-89`) — but `stagePins` itself already returns `nil` with
  no gateway too (`agentenv.go:54-57`), so the process-env channel is
  equally empty in that case; there is no asymmetry between the two
  channels to design around.
- **`checkSettingsRouting`** (`internal/runner/agentenv.go:123-179`) refuses
  any *committed* settings file (`.claude/settings.json`, the user's,
  `$CLAUDE_CONFIG_DIR`'s) that sets a routing-capable key: `apiKeyHelper`,
  or an `env` block naming one of `routingEnvKeys` (`ANTHROPIC_BASE_URL`,
  `ANTHROPIC_VERTEX_BASE_URL`, `ANTHROPIC_BEDROCK_BASE_URL`,
  `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_SKIP_VERTEX_AUTH`,
  the proxy variables) or any `CLAUDE_CODE_USE_*` name
  (`internal/agent/settings.go:95-102, 131`). This is a **different**
  channel from the one this design adds — it polices a file Claude Code
  reads from disk, not a `fugaro.yaml` key the runner turns into
  environment or managed-settings entries — but any new allow-list this
  design creates must never contain a name `RoutingKeys` would flag (§1).
- **`ReservedEnv`, `ReservedEnvPrefixes`, `ReservedEnvNames`**
  (`internal/config/validate.go:60-87`) are a **deny-list** for workflow
  secrets' variable names (`workflows.*.secrets[].env`): a secret may use
  any name *except* these. They exist to stop a workflow secret from
  shadowing something that changes process behavior (`LD_PRELOAD`, a
  proxy, `GODEBUG`) — the opposite shape from what this design needs,
  which is a small allow-list of names Claude Code itself reads, most of
  which (`CLAUDE_CODE_*`, `ANTHROPIC_*`) are **inside** `ReservedEnvPrefixes`
  today, correctly, for secrets.
- **Provider keys** (`internal/config/providers.go:167-186`) are mounted
  into the runner's own process as `FUGARO_PROVIDER_KEY_<NAME>`, under a
  prefix `IsProviderKeyEnv` recognizes and `BuildEnv` never allow-lists; the
  agent's environment never holds one. **This design changes nothing about
  how a provider's key reaches the agent.**
- **`providers.<name>`** (`internal/config/providers.go:29-47`) is owner-only,
  the local config's `~/.config/fugaro/projects/<project>.yaml`, never
  `fugaro.yaml` (`docs/multi-model.md` §2). It reaches a job as
  `FUGARO_MODEL_PROVIDERS`, compact JSON, written by `fugaro init --repo`
  and decoded at runner bootstrap by `ProvidersFromEnv`
  (`internal/runner/providers.go:14-78`), which already strict-decodes
  (`dec.DisallowUnknownFields()`) and re-validates
  (`config.ValidateModelProviders`) — the channel a provider-scoped variant
  (§3) would reuse.
- **Check commands run as the agent's own children, not the runner's.**
  `fugaro verify` writes settings the `fugaro verify build`/`test`
  subcommands read (`internal/runner/runner.go:1004-1010`), and those
  subcommands are what Claude Code's Bash tool invokes — so
  `commands.build`/`commands.test` inherit **exactly** `r.env`, the same
  allow-listed environment the agent process itself got from `BuildEnv`
  (confirmed by `SECURITY.md:52`: "Commands run through `fugaro verify` see
  the agent's full environment" — "full" meaning the agent's own, not the
  runner's unfiltered one). This matters for §4: there is no separate
  "check-command environment" to design; it is the same environment as the
  agent's.
- **`mise.toml`'s `[env]`** (this repository's own, `mise.toml:9-10`, and
  `docs/design/base-image.md:65,164`) already lets a repository set
  environment for its own build tools, but only for processes `mise`
  activates (a shell hook, or `mise exec`) — **never** for the Claude Code
  process itself, which `mise` does not wrap. `base-image.md:164` says so
  plainly: "The agent's environment is an allow-list
  (`internal/agent/env.go`); none of these is added to it by this design."
  This design is the complement `base-image.md` deliberately left out: a
  way to reach the *agent's* environment, not the repository's tools.

## 1. Threat model

`fugaro.yaml` is repository-chosen and PR-reviewed by people (`SECURITY.md`
§"The assumption"); the agent runs with its permission prompts bypassed; the
budget, the real model key, and git credentials are what Fugaro's design
protects (`SECURITY.md` §"What limits the blast radius today").

**An honest accounting of what is already possible today, before weighing
what an open `agent.env:` map would add.** A committed `.claude/settings.json`
can *already* set an `env` block with any name `checkSettingsRouting`
doesn't flag — and it flags only routing names (`routingEnvKeys`/
`CLAUDE_CODE_USE_*`, §0), not `LD_PRELOAD`, a shell prefix, or any
telemetry/OTEL knob. That file is committed, repository-chosen, PR-reviewed
code, the same trust level as `fugaro.yaml` — no compromise needed. An
earlier draft of this design claimed an open `agent.env:` map "would let a
repository's own reviewed file do what today only a compromised agent
reading `/proc` can do"; that overstates the new exposure for every
non-routing name, which a reviewed `.claude/settings.json` can already set.
**What an open `agent.env:` map would newly add, specifically, is reach:**
it would be the first *generic* mechanism that writes into `r.env`, which
is not only the agent's own environment but also what the runner's own
`commands.lint`/`commands.fix` gate step runs with directly
(`internal/runner/gate.go:151,235,361`, each `Env: r.env`) — a surface a
`.claude/settings.json`'s `env` block cannot reach at all, since that file
only configures the `claude` binary's own settings, never the runner's
separate shell-outs for a repository's own lint/fix commands. **And for the
*routing* names specifically** (the table below), an open map would add a
reviewed path to something `checkSettingsRouting` does actively refuse
today, which is the one place this design must not weaken (this design
must not add a *reviewed, first-class* path around that specific guard):

| Must be impossible | Enforcement |
|---|---|
| Redirect model traffic around the gateway (`ANTHROPIC_BASE_URL`, `ANTHROPIC_VERTEX_BASE_URL`, `ANTHROPIC_BEDROCK_BASE_URL`, `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_SKIP_VERTEX_AUTH`, any `CLAUDE_CODE_USE_*`) | Closed allow-list (§2): these names are never on it. `BuildEnv` applies `agent.env`'s values **before** the gateway's own (§2, mirroring the existing `spec.Set` ordering, `env.go:133-140`), so even a bug that let one through would still be overwritten, not merely shadowed. A unit test (`TestAgentEnvAllowListNeverRoutes`, §2) asserts the allow-list and the routing names (`routingEnvKeys`/`CLAUDE_CODE_USE_*`) are disjoint — a narrower claim than disjointness from `ReservedEnvPrefixes`, since the allow-list's `CLAUDE_CODE_*` entries are *expected* to also be in `ReservedEnvPrefixes` (that list is the unrelated secrets deny-list, §0). |
| Proxy the agent's traffic (`HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY`, `NO_PROXY`, lower-case forms) | Same closed allow-list; these are also in `routingEnvKeys` and in `ReservedEnvPrefixes`/`ReservedEnvNames` already. Not on the allow-list, structurally. |
| Add a CA or certificate override (`NODE_EXTRA_CA_CERTS`, `SSL_CERT_FILE`, `SSL_CERT_DIR`, `CURL_CA_BUNDLE`, `GIT_SSL_CAINFO`) | Already `baseEnv` pass-through only (`env.go:21-22`), never agent-settable; not on the new allow-list either. |
| Preload or inject code (`LD_PRELOAD`, `LD_LIBRARY_PATH`, `DYLD_INSERT_LIBRARIES`, `NODE_OPTIONS`, `PYTHONSTARTUP`, `BASH_ENV`, `ENV`, `PERL5OPT`, `RUBYOPT`, …) | All already in `ReservedEnvPrefixes`/`ReservedEnvNames` (`validate.go:61-79`); not on the new allow-list. |
| Rewrite git's behavior (`GIT_*`) | `GIT_` is a reserved prefix already; `gitops.ModelCredentialVars` is stripped from every git call regardless (`internal/runner/agentenv.go:15-24`, `modelCredentialVars`/`strip`); not on the new allow-list. |
| Change `HOME`/`PATH`/the shell (`SHELL`, `IFS`, `BASH_ENV`, …) | Already `ReservedEnvNames`; `baseEnv` passes `HOME`/`PATH` through from the runner only, never settable by `fugaro.yaml`. |
| Impersonate Fugaro's own variables (`FUGARO_*`) | `FUGARO_` is a reserved prefix already; the new allow-list never contains one, and `spec.Set` (Fugaro's own `FUGARO_STATE_DIR` etc., `runner.go:1055-1058`) is applied **after** `agent.env`'s values in `BuildEnv`, so a collision resolves to Fugaro's own value, not the repository's. |

**`NeverAgentEnv`: a tripwire, separate from the allow-list, tested for
disjointness.** `TestAgentEnvAllowListNeverRoutes` (§2) only checks the
allow-list against `routingEnvKeys`/`CLAUDE_CODE_USE_*` — a narrow,
*routing*-specific list. Since `AgentEnvAllowed` is closed and every entry
is proposed individually (§2), nothing stops a future addition from being
some *other* Claude Code variable nobody thought to check against the
routing list because it isn't a routing concern at all (a credential
helper's TTL, a client certificate, a telemetry toggle). Recommendation: a
second list, `NeverAgentEnv` (exact names) and `NeverAgentEnvPrefixes`
(exact prefixes), matched case-sensitively — unlike `ReservedEnv`, which is
case-blind because *proxies* are read in either case (§0); nothing here is
a proxy, so `max_thinking_tokens` or `https_proxy` (lower-case) are simply
not on `AgentEnvAllowed` to begin with and need no case-folding to refuse —
and a test, `TestAgentEnvAllowListNeverHitsNeverList`, asserting
`AgentEnvAllowed` is disjoint from it. This is a second, independent
tripwire on every *future* addition, not a rule this design's own v1 list
needs to lean on (every v1 entry is already checked individually, §2).
**A deny-list is the opposite shape from the allow-list, and "unverified"
cuts the other way.** An earlier draft of this section withheld an
unconfirmed name from `NeverAgentEnv` until it could be checked against
Claude Code 2.1.283's real surface — the right caution for
`AgentEnvAllowed` (an unverified name let *in* could be actively dangerous)
but backwards for a *deny* list: a name added to `NeverAgentEnv` that turns
out not to be real costs nothing (it is simply inert — `TestAgentEnvAllowListNeverHitsNeverList`
already proves `AgentEnvAllowed` is disjoint from it, so an extra deny-list
entry can never accidentally refuse something the allow-list needs), while
a plausible, dangerous-if-real name left *out* is a gap a future
`AgentEnvAllowed` addition could fall into unnoticed. So: **"verify before
shipping" applies only to `AgentEnvAllowed`** (§2); `NeverAgentEnv` and
`NeverAgentEnvPrefixes` take every plausible name or family this review
and this repository's own code between them have named, confirmed or not,
because the cost of a wrong guess here is zero and the cost of a missing
one is not. Claude Code remains closed-source (`docs/design/base-image.md`
§0: "a closed, proprietary binary"), so "confirmed" below still means
"found in this repository's own code," not "confirmed against Anthropic's
own reference" — that distinction still matters for provenance, just not
for whether a name ships on this list:

```go
var NeverAgentEnv = []string{
    // routingEnvKeys and apiKeyHelper's env equivalents (settings.go) —
    // the routing names already excluded above, repeated here so
    // NeverAgentEnv alone is a complete tripwire without cross-referencing
    // routingEnvKeys's exact contents.
    "ANTHROPIC_BASE_URL", "ANTHROPIC_VERTEX_BASE_URL", "ANTHROPIC_BEDROCK_BASE_URL",
    "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_SKIP_VERTEX_AUTH",
    // Read directly from the runner's own environment by checkSettingsRouting
    // (agentenv.go:133, parent["CLAUDE_CONFIG_DIR"]): an agent.env entry of
    // this name would not do what a repository might expect (it already
    // isn't inherited into the agent's environment via baseEnv either) and
    // could confuse the routing check about which files it polices.
    "CLAUDE_CONFIG_DIR",
    // Claude Code's own per-call pins, which agent.PinVars already sets
    // per stage/role (env.go) — an agent.env entry would race the pins and
    // produce a confusing, order-dependent result.
    "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL",
    "CLAUDE_CODE_MAX_OUTPUT_TOKENS",
    // The oauth credential variable BuildEnv sets from a declared secret
    // (env.go) — never a plain value a repository names.
    "CLAUDE_CODE_OAUTH_TOKEN",
    // Vertex's own routing/identity variables (vertexEnv, env.go) and its
    // per-model region overrides.
    "CLOUD_ML_REGION", "ANTHROPIC_VERTEX_PROJECT_ID", "GOOGLE_CLOUD_PROJECT",
    // Plausible, not found in this repository's code — a deny-list costs
    // nothing to over-include (above). A credential-helper TTL, mid-session
    // config files and a shell-wrapping knob, none of which any workflow
    // should ever be tuning through agent.env even if real.
    "CLAUDE_ENV_FILE", "CLAUDE_CODE_SHELL_PREFIX", "CLAUDE_CODE_SHELL",
    "CLAUDE_CODE_ENABLE_TELEMETRY", "CLAUDE_CODE_API_KEY_HELPER_TTL_MS",
    "CLAUDE_CODE_EXTRA_BODY", "DISABLE_PROMPT_CACHING",
    "CLAUDE_CODE_CLIENT_CERT", "CLAUDE_CODE_CLIENT_KEY",
}
var NeverAgentEnvPrefixes = []string{
    "CLAUDE_CODE_USE_", // routingEnvKeys's own prefix check
    "VERTEX_REGION_",   // vertexRegionPrefix, env.go
    // No AgentEnvAllowed entry starts with any of these (asserted in
    // TestAgentEnvAllowListNeverHitsNeverList, Task 1): the whole Anthropic
    // API/Claude Code configuration surface, OpenTelemetry's own variables
    // (a repository could otherwise point Claude Code's telemetry at an
    // arbitrary collector), AWS's SDK-wide variables (credentials, region,
    // profile — relevant the day a Bedrock-style route exists, costs
    // nothing to deny today), and every CLAUDE_CODE_SKIP_* auth bypass
    // (only CLAUDE_CODE_SKIP_VERTEX_AUTH is confirmed above; the prefix
    // denies a sibling this repository hasn't had reason to read yet).
    "ANTHROPIC_", "OTEL_", "AWS_", "CLAUDE_CODE_SKIP_",
}
```

A name that would have needed its own "unverified, named by this review"
entry in an earlier draft — `ANTHROPIC_MODEL`, `ANTHROPIC_SMALL_FAST_MODEL`,
`ANTHROPIC_BETAS`, `ANTHROPIC_CUSTOM_HEADERS` — is now covered by the
`ANTHROPIC_` prefix outright and needs no separate listing (an earlier
draft of this section listed `ANTHROPIC_CUSTOM_HEADERS` oddly, "under
prefixes… a single name, not a prefix, if real"; the prefix above makes
that distinction moot). Nothing concrete remains to add for "plugin-dir
knobs," the one family this review named without a proposed spelling —
there is no name to list until one is.

**The rule that makes all of this enforceable, not just documented:** the
allow-list is **Fugaro's own Go code** (§2), never a `fugaro.yaml` field —
a repository names *which allow-listed variable* it wants set and to what
value, never a new name. Widening the allow-list needs a Fugaro release, the
same way widening the gateway's `routedKeys` field allow-list needs a live
check first (`docs/multi-model.md` §7, Check 25). This is the structural
difference from a secret's variable (a deny-list: anything not reserved is
fine) and is deliberate: an allow-list that only grows on a maintainer's own
review is the generic mechanism's actual safety property, not the per-field
validation around it.

**`checkSettingsRouting` needs no change.** It polices a *file* Claude Code
reads from disk; `agent.env` is a *config key* the runner turns into
environment (§2) or managed-settings (§3) entries through the same
allow-list either way. The two mechanisms are independent and the routing
check's job (refuse a committed settings file that tries to reroute) is
unaffected by either. What **does** need a new, narrow test is that the two
allow-lists (routing's deny-list, `agent.env`'s allow-list) never overlap —
listed in §2.

## 2. Scope and keys: `agent.env`

**The key.** `Agent.Env map[string]string` (`internal/config/config.go`'s
`Agent` struct, next to `Instructions`/`Review`), `yaml:"env,omitempty"`.
Plain data, no nesting: `{VARNAME: "value"}`.

**Scope: repo only — not project layer, not a profile, not an override
flag.** Recommendation, with each alternative's rejection reason:

- **Not the project layer or a profile.** `docs/project-layer.md` §8's own
  threat model is explicit about what the project layer must not hold:
  "The project layer cannot hold policy keys, `secrets`, `followup.*`,
  `base_branch`, `reviewers`, `dockerfile` or `instructions`" — and, until
  the 0.7.0 bucket-IAM hardening ships, **every launcher**, not only
  operators, can write the project layer object directly
  (`project-layer.md` §8, "Residual caveats"; `SECURITY.md`'s "What is not
  on by default" says the same). `agent.env` is not a dollar or model
  policy, but it reaches the same process the budget gateway's pins reach,
  with no PR review gate at all today — exactly the gap `project-layer.md`
  already carves `secrets:` and `instructions` out for. Recommendation:
  refuse it in the project layer the same way, and say why in the scope
  table's `Why` column — the exact text is in the final recommendation
  below, repeated nowhere else in this document.
- **Not an override (`fugaro run --env …`).** `InOverride` keys today
  (`agent.model`, `agent.review_rounds`, `agent.max_budget_usd`,
  `agent.recipe`, `agent.oauth_token`, `workflows.*.timeouts.total`) are
  either a tuning knob a human reasonably wants to try per run, or (like
  `oauth_token`) a choice **among names an allow-list already bounds**, not
  a value. An `--env` flag would let anyone who can launch a run (not
  necessarily anyone who can push a PR) set an allow-listed variable for
  one run without review — a smaller version of the same gap as the
  project layer. The origin story's own fix (thinking/beta knobs for a
  provider route) is a property of the *workflow's configuration*, decided
  once, reviewed with everything else in `fugaro.yaml` — not something a
  launcher should toggle ad hoc. Recommendation: no override; add one later
  only if real use shows a per-run need, the same caution `agent.recipe`
  already got right (it *is* override-able, because picking a different
  canned task loop for one run is exactly a "try this once" choice; setting
  a process environment variable is not the same kind of decision).
- **Repo scope, like `agent.instructions`/`agent.review`** (`whyFiles`),
  because the value is consumed somewhere outside `fugaro.yaml`'s own
  policy math and belongs with the repository that wrote it, under PR
  review. `ScopeRow{Key: "agent.env", In: InRepo, Why: "it changes the
  agent process's environment, which only the repository's own,
  PR-reviewed fugaro.yaml may do, from a closed allow-list"}`.

This recommendation means `agent.env` is a **flat map applied uniformly to
every stage of the workflow**, not a per-role thing: it goes through
`BuildEnv` once at bootstrap (like `Secrets`, not like the per-stage model
pins), which is also the simplest correct implementation (§4).

**The allow-list: an explicit list of names, not a prefix.** The task's own
framing floats "`CLAUDE_CODE_*` knobs that are safe" as a prefix; this
design recommends **against** prefix-matching for v1:
`CLAUDE_CODE_SKIP_VERTEX_AUTH` and every current and future
`CLAUDE_CODE_USE_*` are *also* `CLAUDE_CODE_*`, and a prefix rule would need
to anticipate and exclude every routing-relevant name Anthropic adds next,
forever. An explicit, enumerated list is closed by construction: a name
Anthropic ships tomorrow is simply not on it until a maintainer adds it
(with the same review a prefix widening would need anyway, but without the
risk of a name slipping through a prefix match nobody re-audited). Proposed
`AgentEnvAllowed` (`internal/config/validate.go`, beside `ReservedEnvNames`):

```go
var AgentEnvAllowed = []string{
    // Useful on any stage (an all-Claude repository tuning its own thinking
    // depth, say); also the two names agent.ProviderRouteVars() already
    // hard-codes for a provider-route stage (§3) — a repository's own
    // agent.env entry for either name is always beaten by the provider
    // mechanism's result on a provider-route stage (§3's precedence rule),
    // so setting it here only has an effect on an all-Claude stage.
    "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS",
    "MAX_THINKING_TOKENS",
    // Already told to users with no mechanism to apply it (docs/gcp-setup.md:257).
    "ENABLE_TOOL_SEARCH",
    // Telemetry/updater/non-essential-traffic knobs Claude Code documents.
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC",
    "DISABLE_TELEMETRY",
    "DISABLE_ERROR_REPORTING",
    "DISABLE_BUG_COMMAND",
    // A small set for the project's own tools (commands.build/test see the
    // same environment, §4): conventional, non-routing, non-secret toggles.
    "CI", "NO_COLOR",
}
```

**`DISABLE_AUTOUPDATER` is deliberately left off, not merely unlisted.**
It is already a `baseEnv` pass-through (`env.go:24`) the base image sets
(presumably to a truthy value, pinning Claude Code to
`images/base/Dockerfile`'s `CLAUDE_CODE_VERSION`, self-tested on every
build). Since `agent.env`'s values are applied *after* `baseEnv`'s copy in
`BuildEnv` (decision E2 in the plan), a repository setting
`agent.env: { DISABLE_AUTOUPDATER: "" }` or `"0"` would **override the base
image's own setting**, re-enabling Claude Code's auto-updater mid-run and
letting it replace the pinned, self-tested binary with whatever version it
fetches — exactly the version-pinning guarantee the base image exists to
hold. Every other entry on this list is a variable nothing else sets, so
"override" and "set" are the same operation; `DISABLE_AUTOUPDATER` is the
one exception, where "override" can mean "turn a safety property off," and
it is refused for that reason alone.

**`FORCE_COLOR` is dropped from v1** (an earlier draft of this list
proposed it next to `CI`/`NO_COLOR`): forcing ANSI color codes into a
subprocess's output is a real, if modest, risk specifically for
`commands.build`/`test` (§4: they see the same environment) — color escape
sequences in test-runner or linter output can corrupt a result parser that
expects plain text (`verify.Run`'s pass/fail extraction, a JUnit XML
reader fed malformed input, this repository's own transcript tooling) in
a way that is easy to miss until a real build happens to hit it. `NO_COLOR`
carries no equivalent risk (it can only remove escape codes, never inject
them), so it stays; `FORCE_COLOR` can be proposed again later with its own
review once there is a concrete need, rather than shipped speculatively in
v1's starting list.

Every name here is checked, in a test, against the routing names
(`routingEnvKeys` and `CLAUDE_CODE_USE_*`, `TestAgentEnvAllowListNeverRoutes`,
§1) — never against `ReservedEnvPrefixes`/`ReservedEnvNames`, which is a
different list for a different purpose (the secrets deny-list, §0) that
`CLAUDE_CODE_*` names are *expected* to already be part of. `routingEnvKeys`
itself lives in `internal/agent/settings.go` today, a package `internal/config`
cannot import back without a cycle (`internal/agent` already imports
`internal/config`, `env.go:11`); the plan moves the canonical list into
`internal/config` (beside `ReservedEnvPrefixes`) and has `internal/agent`
read it from there, so both the routing check and this new test share one
list with no cycle. A name that fails the routing check cannot ship. The
list is intentionally short and is expected to grow a few entries at a
time, each its own small, reviewable PR — exactly the safety property §1
relies on. The owner rules on the starting list; nothing above is final.

**Value shape.** A plain, **non-empty** string, printable ASCII only, no
control characters (`\n`, `\r`, `\0`, …) and no whitespace (every value
above is a single token: a boolean-ish `"1"`/`"true"`/`"false"`, a number,
a short word), at most 128 bytes — the same order of magnitude as
`agent.model`'s 100-character cap (`validateAgentModels`,
`validate.go:444-454`) and far short of `maxSettingsFile`'s 1 MiB.
**Empty (`""`) is refused, not accepted as "unset."** An earlier draft of
this design accepted `""` with a muddled rationale about "clearing a
default-ish entry," a concern that only ever applied to
`DISABLE_AUTOUPDATER` — now dropped from the list entirely, specifically
*because* an empty value could clear a safety-relevant default (above).
With that one case gone, there is no remaining reason to accept `""`: a
repository that does not want a variable set simply leaves its key out of
the map, the same as every other optional `fugaro.yaml` field; an explicit
empty value is far more likely to be a mistake (a templating bug, a
copy-paste of an unset shell variable) than an intentional "clear this,"
and refusing it catches that mistake at `fugaro validate` instead of
silently shipping a blank value to Claude Code's environment. Values are
**never secret by construction** (the allow-list excludes anything that
could be): a repository that needs a secret keeps using
`workflows.*.secrets` (`whySecrets`, unchanged). `Validate` refuses: a key
not on `AgentEnvAllowed`, naming the key (`agent.env.<NAME>`, "%s is not an
agent environment variable Fugaro allows; see docs/design/agent-env.md" —
or whatever doc section ships this), a value failing the shape check
(including empty), and — new in this revision — **a key that collides with
the same workflow's own `secrets[].env`** (neither `CI` nor
`MAX_THINKING_TOKENS`, to pick two `AgentEnvAllowed` entries, is in
`ReservedEnv`, §0, so a workflow secret could legally declare either one
today): refused in `Validate`, naming both the `agent.env` key and the
workflow's `secrets[]` index, rather than left to `BuildEnv`'s own,
undocumented precedence between the two (§0's `EnvSpec.Secrets` vs.
`EnvSpec.AgentEnv` ordering settles *which one would win* if this were
allowed, but a silent winner is worse than refusing the collision outright
at the one layer that can see both: `Validate` has the whole resolved
`Config`, including every workflow's `secrets:` block, in view).

**This collision check does not cover the provider side, and that is a
real, separate gap — §3's mechanism can silently override a declared
secret of the same name, with no validator today positioned to catch it.**
A provider's own `agent_env` (owner-only, local config) or the always-on
`DefaultProviderAgentEnv` can set any `AgentEnvAllowed` name on a
provider-route stage; most of those names are not reserved for secrets
either (only the `CLAUDE_CODE_*`-prefixed ones are, via the pre-existing
`ReservedEnvPrefixes` check — `MAX_THINKING_TOKENS` is not, so a workflow
secret named `env: MAX_THINKING_TOKENS` is legal today, with or without
this design). Since `pins` is confirmed to be the structurally final write
in both channels (above), such a secret's real value would be silently
replaced by the provider's value on any provider-route stage — and neither
existing validator can see the collision: `ValidateModelProviders` (the
owner's local config) has no visibility into any repository's `fugaro.yaml`
secrets at all, and `Validate` (one repository's `fugaro.yaml`) has no
visibility into the owner's `providers:` block. **Fix: a new check that
runs where both are already resolved together** — `internal/cli/validate.go`'s
provider-policy block already combines `cfg` (the repo's file, including
every workflow's `secrets:`) with `lc.Providers` (the owner's local config)
in one place (`validate.go:180-196`'s neighborhood), the same place §3's
two-provider conflict check lands; and the runner's own bootstrap
(`checkProviderModels`) resolves both for the same reason. A new
`config`-package function, `CheckProviderAgentEnvSecretCollision(cfg
*Config, providers map[string]ModelProvider) []Problem` (name adjustable),
checks whether any workflow's `secrets[].env` name coincides with any name
the run's actual coder/reviewer/background providers could set (that
provider's `DefaultProviderAgentEnv` merged with its own `agent_env`), and
refuses, naming the colliding variable and the workflow — called from both
`internal/cli/validate.go` and `checkProviderModels`, the same
one-function-two-call-sites pattern §3 already settled on for the
two-provider case.

**The three edits, plus a fourth and fifth** (`docs/design/headroom-compression.md`
§4 restates this pattern most recently; this design follows the same
shape):

1. **The Go struct**, `internal/config/config.go`'s `Agent`: `Env
   map[string]string \`yaml:"env,omitempty"\``.
2. **`Validate`**, `internal/config/validate.go`: the allow-list and shape
   checks above, next to the existing `Agent.*` checks
   (`validateAgentModels`/`validateAgentTokens`, `validate.go:440-473`).
3. **`schemas/fugaro.schema.json`**: an `env` property under `agent`,
   `additionalProperties: false` with one named property per
   `AgentEnvAllowed` entry (a `pattern`/`maxLength` matching the shape
   rule) — not a generic `patternProperties` for an arbitrary name, so an
   unknown key is a schema error too, not just a `Validate` one. **Each
   property's `type` must be `["string", "number", "boolean"]`, not
   `"string"` alone, with the `pattern` applying only when the instance is
   already a string.** Confirmed empirically (`gopkg.in/yaml.v3` unmarshals
   an unquoted `MAX_THINKING_TOKENS: 0` into Go's `map[string]string` as
   the string `"0"` with no error — the real parser is permissive about
   scalar shape), but the schema test harness decodes YAML into `any` first
   (`yamlInstance`, `schemas/schemas_test.go:47-54`, `yaml.Unmarshal(data,
   &v)` then `json.Marshal(v)`), which preserves YAML's *implicit* typing:
   an unquoted `0` becomes a JSON number, not a JSON string. A
   `"type": "string"` property would reject exactly the config the real
   Go-side `Validate` accepts — a schema stricter than the runtime parser,
   the wrong direction for a linting aid to drift from the thing it lints
   for. Accepting the wider type set matches what the real parser actually
   allows, rather than mandating a quoting convention `fugaro.yaml` authors
   would have no other reason to know about. A `testdata/config` corpus
   file exercises both an unquoted numeric value and a quoted string value
   for the same key (`TestFugaroSchemaCorpus`,
   `schemas/schemas_test.go:73`); a new
   `TestFugaroSchemaReservesTheSameAgentEnvAllowList` keeps the schema's
   property names equal to `config.AgentEnvAllowed`, the same shape as the
   existing `TestFugaroSchemaReservesTheSameEnv`
   (`schemas/schemas_test.go:292-313`, which does the equivalent check for
   the reserved-prefix *deny*-list).
4. **`internal/config/scope.go`'s `Scopes` table, and
   `docs/project-layer.md`'s printed copy**: `{Key: "agent.env", In:
   InRepo, Why: "..."}` (above). `TestScopeTableMatchesDocs`
   (`internal/config/docs_scope_test.go:14-36`) keeps the two files equal;
   skipping this edit leaves the project-layer resolver treating
   `agent.env` as unknown regardless of what `Validate` allows
   (`scope.go:39-41`: "A key not listed under a block is unknown").
5. **A fifth edit this design's first draft missed**: `internal/config/scope_test.go`'s
   `TestScopesMatchDesignDoc` (`scope_test.go:42-48` onward) — a *second*,
   independent golden map, `want := map[string]Scope{...}`, pinning every
   key's scope literally in Go, separate from the doc-table check Task 1's
   fourth edit covers. It needs its own `"agent.env": InRepo` entry or the
   test fails the moment `scope.go`'s `Scopes` table gains the row the
   fourth edit adds — not because the two tests duplicate each other's
   job (one pins the doc, this one pins the code against a design
   rationale independent of either doc's wording), but because both exist
   and both must agree.

**Is `agent.env` an "executable" key?** No, by the same reasoning
`NonExecutableImageKeys` already states for `image.node` (`scope.go:138-141`,
quote text on line 139, "a pinned … version … never a command"): every value is a closed-set,
allow-listed literal `Validate` already shape-checks, consumed as an
environment variable Claude Code reads, never interpreted as shell, a URL,
or a tool name to fetch. This matters only if `agent.env` is ever proposed
for the project layer later (recommended against above): were that
reconsidered, it would not need `--executable-changes`, the same way
`image.node` doesn't — but that question does not arise at repo scope,
since `fugaro.yaml` is already PR-reviewed in full.

## 3. Provider-scoped variant: `providers.<name>.agent_env`

**This section generalizes code that already ships, rather than adding a
parallel one.** `agent.ProviderRouteVars()` (`internal/agent/env.go:219-237`)
already sets `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1` and
`MAX_THINKING_TOKENS=0` for any stage `stageOnProviderRoute` flags
(`internal/runner/agentenv.go:54-78`), hard-coded, with no owner or
repository override. The origin bug is not really a repository's concern —
these two variables exist because **OpenRouter's Anthropic-compatible
endpoint**, not the repository's code, cannot be sent `context_management`
or extended thinking — so the knowledge belongs with whoever adds the
provider, the **owner**, in the local config (`docs/multi-model.md` §2),
exactly where the hard-coded fix already assumes it belongs (it applies to
*every* provider today, correctly, since M10 has exactly one provider
kind). What is missing is only the owner's ability to override or extend
it — a second OpenRouter-like endpoint with different quirks, or a
narrower fix once a future Claude Code release no longer needs
`MAX_THINKING_TOKENS=0` for some models — without a Fugaro code change
each time.

**The field.** `ModelProvider.AgentEnv map[string]string`
(`` `yaml:"agent_env,omitempty"` `json:"agent_env,omitempty"` ``,
`internal/config/providers.go:29-47`), owner-only, never in `fugaro.yaml` —
the same posture `providers:` itself already has. Checked by
`ValidateModelProviders` (`providers.go:103-165`) with the **same**
allow-list and shape rules as §2's `agent.env` (one function,
`validateAgentEnvMap`, shared by both call sites) — a provider cannot set
anything a repository couldn't set either; it only gets to set it *for
stages that use it*, automatically, instead of every repository
re-declaring it.

**It never reaches the shared, published config, with no new code
required.** `(*localcfg.Config).Shared()` (`internal/localcfg/shared.go:13-39`)
is the installation-wide config every launcher can read from the runs
bucket, and it already drops the whole `providers:` map outright
(`shared.go:37`, `s.Providers = nil`) — `providers:` is called out by name
in `Shared`'s own doc comment as local-only, "whose base_url is where a
key and a repository's code are sent." Since `AgentEnv` is a field *nested
inside* `ModelProvider`, dropping the whole map drops it too; there is no
separate case to handle and no risk of a provider's `agent_env` leaking
into the shared config through some field-by-field copy that `Providers =
nil` doesn't cover. `TestMergeSharedNeverCarriesProviders`
(`internal/localcfg/shared_test.go:208-230`) already proves this for the
provider block as a whole; the plan adds an `agent_env` sub-key to that
test's existing fixture so the assertion is exercised for the new field
specifically, not only inferred from the map being nil.

**How it reaches the job.** The existing channel, unchanged in shape:
`ProvidersEnv`/`ProvidersFromEnv` (`internal/runner/providers.go:14-78`)
already round-trip the whole `ModelProvider` through `FUGARO_MODEL_PROVIDERS`
JSON with `DisallowUnknownFields` and a re-`ValidateModelProviders` call; a
new field on the struct is carried through it with no new environment
variable and no new secret — **but not "for free"**: see §5's compatibility
gap, which this reuse creates and which needs its own gate.

**How it applies: `DefaultProviderAgentEnv` replaces `agent.ProviderRouteVars()`
as the single source, computed inside `stagePins` — not in
`writeManagedSettings`, and not as a second mechanism alongside the
first.** `stagePins` (`agentenv.go:48-66`) already is the one place that
computes a stage's pins once and whose result (`pins`) is merged into
*both* the managed settings file and the process environment
(§0) — exactly the per-stage, per-role, dual-channel computation a
provider's `agent_env` needs, because it already exists for this reason.
Concretely, `stagePins` changes from:

```go
if r.stageOnProviderRoute(model, bg) {
    maps.Copy(pins, agent.ProviderRouteVars())
}
```

to resolving the provider(s) that serve `model` and `bg`
(`config.ProviderFor`, checking **both**, exactly as `stageOnProviderRoute`
already does — this is not new logic, only where the provider's `AgentEnv`
value is layered in) and merging, in order, `DefaultProviderAgentEnv` (the
renamed, now-overridable former `agent.ProviderRouteVars()` literal), then
that provider's own `AgentEnv`. `agent.ProviderRouteVars()` itself is
removed, not kept as a second code path beside the new one — one function
computing one result is the entire point of putting this inside
`stagePins` rather than `writeManagedSettings` (E3 in the plan, revised).
Two consequences fall out of keeping this inside `stagePins`, reusing the
mechanism that already ships:

- **It is only ever available when the gateway is on**, which a provider
  model already requires unconditionally (`CheckProviderGateway`,
  `providers.go:285-312`, function at `providers.go:293`: a provider model
  without `budget.mode: enforce` is refused before any call) — `stagePins`
  itself returns `nil` with no gateway (`agentenv.go:54-57`), so there is no
  new "what if the gateway is off" case to design for, in either channel.
- **It wins over a repository's `agent.env` in both channels it already
  reaches**, because `pins` is layered on top of `r.env` in both
  `writeManagedSettings` and `req.Env = gitops.WithVars(r.env, pins)`
  (§0) — not merely in the managed settings file. This is the concrete
  mechanism behind the precedence rule below, not a separate guarantee to
  build, and it closes the gap an earlier draft of this design left open
  (computing the provider's override in `writeManagedSettings` alone would
  have left the process-environment channel unprotected — see the plan's
  Task 5, `TestStagePinsProviderAgentEnvBothChannels`).

**The default itself, unchanged in content, renamed and made
overridable.** `DefaultProviderAgentEnv` (`internal/config/providers.go`,
beside `ModelProviderKind`) replaces `agent.ProviderRouteVars()`'s literal,
applied to every provider of kind `anthropic-compat`
(`config.ModelProviderKind`, `providers.go:23`) unless its own `agent_env`
overrides a key — the same two values, now owner-overridable instead of
hard-coded:

```go
var DefaultProviderAgentEnv = map[string]string{
    "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": "1",
    "MAX_THINKING_TOKENS":                    "0",
}
```

This remains the one piece of this design that is not "a generic
mechanism, no policy" — it is the already-shipped, measured fix
(`docs/multi-model.md` §7: "Claude Code features that a compatible
endpoint may not support… surface as the upstream's error and fail the
stage; which of them break is assumed until Check 25 records it," and the
fix's own measured-effect table in the same section), kept on by default
for every provider route because the alternative (every owner rediscovering
Check 25's failure independently, or a second provider needing to carry a
second hard-coded Fugaro release) is worse. A provider's own `agent_env`
can turn a default back off by repeating the key with a different allowed
value (there is no "off" sentinel needed: a key absent from a provider's
map simply keeps the default).

**The two-provider conflict case.** `stageOnProviderRoute`'s existing
check (role model **or** background model) means a single stage can, in
principle, touch two different providers at once: an explicit
`agent.models.background` naming a different provider's model than the
coder's. If both providers set conflicting `agent_env` values for the same
key (or one relies on a `DefaultProviderAgentEnv` key the other's own
`agent_env` overrides differently), there is no principled way to pick a
winner — recommendation: **refuse the run before any call** when a single
stage's role model and background model resolve to two different
providers at all, regardless of whether their `agent_env` actually
conflicts — a repository mixing providers within one Claude Code process
is already an edge case `docs/multi-model.md` never anticipated, and
refusing it outright is simpler and safer than resolving a conflict that
would otherwise depend on map iteration order.

**Where the check lives: one `config`-package function, called from both
places this repository already calls its sibling provider-policy checks
from — not two separate implementations.** `CheckProviderAuth`
(`providers.go:257-283`), `CheckProviderGateway` (`providers.go:285-312`)
and `CheckProviderPolicy` (`providers.go:314-364`) are each a single pure
function in `internal/config`, called from **both** `fugaro validate`'s
offline check (`internal/cli/validate.go:180-196`)
**and** the runner's own bootstrap authority (`checkProviderModels`,
`internal/runner/gateway.go:389-423`, which calls exactly those same three
functions) — the config-side function is the one thing, not two
things duplicated. The two-provider conflict check should be a new
function of the same shape (`config.Problem`'s, named in both the stage's
role and background model and which provider each resolves to), called
from the same two places: `internal/cli/validate.go` so `fugaro validate`
catches it before any job runs, and `checkProviderModels` as the runtime
authority a live run actually enforces — an earlier draft of this design
named only the runtime call site, and an earlier draft of the plan put it
only in `checkProviderModels` directly rather than as a shared, offline-
checkable function; both omissions are fixed by following the pattern its
sibling checks already use. (Today,
with exactly one provider kind and `config.EffectiveBackground` only ever
defaulting the background to the *coder's own* provider when it is a
provider's model, this case cannot yet arise in practice — it becomes
reachable only once an owner's local config has two providers and a
repository's `fugaro.yaml` explicitly pins its coder to one and its
background to the other.)

**The secrets-collision gap (§2) is a sibling of this same case, and
shares its fix.** `CheckProviderAgentEnvSecretCollision` (§2) needs exactly
the same two things the two-provider check needs — the repository's
resolved `Config` and the owner's resolved providers, together — so it
belongs beside it, in the same two call sites (`internal/cli/validate.go`,
`checkProviderModels`), not as a third, differently-located check.

**Precedence, corrected: this is not a five-level override ladder where
each level is structurally protected from the one below it.** An earlier
draft of this section claimed the gateway's own routing variables are
"highest" and "never overridable by any of the above, in both channels,"
then conceded in the very next sentence that `pins` is merged in *after*
them in both channels — which, read correctly, means **`pins` (levels 3-4
below) is the structurally last write in both channels, not the gateway's
routing variables.** Confirmed directly: `writeManagedSettings`
(`agentenv.go:85-104`) computes `vars` from `agent.GatewayVars` first, then
runs `for k, v := range pins { vars[k] = v }` — `pins` overwrites `vars` on
any shared key, not the reverse. For the process channel,
`gitops.WithVars(r.env, pins)` (`internal/gitops/credentials.go:63-75`)
drops any `r.env` entry whose key is also in `pins`, then appends all of
`pins` — again, `pins` always wins over whatever `r.env` already holds
(the gateway vars `BuildEnv` baked in, and the repository's own
`agent.env`, levels 1-2 below, alike). There is no merge-order mechanism
anywhere in this design that makes the gateway's routing variables
unoverridable; if `pins` ever contained a routing-variable key, it would
silently win, in both channels, every time.

**What actually keeps the gateway's routing variables safe is a closed
allow-list, not a precedence rule.** `pins` is `agent.PinVars`'s own fixed
output (hard-coded model/output-limit variable *names*, never a routing
name) plus, from this design, `DefaultProviderAgentEnv`'s fixed literal and
a provider's own `agent_env` — and a provider's `agent_env` can only ever
contain a key from `AgentEnvAllowed` (`validateAgentEnvMap`, shared with
`agent.env`'s own check, §2), which `TestAgentEnvAllowListNeverRoutes`
keeps disjoint from every routing name (§1). So `pins`'s possible key set
is closed and verified, by construction, to never include
`ANTHROPIC_BASE_URL`, `ANTHROPIC_API_KEY`, or any other name
`routingEnvKeys`/`CLAUDE_CODE_USE_*` names — not because the merge
mechanism would refuse to let it win, but because nothing that reaches
`pins` is ever allowed to be one of those names in the first place. The
safety property is "pins can never collide with routing," proven by a
test against the allow-list, not "routing wins the merge."

With that corrected, the four levels that *are* genuinely a precedence
ladder, narrowest wins:

1. Claude Code's own built-in defaults (lowest).
2. The repository's `agent.env` (§2) — process environment only, set once
   at `BuildEnv` time into `r.env`, applies to every stage of the
   workflow. Absent from the managed-settings channel entirely: see below.
3. `DefaultProviderAgentEnv`, for any stage whose role model or background
   model a provider serves — part of `pins`, computed once per stage by
   `stagePins`.
4. That provider's own `agent_env`, for the same stages — overrides a
   built-in default's key within `pins` itself, before `pins` is ever
   merged into either channel.

The gateway's own routing variables (and the rest of `agent.PinVars`'s
model-pin output) sit outside this ladder entirely: never overridden in
practice, but by exclusion from `pins`'s possible content, not by
precedence.

**The two channels, concretely.** `pins` (levels 3-4) is computed once, in
`stagePins`, and is the structurally final write in **both**: the process
channel starts from `r.env` (level 1 and 2, plus the gateway's routing
variables `BuildEnv` already baked in there) and applies
`gitops.WithVars(r.env, pins)` (`runner.go:1574`); the managed-settings
channel starts from `writeManagedSettings`'s own, freshly computed
`GatewayVars` result (the gateway's routing variables again, recomputed
per stage) and applies `for k, v := range pins { vars[k] = v }`. The two
channels' bases differ in one respect: the managed-settings channel's base
never includes level 2 at all, since `writeManagedSettings` never reads
`r.env` — a repository's `agent.env` entry reaches only the process
environment via `BuildEnv`, never the managed-settings file directly;
Claude Code still reads it there for any key the managed-settings file
doesn't separately name. The plan's Task 5 adds a test
(`TestStagePinsProviderAgentEnvBothChannels`) asserting levels 3-4 win over
level 2 in both channels at once, which an earlier draft of this design
lacked.

## 4. Where each applies

| Surface | In scope? | Why |
|---|---|---|
| **The Claude Code process** | Yes — this is the entire point. | `BuildEnv` (§2) and the managed settings file (§3). |
| **The repository's check commands** (`commands.build`/`test`/`lint`/`fix`) | Yes, automatically, with no separate design. | Two paths, both already landing on `r.env`: `commands.build`/`test`, run through `fugaro verify`, are the agent's own Bash-tool children (§0), inheriting exactly the environment `BuildEnv` built; `commands.lint`/`fix`, the runner's own gate step (`internal/runner/gate.go:151,235,361`, each `Env: r.env`), run directly by the runner, never through Claude Code at all. Either way, any allow-listed `agent.env` entry a repository sets is visible, for free — a feature a repository might actually want (`CI=true` steering a test runner's own branching) and costs nothing extra to support. |
| **The image build** (Cloud Build) | **No. Out of scope.** | Cloud Build's own job environment (`IMAGE`, `SECRET_ENVS`, `WORKFLOW`, `REPO_URL`, `BASE_BRANCH`, already reserved names, `validate.go:78`) is a different process, a different trust boundary (it runs before any agent, with the build's own secrets, never the gateway), and already has its own mechanism for a repository's needs: `workflows.*.image.setup`/`image.apt`/`image.tools`, all gated as `ExecutableKeys` (`scope.go:107-132`) because they run as shell. Folding env into that surface would either weaken an executable-key gate that already exists for a good reason, or create a second, non-executable-looking way to influence a step that is, in fact, arbitrary shell — rejected either way. |
| **Secrets** | **No. Out of scope**, unchanged. | `agent.env` values are never secret by construction (§2); a repository that needs a credential keeps using `workflows.*.secrets`. |

## 5. Compatibility

**`agentEnvSince`,** a gate of the same shape as `recipesSince`/
`namedTokensSince`/`layeredSince` (`internal/cli/recipes_skew.go:16-43`):
the first release whose runner's strict `fugaro.yaml` decoder knows
`agent.env` — an older binary refuses the key outright (the same "Binaries
before 0.6.5 refuse this key" comment pattern already on `OAuthToken`,
`config.go:156-157`). `fugaro run` must refuse a launch against an older job
image the same way `checkImageSince` already refuses one that doesn't know
`agent.recipe` or `OAuthToken`
(`recipes_skew.go:91-137`): a new `checkAgentEnvImage` wired the same way,
triggered whenever the resolved `fugaro.yaml` sets a non-empty `agent.env`.
`docs/release.md`'s "Before you tag" checklist gets one more line verifying
`agentEnvSince` equals the release that ships it, mirroring its existing
`recipesSince`/`namedTokensSince` entries.

**The provider-scoped variant has a separate, more severe compatibility
gap `agentEnvSince` alone does not close — a repository's `fugaro.yaml`
plays no part in triggering it.** `providers.<name>.agent_env` travels in
`FUGARO_MODEL_PROVIDERS`, the compact JSON `fugaro init --repo` writes into
a repository's job environment (`internal/infra/spec.go:610-615`,
`ws.Env[ModelProvidersEnv] = pv`) **once, for every subsequent run of that
repository**, whenever any provider in the owner's local config lists the
repository in `allow_data_to` — regardless of whether that particular run's
own `fugaro.yaml` uses a provider model at all. The job's own `fugaro exec`
decodes it unconditionally, on every run (`internal/cli/exec.go:92`,
`runner.ProvidersFromEnv(os.LookupEnv)`), with `DisallowUnknownFields()`
(`internal/runner/providers.go:65`). So: an owner upgrades their CLI,
adds `agent_env` to a provider's local-config entry, runs `fugaro init
--repo` (which updates the job's `FUGARO_MODEL_PROVIDERS` to include the
new field) — and if that repository's **job image** still runs an older
runner binary that doesn't know the `AgentEnv` field, `ProvidersFromEnv`'s
decode fails at bootstrap, for **every** run of that repository from then
on, including a plain all-Claude one that never mentions a provider model.
This is not a hypothetical: `fugaro init --repo` does not always rebuild
the job image (it can leave an existing one in place), so "the owner's CLI
knows `agent_env`" does not imply "the repository's job image does."
**Fix:** gate the write, not only the read — before `fugaro init --repo`
calls `runner.ProvidersEnv` (or inside it), check the repository's current
job-image build record's `base_ref` against a `agentEnvSince` constant
(the same release `agent.env`'s own launch-time gate uses, above in this
section) whenever any provider the repository may use has a non-empty
`AgentEnv`; refuse with the same "run `fugaro image refresh`" message
`checkImageSince` already gives (`recipes_skew.go:97-137`'s `refuse`
closure), for an *existing* record whose `base_ref` predates
`agentEnvSince` — **but, unlike `checkImageSince` itself, a repository
with no build record at all must proceed, not refuse.** This is the actual
adaptation to `init --repo`'s own flow, not merely "no `task.Spec`": a
repository's *first* `fugaro init --repo` ever has no build record yet,
since `init --repo` is what builds the job image in the first place, and
reusing `checkImageSince`'s own "no record is unsafe" rule verbatim would
make that first `init --repo` impossible to complete the moment any
provider it may use already has a non-empty `AgentEnv` — a chicken-and-egg
refusal `checkImageSince`'s own launch-time use never has to resolve,
because a *launch* only ever runs after some image already exists. The
image a first `init --repo` produces is built from the running CLI's own
release, which — if that CLI's `ProvidersEnv`/`WorkflowSpec` code already
knows about `AgentEnv` at all — necessarily knows how to decode it too, so
proceeding with no record is safe in exactly the cases this gate needs to
allow. **A second, narrower
protection already exists for a different half of this risk**: an *older
CLI* running `fugaro init --repo` against a local config that already sets
`agent_env` is refused by `localcfg.go`'s own strict YAML decoding
(`internal/localcfg/localcfg.go:604`, `dec.KnownFields(true)`) before it
ever gets as far as writing anything — that case was never a gap. The gap
this fix closes is the other direction: a *new* CLI writing a *new* field
into an *old* image's job environment.

**`task.Spec`: no change.** `agent.env` is repo scope only (§2), read by
the runner from the checkout's `fugaro.yaml` at bootstrap exactly like
`agent.instructions` and `agent.review` are — never carried on the launch
task the way `task.Spec.OAuthToken` or `task.Spec.Recipe` are (those exist
because `oauth_token`/`recipe` are override-able; `agent.env` is not, §2).

**The job-image version gate** is `checkAgentEnvImage` above, reusing
`checkImageSince`'s existing machinery (`recipes_skew.go:91-137`) — no new
gating primitive needed, only a new call site and a new `*Since` constant.

**`fugaro doctor`/`validate`:** `Validate`'s existing problem-reporting path
(`[]Problem`, already how every other `agent.*` shape error surfaces,
`validate.go:94-98`) covers `agent.env` with no new output shape — an
unknown or malformed entry is a validation error exactly like a bad
`agent.model`. No special-casing needed.

**`fugaro diagnose <run>` shows which env keys applied, by name only,
never a value.** A new line, next to the existing `Config:` project-layer
line (`docs/project-layer.md` §7's "`Config:` line": "project layer
generation N…"): `Agent env: CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS,
MAX_THINKING_TOKENS (provider openrouter)`, distinguishing a repository's
own `agent.env` names from a provider's `agent_env`/default names and which
provider. **Names only is safe by construction, not by redaction**: every
value on the allow-list is, by §2's own rule, a non-secret literal — there
is nothing to redact, so this is the one piece of run output that is
allowed to print an `agent.*` value verbatim if ever useful for debugging
(a future nice-to-have, not proposed here); today, names are enough to
answer "did my fix actually apply."

**Redaction (the log/transcript redactor, not `diagnose`):** no change.
`r.addSecret` (`internal/runner/runner.go` and throughout) registers secret
*values*, never names; `agent.env`'s values are never secret, so none are
registered, and the existing redactor is untouched. This is a direct,
load-bearing consequence of the "plain string values only" rule in §2: the
day an allow-listed name's value needs to be treated as sensitive, it does
not belong on this allow-list, full stop — it belongs in
`workflows.*.secrets`.

## 6. Measurement / live checks (owner-run, not built by this design)

In the style of `docs/multi-model.md`'s Check 25/33 and
`docs/gcp-live-checklist.md`'s existing numbered checks — each a owner task
in the plan (docs/plans/2026-10-13-agent-env.md), none run here:

1. **An allow-listed variable reaches Claude Code.** Set
   `agent.env: { ENABLE_TOOL_SEARCH: "false" }` in a sandbox repository's
   `fugaro.yaml`, run a task whose transcript would show tool search
   behavior, and confirm the agent process actually saw the variable (the
   simplest proof: a deliberately wrong value that changes observable
   behavior, not just "the run didn't fail").
2. **A refused variable fails `fugaro validate`, not the run.** A name not
   on `AgentEnvAllowed` (`ANTHROPIC_BASE_URL`, or a plausible-looking typo)
   is refused at `fugaro validate` with a clear message, before any job
   starts — confirms `Validate`'s error path, not just a unit test of the
   function.
3. **The routing check still catches an attempted override.** With
   `agent.env` shipped, commit a `.claude/settings.json` with an `env`
   block naming a `routingEnvKeys` variable (the existing Check 20-style
   probe, `docs/gcp-live-checklist.md` "Check 20") and confirm
   `checkSettingsRouting` still refuses the stage — proof the two
   mechanisms (§1's closing claim) really are independent and neither
   weakens the other.
4. **The generalized default behaves identically to the fix it replaces,
   and an owner override actually takes effect.** Re-run
   `docs/multi-model.md`'s Check 25's provider-route task once with no
   provider `agent_env` set (confirm the run behaves exactly as the
   already-shipped `agent.ProviderRouteVars()` did: same variables, same
   values, in both the managed settings file and the process environment),
   then once more with the owner setting a provider's own `agent_env` that
   overrides one of the two default keys, confirming the override — not the
   default — reaches both channels.

## 7. Open decisions for the owner to rule on

Each is independently vetoable; this design's recommendation is stated,
not assumed:

- **D1. Scope of `agent.env`.** *Recommended:* repo only, no project
  layer, no override flag (§2). *Veto alternative:* allow it in a profile
  too, for a shared quirk across many repositories of one project —
  rejected for now because the project layer has no PR-review gate until
  0.7.0's bucket-IAM hardening ships (§2), and because there's no evidence
  yet of a *project-wide* (vs. provider-wide, which §3 already covers)
  need for this.
- **D2. The initial allow-list's exact contents.** *Recommended:* the
  nine names in §2. *Veto alternative:* start even narrower (only the two
  names Check 25 actually needed, `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS`
  and `MAX_THINKING_TOKENS`, plus `ENABLE_TOOL_SEARCH` since it's already
  documented) and add the rest only when a repository asks — rejected only
  because growing the list is already cheap and reviewable (§1), but a
  reasonable owner call either way.
- **D3. Allow-list shape: explicit names vs. a narrow prefix with
  carve-outs.** *Recommended:* explicit names (§2). *Veto alternative:* a
  `CLAUDE_CODE_` prefix with `CLAUDE_CODE_USE_*` and
  `CLAUDE_CODE_SKIP_VERTEX_AUTH` carved out — rejected because it requires
  the carve-out list to track every future Anthropic addition, which an
  explicit allow-list does not.
- **D4. `DefaultProviderAgentEnv` as the built-in default every
  `anthropic-compat` provider gets unless it overrides a key, replacing
  `agent.ProviderRouteVars()`'s hard-coded literal, vs. dropping the
  built-in default now that an owner override exists and requiring every
  `providers:` entry to set the two keys itself.** *Recommended:* keep the
  default (§3), since it is the already-shipped, already-measured
  behavior, and removing it would be a regression for every existing
  provider entry the day this ships, not a simplification. *Veto
  alternative:* drop the default, document the two keys in
  `docs/multi-model.md` and require every `providers:` entry to set them —
  rejected because it reproduces exactly the discovery cost (one failed
  live run per owner) `agent.ProviderRouteVars()` already exists to
  remove, for no safety benefit (the override mechanism makes the default
  changeable, not mandatory to keep).
- **D5. `CheckProviderAgentEnvSecretCollision` (§2), a new cross-config
  check for a workflow secret and a provider's effective `agent_env`
  sharing a name — refuse, or accept the residual.** *Recommended:* add
  the check (§2, §3): the alternative is a secret's real value getting
  silently replaced by a provider's configured value on any provider-route
  stage, with no warning anywhere, for a name as ordinary as
  `MAX_THINKING_TOKENS` or `CI`. *Veto alternative:* accept the residual
  and document it instead of adding a fifth cross-config check function —
  defensible given how narrow the collision space is in practice (a
  repository would have to name a secret exactly one of
  `AgentEnvAllowed`'s own entries), but this design recommends against it
  because the failure mode (a secret silently stops being the value the
  workflow's own code expects) is exactly the kind of surprise `Validate`
  exists to catch before a job runs, not discover live.
- **D6. Release placement.** See §8.

## 8. Release placement

**Recommended: 0.7.x** (the owner's framing at the top of this task), as a
small, independently-mergeable slice — **not** blocking on, and not blocked
by, the two large 0.7.0 efforts already touching `internal/config` heavily
(`docs/design/bucket-iam.md`, `docs/design/base-image.md`). Concretely:

- **Depends on nothing from bucket-iam or base-image.** `agent.env` (§2)
  touches `Config.Agent`, `Validate`, the schema, and `scope.go` — the same
  files `docs/design/headroom-compression.md`'s `agent.compression` touched
  and shipped independently of those efforts. The provider-scoped variant
  (§3) touches `ModelProvider` and the existing `FUGARO_MODEL_PROVIDERS`
  channel, neither of which bucket-iam or base-image change.
  `docs/design/named-oauth-tokens.md:117`'s own reasoning for sequencing
  *after* 0.7.0 ("both touching `internal/infra` heavily") does not apply
  here in the same way: this design's one `internal/infra` edit (the
  `fugaro init --repo` write-side gate, §5) is a single, narrow check at
  one call site (`spec.go:610`), nothing like bucket-iam's and
  base-image's own `internal/infra` work across image builds, Terraform
  and the job spec more broadly.
  Recommendation: land it as its own small PR group within the 0.7.x line,
  sequenced only by ordinary merge-conflict hygiene with whichever of
  bucket-iam/base-image's `internal/config`/`internal/infra` edits lands
  first — not a hard dependency either way, just "check for a conflicting
  edit to `scope.go`/`validate.go`/`spec.go` before starting," the same
  courtesy every concurrent config-touching design in this repository
  already extends (`docs/design/headroom-compression.md`'s plan does the
  same for `P3`'s `internal/agent/env.go` churn).
- **Also touches `internal/agent/env.go`, `internal/runner/agentenv.go` and
  `internal/infra/spec.go`** (§3: removing `agent.ProviderRouteVars()`,
  extending `stagePins`; §5: the `init --repo` gate), on top of the
  `internal/config` files §2 already named — an implementer should check
  all of these for independent churn before starting, same as
  `scope.go`/`validate.go`.
- **Gated on nothing live before it ships as opt-in.** Unlike
  `agent.compression` (gated on two live checks before it is "ever usable
  at all"), `agent.env`'s v1 scope (§2) sets process environment variables
  Claude Code already documents reading — there is no unverified
  third-party behavior to confirm first. The provider-scoped variant (§3)
  is different in degree, not in kind: `DefaultProviderAgentEnv`'s two
  keys are not merely "confirmed by a live run" — they are the exact
  values an already-shipped, already-tested runner change
  (`agent.ProviderRouteVars()`, PR #305, 0.6.6) sets today; §3 only makes
  that value owner-overridable. §6's check 4 re-confirms the generalized
  version behaves identically by default and that an override actually
  takes effect; it does not block shipping §3, since the provider surface
  is owner-only and off by default for every provider that doesn't set
  `agent_env` or isn't `anthropic-compat`.
