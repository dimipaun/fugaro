# Generic-tool hardening

Status: design for review (2026-10-08), from the owner's "Generic-Tool Hardening Spec" (`fugaro-generic-tool-spec.md`, 158 lines, 11 sections) and two owner rulings:

- **R1.** Everything in the spec except section 1 is **added to release 0.7.0**, the hard-cut base-image consolidation release ([base-image.md](base-image.md), [plans/2026-10-08-base-image.md](../plans/2026-10-08-base-image.md)). The spec's header says "following 0.7.0"; the ruling overrides it.
- **R2.** Section 1, the Docker-capable execution backend, is **release 0.8.0**. It stays measure-first. Its design and its measurement are here so the work can start right after 0.7.0, but it is not part of any 0.7.0 task group or of the 0.7.0 release notes.

Plan: [plans/2026-10-08-generic-tool.md](../plans/2026-10-08-generic-tool.md). Decisions G1 to G27 below each carry a recommendation and the alternative the owner can choose instead.

Related designs, not duplicated here:
- [base-image.md](base-image.md): one base, mise, `fugaro-services`, no sudo (0.7.0).
- [layered-config.md](layered-config.md): the project layer, profiles and the scope table (0.6.0, Phase 1).
- [recipes.md](recipes.md): the recipe format and its safety rules (0.5.0).
- `bucket-iam.md`: the 0.7.0 IAM hardening of the runs bucket, designed in parallel. This design changes no IAM. SECURITY.md (section 9) names that work and leaves its own claims about bucket access to that design's tasks.

## 0. How much is already shipped

Read from the code at `origin/main` 65a6a24. Each claim names its file.

| Spec section | Already there | Missing |
|---|---|---|
| 1 Docker backend | A narrow backend seam (`internal/backend.Backend`: `Launch`, `Execution`, `List`, `Logs`, `Cancel` and two timeout methods), a conformance suite (`backendtest.Run`) and one implementation, Cloud Run jobs. **Every job already runs on the gen-2 execution environment** (`execution_environment = "EXECUTION_ENVIRONMENT_GEN2"` in `deploy/terraform/gcp/modules/workflow/job.tf`, `repo/check.tf` and `installation/history.tf`). The spec's "gen-1 (current)" is wrong. The Docker-free path for test services (PostgreSQL, Redis, the Firebase emulators) is `fugaro-services` plus `image.apt`, which ship in 0.7.0 (base-image §5). | A Docker daemon. gen-2 alone does not provide one. |
| 2 Repository provisioning | Every derived image bakes a full-history, single-branch clone into `/work/repo` (`images/derived/Dockerfile.tmpl` line 50). At start the runner opens it, fetches the base and checks out `fugaro/<run-id>` from the task's ref (`internal/runner/runner.go` around 790-860). `gitops.OpenOrClone` already clones in full when the directory has no checkout (`internal/gitops/gitops.go:81`), which only local runs use today. | A per-workflow choice, and a job that runs without a derived image. |
| 3 Recipe catalog | The format, three layers (repository, project, catalog), `description` (200 bytes), strict parsing and a recorded sha256 (`internal/recipe`). A catalog of `default`, `cheap-loop-senior` and `claude-solo`. Loop counts come from `agent.first_line_rounds` and `agent.review_rounds` unless a step sets `max_rounds`. | Five of the seven starter recipes, a "use when" text, bounce-back, check steps, review-only and parallel attempts. §3 has the details. |
| 4 Recipe-aware routing | `fugaro recipes ls --json` aggregates the layers. | Every skill. The `routing` and `parallelism` skills never mention recipes, and the `working` skill says to use `--recipe` only when the user asks. |
| 5 Noun-verb CLI | `recipes`, `secrets`, `budget`, `image` and `config` are already nouns, **in the plural** where they name a collection. | `fugaro ls` is a bare verb, and `recipes ls` has no `--verbose`. |
| 6 Model routing | Non-Claude models already go to any Anthropic-compatible endpoint that a `providers:` entry in the owner's local config names (`internal/config/providers.go`). OpenRouter is one such entry; a vendor's own endpoint is another. Claude models always go direct, to Anthropic or Vertex: `ClaudePatterns` cannot be claimed by a provider. | Documentation of the direct case. Nothing else (§6). |
| 7 Logs | `fugaro logs RUN [-f] [--json]` exists (`internal/cli/logs.go`). It reads Cloud Logging through the Fugaro log view, redacts every line and prints `HH:MM:SS SEV [stage/stream] message`. | A link. `logs` prints none; `run` and `diagnose` print the execution's console URL. |
| 8 Cost preview | A worst-case reservation per call (`pricing.Model.WorstCase`), per-run, per-stage and daily caps, `budget.Precheck`, and per-run cost by model (`runstore.Cost.ModelBy`). | Cost per stage. `result.json`'s `StageTiming` keeps only the name and duration, so no average cost per round exists anywhere. |
| 9 SECURITY.md | **It exists** (30 lines): reporting, supported versions, scope. The trust model is in design v1 §6, about 100 dense lines, and is honest about the gaps. | A readable trust model and its known limits in SECURITY.md itself. |
| 10.1 Review-ready list | `result.json` has `PR`, `Outcome` (`ready`, `draft` or `none`) and `Reviews`. | Watch shows **no finished run at all**: a row exists only while `/agents/<slug>/<run>` exists, and `Session.Finish` deletes it (`internal/budget/registry.go:204`). |
| 10.2 Expandable detail | A space key, but it **folds a repository block**. Selection is a repository slug (`model.sel`), never a run, so no run can be expanded. `AgentEntry` has `stage`, `round`, `spent`, `verify` and `prUrl`. The runner writes the first three and **never writes `verify` or `prUrl`**, although the database rules already accept both. | Per-run selection, the detail view, and the fields it needs (§10.2). |
| 10.3 Completed-run filtering | Nothing to filter: finished runs are never shown. | All of it. |
| 11 Pluggable control plane | `docs/backends.md` says Firebase stays GCP-bound. | One paragraph naming the seam. |

**Summary.** About a fifth of the spec is shipped or needs only documentation: logs, model routing, the existing SECURITY.md and backend seam, the recipe layers and listing. Recipes need the most new work: three format extensions and two pieces of runner machinery. The dashboard items look small in the spec but need new data first: watch has never seen a finished run.

## 1. Docker-capable execution backend (release 0.8.0, measure-first)

**Correction to the spec's premise.** Fugaro already runs on gen-2. gen-2 gives full Linux syscall compatibility, but a Cloud Run container is not privileged. Whether a Docker daemon can run there at all, in any form, is the first unknown; startup latency is the second. Both are unverified. The candidates:

1. **Rootless Docker or Podman inside the gen-2 job.** This needs user namespaces, `newuidmap`/`newgidmap`, and a storage driver that works without privileges (fuse-overlayfs or `vfs`). It is not known whether Cloud Run's sandbox (gVisor-based on gen-1, a microVM on gen-2) permits nested user namespaces for a non-root user. The base image already ships the Docker CLI client and no daemon (base-image §9).
2. **A VM backend**, a second `Backend` implementation on Cloud Batch or Compute Engine. A real VM runs `dockerd` normally. Cold start means booting a VM and pulling a roughly 3 GB image. The package comment in `internal/backend/backend.go` already names "a later Batch backend" as the expected sibling.

**The measurement (G1).** One user-run live check, **Check 31** in `docs/gcp-live-checklist.md`, on the sandbox installation `belong` only. Claude creates no cloud resource, in any project. The check:

- **A. Feasibility on gen-2.** A throwaway Cloud Run job in the sandbox project, from an image the owner builds from a committed Dockerfile (`deploy/sandbox/docker-probe/`, written by the plan: the 0.7.0 base plus `podman`, `fuse-overlayfs`, `uidmap` and a probe script). The probe records, as `FACT:` lines:
  - `unshare --user --map-root-user true`;
  - `podman info`;
  - `podman run --rm docker.io/library/postgres:17 postgres --version`;
  - the same through `dockerd-rootless.sh`;
  - the wall time of each step.
- **B. Cold start on gen-2.** If A passes, the probe also times the following, five executions each:
  - execution start to the probe's first line;
  - a `podman pull` of `postgres:17` plus `redis:7`;
  - container start until `pg_isready` answers;
  - the same with a pre-pulled image layer cache restored from the runs bucket (`cache/`).
- **C. Cold start on a VM.** A Cloud Batch job (one task, `e2-standard-4`, the probe image, the Container-Optimized OS boot disk) with the same probe, five executions. It records submission to the first line, the pull, and Postgres ready.
- **D. Today's baseline.** Five runs of the sandbox's existing workflow, measuring execution start to the runner's first log line (`fugaro logs RUN`, first `started` line). This is the same measurement as base-image plan Task 22 Step 1, reused.

**Threshold (G2).** gen-2 is suitable if A passes and B's median start-to-services-ready is at most 60 s above D's median. A VM backend is worth building if C's median is at most 120 s above D's. Otherwise Docker stays out, and the documented answer is `fugaro-services` plus `image.apt`. Veto alternative: no threshold; the owner decides from the numbers.

**The 0.8.0 phase, BLOCKED until Check 31's FACT lines are recorded.**
- **If gen-2 is suitable.** A per-workflow `docker: true` key (scopes: project via profile, and repository). The workflow's job gets a larger `ephemeral-storage`, the image gets rootless podman with a `docker` shim, and the runner starts the user-mode service before the first stage. The agent's environment gains `DOCKER_HOST` (unix socket in `$XDG_RUNTIME_DIR`), which is the first addition to the env allowlist since M9 and must be reviewed as such. No change to the `Backend` interface.
- **If only the VM backend is suitable.** `internal/backend/batch` implements `Backend` and passes `backendtest.Run` on a new fake. The `backend:` key gains `cloud-batch`. `fugaro init` gets a Batch stage: the job template, its service account (the same per-workflow account), and the log routing into the existing log bucket. Provisioning and log isolation are outside the seam (docs/backends.md), so this is the larger option, sized at about 12 tasks.

**Why not 0.7.0** (the owner has ruled, R2; the reasons are recorded):
- 0.7.0 is already a hard cut of every base image, the config and the check job. A second new execution path in the same release doubles what a failed upgrade can break.
- No Fugaro run can test it: a Cloud Run job has no Docker daemon, and CI never touches a cloud.
- The decision between gen-2 and a VM depends on Check 31, which needs the 0.7.0 base.

**Mitigation that ships in 0.7.0 anyway:** the base-image work already makes "services without Docker" a supported path. The 0.7.0 docs task here adds one routing-skill line: a task that needs Docker or Testcontainers stays local (already in the `routing` skill), unless the repository's tests can use `fugaro-services`.

## 2. Repository provisioning: baked or clone

**What "baked" means today.** The derived image holds a clone from the time of its build. The runner moves it to the task's ref at start, which costs one incremental fetch. The checkout is never wrong, only older, so a stale image costs fetch time, not correctness. What a rebuild buys is warm dependency caches and runtimes (mise, the warm-up, `image.setup`). So the spec's "image goes stale on every push" is true only for dependencies, which the daily image check already rebuilds for when a lockfile or mise file changes (`imagecheck.ImageConfigHash`).

**What "clone" should mean for a generic tool.** It should mean "no per-repository image at all". The spec's pros are "no rebuild infrastructure; simplest", and those hold only if nothing is built per repository. So:

- `checkout: clone` (G3: the key's name) on a workflow. The workflow's job runs **the installation's copy of the base image** (`base_images.base`, the 0.7.0 `fugaro-base`) directly. There is no Cloud Build, no derived image, no registry per repository and no daily check job for that workflow.
- At start the runner makes a **blobless partial clone**: `git clone --filter=blob:none --no-checkout --single-branch --branch <base>`, then the usual checkout of the task's ref (G4). A shallow `--depth 1` clone is refused because checkpoints, follow-ups and `merge-base` need history.
- Then, if the checkout has a mise config, the runner runs `mise install` as `fugaro`, with the same settings as the image build (base-image §4). Its time is recorded as a new stage timing, `provision`.
- The dependency warm-up is the agent's job (or the `commands.build` the coder runs); the content-addressed `cache:` entries still apply.
- **Refused with `checkout: clone`**, by `fugaro validate`: `image.apt`, `image.setup`, `image.tools`, `image.skip_build_scripts` and `dockerfile:`. All of them need a build, and D1 (no root at run time) rules out installing system packages at start. The message names `checkout: baked`.

**Default (G5).** Recommend **`baked`** in 0.7.0: it is what every existing workflow, the setup skill, `image refresh`, the check job and the 0.7.0 migration assume, and most real repositories need `image.apt` (Playwright, PostgreSQL). The setup skill offers `clone` when the repository needs no system package. **Veto alternative: the spec's default, `clone`.** Then any workflow with an `image:` block or `dockerfile:` must say `checkout: baked`, and `fugaro validate` says so. This is cheap now (only the owner's installations exist), and it flips the setup skill's recommendation.

**Scope.** It is a workflow key, `workflows.<n>.checkout`, with scopes `project` (through a profile) and `repo` in layered config's scope table. It is not overridable per task: a task cannot choose its job's image.

**Unverified.** That a Cloud Run job's service agent can pull the installation's `fugaro-base` registry image for a workflow job. Today only check jobs and builds read that registry. The plan's live check (Check 32, user-run, sandbox) confirms it before release. Also unverified: the start-up cost of `mise install` (Node is about 30 MB, Temurin about 200 MB) on every run, which Check 32 also measures.

**Later (spec):** choosing dynamically by repository size or setup time. Not designed.

## 3. Recipe catalog

### 3.1 The starter catalog against the format

| Recipe | Today | Needs |
|---|---|---|
| **solo** | Nearly `claude-solo` (`roles: {reviewer: coder}`, `review: {max_rounds: 1}`). | Nothing new. It takes its rounds from the knobs (§3.3), so it omits `max_rounds`. **Expressible now.** |
| **standard** | `first_line` then `review` runs the cheap loop then the senior loop, but a senior rejection is followed by one `fix`, never by another cheap loop. | **Format extension:** `bounce: first_line` on the `review` step (G8). |
| **premium** | Not expressible: a recipe cannot put the coder on the reviewer's (strong) model. | **Format extension:** `roles: {coder: reviewer}` (G9). |
| **best-of-N** | Not expressible. The runner has one implement session per run, and parallelism exists only at launch (`--batch`, `max_parallel`). | **New machinery**, at launch level, not in the runner (G13): `fugaro run --attempts N` plus the `pick` recipe. |
| **review-only** | Not expressible: `implement` always runs first, and the outcome rules assume Fugaro's own PR. | **Format extension plus runner machinery:** `mode: review` (G12). |
| **test-and-fix** | Not expressible. The runner runs no checks; `checks` is reserved and refused. | **Format extension plus runner machinery:** the `check` step as a gate before implement (G10, G11). |
| **lint-fix** | Not expressible, and there are no lint or autofix commands in `fugaro.yaml`. | The `check` step with `autofix`, plus two `fugaro.yaml` keys, `commands.lint` and `commands.fix` (G10). |

**The extended format.** It stays `version: 1` (G7). 0.7.0 is a hard cut, and a 0.6 CLI already refuses the new keys as unknown. The CLI's existing image gate (`checkRecipeImage`) gains `recipesV07Since = "0.7.0"` for a recipe that uses any new key.

```yaml
version: 1
name: standard
description: Cheap coder loops review and fix, a senior review decides; a senior rejection goes back to the cheap loop
use_when: Typical feature or bug-fix work that needs a real review. The default choice when nothing else fits.
steps:
  - first_line: {}
  - review: { bounce: first_line }
```

New keys:
- **`use_when`** (top level, at most 300 bytes) is the "use me when" text of §3.2.
- **`roles: {coder: reviewer}`**: the coder runs on the reviewer's model. It is exclusive with `{reviewer: coder}`.
- **`mode: implement | review`** (default `implement`). `review` means no implement, no fix, and exactly one `review` step.
- **The `check` step:** `check: { command: build | test | lint, autofix: true }`. Check steps may only come first, before the implicit implement. `autofix` is allowed only with `lint`.
- **`bounce: first_line`** on a `review` step. It needs a `first_line` step before it.

Still reserved and refused: `goto`, `on_reject`, `extends`, `on_pass`, `on_fail`, `model`, `models`, and any other role or command text.

**Safety, held to recipes.md §7.** Each extension keeps a rule from that section:
- **A recipe still names no command.** `check.command` is an enum naming a `commands.*` key of the `fugaro.yaml` the run reads at its ref: repository text reviewed like code, and never from the bucket. A project recipe in the bucket therefore cannot make a job run anything the repository did not already say.
- **A recipe still names no model.** `coder: reviewer` maps to a model already in `agent.models`, so the gateway pins, `CheckPins`, `CheckAllowed` and the price table see an ordinary allowed ID.
- **Every loop is still bounded.** Bounce-back is bounded by the knobs (§3.3). The maximum number of stages in a run is `1 + 2·F + R·(2 + 2·F)`: implement, the first cheap loop, then for each senior round a review, a fix and another cheap loop. F is the first-line rounds (at most 3) and R the review rounds (at most 10). That is at most 87 stages under the existing limits, and the dollar and token caps bound it first.
- **Readiness is still not configurable.** A ready PR needs a verified passing test on the final commit and a senior `ship`. A `check` gate that passes ends the run with no PR (G11), never with a ready one. `mode: review` never changes a PR's draft state.
- **The size limit, strict parse and sha256 are unchanged.**

### 3.2 Self-description

`use_when` is required for catalog recipes (a test enforces it) and optional for others. `recipes ls` shows `description`. `recipes ls --verbose` and `--json` also show `use_when`, the steps and the source. A custom recipe without `use_when` gets a `recipes ls` note ("the routing skill matches on use_when; add one"). The skill falls back to `description` (G14). Veto alternative: no new key, with `description` raised to 300 bytes and used for both.

### 3.3 Loop knobs stay outside the recipe

The knobs exist already: `agent.first_line_rounds` (1 to 3, the cheap rounds) and `agent.review_rounds` (1 to 10, the senior rounds; with `bounce`, the number of bounce-backs is `review_rounds - 1`). Layered config already scopes both to project and repository, and `review_rounds` to the per-task override. **The catalog recipes set no `max_rounds`**, so a user tunes the loop in `fugaro.yaml` or the project layer and stays on the built-in recipe (G6). No new knob is added. Veto alternative: a separate `agent.bounces`. The precedence (override, then the recipe's own `max_rounds`, then `agent.*`) is unchanged, so a custom recipe can still pin its rounds.

### 3.4 Layering

Already shipped, in the order repository, then project, then catalog, which is the spec's order read from the most specific. Project recipes are stored once per project (`fugaro/recipes/<name>.yaml` in the runs bucket). Nothing changes.

### 3.5 The catalog in 0.7.0 (G15)

| Name | Text (steps) |
|---|---|
| `default` | Unchanged: today's loop derived from `agent.*`, with `first_line` per `agent.first_line_review`. It stays the implicit choice when nothing is named, so an unconfigured repository keeps its behaviour. |
| `solo` | `roles: {reviewer: coder}`; `review: {}` |
| `standard` | `first_line: {}`; `review: {bounce: first_line}` |
| `premium` | `roles: {coder: reviewer}`; `review: {}` |
| `review-only` | `mode: review`; `review: {max_rounds: 1}` |
| `pick` | `mode: review`; `review: {max_rounds: 1}`; targets several PRs (G13) |
| `test-and-fix` | `check: {command: test}`; `first_line: {}`; `review: {bounce: first_line}` |
| `lint-fix` | `roles: {reviewer: coder}`; `check: {command: lint, autofix: true}`; `review: {}` |

`cheap-loop-senior` and `claude-solo` are **removed** (the hard cut, and the memory rule: only the owner's installations exist). Resolving either name fails with `renamed in 0.7.0: use standard (cheap-loop-senior) or solo (claude-solo)`. Veto alternative: keep both as catalog entries.

### 3.6 Runner machinery

- **Bounce.** In `seniorLoop`, when the step has `bounce: first_line` and the verdict is `changes`: run `fix`, then `firstLine` with the plan's first-line rounds, then the next senior round. `ReviewSummary` gains `Bounce int` so the record shows which cheap loop a review followed.
- **Check gate (G10, G11).** Before implement, for each `check` step, the runner runs `verify.Run` itself (the same code the agent's `fugaro verify` runs) with kind `build`, `test` or the new `lint`, in the checkout at the task's ref.
  - With `autofix`, it first runs `commands.fix` through `sh -c` as the agent user, with the agent's environment. If the tree changed, it commits `fugaro: autofix (commands.fix)` on the run's branch.
  - **All checks pass and nothing changed:** the run ends `succeeded` with outcome `none` and the new `gate: "clean"`. No branch is pushed and no PR is opened, and the report says "nothing to fix".
  - **All pass after an autofix commit:** implement is skipped. The runner runs `verify test` once on the commit, so readiness can be met, and goes to the review steps.
  - **Any check fails:** implement runs with the failing checks' `Record.Summary()` and up to 40 log-tail lines (redacted, the existing `logtail`) prepended to the task, marked as untrusted command output.
- **Review-only (G12).** `fugaro run --recipe review-only --pr N` (any open PR of the repository, not only Fugaro's).
  - The runner checks out the PR's head read-only (on GitHub, `refs/pull/N/head`) and runs the senior review with the reviewer's prompt.
  - It then posts one comment through `gitprov.Provider.Comment` (it exists today): the verdict and findings, redacted, capped at 60 KiB.
  - It never pushes and never edits the PR. The agent's environment has no git token, exactly as a follow-up's has none (`runner.go:953`).
  - The outcome is a new value, `reviewed`.
  - **A PR whose head is in a fork is refused** unless the base branch's `fugaro.yaml` sets `review.allow_forks: true`. Reviewing means the agent can run that code next to the job's secrets (SECURITY.md).
- **best-of-N (G13).** Launch-level.
  - `fugaro run --attempts N` (2 to 4) launches N independent runs of the same task and recipe, with one batch label `bestof-<first run id>`.
  - Each attempt is an ordinary run with its own branch and draft-or-ready PR.
  - When all have settled, the `pick` recipe runs as `fugaro run --recipe pick --pr A --pr B [--pr C]`. A `mode: review` run checks out each head in turn, writes one comparison, and posts it as a comment on each PR, naming the one to keep. It closes nothing.
  - The skill launches `pick` when `fugaro runs ls --batch` shows all attempts settled; the CLI never waits.
  - **Synthesis is not in 0.7.0:** writing a fourth PR from the best parts is a later recipe.
  - Veto alternative: in-run parallel implement sessions. That would mean several Claude Code processes and worktrees in one container, per-session gateway pins, and a much larger runner change.

## 4. Recipe-aware routing skill

The `routing` skill gains a section "Choosing a recipe" (G16). It does not get a new skill: the upgrade work already adds a fifth skill (`upgrade`), and choosing a recipe is the same judgement as choosing where to run.

- It runs `fugaro recipes ls --json` and matches the task against `use_when` (else `description`). There is no decision tree in the skill: the texts are the catalog's, and a custom recipe with a good `use_when` is picked like a built-in one.
- **Override is by policy.** A recipe the user names in the request wins. For one interactive task, the skill states its choice in one line with the reason ("standard: feature work that needs review"). For a batch, it chooses per task without asking, never blocks, and ends with one summary line ("15 runs: 12 standard, 2 solo, 1 best-of-3").
- `agent.recipe` in the repository or project still decides when the skill passes no `--recipe`. The skill passes `--recipe` only when its match differs from that default, so a project's policy is respected unless the task clearly calls for another recipe.
- The `working` skill's `launch.md` line "use `--recipe` only when the user asks" is replaced by "the routing skill chooses; see it". The setup skill's rule (never write `agent.recipe` without the user's decision) stays.

## 5. CLI naming: noun-verb

**Recommendation (G17):**
- **Plural nouns, as today.** `fugaro recipes ls` exists, and so do `secrets`, `budget` and `image`. Cobra aliases make `fugaro recipe ls` work too, at one line each (`Aliases: []string{"recipe"}`), so the spec's spelling works without a second command.
- **`fugaro runs ls` replaces `fugaro ls`.** The noun is "run", not "job": in Fugaro a *job* is a workflow's Cloud Run job, and a *run* is one execution of a task (`fugaro run`, `RUN` arguments, `runs/` in the bucket). `fugaro job ls` would list the wrong thing. The alias `jobs` is not added, for the same reason.
- **`fugaro ls` is removed, with a hidden tombstone in 0.7.0.** `fugaro ls` exits 2 with `fugaro ls was renamed in 0.7.0: use fugaro runs ls (same flags)`. The tombstone is deleted in 0.8.0. No working alias (the memory rule; the tombstone costs one function). Veto alternative: keep `ls` as a hidden working alias.
- **The verbs on one run stay top-level:** `run`, `logs`, `diagnose`, `cancel` and `watch`. They are the hot path, like `git log` and `docker logs`. Moving them under `runs` gains consistency and costs every skill, doc and muscle memory. Veto alternative: `fugaro runs logs|diagnose|cancel`, each with a tombstone.
- `fugaro recipes ls --verbose` (§3.2).
- Future nouns slot in the same way: `fugaro backends ls` when a second backend exists.

**Coordination.** Layered config Task 17 edits `internal/cli/ls.go` (`printRows`, the layer column), so the rename lands after it. The rename touches every doc and skill that says `fugaro ls`, which the plan's docs task greps.

## 6. Model routing knob

**No new knob (G18).** The `providers:` map in the owner's local config already is the knob:
- a model ID that the `openrouter` entry claims goes to OpenRouter;
- the same ID claimed instead by a `deepseek` entry with `base_url: https://api.deepseek.com/anthropic` goes direct;
- providers may not overlap, so the choice is explicit and per model family.

What is missing is the documentation and one check: `docs/multi-model.md` gains a "Direct to the vendor" section with that example, and the setup or doctor output names which route each pinned model takes.

**Not changed:** Claude models never go through OpenRouter. `ClaudePatterns` stays unclaimable, because a Claude call through a third party would leave the Anthropic or Vertex billing and data path the owner chose. Veto alternative: lift `ClaudePatterns` for a provider marked `claude: true`. That touches the oauth/vertex refusals, `isProviderID`, the pin validation and the gateway's route table, which is far from small.

**Unverified:** that a vendor's own Anthropic-compatible endpoint (DeepSeek's, Moonshot's) accepts the gateway's forwarded bodies byte for byte and reports usage the way the gateway reads it. Check 25 covers OpenRouter only. The docs say this, and the plan adds a check entry for the owner (Check 33, optional).

## 7. Logs

`fugaro logs` exists (§0). Additions (G19):
- `fugaro logs RUN --url` prints the execution's console link (`runstore.Launch.LogURL`, which `run` and `diagnose` already print) and reads no log.
- The empty-result hint prints the same link.

Small, and independent of everything else.

## 8. Cost preview (nice-to-have, planned last, optional)

A ceiling, not a forecast (G20). `fugaro budget preview --recipe R --runs N [--repo R]` prints:

- **The ceiling:**
  ```
  N × min(per-run cap, stages_max(R) × agent.max_budget_usd)
  ```
  - The per-run cap is `FUGARO_MAX_RUN_USD` or the database's per-run cap, whichever is lower.
  - `stages_max(R)` is the recipe's stage bound from §3.1, with the repository's knobs.
  - `agent.max_budget_usd` is Claude Code's own per-stage cap.
  - It is labelled "rough worst case". With no per-stage cap and no per-run cap, it says "unbounded: set budget.per_run_usd".
- **Against the headroom:** today's remaining daily cap, global and repository, read as `fugaro budget show` reads it. It prints one verdict line: `fits`, `may exceed the repository's daily cap`, or `exceeds`.
- **A typical figure, when history exists:**
  - the mean cost per stage kind and model over the repository's last 30 days of `result.json`;
  - this needs the runner to record cost per stage: `StageTiming` gains `model_usd` and `model`, from the gateway's `StageReport` (or the oauth result event's `total_cost_usd`);
  - with fewer than 5 runs, the line is omitted.

It is read-only and needs no new IAM: launchers can already read the bucket and the budget. If the per-stage record proves awkward, ship the ceiling alone: it needs no history.

## 9. SECURITY.md

**Recommendation (G21):** keep the existing file (reporting, supported versions, scope) and add a section "Trust model and known limits" of about 100 lines, written for an adopter. The full text is drafted as the deliverable of plan Task 21. It is grounded in what the code does:
- The agent's environment allowlist (`internal/agent/env.go`).
- **What a first run's agent holds:** a repository-scoped GitHub installation token (contents and pull requests write) plus `GH_TOKEN`, a per-run gateway token, and the workflow's secrets.
- **What the container holds:** the GitHub App private key (which reaches every repository the App is installed on), the model and provider keys, and the job's service account through the metadata server.
- **Network egress is open.**
- **Branch protection is the only guard on the base branch:** the runner checks its own push (`fugaro/<id>`), not the agent's.
- **Check commands see the agent's full environment.**
- **The budget bounds a well-behaved run.** The provider account's credit limit bounds a hostile one.

Design v1 §6 stays the detailed reference. Statements about runs-bucket access cite `bucket-iam.md` and are updated by that design's tasks.

## 10. Dashboard (`fugaro watch`)

### 10.1 Review-ready hand-off (G22)

Finished runs come from the **runs bucket**, not RTDB. The queued scanner (`internal/cli/watch_queued.go`) already reads each recent run's objects every poll. It now also returns finished runs (a `result.json` exists) as `watch.FinishedRun{Run, Slug, Status, Outcome, PR, PRURL, FinishedAt, Title}`.

A section **"Ready for your review"** at the bottom lists runs whose `Outcome` is `ready`, meaning a senior `ship` and a verified passing test (the readiness rule), with each PR's URL. "Passed senior review" alone (a `ship` verdict on a draft whose tests failed) is not listed there: it shows under the finished runs with its `draft` outcome. This needs no RTDB rules change and no new writer. Veto alternative: write `prUrl` and the outcome into `/outcomes`. That gives realtime finish events, but needs a rules change and a sweeper change.

### 10.2 Expandable job detail (G23, G24)

**Plumbing.**
- Selection becomes a row cursor over (block header, run row) pairs, keyed by `slug` and `run`, never by index.
- Space on a run row toggles its detail and on a header folds the block. Every key rebuilds and redraws (it already does).
- Space is ignored while help is open, which fixes today's "folds a hidden block" behaviour.
- Blocks keep a **stable order**, by repository name, so they no longer re-sort by spend under the cursor. Within a block: running, then queued, then finished (§10.3). Veto alternative: keep the spend order, which moves the cursor's block.
- When the selected row vanishes, the cursor moves to its neighbour in the same block, not to the top.

**Instrumentation first.** The runner fills these, all through the existing heartbeat (15 s):
- `verify`: the latest verify summary, from the records it already reads;
- `prUrl`: once the early draft PR exists.

Both keys are already accepted by the deployed rules. New keys, which need a rules change (deployed only by `fugaro init`):
- `action`: the last tool summary, from `agent.Relay`'s redacted `toolSummary`, clipped to 120 characters, like `tool Bash: go test ./internal/...`;
- `tokens`: the stage's input and output tokens, from the stream's usage.

The runner writes them only if the rules accept them, and drops them once with a warning naming `fugaro init`, exactly as `recipe` did in 0.5.0 (`registry.go:54`).

**Expanded content:** stage and round, the last action, verify, the PR link, spent dollars (or notional) and tokens, the deadline, and the models with the recipe.

**Data exposure.** `action` is the redacted text that already goes to the run's Cloud Logging lines, but RTDB readers are a wider set: anyone the database rules let read `/agents`, which is the launchers. That widens who sees command lines and file paths from log-view holders to dashboard readers. SECURITY.md says so. Veto alternative: no `action` field; the detail view shows only the stage, verify and spend.

### 10.3 Completed-run filtering (G25)

- **Ordering:** running at the top, then queued, then finished, newest first.
- **Default filter:** a finished success is hidden when it is older than `--keep 6h` or beyond the 15 most recent finished rows (`--keep-count 15`), whichever is stricter.
- **Failures** (`failed`, `halted`, `infra_error`) stay for 24 h, or until acknowledged with `x` on the selected row. Acknowledgements are local, per viewer, in `$XDG_STATE_HOME/fugaro/watch-acks.json` (best effort; losing it only shows failures again).
- **`--all`** starts unfiltered. **`a`** toggles between filtered and full without relaunching.
- **The footer** shows the view: `showing active + recent · a: all` or `showing all · a: recent`, and it is advertised at every width.
- **Degraded mode** (no budget backend) gets the same filter over its `ls`-style rows.

The defaults are flags, not config keys. Veto alternative: local-config keys `watch.keep` and `watch.keep_count`.

## 11. Deferred: the pluggable control plane

`docs/backends.md` gains a section "The control plane seam" (G26). It names what Firebase provides that a replacement must provide: atomic multi-path conditional updates (leases), server-evaluated rules that tie counters together, event streams (kill switches, the dashboard) and a document store for history. It names the packages that talk to it (`internal/budget`, `internal/rtdb`, `internal/firestore`, `internal/watch`) and says the seam is not abstracted and will not be before demand. Docs only.

## 12. Release, order and coordination

**In 0.7.0 (R1):** sections 2 to 10, and 11's doc. Section 8 is the last, optional group.

**Order against the other 0.7.0 work** (G27):
- **Layered config Phase 1 (0.6.0) merges first.** Its scope table (`internal/config/scope.go`) and project layer (`layer.go`) must list the new keys `workflows.<n>.checkout`, `commands.lint`, `commands.fix` and `review.allow_forks`. Its Task 17 edits `ls.go`, so it must land before the rename.
- **Base image Group 2** (`base-kind`, Tasks 9 to 14) edits `internal/config/config.go`, `validate.go`, `schemas/fugaro.schema.json` and `testdata/config`. The tasks here that add `fugaro.yaml` keys (the commands, `checkout`, `review.allow_forks`) start after that group merges, to avoid three-way conflicts in the schema and the corpus. `checkout: clone` also needs the `base` kind (Task 13).
- **The release freeze** starts at base-image Group 2's merge. These groups merge inside it. No release is cut between them.
- **`bucket-iam.md`** owns every IAM change. Nothing here grants a role. `checkout: clone`'s image pull may need one (unverified, Check 32): if it does, that grant is handed to the IAM plan, not added here.
- **Release notes:** the plan's last task adds sections to `docs/releases/v0.7.0.md`, which base-image Task 22 creates. It also adds the operator steps: rerun `fugaro init` for the new RTDB keys, `fugaro ls` is gone, and the old recipe names are gone.

**Sizing.** 23 implementation tasks in 0.7.0, plus one full-suite task per group and the release-notes task (plan Tasks 1 to 25):
- Group A, visibility: 6 tasks.
- Group S, SECURITY.md: 1.
- Group B, naming: 1.
- Group C, recipes: 7.
- Group D, provisioning: 4.
- Group E, docs and skills: 2.
- Group F, cost preview: 2 (optional).

Plus four tasks for 0.8.0 (plan Tasks 26 to 29): the measurement, two blocked phases and the tombstone's removal. Recipes are the largest risk: four of the runner changes sit in `agentLoop`, which every run executes.

## 13. Verified and unverified

**Verified by reading the code:** everything in §0, the env allowlist, the token scopes, the gen-2 setting, the registry fields and their writers, the queued scanner, the recipe parser and runner loop, and `verify.Run`'s reusability.

**Unverified, each with the check that settles it:**
- Docker in gen-2, rootless (Check 31A).
- gen-2 and VM cold starts (Check 31B and 31C).
- A workflow job pulling `fugaro-base` directly, and the cost of `mise install` at start (Check 32).
- Vendor Anthropic-compatible endpoints (Check 33).
- GitHub's `refs/pull/N/head` fetch with an installation token for a fork PR. The docs say it works; it is verified by the review-only task's fake only, so the owner verifies it in the sandbox before using `review-only` on a fork with `allow_forks`.

## 14. Decisions

| # | Decision | Recommendation | Veto alternative |
|---|---|---|---|
| G1 | §1 measurement | User-run Check 31 on `belong`: rootless podman in gen-2, gen-2 and Batch VM cold starts, today's baseline | (owner ruling R2: 0.8.0, measure-first; not vetoable here) |
| G2 | §1 threshold | gen-2 if ≤60 s over baseline, VM if ≤120 s, else no Docker | No threshold; owner decides from numbers |
| G3 | §2 key name | `workflows.<n>.checkout: baked \| clone` | Spec's `repo_provisioning` |
| G4 | §2 clone kind | Blobless partial clone, single branch | `--depth 1` shallow (breaks checkpoints and follow-ups) |
| G5 | §2 default | `baked` in 0.7.0; setup skill offers `clone` | Spec's default `clone` |
| G6 | §3.3 knobs | Reuse `agent.first_line_rounds` and `agent.review_rounds`; catalog recipes set no rounds | New `agent.bounces` knob |
| G7 | §3 format version | Extend version 1 (hard cut) with a 0.7.0 image gate | `version: 2`, both accepted |
| G8 | standard | `review: {bounce: first_line}`, bounded by the knobs | General `goto` (refused by recipes.md) |
| G9 | premium | `roles: {coder: reviewer}` | Not in the catalog |
| G10 | test-and-fix, lint-fix | `check` gate step naming `commands.build\|test\|lint`; `autofix` runs `commands.fix`; new keys `commands.lint`, `commands.fix` | Commands in the recipe (refused: bucket text would run code) |
| G11 | Gate passes | Run ends `succeeded`, outcome `none`, `gate: clean`, no PR | Open an empty PR saying so |
| G12 | review-only | `mode: review`, `--pr N` any PR, comment by the runner, no git token, forks refused unless `review.allow_forks` | Allow fork PRs by default |
| G13 | best-of-N | `run --attempts N` (2–4) plus `pick` recipe over several PRs; no synthesis in 0.7.0 | In-run parallel sessions |
| G14 | Self-description | New `use_when` (300 bytes), required in the catalog | Reuse `description`, raised to 300 bytes |
| G15 | Catalog names | Spec names plus `pick`; `default` unchanged as the implicit choice; `cheap-loop-senior` and `claude-solo` removed with a "renamed" message | Keep them as entries |
| G16 | Routing skill | Extend `routing` with "Choosing a recipe"; batch summaries | A sixth skill |
| G17 | CLI naming | Plural nouns plus singular aliases; `fugaro runs ls`; `ls` tombstone in 0.7.0; run verbs stay top-level | `job ls`; a working `ls` alias; all run verbs under `runs` |
| G18 | Model routing | No knob; document direct providers; Claude stays direct | Allow Claude via OpenRouter |
| G19 | Logs | `logs --url`, link in the empty hint | Nothing (already exists) |
| G20 | Cost preview | `fugaro budget preview`: cap-based ceiling plus optional history from per-stage cost | Drop it (low priority) |
| G21 | SECURITY.md | Add "Trust model and known limits" to the existing file | A separate `docs/security.md` |
| G22 | Review-ready source | Runs bucket (`result.json`) through the queued scanner | RTDB `/outcomes` with a rules change |
| G23 | Watch ordering and selection | Per-row cursor by (slug, run), blocks by name, space on run toggles detail | Keep spend order |
| G24 | Detail data | Write `verify` and `prUrl` (already allowed); new `action` and `tokens` keys with a drop-once fallback | No `action` (narrower exposure) |
| G25 | Filtering | `--keep 6h`, `--keep-count 15`, failures 24 h or until `x`, `--all`, `a`, footer | Local-config keys |
| G26 | Control plane | Document the seam in `docs/backends.md` only | Nothing |
| G27 | Ordering | After layered config Phase 1 and base-image Group 2; inside the freeze; no IAM here | Interleave freely |
