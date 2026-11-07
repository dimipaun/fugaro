# Security classification, reconciled against the code

This file reads [spec.md](spec.md) — the owner's ask of 2026-11-07 — against the CLI, the runner, the
policy stack and the plugin skills **as they are on this tree**, and says for each of the ask's seven
question groups what already exists, what would have to be built, and what a design must decide. It
takes no task approval: the tasks are [implementation-plan.md](implementation-plan.md), and the status
line is unchanged — **plan only; nothing implemented; no task approved**.

**How this was read.** Every claim below was checked by reading the named file and symbol on this tree,
not a summary of it, and by `git grep` against `origin/main` rather than from memory. **Citations name
a path and a symbol**, and a line number only where the line itself is the point and was read directly;
a line number is wrong the next time the file is touched, and this folder ships **no pinning test** to
catch a drifted one (F25). Read against **`origin/main` at `af2644fb`**, 2026-11-07, which is also this
branch's base (`git merge-base --is-ancestor origin/main HEAD` holds).

**What could not be read here, and is therefore not claimed.** This checkout has fetched **no
`refs/pull/*/head` ref at all** (`git for-each-ref 'refs/pull/*/head' | wc -l` is **0**), so the
brief's "every open PR's docs" half of the number reading could not be repeated: §9 says so and reads
only what `origin/main` and the local refs carry. The brief also names **task OD2**, **task OD1** and
**pull request #1822**; **none of the three is in this tree** (F15), so nothing below rests on them.

**Verdicts used here.** *Exists* — the tree already has the thing, or something that does the same job
under another name. *Gap* — the fact or the seam is real and nothing acts on it. *Rule* — the tree
states how this must stay, and enforces it. *Wrong* — a thing the ask takes as given is not what the
tree says.

## 1. Headline table: the ask's seven questions, and what the tree has

| The ask's question | What the tree has | Class |
| --- | --- | --- |
| **(1) The shape**: a project-layer key plus a repo key that may only add, over the engine's floor | **No key exists, in either layer** (F3). The engine's classifier is real and is an **allowlist of routine trees**, so its "Sensitive" side is *everything else* and cannot be written down as a list (F1, F22) — the floor the ask wants is **not** a table that already exists to be copied; it is a table that has to be **written**, and the closest thing already in the tree is `rigor.Area` (F21) | **Gap** |
| **(2) Who consumes it, and when**: the runner's escalation, the launcher, O1, the skill, doctor, the console | The classifier has **zero production consumers** (F1). Review levels are resolved **once, at bootstrap**, and **no escalation path exists** (F5). The run record has **no review-level or class field** (F7). `doctor`, the launcher and the console are all real seams a line could be added to (F8, F11, F24) | **Gap** |
| **(3) How an owner edits it**: `settings set`'s allow-list, the managed no-prompt rule, publish and the migration in `fugaro update` | The allow-list and its floor rule are real and are exactly the fence the ask names (F12). The publish path and the policy migrator are real, and `update` already runs the migrator as one of its stages (F13). **`settings set` carries no list-valued key today**, so "add it to the allow-list" is not the shape the tree uses for a list (F12) | **Exists** (the machinery) / **Gap** (the key) |
| **(4) The merge and the semantics**: repo with no project, project with no repo, empty = the floor, pattern syntax, who may write the list | `policy.Merge` is a working tightening-only merge with **union** semantics on exactly this kind of list (`ReviewFloor`, F10, F23). `rigor.Area` is a working *named doublestar glob validated at load and bounded* (F21) — the pattern half is a shape to copy, not to invent. **Nothing reads a classification from the base branch except the review brief**, which is the precedent the ask wants (F14) | **Rule** (merge, patterns, base-read) |
| **(5) The forced level 3, and OD1/OD2** | A task can already force **a rigor mode** (`--mode`) and carry **a whole review-levels block** (`--review-levels`, F6), so "the task forces it" is a smaller addition than the ask implies. **OD1 and OD2 do not exist in this tree** (F15), so the ask's "queued task OD2" is a fact about the owner's list and not about the repository | **Exists** (the seam) / **Wrong** (OD1/OD2, F15) |
| **(6) The first slice, and what ships dormant** | The tree's own precedent is explicit: an additive, optional key ships with **no policy version bump** and the consumer follows (`orchestrator:`'s fields, F13; the classifier itself shipped unattached, F1) | **Rule** |
| **(7) Migration, schemas, goldens, the CLI reference, docs pins, release notes** | Every one has a named home and a named test: `schemas/project-layer.schema.json` + `internal/config/schema_test.go`'s both-surfaces test, the corpus under `testdata/`, `internal/config/scope_test.go`'s golden table, `website/content/cli-reference.md`'s generator (F18), the docs census (F17), and `docs/releases/README.md`'s `### For operators` (F13) | **Rule** |

**The shape of the whole thing.** The ask's two load-bearing assumptions about the tree are **both
wrong**, and correcting them changes the design rather than decorating it:

1. **The engine's floor is not a list.** `internal/orchestrator/sensitivity` is an **allowlist**: a
   change is `Routine` only when *every* path is inside `docs/` or `website/` minus
   `sensitiveExceptions`, and **everything else — including every tree nobody classified — is
   `Sensitive`** (F1, F22). So "the engine's built-in default list … as the floor" has nothing to copy:
   the floor's *effect* exists and is fail-closed, but its *membership* is "not routine", which is not
   a list of paths. A design that wants a named, per-category floor (credentials, guards, targets,
   infrastructure, prompts, CI) **writes that table**, and it is new code, not a re-export.
2. **The project component is `internal/config`'s project layer, not `internal/projectpolicy`.** The
   ask names `projectpolicy` as the project layer; it is the **published policy node** (F4). The layer
   the ask means is `config.ProjectLayer` in `internal/config/layer.go`, whose `layerTopKeys` is the
   closed allow-list a new key must join. Both artefacts are reached by the same `fugaro update`, which
   is why the correction costs nothing in the outcome and everything in the wording.

Both corrections are F4 and F5 in §3 below, and every decision that follows is written against the
tree, not against the ask's reading of it.

## 2. What "security-sensitive" is, on this tree

The ask's premise — "today the definition lives only in this repository's coordinator habits and in
`internal/orchestrator/sensitivity`" — is **nearly, but not exactly, right**. The definition lives in
**four** places, and only one of them is a program:

- **The classifier.** `internal/orchestrator/sensitivity/classify.go`: `Classify(changedPaths []string)
  Class` over `routineRules` (`docs/`, `website/`) minus `sensitiveExceptions` (the release streams, the
  pending workflows, the plan graph artifacts, the website's manifest, lock, downloads, checksums and
  install commands, the published security policy, and the two redesign scripts). Fail-closed: an empty
  list, an unplaceable path and a path outside ASCII are all `Sensitive` (F22). Its package doc says
  the class "picks the model a decision agent runs on" and names `O4`'s model setting as the consumer.
- **The reviewer-routing rule, as the classifier's own test fixture.** `TestRulesTableMatchesTheReviewerRoutingText`
  holds the tables against the routing text, and the plan's O1 task lists the rule's categories
  verbatim: "credentials, writes and guards, the managed gate, Terraform and IAM, the agent environment
  and gateway, agent prompts, recipes, `CLAUDE.md`, `AGENTS.md`, `.claude/**`, plugin skills, CI"
  (`docs/stateless-orchestrator/implementation-plan.md`). **This sentence, not the tables, is the
  closest thing in the tree to the floor the ask wants** — and it exists as prose in a plan document and
  as a fixture, never as a configurable list.
- **The path-scoped rules.** `.claude/rules/credentials-service.md` ("Everything here is
  security-sensitive. Get an independent review before merging…"), `console.md` and `terraform.md` say
  the same thing for their `paths:` scopes. These are the *mechanism the owner already has* for a
  repository-local answer — and it is a `.claude/` rule, which is a coding agent's file, not the
  engine's.
- **The review brief.** `.fugaro/review.md`'s "Mutation-check a security-sensitive change" paragraph
  names the same areas ("credentials, secrets, the managed settings or the gateway, Terraform or IAM,
  the agent's environment, git tokens, the console's spawn or IPC allow-list, a prompt, a recipe,
  CLAUDE.md, a plugin skill, `.github/` or `ee/`"), and `<repo>/fugaro.yaml`'s `agent.review` points at
  it. This is the ask's "this repository's coordinator habits" — and it is a **document read from the
  base branch**, which F14 shows is the tree's established way to hold a control.

**Four answers, one question, no single source of truth, and — for a project that took Fugaro — no
answer at all.** That is the gap the ask is about, and it is a *sharing* gap more than a *mechanism*
gap: the mechanisms (a glob matched against changed paths, a tightening-only merge, a base-branch read,
a level resolver with a floor) all exist. What does not exist is **one list, in config, that says what
this project calls security-sensitive.**

## 3. The code the design has to stand on

**The classifier is unattached.** `git grep -l 'orchestrator/sensitivity'` outside the package returns
**nothing**: no production file imports it, `Classify` has **zero call sites**, and the class flows to
no model, no level and no record (F1). It is a written, tested, **unconsumed** seam — which the ask's
own precedent makes unsurprising (`docs/stateless-orchestrator/implementation-plan.md`: "Task O1 … is
written on the branch …", and its `depends on: none`).

**The project layer.** `config.ProjectLayer` (`internal/config/layer.go`), parsed by
`ParseProjectLayer(data []byte, a LayerAnchor)` with `KnownFields(true)` (strict), sized by
`LayerMaxBytes`, and gated by a **closed key allow-list** — `layerTopKeys` = `version, project,
gcp_project, defaults, profiles, default_profile, claude_models, governance, ab` — plus `layerReserved`
for keys a later release owns. `LayerKey` is the published object's name; `LayerCopyKey(slug)` is the
per-repository copy; `internal/cli/config_publish.go` publishes it. **A new project-layer key joins
`layerTopKeys`, `schemas/project-layer.schema.json`, the scope table, and the corpus** — a named list of
files, carried by D1519 and F18.

**The repository key.** `config.Config` (`internal/config/config.go`) is decoded with
`KnownFields(true)` by `decodeRepo`, then re-decoded after layer merging in `resolve.go`. Its top-level
keys are `version, project, gcp_project, profile, git, agent, budget, workflows, governance, ab,
followup, rigor`. `internal/config/scope.go` classifies every key (`Scopes`, `ScopeRow`, `ScopeOf`,
`ExecutableKeys`) and `internal/config/scope_test.go`'s golden table plus
`internal/config/docs_scope_test.go` against `docs/project-layer.md` pin it. **A new repo key is a row
in that table before it is a field in that struct.**

**The policy node, which the ask calls the project layer.** `internal/projectpolicy/policy.go`:
`Policy`, `Decode`, `knownTopLevelKeys`, `MaxPolicyBytes`, `Version` = 4 with `VersionSince` and
`VersionRelease`. It is the **owner-only published node** — budgets, providers, accounts, profiles,
routing, `review_floor`. `internal/projectpolicy/migrate.go`: `Migrate`, `Change`, `migrators`
(`{3: migrateV3ToV4}`), `tooOld`, `Migratable`. **A key that reaches a run through the node needs the
node's reader to learn it and, if the meaning of an existing key changed, a migrator** (F13).

**The tightening-only merge, and the floor that already exists.** `internal/policy/merge.go`:
`Merge(ceiling Layer, tighten ...Layer) Effective` — "A later layer may only tighten: the smaller
positive cap, the stricter mode …, the intersection of allow-lists", with looser values recorded in
`Effective.Ignored`. Its **list** handling is the precedent the ask wants for a security pattern list,
and it is **already the shape the tree uses for a review-level floor**: `Layer.ReviewFloor
map[rigor.Mode][]Level`, `(*Effective).unionReviewFloor` — a layer can **add** a level and **never
remove** one — `Effective.ReviewFloor(mode)`, `ParseFloor`, `Level`, `KnownLevel` (F10, F23). The
security list is *the same idea, one level up*: a list of patterns a layer may only add to.

**The pattern dialect, and the twin of the key the design wants.** `internal/rigor/area.go`: `Area{Name,
Match, Mode}` — "a name, a doublestar path glob relative to the repository root, and the mode the rule
sets", matched by `internal/rigor/select.go`'s `matches` via **`doublestar.Match(pattern, p)`**,
**validated at load** by `Area.Validate` ("a pattern the matcher cannot read is a configuration error
rather than a rule that silently never fires"), and **bounded** — `MaxAreaName` 40, `MaxAreaPattern`
200, `MaxAreas` 100 — with `areaNameRE` closing the name's character set because the name "reaches three
surfaces a person reads (the record, the pull request body, `fugaro watch`)". Its own doc names the
other doublestar callers: `internal/plan/footprint.go`, `internal/imagecheck`,
`internal/config/validate.go`. **A security pattern is `rigor.Area` with a category where the mode is**,
and the design should copy its four checks in the same order rather than write a fifth matcher (F21).
The **other** dialect in the tree is `config.MatchSegment` (`internal/config/matchseg.go`) — segment-wise,
`*` only, never crossing `/` — and it is for **repository names** (`MatchRepo`,
`ProjectSecret.AllowsData`), not paths; the design must not confuse the two.

**The consumers, and what each would cost.**

- **The runner, after implement.** The stage order is `bootstrap → (check) → (digest) → implement →
  {first_line → review → adversarial} → finalize` (`internal/runner/runner.go`, `runAgentLoop` /
  `agentLoop`). Levels are resolved **once**, in `(*run).resolveLevels` (`internal/runner/plan.go`)
  inside `resolvePolicy`, by `(*run).applyLevels` (`internal/runner/routing.go`) →
  `config.ResolveLevels(LevelInputs{…})` (`internal/config/levels.go`), and **the plan is built from
  them at that moment** (`planOf`/`planRecipe`). **There is no review-level escalation anywhere**: the
  runner *does* re-resolve the **rigor mode** three times — `bootstrapRigor`, `roundRigor` (called from
  `afterStage(ctx, "implement")`), `finalizeRigor` (`internal/runner/rigor.go`) — folded monotonically
  by `runstore.RigorRecord.Applied`, but `resolveLevels` never runs again (F5, F9). **So the seam the
  ask describes is real and empty**: the window after `implement` exists (`roundRigor` is already
  called there) and what is missing is a re-resolution and a plan rebuild. Changed paths are available
  at that point: `(*Repo).ChangedFiles` (`internal/gitops/changedfiles.go`), recorded by
  `(*run).recordChangedFiles` into `runstore.ChangedFiles{Paths, Total, Truncated}`
  (`internal/runstore/changedfiles.go`, bounded by `MaxChangedFiles` 200 and `MaxChangedPathBytes`).
- **The launcher.** `fugaro run --path` (repeatable) lands in `task.Spec.Paths` (`internal/task/task.go`,
  bounded by `MaxDeclaredPaths` 100); `Spec.ExpectedScope` exists ("roughly which files or areas the run
  is expected to touch") and **no launcher writes it yet**; **there is no `Files` field** anywhere in
  `internal/task` or `internal/cli/plan*.go` (F8). A plan node's predicted path set is
  `internal/plan/footprint.go`'s `Footprint`, "always labelled a prediction (D28)" — advisory, and
  reading it as a commitment would be reading a prediction as a fact. **The launcher's honest half is
  therefore `Spec.Paths`, not a plan task's `Files`.**
- **O1.** One source of truth is the ask's own word; today there are two tables in one package and four
  answers in four files (§2). The design's answer is a single table both read (D1511).
- **The plugin working skill.** No `plugin/` file mentions `security-sensitive` except
  `plugin/skills/setup/SKILL.md`'s unrelated `followup.trusted` warning; `--review-levels` appears
  **nowhere** under `plugin/`; level 3 appears once, in
  `plugin/skills/working/reference/launch.md` ("up to three review levels … then the adversarial review
  where the run's review levels run level 3, which is Critical's row by default"). **Every command and
  flag a skill names is checked against the live cobra tree** by `plugin/skills_lint_test.go`
  (`newCommandIndex` → `commandIndex.resolve`/`checkCommand`, `bareFlagRE`, `ci.allFlags`), with
  `ownerCommands`/`userRunsAllowed` for owner-only commands and `cli.PaidCommandNames()`
  (`internal/cli/api.go`) for paid ones. **A skill that names `fugaro security paths` fails the lint
  until the command exists** — which is the ordering the skill task must respect (F16).
- **doctor.** `doctorCheck{ID, OK, Problem, Fix, Severity, Class}` (`internal/cli/doctor.go`), printed as
  `doctorOutput` by `emitDoctor`, classified by `doctorClassOf`/`classifyDoctorChecks`
  (`internal/cli/doctor_render.go`), with the `--json` contract in `console/docs/json-contract.md` §6.4.
  **One more line is one more `doctorCheck`** from a check-providing function, the pattern
  `doctor_managed_audit.go`'s `doctorManagedAudit` already follows (F11).
- **The console.** Reads are bounded by `consoleReadPaths` (`internal/cli/project.go`) in Go and the
  exact-argv spawn table in `console/src/main/spawn.ts`; the two halves are held equal by a Go test. The
  read-only-screen precedent is real and is the console-managed-screens work (`docs/console-managed-screens/`),
  which also defers a screen that needs a field the CLI does not emit. The ask already says "defer", and
  §7 makes that a fence rather than a preference (F24).

**The record.** `runstore.Record` (`internal/runstore/runstore.go`) carries `Rigor *RigorRecord`,
`Tier *TierRecord`, `ChangedFiles`, `ReviewScope`, `Reviews`, `Checkpoint`, `Policy`, `Recipe`,
`ProjectLayer`, `ConfigSHA256` — and **no review-level, class or sensitivity field** (F7). A record
field naming the escalation would be an **addition** (a field never changes meaning; a new name is
free), which is what makes slice 2 shippable without a version bump.

## 4. Findings

Twenty-six findings, each read from the named path and symbol on this tree. **The class column is
§1's four verdicts**, and the number is what the rest of this file and
[implementation-plan.md](implementation-plan.md) cite.

| # | Finding | Class | Evidence, by path and symbol |
| --- | --- | --- | --- |
| **F1** | The security-sensitive classifier is **on `main` and is unattached**: no production file imports the package and `Classify` has **zero call sites**, so the class reaches no model, no review level and no record | **Gap** | `internal/orchestrator/sensitivity/classify.go` (`Class`, `Routine`, `Sensitive`, `Classify`, `routineRules`, `sensitiveExceptions`); `git grep -l 'orchestrator/sensitivity'` outside the package returns nothing; its only referrer is `classify_test.go` |
| **F2** | The definition of "security-sensitive" lives in **four places and none of them is a config key**: the classifier's tables, the reviewer-routing rule (as a test fixture and as prose in a plan), the `.claude/rules/*.md` `paths:` scopes, and the review brief | **Gap** | `classify.go`; `TestRulesTableMatchesTheReviewerRoutingText` (`classify_test.go`); `.claude/rules/credentials-service.md` ("Everything here is security-sensitive…"), `console.md`, `terraform.md`; `.fugaro/review.md`'s "Mutation-check a security-sensitive change", pointed at by `fugaro.yaml`'s `agent.review` |
| **F3** | **Neither layer has a key for it.** No project-layer key, and no repo key in `fugaro.yaml` | **Gap** | `layerTopKeys` (`internal/config/layer.go`) is `version, project, gcp_project, defaults, profiles, default_profile, claude_models, governance, ab`; `config.Config` (`internal/config/config.go`) is `version, project, gcp_project, profile, git, agent, budget, workflows, governance, ab, followup, rigor` |
| **F4** | **The ask's "projectpolicy" is not the project layer.** `internal/projectpolicy` is the **published policy node** (owner-only, JSON, `config/policy/shared`); the project layer the ask means is `config.ProjectLayer` in `internal/config` | **Wrong** | `internal/projectpolicy/policy.go` (`Policy`, `knownTopLevelKeys`, `Version`); `internal/projectpolicy/paths.go` (`PathShared`); `internal/config/layer.go` (`ProjectLayer`, `ParseProjectLayer`, `LayerKey`, `LayerSum`) |
| **F5** | **There is no review-level escalation anywhere.** Levels are resolved **once**, at bootstrap, and the plan is built from them at that moment | **Gap** | `(*run).resolveLevels` (`internal/runner/plan.go`) inside `resolvePolicy` → `(*run).applyLevels` (`internal/runner/routing.go`) → `config.ResolveLevels` (`internal/config/levels.go`); the plan is built by `planOf`/`planRecipe`; nothing re-runs `resolveLevels` |
| **F6** | **Level 3 is the adversarial step**, and `--review-levels` is a **file** flag carrying an `l1`/`l2`/`l3` block — not a level number | **Exists** | `recipe.StepAdversarial`, `stageAdversarial` (`internal/runner/routing.go`), `runstore.TierAdversarial`; `rigor.defaultLevels`' `Critical: {L2: LevelOn, L3: LevelOn}` (`internal/rigor/levels.go`); `config.ReviewLevels`, `LevelSetting`, `ParseReviewLevelsYAML` (`internal/config/levelsetting.go`); `o.reviewLevelsPath` (`internal/cli/run.go`) |
| **F7** | The run record has **no review-level, class or sensitivity field** — only the rigor mode and the tier | **Gap** | `runstore.Record` (`internal/runstore/runstore.go`): `Rigor *RigorRecord`, `Tier *TierRecord`, `ChangedFiles`, `ReviewScope`, `Reviews`, `Checkpoint`, `Policy`, `Recipe`, `ProjectLayer`, `ConfigSHA256` |
| **F8** | A launch can declare **paths** (`--path`) but there is **no `Files` field**, and a plan node's path set is a labelled **prediction** | **Exists / partial** | `task.Spec.Paths` and `MaxDeclaredPaths` 100, `Spec.ExpectedScope` ("no launcher writes it yet") (`internal/task/task.go`); `--path` (`internal/cli/run.go`); `plan.Node.Footprint` (`internal/plan/footprint.go`, "always labelled a prediction (D28)") |
| **F9** | The **rigor mode** is raise-only and is re-resolved mid-run, but the **levels** are not — the seam the ask describes is real and empty | **Gap** | `rigor.Resolve` (raise-only `Request`, `internal/rigor/resolve.go`); `bootstrapRigor`, `roundRigor` (called from `afterStage(ctx, "implement")`), `finalizeRigor`, folded by `runstore.RigorRecord.Applied` (`internal/runner/rigor.go`) |
| **F10** | `policy.Merge` is a working **tightening-only** merge — "a later layer may only tighten … the intersection of allow-lists" — with looser values kept in `Effective.Ignored` | **Exists** | `Merge`, `tightenInt`, `tightenCap`, `modeRank`, `Effective`, `Ignored`, `SourceCeiling`/`SourcePolicy`/`SourceBranch` (`internal/policy/merge.go`) |
| **F11** | doctor's shape is a check struct, and `doctor --json` prints a document of them — **one more line is one more check** | **Exists** | `doctorCheck{ID, OK, Problem, Fix, Severity, Class}` and `doctorOutput`/`emitDoctor` (`internal/cli/doctor.go`); `doctorClassOf`/`classifyDoctorChecks` (`internal/cli/doctor_render.go`); the precedent `doctorManagedAudit` (`internal/cli/doctor_managed_audit.go`); the contract in `console/docs/json-contract.md` §6.4 |
| **F12** | The managed `settings set` allow-list is **exactly two scalar keys** with a **raise-only floor rule**, and a person at a terminal is not bound by it; **no list-valued key is on it** | **Exists / Gap** | `settingsManagedKeys` = `layer.defaults.agent.review_rounds`, `layer.defaults.agent.adversarial_rounds`, `settingsManagedAdmits`, `settingsAdmittedRefusal`, `settingsFloorRule`, `settingsFloorRefusal`, `settingsFloorBase` (`internal/cli/settings_managed.go`); `docs/managed-agent-converge/reconciliation.md` D1027 |
| **F13** | `fugaro update` already runs a **policy migration stage**, and "an installation at the previous version converges" is a named test | **Exists** | `policyMigrateStage` (`internal/cli/init_policy_migrate.go`); `Migrate`, `migrators`, `tooOld`, `Change` (`internal/projectpolicy/migrate.go`); `TestUpdateConvergesFromThePreviousRelease` (`internal/cli/update_converge_test.go`); the stage order in `initStages` (`internal/cli/init_stages.go`) |
| **F14** | Every control that governs a run is **read from the base branch, never the reviewed checkout** — the precedent the ask names | **Rule** | `readBaseConfig` (`internal/runner/followup.go`, requires `base == ref`); `readReviewCommandFromBase`, `baseInstructions` (`internal/runner/reviewmode.go`) read `fugaro.yaml`, the review command and `CLAUDE.md`/`.claude/rules` **from the base**; `docs_base_test.go` and `docs_in_job_review_test.go` pin it |
| **F15** | **OD1, OD2 and pull request #1822 are not in this tree.** `git grep` for `OD1`/`OD2` over every ref returns nothing; `needs_owner` exists only as a failure-class field | **Wrong** | `git grep -rEn '\bOD[0-9]'` — nothing (working tree and all refs); `docs/design/failure-classes.md` and `docs/failure-classes/spec.md` hold `needs_owner` as one of a failure class's three fields |
| **F16** | Every command and flag a skill names is **checked against the live cobra tree**; the `working` skill already documents level 3 and the owner's floor, and names `fugaro review --pr N` as the independent review | **Rule** | `newCommandIndex`, `commandIndex.resolve`/`checkCommand`, `bareFlagRE`, `allFlags`, `ownerCommands`, `userRunsAllowed` (`plugin/skills_lint_test.go`); `cli.PaidCommandNames()` (`internal/cli/api.go`); `plugin/skills/working/reference/launch.md`; `plugin/skills/routing/SKILL.md` |
| **F17** | The docs census is **101**, and adding one pin moves it — plus three census re-check tests between two other folders' pins | **Rule** | `docs/rigor-modes/reconciliation.md`'s row `ls internal/cli/docs_*_test.go \| wc -l` = 101; `TestDocsRigorModesCensusStatesItsCommands` (`docs_rigor_modes_test.go`); `TestDocsRigorModesReconciliationCountsTheConfigManagementPin` and `…TheRepoImageRemovalPin`; `TestDocsManagedAgentConvergeCensusRowMovedForItsPin` |
| **F18** | The website's CLI reference is **generated** from the command tree and pinned against drift, so a new command is a regenerated file | **Exists** | `cliRefPath` = `website/content/cli-reference.md` and its generator (`internal/clidoc`, cited by `internal/cli/docs_website_cliref_test.go`, "generated from the command tree by …") |
| **F19** | The number blocks on `main`: the highest **written** decision is **D1489** and **D1490–D1499** appear only as `docs/console-managed-screens`' reserve; the highest `## Check` **section** is **291** and the highest **cited** number is **360**. **No pull-request head is fetched in this checkout** | **Exists** | §9 carries the commands; `git for-each-ref 'refs/pull/*/head' \| wc -l` = 0 |
| **F20** | This repository's **own** answer is a review brief, read from the base — the ask's "coordinator habits", and a document rather than a key | **Exists** | `.fugaro/review.md`'s "Mutation-check a security-sensitive change" names credentials, secrets, the managed settings, the gateway, Terraform/IAM, the agent's environment, git tokens, the console's spawn/IPC allow-list, a prompt, a recipe, `CLAUDE.md`, a plugin skill, `.github/`, `ee/`; `fugaro.yaml`'s `agent.review` |
| **F21** | The glob dialect the tree already matches paths with is **doublestar**, via a **named, load-validated, bounded** rule — the twin of the key this design wants. A **second**, segment-wise dialect exists for repository names and must not be confused with it | **Rule** | `rigor.Area{Name, Match, Mode}`, `Area.Validate`, `MaxAreaName` 40, `MaxAreaPattern` 200, `MaxAreas` 100, `areaNameRE` (`internal/rigor/area.go`); `doublestar.Match` in `matches` (`internal/rigor/select.go`); the other callers `internal/plan/footprint.go`, `internal/imagecheck`, `internal/config/validate.go`; `config.MatchSegment` (`internal/config/matchseg.go`, for `MatchRepo`/`AllowsData`) |
| **F22** | The classifier **fails closed**: an empty path list, an unplaceable path, a path outside ASCII and a non-clean relative path are all `Sensitive` | **Rule** | `Classify`, `routinePath`, `cleanRelative` (`internal/orchestrator/sensitivity/classify.go`); `TestEmptyPathListIsSensitive`, `TestUnlistedPathIsSensitive` (`classify_test.go`) |
| **F23** | A **review-level floor with union semantics already exists** and already speaks `l1`/`l2`/`l3` — a layer can add a level and never remove one. It is the merge precedent for a list | **Exists** | `Layer.ReviewFloor map[rigor.Mode][]Level`, `(*Effective).unionReviewFloor`, `Effective.ReviewFloor(mode)`, `ParseFloor`, `Level`, `Levels`, `KnownLevel` (`internal/policy/policy.go`, `internal/policy/merge.go`); `projectpolicy.Policy.ReviewFloor` |
| **F24** | A console read is an **exact-argv fence with two halves held equal by a Go test**, and the tree's precedent for deferring a screen is `docs/console-managed-screens/` | **Rule** | `consoleReadPaths`, `consoleRead` (`internal/cli/project.go`); the design's §5.1 rule 6 table (`docs/design/desktop-console.md`); `SPAWN_COMMANDS`/`argvFor` (`console/src/main/spawn.ts`), `CONSOLE_OPERATIONS` (`console/src/main/ipc.ts`); `docs/console-managed-screens/reconciliation.md` D1486 |
| **F25** | **There is no registry test over `docs/`**: a folder may be documents alone, and a four-document folder with no pin is an existing precedent | **Rule** | `docs/bitbucket_credentials_test.go`, `docs_rigor_modes_test.go`, `docs_in_job_review_test.go`, `docs_backlog_test.go` each state it in their own comments; `docs/image-env/` is `README.md`, `implementation-plan.md`, `reconciliation.md`, `spec.md` and nothing else |
| **F26** | The **independent review is already a command** — a recipe in review mode, launched with `--pr` — and the skills point at it rather than at a rule about it | **Exists** | `internal/cli/review.go` (`Use: "review --pr N"`); `plugin/skills/routing/SKILL.md` ("the independent review is the command: `fugaro review --pr N`…"); `plugin/skills/working/SKILL.md` |

## 5. The list as a security control, as an attacker would see it

The ask is right that this is the load-bearing decision: **the pattern list is a security control, and
the direction it may move is one way.** Three properties follow, and each has a precedent in the tree.

- **It may only add sensitivity, never remove it.** `policy.Merge` and `unionReviewFloor` are the
  working version of exactly this rule for a list (F10), and the classifier's own allowlist direction —
  "Narrowing an allowlist is the direction a review may move it in on its own" — is the classifier's
  stated rule. A repo entry, a project entry and the floor all **union**; nothing intersects, nothing
  subtracts. Concretely: a project can never mark a floor pattern routine, mark the whole repository
  routine, or empty the list down to "nothing is sensitive" (D1504, D1506).
- **It is read from the run's base, not from the tree being reviewed.** This is the tree's established
  property for every control that governs a run: `internal/runner/followup.go`'s `readBaseConfig` fetches
  the base and requires `base == ref` before reading `fugaro.yaml`, `agent.instructions` and the review
  file; `internal/runner/reviewmode.go`'s `readReviewCommandFromBase` and `baseInstructions` read the
  review command and `CLAUDE.md`/`.claude/rules` **from the base branch**, "never the reviewed checkout".
  **A security pattern list read from the PR's own branch is a list the PR can edit** — the change that
  wants a cheaper review would supply the rule that decides its review. The design follows the review
  brief (D1518, F14).
- **It is written only through audited, owner-only paths.** The project layer is published through
  `internal/cli/config_publish.go`; the managed settings write goes through `managed_gate.go`'s
  `managedGate` (`admit`, `record`, re-read before write) and lands an `AuditRecord`
  (`internal/managed/audit.go`, `WriteAudit`) under `AuditPrefix`, read back by
  `managed.ListAuditRecords` and surfaced by `doctor_managed_audit.go`. **A new control inherits that
  path or it is a new hole** (D1516, F12).

**The one direction the list must not be able to move, stated plainly:** *a change may never make its
own review cheaper.* The floor cannot be lowered by any layer; a repo cannot remove a project pattern;
a project cannot remove a floor pattern; an empty list is the floor; and the list in force for a run is
the base branch's. If the design held only five of those six, the sixth is the exploit.

## 6. The floor, written down

The ask's categories — **credentials, guards, targets, infrastructure, prompts, CI** — are the
reviewer-routing rule's own categories (§2), and a per-project floor has to name them **as patterns**,
because "not routine" is not a list. The design's recommendation (D1505) is a floor table in the
classifier's package, one row per category, each a **named doublestar glob** in `rigor.Area`'s shape,
covering at least what the tree already treats as sensitive by other means:

| Category | What it covers, by the paths the tree already names |
| --- | --- |
| **credentials** | `internal/creds/**`, `internal/credsvc/**`, `internal/gitproxy/**`, `internal/gateway/**` (`.claude/rules/credentials-service.md`'s own scope), `SECURITY.md` |
| **guards** | the console's `spawn.ts`/`ipc.ts`, `internal/policy/**`, `internal/managed/**`, `internal/cli/managed_*.go`, the settings write path, `internal/orchestrator/**` |
| **targets** | `.github/**`, `docs/pending-workflows/**`, `deploy/terraform/**`, `internal/infra/**` |
| **infrastructure** | `images/**`, `internal/agentenv/**`, `internal/gateway/**`, the Dockerfiles |
| **prompts** | `CLAUDE.md`, `AGENTS.md`, `.claude/**`, `plugin/skills/**`, `.fugaro/review.md`, `internal/agent/**` |
| **CI** | `.github/workflows/**`, `scripts/release.sh`, `docs/release-streams.yaml` |

**The table above is the design's proposal and not a fact about the tree** — §11 says so. What *is* a
fact is that every path in it is currently held sensitive by one of §2's four mechanisms, and that
**the union of those four mechanisms is the floor the design must not go below** — which is a property
a test can hold (`TestFloorIsAtLeastWhatTheRoutingRuleNames`) rather than a promise.

**Why a named table rather than re-using the allowlist's complement.** The classifier's answer is
"Routine iff every path is routine, else Sensitive". That is the right **model floor** (F1's `O4`) and
the wrong **level-3 trigger**: "level 3 for every change that is not documentation" is not the rule the
ask wants, it is a rule that would run the adversarial pass on nearly every run and cost accordingly.
The level-3 trigger has to be a **positive list**, which is exactly why the ask asks for one.

## 7. What this design does not do, and what it satisfies

- **It changes no code, no schema, no config, no fixture and no workflow.** It writes nothing under
  `internal/`, `schemas/`, `plugin/`, `console/`, `mobile/`, `ee/`, `deploy/` or `testdata/`, and no
  file outside `docs/security-classification/`. Every command, flag, field, key and table below is a
  proposal.
- **It adds no pinning test, and it does not move the docs census.** There is **no registry test over
  `docs/`** in `internal/cli` (`docs/bitbucket_credentials_test.go`, `docs_rigor_modes_test.go`,
  `docs_in_job_review_test.go` and `docs_backlog_test.go` all say so in their own comments), so a folder
  may be documents alone; `docs/image-env/` is the four-document precedent this folder follows. Adding
  a pin would move `docs/rigor-modes/reconciliation.md`'s census row **101 → 102** and force edits to
  three census re-check tests (§7, D1519, F17) — **edits to files outside this folder**, which the
  brief's DOCS ONLY forbids. The plan's tasks therefore **name the pin each will add**, and the census
  moves once, in the task that adds the first test.
- **It does not build OD1, OD2, the needs_owner work, the orchestrator or the console screen.** The
  console screen is deferred by the ask itself and by the console's own precedent (§3).
- **It does not invent a role**, a new permission model, or a second matcher (§3, F21).
- **It adds no dependency and proposes none.**

## 8. Decisions D1500–D1519

Each row is a question the ask raises, **a recommendation**, the alternative, that alternative's cost,
and **an empty owner's-answer cell** — the ask's own word is "Do not decide anything silently", and this
folder's status is "no task approved", so the cell stays empty until the owner fills it. A row whose
recommendation needs no decision is not here; these twenty are the ones that do.

| # | Question | Recommendation (and why) | Alternative | Its cost | Owner's answer |
| --- | --- | --- | --- | --- | --- |
| **D1500** | What is the shape of the answer: one block, or the ask's two parallel lists (`security_paths` **plus** `categories`)? | **One block, `security`, whose list is `security.patterns` — each row `{name, match, category}`.** A pattern with no category cannot be explained to the person reading the pull request ("why did this run a level-3 review?"), and a category with no pattern names nothing. One row per pattern is `rigor.Area`'s own shape (`Area{Name, Match, Mode}`), which the record, the PR body and `watch` already know how to print (§3, F21) | The ask's `security_paths: [globs]` plus a separate `categories:` map | Two lists that can disagree: a glob whose category nobody wrote, and a category whose globs nobody kept — and no name on a match, so the record's "which rule decided" is a glob the reader has to re-read | *(empty)* |
| **D1501** | Which layer carries it: the project layer, the repo's `fugaro.yaml`, or both? | **Both, with the project layer first**: `layer.security` on `config.ProjectLayer` (joining `layerTopKeys` and `schemas/project-layer.schema.json`) and a repo `security:` key on `config.Config`. The ask wants "a project level component, and an optional repo-level one", and the two layers already meet in exactly this way for `rigor` (`Config.RigorConfig()`, `ProjectRigor()`, `RepoRigor`) | Project layer only | Every repository of a project that has one extra sensitive tree must be special-cased in the layer, and a repository owner with no project has no way to say it at all — the ask's "optional repo-level one" is dropped | *(empty)* |
| **D1502** | Which glob dialect, and is it documented? | **`doublestar`, the dialect the tree already matches with** — `rigor.Area.Match` via `doublestar.Match` (`internal/rigor/select.go`), the same matcher `internal/plan/footprint.go`, `internal/imagecheck` and `internal/config/validate.go` use. Validated **at load** by the same four checks in the same order `rigor.Area.Validate` uses — `internal/config/validate.go`'s `validateRebuild` for `rebuild.paths`: not empty, repository-relative (never a leading `/`), no `..` component, and `doublestar.ValidatePattern` — bounded at **200 bytes** like `MaxAreaPattern`, and named by `areaNameRE`'s closed character set. **Not** `config.MatchSegment`, which is the repository-name dialect and never crosses `/` (§3) | A second, purpose-built matcher for security paths | A fifth matcher in a tree that already has two, with its own edge cases (`**` crossing `/`, a trailing `/`, a case-folded comparison) — and the divergence only shows up in a security decision, which is the worst place to discover it | *(empty)* |
| **D1503** | Are patterns that match everything, or nothing, refused? | **`*`, `**` and an empty pattern are refused at write time** — a pattern that marks the whole repository sensitive is not a pattern, it is the "always level 3" setting, and D1517 is where that lives. A pattern that matches no path **is not refused** (the tree may not exist yet, and a project may write its list before its first commit to it), but **`doctor` says once that a pattern matched nothing in the base** | Refuse both, or refuse neither | Refusing a currently-empty pattern makes a correct list unwritable in the order projects actually write it; refusing neither lets `**` masquerade as a pattern and hides the real intent inside a glob — the exact thing a reader of the record cannot see | *(empty)* |
| **D1504** | What does an empty list mean? | **The floor, and never "nothing is sensitive".** An absent project list, an absent repo list and an explicitly empty list all mean **the floor alone**. This is the ask's own ruling, and it is the only fail-closed reading: the alternative reading makes deleting a line the way to buy a cheaper review | Empty means "nothing extra beyond what the classifier would say anyway" | Identical in effect to the recommendation *only* while the classifier's default stays fail-closed — and it puts the whole guarantee outside the list, so the day the classifier's default changes, an empty list silently means something else | *(empty)* |
| **D1505** | What is in the floor, and where does it live? | **A named table in the classifier's package** (`internal/orchestrator/sensitivity`), one row per category, each a named doublestar glob, covering at least the paths §2's four mechanisms already hold sensitive — the concrete list is §6's table. It is **new code**, because the classifier's Sensitive side is "not routine" and cannot be copied as a list (F1, F22) | Re-export the classifier's complement ("everything not routine") as the floor | The level-3 trigger becomes "almost every run", the adversarial pass runs on documentation-adjacent changes it was never meant for, and the cost the ask is trying to *bound* becomes the default | *(empty)* |
| **D1506** | How do the layers combine? | **Union, tightening-only, exactly as `policy.Merge` and `unionReviewFloor` do** (F10, F23): floor ∪ project ∪ repo, no intersections, no subtractions, and a looser value recorded in `Effective.Ignored` the way the merge already records one. A key `security` that a layer sets and a later layer does not **inherits**, never clears | Last-writer-wins, or a repo key that replaces the project's | A repo can delete a project's protection by writing `security: {}` — the one direction the ask says must be impossible ("a repo can never remove a project pattern or the engine's built-in floor list") | *(empty)* |
| **D1507** | Who consumes it at run time, and when? | **The runner, in the window after `implement` and before the review loop.** `roundRigor` is already called there (`afterStage(ctx, "implement")`), so the seam exists; the escalation adds a classification of `recordChangedFiles`' paths and, on any match, a **re-resolution** of the levels and a **rebuild of the plan** — both of which are currently computed once at bootstrap and are why this is not a two-line change (F5) | Only the launcher: match the task's declared paths at launch and set the block there | The task declares no paths on nearly every launch (`Spec.Paths` is opt-in and `ExpectedScope` is written by nothing), so the escalation fires almost never — and the runs it most needs to catch are the ones that surprised their author | *(empty)* |
| **D1508** | Which levels does a match turn on, and does the rigor mode move? | **Level 3 only — the adversarial step — and the rigor mode does not move.** Level 2 is the senior review that every mode above POC already runs; the ask's rule is the *independent/security* review, which is level 3 (`recipe.StepAdversarial`, `stageAdversarial`, `runstore.TierAdversarial`, `rigor.defaultLevels`' `Critical` row). The mode stays the mode: moving it would change round counts and the findings policy (`rigor.Blocks`), which is a different decision than "this change needs a harder review" | Level 2 and 3, or a mode raise to `Critical` | A mode raise rewrites findings policy and round counts for a change whose *only* new fact is its paths, and the record can no longer say why the run got stricter — D1509's field becomes a guess | *(empty)* |
| **D1509** | What records it? | **A new additive member on `runstore.Record`** naming the escalation, the matched pattern's `name`, its `category`, and the moment — no existing field changes meaning, so no version bumps (F7). It is what makes "why did this run review at level 3?" answerable from the record rather than from the diff | No record field: the level is visible in the plan's steps only | The record already prints `Rigor` with its `From`/`At` and its moments; a run whose level rose with no reason recorded is a run whose review level nobody can audit — and the ask's whole subject is auditability | *(empty)* |
| **D1510** | What does the launcher do? | **It computes and records the answer for the declared paths, and warns — it does not refuse.** `Spec.Paths` is what a launch can honestly know (F8); the run's own diff is authoritative, and a launcher that refused a launch would refuse on a prediction the run's classification then overrides | Refuse a launch whose declared paths match and whose level block does not ask for level 3 | A refusal on a *declared* path set that the diff then does not touch — the run is blocked by a path it never changed. The warning reaches the same person with the same information and no false refusal | *(empty)* |
| **D1511** | One source of truth with O1: where does the table live? | **In the classifier's package, read by both `Classify` and the escalation.** The floor table, the project/repo merge and the matcher become one exported surface (`sensitivity.Patterns`, `sensitivity.Match(paths, list)`), and O1's `routineRules`/`sensitiveExceptions` stay as they are — they answer the *model* question, the pattern list answers the *level* question, and the shared thing is the dialect and the merge, not the answer | A second list beside the classifier, owned by the runner | Two lists, two matchers, two merge rules for one question — and the day they disagree, the disagreement is invisible to both, because neither reads the other | *(empty)* |
| **D1512** | The CLI command an agent calls instead of guessing: its name and contract | **`fugaro security paths [--check PATH…] [--json]`.** Without `--check`, it prints the list in force (the floor, the project's rows and the repo's rows, each with its source layer). With `--check`, it answers per path. **The `--json` document**: `{project, repo, base, floor_source, security: {patterns: [{name, match, category, source}], count}, checked: [{path, sensitive, matched: {name, category, source}}], error}` — `source` ∈ `floor|project|repo`, `sensitive` a bool, and a path the reader cannot place is `sensitive: true` (fail-closed, F22). A refusal names the path, never a value | Make it a `fugaro config security` subcommand beside `config show`/`config list` | `config`'s subcommands read *the repository's* configuration; this reads the *merged resolution for a run*, whose base-branch half and whose project half a `config` verb does not reach — and `config`'s output shape is pinned by `internal/config`'s docs tests | *(empty)* |
| **D1513** | doctor's line | **One `doctorCheck`** (`doctor_managed_audit.go`'s pattern) whose `problem`/`fix` name the list in force: how many patterns come from the floor, the project and the repo, and the row **D1503** promises for a pattern that matched nothing in the base — with the whole thing in `doctor --json` as the check's own fields (F11) | No doctor line | The one place a person would notice that their list does nothing is the one place it is not said — and D1503's "matched nothing" case is exactly a silent no-op | *(empty)* |
| **D1514** | The console screen | **Deferred, and by the console's own rule rather than by preference.** The ask says defer; the console's reads are an exact-argv fence (`consoleReadPaths`, `spawn.ts`) whose two halves a Go test holds equal, and the precedent (`docs/console-managed-screens/`, F24) is that a screen waits for a field the CLI emits and adds **no reader of its own**. If the screen is ever built, it draws D1512's document and nothing else | Build a read-only screen now | A new exact-argv row, a new IPC operation, a `docs/design/desktop-console.md` §5.1 row, the preload surface and the fixture — a SECURITY-SENSITIVE console change for a list `fugaro security paths --json` already prints | *(empty)* |
| **D1515** | How does an owner edit it: the `settings set` allow-list (D1027), or the layer file? | **The layer file and `fugaro.yaml` — not `settings set`.** `settings set`'s allow-list (`settingsManagedKeys`) holds exactly two **scalar** keys and its floor rule is written for a scalar that may only be raised (F12); a pattern **list** is not that shape, and the tree's precedent for a list-valued, layer-carried, publishable key is `rigor.areas` — edited in the file, validated at load, published by `config publish`. **The managed allow-list admits nothing new** | Add a `settings set security.patterns.add` verb | A verb that pushes onto a list is a second write path with its own fence, its own floor rule and its own audit record — for an edit the file already carries, and the floor rule the allow-list exists for does not translate to a list | *(empty)* |
| **D1516** | Who may write the list, and is it audited? | **Only through the paths that already audit themselves.** The project layer through `config publish` and, on a managed project, through `managed_gate.go`'s gate with its `AuditRecord`; the repo key through the repository's own review. **A managed project takes no prompt for it** (`internal/cli/settings_managed.go`: "a managed project takes no confirmation prompt, which is the whole point of a managed project"; `internal/initflow/loop.go`'s `ManagedGate`, `internal/initflow/stage.go`'s `PersonAtTheTerminal`/`ConfirmsByGate`) — **a person at a terminal on a managed project is never asked to type a name**, and neither is an agent's session, which may only *add* | A separate `fugaro security publish` with its own gate | A second gate for a control that already has one, and — the real cost — a second audit family that `doctor`'s `managed-audit` check does not read, so the write is invisible to the one place writes are looked for | *(empty)* |
| **D1517** | A task that is security-sensitive by nature, touching no listed path: how does it force level 3? | **Two ways, one raise-only and one a project setting.** (a) A task field `security` with a `fugaro run --security` flag that forces the level-3 row for that run — raise-only, so it needs no fence and no allow-list, exactly as `--mode` already raises (`internal/rigor/resolve.go`'s raise-only `Request`). (b) A project-layer `security.always_level_3` for a project that wants it on every run — which is where D1503's refused `**` pattern belongs | Inferring it from the issue text or the task title | A classification decided by how a change describes itself is the one thing D1234 refuses by name ("the class must not depend on how a change describes itself") and the classifier's own `TestDescriptionCannotLowerTheClass` pins | *(empty)* |
| **D1518** | Where is the list read from, and does it interact with OD1/OD2? | **From the run's base branch, never the reviewed checkout** (F14: `readBaseConfig`, `readReviewCommandFromBase`, `baseInstructions`), so a PR cannot supply the rule that governs its review. **On OD1/OD2**: neither exists in this tree (F15), so the design does not wait on them; the plugin-skill sentence arrives once D1512's command exists (the skill lint fails a command the tree lacks, F16), and `needs_owner` is a failure-class field with no relation to this list | Read the repository's working copy | A change that carries its own pattern list can also carry its own exemption from it — the exact exploit §5 names, and the reason the review brief is already read from the base | *(empty)* |
| **D1519** | The first slice, what ships dormant, and the migration burden | **The floor and the command first.** Slice 1: the floor table, the merge, `fugaro security paths --check --json` and doctor's line — **no escalation**, so nothing changes a run's cost and the whole slice is safe to ship dormant. Slice 2 adds the runner escalation and D1509's record field. Slice 3 adds the two config keys and their schemas. Then, in the same release as the first config key: `layerTopKeys`, `schemas/project-layer.schema.json`, `schemas/fugaro.schema.json`, the `testdata/` corpus, `internal/config/scope.go`'s row plus `scope_test.go`'s golden table, `docs/project-layer.md`'s scope table, `website/content/cli-reference.md`'s generator, the release note's `### For operators`, and **one** pinning test — the moment the census moves 101 → 102 (F17, F18) | One release with the keys, the escalation and the console screen together | The first release changes what runs cost before anyone has seen the list in force — and the ask's own premise is that nobody has yet written one. Shipping the reader before the actor is what makes slice 1's `--json` the thing an owner can check their list against | *(empty)* |

## 9. The number block, and how it was read

**Decisions.** This work takes **D1500–D1519**, and reserves nothing above it. Read on 2026-11-07, in
this checkout:

```sh
git fetch origin main                                        # this branch's base
git rev-parse --short origin/main                            # af2644fb
# the highest D number written on main (a decision, not a name)
git grep -ohE '\bD1[0-9]{3}\b' origin/main -- docs/ | sed 's/D//' | sort -n | uniq | tail -1   # 1499
git grep -n 'D149[0-9]' origin/main -- docs/                 # D1490–D1499: console-managed-screens'
                                                             #   reserve row only, no decision
# and nothing in the block this work takes, on main or in any local ref
git grep -lE '\bD15[01][0-9]\b' origin/main -- docs/         # nothing
git grep -ohE '\bD15[0-9]{2}\b' $(git for-each-ref --format='%(refname)') -- docs/  # nothing
```

**The highest D number actually written on `main` is D1489** (`docs/console-managed-screens/`, which
holds D1470–D1489); **D1490–D1499 appear only in that work's own reserve row** ("reserved and unused, so
no second work can take a number adjacent to this one"). **D1500–D1519 is therefore free by two
independent readings** — the highest written decision is D1489 and the reserved ceiling is D1499 — and
this work takes the block immediately above the reserve rather than inside it, which is the same rule
`docs/failure-classes` (`D1450–D1469`) and `docs/periodic-reviews` (`D1430–D1449`) state for their own
blocks.

**Checks.** This work claims **Check 370**, with **371–379** available. Read on 2026-11-07:

```sh
# the highest Check SECTION actually written on main
git grep -ohE '^#+ +Check +[0-9]+' origin/main -- docs/ | grep -oE '[0-9]+' | sort -n | uniq | tail -1  # 291
# the highest Check number cited anywhere on main
git grep -ohE 'Check [0-9]{3}' origin/main -- docs/ | grep -oE '[0-9]+' | sort -n | uniq | tail -1      # 360
# the block this work takes
git grep -ohE 'Check 3[7-9][0-9]' origin/main -- docs/ | sort -u                                        # nothing
```

The highest written section is **291** (`docs/gcp-live-checklist.md`); the highest number **cited** on
`main` is **360**, which is `docs/console-managed-screens/`'s claim (`implementation-plan.md` §6), and
that work's README says "**361–369 are available if a task needs a second check**". **370 is therefore
the first number above every claim and every reserve on `main`**, and this folder takes it as a claimed
block, not a reserved one: like every claim above the highest written section, it is settled at its
merge, and whichever of two claims merges second renumbers itself.

**What this reading could not include.** The ask says "grep docs on main **and every open PR's docs**",
and **this checkout has fetched no pull-request head**: `git for-each-ref 'refs/pull/*/head' | wc -l` is
**0**, and the only refs present are `refs/heads/main`, this branch, `origin/main` and the release tags.
The `docs/console-managed-screens/reconciliation.md` §8 command (`git fetch origin
'+refs/pull/*/head:refs/remotes/pr/*'`) needs the network and did not run here. **A number claimed in an
unmerged pull request and not yet on `main` is therefore not in this reading** — which is exactly why
370 is taken well above the highest claim (360) rather than adjacent to it, and why the two readings
that *are* available (the written section and the cited number) are the ones the block is measured
against.

## 10. Open tensions

Five things this design does **not** resolve, each of which a reader should see rather than discover.

1. **"Security-sensitive" is now two questions, and they can disagree.** The classifier's class decides
   the *model* (`O4`'s tightening-only `decision_model_per_class`, Sensitive never below opus). The
   pattern list decides the *level* (level 3). A path can be Class `Routine` and match a project
   pattern, or Class `Sensitive` and match nothing. **The design keeps them separate on purpose** (D1511)
   because their fail-closed directions differ — every unknown path is Sensitive but not every unknown
   path deserves the adversarial pass — and a reader who expects one word for one idea will find two.
2. **D1505's floor is proposed, not derived.** §6's table is the design's reading of what the tree
   already holds sensitive; nothing in the tree states it as a list, so a category could be argued to
   contain one more or one fewer path. The test the plan proposes
   (`TestFloorIsAtLeastWhatTheRoutingRuleNames`) turns the argument into a build failure, and the owner
   is the one who settles the residue.
3. **The escalation's cost is not measured.** Level 3 is `agent.adversarial_rounds` rounds on
   `runstore.TierAdversarial`. The ask asks for "the cost effect"; the honest answer is that it is the
   adversarial tier's cost times the fraction of runs that match, and **that fraction is unknowable
   before a project writes its list** — which is D1519's reason for shipping the reader before the actor.
4. **A repo-level key and a project-level key can be written by different people.** The project layer is
   the owner's and, on a managed project, the agent's through the gate (D1516); the repo key is the
   repository's, and on a public or a followed repository that may be someone else's pull request. The
   union direction makes the repository's half **monotonically safe** — it can only add — but it also
   means a repository can make its own runs more expensive than its project's policy expects, and
   nothing in this design caps that. A cap (a project maximum on the repo's additions) is **not
   proposed**, and a reader who wants one should say so.
5. **OD1 and OD2 remain unreadable from here** (F15). If either lands with a shape this design assumed
   wrong, D1517's flag or D1519's slicing is what moves — neither is load-bearing for slices 1 and 2.

## 11. UNVERIFIED

- **Every proposal in this file is unverified by construction.** `fugaro security paths`, its `--check`
  and `--json` contract (D1512), the `security` block in either layer (D1501), `security.always_level_3`
  and `--security` (D1517), the floor table (D1505, §6), the record field (D1509) and the escalation
  (D1507) **do not exist**. No such command, key, flag, field or table is in the tree; `git grep` for
  `security-classification`, `always_level_3` and `security paths` returns nothing on `main`.
- **The number reading is `origin/main` only** (§9): no pull-request head is fetched in this checkout,
  so a number claimed in an open pull request is not in the reading. 370 is chosen above the highest
  *cited* number for exactly that reason, and not because the pull-request heads were read.
- **OD1, OD2 and pull request #1822 are not in this tree** (F15). Nothing in this design was checked
  against their actual text, and this folder takes no position on whether they exist at all.
- **`internal/orchestrator/sensitivity`'s intended consumer is not in the tree.** Its package doc names
  "O4's model setting"; `O4` (`docs/stateless-orchestrator/implementation-plan.md`) is also written as
  a plan task, and `git grep` finds no caller. Whether the class reaches a model at all today is
  therefore **UNVERIFIED** — the reading here is that it does not.
- **The floor's membership (§6) is the design's, not the tree's.** Each path in the table is held
  sensitive by a mechanism named in §2, but the table as a *table* is new text, and the categories are
  the routing rule's words rather than the code's.
- **Nothing here was run.** No command in this file was executed against a real installation: the reads
  are read from the code, and the owner-run check (Check 370) is where the behaviour is proved.
