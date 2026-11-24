# Review levels, A/B for every slot, job max, crash reports, console graph and gauges: implementation plan

## Status

**Re-plan 2026-11-21 (the owner's instruction): T3.1, V1.3 and X1 are re-planned in this document, against the
orchestrator (`docs/stateless-orchestrator/`) and the config-ownership registry (`docs/config-ownership/`), and X2
is closed. Nothing in this re-plan is approved: it is a document for the owner's review, and no task in it launches
until the owner says so.** The re-plan records one new decision, **D738** (in [reconciliation.md](reconciliation.md) §3),
and **it is a decision the owner is being asked to make, not the restatement of a settled one** (corrected
2026-11-21: the first text of this re-plan said it "decides nothing D734 had not already answered"). D734's answer of
2026-10-30 was that the mobile client reads the switch from the pairing-delivered configuration, and **the owner's
ruling on O9 the same day amended it**: "the switches go in a neutral, owner-written `settings` node beside the budget
node … the apps read the node" (reconciliation.md §4). That node is T1.1–T1.3, closed on 2026-11-16 and built by
nobody, so **D738 puts the switch back on the pairing-delivered configuration D734 first named** — the app reads it as
`crash_reports` in the pairing session's response, carried by a top-level `mobile_crash_reports` key of the published policy (beside the `mobile` block, not a member of it). The reversal of
O9's amendment is the review's first question. What moved, in one line each: **T3.1** keeps its slot and moves off
T1.1's `settings` node onto the published project policy, which a phone already reads; **V1.3**
keeps its slot and loses the two closed dependencies it carried for shared files only; **X1** keeps its slot, and
two of its three checks are re-worded against the console and the app as built; **X2 disappears** — Check 273 is
closed with nothing written and nothing run. The re-plan's own section, below, states the contract-first pass, the
edges and, for each task, what it consumes from the orchestrator's state and the registry.

**Ruling 2026-11-16: the orchestrator (`fugaro:orchestrator`) replaces `/jobmax`. J1.2, J1.3 and T1.1–T1.3 are closed as superseded (no `jobmax.auto_launch` is built); V1.2 is unchanged.** **Status as of 2026-11-10: in progress, about 24/40 merged; see [docs/status.md](../status.md) for the current table.** The owner approved every task on 2026-10-30, with these
exceptions: T2.1 and T2.2 stay draft until the processor is named (O4); O1 and O2 are counsel's; T3.2
(O6), the checks X1 writes (**270–272**; X2's Check 273 was closed unrun on 2026-11-21) and every OWNER-RUN part below are the owner's. The owner
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

- O7, O9, O10 and O11 were ruled on 2026-10-30. O7: the missing step is inserted (L3.1). O9: the `settings` node (T1.1–T1.3, J1.3). O10: Hotfix takes Pro's levels (L2.2). O11: the refusal is accepted, and this repository's `fugaro.yaml` changes (L5.2). Ruled again on 2026-11-01, in the fix round on L3.1's PR: the refusal stays and is **by exact model ID**, and a run whose level 1 and level 2 are **both** left unset — no model named anywhere, `--recipe standard` — **warns** that the senior review is not independent of the coder rather than refusing, because two unset models are not an ID the resolver can compare and refusing them would refuse every installation that names no models at all.
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
| G9 | **RETIRED 2026-11-21.** `/jobmax` launches unattended only with the owner's opt-in on (default off), never launches a task whose dependency is unmerged (an unknown merge state counts as unmerged), never launches past a cap, and stops and asks on any ambiguity (D730 as ruled) | **was** J1.2, J1.3, Check 273 — J1.2 and J1.3 closed 2026-11-16 and Check 273 closed with X2 on 2026-11-21, so this constraint pins nothing here. The behaviour is the orchestrator's, and its constraints are that work's **G4** and **G5** (`docs/stateless-orchestrator/implementation-plan.md`), pinned by O6 and its tests; the live half is that work's **Check 302**, not this one's |
| G10 | The A/B `slot` field is fixed before the pair launch writes pairs (D728), so pairs are not written twice in two formats | B1.1 before A3.3 |

**Trust boundaries to probe in review:**

- The task field and the project layer are written by the launcher (`docs/bucket-iam/spec.md` §10 L1, L2), so a hostile task could set a level to `none`, name a model, or embed a layer. The floor is read from the owner-only policy node, and the runner re-runs `ResolveLevels` at bootstrap.
- The `effort` value is passed into the agent's environment, so it is accepted only from a closed set.
- A crash payload leaves the machine: G6, and the DSN cannot be redirected by configuration.
- **The `settings` node is retired (2026-11-21).** T1.1 would have added new database surface written by the owner and read by signed-in users. Nothing adds it: the two switches that would have lived on it are carried by surfaces that already exist — the console's per-user `<userData>/crash.json` (the owner's ruling of 2026-10-31) and, for the app, the published project policy's `mobile` block (T3.1, D738). Both are narrower than the node: a phone reads one boolean through the pairing session and never the policy document, and `config/policy/**` stays out of the paired device's read set.
- **The app's switch is policy surface, and the phone is not its writer.** `mobile_crash_reports` sits beside the block the owner publishes — a top-level policy key, and deliberately **not** a member of the strictly-decoded block (`internal/projectpolicy`), so an older reader tolerates it rather than refusing the whole policy — and the phone reads it through `internal/mobilesession`'s session response — the same one-way path `mobile.costly_modes` already takes. Nothing a phone can reach writes it; `SessionPolicy`'s own comment is the reason (D2791: every value there is a display input, and what a mode permits is decided at the consumer).
- **`/jobmax` reads plan files from the repository, which are untrusted text** — retired with J1.2 (2026-11-16). The orchestrator's launcher reads its work items from its own state (`orchestrator/state`), which is not repository text; that work's own trust boundaries cover it.

## In-flight work this plan waits for (file conflicts)

Open PRs on 2026-10-30 against `main` at `8c9819ea7`. #1362, #1367, #1369 and #1360, named in the first
version of this table, have merged, and so has the console redesign's step 4b-2.

| In flight | Files shared with this plan | Tasks that wait |
| --- | --- | --- |
| #1332 (mobile P7.1b, draft) | `mobile/app.config.js`, `docs/mobile/telemetry-plan.md`, `docs/mobile/implementation-plan.md` | T0.1, T3.1 |
| #1392 (console auto-update U3, halted) and U4, U5 | `console/package.json`, `console/package-lock.json`, `console/src/main/index.ts`, `console/test/unit/pinning.test.ts`, `console/THIRD_PARTY_NOTICES.md` | T2.1 |
| #620 (managed-projects checks) | `docs/gcp-live-checklist.md` | X1, X2 |
| The console redesign's steps 5 to 7 (console 0.116.0) | `console/src/renderer/theme/flight-deck.css`, `screens.css`, `console/src/renderer/screens/live.ts`, `run.ts`, `docs/console-styling/spec.md` | G1.1, V1.2, V1.3 |
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
- **Done (PR "run: --review-levels, gated by the job image's version").** Red first: `TestRecipesSkewRefusesReviewLevelsOnOldImage` and `TestRecipesSkewRefusesResolvedL3OnOldImage` failed to build (`undefined: reviewLevelsAsk`, `undefined: checkReviewLevelsImage`, `undefined: checkCloneReviewLevelsImage`), and `TestRunReviewLevelsFlagWritesTaskField` failed with "pass the task as TEXT or with --task-file" before the flag existed. The gate asks what the launch asks (`reviewLevelsAsk`) rather than what the flag wrote: the resolved levels (`config.ResolveLevels`, called by `launchReviewLevelsAsk` with the mode the flag and the ref's file resolve, the owner's floor off the memoised policy node, the task's or this checkout's recipe) name a level 3 the mode's own row turns on and a first-line step the run inserts (O7) with no task field and no configuration key, and the two layer blocks name what only a runner acting on levels reads (`l1.none`, `l2.model`, L1.3's `agent.levels`). A configuration the launcher has not read — a launch from outside the repository's checkout — is resolved against an empty one, so the mode the flag asked for and the owner's floor are still judged; a resolution that refuses is not read as its zero levels but asks the level 3 the mode's own row turns on, leaving the refusal itself to the runner; an unreadable policy node asks nothing, which is the gate that owns it. A launch that asks nothing is never gated, whatever its image. `reviewLevelsSince` is 0.16.0, the release whose runner acts on levels (L4.2, the same number `adversarialSince` names): 0.15.0 decodes both and acts on neither, so a launch it accepted would drop every level in silence. `--review-levels` reads a file or `-`, refuses `-` beside `--task-file -`, and decodes strictly here so a near miss costs no cloud call; it is in `taskFlags`, so `--retry` takes none and a stored task keeps the block it was launched with. `docs/rigor-modes/reconciliation.md`'s image-skew census row moved 23 → 24 in its own commit, as that folder's rule requires of the commit writing a constant.

#### L1.3 Per-level settings in `fugaro.yaml` and the project layer (D722, D726)

- **Files:** `internal/config/config.go`, `internal/config/layer.go`, `internal/config/validate.go`, `internal/config/defaults.go`, `schemas/fugaro.schema.json`, `schemas/project-layer.schema.json`, `internal/config/config_test.go`, `internal/config/layer_test.go`.
- **Interfaces:** produces `agent.levels.{l1,l2,l3}.{model, effort, none, focus}` in `fugaro.yaml` and in the project layer's `defaults.agent.levels`. Also produces `agent.adversarial_rounds` (1–10, default 2, `config.DefaultAdversarialRounds`). **Superseded by `DefaultRounds`/`AdversarialDefault` (#1483): the default for a level-3 round count is the mode's own senior row (`rigor.AdversarialDefault`, 1 at poc, 2 at startup, 3 at pro, 5 at critical), and no `DefaultAdversarialRounds` constant is declared.** `first_line_rounds` stays default 1 and max 3; `review_rounds` stays default 2. How the keys relate: `agent.levels.l2.model`, when set, wins over `agent.models.reviewer`, and `agent.levels.l1.model` defaults to the coder's model. A recipe's `roles` mapping is applied first. `agent.levels.l1.none`, or an explicit `l1` entry, wins over `agent.first_line_review`, and with neither set, `first_line_review` decides as today. `focus` is bounded to 500 bytes at every layer, not only in the task.
- **Tests:** `TestLevelsDecodeStrict`, `TestAdversarialRoundsDefaultAndBounds`, `TestLayerLevelsOverlay`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/config ./schemas -run 'Levels|AdversarialRounds|Schema' -count=1`.
- **Depends on:** L1.1. Each level's settings reuse `config.LevelSetting` and its closed `config.Efforts`, so this task cannot define them a second time.
- **Red step:** `TestLevelsDecodeStrict` fails because `levels` is an unknown field.
- **Commit:** `config: per-level review settings and agent.adversarial_rounds`.
- **Done (PR "config: per-level review settings and agent.adversarial_rounds").** Red first: `TestLevelsDecodeStrict` failed with `line 12: field levels not found in type config.Agent`, and `TestAdversarialRoundsDefaultAndBounds` with `line 11: field adversarial_rounds not found in type config.Agent`. Shipped as the supersede note above rules: 1–10, no `DefaultAdversarialRounds` constant, unset leaving the number to the mode's own senior row. `agent.levels` is the typed `config.ReviewLevels`, so the block's rules are `config.ReviewLevels.Validate`'s at both paths (`agent.levels` in the repository's file, `defaults.agent.levels` in the project layer), and the two layers merge key by key.

#### L1.4 The owner's level floor, owner-only and tighten-only (D722)

- **Files:** `internal/localcfg/localcfg.go`, `internal/localcfg/shared.go` and `internal/cli/sharedcfg.go` (the floor never enters the launcher-writable shared configuration: `Shared()` drops it and `sharedForbidden` names it), `internal/localcfg/localcfg_test.go`, `internal/projectpolicy/policy.go`, `internal/projectpolicy/policy_test.go`, `internal/cli/project_cmd.go` (the publish builds `projectpolicy.Policy` field by field), `internal/runner/policy.go` (the job reads the node at bootstrap), `internal/policy/policy.go` (the field on `Effective`), `internal/policy/merge.go`, `internal/policy/merge_test.go`.
- **Interfaces:** produces `review_floor: {<mode>: [l1|l2|l3, …]}` in the owner's project config, published by `fugaro project policy publish` into the **owner-only policy node** (`internal/projectpolicy`). It is **not** a project-layer key: `docs/bucket-iam/spec.md` §10 L2 says a launcher can embed any project-layer text in its own `task.json` and the runner trusts it, so a floor there could be dropped by the launcher it is meant to bind. The runner reads the node at bootstrap with its run token. `policy.Effective.ReviewFloor(mode)` merges as a union, so only tightening wins. A launcher with the owner's file applies it early (L3.2), and the job is authoritative. `projectpolicy.Decode` ignores unknown top-level keys, so a runner older than L4.2 would drop the floor silently. L1.2's gate on resolved levels refuses such an image whenever a floor is set. **No policy node:** `fugaro project policy publish` needs the installation's database and a `budget.mode`. An installation without them can publish no floor, so it runs on the rigor defaults alone, and `fugaro validate` warns when the owner's file sets a floor that cannot be published.
- **Tests:** `TestReviewFloorMergesTightenOnly`, `TestReviewFloorPublishedToPolicyNode`, `TestReviewFloorNotInLayer`, `TestReviewFloorNotInShared`, `TestRunnerReadsReviewFloorFromNode`. `TestReviewFloorUnpublishableWarns` is L3.2's, because the warning is printed by `internal/cli/validate.go`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/policy ./internal/localcfg ./internal/projectpolicy -run 'ReviewFloor' -count=1 && GOFLAGS=-p=2 go test ./internal/runner -run 'TestRunnerReadsReviewFloor' -count=1 -timeout 30m`.
- **Depends on:** none.
- **Red step:** the merge test fails because the key does not exist.
- **Reviewer:** opus + mandatory independent review (a policy merge that could loosen, and an owner-only value).
- **Commit:** `policy: the owner's review-level floor, owner-only and tighten-only`.

#### L2.1 The level-3 recipe step (D720, D726)

- **Files:** `internal/recipe/recipe.go`, `internal/recipe/stages.go`, `schemas/recipe.schema.json`, `internal/cli/recipes_skew.go`, `internal/cli/recipes_skew_test.go`, `docs/recipes.md`, `docs/recipes/spec.md`, `internal/recipe/recipe_test.go`, `internal/recipe/stages_test.go`.
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
- **Done (PR "config: resolve review levels, the owner floor and the same-model refusal").** Red first: `TestResolveLevelsRefusesSameModel` failed with "a run with l1 and l2 both on deepseek/deepseek-v4-flash launched; want D721's refusal", and `TestResolveLevelsWarnsCoderAndFamily` and `TestResolveLevelsRefusesFirstLineRecipeWithCoderEqualsReviewer` with it, before the guard existed. `LevelInputs` takes the floor as `[]policy.Level` (internal/policy does not import internal/config, so the two do not cycle) and carries `CatalogDefault`, because the catalog's own `default` is the one recipe whose `first_line` step follows `agent.first_line_review` (docs/recipes.md §2, `runner.planOf`'s `derived`). Level 1's fallback is `Agent.FirstLineOn` except where the mode's own row turns it on (POC), and a recipe with a `first_line` step that is not the catalog default always runs it (O11). The resolver inserts nothing itself: `ResolvedLevel.Inserted` says which step the runner adds (L4.2).

#### L3.2 `fugaro validate` and launch apply the resolver

- **Files:** `internal/cli/validate.go`, `internal/cli/run.go`, `internal/cli/validate_test.go`, `internal/cli/run_review_test.go`.
- **Interfaces:** consumes L3.1 and L1.2. `validate` prints the warnings and refusals, and `--json` carries them. Launch refuses before writing the task, and hands the resolved levels to L1.2's version gate.
- **Tests:** `TestValidateLevelsSameModel`, `TestRunRefusesLevelsBelowFloor`, `TestRunGatesResolvedLevelsOnImage`, and L1.4's `TestReviewFloorUnpublishableWarns`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'ValidateLevels|RunRefusesLevels|RunGatesResolvedLevels' -count=1`.
- **Depends on:** L3.1, L1.2, L5.2 (this repository's own `fugaro.yaml` must not be refused by the check this task ships).
- **Red step:** validate passes a same-model configuration before the change.
- **Release notes:** the CLI release says that a coder = reviewer configuration on a recipe with `first_line` is now refused (O11).
- **Commit:** `validate, run: the review-level checks at the launcher`.
- **Done (PR "validate, run: the review-level checks at the launcher (L3.2)")**. Red first: `TestValidateLevelsSameModel` reported `out = ".../fugaro.yaml is valid\n", err = <nil>` — a file naming the same model for the coder and the reviewer validated clean. `validate` resolves the file's own runs through `config.ResolveLevels` with the project layer that file's routing hands them (`reviewLevelProblems`), prints its warnings and its refusals under `agent.levels` in text and in `--json`, and warns when a `review_floor` could never have been published (`reviewFloorUnpublishable`: no `budget.mode` or no database, naming both halves and `fugaro project policy publish`). It passes no `allowed_models`: the launcher knows no routes, and a list handed to the resolver is a membership test that would refuse a level model a routed run may use — the run's own gate owns that. The launch's resolution (`launchReviewLevelsAsk`, `internal/cli/recipes_skew.go`, outside this task's Files list because the launcher's level resolution lives there and `run.go` alone cannot express the refusal) now refuses the launch on the resolution's own refusal, before the task is written: a refused resolution answers with the zero `Levels`, which read as "no level is on", so the old code asked nothing of the image and reported the launch as the one it wanted — the fail-open direction `TestRecipesSkewRefusesAResolutionThatRefused` recorded, and now pins refused on every image, since a run this launcher may not make is not a launch whose image is worth judging. A configuration with no file read (a launch from outside the checkout) still resolves against the empty one and the mode's row. Two tests in `validate_test.go` moved with the legitimate coder = reviewer warning: `TestValidateWarnsOnLooserThanCeiling`'s count and strip list, and `TestValidateWithoutProjectConfigChecksShapeOnly`'s stderr.

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
- **Done (PR "agent: effort for a Claude stage, never a provider's (L4.3)")**: nothing is wired into a stage here, so until L4.2 lands `EffortVars` has no caller outside its tests. The wiring is L4.2's, not this task's.

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
- **Done (PR #1578).** Red first: `TestLevelsL3BounceSkipsL2` failed because the runner rejects the step — a recipe carrying an `adversarial` step is refused at bootstrap until L4.2 runs it. The plan gains the adversarial arm (after the review step, never L2 between it and the bounce) and `checkPlan` was narrowed to the two things the levels require of a plan: no plan skips the senior review, and no plan drops a step a resolved level needs. The stage itself is the plan's step run through `stageOnce` with its own prompt (`AdversarialPrompt`, the senior brief plus the level's focus) and the level's own model and effort (`stageModel`, `applyStageEffort`, so `agent.EffortVars` reaches a Claude stage and never a provider route, D725). `applyLevels` runs at bootstrap, after the routing switch — level 1's own answer is read off the coder's model (`Agent.FirstLineOn`), so a resolution taken before the rewrite would drop the cheap loop for exactly the runs a switch routed — so a hand-written task meets `config.ResolveLevels`' refusals (the floor, the same-model guard, the effort set) before any stage starts; the allow-list is the run's own gate's (`checkBudget`'s `checkModels(CheckAllowed)`), which judges each level's model as `agent.levels.lN.model` — a list handed to the resolver instead is a membership test that knows no routes and would refuse a routed level model the run may use. Readiness is the highest enabled level's last round (`decidingReview`), with an enabled outer level that did not ship holding the pull request back (`heldBackLevel`, `heldBackReason`, D727) and a mode raised at finalize still blocking (T3.5); a level whose round is missing decides nothing, so the run stays a draft. The PR body's `review levels:` line names each level's verdict, round and head, and the focus beside the level it belongs to. The version rule moved: `adversarialSince` is 0.16.0, not 0.15.0, because 0.15.0 shipped with the step parsing but no runner running it (a 0.15.0 run stopped at planning) — L4.2 missed that tag.

#### L4.5 A model per review level in routing

- **Files:** `internal/runner/routing.go`, `internal/runner/agentenv.go` (`stagePins` applies the provider route's variables), `internal/runner/gateway.go` (the run's allowed set), `internal/runner/routing_levels_test.go` (new).
- **Interfaces:** consumes L3.1. Today a stage pins one of two role slots plus the background model (`pinSlots`). This task routes an L1 or L3 model that differs from the coder's and the reviewer's: the stage's pins, its provider route (`agent.ProviderRouteVars` when a provider claims the model), and the gateway's allowed set for the run, which must name every level's model.
- **Tests:** `TestRoutingL1ModelDistinctFromCoder`, `TestRoutingL3ProviderModelGetsRouteVars`, `TestRoutingRefusesLevelModelOutsideAllowedModels`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/runner -run 'TestRouting' -count=1 -timeout 30m`.
- **First commit:** records U5: whether in-job review Q9's gate (a Claude reviewer in a job waits for the credentials service) still binds under `auth: api-key` with both keys, read from `internal/runner/routing.go` and `docs/multi-model.md`. If it binds, a Claude model at L2 or L3 is refused next to a provider coder until the credentials service lands, and `ResolveLevels` (L3.1) says so. **It does not bind:** with `api-key` the job mounts `anthropic-api-key` for a Claude **pin** no route serves (`config.AnthropicKeyNeeded` is exactly that rule, over the coder, the reviewer and the background: a level's own model is L4.5b), the gateway attaches that key to a direct call, and Q9's wait was for the cascade's own subscription token, so L4.5 adds no refusal to `ResolveLevels` (reconciliation §5, U5).
- **Depends on:** L3.1.
- **Red step:** an L1 model different from the coder's runs on the coder's pin.
- **Reviewer:** opus + mandatory independent review (the gateway's allowed models).
- **Commit:** `runner: route each review level's own model`.
- **Done (PR "runner: route each review level's own model").** Red first: `TestRoutingL1ModelDistinctFromCoder` failed with `review_first CLAUDE_CODE_SUBAGENT_MODEL = "claude-sonnet-5-5", want the level's own model "deepseek/deepseek-v4-pro"` — level 1 is the coder's stage, and it was given the coder's model. `stageModel` answers with the level's own model where a layer named one, and the three consumers that have to agree read it: the stage's pins (`stagePins`, which is what adds `agent.ProviderRouteVars` for a provider-claimed level model), the gateway's set for the stage (`stageSpec`, the same model as the pins or every call of the stage is refused as not pinned), and the routing switch (`pinSlots` carries each level's model as a pin of its own, so `applyRouting` moves a level model the switch names to the route's ID). `applyLevels` is the seam L4.2 calls with its inputs: it resolves through `config.ResolveLevels` (L3.1) and writes every level that runs into the run's own configuration, and a refused resolution — a level model outside the run's `allowed_models` — writes nothing, so no stage is pinned with it. `stageRole` is `config.StageRole` plus `adversarial`, which L4.2 teaches that function. U5 is recorded above: the credentials-service gate does not bind under `auth: api-key`, so `ResolveLevels` gained no refusal. L4.2 still wires it in: it calls `applyLevels` — the stage request's own model (`runner.go`'s `stageOnce`) reads `stageModel` here already.

#### L4.5b `NeedsAnthropicKey` reads a level's own model

- **Files:** `internal/config/providers.go`, `internal/config/needs_anthropic_key_test.go`.
- **Interfaces:** consumes L3.1. `NeedsAnthropicKey` judges the run's coder, the reviewer and the background and no other model, so a run whose every pin a provider may serve and whose L2 or L3 model is a Claude one asks for no key at all: `fugaro update --repo` never prompts, and the runtime drops the mount where the secret has no enabled version, so the level's call is refused at run time with the refusal's own advice (`fugaro secrets set anthropic-api-key`) instead of the key being there. Fail-safe, and still the wrong answer: the owner has a key for exactly this call and the run does not ask for it. The rule reads each enabled level's own model as well, the models `stageModel` runs (`internal/runner/routing.go`, L4.5), so a level model no allowed provider serves needs the key. It changes no wire format: the answer only decides that prompt and that mount, both of which already follow the rule.
- **Tests:** `TestNeedsAnthropicKeyReadsALevelModel`, `TestNeedsAnthropicKeyLevelModelServedByAProvider`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/config -run 'NeedsAnthropicKey|AnthropicKeyNeeded' -count=1`.
- **Depends on:** L3.1.
- **Red step:** a run with a Claude level-2 model and every pin on a provider asks for no key.
- **Reviewer:** opus (the rule decides a mount, not a refusal).
- **Commit:** `config: the Anthropic key rule reads a level's own model`.
- **Done (PR "config: the Anthropic key rule reads a level's own model").** Red first: `TestNeedsAnthropicKeyReadsALevelModel` failed with `NeedsAnthropicKey = false, want true` for a Claude model at each of l1, l2 and l3 under provider pins, and `TestNeedsAnthropicKeyLevelModelServedByAProvider` with the unclaimed `openai/gpt-9` level model reported as no call to Anthropic. `NeedsAnthropicKey` now reads each level's own model off `Agent.Levels` beside the coder, the reviewer and the background, and judges it by the one rule it already applied — `ProviderFor` plus `AllowsData`, so a level model an allowed route claims still needs no key. No signature changed, so no caller did, and the answer feeds the two places that already follow the rule (`AnthropicKeyNeeded`'s prompt and the mount); a level that is off needs no guard, because `LevelSetting.validate` refuses `none: true` beside a model.

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
- **Done (PR "task, runstore: the A/B pair's slot (B1.1, D728)")**. Red first: `TestABSlotVocabulary`, `TestABSlotDefaultsToCoder` and `TestABReviewerSlotNeedsChampionRun` failed to build (`undefined: ABSlots`, `ab.Slot undefined`, `unknown field Slot in struct literal of type AB`), and `TestRecipesSkewRefusesSlotOnOldImage` with `undefined: slotSince`, `undefined: checkSlotImage`, `undefined: checkCloneSlotImage` — an A1.1 task carrying a `slot` key is refused by the strict decoder, which is the field's own red step. The slot is a fourth closed vocabulary beside the arm, the publish and the source (`task.ABSlot`, `ABSlots`, and `IsReviewer` as the one reader that tells the coder apart from the three review levels), and the empty spelling is the value rather than a hole (`AB.EffectiveSlot`), because every block A1.1 wrote means coder by saying nothing and re-reading it must not turn it into a pair nobody could score: `Slot` and `ChampionRun` are additive on `task.schema.json` and `result.schema.json` in this commit, `version` stays 1, and a coder pair writes neither key. `ChampionRun` is refused on everything but a challenger at a review slot — a champion is the run its own block names, and a coder pair's challenger runs the whole task itself — and on such a challenger it is judged by `ValidRunID`, the way `run_id` is, because B1.2 looks the patch up under it. **`slotSince` is the release whose runner replays and not the field's, by L1.2's own version rule**: it was written here as 0.24.0, the release this work was then slotted for, and amended to 0.26.0 on 2026-11-16, because 0.24.0 and 0.25.0 are tagged, both carry the field and neither release's runner has the replay (B1.2 is a task of its own, still open) — the number is the release whose runner acts on the slot, which is B1.2's replay, and a release that cut between this commit and that one would carry the field and not the replay — a runner that decodes the slot and does not replay it runs the whole coding loop on the challenger's model, which `ab.publish: none` keeps harmless and which is still the wrong run and a wrong figure at the pair's cost. The gate is declared here (`slotAsk`, `checkSlotImage`, `checkCloneSlotImage`) and wired by B1.4, which is the launch that writes a slot once the champion's patch exists (the shared-files table's `internal/cli/run.go` writers are L1.2 and L3.2 alone); what it judges is the key being present and not its value, and a launch asking nothing is never gated. Both `docs/release.md`'s Before-you-tag list (`slotSince`'s bullet, held by `TestSlotSinceHasAReleaseBullet`) and `docs/rigor-modes/reconciliation.md`'s image-skew census (35 → 36, in its own commit as that folder's rule requires) move with the constant, and `docs/model-ab-testing/implementation-plan.md` carries the amendment: D555 holds for a coder pair and is amended for reviewer slots by D728, A1.1's interfaces name the two keys, and A3.3 depends on B1.1 and B1.2 for a pair that names a slot.

#### B1.2 The reviewer replay (MIT mechanism)

- **Files:** `internal/runner/abreplay.go` (new), `internal/runner/runner.go`, `internal/runner/abreplay_test.go` (new).
- **Interfaces:** a challenger with `slot: lN` checks out the champion's base, applies the champion's patch (`runs/<slug>/<run-id>/ab/patch.diff`), runs one level-N review with the challenger model and publishes nothing (`ab.publish: none`). Its findings are recorded for the judge.
  - The patch is untrusted: any launcher, and the champion's own job agent, can write that object (`docs/bucket-iam/spec.md` §10 L3).
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
- **Owner ruling (2026-11-16, O12): the rule belongs to the slot, and the ask belongs to the workflow's key.** The ruling came with this pull request's follow-up brief of 2026-11-16 (the run's own words for it, and where they came from, are in docs/review-levels-ab-jobmax/reconciliation.md's O12). A reviewer-slot challenger is admitted against the model the level it replays runs — the launch's own level resolution, with the launch's mode, the owner's floor, the task's `review_levels` and the recipe — rather than against `cfg.Agent.ModelFor(RoleCoder)`: `Admit` keeps its coder baseline and the model a pair varies is the one argument that differs (`admitAgainst`), so a challenger that is the level's own model is refused as `champion-model` while a challenger that is the coder's is admitted, and a level the run does not run is refused rather than sampled. The ask is `fugaro.yaml`'s `workflows.<name>.ab.reviewer_arm` (a review level; `coder` is refused by name), per workflow and never a `fugaro run` flag: `ReviewerArm` reads that key rather than taking a level as an argument, and `internal/cli`'s `checkWorkflowABImage` reads the same key for the job image's own gate (`workflowABSince`, a constant of its own: the ask is a key of the file and the slot a field of the task, and 0.25.0 is tagged with the task's `ab.slot` and no `config.WorkflowAB`, so one number for the two would admit an image that kills at bootstrap on the file's key), so the ask is gated before the champion's job is paid for and this launch cannot be asked for a level its own file does not ask for. **This reverses the 2026-11-15 ruling, which kept the coder rule** (recorded in docs/review-levels-ab-jobmax/reconciliation.md's O12 and D728's cell, where the supersession is recorded): with the coder's model as the baseline, the arm refused the very challenger that varies the review and admitted one that repeats the model the champion's own review ran.
- **Not in this task: the launch entry point.** The helper answers the arm; the call into it, once a champion's patch exists, is `fugaro run`'s own launch (`freshLaunchGates`, `createTask`, `launchRun`) and no seam an `ee/` package can reach exports those, so nothing in the tree calls `ReviewerArm` yet. That entry point is a separate decision and is not built here.
- **Tests:** `TestABReviewerArmLaunchesAfterChampionPatch`, `TestABReviewerArmUsesChampionStartSHA`, `TestABReviewerArmAsksThroughTheWorkflowsOwnKey`, `TestABReviewerArmJudgesTheSlotItVaries`.
- **Command:** `GOFLAGS=-p=2 go test -tags ee ./ee/ab/... -count=1 && GOFLAGS=-p=2 go test ./internal/eeboundary/ -count=1`.
- **Depends on:** B1.1, A3.3.
- **Red step:** a reviewer-slot pair launches its challenger before the champion's patch exists.
- **Commit:** `ab: launch a reviewer arm once the champion's patch exists`.

#### B1.3 Per-slot scoring and `fugaro ab` grouping (`ee/`)

- **Files:** `ee/ab/` (the scoring and report files A4.1–A4.3 create).
- **Interfaces:** a reviewer pair's signals are misses (findings the judge matched in the champion's same-level review that the challenger lacked), false alarms (judge-marked) and cost. `fugaro ab` groups results by slot × model × effort. There is no per-person dimension (D560).
- **Tests:** `TestABScoreReviewerSlot`, `TestABReportGroupsBySlot`.
- **Command:** `GOFLAGS=-p=2 go test -tags ee ./ee/ab/... -count=1 && GOFLAGS=-p=2 go test ./internal/eeboundary/ -count=1`. Every new `ee/` file carries the business-licence notice that `ee/README.md` prescribes.
- **First commit:** **U4 is not measured, and this task's first commit records that instead of a figure (the owner's ruling of 2026-11-18).** U4 — whether the blind judge can match two finding sets reliably enough for a per-reviewer miss rate — is read through the A5 judge's own check, Check 211, which **has not been run**: it is an **OWNER-RUN** check on the sandbox installation (`docs/gcp-live-checklist.md`, "Check 211 … **NOT RUN**"), and no agent runs any step of it, so nothing here can record a measured figure. Until a record of that check exists, the miss rate is treated as unreliable rather than as a figure: `fugaro ab` prints a reviewer slot's miss rate as `unreliable (U4 not measured)` and never as a bare number, in one named seam (`ee/report`'s `ABMissRateUnreliable`, with `TestABReviewerMissRateIsNotAFigure` pinning the output) that a recorded Check 211 changes in one line. Misses per pair, false alarms and cost are read and reported as the interfaces line says.
- **Depends on:** B1.2, B1.4, A4.3, A5.4.
- **Red step:** a reviewer pair is scored with coder signals.
- **Commit:** `ab: score and report the review slots`.
- **Done (PR "ab: score and report the review slots (B1.3, D728)")**. Red first: the tree failed to build (`undefined: ScoreReviewer`), and `TestABScoreReviewerSlot`'s first subtest is the red step made visible rather than named — one fixture, read twice, where the coder reading answers `challenger` (the champion's arm carries no head and no verified test of its own) and the reviewer reading answers `tie` (both rounds answered, neither arm halted), so the pair that was scored with coder signals is now scored on the two rounds at its level: the reviewer each ran on, its effort, the finding count and the word it answered, read from the record's round at that level (`ReviewerRun`, `slotRound`, matched through `reviewRank` because a round carries a tier and never the slot it was launched for, so a level that ran twice is read at the round that answered last). The two named tests are `TestABScoreReviewerSlot` (with `TestABReviewerSlotIsAPairInput` beside them) and `TestABReportGroupsBySlot`, plus `TestABReviewerSlotCostIsTheLevels`. **What a reviewer pair is ranked on is deliberately not its misses and false alarms**: those are a judge's reading of two finding sets (F18 has a finding carry no identity), so the ranking is D567's halt and then whether each round answered — a round that answered nothing has no finding set in the pair — and the judge's two counts sit beside the ranking, are summed per row, and reach the wire as optional `misses` and `false_alarms` keys on the verdicts object (`judgeVerdict`), pointer fields because absent is not zero. **Nothing writes those keys yet**: matching two finding sets is a judgement about free text, no judge run built here makes it, so `ee/report` prints `-` for both cells until one does — the interface the plan's Interfaces line names is read and reported, and the figure it would carry does not exist yet, which the PR body says out loud. The miss rate is the seam the first commit describes: `ee/report`'s `ABMissRateUnreliable` is the one constant a recorded Check 211 replaces, read by the MISS RATE cell, by the note under the table and by the `--json` document's warning, and `TestABReviewerMissRateIsNotAFigure` pins both the constant's wording and that a coder row's cell is `-` (a coder pair varies no level and has no rate at all). A row is now keyed by slot × model × effort: for a reviewer pair the two models are the level's two reviewers and the two efforts theirs, the coder both arms ran being the fixed part of the experiment (B1.2), and R2 gained the slot as a fourth input — two arms that vary different slots are two experiments — with the reviewer pair's one extra check that the challenger names the champion's run whose patch it read (`ab.champion_run`, B1.4). The COST cell of such a row is the level's own stage (`StageTiming.ModelUSD` for `review_first`, `review`, `adversarial`), never the runs' whole model dollars, which is the coder's work. The table grew SLOT, EFFORT, MISSES, FALSE ALARMS and MISS RATE, the document grew `slot`, `champion_effort`, `challenger_effort`, `misses` and `false_alarms` (additive, no format version), and the `fugaro ab` help moved with them — which regenerates `website/content/cli-reference.md` in the same commit (`go run ./ee/clidoc`) — as did the living how-to's column list (`docs/how-to/model-ab-testing.md`).

### Slice J: job max

#### J1.1 Job max in the `parallelism` skill (D729)

- **Files:** `plugin/skills/parallelism/SKILL.md`, `plugin/skills/working/SKILL.md` (a one-line pointer), `plugin/parallelism_skill_test.go`.
- **Content:** job max is "the most completed, reviewed runs in parallel, not tokens spent; only real dependencies order runs". Planning advice: contracts first, stubs and fakes so neither side waits, the graph kept wide and shallow. The skill's "one yes per batch" rule stays the default. The `/jobmax` exception applies only where the owner has turned on the project's opt-in (D730 as ruled; J1.3).
- **Tests:** `TestParallelismSkillDefinesJobMax` (the phrase and the three techniques), plus the lint.
- **Command:** `GOFLAGS=-p=2 go test ./plugin -count=1`.
- **Depends on:** #1367 (`working/SKILL.md`).
- **Red step:** the new test fails on the current skill.
- **Commit:** `plugin: job max in the parallelism skill`.
- **Done (PR #1413, `da056ce7`).** Landed 2026-10-30, before this line was written, with the three files the **Files** line names and nothing else; its commit subject is the **Commit** line's own with the pull request's number appended. The task was picked up again on 2026-11-12, and nothing was re-implemented: the definition (`plugin/skills/parallelism/SKILL.md:33`), the three techniques (line 35), the one-yes-per-batch default with the `/jobmax` exception (line 122) and the `working` pointer (line 16) are the text #1413 wrote, both gates are green, and the run's change is the pin `TestParallelismSkillDefinesJobMax` was missing — the stub technique's own clause, "so neither side waits", which the skill's bold markers had split off from the name the test matched, so a skill keeping the name and dropping the clause passed. [Evidence, clause by clause](j1.1-already-landed.md).

#### J1.2 The `/jobmax` skill (D729, D730 as ruled)

**Closed as superseded 2026-11-16 (the owner's ruling): the orchestrator (`fugaro:orchestrator`) replaces `/jobmax`, so no `jobmax` skill is written.** Nothing below is built from this task; its text is kept as the record.

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

**Closed as superseded 2026-11-16 (the owner's ruling): the orchestrator replaces `/jobmax`, so no `jobmax.auto_launch` is built and D730's opt-in has nothing to switch.** Nothing below is built from this task; its text is kept as the record.

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

- **Files:** `docs/desktop-console/spec.md` (D5 marked superseded by D731), `docs/mobile/telemetry-plan.md` (D255 superseded; Analytics dropped; the §7.4 scan refuses `setAnalyticsCollectionEnabled(` and admits exactly one `setCrashlyticsCollectionEnabled(` call site), `docs/mobile/implementation-plan.md` (P7.2/P7.3 re-scoped), `internal/cli/docs_mobile_telemetry_test.go` (its pinned facts move with the text), `internal/cli/docs_no_crash_reporter_test.go` (new: G8, `TestGoModHasNoCrashReporter`, which needs no switch and so is not blocked on O9).
- **Tests and command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'Docs|GoModHasNoCrashReporter' -count=1 && (cd website && npm run check)`. `website/test/console.mjs` reads the D5 row.
- **Depends on:** #1332 (the same two mobile documents).
- **Red step:** the mobile telemetry docs test fails on the old "always on" pin until both move together.
- **Commit:** `docs: crash reports are opt-out; the app drops Analytics (D731)`.

#### T1.1 The `settings` node and the switch `telemetry.crash_reports` (D732, D734, O9 as ruled)

**Closed as superseded 2026-11-16 (the owner's ruling): with J1.3 gone the `settings` node has no second tenant, and the owner-written switches are re-planned against the config-ownership registry (docs/config-ownership), which is where an owner-only value now lives.** Nothing below is built from this task; its text is kept as the record.

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

**Closed as superseded 2026-11-16 (the owner's ruling): it printed T1.1's node, which is not built; the same reading is re-planned with the registry.** Nothing below is built from this task; its text is kept as the record.

- **Files:** `internal/cli/project_cmd.go`, `internal/cli/project_enroll_test.go`, `console/docs/json-contract.md`.
- **Interfaces:** the document gains `settings: { crash_reports: true|false|null }`. `null` means the node could not be read (offline, signed out, no database), and the console then sends nothing (D734).
- **Tests:** `TestProjectShowSettings`, `TestProjectShowSettingsUnknownIsNull`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run 'ProjectShow|Docs' -count=1`.
- **Depends on:** T1.1, and V1.1 (the same contract).
- **Red step:** the field is absent.
- **Commit:** `project show: the installation's settings`.

#### T1.3 The console may run `fugaro project show --json` (O9 as ruled)

**Closed as superseded 2026-11-16 (the owner's ruling): it allow-listed T1.2's argv, which is not built; the allow-list row is re-planned with the registry.** Nothing below is built from this task; its text is kept as the record.

- **Files:** `console/src/main/ipc.ts` (`CONSOLE_OPERATIONS` gains the exact argv), `console/src/main/spawn.ts` if its allow-list lists argvs, their pinning tests in `console/test/unit/`, `docs/desktop-console/spec.md` (the §5.1 table).
- **Interfaces:** a new allow-listed argv, `project show --json`, which `.claude/rules/console.md` marks security-sensitive: an independent review, its pinning tests and the §5.1 row.
- **Tests:** the IPC and spawn pinning tests with the new row, and `internal/cli/docs_desktop_console_test.go`.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 ipc spawn`, then `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Depends on:** T1.2, T0.1 (the same `desktop-console.md`).
- **Red step:** the pinning test refuses the unlisted argv.
- **Reviewer:** opus + mandatory independent review.
- **Commit:** `console: project show --json joins the allow-list`.

#### T2.1 The console reporter (D733, D734; DRAFT until the processor is named)

- **Files:** `console/package.json`, `console/package-lock.json` (`@sentry/electron`, exact), `console/test/unit/pinning.test.ts` (its runtime-dependency allow-list asserts `dependencies` is empty today), `console/src/main/crash.ts` (new), `console/src/main/index.ts`, the `settings.crash_reports` field decoded inside `console/src/main/crash.ts` from T1.3's `project show --json` operation (no change to `console/src/shared/schema.ts`), `console/test/unit/crash.test.ts` (new), `console/THIRD_PARTY_NOTICES.md`, `docs/licensing/dependency-audit.md`, `docs/desktop-console/spec.md` (a new section: what is sent), `website/src/pages/privacy.astro` (the console paragraph), `website/test/privacy.mjs` (it scans `console/src/` for reporting hosts and pins "carries none"), `docs/releases/console/` (the release's notes).
- **Interfaces:**
  - Initialises only after `project show --json` returns `settings.crash_reports: true` for the chosen installation, and closes the client when the switch reads false or null.
  - `beforeSend` keeps the exception type and frames (function, app-relative file, line). It drops the message, breadcrumbs, contexts, user, request, `server_name`, tags, extra and local variables.
  - Off: native crash minidumps (process memory), session and release-health envelopes, and client reports. Only error events are sent.
  - Main process only (`@sentry/electron/main`), with no renderer preload or IPC injected, so the IPC allow-list and the CSP are unchanged. Renderer errors are not reported in v1.
  - The DSN comes from a build-time environment variable. A build without it sends nothing. **SUPERSEDED (owner's ruling, 2026-10-31):** a build-time DSN was cancelled, so this bullet and the two `after deploy` lines that lean on it (the build secret and the release note that names it) no longer describe what is built — the DSN is one embedded public constant in `console/src/main/crash-dsn.ts`, it is the same in every build, and the per-user switch is the gate that decides whether anything is sent.
- **Tests:** `crash: nothing before the switch is read`, `crash: nothing with the switch off or null`, `crash: the scrubber drops message, home path, hostname and token-shaped placeholder`, `crash: no minidump, no session, no client report`, `crash: no preload is registered`, `pinning: @sentry/electron is exact and the only runtime dependency`, and the website's privacy test as amended.
- **Command:** `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 crash pinning`, then `cd website && npm run check`, then `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Depends on:** T0.1 (the same `desktop-console.md`), T3.1 (the same privacy page and test), the processor named (O4). **T1.3 is dropped (2026-11-21): it is closed, and this task's decode of its argv is superseded by the owner's ruling of 2026-10-31 — see the note below.**
- **Red step:** `crash: nothing before the switch is read` fails against a stub that initialises at startup.
- **Reviewer:** opus + mandatory independent review.
- **OWNER-RUN part:** the owner sets the DSN as the console build's secret, never in the repository. The release's `### For operators` names the variable.
- **Commit:** `console: crash reports, opt-out, stack frames only`.
- **Re-plan note, 2026-11-21 (observed, not approved).** T1.3 is closed, so the **Files** line's "decoded inside `console/src/main/crash.ts` from T1.3's `project show --json` operation" no longer names a task that exists. What shipped — the owner's ruling of 2026-10-31, recorded in this task's own **Interfaces** and in `x1-blocked.md` — is the console's per-user `<userData>/crash.json`, off when it is absent, which `console/src/main/crashstore.ts` reads and `crash.ts` gates on. And **the task is built in substance, which its own text does not say**: `@sentry/electron` is a pinned dependency of the console, `console/src/main/crash.ts`, `crash-electron.ts`, `crash-dsn.ts` and `crashstore.ts` are in the tree and wired at startup (`console/src/main/index.ts` imports all three of the first two and the store), the `crash.json` rules are written into `docs/desktop-console/spec.md` (rule 10, the file list the app writes), the dependency is in `docs/licensing/dependency-audit.md` and `console/THIRD_PARTY_NOTICES.md`, the website's privacy page carries the console paragraph, and the switch is the per-user local file the owner ruled for on 2026-10-31. This re-plan drops the stale edge so the graph carries only real ones, and does **not** re-word the task: T2.1 is not one of the three this re-plan was asked for, it is still a draft. **The owner should fold two things into this review**: T2.1's re-wording (the same shape T3.1's is — the switch's carrier named once, the argv that does not exist not named at all), and a **Done** note for a task whose code has landed, so the plan and the tree agree.

#### T2.2 The console's public statements (D731)

- **Files:** `website/content/downloads.json` (the `no-telemetry` property rewritten, still containing the words "crash reporting" and "update check" that `website/test/console.mjs` requires: "No telemetry and no analytics; crash reporting, stack traces only, is on by default and can be turned off in the project config; an update check that carries no identifier"), `docs/desktop-console/spec.md` (the §7.4 sentence that test pairs with the property), `website/test/console.mjs`, `README.md` (the console sentence).
- **Tests and command:** `cd website && npm run check`, then `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Depends on:** T2.1 (the statement ships with the code).
- **Red step:** `website/test/console.mjs` fails until the property and the test move together.
- **Commit:** `website, README: the console's crash-report statement`.

#### T3.1 The app: opt-out Crashlytics, no Analytics (D731, D734)

**Re-planned 2026-11-21 (the owner's instruction) and NOT APPROVED: it waits on the owner's review of this re-plan before anything launches from it.** D731 and D734 stand unchanged; what moved is the switch's carrier. T1.1's `settings` node is closed, so the switch is a member of a carrier that exists and that a phone already reads.

- **Carrier — D738, a reversal of O9's amendment, put to the owner.** D734's own answer of 2026-10-30 says **the mobile client reads the switch from the pairing-delivered configuration** (the console reads CLI output) — but the owner's ruling on **O9** the same day amended that sentence ("the switches go in a neutral, owner-written `settings` node … the apps read the node", reconciliation.md §4), and that node is T1.1–T1.3, closed on 2026-11-16 and built by nobody. So D738 **moves the switch back** to the pairing-delivered configuration D734 named first: the carrier is the pairing session the app already has, not a node nobody builds, a new argv or a new file. **This is the review's first question** — the owner is being asked to take the switch off the node O9's ruling named, not to confirm a reading that was already settled. What D738 records is the switch's name and home: the switch is the published project policy's **top-level key `mobile_crash_reports`**, an owner-written boolean, default `true`, **beside** the `mobile` block and not a member of it, with `mobile.enabled`, `mobile.sync`, `mobile.accepted_providers`, `mobile.max_auth_age`, `mobile.strict_new_device`, `mobile.costly_modes` and `mobile.biometric_on_costly` staying the block's own closed set. **That home is a top-level key and not an eighth member, and the task's review required it (corrected 2026-11-23):** `decodeMobile` decodes a block strictly — `checkMemberNames` with `DisallowUnknownFields` refuses a member it does not know and `Decode` returns that error for the **whole policy** — so an eighth member would make every run in flight on the previous image and every previous-release CLI reject the project policy after one `update`, breaking the HARD RULE that `update` is safe with runs in flight (`docs/design/update-in-flight.md`). A top-level key is the shape the P8 rule tolerates into `UnknownKeys`, which is why `orchestrator_enabled` sits beside the `orchestrator` block rather than inside it. It is written on the same node: the **published policy node** (`internal/projectpolicy.Policy.MobileCrashReports`, `internal/projectpolicy/policy.go`), which `internal/cli/project_cmd.go` names as the only place these keys live, published the way they are published by `fugaro project policy publish` and seeded by the same default-writing publish (`projectpolicy.MobileCrashReportsForPublish`, which that command calls). **It is not a project-layer key and there is no policy schema to move with it** (corrected 2026-11-21: the first text of this re-plan said the project layer, `schemas/project-layer.schema.json` and `docs/project-layer.md`): that schema has no `mobile` property, that document never mentions the block, and `schemas/` carries no policy schema at all. What pins the block's spelling is its own closed set in code — `mobileMembers`, read off the struct's json tags — and the new key is pinned by `MobileCrashReportsKey` with the top-level read in `policy.go`, `internal/projectpolicy/mobile_test.go` beside them, so the key is added in one place and the decoder and that test move with it; the members' list in the block's own P6 text (mobile-identity-pairing's plan) is corrected in the same PR so the two cannot disagree. The phone reaches it as `crash_reports` in the pairing session's response (`internal/mobilesession/contract.go`'s `SessionPolicy`), **and the member's producer is named rather than implied (added 2026-11-23, on the review's finding that nothing filled it):** it is filled from C1's `Policy.CrashReports`, the method that sits beside `Allow` on the interface for exactly this, so a `Policy` implementation that does not answer the switch does not compile, and P3's `internal/credsvc/mobile_policy.go` answers it from the published policy through `projectpolicy.MobileCrashReportsOn` (off for a key the node does not carry) — with P3's and P4's acceptance lines in mobile-identity-pairing's plan naming the fill, so the record and the code cannot drift, and that closed list is the contract: `shared/mobile-session/port.js`'s **`SESSION_POLICY_FIELDS`** gains it, which is exactly the list that keeps the policy file and the session response from drifting. `config/policy/**` is not in the paired device's read set, so the session is the only path, and it is a one-way one: a phone that edited the value would hide a button and change nothing (D2791).
  - **Why not the registry's generic `fugaro settings` leaf.** The registry (`docs/config-ownership/`) rules that a scalar leaf with no invariant goes through generic `fugaro settings`, and that a value with a publish step or a reader outside the CLI takes a typed carrier with one writer. An app-facing switch is the second: a local-config leaf lives on the owner's machine, where no phone can read it, and reaching the phone at all means publishing, which the typed block already does — with one writer, a default, a closed member set and a schema.
  - **The alternative, recorded so the owner can take it at review:** a **per-device switch in the app's own prefs**, mirroring the console's shipped per-user `<userData>/crash.json` (the owner's ruling of 2026-10-31). It needs no policy member and no session field, and it makes the two clients symmetric — but **it contradicts D734**, which answers that the mobile client reads the pairing configuration, and D731 states the app's switch as project configuration. **It also reverses O9's amendment, and O9's ruling had itself weighed the per-person local file and set it aside** as not meeting "a project-level flag" (reconciliation.md §4). So the alternative is a reversal the owner can make at review and not something the re-plan picks silently; the recommendation stands as written.
- **Files:** `mobile/package.json` and `mobile/package-lock.json` (drop `@react-native-firebase/analytics`), `mobile/app.config.js`, `mobile/src/app-config.test.js` and `mobile/src/app-config.ids.test.js` (they pin the analytics plugin), `mobile/firebase.json` (`crashlytics_auto_collection_enabled: false`, Analytics keys removed), `mobile/src/telemetry/index.js` (new: the module's only entry point), `mobile/src/telemetry/crash.js` (new: the one `setCrashlyticsCollectionEnabled(` call, decided from the session's `crash_reports`), `mobile/src/telemetry/crash.test.js` (new), `mobile/src/telemetry/scan.test.js` (new: the §7.4 scan as amended in T0.1, since P7.2/P7.3 are unbuilt — **amended 2026-11-11: P7.2 is built and P7.3 is not**, P7.2 having landed ahead of T0.1's amendment (the sequencing is recorded in `docs/mobile/telemetry-plan.md`'s P7.2 section), so this scan lands beside P7.2's `notice.test.js`), `mobile/src/telemetry/notice.js` and `mobile/src/telemetry/notice.test.js` (**added 2026-11-11**: P7.2's statement names Analytics while the app ships it, and `notice.test.js` reads `mobile/package.json` and fails until the clause leaves with the package — so this task's removal empties the statement of it), **`internal/projectpolicy/mobile.go`, `internal/projectpolicy/policy.go` and `internal/projectpolicy/mobile_test.go`** (the top-level key `MobileCrashReports`, its default in `DefaultMobileCrashReports` and its fill in `MobileCrashReportsForPublish`, its spelling in `MobileCrashReportsKey`, and — **added 2026-11-21** — the fill that carries the key to a node that published since P6: the struct's tags and that test are the key's whole contract, since `schemas/` carries no policy schema and this task writes none), **`internal/cli/init_policy_publish.go`** and **`internal/cli/init_policy_migrate.go`** with their tests (**added 2026-11-21**: the `mobile_crash_reports` drift row that now fires for a node lacking the key, and the previous-version-converges test of `fugaro update`'s policy stage — the migration half of `CLAUDE.md`'s "`fugaro update` is the whole upgrade", which is what makes the key reach an installation that has already published; the `!carries(mobileKey)` step itself is unchanged and stays tested where it is), **`docs/mobile-identity-pairing/implementation-plan.md`** (P6's acceptance line, which lists the block's members: another spec's document, edited only where that list would otherwise disagree with the block), **`internal/mobilesession/contract.go`, `internal/mobilesession/stub.go` and its test** (the field in `SessionPolicy`, the `CrashReports` method on C1's `Policy` interface that fills it — so the fill is compiler-forced — and the stub's fail-closed off beside it), **`shared/mobile-session/port.js` and its test** (the closed `SESSION_POLICY_FIELDS` list and the value check), `website/src/pages/privacy.astro` (the app paragraph: Crashlytics, opt-out, no Analytics) and `website/test/privacy.mjs` (its `APP_TELEMETRY` list names the analytics package, so the page and the test move with the package in one task), `docs/releases/mobile/` (the release's notes).
- **Interfaces:** after pairing, the app reads `crash_reports` from the session's policy and follows a later change at its next session refresh. Before a session exists, or when the key is absent from the published policy or the policy cannot be read, collection stays off and unsent reports are deleted (U2, G7) — **absence reads off, not "the default on"** (corrected 2026-11-21: the first text of this re-plan cited `mobile.enabled` as the precedent, and that is the inverted shape). The two are not the same reading: `internal/projectpolicy/mobile.go`'s `MobileEnabled` answers off for a **nil block** (a policy published before the key existed; the pairing route's `mobile_disabled`), but `mobileOff`'s rule is that **inside a written block an absent member takes the member's default** — which for `enabled` and `sync` is **on**. A reader copied from that shape would send a report for a switch it did not find, which is the one thing D731's opt-out and G7 forbid. So `crash_reports`'s reader answers **off** for a key it does not find, the rule `MobileStrictNewDevice` and `MobileBiometricOnCostly` already follow, on top of `MobileEnabled`'s nil-block rule — and never `enabled`'s absent-member-in-a-written-block rule. The published default is still **on** for the key a publish **creates**: `MobileCrashReportsForPublish` writes the default when the node carries no key at all. **It does not reach a node that already carries the policy, and closing that gap is this task's work** (corrected 2026-11-21: the first text of this re-plan said the switch reaches installations through that publish, and for a node published since P6 it does not; corrected again 2026-11-23: the carrier is a top-level key, so the gap is closed by a key-level fill and drift row rather than a block fill). `MobileCrashReportsForPublish` keeps a value the node carries and fills the key with the publish's default (`DefaultMobileCrashReports`, `true`) when it is absent, and `policyConfigDrift` gained a `mobile_crash_reports` row — `add(projectpolicy.MobileCrashReportsKey, projectpolicy.MobileCrashReportsForPublish(p.MobileCrashReports), p.MobileCrashReports, false)`, `internal/cli/init_policy_publish.go` — so the row is **silent** for a node that already carries the key and fires for one that predates it — and `withDerivedMembers` in `internal/cli/init_policy_migrate.go` carries the key beside the `mobile` block into a node the migration rewrites, so a migrated node does not leave the very next run a difference to publish: an installation that has published since P6 gains `mobile_crash_reports` in the one diff `fugaro update`'s policy stage already puts in front of the operator, and the step is idempotent from the run after (`CLAUDE.md`: "`fugaro update` is the whole upgrade" — no second command, no `policy publish --delete`, no hand edit). The block's own members are untouched and keep the values the node carries, and the `!carries(mobileKey)` step, the block's member-wise `MobileForPublish` fill and the publisher's own gate are unchanged. **The test that rule requires is named with the task, not left to the implementer:** an installation at the previous version **converges in one `fugaro update`** — a published node whose `mobile` block predates the member ends the run carrying `crash_reports` at the published default `true`, and the update after that finds no drift. **The owner's written `false` still has no door, and that is flagged open as O13** (`reconciliation.md` §4): no command writes the key today — the publish keeps the value it finds and `project policy show` and the drift diff only read it — so a written `false` is a hand edit of the node until a writer exists, and the writer and the default-filling fill ship in the **same** release, or D731's opt-out is a documented hand edit and nothing more. The app reads one field of `SESSION_POLICY_FIELDS`; the port refuses an unknown field (`UNKNOWN_POLICY_FIELD`) and a value that is not a boolean (`BAD_POLICY_VALUE`) rather than guessing. **The key's own skew between the two releases, stated rather than left implicit (added 2026-11-21):** the field joins `SESSION_POLICY_FIELDS`, so any port value carrying it must carry the list that names it — but the two sides release independently (the app from its store, the installation through `fugaro update`), and the port refuses a policy that **omits** a listed member (`MISSING_MEMBER`, `shared/mobile-session/port.js`). So **a new app against an installation whose session response predates the key**: the app's own session assembly — M3's mapping of the response into the port value, which is where this rule has to be honoured, and which this plan therefore records as a requirement on another spec's task — fills the field with the fail-safe (`false`) when the installation's answer does not carry it, so the port never sees it missing, the session stands, and crash reports read **off** until the installation is updated and the switch published — the same fail-safe as an unreadable policy below, and the reading D738 records. The alternative — let `MISSING_MEMBER` reach the app and map it to an "update the installation" screen — is recorded and **refused**: a store release that required the member would refuse **every** session on every installation not yet updated, which is the whole app unusable for a switch that governs only crash reports. The opposite skew is harmless and needs nothing: the closed-set rule is checked on the port value the app assembles from the response's members it knows, so an older app meeting a newer installation's extra key never reads it and never refuses over it. **Consumes nothing from the orchestrator's state**: no switch here is an orchestrator fact, nothing reads `orchestrator/state`, and the running-jobs view's own orchestrator read (mobile-identity-pairing J2) is not this task's. **What it consumes from the registry is one rule**, not a row: an owner-only value that a client outside the CLI reads takes a typed publisher with one writer, which is the `mobile` block and not a generic leaf.
- **Tests:** `crash: off until the session carries the policy`, `crash: follows a later session's change`, `crash: a project that publishes it false sends nothing`, `crash: an unreadable policy sends nothing and deletes unsent reports`, `scan: exactly one setCrashlyticsCollectionEnabled call site, no analytics import`, `projectpolicy: mobile_crash_reports holds the switch, absent reads off`, `projectpolicy: an older reader tolerates the key it predates`, `mobilesession: the session policy carries crash_reports`, `mobilesession: an absent crash_reports answers off, not the member's default`, `port: crash_reports is a session policy field and a non-boolean is refused`, `crash: an installation whose policy predates the member keeps the session and sends nothing`, **`projectpolicy: MobileForPublish fills a member an existing block lacks and keeps the members it carries`**, **`policy: a node whose block predates the member drifts, and one the fill wrote does not`**, **`update: an installation at the previous version converges in one run and the next update finds no drift`**.
- **Command:** `cd mobile && npm test`, then `cd shared && npm test`, then `cd website && npm run check`, then `GOFLAGS=-p=2 go test ./internal/projectpolicy ./internal/mobilesession -count=1`, then `GOFLAGS=-p=2 go test ./internal/cli -run 'Policy|Docs' -count=1`.
- **Depends on:** T0.1 (the same `docs/mobile/telemetry-plan.md` and its scan), #1332 (the same two mobile documents), P6's published `mobile` block (merged) and the app's session **contract** — mobile-identity-pairing **C1** (#3237, the Go contract and the vectors) and **C2** (#3238, `shared/mobile-session/port.js`), both merged. **Corrected 2026-11-21: the first text of this re-plan wrote "C1/M3, merged" and only the contract half is.** **M3 — the app's session, `mobile/src/session/` — is not built** (nor is M2, pairing): the directory does not exist on `main` and no commit builds it, so what this task's app half is written against is the port and the port's own stub (`shared/mobile-session/stub.js`), and the live integration is a real session, which arrives with M3. That is a gate on the paired half of X1's Check 272, **not** on this task: the key's spelling, its default and the port's closed list are the contract, and `crash.js` reads the one field off whichever session value it is given. **No edge to T1.1, T1.2 or T1.3** (closed 2026-11-16), and none to the orchestrator: this task reads no orchestrator state.
- **Red step:** the scan finds the analytics package imported.
- **Reviewer:** opus + mandatory independent review (a phone-facing policy switch, and a package removal).
- **Commit:** `mobile: Crashlytics follows the project policy; Analytics removed`.

#### T3.2 OWNER-RUN: the store privacy forms (O6)

The owner updates the Play Data safety form (remove Analytics; Crash logs optional) and the App Store
privacy labels in the two consoles, then records the date in `docs/mobile/implementation-plan.md`.
No agent performs any step. **Depends on:** T3.1 shipped to a release.

#### T4 OWNER/COUNSEL: Terms §7.3 (O1, O2)

Counsel reviews the draft in reconciliation §4 O2 and the EU opt-out question. The owner names the
processor (O4). No agent edits the Terms.

### Slice G: the graph fits the width (D735)

#### G1.1 Minimum scale and horizontal-scroll fallback

- **Files:** `console/src/renderer/screens/graph.ts`, `console/src/renderer/theme/screens.css`, `console/test/unit/graph.test.ts`, `console/test/unit/layout-client.test.ts`, `docs/console-styling/spec.md`.
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

- **Files:** `console/src/renderer/screens/live.ts`, `console/src/renderer/theme/flight-deck.css`, `console/src/shared/schema.ts` (`LIVE_KEYS` gains `max_parallel`), `console/test/unit/live.test.ts`, `docs/console-styling/spec.md`.
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

**Re-planned 2026-11-21 (the owner's instruction) and NOT APPROVED: it waits on the owner's review of this re-plan before anything launches from it.** Its subject is unchanged — the iterations bar on the Run screen, one bar per level, rounds used against that level's cap. What moved is its dependencies: **T1.2 and J1.3 are dropped** (closed 2026-11-16), and they were there for shared files and a contract member, not for anything the bar draws.

- **Files:** `internal/runner/plan.go` and its test (the `plan` block written from `planOf`'s resolved steps — this task is that file's first and only writer in the plan), `internal/runstore/runstore.go`, `schemas/result.schema.json` and `internal/runstore/runstore_test.go` (the added `plan` field; its writer is the runner), `internal/cli/diagnose.go` and its test (the document the Run screen reads; the read gains the per-level figures), `console/docs/json-contract.md`, `console/src/shared/schema.ts` (`RECORD_KEYS`; `closed()` refuses unknown keys), `console/src/renderer/screens/run.ts`, `console/src/renderer/theme/screens.css`, `console/test/unit/run.test.ts`, `docs/console-styling/spec.md`.
- **Interfaces:** `diagnose --json` gains each level's rounds used and **its cap**. No console decoder reads `reviews` today, so the bar is the first reader.
  - **Rounds used** comes from the record the read already carries: `Diagnosis.Reviews` is `[]runstore.ReviewSummary` (round, tier, verdict, findings, head, model, effort), and a level's used count is its own highest `round` — L4.1's tier (`runstore.TierAdversarial`) is what tells L3's rounds from L2's.
  - **Where a level's cap actually comes from** (corrected 2026-11-21: the first text of this re-plan named a source that does not exist). No level's round count is written into the task: `config.LevelSetting` is `{model, effort, none, focus}` and `schemas/task.schema.json`'s `$defs/level` is `additionalProperties: false`, so L1.1's `review_levels` carries no `rounds` at all. The caps are `config.Agent.FirstLineRounds`, `ReviewRounds` and `AdversarialRounds` (`internal/config`), which `internal/runner/plan.go`'s `planOf` resolves per step in one order: this run's `overrides.review_rounds` in the task, else the recipe step's own `max_rounds`, else the agent key, else the rigor mode's own row (`rigor.DefaultRounds` and `rigor.AdversarialDefault`), each held inside `recipe.MaxFirstLineRounds` (3) and `recipe.MaxReviewRounds` (10). **Only the runner holds that resolution**, and nothing else can replay it: the record carries the reviews and the mode the run resolved (`RigorRecord`), plus the recipe's name, source and hash (`RecipeRecord`) — never the recipe's steps and never the merged agent block.
  - **So the cap is carried in the record, by the runner.** V1.3 adds a `plan` block beside `rigor` and `recipe`: one entry per plan step, its kind and the `Rounds` `planOf` resolved for it, written once at bootstrap. The record's own rule allows this — a field is **added**, nothing is retyped or renamed — and it is the only place the figure exists, so `diagnose` reads it from the record it already reads, the way it reads `RigorRecord`. **A cap it cannot resolve prints `null`, never a guess**: a record written before the block existed, a record from an image older than this task, or a run that never built a plan. The console renders `null` as a bar with no full mark, not as a full one.
  - The full marks are the level's **cap**, and the bar's own numbers are therefore the same figures `fugaro diagnose` prints; the Run screen adds no arithmetic of its own (every figure goes through `hidesView` and `formatReading`, the rule `docs/console-styling/spec.md` §2.10 states and V1.2's gauge and cap bars are already held to).
  - **Consumes nothing from the orchestrator's state and nothing from the registry.** A run's review rounds are the run record's own facts; the orchestrator's state says which work item a run belongs to, not how many rounds it spent, and no config leaf decides what the bar draws — the cap is the run's own resolved plan, recorded by the run itself. This task adds no registry row and reads none.
- **Tests:** `run: iterations bar per level`, `run: a level with no resolvable cap draws no full mark`, `diagnose: each level's rounds and cap`, `diagnose: a record with no plan block prints a null cap`, `runner: the record carries each step's resolved rounds`.
- **Command:** `GOFLAGS=-p=2 go test ./internal/runner -run 'TestPlanRounds' -count=1`, then `GOFLAGS=-p=2 go test ./internal/runstore ./schemas -count=1`, then `GOFLAGS=-p=2 go test ./internal/cli -run 'Diagnose|Docs' -count=1`, then `cd console && npm run typecheck && npm run test:unit -- --maxWorkers=2 --testTimeout=60000 run`.
- **Depends on:** L4.1 (the tier the bar reads), L4.2 (the levels a run actually runs), G1.1 and V1.2 (the same stylesheet, styling doc and `RECORD_KEYS`). **No edge to T1.2 or J1.3** (closed 2026-11-16): both were file-conflict edges against `console/docs/json-contract.md`, `console/src/shared/schema.ts` and `docs/console-styling/spec.md`, and with both gone this task is their only writer at its level.
- **Red step:** the bar is absent.
- **Commit:** `console: the run's review rounds against their caps`.

### X1 Checks 270–272, written into the register

**Re-planned 2026-11-21 (the owner's instruction) and NOT APPROVED: it waits on the owner's review of this re-plan before anything launches from it.** The task keeps its slot, its file and its three check numbers. Two of the three checks are re-worded against the console and the app as built; the third is unchanged. It is still **OWNER-RUN** and still writes nothing until its three dependencies are built.

- **Files:** `docs/gcp-live-checklist.md` (the one writer of that file at this task's level; no other task in this plan writes it now that X2 is closed).
- **The checks:**
  - **270 (unchanged):** a Critical run on the sandbox repository whose L3 rejects once. The PR body shows L2's head older than L3's, and `result.json` records tiers `first`, `senior` and `adversarial`. Its subject is L4.2, which is merged; the run is started with `fugaro run` on the sandbox repository, not through the orchestrator — **this plan's checks launch no orchestrator task**, and the orchestrator's launcher is measured by its own block (Checks 300–305).
  - **271 (re-worded 2026-11-21):** a console crash, forced with a debug build's test trigger, arrives at the processor with type and frames only. **With the console's own switch off — the per-user `<userData>/crash.json` absent, or the menu item turned off — none arrives.** The old wording said `crash_reports: false`, which was T1.2's `project show --json` field; both it and T1.1's node are closed, and what shipped is the owner's ruling of 2026-10-31 (per-user, on when the file says so, off when it is absent). [Evidence, command by command](x1-blocked.md).
  - **272 (re-worded 2026-11-21):** the same for the app on a development build — **before pairing (no session, so no policy), and after pairing with the project policy's `mobile_crash_reports` published as `false`**. Unsent reports are deleted in both states (U2). The old wording said "the switch off" without saying which switch; the switch is T3.1's as re-planned, and 271 and 272 now name their own carriers rather than sharing one phrase.
- All three are **OWNER-RUN, NOT RUN**.
- **Command:** `GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1`.
- **Depends on:** L4.2 (Check 270's subject, merged), T2.1 (Check 271's), T3.1 (Check 272's, as re-planned). **Check 272's paired half has one more gate, named 2026-11-21: the app's own pairing and session (mobile-identity-pairing M2 and M3, `mobile/src/session/`) are not built either** — "after pairing" is a state no build on `main` reaches — so that half waits on another spec's tasks as well as on T3.1. #620, which was open on 2026-10-30 and edits the same register, merges or rebases first — **the re-plan leaves that edge as it found it rather than assuming a merge it did not verify.**
- **Red step:** none. It is a register entry, and the Go docs tests check the register's format and numbers.
- **Commit:** `checklist: checks 270-272 for review levels and crash reports`.
- **Blocked (2026-11-15), and what the re-plan changes about it: T3.1 is not built.** Read as the tree stands on 2026-11-21, **two of the three checks' subjects are already merged** — L4.2 (Check 270) and the console's reporter and per-user switch (Check 271: `@sentry/electron` pinned, `console/src/main/crash*.ts`, the switch in `crashstore.ts`, wired at startup) — and only **Check 272's** subject, T3.1, is not. So no row and no section were written into `docs/gcp-live-checklist.md` and 270–272 stay unused, and **what blocks X1 now is one task of this plan, not a re-plan**: T3.1 — with one gate named 2026-11-21 beyond it, that another spec owns: 272's "after pairing" state needs the app's own pairing and session (mobile-identity-pairing M2 and M3), neither of them built, so scheduling X1 also means that spec's app tasks land first. The re-plan removes the reasons that were the re-plan's own — T3.1 reading a closed node, and 271 naming a closed task's field — by naming a carrier that exists for each. **Whether X1 writes the two checks it can write now and holds 272, or writes all three in one visit, is X1's own run to decide** (the register has one writer, and its rule is that a section is written when the run is ready); the plan does not split the task here.

### X2 Check 273: CLOSED 2026-11-21, with nothing written and nothing run

**Closed by this re-plan, on the owner's instruction of 2026-11-21. X2 disappears as a task.** Its text is kept below as the record.

- **What Check 273 would have measured:** `/jobmax` on a sandbox plan launches only tasks whose dependencies are merged, stops at a cap, and prints what it launched — J1.2's skill, which the owner's ruling of 2026-11-16 closed as superseded. No task in this plan builds `/jobmax`: J1.2 and J1.3 are closed, and no `jobmax.auto_launch` key is read anywhere.
- **Nothing measured it, and there is nothing to amend.** Read in the register on 2026-11-21, `docs/gcp-live-checklist.md` carries **no `## Check 273` section and no row for 270–273**: the only occurrence of the number is the console-coordinator-v2 paragraph that reads this plan's checks row to justify taking Check 290 for itself, and that paragraph is another work's record of how it picked its number (a dated record this re-plan does not rewrite). So the close is a record here, not an edit there: **X2's `Files` line (`docs/gcp-live-checklist.md`) has nothing to write**, and this re-plan does not add a register row for a check nobody will run.
- **What it closes: G9, the `/jobmax` guardrail** — its every pin is now closed (J1.2, J1.3 and Check 273). The behaviour G9 describes is the orchestrator's, merged and in code: `docs/stateless-orchestrator/implementation-plan.md` **O6**, "The dependency-driven launcher: intent-first claims, caps, running cap and breaker" (merged, `6145e445`, #1950), whose constraints are that work's **G4** (one approval covers one item's first attempt and at most one automatic retry of the same approved spec, and caps per cycle, hour, day, running and per minute are code) and **G5** (the intent is committed before the launch, and the per-run-id claim is the concurrency fence). Its pins are that work's tests — `TestSpawnNeverLeavesTheApprovedItem`, `TestCapsStopALoop`, `TestCapsSurviveARestart`, `TestOneAutomaticRetryUnderTheSameApproval`, `TestRecoveryReissuesUnderTheRecordedRunID` — and the live half is that work's own owner-run **Check 302** (O11's cutover: start the daemon, see one launch through it), which is not rerun here and is not this work's.
- **The decision it settles: D730** ("fully automatic `/jobmax`", taken against the recommendation on 2026-10-30) is answered by **non-implementation**. The automatic launcher is the orchestrator's, and it is automatic under a *person's* per-item approval rather than under a project opt-in: the owner's ruling of 2026-11-04 gives one approval the first attempt plus at most one retry of that same approved spec, and a second failure needs a new approval. What D730's opt-in would have gated — an unattended launch with nobody having approved the item — is not built, and nothing reads a `jobmax` key.
- **The check number:** **273 stays this work's and unused.** A number, once reserved, is never reused or renumbered (the register's own rule), so this work now holds **270–289** as: **270–272** written by X1 (not yet run), and **273–289** reserved and unused. 273 is unused because its subject was retired, not because it was reassigned.
- **Commit:** none — the close is this plan's own text, and no register row is written.

## The re-plan of 2026-11-21: T3.1, V1.3, X1, and the close of X2

**Written for the owner's review. Nothing here is approved, no task launches from it, and the plan's status stays
"waiting on a re-plan" until the owner answers it.** The re-plan was asked for in one sentence: write T3.1, V1.3 and
X1 against the orchestrator and the config-ownership registry, and close X2. What follows is the pass behind the
three task sections above.

**What the two carriers are, in the terms the tasks use.** The **orchestrator** (`docs/stateless-orchestrator/`) is
the thing that replaces `/jobmax`: it holds a project's work items in its own state (`orchestrator/<id>` in the
installation's database, siblings `state`, `lease` and `audit`), and its launcher (**O6**, merged) starts a run only
for an item a person approved. The **config-ownership registry** (`docs/config-ownership/`, `internal/settings/registry.go`)
is the rule for where a value lives: one writer per field; a scalar leaf with no invariant goes through generic
`fugaro settings`; a value with a publish step, a secret, a multi-part list or a reader outside the CLI takes a
**typed carrier** and a semantic command.

**What each re-planned task consumes from them, plainly:**

| Task | From the orchestrator's state | From the registry |
| --- | --- | --- |
| T3.1 | **nothing** — no switch here is an orchestrator fact, and nothing reads `orchestrator/state`; the phone's running-jobs view is mobile-identity-pairing J2's read, not this task's | **one rule**, no row: an owner-only value a client outside the CLI reads takes a typed publisher with one writer, so the switch joins the published project policy's `mobile` block rather than a `settings` node (which does not exist) or a generic leaf (which no phone can reach) |
| V1.3 | **nothing** — a run's review rounds are the run record's own facts; the orchestrator says which work item a run belongs to, not how many rounds it spent | **nothing** — the cap is the run's own resolved plan, written into its own record by the runner; this task adds no row and reads none |
| X1 | **nothing directly** — its three checks launch runs with `fugaro run`, and the orchestrator's launcher is measured by that work's own Checks 300–305 | **nothing** — a check writes prose into the register |
| X2 | **the behaviour it would have measured, which is O6's** | none |

**Where a task disappears or merges into another spec's task, named:**

- **J1.2 and J1.3** (both closed 2026-11-16) merge into **`docs/stateless-orchestrator/` O6 and O10**: O6 is the
  dependency-driven launcher, and O10 is the plugin skill and the shared session-start hook slot — the slot a
  `/jobmax` skill would have occupied. Neither this plan nor its tasks re-implements any of it.
- **T1.1, T1.2 and T1.3** (closed 2026-11-16) merge into **the carriers that already exist**: T3.1's switch onto the
  published project policy's `mobile` block (below), and the console's onto its own per-user `<userData>/crash.json`
  (T2.1, as shipped under the owner's ruling of 2026-10-31). **No `settings` node is built by anyone**: with J1.3
  gone it had one tenant left, and the registry's rule puts that tenant on a block that is already published.
- **X2 disappears** as a task: **Check 273 is closed with nothing written and nothing run** (the section above), and
  G9 is retired — its behaviour is **O6**'s, under that work's **G4** and **G5**.

**The contract-first pass, edge by edge.** Every edge the re-plan touches was asked one question: can a small
contract break it, so the two sides stop waiting for each other?

- **`T1.2, J1.3 → V1.3` — broken by removal, not by a contract.** Both edges existed only because three files have
  several writers (`console/docs/json-contract.md`, `console/src/shared/schema.ts`, `docs/console-styling/spec.md`),
  and neither task's *work* was V1.3's input. With both closed, V1.3 is the only writer of those files at its level,
  and it waits on L4.1 and L4.2 — which it genuinely consumes, because the tier and the rounds are what it draws.
- **`T1.1 → T3.1` — broken, and the replacement is a real contract.** The old edge said the app waits for the node
  it reads. The re-plan splits that work at the one place a stub is honest: **the switch's wire name and the closed
  session field list are the contract** (`mobile_crash_reports`, the policy's top-level key → `crash_reports` in
  `SESSION_POLICY_FIELDS`, both validated by `shared/mobile-session/port.js`'s own checks). So the Go half (the
  key, its default and the session field) and the app half (`mobile/src/telemetry/`) can be built against the
  port's existing refusal codes rather than against each other's code, and only the integration check — the app
  reading a real session policy — is one task's. **This is an intra-task decoupling, not a broken edge:** the re-plan
  keeps one task rather than inventing two, because both halves are small and one PR per policy member is the shape
  P6 used.
- **`L4.2 → X1`, `T2.1 → X1`, `T3.1 → X1` — kept.** No contract can break them: a live check reads a shipped build.
- **`L4.1, L4.2 → V1.3` — kept.** V1.3 draws the tier and the round L4.1 records, and reads levels L4.2 runs. A stub
  could not break this without the bar showing a figure the run did not produce.
- **`#620 → X1` — kept as found.** The in-flight table says that pull request was open on 2026-10-30 and edits the
  same register. The re-plan did not verify a merge, so the edge stays.

**The edges as drawn** (only these; the full list is under Parallel execution): `T0.1 → T3.1` and, externally, the
published `mobile` block (P6, merged) and the app's session contract (C1 and C2, merged; the app's own session M3 and its pairing M2 are not built, and gate only X1's paired half) `→ T3.1 → T3.2`; `T0.1, T3.1 → T2.1`
(O4's processor name is a gate, not a task); `L4.1, L4.2, G1.1, V1.2 → V1.3`; `L4.2, T2.1, T3.1 [#620] → X1`.

**The one new decision: D738, and it re-decides the switch's home.** Corrected 2026-11-21: the first text of this
re-plan called D738 "mostly a record", on D734's answer that the mobile client reads the switch from **the
pairing-delivered configuration**. That answer was **amended the same day by the owner's ruling on O9** — "the switches
go in a neutral, owner-written `settings` node beside the budget node … the apps read the node"
(reconciliation.md §4) — and the node that ruling named is T1.1–T1.3, closed on 2026-11-16 and built by nobody. So
D738 **moves the switch back** to the pairing-delivered configuration D734 named first, and that reversal of O9's
amendment is the owner's to make or refuse at review: **the carrier the owner last ruled was the node, and this
re-plan does not get to call the return to the session a formality.** What D738 fixes besides the home is the switch's
spelling: the switch is the published policy's **top-level key `mobile_crash_reports`**, delivered as `crash_reports` in
`SESSION_POLICY_FIELDS`, with absence reading **off** rather than as the block's default. The one alternative — a
**per-device switch in the app's own prefs**, mirroring the console's shipped per-user `<userData>/crash.json` — is
recorded as a **reversal of D734 and of O9's amendment both** (O9's ruling weighed the per-person local file and set it
aside as not meeting "a project-level flag"), not as an equally good reading, and the recommendation is the policy
key. **If the owner reverses it, that is the review's one change to this re-plan**, and it also
rewrites O2's draft Terms sentence (below). It is written up in [reconciliation.md](reconciliation.md), where the
first unused number of this work's reserved block (D738) is now taken for it.

**The plan carries no graph artifact, so there is none to re-plan.** `fugaro plan graph --plan
docs/review-levels-ab-jobmax/implementation-plan.md` refuses with "no graph artifact here; a plan's graph is written
beside it as `<plan>.graph.json`" — this plan never had one, so the edges, the levels table, the critical path and the
shared-files table above are the plan's graph of record, and this re-plan updated them in place rather than in an
artifact beside them. **A future task that wants the artifact would write one, not this re-plan**: it would be a new
`schema_version: 3` document with a node per task and the footprints the tasks predict, and inventing it here would
put a second, unchecked description of the graph next to the one the plan already keeps.

**What this re-plan does not decide.** T2.1's own re-wording (its decode of T1.3's argv, superseded) — noted in its
section, for the owner to fold into this review. **The door to a written `false` — raised as O13** (reconciliation.md
§4): T3.1 ships the migration that fills an absent member into a block an installation already published, but no
command writes a `mobile` member today, so the owner's opt-out on that member is a hand edit of the node until a writer
exists — O13 records the three doors (a semantic command; a config key the publish carries, which would reverse P6's
"the node is the only place they live"; or a documented hand edit) — and the writer the owner picks and T3.1's fill
ship in the same release. L4.4, L4.5b and L5.1's
own edges, which the re-plan leaves as they are. And every check number below 270, which is not this work's.

## Parallel execution

**Edges** (from the Depends-on lines; external gates in brackets; a level is one more than the highest level
among a task's internal dependencies):

- L0.1 → L1.1 → L4.3
- L1.1, L2.1 → L1.2
- L1.1 → L1.3
- L1.1, L1.3, L1.4, L2.2 → L3.1
- L3.1, L1.2, L5.2 → L3.2
- L3.1 → L4.5
- L3.1 → L4.5b
- L1.1, L2.1, L3.1, L4.1, L4.3, L4.5 [#1369] → L4.2
- L1.3, L2.1 → L4.4
- L4.2, L3.2, J1.1, T1.1 → L5.1
- L1.1, L4.1, L1.2 → B1.1 [before A3.3]
- B1.1 [A3.3] → B1.4
- B1.1, L4.2 [A5.2] → B1.2
- B1.2, B1.4 [A4.3, A5.4] → B1.3
- ~~L1.4 → T1.1 → T1.2 → T1.3~~ (all three closed 2026-11-16 as superseded; the `settings` node is built by nobody)
- ~~V1.1 → T1.2~~ (closed)
- ~~T1.1, T1.2 → J1.3~~ (closed; merges into docs/stateless-orchestrator O10)
- ~~J1.1, J1.3 → J1.2~~ (closed; the launcher merges into docs/stateless-orchestrator O6)
- [#1332] → T0.1
- T0.1 [#1332, the published `mobile` block (P6, merged), the app's session contract (C1, C2, merged)] → T3.1 → T3.2
- T0.1, T3.1 [O4] → T2.1 → T2.2
- [console redesign steps 5–7] → G1.1
- V1.1, G1.1 → V1.2
- L4.1, L4.2, G1.1, V1.2 → V1.3 (T1.2 and J1.3 dropped: closed 2026-11-16, file-conflict edges only)
- L4.2, T2.1, T3.1 [#620; 272's paired half also needs the app's own M2 (pairing) and M3 (the session), which another spec has not built] → X1
- ~~J1.2, X1 → X2~~ (X2 closed 2026-11-21: Check 273 was never measured and nothing is written for it)

**Levels:**

| Level | Tasks |
| --- | --- |
| 0 | L0.1, L1.4, L2.1, L2.2, L4.1, L5.2, J1.1, T0.1, G1.1, V1.1 |
| 1 | L1.1, V1.2, T3.1 |
| 2 | L1.2, L1.3, L4.3, T3.2, T2.1 |
| 3 | L3.1, L4.4, B1.1, T2.2 |
| 4 | L3.2, L4.5, L4.5b, B1.4 |
| 5 | L4.2 |
| 6 | L5.1, B1.2, V1.3, X1 |

*(Re-planned 2026-11-21. T1.1–T1.3 and J1.2–J1.3 are out of the table: closed 2026-11-16. X2 is out: closed. T3.1
falls from 2 to 1 and T3.2 from 3 to 2, because T3.1's only remaining internal edge is T0.1 and the two things it
needs from elsewhere — the published `mobile` block and the app's session contract (C1 and C2, merged; M2 and M3 unbuilt)
— are merged so far as the task's own build goes; T2.1 falls from 4 to 2 and
T2.2 from 5 to 3, because T2.1's T1.3 edge is gone (T1.3 is closed, and the console's switch shipped as the per-user
file). X1 stays at 6: L4.2 is level 5, and T2.1 and T3.1 are below it now. Level 7 is empty and is not listed.)*

**Critical path (inside this plan):** for the review levels, L0.1 → L1.1 → L1.3 → L3.1 → L4.5 → L4.2 → L5.1 / X1,
seven tasks. It starts at once; L4.2 also waits until no other run writes `internal/runner/runner.go`. The longest chain
overall is eight tasks, L0.1 → L1.1 → L1.3 → L3.1 → L4.5 → L4.2 → B1.2 → B1.3, and it also waits for the A/B plan's
A3.3 and A5.x. Slices G and V are off the critical path. The console's T2.1 waits on the processor's name (O4).
**Unchanged by the 2026-11-21 re-plan, and the re-plan shortens nothing on it**: T3.1, V1.3 and X1 were never on the
critical path, and X2's close removes its deepest level (7) rather than a task from the path.

**What must be sequential and why:**

- L1.1 after L0.1: the closed effort set.
- L1.3 after L1.1: the same `config.LevelSetting` type and closed effort set.
- L3.1 after L1.1, L1.3, L1.4 and L2.2: it consumes the settings and the defaults, and applies the floor.
- L3.2 after L5.2: this repository's own configuration must pass the check L3.2 ships.
- L4.5, L4.5b and L4.2 after L3.1: they consume the resolved levels, and L4.2 routes through L4.5.
- ~~T1.1 after L1.4, then T1.2, then J1.3 and T1.3~~ — closed 2026-11-16; nothing is sequential on that chain now.
- B1.1 after L1.1, L4.1 and L1.2: the same files.
- T2.1 after T3.1 (the same privacy page and test) and T0.1 (the same `desktop-console.md`): **not** after T1.3, which is closed.
- V1.3 after G1.1, V1.2, L4.1 and L4.2: the same stylesheet, styling doc and `RECORD_KEYS`, and the record's own tier and rounds. **Not** after T1.2 or J1.3, which are closed.
- V1.3 and B1.1 one at a time: both add a field to the record (`internal/runstore/runstore.go`, `schemas/result.schema.json`), and no dependency path joins them — V1.3's ancestors are L4.1, L4.2, G1.1 and slice V's, B1.1's are L1.1, L4.1 and L1.2 — so a run of either waits until the other is not in flight.
- T3.1 after T0.1 only, inside this plan: the published `mobile` block and the app's session contract (C1, C2) are merged, so the switch's carrier is not a wait. The app's own pairing and session (M2, M3) are another spec's unbuilt tasks, and they gate the live reading, not this task's build.

**Shared-files table** (every file with more than one writer; no level holds two writers of a file, and every
pair of writers is joined by a dependency path):

| File | Writers (level) |
| --- | --- |
| `internal/task/task.go`, `schemas/task.schema.json` | L1.1 (1), B1.1 (3) |
| `internal/runstore/runstore.go`, `schemas/result.schema.json` | L4.1 (0), B1.1 (3), V1.3 (6) — **V1.3 added 2026-11-21: it adds the `plan` block the runner writes, and B1.1 adds its own record field. L4.1 joins B1.1, but B1.1 and V1.3 are not joined by any dependency path, so those two must not be in flight at once** |
| `internal/cli/recipes_skew.go`, `internal/cli/recipes_skew_test.go` | L2.1 (0), L1.2 (2), B1.1 (3) |
| `internal/cli/run.go`, `internal/cli/run_review_test.go` | L1.2 (2), L3.2 (4) |
| `internal/runner/runner.go` | L4.2 (5), B1.2 (6) |
| `docs/recipes.md` | L2.1 (0), L5.1 (6) |
| `plugin/skills/working/SKILL.md` | J1.1 (0), L5.1 (6) |
| `docs/gcp-live-checklist.md` | X1 (6) — **one writer now: X2 is closed, and it wrote nothing** |
| `docs/desktop-console/spec.md` | T0.1 (0), T2.1 (2), T2.2 (3) |
| `docs/mobile/implementation-plan.md` | T0.1 (0), T3.2 (2) |
| `console/docs/json-contract.md` | V1.1 (0), V1.3 (6) |
| `console/src/shared/schema.ts` | V1.2 (1), V1.3 (6) |
| `docs/console-styling/spec.md` | G1.1 (0), V1.2 (1), V1.3 (6) |
| `console/src/renderer/theme/screens.css` | G1.1 (0), V1.3 (6) |
| `website/src/pages/privacy.astro`, `website/test/privacy.mjs` | T3.1 (1), T2.1 (2) |
| `internal/projectpolicy/mobile.go`, `internal/projectpolicy/mobile_test.go` | T3.1 (1), P6 (merged) — **the re-plan adds T3.1 as a writer of an already-merged file**, which the graph does not model |
| the CLI release's notes (`docs/releases/`) | L1.2 (2), L3.2 (4), L5.1 (6) |

Every other file in the plan has one writer. **Re-planned 2026-11-21:** the `localcfg`, `sharedcfg` and `project_cmd.go`
rows leave the table with T1.1–T1.3 and J1.3 (closed; L1.4 is their only remaining writer), `plugin/skills/parallelism/SKILL.md`
leaves it with J1.2 (J1.1's alone), and `console/docs/json-contract.md` and `console/src/shared/schema.ts` keep two
writers each with V1.3 as the later one.

## Check numbers this plan takes

| Check | What it measures | Who runs it |
| --- | --- | --- |
| 270 | A Critical run's L3 bounce skips L2, readiness follows the highest level, and the PR body shows each level's head | the owner, on the sandbox installation; NOT RUN |
| 271 | The console's crash report holds type and frames only, and none is sent with the console's own per-user switch off (the file absent, or the menu item off) | the owner; NOT RUN |
| 272 | The app sends none before pairing or with `mobile_crash_reports` published as false, and type and frames only otherwise | the owner, on a development build; NOT RUN |
| ~~273~~ | ~~`/jobmax` on a sandbox plan launches only tasks whose dependencies are merged, stops at a cap, and prints what it launched~~ — **CLOSED 2026-11-21: never measured, nothing written, and its subject is retired with J1.2. The behaviour is docs/stateless-orchestrator O6's, under that work's G4 and G5, and its live half is that work's Check 302** | the owner; NOT RUN, and never will be (X2 closed) |

Checks 274–289 stay reserved and unused, and **273 joins them**: it stays this work's and unused, because a number,
once reserved, is never reused or renumbered. **This work therefore holds 270–289: 270–272 are written by X1 (not yet
run) and 273–289 are reserved and unused.**

## After deploy

- **T2.1:** the console's release notes, `### For operators`, name the build-time DSN variable. The owner sets it as a build secret before the release build.
- **~~T1.1~~:** struck 2026-11-21 — no `settings` entry is added to the database rules by anyone, because no `settings` node is built. Nothing here waits on a rules change. **T3.1** instead publishes the top-level key `mobile_crash_reports`, which rides the policy publish `fugaro update` already runs — **with one migration this task ships, because as the tree stands the key does not reach every installation** (corrected 2026-11-21: the first text of this re-plan said `update` re-publishes it and a project that published before the key existed gets the default from `MobileForPublish`, which is true only of a node that carries **no** block). A node published since P6 carries no such key, and `policyConfigDrift` had no row for it, while the migration chain's `!carries(mobileKey)` step fires only for a node with no `mobile` key — so under the fail-safe below that installation's crash reports are silently off. So `MobileCrashReportsForPublish` fills the key a node lacks (keeping a value it carries), a `mobile_crash_reports` drift row fires, and `update`'s policy stage publishes the key once, idempotently after, **with a previous-version-converges test** — an installation whose policy predates the key ends one `fugaro update` carrying `crash_reports`, no second command and no hand edit (`CLAUDE.md`). The default is **on** for the key the fill writes (`DefaultMobileCrashReports`), and **a key the published policy does not carry reads as off, not as the default** — the fail-safe D734's G7 and U2 require. That is deliberately **not** `mobile.enabled`'s shape: there a nil block answers off, but an absent member inside a written block takes the member's default **on** (`internal/projectpolicy/mobile.go`'s `mobileOff`), so copying it would give a privacy switch the inverted fail-safe. D731's opt-out is the owner publishing `false` — **and no command writes the key today**, so that writer is flagged open (**O13**, reconciliation.md §4) and ships with the fill, or the opt-out is a hand edit of the node.
- **Before the 0.16.0 tag:** 0.15.0 shipped without L4.2 — the `adversarial` step parsed but no runner ran it, and `review_levels` decoded but nothing acted on it — so `adversarialSince` moved to 0.16.0 with L4.2, and `reviewLevelsSince` (L1.2's constant, in the tree since L1.2 landed) names 0.16.0. Both must name the release L4.2 actually ships in, so no image accepts a level it does not run.
- **L1.2:** job images must be refreshed (`fugaro image refresh`) before any launcher sets `--review-levels`. The release's `### For operators` says so.
