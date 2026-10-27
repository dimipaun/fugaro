# Secrets and credential brokering: implementation plan

This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md). It
references decisions by `D#`, findings by `F#` and open items by `O#`. Where they disagree with the
spec, the reconciliation wins.

## Status

- **Nothing is implemented. No task is approved.** Each task needs the owner's own explicit go.
  Answering a decision approves no task.
- **The owner's answers of 2026-10-27 that shape the plan** (reconciliation §5):
  - **D490:** S0 in **0.13.0**; the Bitbucket proxy (S1) in **0.14.0**; 0.12.1 gets only one
    pointer sentence in the already-approved T1.1 of docs/bitbucket-workspace-token.
  - **D491:** staged; Bitbucket first; workflow secrets through a later vault fetch (S3); builds a
    listed exception with an exit slice (S4).
  - **D492/D493:** the wildcard path ships now (dc73, T1.1, T1.2 in 0.12.1); that plan's S2 is
    re-planned against the proxy (§"The re-planned Bitbucket S2").
  - **D494 (departs from the owner's spec):** the proxy credential is the run identity plus the
    job's ID token. No RTDB token store, subscription or sweep is built.
  - **D495:** a loopback forwarder in the runner that holds no secret.
  - **D496:** repository and action rules from the start.
  - **D497:** (a) a launcher-minted branch claim now; (b) GitHub through the proxy deferred (no
    task).
  - **D498:** no new model mechanism; the other `agent.auth` modes are wired later (S2).
  - **D499:** the CI guard is a Go test with an exceptions table.
  - **D500:** flip lists per installation and per secret class, each gated on an owner-run check,
    with operator release notes; `belong` is never touched.
  - **D501/D502 (NOT the recommendation):** the service stays opt-in and **keeps scaling to zero**,
    with no probe added; the doctor read of who may deploy it is kept. Consequence: cold starts on
    the critical path of an opted-in installation, no redundancy beyond Cloud Run's own. Revisit
    before any release that makes the service a default.
  - **D503:** doctor (a) in S0, (b)–(d) in S1. **D504:** kill switch plus `fugaro cancel`.
    **D505:** per-repository jobs first; shared jobs wait for C3.9. **D506:** the token in the
    bundle.
- **Provisional:** the release split of the re-planned Bitbucket S2 follows this plan's reading of
  D493 until the owner rules on **O2**; **O1** and **O3** are open too (reconciliation §7).

## Conventions every part follows

- `CLAUDE.md` and `.claude/rules/credentials-service.md` (every task touching `internal/credsvc`,
  `internal/creds` or `SECURITY.md`): independent review before merge, one audit line per verified
  refusal, no value in a log or error, the service dormant, `ServiceHeldSecretNames` untouched by
  this plan, the service account's grants unchanged (the bundle and `fugaroCredentialsDatabase`),
  `website/content/security.md` regenerated with `go run ./internal/clidoc`.
- `.claude/rules/terraform.md` for `deploy/terraform/**` and `internal/infra/**`.
- No secret-shaped literal in a test, a fixture or a doc. No agent runs a live step, a
  `fugaro update --provision-credentials`, a `fugaro creds set` or a live check: those are
  OWNER-RUN, and the task says what the owner types.
- Docs are tested: `go test ./internal/cli/ -run Docs` after any doc change.
- Version skew: a new run-identity claim gets a `…Since` constant in `internal/cli/recipes_skew.go`
  (B1.1).
- Steps after deploy go in the release's `### For operators` (B1.13's draft, copied by the 0.14.0
  release PR, as docs/bitbucket-workspace-token's T2.11 does).
- Every task marked SECURITY-SENSITIVE: **opus + mandatory independent review**.

## Global constraints and review focus

| # | Constraint | Pinned by |
| --- | --- | --- |
| G1 | No real Bitbucket token is ever in a job of an opted-in installation once its flip is on: not in an env, a mount or the runner's memory | B1.7 (no token read), B1.13 (mount and accessor dropped), Check 192 |
| G2 | A push names only the run's branch; no ref deletion, no tag; a non-run ref is refused **before** any pack byte is forwarded | B1.3, B1.4, B1.5 |
| G3 | The repository is taken from the request path and must equal the run identity's repository and be admitted by the policy's `bitbucket_token` block | B1.4, B1.5 |
| G4 | REST is an allow-list of the runner's endpoints for that repository; `/user`, `/workspaces`, pipelines, settings and every other path are refused | B1.4 |
| G5 | The forwarder holds no credential: it attaches the job's ID token and the run identity, nothing else; it listens on loopback only | B1.6 |
| G6 | A first run's branch claim must equal `fugaro/<fr>` (F28) | B1.1 |
| G7 | One audit line per proxied request, names only (R2); no header, body or token in a line | B1.5 |
| G8 | The kill switches and the run's outcome stop the proxy exactly as they stop a model call (D504) | B1.5 |
| G9 | Without the opt-in nothing changes: no forwarder, no claim required, the job mounts its token as today (D502) | B1.7, B1.8 |
| G10 | The CI guard fails on any new secret reader not in the exceptions table | B0.2 |
| G11 | A git request never outlives its credentials (D495) | B1.6 |

**Attacker's view, for every review:** the agent can read the runner's `/proc` and the metadata
server (F5), so it holds the run identity and can mint the job's ID token. Review every rule as if
the agent sends the request itself. A launcher is trusted (D475) and can mint any identity.

## Slices

| Slice | Release | What | Inert? |
| --- | --- | --- | --- |
| **S0** | **0.13.0** | The secret-reader audit as a tested doc, the CI guard, doctor's Secret Manager sweep and deployer read, Check 190 written and run | Yes: no behaviour change except two doctor findings |
| **S1** | **0.14.0** | The Bitbucket proxy for per-repository jobs of opted-in installations, the re-planned Bitbucket S2 tasks, the flip, Checks 191 and 192 | Inert until an installation is opted in and the flip list is filled (B1.13) |
| **S2** | unscheduled | Every `agent.auth` mode through the service (D498) | — |
| **S3** | unscheduled | Workflow `secrets:` through a vault fetch (D491) | — |
| **S4** | unscheduled | The build exception's exit: builds and the image check through the service (D491, U4) | — |
| **S5** | unscheduled | Shared project jobs at the service (the credentials plan's C3.9), then Bitbucket shared jobs (D505) | — |

Considered and not planned: the spec's RTDB token store (D494), GitHub through the proxy (D497b),
an HTTP health route and minimum instances (D501), the agent sandbox (O1).

## The re-planned Bitbucket S2 (D493, provisional on O2)

docs/bitbucket-workspace-token's S2 is re-planned against the proxy. Its task text stays in that
plan; this table says what happens to each task, and the levels below include the ones that ship.

| Task there | Here |
| --- | --- |
| T2.1 (the node's block, `bitbucket_api_roots`, profile rules, policy v4) | **0.14.0, unchanged in scope** (BW-T2.1). The one version bump of D493 |
| T2.3 (publish derives the block) | **0.14.0, unchanged** (BW-T2.3) |
| T2.8 (the API-root pin) | **0.14.0, unchanged** (BW-T2.8). Under the proxy the pin keeps a launcher from pointing the runner's REST at its own host, which would leak the run identity |
| T2.9 (the runner enforces the block) | **Replaced** by the service's check (B1.4, B1.5): a runner-side check binds nothing the agent cannot bypass (F5) |
| T2.2, T2.4, T2.6, T2.7 (Bitbucket shared jobs, the own-copy warning, phase-3 docs, Check 181) | **Wait for S5** (D505). T2.4's warning is moot for proxied repositories |
| T2.11 (the lockstep release notes) | **0.14.0 release PR** (BW-T2.11), with B1.13's operator steps beside it |
| T1.1, T1.2 (0.12.1, APPROVED) | **Unchanged**, plus one pointer sentence in T1.1 (D490) |

## Tasks

### S0 (0.13.0)

#### B0.1: The secret-reader audit, as a tested doc

- **Files:** `docs/secrets-brokering/secret-readers.md` (new); `internal/cli/docs_secrets_brokering_test.go` (new).
- **Interfaces produced:** a table, one row per exception, with a stable ID (`E1`…): the reader
  (an account class), what it reads, how (mount, `availableSecrets`, API), the decision that keeps
  it (D145, D491, D506 …) and its **exit slice** (S1, S3, S4, or "never" for the credentials
  service's own grant). The rows are reconciliation §3's runtime readers. B0.2 parses this table.
- **Schema/rules/config change:** none.
- **Tests:** `TestSecretReadersTableShape` (every row has an ID, a decision and an exit slice
  named in this plan's Slices table), `TestSecretsBrokeringReadmeStatus` (the README's status line).
- **Command:** `go test ./internal/cli/ -run 'TestSecretReadersTableShape|TestSecretsBrokeringReadmeStatus' -count=1`
- **Red step:** the shape test first, against a table with a row whose exit slice is `S9`: it fails.
- **Depends on:** none. **Reviewer tier:** standard.
- **Commit:** `docs(secrets-brokering): who reads Secret Manager today, each exception with its decision and exit`

#### B0.2: The CI guard (D499)

- **Files:** `deploy/terraform/secret_readers_test.go` (new); `docs/secrets-brokering/README.md`
  (index row for `secret-readers.md`).
- **Interfaces consumed:** B0.1's table.
- **Behaviour:** the test walks `deploy/terraform/gcp/**/*.tf` for every
  `google_secret_manager_secret_iam_member` with `roles/secretmanager.secretAccessor` and every
  `secret_key_ref`; `images/**/cloudbuild.yaml` for `availableSecrets`; and every non-test `.go` file
  under `internal/` and `cmd/` for an import of the Secret Manager API package. Each hit must map to
  one table row (by module and resource name, or by package). A hit with no row fails; a row with no
  hit fails too, so the table cannot go stale.
- **Tests:** `TestEverySecretReaderIsAListedException`, `TestEveryListedExceptionStillExists`.
- **Command:** `go test ./deploy/terraform/ -run 'TestEverySecretReaderIsAListedException|TestEveryListedExceptionStillExists' -count=1`
- **Red step:** run against B0.1's table with one row deleted: the first test fails naming the
  resource.
- **Depends on:** B0.1. **Reviewer tier:** opus + mandatory independent review (it fences secret
  access).
- **Commit:** `terraform: a new Secret Manager reader fails the build unless it is a listed exception`

#### B0.3: Doctor's Secret Manager sweep and the deployer read (D503a, D501)

- **Files:** `internal/cli/doctor_secret_readers.go` (new); `internal/cli/doctor_secret_readers_test.go` (new); `internal/cli/doctor.go` (registration only).
- **Behaviour:** `secret-readers-unexpected` (warning): every `secretAccessor` member on every
  container of the installation's project that is not one of the account classes Fugaro creates
  for that container (computed forward, as `doctorProjectSecretAccessorChecks` does), or is a
  principal outside the installation (a user, a group, another project's account). Humans holding
  Owner or Editor are reported once, as info (F3). `credentials-service-deployers` (info, only when
  the installation opted in): the principals that hold `run.services.update` on the service or
  `iam.serviceAccounts.actAs` on its account, read from IAM policies (D501).
- **Tests:** `TestDoctorSecretReadersFlagsAnUnknownAccessor`, `TestDoctorSecretReadersAcceptsFugaroAccounts`,
  `TestDoctorCredentialsServiceDeployersListsPrincipals`, `TestDoctorCredentialsServiceDeployersSilentWhenNotOptedIn`
  (all on `internal/gcpfake`).
- **Command:** `go test ./internal/cli/ -run 'TestDoctorSecretReaders|TestDoctorCredentialsServiceDeployers' -count=1`
- **Red step:** the unknown-accessor test, with a fake policy carrying a stray user: no finding yet.
- **Depends on:** none. **Reviewer tier:** standard (read-only, reports).
- **Commit:** `doctor: report every Secret Manager reader Fugaro did not create, and who may deploy the credentials service`

#### B0.4: Check 190 written

- **Files:** `docs/gcp-live-checklist.md` (row and section 190); `scripts/live-checks/check190.sh` (new); `scripts/live-checks/check190_test.sh` (new); `scripts/live-checks/check190/main.go` (new: a throwaway reverse proxy to `bitbucket.org` and `api.bitbucket.org` that attaches the token from its own secret and logs request counts and sizes, never a header or a body); `docs/secrets-brokering/check190-steps.md` (new); `docs/secrets-brokering/README.md` (index row).
- **What Check 190 measures** (F13, F14, U2, U7, U8): through a throwaway Cloud Run service on
  installation `fugaro`, against a sandbox Bitbucket repository: a full clone and a blobless clone
  (request count, bytes, time), a push of a pack above 32 MiB on HTTP/1.1 (chunked) and the
  front end's answer, a push of a small pack, the cold start of the first request after idle, and
  the 3600 s timeout on a slow push. It deletes everything it created.
- **Tests:** `check190_test.sh` (the guards: the hard-coded project, a token file that is not
  `0600` is refused, `--dry-run` creates nothing); the register's docs pins.
- **Command:** `bash scripts/live-checks/check190_test.sh && go test ./internal/cli/ -run Docs -count=1`
- **Red step:** `check190_test.sh`'s project-guard case before the guard exists: it fails.
- **Depends on:** none. **Reviewer tier:** opus + mandatory independent review (it handles a real
  token at run time).
- **Commit:** `live-checks: Check 190, git fetch and push through a Cloud Run proxy, written`

#### B0.5: Check 190 — OWNER-RUN

- **What the owner types,** on their own machine, installation `fugaro` only:
  1. Write the sandbox repository's access token to a file with mode `0600`.
  2. `CHECK190_REPO=<workspace>/<sandbox repository> CHECK190_TOKEN_FILE=<that file> bash scripts/live-checks/check190.sh --dry-run`
  3. The same without `--dry-run`, then paste the `CHECK 190 RESULT` block into the register.
- **Depends on:** B0.4. No agent runs any step.
- **Gate:** S1's B1.5 and B1.7 design for HTTP/1.1 if the 32 MiB push passes; otherwise the
  container must serve h2c end to end, and B1.5 records that before it starts.

### S1 (0.14.0)

#### B1.1: The branch claim (D497a, F28, O3) — SECURITY-SENSITIVE

- **Files:** `internal/budget/token/token.go` (`Claims.Branch`, claim `fb`), `internal/budget/token/mint.go`, `internal/budget/token/token_test.go`; `internal/cli/launch_budget.go` (`mintBudgetToken` passes the task's branch), `internal/cli/launch_budget_test.go`; `internal/cli/recipes_skew.go` (`branchClaimSince`); `internal/credsvc/auth.go` (`Identity.Branch`; a first run's claim must equal `fugaro/<fr>`), `internal/credsvc/auth_test.go`.
- **Interfaces produced:** `Identity.Branch string` (empty for an identity minted without the claim).
- **Tests:** `TestMintCarriesTheBranchClaim`, `TestLaunchMintsTheTaskBranch`, `TestAuthFirstRunBranchMustBeTheRunsOwn`, `TestAuthIdentityWithoutBranchClaim`, `TestBranchClaimSkew`.
- **Command:** `go test ./internal/budget/token/ ./internal/credsvc/ -run 'Branch' -count=1 && go test ./internal/cli/ -run 'TestLaunchMintsTheTaskBranch|TestBranchClaimSkew' -count=1`
- **Red step:** `TestAuthFirstRunBranchMustBeTheRunsOwn` with an identity for run X claiming `fugaro/<Y>`: accepted before the check.
- **Depends on:** none in S1 (L1 only to keep it off BW-T2.1's fixtures). **Reviewer tier:** opus + mandatory independent review.
- **Commit:** `token, credsvc: the run identity names its branch, and a first run's must be its own`

#### B1.2: The Bitbucket token in the bundle (D506) — SECURITY-SENSITIVE

- **Files:** `internal/credsvc/bundle.go`, `internal/credsvc/bundle_test.go`; `schemas/creds-bundle.schema.json`; `internal/cli/creds.go` (`creds set bitbucket-token`, `creds ls`), `internal/cli/creds_test.go`.
- **Interfaces produced:** `Bundle.BitbucketToken()` returning the value and its name for audit, never logged.
- **Tests:** `TestBundleBitbucketTokenStrict` (unknown keys, oversize, empty), `TestCredsSetBitbucketToken` (stdin only, refused in an agent's session), `TestCredsLsNamesBitbucketToken`.
- **Command:** `go test ./internal/credsvc/ -run TestBundleBitbucketToken -count=1 && go test ./internal/cli/ -run 'TestCredsSetBitbucketToken|TestCredsLsNamesBitbucketToken' -count=1`
- **Red step:** the strict-schema test with a `bitbucket-token` key: refused as unknown today.
- **Depends on:** none in S1 (L1, as B1.1). **Reviewer tier:** opus + mandatory independent review.
- **Commit:** `credsvc, creds: the bundle holds the Bitbucket token, written by creds set`

#### B1.3: The receive-pack command parser — SECURITY-SENSITIVE

- **Files:** `internal/gitproxy/pktline.go` (new), `internal/gitproxy/pktline_test.go` (new).
- **Interfaces produced:** `ReadCommands(r io.Reader, max int) (cmds []RefUpdate, consumed []byte, err error)`: parses pkt-lines up to the flush packet, returns each `old new ref`, and the bytes read so the caller forwards them unchanged before the pack. Refuses an oversized command section, a malformed length, a missing flush, a push certificate it does not understand.
- **Tests:** table tests from git's own documented examples, `FuzzReadCommands`, `TestReadCommandsBoundedRead`.
- **Command:** `go test ./internal/gitproxy/ -run 'ReadCommands' -count=1 && go test ./internal/gitproxy/ -fuzz FuzzReadCommands -fuzztime 60s`
- **Red step:** `TestReadCommandsBoundedRead` with a 1 GiB declared section: no bound exists yet.
- **Depends on:** none. **Reviewer tier:** opus + mandatory independent review.
- **Commit:** `gitproxy: read a push's ref commands, and nothing past them, before the pack`

#### B1.4: The proxy's rules and wire contract (D496, G2–G4) — SECURITY-SENSITIVE

- **Files:** `internal/gitproxy/rules.go` (new), `internal/gitproxy/rules_test.go` (new), `internal/gitproxy/wire.go` (new: the route prefix, the forwarder's headers).
- **Interfaces consumed:** B1.3; BW-T2.1's `Policy.BitbucketToken` and `projectpolicy.MatchRepo`.
  **Produced:** `Classify(r *http.Request) (Request, error)` (git fetch, git push, or a named REST endpoint, with the repository); `Decide(req Request, id Identity, admitted Admission) Verdict`.
- **Rules:** git fetch of the identity's repository; git push only after B1.3's commands, each a
  `refs/heads/<identity branch>` update (force allowed), no zero new id, no `refs/tags/`; REST only
  the endpoints `bitbucket.Provider` calls (F7), for that repository; the repository admitted by the
  block; everything else refused with a reason name.
- **Tests:** `TestRulesFetchOtherRepositoryRefused`, `TestRulesPushOnlyRunBranch`, `TestRulesPushDeleteRefused`, `TestRulesTagRefused`, `TestRulesRESTAllowList` (one case per `bitbucket.Provider` method and per refused path), `TestRulesNotAdmittedByBlock`, `TestRulesPathTricks` (encoded separators, dot segments, case).
- **Command:** `go test ./internal/gitproxy/ -run 'TestRules' -count=1`
- **Red step:** `TestRulesPushOnlyRunBranch` with a `refs/heads/main` update: no rule yet.
- **Depends on:** B1.3, BW-T2.1. **Reviewer tier:** opus + mandatory independent review.
- **Commit:** `gitproxy: one repository and the run's branch, and only the runner's REST calls`

#### B1.5: The service's Bitbucket route (R1, R2, G7, G8) — SECURITY-SENSITIVE

- **Files:** `internal/credsvc/bitbucket.go` (new), `internal/credsvc/bitbucket_test.go` (new), `internal/credsvc/server.go` (`routeAct`, the route), `internal/credsvc/audit.go` (purposes `git` and `bitbucket-api`).
- **Behaviour:** after `Authenticator` and the kill switches, `Classify` and `Decide`; on allow,
  strip the caller's credentials, attach the bundle's token (`x-token-auth` Basic for git, Bearer
  for REST), forward to `bitbucket.org` or `api.bitbucket.org/2.0` (an owner-listed root for REST
  only, T2 ruling), stream both ways, never buffer the pack. One audit line per request.
- **Tests:** `TestBitbucketRouteForwardsFetch`, `TestBitbucketRoutePushRefusedBeforeAnyPackByte` (the fake upstream sees zero bytes), `TestBitbucketRouteStreamsLargePack`, `TestBitbucketRouteOneAuditLinePerRequest`, `TestBitbucketRouteKillSwitch`, `TestBitbucketRouteEndedRun`, `TestBitbucketRouteNoTokenInErrors`; `TestServiceHasNoAdminRoutes` stays green.
- **Command:** `go test ./internal/credsvc/ -run 'TestBitbucketRoute|TestServiceHasNoAdminRoutes' -count=1`
- **Red step:** `TestBitbucketRoutePushRefusedBeforeAnyPackByte`: the route does not exist.
- **Depends on:** B1.1, B1.2, B1.4 (and B0.5's gate). **Reviewer tier:** opus + mandatory independent review.
- **Commit:** `credsvc: the Bitbucket route, git and REST, under the run's own rules`

#### B1.6: The runner's loopback forwarder (D495, G5, G11) — SECURITY-SENSITIVE

- **Files:** `internal/gitproxy/forwarder.go` (new), `internal/gitproxy/forwarder_test.go` (new).
- **Behaviour:** listens on `127.0.0.1` only; for each request attaches a fresh job ID token
  (Cloud Run IAM) and the current run identity in its own header (B1.4's wire), forwards to the
  service, streams. Holds no Bitbucket credential and accepts none from its caller.
- **Tests:** `TestForwarderLoopbackOnly`, `TestForwarderAttachesFreshTokens`, `TestForwarderDropsCallerAuthorization`, `TestForwarderStreams`.
- **Command:** `go test ./internal/gitproxy/ -run 'TestForwarder' -count=1`
- **Red step:** `TestForwarderDropsCallerAuthorization`: no forwarder yet.
- **Depends on:** B1.4. **Reviewer tier:** opus + mandatory independent review.
- **Commit:** `gitproxy: a loopback forwarder that holds nothing and signs every request fresh`

#### B1.7: The runner on the proxy (G1, G9) — SECURITY-SENSITIVE

- **Files:** `internal/gitprov/providers/providers.go` (with `FUGARO_CREDENTIALS_SERVICE_URL` set on a Bitbucket job: no `FUGARO_BITBUCKET_TOKEN` read, the adapter's base URL is the forwarder), `internal/gitprov/providers/providers_test.go`; `internal/gitprov/bitbucket/bitbucket.go` (a proxied adapter with no token); `internal/runner/runner.go` (start the forwarder, remote rewrite with `url.<forwarder>.insteadOf`, the agent's credential helper vars carry no token), `internal/runner/project.go` (`remoteHost`/`admissionCheck` accept the forwarder for a Bitbucket run of an opted-in job); `internal/runner/gitproxy_test.go` (new).
- **Tests:** `TestProxiedBitbucketReadsNoToken`, `TestProxiedBitbucketAgentEnvHasNoToken`, `TestProxiedRemoteRewrite`, `TestNotOptedInUnchanged` (`TestBitbucketUnchanged` stays green).
- **Command:** `go test ./internal/gitprov/... -run 'Proxied' -count=1 && go test ./internal/runner/ -run 'TestProxied|TestNotOptedInUnchanged|TestBitbucketUnchanged' -count=1 -timeout 20m`
- **Red step:** `TestProxiedBitbucketReadsNoToken` with the service URL set: the token is still read.
- **Depends on:** B1.6, BW-T2.8 (same `runner.go`). **Reviewer tier:** opus + mandatory independent review.
- **Commit:** `runner: an opted-in Bitbucket run reaches Bitbucket only through the service`

#### B1.8: The Bitbucket flip list, inert (D500)

- **Files:** `internal/config/providers.go` (`ProxiedGitSecretNames = []string{}`, and `IsServiceHeldSecretEnv` reads it); `internal/infra/spec.go`, `internal/infra/spec_test.go`; `deploy/terraform/gcp/modules/workflow/sa.tf` (`proxied_git_secret_names`, mirrored); `deploy/terraform/terraform_test.go` (the mirror guard).
- **Behaviour:** with the list empty nothing changes. Filled and the installation opted in, the
  job loses its accessor on and its mount of `bitbucket-token`; the build account keeps its own
  (exception, exit S4). `ServiceHeldSecretNames` is not touched.
- **Tests:** `TestProxiedGitSecretNamesMatchTheModule`, `TestEmptyProxiedListChangesNothing`, `TestFilledProxiedListDropsMountAndAccessor` (with the list overridden in the test).
- **Command:** `go test ./internal/infra/ ./deploy/terraform/ -run 'ProxiedGitSecret|ProxiedList' -count=1`
- **Red step:** `TestProxiedGitSecretNamesMatchTheModule`: neither side exists.
- **Depends on:** none. **Reviewer tier:** opus + mandatory independent review.
- **Commit:** `config, infra, terraform: the Bitbucket token's flip list, empty`

#### B1.9: Doctor's S1 checks (D503b–d)

- **Files:** `internal/cli/doctor_brokering.go` (new), `internal/cli/doctor_brokering_test.go` (new), `internal/cli/doctor.go` (registration), `internal/localcfg/localcfg.go` (the operator's `credentials.rotation_days`, local only, never published).
- **Checks:** `credentials-service-deployed` (the service and every job account's invoker grant,
  through the Run admin API); `bundle-bitbucket-token` (`creds ls` names it on an opted-in
  installation with Bitbucket repositories); `bitbucket-token-mounted` (a repository job still
  mounting a Bitbucket token on an installation whose flip is on); `secret-rotation-age` (the latest
  version's age against `credentials.rotation_days` in the local config, warning only);
  `model-key-in-agent` (budget off, or no service, for a workflow whose `auth` carries a key).
- **Tests:** one positive and one negative per check, on `internal/gcpfake`.
- **Command:** `go test ./internal/cli/ -run 'TestDoctorBrokering' -count=1`
- **Red step:** `TestDoctorBrokeringMountedTokenOnFlippedInstallation`: no check.
- **Depends on:** B1.2, B1.8. **Reviewer tier:** standard.
- **Commit:** `doctor: the credentials service, the bundle's Bitbucket token, rotation age and model keys in reach`

#### B1.10: SECURITY.md and the docs (R3, F22, D501, D504) — SECURITY-SENSITIVE (security claims)

- **Files:** `SECURITY.md`; `website/content/security.md` (regenerated); `docs/git-providers.md` ("What the agent gets", Bitbucket); `docs/credentials-service/runbook.md` (rotating the Bitbucket token with `creds set`; a cold start and an outage of the proxy); `docs/secrets-brokering/secret-readers.md` (the Bitbucket rows' state); `internal/cli/docs_security_test.go`, `internal/cli/docs_website_security_test.go`, `internal/cli/docs_secrets_brokering_test.go`.
- **What they say:** the agent can act as the run at the proxy: one repository, one branch, the
  run's life (R3, O1); D462's reach ends for an installation whose flip is on; the build account
  still holds the token until S4, so rotation writes both copies; cold starts and no redundancy
  (D501); revocation is the kill switch and `fugaro cancel` (D504).
- **Command:** `go run ./internal/clidoc && go test ./internal/cli/ -run Docs -count=1`
- **Red step:** a pin for the R3 sentence in `docs_security_test.go`, before the text: it fails.
- **Depends on:** B1.5, B1.7. **Reviewer tier:** opus + mandatory independent review.
- **Commit:** `SECURITY, docs: what a proxied Bitbucket run can reach, and what it no longer holds`

#### B1.11: Checks 191 and 192 written

- **Files:** `docs/gcp-live-checklist.md` (rows and sections 191, 192); `scripts/live-checks/check191.sh` (new), `scripts/live-checks/check191_test.sh` (new); `docs/secrets-brokering/check191-steps.md` (new); `docs/secrets-brokering/README.md` (index row).
- **Check 191** (S1 end to end, before the flip): on installation `fugaro`, opted in, a sandbox
  Bitbucket repository: a run clones, pushes checkpoints and opens its pull request through the
  service; a hand-made request with the run's identity to another repository, to `main`, a tag and
  a delete is refused and audited; `/user` is refused; after the run ends the identity is refused.
- **Check 192** (after the flip): the job no longer mounts `bitbucket-token` and its account has no
  accessor on it; a run still succeeds; `fugaro doctor` reports `bitbucket-token-mounted` clean.
- **Command:** `bash scripts/live-checks/check191_test.sh && go test ./internal/cli/ -run Docs -count=1`
- **Red step:** `check191_test.sh`'s project-guard case before the guard exists.
- **Depends on:** B1.5, B1.7. **Reviewer tier:** standard (docs and a script that runs only for the owner).
- **Commit:** `live-checks: Checks 191 and 192, the Bitbucket proxy end to end and the flip, written`

#### B1.12: Check 191 — OWNER-RUN

- **What the owner types,** installation `fugaro` only, never `belong`:
  1. `fugaro update --provision-credentials` (only if the installation is not yet opted in; typed
     at a real terminal).
  2. `fugaro creds set bitbucket-token < <file holding the sandbox token>`
  3. `fugaro project policy publish` (BW-T2.3's block).
  4. `CHECK191_REPO=<workspace>/<sandbox repository> bash scripts/live-checks/check191.sh`, and
     paste the `CHECK 191 RESULT` block into the register.
- **Depends on:** B1.10, B1.11, BW-T2.3, and a dev build with S1.

#### B1.13: The flip (D500, G1) — SECURITY-SENSITIVE, owner-gated

- **Files:** `internal/config/providers.go` (`ProxiedGitSecretNames = []string{"bitbucket-token"}`); `deploy/terraform/gcp/modules/workflow/sa.tf` (the mirror); `deploy/terraform/terraform_test.go`; `docs/secrets-brokering/secret-readers.md` (the job row's exit reached); `docs/secrets-brokering/release-0.14.0-operators.md` (new: the `### For operators` steps — store the token with `creds set`, publish the policy, re-apply every Bitbucket repository with `fugaro update --repo`, rotate both copies while S4 is open).
- **Tests:** B1.8's filled-list test now runs on the real list; B0.2's guard (the job row gone).
- **Command:** `go test ./internal/infra/ ./deploy/terraform/ -run 'ProxiedGitSecret|ProxiedList|TestEverySecretReader' -count=1`
- **Red step:** B0.2's `TestEveryListedExceptionStillExists` with the job row still listed: fails once the list is filled, until the row is updated.
- **Depends on:** B1.12 PASS and the owner's go on the flip. **Reviewer tier:** opus + mandatory independent review.
- **Commit:** `config, terraform: an opted-in installation's Bitbucket jobs give up the token`

#### B1.14: Check 192 — OWNER-RUN

- **What the owner types:** `fugaro update --repo <workspace>/<sandbox repository>` from its
  checkout, then `CHECK192_REPO=<workspace>/<sandbox repository> bash scripts/live-checks/check191.sh --after-flip`, and paste the `CHECK 192 RESULT` block.
- **Depends on:** B1.13 on a dev build.

#### BW-T2.1, BW-T2.3, BW-T2.8, BW-T2.11

As written in [../bitbucket-workspace-token/implementation-plan.md](../bitbucket-workspace-token/implementation-plan.md),
moved to 0.14.0 (O2). Their Files, tests and commands are that plan's.

### Later slices (unscheduled; each planned in detail when the owner schedules it)

- **B2.1 (S2, D498):** wire `api-key`, `oauth` and the provider routes through the service, so an
  opted-in job of any `agent.auth` has a model credential again; Vertex stays out. Follows the
  credentials plan's open model-wiring half.
- **B3.1 (S3, D491):** a vault fetch: the runner gets the workflow's `secrets:` from the service at
  bootstrap; the job loses its accessors; a flip list and an owner-run check, as S1.
- **B4.1 (S4, D491, U4):** builds and the image check fetch their credential from the service under
  a build caller class; the build exception leaves the table.
- **B5.1 (S5, D505):** shared project jobs admitted by profile at the service (C3.9); then
  docs/bitbucket-workspace-token's T2.2, T2.4, T2.6 and T2.7 against the proxy.

## Decisions to tasks

| Decision | Tasks |
| --- | --- |
| D490 | Slices table; B0.1–B0.5 in 0.13.0; S1 in 0.14.0; T1.1's pointer (the other plan) |
| D491 | B0.1's exit column; B3.1; B4.1 |
| D492 | T1.1, T1.2 unchanged; B1.10 (D462 ends per installation) |
| D493 | The re-planned S2 table; BW-T2.1, BW-T2.3, BW-T2.8, BW-T2.11; B1.4 replaces T2.9 |
| D494 | B1.1, B1.5, B1.6 (no token store task) |
| D495 | B1.6, B1.7 |
| D496 | B1.3, B1.4, B1.5 |
| D497 | (a) B1.1; (b) none, deferred |
| D498 | B2.1; B1.9's `model-key-in-agent` |
| D499 | B0.2 |
| D500 | B1.8, B1.13, B1.14 |
| D501/D502 | No min instances or probe in any task; B0.3's deployer read; B1.10 states the consequence; B1.7 (opt-in only) |
| D503 | B0.3 (a); B1.9 (b)–(d) |
| D504 | B1.5 (G8); B1.10 |
| D505 | B1.7 (per-repository only); B5.1 |
| D506 | B1.2; B1.12 step 2 |

## Parallel execution

**Edges** (from the "Depends on" lines): B0.1→B0.2; B0.4→B0.5. BW-T2.1→{BW-T2.3, BW-T2.8, B1.4};
B1.3→B1.4; {B1.1, B1.2, B1.4}→B1.5; B1.4→B1.6; {B1.2, B1.8}→B1.9; {B1.6, BW-T2.8}→B1.7;
{B1.5, B1.7}→{B1.10, B1.11}; {B1.10, B1.11, BW-T2.3}→B1.12; B1.12→B1.13→B1.14. B0.5's result gates
B1.5's start.

| Release | Level | Tasks (concurrent) |
| --- | --- | --- |
| 0.13.0 | L0 | B0.1, B0.3, B0.4 |
| 0.13.0 | L1 | B0.2, B0.5 (OWNER-RUN) |
| 0.14.0 | L0 | BW-T2.1, B1.3, B1.8 |
| 0.14.0 | L1 | B1.1, B1.2, B1.4, BW-T2.3, BW-T2.8 |
| 0.14.0 | L2 | B1.5, B1.6, B1.9 |
| 0.14.0 | L3 | B1.7 |
| 0.14.0 | L4 | B1.10, B1.11 |
| 0.14.0 | L5 | B1.12 (OWNER-RUN) |
| 0.14.0 | L6 | B1.13 |
| 0.14.0 | L7 | B1.14 (OWNER-RUN); BW-T2.11 in the release PR |

B1.1 and B1.2 sit at L1 rather than L0 only because BW-T2.1 rewrites policy fixtures in
`internal/credsvc` and `internal/cli` tests; running them beside it would make two writers of
unnamed fixture files.

**Critical path:** S0: B0.4 → B0.5 (the owner's run gates S1's design). S1: B1.3 → B1.4 → B1.6 →
B1.7 → B1.10 → B1.12 (owner) → B1.13 → B1.14 (owner), with BW-T2.1 → B1.4 and BW-T2.1 → BW-T2.8 →
B1.7 joining it. Eight levels in 0.14.0, three of them owner steps.

**Shared files** (every file written by more than one task, with the levels; no level holds two
writers):

| File | Writers (level) |
| --- | --- |
| `docs/secrets-brokering/README.md` | B0.4 (0.13.0 L0), B0.2 (0.13.0 L1), B1.11 (0.14.0 L4) |
| `docs/secrets-brokering/secret-readers.md` | B0.1 (0.13.0 L0), B1.10 (L4), B1.13 (L6) |
| `internal/cli/docs_secrets_brokering_test.go` | B0.1 (0.13.0 L0), B1.10 (L4) |
| `internal/cli/doctor.go` | B0.3 (0.13.0 L0), B1.9 (0.14.0 L2) |
| `docs/gcp-live-checklist.md` | B0.4 (0.13.0 L0), B1.11 (L4) |
| `internal/config/providers.go` | B1.8 (L0), B1.13 (L6) |
| `deploy/terraform/gcp/modules/workflow/sa.tf` | B1.8 (L0), B1.13 (L6) |
| `deploy/terraform/terraform_test.go` | B1.8 (L0), B1.13 (L6) |
| `internal/credsvc/auth.go`, `auth_test.go` | B1.1 (L1) only; BW-T2.1's fixture sweep (L0) may touch `auth_test.go` |
| `internal/credsvc/bundle.go` | B1.2 (L1) only |
| `internal/credsvc/server.go`, `audit.go` | B1.5 (L2) only |
| `internal/gitproxy/wire.go` | B1.4 (L1) only; B1.5, B1.6 consume |
| `internal/runner/runner.go` | BW-T2.8 (L1), B1.7 (L3) |
| `internal/localcfg/localcfg.go` | BW-T2.3 (L1), B1.9 (L2) |
| `internal/gitprov/providers/providers.go` | B1.7 (L3) only |

## Check numbers

| Check | What it measures | Written by | Run by |
| --- | --- | --- | --- |
| 190 | git fetch, blobless clone and a >32 MiB push through a throwaway Cloud Run proxy; cold start; timeout (F13, F14, U2, U7, U8) | B0.4 | the owner, B0.5 |
| 191 | S1 end to end before the flip; every refusal of G2–G4, G8 audited | B1.11 | the owner, B1.12 |
| 192 | After the flip: no mount, no accessor, runs still work | B1.11 | the owner, B1.14 |
| 193–199 | Reserved and unused | — | — |

## Risks

- **Cold starts on every first request after idle** (D501, as ruled). Check 190 records their size.
- **The build account keeps the Bitbucket token until S4**, so a repository's own Dockerfile may
  still reach it at build time (the workspace-token work's U8), and rotation writes two copies.
- **The agent acts as the run** (O1): the proxy narrows the run, not the agent.
- **A launcher can mint a follow-up identity for any branch** (D475, F28).
- **HTTP/1 body limits** (U2): if Check 190 fails the large push, B1.5 and the service need h2c end
  to end before S1 starts.
