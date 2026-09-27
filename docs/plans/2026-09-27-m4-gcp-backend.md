# M4 — GCP Backend and CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Tasks run in Cloud Run. `fugaro run` launches a task and can be repeated safely, `ls`, `logs`, `diagnose` and `cancel` rebuild the local view from cloud state alone, and runs restore and write back dependency caches under a branch lock. Every run reports what it cost, model plus compute, in `result.json`, the PR report and `ls`. `fugaro image build` submits the derived-image build to Cloud Build, and `auth: oauth` works end to end with a subscription token that never appears in argv, logs or chat. A throwaway bootstrap stands up just enough of `edge-devel-dimi` to prove all of this live against the Bitbucket sandbox, and then against `edgeappinc/edgeweb`.

**Architecture:**

```
                     ┌──────────── CLI side (laptop) ─────────────────────────────┐
localcfg ───────────►│ cli: run · ls · logs · diagnose · cancel · secrets · image │
runstore (+launch)──►│        │             │                                    │
runview (join, cost)►│        ▼             ▼                                    │
                     │   backend (seam) ◄── backend/gcp: Cloud Run v2, Logging,   │
                     │                      Secret Manager, Cloud Build, names,   │
                     │                      resource limits, price table         │
                     └────────────────────────────────────────────────────────────┘
                     ┌──────────── runner side (container) ───────────────────────┐
blobx (GCS preconditions) ─► lock (branch lock) ─┐                               │
cache (key, tar.zst, restore, writeback) ────────┼─► runner: bootstrap … writeback│
agent (event relay) ─────────────────────────────┘   + cost, execution, prices    │
                     └────────────────────────────────────────────────────────────┘
gcpfake: httptest fakes of GCS (JSON API), Run v2, Logging v2, Secret Manager v1, Cloud Build v1
```

- The CLI talks to the cloud only through `internal/backend` (design §3.4), and to the runs bucket through `gocloud.dev/blob`. `internal/backend/gcp` is the only implementation. It uses the REST clients in `google.golang.org/api` (`run/v2`, `logging/v2`, `secretmanager/v1`, `cloudbuild/v1`), which the module already has as an indirect dependency. They are plain HTTP, so `httptest` fakes stand in for them in every hermetic test, through `option.WithEndpoint`.
- The runner stays compute-agnostic. It gains a branch lock and caches, which are blob-only, plus a real `writeback` stage, the cost breakdown and a live relay of the agent's events to its log. The only GCS-specific code it touches is `internal/blobx`, which applies generation preconditions when the bucket is GCS and falls back to a documented, non-atomic compare-and-swap on `file://` and `mem://` buckets, which only local runs and tests use.
- Resource limits and prices belong to the backend (design §10.1, §14): `config.Validate` loses Cloud Run's CPU set, and `internal/backend/gcp` checks resources and ships the price table.
- Cloud resources for M4 come from a throwaway script, `deploy/bootstrap/gcp-m4.sh`. It prints every `gcloud` command and runs one step only with `--apply`. Terraform (M5) replaces it.

**Tech Stack:**
- Go 1.27 with the existing cobra, yaml.v3, doublestar and gocloud.dev dependencies
- **New direct dependencies:**
  - `google.golang.org/api`, already indirect, for the run/v2, logging/v2, secretmanager/v1 and cloudbuild/v1 REST clients and `option`
  - `cloud.google.com/go/storage`, already in the module graph through `gocloud.dev/blob/gcsblob`
  - `github.com/klauspost/compress/zstd`, for cache archives, pure Go
  - `golang.org/x/term`, for the hidden secret prompt
  - Each one is justified in the task that adds it. There is no gRPC Cloud client (`cloud.google.com/go/run` and the like): those can't be faked with `httptest`, and they would double the binary.
- `net/http/httptest` fakes, `memblob` and `fileblob`
- `gcloud`, bash and `docker` (through `.superpowers/heavy.sh`) for the bootstrap and the live runbook only

**Spec:** [docs/design/v1.md](../design/v1.md). Read these sections before starting:
- §3 (components, the job model, the bucket layout, the backend seam)
- §4.1 (bootstrap and writeback), §4.5 (cancel), §4.6 and §4.7 (the run record and recovery)
- §5.1 (resources, cache), §5.3 (task spec), §5.4 (local CLI config)
- §6 (security)
- §7.2 (Cloud Build)
- §8 (what Terraform will own, so the bootstrap doesn't drift from it)
- §9.1 (commands)
- §10 and §10.1 (logs and cost)
- §14 M4

The earlier plans explain the code this one changes: [M1](2026-09-26-m1-runner-core.md) (runner, runstore, agent), [M2](2026-09-27-m2-git-providers.md) (providers, PartialError), and [M3](2026-09-27-m3-images.md) (images, `cloudbuild.yaml`).

**Decisions already made (user):**
- **GCP:** project `edge-devel-dimi`, region `us-east5`. Billing is linked, with a 70 CAD/month budget alert.
- **Model auth:** `auth: oauth` with a Claude subscription token from `claude setup-token`, passed as `CLAUDE_CODE_OAUTH_TOKEN` and stored in Secret Manager through `fugaro secrets set`. Cost reports use `model_basis: subscription`.
- **Targets:**
  - The first real target is Bitbucket `edgeappinc/edgeweb`, base branch `master`, with no labels and one default reviewer, `{46e89d3a-40c4-4575-b922-d6727bd8ace6}`. That UUID goes only into EdgeWeb's own `fugaro.yaml`, never into engine code, tests or fixtures.
  - The EdgeWeb repository access token is at `~/.config/fugaro-edgeweb-token` (mode 600). It is scoped to that repository with Repositories: Write and Pull requests: Write, and has been checked against the API.
  - The live sandbox is `edgeappinc/fugarosandbox`, base branch `master`. Its repository access token is at `~/.config/fugaro-bb-token` (mode 600).
- **Infrastructure:** Terraform is M5. M4 gets a minimal, throwaway bootstrap. Any step that enables a billable API or creates a resource is run by the controller only after the user explicitly confirms it (**⚠ CONFIRM** in this plan).
- **Tests:** hermetic by default, with fakes. Live tests use the `live` build tag, touch only `edge-devel-dimi` and the sandbox repository, and clean up after themselves.

**Out of scope for M4:**
- **M5:** the Terraform module, `fugaro init`, the nightly Cloud Build trigger, the budget resource, and lifecycle rules on the bucket. The bootstrap sets lifecycle rules only as a stand-in.
- **M6:** follow-up runs (`run --pr`). `fugaro run` registers a hidden `--pr` that exits 1 with a pointer to M6.
- **M5 (review ruling):** the per-execution timeout override and local price overrides.
- **M7:** plugin skills other than `fugaro:onboard`.
- **Later:** Cloud Build images for GitHub repositories. The git credential in Cloud Build must be a static token, and a GitHub App mints short-lived ones, so M5 adds a token-minting step. In M4, `fugaro image build` for a GitHub repository exits 1 and says so.

## Global Constraints

- **Credentials come only from the environment or Secret Manager**, never from `fugaro.yaml`, the local config, argv, or a file the CLI writes.
  - `fugaro secrets set` reads a value only from stdin: a pipe, a redirect, or a hidden TTY prompt. It has no flag that takes a value.
  - No command prints a secret value, and no error message quotes one. Secret values pass through `agent.Redact` before any error is returned.
- **Redaction (M1/M2):** everything the runner publishes goes through the run's redactor, and that now includes the new agent-event relay and the cache and lock log lines.
- **The PartialError conservative direction (M2) applies to every new status the CLI derives.**
  - `ls`, `diagnose` and the join logic may only show `succeeded` when `result.json` says so.
  - An execution that ended without a final record is `infra_error`, never `succeeded`.
  - When `cancel` can't write the cancel marker, it doesn't hard-cancel the execution unless the user passed `--now`. Hard-cancelling skips finalize, and with it the draft PR.
- **The runner has the last word on ready vs draft (§4.2).**
  - The agent can call `gh pr ready` on GitHub, or the REST API on either provider, during a stage. Finalize's `EnsurePR` re-asserts the draft state on the existing PR anyway. Task 6 pins this with a test.
  - Nothing in M4 lets the agent's view of the PR decide the outcome.
- **Subprocesses** started by new code use `exec.CommandContext` with `cmd.WaitDelay = 5 * time.Second`, the same as `image.ExecRunner` and gitops. `procgroup` is for agent and verify commands only.
- **Docker-heavy work** runs through `.superpowers/heavy.sh <command…>`, which serializes it across parallel streams. That covers base-image builds and pushes, `go test -tags docker`, and `images/*.sh`. The script lives outside git (`.git/info/exclude`), in the main checkout. Call it by absolute path from worktrees: `/Users/dimi/git.lattica/Fugaro/.superpowers/heavy.sh`.
- **TDD:** each task writes the failing test first and runs it to see it fail. `go test ./...` never needs Docker, network or credentials.
  - Docker tests carry `//go:build docker`.
  - Live tests carry `//go:build live`. They name `edge-devel-dimi`, `us-east5` and `edgeappinc/fugarosandbox` as constants in the test file, and refuse to run against anything else.
- **Every test that runs git calls `testutil.IsolateGit(t)` first** (M1).
- **Every command supports `--json`. Exit codes:** 0 for ok, 1 for a user error, 2 for a remote failure (design §9.1). A GCP API error is exit 2, and so is a missing bucket object that should exist. A bad flag, an unknown run or a missing local config is exit 1.
- **No company-, repo- or resource-specific values in engine code, defaults or examples** (design §2).
  - `edge-devel-dimi`, `us-east5`, `edgeappinc/*` and the reviewer UUID appear only in the bootstrap runbook, the live-test constants and this plan.
  - Engine defaults are generic: the published price table, `max_parallel: 20`, `E2_HIGHCPU_8`.
- CI runs `gofmt -l`, `go vet ./...` and `go test -race ./...`. All three pass after every task.
- Code style follows M1–M3: exported identifiers have doc comments, and errors are wrapped with `%w` and context.
- **Naming contract (Task 1, used by the bootstrap, the CLI and M5):**
  - Cloud Run job: `fugaro-<repo-slug>-<workflow>`, sanitized to `[a-z0-9-]`, at most 63 characters, and hash-suffixed if longer
  - Service account ID: the same stem, at most 30 characters, and hash-suffixed if longer
  - Secret Manager ID: `fugaro-<repo-slug>-<logical-name>`
  - Image: `<registry>/<repo-slug>-<workflow>`, sanitized
  - `<repo-slug>` is `task.Slug(repo)`
  - The reserved logical secret names are `bitbucket-token` → `FUGARO_BITBUCKET_TOKEN`, `github-app-key` → `FUGARO_GITHUB_APP_PRIVATE_KEY`, `claude-oauth-token` → `CLAUDE_CODE_OAUTH_TOKEN`, and `anthropic-api-key` → `ANTHROPIC_API_KEY`. A workflow's `secrets:` may not use them.
- **Bucket layout additions** (design §3.3, edited in Task 19):
  - `runs/<slug>/<id>/launch.json`, written once with `IfNotExist`
  - `runs/<slug>/<id>/launching`, the launch claim, written with `IfNotExist`
  - `runs/<slug>/<id>/cancel` (exists since M1)
  - `locks/<slug>/<sha256(branch)[:16]>`
  - `cache/<slug>/<workflow>/<key>.tar.zst`
- **Environment on the Cloud Run job** (set by the bootstrap now and by Terraform in M5):
  - `FUGARO_BUCKET=gs://<runs-bucket>`
  - `FUGARO_BACKEND=cloud-run`
  - `FUGARO_PROJECT=<project>` and `FUGARO_REGION=<region>`, from which the runner builds its canonical execution name
  - the mounted secrets
  - Cloud Run itself sets `CLOUD_RUN_EXECUTION` (the short name) and `CLOUD_RUN_JOB`, and `jobs.run` sets `FUGARO_RUN=<slug>/<run-id>` per execution.
- **One execution name** (Task 1): the full resource name, `projects/<id>/locations/<region>/jobs/<job>/executions/<name>`, everywhere it is stored. It is compared only through `backend.ParseExecution(…).Key()` or `backend.SameExecution`, never as a string.

## Review Focus

These are the failure modes the design implies that are easiest to miss, most likely first. Each is pinned by a test in the task named.

1. **A launch that crashes or is repeated.**
   - The CLI can die between `jobs.run` and writing `launch.json`, or two agents can retry the same `--run-id` at once.
   - There must be exactly one execution. A repeated launch prints the existing run, and `--retry` refuses while a launch may still be in flight.
   - The claim is never deleted after a launch, a held claim always triggers a re-read of `launch.json`, and a stale claim is taken over with a generation-matched replace.
   - Task 10, deterministic through `launchHooks`: `TestLaunchWinnerFinishesBeforeLoserClaims`, `TestLaunchStaleClaimTakeoverHasOneWinner` and `TestLaunchRechecksAfterTakingTheClaim`. Also `TestRunIdempotentRunID`, `TestRetryBackfillsLaunchFromRecord` (with the name exactly as the runner records it), `TestRetryRefusesFreshClaim` and the smoke test `TestRunConcurrentSameRunID`.
2. **A second execution of the same run.**
   - Cloud Run, or a double launch that slipped through, may start two executions of one run.
   - The first record is created with create-if-absent, so the second execution finds it, exits, and writes nothing. It never overwrites `result.json` or steals the lock.
   - The same execution spelled with a project number is not a duplicate.
   - Defensive only: if the first execution's record vanished but its lock is held, the duplicate has written just its own first record, and stops there.
   - Task 6: `TestDuplicateExecutionWritesNothing`, `TestSameExecutionSpelledDifferentlyIsNotADuplicate`, `TestDuplicateExecutionLosesLock`.
3. **Secret values leaking through the new secrets path.**
   - A token piped in with a trailing newline must be stored without it.
   - The value must never appear in stdout, stderr, an error, or a request the CLI logs, and `--json` must not echo it.
   - A multi-line value is refused, except for `github-app-key`.
   - Task 14: `TestSecretsSetFromPipe`, `TestSecretsSetNeverEchoes`, `TestSecretsSetRejectsMultiline`.
4. **A hostile or broken cache archive.** Earlier runs of the same repository write the cache, and the agent can put anything under a cached path. A later restore must never write outside the declared path: no `..`, no absolute names, no symlink or hardlink escapes, no setuid bits, and no archive past the size cap. A corrupt archive is a warning, not a failed run. Restore also never writes through a symlink already in the root, because it goes through `os.Root`. Task 5: `TestRestoreRejectsTraversal`, `TestRestoreRejectsSymlinkEscape`, `TestRestoreRejectsWriteThroughExistingSymlink`, `TestRestoreCorruptArchiveIsWarning`, `TestSaveSkipsOversizedArchive`, `TestSaveSkipsEmptyRoots`.
5. **An execution that died without finalizing.** After an OOM kill, a task timeout or a node loss, `result.json` still says `running`. `ls` and `diagnose` must show `infra_error` ("execution ended without finalizing"), and the lock must expire rather than block the branch forever. The same goes for a `running` record whose execution the backend no longer knows, once its `deadline` has passed. Task 11: `TestJoinExecutionEndedWithoutFinalizing`, `TestJoinRunningPastDeadlineWithoutExecution`. Task 4: `TestAcquireTakesOverExpiredLock`.

Also pinned: `cancel` never hard-cancels a run in `finalize` or `writeback`, and counts `writeback` as finalized (Task 13 `TestCancelCountsWritebackAsFinalized`, `TestCancelNeverHardCancelsDuringFinalize`).

Also pinned, because the M4 brief calls it out: the agent marks the PR ready itself on a run that fails its tests, and finalize returns the PR to draft (Task 6 `TestAgentReadiedPRIsReturnedToDraft`).

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `internal/backend/backend.go` | the `Backend` seam, `Execution`, `State`, `Prices`, `MemoryGiB` | 1 |
| `internal/backend/gcp/names.go`, `limits.go` | the naming contract; Cloud Run's resource rules | 1 |
| `internal/config/validate.go`, `schemas/fugaro.schema.json` | the CPU set leaves core validation; reserved secret names | 1 |
| `internal/cli/compute.go` | runs the backend's resource checks in `validate` and `image` | 1 |
| `internal/localcfg/localcfg.go` | `~/.config/fugaro/config.yaml` (design §5.4) | 2 |
| `internal/task/task.go`, `schemas/task.schema.json` | the `batch` field | 3 |
| `internal/runstore/launch.go`, `list.go` | `launch.json`, the launch claim, `CreateTask`, `CreateRecord`, run listing and bare-ID lookup, `Record.Execution` and `Record.Deadline` | 3 |
| `internal/blobx/blobx.go` | GCS generation preconditions and custom time, with a local fallback | 4 |
| `internal/lock/lock.go` | the branch lock | 4 |
| `internal/gcpfake/{server,gcs}.go` | shared fake plumbing; the GCS JSON API fake | 4 |
| `internal/cache/{key,archive,cache}.go` | cache keys, safe tar.zst, restore and writeback | 5 |
| `internal/runner/{runner,lockcache}.go`, `internal/cli/exec.go`, `images/derived/Dockerfile.tmpl` | lock, caches, the writeback stage, duplicate executions, gs:// buckets | 6 |
| `internal/backend/gcp/prices.go`, `internal/runstore/cost.go`, `internal/runner/{cost,report}.go`, `schemas/result.schema.json` | cost reporting | 7 |
| `internal/agent/relay.go`, `internal/runner/runner.go` | the live agent-event relay | 8 |
| `internal/backend/gcp/{gcp,run,logs}.go`, `internal/gcpfake/{run,logging}.go` | Cloud Run v2 and Logging | 9 |
| `internal/cli/{cloud,run}.go` | shared cloud plumbing; `fugaro run` | 10 |
| `internal/runview/runview.go`, `internal/cli/ls.go` | the join, status, cost and totals; `fugaro ls` | 11 |
| `internal/cli/{logs,diagnose}.go` | `fugaro logs` and `fugaro diagnose` | 12 |
| `internal/cli/cancel.go` | `fugaro cancel` | 13 |
| `internal/backend/gcp/secrets.go`, `internal/gcpfake/secrets.go`, `internal/cli/secrets.go` | Secret Manager; `fugaro secrets set/ls` | 14 |
| `images/derived/cloudbuild.yaml`, `images/images.go`, `internal/backend/gcp/build.go`, `internal/gcpfake/build.go`, `internal/cli/image.go` | Cloud Build | 15 |
| `internal/e2e/cloud_test.go` | the hermetic run → exec → ls → logs → diagnose → cancel loop | 16 |
| `deploy/bootstrap/gcp-m4.sh`, `deploy/bootstrap/bootstrap_test.go`, `internal/cli/gcpcmd.go` | the throwaway bootstrap; hidden `fugaro gcp job-spec` | 17 |
| `internal/backend/gcp/live_test.go`, `internal/e2e/live_gcp_test.go` (`live` tag) | live checks and the live end-to-end | 18 |
| `docs/design/v1.md`, `docs/git-providers.md`, `docs/gcp-bootstrap.md` | design and docs edits | 19 |
| — (runbook, controller-run) | live bring-up in `edge-devel-dimi`, the sandbox, then EdgeWeb | 20 |

## Task dependency graph and parallelism

Real dependencies, meaning code a task calls or a test helper it reuses:

| Task | Depends on | Why |
|---|---|---|
| T1 backend seam, names, limits | — | |
| T2 localcfg | — | |
| T3 task batch, runstore | — | |
| T4 blobx, lock, GCS fake | — | owns `go.mod` in Wave A |
| T5 cache | T4 | `blobx.Bucket`; `go.mod` after T4 |
| T6 runner lock/cache | T1, T3, T4, T5 | `ExecID`, `CreateRecord`, `lock`, `cache` |
| T7 cost | T1, T3, T6 | `Prices`, `Record`; `runner.go` after T6 |
| T8 relay | T7 | `runner.go` after T7 (the `agent/relay.go` half has no dependency) |
| T9 gcp Run and Logging | T1, T4 | seam types; `gcpfake/server.go` |
| T10 `run` | T1, T2, T3, T4, T9 | `cloudEnv`, claim, `blobx.ReplaceIf`, `gcp.New` |
| T11 `ls` | T7, T10 | `NewCost`, `CostLine`; `cloudEnv`, `newCloudFixture` |
| T12 `logs`, `diagnose` | T11 | `loadRows`, `runview`, `seedRun` |
| T13 `cancel` | T12 | `locateLaunched`, `seedRun` |
| T14 secrets | T1, T2, T9, T10 | `gcp.Options`, `cloudEnv`, `newCloudFixture` |
| T15 Cloud Build | T1, T2, T9, T10 | `gcp.Options`, `cloudEnv`, `originRepo`, `newCloudFixture` |
| T16 hermetic e2e | T6–T13 | the whole loop, including T8's `/agent]` log lines |
| T17 bootstrap, `gcp job-spec` | T1, T2 | names, limits, local config |
| T18 live tests | T14–T17 | everything live |
| T19 design and docs | T15, T17 | records every ruling |
| T20 live runbook | all | |

```
T1 ──┬──────────────► T9 ──► T10 ──┬──► T11 ──► T12 ──► T13 ──┐
T4 ──┼─► T5 ─► T6 ─► T7 ─┬─► T8 ───┼───────────────────────────┼─► T16 ─► T18 ─► T20
T3 ──┘          ▲        └────────►┘ (T11 needs T7)            │    ▲
T2 ─────────────┼──► T17 ───────────────────────────────────────┼────┤
                │            T10 ──► T14, T15 ──────────────────┴────┘
                └ T1, T3 also feed T6          T19 after T15 and T17
```

**Waves:**
- **A:** T1, T2, T3, T4
- **B:** T5 (after T4), T9 (after T1, T4), T17 (after T1, T2)
- **C:** T6 (after T5), T10 (after T9)
- **D:** T7 (after T6), T14 and T15 (after T10)
- **E:** T8 (after T7), T11 (after T7 and T10)
- **F:** T12
- **G:** T13
- **H:** T16, then T18; T19 any time after T15 and T17; T20 last

If only one stream runs, go in numeric order: it respects every edge above.

## Parallel lanes

Three worktree lanes branch from `m4` and merge back into it at three points. Each lane runs its tasks in order and rebases on `m4` after each merge point.

| Lane | Branch | Tasks, in order |
|---|---|---|
| **A: GCP adapters and CLI** | `m4-a` | T1 → *M1* → T9 → T10 → *M2* → T11 → T12 → T13 → *M3* |
| **B: runner, cache and cost** | `m4-b` | T4 → *M1* → T5 → T6 → T7 → *M2* → T8 → *M3* |
| **C: side tasks** | `m4-c` | T2, T3 → *M1* → T17 → *M2* → T14 → T15 → T19 (draft) → *M3* |
| on `m4` | — | *M3* → T16 → T18 → T19 (final) → T20 |

**Merge points (into `m4`):**
- **M1:** T1, T2, T3 and T4. Everything in B and C after it needs `backend`, `localcfg`, `runstore` or `blobx`. Lane B's T5 can start before M1 on top of T4, since it needs only `blobx`, but rebases at M1.
- **M2:** T5, T6, T7 (B), T9, T10 (A) and T17 (C). Lane A's T11 needs T7; lane C's T14 and T15 need T9 and T10.
- **M3:** T8, T11–T13, T14 and T15. T16 needs the whole loop, T8's relay included.

**Files each task touches** (Create or Modify, tests included):

| Task | Files |
|---|---|
| T1 | `internal/backend/{backend,backend_test}.go`, `internal/backend/gcp/{names,limits}{,_test}.go`, `internal/config/{config,validate,config_test}.go`, `schemas/fugaro.schema.json`, `testdata/config/{valid,invalid}/…`, `internal/cli/{compute,validate,validate_test,image}.go` |
| T2 | `internal/localcfg/*` |
| T3 | `internal/task/{task,task_test}.go`, `schemas/task.schema.json`, `testdata/task/…`, `internal/runstore/{runstore,launch,list}{,_test}.go` |
| T4 | `internal/gcpfake/{server,gcs,gcs_test}.go`, `internal/blobx/*`, `internal/lock/*`, **`go.mod`/`go.sum`** |
| T5 | `internal/cache/*`, **`go.mod`/`go.sum`** |
| T6 | **`internal/runner/runner.go`**, `internal/runner/{lockcache,lockcache_test,runner_test}.go`, **`internal/cli/exec.go`**, `images/derived/Dockerfile.tmpl`, `internal/image/render_test.go` |
| T7 | `internal/backend/gcp/prices{,_test}.go`, `internal/runstore/{cost,cost_test,runstore}.go`, **`internal/runner/runner.go`**, `internal/runner/{cost,report,pure_test,runner_test}.go`, **`internal/cli/exec.go`**, `schemas/{result.schema.json,schemas_test.go}` |
| T8 | `internal/agent/relay{,_test}.go`, **`internal/runner/runner.go`**, `internal/runner/runner_test.go` |
| T9 | `internal/backend/gcp/{gcp,run,logs,run_test,logs_test}.go`, `internal/gcpfake/{run,logging}.go` |
| T10 | `internal/cli/{cloud,cloud_test,run,run_test}.go`, **`internal/cli/root.go`** |
| T11 | `internal/runview/*`, `internal/cli/{ls,ls_test}.go`, **`internal/cli/root.go`** |
| T12 | `internal/cli/{logs,logs_test,diagnose,diagnose_test}.go`, **`internal/cli/root.go`** |
| T13 | `internal/cli/{cancel,cancel_test}.go`, `internal/gcpfake/run.go`, **`internal/cli/root.go`** |
| T14 | `internal/backend/gcp/secrets{,_test}.go`, `internal/gcpfake/secrets.go`, `internal/cli/{secrets,secrets_test}.go`, **`internal/cli/root.go`**, **`go.mod`/`go.sum`** |
| T15 | `images/derived/cloudbuild.yaml`, `images/{images,cloudbuild_test}.go`, `internal/backend/gcp/build{,_test}.go`, `internal/gcpfake/build.go`, `internal/cli/{image,image_test}.go` |
| T16 | `internal/e2e/cloud_test.go` |
| T17 | `internal/cli/{gcpcmd,gcpcmd_test}.go`, **`internal/cli/root.go`**, `deploy/bootstrap/*` |
| T18 | `internal/backend/gcp/live_test.go`, `internal/e2e/live_gcp_test.go` |
| T19 | `docs/design/v1.md`, `docs/git-providers.md`, `docs/gcp-bootstrap.md`, `plugin/skills/onboard/SKILL.md`, maybe `README.md` |

**Hot files shared across lanes** (bold above), and how they're kept safe:
- **`internal/cli/root.go`:** T17 (C, before M2), T10 (A, before M2), then T11, T12, T13 (A) and T14 (C) after M2. Each adds exactly one `newXxxCmd()` to the `AddCommand` call. On conflict keep every line; the resolved list is sorted by task number.
- **`internal/runner/runner.go` and `internal/cli/exec.go`:** lane B only, in the order T6 → T7 → T8. No other lane touches them.
- **`go.mod`/`go.sum`:** T4 and T5 (B), then T14 (C, after M2). T9 adds no module: `google.golang.org/api` is already direct from T4. Resolve any conflict with `go mod tidy`, never by hand.
- **`internal/runstore/runstore.go`:** T3 (C, before M1), then T7 (B, after M1). The order is sequential through M1.
- **`internal/gcpfake/`:** T4 creates `server.go` (B, before M1). T9 and T13 own `run.go` and `logging.go` (A), T14 owns `secrets.go` and T15 owns `build.go` (C, after M2). No file is shared.
- **`internal/backend/gcp/`:** per-file ownership. T1 has `names.go` and `limits.go`, T7 `prices.go`, T9 `gcp.go`, `run.go` and `logs.go`, T14 `secrets.go`, T15 `build.go`. T14 and T15 depend on T9's `gcp.go` through M2.
- **`internal/cli/image.go`:** T1 (A, before M1) edits `loadCheckout`, and T15 (C, after M2) adds the cloud path.
- **`internal/cli/cloud_test.go`:** created by T10 (A). T11–T15 only call its helpers, and none edits it.
- **`schemas/`:** T1 (`fugaro.schema.json`), T3 (`task.schema.json`) and T7 (`result.schema.json`, `schemas_test.go`), all different files.

---

### Task 1: The backend seam, the GCP naming contract, and Cloud Run's resource rules moved out of config

**Files:**
- Create: `internal/backend/backend.go`, `internal/backend/backend_test.go`
- Create: `internal/backend/gcp/names.go`, `internal/backend/gcp/names_test.go`
- Create: `internal/backend/gcp/limits.go`, `internal/backend/gcp/limits_test.go`
- Modify: `internal/config/validate.go`: CPU is only `>= 1`; reject reserved logical secret names
- Modify: `internal/config/config.go`: add `ReservedSecrets`
- Modify: `internal/config/config_test.go`: the `cpu` case becomes `cpu: -1`, and add a reserved-name case
- Modify: `schemas/fugaro.schema.json`: `cpu` becomes `{"type":"integer","minimum":1}`, and the secret `name` gains `not: {enum: [...]}`
- Create: `testdata/config/valid/cpu-outside-cloud-run.yaml`, `testdata/config/invalid/reserved-secret-name.yaml`
- Create: `internal/cli/compute.go`
- Modify: `internal/cli/validate.go`, `internal/cli/image.go` (`loadCheckout`), `internal/cli/validate_test.go`

**Interfaces:**
- Consumes: `config.Resources`, `config.Problem`, `task.Slug`
- Produces:
  - `backend.State` with `StatePending`, `StateRunning`, `StateSucceeded`, `StateFailed` and `StateCancelled`, and `(State).Terminal() bool`
  - `backend.RepoRef{Repo, Slug string}`, `backend.LaunchSpec{Repo RepoRef; Workflow, RunID string}`, `backend.ExecutionRef{Name, Job, LogURL string}`
  - `backend.Execution{Name, Job string; State State; Created, Started, Completed time.Time; CPU, MemoryGiB float64; LogURL string}` with `(Execution).Billed(now time.Time) time.Duration`
  - `backend.ListFilter{Jobs []string; Since time.Time; ActiveOnly bool}`
  - `backend.LogQuery{Execution string; Since time.Time; Follow bool; Poll time.Duration}` and `backend.LogEntry{Time time.Time; Severity, InsertID, Message string; Fields map[string]any}`
  - the `backend.Backend` interface: `Launch`, `Execution`, `List`, `Logs` and `Cancel`
  - `backend.ErrNotFound`
  - `backend.ExecID{Project, Region, Job, Name string}`, `backend.ParseExecution(name string) (ExecID, bool)`, `(ExecID).Key() string` (`<region>/<job>/<name>`), `(ExecID).String() string`, `backend.SameExecution(a, b string) bool` and `backend.ExecutionFromEnv(getenv func(string) string) (string, error)`: the canonical execution name (C-1, below)
  - `backend.Prices{VCPUSecondUSD, GiBSecondUSD float64; Source string}` with `(Prices).ComputeUSD(cpu, memGiB float64, d time.Duration) float64`, and `backend.MemoryGiB(s string) (float64, error)`
  - `gcp.JobName(slug, workflow string) string`, `gcp.ServiceAccountID(slug, workflow string) string`, `gcp.SecretID(slug, logical string) string`, `gcp.ImageName(registry, slug, workflow string) string`
  - `gcp.CheckResources(r config.Resources) []gcp.Issue`, with `gcp.Issue{Field, Message string}` and `Field` either `"cpu"` or `"memory"`
  - `config.ReservedSecrets map[string]string`, from logical name to env var
  - `cli.computeProblems(cfg *config.Config) []config.Problem`

The seam differs from the design's §3.4 sketch in three places. Task 19 records them in the design.
- `Launch` takes a `LaunchSpec`, which groups the repository, workflow and run ID. A per-execution timeout override is deferred to M5.
- `Execution(name)` is added, because `cancel` and `diagnose` need one execution's state.
- `Logs` calls a function for each structured entry instead of writing to an `io.Writer`, because `--json` needs the fields.

**One execution name everywhere (C-1).**
- The canonical form is the full resource name, `projects/<project-id>/locations/<region>/jobs/<job>/executions/<name>`. It is what `launch.json`, `result.json` (`execution`), the lock holder and every backfill store.
- Cloud Run gives the runner only the short name (`CLOUD_RUN_EXECUTION`) and the job (`CLOUD_RUN_JOB`). `fugaro exec` builds the full name from those plus `FUGARO_PROJECT` and `FUGARO_REGION`, which the job's env carries (Task 6, Task 17).
- The API may answer with a project **number** instead of the ID. So identities are never compared as strings: every comparison and map key uses `ParseExecution(name).Key()`, which is region, job and short name, and ignores the project part.
- The GCP backend accepts any parseable form and rebuilds the name with its configured project ID before calling the API (Task 9).

- [ ] **Step 1: Write the failing tests**

`internal/backend/backend_test.go`:

```go
package backend

import (
	"math"
	"testing"
	"time"
)

func TestMemoryGiB(t *testing.T) {
	for in, want := range map[string]float64{"512Mi": 0.5, "8Gi": 8, "1024Mi": 1, "32Gi": 32} {
		got, err := MemoryGiB(in)
		if err != nil || got != want {
			t.Errorf("MemoryGiB(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "8G", "8GB", "Gi", "-1Gi", "0Mi"} {
		if _, err := MemoryGiB(bad); err == nil {
			t.Errorf("MemoryGiB(%q) accepted", bad)
		}
	}
}

func TestComputeUSD(t *testing.T) {
	p := Prices{VCPUSecondUSD: 0.000018, GiBSecondUSD: 0.000002}
	// 4 vCPU, 8 GiB for an hour: 3600 × (4×0.000018 + 8×0.000002) = $0.3168.
	if got := p.ComputeUSD(4, 8, time.Hour); math.Abs(got-0.3168) > 1e-9 {
		t.Fatalf("ComputeUSD = %v", got)
	}
	if got := p.ComputeUSD(4, 8, -time.Second); got != 0 {
		t.Fatalf("a negative duration costs %v", got)
	}
}

func TestParseExecution(t *testing.T) {
	id, ok := ParseExecution("projects/my-proj/locations/us-east5/jobs/fugaro-acme-app-web/executions/fugaro-acme-app-web-x7k2p")
	if !ok || id.Job != "fugaro-acme-app-web" || id.Name != "fugaro-acme-app-web-x7k2p" || id.Region != "us-east5" {
		t.Fatalf("id = %+v, %v", id, ok)
	}
	if id.String() != "projects/my-proj/locations/us-east5/jobs/fugaro-acme-app-web/executions/fugaro-acme-app-web-x7k2p" {
		t.Fatalf("String = %s", id.String())
	}
	// The API may answer with the project number: still the same execution.
	if !SameExecution(id.String(), "projects/123456789/locations/us-east5/jobs/fugaro-acme-app-web/executions/fugaro-acme-app-web-x7k2p") {
		t.Fatal("project ID vs number must not change identity")
	}
	for _, bad := range []string{"", "fugaro-acme-app-web-x7k2p", "projects/p/locations/r/jobs/j", "projects//locations/r/jobs/j/executions/e"} {
		if _, ok := ParseExecution(bad); ok {
			t.Errorf("ParseExecution(%q) accepted", bad)
		}
	}
}

func TestExecutionFromEnv(t *testing.T) {
	env := map[string]string{"CLOUD_RUN_EXECUTION": "fugaro-acme-app-web-x7k2p", "CLOUD_RUN_JOB": "fugaro-acme-app-web", "FUGARO_PROJECT": "my-proj", "FUGARO_REGION": "us-east5"}
	name, err := ExecutionFromEnv(func(k string) string { return env[k] })
	if err != nil || name != "projects/my-proj/locations/us-east5/jobs/fugaro-acme-app-web/executions/fugaro-acme-app-web-x7k2p" {
		t.Fatalf("name = %q, %v", name, err)
	}
	delete(env, "FUGARO_PROJECT")
	if _, err := ExecutionFromEnv(func(k string) string { return env[k] }); err == nil {
		t.Fatal("a missing FUGARO_PROJECT on Cloud Run was accepted")
	}
	if name, err := ExecutionFromEnv(func(string) string { return "" }); name != "" || err != nil {
		t.Fatalf("outside Cloud Run = %q, %v", name, err)
	}
}

func TestExecutionBilled(t *testing.T) {
	t0 := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	running := Execution{State: StateRunning, Started: t0}
	if got := running.Billed(t0.Add(90 * time.Second)); got != 90*time.Second {
		t.Fatalf("running Billed = %v", got)
	}
	done := Execution{State: StateSucceeded, Started: t0, Completed: t0.Add(time.Minute)}
	if got := done.Billed(t0.Add(time.Hour)); got != time.Minute {
		t.Fatalf("done Billed = %v", got)
	}
	if got := (Execution{State: StatePending}).Billed(t0); got != 0 {
		t.Fatalf("pending Billed = %v", got)
	}
	for s, terminal := range map[State]bool{StatePending: false, StateRunning: false, StateSucceeded: true, StateFailed: true, StateCancelled: true} {
		if s.Terminal() != terminal {
			t.Errorf("%s.Terminal() = %v", s, !terminal)
		}
	}
}
```

`internal/backend/gcp/names_test.go`:

```go
package gcp

import (
	"regexp"
	"strings"
	"testing"
)

var (
	jobNameRE = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)
	saIDRE    = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
)

func TestNames(t *testing.T) {
	if got := JobName("acme-app", "web"); got != "fugaro-acme-app-web" {
		t.Errorf("JobName = %q", got)
	}
	if got := ServiceAccountID("acme-app", "web"); got != "fugaro-acme-app-web" {
		t.Errorf("ServiceAccountID = %q", got)
	}
	if got := SecretID("acme-app", "bitbucket-token"); got != "fugaro-acme-app-bitbucket-token" {
		t.Errorf("SecretID = %q", got)
	}
	if got := ImageName("us-east5-docker.pkg.dev/p/fugaro", "acme-my.app", "web"); got != "us-east5-docker.pkg.dev/p/fugaro/acme-my-app-web" {
		t.Errorf("ImageName = %q", got)
	}
	// Storage slugs may hold '.' and '_'; Cloud Run names may not.
	if got := JobName("acme-my.app_x", "web"); got != "fugaro-acme-my-app-x-web" {
		t.Errorf("JobName sanitizes to %q", got)
	}
}

func TestNamesFitLimitsWithAStableHash(t *testing.T) {
	long := "someorganization-" + strings.Repeat("verylongrepositoryname", 3)
	a, b := JobName(long, "web"), JobName(long, "api")
	if len(a) > 63 || !jobNameRE.MatchString(a) || a == b || a != JobName(long, "web") {
		t.Fatalf("JobName(long) = %q / %q", a, b)
	}
	sa := ServiceAccountID("acmecorp-fugarosandbox", "web") // 35 characters untruncated
	if len(sa) != 30 || !saIDRE.MatchString(sa) || !strings.HasPrefix(sa, "fugaro-acmecorp-fugaro") {
		t.Fatalf("ServiceAccountID = %q (%d)", sa, len(sa))
	}
	if sa == ServiceAccountID("acmecorp-fugarosandbox", "api") {
		t.Fatal("hash suffix does not separate workflows")
	}
}
```

`internal/backend/gcp/limits_test.go`:

```go
package gcp

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

func TestCheckResources(t *testing.T) {
	ok := []config.Resources{{CPU: 1, Memory: "512Mi"}, {CPU: 4, Memory: "8Gi"}, {CPU: 8, Memory: "32Gi"}, {CPU: 4, Memory: "16Gi"}, {CPU: 2, Memory: "4Gi"}}
	for _, r := range ok {
		if issues := CheckResources(r); len(issues) > 0 {
			t.Errorf("%+v: %v", r, issues)
		}
	}
	bad := []struct {
		r            config.Resources
		field, words string
	}{
		{config.Resources{CPU: 3, Memory: "4Gi"}, "cpu", "one of 1, 2, 4, 6, 8"},
		{config.Resources{CPU: 16, Memory: "32Gi"}, "cpu", "one of 1, 2, 4, 6, 8"},
		{config.Resources{CPU: 8, Memory: "64Gi"}, "memory", "at most 32Gi"},
		{config.Resources{CPU: 1, Memory: "256Mi"}, "memory", "at least 512Mi"},
		{config.Resources{CPU: 1, Memory: "8Gi"}, "memory", "needs at least 2 CPUs"},
		{config.Resources{CPU: 2, Memory: "16Gi"}, "memory", "needs at least 4 CPUs"},
		{config.Resources{CPU: 4, Memory: "24Gi"}, "memory", "needs at least 6 CPUs"},
		{config.Resources{CPU: 6, Memory: "32Gi"}, "memory", "needs at least 8 CPUs"},
		{config.Resources{CPU: 4, Memory: "1Gi"}, "memory", "4 CPUs need at least 2Gi"},
		{config.Resources{CPU: 8, Memory: "2Gi"}, "memory", "8 CPUs need at least 4Gi"},
	}
	for _, tc := range bad {
		found := false
		for _, is := range CheckResources(tc.r) {
			found = found || (is.Field == tc.field && strings.Contains(is.Message, tc.words))
		}
		if !found {
			t.Errorf("%+v: want %s issue ~%q, got %v", tc.r, tc.field, tc.words, CheckResources(tc.r))
		}
	}
}
```

Add to `internal/cli/validate_test.go`. It uses the existing `execute`, `writeConfig` and `cliMinimalYAML`:

```go
func TestValidateReportsCloudRunLimits(t *testing.T) {
	cfg := strings.Replace(cliMinimalYAML, "base: web-node,", "base: web-node, resources: { cpu: 3, memory: 4Gi },", 1)
	out, _, err := execute(t, "validate", "--json", writeConfig(t, cfg))
	if ExitCode(err) != ExitUserError {
		t.Fatalf("exit %d (%v), output %s", ExitCode(err), err, out)
	}
	var got validateOutput
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Problems) != 1 || got.Problems[0].Path != "workflows.app.resources.cpu" || !strings.Contains(got.Problems[0].Message, "Cloud Run") {
		t.Fatalf("problems = %+v", got.Problems)
	}
}
```

In `internal/config/config_test.go`, replace the `cpu` case and add one:

```go
		{"cpu", minimalYAML + "    resources: { cpu: -1 }\n", "workflows.server.resources.cpu", "must be at least 1", 0},
		{"reserved secret", minimalYAML + "    secrets:\n      - { name: claude-oauth-token, env: TOK }\n", "workflows.server.secrets[0].name", "reserved", 0},
```

Use `cpu: -1`, not `0`: `applyDefaults` turns 0 into the base default.

`testdata/config/valid/cpu-outside-cloud-run.yaml`:

```yaml
# Core config accepts any positive CPU count; Cloud Run's set is checked by
# the backend (design §14), so this is valid here and flagged by fugaro validate.
version: 1
git: { provider: github }
workflows:
  web:
    base: web-node
    commands: { build: npm run build, test: npm test }
    resources: { cpu: 3, memory: 4Gi }
```

`testdata/config/invalid/reserved-secret-name.yaml`:

```yaml
version: 1
git: { provider: github }
workflows:
  web:
    base: web-node
    commands: { build: npm run build, test: npm test }
    secrets:
      - { name: bitbucket-token, env: MY_TOKEN }
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/backend/... ./internal/config/ ./schemas/ ./internal/cli/ -run 'MemoryGiB|ComputeUSD|ExecutionBilled|Names|CheckResources|TestParseProblems|Corpus|CloudRunLimits'`
Expected: FAIL. The new packages don't compile yet, the config `cpu` case expects the new message, and the corpus fails on `cpu-outside-cloud-run.yaml`, which the schema's enum and `Validate` still reject.

- [ ] **Step 3: Implement**

`internal/backend/backend.go`:

```go
// Package backend is the CLI's only seam to the compute platform (design
// §3.4). internal/backend/gcp implements it on Cloud Run; a later Batch
// backend is a sibling package. Storage is not part of the seam: every
// backend uses gocloud.dev/blob.
package backend

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// State is an execution's lifecycle state as the platform reports it.
type State string

const (
	StatePending   State = "pending"
	StateRunning   State = "running"
	StateSucceeded State = "succeeded" // the container exited 0; says nothing about the PR
	StateFailed    State = "failed"
	StateCancelled State = "cancelled"
)

// Terminal reports whether the execution has finished.
func (s State) Terminal() bool {
	return s == StateSucceeded || s == StateFailed || s == StateCancelled
}

// RepoRef names a repository: "owner/name" and its storage slug.
type RepoRef struct{ Repo, Slug string }

// LaunchSpec is one execution to start.
type LaunchSpec struct {
	Repo     RepoRef
	Workflow string
	RunID    string
}

// ExecID is an execution's identity, parsed from its resource name.
type ExecID struct{ Project, Region, Job, Name string }

// ParseExecution parses projects/<p>/locations/<r>/jobs/<j>/executions/<e>.
// <p> may be a project ID or number.
func ParseExecution(name string) (ExecID, bool) {
	f := strings.Split(name, "/")
	if len(f) != 8 || f[0] != "projects" || f[2] != "locations" || f[4] != "jobs" || f[6] != "executions" {
		return ExecID{}, false
	}
	for _, v := range []string{f[1], f[3], f[5], f[7]} {
		if v == "" {
			return ExecID{}, false
		}
	}
	return ExecID{Project: f[1], Region: f[3], Job: f[5], Name: f[7]}, true
}

// Key identifies the execution regardless of how the project is written.
// Compare executions by Key, never by raw name.
func (id ExecID) Key() string { return id.Region + "/" + id.Job + "/" + id.Name }

// String is the canonical full resource name.
func (id ExecID) String() string {
	return "projects/" + id.Project + "/locations/" + id.Region + "/jobs/" + id.Job + "/executions/" + id.Name
}

// ExecutionFromEnv is the canonical name of the Cloud Run execution this
// process is, or "" outside Cloud Run. Cloud Run sets CLOUD_RUN_EXECUTION
// (the short name) and CLOUD_RUN_JOB; the job sets FUGARO_PROJECT and
// FUGARO_REGION.
func ExecutionFromEnv(getenv func(string) string) (string, error) {
	short := getenv("CLOUD_RUN_EXECUTION")
	if short == "" {
		return "", nil
	}
	id := ExecID{Project: getenv("FUGARO_PROJECT"), Region: getenv("FUGARO_REGION"), Job: getenv("CLOUD_RUN_JOB"), Name: short}
	if id.Project == "" || id.Region == "" || id.Job == "" {
		return "", errors.New("on Cloud Run the job must set FUGARO_PROJECT and FUGARO_REGION (and Cloud Run sets CLOUD_RUN_JOB)")
	}
	return id.String(), nil
}

// SameExecution reports whether a and b name the same execution.
func SameExecution(a, b string) bool {
	x, ok1 := ParseExecution(a)
	y, ok2 := ParseExecution(b)
	return ok1 && ok2 && x.Key() == y.Key()
}

// ExecutionRef identifies a started execution.
type ExecutionRef struct {
	Name   string // the platform's full resource name
	Job    string
	LogURL string
}

// Execution is one execution as the platform reports it.
type Execution struct {
	Name, Job                   string
	State                       State
	Created, Started, Completed time.Time
	CPU, MemoryGiB              float64 // the task's limits, for compute cost
	LogURL                      string
}

// Billed is how long the execution has run: start to completion, or to now
// while it runs; zero before it starts.
func (e Execution) Billed(now time.Time) time.Duration {
	if e.Started.IsZero() {
		return 0
	}
	end := e.Completed
	if end.IsZero() {
		end = now
	}
	return max(0, end.Sub(e.Started))
}

// ListFilter narrows List.
type ListFilter struct {
	Jobs       []string  // job names; empty means every Fugaro job
	Since      time.Time // executions created at or after this
	ActiveOnly bool      // only pending and running executions
}

// LogQuery selects one execution's log entries.
type LogQuery struct {
	Execution string
	Since     time.Time
	Follow    bool          // keep polling until the execution ends and its logs settle
	Poll      time.Duration // follow's poll interval; zero means the backend's default
}

// LogEntry is one structured log line.
type LogEntry struct {
	Time     time.Time
	Severity string
	InsertID string
	Message  string
	Fields   map[string]any // the JSON payload, including run_id, stage and stream
}

// Backend launches and inspects executions.
type Backend interface {
	Launch(ctx context.Context, spec LaunchSpec) (ExecutionRef, error)
	Execution(ctx context.Context, name string) (Execution, error)
	List(ctx context.Context, f ListFilter) ([]Execution, error)
	Logs(ctx context.Context, q LogQuery, fn func(LogEntry) error) error
	Cancel(ctx context.Context, name string) error
}

// ErrNotFound means the execution does not exist.
var ErrNotFound = errors.New("not found")

// Prices are compute list prices per second (design §10.1).
type Prices struct {
	VCPUSecondUSD float64
	GiBSecondUSD  float64
	Source        string // where the figures come from, for reports
}

// ComputeUSD is the cost of cpu vCPUs and memGiB GiB for d.
func (p Prices) ComputeUSD(cpu, memGiB float64, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return d.Seconds() * (cpu*p.VCPUSecondUSD + memGiB*p.GiBSecondUSD)
}

var memoryRE = regexp.MustCompile(`^([1-9][0-9]*)(Mi|Gi)$`)

// MemoryGiB parses a Kubernetes-style quantity ("512Mi", "8Gi") into GiB.
func MemoryGiB(s string) (float64, error) {
	m := memoryRE.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("memory %q must look like 512Mi or 8Gi", s)
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, err
	}
	if m[2] == "Mi" {
		n /= 1024
	}
	return n, nil
}
```

`internal/backend/gcp/names.go`:

```go
// Package gcp implements the backend on Google Cloud: Cloud Run jobs,
// Cloud Logging, Secret Manager and Cloud Build (design §3.4, §8).
package gcp

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// Name limits: Cloud Run job names are at most 63 characters; service
// account IDs 6 to 30.
const (
	maxJobName = 63
	maxSAID    = 30
)

var unsafeNameRE = regexp.MustCompile(`[^a-z0-9-]+`)

func sanitize(s string) string {
	s = unsafeNameRE.ReplaceAllString(strings.ToLower(s), "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

// fit returns name, or when it is longer than limit, its first characters
// and a 6-hex hash of the full name, so distinct long names stay distinct.
func fit(name string, limit int) string {
	if len(name) <= limit {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	return strings.TrimRight(name[:limit-7], "-") + "-" + hex.EncodeToString(sum[:3])
}

func stem(slug, workflow string) string { return sanitize("fugaro-" + slug + "-" + workflow) }

// JobName is the Cloud Run job of (slug, workflow) (design §3.2).
func JobName(slug, workflow string) string { return fit(stem(slug, workflow), maxJobName) }

// ServiceAccountID is the job's dedicated service account ID (design §6.1).
func ServiceAccountID(slug, workflow string) string { return fit(stem(slug, workflow), maxSAID) }

// SecretID is the Secret Manager secret holding a repository's logical
// secret (a workflow secret's name, or one of config.ReservedSecrets).
func SecretID(slug, logical string) string { return sanitize("fugaro-" + slug + "-" + logical) }

// ImageName is the derived image of (slug, workflow) in registry, untagged.
func ImageName(registry, slug, workflow string) string {
	return strings.TrimSuffix(registry, "/") + "/" + sanitize(slug+"-"+workflow)
}
```

`internal/backend/gcp/limits.go`:

```go
package gcp

import (
	"fmt"
	"slices"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/config"
)

// Issue is one way a workflow's resources don't fit Cloud Run.
type Issue struct{ Field, Message string }

// Cloud Run job task limits, from cloud.google.com/run/docs/configuring/jobs/cpu
// and .../jobs/memory-limits (checked 2026-09-27). Memory above each
// threshold needs at least that many CPUs, and 4 or more CPUs need a floor.
var (
	cpuChoices = []int{1, 2, 4, 6, 8}
	memNeedCPU = []struct {
		overGiB float64
		cpu     int
	}{{24, 8}, {16, 6}, {8, 4}, {4, 2}}
	cpuNeedMem = map[int]float64{4: 2, 6: 4, 8: 4}
)

// CheckResources reports why r cannot be a Cloud Run job task (design §14:
// the core schema only requires a positive CPU count and a memory quantity).
func CheckResources(r config.Resources) []Issue {
	var out []Issue
	if !slices.Contains(cpuChoices, r.CPU) {
		out = append(out, Issue{"cpu", "Cloud Run jobs need one of 1, 2, 4, 6, 8 CPUs"})
	}
	gib, err := backend.MemoryGiB(r.Memory)
	switch {
	case err != nil:
		out = append(out, Issue{"memory", err.Error()})
	case gib > 32:
		out = append(out, Issue{"memory", "Cloud Run jobs allow at most 32Gi"})
	case gib < 0.5:
		out = append(out, Issue{"memory", "Cloud Run jobs need at least 512Mi"})
	default:
		for _, t := range memNeedCPU {
			if gib > t.overGiB && r.CPU < t.cpu {
				out = append(out, Issue{"memory", fmt.Sprintf("%s on Cloud Run needs at least %d CPUs", r.Memory, t.cpu)})
				break
			}
		}
		if floor, ok := cpuNeedMem[r.CPU]; ok && gib < floor {
			out = append(out, Issue{"memory", fmt.Sprintf("on Cloud Run, %d CPUs need at least %gGi", r.CPU, floor)})
		}
	}
	return out
}
```

Before committing, fetch both Cloud Run pages named in the comment (WebFetch) and confirm the CPU set and the thresholds. If they differ, change the tables and the test cases together, and note the difference in the commit body.

`internal/config/config.go`, next to `Secret`:

```go
// ReservedSecrets are the logical secret names the platform itself mounts,
// mapped to the variable each one becomes. A workflow's secrets may not
// reuse them (design §5.1, §6.1).
var ReservedSecrets = map[string]string{
	"bitbucket-token":    "FUGARO_BITBUCKET_TOKEN",
	"github-app-key":     "FUGARO_GITHUB_APP_PRIVATE_KEY",
	"claude-oauth-token": "CLAUDE_CODE_OAUTH_TOKEN",
	"anthropic-api-key":  "ANTHROPIC_API_KEY",
}
```

`internal/config/validate.go`:
- Replace the `if !slices.Contains([]int{1, 2, 4, 6, 8}, w.Resources.CPU)` block with a check that CPU is at least 1:

```go
		if w.Resources.CPU < 1 {
			add(p+".resources.cpu", "must be at least 1 (the compute backend checks its own limits)")
		}
```

- In the secrets loop, after the name regexp check, add:

```go
			if _, reserved := ReservedSecrets[s.Name]; reserved {
				add(sp+".name", "%s is reserved for the platform's own secrets", s.Name)
			}
```

`schemas/fugaro.schema.json`:
- `"cpu": { "type": "integer", "minimum": 1 }`
- The secret `name` gains `"not": { "enum": ["bitbucket-token", "github-app-key", "claude-oauth-token", "anthropic-api-key"] }`.

`internal/cli/compute.go`:

```go
package cli

import (
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
)

// computeProblems checks each workflow against its compute backend's limits.
// Every workflow runs on Cloud Run until workflows.<name>.compute exists
// (design §14), so this is gcp.CheckResources for all of them.
func computeProblems(cfg *config.Config) []config.Problem {
	var ps []config.Problem
	for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
		for _, is := range gcp.CheckResources(cfg.Workflows[name].Resources) {
			ps = append(ps, config.Problem{Path: "workflows." + name + ".resources." + is.Field, Message: is.Message})
		}
	}
	return ps
}
```

Add `"maps"` and `"slices"` to its imports. In `validate.go` and in `loadCheckout`, change `problems = config.Check(cfg, …)` to `problems = append(config.Check(cfg, …), computeProblems(cfg)...)`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/backend/... ./internal/config/ ./schemas/ ./internal/cli/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/backend internal/config schemas testdata/config internal/cli/compute.go internal/cli/validate.go internal/cli/validate_test.go internal/cli/image.go
git commit -m "backend: add the backend seam and GCP naming; move Cloud Run resource rules out of config"
```

---

### Task 2: The local CLI config (`~/.config/fugaro/config.yaml`)

**Files:**
- Create: `internal/localcfg/localcfg.go`, `internal/localcfg/localcfg_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks
- Produces:
  - `localcfg.Config` with the fields below, plus `localcfg.Build`, `localcfg.Endpoints` and `localcfg.Repo`. There is no `prices:` override in M4: compute uses the backend's list prices, and the override arrives with M5's `fugaro init`
  - `localcfg.Path(getenv func(string) string) (string, error)`
  - `localcfg.Load(path string) (*Config, error)`, and `localcfg.ErrMissing`
  - `localcfg.Parse(data []byte) (*Config, error)`
  - `(*Config).Override(project, region string)`
  - `(*Config).BucketURL() string`, `(*Config).BuildRegion() string`
  - `(*Config).Me(ctx context.Context) (string, error)`
  - `(*Config).Marshal() ([]byte, error)`

The file (design §5.4, the full schema recorded in Task 19):

```yaml
version: 1
project: my-project                   # GCP project ID
region: us-central1
runs_bucket: my-project-fugaro-runs   # bucket name; the CLI and the jobs use gs://<runs_bucket>
bucket_url: ""                        # optional override, such as file:///tmp/runs, for tests
registry: us-central1-docker.pkg.dev/my-project/fugaro   # Artifact Registry repository for derived images
base_image: ""                        # optional; overrides the published base for image build
build:
  service_account: fugaro-build@my-project.iam.gserviceaccount.com
  machine_type: E2_HIGHCPU_8          # default
  region: ""                          # default: region
user: ""                              # requested_by for run and ls --mine; default git config user.email
max_parallel: 20
endpoints:                            # optional API root overrides, for fakes and emulators
  run: ""
  logging: ""
  secret_manager: ""
  cloud_build: ""
  no_auth: false
repos:
  acme/web:
    base_branch: main
    workflows: [web]
```

- [ ] **Step 1: Write the failing test** — `internal/localcfg/localcfg_test.go`

```go
package localcfg

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

const sample = `version: 1
project: my-project
region: us-central1
runs_bucket: my-runs
registry: us-central1-docker.pkg.dev/my-project/fugaro
repos:
  acme/web: { base_branch: main, workflows: [web] }
`

func TestParseDefaults(t *testing.T) {
	c, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxParallel != 20 || c.Build.MachineType != "E2_HIGHCPU_8" || c.BucketURL() != "gs://my-runs" || c.BuildRegion() != "us-central1" {
		t.Fatalf("defaults = %+v", c)
	}
	if r := c.Repos["acme/web"]; r.BaseBranch != "main" || len(r.Workflows) != 1 {
		t.Fatalf("repo = %+v", r)
	}
	c.Override("other-project", "europe-west1")
	if c.Project != "other-project" || c.Region != "europe-west1" || c.BuildRegion() != "europe-west1" {
		t.Fatalf("override = %+v", c)
	}
}

func TestParseRejects(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, msg string }{
		"unknown field": {sample + "colour: blue\n", "colour"},
		"bad project":   {strings.Replace(sample, "my-project\n", "My_Project\n", 1), "project"},
		"no bucket":     {strings.Replace(sample, "runs_bucket: my-runs\n", "", 1), "runs_bucket"},
		"bad repo":      {sample + "  nope: { workflows: [web] }\n", "owner/name"},
		"bad workflow":  {sample + "  acme/api: { workflows: [Web] }\n", "workflow"},
		"bad parallel":  {sample + "max_parallel: -1\n", "max_parallel"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.yaml)); err == nil || !strings.Contains(err.Error(), tc.msg) {
				t.Fatalf("err = %v, want ~%q", err, tc.msg)
			}
		})
	}
}

func TestPathAndLoad(t *testing.T) {
	dir := t.TempDir()
	env := map[string]string{"HOME": dir}
	get := func(k string) string { return env[k] }
	p, err := Path(get)
	if err != nil || p != filepath.Join(dir, ".config", "fugaro", "config.yaml") {
		t.Fatalf("Path = %q, %v", p, err)
	}
	env["XDG_CONFIG_HOME"] = filepath.Join(dir, "xdg")
	if p, _ = Path(get); p != filepath.Join(dir, "xdg", "fugaro", "config.yaml") {
		t.Fatalf("XDG Path = %q", p)
	}
	env["FUGARO_CONFIG"] = filepath.Join(dir, "explicit.yaml")
	if p, _ = Path(get); p != env["FUGARO_CONFIG"] {
		t.Fatalf("FUGARO_CONFIG Path = %q", p)
	}
	if _, err := Load(p); !errors.Is(err, ErrMissing) || !strings.Contains(err.Error(), "bootstrap") {
		t.Fatalf("missing file: %v", err)
	}
	if err := os.WriteFile(p, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil || c.Project != "my-project" {
		t.Fatalf("Load = %+v, %v", c, err)
	}
	data, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if again, err := Parse(data); err != nil || again.RunsBucket != "my-runs" {
		t.Fatalf("round trip = %+v, %v", again, err)
	}
}

func TestMe(t *testing.T) {
	testutil.IsolateGit(t)
	c, _ := Parse([]byte(sample + "user: someone@example.com\n"))
	if me, err := c.Me(context.Background()); err != nil || me != "someone@example.com" {
		t.Fatalf("Me = %q, %v", me, err)
	}
	c, _ = Parse([]byte(sample))
	testutil.Git(t, "", "config", "--global", "user.email", "git@example.com")
	if me, err := c.Me(context.Background()); err != nil || me != "git@example.com" {
		t.Fatalf("Me from git = %q, %v", me, err)
	}
}
```

If `testutil.Git` needs a directory, pass `t.TempDir()`. `IsolateGit` gives each test its own global config, so `--global` stays inside the test.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/localcfg/`
Expected: FAIL (the package does not exist yet)

- [ ] **Step 3: Implement** — `internal/localcfg/localcfg.go`

```go
// Package localcfg reads the local CLI config, ~/.config/fugaro/config.yaml
// (design §5.4): the installation's project, region and buckets, and the
// onboarded repositories. M4's bootstrap script writes it; M5's fugaro init
// takes over.
package localcfg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the local CLI config.
type Config struct {
	Version     int             `yaml:"version"`
	Project     string          `yaml:"project"`
	Region      string          `yaml:"region"`
	RunsBucket  string          `yaml:"runs_bucket"`
	Bucket      string          `yaml:"bucket_url,omitempty"`
	Registry    string          `yaml:"registry,omitempty"`
	BaseImage   string          `yaml:"base_image,omitempty"`
	Build       Build           `yaml:"build"`
	User        string          `yaml:"user,omitempty"`
	MaxParallel int             `yaml:"max_parallel"`
	Endpoints   Endpoints       `yaml:"endpoints,omitempty"`
	Repos       map[string]Repo `yaml:"repos"`
}

// Build configures Cloud Build submissions.
type Build struct {
	ServiceAccount string `yaml:"service_account,omitempty"`
	MachineType    string `yaml:"machine_type"`
	Region         string `yaml:"region,omitempty"`
}

// Endpoints override API roots, for fakes and emulators.
type Endpoints struct {
	Run           string `yaml:"run,omitempty"`
	Logging       string `yaml:"logging,omitempty"`
	SecretManager string `yaml:"secret_manager,omitempty"`
	CloudBuild    string `yaml:"cloud_build,omitempty"`
	NoAuth        bool   `yaml:"no_auth,omitempty"` // send no credentials (fakes only)
}

// Repo is an onboarded repository.
type Repo struct {
	BaseBranch string   `yaml:"base_branch,omitempty"`
	Workflows  []string `yaml:"workflows"`
}

// ErrMissing means there is no local config yet.
var ErrMissing = errors.New("no local fugaro config")

var (
	projectRE  = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)
	regionRE   = regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]+$`)
	bucketRE   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,61}[a-z0-9]$`)
	repoRE     = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)
	workflowRE = regexp.MustCompile(`^[a-z][a-z0-9-]{0,19}$`)
)

// Path is where the config lives: $FUGARO_CONFIG, else
// $XDG_CONFIG_HOME/fugaro/config.yaml, else ~/.config/fugaro/config.yaml.
func Path(getenv func(string) string) (string, error) {
	if p := getenv("FUGARO_CONFIG"); p != "" {
		return p, nil
	}
	if x := getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "fugaro", "config.yaml"), nil
	}
	home := getenv("HOME")
	if home == "" {
		return "", errors.New("HOME is not set; set FUGARO_CONFIG to the local config file")
	}
	return filepath.Join(home, ".config", "fugaro", "config.yaml"), nil
}

// Load reads and validates the config at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w at %s: create it with deploy/bootstrap/gcp-m4.sh config (fugaro init replaces it in M5)", ErrMissing, path)
	}
	if err != nil {
		return nil, err
	}
	c, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Parse decodes the config strictly, applies defaults and validates it.
func Parse(data []byte) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("local config: %w", err)
	}
	if c.Version == 0 {
		c.Version = 1
	}
	if c.MaxParallel == 0 {
		c.MaxParallel = 20
	}
	if c.Build.MachineType == "" {
		c.Build.MachineType = "E2_HIGHCPU_8"
	}
	return &c, c.validate()
}

func (c *Config) validate() error {
	var errs []error
	bad := func(f string, a ...any) { errs = append(errs, fmt.Errorf("local config: "+f, a...)) }
	if c.Version != 1 {
		bad("version must be 1")
	}
	if !projectRE.MatchString(c.Project) {
		bad("project %q is not a GCP project ID", c.Project)
	}
	if !regionRE.MatchString(c.Region) {
		bad("region %q is not a region such as us-central1", c.Region)
	}
	if c.Bucket == "" && !bucketRE.MatchString(c.RunsBucket) {
		bad("runs_bucket %q is not a bucket name (or set bucket_url)", c.RunsBucket)
	}
	if c.MaxParallel < 1 {
		bad("max_parallel must be at least 1")
	}
	for repo, r := range c.Repos {
		if !repoRE.MatchString(repo) {
			bad("repos: %q must look like owner/name", repo)
		}
		for _, w := range r.Workflows {
			if !workflowRE.MatchString(w) {
				bad("repos.%s: workflow %q is not a workflow name", repo, w)
			}
		}
	}
	return errors.Join(errs...)
}

// Override applies --project and --region when they are set.
func (c *Config) Override(project, region string) {
	if project != "" {
		c.Project = project
	}
	if region != "" {
		c.Region = region
	}
}

// BucketURL is the runs bucket as a gocloud URL.
func (c *Config) BucketURL() string {
	if c.Bucket != "" {
		return c.Bucket
	}
	return "gs://" + c.RunsBucket
}

// BuildRegion is where Cloud Build runs.
func (c *Config) BuildRegion() string {
	if c.Build.Region != "" {
		return c.Build.Region
	}
	return c.Region
}

// Me is who runs are requested by: user, else git config user.email.
func (c *Config) Me(ctx context.Context) (string, error) {
	if c.User != "" {
		return c.User, nil
	}
	cmd := exec.CommandContext(ctx, "git", "config", "user.email")
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
	if me := strings.TrimSpace(string(out)); err == nil && me != "" {
		return me, nil
	}
	return "", errors.New("cannot tell who you are: set user in the local config or git config user.email")
}

// Marshal encodes the config as YAML.
func (c *Config) Marshal() ([]byte, error) { return yaml.Marshal(c) }
```

- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./internal/localcfg/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/localcfg
git commit -m "localcfg: read the local CLI config"
```

---

### Task 3: Task `batch`, `launch.json`, the launch claim, run listing and bare-ID lookup

**Files:**
- Modify: `internal/task/task.go` (`Batch`), `internal/task/task_test.go`
- Modify: `schemas/task.schema.json`
- Create: `testdata/task/valid/batch.json`, `testdata/task/invalid/bad-batch.json`
- Modify: `internal/runstore/runstore.go` (`Record.Execution`, `Record.Deadline`, `ReadFile`, `CreateRecord`, `ErrExists`)
- Create: `internal/runstore/launch.go`, `internal/runstore/list.go`, `internal/runstore/launch_test.go`, `internal/runstore/list_test.go`

**Interfaces:**
- Consumes: `runstore.Store`, `task.Spec`
- Produces:
  - `task.Spec.Batch string` (JSON `batch`), and `task.BatchRE`
  - `runstore.ErrExists`
  - `(*Store).CreateTask(ctx, *task.Spec) error`, which returns `ErrExists` when `task.json` is already there
  - `runstore.Launch{Version int; RunID, Backend, Execution, Job, LogURL, LaunchedBy string; LaunchedAt time.Time}` with JSON keys `version`, `run_id`, `backend`, `execution`, `job`, `log_url`, `launched_by` and `launched_at`
  - `(*Store).WriteLaunch(ctx, *Launch) error`, which returns `ErrExists`, and `(*Store).ReadLaunch(ctx) (*Launch, error)`
  - `runstore.Claim{Holder string; At time.Time}`, `(*Store).Claim(ctx, holder string, at time.Time) (ok bool, existing *Claim, err error)` and `(*Store).ClaimKey() string`. There is deliberately no way to clear a claim here: a successful launch leaves it in place for good, and a stale one is taken over with a generation-matched overwrite in Task 10 (`blobx.ReplaceIf`), never by delete-then-create.
  - `(*Store).CreateRecord(ctx, *Record) error`, which returns `ErrExists`: the runner's first write of `result.json` on Cloud Run (Task 6, I-10)
  - `runstore.Record.Deadline *time.Time` (JSON `deadline,omitempty`): when the run must be over (`started_at + total + 3m`), written by the runner; `ls` uses it for a run whose execution the backend no longer knows
  - `(*Store).ReadFile(ctx, name string) ([]byte, error)`
  - `runstore.Record.Execution string` (JSON `execution,omitempty`)
  - `runstore.RunTime(runID string) (time.Time, error)`
  - `runstore.ListSlugs(ctx, b *blob.Bucket) ([]string, error)`
  - `runstore.ListRunIDs(ctx, b *blob.Bucket, slug string, since time.Time) ([]string, error)`, newest first
  - `runstore.Locate(ctx, b *blob.Bucket, ref string) (slug, runID string, err error)`

- [ ] **Step 1: Write the failing tests**

Add to `internal/task/task_test.go`:

```go
func TestBatch(t *testing.T) {
	s := &Spec{Version: 1, RunID: "20260926-221530-a1b2", Repo: "acme/app", Ref: "main", Task: "x", Batch: "tuesday-cleanup.2"}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"Tuesday", "-x", "a b", strings.Repeat("a", 64)} {
		s.Batch = bad
		if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "batch") {
			t.Errorf("batch %q: %v", bad, err)
		}
	}
}
```

`testdata/task/valid/batch.json`:

```json
{ "version": 1, "run_id": "20260926-221530-a1b2", "repo": "acme/server", "ref": "main", "task": "Do it", "requested_by": "someone@example.com", "batch": "tuesday-cleanup" }
```

`testdata/task/invalid/bad-batch.json`:

```json
{ "version": 1, "run_id": "20260926-221530-a1b2", "repo": "acme/server", "ref": "main", "task": "Do it", "batch": "Has Spaces" }
```

`internal/runstore/launch_test.go`:

```go
package runstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/task"
)

func TestCreateTaskOnce(t *testing.T) {
	ctx := context.Background()
	b := memblob.OpenBucket(nil)
	s := Open(b, "acme-app", "20260926-221530-a1b2")
	spec := &task.Spec{Version: 1, RunID: "20260926-221530-a1b2", Repo: "acme/app", Ref: "main", Task: "x"}
	if err := s.CreateTask(ctx, spec); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateTask(ctx, spec); !errors.Is(err, ErrExists) {
		t.Fatalf("second CreateTask = %v", err)
	}
}

func TestLaunchAndClaim(t *testing.T) {
	ctx := context.Background()
	s := Open(memblob.OpenBucket(nil), "acme-app", "20260926-221530-a1b2")
	if _, err := s.ReadLaunch(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ReadLaunch before = %v", err)
	}
	at := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	ok, _, err := s.Claim(ctx, "laptop", at)
	if err != nil || !ok {
		t.Fatalf("first Claim = %v, %v", ok, err)
	}
	ok, existing, err := s.Claim(ctx, "other", at.Add(time.Second))
	if err != nil || ok || existing == nil || existing.Holder != "laptop" || !existing.At.Equal(at) {
		t.Fatalf("second Claim = %v, %+v, %v", ok, existing, err)
	}
	l := &Launch{Version: 1, RunID: "20260926-221530-a1b2", Backend: "cloud-run", Execution: "projects/p/locations/r/jobs/j/executions/e1", Job: "j", LaunchedAt: at}
	if err := s.WriteLaunch(ctx, l); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteLaunch(ctx, l); !errors.Is(err, ErrExists) {
		t.Fatalf("second WriteLaunch = %v", err)
	}
	got, err := s.ReadLaunch(ctx)
	if err != nil || got.Execution != l.Execution {
		t.Fatalf("ReadLaunch = %+v, %v", got, err)
	}
	if s.ClaimKey() != "runs/acme-app/20260926-221530-a1b2/launching" {
		t.Fatalf("ClaimKey = %s", s.ClaimKey())
	}
}

func TestCreateRecordOnce(t *testing.T) {
	ctx := context.Background()
	s := Open(memblob.OpenBucket(nil), "acme-app", "20260926-221530-a1b2")
	r := &Record{Version: 1, RunID: "20260926-221530-a1b2", Execution: "projects/p/locations/r/jobs/j/executions/e1", Status: StatusRunning, Stage: "bootstrap", Outcome: OutcomeNone}
	if err := s.CreateRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
	r2 := *r
	r2.Execution = "projects/p/locations/r/jobs/j/executions/e2"
	if err := s.CreateRecord(ctx, &r2); !errors.Is(err, ErrExists) {
		t.Fatalf("second CreateRecord = %v", err)
	}
	if got, _ := s.ReadRecord(ctx); got.Execution != r.Execution {
		t.Fatalf("the second CreateRecord overwrote: %+v", got)
	}
}
```

`internal/runstore/list_test.go`:

```go
package runstore

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/task"
)

func seed(t *testing.T, s *Store, id string) {
	t.Helper()
	spec := &task.Spec{Version: 1, RunID: id, Repo: "acme/x", Ref: "main", Task: "x"}
	if err := s.CreateTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
}

func TestListAndLocate(t *testing.T) {
	ctx := context.Background()
	b := memblob.OpenBucket(nil)
	ids := []string{"20260925-100000-aaaa", "20260927-090000-bbbb", "20260927-110000-cccc"}
	for _, id := range ids {
		seed(t, Open(b, "acme-app", id), id)
	}
	seed(t, Open(b, "acme-web", "20260927-120000-dddd"), "20260927-120000-dddd")
	// Not a run: a stray object must not break listing.
	_ = b.WriteAll(ctx, "runs/acme-app/notes.txt", []byte("x"), nil)

	slugs, err := ListSlugs(ctx, b)
	if err != nil || !slices.Equal(slugs, []string{"acme-app", "acme-web"}) {
		t.Fatalf("ListSlugs = %v, %v", slugs, err)
	}
	since := time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)
	got, err := ListRunIDs(ctx, b, "acme-app", since)
	if err != nil || !slices.Equal(got, []string{"20260927-110000-cccc", "20260927-090000-bbbb"}) {
		t.Fatalf("ListRunIDs = %v, %v", got, err)
	}
	if slug, id, err := Locate(ctx, b, "20260927-120000-dddd"); err != nil || slug != "acme-web" || id != "20260927-120000-dddd" {
		t.Fatalf("Locate bare = %s %s %v", slug, id, err)
	}
	if slug, _, err := Locate(ctx, b, "acme-app/20260925-100000-aaaa"); err != nil || slug != "acme-app" {
		t.Fatalf("Locate ref = %s %v", slug, err)
	}
	if _, _, err := Locate(ctx, b, "20260101-000000-ffff"); err == nil || !strings.Contains(err.Error(), "no run") {
		t.Fatalf("Locate missing = %v", err)
	}
	seed(t, Open(b, "acme-web", "20260927-110000-cccc"), "20260927-110000-cccc")
	if _, _, err := Locate(ctx, b, "20260927-110000-cccc"); err == nil || !strings.Contains(err.Error(), "acme-app/20260927-110000-cccc") {
		t.Fatalf("Locate ambiguous = %v", err)
	}
	if rt, err := RunTime("20260927-110000-cccc"); err != nil || !rt.Equal(time.Date(2026, 9, 27, 11, 0, 0, 0, time.UTC)) {
		t.Fatalf("RunTime = %v %v", rt, err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/task/ ./internal/runstore/ ./schemas/`
Expected: FAIL. `Batch`, `CreateTask`, `Launch`, `Claim`, `ListSlugs` and the other new names are undefined, and the schema rejects `batch`.

- [ ] **Step 3: Implement**

`internal/task/task.go`:
- Add `Batch string` with the JSON tag `batch,omitempty` after `RequestedBy`.
- Add `var BatchRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)`.
- In `Validate`, add:

```go
	if s.Batch != "" && !BatchRE.MatchString(s.Batch) {
		bad("task spec: batch %q must be 1-63 lower-case letters, digits, '.', '_' or '-'", s.Batch)
	}
```

`schemas/task.schema.json`: add `"batch": { "type": "string", "pattern": "^[a-z0-9][a-z0-9._-]{0,62}$" }`.

`internal/runstore/runstore.go`:
- Add `Execution string` with the JSON tag `execution,omitempty` to `Record`, after `Workflow`, and `Deadline *time.Time` with the JSON tag `deadline,omitempty`, after `StartedAt`. `Execution` always holds the canonical full name (Task 1).
- Add `var ErrExists = errors.New("already exists")`.
- Export `ReadFile`:

```go
// ReadFile reads one of the run's objects, such as transcripts/review-1.jsonl.
func (s *Store) ReadFile(ctx context.Context, name string) ([]byte, error) { return s.read(ctx, name) }

// create writes name only if it does not exist yet.
func (s *Store) create(ctx context.Context, name string, data []byte, contentType string) error {
	key := s.prefix + name
	err := s.bucket.WriteAll(ctx, key, data, &blob.WriterOptions{ContentType: contentType, IfNotExist: true})
	if gcerrors.Code(err) == gcerrors.FailedPrecondition {
		return fmt.Errorf("%s: %w", key, ErrExists)
	}
	if err != nil {
		return fmt.Errorf("writing %s: %w", key, err)
	}
	return nil
}

// CreateRecord stores result.json only if none exists yet (ErrExists). On
// Cloud Run the runner writes its first record this way, so a duplicate
// execution of the same run never writes one (design §4.7).
func (s *Store) CreateRecord(ctx context.Context, r *Record) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return s.create(ctx, "result.json", data, "application/json")
}

// CreateTask stores task.json unless it already exists (ErrExists), so two
// launches with one run ID cannot both write a task (design §4.7).
func (s *Store) CreateTask(ctx context.Context, spec *task.Spec) error {
	data, err := spec.Marshal()
	if err != nil {
		return err
	}
	return s.create(ctx, "task.json", data, "application/json")
}
```

`internal/runstore/launch.go`:

```go
package runstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Launch is launch.json: proof a run's execution was started (design §4.7).
type Launch struct {
	Version    int       `json:"version"`
	RunID      string    `json:"run_id"`
	Backend    string    `json:"backend"` // "cloud-run"
	Execution  string    `json:"execution"`
	Job        string    `json:"job"`
	LogURL     string    `json:"log_url,omitempty"`
	LaunchedBy string    `json:"launched_by,omitempty"`
	LaunchedAt time.Time `json:"launched_at"`
}

// WriteLaunch stores launch.json once; a second write returns ErrExists.
func (s *Store) WriteLaunch(ctx context.Context, l *Launch) error {
	data, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return err
	}
	return s.create(ctx, "launch.json", data, "application/json")
}

// ReadLaunch loads launch.json; ErrNotFound means never launched.
func (s *Store) ReadLaunch(ctx context.Context) (*Launch, error) {
	data, err := s.read(ctx, "launch.json")
	if err != nil {
		return nil, err
	}
	var l Launch
	if err := json.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("decoding launch.json: %w", err)
	}
	return &l, nil
}

// Claim is the "launching" marker: a CLI is between task.json and
// launch.json for this run.
type Claim struct {
	Holder string    `json:"holder"`
	At     time.Time `json:"at"`
}

// Claim takes the launch claim. When another holder has it, ok is false and
// existing is theirs.
func (s *Store) Claim(ctx context.Context, holder string, at time.Time) (ok bool, existing *Claim, err error) {
	data, err := json.Marshal(Claim{Holder: holder, At: at.UTC()})
	if err != nil {
		return false, nil, err
	}
	err = s.create(ctx, "launching", data, "application/json")
	if err == nil {
		return true, nil, nil
	}
	if !errors.Is(err, ErrExists) {
		return false, nil, err
	}
	raw, rerr := s.read(ctx, "launching")
	if rerr != nil {
		return false, nil, rerr
	}
	var c Claim
	if jerr := json.Unmarshal(raw, &c); jerr != nil {
		return false, &Claim{}, nil // unreadable: treat as held since the zero time
	}
	return false, &c, nil
}

// ClaimKey is the claim's object name, for Task 10's conditional takeover.
func (s *Store) ClaimKey() string { return s.prefix + "launching" }
```

Add `"errors"` to the imports.

`internal/runstore/list.go`:

```go
package runstore

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"gocloud.dev/blob"
)

var runIDRE = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[0-9a-f]{4}$`)

// RunTime is when a run ID was minted (its UTC timestamp prefix).
func RunTime(runID string) (time.Time, error) {
	if !runIDRE.MatchString(runID) {
		return time.Time{}, fmt.Errorf("%q is not a run ID", runID)
	}
	return time.ParseInLocation("20060102-150405", runID[:15], time.UTC)
}

// dirs lists the immediate "directory" names under prefix.
func dirs(ctx context.Context, b *blob.Bucket, prefix string) ([]string, error) {
	var out []string
	it := b.List(&blob.ListOptions{Prefix: prefix, Delimiter: "/"})
	for {
		obj, err := it.Next(ctx)
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("listing %s: %w", prefix, err)
		}
		if obj.IsDir {
			out = append(out, strings.TrimSuffix(strings.TrimPrefix(obj.Key, prefix), "/"))
		}
	}
}

// ListSlugs lists the repositories with runs, sorted.
func ListSlugs(ctx context.Context, b *blob.Bucket) ([]string, error) {
	s, err := dirs(ctx, b, "runs/")
	slices.Sort(s)
	return s, err
}

// ListRunIDs lists slug's run IDs minted at or after since, newest first.
// A zero since lists all of them.
func ListRunIDs(ctx context.Context, b *blob.Bucket, slug string, since time.Time) ([]string, error) {
	all, err := dirs(ctx, b, "runs/"+slug+"/")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, id := range all {
		if t, err := RunTime(id); err == nil && !t.Before(since) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	slices.Reverse(out)
	return out, nil
}

// Locate resolves "<slug>/<run-id>" or a bare run ID (design §3.3) to a run
// whose task.json exists.
func Locate(ctx context.Context, b *blob.Bucket, ref string) (slug, runID string, err error) {
	if strings.Contains(ref, "/") {
		if slug, runID, err = ParseRef(ref); err != nil {
			return "", "", err
		}
		if ok, err := b.Exists(ctx, "runs/"+slug+"/"+runID+"/task.json"); err != nil || !ok {
			return "", "", fmt.Errorf("no run %s in the runs bucket: %w", ref, errOr(err, ErrNotFound))
		}
		return slug, runID, nil
	}
	if !runIDRE.MatchString(ref) {
		return "", "", fmt.Errorf("%q is neither <repo-slug>/<run-id> nor a run ID", ref)
	}
	slugs, err := ListSlugs(ctx, b)
	if err != nil {
		return "", "", err
	}
	var found []string
	for _, s := range slugs {
		ok, err := b.Exists(ctx, "runs/"+s+"/"+ref+"/task.json")
		if err != nil {
			return "", "", err
		}
		if ok {
			found = append(found, s)
		}
	}
	switch len(found) {
	case 0:
		return "", "", fmt.Errorf("no run %s in the runs bucket: %w", ref, ErrNotFound)
	case 1:
		return found[0], ref, nil
	}
	refs := make([]string, len(found))
	for i, s := range found {
		refs[i] = s + "/" + ref
	}
	return "", "", fmt.Errorf("run ID %s is in several repositories; pass one of %s", ref, strings.Join(refs, ", "))
}

func errOr(err, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/task/ ./internal/runstore/ ./schemas/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/task internal/runstore schemas/task.schema.json testdata/task
git commit -m "runstore: add launch.json, the launch claim, run listing and bare run-ID lookup; task batch"
```

---

### Task 4: GCS preconditions (`blobx`), the branch lock, and the fake GCS server

**Files:**
- Create: `internal/gcpfake/server.go`, `internal/gcpfake/gcs.go`, `internal/gcpfake/gcs_test.go`
- Create: `internal/blobx/blobx.go`, `internal/blobx/blobx_test.go`
- Create: `internal/lock/lock.go`, `internal/lock/lock_test.go`
- Modify: `go.mod`, `go.sum` (`cloud.google.com/go/storage` and `google.golang.org/api` become direct)

**Interfaces:**
- Consumes: `gocloud.dev/blob`, `gocloud.dev/blob/gcsblob`, `cloud.google.com/go/storage`
- Produces:
  - `gcpfake.Server`, the shared plumbing: `newServer(t, handler) *Server`, `(*Server).URL`, `(*Server).Requests() []Request`, `gcpfake.Request{Method, Path, Query string; Body []byte}`, `writeJSON`, and `writeError(w, code, status, msg)` in Google's error JSON shape
  - `gcpfake.NewGCS(t) *GCS`, which serves the JSON API subset below, and `(*GCS).Bucket(t, name) *blobx.Bucket`, a gcsblob bucket pointed at it
  - `blobx.Bucket{*blob.Bucket; GCSName string}`
  - `blobx.Open(ctx, url string) (*Bucket, error)` and `blobx.Wrap(b *blob.Bucket) *Bucket`, which is non-GCS
  - `blobx.ErrConflict`, `blobx.ErrExists`, `blobx.ErrNotExist`
  - `(*Bucket).Create(ctx, key string, data []byte, contentType string) (gen int64, err error)`: `IfNotExist`, returning the new object's generation from the writer's own attributes (0 on non-GCS buckets), so no second read is needed
  - `(*Bucket).Read(ctx, key string) (data []byte, gen int64, err error)`: content and generation from one reader, so they always belong together
  - `(*Bucket).ReplaceIf(ctx, key string, data []byte, gen int64, prev []byte) (newGen int64, err error)`
  - `(*Bucket).DeleteIf(ctx, key string, gen int64, prev []byte) error`. On GCS a zero `gen` is refused: `storage.Conditions{GenerationMatch: 0}` is "no conditions", which the client rejects.
  - `(*Bucket).Touch(ctx, key string, t time.Time) error`
  - `lock.Holder{RunID, Execution string; ExpiresAt time.Time}`, `lock.Key(slug, branch string) string`
  - `lock.Acquire(ctx, b *blobx.Bucket, key string, h Holder, now time.Time) (*Lock, error)`, `(*Lock).Release(ctx) error`
  - `lock.BusyError{Holder Holder}`, which implements `error`

**Why GCS-specific code exists at all:**
- Design §4.1 takes an expired lock over with a **generation-matched** overwrite, and gocloud's portable API has only `IfNotExist`. `blobx` reaches the GCS object handle through gocloud's `As` hooks:
  - `Attributes.As(*storage.ObjectAttrs)` gives the generation.
  - `WriterOptions.BeforeWrite` with `***storage.ObjectHandle` applies `GenerationMatch`.
  - `Bucket.As(**storage.Client)` plus the bucket name gives a conditional delete and `CustomTime`.
- On `file://` and `mem://` buckets, which `exec --local` and tests use, it compares contents and then writes. That is not atomic, and the doc comment says so: only one runner ever uses a local bucket.

**The fake GCS server** implements only what gcsblob and `cloud.google.com/go/storage` send for these operations. The client is made with `storage.WithJSONReads()` so that reads use the JSON API too.
- `POST /upload/storage/v1/b/{bucket}/o?uploadType=multipart&name=…[&ifGenerationMatch=N]`: the body is multipart/related, with JSON metadata and then the media. An `ifGenerationMatch=0` means the object must not exist; a mismatch answers 412.
- `POST …?uploadType=resumable` answers with a `Location` header, and `PUT {Location}` then carries the body. Implement it too, because the client switches to resumable above its chunk size.
- `GET /storage/v1/b/{bucket}/o/{object}` gives the metadata (`generation` as a string, `updated`, `size`, `customTime`), and `?alt=media` gives the content with the `X-Goog-Generation`, `X-Goog-Metageneration` and `X-Goog-Stored-Content-Length` headers (the client reads a reader's generation from them). A missing object answers 404.
- `DELETE /storage/v1/b/{bucket}/o/{object}[?ifGenerationMatch=N]`
- `PATCH /storage/v1/b/{bucket}/o/{object}` with `{"customTime": …}`
- `GET /storage/v1/b/{bucket}/o?prefix=&delimiter=` gives `items` and `prefixes`.

If a request that none of these cover reaches the fake, it answers `501` and fails the test with the method and path, so the implementer adds exactly what the client sends.

- [ ] **Step 1: Write the failing tests**

`internal/lock/lock_test.go` runs every case against a mem bucket (the fallback) and against the fake GCS server (the precondition path):

```go
package lock

import (
	"context"
	"errors"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

func buckets(t *testing.T) map[string]*blobx.Bucket {
	return map[string]*blobx.Bucket{
		"mem": blobx.Wrap(memblob.OpenBucket(nil)),
		"gcs": gcpfake.NewGCS(t).Bucket(t, "runs"),
	}
}

var t0 = time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)

func TestKey(t *testing.T) {
	k := Key("acme-app", "fugaro/20260927-100000-abcd")
	if len(k) != len("locks/acme-app/")+16 || k != Key("acme-app", "fugaro/20260927-100000-abcd") || k == Key("acme-app", "fugaro/other") {
		t.Fatalf("Key = %q", k)
	}
}

func TestAcquireBusyRelease(t *testing.T) {
	for name, b := range buckets(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			key := Key("acme-app", "fugaro/x")
			a := Holder{RunID: "20260927-100000-aaaa", Execution: "e1", ExpiresAt: t0.Add(time.Hour)}
			l, err := Acquire(ctx, b, key, a, t0)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Acquire(ctx, b, key, Holder{RunID: "20260927-100000-bbbb", ExpiresAt: t0.Add(time.Hour)}, t0.Add(time.Minute))
			var busy *BusyError
			if !errors.As(err, &busy) || busy.Holder.RunID != a.RunID || busy.Holder.Execution != "e1" {
				t.Fatalf("second Acquire = %v", err)
			}
			if err := l.Release(ctx); err != nil {
				t.Fatal(err)
			}
			if ok, _ := b.Exists(ctx, key); ok {
				t.Fatal("lock object survives Release")
			}
			if _, err := Acquire(ctx, b, key, a, t0); err != nil {
				t.Fatalf("Acquire after Release = %v", err)
			}
		})
	}
}

func TestAcquireTakesOverExpiredLock(t *testing.T) {
	for name, b := range buckets(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			key := Key("acme-app", "fugaro/x")
			old, err := Acquire(ctx, b, key, Holder{RunID: "20260927-100000-aaaa", ExpiresAt: t0.Add(time.Minute)}, t0)
			if err != nil {
				t.Fatal(err)
			}
			next := Holder{RunID: "20260927-110000-bbbb", ExpiresAt: t0.Add(2 * time.Hour)}
			if _, err := Acquire(ctx, b, key, next, t0.Add(2*time.Minute)); err != nil {
				t.Fatalf("takeover = %v", err)
			}
			// The old holder's late Release must not delete the new lock.
			if err := old.Release(ctx); err != nil {
				t.Fatal(err)
			}
			if ok, _ := b.Exists(ctx, key); !ok {
				t.Fatal("a stale Release deleted the new holder's lock")
			}
		})
	}
}

func TestAcquireTakesOverUnreadableLock(t *testing.T) {
	for name, b := range buckets(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			key := Key("acme-app", "fugaro/x")
			if err := b.WriteAll(ctx, key, []byte("garbage"), nil); err != nil {
				t.Fatal(err)
			}
			if _, err := Acquire(ctx, b, key, Holder{RunID: "20260927-110000-bbbb", ExpiresAt: t0.Add(time.Hour)}, t0); err != nil {
				t.Fatalf("Acquire over garbage = %v", err)
			}
		})
	}
}
```

`internal/blobx/blobx_test.go`:

```go
package blobx_test

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

func containsParam(query, name string) bool {
	v, err := url.ParseQuery(query)
	return err == nil && v.Has(name)
}

func TestConditionalOpsOnGCS(t *testing.T) {
	ctx := context.Background()
	fake := gcpfake.NewGCS(t)
	b := fake.Bucket(t, "runs")
	gen, err := b.Create(ctx, "k", []byte("v1"), "text/plain")
	if err != nil || gen == 0 {
		t.Fatalf("Create = %d, %v", gen, err)
	}
	if _, err := b.Create(ctx, "k", []byte("again"), "text/plain"); !errors.Is(err, blobx.ErrExists) {
		t.Fatalf("second Create = %v", err)
	}
	data, rgen, err := b.Read(ctx, "k")
	if err != nil || string(data) != "v1" || rgen != gen {
		t.Fatalf("Read = %q %d %v", data, rgen, err)
	}
	gen2, err := b.ReplaceIf(ctx, "k", []byte("v2"), gen, data)
	if err != nil || gen2 == gen {
		t.Fatalf("ReplaceIf = %d, %v", gen2, err)
	}
	if _, err := b.ReplaceIf(ctx, "k", []byte("v3"), gen, data); !errors.Is(err, blobx.ErrConflict) {
		t.Fatalf("stale ReplaceIf = %v", err)
	}
	if err := b.DeleteIf(ctx, "k", gen, data); !errors.Is(err, blobx.ErrConflict) {
		t.Fatalf("stale DeleteIf = %v", err)
	}
	if err := b.DeleteIf(ctx, "k", 0, nil); err == nil {
		t.Fatal("DeleteIf with generation 0 on GCS must be refused")
	}
	if err := b.Touch(ctx, "k", time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC)); err != nil || !fake.HasCustomTime("runs", "k") {
		t.Fatalf("Touch: %v", err)
	}
	if err := b.DeleteIf(ctx, "k", gen2, []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Read(ctx, "k"); !errors.Is(err, blobx.ErrNotExist) {
		t.Fatalf("Read after delete = %v", err)
	}
	var preconditions int
	for _, r := range fake.Requests() {
		if containsParam(r.Query, "ifGenerationMatch") {
			preconditions++
		}
	}
	if preconditions < 4 { // create, two replaces, two deletes (one refused locally)
		t.Fatalf("%d requests carried ifGenerationMatch", preconditions)
	}
}

func TestConditionalOpsFallback(t *testing.T) {
	ctx := context.Background()
	b := blobx.Wrap(memblob.OpenBucket(nil))
	if _, err := b.Create(ctx, "k", []byte("v1"), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ReplaceIf(ctx, "k", []byte("v2"), 0, []byte("not v1")); !errors.Is(err, blobx.ErrConflict) {
		t.Fatalf("content mismatch = %v", err)
	}
	if _, err := b.ReplaceIf(ctx, "k", []byte("v2"), 0, []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := b.DeleteIf(ctx, "k", 0, []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if err := b.Touch(ctx, "missing", time.Now()); err != nil {
		t.Fatalf("Touch is a no-op off GCS: %v", err)
	}
}
```

`internal/gcpfake/gcs_test.go` checks the fake against the real client, so that `blobx`'s tests prove something:

```go
package gcpfake

import (
	"context"
	"errors"
	"io"
	"testing"

	"cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
)

func TestGCSFakeWithRealClient(t *testing.T) {
	ctx := context.Background()
	g := NewGCS(t)
	c := g.Client(t)
	obj := c.Bucket("runs").Object("runs/acme-app/x/task.json")
	w := obj.If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
	_, _ = w.Write([]byte("{}"))
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	gen := w.Attrs().Generation
	w = obj.If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
	_, _ = w.Write([]byte("{}"))
	if err := w.Close(); err == nil {
		t.Fatal("DoesNotExist overwrite succeeded")
	}
	r, err := obj.NewReader(ctx)
	if err != nil || r.Attrs.Generation != gen {
		t.Fatalf("reader gen = %v, %v", r, err)
	}
	data, _ := io.ReadAll(r)
	r.Close()
	if string(data) != "{}" {
		t.Fatalf("data = %q", data)
	}
	it := c.Bucket("runs").Objects(ctx, &storage.Query{Prefix: "runs/", Delimiter: "/"})
	a, err := it.Next()
	if err != nil || a.Prefix != "runs/acme-app/" {
		t.Fatalf("prefix listing = %+v, %v", a, err)
	}
	if _, err := it.Next(); !errors.Is(err, iterator.Done) {
		t.Fatalf("second item = %v", err)
	}
	if err := obj.If(storage.Conditions{GenerationMatch: gen + 1}).Delete(ctx); err == nil {
		t.Fatal("mismatched delete succeeded")
	}
	if err := obj.If(storage.Conditions{GenerationMatch: gen}).Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := obj.Attrs(ctx); !errors.Is(err, storage.ErrObjectNotExist) {
		t.Fatalf("after delete: %v", err)
	}
}
```

`(*GCS).Client(t) *storage.Client` is the client `Bucket` wraps. `(*GCS).HasCustomTime(bucket, key string) bool` reports whether an object's `customTime` is set.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/lock/ ./internal/blobx/ ./internal/gcpfake/`
Expected: FAIL (the packages do not exist yet)

- [ ] **Step 3: Implement**

`internal/gcpfake/server.go`:

```go
// Package gcpfake serves minimal, stateful httptest fakes of the Google
// Cloud REST APIs Fugaro calls: GCS (JSON API), Cloud Run Admin v2, Cloud
// Logging v2, Secret Manager v1 and Cloud Build v1. Each fake implements
// only the calls Fugaro makes, and fails the test on anything else, so a
// client change that sends a new call is noticed.
package gcpfake

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Request is one call a fake received.
type Request struct {
	Method, Path, Query string
	Body                []byte
}

// Server records requests and hands them to a handler.
type Server struct {
	*httptest.Server
	t    *testing.T
	mu   sync.Mutex
	reqs []Request
}

func newServer(t *testing.T, h func(w http.ResponseWriter, r *http.Request, body []byte)) *Server {
	t.Helper()
	s := &Server{t: t}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.reqs = append(s.reqs, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Body: body})
		s.mu.Unlock()
		h(w, r, body)
	}))
	t.Cleanup(s.Close)
	return s
}

// Requests returns the calls received so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.reqs...)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError answers in Google's error shape, which googleapi parses.
func writeError(w http.ResponseWriter, code int, status, msg string) {
	writeJSON(w, code, map[string]any{"error": map[string]any{"code": code, "status": status, "message": msg}})
}

// unhandled fails the test for a call the fake doesn't implement.
func (s *Server) unhandled(w http.ResponseWriter, r *http.Request) {
	s.t.Errorf("gcpfake: unhandled %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
	writeError(w, http.StatusNotImplemented, "UNIMPLEMENTED", "not faked")
}
```

`internal/gcpfake/gcs.go` keeps objects in memory: `map[bucket]map[name]*object{data []byte; gen int64; ct string; customTime time.Time; updated time.Time}` and a global generation counter. The handler routes as listed above. For multipart uploads, parse the body with `mime/multipart` using the boundary in `Content-Type`: the first part is the metadata JSON (`name`, `contentType`), and the second is the media. On success, answer with the object metadata, with `generation` as a decimal string.

`(*GCS).Bucket` builds the storage client like this:

```go
// Bucket returns a gcsblob bucket named name on this fake.
func (g *GCS) Bucket(t *testing.T, name string) *blobx.Bucket {
	t.Helper()
	ctx := context.Background()
	client, err := storage.NewClient(ctx, option.WithEndpoint(g.URL+"/storage/v1/"),
		option.WithoutAuthentication(), option.WithHTTPClient(g.Client()), storage.WithJSONReads())
	if err != nil {
		t.Fatal(err)
	}
	b, err := gcsblob.OpenBucket(ctx, nil, name, &gcsblob.Options{Client: client})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	return &blobx.Bucket{Bucket: b, GCSName: name}
}
```

`internal/blobx/blobx.go`:

```go
// Package blobx adds the few conditional operations Fugaro needs beyond
// gocloud's portable blob API: create-if-absent with the new generation,
// generation-matched replace and delete, and GCS custom time (design §4.1).
// On GCS they are atomic. On other drivers (file:// and mem://, used by
// local runs and tests with a single writer) replace and delete compare
// contents first, which is NOT atomic.
package blobx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"cloud.google.com/go/storage"
	"gocloud.dev/blob"
	_ "gocloud.dev/blob/fileblob" // file:// buckets
	_ "gocloud.dev/blob/gcsblob"  // gs:// buckets
	_ "gocloud.dev/blob/memblob"  // mem:// buckets
	"gocloud.dev/gcerrors"
	"google.golang.org/api/googleapi"
)

// Bucket is a blob bucket and, when it is GCS, its bucket name.
type Bucket struct {
	*blob.Bucket
	GCSName string
}

var (
	ErrConflict = errors.New("the object changed")   // a conditional write or delete lost a race
	ErrExists   = errors.New("object already exists") // Create found the object
	ErrNotExist = errors.New("object does not exist")
)

// Open opens a gocloud bucket URL (gs://, file://, mem://).
func Open(ctx context.Context, rawURL string) (*Bucket, error) {
	b, err := blob.OpenBucket(ctx, rawURL)
	if err != nil {
		return nil, fmt.Errorf("opening bucket %s: %w", rawURL, err)
	}
	out := &Bucket{Bucket: b}
	if u, err := url.Parse(rawURL); err == nil && u.Scheme == "gs" {
		out.GCSName = u.Host
	}
	return out, nil
}

// Wrap treats b as a non-GCS bucket.
func Wrap(b *blob.Bucket) *Bucket { return &Bucket{Bucket: b} }

func (b *Bucket) client() *storage.Client {
	var c *storage.Client
	if b.GCSName == "" || !b.As(&c) {
		return nil
	}
	return c
}

// write writes data to key, optionally conditioned by cond (GCS only), and
// returns the new generation (0 off GCS).
func (b *Bucket) write(ctx context.Context, key string, data []byte, contentType string, ifNotExist bool, cond *storage.Conditions) (int64, error) {
	var sw *storage.Writer
	opts := &blob.WriterOptions{ContentType: contentType, IfNotExist: ifNotExist, BeforeWrite: func(as func(any) bool) error {
		var oh **storage.ObjectHandle
		if cond != nil && as(&oh) {
			*oh = (*oh).If(*cond)
		}
		as(&sw) // must come after the ObjectHandle (gcsblob's documented order)
		return nil
	}}
	if err := b.WriteAll(ctx, key, data, opts); err != nil {
		if gcerrors.Code(err) == gcerrors.FailedPrecondition {
			if ifNotExist {
				return 0, ErrExists
			}
			return 0, ErrConflict
		}
		return 0, err
	}
	if sw != nil && sw.Attrs() != nil {
		return sw.Attrs().Generation, nil
	}
	return 0, nil
}

// Create writes key only if it does not exist (ErrExists otherwise).
func (b *Bucket) Create(ctx context.Context, key string, data []byte, contentType string) (int64, error) {
	return b.write(ctx, key, data, contentType, true, nil)
}

// Read returns key's content and generation (0 off GCS) from one reader.
func (b *Bucket) Read(ctx context.Context, key string) ([]byte, int64, error) {
	r, err := b.NewReader(ctx, key, nil)
	if gcerrors.Code(err) == gcerrors.NotFound {
		return nil, 0, ErrNotExist
	}
	if err != nil {
		return nil, 0, err
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, 0, err
	}
	var sr *storage.Reader
	if r.As(&sr) {
		return data, sr.Attrs.Generation, nil
	}
	return data, 0, nil
}

// ReplaceIf overwrites key with data only if it is still generation gen
// (GCS) or still holds prev (other drivers), and returns the new generation.
func (b *Bucket) ReplaceIf(ctx context.Context, key string, data []byte, gen int64, prev []byte) (int64, error) {
	if b.client() != nil {
		if gen == 0 {
			return 0, fmt.Errorf("replacing %s: no generation to match", key)
		}
		return b.write(ctx, key, data, "", false, &storage.Conditions{GenerationMatch: gen})
	}
	cur, _, err := b.Read(ctx, key)
	if err != nil || !bytes.Equal(cur, prev) {
		return 0, ErrConflict
	}
	return b.write(ctx, key, data, "", false, nil)
}

// DeleteIf deletes key only if it is still generation gen (GCS) or still
// holds prev (other drivers). A missing object is not an error.
func (b *Bucket) DeleteIf(ctx context.Context, key string, gen int64, prev []byte) error {
	if c := b.client(); c != nil {
		if gen == 0 {
			return fmt.Errorf("deleting %s: no generation to match", key)
		}
		err := c.Bucket(b.GCSName).Object(key).If(storage.Conditions{GenerationMatch: gen}).Delete(ctx)
		var ae *googleapi.Error
		switch {
		case err == nil, errors.Is(err, storage.ErrObjectNotExist):
			return nil
		case errors.As(err, &ae) && ae.Code == http.StatusPreconditionFailed:
			return ErrConflict
		}
		return err
	}
	cur, _, err := b.Read(ctx, key)
	if errors.Is(err, ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(cur, prev) {
		return ErrConflict
	}
	return b.Delete(ctx, key)
}

// Touch sets the object's GCS custom time, which the bucket's lifecycle
// rule reads as "last used" (design §3.3). No-op on other drivers.
func (b *Bucket) Touch(ctx context.Context, key string, t time.Time) error {
	c := b.client()
	if c == nil {
		return nil
	}
	_, err := c.Bucket(b.GCSName).Object(key).Update(ctx, storage.ObjectAttrsToUpdate{CustomTime: t.UTC()})
	return err
}
```

`internal/lock/lock.go`:

```go
// Package lock is the one-run-per-branch lock (design §4.1): an object
// locks/<slug>/<branch-hash> holding its holder and an expiry equal to the
// job's task timeout. Creation is create-if-absent; an expired or
// unreadable lock is taken over with a generation-matched overwrite, so two
// runners can never both take it over.
package lock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
)

// Holder is what the lock object records. Execution is the canonical
// execution name; compare it with backend.SameExecution, never ==.
type Holder struct {
	RunID     string    `json:"run_id"`
	Execution string    `json:"execution,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

// BusyError means a live lock is held by someone else.
type BusyError struct{ Holder Holder }

func (e *BusyError) Error() string {
	return fmt.Sprintf("branch busy: run %s holds its lock until %s", e.Holder.RunID, e.Holder.ExpiresAt.Format(time.RFC3339))
}

// Lock is a held lock.
type Lock struct {
	b    *blobx.Bucket
	key  string
	gen  int64
	body []byte
}

// Key is the lock object for branch in slug's prefix.
func Key(slug, branch string) string {
	sum := sha256.Sum256([]byte(branch))
	return "locks/" + slug + "/" + hex.EncodeToString(sum[:8])
}

// Acquire takes the lock at key for h, taking over one that expired before
// now or that cannot be parsed.
func Acquire(ctx context.Context, b *blobx.Bucket, key string, h Holder, now time.Time) (*Lock, error) {
	body, err := json.Marshal(h)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ { // one retry: the holder may release between our calls
		gen, err := b.Create(ctx, key, body, "application/json")
		if err == nil {
			return &Lock{b: b, key: key, gen: gen, body: body}, nil
		}
		if !errors.Is(err, blobx.ErrExists) {
			return nil, fmt.Errorf("creating lock %s: %w", key, err)
		}
		prev, prevGen, err := b.Read(ctx, key)
		if errors.Is(err, blobx.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading lock %s: %w", key, err)
		}
		var cur Holder
		if json.Unmarshal(prev, &cur) == nil && cur.RunID != "" && now.Before(cur.ExpiresAt) {
			return nil, &BusyError{Holder: cur}
		}
		newGen, err := b.ReplaceIf(ctx, key, body, prevGen, prev)
		if errors.Is(err, blobx.ErrConflict) {
			return nil, &BusyError{Holder: cur} // another runner took it over first
		}
		if err != nil {
			return nil, fmt.Errorf("taking over lock %s: %w", key, err)
		}
		return &Lock{b: b, key: key, gen: newGen, body: body}, nil
	}
	return nil, fmt.Errorf("lock %s kept changing; giving up", key)
}

// Release deletes the lock if this holder still has it. A lock that was
// taken over (it expired) is left alone.
func (l *Lock) Release(ctx context.Context) error {
	err := l.b.DeleteIf(ctx, l.key, l.gen, l.body)
	if errors.Is(err, blobx.ErrConflict) {
		return nil
	}
	return err
}
```

`go.mod`: run `go get cloud.google.com/go/storage google.golang.org/api` and then `go mod tidy`. `storage` is justified because design §4.1 needs generation preconditions, and gocloud has no portable form of them. T4 owns `go.mod` in lane B; T5 comes after it in the same lane. Later tasks that add a module (T9, T14) resolve `go.mod`/`go.sum` conflicts at merge time with `go mod tidy` and never by hand.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/lock/ ./internal/blobx/ ./internal/gcpfake/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/blobx internal/lock internal/gcpfake
git commit -m "lock: add the branch lock on GCS generation preconditions, with a GCS JSON API fake"
```

---

### Task 5: Content-addressed dependency caches (`internal/cache`)

**Files:**
- Create: `internal/cache/key.go`, `internal/cache/archive.go`, `internal/cache/cache.go`
- Create: `internal/cache/key_test.go`, `internal/cache/archive_test.go`, `internal/cache/cache_test.go`
- Modify: `go.mod`, `go.sum` (add `github.com/klauspost/compress`)

**Interfaces:**
- Consumes: `config.CacheEntry`, `blobx.Bucket`, and `doublestar` for key globs
- Produces:
  - `cache.Entry{Key []string; Paths []string}`, which is `config.CacheEntry`: use the config type directly
  - `cache.KeyOf(root string, e config.CacheEntry, baseImage string) (key string, ok bool, err error)`. `ok` is false when no key file matches.
  - `cache.Resolve(paths []string, root, home string) ([]string, error)`: `~/` becomes home, and a relative path becomes a path under root. Absolute paths and `..` are refused.
  - `cache.ObjectKey(slug, workflow, key string) string`, which is `cache/<slug>/<wf>/<key>.tar.zst`
  - `cache.Write(w io.Writer, roots []string, maxBytes int64) (int64, error)` and `cache.ErrTooLarge`
  - `cache.Extract(r io.Reader, roots []string, maxBytes int64) error`
  - `cache.Store{Bucket *blobx.Bucket; Slug, Workflow string; MaxBytes int64}`
  - `(*Store).Restore(ctx, key string, roots []string) (hit bool, err error)`
  - `(*Store).Save(ctx, key string, roots []string) (saved bool, err error)`
  - `cache.DefaultMaxBytes = 8 << 30`

**The archive format:**
- A zstd-compressed tar.
- The first entry is `fugaro-cache.json`, which holds `{"version":1,"roots":N}`.
- Every file of root *i* is stored as `<i>/<path relative to root i>`, so a restore maps entries to roots by index, not by absolute path. `HOME` may differ between images.
- Restore refuses each unsafe entry before writing it (and stops there):
  - a name that is absolute, contains `..`, or has an index outside `[0,N)`
  - a symlink whose target resolves outside its root
  - any write that would follow a symlink already in the root, whether baked into the image or planted by an earlier entry (I-3)
  - a hardlink, device or FIFO
  - more than `maxBytes` of uncompressed content
- Modes are masked to `0o755` for directories and executables and `0o644` for other files. No setuid, setgid or sticky bit survives.
- Extraction goes through Go's `os.Root` (`os.OpenRoot(roots[i])`, Go 1.24+; the module is on 1.27), which refuses any path that escapes the root, through `..` or through a symlink at any component, by construction. Lexical checks stay as a first filter only.
- `Save` skips a cache whose roots hold no regular file at all: archives are immutable, so an empty one would block its key for good.
- `Save` streams tar → zstd → blob writer. When the uncompressed content passes `maxBytes`, it cancels the writer's context, so nothing is uploaded, and returns `ErrTooLarge`.

- [ ] **Step 1: Write the failing tests**

`internal/cache/key_test.go`:

```go
package cache

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

func write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestKeyOf(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"yarn.lock": "v1", "a/build.gradle": "x", "b/build.gradle.kts": "y"})
	e := config.CacheEntry{Key: []string{"yarn.lock"}, Paths: []string{"~/.yarn/berry/cache"}}
	k1, ok, err := KeyOf(root, e, "base@sha256:1")
	if err != nil || !ok || len(k1) != 64 {
		t.Fatalf("KeyOf = %q %v %v", k1, ok, err)
	}
	if k2, _, _ := KeyOf(root, e, "base@sha256:2"); k2 == k1 {
		t.Fatal("the base image does not change the key")
	}
	if k3, _, _ := KeyOf(root, config.CacheEntry{Key: e.Key, Paths: []string{"~/.other"}}, "base@sha256:1"); k3 == k1 {
		t.Fatal("the paths do not change the key")
	}
	write(t, root, map[string]string{"yarn.lock": "v2"})
	if k4, _, _ := KeyOf(root, e, "base@sha256:1"); k4 == k1 {
		t.Fatal("the key file's content does not change the key")
	}
	glob := config.CacheEntry{Key: []string{"**/*.gradle*"}, Paths: []string{"~/.gradle/caches"}}
	if _, ok, err := KeyOf(root, glob, ""); !ok || err != nil {
		t.Fatalf("glob KeyOf ok=%v err=%v", ok, err)
	}
	if _, ok, _ := KeyOf(root, config.CacheEntry{Key: []string{"missing.lock"}, Paths: []string{"x"}}, ""); ok {
		t.Fatal("no matching key file must mean no cache")
	}
}

func TestResolve(t *testing.T) {
	got, err := Resolve([]string{"~/.cache/yarn", "node_modules/.cache"}, "/work/repo", "/home/fugaro")
	if err != nil || got[0] != "/home/fugaro/.cache/yarn" || got[1] != "/work/repo/node_modules/.cache" {
		t.Fatalf("Resolve = %v, %v", got, err)
	}
	for _, bad := range []string{"/etc", "~/../../etc", "../x", "~"} {
		if _, err := Resolve([]string{bad}, "/work/repo", "/home/fugaro"); err == nil {
			t.Errorf("Resolve(%q) accepted", bad)
		}
	}
}
```

`internal/cache/archive_test.go` builds hostile archives by hand with `archive/tar` and zstd:

```go
package cache

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func TestWriteExtractRoundTrip(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	write(t, src, map[string]string{"a/b.txt": "hello", "c.bin": "x"})
	if err := os.Symlink("a/b.txt", filepath.Join(src, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(src, "c.bin"), 0o4755); err != nil { // setuid must not survive
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := Write(&buf, []string{src}, 1<<20); err != nil {
		t.Fatal(err)
	}
	if err := Extract(&buf, []string{dst}, 1<<20); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dst, "a/b.txt")); string(data) != "hello" {
		t.Fatalf("content = %q", data)
	}
	if target, _ := os.Readlink(filepath.Join(dst, "link")); target != "a/b.txt" {
		t.Fatalf("link = %q", target)
	}
	if fi, _ := os.Stat(filepath.Join(dst, "c.bin")); fi.Mode()&os.ModeSetuid != 0 || fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v", fi.Mode())
	}
}

// hostile builds an archive with the manifest and then hdrs.
func hostile(t *testing.T, hdrs ...*tar.Header) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	zw, _ := zstd.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	manifest := []byte(`{"version":1,"roots":1}`)
	_ = tw.WriteHeader(&tar.Header{Name: manifestName, Mode: 0o644, Size: int64(len(manifest)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(manifest)
	for _, h := range hdrs {
		if h.Typeflag == tar.TypeReg {
			h.Size = 1
		}
		_ = tw.WriteHeader(h)
		if h.Typeflag == tar.TypeReg {
			_, _ = tw.Write([]byte("x"))
		}
	}
	_ = tw.Close()
	_ = zw.Close()
	return &buf
}

func TestRestoreRejectsTraversal(t *testing.T) {
	for _, name := range []string{"0/../../escape", "/abs", "1/out-of-range-root", "0/a/../../x"} {
		dst := t.TempDir()
		err := Extract(hostile(t, &tar.Header{Name: name, Mode: 0o644, Typeflag: tar.TypeReg}), []string{filepath.Join(dst, "root")}, 1<<20)
		if err == nil {
			t.Errorf("%s: extracted", name)
		}
		if _, statErr := os.Stat(filepath.Join(dst, "escape")); statErr == nil {
			t.Errorf("%s: wrote outside the root", name)
		}
	}
}

func TestRestoreRejectsSymlinkEscape(t *testing.T) {
	for _, target := range []string{"../../etc", "/etc/passwd"} {
		dst := t.TempDir()
		err := Extract(hostile(t, &tar.Header{Name: "0/link", Linkname: target, Typeflag: tar.TypeSymlink}), []string{dst}, 1<<20)
		if err == nil || !strings.Contains(err.Error(), "outside") {
			t.Errorf("symlink to %s: %v", target, err)
		}
	}
	if err := Extract(hostile(t, &tar.Header{Name: "0/hard", Linkname: "0/x", Typeflag: tar.TypeLink}), []string{t.TempDir()}, 1<<20); err == nil {
		t.Error("hardlink extracted")
	}
}

func TestRestoreRejectsWriteThroughExistingSymlink(t *testing.T) {
	dst, outside := t.TempDir(), t.TempDir()
	victim := filepath.Join(outside, "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Already in the root (baked into the image, say): a file link and a
	// directory link, both pointing outside.
	if err := os.Symlink(victim, filepath.Join(dst, "a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dst, "dir")); err != nil {
		t.Fatal(err)
	}
	// A regular entry at the link itself replaces the link, not the victim.
	if err := Extract(hostile(t, &tar.Header{Name: "0/a", Mode: 0o644, Typeflag: tar.TypeReg}), []string{dst}, 1<<20); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(victim); string(data) != "keep" {
		t.Fatalf("wrote through the symlink: victim = %q", data)
	}
	if fi, _ := os.Lstat(filepath.Join(dst, "a")); fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("the link at the target survived")
	}
	// An entry under the directory link must be refused, not written outside.
	if err := Extract(hostile(t, &tar.Header{Name: "0/dir/planted", Mode: 0o644, Typeflag: tar.TypeReg}), []string{dst}, 1<<20); err == nil {
		t.Fatal("extracted through a directory symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "planted")); err == nil {
		t.Fatal("a file was planted outside the root")
	}
}

func TestExtractEnforcesMaxBytes(t *testing.T) {
	src := t.TempDir()
	write(t, src, map[string]string{"big": strings.Repeat("x", 4096)})
	var buf bytes.Buffer
	if _, err := Write(&buf, []string{src}, 1<<20); err != nil {
		t.Fatal(err)
	}
	if err := Extract(&buf, []string{t.TempDir()}, 1024); err == nil {
		t.Fatal("an archive past maxBytes was extracted")
	}
}
```

`internal/cache/cache_test.go`:

```go
package cache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/blobx"
)

func TestSaveRestore(t *testing.T) {
	ctx := context.Background()
	s := &Store{Bucket: blobx.Wrap(memblob.OpenBucket(nil)), Slug: "acme-app", Workflow: "web", MaxBytes: 1 << 20}
	src := t.TempDir()
	write(t, src, map[string]string{"pkg/a.tgz": "A"})
	saved, err := s.Save(ctx, "k1", []string{src})
	if err != nil || !saved {
		t.Fatalf("Save = %v, %v", saved, err)
	}
	if saved, err := s.Save(ctx, "k1", []string{src}); err != nil || saved {
		t.Fatalf("second Save = %v, %v (an existing key must be left alone)", saved, err)
	}
	dst := filepath.Join(t.TempDir(), "cache")
	hit, err := s.Restore(ctx, "k1", []string{dst})
	if err != nil || !hit {
		t.Fatalf("Restore = %v, %v", hit, err)
	}
	if data, _ := os.ReadFile(filepath.Join(dst, "pkg/a.tgz")); string(data) != "A" {
		t.Fatalf("restored = %q", data)
	}
	if hit, err := s.Restore(ctx, "missing", []string{dst}); err != nil || hit {
		t.Fatalf("Restore miss = %v, %v", hit, err)
	}
}

func TestSaveSkipsOversizedArchive(t *testing.T) {
	ctx := context.Background()
	b := blobx.Wrap(memblob.OpenBucket(nil))
	s := &Store{Bucket: b, Slug: "acme-app", Workflow: "web", MaxBytes: 1024}
	src := t.TempDir()
	write(t, src, map[string]string{"big": strings.Repeat("x", 8192)})
	if _, err := s.Save(ctx, "k1", []string{src}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Save = %v", err)
	}
	if ok, _ := b.Exists(ctx, ObjectKey("acme-app", "web", "k1")); ok {
		t.Fatal("an oversized archive was uploaded")
	}
}

func TestSaveSkipsEmptyRoots(t *testing.T) {
	ctx := context.Background()
	b := blobx.Wrap(memblob.OpenBucket(nil))
	s := &Store{Bucket: b, Slug: "acme-app", Workflow: "web"}
	empty := t.TempDir()
	if err := os.MkdirAll(filepath.Join(empty, "only-dirs"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, roots := range [][]string{{filepath.Join(t.TempDir(), "missing")}, {empty}} {
		if saved, err := s.Save(ctx, "k1", roots); saved || err != nil {
			t.Fatalf("Save(%v) = %v, %v", roots, saved, err)
		}
	}
	if ok, _ := b.Exists(ctx, ObjectKey("acme-app", "web", "k1")); ok {
		t.Fatal("an empty archive now blocks the key")
	}
}

func TestRestoreCorruptArchiveIsWarning(t *testing.T) {
	ctx := context.Background()
	b := blobx.Wrap(memblob.OpenBucket(nil))
	s := &Store{Bucket: b, Slug: "acme-app", Workflow: "web", MaxBytes: 1 << 20}
	_ = b.WriteAll(ctx, ObjectKey("acme-app", "web", "k1"), []byte("not zstd"), nil)
	hit, err := s.Restore(ctx, "k1", []string{t.TempDir()})
	if hit || err == nil {
		t.Fatalf("Restore corrupt = %v, %v (want a miss with an error the runner logs as a warning)", hit, err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/cache/`
Expected: FAIL (the package does not exist yet)

- [ ] **Step 3: Implement**

`go get github.com/klauspost/compress@latest`. It is pure Go and widely used (containerd and docker use it), and the standard library has no zstd. The design names zstd archives (§3.3), and shelling out to `tar --zstd` would make tests depend on the host's tar.

`internal/cache/key.go`:

```go
// Package cache restores and writes back content-addressed dependency
// caches (design §4.1): cache/<slug>/<workflow>/<key>.tar.zst, where key is
// the SHA-256 of the key files' contents, the cached paths and the base
// image. Archives are immutable, so parallel runs never contend.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/dimipaun/fugaro/internal/config"
)

// KeyOf computes e's key in the checkout at root. ok is false when none of
// the key files exist, which means the entry is not cached.
func KeyOf(root string, e config.CacheEntry, baseImage string) (string, bool, error) {
	var files []string
	for _, pattern := range e.Key {
		matches, err := doublestar.Glob(os.DirFS(root), pattern, doublestar.WithFilesOnly())
		if err != nil {
			return "", false, fmt.Errorf("cache key %q: %w", pattern, err)
		}
		files = append(files, matches...)
	}
	slices.Sort(files)
	files = slices.Compact(files)
	if len(files) == 0 {
		return "", false, nil
	}
	h := sha256.New()
	fmt.Fprintf(h, "fugaro-cache-v1\nbase %s\npaths %s\n", baseImage, strings.Join(e.Paths, "\x00"))
	for _, f := range files {
		fh := sha256.New()
		file, err := os.Open(filepath.Join(root, f))
		if err != nil {
			return "", false, err
		}
		_, err = io.Copy(fh, file)
		file.Close()
		if err != nil {
			return "", false, err
		}
		fmt.Fprintf(h, "file %s %x\n", f, fh.Sum(nil))
	}
	return hex.EncodeToString(h.Sum(nil)), true, nil
}

// Resolve turns fugaro.yaml cache paths into absolute directories: "~/x"
// is under home, "x" under the checkout. Absolute paths and ".." are
// refused, so a cache can never target the system.
func Resolve(paths []string, root, home string) ([]string, error) {
	out := make([]string, len(paths))
	for i, p := range paths {
		base, rel := root, p
		if strings.HasPrefix(p, "~/") {
			base, rel = home, strings.TrimPrefix(p, "~/")
		}
		clean := path.Clean(rel)
		if rel == "" || path.IsAbs(rel) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return nil, fmt.Errorf("cache path %q must be ~/<dir> or a directory inside the checkout", p)
		}
		out[i] = filepath.Join(base, filepath.FromSlash(clean))
	}
	return out, nil
}

// ObjectKey is where key's archive lives.
func ObjectKey(slug, workflow, key string) string {
	return "cache/" + slug + "/" + workflow + "/" + key + ".tar.zst"
}

// ErrTooLarge means the cached paths exceed the size cap.
var ErrTooLarge = errors.New("cache archive exceeds the size cap")

// DefaultMaxBytes caps a cache's uncompressed size.
const DefaultMaxBytes int64 = 8 << 30
```

`internal/cache/archive.go` implements `Write` and `Extract` with `archive/tar` and `zstd`, following the format rules above.
- **`Write`:**
  - Write the manifest first.
  - For each root *i* that exists (skip a missing one), `filepath.WalkDir` it and write each regular file, directory and symlink as `fmt.Sprintf("%d/%s", i, rel)`. Skip every other file type. Count regular-file bytes, and return `ErrTooLarge` once the total passes `maxBytes`.
  - Close the tar writer and then the zstd writer.
- **`Extract`:**
  - `os.MkdirAll` each root, then `os.OpenRoot` it; close the `*os.Root`s when done.
  - Read the manifest and require that it is the first entry.
  - For each entry, split the name at the first `/` into the index and `rel`. Require `0 <= index < roots` and `filepath.IsLocal(filepath.FromSlash(rel))`; every file operation below goes through `root := roots[index]`'s `*os.Root`, never through a joined path.
  - A directory: `root.MkdirAll(rel, 0o755)`.
  - A regular file: `root.MkdirAll(filepath.Dir(rel), 0o755)`; if `root.Lstat(rel)` finds anything that is not a directory, `root.Remove(rel)` it first (this unlinks a symlink instead of following it); then `root.OpenFile(rel, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)` with `mode` `0o644`, or `0o755` if any execute bit was set. `O_EXCL` fails rather than follow a link that appeared in between. Copy through a `LimitReader` against the remaining byte budget.
  - A symlink: refuse an absolute `Linkname`, and one whose `filepath.Join(filepath.Dir(rel), linkname)` is not local, with an error naming "outside"; then remove anything at `rel` and `root.Symlink(linkname, rel)`. Even a link that passes this check can't be used to escape later, because `os.Root` checks every path it resolves.
  - Refuse every other type flag.
`internal/cache/cache.go`:

```go
package cache

import (
	"context"
	"errors"
	"fmt"

	"gocloud.dev/blob"
	"gocloud.dev/gcerrors"

	"github.com/dimipaun/fugaro/internal/blobx"
)

// Store is one workflow's caches in the runs bucket.
type Store struct {
	Bucket         *blobx.Bucket
	Slug, Workflow string
	MaxBytes       int64 // zero means DefaultMaxBytes
}

func (s *Store) max() int64 {
	if s.MaxBytes > 0 {
		return s.MaxBytes
	}
	return DefaultMaxBytes
}

// Restore extracts key's archive into roots. A missing archive is a miss
// (false, nil); any other failure is (false, err), which callers log and
// carry on from: caches only save time.
func (s *Store) Restore(ctx context.Context, key string, roots []string) (bool, error) {
	obj := ObjectKey(s.Slug, s.Workflow, key)
	r, err := s.Bucket.NewReader(ctx, obj, nil)
	if gcerrors.Code(err) == gcerrors.NotFound {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("opening %s: %w", obj, err)
	}
	defer r.Close()
	if err := Extract(r, roots, s.max()); err != nil {
		return false, fmt.Errorf("restoring %s: %w", obj, err)
	}
	_ = s.Bucket.Touch(ctx, obj, nowFunc()) // best effort: keeps a used cache past the lifecycle rule
	return true, nil
}

// Save archives roots as key unless that archive already exists. It
// returns saved=false, nil when it does.
func (s *Store) Save(ctx context.Context, key string, roots []string) (bool, error) {
	obj := ObjectKey(s.Slug, s.Workflow, key)
	if ok, err := s.Bucket.Exists(ctx, obj); err != nil || ok {
		return false, err
	}
	if !hasFiles(roots) {
		return false, nil // an empty archive would block this key forever
	}
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w, err := s.Bucket.NewWriter(wctx, obj, &blob.WriterOptions{ContentType: "application/zstd", IfNotExist: true})
	if err != nil {
		return false, err
	}
	if _, err := Write(w, roots, s.max()); err != nil {
		cancel() // abort: gocloud discards a write whose context is cancelled before Close
		_ = w.Close()
		return false, err
	}
	if err := w.Close(); err != nil {
		if gcerrors.Code(err) == gcerrors.FailedPrecondition {
			return false, nil // a parallel run wrote the same key first
		}
		return false, err
	}
	// The lifecycle rule deletes caches by custom time (not used in 30 days),
	// and an object without one never matches it, so set it at birth too.
	_ = s.Bucket.Touch(ctx, obj, nowFunc())
	return true, nil
}
```

Define `var nowFunc = time.Now`, and `hasFiles(roots []string) bool`, which walks the roots and returns true at the first regular file (a missing root counts as empty).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/cache/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/cache
git commit -m "cache: add content-addressed tar.zst caches with safe restore"
```

---

### Task 6: The runner gets the branch lock, caches, a real `writeback` stage, and protection against duplicate executions

**Files:**
- Modify: `internal/runner/runner.go`: `Deps`, `bootstrap`, `Run`, and the `finalize` tail
- Create: `internal/runner/lockcache.go`, `internal/runner/lockcache_test.go` (package `runner_test`, using the existing harness)
- Modify: `internal/runner/runner_test.go`: harness helpers only
- Modify: `internal/cli/exec.go`: open the bucket with `blobx.Open`; build the canonical execution name; set `Bucket`, `Execution` and `BaseImage`; exit 2 on a duplicate
- Modify: `images/derived/Dockerfile.tmpl`: `ENV FUGARO_BASE_IMAGE`
- Modify: `internal/image/render_test.go`: the expected output gains the `ARG`/`ENV` lines

**Interfaces:**
- Consumes: `lock.Acquire`, `lock.Key`, `lock.BusyError` and `(*lock.Lock).Release` (Task 4); `cache.KeyOf`, `cache.Resolve`, `cache.Store` and `cache.ErrTooLarge` (Task 5); `config.DefaultCache`; `runstore.Record.Execution`, `Record.Deadline` and `(*Store).CreateRecord` (Task 3); `blobx.Bucket` (Task 4); `backend.ExecID` and `backend.SameExecution` (Task 1)
- Produces:
  - new `runner.Deps` fields: `Bucket *blobx.Bucket`, `Execution string`, `BaseImage string` and `CacheMaxBytes int64`
  - `runner.ErrDuplicateExecution`
  - `result.json`'s `stage` is `writeback` while caches are written and the lock is released, and stays `writeback` on a finished run
  - `result.json` gains `execution` (the canonical full name) and `deadline`

**Behavior:**
- **The execution name** (C-1). `fugaro exec` turns Cloud Run's short `CLOUD_RUN_EXECUTION` into the canonical full name, with `CLOUD_RUN_JOB`, `FUGARO_PROJECT` and `FUGARO_REGION` (the job sets the last two, Task 17): `projects/<FUGARO_PROJECT>/locations/<FUGARO_REGION>/jobs/<CLOUD_RUN_JOB>/executions/<CLOUD_RUN_EXECUTION>`. `backend.ExecutionFromEnv` (Task 1) does this; with `CLOUD_RUN_EXECUTION` set and any of the others missing, exec fails before touching the bucket. Every comparison uses `backend.SameExecution`.
- **Duplicate execution** (Review Focus 2, I-10). This runs only when `Deps.Execution` is non-empty, which it is on Cloud Run.
  - Right after `ReadTask`, the first `result.json` is written with `CreateRecord` (create-if-absent), not `WriteRecord`.
  - `ErrExists` means another execution got there first. `Run` reads that record; unless it names the same execution (`SameExecution`), or when it can't be read, `Run` returns `ErrDuplicateExecution` **having written nothing**. The deferred finalizer skips its `WriteRecord` for this error.
  - Because the record is created before the lock, two executions of one run are always told apart at the record. The lock check (lock held by the same `run_id`, a different execution → `ErrDuplicateExecution`) is defensive: it is reachable only if the first execution's record vanished, and in that case the duplicate has already created its own first record, which stays `running` until the lock expires and `ls` shows `infra_error`. The Review Focus line says exactly this.
  - Local runs (`Execution` empty) keep today's `WriteRecord`.
- **Deadline.** Once `fugaro.yaml` is loaded, `result.json` gets `deadline = started_at + total + 3m`, the lock's expiry. `ls` uses it (Task 11, minor 14).
- **Lock.**
  - It is acquired after `fugaro.yaml` is loaded and the workflow is selected, because the expiry needs `timeouts.total`. That is before `FetchBase`, before the provider is used for anything but auth, and before any agent work.
  - The key is `lock.Key(slug, branch)`, and `ExpiresAt` is `StartedAt + total + 2m + 1m`: the task timeout plus a minute.
  - A live lock held by another run is `infra_error` with reason `branch busy: …`, the same as today's error path.
  - Nothing before the lock changes remote state: clone and checkout are local.
  - The design says step 2 of bootstrap; Task 19 records the reordering.
- **Cache restore** happens after the lock, from `wf.Cache`, or from `config.DefaultCache(wf.Base, WorkDir)` when that is empty.
  - Each entry's key comes from the checkout at the task's ref and `Deps.BaseImage`. Paths are resolved against `WorkDir` and the environment's `HOME`.
  - It is bounded by `min(10m, total/6)`.
  - Every outcome is logged at Info (`cache restored`, `cache miss`, `cache skipped: no key file`) or Warn (`cache restore failed`), and none of them fails the run.
- **Writeback** runs after a successful finalize. A cancelled run (`r.cancelled`) skips the cache write-back and only releases the lock, so a `fugaro cancel` never waits on an 8 GiB upload (I-4).
  - The stage becomes `writeback`, and the record is saved.
  - Each key is recomputed from the final tree: the agent may have changed a lockfile, and then the new dependencies belong under the new key. `Save` runs for each one.
  - The lock is released at the end.
  - It is bounded by `StartedAt + total + 90s`, and gets at least 20s, which leaves the task timeout's remaining 30s for the final record.
  - Failures are warnings.
- **The lock is also released in `Run`'s deferred path** whenever it is still held, on any exit, before the final `WriteRecord`.
- `finalize` no longer sets `r.rec.Stage = "writeback"` itself.

- [ ] **Step 1: Write the failing tests** — `internal/runner/lockcache_test.go`

```go
package runner_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/agent"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/cache"
	"github.com/dimipaun/fugaro/internal/gitprov"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/runner"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// cacheYAML is the fixture config with one cache entry under HOME.
const cacheYAML = `version: 1
git: { provider: github, base_branch: main }
agent: { auth: api-key, review_rounds: 1 }
workflows:
  app:
    base: web-node
    commands:
      build: sh build.sh
      test: sh test.sh
      reports: ["build/test-results/*.xml"]
    cache:
      - { key: [README.md], paths: ["~/.fugaro-test-cache"] }
    secrets:
      - { name: fixture-fails, env: FIXTURE_FAILS_FILE }
    timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }
`

func withBucket(h *harness) *blobx.Bucket {
	b := blobx.Wrap(h.bucket)
	h.deps.Bucket = b
	return b
}

func TestLockHeldDuringRunThenReleased(t *testing.T) {
	h := newHarness(t, "", nil)
	b := withBucket(h)
	key := lock.Key("acme-app", "fugaro/"+runID)
	held := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if ok, _ := b.Exists(ctx, key); !ok {
			t.Error("the branch lock is not held during implement")
		}
		return implement("feature")(t, ctx, req)
	}
	rec, err := h.run(t, held, review("ship", 0))
	if err != nil || rec.Stage != "writeback" || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if ok, _ := b.Exists(context.Background(), key); ok {
		t.Fatal("the lock survives the run")
	}
}

func TestBranchBusyIsInfraError(t *testing.T) {
	h := newHarness(t, "", nil)
	b := withBucket(h)
	key := lock.Key("acme-app", "fugaro/"+runID)
	if _, err := lock.Acquire(context.Background(), b, key, lock.Holder{RunID: "20260101-000000-ffff", ExpiresAt: time.Now().Add(time.Hour)}, time.Now()); err != nil {
		t.Fatal(err)
	}
	rec, err := h.run(t) // no agent steps may run
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "branch busy") {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if ok, _ := b.Exists(context.Background(), key); !ok {
		t.Fatal("a busy run released someone else's lock")
	}
}

const (
	exec1 = "projects/proj-1234/locations/us-east5/jobs/fugaro-acme-app-app/executions/fugaro-acme-app-app-aaaaa"
	exec2 = "projects/proj-1234/locations/us-east5/jobs/fugaro-acme-app-app/executions/fugaro-acme-app-app-bbbbb"
	// exec1 as the API may spell it, with the project number.
	exec1ByNumber = "projects/123456789/locations/us-east5/jobs/fugaro-acme-app-app/executions/fugaro-acme-app-app-aaaaa"
)

func TestDuplicateExecutionWritesNothing(t *testing.T) {
	h := newHarness(t, "", nil)
	withBucket(h)
	first := &runstore.Record{Version: 1, RunID: runID, Repo: "acme/app", Execution: exec1, Status: runstore.StatusRunning, Stage: "implement", Outcome: runstore.OutcomeNone}
	if err := h.store.WriteRecord(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	h.deps.Execution = exec2
	_, err := h.run(t)
	if !errors.Is(err, runner.ErrDuplicateExecution) {
		t.Fatalf("err = %v", err)
	}
	stored, _ := h.store.ReadRecord(context.Background())
	if stored.Execution != exec1 || stored.Status != runstore.StatusRunning || stored.Stage != "implement" {
		t.Fatalf("the duplicate overwrote result.json: %+v", stored)
	}
	if len(h.provider.State.PRs) != 0 {
		t.Fatal("the duplicate opened a PR")
	}
}

func TestSameExecutionSpelledDifferentlyIsNotADuplicate(t *testing.T) {
	h := newHarness(t, "", nil)
	withBucket(h)
	// A record whose execution came back from the API with the project number.
	prev := &runstore.Record{Version: 1, RunID: runID, Repo: "acme/app", Execution: exec1ByNumber, Status: runstore.StatusRunning, Stage: "bootstrap", Outcome: runstore.OutcomeNone}
	_ = h.store.WriteRecord(context.Background(), prev)
	h.deps.Execution = exec1
	if rec, err := h.run(t, implement("feature"), review("ship", 0)); err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

// TestDuplicateExecutionLosesLock covers the defensive path: the first
// execution's record is gone but its lock is held. The duplicate has
// created its own first record by then; it must not go further, mark the
// run infra_error, or release the other execution's lock.
func TestDuplicateExecutionLosesLock(t *testing.T) {
	h := newHarness(t, "", nil)
	b := withBucket(h)
	key := lock.Key("acme-app", "fugaro/"+runID)
	if _, err := lock.Acquire(context.Background(), b, key, lock.Holder{RunID: runID, Execution: exec1, ExpiresAt: time.Now().Add(time.Hour)}, time.Now()); err != nil {
		t.Fatal(err)
	}
	h.deps.Execution = exec2
	_, err := h.run(t)
	if !errors.Is(err, runner.ErrDuplicateExecution) {
		t.Fatalf("err = %v", err)
	}
	stored, _ := h.store.ReadRecord(context.Background())
	if stored.Status != runstore.StatusRunning || stored.Execution != exec2 || stored.Stage != "bootstrap" {
		t.Fatalf("the duplicate went past its first record: %+v", stored)
	}
	if ok, _ := b.Exists(context.Background(), key); !ok {
		t.Fatal("the duplicate released the first execution's lock")
	}
}

func TestRecordsExecutionAndDeadline(t *testing.T) {
	h := newHarness(t, "", nil)
	h.deps.Execution = exec1
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Execution != exec1 {
		t.Fatalf("rec.Execution = %q, err = %v", rec.Execution, err)
	}
	// The fixture's total is 5m: deadline = started_at + 5m + 3m.
	if rec.Deadline == nil || !rec.Deadline.Equal(rec.StartedAt.Add(8*time.Minute)) {
		t.Fatalf("deadline = %v, started %v", rec.Deadline, rec.StartedAt)
	}
}

func TestCancelledRunSkipsCacheWriteback(t *testing.T) {
	h := newHarness(t, cacheYAML, nil)
	b := withBucket(h)
	home := envValue(h.deps.Env, "HOME")
	cancelAfterFilling := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		_ = os.MkdirAll(filepath.Join(home, ".fugaro-test-cache"), 0o755)
		_ = os.WriteFile(filepath.Join(home, ".fugaro-test-cache", "pkg.tgz"), []byte("deps"), 0o644)
		_ = h.store.RequestCancel(context.Background())
		return blockUntilDone(t, ctx, req)
	}
	rec, err := h.run(t, cancelAfterFilling)
	if err != nil || rec.Status != runstore.StatusCancelled {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	it := b.List(&blob.ListOptions{Prefix: "cache/"})
	if obj, err := it.Next(context.Background()); err == nil {
		t.Fatalf("a cancelled run wrote a cache: %s", obj.Key)
	}
	if ok, _ := b.Exists(context.Background(), lock.Key("acme-app", "fugaro/"+runID)); ok {
		t.Fatal("a cancelled run kept its lock")
	}
}

func TestCachesRestoredAndWrittenBack(t *testing.T) {
	h := newHarness(t, cacheYAML, nil)
	b := withBucket(h)
	h.deps.BaseImage = "base@sha256:abc"
	home := envValue(h.deps.Env, "HOME")
	cacheDir := filepath.Join(home, ".fugaro-test-cache")
	fill := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cacheDir, "pkg.tgz"), []byte("deps"), 0o644); err != nil {
			t.Fatal(err)
		}
		return implement("feature")(t, ctx, req)
	}
	if _, err := h.run(t, fill, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	it := b.List(nil)
	var archives int
	for obj, err := it.Next(context.Background()); err == nil; obj, err = it.Next(context.Background()) {
		if strings.HasPrefix(obj.Key, "cache/acme-app/app/") && strings.HasSuffix(obj.Key, ".tar.zst") {
			archives++
		}
	}
	if archives != 1 {
		t.Fatalf("%d cache archives written, want 1", archives)
	}

	// A second run on the same bucket restores the cache before implement.
	if err := os.RemoveAll(cacheDir); err != nil {
		t.Fatal(err)
	}
	const run2 = "20260926-231530-bcde"
	spec := &task.Spec{Version: 1, RunID: run2, Repo: "acme/app", Ref: "main", Task: "Again"}
	h.deps.Store = runstore.Open(h.bucket, "acme-app", run2)
	if err := h.deps.Store.WriteTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	restored := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		if data, err := os.ReadFile(filepath.Join(cacheDir, "pkg.tgz")); err != nil || string(data) != "deps" {
			t.Errorf("cache not restored before implement: %q, %v", data, err)
		}
		return implement("again")(t, ctx, req)
	}
	if _, err := h.run(t, restored, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
}

func TestCorruptCacheDoesNotFailTheRun(t *testing.T) {
	h := newHarness(t, cacheYAML, nil)
	b := withBucket(h)
	// Plant garbage under the exact key bootstrap will compute.
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(h.files["README.md"]), 0o644); err != nil {
		t.Fatal(err)
	}
	key, ok, err := cache.KeyOf(root, cacheEntry(t, cacheYAML), "")
	if err != nil || !ok {
		t.Fatal(err)
	}
	_ = b.WriteAll(context.Background(), cache.ObjectKey("acme-app", "app", key), []byte("garbage"), nil)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil || rec.Status != runstore.StatusSucceeded {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
}

func TestAgentReadiedPRIsReturnedToDraft(t *testing.T) {
	h := newHarness(t, "", nil)
	h.fails(t, "beta")
	readyItself := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		res, err := implement("feature")(t, ctx, req)
		// The agent opens and readies its own PR (gh pr create && gh pr ready).
		h.provider.State.PRs = append(h.provider.State.PRs, fakePR(1, "fugaro/"+runID, false))
		return res, err
	}
	rec, err := h.run(t, readyItself, review("ship", 0))
	if err != nil || rec.Outcome != runstore.OutcomeDraft {
		t.Fatalf("rec = %+v, err = %v", rec, err)
	}
	if pr := onlyPR(t, h.provider); !pr.Draft {
		t.Fatal("finalize left the agent's ready PR ready on a failing run")
	}
}
```

Add these helpers to the test file:
- `cacheEntry(t, yaml) config.CacheEntry`: parse `yaml` with `config.Parse` and return `Workflows["app"].Cache[0]`.
- `fakePR(n int, branch string, draft bool) fake.PRState`: return `fake.PRState{PR: gitprov.PR{Number: n, URL: "https://example.invalid/pr/1", Draft: draft}, Spec: gitprov.PRSpec{Branch: branch}}`.

`h.fails(t, "beta")` makes the fixture's test `beta` fail, as in `TestFailingTestsOpenDraft`.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/runner/ -run 'Lock|BranchBusy|Duplicate|SameExecution|RecordsExecution|Caches|CorruptCache|CancelledRunSkips|AgentReadied'`
Expected: FAIL to compile (`Deps.Bucket`, `Deps.Execution`, `Deps.BaseImage` and `runner.ErrDuplicateExecution` are undefined). `TestAgentReadiedPRIsReturnedToDraft` may already pass once the file compiles, because `EnsurePR` re-asserts the draft state. Keep it: it pins that behavior.

- [ ] **Step 3: Implement**

`internal/runner/lockcache.go`:

```go
package runner

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/cache"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/lock"
	"github.com/dimipaun/fugaro/internal/task"
)

// ErrDuplicateExecution means another execution already owns this run
// (design §4.7). The duplicate exits without writing anything.
var ErrDuplicateExecution = errors.New("another execution already owns this run")

// taskTimeoutSlack is what Terraform (and the M4 bootstrap) add to
// timeouts.total for the Cloud Run task timeout (design §4.5).
const taskTimeoutSlack = 2 * time.Minute

// writebackGrace is how far past timeouts.total writeback may run: the
// task timeout's slack minus 30s for the final record.
const writebackGrace = 90 * time.Second

type cacheSlot struct {
	entry config.CacheEntry
	roots []string
	key   string // restored or looked up at bootstrap; "" when not cached
}

func (r *run) acquireLock(ctx context.Context) error {
	if r.d.Bucket == nil {
		return nil
	}
	h := lock.Holder{RunID: r.spec.RunID, Execution: r.d.Execution,
		ExpiresAt: r.rec.StartedAt.Add(r.wf.Timeouts.Total.Duration + taskTimeoutSlack + time.Minute)}
	l, err := lock.Acquire(ctx, r.d.Bucket, lock.Key(task.Slug(r.spec.Repo), r.rec.Branch), h, r.d.Now())
	var busy *lock.BusyError
	if errors.As(err, &busy) && busy.Holder.RunID == r.spec.RunID && r.d.Execution != "" && !backend.SameExecution(busy.Holder.Execution, r.d.Execution) {
		return ErrDuplicateExecution
	}
	if err != nil {
		return err
	}
	r.lock = l
	return nil
}

func (r *run) releaseLock(ctx context.Context) {
	if r.lock == nil {
		return
	}
	if err := r.lock.Release(context.WithoutCancel(ctx)); err != nil {
		r.d.Log.Warn("releasing the branch lock failed; it expires on its own", "err", err)
	}
	r.lock = nil
}

func (r *run) cacheStore() *cache.Store {
	return &cache.Store{Bucket: r.d.Bucket, Slug: task.Slug(r.spec.Repo), Workflow: r.rec.Workflow, MaxBytes: r.d.CacheMaxBytes}
}

// restoreCaches restores every cache entry it can; nothing here fails the run.
func (r *run) restoreCaches(ctx context.Context) {
	if r.d.Bucket == nil {
		return
	}
	entries := r.wf.Cache
	if len(entries) == 0 {
		var err error
		if entries, err = config.DefaultCache(r.wf.Base, r.d.WorkDir); err != nil {
			r.d.Log.Warn("no default cache", "err", err)
			return
		}
	}
	ctx, cancel := context.WithTimeout(ctx, min(10*time.Minute, r.wf.Timeouts.Total.Duration/6))
	defer cancel()
	store, home := r.cacheStore(), envLookup(r.d.Env, "HOME")
	for _, e := range entries {
		roots, err := cache.Resolve(e.Paths, r.d.WorkDir, home)
		if err != nil {
			r.d.Log.Warn("cache skipped", "err", err)
			continue
		}
		slot := cacheSlot{entry: e, roots: roots}
		key, ok, err := cache.KeyOf(r.d.WorkDir, e, r.d.BaseImage)
		switch {
		case err != nil:
			r.d.Log.Warn("cache skipped", "err", err)
		case !ok:
			r.d.Log.Info("cache skipped: no key file", "key_files", e.Key)
		default:
			slot.key = key
			hit, err := store.Restore(ctx, key, roots)
			switch {
			case err != nil:
				r.d.Log.Warn("cache restore failed", "key", key, "err", r.redact(err.Error()))
			case hit:
				r.d.Log.Info("cache restored", "key", key, "paths", e.Paths)
			default:
				r.d.Log.Info("cache miss", "key", key, "paths", e.Paths)
			}
		}
		r.caches = append(r.caches, slot)
	}
}

// writeback is the last stage (design §4.1): cache write-back, then the lock.
func (r *run) writeback(ctx context.Context) {
	r.rec.Stage = "writeback"
	r.save(ctx)
	deadline := r.rec.StartedAt.Add(r.wf.Timeouts.Total.Duration + writebackGrace)
	if floor := r.d.Now().Add(20 * time.Second); deadline.Before(floor) {
		deadline = floor
	}
	wctx, cancel := context.WithDeadline(context.WithoutCancel(ctx), deadline)
	defer cancel()
	if r.d.Bucket != nil && !r.cancelled { // a cancelled run only releases its lock
		store := r.cacheStore()
		for _, s := range r.caches {
			key, ok, err := cache.KeyOf(r.d.WorkDir, s.entry, r.d.BaseImage)
			if err != nil || !ok {
				continue
			}
			saved, err := store.Save(wctx, key, s.roots)
			switch {
			case errors.Is(err, cache.ErrTooLarge):
				r.d.Log.Warn("cache not written: too large", "key", key)
			case err != nil:
				r.d.Log.Warn("cache write-back failed", "key", key, "err", r.redact(err.Error()))
			case saved:
				r.d.Log.Info("cache written", "key", key)
			}
		}
	}
	r.releaseLock(wctx)
}

func envLookup(env []string, key string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if k, v, ok := strings.Cut(env[i], "="); ok && k == key {
			return v
		}
	}
	return ""
}

```

`internal/runner/runner.go`:
- **`Deps`** gains four fields:

```go
	// Bucket is the runs bucket, for the branch lock and caches
	// (design §3.3). Nil disables both.
	Bucket *blobx.Bucket
	// Execution is the canonical execution name (backend.ExecID.String()),
	// recorded in result.json; empty for local runs, which skips the
	// duplicate-execution check. Compare with backend.SameExecution.
	Execution string
	// BaseImage is FUGARO_BASE_IMAGE, the base the image was built FROM;
	// it is part of every cache key.
	BaseImage string
	// CacheMaxBytes caps a cache archive; zero means cache.DefaultMaxBytes.
	CacheMaxBytes int64
```

- **`run`** gains `lock *lock.Lock` and `caches []cacheSlot`.
- **`Run`'s deferred function** needs two changes:
  - First, when `errors.Is(err, ErrDuplicateExecution)`, log `d.Log.Error("duplicate execution; exiting without writing", …)` and return before the `WriteRecord`. Set `rec = nil`.
  - Otherwise call `r.releaseLock(ctx)` before the `WriteRecord`.
- **In `Run`**, after `r.finalize` succeeds, call `r.writeback(ctx)` before `return r.rec, nil`. Also make bootstrap's `ErrDuplicateExecution` come back unwrapped enough for `errors.Is`: keep `fmt.Errorf("bootstrap: %w", err)`.
- **`bootstrap`** changes in three places:
  1. Replace the `r.save(ctx)` that follows `r.spec = spec` with:

```go
	r.rec.Execution = r.d.Execution
	if r.d.Execution == "" {
		r.save(ctx)
	} else if err := r.d.Store.CreateRecord(ctx, r.rec); errors.Is(err, runstore.ErrExists) {
		prev, rerr := r.d.Store.ReadRecord(ctx)
		if rerr != nil || !backend.SameExecution(prev.Execution, r.d.Execution) {
			return ErrDuplicateExecution // nothing written
		}
		r.save(ctx) // this very execution restarted: carry on
	} else if err != nil {
		r.d.Log.Warn("writing the first run record failed", "err", err)
	}
```

  2. Set `r.rec.Branch = branch` where the branch is created, not only at the end. After `spec.Apply`, set `dl := r.rec.StartedAt.Add(wf.Timeouts.Total.Duration + taskTimeoutSlack + time.Minute)` and `r.rec.Deadline = &dl`.
  3. After `r.cfg, r.wf, r.rec.Workflow = cfg, wf, name`:

```go
	if err := r.acquireLock(ctx); err != nil {
		return err
	}
	r.restoreCaches(ctx)
```

- **`finalize`**: delete the line `r.rec.Stage = "writeback" // cache write-back arrives in M4`.

`internal/cli/exec.go`:
- Replace `blob.OpenBucket` with `bucket, err := blobx.Open(ctx, o.bucket)`, pass `bucket.Bucket` to `runstore.Open`, and set these `Deps` fields:

```go
		Bucket: bucket, Execution: execName, BaseImage: os.Getenv("FUGARO_BASE_IMAGE"),
```

  where `execName, err := backend.ExecutionFromEnv(os.Getenv)` (Task 1) runs before the bucket is opened; an error is exit 1.

`images/derived/Dockerfile.tmpl`: right after `FROM ${FUGARO_BASE}`, add:

```dockerfile
# The base as built FROM (Cloud Build passes its digest); cache keys use it.
ARG FUGARO_BASE
ENV FUGARO_BASE_IMAGE=${FUGARO_BASE}
```

Update `internal/image/render_test.go`'s expected output to include these lines. A repository Dockerfile doesn't get them. Its runs key caches on an empty base, which only means a base change doesn't invalidate them, and `docs/design/v1.md` §7.2 says so (Task 19).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/runner/ ./internal/cli/ ./internal/image/ ./images/ ./internal/e2e/`
Expected: PASS. The existing e2e tests use `file://` buckets, which now also exercise the lock's local fallback.

- [ ] **Step 5: Commit**

```bash
git add internal/runner internal/cli/exec.go images/derived/Dockerfile.tmpl internal/image/render_test.go
git commit -m "runner: take the branch lock, restore and write back caches, and make writeback a real stage"
```

---

### Task 7: Cost reporting: the price table, the `cost` breakdown, the PR report, and `result.schema.json`

**Files:**
- Create: `internal/backend/gcp/prices.go`, `internal/backend/gcp/prices_test.go`
- Create: `internal/runstore/cost.go`, `internal/runstore/cost_test.go`
- Modify: `internal/runstore/runstore.go` (`Record.Cost`)
- Create: `internal/runner/cost.go`
- Modify: `internal/runner/runner.go` (`Deps.Prices`; call `updateCost` after each stage and before the report), `internal/runner/report.go`, `internal/runner/pure_test.go`, `internal/runner/runner_test.go`
- Modify: `internal/cli/exec.go` (prices from `FUGARO_BACKEND`/`FUGARO_REGION`)
- Create: `schemas/result.schema.json`, and a test in `schemas/schemas_test.go`

**Interfaces:**
- Consumes: `backend.Prices`, `backend.MemoryGiB` (Task 1), `runstore.Record` (Task 3)
- Produces:
  - `gcp.ListPrices(region string) backend.Prices`
  - `runstore.Cost{ModelUSD, ComputeUSD, TotalUSD float64; Estimate bool; ModelBasis string}` with JSON keys `model_usd`, `compute_usd`, `total_usd`, `estimate` and `model_basis`
  - `runstore.BasisAPIList = "api-list"`, `runstore.BasisSubscription = "subscription"`
  - `runstore.ModelBasis(auth string) string`, `runstore.NewCost(modelUSD, computeUSD float64, basis string) runstore.Cost`
  - `runstore.Record.Cost *Cost` (JSON `cost,omitempty`)
  - `runner.Deps.Prices *backend.Prices`
  - `runner.CostLine(c runstore.Cost) string`, which `ls` and `diagnose` reuse

**Rulings:**
- **`total_usd` counts only dollars actually billed.** With `model_basis: subscription`, the model figure is notional, so `total_usd = compute_usd`, and `model_usd` is kept and shown as notional. With `api-list`, `total_usd = model_usd + compute_usd`.
  - Cost if wrong: flipping it is a one-line change in `NewCost` and its test.
  - Task 19 records this in design §10.1.
- **`estimate` is always true**, because storage, logging and builds are left out (§10.1).
- **The runner's compute figure** is elapsed wall time since `started_at` × the list price for the workflow's `resources`.
  - `ls` and `diagnose` recompute compute from the execution's billed duration, at the same list prices (Task 11). Local price overrides, for committed-use discounts, are deferred to M5's `fugaro init`.
- **The PR report lines:**
  - api-list: `**Cost:** ≈ $4.50 (model $4.12 + compute $0.38, estimate)`
  - subscription: `**Cost:** ≈ $0.38 compute (estimate); model $4.12 notional, counted against the Claude subscription`
  - no prices, as on a local run: `**Cost:** model $4.12 (compute not estimated)` for api-list, and `**Cost:** model $4.12 notional, counted against the Claude subscription (compute not estimated)` for subscription

- [ ] **Step 1: Write the failing tests**

`internal/backend/gcp/prices_test.go`:

```go
package gcp

import "testing"

func TestListPrices(t *testing.T) {
	p := ListPrices("us-east5")
	if p.VCPUSecondUSD != 0.000018 || p.GiBSecondUSD != 0.000002 || p.Source == "" {
		t.Fatalf("us-east5 = %+v", p)
	}
	unknown := ListPrices("mars-north1")
	if unknown.VCPUSecondUSD <= p.VCPUSecondUSD {
		t.Fatalf("an unknown region must fall back to the higher tier: %+v", unknown)
	}
}
```

`internal/runstore/cost_test.go`:

```go
package runstore

import "testing"

func TestNewCost(t *testing.T) {
	api := NewCost(4.12, 0.38, BasisAPIList)
	if api.TotalUSD != 4.50 || !api.Estimate || api.ModelBasis != "api-list" {
		t.Fatalf("api-list = %+v", api)
	}
	sub := NewCost(4.12, 0.38, BasisSubscription)
	if sub.TotalUSD != 0.38 || sub.ModelUSD != 4.12 {
		t.Fatalf("subscription = %+v", sub)
	}
	for auth, want := range map[string]string{"vertex": "api-list", "api-key": "api-list", "oauth": "subscription"} {
		if got := ModelBasis(auth); got != want {
			t.Errorf("ModelBasis(%s) = %s", auth, got)
		}
	}
}
```

Round `TotalUSD` to the cent in `NewCost` (`math.Round(x*100)/100`) so the test's float comparison is exact. Keep `ModelUSD` and `ComputeUSD` unrounded.

Add to `internal/runner/pure_test.go`:

```go
func TestCostLine(t *testing.T) {
	cases := map[string]runstore.Cost{
		"**Cost:** ≈ $4.50 (model $4.12 + compute $0.38, estimate)":                                       runstore.NewCost(4.12, 0.38, runstore.BasisAPIList),
		"**Cost:** ≈ $0.38 compute (estimate); model $4.12 notional, counted against the Claude subscription": runstore.NewCost(4.12, 0.38, runstore.BasisSubscription),
		"**Cost:** model $4.12 (compute not estimated)":                                                     runstore.NewCost(4.12, 0, runstore.BasisAPIList),
	}
	for want, c := range cases {
		if got := CostLine(c); got != want {
			t.Errorf("CostLine(%+v) = %q, want %q", c, got, want)
		}
	}
}
```

Add to `internal/runner/runner_test.go`:

```go
func TestCostBreakdown(t *testing.T) {
	h := newHarness(t, "", nil)
	h.deps.Prices = &backend.Prices{VCPUSecondUSD: 0.001, GiBSecondUSD: 0}
	// The clock moves only when a stage says so, 30s per stage, well inside
	// the fixture's 5m total, so the budget can never run out under the test.
	clock := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	h.deps.Now = func() time.Time { return clock }
	tick := func(s step) step {
		return func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
			clock = clock.Add(30 * time.Second)
			return s(t, ctx, req)
		}
	}
	rec, err := h.run(t, tick(implement("feature")), tick(review("ship", 0)))
	if err != nil {
		t.Fatal(err)
	}
	c := rec.Cost
	// 60s × 4 vCPU × $0.001 = $0.24 of compute; totals are compared to the cent.
	if c == nil || c.ModelUSD != 1.5 || c.ModelBasis != "api-list" || math.Abs(c.ComputeUSD-0.24) > 1e-9 || math.Abs(c.TotalUSD-1.74) > 0.005 {
		t.Fatalf("cost = %+v", c)
	}
	if !strings.Contains(onlyPR(t, h.provider).Comments[0], "(model $1.50 + compute $") {
		t.Fatalf("report = %s", onlyPR(t, h.provider).Comments[0])
	}
}
```

The fixture workflow uses the web-node defaults of 4 vCPU and 8Gi, so compute is positive. Keep `CostUSD` (§4.6) equal to the model sum.

In `schemas/schemas_test.go`, add `TestResultSchemaAcceptsRunnerRecords`. It marshals a fully populated `runstore.Record` (every field, `Cost` included, one `verify.Record`) and validates it against `schemas/result.schema.json` with `compile(t, "result.schema.json")`. Then it checks that the same JSON with `"status": "done"` fails.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/backend/gcp/ ./internal/runstore/ ./internal/runner/ ./schemas/`
Expected: FAIL (the new names are undefined, and the schema file is missing)

- [ ] **Step 3: Implement**

`internal/backend/gcp/prices.go`:

```go
package gcp

import (
	"slices"

	"github.com/dimipaun/fugaro/internal/backend"
)

// Cloud Run jobs list prices (instance-based billing), per second, from
// https://cloud.google.com/run/pricing (checked 2026-09-27). The free tier
// is ignored, so figures are an upper bound. Local overrides arrive in M5
// (design §10.1).
var (
	tier1 = backend.Prices{VCPUSecondUSD: 0.000018, GiBSecondUSD: 0.000002, Source: "Cloud Run list price, tier 1 (2026-09)"}
	tier2 = backend.Prices{VCPUSecondUSD: 0.000024, GiBSecondUSD: 0.0000025, Source: "Cloud Run list price, tier 2 (2026-09)"}

	tier1Regions = []string{
		"asia-east1", "asia-northeast1", "asia-northeast2", "europe-north1", "europe-southwest1",
		"europe-west1", "europe-west4", "europe-west8", "europe-west9", "me-west1",
		"us-central1", "us-east1", "us-east4", "us-east5", "us-south1", "us-west1",
	}
)

// ListPrices is Cloud Run's list price in region. An unknown region gets
// tier 2, the higher price, so an estimate errs high rather than low.
func ListPrices(region string) backend.Prices {
	if slices.Contains(tier1Regions, region) {
		return tier1
	}
	return tier2
}
```

Before committing, fetch `https://cloud.google.com/run/pricing` (WebFetch). Confirm the tier-1 and tier-2 per-second vCPU and memory prices for jobs, and the tier-1 region list. If the page won't render, read the SKUs from the Cloud Billing Catalog API instead: `gcloud billing` has no SKU command, so use `curl -s "https://cloudbilling.googleapis.com/v1/services?key=…"` to find the Cloud Run service ID and then `…/v1/services/<id>/skus`, with a key the user supplies, or an `Authorization: Bearer $(gcloud auth print-access-token)` header, sent through `curl --config` so the token stays out of argv. Correct the tables and the test if they differ, and put the date checked in the comment.

`internal/runstore/cost.go`:

```go
package runstore

import "math"

// Cost bases (design §10.1).
const (
	BasisAPIList      = "api-list"     // vertex or api-key: list-price billing
	BasisSubscription = "subscription" // oauth: notional; usage counts against plan limits
)

// Cost is result.json's cost breakdown.
type Cost struct {
	ModelUSD   float64 `json:"model_usd"`
	ComputeUSD float64 `json:"compute_usd"`
	TotalUSD   float64 `json:"total_usd"`
	Estimate   bool    `json:"estimate"`
	ModelBasis string  `json:"model_basis"`
}

// ModelBasis is the basis for an agent.auth mode.
func ModelBasis(auth string) string {
	if auth == "oauth" {
		return BasisSubscription
	}
	return BasisAPIList
}

// NewCost builds a Cost. TotalUSD counts only billed dollars: a
// subscription's model figure is notional and left out of it.
func NewCost(modelUSD, computeUSD float64, basis string) Cost {
	total := computeUSD
	if basis != BasisSubscription {
		total += modelUSD
	}
	return Cost{ModelUSD: modelUSD, ComputeUSD: computeUSD, TotalUSD: math.Round(total*100) / 100, Estimate: true, ModelBasis: basis}
}
```

Add `Cost *Cost` with the JSON tag `cost,omitempty` to `Record`, after `CostUSD`.

`internal/runner/cost.go`:

```go
package runner

import (
	"fmt"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/runstore"
)

// updateCost refreshes the record's cost breakdown from the model spend so
// far and the elapsed compute time at list price.
func (r *run) updateCost() {
	if r.cfg == nil {
		return
	}
	var compute float64
	if r.d.Prices != nil {
		if gib, err := backend.MemoryGiB(r.wf.Resources.Memory); err == nil {
			compute = r.d.Prices.ComputeUSD(float64(r.wf.Resources.CPU), gib, r.d.Now().Sub(r.rec.StartedAt))
		}
	}
	c := runstore.NewCost(r.rec.CostUSD, compute, runstore.ModelBasis(r.cfg.Agent.Auth))
	r.rec.Cost = &c
}

// CostLine renders a cost breakdown for reports (design §10.1).
func CostLine(c runstore.Cost) string {
	sub := c.ModelBasis == runstore.BasisSubscription
	switch {
	case c.ComputeUSD == 0 && sub:
		return fmt.Sprintf("**Cost:** model $%.2f notional, counted against the Claude subscription (compute not estimated)", c.ModelUSD)
	case c.ComputeUSD == 0:
		return fmt.Sprintf("**Cost:** model $%.2f (compute not estimated)", c.ModelUSD)
	case sub:
		return fmt.Sprintf("**Cost:** ≈ $%.2f compute (estimate); model $%.2f notional, counted against the Claude subscription", c.ComputeUSD, c.ModelUSD)
	}
	return fmt.Sprintf("**Cost:** ≈ $%.2f (model $%.2f + compute $%.2f, estimate)", c.TotalUSD, c.ModelUSD, c.ComputeUSD)
}
```

`runner.go`:
- Add `Prices *backend.Prices` to `Deps`, documented as the backend's list prices for the job's compute, with nil meaning compute is not estimated.
- Call `r.updateCost()` after `r.rec.CostUSD += res.CostUSD` in `stage`, and again in `finalize` right before `Report(...)`.

`report.go`: replace `fmt.Fprintf(&b, "**Cost:** $%.2f\n\n", rec.CostUSD)` with:

```go
	if rec.Cost != nil {
		b.WriteString(CostLine(*rec.Cost) + "\n\n")
	} else {
		fmt.Fprintf(&b, "**Cost:** $%.2f\n\n", rec.CostUSD)
	}
```

`exec.go`, after reading the environment:

```go
	var prices *backend.Prices
	if os.Getenv("FUGARO_BACKEND") == "cloud-run" {
		p := gcp.ListPrices(os.Getenv("FUGARO_REGION"))
		prices = &p
	}
```

Pass `Prices: prices` in `Deps`. The `cli` package may import `backend/gcp`; the runner may not.

`schemas/result.schema.json` is draft 2020-12 with `$id` `https://raw.githubusercontent.com/dimipaun/fugaro/main/schemas/result.schema.json`. Its properties are exactly those of `runstore.Record`:
- `version`, which is const 1
- `run_id`
- `repo`
- `workflow`
- `execution`
- `status`, an enum of the five statuses
- `stage`, an enum of `bootstrap`, `implement`, `review`, `fix`, `finalize` and `writeback`
- `outcome`, an enum
- `reason`
- `branch`
- `head_sha`
- `pr` (`number`, `url`)
- `reviews[]`
- `verify[]`: reference the fields `verify.Record` marshals, and read them from `internal/verify/verify.go`
- `cost_usd`
- `cost` (the five fields, with `model_basis` as an enum)
- `stages[]`
- `started_at`
- `deadline`
- `finished_at`

It sets `additionalProperties: false`, and requires `version`, `run_id`, `status`, `stage`, `outcome`, `cost_usd` and `started_at`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/backend/... ./internal/runstore/ ./internal/runner/ ./internal/cli/ ./schemas/ ./internal/e2e/`
Expected: PASS. Update any existing report assertion that matched `**Cost:** $`.

- [ ] **Step 5: Commit**

```bash
git add internal/backend/gcp/prices*.go internal/runstore internal/runner internal/cli/exec.go schemas
git commit -m "runner: report cost as model plus compute, with the Cloud Run price table and result.schema.json"
```

---

### Task 8: The live agent-event relay

**Files:**
- Create: `internal/agent/relay.go`, `internal/agent/relay_test.go`
- Modify: `internal/runner/runner.go` (`stage`: add the relay behind the transcript redactor), `internal/runner/runner_test.go`

**Interfaces:**
- Consumes: `logtail.Clip`, `agent.NewRedactor`
- Produces:
  - `agent.NewRelay(log *slog.Logger, secrets []string) *Relay`, where `*Relay` is an `io.Writer`. It redacts every message again after JSON decoding: a secret the agent printed with `\uXXXX` escapes gets past the line redactor in front of it and only becomes plain text when decoded.
  - `(*Relay).Flush()`
  - log entries with `stream: agent` and `event` set to one of `init`, `text`, `tool`, `tool_error` or `result`

Today the agent's stderr reaches Cloud Logging line by line, but its stream-json stdout, which holds everything the agent says and does, reaches only the transcript, uploaded when the stage ends. So `fugaro logs -f` is silent during a 40-minute stage. The relay parses each stream-json line after redaction and logs a concise event:
- `system` with subtype `init` → `session <id>, model <model>`
- `assistant` text blocks → the text, clipped to 2,000 bytes
- `assistant` `tool_use` blocks → `tool <name>: <summary>`. The summary is `input.command`, `input.file_path`, `input.pattern` or `input.description`, whichever comes first, and otherwise the input JSON. It is clipped to 300 bytes.
- `user` `tool_result` blocks with `is_error: true` → Warn, `tool error: <first line>`, clipped to 300 bytes
- `result` → `result <subtype>` with `cost_usd` and `is_error`

Tool results that succeeded are not logged, because they are bulk output that stays in the transcript. Unparseable lines are ignored, because they're in the transcript anyway.

- [ ] **Step 1: Write the failing tests**

`internal/agent/relay_test.go`:

```go
package agent

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestRelay(t *testing.T) {
	var buf bytes.Buffer
	r := NewRelay(slog.New(slog.NewJSONHandler(&buf, nil)), nil)
	lines := []string{
		`{"type":"system","subtype":"init","session_id":"s1","model":"claude-opus-5-5"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Reading the code."},{"type":"tool_use","name":"Bash","input":{"command":"fugaro verify test"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","is_error":false,"content":"lots of output"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"exit 1\nmore"}]}}`,
		`not json`,
		`{"type":"result","subtype":"success","total_cost_usd":0.5,"is_error":false}`,
	}
	for _, l := range lines {
		_, _ = r.Write([]byte(l + "\n"))
	}
	r.Flush()
	var events []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatal(err)
		}
		events = append(events, m)
	}
	want := []struct{ event, msg string }{
		{"init", "session s1, model claude-opus-5-5"},
		{"text", "Reading the code."},
		{"tool", "tool Bash: fugaro verify test"},
		{"tool_error", "tool error: exit 1"},
		{"result", "result success"},
	}
	if len(events) != len(want) {
		t.Fatalf("events = %v", events)
	}
	for i, w := range want {
		if events[i]["event"] != w.event || events[i]["msg"] != w.msg || events[i]["stream"] != "agent" {
			t.Errorf("event %d = %v, want %+v", i, events[i], w)
		}
	}
}

func TestRelayRedactsAfterDecoding(t *testing.T) {
	var buf bytes.Buffer
	r := NewRelay(slog.New(slog.NewJSONHandler(&buf, nil)), []string{"hunter2-token"})
	// "\u0068unter2-token" is "hunter2-token" once decoded: no byte-level redactor sees it.
	_, _ = r.Write([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"key \u0068unter2-token"}]}}` + "\n"))
	if strings.Contains(buf.String(), "hunter2-token") || !strings.Contains(buf.String(), "[REDACTED]") {
		t.Fatalf("logs = %s", buf.String())
	}
}

func TestRelayClipsLongText(t *testing.T) {
	var buf bytes.Buffer
	r := NewRelay(slog.New(slog.NewJSONHandler(&buf, nil)), nil)
	line, _ := json.Marshal(map[string]any{"type": "assistant", "message": map[string]any{"content": []any{map[string]any{"type": "text", "text": strings.Repeat("x", 10000)}}}})
	_, _ = r.Write(append(line, '\n'))
	if buf.Len() > 3000 {
		t.Fatalf("relay logged %d bytes for one event", buf.Len())
	}
}
```

Add to `internal/runner/runner_test.go`:

```go
func TestAgentEventsRelayedRedacted(t *testing.T) {
	h := newHarness(t, "", nil)
	var logs bytes.Buffer
	h.deps.Log = runner.NewLogger(&logs)
	talk := func(t *testing.T, ctx context.Context, req agent.Request) (agent.Result, error) {
		_, _ = req.Transcript.Write([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"my key is test-key"}]}}` + "\n"))
		return implement("feature")(t, ctx, req)
	}
	if _, err := h.run(t, talk, review("ship", 0)); err != nil {
		t.Fatal(err)
	}
	out := logs.String()
	if !strings.Contains(out, `"event":"text"`) || !strings.Contains(out, "my key is [REDACTED]") || strings.Contains(out, "test-key") {
		t.Fatalf("logs = %s", out)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/agent/ ./internal/runner/ -run 'Relay'`
Expected: FAIL (`NewRelay` is undefined, and no `event` entries are logged)

- [ ] **Step 3: Implement**

`internal/agent/relay.go`:

```go
package agent

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"

	"github.com/dimipaun/fugaro/internal/logtail"
)

const (
	relayTextBytes = 2000
	relayToolBytes = 300
)

// Relay logs a concise, live view of claude's stream-json output (design
// §10: stream "agent"). It sits behind the transcript's Redactor, and it
// redacts again after decoding, because JSON escapes (\u0074oken) hide a
// secret from a byte-level redactor.
type Relay struct {
	log     *slog.Logger
	secrets []string
	buf     []byte
}

// NewRelay returns a Relay logging to log, redacting secrets.
func NewRelay(log *slog.Logger, secrets []string) *Relay {
	return &Relay{log: log.With("stream", "agent"), secrets: secrets}
}

// msg redacts a decoded message, then clips it to n bytes. Redacting
// first means a secret straddling the cut is never half-published.
func (r *Relay) msg(s string, n int) string { return logtail.Clip(Redact(s, r.secrets), n) }

// Write buffers p and logs each complete line.
func (r *Relay) Write(p []byte) (int, error) {
	r.buf = append(r.buf, p...)
	for {
		i := bytes.IndexByte(r.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		r.line(r.buf[:i])
		r.buf = r.buf[i+1:]
	}
}

// Flush logs a trailing line without a newline.
func (r *Relay) Flush() {
	if len(r.buf) > 0 {
		r.line(r.buf)
		r.buf = nil
	}
}

type relayEvent struct {
	Type      string  `json:"type"`
	Subtype   string  `json:"subtype"`
	SessionID string  `json:"session_id"`
	Model     string  `json:"model"`
	IsError   bool    `json:"is_error"`
	Cost      float64 `json:"total_cost_usd"`
	Message   struct {
		Content []struct {
			Type    string          `json:"type"`
			Text    string          `json:"text"`
			Name    string          `json:"name"`
			Input   json.RawMessage `json:"input"`
			IsError bool            `json:"is_error"`
			Content json.RawMessage `json:"content"`
		} `json:"content"`
	} `json:"message"`
}

func (r *Relay) line(raw []byte) {
	var ev relayEvent
	if json.Unmarshal(bytes.TrimSpace(raw), &ev) != nil {
		return
	}
	switch ev.Type {
	case "system":
		if ev.Subtype == "init" {
			r.log.Info(r.msg("session "+ev.SessionID+", model "+ev.Model, relayToolBytes), "event", "init")
		}
	case "assistant":
		for _, c := range ev.Message.Content {
			switch c.Type {
			case "text":
				if t := strings.TrimSpace(c.Text); t != "" {
					r.log.Info(r.msg(t, relayTextBytes), "event", "text")
				}
			case "tool_use":
				r.log.Info(r.msg("tool "+c.Name+": "+toolSummary(c.Input), relayToolBytes), "event", "tool")
			}
		}
	case "user":
		for _, c := range ev.Message.Content {
			if c.Type == "tool_result" && c.IsError {
				first, _, _ := strings.Cut(contentText(c.Content), "\n")
				r.log.Warn(r.msg("tool error: "+first, relayToolBytes), "event", "tool_error")
			}
		}
	case "result":
		r.log.Info(r.msg("result "+ev.Subtype, relayToolBytes), "event", "result", "cost_usd", ev.Cost, "is_error", ev.IsError)
	}
}

func toolSummary(input json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(input, &m) == nil {
		for _, k := range []string{"command", "file_path", "pattern", "description"} {
			if s, ok := m[k].(string); ok && s != "" {
				return strings.Join(strings.Fields(s), " ")
			}
		}
	}
	return string(input)
}

// contentText flattens a tool_result's content, a string or a list of
// {type: text, text} blocks.
func contentText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct{ Text string }
	if json.Unmarshal(raw, &blocks) == nil {
		parts := make([]string, len(blocks))
		for i, b := range blocks {
			parts[i] = b.Text
		}
		return strings.Join(parts, "\n")
	}
	return ""
}
```

`logtail.Clip` clips on a UTF-8 boundary; check its signature, `Clip(line string, maxBytes int) string`. Note that the relay test expects the message key `msg`, which is slog's default. Under `runner.NewLogger`, the same entry's key is `message`.

In `runner.go` `stage`:

```go
	relay := agent.NewRelay(log, r.secrets)
	tw := agent.NewRedactor(io.MultiWriter(&transcript, transcriptTail, relay), r.secrets)
```

After `_ = tw.Flush()`, add `relay.Flush()`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/agent/ ./internal/runner/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/relay*.go internal/runner
git commit -m "agent: relay the agent's stream-json events to the run log as they happen"
```

---

### Task 9: The GCP backend: Cloud Run v2 (`Launch`, `Execution`, `List`, `Cancel`) and Cloud Logging (`Logs`)

**Files:**
- Create: `internal/backend/gcp/gcp.go`, `internal/backend/gcp/run.go`, `internal/backend/gcp/logs.go`
- Create: `internal/backend/gcp/run_test.go`, `internal/backend/gcp/logs_test.go`
- Create: `internal/gcpfake/run.go`, `internal/gcpfake/logging.go`

**Interfaces:**
- Consumes: `backend.*` and `gcp.JobName` (Task 1); `gcpfake.Server`, `writeJSON` and `writeError` (Task 4); `google.golang.org/api/run/v2`, `logging/v2` and `option`
- Produces:
  - `gcp.Endpoints{Run, Logging, SecretManager, CloudBuild string; NoAuth bool}`
  - `gcp.Options{Project, Region string; Endpoints Endpoints; HTTPClient *http.Client; LogSettle time.Duration}`
  - `gcp.New(ctx, Options) (*Backend, error)`. `*gcp.Backend` implements `backend.Backend`.
  - `(*Backend).JobPath(slug, workflow string) string`
  - `gcpfake.NewRun(t) *Run` with:
    - `AddJob(name string, cpu, memory string)`
    - `OnRun func(c gcpfake.RunCall)`, with `RunCall{Execution, Job string; Env map[string]string}`, where `Execution` is the **short** name, as Cloud Run hands `CLOUD_RUN_EXECUTION` to the container, called synchronously for every `:run`
    - `Start(job string) string`, which creates an execution directly, as another tool would
    - `SetState(exec string, s backend.State)`
    - `Executions() []string`
    - `ProjectNumber string`: when set, every name the fake returns uses it instead of the project ID, as the real API may
  - `gcpfake.NewLogging(t) *Logging` with `Add(execution string, e LogEntry)` and `AddJSONLines(execution string, data []byte)`, which parses slog JSON lines into entries. `execution` may be a full or a short name; the fake keys entries by job and short name, as the real labels do

**Mapping (Cloud Run v2 REST):**
- **Launch:** `POST v2/{job}:run`, where `{job}` is `projects/<p>/locations/<r>/jobs/<JobName>`.
  - The body is `{"overrides": {"containerOverrides": [{"env": [{"name": "FUGARO_RUN", "value": "<slug>/<id>"}]}]}}`. Cloud Run merges the override with the job's env. It needs `run.jobs.runWithOverrides`, which plain `run.invoker` lacks: M5's IAM must grant it to whoever runs `fugaro run` (the owner has it in M4).
  - The long-running operation completes only when the execution ends, so `Launch` never waits for it. It reads the Execution from `operation.metadata` (`name`, `logUri`).
  - A 404 on the job is `backend.ErrNotFound`, wrapped with "job <name> does not exist; create it with the bootstrap (M5: fugaro init)".
- **State:**
  - `completionTime` set, with `cancelledCount > 0` → cancelled
  - `completionTime` set, with `succeededCount > 0` and `failedCount == 0` → succeeded
  - `completionTime` set otherwise → failed
  - no `completionTime`, with `startTime` set or `runningCount > 0` → running
  - otherwise → pending
- **Resources:** `template.containers[0].resources.limits.cpu` is `"4"` or `"4000m"`, and `.memory` is something like `"8Gi"`.
- **List:** `GET v2/projects/<p>/locations/<r>/jobs/-/executions?pageSize=100`, or per job when `Jobs` is set, following `nextPageToken`.
  - With `jobs/-`, keep only jobs whose name starts with `fugaro-`.
  - Results are sorted newest first, so stop paging at the first execution created before `Since`.
- **Names (C-1, I-9).** Every name the backend returns (`ExecutionRef.Name`, `Execution.Name`) is rebuilt as `backend.ExecID{Project: o.Project, …}.String()` from whatever the API sent, so a project number never reaches `launch.json` or a comparison. Every name it accepts is parsed with `backend.ParseExecution` and rebuilt the same way before the call; an unparseable name is an error, not a request.
- **Execution:** `GET v2/{name}`.
- **Cancel:** `POST v2/{name}:cancel` with `{}`.
- **Logs:** `POST v2/entries:list` with:
  - `resourceNames: ["projects/<p>"]`
  - `filter: resource.type="cloud_run_job" AND resource.labels.job_name="<job>" AND labels."run.googleapis.com/execution_name"="<short name>" AND timestamp>="<since>"`, with job and short name from `ParseExecution`. `TestLiveListAndLogs` confirms that this label key returns entries on real Cloud Run.
  - `orderBy: "timestamp asc"`, `pageSize: 1000`
  - Each entry's `Message` is `jsonPayload.message`, else `textPayload`. `Fields` is `jsonPayload`.
- **Follow:**
  - Poll every `q.Poll` (default 3s) with `timestamp>=` the last seen time, dropping insert IDs already emitted at that time.
  - After each poll, check the execution. Once it is terminal, stop after `LogSettle` (default 30s) with no new entries, which allows for Cloud Logging's ingestion lag.
  - The context cancels the follow.

- [ ] **Step 1: Write the failing tests**

`internal/backend/gcp/run_test.go`:

```go
package gcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

func newTestBackend(t *testing.T) (*Backend, *gcpfake.Run, *gcpfake.Logging) {
	t.Helper()
	fr, fl := gcpfake.NewRun(t), gcpfake.NewLogging(t)
	b, err := New(context.Background(), Options{Project: "proj-1234", Region: "us-east5",
		Endpoints: Endpoints{Run: fr.URL + "/", Logging: fl.URL + "/", NoAuth: true}, LogSettle: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return b, fr, fl
}

func TestLaunchAndInspect(t *testing.T) {
	ctx := context.Background()
	b, fr, _ := newTestBackend(t)
	fr.AddJob("fugaro-acme-app-web", "4", "8Gi")
	var gotEnv map[string]string
	fr.OnRun = func(c gcpfake.RunCall) { gotEnv = c.Env }
	ref, err := b.Launch(ctx, backend.LaunchSpec{Repo: backend.RepoRef{Repo: "acme/app", Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd"})
	if err != nil {
		t.Fatal(err)
	}
	if gotEnv["FUGARO_RUN"] != "acme-app/20260927-100000-abcd" {
		t.Fatalf("env = %v", gotEnv)
	}
	if !strings.HasPrefix(ref.Name, "projects/proj-1234/locations/us-east5/jobs/fugaro-acme-app-web/executions/") || ref.LogURL == "" {
		t.Fatalf("ref = %+v", ref)
	}
	e, err := b.Execution(ctx, ref.Name)
	if err != nil || e.State != backend.StatePending || e.CPU != 4 || e.MemoryGiB != 8 {
		t.Fatalf("execution = %+v, %v", e, err)
	}
	fr.SetState(ref.Name, backend.StateRunning)
	active, err := b.List(ctx, backend.ListFilter{ActiveOnly: true})
	if err != nil || len(active) != 1 || active[0].State != backend.StateRunning {
		t.Fatalf("List active = %+v, %v", active, err)
	}
	if err := b.Cancel(ctx, ref.Name); err != nil {
		t.Fatal(err)
	}
	if e, _ := b.Execution(ctx, ref.Name); e.State != backend.StateCancelled || e.Completed.IsZero() {
		t.Fatalf("after Cancel = %+v", e)
	}
	if active, _ := b.List(ctx, backend.ListFilter{ActiveOnly: true}); len(active) != 0 {
		t.Fatalf("cancelled execution still active: %+v", active)
	}
}

func TestNamesAreCanonicalWhateverTheAPISends(t *testing.T) {
	ctx := context.Background()
	b, fr, _ := newTestBackend(t)
	fr.ProjectNumber = "123456789"
	fr.AddJob("fugaro-acme-app-web", "4", "8Gi")
	ref, err := b.Launch(ctx, backend.LaunchSpec{Repo: backend.RepoRef{Repo: "acme/app", Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd"})
	if err != nil || !strings.HasPrefix(ref.Name, "projects/proj-1234/") {
		t.Fatalf("ref = %+v, %v (want the project ID form)", ref, err)
	}
	list, _ := b.List(ctx, backend.ListFilter{})
	if len(list) != 1 || list[0].Name != ref.Name {
		t.Fatalf("List names = %+v", list)
	}
	// A name spelled with the number is accepted too.
	id, _ := backend.ParseExecution(ref.Name)
	id.Project = "123456789"
	if e, err := b.Execution(ctx, id.String()); err != nil || e.Name != ref.Name {
		t.Fatalf("Execution(number form) = %+v, %v", e, err)
	}
	if _, err := b.Execution(ctx, "fugaro-acme-app-web-1"); err == nil {
		t.Fatal("a short name was sent to the API")
	}
}

func TestLaunchMissingJob(t *testing.T) {
	b, _, _ := newTestBackend(t)
	_, err := b.Launch(context.Background(), backend.LaunchSpec{Repo: backend.RepoRef{Repo: "acme/app", Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd"})
	if !errors.Is(err, backend.ErrNotFound) || !strings.Contains(err.Error(), "fugaro-acme-app-web") {
		t.Fatalf("err = %v", err)
	}
}

func TestListIgnoresOtherJobsAndOldExecutions(t *testing.T) {
	ctx := context.Background()
	b, fr, _ := newTestBackend(t)
	fr.AddJob("fugaro-acme-app-web", "1", "512Mi")
	fr.AddJob("unrelated-job", "1", "512Mi")
	fr.Start("fugaro-acme-app-web")
	fr.Start("unrelated-job")
	got, err := b.List(ctx, backend.ListFilter{})
	if err != nil || len(got) != 1 || got[0].Job != "fugaro-acme-app-web" {
		t.Fatalf("List = %+v, %v", got, err)
	}
	if got, _ := b.List(ctx, backend.ListFilter{Since: time.Now().Add(time.Hour)}); len(got) != 0 {
		t.Fatalf("Since in the future listed %+v", got)
	}
}

func TestExecutionStateMapping(t *testing.T) {
	now := "2026-09-27T10:00:00Z"
	cases := []struct {
		e    execJSON
		want backend.State
	}{
		{execJSON{}, backend.StatePending},
		{execJSON{StartTime: now}, backend.StateRunning},
		{execJSON{StartTime: now, CompletionTime: now, SucceededCount: 1}, backend.StateSucceeded},
		{execJSON{StartTime: now, CompletionTime: now, FailedCount: 1}, backend.StateFailed},
		{execJSON{StartTime: now, CompletionTime: now, CancelledCount: 1}, backend.StateCancelled},
		{execJSON{CompletionTime: now}, backend.StateFailed}, // ended without any task outcome: never "succeeded"
	}
	for _, tc := range cases {
		if got := stateOf(tc.e.api()); got != tc.want {
			t.Errorf("%+v → %s, want %s", tc.e, got, tc.want)
		}
	}
}
```

Define `execJSON` in the test as a small struct with the timestamp and count fields, and an `api()` method that returns `*run.GoogleCloudRunV2Execution`.

`internal/backend/gcp/logs_test.go`:

```go
package gcp

import (
	"context"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
)

func TestLogsOnceAndFollow(t *testing.T) {
	ctx := context.Background()
	b, fr, fl := newTestBackend(t)
	fr.AddJob("fugaro-acme-app-web", "4", "8Gi")
	ref, _ := b.Launch(ctx, backend.LaunchSpec{Repo: backend.RepoRef{Repo: "acme/app", Slug: "acme-app"}, Workflow: "web", RunID: "20260927-100000-abcd"})
	fl.AddJSONLines(ref.Name, []byte(`{"time":"2026-09-27T10:00:01Z","severity":"INFO","message":"stage started","stage":"implement","run_id":"20260927-100000-abcd"}
{"time":"2026-09-27T10:00:02Z","severity":"WARNING","message":"cache miss","stage":"bootstrap"}
`))
	var got []backend.LogEntry
	collect := func(e backend.LogEntry) error { got = append(got, e); return nil }
	if err := b.Logs(ctx, backend.LogQuery{Execution: ref.Name}, collect); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Message != "stage started" || got[0].Fields["stage"] != "implement" || got[1].Severity != "WARNING" {
		t.Fatalf("entries = %+v", got)
	}

	// Follow: a late entry arrives, then the execution ends; follow returns
	// after LogSettle and never repeats an entry.
	got = nil
	go func() {
		time.Sleep(30 * time.Millisecond)
		fl.AddJSONLines(ref.Name, []byte(`{"time":"2026-09-27T10:00:03Z","severity":"INFO","message":"run finished"}`+"\n"))
		fr.SetState(ref.Name, backend.StateSucceeded)
	}()
	fctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := b.Logs(fctx, backend.LogQuery{Execution: ref.Name, Follow: true, Poll: 10 * time.Millisecond}, collect); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[2].Message != "run finished" {
		t.Fatalf("followed entries = %+v", got)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/backend/gcp/ -run 'Launch|List|StateMapping|Logs'`
Expected: FAIL (`New`, `Options` and the fakes are undefined)

- [ ] **Step 3: Implement**

`internal/backend/gcp/gcp.go`:

```go
package gcp

import (
	"context"
	"fmt"
	"net/http"
	"time"

	logging "google.golang.org/api/logging/v2"
	"google.golang.org/api/option"
	run "google.golang.org/api/run/v2"
)

// Endpoints override API roots, for fakes and emulators. Empty means the
// real Google endpoint.
type Endpoints struct {
	Run, Logging, SecretManager, CloudBuild string
	NoAuth                                  bool
}

// Options configure a Backend.
type Options struct {
	Project, Region string
	Endpoints       Endpoints
	HTTPClient      *http.Client  // optional
	LogSettle       time.Duration // follow's quiet period after the execution ends; zero means 30s
}

// Backend is the Cloud Run backend.
type Backend struct {
	o    Options
	run  *run.Service
	logs *logging.Service
}

func (o Options) client(endpoint string) []option.ClientOption {
	var opts []option.ClientOption
	if endpoint != "" {
		opts = append(opts, option.WithEndpoint(endpoint))
	}
	if o.Endpoints.NoAuth {
		opts = append(opts, option.WithoutAuthentication())
	}
	if o.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(o.HTTPClient))
	}
	if !o.Endpoints.NoAuth && o.Project != "" {
		// User ADC has no project of its own, and some APIs refuse it
		// without a quota project (minor 9).
		opts = append(opts, option.WithQuotaProject(o.Project))
	}
	return opts
}

// New connects to Cloud Run and Cloud Logging with Application Default
// Credentials (gcloud auth application-default login) unless NoAuth.
func New(ctx context.Context, o Options) (*Backend, error) {
	if o.LogSettle == 0 {
		o.LogSettle = 30 * time.Second
	}
	rs, err := run.NewService(ctx, o.client(o.Endpoints.Run)...)
	if err != nil {
		return nil, fmt.Errorf("connecting to Cloud Run: %w", err)
	}
	ls, err := logging.NewService(ctx, o.client(o.Endpoints.Logging)...)
	if err != nil {
		return nil, fmt.Errorf("connecting to Cloud Logging: %w", err)
	}
	return &Backend{o: o, run: rs, logs: ls}, nil
}
```

`run.go` implements `Launch`, `Execution`, `List`, `Cancel`, `stateOf`, `toExecution` and `parseCPU`, following the mapping above. `googleapi` errors with code 404 become `fmt.Errorf("…: %w", backend.ErrNotFound)`. Parse the operation metadata like this:

```go
	var meta struct {
		Name   string `json:"name"`
		LogURI string `json:"logUri"`
	}
	if err := json.Unmarshal(op.Metadata, &meta); err != nil || meta.Name == "" {
		return backend.ExecutionRef{}, fmt.Errorf("Cloud Run started %s but did not report the execution (operation %s)", job, op.Name)
	}
```

`logs.go` implements `Logs`, following the mapping above. Quote the execution name in the filter with `strconv.Quote`. Execution names are `[a-z0-9-]`, but quoting keeps the filter safe regardless.

`internal/gcpfake/run.go` keeps jobs and executions in memory, under a mutex:
- Execution names are `<job>/executions/<short job>-<n>`, with `n` starting at 1.
- `createTime` is `time.Now()`.
- The state is stored as `backend.State` and rendered to the API's counts and times in the JSON response. `running` sets `startTime`. A terminal state sets `completionTime` plus the matching count.
- Routes:
  - `POST /v2/projects/{p}/locations/{l}/jobs/{j}:run` returns 404 `NOT_FOUND` for an unknown job. Otherwise it creates the execution, calls `OnRun`, and answers `{"name": "<job>/operations/<n>", "metadata": {"@type": "type.googleapis.com/google.cloud.run.v2.Execution", "name": …, "logUri": "https://console.cloud.google.com/run/jobs/executions/details/…"}}`.
  - `GET /v2/{parent}/executions` accepts `jobs/-`, sorts newest first, and supports `pageSize` and `pageToken`: return 2 per page when `pageSize` is 2, which tests pagination cheaply.
  - `GET /v2/{execution}`
  - `POST /v2/{execution}:cancel` sets the state to cancelled.
- Anything else calls `s.unhandled`.
- `gcpfake` must not import `internal/backend/gcp`: it would create a cycle in tests. It may import `internal/backend` for `State`.

`internal/gcpfake/logging.go`:
- It stores entries per short execution name.
- `POST /v2/entries:list` decodes `{filter, orderBy, pageSize, pageToken}`. It extracts the execution name with `labels."run.googleapis.com/execution_name"="([^"]+)"` and the bound with `timestamp>="([^"]+)"`, and returns the matching entries in time order as `{"entries": [{"insertId", "timestamp", "severity", "jsonPayload", "labels": {"run.googleapis.com/execution_name": …}}]}`.
- A filter it can't parse fails the test.
- `AddJSONLines` parses each line's `time` and `severity`, and keeps the whole object as `jsonPayload`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/backend/... ./internal/gcpfake/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/backend/gcp internal/gcpfake
git commit -m "gcp: launch, inspect, list and cancel Cloud Run executions, and read their logs"
```

---

### Task 10: `fugaro run`: launch, `--run-id`, `--retry` and `--batch`, plus the shared cloud plumbing

**Files:**
- Create: `internal/cli/cloud.go`, `internal/cli/cloud_test.go`
- Create: `internal/cli/run.go`, `internal/cli/run_test.go`
- Modify: `internal/cli/root.go` (register `run`)

**Interfaces:**
- Consumes: `localcfg` (Task 2); `runstore.CreateTask`, `Claim`, `ClaimKey`, `WriteLaunch`, `ReadLaunch`, `Locate`, `ReadRecord` and `CancelRequested` (Task 3); `blobx.Read`, `ReplaceIf` and `DeleteIf` (Task 4); `backend.ExecutionFromEnv` (Task 1, test only); `backend.Backend` and `gcp.New` (Tasks 1 and 9); `task.NewRunID`; `image.HTTPSOrigin`; `blobx.Open` (Task 4)
- Produces:
  - `cli.cloudOptions{config, project, region string}` and `addCloudFlags(cmd *cobra.Command, o *cloudOptions)`, which add `--config`, `--project` and `--region`
  - `cli.cloudEnv{lc *localcfg.Config; bucket *blobx.Bucket; be backend.Backend; gcp gcp.Options}` and `openCloud(ctx, o cloudOptions) (*cloudEnv, error)`
  - `(*cloudEnv).Close()`
  - `(*cloudEnv).prices() backend.Prices`: `gcp.ListPrices(region)` (local overrides are M5)
  - `cli.remote(err error) error`, which wraps as exit 2, and `cli.userErr(format string, args ...any) error`, which is exit 1
  - `cli.originRepo(ctx) (string, error)`: the `owner/name` of `git remote get-url origin`
  - `cli.launchRun(ctx, env *cloudEnv, spec *task.Spec, now time.Time) (launchResult, error)`, where `launchResult{Run, Repo, RunID, Branch, Execution, LogURL, Status string}` and `Status` is `"launched"` or `"already-launched"`. `run`, `--retry` and the concurrency test share it.
  - `claimTTL = 10 * time.Minute`
  - `launchHooks{beforeClaim, beforeTakeover, afterClaim func()}`, a package variable only tests set

**`fugaro run [TEXT] [--repo R] [--ref REF] [--workflow W] [--run-id ID] [--batch NAME] [--task-file F|-] [--retry RUN] [--json]`:**
1. `--retry RUN` excludes every other task flag. It locates the run and reads `task.json`, and a missing one is exit 1.
   - A cancel marker gives exit 1, "run was cancelled; start a new one".
   - Otherwise it calls `launchRun` with that spec.
2. **Repo:** `--repo`, else the origin of the checkout you are in.
3. **Workflow:** `--workflow`, else the local config's single workflow for the repo, else the checkout's `fugaro.yaml` when its origin is the repo and it defines one workflow. Otherwise exit 1, "pass --workflow".
4. **Ref:** `--ref`, else the local config's `base_branch`, else the checkout's `git.base_branch`. Otherwise exit 1.
5. **Task text:** exactly one of `TEXT` and `--task-file` (a path, or `-` for stdin). Empty is exit 1.
6. The spec has `requested_by` set to `lc.Me()` and `batch` set to `--batch`, and the run ID is `--run-id` or a new one. `spec.Validate()`.
7. **`--pr N`** is registered but hidden, and exits 1 with "follow-up runs (--pr) arrive in M6". `TestRunPRPointsToM6` pins this.
8. **`max_parallel`:** `be.List(ActiveOnly)`. When its count is at least `lc.MaxParallel`, exit 1 with "N runs active; max_parallel is M". The check is skipped when the run ID has already launched.
9. `store.CreateTask(spec)`.
   - `ErrExists` loads the existing spec. If it is equal (compare `Marshal()` output) the call continues. If not, exit 1, "run ID %s already holds a different task".
10. **`launchRun`** is race-free (C-2, I-2); its doc comment below states the protocol:
    - `launch.json`, or `result.json` with an `execution` (backfilled into `launch.json`), means `already-launched`. That covers a CLI that died after `jobs.run`.
    - Take the claim with create-if-absent. **The claim is never deleted after a launch**, so no later caller can take it between another caller's checks, and whoever holds it writes `launch.json` before returning.
    - When the claim is held: re-read `launch.json` (the holder may have finished) → `already-launched`. Otherwise, a claim younger than `claimTTL` is exit 1, "may still be in flight"; an older one is taken over with `blobx.ReplaceIf` on the generation just read. Losing that race re-reads `launch.json`, and otherwise exits 1.
    - Having won, re-check `launch.json` and `result.json` once more, then `be.Launch`.
    - On a launch error, delete our own claim only (`DeleteIf` on the exact object we hold), so a retry can go at once.
    - `WriteLaunch`. `ErrExists` reads theirs and reports `already-launched`. A failed write keeps the claim, and `--retry` later backfills from `result.json`.
11. **Human output:** `launched <slug>/<id>`, then the indented lines `branch fugaro/<id>` and `logs <url>`. With `--json`, it prints `launchResult`.

- [ ] **Step 1: Write the failing tests**

`internal/cli/cloud_test.go` holds the shared test fixture that the Task 11–16 tests reuse:

```go
package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gocloud.dev/blob/memblob"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

// cloudFixture is a local config pointing at fakes and a file:// bucket.
type cloudFixture struct {
	dir     string
	bucket  string // file:// URL
	run     *gcpfake.Run
	logging *gcpfake.Logging
}

// newCloudFixture writes a local config whose endpoints point at the Run and
// Logging fakes, plus any extra "key: url" endpoint entries (secret_manager,
// cloud_build) a later test adds with its own fake.
func newCloudFixture(t *testing.T, extraEndpoints ...string) *cloudFixture {
	t.Helper()
	f := &cloudFixture{dir: t.TempDir(), run: gcpfake.NewRun(t), logging: gcpfake.NewLogging(t)}
	runs := filepath.Join(f.dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	f.bucket = "file://" + runs
	cfg := "version: 1\nproject: proj-1234\nregion: us-east5\nruns_bucket: unused-bucket\nbucket_url: " + f.bucket +
		"\nuser: someone@example.com\nmax_parallel: 2\n" +
		"endpoints: { run: " + f.run.URL + "/, logging: " + f.logging.URL + "/, " + extra(extraEndpoints) + "no_auth: true }\n" +
		"repos:\n  acme/app: { base_branch: main, workflows: [web] }\n"
	path := filepath.Join(f.dir, "config.yaml")
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FUGARO_CONFIG", path)
	f.run.AddJob("fugaro-acme-app-web", "4", "8Gi")
	return f
}

// appendConfig adds top-level YAML (such as registry: …) to the local config.
func (f *cloudFixture) appendConfig(t *testing.T, yaml string) {
	t.Helper()
	path := os.Getenv("FUGARO_CONFIG")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte(yaml)...), 0o600); err != nil {
		t.Fatal(err)
	}
}

func extra(entries []string) string {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e + ", ")
	}
	return b.String()
}

// memEnv builds a cloudEnv on a shared memblob bucket, for tests that call
// launchRun directly and concurrently (fileblob's IfNotExist is not atomic).
func memEnv(t *testing.T, f *cloudFixture) *cloudEnv {
	t.Helper()
	lc, err := localcfg.Load(os.Getenv("FUGARO_CONFIG"))
	if err != nil {
		t.Fatal(err)
	}
	be, err := gcp.New(context.Background(), gcp.Options{Project: lc.Project, Region: lc.Region,
		Endpoints: gcp.Endpoints{Run: f.run.URL + "/", Logging: f.logging.URL + "/", NoAuth: true}})
	if err != nil {
		t.Fatal(err)
	}
	return &cloudEnv{lc: lc, bucket: blobx.Wrap(memblob.OpenBucket(nil)), be: be}
}
```

`internal/cli/run_test.go`:

```go
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

func TestRunLaunches(t *testing.T) {
	f := newCloudFixture(t)
	var env map[string]string
	f.run.OnRun = func(c gcpfake.RunCall) { env = c.Env }
	out, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20260927-100000-abcd", "--batch", "tuesday", "--json", "Add a feature")
	if err != nil {
		t.Fatal(err)
	}
	var res launchResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.Status != "launched" || res.Branch != "fugaro/20260927-100000-abcd" {
		t.Fatalf("result = %+v (%s), %v", res, out, err)
	}
	if env["FUGARO_RUN"] != "acme-app/20260927-100000-abcd" {
		t.Fatalf("FUGARO_RUN = %q", env["FUGARO_RUN"])
	}
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	defer b.Close()
	s := runstore.Open(b, "acme-app", "20260927-100000-abcd")
	spec, err := s.ReadTask(context.Background())
	if err != nil || spec.RequestedBy != "someone@example.com" || spec.Batch != "tuesday" || spec.Ref != "main" || spec.Workflow != "web" {
		t.Fatalf("task = %+v, %v", spec, err)
	}
	if l, err := s.ReadLaunch(context.Background()); err != nil || l.Execution != res.Execution {
		t.Fatalf("launch = %+v, %v", l, err)
	}
}

func TestRunIdempotentRunID(t *testing.T) {
	f := newCloudFixture(t)
	args := []string{"run", "--repo", "acme/app", "--run-id", "20260927-100000-abcd", "--json", "Add a feature"}
	if _, _, err := execute(t, args...); err != nil {
		t.Fatal(err)
	}
	out, _, err := execute(t, args...)
	if err != nil || !strings.Contains(out, `"already-launched"`) {
		t.Fatalf("second run: %s, %v", out, err)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1", n)
	}
	_, _, err = execute(t, "run", "--repo", "acme/app", "--run-id", "20260927-100000-abcd", "A different task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "different task") {
		t.Fatalf("different task: %v", err)
	}
}

// TestRunConcurrentSameRunID is a smoke test on top of the deterministic
// race tests below: eight launches of one run ID start one execution.
func TestRunConcurrentSameRunID(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	spec := &task.Spec{Version: 1, RunID: "20260927-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x"}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := *spec
			_ = runstore.Open(env.bucket.Bucket, "acme-app", s.RunID).CreateTask(context.Background(), &s)
			_, _ = launchRun(context.Background(), env, &s, time.Now())
		}()
	}
	wg.Wait()
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions for one run ID, want 1", n)
	}
}

func setHooks(t *testing.T, before, takeover, after func()) {
	t.Helper()
	launchHooks.beforeClaim, launchHooks.beforeTakeover, launchHooks.afterClaim = before, takeover, after
	t.Cleanup(func() { launchHooks.beforeClaim, launchHooks.beforeTakeover, launchHooks.afterClaim = nil, nil, nil })
}

func raceSpec(t *testing.T, env *cloudEnv) *task.Spec {
	t.Helper()
	spec := &task.Spec{Version: 1, RunID: "20260927-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x"}
	if err := runstore.Open(env.bucket.Bucket, "acme-app", spec.RunID).CreateTask(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	return spec
}

// The winner launches completely between the loser's first check and its
// Claim, the window C-2 describes. The claim was never cleared, so the
// loser's Claim fails and it finds launch.json.
func TestLaunchWinnerFinishesBeforeLoserClaims(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	spec := raceSpec(t, env)
	now := time.Now()
	var winner launchResult
	setHooks(t, func() {
		launchHooks.beforeClaim = nil // the winner runs without hooks
		var err error
		if winner, err = launchRun(context.Background(), env, spec, now); err != nil {
			t.Errorf("winner: %v", err)
		}
	}, nil, nil)
	loser, err := launchRun(context.Background(), env, spec, now)
	if err != nil || loser.Status != "already-launched" || winner.Status != "launched" || loser.Execution != winner.Execution {
		t.Fatalf("winner %+v, loser %+v, %v", winner, loser, err)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1", n)
	}
}

// Two CLIs find the same stale claim. The first to replace it launches;
// the other's generation-matched replace fails, and it reports the launch.
func TestLaunchStaleClaimTakeoverHasOneWinner(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	spec := raceSpec(t, env)
	now := time.Now()
	s := runstore.Open(env.bucket.Bucket, "acme-app", spec.RunID)
	if ok, _, err := s.Claim(context.Background(), "dead-laptop/1/1", now.Add(-2*claimTTL)); !ok || err != nil {
		t.Fatal(ok, err)
	}
	setHooks(t, nil, func() {
		launchHooks.beforeTakeover = nil
		if res, err := launchRun(context.Background(), env, spec, now); err != nil || res.Status != "launched" {
			t.Errorf("first taker: %+v, %v", res, err)
		}
	}, nil)
	second, err := launchRun(context.Background(), env, spec, now)
	if err != nil || second.Status != "already-launched" {
		t.Fatalf("second taker: %+v, %v", second, err)
	}
	if n := len(f.run.Executions()); n != 1 {
		t.Fatalf("%d executions, want 1", n)
	}
}

// An earlier, now-stale holder launched and wrote launch.json just before
// our takeover: the re-check after taking the claim catches it.
func TestLaunchRechecksAfterTakingTheClaim(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	spec := raceSpec(t, env)
	s := runstore.Open(env.bucket.Bucket, "acme-app", spec.RunID)
	setHooks(t, nil, nil, func() {
		_ = s.WriteLaunch(context.Background(), &runstore.Launch{Version: 1, RunID: spec.RunID, Execution: "projects/proj-1234/locations/us-east5/jobs/fugaro-acme-app-web/executions/fugaro-acme-app-web-late"})
	})
	res, err := launchRun(context.Background(), env, spec, time.Now())
	if err != nil || res.Status != "already-launched" || len(f.run.Executions()) != 0 {
		t.Fatalf("res = %+v, err = %v, executions %v", res, err, f.run.Executions())
	}
}

func TestLaunchFailureReleasesOnlyOurClaim(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	spec := raceSpec(t, env)
	spec.Workflow = "missing" // no such job: Launch fails with ErrNotFound
	if _, err := launchRun(context.Background(), env, spec, time.Now()); ExitCode(err) != ExitUserError {
		t.Fatalf("err = %v", err)
	}
	spec.Workflow = "web"
	if res, err := launchRun(context.Background(), env, spec, time.Now()); err != nil || res.Status != "launched" {
		t.Fatalf("retry right after a failed launch: %+v, %v", res, err)
	}
}

func TestRetryBackfillsLaunchFromRecord(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	ctx := context.Background()
	spec := raceSpec(t, env)
	s := runstore.Open(env.bucket.Bucket, "acme-app", spec.RunID)
	// The CLI died after jobs.run: no launch.json, but the runner already
	// wrote result.json, with the name exactly as fugaro exec builds it from
	// Cloud Run's environment.
	recorded, err := backend.ExecutionFromEnv(func(k string) string {
		return map[string]string{"CLOUD_RUN_EXECUTION": "fugaro-acme-app-web-x7k2p", "CLOUD_RUN_JOB": "fugaro-acme-app-web", "FUGARO_PROJECT": "proj-1234", "FUGARO_REGION": "us-east5"}[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.WriteRecord(ctx, &runstore.Record{Version: 1, RunID: spec.RunID, Execution: recorded, Status: runstore.StatusRunning, Stage: "implement", Outcome: runstore.OutcomeNone})
	res, err := launchRun(ctx, env, spec, time.Now())
	if err != nil || res.Status != "already-launched" || len(f.run.Executions()) != 0 {
		t.Fatalf("res = %+v, err = %v, executions = %v", res, err, f.run.Executions())
	}
	l, err := s.ReadLaunch(ctx)
	if err != nil || l.Execution != recorded {
		t.Fatalf("backfilled launch = %+v, %v", l, err)
	}
	// The backfilled name works against the backend: diagnose, cancel and logs rely on that.
	if _, err := env.be.Execution(ctx, l.Execution); err != nil && !errors.Is(err, backend.ErrNotFound) {
		t.Fatalf("the backend rejects the recorded name: %v", err)
	}
}

func TestRunPRPointsToM6(t *testing.T) {
	newCloudFixture(t)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--pr", "12"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "M6") {
		t.Fatalf("--pr: %v", err)
	}
}

func TestRetryRefusesFreshClaim(t *testing.T) {
	f := newCloudFixture(t)
	env := memEnv(t, f)
	ctx := context.Background()
	spec := &task.Spec{Version: 1, RunID: "20260927-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x"}
	s := runstore.Open(env.bucket.Bucket, "acme-app", spec.RunID)
	_ = s.CreateTask(ctx, spec)
	now := time.Now()
	_, _, _ = s.Claim(ctx, "other-laptop/1", now.Add(-time.Minute))
	if _, err := launchRun(ctx, env, spec, now); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "in flight") {
		t.Fatalf("fresh claim: %v", err)
	}
	if _, err := launchRun(ctx, env, spec, now.Add(claimTTL)); err != nil || len(f.run.Executions()) != 1 {
		t.Fatalf("stale claim: %v, %d executions", err, len(f.run.Executions()))
	}
}

func TestRunRetryUnlaunched(t *testing.T) {
	f := newCloudFixture(t)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	spec := &task.Spec{Version: 1, RunID: "20260927-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x"}
	_ = runstore.Open(b, "acme-app", spec.RunID).CreateTask(ctx, spec)
	if _, _, err := execute(t, "run", "--retry", "20260927-100000-abcd"); err != nil {
		t.Fatal(err)
	}
	if len(f.run.Executions()) != 1 {
		t.Fatal("--retry did not launch")
	}
	_ = runstore.Open(b, "acme-app", "20260927-100000-ffff").CreateTask(ctx, &task.Spec{Version: 1, RunID: "20260927-100000-ffff", Repo: "acme/app", Ref: "main", Task: "y"})
	_ = runstore.Open(b, "acme-app", "20260927-100000-ffff").RequestCancel(ctx)
	if _, _, err := execute(t, "run", "--retry", "20260927-100000-ffff"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("retry of a cancelled run: %v", err)
	}
}

func TestRunMaxParallel(t *testing.T) {
	newCloudFixture(t) // max_parallel: 2
	for i, id := range []string{"20260927-100000-aaaa", "20260927-100000-bbbb"} {
		if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", id, "task"); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	_, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20260927-100000-cccc", "task")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "max_parallel") {
		t.Fatalf("third run: %v", err)
	}
}

func TestRunNeedsExactlyOneTaskSource(t *testing.T) {
	newCloudFixture(t)
	if _, _, err := execute(t, "run", "--repo", "acme/app"); ExitCode(err) != ExitUserError {
		t.Fatalf("no task: %v", err)
	}
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--task-file", "-", "text too"); ExitCode(err) != ExitUserError {
		t.Fatalf("two task sources: %v", err)
	}
}
```

`TestRunMaxParallel` relies on the fake's new executions being `pending`, and `ActiveOnly` counts pending and running.

`execute` doesn't isolate the working directory. Tests that don't pass `--repo` must `t.Chdir` into a temp directory that isn't a checkout, so `originRepo` fails cleanly.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/cli/ -run 'TestRun|TestRetry'`
Expected: FAIL (`run` is not a command yet, and `launchRun` and `cloudEnv` are undefined)

- [ ] **Step 3: Implement**

`internal/cli/cloud.go`:

```go
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/image"
	"github.com/dimipaun/fugaro/internal/localcfg"
)

type cloudOptions struct{ config, project, region string }

func addCloudFlags(cmd *cobra.Command, o *cloudOptions) {
	f := cmd.Flags()
	f.StringVar(&o.config, "config", "", "local config file (default $FUGARO_CONFIG or ~/.config/fugaro/config.yaml)")
	f.StringVar(&o.project, "project", "", "GCP project (overrides the local config)")
	f.StringVar(&o.region, "region", "", "GCP region (overrides the local config)")
}

type cloudEnv struct {
	lc     *localcfg.Config
	bucket *blobx.Bucket
	be     backend.Backend
	gcp    gcp.Options
}

func (e *cloudEnv) Close() {
	if e.bucket != nil {
		_ = e.bucket.Close()
	}
}

func userErr(format string, args ...any) error {
	return &ExitError{Code: ExitUserError, Err: fmt.Errorf(format, args...)}
}

// remote marks err as a remote failure (exit 2), unless it already carries a code.
func remote(err error) error {
	var ee *ExitError
	if err == nil || errors.As(err, &ee) {
		return err
	}
	return &ExitError{Code: ExitRemoteError, Err: err}
}

func openCloud(ctx context.Context, o cloudOptions) (*cloudEnv, error) {
	path := o.config
	if path == "" {
		var err error
		if path, err = localcfg.Path(os.Getenv); err != nil {
			return nil, userErr("%v", err)
		}
	}
	lc, err := localcfg.Load(path)
	if err != nil {
		return nil, userErr("%v", err)
	}
	lc.Override(o.project, o.region)
	opts := gcp.Options{Project: lc.Project, Region: lc.Region, Endpoints: gcp.Endpoints{
		Run: lc.Endpoints.Run, Logging: lc.Endpoints.Logging, SecretManager: lc.Endpoints.SecretManager,
		CloudBuild: lc.Endpoints.CloudBuild, NoAuth: lc.Endpoints.NoAuth}}
	be, err := gcp.New(ctx, opts)
	if err != nil {
		return nil, remote(err)
	}
	b, err := blobx.Open(ctx, lc.BucketURL())
	if err != nil {
		return nil, remote(err)
	}
	return &cloudEnv{lc: lc, bucket: b, be: be, gcp: opts}, nil
}

// prices are the backend's list prices for the region (overrides: M5).
func (e *cloudEnv) prices() backend.Prices { return gcp.ListPrices(e.lc.Region) }

// originRepo is owner/name of the checkout's origin remote.
func originRepo(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "remote", "get-url", "origin")
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", userErr("no --repo, and no origin remote here to take it from")
	}
	u := image.HTTPSOrigin(strings.TrimSpace(string(out)))
	_, path, ok := strings.Cut(strings.TrimPrefix(u, "https://"), "/")
	path = strings.TrimSuffix(path, ".git")
	if !ok || strings.Count(path, "/") != 1 {
		return "", userErr("origin %s does not name owner/name; pass --repo", gitprovSafe(u))
	}
	return path, nil
}
```

`gitprovSafe` removes userinfo before a URL is quoted. `HTTPSOrigin` returns the input unchanged for schemes it doesn't handle, so it does not always drop userinfo; strip it explicitly:

```go
// gitprovSafe returns u without userinfo, for error messages.
func gitprovSafe(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return "<unparseable URL>"
	}
	p.User = nil
	return p.String()
}
```

Add `TestGitprovSafe`: `https://user:tok@host/o/r` and `ssh://user:tok@host/o/r` both come back without `tok`, and an unparseable URL gives `<unparseable URL>`.

`internal/cli/run.go` implements the command following the flow above. Its core:

```go
const claimTTL = 10 * time.Minute

type launchResult struct {
	Run       string `json:"run"`
	Repo      string `json:"repo"`
	RunID     string `json:"run_id"`
	Branch    string `json:"branch"`
	Execution string `json:"execution"`
	LogURL    string `json:"log_url,omitempty"`
	Status    string `json:"status"` // launched | already-launched
}

// launchHooks lets tests stop launchRun between its steps, to make the
// races of design §4.7 deterministic. Production code never sets them.
var launchHooks struct {
	beforeClaim    func() // after the first launch.json/result.json check
	beforeTakeover func() // after reading a stale claim, before replacing it
	afterClaim     func() // after taking the claim, before the re-check
}

func hook(f func()) {
	if f != nil {
		f()
	}
}

// launchRun starts spec's execution at most once, however many CLIs race
// on the same run ID (design §4.7, Review Focus 1):
//
//   - launch.json (or result.json's execution) present → already launched.
//   - Otherwise take the "launching" claim with create-if-absent. The claim
//     is never deleted after a launch, so a later caller always finds it,
//     then re-reads launch.json, which the claim holder writes before it
//     returns.
//   - A stale claim (older than claimTTL, its holder presumably dead) is
//     taken over with a generation-matched overwrite, never delete-then-
//     create, so two takers can't both win (I-2).
//   - Having won the claim, re-check launch.json and result.json: an earlier
//     holder may have launched just before going stale.
//
// file:// buckets are single-user: fileblob's IfNotExist is not atomic.
func launchRun(ctx context.Context, env *cloudEnv, spec *task.Spec, now time.Time) (launchResult, error) {
	slug := task.Slug(spec.Repo)
	s := runstore.Open(env.bucket.Bucket, slug, spec.RunID)
	res := launchResult{Run: slug + "/" + spec.RunID, Repo: spec.Repo, RunID: spec.RunID, Branch: "fugaro/" + spec.RunID}
	done := func(l *runstore.Launch, status string) (launchResult, error) {
		res.Execution, res.LogURL, res.Status = l.Execution, l.LogURL, status
		return res, nil
	}
	// launched reports an existing launch, backfilling launch.json from
	// result.json when the CLI that launched died before writing it. The
	// runner records the same canonical name launch.json holds (C-1).
	launched := func() (*runstore.Launch, error) {
		if l, err := s.ReadLaunch(ctx); err == nil {
			return l, nil
		} else if !errors.Is(err, runstore.ErrNotFound) {
			return nil, remote(err)
		}
		rec, err := s.ReadRecord(ctx)
		if errors.Is(err, runstore.ErrNotFound) || (err == nil && rec.Execution == "") {
			return nil, nil
		}
		if err != nil {
			return nil, remote(err)
		}
		l := &runstore.Launch{Version: 1, RunID: spec.RunID, Backend: "cloud-run", Execution: rec.Execution, LaunchedAt: rec.StartedAt}
		if err := s.WriteLaunch(ctx, l); errors.Is(err, runstore.ErrExists) {
			return s.ReadLaunch(ctx)
		} else if err != nil {
			return nil, remote(err)
		}
		return l, nil
	}
	if l, err := launched(); err != nil || l != nil {
		if err != nil {
			return res, err
		}
		return done(l, "already-launched")
	}
	hook(launchHooks.beforeClaim)
	holder := claimHolder()
	ok, existing, err := s.Claim(ctx, holder, now)
	if err != nil {
		return res, remote(err)
	}
	if !ok {
		// Someone holds the claim. If they finished, launch.json is there.
		if l, err := launched(); err != nil || l != nil {
			if err != nil {
				return res, err
			}
			return done(l, "already-launched")
		}
		if now.Sub(existing.At) < claimTTL {
			return res, userErr("a launch of %s started at %s (%s) may still be in flight; retry after %s, or check fugaro ls",
				res.Run, existing.At.Format(time.RFC3339), existing.Holder, existing.At.Add(claimTTL).Format(time.RFC3339))
		}
		prev, gen, err := env.bucket.Read(ctx, s.ClaimKey())
		if err != nil {
			return res, remote(err)
		}
		hook(launchHooks.beforeTakeover)
		mine, _ := json.Marshal(runstore.Claim{Holder: holder, At: now.UTC()})
		if _, err := env.bucket.ReplaceIf(ctx, s.ClaimKey(), mine, gen, prev); errors.Is(err, blobx.ErrConflict) {
			if l, err := launched(); err != nil || l != nil {
				if err != nil {
					return res, err
				}
				return done(l, "already-launched")
			}
			return res, userErr("another CLI took over the stale launch claim of %s just now; check fugaro ls", res.Run)
		} else if err != nil {
			return res, remote(err)
		}
	}
	hook(launchHooks.afterClaim)
	if l, err := launched(); err != nil || l != nil {
		if err != nil {
			return res, err
		}
		return done(l, "already-launched")
	}
	ref, err := env.be.Launch(ctx, backend.LaunchSpec{Repo: backend.RepoRef{Repo: spec.Repo, Slug: slug}, Workflow: spec.Workflow, RunID: spec.RunID})
	if err != nil {
		releaseClaim(context.WithoutCancel(ctx), env, s, holder)
		if errors.Is(err, backend.ErrNotFound) {
			return res, userErr("%v", err)
		}
		return res, remote(err)
	}
	l := &runstore.Launch{Version: 1, RunID: spec.RunID, Backend: "cloud-run", Execution: ref.Name, Job: ref.Job, LogURL: ref.LogURL, LaunchedBy: spec.RequestedBy, LaunchedAt: now.UTC()}
	if err := s.WriteLaunch(ctx, l); errors.Is(err, runstore.ErrExists) {
		theirs, rerr := s.ReadLaunch(ctx)
		if rerr != nil {
			return res, remote(rerr)
		}
		return done(theirs, "already-launched")
	} else if err != nil {
		// The claim stays: nobody may relaunch until it goes stale, by which
		// time the runner has written result.json's execution for --retry.
		return res, remote(fmt.Errorf("launched %s but could not record launch.json (fugaro run --retry %s records it from result.json): %w", ref.Name, res.Run, err))
	}
	return done(l, "launched") // the claim stays in place for good
}

// releaseClaim drops our own claim after a failed launch, so a retry need
// not wait out claimTTL. It deletes only the exact object we hold.
func releaseClaim(ctx context.Context, env *cloudEnv, s *runstore.Store, holder string) {
	data, gen, err := env.bucket.Read(ctx, s.ClaimKey())
	var c runstore.Claim
	if err != nil || json.Unmarshal(data, &c) != nil || c.Holder != holder {
		return
	}
	_ = env.bucket.DeleteIf(ctx, s.ClaimKey(), gen, data)
}

func claimHolder() string {
	host, _ := os.Hostname()
	return fmt.Sprintf("%s/%d/%d", host, os.Getpid(), rand.Uint64()) // unique per call, even within one process
}
```

`rand` is `math/rand/v2`. A `spec.Workflow` that is empty at this point is a bug in the caller: `runCmd` always resolves the workflow first, because the job name needs it. `--retry` of a spec without a workflow resolves it the same way as a new run.

Register `newRunCmd()` in `root.go`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/cli/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/cloud*.go internal/cli/run*.go internal/cli/root.go
git commit -m "cli: add fugaro run with idempotent --run-id, --retry, --batch and launch.json"
```

---

### Task 11: The run view (`internal/runview`) and `fugaro ls`

**Files:**
- Create: `internal/runview/runview.go`, `internal/runview/runview_test.go`
- Create: `internal/cli/ls.go`, `internal/cli/ls_test.go`
- Modify: `internal/cli/root.go`

**Interfaces:**
- Consumes: `runstore.ListSlugs`, `ListRunIDs`, `ReadTask`, `ReadLaunch`, `ReadRecord` and `CancelRequested` (Tasks 1 and 3); `runstore.NewCost` and `ModelBasis` (Task 7); `runner.CostLine`; `backend.Execution`, `Prices` and `ListFilter`; `cloudEnv` and `openCloud` (Task 10)
- Produces:
  - `runview.Input{Slug, RunID string; Task *task.Spec; Launch *runstore.Launch; Record *runstore.Record; Exec *backend.Execution; CancelMarker bool}`
  - `runview.Row{Run, Repo, Workflow, RunID, Status, Stage, Reason, Batch, RequestedBy, PRURL, Execution, LogURL string; Created time.Time; Cost runstore.Cost; Terminal bool}`, with JSON snake_case tags
  - `runview.Join(in Input, prices backend.Prices, now time.Time) Row`
  - `runview.Totals{Runs int; ModelUSD, ModelNotionalUSD, ComputeUSD, TotalUSD float64}` and `runview.Sum(rows []Row) Totals`
  - `runview.StatusUnlaunched = "unlaunched"`, `runview.StatusPending = "pending"`
  - `runview.ReasonNoFinalRecord`, which is `"execution ended without finalizing"`
  - `cli.loadRows(ctx, env *cloudEnv, f lsFilter, now time.Time) ([]runview.Row, error)`, which Task 12 reuses for one run
  - `cli.parseSince(s string) (time.Duration, error)`, which accepts `7d`, `36h`, `90m` and `0`

**Status rules** (`Join`, conservative, Review Focus 5):

| Evidence | Status | Reason |
|---|---|---|
| `result.json` with a final status (anything but `running`) | that status | the record's |
| no `launch.json` and no `result.json`, cancel marker present | `cancelled` | "cancelled before launch" |
| no `launch.json` and no `result.json` | `unlaunched` | — |
| execution terminal as `cancelled`, record missing or `running` | `cancelled` | "execution cancelled before the run finalized" |
| execution terminal otherwise, record missing or `running` | `infra_error` | "execution ended without finalizing (<state>)" |
| execution `pending` | `pending` | — |
| no execution info, record `running`, and `now` past the record's `deadline` | `infra_error` | "no execution found and past the run's deadline" (minor 14) |
| execution `running`, or a record that is `running` with no execution info | `running` | — |
| `launch.json` only, and the execution is unknown to the backend | `pending` | — |

**Cost per row:**
- `model` is `record.CostUSD`, and the basis is `record.Cost.ModelBasis`. When there is no record, or no basis, the basis is `api-list`.
- `compute` is `prices.ComputeUSD(exec.CPU, exec.MemoryGiB, exec.Billed(now))` when the execution is known, else `record.Cost.ComputeUSD`.
- Then `runstore.NewCost`.
- `Sum` adds `TotalUSD`, `ComputeUSD`, api-list `ModelUSD` into `ModelUSD`, and subscription `ModelUSD` into `ModelNotionalUSD`.

**`fugaro ls [--repo R] [--all] [--mine] [--since D] [--batch NAME] [--watch] [--json]`:**
- **Repositories:**
  - `--repo` gives that repository's slug.
  - `--all` gives every slug in the bucket.
  - The default is the local config's repos, or every slug when it lists none.
- **Since:** the default is `7d`. `--since 0` means no bound. The filter uses the run ID's timestamp, so old runs are skipped without reading their objects.
- **Loading:**
  - Executions come from one `be.List(Since: since-1h)`, mapped by `backend.ParseExecution(name).Key()`; a run's execution (from `launch.json`, else `result.json`) is looked up by the same key, never by raw name (C-1, I-9). A run whose execution is older than the list window is fetched with `be.Execution` on its own.
  - Each run's objects are read by 8 workers.
  - A run whose `task.json` is unreadable still shows, with an empty repo and the reason "task.json unreadable".
- **Filters:** `--mine` keeps runs whose `requested_by` is `lc.Me()`, and `--batch` keeps runs whose `task.batch` matches exactly.
- **Order:** newest first.
- **Human table:**

  ```
  RUN                              STATUS       STAGE      AGE   COST     PR
  acme-app/20260927-100000-abcd    succeeded    writeback  2h    $0.52    https://…/pull/12
  ```

  It ends with a totals line: `3 runs · ≈ $1.40 billed (model $0.90 + compute $0.50) · $2.10 model notional (subscription)`. The notional part is omitted when it is zero.
- **JSON:** `{"runs": [Row…], "totals": Totals}`.
- **`--watch`:** it redraws every 10s (the hidden `--interval` flag changes that) until every row is terminal or the context ends. On a TTY it clears the screen with `\x1b[H\x1b[2J`. With `--json` it prints one document per tick.

- [ ] **Step 1: Write the failing tests**

`internal/runview/runview_test.go`:

```go
package runview

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

var (
	now    = time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	prices = backend.Prices{VCPUSecondUSD: 0.00001, GiBSecondUSD: 0}
	spec   = &task.Spec{Version: 1, RunID: "20260927-100000-abcd", Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x", Batch: "b1", RequestedBy: "me@example.com"}
	launch = &runstore.Launch{Execution: "exec-1", LogURL: "https://log"}
)

func rec(status runstore.Status, cost *runstore.Cost) *runstore.Record {
	return &runstore.Record{Status: status, Stage: "implement", CostUSD: 2, Cost: cost}
}

func exec(state backend.State) *backend.Execution {
	e := &backend.Execution{Name: "exec-1", State: state, Started: now.Add(-time.Hour), CPU: 4, MemoryGiB: 8}
	if state.Terminal() {
		e.Completed = now.Add(-30 * time.Minute)
	}
	return e
}

func TestJoinStatuses(t *testing.T) {
	sub := runstore.NewCost(2, 0.1, runstore.BasisSubscription)
	cases := []struct {
		name   string
		in     Input
		status string
	}{
		{"unlaunched", Input{Task: spec}, StatusUnlaunched},
		{"cancelled before launch", Input{Task: spec, CancelMarker: true}, "cancelled"},
		{"launched, not started", Input{Task: spec, Launch: launch, Exec: exec(backend.StatePending)}, StatusPending},
		{"launched, unknown to backend", Input{Task: spec, Launch: launch}, StatusPending},
		{"running", Input{Task: spec, Launch: launch, Record: rec(runstore.StatusRunning, &sub), Exec: exec(backend.StateRunning)}, "running"},
		{"finished", Input{Task: spec, Launch: launch, Record: rec(runstore.StatusSucceeded, &sub), Exec: exec(backend.StateSucceeded)}, "succeeded"},
		{"draft", Input{Task: spec, Launch: launch, Record: rec(runstore.StatusFailed, &sub), Exec: exec(backend.StateSucceeded)}, "failed"},
	}
	for _, tc := range cases {
		if got := Join(tc.in, prices, now); got.Status != tc.status {
			t.Errorf("%s: status %s, want %s", tc.name, got.Status, tc.status)
		}
	}
}

func TestJoinRunningPastDeadlineWithoutExecution(t *testing.T) {
	past := now.Add(-time.Minute)
	r := rec(runstore.StatusRunning, nil)
	r.Deadline = &past
	row := Join(Input{Task: spec, Launch: launch, Record: r}, prices, now)
	if row.Status != "infra_error" || !row.Terminal {
		t.Fatalf("row = %+v", row)
	}
	future := now.Add(time.Hour)
	r.Deadline = &future
	if row := Join(Input{Task: spec, Launch: launch, Record: r}, prices, now); row.Status != "running" {
		t.Fatalf("before the deadline: %+v", row)
	}
}

func TestJoinExecutionEndedWithoutFinalizing(t *testing.T) {
	for _, state := range []backend.State{backend.StateFailed, backend.StateSucceeded} {
		row := Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusRunning, nil), Exec: exec(state)}, prices, now)
		if row.Status != "infra_error" || !strings.Contains(row.Reason, ReasonNoFinalRecord) || !row.Terminal {
			t.Errorf("%s: row = %+v", state, row)
		}
	}
	row := Join(Input{Task: spec, Launch: launch, Exec: exec(backend.StateCancelled)}, prices, now)
	if row.Status != "cancelled" {
		t.Errorf("cancelled execution without a record: %+v", row)
	}
}

func TestJoinCostUsesExecutionAndBasis(t *testing.T) {
	sub := runstore.NewCost(2, 0, runstore.BasisSubscription)
	row := Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusSucceeded, &sub), Exec: exec(backend.StateSucceeded)}, prices, now)
	// 30 minutes × 4 vCPU × $0.00001 = $0.072 compute; model $2 notional.
	if row.Cost.ModelBasis != runstore.BasisSubscription || row.Cost.ComputeUSD < 0.0719 || row.Cost.ComputeUSD > 0.0721 || row.Cost.TotalUSD != 0.07 {
		t.Fatalf("cost = %+v", row.Cost)
	}
	api := Join(Input{Task: spec, Launch: launch, Record: rec(runstore.StatusSucceeded, nil), Exec: exec(backend.StateSucceeded)}, prices, now)
	tot := Sum([]Row{row, api})
	if tot.Runs != 2 || tot.ModelNotionalUSD != 2 || tot.ModelUSD != 2 || math.Abs(tot.TotalUSD-(row.Cost.TotalUSD+api.Cost.TotalUSD)) > 0.005 {
		t.Fatalf("totals = %+v", tot)
	}
}
```

`internal/cli/ls_test.go` seeds a `file://` bucket and the Run fake directly:

```go
package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
	"github.com/dimipaun/fugaro/internal/task"
)

type lsOut struct {
	Runs   []runview.Row  `json:"runs"`
	Totals runview.Totals `json:"totals"`
}

func seedRun(t *testing.T, f *cloudFixture, id, batch, who string, launched bool) string {
	t.Helper()
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, "acme-app", id)
	_ = s.CreateTask(ctx, &task.Spec{Version: 1, RunID: id, Repo: "acme/app", Ref: "main", Workflow: "web", Task: "x", Batch: batch, RequestedBy: who})
	if !launched {
		return ""
	}
	exec := f.run.Start("fugaro-acme-app-web")
	_ = s.WriteLaunch(ctx, &runstore.Launch{Version: 1, RunID: id, Execution: exec, LaunchedAt: time.Now()})
	return exec
}

func TestLsFiltersAndTotals(t *testing.T) {
	f := newCloudFixture(t)
	today := time.Now().UTC().Format("20060102")
	e1 := seedRun(t, f, today+"-090000-aaaa", "b1", "someone@example.com", true)
	seedRun(t, f, today+"-091000-bbbb", "b1", "other@example.com", true)
	seedRun(t, f, today+"-092000-cccc", "b2", "someone@example.com", false)
	seedRun(t, f, "20200101-000000-dddd", "b1", "someone@example.com", false) // outside --since
	f.run.SetState(e1, backend.StateRunning)

	out, _, err := execute(t, "ls", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var got lsOut
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Runs) != 3 || got.Runs[0].Status != runview.StatusUnlaunched || got.Totals.Runs != 3 {
		t.Fatalf("ls = %+v", got)
	}
	out, _, _ = execute(t, "ls", "--json", "--mine", "--batch", "b1")
	_ = json.Unmarshal([]byte(out), &got)
	if len(got.Runs) != 1 || got.Runs[0].Status != "running" || got.Runs[0].Cost.ComputeUSD <= 0 {
		t.Fatalf("ls --mine --batch b1 = %+v", got.Runs)
	}
	out, _, _ = execute(t, "ls", "--json", "--since", "0")
	_ = json.Unmarshal([]byte(out), &got)
	if len(got.Runs) != 4 {
		t.Fatalf("ls --since 0 = %d runs", len(got.Runs))
	}
	human, _, err := execute(t, "ls")
	if err != nil || !strings.Contains(human, "unlaunched") || !strings.Contains(human, "3 runs") {
		t.Fatalf("human ls = %s, %v", human, err)
	}
}

func TestParseSince(t *testing.T) {
	for in, want := range map[string]time.Duration{"7d": 7 * 24 * time.Hour, "36h": 36 * time.Hour, "90m": 90 * time.Minute, "0": 0} {
		if got, err := parseSince(in); err != nil || got != want {
			t.Errorf("parseSince(%q) = %v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "d", "-1d", "7w"} {
		if _, err := parseSince(bad); err == nil {
			t.Errorf("parseSince(%q) accepted", bad)
		}
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/runview/ ./internal/cli/ -run 'Join|Sum|TestLs|ParseSince'`
Expected: FAIL (`runview` doesn't exist, and `ls` is not a command)

- [ ] **Step 3: Implement**

`internal/runview/runview.go`:

```go
// Package runview joins what the runs bucket and the backend know about a
// run into one row (design §4.7, §9.1 ls): status, stage, cost and links.
// It never reports a run as more finished or more successful than
// result.json says: an execution that ended without a final record is an
// infra_error, never a success.
package runview

import (
	"time"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// Statuses beyond runstore's.
const (
	StatusUnlaunched = "unlaunched"
	StatusPending    = "pending"
)

// ReasonNoFinalRecord explains a run whose execution is gone but whose
// record never reached a final status (OOM kill, task timeout, node loss).
const ReasonNoFinalRecord = "execution ended without finalizing"

// Input is everything known about one run.
type Input struct {
	Slug, RunID  string
	Task         *task.Spec
	Launch       *runstore.Launch
	Record       *runstore.Record
	Exec         *backend.Execution
	CancelMarker bool
}

// Row is one run as ls and diagnose show it.
type Row struct {
	Run         string        `json:"run"`
	Repo        string        `json:"repo"`
	Workflow    string        `json:"workflow,omitempty"`
	RunID       string        `json:"run_id"`
	Status      string        `json:"status"`
	Stage       string        `json:"stage,omitempty"`
	Reason      string        `json:"reason,omitempty"`
	Batch       string        `json:"batch,omitempty"`
	RequestedBy string        `json:"requested_by,omitempty"`
	PRURL       string        `json:"pr_url,omitempty"`
	Execution   string        `json:"execution,omitempty"`
	LogURL      string        `json:"log_url,omitempty"`
	Created     time.Time     `json:"created"`
	Cost        runstore.Cost `json:"cost"`
	Terminal    bool          `json:"terminal"`
}

// Join builds the row for in.
func Join(in Input, prices backend.Prices, now time.Time) Row {
	row := Row{Run: in.Slug + "/" + in.RunID, RunID: in.RunID}
	row.Created, _ = runstore.RunTime(in.RunID)
	if t := in.Task; t != nil {
		row.Repo, row.Workflow, row.Batch, row.RequestedBy = t.Repo, t.Workflow, t.Batch, t.RequestedBy
	} else {
		row.Reason = "task.json unreadable"
	}
	if in.Launch != nil {
		row.Execution, row.LogURL = in.Launch.Execution, in.Launch.LogURL
	}
	r, e := in.Record, in.Exec
	if r != nil {
		row.Stage, row.Reason = r.Stage, r.Reason
		if r.PR != nil {
			row.PRURL = r.PR.URL
		}
		if row.Execution == "" {
			row.Execution = r.Execution
		}
	}
	if e != nil && row.LogURL == "" {
		row.LogURL = e.LogURL
	}
	final := r != nil && r.Status != runstore.StatusRunning
	switch {
	case final:
		row.Status = string(r.Status)
	case in.Launch == nil && r == nil && in.CancelMarker:
		row.Status, row.Reason = string(runstore.StatusCancelled), "cancelled before launch"
	case in.Launch == nil && r == nil:
		row.Status = StatusUnlaunched
	case e != nil && e.State == backend.StateCancelled:
		row.Status, row.Reason = string(runstore.StatusCancelled), "execution cancelled before the run finalized"
	case e != nil && e.State.Terminal():
		row.Status, row.Reason = string(runstore.StatusInfraError), ReasonNoFinalRecord+" ("+string(e.State)+")"
	case e != nil && e.State == backend.StatePending:
		row.Status = StatusPending
	case e == nil && r != nil && r.Deadline != nil && now.After(*r.Deadline):
		row.Status, row.Reason = string(runstore.StatusInfraError), "no execution found and past the run's deadline"
	case e != nil || r != nil:
		row.Status = string(runstore.StatusRunning)
	default:
		row.Status = StatusPending
	}
	row.Terminal = row.Status != StatusPending && row.Status != string(runstore.StatusRunning) && row.Status != StatusUnlaunched
	basis, model, compute := runstore.BasisAPIList, 0.0, 0.0
	if r != nil {
		model = r.CostUSD
		if r.Cost != nil {
			if r.Cost.ModelBasis != "" {
				basis = r.Cost.ModelBasis
			}
			compute = r.Cost.ComputeUSD
		}
	}
	if e != nil {
		compute = prices.ComputeUSD(e.CPU, e.MemoryGiB, e.Billed(now))
	}
	row.Cost = runstore.NewCost(model, compute, basis)
	return row
}

// Totals sums rows' costs (design §10.1).
type Totals struct {
	Runs             int     `json:"runs"`
	ModelUSD         float64 `json:"model_usd"`          // billed model spend (api-list)
	ModelNotionalUSD float64 `json:"model_notional_usd"` // subscription model spend, not billed
	ComputeUSD       float64 `json:"compute_usd"`
	TotalUSD         float64 `json:"total_usd"`
}

// Sum totals rows.
func Sum(rows []Row) Totals {
	t := Totals{Runs: len(rows)}
	for _, r := range rows {
		if r.Cost.ModelBasis == runstore.BasisSubscription {
			t.ModelNotionalUSD += r.Cost.ModelUSD
		} else {
			t.ModelUSD += r.Cost.ModelUSD
		}
		t.ComputeUSD += r.Cost.ComputeUSD
		t.TotalUSD += r.Cost.TotalUSD
	}
	return t
}
```

`internal/cli/ls.go` holds these pieces:
- `lsFilter{slugs []string; since time.Time; mine string; batch string; runRef string}`. `runRef` limits the rows to one run, for diagnose.
- `loadRows`, following the rules above. Workers read the task, launch and record with `errors.Is(err, runstore.ErrNotFound)` treated as absent. Any other read error is a remote error and fails the command, which is the conservative choice: `ls` never shows a status guessed from a partial read.
- `parseSince`:

```go
func parseSince(s string) (time.Duration, error) {
	if s == "0" {
		return 0, nil
	}
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || days < 1 {
			return 0, fmt.Errorf("--since %q: use a positive number of days (7d) or a Go duration (36h)", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("--since %q: use a positive number of days (7d) or a Go duration (36h)", s)
	}
	return d, nil
}
```

- the table printer, using `text/tabwriter`, with the age rounded to minutes, hours or days, and the cost as `$%.2f` of `TotalUSD`, with a trailing `~` when the model spend was notional
- the watch loop

Register `newLsCmd()` in `root.go`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/runview/ ./internal/cli/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/runview internal/cli/ls*.go internal/cli/root.go
git commit -m "cli: add fugaro ls with --mine, --since, --batch, --watch and cost totals"
```

---

### Task 12: `fugaro logs` and `fugaro diagnose`

**Files:**
- Create: `internal/cli/logs.go`, `internal/cli/logs_test.go`
- Create: `internal/cli/diagnose.go`, `internal/cli/diagnose_test.go`
- Modify: `internal/cli/root.go`

**Interfaces:**
- Consumes: `backend.Backend.Logs` (Task 9); `runstore.Locate`, `ReadLaunch`, `ReadRecord` and `ReadFile` (Task 3); `loadRows` (Task 11); `agent.ParseStream`; `runner.ParseVerdict`, `runner.Finding` and `runner.CostLine`
- Produces:
  - `fugaro logs RUN [-f|--follow] [--json]`
  - `fugaro diagnose RUN [--json]`
  - `cli.Diagnosis{Row runview.Row; Verify []verify.Record; Failed, Flaky []string; Findings []runner.Finding; AgentMessage string; LogTail []string; ReportPath string}`, with snake_case JSON tags

**`logs`:**
- It resolves the run with `runstore.Locate`. A run without `launch.json` is exit 1, "run <ref> was never launched (see fugaro run --retry)". `result.json`'s `execution` is a fallback, after the backfill rule of Task 10.
- It queries `Since: launch.LaunchedAt - 1m`, and `-f` follows.
- **Human line:** `15:04:05 INFO    [implement] message`. When `Fields["stream"]` is `agent` or `verify`, the stage tag gets that suffix: `[implement/agent]`.
- **JSON:** one object per line, `{"time", "severity", "stage", "stream", "event", "message"}`.

**`diagnose`** gathers, in this order:
- the row, from `loadRows` limited to this run
- the reason
- from `result.json`'s `verify`, the last test record, plus its `failed` and `flaky`
- the last review's findings, from `transcripts/review-<len(reviews)>.jsonl`: `agent.ParseStream` gives the result event, and `runner.ParseVerdict` its verdict. A missing transcript means no findings.
- the agent's final message: the result text of the last implement or fix transcript (`implement-1`, `fix-<n>`, with `n` counted from `rec.Stages`), clipped to 4KB and redaction-safe, since transcripts are stored redacted
- the last 30 log lines, from one `Logs` call collected into a ring buffer
- the PR URL, from the row
- the report path, `runs/<slug>/<id>/report.md`

Human output is a short labelled section per item, with the cost from `runner.CostLine(row.Cost)`. `--json` prints `Diagnosis`.

- [ ] **Step 1: Write the failing tests**

`internal/cli/logs_test.go`:

```go
package cli

import (
	"strings"
	"testing"
)

func TestLogs(t *testing.T) {
	f := newCloudFixture(t)
	exec := seedRun(t, f, "20260927-100000-abcd", "", "someone@example.com", true)
	f.logging.AddJSONLines(exec, []byte(`{"time":"2026-09-27T10:00:01Z","severity":"INFO","message":"stage started","stage":"implement"}
{"time":"2026-09-27T10:00:02Z","severity":"INFO","message":"tool Bash: fugaro verify test","stage":"implement","stream":"agent","event":"tool"}
`))
	out, _, err := execute(t, "logs", "20260927-100000-abcd")
	if err != nil || !strings.Contains(out, "[implement] stage started") || !strings.Contains(out, "[implement/agent] tool Bash") {
		t.Fatalf("logs = %s, %v", out, err)
	}
	js, _, err := execute(t, "logs", "--json", "acme-app/20260927-100000-abcd")
	if err != nil || strings.Count(js, "\n") != 2 || !strings.Contains(js, `"event":"tool"`) {
		t.Fatalf("logs --json = %s, %v", js, err)
	}
	seedRun(t, f, "20260927-110000-bbbb", "", "someone@example.com", false)
	if _, _, err := execute(t, "logs", "20260927-110000-bbbb"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "never launched") {
		t.Fatalf("unlaunched logs: %v", err)
	}
}
```

`internal/cli/diagnose_test.go`:

```go
package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/verify"
)

func TestDiagnose(t *testing.T) {
	f := newCloudFixture(t)
	const id = "20260927-100000-abcd"
	exec := seedRun(t, f, id, "", "someone@example.com", true)
	ctx := context.Background()
	b, _ := blob.OpenBucket(ctx, f.bucket)
	defer b.Close()
	s := runstore.Open(b, "acme-app", id)
	cost := runstore.NewCost(3.2, 0, runstore.BasisSubscription)
	rec := &runstore.Record{Version: 1, RunID: id, Repo: "acme/app", Workflow: "web", Execution: exec,
		Status: runstore.StatusFailed, Stage: "writeback", Outcome: runstore.OutcomeDraft,
		Reason: "tests failing on the final commit", HeadSHA: "abc1234", CostUSD: 3.2, Cost: &cost,
		PR:      &runstore.PRRef{Number: 7, URL: "https://bitbucket.org/acme/app/pull-requests/7"},
		Reviews: []runstore.ReviewSummary{{Round: 1, Verdict: "changes", Findings: 3}, {Round: 2, Verdict: "changes", Findings: 2}},
		Verify: []verify.Record{{N: 1, Kind: verify.KindTest, HeadSHA: "abc1234", CleanTree: true, Passed: false,
			Tests: 3, Failures: 1, Failed: []string{"pkg.Suite.beta"}, Flaky: []string{"pkg.Suite.alpha"}}},
		Stages: []runstore.StageTiming{{Name: "implement"}, {Name: "review"}, {Name: "fix"}, {Name: "review"}}}
	if err := s.WriteRecord(ctx, rec); err != nil {
		t.Fatal(err)
	}
	review := `{"type":"result","subtype":"success","result":"see findings","structured_output":{"verdict":"changes","findings":[{"severity":"high","file":"a.go","summary":"nil deref"},{"severity":"low","file":"b.go","summary":"typo"}]}}` + "\n"
	fix := `{"type":"result","subtype":"success","result":"I fixed the parser but tests still fail"}` + "\n"
	_ = s.PutFile(ctx, "transcripts/review-2.jsonl", []byte(review), "application/x-ndjson")
	_ = s.PutFile(ctx, "transcripts/fix-1.jsonl", []byte(fix), "application/x-ndjson")
	f.logging.AddJSONLines(exec, []byte(`{"time":"2026-09-27T10:05:00Z","severity":"INFO","message":"stage started","stage":"fix"}`+"\n"))

	out, _, err := execute(t, "diagnose", "--json", id)
	if err != nil {
		t.Fatal(err)
	}
	var d Diagnosis
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if d.Row.Status != "failed" || d.Row.PRURL != rec.PR.URL {
		t.Fatalf("row = %+v", d.Row)
	}
	if strings.Join(d.Failed, ",") != "pkg.Suite.beta" || strings.Join(d.Flaky, ",") != "pkg.Suite.alpha" {
		t.Fatalf("failed %v, flaky %v", d.Failed, d.Flaky)
	}
	if len(d.Findings) != 2 || d.Findings[0].Summary != "nil deref" {
		t.Fatalf("findings = %+v", d.Findings)
	}
	if !strings.Contains(d.AgentMessage, "fixed the parser") || len(d.LogTail) != 1 || !strings.Contains(d.LogTail[0], "stage started") {
		t.Fatalf("message %q, tail %v", d.AgentMessage, d.LogTail)
	}
	human, _, err := execute(t, "diagnose", id)
	for _, want := range []string{"Reason:", "Findings", "Cost:", rec.PR.URL, "notional"} {
		if err != nil || !strings.Contains(human, want) {
			t.Fatalf("human diagnose lacks %q (%v):\n%s", want, err, human)
		}
	}
}

func TestDiagnoseWithoutTranscripts(t *testing.T) {
	f := newCloudFixture(t)
	seedRun(t, f, "20260927-110000-bbbb", "", "", true)
	out, _, err := execute(t, "diagnose", "--json", "20260927-110000-bbbb")
	var d Diagnosis
	if err != nil || json.Unmarshal([]byte(out), &d) != nil || d.Row.Status != "pending" || len(d.Findings) != 0 || d.AgentMessage != "" {
		t.Fatalf("diagnose of a run with nothing yet: %s, %v", out, err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/cli/ -run 'TestLogs|TestDiagnose'`
Expected: FAIL (`logs` and `diagnose` are not commands yet)

- [ ] **Step 3: Implement** both commands as specified. Shared pieces:

```go
// locateLaunched resolves ref and returns its store and launch record.
func locateLaunched(ctx context.Context, env *cloudEnv, ref string) (*runstore.Store, *runstore.Launch, string, error) {
	slug, id, err := runstore.Locate(ctx, env.bucket.Bucket, ref)
	if err != nil {
		if errors.Is(err, runstore.ErrNotFound) {
			return nil, nil, "", userErr("%v", err)
		}
		return nil, nil, "", err
	}
	s := runstore.Open(env.bucket.Bucket, slug, id)
	l, err := s.ReadLaunch(ctx)
	if errors.Is(err, runstore.ErrNotFound) {
		if rec, rerr := s.ReadRecord(ctx); rerr == nil && rec.Execution != "" {
			return s, &runstore.Launch{RunID: id, Execution: rec.Execution, LaunchedAt: rec.StartedAt}, slug + "/" + id, nil
		}
		return nil, nil, "", userErr("run %s/%s was never launched (see fugaro run --retry)", slug, id)
	}
	if err != nil {
		return nil, nil, "", remote(err)
	}
	return s, l, slug + "/" + id, nil
}

// transcriptResult is the result event of transcripts/<name>.jsonl, if any.
func transcriptResult(ctx context.Context, s *runstore.Store, name string) (agent.Result, bool) {
	data, err := s.ReadFile(ctx, "transcripts/"+name+".jsonl")
	if err != nil {
		return agent.Result{}, false
	}
	res, found, err := agent.ParseStream(bytes.NewReader(data), nil)
	return res, found && err == nil
}
```

Register both commands in `root.go`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/cli/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/logs*.go internal/cli/diagnose*.go internal/cli/root.go
git commit -m "cli: add fugaro logs and fugaro diagnose"
```

---

### Task 13: `fugaro cancel`

**Files:**
- Create: `internal/cli/cancel.go`, `internal/cli/cancel_test.go`
- Modify: `internal/cli/root.go`

**Interfaces:**
- Consumes: `runstore.Locate`, `ReadLaunch`, `ReadRecord`, `RequestCancel` and `CancelRequested`; `backend.Execution` and `Cancel`; `locateLaunched` (Task 12), for launched runs only
- Produces:
  - `fugaro cancel RUN [--grace D] [--now] [--json]`
  - `cli.cancelResult{Run, Status string; Marker, Hard bool}`, where `Status` is `already-finished`, `not-launched`, `finalized` or `cancelled`

**The flow** (design §4.5):
1. Locate the run with `runstore.Locate` directly: `locateLaunched` errors on exactly the never-launched case, which cancel must handle (minor 15). Then read `launch.json`, falling back to `result.json`'s execution as `locateLaunched` does.
   - **Never launched:** write the marker, so `--retry` refuses and `ls` shows `cancelled`, and report `not-launched`.
   - **Execution already terminal:** report `already-finished`. No marker.
2. Write the cancel marker.
   - If that fails and `--now` wasn't given, return a remote error without touching the execution. Hard-cancelling skips finalize, and with it the draft PR, which is the non-conservative direction.
3. **`--now`:** call `be.Cancel` immediately and report `cancelled` (hard).
4. **Otherwise** poll every 10s for up to `--grace` (default 3m), reading both the execution and `result.json` (I-4):
   - The run counts as **finalized** as soon as `result.json` has a final status, or its stage is `writeback`: the PR exists by then. Report `finalized` without waiting for the execution to end.
   - While the recorded stage is `finalize` when the grace runs out, keep waiting, up to 10 more minutes (`finalizeWait`), because the runner is opening the PR. It **never** hard-cancels a run whose stage is `finalize` or `writeback`.
   - A cancelled run skips its cache write-back (Task 6), so `writeback` after a cancel lasts seconds.
   - When the grace (and any finalize extension) is over, the execution is still running, and the stage is an agent stage or `bootstrap`, call `be.Cancel` and report `cancelled`, with `hard: true`.
5. **Human output:**
   - `cancel requested for <run>; waiting up to 3m for the runner to finalize a draft PR…`
   - then one of `finalized (draft PR <url>)` or `grace period over; cancelled the execution. The run may not have a draft PR; check fugaro diagnose <run>`

- [ ] **Step 1: Write the failing tests** — `internal/cli/cancel_test.go`

```go
package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/runstore"
)

func cancelled(t *testing.T, f *cloudFixture, id string) bool {
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	defer b.Close()
	ok, _ := runstore.Open(b, "acme-app", id).CancelRequested(context.Background())
	return ok
}

func TestCancelRunnerFinalizesInGrace(t *testing.T) {
	f := newCloudFixture(t)
	exec := seedRun(t, f, "20260927-100000-abcd", "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	go func() { time.Sleep(50 * time.Millisecond); f.run.SetState(exec, backend.StateSucceeded) }()
	out, _, err := execute(t, "cancel", "--json", "--grace", "5s", "--poll", "10ms", "20260927-100000-abcd")
	var res cancelResult
	_ = json.Unmarshal([]byte(out), &res)
	if err != nil || res.Status != "finalized" || res.Hard || !cancelled(t, f, "20260927-100000-abcd") {
		t.Fatalf("cancel = %+v, %v", res, err)
	}
}

func TestCancelHardAfterGrace(t *testing.T) {
	f := newCloudFixture(t)
	exec := seedRun(t, f, "20260927-100000-abcd", "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	out, _, err := execute(t, "cancel", "--json", "--grace", "30ms", "--poll", "10ms", "20260927-100000-abcd")
	var res cancelResult
	_ = json.Unmarshal([]byte(out), &res)
	if err != nil || res.Status != "cancelled" || !res.Hard {
		t.Fatalf("cancel = %+v, %v", res, err)
	}
	if e := f.run.State(exec); e != backend.StateCancelled {
		t.Fatalf("execution state = %s", e)
	}
}

func writeStage(t *testing.T, f *cloudFixture, id, exec, stage string) {
	t.Helper()
	b, _ := blob.OpenBucket(context.Background(), f.bucket)
	defer b.Close()
	_ = runstore.Open(b, "acme-app", id).WriteRecord(context.Background(), &runstore.Record{Version: 1, RunID: id, Execution: exec,
		Status: runstore.StatusRunning, Stage: stage, Outcome: runstore.OutcomeNone})
}

func TestCancelCountsWritebackAsFinalized(t *testing.T) {
	f := newCloudFixture(t)
	exec := seedRun(t, f, "20260927-100000-abcd", "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	writeStage(t, f, "20260927-100000-abcd", exec, "writeback")
	out, _, err := execute(t, "cancel", "--json", "--grace", "30ms", "--poll", "10ms", "20260927-100000-abcd")
	var res cancelResult
	_ = json.Unmarshal([]byte(out), &res)
	if err != nil || res.Status != "finalized" || res.Hard || f.run.State(exec) != backend.StateRunning {
		t.Fatalf("cancel during writeback = %+v, %v, state %s", res, err, f.run.State(exec))
	}
}

func TestCancelNeverHardCancelsDuringFinalize(t *testing.T) {
	f := newCloudFixture(t)
	exec := seedRun(t, f, "20260927-100000-abcd", "", "", true)
	f.run.SetState(exec, backend.StateRunning)
	writeStage(t, f, "20260927-100000-abcd", exec, "finalize")
	_, _, err := execute(t, "cancel", "--grace", "20ms", "--finalize-wait", "40ms", "--poll", "10ms", "20260927-100000-abcd")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "finalizing") {
		t.Fatalf("err = %v", err)
	}
	if f.run.State(exec) != backend.StateRunning {
		t.Fatal("cancel hard-cancelled a run mid-finalize")
	}
}

func TestCancelNotLaunchedAndFinished(t *testing.T) {
	f := newCloudFixture(t)
	seedRun(t, f, "20260927-100000-aaaa", "", "", false)
	out, _, err := execute(t, "cancel", "--json", "20260927-100000-aaaa")
	if err != nil || !json.Valid([]byte(out)) || !cancelled(t, f, "20260927-100000-aaaa") {
		t.Fatalf("not launched: %s, %v", out, err)
	}
	exec := seedRun(t, f, "20260927-100000-bbbb", "", "", true)
	f.run.SetState(exec, backend.StateSucceeded)
	out, _, err = execute(t, "cancel", "--json", "20260927-100000-bbbb")
	var res cancelResult
	_ = json.Unmarshal([]byte(out), &res)
	if err != nil || res.Status != "already-finished" || cancelled(t, f, "20260927-100000-bbbb") {
		t.Fatalf("finished: %+v, %v", res, err)
	}
}
```

Add `(*gcpfake.Run).State(exec string) backend.State` to the fake in this task.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/cli/ -run TestCancel`
Expected: FAIL (`cancel` is not a command yet)

- [ ] **Step 3: Implement** `cancel.go` as specified. `emit` prints a `cancelResult` as JSON or human text and returns nil. The hidden `--finalize-wait` flag (`o.finalizeWait`, default 10m) and the hidden `--poll` flag (default 10s) exist for tests. In the loop, `finalizeWait` is `o.finalizeWait`. The wait loop:

```go
	start := time.Now()
	for {
		rec, rerr := s.ReadRecord(ctx)
		if rerr != nil && !errors.Is(rerr, runstore.ErrNotFound) {
			return remote(rerr)
		}
		if rec != nil && (rec.Status != runstore.StatusRunning || rec.Stage == "writeback") {
			return emit(cancelResult{Run: ref, Status: "finalized", Marker: true})
		}
		e, err := env.be.Execution(ctx, l.Execution)
		if err != nil {
			return remote(err)
		}
		if e.State.Terminal() {
			return emit(cancelResult{Run: ref, Status: "finalized", Marker: true})
		}
		finalizing := rec != nil && rec.Stage == "finalize"
		limit := o.grace
		if finalizing {
			limit += o.finalizeWait
		}
		if time.Since(start) >= limit && !finalizing {
			break
		}
		if time.Since(start) >= limit { // still finalizing after the extension: leave it to the task timeout
			return userErr("the run is still finalizing after %s; not cancelling it mid-finalize; check fugaro diagnose %s", limit, ref)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(o.poll):
		}
	}
	if err := env.be.Cancel(ctx, l.Execution); err != nil {
		return remote(err)
	}
	return emit(cancelResult{Run: ref, Status: "cancelled", Marker: true, Hard: true})
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/cli/ ./internal/gcpfake/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/cli/cancel*.go internal/cli/root.go internal/gcpfake/run.go
git commit -m "cli: add fugaro cancel with a finalize grace period"
```

---

### Task 14: Secret Manager: `fugaro secrets set` (stdin only) and `fugaro secrets ls`

**Files:**
- Create: `internal/backend/gcp/secrets.go`, `internal/backend/gcp/secrets_test.go`
- Create: `internal/gcpfake/secrets.go`
- Create: `internal/cli/secrets.go`, `internal/cli/secrets_test.go`
- Modify: `internal/cli/root.go`, `go.mod`, `go.sum` (add `golang.org/x/term`)

**Interfaces:**
- Consumes: `gcp.SecretID` and `config.ReservedSecrets` (Task 1); `gcp.Options` and `gcp.Endpoints` (Task 9); `cloudEnv` (Task 10); `agent.Redact`
- Produces:
  - `gcp.NewSecrets(ctx, o Options) (*Secrets, error)`
  - `(*Secrets).Set(ctx, id string, value []byte, labels map[string]string) (version string, err error)`
  - `(*Secrets).List(ctx, labels map[string]string) ([]SecretInfo, error)`, with `SecretInfo{ID string; Created time.Time; Labels map[string]string}`
  - `(*Secrets).Delete(ctx, id string) error`, which only the live test uses, for cleanup
  - `gcpfake.NewSecrets(t) *Secrets` with `Latest(id string) []byte` and `FailAddVersion string`. When `FailAddVersion` is set, the fake answers `addVersion` with a 400 whose message is that string, so a test can put the payload in it.
  - `cli.readSecret(in io.Reader, prompt io.Writer, multiline bool) ([]byte, error)`

**The command:**
- **`fugaro secrets set NAME [--repo R] [--json]`**
  - `NAME` is a logical name: one of `config.ReservedSecrets`, or a workflow secret matching `^[a-z0-9][a-z0-9-]{0,62}$`.
  - The secret ID is `gcp.SecretID(slug, NAME)`, with labels `fugaro: managed` and `fugaro_repo: <slug sanitized to [a-z0-9_-]>` (underscore: Secret Manager's filter docs don't cover hyphenated label keys).
  - A missing secret is created with automatic replication, and then the value is added as a new version.
  - Output is the ID and the version only: `stored fugaro-acme-app-claude-oauth-token, version 3`. JSON is `{"secret": …, "version": …}`.
  - A second positional argument is exit 1: "pass the value on stdin, never as an argument: arguments land in shell history and process lists".
- **Reading the value** (`readSecret`):
  - When stdin is a terminal (`*os.File` and `term.IsTerminal`), print `Paste the value for NAME (input hidden), then press Enter:` to stderr and read with `term.ReadPassword`.
  - Otherwise read stdin up to 64 KiB. More than that is an error.
  - Trim one trailing `\n` or `\r\n`.
  - Refuse an empty value or one shorter than 4 bytes, which is the redaction floor.
  - Refuse any remaining `\n` or `\r` unless `NAME` is `github-app-key`, whose value is a PEM.
- **Errors** from the Secret Manager call pass through `agent.Redact(msg, []string{value})` before they are returned.
- **`fugaro secrets ls [--repo R] [--json]`** lists the secrets labelled `fugaro_repo: <slug>` by ID and creation time. Values are never read: the command has no access call at all.

`golang.org/x/term` is justified because a hidden prompt is the only safe way to type a subscription token. It is maintained by the Go team.

**Where the user runs it.** `claude setup-token` prints the token in the user's own terminal. The user then runs `fugaro secrets set claude-oauth-token --repo <R>` in that same terminal and pastes at the hidden prompt. The controller never asks for the token, and never runs a command that has it in argv. For file-held tokens such as the EdgeWeb repository access token, the controller may run `fugaro secrets set bitbucket-token --repo edgeappinc/edgeweb < ~/.config/fugaro-edgeweb-token`, because a redirect keeps the value out of argv and out of the transcript.

- [ ] **Step 1: Write the failing tests**

`internal/cli/secrets_test.go`:

```go
package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
)

const tokenValue = "sk-ant-oat01-EXAMPLEEXAMPLEEXAMPLE"

func secretsFixture(t *testing.T) *gcpfake.Secrets {
	t.Helper()
	sm := gcpfake.NewSecrets(t)
	newCloudFixture(t, "secret_manager: "+sm.URL+"/")
	return sm
}

func TestSecretsSetFromPipe(t *testing.T) {
	sm := secretsFixture(t)
	out, errOut, err := executeStdin(t, tokenValue+"\n", "secrets", "set", "claude-oauth-token", "--repo", "acme/app")
	if err != nil {
		t.Fatal(err)
	}
	if got := string(sm.Latest("fugaro-acme-app-claude-oauth-token")); got != tokenValue {
		t.Fatalf("stored %q", got)
	}
	if !strings.Contains(out, "fugaro-acme-app-claude-oauth-token") || strings.Contains(out+errOut, tokenValue) {
		t.Fatalf("stdout %q, stderr %q", out, errOut)
	}
	if _, _, err := executeStdin(t, "second-value\r\n", "secrets", "set", "claude-oauth-token", "--repo", "acme/app"); err != nil {
		t.Fatal(err)
	}
	if got := string(sm.Latest("fugaro-acme-app-claude-oauth-token")); got != "second-value" {
		t.Fatalf("second version = %q", got)
	}
}

func TestSecretsSetNeverEchoes(t *testing.T) {
	sm := secretsFixture(t)
	sm.FailAddVersion = "invalid payload " + tokenValue // a server that echoes what it got
	out, errOut, err := executeStdin(t, tokenValue, "secrets", "set", "claude-oauth-token", "--repo", "acme/app", "--json")
	if err == nil || ExitCode(err) != ExitRemoteError {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(out+errOut+err.Error(), tokenValue) {
		t.Fatalf("the value leaked: out %q, err %q, %v", out, errOut, err)
	}
	if _, _, err := executeStdin(t, "", "secrets", "set", "claude-oauth-token", tokenValue); ExitCode(err) != ExitUserError || strings.Contains(err.Error(), tokenValue) || !strings.Contains(err.Error(), "stdin") {
		t.Fatalf("value as an argument: %v", err)
	}
	help, _, _ := execute(t, "secrets", "set", "--help")
	if strings.Contains(help, "--value") {
		t.Fatal("secrets set grew a --value flag")
	}
}

func TestSecretsSetRejectsMultiline(t *testing.T) {
	secretsFixture(t)
	if _, _, err := executeStdin(t, "line1\nline2\n", "secrets", "set", "bitbucket-token", "--repo", "acme/app"); ExitCode(err) != ExitUserError || strings.Contains(err.Error(), "line1") {
		t.Fatalf("multi-line token: %v", err)
	}
	pem := "-----BEGIN PRIVATE KEY-----\nMIIB\n-----END PRIVATE KEY-----\n"
	if _, _, err := executeStdin(t, pem, "secrets", "set", "github-app-key", "--repo", "acme/app"); err != nil {
		t.Fatalf("PEM: %v", err)
	}
	for _, v := range []string{"", "abc", "\n"} {
		if _, _, err := executeStdin(t, v, "secrets", "set", "bitbucket-token", "--repo", "acme/app"); ExitCode(err) != ExitUserError {
			t.Errorf("value %q accepted", v)
		}
	}
	if _, _, err := executeStdin(t, tokenValue, "secrets", "set", "Bad_Name", "--repo", "acme/app"); ExitCode(err) != ExitUserError {
		t.Error("bad name accepted")
	}
}

func TestSecretsLs(t *testing.T) {
	secretsFixture(t)
	_, _, _ = executeStdin(t, tokenValue, "secrets", "set", "claude-oauth-token", "--repo", "acme/app")
	out, _, err := execute(t, "secrets", "ls", "--repo", "acme/app")
	if err != nil || !strings.Contains(out, "fugaro-acme-app-claude-oauth-token") || strings.Contains(out, tokenValue) {
		t.Fatalf("ls = %s, %v", out, err)
	}
}
```

`executeStdin` already exists in `image_test.go`. It passes a `strings.Reader`, not an `*os.File`, so these tests take the pipe path.

`internal/backend/gcp/secrets_test.go` checks the REST sequence against the fake:

```go
package gcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/gcpfake"
)

func TestSecretsSetSequence(t *testing.T) {
	ctx := context.Background()
	sm := gcpfake.NewSecrets(t)
	s, err := NewSecrets(ctx, Options{Project: "proj-1234", Endpoints: Endpoints{SecretManager: sm.URL + "/", NoAuth: true}})
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{"fugaro": "managed", "fugaro_repo": "acme-app"}
	v1, err := s.Set(ctx, "fugaro-acme-app-bitbucket-token", []byte("tok-1"), labels)
	if err != nil || !strings.HasSuffix(v1, "/versions/1") {
		t.Fatalf("first Set = %q, %v", v1, err)
	}
	if _, err := s.Set(ctx, "fugaro-acme-app-bitbucket-token", []byte("tok-2"), labels); err != nil {
		t.Fatal(err)
	}
	var calls []string
	for _, r := range sm.Requests() {
		calls = append(calls, r.Method+" "+r.Path[strings.LastIndex(r.Path, "/")+1:])
	}
	want := []string{
		"GET fugaro-acme-app-bitbucket-token", "POST secrets", "POST fugaro-acme-app-bitbucket-token:addVersion", // new secret
		"GET fugaro-acme-app-bitbucket-token", "POST fugaro-acme-app-bitbucket-token:addVersion", // existing one
	}
	if !slices.Equal(calls, want) {
		t.Fatalf("calls = %v", calls)
	}
	create := sm.Requests()[1]
	var sec struct {
		Labels      map[string]string `json:"labels"`
		Replication struct {
			Automatic *struct{} `json:"automatic"`
		} `json:"replication"`
	}
	if err := json.Unmarshal(create.Body, &sec); err != nil || sec.Labels["fugaro_repo"] != "acme-app" || sec.Replication.Automatic == nil || !strings.Contains(create.Query, "secretId=fugaro-acme-app-bitbucket-token") {
		t.Fatalf("create = %s?%s", create.Body, create.Query)
	}
	var add struct {
		Payload struct{ Data string } `json:"payload"`
	}
	_ = json.Unmarshal(sm.Requests()[4].Body, &add)
	if got, _ := base64.StdEncoding.DecodeString(add.Payload.Data); string(got) != "tok-2" {
		t.Fatalf("payload = %q", got)
	}
	list, err := s.List(ctx, map[string]string{"fugaro_repo": "acme-app"})
	if err != nil || len(list) != 1 || list[0].ID != "fugaro-acme-app-bitbucket-token" {
		t.Fatalf("List = %+v, %v", list, err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/cli/ ./internal/backend/gcp/ -run Secrets`
Expected: FAIL (`secrets` is not a command, and `NewSecrets` is undefined)

- [ ] **Step 3: Implement**

`internal/backend/gcp/secrets.go`:

```go
package gcp

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"google.golang.org/api/googleapi"
	secretmanager "google.golang.org/api/secretmanager/v1"
)

// Secrets manages a repository's secrets in Secret Manager. It never reads
// a value back: Fugaro only writes secrets, and the platform mounts them.
type Secrets struct {
	svc     *secretmanager.Service
	project string
}

// SecretInfo describes a secret without its value.
type SecretInfo struct {
	ID      string            `json:"id"`
	Created time.Time         `json:"created"`
	Labels  map[string]string `json:"labels,omitempty"`
}

// NewSecrets connects to Secret Manager.
func NewSecrets(ctx context.Context, o Options) (*Secrets, error) {
	svc, err := secretmanager.NewService(ctx, o.client(o.Endpoints.SecretManager)...)
	if err != nil {
		return nil, fmt.Errorf("connecting to Secret Manager: %w", err)
	}
	return &Secrets{svc: svc, project: o.Project}, nil
}

// Set stores value as a new version of secret id, creating the secret with
// labels if it does not exist. It returns the version's resource name.
func (s *Secrets) Set(ctx context.Context, id string, value []byte, labels map[string]string) (string, error) {
	name := "projects/" + s.project + "/secrets/" + id
	_, err := s.svc.Projects.Secrets.Get(name).Context(ctx).Do()
	var ae *googleapi.Error
	if errors.As(err, &ae) && ae.Code == http.StatusNotFound {
		sec := &secretmanager.Secret{Labels: labels, Replication: &secretmanager.Replication{Automatic: &secretmanager.Automatic{}}}
		if _, err = s.svc.Projects.Secrets.Create("projects/"+s.project, sec).SecretId(id).Context(ctx).Do(); err != nil {
			return "", fmt.Errorf("creating secret %s: %w", id, err)
		}
	} else if err != nil {
		return "", fmt.Errorf("reading secret %s: %w", id, err)
	}
	v, err := s.svc.Projects.Secrets.AddVersion(name, &secretmanager.AddSecretVersionRequest{
		Payload: &secretmanager.SecretPayload{Data: base64.StdEncoding.EncodeToString(value)},
	}).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("adding a version to secret %s: %w", id, err)
	}
	return v.Name, nil
}

// List lists secrets carrying all of labels.
func (s *Secrets) List(ctx context.Context, labels map[string]string) ([]SecretInfo, error) {
	var filter []string
	for k, v := range labels {
		filter = append(filter, "labels."+k+"="+v)
	}
	var out []SecretInfo
	err := s.svc.Projects.Secrets.List("projects/"+s.project).Filter(strings.Join(filter, " AND ")).Pages(ctx, func(p *secretmanager.ListSecretsResponse) error {
		for _, sec := range p.Secrets {
			created, _ := time.Parse(time.RFC3339Nano, sec.CreateTime)
			out = append(out, SecretInfo{ID: sec.Name[strings.LastIndex(sec.Name, "/")+1:], Created: created, Labels: sec.Labels})
		}
		return nil
	})
	return out, err
}

// Delete removes secret id and all its versions.
func (s *Secrets) Delete(ctx context.Context, id string) error {
	_, err := s.svc.Projects.Secrets.Delete("projects/" + s.project + "/secrets/" + id).Context(ctx).Do()
	return err
}
```

`internal/cli/secrets.go` implements `readSecret` and the two commands as specified. `internal/gcpfake/secrets.go` implements:
- `GET /v1/projects/{p}/secrets/{id}`, answering 404 when the secret is missing
- `POST /v1/projects/{p}/secrets?secretId=…`
- `POST /v1/projects/{p}/secrets/{id}:addVersion`, which decodes the base64 payload
- `GET /v1/projects/{p}/secrets?filter=…`, which parses the `labels.k=v` terms joined by ` AND `
- `DELETE /v1/projects/{p}/secrets/{id}`

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./internal/cli/ ./internal/backend/gcp/ ./internal/gcpfake/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/backend/gcp/secrets*.go internal/gcpfake/secrets.go internal/cli/secrets*.go internal/cli/root.go
git commit -m "cli: add fugaro secrets set (stdin or hidden prompt only) and secrets ls"
```

---

### Task 15: Cloud Build: one base for render and build, a token-based git secret, workflow secrets, and cloud `fugaro image build`

**Files:**
- Modify: `images/derived/cloudbuild.yaml`, `images/images.go` (embed `CloudBuild`), `images/cloudbuild_test.go`
- Create: `internal/backend/gcp/build.go`, `internal/backend/gcp/build_test.go`
- Create: `internal/gcpfake/build.go`
- Modify: `internal/cli/image.go` (the non-local path), `internal/cli/image_test.go`: replace `TestImageBuildNeedsLocal` and `TestImageBuildLocalRejectsRepo`

**Interfaces:**
- Consumes: `images.CloudBuild`; `gcp.SecretID` and `gcp.ImageName` (Task 1); `gcp.Options` (Task 9); `cloudEnv` (Task 10); `loadCheckout`; `image.HTTPSOrigin`; `image.BaseRef`
- Produces:
  - `images.CloudBuild []byte`
  - `gcp.BuildSpec{Slug, RepoURL, BaseBranch, Workflow, Base, Image, GitSecretID, GitUser, ServiceAccount, MachineType string; WorkflowSecrets []config.Secret}`
  - `gcp.BuildRequest(project string, s BuildSpec) (*cloudbuild.Build, error)`, which is pure
  - `gcp.NewBuilder(ctx, o Options, region string) (*Builder, error)`
  - `(*Builder).Submit(ctx, BuildSpec) (BuildResult, error)` and `(*Builder).Wait(ctx, id string, poll time.Duration) (BuildResult, error)`
  - `gcp.BuildResult{ID, Status, Image, Digest, LogURL string}`, with JSON tags
  - `fugaro image build [--workflow W] [--repo R] [--base IMG] [--no-wait] [--json]` without `--local`

**The `cloudbuild.yaml` changes** (M3 deferrals):
1. **Render and build use the same base.** The `build` step no longer runs `docker pull`. It inspects the image the `render` step ran on the same worker: `docker image inspect --format '{{index .RepoDigests 0}}' "$$FUGARO_BASE"`. If that fails, it stops with "the render step's base image is not on this worker". The step writes the digest to `/workspace/base-digest` and echoes it. A nightly republish of `:X.Y.Z` between the two steps can no longer give render and build different bases.
2. **The git credential is the provider token.** `_GIT_SECRET` names a version of the token secret the job already uses (`fugaro-<slug>-bitbucket-token`), and the new `_GIT_USER` gives its HTTPS username. The `source` step builds the credential-store line itself, from `REPO_URL`'s host, after checking that `REPO_URL` is `https://`. Only the `source` step sees `GIT_TOKEN`. This is one secret per repository instead of two copies of the same token.
3. **Workflow secrets.** `_SECRET_ENVS` is a space-separated list of variable names. `fugaro image build` adds each one to `availableSecrets` and to the build step's `secretEnv`. The build step loops over them, rejects a name that isn't `[A-Z0-9_]+`, and passes `--secret id=$e,env=$e`, which the template already mounts as `required=false`.
   - The raw YAML has no `secretEnv` for them: only `gcp.BuildRequest` adds it. M5's nightly trigger must build its request the same way, not submit the file as is. The After M4 section repeats this.
   - `--secret …,env=` needs BuildKit env-secret support in `gcr.io/cloud-builders/docker`. The sandbox fixture declares one workflow secret (`sandbox-probe`) and an `image.setup` step that fails the build unless it arrives (Task 17), so Task 20's first cloud build exercises this path live.

- [ ] **Step 1: Write the failing tests**

Update `images/cloudbuild_test.go` `TestCloudBuildConfig`:

```go
	for _, k := range []string{"_REPO_URL", "_BASE_BRANCH", "_WORKFLOW", "_FUGARO_BASE", "_IMAGE", "_GIT_SECRET", "_GIT_USER", "_SECRET_ENVS"} {
		if _, ok := cb.Substitutions[k]; !ok {
			t.Errorf("substitution %s is not declared", k)
		}
	}
	build := strings.Join(cb.Steps[2].Args, " ")
	if strings.Contains(build, "docker pull") || !strings.Contains(build, "docker image inspect") {
		t.Error("the build step must reuse the render step's base image, not pull it again")
	}
	if !strings.Contains(build, "SECRET_ENVS") || !strings.Contains(build, "--secret") {
		t.Error("the build step does not pass workflow secrets")
	}
	if !slices.Equal(cb.Steps[0].SecretEnv, []string{"GIT_TOKEN"}) {
		t.Errorf("source secretEnv = %v", cb.Steps[0].SecretEnv)
	}
	if src := strings.Join(cb.Steps[0].Args, " "); !strings.Contains(src, "https://*)") {
		t.Error("the source step must refuse a non-https REPO_URL")
	}
```

Replace the old `GIT_CREDENTIALS` assertions with these. Keep `TestCloudBuildNoSubstitutionsInScripts`, which still holds: every value reaches a script through `env:`.

`internal/backend/gcp/build_test.go`:

```go
package gcp

import (
	"context"
	"slices"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/gcpfake"
)

var spec = BuildSpec{
	Slug: "acme-app", RepoURL: "https://bitbucket.org/acme/app.git", BaseBranch: "main", Workflow: "web",
	Base: "us-east5-docker.pkg.dev/p/fugaro/fugaro-web-node:dev-abc", Image: "us-east5-docker.pkg.dev/p/fugaro/acme-app-web",
	GitSecretID: "fugaro-acme-app-bitbucket-token", GitUser: "x-token-auth",
	ServiceAccount: "fugaro-build@proj-1234.iam.gserviceaccount.com", MachineType: "E2_HIGHCPU_8",
	WorkflowSecrets: []config.Secret{{Name: "npm-token", Env: "NPM_TOKEN"}},
}

func TestBuildRequest(t *testing.T) {
	b, err := BuildRequest("proj-1234", spec)
	if err != nil {
		t.Fatal(err)
	}
	if b.Substitutions["_SECRET_ENVS"] != "NPM_TOKEN" || b.Substitutions["_GIT_USER"] != "x-token-auth" || b.Substitutions["_IMAGE"] != spec.Image {
		t.Fatalf("substitutions = %v", b.Substitutions)
	}
	var versions []string
	for _, s := range b.AvailableSecrets.SecretManager {
		versions = append(versions, s.Env+"="+s.VersionName)
	}
	want := []string{"GIT_TOKEN=projects/proj-1234/secrets/fugaro-acme-app-bitbucket-token/versions/latest", "NPM_TOKEN=projects/proj-1234/secrets/fugaro-acme-app-npm-token/versions/latest"}
	if !slices.Equal(versions, want) {
		t.Fatalf("availableSecrets = %v", versions)
	}
	var build = b.Steps[2]
	if build.Id != "build" || !slices.Contains(build.SecretEnv, "NPM_TOKEN") || slices.Contains(b.Steps[1].SecretEnv, "NPM_TOKEN") {
		t.Fatalf("build step secretEnv = %v", build.SecretEnv)
	}
	if b.ServiceAccount != "projects/proj-1234/serviceAccounts/"+spec.ServiceAccount || b.Options.MachineType != "E2_HIGHCPU_8" || b.Options.Logging != "CLOUD_LOGGING_ONLY" {
		t.Fatalf("build = %+v / %+v", b.ServiceAccount, b.Options)
	}
	bad := spec
	bad.WorkflowSecrets = []config.Secret{{Name: "x", Env: "BAD NAME"}}
	if _, err := BuildRequest("proj-1234", bad); err == nil {
		t.Fatal("an unsafe secret env name was accepted")
	}
}

func TestSubmitAndWait(t *testing.T) {
	fb := gcpfake.NewBuild(t)
	b, err := NewBuilder(context.Background(), Options{Project: "proj-1234", Endpoints: Endpoints{CloudBuild: fb.URL + "/", NoAuth: true}}, "us-east5")
	if err != nil {
		t.Fatal(err)
	}
	res, err := b.Submit(context.Background(), spec)
	if err != nil || res.ID == "" || res.LogURL == "" {
		t.Fatalf("Submit = %+v, %v", res, err)
	}
	done, err := b.Wait(context.Background(), res.ID, 0)
	if err != nil || done.Status != "SUCCESS" || done.Digest == "" {
		t.Fatalf("Wait = %+v, %v", done, err)
	}
	fb.Outcome = "FAILURE"
	res, _ = b.Submit(context.Background(), spec)
	if done, err := b.Wait(context.Background(), res.ID, 0); err == nil || done.Status != "FAILURE" {
		t.Fatalf("failed build = %+v, %v", done, err)
	}
}
```

In `internal/cli/image_test.go`:
- Delete `TestImageBuildNeedsLocal` and `TestImageBuildLocalRejectsRepo`.
- Add `TestImageBuildCloud`. It builds a checkout with `checkoutWith(t, npmFiles())`, sets an https Bitbucket origin (`git remote add origin https://bitbucket.org/acme/app.git`), and uses `newCloudFixture(t, "cloud_build: "+fb.URL+"/")`. The local config needs `registry` and `build.service_account`, which the test adds with `f.appendConfig(t, "registry: us-east5-docker.pkg.dev/proj-1234/fugaro\nbuild: { service_account: fugaro-build@proj-1234.iam.gserviceaccount.com }\n")`.
  - It runs `image build --json --base <ref>` and asserts `status: SUCCESS`.
  - The fake's last build must have `_REPO_URL` equal to `https://bitbucket.org/acme/app.git` and `_GIT_SECRET` naming `fugaro-acme-app-bitbucket-token`.
- Add `TestImageBuildCloudGitHubNeedsM5`: a `git.provider: github` config exits 1 with "M5".
- Add `TestImageBuildCloudNeedsRegistry`: no `registry` in the local config exits 1, mentioning "registry".
- Add `TestImageBuildCloudFailureIsRemote`: `fb.Outcome = "FAILURE"` gives exit 2.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./images/ ./internal/backend/gcp/ ./internal/cli/ -run 'CloudBuild|BuildRequest|SubmitAndWait|ImageBuildCloud'`
Expected: FAIL

- [ ] **Step 3: Implement**

`images/derived/cloudbuild.yaml`, the new substitutions and steps. Keep the header comment, and extend it with points 1 to 3 above.

```yaml
substitutions:
  _REPO_URL: ""       # https clone URL without credentials; also the checkout's origin
  _BASE_BRANCH: main
  _WORKFLOW: ""
  _FUGARO_BASE: ""    # ghcr.io/dimipaun/fugaro-<base>:<version>; build pins the pulled image by digest
  _IMAGE: ""          # the Artifact Registry image name, without a tag
  _GIT_SECRET: ""     # Secret Manager version of the provider token (the job's own)
  _GIT_USER: ""       # the token's https username, such as x-token-auth for Bitbucket
  _SECRET_ENVS: ""    # space-separated workflow secret variables; each is also in availableSecrets
options:
  logging: CLOUD_LOGGING_ONLY
availableSecrets:
  secretManager:
    - versionName: ${_GIT_SECRET}
      env: GIT_TOKEN
steps:
  - id: source
    name: gcr.io/cloud-builders/git
    entrypoint: bash
    secretEnv: [GIT_TOKEN]
    env: ["BASE_BRANCH=${_BASE_BRANCH}", "REPO_URL=${_REPO_URL}", "GIT_USER=${_GIT_USER}"]
    args:
      - -c
      - |
        set -eu
        case "$$REPO_URL" in https://*) ;; *) echo "REPO_URL must be an https URL" >&2; exit 1 ;; esac
        host=$${REPO_URL#https://}; host=$${host%%/*}
        (umask 077 && printf 'https://%s:%s@%s\n' "$$GIT_USER" "$$GIT_TOKEN" "$$host" > /builder/home/.git-credentials)
        git -c credential.helper='store --file=/builder/home/.git-credentials' \
          clone --quiet --depth 1 --branch "$$BASE_BRANCH" -- "$$REPO_URL" /workspace/src
        install -d -m 0777 /workspace/context
  - id: render
    name: ${_FUGARO_BASE}
    dir: src
    entrypoint: sh
    env: [GIT_CONFIG_COUNT=1, GIT_CONFIG_KEY_0=safe.directory, "GIT_CONFIG_VALUE_0=*", "WORKFLOW=${_WORKFLOW}"]
    args: [-c, 'fugaro image render --workflow "$$WORKFLOW" > /workspace/context/Dockerfile']
  # Reuses the exact base image the render step ran (no second pull), so a
  # republished tag between the steps cannot split render and build.
  - id: build
    name: gcr.io/cloud-builders/docker
    entrypoint: bash
    env:
      - DOCKER_BUILDKIT=1
      - "FUGARO_BASE=${_FUGARO_BASE}"
      - "REPO_URL=${_REPO_URL}"
      - "BASE_BRANCH=${_BASE_BRANCH}"
      - "IMAGE=${_IMAGE}"
      - "SECRET_ENVS=${_SECRET_ENVS}"
    args:
      - -c
      - |
        set -eu
        trap 'rm -f /builder/home/.git-credentials' EXIT
        base=$$(docker image inspect --format '{{index .RepoDigests 0}}' "$$FUGARO_BASE") \
          || { echo "the render step's base image is not on this worker" >&2; exit 1; }
        echo "base $$base" | tee /workspace/base-digest
        set --
        for e in $$SECRET_ENVS; do
          case "$$e" in *[!A-Z0-9_]*) echo "bad secret variable name" >&2; exit 1 ;; esac
          set -- "$$@" --secret "id=$$e,env=$$e"
        done
        docker build --progress plain \
          --build-arg "FUGARO_BASE=$$base" \
          --build-arg "REPO_URL=$$REPO_URL" \
          --build-arg "BASE_BRANCH=$$BASE_BRANCH" \
          --secret id=git-credentials,src=/builder/home/.git-credentials \
          "$$@" \
          --tag "$$IMAGE:latest" /workspace/context
images: ["${_IMAGE}:latest"]
```

`images/images.go`:

```go
// CloudBuild is images/derived/cloudbuild.yaml, which fugaro image build
// submits (with per-workflow secrets added) and M5's nightly trigger runs.
//
//go:embed derived/cloudbuild.yaml
var CloudBuild []byte
```

`internal/backend/gcp/build.go`:
- `BuildRequest` unmarshals `images.CloudBuild` with yaml.v3 into `map[string]any`, marshals it to JSON, and unmarshals that into `cloudbuild.Build`. The JSON field names match the YAML keys.
- It then sets `Substitutions` from the spec, including `_GIT_SECRET = projects/<p>/secrets/<GitSecretID>/versions/latest`.
- It replaces `availableSecrets.secretManager[0].versionName` with that literal, so the request doesn't depend on substitution inside `availableSecrets`.
- It appends one `SecretManagerSecret{Env: s.Env, VersionName: projects/<p>/secrets/<SecretID(slug, s.Name)>/versions/latest}` per workflow secret, and adds `s.Env` to the step with `Id == "build"`.
- It validates every env name against `^[A-Z_][A-Z0-9_]*$`.
- It sets:
  - `ServiceAccount = "projects/<p>/serviceAccounts/<email>"`
  - `Options.MachineType`
  - `Options.Logging = "CLOUD_LOGGING_ONLY"`
  - `Timeout = "3600s"`
- `Submit` calls `svc.Projects.Locations.Builds.Create("projects/<p>/locations/<region>", build)` and reads `metadata.build.id` and `metadata.build.logUrl` from the operation.
- `Wait` polls `Builds.Get("projects/<p>/locations/<region>/builds/<id>")` every `poll` (10s by default; zero in tests means 10ms) until the status is `SUCCESS`, `FAILURE`, `INTERNAL_ERROR`, `TIMEOUT`, `CANCELLED` or `EXPIRED`. A non-`SUCCESS` result comes back with an error naming the status and the log URL. On success, `Digest` is `results.images[0].digest`.

`internal/gcpfake/build.go`:
- `POST /v1/projects/{p}/locations/{l}/builds` stores the decoded build, assigns an ID, and answers an operation with `metadata.build{id, logUrl, status: QUEUED}`.
- `GET /v1/projects/{p}/locations/{l}/builds/{id}` answers `status: Outcome`, which defaults to `SUCCESS`, and on success `results.images: [{name, digest: "sha256:…"}]`.
- `(*Build).Last() map[string]any` returns the last build request for assertions.

`internal/cli/image.go`: `runImageBuild` without `--local`:

```go
	if !o.local {
		return runImageBuildCloud(cmd, o)
	}
```

`runImageBuildCloud`:
1. `openCloud` for the local config and the `gcp.Options`.
2. `loadCheckout`.
3. Require `cfg.Git.Provider == "bitbucket"`. Otherwise exit 1: "Cloud Build images for GitHub repositories need a token-minting step that arrives in M5; use --local".
4. Require that `lc.Registry` and `lc.Build.ServiceAccount` are set. Otherwise exit 1 naming the missing key.
5. The repo is `--repo`, else `originRepo`. `REPO_URL` is `image.HTTPSOrigin(origin)`, which must start with `https://`.
6. The base is `--base`, else `lc.BaseImage`, else `image.BaseRef`.
7. `gcp.NewBuilder(ctx, env.gcp, lc.BuildRegion())`, then `Submit`, then `Wait` unless `--no-wait`.
8. Print human or JSON output. A build that isn't `SUCCESS` exits 2.

`--repo` changes meaning: it is now the repository identity, which only the cloud path uses, so `--local --repo` stays an error, as today.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./images/ ./internal/backend/gcp/ ./internal/gcpfake/ ./internal/cli/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add images/derived/cloudbuild.yaml images/images.go images/cloudbuild_test.go internal/backend/gcp/build*.go internal/gcpfake/build.go internal/cli/image*.go
git commit -m "image: submit derived-image builds to Cloud Build from one resolved base, with workflow secrets"
```

---

### Task 16: The hermetic cloud end-to-end: `run` → `exec` → `ls` → `logs` → `diagnose` → `cancel`

**Files:**
- Create: `internal/e2e/cloud_test.go`

**Interfaces:**
- Consumes: everything from Tasks 6–13: the built `fugaro` binary (`testutil.BuildFugaro`), `testutil.FakeClaude`, `testutil.NewRemote`, `testutil.FixtureFiles`, `gcpfake.NewRun`, `gcpfake.NewLogging`, and the fake provider's state file
- Produces: no new API. This test is the proof that the CLI and the runner agree on every object in the bucket.

**How it works:**
- The test writes a local config (`FUGARO_CONFIG`) with `bucket_url: file://…`, the fakes' endpoints, `no_auth: true`, and `repos: {acme/app: {base_branch: main, workflows: [app]}}`, then adds the job `fugaro-acme-app-app` to the Run fake.
- `OnRun` plays Cloud Run. For each execution it starts, in a goroutine, the built binary as `fugaro exec` with:
  - the flags `--bucket <file-url> --run <FUGARO_RUN> --workdir <fresh temp> --remote <bare remote> --state-dir <fresh temp> --provider fake --provider-state <file> --claude <fake> --cancel-poll 100ms`
  - the environment Cloud Run and the job would give: `CLOUD_RUN_EXECUTION=<short name>`, `CLOUD_RUN_JOB=<job>`, `FUGARO_PROJECT=proj-1234` and `FUGARO_REGION=us-east5` (C-1: the runner then records the canonical name itself, and the test checks it matches `launch.json` by `backend.SameExecution`), plus `ANTHROPIC_API_KEY=test-key`, `FIXTURE_FAILS_FILE=…` and `HOME=<temp>`
  - `SetState(running)` on start, and `succeeded` or `failed` from the exit code on exit
  - every stderr line fed to `Logging.AddJSONLines(exec, line)`
- The test runs the CLI through the same binary, in a child process with the test's environment.
- `childTimeout` (90s) bounds every child, as in `e2e_test.go`.

- [ ] **Step 1: Write the test**

```go
package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gocloud.dev/blob"
	_ "gocloud.dev/blob/fileblob"

	"github.com/dimipaun/fugaro/internal/backend"
	"github.com/dimipaun/fugaro/internal/gcpfake"
	"github.com/dimipaun/fugaro/internal/gitprov/fake"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/testutil"
)

type cloudRig struct {
	t        *testing.T
	fugaro   string
	cfg      string
	bucket   string // file:// URL
	provider string
	run      *gcpfake.Run
	logging  *gcpfake.Logging
}

func newCloudRig(t *testing.T, claudeScript string) *cloudRig {
	t.Helper()
	testutil.IsolateGit(t)
	r := &cloudRig{t: t, fugaro: testutil.BuildFugaro(t), run: gcpfake.NewRun(t), logging: gcpfake.NewLogging(t)}
	remote := testutil.NewRemote(t, testutil.FixtureFiles(t))
	claude := testutil.FakeClaude(t, claudeScript)
	dir := t.TempDir()
	bucket := filepath.Join(dir, "runs")
	_ = os.MkdirAll(bucket, 0o755)
	r.provider = filepath.Join(dir, "provider.json")
	r.bucket = "file://" + bucket
	r.cfg = filepath.Join(dir, "config.yaml")
	cfg := "version: 1\nproject: proj-1234\nregion: us-east5\nruns_bucket: unused-bucket\nbucket_url: file://" + bucket +
		"\nuser: someone@example.com\nendpoints: { run: " + r.run.URL + "/, logging: " + r.logging.URL + "/, no_auth: true }\n" +
		"repos:\n  acme/app: { base_branch: main, workflows: [app] }\n"
	if err := os.WriteFile(r.cfg, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	r.run.AddJob("fugaro-acme-app-app", "4", "8Gi")
	r.run.OnRun = func(c gcpfake.RunCall) {
		go r.execute(c, "file://"+bucket, remote, claude)
	}
	return r
}

func (r *cloudRig) execute(c gcpfake.RunCall, bucket, remote, claude string) {
	tmp := r.t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.fugaro, "exec", "--bucket", bucket, "--run", c.Env["FUGARO_RUN"],
		"--workdir", filepath.Join(tmp, "work"), "--remote", remote, "--state-dir", filepath.Join(tmp, "state"),
		"--provider", "fake", "--provider-state", r.provider, "--claude", claude, "--cancel-poll", "100ms")
	cmd.Env = append(os.Environ(), "CLOUD_RUN_EXECUTION="+c.Execution, "CLOUD_RUN_JOB="+c.Job,
		"FUGARO_PROJECT=proj-1234", "FUGARO_REGION=us-east5", "ANTHROPIC_API_KEY=test-key",
		"FIXTURE_FAILS_FILE="+filepath.Join(tmp, "fails"), "HOME="+tmp)
	cmd.WaitDelay = 5 * time.Second
	stderr, _ := cmd.StderrPipe()
	full := "projects/proj-1234/locations/us-east5/jobs/" + c.Job + "/executions/" + c.Execution
	r.run.SetState(full, backend.StateRunning)
	if err := cmd.Start(); err != nil {
		r.t.Errorf("starting exec: %v", err)
		return
	}
	buf := make([]byte, 0, 1<<16)
	chunk := make([]byte, 4096)
	for {
		n, err := stderr.Read(chunk)
		buf = append(buf, chunk[:n]...)
		if i := strings.LastIndexByte(string(buf), '\n'); i >= 0 {
			r.logging.AddJSONLines(full, buf[:i+1])
			buf = buf[i+1:]
		}
		if err != nil {
			break
		}
	}
	state := backend.StateSucceeded
	if err := cmd.Wait(); err != nil {
		state = backend.StateFailed
	}
	r.run.SetState(full, state)
}

// cli runs the fugaro CLI and returns stdout.
func (r *cloudRig) cli(args ...string) (string, error) {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.fugaro, args...)
	cmd.Env = append(os.Environ(), "FUGARO_CONFIG="+r.cfg)
	cmd.Dir = r.t.TempDir() // not a checkout
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.Output()
	return string(out), err
}

func (r *cloudRig) waitStatus(id string, want ...string) map[string]any {
	r.t.Helper()
	deadline := time.Now().Add(childTimeout)
	for time.Now().Before(deadline) {
		out, err := r.cli("ls", "--json", "--repo", "acme/app")
		if err != nil {
			r.t.Fatalf("ls: %v", err)
		}
		var got struct {
			Runs   []map[string]any `json:"runs"`
			Totals map[string]any   `json:"totals"`
		}
		_ = json.Unmarshal([]byte(out), &got)
		for _, row := range got.Runs {
			if row["run_id"] == id && contains(want, row["status"].(string)) {
				return row
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	r.t.Fatalf("run %s never reached %v", id, want)
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestCloudRunLsLogsDiagnose(t *testing.T) {
	r := newCloudRig(t, implementOK+"\n"+reviewShip)
	const id = "20260927-100000-abcd"
	out, err := r.cli("run", "--repo", "acme/app", "--run-id", id, "--batch", "e2e", "--json", "Add a feature")
	if err != nil || !strings.Contains(out, `"launched"`) {
		t.Fatalf("run: %s, %v", out, err)
	}
	row := r.waitStatus(id, "succeeded", "failed", "infra_error")
	if row["status"] != "succeeded" || row["pr_url"] == "" || row["stage"] != "writeback" {
		t.Fatalf("row = %v", row)
	}
	// The runner's recorded name and launch.json's name are the same execution (C-1).
	b, _ := blob.OpenBucket(context.Background(), r.bucket)
	s := runstore.Open(b, "acme-app", id)
	rec, _ := s.ReadRecord(context.Background())
	l, _ := s.ReadLaunch(context.Background())
	if rec == nil || l == nil || !backend.SameExecution(rec.Execution, l.Execution) {
		t.Fatalf("record execution %v vs launch %v", rec, l)
	}
	b.Close()
	if again, err := r.cli("run", "--repo", "acme/app", "--run-id", id, "--batch", "e2e", "--json", "Add a feature"); err != nil || !strings.Contains(again, "already-launched") || len(r.run.Executions()) != 1 {
		t.Fatalf("repeat run: %s, %v", again, err)
	}
	logs, err := r.cli("logs", id)
	if err != nil || !strings.Contains(logs, "stage started") || !strings.Contains(logs, "/agent] result") {
		t.Fatalf("logs: %s, %v", logs, err)
	}
	diag, err := r.cli("diagnose", "--json", id)
	var d struct {
		Row map[string]any `json:"row"`
	}
	if err != nil || json.Unmarshal([]byte(diag), &d) != nil || d.Row["status"] != "succeeded" || d.Row["pr_url"] == "" {
		t.Fatalf("diagnose: %s, %v", diag, err)
	}
	st, _ := fake.Load(r.provider)
	if len(st.PRs) != 1 || st.PRs[0].Draft {
		t.Fatalf("provider = %+v", st)
	}
	batch, _ := r.cli("ls", "--json", "--batch", "e2e", "--since", "0")
	if !strings.Contains(batch, `"total_usd"`) || !strings.Contains(batch, id) {
		t.Fatalf("ls --batch: %s", batch)
	}
}

func TestCloudCancel(t *testing.T) {
	block := `{"shell":"sleep 60","text":"never","cost":0.1}`
	r := newCloudRig(t, block)
	const id = "20260927-110000-abcd"
	if _, err := r.cli("run", "--repo", "acme/app", "--run-id", id, "Slow task"); err != nil {
		t.Fatal(err)
	}
	r.waitStatus(id, "running")
	out, err := r.cli("cancel", "--json", "--grace", "60s", "--poll", "200ms", id)
	if err != nil || !strings.Contains(out, `"finalized"`) {
		t.Fatalf("cancel: %s, %v", out, err)
	}
	row := r.waitStatus(id, "cancelled")
	if row["pr_url"] == "" {
		t.Fatalf("a cancelled run after bootstrap must still leave a draft PR: %v", row)
	}
}
```

Reuse `implementOK`, `reviewShip` and `childTimeout` from `e2e_test.go`.

- [ ] **Step 2: Run it.** It should pass the first time once Tasks 6–13 are in. If it doesn't, the failure is a real integration bug: fix it in the owning package, with a unit test there first.

Run: `go test -race -run 'TestCloud' -v ./internal/e2e/`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add internal/e2e/cloud_test.go
git commit -m "e2e: prove run, exec, ls, logs, diagnose and cancel agree on the runs bucket"
```

---

### Task 17: The throwaway GCP bootstrap (`deploy/bootstrap/gcp-m4.sh`) and hidden `fugaro gcp job-spec`

**Files:**
- Create: `internal/cli/gcpcmd.go`, `internal/cli/gcpcmd_test.go`
- Create: `deploy/bootstrap/gcp-m4.sh` (executable), `deploy/bootstrap/bootstrap_test.go`, `deploy/bootstrap/README.md`
- Create: `deploy/bootstrap/sandbox/` (the sandbox repository fixture: `fugaro.yaml`, `package.json`, `package-lock.json`, `test.js`, `README.md`)
- Modify: `internal/cli/root.go`

**Interfaces:**
- Consumes: `gcp.JobName`, `ServiceAccountID`, `SecretID`, `ImageName` and `CheckResources` (Task 1); `config.ReservedSecrets`; `localcfg` (Task 2); `loadCheckout`
- Produces:
  - `fugaro gcp job-spec [--workflow W] [--repo R] [--field F] [--json]`. It is hidden and development-only, and it is the single source of the names and flags the script passes to `gcloud`, so bash never re-implements the naming contract.
  - `jobSpec{Project, Region, Job, ServiceAccountID, ServiceAccount, Image, CPU, Memory, TaskTimeoutS, Env, Secrets, GitSecret, BuildServiceAccount}`
  - The `--field` values:
    - `job`, `sa-id`, `sa`, `image`, `cpu`, `memory`, `task-timeout` and `git-secret`
    - `env`, printed as `K=V,K=V` for `--set-env-vars`
    - `secrets`, printed as `ENV=secret:latest,…` for `--set-secrets`
    - `secret-ids`, one per line
    - `secret-names`, as `<logical>=<id>` lines
    - `build-secret-ids`: the git token secret and the workflow secrets, which is what `fugaro-build` needs
    - `bucket-condition`, the IAM condition expression

**The job's secrets:**
- For the provider: `bitbucket` gives `FUGARO_BITBUCKET_TOKEN` ← `bitbucket-token`. `github` is exit 1 in M4, because the App ID needs Terraform's variables (M5).
- For `agent.auth`:
  - `oauth` gives `CLAUDE_CODE_OAUTH_TOKEN` ← `claude-oauth-token`.
  - `api-key` gives `ANTHROPIC_API_KEY` ← `anthropic-api-key`.
  - `vertex` gives no secret, and the env gains `CLOUD_ML_REGION=<region>` and `ANTHROPIC_VERTEX_PROJECT_ID=<project>`.
- Each workflow secret maps its `env` to `SecretID(slug, name)`.
- `TaskTimeoutS` is `timeouts.total + 120`.
- `Env` holds `FUGARO_BUCKET=gs://<runs_bucket>`, `FUGARO_BACKEND=cloud-run`, `FUGARO_PROJECT=<project>` and `FUGARO_REGION=<region>`. The runner needs the project and region to build its canonical execution name (Task 6).
- **`bucket-condition`** is exactly this, with trailing slashes so `acme-app` can never reach `acme-app-x` (I-5):

  ```
  resource.name.startsWith("projects/_/buckets/<bucket>/objects/runs/<slug>/") || resource.name.startsWith("projects/_/buckets/<bucket>/objects/cache/<slug>/") || resource.name.startsWith("projects/_/buckets/<bucket>/objects/locks/<slug>/")
  ```

  `roles/storage.objectUser` includes `storage.objects.list`, which is checked against the bucket, not an object, so the condition denies listing. The runner never lists: it only gets, creates, overwrites and deletes named objects.
- The spec also runs `gcp.CheckResources`, and refuses when it reports anything.

**The script** (bash, `set -euo pipefail`):
- **Usage:** `gcp-m4.sh [--apply] STEP`. It reads `PROJECT`, `REGION`, `BUCKET`, `REPO`, `WORKFLOW`, `CHECKOUT` (the target repository's checkout, for `job-spec`), `FUGARO` (default `fugaro`), `HEAVY` (default `/Users/dimi/git.lattica/Fugaro/.superpowers/heavy.sh`) and `FUGARO_SRC` (this checkout) from the environment.
- **Dry run by default.** Without `--apply` it prints every command, prefixed `+ `, and runs nothing but read-only `fugaro gcp job-spec` calls.
- **Warnings.** Every step that enables a billable API, creates a resource, or grants IAM prints `⚠ CONFIRM: <what, and what it costs>` first. It runs only with `--apply`, and the controller passes `--apply` only after the user confirms that step.
- **Steps:**
  - `apis` ⚠: `gcloud services enable run.googleapis.com storage.googleapis.com secretmanager.googleapis.com artifactregistry.googleapis.com cloudbuild.googleapis.com logging.googleapis.com iam.googleapis.com --project $PROJECT`
  - `bucket` ⚠:
    - `gcloud storage buckets create gs://$BUCKET --project $PROJECT --location $REGION --uniform-bucket-level-access --public-access-prevention`
    - `gcloud storage buckets update gs://$BUCKET --lifecycle-file=<tmp>`. The lifecycle deletes `runs/` objects older than 90 days, deletes `cache/` objects with `daysSinceCustomTime` over 30, and, as a safety net, deletes `cache/` objects older than 180 days. It uses `matchesPrefix`.
  - `registry` ⚠: `gcloud artifacts repositories create fugaro --repository-format docker --location $REGION --project $PROJECT`
  - `build-sa` ⚠:
    - `gcloud iam service-accounts create fugaro-build`
    - `roles/artifactregistry.writer` on the repository (`gcloud artifacts repositories add-iam-policy-binding`)
    - `roles/logging.logWriter` on the project
  - `config`: writes `~/.config/fugaro/config.yaml` (or `$FUGARO_CONFIG`) from the script's template, and prints it first. It refuses to overwrite an existing file unless `FORCE=1`, and prints a diff when it does. It fills:
    - `user`, from `git config user.email`
    - `base_image`, from `BASE_IMAGE` when set
    - the build service account, `fugaro-build@$PROJECT.iam.gserviceaccount.com`
    - `repos`, from `REPOS`, a space-separated list of `owner/name:branch:workflow`
    - To add a repository later, rerun with `FORCE=1` and every repository listed.
  - `job-sa` ⚠:
    - `gcloud iam service-accounts create $(job-spec --field sa-id)`
    - on the bucket, `roles/storage.objectUser` with the condition from `--field bucket-condition`, which covers the `runs/<slug>/`, `cache/<slug>/` and `locks/<slug>/` prefixes (design §6.1)
  - `secrets`: prints the `fugaro secrets set …` commands for every ID in `--field secret-ids`, marking the ones the user must run in their own terminal (the OAuth token), and runs nothing.
  - `secrets-access` ⚠:
    - `roles/secretmanager.secretAccessor` for the job service account on each of its secrets
    - the same role for `fugaro-build` on the git secret and on each workflow secret
  - `base` ⚠:
    - `$HEAVY sh $FUGARO_SRC/images/build-base.sh web-node $REGION-docker.pkg.dev/$PROJECT/fugaro/fugaro-web-node:dev-$(git -C $FUGARO_SRC rev-parse --short HEAD)`
    - `gcloud auth configure-docker $REGION-docker.pkg.dev --quiet`
    - `$HEAVY docker push …`
    - It prints the `BASE_IMAGE` to put in the config.
  - `image` ⚠: `cd $CHECKOUT && $FUGARO image build --workflow $WORKFLOW --json`, which is Cloud Build, billed per build minute
  - `job` ⚠: `gcloud run jobs deploy $(job) --project … --region … --image $(image):latest --service-account $(sa) --cpu $(cpu) --memory $(memory) --task-timeout $(task-timeout)s --max-retries 0 --tasks 1 --set-env-vars $(env) --set-secrets $(secrets)`
  - `teardown` ⚠ (per repository and workflow): deletes exactly the job, the job's service account, its bucket IAM binding (`remove-iam-policy-binding` with the same condition), its secrets, and `fugaro-build`'s accessor bindings on them, all named in the banner. It leaves the shared resources alone, so another onboarded repository keeps working.
  - `teardown-all` ⚠ (installation): requires `--all` as well as `--apply`, and refuses while any `fugaro-*` Cloud Run job still exists. It deletes exactly the bucket `gs://$BUCKET` with every run and cache, the repository `fugaro`, and `fugaro-build`, all named in the banner. Neither teardown disables APIs, and neither is needed if M5 takes the resources over.
- **Project guard** (every step): when a local config exists, the script reads its `project:` line and exits 1 if it differs from `PROJECT`. Every ⚠ banner names the project.

**The sandbox fixture** (`deploy/bootstrap/sandbox/`) is committed to `edgeappinc/fugarosandbox` `master` in Task 20. It is a tiny npm project:
- `package.json` has `"test": "node --test --test-reporter=junit --test-reporter-destination=junit.xml"` and no dependencies, with a matching minimal `package-lock.json`.
- `test.js` holds two passing tests.
- `fugaro.yaml`:

```yaml
version: 1
git: { provider: bitbucket, base_branch: master }   # no reviewers: the sandbox must never notify a person
agent: { auth: oauth, review_rounds: 1, max_budget_usd: 2 }
workflows:
  web:
    base: web-node
    commands: { build: "node --check test.js", test: "npm test" }
    image:
      # Proves Cloud Build passes workflow secrets (--secret id=…,env=…): the
      # build fails if SANDBOX_PROBE is missing. The value is random, not a secret.
      setup: ['test -n "$SANDBOX_PROBE"']
    secrets:
      - { name: sandbox-probe, env: SANDBOX_PROBE }
    resources: { cpu: 1, memory: 2Gi }
    timeouts: { total: 20m, stage: 10m, verify: 5m, finalize_reserve: 3m }
```

- [ ] **Step 1: Write the failing tests**

`internal/cli/gcpcmd_test.go`:

```go
package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

const jobSpecYAML = `version: 1
git: { provider: bitbucket, base_branch: main }
agent: { auth: oauth }
workflows:
  web:
    base: web-node
    commands: { build: npm run build, test: npm test }
    secrets:
      - { name: npm-token, env: NPM_TOKEN }
    timeouts: { total: 20m }
`

func jobSpecCheckout(t *testing.T, cfg string) {
	t.Helper()
	files := npmFiles()
	files["fugaro.yaml"] = cfg
	dir := checkoutWith(t, files)
	testutil.Git(t, dir, "remote", "set-url", "origin", "https://bitbucket.org/acme/app.git")
	lc := filepath.Join(t.TempDir(), "config.yaml")
	_ = os.WriteFile(lc, []byte("version: 1\nproject: proj-1234\nregion: us-east5\nruns_bucket: proj-1234-fugaro-runs\nregistry: us-east5-docker.pkg.dev/proj-1234/fugaro\n"), 0o600)
	t.Setenv("FUGARO_CONFIG", lc)
}

func TestGCPJobSpec(t *testing.T) {
	jobSpecCheckout(t, jobSpecYAML)
	out, _, err := execute(t, "gcp", "job-spec", "--workflow", "web", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var js struct {
		Job          string            `json:"job"`
		TaskTimeoutS int               `json:"task_timeout_s"`
		Env          map[string]string `json:"env"`
		Secrets      map[string]string `json:"secrets"`
	}
	if err := json.Unmarshal([]byte(out), &js); err != nil {
		t.Fatal(err)
	}
	if js.Job != "fugaro-acme-app-web" || js.TaskTimeoutS != 20*60+120 || js.Env["FUGARO_BACKEND"] != "cloud-run" || js.Env["FUGARO_PROJECT"] != "proj-1234" || js.Env["FUGARO_REGION"] != "us-east5" {
		t.Fatalf("spec = %+v", js)
	}
	want := map[string]string{"FUGARO_BITBUCKET_TOKEN": "fugaro-acme-app-bitbucket-token", "CLAUDE_CODE_OAUTH_TOKEN": "fugaro-acme-app-claude-oauth-token", "NPM_TOKEN": "fugaro-acme-app-npm-token"}
	if len(js.Secrets) != len(want) {
		t.Fatalf("secrets = %v", js.Secrets)
	}
	for k, v := range want {
		if js.Secrets[k] != v {
			t.Fatalf("secrets = %v", js.Secrets)
		}
	}
	secrets, _, _ := execute(t, "gcp", "job-spec", "--workflow", "web", "--field", "secrets")
	if strings.TrimSpace(secrets) != "CLAUDE_CODE_OAUTH_TOKEN=fugaro-acme-app-claude-oauth-token:latest,FUGARO_BITBUCKET_TOKEN=fugaro-acme-app-bitbucket-token:latest,NPM_TOKEN=fugaro-acme-app-npm-token:latest" {
		t.Fatalf("--field secrets = %q", secrets)
	}
	cond, _, _ := execute(t, "gcp", "job-spec", "--workflow", "web", "--field", "bucket-condition")
	wantCond := `resource.name.startsWith("projects/_/buckets/proj-1234-fugaro-runs/objects/runs/acme-app/") || ` +
		`resource.name.startsWith("projects/_/buckets/proj-1234-fugaro-runs/objects/cache/acme-app/") || ` +
		`resource.name.startsWith("projects/_/buckets/proj-1234-fugaro-runs/objects/locks/acme-app/")`
	if strings.TrimSpace(cond) != wantCond {
		t.Fatalf("bucket-condition =\n%s\nwant\n%s", cond, wantCond)
	}
	help, _, _ := execute(t, "--help")
	if strings.Contains(help, "gcp") {
		t.Fatal("gcp is listed in fugaro --help")
	}
}

func TestGCPJobSpecRefuses(t *testing.T) {
	for name, cfg := range map[string]string{
		"github":   strings.Replace(jobSpecYAML, "bitbucket", "github", 1),
		"cpu 3":    strings.Replace(jobSpecYAML, "    timeouts:", "    resources: { cpu: 3, memory: 4Gi }\n    timeouts:", 1),
	} {
		t.Run(name, func(t *testing.T) {
			jobSpecCheckout(t, cfg)
			if _, _, err := execute(t, "gcp", "job-spec", "--workflow", "web", "--json"); ExitCode(err) != ExitUserError {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
```

`deploy/bootstrap/bootstrap_test.go`, in package `bootstrap_test`:

```go
package bootstrap_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// fakeBin puts gcloud and docker stand-ins on PATH that fail the test if
// the dry run ever calls them.
func fakeBin(t *testing.T) string {
	dir := t.TempDir()
	for _, name := range []string{"gcloud", "docker"} {
		script := "#!/bin/sh\necho \"$0 was called in a dry run: $*\" >&2\nexit 99\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestDryRunNamesMatchTheContract(t *testing.T) {
	testutil.IsolateGit(t)
	fugaro := testutil.BuildFugaro(t)
	checkout := sandboxCheckout(t) // a git checkout of ./sandbox with a Bitbucket https origin
	env := append(os.Environ(),
		"PATH="+fakeBin(t)+":"+filepath.Dir(fugaro)+":"+os.Getenv("PATH"),
		"PROJECT=proj-1234", "REGION=us-east5", "BUCKET=proj-1234-fugaro-runs", "REPO=acme/sandbox",
		"WORKFLOW=web", "CHECKOUT="+checkout, "FUGARO="+fugaro, "FUGARO_CONFIG="+writeLocalConfig(t),
		"HEAVY=/bin/echo", "FUGARO_SRC="+testutil.ModuleRoot())
	for _, step := range []string{"apis", "bucket", "registry", "build-sa", "job-sa", "secrets", "secrets-access", "base", "image", "job", "teardown", "teardown-all"} {
		args := []string{"gcp-m4.sh", step}
		if step == "teardown-all" {
			args = append(args, "--all")
		}
		cmd := exec.Command("bash", args...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v\n%s", step, err, out)
		}
		s := string(out)
		if step != "secrets" && !strings.Contains(s, "⚠ CONFIRM") {
			t.Errorf("%s creates or deletes resources without a ⚠ CONFIRM banner", step)
		}
		if step == "job" {
			for _, want := range []string{gcp.JobName("acme-sandbox", "web"), "--max-retries 0", "--task-timeout 1320s", "FUGARO_BACKEND=cloud-run",
				"CLAUDE_CODE_OAUTH_TOKEN=" + gcp.SecretID("acme-sandbox", "claude-oauth-token") + ":latest"} {
				if !strings.Contains(s, want) {
					t.Errorf("job step lacks %q:\n%s", want, s)
				}
			}
		}
		if step == "job-sa" && !strings.Contains(s, gcp.ServiceAccountID("acme-sandbox", "web")) {
			t.Errorf("job-sa step lacks the service account ID:\n%s", s)
		}
	}
}

func TestScriptGuards(t *testing.T) {
	testutil.IsolateGit(t)
	cfg := writeLocalConfig(t) // project: proj-1234
	base := append(os.Environ(), "PATH="+fakeBin(t)+":"+os.Getenv("PATH"), "FUGARO_CONFIG="+cfg, "REGION=us-east5", "BUCKET=b")
	cmd := exec.Command("bash", "gcp-m4.sh", "apis")
	cmd.Env = append(base, "PROJECT=someone-elses-project")
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "refusing") {
		t.Fatalf("project mismatch: %v\n%s", err, out)
	}
	cmd = exec.Command("bash", "gcp-m4.sh", "teardown-all")
	cmd.Env = append(base, "PROJECT=proj-1234")
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "--all") {
		t.Fatalf("teardown-all without --all: %v\n%s", err, out)
	}
	cmd = exec.Command("bash", "gcp-m4.sh", "teardown")
	cmd.Env = append(base, "PROJECT=proj-1234", "REPO=acme/sandbox", "WORKFLOW=web", "CHECKOUT="+sandboxCheckout(t), "FUGARO="+testutil.BuildFugaro(t))
	out, err := cmd.CombinedOutput()
	if err != nil || strings.Contains(string(out), "storage rm") || strings.Contains(string(out), "repositories delete") {
		t.Fatalf("per-repo teardown touches shared resources: %v\n%s", err, out)
	}
}

func TestScriptRefusesUnknownStep(t *testing.T) {
	cmd := exec.Command("bash", "gcp-m4.sh", "everything")
	if out, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(out), "usage") {
		t.Fatalf("unknown step: %v\n%s", err, out)
	}
}

func TestScriptShellcheck(t *testing.T) {
	if _, err := exec.LookPath("shellcheck"); err != nil {
		t.Skip("shellcheck not installed")
	}
	if out, err := exec.Command("shellcheck", "gcp-m4.sh").CombinedOutput(); err != nil {
		t.Fatalf("shellcheck:\n%s", out)
	}
}
```

Write `sandboxCheckout(t)` and `writeLocalConfig(t)` in the same file.
- `sandboxCheckout(t)` copies `sandbox/` into a temp directory, runs `git init` and a commit, and adds the remote `https://bitbucket.org/acme/sandbox.git`.
- `writeLocalConfig(t)` writes a minimal local config with `project: proj-1234`, `region: us-east5`, `runs_bucket: proj-1234-fugaro-runs`, `registry: us-east5-docker.pkg.dev/proj-1234/fugaro` and `build.service_account`.

The `image` step's dry run prints the `fugaro image build` command; it doesn't run it.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/cli/ -run GCPJobSpec && go test ./deploy/bootstrap/`
Expected: FAIL (the command and the script don't exist yet)

- [ ] **Step 3: Implement** `gcpcmd.go`, following the rules above, and the script:

```bash
#!/usr/bin/env bash
# gcp-m4.sh — THROWAWAY bootstrap for Fugaro M4 live testing. M5's Terraform
# module (design §8) replaces every resource it creates. Dry run by default:
# each command is printed with a leading "+". --apply runs one step; the
# controller passes it only after the user confirms that step's ⚠ CONFIRM.
set -euo pipefail

APPLY=0
if [ "${1:-}" = "--apply" ]; then APPLY=1; shift; fi
STEP=${1:-}
FUGARO=${FUGARO:-fugaro}
HEAVY=${HEAVY:-/Users/dimi/git.lattica/Fugaro/.superpowers/heavy.sh}

run() {
  printf '+ %s\n' "$*"
  if [ "$APPLY" = 1 ]; then "$@"; fi
}
confirm() { printf '⚠ CONFIRM (project %s): %s\n' "${PROJECT:-unset}" "$*"; }
need() { for v in "$@"; do [ -n "${!v:-}" ] || { echo "gcp-m4.sh: set $v" >&2; exit 1; }; done; }
# spec FIELD: one value of `fugaro gcp job-spec` for REPO/WORKFLOW, read-only.
spec() { (cd "$CHECKOUT" && "$FUGARO" gcp job-spec --repo "$REPO" --workflow "$WORKFLOW" --field "$1"); }
build_sa() { echo "fugaro-build@$PROJECT.iam.gserviceaccount.com"; }

# The project guard: never act on a project other than the local config's.
cfgfile=${FUGARO_CONFIG:-$HOME/.config/fugaro/config.yaml}
if [ -n "${PROJECT:-}" ] && [ -f "$cfgfile" ]; then
  cfgproject=$(sed -n 's/^project: *//p' "$cfgfile")
  if [ -n "$cfgproject" ] && [ "$cfgproject" != "$PROJECT" ]; then
    echo "gcp-m4.sh: PROJECT=$PROJECT but $cfgfile says $cfgproject; refusing" >&2
    exit 1
  fi
fi
ALL=0
if [ "${2:-}" = "--all" ]; then ALL=1; fi

case "$STEP" in
  apis)
    need PROJECT
    confirm "enables the Run, Storage, Secret Manager, Artifact Registry, Cloud Build, Logging and IAM APIs in $PROJECT (free to enable, billable once used)"
    run gcloud services enable run.googleapis.com storage.googleapis.com secretmanager.googleapis.com \
      artifactregistry.googleapis.com cloudbuild.googleapis.com logging.googleapis.com iam.googleapis.com --project "$PROJECT"
    ;;
  bucket)
    need PROJECT REGION BUCKET
    confirm "creates bucket gs://$BUCKET in $REGION with lifecycle rules (storage billed per GB-month)"
    run gcloud storage buckets create "gs://$BUCKET" --project "$PROJECT" --location "$REGION" \
      --uniform-bucket-level-access --public-access-prevention
    lifecycle=$(mktemp)
    cat > "$lifecycle" <<'JSON'
{"rule": [
  {"action": {"type": "Delete"}, "condition": {"age": 90, "matchesPrefix": ["runs/"]}},
  {"action": {"type": "Delete"}, "condition": {"daysSinceCustomTime": 30, "matchesPrefix": ["cache/"]}},
  {"action": {"type": "Delete"}, "condition": {"age": 180, "matchesPrefix": ["cache/"]}}
]}
JSON
    run gcloud storage buckets update "gs://$BUCKET" --lifecycle-file="$lifecycle"
    rm -f "$lifecycle"
    ;;
  registry)
    need PROJECT REGION
    confirm "creates Artifact Registry repository fugaro in $REGION (storage billed per GB-month)"
    run gcloud artifacts repositories create fugaro --repository-format docker --location "$REGION" --project "$PROJECT"
    ;;
  build-sa)
    need PROJECT REGION
    confirm "creates service account fugaro-build and grants it artifactregistry.writer on fugaro and logging.logWriter on $PROJECT"
    run gcloud iam service-accounts create fugaro-build --project "$PROJECT" --display-name "Fugaro image builds (M4 bootstrap)"
    run gcloud artifacts repositories add-iam-policy-binding fugaro --location "$REGION" --project "$PROJECT" \
      --member "serviceAccount:$(build_sa)" --role roles/artifactregistry.writer
    run gcloud projects add-iam-policy-binding "$PROJECT" --member "serviceAccount:$(build_sa)" --role roles/logging.logWriter --condition None
    ;;
  config)
    need PROJECT REGION BUCKET REPOS
    path=${FUGARO_CONFIG:-$HOME/.config/fugaro/config.yaml}
    body=$(mktemp)
    {
      echo "version: 1"
      echo "project: $PROJECT"
      echo "region: $REGION"
      echo "runs_bucket: $BUCKET"
      echo "registry: $REGION-docker.pkg.dev/$PROJECT/fugaro"
      [ -z "${BASE_IMAGE:-}" ] || echo "base_image: $BASE_IMAGE"
      echo "build: { service_account: $(build_sa) }"
      echo "user: $(git config user.email)"
      echo "repos:"
      for r in $REPOS; do
        IFS=: read -r name branch wf <<<"$r"
        echo "  $name: { base_branch: $branch, workflows: [$wf] }"
      done
    } > "$body"
    cat "$body"
    if [ -e "$path" ] && [ "${FORCE:-}" != 1 ]; then
      echo "gcp-m4.sh: $path exists; rerun with FORCE=1 to replace it" >&2
      diff -u "$path" "$body" || true
      rm -f "$body"; exit 1
    fi
    if [ "$APPLY" = 1 ]; then mkdir -p "$(dirname "$path")"; install -m 0600 "$body" "$path"; fi
    printf '+ write %s\n' "$path"
    rm -f "$body"
    ;;
  job-sa)
    need PROJECT BUCKET REPO WORKFLOW CHECKOUT
    confirm "creates service account $(spec sa-id) and grants it storage.objectUser on gs://$BUCKET, limited to its runs/, cache/ and locks/ prefixes"
    run gcloud iam service-accounts create "$(spec sa-id)" --project "$PROJECT" --display-name "Fugaro job $REPO $WORKFLOW (M4 bootstrap)"
    run gcloud storage buckets add-iam-policy-binding "gs://$BUCKET" --member "serviceAccount:$(spec sa)" \
      --role roles/storage.objectUser --condition "expression=$(spec bucket-condition),title=fugaro-$(spec sa-id)"
    ;;
  secrets)
    need REPO WORKFLOW CHECKOUT
    echo "Create each secret with fugaro secrets set; the value comes from stdin or a hidden prompt, never argv:"
    spec secret-names | while IFS='=' read -r name id; do
      if [ "$name" = claude-oauth-token ]; then
        echo "  (you, in your own terminal) claude setup-token, then: $FUGARO secrets set $name --repo $REPO   # $id"
      else
        echo "  $FUGARO secrets set $name --repo $REPO < <file holding the value>   # $id"
      fi
    done
    ;;
  secrets-access)
    need PROJECT REPO WORKFLOW CHECKOUT
    confirm "grants secretmanager.secretAccessor on this workflow's secrets to $(spec sa), and on its git and workflow secrets to $(build_sa)"
    for id in $(spec secret-ids); do
      run gcloud secrets add-iam-policy-binding "$id" --project "$PROJECT" --member "serviceAccount:$(spec sa)" --role roles/secretmanager.secretAccessor
    done
    for id in $(spec build-secret-ids); do
      run gcloud secrets add-iam-policy-binding "$id" --project "$PROJECT" --member "serviceAccount:$(build_sa)" --role roles/secretmanager.secretAccessor
    done
    ;;
  base)
    need PROJECT REGION FUGARO_SRC
    tag="$REGION-docker.pkg.dev/$PROJECT/fugaro/fugaro-web-node:dev-$(git -C "$FUGARO_SRC" rev-parse --short HEAD)"
    confirm "builds the web-node base from $FUGARO_SRC with local Docker and pushes $tag (about 1.5 GB of registry storage)"
    run "$HEAVY" sh "$FUGARO_SRC/images/build-base.sh" web-node "$tag"
    run gcloud auth configure-docker "$REGION-docker.pkg.dev" --quiet
    run "$HEAVY" docker push "$tag"
    echo "set base_image: $tag in the local config (BASE_IMAGE=$tag gcp-m4.sh config with FORCE=1)"
    ;;
  image)
    need REPO WORKFLOW CHECKOUT
    confirm "submits a Cloud Build for $REPO/$WORKFLOW (billed per build-minute, E2_HIGHCPU_8)"
    ( cd "$CHECKOUT" && run "$FUGARO" image build --repo "$REPO" --workflow "$WORKFLOW" --json )
    ;;
  job)
    need PROJECT REGION REPO WORKFLOW CHECKOUT
    confirm "creates or updates Cloud Run job $(spec job) (billed per execution second)"
    run gcloud run jobs deploy "$(spec job)" --project "$PROJECT" --region "$REGION" \
      --image "$(spec image):latest" --service-account "$(spec sa)" \
      --cpu "$(spec cpu)" --memory "$(spec memory)" --task-timeout "$(spec task-timeout)s" \
      --max-retries 0 --tasks 1 --set-env-vars "$(spec env)" --set-secrets "$(spec secrets)"
    ;;
  teardown)
    need PROJECT REGION BUCKET REPO WORKFLOW CHECKOUT
    confirm "in $PROJECT, DELETES Cloud Run job $(spec job), service account $(spec sa), its bucket binding on gs://$BUCKET, and secrets: $(spec secret-ids | tr '\n' ' ')(shared resources are kept)"
    run gcloud run jobs delete "$(spec job)" --project "$PROJECT" --region "$REGION" --quiet
    run gcloud storage buckets remove-iam-policy-binding "gs://$BUCKET" --member "serviceAccount:$(spec sa)" \
      --role roles/storage.objectUser --condition "expression=$(spec bucket-condition),title=fugaro-$(spec sa-id)"
    for id in $(spec secret-ids); do run gcloud secrets delete "$id" --project "$PROJECT" --quiet; done
    run gcloud iam service-accounts delete "$(spec sa)" --project "$PROJECT" --quiet
    ;;
  teardown-all)
    need PROJECT REGION BUCKET
    [ "$ALL" = 1 ] || { echo "gcp-m4.sh: teardown-all deletes shared resources; pass --all" >&2; exit 1; }
    if [ "$APPLY" = 1 ] && [ -n "$(gcloud run jobs list --project "$PROJECT" --region "$REGION" --filter 'metadata.name~^fugaro-' --format 'value(metadata.name)')" ]; then
      echo "gcp-m4.sh: fugaro-* jobs still exist; run teardown for each repository first" >&2
      exit 1
    fi
    confirm "in $PROJECT, DELETES bucket gs://$BUCKET with every run and cache, Artifact Registry repository fugaro with every image, and service account $(build_sa)"
    run gcloud storage rm --recursive "gs://$BUCKET"
    run gcloud artifacts repositories delete fugaro --location "$REGION" --project "$PROJECT" --quiet
    run gcloud iam service-accounts delete "$(build_sa)" --project "$PROJECT" --quiet
    ;;
  *)
    echo "usage: gcp-m4.sh [--apply] apis|bucket|registry|build-sa|config|job-sa|secrets|secrets-access|base|image|job|teardown|teardown-all --all" >&2
    exit 2
    ;;
esac
```

In `TestDryRunNamesMatchTheContract`, also assert that the `secrets` step's output names both `claude-oauth-token` and `bitbucket-token`. `deploy/bootstrap/README.md` says in its first line that this is throwaway and that M5's Terraform replaces it, and links to `docs/gcp-bootstrap.md` (Task 19).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/cli/ ./deploy/bootstrap/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add deploy/bootstrap internal/cli/gcpcmd*.go internal/cli/root.go
git commit -m "deploy: add the throwaway M4 GCP bootstrap and the sandbox fixture"
```

---

### Task 18: Live tests (`live` tag): backend checks and the sandbox end-to-end

**Files:**
- Create: `internal/backend/gcp/live_test.go` (`//go:build live`)
- Create: `internal/e2e/live_gcp_test.go` (`//go:build live`)

**Interfaces:**
- Consumes: the real `edge-devel-dimi` resources from Task 20's bootstrap steps; `FUGARO_CONFIG` (the real local config); `FUGARO_BITBUCKET_TOKEN` (the sandbox token), for cleanup only
- Produces: live evidence for the M4 checklist. It produces no fixtures: the fakes are specified from the API documentation and these runs, not from recordings.

**The rules both files enforce:**
- The constants are `liveProject = "edge-devel-dimi"`, `liveRegion = "us-east5"` and `liveRepo = "edgeappinc/fugarosandbox"`.
- The local config's project, region and bucket must match, and the bucket must start with `fugaro-runs-`. Otherwise the test calls `t.Fatal` before any call.
- Every object, secret or PR a test creates is removed in `t.Cleanup`. A `TestLiveGCPCleanup` sweeps everything named `fugaro-live-*` and the `runs/edgeappinc-fugarosandbox/*` runs whose task carries `batch: live-*`, for after a `-timeout` abort.
- Nothing is logged that could hold a secret. The Bitbucket token is read from the environment and never printed.

**`internal/backend/gcp/live_test.go`:**
- `TestLiveListAndLogs` lists executions (read-only). It logs the raw name form the API returned (project ID or number: I-9, recorded in the M4 PR) and checks that `backend.ParseExecution` parses it and that the backend's returned name is canonical. When there is an execution, it reads its first 10 log entries and **requires at least one**, which proves the `labels."run.googleapis.com/execution_name"` filter works on real Cloud Run.
- `TestLiveJobSADeniedOutsideItsPrefixes` (I-5) runs only when `FUGARO_LIVE_JOB_SA` names the sandbox job's service account and the user has granted themselves `roles/iam.serviceAccountTokenCreator` on it (Task 20, ⚠ CONFIRM). It builds a storage client that impersonates that account (`google.golang.org/api/impersonate`, `CredentialsTokenSource` with `TargetPrincipal`), then:
  - writes and deletes `runs/edgeappinc-fugarosandbox/fugaro-live-<stamp>/probe`: allowed
  - writes `runs/edgeappinc-fugarosandbox-x/fugaro-live-<stamp>/probe` (a slug that shares the prefix without the trailing slash) and `runs/other/…`: both 403
  - reads `task.json` of any run outside its prefix: 403
  - lists `runs/`: 403
  - Anything allowed that should be denied fails the test, and its `t.Cleanup` removes whatever the probe managed to write, with the user's own credentials.
- `TestLiveSecretsRoundTrip`: `Set` of `fugaro-live-<stamp>` with a random value, `List` by label shows it, then `Delete`.
- `TestLiveLockOnGCS`: `lock.Acquire`, `BusyError` for a second holder, the expired takeover, then `Release`, on `locks/fugaro-live-<stamp>/…` in the real bucket. This is the generation-precondition path the hermetic fake imitates, and it proves the imitation is faithful.
- `TestLiveCacheOnGCS`: `Save` and then `Restore` of a 1 MB archive under `cache/fugaro-live-<stamp>/web/…`, asserting that `customTime` is set, then `Delete`.

**`internal/e2e/live_gcp_test.go`** runs the built CLI with the real config:
1. `TestLiveSandboxRun`:
   - It launches `fugaro run --repo edgeappinc/fugarosandbox --run-id <new> --batch live-<stamp> --json` with the task: "Add a test to test.js checking that 2 + 2 is 4. Commit it, run fugaro verify test, and write pr.md."
   - It polls `ls --batch live-<stamp> --json` every 20s for up to 30m.
   - It asserts:
     - the status is `succeeded` or `failed`, with a PR URL
     - `cost.model_basis` is `subscription`, `compute_usd` is above 0, and `cost.total_usd` is `compute_usd` rounded to the cent (`math.Abs(total - math.Round(compute*100)/100) < 1e-9`). A sandbox run's compute can be under a cent, so the total may be 0.00 (I-6)
     - `totals.total_usd` is within 0.01 of the sum of the listed rows' `cost.total_usd`
     - `logs` holds `stage started` and an `"event":"tool"` entry
     - `diagnose --json` gives the same PR URL
     - `result.json` has `stage: writeback`, the lock object is gone, and a `cache/edgeappinc-fugarosandbox/web/` archive exists after the first run. The npm default cache is keyed by `package-lock.json`.
2. **Cleanup:** it declines each PR it opened and deletes its branch through the Bitbucket API, using `FUGARO_BITBUCKET_TOKEN`, and deletes the `runs/…/<id>/` objects.

The live cancel check is a manual runbook step (Task 20 step 12): the hermetic `TestCloudCancel` covers the logic, and a live one would hold a 15-minute sleep on the subscription.

The Cloud Build `--secret id=…,env=…` path is exercised by Task 20's first sandbox image build, whose `image.setup` step fails without `SANDBOX_PROBE`.

Run them, only after Task 20's bootstrap steps are applied and confirmed:

```bash
FUGARO_BITBUCKET_TOKEN="$(cat ~/.config/fugaro-bb-token)" FUGARO_LIVE_JOB_SA=<sandbox job SA email, from gcp job-spec --field sa> \
  go test -tags live -timeout 45m -run 'TestLive' -v ./internal/backend/gcp/ ./internal/e2e/
```

- [ ] **Step 1: Write both files** as specified. Check that they compile without running anything: `go vet -tags live ./internal/backend/gcp/ ./internal/e2e/` must print nothing.
- [ ] **Step 2: Check that plain `go test ./...` is unaffected:** `go test ./...` must PASS without credentials.
- [ ] **Step 3: Commit**

```bash
git add internal/backend/gcp/live_test.go internal/e2e/live_gcp_test.go
git commit -m "test: add live GCP backend checks and the sandbox end-to-end behind the live tag"
```

---

### Task 19: Design and docs edits M4 requires

**Files:**
- Modify: `docs/design/v1.md`
- Modify: `docs/git-providers.md`
- Create: `docs/gcp-bootstrap.md`
- Modify: `README.md`, only if it lists commands

**The `docs/design/v1.md` edits.** Each one records a ruling this plan made.

- **§3.3 bucket layout:**
  - Add `launch.json`, `launching` (the launch claim) and `cancel` to the run prefix.
  - The lock key is `sha256(branch)[:16]` in hex.
  - The lifecycle rule for `cache/` uses custom time, which the runner sets on write and on every restore: "not read in 30 days".
- **§3.4 Backend seam:** replace the interface with Task 1's:
  - `Launch(ctx, LaunchSpec)`
  - `Execution(ctx, name)`
  - `List(ctx, ListFilter)`
  - `Logs(ctx, LogQuery, func(LogEntry) error)`
  - `Cancel(ctx, name)`
  - Say that resource limits and price tables live in the backend package, not the seam. `internal/backend/gcp` holds `CheckResources` and `ListPrices`.
  - Say that `internal/blobx` is the one place with GCS-specific precondition code, and that it has a non-atomic local fallback.
  - Execution identity: the canonical name is the full resource name built by the runner from `CLOUD_RUN_EXECUTION`, `CLOUD_RUN_JOB`, `FUGARO_PROJECT` and `FUGARO_REGION`; everything compares by (region, job, short name) through `backend.ParseExecution`, never by string.
  - `fugaro run` needs `run.jobs.runWithOverrides` (M5 IAM).
- **§4.1:**
  - Bootstrap order: read task → first record with create-if-absent (a duplicate execution stops here, having written nothing) → sync code → load `fugaro.yaml` → **lock** (it needs `timeouts.total` for the expiry) → **restore caches** (keys come from the checked-out ref) → fetch base → token.
  - The lock expiry is `started_at + total + 3m`.
  - Duplicate executions exit without writing; the lock-held-by-the-same-run check is defensive only.
  - `deadline` in `result.json`, and a cancelled run skips cache write-back.
  - Writeback recomputes keys from the final tree, runs until `total + 90s`, then releases the lock.
  - A repository Dockerfile doesn't set `FUGARO_BASE_IMAGE`, so its caches aren't keyed on the base.
- **§4.5 cancel:** the grace is measured against the recorded stage: a run in `finalize` or `writeback` is never hard-cancelled, "finalized" means the PR exists, and the hard-cancel message says the run may not have a draft PR.
- **§4.6:** add `execution`, `deadline` and `cost`.
  - With `model_basis: subscription`, `total_usd` excludes the notional model figure.
  - Reference `schemas/result.schema.json`.
- **§4.7:**
  - The launch protocol: create-if-absent claim that is never deleted after a launch, re-read of `launch.json` whenever the claim is held or has just been won, generation-matched takeover of a claim older than 10 minutes, and release of our own claim only after a failed launch.
  - `--retry` backfills `launch.json` from `result.json`'s `execution`.
  - A second `run --run-id` with a different task is an error.
  - `--retry` refuses a cancelled run.
  - `file://` buckets are single-user.
- **§5.1:**
  - `resources.cpu` is any positive integer in the core schema, and Cloud Run checks its own set (`fugaro validate` reports both).
  - The reserved logical secret names.
- **§5.3:** the `batch` pattern.
- **§5.4:** the full local config schema from Task 2, including `bucket_url`, `endpoints`, `build` and `repos`. Price overrides wait for M5. `fugaro init` writes it in M5; in M4 it is `deploy/bootstrap/gcp-m4.sh config`.
- **§6.1:**
  - The job service account's bucket IAM is `roles/storage.objectUser` with the exact three-prefix condition of Task 17, trailing slashes included, which also denies listing.
  - Secret labels are `fugaro` and `fugaro_repo`.
  - The build service account is `fugaro-build`.
  - Secrets are written only through `fugaro secrets set`, from stdin, and never read back.
- **§7.2 Cloud Build:**
  - The render and build steps share one pulled base.
  - `_GIT_SECRET` is the provider token (the job's own secret) with `_GIT_USER`, not a credential line.
  - `_SECRET_ENVS` carries the workflow secrets.
  - The build service account and the machine type come from the local config.
  - GitHub repositories need M5.
- **§9.1 commands:**
  - `run` resolves the workflow and ref; `--pr` exists but exits 1 pointing to M6. There is no per-run timeout flag in M4 (M5).
  - The `ls` defaults are `--since 7d`, `--all` means every repository in the bucket, `--watch` prints NDJSON with `--json`, and the totals line is shown.
  - `logs --json` is NDJSON.
  - `diagnose` fields.
  - `cancel --grace/--now`: "finalized" as soon as `result.json` is final or at `writeback`; never a hard cancel during `finalize` or `writeback`.
  - `image build` without `--local` (`--base`, `--no-wait`).
  - New rows for `secrets set` and `secrets ls`.
- **§10:** the agent-event relay (`stream: agent`, the `event` field) and its clipping.
- **§10.1:**
  - The Cloud Run tier table, with the date checked.
  - An unknown region gets tier 2.
  - Everything uses list prices in M4: the runner's elapsed-time estimate, and `ls`/`diagnose` from the execution's billed duration. Local overrides (committed-use discounts) arrive with M5's `fugaro init`.
  - Totals are compared and shown to the cent.
  - The report-line formats.
- **§14 M4:**
  - Mark it delivered, with a pointer to this plan.
  - Note the throwaway bootstrap, which M5 replaces.
- **§15:** a "Resolved in M4" block covering Cloud Run's resource rules, cost basis semantics, and duplicate-execution safety.

**`docs/git-providers.md`:** add "Finding a Bitbucket reviewer's UUID". It uses the repository access token without putting it in argv (`curl --config` reads the header from a process substitution, and `printf` is a shell builtin):

```bash
curl -sS --config <(printf 'header = "Authorization: Bearer %s"\n' "$(cat ~/.config/fugaro-<repo>-token)") \
  'https://api.bitbucket.org/2.0/repositories/<workspace>/<repo>/pullrequests?state=MERGED&pagelen=30&fields=values.participants.user.uuid,values.participants.user.display_name' \
  | python3 -m json.tool
```

Pick the reviewer's `{…}` UUID by display name, and put it in the repository's own `git.pr.reviewers`. Never put it in Fugaro, its tests or its fixtures. The `fugaro:onboard` skill already refers to reviewers, so add one line in `plugin/skills/onboard/SKILL.md` pointing to this section.

**`docs/gcp-bootstrap.md`:** the M4 runbook, which is Task 20's steps in prose.
- It opens by saying the bootstrap is throwaway, and that M5 imports or recreates the resources with Terraform.
- For each step: the script command, what it creates, whether it is billable, and how to undo it.
- The secrets procedure: `claude setup-token` in your own terminal, then `fugaro secrets set claude-oauth-token --repo <R>` at the hidden prompt. File-held tokens go through a `<` redirect.
- The development loop: rebuild and push the base with `gcp-m4.sh base`, set `base_image`, then `fugaro image build`, then `run`.

- [ ] **Step 1: Make the edits.** Then check that every section number this plan cites (`§3.3`, `§3.4`, `§4.1`, `§4.6`, `§4.7`, `§5.1`, `§5.3`, `§5.4`, `§6.1`, `§7.2`, `§9.1`, `§10`, `§10.1`, `§14`, `§15`) still points to the right section.
- [ ] **Step 2: Run the documentation-adjacent tests:** `go test ./internal/cli/ -run TestSkillCommandsExist ./plugin/`. They check that every command a skill names exists, and `secrets set` and `run` now do.
- [ ] **Step 3: Commit**

```bash
git add docs README.md plugin/skills/onboard/SKILL.md
git commit -m "docs: record M4's backend, recovery, cost and bootstrap decisions"
```

---

### Task 20: Live bring-up runbook (controller-run, with user confirmations)

This task writes no code. The controller runs it step by step, and asks the user before every **⚠ CONFIRM** step. Each such step is a separate question: one approval never covers the next step. Record each step's outcome in the M4 PR description. Anything that fails goes back to the owning task as a bug, with a hermetic test first.

**Preconditions (read-only, no confirmation needed):**
- `gcloud auth list` shows the user's account, and `gcloud auth application-default login` has been done. The user runs both.
- `gcloud auth application-default set-quota-project edge-devel-dimi` has been run, so user ADC carries a quota project (the backend also sends `WithQuotaProject`).
- `gcloud config get project` gives `edge-devel-dimi`.
- `gcloud billing projects describe edge-devel-dimi` shows `billingEnabled: true`.
- `gcloud billing budgets list --billing-account=<account>` shows the 70 CAD alert.
- The sandbox repository access token is at `~/.config/fugaro-bb-token` (mode 600), and the EdgeWeb one at `~/.config/fugaro-edgeweb-token`.

The environment for every step: `PROJECT=edge-devel-dimi REGION=us-east5 BUCKET=fugaro-runs-edge-devel-dimi`, with `FUGARO` set to a binary built from the M4 branch.

1. **⚠ CONFIRM** `gcp-m4.sh --apply apis`. This enables seven APIs. Using them is billable, and enabling them is free.
2. **⚠ CONFIRM** `gcp-m4.sh --apply bucket`: the `gs://fugaro-runs-edge-devel-dimi` bucket in `us-east5` with its lifecycle rules. Storage costs cents.
3. **⚠ CONFIRM** `gcp-m4.sh --apply registry`: the Artifact Registry repository `fugaro`. Storage is about $0.10/GB-month.
4. **⚠ CONFIRM** `gcp-m4.sh --apply build-sa`: the `fugaro-build` service account and two role bindings.
5. `gcp-m4.sh config`. Show the file first, then write it with `--apply`. This is local only.
6. **⚠ CONFIRM** that the sandbox fixture may go to shared state: commit `deploy/bootstrap/sandbox/` to `edgeappinc/fugarosandbox` `master`. Use a direct push only if the user says so; otherwise open a PR that the user merges. It has no reviewers.
7. With `REPO=edgeappinc/fugarosandbox WORKFLOW=web CHECKOUT=<sandbox clone>`:
   - **⚠ CONFIRM** `gcp-m4.sh --apply job-sa`
   - `gcp-m4.sh secrets`, which prints the commands. Then:
     - The controller may run `fugaro secrets set bitbucket-token --repo edgeappinc/fugarosandbox < ~/.config/fugaro-bb-token`, **⚠ CONFIRM**. It creates a secret, at $0.06 per version-month.
     - The controller may run `openssl rand -hex 16 | fugaro secrets set sandbox-probe --repo edgeappinc/fugarosandbox`, **⚠ CONFIRM**. The value is random and not sensitive; it exists only to prove Cloud Build passes workflow secrets.
     - The **user** runs `claude setup-token` in their own terminal, then `fugaro secrets set claude-oauth-token --repo edgeappinc/fugarosandbox` there, and pastes at the hidden prompt. The token must never be typed or pasted into the Claude conversation.
     - The controller checks with `fugaro secrets ls --repo edgeappinc/fugarosandbox`, which lists IDs only.
   - **⚠ CONFIRM** `gcp-m4.sh --apply secrets-access`
8. **⚠ CONFIRM** `gcp-m4.sh --apply base`, which uses `heavy.sh` for the Docker build and pushes about 1.5 GB to Artifact Registry. Then put the printed `base_image` into the config.
9. **⚠ CONFIRM** `gcp-m4.sh --apply image`: the first Cloud Build, on E2_HIGHCPU_8 at about $0.016 per build-minute, so under $0.50.
   - The build succeeding is itself the check that `--secret id=SANDBOX_PROBE,env=SANDBOX_PROBE` reaches BuildKit: the sandbox's `image.setup` step fails without it.
   - Check the log for the `base …@sha256:` line, which is the render and build consistency deferral.
   - Check that the digest matches `docker buildx imagetools inspect <base_image>`.
10. **⚠ CONFIRM** `gcp-m4.sh --apply job`: the Cloud Run job, which is free until executed.
11. **⚠ CONFIRM** granting the user `roles/iam.serviceAccountTokenCreator` on the sandbox job's service account, for the prefix-denial check (I-5): `gcloud iam service-accounts add-iam-policy-binding <sa> --member user:<you> --role roles/iam.serviceAccountTokenCreator --project edge-devel-dimi`. It is removed again in step 13.
12. **⚠ CONFIRM** the live tests (Task 18). The model spend counts against the Claude subscription, and compute costs cents. `go test -tags live …` as in Task 18, with `FUGARO_LIVE_JOB_SA` set, including `TestLiveGCPCleanup` at the end.
13. **⚠ CONFIRM** removing the step-11 grant (`remove-iam-policy-binding`, same arguments).
14. **⚠ CONFIRM** the manual spot checks. Each launches one fresh sandbox run (subscription spend, cents of compute) and opens a sandbox PR. Record the results in the M4 PR:
    - `fugaro run --repo edgeappinc/fugarosandbox --batch spot-<stamp> "…trivial task…"`, then `fugaro logs -f <id>` streams agent `tool` events live.
    - `fugaro run --run-id <same id> …` prints `already-launched`.
    - `fugaro ls --mine --since 1d` shows the runs, with the totals line and the subscription notional figure.
    - The PR report comment carries the `**Cost:** ≈ $… compute (estimate); model $… notional…` line.
    - **Live cancel** (the manual form of the dropped live test): launch a second run whose task begins with a 15-minute `sleep` through Bash; once `ls` shows it `running` at stage `implement`, run `fugaro cancel <id>`. Expect `finalized`, status `cancelled`, and a draft PR.
    - **Cleanup:** decline each spot-check PR and delete its `fugaro/<id>` branch through the Bitbucket API with the sandbox token (`curl --config` reading the header from `~/.config/fugaro-bb-token`, as in `docs/git-providers.md`), then `gcloud storage rm -r gs://fugaro-runs-edge-devel-dimi/runs/edgeappinc-fugarosandbox/<id>/` for each.
15. **EdgeWeb (the first real target), approved by the user 2026-09-27:**
    1. In an EdgeWeb checkout, on a new branch, run `/fugaro:onboard`. It writes `fugaro.yaml`, iterating on `fugaro validate` and `fugaro image build --local` through `heavy.sh`, with:
       - `git: {provider: bitbucket, base_branch: master, pr: {labels: [], reviewers: ["{46e89d3a-40c4-4575-b922-d6727bd8ace6}"]}}`
       - `agent: {auth: oauth}`
       - no `resources:` override: the web-node default of 4 vCPU / 8Gi matches EdgeWeb's Bitbucket Pipelines `size: 2x` (8 GB), where its 6 GB-heap Jest runs already pass (user decision, M3). Raise it only if a live run shows memory pressure.
    2. **⚠ CONFIRM** opening the onboarding PR on `edgeappinc/edgeweb`. The user reviews and merges it: Fugaro never merges.
    3. After the merge, with `REPO=edgeappinc/edgeweb`:
       - **⚠ CONFIRM** `job-sa`
       - `fugaro secrets set bitbucket-token --repo edgeappinc/edgeweb < ~/.config/fugaro-edgeweb-token`, **⚠ CONFIRM**
       - the user sets `claude-oauth-token` for EdgeWeb at the hidden prompt
       - **⚠ CONFIRM** `secrets-access`, `image` and `job`
    4. **⚠ CONFIRM** the first real run, with a small, self-contained task the user chooses. That PR requests the default reviewer, which notifies a real person. `fugaro ls`, `logs` and `diagnose` on it, and the PR report, go into the M4 PR description.
16. **Leave everything running for M5**, which imports or recreates it. `gcp-m4.sh teardown` (one repository and workflow) and `gcp-m4.sh teardown-all --all` (the shared bucket, registry and `fugaro-build`, only once no `fugaro-*` job is left) exist, but they are run only if the user asks, **⚠ CONFIRM** each.

---

## Open questions for the user

I ruled on everything else. The rulings, with what each costs if it's wrong, follow the questions.

None. **Answered (2026-09-27):**
- The user approved Task 20 opening the `fugaro.yaml` onboarding PR on `edgeappinc/edgeweb`. The user reviews and merges it; Fugaro never merges.
- The sandbox token is `~/.config/fugaro-bb-token`.

**Scope rulings from the plan review (2026-09-27):**
- Deferred to M5: the per-execution timeout override (`LaunchSpec.Timeout`, a `run --total-timeout` flag) and the local-config price overrides.
- Made manual: the live cancel check (Task 20 step 14). The hermetic `TestCloudCancel` covers the logic.
- Kept: `ls --watch`, the GCS custom-time `Touch` and its lifecycle rule, and `cancel --now`.

**Rulings** (flip any of them cheaply):
- **The total excludes notional model cost for `oauth` runs.** `total_usd` counts only billed dollars, and `ls` shows the notional figure separately. If that's wrong, it's one line in `runstore.NewCost` plus its test.
- **The base image for live runs comes from our own Artifact Registry**, as `fugaro-web-node:dev-<sha>` built from the branch, not from ghcr.io. No base has been published to ghcr.io yet, and the runner under test must be the M4 build. If that's wrong: publish a release instead, and set `base_image` to it.
- **The lock is acquired after `fugaro.yaml` is loaded**, not at bootstrap step 2, because its expiry needs `timeouts.total`. Nothing before it changes remote state. If that's wrong, the lock would need a provisional expiry and a rewrite.
- **Cloud Build uses the job's own token secret, plus `_GIT_USER`**, instead of a second copy of the token as a credential line. GitHub repositories wait for M5. If that's wrong, add a `git-credentials` secret per repository.
- **REST clients from `google.golang.org/api`, not gRPC Cloud clients.** They can be faked with `httptest`, and the binary stays small. If that's wrong: swapping means rewriting four small adapter files, and the seam stays the same.
- **An unknown region gets tier-2 prices**, so estimates err high. If that's wrong, estimates in an unlisted tier-1 region are about 30% high until the table is updated.
- **Cloud Build runs in `us-east5`**, overridable with `build.region`. If Cloud Build's default pool isn't offered there, set `build.region: global`.

## After M4

- **M5:**
  - Terraform replaces `deploy/bootstrap/gcp-m4.sh`: the APIs, the bucket and its lifecycle, the registry, the service accounts and IAM (the same conditions), the secret bindings, and the jobs, with the same names from `gcp.JobName` and friends.
  - `fugaro init` writes the local config.
  - The nightly Cloud Build trigger passes the same substitutions `gcp.BuildRequest` builds.
  - The nightly trigger must build its request like `gcp.BuildRequest` does, adding workflow secrets to `availableSecrets` and the build step's `secretEnv`; submitting `cloudbuild.yaml` as is drops them.
  - GitHub Cloud Build needs a token-minting step.
  - IAM: `run.jobs.runWithOverrides` for whoever runs `fugaro run` (M4 relies on the owner role).
  - Deferred from M4: a per-execution timeout override, and local price overrides for committed-use discounts.
- **M6:**
  - Follow-up runs (`run --pr`) take the same branch lock, now contended, and `launchRun` unchanged.
  - The runner fetches the PR's review comments and puts them in the agent's prompt (design §4.4). A Bitbucket CLI for the agent is optional and not planned.
- **M7:** the `fugaro:status` and `fugaro:diagnose` skills call `ls --mine --since … --json` and `diagnose --json`, whose shapes Tasks 11 and 12 fix.
- **Later:** a Batch backend implements `backend.Backend`, `CheckResources` and a price table in its own package, and `computeProblems` dispatches on `workflows.<name>.compute`.

## Execution

Subagent-driven is recommended. The 20 tasks share a lot of names across packages (`backend`, `runstore`, `cloudEnv`, and the fakes), and a fresh reviewer per task catches drift in those names before the dependent waves build on them. A shipped mistake in the lock, the launch claim or secret handling is expensive to find later. Tasks 18 and 20 are controller-only: they touch real cloud resources, and the user confirms each ⚠ step.
