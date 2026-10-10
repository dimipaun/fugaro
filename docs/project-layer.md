# The project layer

Design: [design/layered-config.md](design/layered-config.md). Plan: [plans/2026-10-08-layered-config.md](plans/2026-10-08-layered-config.md).

## 1. What it is

The project layer is one object per project, `fugaro/project-layer.yaml` in the runs bucket, holding project-wide `defaults:` and named workflow `profiles:` with a `default_profile`.

It applies to every repository whose `fugaro.yaml` names the project and its `gcp_project:`, and to no other. A project without one, and a repository without the line, behave as before 0.6.0.

## 2. The one rule

For each key, the narrowest layer that sets it wins: per-task flag, then repository, then the workflow's profile, then project defaults, then Fugaro default. The budget ceiling is the owner's, outside every layer, and only tightens.

A workflow takes a profile only when it names one (`workflows.<name>.profile`), or when the file has no `workflows:` at all (then one workflow, `default`, takes `profile:` or the project's `default_profile`). A workflow that names no profile takes nothing from any profile.

## 3. The minimal fugaro.yaml

```yaml
version: 1
project: acme
gcp_project: acme-fugaro
```

Three cases need more:

- `git.base_branch:` when the branch is not `main`;
- `profile: <name>` when the project has no `default_profile`, or the repository wants another profile;
- `git.provider:` when the project layer does not set it.

`fugaro config init --yes` writes this file for you, in a checkout whose project publishes a project layer (`--profile` and `--base-branch` cover the first two cases; it refuses to overwrite an existing `fugaro.yaml`).

## 4. Overriding a profile

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

The merge rules (design §4):

| Value | Rule |
|---|---|
| map (`git`, `git.pr`, `agent`, `agent.models`, a workflow, `image`, `commands`, `rerun_failed`, `resources`, `timeouts`, `rebuild`) | merged key by key, recursively |
| scalar (string, number, bool, duration) | the narrower layer's value replaces the wider one when the key is present, even as `""`, `0` or `false` |
| list (`git.pr.labels`, `image.apt`, `image.setup`, `commands.reports`, `cache`, `rebuild.paths`, and any repo-only list) | replaced whole, **never appended**. To add to a profile's list, repeat it; `fugaro config show` prints the list that results |
| `null` or an empty value (`key:`) | "not set here": the wider layer's value stays. This is today's meaning of null |
| `[]` | an empty list replaces the wider one, which clears it |

Two exceptions: a workflow that sets `dockerfile:` drops its profile's `image:` block (a repository with bespoke needs writes its own Dockerfile, and `image` and `dockerfile` are mutually exclusive today); and top-level `profile:` applies only to a file with no `workflows:` — with `workflows:`, each workflow names its own profile.

## 5. Where each key may be set

Each key may be set only in the layers listed; the project layer refuses the rest, naming the key:

<!-- scope-table:start -->
| Key | May be set in | Why |
|---|---|---|
| `version` | repo | it is the repository's own |
| `project` | repo | it identifies the repository and anchors the project layer |
| `gcp_project` | repo | it identifies the repository and anchors the project layer |
| `profile` | repo | it chooses a profile; the project chooses its default with default_profile |
| `git.provider` | project, repo |  |
| `git.base_branch` | repo | a project-wide base branch would let a bucket writer point image builds at an unreviewed branch |
| `git.pr.labels` | project, repo |  |
| `git.pr.reviewers` | repo | reviewers are people of one repository |
| `git.pr.early_draft` | project, repo |  |
| `git.pr.checkpoints` | project, repo |  |
| `agent.auth` | project, repo |  |
| `agent.model` | project, repo, override |  |
| `agent.models.coder` | project, repo |  |
| `agent.models.reviewer` | project, repo |  |
| `agent.models.background` | project, repo |  |
| `agent.review_rounds` | project, repo, override |  |
| `agent.max_budget_usd` | project, repo, override |  |
| `agent.instructions` | repo | it names a file in the repository |
| `agent.review` | repo | it names a file in the repository |
| `agent.max_output_tokens.coder` | repo | it is policy: the owner's ceiling lives in the installation config, and a repository can only tighten it |
| `agent.max_output_tokens.reviewer` | repo | it is policy: the owner's ceiling lives in the installation config, and a repository can only tighten it |
| `agent.max_run_tokens` | repo | it is policy: the owner's ceiling lives in the installation config, and a repository can only tighten it |
| `agent.first_line_review` | project, repo |  |
| `agent.first_line_rounds` | project, repo |  |
| `agent.recipe` | project, repo, override |  |
| `budget.mode` | repo | it is policy: the owner's ceiling lives in the installation config, and a repository can only tighten it |
| `budget.per_run_usd` | repo | it is policy: the owner's ceiling lives in the installation config, and a repository can only tighten it |
| `budget.allowed_models` | repo | it is policy: the owner's ceiling lives in the installation config, and a repository can only tighten it |
| `budget.per_day_usd` | repo | it is policy: the owner's ceiling lives in the installation config, and a repository can only tighten it |
| `workflows.*.profile` | repo | it chooses a profile |
| `workflows.*.base` | profile, repo |  |
| `workflows.*.image.node` | profile, repo |  |
| `workflows.*.image.jdk` | repo | it is refused on every base |
| `workflows.*.image.apt` | profile, repo |  |
| `workflows.*.image.setup` | profile, repo |  |
| `workflows.*.image.skip_build_scripts` | profile, repo |  |
| `workflows.*.dockerfile` | repo | it names a file in the repository |
| `workflows.*.commands.build` | profile, repo |  |
| `workflows.*.commands.test` | profile, repo |  |
| `workflows.*.commands.rerun_failed` | profile, repo |  |
| `workflows.*.commands.reports` | profile, repo |  |
| `workflows.*.cache` | profile, repo |  |
| `workflows.*.secrets` | repo | secrets belong to one repository's Secret Manager entries |
| `workflows.*.resources.cpu` | profile, repo |  |
| `workflows.*.resources.memory` | profile, repo |  |
| `workflows.*.timeouts.total` | profile, repo, override |  |
| `workflows.*.timeouts.stage` | profile, repo |  |
| `workflows.*.timeouts.verify` | profile, repo |  |
| `workflows.*.timeouts.finalize_reserve` | profile, repo |  |
| `workflows.*.rebuild.check` | profile, repo |  |
| `workflows.*.rebuild.max_age` | profile, repo |  |
| `workflows.*.rebuild.lockfiles` | profile, repo |  |
| `workflows.*.rebuild.base` | profile, repo |  |
| `workflows.*.rebuild.paths` | profile, repo |  |
| `followup.trusted` | repo | it decides whose comments steer a run with the repository's credentials |
| `followup.allow_public` | repo | it decides whose comments steer a run with the repository's credentials |
<!-- scope-table:end -->

Outside this table: secrets' values live in Secret Manager and are never in any layer; per-person Claude tokens are a later project; the individual layer is flags and env only.

## 6. The project layer file

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

Reserved keys, refused with a message: `environments` (the image catalog, Phase 2, blocked on the base image consolidation) and `extends` (profiles do not extend each other in this release). The object has a 64 KiB limit.

Its shape is checked strictly, with the shared config's trust rules: one document, holding a mapping; no anchors, aliases, merge keys, tags or repeated keys; no unknown or reserved keys; no key outside its scope (§5); no credential-shaped value (an Anthropic, OpenRouter, GitHub, Slack, Google API, Bitbucket or AWS key, a PEM private key, or a service-account key file — as a key or a value, anywhere, including inside a comment); and `project` and `gcp_project` equal to what it is read for.

## 7. Commands

| Command | What it does |
|---|---|
| `fugaro config show [--workflow W] [--json] [--project-layer FILE] [--offline]` | The resolved config of the checkout. Every value is printed with its source (`default`, `project`, `profile <name>`, `repo`), the project layer's generation and sha256, and the resolved config's sha256 |
| `fugaro config layer [--json]` | The published project layer: text, generation, sha256 |
| `fugaro config publish FILE [--executable-changes]` | Publishes the project layer (below) |
| `fugaro config init [--profile P] [--base-branch B] [--yes]` | Writes the minimal file into a checkout that has none |
| `fugaro validate [--offline] [--project-layer FILE]` | Resolves exactly as the runner will when the project config is selected (otherwise from the cache, saying so). `--project-layer` checks a layer file before it is published, and lets CI without bucket access validate a minimal file |

`fugaro run` prints `project layer: <project> generation <n> (sha256 <sha>)` before launching, when one applies; and, for every profile that supplied one of the launched workflow's `commands.*` keys, `commands: from profile <name> (<keys>; project layer generation <n>)`.

`fugaro doctor` adds, inside a checkout whose project publishes a project layer: which layer applies (or why none does); a warning when the repository's last run launched without the layer or on an older generation; a warning when the repository's per-repository copy (the one Cloud Build and the daily image check read) differs from the published object; and, per workflow, a warning when its job image was built from image settings other than what it resolves to now.

## 8. Publishing safely

Only an operator's own terminal can publish: `fugaro config publish` refuses in a coding-agent session, before anything else.

**`--executable-changes`.** A profile may set `commands.*`, `image.apt` and `image.setup` — values that run as shell, in the job or in Cloud Build. `config publish` refuses a change to any of them unless `--executable-changes` is given, printing each one's before and after under an `EXECUTABLE CHANGES` banner first.

**The write.** It is made under a generation precondition, so a concurrent publisher is refused rather than overwritten, and a diff against the previous object is shown first. The exact bytes are then copied to `builds/<slug>/project-layer.yaml` for each repository the installation config lists — the per-repository copy that Cloud Build's render step and the daily check job read, since neither holds a grant on `fugaro/project-layer.yaml` itself. `fugaro doctor` reports a repository whose copy differs from the published object.

**The threat model (design §11):**

- **Who can write.** Anyone holding `roles/storage.objectAdmin` on the runs bucket — today that is every launcher and every operator, not operators alone.
- **What a hostile or careless layer could do.** Replace build or test commands for every repository that takes a profile; add `image.apt` or `image.setup`, which run in Cloud Build on the next daily rebuild with no human launching anything; raise `resources` or `timeouts`; change `agent.model`, `recipe` or `auth`; point `base` at another shipped kind; or hide any of this, since a plain bucket write leaves no review trail.
- **What already bounds it.** The project layer cannot hold policy keys, `secrets`, `followup.*`, `base_branch`, `reviewers`, `dockerfile` or `instructions` (§5); the policy ceiling (dollars, tokens, allowed models) still comes from the owner and only tightens; base images stay `config.Bases`; and every run records the layer's sha256 and generation, which `fugaro ls`, `diagnose` and `doctor` show.
- **Recommended future work.** Narrowing the launchers' bucket grant so only operators can write `fugaro/` (an IAM change in the installation module) is not done in 0.6.0: the bucket stays writable by launchers until that hardening lands.

## 9. Rollout (0.6.0)

The order:

1. Release 0.6.0.
2. Every teammate's CLI, and every CI job or pin that runs `fugaro`, moves to 0.6.0.
3. Per repository, run `fugaro image refresh`. This moves its job images and its check job to a 0.6.0 base.
4. Only then publish the project layer with `fugaro config publish`. It warns, listing repositories whose build records predate 0.6.0.
5. Only then merge a minimal or profile-using `fugaro.yaml`.

What an older CLI does, in a repository with a project layer: a minimal or profile-using file is refused as invalid, so there is no silent misrun; a full file runs with today's behaviour, without the project defaults.

## 10. Not yet

- The image catalog (`environments:`): Phase 2, blocked on the base image consolidation — see [design/layered-config.md §9](design/layered-config.md#9-the-image-catalog-phase-2-blocked-on-the-base-image-consolidation).
- `extends` (a profile extending another).
- `fugaro config extract` (suggesting a project layer from several repositories' files).
- Per-person Claude tokens.
