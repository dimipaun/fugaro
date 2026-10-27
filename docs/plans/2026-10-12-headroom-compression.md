# Headroom compression: implementation plan

**Goal:** Let a repository opt into routing its Claude Code model calls
through Headroom, a third-party context-compression proxy, to cut token
cost (`agent.compression` is a repository-wide field, not a per-workflow
one), for `agent.auth: api-key` workflows with the budget gateway already
on, without weakening the budget gateway, the agent's credential allowlist,
the managed settings file's routing protection, or any loop (review, verify,
gate) that depends on reading something exactly. **v1 does nothing for
usage-limit stalls**: it refuses `oauth` (and `vertex`) outright — see the
design's own summary. No code in this plan has been written or run. **This
PR (the one that adds this plan and its design doc) is docs-only: no file
under this plan's "Files" lists is touched by it.** Tasks 1 onward are
future work, gated on the owner approving the design and, independently, on
the checks of Task 11 before the feature's refusal is ever lifted (Task 10)
— not only on approval of the plan's shape.

**Spec:** [docs/design/headroom-compression.md](../design/headroom-compression.md).
Its decisions H1 (integration surface: a chained, standalone sidecar,
requiring the gateway, refused with `auth: oauth` or `auth: vertex` until
their respective live checks settle them) and H2 (fail closed, not fall back
to direct) are settled there; this plan's own decisions continue the same
letter, P1 onward, each independently vetoable. **A code review of this
plan's first draft found nine gaps**, several of them architecture-level
rather than wording (the managed settings file, the real-key exposure
without the gateway, Vertex's different routing variables, the bypass list
having no enforcement point, a merge/release gate contradiction, a silent
no-op config key, a mischaracterized fail-closed rationale, several
scope/schema omissions, and some invented test placeholders); this revision
folds every one of them in, and the design doc carries the same fixes.
Where this plan now differs from
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
  loopback URL, which does not exist until `gateway.Start`
  (`internal/gateway/gateway.go:192-230`) returns it inside
  `startGateway` (the URL alone, not the token too — Task 4's own entry
  below explains why Headroom needs no separate copy of it). An earlier
  draft of this plan had `startCompression`
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
  allowlist the launcher builds — never the runner's `os.Environ()` — and
  its own `ANTHROPIC_API_KEY`, not a copy of the gateway's token, is what
  carries budget auth through it.** Mirrors `agent.Claude.Run`'s own
  refusal of a nil `Env` (`internal/agent/agent.go:101-108`):
  `headroomproc.Start` takes an explicit `Env []string` (never defaults to
  inherit), and the runner's caller (Task 4) builds it from nothing but
  Headroom's own upstream URL — **not the gateway's token**, which an
  earlier draft of this plan also passed in and did not need to: Claude
  Code's own `ANTHROPIC_API_KEY` is already the gateway's per-run token
  (`GatewayVars`'s api-key case, unchanged by compression), sent as the
  request's own auth header on every call Claude Code makes to whatever
  `ANTHROPIC_BASE_URL` names; an ordinary reverse proxy forwards that header
  unchanged to its own configured upstream, so the gateway's token already
  arrives at the gateway without Headroom ever being separately told what it
  is (UNVERIFIED only in that it assumes Headroom forwards headers
  transparently, which Task 7's live reading of its actual behavior
  confirms or not). Nothing else from the runner's environment is included
  — never the GitHub App key, the real model/provider keys, or anything
  `ADC`-shaped. **The sidecar's stdout/stderr are kept as a small, bounded,
  redacted tail, not discarded outright** — an earlier draft of this
  decision said "discard entirely," which conflicts with how the sidecar's
  chosen address or a startup failure reason would ever reach the runner at
  all (§2's "port invites impersonation" entry already assumes the runner
  can read *something* from the process to confirm or diagnose it); the
  tail is redacted the same way `internal/logtail` already bounds and clips
  verify output elsewhere in this codebase, used only to produce a
  diagnosable reason on failure, and is never relayed live to Cloud Logging.
  *Veto alternative:* relay the sidecar's logs through the existing
  redaction pipeline (`internal/agent/relay.go`'s `Redactor`); rejected for
  v1 because that pipeline is built and tested against Claude Code's own
  stream-json shape, not an arbitrary third-party binary's log format, and a
  redactor tuned for the wrong shape is a false sense of safety.
- **P8. A liveness probe, and a memory bound that is honestly not a cgroup.**
  `headroomproc.Process.Dead()` only catches the process exiting; a hang
  (the process stays up but stops answering) is invisible to it.
  `headroomproc` adds a periodic liveness probe (the same ephemeral-port,
  origin-proof check as P2, run on an interval, not only once at startup)
  with a bound after which a hang is treated exactly like a crash (P10).
  **A `cgroup` limit, which an earlier draft of this decision proposed "via
  the same process-group handle `procgroup` already manipulates," is not a
  real option and the earlier text conflated two unrelated Linux concepts**:
  a process group is a job-control mechanism for signal delivery, not a
  resource-accounting mechanism, and creating or writing to a cgroup needs
  privileges or a delegated subtree an unprivileged Cloud Run container user
  does not have. The realistic options, neither verified: an
  `RLIMIT_AS`/`prlimit` address-space limit set on the child before exec
  (unreliable for a Python or garbage-collected Rust runtime that reserves
  large virtual address ranges without committing them), or a memory flag
  Headroom's own configuration exposes (UNVERIFIED whether one exists).
  Task 2 picks between these once Task 7 has read the real binary's
  behavior; the liveness probe above is needed regardless, since neither
  memory option substitutes for detecting a hang. *Veto alternative:* no
  separate memory bound, relying on the container's own OOM killer;
  rejected because an OOM kill of the whole container is a worse failure (it
  can read as `infra_error` with no distinguishing reason, `docs/design/
  checkpoint-pushes.md`'s own worked example of exactly this) than a bound
  that lets the sidecar alone fail first, cleanly, into P10's path.
- **P9. `agent.compression`'s `agent.auth` refusal lives in `config.Validate`
  after all; the gateway-on requirement cannot live in `internal/config` at
  all and moves to the runner's own bootstrap.** An earlier draft of this
  plan put both checks in a single new "resolved-config" layer outside
  `Validate`, reasoning that `Validate` "only sees one file." That reasoning
  was wrong for the `agent.auth` half: `config.Resolve` decodes the merged
  map (repository over project layer) into one `Config` and only then calls
  `Validate(&c)` on it (`internal/config/resolve.go:143`) — even
  `config.Parse` is `Resolve(data, nil)`, so `Validate` is never handed raw,
  unmerged fields. `agent.compression` set with `agent.auth: oauth` or
  `vertex` is therefore an ordinary `Validate` rule (Task 1), the same
  layer as the shape check.
  **The budget-gateway question is different in kind, not merely in
  wiring.** `budget.mode`'s *effective* value is not a fact any
  `fugaro.yaml` resolution produces: it is the tightest of three layers —
  the installation's own ceiling (`FUGARO_BUDGET_MODE` in the job's
  environment), the default branch's `fugaro.yaml`, and the branch's own
  file — merged by `(r *run).resolvePolicy` at runner bootstrap, after the
  checkout is fetched (`internal/runner/policy.go:58-126`,
  `policy.Merge(ceiling, def, FileLayer(cfg))`). `config.Resolve` (and so
  `Validate`) never sees the ceiling or the default-branch file. **A check
  against `cfg.Budget.Mode` directly would be wrong in exactly the common
  case this feature targets**: an owner who turns the gateway on
  installation-wide via the ceiling, for a repository whose own
  `fugaro.yaml` sets no `budget:` block at all (`cfg.Budget == nil`, the
  normal shape). The authoritative check is `r.gatewayOn()`
  (`internal/runner/gateway.go:46-48`) called from `startCompression` (Task
  4), **after** `resolvePolicy` has run — the one place the real, merged
  answer exists. A CLI-side pre-launch check can at most be advisory (it can
  warn when the repository's own file plainly sets a non-`api-key` auth, but
  it cannot know the ceiling), and this plan does not add one, to avoid
  implying a confidence the CLI cannot have. *Veto alternative:* allow
  compression without the gateway, and instead document and test the
  real-key exposure as an accepted risk; rejected because the
  gateway-required rule is strictly safer and costs nothing a v1, api-key,
  opt-in feature needs to give up.
- **P10. A sidecar `Dead()` signal during a stage goes through the ordinary
  stage-failure path, always ending `failed`/`draft`; only a `startCompression`
  failure at bootstrap, before any stage runs, ends `infra_error`.** Three
  drafts of this decision, each correcting the last. The first invented
  `HaltCompressionDied` (`runstore.go:34`'s own comment on `StatusHalted` —
  `"halted" // a budget or token limit stopped the run` — rules this out: a
  sidecar dying is neither). The second replaced it with a split between
  "nothing happened yet" (`infra_error`) and "a PR already exists"
  (`failed`/`draft`), reasoning from `runner.go:306-315`'s `if
  r.rec.Status == runstore.StatusRunning` fallback. That split is also
  wrong, for a reason neither earlier draft noticed: `Dead()` can only ever
  fire *after* `startCompression` has already returned successfully — the
  sidecar is already running — which is only possible once the stage loop
  has already started. So a `Dead()`-reported failure is *never* a bootstrap
  failure; it is always a stage failure, and `Run`'s own doc comment says
  plainly that its returned error is non-nil "for an infra_error... or when
  writing the final record itself fails" and nothing else
  (`runner.go:265-271`) — a stage failure reported via `r.fail` instead
  reaches `finalize`, which commits an empty commit when the agent made none
  (`runner.go:1732-1736`) and always produces a PR, confirmed by
  `TestStageTimeoutOpensDraft` (`runner_test.go:362-377`): an implement-stage
  failure ends `StatusFailed`+`OutcomeDraft` with `Run` itself returning
  **`nil`**. **The fix: a sidecar `Dead()` signal, at any stage, is reported
  through `r.fail(reason)` (or whatever the stage already calls on an
  ordinary `agent.Claude.Run` error) and nothing more** — no bespoke status
  assignment, and no "which stage" branching: every `Dead()`-reported
  failure ends `StatusFailed`+`OutcomeDraft`, full stop. `StatusInfraError`
  is reserved for the one, different case: `startCompression` itself
  returning an error before the stage loop ever starts (the gateway check
  failing, or `headroomproc.Start` erroring) — genuinely "the run itself
  returns an error," the doc comment's own test. *Veto alternative:* a
  dedicated `Halt`/`HaltReason` value; rejected as a type error, not a style
  choice — `Halt` and its reasons are specifically about budget and token
  limits throughout the schema (`schemas/result.schema.json`'s closed
  `halt.reason` enum has no
  room for this, and would need an edit that doesn't belong next to budget
  semantics).
- **P11. `agent.compression` ships reserved-and-refused through Tasks 1-9,
  and the lift is its own separate, final task (Task 10) — not folded into
  the task that makes the sidecar real.** An earlier draft of this plan had
  Task 4 both wire up the runner and lift the refusal in the same change,
  which reintroduces exactly the silent-no-op risk this decision exists to
  prevent, just shifted later: that PR would make `agent.compression:
  headroom` usable by any real run the moment it merges, before Task 11's
  checks (the bypass-list capability question in particular) have run.
  **The schema moves with the refusal, not ahead of it**: while reserved,
  `schemas/fugaro.schema.json`'s enum for `agent.compression` admits only
  `""`, so the corpus fixture exercising `agent.compression: headroom` lives
  in `testdata/config/invalid/` through Tasks 1-9, rejected by both the
  schema and `Validate` for the same reason. Task 10 (alone) edits the
  schema's enum to admit `"headroom"`, edits `Validate`'s message away from
  "not yet implemented," and moves that one fixture to `valid/`. The
  pattern itself — a known key, strictly parsed, refused with a message
  naming it "not yet implemented" rather than an unknown-field error — is
  the same shape `docs/design/recipes.md` §4 already uses for `checks`,
  `goto`, `extends` and `docs/design/layered-config.md` §6 uses for a
  profile's `extends`. *Veto alternative:* ship Task 1 and the sidecar in
  one PR, so the key is never reserved without being usable; rejected
  because it was tried (an earlier draft) and produced exactly the
  lift-without-a-gate problem this decision now closes.
- **P12. The per-call cache read/write *token* totals are recorded per run,
  independent of G20 — not a dollar split, which would need new pricing
  code this plan does not propose writing.** Design §3's prompt-caching
  risk means §6's measurement needs to see cache behavior per arm. The
  gateway already computes the token counts per call
  (`internal/gateway/sse.go:144-169` feeds `pricing.Usage{CacheRead,
  CacheWrite5m, CacheWrite1h, ...}`) but never sums them into anything
  `result.json` keeps — summing and recording the *token* counts needs no
  new pricing logic. **An earlier draft of this decision proposed
  `CacheReadUSD`/`CacheWriteUSD` "summed from per-call pricing," which
  doesn't exist**: `Rates.Cost(u)` (`internal/pricing/pricing.go:311`)
  returns one blended total for a call's whole `Usage`, never a
  per-category breakdown, and `forward.go:759`'s `o.tokens = u.Input +
  u.CacheWrite5m + u.CacheWrite1h + u.CacheRead + u.Output` (which an
  earlier draft also cited as if it computed cost) is a plain token sum, not
  a cost figure. This plan adds `CacheReadTokens`/`CacheWriteTokens`
  (`int64`) to `Cost`, next to `ReportedUSD`/`Unreconciled`, and does not
  propose a dollar-level split.

## Global Constraints

Every task's requirements include these.

From the design:
- `agent.compression` defaults to off; no behavior changes for a repository
  that never sets it (design §4's "default off", mirrored exactly from
  `named-oauth-tokens.md`'s "An installation that adds no `claude_accounts`
  entry is unaffected, in full").
- `agent.compression` is refused outright (not merely a shape check) with
  `agent.auth: oauth` or `agent.auth: vertex` — an ordinary `config.Validate`
  rule, since `Validate` runs on the merged config already (P9) — and,
  separately, whenever the gateway turns out to be off, a check the runner's
  own bootstrap makes after `resolvePolicy`, since that is the only point
  where the real, merged budget mode exists (P9).
- `agent.compression` is **reserved and refused** (P11) through Tasks 1-9;
  no intermediate state exists where the key is accepted but does nothing,
  and the lift is Task 10 alone, gated on Task 11's checks.
- The sidecar never receives outbound network access by default; Task 7's
  CI docker test proves an actual compression pass completes under
  `--network none` (not only that the binary answers `--version` — design
  §2, §5; this is a **tested**, not an enforced, property, since an
  unprivileged Cloud Run container cannot itself block an outbound call the
  sidecar's own code decides to make).
  **(2026-10-27: no such test exists — Task 7's revised run pinned nothing,
  so the artifact this bullet says was tested is not in the image. Read the
  Task 7 revised note before treating this property as covered.)**
- The sidecar's own environment is an explicit allowlist, never the
  runner's inherited one (P7); its stdout/stderr are kept only as a small,
  bounded, redacted tail for diagnosing a startup or crash reason, never
  relayed live to Cloud Logging (P7).
- A sidecar crash or a detected hang **fails the stage/run closed, through
  the ordinary stage-failure path** (design H2, P8, P10) — no task adds a
  silent fallback to direct, uncompressed routing, and no task invents a new
  `Halt`/`HaltReason` value or hard-codes `StatusInfraError` for this.
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
   `TestValidateRefusesCompressionWithOAuth` and
   `TestValidateRefusesCompressionWithVertex` (Task 1, an ordinary
   `config.Validate` rule — P9 corrects an earlier draft's claim that this
   needed a separate resolved-config layer), plus
   `TestStartCompressionRefusesWithoutGateway` (Task 4, the runner's own
   bootstrap check, the *only* place the real, merged budget mode exists)
   and `TestBuildEnvRefusesCompressionWithOAuth`/
   `TestBuildEnvRefusesCompressionWithVertex`/
   `TestBuildEnvRefusesCompressionWithoutGateway` (Task 3, defense in depth
   at the point closest to the credential) — all six assert a hard error,
   never a silent skip.
3. **The managed settings file still routes around Headroom even though the
   environment is correct.** Pinned by
   `TestWriteManagedSettingsCarriesCompressionURL` (Task 3, P5): with
   compression on, asserts the written managed-settings JSON's
   `ANTHROPIC_BASE_URL` is the sidecar's URL, not the gateway's — the exact
   gap design §1.1a found, which no test in this plan's first draft would
   have caught (it only tested `BuildEnv`, never `writeManagedSettings`).
4. **A committed `.claude/settings.json` routes around Headroom.** Pinned
   by `TestCheckSettingsRoutingAppliesWithCompression` (Task 3, P6): a
   repository settings file setting `ANTHROPIC_BASE_URL` is refused
   identically whether `agent.compression` is on or off, proving P9's
   "already covered" claim rather than assuming it.
5. **A sidecar crash or hang during a stage is swallowed, retried, or wrongly
   discards work instead of ending `failed`/`draft`.** Pinned by
   `TestRunnerFailsStageOnSidecarDeath` and `TestRunnerFailsStageOnSidecarHang`
   (Task 4): both assert `Run` returns a nil error (the same shape
   `TestStageTimeoutOpensDraft` already pins for an ordinary stage failure)
   and the record is `StatusFailed`+`OutcomeDraft`, never a retry against the
   real API and never `StatusInfraError`+`OutcomeNone` — which would
   silently discard already-committed work, the mistake an earlier draft of
   this plan made by asserting `infra_error` for a mid-stage death (on the
   wrong premise that no PR existed yet; `Dead()` can only fire once the
   sidecar has already started, which is only possible once the stage loop
   is already running, so this case is never a bootstrap failure).
   Separately, `TestStartCompressionRefusesWithoutGateway` pins the one
   case that *does* end `StatusInfraError`: `startCompression` itself
   failing before any stage starts.
6. **The bypass list is asserted as settled when it isn't, or its go/no-go
   check never runs.** No task in this plan implements a bypass mechanism in
   Fugaro's own code (design §3: Claude Code talks to Headroom directly;
   Fugaro builds no request body in that path), so there is no
   `TestForwardNeverCompressesBypassedPaths`-shaped test here — testing it
   would test something Fugaro's code does not do. What *is* pinned: Task
   10's lift is gated on Task 11's Check L2 (the bypass-capability reading of
   Headroom's real configuration surface) having been recorded with a
   positive answer, not merely scheduled — an earlier draft of this plan had
   no check that actually gated anything here, despite §3/§4/§8 of the
   design implying one existed.
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
| `internal/config/config.go`, `validate.go`, `scope.go`, `schemas/fugaro.schema.json` (enum admits only `""`), `docs/project-layer.md`, `testdata/config/invalid/*` | `Agent.Compression` (reserved and refused, P11), the `agent.auth` refusal (an ordinary `Validate` rule, P9), the `Scopes` row and its printed copy (the fourth edit an earlier draft omitted), the corpus fixture (in `invalid/` until Task 10) | 1 |
| `internal/headroomproc/headroomproc.go` (new), `headroomproc_test.go` (new) | Process supervision: `Start`, origin-proof readiness probe, liveness probe, an `RLIMIT_AS`/`prlimit`-or-Headroom-flag memory bound (P8), `Stop`/process-group kill, `URL()` | 2 |
| `internal/agent/env.go`, `env_test.go` | `EnvSpec.Compression`, the chaining/refusal logic in `BuildEnv`/`GatewayVars` (P3, P7) | 3 |
| `internal/runner/agentenv.go`, `runner.go` (the new `run.compression` field), their tests | `writeManagedSettings` carries Headroom's URL, written **after** `GatewayVars` (P5); `checkSettingsRouting`'s existing condition proven to already cover compression (P6) | 3 |
| `internal/runner/gateway.go` (or a new `internal/runner/compression.go`), `runner.go`, their tests | Starting/supervising/stopping the sidecar per run after `startGateway` (P4); the gateway-on bootstrap check (P9); the ordinary stage-failure path on a crash or hang (P10, no new status) | 4 |
| `internal/runstore/cost.go`, `cost_test.go`, `schemas/result.schema.json`, `internal/cli/diagnose.go`, `diagnose_test.go` | `Cost.CacheReadTokens`/`CacheWriteTokens` (P12, independent of G20); the unconditional-on-nonzero `Cache: N read, M write (tokens)` diagnose line | 5 |
| `internal/task/task.go`, `task_test.go`, `schemas/task.schema.json` | `Spec.CompressionOff bool` (off-only, one-shot, strictly decoded) | 6 |
| `internal/cli/run.go`, `internal/cli/recipes_skew.go`, `run_test.go` | `--no-compression` (off-only; no force-on flag); `compressionSince`, defined in `recipes_skew.go` next to `namedTokensSince`, not `run.go` | 6 |
| `images/base/Dockerfile`, `images/base/tools.tsv`, the base image's selftest, a new CI docker test | The pin, the `harness`-tier presence row, the real no-egress compression-pass test (not only `--version`) | 7 |
| `SECURITY.md`, `docs/compression.md` (new), `plugin/skills/routing/SKILL.md` (or wherever the implementer finds the closest fit), `internal/cli/docs_*_test.go` (new case) | The trust-model bullet, the user doc (described as not-yet-usable until Task 10), skill mentions, docs tests | 8 |
| `docs/release.md` | A `compressionSince` line in "Before you tag", alongside `namedTokensSince`'s | 9 |
| none | Full suite, release-notes task group | 9 |
| `internal/config/validate.go`, `schemas/fugaro.schema.json` (enum gains `"headroom"`), `testdata/config/valid/*` (the fixture moved from `invalid/`), `docs/compression.md` | **The lift**: the only place the refusal is removed (P11) | 10 |
| `docs/gcp-live-checklist.md` (results pasted in) | The owner's own runs, as numbered checks | 11 |

## PR groups

**Three PRs, in order**, a correction to an earlier draft's two: **PR 1
(Tasks 1-2)** is additive and inert — a new, reserved-and-refused config key
(P11) nothing can actually turn on, and a standalone process-supervision
package nothing calls yet — safe to merge on its own, reviewable without
touching the credential path at all. **PR 2 (Tasks 3-9)** wires everything
up — the sidecar runs, the managed settings carry its URL, the runner's
bootstrap check is real — but `agent.compression` **stays refused** the
whole time; nothing in PR 2 lifts it. **PR 3 (Task 10 alone)** is the lift:
three small file edits (`validate.go`'s message, the schema's enum, one
fixture's directory), gated on Task 11's Check L2 (the bypass-list
capability reading) having a recorded, positive answer, and on Task 7's CI
no-egress test already being green (it runs as part of PR 2's own CI, so by
the time PR 2 has merged this gate is already satisfied or not — PR 3 simply
checks it was). **(2026-10-27: that test does not exist — Task 7's revised run
pinned nothing, so this gate cannot be satisfied as written; see the Task 7
revised note.)** **An earlier draft of this plan lifted the refusal inside
PR 2**, which is circular: it would make the feature usable by a real run
before any of these checks had run at all, since the measurement in
particular (Task 11's Check L4) needs the lift to have already happened to
run against. The three-PR split removes the circularity: PR 3 is gated on
things that can be true *before* it merges (a CI result, a documentation
reading), never on something that needs it to have merged first.

Parallelizable: Tasks 1 and 2 are independent and may be implemented
together. Task 3 needs 1 and 2. Task 4 needs 3. Task 5 is independent of 3/4
once 1 exists (it only adds record fields and a display line) but is most
useful once 4 actually populates them. Task 6 needs 1, 3 and 4. Task 7 needs
2 (to know what artifact is being pinned) but can start in parallel with
3-6 once the pin target is chosen. Task 8 needs 1-7 settled (it documents
the shipped shape, noting plainly that it isn't usable yet). Task 9 is last
in PR 2. Task 10 (PR 3) needs PR 2 merged and Task 11's Check L2 answered.
Task 11's checks are the owner's own, run whenever the owner chooses, except
Check L2 (must precede Task 10) and Checks L3/L4 (must follow it).

**Review flag.** Tasks 2, 3, 4, 6, 7 and 10 touch, respectively: a new
process given its own narrow credential surface and memory/network bounds,
the agent's credential-env allowlist (`internal/agent/env.go` and
`internal/runner/agentenv.go`, the files `SECURITY.md` already names as in
scope: "the runner's use of provider and model tokens"), the runner's status
semantics at a failure path every run goes through, a new strictly-decoded
task-spec field with a version gate (the same class of change
`named-oauth-tokens.md` flagged for review), the base image's supply-chain
pin (a new third-party binary with network-adjacent trust, the same class of
review `base-image.md`'s own harness additions get), and the one change
that actually turns the feature on for real runs. Each is flagged for review
at high effort (Opus) specifically for the security question — does this
change widen what the agent, or anything running alongside it, can reach or
read — separate from an ordinary correctness review of the same diff, the
same split `docs/plans/2026-10-08-layered-config.md:174` draws for its own
Task 4 and Task 13 ("the second with a security reviewer, since they are
the merge and the trust boundary").

---

### Task 1: `config.Agent.Compression`, reserved and refused, and the `agent.auth` rule

**Files:**
- Edit: `internal/config/config.go` (the `Agent` struct)
- Edit: `internal/config/validate.go`
- Edit: `internal/config/scope.go` (the `Scopes` table — the fourth edit an
  earlier draft of this plan omitted)
- Edit: `schemas/fugaro.schema.json` (`compression`'s enum admits **only
  `""`** while reserved — Task 10 is the only task that adds `"headroom"`)
- Edit: `docs/project-layer.md` (the printed copy of `Scopes`,
  `TestScopeTableMatchesDocs` keeps the two equal)
- New: `testdata/config/invalid/agent-compression-headroom.yaml` (refused by
  both the schema and `Validate` while reserved; Task 10 moves this exact
  file to `valid/`)
- Test: `internal/config/config_test.go` (there is no `validate_test.go` in
  this package; `TestAgentOAuthToken`, the existing test for the identical
  shape of question, lives in `config_test.go`, confirmed by reading it),
  `internal/config/scope_test.go`
  (or wherever `TestScopeTableMatchesDocs` lives — the implementer greps for
  it)

**Interfaces:**
```go
// internal/config/config.go, in Agent, next to OAuthToken:

// Compression names the context-compression proxy this repository's
// api-key, gateway-on workflows route their model calls through: "" (off,
// the default) or "headroom" (docs/design/headroom-compression.md).
// Reserved and refused (P11) until Task 10 lifts it; the refusal names
// "not yet implemented," not "unknown field," so a repository trying it
// gets a clear answer rather than a strict-parse error. Once implemented,
// also refused together with agent.auth: oauth or vertex -- an ordinary
// Validate rule (P9), since Resolve calls Validate on the merged config,
// not a raw single file. The budget-gateway requirement is a separate,
// runtime-only check (Task 4): it cannot live here, because no
// fugaro.yaml resolution knows the installation's budget ceiling.
// Binaries before compressionSince refuse this key outright.
Compression string `yaml:"compression,omitempty"`
```

```go
// internal/config/scope.go, next to agent.oauth_token's row:
{Key: "agent.compression", In: InRepo | InOverride, Why: "it changes what the model sees on every call; the project layer and a profile are the same launcher-writable bucket object agent.oauth_token already distrusts for the identical reason"},
```

- [ ] **Step 1: Write the failing tests**

```go
// internal/config/config_test.go, next to TestAgentOAuthToken (the existing
// test for the identical shape of question): built on the real
// config.Parse(data) ([]byte) (*Config, []Problem), not a cfg.Validate()
// method call -- Validate is a plain function, Validate(c *Config)
// []Problem (internal/config/validate.go:94), and no validConfigFixture
// helper exists in this package; TestAgentOAuthToken's own
// fmt.Sprintf(base, ...)-over-Parse idiom is the real, already-used pattern
// this task's own tests should follow instead.

func TestValidateCompressionReservedAndRefused(t *testing.T) {
	base := "version: 1\nproject: example\ngit: { provider: github, base_branch: main }\nagent:\n  auth: api-key\n%s" +
		"workflows:\n  app: { base: go, commands: { build: go build ./..., test: go test ./... } }\n"
	for _, val := range []string{"headroom", "anything"} {
		_, ps := Parse([]byte(fmt.Sprintf(base, "  compression: "+val+"\n")))
		if len(ps) == 0 {
			t.Fatalf("compression=%q was accepted before it is implemented", val)
		}
		if !slices.ContainsFunc(ps, func(p Problem) bool { return strings.Contains(p.Message, "not yet implemented") }) {
			t.Errorf("compression=%q: refusal message doesn't say why: %+v", val, ps)
		}
	}
	if cfg, ps := Parse([]byte(fmt.Sprintf(base, ""))); len(ps) > 0 || cfg.Agent.Compression != "" {
		t.Fatalf("unset compression was refused: %+v, %v", cfg, ps)
	}
}

// TestOldConfigParserRefusesCompressionKey proves the strict-decode
// version-skew claim against the Agent struct's *real* current fields
// (copied from config.go, not elided), decoding only the agent: fragment
// so the test needs no stand-in for Config's other, unrelated fields.
func TestOldConfigParserRefusesCompressionKey(t *testing.T) {
	type oldAgent struct {
		Auth            string     `yaml:"auth"`
		Model           string     `yaml:"model"`
		ReviewRounds    int        `yaml:"review_rounds"`
		MaxBudgetUSD    float64    `yaml:"max_budget_usd"`
		Instructions    string     `yaml:"instructions"`
		Review          string     `yaml:"review"`
		Models          ModelRoles `yaml:"models"`
		MaxOutputTokens RoleTokens `yaml:"max_output_tokens"`
		MaxRunTokens    int64      `yaml:"max_run_tokens"`
		FirstLineReview string     `yaml:"first_line_review"`
		FirstLineRounds int        `yaml:"first_line_rounds"`
		Recipe          string     `yaml:"recipe"`
		OAuthToken      string     `yaml:"oauth_token,omitempty"`
	}
	data := []byte("auth: api-key\ncompression: headroom\n")
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var old oldAgent
	if err := dec.Decode(&old); err == nil {
		t.Fatal("an Agent decoder shaped like config.go before this task accepted agent.compression silently")
	}
}

func TestValidateRefusesCompressionWithOAuth(t *testing.T) {
	base := "version: 1\nproject: example\ngit: { provider: github, base_branch: main }\nagent:\n  auth: oauth\n  compression: headroom\n" +
		"workflows:\n  app: { base: go, commands: { build: go build ./..., test: go test ./... } }\n"
	if _, ps := Parse([]byte(base)); len(ps) == 0 {
		t.Fatal("compression + oauth was accepted")
	}
	// (This case is also refused as "not yet implemented" until Task 10;
	// the implementer confirms both reasons appear, or scopes this test to
	// run again, unchanged, once Task 10 lifts the reservation.)
}

func TestValidateRefusesCompressionWithVertex(t *testing.T) {
	base := "version: 1\nproject: example\ngit: { provider: github, base_branch: main }\nagent:\n  auth: vertex\n  compression: headroom\n" +
		"workflows:\n  app: { base: go, commands: { build: go build ./..., test: go test ./... } }\n"
	if _, ps := Parse([]byte(base)); len(ps) == 0 {
		t.Fatal("compression + vertex was accepted")
	}
}
```

```go
// internal/config/scope_test.go

func TestScopesIncludesCompression(t *testing.T) {
	i := slices.IndexFunc(Scopes, func(r ScopeRow) bool { return r.Key == "agent.compression" })
	if i < 0 {
		t.Fatal("agent.compression has no Scopes row")
	}
	if Scopes[i].In != InRepo|InOverride {
		t.Errorf("agent.compression scope = %v, want InRepo|InOverride", Scopes[i].In)
	}
}
```

(The pre-existing, generic `TestScopeTableMatchesDocs` — unchanged by this
task — is what then catches a mismatch between `scope.go` and
`docs/project-layer.md`'s printed table; `TestScopesIncludesCompression`
only pins that the row itself, and its scope value, are correct.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestValidateCompression|TestOldConfigParserRefusesCompressionKey|TestValidateRefusesCompression|TestScopesIncludesCompression|TestScopeTableMatchesDocs' ./internal/config/`
Expected: FAIL, `undefined: Compression`, or `TestScopeTableMatchesDocs` failing on the missing row once the struct exists.

- [ ] **Step 3: Write the implementation** (the struct field, the
  reserved-and-refused `Validate` branch plus the `agent.auth: oauth`/
  `vertex` branch, the `scope.go` row, the schema's `""`-only enum,
  `docs/project-layer.md`'s row, the corpus fixture in `invalid/`)
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/config/... && go test -count=1 ./internal/config/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/validate.go internal/config/scope.go internal/config/config_test.go internal/config/scope_test.go schemas/fugaro.schema.json docs/project-layer.md testdata/config/invalid/agent-compression-headroom.yaml
git commit -m "headroom compression task 1: agent.compression, reserved and refused, and the agent.auth rule"
```

---

**A note on what Task 1 does *not* need, corrected from an earlier draft's
"Task 1b":** that draft added a second, separate "resolved-config" check
for the `agent.auth` refusal, reasoning `Validate` "only sees one file."
That reasoning was wrong (P9): `Resolve` calls `Validate` on the merged
config already, so Task 1's own `Validate` rule above is already correct
against a project-layer-sourced `agent.auth`, and needs no companion task.
The one piece that earlier draft got right — that the *budget-gateway*
question needs a check somewhere else — moves to Task 4 below, as a runtime
check, because `config.Resolve` can never see the installation's ceiling
(P9's full reasoning).

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
	MemoryLimitAS   int64         // RLIMIT_AS bytes for the child, via prlimit before exec; 0 means none (P8; UNVERIFIED reliability, see design §2)
	Grace           time.Duration // SIGTERM -> SIGKILL grace on Stop, mirroring procgroup's shape
	LogTail         *logtail.Writer // a bounded, caller-owned sink for a redacted stdout/stderr tail (P7); nil discards
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
// probes failed (a hang, P8). The runner (Task 4) reports either through
// the same, ordinary stage-failure path (P10) -- this package assigns no
// status of its own.
func (p *Process) Dead() <-chan struct{}

// Stop sends SIGTERM to the process group, escalating to SIGKILL after
// Grace, mirroring procgroup.Run's own shutdown shape.
func (p *Process) Stop(ctx context.Context) error

// checkOrigin is the readiness/liveness probe's own logic, factored out so
// it can be tested against an httptest.Server directly, with no process to
// spawn at all: it GETs path on base, and reports ready only if the
// response is 200 with the exact wantToken body.
func checkOrigin(ctx context.Context, client *http.Client, base, path, wantToken string) error
```

**Tests.** The fake sidecar is this test binary itself, re-executed with a
helper-process environment variable set — the same `TestMain` pattern
Go's own `os/exec` tests use, which avoids depending on `nc`, a shell
one-liner, or any external tool (an earlier draft's `nc` sketch admitted in
its own comment that it didn't work).

```go
// headroomproc_test.go

func TestMain(m *testing.M) {
	if mode := os.Getenv("HEADROOMPROC_HELPER"); mode != "" {
		runHelper(mode) // serves ReadyPath once ("ready"), exits 1 ("exit1"),
		// serves once then self-kills ("crash"), serves once then stops
		// accepting ("hang"), or allocates past a few MB in a loop ("balloon") --
		// reads HEADROOMPROC_TOKEN and HEADROOMPROC_ADDR for the token/port.
		return
	}
	os.Exit(m.Run())
}

func helper(t *testing.T, mode, token string) (bin string, env []string) {
	t.Helper()
	return os.Args[0], append(os.Environ(), "HEADROOMPROC_HELPER="+mode, "HEADROOMPROC_TOKEN="+token, "-test.run=TestMain")
}

func TestStartWaitsForReadyThenReturns(t *testing.T) {
	bin, env := helper(t, "ready", "tok1")
	p, err := Start(context.Background(), Options{Bin: bin, Env: env, ReadyPath: "/healthz", ReadyTimeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop(context.Background())
	if p.URL() == "" {
		t.Fatal("a ready Process has no URL")
	}
}

func TestStartFailsIfProcessExitsBeforeReady(t *testing.T) {
	bin, env := helper(t, "exit1", "tok1")
	if _, err := Start(context.Background(), Options{Bin: bin, Env: env, ReadyPath: "/healthz", ReadyTimeout: time.Second}); err == nil {
		t.Fatal("a binary that exits immediately was accepted as ready")
	}
}

// TestReadinessProbeRejectsImpersonator tests checkOrigin directly against
// an httptest.Server standing in for a port-squatter: no process is
// spawned, because the function under test is the origin check itself, not
// process supervision -- isolating this from the OS's own ephemeral-port
// allocation, which the test cannot control deterministically.
func TestReadinessProbeRejectsImpersonator(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "not-the-real-token")
	}))
	defer srv.Close()
	if err := checkOrigin(context.Background(), srv.Client(), srv.URL, "/healthz", "tok1"); err == nil {
		t.Fatal("an impersonating listener's wrong token was accepted as the real sidecar")
	}
}

func TestDeadClosesOnCrash(t *testing.T) {
	bin, env := helper(t, "crash", "tok1")
	p, err := Start(context.Background(), Options{Bin: bin, Env: env, ReadyPath: "/healthz", ReadyTimeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-p.Dead():
	case <-time.After(2 * time.Second):
		t.Fatal("Dead() never closed after the process crashed")
	}
}

func TestDeadClosesOnLivenessFailure(t *testing.T) {
	bin, env := helper(t, "hang", "tok1")
	p, err := Start(context.Background(), Options{Bin: bin, Env: env, ReadyPath: "/healthz", ReadyTimeout: 2 * time.Second, LivenessPeriod: 50 * time.Millisecond, LivenessMisses: 2})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer p.Stop(context.Background())
	select {
	case <-p.Dead():
	case <-time.After(2 * time.Second):
		t.Fatal("Dead() never closed for a sidecar that stopped answering without exiting")
	}
}

func TestStopKillsTheWholeProcessGroup(t *testing.T) {
	// Mirrors procgroup's own "leaked daemon" test shape and reuses its
	// technique directly (Step 3 below reuses procgroup's process-group
	// code, so this test can reuse procgroup_test.go's own grandchild-spawn
	// helper rather than writing a second one): the helper mode spawns a
	// detached grandchild that outlives a bare SIGTERM to the helper alone;
	// Stop must still reap it, checked the same way procgroup_test.go
	// checks it for claude (the grandchild's PID is gone after Stop returns).
}

func TestMemoryLimitKillsAnOverBudgetSidecar(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("RLIMIT_AS enforcement is Linux-specific; Cloud Run is Linux, so this still covers the real target")
	}
	bin, env := helper(t, "balloon", "tok1")
	p, err := Start(context.Background(), Options{Bin: bin, Env: env, ReadyPath: "/healthz", ReadyTimeout: 2 * time.Second, MemoryLimitAS: 32 << 20})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-p.Dead():
	case <-time.After(3 * time.Second):
		t.Fatal("an over-budget sidecar was not killed by MemoryLimitAS")
	}
	// Not a guarantee for every runtime (design §2: RLIMIT_AS is unreliable
	// for Python/GC'd Rust); this pins the mechanism for a plain allocator,
	// which is what the helper's "balloon" mode exercises.
}
```

(This design's explicit no-outbound-network requirement (design §2) is
**not** tested here against a fake binary — a fake process proves nothing
about what Headroom's real one would try to reach. That property is tested
once, against the real pinned binary, in Task 7's CI docker test — except
that, as of the Task 7 revised note of 2026-10-27, **no artifact was pinned
and no such test exists**, so this parenthetical currently points at nothing.)

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
- Edit: `internal/runner/runner.go` (the `run` struct gains `compression
  *agent.CompressionProxy`, declared **here**, next to the existing
  `gwAgent *agent.Gateway` field — not in Task 4, which an earlier draft of
  this plan assumed and which made the two tasks circular: Task 4 was
  listed as needing Task 3, but Task 3's own test set a field on `run` that
  the earlier draft said Task 4 added. This task declares the field, and
  also adds the one test-only `Deps` field this task's own tests need to
  set it directly, with no real sidecar behind it — see **`Deps.CompressionOverride`**
  below. Task 4 is the only task that *populates* `r.compression` for a
  real run, via `startCompression`)

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
  and when `spec.Gateway == nil` (P9, defense in depth — Task 1's
  `Validate` rule and Task 4's runtime bootstrap check should both already
  have refused this combination before it reaches here, but `BuildEnv` is
  the point closest to the real credential and checks it again, the same
  posture its existing oauth-vs-gateway refusal already has).
- The "Last, so nothing declared or inherited can override the routing"
  block (`env.go:134-141`) gains a step **after** the existing
  `GatewayVars` call, not before — **a correction to an earlier draft of
  this plan, which had the ordering backwards and would have been
  overwritten by it**: `maps.Copy(out, vars)` (`internal/agent/env.go:140`) is itself the
  last write before this task's change, so a compression step placed
  *before* it would have its own `ANTHROPIC_BASE_URL` immediately
  clobbered by the gateway's. The new step runs *after* that `maps.Copy`:
  when `spec.Compression != nil`, `out["ANTHROPIC_BASE_URL"]` is
  overwritten once more to `spec.Compression.BaseURL`. This is also why
  `TestBuildEnvRoutesThroughCompressionNotGateway` below is in this plan's
  Review Focus as a test that would have failed against the earlier,
  wrongly-ordered sketch — it isn't a decorative addition, it's the test
  that catches exactly this mistake.
- **`Deps` gains `CompressionOverride *agent.CompressionProxy`, test-only.**
  `Run` applies it unconditionally, right after constructing `r` and before
  anything else runs (`internal/runner/runner.go:283-290`): `if
  d.CompressionOverride != nil { r.compression = d.CompressionOverride }`.
  This is the mechanism the two tests below actually need: Task 3 is
  implemented and tested *before* Task 4 exists, so there is no
  `startCompression` yet to populate `r.compression` through the real
  config-and-gateway path, and `agent.compression` itself stays refused by
  `Validate` throughout Tasks 1-9 regardless (P11) — a test can only reach
  a populated `r.compression` by setting it directly. `Deps` is the one
  bridge test code in `internal/runner`'s black-box test package
  (`runner_test`, confirmed by `agentenv_test.go`/`gateway_test.go`/
  `runner_test.go`'s own package clauses) has for injecting test-only
  behavior, following the same `h.deps.XXX = value` pattern `gwConfig`/
  `newGWFiles` already establish for other fields. It is safe against real
  `fugaro.yaml` content for the same reason every other `Deps` field is:
  `Deps` is built entirely by Fugaro's own trusted bootstrap code
  (`cmd/fugaro`, `internal/cli`), never from repository content, so nothing
  committed to a run's branch can reach it. (This field is distinct from
  Task 4's `Deps.CompressionForTest`, which instead lets the *real*
  `startCompression` run against a resolved config — see Task 4.)
- `internal/runner/agentenv.go`'s `writeManagedSettings` (P5) reads
  `r.compression` (the `*agent.CompressionProxy` field this task declares on
  `run`, above — set directly by this task's own tests via
  `Deps.CompressionOverride`, populated for a real run only once Task 4
  exists) and, when non-nil, overwrites the
  `ANTHROPIC_BASE_URL` entry in the `vars` map `GatewayVars` returned,
  before the call to `agent.WriteManagedSettings` — the same single write,
  not a second one, and the same "after, not before" ordering as `BuildEnv`
  above.
- `checkSettingsRouting` itself is **unchanged** (P6): its existing
  `if r.gatewayOn()` call site already covers every compression-eligible
  run once Task 4's gateway-required bootstrap check is in place. This task
  adds the test that proves it, not a code change.

**Tests**, reusing this package's existing harness helpers
(`newGW`/`gwConfig`/`h.run`/`envValue` in `internal/runner`, already used by
`TestStagePinsPerRole` and `TestManagedSettingsBeforeEveryStage`):

```go
// internal/agent/env_test.go

func TestBuildEnvUnchangedWithNoCompression(t *testing.T) {
	// spec.Compression nil: run the package's existing TestBuildEnv* cases
	// unmodified and assert byte-identical output -- the point is zero
	// behavior change, proven by reusing the existing fixtures, not a new
	// assertion written from scratch.
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

func TestWriteManagedSettingsCarriesCompressionURL(t *testing.T) {
	h := newGW(t, gwConfig(t, ""), "observe", "", anthropicfake.MessageOK(sonnet, pricing.Usage{Output: 1})).harness
	h.deps.CompressionOverride = &agent.CompressionProxy{BaseURL: "http://127.0.0.1:9100"} // this task's own test-only Deps field; Task 4 is what populates r.compression for a real sidecar
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	env, _, _ := settingsEnv(t, h)
	if env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:9100" {
		t.Fatalf("managed settings ANTHROPIC_BASE_URL = %q, want the sidecar's URL", env["ANTHROPIC_BASE_URL"])
	}
}

func TestCheckSettingsRoutingAppliesWithCompression(t *testing.T) {
	files := map[string]string{".claude/settings.json": `{"env": {"ANTHROPIC_BASE_URL": "https://evil.example"}}`}
	h := newGWFiles(t, gwConfig(t, ""), "observe", "", files).harness
	h.deps.CompressionOverride = &agent.CompressionProxy{BaseURL: "http://127.0.0.1:9100"}
	if _, err := h.run(t, implement("feature")); err == nil {
		t.Fatal("a committed settings.json routing key was not refused with compression on")
	}
}
```

(`TestWriteManagedSettingsUnchangedWithNoCompression` is not a new test:
the existing `TestManagedSettingsBeforeEveryStage` already pins the
no-compression golden shape, and stays unmodified by this task — a second,
duplicate golden test would add nothing. The implementer confirms it still
passes unchanged as this task's own regression check.)

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
git add internal/agent/env.go internal/agent/env_test.go internal/runner/agentenv.go internal/runner/agentenv_test.go internal/runner/runner.go
git commit -m "headroom compression task 3: EnvSpec and the managed settings file both chain the sidecar in"
```

---

### Task 4: the runner starts and supervises the sidecar, and gates on the gateway

**Flagged for a security-focused review (high effort, Opus):** touches
`internal/runner`'s status semantics at a path every run goes through.
**This task does not lift `agent.compression`'s refusal** — that is Task
10 alone (P11); after this task merges, `agent.compression` is fully wired
but still refused by `config.Validate`.

**Files:**
- Edit: `internal/runner/gateway.go` (or a new `internal/runner/compression.go`
  — the implementer's choice, following whichever keeps `startGateway`
  readable), `internal/runner/runner.go`, their tests

**Interfaces:**
- **`Deps` gains two more test-only fields, both needed because
  `internal/runner`'s own tests are a black-box package (`runner_test`) that
  can only drive behavior through `fugaro.yaml` text and `Deps`, and because
  `agent.compression` stays refused by `Validate` throughout Tasks 1-9
  (P11) regardless of what this task wires up:**
  - **`CompressionForTest string`.** Applied once, immediately after
    `resolveConfig` succeeds and before `r.cfg` is assigned
    (`internal/runner/runner.go:831-867`): `if r.d.CompressionForTest != ""
    { cfg.Agent.Compression = r.d.CompressionForTest }`. The YAML committed
    to a test's fake remote never contains a `compression:` key at all —
    tests use the plain fixture (`gwConfig(t, "")`) — so `Validate` runs
    normally and has nothing to refuse; the override happens only in Go
    memory, strictly after `Validate` has already accepted the real config.
    This is the one, narrow exception to P11's refusal, reachable only
    through `Deps`, which Fugaro's own trusted bootstrap code builds
    (`cmd/fugaro`, `internal/cli`) — never from repository content, so
    nothing committed to a run's branch can set it.
  - **`StartHeadroom func(ctx context.Context, o headroomproc.Options)
    (sidecarProcess, error)`**, where `sidecarProcess` is a small interface
    this task adds in `internal/runner` — `URL() string`, `Dead() <-chan
    struct{}`, `Stop(context.Context) error` — the three methods
    `startCompression` actually calls. `*headroomproc.Process` (Task 2)
    already has this exact method set, so it satisfies `sidecarProcess`
    with no change to `internal/headroomproc` itself. `startCompression`
    calls `r.d.StartHeadroom` when non-nil, the real `headroomproc.Start`
    otherwise. This is the seam the test harness's fake sidecar is built on
    (`h.compressionStarts`, `h.lastCompressionEnv`, `h.compressionStops`,
    `h.killSidecarDuring`, `h.hangSidecarDuring` below), so no test in this
    task ever spawns a real sidecar process — the same reasoning
    `TestMain`'s re-exec fake serves in `internal/headroomproc` itself
    (Task 2), one layer up.
- A new `(r *run) startCompression(ctx context.Context) error`, called once
  per run (P4), **after `startGateway` has returned successfully and before
  the first stage**. It is **the one place the `agent.auth`-plus-gateway
  check from P9 actually runs**: `if r.cfg.Agent.Compression != "" &&
  !r.gatewayOn() { return fmt.Errorf("agent.compression needs the budget
  gateway on (budget.mode: observe or enforce)") }` — this fires *after*
  `resolvePolicy` has merged the installation ceiling, the default branch's
  file and the run's own file (`internal/runner/policy.go:58-126`), which is
  the only point the real, effective budget mode exists; nothing in
  `internal/config` can make this check (P9's full reasoning). If it passes,
  `startCompression` builds `headroomproc.Options.Env` explicitly (P7 —
  never `r.d.Env`) to carry **only the gateway's `BaseURL`**
  (`r.gwAgent.BaseURL`, already set by `startGateway`,
  `internal/runner/gateway.go:105-120`) as Headroom's own upstream — **not
  the gateway's token**, which an earlier draft of this task also passed in
  unnecessarily: Claude Code's own `ANTHROPIC_API_KEY` already carries the
  gateway's per-run token on every request (`GatewayVars`'s api-key case is
  unchanged by compression), and an ordinary reverse proxy forwards that
  header to whatever upstream it's configured with, so the token reaches
  the gateway without Headroom needing a separate copy (P7's full
  reasoning; Task 7's live reading of the real binary confirms it actually
  behaves this way before this is relied on in production). It resolves
  the pinned binary's path (from `/etc/fugaro/base.json`'s pins, the same
  place Claude Code's own version is recorded, `docs/design/base-image.md`
  §3), calls `headroomproc.Start`, and on success sets
  `r.compression = &agent.CompressionProxy{BaseURL: p.URL()}` for
  `agent.BuildEnv` (Task 3) and `writeManagedSettings` (Task 3) to pick up
  as *Claude Code's* upstream — a different URL from the one just given to
  Headroom, which is intentional: Claude Code talks to the sidecar, the
  sidecar talks to the gateway.
- `r.compression`'s sidecar `Dead()` channel (closed on crash **or** a
  detected hang, P8) is watched alongside the gateway's `Halted()` channel
  wherever stages already watch for one (`internal/runner/gateway.go:215-248`'s
  pattern), but **it is reported through the ordinary stage-failure path —
  `r.fail("stage %s: the Headroom sidecar died or stopped responding")` (or
  whatever the stage's own error-return already does for an
  `agent.Claude.Run` error) — assigning no special status at all (P10).**
  **A correction to an earlier draft of this task, which split this into a
  "no PR yet" `infra_error` case and a "PR already exists" `failed`+`draft`
  case:** `Dead()` can only ever fire *after* `startCompression` has already
  returned successfully (the sidecar started), which only happens once the
  stage loop is already running — so a `Dead()`-reported failure is always
  a stage failure, never a bootstrap one, and the question "did a PR exist
  yet" never arises the way the earlier draft assumed: `Run`'s own doc
  comment is explicit that its returned error is non-nil "for an
  infra_error... or when writing the final record itself fails" and nothing
  else (`internal/runner/runner.go:265-271`) — a stage failure reported via
  `r.fail` instead reaches `finalize`, which commits an empty commit when
  the agent made none (`runner.go:1732-1736`) and always produces a PR
  (confirmed by `TestStageTimeoutOpensDraft`, `runner_test.go:362-377`: an
  implement-stage failure ends `StatusFailed` with **`err == nil`** from
  `Run` itself). So every `Dead()`-reported failure, at any stage, ends
  `StatusFailed`+`OutcomeDraft` with `Run` returning `nil` — the *only*
  compression-related path that can produce `StatusInfraError` is
  `startCompression` itself returning an error at bootstrap, before the
  stage loop starts (the gateway check failing, or `headroomproc.Start`
  erroring) — genuinely "the run itself returns an error," matching the
  doc comment's own test for `infra_error`.
- `r.endRun`/wherever the sidecar's `Stop` is guaranteed even on a panicking
  stage — the implementer follows whatever defer/cleanup pattern
  `r.gw.Close()`'s own call site already uses, so a leaked sidecar process
  is exactly as impossible as a leaked gateway goroutine is today.

**Tests:**
```go
func TestRunnerStartsNoSidecarByDefault(t *testing.T) {
	h := newGW(t, gwConfig(t, ""), "observe", "").harness // agent.compression unset
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if h.compressionStarts != 0 { // the fake headroomproc constructor's call count
		t.Fatalf("headroomproc.Start was called %d times with compression unset", h.compressionStarts)
	}
}

// TestStartCompressionRefusesWithoutGateway uses the plain, gateway-less
// harness (newHarness, runner_test.go:66 -- the same constructor
// TestStageTimeoutOpensDraft uses) rather than newGW, since the point is
// that the budget is off: no ceiling, no default-branch budget, nothing
// turns the gateway on. The fixture's fugaro.yaml is committed unmodified
// (no compression: key, so Validate has nothing to refuse); only
// Deps.CompressionForTest carries "headroom" into the resolved config, in
// memory, after Validate has already run. This is a bootstrap-time failure
// -- startCompression returns before the stage loop ever starts -- so, per
// Run's own doc comment (runner.go:265-271), Run itself returns a non-nil
// error and the record is StatusInfraError, unlike every Dead()-reported
// failure below.
func TestStartCompressionRefusesWithoutGateway(t *testing.T) {
	h := newHarness(t, "", nil)
	h.deps.CompressionForTest = "headroom"
	rec, err := h.run(t, implement("feature"))
	if err == nil {
		t.Fatal("agent.compression without the gateway did not fail the run")
	}
	if rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "budget gateway") {
		t.Fatalf("record = %+v, want infra_error naming the budget gateway", rec)
	}
	if h.compressionStarts != 0 {
		t.Fatal("headroomproc.Start was called despite the gateway being off")
	}
}

// compressionHarness builds a gateway-on harness with agent.compression
// accepted via Deps.CompressionForTest -- the fixture's own fugaro.yaml
// never contains a compression: key, so Validate's reserved-and-refused
// rule (P11) runs normally and has nothing to refuse; this is a correction
// to an earlier draft of this helper, which tried to commit
// "compression: headroom" directly into fugaro.yaml and have Validate
// accept it, which P11 forbids throughout Tasks 1-9. Deps.StartHeadroom is
// left at the harness's default fake (set once, in newHarnessFiles, for
// every test -- see the Interfaces note above), so no real sidecar process
// is ever spawned.
func compressionHarness(t *testing.T) *harness {
	t.Helper()
	h := newGW(t, gwConfig(t, ""), "observe", "").harness
	h.deps.CompressionForTest = "headroom"
	return h
}

func TestRunnerBuildsANarrowSidecarEnv(t *testing.T) {
	h := compressionHarness(t)
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	for _, kv := range h.lastCompressionEnv { // the Env headroomproc.Start was actually called with
		if strings.Contains(kv, "GH_TOKEN") || strings.Contains(kv, "ANTHROPIC_API_KEY") {
			t.Fatalf("the sidecar's env leaked a credential: %s", kv)
		}
	}
}

// TestRunnerFailsStageOnSidecarDeath needs no git.pr.checkpoints control,
// a correction to an earlier draft of this plan, which disabled checkpoints
// to try to guarantee "no PR exists yet" when implement is killed, on the
// mistaken premise that this changes the outcome. It doesn't: Dead() can
// only fire once the sidecar has already started, which is only possible
// once the stage loop is already running, so this is always a stage
// failure (reported via r.fail), never a bootstrap one -- and a stage
// failure always reaches finalize, which commits an empty commit and opens
// a PR when the agent made none (runner.go:1732-1736). The assertion below
// is deliberately the same shape as TestStageTimeoutOpensDraft's: Run
// returns a nil error, and the record is StatusFailed+OutcomeDraft,
// regardless of which stage the kill happens during.
func TestRunnerFailsStageOnSidecarDeath(t *testing.T) {
	h := compressionHarness(t)
	h.killSidecarDuring("implement") // closes the fake Dead() channel mid-stage
	rec, err := h.run(t, implement("feature"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("a sidecar death mid-stage = %s/%s, want failed/draft (TestStageTimeoutOpensDraft's own shape)", rec.Status, rec.Outcome)
	}
}

// TestRunnerFailsStageOnSidecarHang mirrors TestRunnerFailsStageOnSidecarDeath
// exactly, but the fake Dead() channel closes because of a simulated
// liveness failure rather than a process exit -- proving the runner treats
// both the same way (P8, P10): there is no second code path to test
// separately, only a second way of causing Dead() to close.
func TestRunnerFailsStageOnSidecarHang(t *testing.T) {
	h := compressionHarness(t)
	h.hangSidecarDuring("implement") // the fake's liveness probe starts failing; Dead() closes after LivenessMisses
	rec, err := h.run(t, implement("feature"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != runstore.StatusFailed || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("a sidecar hang mid-stage = %s/%s, want failed/draft", rec.Status, rec.Outcome)
	}
}

func TestRunnerStopsSidecarOnRunEnd(t *testing.T) {
	h := compressionHarness(t)
	if _, err := h.run(t, implement("feature"), review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	if h.compressionStops != 1 {
		t.Fatalf("Stop was called %d times, want exactly 1", h.compressionStops)
	}
}
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestRunnerStartsNoSidecar|TestStartCompressionRefusesWithoutGateway|TestRunnerBuildsANarrowSidecarEnv|TestRunnerFailsStageOnSidecarDeath|TestRunnerFailsStageOnSidecarHang|TestRunnerStopsSidecarOnRunEnd' ./internal/runner/`
Expected: FAIL.

- [ ] **Step 3: Write the implementation**
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/runner/... && go test -race -count=1 ./internal/runner/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/runner/
git commit -m "headroom compression task 4: the runner starts and supervises the sidecar, gated on the gateway"
```

---

### Task 5: `Cost`'s cache read/write token totals, and `fugaro diagnose`

**Files:**
- Edit: `internal/runstore/cost.go`, `cost_test.go`
- Edit: `schemas/result.schema.json` (new, `omitempty` fields need a schema
  entry or `TestResultSchemaAcceptsRunnerRecords` fails)
- Edit: `internal/cli/diagnose.go`, `diagnose_test.go`

**Interfaces:**
- `Cost` gains, independent of G20 (P12 — **token counts, not a dollar
  split**: `Rates.Cost` returns one blended total per call, never a
  per-category breakdown, so a true cache-read-vs-write dollar figure needs
  new pricing code this plan does not propose writing; an earlier draft of
  this task proposed `CacheReadUSD`/`CacheWriteUSD` "summed from per-call
  pricing," which doesn't exist):
  ```go
  // CacheReadTokens and CacheWriteTokens are the run's prompt-cache read
  // and write token totals, summed from the gateway's per-call usage
  // (internal/gateway/sse.go's cache_read_input_tokens/
  // cache_creation_input_tokens parsing feeds pricing.Usage{CacheRead,
  // CacheWrite5m, CacheWrite1h}). They exist to answer design §3's
  // prompt-caching risk (a compressed session's cache prefix can change,
  // turning cheap reads into dearer writes) independent of whether G20's
  // per-stage model_usd has landed -- they answer "did the cache behave as
  // expected," not "what did a stage cost," and need no new pricing logic
  // since they are plain token sums, not a cost calculation.
  CacheReadTokens  int64 `json:"cache_read_tokens,omitempty"`
  CacheWriteTokens int64 `json:"cache_write_tokens,omitempty"`
  ```
- `fugaro diagnose` gains a `Cache: N read, M write (tokens)` line, printed
  only when either is non-zero — unchanged output for every run that
  doesn't exercise this path (gateway off, or a gateway run with no cache
  hits at all).
- **Not added, and this task's commit says so plainly rather than inventing
  a source:** any Headroom-specific before/after token or dollar field.
  No task in this plan reads Headroom's own stats API (none is confirmed to
  exist), so there is no source for such a field; design §3 drops the
  earlier draft's `TokensBefore`/`TokensAfter` proposal for the same reason.

**Tests:**
```go
func TestCostCacheFieldsRoundTrip(t *testing.T) {
	c := Cost{ModelUSD: 1.23, CacheReadTokens: 400, CacheWriteTokens: 50}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var got Cost
	if err := json.Unmarshal(data, &got); err != nil || got != c {
		t.Fatalf("round trip: got %+v, err %v", got, err)
	}
	if data2, _ := json.Marshal(Cost{ModelUSD: 1}); strings.Contains(string(data2), "cache_read_tokens") {
		t.Errorf("a zero CacheReadTokens was not omitted: %s", data2)
	}
}

func TestDiagnoseShowsCacheLineOnlyWhenNonZero(t *testing.T) {
	// A record with both fields zero: diagnose output unchanged (golden,
	// reusing an existing diagnose fixture). A record with either non-zero:
	// diagnose gains exactly one new line, asserted by substring, not
	// full-output equality.
}

func TestResultSchemaAcceptsCacheFields(t *testing.T) {
	// A result.json fixture with cache_read_tokens/cache_write_tokens set
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
git commit -m "headroom compression task 5: Cost's cache read/write token totals, for the design's prompt-caching risk"
```

---

### Task 6: `fugaro run --no-compression`, `task.Spec`, and the image gate

**Flagged for a security-focused review (high effort, Opus):** a new
strictly-decoded `task.Spec` field with its own version-skew gate — the same
class of change `named-oauth-tokens.md` flagged for review, since a
launch-time gate that is subtly wrong (fires too late, or not on every
launch path) is exactly how an old runner would end up running a feature it
doesn't know about.

**Amended 2026-10-27, as this task was implemented: the gate's skip case is
the configuration, not the flag.** The paragraph below ("refuses launching a
run whose resolved `agent.compression` is non-empty **and** not already
overridden off") is the one thing here that does not survive contact with an
older runner. `--no-compression` turns the *sidecar* off; it does not remove
`agent.compression` from the `fugaro.yaml` the run still reads, and an older
runner's `config.Parse` refuses that key outright (§5's "Strict-parser
consequences", the reason this gate exists at all) — so skipping the gate
because the override is set would launch exactly the run the gate is for, and
the override would be a way *past* it. The gate is therefore judged on the
field and the file together: it fires when the task carries
`compression_off`, or when the resolved configuration turns compression on,
whether or not `--no-compression` is given. The field's half is not implied by
the file's — a `--pr` follow-up and a `--retry` inherit `CompressionOff` from
the run they continue, so they carry it even after the repository's key has
been removed — and it is the half the sibling gates' own rule ("the check is
the field's, not the flag's") asks for. Two consequences for the tests below:
`TestCompressionImageGateSkippedWhenOverriddenOff` is replaced by
`TestCompressionImageGateNotTriggeredWithoutCompression` (a launch whose
configuration turns compression on is refused with or without the flag; one
that neither carries the field nor reads such a key is never gated), and
`TestRunNoCompressionSetsSpecField` no longer asserts that the launch is never
refused — it asserts the field is written when there is compression to turn
off and absent when there is not. **Three smaller corrections, same date:**
`compressionSince` names `0.12.0` ("Dolphin"), not the `0.8.0` the snippet
below carries, because the constant must name the release that ships the
field's runner half and this plan's tasks landed after 0.11.6; the runner
honours the field in one place the plan's file list for this task did not
name (`internal/runner/runner.go`'s `resolveConfig`, clearing
`Agent.Compression` after `Deps.CompressionForTest`, so off always wins); and
because `config.Validate` still refuses `agent.compression` (P11), no
committed `fugaro.yaml` can turn compression on for a launcher test, so the
CLI carries a test-only seam for the resolved value (`compressionForTest` in
`internal/cli/recipes_skew.go`, unset in every real binary and read from no
file, environment variable or flag).

**Files:**
- Edit: `internal/task/task.go`, `task_test.go`
- Edit: `schemas/task.schema.json`
- Edit: `internal/cli/run.go`, `internal/cli/recipes_skew.go` (**not
  `run.go`** — an earlier draft of this task put `compressionSince` in
  `run.go`; every other `*Since` constant (`recipesSince`, `layeredSince`,
  `namedTokensSince`) actually lives in `internal/cli/recipes_skew.go`,
  confirmed by reading it, and `docs/release.md`'s own "Before you tag"
  checklist names that file for each of them), `run_test.go`

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
// internal/cli/recipes_skew.go, next to namedTokensSince:
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
func TestTaskSpecCompressionOffRoundTrips(t *testing.T) {
	s := Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", CompressionOff: true}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var got Spec
	if err := json.Unmarshal(data, &got); err != nil || !got.CompressionOff {
		t.Fatalf("round trip: got %+v, err %v", got, err)
	}
	if data2, _ := json.Marshal(Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main"}); strings.Contains(string(data2), "compression_off") {
		t.Errorf("a false CompressionOff was not omitted: %s", data2)
	}
}

// TestOldTaskParserRefusesCompressionOffField mirrors
// TestOldRunnerRefusesOAuthTokenField: a Spec shaped like task.go before
// this task (every current field copied, minus CompressionOff) must refuse
// compression_off, strictly.
func TestOldTaskParserRefusesCompressionOffField(t *testing.T) {
	type oldSpec struct {
		Version     int       `json:"version"`
		RunID       string    `json:"run_id"`
		Repo        string    `json:"repo"`
		Ref         string    `json:"ref"`
		Workflow    string    `json:"workflow,omitempty"`
		Task        string    `json:"task,omitempty"`
		Branch      string    `json:"branch,omitempty"`
		PR          int       `json:"pr,omitempty"`
		PreviousRun string    `json:"previous_run,omitempty"`
		Overrides   Overrides `json:"overrides"`
		RequestedBy string    `json:"requested_by,omitempty"`
		Batch       string    `json:"batch,omitempty"`
		Recipe      *Recipe   `json:"recipe,omitempty"`
		ProjectLayer *ProjectLayer `json:"project_layer,omitempty"`
		OAuthToken  string    `json:"oauth_token,omitempty"`
	}
	data := []byte(`{"version":1,"run_id":"` + runID + `","repo":"acme/app","ref":"main","compression_off":true}`)
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var old oldSpec
	if err := dec.Decode(&old); err == nil {
		t.Fatal("a pre-feature Spec decoder accepted compression_off silently")
	}
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
git add internal/task/task.go internal/task/task_test.go schemas/task.schema.json internal/cli/run.go internal/cli/recipes_skew.go internal/cli/run_test.go
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
  HEADROOM_SHA256_AMD64=…`/`_ARM64=…` — or, if no arm64 artifact exists yet,
  a note saying so, in which case the runner's own bootstrap check (Task 4)
  also refuses `agent.compression` on an arm64 job — design §5's "only if
  Task 7 finds no arm64 artifact" wording, not an unconditional refusal; a
  small addition to that task's logic this task's commit should
  cross-reference)
- Edit: `images/base/tools.tsv` (a `harness`-tier presence row — **not**
  `kit`; design §5 placed Headroom in layer 3 with the other harnesses, and
  the tier should match)
- **Not edited: `images/base/bump.sh`.** It does not exist in this
  repository yet — `images/base/Dockerfile:10` only comments that it *will*
  rewrite pins, and the actual description of the planned monthly-bump
  workflow is `docs/design/base-image.md:174` (**not** `docs/base-image.md`,
  the user-facing doc, which does not mention it at all — an earlier draft
  of this task cited the wrong file). Until `bump.sh` is built (a separate,
  much larger piece of work — the whole monthly-bump mechanism, not
  something this one pin should trigger), resolving Headroom's pin is a
  manual `ARG` edit, the same as every other pin was before `bump.sh`
  existed. If `bump.sh` is built before this task starts, add Headroom's
  entry to it as a small, separate addition; this task does not build the
  mechanism as a side effect.
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

**Task 7 finding (2026-10-27): no standalone native binary is published, so
Step 1's first stop condition holds and this task ships no image change.**
Resolved from Headroom's own release page, as Step 1 requires
(`github.com/headroomlabs-ai/headroom`, its `/releases` list and the
`/releases/latest` payload, its README and `llms.txt`, the wheel
`headroom_ai-0.42.0-cp310-abi3-manylinux_2_28_x86_64.whl` downloaded and
unpacked, and `docs.headroomlabs.ai/llms-full.txt`):

- **The artifact inventory has no binary in it.** 175 releases, latest
  `v0.42.0` (2026-10-26, Apache-2.0). Across *every* release the assets are
  only `headroom_ai-<v>-<python tag>-<platform>.whl` (CPython `cp310-abi3`
  and `cp310`–`cp313`, for `manylinux_2_28` x86_64/aarch64, macOS
  x86_64/arm64 and win_amd64), the `headroom_ai-<v>.tar.gz` sdist, and two
  npm tarballs (`headroom-ai-<v>.tgz`, `headroom-openclaw-<v>.tgz`). No
  release, on any architecture, ships a native self-contained executable.
- **The CLI is a Python console script, not a binary.** The wheel's
  `dist-info/entry_points.txt` is `headroom=headroom.cli:main`, its
  `METADATA` says `Requires-Python: >=3.10`, and its only compiled object is
  the CPython extension `headroom/_core.abi3.so`. The README's install lines
  are `uv tool install --python 3.13 "headroom-ai[all]"` and
  `pip install "headroom-ai[all]"`, its npm line is annotated "TypeScript SDK
  only — no CLI", and the installation page says "Install Headroom via pip,
  npm, or Docker". Running it therefore means a Python runtime plus the
  package's dependency closure (`[all]` = proxy, code, ml, memory, relevance,
  image, reports, otel, evals, voice, html, mcp, spreadsheet).
- **This is design §5's "materially heavier" case**, which is why the route
  is the owner's to choose and not this task's: the base image already
  installs one Python tool from a hash-locked lock file
  (`images/base/graphify/requirements.lock.txt`, uv-compiled with
  `--generate-hashes`), so a Python-distributed Headroom is not unprecedented
  — but adding a Python runtime, a wheel closure and model assets to the
  harness layer is a size and supply-chain decision on top of the one this
  task was scoped to make.
- **Checksums, for the record.** GitHub's release API publishes a per-asset
  `sha256:` digest, so a pin would be verifiable against a
  publisher-provided value; I recomputed two and they match
  (`headroom-ai-0.42.0.tgz` `372c4dc7…`, the `manylinux_2_28_x86_64` wheel
  `fbb3ceb8…`). No release publishes a separate checksum file or a signature,
  and the GitHub attestations API answers 404 for that wheel's digest. There
  is simply no binary to pin.
- **Two further facts to weigh with the no-egress premise**, both from
  Headroom's own docs (`docs.headroomlabs.ai/llms-full.txt`, the network
  egress and installation sections): the ML compression paths download models
  from `huggingface.co` on first use, and a telemetry beacon is **on by
  default** (with a daily `pypi.org` update check); `HEADROOM_OFFLINE=1`
  refuses all of it, and the docs' own advice is to pre-provision model assets
  rather than allow runtime fetches. A no-egress test for the real binary
  would therefore have to pin that switch and a model cache, not merely run
  under `--network none` — a decision about the feature's shipped shape, not
  a test detail.

**Task 7, revised (2026-10-27): the owner's wheel ruling was carried out and
the pin stops on this task's own closure conditions, so there is still no
image change.** The owner's ruling allowed a hash-locked Python wheel in the
base image, installed the way graphify's is. The candidate release was
resolved and measured end to end rather than read off a release page; the
measurements below are from this container, on `linux/amd64`, against PyPI
and GitHub's release API.

- **The pin target itself is sound.** Release **0.40.0** (published
  2026-10-06, 21 days before this note, so inside the "at least 14 days old"
  condition the owner's 2026-10-27 instruction for this task set — **no
  document in this tree states such a rule**, and this note is not the place
  to invent one; 0.41.0 of 2026-10-14 and 0.42.0 of 2026-10-26 are not) it
  publishes `cp310-abi3` wheels for **both** `manylinux_2_28_x86_64`
  (`sha256:49b6d8a01c8eceb5b9e9a675720ed5e4cb24e467282642653aac7f2fd8faf47f`)
  and `manylinux_2_28_aarch64`
  (`sha256:140811066bfb7afe888f471be4a587375779af9de8dd76bb9a8126b52b8e930f`).
  GitHub's per-asset `sha256:` digests and PyPI's `digests.sha256` for those
  two files agree exactly, so a pin would be cross-checked as this task asks.
  The wheel is Apache-2.0 (`License-Expression: Apache-2.0`), its console
  script is `headroom=headroom.cli:main`, and `headroom --version` reports
  `headroom, version 0.40.0`.
- **The closure is more than double the ~400 MB ceiling this run was given,
  and more than four times what the base image has left.** The ~400 MB figure
  is **not this plan's and not the design's** — neither states a size limit
  anywhere — it is the owner's 2026-10-27 instruction for this task, recorded
  here as the run's acceptance criterion rather than as a rule the tree
  carries. The in-tree budget is the stricter and independent check, and it
  is quoted at the end of this bullet. The extras
  the supervised sidecar needs are `[proxy]` — the request path and the local
  compressor — chosen over `[all]` because `[all]` adds `[code]`, `[evals]`,
  `[html]`, `[image]`, `[mcp]`, `[memory]`, `[ml]`, `[otel]`, `[relevance]`,
  `[reports]`, `[spreadsheet]` and `[voice]`, none of which the sidecar's own
  process uses. `uv pip compile` of `headroom-ai[proxy]==0.40.0`
  (`--universal --python-version 3.12 --generate-hashes`, the command
  graphify's `requirements.in` documents) resolves to a **95-package** lock
  and installs to **488 MB** of site-packages, **571 MB** for the venv,
  **662 MB / 746 MB** once `--compile-bytecode` — the flag graphify's own
  test requires, because the venv is root-owned — is applied. Adding uv's
  managed CPython (109 MB) makes it **~855 MB before any model asset**. The
  base image is already **3812 MB** on amd64 against `images/size-budget.sh`'s
  4000 MB hard budget, so it has 188 MB of headroom: this cannot be absorbed
  even at the smaller figure. For comparison, `headroom-ai` with no extras —
  which cannot start the proxy at all, since `fastapi` and `uvicorn` live in
  `[proxy]` — is 68 packages and 264 MB of site-packages.
- **The `[proxy]` closure also fetches data at run time that
  `--require-hashes` does not cover.** Installed from that lock and run under
  `--network none`, `headroom proxy` blocks at startup on `Downloading
  Kompress ONNX model from chopratejas/kompress-v2-base` and never binds;
  that artifact is `onnx/kompress-int8-wo.onnx`, **274 MB** (its tokenizer
  3.6 MB), and the router adds a second HuggingFace model
  (`chopratejas/technique-router`). `HEADROOM_OFFLINE=1` refuses the fetch,
  logs *"Compression falls back to the non-ML path"*, and a
  `POST /v1/compress` then **does** complete with no network at all
  (`tokens_before: 10305`, `tokens_after: 272`,
  `transforms_applied: ["router:text:0.03"]`) — so a no-egress pass is
  possible, but it is the structural-only path: a run that wants the ML
  compressors the docs' own prose describes needs a pre-provisioned model
  cache of ~278 MB or more, which is not a Python distribution and so has no
  hash the lock can carry. Two egress attempts the docs' own "network egress"
  table does not list also appear at startup even with `HEADROOM_OFFLINE=1`
  set: `litellm` fetches `raw.githubusercontent.com/BerriAI/litellm`'s model
  cost map (three attempts, then a local fallback), and the proxy's default
  session-summary beacon is on unless `HEADROOM_BEACON=off`,
  `DO_NOT_TRACK=1` or `HEADROOM_OFFLINE` is set.
- **Neither of the other two stop conditions holds, recorded for the
  record.** Every needed wheel resolves for `aarch64-manylinux_2_28` (93
  packages; no missing build), so the arm64 runner is not the problem and
  Task 4's bootstrap check needs no arm64 clause. **On licences: read per
  package from each installed wheel's own `METADATA`, preferring its
  `License-Expression` over its `License` field and over its classifiers,
  which are looser than either** — the closure is MIT, Apache-2.0,
  BSD-2-Clause/BSD-3-Clause, ISC and PSF-2.0 throughout, with MPL-2.0 in
  exactly three packages and never as the only option on the combined work:
  `certifi` (MPL-2.0 alone), `tqdm` (`MPL-2.0 AND MIT`) and `orjson`
  (`MPL-2.0 AND (Apache-2.0 OR MIT)` — an earlier draft of this note listed
  orjson with the other two on the strength of its classifier line alone,
  which names all three licences separately and reads as if MPL-2.0 were the
  whole answer). `numpy` (`BSD-3-Clause AND 0BSD AND MIT AND Zlib AND
  CC0-1.0`) and `regex` (`Apache-2.0 AND CNRI-Python`) are permissive
  throughout. Nothing GPL, AGPL or SSPL. This is 93 installed packages;
  `pywin32` appears in the universal lock for Windows only.

**Outcome: the pin stops, and `images/base/Dockerfile`,
`images/base/tools.tsv`, `/etc/fugaro/base.json`, the base-image design doc
and `SECURITY.md` are all unchanged by this task** — there is no `headroom`
row, no private venv, no wrapper and no new test, so the base-image selftest
and the no-egress CI docker test have nothing new to cover.

**The no-egress gate this task was meant to feed therefore has no runner, and
every document that reads it as already satisfied is now stale.** That gate
is the design's precondition for ever shipping, not a detail: design §2 makes
the pre-ship `--network none` test the thing whose failure means *"do not ship
it"*, design §5 lists it as one of the two live checks that *"must pass before
that one final change merges"*, and design §6 says its result *"gates the
lift"*. The plan reads it as satisfied once PR 2 merges in four places — Task
10's gating paragraph (*"already being green … it runs as part of PR 2's own
CI"*, and *"Task 7's no-egress CI result is already known by this point"*),
the PR-groups paragraph (*"PR 3 simply checks it was"*), the Global
Constraints bullet (*"Task 7's CI docker test proves an actual compression
pass completes under `--network none`"*) and Task 2's own parenthetical
(*"That property is tested once, against the real pinned binary, in Task 7's
CI docker test"*). Check L3 leans on it too (*"that signal was Task 7's CI
test, which already ran"*), and Task 8's SECURITY.md bullet is to tell the
reader the property is *"tested, not enforced"*. **None of that is true as of
this note: the test was not written, because there is nothing pinned to run it
against, and PR 2 can merge without it.** The gate is not failed — it is
absent, and it is not one this task can supply, since the thing it tests (a
real compression pass by the pinned artifact under `--network none`) is
exactly what the two stop conditions above prevent. What *was* measured here
is not a substitute: it ran a wheel that is not in the image and is pinned by
nothing in this tree. **Task 10 must not treat this gate as satisfied, and
Task 8 must not write its bullet claiming a testing that never happened,
until the owner picks one of the routes below and a Task 7 follow-up pins an
artifact and writes the test against it.** Every site named above now carries
a dated pointer back to this note: the four plan paragraphs, Task 8's bullet,
Check L3, and the design's §2, §5, §6 and §7 claims.

Lifting this needs a decision this task cannot make, and it is a base-image
budget decision before it is a Headroom one: either raise
`images/size-budget.sh`'s `MAX_MB` by roughly 0.9 GB and pre-provision a
hash-pinned model cache beside the venv, or ship the structural-only path
(`HEADROOM_DISABLE_KOMPRESS=1` and `HEADROOM_OFFLINE=1`, the shape that was
measured working under `--network none`) and accept that the ML compressors
are off — the second is still ~0.67 GB over the current budget, so neither
route fits without the owner raising it. **Either way the no-egress test is
still owed**: a structural-only shape needs it just as much, since the
measurement above is the only thing that would have to be re-run against
whatever artifact is finally pinned. **The design's §5 premise still stands
and is now measured: a Python-distributed Headroom is materially heavier than
any other piece of this design, and heavier than this base image's remaining
budget, not merely heavier than the standalone binary Task 7 went looking
for.**

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
  **(2026-10-27: the "tested" half of that wording has nothing behind it —
  Task 7's revised run pinned no artifact and wrote no no-egress test, so this
  bullet must not claim a testing that did not happen. See the Task 7 revised
  note.)**
- New: `docs/compression.md`, covering `agent.compression`, the
  `--no-compression` flag (and the deliberate absence of a force-on flag),
  the `api-key`-with-gateway-on restriction, the `oauth`/`vertex` refusals,
  a pointer to the design doc for the full threat model, and — since
  `agent.compression` is still reserved and refused at this point in the
  plan (Task 10 hasn't run yet) — a note that the key is not yet usable,
  so this doc does not overclaim a feature nobody can turn on.
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

### Task 9: `docs/release.md`'s checklist line, and the full suite for PR 2

**Files:** `docs/release.md` (the "Before you tag" checklist)

- [ ] **Step 1: Add the `compressionSince` line**

`docs/release.md`'s "Before you tag" checklist (`docs/release.md:49` on)
gains a line for `compressionSince`, the same shape as its existing
`namedTokensSince` line (`docs/release.md:58`): naming the constant's real
location, `internal/cli/recipes_skew.go` (**not** `run.go` — see Task 6's
own correction of this), and the launch paths it gates (first run,
follow-up, `--workflow`, `--batch`). This is the last task of PR 2;
`agent.compression` is still fully reserved and refused when it merges.

- [ ] **Step 2: Full focused suite**

Run (foreground, split by package if any call would exceed 9 minutes):

```bash
go vet ./...
gofmt -l . # expect no output
go test -race -count=1 ./internal/config/... ./internal/agent/... ./internal/headroomproc/... ./internal/runner/... ./internal/runstore/... ./internal/task/... ./internal/cli/...
go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m
```

Expected: `ok` everywhere, no `gofmt` output.

- [ ] **Step 3: Commit**

```bash
git add docs/release.md
git commit -m "headroom compression task 9: docs/release.md's compressionSince line, full suite for PR 2"
```

---

### Task 10: the lift — PR 3, the only task that makes `agent.compression` usable

**Flagged for a security-focused review (high effort, Opus):** this is the
one change that turns the feature on for real runs; everything before it
was inert by construction (P11).

**Gated on, before this task starts:** Task 11's Check L2 (the bypass-list
capability reading of Headroom's real configuration surface), answered one
of two ways, with no third option — **there is no per-stage exclusion
mechanism in this plan to fall back on** (P4 starts one sidecar for the
whole run; nothing here builds a way to exclude specific stages from it,
even though `writeManagedSettings`'s own per-stage rewrite could in
principle support one in a future revision). If Check L2 finds Headroom's
configuration can honor the bypass list (pin the diff under review, verify
output, the system/task prompt), this task lifts the refusal
unconditionally, for every recipe. **If Check L2 comes back negative, this
task does not lift the refusal at all for any recipe that has a `review` or
`review_first` step** — which, reading `docs/design/generic-tool.md`'s
catalog (§3.5 there), is effectively every catalog recipe, so a negative
Check L2 is a strong signal against shipping compression at all in v1, not
a narrow scoping note. Task 7's no-egress CI result is already known by
this point, since it ran as part of PR 2's own CI. **(2026-10-27: it is not —
Task 7's revised run pinned no artifact and wrote no such test, so a
no-egress result is not merely "already known", it does not exist. This task
must not read the gate as satisfied; see the Task 7 revised note.)**

**Files:**
- Edit: `internal/config/validate.go` (the message for `agent.compression:
  headroom` changes from "not yet implemented" to nothing — it is simply
  accepted, subject to the `agent.auth`/`vertex`/`oauth` rule Task 1 already
  wrote, unaffected by this task)
- Edit: `schemas/fugaro.schema.json` (`compression`'s enum gains `"headroom"`)
- Move: `testdata/config/invalid/agent-compression-headroom.yaml` to
  `testdata/config/valid/` (the exact file Task 1 created, not a new one —
  `TestFugaroSchemaCorpus` and `internal/config`'s own corpus tests then
  prove the schema and `Validate` agree, moved in the same commit as the
  enum edit so neither is ever ahead of the other)
- Edit: `docs/compression.md` (remove the "not yet usable" note Task 8 added)
- Add: a section to whichever `docs/releases/vX.Y.Z.md` is open for 0.8.0
  (create it if nothing has yet, following `docs/releases/v0.6.5.md`'s
  shape), naming `compressionSince`, the `api-key`-with-gateway-on
  restriction, the `oauth`/`vertex` refusals, the opt-in default, and Check
  H2's recorded answer — not as a claim that compression is recommended,
  since §6's thresholds (Check L4) are a separate, owner-run judgment call
  this task does not itself clear, and have not run yet at this point.

**Tests:**
```go
// internal/config/config_test.go, built on config.Parse like Task 1's own
// tests (there is no validConfigFixture helper or Validate method; see
// Task 1's note on TestAgentOAuthToken's real idiom).
func TestValidateAcceptsCompressionAfterLift(t *testing.T) {
	base := "version: 1\nproject: example\ngit: { provider: github, base_branch: main }\nagent:\n  auth: api-key\n  compression: headroom\n" +
		"workflows:\n  app: { base: go, commands: { build: go build ./..., test: go test ./... } }\n"
	if cfg, ps := Parse([]byte(base)); len(ps) != 0 || cfg.Agent.Compression != "headroom" {
		t.Fatalf("compression + api-key was refused after the lift: %+v, %v", cfg, ps)
	}
}
```

(`TestFugaroSchemaCorpus` and `TestValidateRefusesCompressionWithOAuth`/
`TestValidateRefusesCompressionWithVertex` from Task 1 are **re-run, not
rewritten** — the point of this task is that everything built around the
reserved key keeps working once it's real.)

- [ ] **Step 1: Write the failing test**
- [ ] **Step 2: Run it to verify it fails**

Run: `go test -count=1 -run TestValidateAcceptsCompressionAfterLift ./internal/config/`
Expected: FAIL (still refused as "not yet implemented").

- [ ] **Step 3: Write the implementation** (the three file edits above)
- [ ] **Step 4: Run the full config suite, the schema corpus test, and the new test**

Run: `go test -count=1 ./internal/config/... ./schemas/...`
Expected: `ok`, including `TestFugaroSchemaCorpus` now passing with the
moved fixture in `valid/`.

- [ ] **Step 5: Commit**

```bash
git add internal/config/validate.go internal/config/config_test.go schemas/fugaro.schema.json testdata/config/invalid/agent-compression-headroom.yaml testdata/config/valid/agent-compression-headroom.yaml docs/compression.md docs/releases/
git commit -m "headroom compression task 10: lift the refusal (PR 3, gated on Check L2)"
```

---

### Task 11: owner-run live checks (not part of any PR; sandbox only)

Never run by an implementer or by CI. Matches this repository's existing
`docs/gcp-live-checklist.md` pattern (guardrails: `FUGARO_LIVE_GCP_PROJECT`/
`FUGARO_LIVE_REPO` only, ADC only, cleanup registered before any side
effect, `FACT:` lines). Add these as new, numbered checks continuing
`docs/gcp-live-checklist.md`'s existing sequence (the implementer reads the
file's current last check number when this task starts and numbers from
there; this plan uses placeholder labels **Check L1-L4** below (`L` for
"live check," chosen to avoid colliding with the design doc's own decision
labels H1/H2, which this plan's own P1-P12 continue — an earlier draft
numbered both schemes "H," which named two different things the same way)
rather than guess a number that will have drifted by the time this task
runs). **Dependency order, corrected from two earlier drafts' mistakes:**
Check L1 can run any time, independent of everything else. **Check L2 must
run *before* Task 10** — it gates the lift, is purely a documentation
reading (no cloud, no sandbox, no PR 2 needed), and can start as early as
the owner likes, ideally before implementation even begins. **Checks L3 and
L4 can
only run *after* Task 10 has merged** — an earlier draft had the §6
measurement listed as able to run "before any code merges," and a separate
earlier draft of this task's own H2 asked for a GCP network-isolation test
Cloud Run cannot actually perform (`SECURITY.md:58`: "egress is not
restricted. A run can reach any host" — there is no mechanism to give a
Cloud Run execution a restricted network, so a live version of this test
would be either impossible or a pointless duplicate of Task 7's CI docker
test, which already proves this, locally, with a tool that *can* restrict
network access). That old check is dropped and replaced below with
something actually runnable: a functional smoke test, not a network claim.

- [ ] **Check L1. `ANTHROPIC_BASE_URL` with `CLAUDE_CODE_OAUTH_TOKEN`.**
  (Can run any time; does not depend on this plan's code.) On a machine
  with a real `claude setup-token` value, run `claude -p` with both
  `ANTHROPIC_BASE_URL` pointed at a local, logging reverse proxy (a small
  script is enough — no Headroom binary needed) and `CLAUDE_CODE_OAUTH_TOKEN`
  set. **FACT:** does Claude Code's traffic reach the proxy at all; does the
  proxy see the bearer token in a request header. This settles design
  §1.1's open question and is a precondition for ever reconsidering the
  `oauth` refusal (Task 1) — it does not, by itself, lift it; lifting it is
  a future design's decision, not this check's.
- [ ] **Check L2. Can Headroom's own configuration honor the bypass list?**
  (Can run any time; no cloud, no sandbox — a documentation and
  configuration reading, the one piece of evidence this design has no other
  way to get. **Gates Task 10, with exactly two outcomes, not three — see
  Task 10's own gating note: there is no per-stage exclusion mechanism in
  this plan to fall back on.**) Read Headroom's real configuration surface
  (whatever Task 7 found when it resolved the pinned artifact) and
  determine: can it be told to never compress a specific kind of content —
  a preceding `git diff` call's output, `fugaro verify`'s output, the
  system/task prompt — across an entire session? **FACT:** quote the exact
  configuration mechanism found, if any, or record plainly that none
  exists. **A positive answer lifts the refusal unconditionally (Task 10);
  a negative answer withholds the lift entirely for any recipe with a
  `review`/`review_first` step** — in practice every catalog recipe
  (`docs/design/generic-tool.md` §3.5) — since this plan builds no
  per-stage exclusion to fall back on instead.
- [ ] **Check L3. A real, lifted compression pass works end to end in the
  sandbox.** (Depends on Task 10 having merged, and a sandbox image built
  from it.) Launch one ordinary sandbox task with `agent.compression:
  headroom` on an `agent.auth: api-key`, `budget.mode: observe` repository
  and confirm it completes normally — the first time real infrastructure
  (not Task 2's fake sidecar, not Task 7's local CI docker test) exercises
  the feature. **FACT:** pass/fail, and if fail, the exact failure. A
  failure here is a bug to fix, not a "do not ship" signal by itself (that
  signal was Task 7's CI test, which already ran); this check is functional,
  not a security property. **(2026-10-27: that signal never ran — Task 7's
  revised run pinned no artifact and wrote no test, so this check is no longer
  a functional follow-on to a passed security gate but the first real exercise
  of the feature. See the Task 7 revised note.)**
- [ ] **Check L4. The §6 measurement protocol.** (Depends on Check L3
  passing.) The paired A/B run over the owner's chosen task corpus, in the
  sandbox, on an `agent.auth: api-key` repository with `budget.mode:
  observe`, with each pair's arm order randomized or spaced beyond the
  prompt cache's TTL (design §6's pairing-hazard correction), and the
  design's judgment-call thresholds applied by hand to the results — **not**
  a statistical test (design §6's own correction). **FACT lines:** per
  pair, the dollar cost, the cache read/write token totals, the
  review-round count, the pass/fail outcome, and the wall time, for both
  arms; a summary line stating whether the owner judges the
  20%-dollar-reduction target met.

**Restore.** Nothing persists beyond the sandbox's own runs and branches,
cleaned up the same way every other check in `docs/gcp-live-checklist.md`
already requires (declined PRs, deleted branches, no change to any shared
installation).
