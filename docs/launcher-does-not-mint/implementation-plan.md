# A launcher does not mint a run's identity: implementation plan

**Status: plan only; nothing is implemented; no task is approved.** Each task needs the owner's
explicit go. An answer in [reconciliation.md](reconciliation.md) settles a question; it approves
no task.

This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md), checked
against `origin/main` at `7987acb99` and again after `c16781dc8` and then `588750891` were merged
in. The answers that
shaped it:

- **D3121**: the credentials service issues. Its account gains the minter role on the runs'
  signer, which amends the rule on that account's grants.
- **D3122**: the launcher gets a one-time ticket; the run claims its identity.
- **D3123**: roster service accounts are a class at the issuer; `group:` entries are not, and
  `update` names them first.
- **D3124**: one release; the grant goes only after the issuer answers, and the policy version
  moves so an older CLI is told to upgrade.
- **D3125** (amended by D3136): the resident orchestrator asks the issuer, and gets a ticket only
  for a recorded, unstarted attempt of its own record, with no context repositories.
- **D3126**: the issuer mints pairing and device tokens.
- **D3127**: operators lose the standing grant; the documents state what they can still reach.
- **D3128**: tasks start after always-a-budget's I1 and keys-through-service's release A. Only
  **C1** and **LM0** may start before them, and **LM19** once C1 has merged (D3129).
- **D3129** (amended after review twice; the third answer stands): everything in
  batch-run-identity that touches the key or its C1 waits for LM19; only its BRS, BR0 and BR8 to
  BR11 go. LM19 may start as soon as this plan's C1 merges.
- **D3131** (added by review round 2): the policy bump's halt of executions queued and not
  started is accepted, and the update says the count at its door.
- **D3132 to D3137** (asked after the pull request opened): the orchestrator's launch path was
  traced (F34) and its asks are bound to its record (D3136, amended after review round 4); a
  roster service account is admitted unscoped, as today (D3137); a fourth review round was run
  (D3133); D3126 stands with H1 narrowed (D3134); LM14's first step settles whether an
  installation can launch with no published policy (D3135).
- **D3130** (amended after review, answered again): the issuer enforces the roles-and-scopes
  launch check on the verified caller, and still checks that the repository is the installation's.

Tasks are **C1**, **LM0** to **LM24** without LM9, and **I1**: 26 runs. LM9 was folded into LM6
after review round 1 and its number is not reused. LM18 ships one release after the rest.

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
- **No CLI release is cut between the merge of LM21 and the merge of LM14.** The version bump
  without the move tells every older CLI to upgrade for nothing and halts queued executions with
  no line at the door; the move without the bump is the option D3124 rejected. LM21 waits for I1
  and LM14 waits for LM21, so the gap is one task wide.
- Live checks are run by the owner, from one script each. No agent performs any step of one.

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| No route signs a uid or a claim set the caller chose | C1 (the wire has no such field), LM2, LM4, I1's cross-class test |
| A ticket is claimed once; the second claim is refused and audited | LM2, I1 |
| A re-issue for a run on another caller's record is refused | LM1, I1 |
| The claim comes from the job account on the record (F12 for a shared job) | LM2 |
| The issuance record is unwritable and unreadable with any signed-in identity | LM5 |
| A claim retried after a lost response is not a second claim | C1, LM2, LM7 |
| A caller with no launch scope on the repository gets no ticket | LM23 |
| The orchestrator's class gets a ticket only for a recorded, unstarted attempt, and never with context repositories | LM24, LM6 |
| The probe admits operators only, returns no signature bytes, and audits each refusal | C1, LM2 |
| The deletes the update makes are on its own allow-list, for an ordinary and a managed project | LM20 |
| The people's row and the orchestrator's go only after the issuer answers; a failed probe leaves them and says why | LM14 |
| An installation at the previous release converges with one `fugaro update` and nothing else | LM14 |
| A run in flight keeps running through the update | LM14 (`TestUpdateLeavesARunInFlightRunning`) |
| An older CLI is told to upgrade, by its own refusal | LM21 |
| What the update says about older CLIs, groups and queued executions is printed at its door, before the policy stage | LM14 |
| A new CLI on an installation not yet updated still launches | LM6, I1 |
| An execution created before the update that has bootstrapped finishes; one that has not halts with the skew sentence, and the update said so (F24, O12) | LM7, LM14 |
| The bridge keys on "not served", a release-behind service's answer and no service configured, and on nothing else | C1, LM6 |
| The issuer unreachable: the launch refuses with the fix; the run retries, then halts | LM6, LM7 |
| The plan a reviewer reads names the issuer's row by account and role | LM11, LM13 (`issuerMinterGrant`) |
| No command waits for input, with stdin closed | LM14, LM10 |

## Slices and tasks

Slice A (C1, LM0) can go before the plans D3128 waits on. Slice B is the issuer, slice C the
launcher and the runner, slice D Terraform and the update, slice E the documents and the live
check. LM18 is the removal one release later.

### C1: the identity wire contract

- Acceptance: one package declares the five routes' paths (ticket, claim, pairing, device and
  the probe, with the class each admits: the probe admits the roster's operators only), request and response bodies, refusal
  codes (among them "not served", a code of its own and not the existing `refuseNotServed`), the
  claim's nonce and what a repeated nonce gets (R2), the reference to an attempt that an
  orchestrator's ask carries (D3136), the probe's answer, which says the service signed a fixed payload as the runs' signer and
  carries no signature bytes (F33, R2), what of the slip's mechanism the
  routes reuse (F1), the ticket's execution variable name, and the issuance record's shape, with the nonce's
  digest, and its place under `creds` (R3); mounts the five
  routes on stubs that answer "not served", listed where the service lists the routes it serves
  and the routes it answers ahead of the shared parse; a test pins the answer a service of the
  release behind gives these paths, as `slipRouteAbsent` reads it, which is the bridge's other
  trigger (F27); the wire decodes strictly
  and has no field for a uid or a free claim set.
- Files: `internal/identitywire/wire.go` (new), `internal/identitywire/wire_test.go` (new),
  `internal/credsvc/identity_config.go` (new: the service's identity configuration, the issuance
  store's interface and the scope check's interface), `internal/credsvc/identity_stub.go` (new,
  stub), `internal/credsvc/server.go` and `internal/credsvc/scope_installation.go` (the mount, in
  `servedRoute` and `aheadOfTheSharedParse`), `docs/launcher-does-not-mint/contract.md` (new).
  Past five; the two mount files are a line or two each.
- Interfaces: produces everything LM1–LM7, LM10, LM19, LM23, LM24 and I1 build against. Consumes
  nothing.
- Tests: `TestIdentityWireDecodesStrictly`, `TestIdentityWireHasNoCallerChosenIdentity`,
  `TestIdentityRoutesAnswerNotServed`, `TestUnknownPathAnswerIsPinned`.
- Command: `go test ./internal/identitywire/ ./internal/credsvc/ -run 'IdentityWire|IdentityRoutes|UnknownPathAnswer' -count=1`
- Red step: `TestIdentityRoutesAnswerNotServed` fails before the mount, with the answer
  `TestUnknownPathAnswerIsPinned` records.
- Depends on: none.
- Commit: `identity: the wire contract (C1)`
- Reviewer tier: opus, strictest rigor, and an independent review (`fugaro review --pr N`) before
  any task is written against it.

### LM0: Check 840, as a script

- Acceptance: one script the owner runs prints what a Cloud Run execution's metadata identity
  token carries, whether `run.executions.get` returns an execution's overrides to a launcher, and
  whether one custom token exchanges twice (U1, U2, U3); it changes nothing it does not remove.
- Files: `scripts/live-checks/check-840.sh` (new), `scripts/live-checks/check-840_test.sh` (new),
  `scripts/live_checks_test.go` (the list `TestLiveCheckScripts` reads),
  `docs/gcp-live-checklist.md` (the section for 840).
- Interfaces: none.
- Tests: `check-840_test.sh`; `TestLiveCheckScripts` (`scripts/live_checks_test.go`).
- Command: `bash scripts/live-checks/check-840_test.sh && go test ./scripts/ -run TestLiveCheckScripts -count=1`
- Red step: `check-840_test.sh` fails before the script exists.
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
  yields one custom token signed as the runs' signer with the claims on the record, after the
  record itself is checked again (the account is in the job tables, the uid comes from the
  record's path, the claims pass R4); the probe, for a verified operator, signs a fixed payload as the runs' signer, discards it
  and returns no signature bytes, with one audit line per refusal; the nonce is in no log and no audit line; the same
  nonce again gets the same answer and no audit line of a second claim; another nonce after a
  success, a wrong account, an expired or unknown ticket are each refused with one audit line.
- Files: `internal/credsvc/identity_claim.go` (new), `internal/credsvc/identity_sign.go` (new),
  `internal/credsvc/identity_claim_test.go` (new).
- Interfaces: consumes C1 and `token.Signer`, `token.MintAt`. Produces the real claim handler and
  the service's signer (the service's own credentials through `token.IAMSigner`).
- Tests: `TestClaimSignsTheRecordedClaims`, `TestClaimIsOnce`,
  `TestClaimRetryWithTheSameNonceIsNotASecondClaim`, `TestClaimRevalidatesTheRecord`,
  `TestClaimNeverLogsTheNonce`, `TestProbeSignsAndDiscards`, `TestClaimRefusesAnotherJobAccount`,
  `TestClaimRefusesExpiredTicket`, `TestClaimAnsweredAheadOfRunIdentity`.
- Command: `go test ./internal/credsvc/ -run 'Claim|ProbeSigns' -count=1`
- Red step: `TestClaimIsOnce` fails when the store does not mark the claim.
- Depends on: C1 (the wire and the store's interface; LM1's real store is not needed to build it).
- Commit: `credsvc: a run claims its identity once (LM2)`
- Reviewer tier: opus, independent review.

### LM3: the caller classes

- Acceptance: the ticket route admits a roster person, a roster service account and the
  orchestrator's account, each by its own verified Google ID token, and names the class on the
  record; a `group:` address, an unlisted account and a job's account are refused.
- Files: `internal/credsvc/identity_callers.go` (new), `internal/credsvc/auth.go` and
  `internal/credsvc/launchers.go` (the service-account launcher class beside `VerifyLauncher`),
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

- Acceptance: a test proves that the issuance record's path, as C1 places it under `creds`, is
  refused for read and for write to a run's token, a person's, a device's and an unauthenticated
  caller; no rule is generated or changed (F26).
- Files: `internal/budget/rules/rules_test.go`.
- Interfaces: consumes C1 (the record's path).
- Tests: `TestRulesKeepTheIssuanceRecordClosed`, `TestRulesPreexistingPathsUnchanged`.
- Command: `go test ./internal/budget/rules/ -run 'Rules' -count=1`
- Red step: the test is written first against a path under a run's node, where it fails, and
  passes for the contract's path; the pull request says so.
- Depends on: C1.
- Commit: `rules: the issuance record's place is closed (LM5)`
- Reviewer tier: opus, independent review.

### LM6: the launcher asks the issuer, the resident launcher included

- Acceptance: `launchRun` gets a ticket from the service and puts it on the execution under the
  contract's variable; no token is signed and no object is written; the ticket is asked before
  anything that names the run id is written where another launcher reads (R5).
  A launch that holds a ticket calls LM8's gate. Every way in asks before it writes, the
  troubleshoot launch included. `residentLauncher.Launch` presents its own account's identity
  token, names the attempt it re-issues in its ask (D3136) and takes the same path.
  The launch signs locally as before (R7) only where the service answers "not served", answers
  as a release-behind service does (C1's pinned answer), or no service is configured. Where the
  service is unreachable or refuses, the launch fails with the reason and the fix after a
  bounded retry (R1). Where the bridge fires and the row is already gone, the launch refuses
  with the fix and not with IAM's error.
- Files: `internal/cli/launch_identity.go` (new), `internal/cli/launch_budget.go`,
  `internal/cli/run.go`, `internal/cli/troubleshoot_launch.go`,
  `internal/cli/orchestrator_launcher.go`, `internal/cli/orchestrator_serve.go`, and
  `internal/cli/launch_identity_test.go` (new). Past five; the last three are one call site each.
- Interfaces: consumes C1. Produces the CLI's issuer client.
- Tests: `TestLaunchAsksTheIssuerAndSignsNothing`, `TestLaunchAsksBeforeItWritesTheRun`,
  `TestLaunchBridgesOnlyOnTheThreeAnswers`, `TestLaunchRefusesWithTheFixWhenIssuerUnreachable`,
  `TestLaunchRefusalNamesAnotherCallersRun`, `TestBridgeWithoutTheRowRefusesWithTheFix`,
  `TestLaunchWithATicketRefusesAnOlderImage`, `TestResidentLauncherAsksTheIssuer`.
- Command: `go test ./internal/cli/ -run 'LaunchAsks|LaunchBridges|LaunchRefus|BridgeWithoutTheRow|LaunchWithATicket|ResidentLauncherAsksTheIssuer' -count=1`
- Red step: `TestLaunchAsksTheIssuerAndSignsNothing` fails: the fake IAM sees a `signJwt` call.
- Depends on: C1; LM8 (kept: it calls the gate LM8 writes). External: always-a-budget's AB4
  merged (F17).
- Commit: `cli: a launch asks for a ticket (LM6)`
- Reviewer tier: opus, independent review.

### LM7: the runner claims

- Acceptance: a run whose execution carries a ticket claims its identity from the service with
  the job's own ID token and a nonce it keeps for its retries, and exchanges the token; a refused
  claim halts the run with a message that says
  the ticket was already claimed; an unreachable service is retried for a bounded time, then the
  run halts; an execution that carries the old marker opens the sealed object as before (R7).
- Files: `internal/runner/identity_claim.go` (new), `internal/runner/budget_session.go`,
  `internal/runner/projectpolicy.go`, `internal/runner/identity_claim_test.go` (new).
- Interfaces: consumes C1 and `metadataIDToken`. `takeBudgetToken` in `projectpolicy.go` is the
  take both the budget session and the policy read share; it is where the claim goes in.
- Tests: `TestRunnerClaimsItsIdentity`, `TestRunnerHaltsWhenTicketAlreadyClaimed`,
  `TestRunnerRetriesThenHaltsWhenIssuerUnreachable`, `TestRunnerStillOpensASealedToken`.
- Command: `go test ./internal/runner/ -run 'RunnerClaims|RunnerHalts|RunnerRetries|RunnerStillOpens' -count=1`
- Red step: `TestRunnerClaimsItsIdentity` fails: the runner looks for an object.
- Depends on: C1. External: always-a-budget's AB1a merged; U4 checked as the first step.
- Commit: `runner: a run claims its identity (LM7)`
- Reviewer tier: opus, independent review.

### LM8: a new launcher refuses an image that cannot claim

- Acceptance: a gate function refuses a job image older than the release that claims, naming the
  image refresh the update performs. LM6 calls it for a launch that holds a ticket and not for
  one on the bridge.
- Files: `internal/cli/recipes_skew.go`, `internal/cli/recipes_skew_test.go`.
- Interfaces: a constant beside `serviceAuthSince`.
- Tests: `TestIdentitySinceRefusesAnOlderImage`.
- Command: `go test ./internal/cli/ -run 'IdentitySince' -count=1`
- Red step: the test fails with no gate.
- Depends on: none.
- Commit: `cli: an image that cannot claim is refused at launch (LM8)`
- Reviewer tier: standard.

### LM10: `fugaro pair` asks the issuer

- Acceptance: `fugaro pair` gets the pairing token and the device token from the service; where
  the service answers "not served" it signs locally as before; its refusals name the fix and it
  adds no wait of its own (the code the operator types stays the one value entry it has today).
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
  credentials service receives the signer account, the roster's service accounts and the
  implicit admins (R16) in its environment, under the names LM22 declared, and the three names
  leave the cover's awaiting list in the same change; each roster service
  account holds the invoker row; with the defaults the plan is unchanged.
- Files: `deploy/terraform/gcp/modules/installation/orchestrator.tf`,
  `deploy/terraform/gcp/modules/installation/credentials.tf`,
  `deploy/terraform/gcp/modules/installation/iam.tf`,
  `deploy/terraform/gcp/modules/installation/variables.tf`,
  `deploy/terraform/gcp/roots/installation/variables.tf`,
  `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl`,
  `deploy/terraform/gcp/modules/installation/orchestrator_test.go`, `internal/infra/tf/cover.go`.
  Past five; one module's files and the list that tracks them.
- Interfaces: produces the variables LM13 writes.
- Tests: the module's own tests; `TestOrchestratorMinterIsBehindTheSwitch` (new); the root's
  exact environment map; `TestCredentialsServiceEnvIsTheDeploymentsOwn`.
- Command: `go test ./deploy/terraform/gcp/modules/installation/ -run 'Orchestrator' -count=1 && go test ./internal/cli/ -run 'CredentialsServiceEnvIsTheDeploymentsOwn' -count=1 && terraform fmt -check -recursive deploy/terraform`
- Red step: the new test fails with the row unconditional.
- Depends on: LM22 (the cover's rule: a name is on its list, and read by the command, before a
  module renders it).
- Commit: `terraform(installation): the orchestrator's minter row behind the switch (LM12)`
- Reviewer tier: opus, independent review.

### LM13: `update` writes the issuer's row

- Acceptance: an update passes the credentials account to the Firebase root, so the issuer's row
  is planned, and leaves the switch on; it passes the account only once the installation root
  has created it, so a fresh setup's first apply names no account that does not exist (U9); the
  plan cover names the row by account and role; the roster's `serviceAccount:` entries and the
  implicit admins are rendered to the service, the roster's `group:` entries are not.
- Files: `internal/infra/firebase.go`, `internal/infra/tfvars.go`, `internal/infra/launchers.go`,
  `internal/cli/init_firebase.go` (where the root's inputs are built, F32),
  `internal/infra/tf/cover.go`, `internal/infra/tf/cover_grants_test.go`, and the golden plans
  (`scripts/gen-golden-plans.sh`). Past five; the first four are one field carried through.
- Interfaces: consumes LM11's and LM12's variables.
- Tests: `TestIssuerMinterGrantIsCovered`, `TestLauncherServiceAccountsRendered`,
  `TestFreshSetupPlansNoRowForAMissingAccount`.
- Command: `go test ./internal/infra/... ./internal/cli/ -run 'IssuerMinterGrant|LauncherServiceAccounts|FreshSetupPlansNoRow' -count=1`
- Red step: `TestIssuerMinterGrantIsCovered` fails: the cover reports a grant it does not carry.
- Depends on: LM11 and LM12 (the golden plans are generated from their variables).
- Commit: `infra: update plans the issuer's row (LM13)`
- Reviewer tier: opus, independent review.

### I1: wire the issuer in

- Acceptance: the stub file is gone; the service takes the values LM22's reader read into C1's
  configuration and serves the four routes for real; with no signer account configured it
  answers "not served" on all four, so an installation whose roots carry no issuer row yet stays
  on the bridge; one test runs
  a launch through the service to a session: ticket, claim, exchange. In the same test a second
  person's ask for that run is refused, a caller without the launch scope is refused, an
  orchestrator's ask for a run its record does not carry is refused, a copied
  ticket's second claim is refused and the losing run halts, a retried claim with its nonce
  succeeds, a pairing completes, and no token of one kind is accepted on another kind's route.
- Files: `internal/credsvc/identity_stub.go` (deleted), `internal/credsvc/identity_mount.go` (new:
  the handlers `server.go` already mounts, now bound to the real ones),
  `internal/cli/credentials_serve.go`, `internal/credsvc/identity_e2e_test.go` (new),
  `internal/cli/launch_identity_e2e_test.go` (new). It does not edit `server.go`, which is the
  contract's.
- Interfaces: consumes every side C1 separated.
- Tests: `TestIdentityEndToEnd`, `TestIdentityCrossClassRefused`,
  `TestLaunchToSessionThroughTheIssuer`, `TestNoSignerConfiguredIsNotServed`.
- Command: `go test ./internal/credsvc/ ./internal/cli/ -run 'IdentityEndToEnd|IdentityCrossClass|LaunchToSessionThroughTheIssuer|NoSignerConfiguredIsNotServed' -count=1`
- Red step: `TestIdentityEndToEnd` fails with "not served" while the stubs are mounted.
- Depends on: LM1, LM2, LM3, LM4, LM5, LM6, LM7, LM10, LM23 and LM24 (it runs all of them
  together),
  and LM8 (once this merges a launch holds a real ticket, and an image that cannot claim must be
  refused at launch, not found at the run's start).
- Commit: `identity: the issuer is wired in (I1)`
- Reviewer tier: opus, independent review.

### LM14: `update` moves the grant

- Acceptance: first step, before any code: read whether an installation that can launch after
  always-a-budget can have no published policy (U5, D3135); if it can, stop and say so, because
  the question returns to the owner. Then: at its door, with the note about runs in flight and
  before the policy stage, the
  update prints R10's lines (the `group:` entries by name with the fix, older CLIs, pairing, and
  the count of executions queued and not started, which will halt: D3131). The Firebase root's guard is given
  LM20's list where that root is applied (F32). A probe that answers without a signature made as
  the runs' signer leaves the rows (F33). The stage runs on an
  ordinary and on a managed update. The orchestrator's row goes only when the resident service
  already runs this release's image (R8). calls the probe
  (R2); when it answers, the stage applies both roots with the switch off and the people's and
  the orchestrator's rows are gone; when it does not, the rows stay and one line says why. An
  installation at the previous release converges with this one command, with a run in flight and
  with stdin closed, and asks nothing.
- Files: `internal/cli/init_identity.go` (new), `internal/cli/init_stages.go`,
  `internal/cli/init_firebase.go`, `internal/cli/update_in_flight.go`,
  `internal/cli/init_identity_test.go` (new),
  `internal/cli/update_in_flight_test.go`.
- Interfaces: consumes LM13's values, LM20's list and C1's probe shape.
- Tests: `TestUpdateMovesTheGrantOnlyAfterTheIssuerAnswers`, `TestUpdateKeepsTheGrantAndSaysWhy`,
  `TestUpdateKeepsTheGrantWhenTheIssuerCannotSign`,
  `TestUpdateSaysItsLinesAtTheDoor`, `TestUpdateFromThePreviousReleaseConverges`,
  `TestUpdateMovesTheGrantOnAManagedProject`, `TestOrchestratorRowGoesAfterItsImageMoved`,
  `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/cli/ -run 'UpdateMovesTheGrant|UpdateKeepsTheGrant|UpdateSaysItsLinesAtTheDoor|UpdateFromThePreviousRelease|OrchestratorRowGoesAfter|UpdateLeavesARunInFlightRunning' -count=1`
- Red step: `TestUpdateMovesTheGrantOnlyAfterTheIssuerAnswers` fails: the people's row is in the
  final plan.
- Depends on: I1 (the stage's test launches through the real issuer after the move), LM13 (the
  switch it turns), LM20 (without the list the guard refuses the delete), LM21 (the move never
  ships without the lever, and the convergence test needs the bump).
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
- Depends on: LM13 (the classification names the row it writes) and LM14 (kept, behaviour: before
  the grant has moved, doctor would report every launcher as still signing, with a fix that
  does not clear it).
- Commit: `doctor: the service is the designed signer (LM15)`
- Reviewer tier: opus, independent review.

### LM16: the documents

- Acceptance: SECURITY.md and its website mirror say a launcher may launch any repository and may
  not mint; they state both residuals of R5, the operators' reach (D3127), the bearer token (O5),
  who writes past the rules (R3), that a roster service account is the one class the scope check
  does not judge (D3137), that `rb` is still the launcher's own word where the rules read it
  (R11), and what the issuer is a single point of; H1 is restated as
  narrowed to operators and the service, not closed (O13); the orchestrator design records O14; `budget-token-channel.md`, `roles.md` and the
  orchestrator design say the same; the release notes' `### For operators` is "run
  `fugaro update`" plus what it will say (R10).
- Files: `SECURITY.md`, `website/content/security.md`, `docs/budget-token-channel/spec.md`,
  `docs/roles/spec.md`, `docs/mobile/design/always-on-orchestrator.md`, and the release's notes
  under `docs/releases/`.
- Interfaces: none.
- Tests: the Docs filter and the website check.
- Command: `go test ./internal/cli/ -run Docs -count=1 && (cd website && npm run check)`
- Red step: `docs_website_security_test.go` fails on the first edit until the mirror matches.
- Depends on: I1, LM14 and LM21 (it states what is true after them).
- Commit: `docs: a launcher launches, the service mints (LM16)`
- Reviewer tier: opus, independent review (SECURITY.md).

### LM17: Check 841, as a script

- Acceptance: one script the owner runs on a live installation at the previous release: update
  with a run in flight; that run finishes; a new launch gets a session; a second account's ask
  for that run is refused; a copied ticket loses; an older CLI prints the upgrade sentence;
  `fugaro pair` pairs.
- Files: `scripts/live-checks/check-841.sh` (new), `scripts/live-checks/check-841_test.sh` (new),
  `scripts/live_checks_test.go` (the list `TestLiveCheckScripts` reads),
  `docs/gcp-live-checklist.md`.
- Tests: `check-841_test.sh`; `TestLiveCheckScripts`.
- Command: `bash scripts/live-checks/check-841_test.sh && go test ./scripts/ -run TestLiveCheckScripts -count=1`
- Red step: `check-841_test.sh` fails before the script exists.
- Depends on: LM14 and LM21.
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
  this plan's C1: its D3101 and D3108 with their new object; its C1 re-cut (what of the wire,
  the registration, the key fact and the opt-in fact stays); every task F18 lists rewritten,
  kept or dropped, one by one from its own Depends line; its D3109 settled against D3123; its
  README's approvals corrected (O16); each amended answer marked and put to the owner.
- Files: `docs/batch-run-identity/reconciliation.md`, `implementation-plan.md`,
  `implementation-plan.graph.json`, `README.md`.
- Tests: the Docs filter; `fugaro plan graph --plan docs/batch-run-identity/implementation-plan.md`.
- Command: `go test ./internal/cli/ -run Docs -count=1`
- Red step: none; a documents task.
- Depends on: C1 merged (it is written against the contract's document). **The graph draws no
  edge for this one**, for the same reason as LM1: it reads the contract's text and is not one
  side of an edge the contract broke. It may start as soon as C1 has merged, ahead of D3128's
  gate (D3129).
- Commit: `docs(batch-run-identity): the release is a claim (LM19)`
- Reviewer tier: opus, independent review.

### LM20: the update's own deletes are listed

- Acceptance: the plan that turns the switch off deletes exactly the people's minter rows and
  the orchestrator's. For an ordinary project the update's allow-list names exactly those, so
  `tf.Guard` admits them and still refuses any other delete, and no flag is asked of the
  operator. For a managed project the same addresses go through `managed.Spec.Upgrade`, where
  `GuardManaged` admits a delete whose address it was given, and the test pins that (F25).
- Files: `internal/infra/identity_allow_delete.go` (new),
  `internal/infra/identity_allow_delete_test.go` (new), `internal/cli/init.go`,
  `internal/cli/update_managed.go`, `internal/managed/plan_test.go`.
- Interfaces: consumes the resource addresses LM11 and LM12 give the rows. Produces the list
  LM14's stage passes; it wires the installation root's guard and the managed path, and leaves
  the Firebase root's guard to LM14, which owns that file at its level (F32).
- Tests: `TestIdentityAllowDeleteNamesOnlyTheMinterRows`,
  `TestManagedGuardAdmitsTheMinterRowDeletes`, `TestGuardStillRefusesAnotherDelete`.
- Command: `go test ./internal/infra/ ./internal/managed/ ./internal/cli/ -run 'IdentityAllowDelete|ManagedGuardAdmitsTheMinterRow|GuardStillRefusesAnotherDelete' -count=1`
- Red step: `TestIdentityAllowDeleteNamesOnlyTheMinterRows` fails: with no list, `tf.Guard`
  refuses the ordinary project's plan.
- Depends on: LM11 and LM12 (it names their rows' addresses).
- Commit: `infra: the minter rows' deletes are the update's own (LM20)`
- Reviewer tier: opus, independent review.

### LM21: the policy version moves

- Acceptance: the published policy's version moves to the next one, shape-free, migrated in
  place by the update's policy stage; a CLI of the release behind refuses with `SkewSentence`,
  naming this release; every version-10 node keeps its bytes but for the number.
- Files: `internal/projectpolicy/version.go`, `internal/projectpolicy/migrate.go`,
  `internal/projectpolicy/migrate_test.go`, `internal/projectpolicy/version_since_test.go`,
  `internal/credsvc/policy_test.go`, `internal/cli/launch_budget_test.go`, `docs/release.md` and
  `docs/project-layer.md` (the version's row in each). Past five; it is what the version-10 bump
  touched, one line each.
- Interfaces: none.
- Tests: `TestVersionSinceCoversEveryVersion`, `TestMigrateToTheIdentityVersionIsShapeFree`,
  `TestOlderCLIIsToldToUpgrade`.
- Command: `go test ./internal/projectpolicy/ ./internal/credsvc/ ./internal/cli/ -run 'VersionSince|MigrateToTheIdentityVersion|OlderCLIIsToldToUpgrade|Policy|Docs' -count=1`
- Red step: `TestVersionSinceCoversEveryVersion` fails until the new version has its row.
- Depends on: I1 (the lever ships with what it levers). The rule in Conventions keeps a release
  from being cut between this and LM14.
- Commit: `policy: the version an identity-issuing installation publishes (LM21)`
- Reviewer tier: opus, independent review.

### LM22: the service's new environment names, and their reader

- Acceptance: the three new entries of the service's environment (the signer account, the
  roster's service accounts, the implicit admins of R16) are named once, are on the cover's list
  of entries awaiting the module (`CredentialsServiceEnvAwaitingTheModule`), and are read by
  `credentials serve` into values nothing uses yet; `TestCredentialsServiceEnvIsTheDeploymentsOwn`
  knows the three, so the module that renders them next (LM12) turns nothing red.
- Files: `internal/infra/tf/cover.go`, `internal/infra/tf/cover_test.go`,
  `internal/cli/credentials_serve.go`, `internal/cli/credentials_serve_test.go`.
- Interfaces: produces the three names LM12 renders, and the read values I1 hands to C1's
  configuration.
- Tests: `TestCredentialsServiceEnvKnowsTheIdentityNames`, `TestCredentialsServeReadsTheIdentityEnv`,
  `TestCredentialsServiceEnvIsTheDeploymentsOwn`.
- Command: `go test ./internal/infra/tf/ ./internal/cli/ -run 'CredentialsServiceEnvKnowsTheIdentityNames|CredentialsServeReadsTheIdentityEnv|CredentialsServiceEnvIsTheDeploymentsOwn' -count=1`
- Red step: `TestCredentialsServiceEnvKnowsTheIdentityNames` fails: the names are not on the list.
- Depends on: none.
- Commit: `infra: the identity names and their reader (LM22)`
- Reviewer tier: opus, independent review.

### LM23: the issuer enforces the launch scope

- Acceptance: the ticket route's scope check runs `internal/access`'s resolver on the verified
  caller for the launch action on the repository, and gives the answer the CLI gate
  (`launchPolicyGates`) gives today, arm by arm (R16): the implicit admins come from the service's
  configuration; a roster service account is admitted without a scope judgement (D3137); an installation
  with no roles to judge by is allowed as the gate allows it; each context repository is judged
  for the view action and the policy's allow-list before the list is signed (F31); records that
  do not decode refuse; records that cannot be reached refuse, the one deliberate difference.
  A caller without the scope gets no ticket and one audit line; the orchestrator's class is
  judged by LM24 (D3130 as amended, D3136).
- Files: `internal/credsvc/identity_scope.go` (new), `internal/credsvc/identity_scope_test.go`
  (new), `internal/cli/access_issuer_agree_test.go` (new: the gate is unexported in
  `internal/cli`, which imports the service's package, so the table that runs both lives there).
- Interfaces: consumes C1 (the scope check's interface) and `internal/access`. First step: U10.
- Tests: `TestIssuerRefusesACallerWithoutTheLaunchScope`,
  `TestIssuerAgreesWithTheCLIGate` (a table run through both, with a row for each arm of R16 and the outage row marked as the
  difference), `TestIssuerRefusesAContextRepositoryWithoutTheViewScope`,
  `TestOrchestratorClassIsLeftToItsOwnCheck`.
- Command: `go test ./internal/credsvc/ ./internal/cli/ -run 'IssuerRefusesA|IssuerAgreesWithTheCLIGate|OrchestratorClassIsLeftToItsOwnCheck' -count=1`
- Red step: `TestIssuerRefusesACallerWithoutTheLaunchScope` fails against the stub check, which
  admits everyone.
- Depends on: C1.
- Commit: `credsvc: no launch scope, no ticket (LM23)`
- Reviewer tier: opus, independent review.

### LM24: the orchestrator's asks are bound to its record

- Acceptance: for an ask in the orchestrator's class the issuer reads the orchestrator's own
  record and issues only where the run id the ask names is a recorded attempt of that repository
  with no outcome; an ask for any other run id, for an attempt that has started or ended, or one
  that names any context repository is refused with one audit line (D3136 as amended, F34). No
  committer is read and no scope is judged for this class.
- Files: `internal/credsvc/identity_orchestrated.go` (new),
  `internal/credsvc/identity_orchestrated_test.go` (new).
- Interfaces: consumes C1 (the attempt reference and the scope check's interface) and the
  orchestrator's record as `internal/orchestrator` stores it (`Intent`).
- Tests: `TestOrchestratedAskNeedsARecordedUnstartedAttempt`,
  `TestOrchestratedAskForAnotherRunIsRefused`, `TestOrchestratedAskWithContextReposIsRefused`.
- Command: `go test ./internal/credsvc/ -run 'OrchestratedAsk' -count=1`
- Red step: `TestOrchestratedAskForAnotherRunIsRefused` fails against the stub check, which
  admits the orchestrator's class whole.
- Depends on: C1.
- Commit: `credsvc: the orchestrator is issued only what its record carries (LM24)`
- Reviewer tier: opus, independent review.

## Parallel execution

| Level | Tasks |
| --- | --- |
| 0 | C1, LM0, LM8, LM11, LM22 |
| 1 | LM1, LM2, LM3, LM4, LM5, LM6, LM7, LM10, LM12, LM19, LM23, LM24 |
| 2 | I1, LM13, LM20 |
| 3 | LM21 |
| 4 | LM14 |
| 5 | LM15, LM16, LM17 |
| after a release | LM18 |

The graph read reports six levels and puts LM1 and LM19 at level 0 and LM18 at level 5: it has
no edge for the first two (see their Depends lines, and O11) and no way to say "one release
later". **The table above is the order to launch in, and this graph is not to be imported for
automatic launching while C1 is unmerged.** D3128 holds LM1 behind two other plans, so in
practice C1 has merged long before it; LM19 is launched by hand once C1 has merged (D3129).

**Critical path**, as the read printed it: `C1 -> LM10 -> I1 -> LM21 -> LM14 -> LM15`,
six tasks (LM16 and LM17 sit at the same last level); every path through slice B or C to I1 is as long. LM14 is
sequential behind I1 because it removes the grant, and nothing may remove it before a launch has
gone through the issuer in a test. LM18 waits a release by design.

**External gates (D3128):** everything but C1, LM0 and LM19 waits for always-a-budget's I1 and
keys-through-service's release A.

### Shared files

| File | Writers | Levels |
| --- | --- | --- |
| `internal/credsvc/server.go` | C1 | 0 |
| `internal/credsvc/identity_stub.go` | C1 (creates), I1 (deletes) | 0, 2 |
| `internal/credsvc/identity_mount.go` | I1 | 2 |
| `internal/credsvc/auth.go` | LM3 | 1 |
| `internal/cli/launch_budget.go` | LM6, LM18 | 1, after a release |
| `internal/cli/run.go` | LM6 | 1 |
| `internal/cli/orchestrator_launcher.go`, `orchestrator_serve.go` | LM6 | 1 |
| `internal/cli/pair.go` | LM10, LM18 | 1, after a release |
| `internal/cli/credentials_serve.go` | LM22, I1 | 0, 2 |
| `internal/cli/troubleshoot_launch.go` | LM6 | 1 |
| `internal/cli/update_managed.go` | LM20 | 2 |
| `internal/credsvc/scope_installation.go` | C1 | 0 |
| `internal/cli/init.go` | LM20 | 2 |
| `internal/cli/init_stages.go`, `update_in_flight.go` | LM14 | 4 |
| `internal/cli/init_firebase.go` | LM13, LM14 | 2, 4 |
| `scripts/live_checks_test.go` | LM0, LM17 | 0, 5 |
| `internal/credsvc/launchers.go` | LM3 | 1 |
| `internal/cli/launch_budget_test.go` | LM21 | 3 |
| `internal/cli/recipes_skew.go` | LM8 | 0 |
| `internal/runner/budget_session.go` | LM7, LM18 | 1, after a release |
| `internal/budget/rules/rules_test.go` | LM5 | 1 |
| `internal/infra/tf/cover.go` | LM22, LM12, LM13 | 0, 1, 2 |
| `deploy/terraform/gcp/modules/firebase/iam.tf` | LM11, LM18 | 0, after a release |
| `internal/infra/tokensigners.go` | LM15 | 5 |
| `internal/credsvc/policy_test.go`, `docs/release.md`, `docs/project-layer.md` | LM21 | 3 |
| `internal/projectpolicy/version.go` | LM21 | 3 |
| `docs/gcp-live-checklist.md` | LM0, LM17 | 0, 5 |

No level holds two writers of one file. C1's own files (`internal/identitywire/*`,
`identity_config.go`, the mount in `server.go` and `scope_installation.go`, `contract.md`) are
named by no other task; the stub
file is named by I1 alone, which deletes it.

## The dependencies the plan broke and kept

The pass is on for this repository (`planning.decoupling` is `true`, the default).

**Broken, all by C1, all rejoined by I1** (nine; "A → B" reads "A waited for B"):

- LM2 → LM1: the claim needs the record's shape and the store's interface, not LM1's store.
- LM3 → LM1: the classes return the caller the record names; they need its type, not the route.
- LM5 → LM1: the test needs the record's path.
- LM6 → LM1: the launcher needs the ticket route's shape, not a running route.
- LM23 → LM1: the scope check needs the ticket route's hook, an interface, not the route.
- LM24 → LM23: the orchestrator's check sits behind the same hook as the scope check; it needs
  the hook's interface, not LM23's resolver.
- LM7 → LM2: the runner needs the claim's shape.
- LM4 → LM2: the pairing routes need a signer's interface, not LM2's signer.
- LM10 → LM4: `fugaro pair` needs the pairing routes' shape.

**Kept**, each with why a contract would not do:

- LM12 → LM22: the cover's own rule orders them; the "interface" is three names, and LM22 is
  them.
- LM13 → LM11 and LM13 → LM12: the golden plans are generated from their variables.
- LM20 → LM11 and LM20 → LM12: it lists the addresses of rows those tasks write; a list written
  ahead of the rows is a guess the guard would then enforce.
- LM6 → LM8: it calls the gate LM8 writes; a contract for one function would be the function.
- I1 → LM8: behaviour. Once I1 merges a launch holds a real ticket, and an image that cannot
  claim must already be refused at launch.
- LM14 → I1, LM13, LM20 and LM21: it removes the grant; each is something that must work, not a
  shape.
- LM21 → I1: the version bump ships with what it levers.
- LM15 → LM13 and LM15 → LM14: it classifies the row LM13 writes, and it is right only once the
  grant has moved.
- LM16 → I1, LM14 and LM21; LM17 → LM14 and LM21: they state and prove what is true after them.
- LM18 → LM14: a released version must sit between them.

**The gain**, as the read printed it: `gain: 9 broken, 16 kept, 2 added; before 7 tasks, weight
11; after 6 tasks, weight 10; improves`. The counts are the tool's own: it counts the edges of the
graph as it was before the pass, so its "kept" is not the number of lines in the list above. The
critical path goes from seven tasks to six.

**Contract changes anticipated:**

- **R-C1-execution**: if Check 840 finds an execution-level fact in the identity token, the claim
  binds to it (R15). Reaches LM2 and LM7.
- **R-C1-batch**: if LM19 finds the Batch claim needs a field (the job's unique id, the VM's
  token), the wire gains it. Reaches LM1, LM2 and LM7.

## The estimate

`fugaro plan graph --plan docs/launcher-does-not-mint/implementation-plan.md` printed:

```
ready in 210–840 min, no history yet for 26 tasks (26 tasks; seeds for 26; from the seeds: no history is cached on this machine yet)
```

The estimate is informational and reserves no budget. It is what the tasks' tiers and this
machine's history predict; it moves as runs finish and as the graph changes.

## Runs proposed

26 task runs (C1, LM0–LM24 without LM9, I1), plus one independent review of C1 before slice B and
C start, and the independent review each security-sensitive task takes. None is launched by this
plan. Each run is capped by the repository's own budget in its configuration; the worst case is 26 runs
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
it will name any `group:` launcher entry that can no longer launch and the fix, and say how
many executions are queued and not started, which halt and need a relaunch (D3131); every teammate
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
- **Installation-only checking at the issuer** (D3130's first answer): replaced after review
  round 1 found the resolver (F23).
- **Batch-run-identity's C1 goes and only its key-release tasks wait** (D3129's second answer),
  and **Batch goes ahead in full**: the first was replaced after review round 2 showed that C1
  publishes the key fact; the owner's third answer holds everything that touches the key.
- **A lever other than the policy version** for older CLIs: none exists today (F14); O12 carries
  the cost of the one chosen.
- **A verified-launcher claim**: out of scope; the record and the audit line carry it (R11).
- **An unscoped orchestrator class** (D3125's first answer), and **judging the committer of an
  attempt** (D3136's first answer): the first replaced after the trace (F34), the second after
  review round 4 showed an attempt names no committer and only admins write the node.
- **Judging a roster service account like a person** (D3137's alternative).
