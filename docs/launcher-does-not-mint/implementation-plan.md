# A launcher does not mint a run's identity: implementation plan

**Status: plan only; nothing is implemented; no task is approved.** Each task needs the owner's
explicit go. An answer in [reconciliation.md](reconciliation.md) settles a question; it approves
no task.

This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md), checked
against `origin/main` at `7987acb99`. The answers that shaped it:

- **D3121**: the credentials service issues. Its account gains the minter role on the runs'
  signer, which amends the rule on that account's grants.
- **D3122**: the launcher gets a one-time ticket; the run claims its identity.
- **D3123**: roster service accounts are a class at the issuer; `group:` entries are not, and
  `update` names them first.
- **D3124**: one release; the grant goes only after the issuer answers, and the policy version
  moves so an older CLI is told to upgrade.
- **D3125**: the resident orchestrator asks the issuer, for any repository's run.
- **D3126**: the issuer mints pairing and device tokens.
- **D3127**: operators lose the standing grant; the documents state what they can still reach.
- **D3128**: tasks start after always-a-budget's I1 and keys-through-service's release A. Only
  **C1** and **LM0** may start before them.
- **D3129**: batch-run-identity's C1, BR1, BR2, BR4, BR5 and BR17 wait for LM19.
- **D3130**: the issuer checks that the repository is the installation's; no per-repository list.

Tasks are **C1**, **LM0** to **LM19** and **I1**: 22 runs. LM18 ships one release after the rest.

## Conventions every part follows

- `CLAUDE.md`, `.claude/rules/credentials-service.md` and `.claude/rules/terraform.md` bind every
  task; they are not restated here. Every task under `internal/credsvc`, `internal/creds`,
  `deploy/terraform` or `internal/infra` is security-sensitive: an independent review before
  merge, never an in-loop `ship` alone.
- **Always:** targeted tests, named in the task; `go build ./... && gofmt -l . && go vet ./...`;
  hermetic tests only (`internal/gcpfake`, `internal/testutil`, `t.TempDir()`); once a caller is
  verified, every refusal writes exactly one audit line through `internal/credsvc/audit.go`'s
  writer, which no task here edits.
- **Ask first:** a new dependency; a grant not listed in LM11 or LM12; any change to what a claim
  means; anything in `.github/workflows/`.
- **Never:** a confirmation step (the no-prompt policy); a secret-shaped literal in a test, a
  fixture or a document; a live cloud action by an agent; a route that takes a caller-chosen uid
  or claim set; a second minter row for anything but the credentials account.
- **A field never changes meaning.** `rb` stays the launcher's self-asserted name (R11). The
  execution variable that carries the ticket has a new name; `runner.BudgetTokenEnv` keeps
  meaning "the seal key" until LM18 deletes it.
- **Main stays releasable at every merge.** Until I1, the service's identity routes answer the
  contract's "not served" refusal, the CLI treats that as an installation not yet updated and
  signs locally (R7), and the runner sees no ticket. The people's grant is removed by LM14 alone.
- Live checks are run by the owner, from one script each. No agent performs any step of one.

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| No route signs a uid or a claim set the caller chose | C1 (the wire has no such field), LM2, LM4, I1's cross-class test |
| A ticket is claimed once; the second claim is refused and audited | LM2, I1 |
| A re-issue for a run on another caller's record is refused | LM1, I1 |
| The claim comes from the job account on the record (F12 for a shared job) | LM2 |
| The issuance record is unwritable and unreadable with any run's token | LM5 |
| The people's row and the orchestrator's go only after the issuer answers; a failed probe leaves them and says why | LM14 |
| An installation at the previous release converges with one `fugaro update` and nothing else | LM14 |
| A run in flight keeps running through the update | LM14 (`TestUpdateLeavesARunInFlightRunning`) |
| An older CLI is told to upgrade, by its own refusal | LM14 |
| A new CLI on an installation not yet updated still launches | LM6, I1 |
| An execution created before the update still starts | LM7 |
| The issuer unreachable: the launch refuses with the fix; the run retries, then halts | LM6, LM7 |
| The plan a reviewer reads names the issuer's row by account and role | LM11, LM13 (`issuerMinterGrant`) |
| No command waits for input, with stdin closed | LM14, LM10 |

## Slices and tasks

Slice A (C1, LM0) can go before the plans D3128 waits on. Slice B is the issuer, slice C the
launcher and the runner, slice D Terraform and the update, slice E the documents and the live
check. LM18 is the removal one release later.

### C1: the identity wire contract

- Acceptance: one package declares the four routes' paths, request and response bodies, refusal
  codes (among them "not served"), the ticket's execution variable name and the issuance record's
  shape; the service mounts the four routes on stubs that answer "not served"; the wire decodes
  strictly and has no field for a uid or a free claim set.
- Files: `internal/identitywire/wire.go` (new), `internal/identitywire/wire_test.go` (new),
  `internal/credsvc/identity_config.go` (new: the service's identity configuration and the
  issuance store's interface), `internal/credsvc/identity_stub.go` (new, stub),
  `internal/credsvc/server.go` (mounts the stubs), `docs/launcher-does-not-mint/contract.md` (new).
- Interfaces: produces everything LM1–LM7, LM10, LM13 and LM19 build against. Consumes nothing.
- Tests: `TestIdentityWireDecodesStrictly`, `TestIdentityWireHasNoCallerChosenIdentity`,
  `TestIdentityRoutesAnswerNotServed`.
- Command: `go test ./internal/identitywire/ ./internal/credsvc/ -run 'IdentityWire|IdentityRoutes' -count=1`
- Red step: `TestIdentityRoutesAnswerNotServed` fails with 404 before the mount.
- Depends on: none.
- Commit: `identity: the wire contract (C1)`
- Reviewer tier: opus, strictest rigor, and an independent review (`fugaro review --pr N`) before
  any task is written against it.

### LM0: Check 840, as a script

- Acceptance: one script the owner runs prints what a Cloud Run execution's metadata identity
  token carries, whether `run.executions.get` returns an execution's overrides to a launcher, and
  whether one custom token exchanges twice (U1, U2, U3); it changes nothing it does not remove.
- Files: `scripts/live-checks/check-840.sh` (new), `scripts/live-checks/check-840_test.sh` (new),
  `docs/gcp-live-checklist.md` (the section for 840).
- Interfaces: none.
- Tests: `check-840_test.sh`; `go test ./scripts/ -run LiveChecks -count=1`.
- Command: `bash scripts/live-checks/check-840_test.sh`
- Red step: the scripts test fails on a check the checklist names with no script.
- Depends on: none.
- Commit: `checks: Check 840, what an execution can prove (LM0)`
- Reviewer tier: standard.
- Owner-run: `scripts/live-checks/check-840.sh` against the development installation.

### LM1: the ticket route and the issuance record

- Acceptance: a verified caller gets a ticket for a slug and run after every check in R4; the
  record is written once; another caller's ask for the same run is refused; the same caller's ask
  before a claim replaces the ticket (R6); every refusal is one audit line.
- Files: `internal/credsvc/identity_ticket.go` (new), `internal/credsvc/identity_record.go` (new),
  `internal/credsvc/identity_ticket_test.go` (new), `internal/credsvc/identity_record_test.go` (new).
- Interfaces: consumes C1 (wire, store interface, caller type). Produces the real handler and the
  real store.
- Tests: `TestTicketIssuedToRosterCaller`, `TestTicketRefusedForAnotherCallersRun`,
  `TestTicketReplacedBeforeClaim`, `TestTicketRefusedAfterClaimOrOutcome`,
  `TestTicketRefusesUnknownRepositoryAndForeignAccount`, `TestTicketExpiryBounded`.
- Command: `go test ./internal/credsvc/ -run 'Ticket' -count=1`
- Red step: `TestTicketRefusedForAnotherCallersRun` fails against a store with no caller on it.
- Depends on: C1 (the wire and the store's interface). **The graph draws no edge for this one**:
  its check admits an edge onto a contract only from the consumer side of an edge the contract
  broke, and LM1 is the provider side of four. The graph therefore shows LM1 at level 0; it is
  briefed only after C1 has merged, like the rest of slice B.
- Commit: `credsvc: the identity ticket and its record (LM1)`
- Reviewer tier: opus, independent review.

### LM2: the claim route and the signing

- Acceptance: a job's Google ID token plus an unclaimed ticket, from the account on the record,
  yields one custom token signed as the runs' signer with the claims on the record; a second
  claim, a wrong account, an expired or unknown ticket are each refused with one audit line.
- Files: `internal/credsvc/identity_claim.go` (new), `internal/credsvc/identity_sign.go` (new),
  `internal/credsvc/identity_claim_test.go` (new).
- Interfaces: consumes C1 and `token.Signer`, `token.MintAt`. Produces the real claim handler and
  the service's signer (the service's own credentials through `token.IAMSigner`).
- Tests: `TestClaimSignsTheRecordedClaims`, `TestClaimIsOnce`, `TestClaimRefusesAnotherJobAccount`,
  `TestClaimRefusesExpiredTicket`, `TestClaimAnsweredAheadOfRunIdentity`.
- Command: `go test ./internal/credsvc/ -run 'Claim' -count=1`
- Red step: `TestClaimIsOnce` fails when the store does not mark the claim.
- Depends on: C1 (the wire and the store's interface; LM1's real store is not needed to build it).
- Commit: `credsvc: a run claims its identity once (LM2)`
- Reviewer tier: opus, independent review.

### LM3: the caller classes

- Acceptance: the ticket route admits a roster person, a roster service account and the
  orchestrator's account, each by its own verified Google ID token, and names the class on the
  record; a `group:` address, an unlisted account and a job's account are refused.
- Files: `internal/credsvc/identity_callers.go` (new), `internal/credsvc/auth.go`
  (the service-account launcher class beside `VerifyLauncher`),
  `internal/credsvc/identity_callers_test.go` (new).
- Interfaces: consumes C1 (the caller type) and `VerifyLauncher`, `VerifyOrchestrator`.
- Tests: `TestCallerClassesAdmitPersonServiceAccountOrchestrator`,
  `TestCallerRefusesJobAccountAndUnlisted`, `TestServiceAccountLauncherIsItsOwnClass`.
- Command: `go test ./internal/credsvc/ -run 'Caller|ServiceAccountLauncher' -count=1`
- Red step: `TestServiceAccountLauncherIsItsOwnClass` fails: `NewLaunchers` refuses the address.
- Depends on: C1.
- Commit: `credsvc: who may ask for a ticket (LM3)`
- Reviewer tier: opus, independent review.

### LM4: the pairing and device routes

- Acceptance: a verified roster person gets a pairing token, and a device token for the pairing
  they completed; the service derives the uid and the claims; no field of the request reaches
  either (R14).
- Files: `internal/credsvc/identity_pair.go` (new), `internal/credsvc/identity_pair_test.go` (new).
- Interfaces: consumes C1, `token.MintPairing`, `token.MintDevice`, `token.Signer`.
- Tests: `TestPairingTokenForVerifiedPerson`, `TestDeviceTokenTakesNoCallerClaim`,
  `TestPairRoutesRefuseServiceAccounts`.
- Command: `go test ./internal/credsvc/ -run 'Pairing|DeviceToken|PairRoutes' -count=1`
- Red step: `TestDeviceTokenTakesNoCallerClaim` fails on a body that carries a uid.
- Depends on: C1.
- Commit: `credsvc: pairing and device tokens come from the service (LM4)`
- Reviewer tier: opus, independent review.

### LM5: the rules keep the record the service's

- Acceptance: the generated rules refuse every read and write of the issuance record's path to
  every signed-in identity; every path that existed keeps its rule byte for byte.
- Files: `internal/budget/rules/rules.go`, `internal/budget/rules/rules_test.go`,
  `internal/budget/rules/testdata/` (the next generation's file).
- Interfaces: consumes C1 (the record's path).
- Tests: `TestRulesRefuseTheIssuanceRecord`, `TestRulesPreexistingPathsUnchanged`.
- Command: `go test ./internal/budget/rules/ -run 'Rules' -count=1`
- Red step: `TestRulesRefuseTheIssuanceRecord` fails: the path falls under a run's readable node.
- Depends on: C1.
- Commit: `rules: the issuance record is the service's alone (LM5)`
- Reviewer tier: opus, independent review.

### LM6: the launcher asks the issuer

- Acceptance: `launchRun` gets a ticket from the service and puts it on the execution under the
  contract's variable; no token is signed and no object is written. Where the service answers
  "not served", the launch signs locally as before (R7). Where the service is unreachable or
  refuses, the launch fails with the reason and the fix after a bounded retry (R1).
- Files: `internal/cli/launch_identity.go` (new), `internal/cli/launch_budget.go`,
  `internal/cli/run.go`, `internal/cli/launch_identity_test.go` (new).
- Interfaces: consumes C1. Produces the CLI's issuer client, which LM9 calls.
- Tests: `TestLaunchAsksTheIssuerAndSignsNothing`, `TestLaunchBridgesWhenRouteNotServed`,
  `TestLaunchRefusesWithTheFixWhenIssuerUnreachable`, `TestLaunchRefusalNamesAnotherCallersRun`.
- Command: `go test ./internal/cli/ -run 'LaunchAsksTheIssuer|LaunchBridges|LaunchRefus' -count=1`
- Red step: `TestLaunchAsksTheIssuerAndSignsNothing` fails: the fake IAM sees a `signJwt` call.
- Depends on: C1. External: always-a-budget's AB4 merged (F17).
- Commit: `cli: a launch asks for a ticket (LM6)`
- Reviewer tier: opus, independent review.

### LM7: the runner claims

- Acceptance: a run whose execution carries a ticket claims its identity from the service with
  the job's own ID token and exchanges it; a refused claim halts the run with a message that says
  the ticket was already claimed; an unreachable service is retried for a bounded time, then the
  run halts; an execution that carries the old marker opens the sealed object as before (R7).
- Files: `internal/runner/identity_claim.go` (new), `internal/runner/budget_session.go`,
  `internal/runner/projectpolicy.go`, `internal/runner/identity_claim_test.go` (new).
- Interfaces: consumes C1 and `metadataIDToken`.
- Tests: `TestRunnerClaimsItsIdentity`, `TestRunnerHaltsWhenTicketAlreadyClaimed`,
  `TestRunnerRetriesThenHaltsWhenIssuerUnreachable`, `TestRunnerStillOpensASealedToken`.
- Command: `go test ./internal/runner/ -run 'RunnerClaims|RunnerHalts|RunnerRetries|RunnerStillOpens' -count=1`
- Red step: `TestRunnerClaimsItsIdentity` fails: the runner looks for an object.
- Depends on: C1. External: always-a-budget's AB1a merged; U4 checked as the first step.
- Commit: `runner: a run claims its identity (LM7)`
- Reviewer tier: opus, independent review.

### LM8: a new launcher refuses an image that cannot claim

- Acceptance: a launch that holds a ticket refuses a job image older than the release that
  claims, naming the image refresh the update performs; a launch on the bridge path is not refused.
- Files: `internal/cli/recipes_skew.go`, `internal/cli/recipes_skew_test.go`.
- Interfaces: a constant beside `serviceAuthSince`.
- Tests: `TestIdentitySinceRefusesAnOlderImage`.
- Command: `go test ./internal/cli/ -run 'IdentitySince' -count=1`
- Red step: the test fails with no gate.
- Depends on: none.
- Commit: `cli: an image that cannot claim is refused at launch (LM8)`
- Reviewer tier: standard.

### LM9: the resident orchestrator asks the issuer

- Acceptance: `residentLauncher.Launch` presents its own account's identity token to the issuer
  and launches on the ticket; it makes no `signJwt` call.
- Files: `internal/cli/orchestrator_launcher.go`, `internal/cli/orchestrator_serve.go`,
  `internal/cli/orchestrator_launcher_test.go`.
- Interfaces: consumes LM6's client.
- Tests: `TestResidentLauncherAsksTheIssuer`.
- Command: `go test ./internal/cli/ -run 'ResidentLauncherAsksTheIssuer' -count=1`
- Red step: the fake IAM sees a `signJwt` call from the resident launcher.
- Depends on: LM6 (it calls the client LM6 writes and `launchRun`'s new path).
- Commit: `orchestrator: the resident launcher asks for a ticket (LM9)`
- Reviewer tier: opus, independent review.

### LM10: `fugaro pair` asks the issuer

- Acceptance: `fugaro pair` gets the pairing token and the device token from the service; where
  the service answers "not served" it signs locally as before; its refusals name the fix and it
  never waits on input.
- Files: `internal/cli/pair.go`, `internal/cli/pair_identity.go` (new),
  `internal/cli/pair_identity_test.go` (new).
- Interfaces: consumes C1.
- Tests: `TestPairAsksTheIssuer`, `TestPairBridgesWhenRouteNotServed`,
  `TestPairRefusalNamesTheFix`.
- Command: `go test ./internal/cli/ -run 'PairAsksTheIssuer|PairBridges|PairRefusalNames' -count=1`
- Red step: `TestPairAsksTheIssuer` fails: the fake IAM sees `signJwt`.
- Depends on: C1.
- Commit: `pair: the service mints the pairing and device tokens (LM10)`
- Reviewer tier: opus, independent review.

### LM11: the Firebase root can carry the issuer's row

- Acceptance: with an issuer account given, the module plans one minter row for it on the runs'
  signer, written with literals; a switch, on by default, gates the people's row; with the
  defaults the plan is unchanged. The rule file's sentence on the service account's grants names
  the new row (F7).
- Files: `deploy/terraform/gcp/modules/firebase/iam.tf`,
  `deploy/terraform/gcp/modules/firebase/variables.tf`,
  `deploy/terraform/gcp/roots/firebase/main.tf`, `deploy/terraform/gcp/roots/firebase/variables.tf`,
  `deploy/terraform/gcp/roots/firebase/tests/firebase.tftest.hcl`,
  `deploy/terraform/terraform_test.go`, `.claude/rules/credentials-service.md`.
- Interfaces: produces the two variables LM13 writes.
- Tests: `TestSignerIAMIsMinterOnly`, `TestIssuerMinterIsOneSpelling` (new),
  `TestPeopleMinterIsBehindTheSwitch` (new).
- Command: `go test ./deploy/terraform/ -run 'SignerIAM|IssuerMinter|PeopleMinter' -count=1 && terraform fmt -check -recursive deploy/terraform`
- Red step: `TestIssuerMinterIsOneSpelling` fails with no row.
- Depends on: none.
- Commit: `terraform(firebase): the issuer's minter row, the people's behind a switch (LM11)`
- Reviewer tier: opus, independent review.

### LM12: the installation root: the orchestrator's row and the service's configuration

- Acceptance: the orchestrator's minter row sits behind the same switch, on by default; the
  credentials service receives the signer account and the roster's service accounts in its
  environment; each roster service account holds the invoker row; with the defaults the plan is
  unchanged.
- Files: `deploy/terraform/gcp/modules/installation/orchestrator.tf`,
  `deploy/terraform/gcp/modules/installation/credentials.tf`,
  `deploy/terraform/gcp/modules/installation/iam.tf`,
  `deploy/terraform/gcp/modules/installation/variables.tf`,
  `deploy/terraform/gcp/roots/installation/variables.tf`,
  `deploy/terraform/gcp/modules/installation/orchestrator_test.go`.
- Interfaces: produces the variables LM13 writes.
- Tests: the module's own tests; `TestOrchestratorMinterIsBehindTheSwitch` (new).
- Command: `go test ./deploy/terraform/gcp/modules/installation/ -run 'Orchestrator' -count=1`
- Red step: the new test fails with the row unconditional.
- Depends on: none.
- Commit: `terraform(installation): the orchestrator's minter row behind the switch (LM12)`
- Reviewer tier: opus, independent review.

### LM13: `update` writes the issuer's row and configures the service

- Acceptance: an update passes the credentials account to the Firebase root, so the issuer's row
  is planned, and leaves the switch on; the plan cover names the row by account and role; the
  service reads its signer account and the service-account roster into C1's configuration; the
  roster's `serviceAccount:` entries are rendered, its `group:` entries are not.
- Files: `internal/infra/firebase.go`, `internal/infra/launchers.go`,
  `internal/infra/tf/cover.go`, `internal/infra/tf/cover_grants_test.go`,
  `internal/cli/credentials_serve.go`, and the golden plans (`scripts/gen-golden-plans.sh`).
- Interfaces: consumes LM11's and LM12's variables and C1's configuration.
- Tests: `TestIssuerMinterGrantIsCovered`, `TestLauncherServiceAccountsRendered`,
  `TestCredentialsServeReadsTheSigner`.
- Command: `go test ./internal/infra/... ./internal/cli/ -run 'IssuerMinterGrant|LauncherServiceAccounts|CredentialsServeReadsTheSigner' -count=1`
- Red step: `TestIssuerMinterGrantIsCovered` fails: the cover reports a grant it does not carry.
- Depends on: LM11 and LM12 (the golden plans are generated from their variables), C1.
- Commit: `infra: update plans the issuer's row (LM13)`
- Reviewer tier: opus, independent review.

### I1: wire the issuer in

- Acceptance: the stub file is gone; the service serves the four routes for real; one test runs a
  launch through the service to a session: ticket, claim, exchange. In the same test a second
  person's ask for that run is refused, a copied ticket's second claim is refused and the losing
  run halts, a pairing completes, and no token of one kind is accepted on another kind's route.
- Files: `internal/credsvc/identity_stub.go` (deleted), `internal/credsvc/identity_mount.go` (new:
  the handlers `server.go` already mounts, now bound to the real ones),
  `internal/credsvc/identity_e2e_test.go` (new), `internal/cli/launch_identity_e2e_test.go` (new).
  It does not edit `server.go`, which is the contract's.
- Interfaces: consumes every side C1 separated.
- Tests: `TestIdentityEndToEnd`, `TestIdentityCrossClassRefused`, `TestLaunchToSessionThroughTheIssuer`.
- Command: `go test ./internal/credsvc/ ./internal/cli/ -run 'IdentityEndToEnd|IdentityCrossClass|LaunchToSessionThroughTheIssuer' -count=1`
- Red step: `TestIdentityEndToEnd` fails with "not served" while the stubs are mounted.
- Depends on: LM1, LM2, LM3, LM4, LM5, LM6, LM7, LM10 and LM13 (it runs all of them together,
  on the configured service).
- Commit: `identity: the issuer is wired in (I1)`
- Reviewer tier: opus, independent review.

### LM14: `update` moves the grant

- Acceptance: a stage after the credentials service's probes the ticket route; when it answers,
  the stage prints R10's lines, applies both roots with the switch off, and the people's and the
  orchestrator's rows are gone; when it does not, the rows stay and one line says why. The
  published policy's version moves to the next one, shape-free, so a CLI of the release behind
  refuses with `SkewSentence`. An installation at the previous release converges with this one
  command, with a run in flight and with stdin closed.
- Files: `internal/cli/init_identity.go` (new), `internal/cli/init_stages.go`,
  `internal/cli/update_in_flight.go`, `internal/projectpolicy/version.go`,
  `internal/projectpolicy/migrate.go`, and their tests (`internal/cli/init_identity_test.go` new,
  `internal/cli/update_in_flight_test.go`, `internal/projectpolicy/migrate_test.go`).
- Interfaces: consumes LM13's values and C1's probe shape.
- Tests: `TestUpdateMovesTheGrantOnlyAfterTheIssuerAnswers`, `TestUpdateKeepsTheGrantAndSaysWhy`,
  `TestUpdateNamesGroupLaunchersFirst`, `TestUpdateFromThePreviousReleaseConverges`,
  `TestUpdateLeavesARunInFlightRunning`, `TestVersionSinceCoversEveryVersion`,
  `TestOlderCLIIsToldToUpgrade`.
- Command: `go test ./internal/cli/ ./internal/projectpolicy/ -run 'UpdateMovesTheGrant|UpdateKeepsTheGrant|UpdateNamesGroupLaunchers|UpdateFromThePreviousRelease|UpdateLeavesARunInFlightRunning|VersionSince|OlderCLIIsTold' -count=1`
- Red step: `TestUpdateMovesTheGrantOnlyAfterTheIssuerAnswers` fails: the people's row is in the
  final plan.
- Depends on: I1 (the stage's test launches through the real issuer after the move), LM13 (the
  switch it turns), LM9 (the orchestrator's row goes with the people's), LM8 (a launch after the
  move must refuse an image that cannot claim).
- Commit: `update: the minter grant moves to the service (LM14)`
- Reviewer tier: opus, independent review.

### LM15: doctor reads the signers against the new design

- Acceptance: `fugaro doctor` reports the credentials account as the one designed signer of the
  runs' identity; a person or the orchestrator still holding the row, an operator's path through
  the deployer where the two projects are one, and a `group:` roster entry are each a finding
  with its fix (D3127).
- Files: `internal/infra/tokensigners.go`, `internal/cli/doctor_signers.go`, and their tests.
- Interfaces: consumes the account and role LM13 writes.
- Tests: `TestDoctorNamesTheIssuerAsTheDesignedSigner`, `TestDoctorFlagsAPersonWhoStillSigns`,
  `TestDoctorFlagsAGroupLauncher`.
- Command: `go test ./internal/infra/ ./internal/cli/ -run 'DoctorNamesTheIssuer|DoctorFlagsAPerson|DoctorFlagsAGroup' -count=1`
- Red step: `TestDoctorNamesTheIssuerAsTheDesignedSigner` fails: the account is unclassified.
- Depends on: LM13 (the classification names the row it writes).
- Commit: `doctor: the service is the designed signer (LM15)`
- Reviewer tier: opus, independent review.

### LM16: the documents

- Acceptance: SECURITY.md and its website mirror say a launcher may launch any repository and may
  not mint; they state the residual of R5, the operators' reach (D3127), the bearer token (O5)
  and what the issuer is a single point of; `budget-token-channel.md`, `roles.md` and the
  orchestrator design say the same; the release notes' `### For operators` is "run
  `fugaro update`" plus what it will say (R10).
- Files: `SECURITY.md`, `website/content/security.md`, `docs/design/budget-token-channel.md`,
  `docs/design/roles.md`, `docs/mobile/design/always-on-orchestrator.md`, and the release's notes
  under `docs/releases/`.
- Interfaces: none.
- Tests: the Docs filter and the website check.
- Command: `go test ./internal/cli/ -run Docs -count=1 && (cd website && npm run check)`
- Red step: `docs_website_security_test.go` fails on the first edit until the mirror matches.
- Depends on: I1 and LM14 (it states what is true after them).
- Commit: `docs: a launcher launches, the service mints (LM16)`
- Reviewer tier: opus, independent review (SECURITY.md).

### LM17: Check 841, as a script

- Acceptance: one script the owner runs on a live installation at the previous release: update
  with a run in flight; that run finishes; a new launch gets a session; a second account's ask
  for that run is refused; a copied ticket loses; an older CLI prints the upgrade sentence;
  `fugaro pair` pairs.
- Files: `scripts/live-checks/check-841.sh` (new), `scripts/live-checks/check-841_test.sh` (new),
  `docs/gcp-live-checklist.md`.
- Tests: `check-841_test.sh`.
- Command: `bash scripts/live-checks/check-841_test.sh`
- Red step: the scripts test fails on a check with no script.
- Depends on: LM14.
- Commit: `checks: Check 841, the grant moves and nothing stops (LM17)`
- Reviewer tier: standard.
- Owner-run: `scripts/live-checks/check-841.sh`, before the release is cut.

### LM18: remove the bridge (one release later)

- Acceptance: the CLI no longer builds a signer; the runner no longer opens a sealed object;
  `seal.go`, `object.go`, the `tokens/` prefix and `runner.BudgetTokenEnv` are gone; the Firebase
  module's people row and its switch are deleted; no caller of `token.IAMSigner` is outside the
  service.
- Files: `internal/cli/launch_budget.go`, `internal/cli/pair.go`,
  `internal/runner/budget_session.go`, `internal/budget/token/seal.go` and `object.go` (deleted),
  `deploy/terraform/gcp/modules/firebase/iam.tf`. More than five files: split when it is briefed.
- Tests: `TestNoSignerOutsideTheService` (new, a source census).
- Command: `go test ./internal/cli/ ./internal/runner/ ./internal/budget/... -run 'NoSignerOutsideTheService|Budget' -count=1`
- Red step: the census fails while `(*cloudEnv).tokenSigner` exists.
- Depends on: LM14, and one released version between them. Not before LM19 has settled what
  Batch does with the seal.
- Commit: `identity: the local signer and the seal are removed (LM18)`
- Reviewer tier: opus, independent review.

### LM19: amend batch-run-identity (documents)

- Acceptance: a documents pull request re-plans that plan's release as a VM-attested claim on
  C1: its D3101 and D3108 with their new object, its C1, BR1, BR2, BR4, BR5 and BR17 rewritten or
  dropped, its D3109 settled against D3123, each amended answer marked and put to the owner.
- Files: `docs/batch-run-identity/reconciliation.md`, `implementation-plan.md`,
  `implementation-plan.graph.json`, `README.md`.
- Tests: the Docs filter; `fugaro plan graph --plan docs/batch-run-identity/implementation-plan.md`.
- Command: `go test ./internal/cli/ -run Docs -count=1`
- Red step: none; a documents task.
- Depends on: C1 merged (it is written against the contract's document). **The graph draws no
  edge for this one either**, for the same reason as LM1: it is a consumer of the contract's
  text, not one side of an edge the contract broke. Held by D3128 like every task but C1 and LM0
  unless the owner says otherwise (O10).
- Commit: `docs(batch-run-identity): the release is a claim (LM19)`
- Reviewer tier: opus, independent review.

## Parallel execution

| Level | Tasks |
| --- | --- |
| 0 | C1, LM0, LM8, LM11, LM12 |
| 1 | LM1, LM2, LM3, LM4, LM5, LM6, LM7, LM10, LM13, LM19 |
| 2 | I1, LM9, LM15 |
| 3 | LM14 |
| 4 | LM16, LM17 |
| after a release | LM18 |

The graph read reports five levels and puts LM1 and LM19 at level 0 and LM18 at level 4: it has
no edge for the first two (see their Depends lines) and no way to say "one release later". The
table above is the order to launch in.

**Critical path**, as the read printed it: `C1 -> LM10 -> I1 -> LM14 -> LM16`, five tasks. Every
path through slice B or C to I1 is as long. LM14 is sequential behind I1 because it removes the
grant, and nothing may remove it before a launch has gone through the issuer in a test. LM18
waits a release by design.

**External gates (D3128):** everything but C1 and LM0 waits for always-a-budget's I1 and
keys-through-service's release A.

### Shared files

| File | Writers | Levels |
| --- | --- | --- |
| `internal/credsvc/server.go` | C1 | 0 |
| `internal/credsvc/identity_mount.go` | I1 | 2 |
| `internal/credsvc/identity_stub.go` | C1 (creates), I1 (deletes) | 0, 2 |
| `internal/credsvc/auth.go` | LM3 | 1 |
| `internal/cli/launch_budget.go` | LM6, LM18 | 1, after a release |
| `internal/cli/run.go` | LM6 | 1 |
| `internal/cli/pair.go` | LM10, LM18 | 1, after a release |
| `internal/cli/credentials_serve.go` | LM13 | 1 |
| `internal/cli/recipes_skew.go` | LM8 | 0 |
| `internal/runner/budget_session.go` | LM7, LM18 | 1, after a release |
| `deploy/terraform/gcp/modules/firebase/iam.tf` | LM11, LM18 | 0, after a release |
| `internal/infra/tokensigners.go` | LM15 | 2 |
| `docs/gcp-live-checklist.md` | LM0, LM17 | 0, 4 |
| `internal/projectpolicy/version.go` | LM14 | 3 |

No level holds two writers of one file. C1's own files (`internal/identitywire/*`,
`identity_config.go`, `server.go`'s mount, `contract.md`) are named by no other task; the stub
file is named by I1 alone, which deletes it.

## The dependencies the plan broke and kept

The pass is on for this repository (`planning.decoupling` is `true`, the default).

**Broken, all by C1, all rejoined by I1** (eight; "A → B" reads "A waited for B"):

- LM2 → LM1: the claim needs the record's shape and the store's interface, not LM1's store.
- LM3 → LM1: the classes return the caller the record names; they need its type, not the route.
- LM5 → LM1: the rules need the record's path.
- LM6 → LM1: the launcher needs the ticket route's shape, not a running route.
- LM7 → LM2: the runner needs the claim's shape.
- LM4 → LM2: the pairing routes need a signer's interface, not LM2's signer.
- LM13 → LM2: the update fills the configuration's fields, not the code that reads them.
- LM10 → LM4: `fugaro pair` needs the pairing routes' shape.

**Kept** (ten):

- LM9 → LM6: it calls the client LM6 writes; a contract for one function in the same package
  would be the function.
- LM13 → LM11 and LM13 → LM12: the golden plans are generated from their variables.
- LM14 → I1, LM13, LM9 and LM8: it removes the grant; each is something that must work, not a
  shape.
- LM15 → LM13: it classifies the row LM13 writes.
- LM16 → I1 and LM16 → LM14: it states what is true after them.
- LM17 → LM14 and LM18 → LM14: one proves the move, the other needs a released version after it.

**The gain**, as the read printed it: `gain: 8 broken, 10 kept, 2 added; before 6 tasks, weight
11; after 5 tasks, weight 9.5; improves`. The critical path goes from six tasks to five.

**Contract changes anticipated:**

- **R-C1-execution**: if Check 840 finds an execution-level fact in the identity token, the claim
  binds to it (R15). Reaches LM2 and LM7.
- **R-C1-batch**: if LM19 finds the Batch claim needs a field (the job's unique id, the VM's
  token), the wire gains it. Reaches LM1, LM2 and LM7.

## The estimate

`fugaro plan graph --plan docs/launcher-does-not-mint/implementation-plan.md` printed:

```
ready in 270–1080 min, no history yet for 22 tasks (22 tasks; seeds for 22; from the seeds: no history is cached on this machine yet)
```

The estimate is informational and reserves no budget. It is what the tasks' tiers and this
machine's history predict; it moves as runs finish and as the graph changes.

## Runs proposed

22 task runs (C1, LM0–LM19, I1), plus one independent review of C1 before slice B and C start,
and the independent review each security-sensitive task takes. None is launched by this plan.
Each run is capped by the repository's own budget in its configuration; the worst case is 22 runs
at that cap, and the graph read prints no dollar figure yet (O9).

## Checks the plan takes

From the block 840–849:

- **Check 840** (LM0; owner-run; does not gate, R15): what a Cloud Run execution's identity token
  carries, whether a launcher reads an execution's overrides, whether a custom token exchanges
  twice.
- **Check 841** (LM17; owner-run; before the release is cut): on a live installation at the
  previous release, the update moves the grant with a run in flight and nothing stops.

## After-deploy steps

None beyond `fugaro update`. The release's `### For operators` (LM16) says: run `fugaro update`;
it will name any `group:` launcher entry that can no longer launch and the fix; every teammate
upgrades the CLI before their next launch, and an older CLI says so itself; `fugaro pair` now asks
the service.

## Considered and declined

- **A second service with its own account** (D3121's alternative): the rule in F7 would stand;
  declined by the owner for the existing class, roster and call.
- **The token returned on today's sealed channel** (D3122's alternative): the copy in F9 stays.
- **Binding the claim to a registered execution through the Cloud Run API**: a new read grant for
  the service, and it still cannot prove the caller is that execution (F11). Check 840 decides
  whether a better binding exists.
- **Narrowing operators' deployer grant** (D3127's alternative): one hop removed on the update's
  own chain, the reach unchanged.
- **A per-repository launch list** (D3130's alternative): a spec of its own.
- **A verified-launcher claim**: out of scope; the record and the audit line carry it (R11).
- **Chain-bound issuing for the orchestrator** (D3125's alternative).
