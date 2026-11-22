# Model credentials routing: implementation plan

This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md).

**Status: plan only. Nothing is implemented. No task is approved: each needs the owner's
explicit go, by name.** An answer in the reconciliation settles a question and approves nothing.
No run was launched to write this plan and none launches from it.

The answers that shaped it, all the owner's of 2026-11-21: the table supersedes craft access now
and the earlier plan's not-started tasks are re-cut here (D3170); one chain per model (D3171);
the most specific pattern wins (D3172); the table replaces `routing.claude.via` (D3173); Vertex
is capped (D3174); the service rewrites the model's spelling on a move (D3175); `--token` is
retired (D3176); `subscription_spread` is retired (D3177); the threshold rule waits on Check 870
(D3178); personal pools live on workspace-credentials (D3179); a launcher with no pool is served
by the next source (D3180); a service account's pool is a project pool an admin fills (D3181);
per-repository admission stays, for every source kind (D3182); existing accounts become the
shared pool (D3183); a short in-job wait is kept under a floor, **not the recommendation**
(D3184); a resumed job is a new run in the same chain (D3185); the resident orchestrator resumes
(D3186); the maximum wait defaults to 6 hours (D3187); the console shows the source per step, a
fallback badge, the switch log and a pools screen (D3188).

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
| The launcher is only ever the verified principal on a slip; a forged requester gets nothing | PL4 (`TestAForgedRequesterGetsNothing`), PR3 (`TestAJobCannotNameAnotherLauncher`) |
| A personal pool serves only its owner's runs, including after a pause and a reissue | PL4, PL6, PR3, I2 |
| A service account's run never draws on a person's pool (D2976) | PL6 |
| The agent never sees a credential, a source or a switch | CS2, CS7, I1 |
| The in-job gateway holds no key and no table | CS2, DOC1 |
| No money is spent that no chain names: a model with no entry is refused, Vertex only where named | RT3, CS10, I1 |
| A paid fallback is held to the caps, Vertex included | CS5, CS8, I1 |
| Nothing moves on upgrade: every repository's runs go out on what they went out on | MG1 (`TestMigrateMovesNoRepository`), MG2, Check 874 |
| A run in flight across the update keeps working, on either release | MG3, PR6 |
| A paused run is resumed at most once, and never after its launcher cancelled it | PR4, PR7 |
| A pause the current image cannot read ends with a named outcome | PR5 |
| No proxy call is held open; no command blocks on a paused run | PR2, PR7 |
| A limit found inside a streamed answer is not retried; the next call moves on (U7) | CS1 |
| An older console and an older CLI still parse what a newer run writes | CS9 (`auth` kept), PR1, VS3 |
| No command asks a question | RT5, PL2, PL5, MG2, MG4 |

## What becomes of the earlier plan (D3170)

`docs/credentials-belong-to-models` keeps what is merged and its two-release shape. Every task
of it that is not merged is withdrawn and carried here. A1 writes this table into that plan's
own documents. **The owner stops the withdrawn tasks by hand before any launches (O1)**; C1's
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
| CM8 | not started | CS6 |
| CM9, CM11 | not started | CS7 |
| CM10 | not started | CS8 |
| CM12 | not started | CS9 |
| CM13 | not started | CS11 |
| CM14, CM15 | not started | CS10, RT7 |
| CM16 | not started | RT3 |
| CM17, CM18 | not started | RT6, RT7 |
| CM20 | not started | MG5 |
| CM21 | not started | VS1 |
| CM23 | not started | MG6 |
| CM34 | not started | MG4 |
| I1 | not started | I1 |
| CM24 to CM27 | not started | DOC1 to DOC4 |
| CM28, CM29, CM30, CM33 | not started | X1, X2, X3, X5 |
| Checks 810 and 811 | scripts merged, not run | Replaced by Checks 874 and 875; DOC2 marks them so in the live checklist |

The keys plan's KS13 waits on that plan's I1; A1 re-points it at this plan's I1.

## Slice 0: the documents the other plans need first

### A1: credentials-belong-to-models says what this plan replaces

- Module: `migration`.
- Acceptance: That plan's README, reconciliation and plan say, task by task, what D3170 withdraws and which task here carries it (the table in this plan's "What becomes of the earlier plan"), that D3060, D3064, D3068 and D3069 are amended or reversed by D3170, D3171, D3176 and D3183, and that contract C1 as drafted in #3460 is withdrawn; its graph marks the withdrawn nodes; the keys plan's README says KS13 now waits on this plan's I1; the Docs tests pass.
- Files: `docs/credentials-belong-to-models/README.md`, `docs/credentials-belong-to-models/reconciliation.md`, `docs/credentials-belong-to-models/implementation-plan.md`, `docs/credentials-belong-to-models/implementation-plan.graph.json`, `docs/keys-through-service/README.md`.
- Interfaces: Consumes this plan's reconciliation. Produces nothing a task calls.
- Tests: The Docs tests that pin that folder; `fugaro plan graph --plan docs/credentials-belong-to-models/implementation-plan.md` reads the graph.
- Command: `go test ./internal/cli/ -run 'Docs' -count=1`
- Red step: None: a documents task. The check is that `git grep -n 'D3170' docs/credentials-belong-to-models` finds the amendment in all three documents.
- Depends on: none.
- Commit: `model-credentials-routing A1: credentials-belong-to-models says what this plan replaces`
- Reviewer tier: standard.

### A2: workspace-credentials says what the chain now decides

- Module: `pools`.
- Acceptance: That plan's reconciliation marks D3141, D3142, D3144 and D2974 as amended by D3179 and D3180 with the original answers still visible, says D2976 stands and how D3181 reads beside it, and its plan names which of its tasks change (the automatic use, the shadowing rule, the move to the shared entries); the Docs tests pass.
- Files: `docs/workspace-credentials/README.md`, `docs/workspace-credentials/reconciliation.md`, `docs/workspace-credentials/implementation-plan.md`.
- Interfaces: Consumes this plan's reconciliation. Produces nothing a task calls.
- Tests: The Docs tests that pin that folder.
- Command: `go test ./internal/cli/ -run 'Docs' -count=1`
- Red step: None: a documents task. `git grep -n 'D3180' docs/workspace-credentials` finds the amendment.
- Depends on: none.
- Commit: `model-credentials-routing A2: workspace-credentials says what the chain now decides`
- Reviewer tier: standard.

### A3: the credentials-service, model-crafts and mixed-credentials documents are amended

- Module: `chain-serving`.
- Acceptance: D138 (replaced beyond the floor by D3184), C3.1 (un-deferred as this plan's pause-resume), D152 and D153 (re-keyed by R4 and R17), D154 (amended by R16), D1622 and D1636 (reversed by D3177) and D1637 (amended by D3175) each carry a dated amendment that names this plan, and `docs/mixed-credentials/spec.md` says D1a governs a provider's `models` and not the routing table; the Docs tests pass.
- Files: `docs/credentials-service/reconciliation.md`, `docs/credentials-service/implementation-plan.md`, `docs/model-crafts/reconciliation.md`, `docs/mixed-credentials/spec.md`.
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
- Acceptance: A package `internal/routing` holds the types both sides call and nothing that computes: `Table`, `Entry{Match, Chain}`, `Source{Kind, Pool, Provider, Model}` with the kinds `launcher-pool`, `pool`, `anthropic`, `provider`, `vertex`, the per-repository admission shape, and the signatures `Resolve(table, model) (Entry, error)` and `Walk`; `internal/credsvc/chain_wire.go` holds the request headers that carry the call's model and stage and the answer's members (`source`, `fallback`, `next: vertex`); `contract-table.md` says what each side may rely on, including the run record's new members (`stages[].source`, `stages[].fallback`, `cost.fallback_usd`) and the registry entry's `source`; the stubs return the shapes and do nothing else; the package's tests pin the JSON of every shape.
- Files: `internal/routing/table.go` (new), `internal/routing/table_test.go` (new), `internal/routing/resolve_stub.go` (new, stub), `internal/credsvc/chain_wire.go` (new), `internal/credsvc/chain_stub.go` (new, stub), `docs/model-credentials-routing/contract-table.md` (new).
- Interfaces: Consumes nothing. Produces the types, the wire names and the contract document every task of `routing-table`, `chain-serving`, `migration` and VS1 builds against.
- Tests: `TestTableShapesAreStable`, `TestChainWireNames`.
- Command: `go test ./internal/routing/ -run 'TestTableShapesAreStable' -count=1` and `go test ./internal/credsvc/ -run 'TestChainWireNames' -count=1`
- Red step: `TestTableShapesAreStable` fails to compile before the package exists.
- Depends on: A1 (kept: the earlier plan must say its own C1 (#3460) is withdrawn before a side is written against this one).
- Commit: `model-credentials-routing C1: the table contract`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### C2: the pools contract

- Module: `pools`.
- Acceptance: `internal/routing/pools.go` holds a pool's new members as types (`Rule` with `ordered`, `round-robin`, `failover`; `Owner` as a principal of kind person, service account or project; the optional `Threshold`), the designation record (principal, project, pool) and the interface `LauncherPool(ctx, principal) (PoolRef, bool, error)` the service calls; `contract-pools.md` says where a personal pool's tokens live (the workspace, D3179), that a principal is only ever the verified one on a slip (R7), and the JSON of `fugaro accounts pool ls --json` with state, next reset and headroom and never a value; the stub answers "no pool designated".
- Files: `internal/routing/pools.go` (new), `internal/routing/pools_test.go` (new), `internal/credsvc/launcherpool_stub.go` (new, stub), `docs/model-credentials-routing/contract-pools.md` (new).
- Interfaces: Consumes nothing. Produces the pool types, the designation record and the lookup interface for `pools`, CS1, MG1 and VS2.
- Tests: `TestPoolShapesAreStable`.
- Command: `go test ./internal/routing/ -run 'TestPoolShapesAreStable' -count=1`
- Red step: `TestPoolShapesAreStable` fails to compile before the file exists.
- Depends on: A2 (kept: the workspace plan must carry the amended decisions before a side is written against a contract that rests on them).
- Commit: `model-credentials-routing C2: the pools contract`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### C3: the pause contract

- Module: `pause-resume`.
- Acceptance: `internal/runstore/pause.go` holds the paused record (`pause.json` in the run's folder: the stage, the session's name, the checkpoint commit, the resume time, the chain id, a format version), the Realtime Database node `paused/<slug>/<run>` as a type, the constant for the status word `paused`, the task's new member `resumes` and the service's `can_resume` answer member; `contract-pause.md` says who writes each (the job writes the record; the service alone writes the launcher, R8), what the orchestrator may rely on, and what a reader of an older or newer format version does (refuses with a named outcome, R18); the stubs do nothing.
- Files: `internal/runstore/pause.go` (new), `internal/runstore/pause_test.go` (new), `internal/runner/suspend_stub.go` (new, stub), `internal/orchestrator/resume_stub.go` (new, stub), `docs/model-credentials-routing/contract-pause.md` (new).
- Interfaces: Consumes nothing. Produces the paused record, the node, the status constant and the `resumes` member for `pause-resume`, VS3 and VS4.
- Tests: `TestPauseShapesAreStable`.
- Command: `go test ./internal/runstore/ -run 'TestPauseShapesAreStable' -count=1`
- Red step: `TestPauseShapesAreStable` fails to compile before the file exists.
- Depends on: none.
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
- Acceptance: The installation's config decodes a strict `model_routing:` list of entries and, per source, an `admit:` list of repositories (D3182, every source kind); a repository's `fugaro.yaml` and the project layer have no such key and an attempt is an unknown-field error; the block round-trips through `localcfg`'s writer with comments kept.
- Files: `internal/localcfg/localcfg.go`, `internal/localcfg/routing.go` (new), `internal/localcfg/routing_test.go` (new).
- Interfaces: Consumes C1's types. Produces the block RT3, RT4, RT5, MG2 and MG4 read and write.
- Tests: `TestModelRoutingDecodesStrictly`, `TestModelRoutingRoundTrips`, `TestAdmissionPerSource`.
- Command: `go test ./internal/localcfg/ -run 'TestModelRouting|TestAdmissionPerSource' -count=1`
- Red step: `TestModelRoutingDecodesStrictly` fails: the key is an unknown field today.
- Depends on: C1 (the contract's types).
- Commit: `model-credentials-routing RT2: the project's `model_routing` block and per-repository admission`
- Reviewer tier: standard.

### RT3: the table's checks

- Module: `routing-table`.
- Acceptance: One function reports every problem of a table against the project's credentials, pools, providers and crafts: a craft's model no entry matches (R1); a chain whose only source is the launcher's pool (D3180); a source that does not reach the model (`craft.ByReach`); a source with no mapped spelling for the model (D3175); a Claude model through a provider before Check 50 is recorded as passed (R3); a paid source after a pool without `enforce` and a per-run cap (R17); a pool or provider that is not declared. Each problem names the fix.
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
- Acceptance: `fugaro update` and the policy publisher write the table and the admissions into the policy as `model_routing`, migrating the node in place; the policy's validation refuses a table `routing.Problems` refuses; an installation at the previous policy version converges; the settings registry lists the new member.
- Files: `internal/projectpolicy/policy.go`, `internal/projectpolicy/routing_test.go` (new), `internal/cli/init_policy_publish.go`, `internal/settings/registry.go`, `internal/cli/update_route_test.go`.
- Interfaces: Consumes RT2's block and RT3's checks. Produces the published member the service reads (CS1).
- Tests: `TestPolicyCarriesModelRouting`, `TestUpdatePublishesModelRouting`, `TestUpdateConvergesFromAPolicyWithoutRouting`.
- Command: `go test ./internal/projectpolicy/ -run 'TestPolicyCarriesModelRouting' -count=1` and `go test ./internal/cli/ -run 'TestUpdatePublishesModelRouting|TestUpdateConvergesFromAPolicyWithoutRouting' -count=1`
- Red step: `TestPolicyCarriesModelRouting` fails: the policy decodes strictly and has no such member.
- Depends on: RT2 (kept, behaviour: it publishes what the block holds); RT3 (kept, behaviour: the publisher must refuse what the checks refuse).
- Commit: `model-credentials-routing RT4: the table is published in the policy`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### RT5: `fugaro routing ls`, `set` and `rm`

- Module: `routing-table`.
- Acceptance: `fugaro routing ls` prints the table with, per model a craft uses, the entry that wins and why; `fugaro routing set <pattern> <source>...` and `rm <pattern>` write the block and refuse what `routing.Problems` refuses, printing the fix; `--json` has a schema; none asks a question, and `set` in a coding agent's session is refused as a cloud write is today. The setup skill names the command in the same change, as the plugin's tests require.
- Files: `internal/cli/routing.go` (new), `internal/cli/routing_test.go` (new), `schemas/routing-ls.schema.json` (new), `plugin/skills/setup/SKILL.md`, `internal/cli/root.go`.
- Interfaces: Consumes RT2's block and RT3's checks. Produces the command MG4's refusals and the documents name.
- Tests: `TestRoutingLs`, `TestRoutingSetRefusesWithTheFix`, `TestRoutingAsksNothing`.
- Command: `go test ./internal/cli/ -run 'TestRouting' -count=1`
- Red step: `TestRoutingLs` fails: the command does not exist.
- Depends on: RT3 (kept, behaviour: `set` refuses by the checks' answer); RT2 (kept: it writes the block RT2 adds).
- Commit: `model-credentials-routing RT5: `fugaro routing ls`, `set` and `rm``
- Reviewer tier: standard.

### RT6: `validate`, `craft ls` and `models use` read the table

- Module: `routing-table`.
- Acceptance: `fugaro validate` reports the table's problems for the repository's own crafts; `fugaro craft ls` shows each craft's model and the chain it resolves to in place of an access; `fugaro models use` refuses a model no entry matches, with the fix; the enterprise A/B code reads the chain where it read the access.
- Files: `internal/cli/validate.go`, `internal/cli/craft.go`, `internal/cli/models_use.go`, `ee/ab/admit.go`, `schemas/craft-ls.schema.json`.
- Interfaces: Consumes RT3's `routing.Problems`. Produces the helpers RT7 and MG6 call.
- Tests: `TestValidateReportsRoutingProblems`, `TestCraftLsShowsTheChain`, `TestModelsUseRefusesAnUnroutedModel`.
- Command: `go test ./internal/cli/ -run 'TestValidateReportsRoutingProblems|TestCraftLsShowsTheChain|TestModelsUseRefusesAnUnroutedModel' -count=1`
- Red step: `TestCraftLsShowsTheChain` fails: the output has an access column.
- Depends on: RT3 (kept, behaviour: it prints the checks' own answer).
- Commit: `model-credentials-routing RT6: `validate`, `craft ls` and `models use` read the table`
- Reviewer tier: standard.

### RT7: doctor reads the table

- Module: `routing-table`.
- Acceptance: `fugaro doctor` reports a table problem, a source whose secret is missing from the bundle, a chain that names the launcher's pool on a project where no one has designated one, and a pausing project with no resident orchestrator; it no longer reports a repository's auth mode.
- Files: `internal/cli/doctor.go`, `internal/cli/doctor_brokering.go`, `internal/cli/doctor_routing.go` (new), `internal/cli/doctor_routing_test.go` (new).
- Interfaces: Consumes RT3 and RT6's helpers. Produces nothing a task calls.
- Tests: `TestDoctorRouting` (one sub-test per report).
- Command: `go test ./internal/cli/ -run 'TestDoctorRouting' -count=1`
- Red step: `TestDoctorRouting/table_problem` fails before the file exists.
- Depends on: RT6 (kept: it calls the helpers RT6 adds to `validate.go`).
- Commit: `model-credentials-routing RT7: doctor reads the table`
- Reviewer tier: standard.

## Slice 3: `pools`

### PL1: a pool's rule, owner and threshold in the config

- Module: `pools`.
- Acceptance: `config.Pool` gains `rule` (with `ordered`; `strategy` is still read and means the same), an owner that may be `serviceAccount:<email>` or `project`, and an optional `threshold` between 0 and 1 that validation refuses unless the rule set allows it (PL8); a `claude-oauth` credential that is a member of no pool is a validation problem naming the fix (D3176); `subscription_spread` is still decoded and no longer gates anything in the config's own checks (D3177).
- Files: `internal/config/craft.go`, `internal/config/pool_rules.go` (new), `internal/config/pool_rules_test.go` (new), `internal/localcfg/localcfg.go`.
- Interfaces: Consumes C2's types. Produces the fields PL2, PL3, MG2 and MG5 read.
- Tests: `TestPoolRuleOrdered`, `TestPoolOwnerKinds`, `TestALoneTokenIsRefused`.
- Command: `go test ./internal/config/ -run 'TestPoolRuleOrdered|TestPoolOwnerKinds|TestALoneTokenIsRefused' -count=1`
- Red step: `TestALoneTokenIsRefused` fails: a lone token validates today.
- Depends on: C2 (the contract's types); RT2 (kept: one file, `localcfg.go`).
- Commit: `model-credentials-routing PL1: a pool's rule, owner and threshold in the config`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PL2: the account commands keep every token in a pool

- Module: `pools`.
- Acceptance: `fugaro accounts add` puts a new subscription token in a pool (a pool of one named after it unless `--pool` names another); `fugaro accounts pool add` takes `--rule` and a service-account owner; `fugaro accounts rm` of a pool's last member removes the pool or refuses with the fix; `fugaro accounts pool ls --json` prints the shape of C2's contract; none asks a question.
- Files: `internal/cli/accounts_add.go`, `internal/cli/accounts_pool.go`, `internal/cli/accounts_rm.go`, `internal/cli/accounts_pool_rule_test.go` (new), `schemas/accounts-pool-ls.schema.json` (new).
- Interfaces: Consumes PL1's fields and C2's JSON shape. Produces the commands PL5 extends and the JSON VS2 reads.
- Tests: `TestAccountsAddMakesAPoolOfOne`, `TestPoolAddTakesARule`, `TestPoolLsJSONHasNoValue`.
- Command: `go test ./internal/cli/ -run 'TestAccountsAddMakesAPoolOfOne|TestPoolAddTakesARule|TestPoolLsJSONHasNoValue' -count=1`
- Red step: `TestAccountsAddMakesAPoolOfOne` fails: `add` writes a lone account today.
- Depends on: PL1 (kept: it writes the fields PL1 adds).
- Commit: `model-credentials-routing PL2: the account commands keep every token in a pool`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PL3: the service picks inside a pool by its rule

- Module: `pools`.
- Acceptance: Inside one pool the service picks by the pool's rule: `ordered` is the first listed member that is usable, `round-robin` rotates, `failover` is unchanged; a pool of several subscription tokens serves all of them with `subscription_spread` off (D3177). The rule lives in one new file and `internal/creds/select.go` is not edited.
- Files: `internal/creds/poolrule.go` (new), `internal/creds/poolrule_test.go` (new), `internal/credsvc/pool.go`, `internal/credsvc/roundrobin.go`.
- Interfaces: Consumes C2's rule names. Produces `creds.PickInPool`, which CS1 calls through the contract's signature.
- Tests: `TestPickOrdered`, `TestPickRoundRobin`, `TestAPoolServesWithoutTheSpreadSwitch`.
- Command: `go test ./internal/creds/ -run 'TestPick' -count=1` and `go test ./internal/credsvc/ -run 'TestAPoolServesWithoutTheSpreadSwitch' -count=1`
- Red step: `TestAPoolServesWithoutTheSpreadSwitch` fails: such a pool is inert today.
- Depends on: C2 (the contract's rule names).
- Commit: `model-credentials-routing PL3: the service picks inside a pool by its rule`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PL4: the launcher's pool: a person's own tokens

- Module: `pools`.
- Acceptance: The service resolves "the launcher's pool" for a run from the verified owner of its slip and that person's designation for the project, reads the pool's tokens through the workspace's grant route and serves them only to runs that person launched; no designation, or an empty pool, answers "none" and never an error; no other person's run can name the pool, with a mutation test that a forged `requested_by` changes nothing.
- Files: `internal/credsvc/launcherpool.go` (new), `internal/credsvc/launcherpool_test.go` (new), `internal/creds/designation.go` (new), `internal/creds/designation_test.go` (new), `internal/budget/rules/rules.json.tmpl`.
- Interfaces: Consumes C2's interface and the workspace plan's grant route. Produces the real `LauncherPool` behind the stub.
- Tests: `TestLauncherPoolIsTheSlipOwners`, `TestNoDesignationIsNone`, `TestAForgedRequesterGetsNothing`.
- Command: `go test ./internal/credsvc/ -run 'TestLauncherPool|TestNoDesignationIsNone|TestAForgedRequesterGetsNothing' -count=1`
- Red step: `TestLauncherPoolIsTheSlipOwners` fails against the stub, which always answers "none".
- Depends on: C2 (the contract's interface and record); CS9 (kept: one file, `rules.json.tmpl`).
- Gate: **Not launched before workspace-credentials' I1 has merged: it reads tokens through the route that plan builds.**
- Commit: `model-credentials-routing PL4: the launcher's pool: a person's own tokens`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PL5: designate a pool, and contribute a token to the shared pool

- Module: `pools`.
- Acceptance: `fugaro accounts pool designate <pool>` records the caller's own designation for the project, from their own session only; `fugaro accounts pool contribute <token> --to <shared pool>` copies the caller's own token into a project pool and refuses anyone else's (R9); an admin's `accounts pool rm-member` removes a contributed token; none asks a question.
- Files: `internal/cli/accounts_pool.go`, `internal/cli/accounts_pool_designate.go` (new), `internal/cli/accounts_pool_designate_test.go` (new), `plugin/skills/setup/reference/decisions.md`.
- Interfaces: Consumes PL4's designation store and PL2's commands. Produces nothing a task calls.
- Tests: `TestDesignateIsTheCallersOwn`, `TestContributeRefusesAnothersToken`.
- Command: `go test ./internal/cli/ -run 'TestDesignateIsTheCallersOwn|TestContributeRefusesAnothersToken' -count=1`
- Red step: `TestDesignateIsTheCallersOwn` fails: the command does not exist.
- Depends on: PL2 (kept: one file, `accounts_pool.go`); PL4 (kept, behaviour: it writes through the store PL4 builds, whose rules decide who may write).
- Commit: `model-credentials-routing PL5: designate a pool, and contribute a token to the shared pool`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PL6: a service account's pool

- Module: `pools`.
- Acceptance: A run whose slip names a verified service account (`VerifyServiceAccountLauncher`) resolves "the launcher's pool" to the project pool that principal owns, filled by an admin (D3181); it never resolves to a person's pool (D2976); a run the orchestrator reissues keeps the pool of the launcher recorded on the original slip.
- Files: `internal/credsvc/launcherpool.go`, `internal/credsvc/launcherpool_sa_test.go` (new), `internal/credsvc/launchers.go`.
- Interfaces: Consumes PL4's lookup. Produces nothing a task calls.
- Tests: `TestAServiceAccountsPool`, `TestAServiceAccountNeverGetsAPersonsPool`, `TestAReissuedRunKeepsItsLauncher`.
- Command: `go test ./internal/credsvc/ -run 'TestAServiceAccount|TestAReissuedRunKeepsItsLauncher' -count=1`
- Red step: `TestAServiceAccountsPool` fails: such a run resolves to "none".
- Depends on: PL4 (kept: one file, `launcherpool.go`, and it extends that lookup).
- Commit: `model-credentials-routing PL6: a service account's pool`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PL7: Check 870, as a script

- Module: `pools`.
- Acceptance: `scripts/live-checks/check-870-token-utilisation.sh` makes one small call on a subscription token through the deployed service in the owner's own session and prints, without any value, which rate-limit headers the answer carried and whether one is a utilisation figure; its test runs it against a fake and proves it prints no token.
- Files: `scripts/live-checks/check-870-token-utilisation.sh` (new), `scripts/live-checks/check-870-token-utilisation_test.sh` (new), `docs/gcp-live-checklist.md`.
- Interfaces: Consumes nothing. Produces the answer that gates PL8 (U1).
- Tests: The script's own test.
- Command: `bash scripts/live-checks/check-870-token-utilisation_test.sh`
- Red step: The test fails before the script exists.
- Depends on: none.
- Commit: `model-credentials-routing PL7: Check 870, as a script`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).
- Owner-run: The owner runs `bash scripts/live-checks/check-870-token-utilisation.sh` in his own session against the dev installation. No agent runs it.

### PL8: the usage threshold rule

- Module: `pools`.
- Acceptance: A pool with `threshold: 0.8` moves off a member once the utilisation the provider last reported for it reaches the threshold, before a limit is hit, and comes back when the window resets; with no figure reported the rule does nothing and says so once in the audit line.
- Files: `internal/creds/poolrule.go`, `internal/creds/poolrule_threshold_test.go` (new), `internal/credsvc/forward.go`, `internal/creds/marks.go`.
- Interfaces: Consumes PL3's picker. Produces nothing a task calls.
- Tests: `TestThresholdMovesOffBeforeTheLimit`, `TestThresholdWithNoFigureDoesNothing`.
- Command: `go test ./internal/creds/ -run 'TestThreshold' -count=1`
- Red step: `TestThresholdMovesOffBeforeTheLimit` fails: the picker ignores utilisation.
- Depends on: PL3 (kept: one file, `poolrule.go`, and it extends that picker).
- Gate: **Not launched unless the owner's run of Check 870 found a utilisation figure (D3178). If it did not, this task is withdrawn and the plan says so.**
- Commit: `model-credentials-routing PL8: the usage threshold rule`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

## Slice 4: `chain-serving`

### CS1: the service walks the chain

- Module: `chain-serving`.
- Acceptance: For each model call the service resolves the call's model to its entry and offers the chain's sources in the chain's own order, replacing the fixed tier order (R4): a pool by its rule, the launcher's pool through C2's lookup, a key, a provider key; a source the repository is not admitted to, an empty launcher's pool and an exhausted source are skipped (D3180, D3182); the placement is sticky for one step and the chain is walked from its first source at the next (R16); the answer names the source and whether it was the chain's first; with nothing usable it answers `exhausted_until` with the earliest reset as today. A call with no model header is refused.
- Files: `internal/credsvc/chain.go` (new), `internal/credsvc/chain_test.go` (new), `internal/credsvc/cascade.go`, `internal/creds/select.go`, `internal/credsvc/server.go`.
- Interfaces: Consumes C1's wire and `Resolve`, C2's `LauncherPool` and `PickInPool`. Produces the serving path CS2, CS4 and PR3 meet.
- Tests: `TestChainOrderIsStrict`, `TestChainSkipsAnUnadmittedSource`, `TestChainIsWalkedAgainEachStep`, `TestAnswerNamesTheSourceAndFallback`, `TestACallWithNoModelIsRefused`.
- Command: `go test ./internal/credsvc/ -run 'TestChain|TestAnswerNamesTheSourceAndFallback|TestACallWithNoModelIsRefused' -count=1`
- Red step: `TestChainOrderIsStrict` fails: a key listed before a pool is still served after it.
- Depends on: C1 (the contract's wire and resolver signature); C2 (the contract's pool lookup).
- Commit: `model-credentials-routing CS1: the service walks the chain`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CS2: the gateway says which model and stage a call is for

- Module: `chain-serving`.
- Acceptance: The in-job gateway sends the call's model and the run's current stage on every call to the service, reads the answer's source and fallback members into the step's record hook, and strips them before the agent sees the answer; it holds no key and no table (R2).
- Files: `internal/gateway/service.go`, `internal/gateway/chainwire.go` (new), `internal/gateway/chainwire_test.go` (new), `internal/gateway/gateway.go`.
- Interfaces: Consumes C1's wire names. Produces the hook CS8 reads and the file CS4 extends.
- Tests: `TestGatewaySendsModelAndStage`, `TestTheAgentNeverSeesTheSource`.
- Command: `go test ./internal/gateway/ -run 'TestGatewaySendsModelAndStage|TestTheAgentNeverSeesTheSource' -count=1`
- Red step: `TestGatewaySendsModelAndStage` fails: no such header is sent.
- Depends on: C1 (the contract's wire names).
- Commit: `model-credentials-routing CS2: the gateway says which model and stage a call is for`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CS3: one model, each source's spelling

- Module: `chain-serving`.
- Acceptance: The checked-in map in `internal/pricing/canonical.go` gains a Vertex spelling per canonical Claude id beside the route id; when a call moves to a source whose spelling differs, the service rewrites the request's `model` member to that source's spelling and nothing else in the body; a source entry that names a model (a written downgrade) is the same rewrite to the named model; a model with no spelling for a source is never sent there. The cost is priced by the model that served.
- Files: `internal/pricing/canonical.go`, `internal/pricing/canonical_test.go`, `internal/credsvc/rewrite.go` (new), `internal/credsvc/rewrite_test.go` (new).
- Interfaces: Consumes C1's `Source.Model`. Produces `pricing.SpellingFor`, which RT3's unmapped-spelling rule and CS1 call through the contract.
- Tests: `TestSpellingFor`, `TestRewriteTouchesOnlyTheModel`, `TestAWrittenDowngradeIsPricedAsWhatServed`.
- Command: `go test ./internal/pricing/ -run 'TestSpellingFor' -count=1` and `go test ./internal/credsvc/ -run 'TestRewrite|TestAWrittenDowngrade' -count=1`
- Red step: `TestRewriteTouchesOnlyTheModel` fails before the file exists.
- Depends on: C1 (the contract's source shape).
- Commit: `model-credentials-routing CS3: one model, each source's spelling`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CS4: the Vertex leg

- Module: `chain-serving`.
- Acceptance: When the service's answer names Vertex as the next source, the gateway sends that call to Vertex on the job's own identity with the Vertex spelling and reports the source as Vertex; a chain whose first source is Vertex never calls the service for that model; a Vertex 429 is reported back so the chain moves on or answers exhausted.
- Files: `internal/gateway/vertex.go`, `internal/gateway/vertexleg.go` (new), `internal/gateway/vertexleg_test.go` (new), `internal/gateway/service.go`.
- Interfaces: Consumes C1's `next: vertex` member and CS2's wire file. Produces nothing a task calls.
- Tests: `TestVertexLegOnTheJobsIdentity`, `TestAVertexLimitMovesOn`.
- Command: `go test ./internal/gateway/ -run 'TestVertexLeg|TestAVertexLimitMovesOn' -count=1`
- Red step: `TestVertexLegOnTheJobsIdentity` fails: the answer's member is ignored.
- Depends on: CS2 (kept: one file, `service.go`).
- Commit: `model-credentials-routing CS4: the Vertex leg`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CS5: Vertex is held to the cap

- Module: `chain-serving`.
- Acceptance: Under `budget.mode: enforce` the gateway prices every Vertex call and holds it to the run's lease and caps exactly as a key call, refusing the call that would pass the cap; `VertexBudgetRefusal` and the refusal of a pool with a Vertex member are removed; `SECURITY.md`'s sentence that Vertex has no dollar cap is left for DOC1 and its Docs test is updated there, not here.
- Files: `internal/gateway/ledger.go`, `internal/gateway/vertex_cap_test.go` (new), `internal/runner/gateway.go`, `internal/runner/poolchoice.go`, `internal/config/stage_accounts.go`.
- Interfaces: Consumes nothing of this plan. Produces a Vertex that is an ordinary paid source (D3174).
- Tests: `TestVertexIsHeldToTheCap`, `TestVertexIsNoLongerRefusedUnderEnforce`.
- Command: `go test ./internal/gateway/ -run 'TestVertexIsHeldToTheCap' -count=1` and `go test ./internal/runner/ -run 'TestVertexIsNoLongerRefusedUnderEnforce' -count=1`
- Red step: `TestVertexIsHeldToTheCap` fails: a Vertex call past the cap is forwarded.
- Depends on: none.
- Commit: `model-credentials-routing CS5: Vertex is held to the cap`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CS6: the run's model path follows the table

- Module: `chain-serving`.
- Acceptance: The runner no longer chooses a whole-run upstream from `agent.auth`: every stage's calls go through the gateway to the service (or the Vertex leg), the model per stage is chosen as today, and an unmigrated repository on a release-1 image still runs as its file says (credentials-belong-to-models R3, kept).
- Files: `internal/runner/routing.go`, `internal/runner/gateway.go`, `internal/runner/runner.go`, `internal/runner/routing_table_test.go` (new).
- Interfaces: Consumes C1. Produces the path CS7 and PR2 extend.
- Tests: `TestEveryStageGoesThroughTheChain`, `TestAnUnmigratedRepositoryRunsAsItsFileSays`.
- Command: `go test ./internal/runner/ -run 'TestEveryStageGoesThroughTheChain|TestAnUnmigratedRepositoryRunsAsItsFileSays' -count=1`
- Red step: `TestEveryStageGoesThroughTheChain` fails: an `oauth` run has no gateway.
- Depends on: C1 (the contract's names); CS5 (kept: one file, `runner/gateway.go`).
- Gate: **Not launched before always-a-budget's AB1b has merged (one file, as that plan's CM8 was held).**
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
- Commit: `model-credentials-routing CS7: the agent's environment, and the account rules that go`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CS8: the record says what served each step

- Module: `chain-serving`.
- Acceptance: The run record gains, per stage, `source` (kind and name, never a value) and `fallback` (true when any call of the stage was served by a source that is not the chain's first), and `cost.fallback_usd` (the real money spent on fallback sources); existing members keep their meaning; the result schema lists the new members; a paid fallback is settled against the caps as any key call (D140).
- Files: `internal/runstore/runstore.go`, `internal/runstore/cost.go`, `internal/runner/cost.go`, `internal/runner/stephook.go`, `schemas/result.schema.json`.
- Interfaces: Consumes C1's member names and CS2's hook through the contract. Produces the members VS1 and VS4 read.
- Tests: `TestTheRecordNamesTheSource`, `TestFallbackUSDIsRealMoneyOnly`.
- Command: `go test ./internal/runner/ -run 'TestTheRecordNamesTheSource|TestFallbackUSDIsRealMoneyOnly' -count=1`
- Red step: `TestTheRecordNamesTheSource` fails: the member does not exist.
- Depends on: C1 (the contract's member names).
- Commit: `model-credentials-routing CS8: the record says what served each step`
- Reviewer tier: standard.

### CS9: the registry entry and the live views

- Module: `chain-serving`.
- Acceptance: The live registry entry gains `source`; `auth` is still written, as `-`, so an older console parses it (D3070, kept); the rules template admits the new member; `fugaro watch` and its `--json` show the source of the running stage.
- Files: `internal/budget/model.go`, `internal/budget/registry.go`, `internal/budget/rules/rules.json.tmpl`, `internal/watch/json.go`, `internal/watch/render.go`.
- Interfaces: Consumes C1's member names. Produces the entry PR1, PR7 and the console read.
- Tests: `TestTheEntryCarriesTheSource`, `TestWatchJSONKeepsAuth`.
- Command: `go test ./internal/budget/ -run 'TestTheEntryCarriesTheSource' -count=1` and `go test ./internal/watch/ -run 'TestWatchJSONKeepsAuth' -count=1`
- Red step: `TestTheEntryCarriesTheSource` fails: the rules refuse the member.
- Depends on: C1 (the contract's member names).
- Commit: `model-credentials-routing CS9: the registry entry and the live views`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CS10: the launch

- Module: `chain-serving`.
- Acceptance: `fugaro run` asks the slip for the sources the run's models can reach by the table, tells the job about the service wherever a chain has a served source, refuses a launch whose model no entry matches with the fix, and rejects `--token` naming `fugaro accounts pool designate` (D3176); it asks nothing.
- Files: `internal/cli/run.go`, `internal/cli/run_attach.go`, `internal/cli/launch_budget.go`, `internal/cli/project_routing.go`, `internal/cli/run_chain_test.go` (new).
- Interfaces: Consumes C1 and CS11's plan of the cloud side. Produces the launch every later live check uses.
- Tests: `TestRunGrantsByTheTable`, `TestRunRefusesAnUnroutedModel`, `TestTokenFlagIsRejectedWithTheFix`.
- Command: `go test ./internal/cli/ -run 'TestRunGrantsByTheTable|TestRunRefusesAnUnroutedModel|TestTokenFlagIsRejectedWithTheFix' -count=1`
- Red step: `TestTokenFlagIsRejectedWithTheFix` fails: the flag is accepted.
- Depends on: C1 (the contract's names); CS11 (kept, behaviour: it decides whether a job is told about the service from what CS11 plans).
- Commit: `model-credentials-routing CS10: the launch`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CS11: the cloud side is planned from the table

- Module: `chain-serving`.
- Acceptance: The infra spec derives what a job mounts and whether it is told about the service from the table and the pools (no secret mounted for a served source), not from `agent.auth`; `ModelSecretsServed` answers from the table; the Vertex permission is planned wherever a chain names Vertex.
- Files: `internal/infra/spec.go`, `internal/infra/chain_spec.go` (new), `internal/infra/chain_spec_test.go` (new), `internal/infra/readiness.go`, `internal/config/providers.go`.
- Interfaces: Consumes C1. Produces the plan CS10 reads.
- Tests: `TestSpecFromTheTable`, `TestVertexIsPlannedWhereNamed`.
- Command: `go test ./internal/infra/ -run 'TestSpecFromTheTable|TestVertexIsPlannedWhereNamed' -count=1`
- Red step: `TestSpecFromTheTable` fails before the file exists.
- Depends on: C1 (the contract's names).
- Gate: **Not launched before always-a-budget's AB15 has merged (both edit `spec.go`).**
- Commit: `model-credentials-routing CS11: the cloud side is planned from the table`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

## Slice 5: `migration`, and the first integration

### MG1: the migration, as a function

- Module: `migration`.
- Acceptance: One pure function takes an installation's config and each repository's file and returns the table, the pools and the admissions that keep every run where it was: `oauth` becomes the account's pool, `api-key` the Anthropic key, `cascade` the shared pool then the key where `fallback: auto` was set, `vertex` a chain naming Vertex; `routing.claude.via` becomes the Claude entry (D3173); every ownerless account lands in one shared pool, same members and order (D3183), and a lone account also gets a pool of one (D3176); per-stage accounts are folded into one pool (D3171); each repository is admitted only to the sources it used (D3182). A table-driven test covers every value of `agent.auth` and `via`, and two repositories that differ.
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
- Acceptance: `fugaro update` on an installation at the previous version writes the table, pools and admissions `routing.Migrate` returns into the installation's config, publishes them, and prints before any write one line per repository saying what its runs go out on and that nothing moves; it asks nothing, is safe with runs in flight, and a second run changes nothing; the `e2e` update test converges from a fixture of the previous version.
- Files: `internal/cli/update_routing_migrate.go` (new), `internal/cli/update_routing_migrate_test.go` (new), `internal/cli/update.go`, `internal/cli/init_craft_migrate.go`, `internal/e2e/update_test.go`.
- Interfaces: Consumes MG1's function, RT2's block, RT4's publisher and PL1's fields. Produces the migrated installation I1 proves.
- Tests: `TestUpdateMigratesToTheTable`, `TestUpdateSaysWhatChangesFirst`, `TestUpdateMigrationIsIdempotent`.
- Command: `go test ./internal/cli/ -run 'TestUpdateMigratesToTheTable|TestUpdateSaysWhatChangesFirst|TestUpdateMigrationIsIdempotent' -count=1`
- Red step: `TestUpdateMigratesToTheTable` fails: `update` writes no table.
- Depends on: MG1 (kept, behaviour: it writes what the function answers, and a stub's answer would be written to a real installation); RT4 (kept, behaviour: it publishes through RT4's publisher); PL1 (kept: it writes the pool fields PL1 adds).
- Commit: `model-credentials-routing MG2: `fugaro update` migrates, and says what changes first`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### MG3: the old record is written beside the table for one release

- Module: `migration`.
- Acceptance: For release 1 `fugaro update` keeps writing `creds/repoauth/<slug>` as today, so a run that bootstrapped on the release behind still resolves; the service reads the table first and the old record only for a run whose image predates the table; the update's in-flight line counts such runs.
- Files: `internal/cli/update_repoauth.go`, `internal/cli/update_repoauth_table_test.go` (new), `internal/credsvc/legacy_auth.go` (new), `internal/credsvc/legacy_auth_test.go` (new).
- Interfaces: Consumes C1. Produces the bridge X4 removes.
- Tests: `TestTheOldRecordIsStillWritten`, `TestAnOlderRunStillResolves`.
- Command: `go test ./internal/cli/ -run 'TestTheOldRecordIsStillWritten' -count=1` and `go test ./internal/credsvc/ -run 'TestAnOlderRunStillResolves' -count=1`
- Red step: `TestAnOlderRunStillResolves` fails once CS1 is in and before this bridge exists; before CS1 it is written to fail on the missing file.
- Depends on: C1 (the contract's names).
- Commit: `model-credentials-routing MG3: the old record is written beside the table for one release`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### MG4: the account and craft commands stop writing access

- Module: `migration`.
- Acceptance: `fugaro accounts switch`, `alias`, `craft` and `cascade` no longer write a craft's access or a repository's auth mode: each either writes the table or pool it now means, or is refused with the fix naming `fugaro routing set` or `fugaro accounts pool`; none asks a question.
- Files: `internal/cli/accounts_switch.go`, `internal/cli/accounts_alias.go`, `internal/cli/accounts_craft.go`, `internal/cli/accounts_cascade.go`, `internal/cli/accounts_routing_test.go` (new).
- Interfaces: Consumes RT5's command and RT2's block. Produces nothing a task calls.
- Tests: `TestAccountsCommandsWriteNoAccess` (one sub-test per command).
- Command: `go test ./internal/cli/ -run 'TestAccountsCommandsWriteNoAccess' -count=1`
- Red step: `TestAccountsCommandsWriteNoAccess/switch` fails: it writes `agent.auth`.
- Depends on: RT5 (kept, behaviour: its refusals name a command that must exist, and the plugin's tests check the name).
- Commit: `model-credentials-routing MG4: the account and craft commands stop writing access`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### MG5: the settings registry and the schemas

- Module: `migration`.
- Acceptance: The settings registry and `schemas/settings.schema.json` list the table, a pool's rule and owner, and mark `agent.auth`, a craft's access, `routing.claude`, `credentials.fallback`, `credentials.wait_threshold` and `subscription_spread` as retired with the note `config.RetiredKeyNotes` prints; `schemas/fugaro.schema.json` and `schemas/project-layer.schema.json` still accept the retired keys for release 1 and describe them as retired.
- Files: `internal/settings/registry.go`, `schemas/settings.schema.json`, `schemas/fugaro.schema.json`, `schemas/project-layer.schema.json`, `internal/config/retired.go`.
- Interfaces: Consumes RT4's and PL1's struct members. Produces the registry PR9 extends.
- Tests: `TestRegistryCoversEveryMember`, `TestRetiredKeysAreNamed`.
- Command: `go test ./internal/settings/ -run 'TestRegistryCoversEveryMember' -count=1` and `go test ./internal/config/ -run 'TestRetiredKeysAreNamed' -count=1`
- Red step: `TestRetiredKeysAreNamed` fails: `routing.claude` has no note.
- Depends on: RT4 (kept: one file, `registry.go`); PL1 (kept: the registry's coverage test reads the struct PL1 changes).
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
- Depends on: CS10 (kept: one file, `run.go`); RT6 (kept: one file, `models_use.go`).
- Commit: `model-credentials-routing MG6: the help texts and the CLI reference`
- Reviewer tier: standard.

### I1: integration: the chain serves a migrated installation

- Module: `chain-serving`.
- Acceptance: The stubs of C1 and C2 are deleted; an end-to-end test migrates a fixture installation of the previous version with `update`, launches a run against the fakes and proves: the coder's calls go out on the first source of its model's chain, an exhausted pool moves the next call to the next source with the step flagged and the paid money counted, a repository not admitted to a source skips it, a person with no pool is served by the next source, and the agent's environment holds no credential; a guard test finds no reader of `agent.auth` or a craft's access on the serving path outside the release-1 bridge; the console fixtures of VS1 and VS2 are replaced by the output this test records, and their tests still pass.
- Files: `internal/routing/resolve_stub.go` (deleted), `internal/credsvc/chain_stub.go` (deleted), `internal/credsvc/launcherpool_stub.go` (deleted), `internal/cli/chain_end_to_end_test.go` (new), `internal/config/retired_readers_guard_test.go` (new).
- Interfaces: Consumes every task of slices 2 to 5. Produces release 1's proof.
- Tests: `TestChainEndToEnd`, `TestNoReaderOfTheRetiredKeys`.
- Command: `go test ./internal/cli/ -run 'TestChainEndToEnd' -count=1` and `go test ./internal/config/ -run 'TestNoReaderOfTheRetiredKeys' -count=1`
- Red step: `TestChainEndToEnd` fails while any stub answers.
- Depends on: A1, A2, RT1, RT2, RT3, RT4, RT5, RT6, RT7, PL1, PL2, PL3, PL4, PL5, PL6, CS1, CS2, CS3, CS4, CS5, CS6, CS7, CS8, CS9, CS10, CS11, MG1, MG2, MG3, MG4, MG5, MG6, VS1, VS2, VS4 (the sides it wires).
- Commit: `model-credentials-routing I1: integration: the chain serves a migrated installation`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

## Slice 6: `pause-resume`, and its integration

### PR1: the status word `paused`

- Module: `pause-resume`.
- Acceptance: `paused` is a status in `runstore.Statuses`, the result schema, the budget rules' outcome words and the digest; it is terminal for the execution and is never counted as a failure or a success; `fugaro update` migrates the rules in place and an installation at the previous rules version converges; no existing word changes meaning (R15).
- Files: `internal/runstore/runstore.go`, `schemas/result.schema.json`, `internal/budget/registry.go`, `internal/budget/rules/rules.json.tmpl`, `internal/digest/digest.go`.
- Interfaces: Consumes C3's constant. Produces the word every reader of a status meets.
- Tests: `TestPausedIsAStatus`, `TestTheRulesAdmitPaused`, `TestDigestCountsPausedApart`.
- Command: `go test ./internal/runstore/ -run 'TestPausedIsAStatus' -count=1`, `go test ./internal/budget/ -run 'TestTheRulesAdmitPaused' -count=1` and `go test ./internal/digest/ -run 'TestDigestCountsPausedApart' -count=1`
- Red step: `TestTheRulesAdmitPaused` fails: the rules refuse an unknown word.
- Depends on: C3 (the contract's constant); CS8 (kept: two files, `runstore.go` and `result.schema.json`); CS9 (kept: two files, `registry.go` and `rules.json.tmpl`); PL4 (kept: one file, `rules.json.tmpl`).
- Commit: `model-credentials-routing PR1: the status word `paused``
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PR2: the run suspends itself

- Module: `pause-resume`.
- Acceptance: When the service answers that every source is exhausted, the runner decides once: a hotfix run halts with `HaltNoCredential` (R10); a reset nearer than `credentials.wait_in_job` is waited out in the job as today (D3184); a reset beyond `credentials.max_pause`, or a service answer without `can_resume`, halts at once with the reset time in the reason (D3187, D3186); otherwise the run saves its session, pushes the work in progress as a checkpoint, writes the paused record, ends `paused` and exits with its lease released (R12, R18). No proxy call is held open.
- Files: `internal/runner/suspend.go` (new), `internal/runner/suspend_test.go` (new), `internal/runner/creds_wait.go`, `internal/runner/halt.go`, `internal/runner/runner.go`.
- Interfaces: Consumes C3's record and the settings' names. Produces the suspended run PR4 resumes.
- Tests: `TestSuspendDecision` (one sub-test per branch above), `TestSuspendSavesBeforeItExits`, `TestSuspendReleasesTheLease`.
- Command: `go test ./internal/runner/ -run 'TestSuspend' -count=1`
- Red step: `TestSuspendDecision/pause` fails against the stub, which waits in the job.
- Depends on: C3 (the contract's record); CS6 (kept: one file, `runner.go`).
- Commit: `model-credentials-routing PR2: the run suspends itself`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PR3: the service binds a paused run to its launcher

- Module: `pause-resume`.
- Acceptance: When a run's slip is used for a call the service answers exhausted, the service records under `paused/` the launcher from that slip, never from the job; a slip granted for a run that names `resumes` is bound to the recorded launcher of the run it resumes and to nobody else, so the resumed run draws on the original launcher's pool (R8); the exhausted answer carries `can_resume` only where the installation has a resident orchestrator; a mutation test proves a job that writes another person's name into its own record changes nothing.
- Files: `internal/credsvc/pause.go` (new), `internal/credsvc/pause_test.go` (new), `internal/credsvc/slip.go`, `internal/creds/slips.go`, `internal/credsvc/cascade.go`.
- Interfaces: Consumes C3's node and member. Produces the binding PR4's launch meets.
- Tests: `TestThePausedLauncherIsTheSlips`, `TestAResumedRunDrawsOnTheOriginalLaunchersPool`, `TestAJobCannotNameAnotherLauncher`.
- Command: `go test ./internal/credsvc/ -run 'TestThePausedLauncherIsTheSlips|TestAResumedRunDrawsOnTheOriginalLaunchersPool|TestAJobCannotNameAnotherLauncher' -count=1`
- Red step: `TestAResumedRunDrawsOnTheOriginalLaunchersPool` fails: a reissued run has the orchestrator as its launcher.
- Depends on: C3 (the contract's node and member); CS1 (kept: one file, `cascade.go`); PL6 (kept, behaviour: a resumed run of a service account's launch resolves through PL6's lookup).
- Commit: `model-credentials-routing PR3: the service binds a paused run to its launcher`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PR4: the resident orchestrator resumes what is due

- Module: `pause-resume`.
- Acceptance: On each tick the resident orchestrator reads the paused records whose resume time has passed and reissues each once as a run that continues the paused one (the same chain, `resumes` naming it); a record already resumed, cancelled by its launcher or past `max_pause` is not reissued and the last is ended as halted with the reason; two ticks never resume one record twice.
- Files: `internal/orchestrator/resume.go` (new), `internal/orchestrator/resume_test.go` (new), `internal/orchestrator/tick.go`, `internal/orchestrator/resident_launch.go`, `internal/orchestrator/intent.go`.
- Interfaces: Consumes C3's record and node. Produces the resumed launch PR5's run receives.
- Tests: `TestADuePauseIsResumedOnce`, `TestACancelledPauseIsNotResumed`, `TestAPausePastTheMaximumHalts`.
- Command: `go test ./internal/orchestrator/ -run 'TestADuePauseIsResumedOnce|TestACancelledPauseIsNotResumed|TestAPausePastTheMaximumHalts' -count=1`
- Red step: `TestADuePauseIsResumedOnce` fails against the stub, which resumes nothing.
- Depends on: C3 (the contract's record).
- Gate: **Not launched before launcher-does-not-mint's I1 has merged: the orchestrator's reissue is issued its identity by what that task wires (U4).**
- Commit: `model-credentials-routing PR4: the resident orchestrator resumes what is due`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PR5: the resumed run picks up where the paused one stopped

- Module: `pause-resume`.
- Acceptance: A run whose task names `resumes` checks out the checkpoint, restores the saved session and enters the stage the paused run was in, with the chain's remaining budget (D1045 to D1047, unchanged); a paused record whose format version this image does not read, a missing session or a head that is not the checkpoint ends the run with a named outcome and never starts the stage afresh (R18).
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
- Acceptance: `fugaro watch` and `fugaro runs ls` show a paused run as paused with its resume time and the run that resumed it, in text and in `--json` (new members, none retyped); `fugaro run` of a task that pauses returns and does not block; `fugaro cancel` of a paused run marks its record cancelled so it is never resumed.
- Files: `internal/watch/render.go`, `internal/watch/json.go`, `internal/cli/runs.go`, `internal/cli/runs_show.go`, `internal/cli/runs_paused_test.go` (new).
- Interfaces: Consumes C3's node and record. Produces the JSON the console reads (VS3).
- Tests: `TestWatchShowsAPausedRun`, `TestRunsLsShowsThePauseAndItsResume`, `TestCancelOfAPausedRun`.
- Command: `go test ./internal/cli/ -run 'TestRunsLsShowsThePauseAndItsResume|TestCancelOfAPausedRun' -count=1` and `go test ./internal/watch/ -run 'TestWatchShowsAPausedRun' -count=1`
- Red step: `TestWatchShowsAPausedRun` fails: a paused run has no entry and is not listed.
- Depends on: C3 (the contract's node); CS9 (kept: two files, `render.go` and `json.go`).
- Commit: `model-credentials-routing PR7: `watch` and `runs ls` show a paused run`
- Reviewer tier: standard.

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

### PR9: the two settings: the in-job floor and the maximum pause

- Module: `pause-resume`.
- Acceptance: The policy's credentials block gains `wait_in_job` (default 5 minutes) and `max_pause` (default 6 hours), validated (`wait_in_job` under `max_pause`), written by `fugaro settings set`, published by `update` with the node migrated in place, and read by the service's exhausted answer; `wait_threshold` is still decoded and no longer read.
- Files: `internal/projectpolicy/policy.go`, `internal/settings/registry.go`, `schemas/settings.schema.json`, `internal/cli/settings_cloud.go`, `internal/cli/init_policy_publish.go`.
- Interfaces: Consumes nothing of the contract. Produces the two names PR2 reads through the answer.
- Tests: `TestPauseSettingsDefaults`, `TestWaitInJobMustBeUnderMaxPause`.
- Command: `go test ./internal/projectpolicy/ -run 'TestPauseSettings|TestWaitInJobMustBeUnderMaxPause' -count=1`
- Red step: `TestPauseSettingsDefaults` fails: the policy decodes strictly and has no such member.
- Depends on: RT4 (kept: two files, `policy.go` and `init_policy_publish.go`); MG5 (kept: two files, `registry.go` and `settings.schema.json`).
- Commit: `model-credentials-routing PR9: the two settings: the in-job floor and the maximum pause`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### I2: integration: a run pauses and is resumed

- Module: `pause-resume`.
- Acceptance: C3's stubs are deleted; an end-to-end test against the fakes exhausts every source of a chain with a reset an hour away, sees the run end `paused` with its record and node written and its lease released, advances the clock, sees the orchestrator reissue it once, and sees the resumed run restore the session, draw on the original launcher's pool and finish; the same test with a hotfix run, with a reset past the maximum and with no orchestrator each halts at once; the fixtures of VS3 and VS4 are replaced by what this test records, and their tests still pass.
- Files: `internal/runner/suspend_stub.go` (deleted), `internal/orchestrator/resume_stub.go` (deleted), `internal/cli/pause_end_to_end_test.go` (new).
- Interfaces: Consumes every task of slice 6. Produces the proof of `pause-resume`.
- Tests: `TestPauseEndToEnd` (four sub-tests).
- Command: `go test ./internal/cli/ -run 'TestPauseEndToEnd' -count=1`
- Red step: `TestPauseEndToEnd/pause_and_resume` fails while a stub answers.
- Depends on: PR1, PR2, PR3, PR4, PR5, PR6, PR7, PR8, PR9, VS3, VS4 (the sides it wires); I1 (kept, behaviour: the test exhausts a real chain, which exists only once I1 has wired it).
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
- Acceptance: The live and run screens show a paused run as paused with its resume time and a link to the run that resumed it; the status set gains `paused`; an unknown status still draws as unknown rather than failing.
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
- Acceptance: `SECURITY.md`, the website's security page and `.claude/rules/credentials-service.md` describe the tree as it is: the service owns the table, the gateway holds no key, Vertex is capped, a personal pool serves only its owner's runs, the launcher of a paused run is the service's record; the pinned sentences change in the document and its test together.
- Files: `SECURITY.md`, `website/content/security.md`, `.claude/rules/credentials-service.md`, `internal/cli/docs_security_test.go`.
- Interfaces: Consumes the tree after I1 and I2.
- Tests: The Docs tests; the website's check.
- Command: `go test ./internal/cli/ -run 'Docs' -count=1` and `cd website && npm run check`
- Red step: The Docs test that pins "Vertex has no dollar cap" fails once the sentence is corrected, until the test is changed with it.
- Depends on: I1 (kept: a security statement must be true of the tree it describes); I2 (kept, as I1).
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
- Acceptance: `docs/project-claude-accounts.md`, `README.md` and the release notes of release 1 describe the table and pools; `### For operators` says "run `fugaro update`" and what it prints, and nothing else; the website's summary line is added.
- Files: `docs/project-claude-accounts.md`, `README.md`, `docs/releases/v-next.md` (the release's own file), `website/data/release-summaries.yaml`.
- Interfaces: Consumes the tree after I1 and I2.
- Tests: The Docs tests; the website's check.
- Command: `go test ./internal/cli/ -run 'Docs' -count=1` and `cd website && npm run check`
- Red step: None: a documents task.
- Depends on: DOC2 (kept: its tests pin `docs/multi-model.md`).
- Commit: `model-credentials-routing DOC3: the accounts documents, the README and the release notes`
- Reviewer tier: standard.

### DOC4: the plugin's skills

- Module: `migration`.
- Acceptance: The `setup` and `working` skills say how a model's source is set (`fugaro routing`, `fugaro accounts pool`), what a paused run is and that it needs nothing from the user, and name no `--token` and no auth mode; the plugin's tests pass against the CLI.
- Files: `plugin/skills/setup/SKILL.md`, `plugin/skills/setup/reference/decisions.md`, `plugin/skills/working/SKILL.md`.
- Interfaces: Consumes the CLI after I1 and I2.
- Tests: The plugin's tests.
- Command: `go test ./plugin/... -count=1`
- Red step: The plugin's test of documented flags fails while a skill names `--token`.
- Depends on: DOC3 (kept: the documents tasks run one after another over shared tests); PL5 (kept: one file, `decisions.md`); RT5 (kept: one file, `setup/SKILL.md`).
- Commit: `model-credentials-routing DOC4: the plugin's skills`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### CK1: Checks 871 to 875, as scripts

- Module: `chain-serving`.
- Acceptance: One script per check, each run by the owner in his own session and printing no value, each with a test against a fake: 871 a live chain falls through from an exhausted pool to the key, flagged and counted; 872 the Vertex leg serves each mapped model under the cap; 873 a run pauses and is resumed by the orchestrator; 874 `fugaro update` migrates the dev installation and no repository's source moves; 875 after release 2 a retired key is rejected with the fix.
- Files: `scripts/live-checks/check-871-chain-falls-through.sh` (new), `scripts/live-checks/check-872-vertex-leg.sh` (new), `scripts/live-checks/check-873-pause-resume.sh` (new), `scripts/live-checks/check-874-update-migrates.sh` (new), `scripts/live-checks/check-875-retired-keys.sh` (new).
- Interfaces: Consumes nothing. Produces the owner's checks.
- Tests: Each script's `_test.sh` beside it.
- Command: `for t in scripts/live-checks/check-87[1-5]-*_test.sh; do bash "$t"; done`
- Red step: Each test fails before its script exists.
- Depends on: none.
- Commit: `model-credentials-routing CK1: Checks 871 to 875, as scripts`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).
- Owner-run: The owner runs each script himself, 871 to 874 after release 1 is installed through the real `fugaro update`, 875 after release 2. No agent runs one.

## Slice 9: release 2, the removals

### X1: release 2: the subscription token's old path goes

- Module: `migration`.
- Acceptance: No job image receives a subscription token or a model key in its environment on any path; a launch on an image older than release 1 is refused by the image-age gate with the fix; the release-1 branch of the agent's environment is deleted.
- Files: `internal/agent/env.go`, `internal/runner/agentenv.go`, `internal/cli/launch_budget.go`, `internal/cli/recipes_skew.go`, `internal/agent/env_release2_test.go` (new).
- Interfaces: Consumes I1. Produces the gate X2 relies on.
- Tests: `TestNoCredentialInAnyEnvironment`, `TestAnOlderImageIsRefused`.
- Command: `go test ./internal/agent/ -run 'TestNoCredentialInAnyEnvironment' -count=1` and `go test ./internal/cli/ -run 'TestAnOlderImageIsRefused' -count=1`
- Red step: `TestNoCredentialInAnyEnvironment` fails on the release-1 branch.
- Depends on: I1 (kept, behaviour: it deletes what I1 proves unused); DOC1 (kept: one file, `SECURITY.md`'s test).
- Gate: **Not launched before release 1 is cut and the keys plan's KS13 has merged.**
- Commit: `model-credentials-routing X1: release 2: the subscription token's old path goes`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### X2: release 2: the retired keys are rejected

- Module: `migration`.
- Acceptance: `agent.auth`, a craft's `access`, `agent.oauth_token` and the per-stage account keys in a `fugaro.yaml` or a project layer, and `routing.claude` in the installation's config, are rejected at `validate`, `update` and a default-branch launch with the fix; an older ref reads them as ignored; no `fugaro.yaml` version bump (D3072, kept).
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
- Acceptance: `fugaro update` removes `subscription_spread`, `credentials.fallback`, `credentials.wait_threshold`, the craft credentials and the account-stage keys from the installation's config and the published policy, migrating the node in place; an installation at release 1 converges; the pool marker `cascade` is no longer read.
- Files: `internal/localcfg/localcfg.go`, `internal/projectpolicy/policy.go`, `internal/cli/update_routing_migrate.go`, `internal/config/craft.go`, `internal/cli/update_release2_test.go` (new).
- Interfaces: Consumes X2.
- Tests: `TestUpdateDropsTheOldMembers`, `TestUpdateConvergesFromRelease1`.
- Command: `go test ./internal/cli/ -run 'TestUpdateDropsTheOldMembers|TestUpdateConvergesFromRelease1' -count=1`
- Red step: `TestUpdateDropsTheOldMembers` fails: the members are still written.
- Depends on: X2 (kept: the schema test both change).
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

## Parallel execution

Tasks in one level have no edge between them and may run at once. A gate (a task of another
plan, a release cut, an owner's check) can hold a task past its level; each is on its task.

| Level | Tasks |
| --- | --- |
| 1 | A1, A2, A3, C3, PL7, CS5, CK1 |
| 2 | C1, C2, PR4, PR5, PR8 |
| 3 | RT1, RT2, PL3, CS1, CS2, CS3, CS6, CS7, CS8, CS9, CS11, MG1, MG3, VS1, VS2, VS4 |
| 4 | RT3, PL1, PL4, PL8, CS4, CS10, PR2, PR6, PR7, VS3 |
| 5 | RT4, RT5, RT6, PL2, PL6, PR1 |
| 6 | RT7, PL5, MG2, MG4, MG5, MG6, PR3 |
| 7 | I1, PR9 |
| 8 | I2, X5 |
| 9 | DOC1 |
| 10 | DOC2, X1 |
| 11 | DOC3, X2, X4 |
| 12 | DOC4, X3 |

**The critical path** as `fugaro plan graph` reports it, 12 tasks:
A1, C1, RT1, RT3, RT4, MG2, I1, I2, DOC1, DOC2, DOC3, DOC4. What is sequential and why: the
contract waits on the document that withdraws the earlier contract; the checks call the
resolver's real answer; the publisher refuses by the checks; the update writes through the
publisher; integration joins every side; the pause's integration exhausts a real chain; and the
four documents tasks share pinned tests and run one after another.

**What holds a task besides its edges:** PL4 waits on workspace-credentials' I1; PR4 on
launcher-does-not-mint's I1; CS6 on always-a-budget's AB1b; CS11 on always-a-budget's AB15; PL8
on the result of Check 870; X1 on release 1 being cut and the keys plan's KS13; X5 on release 1
being cut. OpenRouter for a Claude model waits on Check 50 (R3) and holds no task.

### Shared files

No level holds two writers of one file: every pair below is ordered by an edge, directly or
through another task. A change to any task's Files re-checks this table.

| File | Writers | Order |
| --- | --- | --- |
| `console/src/renderer/screens/live.ts` | VS1, VS3 | VS1 then VS3 |
| `console/src/renderer/screens/run.ts` | VS1, VS3 | VS1 then VS3 |
| `console/src/shared/schema.ts` | VS1, VS3 | VS1 then VS3 |
| `docs/gcp-live-checklist.md` | PL7, DOC2 | PL7 then DOC2 |
| `internal/agent/env.go` | CS7, X1 | CS7 then X1 |
| `internal/budget/registry.go` | CS9, PR1 | CS9 then PR1 |
| `internal/budget/rules/rules.json.tmpl` | PL4, CS9, PR1 | CS9 then PL4 then PR1 |
| `internal/cli/accounts_pool.go` | PL2, PL5 | PL2 then PL5 |
| `internal/cli/init_policy_publish.go` | RT4, PR9 | RT4 then PR9 |
| `internal/cli/launch_budget.go` | CS10, X1 | CS10 then X1 |
| `internal/cli/models_use.go` | RT6, MG6 | RT6 then MG6 |
| `internal/cli/run.go` | CS10, MG6 | CS10 then MG6 |
| `internal/cli/update_in_flight.go` | PR6, X4 | PR6 then X4 |
| `internal/cli/update_repoauth.go` | MG3, X4 | MG3 then X4 |
| `internal/cli/update_routing_migrate.go` | MG2, X3 | MG2 then X3 |
| `internal/config/craft.go` | PL1, X3 | PL1 then X3 |
| `internal/creds/poolrule.go` | PL3, PL8 | PL3 then PL8 |
| `internal/credsvc/cascade.go` | CS1, PR3 | CS1 then PR3 |
| `internal/credsvc/chain_stub.go` | C1, I1 | C1 then I1 |
| `internal/credsvc/launcherpool.go` | PL4, PL6 | PL4 then PL6 |
| `internal/credsvc/launcherpool_stub.go` | C2, I1 | C2 then I1 |
| `internal/credsvc/legacy_auth.go` | MG3, X4 | MG3 then X4 |
| `internal/gateway/service.go` | CS2, CS4, X4 | CS2 then CS4 then X4 |
| `internal/localcfg/localcfg.go` | RT2, PL1, X3 | RT2 then PL1 then X3 |
| `internal/orchestrator/resume_stub.go` | C3, I2 | C3 then I2 |
| `internal/projectpolicy/policy.go` | RT4, PR9, X3 | RT4 then PR9 then X3 |
| `internal/routing/resolve_stub.go` | C1, I1 | C1 then I1 |
| `internal/runner/agentenv.go` | CS7, X1 | CS7 then X1 |
| `internal/runner/gateway.go` | CS5, CS6 | CS5 then CS6 |
| `internal/runner/poolchoice.go` | CS5, CS7 | CS5 then CS7 |
| `internal/runner/runner.go` | CS6, PR2 | CS6 then PR2 |
| `internal/runner/suspend_stub.go` | C3, I2 | C3 then I2 |
| `internal/runstore/runstore.go` | CS8, PR1 | CS8 then PR1 |
| `internal/settings/registry.go` | RT4, MG5, PR9 | RT4 then MG5 then PR9 |
| `internal/watch/json.go` | CS9, PR7 | CS9 then PR7 |
| `internal/watch/render.go` | CS9, PR7 | CS9 then PR7 |
| `plugin/skills/setup/SKILL.md` | RT5, DOC4 | RT5 then DOC4 |
| `plugin/skills/setup/reference/decisions.md` | PL5, DOC4 | PL5 then DOC4 |
| `schemas/fugaro.schema.json` | MG5, X2 | MG5 then X2 |
| `schemas/project-layer.schema.json` | MG5, X2 | MG5 then X2 |
| `schemas/result.schema.json` | CS8, PR1 | CS8 then PR1 |
| `schemas/settings.schema.json` | MG5, PR9 | MG5 then PR9 |

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
| PL1 → A2 | C2 | I1 | the pool's config waited on the workspace plan's amendment directly; the contract now carries that wait and the pool's types |
| PL3 → PL1 | C2 | I1 | the picker needs the rule names, not the config's checks |
| PL4 → PL1 | C2 | I1 | the lookup needs the owner's principal type, not the config's checks |
| CS1 → PL4 | C2 | I1 | the walk needs the pool lookup's interface, not the personal pool |
| CS1 → PL3 | C2 | I1 | the walk needs the picker's signature, not the rules |
| MG1 → PL1 | C2 | I1 | the migration needs the pool's types, not the config's checks |
| VS2 → PL2 | C2 | I1 | the console needs the JSON shape, not the command |
| PR2 → PR9 | C3 | I2 | the runner needs the two settings' names in the answer, not the policy |
| PR1 → PR2 | C3 | I2 | the closed sets need the word a suspended run writes: the constant, not the suspend |
| PR3 → PR2 | C3 | I2 | the service needs the node's shape, not the run that pauses |
| PR4 → PR2 | C3 | I2 | the orchestrator needs the paused record's shape, not the run that writes it |
| PR5 → PR4 | C3 | I2 | the resumed run needs the `resumes` member, not the launch |
| PR6 → PR2 | C3 | I2 | the update needs the node's name, not its writer |
| PR7 → PR2 | C3 | I2 | the views need the node's shape, not its writer |
| PR8 → PR4 | C3 | I2 | the notice needs the resume's shape, not the resumer |
| VS3 → PR7 | C3 | I2 | the console needs the JSON members, not the command |
| VS4 → PR2 | C3 | I2 | the mobile feed needs the node's shape, not its writer |

**Kept** (the dependent needs the other task's behaviour, or the two write one file).

| Edge | Why it is kept |
| --- | --- |
| C1 → A1 | the earlier plan must say its own C1 (#3460) is withdrawn before a side is written against this one |
| C2 → A2 | the workspace plan must carry the amended decisions before a side is written against a contract that rests on them |
| RT3 → RT1 | Behaviour: it calls the resolver's answer, and a stub's answer would pass a wrong table |
| RT3 → RT2 | it reads the block RT2 adds |
| RT4 → RT2 | Behaviour: it publishes what the block holds |
| RT4 → RT3 | Behaviour: the publisher must refuse what the checks refuse |
| RT5 → RT3 | Behaviour: `set` refuses by the checks' answer |
| RT5 → RT2 | it writes the block RT2 adds |
| RT6 → RT3 | Behaviour: it prints the checks' own answer |
| RT7 → RT6 | it calls the helpers RT6 adds to `validate.go` |
| PL1 → RT2 | one file, `localcfg.go` |
| PL2 → PL1 | it writes the fields PL1 adds |
| PL4 → CS9 | one file, `rules.json.tmpl` |
| PL5 → PL2 | one file, `accounts_pool.go` |
| PL5 → PL4 | Behaviour: it writes through the store PL4 builds, whose rules decide who may write |
| PL6 → PL4 | one file, `launcherpool.go`, and it extends that lookup |
| PL8 → PL3 | one file, `poolrule.go`, and it extends that picker |
| CS4 → CS2 | one file, `service.go` |
| CS6 → CS5 | one file, `runner/gateway.go` |
| CS7 → CS5 | one file, `poolchoice.go` |
| CS10 → CS11 | Behaviour: it decides whether a job is told about the service from what CS11 plans |
| MG2 → MG1 | Behaviour: it writes what the function answers, and a stub's answer would be written to a real installation |
| MG2 → RT4 | Behaviour: it publishes through RT4's publisher |
| MG2 → PL1 | it writes the pool fields PL1 adds |
| MG4 → RT5 | Behaviour: its refusals name a command that must exist, and the plugin's tests check the name |
| MG5 → RT4 | one file, `registry.go` |
| MG5 → PL1 | the registry's coverage test reads the struct PL1 changes |
| MG6 → CS10 | one file, `run.go` |
| MG6 → RT6 | one file, `models_use.go` |
| PR1 → CS8 | two files, `runstore.go` and `result.schema.json` |
| PR1 → CS9 | two files, `registry.go` and `rules.json.tmpl` |
| PR1 → PL4 | one file, `rules.json.tmpl` |
| PR2 → CS6 | one file, `runner.go` |
| PR3 → CS1 | one file, `cascade.go` |
| PR3 → PL6 | Behaviour: a resumed run of a service account's launch resolves through PL6's lookup |
| PR6 → MG3 | one line each in `update_in_flight.go` |
| PR7 → CS9 | two files, `render.go` and `json.go` |
| PR9 → RT4 | two files, `policy.go` and `init_policy_publish.go` |
| PR9 → MG5 | two files, `registry.go` and `settings.schema.json` |
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
| X1 → I1 | Behaviour: it deletes what I1 proves unused |
| X1 → DOC1 | one file, `SECURITY.md`'s test |
| X2 → X1 | Behaviour: its gate must exist before a file may lose the key |
| X2 → MG5 | two schema files |
| X3 → X2 | the schema test both change |
| X4 → X1 | Behaviour: no image old enough to need the record may still launch |
| X4 → PR6 | one file, `update_in_flight.go` |
| X5 → I1 | Behaviour: nothing may still read the keys it removes |

**The gain**, as the read reports it: `33 broken, 50 kept, 8 added; before 15 tasks, weight 33;
after 12 tasks, weight 25; improves`.

**Contract changes anticipated.** A task that finds a contract wrong raises the change and never
works around it.

| Change | Contract | Most likely raised by | When | Dependents it reaches |
| --- | --- | --- | --- | --- |
| R-chain-answer | C1 | CS4 | The Vertex leg needs more in the service's answer than `next: vertex` | CS1, CS2 |
| R-designation | C2 | PL4 | The workspace's grant route cannot carry a designation | PL5, PL6 |
| R-pause-record | C3 | PR5 | Restoring a session needs a member the record lacks | PR2, PR4 |

## The estimate the graph read printed

`ready in 756–3117 min, no history yet for 51 tasks (66 tasks; tiers for 15, seeds for 51; from the cached history of 2026-11-21 18:33 UTC)`

This line is informational and reserves no budget. It is what the tasks' tiers and this
machine's cached history predict, counts the three contract changes that run only if raised, and
moves as runs finish and as the graph changes.

**Runs proposed, none launched:** 63 task runs (3 documents, 3 contracts, 2 integrations, 55
others), up to 3 contract-change runs if raised, and an independent review
(`fugaro review --pr N`) for each of the 44 tasks marked opus. At the repository's per-run cap of
10 USD the 63 task runs cost at most 630 USD and the reviews at most 440 USD; the gateway
enforces the cap, and no brief states one.

## Checks the plan takes

From the reserved block 870–879. Every one is owner-run, in the owner's own session, through a
script that prints no value.

| Check | What it measures | Script from | When |
| --- | --- | --- | --- |
| 870 | Whether an answer on a subscription token carries a utilisation figure (U1) | PL7 | Any time; gates PL8 |
| 871 | A live chain falls through from an exhausted pool to the key, flagged and counted | CK1 | After release 1 is installed by `fugaro update` |
| 872 | The Vertex leg serves each mapped model, under the cap (U5) | CK1 | After release 1 |
| 873 | A run pauses and the orchestrator resumes it | CK1 | After release 1 |
| 874 | `fugaro update` migrates the dev installation and no repository's source moves | CK1 | At release 1's install |
| 875 | A retired key is rejected with the fix | CK1 | After release 2 |

876–879 are reserved and unused.

## After-deploy steps

Each release's `### For operators` is "run `fugaro update`" and what it prints (DOC3 for release
1; X3's pull request for release 2). No other command, deletion or manual repair ships: the
migration (MG2), the rules and policy changes (RT4, PR1, PR9, X3), the old record (MG3, X4) and
the published layer (X5) are all `update`'s own work. What a person does by choice afterwards,
and no release depends on: designate a personal pool and move their own token into it (PL5).

## Considered and declined

- **Layering the table on top of the finished earlier plan** (D3170's alternative): two
  migrations of the same config and about 30 runs building a shape that is then removed.
- **A craft key in a table entry** (D3171's alternative): a second key and a precedence rule.
- **Always suspending** (the recommendation of D3184): declined by the owner; the floor stays.
- **The same run id for the resumed job** (D3185's alternative): it breaks one terminal outcome
  per run and the write-once budget entry.
- **A resumer on every installation** (D3186's alternative): a new identity with launch rights
  everywhere.
- **Folding the resolver into the contract**: a contract that computes is a side, not a contract.
- **One task for the whole migration**: the pure function (MG1) is reviewable alone and is what
  `TestMigrateMovesNoRepository` pins; the wiring into `update` (MG2) is a different risk.
