# The workspace's credentials service: implementation plan

**Status: plan only. Nothing is implemented and no task is approved; each task needs the owner's
explicit go by name.** It builds [spec.md](spec.md) as amended by
[reconciliation.md](reconciliation.md), which wins where the two disagree.

The answers that shaped it: a second Cloud Run service on the same binary (D2980); the run calls the
workspace's service directly (D2981); the route is decided once at the run's start (D2982); a
bounded wait when a service is unreachable (D2983); every credential kind at once (D2984, not the
recommendation); the run's beat with an off switch (D2985); a load check at 100 concurrent runs
(D2986); the alarm on demand in `doctor` (D2987). From workspace credentials: the member's copy wins
(D2973), `all_members` only after the meter (D2977), one cascade for every kind (D2978), the holder
proxies (D2979).

## 1. Conventions every task follows

Point, don't restate: `CLAUDE.md`, `.claude/rules/credentials-service.md` (every task under
`internal/creds`, `internal/credsvc`, `SECURITY.md`), `.claude/rules/terraform.md` (WS6, WS7, WS10),
`docs/no-prompt-policy/`, `docs/design/update-in-flight.md`.

- **Always.** Targeted tests, never a whole package. Hermetic: `internal/gcpfake`, `anthropicfake`,
  `t.TempDir()`. A task that edits `internal/credsvc/auth.go`, `server.go`, `workspace.go`,
  `internal/projectpolicy/workspace_credentials.go` or `internal/localcfg/localcfg.go` also runs
  `go test ./internal/cli/ -run DocsWorkspaces -count=1` and moves the line citations in
  `docs/workspaces/*.md` in the same change (F19). A new field in a published or `--json` shape is
  added, never retyped.
- **Ask first.** A new dependency; a change to a contract's files (raise a contract change, §7); a
  new command that waits for input (there is none in this plan).
- **Never.** A secret-shaped literal; a live cloud call from a test or an agent; a confirmation
  step; an edit under `.github/workflows/`; a second implementation of a route (R1); a write by the
  workspace's service to a member's database (R8); a credential's value in a log, an error, a
  header or a test name.

## 2. Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| An installation's service behaves exactly as today after the seam lands | WS1 `TestInstallationScopeIsTodaysBehaviour` and the existing `internal/credsvc` suites it must not edit |
| A caller's member comes from the verified job account and nothing in the request | WS2 `TestWorkspaceScopeIgnoresADeclaredMember` |
| A call routed to the workspace still passes the allow-list, the member's policy and the repository's admission | WS2 `TestWorkspaceScopeOffersOnlyTheIntersection`; WS16, WS17 the same for forge entries |
| The workspace's service writes nothing to a member | WS2 and WS8 run against a member database fake that fails on any write |
| A reservation precedes every paid call, probes included, and a failed settle refuses | WS8 `TestEveryPaidCallIsReserved`, `TestAFailedSettleRefuses` |
| A project with no credential of its own is never called for a model or forge call | WS11 `TestARunRoutedToTheWorkspaceNeverCallsTheProject` |
| A pin without an address converges untouched | WS3 `TestAPinWithoutAnAddressIsValid`, WS7 `TestUpdateConvergesAWorkspaceFromThePreviousRelease` |
| Version 2 of the node is never published to a service that cannot read it | WS4 `TestWriterRefusesVersionTwoForAnOlderService` |
| `fugaro update` deploys the service while a run is in flight and changes nothing that run holds | WS7 `TestUpdateDeploysTheWorkspaceServiceWithARunInFlight` |

## 3. Contracts

Each contract depends on no task. It holds types, constants, a short document and stubs, and
nothing that computes. Each is launched at the strictest rigor with an independent review before a
side is written against it: all three are over credentials or a wire format.

### C1: the scope contract
- Acceptance: `credsvc` declares the per-call scope a route needs (the verified caller, and the
  policy, kill, state, outcome, table, offering and meter readers for that caller) and the resolver
  that returns one for a request; a stub resolver returns a scope that refuses every call.
- Files: `internal/credsvc/scope.go` (new), `internal/credsvc/scope_stub.go` (new, stub),
  `docs/workspace-service/contract-scope.md` (new).
- Interfaces: produces `Scope`, `ScopeResolver`; consumed by WS1, WS2, WS5, WS8, WS16, WS17.
- Tests: `TestStubScopeRefusesEveryCall`.
- Command: `go test ./internal/credsvc/ -run StubScope -count=1`.
- Red step: the test fails to compile until the types exist.
- Depends on: none.
- Commit: `credsvc: the per-call scope contract (C1, R1)`.
- Reviewer tier: opus, mandatory independent review.

### C2: the wire contract
- Acceptance: one place names the service's workspace-mode environment name, the job's
  workspace-address environment name, the pin's address field, the published route record
  (`project` or `workspace` per credential name) and the beat's setting key; the route record has a
  JSON Schema; a stub reader returns "every name is the project's".
- Files: `internal/creds/route.go` (new), `internal/creds/route_stub.go` (new, stub),
  `schemas/credential-route.schema.json` (new), `docs/workspace-service/contract-wire.md` (new).
- Interfaces: produces `creds.Route`, `creds.RouteReader` and the names; consumed by WS3, WS5, WS6,
  WS9, WS10, WS11, WS13, WS14, WS20.
- Tests: `TestRouteRecordDecodesStrictly`, `TestStubRouteIsAlwaysTheProject`.
- Command: `go test ./internal/creds/ -run Route -count=1`.
- Red step: an unknown key in the record is accepted until the strict decode lands.
- Depends on: none.
- Commit: `creds: the workspace wire contract (C2, R3, R4)`.
- Reviewer tier: opus, mandatory independent review.

### C3: the node contract, version 2
- Acceptance: version 2 of the `workspace_credentials` node is declared: a member row's job
  accounts each carry their repository (R6), a shared entry carries its kind (model, Bitbucket,
  GitHub) and, for a forge entry, what it covers for a member's repository (U2), and `all_members`
  is a key exclusive with `projects` (R7). Version 1 still decodes. The schema is published.
- Files: `internal/projectpolicy/workspace_credentials_v2.go` (new),
  `schemas/workspace-credentials.schema.json` (new),
  `docs/workspace-service/contract-node.md` (new).
- Interfaces: produces the version 2 types and `Validate`; consumed by WS2, WS4, WS14, WS15, WS16,
  WS17, WS19. No stub: the types are the whole contract.
- Tests: `TestNodeVersionTwoValidates`, `TestAllMembersExcludesProjects`,
  `TestVersionOneStillDecodes`.
- Command: `go test ./internal/projectpolicy/ -run 'NodeVersionTwo|AllMembers|VersionOneStill' -count=1`.
- Red step: `all_members` beside `projects` is accepted until the rule lands.
- Depends on: none.
- Commit: `projectpolicy: the workspace credentials node, version 2 (C3, R6, R7)`.
- Reviewer tier: opus, mandatory independent review.

## 4. Slices and tasks

**Slice A, the service (inert until a member pins an address):** WS1, WS2, WS5, WS6, WS7, WS8.
**Slice B, the workspace owner's side:** WS3, WS4, WS14, WS19.
**Slice C, the member's side:** WS9 to WS13.
**Slice D, forge credentials:** WS15 to WS18.
**Slice E, checks and documents:** WS20, WS21. **Integration:** I1.

### WS1: the server on the scope, and the installation's scope
- Acceptance: every route of `credsvc.Server` reads its caller and its readers from the scope the
  resolver returns; the installation's resolver is built from today's `Config`; no existing test in
  `internal/credsvc` changes.
- Files: `internal/credsvc/server.go`, `internal/credsvc/cascade.go`,
  `internal/credsvc/scope_installation.go` (new), `internal/credsvc/scope_installation_test.go` (new).
- Interfaces: consumes C1; produces the installation's `ScopeResolver`.
- Tests: `TestInstallationScopeIsTodaysBehaviour`, plus the existing suites unchanged.
- Command: `go test ./internal/credsvc/ -run 'InstallationScope|Server|Cascade' -count=1 && go test ./internal/cli/ -run DocsWorkspaces -count=1`.
- Red step: `TestInstallationScopeIsTodaysBehaviour` fails while a route still reads `Config` directly.
- Depends on: C1 (the seam's shape).
- Commit: `credsvc: one server over a per-call scope (WS1, R1)`.
- Reviewer tier: opus, mandatory independent review.

### WS2: the workspace's scope
- Acceptance: a resolver built from `credsvc.Workspace` returns, for a member's job, a scope whose
  readers are that member's own database and whose offering is `Workspace.Shared` over
  `WorkspaceShared` of the bundle and the published node; state is read and written in the
  workspace's database only (R8).
- Files: `internal/credsvc/scope_workspace.go` (new), `internal/credsvc/scope_workspace_test.go`
  (new), `internal/credsvc/workspace.go`.
- Interfaces: consumes C1, C3; produces the workspace's `ScopeResolver`.
- Tests: `TestWorkspaceScopeIgnoresADeclaredMember`, `TestWorkspaceScopeOffersOnlyTheIntersection`,
  `TestWorkspaceScopeWritesNothingToAMember`.
- Command: `go test ./internal/credsvc/ -run WorkspaceScope -count=1 && go test ./internal/cli/ -run DocsWorkspaces -count=1`.
- Red step: the intersection test fails while the offering is the whole bundle.
- Depends on: C1 (the seam), C3 (the node it reads).
- Commit: `credsvc: the workspace's scope (WS2, D2980, R5, R8)`.
- Reviewer tier: opus, mandatory independent review.

### WS3: the local config and the `fugaro workspace` writes
- Acceptance: `workspace add` takes the service's address into the pin, and a pin without one is
  valid (R3); `workspace member add` takes `database_region` and a repository per job account (R6);
  `workspace share <credential> --projects a,b` and `workspace unshare` edit a local shared list;
  every write is a local edit that ends by naming `fugaro update`, and none asks a question.
- Files: `internal/localcfg/localcfg.go`, `internal/cli/workspace.go`,
  `internal/cli/workspace_share.go` (new), `internal/cli/workspace_share_test.go` (new),
  `schemas/workspace.schema.json`.
- Interfaces: consumes C2 (the pin's field name), C3 (the columns); produces the local fields WS4
  and WS10 read.
- Tests: `TestAPinWithoutAnAddressIsValid`, `TestMemberAddTakesRegionAndRepositories`,
  `TestShareRefusesAProjectThatIsNotAMember`, `TestShareAsksNothing`.
- Command: `go test ./internal/cli/ -run 'APinWithout|MemberAddTakes|Share' -count=1 && go test ./internal/cli/ -run DocsWorkspaces -count=1 && (cd plugin && go test ./... -count=1)`.
- Red step: the share test fails with an unknown command.
- Depends on: C2, C3.
- Commit: `cli: the workspace's address, table columns and shared list (WS3, R3, R6)`.
- Reviewer tier: opus, mandatory independent review.

### WS4: the node's writer
- Acceptance: the workspace owner's `fugaro update` publishes the node from the local member table
  and shared list, conditionally and migrated in place; it refuses a shared entry naming a project
  that is not a member (F13); it refuses to publish version 2 while the deployed service is older
  than the release that reads it, printing the fix (R7).
- Files: `internal/cli/update_workspace.go`, `internal/cli/update_workspace_node_test.go` (new),
  `internal/projectpolicy/workspace_credentials_migrate.go` (new).
- Interfaces: consumes C3 and WS3's local fields; produces the published node.
- Tests: `TestUpdatePublishesTheNode`, `TestWriterRefusesVersionTwoForAnOlderService`,
  `TestNodeMigratesInPlace`.
- Command: `go test ./internal/cli/ -run 'UpdatePublishesTheNode|WriterRefuses' -count=1 && go test ./internal/projectpolicy/ -run NodeMigrates -count=1`.
- Red step: no node is written by an update that publishes the record.
- Depends on: C3; WS3 (kept: it reads the local fields WS3 adds to one struct).
- Commit: `update: the workspace credentials node gets its writer (WS4, F11, F13, R7)`.
- Reviewer tier: opus, mandatory independent review.

### WS5: workspace mode in `fugaro credentials serve`
- Acceptance: with the workspace-mode name set the process builds the workspace's resolver, the
  bundle loader and the meter and serves the same routes; without it the process is today's; a
  half-set environment is refused at start with the missing name.
- Files: `internal/cli/credentials_serve.go`, `internal/cli/credentials_serve_workspace_test.go` (new).
- Interfaces: consumes C1 (the resolver), C2 (the names).
- Tests: `TestServeWorkspaceModeBuildsTheWorkspaceResolver`, `TestServeWithoutTheModeIsUnchanged`,
  `TestServeRefusesAHalfSetWorkspaceEnvironment`.
- Command: `go test ./internal/cli/ -run 'ServeWorkspaceMode|ServeWithoutTheMode|ServeRefusesAHalf' -count=1`.
- Red step: the mode test fails while the process ignores the name.
- Depends on: C1, C2.
- Commit: `cli: credentials serve learns workspace mode (WS5, R2)`.
- Reviewer tier: opus, mandatory independent review.

### WS6: the workspace root deploys the service
- Acceptance: the root adds the Cloud Run service running as `google_service_account.workspace`,
  the one bundle accessor, and an invoke grant per member job account from the member table; it
  states a maximum instance count and a concurrency (R9) and `min_instance_count = 0`; the cover
  admits exactly these and nothing else; the bundle comment of F5 is true.
- Files: `deploy/terraform/gcp/roots/workspace/main.tf`,
  `deploy/terraform/gcp/roots/workspace/tests/workspace.tftest.hcl`,
  `internal/infra/tf/workspace.go`, `internal/infra/tf/workspace_test.go`,
  `deploy/terraform/terraform_test.go`.
- Interfaces: consumes C2 (the environment names).
- Tests: `TestWorkspacePlanAdmitsTheService`, `TestWorkspacePlanRefusesAnythingElse`,
  `TestWorkspaceRootDeclaresTheTypesTheCoverHolds`, `TestEmbeddedTree`, the tftest.
- Command: `go test ./internal/infra/tf/ -run Workspace -count=1 && go test ./deploy/terraform/ -count=1 && terraform fmt -check -recursive deploy/terraform`.
- Red step: the cover refuses a plan that holds the service.
- Depends on: C2.
- Commit: `terraform: the workspace root deploys its credentials service (WS6, D2980, F4, R9)`.
- Reviewer tier: opus, mandatory independent review.

### WS7: `fugaro update` deploys it
- Acceptance: the workspace owner's `fugaro update` applies the root with the image and the member
  job accounts, with no other step; an installation at the previous release converges; with runs in
  flight it prints the one line and changes nothing a run holds (R11); a split-Firebase workspace
  gets no service and a line saying why (R12).
- Files: `internal/cli/workspace_create.go`, `internal/cli/workspace_create_test.go`,
  `internal/cli/update_converge_test.go`.
- Interfaces: consumes WS6's root.
- Tests: `TestUpdateConvergesAWorkspaceFromThePreviousRelease`,
  `TestUpdateDeploysTheWorkspaceServiceWithARunInFlight`, `TestSplitFirebaseGetsNoService`.
- Command: `go test ./internal/cli/ -run 'UpdateConvergesAWorkspace|UpdateDeploysTheWorkspaceService|SplitFirebaseGetsNoService' -count=1`.
- Red step: the converge test fails because the root's new variables are not passed.
- Depends on: WS6 (kept: it plans against the root's variables and cover, which are behaviour).
- Commit: `update: the workspace's service is deployed by update alone (WS7, R11, R12)`.
- Reviewer tier: opus, mandatory independent review.

### WS8: the meter on the paid-call path
- Acceptance: the server reserves through the scope's meter before every upstream call, probes
  included, settles on the answer's usage and refuses the call when the settle fails; a call at the
  ceiling is refused with the workspace's refusal header and every answer carries the headroom
  header; an installation's scope has no meter and is unchanged. `SECURITY.md`,
  `website/content/security.md` and the test that pins them say the cap now binds.
- Files: `internal/credsvc/cascade.go`, `internal/credsvc/forward.go`, `internal/credsvc/probe.go`,
  `SECURITY.md` with `website/content/security.md`, `internal/cli/docs_workspaces_security_test.go`.
- Interfaces: consumes C1 (the meter in the scope).
- Tests: `TestEveryPaidCallIsReserved`, `TestAFailedSettleRefuses`, `TestACallAtTheCeilingIsRefused`,
  `TestSecurityMdStatesTheWorkspaceThreatModel`.
- Command: `go test ./internal/credsvc/ -run 'EveryPaidCall|AFailedSettle|ACallAtTheCeiling' -count=1 && go test ./internal/cli/ -run 'SecurityMd|DocsWorkspaces' -count=1 && (cd website && npm run check)`.
- Red step: `TestEveryPaidCallIsReserved` fails on the probe path.
- Depends on: C1; WS1 (kept: both rewrite `cascade.go`).
- Commit: `credsvc: the workspace cap binds, metered at the proxy (WS8, D1301, F10, F18)`.
- Reviewer tier: opus, mandatory independent review.

### WS9: the member publishes the route
- Acceptance: a member's `fugaro update` publishes, per credential name its policy admits, whether
  the project's own bundle holds it; the project's copy wins when both exist (D2973); a member in no
  workspace publishes nothing new. The first step of the task settles U1 and records it in
  `contract-wire.md` through a contract change if the record must move.
- Files: `internal/cli/update_route.go` (new), `internal/cli/update_route_test.go` (new),
  `internal/cli/update.go`.
- Interfaces: consumes C2 (`creds.Route`).
- Tests: `TestUpdatePublishesTheRoute`, `TestTheProjectsCopyWins`, `TestNoWorkspaceNoRoute`.
- Command: `go test ./internal/cli/ -run 'UpdatePublishesTheRoute|TheProjectsCopyWins|NoWorkspaceNoRoute' -count=1`.
- Red step: no route is published for a member whose bundle is empty.
- Depends on: C2.
- Commit: `update: a member publishes where each credential is served (WS9, D2973, D2982, R4)`.
- Reviewer tier: opus, mandatory independent review.

### WS10: the address reaches the member's jobs
- Acceptance: a member's `fugaro update` sets the workspace-address environment name on its jobs
  from the pin; a pin without an address sets nothing; a run in flight is untouched.
- Files: `internal/infra/projectjobs.go`, `internal/infra/tfvars.go`,
  `internal/infra/projectjobs_workspace_test.go` (new).
- Interfaces: consumes C2 (the name) and WS3's pin field.
- Tests: `TestJobsCarryTheWorkspaceAddress`, `TestNoAddressNoVariable`.
- Command: `go test ./internal/infra/ -run 'JobsCarryTheWorkspaceAddress|NoAddressNoVariable' -count=1`.
- Red step: the job's environment lacks the name.
- Depends on: C2; WS3 (kept: it reads the pin field WS3 adds).
- Commit: `infra: a member's jobs learn the workspace's address (WS10, R3)`.
- Reviewer tier: opus, mandatory independent review.

### WS11: the run routes each credential
- Acceptance: at bootstrap the run reads the route; the gateway holds a second upstream for the
  workspace's service, with its own ID token audience; a credential routed to the workspace is
  never asked of the project's service; the workspace's stop and headroom headers are acted on
  (`WorkspaceStop`, `DeliveredHeadroom` get their callers); a run with no address behaves as today.
- Files: `internal/gateway/gateway.go`, `internal/gateway/service.go`, `internal/runner/gateway.go`,
  `internal/gateway/workspace_route_test.go` (new).
- Interfaces: consumes C2.
- Tests: `TestARunRoutedToTheWorkspaceNeverCallsTheProject`, `TestTheWorkspaceUpstreamHasItsOwnAudience`,
  `TestNoAddressIsTodaysRun`, `TestTheWorkspaceStopHaltsTheRun`.
- Command: `go test ./internal/gateway/ -run 'ARunRouted|TheWorkspaceUpstream|NoAddressIsTodays|TheWorkspaceStop' -count=1 && go test ./internal/runner/ -run Gateway -count=1`.
- Red step: the routed call reaches the project's fake.
- Depends on: C2.
- Commit: `gateway: a run routes each credential to its holder (WS11, D2981, D2982, F7, F9)`.
- Reviewer tier: opus, mandatory independent review.

### WS12: an unreachable service is waited for
- Acceptance: when either service cannot be reached the run waits and retries for a bounded time,
  then halts with a reason that names which service; the bound is one constant; the wait is the
  mechanism `noteExhausted` already uses.
- Files: `internal/gateway/service.go`, `internal/runner/creds_wait.go`,
  `internal/gateway/service_unreachable_test.go` (new).
- Interfaces: none new.
- Tests: `TestAnUnreachableServiceIsWaitedFor`, `TestTheWaitIsBounded`, `TestTheHaltNamesTheService`.
- Command: `go test ./internal/gateway/ -run 'AnUnreachableService|TheWaitIsBounded|TheHaltNames' -count=1`.
- Red step: the run halts on the first failed call.
- Depends on: WS11 (kept: both rewrite `service.go`, and the reason names the upstream WS11 adds).
- Commit: `gateway: a bounded wait for an unreachable service (WS12, D2983)`.
- Reviewer tier: opus, mandatory independent review.

### WS13: the beat, and its switch
- Acceptance: a run that routed a credential to the workspace beats the workspace's service at the
  slip beat's interval; a setting turns that beat off and on is the default; a run that routed
  nothing there sends none.
- Files: `internal/runner/budget_session.go`, `internal/settings/registry.go`,
  `internal/config/defaults.go`, `internal/runner/workspace_beat_test.go` (new).
- Interfaces: consumes C2 (the setting's key).
- Tests: `TestARoutedRunBeatsTheWorkspace`, `TestTheBeatCanBeTurnedOff`, `TestNoRouteNoBeat`.
- Command: `go test ./internal/runner/ -run 'ARoutedRunBeats|TheBeatCanBeTurnedOff|NoRouteNoBeat' -count=1`.
- Red step: no beat reaches the workspace's fake.
- Depends on: C2; WS11 (kept: the beat exists only for a run WS11 routed).
- Commit: `runner: the workspace beat and its switch (WS13, D2985, R10)`.
- Reviewer tier: standard.

### WS14: doctor, and the alarm on demand
- Acceptance: `fugaro doctor` on the workspace owner's machine reports the service deployed, the
  node published and readable, each shared entry present in the bundle, and the reconciliation
  alarm's verdict; on a member's it reports the pin's address and the route; `fugaro workspace show`
  carries the alarm's verdict, as a new field in `--json`. All of it reads and writes nothing.
- Files: `internal/cli/doctor_workspace.go`, `internal/cli/workspace_alarm.go` (new),
  `internal/cli/workspace_json.go`, `internal/cli/doctor_workspace_service_test.go` (new),
  `schemas/workspace.schema.json`.
- Interfaces: consumes C2, C3; calls `budget.ReconcileWorkspace`.
- Tests: `TestDoctorReportsTheWorkspaceService`, `TestDoctorReportsAnAlarm`,
  `TestWorkspaceShowCarriesTheAlarm`, `TestDoctorWritesNothing`.
- Command: `go test ./internal/cli/ -run 'DoctorReportsTheWorkspace|DoctorReportsAnAlarm|WorkspaceShowCarries|DoctorWritesNothing' -count=1`.
- Red step: doctor passes a workspace with no service.
- Depends on: C2, C3; WS3 (kept: both edit `schemas/workspace.schema.json`).
- Commit: `doctor: the workspace's service, node and alarm (WS14, D2987, F20)`.
- Reviewer tier: opus, mandatory independent review.

### WS15: forge credentials in the pool
- Acceptance: `creds.Shared` and `WorkspaceShared` carry Bitbucket and GitHub entries with their
  kind and coverage as C3 declares them; a model entry is unchanged; a forge entry the bundle does
  not hold is skipped as a model one is.
- Files: `internal/creds/workspace.go`, `internal/credsvc/bundle_workspace.go`,
  `internal/credsvc/bundle_workspace_test.go`.
- Interfaces: consumes C3; produces the forge offering WS16 and WS17 read.
- Tests: `TestThePoolCarriesForgeEntries`, `TestAModelEntryIsUnchanged`.
- Command: `go test ./internal/credsvc/ ./internal/creds/ -run 'ThePoolCarriesForge|AModelEntryIsUnchanged|Workspace' -count=1`.
- Red step: a Bitbucket entry is dropped from the offering.
- Depends on: C3.
- Commit: `creds: the shared pool carries forge credentials (WS15, D2978, D2984, F15)`.
- Reviewer tier: opus, mandatory independent review.

### WS16: the Bitbucket routes on the scope
- Acceptance: the git proxy and the read mint take their token, coverage and repository table from
  the scope; under a workspace's scope a request is served only for a repository the entry covers,
  the member's policy admits and the allow-list names; an installation's behaviour is unchanged.
- Files: `internal/credsvc/bitbucketproxy.go`, `internal/credsvc/bitbucket.go`,
  `internal/credsvc/bitbucket_workspace_test.go` (new).
- Interfaces: consumes C1, C3.
- Tests: `TestWorkspaceProxyServesOnlyACoveredRepository`, `TestWorkspaceProxyStreamsAndHoldsNothing`,
  the existing Bitbucket suites unchanged.
- Command: `go test ./internal/credsvc/ -run 'WorkspaceProxy|Bitbucket' -count=1`.
- Red step: a repository outside the coverage is served.
- Depends on: C1, C3; WS1 (kept: it calls the scope plumbing WS1 puts in `Server`).
- Commit: `credsvc: the Bitbucket routes read the scope (WS16, D2978, D2979)`.
- Reviewer tier: opus, mandatory independent review.

### WS17: the GitHub routes on the scope
- Acceptance: the mint, the read mint and the merge mint take the App key and the repository from
  the scope, with the same three fences as WS16; an installation's behaviour is unchanged.
- Files: `internal/credsvc/github.go`, `internal/credsvc/githubread.go`,
  `internal/credsvc/mergemint.go`, `internal/credsvc/github_workspace_test.go` (new).
- Interfaces: consumes C1, C3.
- Tests: `TestWorkspaceMintServesOnlyACoveredRepository`, the existing GitHub suites unchanged.
- Command: `go test ./internal/credsvc/ -run 'WorkspaceMint|GitHub|Mint' -count=1`.
- Red step: a repository outside the coverage is minted for.
- Depends on: C1, C3; WS1 (kept: as WS16).
- Commit: `credsvc: the GitHub routes read the scope (WS17, D2978, D2979)`.
- Reviewer tier: opus, mandatory independent review.

### WS18: the run's forge calls follow the route
- Acceptance: the run's git traffic and forge token requests go to the service the route names for
  the forge credential; with no route they go to the project's, as today.
- Files: `internal/gateway/gitforward.go`, `internal/runner/gateway.go`,
  `internal/gateway/gitforward_route_test.go` (new).
- Interfaces: consumes C2 and WS11's second upstream.
- Tests: `TestGitTrafficFollowsTheRoute`, `TestNoRouteGitGoesToTheProject`.
- Command: `go test ./internal/gateway/ -run 'GitTrafficFollows|NoRouteGitGoes' -count=1`.
- Red step: a push routed to the workspace reaches the project's fake.
- Depends on: WS11 (kept: it uses the upstream WS11 builds and both edit `runner/gateway.go`).
- Commit: `gateway: git traffic follows the credential's route (WS18, D2979, D2984)`.
- Reviewer tier: opus, mandatory independent review.

### WS19: `all_members`
- Acceptance: `fugaro workspace share <credential> --all-members` writes the key; the writer
  publishes it as version 2 under WS4's guard; the service offers the entry to every member of the
  node, still intersected with the member's policy; a workspace with no cap set is refused the key
  with the fix printed.
- Files: `internal/cli/workspace_share.go`, `internal/cli/update_workspace.go`,
  `internal/credsvc/bundle_workspace.go`, `internal/cli/workspace_all_members_test.go` (new).
- Interfaces: consumes C3.
- Tests: `TestAllMembersOffersEveryMember`, `TestAllMembersNeedsACap`, `TestAllMembersStillAsksTheMembersPolicy`.
- Command: `go test ./internal/cli/ -run AllMembers -count=1 && go test ./internal/credsvc/ -run AllMembers -count=1`.
- Red step: the key is refused as unknown.
- Depends on: WS8 (kept: D2977 allows the key only once the meter binds); WS4 and WS15 (kept: one
  writer at a time of `update_workspace.go` and `bundle_workspace.go`).
- Commit: `workspace: a shared entry for every member (WS19, D2977, R7)`.
- Reviewer tier: opus, mandatory independent review.

### WS20: the live checks, as scripts
- Acceptance: Checks 770 to 773 exist as one command each under `scripts/live-checks/`, each with
  its hermetic self-test and its row in `docs/gcp-live-checklist.md`; none is run by an agent.
- Files: `scripts/live-checks/check770.sh` to `check773.sh` (new) with their `_test.sh`,
  `docs/gcp-live-checklist.md`.
- Interfaces: consumes C2 (the names).
- Tests: the four `_test.sh`.
- Command: `for n in 770 771 772 773; do bash scripts/live-checks/check${n}_test.sh; done && go test ./internal/cli/ -run Docs -count=1`.
- Red step: each self-test fails on a missing script.
- Depends on: C2.
- Commit: `checks: 770 to 773 for the workspace's service (WS20, D2986)`.
- Reviewer tier: standard.
- Owner-run: `bash scripts/live-checks/check770.sh` and the three like it, after the release that
  carries the service. No agent performs any step of them.

### WS21: the documents and the operator note
- Acceptance: `docs/workspaces/*.md` no longer say the node has no writer, the meter is unwired or
  the service has no caller; the workspace operator guide shows the three commands; the release
  note's `### For operators` is "run `fugaro update`" for the workspace owner and for each member,
  with the one sentence of F17 (a member's new repository reaches the workspace's service after the
  workspace owner's next update); the plugin's skills name the new commands where they name the old.
- Files: `docs/workspaces/README.md`, `docs/workspaces/implementation-plan.md`,
  `docs/workspaces/spec.md`, the release notes file of the release that carries it,
  `plugin/skills/working/SKILL.md`.
- Interfaces: none.
- Tests: `go test ./internal/cli/ -run Docs -count=1`, the plugin's command tests.
- Command: `go test ./internal/cli/ -run Docs -count=1 && (cd plugin && go test ./... -count=1)`.
- Red step: `TestDocsWorkspacesIsWritten` fails on the changed status line until its pin moves.
- Depends on: WS3 and WS19 (kept: the plugin's tests check each named command and flag against the
  built CLI); WS8 (kept: the statements become true only then).
- Commit: `docs: the workspace's service is deployed (WS21)`.
- Reviewer tier: opus, mandatory independent review (a plugin skill).

### I1: integration
- Acceptance: the stubs `scope_stub.go` and `route_stub.go` are deleted; one hermetic end-to-end
  test drives a member's run through its gateway to a workspace-mode server over fakes: a model
  call and a Bitbucket push on a workspace credential, metered, with the project's service never
  called; and the same run with the project holding its own copy never calls the workspace.
- Files: `internal/credsvc/scope_stub.go` (deleted), `internal/creds/route_stub.go` (deleted),
  `internal/cli/workspace_end_to_end_test.go` (new).
- Interfaces: consumes every side of C1, C2 and C3.
- Tests: `TestWorkspaceServesAMemberRunEndToEnd`, `TestTheProjectsOwnCopyKeepsTheWorkspaceUntouched`.
- Command: `go test ./internal/cli/ -run 'WorkspaceServesAMemberRun|TheProjectsOwnCopyKeeps' -count=1 && go build ./... && go vet ./...`.
- Red step: the test fails to build while a stub is still referenced.
- Depends on: C1, C2, C3 and every side of an edge they broke: WS1, WS2, WS4, WS5, WS6, WS8, WS9,
  WS10, WS11, WS14, WS15, WS16, WS17, WS18, WS20.
- Commit: `workspace: the service, wired end to end (I1)`.
- Reviewer tier: opus, mandatory independent review.

## 5. Parallel execution

The decoupling pass is on (`planning.decoupling` is `true` for this repository), and the
repository's mode is `startup`, so the pass is applied aggressively.

**Levels.** Tasks in one level run at once.

| Level | Tasks |
| --- | --- |
| 0 | C1, C2, C3 |
| 1 | WS1, WS2, WS3, WS5, WS6, WS9, WS11, WS15, WS20 |
| 2 | WS4, WS7, WS8, WS10, WS12, WS13, WS14, WS16, WS17, WS18 |
| 3 | WS19, I1 |
| 4 | WS21 |

**Critical path.** By this plan's levels the longest chain is C1, WS1, WS8, WS19, WS21: five steps.
The graph read reports `critical path (4 tasks): C2 -> WS11 -> WS18 -> I1`, with C2 the bottleneck,
and `gain: 13 broken, 16 kept, 7 added; before 5 tasks, weight 9; after 4 tasks, weight 8;
improves`.

**Why the two differ.** The graph format records a contract's consumers only: a task may wait on a
contract in the graph only where a broken edge names it as the dependent. The tasks that build a
contract's other side (WS1 for C1; WS3, WS6, WS9 and WS10 for C2; WS3 and WS4 for C3) therefore
carry no edge to their contract in the graph and read there as ready at once. **They are not:**
each starts after its contract has merged, as its own Depends-on line and the levels table say. A
launcher follows the levels table.

**Edges broken, each by a contract and rejoined in I1.**
- WS2 after WS1, WS5 after WS1, WS5 after WS2: each needs the scope's shape, not the other's code (C1).
- WS11 after WS9, WS11 after WS10, WS14 after WS9, WS5 after WS6, WS20 after WS6: each needs a name
  or the route record, not who writes it (C2).
- WS2 after WS4, WS14 after WS4, WS15 after WS4, WS16 after WS15, WS17 after WS15: each needs the
  node's shape, not its writer or the pool's code (C3).

**Edges kept.** Each is stated in its task: a shared file (WS8 and WS16 and WS17 after WS1; WS12 and
WS18 after WS11; WS14 after WS3; WS19 after WS4 and WS15), a field one task adds to a struct another
reads in the same change set (WS4 and WS10 after WS3), behaviour (WS7 after WS6, WS13 after WS11),
the owner's ordering (WS19 after WS8) and the built CLI (WS21).

**Shared files.** No level holds two writers of one file.

| File | Writers, by level |
| --- | --- |
| `internal/credsvc/cascade.go` | WS1 (1), WS8 (2) |
| `internal/credsvc/server.go` | WS1 (1) |
| `internal/credsvc/workspace.go` | WS2 (1) |
| `internal/credsvc/bundle_workspace.go` | WS15 (1), WS19 (3) |
| `internal/gateway/service.go` | WS11 (1), WS12 (2) |
| `internal/runner/gateway.go` | WS11 (1), WS18 (2) |
| `internal/gateway/gateway.go` | WS11 (1) |
| `internal/localcfg/localcfg.go` | WS3 (1) |
| `internal/cli/workspace.go` | WS3 (1) |
| `internal/cli/workspace_share.go` | WS3 (1), WS19 (3) |
| `internal/cli/update_workspace.go` | WS4 (2), WS19 (3) |
| `internal/cli/update.go` | WS9 (1) |
| `internal/cli/credentials_serve.go` | WS5 (1) |
| `schemas/workspace.schema.json` | WS3 (1), WS14 (2) |
| `SECURITY.md`, `website/content/security.md` | WS8 (2) |
| `docs/gcp-live-checklist.md` | WS20 (1) |
| `docs/workspaces/*.md` | line citations by the task that moves them (levels 1 and 2 touch different files' citations; a conflict is resolved locally), statements by WS21 (4) |

`docs/workspaces/*.md` is the one soft spot: two tasks of one level may each move a citation in the
same document. They are one-line edits; the coordinator merges them back to back and resolves a
conflict locally.

## 6. The estimate

As `fugaro plan graph --plan docs/workspace-service/implementation-plan.md` printed it:

`ready in 285–1140 min, no history yet for 28 tasks (28 tasks; seeds for 28; from the seeds: no history is cached on this machine yet)`

It is informational and reserves no budget: it is what the tasks' tiers and this machine's history
predict, and it moves as runs finish.

## 7. Contract changes anticipated

- **R-route-home** (C2, raised by WS9): if U1 shows the route must live in the member's policy and
  not beside its credential state, the record's path moves. Reaches WS11, WS14.
- **R-forge-coverage** (C3, raised by WS16 or WS17): if a Bitbucket token's coverage or a GitHub
  App's installation cannot be expressed as C3 wrote it. Reaches WS4, WS15 and the other forge task.
- **R-scope-forge** (C1, raised by WS16 or WS17): if the forge routes need a reader the scope does
  not carry. Reaches WS1, WS2.

A launched dependent is reached by a follow-up run on its pull request that names the contract file
by path.

## 8. Checks the plan takes

From the reserved block 770–779. All four are owner-run, as scripts (WS20), after the release.

| Check | What it measures |
| --- | --- |
| **770** | Load: 100 concurrent runs across at least two members, model calls and git pushes and clones on workspace credentials. Records instance count, latency added per call, refusals, and whether R9's limits hold (D2986, O1) |
| **771** | A member's job account is admitted by a real deployment and reads the member's database with `fugaroWorkspaceRead` alone; a call at the ceiling is refused; `doctor` reports the alarm on a forced disagreement (U3; the workspaces design's U1 and U5) |
| **772** | Cold start of the workspace's service with the beat on and off, and a run's behaviour across a forced outage shorter and longer than the bound (D2983, D2985, U4) |
| **773** | `fugaro update` on a workspace and on a member at the previous release, with a run in flight on each: both converge with no other step and the run finishes (R11) |

## 9. After-deploy steps

None beyond `fugaro update`. WS21's release note says: the workspace owner runs `fugaro update`;
each member runs `fugaro workspace add` with the address once (a pin without one keeps working and
takes no workspace credential) and then `fugaro update`; a member that later adds a repository
needs the workspace owner's next `fugaro update` before that repository's runs reach the
workspace's service (F17).

## 10. Considered and declined

- **One service in both roles** (D2980's other option): every member would re-pin, and one account
  would hold a write on its own database beside reads on every member.
- **The project's service forwarding to the workspace's** (D2981's other option): two hops per
  call and a woken project service on every call.
- **Model credentials first** (the recommendation of D2984): the owner chose every kind at once.
- **A scheduled alarm job** (D2987's other option): more Terraform for a figure someone has to
  read anyway.
- **A minimum of one instance** (D2985's other option): it bills all month and the managed cost
  gate blocks it.
