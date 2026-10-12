# Headroom compression: implementation plan

**Goal:** Let a workflow opt into routing its Claude Code model calls through
Headroom, a third-party context-compression proxy, to cut token cost and
usage-limit stalls, without weakening the budget gateway, the agent's
credential allowlist, or any loop (review, verify, gate) that depends on
reading something exactly. No code in this plan has been written or run.
**This PR (the one that adds this plan and its design doc) is docs-only: no
file under this plan's "Files" lists is touched by it.** Tasks 1 onward are
future work, gated on the owner approving the design and, independently, on
the live checks in Task 10 (§1.1 and §2 of the design) before the sidecar is
ever started by a real run — not only on approval of the plan's shape.

**Spec:** [docs/design/headroom-compression.md](../design/headroom-compression.md).
Its decisions H1 (integration surface: a chained, standalone sidecar,
refused with `auth: oauth` until a live check settles it) and H2 (fail
closed, not fall back to direct) are settled there; this plan's own
decisions continue the same letter, P1 onward, each independently vetoable.

**Depends on:** release 0.7.0 merged to `main` first (the base-image
consolidation and bucket-IAM hardening both touch `internal/infra` and the
harness layer heavily; design §5 states this is a scheduling dependency, the
same reasoning `docs/plans/2026-10-09-named-oauth-tokens.md` used for itself).
**Also depends on, but does not block on:** the 0.8.0 cost-preview's
per-stage token field (`docs/design/generic-tool.md` §1.1, G20) — Task 5
below degrades gracefully to a coarser, run-level token figure if that work
has not landed yet (design §6), so this plan does not stall on it, but an
implementer starting Task 5 should check whether `StageTiming` already
carries `model_usd`/`model` before choosing its approach.

**Checked before this plan was written:** every fact the design doc cites was
read from the code at this branch's base — `internal/agent/agent.go`,
`internal/agent/env.go`, `internal/agent/settings.go`, `internal/agent/relay.go`,
`internal/gateway/gateway.go`, `internal/runner/gateway.go`,
`internal/runner/prompts.go`, `internal/runner/runner.go`,
`internal/runstore/runstore.go`, `internal/runstore/cost.go`,
`internal/verify/verify.go`, `internal/procgroup/procgroup.go`,
`internal/config/config.go`, `internal/config/validate.go`, `SECURITY.md`,
`docs/design/base-image.md`, `docs/design/generic-tool.md`,
`docs/design/layered-config.md`, `docs/design/recipes.md`,
`docs/design/named-oauth-tokens.md`, `docs/gcp-live-checklist.md`. If the
merged code differs when a task starts (`internal/agent/env.go` and
`internal/runner/gateway.go` are both mid-churn candidates, given 0.7.0's and
0.8.0's own work nearby), that task's implementer adapts its edits to the
then-current file, not the other way round — the same rule
`docs/plans/2026-10-08-upgrade.md` and `docs/plans/2026-10-09-named-oauth-tokens.md`
both used for themselves.

## Decisions (veto any before execution starts)

Continuing the design doc's H1 and H2:

- **P1. The sidecar is its own package, `internal/headroomproc`, not folded
  into `internal/agent` or `internal/runner`.** It owns process supervision
  only (start, readiness probe, stop, process-group kill) and knows nothing
  about Claude Code, the gateway, or config — the same separation
  `internal/gateway` already keeps from `internal/runner` (the gateway is a
  generic loopback proxy; `internal/runner/gateway.go` is the glue that
  decides when to start one and what to point at it). *Veto alternative:*
  put it directly in `internal/runner`; rejected because process supervision
  with a readiness probe is reusable, testable machinery that has nothing
  runner-specific about it, and a separate package is easier to unit-test
  without a fake run.
- **P2. The readiness probe is a plain HTTP health check on the sidecar's
  own loopback port, not a fixed sleep.** Mirrors nothing existing exactly
  (the gateway needs no probe — it is ready the instant `Serve` is called
  in-process), but is the obvious, standard shape for supervising an
  external process that needs a moment to bind its port. *Veto
  alternative:* a fixed startup delay; rejected as flaky by construction on
  a loaded Cloud Run instance.
- **P3. `EnvSpec` (`internal/agent/env.go`) gains an optional
  `Compression *CompressionProxy{BaseURL string}` field, resolved and
  chained in front of `Gateway` when both are set, refused (an error from
  `BuildEnv`) when `Auth == "oauth"`.** This keeps the existing, already
  carefully specified precedence in `BuildEnv`/`GatewayVars` (`env.go:134-141`,
  "Last, so nothing declared or inherited can override the routing") as the
  single place routing is decided — `Compression`, when set, computes the
  final `ANTHROPIC_BASE_URL` as *its own* loopback URL, with the gateway's
  URL and token folded into whatever config Headroom reads to find its own
  upstream, rather than two independent env-var writers racing to set the
  same key. *Veto alternative:* have the runner skip the gateway's own
  `ANTHROPIC_BASE_URL` write entirely and set Headroom's URL via a
  completely separate code path; rejected because it would duplicate the
  "last writer wins, deliberately" comment's guarantee instead of extending it.
- **P4. The sidecar is started by the runner, once per run, before the first
  stage that needs it — not once per stage.** A run's stages share one
  Claude Code identity and one budget session already; starting a fresh
  Headroom process per stage would lose any token the sidecar prunes across
  the (already session-scoped) history mid-run and adds startup latency on
  every stage for no benefit this design can identify. *Veto alternative:*
  per-stage restart, for a stronger "always-fresh" guarantee against a
  misbehaving sidecar; rejected as unnecessary given Task 4's health
  monitoring already fails the stage (not silently continues) on a crash.

## Global Constraints

Every task's requirements include these.

From the design:
- `agent.compression` defaults to off; no behavior changes for a repository
  that never sets it (design §4's "default off", mirrored exactly from
  `named-oauth-tokens.md`'s "An installation that adds no `claude_accounts`
  entry is unaffected, in full").
- `agent.compression` set with `agent.auth: oauth` is refused by `Validate`
  until Task 10's live check (§1.1) settles the question; no task before
  Task 10 ships code that routes oauth traffic through the sidecar.
- The sidecar never receives outbound network access by default; Task 7's
  image check and a focused test in Task 2 both prove it needs none for an
  ordinary compression pass (design §2).
- A sidecar crash **fails the stage/run closed** (design H2) — no task adds
  a silent fallback to direct, uncompressed routing.
- `task.Spec`'s strict decoding stays strict; any new field this plan adds
  there is `omitempty` and gated by a version check before it is ever set to
  a non-default value (the same shape as `OAuthToken`,
  `named-oauth-tokens.md` §5).
- No live network call to Headroom's own infrastructure, Anthropic, or any
  cloud in any automated test. The fakes-only rule below covers this.

Project rules (carried over from this repository's existing plans):
- Every task's own test run is in the **foreground**, with `-count=1`, and
  `-race` where the package's existing tests use it.
- Fakes only in tests: a fake Headroom-shaped HTTP server (a `httptest`
  server returning canned responses and, for the bypass tests, echoing
  request bodies back so a test can assert what was and wasn't rewritten),
  `gcpfake` where infra is touched, no real `claude`, no real Headroom
  binary.
- Docs must match behavior: the docs task (Task 8) keeps every command and
  key this plan's docs mention real, the same pattern
  `plugin/skills_lint_test.go` and the `docs_*_test.go` family already
  enforce for every other feature.

## Review Focus (mutation-proof notes)

The failure modes most likely to hit an owner, each pinned by a test that
would fail if the fix were subtly wrong:

1. **A run with `agent.compression` unset behaves differently from today.**
   Pinned by `TestBuildEnvUnchangedWithNoCompression` (Task 3) and
   `TestRunnerStartsNoSidecarByDefault` (Task 4) — both assert zero new
   process starts, zero new env keys, byte-identical `ANTHROPIC_BASE_URL`
   handling to today's gateway-only path.
2. **`agent.compression` silently applies to an `oauth` workflow.** Pinned
   by `TestValidateRefusesCompressionWithOAuth` (Task 1) and
   `TestBuildEnvRefusesCompressionWithOAuth` (Task 3) — both assert a hard
   error, never a silent skip.
3. **A sidecar crash is swallowed instead of failing the stage.** Pinned by
   `TestRunnerFailsStageOnSidecarDeath` (Task 4), which kills the fake
   sidecar process mid-stage and asserts the stage ends `infra_error` (or
   the design's chosen failure status — the implementer confirms the exact
   `runstore.Status`/`Reason` shape against `internal/runner/gateway.go`'s
   existing halt path before choosing one), never a retry against the real
   API.
4. **The bypass list is not actually enforced** — a diff, a verify failure,
   or a review prompt gets sent through the compressor anyway. Pinned by
   `TestForwardNeverCompressesBypassedPaths` (Task 3 or wherever the
   chaining logic lives — the implementer locates it once Task 3's actual
   shape exists), using the fake Headroom server's request-echo to assert
   specific marker strings (a fake diff header, a fake `FAIL` test line)
   reach it unmodified in the request body Fugaro constructs before the
   sidecar would see it — this test can only prove what **Fugaro's own
   code** sends toward the sidecar unmodified; it cannot prove what the real
   Headroom binary does with it, which is why Task 10 keeps a live check for
   this too (see Task 10, item 3).
5. **An old runner or an old job image silently runs compression it
   predates.** Pinned by `TestOldConfigParserRefusesCompressionKey` (Task 1,
   the strict-decode version-skew test, the same shape as
   `TestOldRunnerRefusesOAuthTokenField`) and
   `TestCompressionImageGateRefusesOldBuild` (Task 6, mirroring
   `named-oauth-tokens.md`'s `TestRunTokenImageGateRefusesOldFirstRun`).

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `internal/headroomproc/headroomproc.go` (new), `headroomproc_test.go` (new) | Process supervision: `Start`, readiness probe, `Stop`/process-group kill, `URL()` | 2 |
| `internal/config/config.go`, `validate.go`, `schemas/fugaro.schema.json`, `testdata/config/*` | `Agent.Compression`, its enum, the oauth refusal, the corpus fixture | 1 |
| `internal/agent/env.go`, `env_test.go` | `EnvSpec.Compression`, the chaining/refusal logic in `BuildEnv`/`GatewayVars` | 3 |
| `internal/runner/gateway.go` (or a new `internal/runner/compression.go`), `runner.go`, their tests | Starting/stopping the sidecar per run, the fail-closed path, `gatewayOn`-adjacent helper | 4 |
| `internal/runstore/runstore.go`, `cost.go`, their tests, `internal/cli/diagnose.go` | Proposed `StageTiming` compression fields (coordinated with G20), the `Compression:` diagnose line | 5 |
| `internal/cli/run.go`, `run_test.go` | `--no-compression`/`--compression` override flags, `compressionSince` image gate | 6 |
| `images/base/Dockerfile`, `images/base/tools.tsv`, `images/base/bump.sh`, the base image's selftest | The pin, the presence row, the network-isolation smoke | 7 |
| `SECURITY.md`, a new user-facing doc (name TBD by the implementer — the repository's convention is one doc per shipped feature, e.g. `docs/compression.md`), `plugin/skills/*`, `internal/cli/docs_*_test.go` (new case) | The trust-model bullet, the user doc, skill mentions, docs tests | 8 |
| none | Full suite, release-notes task group | 9 |
| `docs/gcp-live-checklist.md` (results pasted in) | The owner's own runs (§1.1, §2, §6 of the design) | 10 |

## PR groups

Two PRs, in order: **PR 1 (Tasks 1-2)** is additive and inert — a new config
key nothing reads yet, and a standalone process-supervision package nothing
calls yet — safe to merge on its own, reviewable without touching the
credential path at all. **PR 2 (Tasks 3-9)** wires it up and is where the
security-sensitive review load concentrates (see the per-task flags below).
Task 10 is the owner's own run, never part of either PR, and gates whether
PR 2's feature is ever exercised by a real run (not whether it merges — the
code can merge inert-but-untested-live, the same posture
`named-oauth-tokens.md` took with its own U1-U4).

Parallelizable: Tasks 1 and 2 are independent and may be implemented
together. Task 3 needs 1 and 2. Task 4 needs 3. Task 5 is independent of 3/4
once 1 exists (it only adds record fields and a display line) but is most
useful once 4 actually populates them. Task 6 needs 1, 3 and 4. Task 7 needs
2 (to know what artifact is being pinned) but can start in parallel with
3-6 once the pin target is chosen. Task 8 needs 1-7 settled (it documents
the shipped shape). Task 9 is last.

**Review flag.** Tasks 2, 3 and 4 touch, respectively: a new process given
credentials-adjacent network access, the agent's credential-env allowlist
(`internal/agent/env.go`, the file `SECURITY.md` already names as in scope:
"the runner's use of provider and model tokens"), and the runner's halt
semantics. Each is flagged for review at high effort (Opus) specifically for
the security question — does this change widen what the agent, or anything
running alongside it, can reach or read — separate from an ordinary
correctness review of the same diff, the same split
`docs/design/layered-config.md` §17 draws for its own Task 4 and Task 13
("the second with a security reviewer, since they are the merge and the
trust boundary").

---

### Task 1: `config.Agent.Compression` and its validation

**Files:**
- Edit: `internal/config/config.go` (the `Agent` struct)
- Edit: `internal/config/validate.go`
- Edit: `schemas/fugaro.schema.json`
- New: a `testdata/config` fixture exercising the key (valid and the
  oauth-refusal case)
- Test: `internal/config/validate_test.go` (or wherever `Agent.OAuthToken`'s
  equivalent cases already live — the implementer greps
  `TestValidateAgentOAuthToken`-shaped names first)

**Interfaces:**
```go
// internal/config/config.go, in Agent, next to OAuthToken:

// Compression names the context-compression proxy this workflow's model
// calls route through: "" (off, the default) or "headroom"
// (docs/design/headroom-compression.md). Refused together with
// agent.auth: oauth until a live check settles whether Claude Code's
// ANTHROPIC_BASE_URL composes with CLAUDE_CODE_OAUTH_TOKEN (design §1.1).
// Binaries before compressionSince refuse this key.
Compression string `yaml:"compression,omitempty"`
```

- [ ] **Step 1: Write the failing tests**

```go
func TestValidateCompressionValue(t *testing.T) {
    for _, tc := range []struct{ val string; ok bool }{
        {"", true}, {"headroom", true}, {"bogus", false},
    } {
        cfg := validConfigFixture() // the package's existing minimal-valid builder
        cfg.Agent.Compression = tc.val
        ps := cfg.Validate()
        if (len(ps) == 0) != tc.ok {
            t.Errorf("Compression=%q: problems=%v, want ok=%v", tc.val, ps, tc.ok)
        }
    }
}

func TestValidateRefusesCompressionWithOAuth(t *testing.T) {
    cfg := validConfigFixture()
    cfg.Agent.Auth = "oauth"
    cfg.Agent.Compression = "headroom"
    ps := cfg.Validate()
    if len(ps) == 0 {
        t.Fatal("compression + oauth was accepted")
    }
}

func TestOldConfigParserRefusesCompressionKey(t *testing.T) {
    // Strict-decode a fugaro.yaml containing "agent: {compression: headroom}"
    // against a struct copy of Config from before this task (a git-blame
    // copy, not a re-derivation, the same discipline
    // TestOldRunnerRefusesOAuthTokenField used) and assert it errors.
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestValidateCompression|TestOldConfigParserRefusesCompressionKey' ./internal/config/`
Expected: FAIL, `undefined: Compression` or a passing-when-it-shouldn't result.

- [ ] **Step 3: Write the implementation** (the struct field, the `Validate`
  branch next to the existing `Agent.OAuthToken` shape check
  (`validate.go:146-151`), the schema enum, the corpus fixture)
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/config/... && go test -count=1 ./internal/config/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/validate.go internal/config/validate_test.go schemas/fugaro.schema.json testdata/config/
git commit -m "headroom compression task 1: agent.compression config key and its oauth refusal"
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

// Options configure a sidecar process.
type Options struct {
    Bin           string        // the pinned binary's path
    Args          []string      // the implementer confirms the real binary's flags once Task 7 pins one; a config file path is likely, not argv
    ReadyPath     string        // a health-check path on the sidecar's own HTTP server
    ReadyTimeout  time.Duration // how long Start waits for a 200 from ReadyPath
    Grace         time.Duration // SIGTERM -> SIGKILL grace on Stop, mirroring procgroup's shape
    Log           *slog.Logger
}

// Process is a running sidecar.
type Process struct { /* ... */ }

// Start launches the sidecar on an ephemeral loopback port, waits for its
// readiness probe, and returns a handle. It fails if the process exits
// before becoming ready, or ReadyTimeout elapses first.
func Start(ctx context.Context, o Options) (*Process, error)

// URL is the sidecar's own loopback base URL (its listening address),
// read from stdout/a discovery file/a fixed port — the implementer confirms
// which, once the real binary's own port-announcement behavior is known
// (UNVERIFIED in the design; this is a live-check item, Task 10).
func (p *Process) URL() string

// Dead returns a channel closed when the sidecar's process exits on its
// own (a crash), for the runner to select on (design H2, fail closed).
func (p *Process) Dead() <-chan struct{}

// Stop sends SIGTERM to the process group, escalating to SIGKILL after
// Grace, mirroring procgroup.Run's own shutdown shape.
func (p *Process) Stop(ctx context.Context) error
```

**Tests:**
```go
func TestStartWaitsForReadyThenReturns(t *testing.T) {
    // A fake binary (a tiny test helper program, or exec.Command("sh", "-c",
    // a script) that sleeps briefly then serves ReadyPath. Start must not
    // return before the probe succeeds.
}

func TestStartFailsIfProcessExitsBeforeReady(t *testing.T) {
    // A fake binary that exits 1 immediately. Start returns a non-nil error,
    // never a Process whose URL() nobody should call.
}

func TestDeadClosesOnCrash(t *testing.T) {
    // Start a fake binary, kill it externally (not via Stop), assert Dead()
    // closes within a bound.
}

func TestStopKillsTheWholeProcessGroup(t *testing.T) {
    // Mirrors procgroup's own "leaked daemon" test shape: the fake binary
    // forks a child that outlives a bare SIGTERM to the binary alone; Stop
    // must still reap it (process-group signal, not a single-PID one).
}

func TestNoNetworkNeededForReadiness(t *testing.T) {
    // Starts the fake binary with the test's own network namespace/process
    // given no route to anything but loopback (or, if that is impractical
    // in this repository's test environment, asserts the fake binary's own
    // recorded syscalls/connect attempts never target a non-loopback
    // address) — the automatable half of design §2's "no outbound network"
    // requirement; the live half (the real Headroom binary) is Task 10.
}
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestStart|TestDead|TestStop|TestNoNetwork' ./internal/headroomproc/`
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

### Task 3: `agent.EnvSpec` chains the sidecar in front of the gateway

**Flagged for a security-focused review (high effort, Opus):** this is the
agent's credential-env allowlist file, `internal/agent/env.go`, named
directly in `SECURITY.md`'s scope.

**Files:**
- Edit: `internal/agent/env.go`, `env_test.go`

**Interfaces:**
```go
// EnvSpec gains:
Compression *CompressionProxy // nil: off (today's behavior, unchanged)

// CompressionProxy is the sidecar Claude Code's calls route through first.
type CompressionProxy struct{ BaseURL string }
```

- `BuildEnv`'s existing `oauth` case (`env.go:106-109`) gains the refusal:
  `if spec.Compression != nil { return nil, nil, fmt.Errorf("auth: oauth
  cannot be compressed yet (docs/design/headroom-compression.md §1.1)") }`
  — a distinct error message from the existing gateway refusal, so a test
  (and a human reading a failed run) can tell which restriction fired.
- The "Last, so nothing declared or inherited can override the routing"
  block (`env.go:134-141`) gains a step **before** the existing
  `GatewayVars` call: when `spec.Compression != nil`, Fugaro sets
  `ANTHROPIC_BASE_URL` (and whatever else Headroom's own config surface
  needs — UNVERIFIED, a Task 10 live-check item) to `spec.Compression.BaseURL`
  instead of the gateway's URL, and separately tells the sidecar (via
  whatever local config file or env Headroom reads, not via the agent's own
  environment) where *its* upstream is: the gateway's URL and token when the
  gateway is also on, or the real upstream directly when it is not. The
  exact mechanism is a Task 10/implementation-time detail once Headroom's
  own configuration surface is read; this task's test only pins the
  contract from the agent's point of view (`ANTHROPIC_BASE_URL` is the
  sidecar's URL, never the gateway's, when both are set).

**Tests:**
```go
func TestBuildEnvUnchangedWithNoCompression(t *testing.T) {
    // spec.Compression nil: BuildEnv's output is byte-identical to today's
    // existing gateway-only test cases (golden comparison against the
    // existing TestBuildEnv* fixtures, not a new assertion written from
    // scratch — the point is zero behavior change).
}

func TestBuildEnvRefusesCompressionWithOAuth(t *testing.T) {
    _, _, err := BuildEnv(parentEnv, EnvSpec{Auth: "oauth", Compression: &CompressionProxy{BaseURL: "http://127.0.0.1:1"}})
    if err == nil {
        t.Fatal("oauth + compression was accepted")
    }
}

func TestBuildEnvRoutesThroughCompressionNotGateway(t *testing.T) {
    env, _, err := BuildEnv(parentEnv, EnvSpec{
        Auth: "api-key", Gateway: &Gateway{BaseURL: "http://127.0.0.1:9000", Token: "gwtok"},
        Compression: &CompressionProxy{BaseURL: "http://127.0.0.1:9100"},
    })
    // assert ANTHROPIC_BASE_URL == "http://127.0.0.1:9100" (the sidecar), not :9000
}
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestBuildEnv' ./internal/agent/`
Expected: FAIL (new cases; existing `TestBuildEnv*` cases in the same
pattern must still pass before this task's edits, confirming no regression
from the test-file edit alone).

- [ ] **Step 3: Write the implementation**
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/agent/... && go test -race -count=1 ./internal/agent/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/env.go internal/agent/env_test.go
git commit -m "headroom compression task 3: EnvSpec chains the sidecar in front of the gateway"
```

---

### Task 4: the runner starts, supervises and fails closed on the sidecar

**Flagged for a security-focused review (high effort, Opus):** touches
`internal/runner`'s halt semantics, the same code path `gatewayHalt`
(`internal/runner/gateway.go:248`) already occupies.

**Files:**
- Edit: `internal/runner/gateway.go` (or a new `internal/runner/compression.go`
  — the implementer's choice, following whichever keeps `startGateway`
  readable), `internal/runner/runner.go`, their tests

**Interfaces:**
- A new `(r *run) startCompression(ctx context.Context) error`, called once
  per run (P4), before the first `startGateway`/first stage, only when
  `r.cfg.Agent.Compression != ""`. It resolves the pinned binary's path
  (from `/etc/fugaro/base.json`'s pins, the same place Claude Code's own
  version is recorded, `docs/design/base-image.md` §3), calls
  `headroomproc.Start`, and on success sets `r.compression = &agent.CompressionProxy{BaseURL: p.URL()}`
  for `agent.BuildEnv` to pick up (Task 3).
- `r.compression`'s sidecar `Dead()` channel is selected alongside the
  gateway's `Halted()` channel wherever stages already watch for a halt
  (`internal/runner/gateway.go:215-248`'s pattern) — on death, the run halts
  with a new `runstore.HaltReason` (the implementer names it consistently
  with the existing reasons in `runstore.go:38-50`, something like
  `HaltCompressionDied`), never a silent continuation (design H2).
- `r.endRun`/wherever the sidecar's `Stop` is guaranteed even on a panicking
  stage — the implementer follows whatever defer/cleanup pattern
  `r.gw.Close()`'s own call site already uses, so a leaked sidecar process
  is exactly as impossible as a leaked gateway goroutine is today.

**Tests:**
```go
func TestRunnerStartsNoSidecarByDefault(t *testing.T) {
    // agent.compression unset: assert no headroomproc.Start call (inject a
    // fake constructor/interface the runner calls through, the same
    // dependency-injection shape r.d.* fields already use for the gateway
    // and Vertex token source).
}

func TestRunnerFailsStageOnSidecarDeath(t *testing.T) {
    // agent.compression set, the fake sidecar's Dead() channel closes
    // mid-stage: assert the stage/run ends in the new halt reason, the
    // fake backend never sees a retried call, and no env BuildEnv call
    // after the death falls back to Compression: nil.
}

func TestRunnerStopsSidecarOnRunEnd(t *testing.T) {
    // Normal run completion: assert Stop was called exactly once, even
    // when the run ends early (a halt, a cancelled context).
}
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestRunnerStartsNoSidecar|TestRunnerFailsStageOnSidecarDeath|TestRunnerStopsSidecarOnRunEnd' ./internal/runner/`
Expected: FAIL.

- [ ] **Step 3: Write the implementation**
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/runner/... && go test -race -count=1 ./internal/runner/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/runner/
git commit -m "headroom compression task 4: the runner starts, supervises and fails closed on the sidecar"
```

---

### Task 5: `result.json` compression stats and `fugaro diagnose`

**Files:**
- Edit: `internal/runstore/runstore.go`, `runstore_test.go`
- Edit: `internal/cli/diagnose.go`, `diagnose_test.go`

**Interfaces:**
- `StageTiming` gains, all `omitempty`:
  ```go
  TokensBefore int64 `json:"tokens_before,omitempty"`
  TokensAfter  int64 `json:"tokens_after,omitempty"`
  CompressionBypassed int `json:"compression_bypassed,omitempty"`
  ```
  Populated only when `agent.compression` was on for the stage and Headroom
  reports its own before/after counts (UNVERIFIED whether it does — if it
  does not, this task ships the fields unpopulated and the design's §3
  observability goal is only partially met until a later task, which this
  task's commit message says plainly rather than inventing numbers).
- `fugaro diagnose` gains a `Compression: headroom N% (before → after
  tokens)` line, printed only when a stage recorded these fields — the same
  "unchanged output for a repository that doesn't use this" discipline
  `named-oauth-tokens.md` Task 7 followed for `OAuthAccount`.

**Tests:**
```go
func TestStageTimingRoundTripsCompressionFields(t *testing.T) { /* marshal/unmarshal, omitempty when zero */ }

func TestDiagnoseShowsCompressionOnlyWhenPresent(t *testing.T) {
    // A record with no compression fields: diagnose output unchanged
    // (golden). A record with them set: diagnose gains exactly one new
    // line, asserted by substring.
}
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestStageTimingRoundTripsCompressionFields|TestDiagnoseShowsCompressionOnlyWhenPresent' ./internal/runstore/... ./internal/cli/`
Expected: FAIL.

- [ ] **Step 3: Write the implementation**
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/runstore/... ./internal/cli/... && go test -count=1 -run 'TestStageTimingRoundTripsCompressionFields|TestDiagnoseShowsCompressionOnlyWhenPresent' ./internal/runstore/... ./internal/cli/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/runstore/runstore.go internal/runstore/runstore_test.go internal/cli/diagnose.go internal/cli/diagnose_test.go
git commit -m "headroom compression task 5: result.json compression stats and fugaro diagnose"
```

---

### Task 6: `fugaro run --no-compression`/`--compression` and the image gate

**Files:**
- Edit: `internal/cli/run.go`, `run_test.go`

**Interfaces:**
- A new flag, tri-state in effect: unset (use `agent.compression` as
  configured), `--compression` (force on, refused if the repository's
  config does not allow `auth != oauth`, same refusal as Task 1's Validate
  rule, checked again here before any network call — the same "resolve and
  refuse before `Launch`" discipline `named-oauth-tokens.md`'s T7 used for
  `--token`), `--no-compression` (force off regardless of `fugaro.yaml`).
- A `compressionSince` constant and a `checkImageSince` call (mirroring
  `namedTokensSince`, `named-oauth-tokens.md` Task 6) that refuses launching
  a run whose resolved `agent.compression` is non-empty against a build
  record older than the feature, on every launch path (first run, follow-up,
  `--workflow`, `--batch`) — the same four-way test matrix
  `named-oauth-tokens.md` Task 6 wrote for its own gate
  (`TestRunTokenImageGateRefuses...`), adapted to this flag's name.

**Tests:**
```go
func TestRunNoCompressionOverridesConfig(t *testing.T) { /* fugaro.yaml sets compression: headroom; --no-compression; assert spec carries no compression */ }
func TestRunCompressionFlagRefusedWithOAuth(t *testing.T) { /* --compression on an oauth workflow: refused before Launch */ }
func TestCompressionImageGateRefusesOldBuild(t *testing.T) { /* mirrors named-oauth-tokens' own four cases */ }
```

- [ ] **Step 1: Write the failing tests**
- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test -count=1 -run 'TestRunNoCompression|TestRunCompressionFlagRefusedWithOAuth|TestCompressionImageGateRefusesOldBuild' ./internal/cli/`
Expected: FAIL.

- [ ] **Step 3: Write the implementation**
- [ ] **Step 4: Run the tests to verify they pass**

Run: `go vet ./internal/cli/... && go test -race -count=1 -run 'TestRunNoCompression|TestRunCompressionFlagRefusedWithOAuth|TestCompressionImageGateRefusesOldBuild' ./internal/cli/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/run.go internal/cli/run_test.go
git commit -m "headroom compression task 6: fugaro run --no-compression/--compression and the image gate"
```

---

### Task 7: the base image pins Headroom and verifies it needs no network

**A Fugaro run can write and unit-test the Go/TSV/Dockerfile-template bits
of this task but cannot build or scan an image** (Cloud Run has no Docker
daemon, the same limit `docs/design/base-image.md` §19 states for its own
Group 1) — CI's `images` job and the owner verify the actual build.

**Files:**
- Edit: `images/base/Dockerfile` (the pin: `ARG HEADROOM_VERSION=…`, `ARG
  HEADROOM_SHA256_AMD64=…`/`_ARM64=…` — or a note that no arm64 artifact
  exists yet, per design §5, in which case `agent.compression`'s launch-time
  check from Task 6 also refuses an arm64 job, a small addition to that
  task's Validate/launch logic this task's commit should cross-reference)
- Edit: `images/base/tools.tsv` (a `kit`-tier presence row)
- Edit: `images/base/bump.sh` (so the monthly bump resolves and re-verifies
  this pin like every other one)
- Edit: the base image's selftest / CI smoke to add a
  no-outbound-network check for this specific tool (design §2): start the
  pinned binary with `--network none` (the existing smoke harness) and
  confirm it still answers its own health/version check, proving it needs
  no network merely to exist — this is **not** a proof that an actual
  compression pass needs no network (that is Task 10's live check), only
  that the binary doesn't phone home just to start.

- [ ] **Step 1: Resolve the actual pinned artifact** (the implementer reads
  Headroom's own release page at this point — not before, since design §5's
  whole premise is that this is unverified today — and records the exact
  binary name, version and per-architecture sha256 the same way
  `bump.sh` already does for every other pin)
- [ ] **Step 2: Write the Dockerfile and tools.tsv changes**
- [ ] **Step 3: Local verification**

Run (foreground): `fugaro image build --local` on the base image kind,
`fugaro image selftest` against the result, as `docs/design/base-image.md`'s
own dogfood checklist (§15) already prescribes for new harness entries.
Expected: the new tool's presence row passes; size budget
(`images/size-budget.sh`) stays under its warning threshold, or the task's
commit message says explicitly that it does not and by how much.

- [ ] **Step 4: Commit**

```bash
git add images/base/Dockerfile images/base/tools.tsv images/base/bump.sh
git commit -m "headroom compression task 7: pin Headroom in the base image, verify it needs no network to start"
```

---

### Task 8: SECURITY.md, the user doc, skills, and docs tests

**Files:**
- Edit: `SECURITY.md` — one new bullet in "What one run's agent can reach"
  or a sibling section, stating plainly that with `agent.compression` on,
  Headroom sees every model call's full body before Anthropic does, runs in
  the same container and user as the agent, and (per design §2, once Task 7
  proves it) needs no outbound network to do so.
- New: a user-facing doc (`docs/compression.md`, or wherever this
  repository's convention for a shipped-feature doc points — the
  implementer checks against `docs/project-layer.md`'s and
  `docs/recipes.md`'s placement, the two closest precedents) covering
  `agent.compression`, the `--compression`/`--no-compression` flags, the
  oauth restriction, and a pointer to the design doc for the full threat
  model.
- Edit: `plugin/skills/routing/SKILL.md` or `plugin/skills/working/` (the
  implementer checks which skill, if any, should mention that compression
  exists and is off by default — likely a short note in the same family as
  the "Choosing a recipe" section `generic-tool.md` §4 added to `routing`,
  not a new skill).
- New/edit: a docs test in the `internal/cli/docs_*_test.go` family
  confirming the new doc's commands and keys are real, the same pattern
  every other feature's docs test already follows.

- [ ] **Step 1: Write the docs edits**
- [ ] **Step 2: Write/extend the docs test**
- [ ] **Step 3: Run the docs tests**

Run: `go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m`
Expected: `ok`.

- [ ] **Step 4: Commit**

```bash
git add SECURITY.md docs/compression.md plugin/skills/ internal/cli/docs_*_test.go
git commit -m "headroom compression task 8: SECURITY.md, the user doc, skill mentions, docs tests"
```

---

### Task 9: full suite and release-notes placement

- [ ] **Step 1: Full focused suite**

Run (foreground, split by package if any call would exceed 9 minutes):

```bash
go vet ./...
gofmt -l . # expect no output
go test -race -count=1 ./internal/config/... ./internal/agent/... ./internal/headroomproc/... ./internal/runner/... ./internal/runstore/... ./internal/cli/...
go test ./internal/cli -run 'Docs|Lint' -count=1 -timeout 9m
```

Expected: `ok` everywhere, no `gofmt` output.

- [ ] **Step 2: Release-notes task group**

Add this feature's section to whichever `docs/releases/vX.Y.Z.md` is open
for 0.8.0 (create it if nothing has yet, following `docs/releases/v0.6.5.md`'s
shape), naming `compressionSince`, the oauth restriction, the opt-in default,
and the three live checks of Task 10 as "verified before this was ever
enabled on a real run" — not as a claim that compression is recommended,
since §6's thresholds are a separate, owner-run gate this task does not
itself clear.

- [ ] **Step 3: Commit**

```bash
git commit -m "headroom compression task 9: full suite, release-notes placement"
```

---

### Task 10: owner-run live checks (not part of either PR; sandbox only)

Never run by an implementer or by CI. Matches this repository's existing
`docs/gcp-live-checklist.md` pattern (guardrails: `FUGARO_LIVE_GCP_PROJECT`/
`FUGARO_LIVE_REPO` only, ADC only, cleanup registered before any side
effect). Paste the `FACT:` lines into the PR that implements Task 3/4 (items
1-2) and into the design doc's own measurement section (item 3), and into
`docs/gcp-live-checklist.md` itself as new, numbered checks continuing the
existing sequence.

- [ ] **1. `ANTHROPIC_BASE_URL` with `CLAUDE_CODE_OAUTH_TOKEN`.** On a
  machine with a real `claude setup-token` value, run `claude -p` with both
  `ANTHROPIC_BASE_URL` pointed at a local, logging reverse proxy (a two-line
  script is enough — no Headroom binary needed for this check) and
  `CLAUDE_CODE_OAUTH_TOKEN` set, and record: does Claude Code's traffic
  actually reach the proxy, and does the proxy see the bearer token in a
  request header? This settles design §1.1's open question and directly
  decides whether Task 1's oauth refusal can ever be lifted.
- [ ] **2. No outbound network during an actual compression pass.** With the
  real, pinned Headroom binary (once Task 7 resolves one) started with no
  route to anything but loopback, run an ordinary sandbox task with
  `agent.compression: headroom` and confirm it completes normally. If it
  cannot — if Headroom needs a network call to do its job — record exactly
  what it tried to reach, and treat this as the design's stated
  "do not ship it" outcome (design §2) rather than a configuration problem
  to work around.
- [ ] **3. The §6 measurement protocol itself.** The paired A/B run over the
  owner's chosen task corpus, in the sandbox, with the design's thresholds
  applied to the results. This is the single largest live-check item in
  this plan and is explicitly the gate the design names for ever shipping
  compression as more than inert, untested code (design §6, §7).

**Restore.** Nothing persists beyond the sandbox's own runs and branches,
cleaned up the same way every other check in `docs/gcp-live-checklist.md`
already requires (declined PRs, deleted branches, no change to any shared
installation).
