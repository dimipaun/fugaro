# Credentials belong to models, not repositories: implementation plan

**Status: plan only; nothing is implemented; no task is approved.** Each task needs the owner's
explicit go, by name. This plan builds [spec.md](spec.md) as amended by
[reconciliation.md](reconciliation.md); where they disagree the reconciliation wins. Decisions are
cited as `D#`, findings as `F#`, resolutions as `R#`. Checked against `origin/main` at `d57d71ddb`
on 2026-11-20.

**Amended 2026-11-22: [model credentials routing](../model-credentials-routing/implementation-plan.md)
supersedes this plan's craft access (D3170), and every task here that is not merged is
withdrawn.** That plan's routing table makes how a model is reached a property of the model, set
once in the project, so a craft no longer names a credential. What is merged is kept — CM35, CM1,
CM3, CM22 and CM32 — with the two-release removal of `agent.auth` (R3) and the rule that an
unmigrated repository runs as its file says. **Contract C1 is withdrawn, its draft pull
request #3460 still open and to be closed unmerged by the owner** (that plan's O1), and re-cut as
that plan's C1; its section is kept below as the
record. The withdrawn tasks' sections are kept and marked, each naming the task that carries it, or
that it is not replaced (CM2); the graph marks the withdrawn nodes in their titles. The owner stops
the withdrawn tasks by hand before any launches (that plan's O1). Nothing below is a launch order
any more: §5's levels, §6's
estimate, §7's contract change and §8's Checks 810 and 811 are the record of what was planned.

| Earlier task | State | Carried by, in model credentials routing |
| --- | --- | --- |
| CM35, CM1, CM3, CM22, CM32 | merged | kept, as merged |
| C1 (the access contract) | draft #3460 | withdrawn; its C1 |
| CM2 (per-repository crafts) | not started | withdrawn, not replaced: one table per project, and its RT2 carries admission |
| CM4, CM19 | not started | MG1, MG2 |
| CM5, CM31 | not started | RT4 and MG3; X4 |
| CM6, CM36 | not started | CS1, PL3 |
| CM7 | not started | CS2 |
| CM8 | not started | CS6, CS13 |
| CM9, CM11 | not started | CS7 |
| CM10 | not started | CS8 |
| CM12 | not started | CS9 |
| CM13 | not started | CS11 |
| CM14, CM15 | not started | CS10, CS12, CS14, CS15, RT7 |
| CM16 | not started | RT3 |
| CM17, CM18 | not started | RT6, RT7 |
| CM20 | not started | MG5 |
| CM21 | not started | VS1 |
| CM23 | not started | MG6 |
| CM34 | not started | MG4 |
| I1 | not started | I1 |
| CM24 to CM27 | not started | DOC1 to DOC4 |
| CM28, CM29, CM30, CM33 | not started | X1, X2, X3, X5; its X6 to X8 are new |
| Checks 810 and 811 | scripts merged, not run | replaced by its Checks 884 and 885; its DOC2 marks them so in the live checklist |

**The decisions the amendment reverses or amends:** D3060 (a craft's access names the credential)
by D3170 and D3171, D3064 (the cascade's marked pool) by D3183, D3066 (Vertex on the job's
identity) by D3174, D3068 (per-stage accounts) by D3171, and D3069 (`--token`) by D3176. D3061's
intent is kept: Vertex is reached only where a chain names it. D3063 and D3072 stand.

**The answers that shaped it** (all the owner's, 2026-11-20, all as recommended): a craft's
access names the credential (D3060); an unset access is the first declared credential that reaches the model, and never Vertex
unless named (D3061, amended by the owner the same day); access is the project's alone (D3062); the old key is read, ignored
and named for one release, then rejected with no version bump (D3063, D3072); the cascade becomes a pool marked as the cascade's (D3064, amended after review) and its consent switch is no longer required (D3065); Vertex stays on the job's
identity through the gateway (D3066); the subscription token leaves the agent's environment in
two releases (D3067); stage accounts become craft access (D3068); `--token` stays (D3069); the
views print `-` and gain a member (D3070); it ships after the two plans before it have started
(D3071); a local invocation uses the developer's environment (D3073).

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
| A repository is never offered a credential its record does not name, whatever its call declares, with the consent switch absent (D3065, O1) | CM6 `TestACallIsNeverServedBeyondItsRecord`, CM8 `TestNoSwitchNoWiderReach` |
| Exactly one audit line per refusal on the new narrowing | CM6 `TestTheAccessRefusalWritesOneAuditLine` |
| An older image's call (old header, old record) is served exactly as before through release 1 | CM6 `TestAnOlderImagesCallIsNarrowedByTheOldRecord` |
| No subscription token is in the agent's environment where the job carries the "credentials served" fact; it stays where the job does not (D3067) | CM9 `TestNoTokenInTheAgentsEnvironmentWhereServed`, `TestTheTokenStaysWhereNotServed` |
| A run in flight is disturbed in nothing it holds, and the update says the one line it owes | CM19 `TestUpdateLeavesARunInFlightRunning`, `TestTheInFlightLineForAccess` |
| Nothing reads `agent.auth` after release 1 but the migration and the retired-key notice | I1 `TestNoReaderOfTheRetiredAuth` |
| The run record's `model_basis`, `served_by` and `credential` are byte for byte what they were for a migrated configuration, a run the service serves included (F25) | CM10 `TestTheRecordIsUnchangedForAMigratedRun`, `TestServedByIsAbsentWhereTheServiceServes` |
| An older image reads the shared node exactly as before, a service not yet redeployed never meets a member it refuses, and a repository not yet migrated runs as its file says (R3, R15) | CM32 `TestTheSharedNodeCarriesNoMarker`, CM4 `TestTheProjectLevelCraftsAreNotRewritten`, CM2 `TestThePerRepositoryNodeIsUntouched`, C1 `TestTheLegacyRuleAnswersEveryOldAuthValue` |
| A run that names an admitted account with `--token`, and a walk to one, are served as today (F34, F35, F44) | CM5 `TestTheRecordNamesEveryAccountTokenMayName`, CM6 `TestAChosenCredentialMustBeInTheRecord`, CM11 `TestAChosenAccountIsNeverWalked` |
| A project-level change never leaves a repository's record stale (F34) | CM34 `TestAProjectChangeRewritesEveryRepositorysRecord` |
| Only a pool `update` wrote for a serving cascade is served a subscription with the spread switch off (D3064 as amended) | CM36 `TestAnUnmarkedPoolStillObeysTheSwitch`, `TestAMarkerOutsideTheRecordIsIgnored`, CM32 `TestTheMarkerIsRefusedWhereNoCascadeServes` |
| No call is served past the access record for want of a header, and nothing moves from a subscription to a paid key without a pool (F42, F43) | CM6 `TestACallWithoutTheNewHeaderIsNeverServedBeyondTheAccessRecord`, `TestAnUnsetCraftIsServedOneCredentialForItsModel` |
| A run is never sent to Vertex because its file names nothing (F49) | CM8 `TestNoEntryAndNoRetiredKeyIsRefusedNeverVertex` |

| An installation at the previous release converges through `fugaro update` alone | I1 `TestAnInstallationAtThePreviousReleaseConverges` |
| Release 2 never leaves a repository with neither a token nor a service that serves it | CM28 `TestALaunchNotServedIsRefusedWithTheFix`, `TestAnOlderImageIsRefusedAtLaunch` |

## 3. The contract, and the task before it

### CM35: the keys plan says what this plan replaces

*Merged, and kept (D3170). **Amended 2026-11-22:** the keys plan's KS13, which this task pointed
at this plan's I1, now waits on model credentials routing's I1, and the keys plan's README says
so (that plan's A1).*

- Acceptance: `docs/keys-through-service/` says, in its README, reconciliation, plan and graph,
  that KS6, KS9 and KS10 are withdrawn and replaced by this plan's CM16 and CM15, CM6, and CM9;
  that KS13 waits on this plan's I1 in their place; that C1's second fact is not needed (F20);
  and that KS3's "under every auth" clause is CM6's; its graph still reads; `docs/status.md`
  is not touched.
- Files: `docs/keys-through-service/README.md`, `docs/keys-through-service/reconciliation.md`,
  `docs/keys-through-service/implementation-plan.md`,
  `docs/keys-through-service/implementation-plan.graph.json`.
- Interfaces: none.
- Tests: the documents tests.
- Command: `go test ./internal/cli/ -run Docs -count=1 && fugaro plan graph --plan docs/keys-through-service/implementation-plan.md`.
- Red step: the keys plan's graph has KS13 waiting on KS10.
- Depends on: none, and it waits on nothing outside this plan either: it launches first and
  alone, before the keys plan's KS6 and KS9 can become ready (F50).
- Commit: `docs(keys-through-service): KS6, KS9 and KS10 are replaced by credentials-belong-to-models (CM35)`.
- Reviewer tier: standard.

### C1: the access contract (withdrawn 2026-11-22 by D3170)

*Withdrawn, its draft pull request #3460 still open and to be closed unmerged by the owner (that
plan's O1), and re-cut as
[model credentials routing](../model-credentials-routing/implementation-plan.md)'s C1 (D3170). The
section is kept as the record of what was withdrawn; every task below that depended on it is
withdrawn with it.*

- Acceptance: one place declares what every side of this plan shares. (a) The question: for a
  stage, and for the task's grade where it names one (F38), the access (a credential or a pool),
  each member's name, type and serving path, whether the credential was chosen or is the
  project's default account (F35), and an interface `Resolver` that answers it. (b) How a run
  learns its access: the name of the entry the launcher puts the resolved answer under in the
  execution's environment, and its encoding; a run with no such entry applies the legacy rule, and where its
  file writes no retired key either is refused with the fix, never sent to Vertex (R15, F49). (c) That rule itself, as a real function and not a stub: the access each old auth
  value, stage account, file `access` and grade `access` means; a file that writes none of them
  means nothing, never Vertex. It is the migration's rule, and
  the contract holds it because the migration, the launch and the runner must give one answer.
  (d) The new record `creds/repoaccess/<slug>`: what a repository's runs may be served by, per
  craft and grade (for an unset one, one credential per model family, F43), the subscription
  credentials `--token` may name, and whether a pool carries
  the cascade's marker; its version and its path; and the statement that a run's reach is the
  union over its crafts (R4). (e) The headers a new
  runner declares its craft in, its task's grade where it names one, and the credential it chose
  where it chose one, with their validation (R5, F44). (f) The registry entry's new member (R6). (g) The
  per-repository craft block's key in the installation's config, and the path and shape of the
  node of its own it is published in (F9, F47). (h) One recorded
  fact per repository, "its credentials are served", and the job entry a run reads it from
  (F20); CM15 records it. A stub resolver answers (c) alone, so every side compiles and behaves
  as today until its real side lands; a stub accessor gives the runner the same.
  The names live in the package that needs them, because `internal/gateway` cannot import
  `internal/credsvc` and `internal/config` imports neither; that is why it is past five files.
- Files: `internal/craft/access.go` (new), `internal/craft/legacy.go` (new, the rule of (c)),
  `internal/craft/access_stub.go` (new, stub), `internal/craft/access_test.go` (new),
  `internal/runner/access_stub.go` (new, stub), `internal/creds/repoaccess.go` (new),
  `internal/credsvc/access_wire.go` (new), `internal/credsvc/access_wire_test.go` (new),
  `internal/gateway/accesswire.go` (new), `internal/config/access_names.go` (new),
  `docs/credentials-belong-to-models/contract-access.md` (new).
- Interfaces: produces all of the above; consumed by every code task.
- Tests: `TestTheLegacyRuleAnswersEveryOldAuthValue`, `TestTheStubAnswersTheLegacyRule`,
  `TestTheRecordNamesCredentialsAndNothingElse`, `TestTheAccessHeaderIsNotTheAuthHeader`,
  `TestTheGatewayAndTheServiceSpellTheHeaderAlike`.
- Command: `go test ./internal/craft/ -run 'TheLegacyRule|TheStubAnswers' -count=1 && go test ./internal/creds/ -run TheRecordNames -count=1 && go test ./internal/credsvc/ -run 'TheAccessHeader|SpellTheHeaderAlike' -count=1`.
- Red step: the header's name equals `X-Fugaro-Auth-Mode`.
- Depends on: CM35 (kept: the keys plan must say what this contract replaces before a side is written
  against it).
- Commit: `craft: the access contract (C1, R1, R4, R5)`.
- Reviewer tier: opus, mandatory independent review (`fugaro review --pr N`) before any side is
  written against it: it is a contract over credentials and a wire.

## 4. Slices and tasks

**Release 1. Slice 1, the question and its answer:** CM1 to CM3. **Slice 0, the plan before it:** CM35. **Slice 2, the migration and the record:** CM4, CM5, CM34. **Slice 3, the service and the run:** CM6 to CM12. **Slice 4, the launch and
the cloud side:** CM13 to CM15. **Slice 5, the readers that judge and report:** CM16 to CM20, CM23. The cascade's pool, CM32 and CM36, is part of slices 2 and 3. **Slice 6, the console and the checks:** CM21, CM22. **Integration:** I1. **Slice 7, the
documents:** CM24 to CM27.
**Release 2. Slice 8, what release 1 kept readable goes:** CM28 to CM31, CM33.

### CM1: the resolver, by reach

*Merged, and kept (D3170).*

- Acceptance: a pure function over the config's own types answers, for one craft's model and a
  project's declared credentials, providers and admissions: a written access as written; an unset
  one as the first declared credential, in the order the project lists them, that reaches the
  model and admits the repository, and nothing after it; Vertex only where an access or a pool
  names it; an unset access nothing declared reaches is a problem that names the two fixes,
  declare a credential or write `access: vertex` (D3061 as amended, R2); "unset" is judged after
  a craft has followed the craft it inherits from (`config.CraftParent`); an access whose
  credential cannot reach its craft's model is a problem at the key that wrote it. It imports
  nothing of the contract; I1 puts it behind C1's interface.
- Files: `internal/craft/reach.go` (new), `internal/craft/reach_test.go` (new).
- Interfaces: consumes `config.Credential`, `config.Pool`, `config.ProviderFor`, `config.IsClaudeModel`;
  produces `craft.ByReach`.
- Tests: `TestASubscriptionAndAKeyBothReachAClaudeModel`,
  `TestAProviderCredentialReachesOnlyItsProvidersModels`,
  `TestAnUnsetAccessIsTheFirstReachingCredentialOnly`, `TestVertexIsNeverImplied`,
  `TestAnUnsetAccessNothingReachesIsRefusedWithTheFix`, `TestAnAccessThatCannotReachItsModelIsAProblem`.
- Command: `go test ./internal/craft/ -run 'Reach|UnsetAccess|VertexIsNeverImplied|CannotReach' -count=1`.
- Red step: an unset access with a subscription declared answers `vertex`.
- Depends on: none.
- Commit: `craft: an unset access is the first credential that reaches, never Vertex (CM1, D3061)`.
- Reviewer tier: opus, mandatory independent review.

### CM2: the project's per-repository crafts (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; withdrawn and not replaced: one table per project, and that plan's RT2 carries admission.*

- Acceptance: the installation's config accepts a crafts block on a repository's entry; it is
  published in a node of its own beside the per-repository node, at C1's path, which no older
  CLI or service reads, so nothing is gated and the per-repository node is left exactly as it
  was (F47, R15); the rules that cover the policy tree give the new node the reads and writes
  the per-repository node has; a repository's block may only select among credentials and pools
  the project holds.
- Files: `internal/localcfg/localcfg.go`, `internal/projectpolicy/policy.go`,
  `internal/projectpolicy/paths.go`, `internal/cli/project_cmd.go` (the node's writer),
  `internal/budget/rules/rules.json.tmpl`, `internal/localcfg/repo_crafts_test.go` (new),
  `internal/projectpolicy/repo_crafts_test.go` (new).
- Interfaces: consumes C1 (the block's key).
- Tests: `TestARepositorysCraftsAreRead`, `TestARepositorysCraftNamesOnlyWhatTheProjectHolds`,
  `TestTheRepoCraftsNodeIsItsOwn`, `TestThePerRepositoryNodeIsUntouched`,
  `TestTheRulesCoverTheRepoCraftsNode`.
- Command: `go test ./internal/localcfg/ -run ARepositorysCraft -count=1 && go test ./internal/projectpolicy/ -run 'TheRepoCraftsNode|DecodeRepo' -count=1 && go test ./internal/budget/ -run Rules -count=1 && go test ./internal/cli/ -run 'ThePerRepositoryNodeIsUntouched|RepoPolicy|ProjectPolicy' -count=1`.
- Red step: a crafts block on a repository's entry is refused as an unknown key.
- Depends on: C1.
- Commit: `config: the project names a repository's crafts (CM2, D3062, F9)`.
- Reviewer tier: opus, mandatory independent review.

### CM3: the retired keys are named

*Merged, and kept (D3170).*

- Acceptance: one function lists, for a repository's file and for a project layer, each retired
  key it still writes (R3), with the path and the sentence a person reads; the keys still decode
  and still validate; `config.Scopes` marks them retired. It changes no reader.
- Files: `internal/config/retired.go` (new), `internal/config/retired_test.go` (new),
  `internal/config/scope.go`, `internal/config/example.yaml` (the template `fugaro config example`
  prints stops writing `auth`).
- Interfaces: produces the list of retired keys and `config.RetiredKeyNotes`, read by CM17, CM18, CM19
  and CM20.
- Tests: `TestEveryRetiredKeyIsNamedWhereItIsWritten`, `TestAFileWithNoRetiredKeySaysNothing`,
  `TestARetiredKeyStillDecodes`. `TestTheExampleWritesNoRetiredKey`.
- Command: `go test ./internal/config/ -run 'RetiredKey|NoRetiredKey|TheExampleWrites|Scope' -count=1`.
- Red step: a file that writes `agent.auth` yields no note.
- Depends on: none.
- Commit: `config: the retired keys, named where they are written (CM3, D3063, R3)`.
- Reviewer tier: standard.

### CM4: `fugaro update` migrates auth to access (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's MG1, MG2.*

- Acceptance: on every write of the local config and for each repository `update` is run for,
  R7 is carried out in place, by C1's rule: stage accounts become their craft's access (D3068); a
  project whose cascade serves anything gets a pool marked as the cascade's, with its members in
  its order, the paid key among them only where `fallback` is `auto` today (D3064 as amended); a
  repository on Vertex, written or defaulted, gets an explicit `access: vertex` (D3061 as
  amended); each repository's crafts are written, in the project's per-repository block and
  nowhere an older image reads (R15), to what its runs go out on today, read from the
  configuration a run checks out (`deploymentConfig`), the layer's inherited value included; a file's
  own access, a grade's included, is moved as written (D3062, F38); each written access carries
  whether its account was chosen or was the project's default (F35). It writes only what a craft
  does not already say, is idempotent, prints one line per repository, and names what it could not convert on
  every run. It asks nothing and fails no update.
- Files: `internal/cli/init_craft_migrate.go`, `internal/cli/init_access_migrate.go` (new),
  `internal/cli/init_access_migrate_test.go` (new), `internal/cli/init.go`,
  `internal/cli/update_converge_test.go`.
- Interfaces: consumes C1's rule, CM1's function, CM2's block and CM32's marker.
- Tests: `TestUpdateKeepsWhatEachCraftGoesOutOn` (one case per old auth value, the mix, and a
  file that names no auth), `TestADefaultedVertexIsWrittenExplicitly`,
  `TestTheCascadeBecomesAMarkedPoolInItsOrder`, `TestAStageAccountBecomesItsCraftsAccess`,
  `TestAFilesAccessMovesToTheProject`, `TestTheProjectLevelCraftsAreNotRewritten`,
  `TestTheMigrationIsSilentTheSecondTime`, `TestWhatCannotBeConvertedIsNamedEveryTime`.
- Command: `go test ./internal/cli/ -run 'UpdateKeepsWhatEachCraft|ADefaultedVertexIsWritten|TheCascadeBecomesAMarkedPool|AStageAccountBecomes|AFilesAccessMoves|TheProjectLevelCrafts|TheMigrationIsSilent|WhatCannotBeConverted|UpdateConverge' -count=1`.
- Red step: after `update`, a repository whose runs go out on a subscription resolves to `vertex`.
- Depends on: CM1 (kept, behaviour: the migration writes what the function answers, and a stub's
  answer written into a config is a wrong config on disk); CM2 and CM32 (kept: it writes members
  they add to shared structs, which a contract cannot own).
- Commit: `update: a repository's auth becomes its crafts' access (CM4, R7)`.
- Reviewer tier: opus, mandatory independent review.

### CM5: the access record is written beside the old one (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's RT4 and MG3.*

- Acceptance: where `fugaro update` writes `creds/repoauth/<slug>` it also writes
  `creds/repoaccess/<slug>` with what C1 (d) says: per craft and grade the access the project
  wrote or, where it wrote none, one credential per model family, the first that reaches it
  (F43); the subscription credentials `--token` may name; and the cascade's marker for a
  pool that carries it (R4 as amended, F34); the old record is derived from the same answer and
  no longer from the file's key, so a file with the key removed leaves the old record as it was
  (F27); a plan-only run writes neither; a failed write warns and fails nothing.
- Files: `internal/creds/repoaccess_store.go` (new), `internal/creds/repoaccess_store_test.go`
  (new), `internal/cli/update_repoauth.go`, `internal/cli/update_repoaccess_test.go` (new).
- Interfaces: consumes C1 (the record).
- Tests: `TestUpdateWritesBothRecords`, `TestTheAccessRecordNamesWhatARunMayBeServedBy`,
  `TestTheRecordNamesEveryAccountTokenMayName`, `TestTheOldRecordSurvivesTheKeysRemoval`,
  `TestAPlanWritesNeitherRecord`, `TestARecordOfAnotherVersionIsRefused`.
- Command: `go test ./internal/creds/ -run 'RepoAccess|ARecordOfAnotherVersion' -count=1 && go test ./internal/cli/ -run 'UpdateWritesBothRecords|TheAccessRecordNames|TheRecordNamesEveryAccount|TheOldRecordSurvives|APlanWritesNeither|RepoAuth' -count=1`.
- Red step: `update` writes no `repoaccess` node.
- Depends on: C1; CM2 (kept: it reads the block CM2 adds to a shared struct).
- Commit: `update: the access record, beside the auth record (CM5, R4)`.
- Reviewer tier: opus, mandatory independent review.

### CM6: the service narrows by access (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's CS1, PL3.*

- Acceptance: a call that carries the new headers is served what the access record names for
  the declared craft, or grade, and the call's model, and nothing the record does not name; a
  declared credential is served only where the record lists it for `--token` or the walk (F44);
  a call with the new header and no access record is refused; a call that lacks the new header,
  whether it carries the old one or none, is narrowed by the old record as today and then,
  wherever an access record exists, to nothing that record does not name (F30, F42); a provider
  credential is offered wherever the record names it (the clause the keys plan's KS3 planned
  "under every auth", F21); the review class is untouched (R11); every refusal writes one audit
  line and names no value. It is past five files because one read has that many wrappers.
- Files: `internal/credsvc/cascade.go`, `internal/credsvc/wire.go`, `internal/credsvc/auth.go`,
  `internal/creds/state.go`, `internal/credsvc/scope.go`, `internal/credsvc/scope_stub.go`,
  `internal/credsvc/refusallog.go`, `internal/credsvc/cascade_access_test.go` (new).
- Interfaces: consumes C1 (the record, the header).
- Tests: `TestACallIsNeverServedBeyondItsRecord`, `TestAnUnsetCraftIsServedOneCredentialForItsModel`,
  `TestAChosenCredentialMustBeInTheRecord`, `TestANewHeaderWithNoRecordIsRefused`,
  `TestAnOlderImagesCallIsNarrowedByTheOldRecord`,
  `TestACallWithoutTheNewHeaderIsNeverServedBeyondTheAccessRecord` (old header, and none),
  `TestTheAccessRefusalWritesOneAuditLine`, `TestAReviewCallerIsUnchanged`.
- Command: `go test ./internal/credsvc/ -run 'NeverServedBeyond|AnUnsetCraftIsServedOne|AChosenCredentialMust|ANewHeaderWithNoRecord|AnOlderImagesCall|OneAuditLine|AReviewCallerIsUnchanged|Cascade|AuthMode' -count=1`.
- Red step: a call with the new header is refused as carrying no auth mode.
- Depends on: C1.
- Commit: `credsvc: a call is narrowed by its repository's access (CM6, R4, R5)`.
- Reviewer tier: opus, mandatory independent review, with a mutation check on the narrowing.

### CM7: the gateway carries the access (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's CS2.*

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

### CM8: the run's model path follows its access (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's CS6, CS13.*

- Acceptance: the run takes each stage's access from the entry its launcher put in the
  execution's environment, and where there is none applies C1's legacy rule, refused with the
  fix where the file writes no retired key either (R15, F49); it builds its
  gateway from the answer: a run has a gateway whatever its credentials are, and reaches the
  service wherever its job carries the "credentials served" fact; a subscription the job is not
  served for keeps today's path for this release (D3067); the consent switch is no longer
  required and the allow-list refusal stays (D3065); the review class is asked by its own name
  (R11); `cascade` is no longer refused for a local invocation, which never reaches the service
  (D3073); a usage-limit failure switches account as today (F25, F35); nothing the run works out
  for itself widens what the service serves it. Every message it prints is rewritten, the halt
  reasons included (the brief's item 3).
- Files: `internal/runner/routing.go`, `internal/runner/gateway.go`, `internal/runner/runner.go`,
  `internal/runner/halt.go`, `internal/runner/access_resolve.go` (new),
  `internal/runner/access_resolve_test.go` (new), and the one call of `subscriptionUsageLimit` in
  `internal/runner/accountchoice.go`. It is past five files for the messages.
- Interfaces: consumes C1; sets the upstream members CM7 adds and the environment member CM9 adds.
- Tests: `TestEveryAccessHasAGateway`, `TestNoSwitchNoWiderReach`,
  `TestASubscriptionNotServedKeepsItsPath`, `TestAReviewRunIsAskedByItsOwnName`,
  `TestALocalInvocationNeverReachesTheService`. `TestARunWithNoHandedAccessAppliesTheLegacyRule`. `TestNoEntryAndNoRetiredKeyIsRefusedNeverVertex`.
- Command: `go test ./internal/runner/ -run 'EveryAccessHasAGateway|NoHandedAccess|NoEntryAndNoRetiredKey|NoSwitchNoWiderReach|ASubscriptionNotServed|AReviewRunIsAsked|ALocalInvocation|Routing|ServiceAuth' -count=1`.
- Red step: a run whose coder resolves to a subscription has no gateway where it is served.
- Depends on: C1; CM7 and CM9 (kept: it sets members those tasks add to shared structs);
  CM11 (kept: one file, `accountchoice.go`). Not launched before always a budget's AB1b has merged.
- Commit: `runner: the model path follows the craft's access (CM8, D3060, D3065)`.
- Reviewer tier: opus, mandatory independent review.

### CM9: the agent's environment, by credential type (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's CS7.*

- Acceptance: `BuildEnv` and `GatewayVars` take the type of the credential a stage resolves to
  (a new member beside `EnvSpec.Auth`, which I1 removes); with a gateway, a subscription presents
  the gateway's token and no `CLAUDE_CODE_OAUTH_TOKEN` is in the environment; without one, for
  this release, the token stays (D3067); a local invocation takes what the developer's
  environment holds in D3073's order, and with none is refused naming the three; the managed
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
- Commit: `agent: a subscription goes through the gateway (CM9, D3067, D3073)`.
- Reviewer tier: opus, mandatory independent review.

### CM10: the record and the cost, by what served (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's CS8.*

- Acceptance: each field keeps the rule it has, said without the auth word (F25): a stage's
  `served_by` and `credential` name a credential the job itself holds and are absent where the
  service serves the stage; `model_basis` is `subscription` where the coder's access is one
  subscription credential and `api-list` otherwise, a pool included; the record of every
  migrated configuration is byte for byte what it was; no member is added, removed or retyped.
- Files: `internal/runner/cost.go`, `internal/runner/stephook.go`,
  `internal/runner/triage_stage.go`, `internal/runstore/cost.go`, `internal/runstore/runstore.go`,
  with their tests.
- Interfaces: consumes C1 (the accessor).
- Tests: `TestTheRecordIsUnchangedForAMigratedRun` (one case per old auth value, switched and
  not), `TestServedByIsAbsentWhereTheServiceServes`, `TestAPoolsBasisIsApiList`,
  `TestTriageIsServedWhereItsCraftIsAKey`.
- Command: `go test ./internal/runner/ -run 'TheRecordIsUnchanged|ServedByIsAbsent|Triage|StageTiming' -count=1 && go test ./internal/runstore/ -run 'APoolsBasis|ModelBasis|CostReaders' -count=1`.
- Red step: `ModelBasis` is asked an auth word.
- Depends on: C1.
- Commit: `runstore: the basis and served-by follow the credential (CM10, F15)`.
- Reviewer tier: standard.

### CM11: the run's account rules, by credential (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's CS7.*

- Acceptance: the account walk applies where a stage's access is one subscription credential
  that is the project's default account and was not chosen, the two conditions it has today
  (F35);
  the shared project job's account check, the direct model call and a pool member's "held here"
  read the credential's type and never an auth word; a subscription member of the cascade's pool, which the launcher's entry marks, is not
  skipped for the spread switch; the layers the runner hands the
  resolver are C1's (b): the file's own `access` is read only for a repository not migrated; the
  mix's rule ("one account for the review stages") is gone with the case it judged (D3068).
- Files: `internal/runner/accountchoice.go`, `internal/runner/projectjob.go`,
  `internal/runner/directcall.go`, `internal/runner/poolchoice.go`, with their tests.
- Interfaces: consumes C1 (the accessor, the entry).
- Tests: `TestTheAccountWalkFollowsTheCredential`, `TestAVertexMemberIsHeldWhereTheStageIsVertex`,
  `TestTheProjectJobsAccountIsCheckedForASubscription`. `TestAChosenAccountIsNeverWalked`.
- Command: `go test ./internal/runner/ -run 'TheAccountWalkFollows|AChosenAccount|AVertexMemberIsHeld|TheProjectJobsAccount|AccountChoice|PoolChoice|DirectCall' -count=1`.
- Red step: the walk is skipped for a craft on a subscription in a run whose file names no auth.
- Depends on: C1. Not launched before the keys plan's KS5 has merged (both edit `poolchoice.go`).
- Commit: `runner: account rules follow the credential (CM11, D3068)`.
- Reviewer tier: opus, mandatory independent review.

### CM12: the registry entry and the views (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's CS9.*

- Acceptance: a new run's entry carries no `auth` and C1's new member, and `credential` keeps
  its meaning (F29); `watch --json` prints `auth` as `-` and the member; `budget show --json`
  leaves `auth` out and adds the member; `Notional` is true where the coder's access is one
  subscription credential, as today (F25); an older run's entry reads as before; the database
  rules gain the member, deployed by `update` as the rules are, and a runner that meets rules
  without it drops the member and writes the rest, as it does for `recipe`.
- Files: `internal/budget/model.go`, `internal/budget/registry.go`,
  `internal/budget/rules/rules.json.tmpl`, `internal/budget/lease.go` and `session.go` (their
  messages), `internal/runner/budget_session.go`, `internal/watch/build.go`,
  `internal/watch/json.go`, `internal/cli/budget.go`, with their tests. It is past five files
  because one member is printed in that many places.
- Interfaces: consumes C1 (the member's name, the accessor).
- Tests: `TestANewRunWritesNoAuth`, `TestWatchPrintsADashAndTheAccess`,
  `TestNotionalIsOneSubscriptionOnly`, `TestTheRulesAcceptTheNewMember`,
  `TestTheMemberIsDroppedUnderOlderRules`.
- Command: `go test ./internal/budget/ -run 'Rules|Registry|DroppedUnderOlderRules' -count=1 && go test ./internal/watch/ -run 'ADashAndTheAccess|Notional|JSON' -count=1 && go test ./internal/runner/ -run ANewRunWritesNoAuth -count=1 && go test ./internal/cli/ -run BudgetShow -count=1`.
- Red step: a new run's entry carries `auth`.
- Depends on: C1; CM2 (kept: one file, `rules.json.tmpl`).
- Commit: `watch: a run shows what it goes out on (CM12, D3070, R6)`.
- Reviewer tier: standard.

### CM13: the cloud side is planned from the crafts (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's CS11.*

- Acceptance: a new `infra.ServiceServes` answers from the types of the credentials a
  repository's crafts resolve to (true where any is a secret the service holds, the bundle is
  ready and the service installed); the job's mounts, its account variants, `vertex`,
  `model_secrets_served` and `stage_account` are worked out from the same answer and are what
  they were for every migrated configuration, so no `.tf` file changes (R12); the old function
  stays until I1.
- Files: `internal/infra/spec.go`, `internal/infra/access_spec.go` (new),
  `internal/infra/access_spec_test.go` (new), `internal/infra/spec_test.go`,
  `internal/infra/retire.go` (its message).
- Interfaces: consumes C1; produces `infra.ServiceServes`, called by CM14, CM15 and CM18.
- Tests: `TestTheMountsAreUnchangedForAMigratedRepository` (one case per old auth value and the
  mix), `TestAVertexOnlyRepositoryIsNotServed`, `TestTheTfvarsAreUnchanged`.
- Command: `go test ./internal/infra/ -run 'TheMountsAreUnchanged|AVertexOnlyRepository|TheTfvarsAreUnchanged|SecretMounts|WorkflowAccounts|ModelSecretsServed' -count=1 && go test -tags terraform ./internal/infra/... -run ModelSecretsServed -count=1`.
- Red step: `SecretMounts` is asked an auth word.
- Depends on: C1. Not launched before always a budget's AB15 and the keys plan's KS19 have merged.
- Commit: `infra: mounts and the switch follow the crafts (CM13, F6, R12)`.
- Reviewer tier: opus, mandatory independent review.

### CM14: the launch, by access (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's CS10, CS12, CS14, CS15, RT7.*

- Acceptance: `fugaro run` decides the account, the job variant and whether the job is told about
  the service from the crafts' access; it resolves each craft's and grade's access and puts the
  answer in the execution's environment under C1's entry, for a migrated repository, and puts
  nothing there for one that is not (R15); `--token` names a subscription credential for the
  run's coder, only among those the project admits for the repository, and is refused otherwise
  with the list (D3069); the launch refusals that named the cascade's switch are gone and the
  allow-list one stays, and removing the routing block is no longer refused for the cascade's
  sake (D3065); `fugaro run --attach` phrases its route by credential; a follow-up and `fugaro review` choose
  their account the same way, `review --token` defaulting to the craft's credential and no longer
  to `agent.oauth_token` (F48); every path that launches an execution hands the entry (U8);
  `credentialsServiceLaunchEnv` keeps its signature until I1. It changes no help string; CM23
  does.
- Files: `internal/cli/run.go`, `internal/cli/run_attach.go`, `internal/cli/project_routing.go`,
  `internal/cli/followup.go`, `internal/cli/review.go`, `internal/cli/run_access_test.go` (new), and the launch tests that pin the old messages
  (`cascade_test.go`, `run_credentials_env_test.go`, `cascade_stage_accounts_test.go`), which no
  other task of its level edits.
- Interfaces: consumes C1 and CM13's `infra.ServiceServes`.
- Tests: `TestTheLaunchFollowsTheCraftsAccess`, `TestTokenNamesAnAdmittedCredential`,
  `TestTokenOutsideTheAdmittedListIsRefused`, `TestNoLaunchRefusalNamesTheSwitch`. `TestTheLauncherHandsTheRunItsAccess`, `TestAnUnmigratedRepositoryIsHandedNothing`. `TestEveryLaunchPathHandsTheEntry`.
- Command: `go test ./internal/cli/ -run 'TheLaunchFollowsTheCrafts|EveryLaunchPathHands|TheLauncherHandsTheRun|AnUnmigratedRepositoryIsHanded|TokenNamesAnAdmitted|TokenOutsideTheAdmitted|NoLaunchRefusalNames|Cascade|RunCredentialsEnv' -count=1`.
- Red step: a launch reads `agent.auth` from the checkout to choose the job.
- Depends on: C1; CM13 (kept, behaviour: it decides whether a job is told about the service, and
  a stub there would launch a job with no credential); CM32 (kept: it reads the marker CM32 adds
  to a shared struct).
- Commit: `run: the launch follows the crafts' access (CM14, D3069)`.
- Reviewer tier: opus, mandatory independent review.

### CM15: the gates and the bundle's readiness (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's CS10, CS12, CS14, CS15, RT7.*

- Acceptance: `credentialsBundleServes` asks whether the bundle holds every credential the
  repository's crafts resolve to, and records the one fact of C1 on the repository's entry, and plans the job's
  entry from it; `recordCredentialsServiceReady` keeps its signature until I1, so its call site
  in `init.go` is untouched; a launch whose craft resolves to a credential that is not served and
  has no mount is refused with the fix (the refusal the keys plan's KS6 planned, F19); the image
  gate is keyed on the fact and a new since-constant, not on an auth word; the shared project
  job picks its account from the craft; `fugaro secrets` wants the tokens the crafts name.
- Files: `internal/cli/init_credentials.go`, `internal/cli/launch_budget.go`,
  `internal/cli/project_job.go`, `internal/cli/recipes_skew.go`, `internal/cli/initsecrets.go`,
  `internal/localcfg/localcfg.go` (the fact's field on a repository's entry),
  `internal/infra/spec.go` (the job's entry),
  `internal/cli/init_credentials_access_test.go` (new). It is past five files because the fact
  is recorded, planned from and gated on in that many.
- Interfaces: consumes C1 (the fact) and CM13.
- Tests: `TestTheBundleIsReadyWhenItHoldsWhatTheCraftsNeed`,
  `TestACraftWhoseCredentialIsNotServedIsRefusedAtLaunch`, `TestTheImageGateFollowsTheFact`,
  `TestAnOperatorsRunLeavesTheFactAsItWas`.
- Command: `go test ./internal/cli/ -run 'TheBundleIsReadyWhen|ACraftWhoseCredential|TheImageGateFollows|AnOperatorsRunLeavesTheFact|InitCredentials|LaunchBudget|ProjectJob' -count=1`.
- Red step: a repository whose file names no auth is judged as Vertex and never ready.
- Depends on: C1; CM13 (kept, as CM14). Not launched before the keys plan's KS19 has merged.
- Commit: `update: readiness and the gates follow the crafts (CM15, F6, F19)`.
- Reviewer tier: opus, mandatory independent review.

### CM16: the config's checks, by reach (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's RT3.*

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

### CM17: validate, craft ls, models use and the enterprise code (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's RT6, RT7.*

- Acceptance: `fugaro validate` judges budget, providers, routing and crafts by CM16's checks and
  prints CM3's notes for each retired key; `fugaro craft ls` prints what each craft resolves to,
  with "by reach" where no one wrote it; `fugaro models use` and `ee/ab` admit a model by reach. It changes no help string; CM23 does.
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

### CM18: doctor (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's RT6, RT7.*

- Acceptance: every doctor check that named a repository's auth reads the crafts: the brokering
  checks (`flipServes`, `flipNotServed`, `doctorModelKeyInAgent`), the account checks and the
  routing checks; doctor names each retired key, each repository whose subscription is not yet
  served and what makes it served (R8), and an image older than the gate (D3072); it does not ask for an owner for the cascade's pool (F36).
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

### CM19: the update says what will change, first (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's MG2.*

- Acceptance: before it changes anything, `fugaro update` prints R8's lines for the repository
  it is run for; each retired key in the checkout is offered its removal as a diff, by the
  anchor's precedent, exactly as the anchor's diff is handled today, with nothing typed, and only
  where every job image of that repository is at or after release 1 (F27), the images named
  where it is not; a repository none of whose declared credentials reaches a craft's model is
  told so, with the two fixes, by `update` and by `setup` alike, which are one engine (`init.go`); with runs in flight it prints
  the one line R10 names; the Vertex and cascade gates of the update read the crafts. It asks
  nothing new.
- Files: `internal/cli/update_access_notice.go` (new),
  `internal/cli/update_access_notice_test.go` (new), `internal/cli/init.go`,
  `internal/cli/init_fugaroyaml.go`, `internal/cli/upgrade_cloud.go`,
  `internal/cli/update_in_flight.go` with `docs/design/update-in-flight.md` and the test that
  counts its lines. It is past five files for that pin.
- Interfaces: consumes CM3 and CM4.
- Tests: `TestUpdateSaysWhatEachCraftWillResolveTo`, `TestARetiredKeyIsOfferedAsADiff`,
  `TestTheRemovalIsNotOfferedOnAnOlderImage`, `TestTheInFlightLineForAccess`,
  `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/cli/ -run 'UpdateSaysWhatEachCraft|ARetiredKeyIsOffered|TheRemovalIsNotOffered|TheInFlightLineForAccess|UpdateLeavesARunInFlightRunning|UpdateInFlight|Anchor' -count=1`.
- Red step: an update that migrates a repository prints nothing about it.
- Depends on: CM4 (kept, behaviour: it prints the migration's own answer, and both edit
  `init.go`); CM3 (kept: it prints the list CM3 writes).
- Commit: `update: what changes is said before it changes (CM19, R8, R10)`.
- Reviewer tier: standard.

### CM20: the settings registry and the schemas (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's MG5.*

- Acceptance: the rows `agent.auth` and `layer.defaults.agent.auth` are marked retired and point
  at this folder (F23); the per-repository craft keys have rows; the three schemas keep the
  retired keys with a description that says so, and describe the new block.
- Files: `internal/settings/registry.go`, `schemas/settings.schema.json`,
  `schemas/fugaro.schema.json`, `schemas/project-layer.schema.json`,
  `schemas/result.schema.json` and `schemas/task.schema.json` (two descriptions that name the
  auth mode; no member changes), `schemas/schemas_test.go`. It is past five files for those.
- Interfaces: consumes CM2 and CM3.
- Tests: `TestTheRetiredRowsSaySo`, `TestTheSchemasDescribeTheRepoCrafts`, and the registry's
  own coverage test.
- Command: `go test ./internal/settings/ -run 'Registry|TheRetiredRows' -count=1 && go test ./schemas/ -count=1`.
- Red step: `fugaro settings agent.auth` describes a live key.
- Depends on: CM2 (kept: the registry's coverage test reads the struct CM2 changes); CM3.
- Commit: `settings: the retired auth rows and the per-repository crafts (CM20)`.
- Reviewer tier: standard.

### CM21: the console reads the new member (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's VS1.*

- Acceptance: the console's parser reads C1's member from `watch --json` where present and shows
  it on the Live screen in place of the auth column; a document without it, and one whose `auth`
  is `-`, both parse; the scrubber's vocabulary knows the member.
- Files: `console/src/shared/schema.ts`, `console/src/renderer/screens/live.ts`,
  `console/tools/scrub-fixture/scrub.ts`, `console/src/source/redact.ts`,
  `console/docs/json-contract.md` (which defines `auth` and `notional` by the auth mode), and the
  unit tests and fixtures that hold them.
- Interfaces: consumes C1 (the member's name).
- Tests: the console's `schema`, `live` and `scrub-vocabulary` unit tests, with one new case each.
- Command: `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 schema live scrub-vocabulary`.
- Red step: a run row with the member and `auth: "-"` shows `-`.
- Depends on: C1.
- Commit: `console: the Live screen shows what a run goes out on (CM21, D3070)`.
- Reviewer tier: standard. A screenshot re-record, if one is pinned, is an owner step named in
  the pull request.

### CM22: the live checks, as scripts

*Merged, and kept (D3170).*

- Acceptance: Checks 810 and 811 exist as one script each, registered where every check script
  is, each printing what it measures and exiting non-zero on a failed row (§8).
- Files: `scripts/live-checks/check810.sh` (new), `scripts/live-checks/check810_test.sh` (new),
  `scripts/live-checks/check811.sh` (new), `scripts/live-checks/check811_test.sh` (new),
  `scripts/live_checks_test.go`, `docs/gcp-live-checklist.md`.
- Interfaces: none.
- Tests: `TestLiveCheckScripts` (the registry of scripts), and the documents test that reads the
  checklist.
- Command: `go test ./scripts/ -run LiveCheckScripts -count=1 && go test ./internal/cli/ -run Docs -count=1`.
- Red step: the registry lists no script for Check 810.
- Depends on: none. Not launched before the keys plan's KS17 and always a budget's AB14 have merged
  (all three edit `scripts/live_checks_test.go` and the checklist).
- Commit: `checks: 810 and 811, the auth retirement, as scripts (CM22)`.
- Reviewer tier: opus, mandatory independent review (a script the owner runs against a live
  project).
- Owner-run: the scripts themselves, after each release (§8). No agent runs one.

### CM32: the cascade's pool is served as the cascade

*Merged, and kept (D3170).*

- Acceptance: a pool in the installation's config may carry a marker that says it is the
  cascade's (D3064 as amended); the marker never reaches the shared node, where the pool is
  published as an ordinary `legacy_open` pool, the publisher's rule that clears `legacy_open` and
  asks for an owner excepting it (F36, R15); the marker is refused, where the config is
  validated, on a pool the migration did not write for a cascade that serves; an unmarked pool
  is validated and published as before. The service's half is CM36.
- Files: `internal/config/craft.go`, `internal/config/craft_cascade_pool_test.go` (new),
  `internal/cli/project_cmd.go` (`publishedCraftPools`), `internal/cli/published_pools_cascade_test.go` (new).
- Interfaces: produces the marker, read by CM4 and CM14.
- Tests: `TestTheMarkerIsRefusedWhereNoCascadeServes`, `TestTheCascadesPoolIsPublishedLegacyOpen`,
  `TestTheSharedNodeCarriesNoMarker`, `TestAnUnmarkedPoolIsPublishedAsBefore`.
- Command: `go test ./internal/config/ -run 'TheMarkerIsRefused|Pools' -count=1 && go test ./internal/cli/ -run 'TheCascadesPoolIsPublished|TheSharedNodeCarriesNoMarker|AnUnmarkedPoolIsPublished|PublishedCraftPools' -count=1`.
- Red step: a pool with the marker is published with `legacy_open` cleared and refused for want of an owner.
- Depends on: none.
- Commit: `config: the cascade's pool, marked and published (CM32, D3064)`.
- Reviewer tier: opus, mandatory independent review.
### CM34: the account and craft commands write the project's access (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's MG4.*

- Acceptance: the commands that wrote a retired account key write the access instead:
  `fugaro accounts switch` (the default account), `accounts alias`, `accounts rm`,
  `fugaro settings set` of `default_claude_account` or a stage's account, and the policy
  publish; `fugaro accounts grade set` and `accounts craft set` write the project's config, for
  one repository where `--repo` names it, and refuse to write an access into a repository's file
  with the fix (D3062, F38); each of them, after it changes what any repository may be served
  by, rewrites the access record of every enrolled repository it changes, from the project's
  config alone (R4, F34), and says which in one line; a coding agent's session is refused these
  writes as today.
- Files: `internal/cli/accounts_switch.go`, `internal/cli/accounts_alias.go`,
  `internal/cli/accounts_rm.go`, `internal/cli/accounts_craft.go`, `internal/cli/settings_set.go`,
  `internal/cli/init_policy_publish.go`, `internal/cli/accounts_access_test.go` (new). It is past
  five files because each command is a file.
- Interfaces: consumes CM2's block and CM5's record store.
- Tests: `TestAccountsSwitchWritesTheAccess`, `TestGradeSetWritesTheProjectNotTheFile`,
  `TestAProjectChangeRewritesEveryRepositorysRecord`, `TestTheseWritesAreRefusedInAnAgentSession`.
- Command: `go test ./internal/cli/ -run 'AccountsSwitchWrites|GradeSetWrites|AProjectChangeRewrites|TheseWritesAreRefused|AccountsCraft|SettingsSet' -count=1`.
- Red step: `accounts grade set` writes an access into the repository's `fugaro.yaml`.
- Depends on: CM2 and CM5 (kept: it writes the block and calls the store they add).
- Commit: `accounts: the commands write the project's access and its records (CM34, R4)`.
- Reviewer tier: opus, mandatory independent review.

### CM36: the service serves the cascade's pool as the cascade (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's CS1, PL3.*

- Acceptance: where the access record marks a pool as the cascade's, the service offers its
  subscription members whatever the spread switch says, admitted per repository exactly as the
  cascade admits them today, and what it offers one repository is what the cascade offered it
  (U4); a pool the record does not mark obeys the switch as before; a marker that reaches the
  service by any road but the record is ignored.
- Files: `internal/credsvc/pool.go`, `internal/credsvc/pool_cascade_test.go` (new).
- Interfaces: consumes C1 (the record's marker) and the record's read, which CM6 adds.
- Tests: `TestAMarkedPoolServesItsSubscriptionsWithSpreadOff`,
  `TestAMarkedPoolOffersWhatTheCascadeOffered`, `TestAnUnmarkedPoolStillObeysTheSwitch`,
  `TestAMarkerOutsideTheRecordIsIgnored`.
- Command: `go test ./internal/credsvc/ -run 'AMarkedPool|AnUnmarkedPool|AMarkerOutsideTheRecord|Pool' -count=1`.
- Red step: a marked pool's subscription member is skipped with the spread switch off.
- Depends on: C1; CM6 (kept: it calls the read of the record CM6 adds to the service's store).
- Commit: `credsvc: the cascade's pool is served as the cascade (CM36, D3064)`.
- Reviewer tier: opus, mandatory independent review, with a mutation check that an unmarked pool
  cannot be served a subscription with the switch off.

### CM23: the help texts and the CLI reference (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's MG6.*

- Acceptance: no command's help names `agent.auth` or a run by its auth (`budget preview`, `runs stats`, `secrets ls`, `setup`, `run --token`, `models use`, the
  `accounts` commands, the doctor's rendering and the managed-settings messages); it is the one task that
  rewords a help string, and it regenerates the reference page once, in the same pull request,
  as `TestCliReferenceIsCurrent` requires (F39).
- Files: `internal/cli/budget_preview.go`, `internal/cli/runs_stats.go`,
  `internal/cli/secrets.go`, `internal/cli/setup.go`, the help strings of `internal/cli/run.go`
  and `internal/cli/models_use.go`, `internal/cli/accounts.go`, `internal/cli/doctor_render.go`,
  `internal/cli/settings_managed.go`, `internal/cli/config_publish_managed.go`, `website/content/cli-reference.md` (generated by
  `go run ./ee/clidoc`), `internal/cli/help_access_test.go` (new). It is past five files because
  help lives in each command's file.
- Interfaces: none.
- Tests: `TestNoHelpTextNamesARepositorysAuth` (new, in `internal/cli/help_access_test.go`), and
  the plugin's tests that read commands and flags.
- Command: `go test ./internal/cli/ -run 'NoHelpTextNames|Docs' -count=1 && go test ./ee/clidoc/ -run CliReferenceIsCurrent -count=1 && go test ./plugin/ -count=1`.
- Red step: `fugaro run --help` says "whose agent.auth is oauth".
- Depends on: CM14 and CM17 (kept: one file each, `run.go` and `models_use.go`).
- Commit: `cli: no help text is phrased by a repository's auth (CM23)`.
- Reviewer tier: standard.

### I1: integration (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's I1.*

- Acceptance: the two stub files are deleted and the real resolver and accessor are wired in;
  what release 1's tasks left for it is removed: `EnvSpec.Auth`, the old default of
  `craft.Resolve` and `config.DefaultCredential` with it (nothing defaults to Vertex), C1's rule
  kept as the one reader of the retired keys, `infra.CredentialsServiceServes`, `config.CheckProviderAuth`,
  `config.CheckStageAccountMix` and its helpers, `config.ServiceServedAuths`,
  `runner.ServiceSwitchedAuth` and `GatewayOn`'s auth argument; nothing outside C1's rule (`internal/craft/legacy.go`),
  the migration that calls it and the retired-key notice reads `Agent.Auth`; an installation at the previous release, with
  one repository on each old auth value and one on the mix, converges through `fugaro update`
  alone, each run goes through its gateway where it is served, and a run in flight keeps
  running. It is past five files because it removes one function from each package that kept it.
- Files: `internal/craft/access_stub.go` (deleted), `internal/runner/access_stub.go` (deleted),
  `internal/config/retired_readers_guard_test.go` (new),
  `internal/cli/access_end_to_end_test.go` (new), `internal/e2e/update_test.go`, and the files
  that hold the functions named above: `internal/agent/env.go`, `internal/craft/resolve.go`,
  `internal/infra/spec.go`, `internal/config/providers.go`, `internal/config/stage_accounts.go`,
  `internal/runner/routing.go`, and their callers in `internal/cli/run.go`, `init.go` and
  `init_credentials.go`.
- Interfaces: consumes every release 1 code task.
- Tests: `TestNoReaderOfTheRetiredAuth`, `TestAnInstallationAtThePreviousReleaseConverges`,
  `TestEveryServedRunGoesThroughItsGateway`, `TestUpdateLeavesARunInFlightRunning`.
- Command: `go build ./... && go vet ./... && go test ./internal/config/ -run NoReaderOfTheRetiredAuth -count=1 && go test ./internal/cli/ -run 'AnInstallationAtThePreviousRelease|EveryServedRun|UpdateLeavesARunInFlightRunning' -count=1 && go test ./internal/e2e/ -run Update -count=1`.
- Red step: the guard test finds a reader.
- Depends on: CM1, CM2, CM4 to CM21 (CM20 and CM23 included, so that nothing is live on a file it
  edits; CM21 because the console's parser is held to the document the CLI now prints), CM23,
  CM32, CM34 and CM36.
- Commit: `access: the stubs go and no reader of the auth mode is left (I1)`.
- Reviewer tier: opus, mandatory independent review.

### CM24: `SECURITY.md`, its page and the rules file (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's DOC1.*

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

### CM25: the credentials documents and the checklist (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's DOC2.*

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
- Depends on: I1 (kept, as CM24); CM24 (kept: its tests also read `SECURITY.md`, and
  `docs_security_test.go` reads the runbook).
- Commit: `docs: credentials, crafts and pools, not auth modes (CM25)`.
- Reviewer tier: standard.

### CM26: the accounts documents, the README and the release notes (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's DOC3.*

- Acceptance: `docs/project-claude-accounts.md`, `docs/mixed-credentials/spec.md`,
  `docs/agent-env/spec.md`, `docs/project-layer.md` and `README.md` say the same; the release's
  notes have `### For operators`: run `fugaro update`, and what it will say (the access each
  craft resolves to, the retired keys offered as a diff, the repositories whose subscription is
  not yet served); the website's summary line is added.
- Files: `docs/project-claude-accounts.md`, `docs/mixed-credentials/spec.md`,
  `docs/agent-env/spec.md`, `docs/project-secrets/spec.md`, `docs/design/v1.md`,
  `docs/release.md`, `docs/project-layer.md`, `README.md`, the release's notes under
  `docs/releases/`, `website/data/release-summaries.yaml`, and the tests
  `docs_mixed_credentials_test.go`, `docs_usage_limit_switch_test.go`,
  `docs_project_named_oauth_tokens_test.go`, `docs_project_layer_test.go`,
  `docs_secrets_brokering_test.go`, `docs_release_notes_test.go` and
  `docs_cloud_senior_review_test.go`, with the sentences they pin. It is past five files
  for those pins.
- Interfaces: none.
- Tests: the four files' tests, rewritten where their sentence is.
- Command: `go test ./internal/cli/ -run Docs -count=1 && (cd website && npm run check)`.
- Red step: a rewritten test fails on the old sentence.
- Depends on: I1 (kept, as CM24); CM25 (kept: its tests pin `docs/multi-model.md` and the runbook,
  and CM25's pin `README.md`).
- Commit: `docs: accounts and stages are crafts' access (CM26)`.
- Reviewer tier: standard.

### CM27: the plugin's skills (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's DOC4.*

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
- Depends on: I1 (kept, as CM24); CM26 (kept: one test file with CM25, and the documents tasks run
  one after another).
- Commit: `plugin: setup declares credentials, not an auth mode (CM27)`.
- Reviewer tier: opus, mandatory independent review (a plugin skill).

### CM28: release 2: the token's old path goes (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's X1.*

- Acceptance: `BuildEnv` has no arm that puts a subscription token in the agent's environment
  and `OAuthStageEnv` is deleted; a launch whose craft resolves to a subscription the service
  does not serve is refused with the fix (a project owner runs `fugaro update`); a launch on a
  job image older than release 1 is refused with the command that rebuilds it; a local
  invocation is unchanged (D3073); `SECURITY.md`'s sentence about the token in the environment is
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
- Commit: `agent: no token in the agent's environment (CM28, D3067)`.
- Reviewer tier: opus, mandatory independent review.

### CM29: release 2: the retired keys of a file and a layer are rejected (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's X2.*

- Acceptance: `fugaro validate`, `fugaro update` and a launch from the default branch reject a
  repository's file that writes `agent.auth`, `agent.oauth_token`, a craft's `access` or a
  grade's `access`, with the fix, the removal `fugaro update` offers as a diff; a run of a ref
  whose file still writes one reads it as ignored and says so in its log (F46); a project layer
  that writes the key is rejected with the fix, `fugaro update`, which rewrites it (CM33); the
  launch and runner fallback to the legacy rule goes, a run with no handed entry being refused
  with the fix, and the rule itself stays for the migration, which reads the old keys before the
  file is validated (F45); the two schemas lose the keys and keep `version` 1 (D3072); CM28's
  image gate is what keeps an older image from a file without the key.
- Files: `internal/config/config.go`, `internal/config/validate.go`, `internal/config/layer.go`,
  `internal/config/scope.go`, `internal/config/retired.go`, `internal/runner/access_resolve.go`,
  `schemas/fugaro.schema.json`, `schemas/project-layer.schema.json`, `schemas/schemas_test.go`.
  It is past five files because the key is declared in that many.
- Interfaces: none new.
- Tests: `TestAFileThatWritesTheAuthIsRejectedWithTheFix`, `TestALayerThatWritesItIsRejected`,
  `TestARunOfAnOlderRefReadsTheKeyAsIgnored`, `TestARepositoryFirstUpdatedNowIsStillMigrated`,
  `TestTheSchemasNoLongerListTheKey`, `TestUpdateOffersTheRemovalFirst`.
- Command: `go test ./internal/config/ -run 'WritesTheAuthIsRejected|ALayerThatWritesIt|Retired' -count=1 && go test ./schemas/ -count=1 && go test ./internal/runner/ -run ARunOfAnOlderRef -count=1 && go test ./internal/cli/ -run 'UpdateOffersTheRemovalFirst|ARepositoryFirstUpdatedNow' -count=1`.
- Red step: a file that writes `agent.auth` validates.
- Depends on: I1; CM28 (kept, behaviour: its gate must exist before a file may lose the key).
  **Not launched before release 1 is cut.**
- Owner-run: before this task merges, the removal `fugaro update` offers for this repository's
  own `fugaro.yaml`, which writes `auth: api-key`, is committed by the owner; the task's pull
  request says so and no agent commits it.
- Commit: `config: agent.auth is rejected, with the fix (CM29, D3063, D3072)`.
- Reviewer tier: standard.

### CM30: release 2: the installation's account keys and the node's members (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's X3.*

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
- Commit: `localcfg: the converted account keys go (CM30, D3068)`.
- Reviewer tier: opus, mandatory independent review.

### CM31: release 2: the old record stops being written (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's X4.*

- Acceptance: `fugaro update` no longer writes `creds/repoauth/<slug>`; it deletes a
  repository's old record only where that repository has no run in flight, and otherwise leaves
  it and says so in one line; the service still narrows a call that lacks the new header by an old record that exists, and
  refuses it where none does (F42); Check 193's script reads the access record; a new runner never sends the old
  header. The old header's parser stays for one more release (§10).
- Files: `internal/cli/update_repoauth.go`, `internal/cli/update_repoaccess_test.go`,
  `internal/cli/update_in_flight.go`, `internal/gateway/service.go`,
  `internal/gateway/gateway.go`, `internal/credsvc/cascade.go`,
  `scripts/live-checks/check193.sh` and `check191.sh` with their `_test.sh`. It is past five
  files for the two scripts.
- Interfaces: none new.
- Tests: `TestUpdateWritesOnlyTheAccessRecord`, `TestAnOldRecordIsKeptWhileARunIsInFlight`,
  `TestANewRunnerSendsNoAuthHeader`, `TestTheAccessRefusalWritesOneAuditLine`.
- Command: `go test ./internal/cli/ -run 'UpdateWritesOnlyTheAccessRecord|AnOldRecordIsKept|RepoAuth' -count=1 && go test ./internal/gateway/ -run ANewRunnerSendsNoAuthHeader -count=1 && go test ./internal/credsvc/ -run 'Cascade|OneAuditLine' -count=1`.
- Red step: `update` writes both records.
- Depends on: I1; CM28 (kept: one file, `update_in_flight.go`, where its one line is counted).
  **Not launched before release 1 is cut.**
- Commit: `update: the auth record is no longer written (CM31, R4)`.
- Reviewer tier: opus, mandatory independent review.

### CM33: release 2: `fugaro update` rewrites the published layer (withdrawn 2026-11-22 by D3170)

*Withdrawn 2026-11-22 by D3170; carried by model credentials routing's X5.*

- Acceptance: where every job image of the project is at or after release 1, `fugaro update`
  rewrites the published project layer in place without `defaults.agent.auth`, by the path
  `fugaro config publish` writes it, and says so in one line; elsewhere it leaves the layer and
  names the images; the local layer file is rewritten the same way; it asks nothing and a failed
  write warns and fails no update (R14).
- Files: `internal/cli/config_publish.go`, `internal/cli/update_layer_auth.go` (new),
  `internal/cli/update_layer_auth_test.go` (new), `internal/cli/init.go`.
- Interfaces: none new.
- Tests: `TestUpdateRewritesTheLayerWithoutTheKey`, `TestTheLayerIsLeftForAnOlderImage`,
  `TestTheLayerRewriteIsSilentTheSecondTime`.
- Command: `go test ./internal/cli/ -run 'UpdateRewritesTheLayer|TheLayerIsLeftForAnOlderImage|TheLayerRewriteIsSilent|ConfigPublish' -count=1`.
- Red step: after `update`, the published layer still writes the key.
- Depends on: I1. **Not launched before release 1 is cut.**
- Commit: `update: the published layer loses agent.auth (CM33, R14)`.
- Reviewer tier: opus, mandatory independent review (a write to a published object).

## 5. Parallel execution

The decoupling pass is on (`planning.decoupling` is `true` for this repository), and the
repository's mode is `startup`, with no area rules, so the pass decouples wherever an interface
is all the dependent needs.

**Levels.** Tasks in one level run at once. **A launcher follows this table**, and the waits in
its last column (R9, D3071). **From 2026-11-22 (D3170) it is the record of what was planned and
not a launch order:** every task but CM1, CM3, CM22, CM32 and CM35 is withdrawn, the owner stops
the withdrawn tasks by hand, and model credentials routing carries the work.

| Level | Tasks | Waits outside this plan |
| --- | --- | --- |
| 0 | CM35, CM1, CM3, CM22, CM32 | CM35: none; it launches first and alone (F50). Every other task of this plan: always a budget's I1 and the keys plan's C1, KS1 to KS5, KS12 and KS19 have merged (D3071). CM22: KS17 and AB14 |
| 1 | C1 | none |
| 2 | CM2, CM6, CM7, CM9, CM10, CM11, CM13, CM16, CM21 | CM9: AB1b. CM13: AB15 |
| 3 | CM4, CM5, CM8, CM12, CM14, CM15, CM17, CM20, CM36 | CM8: AB1b |
| 4 | CM18, CM19, CM23, CM34 | none |
| 5 | I1 | none |
| 6 | CM24 | none |
| 7 | CM25 | none |
| 8 | CM26 | none |
| 9 | CM27 | none |
| 10 | CM28, CM33 | release 1 is cut; CM28 also the keys plan's KS13, which by CM35 waits on this plan's I1 |
| 11 | CM29, CM31 | none |
| 12 | CM30 | none |

**What must be sequential, and why.** CM35 before C1 (the keys plan must say what the contract
replaces). C1 before every task that names something of it. CM1, CM2 and CM32 before CM4 (the
migration writes the function's answer into the block, with the marker). CM4 before CM19 (the
notice prints the migration's answer). Every other task of release 1 before I1, so that nothing is live on a file it edits. The four documents tasks after
I1 and one after another (a document must be true of the tree, and their tests read each other's
documents). Release 2 after release 1 is cut (an image of release 1 must exist before a path it
replaces is removed), and inside it CM28's gate before CM29's rejection.

**Edges broken by C1** (each dependent needs a name or a signature, not the other side's code):

| Dependent | Was waiting on | What it needed |
| --- | --- | --- |
| CM2 | CM1 | the shape of a layer the resolver reads, not the function |

| CM6, CM36 | CM5 | the record's shape, not its writer |
| CM7 | CM6 | the header's name, not the service's narrowing |
| CM8 | CM1 | the resolver's interface, not its rule |
| CM9, CM11 | CM1 | the resolved credential's type, not the resolver's rule |
| CM10, CM12 | CM8 | the run's accessor, not the runner's wiring |
| CM13, CM16 | CM1 | the resolver's interface |
| CM14 | CM8 | the fact's name and the header, not the runner |
| CM21 | CM12 | the registry member's name |

CM1, CM3 and CM32 need nothing of the contract: CM1 is a pure function over the config's own
types, CM3 owns the retired list and CM32 its own marker, so they start before it. CM4, CM5, CM15, CM17 and CM34 reach the contract through a task they already wait on.

**Edges kept**, each with its reason, are on the tasks' "Depends on" lines. The ones a reader
might expect broken: CM1 to CM4 (a stub's answer written into a config is a wrong config on
disk); CM13 to CM14 and CM15, and CM16 to CM17 (a stub on a path that decides a credential or a
refusal would launch or admit wrongly); CM7 and CM9 to CM8, and CM2 and CM32 to CM4 (members of
shared structs, which a contract cannot own without owning the file).

**Shared files.** No level holds two writers of one file:

| File | Writers, by level |
| --- | --- |
| `internal/config/craft.go` | CM32 (0) only |
| `internal/cli/project_cmd.go` | CM32 (0), CM2 (2), CM30 (12) |
| `internal/config/scope.go`, `retired.go` | CM3 (0), CM29 (11) |
| `docs/gcp-live-checklist.md` | CM22 (0), CM25 (7) |
| the two stub files | C1 (1), I1 (5) |
| `internal/craft/legacy.go` | C1 (1) only; it stays |
| `internal/localcfg/localcfg.go` | CM2 (2), CM15 (3), CM30 (12) |
| `internal/budget/rules/rules.json.tmpl` | CM2 (2), CM12 (3) |
| `internal/credsvc/cascade.go` | CM6 (2), CM31 (11) |
| `internal/credsvc/pool.go` | CM36 (3) only |
| `internal/gateway/gateway.go`, `service.go` | CM7 (2), CM31 (11) |
| `internal/runner/accountchoice.go` | CM11 (2), CM8 (3) |
| `internal/agent/env.go`, `internal/runner/agentenv.go` | CM9 (2), I1 (5), CM28 (10) |
| `internal/infra/spec.go` | CM13 (2), CM15 (3), I1 (5) |
| `internal/config/providers.go`, `stage_accounts.go` | CM16 (2), I1 (5) |
| `internal/config/validate.go` | CM16 (2), CM29 (11) |
| `internal/cli/update_repoauth.go` | CM5 (3), CM31 (11) |
| `internal/runner/access_resolve.go` | CM8 (3), CM29 (11) |
| `internal/runner/routing.go` | CM8 (3), I1 (5) |
| `internal/cli/init.go` | CM4 (3), CM19 (4), I1 (5), CM33 (10) |
| `internal/cli/run.go` | CM14 (3), CM23 (4), I1 (5) |
| `internal/cli/init_credentials.go` | CM15 (3), I1 (5) |
| `internal/cli/models_use.go` | CM17 (3), CM23 (4) |
| `internal/cli/validate.go` | CM17 (3) only; CM18 (4) reads it |
| `internal/cli/launch_budget.go`, `recipes_skew.go` | CM15 (3), CM28 (10) |
| `internal/cli/init_access_migrate.go` | CM4 (3), CM30 (12) |
| `internal/settings/registry.go`, the schemas, `schemas/schemas_test.go` | CM20 (3), CM29 (11), CM30 (12) |
| `internal/cli/settings_set.go`, the `accounts_*.go` files, `init_policy_publish.go` | CM34 (4) only |
| `internal/cli/update_in_flight.go` and its document | CM19 (4), CM28 (10), CM31 (11) |
| `website/content/cli-reference.md` | CM23 (4) only |
| `SECURITY.md`, its page, `docs_security_test.go` | CM24 (6), CM28 (10) |
| the documents tests under `internal/cli/docs_*_test.go` | CM24 (6), CM25 (7), CM26 (8), CM27 (9), one level each |
| `docs/keys-through-service/*` | CM35 (0) only |

**The graph read.** `fugaro plan graph --plan docs/credentials-belong-to-models/implementation-plan.md`
reports:

`critical path (10 tasks): CM35 -> C1 -> CM13 -> CM14 -> CM23 -> I1 -> CM24 -> CM25 -> CM26 -> CM27`,
with CM35 the bottleneck, and `gain: 13 broken, 32 kept, 3 added; before 12 tasks, weight 25; after 10 tasks, weight 18.5; improves`.
The graph's levels 0 to 9 are the table's. It cannot say "after release 1 is cut", so it places
CM28, CM29, CM30, CM31 and CM33 from level 6 on; the table's levels 10 to 12 are the order to
launch them in.

## 6. The estimate

As the same command printed it:

`ready in 455–1820 min, no history yet for 39 tasks (39 tasks; seeds for 39; from the seeds: no history is cached on this machine yet)`

It is informational and reserves no budget: it is what the tasks' tiers and this machine's
history predict, and it moves as runs finish. It does not count the waits on the two plans
before this one, the release between levels 9 and 10, or the owner's runs of Checks 810 and 811.
The graph counts one node more than this plan's 38 runs: the contract change of §7, which runs
only if raised.

## 7. Contract changes anticipated

- **R-pool-members** (C1, raised by CM4): if the migration shows the access record must name the
  cascade's pool's members one by one for the service to narrow by it, the record gains them. Reaches CM5 and CM6; a launched one is reached by a follow-up run on its pull
  request that names `internal/creds/repoaccess.go` by path. **Withdrawn 2026-11-22 (D3170):**
  its contract C1 is withdrawn and CM4, CM5 and CM6 with it, so it is never raised.

## 8. Checks the plan takes

From the reserved block 810–819; both owner-run, by one script each (CM22). No agent runs a step.
**Both are replaced, 2026-11-22 (D3170):** model credentials routing takes Checks 884 and 885, and
its DOC2 marks 810 and 811 so in the live checklist. CM22's scripts stay merged and unrun.

- **Check 810, after release 1.** On an installation at the previous release with a repository
  on a subscription, one on an Anthropic key, one on the cascade and one on Vertex, and a run in
  flight: `fugaro update` migrates all four and says what each craft resolves to; the run in
  flight finishes; a new run of each launches, goes through its gateway and is recorded with the
  same `model_basis` as before; the service's audit log holds one line per refusal provoked by a
  hand-built call that declares a craft its record does not name (U5, O1).
- **Check 811, after release 2.** A run on a subscription has no token in its agent's
  environment; a launch on an image older than release 1 is refused with the rebuild command; a
  file that still writes `agent.auth` is rejected with the fix; an old record is kept while a
  run is in flight and gone after.

## 9. After-deploy steps

Each release's `### For operators` is "run `fugaro update`", plus what it will say:

- **Release 1** (CM26 writes it): the access each craft of each repository resolves to; each
  retired key a file still writes, offered as a diff to commit once that repository's job images
  are rebuilt; each repository whose
  subscription is not yet served by the credentials service and that a project owner's
  `fugaro update` makes it served. Then the owner runs Check 810.
- **Release 2** (the release's own notes, written by CM28): a repository whose file still writes
  a retired key is rejected until the offered removal is committed; a job image older than
  release 1 must be rebuilt, which `update` offers behind its own confirmation, as today; the
  published layer is rewritten by `update` itself. Then
  the owner runs Check 811.

No step is another command, a deletion by hand or a repair.

## 10. Considered and declined

- **Vertex as the last resort of an unset access, and every reaching credential in order**
  (D3061's first answer): the owner amended it, because a credential nobody named would bill
  the project. The name `native` for Vertex was also declined: `vertex` is the built-in
  credential's name everywhere already.
- **A plain pool for the cascade** (D3064's first answer): with the spread switch off it would
  serve no subscription (F24).
- **A per-model list of credentials, and a fixed order by kind** (D3060's other options): a
  second config surface beside crafts and pools; or spending nobody chose.
- **Keeping `agent.crafts.<craft>.access` in a repository's file** (D3062): the one tie between
  a file and credentials the owner's two rulings reject.
- **Rejecting the key at once, and `fugaro.yaml` version 2** (D3063, D3072): the first stops
  every repository until a commit and sends an older image to Vertex; the second has every older
  CLI and image refuse every repository.
- **The service calling Vertex** (D3066): a project-wide Vertex grant on the service's account,
  for no key moved.
- **A gateway that holds the mounted token** (D3067): the token would stay mounted in the job,
  which the keys plan removes.
- **Removing the old header's parser and deleting every old record in release 2**: a run in
  flight on an image older than release 1 would lose the record its calls are narrowed by. The
  parser and the last records go in the release after, as a follow-up task with no decision in
  it.
- **Changing a Terraform variable** (R12): `vertex`, `model_secrets_served` and `stage_account`
  keep their meaning, and the keys plan's KS14 already removes the model mounts.
- **Renaming `--token`, and making the cascade's settings a pool's own** (O5, O6): later
  changes with their own readers.
