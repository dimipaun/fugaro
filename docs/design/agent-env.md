# A generic, safe way to set the agent's environment

Status: design for review (2026-10-13), docs only — nothing in this design has
been built. Source: the owner's request, after the first live
provider-model run (`docs/multi-model.md`, Check 25) failed because Claude
Code sends `context_management` when thinking is on and the gateway refuses
it (`internal/gateway/forward.go:222-248`, `docs/multi-model.md` §5). The
fix that unblocked that run was runner code setting
`CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1` and `MAX_THINKING_TOKENS=0` for
stages whose model is served by a provider route — bespoke, not something a
repository or the owner could express, because `fugaro.yaml` has no way to
set an agent environment variable at all (`workflows.<n>.secrets[].env`
only *names* a workflow secret's variable, and the set of names it may use
is a deny-list of everything dangerous, `internal/config/validate.go:60-80`,
not an allow-list of anything Claude Code reads). The owner asked whether to
add a generic mechanism and approved queuing this design. Everything below
is read from the code at this branch's base and cited by file; nothing was
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
  (`env.go:134-141`, the comment says so explicitly) — the gateway's
  routing variables when the gateway is on. This ordering is the one
  invariant every design below must not weaken.
- **Per-stage variables (model pins) do not go through `BuildEnv` at all.**
  They go into `agent.WriteManagedSettings`
  (`internal/agent/settings.go:40-91`), rewritten before every stage
  (`internal/runner/agentenv.go:60-84`, `writeManagedSettings`), because a
  stage's pinned model can differ by role and `BuildEnv`'s result
  (`r.env`, `internal/runner/runner.go:992-998`) is built once at bootstrap
  and reused for every stage of the run. The managed settings file is
  Claude Code's highest-precedence settings source
  (`internal/agent/settings.go:11-14`: "neither a repository's nor a
  user's settings file can override them") and exists **only when the
  gateway is on** (`internal/runner/agentenv.go:66-69`, `writeManagedSettings`
  returns nil with no gateway).
- **`checkSettingsRouting`** (`internal/runner/agentenv.go:103-159`) refuses
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
  subcommands read (`internal/runner/runner.go:951-957`), and those
  subcommands are what Claude Code's Bash tool invokes — so
  `commands.build`/`commands.test` inherit **exactly** `r.env`, the same
  allow-listed environment the agent process itself got from `BuildEnv`
  (confirmed by `SECURITY.md:52`: "Commands run through `fugaro verify` see
  the agent's full environment" — "full" meaning the agent's own, not the
  runner's unfiltered one). This matters for §4: there is no separate
  "check-command environment" to design; it is the same environment as the
  agent's.
- **`mise.toml`'s `[env]`** (this repository's own, `mise.toml:5-7`, and
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
protects (`SECURITY.md` §"What limits the blast radius today"). An **open**
`agent.env:` map — any name, any value — would let a repository's own
reviewed file do what today only a compromised agent reading `/proc` can do
(`SECURITY.md`'s "Known limits" list already enumerates that residual; this
design must not add a *reviewed, first-class* path to the same place):

| Must be impossible | Enforcement |
|---|---|
| Redirect model traffic around the gateway (`ANTHROPIC_BASE_URL`, `ANTHROPIC_VERTEX_BASE_URL`, `ANTHROPIC_BEDROCK_BASE_URL`, `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_SKIP_VERTEX_AUTH`, any `CLAUDE_CODE_USE_*`) | Closed allow-list (§2): these names are never on it. `BuildEnv` applies `agent.env`'s values **before** the gateway's own (§2, mirroring the existing `spec.Set` ordering, `env.go:133-141`), so even a bug that let one through would still be overwritten, not merely shadowed. A unit test (`TestAgentEnvAllowListNeverRoutes`, §2) asserts the allow-list and the routing names (`routingEnvKeys`/`CLAUDE_CODE_USE_*`) are disjoint — a narrower claim than disjointness from `ReservedEnvPrefixes`, since the allow-list's `CLAUDE_CODE_*` entries are *expected* to also be in `ReservedEnvPrefixes` (that list is the unrelated secrets deny-list, §0). |
| Proxy the agent's traffic (`HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY`, `NO_PROXY`, lower-case forms) | Same closed allow-list; these are also in `routingEnvKeys` and in `ReservedEnvPrefixes`/`ReservedEnvNames` already. Not on the allow-list, structurally. |
| Add a CA or certificate override (`NODE_EXTRA_CA_CERTS`, `SSL_CERT_FILE`, `SSL_CERT_DIR`, `CURL_CA_BUNDLE`, `GIT_SSL_CAINFO`) | Already `baseEnv` pass-through only (`env.go:21-22`), never agent-settable; not on the new allow-list either. |
| Preload or inject code (`LD_PRELOAD`, `LD_LIBRARY_PATH`, `DYLD_INSERT_LIBRARIES`, `NODE_OPTIONS`, `PYTHONSTARTUP`, `BASH_ENV`, `ENV`, `PERL5OPT`, `RUBYOPT`, …) | All already in `ReservedEnvPrefixes`/`ReservedEnvNames` (`validate.go:61-79`); not on the new allow-list. |
| Rewrite git's behavior (`GIT_*`) | `GIT_` is a reserved prefix already; `gitops.ModelCredentialVars` is stripped from every git call regardless (`internal/runner/runner.go:14-23`); not on the new allow-list. |
| Change `HOME`/`PATH`/the shell (`SHELL`, `IFS`, `BASH_ENV`, …) | Already `ReservedEnvNames`; `baseEnv` passes `HOME`/`PATH` through from the runner only, never settable by `fugaro.yaml`. |
| Impersonate Fugaro's own variables (`FUGARO_*`) | `FUGARO_` is a reserved prefix already; the new allow-list never contains one, and `spec.Set` (Fugaro's own `FUGARO_STATE_DIR` etc., `runner.go:966-973`) is applied **after** `agent.env`'s values in `BuildEnv`, so a collision resolves to Fugaro's own value, not the repository's. |

**The rule that makes all of this enforceable, not just documented:** the
allow-list is **Fugaro's own Go code** (§2), never a `fugaro.yaml` field —
a repository names *which allow-listed variable* it wants set and to what
value, never a new name. Widening the allow-list needs a Fugaro release, the
same way widening the gateway's `routedKeys` field allow-list needs a live
check first (`docs/multi-model.md` §5, Check 25). This is the structural
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
  table's `Why` column (`whyFiles`-style: "it changes the agent process's
  environment, and only the repository's own, PR-reviewed `fugaro.yaml`
  may do that").
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
    // The Check 25 fix itself (docs/multi-model.md §5, this design's origin).
    "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS",
    "MAX_THINKING_TOKENS",
    // Already told to users with no mechanism to apply it (docs/gcp-setup.md:257).
    "ENABLE_TOOL_SEARCH",
    // Telemetry/updater/non-essential-traffic knobs Claude Code documents.
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC",
    "DISABLE_AUTOUPDATER",       // already a baseEnv pass-through (env.go:24); repo-settable too
    "DISABLE_TELEMETRY",
    "DISABLE_ERROR_REPORTING",
    "DISABLE_BUG_COMMAND",
    // A small set for the project's own tools (commands.build/test see the
    // same environment, §4): conventional, non-routing, non-secret toggles.
    "CI", "FORCE_COLOR", "NO_COLOR",
}
```

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

**Value shape.** A plain string, printable ASCII only, no control
characters (`\n`, `\r`, `\0`, …) and no whitespace (every value above is a
single token: a boolean-ish `"1"`/`"true"`/`"false"`, a number, a short
word), at most 128 bytes — the same order of magnitude as `agent.model`'s
100-character cap (`validateAgentModels`, `validate.go:444-454`) and far
short of `maxSettingsFile`'s 1 MiB. Values are **never secret by
construction** (the allow-list excludes anything that could be): a
repository that needs a secret keeps using `workflows.*.secrets`
(`whySecrets`, unchanged). `Validate` refuses: a key not on
`AgentEnvAllowed`, naming the key (`agent.env.<NAME>`, "%s is not an agent
environment variable Fugaro allows; see docs/design/agent-env.md" — or
whatever doc section ships this), and a value failing the shape check.

**The three edits, plus the fourth** (`docs/design/headroom-compression.md`
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
   unknown key is a schema error too, not just a `Validate` one. A
   `testdata/config` corpus file exercises it
   (`TestFugaroSchemaCorpus`, `schemas/schemas_test.go:73`); a new
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

**Is `agent.env` an "executable" key?** No, by the same reasoning
`NonExecutableImageKeys` already states for `image.node` (`scope.go:140-143`,
"a pinned … version … never a command"): every value is a closed-set,
allow-listed literal `Validate` already shape-checks, consumed as an
environment variable Claude Code reads, never interpreted as shell, a URL,
or a tool name to fetch. This matters only if `agent.env` is ever proposed
for the project layer later (recommended against above): were that
reconsidered, it would not need `--executable-changes`, the same way
`image.node` doesn't — but that question does not arise at repo scope,
since `fugaro.yaml` is already PR-reviewed in full.

## 3. Provider-scoped variant: `providers.<name>.agent_env`

The origin bug is not really a repository's concern — `MAX_THINKING_TOKENS`
and `CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS` exist because **OpenRouter's
Anthropic-compatible endpoint**, not the repository's code, cannot be sent
`context_management` or extended thinking. That knowledge belongs with
whoever adds the provider — the **owner**, in the local config
(`docs/multi-model.md` §2) — not with every repository that happens to pin
a model the provider serves.

**The field.** `ModelProvider.AgentEnv map[string]string` (`\`yaml:"agent_env,omitempty\` \`json:"agent_env,omitempty"\``, `internal/config/providers.go:29-47`),
owner-only, never in `fugaro.yaml` — the same posture `providers:` itself
already has. Checked by `ValidateModelProviders`
(`providers.go:103-165`) with the **same** allow-list and shape rules as
§2's `agent.env` (one function, `validateAgentEnvMap`, shared by both
call sites) — a provider cannot set anything a repository couldn't set
either; it only gets to set it *for stages that use it*, automatically,
instead of every repository re-declaring it.

**How it reaches the job.** The existing channel, unchanged in shape:
`ProvidersEnv`/`ProvidersFromEnv` (`internal/runner/providers.go:14-78`)
already round-trip the whole `ModelProvider` through `FUGARO_MODEL_PROVIDERS`
JSON with `DisallowUnknownFields` and a re-`ValidateModelProviders` call; a
new field on the struct is carried for free. No new environment variable,
no new secret.

**How it applies: through the managed settings file, per stage — not
through `BuildEnv`.** This is the one place this design departs from §2's
mechanism, and for a concrete, code-grounded reason: a provider's
`agent_env` must apply **only to the stages whose role model that provider
serves** (the coder's stage when the coder is the provider's model, not the
Claude reviewer's), and `BuildEnv`'s result (`r.env`) is built once at
bootstrap and reused for every stage (§0) — it has no concept of "this
stage's model." The pins already solve exactly this problem for the model
name itself: `stagePins`/`beginGatewayStage`
(`internal/runner/gateway.go:51-58,142-146`) compute per-stage, per-role
values and `writeManagedSettings(pins)`
(`internal/runner/agentenv.go:65-84`) rewrites the managed-settings file
before every stage with them. A provider's `agent_env` is the same shape of
fact (stage-scoped, role-scoped) and should go into the same file, merged
into `vars` beside the gateway's own routing variables and the pins,
whenever `config.ProviderFor(providers, stageRoleModel)` resolves to a
provider with a non-empty `AgentEnv`. Two consequences fall out of reusing
this exact mechanism rather than inventing a second one:

- **It is only ever available when the gateway is on**, which a provider
  model already requires unconditionally (`CheckProviderGateway`,
  `providers.go:285-312`: a provider model without `budget.mode: enforce`
  is refused before any call) — so there is no new "what if the gateway is
  off" case to design for.
- **It wins over everything a repository's own `agent.env` or a committed
  settings file could set**, for the same reason the gateway's own routing
  variables already do: the managed settings file is Claude Code's
  highest-precedence source (§0). This is the concrete mechanism behind
  the precedence rule below, not a separate guarantee to build.

**Built-in runner defaults for known provider quirks, so the owner needs
nothing for the common case.** The fix this design's origin describes
(`CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS=1`, `MAX_THINKING_TOKENS=0` "for
stages whose model is served by a provider route") reads as a *property of
routing through any non-Anthropic provider*, not of one specific vendor —
every `anthropic-compat` endpoint (`config.ModelProviderKind`,
`providers.go:23`) is, by construction, not the real Anthropic API, so none
of them can be assumed to support extended thinking or beta headers until a
live check says otherwise (`docs/multi-model.md` §5's own "Claude Code
features that a compatible endpoint may not support… surface as the
upstream's error and fail the stage; which of them break is assumed until
Check 25 records it"). Recommendation: a small, hard-coded Go map,
`DefaultProviderAgentEnv` next to `ModelProviderKind`, applied to **every**
`anthropic-compat` provider's stages unless its own `agent_env` overrides a
key:

```go
var DefaultProviderAgentEnv = map[string]string{
    "CLAUDE_CODE_DISABLE_EXPERIMENTAL_BETAS": "1",
    "MAX_THINKING_TOKENS":                    "0",
}
```

This is the one piece of this design that is not "a generic mechanism, no
policy" — it is a specific, cited fix, defaulted on for every provider
route because the alternative (every owner rediscovering Check 25's
failure independently) is worse. A provider's own `agent_env` can turn a
default back off by repeating the key with a different allowed value (there
is no "off" sentinel needed: `MAX_THINKING_TOKENS` absent from a provider's
map simply keeps the default); nothing in `fugaro.yaml` can touch these two
keys for a provider-routed stage, by design — a provider route's thinking
behavior is a provider fact, not a per-repository one.

**Precedence, full stack, narrowest (and most specific) wins, matching
`BuildEnv`'s existing "last always wins" comment literally:**

1. Claude Code's own built-in defaults (lowest).
2. The repository's `agent.env` (§2) — process environment, set once at
   `BuildEnv` time, applies to every stage of the workflow.
3. `DefaultProviderAgentEnv`, for any stage whose model a provider serves.
4. That provider's own `agent_env`, for the same stages — overrides a
   built-in default's key.
5. The gateway's own routing variables and the per-stage model pins
   (highest) — written last into the same managed-settings file, never
   overridable by any of the above (existing behavior, unchanged).

Levels 3 to 5 live in the managed-settings file; levels 1 to 2 are the
process environment `BuildEnv` already builds. Because the managed-settings
file is Claude Code's highest-precedence source, this ordering holds
regardless of which physical channel (env var vs. settings-file `env` key)
Claude Code itself prefers when both are present — the open question
`docs/design/headroom-compression.md`-style designs have had to reason
about elsewhere; here it is moot, because levels 2 and 3-5 are already in
different channels today for an unrelated reason (bootstrap-time vs.
per-stage), and this design only adds a key to the channel that already
exists at each level.

## 4. Where each applies

| Surface | In scope? | Why |
|---|---|---|
| **The Claude Code process** | Yes — this is the entire point. | `BuildEnv` (§2) and the managed settings file (§3). |
| **The repository's check commands** (`commands.build`/`test`/`lint`/`fix`, run through `fugaro verify`) | Yes, automatically, with no separate design. | They are the agent's own Bash-tool children (§0), so they inherit exactly the environment `BuildEnv` built — any allow-listed `agent.env` entry a repository sets is visible to its own build and test commands too, for free. This is a feature a repository might actually want (`CI=true` steering a test runner's own branching) and costs nothing extra to support. |
| **The image build** (Cloud Build) | **No. Out of scope.** | Cloud Build's own job environment (`IMAGE`, `SECRET_ENVS`, `WORKFLOW`, `REPO_URL`, `BASE_BRANCH`, already reserved names, `validate.go:78`) is a different process, a different trust boundary (it runs before any agent, with the build's own secrets, never the gateway), and already has its own mechanism for a repository's needs: `workflows.*.image.setup`/`image.apt`/`image.tools`, all gated as `ExecutableKeys` (`scope.go:107-132`) because they run as shell. Folding env into that surface would either weaken an executable-key gate that already exists for a good reason, or create a second, non-executable-looking way to influence a step that is, in fact, arbitrary shell — rejected either way. |
| **Secrets** | **No. Out of scope**, unchanged. | `agent.env` values are never secret by construction (§2); a repository that needs a credential keeps using `workflows.*.secrets`. |

## 5. Compatibility

**`agentEnvSince`,** a gate of the same shape as `recipesSince`/
`namedTokensSince`/`layeredSince` (`internal/cli/recipes_skew.go:16-43`):
the first release whose runner's strict `fugaro.yaml` decoder knows
`agent.env` — an older binary refuses the key outright (the same "Binaries
before 0.6.5 refuse this key" comment pattern already on `OAuthToken`,
`config.go:167`). `fugaro run` must refuse a launch against an older job
image the same way `checkImageSince` already refuses one that doesn't know
`agent.recipe` or `OAuthToken`
(`recipes_skew.go:91-137`): a new `checkAgentEnvImage` wired the same way,
triggered whenever the resolved `fugaro.yaml` sets a non-empty `agent.env`.
`docs/release.md`'s "Before you tag" checklist gets one more line verifying
`agentEnvSince` equals the release that ships it, mirroring its existing
`recipesSince`/`namedTokensSince` entries.

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
4. **The provider-scoped variant actually fixes Check 25.** Re-run
   `docs/multi-model.md`'s Check 25 end to end, this time with
   `DefaultProviderAgentEnv` doing the work instead of the bespoke runner
   fix the origin story describes, and confirm the run no longer needs any
   repository- or owner-side workaround — the measurement that actually
   closes the loop this design opened with.

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
- **D4. `DefaultProviderAgentEnv` as a hard-coded default vs. requiring
  every owner to set it themselves.** *Recommended:* hard-coded default,
  overridable per provider (§3). *Veto alternative:* no built-in default,
  document the two keys in `docs/multi-model.md` and require every
  `providers:` entry to set them — rejected because it reproduces exactly
  the discovery cost (one failed live run per owner) this design exists to
  remove.
- **D5. Release placement.** See §8.

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
  here: this design never touches `internal/infra`.
  Recommendation: land it as its own small PR group within the 0.7.x line,
  sequenced only by ordinary merge-conflict hygiene with whichever of
  bucket-iam/base-image's `internal/config` edits lands first — not a hard
  dependency either way, just "check for a conflicting edit to
  `scope.go`/`validate.go` before starting," the same courtesy every
  concurrent config-touching design in this repository already extends
  (`docs/design/headroom-compression.md`'s plan does the same for `P3`'s
  `internal/agent/env.go` churn).
- **Gated on nothing live before it ships as opt-in.** Unlike
  `agent.compression` (gated on two live checks before it is "ever usable
  at all"), `agent.env`'s v1 scope (§2) sets process environment variables
  Claude Code already documents reading — there is no unverified
  third-party behavior to confirm first. The provider-scoped variant (§3)
  is different: `DefaultProviderAgentEnv`'s two keys are already confirmed
  by the origin story's own live run (Check 25's actual failure and fix,
  even though that fix itself predates and is not yet reflected in this
  codebase) — §6's check 4 re-confirms it end to end, but does not block
  shipping §3, since the provider surface is owner-only and off by default
  for every provider that doesn't set `agent_env` or isn't
  `anthropic-compat`.
