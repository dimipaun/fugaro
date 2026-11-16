# The model half's contract

**Status as of 2026-11-16: written by MC11 of [activation-plan.md](activation-plan.md), together with the stub it names in `internal/craft/model.go`; MC12–MC17 are not started. See [docs/status.md](../status.md) for the current table.** Spec owner: Dimi Paun.

A craft's model half was written, validated and shown in 0.24.0 and was not yet what a run ran (that release's `### Known limits`, the first limit). MC12–MC16 close it, and each of them needs the same three answers before it can be written: the order a stage's model is chosen in, the craft each stage's model belongs to, and where a resolved model has to be written for the whole run to agree. This document is those answers, and `internal/craft/model.go` is the part of them a package can state in code.

**A change here reaches five tasks.** A rung added, removed or reordered, the write's destination moved, or the map amended is a change to MC12, MC13, MC14, MC15 and MC16 (the plan's contract-change paragraph): each is reached by a follow-up run on its own pull request that names `internal/craft/model.go` by path and never pastes its text, and no other task writes that file.

## The rungs (D1660)

A stage's model is chosen by five rungs, most specific first:

| Rung | Key | Who answers it |
| --- | --- | --- |
| 1 | `task.grade` | the run, for one task |
| 2 | `agent.levels.l1/l2/l3.model` | the run, for the level the stage runs |
| 3 | `agent.crafts.<craft>.model` | `craft.ModelFor` — this package |
| 4 | `agent.models.<role>` | the run, for the role the stage's slot pins (`config.StageRole`) |
| 5 | `agent.model` | the run |

Each rung is a narrower statement by the same person about fewer stages, and a narrower statement wins: a craft is a standing choice about a set of stages, so it may not overrule the grade one task carries or the model one review level names (D1660).

**What a caller may rely on.** `craft.ModelRungs()` is that order, most specific first, and it is the contract's own list: a reader that has to name the rung a value came from reads it there instead of spelling the keys a second time (`TestTheRungOrderIsTheContracts`).

**What it may not.** `craft.ModelFor(crafts, stage)` answers **rung 3 alone**: the model the stage's craft resolved to, and the craft the stage belongs to. It never falls back to rung 4 or rung 5 — a craft no layer gives a model answers `""`, because a fallback answered here would hide that nothing was written — and it reads rungs 1 and 2 nowhere. `crafts` is the resolver's own answer (`craft.Resolve` over the caller's layers): the layers are the caller's — a run's `craftLayers`, the launcher's own — and a package that assembled its own would be a second copy of them. (The plan writes the signature as `ModelFor(stage string)`; the resolution is a parameter for that reason.)

## The map (D1663, F13)

| Craft | Stages |
| --- | --- |
| `coder` | `plan`, `implement`, `fix` |
| `review` | `review_first` |
| `senior-review` | `review`, `triage` |
| `security-review` | `adversarial` |

`""` is a stage outside this vocabulary — a stage that spends no model call (`bootstrap`, `render`, `finalize`, `writeback`, `provision`) and a name that is no stage at all — and it is answered rather than guessed at. The stages of `config.RunStages` are exactly the ones that answer a craft (`TestAnUnknownStageAnswersNoCraft`).

**The map exists twice today** — `stageCraft` in `internal/runner/routing.go` and the same function again in `internal/cli/init_craft_migrate.go`, written from F13's sentence and kept in step by hand. **The contract names the runner's copy as the run's**: it sits beside `stageLevel` and `stageRole`, and the walk that serves a stage, the stage's own checks and MC12's bootstrap write all read it there. MC21 converges the CLI migration's copy onto it rather than keeping two — that unification is MC12's and MC21's change and not MC11's, which touches neither file. `internal/craft` states the map as well, because a stage's craft is half of what `ModelFor` answers and a package cannot read the runner's unexported function; the two are held in step by the runner's own tests, which read both answers for every stage a run has.

## The write (D1661)

A resolved model reaches a run by being **written into the slot the run already reads**, in one bootstrap pass:

| Craft | Slot written |
| --- | --- |
| `coder` | `Agent.Models.Coder` |
| `review` | `Agent.Levels.L1.Model`, where the level wrote none |
| `senior-review` | `Agent.Models.Reviewer`, and `Agent.Levels.L2.Model` where the level wrote none |
| `security-review` | `Agent.Levels.L3.Model`, where the level wrote none |

**Where it runs is part of the contract:** in `resolvePolicy` (`internal/runner/policy.go`), **first among the three that rewrite a model** — before `applyGrade` and before `applyRouting`. A craft's model written after the routing switch would be a model the switch never judged, which is the reason `applyGrade`'s own comment gives for its place, and one written after the level pins would land on top of a model the routing had already resolved for that level rather than under it. "Where the level wrote none" is the same rule from the other side: rung 2 is above the craft's rung, so a level that named a model keeps it.

The senior-review craft's model lands on the reviewer slot because level 1's review does not use it: `reviewStages` gives level 1 the coder's slot and level 2 the reviewer's, and the security-review craft, which follows the senior review (`config.CraftParent`), resolves to the senior's model when it names none — so the write is a no-op for the craft that inherits and a real one for the craft that does not.

**Why a write, and not a fifth rung read where the model is answered.** Two reasons, and both are the contract's:

- **It is `applyGrade`'s own pattern** (`a.Models.Coder = grade.Model`), so the run keeps one way for a narrower statement to reach every consumer.
- **The consumers of a stage's model are more than `stageModel`:** `Agent.PinnedModels()` is the gateway's allow-list, `config.CheckAllowed` and `config.CheckPins` are what the launch and `fugaro validate` judge, `cost.go` prices the stage's row and the record's `StageTiming.Model` reports it. A rung read inside `stageModel` alone would leave the gateway refusing a model the run was told to run.

**What a caller may rely on.** One write, and every consumer names one model (`TestOneAnswerForEveryConsumer`, MC12). **What it may not.** The pass is the runner's, in `internal/runner/craftmodel.go` (MC12); nothing in `internal/craft` writes a configuration.

## The verdict (D1665)

A craft's model passes the four checks a run's model passes, at config time and in `fugaro validate`:

- `budget.allowed_models` bounds it;
- a route claims it (`config.RoutingClaudeFor`);
- a price row covers it (`internal/pricing`);
- the craft's own access admits the repository (F10, D1627).

A model that fails one is **refused before a run starts** — at launch, before the task is written, and by `fugaro validate` — and the refusal is reported at the path of the key in the document that wrote it (`craft.Layer.CraftKey`), so a reader is sent to the file they have. The verdict is never the gateway's: a run refused there has already been paid for, which is the billable failure the panel exists to prevent. MC15 owns the checks and MC16 the read (`craft ls`'s row carries the same verdict); neither is this package's, and this document names them so a reader knows where the verdict lives.
