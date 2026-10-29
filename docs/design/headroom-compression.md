# Headroom: context compression in the agent loop

Status: design for review (2026-10-12), docs only — nothing in this design has
been built. Source: the owner's request to evaluate integrating Headroom
(Headroom Labs, Apache-2.0,
[github.com/headroomlabs-ai](https://github.com/headroomlabs-ai) — the name,
license and URL exactly as given in the owner's request; none of it was
independently confirmed to exist or resolve, since fetching Headroom's own
site was deliberately out of scope for this design, see below), a
third-party, open-source context-compression layer, into the agent loop to
cut token cost and usage-limit stalls. Target release: **0.8.0** (§5),
opt-in, with the feature itself gated on the live checks of §3 (can
Headroom's configuration honor the bypass list) and §5 (no outbound
network) before it is ever usable at all — the §6 measurement plan runs
only after that, against an already-shipped, already-opt-in feature, and
gates nothing but a future default-on proposal.

**v1 does not serve the usage-limit half of the stated motivation.** §1.1
refuses `agent.auth: oauth` until a live check settles whether a proxy can
sit in front of it at all, and `oauth` is exactly the auth mode with no
dollar cap and the usage-limit stalls this feature was partly meant to
relieve (`plugin/skills/working/reference/diagnose.md`'s existing
"usage limit" account-switching logic exists because of it). §1.1 and §4 now
also restrict v1 to `agent.auth: api-key` with the budget gateway on
(`budget.mode: observe` or `enforce`) — not `vertex` either (§1.1). So this
design, as it stands, only addresses the token-cost half of the motivation,
for `api-key` workflows with the gateway already on; it says so plainly here
so the summary doesn't overstate what was asked for.

**What is verified and what is not.** Everything about *Fugaro* below is read
from the code at this branch's base and cited by file. Everything about
*Headroom itself* beyond the one-paragraph description in the owner's request
(Apache-2.0; a proxy, CLI wrapper, MCP server or library; Python and Rust
with a TypeScript SDK; vendor claims of 60-95% fewer tokens on structured
data, about 20% on coding agents, no quality loss on its own benchmarks) is
**UNVERIFIED**: this design was written without fetching Headroom's own docs,
README or source (the task's own instruction), so every claim attributed to
the vendor is marked so, not asserted as fact, and nothing here should be
read as confirming Headroom does what it says. This includes claims this
design itself infers from that one paragraph, not only claims stated as the
vendor's own — for example, "no Go SDK exists" below is this design's
inference from "Python and Rust with a TypeScript SDK," not an independently
confirmed fact about Headroom's actual release artifacts.

## 1. Integration options

### 1.0 What Fugaro's runner can actually see

This bounds every option below. Claude Code is a closed, proprietary binary
(`docs/design/base-image.md` §6: "proprietary (already redistributed
today)"). Fugaro never drives its tool calls directly — Bash, Read, Edit,
Write, Grep and Glob are Claude Code's own built-in tools, executed inside
the `claude` process, not something the runner mediates
(`internal/agent/relay.go`'s `fileToolField` and `bashSubcommands` tables
are built from *Claude Code's* tool names, read only after the fact from its
stream-json output). Fugaro's `agent.Claude.Run`
(`internal/agent/agent.go:105-169`) launches `claude -p --output-format
stream-json --verbose --dangerously-skip-permissions [--resume|--session-id]
[--append-system-prompt] [--model] [--max-budget-usd] [--json-schema]`
(`Args`, `agent.go:77-97`; `--append-system-prompt` is set from
`req.AppendSystemPrompt` at `agent.go:85`, which `runner.go:1136-1137` fills
with `SystemPrompt`'s run contract — so that is how `SystemPrompt`'s output
reaches Claude Code, which matters for §3's bypass list) as a child process
via
`procgroup.Run`, with the prompt on stdin and a **scrubbed environment**
(`agent.BuildEnv`, `internal/agent/env.go:51-153`, an allowlist — `baseEnv`
plus auth variables plus declared workflow secrets). `Claude.Run` itself
refuses a nil `req.Env` outright (`internal/agent/agent.go:99-108`: "req.Env
must not be nil: procgroup treats a nil Env as 'inherit the whole runner
environment', which would hand the permission-bypassed agent every
credential the runner holds" — a distinct check from `BuildEnv`, in a
different file, which an earlier draft of this paragraph conflated). The
runner reads
only `claude`'s stdout, as a stream of `stream-json` events
(`ParseStream`), through `Relay` for a live log view
(`internal/agent/relay.go`) and a `Redactor` for secrets. **The runner has
no API into what Claude Code keeps in its own context window.** Its only
levers are: the argv Fugaro builds, the environment Fugaro sets, and
whatever the *network* sees when Claude Code calls out to an LLM API.

So "compress tool outputs, logs, files and retrieval chunks before they
reach the model" can only mean one thing for Claude Code specifically:
**intercept the HTTP calls Claude Code makes to the model API**, since that
request body is the one place the full, accumulated conversation — every
earlier tool result Claude Code chose to keep — is serialized and visible
outside the closed binary. Each of the four shapes the owner named is
evaluated against that fact.

### 1.1 Proxy (`ANTHROPIC_BASE_URL`-style)

**This is the only shape that touches what the owner's request describes.**
A proxy sits in front of the model API, sees every outbound request body
(the full running context) and every response, and can rewrite the body
before forwarding it.

Fugaro already has exactly one thing in this position: its own budget
gateway (`internal/gateway`, package doc: "a loopback proxy between Claude
Code and the model API that holds the real credential"). It is **not** a
sidecar process — it is an in-process Go HTTP server, started as a
goroutine on an ephemeral loopback port (`gateway.Start`,
`internal/gateway/gateway.go:192-230`: `net.ListenConfig.Listen(ctx, "tcp",
"127.0.0.1:0")` at line 202, then `go func() { _ = s.srv.Serve(ln) }()` at
line 228), torn down with the
run's context. `internal/runner/gateway.go:105-120` points Claude Code at it
by setting `agent.Gateway{BaseURL, Token}`, which `GatewayVars`
(`internal/agent/env.go:169-201`) turns into `ANTHROPIC_BASE_URL` and
`ANTHROPIC_API_KEY` (the gateway's per-run token, never the real key) for
`auth: api-key`, or the Vertex equivalents for `auth: vertex`.

**`auth: oauth` never goes through it, by explicit code, twice:**
`BuildEnv`'s oauth case (`env.go:106-109`: `if spec.Gateway != nil { return
..., fmt.Errorf("auth: oauth never goes through the gateway") }`) and
`GatewayVars`'s own oauth case (`env.go:195-196`, the identical error). This
is a **Fugaro design choice** — `SECURITY.md` says "With `auth: oauth` there
is no gateway, only the token cap and the kill switches" (not verbatim "oauth
has no gateway" as an earlier draft of this paragraph paraphrased it; the "no
dollar cap to meter" rationale is this design's own inference, not
SECURITY.md's text) — not a technical limit of Claude Code or Anthropic's API.
Whether Claude
Code's CLI still honors `ANTHROPIC_BASE_URL` when `CLAUDE_CODE_OAUTH_TOKEN`
is set — i.e., whether a proxy could sit in front of an oauth-authenticated
session at all — is **UNVERIFIED**: nothing in this codebase exercises that
combination, and `docs/design/base-image.md` §6's env-variable table lists
`ANTHROPIC_BASE_URL` for Claude Code generally, without saying whether it
composes with the OAuth token. This is Headroom integration's first open
question, and it has a sharp security edge: Anthropic's OAuth bearer token
is the subscription account's own live credential
(`docs/design/named-oauth-tokens.md` §6c, "Fugaro has no way to scope what a
`claude setup-token` token can do (it is Anthropic's own credential, unlike a
GitHub App installation token or a Bitbucket repo-scoped token)"). A proxy
terminating in front of it would see that bearer token on every
request header — a new kind of exposure that does not exist today, since
oauth traffic currently has **no intermediary at all** (§2 goes into this).

**`auth: vertex` is refused too, for a parallel reason.** Vertex routing does
not use `ANTHROPIC_BASE_URL` at all: `GatewayVars`'s vertex case sets
`ANTHROPIC_VERTEX_BASE_URL` to `base + "/v1"` and
`CLAUDE_CODE_SKIP_VERTEX_AUTH=1` (`internal/agent/env.go:185-192`), a
different variable and a different path shape from the `api-key` case. A
design that only ever rewrites `ANTHROPIC_BASE_URL` would leave a Vertex
run's traffic going straight to the gateway (or straight to Vertex, with the
gateway off), **silently skipping Headroom** — the opposite of the fail-closed
posture §1.5 argues for. Whether Headroom can proxy Vertex's request/path
shape at all is a second, independent UNVERIFIED question (Vertex's wire
format is not identical to the direct Anthropic API's). **v1 refuses
`agent.compression` with `agent.auth: vertex`**, the same way it refuses
`oauth` — not because Vertex is technically ruled out, but because nothing
in this design has checked it, and a design that silently bypasses itself on
one of three auth modes is worse than one that refuses that mode outright.

**With `auth: api-key`, compression requires the gateway to already be on.**
This narrows v1 further than the original draft of this design, for two
reasons found independently of each other:

1. **Without the gateway, Headroom would see the real `ANTHROPIC_API_KEY`.**
   The gateway only runs when the budget is on:
   `gatewayOn()` is `r.spend.On() && auth is api-key or vertex`
   (`internal/runner/gateway.go:46-48`). Budget mode **defaults to off**
   (`SECURITY.md:34`, "there is no gateway and no per-call cap: the real
   model key sits in the agent's environment like any other secret"), and
   with the gateway off, `BuildEnv`'s `api-key` branch puts the **real**
   `ANTHROPIC_API_KEY` straight into the agent's environment
   (`internal/agent/env.go:91-105`). A Headroom sidecar chained in front of
   that traffic would see the real key in every request header it
   terminates — not a gateway-minted, scoped token, the actual credential.
   Requiring the gateway (`budget.mode: observe` or `enforce`) as a
   precondition for `agent.compression` closes this outright: Headroom then
   only ever sees the gateway's own per-run token
   (`agent.Gateway{BaseURL, Token}`, `internal/runner/gateway.go:105-120`),
   which is already the design's existing posture for every other
   `api-key` run.
2. **It also fixes the managed-settings problem below for free**, because
   the check that already guards against a routing bypass only runs when
   the gateway is on (next section) — requiring the gateway for compression
   means that check is already active on every run compression could touch,
   with no new condition to add at its call site.

So **v1 is `agent.auth: api-key` with `budget.mode: observe` or `enforce`
only.** `vertex` and `oauth` are both refused outright until their own,
independent questions are answered (this section, and §1.1 above).

### 1.1a The managed settings file, not only the environment, decides routing

Setting `agent.EnvSpec.Compression`'s URL (the sketch the rest of this
section assumed) is **not enough by itself** — a gap found only by reading
`internal/runner/agentenv.go`, not `internal/agent/env.go`:

- **The runner writes Claude Code's *managed* settings file before every
  stage, and the managed file wins over the process environment.**
  `writeManagedSettings` (`internal/runner/agentenv.go:65-84`) calls
  `agent.GatewayVars` itself — independently of whatever `BuildEnv` put in
  the agent's environment — and writes the result (`ANTHROPIC_BASE_URL`,
  the gateway's token, the stage's model pins) into
  `/etc/claude-code/managed-settings.json`, which Claude Code treats as its
  highest-precedence settings source, overriding both a repository's own
  settings and the process environment. So if `EnvSpec.Compression` alone
  were changed to point the agent's *environment* at Headroom, the managed
  *file* — written separately, by different code, from the same
  `GatewayVars` call — would still carry the gateway's own
  `ANTHROPIC_BASE_URL`, and Claude Code would read that, not the
  environment. Headroom would be silently bypassed by the very file the
  gateway's own routing protection relies on.
- **`checkSettingsRouting` protects the gateway, not Headroom, and only
  runs when the gateway is on.** `internal/runner/agentenv.go:108-159`
  refuses any *other* settings file (a repository's `.claude/settings.json`,
  the user's, `$CLAUDE_CONFIG_DIR`'s) that sets a routing key — but it is
  called only `if r.gatewayOn()` (`internal/runner/runner.go:1434-1439`,
  and again at `internal/runner/gateway.go:358-360`), and it never inspects
  the managed file itself (the runner's own file is trusted by
  construction). Because v1 requires the gateway on whenever compression is
  on (above), this check already runs on every compression-eligible run —
  but only because of that new precondition, not because anything here
  originally wired it to compression.

**The fix this design now requires:** `writeManagedSettings` must write
Headroom's URL (chained in front of the gateway's, per §1.1's recommendation)
into the managed file whenever compression is on for the stage, the same
call that already writes the gateway's own variables — not a second,
independent write. `checkSettingsRouting`'s existing behavior needs no new
condition once compression requires the gateway, but the plan's tests must
prove that explicitly (a committed `.claude/settings.json` setting
`ANTHROPIC_BASE_URL` is refused exactly the same whether compression is on
or off), rather than assume it. §7's plan (Task 3/4) carries this.

**With the gateway required, a Headroom proxy has to be placed relative to
it, and the placement changes what breaks:**

- **Chained in front of the gateway** (`ANTHROPIC_BASE_URL` → Headroom →
  Headroom's own configured upstream → the gateway's loopback URL). This
  keeps the gateway as the last hop before the real key (today's invariant:
  `internal/gateway/gateway.go`'s package doc, "holds the real credential"),
  and Headroom now sits between Claude Code and the gateway's own request
  parser. **The budget risk here is narrower than an earlier draft of this
  section claimed.** The gateway reserves and settles against exactly the
  body it receives (`internal/gateway/gateway.go`'s package doc: "it
  reserves each call's worst case against the run's cap before sending it,
  and settles the call from the usage the response reports") — since
  Headroom sits *upstream* of the gateway in this chain, the gateway prices
  whatever (possibly smaller) body actually arrives, correctly and with no
  drift between what it reserved and what it settled. The real risks are
  different:
  1. **The gateway's own shape and model-pinning checks may simply refuse a
     rewritten request.** The gateway is built to recognize *the actual
     Anthropic request/response shape* ("the request shapes the price table
     can bound", `gateway.go`'s package doc); if Headroom's rewriting
     changes the body's shape in a way the gateway's parser or its pin
     checks (`CheckPins`, `CheckAllowed`) don't recognize, the result is a
     403 and a failed stage — a reliability/compatibility risk, not a
     billing one, but one that needs a live check (§6) before it is
     trusted.
  2. **Headroom making its own model calls bypasses the budget entirely.**
     If Headroom's compression logic itself calls an LLM to decide what to
     summarize (UNVERIFIED whether it does, §2), those calls do not pass
     through Fugaro's gateway at all — they are neither capped by it nor
     visible in `result.json`'s cost fields, and (§2) they would need their
     own credential, which is a second, unaccounted spend sink this design
     has no answer for beyond requiring §2's no-outbound-network property,
     which would also rule this out if confirmed.
- **Replacing the gateway** (Headroom talks to Anthropic directly, no
  Fugaro gateway in the loop). Rejected outright: it would remove budget
  enforcement for every repository that currently relies on it, which is
  exactly the invariant `SECURITY.md`'s "What limits the blast radius today"
  calls out ("the gateway... charges every call against the caps"). Not
  considered further.
- **Inside the gateway's own forward path** (Fugaro's Go code in
  `internal/gateway/forward.go` calls out to Headroom itself, rather than
  `ANTHROPIC_BASE_URL` routing around it). This is the shape that keeps the
  gateway as the sole arbiter of what reaches Anthropic, but Headroom has
  **no Go SDK** (this design's own inference from the owner's description —
  Python and Rust with a TypeScript SDK only — not an independently
  confirmed fact about Headroom's release artifacts) — this codebase cannot
  embed it as a library (§1.4), so "inside the forward path" still means an
  out-of-process call (HTTP or a Unix socket) to a Headroom process, not a
  Go function call. Mechanically it is a sidecar either way; the only
  question this placement changes is which process initiates the hop.

**Recommendation (H1):** a standalone Headroom process (its own OS process,
an HTTP server on loopback — see §1.5 for why this cannot be in-process like
Fugaro's own gateway), chained **in front of** the gateway, required to
already be on (`budget.mode: observe`/`enforce`), never replacing it; and
**refused outright with `agent.auth: oauth` or `agent.auth: vertex`**, not
only `oauth` — until, respectively, the live check of §1.1
(`ANTHROPIC_BASE_URL` plus `CLAUDE_CODE_OAUTH_TOKEN`) and a separate live
check of whether Headroom can proxy Vertex's request shape are both
answered. This also means v1 **does nothing for usage-limit stalls**, which
are an `oauth`-only problem (see the summary at the top of this document);
the veto alternative below is the only path in this design that would ever
reach them. *Veto alternative:* refuse `api-key` too, and only ever reach
Headroom from Go code inside `forward.go` via an explicit round trip Fugaro
controls end to end (more control over what crosses the boundary, a larger
and slower implementation, and — unlike the recommendation — a path that
could in principle be extended to `oauth` later, since it never depends on
`ANTHROPIC_BASE_URL` at all; see
`plugin/skills/working/reference/diagnose.md`'s existing "usage limit"
handling for why that would matter).

### 1.2 CLI wrapper

Mechanically trivial to wire: `agent.Claude{Bin: "headroom-claude"}` would
substitute the binary `procgroup.Run` executes
(`internal/agent/agent.go:109-112`), and `Args(req)` is unchanged since it
only builds Claude Code's own flags. But Fugaro's parser
(`ParseStream`/`agent.Result`) depends on an **exact** `stream-json` stdout
contract (`Usage`, `ModelUsage`, `CostUSD`, the result event) — a wrapper
that is not a byte-transparent passthrough of that stream is a compatibility
risk the runner can only detect as "claude exited with code %d without a
result event" (`agent.go:166`), the same failure mode as a crash.

More fundamentally: a CLI wrapper has no hook into "tool outputs before they
reach the model" **unless it also intercepts HTTP**, because Claude Code's
context representation lives inside its own closed process (§1.0). A
wrapper that reduces to "launch an embedded proxy, then exec into `claude`
with `ANTHROPIC_BASE_URL` pointed at it" is not a distinct integration
surface from §1.1 — it is the proxy, packaged as one binary. **This mode is
plausible only as a convenience packaging of the proxy, and is UNVERIFIED
as anything else** (Headroom's CLI-wrapper mode may exist for agents it runs
as a subprocess with direct visibility into tool calls — unlike Claude Code,
which is not driven as a library). Recommendation: treat it as equivalent to
§1.1 if adopted; do not design separate runner machinery for it.

### 1.3 MCP server

Would only compress what flows through **MCP tool calls** Claude Code makes
to it — and Claude Code's native MCP support is already part of this
codebase's trust model, not a hypothetical to design around. A repository
can declare MCP servers in `.mcp.json`, which Claude Code loads from the
**working tree** (the PR's branch for a follow-up, not the base branch) and
can run or call with the job's full environment and secrets;
`docs/design/v1.md:862` names this directly as a residual risk ("Hooks and
MCP servers planted there run with the job's environment and its secrets...
review the PR's diff with it in mind before launching a follow-up on it"),
and `internal/pluginwire/notices.go`'s `mcpNotices` (lines 32-39) exists
specifically to flag a repository's `.mcp.json` on `fugaro init`'s diff
screen ("the repository has a .mcp.json, which declares MCP servers
(commands or services Claude Code can run or call): read it before trusting
them"); the same file also flags a settings file's
`enableAllProjectMcpServers: true` as "every MCP server the repository
declares is trusted without asking." So Claude Code calling MCP tools is a
real, already-documented path, not something this design would have to
introduce.

What is still true, and is the actual reason this mode doesn't help: **none
of the tool traffic this design is trying to shrink goes through MCP.**
Bash, Read, Edit, Write, Grep and Glob — the tools every stage's
implement/fix/review loop actually generates its volume with — are Claude
Code's native, built-in tools, never MCP tool calls, and no Fugaro-authored
recipe or base-image convention wires an MCP server into that path
(`docs/design/base-image.md` §6's harness table lists no MCP bridge). A
Headroom MCP server would only ever see traffic from an MCP server a
**repository itself** declared — exactly the untrusted, residual-risk path
`v1.md` §6 already warns about, not Fugaro's own tool-execution volume — so
fronting that path with compression does nothing for the traffic this
design is actually about. Making it do more than that — routing Fugaro's
own built-in tool calls through an MCP intermediary — would be a materially
bigger and different project, closer to generic-tool.md's deferred
"pluggable control plane" (§11 there) than to anything proposed here.
**Recommendation unchanged: out of scope for v1** — not because MCP is
foreign to this codebase, but because an MCP-server-shaped integration
doesn't reach the tool calls this design needs to shrink.

### 1.4 Library

**UNVERIFIED, inferred from the owner's own description, not independently
confirmed: no Go SDK exists** (Python, Rust, a TypeScript SDK are the three
named). The runner is Go; if that inference holds, embedding is not
available to it. A
repository's own application code could adopt Headroom as an ordinary
dependency for its own purposes (compressing its own RAG chunks, say) with
**zero Fugaro involvement** — any project can already `mise use -g` a
Python or Rust tool and use it in `commands.build`/`commands.test`/a
script. **Recommendation: not a Fugaro integration point.** Nothing to
design; a project is already free to do this today.

### 1.5 The runner's process model: a true sidecar, not another in-process gateway

Fugaro's existing gateway is deliberately simple to supervise because it is
Go code the runner process owns directly: a goroutine, started and stopped
with `context.Context`, with no separate lifecycle to manage
(`gateway.Start`/`Server.Close`). Headroom cannot be that, because it is not
Go. It must run as a **separate OS process** the runner starts, probes for
readiness, and stops — closer in shape to the harness binaries in the base
image (`docs/design/base-image.md` §6) than to anything the runner currently
supervises.

Two existing patterns almost fit, and neither is quite right:

- **`procgroup.Run`** (`internal/procgroup`), used for `claude` itself and
  for `commands.build`/`test`/`fix` via `fugaro verify`, is a **blocking,
  one-shot** launcher: it runs one command to completion (or until `ctx`
  ends) and returns. It is not built for a long-lived daemon that must
  outlive one `claude` invocation and serve several stages of one run.
- **`fugaro-services`** (`docs/design/base-image.md` §5, "the
  `java-services` replacement") is the closest precedent for "a background
  process the container runs on loopback, started as the unprivileged
  `fugaro` user" — but it is **agent-invoked**: the coder runs `fugaro-services
  start` itself, inside `commands.test`, as a convenience for its own test
  run. Headroom is the opposite shape: it must be running **before the
  first stage starts** (every model call needs it, if it's on at all), the
  runner — not the agent — must start it, and its failure is not "a test
  using Postgres fails," it is "every model call in this run is now
  unrouted."

**This design proposes new runner machinery**, modeled on `gateway.Start`'s
shape but launching a real subprocess — and it must start **after**
`startGateway` succeeds, not before, which an earlier draft of this section
had backwards. §1.1's recommendation is to chain the sidecar in front of the
gateway, which means Headroom's own upstream configuration must be the
gateway's loopback URL — and that URL doesn't exist until
`gateway.Start` (`internal/gateway/gateway.go:192-230`) returns it. There
is no way to tell Headroom where to forward before the gateway it forwards
to has a listening address. Concretely: call `startGateway` first, read
`r.gwAgent.BaseURL` (already set by it,
`internal/runner/gateway.go:105-120` — **not** `.Token` too, which an
earlier draft of this paragraph also passed in: Claude Code's own
`ANTHROPIC_API_KEY` already carries the gateway's token on every request, so
an ordinary reverse-proxying Headroom forwards it without needing a separate
copy, §2), pass it into the sidecar's own
configuration (its `Env`, or whatever file it reads — Task 7's own
artifact-resolution step, not a live check, settles which), then start the
sidecar and wait on its readiness probe (a
local health endpoint, since nothing here should depend on network access —
see §2) before the first stage runs. Kill its process group on run end
exactly as `procgroup` does for `claude` (SIGTERM, then SIGKILL after a
grace period), so a leaked descendant cannot survive the container. This is
new code, not a reuse of an existing primitive, and it sits next to the code
SECURITY.md already
names as in scope ("the runner's use of provider and model tokens") — §7's
plan flags it for a security-focused review.

**Failure mode: fail closed, not fall back to direct (recommendation,
H2).** If the sidecar does not start, or dies mid-stage, the stage — and the
run — fails, rather than silently sending Claude Code's calls straight to
the gateway or the real API. Rationale:

1. **Silent fallback is exactly layered-config.md's "classic failure
   mode"** (`docs/design/layered-config.md` §1: "a value set at project
   level that a repository silently fails to pick up"). A repository that
   turned compression on to manage its token budget would have no idea a
   run quietly reverted to uncompressed, possibly exceeding a token cap
   (`agent.max_run_tokens`) or a dollar cap it was sized against with
   compression in mind — made sharper by §3's prompt-caching risk, where a
   silent reversion mid-session could change the run's cost in either
   direction without anyone deciding so.
2. **The feature is opt-in and default off (§4),** so failing closed never
   affects a repository that did not choose it — unlike, say, the gateway
   itself, which is load-bearing for every budgeted run. An opt-in feature
   can afford a stricter failure mode than one everybody depends on.

*A claim this design's earlier draft made and retracts on review:* a crash
mid-session does **not**, by itself, leave the model holding an
"inconsistent context." Headroom sits on the wire, not inside Claude Code's
own state: Claude Code resends its full, uncompressed message history with
every request (how the Messages API and prompt caching both work), and
Headroom (if it works the way a stateless rewriting proxy would) compresses
each outgoing request fresh, from whatever Claude Code's own history
currently is — it does not mutate anything Claude Code itself remembers. A
crash simply means the *next* request onward goes out at full size; nothing
about the model's own continuity is broken. The real cost of a crash is
dollars (point 1), not correctness.

*Veto alternative:* fall back to a direct connection (no compression,
gateway or real API reached as today) on sidecar death, logged loudly. **This
is not the "one-line code change" an earlier draft of this section called
it.** Claude Code's `ANTHROPIC_BASE_URL` is part of the fixed environment
`procgroup.Run` hands the process at exec time (`internal/agent/env.go`'s
`BuildEnv`, built once before `claude` starts); there is no running-process
hook to repoint it **mid-stage**. **Between stages it is a different
question**, worth stating so the claim above isn't read more broadly than it
should be: `writeManagedSettings` already rewrites the managed settings
file before every stage (§1.1a), so a dead sidecar discovered at a stage
boundary could, in principle, have the *next* stage's `claude` process
started pointed straight at the gateway instead — no restart of anything
already running, since nothing is yet running for that stage. This does not
change the conclusion: a crash is far more likely to be discovered
*mid*-stage, while a stage's `claude` process is already running and already
holds the pre-crash `ANTHROPIC_BASE_URL`, which is the case with no cheap
fix, and treating "between stages" as a reliable recovery path would still
leave mid-stage crashes needing a restart or a hot-spare proxy — real
implementation work either way. A real fallback for the mid-stage case means
either restarting the stage (a new `claude --resume` invocation with a
different environment, losing whatever state a mid-flight tool call held) or
running a *second*, minimal pass-through proxy at the same loopback address
Headroom was using, promoted automatically on crash detection, so Claude
Code's own environment never needs to change. Either is real implementation
work, not a flag flip; rejected for v1 given point 1, but a real option for
a future revision that wants availability over the stricter guarantee.

## 2. Security and trust

**The starting posture, from `SECURITY.md`.** The agent already runs with
every permission prompt bypassed (`--dangerously-skip-permissions`), shares
the container and its user with the runner, and can already reach "the real
model and provider keys" and "the job's service account, through the
metadata server" via `/proc` (`SECURITY.md`, "What one run's agent can
reach"). Headroom running in the same container, as the same unprivileged
`fugaro` user, adds **no new privilege boundary to cross** — it is already
inside the blast radius SECURITY.md describes as the trust boundary:
`SECURITY.md`'s "Out of scope" list (the Scope section) names "An agent
doing harmful things inside its own container with the credentials that
job's service account is allowed to hold: the trust boundary is the
container, and the design says so openly" — an earlier draft of this
sentence spliced the heading's own words "is out of scope" onto that quote
as if they appeared in it, which they don't; the heading says it, the
sentence under it doesn't need to repeat it. What it *does* add is new: a
second network-facing
process that sees every byte of model traffic, including the live OAuth
bearer token if §1.1's open question resolves in favor of routing oauth
through it, and a new place a credential could leave the container if
Headroom's own compression logic calls out anywhere.

**The sidecar's own environment must be an explicit allowlist, not the
runner's.** The runner's process environment, at the point it would launch
Headroom, holds the GitHub App's private key, real model and provider keys,
and can reach the job's service account through the metadata server
(`SECURITY.md`, "What one run's agent can reach"); none of that is Headroom's
business, and a naive `os/exec` launch that inherits the parent's
environment (the same mistake `agent.Claude.Run` refuses by requiring a
non-nil, pre-built `Env`, `internal/agent/agent.go:101-108`) would hand it
all of it. §7's plan requires the sidecar's launcher to build its own,
narrow environment the same deliberate way `agent.BuildEnv` does, plus
whatever telemetry-off variables Headroom's own configuration surface
exposes (UNVERIFIED whether any exist). **Its logs are a channel worth
bounding, but not simply discarding** — a correction to an earlier draft of
this paragraph, which said "discard entirely." The sidecar's stdout/stderr
may be the *only* way the runner learns why it failed to start (§1.5's own
plan has the sidecar's own process report its chosen address or an error on
stdout, which the runner must read to know anything went wrong at all), so
discarding it outright would also discard the one diagnostic a failed launch
produces. The recommendation is a small, bounded, **redacted** tail (the
same shape `internal/logtail` already gives verify output elsewhere in this
codebase — a fixed number of lines, a fixed width, redacted before it is
ever shown) kept only long enough to report a startup or crash reason, never
relayed live to Cloud Logging and never kept once the run ends. Separately,
confirm before shipping whether Headroom persists anything to disk (a cache,
a log file) that could hold source code or request content past the run.

**A fixed port invites impersonation — and the fix has a limit worth
stating plainly.** If the sidecar's address were a fixed, well-known
loopback port, a command that runs *before* the agent starts — the check
gate (`docs/design/generic-tool.md` §3.6's `check` step, which runs
`commands.build`/`test`/`fix` as the agent user, with the agent's own
environment, ahead of `implement`) — could bind that port first and receive
whatever Claude Code or the gateway sends there, including a gateway or
compression token. §1.5 already specifies an ephemeral port (`gateway.Start`'s
own pattern, `"tcp", "127.0.0.1:0"`); this adds the other half, which the
original draft omitted: the runner's readiness probe must confirm the
listener on that port is the process the runner itself started (a per-run
random token the probe must see echoed back), not merely that *something*
is listening and answers 200. **This defends against a race with a
different, less-privileged process (the check gate's commands) — it does
not, and cannot, defend against the agent itself.** The agent shares the
sidecar's container and, in the common case, its own unprivileged user, so
it can already read `/proc/<pid>/environ` of any process that user owns,
sidecar included — exactly the same residual SECURITY.md already states for
every other secret in the container ("it shares the container and its user
with the runner, so it can also read the runner's environment through
`/proc`"). The origin-proof token stops a stale or impersonating listener,
not a well-behaved agent reading the sidecar's own environment directly;
this is not a new gap, it is the existing trust boundary applying here too.

**Hangs and resource limits are unaddressed by §1.5 as written.**
`headroomproc.Process.Dead()` (§7's plan) only catches the process
*exiting*; a sidecar that hangs (stops responding without crashing) is
invisible to it, and §1.5's fail-closed posture only protects against the
case it detects. The plan needs a liveness check (a periodic health probe
with a timeout, not only a one-time readiness probe at startup) and a
latency bound on the forwarding path itself. **Memory is a second,
unaddressed bound**: Cloud Run gives one container one memory limit
(`workflows.*.resources.memory`, `internal/config/config.go:398`), shared by
the runner, the agent and, now, the sidecar — nothing in this design yet
stops Headroom from using enough memory (summarizing a very large tool
output, say) to OOM-kill the whole container, taking the run down with it
regardless of whether the stage itself needed that much memory. **A `cgroup`
limit is not an option here, and an earlier draft of this paragraph was
wrong to propose one**: a process group (what `procgroup` manipulates for
signal delivery) is a Unix job-control concept, unrelated to Linux cgroups,
and creating or writing to a cgroup needs privileges or a delegated
subtree an unprivileged Cloud Run container user does not have. The
realistic options, neither verified to actually bound Headroom: an
`RLIMIT_AS`/`prlimit` address-space limit set on the child before exec
(unreliable for a Python or a garbage-collected Rust runtime that reserves
large virtual address ranges up front without committing them, which can
make `RLIMIT_AS` trip on memory the process never really uses, or fail to
trip on memory it does), or a memory flag Headroom's own configuration
surface might expose (UNVERIFIED whether one exists). §7's plan picks
between these once the real binary is examined, and needs the liveness
check above regardless, since neither memory option is a substitute for
detecting a hang.

**Supply chain.** Pin Headroom exactly as the base image already pins every
other third-party binary (`docs/design/base-image.md` §7, "Pinned... by
version and per-architecture sha256 as `ARG` lines"): a specific release,
and a sha256 per architecture, as `ARG HEADROOM_VERSION=…` and `ARG
HEADROOM_SHA256_AMD64=…`/`_ARM64=…` in `images/base/Dockerfile`, resolved
and verified the same way the monthly bump script
(`images/base/bump.sh`) verifies Claude Code and the other harnesses — never
installed at run time. **No `mise` tool.** `mise` is the sanctioned path for
a *project's own* runtime choices (base-image.md §4); Headroom is not a
project dependency, it is trusted infrastructure that sees every model call
regardless of which repository is running, so it belongs in the base image's
own harness/kit layer, built once, reviewed once, and bumped on the same
monthly cadence as Claude Code itself — the same reasoning base-image.md
already applies to Claude Code, the Node-based harnesses and the Docker CLI.
§5 covers exactly where in the layering.

**What Headroom ships as matters for this pin.** The owner's description
says Python and Rust with a TypeScript SDK; it does not say whether a
standalone, dependency-free native binary is published. Base-image.md
deliberately ships **no system Python with pip** (§3: "`/usr/bin/python3` is
Debian's... with no `pip`") specifically to avoid exactly the kind of
runtime-with-package-manager sprawl a Python tool invites, and excluded
Aider and Kimi CLI from the harness table for precisely this reason ("Python
applications; they would need a private Python runtime. A candidate for a
later bump," base-image.md §6). **UNVERIFIED: whether Headroom publishes a
self-contained Rust/native binary with no Python runtime dependency.** If it
does, that is the artifact to pin (same shape as the Goose, OpenCode and
Crush tarballs, base-image.md §6's harness table). If it does not — if
running it means a Python interpreter, a virtualenv and pinned wheels — this
is materially heavier than any other piece of this design and should be
weighed against the measured benefit (§6) before committing to it; the base
image has avoided this shape for every other tool to date.

**Telemetry and outbound network: off by default, and tested, not assumed.**
The vendor's claims (compression ratios, "no quality loss") are UNVERIFIED
and, more importantly, whether Headroom's *compression itself* ever calls
out to a remote service (a hosted summarization API, a telemetry endpoint,
a license check) is **UNVERIFIED and load-bearing**. If compression needs a
call to an external LLM or service to decide what to keep, that is a
**second place code or secrets can leave the container**, squarely the kind
of thing `SECURITY.md`'s "Network egress is not restricted... Prompt
injection... could send source code or the credentials above anywhere"
already warns about generically — except here it would be a *designed*
egress path, not an incidental one, and it would need its own line in
SECURITY.md rather than hiding inside "egress is open." **This design's hard
requirement: Headroom runs with outbound network access denied by default —
but this can only be a *tested* property, not a *runtime-enforced* one, and
the two must not be conflated.** Cloud Run's job container runs as an
unprivileged user with no path to its own network namespace or firewall
rules, and `SECURITY.md:58` already says plainly, for the container as a
whole, that "egress is not restricted. A run can reach any host." Nothing
in this design gives Fugaro a way to actually *block* an outbound call the
sidecar's own binary decides to make at run time — the base image's
`--network none` smoke (`docs/design/base-image.md` §6, "Presence checks")
only proves a tool doesn't *need* the network to answer `--version`; it is
not a sandbox the shipped container runs inside. The honest claim this
design can make is: **verified, pre-ship, that an actual compression pass
completes with no route to the public internet** (a CI docker test that
runs the real pinned binary through a real request/response pair under
`--network none`, not merely its `--version` check — §7's plan adds this as
a distinct task from the ordinary presence check) — not that the shipped,
running sidecar is prevented from reaching the network if its own code ever
tried to. **If that pre-ship test fails — if compression needs a hosted
service call — this design's recommendation changes to: do not ship it**,
since "data never leaving the container" is a stated requirement of this
task, not a nice-to-have, and a compression layer that itself phones home
would be strictly worse for data exposure than the problem it solves. But
that recommendation rests on a test passing once, not an enforced guarantee
that holds for every future version of a third-party binary this design
does not control.

**(2026-10-27: that pre-ship test does not exist and cannot yet be written —
Task 7's revised run pinned no artifact, so there is nothing for it to run
against. This paragraph's "verified, pre-ship" claim and its "do not ship it"
failure mode are therefore both unexercised: nothing has been verified, and
nothing has been found to fail. See the revised Task 7 note in
`docs/plans/2026-10-12-headroom-compression.md` before relying on either.)**

**License.** Apache-2.0, as given in the owner's request — **UNVERIFIED**,
since the actual `LICENSE` file was never read, the same way every other
Headroom-specific fact in this document is. If confirmed, it would be the
same license already carried by Codex CLI, Gemini CLI, Qwen Code and Goose
in the base image (base-image.md §6's table), and this design's
recommendation would be that it needs no licensing review beyond the
ordinary attribution the base image already does for its Apache-2.0
harnesses — but that recommendation is conditional on confirming the license
text matches what was claimed, not settled by this document.

**SECURITY.md's own terms.** This feature would need its own line (or
sub-bullet) in the trust-model section once built, parallel to the existing
gateway bullet: *"With compression on (`agent.compression`), Headroom sees
every model call's full body, including tool outputs, before Anthropic does;
it runs in the same container and user as the agent, and is tested, but not
enforced, to need no outbound network."* It does not change any existing
bullet's truth value — it adds a new one, the same pattern `SECURITY.md`'s
own history shows (the gateway and named-oauth-tokens' per-account job
variants each earned their own bullets when they shipped, rather than
editing an unrelated one).

**How a malicious tool output could attack the compressor.** The agent runs
untrusted repository content constantly — a file's contents, a PR comment, a
log line — any of which could be prompt-injection text aimed at the *model*.
`SECURITY.md` makes the parallel point about the *task* itself, not a tool
output specifically: "Do not launch runs whose task text you copied from an
untrusted issue or comment without reading it: the task is an instruction
the agent follows" (`SECURITY.md:67`; an earlier draft of this paragraph
attributed a "hidden launcher... instructions... the agent follows" phrase
to `SECURITY.md` that does not appear there verbatim or in substance — this
corrects it). A compression layer introduces a **second audience**
for that same untrusted text: Headroom's own summarization logic, which
decides what to keep and what to drop from a tool result before it ever
reaches Claude. Two concrete failure shapes to test for, both currently
**UNVERIFIED against Headroom's actual implementation**:

1. **Injection against the compressor's own decision logic.** If Headroom
   uses heuristics or a local model to decide "this looks safe to drop," a
   crafted tool output (a `.env` file, a comment, a commit message) could be
   built to make the *important* part of a diff or a test failure look like
   boilerplate worth dropping — not to make the model do something, but to
   make Headroom hide the one line a human reviewer, or the senior review
   stage, needed to see. This is a new failure mode for the review loop
   specifically (§3).
2. **Resource exhaustion.** A single tool output engineered to be very large
   or pathological for Headroom's own algorithm (a deeply nested JSON blob,
   a file full of repeated tokens tuned to defeat a dictionary-based
   compressor) could make compression itself slow or memory-hungry enough to
   be a denial-of-service against the run's own timeout, a new angle
   `verify.Run`'s existing bounded, clipped summarization
   (`internal/logtail`, `verify.Record.Summary()`) was never exposed to,
   since those are Fugaro's own code operating on output *Fugaro* already
   trusts the shape of (JUnit XML, a fixed exit code), not arbitrary
   third-party compression over arbitrary repository content.

Neither failure mode has a design-level mitigation here beyond §3's bypass
list (never compress the material these specific loops depend on being
read exactly, regardless of what Headroom's own logic would otherwise do)
and ordinary resource/timeout bounds on the sidecar process itself
(§1.5's supervision, §7's plan).

## 3. Correctness risk of lossy compression in Fugaro's own loops

Compression happens **inside one Claude Code session's own multi-turn
conversation** — the long back-and-forth of Bash, Read, Edit and Grep calls
within a single `implement`, `fix`, `review_first` or `review` stage (each
stage is its own `claude -p` invocation with its own session,
`internal/agent/agent.go`'s `Args`). The risk is not that Fugaro's own
structured records (`runstore.Record`, `verify.Record`, the review's
`VerdictSchema` JSON) get compressed — those never round-trip back into a
prompt uncompressed today, and this design proposes no change there. The
risk is that **older turns within one session** — an earlier `git diff`
call's output, an earlier failing test's full stack trace, an earlier
review finding quoted back during a bounce (`docs/design/generic-tool.md`
§3.6, `bounce: first_line`) — get summarized away before a later turn in the
*same* session needs to quote or act on the exact text.

**Fugaro has no enforcement point for any of this, and that has to be said
plainly before the list below is read as more than a wish list.** In the
chained architecture §1.1 recommends, Claude Code talks to Headroom
directly: Fugaro's own code never sees, builds or touches the request body
Headroom receives (the gateway, downstream of Headroom, "never rewrites a
body, never buffers a stream" either, `internal/gateway/gateway.go`'s
package doc). There is no Fugaro-authored code sitting between a tool result
and the bytes Headroom decides to compress or keep. So **every item below is
a requirement this design places on *Headroom's own configuration surface*,
not something Fugaro implements or can verify by reading its own code** —
whether Headroom exposes any way to pin or exclude specific content from
compression is itself **UNVERIFIED**, and if it does not, this entire
section is a reason to withhold the lift (§4) entirely for any recipe that
has a `review`/`review_first` step — in practice every catalog recipe
(`docs/design/generic-tool.md` §3.5) — since there is no per-stage
exclusion mechanism elsewhere in this design to fall back on; it is not a
problem Fugaro can work around with its own logic downstream.

**What must never be compressed, if Headroom's own configuration can express
it (a go/no-go live check, not a built feature):**

- **The diff under review.** The reviewer's own `git diff origin/<base>...HEAD`
  call (`internal/runner/prompts.go:77-89`, `ReviewPrompt`'s `scope` line) is
  the ground truth a `ship`/`changes` verdict is judged against. A
  compressed diff that drops a line changes what "nothing must change before
  this is merged" actually means, silently.
- **The review rubric and the task/system prompt.** `ReviewPrompt`'s own
  text and `SystemPrompt`'s run contract (`internal/runner/prompts.go`) are
  short, Fugaro-authored instructions, not accumulated tool output — there
  is no token pressure to relieve here, and summarizing an instruction risks
  changing its meaning (dropping "Never create, edit, convert or comment on
  pull requests" because it looked like boilerplate is exactly the
  injection-adjacent risk of §2's point 1, this time from Fugaro's own
  prompt rather than repository content).
- **Finding lists and verdicts.** The structured output a review stage
  returns (`VerdictSchema`, parsed into `runstore.ReviewSummary{Verdict,
  Findings}`, `internal/runner/runner.go:1271-1277`) is the model's own
  *output*, not an input being compressed — but the untrusted gate-failure
  blocks the runner itself feeds back into a later `implement` prompt
  (`GatePrompt`, `internal/runner/prompts.go:94-111`, calling `gateBlock`, a
  separate function at `prompts.go:124-129`, already wrapped as "untrusted
  output of commands.%s") must reach the model intact:
  this is Fugaro's own, already-bounded (`logtail`, 40 lines) summary of a
  check failure, and a second, independent compression pass over text that
  is already a deliberately curated excerpt risks losing the one line that
  told the agent what to fix.
- **`fugaro verify`'s output for the stage that decides readiness.** The
  exact assertion message or stack trace of a failing test is what the
  model reasons from to fix it; a summarized failure risks the agent fixing
  the wrong thing, or fixing a plausible-looking paraphrase of the real
  failure instead of the failure itself.
- **Anything the reviewer must quote verbatim** as a general rule, not an
  enumerable list: a bypass list names categories (diffs, test/verify
  output, review findings, the system/task prompt), not individual byte
  ranges, because the set of "things a human would need to read exactly" is
  exactly the set a compressor should not touch.

**An earlier draft of this section proposed a default ("compress only turns
older than a fixed recent window") that contradicts the bypass list above,
and is withdrawn rather than patched.** A review stage calls `git diff`
**once, early** in its session (`ReviewPrompt`'s scope line is the first
instruction the reviewer acts on); under an "older than N turns" rule, that
diff becomes an "older turn" the moment the reviewer makes a handful of
follow-up tool calls to read the files the diff touches — which is exactly
what a careful review does. A recency-window default would compress away
the diff it was supposed to protect within the same session, not in some
edge case. There is no age-based or count-based default this design can
propose that both relieves token pressure (its entire purpose) and never
ages out the one thing a session read once, early, and must still be able
to quote at the end. The honest position is: **the bypass list is a set of
requirements for the go/no-go live check of §6/§7 to confirm against
Headroom's real configuration surface** (pinning by tool name, by a
preceding command's shape, by recency with an explicit never-expire flag on
specific turns — whatever Headroom actually offers, if anything) — not a
Fugaro-side algorithm, since Fugaro has no hook to run one (above).

**Prompt caching: compression can raise the dollar cost it is meant to
lower.** Claude Code relies on Anthropic's prompt caching — a stable prefix
of the conversation is cached, so a later request that resends the same
prefix pays the much cheaper cache-read price instead of the full input
price (the gateway already parses this distinction from Anthropic's own
usage response — `internal/gateway/sse.go:144-169`'s
`cache_creation_input_tokens`/`cache_read_input_tokens` — and prices it
distinctly: `Rates.Cost(u)`, `internal/pricing/pricing.go:311`, is where the
dollar figure actually comes from; `forward.go:759`'s `o.tokens =
u.Input + u.CacheWrite5m + u.CacheWrite1h + u.CacheRead + u.Output` is a
plain **token** sum kept for a different purpose, not a cost figure — an
earlier draft of this paragraph conflated the two). **If Headroom rewrites
an earlier turn, it changes the prefix** — the exact bytes Anthropic's
cache matching is keyed on — so what would have been a cheap cache-read on
an uncompressed, stable prefix becomes a full cache *write* (dearer than a
plain input token, let alone a cache read) on the now-different, compressed
one, potentially on every request for the rest of the session if the prefix
keeps changing as more turns age into the compressed window. **The net
dollar cost of a compressed session can rise even while its raw token count
falls**, which the vendor's "fewer tokens" framing does not address and
this design cannot resolve without a live measurement (§6) that tracks
dollars (the existing, already-priced total) alongside the cache
read/write **token** totals (a plain sum of fields the gateway's own
per-call accounting already has, `pricing.Usage{CacheRead, CacheWrite5m,
CacheWrite1h, ...}` — no new pricing-split code is needed for a token count;
a true cache-read-vs-write **dollar** split would need one, since
`Rates.Cost` returns one blended total, not a per-category breakdown, and
this design does not propose writing it). This is UNVERIFIED either way —
it depends entirely on how (and how stably) Headroom rewrites the prefix,
which this design has no way to inspect — but it is exactly the kind of
effect a tokens-only metric would miss, which is why §6's headline metric is
dollars, with the cache token totals read alongside it as a diagnostic
(below).

**Observability.** `result.json`'s `StageTiming` today holds only `{Name,
StartedAt, DurationS}` (`internal/runstore/runstore.go:106-110`) — there is
no per-stage **dollar** figure in the record either, a gap
`docs/design/generic-tool.md` §0 item 8 and §1.1 (the 0.8.0 cost preview,
G20) already identify and plan to close by adding `model_usd` and `model` to
a per-stage record (**dollars, not tokens** — G20 does not add a token
field; this design's earlier draft conflated the two). §6's measurement
needs that per-stage dollar figure and is **blocked on G20**, or the coarser
run-level `Cost.ModelUSD` if G20 has not shipped yet. **Per-stage token
counts, separately, already exist inside the runner's own process** —
`internal/gateway/gateway.go:120`'s `StageReport.Tokens` and
`internal/runner/halt.go:111`'s `countTokens` (`r.tokens +=
max(res.StageTokens(), r.gatewayTokens)`) both compute a per-stage token
count today — but **this is not blocked on G20 at all**, and this design
drops the earlier draft's `TokensBefore`/`TokensAfter` proposal rather than
build on a dependency that does not exist: Fugaro's own `StageReport.Tokens`
is the *post*-compression count (since the gateway sits downstream of
Headroom in the chained architecture, §1.1), never a "before" figure, and
whether Headroom itself reports a "before" count through any API Fugaro
could read is **UNVERIFIED and unnamed by any task in §7's plan** — a field
with no confirmed source is worse than no field, so it is dropped rather
than proposed speculatively. The empirical comparison this design actually
relies on is §6's run-level, paired A/B measurement (dollars, with the
cache read/write token totals alongside), not a per-call before/after
figure inside one run.

**How a user disables it.** `agent.compression` is **repository-wide, not
per-workflow** — it is a single field of `Config.Agent`
(`internal/config/config.go:36`, `Agent Agent`, not a
`map[string]Workflow`-keyed value — every workflow of a repository that sets
it shares the same choice, exactly as `agent.auth` and `agent.model` already
do), so "disabling it" means either the repository's `fugaro.yaml` (§4) or a
proposed `fugaro run --no-compression` flag for one-off debugging of a run
that looks wrong, mirroring `--no-budget-check`'s shape
(`internal/cli/run.go:150`) — a client-side override that still lets the
stage run, just without the sidecar in the loop. There is deliberately no
`--compression` flag to force it *on* (§4's scope reasoning: turning it on
must be a reviewed `fugaro.yaml` change, never a launcher's own flag).

## 4. Config surface

**A new key, `agent.compression`, default off (unset/empty).** Proposed
values: `""` (absent, off — the only spelling of "off"; there is no literal
`"off"` string, to keep the closed set as small as the shape check below
needs it) and a named tool, `headroom` — a string rather than a bare
boolean, so a second compressor could be added later without a breaking
change, the same reasoning `agent.auth` already uses for its own closed
enum. No sub-options in v1 (no "aggressiveness" knob): §3 is explicit that
Fugaro has no way to express or enforce a bypass policy of its own, so there
is no per-repository knob this design could safely expose even if it wanted
to — the question is entirely whether Headroom's own configuration can be
set, once, to honor the categories §3 names, not something a repository
tunes.

**The three edits, plus a fourth this design's first draft omitted**
(`docs/design/recipes.md` §2, restated by every design since: "Config is
strict-parsed in three places"), concretely, following the shape
`OAuthToken` just added (`internal/config/config.go:159-168`):

1. **The Go struct**, `internal/config/config.go`'s `Agent`: `Compression
   string \`yaml:"compression,omitempty"\`` next to `Recipe`/`OAuthToken`.
2. **`Validate`**, `internal/config/validate.go`, next to the existing
   `Agent.OAuthToken` shape check (`validate.go:146-151`): checks the
   value's *shape* (`""` or `"headroom"`, a closed set like `agent.auth`'s
   `slices.Contains([]string{"vertex", "api-key", "oauth"}, ...)`,
   `validate.go:121`) **and** — unlike an earlier draft of this design,
   which wrongly said this couldn't live in `Validate` — the `agent.auth`
   refusal (next point explains why `Validate` can see the right value
   here, but not for the budget-gateway question).
3. **`schemas/fugaro.schema.json`**, the matching `enum`/`pattern`, plus a
   **`testdata/config` corpus file** exercising it, which
   `TestFugaroSchemaCorpus` already holds every new key to
   (`docs/plans/2026-10-08-base-image.md:147`).
4. **`internal/config/scope.go`'s `Scopes` table, and `docs/project-layer.md`'s
   printed copy of it** — the fourth edit this key needs that `agent.recipe`
   and `agent.oauth_token` also needed and this design's first draft left
   out. `scope.go:39-41` says plainly: "A key not listed under a block is
   unknown" — `agent.compression` needs its own `ScopeRow` (next to
   `agent.oauth_token`'s at `scope.go:68`) or the project-layer resolver
   treats it as an unknown key regardless of what `Validate` allows.
   `TestScopeTableMatchesDocs` keeps `scope.go` and `docs/project-layer.md`'s
   table equal, so both files move together.

**The `agent.auth` refusal belongs in `Validate` after all — a correction
to a previous round's correction.** A previous draft argued `Validate`
"only sees one file" and so cannot know `agent.auth` when the project layer
sets it. That argument is wrong: `config.Resolve` decodes the **merged**
map (repository file over the project layer's `defaults:`) into one
`Config`, and only then calls `Validate(&c)` on that merged value
(`internal/config/resolve.go:143`, `if ps := Validate(&c); ...`) — `Validate`
is never handed a single layer's raw, unmerged fields; even
`config.Parse(data)` is `Resolve(data, nil)`, which still runs `Validate` on
the (trivially merged, layer-less) result. So by the time `Validate` runs,
`c.Agent.Auth` already reflects whatever the project layer contributed, and
the `agent.compression` + `agent.auth: oauth`/`vertex` refusal can live in
`Validate` directly, exactly where the shape check above already is. No
separate resolved-config check is needed for this part.

**The budget-gateway requirement is a genuinely different question, and
cannot live in `Validate` or anywhere in `internal/config` at all.**
`budget.mode` is not a fact any single `fugaro.yaml` resolution can produce:
the run's *effective* mode is the tightest of three layers — the
installation's own ceiling (the job's environment, `FUGARO_BUDGET_MODE`),
the default branch's `fugaro.yaml`, and the branch's own file — merged by
`(r *run).resolvePolicy` at runner bootstrap, after the checkout is fetched
and the default branch's file is read (`internal/runner/policy.go:58-126`,
`policy.Merge(ceiling, def, FileLayer(cfg))`; `SECURITY.md:34` is the
installation-ceiling half of this). `config.Resolve` (and therefore
`Validate`) never sees the ceiling or the default-branch file — it resolves
only the project layer and the ref's own `fugaro.yaml`. **A check written
against `cfg.Budget.Mode` (what the repository's own file says, or doesn't)
would be wrong in exactly the common case this feature targets**: an owner
who turns the gateway on installation-wide via the ceiling, for a repository
whose own `fugaro.yaml` sets no `budget:` block at all (`cfg.Budget == nil`,
the normal, recommended shape per `docs/design/m9-budget-and-dashboard.md`).
Refusing compression there because the repository's own file "doesn't say
budget is on" would refuse the single most common, correctly-configured
case. **The authoritative check has to be `r.gatewayOn()` at runner
bootstrap, after `resolvePolicy` has run** (`internal/runner/gateway.go:46-48`):
that is the one place the real, merged answer exists. A CLI-side check
(`fugaro run`/`fugaro validate`, before the ceiling and default-branch file
are ever read) can only be **advisory** — a heads-up when the repository's
own file plainly sets `agent.auth` to something other than `api-key`, never
a confident "the gateway will be on," since the CLI cannot know the ceiling.
§7's plan moves this check into the runner's bootstrap, not `config`.

**v1 refuses `vertex` (in `Validate`, with `oauth`) and requires the gateway
(a runner-bootstrap check, above).** This is narrower than an earlier draft
of this design, which only refused `oauth`, and the plan's earlier "Task 1b"
resolved-config check for the gateway question is withdrawn in favor of the
runner-bootstrap check described above, which is the only place the right
answer exists.

**Strict-parser consequences for older binaries.** A pre-feature runner's
`config.Parse` (`dec.KnownFields(true)`, `config.go:489`) refuses an unknown
`compression` key outright — the same version-skew shape as
`agent.recipe` (0.5.0), `gcp_project` (0.4.0) and `agent.oauth_token`
(0.6.5). A `compressionSince` constant and an image-build-record check
(mirroring `namedTokensSince`/`checkImageSince`,
`docs/plans/2026-10-09-named-oauth-tokens.md` Task 6) refuses *launching* a
run that would set it against an image built before the feature shipped,
naming `fugaro image refresh` as the fix. **The same check covers the task's
own half of the feature, and `--no-compression` is no way past it:** the flag
turns the sidecar off, not the `fugaro.yaml` key the run still reads, so a
launch whose configuration turns compression on is refused with or without it
— and a task carrying `compression_off` (a `--pr` follow-up or a `--retry`
inherits it from the run it continues) is refused too, since `task.Parse`
decodes with `DisallowUnknownFields`. A launch that neither carries that field
nor reads such a key is never gated. (Amended 2026-10-27, as the plan's Task 6
was implemented; the plan's Task 6 carries the same note.)

**Shipping the key before the sidecar is implemented, without a silent
no-op.** This design's first draft had the config PR (Task 1-2) ship a key
`Validate` already *accepts* (`"headroom"`) before any code reads it or
starts a sidecar — a `fugaro.yaml` with `agent.compression: headroom` would
parse, validate and run, doing nothing, which is exactly the silent-failure
shape §1.5 argues against for the sidecar itself and should not be true of
the key either. **The fix: `agent.compression` is reserved and refused**
(the same pattern `docs/design/recipes.md` §4 already uses for `checks`,
`goto`, `on_reject`, `extends` and `docs/design/layered-config.md` §6 uses
for a profile's `extends` — a known key that parses, and is refused with a
message saying so, rather than an unknown key a strict decoder rejects
outright) **until a dedicated, final lift change merges, after everything
else is built — not inside the same PR that wires the sidecar up.** An
earlier draft of this design had the implementing PR both build the feature
and lift the refusal together, which reintroduces the exact problem it was
meant to solve: that PR would make `agent.compression: headroom` immediately
usable by any real run the moment it merges, before the live checks of §6
have ever confirmed Headroom needs no network or can honor the bypass list
(§3). The schema has to move in lockstep with the refusal, not ahead of it:
**while reserved, the schema's own enum for `agent.compression` admits only
`""`** (not `"headroom"`), so `schemas/fugaro.schema.json` and
`config.Validate` refuse the real value for the same reason, and the
corpus fixture exercising `agent.compression: headroom` lives in
`testdata/config/invalid/` the whole time the feature is being built. The
**lift** is its own small, final change: it edits the schema's enum to admit
`"headroom"`, edits `Validate`'s message away from "not yet implemented,"
and moves that one fixture from `invalid/` to `valid/` — three files, one
PR, gated on §6's checks having been run. This also replaces §5's "what
gates it" question below: the live checks gate this one, final, small PR,
not anything inside the PR that builds the feature.

**Scope (the project-layer table, `docs/design/layered-config.md` §5):
Project — no; Profile — no; Repo — yes; Override — `--no-compression` only
(never a force-on flag).** The reasoning for Project/Profile/Repo is the one
`agent.oauth_token` already established for the identical shape of question
(T2, `docs/design/named-oauth-tokens.md` §3): the project layer and a
profile are both keys of the same
`fugaro/project-layer.yaml` bucket object, writable with no PR review
(`layered-config.md` §11's threat model: operators can write it, and,
before the 0.7.0 bucket hardening, any launcher too —
[bucket-iam.md](bucket-iam.md)). Unlike `agent.oauth_token`, turning compression on
does not select among owner-defined credential identities — but it **does**
change what the model sees on every call, silently, for every repository a
profile templates, with no PR review. That is the "classic failure mode"
`layered-config.md` §1 names directly, and it is strictly worse as a
profile-wide default than as one repository's own, reviewed choice.
Repo-only, reviewed in a pull request, is the safer default. **The override
is deliberately one-directional**: `--no-compression` lets a launcher turn a
repository's already-reviewed choice *off* for one suspicious run, but there
is no `--compression` flag to turn it *on* — the whole point of making this
Repo-scoped is that turning it on is a PR-reviewed decision, and a flag that
could force it on from the command line would undo that for every
`api-key`/gateway-on workflow a launcher can already run.

**Operator-only vs. shared.** Unlike `claude_accounts` (named-oauth-tokens'
T1), `agent.compression` does not mint a new credential identity or a new
Cloud Run job variant — it is a behavior flag on an existing workflow's
existing job, so it does **not** need the local-config-only treatment
reserved for things that fork trust boundaries. It belongs in the
repository's own `fugaro.yaml`, PR-reviewed, the same repo-only,
override-able shape as `agent.oauth_token` (`scope.go:68`) — **not**
`agent.recipe`, which is `InProject | InRepo | InOverride`
(`scope.go:67`) and therefore a weaker analogy than an earlier draft of this
paragraph used: `agent.recipe` *can* be set by the project layer,
`agent.compression` deliberately cannot be (above). A **policy-ceiling-shaped
kill switch is still worth considering** — an owner who wants to hold the
line at "off everywhere until I say otherwise," the same shape `budget.*`'s
"the ceiling is the owner's... a repository only tightens" already uses
(`layered-config.md` §5's Why column for `agent.max_run_tokens`/`budget.*`)
— proposed as a future addition if the owner wants to force it off
installation-wide during the opt-in period of §6, not built in v1.

**Interaction with recipes.** `docs/design/generic-tool.md` §3.3 already
rules that loop knobs (`agent.first_line_rounds`, `agent.review_rounds`)
"stay outside the recipe" — a recipe is a topology, not a place for
`agent.*` tuning. `agent.compression` follows the identical rule: it is a
plain `agent.*` key, never a recipe field, and no catalog recipe (§3.5
there) gains a compression setting of its own in v1.

**Interaction with named Claude accounts.** Moot in v1, not merely
orthogonal: `agent.compression` is refused outright whenever `agent.auth`
is `oauth` (above), and named Claude accounts (named-oauth-tokens.md) only
ever apply to `oauth` workflows — so there is no repository configuration in
v1 where both features are simultaneously active. If the oauth/proxy
question is ever answered and the refusal lifts, the two remain orthogonal
by construction: `agent.oauth_token` picks which secret a workflow's job
mounts, `agent.compression` decides whether a sidecar sits in front of the
resulting calls, and nothing about the account named changes which
compression setting applies.

## 5. Image and release placement

**Layering (`docs/design/base-image.md` §3's table).** Headroom is trusted
infrastructure every repository's run may use, not a project-chosen
runtime — it belongs in **layer 3, Harnesses**, next to Claude Code and the
other agent CLIs, pinned the same way (§2), rather than in layer 1's kit
(a general-purpose tool) or installed via `mise` (a project's own choice,
§2). Its binary should sit in the same root-owned, read-only location the
Node-based harnesses already use (`/opt/fugaro/harnesses` or a sibling
directory) so "the agent can't replace them" (`base-image.md` §9) applies to
it too — the agent must not be able to swap in its own compressor.

**Size cost.** UNVERIFIED until a real artifact is pinned (§2's open
question about a native binary vs. a Python runtime). If a standalone
Rust/native binary exists, this is comparable to the smallest existing
harness entries (Crush, about 0.07 GB, `base-image.md` §13's table); if it
needs a private Python runtime, it is a new, unbudgeted category the current
size table (`base-image.md` §13, "about 3.1 GB... the plan's first Docker
check replaces [the estimates] with measurements") does not account for, and
the existing CI size budget (`images/size-budget.sh`, 4.0 GB hard limit,
3.5 GB warning) would need to absorb it.

**arm64 support, one recommendation stated once.** The base image already
builds and publishes both architectures (`base-image.md` §12). **If and
only if** Task 7's own artifact-resolution step (§7 — reading Headroom's
real release page, not one of Task 11's owner-run live checks) finds no
arm64 Headroom artifact, the
recommendation is to refuse `agent.compression: headroom` on an arm64 job
(a launch-time check) rather than apply base-image.md's ordinary fallback
for an optional convenience harness (an `amd64`-only row that silently skips
arm64) — this *is* a security- and correctness-relevant component when
enabled, so a job that can't actually run it should refuse, not silently
run uncompressed. If an arm64 artifact exists, none of this applies and the
row is `all` like every other harness. Today's installations are amd64-only
regardless (`base-image.md` §12, "the installation's copy stays amd64-only,
and that is correct"), so whichever way Task 7 finds it, this is a
forward-looking guard, not a current blocker.

**Presence verification.** A new row in `images/base/tools.tsv`
(`images.BaseTools`), tier `harness` (not `critical` — a run with
compression off needs nothing from it, unlike `claude` or `git`; not `kit`
either — `kit` is layer 1's general-purpose tools, and this design places
Headroom in layer 3 with the other harnesses, above, so its tier should
match). This only means its presence row runs in the **base image's own CI
smoke**, not a derived image's selftest — `base-image.md` §6: "The base's
CI smoke runs every row; a derived image's selftest runs the `critical`
rows" — checked with `--network none`
(`base-image.md` §6, "Presence checks"). **This proves only that the binary
doesn't need the network to answer `--version` or a health probe**, not that
an actual compression pass needs none — §2 already says this is a
`--version`-shaped check, not the real no-egress test this design requires
before shipping; that test is a separate, new CI task (§7's plan), not a
byproduct of this row.

**Release: 0.8.0, not inside the 0.7.0 freeze.** Reasoning, by direct
analogy with two decisions this repository has already made about
similarly-shaped work:

- `docs/design/generic-tool.md` §1 (the Docker-capable backend) and §1.1
  (the cost preview) were both moved to 0.8.0 specifically because they are
  **measure-first**: a live check settles a threshold before the feature is
  built at all (§1 there: "BLOCKED until Check 31's FACT lines are
  recorded"). This design is in the same shape — §6's A/B protocol has not
  run, and §1.1's ANTHROPIC_BASE_URL/oauth question has not been checked
  either.
- 0.7.0 is already "a hard cut of every base image, the config and the
  check job" (`generic-tool.md` §1, "Why not 0.7.0"); a second new
  trust-boundary-adjacent component (a sidecar with visibility into every
  model call) in the same release "doubles what a failed upgrade can break"
  — the identical reasoning the owner already used to defer the Docker
  backend.
- Named OAuth tokens was originally sequenced the same way — "to avoid
  `internal/infra` merge churn with the base-image consolidation"
  (`named-oauth-tokens.md` §8) — but this is a weaker precedent than it
  looks: the owner overrode that reasoning and shipped it at 0.6.5, ahead of
  0.7.0, and `named-oauth-tokens.md` §8 says plainly "this reasoning was not
  revisited." So this bullet is cited for the *shape* of the scheduling
  argument, not as a settled precedent the owner has stuck to — the stronger
  case for waiting is the two bullets above (measure-first, and not
  overloading the 0.7.0 freeze), which the owner has not overridden for any
  feature to date. If the owner wants this sooner, the named-oauth-tokens
  precedent is itself evidence that "after 0.7.0" is a default this design
  recommends, not a hard constraint — this design touches
  `internal/agent/env.go` and the base image's harness layer, both squarely
  inside 0.7.0's own change surface, which is the scheduling reason either
  way.

**What gates merging vs. what gates the final lift — and why the lift has to
be its own, separate, last change.** §4's "reserved and refused" resolution
means every PR that builds the feature (config, the sidecar package, the
env/managed-settings wiring, the runner's bootstrap, the CLI override, the
base image pin) can merge as soon as it is reviewed, since none of them, on
their own, makes `agent.compression: headroom` accepted by anything — the
key stays refused the whole time. **A previous draft of this section
described a gate that was circular**: it said the live checks gate "lifting
the refusal," while the plan's own task structure lifted the refusal inside
the same PR that built the runner wiring — before any live check could have
run, since several of those checks (§6's measurement in particular) need a
working, mergeable implementation to run *against*. The fix is sequencing,
not a different gate: the lift is its own, final, small change (§4 above),
and the live checks that must pass **before that one final change merges**
are, concretely: Task 7's no-outbound-network CI test (a repeatable build-time
check, no live cloud needed) and a new check confirming whether Headroom's
own configuration surface can honor the bypass list of §3 — pin the diff
under review, verify output and the system/task prompt, or else the lift is
withheld entirely for any recipe with a `review`/`review_first` step (in
practice every catalog recipe, since this design builds no per-stage
exclusion mechanism to fall back on instead — the plan's Check L2 names
this explicitly, and it gates the lift, not merely informs it, closing the
contradiction an earlier draft of §3 and §8 left open: those sections said
the bypass list was "Fugaro's own" and "a single, Fugaro-chosen... policy,"
which cannot both be true alongside §3's own admission that Fugaro has no
enforcement point for it). **What §6's
full measurement gates is different again**: not the lift, but whether this
feature is ever proposed as a *default* (§6's own closing paragraph) — and
it necessarily runs *after* the lift has merged, since it needs a real run
to actually exercise compression, the ordering an earlier draft of this
document had backwards ("before any code merges"). None of these gates is
this design's to satisfy — they are live, owner-run checks (§6, §7).
**(2026-10-27: the first of the two named gates — Task 7's
no-outbound-network CI test — does not exist, because Task 7's revised run
pinned no artifact to test. The lift is therefore gated on one real check and
one that is absent, not on two that have run; see the revised Task 7 note in
`docs/plans/2026-10-12-headroom-compression.md`.)**

## 6. Measurement plan

**This is the gate for ever defaulting `agent.compression` on — nothing
more, and an earlier draft of this paragraph overstated it.** Whether
Headroom is worth shipping *at all*, even as opt-in, is decided earlier and
separately: by Check L2 (§3/§5/§7 — can Headroom's configuration honor the
bypass list) and Task 7's no-outbound-network result, both of which gate
the lift (§4/§5) and both of which this measurement runs *after*, against
an already-lifted, already-opt-in feature (§5's "what gates merging vs. the
lift"). **(2026-10-27: the second of those two — Task 7's
no-outbound-network result — does not exist; Task 7's revised run pinned no
artifact, so no such result was ever produced. See the revised Task 7 note in
`docs/plans/2026-10-12-headroom-compression.md`.)** Even a clean pass here
keeps it opt-in (§4); this plan exists only
to decide whether Headroom's own, already-shipped compression is ever worth
proposing as a *default*, set against the vendor's own claims.

**Protocol: a sandbox-only A/B over identical tasks, on `api-key` with the
gateway on.** Sandbox installation only (the global rule: never EdgeWeb,
never EdgeServer), on a repository configured `agent.auth: api-key` with
`budget.mode: observe` (so every call is priced without risking a real
refusal mid-measurement) — the only auth mode v1 ever runs compression
under (§1.1, §4), named explicitly here so a future reader doesn't have to
infer it. **Pin and record the exact Headroom version under test** (the
`ARG HEADROOM_VERSION` §5 pins), since a later bump invalidates a prior
measurement's numbers. A fixed task corpus — the owner's own choice of 15-20
representative real or realistic tasks, spanning at least one small bug fix,
one feature addition touching several files, and one task expected to need
several review rounds (to exercise the bounce/fix loop, where intra-session
compression matters most, §3) — each run **twice**, paired, same model
pins, same recipe, same repository state: once with `agent.compression`
unset (control — there is no literal `"off"` value, §4) and once with
`agent.compression: headroom` (treatment, after the lift of §4/§5 has
happened; a pre-lift control-only run proves nothing about the treatment
arm). Pairing (not two independent random samples) controls for task-to-task
variance, which otherwise swamps a small sample.

**A pairing hazard this design missed until now: prompt caching favors
whichever arm runs second.** Anthropic's prompt cache keys on the exact,
stable prefix (§3) — if a pair's two runs share an identical system prompt
and early task text and run close together, the *second* run of the pair
(whichever arm it is) can hit a cache Anthropic's servers still hold from
the first, understating its cost relative to a cold start, independent of
whether compression did anything. Two mitigations, either is enough:
**randomize which arm of each pair runs first** (so the bias falls evenly
across both arms over the whole batch, rather than systematically favoring
one), or **space a pair's two runs apart by more than the cache's own TTL**
(`CacheWrite5m`/`CacheWrite1h` in the gateway's own accounting name the two
TTLs this codebase already prices, so this is a real, bounded wait, not a
guess). The owner picks one before running the batch; this plan does not
itself prescribe which.

**Metrics — dollars, never tokens, for the headline comparison.** §3's
prompt-caching risk is exactly why: a tokens-only reading could show fewer
tokens and more dollars at once, and a metric that can't see that is worse
than no metric. All read from existing `result.json` fields, plus the one
proposed addition this plan is actually blocked on:

- **Cost** — `Cost.ModelUSD`/`Cost.TotalUSD` (`internal/runstore/cost.go`),
  already in `result.json` today at the run level; **per-stage** needs G20
  (`docs/design/generic-tool.md` §1.1, `model_usd`/`model` on `StageTiming`)
  — if G20 has not shipped when this measurement runs, fall back to the
  run-level figure and say so in the results, rather than blocking the
  whole measurement on it.
- **The cache read/write token totals, per arm** (§3's prompt-caching risk,
  directly — token totals, not a dollar split: `Rates.Cost` returns one
  blended total, so a true per-category dollar figure needs new pricing
  code this design doesn't propose writing). The gateway already computes
  the token counts per call (`internal/gateway/sse.go:144-169`'s
  `cache_creation_input_tokens`/`cache_read_input_tokens` parsing feeds
  `pricing.Usage{CacheRead, CacheWrite5m, CacheWrite1h, ...}`) but does not
  currently surface them in `result.json` at any granularity — this
  measurement needs them summed per run (control vs. treatment), a small,
  additive instrumentation task (§7's plan) independent of G20: without it,
  a dollar increase in the treatment arm cannot be distinguished from "the
  task was just more expensive" versus "compression broke the cache" (a
  jump in cache *writes* relative to control, at roughly the same total
  cache activity, is the signature to look for), which is the one failure
  mode this whole design most needs to catch.
- **Review rounds** — `len(Record.Reviews)` and each entry's `Bounce`
  (`internal/runstore/runstore.go`'s `ReviewSummary`).
- **Pass rate** — `Record.Outcome == "ready"` (the readiness rule: a
  verified passing test on the final commit and a senior `ship`,
  `docs/design/recipes.md` §4) and each `verify.Record.Passed`.
- **Wall time** — `Record.StartedAt`/`FinishedAt`, and per-stage from
  `Stages []StageTiming`.
- **Failure modes** — `Record.Status`/`Reason`, read together: a sidecar
  crash or hang *during a stage* goes through the same generic
  stage-failure path any other stage error does, and ends
  `StatusFailed`+`OutcomeDraft` every time (finalize always produces a PR,
  committing an empty commit if the agent made none) — never `StatusHalted`,
  which `runstore.go:34` reserves for "a budget or token limit stopped the
  run," a different thing. The separate, bootstrap-only `StatusInfraError`+
  `OutcomeNone` applies only when starting the sidecar itself fails, before
  any stage runs (the gateway-off refusal, or the sidecar process failing
  to launch) — a death *after* that point, however early in the first
  stage, is a stage failure, not a bootstrap one. Read `Reason` for a
  sidecar-shaped message in either status rather than assuming one
  predictable value.
- **A class automatic metrics cannot catch:** whether a `ship` verdict or a
  passing-looking fix is *wrong* because the reviewer or the coder acted on
  a compressed summary that silently dropped something load-bearing (§3's
  core risk). This needs a **human (or a separate, uncompressed LLM-judge)
  read of a sample of the treatment group's diffs and review transcripts**,
  not just the automatic pass/fail the gateway and verify records give —
  recommend the owner (or a reviewer they assign) reads at least a third of
  the treatment runs' diffs and review verdicts by hand, specifically
  looking for "this verdict doesn't match what the diff actually says."

**Sample size — a judgment call, not a statistical test, and this document
should not have implied otherwise.** A single replicate per task per arm
(20 paired runs) cannot resolve a claim like "at most one extra failure per
20" as a statistical result — with one observation per cell, any single
failure *is* "one in 20," and the test has no power to tell a real effect
from noise at that scale. This design does not propose running enough
replicates to make the thresholds below a real statistical test (that would
need several replicates per task per arm, multiplying the sandbox cost well
beyond what a design-stage plan should commit the owner to); instead, it
states plainly that **the thresholds below are a judgment call the owner
makes by reading the paired results, not a pass/fail a script can compute
with statistical confidence.** 20 paired runs (40 total launches) as a
starting batch gives the owner enough examples to read, not a sample size
with resolving power. This repository's own best-of-N machinery is
**designed, not built** (`fugaro run --attempts N`,
`docs/design/generic-tool.md` §3.6, G13 — it does not exist in
`internal/cli` today) and, even once built, is a close but not exact fit
(parallel attempts of one task choosing the best, not paired control/
treatment of the same task), so the owner runs each pair as two ordinary,
separately-launched runs regardless of whether `--attempts` ever ships.

**Who runs it.** The owner, in the sandbox installation only, following this
repository's existing live-check discipline (`docs/gcp-live-checklist.md`'s
own rules: cleanup registered before any side effect, `FACT:` lines pasted
back into the PR and the checklist, ADC only, ambient credentials only
where the checklist already requires them). This plan's §7 proposes it as
new, numbered checks in that file, continuing the existing sequence, rather
than a separate document — the pattern every other "the owner must run this
in the sandbox" item in this repository already uses.

**Pass/fail thresholds — judgment-call targets, not a statistical test
(recommendation, veto-able — the owner rules).** The 20% figure below is
anchored on the vendor's own, **UNVERIFIED** claim of "about 20% [fewer
tokens] on coding agents" (the conservative end of its range, and the one
closest to Fugaro's actual workload) — using the vendor's own number as the
bar to clear means this measurement cannot by itself raise confidence above
what that unverified claim already asserts; it can only show the claim
failed to hold even on its own terms, which is still useful, but is a
weaker result than an independently-derived threshold would be:

- **Cost reduction (dollars, not tokens — above):** treatment's mean
  `Cost.ModelUSD` at least 20% below control's, across the paired sample,
  **with the cache read/write split checked for each pair that misses this
  bar** — a miss caused by cache-write costs (§3) is a different finding
  from a miss caused by compression simply not shrinking much.
- **Pass rate:** at most one additional failed/infra_error run in the
  treatment group across the 20 paired runs, relative to control — read as
  "did anything look different," not as a statistically significant rate,
  given the sample-size caveat above.
- **Review rounds:** mean review rounds in the treatment group increases by
  no more than 0.5 over control (a proxy for "compression made the reviewer
  need more passes to reach `ship`," itself a proxy for lost context during
  review).
- **The manual-read class:** zero instances, in the sampled third, of a
  verdict or a fix the reader judges to be wrong *because* of a compressed
  detail — this one is a hard zero, not a tolerance, since it is exactly the
  failure mode §3 exists to prevent.

**What keeps it opt-in vs. ever proposes a default.** Clearing every
threshold above is necessary but not sufficient for a default-on proposal:
consistent with how `agent.recipe` and `agent.oauth_token` both shipped
opt-in and earned no automatic path to becoming a default, this design
recommends a **second gate** — several weeks of real, voluntary opt-in usage
across more than one repository, with no owner-reported regression — before
a *separate*, future design even proposes flipping the default. Nothing in
this document proposes a default-on timeline; that decision, if it is ever
made, deserves its own review, the same way layered-config.md's option A
(executable keys in profiles) named its own hardening as "the next security
task" rather than bundling it into the design that first shipped the
feature.

## 7. Implementation plan

A task-by-task, TDD plan lives at
[docs/plans/2026-10-12-headroom-compression.md](../plans/2026-10-12-headroom-compression.md),
in this repository's existing plan format (see
`docs/plans/2026-10-09-named-oauth-tokens.md` for the precedent this one
follows). Nothing in that plan has been written or run; like the named
OAuth tokens plan before its Task 1, this is a design-to-code plan, gated on
the owner approving this design and, separately, on the live checks named
in §1.1 (the oauth/`ANTHROPIC_BASE_URL` question), §3 (the bypass-list
capability reading), §5 (the no-outbound-network CI result) and §6 (the
measurement) before the feature's refusal is ever lifted or the feature is
proposed as a default. **(2026-10-27: §5's no-outbound-network CI result does
not exist — Task 7's revised run pinned no artifact and wrote no test, so this
list of gates is one short of what it claims; see the revised Task 7 note in
the plan.)**

## 8. Non-goals

- A per-repository or per-recipe compression *strategy* (aggressiveness
  knobs, custom bypass lists) — v1 is a single on/off switch; the bypass
  categories of §3 are a requirement on Headroom's own configuration,
  confirmed once by Check L2 (§6/§7), never a per-repository choice.
- `agent.auth: vertex` and `agent.auth: oauth` under compression (§1.1,
  §4) — v1 is `api-key` with the gateway on, only; extending to either is a
  separate, future question, each needing its own live check.
- Relieving usage-limit stalls (§1.1's own summary at the top of this
  document) — those are an `oauth`-only problem, and v1 refuses `oauth`.
- MCP-server-mediated tool execution (§1.3) — a separate, much larger
  redesign of how Claude Code's tools are exposed, not proposed here.
- A `--compression` flag to force the feature on from the command line (§4)
  — only `--no-compression` exists; turning it on is always a reviewed
  `fugaro.yaml` change.
- A statistically powered measurement (§6) — the sandbox protocol here is a
  judgment-call tool for the owner, not a test with resolving power at its
  proposed sample size.
- A default-on proposal for `agent.compression` — a future design, gated on
  real opt-in usage (§6).
- Compressing Fugaro's own structured records (`result.json`, `verify.Record`,
  the review's JSON-schema output) — these are not in the path this design
  touches at all (§3).
- Supporting any agent harness other than Claude Code — Fugaro drives only
  Claude Code today (`base-image.md` §6), and this design changes nothing
  about that.
- Fetching or taking on faith anything from Headroom's own marketing beyond
  what this design has explicitly marked UNVERIFIED.
