# Post-review triage: implementation plan

Builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md) (it wins where they differ).

## Status

**Plan only. Nothing is implemented. No task is approved:** each task needs the owner's explicit go; an answer to a
decision is not a go. The owner's answers that shaped this plan, all 2026-11-08: D1800 (an agent inside the floor),
D1803 (record, then a CLI files tasks), D1805 (follow BF1m at a cap), D1807 (**all levels, not the recommendation**),
D1808 (the senior reviewer's model), D1809 (a CLI command launches follow-ups), D1811 (Startup, Pro, Hotfix),
D1813 (BF1m first, triage on top).

**External prerequisite: BF1m (#1999) merged.** It edits `internal/rigor/table.go`, `internal/runner/outcome.go`,
`runner.go`, `prflow.go`, `report.go`, `budget.go`, `internal/config/*` and the result and fugaro schemas, so every
task here that writes one of those files waits for it (D1813). Tasks that touch none of them start at once.

## Conventions every task follows

- `CLAUDE.md` and the rules files each area loads: `.claude/rules/credentials-service.md` does not apply (no credential
  is read or written); `internal/runner` tasks touch the agent's prompts and environment, which `CLAUDE.md` and
  `.fugaro/review.md` treat as security paths: those tasks are `opus + mandatory independent review`, level 3, with a
  mutation check run and restored.
- Targeted tests only, `GOFLAGS=-p=2`; never a whole `internal/cli` or `internal/runner` package. Hermetic: no network,
  no home directory, no real cloud (`internal/testutil`, `runnertest`, `t.TempDir()`). No secret-shaped literal.
- A field never changes meaning: new names only (D1810). Config and wire formats decode strictly and have schemas.
- Docs are tested: a doc change runs `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1`; keep every pinned fact.
- A fix round adds no functionality; a finding that needs a decision is reported, not fixed.
- Never edit `.github/workflows/`; stage under `docs/pending-workflows/` if ever needed (none is here).
- No live cloud action by an agent. The live checks below are owner-run scripts with a hermetic fake-gcloud test.
- A change to a documented command changes `plugin/skills` in the same PR (T-TR12), and the CLI reference is regenerated.

## Global constraints and review focus

| Constraint (finding or decision) | Pinned by |
| --- | --- |
| A judge can only defer among findings the table blocks; floor classes (security, data_loss, impact 5, `needs_owner`) are never deferred, in any mode, including level 3 (F3, D1800, D1807) | T-TR6, T-TR7b (mutation check: remove the floor and the test fails) |
| Triage failure, timeout or an unparseable answer blocks every finding (D1806, F18) | T-TR6, T-TR7a |
| An unconfigured repository never defers; the base branch decides (F14, D1812) | T-TR7b |
| POC and Critical are byte-identical to today: no triage call, no new record field written (D1811) | T-TR7a, T-TR7b |
| The persona is chosen from the mode reached at that round, never the starting mode (F14) | T-TR7a |
| The fixer sees only the blocking findings and is told to touch nothing else (F4, F16, acceptance 2) | T-TR7b |
| Reviewer text is untrusted data; a verdict naming no known finding is dropped; `needs_owner` is never flipped (D1816, F15) | T-TR2, T-TR6 |
| `tolerated` keeps its meaning; verdicts live under new names (F10, D1810) | T-TR3 |
| The project layer may turn triage off, a repository may not turn it back on (D1814, F17) | T-TR5 |
| A job writes only its result record: follow-ups are created by the CLI, never from inside a job (F6, D1803) | T-TR10 |
| A follow-up never lowers a floor and carries the parent's applied mode (D1804) | T-TR10 |
| A cap hit: Startup and POC finalize ready with leftovers deferred; Pro and above stay draft, non-floor leftovers still recorded (D1805) | T-TR8 |
| "Ready" is never "merged" (F8, D1802) | T-TR9, T-TR12 |

## Slice 1: the shapes, inert (nothing reads them yet)

### T-TR1: the floor as one predicate
- **Files:** `internal/rigor/floor.go` (new), `internal/rigor/floor_test.go` (new). **Interfaces:** consumed `rigor.Score`,
  the class constants, `overrideCatastrophic` and `overrideSecurity` from `internal/rigor/table.go` (read, not edited);
  produced `Floor(m Mode, s Score) bool`: true when `Blocks` holds for a reason the judge may not change (class
  `security`; `data_loss` or impact 5 outside Hotfix; an unscored finding). `Blocks` itself is unchanged and
  `Floor(m, s)` implies `Blocks(m, s)`. **Schema/rules/config change:** none.
- **Tests:** `TestFloorMatchesBlocksOverrides`, `TestFloorNeverTrueWhenTableTolerates`, `TestFloorHotfixKeepsSecurityOnly`,
  `TestFloorUnscoredFinding`. **Command:** `GOFLAGS=-p=2 go test ./internal/rigor/ -run 'TestFloor' -count=1`.
- **Depends on:** none. **Red step:** `TestFloorHotfixKeepsSecurityOnly` fails: no `Floor`. **Reviewer tier:** opus +
  mandatory independent review (it is the floor). **OWNER-RUN:** no. **Commit:** `rigor: the floor as one predicate the
  triage judge may not move`.

### T-TR2: the triage verdict, its schema, the personas and the prompt
- **Files:** `internal/triage/verdict.go` (new), `internal/triage/prompt.go` (new), `internal/triage/personas/startup.md`,
  `internal/triage/personas/pro.md`, `internal/triage/personas/hotfix.md` (new, embedded), `internal/triage/verdict_test.go`
  (new), `internal/triage/prompt_test.go` (new), `schemas/triage.schema.json` (new). **Interfaces:** consumed nothing from
  the runner; produced `Verdict` (`version`, `findings[]` of `{index, key, verdict: block|defer, reason, group}`),
  `Decode([]byte) (Verdict, error)` strict (unknown fields refused, a verdict other than the two refused, a reason longer
  than 200 bytes refused, no newline in a reason), `Prompt(mode rigor.Mode, findings []Finding) string` (the question
  "Would a [persona] hold this merge for this?", the findings fenced as data, the floor stated as binding), and the three
  embedded persona files (one per level, LD10; not overridable).
- **Tests:** `TestDecodeStrict`, `TestDecodeRefusesUnknownVerdict`, `TestPromptFencesFindingsAsData`,
  `TestPromptNamesThePersonaOfTheMode`, `TestPersonasAreEmbedded`, `TestSchemaAcceptsADecodedVerdict`.
  **Command:** `GOFLAGS=-p=2 go test ./internal/triage/ -run 'TestDecode|TestPrompt|TestPersonas|TestSchema' -count=1`.
- **Depends on:** none. **Red step:** `TestPromptFencesFindingsAsData` fails: a finding containing a fence and an
  instruction reaches the prompt as an instruction. **Reviewer tier:** opus + mandatory independent review (an agent
  prompt). **OWNER-RUN:** no. **Commit:** `triage: the verdict, its schema, the three personas and the prompt`.

### T-TR3: the result record's triage block
- **Files:** `internal/runstore/runstore.go`, `internal/runstore/runstore_test.go`, `schemas/result.schema.json`,
  `internal/runner/testdata/result_triage.json` (new). **Interfaces:** consumed nothing; produced an optional
  `Record.Triage` (`rounds[]` of `{round, mode, level, account, model, failed bool, entries[]}`; an entry is `{index, key,
  verdict, reason, group, source}` with `source` in `agent|table|fail_safe|cap`) and `Record.Deferred` (the deferred
  findings with the same keys, ready for a follow-up). **Schema change:** `version` stays 1; the fields are new and
  optional, `tolerated` is untouched.
- **Tests:** `TestRecordWithoutTriageIsByteIdentical`, `TestTriageBlockRoundTrips`, `TestResultSchemaAcceptsTriage`,
  `TestToleratedKeepsItsMeaning`. **Command:** `GOFLAGS=-p=2 go test ./internal/runstore/ -run 'Triage|ToleratedKeeps' -count=1`
  and `GOFLAGS=-p=2 go test ./internal/runner/ -run 'TestResultSchema' -count=1`.
- **Depends on:** none, **but BF1m (#1999) edits `schemas/result.schema.json` and `runstore.go`: this task waits for it.**
  **Red step:** `TestRecordWithoutTriageIsByteIdentical` passes only once the fields are omitted when empty.
  **Reviewer tier:** standard. **OWNER-RUN:** no. **Commit:** `runstore: an optional triage block and the deferred findings`.

### T-TR4: the task's follow-up fields
- **Files:** `internal/task/task.go`, `internal/task/task_test.go`, `schemas/task.schema.json`. **Interfaces:** produced
  optional `Task.FollowUpOf` (`{run, keys[]}`) and `Task.Debt bool`; consumed nothing. **Schema change:** `version` stays 1
  (fields added, none removed or retyped); strict decode keeps refusing unknown fields.
- **Tests:** `TestTaskWithoutFollowUpIsByteIdentical`, `TestFollowUpOfRoundTrips`, `TestFollowUpOfRefusesAnEmptyRun`,
  `TestTaskSchemaAcceptsFollowUp`. **Command:** `GOFLAGS=-p=2 go test ./internal/task/ -run 'FollowUp' -count=1`.
- **Depends on:** none. **Red step:** `TestFollowUpOfRoundTrips` fails: no field. **Reviewer tier:** standard (the task is
  the wire shape the runner reads; a job does not write it). **OWNER-RUN:** no. **Commit:** `task: follow_up_of and debt,
  both optional`.

## Slice 2: the stage, in shadow first, then enforcing

### T-TR5: the `triage` stage name and its switch
- **Files:** `internal/config/stage_accounts.go`, `internal/config/stage_accounts_test.go`, `internal/config/config.go`,
  `internal/config/layer.go`, `internal/policy/merge.go`, `internal/policy/merge_test.go`, `schemas/fugaro.schema.json`,
  `schemas/project-layer.schema.json`. **Interfaces:** consumed `RunStages`, `MixedAccountStages`; produced `triage` in both
  lists, `agent.triage` (bool, default on in Startup, Pro and Hotfix, absent elsewhere), and a merge rule where a layer
  that turns it off wins and a lower layer cannot turn it back on.
- **Tests:** `TestTriageIsARunStage`, `TestTriageIsAMixedAccountStage`, `TestAgentTriageOffWinsOverALowerOn`,
  `TestAgentTriageDefaultsOnOnlyInTheThreeModes`, `TestConfigRefusesUnknownTriageKey`, `TestSchemasCarryTheKey`.
  **Command:** `GOFLAGS=-p=2 go test ./internal/config/ ./internal/policy/ -run 'Triage' -count=1`.
- **Depends on:** **BF1m (external, edits `internal/config/*` and the schemas).** **Red step:** `TestAgentTriageOffWinsOverALowerOn`
  fails: a repository file re-enables a project-disabled switch. **Reviewer tier:** opus + mandatory independent review
  (config surface, tightening). **OWNER-RUN:** no. **Commit:** `config: the triage stage and agent.triage, tightening only`.

### T-TR6: applying a verdict inside the floor
- **Files:** `internal/triage/apply.go` (new), `internal/triage/apply_test.go` (new). **Interfaces:** consumed T-TR1's `Floor`,
  `rigor.Split`, T-TR2's `Verdict`; produced `Apply(m rigor.Mode, scored []rigor.Score, v Verdict, ok bool) Outcome` with
  `Outcome{Blocking, Deferred []Entry}` where the table-tolerated findings are `Deferred` with `source: table`, a floor
  finding is `Blocking` whatever the verdict, a verdict naming no known finding is dropped, a missing verdict for a
  table-blocked finding blocks it, and `ok == false` (failed or unparseable) blocks every table-blocked finding with
  `source: fail_safe`.
- **Tests:** `TestApplyNeverDefersAFloorFinding`, `TestApplyDropsAVerdictForAnUnknownFinding`, `TestApplyMissingVerdictBlocks`,
  `TestApplyFailSafeBlocksAll`, `TestApplyTableToleratedStaysDeferred`, `TestApplyNeedsOwnerIsNotInTheJudgesReach`.
  **Command:** `GOFLAGS=-p=2 go test ./internal/triage/ -run 'TestApply' -count=1`.
- **Depends on:** T-TR1, T-TR2. **Red step:** `TestApplyNeverDefersAFloorFinding`: an agent verdict deferring a `security`
  finding is accepted. **Mutation (run, restore):** drop the `Floor` check; that test fails. **Reviewer tier:** opus +
  mandatory independent review. **OWNER-RUN:** no. **Commit:** `triage: apply a verdict inside the floor, failing safe`.

### T-TR7a: the stage runs and is logged, and changes nothing
- **Files:** `internal/runner/triage_stage.go` (new), `internal/runner/triage_stage_test.go` (new), `internal/runner/runner.go`
  (one call after each review verdict), `internal/runner/budget.go` (a stage slice), `internal/runner/accountchoice.go`
  (`rec.StageAccounts["triage"]`). **Interfaces:** consumed T-TR3, T-TR5, T-TR6, the review verdicts, `reachedRigor`;
  produced a runner-internal step: when `agent.triage` is on, the mode is Startup, Pro or Hotfix and the repository opted in,
  it calls the senior reviewer's model and account (D1808) with `triage.Prompt`, applies the verdict and **writes it to the
  record only** (shadow): the fixer, `Decide` and the PR are untouched. A failed call records `failed: true` and continues.
- **Tests:** `TestTriageRunsAfterEachReviewRound`, `TestTriagePersonaFollowsTheReachedMode`, `TestTriageSkippedInPOCAndCritical`,
  `TestTriageSkippedForAnUnconfiguredRepository`, `TestTriageFailureIsRecordedAndHarmless`,
  `TestShadowTriageDoesNotChangeTheFixPrompt`, `TestTriageStageAccountIsRecorded`. **Command:** `GOFLAGS=-p=2 go test
  ./internal/runner/ -run 'TestTriage|TestShadowTriage' -count=1 -timeout 20m`.
- **Depends on:** T-TR3, T-TR5, T-TR6, **BF1m (external, edits `runner.go`, `budget.go`)**. **Red step:**
  `TestShadowTriageDoesNotChangeTheFixPrompt` fails if the stage alters what the fixer is given. **Reviewer tier:** opus +
  mandatory independent review, level 3 (runner, agent prompt). **OWNER-RUN:** no. **Commit:** `runner: a triage stage after
  each review round, logged and not yet acted on`.

### T-TR7b: the fixer sees only the blockers, and the loop stops when none remain
- **Files:** `internal/runner/runner.go` (the loop's break and the fix call), `internal/runner/prompts.go` (`FixPrompt` takes the
  blocking subset and says to touch nothing else), `internal/runner/outcome.go` (`Decide` reads the deferred set),
  `internal/runner/triage_stage.go`, `internal/runner/prompts_test.go`, `internal/runner/outcome_test.go`,
  `internal/runner/triage_loop_test.go` (new). **Interfaces:** consumed T-TR7a's outcome; produced: a round whose findings are
  all deferred ends the review loop and finalizes ready (Startup, Pro, Hotfix; D1805's cap rule is T-TR8); a mixed round
  hands the fixer only `Outcome.Blocking`; every level's findings are triaged (D1807).
- **Tests:** `TestFixPromptListsOnlyBlockingFindings`, `TestFixPromptSaysTouchNothingElse`, `TestAllDeferredRoundEndsTheLoop`,
  `TestStartupTrivialOnlyFindingsAreReadyAfterOneReview` (acceptance 1), `TestFixerIsNeverGivenADeferredFinding`
  (acceptance 2), `TestLevelThreeFindingsAreTriagedButFloorStillBlocks`, `TestUnconfiguredRepositoryNeverDefers`,
  `TestPOCAndCriticalLoopUnchanged`. **Command:** `GOFLAGS=-p=2 go test ./internal/runner/ -run
  'TestFixPrompt|TestAllDeferred|TestStartupTrivial|TestFixerIsNever|TestLevelThree|TestUnconfigured|TestPOCAndCritical' -count=1
  -timeout 20m`.
- **Depends on:** T-TR7a. **Red step:** `TestStartupTrivialOnlyFindingsAreReadyAfterOneReview` fails: a second fix round runs.
  **Mutation (run, restore):** hand the fixer the deferred findings; `TestFixerIsNeverGivenADeferredFinding` fails. **Reviewer
  tier:** opus + mandatory independent review, level 3. **OWNER-RUN:** no. **Commit:** `runner: the fixer gets only the
  blocking findings and the loop ends when none block`.

### T-TR8: a review cap defers instead of dropping
- **Files:** `internal/runner/outcome.go`, `internal/runner/outcome_test.go`, `internal/runner/triage_cap_test.go` (new).
  **Interfaces:** consumed T-TR7b and BF1m's cap rule; produced: at the cap, Startup and POC finalize ready with every
  non-floor leftover recorded under `Record.Deferred` (`source: cap`); Pro and above stay draft with the same recorded
  deferrals; a floor finding always holds the draft (D1805).
- **Tests:** `TestCapHitStartupFinalizesReadyWithDeferrals`, `TestCapHitProStaysDraftButRecordsDeferrals`,
  `TestCapHitFloorFindingHoldsTheDraft`, `TestCapHitCriticalUnchanged`. **Command:** `GOFLAGS=-p=2 go test ./internal/runner/ -run
  'TestCapHit' -count=1 -timeout 20m`.
- **Depends on:** T-TR7b, **BF1m (external)**. **Red step:** `TestCapHitStartupFinalizesReadyWithDeferrals` fails: the PR is a
  draft. **Reviewer tier:** opus + mandatory independent review, level 3. **OWNER-RUN:** no. **Commit:** `runner: a cap hit
  defers its leftovers, and only the floor holds the draft`.

### T-TR9: the pull request and the report say what was deferred
- **Files:** `internal/runner/prflow.go`, `internal/runner/report.go`, `internal/runner/prflow_test.go`,
  `internal/runner/report_run_test.go`. **Interfaces:** consumed T-TR3's record; produced a "Deferred (follow-ups)" section in
  the PR status text and `report.md`, one line per deferral with its one-line reason, and the line "run
  `fugaro followups <run>` after merging" (the command is T-TR10). Wording says ready, never merged (D1802).
- **Tests:** `TestPRStatusListsDeferralsWithReasons`, `TestReportListsDeferrals`, `TestNoDeferralsLeavesTheBodyUnchanged`.
  **Command:** `GOFLAGS=-p=2 go test ./internal/runner/ -run 'TestPRStatusLists|TestReportLists|TestNoDeferrals' -count=1`.
- **Depends on:** T-TR7a, **BF1m (external, edits `prflow.go`, `report.go`)**. **Red step:** `TestPRStatusListsDeferralsWithReasons`
  fails: no section. **Reviewer tier:** standard. **OWNER-RUN:** no. **Commit:** `runner: the PR and the report list what was
  deferred and why`.

## Slice 3: the follow-ups

### T-TR10: `fugaro followups RUN`
- **Files:** `internal/cli/followups.go` (new), `internal/cli/followups_test.go` (new), `internal/cli/root.go` (registers it),
  `internal/cli/docs_base_test.go` (the command list), `website/content/cli-reference.md` (regenerated by `go run
  ./internal/clidoc`). **Interfaces:** consumed T-TR3's `Record.Deferred`, T-TR4's task fields, the existing launch path of
  `fugaro run`; produced `fugaro followups RUN [--dry-run] [--json]`: reads the run record, groups deferrals by their `group`
  key (one task per group, one per finding when ungrouped), writes a task per group carrying `follow_up_of{run,keys}`,
  `debt: true`, the parent's applied mode as `Task.Rigor` (re-resolved through the floors at launch, D1804), the finding text
  and the triage reason, and launches them in parallel through the same path as `fugaro run`. Refuses under a coding agent's
  session (writes cloud state). `--dry-run` prints what it would launch and writes nothing.
- **Tests:** `TestFollowupsGroupsByKey`, `TestFollowupsCarriesTheParentsAppliedMode`, `TestFollowupsMarksDebtAndParent`,
  `TestFollowupsRefusesInAnAgentSession`, `TestFollowupsDryRunWritesNothing`, `TestFollowupsIsIdempotentPerRun`,
  `TestFollowupsNeverLowersAFloor`. **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestFollowups' -count=1`; then
  `go run ./internal/clidoc` and `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs|CliReference' -count=1`.
- **Depends on:** T-TR3, T-TR4. **Red step:** `TestFollowupsRefusesInAnAgentSession` fails: it writes. **Mutation (run, restore):**
  drop the agent-session refusal; that test fails. **Reviewer tier:** opus + mandatory independent review (a cloud write path
  and a launch path). **OWNER-RUN:** no. **Commit:** `cli: fugaro followups launches a run's deferred findings as tasks`.

### T-TR11: list the debt
- **Files:** `internal/cli/runs.go`, `internal/cli/runs_test.go`. **Interfaces:** consumed T-TR4's `Task.Debt`; produced
  `fugaro runs ls --debt` (only runs whose task is marked `debt`), and `follow_up_of` in `runs ls --json` as a new optional
  field (no existing field changes).
- **Tests:** `TestRunsLsDebtFiltersToDebtTasks`, `TestRunsLsJSONCarriesFollowUpOf`, `TestRunsLsJSONExistingFieldsUnchanged`.
  **Command:** `GOFLAGS=-p=2 go test ./internal/cli/ -run 'TestRunsLsDebt|TestRunsLsJSON' -count=1`.
- **Depends on:** T-TR4. **Red step:** `TestRunsLsDebtFiltersToDebtTasks` fails: no flag. **Reviewer tier:** standard. **OWNER-RUN:**
  no. **Commit:** `cli: runs ls --debt lists the follow-ups filed from triage`.

## Slice 4: the words, and the live proof

### T-TR12: docs, the skill and the release notes
- **Files:** `docs/triage.md` (new), `docs/recipes.md` (the readiness paragraph, keeping its pinned sentences),
  `plugin/skills/working/SKILL.md` (one paragraph naming `fugaro followups`, `--debt` and the stage), `docs/releases/v0.22.0.md`
  (the entry, and `### For operators`: run `fugaro update`; no other step), `website/data/release-summaries.yaml` only if a new
  release file is added. **Interfaces:** consumed T-TR7b, T-TR8, T-TR9, T-TR10, T-TR11; produced the user-facing account
  of the stage, the floor, "ready is not merged" and the debt command.
- **Tests:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs -count=1` and `GOFLAGS=-p=2 go test ./plugin/ -count=1`.
- **Depends on:** T-TR7b, T-TR8, T-TR9, T-TR10, T-TR11. **Red step:** the plugin test fails until the skill names the command.
  **Reviewer tier:** opus + mandatory independent review (plugin skills are security paths). **OWNER-RUN:** no. **Commit:**
  `docs: post-review triage, the floor and the follow-ups command`.

### T-TR13: Checks 470-472, owner-run
- **Files:** `scripts/live-checks/check470.sh`, `check471.sh`, `check472.sh` (new, idempotent, each takes `--dry-run`, refuses the
  project named `belong`), their `*_test.sh` with a fake gcloud, `docs/gcp-live-checklist.md` (the three sections and the
  register's rows). **Interfaces:** consumed T-TR7b, T-TR8, T-TR10; produced the checks. **Tests:** the fake-gcloud tests only;
  an agent never runs a check.
- **Depends on:** T-TR7b, T-TR8, T-TR10. **Reviewer tier:** standard; the scripts are reviewed in the run. **OWNER-RUN: yes** (see
  Checks). **Commit:** `live-checks: 470-472, triage on a real run`.

## Parallel execution

**Edges** (from "Depends on", plus BF1m as an external prerequisite `B`): T-TR3 ← B; T-TR5 ← B; T-TR6 ← T-TR1, T-TR2; T-TR7a ←
T-TR3, T-TR5, T-TR6, B; T-TR7b ← T-TR7a; T-TR8 ← T-TR7b, B; T-TR9 ← T-TR7a, B; T-TR10 ← T-TR3, T-TR4; T-TR11 ← T-TR4; T-TR12 ←
T-TR7b, T-TR8, T-TR9, T-TR10, T-TR11; T-TR13 ← T-TR7b, T-TR8, T-TR10.

| Level | Tasks (concurrent) | Starts when |
| --- | --- | --- |
| 0 | T-TR1, T-TR2, T-TR4 | at once (no edge, no shared file) |
| 1 | T-TR3, T-TR5, T-TR6, T-TR11 | T-TR3 and T-TR5 after BF1m merges; T-TR6 after T-TR1, T-TR2; T-TR11 after T-TR4 |
| 2 | T-TR7a, T-TR10 | T-TR7a after T-TR3, T-TR5, T-TR6; T-TR10 after T-TR3, T-TR4 |
| 3 | T-TR7b, T-TR9 | after T-TR7a |
| 4 | T-TR8 | after T-TR7b |
| 5 | T-TR12, T-TR13 | after the levels above |

**Critical path:** BF1m → T-TR5 → T-TR7a → T-TR7b → T-TR8 → T-TR12 (and T-TR13). Everything off that path (T-TR1, T-TR2, T-TR4,
T-TR6, T-TR10, T-TR11) can finish while BF1m is still in review. **Sequential on purpose:** T-TR7a before T-TR7b (shadow
first, so the verdicts can be read and tuned before they steer the loop; acceptance 3), and T-TR7b before T-TR8 (both write
`outcome.go`).

### Shared files (no level holds two writers of one file)

| File | Writers (level) |
| --- | --- |
| `internal/rigor/floor.go` | T-TR1 (0) |
| `internal/triage/*` | T-TR2 (0: verdict, prompt, personas, schema); T-TR6 (1: `apply.go`, a separate file) |
| `internal/runstore/runstore.go`, `schemas/result.schema.json` | T-TR3 (1) |
| `internal/task/task.go`, `schemas/task.schema.json` | T-TR4 (0) |
| `internal/config/*`, `internal/policy/merge.go`, `schemas/fugaro.schema.json`, `schemas/project-layer.schema.json` | T-TR5 (1) |
| `internal/runner/runner.go` | T-TR7a (2), T-TR7b (3) |
| `internal/runner/triage_stage.go` | T-TR7a (2), T-TR7b (3) |
| `internal/runner/prompts.go` | T-TR7b (3) |
| `internal/runner/outcome.go` | T-TR7b (3), T-TR8 (4) |
| `internal/runner/budget.go`, `accountchoice.go` | T-TR7a (2) |
| `internal/runner/prflow.go`, `report.go` | T-TR9 (3) |
| `internal/cli/followups.go`, `root.go`, `docs_base_test.go`, `website/content/cli-reference.md` | T-TR10 (2) |
| `internal/cli/runs.go` | T-TR11 (1) |
| `docs/triage.md`, `docs/recipes.md`, `plugin/skills/working/SKILL.md`, `docs/releases/v0.22.0.md` | T-TR12 (5) |
| `scripts/live-checks/check47*`, `docs/gcp-live-checklist.md` | T-TR13 (5) |

BF1m (#1999) writes `internal/rigor/table.go`, `internal/runner/{outcome,runner,prflow,report,budget}.go`, `internal/config/*`, both
config schemas and `schemas/result.schema.json`: it is merged before any level-1-or-later task that names one of them starts.
A change to any task's Files re-checks this table.

## Checks (this folder's block: 470-479; 470-472 taken)

| Check | What it measures | Who runs it |
| --- | --- | --- |
| **470** | A Startup run on a real project whose reviewer reports only a trivial finding (a doc sentence off by one word) finishes ready after one review round, with the finding in the PR's Deferred section and the triage block in `result.json`. | The owner: `scripts/live-checks/check470.sh` (idempotent; `--dry-run`; refuses `belong`). |
| **471** | `fugaro followups RUN` after that PR is merged launches one debt task per group, each at the parent's applied mode, and `fugaro runs ls --debt` lists them. | The owner: `check471.sh`. |
| **472** | A finding of class `security` is never deferred in a real Startup run, and with level 3 requested the same holds (D1807's tension). | The owner: `check472.sh`. |

## After the deploy

`fugaro update` brings an installation to this release: the published policy node learns the `triage` stage name and
`agent.triage` through its normal converge, the job images are rebuilt behind their own confirmation, and no other step is
needed. T-TR12's release entry says exactly that under `### For operators`. A test that an installation at the previous policy
version converges is part of T-TR5.

## Considered and declined

- **Reusing `rec.Tolerated` for persona deferrals:** declined; the field means "the mode's table let it through" (F10).
- **A recipe step kind `triage`:** declined; recipes are closed at version 1 (F9), so triage is a runner-internal step.
- **Markdown backlog items as the follow-up carrier:** declined by D1803; `internal/backlog` stays as it is.
- **Fugaro merging the PR:** declined (D13, F8); out of scope (Open tension 4).
- **Re-reviewing only what changed:** the spec's own later item; not planned here.
- **Wiring follow-ups to the orchestrator's `DependsOn`:** out of scope until that chain lands (Open tension 3).
