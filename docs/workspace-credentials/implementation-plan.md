# Workspace credentials: implementation plan

**Status: plan only. Nothing is implemented and no task is approved; each task needs the owner's
explicit go by name.** It builds [spec.md](spec.md) as amended by
[reconciliation.md](reconciliation.md), which wins where the two disagree.

The answers that shaped it. From the spec: a person's token is stored once at the workspace
(D2960) and is owner-only and never lent (D2962, D2976); the service gets its own grant route and
launcher class (D2970); roster people may store a token (D2971); one switch in the member's policy
(D2972); a missing token refuses (D2974); the person's prefix comes after the threat model
(D2969). From 2026-11-21: both halves are planned, the personal one behind the threat model
(D3140); a personal token follows the route (D3141); it is used automatically, with a flag to skip
it (D3142); it lives in the workspace project's existing intake (D3143), reached at the workspace's own
address and never one a member holds (R13); an exhausted token moves
on to the shared entries (D3144); the roster's grant list is not asked (D3145); a removed
person's run halts within a minute (D3146); a failed grant starts the run on the shared entries
(D3147); a person names the workspace's project to store a token, with no new flag (D3148).

**What this plan does not build**, because another plan owns it (reconciliation F3): the
workspace's service, the shared entries, the cascade, the member's-copy-wins rule, `all_members`
and the meter are [workspace-service](../workspace-service/README.md)'s; a provider key served by a
project's service is [keys-through-service](../keys-through-service/README.md)'s.

## 1. Conventions every task follows

Point, don't restate: `CLAUDE.md`, `.claude/rules/credentials-service.md` (every task under
`internal/creds`, `internal/credsvc`, `SECURITY.md`), `.claude/rules/terraform.md` (WC2, WC3),
`docs/no-prompt-policy/`, `docs/design/update-in-flight.md`, and §1 of
[workspace-service's plan](../workspace-service/implementation-plan.md), whose rules for
`docs/workspaces/*.md` citations and pinned `SECURITY.md` sentences apply here unchanged.

- **Always.** Targeted tests, never a whole package. Hermetic: `internal/gcpfake`,
  `anthropicfake`, `t.TempDir()`. A task that makes a pinned sentence false changes the sentence,
  the test that pins it and `website/content/security.md` in the same change, and runs
  `npm run check` in `website/`. A new field in a published or `--json` shape is added, never
  retyped. A new command or flag is added to the CLI reference, the closed-stdin harness and the
  list of writing commands in the same change (workspace-service F41).
- **Ask first.** A new dependency; a change to a contract's files (raise a contract change, §7);
  anything that would make a command wait for input (there is none in this plan).
- **Never.** A secret-shaped literal; a live cloud call from a test or an agent; a confirmation
  step; an edit under `.github/workflows/`; a second implementation of a route; a write by the
  workspace's service to a member's database; a credential's value, a claim secret or a person's
  token name in a log, an error, a header or a test name; a member's slip sent to the workspace's
  service (R4); the intake's address read from anything a member holds or publishes (R13); a
  change to what an installation's service does for a project in no workspace (A3).

## 2. Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| An installation's service and intake behave exactly as today (A3, D2966) | WC5 `TestAnInstallationStillFallsToTheBundle`; WC3 `TestAnInstallationsIntakeAdmitsItsLaunchersOnly` |
| A grant is given only to a `human` of the roster, by the verified address, and never to a service account | WC4 `TestTheGrantAdmitsOnlyARosterPerson`, `TestAServiceAccountIsRefusedAGrant` |
| A grant names only the caller's own personal names: no other person's, no pool, no `fugaro_p_` name (D2962, D2976) | WC4 `TestAGrantNamesOnlyTheCallersOwnToken` |
| The grant's job account is a member's and its repository is one of that account's (R3) | WC4 `TestAGrantIsHeldToTheNode` |
| A workspace slip carries a claim deadline counted from the grant, so an unclaimed grant expires (owner, threat-model §11 finding 1) | WC4 `TestAnUnclaimedWorkspaceSlipExpiresAtTheClaimDeadline` |
| A grant is bound to the members the person is a launcher of, checked against that member's own list (D3145 reopened, finding 2) | WC4 `TestAGrantIsBoundToTheMembersThePersonLaunchesFor` |
| The slip stores the person's case-folded verified address (finding 7, F11) | WC4 `TestTheSlipStoresTheCaseFoldedAddress` |
| A person removed from the roster is refused a grant and every call under a slip they own within a minute, and an unreadable roster refuses (R3) | WC4 `TestARemovedPersonIsRefusedAfterTheReread`, `TestARemovedPersonsSlipIsRefused`, `TestAnUnreadableRosterRefuses` |
| "No token held" is answered before the node is read (R3) | WC4 `TestNoTokenHeldIsAnsweredBeforeTheNode` |
| The claim secret is answered by the claim alone and never reaches a job's environment (F14) | WC7 `TestTheGrantCarriesNoClaimSecret`; WC8 `TestTheClaimSecretStaysOutOfTheEnvironment` |
| A run whose launcher left the roster halts with a reason naming the removal, and moves to nothing (D3146) | WC8 `TestARemovedOwnersRunHaltsNamingTheRemoval` |
| A refused grant names no account, repository or person, and every grant and refusal is one audit line (R3) | WC4 `TestAGrantRefusalNamesNothing`, `TestEveryGrantIsAudited` |
| The intake's address comes from the workspace's own project config and from nothing of a member's (R13) | WC9 `TestTheIntakeAddressIsNeverAMembers` |
| The workspace's slip is kept in the workspace's database and nothing is written to a member (R3) | WC4 `TestTheSlipIsWrittenToTheWorkspaceOnly` |
| A member's slip never reaches the workspace's service, and the workspace's never reaches the project's (R4) | WC8 `TestEachSlipGoesToItsOwnService`; WC4 `TestWorkspaceScopeRefusesAMembersSlip` |
| A personal token is served only to a call whose slip its owner was granted (D2962) | WC5 `TestAnotherPersonsRunIsNeverServedTheToken` |
| With the member's switch off, no personal token is served and the shared entries are (D2972) | WC5 `TestTheSwitchOffServesNoPersonalToken` |
| A missing container refuses and never falls to a bundle entry (D2974) | WC5 `TestAMissingPersonalTokenRefuses` |
| An exhausted token moves on to the admitted shared entries, and the paid call that follows is reserved (D3144, D2965) | WC5 `TestAnExhaustedTokenMovesToTheSharedEntries`, `TestThePaidCallAfterTheMoveIsReserved` |
| The workspace's account reads a person's `claude-oauth-token` container and no other personal or `fugaro_p_` one (R6) | WC2 `TestTheWorkspaceAccessorIsThePersonsOneName` |
| A roster that cannot be re-read refuses a call under a slip as unavailable, and the run waits and moves to nothing (R3) | WC4 `TestAnUnreadableRosterRefusesACallAsUnavailable`; WC8 `TestAnUnavailableRosterIsWaitedFor` |
| A failed read of a person's container refuses (R7) | WC5 `TestAFailedReadOfAPersonsTokenRefuses` |
| Roster people are admitted at the intake and at nothing else of the workspace project's installation (R5, F10) | WC3 `TestRosterPeopleAreNotLaunchersOfTheWorkspaceProject` |
| Removing a person needs no operator repair (F15) | WC2 `TestWorkspacePlanAdmitsRemovingAPersonsInvokeGrant` |
| `fugaro run` asks for a grant only on a workspace route with the switch on, and an answer of "no token held" starts the run as before (R8, D3141) | WC7 `TestNoGrantIsAskedOnAProjectRoute`, `TestNoTokenHeldStartsTheRunAsBefore` |
| `--no-personal-token` asks for no grant, and no command waits for input (D3142) | WC7 `TestTheFlagSkipsTheGrant`; the closed-stdin harness |
| A failed grant never stops a launch: the run starts on the shared entries and says so (D3147) | WC7 `TestAFailedGrantStartsTheRunWithoutIt` |
| Offboarding removes the person's containers at the workspace (F21) | WC10 `TestUserRmSweepsTheWorkspacesIntake` |
| `fugaro update` converges a workspace and a member from the previous release with a run in flight (R11) | WC11 `TestUpdateNamesTheStepsThatReachAPersonalRun`; Check 851 |

## 3. Contracts

Each contract depends on no task. It holds types, constants, a short document and stubs, and
nothing that computes. Both are over credentials and a wire format, so each is launched at the
strictest rigor and gets an independent review (`fugaro review --pr N`) before a side is written
against it.

### C1: the person wire contract
- Acceptance: one place names the key of the member's switch and its home (R9, U8); the
  environment name a job reads the workspace's slip from; the request and the answer of a
  workspace grant (the job account, the repository, the run, the one personal name; the slip's id
  or "no token held", R8, and never a claim secret, which the claim alone answers, F14); the one
  class a refused grant is answered with; the
  two header names a workspace slip travels in; and the one personal name this plan serves
  (`claude-oauth-token`, A1). The grant's shapes decode strictly. A stub reader answers "the
  switch is off".
- Files: `internal/creds/person.go` (new), `internal/creds/person_stub.go` (new, stub),
  `docs/workspace-credentials/contract-person.md` (new).
- Interfaces: produces the names, `creds.PersonGrant`, `creds.PersonSwitchReader`; consumed by
  WC5, WC7, WC8, WC11. WC4 and WC6 build its other side.
- Tests: `TestPersonGrantDecodesStrictly`, `TestStubSwitchIsOff`.
- Command: `go test ./internal/creds/ -run 'PersonGrant|StubSwitch' -count=1`.
- Red step: an unknown key in a grant is accepted until the strict decode lands.
- Depends on: none.
- Commit: `creds: the person wire contract (C1, R3, R4, R9)`.
- Reviewer tier: opus, mandatory independent review.

### C2: the person scope contract
- Acceptance: `credsvc` declares what a scope that serves people adds to
  workspace-service's `Scope`, as a second interface a scope may also implement: the launcher
  verifier and the slip store for the call, whether a presented slip is this service's own, and
  the reader that returns the personal candidate for a call's slip. It declares the constructor
  variable the workspace's scope calls for that reader, and the configuration a person scope is
  built from (the roster's reader, the launcher verifier, the slip store) with the variable
  `fugaro credentials serve` sets it through. The stub reader offers nothing.
  `internal/credsvc/scope.go` is not edited: it is workspace-service's contract file.
- Files: `internal/credsvc/scope_person.go` (new), `internal/credsvc/scope_person_stub.go` (new,
  stub), `docs/workspace-credentials/contract-scope-person.md` (new).
- Interfaces: produces `PersonScope`, `PersonalReader`, `NewPersonalReader`, `PersonScopeConfig`;
  consumed by WC5 and WC14. WC4
  builds its other side.
- Tests: `TestStubPersonalReaderOffersNothing`.
- Command: `go test ./internal/credsvc/ -run StubPersonalReader -count=1`.
- Red step: the test fails to compile until the types exist.
- Depends on: none.
- Commit: `credsvc: the person scope contract (C2, R3, R7)`.
- Reviewer tier: opus, mandatory independent review.

## 4. Slices and tasks

**Slice A, the shared remainder (useful alone):** WC1.
**Slice B, the gate:** WC0. The owner answered every finding of WC0's review on 2026-11-22 (O1,
threat-model §11), so the tasks of slices C to F are launchable by their plan edges.
**Slice C, the workspace owner's side (inert until a member turns its switch on):** WC2, WC3, WC4,
WC5, WC14.
**Slice D, the member's side:** WC6, WC8.
**Slice E, the person's side:** WC7, WC9, WC10.
**Slice F, reports, checks and documents:** WC11, WC12, WC13. **Integration:** I1.

### WC0: the threat model for the personal half
- Acceptance: `threat-model.md` states, for personal tokens at the workspace: who can read a
  token (every workspace admin, F20; the workspace's service account), who can replace or delete
  one (the intake's account, D3143), who can spend one and for how long (F14 and F24, in the words of O2, and that with D3142 the
  switch is the member admins' own; F24's reach is every Cloud Run service the person can invoke),
  what a stolen workspace-admin credential and a hostile member admin can each do, what
  offboarding does and does not end (a removed person stays admitted at the intake until the
  workspace owner's next `fugaro update` and can store a token nobody can use; a run halted under D3146 can be launched again and is then
  served the member's shared entries under D3147), what a roster
  person who holds a token can learn from the grant route (R3), and the three settled statements
  it amends (the workspaces spec §4, workspace-service R18, and the spec's §6 on a run in flight,
  D3146). D2825 is stated as a residual risk, not amended (F20). F24's window is stated as the token's
  hour plus WC4's claim deadline plus 24 hours from the claim (the review corrected "about 25
  hours"). Every residual risk is numbered. An independent review reads
  it, and each of its findings is answered by the owner in the document before the task is done.
- Files: `docs/workspace-credentials/threat-model.md` (new).
- Interfaces: consumes reconciliation F12 to F16, F19, F20, F23 to F25; produces the numbered risks WC13 cites.
- Tests: `go test ./internal/cli/ -run Docs -count=1` (no test pins the new file; the run proves
  nothing else moved).
- Command: `go test ./internal/cli/ -run Docs -count=1`.
- Red step: none; a document. Its acceptance is the owner's gate and no test can fail it.
- Depends on: none.
- Commit: `docs(workspace-credentials): the threat model for a person's token at the workspace (WC0)`.
- Reviewer tier: opus, mandatory independent review.
- Owner-run: the owner answers each review finding; no agent answers one.

### WC1: a provider key is shared like any model entry
- Acceptance: `fugaro workspace share` accepts a provider key the workspace's bundle holds, the
  project's one or a named entry; the node carries it as a model entry; the workspace's offering
  serves it to the members its allow-list names; a Claude model through it is refused until
  Check 50 has passed, by the selection's existing rule (D3000); a repository credential is still
  refused, with a reason.
- Files: `internal/cli/workspace_share.go`, `internal/cli/workspace_share_test.go`,
  `internal/credsvc/bundle_workspace.go`, `internal/credsvc/bundle_workspace_test.go`,
  `website/content/cli-reference.md`.
- Interfaces: consumes `credsvc.CredentialKey` and `KindProvider`; produces nothing new.
- Tests: `TestShareAcceptsAProviderKey`, `TestShareStillRefusesARepositoryCredential`,
  `TestASharedProviderKeyIsOfferedToItsMembers`, `TestAClaudeModelThroughASharedProviderKeyWaits`.
- Command: `go test ./internal/cli/ -run 'ShareAccepts|ShareStillRefuses' -count=1 && go test ./internal/credsvc/ -run 'SharedProviderKey' -count=1`.
- Red step: `TestShareAcceptsAProviderKey` fails on `workspaceSharedKind`'s refusal (U7 is settled
  here).
- Depends on: none in this plan. Outside it: workspace-service WS19 (the same two files).
- Commit: `workspace: a provider key is shared like any model entry (WC1, D2978, F7)`.
- Reviewer tier: opus, mandatory independent review.

### WC2: the workspace root admits the roster's people
- Acceptance: the workspace root binds an invoke grant on the workspace's service to each `human`
  of the roster, and one accessor for the workspace's account conditioned to the `fugaro_u_`
  prefix and the one name served (R6), its condition using `extract("/secrets/{secret}")` as the
  intake's own does (`intake.tf`); `fugaro update` renders the people from the roster; the cover admits exactly those and
  the deletion of a person's invoke grant, and refuses an accessor that is wider.
- Files: `deploy/terraform/gcp/roots/workspace/main.tf`,
  `deploy/terraform/gcp/roots/workspace/variables.tf`, `internal/infra/tf/workspace.go`,
  `internal/infra/tf/workspace_test.go`, `internal/cli/workspace_create.go`,
  `deploy/terraform/gcp/roots/workspace/tests/workspace.tftest.hcl`; and the pinned
  sentence of `SECURITY.md` with its test and `website/content/security.md` (§5's exception).
- Interfaces: consumes `workspace.Roster`; produces the `people` variable.
- Tests: `TestTheWorkspaceAccessorIsThePersonsOneName`,
  `TestWorkspacePlanAdmitsRemovingAPersonsInvokeGrant`, `TestWorkspaceVarsNameTheRostersPeople`.
- Command: `go test ./internal/infra/tf/ -run Workspace -count=1 && go test ./internal/cli/ -run 'WorkspaceVars|SecurityMd' -count=1 && terraform fmt -check -recursive deploy/terraform`.
- Red step: the cover refuses a plan that binds a person's invoke grant.
- Depends on: WC0 (the owner's answers).
- Commit: `infra: the workspace root admits the roster's people (WC2, D2971, R6)`.
- Reviewer tier: opus, mandatory independent review.

### WC3: the intake admits the roster's people
- Acceptance: the installation module takes a list of intake people of its own; the intake's
  invoker set and its in-process list are the installation's launchers and that list; it is empty
  unless the installation is a workspace's project, where `fugaro update` renders it from the
  roster; those people are admitted to the `fugaro_u_` half under their own id and to nothing
  else of that installation.
- Files: `deploy/terraform/gcp/modules/installation/intake.tf`,
  `deploy/terraform/gcp/modules/installation/variables.tf`, `internal/infra/launchers.go`,
  `internal/infra/tfvars.go`, `internal/cli/intake_serve.go`, `internal/intake/server.go`, with
  `internal/infra/launchers_test.go`, `internal/cli/intake_serve_test.go` and
  `internal/intake/server_test.go`.
- Interfaces: consumes `workspace.Roster`; produces the intake's second list.
- Tests: `TestRosterPeopleAreNotLaunchersOfTheWorkspaceProject` (in `internal/infra`),
  `TestAnInstallationsIntakeAdmitsItsLaunchersOnly` (in `internal/intake`, as the two below), `TestARosterPersonWritesOnlyUnderTheirOwnId`,
  `TestARosterPersonIsRefusedTheProjectPrefix`.
- Command: `go test ./internal/intake/ -run 'RosterPerson|IntakeAdmits' -count=1 && go test ./internal/infra/ -run 'RosterPeople' -count=1 && terraform fmt -check -recursive deploy/terraform`.
- Red step: a roster person who is not a launcher is refused by `VerifyLauncher`.
- Depends on: WC0.
- Commit: `intake: a workspace project's intake admits the roster's people (WC3, D3143, R5)`.
- Reviewer tier: opus, mandatory independent review.

### WC4: the workspace's grant and claim
- Acceptance: workspace mode serves the grant and claim routes; the grant admits a roster `human`
  by the verified address, re-reading the roster as the node is re-read; it holds the job account
  and the repository to the node and the names to the caller's own token; the slip is written to
  the workspace's database; a call that presents a workspace slip is verified against it and one
  that presents any other slip is refused, as is one whose slip's owner has left the roster;
  "no token held" is answered before the node is read; a refused grant is one class that names
  nothing, and each grant and refusal is one audit line.
  `PersonScope` is implemented by the workspace's scope and by no other. Per the owner's answers to
  WC0's review (O1, threat-model §11): **a workspace slip carries a claim deadline counted from the
  grant**, chosen and pinned by WC4, after which it cannot be claimed (today `creds.Slip.Expires()`
  is `Claimed.At + MaxRuntime`, so an unclaimed grant never expires); **a grant is bound to the
  members the person is a launcher of, checked against that member's own list** (D3145 reopened;
  open design question for the brief: where the workspace's service reads a member's own list, since
  it holds no grant to read it); and **the slip stores the person's case-folded verified address at
  grant time**, which the check on every call needs (F11).
- Files: `internal/credsvc/scope_workspace.go`, `internal/credsvc/workspace.go`,
  `internal/credsvc/slip.go`, `internal/credsvc/server.go`,
  `internal/credsvc/scope_workspace_test.go`, `internal/credsvc/scope_installation_test.go` (where
  `TestAScopeThatAcceptsNoSlipRefusesOne` pins the old wording); and the pinned sentences of `SECURITY.md` with
  their tests and `website/content/security.md` (§5's exception).
- Interfaces: consumes C1 (the grant's shapes) and C2 (`PersonScope`); produces the routes WC7 and
  WC8 call.
- Tests: the WC4 tests of §2 (among them `TestAnUnclaimedWorkspaceSlipExpiresAtTheClaimDeadline`,
  `TestAGrantIsBoundToTheMembersThePersonLaunchesFor` and `TestTheSlipStoresTheCaseFoldedAddress`), and
  `TestWorkspaceModeServesTheGrantAndTheClaim`. It rewrites
  `TestWorkspaceScopeRefusesACallThatPresentsASlip` as `TestWorkspaceScopeRefusesAMembersSlip`.
- Command: `go test ./internal/credsvc/ -run 'Grant|WorkspaceScope|RemovedPerson|UnreadableRoster|TheSlipIsWritten|ServiceAccountIsRefused|UnreadableRosterRefusesACall|NoTokenHeld|EveryGrantIsAudited|AcceptsNoSlip' -count=1 && go test ./internal/cli/ -run 'DocsWorkspaces|SecurityMd' -count=1`.
- Red step: `TestWorkspaceModeServesTheGrantAndTheClaim` fails on `servesWorkspaceRoute`.
- Depends on: C1 and C2 (it builds their other side), WC0.
- Commit: `credsvc: the workspace's own grant and launcher class (WC4, D2970, D2962, R3)`.
- Reviewer tier: opus, mandatory independent review.

### WC14: workspace mode is handed the roster and its slips
- Acceptance: `fugaro credentials serve` in workspace mode builds the roster's reader, the
  launcher verifier and a slip store over the workspace's own database and hands them to the
  scope's resolver through C2's configuration; without them the scope serves no grant, as today.
- Files: `internal/cli/credentials_serve.go`, `internal/cli/credentials_serve_test.go`.
- Interfaces: consumes C2 (the configuration's shape) and `workspace.Roster`'s store; produces
  the wiring WC4's scope reads.
- Tests: `TestWorkspaceModeIsHandedTheRosterAndItsSlips`, `TestWorkspaceModeWithoutARosterGrantsNothing`.
- Command: `go test ./internal/cli/ -run 'WorkspaceModeIsHanded|WorkspaceModeWithoutARoster' -count=1`.
- Red step: `credentialsWorkspaceService` passes no roster.
- Depends on: C2, WC0.
- Commit: `credentials serve: workspace mode is handed the roster and its slips (WC14, R3)`.
- Reviewer tier: opus, mandatory independent review.

### WC5: the personal candidate
- Acceptance: for a call under a workspace slip that names the person's token, with the member's
  switch on, the offering puts that token first; a missing container or one with no version
  refuses and names `fugaro secrets user set`; a read that failed refuses; an
  exhausted token is skipped for the next admitted candidate; a call on the token reserves
  nothing and a paid call that follows is reserved. An installation's path is unchanged.
- Files: `internal/credsvc/personal_workspace.go` (new),
  `internal/credsvc/personal_workspace_test.go` (new), `internal/credsvc/cascade.go`,
  `internal/credsvc/dynamicsecrets.go`.
- Interfaces: consumes C2 (`PersonalReader`, the slip's owner) and C1 (`PersonSwitchReader`, the
  name); produces the reader WC4's scope calls through C2's constructor variable.
- Tests: the WC5 tests of §2.
- Command: `go test ./internal/credsvc/ -run 'PersonalToken|ExhaustedToken|PaidCallAfter|SwitchOff|AnotherPersons|StillFallsToTheBundle|FailedReadOfAPersons' -count=1`.
- Red step: `TestAMissingPersonalTokenRefuses` is answered the bundle's entry by
  `Server.resolveEntry`'s fall-through. It also records what U9 finds.
- Depends on: C1, C2, WC0. Outside this plan: workspace-service WS8 (the meter on the paid-call
  path, and the same file `cascade.go`).
- Commit: `credsvc: a person's own token is the first candidate of their runs (WC5, D2974, D3144, R7)`.
- Reviewer tier: opus, mandatory independent review.

### WC6: the member's switch
- Acceptance: a member's admin sets the switch with `fugaro settings set`; the member's
  `fugaro update` publishes it as one top-level key of the policy; absent reads as off; an older
  reader decodes past it; the policy's version does not move; the real `PersonSwitchReader` reads
  it from a member's published policy.
- Files: `internal/projectpolicy/policy.go`, `internal/projectpolicy/policy_test.go`,
  `internal/settings/registry.go`, `schemas/settings.schema.json`,
  `internal/cli/init_policy_publish.go`; `internal/localcfg/localcfg.go` if C1 places the
  switch there (U8); and the settings reference the registry's own tests hold a new key to.
- Interfaces: consumes C1 (the key and its home); produces the published key WC7 and WC11 read.
- Tests: `TestTheSwitchIsOffWhenAbsent`, `TestTheSwitchIsPublishedByUpdate`,
  `TestAnOlderReaderDecodesPastTheSwitch`.
- Command: `go test ./internal/projectpolicy/ -run 'TheSwitch|OlderReader' -count=1 && go test ./internal/cli/ -run TheSwitchIsPublished -count=1`.
- Red step: the key is listed as unknown and read as nothing.
- Depends on: C1 (it builds its other side), WC0.
- Commit: `policy: the member's switch for personal workspace tokens (WC6, D2972, R9)`.
- Reviewer tier: opus, mandatory independent review.

### WC7: `fugaro run` asks the workspace for a grant
- Acceptance: at launch, when the repository's model route is the workspace and the switch is
  on, `fugaro run` asks the workspace's service for a grant; on a slip it hands the job the slip
  by C1's environment name and prints one line naming the workspace and the flag; on "no token
  held" it starts the run as before and prints nothing; `--no-personal-token` asks for none; the
  grant's answer carries no claim secret and none is put in the job's environment; a refusal or an unreachable service starts the run
  without it and says why; a service-account launch asks for none. It never waits for input.
- Files: `internal/cli/run_personal.go` (new), `internal/cli/run_personal_test.go` (new),
  `internal/cli/run.go` (the flag and one call), `website/content/cli-reference.md`,
  `internal/cli/noprompt_harness_test.go`.
- Interfaces: consumes C1 and the key WC6 publishes; produces the job's environment entry WC8
  reads.
- Tests: the WC7 tests of §2 (`TestNoGrantIsAskedOnAProjectRoute`,
  `TestNoTokenHeldStartsTheRunAsBefore`, `TestTheFlagSkipsTheGrant`,
  `TestAFailedGrantStartsTheRunWithoutIt`), `TestTheGrantCarriesNoClaimSecret`, and `TestTheLaunchSaysWhichTokenItDrawsOn`.
- Command: `go test ./internal/cli/ -run 'NoGrantIsAsked|NoTokenHeld|TheFlagSkips|AFailedGrant|TheGrantCarries|TheLaunchSays|CliReference|ClosedStdin' -count=1`.
- Red step: `TestNoGrantIsAskedOnAProjectRoute` fails while the grant is asked unconditionally.
- Depends on: C1; WC6 (it reads the field WC6 adds to the published policy); WC0.
- Commit: `run: a person's runs draw on their own workspace token (WC7, D3142, R8)`.
- Reviewer tier: opus, mandatory independent review.

### WC8: the gateway carries the workspace's slip
- Acceptance: a run that was handed a workspace slip claims it at the workspace's service at
  bootstrap and sends it on every call to that service and on none to the project's; the
  project's slip goes on unchanged to the project's service only; the claim secret is kept as
  the project's is (`slipHold`) and never in the environment; a call refused because the slip's
  owner left the roster halts the run with a reason that names the removal and moves to no other
  credential (D3146); a call refused as unavailable is waited for as an unreachable service is
  (R3); a run handed no workspace slip behaves exactly as today.
- Files: `internal/gateway/service.go`, `internal/gateway/gateway.go`,
  `internal/gateway/workspace_route_test.go`, `internal/runner/gateway.go`,
  `internal/runner/budget_session.go`, `internal/runner/gateway_service_internal_test.go`; and the pinned sentence of `SECURITY.md` with its test and
  `website/content/security.md` (§5's exception).
- Interfaces: consumes C1 (the environment name and the headers); produces nothing new.
- Tests: `TestEachSlipGoesToItsOwnService`, `TestARunWithNoWorkspaceSlipIsUnchanged`,
  `TestTheWorkspaceSlipIsClaimedAtBootstrap`, `TestTheClaimSecretStaysOutOfTheEnvironment`,
  `TestARemovedOwnersRunHaltsNamingTheRemoval`, `TestAnUnavailableRosterIsWaitedFor`. It rewrites
  `TestTheWorkspaceUpstreamCarriesNoSlip` as `TestTheWorkspaceUpstreamCarriesNoMembersSlip`.
- Command: `go test ./internal/gateway/ -run 'Slip' -count=1 && go test -timeout 40m ./internal/runner/ -run 'WorkspaceSlip|ClaimSecretStays|RemovedOwners|UnavailableRoster' -count=1 && go test ./internal/cli/ -run SecurityMd -count=1`.
- Red step: `TestEachSlipGoesToItsOwnService` fails: the workspace's upstream sends no slip.
- Depends on: C1, WC0.
- Commit: `gateway: a workspace slip goes to the workspace's service alone (WC8, R4)`.
- Reviewer tier: opus, mandatory independent review.

### WC9: storing a token at the workspace
- Acceptance: a roster person who is no launcher of the workspace's project runs the existing
  `fugaro secrets user set|ls|rm --project <the workspace's project>` and reaches that project's
  intake at the address read from its own config (`intakeURLOf`), **from a source only the
  workspace's admins write**, and not from `credentials_service.url` of the published shared file
  that any launcher can write (the owner's answer to finding 3; the same weakness for ordinary
  installations is a follow-up to the dynamic-delivery work); no flag is added and no address
  is read from a member's pin, route or policy (R13, D3148); a refusal by the intake says the
  person is not on the workspace's roster yet, or that the workspace owner's `fugaro update` has
  not run since they were added; the value comes from stdin only; the command refuses in an
  agent's session as today.
- Files: `internal/cli/secrets_intake.go`, `internal/cli/secrets_intake_test.go`.
- Interfaces: consumes WC3's admission at run time only; produces the command Check 850 runs.
- Tests: `TestSecretsUserSetReachesTheWorkspacesIntake`, `TestTheIntakeAddressIsNeverAMembers`,
  `TestTheIntakesRefusalNamesTheRoster`.
- Command: `go test ./internal/cli/ -run 'SecretsUserSet|IntakeAddressIsNever|IntakesRefusalNames' -count=1`.
- Red step: the refusal names the installation's launchers and not the roster.
- Depends on: WC0.
- Commit: `secrets: a person stores their token once, at the workspace (WC9, D2960, D2971)`.
- Reviewer tier: opus, mandatory independent review.

### WC10: offboarding sweeps the workspace
- Acceptance: `fugaro workspace user rm` also asks the workspace's intake to delete every
  container the person owns, after the members' sweeps and whatever they answered; its output
  says that a run in flight on that person's token halts within a minute (R3), whatever the sweep
  answered.
- Files: `internal/cli/workspace_offboard.go`, `internal/cli/workspace_offboard_test.go`,
  `internal/cli/workspace_user.go`, `internal/cli/workspace_user_test.go`.
- Interfaces: consumes the intake's delete route and `memberSweeper`'s pattern; produces nothing
  new.
- Tests: `TestUserRmSweepsTheWorkspacesIntake`, `TestTheSweepRunsWhenAMemberRefused`.
- Command: `go test ./internal/cli/ -run 'UserRmSweeps|TheSweepRuns' -count=1`.
- Red step: the workspace's containers are still there after `user rm`.
- Depends on: WC0.
- Commit: `workspace: offboarding removes a person's tokens at the workspace (WC10, F21)`.
- Reviewer tier: opus, mandatory independent review.

### WC11: doctor, and update's lines
- Acceptance: `fugaro doctor` says, for a member, whether the switch is on, and flags a switch
  that is on where the model route is the project's (D3141's shadow); it calls neither the
  workspace's service nor its intake, so it sends a person's identity nowhere (F24); `fugaro update` names, when
  runs are running, the two new steps of R11 that can reach one, and the step-by-step audit in
  `docs/design/update-in-flight.md` gains them.
- Files: `internal/cli/doctor_workspace.go`, `internal/cli/doctor_workspace_test.go`,
  `internal/cli/update_in_flight.go`, `docs/design/update-in-flight.md`.
- Interfaces: consumes C1 and the key WC6 publishes.
- Tests: `TestDoctorFlagsASwitchTheRouteShadows`, `TestUpdateNamesTheStepsThatReachAPersonalRun`.
- Command: `go test ./internal/cli/ -run 'DoctorFlagsASwitch|UpdateNamesTheSteps|Docs' -count=1`.
- Red step: doctor is silent about a shadowed switch.
- Depends on: C1; WC6 (the field it reads).
- Commit: `doctor: the personal half's state, and update's lines for runs in flight (WC11, R11)`.
- Reviewer tier: standard.

### WC12: Checks 850 and 851, as scripts
- Acceptance: each check is one script the owner runs, with a hermetic test of the script's own
  logic and a row in the live checklist; neither is run by an agent.
- Files: `scripts/live-checks/check-850.sh` (new), `scripts/live-checks/check-850_test.sh` (new),
  `scripts/live-checks/check-851.sh` (new), `scripts/live-checks/check-851_test.sh` (new),
  `docs/gcp-live-checklist.md`.
- Interfaces: consumes the built CLI of WC7 and WC9.
- Tests: the two `_test.sh` files, and the checklist's documents test.
- Command: `bash scripts/live-checks/check-850_test.sh && bash scripts/live-checks/check-851_test.sh && go test ./internal/cli/ -run Docs -count=1`.
- Red step: the checklist test fails on a row with no script.
- Depends on: WC7, WC9 (the scripts call the commands those tasks add).
- Commit: `checks: 850 and 851 for a person's token at the workspace (WC12)`.
- Reviewer tier: standard.
- Owner-run: `bash scripts/live-checks/check-850.sh` and `bash scripts/live-checks/check-851.sh`,
  after the release that carries the personal half.

### WC13: documents and the release note
- Acceptance: the release note's `### For operators` says "run `fugaro update`" and that turning
  the personal half on is configuration (§9); `docs/workspaces/README.md` records that the
  workspace's service now admits roster people on two routes and points here; the `working`
  skill names `--no-personal-token` and how a person stores a token at the workspace (D3148).
- Files: the next release's notes under `docs/releases/`, `docs/workspaces/README.md`,
  `plugin/skills/working/SKILL.md`. This folder's README and its status row are the
  coordinator's, moved when I1 merges.
- Interfaces: consumes WC0's numbered risks.
- Tests: the documents tests and the plugin's command-and-flag test.
- Command: `go test ./internal/cli/ -run Docs -count=1 && go test ./plugin/... -count=1`.
- Red step: the plugin's test fails on a flag the CLI does not have until WC7 has merged.
- Depends on: WC4, WC5, WC7, WC9 (it describes what they built).
- Commit: `docs: a person's token at the workspace, and the release note (WC13)`.
- Reviewer tier: opus, mandatory independent review (it edits a plugin skill).

### I1: integration
- Acceptance: the two stub files are deleted and the real sides are wired; **the workspace-mode
  serve path sets `credsvc.PersonSwitchForServe` to WC6's real reader**, a scope-aware read of the
  calling member's published policy (the switch is asked per call of the member the caller's own
  verified project names, so the read is the call's own `Scope.Policy` and not a reader built at
  startup). Without it a workspace-mode process whose half WC14 wired refuses every call whose slip
  names a personal token with `personalSwitchOff`, which is what the stub WC5's file defaults to
  answers for every project (WC5's comment on the variable records the gap, and why the wiring is
  this task's and not WC14's). One end-to-end test, over the fakes, has a roster person store a
  token, launch in a member with the switch on and be served it; a second person's run in the same
  member is served the shared entry; the switch off serves no personal token; a deleted container
  refuses; an exhausted token moves to the shared entry and that call is reserved.
- Files: `internal/creds/person_stub.go` (deleted), `internal/credsvc/scope_person_stub.go`
  (deleted), `internal/credsvc/personal_workspace.go` (the scope-aware switch reader),
  `internal/cli/credentials_serve.go` (the assignment),
  `internal/cli/workspace_personal_end_to_end_test.go` (new).
- Interfaces: consumes the tasks it depends on.
- Tests: `TestAPersonsTokenEndToEnd`, `TestWorkspaceModeAnswersTheSwitchThroughTheRealReader`.
- Command: `go test ./internal/cli/ -run 'APersonsTokenEndToEnd|WorkspaceModeAnswersTheSwitch' -count=1 && go build ./... && go vet ./...`.
- Red step: the build fails on the stubs' duplicate symbols until they are deleted, and
  `TestWorkspaceModeAnswersTheSwitchThroughTheRealReader` is answered `personalSwitchOff` by the stub
  switch WC5's file defaults to until the assignment is written.
- Depends on: C1, C2, WC2, WC3, WC4, WC5, WC6, WC7, WC8, WC9, WC14.
- Commit: `workspace: a person's token, wired end to end (I1)`.
- Reviewer tier: opus, mandatory independent review.

## 5. Parallel execution

The decoupling pass is on (`planning.decoupling` is `true` for this repository) and the
repository's mode is `startup`, so the pass is applied aggressively.

**Levels.** Tasks in one level run at once. **A launcher follows this table.** O1's gate is passed:
the owner answered WC0's review on 2026-11-22, so level 1 starts by its plan edges.

| Level | Tasks |
| --- | --- |
| 0 | C1, C2, WC0, WC1 |
| 1 | WC2, WC3, WC4, WC5, WC6, WC8, WC9, WC10, WC14 |
| 2 | WC7, WC11 |
| 3 | WC12, WC13, I1 |

**What waits on another plan.** The graph cannot hold a task of another plan, so these are stated
here and a launcher checks them by hand:

| Task | Waits on | Why |
| --- | --- | --- |
| WC1 | workspace-service WS19 | the same two files |
| WC5 | workspace-service WS8 | the meter, and `cascade.go` |
| WC8 | workspace-service WS18 | `internal/runner/gateway.go` |
| WC13 | workspace-service WS21 | `docs/workspaces/README.md`, the `working` skill and the release notes |
| I1 | workspace-service I1 | its end-to-end test runs over that plan's wired sides |

**Critical path.** Four steps: WC0, WC6, WC7, I1. The graph read reports
`critical path (4 tasks): WC0 -> WC6 -> WC7 -> I1`, with WC0 the bottleneck, and
`gain: 4 broken, 18 kept, 5 added; before 4 tasks, weight 9; after 4 tasks, weight 7; improves`.
The path is as long as before the pass and lighter: what the contracts buy is that WC5, WC7 and
WC8 no longer queue behind WC4, the largest task.

**Where the graph and this table differ.** As in workspace-service (its O5), the graph records a
contract's consumers only. WC4 and WC6 build C1's other side and WC4 builds C2's; WC14
consumes C2; they carry
no edge to their contract in the graph and each still starts after it has merged. WC11 reads C1's
names too; the graph refuses that edge for the same reason, and WC11 already waits on WC6, which
waits on C1.

**Edges broken, each by a contract and rejoined in I1.**
- WC7 after WC4: the launcher needs the grant's request and answer, not the route's code (C1).
- WC8 after WC4: the gateway needs the header names and the claim's shape (C1).
- WC5 after WC6: the candidate needs to ask "is the switch on", not who publishes it (C1).
- WC5 after WC4: the candidate needs the slip's owner for a call, not the grant's code (C2).

**Edges kept.** A field one task adds and another reads (WC7 and WC11 after WC6); the built CLI
(WC12 after WC7 and WC9); behaviour to describe (WC13 after WC4, WC5, WC7 and WC9); the owner's answers
(every personal task after WC0); the wiring (I1 after the sides it wires). WC8 once waited on WC7 for
the environment name; that name is in C1, so the edge is gone without a break of its own.

**Shared files.** No level holds two writers of one file, with the one exception below the table.

| File | Writers, by level |
| --- | --- |
| `internal/cli/workspace_share.go`, `internal/credsvc/bundle_workspace.go` | WC1 (0) |
| `website/content/cli-reference.md` | WC1 (0), WC7 (2) |
| `internal/cli/noprompt_harness_test.go` | WC7 (2) |
| `deploy/terraform/gcp/roots/workspace/*`, `internal/infra/tf/workspace.go`, `internal/cli/workspace_create.go` | WC2 (1) |
| `deploy/terraform/gcp/modules/installation/*`, `internal/infra/launchers.go`, `internal/infra/tfvars.go`, `internal/cli/intake_serve.go`, `internal/intake/server.go` | WC3 (1) |
| `internal/credsvc/scope_workspace.go`, `workspace.go`, `slip.go`, `server.go`, `scope_installation_test.go` | WC4 (1) |
| `internal/cli/credentials_serve.go` | WC14 (1), I1 (3) |
| `internal/credsvc/personal_workspace.go` | WC5 (1), I1 (3) |
| `internal/credsvc/cascade.go`, `dynamicsecrets.go` | WC5 (1) |
| `internal/projectpolicy/policy.go`, `internal/settings/registry.go`, `schemas/settings.schema.json`, `internal/cli/init_policy_publish.go`, `internal/localcfg/localcfg.go` | WC6 (1) |
| `internal/gateway/service.go`, `internal/gateway/gateway.go`, `internal/runner/gateway.go`, `internal/runner/budget_session.go` | WC8 (1) |
| `internal/cli/secrets_intake.go` | WC9 (1) |
| `internal/cli/workspace_offboard.go`, `internal/cli/workspace_user.go` | WC10 (1) |
| `internal/cli/run.go`, `internal/cli/run_personal.go` | WC7 (2) |
| `internal/cli/doctor_workspace.go`, `internal/cli/update_in_flight.go`, `docs/design/update-in-flight.md` | WC11 (2) |
| `docs/gcp-live-checklist.md`, `scripts/live-checks/check-85*` | WC12 (3) |
| `docs/workspaces/README.md`, `plugin/skills/working/SKILL.md`, the release notes | WC13 (3) |
| the two stub files | C1, C2 (0), I1 (3) |

**The exception.** `SECURITY.md`, with `website/content/security.md` and the two tests that pin
its sentences, is edited by WC2, WC4 and WC8 at level 1, and the line citations in
`docs/workspaces/*.md` are moved by whichever task shifts the cited code. Each such edit is one
sentence or one line number in its own paragraph (F16). The coordinator merges those tasks back to
back and resolves a conflict locally; none is a reason to order two tasks. It is the exception
workspace-service's plan takes for the same files.

## 6. The estimate

As `fugaro plan graph --plan docs/workspace-credentials/implementation-plan.md` printed it:

`ready in 210–840 min, no history yet for 20 tasks (20 tasks; seeds for 20; from the seeds: no history is cached on this machine yet)`

The graph counts 20 nodes: the 18 runs of this plan (two contracts, WC0 to WC14 and I1) and the
two contract changes of §7, which run only if raised.

It is informational and reserves no budget: it is what the tasks' tiers and this machine's history
predict, and it moves as runs finish.

## 7. Contract changes anticipated

- **R-person-wire** (C1, raised by WC6): if U8 shows the switch's home must be elsewhere, or an
  answer to WC0's review changes a name or a shape. Reaches WC4, WC5, WC7, WC8, WC11.
- **R-person-scope** (C2, raised by WC4, or by WC5 through it): if the candidate needs something of the call the
  interface does not carry (U9). Reaches WC4, WC5, WC14.

A launched dependent is reached by a follow-up run on its pull request that names the contract
file by path.

## 8. Checks the plan takes

From the reserved block 850–859. Both are owner-run, as scripts (WC12), after the release.

| Check | What it measures |
| --- | --- |
| **850** | A roster person stores a token at the workspace with nothing but the two invoke grants (U6); launches in two members with the switch on and is served it in both; a second person's run is not; a roster person with no role on the workspace's project loads its config and reaches its intake by naming the project (U10, D3148); the workspace's account reads the container through the prefix-conditioned accessor (U5); the switch off and a deleted token each refuse as written; a person removed from the roster is refused within a minute, a run of theirs in flight included |
| **851** | `fugaro update` on a workspace and on a member at the previous release, with a run in flight on each: both converge with no other step, the run finishes, and a member that never turns the switch on sees no change |

## 9. After-deploy steps

**For an installation that does not use the feature: `fugaro update`, and nothing else.** Nothing
it holds changes, and a workspace whose members leave the switch off serves exactly what it served
before.

**Turning the personal half on is configuration**, and WC13's release note says so, as
workspace-service's O7 settled for the shared half:

- the workspace owner runs `fugaro update`, which binds the grants for the people on the roster; a
  person added later needs the next one (D2971);
- each member's admin sets the switch and runs `fugaro update`;
- each person stores their token once, naming the workspace's own project
  (`fugaro secrets user set --project <the workspace's project>`, D3148).

A project that still holds a model credential of its own keeps using it (D3141); `fugaro doctor`
says so (WC11).

## 10. Considered and declined

- **Per-run routing** (D3141's other option): it reverses D2989 for some runs and gives the route
  a second dimension the gateway must read.
- **Only on a flag** (D3142's other option): the shared entries would keep carrying the load.
- **A second intake with its own prefix** (D3143's other option): a second service, grant set and
  cover, and a second implementation of a prefix rule.
- **Wait and never switch** (D3144's other option): long stalls, and runs that end on their time
  budget.
- **Letting a removed person's run finish** (D3146's other option): the token would keep being
  spent for up to a day.
- **Refusing the launch on a failed grant** (D3147's other option): a brief outage of the
  workspace's service would block every launch by a person who holds a token.
- **Doctor asking whether the caller holds a token** (the first draft of WC11): it would send a
  person's identity to an address a member publishes (F24).
- **A `--workspace` flag, or a marker in the workspace's shared config** (D3148's other option):
  a name that resolves to a project is a name that can be made to resolve to the wrong one.
- **Requiring the roster's grant mark** (D3145's other option): the list is a record, not a
  grant.
- **A personal token for a project in no workspace** (D2966): the producer of F4 is written for
  the workspace's offering only. An installation keeps R7 of dynamic delivery; giving it the same
  candidate is its own small spec.
- **A command that moves a project's token to the workspace** (R10): a stored value cannot be
  read back.
- **The intake's address in the member's pin** (the first draft of F23, dropped after review
  round 1): a member admin could aim a person's raw token at an address of their own.
- **Trusting the member's launcher claim or its identity ticket** (F12): both live where a
  member's admins can write.
- **A person's forge token served per person** (A1): nothing in the spec asks for it.
- **Sharing a repository's own credential** (WC1): it is one repository's by construction.
- **Editing `internal/credsvc/scope.go`** (C2): it is another plan's contract file; a second
  interface leaves it alone.
