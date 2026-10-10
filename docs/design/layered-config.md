# Layered project configuration

Status: design, 2026-10-08. Source: the owner's spec "Layered Config and Automatable Repo Adoption" (sub-project 1 of 3), the owner's binding decisions D-a to D-d, and two later rulings: the base image consolidation (one language-agnostic base, runtimes from `mise.toml`) and its hard cut of the legacy kinds. Plan: [../plans/2026-10-08-layered-config.md](../plans/2026-10-08-layered-config.md); its decisions L1 to L24 can be vetoed one by one. Target: **release 0.6.0** for Phase 1. Phase 2 (the image catalog) is blocked on the consolidation release.

Related designs: [shared-config.md](shared-config.md) (the installation config in the bucket, its trust rules and cache), [recipes.md](recipes.md) (project objects resolved by the CLI and embedded in `task.json`), [image-refresh.md](image-refresh.md) (base images, the check job's spec), [m11-setup-and-skills.md](m11-setup-and-skills.md) (`init`, the setup skill).

## 1. Problem

Adopting a repository is heavy and repetitive. Every `fugaro.yaml` restates the same git, agent, image, command, resource and timeout settings. The Rocket Core team has 50 or more microservices that share one Docker and infrastructure setup, so the same 40 lines would be copied 50 times and then drift apart.

**Goal.** Most configuration moves to the project. A repository needs only a minimal `fugaro.yaml` and inherits the rest. Every consumer resolves the same values, and a person can see where each value came from.

The owner named the classic failure mode: **a value set at project level that a repository silently fails to pick up.** Each choice below is judged against it.

**Sub-projects.**

1. Layered configuration: this design.
2. Unattended adoption, a script-safe `fugaro adopt`: a separate, later design. §12 lists the hooks it needs from this one.
3. Lighter per-repository infrastructure: a separate, later design. See the non-goals in §13.

## 2. What exists today (verified in the code)

| Layer | Where | Who reads it |
|---|---|---|
| Fugaro defaults | `internal/config/defaults.go` `applyDefaults`, called once inside `config.Parse`. Kinds are `config.Bases` = go, java-services, web-node | everyone |
| Local project config | `~/.config/fugaro/projects/<name>.yaml` (`internal/localcfg`): the installation's view, the budget ceiling, `base_images`, `repos` | the CLI |
| Shared installation config | `fugaro/config.yaml` in `fugaro-runs-<gcp>` (`internal/cli/sharedcfg.go`): strict `ParseShared`, a 64 KiB cap, a 24 h / 7 d cache. Teammates reach it through `gcp_project:` in `fugaro.yaml`, the trust anchor | the CLI |
| Repository | the checkout's `fugaro.yaml`, strictly parsed (`KnownFields`). Today it must have `version`, `project`, `git.provider` and at least one workflow with `base`, `commands.build` and `commands.test` | 9 `config.Parse` sites: runner bootstrap and follow-up, `cloud.go`, `imagecheck.go` (the check job), `init_repo_target.go`, `init_anchor.go`, `initsecrets.go`, `image.go`, `validate.go`, `doctor.go` |
| Policy ceiling | the owner's budget, baked into each job's env by `init --repo` (`internal/infra/spec.go`). `config.PolicyOf` reads the default branch's policy keys, and the merge only ever tightens (`internal/runner/policy.go`) | the runner |
| Per-task overrides | `task.Overrides`: review rounds, budget, model, total timeout | the runner |
| Recipes | repository, project (`fugaro/recipes/<name>.yaml`) and catalog. The CLI resolves them and embeds the result in `task.json` | the CLI, then the runner |
| Individual | `user:`, `FUGARO_*` env vars, flags. The Claude token is a per-repository secret, not a per-person one | the CLI |

Facts that shape this design:

- **The job account cannot read `fugaro/`.** It reads only `runs/`, `cache/` and `locks/` under its slug (`gcp.JobBucketPrefixes`). The build account, which the daily check job also runs as (`deploy/terraform/gcp/modules/repo/check.tf`), reads and writes `builds/<slug>/` (`gcp.BuildBucketPrefixes`).
- **Cloud Build sees only a shallow clone.** Its `render` step runs `fugaro image render` from the base image. Its `gate` step already reads the bucket as the build account.
- **Task specs are strict.** `task.Parse` uses `DisallowUnknownFields`, so a runner refuses a field it does not know. That is the version-skew pattern recipes used.
- **The image-config hash** (`imagecheck.ImageConfigHash`) covers only a workflow's base, image settings and Dockerfile. It is computed from whatever config the check parsed, so a resolved config hashes like any other.

## 3. The layers and the one rule

From widest to narrowest. For each key, **the narrowest layer that sets it wins.**

1. **Fugaro default.** Ships with Fugaro and is filled in last (`applyDefaults`).
2. **Project layer.** One object per project, `fugaro/project-layer.yaml` in the runs bucket, published by an operator. It has two parts:
   - `defaults:` holds project-wide values for repository keys (git, agent).
   - `profiles:` holds named workflow templates, plus a `default_profile`.
3. **Profile.** The template a workflow starts from. It sits between the project defaults and the repository because it describes one workflow, while the project defaults describe the repository as a whole. The two never set the same key (see §5).
4. **Repository.** The checkout's `fugaro.yaml`. It is reviewed in pull requests.
5. **Per-task override.** The flags of `fugaro run` and the follow-up command: `--model`, `--review-rounds`, `--max-budget-usd`, `--total-timeout`, `--recipe`.

**The policy ceiling is not a layer in this list.** It stays the owner's, in the installation config and the job env. It keeps its tighter-only merge, and no layer can loosen it. The project layer may not even hold policy keys (§5).

**Which repositories the project layer applies to.** Exactly those whose `fugaro.yaml` has `gcp_project:` (the anchor) and whose installation uses the default runs bucket name (decision L9). The anchor line already says "this repository trusts this installation's bucket", so it is also the opt-in. A repository without the line behaves exactly as today. So does every repository of a project that has published no project layer.

**What a repository inherits.**

- Every anchored repository gets the project's `defaults:`.
- A workflow gets a profile only when it names one (`workflows.<name>.profile`).
- A file with no `workflows:` at all gets one implicit workflow named `default`. Its profile is the one named by top-level `profile:`, else the project's `default_profile`.
- A workflow that names no profile takes nothing from any profile.

The profile rule is what keeps existing repositories unchanged. It is also what `fugaro config show` displays, so nobody has to guess.

## 4. Resolution

One pure function, `config.Resolve(repoYAML, layer)`. Every consumer calls it with the same two inputs: the CLI, the runner, Cloud Build's render step and the daily check job. `config.Parse(data)` becomes `Resolve(data, nil)` and behaves byte for byte as before. The only difference is that naming a profile without a layer is an error.

The steps:

1. Decode the repository file alone, strictly, with today's line-numbered errors.
2. Merge, as plain YAML maps: the project `defaults:`, then the repository's top-level keys.
3. Build each workflow by merging its profile and then the repository's own keys for it. A file with no `workflows:` gets the implicit workflow.
4. Strict-decode the merged map into `config.Config`.
5. Apply the Fugaro defaults, then `Validate`.

The function returns the config, a `Resolution` and any problems. The `Resolution` holds:

- **the source of every key:** default, project, profile `<name>` or repo;
- **`LayerSHA256`**, the sha256 of the project layer's text;
- **`ConfigSHA256`**, the sha256 of the resolved config's JSON.

Per-task overrides are applied afterwards, by the runner, exactly as today (`spec.Apply`).

**Merge rules.** There is one rule per value type, with no exceptions per field except the two named below.

| Value | Rule |
|---|---|
| map (`git`, `git.pr`, `agent`, `agent.models`, a workflow, `image`, `commands`, `rerun_failed`, `resources`, `timeouts`, `rebuild`) | merged key by key, recursively |
| scalar (string, number, bool, duration) | the narrower layer's value replaces the wider one when the key is present, even as `""`, `0` or `false` |
| list (`git.pr.labels`, `image.apt`, `image.setup`, `commands.reports`, `cache`, `rebuild.paths`, and any repo-only list) | replaced whole, **never appended**. To add to a profile's list, repeat it; `fugaro config show` prints the list that results |
| `null` or an empty value (`key:`) | "not set here": the wider layer's value stays. This is today's meaning of null |
| `[]` | an empty list replaces the wider one, which clears it |

The two exceptions:

1. **`dockerfile:` (repo).** A repository workflow that sets `dockerfile:` drops its profile's `image:` block. This is the inline escape hatch: a repository with bespoke needs writes its own Dockerfile, and the profile's generated-image settings cannot be combined with one (`image` and `dockerfile` are mutually exclusive today).
2. **`profile:` (repo, top level).** It applies only to a file with no `workflows:`. With `workflows:`, each workflow names its own profile, and a top-level `profile:` is an error.

**Errors name the layer.** A problem with a value a profile or the project set ends with `(set by profile java-service)` or `(set by project)`. A missing value in a workflow that took a profile ends with `(set neither by the repository nor by profile java-service)`. A file whose `workflows:` would come from a layer that is absent says so, and names `fugaro config layer`.

## 5. Scopes: where each key may be set

Each key declares the layers it may appear in. The table is enforced now:

- `config.ParseProjectLayer` refuses an out-of-scope key in `defaults:` or a profile, naming the key, the layers it may live in and why.
- The repository file may set every repository key, as today.
- The individual layer is unchanged: flags and env only.

The table is `config.Scopes` in code; `docs/project-layer.md` prints it, and a docs test keeps the two equal. "Project" means the project layer's `defaults:`, and "profile" a profile.

| Key | Project | Profile | Repo | Override | Why it is limited |
|---|---|---|---|---|---|
| `version`, `project`, `gcp_project` | | | yes | | they identify the repository and anchor the project layer |
| `profile` | | | yes | | the project chooses its default with `default_profile` |
| `git.provider` | yes | | yes | | |
| `git.base_branch` | | | yes | | a project-wide branch would let a bucket writer point image builds at an unreviewed branch (the shared config refuses `repos.<r>.base_branch` for the same reason) |
| `git.pr.labels`, `early_draft`, `checkpoints` | yes | | yes | | |
| `git.pr.reviewers` | | | yes | | reviewers are people of one repository |
| `agent.auth`, `models.*`, `first_line_review`, `first_line_rounds` | yes | | yes | | |
| `agent.model`, `review_rounds`, `max_budget_usd`, `recipe` | yes | | yes | yes | |
| `agent.instructions`, `agent.review` | | | yes | | they name files in the repository (and are prompt text) |
| `agent.max_run_tokens`, `agent.max_output_tokens.*`, `budget.*` | | | yes | | policy: the ceiling is the owner's, in the installation config; a repository only tightens it |
| `workflows.*.profile` | | | yes | | it chooses a profile |
| `workflows.*.base`, `image.{node,apt,setup,skip_build_scripts}`, `commands.*`, `cache`, `resources.*`, `timeouts.{stage,verify,finalize_reserve}`, `rebuild.*` | | yes | yes | | |
| `workflows.*.timeouts.total` | | yes | yes | yes | |
| `workflows.*.image.jdk` | | | yes | | refused on every base, as today |
| `workflows.*.dockerfile` | | | yes | | it names a file in the repository |
| `workflows.*.secrets` | | | yes | | secrets belong to one repository's Secret Manager entries |
| `followup.trusted`, `followup.allow_public` | | | yes | | they decide whose comments steer a run with the repository's credentials |

**Secrets and tokens.** No `fugaro.yaml` key holds a secret value: `workflows.*.secrets` maps names to variables. The project layer additionally refuses any value shaped like a credential (`sk-ant-…`, `ghp_…`, `github_pat_…`, a PEM private key, and the like), since nothing in it is secret (decision L18).

**Per-person Claude tokens are out of scope.** They change how runs authenticate: a run would need to know who launched it and which secret to mount. That is a separate, later project. The individual layer stays as it is: `user:`, `FUGARO_*` env vars and flags.

**Owner-only keys.** No `fugaro.yaml` key is owner-only today. The ceiling lives outside every committed layer, in the installation config and the job env, so "a repository cannot set what only the owner may" holds by construction. The refusal of policy keys in the project layer keeps it so.

## 6. Profiles

```yaml
# fugaro/project-layer.yaml, in gs://fugaro-runs-acme-fugaro
version: 1
project: acme
gcp_project: acme-fugaro
defaults:
  git:
    provider: github
    pr: { labels: [fugaro] }
  agent:
    auth: oauth
    review_rounds: 2
    recipe: cheap-loop-senior
profiles:
  java-service:
    description: Gradle service with Postgres
    base: java-services
    commands:
      build: ./gradlew assemble
      test: ./gradlew test
      reports: ["**/build/test-results/**/*.xml"]
    cache:
      - { key: [gradle/libs.versions.toml], paths: [~/.gradle/caches] }
    resources: { cpu: 4, memory: 16Gi }
    timeouts: { total: 2h }
  node-web:
    base: web-node
    image: { node: "22" }
    commands: { build: npm run build, test: npm test }
default_profile: java-service
```

The minimal repository file, which a script can write (§12):

```yaml
version: 1
project: acme
gcp_project: acme-fugaro
```

That file is exactly the owner's "about two settings". Three cases need more:

- `git.base_branch:` when the branch is not `main` (decision L12);
- `profile: <name>` when the project has no `default_profile`, or the repository wants another profile;
- `git.provider:` when the project layer does not set it.

**Overriding one field:**

```yaml
version: 1
project: acme
gcp_project: acme-fugaro
workflows:
  api:
    profile: java-service
    commands: { test: ./gradlew test -x integrationTest }
    secrets: [{ name: db-password, env: DB_PASSWORD }]
```

**A profile's `base` is a string checked by today's rules.** It must be one of `config.Bases` in 0.6.0. Phase 1 adds nothing that depends on a particular base kind, and the profile syntax does not change when the base image consolidation replaces the kinds (§9).

**Not in 0.6.0:** a profile extending another (`extends` is reserved and refused with a message), and project-wide defaults for workflow keys outside profiles. Both are easy to add later; neither is needed for the owner's case.

## 7. The project layer object, publishing and reading

**Object.** `fugaro/project-layer.yaml`, next to `fugaro/config.yaml` and `fugaro/recipes/` (decision L5). It is not called `project.yaml` because `fugaro/project.json` is the Terraform-written project marker, and a near-twin name invites mistakes. It has a 64 KiB cap, and its shape is checked strictly with the shared config's rules:

- one document, holding a mapping;
- no anchors, aliases, merge keys, tags or repeated keys;
- no unknown or reserved keys;
- no key outside its scope;
- no credential-shaped value;
- `project` and `gcp_project` equal to what it is read for.

A JSON schema, `schemas/project-layer.schema.json`, describes it for editors.

**Publishing: `fugaro config publish FILE`** (decision L17, mirroring `fugaro recipes publish`). It:

1. Refuses inside a coding-agent session (`initflow.AgentRefusal`), before anything else.
2. Validates the file with the same `ParseProjectLayer`, anchored on the selected installation.
3. Supports only default-named runs buckets.
4. Shows a line diff against the published object.
5. Lists every profile whose executable keys change, under a banner, and refuses unless `--executable-changes` is given (decision L7).
6. Writes under a generation precondition, so a concurrent publisher is refused.
7. Copies the exact bytes to `builds/<slug>/project-layer.yaml` for each repository the installation config lists (decision L6). It reports per repository, and exits 2 if a copy failed.
8. Warns about the repositories whose job images predate 0.6.0 and so will be refused at launch (§10).

**Strict and lenient readers** (decision L16).
- **Strict.** Launches, cloud builds, `config show`, `config layer`, `config init` and `config publish` fail when the bucket cannot be read.
- **Lenient.** The commands that worked offline before 0.6.0 (`validate`, `doctor`, `init`'s repository and anchor stages, `secrets`, `image render`) never fail for that reason. With no project config selected they read only the cache. Otherwise a read failure leaves the layer unknown and says so. A file that needs the layer then gets a problem naming `--project-layer FILE`.
- **Both.** A present but invalid object fails every reader.

**Reading: every launch reads the bucket** (decision L13). Recipes and the shared config use a 24-hour fresh window. The project layer does not, because a stale copy is exactly the classic failure mode. The cache, `$XDG_CACHE_HOME/fugaro/project-layers/<project>.json`, is used only when the bucket cannot be reached at all, for up to 7 days, with a warning. A 403, a 5xx or a refusal never falls back to it. A present but invalid object fails the command; it never counts as "no layer".

**Commands.**

| Command | What it does |
|---|---|
| `fugaro config show [--workflow W] [--json]` | The resolved config of the checkout. Every value is printed with its source (`default`, `project`, `profile <name>`, `repo`), followed by the project layer's generation, sha256 and age, and the resolved config's sha256. `--json` is the hook for sub-project 2 (§12) |
| `fugaro config layer [--json]` | The published project layer: text, generation, sha256 (`doctor` reports a repository whose copy differs) |
| `fugaro config publish FILE [--executable-changes]` | As above |
| `fugaro config init [--profile P] [--base-branch B] [--yes]` | Writes the minimal file into a checkout that has none. It is non-interactive with `--yes`, and refuses to overwrite (§12) |
| `fugaro validate [--offline] [--project-layer FILE]` | Resolves exactly as the runner will when the project config is selected (otherwise from the cache, saying so). `--project-layer` checks a layer file before it is published, and lets CI without bucket access validate a minimal file |

## 8. How every consumer gets the same bytes

Each consumer below runs the same `config.Resolve` on the same two inputs. Each one records or compares `LayerSHA256` (decision L8).

| Consumer | How it gets the project layer |
|---|---|
| `fugaro run`, follow-ups | The CLI reads the layer and **embeds its exact text** in `task.json` as `project_layer: {sha256, generation, yaml}`. The runner never reads the bucket. It merges the embedded text with the `fugaro.yaml` it reads at the ref (first run) or at the base branch (follow-up) |
| `fugaro validate`, `config show`, `doctor`, `init --repo`, `init --anchor`, `secrets`, `image build --local`, `image render` | The CLI reads it, or uses `--project-layer FILE` |
| Cloud Build (`image build`, `init`'s image stage, `image refresh`, the check job's rebuilds) | The submitter makes sure `builds/<slug>/project-layer.yaml` holds the layer it resolved, then passes its sha256 as `_PROJECT_LAYER_SHA256`. The render step reads that object as the build account. It refuses a mismatch ("the project layer changed while the build was queued") and renders with the hidden `--layer-bucket`, `--layer-slug` and `--layer-sha256` flags. An empty substitution renders exactly as today, so an old base image is unaffected |
| The daily check job | Reads `builds/<slug>/project-layer.yaml` (its own prefix) and resolves `fugaro.yaml` against it. When it submits a rebuild, it passes the same sha256 |

**Why the layer text is embedded and not the merged config.** The runner reads `fugaro.yaml` at the task's ref, which the launching CLI may not have: `fugaro run --repo` outside a checkout, or a ref newer than the working tree. Embedding the input and running one pure function in both places keeps them equal. The run record keeps both sums:

- `project_layer: {sha256, generation, applied}`, which traces a run to the exact layer text;
- `config_sha256`, which traces it to the exact resolved config.

**Showing it.** `fugaro ls` adds a LAYER column once any listed run has `project_layer`: `gen N`, or `gen N (not applied)` for a run whose `fugaro.yaml` at the ref didn't name the layer's project and gcp_project, so it resolved without the layer (a launch from outside a checkout); a run with none shows `-`. `fugaro diagnose` adds a `Config:` line: `project layer generation N (sha256 <short>)`, `, not applied` when it wasn't, and `; resolved sha256 <short>` when the run also recorded one; a run with no layer but a resolved sum shows `no project layer; resolved sha256 <short>` instead. `--json` carries both fields on every row unredacted: `project_layer: {sha256, generation, applied}` and `config_sha256`. `result.json` itself is launcher-written and read with a plain unmarshal, not checked against a schema, so neither command trusts its own sha256 or generation at face value: a value that isn't a 64-character hex sha256, or a negative generation, shows as `(invalid)` or `-` in the text view rather than being sliced or printed as though it were real; `--json` still carries it unmodified, since a machine reader may want the raw value.

**Why a per-repository copy for the check job and Cloud Build (decision L6).** The alternatives were weighed as follows:

- **The check job's spec env (`FUGARO_CHECK_SPEC`).** Terraform renders it, so every publish would need a job update for each of 50 repositories, and the layer's 64 KiB is large for an env var.
- **A read grant on `fugaro/project-layer.yaml` for the build account.** This is new IAM, which the owner prefers to avoid.

A build without a layer never touches a copy. To retire a layer, publish one with no defaults and no profiles.

The copy uses the access the build account already has. Its trust level is that of the build record beside it: the build account can already push an image and write a record for its own repository, so a copy it could also write adds nothing. Publishing refreshes every copy. `image build`, `init --repo` and `image refresh` refresh their repository's copy, and `doctor` flags a copy that differs from the canonical object.

**How a project-layer change reaches images.** The check job resolves its config against the copy, and `ImageConfigHash` hashes the resolved workflow. So a profile change to `base` or `image.*` changes the hash, and the next daily check rebuilds, as it already does when a repository edits those keys. Commands, resources and timeouts do not change the image, so they need no rebuild; runs pick them up at once.

**The old-CLI gap (accepted, plainly stated).** A teammate on a CLI older than 0.6.0 does not know the layer, so it launches without embedding it. Two kinds of repository are affected differently:

- **A minimal or profile-using repository is safe.** Its file cannot be parsed by a pre-0.6.0 binary (no `workflows:`, or an unknown `profile:` key), so the old CLI refuses it.
- **An anchored repository with full workflows is not.** It runs without the project's `defaults:`. The run record then says `project_layer` is absent, and `doctor` flags it.

The rollout order (§10) is: upgrade every CLI before publishing. A stronger guard would bake the layer's sha into the job image at build time, so a runner could refuse a task without one. It is offered as a veto alternative of decision L14.

## 9. The image catalog (Phase 2, blocked on the base image consolidation)

**Why it waits.** The catalog's main value is a list of named environments such as `java-17` or `node-20`. Those exist cheaply only once a single base image installs runtimes from `mise.toml`. Today a different JDK needs a whole operator-built base: `image.jdk` is refused, and java-services pins JDK 25. The owner ruled a **hard cut**: the consolidation release removes `go`, `web-node` and `java-services`. The strict parser and the schema refuse them, and repositories move to the single base plus `mise.toml`, with no aliases and no overlap. The agent gets no passwordless sudo. Runtimes come from `mise install` at build time as the unprivileged user, and system packages from the project's Dockerfile layer as root, before hardening. So the catalog is designed here and built after the consolidation lands.

**The sequencing risk, stated plainly.** Repositories that adopt profiles in 0.6.0 name legacy kinds (`base: java-services`). At the hard cut every such value must change. Profiles are what make that cheap:

- **with profiles,** the operator edits a handful of profiles in one object and republishes, and 50 repositories follow on their next resolution;
- **without profiles,** 50 pull requests would each edit `fugaro.yaml`.

The repositories' minimal files never name a kind, so they do not change at all. This is a selling point of doing Phase 1 first.

After the cut, `fugaro validate` refuses a legacy kind with a message naming the migration document. That message, and the migration document itself, belong to the consolidation design. This design only reserves the hook: the problem's `Code` is `legacy_base`, so `config show` and the setup skill can point at the migration.

**Phase 2 design.**

- **Catalog entries: `environments:` in the project layer.** The key is reserved and refused in 0.6.0, so no unpublished format can leak out. An entry is a **named environment**:
  ```yaml
  environments:
    java-17:
      tools: { java: temurin-17, gradle: "8.10" }   # mise tool set
      apt: [graphviz]
      setup: ["./scripts/warm-gradle.sh"]
    node-20:
      tools: { node: "20" }
    legacy-oracle:
      image: us-east5-docker.pkg.dev/acme-fugaro/fugaro-base/oracle-jdk:2026-10-01@sha256:…   # operator-built
  ```
  An entry is either the single Fugaro base plus a mise tool set plus optional `apt`/`setup`, or a reference to a base image an operator built. An operator-built image must be inside the anchor's `fugaro-base` registry, match the strict reference grammar of `ParseShared`'s `baseImageRE`, and is never replaced silently: the custom-base rules of `init_images.go` and `image_refresh_rules.go` apply.
- **Choosing an environment.** A profile or workflow writes `base: java-17`. `base:` keeps its name and becomes "the single base's name, or a catalog name". It is resolved through the catalog, and the closed enum in Go and in the schema becomes a name pattern plus a resolution check. There is no new key and nothing for the consolidation to break.
- **The inline escape hatch stays.** It is `dockerfile:`, plus inline `image.apt` and `image.setup`.
- **`mise.toml` precedence.** A repository's own `mise.toml` (or `.tool-versions`) wins wholesale. The environment's `tools` apply only when the repository has none. There is no per-tool merge: two version sources for one runtime is the classic failure mode again. `fugaro config show` prints the resolved tool versions and which file or environment they came from.
- **Generalising the base-kind plumbing.** The local and shared configs' `base_images` stay a map. After the cut it holds one key, the single base's name, plus nothing for catalog entries: an operator-built environment carries its own reference in the project layer. The same applies to:
  - `image refresh`: copies the single base and skips operator-built environments with a note ("rebuild it and publish");
  - the check job's `CheckJobSpec.BaseImages`: keyed the same way;
  - the job image: always the single base.

  The 0.5.1 behaviour stays until the cut, because Phase 2 ships after it.
- **Rebuilds.** `ImageConfigHash` gains the resolved environment: tools, apt, setup and the image reference. A catalog edit therefore rebuilds every image whose environment changed at the next daily check. The base trigger watches an operator-built reference's digest like any base.
- **Adding an operator-built entry.** These are documented steps, not a new pipeline:
  1. Write a Dockerfile `FROM` the single base.
  2. Build and push it to `<region>-docker.pkg.dev/<gcp>/fugaro-base/<name>:<tag>` (`images/build-base.sh` shows the pattern; `docker buildx` for both architectures).
  3. Pin the digest.
  4. Add the entry and run `fugaro config publish`.
  5. `doctor` and the check job's base trigger take it from there.

## 10. Rollout and compatibility

**New keys, strictly refused by old binaries.** These are `profile:` (top level), `workflows.<n>.profile` and a file with no `workflows:`. The task field `project_layer` is new as well. This is the same caveat as `gcp_project` in 0.4.0, `agent.recipe` in 0.5.0 and `git.pr.checkpoints` in 0.5.1 (see [../release.md](../release.md) and [shared-config.md](shared-config.md) §9).

**The order, in the 0.6.0 Highlights and `docs/release.md`:**

1. Release 0.6.0.
2. Every teammate's CLI, and every CI job or pin that runs `fugaro`, moves to 0.6.0.
3. Per repository, run `fugaro image refresh`. This moves its job images and its check job to a 0.6.0 base.
4. Only then publish the project layer with `fugaro config publish`. It warns, listing repositories whose build records predate 0.6.0.
5. Only then merge a minimal or profile-using `fugaro.yaml`.

**The version gate, `layeredSince = "0.6.0"`.**

- A launch whose task carries a project layer is refused when the workflow's build record `base_ref` predates it. The message names `fugaro image refresh`. The check generalises `checkRecipeImage`.
- A Cloud Build submission with a layer is refused when its base predates 0.6.0.
- `fugaro validate` warns when the file uses a 0.6.0 key.

**What an old CLI sees** in a repository with a project layer:

- a minimal or profile-using file: refused as invalid, so there is no silent misrun;
- a full file: today's behaviour, without the project defaults (the gap of §8).

**Existing repositories keep working untouched.** A file without `gcp_project:` never sees the layer. An anchored file with full workflows that name no profile gets only the project's `defaults:`, and `config show` lists each of them as `project`.

**Migration of an existing repository.** Delete what the profile already says, and add `profile:` where needed. `fugaro config show` before and after must print the same values (the resolved sha256 is the quick check). A `fugaro config extract`, which would suggest a project layer from several repositories' files, is a nice-to-have and **not in 0.6.0** (decision L17).

## 11. Security and the threat model

**Who can write the project layer.** Anyone holding `roles/storage.objectAdmin` on the runs bucket: launchers and operators (`modules/installation/iam.tf`). The job and build accounts cannot write `fugaro/`. They write only under their slug, and the build account's copy is discussed in §8. That grant (`objectUser` on `builds/<slug>/`, decision L6) also means a later, repository-controlled build step (the Dockerfile's own `RUN` steps, or anything else the build runs with the build account's credentials) could rewrite the same copy before the next daily check job (Task 15) reads it. This stays confined to the one repository and the build secrets it already has access to: it cannot reach another repository's copy, the canonical object in `fugaro/`, or anything `objectUser` on its own prefix does not already allow.

**What a malicious or careless writer could do:**

1. **Replace build or test commands** for every repository that takes a profile. The commands run in the run's container with the repository's git credential and the workflow's secrets.
2. **Add `image.setup` or `image.apt`.** These run in Cloud Build, with the workflow's build secrets mounted, on the next daily rebuild. No human launches anything.
3. **Raise `resources` or `timeouts`,** which costs money.
4. **Change `agent.model`, `recipe` or `auth`.**
5. **Point `base` at another shipped kind.**
6. **Hide a change.** A plain bucket write leaves no review trail.

**What already bounds it.** A launcher can already launch any task text with the repository's credential, and the agent will run what it is asked. So item 1 adds no capability to a launcher. It adds stealth and reach: commands change under other people's runs, and item 2 needs no launch at all.

**Protections in 0.6.0:**

- **Publishing.** Only an operator's own terminal can publish: `config publish` refuses in an agent session. The write has a generation precondition, and a diff is shown first.
- **Bounded values.**
  - The policy ceiling is untouchable: dollars, tokens and allowed models still come from the owner, and only tighten.
  - The project layer cannot hold policy keys, `secrets`, `followup.*`, `base_branch`, `reviewers`, `dockerfile` or `instructions` (§5).
  - Base images stay `config.Bases` in Phase 1. In Phase 2, catalog references must sit inside the anchor's registry, under the custom-base rules.
- **Strict parsing.** The size cap and the credential check apply as described in §7.
- **Traceability.** Every run records the layer's sha256 and generation. `fugaro ls`, `diagnose` and `doctor` show it, and `doctor` flags drift.

**The open question: commands in a bucket object (decision L7).** The repository's `fugaro.yaml` is code-reviewed in a pull request; the bucket object is not. Recipes carry no commands. The options:

| Option | What it means | Cost |
|---|---|---|
| **A. Executable keys allowed, guarded (recommended)** | Profiles may set `commands.*`, `image.apt` and `image.setup`. `config publish` refuses a change to any of them without `--executable-changes`, printing each one before and after under a banner. `fugaro run` prints `commands: from profile <p> (project layer gen <n>)` before launching. The record keeps the sha. A follow-up hardening narrows the launchers' bucket grant so only operators can write `fugaro/` (an IAM change in the installation module, listed as future work) | The bucket stays writable by launchers until the hardening lands |
| B. No executable keys in the project layer | Profiles carry base, resources, timeouts, cache and rebuild; commands always live in the reviewed `fugaro.yaml` | The minimal file grows to about 7 lines: `workflows.default.commands.{build,test}` |
| C. Allowed, unguarded | Like A without the flag or the banner | The weakest |

The recommendation is A, with the IAM hardening as the next security task. B is the safe fallback if the owner prefers review over brevity. The code keeps B one switch away: `config.ExecutableKeys` already names the keys, and moving them out of `InProfile` in `config.Scopes` is the whole change.

## 12. Hooks for sub-project 2 (unattended adoption)

This design does not build `fugaro adopt`. A script adopting 50 repositories will need:

- `fugaro config init --yes [--profile P] [--base-branch B]`. A non-interactive, idempotent writer of the minimal file. It refuses to overwrite an existing `fugaro.yaml` and exits 0 when the file already says exactly that.
- `fugaro config show --json`: the resolved config and the source of every value, so a script can assert that a repository resolves as intended without parsing prose.
- `fugaro validate --json`, which resolves like the runner.
- Profiles chosen by name, never by prompt: no default profile and no `profile:` is an error, not a question.
- The open walls that are sub-project 2's to solve:
  - the first image build needs a typed confirmation;
  - `init --repo` runs Terraform per repository;
  - the setup skill is interactive by design.

  The setup skill writes the minimal file when the project has a layer (Phase 1 change); the investigative path stays for first-time projects.

## 13. Non-goals

- **Unattended adoption** (sub-project 2): a script-safe `fugaro adopt`, and the typed-confirmation wall of the first image build.
- **Lighter per-repository infrastructure** (sub-project 3). The findings so far:
  - each repository gets its own Terraform root with a registry, a build account, secrets, a check job and a scheduler job;
  - a shared registry and build account per project would remove most of that weight;
  - the root cause of the per-repository heaviness (the spec's open question) is mostly this per-repository Terraform plus the first build's confirmation.

  Not investigated further here.
- Per-person Claude tokens (§5).
- The image catalog's implementation (Phase 2, §9).
- `fugaro config extract`, profile inheritance (`extends`), and project-wide workflow defaults outside profiles.
- Narrowing the launchers' bucket grant (the hardening of §11, option A).

## 14. Testing

- **Golden resolution tables.** Each key of `config.Scopes` is tested in every layer it may live in, and refused in every layer it may not. Each merge rule of §4 gets a row. Each problem names its layer.
- **One function, every consumer.** A property-style test feeds the same repository file and layer through each consumer's entry point:
  - the CLI's checkout resolution;
  - the runner's bootstrap parse;
  - the Cloud Build render path (the copy, checked by its sum);
  - the check job's head-config read.

  It asserts one `ConfigSHA256`.
- **No layer, no change.** Every file of the existing corpus resolves with no layer exactly as `Parse` did.
- **Fakes only.** These cover publish, fetch, the cache, the copies, the gate and `doctor`:
  - `file://` and `mem://` buckets;
  - `gcpfake`;
  - the scripted agent;
  - local bare remotes.

  There is no live cloud and nothing touches EdgeWeb or EdgeServer.
- **Docs.** A test keeps the scope table of `docs/project-layer.md` equal to `config.Scopes`. The skills lint keeps the setup skill's commands real.

## 15. Docs

- New user doc: `docs/project-layer.md`. It covers the layers, the one rule, the scope table, the merge table, the commands and the rollout order. It is written in parallel to `docs/recipes.md`.
- Cross-references from `docs/design/shared-config.md`, `docs/recipes.md` and `docs/gcp-setup.md` (operator steps: publish after upgrading).
- `docs/release.md` gets the `layeredSince` line in "Before you tag".
- `fugaro config example` says the file may be minimal when the project has a layer.
- The setup skill writes the minimal file when the project has a layer.

## 16. Decisions

The plan lists them as L1 to L24, each with a veto alternative:

- L1 to L4 are the owner's D-a to D-d.
- L4 is narrowed to Phase 2 by the consolidation ruling.
- The ones this design adds are:
  - the object name;
  - the per-repository copy;
  - embedding the text, not the result;
  - anchoring as the opt-in;
  - the merge rules;
  - the syntax;
  - the minimal file;
  - reading on every launch;
  - the gate;
  - follow-ups;
  - the commands;
  - the credential check;
  - the implicit workflow's name;
  - commands in the bucket (option A).

## 17. Delivery

**Phase 1 is release 0.6.0,** in six PR groups:

1. The `internal/config` core: scopes, the layer parser, the keys and `Resolve`.
2. The task field and the runner.
3. The CLI resolution and commands.
4. Cloud Build and the check job.
5. Visibility: `doctor`, `ls` and `diagnose`.
6. Docs and the setup skill.

Then `/new-release 0.6.0`.

**Phase 2 is the catalog.** Its tasks are listed in the plan as BLOCKED until the consolidation release ships and its design settles the single base's name, the user and the pinning.
