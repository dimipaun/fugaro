# Credentials belong to models, not repositories: implementation plan

**Status: plan only; nothing is implemented; no task is approved.** Each task needs the owner's
explicit go, by name. This plan builds [spec.md](spec.md) as amended by
[reconciliation.md](reconciliation.md); where they disagree the reconciliation wins. Decisions are
cited as `D#`, findings as `F#`, resolutions as `R#`. Checked against `origin/main` at `d57d71ddb`
on 2026-11-20.

**The answers that shaped it** (all the owner's, 2026-11-20, all as recommended): a craft's
access names the credential (D3040); an unset access is the first declared credential that reaches the model, and never Vertex
unless named (D3041, amended by the owner the same day); access is the project's alone (D3042); the old key is read, ignored
and named for one release, then rejected with no version bump (D3043, D3052); the cascade becomes
a pool (D3044) and its consent switch is no longer required (D3045); Vertex stays on the job's
identity through the gateway (D3046); the subscription token leaves the agent's environment in
two releases (D3047); stage accounts become craft access (D3048); `--token` stays (D3049); the
views print `-` and gain a member (D3050); it ships after the two plans before it have started
(D3051); a local invocation uses the developer's environment (D3053).

**What it builds, in one paragraph.** One question replaces the auth mode: for this stage of this
repository, which credential or pool, of which type (R1). Contract C1 writes the question and
every name two sides share. Release 1 moves every reader to it, migrates every config and record
inside `fugaro update`, serves a subscription through the gateway wherever the bundle is ready,
and names the retired keys. Release 2, cut with the keys plan's release B, removes what release 1
kept readable.

## 1. Conventions every task follows

- `CLAUDE.md`, and `.claude/rules/credentials-service.md` for anything under `internal/creds`,
  `internal/credsvc`, the gateway, `SECURITY.md` or its website page: security-sensitive, an
  independent review before merging, exactly one audit line per refusal, no credential value in a
  log, an error or a fixture.
- **`fugaro update` is the whole upgrade** and is safe with runs in flight. No task adds an
  operator step, a second command or a confirmation. A task that changes what an installed
  version relies on ships its convergence in `update`, with a test that an installation at the
  previous release converges.
- **Fugaro asks for nothing.** A refusal is printed with its fix; nothing waits for input.
- **A field never changes meaning.** The new record and the new header have new names (R4, R5);
  the registry's `auth` is left out, never reused (R6).
- **The brief's item 3 is a clause of every code task:** the task that moves a reader rewrites
  the refusals, doctor lines and help texts that reader prints, so none is phrased by a
  repository's auth. The words to use are "a run on a subscription", "the credential a craft
  resolves to", "the project's credentials".
- Hermetic tests (`internal/gcpfake`, `internal/testutil`, `t.TempDir()`); no secret-shaped
  literal; comments say why; `gofmt`, `go vet`; targeted tests, never a whole package unless the
  task says so.
- Documents are tested: a task that changes a pinned sentence changes its test, and runs
  `go test ./internal/cli/ -run Docs -count=1`.
- **Always:** the red step is run and reported before the change. **Ask first:** a new
  dependency (none is proposed), a schema change other than the ones CM20 and CM29 own, a
  `.github/workflows/` change (none is proposed). **Never:** a live cloud action by an agent;
  `fugaro creds set`; editing `spec.md`.
- **Other plans' files** (R9): a task is not launched while a task of the keys plan, always a
  budget, workspace service or secrets brokering that edits the same file is live. The named
  waits are in §5.

## 2. Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| No migrated configuration changes what serves it: before and after `update`, every craft of every repository goes out on the same credential | CM4 `TestUpdateKeepsWhatEachCraftGoesOutOn` |
| A repository is never offered a credential its record does not name, whatever its call declares, with the consent switch absent (D3045, O1) | CM6 `TestACallIsNeverServedBeyondItsRecord`, CM8 `TestNoSwitchNoWiderReach` |
| Exactly one audit line per refusal on the new narrowing | CM6 `TestTheAccessRefusalWritesOneAuditLine` |
| An older image's call (old header, old record) is served exactly as before through release 1 | CM6 `TestAnOlderImagesCallIsNarrowedByTheOldRecord` |
| No subscription token is in the agent's environment where the job carries the "credentials served" fact; it stays where the job does not (D3047) | CM9 `TestNoTokenInTheAgentsEnvironmentWhereServed`, `TestTheTokenStaysWhereNotServed` |
| A run in flight is disturbed in nothing it holds, and the update says the one line it owes | CM19 `TestUpdateLeavesARunInFlightRunning`, `TestTheInFlightLineForAccess` |
| Nothing reads `agent.auth` after release 1 but the migration and the retired-key notice | I1 `TestNoReaderOfTheRetiredAuth` |
| The run record's `model_basis`, `served_by` and `credential` are byte for byte what they were for a migrated configuration | CM10 `TestTheRecordIsUnchangedForAMigratedRun` |
| An installation at the previous release converges through `fugaro update` alone | I1 `TestAnInstallationAtThePreviousReleaseConverges` |
| Release 2 never leaves a repository with neither a token nor a service that serves it | CM28 `TestALaunchNotServedIsRefusedWithTheFix`, `TestAnOlderImageIsRefusedAtLaunch` |

## 3. The contract

### C1: the access contract
- Acceptance: one place declares what every side of this plan shares. (a) The question: for a
  stage, the access (a credential or a pool), each member's name, type and serving path, and an
  interface `Resolver` that answers it. (b) The reach rule's signature (R2). (c) The new record
  `creds/repoaccess/<slug>`: its members, its version and its path (R4). (d) The header a new
  runner declares its craft in, and its validation (R5). (e) The registry entry's new member
  (R6). (f) The per-repository craft block's key in the installation's config and in the
  per-repository node (F9). (g) One recorded fact per repository, "its credentials are served",
  and the job entry a run reads it from (F20). (h) The list of retired keys (R3). A stub resolver
  answers from the legacy auth mode, so every side compiles and behaves as today until its real
  side lands; a stub accessor gives the runner the same.
  The names live in the package that needs them, because `internal/gateway` cannot import
  `internal/credsvc` and `internal/config` imports neither; that is why it is past five files.
- Files: `internal/craft/access.go` (new), `internal/craft/access_stub.go` (new, stub),
  `internal/craft/access_test.go` (new), `internal/runner/access_stub.go` (new, stub),
  `internal/creds/repoaccess.go` (new), `internal/credsvc/access_wire.go` (new),
  `internal/credsvc/access_wire_test.go` (new), `internal/gateway/accesswire.go` (new),
  `internal/config/access_names.go` (new),
  `docs/credentials-belong-to-models/contract-access.md` (new).
- Interfaces: produces all of the above; consumed by every code task.
- Tests: `TestTheStubAnswersTheLegacyAuth`, `TestTheRecordNamesCredentialsAndNothingElse`,
  `TestTheAccessHeaderIsNotTheAuthHeader`, `TestTheGatewayAndTheServiceSpellTheHeaderAlike`.
- Command: `go test ./internal/craft/ -run 'TheStubAnswers' -count=1 && go test ./internal/creds/ -run TheRecordNames -count=1 && go test ./internal/credsvc/ -run 'TheAccessHeader|SpellTheHeaderAlike' -count=1`.
- Red step: the header's name equals `X-Fugaro-Auth-Mode`.
- Depends on: none.
- Commit: `craft: the access contract (C1, R1, R4, R5)`.
- Reviewer tier: opus, mandatory independent review (`fugaro review --pr N`) before any side is
  written against it: it is a contract over credentials and a wire.

## 4. Slices and tasks

**Release 1. Slice 1, the question and its answer:** CM1 to CM3. **Slice 2, the migration and the
record:** CM4, CM5. **Slice 3, the service and the run:** CM6 to CM12. **Slice 4, the launch and
the cloud side:** CM13 to CM15. **Slice 5, the readers that judge and report:** CM16 to CM20,
CM23. **Slice 6, the console and the checks:** CM21, CM22. **Integration:** I1. **Slice 7, the
documents:** CM24 to CM27.
**Release 2. Slice 8, what release 1 kept readable goes:** CM28 to CM31.

### CM1: the resolver, by reach
- Acceptance: a `Resolver` answers every craft: a written access as written; an unset one as the
  first declared credential, in the order the project lists them, that reaches the craft's model,
  and nothing after it; Vertex only where an access or a pool names it; an unset access nothing
  declared reaches is a problem that names the two fixes, declare a credential or write
  `access: vertex` (D3041 as amended, R2); an access whose credential cannot
  reach its craft's model is a problem at the key that wrote it. It adds a function and leaves
  `craft.Resolve` as it is; I1 removes the old default.
- Files: `internal/craft/reach.go` (new), `internal/craft/reach_test.go` (new).
- Interfaces: consumes C1 (the interface and the reach signature); produces the real resolver.
- Tests: `TestASubscriptionAndAKeyBothReachAClaudeModel`,
  `TestAProviderCredentialReachesOnlyItsProvidersModels`,
  `TestAnUnsetAccessIsTheFirstReachingCredentialOnly`, `TestVertexIsNeverImplied`,
  `TestAnUnsetAccessNothingReachesIsRefusedWithTheFix`, `TestAnAccessThatCannotReachItsModelIsAProblem`.
- Command: `go test ./internal/craft/ -run 'Reach|UnsetAccess|VertexIsNeverImplied|CannotReach' -count=1`.
- Red step: an unset access with a subscription declared answers `vertex`.
- Depends on: C1.
- Commit: `craft: an unset access is the first credential that reaches, never Vertex (CM1, D3041)`.
- Reviewer tier: opus, mandatory independent review.

### CM2: the project's per-repository crafts
- Acceptance: the installation's config accepts a crafts block on a repository's entry, and the
  per-repository node carries it; the resolver's layers read it between the repository's file
  and the project's own block; a repository's block may only select among credentials and pools
  the project holds; the node's block is published only where every job image of that repository
  is at or after the release that reads it, and is held back with one line elsewhere (F10, U2).
- Files: `internal/localcfg/localcfg.go`, `internal/projectpolicy/policy.go`,
  `internal/cli/project_cmd.go` (the per-repository node's writer),
  `internal/localcfg/repo_crafts_test.go` (new), `internal/projectpolicy/repo_crafts_test.go` (new).
- Interfaces: consumes C1 (the block's key).
- Tests: `TestARepositorysCraftsAreRead`, `TestARepositorysCraftNamesOnlyWhatTheProjectHolds`,
  `TestTheRepoBlockIsHeldBackForAnOlderImage`, `TestTheLastTagsDecoderMeetsTheBlock`.
- Command: `go test ./internal/localcfg/ -run ARepositorysCraft -count=1 && go test ./internal/projectpolicy/ -run 'TheRepoBlock|TheLastTagsDecoder' -count=1 && go test ./internal/cli/ -run 'RepoPolicy|ProjectPolicy' -count=1`.
- Red step: a crafts block on a repository's entry is refused as an unknown key.
- Depends on: C1.
- Commit: `config: the project names a repository's crafts (CM2, D3042, F9)`.
- Reviewer tier: opus, mandatory independent review.

### CM3: the retired keys are named
- Acceptance: one function lists, for a repository's file and for a project layer, each retired
  key it still writes (R3), with the path and the sentence a person reads; the keys still decode
  and still validate; `config.Scopes` marks them retired. It changes no reader.
- Files: `internal/config/retired.go` (new), `internal/config/retired_test.go` (new),
  `internal/config/scope.go`.
- Interfaces: consumes C1 (the list); produces `config.RetiredKeyNotes`, read by CM17, CM18,
  CM19 and CM20.
- Tests: `TestEveryRetiredKeyIsNamedWhereItIsWritten`, `TestAFileWithNoRetiredKeySaysNothing`,
  `TestARetiredKeyStillDecodes`.
- Command: `go test ./internal/config/ -run 'RetiredKey|NoRetiredKey|Scope' -count=1`.
- Red step: a file that writes `agent.auth` yields no note.
- Depends on: C1.
- Commit: `config: the retired keys, named where they are written (CM3, D3043, R3)`.
- Reviewer tier: standard.

### CM4: `fugaro update` migrates auth to access
- Acceptance: on every write of the local config and for each repository `update` is run for,
  R7 is carried out in place: stage accounts become their craft's access (D3048); a project whose
  cascade serves anything gets a pool with its members in its order, the paid key among them
  only where `fallback` is `auto` today (D3044); a repository on Vertex, written or defaulted,
  gets an explicit `access: vertex` (D3041 as amended); each repository's
  crafts are written, in the project's per-repository block, to what its runs go out on today,
  read from the configuration a run checks out (`deploymentConfig`); a file's own access is moved
  as written (D3042). It writes only what a craft does not already say, is idempotent, prints one
  line per repository, and names what it could not convert on every run. It asks nothing and
  fails no update.
- Files: `internal/cli/init_craft_migrate.go`, `internal/cli/init_access_migrate.go` (new),
  `internal/cli/init_access_migrate_test.go` (new), `internal/cli/init.go`,
  `internal/cli/update_converge_test.go`.
- Interfaces: consumes C1, CM1's resolver and CM2's block.
- Tests: `TestUpdateKeepsWhatEachCraftGoesOutOn` (one case per old auth value, the mix, and a
  file that names no auth), `TestADefaultedVertexIsWrittenExplicitly`,
  `TestTheCascadeBecomesAPoolInItsOrder`, `TestAStageAccountBecomesItsCraftsAccess`,
  `TestAFilesAccessMovesToTheProject`, `TestTheMigrationIsSilentTheSecondTime`,
  `TestWhatCannotBeConvertedIsNamedEveryTime`.
- Command: `go test ./internal/cli/ -run 'UpdateKeepsWhatEachCraft|ADefaultedVertexIsWritten|TheCascadeBecomesAPool|AStageAccountBecomes|AFilesAccessMoves|TheMigrationIsSilent|WhatCannotBeConverted|UpdateConverge' -count=1`.
- Red step: after `update`, a repository whose runs go out on a subscription resolves to `vertex`.
- Depends on: C1; CM1 (kept, behaviour: the migration writes what the resolver answers, and a
  stub's answer written into a config is a wrong config on disk); CM2 (kept: it writes the
  member CM2 adds to a shared struct, which a contract cannot own).
- Commit: `update: a repository's auth becomes its crafts' access (CM4, R7)`.
- Reviewer tier: opus, mandatory independent review.

### CM5: the access record is written beside the old one
- Acceptance: where `fugaro update` writes `creds/repoauth/<slug>` it also writes
  `creds/repoaccess/<slug>`: the credentials and pools that repository's crafts resolve to, and
  nothing else; a plan-only run writes neither; a failed write warns and fails nothing; the old
  record is written exactly as before (R4).
- Files: `internal/creds/repoaccess_store.go` (new), `internal/creds/repoaccess_store_test.go`
  (new), `internal/cli/update_repoauth.go`, `internal/cli/update_repoaccess_test.go` (new).
- Interfaces: consumes C1 (the record).
- Tests: `TestUpdateWritesBothRecords`, `TestTheAccessRecordNamesWhatTheCraftsResolveTo`,
  `TestAPlanWritesNeitherRecord`, `TestARecordOfAnotherVersionIsRefused`.
- Command: `go test ./internal/creds/ -run 'RepoAccess|ARecordOfAnotherVersion' -count=1 && go test ./internal/cli/ -run 'UpdateWritesBothRecords|TheAccessRecordNames|APlanWritesNeither|RepoAuth' -count=1`.
- Red step: `update` writes no `repoaccess` node.
- Depends on: C1.
- Commit: `update: the access record, beside the auth record (CM5, R4)`.
- Reviewer tier: opus, mandatory independent review.

### CM6: the service narrows by access
- Acceptance: a call that carries the new header is narrowed to what the access record names for
  the declared craft, and to nothing the record does not name; a call that carries the old header
  is narrowed by the old record exactly as today; a call that carries neither, where a record
  exists, is refused; a provider credential is offered wherever the record names it (the clause
  the keys plan's KS3 planned "under every auth", F21); the review class is untouched (R11); every
  refusal writes one audit line and names no value. It is past five files because one read has
  that many wrappers.
- Files: `internal/credsvc/cascade.go`, `internal/credsvc/wire.go`, `internal/credsvc/auth.go`,
  `internal/creds/state.go`, `internal/credsvc/scope.go`, `internal/credsvc/scope_stub.go`,
  `internal/credsvc/refusallog.go`, `internal/credsvc/cascade_access_test.go` (new).
- Interfaces: consumes C1 (the record, the header).
- Tests: `TestACallIsNeverServedBeyondItsRecord`, `TestAnOlderImagesCallIsNarrowedByTheOldRecord`,
  `TestACallThatDeclaresNothingIsRefused`, `TestTheAccessRefusalWritesOneAuditLine`,
  `TestAReviewCallerIsUnchanged`.
- Command: `go test ./internal/credsvc/ -run 'NeverServedBeyond|AnOlderImagesCall|DeclaresNothing|OneAuditLine|AReviewCallerIsUnchanged|Cascade|AuthMode' -count=1`.
- Red step: a call with the new header is refused as carrying no auth mode.
- Depends on: C1. Not launched before the keys plan's KS3 has merged (both edit `cascade.go`).
- Commit: `credsvc: a call is narrowed by its repository's access (CM6, R4, R5)`.
- Reviewer tier: opus, mandatory independent review, with a mutation check on the narrowing.

### CM7: the gateway carries the access
- Acceptance: the gateway's upstream carries, per stage, the craft a call is declared for and
  sends it in C1's header; a provider route through the service is admitted by what the stage's
  access is and no longer by the word `api-key` (`validateRoutes`); a call with no stage is
  refused, never sent undeclared. The old fields stay until I1.
- Files: `internal/gateway/gateway.go`, `internal/gateway/service.go`, `internal/gateway/route.go`,
  `internal/gateway/access_test.go` (new).
- Interfaces: consumes C1 (the header's name).
- Tests: `TestTheGatewayDeclaresTheStagesCraft`, `TestARouteIsAdmittedByItsAccess`,
  `TestACallWithNoStageIsRefused`.
- Command: `go test ./internal/gateway/ -run 'DeclaresTheStagesCraft|AdmittedByItsAccess|NoStageIsRefused|Service|Route' -count=1`.
- Red step: the service's upstream sends no access header.
- Depends on: C1.
- Commit: `gateway: a call declares its craft (CM7, R5)`.
- Reviewer tier: opus, mandatory independent review.

### CM8: the run's model path follows its access
- Acceptance: the run resolves each stage's access through C1's resolver and builds its gateway
  from the answer: a run has a gateway whatever its credentials are, and reaches the service
  wherever its job carries the "credentials served" fact; a subscription the job is not served
  for keeps today's path for this release (D3047); the consent switch is no longer required and
  the allow-list refusal stays (D3045); the review class is asked by its own name (R11);
  `cascade` is no longer refused for a local invocation, which never reaches the service (D3053);
  a usage-limit failure switches account where the credential that served is a subscription
  (F15). Every message it prints is rewritten (the brief's item 3).
- Files: `internal/runner/routing.go`, `internal/runner/gateway.go`, `internal/runner/runner.go`,
  `internal/runner/access_resolve.go` (new), `internal/runner/access_resolve_test.go` (new), and
  the one call of `subscriptionUsageLimit` in `internal/runner/accountchoice.go`.
- Interfaces: consumes C1; sets the upstream members CM7 adds and the environment member CM9 adds.
- Tests: `TestEveryAccessHasAGateway`, `TestNoSwitchNoWiderReach`,
  `TestASubscriptionNotServedKeepsItsPath`, `TestAReviewRunIsAskedByItsOwnName`,
  `TestALocalInvocationNeverReachesTheService`.
- Command: `go test ./internal/runner/ -run 'EveryAccessHasAGateway|NoSwitchNoWiderReach|ASubscriptionNotServed|AReviewRunIsAsked|ALocalInvocation|Routing|ServiceAuth' -count=1`.
- Red step: a run whose coder resolves to a subscription has no gateway where it is served.
- Depends on: C1; CM7 and CM9 (kept: it sets members those tasks add to shared structs);
  CM11 (kept: one file, `accountchoice.go`). Not launched before always a budget's AB1b has merged.
- Commit: `runner: the model path follows the craft's access (CM8, D3040, D3045)`.
- Reviewer tier: opus, mandatory independent review.

### CM9: the agent's environment, by credential type
- Acceptance: `BuildEnv` and `GatewayVars` take the type of the credential a stage resolves to
  (a new member beside `EnvSpec.Auth`, which I1 removes); with a gateway, a subscription presents
  the gateway's token and no `CLAUDE_CODE_OAUTH_TOKEN` is in the environment; without one, for
  this release, the token stays (D3047); a local invocation takes what the developer's
  environment holds in D3053's order, and with none is refused naming the three; the managed
  settings file follows the same answer.
- Files: `internal/agent/env.go`, `internal/agent/env_access_test.go` (new),
  `internal/runner/agentenv.go`, `internal/runner/agentenv_access_test.go` (new).
- Interfaces: consumes C1 (the types, the fact, the accessor's stub).
- Tests: `TestNoTokenInTheAgentsEnvironmentWhereServed`, `TestTheTokenStaysWhereNotServed`,
  `TestALocalInvocationUsesWhatTheEnvironmentHolds`, `TestALocalInvocationWithNothingIsRefused`,
  `TestAStageOnASubscriptionGoesThroughTheGateway`.
- Command: `go test ./internal/agent/ -run 'NoTokenInTheAgents|TheTokenStays|ALocalInvocation|Env' -count=1 && go test ./internal/runner/ -run 'AStageOnASubscription|AgentEnv|ManagedSettings' -count=1`.
- Red step: a subscription with a gateway is refused with "auth: oauth never goes through the
  gateway".
- Depends on: C1. Not launched before always a budget's AB1b has merged.
- Commit: `agent: a subscription goes through the gateway (CM9, D3047, D3053)`.
- Reviewer tier: opus, mandatory independent review.

### CM10: the record and the cost, by what served
- Acceptance: `model_basis`, a stage's `served_by` and `credential` are worked out from the type
  of the credential that served, and are what they were for every migrated configuration (F15);
  no member of the record is added, removed or retyped.
- Files: `internal/runner/cost.go`, `internal/runner/stephook.go`,
  `internal/runner/triage_stage.go`, `internal/runstore/cost.go`, `internal/runstore/runstore.go`,
  with their tests.
- Interfaces: consumes C1 (the accessor).
- Tests: `TestTheRecordIsUnchangedForAMigratedRun`, `TestTheBasisFollowsTheCodersCredential`,
  `TestTriageIsServedWhereItsCraftIsAKey`.
- Command: `go test ./internal/runner/ -run 'TheRecordIsUnchanged|Triage|StageTiming' -count=1 && go test ./internal/runstore/ -run 'TheBasisFollows|ModelBasis|CostReaders' -count=1`.
- Red step: `ModelBasis` is asked an auth word.
- Depends on: C1.
- Commit: `runstore: the basis and served-by follow the credential (CM10, F15)`.
- Reviewer tier: standard.

### CM11: the run's account rules, by credential
- Acceptance: the account walk applies where a stage's credential is a subscription; the shared
  project job's account check, the direct model call and a pool member's "held here" read the
  credential's type and never an auth word; the mix's rule ("one account for the review stages")
  is gone with the case it judged (D3048).
- Files: `internal/runner/accountchoice.go`, `internal/runner/projectjob.go`,
  `internal/runner/directcall.go`, `internal/runner/poolchoice.go`, with their tests.
- Interfaces: consumes C1 (the accessor).
- Tests: `TestTheAccountWalkFollowsTheCredential`, `TestAVertexMemberIsHeldWhereTheStageIsVertex`,
  `TestTheProjectJobsAccountIsCheckedForASubscription`.
- Command: `go test ./internal/runner/ -run 'TheAccountWalkFollows|AVertexMemberIsHeld|TheProjectJobsAccount|AccountChoice|PoolChoice|DirectCall' -count=1`.
- Red step: the walk is skipped for a craft on a subscription in a run whose file names no auth.
- Depends on: C1.
- Commit: `runner: account rules follow the credential (CM11, D3048)`.
- Reviewer tier: opus, mandatory independent review.

### CM12: the registry entry and the views
- Acceptance: a new run's entry carries no `auth` and C1's new member; `watch --json` prints
  `auth` as `-` and the member; `budget show --json` leaves `auth` out and adds the member;
  `Notional` follows the coder's credential type; an older run's entry reads as before; the
  database rules accept the member (U3), and a rules change, if one is needed, is deployed by
  `update` as the rules already are.
- Files: `internal/budget/model.go`, `internal/runner/budget_session.go`,
  `internal/watch/build.go`, `internal/watch/json.go`, `internal/cli/budget.go`, and
  `internal/budget/rules/rules.json.tmpl` if U3 requires it; with their tests.
- Interfaces: consumes C1 (the member's name, the accessor).
- Tests: `TestANewRunWritesNoAuth`, `TestWatchPrintsADashAndTheAccess`,
  `TestNotionalFollowsTheCredential`, `TestTheRulesAcceptTheNewMember`.
- Command: `go test ./internal/budget/ -run 'Rules|Registry' -count=1 && go test ./internal/watch/ -run 'ADashAndTheAccess|Notional|JSON' -count=1 && go test ./internal/runner/ -run ANewRunWritesNoAuth -count=1 && go test ./internal/cli/ -run BudgetShow -count=1`.
- Red step: a new run's entry carries `auth`.
- Depends on: C1.
- Commit: `watch: a run shows what it goes out on (CM12, D3050, R6)`.
- Reviewer tier: standard.

### CM13: the cloud side is planned from the crafts
- Acceptance: a new `infra.ServiceServes` answers from the types of the credentials a
  repository's crafts resolve to (true where any is a secret the service holds, the bundle is
  ready and the service installed); the job's mounts, its account variants, `vertex`,
  `model_secrets_served` and `stage_account` are worked out from the same answer and are what
  they were for every migrated configuration, so no `.tf` file changes (R12); the old function
  stays until I1.
- Files: `internal/infra/spec.go`, `internal/infra/access_spec.go` (new),
  `internal/infra/access_spec_test.go` (new), `internal/infra/spec_test.go`.
- Interfaces: consumes C1; produces `infra.ServiceServes`, called by CM14, CM15 and CM18.
- Tests: `TestTheMountsAreUnchangedForAMigratedRepository` (one case per old auth value and the
  mix), `TestAVertexOnlyRepositoryIsNotServed`, `TestTheTfvarsAreUnchanged`.
- Command: `go test ./internal/infra/ -run 'TheMountsAreUnchanged|AVertexOnlyRepository|TheTfvarsAreUnchanged|SecretMounts|WorkflowAccounts|ModelSecretsServed' -count=1 && go test -tags terraform ./internal/infra/... -run ModelSecretsServed -count=1`.
- Red step: `SecretMounts` is asked an auth word.
- Depends on: C1. Not launched before always a budget's AB15 and the keys plan's KS19 have merged.
- Commit: `infra: mounts and the switch follow the crafts (CM13, F6, R12)`.
- Reviewer tier: opus, mandatory independent review.

### CM14: the launch, by access
- Acceptance: `fugaro run` decides the account, the job variant and whether the job is told about
  the service from the crafts' access; `--token` names a subscription credential for the run's
  coder, only among those the project admits for the repository, and is refused otherwise with
  the list (D3049); the launch refusals that named the cascade's switch are gone and the
  allow-list one stays (D3045); `fugaro run --attach` phrases its route by credential.
- Files: `internal/cli/run.go`, `internal/cli/run_attach.go`,
  `internal/cli/run_access_test.go` (new), and the launch tests that pin the old messages
  (`cascade_test.go`, `run_credentials_env_test.go`, `cascade_stage_accounts_test.go`).
- Interfaces: consumes C1 and CM13's `infra.ServiceServes`.
- Tests: `TestTheLaunchFollowsTheCraftsAccess`, `TestTokenNamesAnAdmittedCredential`,
  `TestTokenOutsideTheAdmittedListIsRefused`, `TestNoLaunchRefusalNamesTheSwitch`.
- Command: `go test ./internal/cli/ -run 'TheLaunchFollowsTheCrafts|TokenNamesAnAdmitted|TokenOutsideTheAdmitted|NoLaunchRefusalNames|Cascade|RunCredentialsEnv' -count=1`.
- Red step: a launch reads `agent.auth` from the checkout to choose the job.
- Depends on: C1; CM13 (kept, behaviour: it decides whether a job is told about the service, and
  a stub there would launch a job with no credential).
- Commit: `run: the launch follows the crafts' access (CM14, D3049)`.
- Reviewer tier: opus, mandatory independent review.

### CM15: the gates and the bundle's readiness
- Acceptance: `credentialsBundleServes` asks whether the bundle holds every credential the
  repository's crafts resolve to, and records the one fact of C1 on the repository's entry, from
  which the job is planned; a launch whose craft resolves to a credential that is not served and
  has no mount is refused with the fix (the refusal the keys plan's KS6 planned, F19); the image
  gate is keyed on the fact and a new since-constant, not on an auth word; the shared project
  job picks its account from the craft; `fugaro secrets` wants the tokens the crafts name.
- Files: `internal/cli/init_credentials.go`, `internal/cli/launch_budget.go`,
  `internal/cli/project_job.go`, `internal/cli/recipes_skew.go`, `internal/cli/initsecrets.go`,
  `internal/cli/init_credentials_access_test.go` (new).
- Interfaces: consumes C1 (the fact) and CM13.
- Tests: `TestTheBundleIsReadyWhenItHoldsWhatTheCraftsNeed`,
  `TestACraftWhoseCredentialIsNotServedIsRefusedAtLaunch`, `TestTheImageGateFollowsTheFact`,
  `TestAnOperatorsRunLeavesTheFactAsItWas`.
- Command: `go test ./internal/cli/ -run 'TheBundleIsReadyWhen|ACraftWhoseCredential|TheImageGateFollows|AnOperatorsRunLeavesTheFact|InitCredentials|LaunchBudget|ProjectJob' -count=1`.
- Red step: a repository whose file names no auth is judged as Vertex and never ready.
- Depends on: C1; CM13 (kept, as CM14). Not launched before the keys plan's KS19 has merged.
- Commit: `update: readiness and the gates follow the crafts (CM15, F6, F19)`.
- Reviewer tier: opus, mandatory independent review.

### CM16: the config's checks, by reach
- Acceptance: a model is admitted where a declared credential reaches it and is admitted for the
  repository, and refused otherwise naming the model and what would reach it (this replaces
  `CheckProviderAuth` and the keys plan's KS6); an Anthropic key is "needed" where a craft
  resolves to one; the stage-account mix's checks are replaced by one: an access must reach its
  craft's model; the cascade's own validation (the public follow-up rule, the gateway rule) is
  asked of a craft that resolves to a pool the service orders. The old functions stay until I1.
- Files: `internal/config/access_checks.go` (new), `internal/config/access_checks_test.go` (new),
  `internal/config/providers.go`, `internal/config/stage_accounts.go`,
  `internal/config/validate.go`.
- Interfaces: consumes C1; produces `config.CheckReach` and `config.KeyNeeded`, called by CM17.
- Tests: `TestAModelNoCredentialReachesIsRefused`, `TestAProviderModelNeedsNoAuthWord`,
  `TestAKeyIsNeededWhereACraftResolvesToOne`, `TestThePublicFollowUpRuleFollowsThePool`.
- Command: `go test ./internal/config/ -run 'NoCredentialReaches|NeedsNoAuthWord|AKeyIsNeeded|PublicFollowUp|Providers|StageAccount' -count=1`.
- Red step: a provider's model on a subscription-first project is refused for its auth.
- Depends on: C1.
- Commit: `config: a model is judged by what reaches it (CM16, R2, F19)`.
- Reviewer tier: opus, mandatory independent review.

### CM17: validate, craft ls, models use and the enterprise code
- Acceptance: `fugaro validate` judges budget, providers, routing and crafts by CM16's checks and
  prints CM3's notes for each retired key; `fugaro craft ls` prints what each craft resolves to,
  with "by reach" where no one wrote it; `fugaro models use` and `ee/ab` admit a model by reach.
- Files: `internal/cli/validate.go`, `internal/cli/craft.go`, `internal/cli/models_use.go`,
  `ee/ab/admit.go`, `ee/ab/launch.go`, with their tests.
- Interfaces: consumes CM16 and CM3.
- Tests: `TestValidateNamesARetiredKey`, `TestValidateJudgesAModelByReach`,
  `TestCraftLsSaysByReach`, `TestABChallengerIsAdmittedByReach`.
- Command: `go test ./internal/cli/ -run 'ValidateNamesARetiredKey|ValidateJudgesAModel|CraftLsSaysByReach|ModelsUse|Routing' -count=1 && go test ./ee/ab/ -run 'Admit|ByReach' -count=1`.
- Red step: `craft ls` lists `vertex` for a craft a subscription reaches.
- Depends on: C1; CM16 (kept, behaviour: it calls the checks CM16 writes, and a stub of a
  refusal would admit what must be refused); CM3 (kept: it prints the list CM3 writes).
- Commit: `validate: models and crafts are judged by reach (CM17)`.
- Reviewer tier: standard.

### CM18: doctor
- Acceptance: every doctor check that named a repository's auth reads the crafts: the brokering
  checks (`flipServes`, `flipNotServed`, `doctorModelKeyInAgent`), the account checks and the
  routing checks; doctor names each retired key, each repository whose subscription is not yet
  served and what makes it served (R8), and an image older than the gate (D3052).
- Files: `internal/cli/doctor.go`, `internal/cli/doctor_brokering.go`,
  `internal/cli/project_status.go`, `internal/cli/doctor_access_test.go` (new).
- Interfaces: consumes CM3, CM13 and the helpers CM17 changes in `validate.go`.
- Tests: `TestDoctorNamesARetiredKey`, `TestDoctorNamesASubscriptionNotYetServed`,
  `TestNoDoctorLineNamesARepositorysAuth`.
- Command: `go test ./internal/cli/ -run 'DoctorNamesARetiredKey|DoctorNamesASubscription|NoDoctorLineNames|DoctorBrokering' -count=1`.
- Red step: a doctor line says "its agent.auth is".
- Depends on: CM3, CM13 (kept: it calls what they add); CM17 (kept: it calls helpers of
  `validate.go` whose signatures CM17 changes).
- Commit: `doctor: checks follow the crafts (CM18, R8)`.
- Reviewer tier: standard.

### CM19: the update says what will change, first
- Acceptance: before it changes anything, `fugaro update` prints R8's lines for the repository
  it is run for; each retired key in the checkout is offered its removal as a diff, by the
  anchor's precedent, written on `--yes` or the person's word and printed otherwise; a repository none of whose
  declared credentials reaches a craft's model is told so, with the two fixes, by `update` and by
  `setup` alike; with runs in flight it prints the one line R10 names; the Vertex and cascade gates of the update read the
  crafts. It asks nothing new.
- Files: `internal/cli/update_access_notice.go` (new),
  `internal/cli/update_access_notice_test.go` (new), `internal/cli/init.go`,
  `internal/cli/init_fugaroyaml.go`, `internal/cli/upgrade_cloud.go`,
  `internal/cli/update_in_flight.go` with `docs/design/update-in-flight.md` and the test that
  counts its lines. It is past five files for that pin.
- Interfaces: consumes CM3 and CM4.
- Tests: `TestUpdateSaysWhatEachCraftWillResolveTo`, `TestARetiredKeyIsOfferedAsADiff`,
  `TestTheInFlightLineForAccess`, `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/cli/ -run 'UpdateSaysWhatEachCraft|ARetiredKeyIsOffered|TheInFlightLineForAccess|UpdateLeavesARunInFlightRunning|UpdateInFlight|Anchor' -count=1`.
- Red step: an update that migrates a repository prints nothing about it.
- Depends on: CM4 (kept, behaviour: it prints the migration's own answer, and both edit
  `init.go`); CM3 (kept: it prints the list CM3 writes).
- Commit: `update: what changes is said before it changes (CM19, R8, R10)`.
- Reviewer tier: standard.

### CM20: the settings registry and the schemas
- Acceptance: the rows `agent.auth` and `layer.defaults.agent.auth` are marked retired and point
  at this folder (F23); the per-repository craft keys have rows; the three schemas keep the
  retired keys with a description that says so, and describe the new block.
- Files: `internal/settings/registry.go`, `schemas/settings.schema.json`,
  `schemas/fugaro.schema.json`, `schemas/project-layer.schema.json`, `schemas/schemas_test.go`.
- Interfaces: consumes CM2 and CM3.
- Tests: `TestTheRetiredRowsSaySo`, `TestTheSchemasDescribeTheRepoCrafts`, and the registry's
  own coverage test.
- Command: `go test ./internal/settings/ -run 'Registry|TheRetiredRows' -count=1 && go test ./schemas/ -count=1`.
- Red step: `fugaro settings agent.auth` describes a live key.
- Depends on: CM2 (kept: the registry's coverage test reads the struct CM2 changes); CM3.
- Commit: `settings: the retired auth rows and the per-repository crafts (CM20)`.
- Reviewer tier: standard.

### CM21: the console reads the new member
- Acceptance: the console's parser reads C1's member from `watch --json` where present and shows
  it on the Live screen in place of the auth column; a document without it, and one whose `auth`
  is `-`, both parse; the scrubber's vocabulary knows the member.
- Files: `console/src/shared/schema.ts`, `console/src/renderer/screens/live.ts`,
  `console/tools/scrub-fixture/scrub.ts`, `console/src/source/redact.ts`, and the unit tests and
  fixtures that hold them.
- Interfaces: consumes C1 (the member's name).
- Tests: the console's `schema`, `live` and `scrub-vocabulary` unit tests, with one new case each.
- Command: `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 schema live scrub-vocabulary`.
- Red step: a run row with the member and `auth: "-"` shows `-`.
- Depends on: C1.
- Commit: `console: the Live screen shows what a run goes out on (CM21, D3050)`.
- Reviewer tier: standard. A screenshot re-record, if one is pinned, is an owner step named in
  the pull request.

### CM22: the live checks, as scripts
- Acceptance: Checks 800 and 801 exist as one script each, registered where every check script
  is, each printing what it measures and exiting non-zero on a failed row (§8).
- Files: `scripts/live-checks/check800.sh` (new), `scripts/live-checks/check801.sh` (new),
  `scripts/live_checks_test.go`, `docs/gcp-live-checklist.md`.
- Interfaces: none.
- Tests: `TestLiveChecks` (the registry of scripts), and the documents test that reads the
  checklist.
- Command: `go test ./scripts/ -run LiveChecks -count=1 && go test ./internal/cli/ -run Docs -count=1`.
- Red step: the registry lists no script for Check 800.
- Depends on: none.
- Commit: `checks: 800 and 801, the auth retirement, as scripts (CM22)`.
- Reviewer tier: opus, mandatory independent review (a script the owner runs against a live
  project).
- Owner-run: the scripts themselves, after each release (§8). No agent runs one.

### CM23: the help texts and the CLI reference
- Acceptance: no command's help names `agent.auth` or a run by its auth (`budget preview`,
  `runs stats`, `secrets ls`, `setup`, `run --token`); the generated reference page is
  regenerated.
- Files: `internal/cli/budget_preview.go`, `internal/cli/runs_stats.go`,
  `internal/cli/secrets.go`, `internal/cli/setup.go`, `website/content/cli-reference.md`
  (generated by `go run ./ee/clidoc`).
- Interfaces: none.
- Tests: `TestNoHelpTextNamesARepositorysAuth` (new, in `internal/cli/help_access_test.go`), and
  the plugin's tests that read commands and flags.
- Command: `go test ./internal/cli/ -run 'NoHelpTextNames|Docs' -count=1 && go test ./plugin/ -count=1`.
- Red step: `fugaro run --help` says "whose agent.auth is oauth".
- Depends on: CM14 and CM17 (kept: they change help strings in `run.go` and `models_use.go`,
  and the reference is generated once, from all of them).
- Commit: `cli: no help text is phrased by a repository's auth (CM23)`.
- Reviewer tier: standard.

### I1: integration
- Acceptance: the two stub files are deleted and the real resolver and accessor are wired in;
  what release 1's tasks left for it is removed: `EnvSpec.Auth`, the old default of
  `craft.Resolve` and `config.DefaultCredential` with it (nothing defaults to Vertex), `infra.CredentialsServiceServes`, `config.CheckProviderAuth`,
  `config.CheckStageAccountMix` and its helpers, `config.ServiceServedAuths`,
  `runner.ServiceSwitchedAuth` and `GatewayOn`'s auth argument; nothing outside the migration
  and the retired-key notice reads `Agent.Auth`; an installation at the previous release, with
  one repository on each old auth value and one on the mix, converges through `fugaro update`
  alone, each run goes through its gateway where it is served, and a run in flight keeps
  running. It is past five files because it removes one function from each package that kept it.
- Files: `internal/craft/access_stub.go` (deleted), `internal/runner/access_stub.go` (deleted),
  `internal/config/retired_readers_guard_test.go` (new),
  `internal/cli/access_end_to_end_test.go` (new), `internal/e2e/update_test.go`, and the files
  that hold the functions named above.
- Interfaces: consumes every release 1 code task.
- Tests: `TestNoReaderOfTheRetiredAuth`, `TestAnInstallationAtThePreviousReleaseConverges`,
  `TestEveryServedRunGoesThroughItsGateway`, `TestUpdateLeavesARunInFlightRunning`.
- Command: `go build ./... && go vet ./... && go test ./internal/config/ -run NoReaderOfTheRetiredAuth -count=1 && go test ./internal/cli/ -run 'AnInstallationAtThePreviousRelease|EveryServedRun|UpdateLeavesARunInFlightRunning' -count=1 && go test ./internal/e2e/ -run Update -count=1`.
- Red step: the guard test finds a reader.
- Depends on: C1, CM1, CM4 to CM19, and CM21 (the console's parser is held to the document the
  CLI now prints, by the parity fixture).
- Commit: `access: the stubs go and no reader of the auth mode is left (I1)`.
- Reviewer tier: opus, mandatory independent review.

### CM24: `SECURITY.md`, its page and the rules file
- Acceptance: `SECURITY.md` states the boundary as it now is (a repository is served the
  credentials its record names; a subscription is served by the service wherever the bundle is
  ready, and where it is not the token is in the agent's environment for this release) and no
  sentence is phrased by a repository's auth; the website page is regenerated;
  `.claude/rules/credentials-service.md` and the one sentence of `CLAUDE.md` say the same; the
  pinned sentences move with their tests.
- Files: `SECURITY.md`, `website/content/security.md` (generated by `go run ./internal/clidoc`),
  `.claude/rules/credentials-service.md`, `CLAUDE.md`, `docs/go-to-market/security-check.md`,
  `internal/cli/docs_security_test.go`.
- Interfaces: none.
- Tests: `TestSecurityMdStatesTheCredentialsService`,
  `TestSecurityMdNamesTheCredentialsServicesKnownLimits`, `TestSecurityMdStatesTheBitbucketGitProxy`,
  and a new `TestSecurityMdNamesNoRepositoryByItsAuth`.
- Command: `go test ./internal/cli/ -run 'SecurityMd|Docs' -count=1 && (cd website && npm run check)`.
- Red step: the new test finds "an `oauth` repository".
- Depends on: I1 (kept: a rules file and a security statement must be true of the tree they
  are merged into).
- Commit: `docs: SECURITY.md and the rules say what a repository is served (CM24)`.
- Reviewer tier: opus, mandatory independent review.

### CM25: the credentials documents and the checklist
- Acceptance: `docs/multi-model.md`, `docs/credentials-service/default-on.md` and `runbook.md`
  and `docs/gcp-live-checklist.md` describe credentials, crafts and pools, and name no repository
  by its auth; their pinned sentences move with their tests.
- Files: `docs/multi-model.md`, `docs/credentials-service/default-on.md`,
  `docs/credentials-service/runbook.md`, `docs/gcp-live-checklist.md`,
  `internal/cli/docs_credentials_auth_list_test.go`, `internal/cli/docs_credentials_runbook_test.go`.
- Interfaces: none.
- Tests: `TestDocsCredentialsAuthListNamesCascade` (rewritten for the credential types),
  `TestDocsMultiModelDocumentsTheCascadeSwitch` (rewritten for the pool),
  `TestDocsCredentialsRunbookSaysWhatTheLaunchWires`.
- Command: `go test ./internal/cli/ -run Docs -count=1`.
- Red step: the rewritten test fails on the old sentence.
- Depends on: I1 (kept, as CM24).
- Commit: `docs: credentials, crafts and pools, not auth modes (CM25)`.
- Reviewer tier: standard.

### CM26: the accounts documents, the README and the release notes
- Acceptance: `docs/project-claude-accounts.md`, `docs/design/mixed-credentials.md`,
  `docs/design/agent-env.md`, `docs/project-layer.md` and `README.md` say the same; the release's
  notes have `### For operators`: run `fugaro update`, and what it will say (the access each
  craft resolves to, the retired keys offered as a diff, the repositories whose subscription is
  not yet served); the website's summary line is added.
- Files: `docs/project-claude-accounts.md`, `docs/design/mixed-credentials.md`,
  `docs/design/agent-env.md`, `docs/project-layer.md`, `README.md`, the release's notes under
  `docs/releases/`, `website/data/release-summaries.yaml`, and the tests
  `docs_mixed_credentials_test.go`, `docs_usage_limit_switch_test.go`,
  `docs_project_named_oauth_tokens_test.go`, `docs_project_layer_test.go`. It is past five files
  for those pins.
- Interfaces: none.
- Tests: the four files' tests, rewritten where their sentence is.
- Command: `go test ./internal/cli/ -run Docs -count=1 && (cd website && npm run check)`.
- Red step: a rewritten test fails on the old sentence.
- Depends on: I1 (kept, as CM24).
- Commit: `docs: accounts and stages are crafts' access (CM26)`.
- Reviewer tier: standard.

### CM27: the plugin's skills
- Acceptance: the setup skill no longer asks for `agent.auth`: its second decision is which
  credentials the project declares and what each craft goes out on, with Vertex offered as a
  choice a person makes and never as what happens when they make none, and it writes nothing about
  credentials into a repository's file; the working skill's references describe a usage-limit
  switch and notional spend by credential; every command and flag a skill names exists.
- Files: `plugin/skills/setup/SKILL.md`, `plugin/skills/setup/reference/decisions.md`,
  `plugin/skills/setup/reference/transitions.md`, `plugin/skills/working/reference/diagnose.md`,
  `plugin/skills/working/reference/periodic-reviews.md`,
  `plugin/skills/working/reference/status.md`, `plugin/setup_skill_test.go`, and
  `TestSetupSkillCascadeOfferIsPinnedToC119` in `internal/cli/docs_credentials_auth_list_test.go`.
  It is past five files because the decision is spread over them.
- Interfaces: none.
- Tests: `TestSetupSkillContent`, `TestSetupSkillVerifiesBeforeRecommending`,
  `TestSetupSkillRecommendationAnchors`, the skills' lint and byte budgets.
- Command: `go test ./plugin/ -count=1 && go test ./internal/cli/ -run 'SetupSkill|Docs' -count=1`.
- Red step: the setup skill's test still requires the `vertex` row of the auth table.
- Depends on: I1 (kept, as CM24); CM25 (kept: one test file).
- Commit: `plugin: setup declares credentials, not an auth mode (CM27)`.
- Reviewer tier: opus, mandatory independent review (a plugin skill).

### CM28: release 2: the token's old path goes
- Acceptance: `BuildEnv` has no arm that puts a subscription token in the agent's environment
  and `OAuthStageEnv` is deleted; a launch whose craft resolves to a subscription the service
  does not serve is refused with the fix (a project owner runs `fugaro update`); a launch on a
  job image older than release 1 is refused with the command that rebuilds it; a local
  invocation is unchanged (D3053); `SECURITY.md`'s sentence about the token in the environment is
  removed with its test; the update's in-flight line says a run in flight keeps the environment
  it started with.
- Files: `internal/agent/env.go`, `internal/runner/agentenv.go`, `internal/cli/launch_budget.go`,
  `internal/cli/recipes_skew.go`, `SECURITY.md` with `website/content/security.md` and
  `internal/cli/docs_security_test.go`, `internal/cli/update_in_flight.go` with its document and
  test. It is past five files for those pins.
- Interfaces: none new.
- Tests: `TestNoArmPutsATokenInTheAgentsEnvironment`, `TestALaunchNotServedIsRefusedWithTheFix`,
  `TestAnOlderImageIsRefusedAtLaunch`, `TestALocalInvocationUsesWhatTheEnvironmentHolds`.
- Command: `go test ./internal/agent/ -run 'NoArmPutsAToken|ALocalInvocation|Env' -count=1 && go test ./internal/cli/ -run 'ALaunchNotServed|AnOlderImageIsRefused|SecurityMd|UpdateInFlight' -count=1 && (cd website && npm run check)`.
- Red step: an unserved subscription run still launches with the token in its environment.
- Depends on: I1 and CM24 (kept: `SECURITY.md`). **Not launched before release 1 is cut and the
  keys plan's KS13 has merged.**
- Commit: `agent: no token in the agent's environment (CM28, D3047)`.
- Reviewer tier: opus, mandatory independent review.

### CM29: release 2: the retired keys of a file and a layer are rejected
- Acceptance: a repository's file or a project layer that writes `agent.auth`,
  `agent.oauth_token` or a craft's `access` is rejected with the fix, the one-line removal;
  `fugaro update` still offers the removal as a diff before anything else; the two schemas lose
  the keys and keep `version` 1 (D3052); the image gate of CM28 is what keeps an older image
  from a file without the key.
- Files: `internal/config/config.go`, `internal/config/validate.go`, `internal/config/layer.go`,
  `internal/config/scope.go`, `internal/config/retired.go`, `schemas/fugaro.schema.json`,
  `schemas/project-layer.schema.json`, `schemas/schemas_test.go`. It is past five files because
  the key is declared in that many.
- Interfaces: none new.
- Tests: `TestAFileThatWritesTheAuthIsRejectedWithTheFix`, `TestALayerThatWritesItIsRejected`,
  `TestTheSchemasNoLongerListTheKey`, `TestUpdateOffersTheRemovalFirst`.
- Command: `go test ./internal/config/ -run 'WritesTheAuthIsRejected|ALayerThatWritesIt|Retired' -count=1 && go test ./schemas/ -count=1 && go test ./internal/cli/ -run UpdateOffersTheRemovalFirst -count=1`.
- Red step: a file that writes `agent.auth` validates.
- Depends on: I1. **Not launched before release 1 is cut.**
- Commit: `config: agent.auth is rejected, with the fix (CM29, D3043, D3052)`.
- Reviewer tier: standard.

### CM30: release 2: the installation's account keys and the node's members
- Acceptance: on its next write the installation's config drops `stages.<stage>.claude_account`
  and `default_claude_account`, both already converted by CM4, and names any it could not
  convert instead of dropping it; the node's `claude_account_stages` and
  `default_claude_account` are no longer written and still decode, so the node keeps its
  version; the settings rows and schema follow.
- Files: `internal/localcfg/localcfg.go`, `internal/cli/init_access_migrate.go`,
  `internal/cli/project_cmd.go`, `internal/settings/registry.go`,
  `schemas/settings.schema.json`, `schemas/schemas_test.go`.
- Interfaces: none new.
- Tests: `TestTheConvertedAccountKeysAreDropped`, `TestAnUnconvertedKeyIsKeptAndNamed`,
  `TestTheNodeNoLongerCarriesStageAccounts`, `TestAnOlderNodeStillDecodes`.
- Command: `go test ./internal/localcfg/ -run 'ConvertedAccountKeys|UnconvertedKey' -count=1 && go test ./internal/cli/ -run 'TheNodeNoLongerCarries|ProjectPolicy' -count=1 && go test ./internal/projectpolicy/ -run AnOlderNodeStillDecodes -count=1 && go test ./schemas/ ./internal/settings/ -count=1`.
- Red step: a config whose stage accounts were converted still writes them.
- Depends on: I1; CM29 (kept: one file, `schemas/schemas_test.go`).
- Commit: `localcfg: the converted account keys go (CM30, D3048)`.
- Reviewer tier: opus, mandatory independent review.

### CM31: release 2: the old record stops being written
- Acceptance: `fugaro update` no longer writes `creds/repoauth/<slug>`; it deletes a
  repository's old record only where that repository has no run in flight, and otherwise leaves
  it and says so in one line; the service still narrows a call that carries the old header by an
  old record that exists, and refuses it where none does; a new runner never sends the old
  header. The old header's parser stays for one more release (§10).
- Files: `internal/cli/update_repoauth.go`, `internal/cli/update_repoaccess_test.go`,
  `internal/gateway/service.go`, `internal/gateway/gateway.go`, `internal/credsvc/cascade.go`.
- Interfaces: none new.
- Tests: `TestUpdateWritesOnlyTheAccessRecord`, `TestAnOldRecordIsKeptWhileARunIsInFlight`,
  `TestANewRunnerSendsNoAuthHeader`, `TestTheAccessRefusalWritesOneAuditLine`.
- Command: `go test ./internal/cli/ -run 'UpdateWritesOnlyTheAccessRecord|AnOldRecordIsKept|RepoAuth' -count=1 && go test ./internal/gateway/ -run ANewRunnerSendsNoAuthHeader -count=1 && go test ./internal/credsvc/ -run 'Cascade|OneAuditLine' -count=1`.
- Red step: `update` writes both records.
- Depends on: I1. **Not launched before release 1 is cut.**
- Commit: `update: the auth record is no longer written (CM31, R4)`.
- Reviewer tier: opus, mandatory independent review.

## 5. Parallel execution

The decoupling pass is on (`planning.decoupling` is `true` for this repository), and the
repository's mode is `startup`, with no area rules, so the pass decouples wherever an interface
is all the dependent needs.

**Levels.** Tasks in one level run at once. **A launcher follows this table**, and the waits in
its last column (R9, D3051).

| Level | Tasks | Waits outside this plan |
| --- | --- | --- |
| 0 | C1, CM22 | always a budget's I1 has merged; the keys plan's README says KS6, KS9 and KS10 are withdrawn (O4) |
| 1 | CM1, CM2, CM3, CM5, CM6, CM7, CM9, CM10, CM11, CM12, CM13, CM16, CM21 | CM6: the keys plan's KS3. CM9: AB1b. CM13: AB15 and KS19 |
| 2 | CM4, CM8, CM14, CM15, CM17, CM20 | CM8: AB1b. CM15: KS19 |
| 3 | CM18, CM19, CM23 | none |
| 4 | I1 | none |
| 5 | CM24, CM25, CM26 | none |
| 6 | CM27 | none |
| 7 | CM28, CM29, CM31 | release 1 is cut; CM28 also the keys plan's KS13 |
| 8 | CM30 | none |

**What must be sequential, and why.** C1 before everything (the names). CM1 and CM2 before CM4
(the migration writes the resolver's answer into the block). CM4 before CM19 (the notice prints
the migration's answer). Everything before I1. The documents after I1 (they must be true of the
tree). Release 2 after release 1 is cut (an image of release 1 must exist before a path it
replaces is removed).

**Edges broken by C1** (each dependent needs a name or a signature, not the other side's code):

| Dependent | Was waiting on | What it needed |
| --- | --- | --- |
| CM6 | CM5 | the record's shape, not its writer |
| CM7 | CM6 | the header's name, not the service's narrowing |
| CM8 | CM1 | the resolver's interface, not its rule |
| CM9, CM11 | CM1 | the resolved credential's type, not the resolver's rule |
| CM10, CM12 | CM8 | the run's accessor, not the runner's wiring |
| CM13, CM16 | CM1 | the resolver's interface |
| CM14 | CM8 | the fact's name and the header, not the runner |
| CM21 | CM12 | the registry member's name |

**Edges kept**, each with its reason, are on the tasks' "Depends on" lines. The ones a reader
might expect broken: CM1 to CM4 (a stub's answer written into a config is a wrong config on
disk); CM13 to CM14 and CM15, and CM16 to CM17 (a stub on a path that decides a credential or a
refusal would launch or admit wrongly); CM7 and CM9 to CM8 (members of shared structs, which a
contract cannot own without owning the file).

**Shared files.** No level holds two writers of one file:

| File | Writers, by level |
| --- | --- |
| `internal/cli/init.go` | CM4 (2), CM19 (3) |
| `internal/cli/update_repoauth.go` | CM5 (1), CM31 (7) |
| `internal/credsvc/cascade.go` | CM6 (1), CM31 (7) |
| `internal/gateway/gateway.go`, `service.go` | CM7 (1), CM31 (7) |
| `internal/runner/accountchoice.go` | CM11 (1), CM8 (2) |
| `internal/agent/env.go`, `internal/runner/agentenv.go` | CM9 (1), CM28 (7) |
| `internal/infra/spec.go` | CM13 (1), I1 (4) |
| `internal/config/providers.go`, `stage_accounts.go` | CM16 (1), I1 (4) |
| `internal/config/validate.go` | CM16 (1), CM29 (7) |
| `internal/config/scope.go`, `retired.go` | CM3 (1), CM29 (7) |
| `internal/localcfg/localcfg.go`, `internal/cli/project_cmd.go` | CM2 (1), CM30 (8) |
| `internal/cli/validate.go`, `models_use.go` | CM17 (2) only; CM18 and CM23 (3) read them |
| `internal/cli/launch_budget.go`, `recipes_skew.go` | CM15 (2), CM28 (7) |
| `internal/cli/init_access_migrate.go` | CM4 (2), CM30 (8) |
| `internal/cli/update_in_flight.go` and its document | CM19 (3), CM28 (7) |
| `internal/settings/registry.go`, the schemas, `schemas/schemas_test.go` | CM20 (2), CM29 (7), CM30 (8) |
| `docs/gcp-live-checklist.md` | CM22 (0), CM25 (5) |
| `SECURITY.md`, its page, `docs_security_test.go` | CM24 (5), CM28 (7) |
| `internal/cli/docs_credentials_auth_list_test.go` | CM25 (5), CM27 (6) |
| the two stub files | C1 (0), I1 (4) |

**The graph read.** `fugaro plan graph --plan docs/credentials-belong-to-models/implementation-plan.md`
reports:

`critical path (7 tasks): C1 -> CM16 -> CM17 -> CM18 -> I1 -> CM24 -> CM28`, with C1 the
bottleneck, and `gain: 11 broken, 21 kept, 3 added; before 8 tasks, weight 16; after 7 tasks, weight 13; improves`.
The graph cannot record a wait on C1 for a task whose edge the pass did not move there (CM1 to
CM5, CM15, CM17 and I1 build or join its other sides), so its own levels put CM1, CM2, CM3 and
CM5 beside C1 and count seven levels where the table above has nine. The table is the order to
launch in. By the table, the longest chain is C1, CM1, CM4, CM19, I1, CM24, CM27, then the
release, then CM29 and CM30.

## 6. The estimate

As the same command printed it:

`ready in 345–1380 min, no history yet for 34 tasks (34 tasks; seeds for 34; from the seeds: no history is cached on this machine yet)`

It is informational and reserves no budget: it is what the tasks' tiers and this machine's
history predict, and it moves as runs finish. It does not count the waits on the two plans
before this one, the release between levels 6 and 7, or the owner's runs of Checks 800 and 801.
The graph counts one node more than this plan's 33 runs: the contract change of §7, which runs
only if raised.

## 7. Contract changes anticipated

- **R-pool-members** (C1, raised by CM4): if U4 shows the cascade's members for one repository
  cannot be said as a pool of named credentials with per-credential admission, the record gains
  what is missing. Reaches CM5 and CM6; a launched one is reached by a follow-up run on its pull
  request that names `internal/creds/repoaccess.go` by path.

## 8. Checks the plan takes

From the reserved block 800–809; both owner-run, by one script each (CM22). No agent runs a step.

- **Check 800, after release 1.** On an installation at the previous release with a repository
  on a subscription, one on an Anthropic key, one on the cascade and one on Vertex, and a run in
  flight: `fugaro update` migrates all four and says what each craft resolves to; the run in
  flight finishes; a new run of each launches, goes through its gateway and is recorded with the
  same `model_basis` as before; the service's audit log holds one line per refusal provoked by a
  hand-built call that declares a craft its record does not name (U5, O1).
- **Check 801, after release 2.** A run on a subscription has no token in its agent's
  environment; a launch on an image older than release 1 is refused with the rebuild command; a
  file that still writes `agent.auth` is rejected with the fix; an old record is kept while a
  run is in flight and gone after.

## 9. After-deploy steps

Each release's `### For operators` is "run `fugaro update`", plus what it will say:

- **Release 1** (CM26 writes it): the access each craft of each repository resolves to; each
  retired key a file still writes, offered as a diff to commit; each repository whose
  subscription is not yet served by the credentials service and that a project owner's
  `fugaro update` makes it served. Then the owner runs Check 800.
- **Release 2** (the release's own notes, written by CM28): a repository whose file still writes
  a retired key is rejected until the offered removal is committed; a job image older than
  release 1 must be rebuilt, which `update` offers behind its own confirmation, as today. Then
  the owner runs Check 801.

No step is another command, a deletion by hand or a repair.

## 10. Considered and declined

- **Vertex as the last resort of an unset access, and every reaching credential in order**
  (D3041's first answer): the owner amended it, because a credential nobody named would bill
  the project. The name `native` for Vertex was also declined: `vertex` is the built-in
  credential's name everywhere already.
- **A per-model list of credentials, and a fixed order by kind** (D3040's other options): a
  second config surface beside crafts and pools; or spending nobody chose.
- **Keeping `agent.crafts.<craft>.access` in a repository's file** (D3042): the one tie between
  a file and credentials the owner's two rulings reject.
- **Rejecting the key at once, and `fugaro.yaml` version 2** (D3043, D3052): the first stops
  every repository until a commit and sends an older image to Vertex; the second has every older
  CLI and image refuse every repository.
- **The service calling Vertex** (D3046): a project-wide Vertex grant on the service's account,
  for no key moved.
- **A gateway that holds the mounted token** (D3047): the token would stay mounted in the job,
  which the keys plan removes.
- **Removing the old header's parser and deleting every old record in release 2**: a run in
  flight on an image older than release 1 would lose the record its calls are narrowed by. The
  parser and the last records go in the release after, as a follow-up task with no decision in
  it.
- **Changing a Terraform variable** (R12): `vertex`, `model_secrets_served` and `stage_account`
  keep their meaning, and the keys plan's KS14 already removes the model mounts.
- **Renaming `--token`, and making the cascade's settings a pool's own** (O5, O6): later
  changes with their own readers.
