# Recipes: selectable task loops

Status: design, 2026-10-07. Source: the user's draft `fugaro-recipes-spec.md`, narrowed by the decisions below. Target release: 0.5.0.

## 1. Purpose

The runner's task loop is fixed: implement, an optional first-line review and fix loop, then one senior review and fix loop. A **recipe** makes that loop a named, selectable, inspectable object. Users pick one from a built-in catalog, a project defines its own once for all its repositories, and a repository can define its own.

Success: a task can name a recipe (`fugaro run --recipe claude-solo`), and the run behaves accordingly. A repository that names none behaves exactly as before.

## 2. What the code does today

These facts shape the design; the draft spec assumed otherwise in places.

- `agentLoop` (`internal/runner/runner.go`) is hard-coded: `implement`; optional `first_line` (`review_first` then `fix`, up to `agent.first_line_rounds`, at most 3, coder model, never decides readiness); then `review` and `fix` for up to `agent.review_rounds` (1 to 10) with the reviewer model.
- Stage names map to only two roles (`config.StageRole`): `review` is the reviewer, every other stage is the coder. `background` is Claude Code's own housekeeping model. The budget gateway pins, the allow-list and price checks, and the dashboard entry all assume exactly these roles.
- The runner runs no checks. The coder calls `fugaro verify`; the runner only reads the records. A run is ready only if the final commit has a passing test record and the senior review's verdict is `ship`; otherwise the PR is a draft (`runner/outcome.go`).
- The runner does not read the project's shared bucket config. Project settings reach it as job environment variables set by `fugaro init --repo`. Task specs (`internal/task`) are written only by the CLI (`run.go`, `followup.go`).
- Config is strict-parsed in three places: the Go struct, `Validate` and the JSON schema.

## 3. Decisions

| # | Question | Decision |
|---|---|---|
| 1 | How much freedom in 1.0 | Topologies over the existing roles; the format uses named roles so more can be added later. |
| 2 | Models by name or role | By role only. A recipe never names a model; models come from `agent.models`. |
| 3 | How a project recipe reaches the runner | The launching CLI resolves it and embeds it in the task spec. The runner needs no bucket access. |
| 4 | Extending a recipe | No `extends` in 1.0 (key reserved). |
| 5 | Existing `agent.*` knobs | They keep working; the `default` recipe reads them. A recipe's own values win over them; a per-task override wins over both. |
| 6 | Check steps | Not in 1.0 (step type reserved). |
| 7 | Project recipe storage | One object per recipe in the runs bucket, with its own commands. |

## 4. The format

```yaml
version: 1
name: cheap-loop-senior
description: Cheap first-line review/fix loop, then one senior review
roles:                       # optional; aliasing only
  reviewer: coder            # the reviewer role uses the coder's model
steps:
  - first_line: { max_rounds: 2 }   # optional
  - review:     { max_rounds: 3 }   # required
```

- The `implement` stage always runs first. `steps` covers what follows it, in order.
- 1.0 step types: `first_line` (at most once, `max_rounds` 1 to 3) and `review` (exactly once, last, `max_rounds` 1 to 10). They are today's stages with today's behaviour.
- `roles` can only map `reviewer` to `coder`. This is how a single model plays both parts. Readiness still requires a senior review verdict of `ship`, so `review` is required: a recipe cannot produce a ready PR with no review. "Solo" means the same model reviews in a fresh session.
- Outcome rules are not configurable: ready needs a verified passing test on the final commit and a `ship` verdict; anything else is a draft. The draft spec's `on_pass` and `on_fail` are left out.
- Reserved and refused in 1.0, with a message saying so: `checks`, `goto`, `on_reject`, `extends`, any other role name, any model name, `on_pass`, `on_fail`.
- Size limit 16 KiB. Strict parsing: unknown keys, duplicate keys, anchors, aliases, tags and extra documents are refused, as for the shared config.

### Catalog

| Name | Behaviour |
|---|---|
| `default` | Exactly today's loop: first-line per `agent.first_line_review` and `first_line_rounds`, senior rounds per `agent.review_rounds`. |
| `cheap-loop-senior` | First-line (2 rounds), then senior (1 round, reviewer role). |
| `claude-solo` | `reviewer: coder`, senior review (1 round), no first-line. |

Task-shaped recipes (fix a bug, add a feature, refactor) wait until the format has proven itself.

## 5. Choosing and finding a recipe

- **Choosing.** The first of: `--recipe NAME` (on `fugaro run` and the follow-up command), `agent.recipe: NAME` in `fugaro.yaml`, `default`. A follow-up keeps the recipe of the run it continues unless one is named.
- **Finding.** For a name, the first match wins: repository `.fugaro/recipes/<name>.yaml`; project `fugaro/recipes/<name>.yaml` in the runs bucket (`fugaro-runs-<gcp_project>`); catalog. A project recipe named `default` replaces the catalog default for the whole project.
- **Who resolves.** The CLI resolves all three layers so a malformed or unknown recipe fails before any cloud work: it reads the repository layer from the local working tree's `.fugaro/recipes/`, then the project and catalog layers, and embeds a project or catalog recipe in `task.json` with its sha256. When the repository file wins, the task carries only its name and source; the runner reads that file from the checkout at the task's ref at bootstrap, parses it once, and fails the run with "commit and push it" if it is not there. A first run with no recipe in the task uses `agent.recipe` from the `fugaro.yaml` it read; a follow-up gets its recipe only from the launching CLI (`--recipe`, else the previous run's task).
- **Project storage.** `fugaro recipes publish <file>` writes `fugaro/recipes/<name>.yaml`. The CLI reads it with the same strict read, cache (24 h fresh, 7 days offline with a warning) and trust rules as the shared config (`internal/localcfg/sharedcache.go`, `blobx.ReadStrict`). Publishing is refused in a coding-agent session and exits non-zero when nothing was written. Only default-named runs buckets are supported, as for the shared config.
- **Commands.** `fugaro recipes ls`, `show <name>` (with the layer it comes from), `publish <file>`, `validate <file>`. `fugaro validate` also checks the repository's `.fugaro/recipes/`.

## 6. Running a recipe

- `agentLoop` takes a step list instead of the hard-coded sequence. `stage()` stays the primitive.
- Roles resolve before model lookup: with `reviewer: coder`, `ModelFor("reviewer")` returns the coder's model. Gateway pins, `CheckPins`, `CheckAllowed` and the price checks therefore see ordinary model IDs and are unchanged. If the resulting models fail those checks, the run fails at bootstrap as it would today.
- The `default` recipe's step list is derived from `agent.*` at bootstrap, so the loop for a repository with no recipe is the same code path with the same inputs.
- Precedence of values: per-task override, then the recipe's own, then `agent.*`. Token and dollar caps always come from policy and can only tighten.

## 7. Safety

- A repository recipe is repository data, as `fugaro.yaml` is. The agent could edit it during a run, but only the next run reads it, and the runner parses it once at bootstrap.
- A recipe can only choose among today's stages within today's limits (review rounds 1 to 10, first-line rounds 1 to 3), cannot name a model, and cannot change policy, so it cannot escape the allow-list, the price table or the caps.
- A project recipe in the bucket is untrusted input: anyone with object write access can change it. It gets the same treatment as the shared config: strict parse, size limit, validation, and a recorded sha256 so a run can be traced to the exact text.
- Nothing about a recipe is a secret.

## 8. Records and the dashboard

- The run record (`result.json`) gets `recipe: {name, source, sha256}`, where source is `repo`, `project` or `catalog`.
- The registry entry (`agents/<slug>/<run>`) gets the recipe name for non-`default` recipes. This needs a field in `budget.AgentEntry`, `clipEntry` and the RTDB rules, which only `fugaro init` deploys.
- `fugaro watch` and `fugaro ls` show it.

## 9. Compatibility and errors

- A task spec without a recipe field runs as `default`. Queued tasks stay valid.
- **Version skew.** A runner older than this feature rejects a task spec carrying a recipe. The CLI therefore checks the workflow's build record (`base_ref`, as `init --anchor` does for `gcp_project`) and refuses to launch a recipe run on an image older than the release that ships recipes, naming the fix: `init --base <kind>`, `init --repo`, `image build`. Runs of `default` carry no recipe field and are unaffected.
- A malformed or unknown recipe fails at the CLI before any cloud work, with the path of the problem. A bad repository recipe found by the runner at bootstrap ends the run as `infra_error` with no outcome and the reason in the run's error, because bootstrap runs before any PR exists; it never falls back to `default` silently.
- **`agent.recipe` in `fugaro.yaml`.** Binaries older than 0.5.0 refuse the key as unknown, exactly as they did `gcp_project`. `fugaro validate` warns when it is set, the image check runs whenever the checkout sets it, and the 0.5.0 Highlights say every CLI, CI pin and job image must be on 0.5.0 before it is merged.
- **Old RTDB rules.** The deployed rules refuse an entry with an unknown key, which would halt runs on an installation that has not rerun `fugaro init`. The runner therefore writes the registry `recipe` field only for recipes other than `default`, and drops it once with a warning naming `fugaro init` if the rules refuse the entry.

## 10. Testing

- Parser and validator table tests, golden recipe files, each reserved key's message.
- Resolution order across the three layers, and the follow-up rule.
- Equivalence: the `default` recipe through the new loop and the old loop, with the fake agent, gives identical stage order, models and outcomes.
- Runner tests per catalog recipe with the fake agent; gateway-pin tests for `reviewer: coder`.
- CLI tests for `recipes publish/ls/show/validate` against the fake bucket, including refusal in an agent session.
- Schema and docs checks following the existing `rules` tests.

## 11. Delivery

1. Recipe package: types, parser, validator, JSON schema, catalog, the `default` equivalence test.
2. Runner: execute the step list; record the recipe.
3. CLI: `--recipe`, `agent.recipe`, resolution, task embedding, `recipes` commands, the version-skew check.
4. Registry and dashboard: field, rules, `watch` and `ls` columns.
5. Docs and the setup skill (ask which recipe; explain the catalog).

Released as 0.5.0.

## 12. Out of scope (future)

Check steps run by the runner, `goto` and bounces, `extends`, arbitrary roles and per-recipe model pins, `on_pass` and `on_fail`, task-shaped catalog recipes, executable recipes (which need a sandboxing story).

## 13. Settled in the implementation plan

`docs/plans/2026-10-07-recipes.md` settles these, in its decisions D1 to D11:

- RTDB rules: a `recipe` string under the agent entry, deployed only by `fugaro init`, with the two guards in section 9.
- The 0.5.0 image check reads the build record's `base_ref`, against a `recipesSince = "0.5.0"` constant.
- `fugaro run` prints `recipe: <name> (<source>)` to stderr before launching, and `--json` carries the same.
