# Contract-first task decoupling: implementation plan

Builds `spec.md` as amended by `reconciliation.md` (decisions D1810-D1823, findings F1-F19). Written
against `origin/main` at `a7eb6ca28`, 2026-11-08.

## Status

**Plan only. Nothing is implemented. No task is approved:** each task below needs the owner's explicit
go; answering a decision is not approving a task. The owner's answers that shaped this plan, all
2026-11-08: extend `parallelism` (D1810), graph schema 3 (D1811), amendments as plan nodes with follow-up
runs (D1812), rigor mode plus one on/off setting (D1813), change requests recorded in the graph document
(D1814), stubs in whole files the contract names (D1815), weighted gain and "break only if shorter"
(D1816). Every one is the recommendation.

## Conventions every task follows

- `CLAUDE.md`: pre-1.0, reshape directly; small diffs; no new dependency; comments say why, never what;
  no TODO or section-header comments; search before writing a helper.
- `.claude/rules/console.md` for the one task that edits `console/docs/json-contract.md` (CD4).
- **Hermetic tests**: no network, no home directory, no real cloud; `t.TempDir()`, `internal/testutil`.
  No secret-shaped literal in a test, doc or fixture.
- **Docs are tested**: a task that changes a doc runs `go test ./internal/cli/ -run Docs`.
- **Plugin tests tie skill to CLI** (`plugin/`): a skill sentence may name a command or flag only once
  the CLI has it (F16), so CLI tasks precede the skill task that teaches them.
- **A field never changes meaning** (CLAUDE.md): schema 3 only *adds* fields (`kind`, `size`, `stubs`,
  `decoupling`, `contract_changes`) and the read's output only adds `gain` and `stubs_present`; no
  existing field is retyped.
- **`fugaro update` is the whole upgrade** (CLAUDE.md hard rule): the new setting's skew gate lands in
  `update` in the same release (CD7), with a test that an installation at the previous version converges.
- **No live cloud action by an agent**: check 480 is owner-run. No agent runs it.
- Files edited stay under the size limits: `plugin/skills_lint_test.go` allows 400 lines and 24 KB for a
  `SKILL.md`, 300 lines and 16 KB for a reference file.
- Never edit `.github/workflows/`. None of these tasks needs to.

## Global constraints and review focus

| Constraint or failure mode | Pinned by |
| --- | --- |
| A graph the build does not understand is refused, never drawn (a future version, an unknown field, a bad reference, a cycle) | CD2 `TestGraphV3RefusesWhatItDoesNotKnow` |
| Stub paths are untrusted plan text: literal repo-relative only, no `..`, no absolute path, no symlink leaving the checkout, never globs (F9) | CD2 `TestStubPathsAreLiteralRepoRelative`, CD3 `TestStubsPresentDoesNotFollowSymlinksOut` |
| Breaking an edge must not lengthen the weighted path; the report says so honestly (F5) | CD3 `TestGainReportsALossWhenTheBreakLengthensThePath` |
| The CLI computes before/after itself from the recorded broken edges; the agent never supplies the numbers | CD3 `TestGainIsComputedNotRead` |
| One integration node per contract; every broken edge has an integration node depending on both endpoints (D1817) | CD2 `TestDecouplingNeedsIntegrationOverBothEndpoints` |
| A contract's files appear in no other task's footprint except the contract and its change nodes (F10) | CD2 `TestContractFilesBelongToTheContractAndItsChanges` |
| The skill never parses the graph; the CLI does (D1821) | CD1/CD5 `TestParallelismSkillNamesTheCLIForTheGraph` |
| Contract tasks launch with `--mode` at least the highest consumer mode; raise-only is the resolver's own rule (D1819) | CD1 `TestParallelismSkillContractTaskRigor` |
| Hotfix skips the pass; Critical needs fully specified contracts and a named end-to-end test (D1818) | CD1 and CD5 skill-text tests, CD5 `TestSkillRigorTableMatchesInternalRigor` |
| The skill's text carries no repository-specific or organisation-specific wording (spec: generic) | CD1/CD5 `TestParallelismSkillIsGeneric` |
| A launched dependent learns of an amendment through a follow-up run that names the contract file and never pastes text (D1812; `followup.md` ground rules) | CD1 `TestFollowupInstructionsNameTheContractFile`, check 480 |
| A new config key does not break an older runner or an older installation (F7) | CD7 `TestUpdateConvergesFromPreviousVersionWithPlanningKey` |

## Slices

- **Slice A, the skill on schema 2 (CD1).** Ships alone and is useful: the agent follows the procedure
  with contract and integration tasks as ordinary nodes (D1822).
- **Slice B, the graph (CD2, CD3, CD4).** Schema 3, the computed gain, the CLI read and the console contract.
- **Slice C, the setting (CD6, CD7).** `planning.decoupling` and the update gate.
- **Slice D, the skill teaches the rest (CD5).** Schema 3, the setting, the check script.

CD2 is itself the contract for CD3 and CD4: it fixes the document types and their validation, and CD3 and
CD4 build against it in the order the edges below give.

## Tasks

### CD1: the decoupling pass as skill text, on schema 2

- **Files:** `plugin/skills/parallelism/SKILL.md` (edit: one section "Break dependencies with a contract
  first", a pointer to the reference); `plugin/skills/parallelism/reference/decoupling.md` (new: the
  per-edge test, the contract-task template, the integration task, the change-request flow, the rigor
  table, the planner output); `plugin/parallelism_decoupling_test.go` (new; a new file so CD1 and CD2
  never both write `plugin/parallelism_skill_test.go`).
- **Interfaces:** consumes `fugaro plan graph` as it is today (schema 2: nodes, edges with reasons).
  Produces text only. Contract and integration tasks are ordinary nodes named `C-...` and `I-...`, each
  edge carries its reason; the broken-versus-kept list is written in the plan document.
- **Schema, rules, config change:** none.
- **Tests:** `TestParallelismSkillHasDecouplingSection`, `TestParallelismSkillContractTaskRigor`,
  `TestFollowupInstructionsNameTheContractFile`, `TestParallelismSkillNamesTheCLIForTheGraph`,
  `TestParallelismSkillIsGeneric`, and the existing lint tests.
- **Command:** `go test ./plugin/ -run 'Parallelism|Lint|SkillSet' -count=1 && go test ./ee/plugincheck/ -count=1`.
- **Depends on:** nothing.
- **Commit:** `plugin: parallelism teaches breaking a dependency with a contract task`.
- **Red step:** `TestParallelismSkillHasDecouplingSection` fails because the section does not exist.
- **Reviewer:** `opus + mandatory independent review` (a plugin skill is agent-facing instruction text).
- **Content the text must hold:** the per-edge question ("does B need A's interface or A's behaviour?");
  keep edges that need real behaviour or data; the contract task's contents (types, signatures, API and
  schema shapes, events, plausible stubs in files the contract names, a short contract doc); small;
  reviewed at least at the highest rigor of its consumers (`--mode`); the integration task depends on
  both sides, deletes the stub files and runs end-to-end checks; the change-request flow (D1812: a node,
  an edge for unlaunched dependents, a follow-up run for launched ones that names the contract file as it is on `origin/<base>`, which must be a
  contract node of the merged graph, and never pastes its text; a base that changed `.github/workflows` is
  never merged into a branch with a cloud follow-up run, CLAUDE.md); per-mode behaviour (POC and Startup aggressive, Pro where the contract is
  clear, Critical only fully specified with a named end-to-end test, Hotfix skip); break an edge only
  when the weighted path shrinks (D1816), by the agent's own arithmetic until CD5. A task's resolved rigor mode is read with `fugaro settings rigor.default rigor.areas --json` and its footprint paths matched against the areas by the agent (no command resolves a mode for given paths, F18).

### CD2: graph schema 3, the document and its validation

- **Files:** `console/docs/json-contract.md` (edit: section 6.9 names schema versions 1-3 and every json tag of `Document` and `Node`, because `TestDocsDesktopConsoleContractNamesTheOtherShapesFields` requires it); `plugin/parallelism_skill_test.go` (edit: the example's version is checked as readable, not as equal to `plan.SchemaVersion`); `internal/cli/plan_test.go` (edit: the v2 fixture check, same reason); `internal/plan/document.go` (edit: `SchemaVersion` 3, `Node.Kind`, `Node.Size`,
  `Node.Stubs`, `Document.Decoupling`, `Document.ContractChanges`, the types); `internal/plan/read.go`
  (edit: read versions 1-3, closed-set decode at each, v1/v2 documents carrying a v3 field are refused; the
  two comparisons written against the constant, the stream rule and the version-shape rule in `Normalize`,
  are written against the literal 2: the stream rule refuses a stream on a document `< 2`, and the version-shape rule applies to every document `>= 2`, so a v3 graph's node versions are still shape-checked; the refusal texts print the literal too); `internal/plan/read_test.go`, `internal/plan/analyze_test.go` and `internal/plan/testdata/future.json` (edit: the existing tests that compare to `SchemaVersion` or expect version 3 to be the future, and the future fixture moves to version 99); `internal/cli/docs_desktop_console_test.go` (edit: the shapes list gains the new nested types, so keys such as `raised_by`, `affects`, `added_tasks`, `improves` and `weight` are pinned to the contract page);
  `internal/plan/decoupling_test.go` (new).
- **Interfaces:** produces the v3 document types consumed by CD3 and CD4. Every new key is `omitempty` (`kind`, `size`, `stubs`, `decoupling`, `contract_changes`, and in CD3 `gain` and `stubs_present`) so a v1 or v2 graph prints byte-for-byte as before; `analyze_test.go` pins an exact string for a v1 graph. `kind` is one of `task`
  (default when absent), `contract`, `integration`, `contract_change`; `size` a number greater than 0
  (absent means 1); `stubs` only on `contract` nodes, literal repo-relative paths, and each stub must also appear *literally* in that contract node's own footprint paths (a glob such as `**` never admits a stub), so a stub is always a file the planner named for the contract by name. The contract's files for the F10 check are likewise its literal footprint paths; the validator refuses a `contract` node whose footprint has no literal path. `decoupling` entries:
  `{contract, was: {task, needs}, integration, reason}`. `contract_changes` entries:
  `{change, contract, raised_by, reason, affects}`.
- **Schema, rules, config change:** the graph artifact schema version 2 to 3, additive; the version
  check, the refusal text and `FirstSchemaVersion` stay.
- **Tests:** `TestGraphV3RoundTrips`, `TestGraphV3RefusesWhatItDoesNotKnow`,
  `TestV2DocumentWithAV3FieldIsRefused`, `TestDecouplingNeedsIntegrationOverBothEndpoints`,
  `TestDecouplingWasEdgeMustNotStillBeAnEdge`, `TestContractChangeReferencesMustExistAndMatchKinds`,
  `TestStubPathsAreLiteralRepoRelative`, `TestContractFilesBelongToTheContractAndItsChanges` (contract files are the contract node's literal footprint paths minus its stubs, checked with `Footprint.Matches`; the integration node is exempt for the stubs it deletes), `TestStubMustAppearLiterallyInTheContractFootprint`, `TestContractNodeNeedsALiteralFootprintPath`, `TestGraphV3SpellsTargetVersions`, `TestV2GraphNamingAStreamStillReads`,
  `TestGraphV3CycleStillRefused`, the existing `internal/plan` tests updated as listed above (none loses a check).
- **Command:** `go test ./internal/plan/ ./plugin/ -count=1 && go test ./internal/cli/ -run 'PlanGraph|Docs' -count=1`.
- **Depends on:** nothing.
- **Commit:** `plan: graph schema 3 carries contract tasks, the broken edges and the change requests`.
- **Red step:** `TestGraphV3RoundTrips` fails: the decoder refuses `kind` as an unknown field.
- **Reviewer:** `opus + mandatory independent review` (parses untrusted plan text; path rules).

### CD3: the computed gain and the stub read

- **Files:** `internal/plan/decouple.go` (new: the before graph, both path measures, the added task
  count, `stubs_present`); `internal/plan/analyze.go` (edit: additive `Graph.Gain`, `Graph.Decoupling`, `Graph.ContractChanges` (`Graph` copies the document's fields one by one, so `contract_changes` would otherwise be dropped, D1814),
  per-contract `stubs_present`); `console/docs/json-contract.md` and `internal/cli/docs_desktop_console_test.go` (edit: the `Graph`, `NodeState` and gain tags and the gain, before and after types, same docs pin as CD2); `internal/plan/decouple_test.go` (new).
- **Interfaces:** consumes CD2's types. Produces `gain`: `{broken, kept, added_tasks, before: {tasks,
  weight}, after: {tasks, weight}, improves}`; the *before* graph is the recorded `was` edges restored
  with every `contract`, `integration` and `contract_change` node removed, and every edge that ran through a removed integration node rewritten onto that node's endpoints (a task that waited on the integration node waited on both sides). `stubs_present` lists the
  contract's stub paths that still exist under the checkout root, which is the git top level of the graph artifact (a `--plan` outside the working directory's checkout is refused when the graph has stubs, because stubs would be read in the wrong tree), resolved through `os.Root` (or, if the pinned Go version lacks it, an `Lstat` of every path component) so a symlinked directory cannot lead outside the checkout.
- **Schema, rules, config change:** none beyond additive output fields.
- **Tests:** `TestGainIsComputedNotRead`, `TestGainReportsALossWhenTheBreakLengthensThePath`,
  `TestGainWeightedShrinksWhenSidesAreLarge`, `TestGainCountsAddedTasks`,
  `TestStubsPresentListsOnlyExistingFiles`, `TestStubsPresentDoesNotFollowSymlinksOut`,
  `TestGainRewritesEdgesOnTheIntegrationNode`, `TestGraphCarriesContractChanges`, `TestStubsPresentDoesNotFollowASymlinkedDirectory`, `TestPlanStatusReadsAV3Graph` (`internal/plan/state.go` reads v3 through `Parse`, unchanged), `TestGraphWithoutDecouplingHasNoGainNoise` (a v2 graph reads exactly as before).
- **Command:** `go test ./internal/plan/ -count=1 && go test ./internal/cli/ -run Docs -count=1`.
- **Depends on:** CD2.
- **Commit:** `plan: the read computes the decoupling gain and which stubs remain`.
- **Red step:** `TestGainReportsALossWhenTheBreakLengthensThePath` fails: no `gain` field exists.
- **Reviewer:** `opus + mandatory independent review` (it stats paths taken from plan text).

### CD4: `fugaro plan graph` prints it, and the console contract says so

- **Files:** `internal/cli/plan.go` (edit: a "DECOUPLING" block in the table and the JSON passthrough);
  `internal/cli/plan_test.go` (edit); `website/content/cli-reference.md` (edit, only if the `plan graph` help text changes; `docs_website_cliref_test.go` pins it). The console contract page is CD2's and CD3's, because its pin test fails the moment the Go types change.
- **Interfaces:** consumes CD3's output. Produces the CLI's stdout and `--json` shape.
- **Schema, rules, config change:** none.
- **Tests:** `TestPlanGraphPrintsDecouplingBlock`, `TestPlanGraphJSONCarriesGain`,
  `TestPlanGraphV2OutputIsUnchanged`, and `go test ./internal/cli/ -run Docs`.
- **Command:** `go test ./internal/cli/ -run 'PlanGraph|Docs' -count=1`.
- **Depends on:** CD3.
- **Commit:** `cli: plan graph prints the broken edges, the gain and the stubs that remain`.
- **Red step:** `TestPlanGraphPrintsDecouplingBlock` fails: the table has no such block.
- **Reviewer:** standard.

### CD6: the setting `planning.decoupling`

- **Files:** `internal/config/defaults.go` (edit: `applyDefaults` sets `planning.decoupling` on); `internal/settings/defaults_test.go` (edit: a reader for the key, or `TestRegistryDefaultsMatchApplyDefaults` fails); `internal/config/config.go` (edit: a `Planning` block with `Decoupling` on/off in the repo
  file); `internal/config/layer.go` (edit: the project layer's `LayerDefaults` and its `Sets*()` answer; the layer is decoded with `KnownFields(true)`); `docs/project-layer.md` (edit: the scope table, `TestScopeTableMatchesDocs`); `internal/cli/docs_config_management_test.go`, `docs/config-management/README.md` and `docs/config-management/reconciliation.md` (edit: the registry row count `cfmRegistryRows` and the counts the docs state); `internal/config/scope.go` (edit: the key's scopes);
  `internal/settings/registry.go` (edit: the row, default `on`, merge Replace); `internal/cli/validate.go`
  (edit if the key's validation lives there); `schemas/fugaro.schema.json`, `schemas/project-layer.schema.json`,
  `schemas/settings.schema.json` (edit); tests beside each.
- **Interfaces:** produces the key that `fugaro settings planning.decoupling --json` prints and the skill
  reads. Narrower layer wins; no per-area and no per-task value (D1813).
- **Schema, rules, config change:** the three JSON Schemas and the two decoders (strict).
- **Tests:** `TestPlanningDecouplingDefaultsOn`, `TestPlanningDecouplingRepoOverridesProject`,
  `TestRegistryDefaultsMatchApplyDefaults` (existing, now covering the row),
  `TestSettingsSchemaCoversPlanningKey`.
- **Command:** `go test ./internal/config/ ./internal/settings/ ./schemas/ -count=1 && go test ./internal/cli/ -run 'Docs|Scope' -count=1`.
- **Depends on:** nothing.
- **Commit:** `config: planning.decoupling turns the decoupling pass off for a project or a repository`.
- **Red step:** `TestPlanningDecouplingDefaultsOn` fails: the registry has no such key.
- **Reviewer:** standard.

### CD7: the skew gate in `fugaro update`

- **Files:** `internal/cli/recipes_skew.go` (edit: `planningDecouplingSince` and its message);
  `internal/cli/init.go` (edit: the `update --repo` gate beside `gateGitAuthor`); `internal/cli/run.go` (edit: the launch gate, as `checkGitAuthorImage` is for `gitAuthorSince`); `docs/release.md` (edit: the "Verify ...Since" bullet and its `Test...SinceIsTheReleaseTheChecklistNames` pin); tests beside them;
  the release notes file of the release in flight under `docs/releases/` (edit: `### For operators`: run
  `fugaro update`, plus what it asks) and its line in `website/data/release-summaries.yaml` (no backticks).
- **Interfaces:** consumes CD6's key.
- **Schema, rules, config change:** none.
- **Tests:** `TestUpdateConvergesFromPreviousVersionWithPlanningKey`,
  `TestRepoWithPlanningKeyIsRefusedByAnOlderRunnerGate`, `go test ./internal/cli/ -run Docs`.
- **Command:** `go test ./internal/cli/ -run 'PlanningKey|Docs' -count=1`.
- **Depends on:** CD6.
- **Commit:** `update: the repository gate knows planning.decoupling`.
- **Red step:** `TestUpdateConvergesFromPreviousVersionWithPlanningKey` fails: the gate does not name the key.
- **Reviewer:** `opus + mandatory independent review` (the update path and a version-skew gate).

### CD5: the skill teaches schema 3, the setting, and the check script

- **Files:** `plugin/skills/parallelism/SKILL.md` and `plugin/skills/parallelism/reference/decoupling.md`
  (edit); `plugin/parallelism_skill_test.go` and `plugin/parallelism_decoupling_test.go` (edit: CD1's pinned sentences about schema-2 naming and the agent's own arithmetic are rewritten); `docs/contract-first-decoupling/check-480.sh` and `check-481.sh` (new, one command each, not run by any agent).
- **Interfaces:** consumes CD4's CLI and CD7's gate. The skill now writes `kind`, `decoupling`, `stubs`,
  `size` and `contract_changes`, tells the agent to read `fugaro settings planning.decoupling --json`
  (off means skip the pass and say so), and to report broken versus kept edges and the gain from
  `fugaro plan graph`, not from its own arithmetic.
- **Schema, rules, config change:** none.
- **Tests:** `TestParallelismSkillGraphExampleIsReadableAtV3` (the example parses with the real
  `plan.Parse`), `TestSkillRigorTableMatchesInternalRigor`, `TestSkillNamesPlanningSetting`,
  the existing `TestParallelismSkillGraphExampleIsReadable`, the lint, and the command-and-flag check in `ee/plugincheck/skills_test.go` (the real F16 check; `plugin/skillcli_test.go` is only a stub in the core).
- **Command:** `go test ./plugin/ ./ee/plugincheck/ -count=1`.
- **Depends on:** CD1, CD4, CD7.
- **Commit:** `plugin: parallelism writes schema 3 graphs and honours planning.decoupling`.
- **Red step:** `TestParallelismSkillGraphExampleIsReadableAtV3` fails: the example is schema 2.
- **Reviewer:** `opus + mandatory independent review`.

## Parallel execution

Edges from the Depends-on lines: CD3 needs CD2; CD4 needs CD3; CD7 needs CD6; CD5 needs CD1, CD4 and CD7.

| Level | Tasks (concurrent) |
| --- | --- |
| 0 | CD1, CD2, CD6 |
| 1 | CD3, CD7 |
| 2 | CD4 |
| 3 | CD5 |

**Critical path:** CD2, CD3, CD4, CD5 (four tasks, four levels). Seven tasks run in four levels; chained as
a sequence they would be seven. The chain is real: CD3 reads CD2's types, CD4 prints CD3's result, and
CD5 may name only what the CLI has (F16).

### Shared files: no level holds two writers of one file

| File | Writers (level) |
| --- | --- |
| `plugin/skills/parallelism/SKILL.md`, `.../reference/decoupling.md` | CD1 (0), CD5 (3) |
| `plugin/parallelism_decoupling_test.go` | CD1 (0), CD5 (3) |
| `plugin/parallelism_skill_test.go` | CD2 (0), CD5 (3) |
| `internal/plan/document.go`, `internal/plan/read.go`, `read_test.go`, `analyze_test.go`, `testdata/future.json` | CD2 (0) |
| `internal/cli/docs_desktop_console_test.go` | CD2 (0), CD3 (1) |
| `console/docs/json-contract.md` | CD2 (0), CD3 (1) |
| `internal/cli/plan_test.go` | CD2 (0), CD4 (2) |
| `internal/plan/analyze.go`, `decouple.go` | CD3 (1) |
| `internal/cli/plan.go`, `website/content/cli-reference.md` | CD4 (2) |
| `internal/config/config.go`, `defaults.go`, `layer.go`, `internal/settings/defaults_test.go`, `scope.go`, `internal/settings/registry.go`, the three schemas, `docs/project-layer.md`, `docs/config-management/*`, `internal/cli/docs_config_management_test.go` | CD6 (0) |
| `internal/cli/recipes_skew.go`, `internal/cli/init.go`, `internal/cli/run.go`, `docs/release.md`, `docs/releases/<release>.md`, `website/data/release-summaries.yaml` | CD7 (1) |
| `docs/contract-first-decoupling/check-480.sh`, `check-481.sh` | CD5 (3) |

Any change to a task's Files re-checks this table.

## Checks (480-489 reserved; 480 and 481 used)

- **Check 480 (OWNER-RUN, live): a launched dependent conforms to an amended contract.** Measures that a
  follow-up run, launched with instructions that name the contract file and ask it to merge the base and
  conform, merges the base and changes its code to the amended contract (U4). The owner types, from a
  checkout of a sandbox repository (never the dev installation named in the owner's notes):
  `bash docs/contract-first-decoupling/check-480.sh`. No agent performs any step of it.
- **Check 481 (OWNER-RUN, optional): the gain is real.** Plans one real interface-only dependency with
  the skill, runs both shapes, and compares the weighted estimate with the wall-clock. The owner types
  `bash docs/contract-first-decoupling/check-481.sh <plan>`, which prints the plan's gain and the two batches' wall-clock; no agent performs any step of it. The weighted figure is only as honest as the `size` the planner wrote; the check is how that is learnt.

Nothing here needs a backfill, a secret or a config change after deploy beyond what `fugaro update` does,
which CD7's release-notes entry states.

## What waits on something external

- CD5 waits on CD7 and so on the release that carries the new CLI; the skill must not teach schema 3
  to a plugin pin whose CLI cannot read it.
- O1 (a guard that a task does not edit a merged contract), O2 (the triage personas) and O3 (whether an
  older CLI should read v3) wait on the owner and have no task.

## Considered and declined

- **A sixth skill** (D1810): changes the pinned five-skill set for no behavioural gain.
- **A daemon that notifies dependents** (D1812): waits on the unmerged orchestrator tick.
- **Marker comments for stubs** (D1815): conflict with the comment rule.
- **A per-task off switch** (D1813): a per-task value may only raise.
