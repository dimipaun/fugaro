# Headroom compression: implementation plan

**Goal:** Let a workflow opt into routing its Claude Code model calls through
Headroom, a third-party context-compression proxy, to cut token cost, for
`agent.auth: api-key` workflows with the budget gateway already on, without
weakening the budget gateway, the agent's credential allowlist, the managed
settings file's routing protection, or any loop (review, verify, gate) that
depends on reading something exactly. **v1 does nothing for usage-limit
stalls**: it refuses `oauth` (and `vertex`) outright — see the design's own
summary. No code in this plan has been written or run. **This PR (the one
that adds this plan and its design doc) is docs-only: no file under this
plan's "Files" lists is touched by it.** Tasks 1 onward are future work,
gated on the owner approving the design and, independently, on the live
checks in Task 10 before the sidecar is ever started by a real run — not
only on approval of the plan's shape.

**Spec:** [docs/design/headroom-compression.md](../design/headroom-compression.md).
Its decisions H1 (integration surface: a chained, standalone sidecar,
requiring the gateway, refused with `auth: oauth` or `auth: vertex` until
their respective live checks settle them) and H2 (fail closed, not fall back
to direct) are settled there; this plan's own decisions continue the same
letter, P1 onward, each independently vetoable. **A code review of this
plan's first draft found eight blocking gaps and several smaller ones**
(the managed settings file, the real-key exposure without the gateway,
Vertex's different routing variables, the bypass list having no enforcement
point, a merge/release gate contradiction, a silent no-op config key, a
mischaracterized fail-closed rationale, several scope/schema omissions, and
some invented test placeholders); this revision folds every one of them in,
and the design doc carries the same fixes. Where this plan now differs from
an earlier draft, the text says so rather than silently rewriting history.

**Depends on:** release 0.7.0 merged to `main` first (the base-image
consolidation and bucket-IAM hardening both touch `internal/infra` and the
harness layer heavily; design §5 states this is a scheduling dependency, the
same reasoning `docs/plans/2026-10-09-named-oauth-tokens.md` used for itself).
**Also depends on, but does not block on:** the 0.8.0 cost-preview's
per-stage dollar field (`docs/design/generic-tool.md` §1.1, G20 — **dollars**,
`model_usd`/`model`, not a token field; an earlier draft of this plan
conflated the two) — Task 5 below degrades gracefully to a coarser,
run-level dollar figure if that work has not landed yet (design §6), so this
plan does not stall on it, but an implementer starting Task 5 should check
whether `StageTiming` already carries `model_usd`/`model` before choosing
its approach.

**Checked before this plan was written:** every fact the design doc cites was
read from the code at this branch's base — `internal/agent/agent.go`,
`internal/agent/env.go`, `internal/agent/settings.go`, `internal/agent/relay.go`,
`internal/runner/agentenv.go` (the managed-settings and routing-check code,
missed in this plan's first draft), `internal/gateway/gateway.go`,
`internal/gateway/sse.go`, `internal/gateway/forward.go`,
`internal/runner/gateway.go`, `internal/runner/prompts.go`,
`internal/runner/runner.go`, `internal/runner/halt.go`,
`internal/runstore/runstore.go`, `internal/runstore/cost.go`,
`internal/verify/verify.go`, `internal/procgroup/procgroup.go`,
`internal/config/config.go`, `internal/config/validate.go`,
`internal/config/scope.go`, `internal/task/task.go`, `schemas/task.schema.json`,
`schemas/result.schema.json`, `docs/project-layer.md`, `SECURITY.md`,
`docs/design/base-image.md`, `docs/design/generic-tool.md`,
`docs/design/layered-config.md`, `docs/design/recipes.md`,
`docs/design/named-oauth-tokens.md`, `docs/gcp-live-checklist.md`,
`docs/release.md`, `images/base/tools.tsv`. If the merged code differs when a
task starts (`internal/agent/env.go`, `internal/runner/agentenv.go` and
`internal/runner/gateway.go` are all mid-churn candidates, given 0.7.0's and
0.8.0's own work nearby), that task's implementer adapts its edits to the
then-current file, not the other way round — the same rule
`docs/plans/2026-10-08-upgrade.md` and `docs/plans/2026-10-09-named-oauth-tokens.md`
both used for themselves.

## Decisions (veto any before execution starts)

Continuing the design doc's H1 and H2:

- **P1. The sidecar is its own package, `internal/headroomproc`, not folded
  into `internal/agent` or `internal/runner`.** It owns process supervision
  only (start, readiness probe, liveness probe, stop, process-group kill,
  a memory ceiling) and knows nothing about Claude Code, the gateway, or
  config — the same separation `internal/gateway` already keeps from
  `internal/runner`. *Veto alternative:* put it directly in
  `internal/runner`; rejected because process supervision with a readiness
  probe is reusable, testable machinery that has nothing runner-specific
  about it, and a separate package is easier to unit-test without a fake
  run.
- **P2. The readiness probe is a plain HTTP health check on the sidecar's
  own ephemeral loopback port, not a fixed sleep, and it must prove the
  listener is the process the runner started, not merely that something
  answers.** A fixed, well-known port would let a command that runs before
  the agent (the check gate, `docs/design/generic-tool.md` §3.6) bind it
  first and receive whatever the runner or Claude Code sends there — an
  ephemeral port (`"tcp", "127.0.0.1:0"`, `gateway.Start`'s own pattern)
  closes the obvious half of this, but the probe itself must also close the
  other half: a per-start random token the probe requires echoed back (or
  an equivalent proof the answering process is the one `Start` launched),
  not just a 200 from *a* listener on that port. *Veto alternative:* a
  fixed startup delay with no origin check; rejected on both counts — flaky
  by construction on a loaded Cloud Run instance, and blind to
  impersonation.
- **P3. `EnvSpec` (`internal/agent/env.go`) gains an optional
  `Compression *CompressionProxy{BaseURL string}` field, resolved and
  chained in front of `Gateway` when both are set.** `BuildEnv` refuses
  `Compression != nil` whenever `Auth` is `oauth` **or** `vertex` — not only
  `oauth`, which an earlier draft of this plan missed (design §1.1: Vertex
  uses `ANTHROPIC_VERTEX_BASE_URL` and a `/v1` path shape `Compression`
  never touches, so a Vertex run would silently skip the sidecar rather than
  refuse it, the opposite of fail-closed). `BuildEnv` also refuses
  `Compression != nil` when `Gateway == nil` — compression requires the
  gateway, so this should be unreachable by the time `fugaro.yaml`
  validation (Task 1) and the runner's own precondition (Task 4) are both
  in place, but `BuildEnv` enforces it a third time at the point closest to
  the real credential, the same defense-in-depth posture the existing oauth
  refusal already has (it also duplicates a rule `Validate` enforces
  elsewhere). *Veto alternative:* have the runner skip the gateway's own
  `ANTHROPIC_BASE_URL` write entirely and set Headroom's URL via a
  completely separate code path; rejected because it would duplicate the
  "last writer wins, deliberately" comment's guarantee instead of extending
  it.
- **P4. The sidecar is started by the runner, once per run, after
  `startGateway` succeeds and before the first stage that needs it — not
  once per stage, and not before the gateway.** Two separate points, both
  settled here: (a) a run's stages share one Claude Code identity and one
  budget session already, so starting a fresh Headroom process per stage
  adds startup latency on every stage for no benefit this design can
  identify; (b) **the ordering relative to the gateway is not a choice, it
  is forced** — design §1.1's chained-in-front-of-the-gateway architecture
  means Headroom's own upstream configuration must be the gateway's
  loopback URL and token, which do not exist until `gateway.Start`
  (`internal/gateway/gateway.go:192-230`) returns them inside
  `startGateway`. An earlier draft of this plan had `startCompression`
  called "before `startGateway`," which cannot work: there is no mechanism
  to tell Headroom where to forward before the gateway it forwards to has
  a listening address. *Veto alternative (for point (a) only):* per-stage
  restart, for a stronger "always-fresh" guarantee against a misbehaving
  sidecar; rejected as unnecessary given Task 4's health monitoring already
  fails the stage (not silently continues) on a crash or a hang. There is
  no veto alternative for point (b) — the ordering is a hard dependency,
  not a design choice.
- **P5. `writeManagedSettings` — not a second, independent code path — also
  writes Headroom's URL into the managed settings file whenever compression
  is on, in the same call that already writes the gateway's variables.**
  This is the fix for the gap the design's §1.1a found:
  `internal/runner/agentenv.go:65-84`'s `writeManagedSettings` calls
  `agent.GatewayVars` on its own, independently of whatever `BuildEnv` put
  in the agent's *environment*, and writes the result into
  `/etc/claude-code/managed-settings.json`, which takes precedence over the
  environment. Changing only `EnvSpec`/`BuildEnv` (P3) would leave the
  managed file still pointing Claude Code at the gateway directly, silently
  bypassing Headroom. *Veto alternative:* a second, parallel write from a
  new code path; rejected — two independent writers of the same file is
  exactly the kind of duplication that drifts out of sync, which is the
  bug this fix exists to prevent.
- **P6. `checkSettingsRouting` needs no new condition, because P9 (below)
  already requires the gateway for compression.** `agentenv.go:108-159`'s
  routing check already runs whenever `r.gatewayOn()`
  (`runner.go:1434-1439`, `gateway.go:358-360`); since compression can only
  be on when the gateway is (P9), that check already covers every
  compression-eligible run with no new call site. The plan still needs a
  test that proves this explicitly (a committed `.claude/settings.json`
  setting a routing key is refused identically whether compression is on or
  off), rather than assuming the existing condition "just works." *Veto
  alternative:* add an explicit `|| compressionOn()` to the call site
  anyway, for readability; the implementer may choose this if it makes the
  code clearer, but it changes no behavior, so it is not required.
- **P7. The sidecar's own process environment is an explicit, narrow
  allowlist the launcher builds — never the runner's `os.Environ()`.**
  Mirrors `agent.Claude.Run`'s own refusal of a nil `Env`
  (`internal/agent/agent.go:101-108`): `headroomproc.Start` takes an
  explicit `Env []string` (never defaults to inherit), and the runner's
  caller (Task 4) builds it from nothing but what Headroom's own
  configuration needs (its upstream URL, a token if its config format needs
  one) — never the GitHub App key, the real model/provider keys, or
  anything `ADC`-shaped. The sidecar's stdout/stderr are discarded, not
  relayed to Cloud Logging, since they may contain request or response
  fragments (§2's "sidecar environment and logs" point). *Veto
  alternative:* relay the sidecar's logs through the existing redaction
  pipeline (`internal/agent/relay.go`'s `Redactor`); rejected for v1 because
  that pipeline is built and tested against Claude Code's own stream-json
  shape, not an arbitrary third-party binary's log format, and a redactor
  tuned for the wrong shape is a false sense of safety.
- **P8. A liveness probe and a memory ceiling, not only a startup readiness
  check.** `headroomproc.Process.Dead()` only catches the process exiting;
  a hang (the process stays up but stops answering) is invisible to it.
  `headroomproc` adds a periodic liveness probe (the same ephemeral-port,
  origin-proof check as P2, run on an interval, not only once at startup)
  with a bound after which a hang is treated exactly like a crash (P10). A
  memory ceiling on the sidecar process (a cgroup limit via the same
  process-group handle `procgroup` already manipulates for
  SIGTERM/SIGKILL, or an explicit flag if Headroom's own configuration
  exposes one) keeps a compression pass over a very large tool output from
  using enough of the container's one shared memory limit
  (`workflows.*.resources.memory`) to OOM-kill the whole run, not just the
  sidecar. *Veto alternative:* no separate memory ceiling, relying on the
  container's own OOM killer; rejected because an OOM kill of the whole
  container is a worse failure (an `infra_error` indistinguishable from a
  crashed stage, design `docs/design/checkpoint-pushes.md`'s own worked
  example of exactly this) than a ceiling that lets the sidecar alone fail
  first, cleanly, into P10's path.
- **P9. `agent.compression` requires `agent.auth: api-key` and
  `budget.mode: observe` or `enforce`.** Resolved against the final,
  layered config (`config.Resolve`), not a single file's raw `Validate` —
  `agent.auth` is itself `InProject | InRepo`
  (`internal/config/scope.go:53`), so a repository file alone cannot always
  say what the effective auth mode is; the check has to run after
  resolution, in the launching CLI's and the runner's shared path, not
  inside `config.Validate` (which only ever sees one file). This closes two
  separate gaps at once: without it, a budget-off `api-key` run would hand
  Headroom the **real** `ANTHROPIC_API_KEY` (`BuildEnv`'s api-key branch
  without a gateway, `internal/agent/env.go:91-105`; `gatewayOn()`
  requires `r.spend.On()`, `internal/runner/gateway.go:46-48`; budget
  defaults off, `SECURITY.md:34`), and P6's routing-check reuse would not
  actually apply (it only fires when the gateway is on). *Veto
  alternative:* allow compression without the gateway, and instead document
  and test the real-key exposure as an accepted risk; rejected because the
  gateway-required rule is strictly safer and costs nothing a v1, api-key,
  opt-in feature needs to give up.
- **P10. A sidecar crash or liveness-check failure maps to
  `runstore.StatusInfraError`, with a plain `Reason` string — never a new
  `HaltReason` and never `StatusHalted`.** An earlier draft of this plan
  invented `HaltCompressionDied`, but `runstore.go:34`'s own comment on
  `StatusHalted` is explicit: `"halted" // a budget or token limit stopped
  the run` — a sidecar dying is neither. `infra_error` is the status this
  codebase already uses for exactly this shape of problem, including
  mid-run (the routing check's own refusal is `infra_error`,
  `docs/design/v1.md:195`; a container killed by a resource limit mid-stage
  is `infra_error` too, `docs/design/checkpoint-pushes.md`'s worked
  example) — it is not limited to bootstrap-time failures, despite one
  `docs/design/recipes.md:95` passage describing only that case. *Veto
  alternative:* a new `Halt`/`HaltReason` value; rejected as a type error,
  not merely a style choice — `Halt` and its reasons are specifically about
  budget and token limits throughout the schema (`schemas/result.schema.json`'s
  closed `halt.reason` enum has no room for this either, and would need an
  edit that doesn't belong next to budget semantics).
- **P11. `agent.compression` ships reserved-and-refused in Task 1, before
  any sidecar code exists, and the refusal lifts only in the same change
  that makes Tasks 3-4 real.** The alternative — `Validate` accepting
  `"headroom"` as soon as the Go struct and schema exist, before anything
  reads the field — would be a silent no-op exactly like the thing design
  §1.5 argues against for the sidecar's own failure mode: a `fugaro.yaml`
  with `agent.compression: headroom` would parse, validate and run, doing
  nothing. The fix is the same shape `docs/design/recipes.md` §4 already
  uses for `checks`, `goto`, `extends` and `docs/design/layered-config.md`
  §6 uses for a profile's `extends`: a known key, strictly parsed, refused
  with a message naming it "not yet implemented," until the PR that
  implements it changes the refusal to an acceptance. *Veto alternative:*
  ship Task 1 and the sidecar in one PR, so the key is never reserved
  without being usable; rejected only because it makes Task 1 a much larger,
  less independently-reviewable unit — Task 1's own `Validate` test suite
  already needs the reserved-and-refused behavior as its starting state
  regardless of how the PRs are split, so splitting costs nothing.
- **P12. The per-call cache read/write split is recorded per run, summed,
  independent of G20.** Design §3's prompt-caching risk means §6's
  measurement needs the cache read/write totals per arm, not just a dollar
  figure — the gateway already computes this per call
  (`internal/gateway/sse.go:144-169`, `forward.go:759`) but never sums it
  into anything `result.json` keeps. This is a small, additive field (on
  `Cost`, next to `ReportedUSD`/`Unreconciled`) that Task 5 adds regardless
  of whether G20's `StageTiming.model_usd` has landed, since it answers a
  different question (did the cache behave as expected) than G20 does (what
  did a stage cost).

## Global Constraints

Every task's requirements include these.

From the design:
- `agent.compression` defaults to off; no behavior changes for a repository
  that never sets it (design §4's "default off", mirrored exactly from
  `named-oauth-tokens.md`'s "An installation that adds no `claude_accounts`
  entry is unaffected, in full").
- `agent.compression` is refused outright (not merely a shape check) with
  `agent.auth: oauth`, with `agent.auth: vertex`, and with the budget off
  (P9) — resolved against the final layered config, not a single file's
  `Validate` alone (P9).
- `agent.compression` is **reserved and refused** (P11) until Tasks 3-4 are
  complete; no intermediate state exists where the key is accepted but does
  nothing.
- The sidecar never receives outbound network access by default; Task 7's
  CI docker test proves an actual compression pass completes under
  `--network none` (not only that the binary answers `--version` — design
  §2, §5; this is a **tested**, not an enforced, property, since an
  unprivileged Cloud Run container cannot itself block an outbound call the
  sidecar's own code decides to make).
- The sidecar's own environment is an explicit allowlist, never the
  runner's inherited one (P7); its stdout/stderr are discarded, never
  relayed to Cloud Logging unredacted (P7).
- A sidecar crash or a detected hang **fails the stage/run closed as
  `StatusInfraError`** (design H2, P8, P10) — no task adds a silent
  fallback to direct, uncompressed routing, and no task invents a new
  `Halt`/`HaltReason` value for this.
- There is **no `--compression` flag to force the feature on**; only
  `--no-compression` exists (design §4's scope reasoning: turning it on is
  always a reviewed `fugaro.yaml` change).
- `task.Spec`'s strict decoding stays strict; the new field this plan adds
  (Task 6) is `omitempty` and gated by a version check before it is ever
  set to a non-default value (the same shape as `OAuthToken`,
  `named-oauth-tokens.md` §5).
- No live network call to Headroom's own infrastructure, Anthropic, or any
  cloud in any automated test. The fakes-only rule below covers this.

Project rules (carried over from this repository's existing plans):
- Every task's own test run is in the **foreground**, with `-count=1`, and
  `-race` where the package's existing tests use it.
- Fakes only in tests: a fake Headroom-shaped HTTP server (a `httptest`
  server returning canned responses), a tiny scripted fake binary (a
  temp-file shell script, the same shape `internal/runner/runner_test.go`'s
  own `exec.Command("sh", "-c", script)` pattern already uses) standing in
  for the real pinned sidecar, `gcpfake` where infra is touched, no real
  `claude`, no real Headroom binary.
- Docs must match behavior: the docs task (Task 8) keeps every command and
  key this plan's docs mention real, the same pattern
  `plugin/skills_lint_test.go` and the `docs_*_test.go` family already
  enforce for every other feature.

## Review Focus (mutation-proof notes)

The failure modes most likely to hit an owner, each pinned by a test that
would fail if the fix were subtly wrong:

1. **A run with `agent.compression` unset behaves differently from today.**
   Pinned by `TestBuildEnvUnchangedWithNoCompression` (Task 3),
   `TestWriteManagedSettingsUnchangedWithNoCompression` (Task 3/4 — the
   managed-settings fix P5 needs its own golden test, separate from
   `BuildEnv`'s, since they are two different code paths that must both stay
   byte-identical with compression off) and `TestRunnerStartsNoSidecarByDefault`
   (Task 4) — all three assert zero new process starts, zero new env or
   settings keys, byte-identical `ANTHROPIC_BASE_URL` handling to today's
   gateway-only path.
2. **`agent.compression` silently applies to an `oauth` or `vertex`
   workflow, or to a budget-off `api-key` workflow.** Pinned by
   `TestResolvedConfigRefusesCompressionWithOAuth`,
   `TestResolvedConfigRefusesCompressionWithVertex` and
   `TestResolvedConfigRefusesCompressionWithoutGateway` (Task 1, against
   the *resolved* config, not a single file — P9), plus
   `TestBuildEnvRefusesCompressionWithOAuth`,
   `TestBuildEnvRefusesCompressionWithVertex` and
   `TestBuildEnvRefusesCompressionWithoutGateway` (Task 3, the
   defense-in-depth copy of the same three rules at the point closest to
   the credential) — all six assert a hard error, never a silent skip.
3. **The managed settings file still routes around Headroom even though the
   environment is correct.** Pinned by
   `TestWriteManagedSettingsCarriesCompressionURL` (Task 3/4, P5): with
   compression on, asserts the written managed-settings JSON's
   `ANTHROPIC_BASE_URL` is the sidecar's URL, not the gateway's — the exact
   gap design §1.1a found, which no test in this plan's first draft would
   have caught (it only tested `BuildEnv`, never `writeManagedSettings`).
4. **A committed `.claude/settings.json` routes around Headroom.** Pinned
   by `TestCheckSettingsRoutingAppliesWithCompression` (Task 4, P6): a
   repository settings file setting `ANTHROPIC_BASE_URL` is refused
   identically whether `agent.compression` is on or off, proving P9's
   "already covered" claim rather than assuming it.
5. **A sidecar crash or hang is swallowed instead of failing the stage.**
   Pinned by `TestRunnerFailsStageOnSidecarDeath` and
   `TestRunnerFailsStageOnSidecarHang` (Task 4): both assert the stage/run
   ends `runstore.StatusInfraError` (P10), never a retry against the real
   API and never `StatusHalted`.
6. **The bypass list is asserted as if Fugaro enforces it, when it cannot.**
   No task in this plan implements a bypass mechanism in Fugaro's own code
   (design §3: Claude Code talks to Headroom directly; Fugaro builds no
   request body in that path). This plan does **not** include a
   `TestForwardNeverCompressesBypassedPaths`-shaped test, because it would
   test something Fugaro's code does not do — the bypass list is entirely a
   Task 10 go/no-go live-check question against Headroom's actual
   configuration surface, not a unit-testable claim.
7. **An old runner or an old job image silently runs compression it
   predates.** Pinned by `TestOldConfigParserRefusesCompressionKey` (Task 1,
   the strict-decode version-skew test, the same shape as
   `TestOldRunnerRefusesOAuthTokenField`, with a real test body — see Task
   1) and `TestCompressionImageGateRefusesOldBuild` (Task 6, mirroring
   `named-oauth-tokens.md`'s `TestRunTokenImageGateRefusesOldFirstRun`).
8. **A port-impersonation attempt (P2) goes undetected.** Pinned by
   `TestReadinessProbeRejectsImpersonator` (Task 2): a second process binds
   the sidecar's chosen ephemeral port *before* the real sidecar can, and
   answers 200 on the health path without the expected origin proof;
   `Start` must treat this as a failure, not a successful readiness check.

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `internal/headroomproc/headroomproc.go` (new), `headroomproc_test.go` (new) | Process supervision: `Start`, origin-proof readiness probe, liveness probe, memory ceiling, `Stop`/process-group kill, `URL()` | 2 |
| `internal/config/config.go`, `validate.go`, `scope.go`, `schemas/fugaro.schema.json`, `testdata/config/*`, `docs/project-layer.md` | `Agent.Compression` (reserved and refused, P11), its enum, the `Scopes` row (the fourth edit an earlier draft omitted), the corpus fixture | 1 |
| `internal/config` (a resolved-config check, wherever `fugaro run`/`fugaro validate`/the runner's bootstrap share one) | The oauth/vertex/no-gateway refusal against the *resolved* config (P9) | 1 (continued, after `config.Resolve` exists) |
| `internal/agent/env.go`, `env_test.go` | `EnvSpec.Compression`, the chaining/refusal logic in `BuildEnv`/`GatewayVars` (P3) | 3 |
| `internal/runner/agentenv.go`, `agentenv_test.go` | `writeManagedSettings` carries Headroom's URL (P5); `checkSettingsRouting`'s existing condition proven to already cover compression (P6) | 3 |
| `internal/runner/gateway.go` (or a new `internal/runner/compression.go`), `runner.go`, `halt.go`, their tests | Starting/supervising/stopping the sidecar per run, the `StatusInfraError` fail-closed path (P10) | 4 |
| `internal/runstore/cost.go`, `runstore_test.go`, `internal/cli/diagnose.go` | `Cost`'s cache read/write totals (P12, independent of G20); the `Compression:` diagnose line once a dollar figure exists | 5 |
| `internal/task/task.go`, `task_test.go`, `schemas/task.schema.json` | `Spec.CompressionOff bool` (off-only, one-shot, strictly decoded), `compressionSince` | 6 |
| `internal/cli/run.go`, `run_test.go` | `--no-compression` (off-only; no force-on flag), the `compressionSince` image gate | 6 |
| `images/base/Dockerfile`, `images/base/tools.tsv`, the base image's selftest, a new CI docker test | The pin, the `harness`-tier presence row, the real no-egress compression-pass test (not only `--version`) | 7 |
| `SECURITY.md`, `docs/compression.md` (new), `plugin/skills/routing/SKILL.md` (or wherever the implementer finds the closest fit), `internal/cli/docs_*_test.go` (new case) | The trust-model bullet, the user doc, skill mentions, docs tests | 8 |
| `docs/release.md` | A `compressionSince` line in "Before you tag", alongside `namedTokensSince`'s | 9 |
| none | Full suite, release-notes task group | 9 |
| `docs/gcp-live-checklist.md` (results pasted in) | The owner's own runs, as numbered checks (§1.1, §2, §6 of the design) | 10 |

## PR groups

Two PRs, in order: **PR 1 (Tasks 1-2)** is additive and inert — a new,
reserved-and-refused config key (P11) nothing can actually turn on, and a
standalone process-supervision package nothing calls yet — safe to merge on
its own, reviewable without touching the credential path at all. **PR 2
(Tasks 3-9)** wires it up, lifts the Task 1 refusal, and is where the
security-sensitive review load concentrates (see the per-task flags below).
Task 10 is the owner's own run, never part of either PR, and gates:
(a) whether the base image ever bakes a real Headroom binary in at all
(the no-egress CI test, Task 7, which itself needs the pin Task 10 item 1
does not block — see the reordering note on Task 10), (b) whether the
`oauth`/`vertex` refusals could ever be reconsidered (Task 10 items 1 and a
possible future Vertex check), and (c) whether this feature is ever proposed
as a default (the §6 measurement, Task 10 item 3, which needs the merged PR
2 and a built image to run against — it cannot run before PR 2 merges, the
opposite of an earlier draft's ordering).

Parallelizable: Tasks 1 and 2 are independent and may be implemented
together. Task 3 needs 1 and 2. Task 4 needs 3. Task 5 is independent of 3/4
once 1 exists (it only adds record fields and a display line) but is most
useful once 4 actually populates them. Task 6 needs 1, 3 and 4. Task 7 needs
2 (to know what artifact is being pinned) but can start in parallel with
3-6 once the pin target is chosen. Task 8 needs 1-7 settled (it documents
the shipped shape). Task 9 is last.

**Review flag.** Tasks 2, 3, 4, 6 and 7 touch, respectively: a new process
given its own narrow credential surface and memory/network bounds, the
agent's credential-env allowlist (`internal/agent/env.go` and
`internal/runner/agentenv.go`, the files `SECURITY.md` already names as in
scope: "the runner's use of provider and model tokens"), the runner's halt
and status semantics, a new strictly-decoded task-spec field with a version
gate (the same class of change `named-oauth-tokens.md` flagged for review),
and the base image's supply-chain pin (a new third-party binary with
network-adjacent trust, the same class of review `base-image.md`'s own
harness additions get). Each is flagged for review at high effort (Opus)
specifically for the security question — does this change widen what the
agent, or anything running alongside it, can reach or read — separate from
an ordinary correctness review of the same diff, the same split
`docs/plans/2026-10-08-layered-config.md:174` draws for its own Task 4 and
Task 13 ("the second with a security reviewer, since they are the merge and
the trust boundary").

---

### Task 1: `config.Agent.Compression`, reserved and refused

**Files:**
- Edit: `internal/config/config.go` (the `Agent` struct)
- Edit: `internal/config/validate.go`
- Edit: `internal/config/scope.go` (the `Scopes` table — the fourth edit an
  earlier draft of this plan omitted)
- Edit: `schemas/fugaro.schema.json`
- Edit: `docs/project-layer.md` (the printed copy of `Scopes`,
  `TestScopeTableMatchesDocs` keeps the two equal)
- New: a `testdata/config` fixture exercising the key (the reserved-refusal
  case; a second fixture is added once Task 3-4 lift the refusal)
- Test: `internal/config/validate_test.go` (or wherever `Agent.OAuthToken`'s
  equivalent cases already live — the implementer greps
  `TestValidateAgentOAuthToken`-shaped names first), `internal/config/scope_test.go`
  or wherever `TestScopeTableMatchesDocs` lives

**Interfaces:**
```go
// internal/config/config.go, in Agent, next to OAuthToken:

// Compression names the context-compression proxy this repository's
// api-key, gateway-on workflows route their model calls through: "" (off,
// the default) or "headroom" (docs/design/headroom-compression.md).
// Reserved and refused (P11) until the PR that implements the sidecar
// lands; the refusal names "not yet implemented," not "unknown field,"
// so a repository trying it gets a clear answer rather than a strict-parse
// error. Separately refused, once implemented, together with agent.auth:
// oauth or vertex, or without the budget gateway on (P9) -- that check
// runs against the *resolved* config, not here (config.Validate sees only
// one file). Binaries before compressionSince refuse this key outright.
Compression string `yaml:"compression,omitempty"`
```

```go
// internal/config/scope.go, next to agent.oauth_token's row:
{Key: "agent.compression", In: InRepo | InOverride, Why: "it changes what the model sees on every call; the project layer and a profile are the same launcher-writable bucket object agent.oauth_token already distrusts for the identical reason"},
```

- [ ] **Step 1: Write the failing tests**

```go
// internal/config/validate_test.go

func TestValidateCompressionReservedAndRefused(t *testing.T) {
	// Until Task 3/4 lift this, every non-empty value is refused, including
	// the eventual real one, with a message naming "not yet implemented".
	for _, val := range []string{"headroom", "off", "anything"} {
		cfg := validConfigFixture()
		cfg.Agent.Compression = val
		ps := cfg.Validate()
		if len(ps) == 0 {
			t.Fatalf("Compression=%q was accepted before it is implemented", val)
		}
		found := false
		for _, p := range ps {
			found = found || strings.Contains(p.Message, "not yet implemented")
		}
		if !found {
			t.Errorf("Compression=%q: refusal message doesn't say why: %+v", val, ps)
		}
	}
}

func TestValidateCompressionEmptyIsAlwaysFine(t *testing.T) {
	cfg := validConfigFixture() // Agent.Compression left at its zero value
	if ps := cfg.Validate(); len(ps) != 0 {
		t.Fatalf("an unset Compression was refused: %+v", ps)
	}
}

func TestOldConfigParserRefusesCompressionKey(t *testing.T) {
	// Decodes a fugaro.yaml with agent.compression set against a struct
	// shaped like Config *before* this task (copied from git blame, not
	// re-derived), strictly, the same discipline
	// TestOldRunnerRefusesOAuthTokenField used for task.Spec.
	data := []byte(`
version: 1
project: acme
gcp_project: acme-fugaro
git: {provider: github}
agent: {auth: api-key, compression: headroom}
workflows: {default: {base: base, commands: {build: "true", test: "true"}}}
`)
	type oldAgent struct {
		Auth         string `yaml:"auth"`
		Model        string `yaml:"model"`
		ReviewRounds int    `yaml:"review_rounds"`
		// ... every other current Agent field except Compression,
		// copied verbatim by the implementer from config.go as it stands
		// immediately before this task's edit ...
	}
	type oldConfig struct {
		Version int      `yaml:"version"`
		Project string   `yaml:"project"`
		// ... every other current Config field, copied verbatim ...
		Agent oldAgent `yaml:"agent"`
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var old oldConfig
	if err := dec.Decode(&old); err == nil {
		t.Fatal("an old-shaped decoder accepted agent.compression silently")
	}
}
```

```go
// internal/config/scope_test.go (or wherever TestScopeTableMatchesDocs lives)

func TestScopeTableMatchesDocsIncludesCompression(t *testing.T) {
	// The existing TestScopeTableMatchesDocs already walks every row of
	// config.Scopes against docs/project-layer.md's printed table; this
	// test only needs config.Scopes to have an "agent.compression" row and
	// docs/project-layer.md to print a matching one -- the generic
	// equality test catches a mismatch without a new assertion, so this
	// case is really "add the row to both files and let the existing test
	// cover it," confirmed by running TestScopeTableMatchesDocs itself.
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestValidateCompression|TestOldConfigParserRefusesCompressionKey|TestScopeTableMatchesDocs' ./internal/config/`
Expected: FAIL, `undefined: Compression`, or `TestScopeTableMatchesDocs` failing on the missing row once the struct exists.

- [ ] **Step 3: Write the implementation** (the struct field, the
  reserved-and-refused `Validate` branch, the `scope.go` row, the schema
  enum, `docs/project-layer.md`'s row, the corpus fixture)
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/config/... && go test -count=1 ./internal/config/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/validate.go internal/config/scope.go internal/config/validate_test.go internal/config/scope_test.go schemas/fugaro.schema.json docs/project-layer.md testdata/config/
git commit -m "headroom compression task 1: agent.compression, reserved and refused until implemented"
```

---

### Task 1b: the resolved-config refusal (oauth, vertex, no gateway)

Split from Task 1 proper because it needs `config.Resolve` to exist and
needs to live wherever `fugaro run`, `fugaro validate` and the runner's
bootstrap share one resolved-config code path — the implementer locates
that path first (greps `config.Resolve(` call sites) rather than guessing.
This step only adds the P9 refusal; it stays dormant (unreachable, since
Task 1's `Validate` already refuses every non-empty value) until Task 3/4
lift the Task 1 refusal, but it is written and tested now so the two
refusals land together in review rather than one being assumed later.

**Files:** wherever `config.Resolve`'s callers share a post-resolution
check (the implementer names the exact file in its commit message).

**Tests:**
```go
func TestResolvedConfigRefusesCompressionWithOAuth(t *testing.T) {
	// Repo file: agent.compression: headroom (bypassing Task 1's own
	// Validate refusal for this test's purposes -- the implementer builds
	// the resolved Config directly, not through the strict YAML parse, to
	// isolate this check from Task 1's). Project layer or repo: auth: oauth.
	// Resolve, then the new check: must refuse, naming "oauth".
}

func TestResolvedConfigRefusesCompressionWithVertex(t *testing.T) {
	// Same shape, auth: vertex: must refuse, naming "vertex".
}

func TestResolvedConfigRefusesCompressionWithoutGateway(t *testing.T) {
	// auth: api-key, budget.mode unset (off): must refuse, naming "the
	// budget gateway must be on".
}

func TestResolvedConfigAcceptsCompressionWithAPIKeyAndGatewayOn(t *testing.T) {
	// auth: api-key, budget.mode: observe: no refusal from this check
	// (Task 1's own reserved-and-refused Validate rule still fires
	// separately until Task 3/4 lift it -- this test asserts only that
	// *this* check has nothing to say about the happy path).
}

func TestResolvedConfigRefusalSeesProjectLayerAuth(t *testing.T) {
	// The case P9 exists for: the repository's own fugaro.yaml sets no
	// agent.auth at all (and agent.compression: headroom), but the project
	// layer's defaults: set auth: oauth. Resolve the two together; the
	// refusal must still fire, proving the check runs on the *resolved*
	// value, not a single file's raw field.
}
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run TestResolvedConfigRefusesCompression ./internal/config/...` (package path TBD by where `config.Resolve`'s callers live)
Expected: FAIL.

- [ ] **Step 3: Write the implementation**
- [ ] **Step 4: Run the tests to verify they pass**
- [ ] **Step 5: Commit**

```bash
git commit -m "headroom compression task 1b: refuse agent.compression on the resolved config (oauth, vertex, no gateway)"
```

---

### Task 2: `internal/headroomproc`, the process supervisor

**Flagged for a security-focused review (high effort, Opus):** a new
runner-managed process with network access, even loopback-only, is exactly
the shape `SECURITY.md` asks a report to describe precisely.

**Files:**
- New: `internal/headroomproc/headroomproc.go`, `headroomproc_test.go`

**Interfaces:**
```go
package headroomproc

// Options configure a sidecar process. Env must be non-nil, the same
// refusal agent.Claude.Run already makes for its own Env (P7): a nil Env
// inherits the launcher's full environment, which must never happen here.
type Options struct {
	Bin             string        // the pinned binary's path
	Args            []string      // confirmed once Task 7 pins a real binary; a config file path is likely, not argv
	Env             []string      // explicit allowlist (P7); never nil
	ReadyPath       string        // a health-check path on the sidecar's own HTTP server
	ReadyTimeout    time.Duration // how long Start waits for a verified-origin 200 from ReadyPath
	LivenessPeriod  time.Duration // how often, after readiness, the same check repeats (P8); 0 disables it (tests only)
	LivenessMisses  int           // consecutive failures before Dead() closes for a hang, not only an exit
	MemoryLimit     int64         // bytes; 0 means no ceiling (tests only) (P8)
	Grace           time.Duration // SIGTERM -> SIGKILL grace on Stop, mirroring procgroup's shape
	Log             *slog.Logger
}

// Process is a running sidecar.
type Process struct { /* ... */ }

// Start launches the sidecar on an ephemeral loopback port, waits for a
// readiness probe that proves the answering process is the one Start
// itself launched (a per-start random token echoed back; P2), and returns
// a handle. It fails if the process exits before becoming ready, if
// ReadyTimeout elapses first, or if something else answers the port
// without the expected token (impersonation, P2).
func Start(ctx context.Context, o Options) (*Process, error)

// URL is the sidecar's own loopback base URL.
func (p *Process) URL() string

// Dead returns a channel closed when the sidecar is no longer usable: it
// exited on its own (a crash), or LivenessMisses consecutive liveness
// probes failed (a hang, P8) -- the runner (Task 4) treats both the same
// way, as StatusInfraError (P10).
func (p *Process) Dead() <-chan struct{}

// Stop sends SIGTERM to the process group, escalating to SIGKILL after
// Grace, mirroring procgroup.Run's own shutdown shape.
func (p *Process) Stop(ctx context.Context) error
```

**Tests:**
```go
func TestStartWaitsForReadyThenReturns(t *testing.T) {
	bin := fakeSidecarScript(t, `#!/bin/sh
exec nc -l -p "$PORT" -e sh -c 'read req; printf "HTTP/1.1 200 OK\r\nContent-Length: %d\r\n\r\n%s" "${#TOKEN}" "$TOKEN"'
`) // the implementer replaces this sketch with a small Go HTTP test server
	// invoked as the "fake binary" via a thin wrapper, since a real nc
	// one-liner cannot read Start's injected token reliably -- the point
	// pinned here is behavioral: Start must not return before the probe
	// succeeds, and must time out, not hang forever, if it never does.
	p, err := Start(context.Background(), Options{Bin: bin, Env: []string{}, ReadyPath: "/healthz", ReadyTimeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop(context.Background())
	if p.URL() == "" {
		t.Fatal("a ready Process has no URL")
	}
}

func TestStartFailsIfProcessExitsBeforeReady(t *testing.T) {
	bin := fakeSidecarScript(t, "#!/bin/sh\nexit 1\n")
	if _, err := Start(context.Background(), Options{Bin: bin, Env: []string{}, ReadyPath: "/healthz", ReadyTimeout: time.Second}); err == nil {
		t.Fatal("a binary that exits immediately was accepted as ready")
	}
}

func TestReadinessProbeRejectsImpersonator(t *testing.T) {
	// Before calling Start, bind the OS-chosen ephemeral port range isn't
	// predictable, so this test instead injects a fixed port via a test-only
	// Options field (or by pre-binding a listener and passing its address
	// as the target Start probes) and starts an impersonator HTTP server on
	// it that answers 200 on ReadyPath with no token, or the wrong token.
	// Start must return an error, never a Process built on that listener.
}

func TestDeadClosesOnCrash(t *testing.T) {
	bin := fakeSidecarScript(t, readySidecarScript+"\nsleep 0.2\nkill -9 $$\n")
	p, err := Start(context.Background(), Options{Bin: bin, Env: []string{}, ReadyPath: "/healthz", ReadyTimeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-p.Dead():
	case <-time.After(2 * time.Second):
		t.Fatal("Dead() never closed after the process was killed")
	}
}

func TestDeadClosesOnLivenessFailure(t *testing.T) {
	// The fake sidecar answers ReadyPath once (so Start succeeds), then
	// stops answering without exiting (a hang, not a crash). With
	// LivenessPeriod small and LivenessMisses set, Dead() must close
	// within roughly LivenessPeriod*LivenessMisses, proving the liveness
	// probe -- not only process-exit detection -- drives Dead().
}

func TestStopKillsTheWholeProcessGroup(t *testing.T) {
	// Mirrors procgroup's own "leaked daemon" test shape: the fake binary
	// forks a child that outlives a bare SIGTERM to the binary alone; Stop
	// must still reap it (process-group signal, not a single-PID one) --
	// verified the same way procgroup_test.go already verifies this for
	// claude, by checking the child's PID is gone after Stop returns.
}

func TestMemoryLimitKillsAnOverBudgetSidecar(t *testing.T) {
	// The fake binary allocates well past a small MemoryLimit (a few MB);
	// Start/Dead() must observe it killed by the ceiling, not left running
	// to pressure the container's own, shared memory limit. Skipped on a
	// platform where the implementer's chosen enforcement mechanism (a
	// cgroup limit, most likely) isn't available to the test sandbox, with
	// a comment naming the skip reason explicitly -- never silently passed.
}
```

(This design's explicit no-outbound-network requirement (design §2) is
**not** tested here against a fake binary — a fake process proves nothing
about what Headroom's real one would try to reach. That property is tested
once, against the real pinned binary, in Task 7's CI docker test.)

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestStart|TestDead|TestStop|TestReadinessProbeRejectsImpersonator|TestMemoryLimit' ./internal/headroomproc/`
Expected: FAIL, package does not exist yet.

- [ ] **Step 3: Write the implementation**, reusing `internal/procgroup`'s
  process-group technique (`Setpgid`, signal-the-group-then-escalate) rather
  than reinventing it — the implementer checks whether `procgroup` can be
  extended with a non-blocking `Start`/`Stop` pair instead of a wholly new
  package, which would be a smaller change than P1 assumes; if so, this
  task's File Structure line moves into `internal/procgroup` and P1 is
  revisited in this task's commit message, not silently overridden.
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/headroomproc/... && go test -race -count=1 ./internal/headroomproc/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/headroomproc/
git commit -m "headroom compression task 2: internal/headroomproc, the sidecar process supervisor"
```

---

### Task 3: `agent.EnvSpec` and the managed settings file both chain the sidecar in

**Flagged for a security-focused review (high effort, Opus):** this touches
the agent's credential-env allowlist (`internal/agent/env.go`) and the
managed-settings/routing-check file (`internal/runner/agentenv.go`), both
named directly or in substance by `SECURITY.md`'s scope.

**Files:**
- Edit: `internal/agent/env.go`, `env_test.go`
- Edit: `internal/runner/agentenv.go`, `agentenv_test.go`

**Interfaces:**
```go
// internal/agent/env.go

// EnvSpec gains:
Compression *CompressionProxy // nil: off (today's behavior, unchanged)

// CompressionProxy is the sidecar Claude Code's calls route through first.
type CompressionProxy struct{ BaseURL string }
```

- `BuildEnv` refuses `spec.Compression != nil` when `spec.Auth` is `oauth`
  **or** `vertex` (P3 — an earlier draft of this plan refused only `oauth`),
  and when `spec.Gateway == nil` (P9, defense in depth — the resolved-config
  check of Task 1b should already have refused this combination before it
  reaches here, but `BuildEnv` is the point closest to the real credential
  and checks it again, the same posture its existing oauth-vs-gateway
  refusal already has).
- The "Last, so nothing declared or inherited can override the routing"
  block (`env.go:134-141`) gains a step **before** the existing
  `GatewayVars` call: when `spec.Compression != nil`, `ANTHROPIC_BASE_URL`
  becomes `spec.Compression.BaseURL`, not the gateway's.
- `internal/runner/agentenv.go`'s `writeManagedSettings` (P5) takes the same
  `*agent.CompressionProxy` (via a new field on `run`, set by Task 4) and,
  when non-nil, overwrites the `ANTHROPIC_BASE_URL` entry in the `vars` map
  `GatewayVars` returned, before the call to `agent.WriteManagedSettings` —
  the same single write, not a second one.
- `checkSettingsRouting` itself is **unchanged** (P6): its existing
  `if r.gatewayOn()` call site already covers every compression-eligible
  run once Task 1b's gateway-required rule is in place. This task adds the
  test that proves it, not a code change.

**Tests:**
```go
// internal/agent/env_test.go

func TestBuildEnvUnchangedWithNoCompression(t *testing.T) {
	// spec.Compression nil: BuildEnv's output is byte-identical to today's
	// existing gateway-only test cases (golden comparison against the
	// existing TestBuildEnv* fixtures, not a new assertion written from
	// scratch -- the point is zero behavior change).
}

func TestBuildEnvRefusesCompressionWithOAuth(t *testing.T) {
	if _, _, err := BuildEnv(parentEnv, EnvSpec{Auth: "oauth", Compression: &CompressionProxy{BaseURL: "http://127.0.0.1:1"}}); err == nil {
		t.Fatal("oauth + compression was accepted")
	}
}

func TestBuildEnvRefusesCompressionWithVertex(t *testing.T) {
	if _, _, err := BuildEnv(parentEnv, EnvSpec{Auth: "vertex", Compression: &CompressionProxy{BaseURL: "http://127.0.0.1:1"}}); err == nil {
		t.Fatal("vertex + compression was accepted")
	}
}

func TestBuildEnvRefusesCompressionWithoutGateway(t *testing.T) {
	if _, _, err := BuildEnv(parentEnv, EnvSpec{Auth: "api-key", Compression: &CompressionProxy{BaseURL: "http://127.0.0.1:1"}}); err == nil {
		t.Fatal("compression without a gateway was accepted")
	}
}

func TestBuildEnvRoutesThroughCompressionNotGateway(t *testing.T) {
	env, _, err := BuildEnv(parentEnv, EnvSpec{
		Auth: "api-key", Gateway: &Gateway{BaseURL: "http://127.0.0.1:9000", Token: "gwtok"},
		Compression: &CompressionProxy{BaseURL: "http://127.0.0.1:9100"},
	})
	if err != nil {
		t.Fatalf("BuildEnv: %v", err)
	}
	if !slices.Contains(env, "ANTHROPIC_BASE_URL=http://127.0.0.1:9100") {
		t.Fatalf("ANTHROPIC_BASE_URL did not point at the sidecar: %v", env)
	}
}
```

```go
// internal/runner/agentenv_test.go

func TestWriteManagedSettingsUnchangedWithNoCompression(t *testing.T) {
	// r.compression nil: the written managed-settings JSON is
	// byte-identical to today's gateway-only golden case.
}

func TestWriteManagedSettingsCarriesCompressionURL(t *testing.T) {
	// r.compression set alongside a gateway: the written JSON's env.ANTHROPIC_BASE_URL
	// is the sidecar's URL, never the gateway's -- the exact gap design
	// §1.1a found. Read back the file WriteManagedSettings wrote and
	// unmarshal it, rather than asserting on the in-memory map alone, so a
	// bug in the write path itself (not only the value computed) would
	// also be caught.
}

func TestCheckSettingsRoutingAppliesWithCompression(t *testing.T) {
	// A repository settings file setting ANTHROPIC_BASE_URL, with
	// compression on (and therefore, per P9, the gateway also on):
	// checkSettingsRouting refuses it, identically to the existing
	// gateway-only case -- proving P6's "already covered" claim rather
	// than assuming the existing `if r.gatewayOn()` condition suffices.
}
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestBuildEnv' ./internal/agent/` and
`go test -count=1 -run 'TestWriteManagedSettings|TestCheckSettingsRouting' ./internal/runner/`
Expected: FAIL (new cases; existing cases in the same pattern must still
pass before this task's edits, confirming no regression from the test-file
edit alone).

- [ ] **Step 3: Write the implementation**
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/agent/... ./internal/runner/... && go test -race -count=1 ./internal/agent/... ./internal/runner/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/env.go internal/agent/env_test.go internal/runner/agentenv.go internal/runner/agentenv_test.go
git commit -m "headroom compression task 3: EnvSpec and the managed settings file both chain the sidecar in"
```

---

### Task 4: the runner starts, supervises and fails closed on the sidecar; lifts Task 1's refusal

**Flagged for a security-focused review (high effort, Opus):** touches
`internal/runner`'s status semantics, and this is the task that finally
makes `agent.compression` usable end to end.

**Files:**
- Edit: `internal/runner/gateway.go` (or a new `internal/runner/compression.go`
  — the implementer's choice, following whichever keeps `startGateway`
  readable), `internal/runner/runner.go`, `internal/runner/halt.go`, their
  tests
- Edit: `internal/config/validate.go` (lifts Task 1's reserved-and-refused
  rule for `"headroom"` specifically — P11)

**Interfaces:**
- A new `(r *run) startCompression(ctx context.Context) error`, called once
  per run (P4), **after `startGateway` has returned successfully and before
  the first stage**, only when `r.cfg.Agent.Compression != ""` (which, by
  Task 1b's resolved-config refusal, only ever fires when the gateway is
  already on — so this ordering is always satisfiable, never a race). It
  reads the gateway's own `BaseURL`/`Token` from `r.gwAgent` (already set by
  `startGateway`, `internal/runner/gateway.go:105-120`) and builds
  `headroomproc.Options.Env` explicitly (P7 — never `r.d.Env`, never any
  form of "inherit") to carry them into Headroom's own upstream
  configuration — this is the one, deliberate exception to "never pass the
  runner's environment through": the gateway's URL and token are exactly
  what Headroom needs to know where to forward, and nothing else from the
  runner's environment is included. It resolves the pinned binary's path
  (from `/etc/fugaro/base.json`'s pins, the same place Claude Code's own
  version is recorded, `docs/design/base-image.md` §3), calls
  `headroomproc.Start`, and on success sets
  `r.compression = &agent.CompressionProxy{BaseURL: p.URL()}` for
  `agent.BuildEnv` (Task 3) and `writeManagedSettings` (Task 3) to pick up
  as *Claude Code's* upstream — a different URL from the one just given to
  Headroom, which is intentional: Claude Code talks to the sidecar, the
  sidecar talks to the gateway.
- `r.compression`'s sidecar `Dead()` channel (closed on crash **or** a
  detected hang, P8) is selected alongside the gateway's `Halted()` channel
  wherever stages already watch for a halt
  (`internal/runner/gateway.go:215-248`'s pattern) — on either, the run ends
  `runstore.StatusInfraError` with a plain `Reason` string naming the
  sidecar (P10), **never** a new `HaltReason` and never `StatusHalted`.
- `r.endRun`/wherever the sidecar's `Stop` is guaranteed even on a panicking
  stage — the implementer follows whatever defer/cleanup pattern
  `r.gw.Close()`'s own call site already uses, so a leaked sidecar process
  is exactly as impossible as a leaked gateway goroutine is today.
- `config.Validate`'s reserved-and-refused rule for `agent.compression:
  headroom` (Task 1, P11) is changed, in this task's commit, to accept the
  value's *shape* only (the oauth/vertex/gateway refusal stays in Task 1b's
  resolved-config check, unaffected).

**Tests:**
```go
func TestRunnerStartsNoSidecarByDefault(t *testing.T) {
	// agent.compression unset: assert no headroomproc.Start call (inject a
	// fake constructor/interface the runner calls through, the same
	// dependency-injection shape r.d.* fields already use for the gateway
	// and Vertex token source).
}

func TestRunnerBuildsANarrowSidecarEnv(t *testing.T) {
	// agent.compression set: assert the Env passed to the fake
	// headroomproc.Start contains none of r.d.Env's credential-shaped
	// entries (the GitHub token, any provider key) -- a regression test for
	// P7, not just a behavioral one.
}

func TestRunnerFailsStageOnSidecarDeath(t *testing.T) {
	// agent.compression set, the fake sidecar's Dead() channel closes
	// mid-stage (simulating a crash): assert the run ends
	// runstore.StatusInfraError, the fake backend never sees a retried
	// call, and no later BuildEnv call falls back to Compression: nil.
}

func TestRunnerFailsStageOnSidecarHang(t *testing.T) {
	// Same assertion, but Dead() closes because of a simulated liveness
	// failure rather than a process exit -- proving the runner treats both
	// the same way (P8, P10), not only an exit.
}

func TestRunnerStopsSidecarOnRunEnd(t *testing.T) {
	// Normal run completion: assert Stop was called exactly once, even
	// when the run ends early (a halt, a cancelled context).
}
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestRunnerStartsNoSidecar|TestRunnerBuildsANarrowSidecarEnv|TestRunnerFailsStageOnSidecarDeath|TestRunnerFailsStageOnSidecarHang|TestRunnerStopsSidecarOnRunEnd' ./internal/runner/`
Expected: FAIL.

- [ ] **Step 3: Write the implementation**
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/runner/... ./internal/config/... && go test -race -count=1 ./internal/runner/... && go test -count=1 ./internal/config/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/runner/ internal/config/validate.go
git commit -m "headroom compression task 4: the runner starts, supervises and fails closed on the sidecar"
```

---

### Task 5: `Cost`'s cache read/write split, and `fugaro diagnose`

**Files:**
- Edit: `internal/runstore/cost.go`, `cost_test.go`
- Edit: `schemas/result.schema.json` (new, `omitempty` fields need a schema
  entry or `TestResultSchemaAcceptsRunnerRecords` fails)
- Edit: `internal/cli/diagnose.go`, `diagnose_test.go`

**Interfaces:**
- `Cost` gains, independent of G20 (P12):
  ```go
  // CacheReadUSD and CacheWriteUSD are the run's prompt-cache read and
  // write spend, summed from the gateway's per-call pricing
  // (internal/gateway/sse.go's cache_read_input_tokens/
  // cache_creation_input_tokens parsing). They exist to answer design
  // §3's prompt-caching risk (a compressed session's cache prefix can
  // change, turning cheap reads into dearer writes) independent of
  // whether G20's per-stage model_usd has landed -- they answer "did the
  // cache behave as expected," not "what did a stage cost."
  CacheReadUSD  float64 `json:"cache_read_usd,omitempty"`
  CacheWriteUSD float64 `json:"cache_write_usd,omitempty"`
  ```
- `fugaro diagnose` gains a `Cache: $X read, $Y write` line, printed only
  when either is non-zero — unchanged output for every run that doesn't
  exercise this path (gateway off, or a gateway run with no cache hits at
  all).
- **Not added, and this task's commit says so plainly rather than inventing
  a source:** any Headroom-specific before/after token or dollar field.
  No task in this plan reads Headroom's own stats API (none is confirmed to
  exist), so there is no source for such a field; design §3 drops the
  earlier draft's `TokensBefore`/`TokensAfter` proposal for the same reason.
  The `Compression:` line design §3 originally imagined is deferred until
  either G20 lands (giving a per-stage dollar figure to show) or a future
  task finds a real Headroom-side source worth naming.

**Tests:**
```go
func TestCostCacheFieldsRoundTrip(t *testing.T) { /* marshal/unmarshal, omitempty when zero */ }

func TestDiagnoseShowsCacheLineOnlyWhenNonZero(t *testing.T) {
	// A record with both fields zero: diagnose output unchanged (golden).
	// A record with either non-zero: diagnose gains exactly one new line,
	// asserted by substring, not full-output equality.
}

func TestResultSchemaAcceptsCacheFields(t *testing.T) {
	// A result.json fixture with cache_read_usd/cache_write_usd set
	// validates against schemas/result.schema.json -- pinned separately
	// from the existing TestResultSchemaAcceptsRunnerRecords so a future
	// schema edit that tightens `cost`'s additionalProperties doesn't
	// silently re-break this.
}
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestCostCacheFields|TestDiagnoseShowsCacheLine|TestResultSchemaAcceptsCacheFields' ./internal/runstore/... ./internal/cli/ ./schemas/`
Expected: FAIL.

- [ ] **Step 3: Write the implementation**
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/runstore/... ./internal/cli/... && go test -count=1 -run 'TestCostCacheFields|TestDiagnoseShowsCacheLine|TestResultSchemaAcceptsCacheFields' ./internal/runstore/... ./internal/cli/ ./schemas/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/runstore/cost.go internal/runstore/cost_test.go schemas/result.schema.json internal/cli/diagnose.go internal/cli/diagnose_test.go
git commit -m "headroom compression task 5: Cost's cache read/write split, for the design's prompt-caching risk"
```

---

### Task 6: `fugaro run --no-compression`, `task.Spec`, and the image gate

**Files:**
- Edit: `internal/task/task.go`, `task_test.go`
- Edit: `schemas/task.schema.json`
- Edit: `internal/cli/run.go`, `run_test.go`

**Interfaces:**
```go
// internal/task/task.go, next to OAuthToken:

// CompressionOff, when true, disables agent.compression for this run only,
// overriding fugaro.yaml (docs/design/headroom-compression.md §4's
// deliberately one-directional override: there is no field or flag to
// force compression *on*, only off). A runner before compressionSince
// refuses the field, since this package's decoder disallows unknown
// fields.
CompressionOff bool `json:"compression_off,omitempty"`
```

```go
const compressionSince = "0.8.0" // confirm against docs/release.md when this task starts
```

- `fugaro run` gains **one** new flag, `--no-compression` (bool, default
  false) — no `--compression` flag exists (Global Constraints). Setting it
  sets `spec.CompressionOff = true`; a follow-up inherits the previous run's
  `CompressionOff` the same way `OAuthToken` is inherited
  (`named-oauth-tokens.md` §6b point 2), and `--no-compression` on a
  follow-up additionally sets it (turning compression off is never refused,
  the same one-directional reasoning as above — only turning it *back on*
  via a follow-up flag would need the reviewed-`fugaro.yaml` guarantee this
  design protects).
- A `compressionSince` version-skew gate (mirroring `namedTokensSince`,
  `named-oauth-tokens.md` Task 6) refuses launching a run whose resolved
  `agent.compression` is non-empty **and** not already overridden off,
  against a build record older than the feature — on every launch path
  (first run, follow-up, `--workflow`, `--batch`), the same four-way test
  matrix `named-oauth-tokens.md` Task 6 wrote for its own gate.

**Tests:**
```go
func TestTaskSpecCompressionOffRoundTrips(t *testing.T) { /* marshal/unmarshal, omitempty when false */ }

func TestOldTaskParserRefusesCompressionOffField(t *testing.T) {
	// The same shape as TestOldRunnerRefusesOAuthTokenField: a pre-feature
	// decoder (a struct copy of Spec minus CompressionOff) refuses a
	// task.json carrying compression_off, strictly.
}

func TestRunNoCompressionSetsSpecField(t *testing.T) {
	// fugaro.yaml sets agent.compression: headroom; --no-compression on the
	// launch: assert spec.CompressionOff is true and the launch is never
	// refused by the image gate just because the repository's config would
	// otherwise want compression on.
}

func TestNoCompressionFlagExistsButNoForceOnFlagDoes(t *testing.T) {
	// Asserts the cobra command's flag set has --no-compression and does
	// NOT have --compression -- a structural test that would fail if a
	// future edit added a force-on flag the design explicitly rules out.
}

func TestCompressionImageGateRefusesOldBuild(t *testing.T) {
	// Mirrors named-oauth-tokens' own four cases (first run, follow-up,
	// --workflow, --batch), adapted to compressionSince.
}

func TestCompressionImageGateSkippedWhenOverriddenOff(t *testing.T) {
	// agent.compression: headroom in fugaro.yaml, but --no-compression on
	// the launch, against an old build record: the gate does not fire,
	// since the run will not actually use compression -- mirrors
	// named-oauth-tokens' TestRunTokenImageGateSkippedForDefaultAccount.
}
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestTaskSpecCompressionOff|TestOldTaskParserRefusesCompressionOffField' ./internal/task/` and
`go test -count=1 -run 'TestRunNoCompression|TestNoCompressionFlagExistsButNoForceOnFlagDoes|TestCompressionImageGateRefusesOldBuild|TestCompressionImageGateSkippedWhenOverriddenOff' ./internal/cli/`
Expected: FAIL.

- [ ] **Step 3: Write the implementation**
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/task/... ./internal/cli/... && go test -race -count=1 ./internal/task/... && go test -race -count=1 -run 'TestRunNoCompression|TestNoCompressionFlagExistsButNoForceOnFlagDoes|TestCompressionImageGateRefusesOldBuild|TestCompressionImageGateSkippedWhenOverriddenOff' ./internal/cli/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/task/task.go internal/task/task_test.go schemas/task.schema.json internal/cli/run.go internal/cli/run_test.go
git commit -m "headroom compression task 6: task.Spec.CompressionOff, --no-compression, the image gate"
```

---

### Task 7: the base image pins Headroom and proves a real compression pass needs no network

**Flagged for a security-focused review (high effort, Opus):** a new
third-party binary added to the base image with network-adjacent trust.

**A Fugaro run can write and unit-test the Go/TSV/Dockerfile-template bits
of this task but cannot build or scan an image** (Cloud Run has no Docker
daemon, the same limit `docs/design/base-image.md` §19 states for its own
Group 1) — CI's `images` and `docker-tests` jobs and the owner verify the
actual build and the no-egress test below.

**Files:**
- Edit: `images/base/Dockerfile` (the pin: `ARG HEADROOM_VERSION=…`, `ARG
  HEADROOM_SHA256_AMD64=…`/`_ARM64=…` — or a note that no arm64 artifact
  exists yet, per design §5, in which case Task 1b's resolved-config check
  also refuses `agent.compression` on an arm64 job, a small addition to
  that task's logic this task's commit should cross-reference)
- Edit: `images/base/tools.tsv` (a `harness`-tier presence row — **not**
  `kit`; design §5 placed Headroom in layer 3 with the other harnesses, and
  the tier should match)
- **Not edited: `images/base/bump.sh`.** It does not exist in this
  repository yet (`images/base/Dockerfile:10` only comments that it will
  rewrite pins; `docs/base-image.md:173` names it as planned, not built).
  An earlier draft of this plan assumed it existed and asked this task to
  edit it. Until it is built (a separate, much larger piece of work — the
  whole monthly-bump mechanism, not something this one pin should trigger),
  resolving Headroom's pin is a manual `ARG` edit, the same as every other
  pin was before `bump.sh` existed. If `bump.sh` is built before this task
  starts, add Headroom's entry to it as a small, separate addition; this
  task does not build the mechanism as a side effect.
- Edit/new: a CI docker test (alongside the existing `docker-tests` job,
  not `fugaro image selftest`'s presence-check smoke, which only proves the
  binary answers `--version`/a health path, design §2/§5) that runs the
  real pinned binary through an actual request/response pair under
  `--network none` and asserts it completes — the test this design's
  no-outbound-network claim actually rests on.

- [ ] **Step 1: Resolve the actual pinned artifact** (the implementer reads
  Headroom's own release page at this point — not before, since design §5's
  whole premise is that this is unverified today — and records the exact
  binary name, version and per-architecture sha256; if no standalone
  native binary is published and running Headroom means a Python runtime,
  the implementer stops and reports this back rather than proceeding, per
  design §2's "materially heavier than any other piece of this design"
  warning)
- [ ] **Step 2: Write the Dockerfile and tools.tsv changes**
- [ ] **Step 3: Write the no-egress CI docker test**
- [ ] **Step 4: Local verification**

Run (foreground): `fugaro image build --local` on the base image kind,
`fugaro image selftest` against the result, as `docs/design/base-image.md`'s
own dogfood checklist (§15) already prescribes for new harness entries; then
the new no-egress docker test, locally, under `--network none`.
Expected: the new tool's presence row passes; the no-egress test passes
(or, if it does not, this task stops and reports the design's "do not ship
it" outcome rather than working around the finding); size budget
(`images/size-budget.sh`) stays under its warning threshold, or the task's
commit message says explicitly that it does not and by how much.

- [ ] **Step 5: Commit**

```bash
git add images/base/Dockerfile images/base/tools.tsv images/
git commit -m "headroom compression task 7: pin Headroom in the base image, prove a real pass needs no network"
```

---

### Task 8: SECURITY.md, the user doc, skills, and docs tests

**Files:**
- Edit: `SECURITY.md` — one new bullet stating plainly that with
  `agent.compression` on, Headroom sees every model call's full body
  (including the `api-key`-gateway's per-run token, never the real key, per
  P9) before Anthropic does, runs in the same container and user as the
  agent with its own narrow environment (P7), and is *tested*, not
  *enforced*, to need no outbound network (design §2 — the wording must not
  overclaim an enforced guarantee Cloud Run cannot actually provide).
- New: `docs/compression.md`, covering `agent.compression`, the
  `--no-compression` flag (and the deliberate absence of a force-on flag),
  the `api-key`-with-gateway-on restriction, the `oauth`/`vertex` refusals,
  and a pointer to the design doc for the full threat model.
- Edit: `plugin/skills/routing/SKILL.md` (the implementer confirms this is
  the right skill, following the precedent `generic-tool.md` §4's "Choosing
  a recipe" section set for the same skill, rather than a new one) with a
  short note that compression exists, is off by default, and only applies
  to `api-key` workflows with the gateway on.
- New/edit: a docs test in the `internal/cli/docs_*_test.go` family
  confirming `docs/compression.md`'s commands and keys are real, the same
  pattern every other feature's docs test already follows.

- [ ] **Step 1: Write the docs edits**
- [ ] **Step 2: Write/extend the docs test**
- [ ] **Step 3: Run the docs tests**

Run: `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m`
Expected: `ok`.

- [ ] **Step 4: Commit**

```bash
git add SECURITY.md docs/compression.md plugin/skills/routing/SKILL.md internal/cli/docs_*_test.go
git commit -m "headroom compression task 8: SECURITY.md, the user doc, a routing-skill mention, docs tests"
```

---

### Task 9: `docs/release.md`, full suite and release-notes placement

**Files:** `docs/release.md` (the "Before you tag" checklist)

- [ ] **Step 1: Add the `compressionSince` line**

`docs/release.md`'s "Before you tag" checklist (`docs/release.md:49` on)
gains a line for `compressionSince`, the same shape as its existing
`namedTokensSince` line (`docs/release.md:58`): naming the constant's
location (`internal/cli/run.go`, next to where Task 6 defines it, or
wherever the implementer's grep for `namedTokensSince`/`recipesSince`
finds the convention actually lives) and the launch paths it gates
(first run, follow-up, `--workflow`, `--batch`).

- [ ] **Step 2: Full focused suite**

Run (foreground, split by package if any call would exceed 9 minutes):

```bash
go vet ./...
gofmt -l . # expect no output
go test -race -count=1 ./internal/config/... ./internal/agent/... ./internal/headroomproc/... ./internal/runner/... ./internal/runstore/... ./internal/task/... ./internal/cli/...
go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m
```

Expected: `ok` everywhere, no `gofmt` output.

- [ ] **Step 3: Release-notes task group**

Add this feature's section to whichever `docs/releases/vX.Y.Z.md` is open
for 0.8.0 (create it if nothing has yet, following `docs/releases/v0.6.5.md`'s
shape), naming `compressionSince`, the `api-key`-with-gateway-on
restriction, the `oauth`/`vertex` refusals, the opt-in default, and Task
10's live checks as "verified before this was ever enabled on a real run"
— not as a claim that compression is recommended, since §6's thresholds are
a separate, owner-run judgment call this task does not itself clear.

- [ ] **Step 4: Commit**

```bash
git add docs/release.md docs/releases/
git commit -m "headroom compression task 9: docs/release.md's compressionSince line, full suite, release-notes placement"
```

---

### Task 10: owner-run live checks (not part of either PR; sandbox only)

Never run by an implementer or by CI. Matches this repository's existing
`docs/gcp-live-checklist.md` pattern (guardrails: `FUGARO_LIVE_GCP_PROJECT`/
`FUGARO_LIVE_REPO` only, ADC only, cleanup registered before any side
effect, `FACT:` lines). Add these as new, numbered checks continuing
`docs/gcp-live-checklist.md`'s existing sequence (the implementer reads the
file's current last check number when this task starts and numbers from
there; this plan uses placeholder numbers **Check H1-H3** below rather than
guess a number that will have drifted by the time this task runs).
**Reordering note:** an earlier draft of this plan listed the §6 A/B
measurement as "item 3," implying it could run alongside items 1-2, before
any code merged. It cannot: §6 needs a merged PR 2 and a built image to run
against. The order below is corrected to reflect that.

- [ ] **Check H1. `ANTHROPIC_BASE_URL` with `CLAUDE_CODE_OAUTH_TOKEN`.**
  (Can run any time; does not depend on this plan's code.) On a machine
  with a real `claude setup-token` value, run `claude -p` with both
  `ANTHROPIC_BASE_URL` pointed at a local, logging reverse proxy (a small
  script is enough — no Headroom binary needed) and `CLAUDE_CODE_OAUTH_TOKEN`
  set. **FACT:** does Claude Code's traffic reach the proxy at all; does the
  proxy see the bearer token in a request header. This settles design
  §1.1's open question and is a precondition for ever reconsidering the
  `oauth` refusal (Task 1b) — it does not, by itself, lift it; lifting it
  is a future design's decision, not this check's.
- [ ] **Check H2. A real compression pass needs no network.** (Depends on
  Task 7's pin.) With the real, pinned Headroom binary started with no
  route to anything but loopback, run an ordinary sandbox task with
  `agent.compression: headroom` (Task 1b's refusal lifted by Task 4's own
  merge, so this needs PR 2 merged first) and confirm it completes
  normally. **FACT:** pass/fail, and if fail, exactly what the sidecar tried
  to reach. A failure here means the design's stated "do not ship it"
  outcome (design §2), not a configuration problem to work around — the
  check does not proceed to Check H3 if it fails.
- [ ] **Check H3. The §6 measurement protocol.** (Depends on PR 2 merged, a
  built image carrying the pinned sidecar, and Check H2 passing.) The
  paired A/B run over the owner's chosen task corpus, in the sandbox, on an
  `agent.auth: api-key` repository with `budget.mode: observe`, with the
  design's judgment-call thresholds applied by hand to the results —
  **not** a statistical test (design §6's own correction). **FACT lines:**
  per pair, the dollar cost, the cache read/write split, the review-round
  count, the pass/fail outcome, and the wall time, for both arms; a summary
  line stating whether the owner judges the 20%-dollar-reduction target met.

**Restore.** Nothing persists beyond the sandbox's own runs and branches,
cleaned up the same way every other check in `docs/gcp-live-checklist.md`
already requires (declined PRs, deleted branches, no change to any shared
installation).
