# Bitbucket support through the credentials service: implementation plan

## Status

**Plan only. Nothing is implemented, and no task is approved.** Each task needs the owner's own
explicit go, and answering a decision does not approve a task.

The plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md), and it is
written to the owner's answers of 2026-10-26:

- **D360**, reopened for runs too: not the recommendation. **Amended 2026-10-26: narrowed back to
  coordinator reads only, which is the recommendation.**
- **D361**, a read-scoped OAuth consumer for the coordinator.
- **D362**, the credentials service holds the credentials: not the recommendation.
- **D363**, the service mints run tokens from a consumer: not the recommendation. **Amended
  2026-10-26: reversed by the owner (O1). Runs keep their per-repository tokens.**
- **D364**, the console and the CLI read the stored view, with no key.
- **D365**, the coordinator job keeps its own App key grant, documented as the second holder.
- **D366**, Data Center is out of scope.
- **D367**, the consumers are owned by the installing person: not the recommendation. While S3
  is parked, this applies to the read consumer only.
- **T1** was accepted, with policy and audit in the service as the mitigation. **It is moot until
  S3 returns.**

**The owner's rulings of 2026-10-26, after #1104** (reconciliation §7):

- **O1, answered.** D363 is revisited, and runs keep their per-repository access tokens. **S3 is
  parked**: it is deferred until a push and pull-request proxy exists, not cancelled. S1 and S2
  stay.
- **O2, answered.** Two consumers is the design. Only the read consumer is built now, in T2.2. The
  run consumer's bundle kind and its allow-list return with S3.
- **O7, answered.** Review-only and artifact runs get **no** write token. This is moot while runs
  keep per-repository tokens, and it is the rule for whenever S3 returns.
- **O3** (the per-repository flip of T3.6) is parked with S3.
- **O4** makes Check 145 a precondition of T2.3, the only mint left in the active plan.
- **O6** is the read mint's custody, recorded as a residual risk.

**The active plan is S0–S2.** S3 is kept below as a parked slice. Its tasks keep their numbers, and
none of them may start until the owner unparks S3.

**D366 lands in no task by design.** Nothing is built for Data Center. T2.5 pins the API root for
the service-minted read path, which also keeps it on Bitbucket Cloud. T3.2, which is parked, does
the same for runs.

## Conventions every part follows

- **`CLAUDE.md`:**
  - run targeted tests, never whole packages;
  - `gofmt -l .` must print nothing;
  - tests are hermetic;
  - no literal shaped like a secret in tests, docs or fixtures;
  - comments say why.
- **`.claude/rules/credentials-service.md`** (`internal/credsvc/**`, `SECURITY.md`,
  `website/content/security.md`):
  - every part is security-sensitive and gets an independent review;
  - after verification, every refusal writes exactly one audit line;
  - no credential value appears in any line or error;
  - `fugaro creds set` and `fugaro update --provision-credentials` are never run by an agent;
  - `config.ServiceHeldSecretNames` **stays empty and is not touched by this plan** (T3.6 uses a
    separate per-repository switch);
  - `website/content/security.md` is regenerated with `go run ./internal/clidoc`.
- **`.claude/rules/console.md`** (`console/**`): a new spawn or IPC argv is security-sensitive.
  It needs its pinning tests and a row in the §5.1 table of `docs/design/desktop-console.md`.
- **`.claude/rules/terraform.md`** (`deploy/terraform/**`, `internal/infra/**`), for every
  Terraform task:
  - `terraform fmt -check -recursive deploy/terraform`;
  - in each affected root, `init -backend=false -lockfile=readonly`, then `validate`, then
    `terraform test`, with `TF_DATA_DIR` outside the tree;
  - `scripts/gen-golden-plans.sh` when a module's plan changes;
  - `go test -tags terraform ./internal/infra/...`;
  - `tflint`;
  - `sh deploy/terraform/scan.sh`;
  - `internal/infra/tf/cover.go`, deliberately, for a role granted to a new kind of principal.
- **No agent takes a live cloud action.** Checks 145–149 are the owner's. A task that ships a
  check script writes the script and its `_test.sh` only.
- **Docs are tested.** After any change under `docs/`, `SECURITY.md` or `README.md`, run
  `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Operator steps go in the release notes.** Steps an operator runs after a deploy go in that
  PR's `### For operators` section (`docs/releases/README.md`). Each step is labelled
  per-installation or per-repository.
- **`docs/coordinator/` is not edited**, except to add a pointer if a docs test requires one
  (D364's answer).

## Global constraints and review focus

**G1, G7 and G9 apply to the parked S3 only.** In G2, G3, G5, G8 and G10, the clauses about the
run mint apply when S3 returns.

| # | Constraint | Pinned by |
| --- | --- | --- |
| G1 | A Bitbucket run token goes **only** to a per-repository job bound to a **Bitbucket** repository on the bundle's allow-list, with a live run identity. The repository and provider come from the verified binding and never from the caller | T3.1 (`TestRunMintRefusesGitHubJobWithSameName`) |
| G2 | Every mint, granted or refused after verification, writes exactly one audit line naming the run (or the coordinator), the repository and the consumer kind. A mint that cannot be audited hands out nothing | T2.3, T3.1 |
| G3 | The read consumer is never used for a run mint, and the run consumer never for a coordinator mint | T2.3, T3.1 |
| G4 | The coordinator caller class admits only the coordinator account's verified email, only on the read-mint route. Every other account is refused by name, including the coordinator's planned resident service (coordinator P7) | T2.3 |
| G5 | No consumer key, consumer secret, minted token or **refresh token** appears in any answer, log, error, audit line, run record or comment. A refresh token is never forwarded to a caller | T2.1, T2.3, T3.1, T3.2 |
| G6 | The console child gets no new environment key, and its CSP stays `connect-src 'none'` | T1.2 |
| G7 | The run consumer's scopes are the minimum set from T3.5. The service can enforce this only if the token response lists scopes (U11); otherwise Check 147 is the only enforcement | T3.5, Check 147 |
| G8 | Without the service, every Bitbucket path and the GitHub sweep behave exactly as they do on `main` | T2.5 (`TestSweepGitHubUnchangedWithService`, `TestSweepBitbucketUnchangedWithoutService`), T3.2 (`TestRunPathUnchangedWithoutService`) |
| G9 | With the service on, only repositories whose `bitbucket_via_service` switch is on (set by Terraform on the job) use the service path or lose their mounted token. Every other Bitbucket repository is unchanged. The switch is a code path; the allow-list entry is the exposure | T3.0, T3.2, T3.6 |
| G10 | A service-minted token is never sent to an overridden API root (`FUGARO_BITBUCKET_API_URL`) | T2.5, T3.2 (`TestServicePathRefusesAPIRootOverride`) |

**Review focus: attack the new boundaries.**

- **Agents and launchers.** While S3 is parked, no run holds a consumer token. Runs keep their
  per-repository access token, as on `main`. The S3 probes (F7, F19, F20, F26–F29) apply when S3
  returns.
- **Caller identity.** Someone who forges the coordinator's identity, or overrides the coordinator
  job's environment (G4, G10).
- **Allow-list edges:**
  - A repository is taken off the allow-list while one of its runs is live. The next mint is
    refused, and a token already minted lives until it expires.
  - A GitHub job has the same canonical name as an allow-listed Bitbucket repository.
- **A partial bundle.** If the bundle holds only one consumer, a mint of the other kind is refused
  by name.

## Slices and tasks

### S0: pins and the check register

**T0.1 Pin this folder.**

- **Files:** `internal/cli/docs_bitbucket_credentials_test.go` (new).
- **What it pins:**
  - the status line;
  - the spec, verbatim;
  - D360–D367, answered, with the not-recommended marks;
  - the number blocks, D360–D389 and Checks 145–154;
  - every cited path exists.
- **Tests:** `TestDocsBitbucketCredentials*`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run DocsBitbucketCredentials -count=1`.
- **Depends on:** none.
- **Red step:** change one answer cell in a scratch copy; the pin must fail.
- **Reviewer:** standard. **Owner-run:** no.
- **Commit:** `docs(bitbucket-credentials): pin the folder`.

**T0.2 Register Checks 145, 146 and 149.**

- **Files:** `docs/gcp-live-checklist.md`, with rows and sections for 145, 146 and 149, each
  marked NOT RUN. Checks 147 and 148 are parked with S3 and stay reserved.
- **Tests:** the existing register pins.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Depends on:** none.
- **Red step:** a section without its row must fail the register pin. If no pin catches that,
  T0.2 adds one in its own new file, `internal/cli/docs_checks_145_149_test.go`.
- **Reviewer:** standard. **Owner-run:** no.
- **Commit:** `docs: register checks 145, 146 and 149 (Bitbucket through the credentials service)`.

### S1: the stored-view read, with no key (D364)

**T1.1 `fugaro coordinator status --from-store`, with a runs-only fallback built in.**

- **What it does:** `--from-store` reads `coordinator/repos/<slug>/` as the person, with the same
  `--repo` and `--all` selection the command already has.
  - The read goes through `roles/firebasedatabase.viewer`, which `modules/firebase/iam.tf` grants
    to launchers and operators. It is an IAM read, not a rules read: the rules' reader on
    `coordinator` is a device user.
  - The read uses ADC through `internal/cli/cloud.go` `newBudgetClient` and
    `coordinator.Store.Read`.
  - When no node is stored, the same command prints the runs half with `"stored": false`. That is
    the runs-only fallback, so a console needs only one argv.
  - The fallback does not consult the local config. `localcfg.Config.Shared` drops `Terraform`,
    which is where the coordinator setting lives, so on a teammate's machine it would always read
    as off.
  - A node left over after the coordinator was turned off is printed as the last stored view, and
    the console labels it that way (open item in the PR).
  - The stored view carries no time by design (`internal/coordinator/sweep.go`), so the output does
    not claim an age it cannot know.
  - It reads no App ID, no key and no Bitbucket credential.
  - It works with `FUGARO_CREDENTIALS_SERVICE_URL` set.
  - A stored `StaleUnsupported` or other stale marker is printed exactly as stored.
- **Files:**
  - `internal/cli/coordinator.go` (the flag and help text only);
  - `internal/cli/coordinator_view.go` (new);
  - `internal/cli/coordinator_view_test.go` (new);
  - `internal/coordinator/state.go` (a typed `ReadRepoView`);
  - `internal/coordinator/state_test.go`;
  - `website/content/cli-reference.md`, regenerated with `go run ./internal/clidoc`.
- **Interfaces:** the JSON shape is documented in the help text. T1.2 copies it into
  `console/docs/json-contract.md`.
- **Tests:**
  - `TestCoordinatorStatusFromStoreNeedsNoKey`;
  - `TestCoordinatorStatusFromStoreWithService`;
  - `TestCoordinatorStatusFromStoreFallsBackToRuns`;
  - `TestCoordinatorStatusFromStoreWithSharedConfigOnly`;
  - `TestReadRepoView`;
  - `TestCliReferenceIsCurrent`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'CoordinatorStatusFromStore' -count=1 && go test ./internal/coordinator -run ReadRepoView -count=1 && go test ./internal/clidoc -count=1`.
- **Depends on:** none.
- **Red step:** implement `--from-store` in a scratch tree by delegating to `coordinatorReader`.
  `TestCoordinatorStatusFromStoreWithService` must then fail on the `coordinatorAppCredential`
  refusal.
  - The fixture must include a run that names a pull request. `coordinatorReader` reaches that
    refusal only for such a run.
- **Reviewer:** standard. It holds no credential. **Owner-run:** no.
- **Commit:** `cli: coordinator status reads the stored view with no key (D364)`.

**T1.2 Console P6, rebuilt on T1.1.** This is coordinator P6 as amended by D364. It replaces P6's
"read through `coordinator status`". Everything else in P6 stands: no new credential, the CSP
unchanged, and `textContent` only.

- **Files:**
  - `console/src/main/ipc.ts`, `console/src/main/spawn.ts` and `console/src/preload/index.ts`;
  - `console/src/source/cli.ts`;
  - `console/src/renderer/entry.ts`, and a new renderer view file;
  - `console/src/shared/sanitize.ts`, if a new text class is needed;
  - the pinning tests in `console/test/unit/`, including `console/test/unit/window.test.ts` (P6's
    CSP mutation);
  - `console/docs/json-contract.md`;
  - the §5.1 table in `docs/design/desktop-console.md`.
- **Tests:**
  - P6's three mutations;
  - the argv `coordinator status --from-store --all --json` pinned exactly, with the `--project`
    word that spawn rule 2 adds to every argv, and an args spec like `budget.show`'s;
  - a variant without `--from-store` is refused.
- **Command:** in `console/`, `npm run typecheck && npm run test:unit -- --maxWorkers=2 --testTimeout=60000`, then `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Depends on:** T1.1.
- **Red step:** allow the variant without `--from-store` in a scratch tree; the pinning test must
  fail.
- **Reviewer:** **opus + mandatory independent review** (a new argv). **Owner-run:** no.
- **Branch:** the console's own release convention applies.
- **Commit:** `console: the project view, read from the stored coordinator view`.

**T1.3 Check 149 (OWNER-RUN).**

- **What it confirms:** U9, end to end. The console already spawns reads that depend on ADC
  (`budget.show`), so this check confirms the new view rather than the mechanism.
- **What the owner does:**
  - types `fugaro coordinator status --from-store --all --json --project <name>`;
  - opens the console's project view.
- **Depends on:** T1.2 and T0.2.

### S2: Bitbucket in the coordinator job, through the service (D361, D362, D365)

**T2.1 A token source for the Bitbucket provider, and the `client_credentials` exchange.**

- **What changes:**
  - `bitbucket.Options` takes a `gitprov.TokenSource`, and a static token is wrapped into one.
  - A new `oauth.go` exchanges a consumer's key and secret for an access token and its expiry.
  - It **drops any refresh token** and never puts the secret into an error.
- **Files:**
  - `internal/gitprov/bitbucket/bitbucket.go`;
  - `internal/gitprov/bitbucket/oauth.go` (new);
  - `internal/gitprov/bitbucket/oauth_test.go` (new);
  - `internal/gitprov/bitbucket/bitbucket_test.go`.
- **Tests:**
  - `TestClientCredentialsExchange`;
  - `TestExchangeErrorCarriesNoSecret`;
  - `TestExchangeDropsRefreshToken`;
  - `TestStaticTokenUnchanged`.
- **Command:** `go test ./internal/gitprov/bitbucket -run 'ClientCredentials|Exchange|StaticToken' -count=1`.
- **Depends on:** none.
- **Red step:** run `TestExchangeErrorCarriesNoSecret` against an exchange that wraps the response
  body; it must fail.
- **Reviewer:** **opus + mandatory independent review**. **Owner-run:** no.
- **Commit:** `gitprov/bitbucket: a token source and the client_credentials exchange`.

**T2.2 The read consumer in the bundle (F21; O2 answered 2026-10-26).**

- **What changes:**
  - A new bundle kind, `bitbucket-read-consumer`, holding a key, a secret and a workspace.
  - `fugaro creds set` accepts it, from stdin only.
  - The second kind, `bitbucket-run-consumer`, and its `repositories` allow-list are parked with
    S3. The owner confirmed two consumers as the design (O2), but building the run kind now would
    be dead code.
- **Files:**
  - `internal/credsvc/bundle.go` and `internal/credsvc/bundle_test.go`;
  - `schemas/creds-bundle.schema.json`;
  - `internal/cli/creds.go` and `internal/cli/creds_test.go`;
  - `website/content/cli-reference.md`, if the help text changes (regenerated).
- **Schema change:** yes. Unknown fields are refused.
- **Tests:**
  - `TestBundleBitbucketReadConsumer`;
  - `TestBundleRefusesRunConsumerUntilS3`, which pins the parked kind as unknown;
  - `TestBundleRefusesConsumerWithoutWorkspace`;
  - `TestCredsSetBitbucketConsumerFromStdin`.
- **Command:** `go test ./internal/credsvc -run Bundle -count=1 && GOFLAGS=-p=2 go test ./internal/cli -run CredsSet -count=1`.
- **Depends on:** T1.1, which also writes the generated `website/content/cli-reference.md`.
- **Red step:** an entry with an unknown field must fail to decode.
- **Reviewer:** **opus + mandatory independent review**. **Owner-run:** no.
- **Commit:** `credsvc: the bundle holds the Bitbucket read consumer`.

**T2.3 The coordinator caller class, and the read mint (D362, F11).**

- **What changes:**
  - `Authenticator` gains a second, separate admission. It verifies a Google ID token whose email
    is **the coordinator account**, and it takes no run identity.
  - The service learns that account's email from its environment, as T2.4 wires it.
  - That admission is valid **only** on a new `POST /v1/bitbucket/read-token`, which mints from
    `bitbucket-read-consumer` only.
  - Every mint writes one audit line with a new purpose. The answer carries the access token and
    its expiry, never a refresh token.
- **The stop switch:** take the read consumer out of the bundle. The caller class is honoured only
  while the bundle holds one.
- **Files:**
  - `internal/credsvc/auth.go` and `internal/credsvc/auth_test.go`;
  - `internal/credsvc/bitbucket.go` (new) and `internal/credsvc/bitbucket_test.go` (new);
  - `internal/credsvc/server.go`;
  - `internal/credsvc/audit.go` and `internal/credsvc/audit_test.go`;
  - `internal/credsvc/wire.go`;
  - `internal/cli/credentials_serve.go` and its test, which read the coordinator account's email
    from the service's environment.
- **Tests:**
  - `TestCoordinatorCallerOnlyOnReadRoute`;
  - `TestCoordinatorCallerRefusedOtherAccounts`;
  - `TestReadMintNeverUsesRunConsumer`;
  - `TestReadMintAuditedOnce`;
  - `TestReadMintFailsClosedUnaudited`;
  - `TestReadMintRefusedWithoutReadConsumer`;
  - `TestReadMintNeverForwardsRefreshToken`;
  - `TestRunIdentityRefusedOnReadRoute`;
  - `TestReadMintTakesNoTokenURLFromCaller`, which pins that the token endpoint is the service's
    own constant;
  - `TestReadMintRefusedWhenStopped`: the global kill switch (`Kills.Stop`) also stops the
    coordinator class, which has no slug;
  - `TestCoordinatorClassEmptyEmailAdmitsNobody`, for the service on with the coordinator off;
  - `TestNewRoutesAuditConsumerKind`: `routeAct` and the route list name the new paths, so a
    refusal before the branch audits the consumer kind, never the App key's name.
- **Command:** `go test ./internal/credsvc -run 'CoordinatorCaller|ReadMint|RunIdentityRefusedOnRead' -count=1 && GOFLAGS=-p=2 go test ./internal/cli -run CredentialsServe -count=1`.
- **Depends on:** T2.1, T2.2 and T2.8 (Check 145 passed, O4).
- **Red step:** run `TestCoordinatorCallerOnlyOnReadRoute` against an admission that returns a
  generic `Identity`; it must fail.
- **Reviewer:** **opus + mandatory independent review** (the front door). **Owner-run:** no.
- **Commit:** `credsvc: the coordinator's caller class and the Bitbucket read mint (D362)`.

**T2.4 Terraform: the coordinator job may call the service, and a Bitbucket-only installation can
deploy it (F23, F24).**

- **What changes:**
  - The coordinator job gets **`FUGARO_COORDINATOR_CREDENTIALS_SERVICE_URL`**, not
    `FUGARO_CREDENTIALS_SERVICE_URL` (F23).
  - Its account gets `roles/run.invoker` on the service. That is a role granted to a new kind of
    principal, so `grantRules` in `internal/infra/tf/cover.go` is updated deliberately.
  - The service learns the coordinator account's email.
  - The `coordinator` validation and the `coordinator_app_key` precondition stop requiring a
    GitHub App when the installation has no GitHub repository (F24).
  - The App key mount becomes conditional on that same fact.
- **Files:**
  - `deploy/terraform/gcp/modules/installation/coordinator.tf`, `credentials.tf` and
    `variables.tf`;
  - `deploy/terraform/gcp/roots/installation/variables.tf` and `main.tf`;
  - `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl`;
  - `internal/infra/firebase.go` (`Coordinator`) and `internal/infra/projectjobs.go`
    (`recordedGitHubAppID`);
  - `internal/cli/init_firebase.go` (`coordinatorJob`, which today forces `DeployJob = false`
    without a GitHub App ID) and its test;
  - `internal/infra/tfvars.go` and `internal/infra/testdata/*.tfvars.json`, where the
    `coordinator` and `credentials_service` objects are serialised;
  - `internal/infra/tf/cover.go` and `internal/infra/tf/cover_golden_test.go`;
  - the regenerated `internal/infra/tf/testdata/golden/installation-*.plan.json`.
- **Tests:** tftest runs for each of these cases:
  - the coordinator on with the service off (no change);
  - the coordinator on with the service on (invoker plus the variable);
  - the service on with the coordinator off (no email);
  - a Bitbucket-only installation with the coordinator on (no App key).

  Also the `internal/infra` tests for `Coordinator`.
- **Command:** `terraform fmt -check -recursive deploy/terraform`, then
  `terraform -chdir=deploy/terraform/gcp/roots/installation init -backend=false -lockfile=readonly`,
  `validate` and `test` (with `TF_DATA_DIR` outside the tree), then `scripts/gen-golden-plans.sh`,
  then `go test -tags terraform ./internal/infra/... -count=1`, then `tflint` and
  `sh deploy/terraform/scan.sh`.
- **Depends on:** T2.3, so that the service accepts the caller before it is wired.
- **Red step:** the Bitbucket-only run must fail against today's validation.
- **Reviewer:** **opus + mandatory independent review** (IAM and the cover table). **Owner-run:**
  no.
- **Release note:** `### For operators`, per installation: `fugaro update`.
- **Commit:** `terraform: the coordinator job may call the credentials service; Bitbucket-only installations`.

**T2.5 The sweep reads Bitbucket through the service.**

- **What changes:**
  - `coordinator.supported` admits Bitbucket when the reader carries a Bitbucket token source.
  - The sweep opens Bitbucket repositories with `bitbucket.ServiceReadTokens`, a new client half,
    whenever `FUGARO_COORDINATOR_CREDENTIALS_SERVICE_URL` is set.
  - It refuses `FUGARO_BITBUCKET_API_URL` on that path (G10).
  - The GitHub sweep is untouched (G8, F23).
  - Without the variable, Bitbucket repositories stay `StaleUnsupported`.
  - A person's local `coordinator status` for a Bitbucket repository still refuses, and the
    refusal points at `--from-store`.
- **Files:**
  - `internal/coordinator/project.go` and `internal/coordinator/project_test.go`;
  - `internal/cli/coordinator.go` (the sweep's wiring);
  - `internal/cli/coordinator_sweep_bitbucket_test.go` (new);
  - `internal/gitprov/bitbucket/service.go` (new) and
    `internal/gitprov/bitbucket/service_test.go` (new).
- **Tests:**
  - `TestSweepReadsBitbucketThroughService`;
  - `TestSweepGitHubUnchangedWithService`;
  - `TestSweepBitbucketUnchangedWithoutService`;
  - `TestSweepRefusesAPIRootOverride`;
  - `TestSweepNeverRevokesBitbucketTokenAtGitHub`: Bitbucket read tokens never enter
    `coordinatorTokens`, whose revoke goes to `github.RevokeInstallationToken`;
  - `TestLocalStatusBitbucketPointsAtFromStore`;
  - `TestServiceReadTokensRedacted`.
- **Command:** `go test ./internal/coordinator -run Bitbucket -count=1 && go test ./internal/gitprov/bitbucket -run ServiceRead -count=1 && GOFLAGS=-p=2 go test ./internal/cli -run 'Sweep|LocalStatusBitbucket' -count=1`.
- **Depends on:** T2.1, T2.3, and T1.1 (they share `internal/cli/coordinator.go`).
- **Red step:** `TestSweepReadsBitbucketThroughService` fails while `supported` returns
  `ErrBitbucket`.
- **Reviewer:** **opus + mandatory independent review**. **Owner-run:** no.
- **Commit:** `coordinator: the sweep reads Bitbucket through the credentials service`.

**T2.6 Runbook: create, rotate, stop and transfer the read consumer (D367).**

- **What it documents:**
  - creating the read consumer under the installing person's account, with read scopes only;
  - that its token acts as that person, read-only, and stops working when the person leaves;
  - whether a consumer must be private, once Check 145 has said (U15);
  - writing them with `fugaro creds set`;
  - **the stop switch**: take the consumer out of the bundle;
  - **rotation**: regenerate the secret, write a new bundle version, confirm a mint;
  - **ownership transfer** when the person leaves: a new owner creates a new consumer and writes
    it, and the old one is deleted.
  - In `docs/git-providers.md`: the coordinator's Bitbucket paragraph (D361, D362), and the App key's
    second holder (D365).
  - The run consumer's scopes come with S3 (T3.5).
- **Files:**
  - `docs/credentials-service/runbook.md`;
  - `docs/git-providers.md`;
  - `internal/cli/docs_bitbucket_runbook_test.go` (new).
- **Tests:** `TestDocsBitbucketRunbook*` checks that the read consumer and its read scopes are
  named, and that the creation, stop, rotation and transfer steps are present.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Depends on:** T2.8, whose results the runbook states (U14, U15).
- **Red step:** the new test fails before the text lands.
- **Reviewer:** **opus + independent review** (a credential procedure). **Owner-run:** no.
- **Commit:** `docs: the Bitbucket read consumer: creation, stop, rotation and ownership transfer`.

**T2.7 The Check 145 script.**

- **What the script does:** `scripts/live-checks/check145.sh` reads the sandbox consumer from a
  file the owner names, and never prints it. It reports:
  - the token lifetime (U1);
  - whether a refresh token is returned, and whether a token can be revoked (U2);
  - whether the token reaches a second workspace (U3);
  - whether the sandbox's plan admits consumers (U4);
  - the seat count before and after (U5);
  - whether IP allowlisting applies (U8). If the sandbox's plan offers no allowlisting, U8 stays
    UNVERIFIED, and T3.4 says so in SECURITY.md;
  - whether the scopes appear in the response (U11);
  - whether two grants return distinct tokens (U12);
  - whether the creator must be an admin, and whether the token reaches admin endpoints (U14);
  - whether the consumer must be private (U15).

  It ends with a `CHECK 145 RESULT` block, like `check90.sh`.
- **Files:** `scripts/live-checks/check145.sh` (new) and `scripts/live-checks/check145_test.sh`
  (new).
- **Tests:** `check145_test.sh` runs against a local fake, with no network, and checks that the
  secret is never echoed.
- **Command:** `bash scripts/live-checks/check145_test.sh`.
- **Depends on:** none.
- **Red step:** the test fails when the script echoes the secret.
- **Reviewer:** **opus + independent review**. **Owner-run:** no (the script is run in T2.8).
- **Commit:** `live-checks: check 145, the Bitbucket consumer facts`.

**T2.8 Check 145 (OWNER-RUN).**

- **What the owner types:** `scripts/live-checks/check145.sh --consumer-file <path> --workspace <sandbox> --second-workspace <sandbox-2>`.
- **Depends on:** T2.7 and T0.2.
- **Gate:** T2.3 and T3.1 do not merge before this check passes.
- **If U3 shows reach beyond the workspace:** D363 goes back to the owner (O4).

**T2.9 The Check 146 script.**

- **What it does:** `scripts/live-checks/check146.sh` runs the sweep on a sandbox installation with
  the service on. It asserts that a Bitbucket repository's view is read rather than
  `StaleUnsupported`, and that the installation's GitHub views are unchanged.
- **Files:** `scripts/live-checks/check146.sh` (new) and `scripts/live-checks/check146_test.sh`
  (new).
- **Command:** `bash scripts/live-checks/check146_test.sh`.
- **Depends on:** T2.4 and T2.5.
- **Red step:** the test fails on a sweep log showing `StaleUnsupported`.
- **Reviewer:** standard. **Owner-run:** no.
- **Commit:** `live-checks: check 146, the coordinator reads Bitbucket`.

**T2.10 Check 146 (OWNER-RUN).**

- **What the owner types:** `scripts/live-checks/check146.sh --installation <sandbox>`.
- **Depends on:** T2.9 and T0.2.

**T2.11 SECURITY.md for S2.**

- **What it says:**
  - the coordinator's caller class at the credentials service;
  - the read consumer's reach: everything the installing person can read in the workspace, or
    beyond it if U3 says so;
  - the dependency on that person's account (D367);
  - the coordinator job as the App key's second holder (D365);
  - the read mint's custody: whoever may run the coordinator job with overrides (O6).
- **Files:**
  - `SECURITY.md`;
  - `website/content/security.md` (generated);
  - `internal/cli/docs_bitbucket_security_test.go` (new).
- **Tests:**
  - `TestDocsBitbucketSecurityCoordinator`;
  - `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|WebsiteSecurity' -count=1`;
  - in `website/`, `npm run build && node --test test/security.mjs`.
- **Depends on:** T2.3 and T2.5.
- **Red step:** the new pin fails before the text lands.
- **Reviewer:** **opus + mandatory independent review**. **Owner-run:** no.
- **Commit:** `SECURITY: the coordinator's Bitbucket read through the credentials service`.

### S3, PARKED: the service mints Bitbucket run tokens (D360, D363, T1)

**Parked by the owner on 2026-10-26 (O1).** Runs keep their per-repository access tokens. S3 is
deferred until a push and pull-request proxy exists (D363(d)), and it is not cancelled.

- No task in this slice may start until the owner unparks it.
- When it returns, it is re-planned against the proxy. The tasks below were written for direct
  minting and are kept as the record of that design and of its review findings.
- **O7's rule binds S3 when it returns:** review-only and artifact runs get **no** write token
  (F33, U16).
- The parked parts are: T3.0–T3.8, Checks 147 and 148, the run consumer (F21), and the SECURITY.md
  statements of T3.4.

**T3.0 The per-repository switch, rendered into the job (F25, G9).**

- **What it adds:** `bitbucket_via_service`, a per-repository field in the **operator's local
  config**, off by default.
  - It reaches the run as a **job-level environment variable that Terraform writes**:
    `FUGARO_BITBUCKET_VIA_SERVICE=1` on that repository's workflow jobs. Nothing is decided at
    launch.
  - Every launcher's run therefore sees the same value, whatever their own local config holds.
  - It is applied with `fugaro update --repo .` in the repository's checkout.
- **Why it is not in the shared config (corrected in review round 3):**
  - A teammate's own local entry wins over the published one (`localcfg.MergeShared`), so the
    switch would not reach every launcher.
  - The shared config is writable by any launcher with `objectAdmin` on it. That is no place for
    an operator's gate (`internal/localcfg/shared.go`).
- **What it is not:** it selects the code path. It is **not** the security boundary. An agent in an
  allow-listed repository can call the run mint itself, whatever the switch says (F29). **The
  allow-list entry is the exposure.**
- **What a launcher's override does:** if a launcher overrides the variable off, that launcher's
  own run fails once T3.6 has dropped the token. That is a denial of service to themselves only.
- **Files:**
  - `internal/localcfg/localcfg.go` (the `Repo` field) and its test;
  - `internal/infra/spec.go` and `internal/infra/spec_test.go`;
  - `deploy/terraform/gcp/modules/workflow/job.tf` and `variables.tf`;
  - `deploy/terraform/gcp/modules/repo/workflows.tf` and `variables.tf`;
  - `deploy/terraform/gcp/roots/repo/variables.tf` and `deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl`;
  - the regenerated `internal/infra/tf/testdata/golden/*`.
- **Tests:**
  - `TestRepoSpecBitbucketViaService`;
  - tftest runs: the switch off (no variable), the switch on (the variable on that repository's
    jobs only), and a GitHub repository (never).
- **Command:** `go test ./internal/localcfg ./internal/infra -run 'BitbucketViaService' -count=1`,
  then in the `repo` root `terraform init -backend=false -lockfile=readonly`, `validate` and
  `test`, then `scripts/gen-golden-plans.sh`, `tflint` and `sh deploy/terraform/scan.sh`.
- **Depends on:** none.
- **Red step:** the "GitHub repository" tftest run fails against a module that sets the variable
  on every job.
- **Reviewer:** **opus + mandatory independent review**. **Owner-run:** no.
- **Commit:** `terraform: a per-repository switch for Bitbucket through the credentials service`.

**T3.1 The run mint, with an allow-list and an audit line.**

- **What it admits:** `POST /v1/bitbucket/access-token` admits exactly what the GitHub mint does:
  a per-repository job, a live run identity, and no repository field.
- **When it mints:** only from `bitbucket-run-consumer`, and only when the job's verified binding
  is a **Bitbucket** repository, in the consumer's workspace, on its `repositories` allow-list.
  - `JobAccount.Repo` is already the canonical `<provider>/<owner>/<name>`.
  - The run mint parses it the way `credsvc/github.go` `mintRepository` does, and it matches
    allow-list entries written in the same canonical form.
- **Otherwise:** it refuses by name, with one audit line.
- **What it never does:** forward a refresh token.
- **Files:**
  - `internal/credsvc/bitbucket.go` and `internal/credsvc/bitbucket_test.go`;
  - `internal/credsvc/server.go` (routing);
  - `internal/credsvc/audit.go` and `internal/credsvc/audit_test.go`.
- **Tests:**
  - `TestRunMintAllowListed`;
  - `TestRunMintRefusesUnlisted`;
  - `TestRunMintRefusesGitHubJobWithSameName`;
  - `TestRunMintNoRepositoryField`;
  - `TestRunMintNeverUsesReadConsumer`;
  - `TestRunMintAuditedOnce`;
  - `TestRunMintSharedJobRefused`;
  - `TestRunMintNeverForwardsRefreshToken`;
  - `TestRunMintTakesNoTokenURLFromCaller`;
  - `TestRunMintRefusedWhenStopped` (global and per repository);
  - `TestCoordinatorRefusedOnRunMint`.
- **Command:** `go test ./internal/credsvc -run 'RunMint|CoordinatorRefusedOnRunMint' -count=1`.
- **Depends on:** T2.3 (same files), T2.8 (Check 145) and O1.
- **Red step:** run `TestRunMintRefusesGitHubJobWithSameName` against a mint that strips the
  provider prefix and matches `<owner>/<name>` against the allow-list; it must fail.
- **Reviewer:** **opus + mandatory independent review**. **Owner-run:** no.
- **Commit:** `credsvc: Bitbucket run tokens, allow-listed and audited (D363)`.

**T3.2 The runner gets its Bitbucket token from the service.**

- **When it applies:** only when `FUGARO_BITBUCKET_VIA_SERVICE=1` (G9).
- **How it opens:**
  - `providers.FromEnv` opens Bitbucket **with no token** when the switch is on. It refuses
    `FUGARO_BITBUCKET_API_URL` (G10) and never reads `FUGARO_BITBUCKET_TOKEN`.
  - The runner then installs `bitbucket.ServiceTokens` in `runner.openProvider`, which holds the
    run identity, the way it installs `github.ServiceTokens` today. That source presents the two
    identity headers.
  - It goes through a **Bitbucket-specific setter**, never `gitprov.ServiceTokenSetter`.
    `openProvider` today installs the GitHub source on any provider that implements that
    interface (F31).
- **Refresh:** the existing `refreshGitAuth` rule carries the new token to the agent.
  - A mint failure at bootstrap halts the run.
  - A failure mid-run keeps the current token, as `refreshGitAuth` already does. That token
    expires at its own time, and the run never falls back to the mounted token.
- **Files:**
  - `internal/gitprov/providers/providers.go`;
  - `internal/gitprov/gitprov.go` (the Bitbucket setter interface);
  - `internal/gitprov/bitbucket/service.go` and `internal/gitprov/bitbucket/service_test.go`;
  - `internal/runner/runner.go`;
  - `internal/runner/git_service_internal_test.go`.
- **Tests:**
  - `TestBitbucketRunTokenFromService`;
  - `TestBitbucketNoFallbackToMountedToken`;
  - `TestRunPathUnchangedWithoutService`;
  - `TestRunPathUnchangedWithSwitchOff`;
  - `TestServicePathRefusesAPIRootOverride`;
  - `TestRefreshGitAuthBitbucketService`;
  - `TestRunnerNeverInstallsGitHubSourceOnBitbucket`;
  - `TestBitbucketRunTokenRedacted`.
- **Command:** `go test ./internal/gitprov/... -run 'BitbucketRunToken|NoFallback|RunPathUnchanged|APIRootOverride' -count=1 && go test -timeout 40m ./internal/runner -run 'RefreshGitAuthBitbucketService|NeverInstallsGitHubSource|BitbucketRunTokenRedacted' -count=1`.
- **Depends on:** T3.1, T2.5 (same `service.go`), T3.0 and O1.
- **Red step:** run `TestBitbucketNoFallbackToMountedToken` against an opener that reads
  `FUGARO_BITBUCKET_TOKEN` first; it must fail.
- **Reviewer:** **opus + mandatory independent review**. **Owner-run:** no.
- **Release note:** `### For operators`, per repository:
  - Add the repository to the allow-list (`fugaro creds set bitbucket-run-consumer`).
  - Then set `bitbucket_via_service` in the operator's local config and run `fugaro update --repo .`
    in the checkout.
  - The switch without an allow-list entry halts the repository's runs.
  - An allow-list entry without the switch still exposes the token (T3.0).
- **Commit:** `runner: Bitbucket run tokens from the credentials service`.

**T3.3 Comment identity and the cross-repository trust paths (F19, F28). HELD on O1.**

- **What it does:** implements O1's chosen option.
- **Its tests hold under every option:**
  - `TestAgentCommentAsPersonNeverSteers`, for the same identity and for a repository that is not
    allow-listed with the person in `followup.trusted`;
  - `TestReviewModeSameRepoPRByConsumerOwner`;
  - `TestPRAuthorIDPathCovered`;
  - `TestNoAccountScopeMarkersPinned`.
- **What it also decides, with O1:** whether the run consumer carries the `account` scope.
  Without that scope, `selfUser` marks every comment `SelfKnown=false`. Then
  `internal/followup/select.go` honours run markers from any author (`MarkersFromAnyone`).
- **Files:**
  - `internal/gitprov/bitbucket/comments.go` and `internal/gitprov/bitbucket/comments_test.go`
    (new; not `bitbucket_test.go`, which T2.1 writes);
  - `internal/followup/trust.go`, `internal/followup/select.go` and their existing tests;
  - `internal/runner/reviewmode.go` and `internal/runner/reviewmode_test.go`.
- **Command:** `go test ./internal/followup ./internal/gitprov/bitbucket -run 'Self|Steer|PRAuthor|NoAccountScope' -count=1 && go test -timeout 40m ./internal/runner -run ReviewModeSameRepo -count=1`.
- **Depends on:** O1.
- **Red step:** `TestAgentCommentAsPersonNeverSteers` fails against a `Self` that counts marked
  comments only.
- **Reviewer:** **opus + mandatory independent review**. **Owner-run:** no.
- **Commit:** `followup: Bitbucket comment identity under a person-owned consumer`.

**T3.4 SECURITY.md and the docs say it plainly (T1).**

- **What the docs must say, in `SECURITY.md` and its hardening advice:**
  - A Bitbucket run token minted by the service is **workspace-wide for the consumer's owner**, for
    **the life of the run plus up to 1–2 hours** (F29).
  - The agent and the launcher can obtain it (F7, F27).
  - With it they can push, comment, approve and merge as that person anywhere in the workspace
    (F20). They can set off any repository's Pipelines with its secured variables and deploy
    identity, and push to any protected branch the person is exempt from (F26).
  - The allow-list decides who receives a token, not what the token reaches.
  - Artifact and review runs in an allow-listed repository also receive a write token, unless O7
    rules otherwise (F33).
  - Advice: keep the owner out of required approvers, merge checks and branch-restriction
    exemptions, and out of deployment environments' permitted users.
- **Sentences to replace:** today's SECURITY.md sentences that S3 makes false. These are §"Narrow
  git credentials" ("On Bitbucket, it is a repository access token, scoped to that one
  repository"), the review-mode paragraph, and the advice to give Fugaro "a repository access
  token on Bitbucket".
- **U8:** if it is still unsettled, SECURITY.md says so.
- **Elsewhere:**
  - `docs/git-providers.md` §"What the agent gets" and §Bitbucket Cloud are updated.
  - `website/content/security.md` is regenerated.
- **Files:**
  - `SECURITY.md`;
  - `website/content/security.md` (generated);
  - `docs/git-providers.md`;
  - `internal/cli/docs_bitbucket_security_test.go` (T2.11 creates it).
- **Tests:**
  - `TestDocsBitbucketSecurity`;
  - `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|WebsiteSecurity' -count=1`;
  - in `website/`, `npm run build && node --test test/security.mjs`.
- **Depends on:** T3.2, T3.5 (it states the scope set), T2.6 (same `docs/git-providers.md`) and
  T2.11 (same `SECURITY.md` and test file).
- **Red step:** `TestDocsBitbucketSecurity` asserts the sentence "workspace-wide for the consumer's
  owner" and fails before the text lands.
- **Reviewer:** **opus + mandatory independent review**. **Owner-run:** no.
- **Commit:** `SECURITY: a Bitbucket run token is workspace-wide for the consumer's owner`.

**T3.5 The minimum run scopes, enforced where possible, and their check (G7).**

- **The scope set:**
  - `repository:write`, which includes read;
  - `pullrequest:write`;
  - `account`, if O1's answer keeps `Self` detection (T3.3).

  Nothing else: no `pipeline`, `webhook`, `runner`, `project` or `*:admin`.
- **Enforcement:** if the token response lists scopes (U11), the service refuses and audits a
  token that carries more.
  - If Check 145 shows that it does not list them, no service-side enforcement is built, and
    `TestRunMintRefusesWiderScopes` is dropped. G7 then rests on Check 147 alone, and SECURITY.md
    says so.
- **Runbook:** the scope sets are added to `docs/credentials-service/runbook.md`.
- **Check 147's script:**
  - runs one sandbox run;
  - proves push, opening a pull request, the draft flag, reviewers and comments with this set;
  - **records the token's reach into a second sandbox repository** (F7, F20), whether the push
    triggered that repository's pipeline (F26), and U10.
- **Files:**
  - `internal/credsvc/bitbucket.go` and `internal/credsvc/bitbucket_test.go`;
  - `docs/credentials-service/runbook.md`;
  - `scripts/live-checks/check147.sh` (new) and `scripts/live-checks/check147_test.sh` (new).
- **Tests:**
  - `TestRunMintRefusesWiderScopes`;
  - `check147_test.sh`;
  - `TestDocsBitbucketRunbook*`, extended with the scope sets.
- **Command:** `go test ./internal/credsvc -run WiderScopes -count=1 && bash scripts/live-checks/check147_test.sh && GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Depends on:** T3.1 (same `bitbucket.go`) and T2.6 (same runbook).
- **Red step:** `TestRunMintRefusesWiderScopes` fails against a mint that ignores scopes.
- **Reviewer:** **opus + mandatory independent review**. **Owner-run:** no.
- **Commit:** `credsvc: the Bitbucket run token's scopes are the minimum`.

**T3.6 The per-repository drop of the job's own `bitbucket-token` (F22, O3). HELD on O1.**

- **What it builds:** a repository's `bitbucket_via_service` switch becomes a per-repository module
  input. When that input is on **and** the owner's flip (`bitbucket_token_service_held`) is set, the repository's workflow job loses its `bitbucket-token` mount and
  accessor.
  - The flip lives in the operator's local config, never in the shared config, for T3.0's reasons.
  - It is applied with `fugaro update --repo .` in the repository's checkout.
  - Before the flip, the repository's image must carry a runner that understands
    `FUGARO_BITBUCKET_VIA_SERVICE` (see Release actions).
- **What stays the same:**
  - The repository's build account keeps its grant (F8).
  - `config.ServiceHeldSecretNames` is not touched, as the rules file requires.
  - The flip is off by default. The owner sets it per repository after Check 147.
- **Files:**
  - `deploy/terraform/gcp/modules/workflow/sa.tf` and `variables.tf`;
  - `deploy/terraform/gcp/modules/repo/workflows.tf` and `variables.tf`;
  - `deploy/terraform/gcp/roots/repo/variables.tf` and
    `deploy/terraform/gcp/roots/repo/tests/repo.tftest.hcl`;
  - `internal/infra/spec.go` and `internal/infra/spec_test.go` (`RepoSpec`: its addresses);
  - `internal/infra/readiness.go`;
  - `deploy/terraform/terraform_test.go`, whose `TestServiceHeldSecretsMatchTheCredentialsService`
    pins `sa.tf`'s expressions;
  - `internal/infra/tf/cover.go`, only if a grant's principal changes;
  - the regenerated `internal/infra/tf/testdata/golden/*`.
- **Tests:** tftest runs for: the switch off; the switch on without the flip; the switch on with the
  flip, where the build grant survives. Also the `internal/infra` spec tests.
- **Command:** as in T2.4, in the `repo` root, plus `go test ./deploy/terraform -run ServiceHeld -count=1`.
- **Depends on:** T3.0 (same module files), T3.1, T2.4 (same golden plans and `cover.go`) and O1.
- **Red step:** the "build grant survives" run fails against a drop that removes it.
- **Reviewer:** **opus + mandatory independent review**. **Owner-run:** the flip.
- **Commit:** `terraform: the per-repository, owner-flipped drop of the job's bitbucket-token`.

**T3.7 Check 147 (OWNER-RUN).**

- **What the owner types:** `scripts/live-checks/check147.sh --installation <sandbox> --repo <sandbox-repo> --second-repo <sandbox-repo-2>`.
- **Depends on:** T3.2, T3.5 and T0.2.

**T3.8 Check 148: the stop, rotation and ownership-transfer drill (OWNER-RUN, D367).**

- **What the owner does:** follows the runbook on a sandbox:
  - takes the read consumer out of the bundle, and confirms a read mint is refused;
  - rotates a secret, and confirms that the old secret is refused **and** whether tokens already
    minted stop working (U13);
  - transfers ownership to a second sandbox account, and confirms a read mint (and, once S3 is
    deployed, a run mint).
- **Depends on:** T2.6, T2.4 and T2.5 (a deployed read mint), and T0.2.

## Parallel execution

**Re-derived 2026-10-26 for the active plan, S0–S2.** S3 is parked, and its edges, levels and
files are kept at the end of this section for when it returns.

**Edges** (from each "Depends on"):

- T1.1 → T1.2 → T1.3
- T1.1 → T2.2
- T2.1, T2.2 and T2.8 → T2.3
- T2.3 → T2.4
- T2.1, T2.3 and T1.1 → T2.5
- T2.8 → T2.6
- T2.3 and T2.5 → T2.11
- T2.4 and T2.5 → T2.9 → T2.10
- T2.7 → T2.8
- T0.2 → every owner-run check

**Levels** (the tasks in one level run concurrently):

| Level | Tasks |
| --- | --- |
| L0 | T0.1, T0.2, T1.1, T2.1, T2.7 |
| L1 | T1.2, T2.2, T2.8 (owner) |
| L2 | T1.3 (owner), T2.3, T2.6 |
| L3 | T2.4, T2.5 |
| L4 | T2.9, T2.11 |
| L5 | T2.10 (owner) |

**Critical path:** T2.7 → T2.8 (Check 145) → T2.3 → T2.4 / T2.5 → T2.9 → T2.10 (Check 146).

- **The long pole is the owner's Check 145.** It is no longer O1, which is answered.
- S1 is off the critical path and ships first. It is the smallest useful slice, and it needs no
  credential.

**Must be sequential, and why:**

- T1.1 → T2.5 share `internal/cli/coordinator.go`.
- T1.1 → T2.2 share the generated `website/content/cli-reference.md`.
- Check 145 comes before the read mint (O4).

**Shared files.** No level has two writers of one file. Recheck this whenever a task's Files line
changes.

| File | Writers (level) |
| --- | --- |
| `internal/cli/coordinator.go` | T1.1 (L0), T2.5 (L3) |
| `website/content/cli-reference.md` | T1.1 (L0), T2.2 (L1) |
| `internal/credsvc/bundle.go`, `schemas/creds-bundle.schema.json`, `internal/cli/creds.go` | T2.2 (L1) |
| `internal/credsvc/bitbucket.go`, `auth.go`, `server.go`, `audit.go`, `wire.go`, `internal/cli/credentials_serve.go` | T2.3 (L2) |
| `internal/gitprov/bitbucket/bitbucket.go`, `bitbucket_test.go`, `oauth.go` | T2.1 (L0) |
| `internal/gitprov/bitbucket/service.go`, `internal/coordinator/project.go` | T2.5 (L3) |
| `docs/credentials-service/runbook.md`, `docs/git-providers.md` | T2.6 (L2) |
| `docs/gcp-live-checklist.md` | T0.2 (L0) |
| `deploy/terraform/gcp/modules/installation/*`, `roots/installation/*`, `internal/infra/firebase.go`, `projectjobs.go`, `tfvars.go`, `internal/cli/init_firebase.go`, `internal/infra/tf/cover.go`, `internal/infra/tf/testdata/golden/*` | T2.4 (L3) |
| `SECURITY.md`, `website/content/security.md`, `internal/cli/docs_bitbucket_security_test.go` | T2.11 (L4) |
| `console/**`, `console/docs/json-contract.md`, `docs/design/desktop-console.md` | T1.2 (L1) |

**Kept for when S3 returns (parked, not scheduled).** These are the edges written for direct
minting:

- T3.0, T3.1 and T2.4 → T3.6
- T2.6 → T3.5
- T2.3 and T2.8 → T3.1
- T3.1, T2.5 and T3.0 → T3.2
- T3.1 → T3.5
- T3.2, T3.5, T2.6 and T2.11 → T3.4
- T3.2 and T3.5 → T3.7
- T2.6, T2.4 and T2.5 → T3.8

When S3 is re-planned against the proxy, its levels and its shared-files rows are derived again.
T3.4 is the second writer of `SECURITY.md` and `docs/git-providers.md`. T3.5 is the second writer
of the runbook. T3.0 and T3.6 write the golden plans and the workflow module.

## Checks this plan takes (Checks 145–154)

| Check | What it measures | Run by | Written by |
| --- | --- | --- | --- |
| 145 | The consumer facts: U1–U5, U8, U11, U12, U14 and U15. **It gates the read mint (O4)** | owner | T0.2 (section), T2.7 (script) |
| 146 | The coordinator sweep reads a Bitbucket repository through the service, and its GitHub views are unchanged | owner | T0.2, T2.9 |
| 147, **parked with S3** | A sandbox run on a minted token works with exactly the minimum scopes. It records the token's reach into a second repository, the pipeline trigger, and U10 | owner | T0.2, T3.5 |
| 148, **parked with S3** | The stop, rotation and ownership-transfer drill, with U13 | owner | reserved |
| 149 | The console shows the stored view on the owner's own machine (U9, end to end) | owner | T0.2 |

Checks 150–154 are reserved and unused.

## Release actions

These go in the `### For operators` section of the release notes, in the PR that ships each part.

- **T2.4**, per installation: `fugaro update`.
- **T2.2 and T2.6**, per installation:
  - **First** run `fugaro update`, so the deployed credentials service is a version that knows
    the new bundle kind. The bundle loader refuses the whole bundle on an unknown key
    (`internal/credsvc/bundle.go`, `ErrBundleUnknownKey`). Writing a consumer into a bundle that
    an older service still reads halts every model call and every GitHub mint.
  - **Then** create the read consumer as the installing person, with read scopes only, and run
    `fugaro creds set bitbucket-read-consumer`.
- **Parked with S3. These are not release actions until S3 returns.**
- **T3.0 and T3.2**, per repository:
  - First rebuild the repository's image, so that its runner knows the service path.
  - Then add the allow-list entry, set `bitbucket_via_service` in the operator's local config,
    and run `fugaro update --repo .` in the checkout.
- **T3.6**, per repository and only when the owner decides: confirm that the repository's image
  carries T3.2's runner, then set `bitbucket_token_service_held`, then run `fugaro update --repo .`
  in the checkout.
