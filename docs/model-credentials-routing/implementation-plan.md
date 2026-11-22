# Model credentials routing: implementation plan

This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md).

**Status: plan only. Nothing is implemented. No task is approved: each needs the owner's
explicit go, by name.** An answer in the reconciliation settles a question and approves nothing.
No run was launched to write this plan and none launches from it.

The answers that shaped it, all the owner's of 2026-11-21: the table supersedes craft access now
and the earlier plan's not-started tasks are re-cut here (D3170); one chain per model (D3171);
the most specific pattern wins (D3172); the table replaces `routing.claude.via` (D3173); Vertex
is a source the credentials service itself calls, capped as far as a key is (D3174, **amended
after review, twice**); the service rewrites the model's spelling on a move (D3175); `--token` becomes a narrow `--pool` (D3176, amended by the owner the same day); `subscription_spread` is retired (D3177); the threshold rule waits on Check 880
(D3178); personal pools live in the project's per-user containers (D3179, **amended after review**); a launcher with no pool is served
by the next source (D3180); a service account's pool is a project pool an admin fills (D3181);
per-repository admission stays, for every source kind (D3182); existing accounts become the
shared pool (D3183); a short in-job wait is kept under a floor, **not the recommendation**
(D3184); a resumed job is a new run in the same chain (D3185); the resident orchestrator resumes
(D3186); the maximum wait defaults to 6 hours (D3187); the console shows the source per step, a
fallback badge, the switch log and a pools screen (D3188); the issuer may serve the orchestrator
a resume of a pause the credentials service recorded (D3189); a chain's pauses are bounded by
total paused time, **not the recommendation**, with a minimum pause added after review (D3190);
a resume of a person's run draws on that person's pool (D3191); a strict chain pays at once and
`update` says so (D3192); Gemini on Vertex is added, **not the recommendation** (D3193); a member may contribute their own
token to a marked shared pool (D3194); a pull request whose chain is paused is in a fifth state,
`paused`, **not the recommendation** (D3195); a configuration the checks would refuse stays on
the release-1 bridge (D3196); only the launcher cancels a pause (D3197).

The contract-first pass is on for this repository (`planning.decoupling` is `true`), and the
repository's rigor is `startup` with no area rules, so the pass decouples aggressively; the three
contracts are over credentials and wire formats and are reviewed at the strictest tier whatever
the mode.

## Conventions every part follows

The repository's own rules apply and are not restated: `CLAUDE.md`, and the rules files the
touched paths load (`.claude/rules/credentials-service.md`, `terraform.md`, `console.md`,
`mobile.md`, `website.md`, `ee.md`). What this plan's areas trigger:

- **Always.** Targeted tests, never a whole package (`CLAUDE.md`, "Run targeted tests").
  `go build ./... && gofmt -l . && go vet ./...` before a commit. Hermetic tests only
  (`internal/gcpfake`, `internal/testutil`, `t.TempDir()`). Config and wire formats decode
  strictly and have a schema. A member is added, never retyped or reused (`CLAUDE.md`, "A field
  never changes meaning"). `fugaro update` is the whole upgrade and is safe with runs in flight:
  every task that changes a policy, rules or record shape ships its migration inside `update`
  with a test that the previous version converges. A task's pull request names the task id and
  the `D#` it implements and does not touch `docs/status.md`.
- **One at a time.** `website/content/cli-reference.md` is generated and pinned byte for byte:
  a task that adds a command or a flag, or edits a help string, regenerates it, and is not live
  beside another such task (the plan orders the ones it knows; F50).
- **Ask first.** A new dependency or framework; any confirmation step (the no-prompt policy
  forbids one, `docs/no-prompt-policy/`); a change to a contract's file (raise a contract change,
  below); anything a finding of this plan does not cover.
- **Never.** A secret-shaped literal in a test, a fixture, a log or a document. A credential
  value in any log or audit line. A live cloud action by an agent: every live check is the
  owner's. An edit under `.github/workflows/`. A question asked of the user by a command. A
  reader that trusts `requested_by` or `launched_by` as an identity (R7). A text-only coder
  reading an image file (the console tasks check screenshots by name and size).

## Global constraints and review focus

Inputs and failure modes the spec implies and no single task's happy path tests. Each is pinned
by the task named.

| Constraint | Pinned by |
| --- | --- |
| The launcher is the verified principal on the run's slip; a forged requester gets nothing (R7) | PL4 (`TestAForgedRequesterGetsNothing`), CS14 (`TestANamedPoolIsGrantedOnlyToItsOwner`) |
| Only the credentials service writes a pause, its resume time and its launcher; a `/pause` nobody answered is refused | PR3 (`TestThePausedLauncherIsTheServicesRecord`, `TestAPauseNobodyAnsweredIsRefused`) |
| Only the orchestrator's class may resume, for the same repository and chain, once, and not before its time | PR3 (`TestOnlyTheOrchestratorMayResume`, `TestAResumeIsSingleUse`, `TestNoResumeBeforeItsTime`), PR4 |
| Only its launcher or a project admin cancels a pause | PR10 |
| A designation and a personal pool's membership are written only through the service's route, keyed on the verified subject (R26) | PL4 (`TestDesignationRouteRefusesAnothersPool`), PL9 |
| The model a call is routed by is the one in its body; a source the grant does not cover is never served | CS1 |
| A personal pool serves only its owner's runs, including after a pause and a reissue | PL4, PL6, PR3, I2 |
| `--pool` picks only among pools the launcher owns or is allowed on, and never changes a chain | PL4 (`TestANamedPoolMustBeTheLaunchers`), CS14, CS12 |
| A service account's run never draws on a person's pool (D2976) | PL6 |
| The agent never sees a credential, a source or a switch | CS2, CS7, I1 |
| The in-job gateway holds no key and no table; a job's account cannot reach Vertex on an `enforce` project in release 1, or anywhere after release 2 | CS2, CS11 (`TestNoJobVertexGrantUnderEnforce`), X8, Check 882 |
| No money is spent that no chain names: a model with no entry is refused, Vertex only where named | RT3, CS10, I1 |
| A paid fallback the gateway sees is held to the caps, Vertex included, and priced by the model that served; a hand-built call straight to the service is the accepted residual risk (R23) | CS4, CS8 (`TestTheLedgerPricesTheModelThatServed`), I1, DOC1 |
| Nothing moves on upgrade but what `update` names first: a pool that starts rotating (D3177) and a project that now pays at once (D3192) | MG1 (`TestMigrateMovesNoRepository`), PL3, MG2, Check 884 |
| A release-behind reader still decodes the published policy, a release-behind gateway meets today's answer, and the rules still converge | RT4, PR9, X3, CS1 (`TestAReleaseBehindGatewayMeetsTheOldAnswer`), MG3, CS9, PR1 |
| A chain's total paused time is bounded and no pause is shorter than the minimum (D3190) | PR3 (`TestTotalPausedTimeIsBounded`, `TestAPauseIsNeverShorterThanTheMinimum`) |
| The agent never sees another model's spelling, and an untranslatable Gemini member is refused, never dropped | CS3, GV1, GV2 |
| Every refusal writes one audit line and no value | CS1, CS3, CS4, CS14, PL4, PL5, PL9, PR3, PR10, GV1 |
| A run in flight across the update keeps working, on either release | MG3, PR6 |
| A paused run is resumed at most once, never after its launcher cancelled it, and never while its run has not ended `paused` | PR3 (`TestNoResumeOfACancelledPause`), PR4 (`TestARunThatDidNotEndPausedIsNotResumed`), PR10 |
| A paused chain is in flight to every guard, and its pull request reads `paused` (D3195) | PR12, PR13 |
| A run with no slip is served no launcher's pool | CS1, PL4 (`TestARunWithNoSlipHasNoLaunchersPool`) |
| A pause the current image cannot read ends with a named outcome | PR5 |
| No proxy call is held open; no command blocks on a paused run | PR2, PR7 |
| A limit found inside a streamed answer is not retried; the next call moves on (U7) | CS1 |
| An older console and an older CLI still parse what a newer run writes, or the owner decides a version bump (O8) | CS9, VS3 |
| No command asks a question | RT5, PL2, PL5, PL9, CS12, MG2, MG4, PR10 |

## What becomes of the earlier plan (D3170)

`docs/credentials-belong-to-models` keeps what is merged and its two-release shape. Every task
of it that is not merged is withdrawn and carried here, except CM2 (per-repository crafts), which
is not replaced: one table per project, and RT2 carries admission. A1 writes this table into that
plan's own documents. **The owner stops the withdrawn tasks by hand before any launches (O1)**; C1's
draft pull request #3460 is closed unmerged.

| Earlier task | State | Here |
| --- | --- | --- |
| CM35, CM1, CM3, CM22, CM32 | merged | Kept. `craft.ByReach` is what RT3 calls for reach; `config.RetiredKeyNotes` gains keys in MG5; the pool marker `cascade` is read until X3 |
| C1 (the access contract) | draft #3460 | Withdrawn; C1 here |
| CM2 (per-repository crafts) | not started | Withdrawn, not replaced: one table per project; RT2 carries admission |
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
| CM28, CM29, CM30, CM33 | not started | X1, X2, X3, X5; X6 to X8 are new |
| Checks 810 and 811 | scripts merged, not run | Replaced by Checks 884 and 885; DOC2 marks them so in the live checklist |

The keys plan's KS13 waits on that plan's I1; A1 re-points it at this plan's I1.

## Slice 0: the documents the other plans need first

### A1: credentials-belong-to-models says what this plan replaces

- Module: `migration`.
- Acceptance: That plan's README, reconciliation and plan say, task by task, what D3170 withdraws and which task here carries it (the table in this plan's "What becomes of the earlier plan"), that D3060, D3064, D3066, D3068 and D3069 are amended or reversed by D3170, D3183, D3174, D3171 and D3176, and that contract C1 as drafted in #3460 is withdrawn; its graph marks the withdrawn nodes; the keys plan's README says KS13 now waits on this plan's I1; the Docs tests pass.
- Files: `docs/credentials-belong-to-models/README.md`, `docs/credentials-belong-to-models/reconciliation.md`, `docs/credentials-belong-to-models/implementation-plan.md`, `docs/credentials-belong-to-models/implementation-plan.graph.json`, `docs/keys-through-service/README.md`.
- Interfaces: Consumes this plan's reconciliation. Produces nothing a task calls.
- Tests: The Docs tests that pin that folder; `fugaro plan graph --plan docs/credentials-belong-to-models/implementation-plan.md` reads the graph.
- Command: `go test ./internal/cli/ -run 'Docs' -count=1`
- Red step: None: a documents task. The check is that `git grep -n 'D3170' docs/credentials-belong-to-models` finds the amendment in all three documents.
- Depends on: none.
- Commit: `model-credentials-routing A1: credentials-belong-to-models says what this plan replaces`
- Reviewer tier: standard.

### A2: the per-user token documents say what now serves them

- Module: `pools`.
- Acceptance: The dynamic-delivery reconciliation of secrets-brokering says its R7 and R11 stand (a per-user token is owner-only and never lent), that a per-user token now serves a model call through its owner's personal pool (this plan's PL4, where workspace-credentials F4 recorded that nothing did), and that contributing to a shared pool is the holder's own act (R9), for which the intake takes a member's write under a shared container of a pool marked as taking contributions, reversing D1935 by that one case (D3194). Workspace-credentials' README and reconciliation say that plan is unchanged: a person's token at the workspace still follows the route (D3141), a project with a routing table serves from its own service, and D2976 is amended by one case in a project (D3191: a run that resumes a person's paused run draws on that person's pool). The Docs tests pass.
- Files: `docs/secrets-brokering/dynamic-delivery/reconciliation.md`, `docs/workspace-credentials/README.md`, `docs/workspace-credentials/reconciliation.md`.
- Interfaces: Consumes this plan's reconciliation. Produces nothing a task calls.
- Tests: The Docs tests that pin that folder.
- Command: `go test ./internal/cli/ -run 'Docs' -count=1`
- Red step: None: a documents task. `git grep -n 'D3191' docs/workspace-credentials docs/secrets-brokering` finds the note.
- Depends on: none.
- Commit: `model-credentials-routing A2: the per-user token documents say what now serves them`
- Reviewer tier: standard.

### A3: the credentials-service, model-crafts and mixed-credentials documents are amended

- Module: `chain-serving`.
- Acceptance: D138 (replaced beyond the floor by D3184), C3.1 (un-deferred as this plan's pause-resume), D152 and D153 (re-keyed by R4 and R17), D154 (amended by R16), D1622 and D1636 (reversed by D3177) and D1637 (amended by D3175) each carry a dated amendment that names this plan, and the mixed-credentials plan, where D1a is written, says it governs a provider's `models` and not the routing table; launcher-does-not-mint's reconciliation carries D3136 amended by D3189 (the issuer also serves the orchestrator a resume of a pause the credentials service recorded) and D3066 of credentials-belong-to-models is noted there as amended by D3174; always-a-budget's README notes that its Vertex refusal under `enforce` is lifted for a chain the service serves (D3174); the Docs tests pass.
- Files: `docs/credentials-service/reconciliation.md`, `docs/credentials-service/implementation-plan.md`, `docs/model-crafts/reconciliation.md`, `docs/mixed-credentials/implementation-plan.md`, `docs/launcher-does-not-mint/reconciliation.md`, `docs/always-a-budget/README.md`.
- Interfaces: Consumes this plan's reconciliation. Produces nothing a task calls.
- Tests: The Docs tests that pin those folders.
- Command: `go test ./internal/cli/ -run 'Docs' -count=1`
- Red step: None: a documents task. `git grep -n 'model-credentials-routing' docs/credentials-service docs/model-crafts docs/mixed-credentials` finds each amendment.
- Depends on: none.
- Commit: `model-credentials-routing A3: the credentials-service, model-crafts and mixed-credentials documents are amended`
- Reviewer tier: standard.

## Slice 1: the three contracts

### C1: the table contract

- Module: `routing-table`.
- Acceptance: A package `internal/routing` holds the types both sides call and nothing that computes: `Table`, `Entry{Match, Chain}`, `Source{Kind, Pool, Provider, Model}` with the kinds `launcher-pool`, `pool`, `anthropic`, `provider`, `vertex`, the admission of a key, a provider key and Vertex per repository (a subscription account keeps its own `allow_data_to`), and the signatures `Resolve(table, model) (Entry, error)` and `pricing.SpellingFor(kind, model) (string, bool)`; `internal/credsvc/chain_wire.go` holds the stage header, the answer's members (`source`, `fallback`, and `model`, the canonical id of the model that served, which the gateway prices by), the rule that these members and any new refusal member are sent only to a caller that sent the stage header (a release-behind gateway decodes strictly and would pass an unknown header to the agent), the rule that the service reads the model from the request's body and never from a header, and the interface the release-1 bridge implements; `internal/gateway/chain_names.go` holds the gateway's option that says the service serves this run and the signature of the hook a step's record reads; `contract-table.md` says what each side may rely on, including the run record's new members (`stages[].source`, `stages[].fallback`, `cost.fallback_usd`), the registry entry's `source` and `fallback`, the `watch --json` member that carries the source, the one new top-level policy key `model_routing` that carries the table, the admissions and the pools in their new shape (a release-behind reader tolerates an unknown top-level key and refuses an unknown member of a block it knows), and that every refusal writes one audit line; the stubs return the shapes and do nothing else; the tests pin the JSON of every shape.
- Files: `internal/routing/table.go` (new), `internal/routing/table_test.go` (new), `internal/routing/resolve_stub.go` (new, stub), `internal/pricing/spelling_stub.go` (new, stub), `internal/credsvc/chain_wire.go` (new), `internal/credsvc/chain_stub.go` (new, stub), `internal/gateway/chain_names.go` (new), `docs/model-credentials-routing/contract-table.md` (new).
- Interfaces: Consumes nothing. Produces the types, the wire names and the contract document every task of `routing-table`, `chain-serving`, `migration` and VS1 builds against.
- Tests: `TestTableShapesAreStable`, `TestChainWireNames`.
- Command: `go test ./internal/routing/ -run 'TestTableShapesAreStable' -count=1` and `go test ./internal/credsvc/ -run 'TestChainWireNames' -count=1`
- Red step: `TestTableShapesAreStable` fails to compile before the package exists.
- Depends on: A1 (kept: the earlier plan must say its own C1 (#3460) is withdrawn before a side is written against this one).
- Commit: `model-credentials-routing C1: the table contract`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### C2: the pools contract

- Module: `pools`.
- Acceptance: `internal/routing/pools.go` holds a pool's new members as types (`Rule` with `ordered`, `round-robin`, `failover`; `Principal` with a kind (person, service account, project) and an id, a person's being the Google subject; the optional `Threshold`), the designation record (principal, project, pool), the interface `LauncherPool(ctx, principal, named) (PoolRef, bool, error)` the service calls, where `named` is the pool a run asked for with `--pool` or empty (D3176), and the signature `creds.PickInPool`; `internal/creds/slip_chain.go` holds the grant's new members under new names (the sources the grant covers and the named pool; `Slip.Pool` keeps its meaning and is not reused); `contract-pools.md` says that a personal pool's tokens live in the project's per-user containers (D3179 as amended), that the principal is the verified one on the run's slip (R7), that an installation is migrated exactly when its published policy carries `model_routing` (the marker PL3 reads), that `owner` keeps its meaning and the principal is a new member, and the JSON of `fugaro accounts pool ls --json` with state, next reset, usage and headroom and never a value (every launcher of the project can read these figures and the designations in the database, so they are display data and never an authorization input); the stubs answer "no pool designated" and the first member.
- Files: `internal/routing/pools.go` (new), `internal/routing/pools_test.go` (new), `internal/creds/slip_chain.go` (new), `internal/credsvc/launcherpool_stub.go` (new, stub), `internal/creds/poolrule_stub.go` (new, stub), `docs/model-credentials-routing/contract-pools.md` (new).
- Interfaces: Consumes nothing. Produces the pool types, the designation record and the lookup interface for `pools`, CS1, MG1 and VS2.
- Tests: `TestPoolShapesAreStable`.
- Command: `go test ./internal/routing/ -run 'TestPoolShapesAreStable' -count=1`
- Red step: `TestPoolShapesAreStable` fails to compile before the file exists.
- Depends on: A2 (kept: the per-user token's documents must say a model call may now be served from one before a side is written against the contract).
- Commit: `model-credentials-routing C2: the pools contract`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### C3: the pause contract

- Module: `pause-resume`.
- Acceptance: `internal/runstore/pause.go` holds: the record a pausing job writes for the run that resumes it and for nobody else (`pause.json`: the stage, the session's name, the checkpoint commit, a format version); the node only the credentials service writes (`paused/<slug>/<run>`: the launcher, the pool the launch named, the resume time, the chain id, the chain's paused seconds so far, a cancelled mark); the members `fugaro watch --json` and `runs ls --json` gain for a paused run (the resume time and the run that resumed it); the constant for the status word `paused`; the task's new member `resumes`; the members the service's exhausted answer gains on the wire (`action`: `wait`, `pause` or `halt`, and `resume_at`), sent only to a caller that sent the stage header (R24), and the request a run makes once it has saved (`/pause`); `contract-pause.md` says who writes each, that the resume time and the launcher are the service's own and never the job's (R8, D3189), who may ask for a slip that names `resumes` (the orchestrator's class only, same repository and chain, once, not before the resume time), who may cancel a pause (its launcher only, D3197), that `/pause` is honoured only for a run the service last answered `pause` and once per answer, and what a reader of another format version does (ends with a named outcome, R18); the stubs do nothing.
- Files: `internal/runstore/pause.go` (new), `internal/runstore/pause_test.go` (new), `internal/runner/suspend_stub.go` (new, stub), `internal/orchestrator/resume_stub.go` (new, stub), `docs/model-credentials-routing/contract-pause.md` (new).
- Interfaces: Consumes nothing. Produces the paused record, the node, the status constant and the `resumes` member for `pause-resume`, VS3 and VS4.
- Tests: `TestPauseShapesAreStable`.
- Command: `go test ./internal/runstore/ -run 'TestPauseShapesAreStable' -count=1`
- Red step: `TestPauseShapesAreStable` fails to compile before the file exists.
- Depends on: A3 (kept: the credentials-service and launcher plans must carry the amended D138 and D3136 before a side is written against a contract that rests on them).
- Commit: `model-credentials-routing C3: the pause contract`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

## Slice 2: `routing-table`

### RT1: the resolver: the most specific pattern wins

- Module: `routing-table`.
- Acceptance: `routing.Resolve` returns the entry whose pattern is the most specific match for a model: an exact id beats a prefix, a longer prefix beats a shorter one, two entries with the same pattern are an error, and a model nothing matches is the typed `ErrNoEntry` (D3172, R1). It reuses `config.PatternMatches` and adds no second grammar.
- Files: `internal/routing/resolve.go` (new), `internal/routing/resolve_test.go` (new).
- Interfaces: Consumes C1's types. Produces `Resolve`, which RT3, CS1 and MG1's tests call.
- Tests: `TestResolveMostSpecificWins`, `TestResolveRefusesTwinPatterns`, `TestResolveNoEntry`.
- Command: `go test ./internal/routing/ -run 'TestResolve' -count=1`
- Red step: `TestResolveMostSpecificWins` fails against the stub, which answers the first entry.
- Depends on: C1 (the contract's types and the stub it replaces).
- Commit: `model-credentials-routing RT1: the resolver: the most specific pattern wins`
- Reviewer tier: standard.

### RT2: the project's `model_routing` block and per-repository admission

- Module: `routing-table`.
- Acceptance: The installation's config decodes a strict `model_routing:` list of entries and, for a key, a provider key and Vertex, an `admit:` list of repositories (D3182; a subscription account keeps its own `allow_data_to`, so a pool member a repository is not admitted to is skipped); a repository's `fugaro.yaml` and the project layer have no such key and an attempt is an unknown-field error; the block round-trips through `localcfg`'s writer with comments kept; the settings registry and its schema gain the rows the coverage test asks for.
- Files: `internal/localcfg/localcfg.go`, `internal/localcfg/routing.go` (new), `internal/localcfg/routing_test.go` (new), `internal/settings/registry.go`, `schemas/settings.schema.json`.
- Interfaces: Consumes C1's types. Produces the block RT3, RT4, RT5, MG2 and MG4 read and write.
- Tests: `TestModelRoutingDecodesStrictly`, `TestModelRoutingRoundTrips`, `TestAdmissionPerSource`, `TestRegistryCoversEveryConfigField`.
- Command: `go test ./internal/localcfg/ -run 'TestModelRouting|TestAdmissionPerSource' -count=1` and `go test ./internal/settings/ -run 'TestRegistryCoversEveryConfigField' -count=1`
- Red step: `TestModelRoutingDecodesStrictly` fails: the key is an unknown field today.
- Depends on: C1 (the contract's types).
- Commit: `model-credentials-routing RT2: the project's `model_routing` block and per-repository admission`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### RT3: the table's checks

- Module: `routing-table`.
- Acceptance: One function reports every problem of a table against the project's credentials, pools, providers and crafts: a model a run can call that no entry matches, counting every model the rungs can choose (`craft.ModelRungs`: the task's grade, a level's model, a craft's model, a pinned role of `agent.models`, `agent.model`) and a `--model` override (R1, R22); a chain whose only source is the launcher's pool (D3180); a source that does not reach the model (`craft.ByReach`); a source with no mapped spelling for the model (D3175); a Claude model through a provider before Check 50 is recorded as passed (R3); a paid source after a pool without `enforce` and a per-run cap (R17); a pool or provider that is not declared. A subscription token in no pool is reported as a warning in release 1 and is refused in release 2 (X2). A non-Claude model routed to Vertex is refused by a rule of its own, which GV4 lifts for a mapped and priced Gemini model once the translation exists. Each problem names the fix.
- Files: `internal/routing/checks.go` (new), `internal/routing/checks_test.go` (new).
- Interfaces: Consumes RT1's `Resolve` and RT2's block. Produces `routing.Problems`, which RT5, RT6, RT7 and MG2 call.
- Tests: `TestProblems` (one sub-test per rule above).
- Command: `go test ./internal/routing/ -run 'TestProblems' -count=1`
- Red step: `TestProblems/unmatched_craft_model` fails before the function exists.
- Depends on: C1 (the contract's spelling signature); RT1 (kept, behaviour: it calls the resolver's answer, and a stub's answer would pass a wrong table); RT2 (kept: it reads the block RT2 adds).
- Commit: `model-credentials-routing RT3: the table's checks`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### RT4: the table is published in the policy

- Module: `routing-table`.
- Acceptance: `fugaro update` and the policy publisher write one new top-level policy key, `model_routing`, holding the table, the admissions and the pools in their new shape; `craft_pools`, `credentials` and every other existing block are left byte for byte as a release-behind reader decodes them, with a test that the previous release's decoder still reads the published node; the policy's validation refuses a table `routing.Problems` refuses; an installation at the previous policy version converges.
- Files: `internal/projectpolicy/policy.go`, `internal/projectpolicy/routing_test.go` (new), `internal/cli/init_policy_publish.go`, `internal/cli/update_route_test.go`.
- Interfaces: Consumes RT2's block and RT3's checks. Produces the published member the service reads (CS1).
- Tests: `TestPolicyCarriesModelRouting`, `TestAReleaseBehindReaderStillDecodes`, `TestUpdatePublishesModelRouting`, `TestUpdateConvergesFromAPolicyWithoutRouting`.
- Command: `go test ./internal/projectpolicy/ -run 'TestPolicyCarriesModelRouting|TestAReleaseBehindReaderStillDecodes' -count=1` and `go test ./internal/cli/ -run 'TestUpdatePublishesModelRouting|TestUpdateConvergesFromAPolicyWithoutRouting' -count=1`
- Red step: `TestPolicyCarriesModelRouting` fails: the policy decodes strictly and has no such member.
- Depends on: RT2 (kept, behaviour: it publishes what the block holds); RT3 (kept, behaviour: the publisher must refuse what the checks refuse); PL1 (kept, behaviour: it publishes the pools in the shape PL1 adds to the config).
- Commit: `model-credentials-routing RT4: the table is published in the policy`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### RT5: `fugaro routing ls`, `set` and `rm`

- Module: `routing-table`.
- Acceptance: `fugaro routing ls` prints the table with, per model a craft uses, the entry that wins and why; `fugaro routing set <pattern> <source>...` and `rm <pattern>` write the block and refuse what `routing.Problems` refuses, printing the fix; `--json` has a schema; none asks a question, and `set` in a coding agent's session is refused as a cloud write is today. The setup skill names the command in the same change, as the plugin's tests require.
- Files: `internal/cli/routing.go` (new), `internal/cli/routing_test.go` (new), `schemas/routing-ls.schema.json` (new), `plugin/skills/setup/SKILL.md`, `internal/cli/root.go`, `website/content/cli-reference.md`.
- Interfaces: Consumes RT2's block and RT3's checks. Produces the command MG4's refusals and the documents name.
- Tests: `TestRoutingLs`, `TestRoutingSetRefusesWithTheFix`, `TestRoutingAsksNothing`.
- Command: `go test ./internal/cli/ -run 'TestRouting' -count=1`
- Red step: `TestRoutingLs` fails: the command does not exist.
- Depends on: RT3 (kept, behaviour: `set` refuses by the checks' answer); RT2 (kept: it writes the block RT2 adds).
- Commit: `model-credentials-routing RT5: `fugaro routing ls`, `set` and `rm``
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### RT6: `validate`, `craft ls` and `models use` read the table

- Module: `routing-table`.
- Acceptance: `fugaro validate` reports the table's problems for the repository's own crafts; `fugaro craft ls` shows each craft's model and the chain it resolves to in place of an access; `fugaro models use` refuses a model no entry matches, with the fix; the enterprise A/B code reads the chain where it read the access.
- Files: `internal/cli/validate.go`, `internal/cli/craft.go`, `internal/cli/models_use.go`, `ee/ab/admit.go`, `schemas/craft-ls.schema.json`.
- Interfaces: Consumes RT3's `routing.Problems`. Produces the helpers RT7 and MG6 call.
- Tests: `TestValidateReportsRoutingProblems`, `TestCraftLsShowsTheChain`, `TestModelsUseRefusesAnUnroutedModel`.
- Command: `go test ./internal/cli/ -run 'TestValidateReportsRoutingProblems|TestCraftLsShowsTheChain|TestModelsUseRefusesAnUnroutedModel' -count=1`
- Red step: `TestCraftLsShowsTheChain` fails: the output has an access column.
- Depends on: RT3 (kept, behaviour: it prints the checks' own answer); CS5 (kept: one file, `validate.go`).
- Commit: `model-credentials-routing RT6: `validate`, `craft ls` and `models use` read the table`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### RT7: doctor reads the table

- Module: `routing-table`.
- Acceptance: `fugaro doctor` reports a table problem, a subscription token in no pool, a source whose secret is missing from the bundle, a chain that names the launcher's pool on a project where no one has designated one, and a pausing project with no resident orchestrator; it no longer reports a repository's auth mode.
- Files: `internal/cli/doctor.go`, `internal/cli/doctor_brokering.go`, `internal/cli/doctor_routing.go` (new), `internal/cli/doctor_routing_test.go` (new).
- Interfaces: Consumes RT3 and RT6's helpers. Produces nothing a task calls.
- Tests: `TestDoctorRouting` (one sub-test per report).
- Command: `go test ./internal/cli/ -run 'TestDoctorRouting' -count=1`
- Red step: `TestDoctorRouting/table_problem` fails before the file exists.
- Depends on: RT6 (kept: it calls the helpers RT6 adds to `validate.go`); C2 (the contract's designation record).
- Gate: **Not launched before always-a-budget's AB15 has merged (both edit `doctor_brokering.go`).**
- Commit: `model-credentials-routing RT7: doctor reads the table`
- Reviewer tier: standard.

## Slice 3: `pools`

### PL1: a pool's rule, owner and threshold in the config

- Module: `pools`.
- Acceptance: `config.Pool` gains three new members and changes none: `rule` (with `ordered`; `strategy` is still read and means the same, and `internal/policy/craft.go` ranks `ordered`), `principal` (a kind and an id; `owner` stays an email and keeps its meaning), and an optional `threshold` between 0 and 1 that validation refuses unless the rule set allows it (PL8). A subscription token in no pool is reported and not refused in release 1, since `update` has not wrapped it yet (D3176; X2 refuses it). `subscription_spread` is still decoded. The settings registry gains the rows its coverage test asks for.
- Files: `internal/config/craft.go`, `internal/config/pool_rules.go` (new), `internal/config/pool_rules_test.go` (new), `internal/localcfg/localcfg.go`, `internal/policy/craft.go`, `internal/settings/registry.go`, `schemas/settings.schema.json`.
- Interfaces: Consumes C2's types. Produces the fields PL2, PL3, MG2 and MG5 read.
- Tests: `TestPoolRuleOrdered`, `TestPoolPrincipalKinds`, `TestOwnerKeepsItsMeaning`, `TestRegistryCoversEveryConfigField`.
- Command: `go test ./internal/config/ -run 'TestPoolRuleOrdered|TestPoolPrincipalKinds|TestOwnerKeepsItsMeaning' -count=1` and `go test ./internal/settings/ -run 'TestRegistryCoversEveryConfigField' -count=1`
- Red step: `TestPoolRuleOrdered` fails: `ordered` is an unknown strategy today.
- Depends on: C2 (the contract's types); RT2 (kept: one file, `localcfg.go`).
- Commit: `model-credentials-routing PL1: a pool's rule, owner and threshold in the config`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PL2: the account commands keep every token in a pool

- Module: `pools`.
- Acceptance: `fugaro accounts add` puts a new subscription token in a pool (a pool of one named after it unless `--pool` names another); `fugaro accounts pool add` takes `--rule` and a service-account principal; `fugaro accounts rm` of a pool's last member removes the pool or refuses with the fix; `fugaro accounts pool ls --json` prints the shape of C2's contract; none asks a question.
- Files: `internal/cli/accounts_add.go`, `internal/cli/accounts_pool.go`, `internal/cli/accounts_rm.go`, `internal/cli/accounts_pool_rule_test.go` (new), `schemas/accounts-pool-ls.schema.json` (new), `website/content/cli-reference.md`.
- Interfaces: Consumes PL1's fields and C2's JSON shape. Produces the commands PL5 extends and the JSON VS2 reads.
- Tests: `TestAccountsAddMakesAPoolOfOne`, `TestPoolAddTakesARule`, `TestPoolLsJSONHasNoValue`.
- Command: `go test ./internal/cli/ -run 'TestAccountsAddMakesAPoolOfOne|TestPoolAddTakesARule|TestPoolLsJSONHasNoValue' -count=1`
- Red step: `TestAccountsAddMakesAPoolOfOne` fails: `add` writes a lone account today.
- Depends on: PL1 (kept: it writes the fields PL1 adds); RT5 (kept: one file, the generated `cli-reference.md`).
- Commit: `model-credentials-routing PL2: the account commands keep every token in a pool`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PL3: the service picks inside a pool by its rule

- Module: `pools`.
- Acceptance: Inside one pool the service picks by the pool's rule: `ordered` is the first listed member that is usable, `round-robin` rotates, `failover` is unchanged; a pool of several subscription tokens serves all of them with `subscription_spread` off (D3177). Until `fugaro update` has migrated an installation (its published policy carries `model_routing`, as C2's contract defines), a pool that is inert today stays inert, so merging this task alone moves nothing. The rule lives in one new file and `internal/creds/select.go` is not edited.
- Files: `internal/creds/poolrule.go` (new), `internal/creds/poolrule_test.go` (new), `internal/credsvc/pool.go`, `internal/credsvc/roundrobin.go`.
- Interfaces: Consumes C2's rule names. Produces `creds.PickInPool`, which CS1 calls through the contract's signature.
- Tests: `TestPickOrdered`, `TestPickRoundRobin`, `TestAPoolServesWithoutTheSpreadSwitch`, `TestAnUnmigratedInstallationKeepsItsInertPool`.
- Command: `go test ./internal/creds/ -run 'TestPick' -count=1` and `go test ./internal/credsvc/ -run 'TestAPoolServesWithoutTheSpreadSwitch|TestAnUnmigratedInstallationKeepsItsInertPool' -count=1`
- Red step: `TestAPoolServesWithoutTheSpreadSwitch` fails: such a pool is inert today.
- Depends on: C2 (the contract's rule names).
- Commit: `model-credentials-routing PL3: the service picks inside a pool by its rule`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PL4: the launcher's pool: a person's own tokens

- Module: `pools`.
- Acceptance: The service resolves "the launcher's pool" for a run from the verified owner of its slip and that person's designation for the project, keyed by Google subject, and serves the pool's members from that person's per-user containers in the project, only to runs that person launched (and to a run that resumes one, PR3); a run with no slip resolves to "none"; a run launched with `--pool` resolves to the named pool only where its launcher owns it or is allowed on it, and to nothing otherwise (D3176); no designation, or an empty pool, answers "none" and never an error. A designation is written only through a route of the service keyed on the caller's verified subject, which refuses a pool the caller does not own and a designation for another principal; the database rules cannot hold this (people reach the database by IAM), so the route is the only writer and the store is otherwise closed. Mutation tests: a forged `requested_by` changes nothing, and naming another person's pool gets nothing. Every new refusal writes exactly one audit line and no value.
- Files: `internal/credsvc/launcherpool.go` (new), `internal/credsvc/launcherpool_test.go` (new), `internal/creds/designation.go` (new), `internal/creds/designation_test.go` (new), `internal/credsvc/designation_route.go` (new), `internal/credsvc/server.go`.
- Interfaces: Consumes C2's interface and record and the per-user grant that exists (`checkGrantedNames` in `internal/credsvc/slip.go`). Produces the real `LauncherPool` behind the stub.
- Tests: `TestLauncherPoolIsTheSlipOwners`, `TestNoDesignationIsNone`, `TestARunWithNoSlipHasNoLaunchersPool`, `TestAForgedRequesterGetsNothing`, `TestANamedPoolMustBeTheLaunchers`, `TestDesignationRouteRefusesAnothersPool`.
- Command: `go test ./internal/credsvc/ -run 'TestLauncherPool|TestNoDesignationIsNone|TestARunWithNoSlipHasNoLaunchersPool|TestAForgedRequesterGetsNothing|TestANamedPoolMustBeTheLaunchers|TestDesignationRouteRefusesAnothersPool' -count=1`
- Red step: `TestLauncherPoolIsTheSlipOwners` fails against the stub, which always answers "none".
- Depends on: C2 (the contract's interface and record); CS1 (kept: one file, `server.go`).
- Commit: `model-credentials-routing PL4: the launcher's pool: a person's own tokens`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PL5: designate a pool, and contribute a token to the shared pool

- Module: `pools`.
- Acceptance: `fugaro accounts pool designate <pool>` asks the service's designation route (PL4) to record the caller's own designation for the project, from their own session only; `fugaro accounts pool contribute --to <shared pool>` takes the caller's own token through the intake that already receives secret values, into a member of the project pool marked as that person's contribution, where an admin has marked the pool as taking contributions (D3194, which reverses D1935 by this one case); the contributor withdraws their own member (`accounts pool withdraw`) and an admin removes any (`accounts pool rm-member`); nobody adds a token in another person's name (R9); none asks a question, and a value only a person can type fails at once with no terminal. Every new refusal writes exactly one audit line and no value.
- Files: `internal/cli/accounts_pool.go`, `internal/cli/accounts_pool_designate.go` (new), `internal/cli/accounts_pool_designate_test.go` (new), `internal/intake/handlers.go`, `internal/cli/secrets_intake.go`, `plugin/skills/setup/reference/decisions.md`, `website/content/cli-reference.md`.
- Interfaces: Consumes PL4's designation store and PL2's commands. Produces nothing a task calls.
- Tests: `TestDesignateIsTheCallersOwn`, `TestContributeIsTheHoldersOwn`, `TestAContributorWithdrawsOnlyTheirOwn`.
- Command: `go test ./internal/cli/ -run 'TestDesignateIsTheCallersOwn|TestContributeIsTheHoldersOwn' -count=1` and `go test ./internal/intake/ -run 'TestAContributorWithdrawsOnlyTheirOwn' -count=1`
- Red step: `TestDesignateIsTheCallersOwn` fails: the command does not exist.
- Depends on: PL2 (kept: one file, `accounts_pool.go`); PL4 (kept, behaviour: it writes through the store PL4 builds, whose rules decide who may write); CS15 (kept: one file, the generated `cli-reference.md`).
- Commit: `model-credentials-routing PL5: designate a pool, and contribute a token to the shared pool`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PL6: a service account's pool

- Module: `pools`.
- Acceptance: A run whose slip names a verified service account (`VerifyServiceAccountLauncher`) resolves "the launcher's pool" to the project pool whose principal it is, filled by an admin (D3181); it never resolves to a person's pool, with the one case D3191 adds: a run that resumes a person's paused run draws on that person's pool (PR3); a run the orchestrator reissues keeps the launcher recorded on the original slip and the pool that launch named.
- Files: `internal/credsvc/launcherpool.go`, `internal/credsvc/launcherpool_sa_test.go` (new), `internal/credsvc/launchers.go`.
- Interfaces: Consumes PL4's lookup. Produces nothing a task calls.
- Tests: `TestAServiceAccountsPool`, `TestAServiceAccountNeverGetsAPersonsPool`, `TestAReissuedRunKeepsItsLauncher`.
- Command: `go test ./internal/credsvc/ -run 'TestAServiceAccount|TestAReissuedRunKeepsItsLauncher' -count=1`
- Red step: `TestAServiceAccountsPool` fails: such a run resolves to "none".
- Depends on: PL4 (kept: one file, `launcherpool.go`, and it extends that lookup); CS14 (kept, behaviour: it resolves a service account from the grant CS14 teaches to admit that class).
- Commit: `model-credentials-routing PL6: a service account's pool`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PL7: Check 880, as a script

- Module: `pools`.
- Acceptance: The service records, per source and without any value, the names of the rate-limit headers the last answer carried; `scripts/live-checks/check-880-token-utilisation.sh`, run by the owner after any run on a subscription token, reads that record and prints whether one of them is a utilisation figure (a model route admits a run's identity only, so the script makes no call of its own); its test runs it against a fake and proves it prints no token.
- Files: `scripts/live-checks/check-880-token-utilisation.sh` (new), `scripts/live-checks/check-880-token-utilisation_test.sh` (new), `internal/credsvc/forward.go`, `internal/creds/marks.go`, `docs/gcp-live-checklist.md`.
- Interfaces: Consumes nothing. Produces the answer that gates PL8 (U1).
- Tests: The script's own test.
- Command: `bash scripts/live-checks/check-880-token-utilisation_test.sh` and `go test ./internal/credsvc/ -run 'TestHeaderNamesAreRecordedWithoutValues' -count=1`
- Red step: The test fails before the script exists.
- Depends on: CS4 (kept: one file, `forward.go`).
- Commit: `model-credentials-routing PL7: Check 880, as a script`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).
- Owner-run: The owner runs `bash scripts/live-checks/check-880-token-utilisation.sh` in his own session against the dev installation. No agent runs it.

### PL8: the usage threshold rule

- Module: `pools`.
- Acceptance: A pool with `threshold: 0.8` moves off a member once the utilisation the provider last reported for it reaches the threshold, before a limit is hit, and comes back when the window resets; with no figure reported the rule does nothing and says so once in the audit line.
- Files: `internal/creds/poolrule.go`, `internal/creds/poolrule_threshold_test.go` (new), `internal/credsvc/forward.go`, `internal/creds/marks.go`.
- Interfaces: Consumes PL3's picker. Produces nothing a task calls.
- Tests: `TestThresholdMovesOffBeforeTheLimit`, `TestThresholdWithNoFigureDoesNothing`.
- Command: `go test ./internal/creds/ -run 'TestThreshold' -count=1`
- Red step: `TestThresholdMovesOffBeforeTheLimit` fails: the picker ignores utilisation.
- Depends on: PL3 (kept: one file, `poolrule.go`, and it extends that picker); PL10 (kept: two files, `forward.go` and `marks.go` through PL7).
- Gate: **Not launched unless the owner's run of Check 880 found a utilisation figure (D3178). If it did not, this task is withdrawn and the plan says so.**
- Commit: `model-credentials-routing PL8: the usage threshold rule`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PL9: a personal pool in the project

- Module: `pools`.
- Acceptance: A person creates a personal pool in the project (`fugaro accounts pool add --personal <name>`), puts their own subscription token in it through the intake (`fugaro accounts add --pool <name>` from their own session stores it in their per-user container), sets its rule and removes a member; the pool's members and rule are kept by the service in a store of its own, written only through a route keyed on the owner's verified subject (a member cannot write the project's config or policy); nobody else can add a token to it, and no command or view shows a value to anyone, an admin included; none asks a question. Every new refusal writes exactly one audit line and no value.
- Files: `internal/cli/accounts_pool_personal.go` (new), `internal/cli/accounts_pool_personal_test.go` (new), `internal/cli/accounts_add.go`, `internal/cli/secrets_intake.go`, `website/content/cli-reference.md`, `internal/credsvc/personalpool_route.go` (new), `internal/credsvc/server.go`.
- Interfaces: Consumes C2's types and the intake. Produces the pools PL4's lookup reads.
- Tests: `TestAPersonCreatesTheirOwnPool`, `TestNobodyElseFillsAPersonalPool`.
- Command: `go test ./internal/cli/ -run 'TestAPersonCreatesTheirOwnPool|TestNobodyElseFillsAPersonalPool' -count=1`
- Red step: `TestAPersonCreatesTheirOwnPool` fails: the flag does not exist.
- Depends on: C2 (the contract's types); PL5 (kept: two files, `secrets_intake.go` and the generated `cli-reference.md`); PL2 (kept: one file, `accounts_add.go`); PL4 (kept: one file, `server.go`).
- Commit: `model-credentials-routing PL9: a personal pool in the project`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PL10: usage and headroom per pool

- Module: `pools`.
- Acceptance: The service counts calls and tokens per pool member per window in a node of its own and `fugaro accounts pool ls --json` prints, per pool and member, the usage of the current window, the next reset, and headroom where the provider reported a utilisation figure (absent otherwise, never guessed); an admin sees every pool's figures and no value; a person sees their own and the shared pools'. The service is the node's only writer; every launcher can read it, so it is display data.
- Files: `internal/creds/usage.go` (new), `internal/creds/usage_test.go` (new), `internal/credsvc/forward.go`, `internal/cli/accounts_pool.go`.
- Interfaces: Consumes PL3's picker path and C2's JSON shape. Produces the figures VS2 draws.
- Tests: `TestUsageIsCountedPerMember`, `TestPoolLsPrintsUsageAndNoValue`.
- Command: `go test ./internal/creds/ -run 'TestUsageIsCountedPerMember' -count=1` and `go test ./internal/cli/ -run 'TestPoolLsPrintsUsageAndNoValue' -count=1`
- Red step: `TestUsageIsCountedPerMember` fails before the file exists.
- Depends on: PL3 (kept, behaviour: it counts on the path the picker serves); PL9 (kept: `accounts_pool.go` through PL5); PL7 (kept: one file, `forward.go`).
- Commit: `model-credentials-routing PL10: usage and headroom per pool`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

## Slice 4: `chain-serving`

### CS1: the service walks the chain

- Module: `chain-serving`.
- Acceptance: For each model call the service reads the model from the request's body (a body that names none is refused; no header is trusted for it), resolves it to its entry and offers the chain's sources in the chain's own order, replacing the fixed tier order (R4): a pool by its rule, the launcher's pool through C2's lookup, a key, a provider key, Vertex. Skipped: a key, provider key or Vertex the repository is not admitted to, a pool member whose account does not admit the repository, an empty launcher's pool, an exhausted source (D3180, D3182). A source the run's grant does not cover is never served; a run with no slip (the grant is best-effort at launch, and an orchestrator's launch has none) is served the project's sources its repository is admitted to and no launcher's pool. The placement is sticky for one step and the chain is walked from its first source at the next (R16). The answer names the source, whether it was the chain's first and the model that served, and carries those members, and any new refusal member, only to a caller that sent the stage header, so a release-behind gateway meets exactly the answer it meets today; with nothing usable it answers exhausted with the earliest reset. A limit found inside a streamed answer marks the source and is not retried (U7). Every new refusal writes exactly one audit line and no value.
- Files: `internal/credsvc/chain.go` (new), `internal/credsvc/chain_test.go` (new), `internal/credsvc/cascade.go`, `internal/creds/select.go`, `internal/credsvc/server.go`.
- Interfaces: Consumes C1's wire and `Resolve`, C2's `LauncherPool` and `PickInPool`. Produces the serving path CS2, CS4 and PR3 meet.
- Tests: `TestChainOrderIsStrict`, `TestChainSkipsAnUnadmittedSource`, `TestChainSkipsAMemberThatDoesNotAdmitTheRepository`, `TestChainIsWalkedAgainEachStep`, `TestAnswerNamesTheSourceAndFallback`, `TestACallWithNoModelInTheBodyIsRefused`, `TestASourceTheGrantDoesNotCoverIsNeverServed`, `TestAReleaseBehindGatewayMeetsTheOldAnswer`.
- Command: `go test ./internal/credsvc/ -run 'TestChain|TestAnswerNamesTheSourceAndFallback|TestACallWithNoModelInTheBodyIsRefused|TestASourceTheGrantDoesNotCoverIsNeverServed|TestAReleaseBehindGatewayMeetsTheOldAnswer' -count=1`
- Red step: `TestChainOrderIsStrict` fails: a key listed before a pool is still served after it.
- Depends on: C1 (the contract's wire and resolver signature); C2 (the contract's pool lookup).
- Gate: **Not launched before always-a-budget's I1 and the keys plan's KS12 have merged (D3071, kept: a key source is served from the bundle KS12 fills).**
- Commit: `model-credentials-routing CS1: the service walks the chain`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CS2: the gateway says which stage a call is for

- Module: `chain-serving`.
- Acceptance: The in-job gateway sends the run's current stage on every call to the service and no model header (the service reads the body), reads the answer's source and fallback members into the step's record hook, and strips them before the agent sees the answer (they join the explicit list in `dropResponseHeaders`). The stage header is the gateway's own: it is set from the run's current stage and the caller's copy is dropped before it is set, the same rule and place as `HeaderProvider` in `serviceUpstream` — an agent that could send it would make the service answer in full, and a release-behind gateway passes an unknown answer header to the agent — and the mirror test carries the request side with the names. It holds no key and no table (R2).
- Files: `internal/gateway/service.go`, `internal/gateway/chainwire.go` (new), `internal/gateway/chainwire_test.go` (new), `internal/gateway/gateway.go`, `internal/gateway/forward.go`.
- Interfaces: Consumes C1's wire names. Produces the hook CS8 reads and the file CS4 extends.
- Tests: `TestGatewaySendsTheStage`, `TestTheAgentNeverSeesTheSource`.
- Command: `go test ./internal/gateway/ -run 'TestGatewaySendsTheStage|TestTheAgentNeverSeesTheSource' -count=1`
- Red step: `TestGatewaySendsTheStage` fails: no such header is sent.
- Depends on: C1 (the contract's wire names).
- Commit: `model-credentials-routing CS2: the gateway says which stage a call is for`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CS3: one model, each source's spelling

- Module: `chain-serving`.
- Acceptance: The checked-in map in `internal/pricing/canonical.go` gains a Vertex spelling per canonical Claude id beside the route id; when a call moves to a key or a provider key whose spelling differs, the service rewrites the request's `model` member to that source's spelling and nothing else in the body (Vertex's wire is CS4's own translation and is excluded from this rule), and restores the model the agent asked for in the answer, so the agent never sees another spelling; a source entry that names a model (a written downgrade) is the same rewrite to the named model; a model with no spelling for a source is never sent there. The answer's `model` member tells the gateway which model to price. Every new refusal writes exactly one audit line and no value.
- Files: `internal/pricing/canonical.go`, `internal/pricing/canonical_test.go`, `internal/credsvc/rewrite.go` (new), `internal/credsvc/rewrite_test.go` (new).
- Interfaces: Consumes C1's `Source.Model` and the `SpellingFor` signature. Produces the real `pricing.SpellingFor` behind C1's stub, which RT3's unmapped-spelling rule and CS1 call.
- Tests: `TestSpellingFor`, `TestRewriteTouchesOnlyTheModel`, `TestTheAnswerShowsTheModelTheAgentAsked`, `TestAWrittenDowngradeIsPricedAsWhatServed`.
- Command: `go test ./internal/pricing/ -run 'TestSpellingFor' -count=1` and `go test ./internal/credsvc/ -run 'TestRewrite|TestTheAnswerShowsTheModelTheAgentAsked|TestAWrittenDowngrade' -count=1`
- Red step: `TestRewriteTouchesOnlyTheModel` fails before the file exists.
- Depends on: C1 (the contract's source shape).
- Commit: `model-credentials-routing CS3: one model, each source's spelling`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CS4: Vertex, served by the service

- Module: `chain-serving`.
- Acceptance: Vertex is a source the credentials service serves on its own identity, as it serves a key: it translates the call to Vertex's Anthropic wire (the model in the URL path, `anthropic_version` in the body); that translation is new work, since today the agent's harness builds the Vertex request and `internal/gateway/vertex.go` only validates its path; and the gateway prices and settles the call as real money against the run's caps, exactly as it does a key call. It is capped as far as a key is and no further: a call an agent builds itself and posts straight to the service is served under the policy's rules and not counted against the run's cap, the residual risk `SECURITY.md` already records for keys (D3003; D3174 as amended twice). A repository not admitted to Vertex is never served it. A Vertex 429 marks the source so the chain moves on or answers exhausted. Every new refusal writes exactly one audit line and no value.
- Files: `internal/credsvc/vertex.go` (new), `internal/credsvc/vertex_test.go` (new), `internal/credsvc/forward.go`.
- Interfaces: Consumes C1's source kinds. Produces the capped Vertex source CS5 and CS11 rely on.
- Tests: `TestVertexIsServedOnTheServicesIdentity`, `TestVertexWireIsTranslated`, `TestVertexIsSettledAsMoney`, `TestAnUnadmittedRepositoryGetsNoVertex`, `TestAVertexLimitMovesOn`.
- Command: `go test ./internal/credsvc/ -run 'TestVertexIsServedOnTheServicesIdentity|TestVertexWireIsTranslated|TestVertexIsSettledAsMoney|TestAnUnadmittedRepositoryGetsNoVertex|TestAVertexLimitMovesOn' -count=1`
- Red step: `TestVertexIsServedOnTheServicesIdentity` fails before the file exists.
- Depends on: C1 (the contract's source kinds).
- Commit: `model-credentials-routing CS4: Vertex, served by the service`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CS5: Vertex is no longer refused under `enforce`

- Module: `chain-serving`.
- Acceptance: For a run the table serves, a chain that names Vertex is accepted under `budget.mode: enforce`: `VertexBudgetRefusal` and the refusal of a pool with a Vertex member no longer fire for it, at `validate`, at `init` and in the runner. An unmigrated repository whose file still says `auth: vertex` runs on the job's identity as today and is still refused under `enforce`, since nothing caps that path. `SECURITY.md`'s sentence is left for DOC1.
- Files: `internal/runner/gateway.go`, `internal/runner/poolchoice.go`, `internal/config/stage_accounts.go`, `internal/cli/init.go`, `internal/cli/validate.go`, `internal/runner/vertex_refusal_test.go` (new).
- Interfaces: Consumes CS4's capped source. Produces a Vertex that is an ordinary paid source (D3174).
- Tests: `TestVertexInAChainIsAcceptedUnderEnforce`, `TestLegacyVertexIsStillRefusedUnderEnforce`.
- Command: `go test ./internal/runner/ -run 'TestVertexInAChainIsAcceptedUnderEnforce|TestLegacyVertexIsStillRefusedUnderEnforce' -count=1`
- Red step: `TestVertexInAChainIsAcceptedUnderEnforce` fails: the refusal fires.
- Depends on: CS4 (kept, behaviour: the refusal may go only once the served path exists); CS11 (kept, behaviour: the refusal may go only where the job's account no longer holds the Vertex permission).
- Gate: **Not launched before always-a-budget's AB1b has merged (it rewrites `VertexBudgetRefusal` and `runner/gateway.go`).**
- Commit: `model-credentials-routing CS5: Vertex is no longer refused under `enforce``
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CS6: the run's model path follows the table

- Module: `chain-serving`.
- Acceptance: The runner no longer chooses a whole-run upstream from `agent.auth`: every stage's calls go through the gateway to the service, the model per stage is chosen as today, and an unmigrated repository on a release-1 image still runs as its file says (credentials-belong-to-models R3, kept).
- Files: `internal/runner/routing.go`, `internal/runner/gateway.go`, `internal/runner/runner.go`, `internal/runner/routing_table_test.go` (new).
- Interfaces: Consumes C1. Produces the path CS7 and PR2 extend.
- Tests: `TestEveryStageGoesThroughTheChain`, `TestAnUnmigratedRepositoryRunsAsItsFileSays`.
- Command: `go test ./internal/runner/ -run 'TestEveryStageGoesThroughTheChain|TestAnUnmigratedRepositoryRunsAsItsFileSays' -count=1`
- Red step: `TestEveryStageGoesThroughTheChain` fails: an `oauth` run has no gateway.
- Depends on: C1 (the contract's names); CS5 (kept: one file, `runner/gateway.go`).
- Gate: **Not launched before always-a-budget's AB1b has merged (it rewrites `routing.go`, `gateway.go` and `runner.go`).**
- Commit: `model-credentials-routing CS6: the run's model path follows the table`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CS7: the agent's environment, and the account rules that go

- Module: `chain-serving`.
- Acceptance: The agent's environment carries no subscription token and no key where the gateway serves the run; the per-stage account walk and the run-level account choice are removed, since a pool's rule replaces them (D3171); a release-1 image on an unmigrated repository keeps the old environment.
- Files: `internal/agent/env.go`, `internal/runner/agentenv.go`, `internal/runner/accountchoice.go`, `internal/runner/poolchoice.go`, `internal/agent/env_chain_test.go` (new).
- Interfaces: Consumes C1. Produces nothing a task calls.
- Tests: `TestTheAgentHoldsNoCredentialUnderAChain`, `TestNoAccountWalk`.
- Command: `go test ./internal/agent/ -run 'TestTheAgentHoldsNoCredentialUnderAChain' -count=1` and `go test ./internal/runner/ -run 'TestNoAccountWalk' -count=1`
- Red step: `TestTheAgentHoldsNoCredentialUnderAChain` fails: the token is in the environment.
- Depends on: C1 (the contract's names); CS5 (kept: one file, `poolchoice.go`).
- Gate: **Not launched before always-a-budget's AB1b has merged (it rewrites `agent/env.go` and `runner/agentenv.go`).**
- Commit: `model-credentials-routing CS7: the agent's environment, and the account rules that go`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CS8: the record says what served each step

- Module: `chain-serving`.
- Acceptance: The run record gains, per stage, `source` (kind and name, never a value) and `fallback` (true when any call of the stage was served by a source that is not the chain's first), and `cost.fallback_usd` (the real money spent on fallback sources); existing members keep their meaning; the result schema lists the new members; the gateway's ledger prices each call by the answer's `model` member, so a written downgrade and a Vertex call are priced as what served, and a paid fallback is settled against the caps as any key call (D140).
- Files: `internal/runstore/runstore.go`, `internal/runstore/cost.go`, `internal/runner/cost.go`, `internal/runner/stephook.go`, `internal/gateway/ledger.go`, `schemas/result.schema.json`.
- Interfaces: Consumes C1's member names and CS2's hook through the contract. Produces the members VS1 and VS4 read.
- Tests: `TestTheRecordNamesTheSource`, `TestFallbackUSDIsRealMoneyOnly`, `TestTheLedgerPricesTheModelThatServed`.
- Command: `go test ./internal/runner/ -run 'TestTheRecordNamesTheSource|TestFallbackUSDIsRealMoneyOnly' -count=1` and `go test ./internal/gateway/ -run 'TestTheLedgerPricesTheModelThatServed' -count=1`
- Red step: `TestTheRecordNamesTheSource` fails: the member does not exist.
- Depends on: C1 (the contract's member names).
- Commit: `model-credentials-routing CS8: the record says what served each step`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CS9: the registry entry and the live views

- Module: `chain-serving`.
- Acceptance: The live registry entry gains `source` and `fallback`; for a run the table serves `auth` is written as nothing and the views print `-`, as D3070 settled; the rules template admits the new member; `fugaro watch` and its `--json` show the source of the running stage. The rules change ships with a test that an installation at the previous rules version converges.
- Files: `internal/budget/model.go`, `internal/budget/registry.go`, `internal/watch/json.go`, `internal/watch/render.go`, `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/marker.go`, `internal/budget/rules/testdata/rules.golden.json`.
- Interfaces: Consumes C1's member names. Produces the entry PR1, PR7 and the console read.
- Tests: `TestTheEntryCarriesTheSource`, `TestWatchPrintsADashForAuth`, `TestRulesConvergeWithTheSource`.
- Command: `go test ./internal/budget/ -run 'TestTheEntryCarriesTheSource|TestRulesConvergeWithTheSource' -count=1` and `go test ./internal/watch/ -run 'TestWatchPrintsADashForAuth' -count=1`
- Red step: `TestTheEntryCarriesTheSource` fails: the rules refuse the member.
- Depends on: C1 (the contract's member names).
- Commit: `model-credentials-routing CS9: the registry entry and the live views`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CS10: the launch

- Module: `chain-serving`.
- Acceptance: `fugaro run` asks for a grant of the sources the run's models can reach by the table, tells the job about the service wherever a chain has a served source, and refuses with the fix a launch where no entry matches every model the rungs can choose (`craft.ModelRungs`: the task's grade, a level's model, a craft's model, a pinned role of `agent.models`, `agent.model`) and a `--model` override; it asks nothing.
- Files: `internal/cli/run.go`, `internal/cli/run_attach.go`, `internal/cli/launch_budget.go`, `internal/cli/run_chain_test.go` (new).
- Interfaces: Consumes C1 and CS11's plan of the cloud side. Produces the launch every later live check uses.
- Tests: `TestRunGrantsByTheTable`, `TestRunRefusesAnUnroutedModel`.
- Command: `go test ./internal/cli/ -run 'TestRunGrantsByTheTable|TestRunRefusesAnUnroutedModel' -count=1`
- Red step: `TestRunRefusesAnUnroutedModel` fails: the launch goes ahead.
- Depends on: C1 (the contract's names); CS11 (kept, behaviour: it decides whether a job is told about the service from what CS11 plans); C2 (the contract's grant members).
- Commit: `model-credentials-routing CS10: the launch`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CS11: the cloud side is planned from the table

- Module: `chain-serving`.
- Acceptance: The infra spec derives what a job mounts and whether it is told about the service from the table and the pools (no secret mounted for a served source), not from `agent.auth`; `infra.ModelSecretsServed` answers from the table and the Terraform variable `model_secrets_served` keeps its three values and their meaning; the credentials service's own account is planned the Vertex permission wherever a chain names Vertex, through a custom role and never a predefined one, as the rules file requires; on a project whose budget mode is `enforce` a job's account and the shared project job's account are planned no Vertex permission (no run in flight can hold it there: Vertex is refused under `enforce` today), and on an `observe` project they keep it until release 2 (X8), so an `observe` run in flight is not broken; the Terraform tests pin all three.
- Files: `internal/infra/spec.go`, `internal/infra/chain_spec.go` (new), `internal/infra/chain_spec_test.go` (new), `internal/infra/readiness.go`, `deploy/terraform/gcp/modules/installation/credentials.tf`, `deploy/terraform/gcp/modules/workflow/sa.tf`, `deploy/terraform/gcp/modules/project/sa.tf`, `deploy/terraform/terraform_test.go`, `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl`.
- Interfaces: Consumes C1. Produces the plan CS10 reads.
- Tests: `TestSpecFromTheTable`, `TestTheServiceIsPlannedVertexWhereNamed`, `TestNoJobVertexGrantUnderEnforce`; the installation root's Terraform test.
- Command: `go test ./internal/infra/ -run 'TestSpecFromTheTable|TestTheServiceIsPlannedVertexWhereNamed|TestNoJobVertexGrantUnderEnforce' -count=1` and `go test -tags terraform ./internal/infra/... -run 'Installation' -count=1`
- Red step: `TestSpecFromTheTable` fails before the file exists.
- Depends on: C1 (the contract's names); CS4 (kept, behaviour: the permission it plans is for the caller CS4 builds).
- Gate: **Not launched before always-a-budget's AB15 has merged (both edit `spec.go`). Retiring the flip list `config.ServiceServedAuths` needs the owner's go, as the rules file says (O7); this task leaves it.**
- Commit: `model-credentials-routing CS11: the cloud side is planned from the table`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CS12: `--pool` replaces `--token`

- Module: `chain-serving`.
- Acceptance: `fugaro run --pool <name>` carries the name on the run's grant request as the pool the launcher's pool resolves to for this run, and is refused with the fix where no chain of the run's models names the launcher's pool; `--token` is rejected with a fix naming `--pool` (D3176 as amended); a follow-up launched by a person uses that person's own designation or `--pool`, and only a resume keeps the pool the paused run's launch named; the working skill and its three reference pages that name `fugaro run --token` name `--pool` in the same change, as the plugin's tests require.
- Files: `internal/cli/run.go`, `internal/cli/run_pool_test.go` (new), `plugin/skills/working/SKILL.md`, `plugin/skills/working/reference/diagnose.md`, `plugin/skills/working/reference/followup.md`, `plugin/skills/working/reference/launch.md`, `website/content/cli-reference.md`.
- Interfaces: Consumes C2's named-pool member and CS10's launch. Produces the flag PL4 resolves.
- Tests: `TestTokenFlagIsRejectedWithTheFix`, `TestPoolFlagNeedsALaunchersPoolInAChain`, `TestAFollowUpUsesItsOwnLaunchersPool`; the plugin's tests.
- Command: `go test ./internal/cli/ -run 'TestTokenFlagIsRejectedWithTheFix|TestPoolFlagNeedsALaunchersPoolInAChain|TestAFollowUpUsesItsOwnLaunchersPool' -count=1` and `go test ./plugin/... -count=1`
- Red step: `TestTokenFlagIsRejectedWithTheFix` fails: the flag is accepted.
- Depends on: C2 (the contract's named-pool member); CS10 (kept: one file, `run.go`); PL2 (kept: one file, the generated `cli-reference.md`); CS14 (kept, behaviour: the flag is carried in the grant member CS14 adds).
- Commit: `model-credentials-routing CS12: `--pool` replaces `--token``
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CS13: the other model callers go by the table

- Module: `chain-serving`.
- Acceptance: The shared project jobs, the review job and the diagnose agent reach a model through the table as a run does: none reads `agent.auth` or an account name to choose a credential, each is refused with the fix where its model has no entry, and each records its source.
- Files: `internal/runner/projectjob.go`, `internal/infra/projectjobs.go`, `internal/credsvc/review.go`, `internal/diagnoseagent/creds.go`, `internal/runner/projectjob_chain_test.go` (new).
- Interfaces: Consumes C1. Produces nothing a task calls.
- Tests: `TestAProjectJobGoesByTheTable`, `TestTheReviewJobGoesByTheTable`, `TestTheDiagnoseAgentGoesByTheTable`.
- Command: `go test ./internal/runner/ -run 'TestAProjectJobGoesByTheTable' -count=1`, `go test ./internal/credsvc/ -run 'TestTheReviewJobGoesByTheTable' -count=1` and `go test ./internal/diagnoseagent/ -run 'TestTheDiagnoseAgentGoesByTheTable' -count=1`
- Red step: `TestAProjectJobGoesByTheTable` fails: it reads the account.
- Depends on: C1 (the contract's names).
- Commit: `model-credentials-routing CS13: the other model callers go by the table`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CS14: the grant carries the chain's sources and the named pool

- Module: `chain-serving`.
- Acceptance: A run's grant records, under the new member names of C2's contract, the sources its models can reach by the table and the pool named with `--pool`; the service refuses to grant a source the table does not give the repository, a personal pool to anyone but its owner, and a named pool the launcher is not entitled to; `Slip.Pool` keeps its meaning; the grant admits the service-account and orchestrator launcher classes, which only the identity routes admit today (`VerifyServiceAccountLauncher`), so PL6 and PR3 have a principal to resolve; an older CLI's grant request, which names no sources, is served as today. Every new refusal writes exactly one audit line and no value.
- Files: `internal/credsvc/slip.go`, `internal/creds/slips.go`, `internal/credsvc/slip_chain_test.go` (new).
- Interfaces: Consumes C1's table and C2's grant members. Produces the grant CS1 checks, PL4 resolves from and PR3 binds a resume to.
- Tests: `TestTheGrantCoversOnlyTheTablesSources`, `TestANamedPoolIsGrantedOnlyToItsOwner`, `TestAnOlderGrantRequestIsServedAsToday`.
- Command: `go test ./internal/credsvc/ -run 'TestTheGrantCoversOnlyTheTablesSources|TestANamedPoolIsGrantedOnlyToItsOwner|TestAnOlderGrantRequestIsServedAsToday' -count=1`
- Red step: `TestTheGrantCoversOnlyTheTablesSources` fails: the grant has no such member.
- Depends on: C1 (the contract's table types); C2 (the contract's grant members).
- Commit: `model-credentials-routing CS14: the grant carries the chain's sources and the named pool`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CS15: `fugaro review --token` goes

- Module: `chain-serving`.
- Acceptance: `fugaro review --pr N` takes no `--token`: the review job reaches its model by the table (CS13), and the flag is rejected with the fix; the skill pages that name it are changed in the same change.
- Files: `internal/cli/review.go`, `internal/cli/review_token_test.go` (new), `plugin/skills/routing/SKILL.md`, `plugin/skills/working/reference/review.md`, `website/content/cli-reference.md`.
- Interfaces: Consumes CS13's review job. Produces nothing a task calls.
- Tests: `TestReviewTokenFlagIsRejectedWithTheFix`; the plugin's tests.
- Command: `go test ./internal/cli/ -run 'TestReviewTokenFlagIsRejectedWithTheFix' -count=1` and `go test ./plugin/... -count=1`
- Red step: `TestReviewTokenFlagIsRejectedWithTheFix` fails: the flag is accepted.
- Depends on: CS13 (kept, behaviour: the flag may go only once the review job needs no account); CS12 (kept: one file, the generated `cli-reference.md`).
- Commit: `model-credentials-routing CS15: `fugaro review --token` goes`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

## Slice 5: `migration`, and the first integration

### MG1: the migration, as a function

- Module: `migration`.
- Acceptance: One pure function takes an installation's config and each repository's file and returns the table, the pools and the admissions that keep every run where it was: `oauth` becomes the account's pool, `api-key` the Anthropic key, `cascade` the shared pool then the key where `fallback: auto` was set, `vertex` a chain naming Vertex; `routing.claude.via` becomes the Claude entry (D3173); every ownerless account lands in one shared pool, same members and order and the rule `failover`, which is what the cascade does today (D3183), and a lone account also gets a pool of one (D3176); per-stage accounts and a grade's access (`model_grades.<grade>.access`) are folded into one pool (D3171, F46); each repository is admitted only to the keys it used, and each account keeps its own admissions untouched (D3182). What it cannot write as a table RT3 accepts (a Claude model through a provider before Check 50; a Vertex model with no checked-in spelling) it leaves on the release-1 bridge and returns by name (D3196). The function also returns the list of pools that are inert today and will rotate once migrated (D3177). A table-driven test covers every value of `agent.auth` and `via`, two repositories that differ, and two repositories admitted to two different accounts that land in the one shared pool.
- Files: `internal/routing/migrate.go` (new), `internal/routing/migrate_test.go` (new), `internal/routing/testdata/migrate` (new).
- Interfaces: Consumes C1 and C2's types. Produces `routing.Migrate`, which MG2 calls.
- Tests: `TestMigrate` (one case per auth mode and per `via`), `TestMigrateMovesNoRepository`.
- Command: `go test ./internal/routing/ -run 'TestMigrate' -count=1`
- Red step: `TestMigrate/oauth` fails before the function exists.
- Depends on: C1 (the contract's table types); C2 (the contract's pool types).
- Commit: `model-credentials-routing MG1: the migration, as a function`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### MG2: `fugaro update` migrates, and says what changes first

- Module: `migration`.
- Acceptance: `fugaro update` on an installation at the previous version writes the table, pools and admissions `routing.Migrate` returns into the installation's config, publishes them, and prints before any write one line per repository saying what its runs go out on and that nothing moves, and one line per pool that starts rotating among its tokens now that the spread switch is gone (D3177); and one line for a project whose `fallback` was `auto`, saying a run now pays on the key as soon as its subscriptions are exhausted instead of waiting up to the old threshold first (D3192); it names each entry left on the release-1 bridge (D3196) and still converges; it asks nothing, is safe with runs in flight, and a second run changes nothing; the `e2e` update test converges from a fixture of the previous version.
- Files: `internal/cli/update_routing_migrate.go` (new), `internal/cli/update_routing_migrate_test.go` (new), `internal/cli/update.go`, `internal/cli/init_craft_migrate.go`, `internal/e2e/update_test.go`.
- Interfaces: Consumes MG1's function, RT2's block, RT4's publisher and PL1's fields. Produces the migrated installation I1 proves.
- Tests: `TestUpdateMigratesToTheTable`, `TestUpdateSaysWhatChangesFirst`, `TestUpdateMigrationIsIdempotent`.
- Command: `go test ./internal/cli/ -run 'TestUpdateMigratesToTheTable|TestUpdateSaysWhatChangesFirst|TestUpdateMigrationIsIdempotent' -count=1`
- Red step: `TestUpdateMigratesToTheTable` fails: `update` writes no table.
- Depends on: MG1 (kept, behaviour: it writes what the function answers, and a stub's answer would be written to a real installation); RT4 (kept, behaviour: it publishes through RT4's publisher); PL1 (kept: it writes the pool fields PL1 adds).
- Gate: **Not launched before always-a-budget's I1 and the keys plan's KS12 have merged (D3071, kept).**
- Commit: `model-credentials-routing MG2: `fugaro update` migrates, and says what changes first`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### MG3: the old record is written beside the table for one release

- Module: `migration`.
- Acceptance: For release 1 `fugaro update` keeps writing `creds/repoauth/<slug>` as today, so a run that bootstrapped on the release behind still resolves; the service reads the table first and the old record only for a run whose image predates the table; the old record is derived from the table on every write of either (`update`, `routing set`, an admission change), so it gains no source the table does not have, and the entries D3196 leaves on it — the ones the checks refuse — stay there, so it is wider than the table by exactly those and never wider than the repository's own pre-migration access; the walk reads the record only for a run whose image predates the table, so a call of this release reaches the old path never, whatever header it sends; the update's in-flight line counts such runs.
- Files: `internal/cli/update_repoauth.go`, `internal/cli/update_repoauth_table_test.go` (new), `internal/credsvc/legacy_auth.go` (new), `internal/credsvc/legacy_auth_test.go` (new), `internal/cli/update_in_flight.go`.
- Interfaces: Consumes C1. Produces the bridge X4 removes.
- Tests: `TestTheOldRecordIsStillWritten`, `TestAnOlderRunStillResolves`, `TestTheOldRecordGainsNoSourceTheTableLacks`.
- Command: `go test ./internal/cli/ -run 'TestTheOldRecordIsStillWritten|TestTheOldRecordGainsNoSourceTheTableLacks' -count=1` and `go test ./internal/credsvc/ -run 'TestAnOlderRunStillResolves' -count=1`
- Red step: `TestAnOlderRunStillResolves` fails once CS1 is in and before this bridge exists; before CS1 it is written to fail on the missing file.
- Depends on: C1 (the contract's names).
- Commit: `model-credentials-routing MG3: the old record is written beside the table for one release`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### MG4: the account and craft commands stop writing access

- Module: `migration`.
- Acceptance: `fugaro accounts switch`, `alias`, `craft`, `grade` and `cascade` no longer write a craft's access or a repository's auth mode: each either writes the table or pool it now means, or is refused with the fix naming `fugaro routing set` or `fugaro accounts pool`; none asks a question.
- Files: `internal/cli/accounts_switch.go`, `internal/cli/accounts_alias.go`, `internal/cli/accounts_craft.go`, `internal/cli/accounts_cascade.go`, `internal/cli/accounts_routing_test.go` (new), `website/content/cli-reference.md`.
- Interfaces: Consumes RT5's command and RT2's block. Produces nothing a task calls.
- Tests: `TestAccountsCommandsWriteNoAccess` (one sub-test per command).
- Command: `go test ./internal/cli/ -run 'TestAccountsCommandsWriteNoAccess' -count=1`
- Red step: `TestAccountsCommandsWriteNoAccess/switch` fails: it writes `agent.auth`.
- Depends on: RT5 (kept, behaviour: its refusals name a command that must exist, and the plugin's tests check the name); PL9 (kept: one file, the generated `cli-reference.md`).
- Commit: `model-credentials-routing MG4: the account and craft commands stop writing access`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### MG5: the settings registry and the schemas

- Module: `migration`.
- Acceptance: The settings registry and `schemas/settings.schema.json` mark the installation's `routing.claude`, `credentials.fallback`, `credentials.wait_threshold` and `subscription_spread` as retired. The repository file's and the layer's `agent.auth`, craft access, grade access and `agent.oauth_token` are already retired rows in `internal/config/scope.go`; their notes, which send the reader to a craft's access, are reworded to send them to the project's routing table and pools. `schemas/fugaro.schema.json` and `schemas/project-layer.schema.json` still accept the retired keys for release 1 and describe them as retired.
- Files: `internal/settings/registry.go`, `schemas/settings.schema.json`, `schemas/fugaro.schema.json`, `schemas/project-layer.schema.json`, `internal/config/scope.go`.
- Interfaces: Consumes RT4's and PL1's struct members. Produces the registry PR9 extends.
- Tests: `TestRegistryCoversEveryConfigField`, `TestRetiredNotesNameTheRoutingTable`.
- Command: `go test ./internal/settings/ -run 'TestRegistryCoversEveryConfigField' -count=1` and `go test ./internal/config/ -run 'TestRetiredNotesNameTheRoutingTable' -count=1`
- Red step: `TestRetiredNotesNameTheRoutingTable` fails: the notes name a craft's access.
- Depends on: PL1 (kept: two files, `registry.go` and `settings.schema.json`).
- Commit: `model-credentials-routing MG5: the settings registry and the schemas`
- Reviewer tier: standard.

### MG6: the help texts and the CLI reference

- Module: `migration`.
- Acceptance: No help text, budget preview or run statistic speaks of an oauth or an api-key repository; each says which source a model goes out on; the generated CLI reference is regenerated; the Docs tests pass.
- Files: `internal/cli/budget_preview.go`, `internal/cli/runs_stats.go`, `internal/cli/run.go`, `internal/cli/models_use.go`, `website/content/cli-reference.md`.
- Interfaces: Consumes CS10 and RT6. Produces nothing a task calls.
- Tests: `TestNoHelpTextNamesARepositoryAuthMode`, the Docs tests.
- Command: `go test ./internal/cli/ -run 'TestNoHelpTextNamesARepositoryAuthMode|Docs' -count=1`
- Red step: `TestNoHelpTextNamesARepositoryAuthMode` fails on today's texts.
- Depends on: MG4 (kept: one file, the generated `cli-reference.md`, and `run.go` through CS12); RT6 (kept: one file, `models_use.go`).
- Commit: `model-credentials-routing MG6: the help texts and the CLI reference`
- Reviewer tier: standard.

### I1: integration: the chain serves a migrated installation

- Module: `chain-serving`.
- Acceptance: The stubs of C1 and C2 are deleted; an end-to-end test migrates a fixture installation of the previous version with `update`, launches a run against the fakes and proves: the coder's calls go out on the first source of its model's chain, an exhausted pool moves the next call to the next source with the step flagged and the paid money counted, a repository not admitted to a source skips it, a person with no pool is served by the next source, and the agent's environment holds no credential; a guard test finds no reader of `agent.auth` or a craft's access on the serving path outside the release-1 bridge; the console fixtures of VS1 and VS2 are replaced by the output this test records, and their tests still pass.
- Files: `internal/routing/resolve_stub.go` (deleted), `internal/credsvc/chain_stub.go` (deleted), `internal/credsvc/launcherpool_stub.go` (deleted), `internal/pricing/spelling_stub.go` (deleted), `internal/creds/poolrule_stub.go` (deleted), `internal/cli/chain_end_to_end_test.go` (new), `internal/config/retired_readers_guard_test.go` (new).
- Interfaces: Consumes every task of slices 2 to 5. Produces release 1's proof.
- Tests: `TestChainEndToEnd`, `TestNoReaderOfTheRetiredKeys`.
- Command: `go test ./internal/cli/ -run 'TestChainEndToEnd' -count=1` and `go test ./internal/config/ -run 'TestNoReaderOfTheRetiredKeys' -count=1`
- Red step: `TestChainEndToEnd` fails while any stub answers.
- Depends on: A1, A2, RT1, RT2, RT3, RT4, RT5, RT6, RT7, PL1, PL2, PL3, PL4, PL5, PL6, CS1, CS2, CS3, CS4, CS5, CS6, CS7, CS8, CS9, CS10, CS11, MG1, MG2, MG3, MG4, MG5, MG6, VS1, VS2, VS4, PL9, CS12, CS13, CS14, CS15, PL10 (the sides it wires).
- Commit: `model-credentials-routing I1: integration: the chain serves a migrated installation`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

## Slice 5b: Gemini on Vertex (D3193), after release 1's integration

### GV1: Gemini on Vertex: the request

- Module: `chain-serving`.
- Acceptance: For a non-Claude model a chain routes to Vertex, the service translates the Anthropic-shaped request the agent's harness sends (system, messages, tools, tool results, the sampling members it uses) into Vertex's Google-publisher request for that model, on the service's own identity; a member it cannot translate faithfully (an image block, an unknown tool form) is refused with a typed reason and never dropped; the task's pull request cites the provider's current documentation for every field it maps, since the wire is not in this repository (U8). Every new refusal writes exactly one audit line and no value.
- Files: `internal/credsvc/vertex_gemini.go` (new), `internal/credsvc/vertex_gemini_test.go` (new), `internal/credsvc/testdata/vertex_gemini` (new).
- Interfaces: Consumes CS4's Vertex caller. Produces the request half GV2 and GV4 build on.
- Tests: `TestGeminiRequestTranslation` (golden pairs), `TestAnUntranslatableMemberIsRefused`.
- Command: `go test ./internal/credsvc/ -run 'TestGeminiRequestTranslation|TestAnUntranslatableMemberIsRefused' -count=1`
- Red step: `TestGeminiRequestTranslation` fails before the file exists.
- Depends on: CS4 (kept, behaviour: it extends the caller and the identity CS4 builds).
- Commit: `model-credentials-routing GV1: Gemini on Vertex: the request`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### GV2: Gemini on Vertex: the answer and the stream

- Module: `chain-serving`.
- Acceptance: The service translates Vertex's answer for a Gemini model, streamed or whole, back into the Anthropic-shaped answer the agent's harness reads: text, tool calls, the stop reason and the usage figures the gateway prices by; a 429 or a quota error marks the source so the chain moves on; an answer it cannot translate ends the call with a typed error and never a half-translated stream.
- Files: `internal/credsvc/vertex_gemini_stream.go` (new), `internal/credsvc/vertex_gemini_stream_test.go` (new), `internal/credsvc/testdata/vertex_gemini_stream` (new).
- Interfaces: Consumes GV1's request half. Produces the answer half GV4 proves.
- Tests: `TestGeminiAnswerTranslation`, `TestGeminiStreamTranslation`, `TestAGeminiLimitMovesOn`.
- Command: `go test ./internal/credsvc/ -run 'TestGeminiAnswerTranslation|TestGeminiStreamTranslation|TestAGeminiLimitMovesOn' -count=1`
- Red step: `TestGeminiStreamTranslation` fails before the file exists.
- Depends on: GV1 (kept, behaviour: an answer is translated for the request GV1 built, and the two share the tool-call mapping).
- Commit: `model-credentials-routing GV2: Gemini on Vertex: the answer and the stream`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### GV3: Gemini on Vertex: spelling and price

- Module: `chain-serving`.
- Acceptance: The checked-in map gains the Vertex spelling of each Gemini model the catalog lists, and the price table gains their Vertex prices with the date they were read; a Gemini model with no price is refused in every budget mode, as any unpriced model is (always-a-budget D3020).
- Files: `internal/pricing/canonical.go`, `internal/pricing/canonical_test.go`, `internal/pricing/vertex_gemini.go` (new), `internal/pricing/vertex_gemini_test.go` (new).
- Interfaces: Consumes CS3's `SpellingFor`. Produces the spelling and the price GV4 and the gateway's ledger read.
- Tests: `TestSpellingForGeminiOnVertex`, `TestAnUnpricedGeminiModelIsRefused`.
- Command: `go test ./internal/pricing/ -run 'TestSpellingForGeminiOnVertex|TestAnUnpricedGeminiModelIsRefused' -count=1`
- Red step: `TestSpellingForGeminiOnVertex` fails: the map has no such row.
- Depends on: CS3 (kept: two files, `canonical.go` and its test).
- Commit: `model-credentials-routing GV3: Gemini on Vertex: spelling and price`
- Reviewer tier: standard.

### GV4: Gemini on Vertex: the checks accept it, end to end

- Module: `chain-serving`.
- Acceptance: `routing.Problems` accepts a chain that routes a mapped, priced Gemini model to Vertex and still refuses any other non-Claude model there with the reason; an end-to-end test against a fake Vertex runs a coder stage on a Gemini model through the chain, with a tool call and a streamed answer, priced and settled against the cap; Check 886's script is added with its test.
- Files: `internal/routing/checks.go`, `internal/routing/checks_gemini_test.go` (new), `internal/cli/gemini_vertex_end_to_end_test.go` (new), `scripts/live-checks/check-886-gemini-on-vertex.sh` (new), `scripts/live-checks/check-886-gemini-on-vertex_test.sh` (new).
- Interfaces: Consumes GV1, GV2, GV3 and the wired chain.
- Tests: `TestProblemsAcceptGeminiOnVertex`, `TestGeminiOnVertexEndToEnd`; the script's test.
- Command: `go test ./internal/routing/ -run 'TestProblemsAcceptGeminiOnVertex' -count=1`, `go test ./internal/cli/ -run 'TestGeminiOnVertexEndToEnd' -count=1` and `bash scripts/live-checks/check-886-gemini-on-vertex_test.sh`
- Red step: `TestProblemsAcceptGeminiOnVertex` fails: the checks refuse every non-Claude model on Vertex.
- Depends on: GV2 (kept, behaviour: it runs the whole translation); GV3 (kept, behaviour: it needs the spelling and the price); I1 (kept, behaviour: the test runs through the wired chain, and `checks.go` is RT3's).
- Commit: `model-credentials-routing GV4: Gemini on Vertex: the checks accept it, end to end`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).
- Owner-run: The owner runs `bash scripts/live-checks/check-886-gemini-on-vertex.sh` himself after the release that carries GV4. No agent runs it.

## Slice 6: `pause-resume`, and its integration

### PR1: the status word `paused`

- Module: `pause-resume`.
- Acceptance: `paused` is a status in `runstore.Statuses`, the result schema and the budget rules' outcome words; it is terminal for the execution; `fugaro update` migrates the rules in place; no existing word changes meaning (R15). The rules change ships with a test that an installation at the previous rules version converges.
- Files: `internal/runstore/runstore.go`, `schemas/result.schema.json`, `internal/budget/registry.go`, `internal/budget/rules/rules.json.tmpl`, `internal/budget/rules/marker.go`, `internal/budget/rules/testdata/rules.golden.json`.
- Interfaces: Consumes C3's constant. Produces the word every reader of a status meets.
- Tests: `TestPausedIsAStatus`, `TestTheRulesAdmitPaused`, `TestRulesConvergeWithPaused`.
- Command: `go test ./internal/runstore/ -run 'TestPausedIsAStatus' -count=1` and `go test ./internal/budget/ -run 'TestTheRulesAdmitPaused|TestRulesConvergeWithPaused' -count=1`
- Red step: `TestTheRulesAdmitPaused` fails: the rules refuse an unknown word.
- Depends on: C3 (the contract's constant); CS8 (kept: two files, `runstore.go` and `result.schema.json`); CS9 (kept: `registry.go` and the rules files).
- Commit: `model-credentials-routing PR1: the status word `paused``
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PR2: the run suspends itself

- Module: `pause-resume`.
- Acceptance: When the service answers that every source is exhausted, the gateway decodes the refusal's new members (`action`, `resume_at`) and hands them to the runner, which follows the action and decides nothing about time itself: `wait` is the in-job wait as today (D3184); `halt` ends the run with `HaltNoCredential` and the reset time in the reason; `pause` saves the session, pushes the work in progress as a checkpoint, writes `pause.json`, tells the service it has saved (`/pause`), ends `paused` and exits with its lease released (R12, R18). A hotfix run treats `wait` and `pause` as `halt` (R10). No proxy call is held open.
- Files: `internal/runner/suspend.go` (new), `internal/runner/suspend_test.go` (new), `internal/runner/creds_wait.go`, `internal/runner/halt.go`, `internal/runner/runner.go`, `internal/runner/gateway.go`, `internal/gateway/service.go`.
- Interfaces: Consumes C3's record and answer members. Produces the suspended run the service records and PR4 resumes.
- Tests: `TestSuspendFollowsTheAnswer` (one sub-test per action, and hotfix), `TestSuspendSavesBeforeItExits`, `TestSuspendReleasesTheLease`.
- Command: `go test ./internal/runner/ -run 'TestSuspend' -count=1`
- Red step: `TestSuspendFollowsTheAnswer/pause` fails against the stub, which waits in the job.
- Depends on: C3 (the contract's record); CS6 (kept: one file, `runner.go`); CS2 (kept: one file, `gateway/service.go`).
- Commit: `model-credentials-routing PR2: the run suspends itself`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PR3: the service decides the pause and records it

- Module: `pause-resume`.
- Acceptance: The service chooses the exhausted answer's action from its own knowledge: `wait` when the reset is nearer than `pause.wait_in_job`; `halt` when the installation has no resident orchestrator, when the reset is beyond `pause.max_pause`, or when the chain's total paused time would pass `pause.max_pause` (D3186, D3187, D3190); otherwise `pause`, with a resume time never nearer than `pause.min_pause` (D3190 as amended). It honours a run's `/pause` only if it last answered that run `pause`, and once per answer; it then writes `paused/<slug>/<run>` with the resume time it computed and the launcher and named pool from its own record of the run's grant, never from the job (R8). The node is written by the service's identity alone (the database's root is closed to a run's token already); every launcher can read it, so it is display data to them and an input only to the service and the orchestrator. A slip that names `resumes` is granted only to the orchestrator's class, only for the same repository and chain, once, not before the resume time, and never for a node marked cancelled; the resumed run draws on the original launcher's pool (D3191). Every new refusal writes exactly one audit line and no value.
- Files: `internal/credsvc/pause.go` (new), `internal/credsvc/pause_test.go` (new), `internal/credsvc/slip.go`, `internal/creds/slips.go`, `internal/credsvc/cascade.go`, `internal/credsvc/wire.go`, `internal/credsvc/server.go`.
- Interfaces: Consumes C3's node and members and PR9's settings. Produces the node PR4 reads and the binding its launch meets.
- Tests: `TestTheActionIsTheServices` (one sub-test per branch), `TestAPauseNobodyAnsweredIsRefused`, `TestThePausedLauncherIsTheServicesRecord`, `TestAResumedRunDrawsOnTheOriginalLaunchersPool`, `TestOnlyTheOrchestratorMayResume`, `TestAResumeIsSingleUse`, `TestNoResumeBeforeItsTime`, `TestNoResumeOfACancelledPause`, `TestTotalPausedTimeIsBounded`, `TestAPauseIsNeverShorterThanTheMinimum`.
- Command: `go test ./internal/credsvc/ -run 'TestTheActionIsTheServices|TestAPauseNobodyAnsweredIsRefused|TestThePausedLauncher|TestAResumedRun|TestOnlyTheOrchestratorMayResume|TestAResumeIsSingleUse|TestNoResume|TestTotalPausedTimeIsBounded|TestAPauseIsNeverShorterThanTheMinimum' -count=1`
- Red step: `TestAResumedRunDrawsOnTheOriginalLaunchersPool` fails: a reissued run has the orchestrator as its launcher.
- Depends on: C3 (the contract's node and members); CS1 (kept: one file, `cascade.go`); CS14 (kept: two files, `slip.go` and `slips.go`); PL6 (kept, behaviour: a resumed run of a service account's launch resolves through PL6's lookup); PL9 (kept: one file, `server.go`); PR9 (kept, behaviour: it reads the settings from the struct PR9 adds).
- Commit: `model-credentials-routing PR3: the service decides the pause and records it`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PR4: the resident orchestrator resumes what is due

- Module: `pause-resume`.
- Acceptance: On each tick the resident orchestrator reads the nodes under `paused/` whose resume time has passed and reissues each once as a run that continues the paused one (the same chain, `resumes` naming it); the issuer serves it an identity for a resume only where the node is one the credentials service wrote (D3189, the one case added to D3136); a node already resumed or marked cancelled is not reissued, and neither is one whose run has not ended with the status `paused` (a `/pause` posted by an agent whose runner runs on resumes nothing); two ticks never resume one node twice.
- Files: `internal/orchestrator/resume.go` (new), `internal/orchestrator/resume_test.go` (new), `internal/orchestrator/tick.go`, `internal/orchestrator/resident_launch.go`, `internal/orchestrator/intent.go`, `internal/credsvc/identity_ticket.go`.
- Interfaces: Consumes C3's record and node. Produces the resumed launch PR5's run receives.
- Tests: `TestADuePauseIsResumedOnce`, `TestACancelledPauseIsNotResumed`, `TestARunThatDidNotEndPausedIsNotResumed`, `TestTheIssuerServesOnlyAServiceWrittenPause`.
- Command: `go test ./internal/orchestrator/ -run 'TestADuePauseIsResumedOnce|TestACancelledPauseIsNotResumed|TestARunThatDidNotEndPausedIsNotResumed' -count=1` and `go test ./internal/credsvc/ -run 'TestTheIssuerServesOnlyAServiceWrittenPause' -count=1`
- Red step: `TestADuePauseIsResumedOnce` fails against the stub, which resumes nothing.
- Depends on: C3 (the contract's record).
- Gate: **Not launched before launcher-does-not-mint's I1 has merged: it extends the issuer that task wires.**
- Commit: `model-credentials-routing PR4: the resident orchestrator resumes what is due`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PR5: the resumed run picks up where the paused one stopped

- Module: `pause-resume`.
- Acceptance: A run whose task names `resumes` reads the paused run's `pause.json`, checks out the checkpoint, restores the saved session and enters the stage the paused run was in, with the chain's remaining budget (D1045 to D1047, unchanged); a paused record whose format version this image does not read, a missing session or a head that is not the checkpoint ends the run with a named outcome and never starts the stage afresh (R18). `pause.json` is written inside the job, like the run record a follow-up already trusts; that trust is unchanged and is said in R23.
- Files: `internal/runner/session.go`, `internal/runner/resume_test.go` (new), `internal/task/task.go`, `schemas/task.schema.json`, `internal/orchestrator/launch.go`.
- Interfaces: Consumes C3's record and `resumes`. Produces the resumed run I2 proves.
- Tests: `TestAResumedRunRestoresTheSession`, `TestAResumeOfAnUnreadableRecordIsNamed`.
- Command: `go test ./internal/runner/ -run 'TestAResumedRunRestoresTheSession|TestAResumeOfAnUnreadableRecordIsNamed' -count=1`
- Red step: `TestAResumedRunRestoresTheSession` fails: `task.Parse` refuses the unknown member.
- Depends on: C3 (the contract's record and member).
- Commit: `model-credentials-routing PR5: the resumed run picks up where the paused one stopped`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PR6: `fugaro update` with runs paused

- Module: `pause-resume`.
- Acceptance: `fugaro update` rewrites nothing a paused run holds (its record, its node, its saved session, its chain's ledger), says in its one in-flight line how many runs are paused and that none is touched, and `TestUpdateLeavesARunInFlightRunning` gains a paused run that resumes after the update; the design document says so step by step.
- Files: `internal/cli/update_in_flight.go`, `internal/cli/update_in_flight_test.go`, `internal/cli/update_converge_test.go`, `docs/design/update-in-flight.md`.
- Interfaces: Consumes C3's node. Produces nothing a task calls.
- Tests: `TestUpdateLeavesARunInFlightRunning`, `TestUpdateCountsPausedRuns`.
- Command: `go test ./internal/cli/ -run 'TestUpdateLeavesARunInFlightRunning|TestUpdateCountsPausedRuns' -count=1`
- Red step: `TestUpdateCountsPausedRuns` fails: the line knows nothing of paused runs.
- Depends on: C3 (the contract's node); MG3 (kept: one line each in `update_in_flight.go`).
- Commit: `model-credentials-routing PR6: `fugaro update` with runs paused`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PR7: `watch` and `runs ls` show a paused run

- Module: `pause-resume`.
- Acceptance: `fugaro watch` and `fugaro runs ls` show a paused run as paused with its resume time and the run that resumed it, in text and in `--json` (new members, none retyped); `fugaro watch` returns when its run pauses and does not block;
- Files: `internal/watch/render.go`, `internal/watch/json.go`, `internal/cli/runs.go`, `internal/cli/runs_show.go`, `internal/cli/runs_paused_test.go` (new).
- Interfaces: Consumes C3's node and record. Produces the JSON the console reads (VS3).
- Tests: `TestWatchShowsAPausedRun`, `TestRunsLsShowsThePauseAndItsResume`.
- Command: `go test ./internal/cli/ -run 'TestRunsLsShowsThePauseAndItsResume' -count=1` and `go test ./internal/watch/ -run 'TestWatchShowsAPausedRun' -count=1`
- Red step: `TestWatchShowsAPausedRun` fails: a paused run has no entry and is not listed.
- Depends on: C3 (the contract's node); CS9 (kept: two files, `render.go` and `json.go`).
- Commit: `model-credentials-routing PR7: `watch` and `runs ls` show a paused run`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PR8: the notice kind for a paused run

- Module: `pause-resume`.
- Acceptance: The orchestrator's notices gain one kind, a run paused until a time, and one for a pause that ended as halted; the log sink prints both; no new channel is built (R13).
- Files: `internal/orchestrator/notify.go`, `internal/orchestrator/runview.go`, `internal/orchestrator/notify_paused_test.go` (new).
- Interfaces: Consumes C3's node. Produces the kind the mobile push plan delivers when it lands.
- Tests: `TestNoticeForAPausedRun`.
- Command: `go test ./internal/orchestrator/ -run 'TestNoticeForAPausedRun' -count=1`
- Red step: `TestNoticeForAPausedRun` fails: no such kind.
- Depends on: C3 (the contract's node).
- Commit: `model-credentials-routing PR8: the notice kind for a paused run`
- Reviewer tier: standard.

### PR9: the pause settings: the in-job floor, the minimum and the maximum

- Module: `pause-resume`.
- Acceptance: The policy gains one new top-level key, `pause`, with `wait_in_job` (default 5 minutes), `min_pause` (default 30 minutes; a pause is never shorter, D3190 as amended) and `max_pause` (default 6 hours, which also bounds a chain's total paused time), validated (`wait_in_job` under `min_pause` under `max_pause`), written by `fugaro settings set` and published by `update`; the `credentials` block is not touched, so a release-behind reader still decodes the policy, with a test; `credentials.wait_threshold` is still decoded and no longer read once an installation is migrated.
- Files: `internal/projectpolicy/policy.go`, `internal/settings/registry.go`, `schemas/settings.schema.json`, `internal/cli/settings_cloud.go`, `internal/cli/init_policy_publish.go`.
- Interfaces: Consumes nothing of the contract. Produces the three settings PR3 reads.
- Tests: `TestPauseSettingsDefaults`, `TestPauseSettingsAreOrdered`, `TestAReleaseBehindReaderToleratesPause`.
- Command: `go test ./internal/projectpolicy/ -run 'TestPauseSettings|TestAReleaseBehindReaderToleratesPause' -count=1`
- Red step: `TestPauseSettingsDefaults` fails: the policy decodes strictly and has no such member.
- Depends on: RT4 (kept: two files, `policy.go` and `init_policy_publish.go`); MG5 (kept: two files, `registry.go` and `settings.schema.json`).
- Commit: `model-credentials-routing PR9: the pause settings: the in-job floor, the minimum and the maximum`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PR10: cancel a paused run

- Module: `pause-resume`.
- Acceptance: `fugaro cancel` of a paused run asks a route of the service to mark its node cancelled, so it is never resumed; the route serves the run's launcher and nobody else (D3197: an admin does not cancel another person's pause in this plan, since the service has no admin surface, D143); a cancelled pause shows as cancelled in `runs ls`. Every new refusal writes exactly one audit line and no value.
- Files: `internal/cli/cancel.go`, `internal/cli/cancel_paused_test.go` (new), `internal/credsvc/pause_cancel.go` (new), `internal/credsvc/pause_cancel_test.go` (new), `internal/credsvc/server.go`.
- Interfaces: Consumes C3's node and its rule for who may cancel. Produces the mark PR4 honours.
- Tests: `TestCancelOfAPausedRun`, `TestOnlyTheLauncherCancelsAPause`.
- Command: `go test ./internal/cli/ -run 'TestCancelOfAPausedRun' -count=1` and `go test ./internal/credsvc/ -run 'TestOnlyTheLauncherCancelsAPause' -count=1`
- Red step: `TestCancelOfAPausedRun` fails: there is no execution to cancel and the command reports nothing to do.
- Depends on: C3 (the contract's node and rule); PR3 (kept: one file, `server.go`).
- Commit: `model-credentials-routing PR10: cancel a paused run`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PR11: every reader of a status meets `paused`

- Module: `pause-resume`.
- Acceptance: The summary schema's status set, the run view, the user statistics, the budget rollup, the digest and `watch`'s finished list each handle `paused`: it is counted apart from a failure and a success, and a paused run is never listed as finished. About three dozen files switch on a status; this task changes the six that would miscount and adds one guard test that walks the tree and fails on any switch over `runstore.Status` that neither names `paused` nor has a default, so the rest are found by the test and fixed where it points.
- Files: `schemas/summary.schema.json`, `internal/runview/runview.go`, `internal/userstats/userstats.go`, `internal/budget/rollup.go`, `internal/watch/finished.go`, `internal/digest/digest.go`.
- Interfaces: Consumes C3's constant and PR1's set. Produces nothing a task calls.
- Tests: `TestEveryStatusReaderHandlesPaused` (one sub-test per reader), `TestEverySwitchOverStatusKnowsPaused`.
- Command: `go test ./internal/runview/ -run 'TestEveryStatusReaderHandlesPaused' -count=1`, `go test ./internal/userstats/ -run 'TestEveryStatusReaderHandlesPaused' -count=1`, `go test ./internal/budget/ -run 'TestRollupCountsPausedApart' -count=1` and `go test ./internal/digest/ -run 'TestDigestCountsPausedApart' -count=1`
- Red step: `TestEveryStatusReaderHandlesPaused/runview` fails: the word is unknown to it.
- Depends on: PR1 (kept, behaviour: the readers switch over the set PR1 extends, and their exhaustiveness tests read it).
- Commit: `model-credentials-routing PR11: every reader of a status meets `paused``
- Reviewer tier: standard.

### PR12: a paused chain is in flight, and its pull request is `paused`

- Module: `pause-resume`.
- Acceptance: Between a pause and its resume the chain is in flight to every guard that asks: a duplicate launch and a follow-up are refused as they are for a running run, and the orchestrator's own view counts it; a cancelled pause is not in flight. The pull request is in a fifth state, `paused` (D3195): `runs ls --pr`, `watch` and their `--json` say so with the resume time, as a new value of the state member beside live, ready, stalled and orphan, none of which changes meaning.
- Files: `internal/cli/runs_prstate.go`, `internal/cli/run_duplicate.go`, `internal/cli/followup.go`, `internal/cli/runs_live.go`, `internal/cli/orchestrator_run.go`, `internal/cli/paused_in_flight_test.go` (new).
- Interfaces: Consumes C3's node. Produces nothing a task calls.
- Tests: `TestAPausedChainIsInFlight` (one sub-test per guard), `TestACancelledPauseIsNotInFlight`, `TestThePullRequestStateIsPaused`.
- Command: `go test ./internal/cli/ -run 'TestAPausedChainIsInFlight|TestACancelledPauseIsNotInFlight|TestThePullRequestStateIsPaused' -count=1`
- Red step: `TestAPausedChainIsInFlight/duplicate` fails: the second launch is admitted.
- Depends on: C3 (the contract's node).
- Commit: `model-credentials-routing PR12: a paused chain is in flight, and its pull request is `paused``
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PR13: the fifth pull-request word, where people and agents read it

- Module: `pause-resume`.
- Acceptance: `CLAUDE.md`'s paragraph of pull-request words, the rules at the top of `docs/status.md`, and the `landing` and `working` skills name five states and define `paused` the same way: a run suspended itself on this pull request and the orchestrator will resume it at a named time; it needs nothing from a person, is not landed and takes no follow-up while it stands. No row of `docs/status.md` is touched.
- Files: `CLAUDE.md`, `docs/status.md`, `plugin/skills/landing/SKILL.md`, `plugin/skills/working/SKILL.md`.
- Interfaces: Consumes PR12's state. Produces nothing a task calls.
- Tests: The Docs tests; the plugin's tests.
- Command: `go test ./internal/cli/ -run 'Docs' -count=1` and `go test ./plugin/... -count=1`
- Red step: None: a documents task. `git grep -n 'paused' CLAUDE.md plugin/skills/landing/SKILL.md` finds the definition in each.
- Depends on: PR12 (kept, behaviour: the documents describe the state PR12 builds); CS12 (kept: one file, `working/SKILL.md`).
- Commit: `model-credentials-routing PR13: the fifth pull-request word, where people and agents read it`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### I2: integration: a run pauses and is resumed

- Module: `pause-resume`.
- Acceptance: C3's stubs are deleted; an end-to-end test against the fakes exhausts every source of a chain with a reset an hour away, sees the run end `paused` with its record and node written and its lease released, advances the clock, sees the orchestrator reissue it once, and sees the resumed run restore the session, draw on the original launcher's pool and finish; the same test with a hotfix run, with a reset past the maximum, with a chain whose paused time is used up and with no orchestrator each halts at once; a cancelled pause is not resumed; the fixtures of VS3 and VS4 are replaced by what this test records, and their tests still pass.
- Files: `internal/runner/suspend_stub.go` (deleted), `internal/orchestrator/resume_stub.go` (deleted), `internal/cli/pause_end_to_end_test.go` (new).
- Interfaces: Consumes every task of slice 6. Produces the proof of `pause-resume`.
- Tests: `TestPauseEndToEnd` (four sub-tests).
- Command: `go test ./internal/cli/ -run 'TestPauseEndToEnd' -count=1`
- Red step: `TestPauseEndToEnd/pause_and_resume` fails while a stub answers.
- Depends on: A3, PR1, PR2, PR3, PR4, PR5, PR6, PR7, PR8, PR9, VS3, VS4, PR10, PR11, PR12, PR13 (the sides it wires); I1 (kept, behaviour: the test exhausts a real chain, which exists only once I1 has wired it).
- Commit: `model-credentials-routing I2: integration: a run pauses and is resumed`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

## Slice 7: `visibility`

### VS1: the console's run screen: source, fallback, switches

- Module: `visibility`.
- Acceptance: The run screen names the source of each step (kind and name), shows a fallback badge on a step and on the run when `fallback` is true, lists the switches with their reason and cache cost, and shows the paid amount spent on fallback; a record without the new members draws as today; `auth` read as `-` draws no auth mode.
- Files: `console/src/shared/schema.ts`, `console/src/renderer/screens/run.ts`, `console/src/renderer/screens/live.ts`, `console/test/unit/run-source.test.ts` (new), `console/test/fixtures/run-fallback.json` (new).
- Interfaces: Consumes C1's member names, from a fixture. Produces the screen VS3 extends.
- Tests: `run-source.test.ts`.
- Command: `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 run-source`
- Red step: The test fails: the screen draws no source.
- Depends on: C1 (the contract's member names).
- Commit: `model-credentials-routing VS1: the console's run screen: source, fallback, switches`
- Reviewer tier: standard.

### VS2: the console's pools screen

- Module: `visibility`.
- Acceptance: A pools screen under Manage lists each pool the viewer may see with its owner, rule, members by name, each member's state and next reset, and headroom where a figure exists; it never shows a value and shows another person's personal pool only as a name with usage and headroom to an admin.
- Files: `console/src/renderer/manage/kinds/pools.ts` (new), `console/src/renderer/manage/kinds/read.ts`, `console/src/renderer/manage/kinds/people.ts`, `console/test/unit/manage-pools.test.ts` (new), `console/test/fixtures/accounts-pool-ls.json` (new).
- Interfaces: Consumes C2's JSON shape, from a fixture. Produces nothing a task calls.
- Tests: `manage-pools.test.ts`.
- Command: `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 manage-pools`
- Red step: The test fails: no such kind.
- Depends on: C2 (the contract's JSON shape).
- Commit: `model-credentials-routing VS2: the console's pools screen`
- Reviewer tier: standard.

### VS3: the console shows a paused run

- Module: `visibility`.
- Acceptance: The live and run screens show a paused run as paused with its resume time and a link to the run that resumed it; the status set gains `paused`; an unknown status still draws as unknown rather than failing. A unit test feeds `paused` to a pinned copy of the parser of the last released console (checked in under the test's fixtures): if that parser refuses the word, the pull request says so and the owner's answer to O8 decides whether the `--json` formats that carry a status are bumped.
- Files: `console/src/shared/schema.ts`, `console/src/renderer/screens/live.ts`, `console/src/renderer/screens/run.ts`, `console/test/unit/run-paused.test.ts` (new).
- Interfaces: Consumes C3's node and status, from a fixture. Produces nothing a task calls.
- Tests: `run-paused.test.ts`.
- Command: `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 run-paused`
- Red step: The test fails: the parser refuses the status.
- Depends on: C3 (the contract's node and status); VS1 (kept: three files shared with VS1).
- Commit: `model-credentials-routing VS3: the console shows a paused run`
- Reviewer tier: standard.

### VS4: the mobile feed: paused and fallback

- Module: `visibility`.
- Acceptance: The mobile feed's document shows a paused run with its resume time and a fallback mark on a run that fell back; a feed without the new members reads as today.
- Files: `mobile/src/feed/nodes.js`, `mobile/src/feed/document.js`, `mobile/src/feed/document.test.js`, `mobile/src/feed/fixtures/paused.json` (new).
- Interfaces: Consumes C1's and C3's member names, from fixtures. Produces nothing a task calls.
- Tests: `document.test.js`.
- Command: `cd mobile && npm test -- document`
- Red step: The test fails: the node is not read.
- Depends on: C1 (the contract's member names); C3 (the contract's node).
- Commit: `model-credentials-routing VS4: the mobile feed: paused and fallback`
- Reviewer tier: standard.

## Slice 8: documents, skills and checks

### DOC1: `SECURITY.md`, its page and the rules file

- Module: `chain-serving`.
- Acceptance: `SECURITY.md`, the website's security page and `.claude/rules/credentials-service.md` describe the tree as it is: the service owns the table and calls every source, Vertex included; the gateway holds no key; Vertex is capped as far as a key is, with the same residual risk of a hand-built call (D3003), and is no longer "without a dollar cap in any mode"; a personal pool serves only its owner's runs and a resume of one; the members of `admins` and `budget_admins`, who hold the database by IAM, can forge a slip, a designation or a paused node, as for any project state (accepted, D1943), and every launcher can read those nodes; the rule against a predefined role names the custom role CS11 added; the pinned sentences change in the document and its test together.
- Files: `SECURITY.md`, `website/content/security.md`, `.claude/rules/credentials-service.md`, `internal/cli/docs_security_test.go`.
- Interfaces: Consumes the tree after I1 and I2.
- Tests: The Docs tests; the website's check.
- Command: `go test ./internal/cli/ -run 'Docs' -count=1` and `cd website && npm run check`
- Red step: The Docs test that pins "Vertex has no dollar cap" fails once the sentence is corrected, until the test is changed with it.
- Depends on: I1 (kept: a security statement must be true of the tree it describes); I2 (kept, as I1).
- Gate: **Not live at the same time as the keys plan's KS13, which edits `SECURITY.md` too: whichever launches second waits for the first to merge.**
- Commit: `model-credentials-routing DOC1: `SECURITY.md`, its page and the rules file`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### DOC2: the credentials documents and the runbook

- Module: `chain-serving`.
- Acceptance: `docs/multi-model.md`, the credentials-service runbook and default-on document and the live checklist describe chains, pools and pausing, and no longer a repository's auth mode.
- Files: `docs/multi-model.md`, `docs/credentials-service/runbook.md`, `docs/credentials-service/default-on.md`, `docs/gcp-live-checklist.md`.
- Interfaces: Consumes the tree after I1 and I2.
- Tests: The Docs tests.
- Command: `go test ./internal/cli/ -run 'Docs' -count=1`
- Red step: None: a documents task; the pinned counts and anchors must still pass.
- Depends on: DOC1 (kept: its tests also read `SECURITY.md`); PL7 (kept: one file, `gcp-live-checklist.md`).
- Commit: `model-credentials-routing DOC2: the credentials documents and the runbook`
- Reviewer tier: standard.

### DOC3: the accounts documents, the README and the release notes

- Module: `migration`.
- Acceptance: `docs/project-claude-accounts.md`, `README.md` and the notes of release 1 (the file `scripts/release.sh` names for that version under `docs/releases/`) describe the table and pools; `### For operators` says "run `fugaro update`" and what it prints, and nothing else; the website's summary line is added.
- Files: `docs/project-claude-accounts.md`, `README.md`, `docs/releases/` (release 1's notes file), `website/data/release-summaries.yaml`.
- Interfaces: Consumes the tree after I1 and I2.
- Tests: The Docs tests; the website's check.
- Command: `go test ./internal/cli/ -run 'Docs' -count=1` and `cd website && npm run check`
- Red step: None: a documents task.
- Depends on: DOC2 (kept: its tests pin `docs/multi-model.md`).
- Commit: `model-credentials-routing DOC3: the accounts documents, the README and the release notes`
- Reviewer tier: standard.

### DOC4: the plugin's skills

- Module: `migration`.
- Acceptance: The `setup` and `working` skills say how a model's source is set (`fugaro routing`, `fugaro accounts pool`), what a paused run is and that it needs nothing from the user, what `--pool` does, and name no `--token` and no auth mode; the plugin's tests pass against the CLI.
- Files: `plugin/skills/setup/SKILL.md`, `plugin/skills/setup/reference/decisions.md`, `plugin/skills/setup/reference/transitions.md`, `plugin/skills/working/SKILL.md`, `plugin/skills/setup/reference/managed.md`.
- Interfaces: Consumes the CLI after I1 and I2.
- Tests: The plugin's tests.
- Command: `go test ./plugin/... -count=1`
- Red step: The plugin's test of documented flags fails while a skill names `--token`.
- Depends on: DOC3 (kept: the documents tasks run one after another over shared tests); PL5 (kept: one file, `decisions.md`); RT5 (kept: one file, `setup/SKILL.md`); CS12 (kept: one file, `working/SKILL.md`); PR13 (kept: one file, `working/SKILL.md`).
- Commit: `model-credentials-routing DOC4: the plugin's skills`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CK1: Checks 881 and 882, as scripts

- Module: `chain-serving`.
- Acceptance: One script per check, each run by the owner in his own session and printing no value, each with a test against a fake: 881 a live chain falls through from an exhausted pool to the key, flagged and counted; 882 the service serves each mapped model on Vertex, priced and settled against the run's cap, and a call made from inside a job straight to Vertex on an `enforce` project is refused.
- Files: `scripts/live-checks/check-881-chain-falls-through.sh` (new), `scripts/live-checks/check-881-chain-falls-through_test.sh` (new), `scripts/live-checks/check-882-vertex-capped.sh` (new), `scripts/live-checks/check-882-vertex-capped_test.sh` (new).
- Interfaces: Consumes nothing. Produces the owner's checks.
- Tests: Each script's `_test.sh` beside it.
- Command: `for t in scripts/live-checks/check-88[12]-*_test.sh; do bash "$t"; done`
- Red step: Each test fails before its script exists.
- Depends on: none.
- Commit: `model-credentials-routing CK1: Checks 881 and 882, as scripts`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).
- Owner-run: The owner runs each script himself after release 1 is installed through the real `fugaro update`. No agent runs one.

### CK2: Checks 883 to 885, as scripts

- Module: `pause-resume`.
- Acceptance: One script per check, each run by the owner in his own session and printing no value, each with a test against a fake: 883 a run pauses and the orchestrator resumes it on the original launcher's pool; 884 `fugaro update` migrates the dev installation and no repository's source moves; 885 after release 2 a retired key is rejected with the fix.
- Files: `scripts/live-checks/check-883-pause-resume.sh` (new), `scripts/live-checks/check-883-pause-resume_test.sh` (new), `scripts/live-checks/check-884-update-migrates.sh` (new), `scripts/live-checks/check-884-update-migrates_test.sh` (new), `scripts/live-checks/check-885-retired-keys.sh` (new), `scripts/live-checks/check-885-retired-keys_test.sh` (new).
- Interfaces: Consumes nothing. Produces the owner's checks.
- Tests: Each script's `_test.sh` beside it.
- Command: `for t in scripts/live-checks/check-88[345]-*_test.sh; do bash "$t"; done`
- Red step: Each test fails before its script exists.
- Depends on: none.
- Commit: `model-credentials-routing CK2: Checks 883 to 885, as scripts`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).
- Owner-run: The owner runs each script himself: 884 at release 1's install through the real `fugaro update`, 883 after it, 885 after release 2. No agent runs one.

## Slice 9: release 2, the removals

### X1: release 2: the subscription token's old path goes

- Module: `migration`.
- Acceptance: No job image receives a subscription token or a model key in its environment on any path; a launch on an image older than release 1 is refused by the image-age gate with the fix; the release-1 branch of the agent's environment is deleted.
- Files: `internal/agent/env.go`, `internal/runner/agentenv.go`, `internal/cli/launch_budget.go`, `internal/cli/recipes_skew.go`, `internal/agent/env_release2_test.go` (new).
- Interfaces: Consumes I1. Produces the gate X2 relies on.
- Tests: `TestNoCredentialInAnyEnvironment`, `TestAnOlderImageIsRefused`.
- Command: `go test ./internal/agent/ -run 'TestNoCredentialInAnyEnvironment' -count=1` and `go test ./internal/cli/ -run 'TestAnOlderImageIsRefused' -count=1`
- Red step: `TestNoCredentialInAnyEnvironment` fails on the release-1 branch.
- Depends on: I1 (kept, behaviour: it deletes what I1 proves unused); I2 (kept, behaviour: release 2 follows the whole of release 1, the pause included, and its tasks edit files the pause's tasks write).
- Gate: **Not launched before release 1 is cut and the keys plan's KS13 has merged.**
- Commit: `model-credentials-routing X1: release 2: the subscription token's old path goes`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### X2: release 2: the retired keys are rejected

- Module: `migration`.
- Acceptance: `agent.auth`, a craft's `access`, a grade's `access`, `agent.oauth_token` and the per-stage account keys in a `fugaro.yaml` or a project layer, and `routing.claude` in the installation's config, are rejected at `validate`, `update` and a default-branch launch with the fix; an older ref reads them as ignored; a subscription token in no pool is refused (D3176); no `fugaro.yaml` version bump (D3072, kept).
- Files: `internal/config/config.go`, `internal/config/layer.go`, `internal/config/validate.go`, `schemas/fugaro.schema.json`, `schemas/project-layer.schema.json`.
- Interfaces: Consumes X1's gate.
- Tests: `TestRetiredKeysAreRejectedWithTheFix`.
- Command: `go test ./internal/config/ -run 'TestRetiredKeysAreRejectedWithTheFix' -count=1`
- Red step: The test fails: the keys are accepted and named.
- Depends on: X1 (kept, behaviour: its gate must exist before a file may lose the key); MG5 (kept: two schema files).
- Commit: `model-credentials-routing X2: release 2: the retired keys are rejected`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### X3: release 2: the installation's old keys and the policy's old members

- Module: `migration`.
- Acceptance: `fugaro update` removes `subscription_spread`, `credentials.fallback`, `credentials.wait_threshold`, the craft credentials, the pools in their old shape (`craft_pools`) and the account-stage keys from the installation's config and the published policy, migrating the node in place; an installation at release 1 converges; the pool marker `cascade` is no longer read. The retired members stay decodable and are written empty, so a release-1 image, and a paused run that resumes on one, still reads the policy; taking them out of the format, which bumps its version, is left to a later release with a gate of its own (R25). The console's notes screen stops drawing `routing.claude`.
- Files: `internal/localcfg/localcfg.go`, `internal/projectpolicy/policy.go`, `internal/cli/update_routing_migrate.go`, `internal/config/craft.go`, `internal/cli/update_release2_test.go` (new), `console/src/renderer/screens/policynotes.ts`.
- Interfaces: Consumes X2.
- Tests: `TestUpdateDropsTheOldMembers`, `TestUpdateConvergesFromRelease1`.
- Command: `go test ./internal/cli/ -run 'TestUpdateDropsTheOldMembers|TestUpdateConvergesFromRelease1' -count=1`
- Red step: `TestUpdateDropsTheOldMembers` fails: the members are still written.
- Depends on: X2 (kept, behaviour: the installation's members may be emptied only once a file that names the old keys is rejected).
- Commit: `model-credentials-routing X3: release 2: the installation's old keys and the policy's old members`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### X4: release 2: the old record stops being written

- Module: `migration`.
- Acceptance: `fugaro update` no longer writes `creds/repoauth/<slug>` and deletes none that a run in flight may still read; the service's bridge for older runs is removed; the headers `X-Fugaro-Auth-Mode` and `X-Fugaro-Auth-Account` are no longer sent or read.
- Files: `internal/cli/update_repoauth.go`, `internal/credsvc/legacy_auth.go` (deleted), `internal/credsvc/wire.go`, `internal/gateway/service.go`, `internal/cli/update_in_flight.go`.
- Interfaces: Consumes X1's gate.
- Tests: `TestTheOldRecordIsNoLongerWritten`.
- Command: `go test ./internal/cli/ -run 'TestTheOldRecordIsNoLongerWritten' -count=1`
- Red step: The test fails: the record is written.
- Depends on: X1 (kept, behaviour: no image old enough to need the record may still launch); PR6 (kept: one file, `update_in_flight.go`).
- Commit: `model-credentials-routing X4: release 2: the old record stops being written`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### X5: release 2: `fugaro update` rewrites the published layer

- Module: `migration`.
- Acceptance: `fugaro update` removes the retired keys from the published project layer in place and offers their removal from a checkout's `fugaro.yaml` as a diff the user commits or does not; it asks nothing.
- Files: `internal/cli/config_publish.go`, `internal/cli/update_layer_routing.go` (new), `internal/cli/update_layer_routing_test.go` (new).
- Interfaces: Consumes I1.
- Tests: `TestUpdateRewritesThePublishedLayer`, `TestTheFileRemovalIsOfferedAsADiff`.
- Command: `go test ./internal/cli/ -run 'TestUpdateRewritesThePublishedLayer|TestTheFileRemovalIsOfferedAsADiff' -count=1`
- Red step: `TestUpdateRewritesThePublishedLayer` fails before the file exists.
- Depends on: I1 (kept, behaviour: nothing may still read the keys it removes).
- Gate: **Not launched before release 1 is cut.**
- Commit: `model-credentials-routing X5: release 2: `fugaro update` rewrites the published layer`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### X6: release 2: no caller derives a job per account

- Module: `migration`.
- Acceptance: No command or doctor report derives a job, an account or a secret per named account: the callers of `WorkflowVariant` in the CLI are removed with their tests and goldens; `fugaro doctor` reports a per-account job that is still deployed as one `fugaro update` will retire.
- Files: `internal/cli/run.go`, `internal/cli/cloud.go`, `internal/cli/doctor.go`, `internal/cli/doctor_brokering.go`, `internal/cli/launch_budget.go`, `internal/cli/project_job.go`, `internal/cli/image_refresh_steps.go`, `internal/cli/init.go`.
- Interfaces: Consumes X1's gate.
- Tests: `TestNoCallerDerivesAJobPerAccount`.
- Command: `go test ./internal/cli/ -run 'TestNoCallerDerivesAJobPerAccount' -count=1`
- Red step: `TestNoCallerDerivesAJobPerAccount` fails: `run.go` still derives one.
- Depends on: X1 (kept, behaviour: no image that needs a per-account job may still launch).
- Commit: `model-credentials-routing X6: release 2: no caller derives a job per account`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### X7: release 2: the infra plans no job per account

- Module: `migration`.
- Acceptance: What plans a job, a service account or a secret per named account is removed from the infra spec; `fugaro update` retires the orphaned per-account jobs and says so in its named in-flight line (a retirement deletes an orphaned job with the executions on it), asking nothing; an installation at release 1 converges.
- Files: `internal/infra/repo.go`, `internal/infra/tfvars.go`, `internal/infra/retire.go`, `internal/infra/readiness.go`, `internal/infra/spec.go`, `internal/infra/retire_variants_test.go` (new).
- Interfaces: Consumes X6.
- Tests: `TestNoJobPerAccount`, `TestUpdateRetiresTheVariantJobs`.
- Command: `go test ./internal/infra/ -run 'TestNoJobPerAccount|TestUpdateRetiresTheVariantJobs' -count=1`
- Red step: `TestNoJobPerAccount` fails: a variant is still planned.
- Depends on: X6 (kept, behaviour: the function may go only once nothing calls it).
- Commit: `model-credentials-routing X7: release 2: the infra plans no job per account`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### X8: release 2: no job account holds the Vertex permission

- Module: `migration`.
- Acceptance: No job's account and no shared project job's account is planned `roles/aiplatform.user` in any budget mode; only the credentials service's account holds it, through its custom role; the Terraform tests pin it and `fugaro update` applies it, with its in-flight line naming that a run still calling Vertex on its job's identity (an `observe` run on a release-1 image) would lose it, which X1's image-age gate has already ruled out.
- Files: `deploy/terraform/gcp/modules/workflow/sa.tf`, `deploy/terraform/gcp/modules/project/sa.tf`, `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl`, `internal/infra/chain_spec.go`, `internal/infra/chain_spec_test.go`.
- Interfaces: Consumes X1's gate and CS11's plan.
- Tests: `TestNoJobAccountHoldsVertex`; the Terraform tests.
- Command: `go test ./internal/infra/ -run 'TestNoJobAccountHoldsVertex' -count=1` and `terraform fmt -check -recursive deploy/terraform`
- Red step: `TestNoJobAccountHoldsVertex` fails: an `observe` project's job still holds it.
- Depends on: X1 (kept, behaviour: no image that calls Vertex on the job's identity may still launch).
- Commit: `model-credentials-routing X8: release 2: no job account holds the Vertex permission`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### X9: release 2: `WorkflowVariant` goes

- Module: `migration`.
- Acceptance: `WorkflowVariant` and the names it derives are removed from both backends, the account config, the sweeper and the project job; `fugaro update` retires the orphaned per-account jobs and says so in its named in-flight line (a retirement deletes an orphaned job with the executions on it), asking nothing; an installation at release 1 converges.
- Files: `internal/backend/gcp/names.go`, `internal/backend/batch/names.go`, `internal/config/claude_accounts.go`, `internal/budget/sweep.go`, `internal/runner/projectjob.go`, `internal/backend/gcp/names_variant_test.go` (new).
- Interfaces: Consumes X6 and X7.
- Tests: `TestNoWorkflowVariant`, `TestUpdateRetiresTheVariantJobs`.
- Command: `go test ./internal/backend/gcp/ -run 'TestNoWorkflowVariant' -count=1` and `go test ./internal/infra/ -run 'TestUpdateRetiresTheVariantJobs' -count=1`
- Red step: `TestNoWorkflowVariant` fails: the function exists.
- Depends on: X7 (kept, behaviour: the function may go only once nothing plans with it); X6 (kept, behaviour: and once nothing in the CLI calls it).
- Commit: `model-credentials-routing X9: release 2: `WorkflowVariant` goes`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### X10: release 2: the notes

- Module: `migration`.
- Acceptance: Release 2's notes say what is rejected and removed; `### For operators` is "run `fugaro update`" and what it prints, including the per-account jobs it retires; the website's summary line is added.
- Files: `docs/releases/` (release 2's notes file), `website/data/release-summaries.yaml`.
- Interfaces: Consumes the tree after X1 to X9.
- Tests: The Docs tests; the website's check.
- Command: `go test ./internal/cli/ -run 'Docs' -count=1` and `cd website && npm run check`
- Red step: None: a documents task.
- Depends on: X2 (kept: it describes what X2 rejects); X3 (kept: and what X3 empties); X4 (kept: and X4); X5 (kept: and X5); X8 (kept: and X8); X9 (kept: and X9); DOC3 (kept: one file, `release-summaries.yaml`).
- Commit: `model-credentials-routing X10: release 2: the notes`
- Reviewer tier: standard.

## Parallel execution

Tasks in one level have no edge between them and may run at once. A gate (a task of another
plan, a release cut, an owner's check) can hold a task past its level; each is on its task.

| Level | Tasks |
| --- | --- |
| 1 | A1, A2, A3, CK1, CK2 |
| 2 | C1, C2, C3 |
| 3 | RT1, RT2, PL3, CS1, CS2, CS3, CS4, CS8, CS9, CS13, CS14, MG1, MG3, PR4, PR5, PR8, PR12, VS1, VS2, VS4 |
| 4 | RT3, PL1, PL4, PL7, CS11, GV1, GV3, PR1, PR6, PR7, VS3 |
| 5 | RT4, RT5, PL6, CS5, CS10, MG5, GV2, PR11 |
| 6 | RT6, PL2, CS6, CS7, MG2, PR9 |
| 7 | RT7, CS12, PR2 |
| 8 | CS15, PR13 |
| 9 | PL5 |
| 10 | PL9 |
| 11 | PL10, MG4, PR3 |
| 12 | PL8, MG6, PR10 |
| 13 | I1 |
| 14 | GV4, I2, X5 |
| 15 | DOC1, X1 |
| 16 | DOC2, X2, X4, X6, X8 |
| 17 | DOC3, X3, X7 |
| 18 | DOC4, X9 |
| 19 | X10 |

**The critical path** as `fugaro plan graph` reports it, 19 tasks:
A1, C1, RT1, RT3, RT5, PL2, CS12, CS15, PL5, PL9, MG4, MG6, I1, I2, X1, X6, X7, X9, X10. What
is sequential and why: the contract waits on the document that withdraws the earlier contract;
the checks call the resolver's real answer; `routing set` refuses by the checks; then eight
tasks in a row each add a command or a flag, or change help text, and so each regenerate the one
pinned CLI reference (F50), which is an order of one generated file, not of logic; integration
joins every side; the pause's integration exhausts a real chain; and release 2 removes things
in the only safe order (the old path, its callers, the plans, the function, the notes). Release
1 alone ends at DOC4.

**What holds a task besides its edges:** CS1 and MG2 wait on always-a-budget's I1 and the keys
plan's KS12 (D3071, kept); CS5, CS6 and CS7 on always-a-budget's AB1b; CS11 and RT7 on
always-a-budget's AB15; PR4 on launcher-does-not-mint's I1; PL8 on the result of Check 880; DOC1 is not
live beside the keys plan's KS13; X1 waits on release 1 being cut and on KS13; X5 on release 1
being cut. **Release 1 is not cut before DOC1 to DOC4 have merged**: until DOC1, `SECURITY.md`
still says Vertex has no dollar cap. OpenRouter for a Claude model waits on Check 50 (R3) and
holds no task. Nothing here waits on workspace-credentials (D3179).

### Shared files

No level holds two writers of one file: every pair below is ordered by an edge, directly or
through another task. A change to any task's Files re-checks this table.

| File | Writers | Order |
| --- | --- | --- |
| `console/src/renderer/screens/live.ts` | VS1, VS3 | VS1 then VS3 |
| `console/src/renderer/screens/run.ts` | VS1, VS3 | VS1 then VS3 |
| `console/src/shared/schema.ts` | VS1, VS3 | VS1 then VS3 |
| `deploy/terraform/gcp/modules/project/sa.tf` | CS11, X8 | CS11 then X8 |
| `deploy/terraform/gcp/modules/workflow/sa.tf` | CS11, X8 | CS11 then X8 |
| `deploy/terraform/gcp/roots/installation/tests/installation.tftest.hcl` | CS11, X8 | CS11 then X8 |
| `docs/gcp-live-checklist.md` | PL7, DOC2 | PL7 then DOC2 |
| `docs/releases/` | DOC3, X10 | DOC3 then X10 |
| `internal/agent/env.go` | CS7, X1 | CS7 then X1 |
| `internal/budget/registry.go` | CS9, PR1 | CS9 then PR1 |
| `internal/budget/rules/marker.go` | CS9, PR1 | CS9 then PR1 |
| `internal/budget/rules/rules.json.tmpl` | CS9, PR1 | CS9 then PR1 |
| `internal/budget/rules/testdata/rules.golden.json` | CS9, PR1 | CS9 then PR1 |
| `internal/cli/accounts_add.go` | PL2, PL9 | PL2 then PL9 |
| `internal/cli/accounts_pool.go` | PL2, PL5, PL10 | PL2 then PL5 then PL10 |
| `internal/cli/doctor.go` | RT7, X6 | RT7 then X6 |
| `internal/cli/doctor_brokering.go` | RT7, X6 | RT7 then X6 |
| `internal/cli/init.go` | CS5, X6 | CS5 then X6 |
| `internal/cli/init_policy_publish.go` | RT4, PR9 | RT4 then PR9 |
| `internal/cli/launch_budget.go` | CS10, X1, X6 | CS10 then X1 then X6 |
| `internal/cli/models_use.go` | RT6, MG6 | RT6 then MG6 |
| `internal/cli/run.go` | CS10, CS12, MG6, X6 | CS10 then CS12 then MG6 then X6 |
| `internal/cli/secrets_intake.go` | PL5, PL9 | PL5 then PL9 |
| `internal/cli/update_in_flight.go` | MG3, PR6, X4 | MG3 then PR6 then X4 |
| `internal/cli/update_repoauth.go` | MG3, X4 | MG3 then X4 |
| `internal/cli/update_routing_migrate.go` | MG2, X3 | MG2 then X3 |
| `internal/cli/validate.go` | RT6, CS5 | CS5 then RT6 |
| `internal/config/craft.go` | PL1, X3 | PL1 then X3 |
| `internal/creds/marks.go` | PL7, PL8 | PL7 then PL8 |
| `internal/creds/poolrule.go` | PL3, PL8 | PL3 then PL8 |
| `internal/creds/poolrule_stub.go` | C2, I1 | C2 then I1 |
| `internal/creds/slips.go` | CS14, PR3 | CS14 then PR3 |
| `internal/credsvc/cascade.go` | CS1, PR3 | CS1 then PR3 |
| `internal/credsvc/chain_stub.go` | C1, I1 | C1 then I1 |
| `internal/credsvc/forward.go` | PL7, PL8, PL10, CS4 | CS4 then PL7 then PL10 then PL8 |
| `internal/credsvc/launcherpool.go` | PL4, PL6 | PL4 then PL6 |
| `internal/credsvc/launcherpool_stub.go` | C2, I1 | C2 then I1 |
| `internal/credsvc/legacy_auth.go` | MG3, X4 | MG3 then X4 |
| `internal/credsvc/server.go` | PL4, PL9, CS1, PR3, PR10 | CS1 then PL4 then PL9 then PR3 then PR10 |
| `internal/credsvc/slip.go` | CS14, PR3 | CS14 then PR3 |
| `internal/credsvc/wire.go` | PR3, X4 | PR3 then X4 |
| `internal/gateway/service.go` | CS2, PR2, X4 | CS2 then PR2 then X4 |
| `internal/infra/chain_spec.go` | CS11, X8 | CS11 then X8 |
| `internal/infra/chain_spec_test.go` | CS11, X8 | CS11 then X8 |
| `internal/infra/readiness.go` | CS11, X7 | CS11 then X7 |
| `internal/infra/spec.go` | CS11, X7 | CS11 then X7 |
| `internal/localcfg/localcfg.go` | RT2, PL1, X3 | RT2 then PL1 then X3 |
| `internal/orchestrator/resume_stub.go` | C3, I2 | C3 then I2 |
| `internal/pricing/canonical.go` | CS3, GV3 | CS3 then GV3 |
| `internal/pricing/canonical_test.go` | CS3, GV3 | CS3 then GV3 |
| `internal/pricing/spelling_stub.go` | C1, I1 | C1 then I1 |
| `internal/projectpolicy/policy.go` | RT4, PR9, X3 | RT4 then PR9 then X3 |
| `internal/routing/checks.go` | RT3, GV4 | RT3 then GV4 |
| `internal/routing/resolve_stub.go` | C1, I1 | C1 then I1 |
| `internal/runner/agentenv.go` | CS7, X1 | CS7 then X1 |
| `internal/runner/gateway.go` | CS5, CS6, PR2 | CS5 then CS6 then PR2 |
| `internal/runner/poolchoice.go` | CS5, CS7 | CS5 then CS7 |
| `internal/runner/projectjob.go` | CS13, X9 | CS13 then X9 |
| `internal/runner/runner.go` | CS6, PR2 | CS6 then PR2 |
| `internal/runner/suspend_stub.go` | C3, I2 | C3 then I2 |
| `internal/runstore/runstore.go` | CS8, PR1 | CS8 then PR1 |
| `internal/settings/registry.go` | RT2, PL1, MG5, PR9 | RT2 then PL1 then MG5 then PR9 |
| `internal/watch/json.go` | CS9, PR7 | CS9 then PR7 |
| `internal/watch/render.go` | CS9, PR7 | CS9 then PR7 |
| `plugin/skills/setup/SKILL.md` | RT5, DOC4 | RT5 then DOC4 |
| `plugin/skills/setup/reference/decisions.md` | PL5, DOC4 | PL5 then DOC4 |
| `plugin/skills/working/SKILL.md` | CS12, PR13, DOC4 | CS12 then PR13 then DOC4 |
| `schemas/fugaro.schema.json` | MG5, X2 | MG5 then X2 |
| `schemas/project-layer.schema.json` | MG5, X2 | MG5 then X2 |
| `schemas/result.schema.json` | CS8, PR1 | CS8 then PR1 |
| `schemas/settings.schema.json` | RT2, PL1, MG5, PR9 | RT2 then PL1 then MG5 then PR9 |
| `website/content/cli-reference.md` | RT5, PL2, PL5, PL9, CS12, CS15, MG4, MG6 | RT5 then PL2 then CS12 then CS15 then PL5 then PL9 then MG4 then MG6 |
| `website/data/release-summaries.yaml` | DOC3, X10 | DOC3 then X10 |

## The dependencies the plan broke, and the ones it kept

**Broken by a contract** (the dependent needed only an interface). Each is re-joined by the
integration task named, which deletes the contract's stubs.

| Edge that was | Contract | Integration | Why an interface was enough |
| --- | --- | --- | --- |
| RT2 → A1 | C1 | I1 | the block waited on the earlier plan's amendment directly; the contract now carries that wait and the table's types |
| RT1 → RT2 | C1 | I1 | the resolver needs the table's types, not the config block |
| RT3 → CS3 | C1 | I1 | the checks need the spelling lookup's signature, not the map |
| CS1 → RT4 | C1 | I1 | the service needs the published table's shape, not its publisher |
| CS2 → CS1 | C1 | I1 | the gateway needs the header and answer names, not the walk |
| CS3 → RT2 | C1 | I1 | the rewrite needs a source's model member, not the config block |
| CS6 → CS2 | C1 | I1 | the runner needs the gateway's option names, not its wiring |
| CS7 → CS6 | C1 | I1 | the environment needs the name of the fact that the gateway serves, not the runner's path |
| CS8 → CS2 | C1 | I1 | the record needs the member names, not the gateway's hook |
| CS9 → RT2 | C1 | I1 | the entry needs the source kinds' names, not the config block |
| CS10 → CS6 | C1 | I1 | the launch needs the fact's name and the header, not the runner |
| CS11 → RT2 | C1 | I1 | the spec needs the table's types, not the config block |
| MG1 → RT2 | C1 | I1 | the migration needs the table's types, not the config block |
| MG3 → CS1 | C1 | I1 | the bridge needs the service's read order as an interface, not the walk |
| VS1 → CS8 | C1 | I1 | the console needs the record's members, not their writer |
| VS4 → CS8 | C1 | I1 | the mobile feed needs the record's members, not their writer |
| PL3 → PL1 | C2 | I1 | the picker needs the rule names, not the config's checks |
| PL4 → PL1 | C2 | I1 | the lookup needs the owner's principal type, not the config's checks |
| CS1 → PL3 | C2 | I1 | the walk needs the picker's signature, not the rules |
| MG1 → PL1 | C2 | I1 | the migration needs the pool's types, not the config's checks |
| VS2 → PL2 | C2 | I1 | the console needs the JSON shape, not the command |
| CS4 → CS1 | C1 | I1 | the Vertex source needs the walk's source interface, not the walk |
| CS13 → CS1 | C1 | I1 | the other callers need the stage header's name, not the walk |
| CS12 → PL4 | C2 | I1 | the launch needs the named-pool member, not the lookup |
| PR3 → A3 | C3 | I2 | the service's pause waited on the amended documents directly; the contract now carries that wait and the node's shape |
| PR2 → PR3 | C3 | I2 | the runner needs the answer's action member, not the service's decision |
| PR1 → PR2 | C3 | I2 | the closed sets need the word a suspended run writes: the constant, not the suspend |
| PR4 → PR3 | C3 | I2 | the orchestrator needs the node's shape, not the service that writes it |
| PR5 → PR4 | C3 | I2 | the resumed run needs the `resumes` member, not the launch |
| PR6 → PR3 | C3 | I2 | the update needs the node's name, not its writer |
| PR7 → PR3 | C3 | I2 | the views need the node's shape, not its writer |
| PR8 → PR4 | C3 | I2 | the notice needs the resume's shape, not the resumer |
| VS3 → PR7 | C3 | I2 | the console needs the JSON members, not the command |
| VS4 → PR3 | C3 | I2 | the mobile feed needs the node's shape, not its writer |
| CS14 → RT4 | C1 | I1 | the grant needs the table's types, not the publisher |
| CS14 → PL4 | C2 | I1 | the grant needs the named-pool member, not the lookup |
| PL9 → PL1 | C2 | I1 | the personal pool needs the pool's types, not the config's checks |
| PR10 → PR4 | C3 | I2 | the cancel route needs the cancelled mark's shape, not the resumer that honours it |
| PL1 → A2 | C2 | I1 | the pool's config waited on the per-user token documents directly; the contract now carries that wait and the pool's types |
| RT7 → PL4 | C2 | I1 | doctor needs the designation record's shape, not the store |
| CS10 → CS14 | C2 | I1 | the launch needs the grant's member names, not the grant |
| PR12 → PR3 | C3 | I2 | the guards need the node's shape, not its writer |

**Kept** (the dependent needs the other task's behaviour, or the two write one file).

| Edge | Why it is kept |
| --- | --- |
| C1 → A1 | the earlier plan must say its own C1 (#3460) is withdrawn before a side is written against this one |
| C2 → A2 | the per-user token's documents must say a model call may now be served from one before a side is written against the contract |
| C3 → A3 | the credentials-service and launcher plans must carry the amended D138 and D3136 before a side is written against a contract that rests on them |
| RT3 → RT1 | Behaviour: it calls the resolver's answer, and a stub's answer would pass a wrong table |
| RT3 → RT2 | it reads the block RT2 adds |
| RT4 → RT2 | Behaviour: it publishes what the block holds |
| RT4 → RT3 | Behaviour: the publisher must refuse what the checks refuse |
| RT4 → PL1 | Behaviour: it publishes the pools in the shape PL1 adds to the config |
| RT5 → RT3 | Behaviour: `set` refuses by the checks' answer |
| RT5 → RT2 | it writes the block RT2 adds |
| RT6 → RT3 | Behaviour: it prints the checks' own answer |
| RT6 → CS5 | one file, `validate.go` |
| RT7 → RT6 | it calls the helpers RT6 adds to `validate.go` |
| PL1 → RT2 | one file, `localcfg.go` |
| PL2 → PL1 | it writes the fields PL1 adds |
| PL2 → RT5 | one file, the generated `cli-reference.md` |
| PL4 → CS1 | one file, `server.go` |
| PL5 → PL2 | one file, `accounts_pool.go` |
| PL5 → PL4 | Behaviour: it writes through the store PL4 builds, whose rules decide who may write |
| PL5 → CS15 | one file, the generated `cli-reference.md` |
| PL6 → PL4 | one file, `launcherpool.go`, and it extends that lookup |
| PL6 → CS14 | Behaviour: it resolves a service account from the grant CS14 teaches to admit that class |
| PL7 → CS4 | one file, `forward.go` |
| PL8 → PL3 | one file, `poolrule.go`, and it extends that picker |
| PL8 → PL10 | two files, `forward.go` and `marks.go` through PL7 |
| PL9 → PL5 | two files, `secrets_intake.go` and the generated `cli-reference.md` |
| PL9 → PL2 | one file, `accounts_add.go` |
| PL9 → PL4 | one file, `server.go` |
| PL10 → PL3 | Behaviour: it counts on the path the picker serves |
| PL10 → PL9 | `accounts_pool.go` through PL5 |
| PL10 → PL7 | one file, `forward.go` |
| CS5 → CS4 | Behaviour: the refusal may go only once the served path exists |
| CS5 → CS11 | Behaviour: the refusal may go only where the job's account no longer holds the Vertex permission |
| CS6 → CS5 | one file, `runner/gateway.go` |
| CS7 → CS5 | one file, `poolchoice.go` |
| CS10 → CS11 | Behaviour: it decides whether a job is told about the service from what CS11 plans |
| CS11 → CS4 | Behaviour: the permission it plans is for the caller CS4 builds |
| CS12 → CS10 | one file, `run.go` |
| CS12 → PL2 | one file, the generated `cli-reference.md` |
| CS12 → CS14 | Behaviour: the flag is carried in the grant member CS14 adds |
| CS15 → CS13 | Behaviour: the flag may go only once the review job needs no account |
| CS15 → CS12 | one file, the generated `cli-reference.md` |
| MG2 → MG1 | Behaviour: it writes what the function answers, and a stub's answer would be written to a real installation |
| MG2 → RT4 | Behaviour: it publishes through RT4's publisher |
| MG2 → PL1 | it writes the pool fields PL1 adds |
| MG4 → RT5 | Behaviour: its refusals name a command that must exist, and the plugin's tests check the name |
| MG4 → PL9 | one file, the generated `cli-reference.md` |
| MG5 → PL1 | two files, `registry.go` and `settings.schema.json` |
| MG6 → MG4 | one file, the generated `cli-reference.md`, and `run.go` through CS12 |
| MG6 → RT6 | one file, `models_use.go` |
| GV1 → CS4 | Behaviour: it extends the caller and the identity CS4 builds |
| GV2 → GV1 | Behaviour: an answer is translated for the request GV1 built, and the two share the tool-call mapping |
| GV3 → CS3 | two files, `canonical.go` and its test |
| GV4 → GV2 | Behaviour: it runs the whole translation |
| GV4 → GV3 | Behaviour: it needs the spelling and the price |
| GV4 → I1 | Behaviour: the test runs through the wired chain, and `checks.go` is RT3's |
| PR1 → CS8 | two files, `runstore.go` and `result.schema.json` |
| PR1 → CS9 | `registry.go` and the rules files |
| PR2 → CS6 | one file, `runner.go` |
| PR2 → CS2 | one file, `gateway/service.go` |
| PR3 → CS1 | one file, `cascade.go` |
| PR3 → CS14 | two files, `slip.go` and `slips.go` |
| PR3 → PL6 | Behaviour: a resumed run of a service account's launch resolves through PL6's lookup |
| PR3 → PL9 | one file, `server.go` |
| PR3 → PR9 | Behaviour: it reads the settings from the struct PR9 adds |
| PR6 → MG3 | one line each in `update_in_flight.go` |
| PR7 → CS9 | two files, `render.go` and `json.go` |
| PR9 → RT4 | two files, `policy.go` and `init_policy_publish.go` |
| PR9 → MG5 | two files, `registry.go` and `settings.schema.json` |
| PR10 → PR3 | one file, `server.go` |
| PR11 → PR1 | Behaviour: the readers switch over the set PR1 extends, and their exhaustiveness tests read it |
| PR13 → PR12 | Behaviour: the documents describe the state PR12 builds |
| PR13 → CS12 | one file, `working/SKILL.md` |
| I2 → I1 | Behaviour: the test exhausts a real chain, which exists only once I1 has wired it |
| VS3 → VS1 | three files shared with VS1 |
| DOC1 → I1 | a security statement must be true of the tree it describes |
| DOC1 → I2 | As I1 |
| DOC2 → DOC1 | its tests also read `SECURITY.md` |
| DOC2 → PL7 | one file, `gcp-live-checklist.md` |
| DOC3 → DOC2 | its tests pin `docs/multi-model.md` |
| DOC4 → DOC3 | the documents tasks run one after another over shared tests |
| DOC4 → PL5 | one file, `decisions.md` |
| DOC4 → RT5 | one file, `setup/SKILL.md` |
| DOC4 → CS12 | one file, `working/SKILL.md` |
| DOC4 → PR13 | one file, `working/SKILL.md` |
| X1 → I1 | Behaviour: it deletes what I1 proves unused |
| X1 → I2 | Behaviour: release 2 follows the whole of release 1, the pause included, and its tasks edit files the pause's tasks write |
| X2 → X1 | Behaviour: its gate must exist before a file may lose the key |
| X2 → MG5 | two schema files |
| X3 → X2 | Behaviour: the installation's members may be emptied only once a file that names the old keys is rejected |
| X4 → X1 | Behaviour: no image old enough to need the record may still launch |
| X4 → PR6 | one file, `update_in_flight.go` |
| X5 → I1 | Behaviour: nothing may still read the keys it removes |
| X6 → X1 | Behaviour: no image that needs a per-account job may still launch |
| X7 → X6 | Behaviour: the function may go only once nothing calls it |
| X8 → X1 | Behaviour: no image that calls Vertex on the job's identity may still launch |
| X9 → X7 | Behaviour: the function may go only once nothing plans with it |
| X9 → X6 | Behaviour: and once nothing in the CLI calls it |
| X10 → X2 | it describes what X2 rejects |
| X10 → X3 | and what X3 empties |
| X10 → X4 | and X4 |
| X10 → X5 | and X5 |
| X10 → X8 | and X8 |
| X10 → X9 | and X9 |
| X10 → DOC3 | one file, `release-summaries.yaml` |

**The gain**, as the read reports it: `42 broken, 95 kept, 8 added; before 24 tasks, weight 53;
after 19 tasks, weight 37; improves`.

**Contract changes anticipated.** A task that finds a contract wrong raises the change and never
works around it.

| Change | Contract | Most likely raised by | When | Dependents it reaches |
| --- | --- | --- | --- | --- |
| R-chain-answer | C1 | CS4 | Serving Vertex from the service needs more on the wire than a source kind | CS1, CS2 |
| R-designation | C2 | PL4 | The project's per-user grant cannot carry a pool of several containers under one designation | PL5, PL6, PL9 |
| R-pause-record | C3 | PR5 | Restoring a session needs a member the record lacks | PR2, PR4 |

## The estimate the graph read printed

`ready in 1238–5135 min, no history yet for 66 tasks (86 tasks; tiers for 20, seeds for 66; from the cached history of 2026-11-21 18:33 UTC)`

This line is informational and reserves no budget. It is what the tasks' tiers and this
machine's cached history predict, counts the three contract changes that run only if raised, and
moves as runs finish and as the graph changes.

**Runs proposed, none launched:** 83 task runs (3 documents, 3 contracts, 2 integrations, 75
others), up to 3 contract-change runs if raised, and an independent review
(`fugaro review --pr N`) for each of the 66 tasks marked opus. At the repository's per-run cap of
10 USD the 83 task runs cost at most 830 USD and the reviews at most 660 USD; the gateway
enforces the cap, and no brief states one.

## Checks the plan takes

From the reserved block 880–889 (880 to 886 used). Every one is owner-run, in the owner's own session, through a
script that prints no value.

| Check | What it measures | Script from | When |
| --- | --- | --- | --- |
| 880 | Whether an answer on a subscription token carries a utilisation figure (U1); the script reads the header names the service recorded and makes no call | PL7 | After any run on a subscription token; gates PL8 |
| 881 | A live chain falls through from an exhausted pool to the key, flagged and counted | CK1 | After release 1 is installed by `fugaro update` |
| 882 | The service serves each mapped Claude model on Vertex, priced and settled against the run's cap (U5); a call from inside a job straight to Vertex on an `enforce` project is refused | CK1 | After release 1 |
| 883 | A run pauses and the orchestrator resumes it on the original launcher's pool | CK2 | After release 1 |
| 884 | `fugaro update` migrates the dev installation and no repository's source moves | CK2 | At release 1's install |
| 885 | A retired key is rejected with the fix | CK2 | After release 2 |
| 886 | A coder stage runs on a Gemini model through Vertex, with a tool call and a streamed answer, priced (U8) | GV4 | After the release that carries GV4 |

887–889 are reserved and unused.

## After-deploy steps

Each release's `### For operators` is "run `fugaro update`" and what it prints (DOC3 for release
1; X3's pull request for release 2, which is X10 and also names the per-account jobs `update` retires). No other command, deletion or manual repair ships: the
migration (MG2), the rules and policy changes (RT4, PR1, PR9, X3), the old record (MG3, X4) and
the published layer (X5) are all `update`'s own work. What a person does by choice afterwards,
and no release depends on: designate a personal pool and move their own token into it (PL5).

## Considered and declined

- **Layering the table on top of the finished earlier plan** (D3170's alternative): two
  migrations of the same config and about 30 runs building a shape that is then removed.
- **A broad `--pool`** that replaces every pool source of a chain for one run (D3176's other
  option): a per-run override of the project's table.
- **A craft key in a table entry** (D3171's alternative): a second key and a precedence rule.
- **Always suspending** (the recommendation of D3184): declined by the owner; the floor stays.
- **Personal pools at the workspace** (D3179 as first answered): a chain cannot span two
  services (F55); the owner moved them into the project.
- **The service metering Vertex itself** (an option of D3174's second amendment): it reverses
  D3003 for one source; the owner accepted the same residual risk as for a key.
- **Leaving Gemini on Vertex out** (the recommendation of D3193): declined by the owner.
- **Taking the retired members out of the policy in release 2**: a release-1 image refuses a
  policy version it does not know (F63); release 2 empties them and a later release removes them.
- **Holding a designation in the database rules**: people reach the database by IAM (F61).
- **Calling a paused pull request live** (the recommendation of D3195): declined by the owner;
  it gets a word of its own.
- **A count of pauses per chain** (the recommendation of D3190): declined by the owner; total
  paused time is the bound.
- **Vertex on the job's identity with a cap in the gateway** (D3174 as first answered): the
  agent holds that identity, so the cap would not bind (review round 1).
- **A model header on each call**: the service reads the body, so there is nothing to disagree.
- **New members inside the policy's `credentials` block or a published pool**: a release-behind
  reader refuses the whole policy (F49).
- **Refusing a lone token in release 1**: an upgrade would refuse a config it has not migrated
  yet; release 1 reports it and release 2 refuses it.
- **The same run id for the resumed job** (D3185's alternative): it breaks one terminal outcome
  per run and the write-once budget entry.
- **A resumer on every installation** (D3186's alternative): a new identity with launch rights
  everywhere.
- **Folding the resolver into the contract**: a contract that computes is a side, not a contract.
- **One task for the whole migration**: the pure function (MG1) is reviewable alone and is what
  `TestMigrateMovesNoRepository` pins; the wiring into `update` (MG2) is a different risk.

## Where each finding, decision and resolution lands

| Item | Tasks |
| --- | --- |
| D3170 | A1, and every task that carries an earlier one (the table above) |
| D3171 | MG1, CS7 |
| D3172 | RT1 |
| D3173 | MG1, MG5, X2 |
| D3174 | CS4, CS5, CS11, X8, DOC1, Check 882 |
| D3175 | CS3, RT3 |
| D3176 | CS12, CS14, CS15, PL4, PL2, MG1, X2 |
| D3177 | PL3, MG1, MG2, X3 |
| D3178 | PL7, PL8 |
| D3179 | A2, PL4, PL9 |
| D3180 | CS1, RT3 |
| D3181 | PL6 |
| D3182 | RT2, CS1, MG1 |
| D3183 | MG1, MG2 |
| D3184 | PR2, PR3, PR9 |
| D3185 | PR1, PR11, PR4, PR5 |
| D3186 | PR3, PR4, RT7 |
| D3187 | PR9, PR3 |
| D3188 | CS8, PL10, VS1, VS2 |
| D3189 | A3, PR4 |
| D3190 | PR3, PR9 |
| D3191 | A2, PL6, PR3 |
| D3192 | MG2 |
| D3193 | GV1, GV2, GV3, GV4, Check 886 |
| D3194 | A2, PL5 |
| D3195 | PR12, PR13 |
| D3196 | MG1, MG2 |
| D3197 | PR10 |
| R1 | RT3, CS10 |
| R2 | CS1, CS2, CS4 |
| R3 | RT3 |
| R4 | CS1, MG1, MG5 |
| R5 | PL1, PL3 |
| R6 | C3, PR3 |
| R7 | PL4, PL6, CS14 |
| R8 | PR3 |
| R9 | PL5 |
| R10 | PR2 |
| R11 | PR9 |
| R12 | PR2, PR5 |
| R13 | PR7, PR8, PR11, VS3, VS4 |
| R14 | MG2, X1 to X10 |
| R15 | PR1, PR11 |
| R16 | CS1 |
| R17 | RT3 |
| R18 | PR2, PR5 |
| R19 | C3, PR3 |
| R20 | RT2, CS1, MG1 |
| R21 | RT4, PL1, PR9 |
| R22 | RT3, CS10, CS13 |
| R23 | CS4, DOC1 |
| R24 | CS1, MG3 |
| R25 | X3 |
| R26 | PL4, PL9, PL10, PR3, PR5 |
| F1, F2 | A1, C1, MG1 |
| F3, F8 | RT3, CS10 |
| F4 | RT1 |
| F5 | A3, RT3 |
| F6 | MG1, MG5 |
| F7 | No task: the repository keeps its model choice, and RT3 and CS10 refuse a model with no entry |
| F9, F41 | RT2, MG1 |
| F10 | PL1, PL2, CS12, X2 |
| F11, F12 | PL1, PL3, CS1 |
| F13 | PL7, PL8 |
| F14, F15 | A2, PL4, PL5, PL9 |
| F16 | PL6 |
| F17 | PL5 |
| F18 | MG1 |
| F19 | PL10, VS2 |
| F20, F21, F22 | CS1, CS2 |
| F23 | CS4, CS5, CS11, X8 |
| F24 | RT3 |
| F25 | CS3 |
| F26 | MG1, RT3 |
| F27 | CS8 |
| F28, F29, F35 | PR2, PR3 |
| F30 | PR9, PR3 |
| F31, F37 | C3, PR3 |
| F32 | PR1, PR11 |
| F33 | PR4 |
| F34, F36 | PR2, PR5, PR6 |
| F38 | VS1 |
| F39 | PR8, VS3, VS4 |
| F40 | MG2, X2 |
| F42 | MG3, CS9, CS11, X4 |
| F43 | MG5, X3 |
| F44 | Every task's acceptance line; CK1, CK2 |
| F45 | Conventions, above |
| F46 | MG1, MG4, MG5, X2 |
| F47 | RT3, CS10, CS13 |
| F48 | X6, X7, X9 |
| F49 | RT4, PL1, PR9 |
| F50 | RT5, PL2, CS12, PL5, PL9, MG6 (the order of the CLI reference) |
| F51 | PL4, DOC1 |
| F52 | PR3, PR4 |
| F53 | PR3 |
| F54 | PR7 |
| F55 | PL4, PL9 (personal pools in the project) |
| F56 | CS4, CS11, X8, DOC1 |
| F57 | CS1 |
| F58 | CS4, GV1 to GV4 |
| F59 | PL6, PR3 |
| F60 | CS14, CS1, PL4 |
| F61 | PL4, PL9, PL10, PR3 |
| F62 | PL5 |
| F63 | X3 |
| F64 | PR12, PR13 |
| F65 | MG1, MG2 |
| F66 | PR10 |
| F67 | PR2, PR3 |
| O1, O2, O7 | The owner's, before launch; no task |
| O3 | RT3 |
| O4, O5, O9 | PR9, PR3 |
| O11 | GV4, Check 886 |
| O6 | No task: noted in the reconciliation |
| O8 | VS3 |
| O13 to O16 | Closed by D3194 to D3197 |
| O17 | No task: the pull request's open items |
| O10 | Withdrawn |
| U1 | PL7 |
| U3 | No task: R7 does not depend on it |
| U4 | PR4 |
| U5 | CS3, Check 882 |
| U6 | CS13, and I1's guard test |
| U7 | CS1 |
| U8 | GV1, GV2, Check 886 |
