# Review levels, A/B for every slot, job max, crash reports, console graph and gauges: implementation plan

## Status

**Approved; nothing is implemented yet.** The owner approved every task on 2026-10-30, with these
exceptions: T2.1 and T2.2 stay draft until the processor is named (O4); O1 and O2 are counsel's; T3.2
(O6), the checks X1 and X2 write (270–273) and every OWNER-RUN part below are the owner's. The owner
also named L5.2's model: `claude-opus-5-5` as this repository's level-2 reviewer. *(**Superseded
2026-10-30 (evening):** L5.2's level-2 model is `deepseek/deepseek-v4-pro` through the same
OpenRouter route as the coder, `agent.auth` stays `api-key`, and no new secret is stored — the
per-stage OAuth reviewer, mixed-credentials.md §5 Path B, is not built. Shipped in PR "fugaro.yaml:
deepseek-v4-pro as the level-2 reviewer (O11)".)* CLI work ships in 0.15.0 and console work in
console 0.116.0.

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
- **D730** (ruled 2026-10-30, not the recommendation): `/jobmax` is fully automatic, with guardrails. It launches only inside the owner-set caps, only when every dependency is merged, and it stops and asks on any cap hit or ambiguity. The permission is a per-project opt-in the owner sets, off by default and revocable (J1.3), and with it off, the `parallelism` skill's one-yes-per-batch rule stays.
- **D731** supersedes D255, console D5, the download page's text and Terms §7.3, and drops Firebase Analytics from the app.
- **D732**: no crash reports from the CLI or the job.
- **D733** approves `@sentry/electron`; the processor is named later, so **T2.1 and T2.2 stay draft until it is named**.
- **D734**: nothing is sent before the switch is read; only the error type and frames are sent; the console reads it from CLI output. As amended by the owner's O9 ruling (2026-10-30), the console and the apps read the switch from the owner-written `settings` node after sign-in (`fugaro project show --json` for the console), not from the pairing delivery.
- **D735**: no ELK wrapping; a minimum node scale with a horizontal-scroll fallback.
- **D736**: no ECharts.
- **D737** defines the gauge semantics.

**Waiting on something external:**

- O7, O9, O10 and O11 were ruled on 2026-10-30. O7: the missing step is inserted (L3.1). O9: the `settings` node (T1.1–T1.3, J1.3). O10: Hotfix takes Pro's levels (L2.2). O11: the refusal is accepted, and this repository's `fugaro.yaml` changes (L5.2).
- O4, the processor's name, gates T2.1 and T2.2.
- The A/B plan's A3.3 and A5.x gate B1.2 and B1.3.
- The mobile app's sign-in to the installation's database (the mobile plan) gates T3.1.
- The in-flight work below gates the tasks that share its files.

## Conventions every part follows

- `CLAUDE.md` and the rules file for each area touched: `.claude/rules/console.md` (exact pins, the spawn and IPC allow-lists, the sanitiser, `hidesView`, goldens only by the owner), `.claude/rules/mobile.md` (the scan tests, no logger, `npm test` in `mobile/` and `shared/`), `.claude/rules/website.md` (`npm run check`), `.claude/rules/ee.md` (B1.3).
- Targeted tests only, with `GOFLAGS=-p=2`. Never run `internal/cli` or `internal/runner` whole. `gofmt -l .` must print nothing.
- Docs are pinned: after any doc change, run `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`, and `npm run check` in `website/` when website content changes.
- Strict decoders: every new task, record, `fugaro.yaml`, project-layer or recipe key gets its JSON Schema in `schemas/`. A new task field or recipe construct gets its own `…Since` constant judged in `internal/cli/recipes_skew.go` (in-job review R5; `recipeSinceFor` for a recipe construct; never `newKeys`, which is the 0.7.0 gate). The constant names the release whose runner acts on it (L1.2's version rule).
- No secret-shaped literal anywhere, the crash-report DSN included: it is injected at build time and never committed. No real names. No live cloud action by an agent: checks are OWNER-RUN.
- Plugin skills stay inside `plugin/skills_lint_test.go`'s budgets (400 lines and 24576 bytes per `SKILL.md`), and every command or flag a skill names is checked against the CLI by `plugin/` tests.
- A user-visible change gets release notes per `docs/releases/README.md`. A step an operator must run after deploy goes in the release's `### For operators` section in the same PR.
- Every new dependency is pinned exactly and recorded in `docs/licensing/dependency-audit.md` and the component's `THIRD_PARTY_NOTICES.md`. D733 approves only `@sentry/electron`, and only for the console.

## Global constraints and review focus

| # | Constraint | Pinned by |
| --- | --- | --- |
| G1 | No layer below the owner's floor can turn a review level off (D722). A task's `none` under the floor is refused, never silently ignored. The floor lives in the owner-only policy node, not in the launcher-embeddable project layer | L1.4, L3.1, L4.2 |
| G2 | A run with L1 and L2 on the same exact model ID never launches (D721), and the refusal names both settings | L3.1, L3.2 |
| G3 | The worst-case stage bound covers L3: `StageBound` errs high and never low | L2.1, L4.4 |
| G4 | Readiness is decided by the highest level that ran, and only when every enabled outer level's last verdict is `ship` (D723, D727). A verdict that cannot be parsed blocks as it does today | L4.2 |
| G5 | Effort never reaches a provider route (D725). A provider stage's environment keeps `MAX_THINKING_TOKENS=0` | L3.1, L4.3 |
| G6 | A crash report holds the error type and frames only: no message, no absolute path, no user name, no breadcrumbs, no local variables (D734). A scrubber test feeds a hostile message carrying a token-shaped placeholder, a home path and a repository name, and asserts none of them leaves | T2.1, T3.1 |
| G7 | Nothing is sent before the switch is read, and nothing is sent with the switch off (D734) | T2.1, T3.1 |
| G8 | The CLI and the job send no crash report: `go.mod` carries no crash-reporting module (D732) | T0.1 |
| G9 | `/jobmax` launches unattended only with the owner's opt-in on (default off). It never launches a task whose dependency is unmerged (an unknown merge state counts as unmerged), never launches past a cap, and stops and asks on any ambiguity (D730 as ruled) | J1.2, J1.3, Check 273 |
| G10 | The A/B `slot` field is fixed before the pair launch writes pairs (D728), so pairs are not written twice in two formats | B1.1 before A3.3 |

**Trust boundaries to probe in review:**

- The task field and the project layer are written by the launcher (`docs/design/bucket-iam.md` §10 L1, L2), so a hostile task could set a level to `none`, name a model, or embed a layer. The floor is read from the owner-only policy node, and the runner re-runs `ResolveLevels` at bootstrap.
- The `effort` value is passed into the agent's environment, so it is accepted only from a closed set.
- A crash payload leaves the machine: G6, and the DSN cannot be redirected by configuration.
- The `settings` node is new database surface: written by the owner only, read by signed-in users of the installation. Its rules entry is security-reviewed with emulator tests (T1.1).
- `/jobmax` reads plan files from the repository, which are untrusted text: the graph is parsed by `fugaro plan graph`, never by the skill, and a task's text goes to `fugaro run` on stdin.

## In-flight work this plan waits for (file conflicts)

Open PRs on 2026-10-30 against `main` at `8c9819ea7`. #1362, #1367, #1369 and #1360, named in the first
version of this table, have merged, and so has the console redesign's step 4b-2.

| In flight | Files shared with this plan | Tasks that wait |
| --- | --- | --- |
| #1332 (mobile P7.1b, draft) | `mobile/app.config.js`, `docs/plans/2026-10-19-mobile-telemetry.md`, `docs/mobile/implementation-plan.md` | T0.1, T3.1 |
| #1392 (console auto-update U3, halted) and U4, U5 | `console/package.json`, `console/package-lock.json`, `console/src/main/index.ts`, `console/test/unit/pinning.test.ts`, `console/THIRD_PARTY_NOTICES.md` | T2.1 |
| #620 (managed-projects checks) | `docs/gcp-live-checklist.md` | X1, X2 |
| The console redesign's steps 5 to 7 (console 0.116.0) | `console/src/renderer/theme/flight-deck.css`, `screens.css`, `console/src/renderer/screens/live.ts`, `run.ts`, `docs/design/console-styling.md` | G1.1, V1.2, V1.3 |
| Any other run writing `internal/runner/runner.go` (one writer at a time across every plan) | `internal/runner/runner.go` | L4.2, B1.2 |

## Slices and tasks

Six slices. **L** (review levels) and **J** (job max) are useful alone. **B** (A/B slots) needs L and the
A/B plan. **T** (crash reports) ships per client. **G** (graph) and **V** (gauges) are console-only.
Every task's reviewer tier is **standard** unless marked otherwise.

### Slice L: review levels 1–3

#### L0.1 Answer U1: how Claude Code takes an effort setting

- **Files:** `docs/multi-model.md` (its section on what was measured against Claude Code, beside `MAX_THINKING_TOKENS`).
- **Interfaces:** produces the closed set of effort values and the per-process mechanism (a flag, an environment variable or a settings key) for the pinned Claude Code version. It is measured with the fake `claude` and the pinned binary's own `--help`, offline. If there is no per-process mechanism, the task says so and L4.3 is dropped (D725 then has nothing to apply). **Done** (#1422, measured locally because this repository's cloud loop pins no Claude model): the mechanism exists, so L4.3 stays.
- **Tests and command:** `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Depends on:** none.
- **Red step:** none. It is a measurement recorded in a doc, and L1.1's closed set is built from it.
- **Commit:** `docs: how Claude Code takes an effort setting (U1)`.

#### L1.1 The task's `review_levels` field (D724)

- **Files:** `internal/config/levelsetting.go` (new), `internal/config/levelsetting_test.go` (new), `internal/task/task.go`, `schemas/task.schema.json`, `internal/task/task_test.go`.
- **Interfaces:** produces `config.LevelSetting{Model, Effort, Off bool, Focus string}` and `config.ReviewLevels{L1, L2, L3 *LevelSetting}`. They live in `internal/config` because `internal/task` already imports `internal/config`, so a resolver in `config` cannot import `task`. `task.Spec.ReviewLevels` reuses the type. `Off` is the YAML/JSON `none`. `Effort` is decoded against a closed set, `config.Efforts` = `low`, `medium`, `high`, `xhigh`, `max` (L0.1, [multi-model.md](../multi-model.md) "How the pinned Claude Code takes an effort setting"), so an unknown value is refused at decode time. The refusal has to be Fugaro's: Claude Code never refuses a bad value (its flag warns and sends the default, its variable and settings key drop it silently). `Focus` is at most 500 bytes, is never put through a shell, and reaches the reviewer only as quoted data (L4.2). Consumed by L3.1 and L4.2. Inert until L3.1.
- **Schema change:** `task.schema.json` gains `review_levels` (closed objects, `additionalProperties: false`).
- **Tests:** `TestReviewLevelsRoundTrip`, `TestReviewLevelsRefusesUnknownKey`, `TestReviewLevelsFocusBound`, `TestLevelSettingRefusesUnknownEffort`, and `schemas/schemas_test.go`'s existing schema-vs-struct check.
- **Command:** `GOFLAGS=-p=2 go test ./internal/config ./internal/task ./schemas -run 'ReviewLevels|LevelSetting|Schema' -count=1`.
- **Depends on:** L0.1 (the closed effort set).
- **Red step:** `TestReviewLevelsRoundTrip` fails while `task.Parse` refuses the unknown field `review_levels`.
- **Commit:** `task: the review_levels field (review levels L1.1)`.

#### L1.2 The version gate and the `--review-levels` flag

- **Files:** `internal/cli/recipes_skew.go`, `internal/cli/run.go`, `internal/cli/recipes_skew_test.go`, `internal/cli/run_review_test.go`, `website/content/cli-reference.md` (regenerated with `go run ./ee/clidoc`).
- **Interfaces:** consumes L1.1. Produces `reviewLevelsSince` and `fugaro run --review-levels <file|->` (YAML, decoded strictly into `task.ReviewLevels`). A job image older than `reviewLevelsSince` refuses the launch, naming `fugaro image refresh`. The gate looks at the **resolved** run, not only at the task field, because a Critical default L3, a level added under O7, or an owner floor reaches a run with no task field at all. The launcher refuses an older image whenever the resolved levels include L3, any `none`, a per-level model or effort, or a floor. L3.2 supplies the resolved levels.
- **Version rule:** `reviewLevelsSince` names the release that ships L4.2, which is the first runner that acts on levels. Until L4.2 is released that version does not exist, so no image accepts such a launch and then ignores `none` or L3. `adversarialSince` (L2.1) follows the same rule.
- **Tests:** `TestRecipesSkewRefusesReviewLevelsOnOldImage`, `TestRecipesSkewRefusesResolvedL3OnOldImage`, `TestRunReviewLevelsFlagWritesTaskField`, `TestCliReferenceNamesEveryCommandAndFlag`, and `ee/clidoc`'s `TestCliReferenceIsCurrent`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'RecipesSkew|RunReviewLevels|CliReference' -count=1 && GOFLAGS=-p=2 go test -tags ee ./ee/clidoc -count=1 && (cd website && npm run check)`.
- **Depends on:** L1.1, L2.1 (the same `recipes_skew.go`), #1362, #1367 (`cli-reference.md`).
- **Red step:** the skew test fails because no constant judges the field.
- **Release notes:** the CLI release's `### For operators` says to refresh job images (`fugaro image refresh`) before any launcher sets `--review-levels` or a repository sets levels.
- **Commit:** `run: --review-levels, gated by the job image's version`.

#### L1.3 Per-level settings in `fugaro.yaml` and the project layer (D722, D726)

- **Files:** `internal/config/config.go`, `internal/config/layer.go`, `internal/config/validate.go`, `internal/config/defaults.go`, `schemas/fugaro.schema.json`, `schemas/project-layer.schema.json`, `internal/config/config_test.go`, `internal/config/layer_test.go`.
- **Interfaces:** produces `agent.levels.{l1,l2,l3}.{model, effort, none, focus}` in `fugaro.yaml` and in the project layer's `defaults.agent.levels`. Also produces `agent.adversarial_rounds` (1–10, default 2, `config.DefaultAdversarialRounds`). **Superseded by `DefaultRounds`/`AdversarialDefault` (#1483): the default for a level-3 round count is the mode's own senior row (`rigor.AdversarialDefault`, 1 at poc, 2 at startup, 3 at pro, 5 at critical), and no `DefaultAdversarialRounds` constant is declared.** `first_line_rounds` stays default 1 and max 3; `review_rounds` stays default 2. How the keys relate: `agent.levels.l2.model`, when set, wins over `agent.models.reviewer`, and `agent.levels.l1.model` defaults to the coder's model. A recipe's `roles` mapping is applied first. `agent.levels.l1.none`, or an explicit `l1` entry, wins over `agent.first_line_review`, and with neither set, `first_line_review` decides as today. `focus` is bounded to 500 bytes at every layer, not only in the task.
- **Tests:** `TestLevelsDecodeStrict`, `TestAdversarialRoundsDefaultAndBounds`, `TestLayerLevelsOverlay`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/config ./schemas -run 'Levels|AdversarialRounds|Schema' -count=1`.
- **Depends on:** L1.1. Each level's settings reuse `config.LevelSetting` and its closed `config.Efforts`, so this task cannot define them a second time.
- **Red step:** `TestLevelsDecodeStrict` fails because `levels` is an unknown field.
- **Commit:** `config: per-level review settings and agent.adversarial_rounds`.

#### L1.4 The owner's level floor, owner-only and tighten-only (D722)

- **Files:** `internal/localcfg/localcfg.go`, `internal/localcfg/shared.go` and `internal/cli/sharedcfg.go` (the floor never enters the launcher-writable shared configuration: `Shared()` drops it and `sharedForbidden` names it), `internal/localcfg/localcfg_test.go`, `internal/projectpolicy/policy.go`, `internal/projectpolicy/policy_test.go`, `internal/cli/project_cmd.go` (the publish builds `projectpolicy.Policy` field by field), `internal/runner/policy.go` (the job reads the node at bootstrap), `internal/policy/policy.go` (the field on `Effective`), `internal/policy/merge.go`, `internal/policy/merge_test.go`.
- **Interfaces:** produces `review_floor: {<mode>: [l1|l2|l3, …]}` in the owner's project config, published by `fugaro project policy publish` into the **owner-only policy node** (`internal/projectpolicy`). It is **not** a project-layer key: `docs/design/bucket-iam.md` §10 L2 says a launcher can embed any project-layer text in its own `task.json` and the runner trusts it, so a floor there could be dropped by the launcher it is meant to bind. The runner reads the node at bootstrap with its run token. `policy.Effective.ReviewFloor(mode)` merges as a union, so only tightening wins. A launcher with the owner's file applies it early (L3.2), and the job is authoritative. `projectpolicy.Decode` ignores unknown top-level keys, so a runner older than L4.2 would drop the floor silently. L1.2's gate on resolved levels refuses such an image whenever a floor is set. **No policy node:** `fugaro project policy publish` needs the installation's database and a `budget.mode`. An installation without them can publish no floor, so it runs on the rigor defaults alone, and `fugaro validate` warns when the owner's file sets a floor that cannot be published.
- **Tests:** `TestReviewFloorMergesTightenOnly`, `TestReviewFloorPublishedToPolicyNode`, `TestReviewFloorNotInLayer`, `TestReviewFloorNotInShared`, `TestRunnerReadsReviewFloorFromNode`. `TestReviewFloorUnpublishableWarns` is L3.2's, because the warning is printed by `internal/cli/validate.go`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/policy ./internal/localcfg ./internal/projectpolicy -run 'ReviewFloor' -count=1 && GOFLAGS=-p=2 go test ./internal/runner -run 'TestRunnerReadsReviewFloor' -count=1 -timeout 30m`.
- **Depends on:** none.
- **Red step:** the merge test fails because the key does not exist.
- **Reviewer:** opus + mandatory independent review (a policy merge that could loosen, and an owner-only value).
- **Commit:** `policy: the owner's review-level floor, owner-only and tighten-only`.

#### L2.1 The level-3 recipe step (D720, D726)

- **Files:** `internal/recipe/recipe.go`, `internal/recipe/stages.go`, `schemas/recipe.schema.json`, `internal/cli/recipes_skew.go`, `internal/cli/recipes_skew_test.go`, `docs/recipes.md`, `docs/design/recipes.md`, `internal/recipe/recipe_test.go`, `internal/recipe/stages_test.go`.
- **Interfaces:** produces `recipe.StepAdversarial` (`adversarial: { max_rounds, bounce: first_line }`): at most once, after `review`, and `bounce` may target only `first_line`. `StageBound` adds `n₃·(2 + 2·f)` with a bounce, keeping its signature: `n₃` is the step's own `max_rounds`, whose default (`recipe.DefaultAdversarialRounds`, 2) is declared in `internal/recipe` because `internal/config` imports `recipe`, never the reverse. **Superseded by `DefaultRounds`/`AdversarialDefault` (#1483): `internal/recipe` declares no such constant, the default for `n₃` is the rigor mode's senior row, and `StageBound` takes it as its fourth argument — `internal/rigor`'s own test imports `internal/recipe`, so the import cannot go the other way, and a caller with no mode passes critical's five.** It also produces `recipe.UsesAdversarial` and `adversarialSince`, a case in `recipeSinceFor` the way `UsesDigest` has one. `recipe.Recipe.newKeys` (`internal/recipe/recipe.go`) is the 0.7.0 gate and would let a 0.13 image accept the launch and then refuse the step. `recipeSinceFor` returns the base gate when the task carries no catalog recipe (`r == nil`, a repository-sourced recipe), so the launcher reads the repository's recipe at the ref and applies `adversarialSince` to it too.
- **Tests:** `TestAdversarialStepOrder`, `TestAdversarialBounceTarget`, `TestStageBoundWithAdversarial` (which pins 11 for today's `standard` at the defaults and 19 with L3 at f = 1, n = 2, n₃ = 2), `TestRecipesSkewRefusesAdversarialOnOldImage`, and `TestRecipesSkewRefusesRepositoryAdversarialOnOldImage`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/recipe -count=1`, then `GOFLAGS=-p=2 go test ./internal/cli -run 'DocsRecipes|RecipesSkew' -count=1`.
- **Depends on:** none.
- **Red step:** a recipe with `adversarial:` is refused as an unknown step.
- **Commit:** `recipe: the adversarial step (review level 3)`.

#### L2.2 Per-rigor level defaults (D722, R2; folds rigor LV6.1)

- **Files:** `internal/rigor/levels.go` (new), `internal/rigor/rounds.go`, `internal/rigor/levels_test.go` (new).
- **Interfaces:** produces `rigor.DefaultLevels(mode)` for **L2 and L3 only**: POC {L2: none}; Startup and Pro {L2}; Critical {L2, L3}. L1 keeps following `agent.first_line_review` (`auto` turns it on only when a provider serves the coder and none serves the reviewer, `config.Agent.FirstLineOn`), except at POC, where L2 is `none` and L1 is turned on so a review still runs (D723). This is what keeps `solo`, `lint-fix`, and a coder = reviewer configuration on the catalog `default` recipe, from resolving to L1 = L2 and being refused under D721. A recipe that names `first_line` runs it regardless, so there the refusal stands (O11). Hotfix is not in the spec; the owner ruled on 2026-10-30 (O10) that it takes Pro's levels: L2 on, with L1 as Pro has it. Also produces `rigor.AdversarialCeiling(mode)`, held to `minRoundCeiling` while rigor O3 is open — **superseded by `DefaultRounds`/`AdversarialDefault` (#1483): the table is `roundDefaults`, the numbers are defaults rather than ceilings, and no clamp remains.**
- **Tests:** `TestDefaultLevelsPerMode`, `TestDefaultLevelsLeaveL1ToFirstLineReview`, `TestAdversarialCeilingNeverRaises` — **this last one is superseded by `DefaultRounds`/`AdversarialDefault` (#1483); it is `TestAdversarialDefaultIsTheModesSeniorRow` now.**
- **Command:** `GOFLAGS=-p=2 go test ./internal/rigor -count=1`.
- **Depends on:** none.
- **Red step:** the package does not compile without `DefaultLevels`, and the test fails.
- **Commit:** `rigor: the levels each mode runs by default`.

#### L3.1 The level resolver and the guardrail (D721, D722, D723, D725)

- **Files:** `internal/config/levels.go` (new), `internal/config/levels_test.go` (new).
- **Interfaces:** consumes L1.1, L1.3, L1.4 and L2.2. Produces `config.ResolveLevels(in LevelInputs) (Levels, []Warning, error)`. `LevelInputs` carries the merged config, the mode, the floor, the task's `*config.ReviewLevels`, the recipe, the providers map (which models a provider claims) and the effective `allowed_models`. Precedence is rigor defaults < project < repository < task, and the floor is applied last. **O7 as ruled (2026-10-30): the rigor mode wins.** An enabled level whose step the recipe lacks is inserted in its fixed place: L1 before `review`, L3 after `review` with `bounce: first_line`, its knobs from `agent.*`. `none` removes a step the recipe has, unless the owner's floor requires it. The same function is the runner's bootstrap check (L4.2), so a task written straight to the bucket meets the same refusals.
  - Refuses L1 = L2 by exact model ID.
  - Warns when L2 equals the coder's model, or when L1 and L2 share a vendor family. It creates `config.ModelFamily`: a Claude ID is `anthropic`, a provider ID is its vendor prefix. A/B D553 reuses it.
  - Refuses `effort` for a model a provider claims, with the reason (D725).
  - Warns when `effort` is set for a Claude model that ignores it (`claude-haiku-4-5` sends no effort by any mechanism, L0.1).
  - Refuses a level model outside `allowed_models`.
- **Tests:** `TestResolveLevelsPrecedence`, `TestResolveLevelsFloorRefusesNone`, `TestResolveLevelsRefusesSameModel`, `TestResolveLevelsInsertsMissingStep`, `TestResolveLevelsWarnsCoderAndFamily`, `TestResolveLevelsRefusesProviderEffort`, `TestResolveLevelsRefusesModelOutsideAllowList`, `TestResolveLevelsSoloUnaffected` (`solo`, `lint-fix`, and a coder = reviewer configuration on the catalog `default` recipe, resolve with no refusal at every mode), and `TestResolveLevelsRefusesFirstLineRecipeWithCoderEqualsReviewer`. A recipe carrying `first_line: {}` (`standard`, `test-and-fix`) always runs L1 (`docs/recipes.md` §2), so a coder = reviewer configuration on it **is** refused under D721. That includes this repository's own `fugaro.yaml` launched with `--recipe standard`. See O11.
- **Command:** `GOFLAGS=-p=2 go test ./internal/config -run 'ResolveLevels' -count=1`.
- **Depends on:** L1.1, L1.3, L1.4, L2.2.
- **Red step:** `TestResolveLevelsRefusesSameModel` fails before the refusal exists.
- **Reviewer:** opus + mandatory independent review (the floor and the guardrail are policy).
- **Commit:** `config: resolve review levels, the owner floor and the same-model refusal`.

#### L3.2 `fugaro validate` and launch apply the resolver

- **Files:** `internal/cli/validate.go`, `internal/cli/run.go`, `internal/cli/validate_test.go`, `internal/cli/run_review_test.go`.
- **Interfaces:** consumes L3.1 and L1.2. `validate` prints the warnings and refusals, and `--json` carries them. Launch refuses before writing the task, and hands the resolved levels to L1.2's version gate.
- **Tests:** `TestValidateLevelsSameModel`, `TestRunRefusesLevelsBelowFloor`, `TestRunGatesResolvedLevelsOnImage`, and L1.4's `TestReviewFloorUnpublishableWarns`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'ValidateLevels|RunRefusesLevels|RunGatesResolvedLevels' -count=1`.
- **Depends on:** L3.1, L1.2, L5.2 (this repository's own `fugaro.yaml` must not be refused by the check this task ships).
- **Red step:** validate passes a same-model configuration before the change.
- **Release notes:** the CLI release says that a coder = reviewer configuration on a recipe with `first_line` is now refused (O11).
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

- **Files:** `internal/agent/env.go`, `internal/agent/effort_test.go` (new), and `internal/agent/settings.go` only if its managed settings could carry `maxEffortLevel`.
- **Interfaces:** produces `agent.EffortVars(effort string, provider bool) (map[string]string, error)`. It is empty for a provider stage and for "", and it returns an error for a value outside `config.Efforts`. It sets `CLAUDE_CODE_EFFORT_LEVEL` (L0.1: the variable beats the `--effort` flag and the `effortLevel` settings key, and a `maxEffortLevel` in any settings file, the managed one included, clamps all three). So the agent's managed settings file must carry no `maxEffortLevel` below the value set, and the stage's record keeps the value Fugaro set, because Claude Code's stream-json output does not show the level. `claude-haiku-4-5` sends no effort by any mechanism: setting one there is a no-op, and L3.1 warns about it.
- **Tests:** `TestEffortVarsClaudeOnly`, `TestEffortVarsNeverOnProviderStage`, `TestEffortVarsRefusesUnknown`, `TestEffortVarsSetsTheVariable` (the name is `CLAUDE_CODE_EFFORT_LEVEL`), and `TestManagedSettingsCarryNoEffortCap`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/agent -run 'EffortVars' -count=1`.
- **Depends on:** L1.1 (`config.Efforts`, fixed by L0.1, so this task changes no schema).
- **Red step:** an undefined function fails the test.
- **Commit:** `agent: effort for a Claude stage, never a provider's`.
- **Done (PR "agent: effort for a Claude stage, never a provider's (L4.3)")**: nothing is wired into a stage here, L4.2 does that.

#### L4.2 The runner runs level 3, skips L2 after an L3 bounce, and decides by the highest level (D723, D727)

- **Files:** `internal/runner/runner.go`, `internal/runner/outcome.go`, `internal/runner/prflow.go`, `internal/runner/levels_test.go` (new).
- **Interfaces:** consumes L1.1, L2.1, L3.1, L4.1 and L4.3.
  - After `review`, the runner runs `adversarial`. An L3 rejection runs the fix, then the L1 loop, then L3 again, never L2.
  - `Decide` reads the highest level that ran and requires every enabled outer level's last verdict to be `ship`.
  - The PR body lists each level's verdict and head.
  - A level marked `none` does not run.
  - The bootstrap runs `config.ResolveLevels` again (the floor, `allowed_models`, the same-model refusal, the provider-effort refusal and the closed effort set), so a hand-written or replayed task meets the launcher's checks.
  - A level's focus text enters the review prompt as quoted data under a fixed heading. The PR body records it through `safetext`, the sanitiser every job-written field takes. Quoting does not stop a hostile focus text from steering a model. What bounds that risk is that only a launcher writes it, and a floor-required level still runs.
- **Tests:** `TestLevelsL3BounceSkipsL2`, `TestLevelsReadinessHighestLevel`, `TestLevelsPOCL1Decides`, `TestLevelsBootstrapRefusesBelowFloor`, `TestLevelsBootstrapRefusesSameModelTask`, `TestLevelsFocusIsQuotedAndRecorded`, `TestLevelsPRBodyShowsHeads`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/runner -run 'TestLevels' -count=1 -timeout 30m`.
- **Depends on:** L1.1, L2.1, L3.1, L4.1, L4.3, L4.5, #1369.
- **Red step:** `TestLevelsL3BounceSkipsL2` fails because the runner rejects the step.
- **Reviewer:** opus + mandatory independent review (readiness).
- **Commit:** `runner: review level 3, its bounce, and readiness by the highest level`.

#### L4.5 A model per review level in routing

- **Files:** `internal/runner/routing.go`, `internal/runner/agentenv.go` (`stagePins` applies the provider route's variables), `internal/runner/gateway.go` (the run's allowed set), `internal/runner/routing_levels_test.go` (new).
- **Interfaces:** consumes L3.1. Today a stage pins one of two role slots plus the background model (`pinSlots`). This task routes an L1 or L3 model that differs from the coder's and the reviewer's: the stage's pins, its provider route (`agent.ProviderRouteVars` when a provider claims the model), and the gateway's allowed set for the run, which must name every level's model.
- **Tests:** `TestRoutingL1ModelDistinctFromCoder`, `TestRoutingL3ProviderModelGetsRouteVars`, `TestRoutingRefusesLevelModelOutsideAllowedModels`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/runner -run 'TestRouting' -count=1 -timeout 30m`.
- **First commit:** records U5: whether in-job review Q9's gate (a Claude reviewer in a job waits for the credentials service) still binds under `auth: api-key` with both keys, read from `internal/runner/routing.go` and `docs/multi-model.md`. If it binds, a Claude model at L2 or L3 is refused next to a provider coder until the credentials service lands, and `ResolveLevels` (L3.1) says so.
- **Depends on:** L3.1.
- **Red step:** an L1 model different from the coder's runs on the coder's pin.
- **Reviewer:** opus + mandatory independent review (the gateway's allowed models).
- **Commit:** `runner: route each review level's own model`.

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
- **Depends on:** L4.2, L3.2, J1.1 (which also writes `working`), T1.1 (its operator line).
- **Red step:** a new pin in `internal/cli/docs_recipes_test.go` (the levels section of `docs/recipes.md`, L1 to L3 and their keys) fails until the section is written.
- **Commit:** `docs: review levels 1-3`.

#### L5.2 This repository's `fugaro.yaml` gets a different level-2 model (O11)

- **Files:** `fugaro.yaml`, `docs/dogfooding.md` (its statement of this repository's setup).
- **Interfaces:** `agent.models.reviewer` names a model other than the coder's `deepseek/deepseek-v4.1-flash` (the owner's evening ruling of 2026-10-30 named `deepseek/deepseek-v4-pro`, served through the coder's own OpenRouter route, so the senior review runs in the job and no credential this repository does not mount is needed), so a `standard` launch here no longer resolves to L1 = L2 (D721). The comment above `agent:` and `docs/dogfooding.md`'s statement of this setup change with it.
- **OWNER-RUN part:** none for the model this ruling names — it needs no credential the repository's jobs do not already mount. (Had the model been a Claude one, the owner would have stored `anthropic-api-key` with `fugaro secrets set <name>` in their own terminal and run `fugaro update --repo`; no agent performs either step.)
- **Tests and command:** `fugaro validate --json` in this checkout (no refusal), then `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1` (`docs/dogfooding.md` is pinned).
- **Depends on:** none. It must merge before L3.2.
- **Red step:** none in this task. L3.2's `TestResolveLevelsRefusesFirstLineRecipeWithCoderEqualsReviewer` is the case this file would hit.
- **Commit:** `fugaro.yaml: a level-2 model other than the coder's`.

### Slice B: A/B for the review slots (amends docs/model-ab-testing)

#### B1.1 The `slot` field on the pair (D728, before A3.3)

- **Files:** `internal/task/task.go`, `internal/task/task_test.go`, `schemas/task.schema.json`, `internal/runstore/runstore.go`, `internal/runstore/runstore_test.go`, `schemas/result.schema.json`, `internal/cli/recipes_skew.go` and `internal/cli/recipes_skew_test.go` (`slotSince`), `docs/model-ab-testing/implementation-plan.md` (D555 amended: a coder pair still holds every reviewer fixed; A3.3 now depends on B1.1).
- **Interfaces:** produces `task.AB.Slot` ∈ {`coder`, `l1`, `l2`, `l3`} and `task.AB.ChampionRun`, the champion's run ID. `ChampionRun` is required on a reviewer-slot challenger and absent otherwise; it is what B1.2's "patch from another run" check compares with. Empty `Slot` means `coder`, so records written by A1.1 still read. A launch with a non-empty slot is gated by `slotSince`.
- **Tests:** `TestABSlotVocabulary`, `TestABSlotDefaultsToCoder`, `TestABReviewerSlotNeedsChampionRun`, `TestRecipesSkewRefusesSlotOnOldImage`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/task ./internal/runstore ./schemas -run 'ABSlot|Schema' -count=1 && GOFLAGS=-p=2 go test ./internal/cli -run 'RecipesSkew|Docs' -count=1`.
- **Depends on:** L1.1, L4.1 and L1.2 (the same files, at earlier levels). Must land before A3.3.
- **Red step:** an unknown field `slot` is refused.
- **Commit:** `task, runstore: the A/B pair's slot`.

#### B1.2 The reviewer replay (MIT mechanism)

- **Files:** `internal/runner/abreplay.go` (new), `internal/runner/runner.go`, `internal/runner/abreplay_test.go` (new).
- **Interfaces:** a challenger with `slot: lN` checks out the champion's base, applies the champion's patch (`runs/<slug>/<run-id>/ab/patch.diff`), runs one level-N review with the challenger model and publishes nothing (`ab.publish: none`). Its findings are recorded for the judge.
  - The patch is untrusted: any launcher, and the champion's own job agent, can write that object (`docs/design/bucket-iam.md` §10 L3).
  - It reads only the patch under `ChampionRun`'s own prefix.
  - The replay refuses a patch that touches `.claude/**`, `.fugaro/**`, `.github/**`, `.git*`, `fugaro.yaml`, a recipe file, `CLAUDE.md` or `CLAUDE.local.md` at any depth, or `.mcp.json`, so no hook, instruction or tool setting crosses runs.
  - It applies the patch with `git apply` and no scripts, and runs the review with the reviewer's read-only tool set.
- **Tests:** `TestABReplayReviewsChampionPatch`, `TestABReplayPublishesNothing`, `TestABReplayRefusesPatchFromOtherRun`, `TestABReplayRefusesPatchTouchingAgentConfig`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/runner -run 'ABReplay' -count=1 -timeout 30m`.
- **Depends on:** B1.1, L4.2, A5.2. It is launched by B1.4.
- **Red step:** a `slot: l2` challenger runs a full coding loop.
- **Reviewer:** opus + mandatory independent review (it reads another run's object).
- **Commit:** `runner: an A/B reviewer arm replays the champion's patch`.

#### B1.4 The deferred reviewer-arm launch (`ee/`)

- **Files:** `ee/ab/` (the launch files A3.3 creates), and their tests.
- **Interfaces:** A3.3 launches a coder pair's two arms at one moment (D571). A reviewer-slot challenger needs the champion's patch, which exists only once the champion has finished, so it is launched afterwards, by the same `ee/ab` code, at the champion's recorded start commit. It sets `ab.slot` and `ab.champion_run`. D571's one-commit rule holds, and its same-moment launch does not apply to reviewer slots.
- **Tests:** `TestABReviewerArmLaunchesAfterChampionPatch`, `TestABReviewerArmUsesChampionStartSHA`.
- **Command:** `GOFLAGS=-p=2 go test -tags ee ./ee/ab/... -count=1 && GOFLAGS=-p=2 go test ./internal/eeboundary/ -count=1`.
- **Depends on:** B1.1, A3.3.
- **Red step:** a reviewer-slot pair launches its challenger before the champion's patch exists.
- **Commit:** `ab: launch a reviewer arm once the champion's patch exists`.

#### B1.3 Per-slot scoring and `fugaro ab` grouping (`ee/`)

- **Files:** `ee/ab/` (the scoring and report files A4.1–A4.3 create).
- **Interfaces:** a reviewer pair's signals are misses (findings the judge matched in the champion's same-level review that the challenger lacked), false alarms (judge-marked) and cost. `fugaro ab` groups results by slot × model × effort. There is no per-person dimension (D560).
- **Tests:** `TestABScoreReviewerSlot`, `TestABReportGroupsBySlot`.
- **Command:** `GOFLAGS=-p=2 go test -tags ee ./ee/ab/... -count=1 && GOFLAGS=-p=2 go test ./internal/eeboundary/ -count=1`. Every new `ee/` file carries the business-licence notice that `ee/README.md` prescribes.
- **First commit:** records U4 from the A5 judge's own check (Check 211): how often the judge's finding matches agree across its two swapped passes. Below the owner's bar, the miss rate is reported as unreliable rather than as a figure.
- **Depends on:** B1.2, B1.4, A4.3, A5.4.
- **Red step:** a reviewer pair is scored with coder signals.
- **Commit:** `ab: score and report the review slots`.

### Slice J: job max

#### J1.1 Job max in the `parallelism` skill (D729)

- **Files:** `plugin/skills/parallelism/SKILL.md`, `plugin/skills/working/SKILL.md` (a one-line pointer), `plugin/parallelism_skill_test.go`.
- **Content:** job max is "the most completed, reviewed runs in parallel, not tokens spent; only real dependencies order runs". Planning advice: contracts first, stubs and fakes so neither side waits, the graph kept wide and shallow. The skill's "one yes per batch" rule stays the default. The `/jobmax` exception applies only where the owner has turned on the project's opt-in (D730 as ruled; J1.3).
- **Tests:** `TestParallelismSkillDefinesJobMax` (the phrase and the three techniques), plus the lint.
- **Command:** `GOFLAGS=-p=2 go test ./plugin -count=1`.
- **Depends on:** #1367 (`working/SKILL.md`).
- **Red step:** the new test fails on the current skill.
- **Commit:** `plugin: job max in the parallelism skill`.

#### J1.2 The `/jobmax` skill (D729, D730 as ruled)

- **Files:** `plugin/skills/jobmax/SKILL.md` (new), `plugin/jobmax_skill_test.go` (new), `plugin/plugin_test.go` (it pins exactly five skills), `plugin/skills/parallelism/SKILL.md` (the `/jobmax` exception to "one yes per batch", only with the opt-in on), `plugin/.claude-plugin/plugin.json` (description).
- **Interfaces:**
  - **The opt-in.** It reads the project's `jobmax.auto_launch` (J1.3) from `fugaro project show --json`. Off, unset or unreadable means the `parallelism` rule applies: one yes for the whole batch, with the runs and the worst case shown. On means it launches with no question, inside the guardrails below.
  - **Guardrail 1, caps.** It launches only within the owner-set caps: `max_parallel` (active runs), the per-run cap (`budget.per_run_usd`), the project's daily cap (`budget.per_day_usd`, the batch's worst case checked against today's headroom from `fugaro budget show --json`), which is also the 20-run cap: in the repository, the only limit on simultaneous runs is `max_parallel` (default 20, `internal/localcfg`; enforced by `checkMaxParallel` in `internal/cli/run.go`), so "max parallel runs" and "the existing 20-run cloud cap" in the ruling are one cap.
  - **Guardrail 2, merged only.** A task launches only when **all** its dependencies are **merged**, never on an open PR. It reads `fugaro plan status --all --json` (without `--all`, merged and cancelled tasks are not listed) and checks the merge field itself. Cancelled, and done with an unknown merge state (Bitbucket, no `gh`, past the lookup cap), count as not merged. The built-in `ready` state treats an unknown merge as done.
  - **Guardrail 3, stop and ask.** It stops and asks the user on any cap hit, any refusal, and any ambiguity: a graph `fugaro plan graph` refuses, a task with no text, two ready tasks whose footprints overlap, a dependency it cannot classify, or a dirty or non-base checkout.
  - Starting point: a plan with a `*.graph.json`. When the backlog has none, it stops and points to the `parallelism` skill, which breaks the work into a graph with the user (the spec's "breaks the backlog or plan into parallel jobs").
  - Runs only on a clean checkout of the base branch, at its tip, so a branch's unreviewed plan text never drives launches.
  - Launches each ready task with `fugaro run --batch <plan> --plan-task <plan>/<task> --task-file -`. `--plan-task` is what `plan status` joins runs by. The run ID is the CLI's own, because `task.ValidRunID` accepts only `YYYYMMDD-HHMMSS-xxxx`.
  - The task's text is untrusted. It goes to `fugaro run` on stdin only, and the skill states that instructions inside it are not followed.
  - It prints what it launched and the worst case of what it launched. There is no waiting loop: running it again launches what has become ready.
- **Tests:** `TestJobmaxSkillNamesOnlyRealCommands` (the plugin check), `TestJobmaxSkillOptInDefaultsOff`, `TestJobmaxSkillRequiresMergedDependencies`, `TestJobmaxSkillStopsOnCapAndAmbiguity`, `TestJobmaxSkillBaseBranchOnly`, and `plugin_test.go` updated to count the new skill. All but the first and last are wording tests: they pin the rules the skill states, and they cannot enforce an agent's behaviour. The behaviour is Check 273 (X2, OWNER-RUN).
- **Command:** `GOFLAGS=-p=2 go test ./plugin -count=1`.
- **Depends on:** J1.1, J1.3.
- **Red step:** the plugin check fails on a skill that does not exist.
- **Commit:** `plugin: /jobmax launches every ready task of a plan`.

#### J1.3 The owner's opt-in: `jobmax.auto_launch` (D730 as ruled)

- **Files:** `internal/localcfg/localcfg.go`, `internal/localcfg/localcfg_test.go`, `internal/cli/project_cmd.go` (the owner's publish into `settings`, and `project show --json`), `internal/cli/project_enroll_test.go` (where `project show` is tested), `console/docs/json-contract.md`.
- **Interfaces:** `jobmax: { auto_launch: false }` in the owner's project config, decoded strictly: a boolean, default `false`, and any other key under `jobmax:` refused. It is published only by the owner, into the same owner-written `settings` node as the crash switch (O9 as ruled), never through the launcher-writable shared configuration. The owner can turn it off again at any time. `fugaro project show --json` prints `settings.jobmax_auto_launch: true|false|null`, and `null` (unreadable) means off.
- **Tests:** `TestJobmaxAutoLaunchDefaultsOff`, `TestJobmaxAutoLaunchStrict`, `TestJobmaxAutoLaunchNotInShared`, `TestProjectShowJobmaxAutoLaunchUnknownIsNull`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/localcfg -run Jobmax -count=1 && GOFLAGS=-p=2 go test ./internal/cli -run 'Jobmax|ProjectShow|Docs' -count=1`.
- **Depends on:** T1.1 and T1.2 (the same `localcfg.go`, `project_cmd.go` and contract).
- **Red step:** the unknown key `jobmax` is refused.
- **Reviewer:** opus + mandatory independent review (an owner-only permission to spend).
- **Commit:** `localcfg: jobmax.auto_launch, the owner's opt-in to unattended launches`.

### Slice T: crash reports (console and mobile; none from the CLI)

#### T0.1 Record the supersessions (D731)

- **Files:** `docs/design/desktop-console.md` (D5 marked superseded by D731), `docs/plans/2026-10-19-mobile-telemetry.md` (D255 superseded; Analytics dropped; the §7.4 scan refuses `setAnalyticsCollectionEnabled(` and admits exactly one `setCrashlyticsCollectionEnabled(` call site), `docs/mobile/implementation-plan.md` (P7.2/P7.3 re-scoped), `internal/cli/docs_mobile_telemetry_test.go` (its pinned facts move with the text), `internal/cli/docs_no_crash_reporter_test.go` (new: G8, `TestGoModHasNoCrashReporter`, which needs no switch and so is not blocked on O9).
- **Tests and command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|GoModHasNoCrashReporter' -count=1 && (cd website && npm run check)`. `website/test/console.mjs` reads the D5 row.
- **Depends on:** #1332 (the same two mobile documents).
- **Red step:** the mobile telemetry docs test fails on the old "always on" pin until both move together.
- **Commit:** `docs: crash reports are opt-out; the app drops Analytics (D731)`.

#### T1.1 The `settings` node and the switch `telemetry.crash_reports` (D732, D734, O9 as ruled)

- **Files:** `internal/localcfg/localcfg.go`, `internal/localcfg/shared.go` (`Shared()` is a deny-list: it sets `Telemetry = nil` as it does `UpdateCheck`), `internal/cli/sharedcfg.go` (`sharedForbidden`), `internal/localcfg/localcfg_test.go`, `internal/cli/project_cmd.go` (the owner's publish writes `settings`), `internal/budget/rules/rules.json.tmpl` (the rules entry), `internal/budget/rules/rules_test.go`, `internal/budget/rules/emulator_test.go`.
- **Interfaces:** produces `localcfg.Telemetry{CrashReports *bool}` (nil means true) in the owner's project config. The owner's `fugaro project policy publish` writes it to a neutral **`settings` node beside the budget node** in the installation's database. The rules let the owner write it, let any signed-in user of the installation read it, and let nothing else write it. It never goes through the launcher-writable shared configuration. The owner asked why the earlier recommendation put the switch under `budget show`. The reason was only that `budget show` was the online read the console already ran; the switch is not a budget fact, and the node is neutral by the owner's ruling.
- **Tests:** `TestTelemetryCrashReportsDefaultTrue`, `TestTelemetryNotInShared`, `TestTelemetrySharedForbidden`, `TestPublishWritesSettings`, and the rules tests: the owner writes, a user reads, a user cannot write, and a run token cannot write.
- **Command:** `GOFLAGS=-p=2 go test ./internal/localcfg -run Telemetry -count=1 && GOFLAGS=-p=2 go test ./internal/cli -run 'Telemetry|SharedForbidden|PublishWritesSettings' -count=1 && GOFLAGS=-p=2 go test ./internal/budget/rules -count=1 && GOFLAGS=-p=2 go test -tags firebase ./internal/budget/rules -run Settings -count=1`.
- **Reviewer:** opus + mandatory independent review (a database rules change and an owner-only value).
- **Depends on:** L1.4 (the same `localcfg.go` and `project_cmd.go`), #1362.
- **Red step:** the rules test fails because a user cannot read `settings`.
- **Release notes:** the `### For operators` line (run `fugaro update` so the database rules gain the `settings` entry) is written by L5.1, which owns the CLI release's notes.
- **Commit:** `settings: the owner's crash-report switch, owner-written and user-readable`.

#### T1.2 `fugaro project show --json` prints the settings (O9 as ruled)

- **Files:** `internal/cli/project_cmd.go`, `internal/cli/project_enroll_test.go`, `console/docs/json-contract.md`.
- **Interfaces:** the document gains `settings: { crash_reports: true|false|null }`. `null` means the node could not be read (offline, signed out, no database), and the console then sends nothing (D734).
- **Tests:** `TestProjectShowSettings`, `TestProjectShowSettingsUnknownIsNull`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'ProjectShow|Docs' -count=1`.
- **Depends on:** T1.1, and V1.1 (the same contract).
- **Red step:** the field is absent.
- **Commit:** `project show: the installation's settings`.

#### T1.3 The console may run `fugaro project show --json` (O9 as ruled)

- **Files:** `console/src/main/ipc.ts` (`CONSOLE_OPERATIONS` gains the exact argv), `console/src/main/spawn.ts` if its allow-list lists argvs, their pinning tests in `console/test/unit/`, `docs/design/desktop-console.md` (the §5.1 table).
- **Interfaces:** a new allow-listed argv, `project show --json`, which `.claude/rules/console.md` marks security-sensitive: an independent review, its pinning tests and the §5.1 row.
- **Tests:** the IPC and spawn pinning tests with the new row, and `internal/cli/docs_desktop_console_test.go`.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 ipc spawn`, then `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Depends on:** T1.2, T0.1 (the same `desktop-console.md`).
- **Red step:** the pinning test refuses the unlisted argv.
- **Reviewer:** opus + mandatory independent review.
- **Commit:** `console: project show --json joins the allow-list`.

#### T2.1 The console reporter (D733, D734; DRAFT until the processor is named)

- **Files:** `console/package.json`, `console/package-lock.json` (`@sentry/electron`, exact), `console/test/unit/pinning.test.ts` (its runtime-dependency allow-list asserts `dependencies` is empty today), `console/src/main/crash.ts` (new), `console/src/main/index.ts`, the `settings.crash_reports` field decoded inside `console/src/main/crash.ts` from T1.3's `project show --json` operation (no change to `console/src/shared/schema.ts`), `console/test/unit/crash.test.ts` (new), `console/THIRD_PARTY_NOTICES.md`, `docs/licensing/dependency-audit.md`, `docs/design/desktop-console.md` (a new section: what is sent), `website/src/pages/privacy.astro` (the console paragraph), `website/test/privacy.mjs` (it scans `console/src/` for reporting hosts and pins "carries none"), `docs/releases/console/` (the release's notes).
- **Interfaces:**
  - Initialises only after `project show --json` returns `settings.crash_reports: true` for the chosen installation, and closes the client when the switch reads false or null.
  - `beforeSend` keeps the exception type and frames (function, app-relative file, line). It drops the message, breadcrumbs, contexts, user, request, `server_name`, tags, extra and local variables.
  - Off: native crash minidumps (process memory), session and release-health envelopes, and client reports. Only error events are sent.
  - Main process only (`@sentry/electron/main`), with no renderer preload or IPC injected, so the IPC allow-list and the CSP are unchanged. Renderer errors are not reported in v1.
  - The DSN comes from a build-time environment variable. A build without it sends nothing.
- **Tests:** `crash: nothing before the switch is read`, `crash: nothing with the switch off or null`, `crash: the scrubber drops message, home path, hostname and token-shaped placeholder`, `crash: no minidump, no session, no client report`, `crash: no preload is registered`, `pinning: @sentry/electron is exact and the only runtime dependency`, and the website's privacy test as amended.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 crash pinning`, then `cd website && npm run check`, then `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Depends on:** T1.3, T0.1 (the same `desktop-console.md`), T3.1 (the same privacy page and test), the processor named (O4).
- **Red step:** `crash: nothing before the switch is read` fails against a stub that initialises at startup.
- **Reviewer:** opus + mandatory independent review.
- **OWNER-RUN part:** the owner sets the DSN as the console build's secret, never in the repository. The release's `### For operators` names the variable.
- **Commit:** `console: crash reports, opt-out, stack frames only`.

#### T2.2 The console's public statements (D731)

- **Files:** `website/content/downloads.json` (the `no-telemetry` property rewritten, still containing the words "crash reporting" and "update check" that `website/test/console.mjs` requires: "No telemetry and no analytics; crash reporting, stack traces only, is on by default and can be turned off in the project config; an update check that carries no identifier"), `docs/design/desktop-console.md` (the §7.4 sentence that test pairs with the property), `website/test/console.mjs`, `README.md` (the console sentence).
- **Tests and command:** `cd website && npm run check`, then `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Depends on:** T2.1 (the statement ships with the code).
- **Red step:** `website/test/console.mjs` fails until the property and the test move together.
- **Commit:** `website, README: the console's crash-report statement`.

#### T3.1 The app: opt-out Crashlytics, no Analytics (D731, D734)

- **Files:** `mobile/package.json` and `mobile/package-lock.json` (drop `@react-native-firebase/analytics`), `mobile/app.config.js`, `mobile/src/app-config.test.js` and `mobile/src/app-config.ids.test.js` (they pin the analytics plugin), `mobile/firebase.json` (`crashlytics_auto_collection_enabled: false`, Analytics keys removed), `mobile/src/telemetry/index.js` (new: the module's only entry point), `mobile/src/telemetry/crash.js` (new: the one `setCrashlyticsCollectionEnabled(` call, from the settings node), `mobile/src/telemetry/crash.test.js` (new), `mobile/src/telemetry/scan.test.js` (new: the §7.4 scan as amended in T0.1, since P7.2/P7.3 are unbuilt), `website/src/pages/privacy.astro` (the app paragraph: Crashlytics, opt-out, no Analytics) and `website/test/privacy.mjs` (its `APP_TELEMETRY` list names the analytics package, so the page and the test move with the package in one task), `docs/releases/mobile/` (the release's notes).
- **Interfaces:** after sign-in, the app reads `settings/crash_reports` from the installation's database (T1.1's node; O9 as ruled), and follows later changes while signed in. Before sign-in, or if the node cannot be read, collection stays off and unsent reports are deleted (U2).
- **Tests:** `crash: off until signed in`, `crash: follows the settings node, including a later change`, `scan: exactly one setCrashlyticsCollectionEnabled call site, no analytics import`.
- **Command:** `cd mobile && npm test`, then `cd website && npm run check`.
- **Depends on:** T0.1, T1.1, #1332, and the app's sign-in to the installation's database.
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
- **Interfaces:** `GRAPH_MIN_SCALE` (the node width at which a title stays legible). When the panel width divided by the picture's width falls below it, the canvas takes `width = picture width × GRAPH_MIN_SCALE` inside a wrapper that scrolls horizontally, and that graph only. `LAYOUT_OPTIONS` (`console/src/renderer/graph/layout.ts`, unchanged) stays `layered` and `DOWN` with no wrapping key.
- **Tests:** `graph: fits the width above the minimum scale`, `graph: scrolls horizontally below it`, `layout: no wrapping option`.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 graph layout`, then `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1` (the styling doc is pinned).
- **Depends on:** the console redesign's steps 5 to 7 (console 0.116.0), the ones that edit `screens.css` and `console-styling.md`.
- **Red step:** the minimum-scale test fails while the canvas always fits.
- **OWNER-RUN part:** the graph goldens are re-recorded by the owner.
- **Commit:** `console: the graph keeps a legible scale and scrolls sideways only when it must`.

### Slice V: live gauges (D736, D737)

#### V1.1 The watch document carries `max_parallel`

- **Files:** `internal/cli/watch.go`, `internal/cli/watch_test.go`, `console/docs/json-contract.md`.
- **Interfaces:** the document Live decodes (`fugaro watch --json`, closed `LIVE_KEYS`) gains `max_parallel`. Headroom is already there: Live takes it from `total.daily_cap_usd`. Live does not read `budget show`.
- **Tests:** `TestWatchJSONMaxParallel`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'TestWatchJSON|Docs' -count=1`.
- **Depends on:** none.
- **Red step:** the field is absent.
- **Commit:** `watch: max_parallel for the console's cap bars`.

#### V1.2 The spend-rate gauge, one-hour sparklines and cap bars on Live

- **Files:** `console/src/renderer/screens/live.ts`, `console/src/renderer/theme/flight-deck.css`, `console/src/shared/schema.ts` (`LIVE_KEYS` gains `max_parallel`), `console/test/unit/live.test.ts`, `docs/design/console-styling.md`.
- **Interfaces:**
  - An SVG gauge in $/h. Its red zone starts at the rate that would use up the day's remaining headroom before the UTC day ends.
  - `HourHistory` is a second bounded history (one hour, kept only while the console runs) beside the 5-minute `BurnHistory`, whose pin to `BurnWindow` is untouched.
  - Cap bars: spend and concurrent runs against `max_parallel`.
  - Every figure goes through `hidesView` and `formatReading`.
- **Tests:** `live: red zone from headroom and hours left`, `live: hour history bounded`, `live: burn window still equals BurnWindow`, `live: concurrent bar against max_parallel`.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 live`, then `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Depends on:** V1.1, G1.1 (the same `console-styling.md`), and the console redesign's steps 5 to 7 (`live.ts`, `flight-deck.css`, `console-styling.md`).
- **Red step:** the red-zone test fails because no gauge exists.
- **OWNER-RUN part:** the Live goldens.
- **Commit:** `console: the spend-rate gauge, hour sparklines and cap bars`.

#### V1.3 The iterations bar on the Run screen

- **Files:** `internal/cli/diagnose.go` and its test (the document the Run screen reads), `console/docs/json-contract.md`, `console/src/shared/schema.ts` (`RECORD_KEYS`; `closed()` refuses unknown keys), `console/src/renderer/screens/run.ts`, `console/src/renderer/theme/screens.css`, `console/test/unit/run.test.ts`, `docs/design/console-styling.md`.
- **Interfaces:** `diagnose --json` gains each level's rounds used and its cap. No console decoder reads `reviews` today. The Run screen draws one bar per level.
- **Tests:** `run: iterations bar per level`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Diagnose|Docs' -count=1`, then `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 run`.
- **Depends on:** L4.1, L4.2, G1.1, V1.2, T1.2 and J1.3 (the same stylesheet, styling doc, `schema.ts` and contract, at earlier levels).
- **Red step:** the bar is absent.
- **Commit:** `console: the run's review rounds against their caps`.

### X1 Checks 270–272, written into the register

- **Files:** `docs/gcp-live-checklist.md`.
- **The checks:**
  - **270:** a Critical run on the sandbox repository whose L3 rejects once. The PR body shows L2's head older than L3's, and `result.json` records tiers `first`, `senior` and `adversarial`.
  - **271:** a console crash, forced with a debug build's test trigger, arrives at the processor with type and frames only. With `crash_reports: false`, none arrives.
  - **272:** the same for the app on a development build, before sign-in and after sign-in with the switch off.
- All three are **OWNER-RUN, NOT RUN**.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Depends on:** L4.2, T2.1, T3.1. #620, which is open and edits the same register, merges or rebases first.
- **Red step:** none. It is a register entry, and the Go docs tests check the register's format and numbers.
- **Commit:** `checklist: checks 270-272 for review levels and crash reports`.

### X2 Check 273, written into the register

- **Files:** `docs/gcp-live-checklist.md`.
- **The check:** **273:** `/jobmax` on a sandbox plan launches only tasks whose dependencies are merged, stops at a cap, and prints what it launched. **OWNER-RUN, NOT RUN**.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Depends on:** J1.2, and X1 (the same register).
- **Red step:** as X1.
- **Commit:** `checklist: check 273 for /jobmax`.

## Parallel execution

**Edges** (from the Depends-on lines; external gates in brackets; a level is one more than the highest level
among a task's internal dependencies):

- L0.1 → L1.1 → L4.3
- L1.1, L2.1 → L1.2
- L1.1 → L1.3
- L1.1, L1.3, L1.4, L2.2 → L3.1
- L3.1, L1.2, L5.2 → L3.2
- L3.1 → L4.5
- L1.1, L2.1, L3.1, L4.1, L4.3, L4.5 [#1369] → L4.2
- L1.3, L2.1 → L4.4
- L4.2, L3.2, J1.1, T1.1 → L5.1
- L1.1, L4.1, L1.2 → B1.1 [before A3.3]
- B1.1 [A3.3] → B1.4
- B1.1, L4.2 [A5.2] → B1.2
- B1.2, B1.4 [A4.3, A5.4] → B1.3
- L1.4 → T1.1 → T1.2 → T1.3
- V1.1 → T1.2
- T1.1, T1.2 → J1.3
- J1.1, J1.3 → J1.2
- [#1332] → T0.1
- T0.1, T1.1 [#1332, app sign-in] → T3.1 → T3.2
- T1.3, T0.1, T3.1 [O4] → T2.1 → T2.2
- [console redesign steps 5–7] → G1.1
- V1.1, G1.1 → V1.2
- L4.1, L4.2, G1.1, V1.2, T1.2, J1.3 → V1.3
- L4.2, T2.1, T3.1 [#620] → X1
- J1.2, X1 → X2

**Levels:**

| Level | Tasks |
| --- | --- |
| 0 | L0.1, L1.4, L2.1, L2.2, L4.1, L5.2, J1.1, T0.1, G1.1, V1.1 |
| 1 | L1.1, T1.1, V1.2 |
| 2 | L1.2, L1.3, L4.3, T1.2, T3.1 |
| 3 | L3.1, L4.4, B1.1, J1.3, T1.3, T3.2 |
| 4 | L3.2, L4.5, B1.4, J1.2, T2.1 |
| 5 | L4.2, T2.2 |
| 6 | L5.1, B1.2, V1.3, X1 |
| 7 | B1.3, X2 |

**Critical path (inside this plan):** for the review levels, L0.1 → L1.1 → L1.3 → L3.1 → L4.5 → L4.2 → L5.1 / X1,
seven tasks. It starts at once; L4.2 also waits until no other run writes `internal/runner/runner.go`. The longest chain
overall is eight tasks, L0.1 → L1.1 → L1.3 → L3.1 → L4.5 → L4.2 → B1.2 → B1.3, and it also waits for the A/B plan's
A3.3 and A5.x. Slices G and V are off the critical path. The console's T2.1 waits on the processor's name (O4).

**What must be sequential and why:**

- L1.1 after L0.1: the closed effort set.
- L1.3 after L1.1: the same `config.LevelSetting` type and closed effort set.
- L3.1 after L1.1, L1.3, L1.4 and L2.2: it consumes the settings and the defaults, and applies the floor.
- L3.2 after L5.2: this repository's own configuration must pass the check L3.2 ships.
- L4.5 and L4.2 after L3.1: they consume the resolved levels, and L4.2 routes through L4.5.
- T1.1 after L1.4, then T1.2, then J1.3 and T1.3: the same `localcfg.go`, `project_cmd.go` and contract; T1.3 allow-lists what T1.2 prints.
- B1.1 after L1.1, L4.1 and L1.2: the same files.
- T2.1 after T1.3 and T3.1: the argv it calls, and the same privacy page and test.
- V1.3 after G1.1, V1.2, T1.2 and J1.3: the same stylesheet, styling doc, `schema.ts` and contract.

**Shared-files table** (every file with more than one writer; no level holds two writers of a file, and every
pair of writers is joined by a dependency path):

| File | Writers (level) |
| --- | --- |
| `internal/task/task.go`, `schemas/task.schema.json` | L1.1 (1), B1.1 (3) |
| `internal/runstore/runstore.go`, `schemas/result.schema.json` | L4.1 (0), B1.1 (3) |
| `internal/cli/recipes_skew.go`, `internal/cli/recipes_skew_test.go` | L2.1 (0), L1.2 (2), B1.1 (3) |
| `internal/localcfg/localcfg.go` | L1.4 (0), T1.1 (1), J1.3 (3) |
| `internal/localcfg/shared.go`, `internal/cli/sharedcfg.go` | L1.4 (0), T1.1 (1) |
| `internal/cli/project_cmd.go` | L1.4 (0), T1.1 (1), T1.2 (2), J1.3 (3) |
| `internal/cli/project_enroll_test.go` | T1.2 (2), J1.3 (3) |
| `internal/cli/run.go`, `internal/cli/run_review_test.go` | L1.2 (2), L3.2 (4) |
| `internal/runner/runner.go` | L4.2 (5), B1.2 (6) |
| `docs/recipes.md` | L2.1 (0), L5.1 (6) |
| `plugin/skills/parallelism/SKILL.md` | J1.1 (0), J1.2 (4) |
| `plugin/skills/working/SKILL.md` | J1.1 (0), L5.1 (6) |
| `docs/gcp-live-checklist.md` | X1 (6), X2 (7) |
| `docs/design/desktop-console.md` | T0.1 (0), T1.3 (3), T2.1 (4), T2.2 (5) |
| `docs/mobile/implementation-plan.md` | T0.1 (0), T3.2 (3) |
| `console/docs/json-contract.md` | V1.1 (0), T1.2 (2), J1.3 (3), V1.3 (6) |
| `console/src/shared/schema.ts` | V1.2 (1), V1.3 (6) |
| `docs/design/console-styling.md` | G1.1 (0), V1.2 (1), V1.3 (6) |
| `console/src/renderer/theme/screens.css` | G1.1 (0), V1.3 (6) |
| `website/src/pages/privacy.astro`, `website/test/privacy.mjs` | T3.1 (2), T2.1 (4) |
| the CLI release's notes (`docs/releases/`) | L1.2 (2), L3.2 (4), L5.1 (6) |

Every other file in the plan has one writer.

## Check numbers this plan takes

| Check | What it measures | Who runs it |
| --- | --- | --- |
| 270 | A Critical run's L3 bounce skips L2, readiness follows the highest level, and the PR body shows each level's head | the owner, on the sandbox installation; NOT RUN |
| 271 | The console's crash report holds type and frames only, and none is sent with the switch off | the owner; NOT RUN |
| 272 | The app sends none before sign-in or with the switch off, and type and frames only otherwise | the owner, on a development build; NOT RUN |
| 273 | `/jobmax` on a sandbox plan launches only tasks whose dependencies are merged, stops at a cap, and prints what it launched | the owner; NOT RUN (written by X2) |

Checks 274–289 stay reserved and unused.

## After deploy

- **T2.1:** the console's release notes, `### For operators`, name the build-time DSN variable. The owner sets it as a build secret before the release build.
- **T1.1:** the database rules gain the `settings` entry only when the operator runs `fugaro update`. Until then, the console and the apps read `null` and send nothing.
- **Before the 0.15.0 tag:** `reviewLevelsSince` and `adversarialSince` name 0.15.0 only if L4.2 is in it. If L4.2 has not merged, both move to the next release before the tag, so no 0.15.0 image accepts a level it does not run.
- **L1.2:** job images must be refreshed (`fugaro image refresh`) before any launcher sets `--review-levels`. The release's `### For operators` says so.
