# The project layer

Design: [design/layered-config.md](design/layered-config.md). Plan: [plans/2026-10-08-layered-config.md](plans/2026-10-08-layered-config.md). The runs-bucket write access this design assumes is narrowed in a later release: [design/bucket-iam.md](design/bucket-iam.md) (§8).

## 1. What it is

The project layer is one object per project, `fugaro/project-layer.yaml` in the runs bucket, holding project-wide `defaults:` and named workflow `profiles:` with a `default_profile`.

It applies to every repository whose `fugaro.yaml` names the project and its `gcp_project:`, and to no other. A project without one, and a repository without the line, behave as before 0.6.0.

## 2. The one rule

For each key, the narrowest layer that sets it wins: per-task flag, then repository, then the workflow's profile, then project defaults, then Fugaro default. The budget ceiling is the owner's, outside every layer, and only tightens. A project that has published a project policy ([design/project-centric.md](design/project-centric.md)) has no `budget.mode: off`: a run reads the policy from the project's database, so its jobs must carry the Firebase backend and the mode itself is policy.

A published policy is read by the run itself, not by the launcher: it lives at `config/policy/shared` in the project's Realtime Database, and the run reads it with its own Firebase identity before it starts. Each key in that node is then the ceiling **for that key**: a key the node does not set keeps the value the job's environment was deployed with, so a policy published only to set `budget.allowed_models` leaves the mode, the caps and the kill switches exactly as they were, and one that names a mode replaces it. `budget.*`, `model_prices`, `allow_repos` and `routing` take effect in this release. A run checks two things before any git credential exists and before anything is cloned: that the storage slug it was launched for (its `FUGARO_RUN`) is the slug of the repository its task names, and that a published `allow_repos` admits that repository. A slug that belongs to another repository stops the run, and so does a repository the list does not admit — a policy published with an empty `allow_repos` therefore admits nothing at all. That is not the same as an installation that has published no policy: with no node under the marker there is no allow-list, and only the slug binding is checked. A run with no Cloud Run execution — a local `fugaro exec --provider fake` — has no job identity to bind to, so neither check applies to it. `fugaro run` makes the same two checks before it creates an execution. A repository the list does not admit is refused with the run's own message (`projectpolicy.NotAdmitted`'s, one step earlier), while the slug binding and the release skew — a policy whose `version` is newer than the binary, which only this side can see — are worded by the launcher itself, the skew refusal quoting the release the policy model ships in. The launch-side checks are advisory, since a launcher may start a job directly, and the run's own check is the one that counts. `providers` is decoded, validated and accepted by a run, but never applied: a run routes by the copy in its own job's environment (§5). `routing` is **applied**: the run rewrites the Claude pins the published switch names from the canonical ID to the route's own ID, once, under the same gateway that routes and prices the call (`internal/runner/routing.go`, called from `resolvePolicy`) — so a policy published with `routing.claude` sends that project's Claude calls where the switch says, and `fugaro project policy publish` writes the block from the operator's own config (§7). §5's policy-key table says what each of them does in this release. The project layer's keys can only tighten whatever results. The node beside it, `config/limits/policyRules`, records which generation of the database's security rules is deployed, and that marker is what makes a refusal readable: before the owner runs `fugaro init --firebase` with a Fugaro that deploys the policy rules, the policy node is left unwritten — an administrator's IAM credentials can write it by hand, and `fugaro project policy publish` refuses until the marker is there, because a policy published before the rules is one the runs it is meant to bound cannot read — and a run whose job carries no backend behaves exactly as it did before the policy existed. Once the marker is deployed, a policy that cannot be read or decoded **stops the run** — it never falls back to the ceiling the job environment was deployed with, because that ceiling is the one the published policy was meant to replace. The halt names the one cause that is not tampering: an older CLI's `fugaro init --firebase` redeploys rules from before the policy nodes and leaves the marker in place, so a policy that was published becomes unreadable and the fix is to run `fugaro init --firebase` again with an up-to-date Fugaro.

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
| `agent.oauth_token` | repo, override | it names a credential identity that only the repository's own, PR-reviewed fugaro.yaml may choose among names the local config (never the project layer) allow-lists |
| `budget.mode` | repo | it is policy: the owner's ceiling lives in the installation config, and a repository can only tighten it |
| `budget.per_run_usd` | repo | it is policy: the owner's ceiling lives in the installation config, and a repository can only tighten it |
| `budget.allowed_models` | repo | it is policy: the owner's ceiling lives in the installation config, and a repository can only tighten it |
| `budget.per_day_usd` | repo | it is policy: the owner's ceiling lives in the installation config, and a repository can only tighten it |
| `workflows.*.profile` | repo | it chooses a profile |
| `workflows.*.base` | profile, repo |  |
| `workflows.*.checkout` | profile, repo |  |
| `workflows.*.image.node` | profile, repo |  |
| `workflows.*.image.jdk` | repo | it is refused on every base |
| `workflows.*.image.apt` | profile, repo |  |
| `workflows.*.image.setup` | profile, repo |  |
| `workflows.*.image.skip_build_scripts` | profile, repo |  |
| `workflows.*.image.tools` | profile, repo |  |
| `workflows.*.dockerfile` | repo | it names a file in the repository |
| `workflows.*.commands.build` | profile, repo |  |
| `workflows.*.commands.test` | profile, repo |  |
| `workflows.*.commands.lint` | profile, repo |  |
| `workflows.*.commands.fix` | profile, repo |  |
| `workflows.*.commands.render` | profile, repo |  |
| `workflows.*.commands.probe` | profile, repo |  |
| `workflows.*.commands.rerun_failed` | profile, repo |  |
| `workflows.*.commands.reports` | profile, repo |  |
| `workflows.*.cache` | profile, repo |  |
| `workflows.*.secrets` | repo | secrets belong to one repository's Secret Manager entries |
| `workflows.*.resources.cpu` | profile, repo |  |
| `workflows.*.resources.memory` | profile, repo |  |
| `workflows.*.timeouts.total` | profile, repo, override |  |
| `workflows.*.timeouts.stage` | profile, repo |  |
| `workflows.*.timeouts.verify` | profile, repo |  |
| `workflows.*.timeouts.render` | profile, repo |  |
| `workflows.*.timeouts.finalize_reserve` | profile, repo |  |
| `workflows.*.rebuild.check` | profile, repo |  |
| `workflows.*.rebuild.max_age` | profile, repo |  |
| `workflows.*.rebuild.lockfiles` | profile, repo |  |
| `workflows.*.rebuild.base` | profile, repo |  |
| `workflows.*.rebuild.paths` | profile, repo |  |
| `workflows.*.inputs.max_files` | profile, repo |  |
| `workflows.*.inputs.max_bytes` | profile, repo |  |
| `workflows.*.inputs.max_total_bytes` | profile, repo |  |
| `followup.trusted` | repo | it decides whose comments steer a run with the repository's credentials |
| `followup.allow_public` | repo | it decides whose comments steer a run with the repository's credentials |
<!-- scope-table:end -->

Outside this table: secrets' values live in Secret Manager and are never in any layer — and a secret name is stored **project-wide by default**: `fugaro secrets set NAME` with no flag writes the project's own container, whose reach is decided per name rather than by enrolment — the repositories the name's own `project_secrets.<name>.allow_data_to` list names, a provider key's provider's `allow_data_to`, or a named Claude account's own list (`infra.ProjectSecretCandidate` and `AllowsData`; SECURITY.md's "the reach is…"), each of them still bounded by what a published policy admits — until phase 3 gives the mounts to a profile, `--repo` is what asks for one repository's own copy instead, and a repository copy with an enabled version wins over the project's for that repository ([design/project-centric.md §6.3](design/project-centric.md); `fugaro doctor` lists which copy each name resolves to, and warns when a repository copy shadows the project's). A project-scope secret is not a layer and cannot be set in one — `workflows.*.secrets` stays a repository's own application secret, and a name with a project-wide meaning cannot be shared through a workflow's block; per-person Claude tokens are a later project; the individual layer is flags and env only. `claude_models` (§6) has no row here on purpose: it is a top-level key of the layer file, not a `fugaro.yaml` key at all, so it exists in no repository's file to be scoped — a repository cannot write it even in principle, which is the whole point of the key (§8).

The published policy's own keys are outside this table too — they are set in the node (`config/policy/shared`) and in no layer, and the node *is* the ceiling for the keys it names (§2). What a run does with each of them in this release:

| Policy key | What the run does with it today |
|---|---|
| `version` | must be the version this Fugaro reads; anything else stops the run (release skew, P8). `fugaro run` refuses the launch before it creates an execution, naming the release the policy model ships in |
| `budget.mode`, `budget.per_run_usd`, `budget.max_run_tokens`, `budget.allowed_models` | applied: the ceiling for the keys the node names, the environment's for the rest |
| `model_prices` | applied: the price table the run's caps are computed with. Its member names are **keyed by the model ID escaped** (RTDB refuses a key holding `.` or `/`, and the documented `deepseek/deepseek-v4-flash` carries one), and every reader decodes them back — the operator reads model IDs in `policy show`, the diff and `--json`, and a run prices the model it calls |
| `allow_repos` | applied: the run refuses a repository the list does not admit (and a slug that is not the task's own repository), before any git credential exists; `fugaro run` refuses an unadmitted repository earlier, with the run's own message |
| `providers` | decoded, validated and accepted, **not applied**: a run routes by the providers its own job carries (`FUGARO_MODEL_PROVIDERS`), which is what the switch resolution and the prices use — the node's block is what the switch is validated against when it is published and read, and what a later release will apply |
| `routing` | **applied**: with the gateway on, the run rewrites the Claude pins the switch names — `routing.claude.models` is the opt-in list (D1a) — from the canonical ID (`claude-sonnet-5-5`) to the route's own ID (`anthropic/claude-sonnet-5.5`), so the pin the agent is given, the route the gateway sends the call to and the row it is charged from all name one model. A switch this run cannot resolve (a model no job route claims, a route whose `allow_data_to` is not written, two routes claiming one model) **halts the run** rather than falling back to the direct path, and `via: auto` is skipped for a Vertex run, which serves Claude under the project's own credential. With the gateway off nothing changes: the pins keep Claude's IDs and the switch has nothing to act on |

A key in the `providers` row is not an unknown key: the CLI knows it and does not refuse it the way it refuses a misspelt one (§2, [design/project-centric.md](design/project-centric.md)). The two rows differ for a reason worth stating: `providers` has no effect on a run, `routing` decides where that run's Claude calls go and what they cost, so publishing a switch is a live change to every repository of the project.

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
    recipe: standard
profiles:
  java-service:
    description: Gradle service with Postgres
    image: { tools: { java: "temurin-17" } }
    commands:
      build: ./gradlew assemble
      test: ./gradlew test
      reports: ["**/build/test-results/**/*.xml"]
    cache:
      - { key: [gradle/libs.versions.toml], paths: [~/.gradle/caches] }
    resources: { cpu: 4, memory: 16Gi }
    timeouts: { total: 2h }
  node-web:
    image: { tools: { node: "22" } }
    commands: { build: npm run build, test: npm test }
default_profile: java-service
# An explicit Claude model ID a route the installation config already
# defines also serves (mixed-credentials.md §4): provider name -> one
# "anthropic/<id>" per line, never a pattern.
claude_models:
  openrouter: ["anthropic/claude-sonnet-5.5"]
```

`claude_models` is the one top-level key (`version`, `project`, `gcp_project`, `defaults`, `profiles`, `default_profile`, `claude_models`) that adds to the **local** config rather than to a repository's file: it names a route only the owner defines and says which Claude model that route also serves, so a senior review can be routed to it. It may only add an explicit `anthropic/<id>` to a provider the installation config already lists — never create a provider, never change its `base_url`, `auth`, `secret` or `allow_data_to`, never name a class of models. An entry is appended to that route's own `models`, so it is held to the route-entry rule as well: at most 100 characters, the same bound a route entry carries (`config.CheckModelPattern`), because the gateway applies that rule to the merged table and a longer ID would otherwise leave it refusing to start every run whose job carries the route. The refusal for a claim the installation config cannot take is at the surfaces that hold that whole config: `fugaro config publish` refuses to publish the object at all when a name is not defined, a list is empty, or an entry overlaps another route's pattern, and `fugaro validate` refuses the checkout for the same reasons. A run cannot make that check — its route table is the job's own `FUGARO_MODEL_PROVIDERS`, written per repository with only the providers that list that repository in `allow_data_to` — so a claim naming a provider this repository's job does not carry is a route **this repository does not have**: the claim is dropped for that run, and only a pin on the claimed ID is refused, as a model no provider serves. That is deliberate, because the layer is installation-wide: one claim naming a provider the owner did not list for every repository must not fail the bootstrap of repositories that never name the claimed model, let alone those that do not use a provider at all. The price is not the layer's to set: it comes from the operator's `model_prices`, and never from the built-in catalog — the catalog prices only the models a *route* lists, and the layer's claim is in no route the deployed job carries (below) — so a claimed ID with no `model_prices` row fails the run as unpriced, and the layer cannot make a call cheaper than the ceiling says. The claim reaches a run through the layer (it is never written into the deployed job's `FUGARO_MODEL_PROVIDERS`), so removing it from the layer puts the refusal back on the next launch.

Reserved keys, refused with a message: `environments` (the image catalog, Phase 2, blocked on the base image consolidation) and `extends` (profiles do not extend each other in this release). The object has a 64 KiB limit.

Its shape is checked strictly, with the shared config's trust rules: one document, holding a mapping; no anchors, aliases, merge keys, tags or repeated keys; no unknown or reserved keys; no key outside its scope (§5); no credential-shaped value (an Anthropic, OpenRouter, GitHub, Slack, Google API, Bitbucket or AWS key, a PEM private key, or a service-account key file — as a key or a value, anywhere, including inside a comment); and `project` and `gcp_project` equal to what it is read for.

## 7. Commands

| Command | What it does |
|---|---|
| `fugaro config show [--workflow W] [--json] [--project-layer FILE] [--offline]` | The resolved config of the checkout. Every value is printed with its source (`default`, `project`, `profile <name>`, `repo`), the project layer's generation and sha256, and the resolved config's sha256 |
| `fugaro config layer [--json]` | The published project layer: text, generation, sha256 |
| `fugaro config publish FILE [--executable-changes]` | Publishes the project layer (below) |
| `fugaro config init [--profile P] [--base-branch B] [--yes]` | Writes the minimal file into a checkout that has none |
| `fugaro validate [--json] [--offline] [--project-layer FILE]` | Resolves exactly as the runner will when the project config is selected (otherwise from the cache, saying so). `--project-layer` checks a layer file before it is published, and lets CI without bucket access validate a minimal file |
| `fugaro project policy publish [--seed-repos] [--delete] [--yes] [--remove-routing]` | Builds the project's policy from this machine's project config — the `budget` block, the providers (their secrets by **name**, never a value), `model_prices`, the allow-list and the `routing` block — and writes it to `config/policy/shared` with an ETag-guarded write, printing what changes first. It refuses without the rules marker and refuses a node written by a newer Fugaro. `allow_repos` is seeded from the operator's `repos:` and the shared config's list, and only when the node is absent or `--seed-repos` is passed, so a later publish never re-adds a name an operator removed. `--delete` removes the policy — the `config/policy/repos/*` children first and the shared node last, so a launch mid-delete still sees the policy and keeps its tighter ceiling instead of briefly falling back to the looser one its job's environment carries — and removes the children whether or not the shared node is still there, because a delete that failed on the node leaves them behind and "nothing to delete" on the strength of an absent node would leave that policy text forever; it goes through the same rules-marker gate as a publish, and refuses without `--yes` while runs are in flight. `routing` is written where the operator writes it — this config — and because a published switch is applied by every run of the project (§5), a publish refuses to **drop** an active route the config does not account for, and refuses a config that writes a switch in the same state: the block is kept as the node has it, or rewritten from the config, and nothing else. `--remove-routing` is how the operator says the block goes away **on purpose**, for a node whose switch is not in this config; without it a config silent about Claude keeps the node's switch rather than turning it off, which is why the two together (a config that writes a switch *and* `--remove-routing`) are refused |
| `fugaro project policy show [--json]` | The published policy: its version, its budget block, its allow-list, its providers with each secret's **name**, `require_zdr` and `price_policy`, `model_prices` one line per model under the ID the operator priced it by with the rates it carries, and the `routing` block when the node carries one (a run applies it, §5, and `fugaro project policy publish` writes it from the operator's config) |
| `fugaro project show [--json]` | The effective project: the caps the installation's nodes carry (global, defaults, per repository), the policy, the allow-list with where each entry came from, and the repositories **running now** — the run registry holds the runs in flight, so that list is what is running at this moment, never a history — then the two differences an operator needs, repositories running that the allow-list does not admit and allow-list entries that are not running now (the registry cannot tell "not now" from "never", and the output says so; phase 4's discovery surface is where a durable answer comes from) |
| `fugaro project repos ls` | The allow-list with each entry's source, and the repositories running now |
| `fugaro project repos add <provider/owner/name \| owner/name \| *>` | Enrolls a repository: adds one canonical entry to the published `allow_repos` and nothing else. A bare `owner/name` takes its provider from this machine's project config; `*` is the list's own every-repository entry (the design's explicit choice), written as it is |
| `fugaro project repos rm <provider/owner/name \| owner/name \| *>` | Un-enrolls a repository: removes one entry. Removing an allowed repository stops its runs, so it asks for the project's name — and `--yes` does not stand in for that while a run is in flight. Removing `*` refuses every repository the rest of the list does not admit, so it asks the same way as any other removal |

`fugaro run` prints `project layer: <project> generation <n> (sha256 <sha>)` before launching, when one applies; and, for every profile that supplied one of the launched workflow's `commands.*` keys, `commands: from profile <name> (<keys>; project layer generation <n>)`.

`fugaro doctor` adds, inside a checkout whose project publishes a project layer: which layer applies (or why none does); a warning when the repository's last run launched without the layer or on an older generation; a warning when the repository's per-repository copy (the one Cloud Build and the daily image check read) differs from the published object; and, per workflow, a warning when its job image was built from image settings other than what it resolves to now.

It also reports the project's policy, for any installation with a budget backend (nothing here changes anything: doctor only reads). An info line says whether a policy is published and which version — and it reads the node the way a run does, so a node the deployed rules do not carry the marker for is reported as written but governing nothing (with `fugaro init --firebase` as the way to make it govern) — or that there is none, or that the installation has no backend to publish one to. Warnings follow for the eight states that need the operator: a policy **newer than this Fugaro** (upgrade), a policy **older than this Fugaro** (a node a person or a release before this shape wrote: publishing refuses to overwrite it, so remove it with `fugaro project policy publish --delete` and publish again), an **empty `allow_repos`** in a published policy (which is deny-all, not "no policy": enroll a repository), **a repository this machine's config names that the allow-list does not admit** (the state `fugaro project repos rm` leaves behind, which refuses that repository's runs before they mint a git token: `fugaro project repos add` enrolls it), the installation's **jobs still on an older runner image** (a policy published now is one those runs do not read — `fugaro image refresh`, then `fugaro init --repo .`), the **deployed rules older than the marker** `config/limits/policyRules` (an older CLI's `fugaro init --firebase` redeployed the pre-policy template while the marker stayed, so a published policy is unreadable and every budget-on run halts: re-run `fugaro init --firebase` with a current Fugaro), **a rules marker below the generation this Fugaro reads a policy under** (it records rules that predate the policy nodes while claiming a generation, so no run reads a policy and `fugaro project policy publish` refuses: the same `fugaro init --firebase`, which records this release's generation), and the opposite skew, **deployed rules newer than this Fugaro** (the marker records a generation above this binary's, so it cannot read a policy written under them — and here `fugaro init --firebase` is the wrong move, because this release's template would be redeployed over newer rules: `fugaro upgrade` first, and the deploy refuses it rather than relying on the operator's reading: a marker above the binary's generation stops `fugaro init --firebase` before anything is planned, so an older binary cannot downgrade a newer installation's rules or rewrite the marker down).

`fugaro runs ls` adds a LAYER column once any listed run carries a project layer: `gen N`, `gen N (not applied)` for a run that resolved without it (a launch from outside a checkout whose `fugaro.yaml` at the ref didn't name the layer's project and `gcp_project`), or `-` for a run with none. `fugaro diagnose` adds a `Config:` line: `project layer generation N (sha256 <short>)`, with `, not applied` and `; resolved sha256 <short>` appended when they apply; a run with no layer but a resolved sum shows `no project layer; resolved sha256 <short>` instead.

## 8. Publishing safely

**`--executable-changes` is a CLI check, not an access control.** It only gates `fugaro config publish`; whoever can write the bucket object directly (for example with `gcloud storage cp`) bypasses the guard below and the sha256 trail. Since the 0.7.0 bucket IAM hardening ([design/bucket-iam.md](design/bucket-iam.md)), that is operators only (and, for its own copy, a repository's own build account): a launcher's direct write is refused with a 403. An installation whose operator has not yet run `fugaro init` to apply the 0.7.0 grants still has every launcher holding `roles/storage.objectAdmin`, with the old, wider bypass, until they do.

`fugaro config publish` refuses in a coding-agent session, before anything else. That is what "only from your own terminal" means here. Since 0.7.0 it is also operator-only in effect: a launcher's own terminal gets the operator-role refusal ([gcp-setup.md](gcp-setup.md)), the same as any other write to `fugaro/`.

**`--executable-changes`.** A profile may set the keys of `config.ExecutableKeys`:

<!-- executable-keys:start -->
- `workflows.*.commands.build`
- `workflows.*.commands.test`
- `workflows.*.commands.lint`
- `workflows.*.commands.fix`
- `workflows.*.commands.render`
- `workflows.*.commands.probe`
- `workflows.*.commands.rerun_failed`
- `workflows.*.image.apt`
- `workflows.*.image.setup`
- `workflows.*.image.tools`
- `workflows.*.image.skip_build_scripts`
<!-- executable-keys:end -->

— values that run as shell, or (`image.skip_build_scripts`) control whether shell a profile already describes runs, in the job or in the image build. `commands.render` and `commands.probe` are gated like `commands.build`: an artifact recipe's render and probe steps run the workflow's own text as shell in the job. `commands.reports` is not gated: it only lists paths to collect, and runs nothing. `inputs.*` is not gated either: the caps are numbers, and they bound what a launcher may attach, not anything a profile runs. `config publish` refuses a change to any of these keys, in either direction (`image.skip_build_scripts` going from `false` to `true` is reported too, not only the unsafe direction), unless `--executable-changes` is given, printing each one's before and after under an `EXECUTABLE CHANGES` banner first; changing `default_profile` itself is gated the same way, whenever either the old or the new default profile sets one of these keys.

**The write.** It is made under a generation precondition, so a concurrent publisher is refused rather than overwritten, and a diff against the previous object is shown first. The exact bytes are then copied to `builds/<slug>/project-layer.yaml` for each repository the installation config lists. `fugaro doctor` reports a repository whose copy differs from the published object.

**Residual caveats, now that Task 14 and Task 15 have both shipped.** Neither Cloud Build's render step nor the daily check job verifies this copy's sha256 against the canonical `fugaro/project-layer.yaml` — neither account can read that object at all. Cloud Build's render step only refuses a copy whose sha256 changed between submission and render (`_PROJECT_LAYER_SHA256`); the daily check job verifies nothing, trusting the copy as published, the same trust its neighboring build record already carries (design §8, §11's residual risk: a repository-controlled build step holding `objectUser` on its own `builds/<slug>/` could rewrite its own copy, confined to that one repository and the build secrets it already has access to).

**The threat model (design §11):**

- **Who can write.** Operators, since the 0.7.0 bucket IAM hardening. An installation whose operator has not yet run `fugaro init` to apply the 0.7.0 grants still has every launcher holding `roles/storage.objectAdmin` too (see the lead note above).
- **What a hostile or careless layer could do.** Replace build or test commands for every repository that takes a profile: this already takes effect, through the embedded layer every launch carries, with no human reviewing the change first. Since Task 14 and Task 15 shipped, add `image.apt`, `image.setup` or `image.tools` (as dangerous as `image.setup`: it names tools installed into the image) or turn off `image.skip_build_scripts`, which then runs in Cloud Build, including on the nightly rebuild, with no human launching anything. `commands.lint` and `commands.fix` are executable keys too, like `commands.build` and `commands.test`, and so are `commands.render` and `commands.probe` since 0.8.0. Also: raise `resources` or `timeouts`; change `agent.model`, `recipe` or `auth`; point `base` at another shipped kind; or hide any of this, since a plain bucket write leaves no review trail. And, since the claim shipped, add one explicit Claude model ID to a route the installation config already defines (`claude_models`, §6), which sends that route's model to the provider the owner set up under the owner's own terms — nothing more: the layer cannot create a route, change a route's `base_url`, `auth`, `secret` or `allow_data_to`, name a provider the operator did not define, claim a class of Claude models, or set a price.
- **What already bounds it.** The project layer cannot hold policy keys, `secrets`, `followup.*`, `base_branch`, `reviewers`, `dockerfile` or `instructions` (§5); the policy ceiling (dollars, tokens, allowed models) still comes from the owner and only tightens; base images stay `config.Bases`; and every run records the layer's sha256 and generation, which `fugaro runs ls`, `diagnose` and `doctor` show. A `claude_models` claim (§6) is bounded by the route it names: the traffic still goes to a provider the owner defined, under that provider's `allow_data_to` (so a repository the owner did not list for it never gets that route in its job at all — the claim is dropped for that repository's run, and a pin on the claimed ID is refused as a model no provider serves, never sent), the claim is one explicit ID rather than a class, the run needs `agent.auth: api-key` and `budget.mode: enforce` like any other provider model, and the price is the owner's `model_prices`', never the layer's and never the built-in catalog's.
- **The fix, shipped in 0.7.0.** [design/bucket-iam.md](design/bucket-iam.md) narrows the bucket grant so only operators (and, for its own copy, a repository's own build account) can write `fugaro/` and `builds/<slug>/`. Each installation's operator applies it by running `fugaro init`; until they do, the bucket stays writable by every launcher (see the lead note above).

## 9. Rollout (0.6.0)

The order:

1. Release 0.6.0.
2. Every teammate's CLI, and every CI job or pin that runs `fugaro`, moves to 0.6.0.
3. Per repository, run `fugaro image refresh`. This moves its job images and its check job to a 0.6.0 base. Before a project layer is published there is nothing for it to copy: it writes no `builds/<slug>/project-layer.yaml` (decision L6, a build without a layer never touches a copy), so this step is safe to run against every repository regardless of whether its project has published one yet.
4. Only then publish the project layer with `fugaro config publish`. It warns, listing repositories whose build records predate 0.6.0.
5. Only then merge a minimal or profile-using `fugaro.yaml`.

What an older CLI does, in a repository with a project layer: a minimal or profile-using file is refused as invalid, so there is no silent misrun; a full file runs with today's behaviour, without the project defaults.

Step 5 is safe once Task 14 and Task 15 have shipped (§8): the project's `defaults:` and a profile's `commands.*`, `resources` and `timeouts` all take effect at once, through the layer every launch embeds, and a profile's `image.apt`, `image.setup` and `image.skip_build_scripts` reach every image a build produces — a human-triggered `image build`/`image refresh` and the unattended nightly rebuild alike.

## 10. Not yet

- **The 0.7.0 bucket IAM hardening** ([design/bucket-iam.md](design/bucket-iam.md)): narrows who can write `fugaro/project-layer.yaml` and `builds/<slug>/project-layer.yaml` from every launcher to operators only (§8). Until it ships, the residual risk in §8's "Residual caveats" stands: a repository-controlled build step can rewrite its own `builds/<slug>/` copy, and neither Cloud Build's render step nor the daily check job can tell it from a legitimate one.
- The image catalog (`environments:`): Phase 2, blocked on the base image consolidation — see [design/layered-config.md §9](design/layered-config.md#9-the-image-catalog-phase-2-blocked-on-the-base-image-consolidation).
- `extends` (a profile extending another).
- `fugaro config extract` (suggesting a project layer from several repositories' files).
- **The rest of `fugaro project`** (phase 4's `project sync` and discovery surface, [plans/2026-10-14-project-centric.md](plans/2026-10-14-project-centric.md)): the commands that write and read `config/policy/shared` ship in this release (§7), `fugaro doctor` reports the policy's state and `upgrade` says that a job's environment copy of the ceiling is inert for the keys a policy governs (§7), but `fugaro project repos ls` prints the allow-list and the repositories running now — the run registry's runs in flight, which is what the launcher's nodes hold today — without the fuller discovery answer phase 4 adds.
- **The published policy's `providers` block** (phase 1b of [plans/2026-10-14-project-centric.md](plans/2026-10-14-project-centric.md)): the node carries it and a run decodes, validates and accepts it, but nothing applies it — a run routes by the providers its own job carries, so the block is what a published switch is validated against when it is written and read, not what a run routes by. §5's policy-key table says what each key does in this release. (`routing` is no longer one of these: a run applies the published switch, §5. `allow_repos` stopped being one of these earlier: a run and `fugaro run` both apply it, §5.)
- Per-person Claude tokens.
