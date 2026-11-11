# Outer-loop escalation: implementation plan

This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md): a **ladder of rungs R0-R4** in the
orchestrator daemon that decides, per pull request and from its age, attempts, cost and churn, whether to rebase it (R0),
resume it (R1), run one stronger single pass (R2), merge it with follow-ups (R3) or split it (R4). It is built **on** BF2/O14
of `docs/stateless-orchestrator`, not beside it.

## Status

**Plan only. Nothing is implemented and no task is approved.** Each task needs the owner's own explicit go; an answer to a
decision is not a go. The owner's answers that shaped the plan (2026-11-10): D2030 the orchestrator merges under D1245's gate;
D2031 the merge judge can only block; D2032 the ladder replaces `orchestrator.triage_after`; D2033 rungs and model grades;
D2034 an Escalation section in the one digest; D2035 rung 0 is a cloud rebase run; D2036 a repeated finding goes to R2 then
R4; D2037 Critical and non-Routine wait for a person; **D2041 the daemon may ready a draft and merge it (not the
recommendation)**; D2042 an item's approval covers its ladder runs. D2046 (default off for `ready_drafts`) awaits confirmation. Resolutions D2038-D2040 and D2043-D2045 stand unless overturned at review.

External prerequisites (not tasks here): O0-O4 and O6 of `docs/stateless-orchestrator` are merged; **O7, O8 and O14** are not
(`docs/status.md`: 10 of 19), and this plan's R3/R4 executor (E10b) owns the merge and the split, so O14 is **re-scoped to read
the ladder** when it is approved (E0 records that); the runner filling `TriageEntry.Key` (the field exists, #2215) is needed by
E9; the daily digest of `docs/periodic-reviews` (R7-R12, not built) is needed by E12.

## Conventions every part follows

- `CLAUDE.md`: Go gates (`go build ./... && gofmt -l . && go vet ./...`), targeted tests only, hermetic tests (`internal/gcpfake`,
  `internal/testutil`, `t.TempDir()`), no secret-shaped literals, comments say why, no TODO comments, search before writing a
  helper, a CLI change that touches a documented command changes the plugin skill in the same PR.
- `.claude/rules/credentials-service.md` for the judge, grade resolution and the merge call (credentials, tokens, prompts).
- **A field never changes meaning.** Every new field below is new and optional; no existing field is retyped. Strict decoders
  mean each new `fugaro.yaml`/policy key needs a "Needs fugaro X.Y.Z" refusal on older images (the `triageSince` pattern in
  `internal/cli`); the task that adds a key owns it.
- **`fugaro update` is the whole upgrade and is safe with runs in flight.** New local keys have defaults; new state fields are
  optional; the `escalate`, `rebase` and `merge-judge` recipes ship in the catalog. The release's operator section is "run
  `fugaro update`" (E14). A test per format task proves an installation at the previous version converges.
- **No agent performs a live action.** Checks 540-541 are owner-run (E15). The merge call has a fake provider in tests only.
- Never edit `.github/workflows/`; no task here needs to.
- Size limits: `internal/orchestrator/tick.go` and `internal/cli` are large; each task states the file it touches and adds new
  files rather than growing a big one.

## Global constraints and review focus

Inputs and failure modes the spec implies and no task would otherwise test; each names the task that pins it.

| # | Constraint | Pinned by |
| --- | --- | --- |
| G1 | The merge judge can never cause a merge, and an absent verdict is a veto: a judge run that fails, times out, runs out of budget or returns malformed output blocks the merge (D2044); a diff containing instructions to merge changes nothing. | E6, E10b, Check 541 |
| G2 | One head SHA end to end: the findings, path set, CI read (`Checks(number, sha)`), `MarkReady` and `Merge` all carry the SHA that was reviewed or judged; each re-reads the head and refuses on a mismatch; a provider with no expected-head precondition on merge refuses to merge (F24). | E8, E10b |
| G3 | Thresholds, `cost_ceiling` and `ready_drafts` are never settable by a repository file or the project layer (loosening authority); they are local-only. Grades are the one exception: the project layer defines them and a repository file cannot. | E2, E3 |
| G4 | Each rung transition is idempotent across a daemon crash (audit id `<subject>:<transition>`) and bounded: at most one R2 attempt, one R0 per head SHA, R4 never relaunches the same spec. | E7, E10a |
| G5 | An unset grade, an unreadable record, a missing CI read or an unknown mode refuses and asks (fail closed to recommend-only), never a default. | E2, E10a |
| G6 | Importance sorts and notifies; it never approves a follow-up (D1226, F21). | E11 |
| G7 | "Blocks others" is computed each tick from `DependsOn` of **approved** items only (an unapproved item an agent added cannot halve a PR's thresholds); a cycle or a 32+ edge fan-out (`MaxDependsOn`) does not loop or panic. | E1, E10a |
| G8 | The ledger and the digest never hold a secret, a prompt or a diff; only rung, reason code, counters, times, run and PR ids. | E7, E12 |
| G9 | Cost ceiling reads the sum of the item's own run records (the chain's figure where a chain exists); a restart cannot reset it. | E1, E10a, E11 |
| G10 | A non-Routine path set, a critical finding or a high finding in a security path never auto-merges in any mode (D1245, D1247, D2037). | E10b |
| G12 | Only the daemon writes the escalation block: it is in `applyUpsert`'s daemon-only set (`patch.go`), so an agent-class actor cannot steer an item into R3; and it is in `bookkeepingFields`, so a ledger write never changes `SpecDigest`. | E7 |
| G13 | R0 never merges directly (D2043): a merge is eligible only at a head covered by a scored review verdict, with the path set computed at that head; a conflict-resolved R0 head gets a `review-only` run first and the judge only adds vetoes. | E10b |
| G14 | A merge never bypasses branch protection: no admin or bypass flag, and the call refuses when the installation token's actor is on the base's bypass list or the list cannot be read (D2045, F26). | E8, E10b |
| G15 | The ladder's launches are covered by the item's approval and bounded by the cost ceiling; `ApprovalAttempts` counts normal runs only; nothing the ladder files is approved (D2042, F21). | E10a, E11 |
| G11 | A task's `model_grade` is one of the two names the project layer defines and resolves only to what that layer says; it cannot name a raw model or credential, and an unset grade refuses. | E4 |

## Slices

- **Slice A, inert foundations (E0-E5, E8):** the engine as pure code, grades, local keys, recipes and the provider's merge and
  checks calls. Nothing in the daemon calls any of it; shippable alone.
- **Slice B, records and judge (E6, E7, E9, E11, E12, E13):** the ledger, the churn comparator, the judge, importance, the digest
  section and the read command. Still no rung is executed.
- **Slice C, the executor (E10a, E10b):** the daemon walks the ladder. Rungs 0-2 first, then 3 and 4 with the merge.
- **Slice D, ship (E14, E15):** docs, skill, release notes and the owner-run checks.

## Tasks

Reviewer tier is **standard** unless stated. Every task's tests are targeted; no task runs a whole package suite.

### E0. Amend the pages this plan changes

- **Files:** `docs/stateless-orchestrator/reconciliation.md` (D1244 row: "replaced by D2032 of `docs/outer-loop-escalation`"
  and that `orchestrator.triage_after` was never built; D1246 row: amended by D2036; D1245 row: amended by D2041, a draft may be
  readied by the daemon behind `escalation.ready_drafts`; D1226 row: amended by D2042), `docs/stateless-orchestrator/implementation-plan.md`
  (O14 re-scoped to read the ladder), `docs/rigor-modes/levels-implementation-plan.md` and `docs/rigor-modes/levels-reconciliation.md`
  (D13's "never merges" and Hotfix: "amended by D2030, D2041, D2045"), `docs/rigor-modes/reconciliation.md` (D13's own ruling), `docs/stateless-orchestrator/README.md` (it still names `triage_after`), `docs/design/v1.md` (the non-goal "Auto-merge" reads
  "no merge outside the orchestrator's gated rule"), and the Docs tests that pin those sentences
  (`internal/cli/docs_stateless_orchestrator_test.go`, `docs_*` for rigor and design, whichever pins the edited text).
  `docs/status.md` is **not** edited by this task (its row is added by the plan's own PR and moves with merges).
- **Interfaces:** none. **Schema/rules/config:** none.
- **Tests:** `go test ./internal/cli/ -run Docs -count=1` (keep each pinned fact, change both places).
- **Command:** `go test ./internal/cli/ -run Docs -count=1`.
- **Depends on:** none (the owner confirms T1, T3 and T4 first).
- **Commit:** `docs: the ladder amends D1226, D1244, D1245, D1246 and D13 (E0)`.
- **Red step:** a Docs test that pins the D1244 sentence fails after the edit until its pinned copy changes with it.
- **Reviewer tier:** security (it edits owner rulings and a readiness rule).

### E1. The ladder as pure code

- **Files:** `internal/escalation/rung.go`, `thresholds.go`, `decide.go`, `decide_test.go`, `thresholds_test.go` (all new).
- **Interfaces:** consumes `rigor.Mode` (`internal/rigor/mode.go`), nothing from the daemon. Produces `Rung` (R0-R4),
  `Thresholds` (per mode: runs before R2, grade, ceiling hours, at-ceiling action, as the spec's table), `Facts` (elapsed,
  outer attempts, chain cost, blocks-others, mode, churn kind, head SHA, last review blocking?) and `Decide(Facts, Thresholds)
  (Step, Reason)`, where `Reason` is a closed code (`age`, `attempts`, `cost`, `churn_new`, `churn_same`, `hotfix`,
  `critical_path`).
- **Schema/rules/config:** none. **Tests:** `TestDecideTable` (every mode, with and without critical path, factor 0.5),
  `TestDecideCostCeiling`, `TestDecideChurnNewGoesToR3`, `TestDecideSameFindingR2ThenR4`, `TestDecideHotfixStraightToR2`,
  `TestDecideUnknownModeRefuses`, `TestDecideNeverLoops`.
- **Command:** `go test ./internal/escalation/ -count=1`.
- **Depends on:** none. **Commit:** `feat(escalation): the rung decision as pure code (E1)`.
- **Red step:** `TestDecideTable` against an empty `Decide` returns R1 for every row and fails on the Hotfix and Pro rows.
- **Reviewer tier:** standard.

### E2. Model grades in the project layer

- **Files:** `schemas/project-layer.schema.json`, `internal/projectpolicy/` (the decoded layer and its validator),
  `internal/policy/merge.go` (a grade is project-only: a later layer that names one goes to `Ignored`),
  `internal/settings/registry.go` (the key and its description), tests beside each.
- **Interfaces:** produces `ModelGrades{Strong, Strongest}` (each a model plus its credential or pool, validated exactly as a
  craft's). **Schema/rules/config:** adds `defaults.model_grades.{strong,strongest}` (`defaults` is closed, `additionalProperties: false` and `DisallowUnknownFields`, so this **bumps `projectpolicy.Version`** with its `update` migration, and a test proves a run in flight on the previous version keeps working).
- **Tests:** `TestProjectLayerModelGrades`, `TestPolicyMergeGradesProjectOnly`, `TestGradeUnsetIsRefusal`,
  `TestGradeWithoutPriceRefused` (a model must have a known price when a budget is on), `TestPolicyPreviousVersionConverges`, `TestRunInFlightSurvivesPolicyBump`.
- **Command:** `go test ./internal/projectpolicy/ ./internal/policy/ ./internal/settings/ -run 'Grade|Converges' -count=1`.
- **Depends on:** none. **Commit:** `feat(policy): model grades strong and strongest in the project layer (E2)`.
- **Red step:** `TestPolicyMergeGradesProjectOnly` fails while a repo `fugaro.yaml` can set a grade.
- **Note:** a grade named in a repo file goes to `Ignored` (the policy-merge convention), where the thresholds in E3 are refused
  by name: a grade is a model choice the project layer owns, a threshold is authority that no shared layer may hold.
- **Reviewer tier:** opus + mandatory independent review (a model and credential choice).

### E3. The local keys

- **Files:** `internal/localcfg/escalation.go` (new), `internal/localcfg/localcfg.go` (one decode hook),
  `internal/settings/registry.go`, tests (the load-time refusal lives in `internal/localcfg/escalation.go`, not in
  `internal/orchestrator/validate.go`, which E7 owns).
- **Interfaces:** consumes `escalation.Thresholds` (E1) and the registry (E2). Produces the keys
  `escalation.r2_after_hours`, `escalation.r2_after_attempts`, `escalation.r3_after_hours`,
  `escalation.critical_path_factor` (default 0.5), `escalation.cost_ceiling`, `escalation.r2_coder`,
  `escalation.merge_judge` (a grade name), **`escalation.ready_drafts` (default off, T3)** and the per-mode override table.
  The spec's `tier2_*`, `tier3_*` and `tier2_coder` keys are renamed for rungs (D2033), because a key named for the old word
  would mean something else beside `internal/tier`; the reconciliation maps one to the other.
- **Schema/rules/config:** local config only; **refused at load** when set in the project layer or a repo `fugaro.yaml`
  (the D1244 pattern, G3). (`orchestrator.triage_after` never shipped, so nothing is retired in code; E0 changes the docs.) Turning
  `ready_drafts` on, lowering a threshold or raising `cost_ceiling` is a loosening and is **refused from a coding agent's
  session**, like the existing `agentSession` floor refusals.
- **Tests:** `TestEscalationKeysDefaultsPerMode`, `TestEscalationKeysRefusedInProjectLayer`, `TestEscalationKeysRefusedInRepo`,
  `TestEscalationReadyDraftsDefaultsOff`, `TestEscalationKeysRefusedFromAgentSession`.
- **Command:** `go test ./internal/localcfg/ ./internal/settings/ -run 'Escalation' -count=1`.
- **Depends on:** E1, E2 (the registry file). **Commit:** `feat(localcfg): the local escalation keys (E3)`.
- **Red step:** `TestEscalationKeysRefusedInProjectLayer` fails while the project decoder accepts the key.
- **Reviewer tier:** opus + mandatory independent review (the keys hold merge authority).

### E4. A task names a model grade, and carries a follow-up importance

- **Files:** `internal/task/task.go`, `schemas/task.schema.json`, `internal/runner/routing.go` (resolve the grade to a model and
  credential through the project's grades), `internal/cli/recipes_skew.go` (the "Needs fugaro X.Y.Z" refusal for both fields), tests.
- **Interfaces:** consumes `ModelGrades` (E2). Produces two optional task fields: `model_grade` (`strong|strongest`) and, on the follow-up block, `importance` (`important|minor`, D2040).
- **Schema/rules/config:** `task.Version` is unchanged (new optional field); the strict decoder and the schema list it; "Needs
  fugaro X.Y.Z" on older images.
- **Tests:** `TestTaskModelGradeRoundTrip`, `TestRoutingResolvesGrade`, `TestRoutingGradeUnsetRefuses`,
  `TestRoutingGradeCannotNameRawModel` (G11), `TestRoutingGradeAppliesToCoderAndJudge`, `TestTaskImportanceRoundTrip`.
- **Command:** `go test ./internal/task/ ./internal/runner/ -run 'ModelGrade|Grade|TaskImportance' -count=1`.
- **Depends on:** E2. **Commit:** `feat(runner): a task may name a model grade (E4)`.
- **Red step:** `TestRoutingResolvesGrade` fails with the coder still on `agent.models.coder`.
- **Reviewer tier:** opus + mandatory independent review.

### E5. The `escalate` and `rebase` recipes

- **Files:** `internal/recipe/catalog/escalate.yaml`, `internal/recipe/catalog/rebase.yaml` (both new),
  `internal/recipe/stages.go`, `schemas/recipe.schema.json` (a `hand_over` flag: end the run at the review with the verdict
  recorded, no fix), `internal/runner/recipe.go`, `internal/gitops/rebase.go` (new; reuses the existing force-with-lease push, `PushExisting`, and adds the rebase step the `rebase` recipe needs), `docs/recipes.md`, `internal/cli/recipes_skew.go` (the three recipe names, `escalate`, `rebase`, `merge-judge`, with their "Needs fugaro X.Y.Z" gate), tests.
- **Interfaces:** `escalate` = coder, one review (`max_rounds: 1`), `hand_over: true`; `rebase` = rebase onto the base, resolve
  conflicts, re-run the tests, push, no review (R0). A `rebase` run that cannot resolve a conflict ends refused with a reason.
- **Schema/rules/config:** recipe `version: 1` unchanged (new optional step flag); the catalog ships in the image.
- **Tests:** `TestRecipeEscalateOnePass`, `TestRecipeHandOverSkipsFix`, `TestRecipeRebaseStagesOrder`,
  `TestRecipeCatalogLints`, `TestRecipeRebaseRefusesOnUnresolvedConflict` (in `internal/runner/runnertest`).
- **Command:** `go test ./internal/recipe/ ./internal/runner/ -run 'Escalate|Rebase|HandOver|Catalog' -count=1`.
- **Depends on:** none. **Commit:** `feat(recipe): escalate and rebase recipes (E5)`.
- **Red step:** `TestRecipeHandOverSkipsFix` fails while findings after the one review launch a fix.
- **Reviewer tier:** opus + mandatory independent review (recipes and prompts).

### E8. The provider's ready, merge and checks calls

- **Files:** `internal/gitprov/gitprov.go` (`MarkReady(ctx, number, expectedHeadSHA)`, `Merge(ctx, number, expectedHeadSHA)`,
  `Checks(ctx, number, sha)`), `internal/gitprov/github/`, `internal/gitprov/bitbucket/`, `internal/gitprov/fake/`,
  `internal/gitprov/marker.go` (a transition marker kind), tests.
- **Interfaces:** produces the three calls; consumed by E10b. `MarkReady` re-reads the head, refuses on a mismatch, then sets
  `Draft:false` (`ApplyReady` is not used: it only adds reviewers and labels). `Merge` carries the expected head, never uses an
  admin or bypass flag, uses the provider's installation `TokenSource` (F26), refuses when that actor is on the base's bypass list or the list is unreadable, and **refuses on a provider whose API has no expected-head precondition**. `Checks` reads the check runs
  at that SHA: green means at least one reported run and every reported run concluded success; zero runs, a run still pending, or a failure to
  read the required set is `CIUnknown` and never green (no branch-protection admin read is needed). If O14 lands its own CI read
  first, this task shrinks to `MarkReady`, `Merge` and the marker (searched before writing).
- **Schema/rules/config:** none. **Tests:** `TestFakeMergeRefusesOnHeadChange`, `TestFakeMarkReadyRefusesOnHeadChange`,
  `TestGithubMergeSendsExpectedSHAAndNoBypass`, `TestBitbucketMergeRefusesWithoutHeadPrecondition`,
  `TestChecksZeroRunsIsUnknown`, `TestChecksPendingIsUnknown`, `TestChecksGreenOnlyWhenAllReportedPass`, `TestMergeRefusesWhenActorOnBypassList`, `TestMarkerRoundTripEscalation`.
- **Command:** `go test ./internal/gitprov/... -run 'Merge|MarkReady|Checks|Marker' -count=1`.
- **Depends on:** none. **Commit:** `feat(gitprov): ready, merge and read checks, each bound to a head SHA (E8)`.
- **Red step:** `TestFakeMergeRefusesOnHeadChange` fails against a `Merge` that ignores the SHA.
- **Reviewer tier:** opus + mandatory independent review (a write path and git provider tokens).

### E7. The ledger in the orchestrator state

- **Files:** `internal/orchestrator/state.go` (an optional `Escalation` block on an item: rung, entered-at, outer attempts,
  transitions `[{from,to,reason,at,head_sha}]`, bounded; the block is added to `bookkeepingFields`, G12), `patch.go` (the block
  joins `applyUpsert`'s daemon-only set), `validate.go`, `schemas/orchestrator-state.schema.json`,
  `schemas/orchestrator-patch.schema.json`, tests. The deployed RTDB rules do not pin the node's shape, so no rules change.
- **Interfaces:** consumes `escalation.Rung` (E1). **Schema/rules/config:** adds one block and bumps `schema_version` to 2 with a
  `Migrate` that `fugaro update` runs on a daemon's existing state (the decoder is closed and a reader above its version stops, so
  the old state must be migrated, not left); the phase machine is unchanged; transitions are append-only through the typed patch.
- **Tests:** `TestStateEscalationRoundTrip`, `TestPatchEscalationAppendOnly`, `TestValidateEscalationBounds`,
  `TestStateV1MigratesToV2`, `TestOldDaemonStopsOnNewerVersion`, `TestEscalationNotInSpecDigest` (a ledger write keeps the approval), `TestAgentActorCannotWriteEscalation` (G12), `TestLedgerHoldsNoPromptOrDiff` (G8), `TestStatePreviousVersionConverges`.
- **Command:** `go test ./internal/orchestrator/ -run 'Escalation|Migrates|OldDaemon|Converges|NoPrompt|AgentActor' -count=1`.
- **Depends on:** E1. **Commit:** `feat(orchestrator): the escalation ledger on an item (E7)`.
- **Red step:** `TestPatchEscalationAppendOnly` fails while a patch can rewrite a past transition.
- **Reviewer tier:** opus + mandatory independent review (the daemon-only write set).

### E9. The churn comparator

- **Files:** `internal/escalation/churn.go`, `churn_test.go` (new).
- **Interfaces:** consumes `runstore.TriageEntry.Key` (exists; filled by the triage stage in Startup and Pro only) plus the
  finding's file and class, and `rigor.Blocks`. Produces `Churn(rounds) Kind` in `{none, same, new_minor}`; two findings are "same"
  when the key **or** the file and class match; "minor" is `rigor.Blocks`' answer for the mode. Off (`none`) in POC, Critical and Hotfix.
- **Schema/rules/config:** none. **Tests:** `TestChurnSameFinding`, `TestChurnNewMinorEveryRound`, `TestChurnMixedIsSame`,
  `TestChurnNoKeyIsNone` (fail closed to R1's normal path, never a guess), `TestChurnRewordedSameFileClassIsSame`, `TestChurnOffInPOCCriticalHotfix`.
- **Command:** `go test ./internal/escalation/ -run Churn -count=1`.
- **Depends on:** E1; external: the runner filling the key (UNVERIFIED which post-review-triage task). **Commit:** `feat(escalation): churn across review rounds (E9)`.
- **Red step:** `TestChurnNewMinorEveryRound` fails while only identical keys are compared.
- **Reviewer tier:** standard.

### E11. The launcher orders follow-ups and bounds the ladder's spend

- **Files:** `internal/orchestrator/launch.go` (order approved items important-first; sort pending ones for the person; count
  ladder launches apart from `ApprovalAttempts`), `internal/orchestrator/intent.go` (`approvalCovers` and `spentBy`: a ladder intent
  is covered by the item's approval and survives a crash and a retry that clears `Approved`; a mode's third normal run is a ladder
  R1 launch, covered by D2042 under the cost ceiling), tests.
- **Interfaces:** consumes `Task.FollowUpOf` and `importance` (E4). **Schema/rules/config:** none.
- **Tests:** `TestLauncherOrdersImportantFirst`, `TestImportanceNeverApproves` (G6), `TestImportanceRuleSecurityOrImpact5IsImportant`,
  `TestApprovalAttemptsCountNormalRunsOnly` (G15), `TestLadderLaunchStopsAtCostCeiling` (G15), `TestLadderIntentSurvivesCrashAndRetry`, `TestThirdNormalRunIsCoveredByLadder`.
- **Command:** `go test ./internal/orchestrator/ -run 'Importance|ApprovalAttempts|LadderLaunch|LadderIntent|ThirdNormal|Launcher' -count=1`.
- **Depends on:** E4. **Commit:** `feat(orchestrator): importance orders follow-ups; the ladder's launches have their own bound (E11)`.
- **Red step:** `TestImportanceNeverApproves` fails against a launcher that treats important as approved.
- **Reviewer tier:** opus + mandatory independent review (approval semantics).

### E6. The merge judge

- **Files:** `internal/config/craft.go` (craft `merge-judge`, following `senior-review`), `internal/runner/mergejudge.go` (new),
  `internal/runner/prompts.go` (the prompt: only the diff, the open findings and the fundamentals checklist, never the
  reviewer's fix framing), `schemas/merge-judge.schema.json` (new), `schemas/result.schema.json` (an optional `merge_judge`
  block), `internal/recipe/catalog/merge-judge.yaml` (new), `internal/recipe/stages.go` (a read-only judge stage), `schemas/schemas_test.go` (the new schema), `internal/runner/routing.go` (the judge's grade; E4 wrote it a level earlier), tests.
- **Interfaces:** consumes the grade (E4) and the `hand_over` flag (E5). Produces a verdict `{merge_with_follow_ups|blocked,
  reason, blocker?, follow_ups:[{key,importance}]}`. The model runs read-only with no push credential.
- **Schema/rules/config:** `result.json` gains an optional block; "Needs fugaro X.Y.Z" on older images.
- **Tests:** `TestJudgeInputExcludesFixFraming`, `TestJudgeVerdictSchema`, `TestJudgeMalformedOutputIsBlocked`,
  `TestJudgeInjectedDiffCannotMerge` (G1), `TestJudgeBlockedNamesFundamental`, `TestJudgeHasNoPushCredential`, `TestJudgeFailedOrTimedOutRunIsBlocked` (G1).
- **Command:** `go test ./internal/runner/ ./internal/config/ ./internal/recipe/ -run 'Judge|MergeJudge' -count=1`.
- **Depends on:** E4, E5. **Commit:** `feat(runner): an advisory merge judge that can only block (E6)`.
- **Red step:** `TestJudgeMalformedOutputIsBlocked` fails while unreadable output reads as "merge".
- **Reviewer tier:** opus + mandatory independent review, with a mutation check (flip the veto).

### E12. The digest's Escalation section

- **Files:** `internal/digest/escalation.go` (new, in the periodic-reviews digest package), `internal/budget/rules/rules.json.tmpl`
  (a read grant for the reports job's account on the ledger's escalation block only, F27, with its rules-version bump and its `update`
  step), tests. Added to whichever `docs/periodic-reviews` task lands the daily digest.
- **Interfaces:** consumes the ledger (E7) and the run chain's cost. Produces the section, 24 hours and 7 days: PRs per rung,
  median time to merge per rung, rung 3 merges, "reverted within 7 days" as the regression proxy (O1), follow-ups from rung 3
  and how many closed, churn detections, longest open PRs, hours and cost past R1, and threshold suggestions as text.
- **Schema/rules/config:** one optional block. **Tests:** `TestDigestEscalationSection`, `TestDigestEscalationRegressionProxyIsRevert`,
  `TestDigestEscalationSuggestsLowerWhenNoRegressions`, `TestDigestEscalationHoldsNoSecret` (G8), `TestRulesReportsJobReadsEscalationOnly`.
- **Command:** `go test ./internal/digest/ ./internal/budget/... -run 'DigestEscalation|RulesReportsJob' -count=1`.
- **Depends on:** E7; external R7-R12. **Commit:** `feat(report): an Escalation section in the daily digest (E12)`.
- **Red step:** `TestDigestEscalationSection` fails with no section.
- **Reviewer tier:** opus + mandatory independent review (a rules grant).

### E13. `fugaro escalation`

- **Files:** `internal/cli/escalation.go` (new; `ls` and `show`, read-only, `--json`), `internal/cli/root.go` (registration),
  `schemas/escalation.schema.json` (new), `schemas/schemas_test.go`, `website/content/cli-reference.md` (generated), tests.
- **Interfaces:** consumes the ledger (E7). Prints rung, reason, age, attempts, cost and each transition per PR.
- **Schema/rules/config:** new `--json` format with its own version.
- **Tests:** `TestEscalationLsJSONSchema`, `TestEscalationShowTransitions`, `TestEscalationReadsNoWrite`, `go test
  ./plugin/... -run Skill` (command named in a skill must exist).
- **Command:** `go test ./internal/cli/ -run 'Escalation|Docs' -count=1`.
- **Depends on:** E7, E6 (both write `schemas/schemas_test.go`). **Commit:** `feat(cli): fugaro escalation ls and show (E13)`.
- **Red step:** `TestEscalationLsJSONSchema` fails with no command.
- **Reviewer tier:** standard.

### E10a. The executor, rungs 0 to 2

- **Files:** `internal/orchestrator/escalate_launch.go` (new; it also holds the extension point E10b uses, because `hooks.go` is the ee/
  seam and is not touched), `internal/orchestrator/tick.go` (one call into the executor), tests.
- **Interfaces:** consumes E1, E3, E4, E5, E7, E9, E11, E8 (marker). The ladder's runs are covered by the item's approval (D2042). Each tick, per item: gather facts (elapsed, attempts, chain
  cost, mode, blocks-others from `DependsOn`), `Decide`, then act: R0 launches `rebase` once per head SHA, R1 is today's resume,
  R2 launches `escalate` once with `model_grade` from `escalation.r2_coder` (strongest for critical path, Critical, Hotfix),
  appends the transition and writes the marker comment. Everything is idempotent by audit id (G4).
- **Schema/rules/config:** none beyond E3 and E7. **Tests:** `TestTickR0OncePerHead`, `TestTickR2OnceThenR3Or4`,
  `TestTickCriticalPathHalvesAndUsesStrongest`, `TestTickCrashMidRungIsIdempotent`, `TestTickUnsetGradeRefuses` (G5),
  `TestTickDependsOnCycleDoesNotLoop` (G7), `TestTickCostCeilingEscalates`, `TestTickWritesTransitionMarker`, `TestTickLaunchesWithinTheItemsApproval` (G15), `TestTickUnapprovedDependsOnDoesNotHalve` (G7), `TestTickR0AtMostTwicePerItem`.
- **Command:** `go test ./internal/orchestrator/ -run 'TestTick' -count=1`.
- **Depends on:** E3, E4, E5, E7, E8, E9, E11; external O7, O8 (as O14 requires). **Commit:** `feat(orchestrator): walk rungs 0 to 2 (E10a)`.
- **Red step:** `TestTickR2OnceThenR3Or4` fails while a second R2 launches.
- **Reviewer tier:** opus + mandatory independent review.

### E10b. The executor, rungs 3 and 4, merge and human gate

- **Files:** `internal/orchestrator/escalate_merge.go` (new), `internal/orchestrator/escalate_split.go` (new),
  `internal/orchestrator/notify.go` (kind `merged-needs-review`), tests; extends only through the hook E10a adds.
- **Interfaces:** consumes E6, E8, E10a, `internal/orchestrator/sensitivity`. R3: launch the judge (an absent, failed or late verdict is a veto, D2044); a merge is eligible only at a head covered by a scored review verdict, so a conflict-resolved R0 head gets a `review-only` run at that head first and the judge only adds vetoes (D2043); recompute the D1245 gate (run
  terminal, commits, CI green via `Checks`, `Classify` is `Routine` for the whole path set, no critical or high-in-security
  finding); at the head SHA that was judged, **if `escalation.ready_drafts` is on** ready a draft, then `Merge` with that SHA,
  file every open finding as a follow-up with `importance`, notify for Pro and Hotfix. A veto, a non-Routine path set,
  Critical, or the switch off raises D1247's `waiting_on: person` with the verdict attached. R4: this task builds the split (`escalate_split.go`: the branch kept, the judge's named blocker the scope of the sub-tasks, the D1246
  rule that a split never relaunches the same spec); O14, when built, calls it.
- **Schema/rules/config:** one notification kind. **Tests:** `TestR3MergesOnlyUnderGate`, `TestR3HeadMovedRefuses` (G2),
  `TestR3VetoRaisesDecision` (G1), `TestR3NonRoutineWaitsInEveryMode` (G10), `TestR3CriticalAlwaysWaits`,
  `TestR3DraftNotReadiedWhenSwitchOff`, `TestR3DraftReadiedThenMergedWhenOn`, `TestR3FollowUpsFiledWithImportance`,
  `TestR3NotifiesProAndHotfix`, `TestR4SplitScopedToBlocker`, `TestR3UnreadableInputRecommendsOnly` (G5), `TestR3JudgeAbsentIsVeto` (G1), `TestR3RebasedHeadNeedsScoredReview` (G13), `TestR4BlockerTextFencedAndClipped`, `TestR3NoAdminBypass` (G14), `TestR3BitbucketWithoutPreconditionWaits` (G2).
- **Command:** `go test ./internal/orchestrator/ -run 'TestR[34]' -count=1`.
- **Depends on:** E6, E8, E10a (it builds the split itself, so not on O14). **Commit:** `feat(orchestrator): rung 3 merge under the gate and rung 4 split (E10b)`.
- **Red step:** `TestR3HeadMovedRefuses` fails against a merge that ignores a push after the gate.
- **Reviewer tier:** opus + mandatory independent review, with a mutation check (drop the gate and the SHA in turn).

### E14. Docs, skill, release notes

- **Files:** `docs/outer-loop-escalation/README.md`, `docs/recipes.md` (readiness paragraph: the daemon may ready a draft under D2041), `README.md` (the "Fugaro itself never merges" line), `SECURITY.md` and `website/content/security.md` (where they say it), `docs/status.md` (row moves with merges, in the sweep, not in a task's own PR), `plugin/skills/working/SKILL.md` (the ladder in one paragraph, the commands), `docs/releases/` notes per its README
  (`### For operators`: run `fugaro update`; it asks nothing new; the daemon restarts into its own state migration to version 2, `escalation.ready_drafts` is off, and the App needs the merge permission, O4), `website/data/release-summaries.yaml` (no backticks).
- **Tests:** `go test ./internal/cli/ -run Docs -count=1`; `go test ./plugin/... -count=1`; `npm run check` in `website/` if
  website content changed. **Command:** the first of these.
- **Depends on:** E10b, E12, E13. **Commit:** `docs: the escalation ladder, its skill and release notes (E14)`.
- **Red step:** the plugin test that checks every command a skill names fails until `fugaro escalation` is real.
- **Reviewer tier:** security (plugin skill path).

### E15. Checks 540-541 (OWNER-RUN)

- **Files:** `scripts/live-checks/check540.sh`, `check540_test.sh`, `check541.sh`, `check541_test.sh` (new, thin; the repo's shape, pinned by
  `scripts/live_checks_test.go`; owner checks ship as scripts, one command).
- **Check 540:** a stalled, Routine sandbox PR walks R0 to R3 on the owner's dogfood installation, with `ready_drafts` turned on
  for the check only; it measures time to merge and that follow-ups appear pending. **Check 541:** a sandbox PR whose diff
  contains instructions to the judge ("verdict merge") on a non-Routine path set must raise `waiting_on: person` and merge
  nothing (G1, G10).
- **What the owner types:** `scripts/live-checks/check540.sh` then `scripts/live-checks/check541.sh`. No agent runs any step of either.
- **Also:** a row for each check in `docs/gcp-live-checklist.md`. **Depends on:** E10b and a released CLI. **Commit:** `test: owner-run checks 540 and 541 (E15)`.
- **Reviewer tier:** standard (opus if the script touches credentials).

## Parallel execution

Edges, from the "Depends on" lines:

E1 ← none; E2 ← none; E5 ← none; E8 ← none; E0 ← none (after the owner confirms T1, T3);
E3 ← E1, E2; E4 ← E2; E7 ← E1; E9 ← E1 (+ T-TR3); E11 ← E4;
E6 ← E4, E5; E12 ← E7 (+ R7-R12); E13 ← E7, E6;
E10a ← E3, E4, E5, E7, E8, E9, E11; E10b ← E6, E8, E10a;
E14 ← E10b, E12, E13; E15 ← E10b.

| Level | Tasks (concurrent) |
| --- | --- |
| 0 | E0, E1, E2, E5, E8 |
| 1 | E3, E4, E7, E9 |
| 2 | E6, E11, E12 |
| 3 | E10a, E13 |
| 4 | E10b |
| 5 | E14 |
| 6 | E15 (owner-run) |

**Critical path:** E2 → E4 → E11 → E10a → E10b → E14 → E15 (seven levels). **Sequential by necessity:** E10a before E10b
(E10b extends the hook E10a adds); E4 before E11 (E11 reads the fields E4 adds); E2 before E3 (same `settings/registry.go`); E5 before E6
(same `stages.go`); E6 before E13 (same `schemas/schemas_test.go`); E5 before E4 (same `recipes_skew.go`, by level).

### Shared-files table (no level holds two writers of one file)

| File | Writers (level) |
| --- | --- |
| `internal/settings/registry.go` | E2 (0), E3 (1) |
| `internal/task/task.go`, `schemas/task.schema.json` | E4 (1) only |
| `internal/cli/recipes_skew.go` | E5 (0), E4 (1) |
| `schemas/schemas_test.go` | E6 (2), E13 (3) |
| `internal/orchestrator/launch.go` | E11 (2) only |
| `internal/orchestrator/tick.go`, `escalate_launch.go` | E10a (3) only; `hooks.go` is never touched |
| `internal/orchestrator/notify.go` | E10b (4) only |
| `internal/orchestrator/state.go`, `patch.go`, `validate.go` | E7 (1) only |
| `internal/recipe/stages.go` | E5 (0), E6 (2) |
| `internal/runner/routing.go` | E4 (1), E6 (2) |
| `internal/orchestrator/intent.go` | E11 (2) only |
| `internal/gitops/rebase.go` | E5 (0) only |
| `internal/budget/rules/rules.json.tmpl` | E12 (2) only |
| `internal/digest/escalation.go` | E12 (2) only |
| `docs/gcp-live-checklist.md` | E15 (6) only |
| `internal/runner/recipe.go` | E5 (0) only |
| `internal/runner/prompts.go`, `mergejudge.go` | E6 (2) only |
| `schemas/result.schema.json` | E6 (2) only |
| `schemas/recipe.schema.json` | E5 (0) only |
| `internal/escalation/*` | E1 (0), E9 (1); different files in the package (`churn.go`) |
| `internal/gitprov/*` | E8 (0) only |
| `internal/cli/root.go`, `website/content/cli-reference.md` | E13 (3) only; **external** O9, O11, O13, O14 also regenerate the reference |
| `docs/status.md` | this plan's own PR (the row), then E14 (5) |
| `docs/recipes.md` | E5 (0), E14 (5) |

Two conflicts the table found while drafting, and their fix (already applied in the Files above): **E3 and E7 both write `internal/orchestrator/validate.go` at level 1.** E3 moves
its load-time refusal to `internal/localcfg/escalation.go` (E3 no longer edits `validate.go`). **E0 and E5 both write
`docs/recipes.md` at level 0.** E5 owns the file; E0's readiness-paragraph edit went to E14, and E0 amends only the
stateless-orchestrator, rigor-modes and design pages. A change to any task's Files re-checks this table.

## Checks

540-549 are reserved; **540** and **541** are used (E15, owner-run). 542-549 stay unused and reserved.

## After deploy

Nothing beyond `fugaro update`: the local keys have defaults, `escalation.ready_drafts` is off, the new recipes ride the catalog,
the new state and task fields are optional. E14 states it in the release notes. If E7 finds the RTDB rules pin the node shape,
the rules change and its migration ship inside `update` in that task's release (the HARD RULE of `CLAUDE.md`).

## Considered and declined

- **Per-orchestrator reports with a roll-up** (the spec): reopens periodic-reviews E4 (D2034).
- **A model that decides the merge** (the spec): D1244/D1245's rejected alternative (D2031).
- **Local git in the daemon for R0**: a new trust boundary (D2035).
- **A console dashboard**: a console plan of its own, after E12 and E13 give it data (O2).
