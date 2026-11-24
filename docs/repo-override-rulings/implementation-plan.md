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
| D3302, amended after review (`<name>.<owner-repo>-<8 hex>`, at most 96 characters; the owner's own answer) | PR0's grammar, PR6's checks and S5's `recipeCopySince` gate |
| D3303 (the pointer is a lookup rule, no new data) | PR0's lookup order, PR1 and PR2 |
| D3304 (a pre-L8 task naming a repository recipe is refused at bootstrap) | PR3 |
| D3305 (migrate once per repository, recorded) | C2's `Project`, M2's record, RG2, PR4, XB5 |
| D3306 (the real list, fail open; **not the recommendation**) | M1, M2's lines, release A's notes |
| D3307 (O5's residue accepted) | PR3; release A's notes |
| D3308 (O6: the launcher refuses an unmigrated repository's folder recipe) | C2's `RecordPath`, PR1 |

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

1. **Contracts and the inert first slice** (level 0): A1, C1, C2, PR0, S0, AB1, RG1, PR3, RC1,
   RC2, M1. Nothing here changes what a run or a command does except PR3, AB1, RG1, RC1 and RC2,
   which are held by the release gate below.
2. **Release A, items and planners**: S1, S4, S5, AB2, AB3, MC1, RG2, PR5, PR6, PR4, M2, RC3, RC4,
   RT1, RG3, PR1, PR2, PR7, D3, CK1, CK2, CK3, RT2, RT3, S2, DK1, DK2, DK3, D1, D2.
3. **Release A, integrations**: I2, I3, I1.
4. **Release B** (D3298): XB4, XB1, XB2, XB3, XB5, XB6.

**The release gate (R18, amended after review).** Contracts, planners, check scripts and S0 may
merge at any time: no caller reaches a planner before I2 registers it, and S0 changes only sentences
that are true today. Every other release-A task — S1, RT1, RT2, S2, S4, S5, AB1, AB2, RG1, RG3, PR1,
PR2, PR3, PR5, PR6, PR7, RC1, RC2, RC3, RT3, M2, I2, I3, I1, DK1, DK2, DK3, D1, D2, D3 (the **gated set**) — is **not launched before the release
carrying L1 has been tagged** (`docs/release.md`: a `vX.Y.Z` tag is cut from `main`'s head), so
release A is at least one release after L1's (D3300).

**The release fence.** Release A lands **as one batch**: from the first merge of the gated set until
**every** task of the gated set has merged, no CLI tag is cut, so no release carries half of release A (a launcher that drops
keys before `update` can migrate them, or a runner that refuses a recipe no launcher embeds yet).
Release B is fenced the same way: XB4, XB1, XB2, XB3, XB5 and XB6 are not launched before release A has
been tagged (D3298), and from the first of them to merge until the last has, no CLI tag is cut.
**The tagged commit.** `docs/release.md` allows tagging an earlier green commit, so the fence is held
at the tag too, as an owner step of the release (`scripts/release.sh` is not changed): before
tagging, the person cutting the release runs
`git log --format=%s <the commit> | grep -oE '^(# Task: )?repo-override-rulings [A-Z]+[0-9]+:'`
(a run's commit may carry its brief's `# Task: ` line as the subject), reduces each match to its task
id, compares the ids with the gated set's, and tags only a commit that holds all of them or none; release A's and
release B's notes record that it was checked. A
batch rather than a switch, because a switch is code the spec does not ask for and a tag freeze
costs nothing; console and mobile tags are not affected.

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
  `agent.levels.l1–l3.model` (D3291), `ab.rate`, `rigor.default`, `rigor.areas`, and L13's
  `model_grades.strong.model` and `model_grades.strongest.model` (D3299); never a
  `model_grades.*.access` row, which is MCR's), each with its new home and its fix sentence, and
  `LiftedKeyNotes(data []byte) []RetiredKeyNote`, which names every lifted key a repository's file
  writes. No lifted key is marked `Retired` and no MCR retired key is on the list (R1, amended after
  review). `contract-lift-keys.md` says what each side may rely on: the list is the only answer to
  "is this key lifted", the sentence is the one refusal text release B prints, and `config.Resolve`
  is the only place that drops.
- Files: `internal/config/lifted.go` (new), `internal/config/lifted_test.go` (new),
  `docs/repo-override-rulings/contract-lift-keys.md` (new).
- Interfaces: Produces `config.LiftedKeys`, `config.LiftedKeyNotes`, which S1, S2, S4 and S5 consume.
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
  price table, and the set of repositories already migrated, D3305), `Change` (layer edits by path,
  store writes by key, database writes by path, the migrated records to write, and the lines to
  print before any of them), `Planner func(Project, []Repo) (Change, error)` and
  `Source func(ctx) ([]Repo, []string, error)` (the repositories, and the admit patterns it could not
  list, D3306) and `RecordPath(slug string) string` (`fugaro/lift/l8/<slug>.json`, the one name
  M2 writes and PR1 reads, D3305, D3308); `internal/lift/stubs.go` registers the stub planners, which return no change;
  `contract-migrate.md` says a planner is pure, never writes, acts only on a repository not yet
  migrated, never overwrites a project value, names (never applies) a file value of a repository
  already migrated, and names every repository whose runs change.
- Files: `internal/lift/lift.go` (new), `internal/lift/stubs.go` (new),
  `docs/repo-override-rulings/contract-migrate.md` (new).
- Interfaces: Produces the types MC1, AB3, RG2, PR4, RC4, RT3 and M2 consume. Stubs: `internal/lift/stubs.go`.
- Tests: `TestStubPlannersChangeNothing`.
- Command: `go test ./internal/lift/ -count=1`
- Red step: the test fails before the package exists.
- Depends on: none. Breaks every planner's wait on M1 and M2's wait on every planner.
- Commit: `repo-override-rulings C2: contract: the migration's repository files and planners`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PR0: a recipe's project copy, its name and its lookup order
- Module: `project-recipes`.
- Acceptance: `recipe.CopyName(name, provider, repo string) string` returns
  `<name>.<owner-repo>-<8 hex>`: the readable half of `task.Slug` and the first 8 hex of its hash
  (D3302, amended after review); `recipe.IsCopyName` and `recipe.CopyNameRE` accept it (a recipe
  name, one dot, a readable half, a hyphen and 8 hex; at most 96 characters) and nothing else with
  a dot; `recipe.LookupOrder(name, provider, repo string) []string` is `[<copy>, <name>]` (D3303), the
  catalog last.
- Files: `internal/recipe/copyname.go` (new), `internal/recipe/copyname_test.go` (new).
- Interfaces: Produces the functions PR6, PR1, PR2, PR4 and S5 call.
- Tests: `TestCopyName`, `TestCopyNameKeepsCollidingSlugsApart` ("acme/app-web" and "acme-app/web"
  share a readable half and get two names), `TestCopyNameAtMost96`, `TestLookupOrder`, `TestNameREStillRefusesADot`.
- Command: `go test ./internal/recipe/ -run 'TestCopyName|TestLookupOrder|TestNameREStillRefusesADot' -count=1`
- Red step: `TestCopyName` fails before the file exists.
- Depends on: none. Implements D3302 (amended) and D3303. Kept as a task, not a contract: the
  functions are the interface, and a contract would equal them.
- Commit: `repo-override-rulings PR0: a recipe's project copy, its name and its lookup order`
- Reviewer tier: standard.

### PR6: every recipe-name check that refuses a dot accepts a copy name
- Module: `project-recipes`.
- Acceptance: `recipe.Parse`'s `name:` check, `schemas/recipe.schema.json`'s `name` pattern, the
  task's recipe validation (`internal/task`'s `Spec` validation of `Recipe.Name`) and the recipe
  cache (`internal/localcfg/recipecache.go`) accept a name `recipe.IsCopyName` accepts, and still
  refuse any other dotted name (R19).
- Files: `internal/recipe/recipe.go`, `schemas/recipe.schema.json`, `internal/task/task.go`,
  `internal/localcfg/recipecache.go`, `internal/recipe/copyname_accept_test.go` (new).
- Interfaces: Consumes PR0.
- Tests: `TestEveryNameCheckAcceptsACopy` (Parse, the task, the cache), `TestRecipeSchemaAcceptsACopy`.
- Command: `go test ./internal/recipe/ ./internal/task/ ./internal/localcfg/ -run 'TestEveryNameCheckAcceptsACopy|TestRecipeSchemaAcceptsACopy|Recipe' -count=1`
- Red step: `TestEveryNameCheckAcceptsACopy/parse` fails: "must be a recipe name".
- Depends on: PR0 (kept: the grammar). Release gate.
- Commit: `repo-override-rulings PR6: every recipe-name check accepts a copy name`
- Reviewer tier: standard.

### S0: the scope table
- Module: `lift-keys`.
- Acceptance: every kept row's `Why` in `config.Scopes` cites D3237; no lifted row is marked
  `Retired` (R1, amended after review: that would break `TestEveryRetiredKeyIsNamedWhereItIsWritten`
  and would announce a move `update` cannot make yet); `docs/project-layer.md` prints the table
  (`TestScopeTableMatchesDocs`).
- Files: `internal/config/scope.go`, `docs/project-layer.md`, `internal/config/scope_lift_test.go` (new).
- Interfaces: none new.
- Tests: `TestScopeTableMatchesDocs`, `TestKeptRowsCiteD3237`, `TestEveryRetiredKeyIsNamedWhereItIsWritten`, the Docs tests.
- Command: `go test ./internal/config/ -run 'TestScopeTableMatchesDocs|TestKeptRowsCiteD3237|TestEveryRetiredKeyIsNamedWhereItIsWritten' -count=1 && go test ./internal/cli/ -run Docs -count=1`
- Red step: `TestKeptRowsCiteD3237` fails on today's rows.
- Depends on: none. **Not release-gated**: it changes sentences that are true today (each kept
  row was kept by D3237) and no behaviour.
- Commit: `repo-override-rulings S0: the scope table cites D3237`
- Reviewer tier: standard.

### S1: config.Resolve drops the lifted keys, on every ref
- Module: `lift-keys`.
- Acceptance: both arms of `config.Resolve` (with and without a layer) drop every key
  `config.LiftedKeys` lists before the merge, record each in the resolution with its note, and leave
  MCR's retired keys as they are; the repository-own fields computed from the file —
  `Config.RepoRigor`, `RepoRigorSet`, `RepoRigorLevelSet`, `RepoRigorScalarSet`,
  `RepoLevelsNoneModelSet` — are computed after the drop, while an empty `rigor:` block still opts
  in (R2); `config.AgentOf`, which bypasses `Resolve`, drops the same keys from the agent block it
  returns (R21, F25); a file that writes only lifted keys resolves to the project's (or Fugaro's)
  values; `schemas/fugaro.schema.json` describes each lifted key as read and not honoured.
- Files: `internal/config/resolve.go`, `internal/config/lifted_drop.go` (new),
  `internal/config/lifted_drop_test.go` (new), `schemas/fugaro.schema.json`, `internal/config/config.go`.
- Interfaces: Consumes C1. Produces the dropped keys in `Resolution`, which S2 and I1 read.
- Tests: `TestResolveDropsLiftedKeys` (one case per key, with and without a layer),
  `TestResolveKeepsMCRRetiredKeys`, `TestRepoRigorFlagsSeeNoLiftedKey`, `TestAgentOfDropsLiftedKeys`.
- Command: `go test ./internal/config/ -run 'TestResolveDropsLiftedKeys|TestResolveKeepsMCRRetiredKeys|TestRepoRigorFlagsSeeNoLiftedKey|TestAgentOfDropsLiftedKeys' -count=1`
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
- Acceptance: where RT1 marked a value unchecked, the baseline is where the defaults are applied
  (R20): `config.ResolveLevels` takes the tighter of the repository's level settings and the mode's
  `rigor.DefaultLevels` row (`none`, effort); the runner's `setRounds` and the launch's budget and
  preview take the tighter of the repository's rounds and `rigor.DefaultRounds` /
  `rigor.AdversarialDefault` at the mode the run is at; a key no mode row holds is judged against
  Fugaro's `applyDefaults` value; the record names which decided. A silent project, a critical run
  and a repository's `l3.none: true` leave L3 running.
- Files: `internal/config/levels.go`, `internal/runner/plan.go`, `internal/cli/launch_budget.go`,
  `internal/cli/budget_preview.go`, `internal/runner/plan_tighten_test.go` (new).
- Interfaces: Consumes RT1's mark.
- Tests: `TestSilentProjectCriticalRunKeepsL3`, `TestUncheckedRoundsTakeTheMode`, `TestLaunchBudgetUsesTheTighter`.
- Command: `go test ./internal/config/ -run 'TestSilentProjectCriticalRunKeepsL3' -count=1 && go test ./internal/runner/ -run 'TestUncheckedRoundsTakeTheMode' -count=1 && go test ./internal/cli/ -run 'TestLaunchBudgetUsesTheTighter' -count=1`
- Red step: `TestSilentProjectCriticalRunKeepsL3` fails: the repository turns L3 off.
- Depends on: RT1 (kept, behaviour); PR1 (kept: one file, `budget_preview.go`). Release gate.
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
  `[0, ab.rate]` (D3297), keyed as `rigor.repos` is; `config.AB` is shared with a repository's file,
  so `validateRepoAB` refuses `ab.repos` there explicitly, with the fix; `ProjectLayer.SetsABRepos`
  says whether the layer writes it; the settings registry carries the row
  (`internal/settings/meta_layer.go`, so `TestRegistryCoversEveryConfigField` passes) and
  `schemas/settings.schema.json` and `schemas/project-layer.schema.json` describe it.
- Files: `internal/config/ab.go`, `internal/config/ab_repos_test.go` (new),
  `schemas/project-layer.schema.json`, `internal/settings/meta_layer.go`, `schemas/settings.schema.json`.
- Interfaces: Produces `config.AB.Repos`, `ProjectLayer.SetsABRepos`.
- Tests: `TestABReposBounds`, `TestABReposRefusedInARepoFile`, `TestRegistryCoversEveryConfigField`.
- Command: `go test ./internal/config/ -run 'TestABRepos' -count=1 && go test ./internal/settings/ -run 'TestRegistryCoversEveryConfigField' -count=1`
- Red step: `TestABReposBounds` fails: the strict decode refuses `repos`.
- Depends on: none.
- Commit: `repo-override-rulings AB1: ab.repos in the layer's ab block`
- Reviewer tier: standard.

### AB2: ee/ab reads a repository's rate from ab.repos
- Module: `ab-repos`.
- Acceptance: `abRate` in `ee/ab/launch.go`, the one reader that samples, feeds the repository's
  rate from `ab.repos.<repo>` through `policy.Merge`, unchanged; a repository's own `ab.rate` no
  longer reaches it (dropped by S1); the new file carries the `ee/` notice. (`ee/ab/cli`'s
  `abRateSource` and `ee/report`'s `abRate` read the exchange rates, not the sample, and are not
  touched.)
- Files: `ee/ab/launch.go`, `ee/ab/repos_rate_test.go` (new).
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
  changes is named; a repository whose own `budget.allowed_models` excludes the chosen value is
  not counted for it and is named with the fix before the write (R17); `budget.allowed_models` is
  not written.
- Files: `internal/lift/models.go` (new), `internal/lift/models_test.go` (new).
- Interfaces: Consumes C2. Produces `lift.PlanModels`.
- Tests: `TestPlanModels` (project sets, all agree, majority, tie by price, levels models, a repository's allow-list excludes the choice).
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
  entry or a member area naming it, else the repository's empty `rigor:` block, which still opts in
  until release B (XB3) — its `rigor.default` and `rigor.areas` are dropped from release A by S1.
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
- Acceptance: for a repository not yet migrated (D3305), its `rigor.default` becomes
  `defaults.rigor.repos.<repo>` unless the project already has an entry for it, which stays (the
  file's value is named); each `rigor.areas` rule becomes a project area `{repo, paths: [its
  match]}` keeping its name unless any project area already has it (then `-<owner-repo>`, cut to 40,
  a short hash on a second collision, R4); an empty `rigor:` block becomes `rigor.repos.<repo>` at
  the level it resolves to today; every repository and path whose level changes under D3294's
  order, raised or lowered, is named with the old and new level (R5).
- Files: `internal/lift/rigor.go` (new), `internal/lift/rigor_test.go` (new).
- Interfaces: Consumes C2. Produces `lift.PlanRigor`.
- Tests: `TestPlanRigor` (default, existing entry kept, areas, name taken, name too long, empty block, a raise named, a lowering named, already migrated).
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
- Acceptance: `resolveRecipe` accepts a copy name and never reads the checkout's
  `.fugaro/recipes/`; it reads the store in `recipe.LookupOrder` and then the catalog; `fugaro
  recipes ls`, `publish` and `rm` accept copy names, so an admin can replace or remove a migrated
  copy, and `ls` says the folder is not read; `refuseFollowUpRepoRecipe` judges the project copy;
  `budget_preview.go`'s `repoHasRecipe` stops reading the folder and accepts a copy name (R19, R22).
  A launch of a repository with no record at `lift.RecordPath` whose default branch holds
  `.fugaro/recipes/<name>.yaml` for the recipe it resolves is refused before anything is written,
  naming the repository, the recipe and the fix (list the repository in the project, then `fugaro
  update`); it asks nothing, and a migrated repository or one with no such file launches as before
  (D3308). The default-branch read is the one S5's gate makes (R16).
- Files: `internal/cli/recipes_resolve.go`, `internal/cli/recipes.go`, `internal/cli/budget_preview.go`,
  `internal/cli/recipes_lift_test.go` (new).
- Interfaces: Consumes PR0, PR6, C2's `RecordPath`.
- Tests: `TestResolveRecipeOwnCopyFirst`, `TestResolveRecipeIgnoresTheFolder`, `TestRecipesCommandsAcceptACopy`, `TestRecipesLsSaysTheFolderIsNotRead`, `TestLaunchRefusesAnUnmigratedFolderRecipe` (refused without a record; launched with one; launched with no folder file).
- Command: `go test ./internal/cli/ -run 'TestResolveRecipeOwnCopyFirst|TestResolveRecipeIgnoresTheFolder|TestRecipesCommandsAcceptACopy|TestRecipesLsSaysTheFolderIsNotRead|TestLaunchRefusesAnUnmigratedFolderRecipe' -count=1`
- Red step: `TestResolveRecipeIgnoresTheFolder` fails: the checkout's file wins; `TestLaunchRefusesAnUnmigratedFolderRecipe` fails: the launch runs the catalog's recipe.
- Depends on: PR0 (kept); PR6 (kept, behaviour: a copy must parse); C2 (the record's path). Implements D3303 and D3308. Release gate.
- Commit: `repo-override-rulings PR1: the launcher resolves a repository's own copy first`
- Reviewer tier: standard.

### PR5: the cleanup diff, and validate prints it
- Module: `project-recipes`.
- Acceptance: `recipesCleanupLines(root, store)` reads the project store through
  `recipe.LookupOrder` and the stored sums: for a checkout holding `.fugaro/recipes/` it offers the
  diff `git rm -r .fugaro/recipes` only when every file there has its copy in the store (one line
  per file naming the copy), and otherwise names, per file with no copy, `fugaro recipes publish
  <file>` first and offers no removal; `validate` prints the lines; nothing is deleted.
- Files: `internal/cli/recipes_cleanup.go` (new), `internal/cli/validate_recipes.go`,
  `internal/cli/recipes_cleanup_test.go` (new).
- Interfaces: Consumes PR0. Produces `recipesCleanupLines`, which S2, PR2, XB2 and XB5 call.
- Tests: `TestRecipesCleanupOffersRemovalOnlyWhenEveryCopyIsStored`, `TestRecipesCleanupNamesPublishFirst`.
- Command: `go test ./internal/cli/ -run 'TestRecipesCleanup' -count=1`
- Red step: the tests fail before the file exists.
- Depends on: PR0 (kept: the lookup order). Release gate.
- Commit: `repo-override-rulings PR5: the cleanup diff`
- Reviewer tier: standard.

### PR2: every launch path embeds the project copy
- Module: `project-recipes`.
- Acceptance: a first run from outside a checkout reads `agent.recipe` from the ref's file and
  embeds the copy; a follow-up that inherits a repository recipe by name embeds its copy, and an
  inherited copy name is carried rather than dropped (`internal/cli/followup.go` drops today, without
  a word, a name `NameRE` refuses, R19); the `default` fallback embeds a repository's own `default`
  copy when one exists; no task this launcher writes carries `Source: repo` (R7); the launch prints
  PR5's cleanup lines.
- Files: `internal/cli/run.go`, `internal/cli/followup.go`, `internal/cli/recipes_launch_lift_test.go` (new).
- Interfaces: Consumes PR0, PR5, PR6.
- Tests: `TestLaunchEmbedsTheCopy` (outside a checkout, follow-up, default fallback), `TestFollowUpCarriesACopyName`, `TestNoTaskCarriesARepoRecipe`.
- Command: `go test ./internal/cli/ -run 'TestLaunchEmbedsTheCopy|TestFollowUpCarriesACopyName|TestNoTaskCarriesARepoRecipe' -count=1`
- Red step: `TestFollowUpCarriesACopyName` fails: the inherited name is dropped.
- Depends on: PR0 (kept); PR5 (kept); PR6 (kept, behaviour). Implements D3303. Release gate.
- Commit: `repo-override-rulings PR2: every launch path embeds the project copy`
- Reviewer tier: standard.

### PR7: the launcher's dead repository-recipe branches go
- Module: `project-recipes`.
- Acceptance: once no launcher writes `Source: repo` (PR2), the branches that handle a
  repository-sourced recipe in `internal/cli/reviewspec.go` and `internal/cli/run_digest_manifest.go`
  are removed; the build and their tests pass.
- Files: `internal/cli/reviewspec.go`, `internal/cli/run_digest_manifest.go`, `internal/cli/recipes_dead_branches_test.go` (new).
- Interfaces: none new.
- Tests: `TestNoLauncherPathReadsARepoRecipe`, the existing review-spec and digest tests.
- Command: `go test ./internal/cli/ -run 'TestNoLauncherPathReadsARepoRecipe|ReviewSpec|Digest' -count=1`
- Red step: `TestNoLauncherPathReadsARepoRecipe` fails while the branches exist.
- Depends on: PR2 (kept, behaviour). Release gate.
- Commit: `repo-override-rulings PR7: the dead repository-recipe branches go`
- Reviewer tier: standard.

### PR3: the runner stops reading .fugaro/recipes/
- Module: `project-recipes`.
- Acceptance: a release-A runner resolves the embedded recipe or a catalog one, never the
  repository's folder; a task naming a repository recipe (`Source: repo`) is refused at bootstrap
  with the fix (D3304, R8); so is a task that embeds no recipe on a ref that still holds
  `.fugaro/recipes/<name>.yaml` for the name it resolves (today the runner would read that file);
  a task that embeds none, on a ref whose folder is already gone, for a name the catalog has, runs
  the catalog recipe, which nothing at the ref can contradict (open item O5); a run already
  bootstrapped is untouched.
- Files: `internal/runner/recipe.go`, `internal/runner/recipe_lift_test.go` (new).
- Interfaces: none new.
- Tests: `TestRunnerNeverReadsTheFolder`, `TestRepoSourcedTaskIsRefused`, `TestUnembeddedTaskWithTheFileStillThereIsRefused`.
- Command: `go test ./internal/runner/ -run 'TestRunnerNeverReadsTheFolder|TestRepoSourcedTaskIsRefused|TestUnembeddedTaskWithTheFileStillThereIsRefused' -count=1`
- Red step: `TestRunnerNeverReadsTheFolder` fails: `readRepoRecipe` reads it.
- Depends on: none. Implements D3304. Release gate.
- Commit: `repo-override-rulings PR3: the runner stops reading .fugaro/recipes/`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### PR4: the planner that publishes repository recipes flat, renaming clashes
- Module: `project-recipes`.
- Acceptance: on a repository's first migration only (D3305), every regular file of
  `.fugaro/recipes/*.yaml` on its default branch that parses (`recipe.Parse`, 16 KiB) is published
  flat under its name when nothing else answers that name; on a clash (D3301: another repository's
  file, an existing project recipe or a catalog name, with different text) each clashing copy is
  published as `recipe.CopyName` with its `name:` line rewritten and nothing else (R14), one line per
  rename; two repositories whose copies would take one name are both named and neither is
  published; identical texts publish once; a file that does not parse is named and left; a
  repository already migrated publishes nothing and has a changed file named.
- Files: `internal/lift/recipes.go` (new), `internal/lift/recipes_test.go` (new).
- Interfaces: Consumes C2, PR0. Produces `lift.PlanRecipes`.
- Tests: `TestPlanRecipes` (unique, two repositories clash, project clash, catalog clash, same text, invalid file, copy-name collision, already migrated).
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
  ceiling; the change is named in `rules_test.go`'s `namedRuleChanges`; the rules' version marker
  moves if the rules generation must (so `update` publishes it); the golden file and an emulator test
  agree.
- Files: `internal/budget/rules/rules.go`, `internal/budget/rules/rules.json.tmpl`,
  `internal/budget/rules/testdata/rules.golden.json`, `internal/budget/rules/rules_test.go`,
  `internal/budget/rules/lease_ceiling_emulator_test.go` (new).
- Interfaces: none new.
- Tests: `TestLeaseClampsRepoCap` (emulator), the rules golden test, the named-changes test.
- Command: `go test -tags firebase ./internal/budget/rules/ -run 'TestLeaseClampsRepoCap' -count=1 && go test ./internal/budget/rules/ -count=1`
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

### M1: the reader of every repository update can list
- Module: `migrate`.
- Acceptance: `repofiles.Read` lists the repositories it can name — the local config's repositories
  and every concrete owner/name in the published policy's admits — and returns each one's default
  branch `fugaro.yaml` (`gitprov.ContentsReader`) and `.fugaro/recipes/` files (`gitprov.TreeReader`,
  bounded by `recipe.MaxBytes`), or the one-line reason it could not read that repository, and every
  admit pattern (`*`, `github/acme/*`) it could not expand (D3306, R12); it reads no working
  directory and refuses nothing; a test covers each provider's fixture, a host with neither
  interface (U3) and a pattern admit.
- Files: `internal/repofiles/reader.go` (new), `internal/repofiles/reader_test.go` (new).
- Interfaces: Produces `repofiles.Read`, which I2 adapts to C2's `Source` and L5 can call (O1).
- Tests: `TestReadEveryListedRepo`, `TestReadNamesAnUnreadableRepo`, `TestReadNamesAPatternItCannotList`.
- Command: `go test ./internal/repofiles/ -count=1`
- Red step: the test fails before the package exists.
- Depends on: none.
- Commit: `repo-override-rulings M1: the reader of every repository update can list`
- Reviewer tier: standard.

### M2: fugaro update's L8 stage: say first, then write, once
- Module: `migrate`.
- Acceptance: a stage before the Layer stage runs every registered planner over the repositories
  not yet migrated (D3305), prints every line of every change before any write — including each
  admit pattern it could not list and each repository it could not read, whose lifted values are
  dropped and never moved (D3306) — then writes the layer file's edits (published by the Layer
  stage), the store objects and the database nodes, each behind the same `confirmStageWrite` gate
  every other stage's write passes (`internal/cli/init.go`); a repository's record
  `fugaro/lift/l8/<slug>.json` in the runs bucket is written only after the Layer stage's publish of
  that repository's edits has landed, and never when the publish is refused or skipped; it asks
  nothing; a second run
  prints only the names of later file values and writes nothing; the in-flight line names the
  lowered caps. `fugaro update --plan-only` runs the planners, prints the same lines and writes
  nothing, no record included.
- Files: `internal/cli/update_lift.go` (new), `internal/cli/update_lift_test.go` (new),
  `internal/cli/init_stages.go`, `internal/cli/update_in_flight.go`.
- Interfaces: Consumes C2.
- Tests: `TestLiftStageSaysFirst`, `TestLiftStageRecordsOnlyAfterThePublish`, `TestLiftStageMigratesOnce`, `TestLiftStageNamesUnlistedPatterns`, `TestLiftStagePlanOnlyWritesNothing`.
- Command: `go test ./internal/cli/ -run 'TestLiftStage' -count=1`
- Red step: `TestLiftStageSaysFirst` fails before the stage exists.
- Depends on: C2. Release gate.
- Commit: `repo-override-rulings M2: fugaro update's L8 stage`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### RT3: the planner that names the repositories whose runs now stop earlier
- Module: `review-tighten`.
- Acceptance: for each repository not yet migrated, a value B4's rule drops (R20) is named before
  any write — above all an `agent.max_budget_usd` above a silent project's baseline,
  `config.DefaultMaxBudgetUSD` (25), whose runs will now stop at 25; the change writes nothing.
- Files: `internal/lift/tighten.go` (new), `internal/lift/tighten_test.go` (new).
- Interfaces: Consumes C2. Produces `lift.PlanTighten`.
- Tests: `TestPlanTightenNamesTheBudgetCap`.
- Command: `go test ./internal/lift/ -run 'TestPlanTighten' -count=1`
- Red step: the test fails before the file exists.
- Depends on: C2; RT1 (kept, behaviour: it names what RT1 drops).
- Commit: `repo-override-rulings RT3: update names the repositories whose runs stop earlier`
- Reviewer tier: standard.

### I2: integration: update converges a project whose repositories write every lifted key
- Module: `migrate`. Kind: integration (C2).
- Acceptance: `internal/lift/stubs.go` is deleted; `registry.go` registers MC1, AB3, RG2, PR4, RC4
  and RT3 and wires M1 as the source; `fugaro update` on a fixture project at the previous version,
  whose repositories write every lifted key, converges, prints one line per changed repository
  before any write, writes each repository's migrated record, and changes nothing on a second run
  (acceptance 1); a later project value survives a third run (D3305); a run whose layer publish is
  refused writes no migrated record, so the next `update` migrates those repositories again; a run of each repository
  resolves the same models (where they agreed), rigor, A/B rate and recipe text (acceptance 2);
  `TestUpdateLeavesARunInFlightRunning` is extended with a run holding a repository recipe and a
  repository cap (acceptance 8).
- Files: `internal/lift/stubs.go` (deleted), `internal/lift/registry.go` (new),
  `internal/e2e/update_test.go`, `internal/cli/update_converge_test.go`, `internal/cli/update_in_flight_test.go`.
- Interfaces: Consumes M1, M2, MC1, AB3, RG2, PR4, RC4, RT3, PR1.
- Tests: `TestUpdateLiftsEveryKey`, `TestUpdateLiftKeepsEveryRunWhereItWas`, `TestUpdateLiftNeverOverwritesALaterProjectValue`, `TestUpdateLiftRefusedPublishWritesNoRecord`, `TestUpdateLeavesARunInFlightRunning`, `TestLaunchAfterUpdateFindsTheRecord` (a repository's folder-recipe launch is refused before `update` and goes through after it, D3308).
- Command: `go test ./internal/cli/ -run 'TestUpdateLiftsEveryKey|TestUpdateLiftKeepsEveryRunWhereItWas|TestUpdateLiftNeverOverwritesALaterProjectValue|TestUpdateLiftRefusedPublishWritesNoRecord|TestUpdateLeavesARunInFlightRunning|TestLaunchAfterUpdateFindsTheRecord' -count=1 && go test ./internal/e2e/ -run 'TestUpdate' -count=1`
- Red step: `TestUpdateLiftsEveryKey` fails while the stubs are registered.
- Depends on: M1, M2, MC1, AB3, RG2, PR4, RC4, RT3 (the sides it wires); PR1 (its read of M2's record, D3308). Release gate.
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
  every lifted key yields a resolved config with none of their values (acceptance 3), and so do the
  four `config.AgentOf` readers — `craft ls`'s layers (`craft.go`), `validateCraftLayers`,
  `budget_stops.go`'s `UnpricedPins` and `run.go`'s `agentAtRef` (F25); no lifted key
  is a `Retired` row, and `RetiredKeys` is still exactly MCR's list (R1); the writers refuse (S4,
  RG3); the launch names and gates (S5).
- Files: `internal/cli/consumers_test.go`, `internal/cli/testdata/lifted/fugaro.yaml` (new),
  `internal/runner/lifted_readers_test.go` (new), `internal/config/lifted_rows_test.go` (new).
- Interfaces: Consumes C1, S0, S1, S2, S4, S5, RG3.
- Tests: `TestEveryReaderDropsLiftedKeys`, `TestLiftedAndRetiredListsAreApart`.
- Command: `go test ./internal/cli/ -run 'TestEveryConsumerResolvesTheSameBytes|TestEveryReaderDropsLiftedKeys' -count=1 && go test ./internal/runner/ -run 'TestEveryReaderDropsLiftedKeys' -count=1 && go test ./internal/config/ -run 'TestLiftedAndRetiredListsAreApart|TestEveryRetiredKeyIsNamedWhereItIsWritten' -count=1`
- Red step: `TestEveryReaderDropsLiftedKeys` fails for any reader that resolves the file outside `config.Resolve`.
- Depends on: S0, S1, S2, S4, S5, RG3 (the sides it wires).
- Commit: `repo-override-rulings I1: every reader of a repository's file drops every lifted key`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### D3: the in-flight design document follows the new stage and the bootstrap refusal
- Module: `migrate`, `project-recipes`.
- Acceptance: `docs/design/update-in-flight.md` says, step by step, what M2's stage writes and that a
  run in flight keeps its task, layer and recipe; that a lowered repository cap is met at the next
  lease and named; and that D3304's bootstrap refusal meets no run already bootstrapped (U1 and
  Check 913 named for the retried execution); the Docs tests pass.
- Files: `docs/design/update-in-flight.md`.
- Interfaces: none.
- Tests: the Docs tests.
- Command: `go test ./internal/cli/ -run Docs -count=1`
- Red step: none (documents).
- Depends on: M2, PR3 (kept, behaviour: it states what they do). Release gate.
- Commit: `repo-override-rulings D3: update in flight, with the L8 stage`
- Reviewer tier: standard.

### DK1: agent.compression's repository row goes; RepoModelGradesSet goes
- Module: `dead-keys` (D3299).
- Acceptance: `model_grades.strong.model` and `model_grades.strongest.model` in a repository's file
  are dropped and named on every ref (C1's list, through S1), and the `.access` rows stay MCR's
  (R11); `agent.compression`'s row loses `InRepo` (it is refused already, F16);
  `Config.RepoModelGradesSet` and its setter go.
- Files: `internal/config/scope.go`, `internal/config/grades.go`, `internal/config/resolve.go`,
  `internal/config/config.go`, `internal/config/dead_keys_test.go` (new).
- Interfaces: none new.
- Tests: `TestRepoModelGradesModelDropped`, `TestModelGradesAccessStaysMCRs`, `TestScopeTableMatchesDocs`.
- Command: `go test ./internal/config/ -run 'TestRepoModelGradesModelDropped|TestModelGradesAccessStaysMCRs|TestScopeTableMatchesDocs' -count=1`
- Red step: `TestRepoModelGradesModelDropped` fails: nothing names the key.
- Depends on: S0 (kept: `scope.go`); RT1 (kept: `resolve.go`). Release gate.
- Commit: `repo-override-rulings DK1: model_grades' model and agent.compression leave a repository's file`
- Reviewer tier: standard.

### DK2: the grades' skew gates read the layer alone; the default branch refuses the model grades
- Module: `dead-keys` (D3299).
- Acceptance: `refuseModelGradesSkew` (defined in `recipes_skew.go`, called from `init.go`) and the
  other `recipes_skew.go` readers of `RepoModelGradesSet` read the layer alone; `fugaro validate` and
  a default-branch launch refuse `model_grades.*.model` in the file with the fix, in the sentence MCR
  X2 uses for `model_grades.*.access`, while an older ref only names it (R2, R11).
- Files: `internal/cli/recipes_skew.go`, `internal/cli/init.go`, `internal/cli/validate.go`,
  `internal/cli/dead_keys_skew_test.go` (new).
- Interfaces: none new.
- Tests: `TestModelGradesSkewReadsTheLayer`, `TestDefaultBranchRefusesModelGrades`.
- Command: `go test ./internal/cli/ -run 'TestModelGradesSkew|TestDefaultBranchRefusesModelGrades' -count=1`
- Red step: `TestDefaultBranchRefusesModelGrades` fails: the launch goes through.
- Depends on: DK1 (kept, behaviour); S5 (kept: `recipes_skew.go`); S2 (kept: `validate.go`). Release gate.
- Commit: `repo-override-rulings DK2: the grades' skew gates read the layer alone`
- Reviewer tier: standard.

### DK3: tightenGrades loses its repository half
- Module: `dead-keys` (D3299).
- Acceptance: `tightenGrades` (`internal/policy/grades.go`, called from `merge.go`'s `Merge`) no
  longer reads a repository's grades; `go test ./internal/policy/` passes.
- Files: `internal/policy/grades.go`, `internal/policy/merge.go`, `internal/policy/grades_repo_test.go` (new).
- Interfaces: none new.
- Tests: `TestTightenGradesIgnoresTheRepository`, `go test ./internal/policy/`.
- Command: `go test ./internal/policy/ -count=1`
- Red step: `TestTightenGradesIgnoresTheRepository` fails: a repository grade is recorded.
- Depends on: DK1 (kept, behaviour: the repository's grades no longer reach a layer). Release gate.
- Commit: `repo-override-rulings DK3: tightenGrades loses its repository half`
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
- Acceptance: `check-910-lift-snapshot.sh <project>` records, read-only and with the **pre-L8**
  binary the owner has installed before the upgrade, every listed repository's raw default-branch
  values of every lifted key (read with `git show origin/<default>:fugaro.yaml` and
  `.fugaro/recipes/`, never through a resolver), its resolved model per role, rigor level and areas,
  A/B rate and recipe sums (`fugaro settings … --repo … --json`, `fugaro recipes ls --json`), and,
  with the release-A binary, `fugaro update --plan-only`'s lines; Check 911 runs with the release-A
  binary after the update, so 910 against 911 compares the values before the drop with the values
  after the migration; `check-911-lift-compare.sh <project> <snapshot>` compares the same read after release A's
  update: PASS where every value matches or the update named the change, FAIL otherwise; each has a
  hermetic test against fakes; stdin closed, no token on an argv, nothing written.
- Files: `scripts/live-checks/check-910-lift-snapshot.sh` (new), `…_test.sh` (new),
  `scripts/live-checks/check-911-lift-compare.sh` (new), `…_test.sh` (new).
- Interfaces: none.
- Tests: the two `_test.sh` files through `go test ./scripts`.
- Command: `go test ./scripts/ -run 'TestLiveCheckScripts' -count=1`
- Red step: the tests fail before the scripts exist.
- Depends on: M2 (kept, behaviour: Check 910 reads `update --plan-only`'s L8 lines, which M2 defines).
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

### CK3: Check 913 as a script, and registered
- Module: `project-recipes`.
- Acceptance: `check-913-retry-image.sh <project>` lists, read-only, every running execution of the
  project's jobs, the image digest it runs and its job's current image digest: PASS when every
  execution keeps its own image after an update (so D3304's refusal meets no retried execution, U1
  settled), FAIL naming each execution that took the job's new image; a hermetic test drives it
  against fakes; stdin closed, no token on an argv; registered beside 910–912.
- Files: `scripts/live-checks/check-913-retry-image.sh` (new), `…_test.sh` (new),
  `scripts/live_checks_test.go`, `docs/gcp-live-checklist.md`, `scripts/live-checks/README.md`.
- Interfaces: none.
- Tests: `go test ./scripts`, the Docs tests.
- Command: `go test ./scripts/ -count=1 && go test ./internal/cli/ -run Docs -count=1`
- Red step: the registration test fails before the row exists.
- Depends on: CK2 (kept: three files of the registration).
- Commit: `repo-override-rulings CK3: Check 913, as a script`
- Reviewer tier: standard.
- Owner-run: the owner runs `bash scripts/live-checks/check-913-retry-image.sh fugaro` right after
  release A's `fugaro update`, with a run in flight. No agent runs it.

### XB4: release B: the launch gate for the refusal
- Module: `lift-keys`. Gate: release B, fenced (R18, D3298).
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
- Module: `lift-keys`. Gate: release B (R18).
- Acceptance: the lifted rows lose `InRepo` (none is marked `Retired`, so "`Retired` is a subset of
  `In`" holds throughout, R1); resolving a default branch's file that writes one, or an empty
  `rigor:` block, is a problem with the fix (C1's sentence); resolving an older ref's file (a
  follow-up's base, a non-default `--ref`) drops it and names it (R2); every caller outside XB2
  takes the naming mode unless it knows it reads the default branch: `validate` refuses only when
  the checkout is on the default branch, and names on a feature branch; `doctor`, `config show`,
  `settings` and `craft ls` name; the launch refuses (XB2); `docs/project-layer.md` prints the table.
- Files: `internal/config/scope.go`, `internal/config/lifted_drop.go`, `internal/config/resolve.go`,
  `internal/config/lifted_refuse_test.go` (new), `docs/project-layer.md`.
- Tests: `TestLiftedKeyRefusedOnTheDefaultBranch`, `TestLiftedKeyIgnoredOnAnOlderRef`, `TestEmptyRigorBlockRefusedOnTheDefaultBranch`, `TestScopeTableMatchesDocs`.
- Command: `go test ./internal/config/ -run 'TestLiftedKey|TestEmptyRigorBlock|TestScopeTableMatchesDocs' -count=1 && go test ./internal/cli/ -run Docs -count=1`
- Red step: `TestLiftedKeyRefusedOnTheDefaultBranch` fails: the key is dropped.
- Depends on: XB4 (kept, behaviour); DK1 (kept: two files); D1 (kept: `project-layer.md`).
- Commit: `repo-override-rulings XB1: release B refuses a lifted key on the default branch`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### XB2: release B: the runner's older ref, the launch's refusal and the recipes folder refused
- Module: `lift-keys`, `project-recipes`. Gate: release B, fenced (R18).
- Acceptance: the runner resolves a follow-up's, a review's and a `--ref` run's file in the
  older-ref mode, and the default branch in the refusing mode; a default-branch launch whose file
  writes a lifted key, or whose default branch holds `.fugaro/recipes/`, is refused with the fix
  and PR5's lines — the removal only where every copy is stored, `fugaro recipes publish <file>`
  first otherwise (acceptance 4, 7).
- Files: `internal/runner/followup.go`, `internal/runner/runner.go`, `internal/cli/run.go`,
  `internal/cli/recipes_cleanup.go`, `internal/cli/release_b_refusal_test.go` (new).
- Tests: `TestReleaseBRefusesAtLaunch`, `TestReleaseBFollowUpIgnoresAndNames`.
- Command: `go test ./internal/cli/ -run 'TestReleaseBRefusesAtLaunch' -count=1 && go test ./internal/runner/ -run 'TestReleaseBFollowUpIgnoresAndNames' -count=1`
- Red step: `TestReleaseBRefusesAtLaunch/recipes-folder` fails: the launch goes through.
- Depends on: XB1 (kept, behaviour); PR2 (kept: `run.go`).
- Commit: `repo-override-rulings XB2: release B refuses at the launch`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

### XB3: release B: the schemas, the settings registry and the empty rigor block's opt-in
- Module: `lift-keys`, `rigor-repos`. Gate: release B (R18).
- Acceptance: `schemas/fugaro.schema.json` no longer accepts a lifted key; the settings registry and
  `schemas/settings.schema.json` give each lifted key the project scope only; `rigor.Config.RepoBlock`
  no longer opts a repository in (`Configured`), since RG2 moved every empty block to `rigor.repos`;
  `TestRegistryCoversEveryConfigField` passes.
- Files: `schemas/fugaro.schema.json`, `internal/settings/registry.go`, `schemas/settings.schema.json`,
  `internal/rigor/resolve.go`, `internal/settings/lift_rows_test.go` (new).
- Tests: `TestRegistryCoversEveryConfigField`, `TestLiftedRowsAreProjectOnly`, `TestEmptyRigorBlockOptsInNoLonger`.
- Command: `go test ./internal/settings/ -run 'TestRegistryCoversEveryConfigField|TestLiftedRowsAreProjectOnly' -count=1 && go test ./internal/rigor/ -run 'TestEmptyRigorBlockOptsInNoLonger' -count=1`
- Red step: `TestLiftedRowsAreProjectOnly` fails on the repository scope.
- Depends on: XB1 (kept, behaviour).
- Commit: `repo-override-rulings XB3: the schemas and the empty rigor block`
- Reviewer tier: standard.

### XB6: release B: validate refuses a lifted key only on the default branch
- Module: `lift-keys`. Gate: release B, fenced (R18).
- Acceptance: `fugaro validate` refuses a lifted key, an empty `rigor:` block or a
  `.fugaro/recipes/` folder only when the checkout it reads is on the default branch, with the fix
  and PR5's lines, and names them on any other branch, as XB1 says; its read of
  `.fugaro/recipes/default.yaml` goes (R22).
- Files: `internal/cli/validate.go`, `internal/cli/validate_release_b_test.go` (new).
- Tests: `TestValidateRefusesOnTheDefaultBranch`, `TestValidateNamesOnAFeatureBranch`.
- Command: `go test ./internal/cli/ -run 'TestValidateRefusesOnTheDefaultBranch|TestValidateNamesOnAFeatureBranch' -count=1`
- Red step: `TestValidateRefusesOnTheDefaultBranch` fails: validate only names.
- Depends on: XB1 (kept, behaviour); DK2 (kept: one file, `validate.go`).
- Commit: `repo-override-rulings XB6: validate refuses only on the default branch`
- Reviewer tier: standard.

### XB5: release B: update names a lifted key still written; the planners retire
- Module: `migrate`. Gate: release B, fenced (R18).
- Acceptance: `fugaro update` names every listed repository whose default branch still writes a
  lifted key or holds `.fugaro/recipes/`, with the fix and PR5's lines (the removal only where every
  copy is stored), and converges everything else;
  it asks nothing (spec §4's "refused at update", read under the HARD RULE as named, never as a
  stop of the whole update); the L8 planners and their stage retire (D3305): nothing is migrated in
  release B.
- Files: `internal/cli/update_lift.go`, `internal/cli/update_lift_refuse_test.go` (new).
- Tests: `TestUpdateNamesALiftedKeyStillWritten`, `TestReleaseBMigratesNothing`.
- Command: `go test ./internal/cli/ -run 'TestUpdateNamesALiftedKeyStillWritten|TestReleaseBMigratesNothing' -count=1`
- Red step: the test fails: nothing is named.
- Depends on: XB1 (kept, behaviour); I2 (kept: `update_lift.go`, through M2).
- Commit: `repo-override-rulings XB5: update names a lifted key still written`
- Reviewer tier: opus, with a mandatory independent review (`fugaro review --pr N`).

## Parallel execution

### Levels

| Level | Tasks |
| --- | --- |
| 0 | A1, C1, C2, PR0, S0, AB1, RG1, PR3, M1, RC1, RC2 |
| 1 | S1, PR6, PR5, S4, S5, AB2, AB3, MC1, RG2, PR4, M2, RC3, RC4 |
| 2 | RT1, PR1, RG3, PR2, D3, CK1 |
| 3 | RT2, S2, CK2, RT3, DK1, PR7, D1, D2 |
| 4 | CK3, DK3, DK2, I1, I2 |
| 5 | I3, and the contract changes R-lift-phase and R-planner-change if raised |
| 6 | XB4 (release B, fenced) |
| 7 | XB1 |
| 8 | XB6, XB2, XB3, XB5 |

**Critical path** (9 tasks, as the graph read reports it): C1 → S1 → RT1 → RT3 → I2 → I3 → XB4 →
XB1 → XB2. Release A ends at level 5. What must be sequential and why: `internal/config/resolve.go`
is written by S1, RT1, DK1 and XB1 in that order; `recipes_skew.go` by S5, DK2 and XB4; a contract
change comes after every consumer of its contract has landed, never beside the contract; release B
waits for release A by the owner's ruling (D3298).

### Shared files

No level holds two writers of one file (checked against the graph's footprints):

| File | Writers, by level |
| --- | --- |
| `internal/config/resolve.go` | S1 (1), RT1 (2), DK1 (3), XB1 (7) |
| `internal/config/scope.go` | S0 (0), DK1 (3), XB1 (7) |
| `internal/config/lifted_drop.go` | S1 (1), XB1 (7) |
| `docs/project-layer.md` | S0 (0), D1 (3), XB1 (7) |
| `internal/cli/recipes_skew.go` | S5 (1), DK2 (4), XB4 (6) |
| `internal/cli/run.go` | PR2 (2), XB2 (8) |
| `internal/cli/recipes_cleanup.go` | PR5 (1), XB2 (8) |
| `internal/cli/update_lift.go` | M2 (1), XB5 (8) |
| `internal/lift/stubs.go` | C2 (0, creates), I2 (4, deletes) |
| `internal/cli/validate.go` | S2 (3), DK2 (4), XB6 (8) |
| `internal/cli/budget_preview.go` | PR1 (2), RT2 (3), ordered by an edge |
| `internal/config/config.go` | S1 (1), DK1 (3) |
| C1's files (`lifted.go`, `contract-lift-keys.md`) | C1 (0), R-lift-phase (5, if raised) |
| C2's files (`lift.go`, `contract-migrate.md`) | C2 (0), R-planner-change (5, if raised) |
| `internal/rigor/resolve.go` | RG1 (0), XB3 (8) |
| `schemas/settings.schema.json` | AB1 (0), XB3 (8) |
| `scripts/live_checks_test.go`, `docs/gcp-live-checklist.md`, `scripts/live-checks/README.md` | CK2 (3), CK3 (4) |
| `schemas/fugaro.schema.json` | S1 (1), XB3 (8) |
| `website/content/cli-reference.md` | S4 (1), RG3 (2) |

Across plans (reconciliation O2): model-credentials-routing's MG5 (`scope.go`, both schemas), MG6
(`models_use.go`, `run.go`, the CLI reference) and X2 (`config.go`, `layer.go`, `validate.go`, both
schemas) are not ordered against this plan; the second to merge rebases.

## The dependencies broken and kept

The decoupling switch is on for `dimipaun/fugaro` (`fugaro settings planning.decoupling --json
--repo dimipaun/fugaro`: `true`, the default), and the repository's mode is `startup`
(`rigor.default`), so the pass decoupled wherever only an interface crossed an edge.

**Broken (17 edges, two contracts):**

- C1 breaks S1, S2, S4 and S5's wait on S0: each needs only which keys are lifted, which the
  contract lists, not the scope rows S0 marks. Integration: I1.
- C2 breaks MC1, AB3, RG2, PR4, RC4 and RT3's wait on M1 (a planner needs the shape of a repository's
  files, not the provider read that fills it) and M2's wait on each of those six (the stage needs a
  planner's signature and its change type, not what it computes), and PR1's wait on M2 (the
  launcher needs only the migration record's path, not the stage that writes it, D3308).
  Integration: I2.

**Kept (55 edges)**, each with its reason in the graph; the families: one file (`resolve.go`,
`scope.go`, `recipes_skew.go`, `run.go`, `update_lift.go`, `project-layer.md`, the CLI reference);
behaviour (RT2 on RT1's mark, S2 on RT1's record, DK2 on DK1, the documents and skills on what
they describe, every release-B task on release A); and four small functions a contract would equal
(PR0's copy name and lookup, AB1's field, RC1's ceiling, PR5's cleanup lines).

**Considered and not broken:** a contract for B16's copy name (PR0 is the function itself, so it
stays a task); a contract for `ab.repos` (the field is the whole of AB1).

**The gain**, as the read reports it: 17 broken, 55 kept, 6 added; before 10 tasks, weight 22;
after 9 tasks, weight 19; improves. The critical path is release B's chain; the contracts widen
level 1 (thirteen tasks run at once) rather than shorten it.

**Contract changes anticipated:**

| Change | Contract | Raised by | When | Reaches |
| --- | --- | --- | --- | --- |
| R-lift-phase | C1 | XB1 | Release B's refusal needs a per-key phase in the list rather than the scope rows' `InRepo` bit | S1, S2, S4, S5 |
| R-planner-change | C2 | PR4 | A store write or a database write needs more than `Change` carries | MC1, AB3, RG2, RC4, RT3, M2 |

## The estimate

`fugaro plan graph --plan docs/repo-override-rulings/implementation-plan.md` printed:

`ready in 392–1696 min, no history yet for 21 tasks (52 tasks; tiers for 31, seeds for 21; from the cached history of 2026-11-22 07:25 UTC)`

It is informational and reserves no budget: it is what the tasks' tiers and this machine's cached
history predict, and it moves as runs finish and as the graph changes. The 52 count includes the
two contract changes, which run only if raised. **Proposed runs:** 50 (the two contracts, 46 tasks and the two integrations),
plus up to two contract changes, plus one independent review (`fugaro review --pr N`) for each
opus-tier task and for C1 and C2 before their consumers launch.

## Checks the plan takes

Block **910–919**; this plan uses 910–913.

| Check | What it measures | Who runs it, when |
| --- | --- | --- |
| 910 | Every enrolled repository's resolved models, rigor, A/B rate and recipe sums, and `update --plan-only`'s L8 lines, before release A | The owner, before release A's `fugaro update` (CK1) |
| 911 | The same read after release A's update, compared with 910: every value the same or named by the update | The owner, after release A's `fugaro update` (CK1) |
| 912 | Release B refuses a lifted key on the default branch with the fix and names it on an older ref | The owner, after release B is installed (CK2) |
| 913 | Whether a retried execution of a run launched before `update` boots on the rebuilt image (U1): each running execution's image against its job's, read-only | The owner, during release A's `fugaro update` with a run in flight (CK3) |

## After-deploy steps

None beyond `fugaro update`. Release A's notes (`docs/releases/`, written with the release) say
under `### For operators`: run `fugaro update`; it prints first one line per repository whose model,
rigor (raised or lowered), A/B rate, recipe name or cap changes, each recipe it renamed, and each
repository whose runs now stop at the default `agent.max_budget_usd` of 25 because its file wrote
more and the project sets none (R20); every member upgrades the CLI (R9); `doctor` offers each
repository's `git rm -r .fugaro/recipes` as a diff; **a repository `update` cannot list (admitted only
by a pattern such as `*` or `github/acme/*`) or cannot read is not migrated, and its lifted values
are dropped — an `ab.rate: 0` opt-out, a raised rigor level and its models — and a recipe name it
uses resolves to whatever a listed repository published flat under that name, or to the catalog's;
list it by name and run `fugaro update` again** (D3306, O6; `update` prints each pattern it could not expand);
and a task from a pre-L8 launcher that embeds no recipe, on a ref whose `.fugaro/recipes/` is gone,
runs the catalog recipe of that name (D3307). Release B's say: run `fugaro update`; a repository's
file that still writes a lifted key is refused on its default branch, with the fix. Both say the
tagged commit was checked against the fence above. M2's and XB5's pull requests carry those lines as
release-actions entries.
