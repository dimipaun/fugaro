# Recipes: choose the task loop

Recipes need fugaro 0.5.0 or later; the format this document describes (`use_when`, `mode`, `roles: { coder: reviewer }`, the `check` step and `bounce: first_line`) needs 0.7.0 or later. Design: [design/recipes.md](design/recipes.md), [design/generic-tool.md](design/generic-tool.md) (§3).

A Fugaro run implements the task, then loops: an optional first-line review by the coder's model, then the senior review, each followed by a fix when the review asks for changes. A **recipe** names that loop after `implement` so you can pick it per run, per repository or per project. A repository that names no recipe runs the `default` recipe, which is the loop Fugaro has always had.

## 1. What a recipe is

A recipe is a small YAML file. This is the catalog's `standard` exactly as `fugaro recipes show standard` prints it (without the header):

```yaml
version: 1
name: standard
description: The cheap coder loops review and fix, then the senior reviews; a rejection goes back to the cheap loop.
use_when: Typical feature or bug-fix work that needs a real review. Choose it when nothing more specific fits.
steps:
  - first_line: {}
  - review:
      bounce: first_line
```

The keys are `version` (must be 1), `name`, `description` (optional, at most 200 bytes), `use_when` (optional, at most 300 bytes: see section 2), `roles` (optional, see below) and `steps`. `version`, `name` and `steps` are required. The catalog's `solo` shows `roles`:

```yaml
version: 1
name: solo
description: One model codes and reviews its own work in a fresh session.
use_when: Small, low-risk changes confined to one or two files, where a second model's review would cost more than it finds.
roles:
  reviewer: coder
steps:
  - review: {}
```

- `implement` always runs first, unless `mode: review` (section 3) says there is no `implement` at all. `steps` lists what follows it, in order.
- `check` may appear at most once, and only before `first_line` and `review`: `check: { command: build | test | lint, autofix: true }`. `command` names a `commands.*` key of the `fugaro.yaml` the run reads at its ref (never a command of its own: a recipe never names a command). `autofix` is allowed only with `command: lint`, and needs `commands.fix` next to `commands.lint`.
- `first_line` may appear at most once, with `max_rounds` 1 to 3. It is a review by the coder's model in a fresh session, followed by a fix when it asks for changes. It never decides readiness.
- `review` is required in every recipe: exactly once, and last, with `max_rounds` 1 to 10 (1 only, under `mode: review`). A recipe cannot produce a ready pull request without a senior review.
- A step without `max_rounds` takes `agent.first_line_rounds` or `agent.review_rounds` from `fugaro.yaml` (section 4). A recipe's own `max_rounds` wins over those keys. A per-task `review_rounds` override (the task's `overrides`) wins over a recipe's `max_rounds` for the `review` step.
- `roles: { reviewer: coder }` makes the reviewer run on the coder's model, in a fresh session, with the reviewer's prompt. "Solo" means exactly this: the same model reviews, in a new session that has not seen the coder's reasoning. `roles: { coder: reviewer }` is the other direction: the coder (the `implement` stage, and any `fix`) runs on the reviewer's model instead, with the reviewer's per-call output limit; `agent.models.coder` is ignored if it is set (the run logs that), the same way `reviewer: coder` ignores `agent.models.reviewer`. The two exclude each other: a recipe cannot set both.
- Models are never named in a recipe. They come from `agent.models` in `fugaro.yaml`, as they always did.
- Readiness is unchanged and not configurable: a pull request is ready only if the final commit has a verified passing test and the senior review's verdict is `ship`. Anything else is a draft. A `check` step that passes ends the run with no pull request at all (section 5); `mode: review` never changes a pull request's draft state.
- Under `reviewer: coder`, the reviewer's model and its per-call output limit are the coder's, and `agent.models.reviewer` is ignored (the run logs that). The output limit is applied before the project's policy: if the default branch's `fugaro.yaml` sets a tighter `agent.max_output_tokens.reviewer`, the aliased reviewer limit ends up tighter than the coder's, and the run's report notes that the looser value was ignored. Limits from policy can only tighten. `coder: reviewer` is the mirror image: the coder's (and `fix`'s) model and per-call output limit are the reviewer's, `agent.models.coder` is ignored if set (the run logs that), and the same policy rule applies to the coder's output limit.
- `review: { bounce: first_line }` needs a `first_line` step before the `review` step. When the senior review asks for changes, the usual `fix` still runs, but it is then followed by another `first_line` round (bounded by `agent.first_line_rounds`, or the `first_line` step's own `max_rounds`) before the next senior round, instead of going straight back to the senior reviewer. This repeats on every senior rejection, up to `agent.review_rounds` (or the `review` step's `max_rounds`) senior rounds in total; the last round never bounces, since there is no further round to use it. `result.json`'s `reviews` array records which cheap loop (if any) a first-line review followed: `bounce: 1` on the first one that followed a senior rejection, `2` on the second, and so on; the field is omitted (0) on every senior review and on a first-line review that ran before any rejection.
- A recipe file is at most 16 KiB and is strictly parsed: unknown keys, duplicate keys, YAML anchors, aliases, tags and extra documents are refused. A recipe older than 0.7.0 never used `use_when`, `mode`, `roles.coder`, a `check` step or `bounce`; a recipe that does is refused outright by a CLI or runner older than 0.7.0 (section 6).
- `name`, `description` and `use_when` are refused if they hold a C0 or C1 control character, DEL, a Unicode format character (bidi overrides, zero-width characters and the like) or a line or paragraph separator (U+2028, U+2029): a project recipe is anyone-with-bucket-write's text, shown verbatim by `recipes ls` and read by the routing skill, so none of those three fields can carry a character that would corrupt a terminal or make the same bytes read differently to a human and a skill.

## 2. The catalog

| Name | Behaviour | Use when |
|---|---|---|
| `default` | Today's loop. A first-line review only when `agent.first_line_review` turns it on (`auto`, the default for that key, turns it on when a provider serves the coder and none serves the reviewer), for `agent.first_line_rounds` rounds, then the senior review for `agent.review_rounds` rounds. | The implicit choice: an unconfigured repository keeps its behaviour. |
| `solo` | `roles: { reviewer: coder }`; `review: {}`. | Small, low-risk changes confined to one or two files, where a second model's review would cost more than it finds. |
| `standard` | `first_line: {}`; `review: { bounce: first_line }`. | Typical feature or bug-fix work that needs a real review. Choose it when nothing more specific fits. |
| `premium` | `roles: { coder: reviewer } `; `review: {}`. No cheap stage. | High-stakes or subtle work (concurrency, security, data migrations) where a cheap model's attempt is likely to waste the review. |
| `review-only` | `mode: review`; `review: { max_rounds: 1 }`. No coding. | Reviewing a human-written or external pull request. Launch with `--pr N`. **Not yet launchable, section 8.** |
| `pick` | `mode: review`; `review: { max_rounds: 1 }`. No coding. | Comparing several pull requests for the same task to decide which to keep, once every attempt has one. **Not yet launchable, section 8**: needs a best-of-N launch flow `fugaro run` does not yet have (today's `--pr` takes one pull request, not several). |
| `test-and-fix` | `check: { command: test }`; `first_line: {}`; `review: { bounce: first_line }`. | Keeping a branch green, chasing a failing or flaky test, CI babysitting. A green run ends with no pull request. **Not yet launchable, section 8.** |
| `lint-fix` | `roles: { reviewer: coder }`; `check: { command: lint, autofix: true }`; `review: {}`. | Routine mechanical cleanup (formatting, lint and type-check errors) in a repository with `commands.lint` and `commands.fix`. **Not yet launchable, section 8.** |

Only the catalog's own `default` follows `agent.first_line_review`. Every other recipe, including a `default` you define yourself in the repository or the project, is taken as written: a `first_line` step in it always runs.

`use_when` is required for every catalog recipe but `default`. A repository or project recipe without one gets a `fugaro recipes ls` note ("the routing skill matches on use_when, add one"): the routing skill falls back to `description`, but a good `use_when` is what it actually matches the task against.

**`check` steps and `mode: review` are not yet launchable.** Both are part of the 0.7.0 recipe format (Task 8 of the generic-tool plan), and `fugaro recipes ls`/`show` list and print them like any other recipe, but no runner yet executes a `check` step as a gate or skips `implement` under `mode: review` (that lands in a later release). Rather than silently fall back to running `implement` regardless, `fugaro run`, `fugaro recipes validate` and `fugaro recipes publish` all refuse a recipe using either, with "recipe NAME cannot be launched yet: ...", naming which construct. This means `solo`, `standard`, `premium` and `default` are launchable today; `review-only`, `pick`, `test-and-fix` and `lint-fix` are not, until that runner work lands (section 8 has the exact refusal). `commands.lint`, `commands.fix` and `review.allow_forks` already exist in `fugaro.yaml` (validated, schema'd) ahead of it.

## 3. Review mode and best-of-N (format only: not yet launchable, section 8)

`mode: review` (default `mode: implement`) means the recipe has no `implement` and no `fix`: exactly one `review` step, `max_rounds: 1`, and no `roles` (there is no coder step to map them onto). `review-only` and `pick` are both `mode: review`; the difference is how they are meant to be launched, not the format. `review.allow_forks` (repository scope only, read from the default branch like `followup.trusted`) will let review mode run a fork pull request's code next to the job's secrets; `false` (the default) is meant to refuse a fork PR once review mode runs.

## 4. Loop knobs stay outside the recipe

`agent.first_line_rounds` (1 to 3, the cheap rounds) and `agent.review_rounds` (1 to 10, the senior rounds; with `bounce`, the number of bounce-backs is `review_rounds - 1`) are the knobs a recipe's own `max_rounds` overrides, never the other way around (section 1). The catalog recipes above set no `max_rounds` (`review-only` and `pick` excepted, which `mode: review` pins to 1), so tuning the loop in `fugaro.yaml` or the project layer keeps you on a built-in recipe. A custom recipe can still pin its own rounds.

## 5. Choosing a recipe

The first of these wins:

1. `fugaro run --recipe NAME`
2. `agent.recipe: NAME` in `fugaro.yaml`
3. `default`

`agent.recipe` applies to first runs only. A follow-up (`fugaro run --pr`) keeps the recipe of the run it continues unless `--recipe` names another.

`fugaro run` prints one line to stderr before it launches: `recipe: <name> (<source>)` when the CLI resolved the recipe; `recipe: default (catalog)` when it resolved the catalog `default`; and `recipe: chosen by the runner (agent.recipe in fugaro.yaml at <ref>, else default)` when you launch with no `--recipe`, there is no local checkout of the repository, and the project has no recipe named `default`. In that case the CLI validates nothing about `agent.recipe` locally: the runner reads it at the ref, so a malformed or unknown recipe fails in the run, not at your terminal, and a project recipe other than `default` is not found there (name it with `--recipe` instead). If the project has a `default`, the CLI embeds it and the line reads `recipe: default (project)`.

## 6. Where a recipe comes from

For a name, the first match wins:

1. The repository: `.fugaro/recipes/<name>.yaml`. It is read from the checkout at the task's ref (for a follow-up, at the base), so commit and push it before you launch; if it is not there the run ends with "commit and push it".
2. The project: `fugaro/recipes/<name>.yaml` in the runs bucket `fugaro-runs-<gcp_project>`, for every repository of the project.
3. The catalog.

A name the 0.7.0 catalog retired (`cheap-loop-senior` -> `standard`, `claude-solo` -> `solo`) is refused with its replacement ("`cheap-loop-senior` was renamed in 0.7.0: use `standard`") once neither the repository nor the project define a recipe of that exact name; a repository or project recipe of the old name still wins, exactly as any other name would.

A project recipe named `default` replaces the catalog's `default` for the whole project. It is then an ordinary recipe-carrying launch: it is embedded in every task, so every launch needs 0.5.0 job images, or 0.7.0 if its text uses a construct from section 1 (section 8). The file name must equal the recipe's `name:`. A name is 1 to 40 of `a-z`, `0-9` and `-`.

When the CLI resolves the recipe (`--recipe`, or a local checkout), it does so before any cloud work, so a malformed, unknown or renamed recipe fails at your terminal with the path of the problem. A project recipe, or a catalog recipe other than `default`, is embedded in the task with its sha256; the catalog `default` is deliberately not embedded (so a default run needs no 0.5.0 runner), except when `--recipe default` must override an `agent.recipe` that could apply. The run record (`result.json`) records the recipe's name, source and sha256. A repository recipe is not embedded: the task carries its name and the runner reads the file at the ref. A bad repository recipe found by the runner ends the run as `infra_error` with the reason; it never falls back to `default`. Repository defaults and workflow profiles use the same publishing model: see [project-layer.md](project-layer.md).

## 7. Commands

- `fugaro recipes ls` lists the recipes of this checkout, the project and the catalog, and which layer wins for each name. `--verbose` also prints each recipe's `use_when` and `steps`; `--json` always carries both, plus `launchable` (a boolean) and, when it is `false`, `not_launchable_reason`: the routing skill reads these so it never picks a recipe only to have `fugaro run` refuse it (section 2's not-yet-launchable recipes, or any recipe with a parse problem, both report `launchable: false`). `not_launchable_reason` reflects `recipe.NotYetRunnable` and goes away, along with that guard, once the runner executes `check` steps and `mode: review` (Tasks 11 and 12 of the generic-tool plan).
- `fugaro recipes show NAME` prints the recipe a run would get, the layer it comes from and its sha256.
- `fugaro recipes validate FILE` checks a recipe file; it needs no cloud access.
- `ls`, `show` and `validate` take `--json` for machine-readable output.
- `fugaro recipes publish FILE` writes the file to the project as `fugaro/recipes/<name>.yaml`. It shows what it replaces and refuses to overwrite a concurrent change. It is refused in a coding-agent session: run it in your own terminal. Project recipes need the default-named runs bucket (`fugaro-runs-<gcp_project>`); with a custom-named bucket, `publish` is refused with a note that gives the required name, and `ls` and name lookups skip the project layer and print the same note.
- To remove a project recipe, delete the object: `gcloud storage rm gs://fugaro-runs-<gcp_project>/fugaro/recipes/<name>.yaml`.
- `fugaro validate` also checks the repository's `.fugaro/recipes/` and warns when `agent.recipe` is set, including when it names a renamed catalog name (see section 8).

A published project recipe is cached on each machine for 24 hours (up to 7 days when the bucket cannot be reached, with a note), so another machine's `fugaro run` may launch the old text for up to 24 hours. `ls` and `show` refresh the cache; `fugaro run` does not.

`fugaro watch` shows the recipe name of a run that does not use `default`. `fugaro runs ls` adds a RECIPE column once any listed run is not `default`; the column then has a value on every row, `default` included. In `fugaro watch` a name longer than 24 columns is shortened, and at widths of about 100 to 130 columns a long recipe name can be clipped further.

## 8. What is refused

A recipe that uses a reserved key is refused with a message that says so:

- `checks` as a step name: the step type is `check`, not `checks`.
- `goto` and `on_reject`: no general jumps between steps. (`review: { bounce: first_line }`, section 1, is the one bounce the format allows, and only back to a `first_line` step already in the recipe.)
- `extends`: copy the recipe instead.
- `on_pass` and `on_fail`: outcome rules are not configurable.
- `model` and `models`: a recipe never names a model; models come from `agent.models`.
- any role other than `reviewer` or `coder`; any `reviewer` value other than `coder`; any `coder` value other than `reviewer`; and both mapped at once (`reviewer: coder` and `coder: reviewer` exclude each other).
- `check.command` other than `build`, `test` or `lint`; `autofix` on anything but `command: lint`.

What recipes do not do in 0.5.0: jump with `goto`, extend another recipe, pin a model per recipe, or configure `on_pass` and `on_fail`. (A `check` step, which does run a build, test or lint command, shipped later; see the generic-tool design.) There are no task-shaped catalog recipes (fix a bug, add a feature) yet.

A recipe that is valid but uses a construct no runner yet executes is refused too, separately from the parser: `mode: review` (section 2). The message says so: `recipe NAME cannot be launched yet: mode: review is not yet executed by any runner: it would run implement and open a pull request instead of only reviewing`. This applies to `fugaro run` (a first run or a follow-up, including one inheriting or keeping a prior run's recipe), `fugaro recipes validate` and `fugaro recipes publish`; `fugaro recipes ls` and `show` still display such a recipe, since looking at one is not launching it. A `check` step is executed by the runner (see the check gate above).

## 9. Rolling out 0.5.0 and 0.7.0

Order matters:

1. Upgrade every CLI, every CI pin and every workflow's job image to 0.5.0 **before** you merge `agent.recipe` into `fugaro.yaml` or launch a non-default recipe; to 0.7.0 **before** you launch a recipe that uses `use_when`, `mode`, `roles.coder`, a `check` step or `bounce` (every catalog recipe but `default` does). A binary older than the one you need refuses the new keys as unknown, and an older runner rejects a task that carries a recipe using them. `fugaro validate` warns when `agent.recipe` is set.
2. `fugaro run` checks the workflow's build record and refuses a recipe run (or a checkout that sets `agent.recipe`) on a job image too old for what it carries. The fix is `fugaro image refresh --repo <owner/name> --workflow <name>` in the repository's checkout, in your own terminal window, or with `--yes` from CI or a script (it copies this release's base image, points the daily image check job at it and rebuilds the image; refused in a coding agent's session either way, and there is still no `--json` or `--plan-only`). Then use the recipe. A launch of the catalog `default` carries no recipe and needs no job-image gate, unless `--recipe default` must override an `agent.recipe` that could apply; a project or repository recipe named `default` is a recipe-carrying launch and does need it.
3. `cheap-loop-senior` and `claude-solo`, the 0.6 catalog's two non-default recipes, are gone from the 0.7.0 catalog: `agent.recipe` or `--recipe` naming either is refused with its replacement (`standard`, `solo`), unless a repository or project recipe of that exact name still exists, which wins as it always did.
4. Deployed Firebase rules are updated only by `fugaro init --firebase <firebase-project-id>` (its Firebase stage). Until you rerun it, the dashboard shows no recipe name; the runs still work, and the run logs a warning that names that command.
5. `review-only`, `pick`, `test-and-fix` and `lint-fix` are in the catalog ahead of the runner work that executes them: launching any of them is refused (section 8) until a later release.

## 10. Safety

A recipe can only choose among the existing stages within their limits. It cannot name a model, change policy or loosen a cap, so it cannot get past the model allow-list, the price table or the token and dollar caps. A repository recipe is repository data, like `fugaro.yaml`: the agent could edit it during a run, but only a later run reads it. A project recipe is bucket text that anyone with write access to the bucket can change, so it is parsed strictly, size-limited and validated, and its sha256 is recorded with each run. A recipe holds no secrets. A `check` step names no command of its own (section 1): it can only point at a `commands.*` key of the `fugaro.yaml` the run already read at its ref, so a project recipe cannot make a job run anything the repository itself did not already say.
