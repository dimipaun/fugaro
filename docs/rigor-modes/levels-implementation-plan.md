# Rigor Levels: the overlay implementation plan

**What this builds:** [levels-spec.md](levels-spec.md) as amended by [levels-reconciliation.md](levels-reconciliation.md) (decisions **D261–D271**, answered by the owner on 2026-10-22), **layered on** the approved rigor-modes plan ([implementation-plan.md](implementation-plan.md), D1–D32). **Nothing is implemented. No task here is approved: each needs the owner's explicit go.** An answer settles a question; it does not approve a task.

## Status and what shaped the plan

| Owner answer | What it does to the plan |
| --- | --- |
| D261 both: persona prompt + thresholds | LV2: personas are prompt text; `runner.Decide` and the score thresholds (Slices 2–3) are untouched |
| D262 rename to `--rigor` / `level` | LV1.1 first: nothing is released, only the branch holds the word |
| D263 floors stay | LV3.2: "task is strongest" among defaults only; the resolver states the one exception once |
| D264 areas = named semantic parts of the project (repos or repos-with-paths) | LV3.1: one area concept at the project layer; a repo's own YAML keeps its path areas |
| D265 artefacts = the backlog + `open-risk` kind | LV4.1: a ninth required front-matter field `kind`; no second format |
| D266 promote task, a later slice | LV7.1, last |
| D267 Hotfix revert plan in the PR body; verification in the morning note | LV2.1 (the Hotfix text), LV4.2 (the sections) |
| D268 Pro stops at a draft PR with OPEN QUESTIONS | LV5.2: one new end reason |
| D269 recipes may branch on the level | LV6.1; D16's cost ceiling unchanged |
| D270 persona texts embedded, not overridable | LV2.1 |
| D271 overlay plan beside the existing one | this file; it names which existing tasks it changes |

**Where the work is built.** On the branch `rigor-modes` (Slices 0–1 and the backlog's T4.3/T4.4 are there, and the branch is 131 commits behind `main`), after the branch is synced with `main` by its own task. An overlay task that needs a Slice not yet built says so in its **Depends on** (Slice numbers are the 2026-10-20 plan's).

## Conventions every task follows

`CLAUDE.md` and the `.claude/rules/*` files the touched areas load; tests first with a named **Red step**; targeted tests only (never `go test ./...`); one pull request per task; a security-sensitive task gets an independent opus review; no secret-shaped literal in a test; no agent performs a live cloud action; **the repository's own safety rules (no real secret in code, nothing destructive to shared resources, no live cloud action by an agent) are outside every level** and a persona's wording cannot lower them (reconciliation §4 item 2). Wherever a 2026-10-20 document says "Critical mode is byte-identical to today", the overlay restates it as "Critical's findings policy and readiness are unchanged" (reconciliation §4 item 1; the sentences are listed by LV1.2).

## Global constraints and review focus

- **Readiness is one deterministic rule inside `runner.Decide`** (D7). A persona can never make a pull request ready or not ready; a test pins that two runs with the same findings and different personas get the same `Decide` answer. *Pinned by LV2.2.*
- **A floor is never lowered by a task, a follow-up, a label or a job variable** (D8, D263). *Pinned by LV3.2.*
- **Hotfix still never deploys and never merges** (D13). *Pinned by LV4.2 (the PR-body sections) and the existing D13 tests.*
- **A persona text is data, not code:** embedded Markdown with stable wording tests; no template that interpolates repository content (repository content is data, never instructions). *Pinned by LV2.1.*
- **A repository in two areas** is a validation error, not a silent pick (D264). *Pinned by LV3.1.*

## Slices and tasks

### LV1 — the words (both tasks are small and safe to merge alone)

**LV1.1 — rename mode → level.** Files: the ones Slice 1 touched on the branch (`git diff --stat origin/main...origin/rigor-modes`: the `internal/rigor` package, `internal/config` rigor block, the project layer's `defaults.rigor`, testdata fixtures under `testdata/config/*/rigor*.yaml` and `testdata/project-layer/*/rigor*.yaml`, `docs/rigor-modes/*`). The area-rule key `mode:` becomes `level:`; the type `Mode` becomes `Level`; the planned flag is `--rigor`; prose says "rigor level". Interfaces: none new. **Tests:** the existing Slice 1 tests renamed; a fixture that still writes `mode:` is refused with a message naming `level:`. **Command:** `go test ./internal/rigor/... ./internal/config/... ./internal/projectlayer/... -count=1` (the third package name is UNVERIFIED: the task finds the real one). **Red step:** change the valid fixture to `level:` and see the loader refuse it before the rename. **Depends on:** the branch synced with `main`. **Commit:** `rigor: mode becomes level (D262)`. **Reviewer:** standard.

**LV1.2 — restate the "byte-identical" sentences.** Files: the 2026-10-20 documents' sentences that say Critical mode is byte-identical to today (`docs/rigor-modes/README.md`, `reconciliation.md` D7 and D19, `implementation-plan.md`), found by `git grep -n "byte-identical" docs/rigor-modes`. Docs only; the pins (`internal/cli/docs_rigor_modes_test.go`, UNVERIFIED name) updated with them. **Command:** `go test ./internal/cli/ -run 'RigorModes' -count=1`. **Depends on:** nothing. **Commit:** `docs(rigor-modes): Critical's policy and readiness are unchanged, not its prompts`. **Reviewer:** standard.

### LV2 — the personas

**LV2.1 — the five persona texts.** Files (new): `internal/rigor/personas/poc.md`, `startup.md`, `pro.md`, `critical.md`, `hotfix.md`, `internal/rigor/persona.go` (embed + `Persona(level) string`), `internal/rigor/persona_test.go`. The Hotfix text carries the D267 wording (revert plan in the PR body, a morning note; no deploy); the POC text carries the two hard guardrails. **Tests:** every level has a non-empty text under a size budget; the wording tests pin each persona's first sentence and its "hands back" list; the Hotfix text contains "revert" and "morning note" and does not contain "deploy"; no text contains a template placeholder. **Command:** `go test ./internal/rigor/ -run Persona -count=1`. **Red step:** the test fails because the package has no `Persona`. **Depends on:** LV1.1. **Commit:** `rigor: the five personas, embedded (D261, D270)`. **Reviewer:** standard.

**LV2.2 — inject the persona.** Files: `internal/runner/prompts.go` (`SystemPrompt`, `ReviewPrompt`, `FixPrompt` take the resolved level's persona), the one place the runner holds the resolved level (Slice 1's record). **Tests:** each prompt contains its level's persona; **two runs with the same findings and different levels get the same `Decide` answer** (the global constraint); the prompt size budget holds. **Command:** `go test ./internal/runner/ -run 'Prompt|Decide' -count=1`. **Red step:** the prompt-contains-persona test. **Depends on:** LV2.1, Slice 1. **Commit:** `runner: the level's persona shapes the coder and the reviewer (D261)`. **Reviewer:** opus + mandatory independent review (it changes every run's prompt).

### LV3 — the layers

**LV3.1 — project-level areas with members.** Files: the project layer's rigor schema and loader (`defaults.rigor` today; new `rigor.areas[].members[]` where a member is `{repo}` or `{repo, paths}`), `testdata/project-layer/{valid,invalid}/rigor-*.yaml` (new fixtures), the resolver's area matching. **Tests:** a member of a whole repo; a member with paths; a repo in two areas is refused by validation naming both; a path outside its repo is refused (the existing `rigor-area-outside-repo` fixture still refuses); a run touching two areas takes the strictest (D23). **Command:** `go test ./internal/rigor/... ./internal/projectlayer/... -count=1`. **Red step:** the two-area fixture loads without error. **Depends on:** LV1.1. **Commit:** `rigor: an area is a named part of the project (D264)`. **Reviewer:** opus + mandatory independent review (it decides which level a path runs at).

**LV3.2 — the order, the floors and `--rigor`.** Files: the resolver (one function stating the order once), the run command's flag and the task file's key (`fugaro run --rigor`, `rigor:`), `docs/project-layer.md` §2's statement of the order. **Tests:** the full order (built-in < project < area < project per-repo < repo YAML < task) as a table test; a task lowering a default works; **a task below a floor is refused, and so is every other lowering path** (follow-up comment, label, env variable); Hotfix needs its base-branch opt-in (D12). **Command:** `go test ./internal/rigor/... ./internal/cli/ -run 'Rigor' -count=1`. **Red step:** a task at `poc` on a floor path resolves to `poc`. **Depends on:** LV3.1, Slice 1. **Commit:** `rigor: --rigor, the one order, and the floors that stay (D263)`. **Reviewer:** opus + mandatory independent review.

### LV4 — the artefacts

**LV4.1 — the backlog's `kind` field.** Files: the backlog format and its generated index (the T4.3/T4.4 files under `docs/backlog/` tooling: the generator, the pin test), front matter gains a ninth required field `kind` (`deferred` or `open-risk`) and `mode` becomes `level`. **Tests:** an item without `kind` is refused; the index lists the kind; regenerating is byte-stable. **Command:** `go test ./internal/backlog/... -count=1` (package name UNVERIFIED). **Red step:** a fixture item without `kind` passes today. **Depends on:** LV1.1, the backlog's T4.3/T4.4. **Commit:** `backlog: items carry a kind; open risks are items (D265)`. **Reviewer:** standard.

**LV4.2 — the per-level hand-back.** Files: the pull-request body builder, and the filing step of Slice 4's T4.5. **Behaviour:** POC: a PR-body "how to run it" note and the line "this is a prototype; do not ship it", no item; Startup/Pro: one `deferred` item per tolerated finding (D30, unchanged) and the list in the body; Critical: one `open-risk` item per finding it could not close, and the verification reasoning in the body; Hotfix: the **revert plan** and the **morning note** (what broke, what was done, what needs a proper fix, **how to verify the service is restored**) in the body, plus the one item Hotfix always files. **Tests:** one golden PR body per level. **Command:** `go test ./internal/runner/ -run 'PRBody|Backlog' -count=1`. **Red step:** the Hotfix golden lacks the revert plan. **Depends on:** LV2.2, LV4.1, Slice 4 T4.5. **Commit:** `runner: each level hands back its own debt (D265, D267)`. **Reviewer:** standard.

### LV5 — review matching and the Pro stop

**LV5.1 — the deterministic artefact checks.** Files: `runner.Decide` and its policy table. **Behaviour:** at startup and pro a tolerated finding without its item makes the run not ready; at critical a finding that is neither fixed nor an `open-risk` item makes it not ready; at hotfix a body without a revert plan or a morning note makes it not ready, and a diff over D14's minimality rule is not ready. The reviewer's persona carries the standards; these checks do not depend on it. **Tests:** one per rule, each with the mutation that removes it. **Command:** `go test ./internal/runner/ -run 'Decide' -count=1`. **Red step:** a Hotfix body with no revert plan is ready. **Depends on:** LV4.2, Slice 3. **Commit:** `runner: Decide checks what each level promised to hand back (D261)`. **Reviewer:** opus + mandatory independent review (it is `Decide`).

**LV5.2 — Pro stops with OPEN QUESTIONS.** Files: the runner's end reasons and the run record (`runstore.Record`), the PR body builder, the console's schema for the record if a new reason must be listed (U4). **Behaviour:** a run whose agent reports a scope, cost or data-model decision it should not make ends with the PR as a draft whose body opens with OPEN QUESTIONS and the end reason `needs_guidance`; a follow-up answers it. **Tests:** the reason round-trips through the record, `fugaro diagnose` prints it, the console's schema accepts it; a draft is never marked ready with the section present. **Command:** `go test ./internal/runner/ ./internal/runstore/ -run 'Guidance' -count=1`. **Red step:** the record refuses the unknown reason. **Depends on:** LV2.2. **Commit:** `runner: a Pro run that needs a call it may not make stops as a draft with OPEN QUESTIONS (D268)`. **Reviewer:** standard.

### LV6 — recipes

**LV6.1 — a recipe may branch on the level.** Files: `internal/recipe` (schema and validation: a `rigor` condition on a step or model choice; U3 names the existing conditional if there is one), `docs/recipes.md`. **Tests:** a recipe naming different reviewers per level resolves to each; an unknown level is refused; **the per-run cap is the same at every level** (D16). **Command:** `go test ./internal/recipe/... -count=1`. **Red step:** a recipe with a `rigor` key is refused today. **Depends on:** LV3.2. **Commit:** `recipe: the level is available to a recipe (D269)`. **Reviewer:** standard.

### LV7 — promotion

**LV7.1 — `fugaro run --promote --rigor <level> <area>`.** Files: the run command, a new task kind in the runner's task spec, the backlog reader (T4.x tooling), the persona text of the target level. **Behaviour:** reads the area's backlog items (deferred, open-risk, a Hotfix morning note) and the code, works at the target level and closes the items it pays down (D31: the closing PR flips the status); a source last run at `poc` has no items, so the first pass is an **audit** that writes the missing list as items before any fix. **Tests:** items are read and closed; the audit pass runs when no items exist; a promote target below the area's current level is refused. **Command:** `go test ./internal/cli/ ./internal/runner/ -run 'Promote' -count=1`. **Red step:** `--promote` is an unknown flag. **Depends on:** LV2.2, LV3.2, LV4.1, LV4.2. **Commit:** `run: a promote task raises an area to a higher level (D266)`. **Reviewer:** opus + mandatory independent review.

## Parallel execution

Edges from the **Depends on** lines. Levels (tasks in one level run concurrently):

| Level | Tasks |
| --- | --- |
| 0 | LV1.1, LV1.2 (the branch sync is a prerequisite task of the 2026-10-20 plan, not repeated here) |
| 1 | LV2.1, LV3.1, LV4.1 |
| 2 | LV2.2, LV3.2 |
| 3 | LV4.2, LV5.2, LV6.1 |
| 4 | LV5.1 |
| 5 | LV7.1 |

**Critical path:** LV1.1 → LV2.1 → LV2.2 → LV4.2 → LV5.1 → LV7.1.

**Shared files (no level has two writers of one file; the table is re-checked when a task's Files change):**

| File / area | Writers (task: level) |
| --- | --- |
| `internal/rigor/` (types, loader) | LV1.1: 0; LV3.1: 1 |
| `internal/rigor/persona*.go` and `personas/` | LV2.1: 1 only |
| the project layer's rigor schema | LV3.1: 1 only |
| backlog format and generator | LV4.1: 1 only |
| `internal/runner/prompts.go` | LV2.2: 2 only |
| the resolver | LV3.2: 2 only |
| the PR body builder | LV4.2: 3 only |
| the run record and end reasons | LV5.2: 3 only |
| `internal/recipe/` | LV6.1: 3 only |
| `runner.Decide` | LV5.1: 4 only |
| the run command and task spec | LV3.2: 2 (the flag); LV7.1: 5 (the promote kind) |

LV4.2 and LV5.2 both touch the runner package in level 3 but different files (the PR body builder, the end reasons): **UNVERIFIED** until each task's first commit names its files; if both turn out to edit one file, LV5.2 moves to level 4.

## Checks

No new check number. Slice 8's Check 119 (owner-run) gains one step per level: a throwaway task at each level on the sandbox, and the hand-back artefact read back. The owner runs it; no agent performs any step.

## Open items

None beyond reconciliation §4 (the three open tensions). Nothing here is approved to build.
