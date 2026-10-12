# Headroom: context compression in the agent loop

Status: design for review (2026-10-12), docs only — nothing in this design has
been built. Source: the owner's request to evaluate integrating Headroom
(Headroom Labs, Apache-2.0,
[github.com/headroomlabs-ai](https://github.com/headroomlabs-ai)), a
third-party, open-source context-compression layer, into the agent loop to
cut token cost and usage-limit stalls. Target release: **0.8.0** (§5), opt-in
and gated behind the measurement plan of §6, which has not run.

**What is verified and what is not.** Everything about *Fugaro* below is read
from the code at this branch's base and cited by file. Everything about
*Headroom itself* beyond the one-paragraph description in the owner's request
(Apache-2.0; a proxy, CLI wrapper, MCP server or library; Python and Rust
with a TypeScript SDK; vendor claims of 60-95% fewer tokens on structured
data, about 20% on coding agents, no quality loss on its own benchmarks) is
**UNVERIFIED**: this design was written without fetching Headroom's own docs,
README or source (the task's own instruction), so every claim attributed to
the vendor is marked so, not asserted as fact, and nothing here should be
read as confirming Headroom does what it says.

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
[--model] [--max-budget-usd] [--json-schema]` as a child process via
`procgroup.Run`, with the prompt on stdin and a **scrubbed environment**
(`agent.BuildEnv`, `internal/agent/env.go:51-153`, an allowlist — `baseEnv`
plus auth variables plus declared workflow secrets; `req.Env` must never be
nil, "which would inherit the runner's full environment"). The runner reads
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
`internal/gateway/gateway.go:192-225`: `net.ListenConfig.Listen(ctx, "tcp",
"127.0.0.1:0")`, then `go func() { s.srv.Serve(ln) }()`), torn down with the
run's context. `internal/runner/gateway.go:105-120` points Claude Code at it
by setting `agent.Gateway{BaseURL, Token}`, which `GatewayVars`
(`internal/agent/env.go:169-201`) turns into `ANTHROPIC_BASE_URL` and
`ANTHROPIC_API_KEY` (the gateway's per-run token, never the real key) for
`auth: api-key`, or the Vertex equivalents for `auth: vertex`.

**`auth: oauth` never goes through it, by explicit code, twice:**
`BuildEnv`'s oauth case (`env.go:106-109`: `if spec.Gateway != nil { return
..., fmt.Errorf("auth: oauth never goes through the gateway") }`) and
`GatewayVars`'s own oauth case (`env.go:195-196`, the identical error). This
is a **Fugaro design choice** (oauth has no dollar cap to enforce, so there
is nothing for the gateway to meter — `SECURITY.md`, "oauth has no gateway"),
not a technical limit of Claude Code or Anthropic's API. Whether Claude
Code's CLI still honors `ANTHROPIC_BASE_URL` when `CLAUDE_CODE_OAUTH_TOKEN`
is set — i.e., whether a proxy could sit in front of an oauth-authenticated
session at all — is **UNVERIFIED**: nothing in this codebase exercises that
combination, and `docs/design/base-image.md` §6's env-variable table lists
`ANTHROPIC_BASE_URL` for Claude Code generally, without saying whether it
composes with the OAuth token. This is Headroom integration's first open
question, and it has a sharp security edge: Anthropic's OAuth bearer token
is the subscription account's own live credential (`SECURITY.md`, "f.
Expired or revoked token" treats it as "Anthropic's own credential"). A
proxy terminating in front of it would see that bearer token on every
request header — a new kind of exposure that does not exist today, since
oauth traffic currently has **no intermediary at all** (§2 goes into this).

**With `auth: api-key` or `auth: vertex`** (already gatewayed), a Headroom
proxy has to be placed relative to Fugaro's own gateway, and the placement
changes what breaks:

- **Chained in front of the gateway** (`ANTHROPIC_BASE_URL` → Headroom →
  Headroom's own configured upstream → the gateway's loopback URL). This
  keeps the gateway as the last hop before the real key (today's invariant:
  `internal/gateway/gateway.go`'s package doc, "holds the real credential"),
  but Headroom now sits between Claude Code and the gateway's own request
  parser. The gateway's worst-case reservation and settlement
  (`internal/gateway/route.go`, `forward.go`) are built to understand *the
  actual Anthropic request/response shape* ("the request shapes the price
  table can bound", `gateway.go`'s package doc) — a body Headroom has
  rewritten (dropped or summarized tool-result blocks) changes the token
  counts the gateway reserves against and reconciles from reported usage.
  This is a **correctness risk for the budget system itself**, not only for
  the model's output: if compression changes what the gateway sees enough to
  misprice a call, `StageReport.Unreconciled`/`Overrun`
  (`internal/gateway/gateway.go:112-119`) could drift in ways the existing
  tests (built against uncompressed bodies) never exercise. §3 covers this
  further.
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
  **no Go SDK** (per the owner's description: Python and Rust with a
  TypeScript SDK only) — this codebase cannot embed it as a library (§1.4),
  so "inside the forward path" still means an out-of-process call (HTTP or a
  Unix socket) to a Headroom process, not a Go function call. Mechanically
  it is a sidecar either way; the only question this placement changes is
  which process initiates the hop.

**Recommendation (H1):** a standalone Headroom process (its own OS process,
an HTTP server on loopback — see §1.5 for why this cannot be in-process like
Fugaro's own gateway), chained **in front of** the gateway when the gateway
is on (api-key, vertex), never replacing it; **refused outright with `auth:
oauth`** until the live check of §1.1 (ANTHROPIC_BASE_URL plus
`CLAUDE_CODE_OAUTH_TOKEN`) and the token-exposure question above are both
answered. *Veto alternative:* refuse `api-key` too, and only ever reach
Headroom from Go code inside `forward.go` via an explicit round trip Fugaro
controls end to end (more control over what crosses the boundary, a larger
and slower implementation, and no help at all for `oauth` workflows, which
today are the ones most exposed to usage-limit stalls — see
`plugin/skills/working/reference/diagnose.md`'s existing "usage limit"
handling, the problem this feature is partly meant to help with).

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
to it. Fugaro's workflows do not route tool use through MCP: Bash, Read,
Edit, Write, Grep and Glob are Claude Code's native built-in tools, and
nothing in the base image or any catalog recipe front-ends them with an MCP
bridge (`docs/design/base-image.md` §6's harness table lists no MCP layer;
`docs/design/recipes.md` §7 keeps "a recipe names no command" as a safety
rule — introducing a tool-call intermediary is a materially bigger and
different project, closer to generic-tool.md's deferred "pluggable control
plane", §11 there). **Recommendation: out of scope for v1.** It would need
Fugaro to restructure how Claude Code's tools are exposed, which this design
does not propose and the owner did not ask for.

### 1.4 Library

No Go SDK exists (Python, Rust, a TypeScript SDK — the owner's own
description). The runner is Go; embedding is not available to it. A
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
shape but launching a real subprocess: start it with its own context before
`startGateway` (or instead of needing the gateway's own BaseURL mechanics
for oauth, which refuses it — §1.1), wait on a readiness probe (a local
health endpoint, since nothing here should depend on network access — see
§2), and kill its process group on run end exactly as `procgroup` does for
`claude` (SIGTERM, then SIGKILL after a grace period), so a leaked
descendant cannot survive the container. This is new code, not a reuse of
an existing primitive, and it sits next to the code SECURITY.md already
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
   compression in mind.
2. **A mid-stream crash is worse than a cost regression — it is a
   correctness one.** If Headroom dies after summarizing some of a long
   session's earlier turns, falling back to "send everything raw from now
   on" leaves the model holding a conversation where some of its own
   history is a summary it was told to trust and the rest is suddenly raw —
   an inconsistent context the model has no way to reconcile. Failing the
   stage is the same posture the runner already takes for a gateway halt
   (`internal/runner/gateway.go:215-248`, `gatewayHalt`): stop the run
   cleanly rather than continue on an environment the runner can no longer
   vouch for.
3. The feature is opt-in and default off (§4), so failing closed never
   affects a repository that did not choose it — unlike, say, the gateway
   itself, which is load-bearing for every budgeted run.

*Veto alternative:* fall back to direct (no compression, gateway or real API
reached as today) on sidecar death, logged loudly. This trades a run outage
for a silent cost/behavior change; rejected for v1 given point 1 above, but
it is a one-line code change if the owner prefers availability over the
stricter guarantee, and could reasonably be revisited once real usage data
exists (§6).

## 2. Security and trust

**The starting posture, from `SECURITY.md`.** The agent already runs with
every permission prompt bypassed (`--dangerously-skip-permissions`), shares
the container and its user with the runner, and can already reach "the real
model and provider keys" and "the job's service account, through the
metadata server" via `/proc` (`SECURITY.md`, "What one run's agent can
reach"). Headroom running in the same container, as the same unprivileged
`fugaro` user, adds **no new privilege boundary to cross** — it is already
inside the blast radius SECURITY.md describes as the trust boundary ("the
agent doing harmful things inside its own container... is out of scope" —
`SECURITY.md`, Scope). What it *does* add is new: a second network-facing
process that sees every byte of model traffic, including the live OAuth
bearer token if §1.1's open question resolves in favor of routing oauth
through it, and a new place a credential could leave the container if
Headroom's own compression logic calls out anywhere.

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
SECURITY.md rather than hiding inside "egress is open." **This design's
hard requirement: Headroom runs with outbound network access denied by
default**, verified the same way the base image already proves a tool needs
none: `fugaro image selftest` runs every presence check with `--network
none` (`docs/design/base-image.md` §6, "Presence checks"). The equivalent
check here is a run-time one: a loopback-only network namespace (or, at
minimum, an egress test in CI that starts the sidecar with no route to the
public internet and confirms compression still completes) proves Headroom
needs nothing beyond the loopback socket Claude Code's traffic already
flows through. **If Headroom cannot run with outbound network denied — if
its compression is inherently a hosted service call — this design's
recommendation changes to: do not ship it**, since "data never leaving the
container" is a stated requirement of this task, not a nice-to-have, and a
compression layer that itself phones home would be strictly worse for data
exposure than the problem it solves.

**License.** Apache-2.0, the same license already carried by Codex CLI,
Gemini CLI, Qwen Code and Goose in the base image (base-image.md §6's
table) — no new licensing review needed beyond the ordinary attribution the
base image already does for its Apache-2.0 harnesses.

**SECURITY.md's own terms.** This feature would need its own line (or
sub-bullet) in the trust-model section once built, parallel to the existing
gateway bullet: *"With compression on (`agent.compression`), Headroom sees
every model call's full body, including tool outputs, before Anthropic does;
it runs in the same container and user as the agent, with network egress
denied."* It does not change any existing bullet's truth value — it adds a
new one, the same pattern `SECURITY.md`'s own history shows (the gateway and
named-oauth-tokens' per-account job variants each earned their own bullets
when they shipped, rather than editing an unrelated one).

**How a malicious tool output could attack the compressor.** The agent runs
untrusted repository content constantly — a file's contents, a PR comment, a
log line — any of which could be prompt-injection text aimed at the *model*
(already SECURITY.md's known limit: "a hidden launcher... instructions...
the agent follows"). A compression layer introduces a **second audience**
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

**What must never be compressed (the bypass list, recommendation):**

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
  (`GatePrompt`/`gateBlock`, `internal/runner/prompts.go:85-104`, already
  wrapped as "untrusted output of commands.%s") must reach the model intact:
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

**A concrete, testable default:** compress only tool-result content from
turns older than a fixed recent window (for example, the last N turns, or
everything before the most recent `git diff`/`fugaro verify` call in the
session), never the most recent turn, and never a turn whose content matches
the bypass categories above by tool/command shape (the same kind of
allowlist-not-parser discipline `internal/agent/relay.go`'s
`bashSubcommands`/`isBareword` already uses for a different purpose — "an
unsafe guess here is [exposure] every launcher can read," `relay.go:311`,
generalizes to "an unsafe compression here is context the model can no
longer act on correctly"). This is a recommendation for the eventual
implementation to validate against Headroom's actual configuration surface
(UNVERIFIED: whether Headroom exposes a bypass/pin mechanism at all — if it
does not, that is a reason not to ship it, not a reason to work around it).

**Observability: before/after token counts.** `result.json`'s
`StageTiming` today holds only `{Name, StartedAt, DurationS}`
(`internal/runstore/runstore.go:106-110`) — there is **no per-stage token
field in the record at all yet**, a gap `docs/design/generic-tool.md` §0
item 8 and §1.1 (the 0.8.0 cost preview, G20) already identify and plan to
close by adding `model_usd` and `model` to a per-stage record, sourced from
the gateway's `StageReport` or the oauth result event's `total_cost_usd`.
**This design depends on that work landing first** (or landing together with
it): compression's own before/after counts are only interesting relative to
a per-stage token baseline that does not exist in `result.json` today.
Proposed, pending that work: `StageTiming` gains (all **proposed, not
built**) `TokensBefore`, `TokensAfter int64` (Headroom's own reported
counts, when it reports them — UNVERIFIED whether it does) and
`CompressionBypassed int` (how many turns the bypass list excluded). `fugaro
diagnose` gets a proposed `Compression: <tool> N% (before → after tokens)`
line per stage, printed only when the run had compression on, the same
"print nothing for a repository that doesn't use this" discipline
`layered-config.md`'s LAYER column and named-oauth-tokens' `account:` line
both already follow.

**How a user disables it per workflow.** `agent.compression: off` in the
repository's `fugaro.yaml` (§4), or a proposed `fugaro run --no-compression`
flag for one-off debugging of a run that looks wrong, mirroring
`--no-budget-check`'s shape (`internal/cli/run.go:150`) — a client-side
override that still lets the stage run, just without the sidecar in the
loop.

## 4. Config surface

**A new key, `agent.compression`, default off (unset).** Proposed values:
`off` (or absent) and a named tool, `headroom` — a string rather than a bare
boolean, so a second compressor could be added later without a breaking
change, the same reasoning `agent.auth` already uses for its own closed
enum. No sub-options in v1 (no "aggressiveness" knob): the bypass list of
§3 is Fugaro's own, not configurable per repository, so there is nothing
else safe to expose yet.

**The three edits** (`docs/design/recipes.md` §2, restated by every design
since: "Config is strict-parsed in three places"), concretely, following the
shape `OAuthToken` just added (`internal/config/config.go:159-168`):

1. **The Go struct**, `internal/config/config.go`'s `Agent`: `Compression
   string \`yaml:"compression,omitempty"\`` next to `Recipe`/`OAuthToken`.
2. **`Validate`**, `internal/config/validate.go`, next to the existing
   `Agent.OAuthToken` shape check (`validate.go:146-151`): the value must be
   `""` or `"headroom"` (a closed set, like `agent.auth`'s
   `slices.Contains([]string{"vertex", "api-key", "oauth"}, ...)`,
   `validate.go:121`), and — mirroring §1.1's refusal — `agent.compression`
   set alongside `agent.auth: oauth` is refused outright until the
   oauth/`ANTHROPIC_BASE_URL` question is settled by a live check, the same
   pattern `VertexBudgetRefusal` already uses for a different
   auth/feature combination (`internal/runner/gateway.go:26-28`).
3. **`schemas/fugaro.schema.json`**, the matching `enum`/`pattern`, plus a
   **`testdata/config` corpus file** exercising it, which
   `TestFugaroSchemaCorpus` already holds every new key to
   (`docs/plans/2026-10-08-base-image.md:147`).

**Strict-parser consequences for older binaries.** A pre-feature runner's
`config.Parse` (`dec.KnownFields(true)`, `config.go:489`) refuses an unknown
`compression` key outright — the same version-skew shape as
`agent.recipe` (0.5.0), `gcp_project` (0.4.0) and `agent.oauth_token`
(0.6.5). A `compressionSince` constant and an image-build-record check
(mirroring `namedTokensSince`/`checkImageSince`,
`docs/plans/2026-10-09-named-oauth-tokens.md` Task 6) refuses *launching* a
run that would set it against an image built before the feature shipped,
naming `fugaro image refresh` as the fix.

**Scope (the project-layer table, `docs/design/layered-config.md` §5):
Project — no; Profile — no; Repo — yes; Override — yes (`--compression`/
`--no-compression`).** The reasoning is the one `agent.oauth_token` already
established for the identical shape of question (T2,
`docs/design/named-oauth-tokens.md` §3): the project layer and a profile are
both keys of the same launcher-writable `fugaro/project-layer.yaml` bucket
object (`layered-config.md` §11's threat model: any launcher holding
`roles/storage.objectAdmin`, not only an operator, can write it). Unlike
`agent.oauth_token`, turning compression on does not select among
owner-defined credential identities — but it **does** change what the model
sees on every call, silently, for every repository a profile templates, with
no PR review. That is the "classic failure mode" `layered-config.md` §1
names directly, and it is strictly worse as a profile-wide default than as
one repository's own, reviewed choice. Repo-only, reviewed in a pull
request, is the safer default; Override exists so a launcher can turn it off
for a single suspicious run without touching `fugaro.yaml`.

**Operator-only vs. shared.** Unlike `claude_accounts` (named-oauth-tokens'
T1), `agent.compression` does not mint a new credential identity or a new
Cloud Run job variant — it is a behavior flag on an existing workflow's
existing job, so it does **not** need the local-config-only treatment
reserved for things that fork trust boundaries. It belongs in the
repository's own `fugaro.yaml`, PR-reviewed, like `agent.recipe`. A
**policy-ceiling-shaped kill switch is still worth considering** — an
owner who wants to hold the line at "off everywhere until I say otherwise,"
the same shape `budget.*`'s "the ceiling is the owner's... a repository only
tightens" already uses (`layered-config.md` §5's Why column for
`agent.max_run_tokens`/`budget.*`) — proposed as a future addition if the
owner wants to force it off installation-wide during the opt-in period of
§6, not built in v1.

**Interaction with recipes.** `docs/design/generic-tool.md` §3.3 already
rules that loop knobs (`agent.first_line_rounds`, `agent.review_rounds`)
"stay outside the recipe" — a recipe is a topology, not a place for
`agent.*` tuning. `agent.compression` follows the identical rule: it is a
plain `agent.*` key, never a recipe field, and no catalog recipe (§3.5
there) gains a compression setting of its own in v1.

**Interaction with named Claude accounts.** Orthogonal by construction:
`agent.oauth_token` (named-oauth-tokens.md) picks which secret a workflow's
job mounts; `agent.compression` decides whether a sidecar sits in front of
that workflow's model calls. The only coupling is §1.1's refusal —
`agent.compression` set on a workflow whose `agent.auth` is `oauth` (any
named account or the default) is refused until the live check settles
whether oauth can route through a proxy at all; once (if) it is settled,
the refusal lifts for every named account equally, since none of them
changes the auth *mechanism*, only which secret backs it.

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

**arm64 support.** The base image already builds and publishes both
architectures (`base-image.md` §12); Headroom needs its own arm64 artifact
for parity. If one is missing, base-image.md's existing fallback applies
mechanically (an `amd64`-only row in `images/base/tools.tsv`, the install
step skipping arm64 with a build-log note) — but because this is a
security- and correctness-relevant component when enabled, not an optional
convenience harness, this design's recommendation is to **refuse
`agent.compression: headroom` outright on an arm64 job** (a validate-time or
launch-time check) rather than silently running an amd64-only image on an
amd64-only job and calling it "fine because Cloud Run is amd64-only anyway"
— today's installations are amd64-only (`base-image.md` §12, "the
installation's copy stays amd64-only, and that is correct"), so this is a
forward-looking guard, not a current blocker, kept so a future arm64 backend
does not silently run compression nobody built for that architecture.

**Presence verification.** A new row in `images/base/tools.tsv`
(`images.BaseTools`), tier `kit` (not `critical` — a run with compression
off needs nothing from it, unlike `claude` or `git`), checked by `fugaro
image selftest`'s existing smoke with `--network none`
(`base-image.md` §6, "Presence checks") — which conveniently doubles as the
first half of §2's "no outbound network" requirement, since a tool that
needs the network to answer `--version` or a health probe already fails
that smoke today for every other row in the table.

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
- Named OAuth tokens, a comparably-scoped feature, was explicitly sequenced
  to start only after 0.7.0 merges, "to avoid `internal/infra` merge churn
  with the base-image consolidation" (`named-oauth-tokens.md` §8) — this
  design touches `internal/agent/env.go` and the base image's harness
  layer, both squarely inside 0.7.0's own change surface, for the same
  scheduling reason, not a functional dependency.

**What gates it, concretely, before any code merges:** the §6 measurement
protocol must run in the sandbox and clear its thresholds; independently,
and before even a sandboxed trial, the §1.1 live check (does
`ANTHROPIC_BASE_URL` compose with `CLAUDE_CODE_OAUTH_TOKEN`) and the §2
no-outbound-network verification must both pass. None of these three gates
is this design's to satisfy — they are live, owner-run checks (§6, §7).

## 6. Measurement plan

**This is the gate for ever defaulting `agent.compression` on — not merely
for shipping it as opt-in.** Even a clean pass keeps it opt-in in v1 (§4);
this plan exists to decide whether Headroom is worth shipping *at all*, set
against the vendor's own claims.

**Protocol: a sandbox-only A/B over identical tasks.** Sandbox installation
only (the global rule: never EdgeWeb, never EdgeServer). A fixed task corpus
— the owner's own choice of 15-20 representative real or realistic tasks,
spanning at least one small bug fix, one feature addition touching several
files, and one task expected to need several review rounds (to exercise the
bounce/fix loop, where intra-session compression matters most, §3) — each
run **twice**, paired, same model pins, same recipe, same repository state:
once with `agent.compression: off` (control) and once with
`agent.compression: headroom` (treatment). Pairing (not two independent
random samples) controls for task-to-task variance, which otherwise swamps a
small sample.

**Metrics, all read from existing or §3's proposed `result.json` fields:**

- **Cost** — `Cost.ModelUSD`/`Cost.TotalUSD` (`internal/runstore/cost.go`),
  already in `result.json` today.
- **Input/output tokens** — **blocked on** the 0.8.0 cost-preview's
  per-stage token field (`docs/design/generic-tool.md` §1.1, G20) landing
  first; without it, only the run-level token figure Claude Code's own
  result event reports is available, which is coarser than this plan would
  like. If G20 has not shipped when this measurement runs, fall back to the
  coarser run-level number and say so in the results, rather than blocking
  the whole measurement on it.
- **Review rounds** — `len(Record.Reviews)` and each entry's `Bounce`
  (`internal/runstore/runstore.go`'s `ReviewSummary`).
- **Pass rate** — `Record.Outcome == "ready"` (the readiness rule: a
  verified passing test on the final commit and a senior `ship`,
  `docs/design/recipes.md` §4) and each `verify.Record.Passed`.
- **Wall time** — `Record.StartedAt`/`FinishedAt`, and per-stage from
  `Stages []StageTiming`.
- **Failure modes** — `Record.Status`/`Reason`, counting `halted` and
  `infra_error` separately from an ordinary `failed`, since a sidecar crash
  (§1.5) should show up distinctly from a model mistake.
- **A class automatic metrics cannot catch:** whether a `ship` verdict or a
  passing-looking fix is *wrong* because the reviewer or the coder acted on
  a compressed summary that silently dropped something load-bearing (§3's
  core risk). This needs a **human (or a separate, uncompressed LLM-judge)
  read of a sample of the treatment group's diffs and review transcripts**,
  not just the automatic pass/fail the gateway and verify records give —
  recommend the owner (or a reviewer they assign) reads at least a third of
  the treatment runs' diffs and review verdicts by hand, specifically
  looking for "this verdict doesn't match what the diff actually says."

**Sample size (recommendation, not a statistically derived number — the
owner rules on it).** At least 20 paired runs (40 total launches) as a
starting batch; double it if the first batch's cost or pass-rate variance
looks too high to read a 20% difference out of it with any confidence. This
repository's own best-of-N machinery (`fugaro run --attempts N`,
`docs/design/generic-tool.md` §3.6, G13) is a close existing primitive for
*parallel* attempts of one task; it is not quite this plan's shape (paired
control/treatment of the *same* task, not N attempts choosing the best), so
the owner runs each pair as two ordinary, separately-launched runs rather
than one `--attempts` batch.

**Who runs it.** The owner, in the sandbox installation only, following this
repository's existing live-check discipline (`docs/gcp-live-checklist.md`'s
own rules: cleanup registered before any side effect, `FACT:` lines pasted
back into the PR and the checklist, ADC only, ambient credentials only
where the checklist already requires them). This plan's §7 proposes it as
new, numbered checks in that file, continuing the existing sequence, rather
than a separate document — the pattern every other "the owner must run this
in the sandbox" item in this repository already uses.

**Pass/fail thresholds (recommendation, veto-able — the owner rules).**
Given the vendor's own, UNVERIFIED claim of "about 20% [fewer tokens] on
coding agents" (the conservative end of its range, and the one closest to
Fugaro's actual workload), require the *measured* floor to clear that
number before trusting the vendor's higher, structured-data figures at all:

- **Cost/token reduction:** treatment's mean `Cost.ModelUSD` (or token
  count, if available) at least 20% below control's, across the paired
  sample.
- **Pass rate:** no more than one additional failed/halted/infra_error run
  in the treatment group per 20 paired runs, relative to control.
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
the owner approving this design and, separately, on the live checks of §1.1,
§2 and §6 before the feature is ever enabled by default anywhere.

## 8. Non-goals

- A per-repository or per-recipe compression *strategy* (aggressiveness
  knobs, custom bypass lists) — v1 is a single, Fugaro-chosen bypass policy
  and an on/off switch.
- MCP-server-mediated tool execution (§1.3) — a separate, much larger
  redesign of how Claude Code's tools are exposed, not proposed here.
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
