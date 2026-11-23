# Repo-override rulings (lift L8): implementation plan

What this builds: [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md), which
wins where the two disagree. It applies D3237: B3, B9, B11 and B16 move to the project, migrated
once by `fugaro update` (release A) and refused in a repository's file the release after (release
B); B4 becomes tighten-only; B18's repository cap gets a ceiling; and, on D3299's recommendation,
L13's dead keys are refused.

## Status

**Plan only. Nothing is implemented. No task is approved**: each needs the owner's explicit go,
and none is launched from this document.

The answers that shaped it: D3290 (B3's majority rule), D3291 (`agent.levels.lN.model` is a B3
key), D3292 (`agent.recipe` may name any project or catalog recipe), D3293 (`workspace_beat`
keeps the replace rule), D3294 (a member area naming the repository ranks above `rigor.repos`),
D3295 (**one flat recipe namespace, not the recommendation**, with the owner's rename-on-clash
rule), D3296 (the cleanup diff in `doctor`, `validate` and the launch), D3297 (`ab.repos` in
`[0, ab.rate]`), D3298 (release B is the release after A, behind the image gate), and, answered
the same day as recommended, the six this reconciliation raised:

| Decision | What it settles in this plan |
| --- | --- |
| D3299 (capability map, L13 inside) | The nine modules; DK1 and DK2 are in the plan |
| D3300 (release A one release after L1) | The release gate below |
| D3301 (a clash is any name another repository already resolves to different text) | PR4's clash rule |
| D3302 (`<name>.<owner-repo>`, one dot, at most 81 characters) | PR0's grammar and S5's `recipeCopySince` gate |
| D3303 (the pointer is a lookup rule, no new data) | PR0's lookup order, PR1 and PR2 |
| D3304 (a pre-L8 task naming a repository recipe is refused at bootstrap) | PR3 |

**External edges, not tasks.** L1, the required-version stamp (`docs/required-version-stamp/`,
D3260–D3289, being planned beside this one): release A is cut only after the release that carries
L1 has shipped (D3300). Model-credentials-routing: its open MG5, MG6 and X2 write some of
the same files (reconciliation O2); a task that finds one merged first rebases on it, and none
waits. L5 (not yet specced): it needs M1's reader (O1).

## Conventions every part follows

- `CLAUDE.md` and the rules files the touched areas load: `.claude/rules/ee.md` for AB2
  (the two-line notice on every new file under `ee/`; nothing outside `ee/` imports it; run
  `go test ./internal/eeboundary/ -count=1`). No other rules file is triggered.
- **The HARD RULE on `fugaro update`**: the migration is inside `update`, asks nothing, prints
  before any write one line per repository whose runs change, is safe with runs in flight, and a
  second run changes nothing (I2 pins all four). Release A's `### For operators` is "run
  `fugaro update`", what it will print, and "every member upgrades the CLI" (R9).
- **Nothing asks** (`docs/no-prompt-policy/`): every refusal is one sentence with the fix, and a
  change to a repository's file is offered as a diff, never made.
- **A field never changes meaning**; config and layer decode strictly; `ab.repos` is the one new
  layer member (R9), and the copy-name grammar is additive (D3302).
- **Docs are tested**: a task that touches a document runs
  `go test ./internal/cli/ -run Docs -count=1`; a task that changes help text regenerates
  `website/content/cli-reference.md`.
- **Hermetic tests**: `internal/gcpfake`, `internal/testutil`, `t.TempDir()`, the RTDB emulator
  harness for RC2. No secret-shaped literal anywhere. No agent touches a live project: Checks
  910–912 are scripts the owner runs.
- **Comments say why, never what.** Each task's pull request names the `T#` and `D#` it implements
  and does not touch `docs/status.md`.
- **Boundaries.** Always: the red test first, targeted test commands, `gofmt -l .` empty,
  `go vet ./...`. Ask first: a new dependency, a schema version bump, a change to a contract's files
  outside its R-task, a CI change (staged under `docs/pending-workflows/`). Never: edit `spec.md`,
  delete a failing test, run a live check, pass `--total-timeout`.
- **Reviewer tier.** Opus with a mandatory independent review (`fugaro review --pr N`) for every
  task that touches a write guard, the budget rules, a writer's refusal, the skills, or the
  contracts C1 and C2 (the strictest consumer of each is a refusal); standard elsewhere.

## Global constraints and review focus

| Constraint | Pinned by |
| --- | --- |
| A lifted key never reaches a resolved config from any reader, on any ref | I1 |
| Model-credentials-routing's retired rows stay honoured (F1) | C1's test, I1 |
| A migration that cannot keep a run where it was names that repository before any write | MC1, RG2, PR4, RC4, I2 |
| `update` twice changes nothing; a run in flight keeps its task, layer and recipe | I2 |
| Between `brew upgrade` and `update`, no launch runs on a pre-L8 image with a lifted key or a recipes folder | S5 |
| A repository recipe reaches a run only from the store after release A | PR3, I3 |
| Release B refuses only on the default branch; every other ref ignores and names | XB1, XB2 |

## Modules and slices

The modules are the capability map of reconciliation §2 (D3299, approved): `lift-keys`, `migrate`,
`model-choice`, `review-tighten`, `ab-repos`, `rigor-repos`, `project-recipes`, `repo-caps`,
`dead-keys`. The slices:

1. **Contracts and the inert first slice** (level 0): A1, C1, C2, PR0, S0, PR5, AB1, RG1, PR3,
   RC1, RC2, M1, CK1. Nothing here changes what a run does except PR3, RG1, RC1 and RC2, which are
   held by the release gate below.
2. **Release A, items and planners**: S1, S4, S5, AB2, AB3, MC1, RG2, PR1, PR2, PR4, RC3, RC4,
   M2, CK2, RT1, RG3, RT2, S2, DK1, D1, D2, DK2.
3. **Release A, integrations**: I2, I3, I1.
4. **Release B** (gated on release A being cut, D3298): XB4, XB1, XB2, XB3, XB5.

**The release gate.** Contracts, planners, documents and check scripts may merge at any time: no
caller reaches them before I2 registers the planners. Every task that changes what a command or a
run does — S1, RT1, RT2, S2, S4, S5, AB2, RG1, RG3, PR1, PR2, PR3, RC1, RC2, RC3, M2, I2, DK1,
DK2 — is **not launched before L1's implementation has merged**, and release A is not cut before
the release carrying L1 has shipped (D3300).

## Tasks

### A1: model-credentials-routing's F7 amended for D3237
- Module: `model-choice`.
- Acceptance: `docs/model-credentials-routing/reconciliation.md` F7 carries a dated amendment
  (2026-11-23) saying D3237 reverses "the repository keeps its model choice": the project layer
  chooses each role's model, the routing table its source; the README's index names the amendment;
  the original text stays visible.
- Files: `docs/model-credentials-routing/reconciliation.md`, `docs/model-credentials-routing/README.md`.
- Interfaces: none.
- Tests: the Docs tests.
- Command: `go test ./internal/cli/ -run Docs -count=1`
- Red step: none (documents); the Docs tests must stay green.
- Depends on: none.
- Commit: `repo-override-rulings A1: model-credentials-routing's F7 amended for D3237`
- Reviewer tier: standard.

### C1: contract: the lifted keys and their notes
- Module: `lift-keys`. Kind: contract.
- Acceptance: `internal/config/lifted.go` holds `LiftedKeys` (every B3 key including
  `agent.levels.l1–l3.model` (D3291), `ab.rate`, `rigor.default`, `rigor.areas`, and the L13 keys
  `model_grades.*` and `agent.compression` marked refuse-now (D3299)), each with
  its new home and its fix sentence, and `LiftedKeyNotes(data []byte) []RetiredKeyNote`, which names
  every lifted key a repository's file writes; no model-credentials-routing retired key is on the
  list (R1). `contract-lift-keys.md` says what each side may rely on: the list is the only
  answer to "is this key lifted", the sentence is the one refusal text release B prints, and
  `config.Resolve` is the only place that drops.
- Files: `internal/config/lifted.go` (new), `internal/config/lifted_test.go` (new),
  `docs/repo-override-rulings/contract-lift-keys.md` (new).
- Interfaces: Produces `config.LiftedKeys`, `config.LiftedKeyNotes`, which S1, S2, S4, S5 consume.
- Tests: `TestLiftedKeysAreNotMCRsRetiredKeys`, `TestLiftedKeyNotesNamesEveryWrittenKey`.
- Command: `go test ./internal/config/ -run 'TestLiftedKey' -count=1`
- Red step: `TestLiftedKeyNotesNamesEveryWrittenKey` fails before the function exists.
- Depends on: none. Breaks S1, S2, S4 and S5's wait on S0.
- Commit: `repo-override-rulings C1: contract: the lifted keys and their notes`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`), before S1,
  S2, S4 or S5 is launched.

### C2: contract: the migration's repository files and planners
- Module: `migrate`. Kind: contract.
- Acceptance: `internal/lift/lift.go` defines `Repo` (the owner/name, its provider, its default
  branch, its `fugaro.yaml` bytes, its `.fugaro/recipes/` files, and why it could not be read),
  `Project` (the local layer file's tree, the store's recipe names and sums, the caps nodes, the
  price table), `Change` (layer edits by path, store writes by key, database writes by path, and
  the lines to print before any of them), `Planner func(Project, []Repo) (Change, error)` and
  `Source func(ctx) ([]Repo, error)`; `internal/lift/stubs.go` registers five stub planners that
  return no change; `contract-migrate.md` says a planner is pure, never writes, and names every
  repository whose runs change.
- Files: `internal/lift/lift.go` (new), `internal/lift/stubs.go` (new),
  `docs/repo-override-rulings/contract-migrate.md` (new).
- Interfaces: Produces the types MC1, AB3, RG2, PR4, RC4 and M2 consume. Stubs: `internal/lift/stubs.go`.
- Tests: `TestStubPlannersChangeNothing`.
- Command: `go test ./internal/lift/ -count=1`
- Red step: the test fails before the package exists.
- Depends on: none. Breaks every planner's wait on M1 and M2's wait on every planner.
- Commit: `repo-override-rulings C2: contract: the migration's repository files and planners`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PR0: a recipe's project copy, its name and its lookup order
- Module: `project-recipes`.
- Acceptance: `recipe.CopyName(name, repo string) string` returns `<name>.<owner-repo>` (A4);
  `recipe.IsCopyName` and the widened grammar accept it and nothing else with a dot (D3302);
  `recipe.LookupOrder(name, repo string) []string` is `[<name>.<owner-repo>, <name>]` (D3303), the
  catalog last; a name over the limit and a slug with an unsafe character are refused.
- Files: `internal/recipe/copyname.go` (new), `internal/recipe/copyname_test.go` (new).
- Interfaces: Produces the functions PR1, PR2, PR4 and S5 call.
- Tests: `TestCopyName`, `TestLookupOrder`, `TestNameREStillRefusesADot`.
- Command: `go test ./internal/recipe/ -run 'TestCopyName|TestLookupOrder|TestNameREStillRefusesADot' -count=1`
- Red step: `TestCopyName` fails before the file exists.
- Depends on: none. Implements D3302 and D3303. Kept as a task, not a contract: the
  functions are the interface, and a contract would equal them.
- Commit: `repo-override-rulings PR0: a recipe's project copy, its name and its lookup order`
- Reviewer tier: standard.

### S0: the scope table
- Module: `lift-keys`.
- Acceptance: every lifted key's row in `config.Scopes` is `Retired: InRepo` with a `RetiredWhy`
  naming its new home and command (R1); every kept row's `Why` cites D3237; `docs/project-layer.md`
  prints the table (`TestScopeTableMatchesDocs`); `schemas/fugaro.schema.json` describes each
  lifted key as retired.
- Files: `internal/config/scope.go`, `docs/project-layer.md`, `schemas/fugaro.schema.json`,
  `internal/config/scope_lift_test.go` (new).
- Interfaces: Produces the rows `RetiredKeyNotes` reads.
- Tests: `TestScopeTableMatchesDocs`, `TestKeptRowsCiteD3237`, the Docs tests.
- Command: `go test ./internal/config/ -run 'TestScopeTableMatchesDocs|TestKeptRowsCiteD3237' -count=1 && go test ./internal/cli/ -run Docs -count=1`
- Red step: `TestKeptRowsCiteD3237` fails on today's rows.
- Depends on: none.
- Commit: `repo-override-rulings S0: the scope table`
- Reviewer tier: standard.

### S1: config.Resolve drops the lifted keys, on every ref
- Module: `lift-keys`.
- Acceptance: both arms of `config.Resolve` (with and without a layer) drop every key
  `config.LiftedKeys` lists before the merge, record each in the resolution with its note, and leave
  MCR's retired keys as they are; a file that writes only lifted keys resolves to the project's (or
  Fugaro's) values (R2).
- Files: `internal/config/resolve.go`, `internal/config/lifted_drop.go` (new),
  `internal/config/lifted_drop_test.go` (new).
- Interfaces: Consumes C1. Produces the dropped keys in `Resolution`, which S2 and I1 read.
- Tests: `TestResolveDropsLiftedKeys` (one case per key, with and without a layer),
  `TestResolveKeepsMCRRetiredKeys`.
- Command: `go test ./internal/config/ -run 'TestResolveDropsLiftedKeys|TestResolveKeepsMCRRetiredKeys' -count=1`
- Red step: `TestResolveDropsLiftedKeys/agent.models.coder` fails: the repository's value wins.
- Depends on: C1 (the contract's list). Release gate.
- Commit: `repo-override-rulings S1: config.Resolve drops the lifted keys, on every ref`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### RT1: the tighten-only merge against the project's value
- Module: `review-tighten`.
- Acceptance: for each key of spec §5.2's table, a repository value looser than the project's is
  dropped, its source stays the project's and the resolution records it in the `policy.Ignored`
  shape; a tighter one wins; `agent.levels.lN.none` may turn a level on, never off; effort follows
  `config.Efforts`; focus quotes both texts; `agent.recipe` accepts any project or catalog recipe
  (D3292); `workspace_beat` keeps the replace rule (D3293); where the project is silent the
  repository's value is kept and marked unchecked (R3); a project that raises its value never makes
  a repository's file invalid.
- Files: `internal/config/resolve.go`, `internal/config/tighten.go` (new),
  `internal/config/tighten_test.go` (new).
- Interfaces: Produces the ignored values S2 prints and the unchecked mark RT2 reads.
- Tests: `TestTightenOnly` (one sub-test per key, looser dropped and tighter kept),
  `TestProjectRaiseKeepsRepoFileValid`.
- Command: `go test ./internal/config/ -run 'TestTightenOnly|TestProjectRaiseKeepsRepoFileValid' -count=1`
- Red step: `TestTightenOnly/review_rounds` fails: a lower repository value replaces the project's.
- Depends on: S1 (kept: one file, `resolve.go`). Release gate.
- Commit: `repo-override-rulings RT1: the tighten-only merge against the project's value`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### RT2: a silent project's baseline is the run's mode
- Module: `review-tighten`.
- Acceptance: where RT1 marked a value unchecked, the runner's `setRounds` and the launch's budget
  and preview take the tighter of it and the mode's default (`rigor.DefaultRounds`,
  `rigor.AdversarialDefault`) at the mode the run is at, and the record names which decided (R3).
- Files: `internal/runner/plan.go`, `internal/cli/launch_budget.go`, `internal/cli/budget_preview.go`,
  `internal/runner/plan_tighten_test.go` (new).
- Interfaces: Consumes RT1's mark.
- Tests: `TestUncheckedRoundsTakeTheMode`, `TestLaunchBudgetUsesTheTighter`.
- Command: `go test ./internal/runner/ -run 'TestUncheckedRoundsTakeTheMode' -count=1 && go test ./internal/cli/ -run 'TestLaunchBudgetUsesTheTighter' -count=1`
- Red step: `TestUncheckedRoundsTakeTheMode` fails: a critical run takes the repository's 1.
- Depends on: RT1 (kept, behaviour). Release gate.
- Commit: `repo-override-rulings RT2: a silent project's baseline is the run's mode`
- Reviewer tier: standard.

### S2: validate and doctor name every lifted key, every dropped value and the recipes cleanup
- Module: `lift-keys`.
- Acceptance: `fugaro validate` and `fugaro doctor` print, once each, every lifted key the file
  writes with its fix (`LiftedKeyNotes`), every value RT1 dropped, and PR5's cleanup diff for a
  checkout holding `.fugaro/recipes/` (D3296); `--json` carries them as notes; nothing asks.
- Files: `internal/cli/validate.go`, `internal/cli/doctor.go`, `internal/cli/lifted_notes_test.go` (new).
- Interfaces: Consumes C1, RT1's record and PR5's lines.
- Tests: `TestValidateNamesLiftedKeys`, `TestDoctorNamesLiftedKeys`, `TestValidatePrintsDroppedValuesOnce`.
- Command: `go test ./internal/cli/ -run 'TestValidateNamesLiftedKeys|TestDoctorNamesLiftedKeys|TestValidatePrintsDroppedValuesOnce' -count=1`
- Red step: `TestValidateNamesLiftedKeys` fails: nothing prints them (F2).
- Depends on: C1 (the contract's notes); RT1 (kept, behaviour); PR5 (kept: doctor prints its lines).
  Release gate.
- Commit: `repo-override-rulings S2: validate and doctor name every lifted key`
- Reviewer tier: standard.

### S4: settings set and models use refuse a lifted key in a repository
- Module: `lift-keys`, `model-choice`.
- Acceptance: `fugaro settings set <lifted key> --layer repo` and `fugaro models use … --layer repo`
  are refused with the project-layer command (R15); `models use` defaults to `--layer project`; the
  help texts and the generated CLI reference say so; nothing asks.
- Files: `internal/cli/settings_set.go`, `internal/cli/models_use.go`,
  `internal/cli/lifted_writers_test.go` (new), `website/content/cli-reference.md`.
- Interfaces: Consumes C1.
- Tests: `TestWritersRefuseLiftedKeys` (one sub-test per writer), `TestModelsUseDefaultsToProject`.
- Command: `go test ./internal/cli/ -run 'TestWritersRefuseLiftedKeys|TestModelsUseDefaultsToProject|Docs' -count=1`
- Red step: `TestModelsUseDefaultsToProject` fails: the default is `repo`.
- Depends on: C1. Release gate.
- Commit: `repo-override-rulings S4: settings set and models use refuse a lifted key in a repository`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### S5: the release-A launch: its notes, and its gate on a pre-L8 image
- Module: `lift-keys`.
- Acceptance: a launch prints each lifted key its default branch writes, once; it refuses a job
  image before `l8Since` where the default branch writes a lifted key, holds `.fugaro/recipes/`
  (R16), or the layer writes `ab.repos` (`abReposSince`), and where the task embeds a copy name
  (`recipeCopySince`, D3302), each with `fugaro update` as the fix.
- Files: `internal/cli/recipes_skew.go`, `internal/cli/l8_skew_test.go` (new).
- Interfaces: Consumes C1, AB1's `Layer.SetsABRepos`, PR0's `IsCopyName`. Produces the gate XB4 extends.
- Tests: `TestL8GateRefusesAnOlderImage` (one case per trigger), `TestLaunchNamesLiftedKeys`.
- Command: `go test ./internal/cli/ -run 'TestL8GateRefusesAnOlderImage|TestLaunchNamesLiftedKeys' -count=1`
- Red step: `TestL8GateRefusesAnOlderImage/recipes-folder` fails: the launch goes through.
- Depends on: C1; AB1 (kept: the field); PR0 (kept: the function). Release gate.
- Commit: `repo-override-rulings S5: the release-A launch gate`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### AB1: ab.repos in the layer's ab block
- Module: `ab-repos`.
- Acceptance: the layer's `ab:` block accepts `repos: {<owner/name>: <rate>}`, each in
  `[0, ab.rate]` (D3297), keyed as `rigor.repos` is; `Layer.SetsABRepos` says whether it is written;
  `schemas/project-layer.schema.json` describes it; a value above `ab.rate` is refused with the fix.
- Files: `internal/config/ab.go`, `internal/config/layer.go`, `internal/config/ab_repos_test.go` (new),
  `schemas/project-layer.schema.json`.
- Interfaces: Produces `config.AB.Repos`, `Layer.SetsABRepos`.
- Tests: `TestABReposBounds`, `TestABReposSchema`.
- Command: `go test ./internal/config/ -run 'TestABRepos' -count=1`
- Red step: `TestABReposBounds` fails: the strict decode refuses `repos`.
- Depends on: none.
- Commit: `repo-override-rulings AB1: ab.repos in the layer's ab block`
- Reviewer tier: standard.

### AB2: ee/ab reads a repository's rate from ab.repos
- Module: `ab-repos`.
- Acceptance: `abRate` (and its copies in `ee/ab/cli` and `ee/report`) feeds the repository's rate
  from `ab.repos.<repo>` through `policy.Merge`, unchanged; a repository's own `ab.rate` no longer
  reaches it (dropped by S1); the new file carries the `ee/` notice.
- Files: `ee/ab/launch.go`, `ee/ab/cli/ab.go`, `ee/report/ab.go`, `ee/ab/repos_rate_test.go` (new).
- Interfaces: Consumes AB1.
- Tests: `TestABRateFromRepos`, `go test ./internal/eeboundary/`.
- Command: `go test ./ee/ab/ -run 'TestABRateFromRepos' -count=1 && go test ./internal/eeboundary/ -count=1`
- Red step: `TestABRateFromRepos` fails: an opted-out repository is sampled.
- Depends on: AB1 (kept: the field). Release gate.
- Commit: `repo-override-rulings AB2: ee/ab reads a repository's rate from ab.repos`
- Reviewer tier: standard.

### AB3: the planner that moves ab.rate to ab.repos
- Module: `ab-repos`.
- Acceptance: for every repository whose default branch writes `ab.rate: 0`, the change writes
  `ab.repos.<repo>: 0`; nothing else; one line per repository; no change when already written.
- Files: `internal/lift/ab.go` (new), `internal/lift/ab_test.go` (new).
- Interfaces: Consumes C2 and AB1. Produces `lift.PlanAB`, which I2 registers.
- Tests: `TestPlanAB`.
- Command: `go test ./internal/lift/ -run 'TestPlanAB' -count=1`
- Red step: `TestPlanAB` fails before the file exists.
- Depends on: C2; AB1 (kept: the field).
- Commit: `repo-override-rulings AB3: the planner that moves ab.rate to ab.repos`
- Reviewer tier: standard.

### MC1: the planner that moves the model choice to the project
- Module: `model-choice`.
- Acceptance: for each B3 key and `agent.levels.lN.model` (D3291): the project's value stays where
  it sets one, and each repository that wrote another is named ("acme/app: coder claude-x ->
  claude-y (the project's)"); a silent project takes the value every writer agrees on, and every
  non-writing repository that moves off Fugaro's default is named; writers that disagree give the
  majority, ties to the cheaper model in the price table (D3290), and each repository whose model
  changes is named; `budget.allowed_models` is not touched.
- Files: `internal/lift/models.go` (new), `internal/lift/models_test.go` (new).
- Interfaces: Consumes C2. Produces `lift.PlanModels`.
- Tests: `TestPlanModels` (project sets, all agree, majority, tie by price, levels models).
- Command: `go test ./internal/lift/ -run 'TestPlanModels' -count=1`
- Red step: `TestPlanModels/majority` fails before the file exists.
- Depends on: C2.
- Commit: `repo-override-rulings MC1: the planner that moves the model choice to the project`
- Reviewer tier: standard.

### RG1: the new rigor precedence and Configured per repository
- Module: `rigor-repos`.
- Acceptance: in `rigor.Resolve` a project area with a member naming the run's repository ranks
  above `rigor.repos` (D3294), a `match`-only project area stays below it; `Configured` is per
  repository: the project's default or project-wide areas, else this repository's `rigor.repos`
  entry or a member area naming it; the repository's own block still counts until release B.
- Files: `internal/rigor/resolve.go`, `internal/rigor/select.go`, `internal/rigor/resolve_lift_test.go` (new).
- Interfaces: none new.
- Tests: `TestMemberAreaOutranksRigorRepos`, `TestConfiguredPerRepository`.
- Command: `go test ./internal/rigor/ -run 'TestMemberAreaOutranksRigorRepos|TestConfiguredPerRepository' -count=1`
- Red step: `TestMemberAreaOutranksRigorRepos` fails on today's order (LD4).
- Depends on: none. Release gate.
- Commit: `repo-override-rulings RG1: the new rigor precedence and Configured per repository`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### RG2: the planner that moves rigor.default and rigor.areas to the project
- Module: `rigor-repos`.
- Acceptance: a repository's `rigor.default` becomes `defaults.rigor.repos.<repo>` (the file's
  value wins over an existing entry, as it does today, and is named); each `rigor.areas` rule
  becomes a project area `{repo, paths: [its match]}` keeping its name unless any project area
  already has it (then `-<owner-repo>`, cut to 40, a short hash on a second collision, R4); an
  empty `rigor:` block becomes `rigor.repos.<repo>` at the level it resolves to today; a path whose
  level would rise because a moved area meets an existing member area is named (R5).
- Files: `internal/lift/rigor.go` (new), `internal/lift/rigor_test.go` (new).
- Interfaces: Consumes C2. Produces `lift.PlanRigor`.
- Tests: `TestPlanRigor` (default, areas, name taken, name too long, empty block, overlap named).
- Command: `go test ./internal/lift/ -run 'TestPlanRigor' -count=1`
- Red step: `TestPlanRigor/default` fails before the file exists.
- Depends on: C2.
- Commit: `repo-override-rulings RG2: the planner that moves rigor to the project`
- Reviewer tier: standard.

### RG3: rigor set writes rigor.repos and member areas; --layer repo refused
- Module: `rigor-repos`.
- Acceptance: `fugaro rigor set <mode> --repo <owner/name>` with no `--layer` writes
  `defaults.rigor.repos.<owner/name>`; `--area <glob> --name <n> --repo <owner/name>` writes a
  member area; `--layer repo` is refused for both keys with that command (R6, R15); the
  sensitive-direction fence keeps working; `writers_labels.go` names the writer; the CLI reference
  is regenerated.
- Files: `internal/cli/rigor.go`, `internal/cli/writers_labels.go`, `internal/cli/rigor_repos_test.go` (new),
  `website/content/cli-reference.md`.
- Interfaces: none new.
- Tests: `TestRigorSetRepoWritesRigorRepos`, `TestRigorSetLayerRepoRefused`, the Docs tests.
- Command: `go test ./internal/cli/ -run 'TestRigorSetRepoWritesRigorRepos|TestRigorSetLayerRepoRefused|Docs' -count=1`
- Red step: `TestRigorSetRepoWritesRigorRepos` fails: nothing writes the map (F13).
- Depends on: S4 (kept: one file, the generated CLI reference). Release gate.
- Commit: `repo-override-rulings RG3: rigor set writes rigor.repos and member areas`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PR1: the launcher resolves a repository's own copy, then the flat name, then the catalog
- Module: `project-recipes`.
- Acceptance: `resolveRecipe` never reads the checkout's `.fugaro/recipes/`; it reads the store in
  `recipe.LookupOrder` and then the catalog; `fugaro recipes ls` lists the store and says the
  folder is not read; `refuseFollowUpRepoRecipe` judges the project copy.
- Files: `internal/cli/recipes_resolve.go`, `internal/cli/recipes.go`, `internal/cli/recipes_lift_test.go` (new).
- Interfaces: Consumes PR0.
- Tests: `TestResolveRecipeOwnCopyFirst`, `TestResolveRecipeIgnoresTheFolder`, `TestRecipesLsSaysTheFolderIsNotRead`.
- Command: `go test ./internal/cli/ -run 'TestResolveRecipeOwnCopyFirst|TestResolveRecipeIgnoresTheFolder|TestRecipesLsSaysTheFolderIsNotRead' -count=1`
- Red step: `TestResolveRecipeIgnoresTheFolder` fails: the checkout's file wins.
- Depends on: PR0 (kept). Implements D3303. Release gate.
- Commit: `repo-override-rulings PR1: the launcher resolves a repository's own copy first`
- Reviewer tier: standard.

### PR5: the cleanup diff, and validate prints it
- Module: `project-recipes`.
- Acceptance: `recipesCleanupLines(root)` returns, for a checkout holding `.fugaro/recipes/`, the
  diff `git rm -r .fugaro/recipes` and one line per file naming the copy it became; `validate`
  prints them; nothing is deleted.
- Files: `internal/cli/recipes_cleanup.go` (new), `internal/cli/validate_recipes.go`,
  `internal/cli/recipes_cleanup_test.go` (new).
- Interfaces: Produces `recipesCleanupLines`, which S2 and PR2 call.
- Tests: `TestRecipesCleanupLines`.
- Command: `go test ./internal/cli/ -run 'TestRecipesCleanupLines' -count=1`
- Red step: the test fails before the file exists.
- Depends on: none.
- Commit: `repo-override-rulings PR5: the cleanup diff`
- Reviewer tier: standard.

### PR2: every launch path embeds the project copy
- Module: `project-recipes`.
- Acceptance: a first run from outside a checkout reads `agent.recipe` from the ref's file and
  embeds the copy; a follow-up that inherits a repository recipe by name embeds its copy; the
  `default` fallback embeds a repository's own `default` copy when one exists; no task this
  launcher writes carries `Source: repo` (R7); the launch prints PR5's cleanup lines.
- Files: `internal/cli/run.go`, `internal/cli/followup.go`, `internal/cli/recipes_launch_lift_test.go` (new).
- Interfaces: Consumes PR0, PR5.
- Tests: `TestLaunchEmbedsTheCopy` (outside a checkout, follow-up, default fallback), `TestNoTaskCarriesARepoRecipe`.
- Command: `go test ./internal/cli/ -run 'TestLaunchEmbedsTheCopy|TestNoTaskCarriesARepoRecipe' -count=1`
- Red step: `TestNoTaskCarriesARepoRecipe/follow-up` fails: the name travels alone.
- Depends on: PR0 (kept); PR5 (kept). Implements D3303. Release gate.
- Commit: `repo-override-rulings PR2: every launch path embeds the project copy`
- Reviewer tier: standard.

### PR3: the runner stops reading .fugaro/recipes/
- Module: `project-recipes`.
- Acceptance: a release-A runner resolves the embedded recipe or a catalog one, never the
  repository's folder; a task naming a repository recipe is refused at bootstrap with the fix (R8);
  a run already bootstrapped is untouched.
- Files: `internal/runner/recipe.go`, `internal/runner/recipe_lift_test.go` (new).
- Interfaces: none new.
- Tests: `TestRunnerNeverReadsTheFolder`, `TestRepoSourcedTaskIsRefused`.
- Command: `go test ./internal/runner/ -run 'TestRunnerNeverReadsTheFolder|TestRepoSourcedTaskIsRefused' -count=1`
- Red step: `TestRunnerNeverReadsTheFolder` fails: `readRepoRecipe` reads it.
- Depends on: none. Implements D3304. Release gate.
- Commit: `repo-override-rulings PR3: the runner stops reading .fugaro/recipes/`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PR4: the planner that publishes repository recipes flat, renaming clashes
- Module: `project-recipes`.
- Acceptance: every regular file of `.fugaro/recipes/*.yaml` on a default branch that parses
  (`recipe.Parse`, 16 KiB) is published flat under its name when nothing else answers that name;
  on a clash (D3295 as D3301 defines it: another repository's file, an existing project recipe or a
  catalog name, with different text) each clashing copy is published as `recipe.CopyName` with its
  `name:` line rewritten and nothing else (R14), one line per rename; identical texts publish once;
  a file that does not parse is named and left; a second run changes nothing.
- Files: `internal/lift/recipes.go` (new), `internal/lift/recipes_test.go` (new).
- Interfaces: Consumes C2, PR0. Produces `lift.PlanRecipes`.
- Tests: `TestPlanRecipes` (unique, two repositories clash, project clash, catalog clash, same text, invalid file, idempotent).
- Command: `go test ./internal/lift/ -run 'TestPlanRecipes' -count=1`
- Red step: `TestPlanRecipes/catalog-clash` fails before the file exists.
- Depends on: C2; PR0 (kept). Implements D3301.
- Commit: `repo-override-rulings PR4: the planner that publishes repository recipes`
- Reviewer tier: standard.

### RC1: Caps clamps a repository cap to its ceiling
- Module: `repo-caps`.
- Acceptance: `budget.RepoCeiling` is, for daily and per-run separately, the global cap when set,
  else the default repository cap (A3); `Caps.RepoDaily` and `RepoPerRun` return the smaller of the
  node and its ceiling; no ceiling leaves the node standing.
- Files: `internal/budget/model.go`, `internal/budget/model_ceiling_test.go` (new).
- Interfaces: Produces `budget.RepoCeiling`.
- Tests: `TestRepoCapClampedToCeiling`.
- Command: `go test ./internal/budget/ -run 'TestRepoCapClampedToCeiling' -count=1`
- Red step: the test fails: a node above the default is returned.
- Depends on: none. Release gate.
- Commit: `repo-override-rulings RC1: Caps clamps a repository cap to its ceiling`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### RC2: the rules' lease check clamps the same way
- Module: `repo-caps`.
- Acceptance: the database rules' lease check reads a repository node above its ceiling as the
  ceiling; the rules marker moves one generation, so `update` publishes it; the golden file and an
  emulator test agree.
- Files: `internal/budget/rules/rules.go`, `internal/budget/rules/rules.json.tmpl`,
  `internal/budget/rules/marker.go`, `internal/budget/rules/testdata/rules.golden.json`,
  `internal/budget/rules/lease_ceiling_emulator_test.go` (new).
- Interfaces: none new.
- Tests: `TestLeaseClampsRepoCap` (emulator), the rules golden test.
- Command: `go test -tags firebase ./internal/budget/rules/ -run 'TestLeaseClampsRepoCap|TestRulesGolden' -count=1`
- Red step: `TestLeaseClampsRepoCap` fails: a lease above the default is granted.
- Depends on: none. Release gate.
- Commit: `repo-override-rulings RC2: the rules' lease check clamps the repository cap`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### RC3: budget set --repo refuses a figure above the ceiling
- Module: `repo-caps`.
- Acceptance: `fugaro budget set --repo` above the ceiling is refused, naming the ceiling and
  `fugaro budget set --global` as the way to raise it; nothing asks.
- Files: `internal/cli/budget.go`, `internal/cli/budget_ceiling_test.go` (new).
- Interfaces: Consumes RC1.
- Tests: `TestBudgetSetRepoRefusedAboveCeiling`.
- Command: `go test ./internal/cli/ -run 'TestBudgetSetRepoRefusedAboveCeiling' -count=1`
- Red step: the test fails: the write lands.
- Depends on: RC1 (kept). Release gate.
- Commit: `repo-override-rulings RC3: budget set --repo refuses a figure above the ceiling`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### RC4: the planner that lowers a node above its ceiling
- Module: `repo-caps`.
- Acceptance: each `config/caps/repos/<slug>` above its ceiling is lowered to it in the change and
  named; with a global cap set the line says no run's limit changes; the in-flight line names the
  lowered caps (spec §7).
- Files: `internal/lift/caps.go` (new), `internal/lift/caps_test.go` (new).
- Interfaces: Consumes C2, RC1. Produces `lift.PlanCaps`.
- Tests: `TestPlanCaps`.
- Command: `go test ./internal/lift/ -run 'TestPlanCaps' -count=1`
- Red step: the test fails before the file exists.
- Depends on: C2; RC1 (kept).
- Commit: `repo-override-rulings RC4: the planner that lowers a repository cap`
- Reviewer tier: standard.

### M1: the reader of every enrolled repository's default branch
- Module: `migrate`.
- Acceptance: `repofiles.Read` returns, for every repository the published policy admits, its
  default branch's `fugaro.yaml` (`gitprov.ContentsReader`) and its `.fugaro/recipes/` files
  (`gitprov.TreeReader`, bounded by `recipe.MaxBytes`), or the one-line reason it could not read
  that repository (R12); it reads no working directory; a test covers each provider's fixture and a
  host with neither interface (U3).
- Files: `internal/repofiles/reader.go` (new), `internal/repofiles/reader_test.go` (new).
- Interfaces: Produces `repofiles.Read`, which I2 adapts to C2's `Source` and L5 can call (O1).
- Tests: `TestReadEveryEnrolledRepo`, `TestReadNamesAnUnreadableRepo`.
- Command: `go test ./internal/repofiles/ -count=1`
- Red step: the test fails before the package exists.
- Depends on: none.
- Commit: `repo-override-rulings M1: the reader of every enrolled repository's default branch`
- Reviewer tier: standard.

### M2: fugaro update's L8 stage: say first, then write, once
- Module: `migrate`.
- Acceptance: a stage before the Layer stage runs every registered planner, prints every line of
  every change before any write, then writes the layer file's edits (published by the Layer
  stage), the store objects and the database nodes; it asks nothing; a second run prints nothing
  and writes nothing; the in-flight line names the lowered caps; a repository it could not read is
  named and the rest converge.
- Files: `internal/cli/update_lift.go` (new), `internal/cli/update_lift_test.go` (new),
  `internal/cli/init_stages.go`, `internal/cli/update_in_flight.go`.
- Interfaces: Consumes C2.
- Tests: `TestLiftStageSaysFirst`, `TestLiftStageIdempotent`, `TestLiftStageNamesUnreadable`.
- Command: `go test ./internal/cli/ -run 'TestLiftStage' -count=1`
- Red step: `TestLiftStageSaysFirst` fails before the stage exists.
- Depends on: C2. Release gate.
- Commit: `repo-override-rulings M2: fugaro update's L8 stage`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### I2: integration: update converges a project whose repositories write every lifted key
- Module: `migrate`. Kind: integration (C2).
- Acceptance: `internal/lift/stubs.go` is deleted; `registry.go` registers MC1, AB3, RG2, PR4 and
  RC4 and wires M1 as the source; `fugaro update` on a fixture project at the previous version,
  whose repositories write every lifted key, converges, prints one line per changed repository
  before any write, and changes nothing on a second run (acceptance 1); a run of each repository
  resolves the same models (where they agreed), rigor, A/B rate and recipe text (acceptance 2);
  `TestUpdateLeavesARunInFlightRunning` is extended with a run holding a repository recipe and a
  repository cap (acceptance 8).
- Files: `internal/lift/stubs.go` (deleted), `internal/lift/registry.go` (new),
  `internal/e2e/update_test.go`, `internal/cli/update_converge_test.go`.
- Interfaces: Consumes M1, M2, MC1, AB3, RG2, PR4, RC4.
- Tests: `TestUpdateLiftsEveryKey`, `TestUpdateLiftKeepsEveryRunWhereItWas`, `TestUpdateLeavesARunInFlightRunning`.
- Command: `go test ./internal/cli/ -run 'TestUpdateLiftsEveryKey|TestUpdateLiftKeepsEveryRunWhereItWas|TestUpdateLeavesARunInFlightRunning' -count=1 && go test ./internal/e2e/ -run 'TestUpdate' -count=1`
- Red step: `TestUpdateLiftsEveryKey` fails while the stubs are registered.
- Depends on: M1, M2, MC1, AB3, RG2, PR4, RC4 (the sides it wires). Release gate.
- Commit: `repo-override-rulings I2: update converges a project whose repositories write every lifted key`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### I3: end to end: a migrated recipe reaches a run only from the project store
- Module: `project-recipes`.
- Acceptance: after I2's update, a first run, a follow-up and a launch from outside a checkout of a
  repository whose folder held `review.yaml` (clashing and not) all run the copy from the store,
  recorded `source: project`; a pre-L8 task naming the repository recipe is refused (acceptance 7).
- Files: `internal/cli/recipes_e2e_lift_test.go` (new), `internal/runner/recipe_embedded_lift_test.go` (new).
- Interfaces: Consumes PR1, PR2, PR3, I2.
- Tests: `TestMigratedRecipeReachesARunFromTheStore`.
- Command: `go test ./internal/cli/ -run 'TestMigratedRecipeReachesARunFromTheStore' -count=1 && go test ./internal/runner/ -run 'TestEmbeddedCopy' -count=1`
- Red step: the follow-up case fails until PR2 embeds the copy.
- Depends on: PR1, PR2, PR3 (kept, behaviour); I2 (kept, behaviour).
- Commit: `repo-override-rulings I3: a migrated recipe reaches a run only from the project store`
- Reviewer tier: standard.

### I1: integration: every reader of a repository's file drops every lifted key
- Module: `lift-keys`. Kind: integration (C1).
- Acceptance: every reader of report §5 and spec §6 (through `TestEveryConsumerResolvesTheSameBytes`
  and a runner test over `ResolveConfig`, `readBaseConfig` and the ref arm) on a file that writes
  every lifted key yields a resolved config with none of their values (acceptance 3); every lifted
  key is a retired `InRepo` row of `Scopes` and no MCR retired key is lifted (R1); the writers
  refuse (S4, RG3); the launch names and gates (S5).
- Files: `internal/cli/consumers_test.go`, `internal/cli/testdata/lifted/fugaro.yaml` (new),
  `internal/runner/lifted_readers_test.go` (new), `internal/config/lifted_rows_test.go` (new).
- Interfaces: Consumes C1, S0, S1, S2, S4, S5, RG3.
- Tests: `TestEveryReaderDropsLiftedKeys`, `TestLiftedKeysAreRetiredRows`.
- Command: `go test ./internal/cli/ -run 'TestEveryConsumerResolvesTheSameBytes|TestEveryReaderDropsLiftedKeys' -count=1 && go test ./internal/runner/ -run 'TestEveryReaderDropsLiftedKeys' -count=1 && go test ./internal/config/ -run 'TestLiftedKeysAreRetiredRows' -count=1`
- Red step: `TestLiftedKeysAreRetiredRows` fails until S0 has marked the rows.
- Depends on: S0, S1, S2, S4, S5, RG3 (the sides it wires).
- Commit: `repo-override-rulings I1: every reader of a repository's file drops every lifted key`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### DK1: model_grades in a repository's file and agent.compression refused; RepoModelGradesSet goes
- Module: `dead-keys` (D3299).
- Acceptance: `model_grades.*` in a repository's file is refused at decode with the fix naming the
  project layer, in the sentence model-credentials-routing X2 uses for `model_grades.*.access`
  (R11); `agent.compression`'s row loses `InRepo` (it is refused already, F16);
  `Config.RepoModelGradesSet` and its setter go.
- Files: `internal/config/scope.go`, `internal/config/grades.go`, `internal/config/resolve.go`,
  `internal/config/config.go`, `internal/config/dead_keys_test.go` (new).
- Interfaces: none new.
- Tests: `TestRepoModelGradesRefused`, `TestScopeTableMatchesDocs`.
- Command: `go test ./internal/config/ -run 'TestRepoModelGradesRefused|TestScopeTableMatchesDocs' -count=1`
- Red step: `TestRepoModelGradesRefused` fails: the key is dropped silently.
- Depends on: S0 (kept: `scope.go`); RT1 (kept: `resolve.go`). Release gate.
- Commit: `repo-override-rulings DK1: model_grades in a repository's file refused`
- Reviewer tier: standard.

### DK2: the merge's repository half of the grades and its skew gates go
- Module: `dead-keys` (D3299).
- Acceptance: `policy.tightenGrades` loses its repository half; `refuseModelGradesSkew` and the
  `recipes_skew.go` readers of `RepoModelGradesSet` read the layer alone; the build and the skew
  tests pass.
- Files: `internal/cli/recipes_skew.go`, `internal/cli/init.go`, `internal/policy/craft.go`,
  `internal/cli/dead_keys_skew_test.go` (new).
- Interfaces: none new.
- Tests: `TestModelGradesSkewReadsTheLayer`, `go test ./internal/policy/`.
- Command: `go test ./internal/cli/ -run 'TestModelGradesSkew' -count=1 && go test ./internal/policy/ -count=1`
- Red step: the build fails once DK1 removed the field; the new test pins the layer-only read.
- Depends on: DK1 (kept, behaviour); S5 (kept: `recipes_skew.go`). Release gate.
- Commit: `repo-override-rulings DK2: the grades' repository half goes`
- Reviewer tier: standard.

### D1: documents: recipes, layered config, rigor order
- Module: `project-recipes`, `review-tighten`, `rigor-repos`.
- Acceptance: `docs/recipes.md` and `docs/recipes/spec.md` §5 state the flat store, the rename on a
  clash and the lookup order (D3295, D3301, D3303) and that `.fugaro/recipes/` is not read;
  `docs/layered-config/spec.md` states B4's tighten rule; `docs/project-layer.md` and
  `docs/rigor-modes.md` state the new rigor order (D3294) and that a repository's level lives in
  `rigor.repos`; the Docs tests pass.
- Files: `docs/recipes.md`, `docs/recipes/spec.md`, `docs/layered-config/spec.md`, `docs/rigor-modes.md`, `docs/project-layer.md`.
- Interfaces: none.
- Tests: the Docs tests.
- Command: `go test ./internal/cli/ -run Docs -count=1`
- Red step: none (documents); a pinned sentence that moves is moved in its test in the same task.
- Depends on: S0 (kept: `project-layer.md`); RG1, RT1, PR1 (kept, behaviour: it states what they do).
- Commit: `repo-override-rulings D1: documents`
- Reviewer tier: standard.

### D2: skills: the model and rigor questions move to the project
- Module: `model-choice`, `rigor-repos`.
- Acceptance: the setup skill's step 3 and `reference/decisions.md` §3 ask the model question of
  the project (`fugaro models use --layer project`); the working skill's `agent.models.vision` "on
  the repository" says the project; the decoupling reference and spec-to-pr's `plan.md` read a
  repository's mode from `rigor.repos` and the member areas; the plugin tests pass.
- Files: `plugin/skills/setup/SKILL.md`, `plugin/skills/setup/reference/decisions.md`,
  `plugin/skills/working/SKILL.md`, `plugin/skills/parallelism/reference/decoupling.md`,
  `plugin/skills/spec-to-pr/reference/plan.md`.
- Interfaces: none.
- Tests: the plugin tests, the Docs tests.
- Command: `go test ./plugin/ -count=1 && go test ./internal/cli/ -run Docs -count=1`
- Red step: none (documents).
- Depends on: S4, RG3 (kept, behaviour: the plugin tests check the commands a skill names).
- Commit: `repo-override-rulings D2: skills`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`) (plugin skills).

### CK1: Checks 910 and 911, as scripts
- Module: `migrate`.
- Acceptance: `check-910-lift-snapshot.sh <project>` records, read-only, every enrolled
  repository's resolved model per role, rigor level and areas, A/B rate and recipe sums
  (`fugaro settings … --repo … --json`, `fugaro recipes ls --json`) and `fugaro update --plan-only`'s
  lines; `check-911-lift-compare.sh <project> <snapshot>` compares the same read after release A's
  update: PASS where every value matches or the update named the change, FAIL otherwise; each has a
  hermetic test against fakes; stdin closed, no token on an argv, nothing written.
- Files: `scripts/live-checks/check-910-lift-snapshot.sh` (new), `…_test.sh` (new),
  `scripts/live-checks/check-911-lift-compare.sh` (new), `…_test.sh` (new).
- Interfaces: none.
- Tests: the two `_test.sh` files through `go test ./scripts`.
- Command: `go test ./scripts/ -run 'TestLiveCheckScripts' -count=1`
- Red step: the tests fail before the scripts exist.
- Depends on: none.
- Commit: `repo-override-rulings CK1: Checks 910 and 911, as scripts`
- Reviewer tier: standard.
- Owner-run: the owner runs `bash scripts/live-checks/check-910-lift-snapshot.sh fugaro` before
  release A's `fugaro update`, and `bash scripts/live-checks/check-911-lift-compare.sh fugaro
  <the snapshot file 910 printed>` after it. No agent runs either.

### CK2: Check 912 as a script, and Checks 910 to 912 registered
- Module: `lift-keys`.
- Acceptance: `check-912-lift-refusal.sh <project> <checkout>` confirms after release B, read-only,
  that `fugaro validate` refuses a lifted key on the default branch with the fix and that a
  follow-up ref is named, not refused (no launch: `--plan-only` and `validate` only); the three
  checks are registered in `docs/gcp-live-checklist.md`, `scripts/live-checks/README.md` and
  `scripts/live_checks_test.go`.
- Files: `scripts/live-checks/check-912-lift-refusal.sh` (new), `…_test.sh` (new),
  `scripts/live_checks_test.go`, `docs/gcp-live-checklist.md`, `scripts/live-checks/README.md`.
- Interfaces: none.
- Tests: `go test ./scripts`, the Docs tests.
- Command: `go test ./scripts/ -count=1 && go test ./internal/cli/ -run Docs -count=1`
- Red step: the registration test fails before the rows exist.
- Depends on: CK1 (kept: it registers CK1's scripts).
- Commit: `repo-override-rulings CK2: Check 912, and Checks 910 to 912 registered`
- Reviewer tier: standard.
- Owner-run: the owner runs `bash scripts/live-checks/check-912-lift-refusal.sh fugaro <a checkout>`
  after release B is installed. No agent runs it.

### XB4: release B: the launch gate for the refusal
- Module: `lift-keys`. Gate: **not launched before release A is cut** (D3298).
- Acceptance: a default-branch launch on an image before release B is refused where the refusal
  would apply, with `fugaro update` as the fix (as MCR X1 gates X2).
- Files: `internal/cli/recipes_skew.go`, `internal/cli/l8b_skew_test.go` (new).
- Tests: `TestReleaseBGate`.
- Command: `go test ./internal/cli/ -run 'TestReleaseBGate' -count=1`
- Red step: the test fails: the launch goes through.
- Depends on: DK2 (kept: `recipes_skew.go`); I1, I3 (kept, behaviour:
  release B follows the whole of release A).
- Commit: `repo-override-rulings XB4: release B's launch gate`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### XB1: release B: config.Resolve refuses a lifted key on the default branch, ignores it on an older ref
- Module: `lift-keys`. Gate: not launched before release A is cut.
- Acceptance: the lifted rows lose `InRepo`; resolving a default branch's file that writes one is a
  problem with the fix (C1's sentence); resolving an older ref's file (a follow-up's base, a
  non-default `--ref`) drops it and names it (R2); `docs/project-layer.md` prints the table.
- Files: `internal/config/scope.go`, `internal/config/lifted_drop.go`, `internal/config/resolve.go`,
  `internal/config/lifted_refuse_test.go` (new), `docs/project-layer.md`.
- Tests: `TestLiftedKeyRefusedOnTheDefaultBranch`, `TestLiftedKeyIgnoredOnAnOlderRef`, `TestScopeTableMatchesDocs`.
- Command: `go test ./internal/config/ -run 'TestLiftedKey|TestScopeTableMatchesDocs' -count=1 && go test ./internal/cli/ -run Docs -count=1`
- Red step: `TestLiftedKeyRefusedOnTheDefaultBranch` fails: the key is dropped.
- Depends on: XB4 (kept, behaviour); DK1 (kept: two files); D1 (kept: `project-layer.md`).
- Commit: `repo-override-rulings XB1: release B refuses a lifted key on the default branch`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### XB2: release B: the runner's older ref, the launch's refusal and the recipes folder refused
- Module: `lift-keys`, `project-recipes`. Gate: not launched before release A is cut.
- Acceptance: the runner resolves a follow-up's, a review's and a `--ref` run's file in the
  older-ref mode, and the default branch in the refusing mode; a default-branch launch whose file
  writes a lifted key, or whose default branch holds `.fugaro/recipes/`, is refused with the fix
  and PR5's diff (acceptance 4, 7).
- Files: `internal/runner/followup.go`, `internal/runner/runner.go`, `internal/cli/run.go`,
  `internal/cli/recipes_cleanup.go`, `internal/cli/release_b_refusal_test.go` (new).
- Tests: `TestReleaseBRefusesAtLaunch`, `TestReleaseBFollowUpIgnoresAndNames`.
- Command: `go test ./internal/cli/ -run 'TestReleaseBRefusesAtLaunch' -count=1 && go test ./internal/runner/ -run 'TestReleaseBFollowUpIgnoresAndNames' -count=1`
- Red step: `TestReleaseBRefusesAtLaunch/recipes-folder` fails: the launch goes through.
- Depends on: XB1 (kept, behaviour); PR2 (kept: `run.go`).
- Commit: `repo-override-rulings XB2: release B refuses at the launch`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### XB3: release B: the schemas and the settings registry drop the lifted keys
- Module: `lift-keys`. Gate: not launched before release A is cut.
- Acceptance: `schemas/fugaro.schema.json` no longer accepts a lifted key; the settings registry and
  `schemas/settings.schema.json` give each lifted key the project scope only;
  `TestRegistryCoversEveryConfigField` passes.
- Files: `schemas/fugaro.schema.json`, `internal/settings/registry.go`, `schemas/settings.schema.json`,
  `internal/settings/lift_rows_test.go` (new).
- Tests: `TestRegistryCoversEveryConfigField`, `TestLiftedRowsAreProjectOnly`.
- Command: `go test ./internal/settings/ -run 'TestRegistryCoversEveryConfigField|TestLiftedRowsAreProjectOnly' -count=1`
- Red step: `TestLiftedRowsAreProjectOnly` fails on the repository scope.
- Depends on: XB1 (kept, behaviour).
- Commit: `repo-override-rulings XB3: the schemas drop the lifted keys`
- Reviewer tier: standard.

### XB5: release B: update refuses a lifted key on a default branch, with the fix
- Module: `migrate`. Gate: not launched before release A is cut.
- Acceptance: `fugaro update` names every enrolled repository whose default branch still writes a
  lifted key or holds `.fugaro/recipes/`, with the fix and the diff, and converges everything else;
  it asks nothing (spec §4's "refused at update", read under the HARD RULE as named, never as a
  stop of the whole update).
- Files: `internal/cli/update_lift.go`, `internal/cli/update_lift_refuse_test.go` (new).
- Tests: `TestUpdateNamesALiftedKeyStillWritten`.
- Command: `go test ./internal/cli/ -run 'TestUpdateNamesALiftedKeyStillWritten' -count=1`
- Red step: the test fails: nothing is named.
- Depends on: XB1 (kept, behaviour); I2 (kept: `update_lift.go`, through M2).
- Commit: `repo-override-rulings XB5: update names a lifted key still written`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

## Parallel execution

### Levels

| Level | Tasks |
| --- | --- |
| 0 | A1, C1, C2, PR0, S0, PR5, AB1, RG1, PR3, RC1, RC2, M1, CK1 |
| 1 | S1, S4, S5, AB2, AB3, MC1, RG2, PR1, PR2, PR4, RC3, RC4, M2, CK2 |
| 2 | RT1, RG3, I2 |
| 3 | RT2, S2, I3, DK1, D1, D2 |
| 4 | I1, DK2 |
| 5 | XB4 (release B, gated on release A) |
| 6 | XB1 |
| 7 | XB2, XB3, XB5 |

**Critical path** (8 tasks, as the graph read reports it): C1 → S1 → RT1 → DK1 → DK2 → XB4 → XB1 →
XB2. Release A ends at level 4. What must be sequential and why: `internal/config/resolve.go` is
written by S1, RT1, DK1 and XB1 in that order; `recipes_skew.go` by S5, DK2 and XB4; release B
waits for release A by the owner's ruling (D3298).

### Shared files

No level holds two writers of one file (checked against the graph's footprints):

| File | Writers, by level |
| --- | --- |
| `internal/config/resolve.go` | S1 (1), RT1 (2), DK1 (3), XB1 (6) |
| `internal/config/scope.go` | S0 (0), DK1 (3), XB1 (6) |
| `internal/config/lifted_drop.go` | S1 (1), XB1 (6) |
| `docs/project-layer.md` | S0 (0), D1 (3), XB1 (6) |
| `internal/cli/recipes_skew.go` | S5 (1), DK2 (4), XB4 (5) |
| `internal/cli/run.go` | PR2 (1), XB2 (7) |
| `internal/cli/recipes_cleanup.go` | PR5 (0), XB2 (7) |
| `internal/cli/update_lift.go` | M2 (1), XB5 (7) |
| `internal/lift/stubs.go` | C2 (0, creates), I2 (2, deletes) |
| `schemas/fugaro.schema.json` | S0 (0), XB3 (7) |
| `website/content/cli-reference.md` | S4 (1), RG3 (2) |

Across plans (reconciliation O2): model-credentials-routing's MG5 (`scope.go`, both schemas), MG6
(`models_use.go`, `run.go`, the CLI reference) and X2 (`config.go`, `layer.go`, `validate.go`, both
schemas) are not ordered against this plan; the second to merge rebases.

## The dependencies broken and kept

The decoupling switch is on for `dimipaun/fugaro` (`fugaro settings planning.decoupling --json
--repo dimipaun/fugaro`: `true`, the default), and the repository's mode is `startup`
(`rigor.default`), so the pass decoupled wherever only an interface crossed an edge.

**Broken (14 edges, two contracts):**

- C1 breaks S1, S2, S4 and S5's wait on S0: each needs only which keys are lifted, which the
  contract lists, not the scope rows S0 marks. Integration: I1.
- C2 breaks MC1, AB3, RG2, PR4 and RC4's wait on M1 (a planner needs the shape of a repository's
  files, not the provider read that fills it) and M2's wait on each of those five (the stage needs a
  planner's signature and its change type, not what it computes). Integration: I2.

**Kept (38 edges)**, each with its reason in the graph; the families: one file (`resolve.go`,
`scope.go`, `recipes_skew.go`, `run.go`, `update_lift.go`, `project-layer.md`, the CLI reference);
behaviour (RT2 on RT1's mark, S2 on RT1's record, DK2 on DK1, the documents and skills on what
they describe, every release-B task on release A); and four small functions a contract would equal
(PR0's copy name and lookup, AB1's field, RC1's ceiling, PR5's cleanup lines).

**Considered and not broken:** a contract for B16's copy name (PR0 is the function itself, so it
stays a task); a contract for `ab.repos` (the field is the whole of AB1).

**The gain**, as the read reports it: 14 broken, 38 kept, 6 added; before 8 tasks, weight 18;
after 8 tasks, weight 17; improves. The critical path is release B's chain; the contracts widen
level 1 (fourteen tasks run at once) rather than shorten it.

**Contract changes anticipated:**

| Change | Contract | Raised by | When | Reaches |
| --- | --- | --- | --- | --- |
| R-lift-phase | C1 | XB1 | Release B's refusal needs a per-key phase in the list rather than the scope rows' `InRepo` bit | S1, S2, S4, S5 |
| R-planner-change | C2 | PR4 | A store write or a database write needs more than `Change` carries | MC1, AB3, RG2, RC4, M2 |

## The estimate

`fugaro plan graph --plan docs/repo-override-rulings/implementation-plan.md` printed:

`ready in 347–1535 min, no history yet for 20 tasks (45 tasks; tiers for 25, seeds for 20; from the cached history of 2026-11-22 07:25 UTC)`

It is informational and reserves no budget: it is what the tasks' tiers and this machine's cached
history predict, and it moves as runs finish and as the graph changes. The 45 count includes the
two contract changes, which run only if raised. **Proposed runs:** 43 (the two contracts, 39 tasks and the two integrations),
plus up to two contract changes, plus one independent review (`fugaro review --pr N`) for each
opus-tier task and for C1 and C2 before their consumers launch.

## Checks the plan takes

Block **910–919**; this plan uses 910–912.

| Check | What it measures | Who runs it, when |
| --- | --- | --- |
| 910 | Every enrolled repository's resolved models, rigor, A/B rate and recipe sums, and `update --plan-only`'s L8 lines, before release A | The owner, before release A's `fugaro update` (CK1) |
| 911 | The same read after release A's update, compared with 910: every value the same or named by the update | The owner, after release A's `fugaro update` (CK1) |
| 912 | Release B refuses a lifted key on the default branch with the fix and names it on an older ref | The owner, after release B is installed (CK2) |

## After-deploy steps

None beyond `fugaro update`. Release A's notes (`docs/releases/`, written with the release) say
under `### For operators`: run `fugaro update`; it prints first one line per repository whose model,
rigor, A/B rate, recipe name or cap changes, and each recipe it renamed; every member upgrades the
CLI (R9); and `doctor` offers each repository's `git rm -r .fugaro/recipes` as a diff. Release B's
say: run `fugaro update`; a repository's file that still writes a lifted key is refused on its
default branch, with the fix. M2's and XB5's pull requests carry those lines as release-actions
entries.
