# Bitbucket support through the credentials service: implementation plan

## Status

**Plan only. Nothing is implemented, and no task is approved.** Each task needs the owner's own
explicit go. Answering a decision does not approve a task.

The plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md). It is
written to the owner's answers of 2026-10-26:

- **D360:** reopened for runs too. Not the recommendation.
- **D361:** a read-scoped OAuth consumer for the coordinator.
- **D362:** the credentials service holds the credentials. Not the recommendation.
- **D363:** the service mints run tokens from a consumer. Not the recommendation.
- **D364:** the console and the CLI read the stored view, with no key.
- **D365:** the coordinator job keeps its own App key grant, documented as the second holder.
- **D366:** Data Center is out of scope.
- **D367:** the consumers are owned by the installing person. Not the recommendation.
- **T1:** accepted, with policy and audit in the service.

Four items are still open and gate parts of this plan:

- **O1:** the installing person's identity in runs. Task T3.3 is held on it.
- **O2:** two consumers, one read and one run.
- **O3:** the job token's gated drop.
- **O4:** Check 145 before any mint.

## Conventions every part follows

- Follow `CLAUDE.md`: targeted tests and never whole packages, `gofmt -l .` must print nothing,
  hermetic tests, no secret-shaped literal in tests, docs or fixtures, and comments that explain
  why.
- Follow `.claude/rules/credentials-service.md` for everything under `internal/credsvc/**`, and for
  `SECURITY.md` and `website/content/security.md`:
  - every part is security-sensitive and gets an independent review;
  - after verification, exactly one audit line per refusal;
  - no credential value in any line or error;
  - `fugaro creds set` and `fugaro update --provision-credentials` are never run by an agent;
  - `config.ServiceHeldSecretNames` stays empty;
  - `website/content/security.md` is regenerated with `go run ./internal/clidoc`.
- Follow `.claude/rules/console.md` for `console/**`. A new spawn or IPC argv is security-sensitive
  and needs its pinning tests and a §5.1 row in `docs/design/desktop-console.md`.
- Follow `.claude/rules/terraform.md` for `deploy/terraform/**`.
- **No live cloud action by an agent.** Checks 145–149 are the owner's, and no agent performs a
  step of them. A task that ships a check script writes the script and its `_test.sh` only.
- **Docs are tested.** After any change under `docs/`, `SECURITY.md` or `README.md`, run
  `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`. The pins
  `docs_credentials_runbook_test.go`, `docs_website_security_test.go` and
  `docs_coordinator_test.go` are the likely ones.
- **Steps an operator runs after deploy** go in the release notes' `### For operators` section in
  the same PR (`docs/releases/README.md`). For this plan those are: creating the two consumers,
  `fugaro creds set`, and the allow-list.
- Do not edit `docs/coordinator/` files except to add a pointer when a docs test requires one
  (D364's answer).

## Global constraints and review focus

| # | Constraint | Pinned by |
| --- | --- | --- |
| G1 | A Bitbucket run token goes **only** to a per-repository job, bound to a repository on the bundle's allow-list, presenting a live run identity. Its repository is never the caller's to name | T3.1 tests |
| G2 | Every mint, granted or refused after verification, writes exactly one audit line naming the run, the repository and the consumer kind. A mint that cannot be audited hands out nothing | T2.3, T3.1 |
| G3 | The read consumer is never used for a run mint, and the run consumer is never used for a coordinator mint | T2.3, T3.1 (cross-use tests) |
| G4 | The coordinator caller class admits only the coordinator account's verified email, only on the read-mint route | T2.3 |
| G5 | No consumer key, consumer secret or minted token appears in any log, error, audit line, run record or comment | T2.1, T2.3, T3.1, T3.2 (redaction tests) |
| G6 | The console child gets no new environment key, and its CSP stays `connect-src 'none'` | T1.2 |
| G7 | The run consumer's scopes are exactly the minimum set T3.5 names. A token whose granted scopes go beyond it is refused | T3.5, Check 147 |
| G8 | Without the service, every Bitbucket path behaves exactly as on `main` | T2.5, T3.2 (`TestBitbucketUnchangedWithoutService`) |

**Review focus: attack the new boundaries.**

- A hostile agent holding a run token (F7, F19, F20).
- A caller that forges the coordinator's identity (G4).
- A repository taken off the allow-list while one of its runs is live: the next mint is refused,
  and a token already minted lives until it expires.
- A bundle holding only one consumer: a mint of the other kind is refused by name.

## Slices and tasks

### S0: pins and the check register

**T0.1 Pin this folder.**

- **Files:** `internal/cli/docs_bitbucket_credentials_test.go` (new).
- **Pins:** the status line; the spec is verbatim; D360–D367 are answered with the
  not-recommended marks; the blocks are D360–D389 and Checks 145–154; every cited path exists.
- **Interfaces:** none. **Schema/rules/config:** none.
- **Tests:** `TestDocsBitbucketCredentials*`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run DocsBitbucketCredentials -count=1`.
- **Depends on:** none.
- **Red step:** change one answer cell in a scratch copy; the pin must fail.
- **Reviewer:** standard. **Owner-run:** no.
- **Commit:** `docs(bitbucket-credentials): pin the folder`.

**T0.2 Register Checks 145–149.**

- **Files:** `docs/gcp-live-checklist.md`, rows and sections for 145–149, each NOT RUN, with the
  steps from the plan's check table.
- **Tests:** the existing register pins. **Command:** `go test ./internal/cli -run Docs -count=1`.
- **Depends on:** none.
- **Red step:** a section without its row must fail the register pin. If no pin covers that, T0.1
  adds one.
- **Reviewer:** standard. **Owner-run:** no.
- **Commit:** `docs: register checks 145-149 (Bitbucket through the credentials service)`.

### S1: the stored-view read, with no key (D364)

**T1.1 `fugaro coordinator status --from-store` and `--runs-only`.**

- **What it does:**
  - `--from-store` reads `coordinator/repos/<slug>/` as the person: ADC through
    `internal/cli/cloud.go` `newBudgetClient`, and `coordinator.Store.Read`.
  - `--runs-only` prints the runs half with no pull-request read at all.
  - Neither flag reads an App ID, a key or a Bitbucket credential. Either works with
    `FUGARO_CREDENTIALS_SERVICE_URL` set (F2).
  - A stored view's `StaleUnsupported` and other stale markers are printed as they are stored.
- **Files:** `internal/cli/coordinator.go` (flags and help text only),
  `internal/cli/coordinator_view.go` (new), `internal/cli/coordinator_view_test.go` (new),
  `internal/coordinator/state.go` (a typed `ReadRepoView`), `internal/coordinator/state_test.go`.
- **Interfaces:** it produces the JSON shape of a stored view, documented in the command help. T1.2
  copies that shape into `console/docs/json-contract.md`.
- **Tests:** `TestCoordinatorStatusFromStoreNeedsNoKey`, `TestCoordinatorStatusFromStoreWithService`,
  `TestCoordinatorStatusRunsOnly`, `TestReadRepoView`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'CoordinatorStatus(FromStore|RunsOnly)' -count=1 && go test ./internal/coordinator -run ReadRepoView -count=1`.
- **Depends on:** none.
- **Red step:** run `TestCoordinatorStatusFromStoreWithService` against the current code; it must
  fail on the `coordinatorAppCredential` refusal.
- **Reviewer:** standard, because it holds no credential. **Owner-run:** no.
- **Commit:** `cli: coordinator status reads the stored view with no key (D364)`.

**T1.2 Console P6, rebuilt on T1.1.** This is coordinator P6 as amended by D364. It replaces P6's
"read through `coordinator status`". Everything else in P6 stands: no new credential, the CSP
unchanged, and `textContent` only.

- **Files:** `console/src/main/ipc.ts`, `console/src/main/spawn.ts`, `console/src/preload/index.ts`,
  `console/src/renderer/entry.ts`, a new renderer view file, `console/src/shared/sanitize.ts` if a
  new text class is needed, `console/test/unit/` (the pinning tests), `console/docs/json-contract.md`,
  and the §5.1 table in `docs/design/desktop-console.md`.
- **Tests:** P6's three mutations, plus one more. The argv `coordinator status --from-store --json`
  is pinned exactly, and a variant without `--from-store` must be refused by the spawn allow-list.
- **Command:** in `console/`, `npm run typecheck && npm run test:unit -- --maxWorkers=2 --testTimeout=60000`.
  Then `go test ./internal/cli -run Docs -count=1`.
- **Depends on:** T1.1.
- **Red step:** add the argv without `--from-store` to the allow-list in a scratch tree; the
  pinning test must fail.
- **Reviewer:** **opus + mandatory independent review**, because this is a new argv
  (`.claude/rules/console.md`). **Owner-run:** no.
- **Commit:** `console: the project view, read from the stored coordinator view`.
- **Branch:** the console's own release convention applies.

**T1.3 Check 149 (OWNER-RUN).** On the owner's own machine, the console's project view shows a
stored view. This settles U9.

- **What the owner types:** `fugaro coordinator status --from-store --json`, then opens the
  console's project view.
- **Depends on:** T1.2 and T0.2. No agent performs any step of it.

### S2: Bitbucket in the coordinator job, through the service (D361, D362, D365)

**T2.1 A token source for the Bitbucket provider, and the `client_credentials` exchange.**

- **What changes:**
  - `bitbucket.Options` takes a `gitprov.TokenSource`, and a static token is wrapped into one, so
    today's path is unchanged.
  - A new `oauth.go` exchanges a consumer's key and secret at the token endpoint and returns the
    token and its expiry.
  - The exchange never logs or wraps an error with the secret.
- **Files:** `internal/gitprov/bitbucket/bitbucket.go`, `internal/gitprov/bitbucket/oauth.go`
  (new), `internal/gitprov/bitbucket/oauth_test.go` (new),
  `internal/gitprov/bitbucket/bitbucket_test.go`.
- **Tests:** `TestClientCredentialsExchange`, `TestExchangeErrorCarriesNoSecret`,
  `TestStaticTokenUnchanged`.
- **Command:** `go test ./internal/gitprov/bitbucket -run 'ClientCredentials|ExchangeError|StaticToken' -count=1`.
- **Depends on:** none.
- **Red step:** `TestExchangeErrorCarriesNoSecret` fails against an exchange that wraps the
  response body.
- **Reviewer:** **opus + mandatory independent review**. **Owner-run:** no.
- **Commit:** `gitprov/bitbucket: a token source and the client_credentials exchange`.

**T2.2 Two consumers in the bundle, and the allow-list (F21).**

- **What changes:**
  - New bundle kinds `bitbucket-read-consumer` and `bitbucket-run-consumer`. Each holds the
    consumer key, the consumer secret and the workspace.
  - The run consumer also holds `repositories`, the allow-list.
  - `fugaro creds set` accepts both, from stdin only (the owner's 2026-10-22 ruling).
- **Files:** `internal/credsvc/bundle.go`, `internal/credsvc/bundle_test.go`,
  `schemas/creds-bundle.schema.json`, `internal/cli/creds.go`, `internal/cli/creds_test.go`.
- **Schema change:** yes, two kinds, and unknown fields are refused.
- **Tests:** `TestBundleBitbucketConsumers`, `TestBundleRefusesConsumerWithoutWorkspace`,
  `TestCredsSetBitbucketConsumerFromStdin`.
- **Command:** `go test ./internal/credsvc -run Bundle -count=1 && GOFLAGS=-p=2 go test ./internal/cli -run CredsSet -count=1`.
- **Depends on:** none.
- **Red step:** a bundle that names an unknown field in a consumer entry must fail to decode.
- **Reviewer:** **opus + mandatory independent review**. **Owner-run:** no.
- **Commit:** `credsvc: the bundle holds a read and a run Bitbucket consumer`.

**T2.3 The coordinator caller class, and the read mint (D362, F11).**

- **What changes:**
  - `Authenticator` gains a second, separate admission. It verifies a Google ID token whose email
    is **the coordinator account**, named to the service by its environment.
  - That admission takes no run identity and is valid **only** on a new
    `POST /v1/bitbucket/read-token`.
  - The new route mints from `bitbucket-read-consumer` only, and writes one audit line with a new
    purpose and no tier.
- **Files:** `internal/credsvc/auth.go`, `internal/credsvc/auth_test.go`,
  `internal/credsvc/bitbucket.go` (new), `internal/credsvc/bitbucket_test.go` (new),
  `internal/credsvc/server.go`, `internal/credsvc/audit.go`, `internal/credsvc/audit_test.go`,
  `internal/credsvc/wire.go`.
- **Tests:**
  - `TestCoordinatorCallerOnlyOnReadRoute`: the coordinator account is refused on `/v1/messages`
    and on both mint routes except the read one.
  - `TestReadMintNeverUsesRunConsumer`.
  - `TestReadMintAuditedOnce`.
  - `TestReadMintFailsClosedUnaudited`.
  - `TestRunIdentityRefusedOnReadRoute`.
- **Command:** `go test ./internal/credsvc -run 'CoordinatorCaller|ReadMint|RunIdentityRefusedOnRead' -count=1`.
- **Depends on:** T2.1 and T2.2. **Check 145 must have passed (O4)** before this merges.
- **Red step:** `TestCoordinatorCallerOnlyOnReadRoute` fails against an admission that returns a
  generic `Identity`.
- **Reviewer:** **opus + mandatory independent review**, because the front door changes.
  **Owner-run:** no.
- **Commit:** `credsvc: the coordinator's caller class and the Bitbucket read mint (D362)`.

**T2.4 Terraform: the coordinator job may call the service.**

- **What changes:**
  - In `modules/installation/coordinator.tf`, the coordinator job gets
    `FUGARO_CREDENTIALS_SERVICE_URL`, and its account gets `roles/run.invoker` on the service.
  - In `credentials.tf`, the service learns the coordinator account's email.
  - Global Constraint 12 of the credentials-service plan holds: one container, one grant, and no
    new secret.
- **Files:** `deploy/terraform/gcp/modules/installation/coordinator.tf`,
  `deploy/terraform/gcp/modules/installation/credentials.tf`,
  `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl`.
- **Tests:** tftest runs for: the coordinator on with the service off (no change), the coordinator
  on with the service on (invoker plus env), and the service on with the coordinator off (no
  email).
- **Command:** `terraform fmt -check -recursive deploy/terraform && go test -tags terraform ./internal/infra/... -run Installation -count=1`.
- **Depends on:** T2.3, for the environment variable's name.
- **Red step:** the "service off" run must fail against a module that always adds the invoker.
- **Reviewer:** **opus + mandatory independent review** (IAM). **Owner-run:** no.
- **Release note:** `### For operators`: `fugaro update` re-applies the installation.
- **Commit:** `terraform: the coordinator job may call the credentials service`.

**T2.5 The sweep reads Bitbucket through the service.**

- **What changes:**
  - `coordinator.supported` admits Bitbucket when the reader carries a Bitbucket token source.
  - The sweep's wiring opens Bitbucket repositories with
    `bitbucket.ServiceReadTokens(serviceURL, coordinator identity)`, a new client half.
  - Without the service, the sweep keeps `StaleUnsupported`, so G8 holds.
  - A person's local `coordinator status` for a Bitbucket repository still refuses, and the
    refusal points at `--from-store`.
- **Files:** `internal/coordinator/project.go`, `internal/coordinator/project_test.go`,
  `internal/cli/coordinator.go` (the sweep's wiring), `internal/gitprov/bitbucket/service.go` (new),
  `internal/gitprov/bitbucket/service_test.go` (new).
- **Tests:** `TestSweepReadsBitbucketThroughService`, `TestBitbucketUnchangedWithoutService`,
  `TestLocalStatusBitbucketPointsAtFromStore`, `TestServiceReadTokensRedacted`.
- **Command:** `go test ./internal/coordinator -run 'Bitbucket' -count=1 && go test ./internal/gitprov/bitbucket -run ServiceRead -count=1 && GOFLAGS=-p=2 go test ./internal/cli -run 'Sweep|LocalStatusBitbucket' -count=1`.
- **Depends on:** T2.1, T2.3 and T1.1, because they share `internal/cli/coordinator.go`.
- **Red step:** `TestSweepReadsBitbucketThroughService` fails while `supported` returns
  `ErrBitbucket`.
- **Reviewer:** **opus + mandatory independent review**. **Owner-run:** no.
- **Commit:** `coordinator: the sweep reads Bitbucket through the credentials service`.

**T2.6 Runbook: create, rotate and transfer the consumers (D367).**

- **What it documents:**
  - Creating both consumers under the installing person's account, with exactly the read scopes
    and exactly T3.5's run scopes.
  - Writing them with `fugaro creds set`.
  - Rotation: regenerate the secret, write a new bundle version, and confirm a mint.
  - **Ownership transfer**, when the person leaves: a new owner creates new consumers, writes them,
    and the old ones are deleted.
  - In `docs/git-providers.md`, the coordinator's Bitbucket paragraph (D361, D362) and the second
    holder of the App key (D365).
- **Files:** `docs/credentials-service/runbook.md`, `docs/git-providers.md`,
  `internal/cli/docs_bitbucket_runbook_test.go` (new).
- **Tests:** `TestDocsBitbucketRunbook*` checks that the creation, rotation and ownership-transfer
  steps are present, that both consumers are named, and that the scope sets equal T3.5's.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Depends on:** none. It documents the agreed design.
- **Red step:** `TestDocsBitbucketRunbook*` fails before the runbook text lands.
- **Reviewer:** **opus + independent review**, because it is a credential procedure.
  **Owner-run:** no.
- **Commit:** `docs: Bitbucket consumers, creation, rotation and ownership transfer`.

**T2.7 The Check 145 script.**

- **What it does:** `scripts/live-checks/check145.sh` reads the sandbox consumer from a file the
  owner names and never prints it. It reports:
  - U1, the token lifetime;
  - U2, whether a refresh token is returned and whether `DELETE` revokes a token;
  - U3, whether the token reaches a second workspace;
  - U5, the seat count before and after;
  - U8, whether IP allowlisting applies;
  - U11, whether the token response carries scopes.

  It prints a `CHECK 145 RESULT` block, in the shape of `check90.sh`.
- **Files:** `scripts/live-checks/check145.sh` (new), `scripts/live-checks/check145_test.sh` (new).
- **Tests:** `check145_test.sh` runs against a local fake, needs no network, and checks that the
  secret is never echoed.
- **Command:** `bash scripts/live-checks/check145_test.sh`.
- **Depends on:** none.
- **Red step:** the test fails when the script echoes the secret.
- **Reviewer:** **opus + independent review**. **Owner-run:** no. The script is written here and
  run in T2.8.
- **Commit:** `live-checks: check 145, the Bitbucket consumer facts`.

**T2.8 Check 145 (OWNER-RUN).**

- **What the owner types:** `scripts/live-checks/check145.sh --consumer-file <path> --workspace <sandbox> --second-workspace <sandbox-2>`.
- **Depends on:** T2.7 and T0.2.
- **Gate:** T2.3 and T3.1 do not merge before it passes. A U3 result of "beyond the workspace"
  returns D363 to the owner (O4).

**T2.9 The Check 146 script.**

- **What it does:** `scripts/live-checks/check146.sh` runs the coordinator sweep against a sandbox
  installation with the service on, and asserts a Bitbucket repository's view is read, not
  `StaleUnsupported`.
- **Files:** `scripts/live-checks/check146.sh` (new), `scripts/live-checks/check146_test.sh` (new).
- **Command:** `bash scripts/live-checks/check146_test.sh`.
- **Depends on:** T2.4 and T2.5.
- **Red step:** the test fails on a sweep log that shows `StaleUnsupported`.
- **Reviewer:** standard. **Owner-run:** no.
- **Commit:** `live-checks: check 146, the coordinator reads Bitbucket`.

**T2.10 Check 146 (OWNER-RUN).**

- **What the owner types:** `scripts/live-checks/check146.sh --installation <sandbox>`.
- **Depends on:** T2.9 and T0.2.

### S3: the service mints Bitbucket run tokens (D360, D363, T1)

**T3.1 The run mint, with an allow-list and an audit line.**

- **What it does:** `POST /v1/bitbucket/access-token` admits exactly what the GitHub mint admits:
  a per-repository job, a live run identity, and no repository field.
  - It mints from `bitbucket-run-consumer` only when the job's bound repository
    (`JobAccount.Repo`) is on the consumer's `repositories` allow-list and in its workspace.
  - Otherwise it refuses by name, with one audit line (G1, G2).
  - Every mint writes one audit line with the run, the repository and the consumer kind.
  - If the audit line cannot be written, the mint fails closed.
- **Files:** `internal/credsvc/bitbucket.go`, `internal/credsvc/bitbucket_test.go`,
  `internal/credsvc/server.go`, `internal/credsvc/audit.go`, `internal/credsvc/audit_test.go`.
- **Tests:** `TestRunMintAllowListed`, `TestRunMintRefusesUnlisted`, `TestRunMintNoRepositoryField`,
  `TestRunMintNeverUsesReadConsumer`, `TestRunMintAuditedOnce`, `TestRunMintSharedJobRefused`,
  `TestCoordinatorRefusedOnRunMint`.
- **Command:** `go test ./internal/credsvc -run 'RunMint|CoordinatorRefusedOnRunMint' -count=1`.
- **Depends on:** T2.3, which creates `bitbucket.go` and the routing, and T2.8 (Check 145 passed).
- **Red step:** `TestRunMintRefusesUnlisted` fails against a mint that checks only the run
  identity.
- **Reviewer:** **opus + mandatory independent review**. **Owner-run:** no.
- **Commit:** `credsvc: Bitbucket run tokens, allow-listed and audited (D363)`.

**T3.2 The runner asks the service for its Bitbucket token.**

- **What changes:**
  - With `FUGARO_CREDENTIALS_SERVICE_URL` set and the provider `bitbucket`,
    `providers.FromEnv` opens the provider on `bitbucket.ServiceTokens`. That source presents the
    two identity headers, as `github.ServiceTokens` does.
  - The runner's `refreshGitAuth` refreshes the agent's `FUGARO_GIT_TOKEN` before each stage.
  - A mint failure halts the run. It never falls back to the mounted token.
- **Files:** `internal/gitprov/providers/providers.go`, `internal/gitprov/bitbucket/service.go`,
  `internal/gitprov/bitbucket/service_test.go`, `internal/runner/runner.go`, and the runner's
  targeted test file.
- **Tests:** `TestBitbucketRunTokenFromService`, `TestBitbucketNoFallbackToMountedToken`,
  `TestBitbucketUnchangedWithoutService`, `TestRefreshGitAuthBitbucket`.
- **Command:** `go test ./internal/gitprov/... -run 'BitbucketRunToken|NoFallback|UnchangedWithout' -count=1 && go test -timeout 40m ./internal/runner -run RefreshGitAuthBitbucket -count=1`.
- **Depends on:** T3.1 and T2.5, which share `service.go`.
- **Red step:** `TestBitbucketNoFallbackToMountedToken` fails against an opener that reads
  `FUGARO_BITBUCKET_TOKEN` first.
- **Reviewer:** **opus + mandatory independent review**. **Owner-run:** no.
- **Commit:** `runner: Bitbucket run tokens from the credentials service`.

**T3.3 Comment identity under the installing person (F19). HELD on O1.**

- **What it does:** implements the option the owner picks for O1.
  - Under O1(a): Bitbucket `Self` stays the token's user, and the docs say the installing person
    cannot steer a follow-up.
  - Under O1(b): the run consumer's owner becomes a dedicated account. This is a runbook change
    only.
- **Files:** `internal/gitprov/bitbucket/comments.go`, `internal/followup/trust.go`,
  `internal/followup/select.go`, and their tests, as the option needs.
- **Tests:** `TestAgentCommentAsPersonNeverSteers`, which holds under every option.
- **Command:** `go test ./internal/followup ./internal/gitprov/bitbucket -run 'Self|Steer' -count=1`.
- **Depends on:** O1.
- **Red step:** `TestAgentCommentAsPersonNeverSteers` fails against marker-only `Self`.
- **Reviewer:** **opus + mandatory independent review**. **Owner-run:** no.
- **Commit:** `followup: Bitbucket comment identity under a person-owned consumer`.

**T3.4 SECURITY.md and the docs say it plainly (T1).**

- **What it says:**
  - In `SECURITY.md` §"Narrow git credentials", and in the hardening advice: a Bitbucket run token
    minted by the service is **workspace-wide for the consumer's owner** for 1–2 hours. The agent
    can read it, so the agent can push, comment, approve and merge as that person anywhere in the
    workspace (F7, F20).
  - The allow-list decides who receives a token, not what it reaches.
  - The advice: keep the owner out of required approvers and merge checks.
  - `docs/git-providers.md` §"What the agent gets" and §Bitbucket Cloud, updated to match.
  - `website/content/security.md` is regenerated.
- **Files:** `SECURITY.md`, `website/content/security.md` (generated), `docs/git-providers.md`,
  `internal/cli/docs_bitbucket_security_test.go` (new).
- **Tests:** `TestDocsBitbucketSecurity` (new here);
  `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|WebsiteSecurity' -count=1`; in `website/`,
  `node --test test/security.mjs`.
- **Depends on:** T3.2, because it describes what is built, and T2.6, because they share
  `docs/git-providers.md`.
- **Red step:** `TestDocsBitbucketSecurity` asserts the sentence "workspace-wide for the consumer's
  owner" in `SECURITY.md`, and fails before the text lands.
- **Reviewer:** **opus + mandatory independent review** (the credentials-service rules).
  **Owner-run:** no.
- **Commit:** `SECURITY: a Bitbucket run token is workspace-wide for the consumer's owner`.

**T3.5 The minimum run scopes, enforced, with their check (G7).**

- **The scope set:**
  - `repository:write`, which includes read;
  - `pullrequest:write`;
  - `account`, only if `GET /user` needs it, which the check settles;
  - and nothing else: no `pipeline`, `webhook`, `runner`, `project` or `*:admin`.
- **Enforcement:** the service compares the token response's scopes, when Bitbucket returns them
  (U11), with this set. It refuses and audits a token that carries more.
- **The check script:** `check147.sh` runs one sandbox run through the service. It proves push,
  pull-request create, the draft flag, reviewers and comments with exactly this set. It also
  **records the token's reach into a second sandbox repository**, which shows F7 and F20 on the
  owner's own sandbox, and U10.
- **Files:** `internal/credsvc/bitbucket.go`, `internal/credsvc/bitbucket_test.go`,
  `scripts/live-checks/check147.sh` (new), `scripts/live-checks/check147_test.sh` (new).
- **Tests:** `TestRunMintRefusesWiderScopes`; `check147_test.sh`.
- **Command:** `go test ./internal/credsvc -run WiderScopes -count=1 && bash scripts/live-checks/check147_test.sh`.
- **Depends on:** T3.1, because they share `bitbucket.go`.
- **Red step:** `TestRunMintRefusesWiderScopes` fails against a mint that ignores scopes.
- **Reviewer:** **opus + mandatory independent review**. **Owner-run:** no.
- **Commit:** `credsvc: the Bitbucket run token's scopes are the minimum, enforced`.

**T3.6 The gated drop of the job's own `bitbucket-token` (F22, O3).**

- **What changes:**
  - `bitbucket-token` becomes a name the service-held mechanism recognises, with its mirror in
    `modules/workflow/sa.tf` `service_held_secret_names`.
  - **The list stays empty**, as the rules file requires. The owner flips it after Check 147.
  - The repository's build account keeps its grant (F8), so image builds are unchanged.
- **Files:** `internal/config/providers.go`, `internal/config/providers_test.go`,
  `deploy/terraform/gcp/modules/workflow/sa.tf`,
  `deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl`.
- **Tests:** `TestServiceHeldBitbucketTokenKeepsBuildGrant`, and the existing guard test that the
  list is empty.
- **Command:** `go test ./internal/config -run ServiceHeld -count=1 && go test -tags terraform ./internal/infra/... -run Repo -count=1`.
- **Depends on:** T3.1.
- **Red step:** with the list flipped in a scratch tree, the build grant must survive. The test
  fails against a drop that removes it.
- **Reviewer:** **opus + mandatory independent review**. **Owner-run:** the flip itself is the
  owner's.
- **Commit:** `config: bitbucket-token can be service-held (gated, list empty)`.

**T3.7 Check 147 (OWNER-RUN).**

- **What the owner types:** `scripts/live-checks/check147.sh --installation <sandbox> --repo <sandbox-repo> --second-repo <sandbox-repo-2>`.
- **Depends on:** T3.2, T3.5 and T0.2.
- **Next step:** after it passes, the owner decides on the T3.6 flip.

**T3.8 Check 148, the rotation and ownership-transfer drill (OWNER-RUN, D367).**

- **What the owner does:** follows `docs/credentials-service/runbook.md` §Bitbucket consumers on the
  sandbox: rotate a secret, then transfer ownership to a second sandbox account. After each step the
  owner confirms a read mint (and a run mint once S3 is deployed), and confirms the old secret is
  refused.
- **Depends on:** T2.6, T2.3 and T0.2.

## Parallel execution

**Edges** (from each task's "Depends on"):

- T1.1 → T1.2 → T1.3
- T2.1 → T2.3
- T2.2 → T2.3
- T2.3 → T2.4
- T2.1, T2.3 and T1.1 → T2.5
- T2.4 and T2.5 → T2.9 → T2.10
- T2.7 → T2.8
- T2.8 and T2.3 → T3.1
- T3.1 and T2.5 → T3.2
- T3.1 → T3.5
- T3.1 → T3.6
- T3.2 and T2.6 → T3.4
- T3.2 and T3.5 → T3.7
- T2.6 and T2.3 → T3.8
- O1 → T3.3
- T0.2 → every owner-run check

**Levels** (the tasks in one level run concurrently):

| Level | Tasks |
| --- | --- |
| L0 | T0.1, T0.2, T1.1, T2.1, T2.2, T2.6, T2.7 |
| L1 | T1.2, T2.8 (owner), T3.3 (held on O1) |
| L2 | T1.3 (owner), T2.3 |
| L3 | T2.4, T2.5, T3.1, T3.8 (owner) |
| L4 | T2.9, T3.2, T3.5, T3.6 |
| L5 | T2.10 (owner), T3.4, T3.7 (owner) |

T2.3 sits at L2 because it waits for Check 145 (T2.8), not only for T2.1 and T2.2.

**Critical path:** T2.7 → T2.8 (Check 145) → T2.3 → T3.1 → T3.2 → T3.7 (Check 147), then the
owner's T3.6 flip. S1 is off the critical path and ships first. It is the smallest useful slice,
and it needs no credential.

**What must be sequential, and why:**

- T2.3 → T3.1 → T3.5: one file, `internal/credsvc/bitbucket.go`, and they share the routes in
  `server.go` and `audit.go`.
- T2.5 → T3.2: one file, `internal/gitprov/bitbucket/service.go`.
- T1.1 → T2.5: one file, `internal/cli/coordinator.go`.
- T2.6 → T3.4: one file, `docs/git-providers.md`.
- Check 145 before any mint: O4.

**Shared files.** No level has two writers of one file. A change to any task's Files list
re-checks this table.

| File | Writers (level) |
| --- | --- |
| `internal/cli/coordinator.go` | T1.1 (L0), T2.5 (L3) |
| `internal/credsvc/bitbucket.go`, `bitbucket_test.go` | T2.3 (L2), T3.1 (L3), T3.5 (L4) |
| `internal/credsvc/server.go`, `audit.go`, `audit_test.go` | T2.3 (L2), T3.1 (L3) |
| `internal/credsvc/auth.go` | T2.3 (L2) |
| `internal/gitprov/bitbucket/bitbucket.go` | T2.1 (L0) |
| `internal/gitprov/bitbucket/service.go`, `service_test.go` | T2.5 (L3), T3.2 (L4) |
| `internal/gitprov/bitbucket/comments.go` | T3.3 (L1) |
| `internal/credsvc/bundle.go`, `schemas/creds-bundle.schema.json`, `internal/cli/creds.go` | T2.2 (L0) |
| `docs/git-providers.md` | T2.6 (L0), T3.4 (L5) |
| `docs/credentials-service/runbook.md` | T2.6 (L0) |
| `docs/gcp-live-checklist.md` | T0.2 (L0) |
| `deploy/terraform/gcp/modules/installation/*.tf` | T2.4 (L3) |
| `deploy/terraform/gcp/modules/workflow/sa.tf`, `internal/config/providers.go` | T3.6 (L4) |
| `internal/gitprov/providers/providers.go`, `internal/runner/runner.go` | T3.2 (L4) |
| `SECURITY.md`, `website/content/security.md` | T3.4 (L5) |
| `console/**`, `console/docs/json-contract.md`, `docs/design/desktop-console.md` | T1.2 (L1) |

## Checks this plan takes (Checks 145–154)

| Check | What it measures | Run by | Written by |
| --- | --- | --- | --- |
| 145 | The consumer facts: U1 lifetime, U2 refresh and revocation, U3 reach beyond the workspace, U4 plan, U5 seats, U8 IP allowlisting, U11 scopes in the response. **It gates every mint (O4)** | owner | T0.2 (section), T2.7 (script) |
| 146 | The coordinator sweep reads a Bitbucket repository through the service | owner | T0.2, T2.9 |
| 147 | A sandbox run on a minted token works with exactly the minimum scopes, and the token's reach into a second repository is recorded (F7, F20, U10) | owner | T0.2, T3.5 |
| 148 | The rotation and ownership-transfer drill (D367) | owner | T0.2, T2.6 (runbook) |
| 149 | The console shows the stored view on the owner's own machine (U9) | owner | T0.2 |

Checks 150–154 are reserved and unused.

## Release actions

These go in the release notes' `### For operators` section of the PR that ships each part.

- **T2.4:** `fugaro update` per installation.
- **T2.6 and T3.1:** create the two consumers as the installing person, then `fugaro creds set`
  both, with the allow-list (per installation).
- **T3.6:** no action until the owner flips the gate.
