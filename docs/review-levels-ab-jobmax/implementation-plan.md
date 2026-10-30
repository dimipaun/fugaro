# Review levels, A/B for every slot, job max, crash reports, console graph and gauges: implementation plan

## Status

**Plan only; nothing is implemented; no task is approved.** Each task needs the owner's explicit
go. An answered decision is not a go.

This plan builds [spec.md](spec.md) as amended by [reconciliation.md](reconciliation.md). Where the
two disagree, the reconciliation wins. The owner answered D720–D737 on 2026-10-30, every one as
recommended except **D730** (fully automatic `/jobmax`, not the recommendation). These answers shape
the plan:

- **D720** keeps the `first_line`/`review` keys and adds one level-3 step.
- **D721** refuses L1 = L2 by exact model ID and warns when L2 is the coder's model or in the same family.
- **D722** layers the settings as rigor defaults < project < repository < task, plus a tighten-only owner floor.
- **D723**: the highest level that ran decides readiness.
- **D724**: one versioned task field replaces in-job review's `review_plan`.
- **D725**: effort applies to Claude models only.
- **D726** caps: L1 default 1 (max 3), L2 2, L3 2.
- **D727** skips L2 after an L3 bounce and records each level's approved head.
- **D728**: a reviewer A/B pair replays the challenger reviewer on the champion's patch, judged by A5, with `slot` added before A3.3.
- **D729**: job max is defined in `parallelism`, and `/jobmax` is a plugin skill.
- **D730**: launches are fully automatic, and task J1.2 waits for the tension OT1 ruling.
- **D731** supersedes D255, console D5, the download page's text and Terms §7.3, and drops Firebase Analytics from the app.
- **D732**: no crash reports from the CLI or the job.
- **D733** approves `@sentry/electron`; the processor is named later, so **T2.1 and T2.2 stay draft until it is named**.
- **D734**: nothing is sent before the switch is read; only the error type and frames are sent; mobile reads the switch from pairing, the console from CLI output.
- **D735**: no ELK wrapping; a minimum node scale with a horizontal-scroll fallback.
- **D736**: no ECharts.
- **D737** defines the gauge semantics.

**Waiting on something external:**

- OT1, the owner's ruling, gates J1.2.
- O4, the processor's name, gates T2.1 and T2.2.
- The A/B plan's A3.3 and A5.x gate B1.2 and B1.3.
- The mobile plan's P6.8, the app's pairing consumer, gates T3.1.
- The in-flight work below gates the tasks that share its files.

## Conventions every part follows

- `CLAUDE.md` and the rules file for each area touched: `.claude/rules/console.md` (exact pins, the spawn and IPC allow-lists, the sanitiser, `hidesView`, goldens only by the owner), `.claude/rules/mobile.md` (the scan tests, no logger, `npm test` in `mobile/` and `shared/`), `.claude/rules/website.md` (`npm run check`).
- Targeted tests only, with `GOFLAGS=-p=2`. Never run `internal/cli` or `internal/runner` whole. `gofmt -l .` must print nothing.
- Docs are pinned: after any doc change, run `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`, and `npm run check` in `website/` when website content changes.
- Strict decoders: every new task, record, `fugaro.yaml`, project-layer or recipe key gets its JSON Schema in `schemas/`. A new task field gets a `…Since` constant judged in `internal/cli/recipes_skew.go` (in-job review R5), and a new recipe key joins the recipe's `newKeys` image gate.
- No secret-shaped literal anywhere, the crash-report DSN included: it is injected at build time and never committed. No real names. No live cloud action by an agent: checks are OWNER-RUN.
- Plugin skills stay inside `plugin/skills_lint_test.go`'s budgets (400 lines and 24576 bytes per `SKILL.md`), and every command or flag a skill names is checked against the CLI by `plugin/` tests.
- A user-visible change gets release notes per `docs/releases/README.md`. A step an operator must run after deploy goes in the release's `### For operators` section in the same PR.
- Every new dependency is pinned exactly and recorded in `docs/licensing/dependency-audit.md` and the component's `THIRD_PARTY_NOTICES.md`. D733 approves only `@sentry/electron`, and only for the console.

## Global constraints and review focus

| # | Constraint | Pinned by |
| --- | --- | --- |
| G1 | No layer below the owner's floor can turn a review level off (D722). A task's `none` under the floor is refused, never silently ignored | L1.4, L3.1 |
| G2 | A run with L1 and L2 on the same exact model ID never launches (D721), and the refusal names both settings | L3.1, L3.2 |
| G3 | The worst-case stage bound covers L3: `StageBound` errs high and never low | L2.1, L4.4 |
| G4 | Readiness is decided by the highest level that ran, and only when every enabled outer level's last verdict is `ship` (D723, D727). A verdict that cannot be parsed blocks as it does today | L4.2 |
| G5 | Effort never reaches a provider route (D725). A provider stage's environment keeps `MAX_THINKING_TOKENS=0` | L3.1, L4.3 |
| G6 | A crash report holds the error type and frames only: no message, no absolute path, no user name, no breadcrumbs, no local variables (D734). A scrubber test feeds a hostile message carrying a token-shaped placeholder, a home path and a repository name, and asserts none of them leaves | T2.1, T3.1 |
| G7 | Nothing is sent before the switch is read, and nothing is sent with the switch off (D734) | T2.1, T3.1 |
| G8 | The CLI and the job send no crash report: `go.mod` carries no crash-reporting module (D732) | T1.1 |
| G9 | `/jobmax` never launches a task whose dependency is unmerged, and never launches past a cap (OT1's proposal, pending the ruling) | J1.2 |
| G10 | The A/B `slot` field is fixed before the pair launch writes pairs (D728), so pairs are not written twice in two formats | B1.1 before A3.3 |

**Trust boundaries to probe in review:**

- The task field is written by the launcher, so a hostile task could set a level to `none` or name a model. G1 and `allowed_models` hold, and the runner re-checks both at bootstrap.
- The `effort` value is passed into the agent's environment, so it is accepted only from a closed set.
- A crash payload leaves the machine: G6, and the DSN cannot be redirected by configuration.
- The pairing delivery is sealed and compared field by field (D199): T1.3 adds a field outside the compared four.
- `/jobmax` reads plan files from the repository, which are untrusted text: the graph is parsed by `fugaro plan graph`, never by the skill, and a task's text goes to `fugaro run` on stdin.

## In-flight work this plan waits for (file conflicts)

| In flight | Files shared with this plan | Tasks that wait |
| --- | --- | --- |
| #1362 (forge A2b, run 50f1) | `internal/config/config.go`, `internal/cli/run.go`, `internal/cli/validate.go`, `internal/cli/recipes_skew.go`, `internal/localcfg/localcfg.go`, `internal/localcfg/shared.go`, `schemas/fugaro.schema.json` | L1.2, L1.3, L3.2, T1.1 |
| #1360 (C8b0, run 720d) | `internal/config/config.go`, `schemas/fugaro.schema.json`, `schemas/project-layer.schema.json` | L1.3 |
| #1358 (run 5f0c; the A/B A3 queue follows it on the same file) | `internal/runner/runner.go` | L4.2, B1.2 |
| #1363 (pairing T4) | `internal/cli/pair.go` | T1.3 |
| #1332 (mobile P7.1b) | `mobile/app.config.js` | T3.1 |
| Console Papaya steps 4b-2 to 7 (#1325 and its successors) | `console/src/renderer/theme/flight-deck.css`, `screens.css`, `console/src/renderer/screens/live.ts`, `run.ts`, `docs/design/console-styling.md` | G1.1, V1.2, V1.3 |

## Slices and tasks

Six slices. **L** (review levels) and **J** (job max) are useful alone. **B** (A/B slots) needs L and the
A/B plan. **T** (crash reports) ships per client. **G** (graph) and **V** (gauges) are console-only.
Every task's reviewer tier is **standard** unless marked otherwise.

### Slice L: review levels 1–3

#### L1.1 The task's `review_levels` field (D724)

- **Files:** `internal/task/task.go`, `schemas/task.schema.json`, `internal/task/task_test.go`.
- **Interfaces:** produces `task.ReviewLevels{L1, L2, L3 *LevelSetting}`, where `LevelSetting{Model, Effort, Off bool, Focus string}`. `Off` is the YAML/JSON `none`. `Focus` is at most 500 bytes and is never put through a shell. Consumed by L3.1 and L4.2. Inert until L3.1.
- **Schema change:** `task.schema.json` gains `review_levels` (closed objects, `additionalProperties: false`).
- **Tests:** `TestReviewLevelsRoundTrip`, `TestReviewLevelsRefusesUnknownKey`, `TestReviewLevelsFocusBound`, and `schemas/schemas_test.go`'s existing schema-vs-struct check.
- **Command:** `GOFLAGS=-p=2 go test ./internal/task ./schemas -run 'ReviewLevels|Schema' -count=1`.
- **Depends on:** none.
- **Red step:** `TestReviewLevelsRoundTrip` fails while `task.Parse` refuses the unknown field `review_levels`.
- **Commit:** `task: the review_levels field (review levels L1.1)`.

#### L1.2 The version gate and the `--review-levels` flag

- **Files:** `internal/cli/recipes_skew.go`, `internal/cli/run.go`, `internal/cli/recipes_skew_test.go`, `internal/cli/run_review_test.go`.
- **Interfaces:** consumes L1.1. Produces `reviewLevelsSince` and `fugaro run --review-levels <file|->` (YAML, decoded strictly into `task.ReviewLevels`). A job image older than `reviewLevelsSince` refuses the launch, naming `fugaro image refresh`.
- **Tests:** `TestRecipesSkewRefusesReviewLevelsOnOldImage`, `TestRunReviewLevelsFlagWritesTaskField`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'RecipesSkew|RunReviewLevels' -count=1`.
- **Depends on:** L1.1, #1362.
- **Red step:** the skew test fails because no constant judges the field.
- **Commit:** `run: --review-levels, gated by the job image's version`.

#### L1.3 Per-level settings in `fugaro.yaml` and the project layer (D722, D726)

- **Files:** `internal/config/config.go`, `internal/config/layer.go`, `internal/config/validate.go`, `internal/config/defaults.go`, `schemas/fugaro.schema.json`, `schemas/project-layer.schema.json`, `internal/config/config_test.go`, `internal/config/layer_test.go`.
- **Interfaces:** produces `agent.levels.{l1,l2,l3}.{model, effort, none, focus}` in `fugaro.yaml` and in the project layer's `defaults.agent.levels`. Also produces `agent.adversarial_rounds` (1–10, default 2, `config.DefaultAdversarialRounds`). `first_line_rounds` stays default 1 and max 3; `review_rounds` stays default 2.
- **Tests:** `TestLevelsDecodeStrict`, `TestAdversarialRoundsDefaultAndBounds`, `TestLayerLevelsOverlay`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/config ./schemas -run 'Levels|AdversarialRounds|Schema' -count=1`.
- **Depends on:** #1362, #1360.
- **Red step:** `TestLevelsDecodeStrict` fails because `levels` is an unknown field.
- **Commit:** `config: per-level review settings and agent.adversarial_rounds`.

#### L1.4 The owner's level floor, tighten-only (D722)

- **Files:** `internal/config/layer.go`, `schemas/project-layer.schema.json`, `internal/policy/policy.go`, `internal/policy/policy_test.go`.
- **Interfaces:** produces a top-level project-layer key `review_floor: {<mode>: [l1|l2|l3, …]}` (top level for the reason `ab:` and `governance:` are: a key under `defaults:` would let a repository replace it). `policy.Effective.ReviewFloor(mode)` is merged as a union, so only tightening wins.
- **Tests:** `TestReviewFloorMergesTightenOnly`, `TestReviewFloorRepositoryCannotLower`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/policy ./internal/config -run 'ReviewFloor' -count=1`.
- **Depends on:** L1.3.
- **Red step:** the merge test fails because the key does not exist.
- **Reviewer:** opus + mandatory independent review (a policy merge that could loosen).
- **Commit:** `policy: the owner's review-level floor, merged tighten-only`.

#### L2.1 The level-3 recipe step (D720, D726)

- **Files:** `internal/recipe/recipe.go`, `internal/recipe/stages.go`, `schemas/recipe.schema.json`, `docs/recipes.md`, `docs/design/recipes.md`, `internal/recipe/recipe_test.go`, `internal/recipe/stages_test.go`.
- **Interfaces:** produces `recipe.StepAdversarial` (`adversarial: { max_rounds, bounce: first_line }`): at most once, after `review`, and `bounce` may target only `first_line`. `StageBound` adds `n₃·(2 + 2·f)` with a bounce. It joins `newKeys`.
- **Tests:** `TestAdversarialStepOrder`, `TestAdversarialBounceTarget`, `TestStageBoundWithAdversarial`, which pins 11 for today's `standard` at the defaults and 19 with L3 at f = 1, n = 2, n₃ = 2.
- **Command:** `GOFLAGS=-p=2 go test ./internal/recipe -count=1`, then `GOFLAGS=-p=2 go test ./internal/cli -run 'DocsRecipes' -count=1`.
- **Depends on:** none.
- **Red step:** a recipe with `adversarial:` is refused as an unknown step.
- **Commit:** `recipe: the adversarial step (review level 3)`.

#### L2.2 Per-rigor level defaults (D722, R2; folds rigor LV6.1)

- **Files:** `internal/rigor/levels.go` (new), `internal/rigor/rounds.go`, `internal/rigor/levels_test.go` (new).
- **Interfaces:** produces `rigor.DefaultLevels(mode)`: POC {L1}; Startup and Pro {L1, L2}; Critical {L1, L2, L3}; Hotfix {L1, L2}. Also produces `rigor.AdversarialCeiling(mode)`, held to `minRoundCeiling` while rigor O3 is open.
- **Tests:** `TestDefaultLevelsPerMode`, `TestAdversarialCeilingNeverRaises`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/rigor -count=1`.
- **Depends on:** none.
- **Red step:** the package does not compile without `DefaultLevels`, and the test fails.
- **Commit:** `rigor: the levels each mode runs by default`.

#### L3.1 The level resolver and the guardrail (D721, D722, D723, D725)

- **Files:** `internal/config/levels.go` (new), `internal/config/levels_test.go` (new).
- **Interfaces:** consumes L1.1, L1.3, L1.4 and L2.2. Produces `config.ResolveLevels(cfg, mode, floor, task) (Levels, []Warning, error)`, with precedence rigor defaults < project < repository < task, and the floor applied last.
  - Refuses L1 = L2 by exact model ID.
  - Warns when L2 equals the coder's model, or when L1 and L2 share a vendor family (`config.ModelFamily`: a Claude ID is `anthropic`, a provider ID is its prefix; A/B D553 reuses it).
  - Refuses `effort` for a model a provider claims, with the reason.
  - Accepts `effort` only from Claude Code's closed set (U1).
- **Tests:** `TestResolveLevelsPrecedence`, `TestResolveLevelsFloorRefusesNone`, `TestResolveLevelsRefusesSameModel`, `TestResolveLevelsWarnsCoderAndFamily`, `TestResolveLevelsRefusesProviderEffort`, `TestResolveLevelsSoloUnaffected`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/config -run 'ResolveLevels' -count=1`.
- **Depends on:** L1.1, L1.3, L1.4, L2.2.
- **Red step:** `TestResolveLevelsRefusesSameModel` fails before the refusal exists.
- **Reviewer:** opus + mandatory independent review (the floor and the guardrail are policy).
- **Commit:** `config: resolve review levels, the owner floor and the same-model refusal`.

#### L3.2 `fugaro validate` and launch apply the resolver

- **Files:** `internal/cli/validate.go`, `internal/cli/run.go`, `internal/cli/validate_test.go`, `internal/cli/run_review_test.go`.
- **Interfaces:** consumes L3.1 and L1.2. `validate` prints the warnings and refusals, and `--json` carries them. Launch refuses before writing the task.
- **Tests:** `TestValidateLevelsSameModel`, `TestRunRefusesLevelsBelowFloor`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'ValidateLevels|RunRefusesLevels' -count=1`.
- **Depends on:** L3.1, L1.2.
- **Red step:** validate passes a same-model configuration before the change.
- **Commit:** `validate, run: the review-level checks at the launcher`.

#### L4.1 The record: level-3 tier and each level's approved head (D727)

- **Files:** `internal/runstore/runstore.go`, `schemas/result.schema.json`, `internal/runstore/runstore_test.go`.
- **Interfaces:** produces `runstore.TierAdversarial` and `ReviewSummary.Head` (the commit the review read; omitted when empty). Consumed by L4.2, by the PR body and by B1.1.
- **Tests:** `TestReviewSummaryHeadRoundTrip`, `TestTierAdversarialInSchema`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/runstore ./schemas -count=1`.
- **Depends on:** none.
- **Red step:** the schema test fails on the unknown tier.
- **Commit:** `runstore: the adversarial tier and the head each review read`.

#### L4.3 Effort for a Claude stage (D725)

- **Files:** `internal/agent/env.go`, `internal/agent/env_test.go`.
- **Interfaces:** produces `agent.EffortVars(effort string, provider bool) map[string]string`. It is empty for a provider stage, and for "" it is empty too. The variable or flag it sets is what U1 finds. **The task's first commit records U1's answer**, measured with the fake `claude` against the pinned Claude Code version, and stops if Claude Code has no per-process effort setting.
- **Tests:** `TestEffortVarsClaudeOnly`, `TestEffortVarsNeverOnProviderStage`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/agent -run 'EffortVars' -count=1`.
- **Depends on:** none.
- **Red step:** an undefined function fails the test.
- **Commit:** `agent: effort for a Claude stage, never a provider's`.

#### L4.2 The runner runs level 3, skips L2 after an L3 bounce, and decides by the highest level (D723, D727)

- **Files:** `internal/runner/runner.go`, `internal/runner/outcome.go`, `internal/runner/prflow.go`, `internal/runner/levels_test.go` (new).
- **Interfaces:** consumes L1.1, L2.1, L3.1, L4.1 and L4.3.
  - After `review`, the runner runs `adversarial`. An L3 rejection runs the fix, then the L1 loop, then L3 again, never L2.
  - `Decide` reads the highest level that ran and requires every enabled outer level's last verdict to be `ship`.
  - The PR body lists each level's verdict and head.
  - A level marked `none` does not run.
  - The bootstrap re-checks the floor and `allowed_models` against the task.
- **Tests:** `TestLevelsL3BounceSkipsL2`, `TestLevelsReadinessHighestLevel`, `TestLevelsPOCL1Decides`, `TestLevelsBootstrapRefusesBelowFloor`, `TestLevelsPRBodyShowsHeads`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/runner -run 'TestLevels' -count=1 -timeout 30m`.
- **Depends on:** L1.1, L2.1, L3.1, L4.1, L4.3, #1358.
- **Red step:** `TestLevelsL3BounceSkipsL2` fails because the runner rejects the step.
- **Reviewer:** opus + mandatory independent review (readiness).
- **Commit:** `runner: review level 3, its bounce, and readiness by the highest level`.

#### L4.4 `budget preview` counts level 3

- **Files:** `internal/cli/budget_preview.go`, `internal/cli/budget_preview_test.go`.
- **Interfaces:** consumes L1.3 and L2.1. The preview's knobs gain the L3 rounds.
- **Tests:** `TestBudgetPreviewAdversarialStages`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'BudgetPreview' -count=1`.
- **Depends on:** L1.3, L2.1.
- **Red step:** the preview reports 11 for a recipe whose bound is 19.
- **Commit:** `budget preview: the level-3 rounds in the bound`.

#### L5.1 Docs: levels, supersessions, release notes

- **Files:** `docs/recipes.md`, `docs/in-job-review/README.md` (a status line: §4's `review_plan` is superseded by D724), `docs/rigor-modes/levels-implementation-plan.md` (LV6.1 folded into L2.2), `plugin/skills/working/SKILL.md` (the `--review-levels` flag), `docs/releases/` (the release's notes).
- **Tests:** the docs tests and the plugin checks.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1 && GOFLAGS=-p=2 go test ./plugin -count=1`.
- **Depends on:** L4.2, L3.2, J1.1 (which also writes `working`).
- **Red step:** the plugin check fails while the skill names a flag the CLI lacks. This task writes both together, so the red is the flag's absence before L1.2.
- **Commit:** `docs: review levels 1-3`.

### Slice B: A/B for the review slots (amends docs/model-ab-testing)

#### B1.1 The `slot` field on the pair (D728, before A3.3)

- **Files:** `internal/task/task.go`, `schemas/task.schema.json`, `internal/runstore/runstore.go`, `schemas/result.schema.json`, `docs/model-ab-testing/implementation-plan.md` (D555 amended: a coder pair still holds every reviewer fixed; A3.3 now depends on B1.1).
- **Interfaces:** produces `task.AB.Slot` ∈ {`coder`, `l1`, `l2`, `l3`}. Empty means `coder`, so records written by A1.1 still read.
- **Tests:** `TestABSlotVocabulary`, `TestABSlotDefaultsToCoder`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/task ./internal/runstore ./schemas -run 'ABSlot|Schema' -count=1`.
- **Depends on:** L1.1 and L4.1 (same files, earlier level). Must land before A3.3.
- **Red step:** an unknown field `slot` is refused.
- **Commit:** `task, runstore: the A/B pair's slot`.

#### B1.2 The reviewer replay (MIT mechanism)

- **Files:** `internal/runner/abreplay.go` (new), `internal/runner/runner.go`, `internal/runner/abreplay_test.go` (new).
- **Interfaces:** a challenger with `slot: lN` checks out the champion's base, applies the champion's patch (`runs/<slug>/<run-id>/ab/patch.diff`), runs one level-N review with the challenger model and publishes nothing (`ab.publish: none`). Its findings are recorded for the judge.
- **Tests:** `TestABReplayReviewsChampionPatch`, `TestABReplayPublishesNothing`, `TestABReplayRefusesPatchFromOtherRun`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/runner -run 'ABReplay' -count=1 -timeout 30m`.
- **Depends on:** B1.1, L4.2, A3.3, A5.2.
- **Red step:** a `slot: l2` challenger runs a full coding loop.
- **Reviewer:** opus + mandatory independent review (it reads another run's object).
- **Commit:** `runner: an A/B reviewer arm replays the champion's patch`.

#### B1.3 Per-slot scoring and `fugaro ab` grouping (`ee/`)

- **Files:** `ee/ab/` (the scoring and report files A4.1–A4.3 create).
- **Interfaces:** a reviewer pair's signals are misses (findings the judge matched in the champion's same-level review that the challenger lacked), false alarms (judge-marked) and cost. `fugaro ab` groups results by slot × model × effort. There is no per-person dimension (D560).
- **Tests:** `TestABScoreReviewerSlot`, `TestABReportGroupsBySlot`.
- **Command:** `GOFLAGS=-p=2 go test -tags ee ./ee/ab/... -count=1`.
- **Depends on:** B1.2, A4.3, A5.4.
- **Red step:** a reviewer pair is scored with coder signals.
- **Commit:** `ab: score and report the review slots`.

### Slice J: job max

#### J1.1 Job max in the `parallelism` skill (D729)

- **Files:** `plugin/skills/parallelism/SKILL.md`, `plugin/skills/working/SKILL.md` (a one-line pointer), `plugin/parallelism_skill_test.go`.
- **Content:** job max is "the most completed, reviewed runs in parallel, not tokens spent; only real dependencies order runs". Planning advice: contracts first, stubs and fakes so neither side waits, the graph kept wide and shallow. The skill's "one yes per batch" rule stays until OT1 is ruled.
- **Tests:** `TestParallelismSkillDefinesJobMax` (the phrase and the three techniques), plus the lint.
- **Command:** `GOFLAGS=-p=2 go test ./plugin -count=1`.
- **Depends on:** none.
- **Red step:** the new test fails on the current skill.
- **Commit:** `plugin: job max in the parallelism skill`.

#### J1.2 The `/jobmax` skill (D729, D730; BLOCKED on OT1)

- **Files:** `plugin/skills/jobmax/SKILL.md` (new), `plugin/jobmax_skill_test.go` (new), `plugin/skills/parallelism/SKILL.md` (the `/jobmax` exception to "one yes per batch", as OT1 rules), `plugin/.claude-plugin/plugin.json` (description).
- **Interfaces:**
  - Reads `fugaro plan graph --plan <p> --json` and `fugaro plan status --json`.
  - Launches every task whose dependencies are all merged with `fugaro run --batch <plan> --run-id <plan-task> --task-file -`, inside the caps (OT1's guardrails as ruled).
  - Prints what it launched and the worst case.
  - Stops on any refusal. Running it again launches what has become ready. No waiting loop.
- **Tests:** `TestJobmaxSkillNamesOnlyRealCommands` (the plugin check), `TestJobmaxSkillRequiresMergedDependencies`, `TestJobmaxSkillStopsOnCap`.
- **Command:** `GOFLAGS=-p=2 go test ./plugin -count=1`.
- **Depends on:** J1.1 and the OT1 ruling.
- **Red step:** the plugin check fails on a skill that does not exist.
- **Commit:** `plugin: /jobmax launches every ready task of a plan`.

### Slice T: crash reports (console and mobile; none from the CLI)

#### T0.1 Record the supersessions (D731)

- **Files:** `docs/design/desktop-console.md` (D5 marked superseded by D731), `docs/plans/2026-10-19-mobile-telemetry.md` (D255 superseded; Analytics dropped; the §7.4 scan refuses `setAnalyticsCollectionEnabled(` and admits exactly one `setCrashlyticsCollectionEnabled(` call site), `docs/mobile/implementation-plan.md` (P7.2/P7.3 re-scoped), `internal/cli/docs_mobile_telemetry_test.go` (its pinned facts move with the text).
- **Tests and command:** `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Depends on:** none.
- **Red step:** the mobile telemetry docs test fails on the old "always on" pin until both move together.
- **Commit:** `docs: crash reports are opt-out; the app drops Analytics (D731)`.

#### T1.1 The switch: `telemetry.crash_reports` in the project config (D732, D734)

- **Files:** `internal/localcfg/localcfg.go`, `internal/localcfg/shared.go`, `internal/localcfg/localcfg_test.go`, `internal/cli/docs_no_crash_reporter_test.go` (new: G8).
- **Interfaces:** produces `localcfg.Telemetry{CrashReports *bool}` (nil means true), published with the shared configuration so teammates' consoles read the owner's value.
- **Tests:** `TestTelemetryCrashReportsDefaultTrue`, `TestTelemetrySharedRoundTrip`, `TestGoModHasNoCrashReporter`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/localcfg -run Telemetry -count=1 && GOFLAGS=-p=2 go test ./internal/cli -run 'GoModHasNoCrashReporter' -count=1`.
- **Depends on:** #1362.
- **Red step:** the unknown key is refused.
- **Commit:** `localcfg: telemetry.crash_reports, on unless the owner turns it off`.

#### T1.2 `config list --json` prints the switch

- **Files:** `internal/cli/config_list.go`, `internal/cli/config_list_test.go`, `console/docs/json-contract.md`.
- **Interfaces:** each installation gains `"crash_reports": true|false`. This is an existing allow-listed console argv (`installation.list`), so no spawn or IPC change.
- **Tests:** `TestConfigListCrashReports`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'ConfigListCrashReports|Docs' -count=1`.
- **Depends on:** T1.1.
- **Red step:** the field is absent.
- **Commit:** `config list: each installation's crash-report switch`.

#### T1.3 The pairing delivery carries the switch (D734, O5)

- **Files:** `internal/budget/token/device.go`, `internal/cli/pair.go`, `internal/budget/token/device_test.go`, `internal/cli/pair_test.go`, `docs/plans/2026-10-22-mobile-pairing-cli.md`.
- **Interfaces:** `token.Delivery` gains `CrashReports bool`, kept **outside** `DeliveryConfig`, whose four fields the phone compares with the QR (D199). The doc states O5: a changed switch reaches a phone at its next pairing.
- **Tests:** `TestDeliveryCarriesCrashReports`, `TestDeliveryConfigStillFourFields`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/budget/token ./internal/cli -run 'Delivery|Pair' -count=1`.
- **Depends on:** T1.1, #1363.
- **Red step:** the field is absent from the payload.
- **Reviewer:** opus + mandatory independent review (the pairing delivery).
- **Commit:** `pair: the delivery carries the crash-report switch`.

#### T2.1 The console reporter (D733, D734; DRAFT until the processor is named)

- **Files:** `console/package.json`, `console/package-lock.json` (`@sentry/electron`, exact), `console/src/main/crash.ts` (new), `console/src/main/index.ts`, `console/src/shared/schema.ts` (the `crash_reports` field), `console/test/unit/crash.test.ts` (new), `console/THIRD_PARTY_NOTICES.md`, `docs/licensing/dependency-audit.md`, `docs/design/desktop-console.md` (a new section: what is sent).
- **Interfaces:**
  - Initialises only after `installation.list` returns `crash_reports: true` for the chosen installation, and closes the client when the switch reads false.
  - `beforeSend` keeps the exception type and frames (function, app-relative file, line), drops the message, and drops breadcrumbs, contexts, user, request and local variables.
  - The DSN comes from a build-time environment variable. A build without it sends nothing.
- **Tests:** `crash: nothing before the switch is read`, `crash: nothing with the switch off`, `crash: the scrubber drops message, home path and token-shaped placeholder`, `pinning: @sentry/electron is exact`.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 crash pinning`.
- **Depends on:** T1.2, the processor named (O4).
- **Red step:** `crash: nothing before the switch is read` fails against a stub that initialises at startup.
- **Reviewer:** opus + mandatory independent review.
- **OWNER-RUN part:** the owner sets the DSN as the console build's secret, never in the repository. The release's `### For operators` names the variable.
- **Commit:** `console: crash reports, opt-out, stack frames only`.

#### T2.2 The console's public statements (D731)

- **Files:** `website/content/downloads.json` (the `no-telemetry` property rewritten: "No telemetry and no analytics; crash reports (stack traces only) are on by default and can be turned off in the project config"), `website/test/console.mjs`, `README.md` (the console sentence).
- **Tests and command:** `cd website && npm run check`, then `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Depends on:** T2.1 (the statement ships with the code).
- **Red step:** `website/test/console.mjs` fails until the property and the test move together.
- **Commit:** `website, README: the console's crash-report statement`.

#### T3.1 The app: opt-out Crashlytics, no Analytics (D731, D734)

- **Files:** `mobile/package.json` (drop `@react-native-firebase/analytics`), `mobile/app.config.js`, `mobile/firebase.json` (`crashlytics_auto_collection_enabled: false`, Analytics keys removed), `mobile/src/telemetry/crash.js` (new: the one `setCrashlyticsCollectionEnabled(` call, from the paired switch), `mobile/src/telemetry/crash.test.js` (new), the §7.4 scan test as amended in T0.1, `website/src/pages/privacy.astro` (the app paragraph: Crashlytics, opt-out, no Analytics).
- **Interfaces:** consumes the delivery's `crash_reports` (T1.3) through the app's pairing store (mobile P6.8). Until a pairing is stored, collection stays off and unsent reports are deleted (U2).
- **Tests:** `crash: off until paired`, `crash: follows the paired switch`, `scan: exactly one setCrashlyticsCollectionEnabled call site, no analytics import`.
- **Command:** `cd mobile && npm test`, then `cd website && npm run check`.
- **Depends on:** T0.1, T1.3, #1332, mobile P6.8.
- **Red step:** the scan finds the analytics package imported.
- **Reviewer:** opus + mandatory independent review.
- **Commit:** `mobile: Crashlytics follows the project switch; Analytics removed`.

#### T3.2 OWNER-RUN: the store privacy forms (O6)

The owner updates the Play Data safety form (remove Analytics; Crash logs optional) and the App Store
privacy labels in the two consoles, then records the date in `docs/mobile/implementation-plan.md`.
No agent performs any step. **Depends on:** T3.1 shipped to a release.

#### T4 OWNER/COUNSEL: Terms §7.3 (O1, O2)

Counsel reviews the draft in reconciliation §4 O2 and the EU opt-out question. The owner names the
processor (O4). No agent edits the Terms.

### Slice G: the graph fits the width (D735)

#### G1.1 Minimum scale and horizontal-scroll fallback

- **Files:** `console/src/renderer/screens/graph.ts`, `console/src/renderer/theme/screens.css`, `console/test/unit/graph.test.ts`, `console/test/unit/layout-client.test.ts`, `docs/design/console-styling.md`.
- **Interfaces:** `GRAPH_MIN_SCALE` (the node width at which a title stays legible). When the panel width divided by the picture's width falls below it, the canvas takes `width = picture width × GRAPH_MIN_SCALE` inside a wrapper that scrolls horizontally, and that graph only. `LAYOUT_OPTIONS` stays `layered` and `DOWN` with no wrapping key.
- **Tests:** `graph: fits the width above the minimum scale`, `graph: scrolls horizontally below it`, `layout: no wrapping option`.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 graph layout`.
- **Depends on:** the Papaya steps that edit `screens.css` and `console-styling.md`.
- **Red step:** the minimum-scale test fails while the canvas always fits.
- **OWNER-RUN part:** the graph goldens are re-recorded by the owner.
- **Commit:** `console: the graph keeps a legible scale and scrolls sideways only when it must`.

### Slice V: live gauges (D736, D737)

#### V1.1 `budget show --json` prints `max_parallel` and today's headroom

- **Files:** `internal/cli/budget.go`, `internal/cli/budget_show_totals_test.go`, `console/docs/json-contract.md`.
- **Interfaces:** adds `max_parallel` and the UTC day's remaining headroom (if not already printed; the task's first commit says which).
- **Tests:** `TestBudgetShowMaxParallel`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'BudgetShow|Docs' -count=1`.
- **Depends on:** none.
- **Red step:** the field is absent.
- **Commit:** `budget show: max_parallel for the console's cap bars`.

#### V1.2 The spend-rate gauge, one-hour sparklines and cap bars on Live

- **Files:** `console/src/renderer/screens/live.ts`, `console/src/renderer/theme/flight-deck.css`, `console/src/shared/schema.ts`, `console/test/unit/live.test.ts`, `docs/design/console-styling.md`.
- **Interfaces:**
  - An SVG gauge in $/h. Its red zone starts at the rate that would use up the day's remaining headroom before the UTC day ends.
  - `HourHistory` is a second bounded history (one hour, kept only while the console runs) beside the 5-minute `BurnHistory`, whose pin to `BurnWindow` is untouched.
  - Cap bars: spend and concurrent runs against `max_parallel`.
  - Every figure goes through `hidesView` and `formatReading`.
- **Tests:** `live: red zone from headroom and hours left`, `live: hour history bounded`, `live: burn window still equals BurnWindow`, `live: concurrent bar against max_parallel`.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 live`.
- **Depends on:** V1.1, the Papaya steps that edit `live.ts`, `flight-deck.css` and `console-styling.md`.
- **Red step:** the red-zone test fails because no gauge exists.
- **OWNER-RUN part:** the Live goldens.
- **Commit:** `console: the spend-rate gauge, hour sparklines and cap bars`.

#### V1.3 The iterations bar on the Run screen

- **Files:** `console/src/renderer/screens/run.ts`, `console/src/renderer/theme/screens.css`, `console/test/unit/run.test.ts`, `docs/design/console-styling.md`.
- **Interfaces:** review rounds used against each level's cap, read from the run document's `reviews` (tier and round). If the cap is not in the document, the task adds it to the CLI's run document first, and its first commit says which.
- **Tests:** `run: iterations bar per level`.
- **Command:** `cd console && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 run`.
- **Depends on:** L4.1, G1.1 and V1.2 (same stylesheet and styling doc, earlier levels).
- **Red step:** the bar is absent.
- **Commit:** `console: the run's review rounds against their caps`.

### X1 Checks 270–272, written into the register

- **Files:** `docs/gcp-live-checklist.md`.
- **The checks:**
  - **270:** a Critical run on the sandbox repository whose L3 rejects once. The PR body shows L2's head older than L3's, and `result.json` records tiers `first`, `senior` and `adversarial`.
  - **271:** a console crash, forced with a debug build's test trigger, arrives at the processor with type and frames only. With `crash_reports: false`, none arrives.
  - **272:** the same for the app on a development build, before pairing and after pairing with the switch off.
- All three are **OWNER-RUN, NOT RUN**.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Depends on:** L4.2, T2.1, T3.1.
- **Red step:** none (a register entry). The docs test holds the numbers.
- **Commit:** `checklist: checks 270-272 for review levels and crash reports`.

## Parallel execution

**Edges** (from the Depends-on lines; external gates in brackets):

- L1.1 → L1.2 [#1362]
- [#1362, #1360] → L1.3 → L1.4
- L1.1, L1.3, L1.4, L2.2 → L3.1
- L3.1, L1.2 → L3.2
- L1.1, L2.1, L3.1, L4.1, L4.3 [#1358] → L4.2
- L1.3, L2.1 → L4.4
- L4.2, L3.2, J1.1 → L5.1
- L1.1, L4.1 → B1.1 [before A3.3]
- B1.1, L4.2 [A3.3, A5.2] → B1.2
- B1.2 [A4.3, A5.4] → B1.3
- J1.1 [OT1] → J1.2
- [#1362] → T1.1 → T1.2 → T2.1 [O4] → T2.2
- T1.1 [#1363] → T1.3 → T3.1 [T0.1, #1332, P6.8] → T3.2
- [Papaya] → G1.1
- V1.1 [Papaya] → V1.2
- L4.1, G1.1, V1.2 → V1.3
- L4.2, T2.1, T3.1 → X1

**Levels** (the tasks in one level run concurrently once their external gates are open):

| Level | Tasks |
| --- | --- |
| 0 | L1.1, L2.1, L2.2, L4.1, L4.3, J1.1, T0.1, V1.1 |
| 1 | L1.2, L1.3, B1.1, J1.2, T1.1, G1.1 |
| 2 | L1.4, L4.4, T1.2, T1.3, V1.2 |
| 3 | L3.1, T2.1, T3.1, V1.3 |
| 4 | L3.2, L4.2, T2.2, T3.2 |
| 5 | L5.1, B1.2, X1 |
| 6 | B1.3 |

**Critical path (inside this plan):** L1.3 → L1.4 → L3.1 → L4.2 → L5.1 / X1, five tasks. It starts when
#1362 and #1360 land, and L4.2 also waits for #1358. The A/B chain (B1.1 → B1.2 → B1.3) is longer in
calendar time because it waits for the A/B plan's A3.3 and A5.x, but it does not hold up the levels.
Slices J, G and V are off the critical path. Slice T's console half waits on the processor's name (O4).

**What must be sequential and why:**

- L1.4 after L1.3: both edit `layer.go` and `project-layer.schema.json`.
- L3.1 after L1.4: it applies the floor.
- L4.2 after L3.1: it consumes the resolved levels.
- B1.1 after L1.1 and L4.1: the same files.
- V1.3 after G1.1 and V1.2: the same stylesheet and styling doc.

**Shared-files table** (every file with more than one writer; no level holds two writers of a file):

| File | Writers (level) |
| --- | --- |
| `internal/task/task.go` | L1.1 (0), B1.1 (1) |
| `schemas/task.schema.json` | L1.1 (0), B1.1 (1) |
| `internal/runstore/runstore.go` | L4.1 (0), B1.1 (1) |
| `schemas/result.schema.json` | L4.1 (0), B1.1 (1) |
| `internal/config/layer.go` | L1.3 (1), L1.4 (2) |
| `schemas/project-layer.schema.json` | L1.3 (1), L1.4 (2) |
| `internal/cli/run.go` | L1.2 (1), L3.2 (4) |
| `internal/runner/runner.go` | L4.2 (4), B1.2 (5) |
| `docs/recipes.md` | L2.1 (0), L5.1 (5) |
| `plugin/skills/parallelism/SKILL.md` | J1.1 (0), J1.2 (1) |
| `plugin/skills/working/SKILL.md` | J1.1 (0), L5.1 (5) |
| `docs/design/desktop-console.md` | T0.1 (0), T2.1 (3) |
| `docs/mobile/implementation-plan.md` | T0.1 (0), T3.2 (4) |
| `console/docs/json-contract.md` | V1.1 (0), T1.2 (2) |
| `console/src/shared/schema.ts` | V1.2 (2), T2.1 (3) |
| `docs/design/console-styling.md` | G1.1 (1), V1.2 (2), V1.3 (3) |
| `console/src/renderer/theme/screens.css` | G1.1 (1), V1.3 (3) |
| `README.md` | T2.2 (4) only |
| `docs/gcp-live-checklist.md` | X1 (5) only |

Every other file in the plan has one writer.

## Check numbers this plan takes

| Check | What it measures | Who runs it |
| --- | --- | --- |
| 270 | A Critical run's L3 bounce skips L2, readiness follows the highest level, and the PR body shows each level's head | the owner, on the sandbox installation; NOT RUN |
| 271 | The console's crash report holds type and frames only, and none is sent with the switch off | the owner; NOT RUN |
| 272 | The app sends none before pairing or with the switch off, and type and frames only otherwise | the owner, on a development build; NOT RUN |

Checks 273–289 stay reserved and unused.

## After deploy

- **T2.1:** the console's release notes, `### For operators`, name the build-time DSN variable. The owner sets it as a build secret before the release build.
- **T3.1:** phones paired before the release keep collection off until they pair again (O5). The mobile release notes say so.
- **L1.2:** job images must be refreshed (`fugaro image refresh`) before any launcher sets `--review-levels`. The release's `### For operators` says so.
