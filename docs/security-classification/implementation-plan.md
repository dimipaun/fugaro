# Security classification: implementation plan

**Status: plan only; nothing is implemented; no task approved.** Every task below is a proposal, every
decision it is written against is **unanswered** ([reconciliation.md](reconciliation.md) §8, D1500–D1519,
each with an empty owner's-answer cell), and the two slices this plan orders are a recommendation
(**D1519**) rather than a schedule. This document adds no code, no schema, no config, no fixture and no
workflow: it names the files the tasks will touch and edits none of them.

**What "parallel by construction" means here.** Every task names **at most six files**. Two kinds of
path are not counted, because they are written but carry no behaviour to review: the `testdata/`
fixtures a refusal needs, which the tree writes **one file per case** (`testdata/config/invalid/` holds
one small YAML per case, `agent-compression-headroom.yaml`, `budget-mode.yaml` and the rest), and the
one **live-check document** an owner-run check writes into (`docs/gcp-live-checklist.md`, §6). On the
first count **SC7** is the task that reaches seven paths, and on the second **SC6** is (**Check 370**'s
section and the register's row). **No level has two writers of one file** — that is the
invariant §1's levels exist to hold, and §2's table is the proof. A task that shares a file with another
task is in a different level, and the table says which level each writer is at.

**The dependency the whole plan hangs on is unanswered.** Every task that reads the list in force
depends on **D1500/D1502/D1505** (the shape, the dialect, the floor); a reader who changes the shape
after reading this plan changes SC1, SC3, SC7 and SC8 together. The plan is therefore written so that
**slice 1 changes no run's cost** (D1519): if the owner rejects D1505's floor, slice 1 is rebuilt and
nothing that ran was affected.

## 1. The order, and what forces it

```
  SC1  the floor, the matcher, the merge      SC2  this folder's pin, the census 101 -> 102
        └────────────────┬───────────────────────────────┘        ← level 0, two independent tasks
                         │
        ┌────────────────┴────────────────┐
      SC3  fugaro security paths        SC4  doctor's line        ← level 1, two independent readers
        │
      SC5  the CLI reference, the release note                    ← level 2
        │
   ┌────┴──────────────────────────────┐
 SC6  the runner's escalation        SC7  the repo key           ← level 3, two independent writers
      and the record field                 │
                                           │
                                         SC8  the project-layer key ← level 4
                                           │
                                         SC9  the two layers, wired ← level 5
                                           │
                                         SC10 the forced level 3    ← level 6
                                           │
                                         SC11 the skill's sentence  ← level 7
```

**Why SC1 and SC2 are level 0 and independent.** SC1 writes the classifier's package
(`internal/orchestrator/sensitivity`); SC2 writes one new pin in `internal/cli` and the census row in
`docs/rigor-modes/`. **They share no file.** SC1 is the engine's list and needs no config key to exist
(D1505); SC2 documents the folder that decided it. Neither is a prerequisite of the other — and SC2 is
the task that moves the census **101 → 102** (F17), which is why it is the *only* task here that touches
a document outside `docs/security-classification/` in the `docs/` tree.

**Why SC3 and SC4 are level 1 and independent.** Both read SC1's resolver and neither writes the
other's file: SC3 is a new command (`internal/cli/security.go`), SC4 is a new doctor check
(`internal/cli/doctor_security.go` plus one registration line in `doctor.go`). A reader who wants the
list in force without a new command can take SC4 alone; a reader who wants the command without doctor
can take SC3 alone.

**Why SC5 is level 2.** `website/content/cli-reference.md` is **generated from the command tree**
(F18), so it cannot name a command the tree does not yet have — SC3 must land first. The release note's
`### For operators` (F13) belongs to the same commit as the reference, because the note is what tells an
operator the command exists.

**Why SC6 and SC7 are level 3 and independent.** SC6 is the runner and the record
(`internal/runner/*`, `internal/runstore/*`); SC7 is the repository's config key (`internal/config/*`,
`schemas/fugaro.schema.json`, `testdata/`, `docs/project-layer.md`). **They share no file**, and each is
useful without the other: SC6 escalates on the **floor alone** (D1505's table) before any project has a
key, and SC7 makes a key exist before anything acts on it (D1519's "the reader before the actor").

**Why SC8 is level 4 though it is "the other half" of SC7.** Both tasks document their key in
`docs/project-layer.md` — SC7 the repository key's row in §5's scope table, SC8 the layer key's own
table — and that is **one file with two writers**, which is exactly what the level exists to separate.
SC8 is the first task that can write the layer's table because SC7's row is already there.

**Why SC9 is level 5.** It is the wiring: the two keys, the floor and the repository's, resolved into
the one list in force — and the three readers of slices 1 and 2 (SC3's command, SC4's check, SC6's
escalation) re-pointed at it. It depends on **SC7 and SC8** for the keys, and on **SC3 and SC6** for the
files it re-points, and it writes one new file (`internal/config/security.go`) — so it is alone at its
level rather than one of a pair.

**Why SC10 is level 6.** The forced level 3 (D1517) reads what SC6 escalates *to* and what SC9 resolves
*into*, so it cannot precede either. It adds a `--security` flag, a task field and
`security.always_level_3` — and it writes `internal/runner/security.go` a second time, which is why it
is not in SC6's level.

**Why SC11 is level 7, and last.** The plugin `working` skill may name `fugaro security paths` **only
once the command exists** (F16: `plugin/skills_lint_test.go` resolves every command a skill names
against the live cobra tree and fails on one the tree lacks), and the sentence it writes is the one
D1518 makes depend on all of the above. It is the last task because it is the one that tells an agent
what to do with everything the other ten built.

**Not in this plan.** The console screen (**D1514**) is deferred by the ask and by the console's own
rule (F24). **No task here builds a screen, and none touches a file under `console/src/`**: the folder's
one `console/` path is SC3's `console/docs/json-contract.md`, which is a **document** — the new §6.x in
its "other documents" family — and it is written because the console reads that contract, not because
this plan gives the console anything to do. The orchestrator's consumer (D1511) is registered as a
dependency of SC1's exported surface and is built by the orchestrator's own plan, not this one.

## 2. Shared files

Every file more than one task here writes, and the level each writer is at. **No level has two writers
of one file** — that is the invariant §1's levels exist to hold, and this table is the proof. A file
written by two tasks appears twice, at two different levels.

| File | Written by (level) | What each writes |
| --- | --- | --- |
| `docs/project-layer.md` | SC7 (3), SC8 (4) | SC7 adds the repository `security` key to §5's scope table; SC8 adds the project layer's `security` block to the layer's own table |
| `internal/cli/security.go` | SC3 (1), SC9 (5) | SC3 writes the command and its document, reading the **floor** (slice 1 has no key to read); SC9 re-points it at the one resolution so a project's rows appear |
| `internal/cli/doctor_security.go` | SC4 (1), SC9 (5) | SC4 writes the check over the floor's counts; SC9 makes it count each source layer |
| `internal/runner/security.go` | SC6 (3), SC9 (5), SC10 (6) | SC6 writes the escalation over the floor; SC9 re-points it at the one resolution; SC10 adds the forced row (`--security`, the task field, `always_level_3`) |

**Every one of those is written at different levels**, which is the whole invariant: the four readers
land against the floor (slices 1 and 2) and **one** task, SC9, is what makes them read the two config
keys (slice 3). A reader who ships slice 1 and stops has a working command, a working doctor check and a
working escalation, all over the engine's own list.

**Files a task shares with work outside this plan**, so a conflict there is a semantic one:
`console/docs/json-contract.md` (SC3's new §6.x, in §6's "other documents" family — the console's own
source cites that contract by section, `console/src/main/ipc.ts` among others, so a renumbered section
is a stale comment in the app),
`internal/cli/doctor.go` and `internal/cli/doctor_render.go` (SC4's one registration line and one ID
prefix, beside eight other check-providing files and the two class tables the whole `doctor` view is
classified by), `internal/cli/run.go` (SC10's flag, beside every other launch flag, whose list
`taskFlags` also pins), `internal/config/config.go` and `internal/config/scope.go` (SC7's key and its
scope row, the two files every config change touches), `internal/runstore/runstore.go` (SC6's record
member, a wire shape the console, mobile and history read — **additive, never a retype**),
`schemas/fugaro.schema.json` and `schemas/project-layer.schema.json` (SC7 and SC8, each pinned to the Go
decoder by `internal/config/schema_test.go` and `schemas/schemas_test.go`),
**`docs/gcp-live-checklist.md`** (SC6's Check 370 section and the register's row, §6 — the same file and
the same two places `docs/console-managed-screens/implementation-plan.md` gives to its own **CM12**, which
writes **Check 360** there, so the register is what a merge conflict in this file is about: the register's
own rule is that a claim is settled at its merge and whichever of two claims merges second renumbers
itself), and `docs/rigor-modes/reconciliation.md` (SC2's census row, which four other folders' pins
re-check — F17).

## 3. The slices

Three slices, from D1519. **Slice 1 changes no run's cost and is safe to ship dormant**; slice 2 is what
makes a match act; slice 3 is what lets a project say what it means. A slice is a release grouping, not
a level: slice 1 spans levels 0–2, slice 2 is level 3, slice 3 is levels 4–7.

| Slice | Tasks | What ships | What it costs a run |
| --- | --- | --- | --- |
| **1 — the floor and the reader** | SC1, SC2, SC3, SC4, SC5 | The engine's categories as a floor, `fugaro security paths --check --json`, doctor's line, the reference and the note | **Nothing.** Nothing reads the list at run time, so no run's level or cost moves |
| **2 — the escalation** | SC6 | The runner escalates to level 3 on a match, and the record says why | `agent.adversarial_rounds` rounds on `runstore.TierAdversarial`, for the runs that match — a fraction unknowable until a project writes a list (§10, tension 3) |
| **3 — the two keys, and the forced row** | SC7, SC8, SC9, SC10, SC11 | The repository key, the project-layer key, the merge that wires them, `--security`/`always_level_3`, and the skill's sentence | Nothing above slice 2's: a key that no run carries changes no run |

## 4. The tasks

### SC1. The floor, the matcher and the merge (F1, F10, F21, F22, F23, D1500, D1502, D1505, D1506, D1511). **SEC — size L**

- **Files:** `internal/orchestrator/sensitivity/pattern.go` (new — `Pattern{Name, Match, Category}`,
  `Match(paths, list)`, `Union(lists ...[]Pattern)`, the three bounds and the load-time validation),
  `internal/orchestrator/sensitivity/pattern_test.go` (new), `internal/orchestrator/sensitivity/floor.go`
  (new — the **43 rows** of reconciliation §6, each a name, one of the seven labels and a doublestar glob,
  and `Floor() []Pattern`),
  `internal/orchestrator/sensitivity/floor_test.go` (new), `internal/orchestrator/sensitivity/export_test.go`
  (the existing export seam gains `Match` and `Union` for the corpus test).
- **Interface:** `func Match(paths []string, list []Pattern) []Pattern` — the rules that matched, so the
  caller can name them; `func Union(lists ...[]Pattern) []Pattern` — deduplicated by `Name`, keeping the
  **first** writer's row for a name written twice, which is the union direction `unionReviewFloor`
  already uses (F23). `Pattern.Match` is a doublestar glob validated by the same four checks
  `rigor.Area.Validate` uses (F21, D1502), bounded at `MaxPatternName` 40, `MaxPatternMatch` 200 and
  `MaxPatterns` 100 — `MaxAreaName`/`MaxAreaPattern`/`MaxAreas`' own numbers, because a pattern reaches
  the same three surfaces a rule name does (`runstore.Record`, the pull request body, `fugaro watch`).
  **A path the matcher cannot place is sensitive**: `Match` reports no rule for it, and the caller asks
  `Classify`, whose answer for an unplaceable path is `Sensitive` (F22) — the one composition that keeps
  the fail-closed direction whole.
- **Red step:** `TestAProjectPatternCannotRemoveAFloorRow` fails to build (package surface absent), then
  fails when `Union` lets a later list drop a floor row of the same name.
- **Tests:** `TestFloorIsAtLeastWhatTheRoutingRuleNames`, which has **two halves and both are needed**:
  it reads `internal/orchestrator/sensitivity/testdata/paths.txt` — the **32 paths across 15 sections**
  the classifier's own routing pin holds — and fails on any path **no floor row matches**; and it walks
  `sensitivity.SensitiveExceptions()` (`Classify`'s own table, 11 rules, one of them the class
  `docs/**/*.graph.json`) and fails on any rule **whose paths no floor row matches**. **A fixture-only
  test would not catch the second**: the published checksums, the install commands and the plan-graph
  artifacts live under `website/` and `docs/` and are in no fixture, so their absence from the floor is
  visible only through the exceptions table. Together the two halves are what make reconciliation §6's
  table checkable rather than a promise, and this test is the one that fails on a floor that omits the
  exceptions. `TestFloorRowsAreNamedAndBounded`,
  `TestAProjectPatternCannotRemoveAFloorRow`,
  `TestUnionKeepsEveryLayersAdditions`, `TestUnionDeduplicatesByNameKeepingTheFirst`,
  `TestPatternMustBeARelativeDoublestarGlob` (the four checks, one case each),
  `TestPatternThatMatchesEverythingIsRefused` (D1503), `TestPatternThatMatchesNothingIsNotRefused`
  (D1503's other half), `TestUnplaceablePathMatchesNoRuleAndClassifiesSensitive`,
  `TestPatternNameReachesTheRecordBounded`, `TestMatchIsCheapOnAPathologicalPattern` (the two-pointer
  match `MatchSegment` documents, so a published pattern cannot make a run quadratic). Command:
  `GOFLAGS=-p=2 go test ./internal/orchestrator/sensitivity -count=1`.
- **Mutation (run, restore):** two. (a) make `Union` return its last list instead of the union:
  `TestAProjectPatternCannotRemoveAFloorRow` and `TestUnionKeepsEveryLayersAdditions` fail. (b) drop the
  eight `artifacts` rows that answer to `sensitiveExceptions` and keep only the fixture's paths: the
  first half of `TestFloorIsAtLeastWhatTheRoutingRuleNames` still passes and its second half fails —
  which is exactly the floor this plan carried before review.
- **Why SEC:** this is the floor of a security control: every later layer is measured against it.
- **Reviewer tier:** security. **Owner-run:** no. **Depends on:** nothing. **Commit:** `feat(sensitivity):
the security-pattern floor, the matcher and the union merge`.

### SC2. This folder's pin, and the census row (F17, F25). **size M**

- **Files:** `internal/cli/docs_security_classification_test.go` (new — the pin for this folder),
  `docs/rigor-modes/reconciliation.md` (the census row **101 → 102**, with the amendment sentence the
  other folders' moves carry), `internal/cli/docs_config_management_test.go`,
  `internal/cli/docs_repo_image_removal_test.go`, `internal/cli/docs_managed_agent_converge_test.go`
  (each re-derives the count and compares it to the row).
- **Interface:** one `TestDocsSecurityClassification…` per claim this folder makes that a test can hold
  without the code existing yet — that [spec.md](spec.md) is the brief verbatim, that
  [reconciliation.md](reconciliation.md) carries twenty rows **D1500–D1519** each with an empty
  owner's-answer cell, that §9's block is above every claim on `main`, and that
  [implementation-plan.md](implementation-plan.md) names each task's files, red step and reviewer tier.
  **Nothing in this task pins a claim about code**, because no code exists: it pins the folder.
- **Red step:** `TestDocsRigorModesCensusStatesItsCommands` (`docs_rigor_modes_test.go`) fails the moment
  the new pin exists and the row still says 101 — which is the order this task writes them in, so the red
  step is the new file added first.
- **Tests:** the new pin's own tests, `TestDocsRigorModesCensusStatesItsCommands`, and the three
  re-checks above. Command: `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Mutation (run, restore):** leave the row at 101 with the new pin present; the census test fails
  naming both numbers.
- **Reviewer tier:** standard. **Owner-run:** no. **Depends on:** nothing.
  **Commit:** `docs(security-classification): the folder's pin, and the census moves to 102`.

### SC3. `fugaro security paths [--check PATH…] [--json]` (D1512). **size M**

- **Files:** `internal/cli/security.go` (new — the command, its reader and its document),
  `internal/cli/security_test.go` (new), `console/docs/json-contract.md` (a new §6.x for the document,
  in the "other documents" family).
- **Interface:** the contract of **D1512**, verbatim: `{project, repo, base, floor_source, security:
  {patterns: [{name, match, category, source}], count}, checked: [{path, sensitive, matched: {name,
  category, source}}], error}`, with `source` ∈ `floor|project|repo`. Without `--check` it prints the
  list in force; with it, an answer per path, and a path the reader cannot place is `sensitive: true`
  (F22). **In slice 1 the list in force is the floor alone** — no key exists yet, which is D1504's own
  answer — and **SC9 re-points this command at the one resolution** so a project's and a repository's
  rows appear under their `source` without the document changing shape. **It reads the base branch, not
  the working copy** (F14, D1518): the list it prints for a repository is the one the next run will use,
  which is `origin/<base>`'s. A refusal names the path, never a value; nothing here is a credential, and
  the document carries no argv.
- **Red step:** `TestSecurityPathsPrintsTheListInForce` fails on today's tree — the command does not
  exist.
- **Tests:** `TestSecurityPathsPrintsTheListInForce`, `TestSecurityPathsCheckAnswersPerPath`,
  `TestSecurityPathsCheckFailsClosedOnAnUnplaceablePath`,
  `TestSecurityPathsNamesTheSourceLayerOfEveryRow`, `TestSecurityPathsReadsTheBaseAndNotTheWorktree`,
  `TestSecurityPathsNamesItsFieldsInTheContract`,
  `TestSecurityPathsOnARepositoryWithNoKeyIsTheFloor`. Command:
  `GOFLAGS=-p=2 go test ./internal/cli -run 'SecurityPaths|Docs' -count=1`.
- **Mutation (run, restore):** print the working copy's list instead of the base's;
  `TestSecurityPathsReadsTheBaseAndNotTheWorktree` fails.
- **Why SEC:** it is the reader an agent calls instead of guessing, and D1518's base-branch property is
  the whole of its safety.
- **Reviewer tier:** security. **Owner-run:** no. **Depends on:** SC1.
  **Commit:** `cli: fugaro security paths answers what this project calls security-sensitive`.

### SC4. doctor's line (F11, D1513). **size S**

- **Files:** `internal/cli/doctor_security.go` (new — the check, following
  `doctor_managed_audit.go`'s `doctorManagedAudit`), `internal/cli/doctor_security_test.go` (new),
  `internal/cli/doctor_render.go` (`doctorSecurityPrefixes` gains the check's ID prefix, so
  `fugaro doctor --security` carries it), `internal/cli/doctor.go` (one registration).
- **Interface:** one `doctorCheck` whose `problem`/`fix` name the list in force — how many rows come
  from the floor and how many from each layer — and, for **D1503**'s second half, one row for a pattern
  that matched nothing in the base. **In slice 1 there are no layers, so it counts the floor alone**;
  **SC9 re-points it at the one resolution** so the two layer counts appear. It prints **counts and
  names, never a value**. Its class follows `doctorClassOf`: an **info** line where the list is only
  measured (a `measure`, so the default view does not print it and `--security` does, which is why the
  ID joins `doctorSecurityPrefixes`), and a **finding** (`actionable`, and the reader's to fix) where a
  pattern matched nothing in the base — because a pattern that matches nothing is a list that does not
  do what its author thinks, which is the one thing this check exists to say.
- **Red step:** `TestDoctorNamesTheSecurityListInForce` fails on today's tree — the check does not exist.
- **Tests:** `TestDoctorNamesTheSecurityListInForce`,
  `TestDoctorSaysWhenAPatternMatchedNothingInTheBase`,
  `TestDoctorSecurityCheckIsAMeasureWhenTheListIsSound`,
  `TestDoctorSecurityCheckIsAFindingWhenAPatternMatchedNothing`,
  `TestDoctorSecurityCheckIsOnTheSecuritySweep`, `TestDoctorSecurityCheckAppearsInJSON`. Command:
  `GOFLAGS=-p=2 go test ./internal/cli -run 'Doctor.*Security|Docs' -count=1`.
- **Mutation (run, restore):** drop the "matched nothing" row; its case fails.
- **Reviewer tier:** standard. **Owner-run:** no. **Depends on:** SC1.
  **Commit:** `cli: doctor says which security patterns are in force`.

### SC5. The CLI reference, and the release note (F13, F18). **size S**

- **Files:** `website/content/cli-reference.md` (regenerated with **`go run ./ee/clidoc`**, the generator
  the file's own header and `docs_website_cliref_test.go` name — `internal/clidoc` no longer writes this
  page at all, F18),
  `docs/releases/vX.Y.Z.md` (the release that ships slice 1), `website/data/release-summaries.yaml`
  (the one line that release's README requires, no backticks).
- **Interface:** the reference's `security paths` section, generated and not hand-written; the note's
  `### For operators` entry, which for slice 1 is the honest one — **nothing to run**: the command is a
  reader and no run's cost changes (D1519). It is written even when it is one sentence, because F13's
  rule is that a release whose operator story is "nothing" says so rather than leaving the section out.
- **Red step:** `TestCliReferenceNamesEveryCommandAndFlag` (`docs_website_cliref_test.go`, F18) fails
  while the tree has `security paths` and the reference does not — the red step is SC3 landing first.
- **Tests:** `go test ./internal/cli -run 'CliReference|Docs' -count=1` and
  `cd website && npm run check`.
- **Mutation (run, restore):** remove the generated section; the reference pin fails.
- **Reviewer tier:** standard. **Owner-run:** no. **Depends on:** SC3.
  **Commit:** `docs: the CLI reference and the release note for the security reader`.

### SC6. The runner escalates a matching diff to level 3, and the record says why (F5, F7, F9, D1507, D1508, D1509). **SEC — size L**

- **Files:** `internal/runner/security.go` (new — read the changed paths, classify them, re-resolve the
  levels, rebuild the plan), `internal/runner/security_test.go` (new), `internal/runner/runner.go` (the
  two calls — the post-`implement` window and the post-`fix` seam beside `roundRigor`),
  `internal/runner/plan.go` (a re-resolution beside `(*run).resolveLevels`),
  `internal/runstore/security.go` (new — the record's member and its constructor),
  `internal/runstore/runstore.go` (the field on `Record`) — plus the live-check document this task's
  owner-run check writes into, which the header's bound does not count: **`docs/gcp-live-checklist.md`
  (Check 370's own section, and the register's row, §6)**, and it is this task's because a task that
  lands a check without the behaviour is a check nobody can run.
- **Interface:** the escalation reads the diff the way `roundRigor` already does —
  **`(*Repo).ChangedFiles(ctx, r.workBase())`** (`internal/gitops/changedfiles.go`, the read in
  `internal/runner/rigor.go`) — and **not** through `runstore.Record.ChangedFiles`. That field is written
  by `(*run).recordChangedFiles`, whose every call site is inside `finalize` and `shadowFinalize`, so at
  the post-`implement` moment it is still nil: an escalation built on it would classify an empty list and
  **never fire** (F5). The first seam is the post-`implement` window D1507 names
  (`afterStage(ctx, "implement")`); the second is the post-`fix` seam `(*run).fix` already re-reads the
  diff at — `afterStage(ctx, "fix")` and then `roundRigor` — which is the precedent for **the read**, not
  for the moment (F9): a fix round can add a path the implement round did not touch, and the mode's own
  re-resolution is what makes that a moment the run already pays for.
  On **any** match the runner sets **level 3 on** for this run, re-resolves the levels through
  `config.ResolveLevels` and **rebuilds the plan** so the `adversarial` step is in it.
  **Level 3 only, and the rigor mode does not move** (D1508). **In slice 2 the list in force is the
  floor alone** — SC9 re-points it at the one resolution, and the record member it names does not change
  shape when it does. **It never fails the run**: a read that cannot be made is a warning and the levels
  stay where they are, which is `roundRigor`'s own rule for the mode (F9). The record gains an additive member naming the escalation, the matched pattern's
  `name` and `category`, and the moment — no existing field changes meaning, so no version bumps
  (D1509). A run whose `--review-levels` block already carries level 3 records **no** escalation, because
  none happened.
- **Red step:** `TestRunEscalatesToLevelThreeOnAMatchingDiff` fails on today's tree — a diff touching a
  floor path runs the mode's levels and no more.
- **Tests:** `TestRunEscalatesToLevelThreeOnAMatchingDiff`,
  `TestRunEscalationLeavesTheRigorModeAlone` (the mode before and after are equal),
  `TestRunRecordsThePatternThatEscalatedIt`,
  `TestRunWithLevelThreeAlreadyAskedRecordsNoEscalation`,
  `TestRunEscalationSurvivesAUnitThatChangedNothing` (a matching diff then an empty one),
  `TestRunEscalationIsIdempotentAcrossRoundRigor`,
  `TestRunEscalationReadsTheDiffBeforeTheRecordHasIt` (the escalation fires at the post-`implement` seam
  **while `Record.ChangedFiles` is still nil** — the test that fails on an escalation built from the
  record rather than from the repository, and the reason this task's interface names the read),
  `TestRunEscalationWarnsAndKeepsTheLevelsWhenTheReadFails`,
  `TestRunEscalationFiresOnAPathOnlyALaterFixAdded`,
  `TestRunRecordGainsTheFieldAdditively` (the round-trip keeps every existing member). Command:
  `GOFLAGS=-p=2 go test ./internal/runner -run 'Security|Escalat' -count=1` (the runner needs
  `-timeout 40m` for the whole package, not for this filter).
- **Mutation (run, restore):** two, and the second is the one this interface exists to prevent. (a)
  escalate on the diff but do not rebuild the plan: `TestRunEscalatesToLevelThreeOnAMatchingDiff` fails,
  because the step is not in the plan. (b) classify `r.rec.ChangedFiles` instead of
  `r.repo.ChangedFiles(ctx, r.workBase())`: `TestRunEscalationReadsTheDiffBeforeTheRecordHasIt` fails,
  because the record is still empty at that moment — the escalation silently never fires.
- **Why SEC:** it changes what a run spends and what its review is, off a control read from the base.
- **Reviewer tier:** security. **Owner-run:** **yes** — the escalation is only true on a real run, and
  Check 370 is where the owner sees it (D1519's reason for shipping slice 1 first).
  **Depends on:** SC1. **Commit:** `feat(runner): a matching diff escalates the run to level 3`.

### SC7. The repository key, `security` (F3, D1501, D1502, D1506). **SEC — size M**

- **Files:** five — `internal/config/config.go` (the field), `internal/config/scope.go` (its `ScopeRow`),
  `internal/config/scope_test.go` (the golden `want` table), `schemas/fugaro.schema.json` (the property
  and its `$defs`), `docs/project-layer.md` (the row in §5's scope table) — **plus the fixture pair** the
  header's bound does not count, one file per case as the tree writes them:
  `testdata/config/valid/security.yaml` (new) and `testdata/config/invalid/security.yaml` (new).
- **Interface:** `security:` on `config.Config`, the **same** shape the layer will carry (D1500): a list
  of `{name, match, category}`, decoded strictly, each row validated by SC1's `Pattern` rules — **so a
  pattern the matcher cannot read is a configuration error at load, not a rule that silently never
  fires** (F21). `category` is the closed **seven-value** vocabulary of reconciliation §6 (the ask's six plus
  `artifacts`, D1500). `name` is bounded
  and closed by the same character set as `rigor.Area`'s (F21).
- **Red step:** `TestScopesMatchDesignDoc` (`internal/config/scope_test.go`) fails the moment the field
  exists and the golden table has no row for it — the tree's own order, so the red step is the field
  added first.
- **Tests:** `TestSecurityKeyDecodesStrictly`, `TestSecurityKeyRefusesAUnknownCategory`,
  `TestSecurityKeyRefusesAMatchTheMatcherCannotRead`,
  `TestSecurityKeyRefusesAPatternThatMatchesEverything`,
  `TestSecurityKeySchemaAgreesWithTheDecoder` (both surfaces on the same text, the pattern
  `internal/config/schema_test.go` already uses), `TestScopeTableMatchesDocs` (the
  `docs_website…`-style pin in `internal/config/docs_scope_test.go`). Command:
  `GOFLAGS=-p=2 go test ./internal/config -run 'Security|Scope' -count=1 && go test ./schemas -count=1`.
- **Mutation (run, restore):** add the field to the struct and to the decoder but not to the schema;
  `TestSecurityKeySchemaAgreesWithTheDecoder` fails.
- **Why SEC:** a repository writes this list, and the list decides what its own runs review.
- **Reviewer tier:** security. **Owner-run:** no. **Depends on:** SC1.
  **Commit:** `feat(config): the repository says what counts as security-sensitive`.

### SC8. The project-layer key, `security` (F3, F4, D1501, D1506). **SEC — size M**

- **Files:** `internal/config/layer.go` (`layerTopKeys` gains `security`, and the block's strict decode
  beside the existing scope checks), `internal/config/layer_test.go`,
  `schemas/project-layer.schema.json` (the property and its `$defs`),
  `testdata/project-layer/valid/security.yaml` (new), `docs/project-layer.md` (the layer's own table).
- **Interface:** `security:` on `config.ProjectLayer`, the shape SC7 defines, with the same validation
  and the same closed category vocabulary — **plus** D1517's `always_level_3`, which is the only
  non-pattern member and is what D1503's refused `**` pattern points at instead. It is **additive**:
  an optional key, so no policy version bumps (F13's own precedent, `orchestrator:`).
- **Red step:** `TestProjectLayerSecurityDecodesStrictly` fails to build, then fails when the layer
  accepts an unknown category.
- **Tests:** `TestProjectLayerSecurityDecodesStrictly`,
  `TestProjectLayerSecurityIsInTheTopKeyAllowList` (`layerTopKeys` — a key not on it is refused with the
  list printed, which is what makes the allow-list a fence),
  `TestProjectLayerSecuritySchemaAgreesWithTheLayerParser`,
  `TestProjectLayerCorpus` (the new fixtures), `TestScopeTableMatchesDocs`. Command:
  `GOFLAGS=-p=2 go test ./internal/config -run 'Layer.*Security|Scope|Corpus' -count=1 && go test ./schemas -count=1`.
- **Mutation (run, restore):** add the key to the parser and the schema but not to `layerTopKeys`; the
  allow-list test fails, naming the list.
- **Why SEC:** the project layer is the shared, published half of the control.
- **Reviewer tier:** security. **Owner-run:** no. **Depends on:** SC7.
  **Commit:** `feat(config): the project layer says what counts as security-sensitive`.

### SC9. The two layers, wired (F10, F23, D1506, D1511). **size M**

- **Files:** `internal/config/security.go` (new — the resolution of the floor, the layer and the
  repository into one list, with the sources), `internal/config/security_test.go` (new),
  `internal/cli/security.go` and `internal/cli/doctor_security.go` (both re-pointed at the one
  resolution), `internal/runner/security.go` (re-pointed the same way).
- **Interface:** `func SecurityPatterns(c *Config, layer *ProjectLayer) []sensitivity.Pattern` — one
  function that returns the list in force with each row's `source`, by `sensitivity.Union(Floor(),
  layer.security, repo.security)` (D1506). **Union, never replacement**: a repository with no key gets
  the layer's and the floor's; a project with no key gets the floor; both empty gets the floor; and a
  looser row is recorded the way `Effective.Ignored` records one (F10). **This is the single function
  every reader calls** — SC3's command, SC4's check and SC6's escalation are all re-pointed at it in
  this task, which is D1511's "one source of truth" made into one exported name rather than a promise.
- **Red step:** `TestRepoKeyCannotRemoveAProjectRow` fails to build, then fails when the resolution lets
  the repo's list replace the layer's.
- **Tests:** `TestRepoKeyCannotRemoveAProjectRow`, `TestEmptyRepoListIsTheProjectListAndTheFloor`,
  `TestEmptyProjectListIsTheFloor`, `TestBothEmptyIsTheFloor`,
  `TestResolutionNamesEachRowsSource`, `TestResolutionIsTheSameFunctionEveryReaderCalls`
  (a reflective check that `internal/cli` and `internal/runner` both reach this one exported name, and
  that no reader still calls `sensitivity.Floor()` directly). Command:
  `GOFLAGS=-p=2 go test ./internal/config -run Security -count=1 && go test ./internal/cli ./internal/runner -run Security -count=1`.
- **Mutation (run, restore):** resolve the repository's list alone when it is non-empty; the first two
  cases fail.
- **Reviewer tier:** security. **Owner-run:** no. **Depends on:** SC1, SC3, SC6, SC7, SC8.
  **Commit:** `feat(config): the floor, the project and the repository resolve into one list`.

### SC10. The forced level 3: `--security`, the task field, `always_level_3` (D1517, D1503). **size M**

- **Files:** `internal/task/task.go` (the `Security` field and its validate), `internal/task/security_test.go`
  (new), `internal/cli/run.go` (`--security`, and `taskFlags`), `internal/runner/security.go` (the
  forced row, beside SC6's), `schemas/task.schema.json` (the field).
- **Interface:** a task says `security: true` (or a launch passes `--security`) and the run's level 3 is
  **on**, whatever the paths are — **raise-only**, so it needs no fence and no allow-list, exactly as
  `--mode` already raises (F9). And the project layer's `always_level_3` (SC8) turns it on for every run
  of that project. **A task that says `security: false` changes nothing**: there is no way to ask for
  less (D1517's whole point), and the field is a **bool that only turns on**, so a false is the floor's
  answer and not an exemption.
- **Red step:** `TestTaskSecurityFieldRaisesTheLevel` fails on today's tree — the field does not exist.
- **Tests:** `TestTaskSecurityFieldRaisesTheLevel`, `TestRunSecurityFlagRaisesTheLevel`,
  `TestSecurityFieldCannotLowerTheLevel` (a task with `security: false` on a matching diff still
  escalates), `TestAlwaysLevelThreeRaisesEveryRun`,
  `TestTaskSchemaAgreesWithTheTaskDecoder`. Command:
  `GOFLAGS=-p=2 go test ./internal/task ./internal/cli ./internal/runner -run 'Security' -count=1 && go test ./schemas -count=1`.
- **Mutation (run, restore):** let `security: false` suppress SC6's escalation; the third case fails.
- **Reviewer tier:** security. **Owner-run:** no. **Depends on:** SC6, SC9.
  **Commit:** `feat(runner): a task or a project can force level 3`.

### SC11. The `working` skill's sentence (F16, D1518). **size S**

- **Files:** `plugin/skills/working/reference/launch.md` (the sentence, beside the level-3 paragraph
  already there), `plugin/skills/working/SKILL.md` (the pointer, if the reference's line needs one).
- **Interface:** the sentence an agent reads instead of guessing: **what this project calls
  security-sensitive is `fugaro security paths --json`'s answer, not the agent's judgement**, and a task
  that is security-sensitive by nature says so with `--security` (D1517). It names the command, its
  `--json` flag and `--check`, and nothing else — **every command and flag it names is checked against
  the live cobra tree** (F16), so the sentence cannot outrun the CLI.
- **Red step:** `TestPluginSkillsLintClean` (`plugin/skills_lint_test.go`) fails while the skill names
  `fugaro security paths` and the tree lacks it — which is why this task is last (§1).
- **Tests:** `TestPluginSkillsLintClean`, `TestSkillFilesQuoteCommands`. Command:
  `go test ./plugin -count=1`.
- **Mutation (run, restore):** name `fugaro security pattern` (a command that does not exist); the lint
  fails naming it.
- **Reviewer tier:** standard. **Owner-run:** no. **Depends on:** SC9, SC10.
  **Commit:** `plugin(working): the security list is a command, not a guess`.

## 5. The levels, and the critical path

| Level | Tasks | What makes them parallel |
| --- | --- | --- |
| **0** | SC1, SC2 | The classifier's package and one new pin plus the census row: no shared file, no dependency |
| **1** | SC3, SC4 | A new command and a new doctor check: both read SC1, neither writes the other's file |
| **2** | SC5 | Generated from SC3's command tree, so it waits for it |
| **3** | SC6, SC7 | The runner/record and the repository key: no shared file, each useful without the other |
| **4** | SC8 | The layer key documents itself in `docs/project-layer.md`, which SC7 already wrote |
| **5** | SC9 | The wiring needs both keys and the floor |
| **6** | SC10 | The forced row needs what SC6 escalates to and what SC9 resolves into |
| **7** | SC11 | The skill may name the command only once it exists |

**Critical path: SC1 → SC7 → SC8 → SC9 → SC10 → SC11** — **six tasks and five edges**, and six is the
most a chain can be when the plan has eight levels and its first holds two tasks side by side. The
other branch (SC1 → SC3 → SC5, and SC1 → SC6) is shorter and is what makes the plan two-wide at
**three** of its eight levels: a pair at levels 0, 1 and 3 (six tasks), and a single task at 2, 4, 5, 6
and 7 (five) — eleven in all.

**Two long tasks carry the risk**, and both are **SEC**: SC1 (the floor every layer is measured against)
and SC6 (the escalation, which changes what a run spends). They are not on the same branch of the path,
which is deliberate — a reviewer can reject D1505's floor without holding up the escalation's seam, and
can reject D1508's level choice without rebuilding the floor.

**Where a slice boundary is also a level boundary.** Slice 1 ends after level 2, slice 2 is level 3's
SC6, and slice 3 is levels 4–7 with SC7. The boundaries are not levels, but each is **after a level and
not inside one** — so a slice can be cut without splitting a level's pair, which is what "parallel by
construction" has to mean for a plan that ships in stages.

## 6. The owner-run live check: Check 370 (D1519, F19)

Claimed in [reconciliation.md](reconciliation.md) §9, above every claim on `main` (the highest `## Check`
section written is 291, the highest number cited is 360). It is **owner-run** because two of its three
parts are only true on a real installation, and one of them spends real money:

1. **The reader, on a project with no key.** `fugaro security paths --json` prints the floor's 43 rows.
   `--check internal/creds/state.go` answers `sensitive: true` from the `credentials` floor row, and
   `--check docs/recipes.md` answers `sensitive: false` — the pair that shows the floor is a **positive
   list and not the classifier's complement** (F1, F22): `docs/` is a routine tree, and only the two
   `docs/` rows the routing text's own exceptions name are on the floor. The interesting case in
   between is `--check docs/pending-workflows/console-tests.yml`, which answers `true` from
   `pending-workflows`: a routine tree with a listed exception, which is the shape a project's own list
   will have.
2. **The escalation, on a real run.** A run whose diff touches a floor path **runs the adversarial step**
   and its record names the pattern and the category; a run whose diff touches nothing on the list does
   not, and its record carries no escalation member. **This is the part that costs money and the part
   no test can prove.**
3. **The base-branch property.** With a working tree whose `fugaro.yaml` carries a `security` row and a
   base that does not, `fugaro security paths --json` prints the **base's** list — the property D1518
   exists for, and the one a reviewer cannot check by reading.

**The file this task writes** is `docs/gcp-live-checklist.md`'s Check 370 section and its register row,
and in §4 above it sits in **SC6**'s file list — the task that proves the behaviour, because a task that
lands a check without it is a check nobody can run. §2 carries it as one of the files shared with work
outside this plan: `docs/console-managed-screens/implementation-plan.md` gives the same file and the same
two places to its own **CM12** for **Check 360**, so the register's rule (a claim above the highest
written section is settled at its merge, and the second of two claims renumbers itself) is what makes 370
this work's to claim now and to prove later.

## 7. What is deliberately not in this plan

- **The console screen** (D1514). Deferred by the ask and by the console's own rule (F24); no task here
  names `consoleReadPaths`, `spawn.ts`, `ipc.ts` or any other file under `console/src/`. The one
  `console/` path in this plan is SC3's `console/docs/json-contract.md`, a document the console's own
  code cites (§6.12, §6.22 and others in `console/src/main/ipc.ts`), not a screen.
- **The orchestrator's consumer** (D1511). SC1 exports the surface O1's `O4` consumer would read; the
  orchestrator's own plan builds it, and this plan does not wait for it.
- **`settings set` for the list** (D1515). The list is edited in the layer file and the repository's
  `fugaro.yaml`, published by `config publish`; the managed allow-list admits nothing new.
- **OD1, OD2, the needs_owner work** (F15). None is in this tree; D1518 makes the design independent of
  them, and SC11 is the only task whose *content* depends on OD2's intent.
- **A publish or a migration of its own** (F13). The key rides `fugaro update`'s existing stages,
  including `policyMigrateStage`; an additive optional key means no migrator and no policy version bump.
- **Any new dependency.** The matcher is `doublestar`, already imported (F21).
