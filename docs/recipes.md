# Recipes: choose the task loop

Recipes need fugaro 0.5.0 or later. Design: [design/recipes.md](design/recipes.md).

A Fugaro run implements the task, then loops: an optional first-line review by the coder's model, then the senior review, each followed by a fix when the review asks for changes. A **recipe** names that loop after `implement` so you can pick it per run, per repository or per project. A repository that names no recipe runs the `default` recipe, which is the loop Fugaro has always had.

## 1. What a recipe is

A recipe is a small YAML file. This is the catalog's `cheap-loop-senior` exactly as `fugaro recipes show cheap-loop-senior` prints it (without the header):

```yaml
version: 1
name: cheap-loop-senior
description: Cheap first-line review/fix loop by the coder's model (2 rounds), then one senior review
steps:
  - first_line: { max_rounds: 2 }
  - review: { max_rounds: 1 }
```

The keys are `version` (must be 1), `name`, `description` (optional, at most 200 bytes), `roles` (optional, see below) and `steps`. `version`, `name` and `steps` are required. The catalog's `claude-solo` shows `roles`:

```yaml
version: 1
name: claude-solo
description: One model plays both parts. The coder's model reviews in a fresh session, one senior round, no first line.
roles:
  reviewer: coder
steps:
  - review: { max_rounds: 1 }
```

- `implement` always runs first. `steps` lists what follows it, in order.
- `first_line` may appear at most once, with `max_rounds` 1 to 3. It is a review by the coder's model in a fresh session, followed by a fix when it asks for changes. It never decides readiness.
- `review` is required in every recipe: exactly once, and last, with `max_rounds` 1 to 10. A recipe cannot produce a ready pull request without a senior review.
- A step without `max_rounds` takes `agent.first_line_rounds` or `agent.review_rounds` from `fugaro.yaml`. A recipe's own `max_rounds` wins over those keys. A per-task `review_rounds` override (the task's `overrides`) wins over a recipe's `max_rounds` for the `review` step.
- `roles: { reviewer: coder }` makes the reviewer run on the coder's model, in a fresh session, with the reviewer's prompt. "Solo" means exactly this: the same model reviews, in a new session that has not seen the coder's reasoning. It is the only role mapping allowed.
- Models are never named in a recipe. They come from `agent.models` in `fugaro.yaml`, as they always did.
- Readiness is unchanged and not configurable: a pull request is ready only if the final commit has a verified passing test and the senior review's verdict is `ship`. Anything else is a draft.
- Under `reviewer: coder`, the reviewer's model and its per-call output limit are the coder's, and `agent.models.reviewer` is ignored (the run logs that). The output limit is applied before the project's policy: if the default branch's `fugaro.yaml` sets a tighter `agent.max_output_tokens.reviewer`, the aliased reviewer limit ends up tighter than the coder's, and the run's report notes that the looser value was ignored. Limits from policy can only tighten.
- A recipe file is at most 16 KiB and is strictly parsed: unknown keys, duplicate keys, YAML anchors, aliases, tags and extra documents are refused.

## 2. The catalog

| Name | Behaviour |
|---|---|
| `default` | Today's loop. A first-line review only when `agent.first_line_review` turns it on (`auto`, the default for that key, turns it on when a provider serves the coder and none serves the reviewer), for `agent.first_line_rounds` rounds, then the senior review for `agent.review_rounds` rounds. |
| `cheap-loop-senior` | Two first-line rounds by the coder's model, then one senior round. |
| `claude-solo` | `reviewer: coder`: the coder's model also reviews, in a fresh session, for one round. No first line. |

Only the catalog's own `default` follows `agent.first_line_review`. Every other recipe, including a `default` you define yourself in the repository or the project, is taken as written: a `first_line` step in it always runs.

## 3. Choosing a recipe

The first of these wins:

1. `fugaro run --recipe NAME`
2. `agent.recipe: NAME` in `fugaro.yaml`
3. `default`

`agent.recipe` applies to first runs only. A follow-up (`fugaro run --pr`) keeps the recipe of the run it continues unless `--recipe` names another.

`fugaro run` prints one line to stderr before it launches: `recipe: <name> (<source>)` when the CLI resolved the recipe; `recipe: default (catalog)` when it resolved the catalog `default`; and `recipe: chosen by the runner (agent.recipe in fugaro.yaml at <ref>, else default)` when you launch with no `--recipe` and there is no local checkout of the repository. In that last case the CLI validates nothing locally and cannot see a project recipe: the runner reads `agent.recipe` at the ref, so a malformed or unknown recipe fails in the run, not at your terminal, and a project recipe is not found there (name it with `--recipe` instead).

## 4. Where a recipe comes from

For a name, the first match wins:

1. The repository: `.fugaro/recipes/<name>.yaml`. It is read from the checkout at the task's ref (for a follow-up, at the base), so commit and push it before you launch; if it is not there the run ends with "commit and push it".
2. The project: `fugaro/recipes/<name>.yaml` in the runs bucket `fugaro-runs-<gcp_project>`, for every repository of the project.
3. The catalog.

A project recipe named `default` replaces the catalog's `default` for the whole project. It is then an ordinary recipe-carrying launch: it is embedded in every task, so every launch needs 0.5.0 job images (section 7). The file name must equal the recipe's `name:`. A name is 1 to 40 of `a-z`, `0-9` and `-`.

When the CLI resolves the recipe (`--recipe`, or a local checkout), it does so before any cloud work, so a malformed or unknown recipe fails at your terminal with the path of the problem. A project recipe, or a catalog recipe other than `default`, is embedded in the task with its sha256; the catalog `default` is deliberately not embedded (so a default run needs no 0.5.0 runner), except when `--recipe default` must override an `agent.recipe` that could apply. The run record (`result.json`) records the recipe's name, source and sha256. A repository recipe is not embedded: the task carries its name and the runner reads the file at the ref. A bad repository recipe found by the runner ends the run as `infra_error` with the reason; it never falls back to `default`.

## 5. Commands

- `fugaro recipes ls` lists the recipes of this checkout, the project and the catalog, and which layer wins for each name.
- `fugaro recipes show NAME` prints the recipe a run would get, the layer it comes from and its sha256.
- `fugaro recipes validate FILE` checks a recipe file; it needs no cloud access.
- `ls`, `show` and `validate` take `--json` for machine-readable output.
- `fugaro recipes publish FILE` writes the file to the project as `fugaro/recipes/<name>.yaml`. It shows what it replaces and refuses to overwrite a concurrent change. It is refused in a coding-agent session: run it in your own terminal. Project recipes need the default-named runs bucket (`fugaro-runs-<gcp_project>`); with a custom-named bucket, `publish` is refused with a note that gives the required name, and `ls` and name lookups skip the project layer and print the same note.
- To remove a project recipe, delete the object: `gcloud storage rm gs://fugaro-runs-<gcp_project>/fugaro/recipes/<name>.yaml`.
- `fugaro validate` also checks the repository's `.fugaro/recipes/` and warns when `agent.recipe` is set (see section 7).

A published project recipe is cached on each machine for 24 hours (up to 7 days when the bucket cannot be reached, with a note), so another machine's `fugaro run` may launch the old text for up to 24 hours. `ls` and `show` refresh the cache; `fugaro run` does not.

`fugaro watch` shows the recipe name of a run that does not use `default`. `fugaro ls` adds a RECIPE column once any listed run is not `default`; the column then has a value on every row, `default` included. In `fugaro watch` a name longer than 24 columns is shortened, and at widths of about 100 to 130 columns a long recipe name can be clipped further.

## 6. What is refused

A recipe that uses a reserved key is refused with a message that says so:

- `checks`: check steps are reserved for a later recipe version. The runner runs no checks; the coder calls `fugaro verify`.
- `goto` and `on_reject`: no jumps or bounces between steps.
- `extends`: copy the recipe instead.
- `on_pass` and `on_fail`: outcome rules are not configurable.
- `model` and `models`: a recipe never names a model; models come from `agent.models`.
- any role other than `reviewer`, and any role value other than `coder`.

What recipes do not do in 0.5.0: run check steps, jump with `goto`, extend another recipe, pin a model per recipe, or configure `on_pass` and `on_fail`. There are no task-shaped catalog recipes (fix a bug, add a feature) yet.

## 7. Rolling out 0.5.0

Order matters:

1. Upgrade every CLI, every CI pin and every workflow's job image to 0.5.0 **before** you merge `agent.recipe` into `fugaro.yaml` or launch a non-default recipe. A binary older than 0.5.0 refuses `agent.recipe` as an unknown key, and an older runner rejects a task that carries a recipe. `fugaro validate` warns when `agent.recipe` is set.
2. `fugaro run` checks the workflow's build record and refuses a recipe run (or a checkout that sets `agent.recipe`) on a job image older than 0.5.0. The fix is, in order: `fugaro init --base <kind>` from outside the checkout, `fugaro init --repo` in the checkout, `fugaro image build`. Then use the recipe. A launch of the catalog `default` carries no recipe and needs no 0.5.0 image, unless `--recipe default` must override an `agent.recipe` that could apply; a project or repository recipe named `default` is a recipe-carrying launch and does need it.
3. Deployed Firebase rules are updated only by `fugaro init`. Until you rerun it, the dashboard shows no recipe name; the runs still work, and the run logs a warning that names `fugaro init`.

## 8. Safety

A recipe can only choose among the existing stages within their limits. It cannot name a model, change policy or loosen a cap, so it cannot get past the model allow-list, the price table or the token and dollar caps. A repository recipe is repository data, like `fugaro.yaml`: the agent could edit it during a run, but only a later run reads it. A project recipe is bucket text that anyone with write access to the bucket can change, so it is parsed strictly, size-limited and validated, and its sha256 is recorded with each run. A recipe holds no secrets.
