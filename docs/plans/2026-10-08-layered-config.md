# Layered Project Configuration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Most configuration moves to the project. A project publishes one **project layer** (`fugaro/project-layer.yaml` in its runs bucket), which holds:
- project-wide `defaults:` for git and agent keys;
- named workflow `profiles:`, one of them the `default_profile`.

An anchored repository needs only a three-line `fugaro.yaml` (`version`, `project`, `gcp_project`). It may name a profile, or override one field of a profile. One pure function, `config.Resolve`, merges the layers with one rule: the narrowest layer that sets a key wins. The CLI, the runner, Cloud Build and the daily check job all call it on the same bytes, and every run records the project layer's and the resolved config's sha256. A repository with no project layer, or no `gcp_project:`, behaves exactly as in 0.5.1.

**Architecture:**
- **`internal/config` (pure).**
  - `Scopes`: the table of which key may live in which layer.
  - `ParseProjectLayer`: strict, with the shared config's trust rules.
  - `Resolve(repo, layer)`: a merge of plain YAML maps with a source map, then the strict decode, `applyDefaults` and `Validate`.
  - `Config.SHA256`.

  `Parse(data)` becomes `Resolve(data, nil)`.
- **The CLI.**
  - It reads the layer from the bucket on every launch, with a cache only for an unreachable bucket (`findLayer`).
  - It embeds the layer's text in `task.json` (`task.ProjectLayer`).
  - It resolves every in-checkout command through one seam, `resolveFugaroYAML`.
  - It adds `fugaro config show|layer|publish|init`, `validate --project-layer|--offline` and the `layeredSince` image gate.
- **The runner.** It merges the embedded layer with the `fugaro.yaml` it reads at the ref or the base, and records `project_layer` and `config_sha256`.
- **Cloud Build and the check job.** They read a per-repository copy, `builds/<slug>/project-layer.yaml`, in the build account's own prefix. The copy is written by `config publish`, `image build`, `init --repo` and `image refresh`, and pinned by `_PROJECT_LAYER_SHA256`.
- **Visibility.** `doctor`, `ls` and `diagnose` show the layer and flag drift.

**Tech Stack:**
- Go 1.27, `gopkg.in/yaml.v3`, cobra;
- `github.com/santhosh-tekuri/jsonschema/v6` for the schema tests;
- gocloud `blob` through `internal/blobx`;
- the existing fakes: `file://` and `mem://` buckets, `gcpfake`, the scripted runner agent (`harness`), and local bare remotes (`testutil.NewRemote`).

**Spec:** [docs/design/layered-config.md](../design/layered-config.md)

**Checked before review:** the code of Tasks 1 to 17 was applied to a scratch copy of `main` (616eb52), and `go vet` is clean.
- **Focused tests.** Every focused test the tasks name passes.
- **Full suites:**
  - `go test ./internal/cli/` (13 minutes; it needs `-timeout 30m`);
  - `internal/config`, `schemas`, `internal/task`, `internal/runstore`, `internal/localcfg`, `internal/runview`, `images`, `internal/backend/gcp` and `plugin`, with `-race` where the tasks say so;
  - `go test -race ./internal/runner/ -run 'TestRecipe|TestFollowUp|TestRunner'`.
- **Not run:** the full runner suite.
- **What the full CLI run changed.** Its first pass found three things, and Tasks 9, 10, 11 and 14 carry the fixes:
  - offline commands reached the bucket, hence the lenient mode of decision L16;
  - every image build touched a copy;
  - every launch printed a "none applies" note.
- **The docs.** Task 18's scope table was checked by its test against a draft `docs/project-layer.md`. Tasks 18 to 20 are otherwise docs and were not applied.

**Phases.**
- **Phase 1** is release **0.6.0**: Tasks 1 to 20, in PR groups 1 to 6.
- **Phase 2**, the image catalog, is **BLOCKED** on the base image consolidation release. Tasks P2-1 to P2-6 are listed at the end, outside every Phase 1 PR group. Their TDD steps are written once the consolidation design fixes the single base's name, its user and its pinning, because their signatures depend on those.

## Decisions (veto any before execution starts)

L1 to L4 are the owner's D-a to D-d; L4 is narrowed by the consolidation ruling. L5 to L24 are this plan's.

- **L1. The project layer lives in the runs bucket, resolved by the CLI and embedded (owner D-a).**
  - The object is `fugaro/project-layer.yaml`.
  - It is published by `fugaro config publish`.
  - It is strictly parsed (`config.ParseProjectLayer`) with the shared config's trust rules.
  - It is read through a cache.

  The runner never reads the bucket. Veto alternative: none offered (owner's ruling).
- **L2. Named profiles plus a project default (owner D-b).**
  - **Order.** For any value: per-task override, then repository, then profile, then project defaults, then Fugaro default. The policy ceiling keeps its tighter-only merge and is no layer's to loosen.
  - **No layer, no change.** A repository with no layer behaves exactly as today: `TestResolveWithoutLayerIsParse` runs the whole existing corpus through `Resolve(data, nil)`.
- **L3. Per-setting scopes, enforced now (owner D-c).**
  - The table is `config.Scopes` (design §5).
  - `ParseProjectLayer` refuses out-of-scope keys, naming the key, its layers and why.
  - Repo-only: `secrets`, `followup.*`, `git.pr.reviewers`, `agent.instructions`, `agent.review`, `dockerfile`, `git.base_branch`, and every policy key (`budget.*`, `agent.max_run_tokens`, `agent.max_output_tokens.*`).
  - Per-person Claude tokens are out of scope.
- **L4. The image catalog is Phase 2, blocked on the consolidation (owner D-d, narrowed by the hard-cut ruling).**
  - 0.6.0 reserves `environments:` in the project layer and refuses it with a message.
  - A profile's `base` is a string checked by today's rules (`config.Bases`).
  - Design §9 describes the catalog in full; Tasks P2-1 to P2-6 are blocked.
- **L5. Names.**
  - The object is `fugaro/project-layer.yaml`, not `fugaro/project.yaml`, which would sit beside the Terraform marker `fugaro/project.json`.
  - The per-repository copy is `builds/<slug>/project-layer.yaml` (`config.LayerCopyKey`).
  - The cache is `$XDG_CACHE_HOME/fugaro/project-layers/<project>.json`.
  - The implicit workflow is `default` (`config.ImplicitWorkflow`).
- **L6. The check job and Cloud Build read a per-repository copy, with no new IAM.**
  - **Writers.** `config publish` copies the exact bytes to every repository listed in the installation config. `image build`, `init`'s image stage, `image refresh` and `init --repo` refresh their repository's copy before they use it.
  - **Cloud Build.** The render step gets `_PROJECT_LAYER_SHA256` and refuses a copy whose sha differs.
  - **The check job.** It reads the copy from its own prefix (the build account's `builds/<slug>/`).
  - **Trust.** That of the build record beside it: the build account can already push an image and write that record.

  A build without a layer never touches a copy. To retire a layer, publish one with no defaults and no profiles; `doctor` reports any copy that differs from the published object.

  Rejected: the copy in `FUGARO_CHECK_SPEC` (Terraform-rendered; one job update per repository per publish; 64 KiB in an env var), and a read grant on `fugaro/project-layer.yaml` (new IAM). Veto alternative: the IAM grant (one `google_storage_bucket_iam_member` condition per build account, and no copies).
- **L7. Commands in the bucket: option A, guarded (needs the owner's explicit answer).**
  - **Allowed.** Profiles may set `commands.*`, `image.apt` and `image.setup` (`config.ExecutableKeys`).
  - **The publish guard.** `config publish` refuses a change to any of them unless `--executable-changes` is given, printing each before and after under a banner.
  - **The launch line.** `fugaro run` prints `commands: from profile <p> (project layer gen <n>)` when the workflow's commands came from a profile.
  - **The follow-up hardening.** Narrowing the launchers' bucket grant so only operators can write `fugaro/` is a separate IAM change.

  Veto alternative B: executable keys are repo-only. Remove `InProfile` from the five `ExecutableKeys` rows of `config.Scopes`, and the minimal file grows by `workflows.default.commands.{build,test}`. Veto alternative C: allowed with no flag.
- **L8. The task embeds the layer's text, not the merged config.** `task.ProjectLayer{SHA256, Generation, YAML}`. The runner merges it with the `fugaro.yaml` it reads at the ref (first run) or the base (follow-up), with the same `Resolve`, because the CLI may have no checkout (`run --repo`) or a different tree. The run record gets:
  - `project_layer: {sha256, generation, applied}`;
  - `config_sha256` (the resolved config before per-task overrides).
- **L9. Anchoring is the opt-in.** The layer applies to a `fugaro.yaml` whose `gcp_project:` and `project:` equal the layer's own, and only for a default-named runs bucket.
  - **Inside a checkout,** the CLI embeds the layer only for an anchored file.
  - **Outside one** (`run --repo`), it embeds the installation's layer when one exists. The runner applies it only to an anchored file at the ref, and records `applied: false` otherwise.
  - **What applies to whom.** `defaults:` apply to every anchored repository. A profile applies only to a workflow that names it (`workflows.<n>.profile`), or to the implicit workflow of a file with no `workflows:`.
- **L10. Merge rules (design §4).**
  - Maps merge key by key.
  - Scalars and lists are replaced whole. There is no appending anywhere.
  - `null` or an empty value means "not set here", and `[]` clears.
  - A repository `dockerfile:` drops the profile's `image:`.
  - A top-level `profile:` is an error beside `workflows:`.
- **L11. Syntax.** `profile:` (top level, the implicit workflow's) and `workflows.<n>.profile`. Profile names have the project-name shape. `extends` is reserved.
- **L12. The minimal file** is `version`, `project` and `gcp_project`. Three cases need more:
  - `git.base_branch` when the branch is not `main`;
  - `profile:` when the project has no `default_profile`;
  - `git.provider` when the project does not set it.

  The project layer may set `git.provider`; it may not set `git.base_branch` (L3).
- **L13. Every read goes to the bucket when it can (L16 says when a lenient command may not).** There is no 24-hour fresh window, unlike recipes and the shared config, because a stale layer is the classic failure mode.
  - The cache answers only when the bucket cannot be reached at all (`isUnreachable`), for up to 7 days, with a warning.
  - A 403, a 5xx or a refusal never falls back.
  - A present but invalid object fails the command.
  - An absent object drops the cache entry.
- **L14. The version gate, `layeredSince = "0.6.0"`, and the old-CLI gap.**
  - **Launches.** One whose task carries a layer is refused when the workflow's build record `base_ref` predates 0.6.0. This is `checkRecipeImage` generalised to `checkImageSince`.
  - **Builds.** A Cloud Build submission with a layer is refused when its base predates 0.6.0.
  - **`config publish`** warns, listing the repositories whose records predate 0.6.0.
  - **The gap.** A pre-0.6.0 CLI launching an anchored full-workflow repository runs it without the project defaults. A minimal or profile-using file is refused by old binaries. The rollout order (CLIs first) and `doctor`'s "runs without the project layer" line cover the gap.

  Veto alternative: bake the layer's sha into the job image, so a runner can refuse a task without one (one more task in PR group 4).
- **L15. Follow-ups use the current project layer.** They already re-read the base branch's `fugaro.yaml`, so they resolve against today's project, not the previous run's.
- **L16. Strict where it runs or builds, lenient where it only reads.** Every in-checkout command resolves through one seam, in one of two modes.
  - **Strict:** `fugaro run`, the cloud image build, `init`'s builds, `config show`, `config layer`, `config init` and `config publish`. A bucket that cannot be read fails the command; a launch never goes ahead without the layer it should carry.
  - **Lenient** (`layerOptions.Lenient`): `validate`, `doctor`, `init`'s repository and anchor stages, `secrets` and `image render`. These are the commands that worked offline before 0.6.0, and `TestOfflineCommandsNeverFetchTheSharedConfig` keeps them that way.
    - With no project config selected, they read only the cache.
    - A bucket that cannot be read leaves the layer unknown, with a note saying it was not checked. A file that needs the layer then gets a problem naming `--project-layer FILE`.
    - An invalid published object still fails them.

  `fugaro validate` therefore resolves exactly as the runner will whenever the project config is selected, and says so when it could not. `--offline` forces the cache, and `--project-layer FILE` validates the checkout against a layer file and also checks the file. That covers checking a layer before publishing it, and CI without bucket access. Veto alternative: `validate` always reads the bucket, reversing shared-config.md §7's "validate stays offline".
- **L17. Commands.** These are `fugaro config show [--workflow] [--json]`, `fugaro config layer [--json]`, `fugaro config publish FILE [--executable-changes]` and `fugaro config init [--profile] [--base-branch] [--yes]`. There is no `fugaro config extract` in 0.6.0.
- **L18. Credential-shaped values are refused in the project layer:** `sk-ant-…`, `ghp_…`, `github_pat_…`, `gh[osu]_…`, `xox?-…`, `AIza…`, `ATBB…` and PEM private keys (`config.tokenRE`).
- **L19. `config publish` fans out.**
  1. It writes the canonical object under a generation precondition.
  2. It copies the same bytes to each listed repository's `builds/<slug>/project-layer.yaml`, reporting each.
  3. It exits 2 if any copy failed; the canonical object stays written and `doctor` flags the stale copy.
- **L20. Visibility.**
  - **`doctor`** gains four checks:
    - `project-layer` (info: generation, sha, age, or "none");
    - `project-layer-drift` (warning: the repository's last run used another sha, or none);
    - `project-layer-copy` (warning: the repository's copy differs from the canonical object);
    - `image-config` (warning: a workflow's resolved image settings hash differs from its build record's).
  - **`ls`** shows a `LAYER` column (generation) when any run has one.
  - **`diagnose`** prints `Config:` with both sums.
- **L21. The run record.**
  - `project_layer: {sha256, generation, applied}` and `config_sha256` are added to `result.json` and its schema.
  - A record before 0.6.0 has neither.
  - A task whose layer is invalid ends the run at bootstrap as `infra_error` naming the project layer, like an invalid `fugaro.yaml`.
- **L22. Reserved keys.** `environments` (Phase 2) and `extends` in the project layer, each refused with a message.
- **L23. Problems name the layer.** A problem with a value a profile or the project set ends with `(set by profile <p>)` or `(set by project)`. A missing one in a profiled workflow ends with `(set neither by the repository nor by profile <p>)`.
- **L24. Release 0.6.0,** a minor release with a new format. `docs/releases/v0.6.0.md` carries the rollout order. The `/new-release` operator list gains "upgrade CLIs, then `image refresh`, then `config publish`".

## Global Constraints

Every task's requirements include these.

From the design:
- **One function.** Every consumer resolves through `config.Resolve` with the same repository bytes and the same layer bytes. No consumer re-implements a merge, and no consumer reads the canonical object except the CLI.
- **No layer, no change.** `Resolve(data, nil)` must equal 0.5.1's `Parse(data)` for every file without a `profile` key (`TestResolveWithoutLayerIsParse`).
- **Fail closed.** The project layer is untrusted input. Refuse, never repair. An invalid object fails the command; it never counts as absent. Error messages quote hostile text with `%q` or `pluginwire.Printable`.
- **The ceiling.** It is untouched: `runner/policy.go` and `config.PolicyOf` do not change, and the project layer cannot carry a policy key.
- **Not doing in Phase 1:** the image catalog, `extends`, `config extract`, per-person tokens, `fugaro adopt`, and the IAM narrowing of launchers.

Project rules:
- No live cloud: no `fugaro init` against a real project, no real bucket writes, no Cloud Build submissions. Use `file://` and `mem://` buckets, `gcpfake`, local bare remotes, the scripted agent and the fake Cloud Build seam. Never touch EdgeWeb or EdgeServer, and handle no real secrets.
- Subagent-driven development, one git worktree per PR group (`.worktrees/layered-config-<n>`), a fresh implementer per task. Dogfood runs, if used, also run from git worktrees. The token-economy rule applies:
  - **Task 4 (`Resolve`) and Task 13 (`config publish`) get their own review,** the second with a security reviewer, since they are the merge and the trust boundary;
  - the rest are reviewed once per PR group.
- Every CI check (`test`, `terraform`, `rules`) is read before merge: each job's log, not only the summary.
- Docs must match behaviour. These must stay green:
  - the `rules` tests: `plugin/*_test.go` lint, `schemas/schemas_test.go`, the config corpus, `TestExampleIsValid`;
  - Task 18's `TestScopeTableMatchesDocs`.
- A `fugaro.yaml` key needs three edits: the Go struct (`internal/config/config.go`), `Validate` (`internal/config/validate.go`) and `schemas/fugaro.schema.json` (Task 3).
- Releases go through `/new-release` with `docs/releases/vX.Y.Z.md` merged first.
- **The `internal/cli` and `internal/runner` suites are slow** (runner about 19 minutes, CLI about 13; run the full CLI suite with `-timeout 30m`). Each task runs only its focused tests (`-run`), in the foreground, with `-race` as CI does. Each PR group runs its full package suites once at the end.

## Review Focus

The five failure modes most likely to hit a user, each pinned by a test:

1. **A value set at project level that a repository silently fails to pick up (the owner's classic failure).** Expected:
   - every launch reads the current object, not a day-old cache;
   - `config show` names the source of every value;
   - a run records the layer it used;
   - `doctor` flags a repository whose last run used another layer, or none.

   Pinned by:
   - `TestFindLayerReadsTheBucketEveryTime` and `TestFindLayerCacheOnlyWhenUnreachable` (Task 9);
   - `TestConfigShowNamesEverySource` (Task 12);
   - `TestRunnerRecordsTheLayer` (Task 7);
   - `TestDoctorFlagsLayerDrift` (Task 16);
   - `TestResolveTable`'s "a workflow without profile: takes nothing" row (Task 4), which documents the one place a profile does not apply.
2. **The CLI, the runner, Cloud Build and the check job resolve different configs.** Expected: one `ConfigSHA256` for one repository file and one layer, whichever consumer resolves it. Pinned by `TestEveryConsumerResolvesTheSameBytes` (Task 15), which drives the CLI seam (`resolveFugaroYAML`), the task's embedded text through `config.Resolve`, the Cloud Build render path (`readLayerCopy` and `loadCheckoutResolved`) and the check job's `readHeadConfig` over `jobLayerOptions`. `TestRunnerRecordsTheLayer` (Task 7) pins the runner's `resolveConfig` to the same `config.Resolve`, and `TestResolveIsDeterministic` (Task 4) covers repeated resolutions.
3. **A repository is launched or rebuilt on an image older than 0.6.0 after the layer is published.** Expected: a refusal naming `fugaro image refresh`, before any cloud write. Pinned by `TestRunRefusesLayerOnOldImage` (Task 11) and `TestImageBuildRefusesOldBaseWithLayer` (Task 14).
4. **A hostile or broken project layer.** Expected:
   - publish refuses out-of-scope keys, credentials and an unflagged executable change, and loses no race;
   - a broken published object stops launches with the object named, never "no layer".

   Pinned by:
   - `TestParseProjectLayerRefuses` (Task 2);
   - `TestPublishRefusesExecutableChangeWithoutFlag`, `TestPublishRefusesConcurrentPublisher` and `TestPublishRefusedInAgentSession` (Task 13);
   - `TestFindLayerRefusesAnInvalidObject` (Task 9).
5. **An existing repository changes behaviour, or a minimal file resolves to something unexpected.** Expected:
   - no change without `gcp_project:` or without a layer;
   - a minimal file gets exactly the default profile;
   - every problem says which layer set the value.

   Pinned by:
   - `TestResolveWithoutLayerIsParse`, `TestResolveMinimalTakesTheDefaultProfile` and `TestResolveProblemsNameTheLayer` (Task 4);
   - `TestUnanchoredCheckoutIgnoresTheLayer` (Task 9);
   - `TestRunnerIgnoresTheLayerForAnUnanchoredFile` (Task 7).

---

## File Structure

| Path | Responsibility | Task |
|---|---|---|
| `internal/config/scope.go` (new), `internal/config/scope_test.go` (new) | `Scope`, `Scopes`, `ScopeOf`, `ExecutableKeys` | 1 |
| `internal/config/layer.go` (new), `internal/config/layer_test.go` (new) | `ProjectLayer`, `ParseProjectLayer`, `LayerKey`, `LayerCopyKey`, `LayerSum` | 2 |
| `internal/config/config.go`, `validate.go`, `schemas/fugaro.schema.json`, `internal/config/config_test.go` | `profile` and `workflows.<n>.profile` | 3 |
| `internal/config/resolve.go` (new), `config.go`, `internal/config/resolve_test.go` (new) | `Resolve`, `Resolution`, `Config.SHA256`; `Parse` = `Resolve(data, nil)` | 4 |
| `schemas/project-layer.schema.json` (new), `schemas/schemas_test.go`, `testdata/project-layer/**` (new), `testdata/config/layered/**` (new) | schema and corpora | 5 |
| `internal/task/task.go`, `schemas/task.schema.json`, `internal/task/task_test.go` | `task.ProjectLayer` | 6 |
| `internal/runner/runner.go`, `followup.go`, `internal/runstore/runstore.go`, `schemas/result.schema.json`, `internal/runner/layer_test.go` (new) | resolution at bootstrap, the record | 7 |
| `internal/localcfg/layercache.go` (new), `layercache_test.go` (new) | the offline cache | 8 |
| `internal/cli/layer_resolve.go` (new), `layer_resolve_test.go` (new), `layer_helpers_test.go` (new) | `findLayer`, `resolveFugaroYAML` | 9 |
| `internal/cli/validate.go`, `image.go`, `cloud.go`, `doctor.go`, `init_repo_target.go`, `init_anchor.go`, `initsecrets.go`, `layer_sites_test.go` (new) | every in-checkout parse through the seam | 10 |
| `internal/cli/run.go`, `recipes_skew.go`, `run_layer.go` (new), `run_layer_test.go` (new), `layer_helpers_test.go`, `docs/release.md` | embedding, the gate, the launch lines | 11 |
| `internal/cli/configcmd.go`, `config_show.go` (new), `config_init.go` (new), `config_cmd_test.go` (new), `internal/config/config.go` (`Duration.MarshalYAML`) | `config show`, `config layer`, `config init` | 12 |
| `internal/cli/config_publish.go` (new), `layer_copy.go` (new), `config_publish_test.go` (new) | `config publish` and the fan-out | 13 |
| `images/derived/cloudbuild.yaml`, `images/cloudbuild_test.go`, `internal/backend/gcp/build.go`, `build_test.go`, `internal/cli/image.go`, `internal/cli/init.go`, `internal/cli/image_layer.go` (new), `image_layer_test.go` (new) | Cloud Build | 14 |
| `internal/cli/imagecheck.go`, `imagecheck_layer_test.go` (new), `consumers_test.go` (new) | the check job (`readHeadConfig`, `jobLayerOptions`); the one-sum property test | 15 |
| `internal/cli/doctor_layer.go` (new), `doctor.go`, `doctor_layer_test.go` (new) | `doctor` | 16 |
| `internal/runview/runview.go`, `internal/cli/ls.go`, `diagnose.go`, `ls_layer_test.go` (new) | `ls`, `diagnose` | 17 |
| `docs/project-layer.md` (new), `docs/design/shared-config.md`, `docs/recipes.md`, `docs/gcp-setup.md`, `internal/config/example.yaml`, `README.md`, `internal/config/docs_scope_test.go` (new) | docs | 18 |
| `plugin/skills/setup/SKILL.md`, `plugin/skills/setup/reference/decisions.md`, `plugin/setup_skill_test.go` | the setup skill | 19 |
| `docs/releases/v0.6.0.md` (new) | release | 20 |

## PR groups

Each group is one worktree branch, and a task is sized for one dogfood run. The order of the groups is the dependency order.

1. **PR 1: config core** (Tasks 1 to 5). Tasks 1 and 3 are independent and may run in parallel. Task 2 needs Task 1. Task 4 needs Tasks 2 and 3. Task 5 needs Task 4. This group is the parse seam everything else uses. Nothing reads a project layer yet, and `Parse` behaves as before except for the new keys.
2. **PR 2: task and runner** (Tasks 6 and 7, in order). Needs PR 1. A runner that understands `project_layer` must be on `main` before any CLI embeds one.
3. **PR 3: CLI resolution and commands** (Tasks 8 to 13). Needs PR 1. Merge after PR 2. Task 8 is independent of everything in PR 3. Task 9 needs Task 8. Tasks 10, 11, 12 and 13 need Task 9 and are independent of each other, so they run in parallel.
4. **PR 4: Cloud Build and the check job** (Tasks 14 and 15, in order). Needs PR 3's Task 9 (and Task 13's copy writer, `writeLayerCopy`, which Task 14 creates if Task 13 has not merged; the two define it identically).
5. **PR 5: visibility** (Tasks 16 and 17, independent). Needs PRs 2 and 3.
6. **PR 6: docs and the setup skill** (Tasks 18 and 19). Needs PRs 3 and 5, because the skills lint checks every command and flag named.
7. **Release** (Task 20), after all six are merged.

**Phase 2** (Tasks P2-1 to P2-6) is BLOCKED on the base image consolidation release and is in no Phase 1 group.

---
## PR 1: config core

### Task 1: The scope table (`internal/config/scope.go`)

**Files:**
- Create: `internal/config/scope.go`
- Test: `internal/config/scope_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  ```go
  type Scope uint8
  const ( InProject Scope = 1 << iota; InProfile; InRepo; InOverride )
  type ScopeRow struct { Key string; In Scope; Why string }
  var Scopes []ScopeRow
  var ExecutableKeys []string
  func ScopeOf(path string) (ScopeRow, bool)
  func (s Scope) String() string
  func starWorkflow(path string) string      // "workflows.web.x" -> "workflows.*.x"
  func scopeRow(key string) (ScopeRow, bool) // exact key
  func isBlock(path string) bool             // "agent", "workflows.*.image"
  ```

- [ ] **Step 1: Write the failing test**

Create `internal/config/scope_test.go`:

```go
package config

import "testing"

func TestScopeOf(t *testing.T) {
	for path, want := range map[string]Scope{
		"workflows.web.commands.test":                 InProfile | InRepo,
		"workflows.web.commands.rerun_failed.command": InProfile | InRepo,
		"workflows.web.secrets":                       InRepo,
		"git.provider":                                InProject | InRepo,
		"agent.model":                                 InProject | InRepo | InOverride,
	} {
		row, ok := ScopeOf(path)
		if !ok || row.In != want {
			t.Errorf("ScopeOf(%s) = %v %v, want %v", path, row.In, ok, want)
		}
	}
	if _, ok := ScopeOf("agent.colour"); ok {
		t.Error("agent.colour has a scope")
	}
	if s := (InProject | InRepo).String(); s != "project, repo" {
		t.Errorf("String = %q", s)
	}
}
```

- [ ] **Step 2: Run it and see it fail**

Run: `go test ./internal/config/ -run TestScopeOf`
Expected: FAIL to compile: `undefined: Scope`, `undefined: ScopeOf`, `undefined: InProject`.

- [ ] **Step 3: Implement**

Create `internal/config/scope.go`:

```go
package config

import "strings"

// Scope is the set of layers a fugaro.yaml key may be set in
// (docs/design/layered-config.md §5). The Fugaro default is not a scope:
// applyDefaults fills what no layer set.
type Scope uint8

// The layers a key may be set in, narrowest last.
const (
	InProject  Scope = 1 << iota // the project layer's defaults:
	InProfile                    // a profile of the project layer
	InRepo                       // the repository's fugaro.yaml
	InOverride                   // a task flag (--model, --review-rounds, ...)
)

// ScopeRow is one key and where it may be set. Key is a fugaro.yaml path;
// "*" stands for a workflow name. Why is said when a layer sets the key
// outside its scope.
type ScopeRow struct {
	Key string
	In  Scope
	Why string
}

const (
	whyAnchor    = "it identifies the repository and anchors the project layer"
	whyBranch    = "a project-wide base branch would let a bucket writer point image builds at an unreviewed branch"
	whyPeople    = "reviewers are people of one repository"
	whyFiles     = "it names a file in the repository"
	whyPolicy    = "it is policy: the owner's ceiling lives in the installation config, and a repository can only tighten it"
	whySecrets   = "secrets belong to one repository's Secret Manager entries"
	whyFollowup  = "it decides whose comments steer a run with the repository's credentials"
	whyRepoShape = "it is the repository's own"
)

// Scopes is every fugaro.yaml key and the layers it may be set in. A key
// not listed under a block is unknown. docs/project-layer.md prints this
// table; TestScopeTableMatchesDocs keeps the two equal.
var Scopes = []ScopeRow{
	{Key: "version", In: InRepo, Why: whyRepoShape},
	{Key: "project", In: InRepo, Why: whyAnchor},
	{Key: "gcp_project", In: InRepo, Why: whyAnchor},
	{Key: "profile", In: InRepo, Why: "it chooses a profile; the project chooses its default with default_profile"},
	{Key: "git.provider", In: InProject | InRepo},
	{Key: "git.base_branch", In: InRepo, Why: whyBranch},
	{Key: "git.pr.labels", In: InProject | InRepo},
	{Key: "git.pr.reviewers", In: InRepo, Why: whyPeople},
	{Key: "git.pr.early_draft", In: InProject | InRepo},
	{Key: "git.pr.checkpoints", In: InProject | InRepo},
	{Key: "agent.auth", In: InProject | InRepo},
	{Key: "agent.model", In: InProject | InRepo | InOverride},
	{Key: "agent.models.coder", In: InProject | InRepo},
	{Key: "agent.models.reviewer", In: InProject | InRepo},
	{Key: "agent.models.background", In: InProject | InRepo},
	{Key: "agent.review_rounds", In: InProject | InRepo | InOverride},
	{Key: "agent.max_budget_usd", In: InProject | InRepo | InOverride},
	{Key: "agent.instructions", In: InRepo, Why: whyFiles},
	{Key: "agent.review", In: InRepo, Why: whyFiles},
	{Key: "agent.max_output_tokens.coder", In: InRepo, Why: whyPolicy},
	{Key: "agent.max_output_tokens.reviewer", In: InRepo, Why: whyPolicy},
	{Key: "agent.max_run_tokens", In: InRepo, Why: whyPolicy},
	{Key: "agent.first_line_review", In: InProject | InRepo},
	{Key: "agent.first_line_rounds", In: InProject | InRepo},
	{Key: "agent.recipe", In: InProject | InRepo | InOverride},
	{Key: "budget.mode", In: InRepo, Why: whyPolicy},
	{Key: "budget.per_run_usd", In: InRepo, Why: whyPolicy},
	{Key: "budget.allowed_models", In: InRepo, Why: whyPolicy},
	{Key: "budget.per_day_usd", In: InRepo, Why: whyPolicy},
	{Key: "workflows.*.profile", In: InRepo, Why: "it chooses a profile"},
	{Key: "workflows.*.base", In: InProfile | InRepo},
	{Key: "workflows.*.image.node", In: InProfile | InRepo},
	{Key: "workflows.*.image.jdk", In: InRepo, Why: "it is refused on every base"},
	{Key: "workflows.*.image.apt", In: InProfile | InRepo},
	{Key: "workflows.*.image.setup", In: InProfile | InRepo},
	{Key: "workflows.*.image.skip_build_scripts", In: InProfile | InRepo},
	{Key: "workflows.*.dockerfile", In: InRepo, Why: whyFiles},
	{Key: "workflows.*.commands.build", In: InProfile | InRepo},
	{Key: "workflows.*.commands.test", In: InProfile | InRepo},
	{Key: "workflows.*.commands.rerun_failed", In: InProfile | InRepo},
	{Key: "workflows.*.commands.reports", In: InProfile | InRepo},
	{Key: "workflows.*.cache", In: InProfile | InRepo},
	{Key: "workflows.*.secrets", In: InRepo, Why: whySecrets},
	{Key: "workflows.*.resources.cpu", In: InProfile | InRepo},
	{Key: "workflows.*.resources.memory", In: InProfile | InRepo},
	{Key: "workflows.*.timeouts.total", In: InProfile | InRepo | InOverride},
	{Key: "workflows.*.timeouts.stage", In: InProfile | InRepo},
	{Key: "workflows.*.timeouts.verify", In: InProfile | InRepo},
	{Key: "workflows.*.timeouts.finalize_reserve", In: InProfile | InRepo},
	{Key: "workflows.*.rebuild.check", In: InProfile | InRepo},
	{Key: "workflows.*.rebuild.max_age", In: InProfile | InRepo},
	{Key: "workflows.*.rebuild.lockfiles", In: InProfile | InRepo},
	{Key: "workflows.*.rebuild.base", In: InProfile | InRepo},
	{Key: "workflows.*.rebuild.paths", In: InProfile | InRepo},
	{Key: "followup.trusted", In: InRepo, Why: whyFollowup},
	{Key: "followup.allow_public", In: InRepo, Why: whyFollowup},
}

// ExecutableKeys are the profile keys whose values run as shell, in the
// job or in the image build (decision L7): a publish that changes one needs
// --executable-changes.
var ExecutableKeys = []string{
	"workflows.*.commands.build", "workflows.*.commands.test", "workflows.*.commands.rerun_failed",
	"workflows.*.image.apt", "workflows.*.image.setup",
}

// ScopeOf is the row of path, a fugaro.yaml path with a real workflow name
// ("workflows.web.commands.test") or "*". A path inside a listed key (an
// entry of cache:) is the listed key's. false: no such key.
func ScopeOf(path string) (ScopeRow, bool) {
	parts := strings.Split(starWorkflow(path), ".")
	for n := len(parts); n > 0; n-- {
		if r, ok := scopeRow(strings.Join(parts[:n], ".")); ok {
			return r, true
		}
	}
	return ScopeRow{}, false
}

// starWorkflow is path with its workflow name, if any, replaced by "*".
func starWorkflow(path string) string {
	parts := strings.Split(path, ".")
	if len(parts) >= 2 && parts[0] == "workflows" {
		parts[1] = "*"
	}
	return strings.Join(parts, ".")
}

// scopeRow is the row whose key is exactly key.
func scopeRow(key string) (ScopeRow, bool) {
	for _, r := range Scopes {
		if r.Key == key {
			return r, true
		}
	}
	return ScopeRow{}, false
}

// isBlock reports whether path ("agent", "workflows.*.image") is a block
// that holds listed keys rather than a key itself.
func isBlock(path string) bool {
	for _, r := range Scopes {
		if strings.HasPrefix(r.Key, path+".") {
			return true
		}
	}
	return false
}

// String names the layers of s, as the docs table and the errors do.
func (s Scope) String() string {
	var out []string
	for _, l := range []struct {
		bit  Scope
		name string
	}{{InProject, "project"}, {InProfile, "profile"}, {InRepo, "repo"}, {InOverride, "override"}} {
		if s&l.bit != 0 {
			out = append(out, l.name)
		}
	}
	return strings.Join(out, ", ")
}
```

- [ ] **Step 4: Run it and see it pass**

Run: `go test -race ./internal/config/ -run TestScopeOf`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/config/scope.go internal/config/scope_test.go
git commit -m "layered config task 1: the scope table of every fugaro.yaml key"
```

### Task 2: The project layer and its strict parser (`internal/config/layer.go`)

**Files:**
- Create: `internal/config/layer.go`
- Test: `internal/config/layer_test.go`

**Interfaces:**
- Consumes: `Scopes`, `scopeRow`, `starWorkflow`, `isBlock` (Task 1); `yamlProblems`, `sortedKeys`, `validateImage`, `validateRebuild`, `validateAgentModels`, `memoryRE`, `Bases`, `Providers`, `ProjectNameRE`, `GCPProjectRE`, `MaxFirstLineRounds`, `recipe.NameRE` (existing).
- Produces:
  ```go
  const ( LayerKey = "fugaro/project-layer.yaml"; LayerMaxBytes = 64 << 10; ImplicitWorkflow = "default" )
  func LayerCopyKey(slug string) string // builds/<slug>/project-layer.yaml
  type LayerAnchor struct{ Project, GCPProject string }
  type ProjectLayer struct {
      Version int; Project, GCPProject string; Defaults LayerDefaults
      Profiles map[string]Profile; DefaultProfile string
      Raw []byte; SHA256 string // set by ParseProjectLayer
      tree map[string]any       // what Resolve merges
  }
  type LayerDefaults struct { Git LayerGit; Agent LayerAgent }
  type LayerGit struct { Provider string; PR LayerPR }
  type LayerPR struct { Labels []string; EarlyDraft, Checkpoints *bool }
  type LayerAgent struct { Auth, Model string; Models ModelRoles; ReviewRounds int; MaxBudgetUSD float64; FirstLineReview string; FirstLineRounds int; Recipe string }
  type Profile struct { Description, Base string; Image Image; Commands Commands; Cache []CacheEntry; Resources Resources; Timeouts Timeouts; Rebuild Rebuild }
  func (p Profile) HasExecutable() bool
  func LayerSum(data []byte) string
  func ParseProjectLayer(data []byte, a LayerAnchor) (*ProjectLayer, []Problem)
  func maps1(m map[string]any) map[string]any // shallow copy, used by Resolve
  ```

- [ ] **Step 1: Write the failing test**

Create `internal/config/layer_test.go`:

```go
package config

import (
	"strings"
	"testing"
)

const testLayer = `version: 1
project: acme
gcp_project: acme-fugaro
defaults:
  git:
    provider: github
    pr:
      labels: [fugaro]
      early_draft: false
  agent:
    review_rounds: 3
    recipe: claude-solo
profiles:
  java-service:
    description: Gradle service
    base: java-services
    image:
      apt: [graphviz]
    commands:
      build: ./gradlew assemble
      test: ./gradlew test
    resources: { cpu: 4, memory: 16Gi }
    timeouts: { total: 2h }
  node-web:
    base: web-node
    image: { node: "20" }
    commands: { build: npm run build, test: npm test }
default_profile: java-service
`

var testAnchor = LayerAnchor{Project: "acme", GCPProject: "acme-fugaro"}

func mustLayer(t *testing.T, text string) *ProjectLayer {
	t.Helper()
	l, ps := ParseProjectLayer([]byte(text), testAnchor)
	if len(ps) > 0 {
		t.Fatalf("layer problems: %v", ps)
	}
	return l
}

func TestParseProjectLayer(t *testing.T) {
	l := mustLayer(t, testLayer)
	if l.SHA256 != LayerSum([]byte(testLayer)) || l.DefaultProfile != "java-service" || l.Profiles["node-web"].Image.Node != "20" {
		t.Fatalf("layer = %+v", l)
	}
	if !l.Profiles["java-service"].HasExecutable() {
		t.Fatal("java-service sets commands")
	}
}

func TestParseProjectLayerRefuses(t *testing.T) {
	head := "version: 1\nproject: acme\ngcp_project: acme-fugaro\n"
	for _, tc := range []struct{ name, text, want string }{
		{"secrets are repo-only", head + "profiles:\n  p:\n    secrets: [{name: db, env: DB}]\n", "profiles.p.secrets: workflows.*.secrets may only be set in: repo"},
		{"followup is repo-only", head + "defaults:\n  followup:\n    trusted: ['1']\n", "defaults.followup.trusted: followup.trusted may only be set in: repo"},
		{"reviewers are repo-only", head + "defaults:\n  git:\n    pr:\n      reviewers: [someone]\n", "git.pr.reviewers may only be set in: repo"},
		{"instructions are repo-only", head + "defaults:\n  agent:\n    instructions: AGENTS.md\n", "agent.instructions may only be set in: repo"},
		{"policy is the owner's", head + "defaults:\n  budget:\n    per_run_usd: 100\n", "budget.per_run_usd may only be set in: repo"},
		{"run tokens are policy", head + "defaults:\n  agent:\n    max_run_tokens: 9\n", "agent.max_run_tokens may only be set in: repo"},
		{"base branch is repo-only", head + "defaults:\n  git:\n    base_branch: evil\n", "git.base_branch may only be set in: repo"},
		{"dockerfile is repo-only", head + "profiles:\n  p:\n    dockerfile: x.Dockerfile\n", "workflows.*.dockerfile may only be set in: repo"},
		{"workflow keys are not defaults", head + "defaults:\n  workflows:\n    web:\n      commands: { test: x }\n", "may only be set in: profile, repo"},
		{"unknown key", head + "defaults:\n  agent:\n    colour: red\n", "defaults.agent.colour: is not a fugaro.yaml key"},
		{"the catalog is reserved", head + "environments:\n  java-17: {}\n", "arrives with the single base image"},
		{"a credential", head + "profiles:\n  p:\n    commands: { test: 'curl -H \"x: ghp_abcdefghijklmnopqrstuvwxyz0123\" x' }\n", "shaped like a credential"},
		{"another project", "version: 1\nproject: other\ngcp_project: acme-fugaro\n", `project: is "other", but it is read for project "acme"`},
		{"another gcp project", "version: 1\nproject: acme\ngcp_project: other-proj\n", "gcp_project: is \"other-proj\""},
		{"an anchor", head + "defaults: &d\n  agent: {}\nprofiles:\n  p: *d\n", "anchor or alias"},
		{"a merge key", head + "profiles:\n  p:\n    <<: {base: go}\n", "merge key"},
		{"a repeated key", head + "profiles:\n  p: {base: go}\n  p: {base: go}\n", `repeats the key "p"`},
		{"two documents", head + "---\nversion: 1\n", "more than one YAML document"},
		{"a bad base", head + "profiles:\n  p: { base: java-17 }\n", "profiles.p.base: must be one of go, java-services, web-node"},
		{"an unknown default profile", head + "profiles:\n  p: { base: go }\ndefault_profile: q\n", `default_profile: names "q"`},
		{"a bad profile name", head + "profiles:\n  P_1: { base: go }\n", "a profile name must be"},
		{"node off web-node", head + "profiles:\n  p: { base: go, image: { node: '20' } }\n", "only applies to base web-node"},
		{"oversized", head + "# " + strings.Repeat("x", LayerMaxBytes) + "\n", "over the 64 KiB limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ps := ParseProjectLayer([]byte(tc.text), testAnchor)
			var msgs []string
			for _, p := range ps {
				msgs = append(msgs, p.String())
			}
			if got := strings.Join(msgs, "; "); !strings.Contains(got, tc.want) {
				t.Fatalf("problems %q, want one containing %q", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run it and see it fail**

Run: `go test ./internal/config/ -run 'TestParseProjectLayer'`
Expected: FAIL to compile: `undefined: ProjectLayer`, `undefined: ParseProjectLayer`, `undefined: LayerAnchor`.

- [ ] **Step 3: Implement**

Create `internal/config/layer.go`:

```go
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/recipe"
)

// The project layer (docs/design/layered-config.md): one object per project
// in the runs bucket, published by fugaro config publish, holding the
// defaults and the workflow profiles of every repository of the project.
const (
	// LayerKey is the project layer's object in the runs bucket.
	LayerKey = "fugaro/project-layer.yaml"
	// LayerMaxBytes caps the object, as the shared config is capped.
	LayerMaxBytes = 64 << 10
	// ImplicitWorkflow is the workflow a fugaro.yaml with no workflows:
	// gets from its profile.
	ImplicitWorkflow = "default"
	// maxProfileDescription bounds a profile's description.
	maxProfileDescription = 200
)

// LayerCopyKey is the per-repository copy of the project layer, under the
// build account's own prefix, which the daily image check and Cloud Build
// read (decision L4).
func LayerCopyKey(slug string) string { return "builds/" + slug + "/project-layer.yaml" }

// LayerAnchor is what a project layer must name: the project and GCP
// project of the repository (or local config) it is read for. An empty
// field is not checked.
type LayerAnchor struct{ Project, GCPProject string }

// ProjectLayer is a parsed, validated project layer.
type ProjectLayer struct {
	Version        int                `yaml:"version"`
	Project        string             `yaml:"project"`
	GCPProject     string             `yaml:"gcp_project"`
	Defaults       LayerDefaults      `yaml:"defaults"`
	Profiles       map[string]Profile `yaml:"profiles"`
	DefaultProfile string             `yaml:"default_profile"`

	// Raw is the exact text, SHA256 its hex sha256.
	Raw    []byte `yaml:"-"`
	SHA256 string `yaml:"-"`
	// tree is the text as plain maps, which Resolve merges.
	tree map[string]any
}

// LayerDefaults are the project-wide values of the repository keys the
// project layer may set (scope project).
type LayerDefaults struct {
	Git   LayerGit   `yaml:"git"`
	Agent LayerAgent `yaml:"agent"`
}

// LayerGit is defaults.git.
type LayerGit struct {
	Provider string  `yaml:"provider"`
	PR       LayerPR `yaml:"pr"`
}

// LayerPR is defaults.git.pr.
type LayerPR struct {
	Labels      []string `yaml:"labels"`
	EarlyDraft  *bool    `yaml:"early_draft"`
	Checkpoints *bool    `yaml:"checkpoints"`
}

// LayerAgent is defaults.agent.
type LayerAgent struct {
	Auth            string     `yaml:"auth"`
	Model           string     `yaml:"model"`
	Models          ModelRoles `yaml:"models"`
	ReviewRounds    int        `yaml:"review_rounds"`
	MaxBudgetUSD    float64    `yaml:"max_budget_usd"`
	FirstLineReview string     `yaml:"first_line_review"`
	FirstLineRounds int        `yaml:"first_line_rounds"`
	Recipe          string     `yaml:"recipe"`
}

// Profile is a named workflow template (scope profile).
type Profile struct {
	Description string       `yaml:"description"`
	Base        string       `yaml:"base"`
	Image       Image        `yaml:"image"`
	Commands    Commands     `yaml:"commands"`
	Cache       []CacheEntry `yaml:"cache"`
	Resources   Resources    `yaml:"resources"`
	Timeouts    Timeouts     `yaml:"timeouts"`
	Rebuild     Rebuild      `yaml:"rebuild"`
}

// HasExecutable reports whether the profile sets a key that runs as shell
// (ExecutableKeys).
func (p Profile) HasExecutable() bool {
	return p.Commands.Build != "" || p.Commands.Test != "" || p.Commands.RerunFailed != nil || len(p.Image.Apt) > 0 || len(p.Image.Setup) > 0
}

// LayerSum is the hex sha256 of a project layer's text.
func LayerSum(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

var layerTopKeys = []string{"version", "project", "gcp_project", "defaults", "profiles", "default_profile"}

// layerReserved are keys a later release defines; this one refuses them
// with a message saying so.
var layerReserved = map[string]string{
	"environments": "the image catalog (named environments) arrives with the single base image (docs/design/layered-config.md, Phase 2); this release refuses it",
	"extends":      "profiles do not extend each other in this release",
}

// tokenRE matches values shaped like a credential. Nothing in the project
// layer is a secret, so one is refused rather than published.
var tokenRE = regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{8,}|ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|gh[osu]_[A-Za-z0-9]{20,}|xox[abprs]-[A-Za-z0-9-]{10,}|AIza[0-9A-Za-z_-]{30,}|ATBB[A-Za-z0-9]{20,}|-----BEGIN [A-Z ]*PRIVATE KEY-----`)

// ParseProjectLayer parses and validates a project layer. Anyone holding
// objectAdmin on the runs bucket can write it, so, like the shared config,
// it refuses (never repairs) anything off: over LayerMaxBytes, more than
// one document, anchors, aliases, merge keys, tags, repeated keys, unknown
// or reserved keys, a key outside its scope, a credential-shaped value, a
// project or GCP project other than a's, and invalid values.
func ParseProjectLayer(data []byte, a LayerAnchor) (*ProjectLayer, []Problem) {
	if len(data) > LayerMaxBytes {
		return nil, []Problem{{Message: fmt.Sprintf("is over the %d KiB limit", LayerMaxBytes>>10)}}
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, []Problem{{Message: "is empty"}}
		}
		return nil, yamlProblems(err)
	}
	var more yaml.Node
	if err := dec.Decode(&more); !errors.Is(err, io.EOF) {
		return nil, []Problem{{Message: "holds more than one YAML document"}}
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, []Problem{{Message: "is not a YAML mapping"}}
	}
	if p := layerShape(doc.Content[0]); p != nil {
		return nil, []Problem{*p}
	}
	var tree map[string]any
	if err := doc.Decode(&tree); err != nil {
		return nil, yamlProblems(err)
	}
	var ps []Problem
	for _, k := range sortedKeys(tree) {
		if msg, ok := layerReserved[k]; ok {
			ps = append(ps, Problem{Path: k, Message: msg})
		} else if !slices.Contains(layerTopKeys, k) {
			ps = append(ps, Problem{Path: k, Message: "is not a project layer key (" + strings.Join(layerTopKeys, ", ") + ")"})
		}
	}
	if d, ok := tree["defaults"].(map[string]any); ok {
		ps = append(ps, scopeProblems("defaults", "", d, InProject)...)
	}
	if prs, ok := tree["profiles"].(map[string]any); ok {
		for _, name := range sortedKeys(prs) {
			if p, ok := prs[name].(map[string]any); ok {
				q := maps1(p)
				delete(q, "description")
				ps = append(ps, scopeProblems("profiles."+name, "workflows.*", q, InProfile)...)
			}
		}
	}
	ps = append(ps, tokenProblems("", tree)...)
	if len(ps) > 0 {
		return nil, ps
	}
	var l ProjectLayer
	strict := yaml.NewDecoder(bytes.NewReader(data))
	strict.KnownFields(true)
	if err := strict.Decode(&l); err != nil {
		return nil, yamlProblems(err)
	}
	if ps := validateLayer(&l, a); len(ps) > 0 {
		return nil, ps
	}
	l.Raw, l.SHA256, l.tree = slices.Clone(data), LayerSum(data), tree
	return &l, nil
}

func maps1(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// scopeProblems walks m, the layer's block at prefix, whose keys are the
// fugaro.yaml keys under repoPrefix, and reports every key that layer may
// not set or that does not exist.
func scopeProblems(prefix, repoPrefix string, m map[string]any, layer Scope) []Problem {
	var ps []Problem
	for _, k := range sortedKeys(m) {
		path, key := prefix+"."+k, k
		if repoPrefix != "" {
			key = repoPrefix + "." + k
		}
		key = starWorkflow(key)
		row, isRow := scopeRow(key)
		sub, isMap := m[k].(map[string]any)
		switch {
		case isRow && row.In&layer == 0:
			ps = append(ps, Problem{Path: path, Message: fmt.Sprintf("%s may only be set in: %s (%s); the project layer may not set it", row.Key, row.In, row.Why)})
		case isRow:
		case isMap && isBlock(key):
			ps = append(ps, scopeProblems(path, key, sub, layer)...)
		default:
			ps = append(ps, Problem{Path: path, Message: "is not a fugaro.yaml key the project layer knows"})
		}
	}
	return ps
}

func tokenProblems(path string, v any) []Problem {
	switch t := v.(type) {
	case string:
		if tokenRE.MatchString(t) {
			return []Problem{{Path: path, Message: "holds a value shaped like a credential; nothing in the project layer is secret, and secrets are never published"}}
		}
	case map[string]any:
		var ps []Problem
		for _, k := range sortedKeys(t) {
			p := k
			if path != "" {
				p = path + "." + k
			}
			ps = append(ps, tokenProblems(p, t[k])...)
		}
		return ps
	case []any:
		var ps []Problem
		for i, e := range t {
			ps = append(ps, tokenProblems(fmt.Sprintf("%s[%d]", path, i), e)...)
		}
		return ps
	}
	return nil
}

// layerShape refuses anchors, aliases, explicit tags, merge keys, non-scalar
// keys and repeated keys anywhere, as the shared config's reader does.
func layerShape(n *yaml.Node) *Problem {
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return &Problem{Line: n.Line, Message: "uses a YAML anchor or alias, which the project layer refuses"}
	}
	if n.Style&yaml.TaggedStyle != 0 {
		return &Problem{Line: n.Line, Message: fmt.Sprintf("uses an explicit YAML tag %q, which the project layer refuses", n.Tag)}
	}
	if n.Kind == yaml.MappingNode {
		seen := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			switch {
			case k.Kind != yaml.ScalarNode:
				return &Problem{Line: k.Line, Message: "has a key that is not a plain value"}
			case k.Value == "<<" || k.Tag == "!!merge":
				return &Problem{Line: k.Line, Message: "uses a YAML merge key <<, which the project layer refuses"}
			case seen[k.Value]:
				return &Problem{Line: k.Line, Message: fmt.Sprintf("repeats the key %q", k.Value)}
			}
			seen[k.Value] = true
		}
	}
	for _, c := range n.Content {
		if p := layerShape(c); p != nil {
			return p
		}
	}
	return nil
}

func validateLayer(l *ProjectLayer, a LayerAnchor) []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if l.Version != 1 {
		add("version", "must be 1")
	}
	switch {
	case !ProjectNameRE.MatchString(l.Project):
		add("project", "must be a project name")
	case a.Project != "" && l.Project != a.Project:
		add("project", "is %q, but it is read for project %q", l.Project, a.Project)
	}
	switch {
	case !GCPProjectRE.MatchString(l.GCPProject):
		add("gcp_project", "must be a GCP project ID")
	case a.GCPProject != "" && l.GCPProject != a.GCPProject:
		add("gcp_project", "is %q, but it is read for GCP project %q", l.GCPProject, a.GCPProject)
	}
	d := l.Defaults
	if d.Git.Provider != "" && !slices.Contains(Providers, d.Git.Provider) {
		add("defaults.git.provider", "must be one of %s", strings.Join(Providers, ", "))
	}
	ag := d.Agent
	if ag.Auth != "" && !slices.Contains([]string{"vertex", "api-key", "oauth"}, ag.Auth) {
		add("defaults.agent.auth", "must be one of vertex, api-key, oauth")
	}
	if ag.ReviewRounds != 0 && (ag.ReviewRounds < 1 || ag.ReviewRounds > 10) {
		add("defaults.agent.review_rounds", "must be between 1 and 10")
	}
	if ag.FirstLineReview != "" && !slices.Contains([]string{FirstLineAuto, FirstLineOn, FirstLineOff}, ag.FirstLineReview) {
		add("defaults.agent.first_line_review", "must be one of auto, on, off")
	}
	if ag.FirstLineRounds != 0 && (ag.FirstLineRounds < 1 || ag.FirstLineRounds > MaxFirstLineRounds) {
		add("defaults.agent.first_line_rounds", "must be between 1 and %d", MaxFirstLineRounds)
	}
	if ag.Recipe != "" && !recipe.NameRE.MatchString(ag.Recipe) {
		add("defaults.agent.recipe", "must be a recipe name")
	}
	if ag.MaxBudgetUSD < 0 {
		add("defaults.agent.max_budget_usd", "must not be negative")
	}
	for _, p := range validateAgentModels(Agent{Model: ag.Model, Models: ag.Models}) {
		p.Path = "defaults." + p.Path
		ps = append(ps, p)
	}
	for _, name := range sortedKeys(l.Profiles) {
		p := "profiles." + name
		if !ProjectNameRE.MatchString(name) {
			add(p, "a profile name must be 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit")
		}
		ps = append(ps, validateProfile(p, l.Profiles[name])...)
	}
	if l.DefaultProfile != "" {
		if _, ok := l.Profiles[l.DefaultProfile]; !ok {
			add("default_profile", "names %q, which is not one of profiles: (%s)", l.DefaultProfile, strings.Join(sortedKeys(l.Profiles), ", "))
		}
	}
	return ps
}

// validateProfile checks what a profile sets; what it leaves out the
// repository may set, and Validate checks the merged workflow.
func validateProfile(p string, pr Profile) []Problem {
	var ps []Problem
	add := func(path, format string, args ...any) {
		ps = append(ps, Problem{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if len(pr.Description) > maxProfileDescription || strings.ContainsAny(pr.Description, "\r\n") {
		add(p+".description", "must be one line of at most %d characters", maxProfileDescription)
	}
	if pr.Base != "" && !slices.Contains(Bases, pr.Base) {
		add(p+".base", "must be one of %s", strings.Join(Bases, ", "))
	}
	ps = append(ps, validateImage(p, Workflow{Base: pr.Base, Image: pr.Image})...)
	if rf := pr.Commands.RerunFailed; rf != nil {
		if strings.TrimSpace(rf.Command) == "" {
			add(p+".commands.rerun_failed.command", "is required")
		}
		if !strings.Contains(rf.Each, "{id}") {
			add(p+".commands.rerun_failed.each", "must contain {id}")
		}
	}
	for i, ce := range pr.Cache {
		cp := fmt.Sprintf("%s.cache[%d]", p, i)
		if len(ce.Key) == 0 {
			add(cp+".key", "must list at least one file")
		}
		if len(ce.Paths) == 0 {
			add(cp+".paths", "must list at least one path")
		}
	}
	if pr.Resources.CPU < 0 {
		add(p+".resources.cpu", "must be at least 1 (the compute backend checks its own limits)")
	}
	if m := pr.Resources.Memory; m != "" && !memoryRE.MatchString(m) {
		add(p+".resources.memory", "must look like 512Mi or 16Gi")
	}
	t := pr.Timeouts
	for _, d := range []struct {
		name string
		v    Duration
	}{{"total", t.Total}, {"stage", t.Stage}, {"verify", t.Verify}, {"finalize_reserve", t.FinalizeReserve}} {
		if d.v.Set && d.v.Duration <= 0 {
			add(p+".timeouts."+d.name, "must be positive")
		}
	}
	r := pr.Rebuild
	if r.Check == "" {
		r.Check = "daily"
	}
	return append(ps, validateRebuild(p+".rebuild", r)...)
}
```

- [ ] **Step 4: Run it and see it pass**

Run: `go test -race ./internal/config/ -run 'TestParseProjectLayer|TestScopeOf'`
Expected: `ok` (24 subtests of `TestParseProjectLayerRefuses` pass).

- [ ] **Step 5: Commit**

```bash
git add internal/config/layer.go internal/config/layer_test.go
git commit -m "layered config task 2: the project layer and its strict, scope-checked parser"
```

### Task 3: `profile` and `workflows.<n>.profile` in fugaro.yaml (struct, Validate, schema)

**Files:**
- Modify: `internal/config/config.go` (the `Config` and `Workflow` structs), `internal/config/validate.go` (`Validate`), `schemas/fugaro.schema.json`
- Test: `internal/config/config_test.go`, `schemas/schemas_test.go`

**Interfaces:**
- Consumes: `ProjectNameRE`.
- Produces: the fields `Config.Profile` and `Workflow.Profile` (both `string`, YAML key `profile`, omitempty). The schema stops requiring `git`, `workflows` and `git.provider` when the file has `gcp_project`, and stops requiring a workflow's `base`, `commands`, `commands.build` and `commands.test` when the workflow has `profile`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/config_test.go` (it already imports `strings`, `testing` and `gopkg.in/yaml.v3`):

```go
const profileBase = "version: 1\nproject: aurora\ngit: { provider: github }\nworkflows:\n  web: { base: go, commands: { build: make, test: make test } }\n"

func TestProfileKeysDecode(t *testing.T) {
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(strings.Replace(profileBase, "  web: {", "  web: { profile: java-service,", 1) + "profile: node-web\n"))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		t.Fatal(err)
	}
	if c.Profile != "node-web" || c.Workflows["web"].Profile != "java-service" {
		t.Fatalf("decoded %q, %q", c.Profile, c.Workflows["web"].Profile)
	}
}

func TestValidateProfileNames(t *testing.T) {
	c, ps := Parse([]byte(profileBase))
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	c.Profile = "Bad_Name"
	w := c.Workflows["web"]
	w.Profile = "also bad!"
	c.Workflows["web"] = w
	var paths []string
	for _, p := range Validate(c) {
		if strings.Contains(p.Message, "must be a profile name") {
			paths = append(paths, p.Path)
		}
	}
	if strings.Join(paths, ",") != "profile,workflows.web.profile" {
		t.Fatalf("profile problems at %v", paths)
	}
}
```

Append to `schemas/schemas_test.go`:

```go
func TestFugaroSchemaProfileKeys(t *testing.T) {
	sch := compile(t, "fugaro.schema.json")
	for text, valid := range map[string]bool{
		"version: 1\nproject: acme\ngcp_project: acme-fugaro\n":                                               true,
		"version: 1\nproject: acme\ngcp_project: acme-fugaro\nprofile: node-web\n":                            true,
		"version: 1\nproject: acme\ngcp_project: acme-fugaro\nworkflows:\n  api: { profile: java-service }\n": true,
		"version: 1\nproject: acme\n":                                                                          false,
		"version: 1\nproject: acme\ngit: { provider: github }\nworkflows:\n  api: { profile: java-service }\n": true,
		"version: 1\nproject: acme\ngit: { provider: github }\nworkflows:\n  api: { commands: { build: a } }\n": false,
		"version: 1\nproject: acme\ngcp_project: acme-fugaro\nprofile: Bad_Name\n":                            false,
	} {
		if err := sch.Validate(yamlInstance(t, []byte(text))); (err == nil) != valid {
			t.Errorf("%q: valid = %v, want %v (%v)", text, err == nil, valid, err)
		}
	}
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/config/ -run 'TestProfileKeysDecode|TestValidateProfileNames' && go test ./schemas/ -run TestFugaroSchemaProfileKeys`
Expected: FAIL: `c.Profile undefined (type Config has no field or method Profile)`. With the struct fields alone, `TestFugaroSchemaProfileKeys` fails on the minimal file (`missing properties 'git', 'workflows'`) and on `profile`.

- [ ] **Step 3: Implement**

In `internal/config/config.go`, replace

```go
	GCPProject string `yaml:"gcp_project,omitempty"`
	Git        Git    `yaml:"git"`
	Agent      Agent  `yaml:"agent"`
```

with

```go
	GCPProject string `yaml:"gcp_project,omitempty"`
	// Profile names the project layer's profile of the implicit workflow
	// (ImplicitWorkflow), for a file with no workflows:. "" is the
	// project's default_profile. Binaries before 0.6.0 refuse the key.
	Profile string `yaml:"profile,omitempty"`
	Git     Git    `yaml:"git"`
	Agent   Agent  `yaml:"agent"`
```

and in `type Workflow struct {`, before `Base`, add

```go
	// Profile names the project layer's profile this workflow starts from;
	// the workflow's own keys override it field by field. "" is none.
	// Binaries before 0.6.0 refuse the key.
	Profile    string       `yaml:"profile,omitempty"`
```

In `internal/config/validate.go`'s `Validate`, before the `gcp_project` check, add

```go
	if c.Profile != "" && !ProjectNameRE.MatchString(c.Profile) {
		add("profile", "must be a profile name: 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit")
	}
```

and in the workflow loop, before the `base` check, add

```go
		if w.Profile != "" && !ProjectNameRE.MatchString(w.Profile) {
			add(p+".profile", "must be a profile name: 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or digit")
		}
```

In `schemas/fugaro.schema.json`:

1. Replace `"required": ["version", "project", "git", "workflows"],` with `"required": ["version", "project"],`.
2. Append to the top-level `"allOf"` array a third entry:
   ```json
   {
     "if": { "not": { "required": ["gcp_project"] } },
     "then": { "required": ["git", "workflows"], "properties": { "git": { "required": ["provider"] } } }
   }
   ```
3. In `properties.git`, delete `"required": ["provider"],`.
4. After `properties.gcp_project`, add:
   ```json
   "profile": { "type": "string", "pattern": "^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$", "description": "The project layer's profile of the implicit workflow \"default\", for a file with no workflows: (docs/project-layer.md). Absent: the project's default_profile. Needs fugaro 0.6.0." },
   ```
5. In `$defs.workflow`, delete `"required": ["base", "commands"],`, add as the first property
   ```json
   "profile": { "type": "string", "pattern": "^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$", "description": "The project layer's profile this workflow starts from; its own keys override the profile's field by field. Needs fugaro 0.6.0." },
   ```
   and append to `$defs.workflow.allOf`:
   ```json
   {
     "if": { "not": { "required": ["profile"] } },
     "then": { "required": ["base", "commands"], "properties": { "commands": { "required": ["build", "test"] } } }
   }
   ```
6. In `$defs.workflow.properties.commands`, delete `"required": ["build", "test"],`.

The file `testdata/config/invalid/no-workflows.yaml` has no `gcp_project`, so it stays invalid for the schema.

- [ ] **Step 4: Run them and see them pass**

Run: `go test -race ./internal/config/ && go test -race ./schemas/`
Expected: `ok` for both (the existing corpus and `TestExampleIsValid` included).

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/validate.go internal/config/config_test.go schemas/fugaro.schema.json schemas/schemas_test.go
git commit -m "layered config task 3: profile and workflows.<n>.profile in fugaro.yaml"
```

### Task 4: `Resolve`: the merge, the sources and the sums (`internal/config/resolve.go`)

**Files:**
- Create: `internal/config/resolve.go`
- Modify: `internal/config/config.go` (`Parse` becomes `Resolve(data, nil)`; its body becomes `decodeRepo`)
- Test: `internal/config/resolve_test.go`

**Interfaces:**
- Consumes: `ParseProjectLayer`, `ProjectLayer.tree`, `maps1`, `ImplicitWorkflow`, `LayerKey` (Task 2); `Config.Profile`, `Workflow.Profile` (Task 3); `applyDefaults`, `Validate`, `yamlProblems`, `sortedKeys`.
- Produces:
  ```go
  const ( SourceDefault = "default"; SourceProject = "project"; SourceRepo = "repo" )
  func SourceProfile(name string) string
  const CodeNeedsLayer = "needs_project_layer"
  type Resolution struct { Sources map[string]string; LayerSHA256, ConfigSHA256 string }
  func (r *Resolution) SourceOf(path string) string
  func (c *Config) SHA256() string
  // Config gains: Layer *ProjectLayer `yaml:"-" json:"-"` (the layer it was resolved over)
  func Resolve(data []byte, l *ProjectLayer) (*Config, *Resolution, []Problem)
  func decodeRepo(data []byte) (*Config, []Problem) // config.go
  ```

- [ ] **Step 1: Write the failing test**

Create `internal/config/resolve_test.go` (it reuses `testLayer` and `mustLayer` from `layer_test.go`):

```go
package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const minimalRepo = "version: 1\nproject: acme\ngcp_project: acme-fugaro\n"

func TestResolveMinimalTakesTheDefaultProfile(t *testing.T) {
	l := mustLayer(t, testLayer)
	c, res, ps := Resolve([]byte(minimalRepo), l)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	if c.Layer != l {
		t.Fatal("the resolved config does not carry its layer")
	}
	w, ok := c.Workflows[ImplicitWorkflow]
	if !ok || len(c.Workflows) != 1 {
		t.Fatalf("workflows = %v", c.Workflows)
	}
	if w.Base != "java-services" || w.Commands.Test != "./gradlew test" || w.Profile != "java-service" ||
		w.Timeouts.Total.Duration != 2*time.Hour || w.Timeouts.Stage.Duration != 40*time.Minute {
		t.Fatalf("workflow = %+v", w)
	}
	if c.Git.Provider != "github" || c.Agent.ReviewRounds != 3 || c.Agent.Recipe != "claude-solo" || c.Git.PR.EarlyDraftOn() {
		t.Fatalf("defaults not applied: %+v %+v", c.Git, c.Agent)
	}
	for path, want := range map[string]string{
		"project":                            SourceRepo,
		"git.provider":                       SourceProject,
		"git.pr.labels":                      SourceProject,
		"git.base_branch":                    SourceDefault,
		"workflows.default.commands.test":    SourceProfile("java-service"),
		"workflows.default.profile":          SourceProject,
		"workflows.default.timeouts.stage":   SourceDefault,
		"workflows.default.resources.memory": SourceProfile("java-service"),
		"workflows.default.image.apt":        SourceProfile("java-service"),
		"workflows.default.rebuild.check":    SourceDefault,
		"agent.first_line_rounds":            SourceDefault,
	} {
		if got := res.SourceOf(path); got != want {
			t.Errorf("SourceOf(%s) = %q, want %q", path, got, want)
		}
	}
	if res.LayerSHA256 != LayerSum([]byte(testLayer)) || res.ConfigSHA256 != c.SHA256() {
		t.Fatalf("sums = %+v", res)
	}
}

// The golden table: each row is one repository file over testLayer, and
// the values and sources it must resolve to.
func TestResolveTable(t *testing.T) {
	type want struct{ value, source string }
	for _, tc := range []struct {
		name string
		repo string
		get  map[string]func(*Config) string
		want map[string]want
	}{
		{
			name: "a repository scalar beats the project",
			repo: minimalRepo + "agent:\n  review_rounds: 1\n",
			get:  map[string]func(*Config) string{"agent.review_rounds": func(c *Config) string { return itoa(c.Agent.ReviewRounds) }},
			want: map[string]want{"agent.review_rounds": {"1", SourceRepo}},
		},
		{
			name: "false beats a project true, and the project's false beats the default",
			repo: minimalRepo + "git:\n  pr:\n    checkpoints: false\n",
			get: map[string]func(*Config) string{
				"git.pr.checkpoints": func(c *Config) string { return btoa(c.Git.PR.CheckpointsOn()) },
				"git.pr.early_draft": func(c *Config) string { return btoa(c.Git.PR.EarlyDraftOn()) },
			},
			want: map[string]want{"git.pr.checkpoints": {"false", SourceRepo}, "git.pr.early_draft": {"false", SourceProject}},
		},
		{
			name: "a list is replaced, never appended",
			repo: minimalRepo + "git:\n  pr:\n    labels: [mine]\n",
			get:  map[string]func(*Config) string{"git.pr.labels": func(c *Config) string { return strings.Join(c.Git.PR.Labels, ",") }},
			want: map[string]want{"git.pr.labels": {"mine", SourceRepo}},
		},
		{
			name: "an empty list clears",
			repo: minimalRepo + "git:\n  pr:\n    labels: []\n",
			get:  map[string]func(*Config) string{"git.pr.labels": func(c *Config) string { return strings.Join(c.Git.PR.Labels, ",") }},
			want: map[string]want{"git.pr.labels": {"", SourceRepo}},
		},
		{
			name: "null sets nothing",
			repo: minimalRepo + "git:\n  pr:\n    labels:\n",
			get:  map[string]func(*Config) string{"git.pr.labels": func(c *Config) string { return strings.Join(c.Git.PR.Labels, ",") }},
			want: map[string]want{"git.pr.labels": {"fugaro", SourceProject}},
		},
		{
			name: "profile: picks another profile",
			repo: minimalRepo + "profile: node-web\n",
			get: map[string]func(*Config) string{
				"workflows.default.base":       func(c *Config) string { return c.Workflows["default"].Base },
				"workflows.default.image.node": func(c *Config) string { return c.Workflows["default"].Image.Node },
				"workflows.default.profile":    func(c *Config) string { return c.Workflows["default"].Profile },
			},
			want: map[string]want{
				"workflows.default.base":       {"web-node", SourceProfile("node-web")},
				"workflows.default.image.node": {"20", SourceProfile("node-web")},
				"workflows.default.profile":    {"node-web", SourceRepo},
			},
		},
		{
			name: "a workflow overrides one field of its profile",
			repo: minimalRepo + "workflows:\n  api:\n    profile: java-service\n    commands:\n      test: ./gradlew test -x slow\n    resources: { memory: 8Gi }\n",
			get: map[string]func(*Config) string{
				"workflows.api.commands.test":    func(c *Config) string { return c.Workflows["api"].Commands.Test },
				"workflows.api.commands.build":   func(c *Config) string { return c.Workflows["api"].Commands.Build },
				"workflows.api.resources.cpu":    func(c *Config) string { return itoa(c.Workflows["api"].Resources.CPU) },
				"workflows.api.resources.memory": func(c *Config) string { return c.Workflows["api"].Resources.Memory },
			},
			want: map[string]want{
				"workflows.api.commands.test":    {"./gradlew test -x slow", SourceRepo},
				"workflows.api.commands.build":   {"./gradlew assemble", SourceProfile("java-service")},
				"workflows.api.resources.cpu":    {"4", SourceProfile("java-service")},
				"workflows.api.resources.memory": {"8Gi", SourceRepo},
			},
		},
		{
			name: "a workflow without profile: takes nothing from any profile",
			repo: minimalRepo + "workflows:\n  web:\n    base: go\n    commands: { build: make, test: make test }\n",
			get: map[string]func(*Config) string{
				"workflows.web.resources.memory": func(c *Config) string { return c.Workflows["web"].Resources.Memory },
				"workflows.web.timeouts.total":   func(c *Config) string { return c.Workflows["web"].Timeouts.Total.String() },
			},
			want: map[string]want{
				"workflows.web.resources.memory": {"8Gi", SourceDefault},
				"workflows.web.timeouts.total":   {"1h30m0s", SourceDefault},
			},
		},
		{
			name: "a repository Dockerfile replaces the profile's image settings",
			repo: minimalRepo + "workflows:\n  api:\n    profile: java-service\n    dockerfile: .fugaro/api.Dockerfile\n",
			get: map[string]func(*Config) string{
				"workflows.api.image.apt":  func(c *Config) string { return strings.Join(c.Workflows["api"].Image.Apt, ",") },
				"workflows.api.dockerfile": func(c *Config) string { return c.Workflows["api"].Dockerfile },
			},
			want: map[string]want{
				"workflows.api.image.apt":  {"", SourceDefault},
				"workflows.api.dockerfile": {".fugaro/api.Dockerfile", SourceRepo},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, res, ps := Resolve([]byte(tc.repo), mustLayer(t, testLayer))
			if len(ps) > 0 {
				t.Fatal(ps)
			}
			for path, w := range tc.want {
				if got := tc.get[path](c); got != w.value {
					t.Errorf("%s = %q, want %q", path, got, w.value)
				}
				if got := res.SourceOf(path); got != w.source {
					t.Errorf("source of %s = %q, want %q", path, got, w.source)
				}
			}
		})
	}
}

func TestResolveProblemsNameTheLayer(t *testing.T) {
	l := mustLayer(t, testLayer)
	for _, tc := range []struct{ name, repo, want string }{
		{"an unknown profile", minimalRepo + "profile: nope\n", `profile: names profile "nope", which project acme's layer does not have (its profiles: java-service, node-web)`},
		{"profile: beside workflows:", minimalRepo + "profile: node-web\nworkflows:\n  web: { base: go, commands: { build: a, test: b } }\n", "applies only to a fugaro.yaml with no workflows:"},
		{"a missing command names the profile", minimalRepo + "workflows:\n  api:\n    profile: node-web\n    commands: { test: '' }\n", "workflows.api.commands.test: is required"},
		{"a value the profile set", minimalRepo + "workflows:\n  api:\n    profile: java-service\n    timeouts: { stage: 3h }\n", "workflows.api.timeouts.stage: must not exceed timeouts.total"},
		{"another project's layer", "version: 1\nproject: other\ngcp_project: acme-fugaro\n", `the project layer given is project "acme"'s`},
		{"an unanchored file", "version: 1\nproject: acme\n", "the project layer applies only to a fugaro.yaml whose gcp_project: names it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, ps := Resolve([]byte(tc.repo), l)
			var msgs []string
			for _, p := range ps {
				msgs = append(msgs, p.String())
			}
			if got := strings.Join(msgs, "; "); !strings.Contains(got, tc.want) {
				t.Fatalf("problems %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveWithoutLayerIsParse(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "config", "valid", "*.yaml"))
	if err != nil || len(files) == 0 {
		t.Fatal("no corpus")
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		c, res, ps := Resolve(data, nil)
		if len(ps) > 0 || c == nil || res.LayerSHA256 != "" || res.ConfigSHA256 != c.SHA256() {
			t.Errorf("%s: %v", f, ps)
		}
	}
	_, _, ps := Resolve([]byte(minimalRepo+"profile: x\n"), nil)
	if len(ps) != 1 || ps[0].Code != CodeNeedsLayer {
		t.Fatalf("a profile with no layer: %v", ps)
	}
	_, _, ps = Resolve([]byte(minimalRepo+"git: { provider: github }\n"), nil)
	if len(ps) == 0 || !strings.Contains(ps[len(ps)-1].Message, "takes one from project acme's layer") {
		t.Fatalf("an anchored file with no workflows: %v", ps)
	}
}

func TestResolveIsDeterministic(t *testing.T) {
	l := mustLayer(t, testLayer)
	first, _, _ := Resolve([]byte(minimalRepo), l)
	for range 20 {
		c, _, _ := Resolve([]byte(minimalRepo), l)
		if c.SHA256() != first.SHA256() {
			t.Fatal("two resolutions of the same inputs differ")
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func btoa(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
```

- [ ] **Step 2: Run it and see it fail**

Run: `go test ./internal/config/ -run 'TestResolve'`
Expected: FAIL to compile: `undefined: Resolve`, `undefined: SourceProject`, `undefined: CodeNeedsLayer`.

- [ ] **Step 3: Implement**

In `internal/config/config.go`, replace the head of `Parse` so that its old body becomes `decodeRepo` without the last two steps:

```go
// Parse decodes fugaro.yaml strictly, applies defaults and validates the result.
// It returns either a config or the problems that prevented one.
func Parse(data []byte) (*Config, []Problem) {
	c, _, ps := Resolve(data, nil)
	return c, ps
}

// decodeRepo decodes a fugaro.yaml strictly, without defaults or
// validation: the repository layer alone, with today's line-numbered
// errors.
func decodeRepo(data []byte) (*Config, []Problem) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, []Problem{{Message: "file is empty"}}
		}
		return nil, yamlProblems(err)
	}
	// The strict decode reads 123 or true as the string "123"; a project
	// name is a YAML string, as ProjectOf (which the runner and the CLI
	// use) insists, so the two cannot disagree about a file.
	if _, err := ProjectOf(data); err != nil {
		return nil, []Problem{problemFromYAML(err.Error())}
	}
	if _, err := GCPProjectOf(data); err != nil {
		return nil, []Problem{problemFromYAML(err.Error())}
	}
	return &c, nil
}
```

(`applyDefaults(&c)` and the `Validate` call move into `Resolve`.)

Add to `Config`, after `Followup`:

```go

	// Layer is the project layer Resolve resolved this config over, nil
	// for none. It is never part of the file or of SHA256: a consumer that
	// holds a resolved config (an image build) passes it on.
	Layer *ProjectLayer `yaml:"-" json:"-"`
```

Create `internal/config/resolve.go`:

```go
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Sources of a resolved value (Resolution.SourceOf).
const (
	SourceDefault = "default"
	SourceProject = "project"
	SourceRepo    = "repo"
)

// SourceProfile is the source of a value a profile set.
func SourceProfile(name string) string { return "profile " + name }

// CodeNeedsLayer is the Code of a problem that only a project layer can
// solve: a profile named, or no workflows, with no layer given.
const CodeNeedsLayer = "needs_project_layer"

// Resolution says how a config was resolved.
type Resolution struct {
	// Sources maps each key a layer set (a fugaro.yaml path, with real
	// workflow names; a list is one key) to its source: SourceProject,
	// SourceRepo or SourceProfile(name). A key in no layer is a default.
	Sources map[string]string
	// LayerSHA256 is the project layer's sha256, "" without one.
	LayerSHA256 string
	// ConfigSHA256 is the resolved config's (Config.SHA256).
	ConfigSHA256 string
}

// SourceOf is the source of path: its own, else its nearest listed
// ancestor's, else SourceDefault.
func (r *Resolution) SourceOf(path string) string {
	for p := path; p != ""; {
		if s, ok := r.Sources[p]; ok {
			return s
		}
		i := strings.LastIndex(p, ".")
		if i < 0 {
			break
		}
		p = p[:i]
	}
	return SourceDefault
}

// SHA256 is the hex sha256 of the config's JSON encoding: the same config
// gives the same sum in the CLI, the runner, Cloud Build and the check job.
func (c *Config) SHA256() string {
	data, err := json.Marshal(c)
	if err != nil {
		panic(err) // a Config always encodes
	}
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// Resolve is a repository's fugaro.yaml resolved over the project layer l
// and Fugaro's defaults (docs/design/layered-config.md §4): for every key,
// the repository's value, else its workflow's profile's, else the project
// layer's defaults', else Fugaro's default. Maps merge key by key; a
// scalar or a list is replaced whole; null is "not set here". A nil l is
// Parse as before 0.6.0, except that naming a profile is an error.
func Resolve(data []byte, l *ProjectLayer) (*Config, *Resolution, []Problem) {
	repo, ps := decodeRepo(data)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	var tree map[string]any
	if err := yaml.Unmarshal(data, &tree); err != nil {
		return nil, nil, yamlProblems(err)
	}
	if l == nil {
		if ps := layerlessProblems(repo); len(ps) > 0 {
			return nil, nil, ps
		}
		applyDefaults(repo)
		if ps := Validate(repo); len(ps) > 0 {
			return nil, nil, noLayerHint(repo, ps)
		}
		res := &Resolution{Sources: map[string]string{}}
		markLeaves(tree, "", SourceRepo, res.Sources)
		res.ConfigSHA256 = repo.SHA256()
		return repo, res, nil
	}
	if ps := anchorProblems(repo, l); len(ps) > 0 {
		return nil, nil, ps
	}
	res := &Resolution{Sources: map[string]string{}, LayerSHA256: l.SHA256}
	merged := map[string]any{}
	if d, ok := l.tree["defaults"].(map[string]any); ok {
		overlay(merged, d, "", SourceProject, res.Sources)
	}
	top := maps1(tree)
	delete(top, "workflows")
	overlay(merged, top, "", SourceRepo, res.Sources)
	wfs, profileOf, ps := resolveWorkflows(tree, repo, l, res.Sources)
	if len(ps) > 0 {
		return nil, nil, ps
	}
	merged["workflows"] = wfs
	out, err := yaml.Marshal(merged)
	if err != nil {
		return nil, nil, []Problem{{Message: "resolving: " + err.Error()}}
	}
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(out))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, nil, yamlProblems(err)
	}
	c.Layer = l
	applyDefaults(&c)
	if ps := Validate(&c); len(ps) > 0 {
		return nil, nil, annotate(ps, res, profileOf)
	}
	res.ConfigSHA256 = c.SHA256()
	return &c, res, nil
}

// layerlessProblems are the keys that need a project layer when there is
// none.
func layerlessProblems(c *Config) []Problem {
	why := "profiles come from the project layer (" + LayerKey + " in the runs bucket of gcp_project), and none applies"
	if c.GCPProject == "" {
		why = "profiles come from the project layer, which applies only to a fugaro.yaml with gcp_project:"
	}
	var ps []Problem
	if c.Profile != "" {
		ps = append(ps, Problem{Path: "profile", Message: fmt.Sprintf("names profile %q, but %s", c.Profile, why), Code: CodeNeedsLayer})
	}
	for _, name := range sortedKeys(c.Workflows) {
		if p := c.Workflows[name].Profile; p != "" {
			ps = append(ps, Problem{Path: "workflows." + name + ".profile", Message: fmt.Sprintf("names profile %q, but %s", p, why), Code: CodeNeedsLayer})
		}
	}
	return ps
}

// noLayerHint adds, to the no-workflows problem of an anchored file, that
// a project layer could supply them.
func noLayerHint(c *Config, ps []Problem) []Problem {
	if c.GCPProject == "" {
		return ps
	}
	for i, p := range ps {
		if p.Path == "workflows" {
			ps[i].Message += fmt.Sprintf("; a fugaro.yaml without workflows takes one from project %s's layer, and none was found (fugaro config layer)", c.Project)
			ps[i].Code = CodeNeedsLayer
		}
	}
	return ps
}

func anchorProblems(c *Config, l *ProjectLayer) []Problem {
	var ps []Problem
	if c.Project != l.Project {
		ps = append(ps, Problem{Path: "project", Message: fmt.Sprintf("is %q, but the project layer given is project %q's", c.Project, l.Project)})
	}
	if c.GCPProject != l.GCPProject {
		ps = append(ps, Problem{Path: "gcp_project", Message: fmt.Sprintf("is %q, but the project layer given is GCP project %q's; the project layer applies only to a fugaro.yaml whose gcp_project: names it", c.GCPProject, l.GCPProject)})
	}
	return ps
}

// resolveWorkflows builds the merged workflows: and says which profile
// each workflow took ("" for none).
func resolveWorkflows(tree map[string]any, repo *Config, l *ProjectLayer, src map[string]string) (map[string]any, map[string]string, []Problem) {
	profiles, _ := l.tree["profiles"].(map[string]any)
	names := strings.Join(sortedKeys(profiles), ", ")
	profileTree := func(name string) (map[string]any, bool) {
		p, ok := profiles[name].(map[string]any)
		if !ok {
			return nil, false
		}
		q := deepCopy(p).(map[string]any)
		delete(q, "description")
		return q, true
	}
	out, profileOf := map[string]any{}, map[string]string{}
	if raw, has := tree["workflows"]; !has || raw == nil {
		name, from := repo.Profile, SourceRepo
		if name == "" {
			name, from = l.DefaultProfile, SourceProject
		}
		if name == "" {
			return nil, nil, []Problem{{Path: "workflows", Message: fmt.Sprintf("must define at least one workflow, or name a profile with profile: (project %s's layer has no default_profile; its profiles: %s)", l.Project, names)}}
		}
		p, ok := profileTree(name)
		if !ok {
			return nil, nil, []Problem{{Path: "profile", Message: fmt.Sprintf("names profile %q, which project %s's layer does not have (its profiles: %s)", name, l.Project, names)}}
		}
		path := "workflows." + ImplicitWorkflow
		w := map[string]any{}
		overlay(w, p, path, SourceProfile(name), src)
		w["profile"] = name
		src[path+".profile"] = from
		out[ImplicitWorkflow], profileOf[ImplicitWorkflow] = w, name
		return out, profileOf, nil
	}
	if repo.Profile != "" {
		return nil, nil, []Problem{{Path: "profile", Message: "applies only to a fugaro.yaml with no workflows:; name each workflow's profile with workflows.<name>.profile"}}
	}
	var ps []Problem
	wm, _ := tree["workflows"].(map[string]any)
	for _, name := range sortedKeys(wm) {
		rw, _ := wm[name].(map[string]any)
		path := "workflows." + name
		w := map[string]any{}
		if pname, _ := rw["profile"].(string); pname != "" {
			p, ok := profileTree(pname)
			if !ok {
				ps = append(ps, Problem{Path: path + ".profile", Message: fmt.Sprintf("names profile %q, which project %s's layer does not have (its profiles: %s)", pname, l.Project, names)})
				continue
			}
			// The inline escape hatch: a repository Dockerfile replaces the
			// profile's generated-image settings.
			if rw["dockerfile"] != nil {
				delete(p, "image")
			}
			overlay(w, p, path, SourceProfile(pname), src)
			profileOf[name] = pname
		}
		overlay(w, rw, path, SourceRepo, src)
		out[name] = w
	}
	return out, profileOf, ps
}

// overlay merges src into dst at path: maps key by key, anything else
// replaced whole; a null in src sets nothing. sources records the leaves
// src set.
func overlay(dst, src map[string]any, path string, source string, sources map[string]string) {
	for _, k := range sortedKeys(src) {
		v := src[k]
		if v == nil {
			continue
		}
		p := k
		if path != "" {
			p = path + "." + k
		}
		if sm, ok := v.(map[string]any); ok {
			dm, ok := dst[k].(map[string]any)
			if !ok {
				forget(sources, p)
				dm = map[string]any{}
				dst[k] = dm
			}
			overlay(dm, sm, p, source, sources)
			continue
		}
		forget(sources, p)
		dst[k] = deepCopy(v)
		sources[p] = source
	}
}

// forget drops the sources of path and everything under it.
func forget(sources map[string]string, path string) {
	for k := range sources {
		if k == path || strings.HasPrefix(k, path+".") {
			delete(sources, k)
		}
	}
}

// markLeaves records source for every leaf of v under path.
func markLeaves(v any, path, source string, sources map[string]string) {
	if m, ok := v.(map[string]any); ok {
		for k, e := range m {
			p := k
			if path != "" {
				p = path + "." + k
			}
			markLeaves(e, p, source, sources)
		}
		return
	}
	if v != nil && path != "" {
		sources[path] = source
	}
}

func deepCopy(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = deepCopy(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = deepCopy(e)
		}
		return out
	}
	return v
}

// annotate says, on each problem of a resolved config, which layer set the
// value, or that neither the repository nor the workflow's profile did.
func annotate(ps []Problem, r *Resolution, profileOf map[string]string) []Problem {
	for i, p := range ps {
		if p.Path == "" {
			continue
		}
		switch src := r.SourceOf(p.Path); {
		case src != SourceDefault && src != SourceRepo:
			ps[i].Message += " (set by " + src + ")"
		case src == SourceDefault:
			parts := strings.SplitN(p.Path, ".", 3)
			if len(parts) >= 2 && parts[0] == "workflows" && profileOf[parts[1]] != "" {
				ps[i].Message += fmt.Sprintf(" (set neither by the repository nor by profile %s)", profileOf[parts[1]])
			}
		}
	}
	return ps
}
```

- [ ] **Step 4: Run it and see it pass**

Run: `go test -race ./internal/config/`
Expected: `ok`. The whole package passes, including the existing `TestCorpus`, `TestExampleIsValid` and every `Parse` test, which now go through `Resolve(data, nil)`.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/resolve.go internal/config/resolve_test.go
git commit -m "layered config task 4: Resolve merges the project layer, a profile and the repository"
```

**Review (own reviewer):** the merge is every consumer's. Check the following:
- `Resolve(data, nil)` is `Parse` for every file without a `profile` key;
- null and `[]` behave as the table in design §4 says;
- a source is forgotten when a narrower layer replaces a subtree;
- `SHA256` is deterministic (maps marshal sorted).

### Task 5: The project layer's JSON schema and the two corpora

**Files:**
- Create: `schemas/project-layer.schema.json`, `testdata/project-layer/valid/*.yaml`, `testdata/project-layer/invalid/*.yaml`, `testdata/config/layered/valid/*.yaml`, `testdata/config/layered/invalid/*.yaml`
- Test: `internal/config/layer_test.go`, `internal/config/resolve_test.go`, `schemas/schemas_test.go`

**Interfaces:**
- Consumes: `ParseProjectLayer`, `Resolve`.
- Produces: the schema (`$id` `https://raw.githubusercontent.com/dimipaun/fugaro/main/schemas/project-layer.schema.json`), used by editors and by Task 18's docs.

- [ ] **Step 1: Write the failing tests and the corpora**

Append to `internal/config/layer_test.go` (add `"os"` and `"path/filepath"` to its imports):

```go
func TestProjectLayerCorpus(t *testing.T) {
	a := LayerAnchor{Project: "aurora", GCPProject: "proj-1234"}
	for _, kind := range []string{"valid", "invalid"} {
		files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "project-layer", kind, "*.yaml"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no %s project layer corpus", kind)
		}
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			l, ps := ParseProjectLayer(data, a)
			switch {
			case kind == "valid" && len(ps) > 0:
				t.Errorf("%s: unexpected problems %v", f, ps)
			case kind == "invalid" && l != nil:
				t.Errorf("%s: parsed without problems, want invalid", f)
			}
		}
	}
}
```

Append to `internal/config/resolve_test.go`:

```go
func TestLayeredCorpus(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "project-layer", "valid", "full.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	l, ps := ParseProjectLayer(data, LayerAnchor{Project: "aurora", GCPProject: "proj-1234"})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	for _, kind := range []string{"valid", "invalid"} {
		files, err := filepath.Glob(filepath.Join("..", "..", "testdata", "config", "layered", kind, "*.yaml"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no layered %s corpus", kind)
		}
		for _, f := range files {
			repo, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			c, _, ps := Resolve(repo, l)
			switch {
			case kind == "valid" && len(ps) > 0:
				t.Errorf("%s: unexpected problems %v", f, ps)
			case kind == "invalid" && c != nil:
				t.Errorf("%s: resolved without problems, want invalid", f)
			}
		}
	}
}
```

Append to `schemas/schemas_test.go`:

```go
func TestProjectLayerSchemaCorpus(t *testing.T) {
	sch := compile(t, "project-layer.schema.json")
	for _, f := range globAll(t, "../testdata/project-layer/valid/*.yaml") {
		data, _ := os.ReadFile(f)
		if err := sch.Validate(yamlInstance(t, data)); err != nil {
			t.Errorf("%s: schema rejects a valid project layer: %v", f, err)
		}
	}
	for _, f := range globAll(t, "../testdata/project-layer/invalid/*.yaml") {
		data, _ := os.ReadFile(f)
		if err := sch.Validate(yamlInstance(t, data)); err == nil {
			t.Errorf("%s: schema accepts an invalid project layer", f)
		}
	}
}

func TestFugaroSchemaLayeredCorpus(t *testing.T) {
	sch := compile(t, "fugaro.schema.json")
	for _, f := range globAll(t, "../testdata/config/layered/valid/*.yaml") {
		data, _ := os.ReadFile(f)
		if err := sch.Validate(yamlInstance(t, data)); err != nil {
			t.Errorf("%s: schema rejects a valid layered config: %v", f, err)
		}
	}
}
```

Create the corpus files, each with exactly this content:

`testdata/project-layer/invalid/bad-base.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
profiles:
  p: { base: java-17 }
```

`testdata/project-layer/invalid/bad-memory.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
profiles:
  p: { resources: { memory: 16G } }
```

`testdata/project-layer/invalid/bad-profile-name.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
profiles:
  P_1: { base: go }
```

`testdata/project-layer/invalid/base-branch-in-defaults.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
defaults:
  git: { base_branch: evil }
```

`testdata/project-layer/invalid/budget-in-defaults.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
defaults:
  budget: { per_run_usd: 100 }
```

`testdata/project-layer/invalid/dockerfile-in-profile.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
profiles:
  p: { dockerfile: x.Dockerfile }
```

`testdata/project-layer/invalid/environments-reserved.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
environments:
  java-17: {}
```

`testdata/project-layer/invalid/followup-in-defaults.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
defaults:
  followup: { trusted: ['1'] }
```

`testdata/project-layer/invalid/instructions-in-defaults.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
defaults:
  agent: { instructions: AGENTS.md }
```

`testdata/project-layer/invalid/reviewers-in-defaults.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
defaults:
  git: { pr: { reviewers: [someone] } }
```

`testdata/project-layer/invalid/secrets-in-profile.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
profiles:
  p:
    secrets: [{ name: db, env: DB }]
```

`testdata/project-layer/invalid/unknown-top.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
colour: red
```

`testdata/project-layer/invalid/version-two.yaml`:

```yaml
version: 2
project: aurora
gcp_project: proj-1234
```

`testdata/project-layer/valid/defaults-only.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
defaults:
  git: { provider: bitbucket }
```

`testdata/project-layer/valid/full.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
defaults:
  git:
    provider: github
    pr: { labels: [fugaro], early_draft: true, checkpoints: true }
  agent:
    auth: api-key
    model: claude-sonnet-5-5
    models: { coder: claude-sonnet-5-5, reviewer: claude-opus-5-5 }
    review_rounds: 2
    max_budget_usd: 25
    first_line_review: auto
    first_line_rounds: 1
    recipe: cheap-loop-senior
profiles:
  java-service:
    description: Gradle service with Postgres
    base: java-services
    image: { apt: [graphviz], setup: ["./scripts/warm.sh"] }
    commands:
      build: ./gradlew assemble
      test: ./gradlew test
      rerun_failed: { command: ./gradlew test, each: "--tests {id}" }
      reports: ["**/build/test-results/**/*.xml"]
    cache:
      - { key: [gradle/libs.versions.toml], paths: [~/.gradle/caches] }
    resources: { cpu: 4, memory: 16Gi }
    timeouts: { total: 2h, stage: 50m, verify: 30m, finalize_reserve: 5m }
    rebuild: { check: daily, max_age: 14d, lockfiles: true, base: true, paths: [build.gradle.kts] }
  node-web:
    base: web-node
    image: { node: "22", skip_build_scripts: true }
    commands: { build: npm run build, test: npm test }
default_profile: java-service
```

`testdata/project-layer/valid/minimal.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
```

`testdata/config/layered/invalid/override-breaks-profile.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
workflows:
  api:
    profile: java-service
    timeouts: { stage: 3h }
```

`testdata/config/layered/invalid/profile-beside-workflows.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
profile: node-web
workflows:
  api: { profile: java-service }
```

`testdata/config/layered/invalid/unanchored.yaml`:

```yaml
version: 1
project: aurora
git: { provider: github }
profile: node-web
```

`testdata/config/layered/invalid/unknown-profile.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
profile: nope
```

`testdata/config/layered/valid/dockerfile-escape.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
workflows:
  api: { profile: java-service, dockerfile: .fugaro/api.Dockerfile }
```

`testdata/config/layered/valid/minimal.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
```

`testdata/config/layered/valid/mixed.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
git: { base_branch: develop }
workflows:
  api: { profile: java-service }
  tool: { base: go, commands: { build: make, test: make test } }
```

`testdata/config/layered/valid/named-profile.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
profile: node-web
```

`testdata/config/layered/valid/override-one-field.yaml`:

```yaml
version: 1
project: aurora
gcp_project: proj-1234
workflows:
  api:
    profile: java-service
    commands: { test: ./gradlew test -x slow }
    secrets: [{ name: db-password, env: DB_PASSWORD }]
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/config/ -run 'Corpus' && go test ./schemas/ -run 'ProjectLayerSchema|LayeredCorpus'`
Expected: the Go corpus tests pass, because the corpus only exercises Tasks 2 to 4. The schema test FAILS: `open project-layer.schema.json: no such file or directory`.

- [ ] **Step 3: Implement**

Create `schemas/project-layer.schema.json`:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://raw.githubusercontent.com/dimipaun/fugaro/main/schemas/project-layer.schema.json",
  "title": "fugaro/project-layer.yaml",
  "description": "A Fugaro project's layer: project-wide defaults and workflow profiles for every repository whose fugaro.yaml names the project and its gcp_project. See docs/project-layer.md.",
  "type": "object",
  "additionalProperties": false,
  "required": ["version", "project", "gcp_project"],
  "properties": {
    "version": { "const": 1 },
    "project": { "type": "string", "pattern": "^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$" },
    "gcp_project": { "type": "string", "pattern": "^[a-z][a-z0-9-]{4,28}[a-z0-9]$" },
    "defaults": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "git": {
          "type": "object",
          "additionalProperties": false,
          "properties": {
            "provider": { "enum": ["github", "bitbucket"] },
            "pr": {
              "type": "object",
              "additionalProperties": false,
              "properties": {
                "labels": { "type": "array", "items": { "type": "string" } },
                "early_draft": { "type": "boolean" },
                "checkpoints": { "type": "boolean" }
              }
            }
          }
        },
        "agent": {
          "type": "object",
          "additionalProperties": false,
          "properties": {
            "auth": { "enum": ["vertex", "api-key", "oauth"] },
            "model": { "type": "string", "maxLength": 100, "pattern": "^\\S*$" },
            "models": {
              "type": "object",
              "additionalProperties": false,
              "properties": {
                "coder": { "type": "string", "maxLength": 100, "pattern": "^\\S*$" },
                "reviewer": { "type": "string", "maxLength": 100, "pattern": "^\\S*$" },
                "background": { "type": "string", "maxLength": 100, "pattern": "^\\S*$" }
              }
            },
            "review_rounds": { "type": "integer", "minimum": 1, "maximum": 10 },
            "max_budget_usd": { "type": "number", "minimum": 0 },
            "first_line_review": { "enum": ["auto", "on", "off"] },
            "first_line_rounds": { "type": "integer", "minimum": 1, "maximum": 3 },
            "recipe": { "type": "string", "pattern": "^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$" }
          }
        }
      }
    },
    "profiles": {
      "type": "object",
      "propertyNames": { "pattern": "^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$" },
      "additionalProperties": { "$ref": "#/$defs/profile" }
    },
    "default_profile": { "type": "string", "pattern": "^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$" }
  },
  "$defs": {
    "duration": { "type": "string", "minLength": 2, "pattern": "^([0-9]+d)?([0-9]+h)?([0-9]+m)?([0-9]+s)?$" },
    "profile": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "description": { "type": "string", "maxLength": 200, "pattern": "^[^\\r\\n]*$" },
        "base": { "enum": ["go", "java-services", "web-node"] },
        "image": {
          "type": "object",
          "additionalProperties": false,
          "properties": {
            "node": { "type": ["string", "integer"], "pattern": "^[0-9]+(\\.[0-9]+\\.[0-9]+)?$" },
            "skip_build_scripts": { "type": "boolean" },
            "apt": { "type": "array", "items": { "type": "string", "pattern": "^[a-z0-9][a-z0-9+.-]+(=[A-Za-z0-9.+~:-]+)?$" } },
            "setup": { "type": "array", "items": { "type": "string", "pattern": "^[^\\r\\n]*\\S[^\\r\\n]*$", "not": { "pattern": "<<|\\\\\\s*$|^[\\s\\v]*[-\\[]" } } }
          }
        },
        "commands": {
          "type": "object",
          "additionalProperties": false,
          "properties": {
            "build": { "type": "string", "minLength": 1 },
            "test": { "type": "string", "minLength": 1 },
            "rerun_failed": {
              "type": "object",
              "additionalProperties": false,
              "required": ["command", "each"],
              "properties": { "command": { "type": "string", "minLength": 1 }, "each": { "type": "string", "pattern": "\\{id\\}" } }
            },
            "reports": { "type": "array", "items": { "type": "string" } }
          }
        },
        "cache": {
          "type": "array",
          "items": {
            "type": "object",
            "additionalProperties": false,
            "required": ["key", "paths"],
            "properties": {
              "key": { "type": "array", "minItems": 1, "items": { "type": "string" } },
              "paths": { "type": "array", "minItems": 1, "items": { "type": "string" } }
            }
          }
        },
        "resources": {
          "type": "object",
          "additionalProperties": false,
          "properties": { "cpu": { "type": "integer", "minimum": 1 }, "memory": { "type": "string", "pattern": "^[1-9][0-9]*(Mi|Gi)$" } }
        },
        "timeouts": {
          "type": "object",
          "additionalProperties": false,
          "properties": {
            "total": { "$ref": "#/$defs/duration" },
            "stage": { "$ref": "#/$defs/duration" },
            "verify": { "$ref": "#/$defs/duration" },
            "finalize_reserve": { "$ref": "#/$defs/duration" }
          }
        },
        "rebuild": {
          "type": "object",
          "additionalProperties": false,
          "properties": {
            "check": { "enum": ["daily", "off"] },
            "max_age": { "$ref": "#/$defs/duration" },
            "lockfiles": { "type": "boolean" },
            "base": { "type": "boolean" },
            "paths": { "type": "array", "items": { "type": "string", "minLength": 1 } }
          }
        }
      }
    }
  }
}
```

- [ ] **Step 4: Run them and see them pass**

Run: `go test -race ./internal/config/ ./schemas/`
Expected: `ok` for both.

- [ ] **Step 5: Commit**

```bash
git add schemas/project-layer.schema.json schemas/schemas_test.go testdata/project-layer testdata/config/layered internal/config/layer_test.go internal/config/resolve_test.go
git commit -m "layered config task 5: the project layer schema and the layered corpora"
```

PR 1 ends with one full `go test -race ./internal/config/... ./schemas/...` and a PR titled "layered config 1/6: the config core".

---
## PR 2: task and runner

### Task 6: `task.ProjectLayer` in `task.json`

**Files:**
- Modify: `internal/task/task.go`, `schemas/task.schema.json`
- Create: `testdata/task/valid/project-layer.json`, `testdata/task/invalid/project-layer-sha.json`
- Test: `internal/task/task_test.go`

**Interfaces:**
- Consumes: `config.LayerSum`, `config.LayerMaxBytes` (Task 2).
- Produces:
  ```go
  // in Spec, after Recipe:
  ProjectLayer *ProjectLayer `json:"project_layer,omitempty"`
  type ProjectLayer struct {
      SHA256     string `json:"sha256"`
      Generation int64  `json:"generation,omitempty"`
      YAML       string `json:"yaml"`
  }
  ```

- [ ] **Step 1: Write the failing test**

Append to `internal/task/task_test.go` (add `"github.com/dimipaun/fugaro/internal/config"` to its imports if missing):

```go
func TestProjectLayerField(t *testing.T) {
	text := "version: 1\nproject: aurora\ngcp_project: proj-1234\n"
	ok := Spec{Version: 1, RunID: "20261008-100000-abcd", Repo: "acme/app", Ref: "main", Task: "x",
		ProjectLayer: &ProjectLayer{SHA256: config.LayerSum([]byte(text)), Generation: 7, YAML: text}}
	data, err := ok.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse(data)
	if err != nil || got.ProjectLayer == nil || *got.ProjectLayer != *ok.ProjectLayer {
		t.Fatalf("round trip = %+v, %v", got, err)
	}
	for _, tc := range []struct {
		pl   ProjectLayer
		want string
	}{
		{ProjectLayer{SHA256: strings.Repeat("0", 64), YAML: text}, "project_layer.sha256 does not match"},
		{ProjectLayer{SHA256: config.LayerSum(nil)}, "project_layer.yaml must hold the project layer"},
		{ProjectLayer{SHA256: config.LayerSum([]byte(text)), YAML: text, Generation: -1}, "project_layer.generation"},
	} {
		bad := ok
		bad.ProjectLayer = &tc.pl
		if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want %q", tc.pl, err, tc.want)
		}
	}
}
```

Create `testdata/task/valid/project-layer.json`:

```json
{ "version": 1, "run_id": "20261008-100000-abcd", "repo": "acme/app", "ref": "main", "task": "Do it", "project_layer": { "sha256": "edc48455071fab6211dd72dc473e941d10a1659cb12ab47a7123f858bf592d96", "generation": 7, "yaml": "version: 1\nproject: aurora\ngcp_project: proj-1234\n" } }
```

Create `testdata/task/invalid/project-layer-sha.json`:

```json
{ "version": 1, "run_id": "20261008-100000-abcd", "repo": "acme/app", "ref": "main", "task": "Do it", "project_layer": { "sha256": "short", "yaml": "version: 1\n" } }
```

- [ ] **Step 2: Run it and see it fail**

Run: `go test ./internal/task/ -run TestProjectLayerField && go test ./schemas/ -run TestTaskSchemaCorpus`
Expected: FAIL to compile, with `unknown field ProjectLayer in struct literal`. The schema corpus test fails on `project-layer.json` with `additional properties 'project_layer' not allowed`.

- [ ] **Step 3: Implement**

In `internal/task/task.go`, add the field to `Spec` after `Recipe`:

```go
	// ProjectLayer is the project layer the launching CLI read
	// (docs/design/layered-config.md §8): its exact text, which the runner
	// resolves fugaro.yaml against. Nil: none. Runners before 0.6.0 refuse
	// the field, so the CLI checks the job image first (layeredSince).
	ProjectLayer *ProjectLayer `json:"project_layer,omitempty"`
```

Add the type after `Recipe`:

```go
// ProjectLayer is a project layer as the launching CLI read it.
type ProjectLayer struct {
	SHA256     string `json:"sha256"`               // of YAML
	Generation int64  `json:"generation,omitempty"` // the object's, for display
	YAML       string `json:"yaml"`                 // the exact text
}
```

In `Validate`, before `return errors.Join(errs...)`:

```go
	if pl := s.ProjectLayer; pl != nil {
		switch {
		case pl.YAML == "" || len(pl.YAML) > config.LayerMaxBytes:
			bad("task spec: project_layer.yaml must hold the project layer, at most %d bytes", config.LayerMaxBytes)
		case pl.SHA256 != config.LayerSum([]byte(pl.YAML)):
			bad("task spec: project_layer.sha256 does not match project_layer.yaml")
		}
		if pl.Generation < 0 {
			bad("task spec: project_layer.generation must not be negative")
		}
	}
```

In `schemas/task.schema.json`, add after the `"recipe"` property:

```json
    "project_layer": {
      "type": "object",
      "additionalProperties": false,
      "required": ["sha256", "yaml"],
      "description": "The project layer the launching CLI read (docs/design/layered-config.md §8); the runner resolves fugaro.yaml against it. Absent: none. Needs a 0.6.0 runner.",
      "properties": {
        "sha256": { "type": "string", "pattern": "^[0-9a-f]{64}$" },
        "generation": { "type": "integer", "minimum": 0 },
        "yaml": { "type": "string", "minLength": 1, "maxLength": 65536 }
      }
    },
```

- [ ] **Step 4: Run it and see it pass**

Run: `go test -race ./internal/task/ && go test -race ./schemas/ -run TestTaskSchemaCorpus`
Expected: `ok` for both.

- [ ] **Step 5: Commit**

```bash
git add internal/task/task.go internal/task/task_test.go schemas/task.schema.json testdata/task/valid/project-layer.json testdata/task/invalid/project-layer-sha.json
git commit -m "layered config task 6: task.json carries the project layer's exact text"
```

### Task 7: The runner resolves over the embedded layer and records it

**Files:**
- Modify: `internal/runner/runner.go` (first-run config read; `parseConfig` replaced), `internal/runner/followup.go` (`readBaseConfig`), `internal/runstore/runstore.go`, `schemas/result.schema.json`
- Create: `internal/runner/layer_test.go`
- Test: `schemas/schemas_test.go`

**Interfaces:**
- Consumes: `task.ProjectLayer` (Task 6); `config.ParseProjectLayer`, `config.Resolve`, `config.ProjectOf`, `config.GCPProjectOf` (Tasks 2 and 4).
- Produces:
  ```go
  // runstore
  type ProjectLayerRecord struct { SHA256 string; Generation int64; Applied bool }
  // Record gains:
  ProjectLayer *ProjectLayerRecord `json:"project_layer,omitempty"`
  ConfigSHA256 string              `json:"config_sha256,omitempty"`
  // runner
  func (r *run) resolveConfig(data []byte) (*config.Config, error) // replaces parseConfig
  func problemsText(ps []config.Problem) string
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/runner/layer_test.go`:

```go
package runner_test

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// runnerLayer's profile app is the fixture's workflow app, minus its
// secrets, which are repo-only.
const runnerLayer = `version: 1
project: aurora
gcp_project: proj-1234
defaults:
  git: { provider: github }
  agent: { auth: api-key, review_rounds: 2 }
profiles:
  app:
    base: web-node
    commands:
      build: sh build.sh
      test: sh test.sh
      rerun_failed: { command: sh test.sh, each: "{id}" }
      reports: ["build/test-results/*.xml"]
    timeouts: { total: 5m, stage: 2m, verify: 1m, finalize_reserve: 30s }
default_profile: app
`

// layeredRepo names profile app and adds the fixture's repo-only secret.
const layeredRepo = `version: 1
project: aurora
gcp_project: proj-1234
workflows:
  app:
    profile: app
    secrets:
      - { name: fixture-fails, env: FIXTURE_FAILS_FILE }
`

func layerSpec(text string) *task.Spec {
	return &task.Spec{Version: 1, RunID: runID, Repo: "acme/app", Ref: "main", Task: "Add a feature",
		ProjectLayer: &task.ProjectLayer{SHA256: config.LayerSum([]byte(text)), Generation: 3, YAML: text}}
}

// Review Focus 1.
func TestRunnerRecordsTheLayer(t *testing.T) {
	h := newHarness(t, layeredRepo, layerSpec(runnerLayer))
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	pl := rec.ProjectLayer
	if pl == nil || !pl.Applied || pl.SHA256 != config.LayerSum([]byte(runnerLayer)) || pl.Generation != 3 {
		t.Fatalf("project_layer = %+v", pl)
	}
	want, _, ps := config.Resolve([]byte(layeredRepo), mustRunnerLayer(t))
	if len(ps) > 0 || rec.ConfigSHA256 != want.SHA256() || rec.Workflow != "app" {
		t.Fatalf("config_sha256 = %s, want %s (workflow %s)", rec.ConfigSHA256, want.SHA256(), rec.Workflow)
	}
}

// Review Focus 5: a launch from outside a checkout may carry the layer for
// a file that is not anchored; the runner ignores it and says so.
func TestRunnerIgnoresTheLayerForAnUnanchoredFile(t *testing.T) {
	h := newHarness(t, "", layerSpec(runnerLayer)) // the fixture's own fugaro.yaml: no gcp_project
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.ProjectLayer == nil || rec.ProjectLayer.Applied || rec.ConfigSHA256 == "" {
		t.Fatalf("project_layer = %+v, config_sha256 %q", rec.ProjectLayer, rec.ConfigSHA256)
	}
}

func TestRunnerRefusesAnInvalidLayer(t *testing.T) {
	bad := "version: 1\nproject: aurora\ngcp_project: proj-1234\ndefaults:\n  budget: { per_run_usd: 1 }\n"
	h := newHarness(t, layeredRepo, layerSpec(bad))
	rec, err := h.run(t, implement("feature"))
	if err == nil || rec.Status != runstore.StatusInfraError || !strings.Contains(rec.Reason, "project layer") ||
		!strings.Contains(rec.Reason, "budget.per_run_usd may only be set in: repo") || len(h.agent.calls) != 0 {
		t.Fatalf("rec = %+v, err = %v, calls %d", rec, err, len(h.agent.calls))
	}
}

func TestRunnerWithoutALayerRecordsOnlyTheConfig(t *testing.T) {
	h := newHarness(t, "", nil)
	rec, err := h.run(t, implement("feature"), review("ship", 0))
	if err != nil {
		t.Fatal(err)
	}
	if rec.ProjectLayer != nil || len(rec.ConfigSHA256) != 64 {
		t.Fatalf("project_layer = %+v, config_sha256 %q", rec.ProjectLayer, rec.ConfigSHA256)
	}
}

func mustRunnerLayer(t *testing.T) *config.ProjectLayer {
	t.Helper()
	l, ps := config.ParseProjectLayer([]byte(runnerLayer), config.LayerAnchor{})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	return l
}
```

Append to `schemas/schemas_test.go`:

```go
func TestResultSchemaProjectLayer(t *testing.T) {
	sch := compile(t, "result.schema.json")
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		pl  *runstore.ProjectLayerRecord
		sum string
		ok  bool
	}{
		{&runstore.ProjectLayerRecord{SHA256: strings.Repeat("a", 64), Generation: 3, Applied: true}, strings.Repeat("b", 64), true},
		{nil, strings.Repeat("b", 64), true},
		{&runstore.ProjectLayerRecord{SHA256: "short", Applied: true}, "", false},
		{nil, "short", false},
	} {
		data, err := json.Marshal(runstore.Record{Version: 1, RunID: "20261008-100000-abcd", Repo: "acme/app",
			Status: runstore.StatusRunning, Stage: "implement", StartedAt: at, ProjectLayer: tc.pl, ConfigSHA256: tc.sum})
		if err != nil {
			t.Fatal(err)
		}
		inst, _ := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err := sch.Validate(inst); (err == nil) != tc.ok {
			t.Errorf("%+v %q: err = %v, want ok %v", tc.pl, tc.sum, err, tc.ok)
		}
	}
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test -race ./internal/runner/ -run 'TestRunner(RecordsTheLayer|IgnoresTheLayer|RefusesAnInvalidLayer|WithoutALayer)'`
Expected: FAIL to compile, with `rec.ProjectLayer undefined (type *runstore.Record has no field or method ProjectLayer)`.

- [ ] **Step 3: Implement**

In `internal/runstore/runstore.go`, add to `Record` after `Recipe`:

```go
	// ProjectLayer is the project layer the run's task carried
	// (docs/design/layered-config.md §8); absent when it carried none, and
	// in records from before 0.6.0.
	ProjectLayer *ProjectLayerRecord `json:"project_layer,omitempty"`
	// ConfigSHA256 is the sha256 of the resolved fugaro.yaml, before the
	// per-task overrides (config.Config.SHA256); absent before 0.6.0.
	ConfigSHA256 string `json:"config_sha256,omitempty"`
```

Add after `RecipeRecord`:

```go
// ProjectLayerRecord traces a run to the exact project layer it was given.
type ProjectLayerRecord struct {
	SHA256     string `json:"sha256"`
	Generation int64  `json:"generation,omitempty"`
	// Applied is false when fugaro.yaml at the ref did not name the layer's
	// project and gcp_project (a launch from outside a checkout), so the
	// run resolved without it.
	Applied bool `json:"applied"`
}
```

In `schemas/result.schema.json`, add next to `"recipe"`:

```json
    "project_layer": {
      "type": "object",
      "additionalProperties": false,
      "required": ["sha256", "applied"],
      "properties": {
        "sha256": { "type": "string", "pattern": "^[0-9a-f]{64}$" },
        "generation": { "type": "integer", "minimum": 0 },
        "applied": { "type": "boolean" }
      }
    },
    "config_sha256": { "type": "string", "pattern": "^[0-9a-f]{64}$" },
```

In `internal/runner/runner.go`, replace the free function `parseConfig` (the one at the end of the config section) with:

```go
// resolveConfig resolves fugaro.yaml (data) over the task's project layer
// (docs/design/layered-config.md §8) and records which layer and which
// resolved config the run uses. The layer applies only to a file whose
// project: and gcp_project: are the layer's; a launch from outside a
// checkout may carry one for a file that is not, and the record says the
// layer was not applied. An invalid layer fails bootstrap, as an invalid
// fugaro.yaml does.
func (r *run) resolveConfig(data []byte) (*config.Config, error) {
	var layer *config.ProjectLayer
	if pl := r.spec.ProjectLayer; pl != nil {
		l, ps := config.ParseProjectLayer([]byte(pl.YAML), config.LayerAnchor{Project: r.d.Project})
		if len(ps) > 0 {
			return nil, fmt.Errorf("the task's project layer (sha256 %s) is invalid: %s", pl.SHA256, problemsText(ps))
		}
		project, _ := config.ProjectOf(data)
		gcp, _ := config.GCPProjectOf(data)
		applied := project == l.Project && gcp == l.GCPProject
		r.rec.ProjectLayer = &runstore.ProjectLayerRecord{SHA256: pl.SHA256, Generation: pl.Generation, Applied: applied}
		if applied {
			layer = l
		} else {
			r.d.Log.Info("project layer not applied: fugaro.yaml does not name its project and gcp_project", "layer_project", l.Project, "layer_gcp_project", l.GCPProject)
		}
	}
	cfg, res, problems := config.Resolve(data, layer)
	if len(problems) > 0 {
		return nil, fmt.Errorf("fugaro.yaml is invalid: %s", problemsText(problems))
	}
	r.rec.ConfigSHA256 = res.ConfigSHA256
	return cfg, nil
}

// problemsText is problems as one line, "; "-separated.
func problemsText(ps []config.Problem) string {
	msgs := make([]string, len(ps))
	for i, p := range ps {
		msgs[i] = p.String()
	}
	return strings.Join(msgs, "; ")
}
```

In `bootstrap`'s first-run branch, replace `if cfg, err = parseConfig(data); err != nil {` with `if cfg, err = r.resolveConfig(data); err != nil {`. In `internal/runner/followup.go`'s `readBaseConfig`, replace `cfg, err := parseConfig(data)` with `cfg, err := r.resolveConfig(data)`.

- [ ] **Step 4: Run them and see them pass**

Run: `go test -race ./internal/runner/ -run 'TestRunner(RecordsTheLayer|IgnoresTheLayer|RefusesAnInvalidLayer|WithoutALayer)|TestRecipe|TestFollowUp' && go test -race ./schemas/ -run 'TestResultSchema'`
Expected: `ok` for both. The recipe and follow-up tests confirm that the two replaced call sites behave as before without a layer.

- [ ] **Step 5: Commit**

```bash
git add internal/runner/runner.go internal/runner/followup.go internal/runner/layer_test.go internal/runstore/runstore.go schemas/result.schema.json schemas/schemas_test.go
git commit -m "layered config task 7: the runner resolves over the task's project layer and records both sums"
```

PR 2 ends with one full `go test -race ./internal/runner/... ./internal/task/... ./internal/runstore/... ./schemas/...` (about 20 minutes, in the foreground) and a PR titled "layered config 2/6: task and runner".

---
## PR 3: CLI resolution and commands

### Task 8: The offline cache of the project layer (`internal/localcfg/layercache.go`)

**Files:**
- Create: `internal/localcfg/layercache.go`
- Test: `internal/localcfg/layercache_test.go`

**Interfaces:**
- Consumes: `cachePath`, `writeCacheFile`, `SharedCacheEntry` and its `UsableOffline` (existing); `config.LayerMaxBytes` (Task 2).
- Produces:
  ```go
  func LoadLayerCache(getenv func(string) string, project string) (SharedCacheEntry, bool)
  func SaveLayerCache(getenv func(string) string, project string, e SharedCacheEntry) error
  func DropLayerCache(getenv func(string) string, project string) error
  ```

- [ ] **Step 1: Write the failing test**

Create `internal/localcfg/layercache_test.go`:

```go
package localcfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLayerCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	getenv := func(k string) string {
		if k == "XDG_CACHE_HOME" {
			return dir
		}
		return ""
	}
	if _, ok := LoadLayerCache(getenv, "aurora"); ok {
		t.Fatal("a cache entry before any save")
	}
	at := time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)
	e := SharedCacheEntry{GCPProject: "proj-1234", Bucket: "fugaro-runs-proj-1234", Generation: 7, CheckedAt: at, YAML: "version: 1\n"}
	if err := SaveLayerCache(getenv, "aurora", e); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "fugaro", "project-layers", "aurora.json")
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("cache file %s: %v %v", path, fi, err)
	}
	got, ok := LoadLayerCache(getenv, "aurora")
	if !ok || got != e {
		t.Fatalf("loaded %+v, %v", got, ok)
	}
	if !got.UsableOffline(at.Add(6*24*time.Hour)) || got.UsableOffline(at.Add(8*24*time.Hour)) {
		t.Fatal("the offline allowance is not 7 days")
	}
	if err := DropLayerCache(getenv, "aurora"); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadLayerCache(getenv, "aurora"); ok {
		t.Fatal("an entry after the drop")
	}
	if err := DropLayerCache(getenv, "aurora"); err != nil {
		t.Fatalf("dropping a missing entry: %v", err)
	}
	if err := SaveLayerCache(getenv, "../x", e); err == nil || !strings.Contains(err.Error(), "not a project name") {
		t.Fatalf("a bad project name: %v", err)
	}
	big := e
	big.YAML = strings.Repeat("x", 64<<10+1)
	if err := SaveLayerCache(getenv, "aurora", big); err != nil {
		t.Fatal(err)
	}
	if _, ok := LoadLayerCache(getenv, "aurora"); ok {
		t.Fatal("an oversized entry was loaded")
	}
}
```

- [ ] **Step 2: Run it and see it fail**

Run: `go test ./internal/localcfg/ -run TestLayerCacheRoundTrip`
Expected: FAIL to compile: `undefined: LoadLayerCache`.

- [ ] **Step 3: Implement**

Create `internal/localcfg/layercache.go`:

```go
package localcfg

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"

	"github.com/dimipaun/fugaro/internal/config"
)

// The project layer's cache (docs/design/layered-config.md §7, decision
// L13): unlike the shared config's and the recipes', it is never "fresh".
// Every command reads the bucket; the entry stands in only when the bucket
// cannot be reached at all, for SharedOfflineFor.

// layerCachePath is $XDG_CACHE_HOME/fugaro/project-layers/<project>.json,
// else under ~/.cache.
func layerCachePath(getenv func(string) string, project string) (string, error) {
	return cachePath(getenv, "project-layers", project)
}

// LoadLayerCache is the project's cached project layer, whatever its age:
// the caller decides with UsableOffline and parses YAML again on every use.
// Any trouble is a miss.
func LoadLayerCache(getenv func(string) string, project string) (SharedCacheEntry, bool) {
	path, err := layerCachePath(getenv, project)
	if err != nil {
		return SharedCacheEntry{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) > 6*config.LayerMaxBytes+1024 {
		return SharedCacheEntry{}, false
	}
	var e SharedCacheEntry
	if json.Unmarshal(data, &e) != nil || e.GCPProject == "" || e.Bucket == "" || e.CheckedAt.IsZero() || e.YAML == "" || len(e.YAML) > config.LayerMaxBytes {
		return SharedCacheEntry{}, false
	}
	return e, true
}

// SaveLayerCache stores the entry, 0600 in a 0700 directory, atomically.
// Best effort: callers ignore the error.
func SaveLayerCache(getenv func(string) string, project string, e SharedCacheEntry) error {
	path, err := layerCachePath(getenv, project)
	if err != nil {
		return err
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return writeCacheFile(path, data)
}

// DropLayerCache removes the entry; a missing one is not an error.
func DropLayerCache(getenv func(string) string, project string) error {
	path, err := layerCachePath(getenv, project)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run it and see it pass**

Run: `go test -race ./internal/localcfg/ -run TestLayerCacheRoundTrip`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/localcfg/layercache.go internal/localcfg/layercache_test.go
git commit -m "layered config task 8: the project layer's offline-only cache"
```

### Task 9: Finding the layer and resolving a checkout: the CLI's one seam (`internal/cli/layer_resolve.go`)

**Files:**
- Create: `internal/cli/layer_resolve.go`, `internal/cli/layer_helpers_test.go`
- Test: `internal/cli/layer_resolve_test.go`

**Interfaces:**
- Consumes:
  - `config.ParseProjectLayer`, `config.Resolve`, `config.ProjectOf`, `config.GCPProjectOf`, `config.LayerKey`, `config.LayerMaxBytes` (PR 1);
  - `localcfg.LoadLayerCache`, `SaveLayerCache` and `DropLayerCache` (Task 8);
  - `isUnreachable`, `bucketErrFor`, `ageDays`, `userErr`, `remote` and `pluginwire.Printable` (existing).
- Produces:
  ```go
  type layerOptions struct {
      File    string // --project-layer: read this file, not the bucket
      Offline bool   // --offline: the cache only
      Data    []byte // a layer already read and checked (Cloud Build, the check job)
      Where   string // where Data came from, for messages
      NoBucket bool  // never read the canonical object (the check job)
  }
  type foundLayer struct {
      Layer      *config.ProjectLayer // nil: none applies
      Where      string
      Generation int64
      CheckedAt  time.Time
      Unknown    bool   // whether one applies could not be told (offline, nothing cached)
      Note       string // a warning: a cached copy, or why none applies
  }
  type resolvedFile struct { Cfg *config.Config; Res *config.Resolution; Layer foundLayer; Problems []config.Problem }
  var layerRead func(ctx context.Context, b *blobx.Bucket) ([]byte, int64, error) // test seam
  func findLayer(ctx context.Context, getenv func(string) string, data []byte, lc *localcfg.Config, o layerOptions, now time.Time) (foundLayer, error)
  func resolveFugaroYAML(ctx context.Context, data []byte, lc *localcfg.Config, o layerOptions) (resolvedFile, error)
  func parseCheckoutFugaroYAML(ctx context.Context, data []byte, lc *localcfg.Config) (*config.Config, []config.Problem)
  func readLayerFile(path string) ([]byte, error)
  func layerProblemsText(ps []config.Problem) string
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/cli/layer_helpers_test.go`:

```go
package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/backend/gcp"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/testutil"
)

// testProjectLayer is aurora's project layer in the cloud fixture: profile
// svc is recipeCheckout's workflow svc.
const testProjectLayer = `version: 1
project: aurora
gcp_project: proj-1234
defaults:
  git: { provider: github }
  agent: { auth: api-key }
profiles:
  svc:
    base: web-node
    commands: { build: sh build.sh, test: sh test.sh }
default_profile: svc
`

const minimalAnchored = "version: 1\nproject: aurora\ngcp_project: proj-1234\n"

// publishedLayer moves the fixture to the default runs bucket name and,
// unless text is "", writes text as the project layer.
func publishedLayer(t *testing.T, f *cloudFixture, text string) {
	t.Helper()
	projectRecipesFixture(t, f, nil)
	if text != "" {
		writeBucketFile(t, f, config.LayerKey, text)
	}
}

// layerWithDefaults is testProjectLayer with extra lines (indented two
// spaces) added under defaults:.
func layerWithDefaults(extra string) string {
	return strings.Replace(testProjectLayer, "  agent: { auth: api-key }\n", "  agent: { auth: api-key }\n"+extra, 1)
}

// isolateCache gives the test a cache directory of its own.
func isolateCache(t *testing.T) { t.Setenv("XDG_CACHE_HOME", t.TempDir()) }

// layerCheckout makes the current directory a checkout of acme/other whose
// fugaro.yaml is repoYAML, with what web-node's checks look for.
func layerCheckout(t *testing.T, f *cloudFixture, repoYAML string) string {
	t.Helper()
	testutil.IsolateGit(t)
	f.run.AddJob(gcp.JobName(mustSlug("github", "acme/other"), config.ImplicitWorkflow), "2", "4Gi")
	dir := t.TempDir()
	testutil.Git(t, dir, "init", "-q")
	testutil.Git(t, dir, "remote", "add", "origin", "git@github.com:acme/other.git")
	testutil.WriteFiles(t, dir, map[string]string{"fugaro.yaml": repoYAML, "build.sh": "true\n", "test.sh": "true\n", "package-lock.json": "{}\n"})
	t.Chdir(dir)
	return dir
}
```

Create `internal/cli/layer_resolve_test.go`:

```go
package cli

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/googleapi"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
)

var layerNow = time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)

// Review Focus 1: no fresh window, so a publish is seen at once.
func TestFindLayerReadsTheBucketEveryTime(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	lc := fileEnv(t, f).lc
	first, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow)
	if err != nil || first.Layer == nil || first.Layer.SHA256 != config.LayerSum([]byte(testProjectLayer)) {
		t.Fatalf("first = %+v, %v", first, err)
	}
	v2 := strings.Replace(testProjectLayer, "auth: api-key", "auth: oauth", 1)
	writeBucketFile(t, f, config.LayerKey, v2)
	second, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow.Add(time.Minute))
	if err != nil || second.Layer.SHA256 != config.LayerSum([]byte(v2)) || second.Note != "" {
		t.Fatalf("second = %+v, %v", second, err)
	}
}

func TestFindLayerCacheOnlyWhenUnreachable(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	lc := fileEnv(t, f).lc
	if _, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow); err != nil {
		t.Fatal(err)
	}
	read := layerRead
	t.Cleanup(func() { layerRead = read })
	layerRead = func(context.Context, *blobx.Bucket) ([]byte, int64, error) {
		return nil, 0, &net.OpError{Op: "dial", Err: errors.New("no route to host")}
	}
	got, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow.Add(3*24*time.Hour))
	if err != nil || got.Layer == nil || !strings.Contains(got.Note, "using the cached project layer of aurora, 3 days old") {
		t.Fatalf("unreachable: %+v, %v", got, err)
	}
	if _, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow.Add(8*24*time.Hour)); err == nil {
		t.Fatal("an 8-day-old cache stood in")
	}
	layerRead = func(context.Context, *blobx.Bucket) ([]byte, int64, error) {
		return nil, 0, &googleapi.Error{Code: 403, Message: "forbidden"}
	}
	if _, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), lc, layerOptions{}, layerNow.Add(time.Hour)); err == nil || !strings.Contains(err.Error(), "no access") {
		t.Fatalf("a 403 fell back to the cache: %v", err)
	}
}

// Review Focus 4.
func TestFindLayerRefusesAnInvalidObject(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, layerWithDefaults("  followup: { trusted: ['1'] }\n"))
	_, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), fileEnv(t, f).lc, layerOptions{}, layerNow)
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro/project-layer.yaml is invalid") ||
		!strings.Contains(err.Error(), "followup.trusted may only be set in: repo") {
		t.Fatalf("err = %v", err)
	}
}

func TestFindLayerAbsentIsNone(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	got, err := findLayer(context.Background(), os.Getenv, []byte(minimalAnchored), fileEnv(t, f).lc, layerOptions{}, layerNow)
	if err != nil || got.Layer != nil || got.Unknown {
		t.Fatalf("got %+v, %v", got, err)
	}
}

// Review Focus 5: no gcp_project:, no layer, no bucket read.
func TestUnanchoredCheckoutIgnoresTheLayer(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	read := layerRead
	t.Cleanup(func() { layerRead = read })
	reads := 0
	layerRead = func(ctx context.Context, b *blobx.Bucket) ([]byte, int64, error) { reads++; return read(ctx, b) }
	got, err := findLayer(context.Background(), os.Getenv, []byte("version: 1\nproject: aurora\n"), fileEnv(t, f).lc, layerOptions{}, layerNow)
	if err != nil || got.Layer != nil || reads != 0 {
		t.Fatalf("got %+v, %v, %d reads", got, err, reads)
	}
}

func TestResolveFugaroYAMLMinimal(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	rf, err := resolveFugaroYAML(context.Background(), []byte(minimalAnchored), fileEnv(t, f).lc, layerOptions{})
	if err != nil || len(rf.Problems) > 0 {
		t.Fatalf("%v %v", err, rf.Problems)
	}
	if rf.Cfg.Workflows[config.ImplicitWorkflow].Commands.Test != "sh test.sh" || rf.Res.SourceOf("workflows.default.commands.test") != "profile svc" {
		t.Fatalf("resolved %+v", rf.Cfg.Workflows)
	}
}

func TestResolveFugaroYAMLOfflineWithoutCache(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	rf, err := resolveFugaroYAML(context.Background(), []byte(minimalAnchored), fileEnv(t, f).lc, layerOptions{Offline: true})
	if err != nil || !rf.Layer.Unknown || len(rf.Problems) == 0 || !strings.Contains(rf.Problems[len(rf.Problems)-1].Message, "--project-layer") {
		t.Fatalf("%v %+v", err, rf)
	}
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/cli/ -run 'TestFindLayer|TestUnanchoredCheckoutIgnoresTheLayer|TestResolveFugaroYAML'`
Expected: FAIL to compile: `undefined: findLayer`, `undefined: layerOptions`, `undefined: layerRead`.

- [ ] **Step 3: Implement**

Create `internal/cli/layer_resolve.go`:

```go
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// layerOptions say where findLayer takes the project layer from.
type layerOptions struct {
	File    string // --project-layer: read this file, not the bucket
	Offline bool   // --offline: the cache only
	Data    []byte // a layer already read and checked (Cloud Build, the check job)
	Where   string // where Data came from, for messages
	// NoBucket never reads the canonical object: without Data there is no
	// layer (the daily check job, whose account cannot read fugaro/).
	NoBucket bool
	// Lenient is for the commands that worked offline before 0.6.0
	// (validate, doctor, init's stages, secrets, image render): with no
	// project config selected they read only the cache, and a bucket that
	// cannot be read leaves the layer unknown, with a note, instead of
	// failing. An invalid object still fails. Launches and builds are
	// strict (decision L16).
	Lenient bool
}

// foundLayer is the project layer that applies to a checkout's fugaro.yaml.
type foundLayer struct {
	Layer      *config.ProjectLayer // nil: none applies
	Where      string               // gs://fugaro-runs-<gcp>/fugaro/project-layer.yaml, or the file
	Generation int64
	CheckedAt  time.Time // when the bucket was read (the cache's stamp when cached)
	// Unknown: whether one applies could not be told (--offline with
	// nothing cached).
	Unknown bool
	Note    string // a warning: a cached copy, or why none applies
}

// resolvedFile is a checkout's fugaro.yaml resolved over its project layer.
type resolvedFile struct {
	Cfg      *config.Config // nil when there are Problems
	Res      *config.Resolution
	Layer    foundLayer
	Problems []config.Problem
}

// layerRead reads the canonical object; tests replace it.
var layerRead = func(ctx context.Context, b *blobx.Bucket) ([]byte, int64, error) {
	return b.ReadMaxStrict(ctx, config.LayerKey, config.LayerMaxBytes)
}

// findLayer is the project layer that applies to the fugaro.yaml data
// (docs/design/layered-config.md §3 and §7). None applies to a file
// without gcp_project: (decision L9), or to an installation whose runs
// bucket is not default-named. Otherwise it is the bucket's object, read
// on every call (decision L13): only when the bucket cannot be reached at
// all does a cached copy up to 7 days old stand in, with a note. A present
// but invalid object is an error; it never counts as none.
func findLayer(ctx context.Context, getenv func(string) string, data []byte, lc *localcfg.Config, o layerOptions, now time.Time) (foundLayer, error) {
	project, perr := config.ProjectOf(data)
	gcp, gerr := config.GCPProjectOf(data)
	if perr != nil || gerr != nil || gcp == "" || !config.ProjectNameRE.MatchString(project) || !config.GCPProjectRE.MatchString(gcp) {
		return foundLayer{}, nil // not anchored; the parse reports a malformed file
	}
	anchor := config.LayerAnchor{Project: project, GCPProject: gcp}
	parse := func(text []byte, where string) (*config.ProjectLayer, error) {
		l, ps := config.ParseProjectLayer(text, anchor)
		if len(ps) > 0 {
			return nil, userErr("%s is invalid, so nothing resolves against it: %s; ask an operator to publish a valid one (fugaro config publish)", where, pluginwire.Printable(layerProblemsText(ps)))
		}
		return l, nil
	}
	switch {
	case o.Data != nil:
		l, err := parse(o.Data, o.Where)
		return foundLayer{Layer: l, Where: o.Where}, err
	case o.File != "":
		text, err := readLayerFile(o.File)
		if err != nil {
			return foundLayer{}, userErr("%v", err)
		}
		l, err := parse(text, o.File)
		return foundLayer{Layer: l, Where: o.File}, err
	case o.NoBucket:
		return foundLayer{}, nil
	}
	if lc == nil && o.Lenient {
		o.Offline = true
	}
	bucketName := "fugaro-runs-" + gcp
	bucketURL := "gs://" + bucketName
	if lc != nil && lc.Name == project && lc.GCPProject == gcp {
		if lc.RunsBucketName() != bucketName {
			return foundLayer{Note: fmt.Sprintf("the project layer needs the default runs bucket name %s (this installation's is %q), so none applies", bucketName, lc.RunsBucketName())}, nil
		}
		bucketURL = lc.BucketURL()
	}
	where := "gs://" + bucketName + "/" + config.LayerKey
	cached, ok := localcfg.LoadLayerCache(getenv, project)
	ours := ok && cached.GCPProject == gcp && cached.Bucket == bucketName && cached.UsableOffline(now)
	fromCache := func(note string) (foundLayer, error) {
		l, err := parse([]byte(cached.YAML), "the cached copy of "+where)
		if err != nil {
			_ = localcfg.DropLayerCache(getenv, project)
			return foundLayer{}, err
		}
		return foundLayer{Layer: l, Where: where, Generation: cached.Generation, CheckedAt: cached.CheckedAt, Note: note}, nil
	}
	if o.Offline {
		if ours {
			return fromCache(fmt.Sprintf("--offline: using the cached project layer of %s, %s old", project, ageDays(now.Sub(cached.CheckedAt))))
		}
		return foundLayer{Unknown: true, Note: fmt.Sprintf("no project config is selected and no project layer of %s is cached, so the project layer was not checked", project)}, nil
	}
	unread := func(err error) (foundLayer, error) {
		if o.Lenient {
			return foundLayer{Unknown: true, Note: fmt.Sprintf("the project layer of %s could not be read (%s), so it was not checked", project, oneLineCLI(err.Error()))}, nil
		}
		return foundLayer{}, err
	}
	b, err := blobx.Open(ctx, bucketURL)
	if err != nil {
		return unread(remote(err))
	}
	defer b.Close()
	text, gen, err := layerRead(ctx, b)
	switch {
	case err == nil:
	case errors.Is(err, blobx.ErrNotExist):
		_ = localcfg.DropLayerCache(getenv, project)
		return foundLayer{}, nil
	case errors.Is(err, blobx.ErrTooLarge):
		return foundLayer{}, userErr("%s is over the %d KiB limit; ask an operator to publish it again (fugaro config publish)", where, config.LayerMaxBytes>>10)
	case isUnreachable(err) && ours:
		return fromCache(fmt.Sprintf("using the cached project layer of %s, %s old: %s is unreachable", project, ageDays(now.Sub(cached.CheckedAt)), bucketURL))
	default:
		return unread(bucketErrFor(bucketURL, "reading "+config.LayerKey, "the project layer of "+project, err))
	}
	l, err := parse(text, where)
	if err != nil {
		_ = localcfg.DropLayerCache(getenv, project)
		return foundLayer{}, err
	}
	_ = localcfg.SaveLayerCache(getenv, project, localcfg.SharedCacheEntry{GCPProject: gcp, Bucket: bucketName, Generation: gen, CheckedAt: now, YAML: string(text)})
	return foundLayer{Layer: l, Where: where, Generation: gen, CheckedAt: now}, nil
}

// resolveFugaroYAML resolves data, a checkout's fugaro.yaml, exactly as the
// runner will (decision L16): over the project layer findLayer finds for
// it. Every in-checkout command goes through here.
func resolveFugaroYAML(ctx context.Context, data []byte, lc *localcfg.Config, o layerOptions) (resolvedFile, error) {
	fl, err := findLayer(ctx, os.Getenv, data, lc, o, time.Now())
	if err != nil {
		return resolvedFile{}, err
	}
	cfg, res, ps := config.Resolve(data, fl.Layer)
	if fl.Unknown {
		for i, p := range ps {
			if p.Code == config.CodeNeedsLayer {
				ps[i].Message += "; " + fl.Note + ": select the project config, connect, or pass --project-layer FILE"
			}
		}
	}
	return resolvedFile{Cfg: cfg, Res: res, Layer: fl, Problems: ps}, nil
}

// parseCheckoutFugaroYAML is config.Parse for a checkout's fugaro.yaml over
// its project layer, for the commands that want only the config and its
// problems. A failure to read the layer is one problem.
func parseCheckoutFugaroYAML(ctx context.Context, data []byte, lc *localcfg.Config) (*config.Config, []config.Problem) {
	rf, err := resolveFugaroYAML(ctx, data, lc, layerOptions{Lenient: true})
	if err != nil {
		return nil, []config.Problem{{Path: "project layer", Message: oneLineCLI(err.Error())}}
	}
	return rf.Cfg, rf.Problems
}

// readLayerFile reads a project layer file the user names: a regular file
// only, opened without blocking, read through a cap of the size limit.
func readLayerFile(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, config.LayerMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > config.LayerMaxBytes {
		return nil, fmt.Errorf("%s is over the %d KiB limit", path, config.LayerMaxBytes>>10)
	}
	return data, nil
}

// layerProblemsText is ps as one line.
func layerProblemsText(ps []config.Problem) string {
	msgs := make([]string, len(ps))
	for i, p := range ps {
		msgs[i] = p.String()
	}
	return strings.Join(msgs, "; ")
}
```

- [ ] **Step 4: Run them and see them pass**

Run: `go test -race ./internal/cli/ -run 'TestFindLayer|TestUnanchoredCheckoutIgnoresTheLayer|TestResolveFugaroYAML'`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/layer_resolve.go internal/cli/layer_resolve_test.go internal/cli/layer_helpers_test.go
git commit -m "layered config task 9: findLayer and resolveFugaroYAML, the CLI's one resolution seam"
```

### Task 10: Every in-checkout command resolves through the seam

**Files:**
- Modify:
  - `internal/cli/validate.go`;
  - `internal/cli/image.go` (`loadCheckoutConfigAt`);
  - `internal/cli/cloud.go` (`checkoutParse`);
  - `internal/cli/doctor.go` (`fugaroYAMLCheck` and its call);
  - `internal/cli/init_repo_target.go` (`resolveRepoTarget`);
  - `internal/cli/init_anchor.go`;
  - `internal/cli/initsecrets.go` (`defaultBranchConfig`);
  - `internal/cli/readyaml_test.go` (the two `fugaroYAMLCheck` calls).
- Create: `internal/cli/layer_sites_test.go`

**Interfaces:**
- Consumes: `resolveFugaroYAML`, `parseCheckoutFugaroYAML`, `layerOptions` (Task 9); `selectedProjectConfig`.
- Produces:
  ```go
  func loadCheckoutResolved(ctx context.Context, dir string, lc *localcfg.Config, o layerOptions) (root string, rf resolvedFile, err error) // lc nil: the selected one
  func fugaroYAMLCheck(ctx context.Context, root string, lc *localcfg.Config) (*doctorCheck, *doctorFugaroYAML) // was (root string)
  // validate gains --project-layer FILE and --offline; its JSON gains "project_layer"
  type validateLayer struct { Where string `json:"where"`; Generation int64 `json:"generation,omitempty"`; SHA256 string `json:"sha256"` }
  func validateLayerLines(rf resolvedFile) (*validateLayer, []config.Problem)
  func usesProfiles(c *config.Config) bool
  func shortSHA(s string) string // also used by doctor (Task 16)
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/cli/layer_sites_test.go`:

```go
package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/testutil"
)

func TestValidateResolvesAMinimalFile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	out, errOut, err := execute(t, "validate")
	if err != nil || !strings.Contains(out, "fugaro.yaml is valid") || !strings.Contains(errOut, "uses the project layer") {
		t.Fatalf("out %q, stderr %q, err %v", out, errOut, err)
	}
}

func TestValidateWithAProjectLayerFile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "") // none published
	layerCheckout(t, f, minimalAnchored)
	dir := t.TempDir()
	testutil.WriteFiles(t, dir, map[string]string{"layer.yaml": testProjectLayer, "bad.yaml": layerWithDefaults("  budget: { per_run_usd: 1 }\n")})
	if out, _, err := execute(t, "validate", "--project-layer", filepath.Join(dir, "layer.yaml")); err != nil || !strings.Contains(out, "is valid") {
		t.Fatalf("good layer: %q %v", out, err)
	}
	_, _, err := execute(t, "validate", "--project-layer", filepath.Join(dir, "bad.yaml"))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "bad.yaml is invalid") {
		t.Fatalf("bad layer: %v", err)
	}
}

func TestValidateOfflineNeedsTheLayerForAMinimalFile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	out, _, err := execute(t, "validate", "--offline")
	if ExitCode(err) != ExitUserError || !strings.Contains(out, "--project-layer") {
		t.Fatalf("out %q, err %v", out, err)
	}
}

func TestValidateOfflineWarnsForAFullFile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored+"git: { provider: github }\nworkflows:\n  svc: { base: web-node, commands: { build: sh build.sh, test: sh test.sh } }\n")
	out, errOut, err := execute(t, "validate", "--offline")
	if err != nil || !strings.Contains(out, "is valid") || !strings.Contains(errOut, "the project layer was not checked") {
		t.Fatalf("out %q, stderr %q, err %v", out, errOut, err)
	}
}

func TestDoctorFugaroYAMLResolves(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	if c, fy := fugaroYAMLCheck(context.Background(), dir, fileEnv(t, f).lc); c == nil || !c.OK || fy == nil || !fy.Valid {
		t.Fatalf("check %+v, %+v", c, fy)
	}
}

func TestImageRenderResolvesAMinimalFile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	out, _, err := execute(t, "image", "render")
	if err != nil || !strings.Contains(out, "FROM") {
		t.Fatalf("out %q, err %v", out, err)
	}
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/cli/ -run 'TestValidate(ResolvesAMinimalFile|WithAProjectLayerFile|Offline)|TestDoctorFugaroYAMLResolves|TestImageRenderResolvesAMinimalFile'`
Expected: FAIL. It does not compile until `fugaroYAMLCheck` takes three arguments. After that, the minimal file fails with `workflows: must define at least one workflow; a fugaro.yaml without workflows takes one from project aurora's layer, and none was found`, because `config.Parse` never reads the layer. `--project-layer` and `--offline` fail with `unknown flag`.

- [ ] **Step 3: Implement**

**`internal/cli/validate.go`.**
- Give `newValidateCmd` two more flag variables, `var layerFile string; var offline bool`.
- Register the flags next to `--json`:
  ```go
  cmd.Flags().StringVar(&layerFile, "project-layer", "", "resolve against this project layer file instead of the published one (also checks the file; for CI without bucket access, and before fugaro config publish)")
  cmd.Flags().BoolVar(&offline, "offline", false, "never read the runs bucket: resolve against the cached project layer, if any")
  ```
- Add `ProjectLayer *validateLayer \`json:"project_layer,omitempty"\`` to `validateOutput`, and add the type:
  ```go
  // validateLayer is the project layer validate resolved against.
  type validateLayer struct {
      Where      string `json:"where"`
      Generation int64  `json:"generation,omitempty"`
      SHA256     string `json:"sha256"`
  }
  ```
- Replace `cfg, problems := config.Parse(data)` with:

```go
			rf, err := resolveFugaroYAML(cmd.Context(), data, selectedProjectConfig(cmd.Context()), layerOptions{File: layerFile, Offline: offline, Lenient: true})
			if err != nil {
				return err
			}
			cfg, problems := rf.Cfg, rf.Problems
			layer, layerWarnings := validateLayerLines(rf)
```

- Keep `var warnings []config.Problem` where it is. The `if cfg != nil` block reassigns it with the budget warnings, so the layer lines are added after that block, right before `out := cmd.OutOrStdout()`:

```go
			warnings = append(layerWarnings, warnings...)
```

- Pass `ProjectLayer: layer` into the `validateOutput` literal.
- Add:

```go
// validateLayerLines are validate's project layer summary and warning
// lines: which layer the file resolved against, why none or the cache was
// used, and that profiles need layeredSince everywhere.
func validateLayerLines(rf resolvedFile) (*validateLayer, []config.Problem) {
	var layer *validateLayer
	var warnings []config.Problem
	if l := rf.Layer.Layer; l != nil {
		layer = &validateLayer{Where: rf.Layer.Where, Generation: rf.Layer.Generation, SHA256: l.SHA256}
		warnings = append(warnings, config.Problem{Path: "project layer", Message: fmt.Sprintf("uses the project layer %s (generation %d, sha256 %s); fugaro config show prints where each value comes from", l.Project, rf.Layer.Generation, shortSHA(l.SHA256))})
	}
	if rf.Layer.Note != "" {
		warnings = append(warnings, config.Problem{Path: "project layer", Message: rf.Layer.Note})
	}
	if rf.Cfg != nil && usesProfiles(rf.Cfg) {
		warnings = append(warnings, config.Problem{Path: "profile", Message: "profiles need fugaro " + layeredSince + ": every teammate's CLI, every CI pin and every job image must be on it before this file is merged (older ones refuse it)"})
	}
	return layer, warnings
}

// usesProfiles reports whether a resolved config took any workflow from a
// profile: what binaries before layeredSince refuse.
func usesProfiles(c *config.Config) bool {
	for _, w := range c.Workflows {
		if w.Profile != "" {
			return true
		}
	}
	return false
}

// shortSHA is the first 12 characters of a sum, or all of a shorter one.
func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
```

(`layeredSince` is defined in Task 11. If Task 10 merges first, define `const layeredSince = "0.6.0"` in `layer_resolve.go`, and Task 11 moves it.)

**`internal/cli/image.go`.** Split `loadCheckoutConfigAt`:

```go
// loadCheckoutConfigAt is loadCheckoutConfig for the checkout holding dir
// (empty: the current directory).
func loadCheckoutConfigAt(ctx context.Context, dir string) (root string, cfg *config.Config, err error) {
	root, rf, err := loadCheckoutResolved(ctx, dir, nil, layerOptions{Lenient: true})
	return root, rf.Cfg, err
}

// loadCheckoutResolved is loadCheckoutConfigAt resolved over the project
// layer o finds, with the layer (image build passes it to Cloud Build).
func loadCheckoutResolved(ctx context.Context, dir string, lc *localcfg.Config, o layerOptions) (root string, rf resolvedFile, err error) {
	args := []string{"rev-parse", "--show-toplevel"}
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		if dir != "" {
			return "", rf, &ExitError{Code: ExitUserError, Err: fmt.Errorf("%s is not inside a git checkout; point at the repository's checkout", dir)}
		}
		return "", rf, &ExitError{Code: ExitUserError, Err: errors.New("not inside a git checkout; run this from the repository")}
	}
	root = strings.TrimSpace(string(out))
	data, err := readFugaroYAML(filepath.Join(root, "fugaro.yaml"))
	if err != nil {
		return "", rf, &ExitError{Code: ExitUserError, Err: fmt.Errorf("%w; create it with /fugaro:setup or fugaro config example", err)}
	}
	if lc == nil {
		lc = selectedProjectConfig(ctx)
	}
	if rf, err = resolveFugaroYAML(ctx, data, lc, o); err != nil {
		return "", rf, err
	}
	problems := rf.Problems
	if rf.Cfg != nil {
		problems = append(config.Check(rf.Cfg, root), computeProblems(rf.Cfg)...)
	}
	if len(problems) > 0 {
		msgs := make([]string, len(problems))
		for i, p := range problems {
			msgs[i] = p.String()
		}
		return "", rf, &ExitError{Code: ExitUserError, Err: fmt.Errorf("fugaro.yaml has %d problem(s), see fugaro validate:\n  %s", len(problems), strings.Join(msgs, "\n  "))}
	}
	return root, rf, nil
}
```

`loadCheckoutConfigAt` is lenient (the commands that use it worked offline before), while `loadCheckoutResolved` callers choose. Task 14 makes the cloud image build strict.

**`internal/cli/cloud.go`.** In `checkoutParse`, replace `return config.Parse(data)` with `return parseCheckoutFugaroYAML(ctx, data, selectedProjectConfig(ctx))`.

**`internal/cli/doctor.go`.**
- Change the signature to `func fugaroYAMLCheck(ctx context.Context, root string, lc *localcfg.Config) (*doctorCheck, *doctorFugaroYAML)`.
- Replace its `cfg, problems := config.Parse(data)` with `cfg, problems := parseCheckoutFugaroYAML(ctx, data, lc)`.
- Change the call to `fugaroYAMLCheck(ctx, co.Root, lc)`.
- In `internal/cli/readyaml_test.go`, change the two calls to `fugaroYAMLCheck(context.Background(), dir, nil)` and add `"context"` to its imports. With `lc` nil and no `gcp_project:` in those fixtures, no bucket is read.

**`internal/cli/init_repo_target.go`.** In `resolveRepoTarget`, replace `cfg, problems := config.Parse(data)` with `cfg, problems := parseCheckoutFugaroYAML(ctx, data, selectedProjectConfig(ctx))`. Change the doc comment's "with no cloud call" to "with no cloud call besides reading the project layer (docs/design/layered-config.md)".

**`internal/cli/init_anchor.go`.** Replace `cfg, problems := config.Parse(data)` with `cfg, problems := parseCheckoutFugaroYAML(ctx, data, lc)`.

**`internal/cli/initsecrets.go`.** In `defaultBranchConfig`, replace `cfg, _ := config.Parse(data)` with `cfg, _ := parseCheckoutFugaroYAML(ctx, data, selectedProjectConfig(ctx))`.

- [ ] **Step 4: Run them and see them pass**

Run: `go test -race ./internal/cli/ -run 'TestValidate|TestDoctor|TestImageRender|TestInit(Anchor|Repo)|TestSecrets|TestReadYAML|TestFugaroYAMLCheck'`
Expected: `ok`. The existing validate, doctor, init, secrets and readyaml tests pass unchanged, since none of their fixtures is anchored to a published layer.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/validate.go internal/cli/image.go internal/cli/cloud.go internal/cli/doctor.go internal/cli/init_repo_target.go internal/cli/init_anchor.go internal/cli/initsecrets.go internal/cli/readyaml_test.go internal/cli/layer_sites_test.go
git commit -m "layered config task 10: validate, doctor, init, secrets and image resolve over the project layer"
```

### Task 11: `fugaro run` embeds the layer, the 0.6.0 image gate, the launch lines

**Files:**
- Modify: `internal/cli/run.go`, `internal/cli/recipes_skew.go`, `docs/release.md` ("Before you tag")
- Create: `internal/cli/run_layer.go`, `internal/cli/run_layer_test.go`

**Interfaces:**
- Consumes: `findLayer` (Task 9), `task.ProjectLayer` (Task 6), `config.Resolve`, `checkoutRoot`, `readFugaroYAML`, `baseRefReleaseRE`, `imagePredates`.
- Produces:
  ```go
  const layeredSince = "0.6.0"
  func checkImageSince(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind, since, subject, knows, alt string, warn io.Writer) error
  func embedProjectLayer(ctx context.Context, env *cloudEnv, spec *task.Spec, warn io.Writer) error
  ```
  `checkRecipeImage` keeps its signature and becomes a wrapper.

- [ ] **Step 1: Write the failing tests**

Create `internal/cli/run_layer_test.go`:

```go
package cli

import (
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

const release060 = "ghcr.io/dimipaun/fugaro-web-node:0.6.0"

func TestRunEmbedsTheProjectLayer(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), config.ImplicitWorkflow, release060)
	_, errOut, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task")
	if err != nil {
		t.Fatal(err)
	}
	pl := readSpecOf(t, f, mustSlug("github", "acme/other"), "20261008-100000-abcd").ProjectLayer
	if pl == nil || pl.SHA256 != config.LayerSum([]byte(testProjectLayer)) || pl.YAML != testProjectLayer {
		t.Fatalf("task project_layer = %+v", pl)
	}
	for _, want := range []string{"project layer: aurora generation", "commands: from profile svc (project layer generation"} {
		if !strings.Contains(errOut, want) {
			t.Errorf("stderr %q lacks %q", errOut, want)
		}
	}
}

// Review Focus 3.
func TestRunRefusesLayerOnOldImage(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	writeBuildRecord(t, f, mustSlug("github", "acme/other"), config.ImplicitWorkflow, release050)
	wantRefused(t, f, "the project layer needs a job image whose runner knows the project layer (fugaro 0.6.0 or later)",
		"run", "--run-id", "20261008-100000-abcd", "A task")
}

func TestRunWithoutALayerEmbedsNothing(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	recipeCheckout(t, f, "", nil)
	if _, _, err := execute(t, "run", "--run-id", "20261008-100000-abcd", "A task"); err != nil {
		t.Fatal(err)
	}
	if pl := readSpecOf(t, f, mustSlug("github", "acme/other"), "20261008-100000-abcd").ProjectLayer; pl != nil {
		t.Fatalf("task project_layer = %+v", pl)
	}
}

func TestRunOutsideACheckoutEmbedsTheInstallationLayer(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	writeBuildRecord(t, f, appSlug, "web", release060)
	if _, _, err := execute(t, "run", "--repo", "acme/app", "--run-id", "20261008-100000-abcd", "A task"); err != nil {
		t.Fatal(err)
	}
	if pl := readSpec(t, f, "20261008-100000-abcd").ProjectLayer; pl == nil || pl.SHA256 != config.LayerSum([]byte(testProjectLayer)) {
		t.Fatalf("task project_layer = %+v", pl)
	}
}
```

It uses the existing helpers `readSpecOf(t, f, slug, id)` (`run_recipe_test.go`), `readSpec`, `wantRefused`, `writeBuildRecord` and `release050`.

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/cli/ -run 'TestRun(EmbedsTheProjectLayer|RefusesLayerOnOldImage|WithoutALayerEmbedsNothing|OutsideACheckoutEmbedsTheInstallationLayer)'`
Expected: FAIL with `task project_layer = <nil>`. `TestRunRefusesLayerOnOldImage` fails because the run launches.

- [ ] **Step 3: Implement**

In `internal/cli/recipes_skew.go`, add after `recipesSince`:

```go
// layeredSince is the first fugaro release whose runner reads a task's
// project layer and fugaro.yaml's profile keys, and whose base image's
// fugaro renders with --layer-bucket; an older one refuses all of them.
// docs/release.md ("Before you tag") has the checklist verify it.
const layeredSince = "0.6.0"
```

Replace `checkRecipeImage`'s body so it delegates, and add `checkImageSince`, which is the old body with the release, the subject, what the runner must know and the way out as parameters:

```go
func checkRecipeImage(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind string, warn io.Writer) error {
	subject, alt := "fugaro.yaml's agent.recipe", ""
	if spec.Recipe != nil {
		subject = "recipe " + spec.Recipe.Name
		if spec.Recipe.Name != recipe.DefaultName { // a carried default cannot be the way out
			alt = "; or launch with --recipe default to run today's loop"
		}
	}
	return checkImageSince(ctx, env, slug, spec, kind, recipesSince, subject, "recipes", alt, warn)
}

// checkImageSince refuses a launch whose job image runs a fugaro older than
// since: subject needs a runner that knows knows. The judge is the build
// record's base_ref, the base the image was built FROM. A non-release base
// is not judged (one warning); no record, or one without base_ref, is
// refused. kind is the workflow's base kind, "" when unknown.
func checkImageSince(ctx context.Context, env *cloudEnv, slug string, spec *task.Spec, kind, since, subject, knows, alt string, warn io.Writer) error {
	base := "this release's base image"
	if kind != "" {
		base = "this release's " + kind + " base image"
	}
	refuse := func(why string) error {
		return userErr("%s needs a job image whose runner knows %s (fugaro %s or later), but the job image of %s workflow %s %s. "+
			"Run fugaro image refresh --repo %s --workflow %s in its checkout, in your own terminal window (interactive: needs a real terminal, cannot run in CI, has no --yes; it copies %s, points the daily image check job at it and rebuilds the image)%s",
			subject, knows, since, spec.Repo, spec.Workflow, why, spec.Repo, spec.Workflow, base, alt)
	}
	b, err := env.recordBucket(ctx)
	if err != nil {
		return remote(err)
	}
	data, _, err := b.Read(ctx, imagecheck.RecordKey(slug, spec.Workflow))
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return refuse("has no build record")
	case err != nil:
		return remote(fmt.Errorf("reading the build record of %s workflow %s: %w", spec.Repo, spec.Workflow, err))
	}
	rec, err := imagecheck.ParseRecord(data)
	if err != nil {
		return refuse("has an unreadable build record (" + oneLineCLI(err.Error()) + ")")
	}
	if rec.BaseRef == "" {
		return refuse("has a build record that does not say which base image it was built from")
	}
	ref := pluginwire.Printable(rec.BaseRef)
	m := baseRefReleaseRE.FindStringSubmatch(rec.BaseRef)
	switch {
	case m == nil:
		fmt.Fprintf(warn, "warning: the job image of %s workflow %s was built from %s, which is not a release base image; whether its runner knows %s is not checked\n", spec.Repo, spec.Workflow, ref, knows)
	case imagePredates(m[2], since):
		return refuse(fmt.Sprintf("was built from base image %s, release %s", ref, m[2]))
	}
	return nil
}
```

Create `internal/cli/run_layer.go`:

```go
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/task"
)

// embedProjectLayer puts the project layer that applies to the task's
// repository into spec (decisions L8, L9, L13). In the repository's
// checkout, it is the one findLayer finds for its fugaro.yaml. Outside one
// (fugaro run --repo), it is the installation's own, which the runner
// applies only to a fugaro.yaml at the ref that names it. It prints one
// line naming the layer, and one when the workflow's commands come from a
// profile (decision L7).
func embedProjectLayer(ctx context.Context, env *cloudEnv, spec *task.Spec, warn io.Writer) error {
	var data []byte
	if root := checkoutRoot(ctx, spec.Repo); root != "" {
		data, _ = readFugaroYAML(filepath.Join(root, "fugaro.yaml"))
	}
	if data == nil {
		data = []byte(fmt.Sprintf("version: 1\nproject: %s\ngcp_project: %s\n", env.lc.Name, env.lc.GCPProject))
	}
	fl, err := findLayer(ctx, os.Getenv, data, env.lc, layerOptions{}, time.Now())
	if err != nil {
		return err
	}
	l := fl.Layer
	if l == nil {
		return nil // why none applies is config show's and doctor's to say, not every launch's
	}
	if fl.Note != "" {
		fmt.Fprintln(warn, "note: "+fl.Note) // a cached copy stood in
	}
	spec.ProjectLayer = &task.ProjectLayer{SHA256: l.SHA256, Generation: fl.Generation, YAML: string(l.Raw)}
	fmt.Fprintf(warn, "project layer: %s generation %d (sha256 %s)\n", l.Project, fl.Generation, l.SHA256[:12])
	if cfg, res, ps := config.Resolve(data, l); len(ps) == 0 {
		if name, _, err := cfg.SelectWorkflow(spec.Workflow); err == nil {
			if src := res.SourceOf("workflows." + name + ".commands.test"); strings.HasPrefix(src, "profile ") {
				fmt.Fprintf(warn, "commands: from %s (project layer generation %d)\n", src, fl.Generation)
			}
		}
	}
	return nil
}
```

In `internal/cli/run.go`'s `RunE`, right after the `switch` that builds `spec` and its `if err != nil { return err }`, add:

```go
	if o.retry == "" && !reused {
		if err := embedProjectLayer(ctx, env, spec, cmd.ErrOrStderr()); err != nil {
			return err
		}
	}
```

In the `if prior == nil {` block, after the recipe image check, add:

```go
		if spec.ProjectLayer != nil {
			if err := checkImageSince(ctx, env, slug, spec, kind, layeredSince, "the project layer", "the project layer", "", cmd.ErrOrStderr()); err != nil {
				return err
			}
		}
```

In `docs/release.md`'s "Before you tag" list, next to the `recipesSince` line, add: "`layeredSince` in `internal/cli/recipes_skew.go` equals the release that ships the project layer (0.6.0)."

- [ ] **Step 4: Run them and see them pass**

Run: `go test -race ./internal/cli/ -run 'TestRun|TestRecipe'`
Expected: `ok`. The existing recipe gate tests pass through the `checkRecipeImage` wrapper, with the message unchanged.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/run.go internal/cli/run_layer.go internal/cli/run_layer_test.go internal/cli/recipes_skew.go docs/release.md
git commit -m "layered config task 11: fugaro run embeds the project layer behind a 0.6.0 image gate"
```

### Task 12: `fugaro config show`, `config layer` and `config init`

**Files:**
- Modify: `internal/cli/configcmd.go`, `internal/config/config.go` (`Duration.MarshalYAML`)
- Create: `internal/cli/config_show.go`, `internal/cli/config_init.go`, `internal/cli/config_cmd_test.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: `loadCheckoutResolved` (Task 10), `findLayer`, `resolveFugaroYAML` (Task 9), `selectProject`, `addCloudFlags`, `gitRead`.
- Produces:
  ```go
  func (d Duration) MarshalYAML() (any, error) // "1h30m0s": config show prints durations as written
  type configShowDoc struct { ProjectLayer *validateLayer `json:"project_layer"`; ConfigSHA256 string `json:"config_sha256"`; Values []configShowValue `json:"values"`; Notes []string `json:"notes,omitempty"` }
  type configShowValue struct { Path string `json:"path"`; Value any `json:"value"`; Source string `json:"source"` }
  func showValues(cfg *config.Config, res *config.Resolution, workflow string) ([]configShowValue, error)
  func minimalFugaroYAML(project, gcp, profile, baseBranch string) string
  ```

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/config_test.go`:

```go
func TestDurationMarshalsAsWritten(t *testing.T) {
	out, err := yaml.Marshal(Timeouts{Total: Duration{Duration: 90 * time.Minute, Set: true}})
	if err != nil || !strings.Contains(string(out), "total: 1h30m0s") {
		t.Fatalf("%s %v", out, err)
	}
}
```

(Add `"time"` to the imports of `config_test.go` if it is missing.)

Create `internal/cli/config_cmd_test.go`:

```go
package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
)

// Review Focus 1.
func TestConfigShowNamesEverySource(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored+"agent:\n  review_rounds: 3\n")
	out, _, err := execute(t, "config", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var doc configShowDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, v := range doc.Values {
		got[v.Path] = v.Source
	}
	for path, want := range map[string]string{
		"project":                            "repo",
		"agent.review_rounds":                "repo",
		"agent.auth":                         "project",
		"git.provider":                       "project",
		"git.base_branch":                    "default",
		"workflows.default.commands.test":    "profile svc",
		"workflows.default.timeouts.total":   "default",
		"workflows.default.resources.memory": "default",
	} {
		if got[path] != want {
			t.Errorf("%s: source %q, want %q", path, got[path], want)
		}
	}
	if doc.ProjectLayer == nil || doc.ProjectLayer.SHA256 != config.LayerSum([]byte(testProjectLayer)) || len(doc.ConfigSHA256) != 64 {
		t.Fatalf("doc = %+v", doc)
	}
	text, _, err := execute(t, "config", "show")
	if err != nil || !strings.Contains(text, "workflows.default.commands.test") || !strings.Contains(text, "profile svc") || !strings.Contains(text, "project layer:") {
		t.Fatalf("text %q, %v", text, err)
	}
}

func TestConfigLayerPrintsThePublishedLayer(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	layerCheckout(t, f, minimalAnchored)
	out, _, err := execute(t, "config", "layer")
	if err != nil || !strings.Contains(out, "default_profile: svc") || !strings.Contains(out, "generation") {
		t.Fatalf("out %q, %v", out, err)
	}
}

func TestConfigInitWritesTheMinimalFile(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := execute(t, "config", "init", "--project", "aurora"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("without --yes: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "fugaro.yaml")); err == nil {
		t.Fatal("written without --yes")
	}
	out, _, err := execute(t, "config", "init", "--project", "aurora", "--base-branch", "develop", "--yes")
	if err != nil || !strings.Contains(out, "wrote") {
		t.Fatalf("out %q, %v", out, err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "fugaro.yaml"))
	if want := minimalAnchored + "git:\n  base_branch: develop\n"; string(data) != want {
		t.Fatalf("fugaro.yaml = %q, want %q", data, want)
	}
	if _, _, err := execute(t, "config", "init", "--project", "aurora", "--base-branch", "develop", "--yes"); err != nil {
		t.Fatalf("rerun on the same file: %v", err)
	}
	if _, _, err := execute(t, "config", "init", "--project", "aurora", "--profile", "svc", "--yes"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "never overwrites") {
		t.Fatalf("a different existing file: %v", err)
	}
}

func TestConfigInitRefusesWithoutALayer(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	dir := layerCheckout(t, f, minimalAnchored)
	if err := os.Remove(filepath.Join(dir, "fugaro.yaml")); err != nil {
		t.Fatal(err)
	}
	_, _, err := execute(t, "config", "init", "--project", "aurora", "--yes")
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "publishes no project layer") {
		t.Fatalf("err = %v", err)
	}
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/config/ -run TestDurationMarshalsAsWritten && go test ./internal/cli/ -run 'TestConfig(Show|Layer|Init)'`
Expected: FAIL. `TestDurationMarshalsAsWritten` gets `total:\n    duration: 1h30m0s` instead of `total: 1h30m0s`. The CLI tests do not compile (`undefined: configShowDoc`).

- [ ] **Step 3: Implement**

In `internal/config/config.go`, after `Duration.UnmarshalYAML`:

```go
// MarshalYAML writes the duration as UnmarshalYAML reads it, such as
// 1h30m0s, so a resolved config prints (fugaro config show) and re-reads
// as written.
func (d Duration) MarshalYAML() (any, error) { return d.Duration.String(), nil }
```

Create `internal/cli/config_show.go`:

```go
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// configShowDoc is fugaro config show --json: the hook a script uses to
// check a repository resolves as intended (docs/design/layered-config.md
// §12).
type configShowDoc struct {
	ProjectLayer *validateLayer    `json:"project_layer"` // null: none applies
	ConfigSHA256 string            `json:"config_sha256"`
	Values       []configShowValue `json:"values"`
	Notes        []string          `json:"notes,omitempty"`
}

// configShowValue is one resolved key, with its source: default, project,
// profile <name> or repo.
type configShowValue struct {
	Path   string `json:"path"`
	Value  any    `json:"value"`
	Source string `json:"source"`
}

func newConfigShowCmd() *cobra.Command {
	var (
		workflow string
		asJSON   bool
		o        layerOptions
	)
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the checkout's fugaro.yaml resolved over the project layer, with where each value comes from",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, rf, err := loadCheckoutResolved(cmd.Context(), "", nil, o)
			if err != nil {
				return err
			}
			values, err := showValues(rf.Cfg, rf.Res, workflow)
			if err != nil {
				return userErr("%v", err)
			}
			doc := configShowDoc{ConfigSHA256: rf.Res.ConfigSHA256, Values: values}
			if l := rf.Layer.Layer; l != nil {
				doc.ProjectLayer = &validateLayer{Where: rf.Layer.Where, Generation: rf.Layer.Generation, SHA256: l.SHA256}
			}
			if rf.Layer.Note != "" {
				doc.Notes = append(doc.Notes, rf.Layer.Note)
			}
			return printConfigShow(cmd.OutOrStdout(), doc, rf.Layer.CheckedAt, asJSON)
		},
	}
	cmd.Flags().StringVar(&workflow, "workflow", "", "show only this workflow's keys besides the top-level ones")
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	cmd.Flags().StringVar(&o.File, "project-layer", "", "resolve against this project layer file instead of the published one")
	cmd.Flags().BoolVar(&o.Offline, "offline", false, "never read the runs bucket: resolve against the cached project layer, if any")
	return cmd
}

// showValues flattens cfg into its keys, each with its value and source,
// sorted by path; workflow, when set, keeps only that workflow's keys
// besides the top-level ones. A list is one key.
func showValues(cfg *config.Config, res *config.Resolution, workflow string) ([]configShowValue, error) {
	if workflow != "" {
		if _, ok := cfg.Workflows[workflow]; !ok {
			return nil, fmt.Errorf("fugaro.yaml has no workflow %q", workflow)
		}
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var tree map[string]any
	if err := yaml.Unmarshal(data, &tree); err != nil {
		return nil, err
	}
	var out []configShowValue
	var walk func(path string, v any)
	walk = func(path string, v any) {
		if m, ok := v.(map[string]any); ok && len(m) > 0 {
			for k, e := range m {
				walk(strings.TrimPrefix(path+"."+k, "."), e)
			}
			return
		}
		if parts := strings.SplitN(path, ".", 3); workflow != "" && len(parts) >= 2 && parts[0] == "workflows" && parts[1] != workflow {
			return
		}
		out = append(out, configShowValue{Path: path, Value: v, Source: res.SourceOf(path)})
	}
	walk("", tree)
	slices.SortFunc(out, func(a, b configShowValue) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

func printConfigShow(w io.Writer, doc configShowDoc, checked time.Time, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(doc)
	}
	if l := doc.ProjectLayer; l != nil {
		fmt.Fprintf(w, "project layer: %s, generation %d, sha256 %s, read %s ago\n", pluginwire.Printable(l.Where), l.Generation, l.SHA256, time.Since(checked).Round(time.Second))
	} else {
		fmt.Fprintln(w, "project layer: none applies")
	}
	for _, n := range doc.Notes {
		fmt.Fprintln(w, "note: "+pluginwire.Printable(n))
	}
	fmt.Fprintf(w, "resolved sha256: %s\n\n", doc.ConfigSHA256)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tVALUE\tSOURCE")
	for _, v := range doc.Values {
		val, _ := json.Marshal(v.Value)
		fmt.Fprintf(tw, "%s\t%s\t%s\n", v.Path, pluginwire.Printable(string(val)), v.Source)
	}
	return tw.Flush()
}

func newConfigLayerCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "layer",
		Short: "Print the project layer published for this checkout's project",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := gitRead(cmd.Context(), ".", "rev-parse", "--show-toplevel")
			if err != nil {
				return userErr("not inside a git checkout; run this from the repository")
			}
			data, err := readFugaroYAML(root + "/fugaro.yaml")
			if err != nil {
				return userErr("%v", err)
			}
			fl, err := findLayer(cmd.Context(), getenvOS, data, selectedProjectConfig(cmd.Context()), layerOptions{}, time.Now())
			if err != nil {
				return err
			}
			l := fl.Layer
			if l == nil {
				msg := "no project layer applies to this checkout (its fugaro.yaml needs gcp_project:, and the project must publish one with fugaro config publish)"
				if fl.Note != "" {
					msg += "; " + fl.Note
				}
				return userErr("%s", msg)
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]any{"where": fl.Where, "generation": fl.Generation, "sha256": l.SHA256, "yaml": string(l.Raw)})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "# %s, generation %d, sha256 %s\n%s", pluginwire.Printable(fl.Where), fl.Generation, l.SHA256, printableLines(string(l.Raw)))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print machine-readable output")
	return cmd
}
```

In `layer_resolve.go`, add `var getenvOS = os.Getenv`, so `findLayer` callers in commands pass one name.

Create `internal/cli/config_init.go`:

```go
package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/config"
)

// minimalFugaroYAML is the minimal fugaro.yaml of a repository of project
// whose installation is in GCP project gcp (decision L12): base_branch
// only when it is not main, profile only when named.
func minimalFugaroYAML(project, gcp, profile, baseBranch string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "version: 1\nproject: %s\ngcp_project: %s\n", project, gcp)
	if baseBranch != "" && baseBranch != "main" {
		fmt.Fprintf(&b, "git:\n  base_branch: %s\n", baseBranch)
	}
	if profile != "" {
		fmt.Fprintf(&b, "profile: %s\n", profile)
	}
	return b.String()
}

func newConfigInitCmd() *cobra.Command {
	var (
		o                   cloudOptions
		profile, baseBranch string
		yes                 bool
	)
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write the minimal fugaro.yaml of a repository whose project publishes a project layer",
		Long: "Write the minimal fugaro.yaml (version, project, gcp_project) into this checkout, for a project that\n" +
			"publishes a project layer. Non-interactive: without --yes it prints the file and writes nothing. It never\n" +
			"overwrites a fugaro.yaml, and writes nothing that does not resolve against the project layer.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			if profile != "" && !config.ProjectNameRE.MatchString(profile) {
				return userErr("--profile %q is not a profile name", profile)
			}
			if baseBranch != "" && !config.ValidBranchName(baseBranch) {
				return userErr("--base-branch %q is not a plain git branch name", baseBranch)
			}
			root, err := gitRead(ctx, ".", "rev-parse", "--show-toplevel")
			if err != nil {
				return userErr("not inside a git checkout; run this from the repository")
			}
			_, lc, err := selectProject(ctx, o)
			if err != nil {
				return err
			}
			text := minimalFugaroYAML(lc.Name, lc.GCPProject, profile, baseBranch)
			rf, err := resolveFugaroYAML(ctx, []byte(text), lc, layerOptions{})
			if err != nil {
				return err
			}
			if rf.Layer.Layer == nil {
				return userErr("project %s publishes no project layer, so a minimal fugaro.yaml would not resolve; write a full one with /fugaro:setup", lc.Name)
			}
			if len(rf.Problems) > 0 {
				return userErr("the minimal fugaro.yaml would not resolve against project %s's layer: %s", lc.Name, layerProblemsText(rf.Problems))
			}
			path := filepath.Join(root, "fugaro.yaml")
			switch old, err := os.ReadFile(path); {
			case err == nil && string(old) == text:
				fmt.Fprintf(cmd.OutOrStdout(), "%s is already this minimal file; nothing written\n", path)
				return nil
			case err == nil:
				return userErr("%s exists; fugaro config init never overwrites it (delete it first, or edit it by hand)", path)
			case !errors.Is(err, fs.ErrNotExist):
				return userErr("%v", err)
			}
			if !yes {
				fmt.Fprint(cmd.OutOrStdout(), text)
				return userErr("nothing written: pass --yes to write %s", path)
			}
			if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
				return userErr("%v", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s: workflow %s from profile %s (resolved sha256 %s)\n", path, config.ImplicitWorkflow, rf.Cfg.Workflows[config.ImplicitWorkflow].Profile, rf.Res.ConfigSHA256[:12])
			return nil
		},
	}
	cmd.Flags().StringVar(&profile, "profile", "", "the project layer's profile to use (default: its default_profile)")
	cmd.Flags().StringVar(&baseBranch, "base-branch", "", "the repository's base branch, when it is not main")
	cmd.Flags().BoolVar(&yes, "yes", false, "write the file (without it, print it and write nothing)")
	addCloudFlags(cmd, &o)
	return cmd
}
```

In `internal/cli/configcmd.go`, after the `example` subcommand, add `cmd.AddCommand(newConfigShowCmd(), newConfigLayerCmd(), newConfigInitCmd())`. Task 13 adds `newConfigPublishCmd()` to the same line.

- [ ] **Step 4: Run them and see them pass**

Run: `go test -race ./internal/config/ -run TestDurationMarshalsAsWritten && go test -race ./internal/cli/ -run 'TestConfig'`
Expected: `ok` for both, including the existing `config example` tests.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/cli/configcmd.go internal/cli/config_show.go internal/cli/config_init.go internal/cli/layer_resolve.go internal/cli/config_cmd_test.go
git commit -m "layered config task 12: fugaro config show, layer and init"
```

### Task 13: `fugaro config publish` with the fan-out copies

**Files:**
- Modify: `internal/cli/configcmd.go`
- Create: `internal/cli/config_publish.go`, `internal/cli/layer_copy.go`, `internal/cli/config_publish_test.go`

**Interfaces:**
- Consumes:
  - `config.ParseProjectLayer`, `config.LayerCopyKey`, `config.ExecutableKeys` (PR 1);
  - `readLayerFile`, `layerProblemsText`, `localcfg.SaveLayerCache` (Tasks 8 and 9);
  - `agentMarker`, `initflow.AgentRefusal`, `projectRecipesNote`, `fakeEndpointsOnGS`, `lineDiff`, `printableLines`, `task.Slug`, `imagecheck.RecordKey`, `imagecheck.ParseRecord`, `baseRefReleaseRE`, `imagePredates`, `layeredSince` (Task 11).
- Produces:
  ```go
  var layerPublishRace func(ctx context.Context, b *blobx.Bucket) // test seam
  func publishLayer(ctx context.Context, w io.Writer, b *blobx.Bucket, l *config.ProjectLayer, executable bool) (int64, error)
  func executableChanges(prev, next *config.ProjectLayer) []string
  func fanOutLayer(ctx context.Context, w io.Writer, env *cloudEnv, l *config.ProjectLayer) (failed int)
  func warnOldImages(ctx context.Context, w io.Writer, env *cloudEnv)
  func writeLayerCopy(ctx context.Context, b *blobx.Bucket, slug string, data []byte) error // layer_copy.go; nil data removes it
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/cli/config_publish_test.go`:

```go
package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
)

func layerFile(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "project-layer.yaml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func bucketText(t *testing.T, f *cloudFixture, key string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, "runs", filepath.FromSlash(key)))
	if err != nil {
		return ""
	}
	return string(data)
}

// Review Focus 4.
func TestPublishRefusedInAgentSession(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	noAgentSession(t)
	t.Setenv("CLAUDECODE", "1")
	_, _, err := execute(t, "config", "publish", "--executable-changes", layerFile(t, testProjectLayer))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "your own terminal") {
		t.Fatalf("err = %v", err)
	}
	if bucketText(t, f, config.LayerKey) != "" {
		t.Fatal("published from an agent session")
	}
}

func TestPublishWritesAndCopies(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	noAgentSession(t)
	out, _, err := execute(t, "config", "publish", "--executable-changes", layerFile(t, testProjectLayer))
	if err != nil || !strings.Contains(out, "published the project layer of aurora to gs://fugaro-runs-proj-1234/fugaro/project-layer.yaml") ||
		!strings.Contains(out, "acme/app: copied to "+config.LayerCopyKey(appSlug)) {
		t.Fatalf("out %q, err %v", out, err)
	}
	if bucketText(t, f, config.LayerKey) != testProjectLayer || bucketText(t, f, config.LayerCopyKey(appSlug)) != testProjectLayer {
		t.Fatal("the object or its copy is not the published text")
	}
}

// Review Focus 4, decision L7.
func TestPublishRefusesExecutableChangeWithoutFlag(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	noAgentSession(t)
	_, errOut, err := execute(t, "config", "publish", layerFile(t, testProjectLayer))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "--executable-changes") ||
		!strings.Contains(errOut, "EXECUTABLE CHANGES") || !strings.Contains(errOut, `profile svc: commands.test: "" -> "sh test.sh"`) {
		t.Fatalf("stderr %q, err %v", errOut, err)
	}
	if bucketText(t, f, config.LayerKey) != "" {
		t.Fatal("published without the flag")
	}
	if _, _, err := execute(t, "config", "publish", "--executable-changes", layerFile(t, testProjectLayer)); err != nil {
		t.Fatal(err)
	}
	// A change outside the executable keys needs no flag.
	v2 := strings.Replace(testProjectLayer, "auth: api-key", "auth: oauth", 1)
	if _, errOut, err := execute(t, "config", "publish", layerFile(t, v2)); err != nil || strings.Contains(errOut, "EXECUTABLE") {
		t.Fatalf("stderr %q, err %v", errOut, err)
	}
}

// Review Focus 4.
func TestPublishRefusesConcurrentPublisher(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	noAgentSession(t)
	race := layerPublishRace
	t.Cleanup(func() { layerPublishRace = race })
	other := strings.Replace(testProjectLayer, "auth: api-key", "auth: vertex", 1)
	layerPublishRace = func(context.Context, *blobx.Bucket) { writeBucketFile(t, f, config.LayerKey, other) }
	v2 := strings.Replace(testProjectLayer, "auth: api-key", "auth: oauth", 1)
	_, _, err := execute(t, "config", "publish", layerFile(t, v2))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "another publisher changed") {
		t.Fatalf("err = %v", err)
	}
	if bucketText(t, f, config.LayerKey) != other {
		t.Fatal("the concurrent publisher's object was overwritten")
	}
}

func TestPublishRefusesAnInvalidLayer(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	noAgentSession(t)
	_, _, err := execute(t, "config", "publish", layerFile(t, layerWithDefaults("  budget: { per_run_usd: 1 }\n")))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "nothing was published") || !strings.Contains(err.Error(), "budget.per_run_usd may only be set in: repo") {
		t.Fatalf("err = %v", err)
	}
}

func TestPublishWarnsAboutOldImages(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	noAgentSession(t)
	writeBuildRecord(t, f, appSlug, "web", release050)
	_, errOut, err := execute(t, "config", "publish", "--executable-changes", layerFile(t, testProjectLayer))
	if err != nil || !strings.Contains(errOut, "acme/app workflow web runs a job image built from") || !strings.Contains(errOut, "fugaro image refresh") {
		t.Fatalf("stderr %q, err %v", errOut, err)
	}
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/cli/ -run 'TestPublish'`
Expected: FAIL to compile, with `undefined: layerPublishRace`. Once the seam exists, `unknown command "publish" for "fugaro config"`.

- [ ] **Step 3: Implement**

Create `internal/cli/layer_copy.go`:

```go
package cli

import (
	"bytes"
	"context"
	"errors"

	"gocloud.dev/blob"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
)

// writeLayerCopy makes builds/<slug>/project-layer.yaml, the copy the daily
// image check and Cloud Build read (decision L6), hold data, writing only
// when it differs; nil data removes a copy.
func writeLayerCopy(ctx context.Context, b *blobx.Bucket, slug string, data []byte) error {
	key := config.LayerCopyKey(slug)
	old, _, err := b.ReadMax(ctx, key, config.LayerMaxBytes)
	exists := err == nil || errors.Is(err, blobx.ErrTooLarge)
	switch {
	case err == nil && data != nil && bytes.Equal(old, data):
		return nil
	case exists, errors.Is(err, blobx.ErrNotExist):
	default:
		return err
	}
	if data == nil {
		if exists {
			return b.Delete(ctx, key)
		}
		return nil
	}
	return b.WriteAll(ctx, key, data, &blob.WriterOptions{ContentType: "application/yaml"})
}
```

Create `internal/cli/config_publish.go`:

```go
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/initflow"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/task"
)

func newConfigPublishCmd() *cobra.Command {
	var (
		o          cloudOptions
		executable bool
	)
	cmd := &cobra.Command{
		Use:   "publish FILE",
		Short: "Publish the project layer: defaults and profiles for every repository of the project (fugaro/project-layer.yaml in the runs bucket)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if m := agentMarker(os.Getenv); m != "" {
				return userErr("nothing was published: fugaro config publish writes to the cloud and changes what every repository of the project runs: %s", initflow.AgentRefusal(m))
			}
			data, err := readLayerFile(args[0])
			if err != nil {
				return userErr("nothing was published: %v", err)
			}
			env, err := openCloud(ctx, o)
			if err != nil {
				return err
			}
			defer env.Close()
			if note := projectRecipesNote(env.lc); note != "" {
				return userErr("nothing was published: %s", strings.Replace(note, "project recipes need", "the project layer needs", 1))
			}
			if fakeEndpointsOnGS(env.lc, env.lc.BucketURL()) {
				return userErr("nothing was published: the project's storage endpoint is a fake")
			}
			l, ps := config.ParseProjectLayer(data, config.LayerAnchor{Project: env.lc.Name, GCPProject: env.lc.GCPProject})
			if len(ps) > 0 {
				return userErr("nothing was published: %s is invalid: %s", args[0], pluginwire.Printable(layerProblemsText(ps)))
			}
			gen, err := publishLayer(ctx, cmd.ErrOrStderr(), env.bucket, l, executable)
			if err != nil {
				return err
			}
			bucket := "fugaro-runs-" + env.lc.GCPProject
			_ = localcfg.SaveLayerCache(os.Getenv, env.lc.Name, localcfg.SharedCacheEntry{GCPProject: env.lc.GCPProject, Bucket: bucket, Generation: gen, CheckedAt: time.Now(), YAML: string(data)})
			fmt.Fprintf(cmd.OutOrStdout(), "published the project layer of %s to gs://%s/%s (generation %d, sha256 %s)\n", env.lc.Name, bucket, config.LayerKey, gen, l.SHA256)
			failed := fanOutLayer(ctx, cmd.OutOrStdout(), env, l)
			warnOldImages(ctx, cmd.ErrOrStderr(), env)
			if failed > 0 {
				return remote(fmt.Errorf("the project layer is published, but %d repository copy(ies) failed (listed above); run fugaro config publish again", failed))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&executable, "executable-changes", false, "allow a change to a profile's commands, image.apt or image.setup (they run as shell in every repository that takes the profile)")
	addCloudFlags(cmd, &o)
	return cmd
}

// layerPublishRace is a test seam between publish's read of the existing
// object and its conditional write.
var layerPublishRace = func(ctx context.Context, b *blobx.Bucket) {}

// publishLayer writes l to config.LayerKey, showing on w what it replaces
// and every executable change, and only if the object is still the one it
// read (or still absent): a concurrent publisher is refused, never
// overwritten. An executable change needs executable (decision L7). It
// returns the new generation.
func publishLayer(ctx context.Context, w io.Writer, b *blobx.Bucket, l *config.ProjectLayer, executable bool) (int64, error) {
	old, gen, rerr := b.ReadMaxStrict(ctx, config.LayerKey, config.LayerMaxBytes)
	var prev *config.ProjectLayer
	switch {
	case rerr == nil:
		if string(old) == string(l.Raw) {
			fmt.Fprintln(w, "note: the project already has this exact layer; writing it again")
		} else {
			fmt.Fprintf(w, "replacing the project layer (sha256 %s -> %s):\n%s", config.LayerSum(old), l.SHA256, printableLines(lineDiff(string(old), string(l.Raw))))
		}
		// An unparseable old object compares as empty: every executable
		// key of the new one counts as changed.
		prev, _ = config.ParseProjectLayer(old, config.LayerAnchor{})
	case errors.Is(rerr, blobx.ErrNotExist):
	case errors.Is(rerr, blobx.ErrTooLarge):
		return 0, userErr("nothing was published: the existing %s is over the %d KiB limit: delete it by hand, then run this again", config.LayerKey, config.LayerMaxBytes>>10)
	default:
		return 0, remote(fmt.Errorf("nothing was published: reading %s before replacing it: %w", config.LayerKey, rerr))
	}
	if changes := executableChanges(prev, l); len(changes) > 0 {
		fmt.Fprintln(w, "=== EXECUTABLE CHANGES: these run as shell in every repository that takes the profile ===")
		for _, c := range changes {
			fmt.Fprintln(w, "  "+pluginwire.Printable(c))
		}
		if !executable {
			return 0, userErr("nothing was published: the layer changes commands or image steps (shown above); review them, then run again with --executable-changes")
		}
	}
	layerPublishRace(ctx, b)
	var err error
	if rerr == nil {
		gen, err = b.ReplaceIfType(ctx, config.LayerKey, l.Raw, "application/yaml", gen, old)
	} else {
		gen, err = b.Create(ctx, config.LayerKey, l.Raw, "application/yaml")
	}
	switch {
	case errors.Is(err, blobx.ErrConflict), errors.Is(err, blobx.ErrExists):
		return 0, userErr("nothing was published: another publisher changed %s while this ran; look at it (fugaro config layer) and run this again", config.LayerKey)
	case err != nil:
		return 0, remote(fmt.Errorf("nothing was published: writing %s: %w", config.LayerKey, err))
	}
	return gen, nil
}

// executableChanges lists, per profile, each executable key
// (config.ExecutableKeys) whose value differs between prev (nil: none) and
// next, as `profile p: commands.test: "old" -> "new"`.
func executableChanges(prev, next *config.ProjectLayer) []string {
	values := func(l *config.ProjectLayer, name string) map[string]string {
		out := map[string]string{}
		if l == nil {
			return out
		}
		p, ok := l.Profiles[name]
		if !ok {
			return out
		}
		out["commands.build"], out["commands.test"] = p.Commands.Build, p.Commands.Test
		if rf := p.Commands.RerunFailed; rf != nil {
			out["commands.rerun_failed"] = rf.Command + " " + rf.Each
		}
		if len(p.Image.Apt) > 0 {
			out["image.apt"] = fmt.Sprintf("%q", p.Image.Apt)
		}
		if len(p.Image.Setup) > 0 {
			out["image.setup"] = fmt.Sprintf("%q", p.Image.Setup)
		}
		return out
	}
	names := map[string]bool{}
	for _, l := range []*config.ProjectLayer{prev, next} {
		if l != nil {
			for n := range l.Profiles {
				names[n] = true
			}
		}
	}
	var out []string
	for _, name := range slices.Sorted(maps.Keys(names)) {
		a, b := values(prev, name), values(next, name)
		for _, k := range []string{"commands.build", "commands.test", "commands.rerun_failed", "image.apt", "image.setup"} {
			if a[k] != b[k] {
				out = append(out, fmt.Sprintf("profile %s: %s: %q -> %q", name, k, a[k], b[k]))
			}
		}
	}
	return out
}

// fanOutLayer copies l's exact bytes to every repository the installation
// config lists (decision L19), reporting each, and returns how many copies
// failed. A repository whose provider neither the installation config nor
// the layer's defaults names has no slug yet; it is skipped with a note,
// and its next image build or init --repo writes its copy.
func fanOutLayer(ctx context.Context, w io.Writer, env *cloudEnv, l *config.ProjectLayer) (failed int) {
	for _, repo := range slices.Sorted(maps.Keys(env.lc.Repos)) {
		provider := env.lc.Repos[repo].Provider
		if provider == "" {
			provider = l.Defaults.Git.Provider
		}
		if provider == "" {
			fmt.Fprintf(w, "  %s: skipped: no provider is known for it yet; its next fugaro image build or fugaro init --repo writes its copy\n", repo)
			continue
		}
		slug, err := task.Slug(provider, repo)
		if err == nil {
			err = writeLayerCopy(ctx, env.bucket, slug, l.Raw)
		}
		if err != nil {
			fmt.Fprintf(w, "  %s: not copied: %s\n", repo, oneLineCLI(err.Error()))
			failed++
			continue
		}
		fmt.Fprintf(w, "  %s: copied to %s\n", repo, config.LayerCopyKey(slug))
	}
	return failed
}

// warnOldImages names every listed workflow whose build record says its job
// image predates layeredSince (decision L14): its launches are refused until
// fugaro image refresh. Best effort: a record it cannot read says nothing.
func warnOldImages(ctx context.Context, w io.Writer, env *cloudEnv) {
	b, err := env.recordBucket(ctx)
	if err != nil {
		return
	}
	for _, repo := range slices.Sorted(maps.Keys(env.lc.Repos)) {
		r := env.lc.Repos[repo]
		slug, err := task.Slug(r.Provider, repo)
		if err != nil {
			continue
		}
		for _, wf := range r.Workflows {
			data, _, err := b.Read(ctx, imagecheck.RecordKey(slug, wf))
			if err != nil {
				continue
			}
			rec, err := imagecheck.ParseRecord(data)
			if err != nil {
				continue
			}
			if m := baseRefReleaseRE.FindStringSubmatch(rec.BaseRef); m != nil && imagePredates(m[2], layeredSince) {
				fmt.Fprintf(w, "warning: %s workflow %s runs a job image built from %s (release %s), older than %s: its launches are refused until you run fugaro image refresh in its checkout\n",
					repo, wf, pluginwire.Printable(rec.BaseRef), m[2], layeredSince)
			}
		}
	}
}
```

In `internal/cli/configcmd.go`, make the line `cmd.AddCommand(newConfigShowCmd(), newConfigLayerCmd(), newConfigInitCmd(), newConfigPublishCmd())`.

- [ ] **Step 4: Run them and see them pass**

Run: `go test -race ./internal/cli/ -run 'TestPublish|TestConfig'`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/config_publish.go internal/cli/layer_copy.go internal/cli/config_publish_test.go internal/cli/configcmd.go
git commit -m "layered config task 13: fugaro config publish, guarded, conditional, with per-repository copies"
```

**Review (own reviewer, security):** this task is the trust boundary. Check the following:
- the agent-session refusal comes before any file, credential or network use;
- nothing is written when the file is invalid, the flag is missing or a race is lost;
- every string from the file or the old object reaches the terminal through `pluginwire.Printable` or `%q`;
- a copy is never written to a slug the installation does not list.

PR 3 ends with one full `go test -race ./internal/cli/... ./internal/localcfg/...` (in the foreground) and a PR titled "layered config 3/6: CLI resolution and commands".

---
## PR 4: Cloud Build and the check job

### Task 14: Builds carry the layer: the copy, `_PROJECT_LAYER_SHA256`, the render step

**Files:**
- Modify:
  - `images/derived/cloudbuild.yaml` (the substitution and the render step);
  - `internal/backend/gcp/build.go` (`BuildSpec`, `BuildRequest`, `check`);
  - `internal/cli/image.go` (`cloudBuildSpec`, `newImageRenderCmd`, the image build path);
  - `internal/cli/init.go` (`submitAndWait`).
- Create: `internal/cli/image_layer.go`, `internal/cli/image_layer_test.go`
- Test: `internal/backend/gcp/build_test.go`, `images/cloudbuild_test.go`

**Interfaces:**
- Consumes: `config.Config.Layer` (Task 4), `writeLayerCopy` (Task 13; if Task 13 has not merged, create `internal/cli/layer_copy.go` with exactly Task 13's content), `loadCheckoutResolved`, `layerOptions{Data, Where}` (Tasks 9 and 10), `layeredSince` (Task 11).
- Produces:
  ```go
  // gcp.BuildSpec gains:
  ProjectLayerSHA256 string // the project layer's sha256, "" for none: the render step reads the repository's copy and refuses another
  // cli
  func layerSHAOf(cfg *config.Config) string
  func prepareLayerCopy(ctx context.Context, b *blobx.Bucket, slug string, l *config.ProjectLayer, base string) (string, error) // only with a layer; builds without one never touch a copy
  func readLayerCopy(ctx context.Context, bucketURL, slug, sha string) ([]byte, error)
  // fugaro image render gains hidden --layer-bucket, --layer-slug, --layer-sha256
  ```

- [ ] **Step 1: Write the failing tests**

Append to `internal/backend/gcp/build_test.go`:

```go
func TestBuildRequestCarriesTheLayerSHA(t *testing.T) {
	spec := buildSpec(t)
	b, err := BuildRequest("proj-1234", spec)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := b.Substitutions["_PROJECT_LAYER_SHA256"]; !ok || v != "" {
		t.Fatalf("no layer: substitution %q, %v", v, ok)
	}
	spec.ProjectLayerSHA256 = strings.Repeat("a", 64)
	if b, err = BuildRequest("proj-1234", spec); err != nil || b.Substitutions["_PROJECT_LAYER_SHA256"] != spec.ProjectLayerSHA256 {
		t.Fatalf("a layer: %v, %v", b.Substitutions, err)
	}
	spec.ProjectLayerSHA256 = "short"
	if _, err := BuildRequest("proj-1234", spec); !errors.Is(err, ErrBadBuildSpec) {
		t.Fatalf("a bad sum: %v", err)
	}
}
```

Append to `images/cloudbuild_test.go` (package `images_test`; it already imports `strings`, `testing`, `gopkg.in/yaml.v3` and `github.com/dimipaun/fugaro/images`; add whichever is missing):

```go
func TestRenderStepTakesTheProjectLayer(t *testing.T) {
	var cb struct {
		Substitutions map[string]string `yaml:"substitutions"`
		Steps         []struct {
			ID   string   `yaml:"id"`
			Env  []string `yaml:"env"`
			Args []string `yaml:"args"`
		} `yaml:"steps"`
	}
	if err := yaml.Unmarshal(images.CloudBuild, &cb); err != nil {
		t.Fatal(err)
	}
	if v, ok := cb.Substitutions["_PROJECT_LAYER_SHA256"]; !ok || v != "" {
		t.Fatalf("_PROJECT_LAYER_SHA256 = %q, %v", v, ok)
	}
	for _, s := range cb.Steps {
		if s.ID != "render" {
			continue
		}
		env, script := strings.Join(s.Env, " "), strings.Join(s.Args, " ")
		for _, want := range []string{"LAYER_SHA=${_PROJECT_LAYER_SHA256}", "BUCKET=${_BUCKET}", "SLUG=${_SLUG}"} {
			if !strings.Contains(env, want) {
				t.Errorf("render env lacks %s", want)
			}
		}
		for _, want := range []string{`if [ -n "$$LAYER_SHA" ]`, `--layer-sha256 "$$LAYER_SHA"`, `fugaro image render --workflow "$$WORKFLOW" --cloud-outputs /workspace/out "$$@"`} {
			if !strings.Contains(script, want) {
				t.Errorf("render script lacks %s", want)
			}
		}
		return
	}
	t.Fatal("no render step")
}
```

Create `internal/cli/image_layer_test.go`:

```go
package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/infra"
)

// Review Focus 3.
func TestImageBuildRefusesOldBaseWithLayer(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	l, ps := config.ParseProjectLayer([]byte(testProjectLayer), config.LayerAnchor{})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	slug := mustSlug("github", "acme/other")
	ctx := context.Background()
	if _, err := prepareLayerCopy(ctx, env.bucket, slug, l, "ghcr.io/dimipaun/fugaro-web-node:0.5.1"); ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "fugaro image refresh") {
		t.Fatalf("old base: %v", err)
	}
	if bucketText(t, f, config.LayerCopyKey(slug)) != "" {
		t.Fatal("a copy was written for a refused build")
	}
	sha, err := prepareLayerCopy(ctx, env.bucket, slug, l, release060)
	if err != nil || sha != l.SHA256 || bucketText(t, f, config.LayerCopyKey(slug)) != testProjectLayer {
		t.Fatalf("0.6.0 base: %q, %v", sha, err)
	}
	if sha, err := prepareLayerCopy(ctx, env.bucket, slug, l, release060); err != nil || sha != l.SHA256 {
		t.Fatalf("an unchanged copy: %q, %v", sha, err)
	}
}

func TestCloudBuildSpecCarriesTheLayer(t *testing.T) {
	l, _ := config.ParseProjectLayer([]byte(testProjectLayer), config.LayerAnchor{})
	cfg, _, ps := config.Resolve([]byte(minimalAnchored), l)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	rs := infra.RepoSpec{Slug: "s", Provider: "github", Workflows: map[string]infra.WorkflowSpec{config.ImplicitWorkflow: {}}}
	spec, err := cloudBuildSpec(rs, cfg, config.ImplicitWorkflow, release060, "E2_HIGHCPU_8", "gs://fugaro-runs-proj-1234")
	if err != nil || spec.ProjectLayerSHA256 != l.SHA256 {
		t.Fatalf("spec %+v, %v", spec, err)
	}
}

func TestImageRenderReadsTheCopyAndChecksItsSum(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, "")
	layerCheckout(t, f, minimalAnchored)
	slug := mustSlug("github", "acme/other")
	writeBucketFile(t, f, config.LayerCopyKey(slug), testProjectLayer)
	sum := config.LayerSum([]byte(testProjectLayer))
	out, _, err := execute(t, "image", "render", "--layer-bucket", f.bucket, "--layer-slug", slug, "--layer-sha256", sum)
	if err != nil || !strings.Contains(out, "FROM") {
		t.Fatalf("out %q, err %v", out, err)
	}
	_, _, err = execute(t, "image", "render", "--layer-bucket", f.bucket, "--layer-slug", slug, "--layer-sha256", strings.Repeat("0", 64))
	if ExitCode(err) != ExitUserError || !strings.Contains(err.Error(), "changed while the build was queued") {
		t.Fatalf("a stale sum: %v", err)
	}
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/backend/gcp/ -run TestBuildRequestCarriesTheLayerSHA; go test ./images/ -run TestRenderStepTakesTheProjectLayer; go test ./internal/cli/ -run 'TestImageBuildRefusesOldBaseWithLayer|TestCloudBuildSpecCarriesTheLayer|TestImageRenderReadsTheCopy'`
Expected: FAIL. The gcp and cli tests do not compile (`unknown field ProjectLayerSHA256`, `undefined: prepareLayerCopy`). The images test reports `_PROJECT_LAYER_SHA256 = "", false`.

- [ ] **Step 3: Implement**

**`images/derived/cloudbuild.yaml`.**
- Under `substitutions:`, add:
  ```yaml
  _PROJECT_LAYER_SHA256: ""  # the project layer's sha256, "" for none: render reads builds/<slug>/project-layer.yaml and refuses another (docs/design/layered-config.md §8)
  ```
- Replace the render step's `env:` and `args:` with:
  ```yaml
      env: [GIT_CONFIG_COUNT=1, GIT_CONFIG_KEY_0=safe.directory, "GIT_CONFIG_VALUE_0=*", "WORKFLOW=${_WORKFLOW}", "BUCKET=${_BUCKET}", "SLUG=${_SLUG}", "LAYER_SHA=${_PROJECT_LAYER_SHA256}"]
      args:
        - -c
        - |
          set -eu
          set --
          if [ -n "$$LAYER_SHA" ]; then
            set -- --layer-bucket "$$BUCKET" --layer-slug "$$SLUG" --layer-sha256 "$$LAYER_SHA"
          fi
          fugaro image render --workflow "$$WORKFLOW" --cloud-outputs /workspace/out "$$@" > /workspace/context/Dockerfile
  ```
- Extend the comment above the render step with one line: "With a project layer (_PROJECT_LAYER_SHA256 set) it resolves fugaro.yaml over the repository's copy of it, read as the build account, whose sum must be the one the build was submitted with."

With the substitution empty, the command is the one before this change. That keeps old base images, which never get a sum, unaffected: Task 14's gate refuses to send a sum to them.

**`internal/backend/gcp/build.go`.**
- Add to `BuildSpec`:
  ```go
  	// ProjectLayerSHA256 is the project layer's sha256, "" for none: the
  	// render step reads the repository's copy (builds/<Slug>/project-layer.yaml)
  	// and refuses one with another sum.
  	ProjectLayerSHA256 string
  ```
- In `BuildRequest`, add `"_PROJECT_LAYER_SHA256": s.ProjectLayerSHA256,` to the `b.Substitutions` literal. The template references it, so it is always sent.
- In `check`, before the `bucketURLRE` check, add:
  ```go
  	if s.ProjectLayerSHA256 != "" && !sha256HexRE.MatchString(s.ProjectLayerSHA256) {
  		return fmt.Errorf("the project layer sum %q is not 64 hex digits", s.ProjectLayerSHA256)
  	}
  ```
  and to the `var (...)` block of regexps: `sha256HexRE = regexp.MustCompile(\`^[0-9a-f]{64}$\`)`.

**`internal/cli/image.go`.**
- In `cloudBuildSpec`'s returned literal, add `ProjectLayerSHA256: layerSHAOf(cfg),`.
- In `runImageBuildCloud`, the build must read the layer strictly (a transient read failure must not build without it). Replace

  ```go
  	_, cfg, name, err := loadCheckout(ctx, o.workflow)
  	if err != nil {
  		return err
  	}
  ```

  with

  ```go
  	_, rf, err := loadCheckoutResolved(ctx, "", lc, layerOptions{})
  	if err != nil {
  		return err
  	}
  	cfg := rf.Cfg
  	name, _, err := cfg.SelectWorkflow(o.workflow)
  	if err != nil {
  		return &ExitError{Code: ExitUserError, Err: err}
  	}
  ```

  Then, after `spec, err := cloudBuildSpec(rs, cfg, name, base, lc.Build.MachineType, lc.RecordBucketURL())` and its error check, add:

  ```go
  	if cfg.Layer != nil {
  		rb, err := env.recordBucket(ctx)
  		if err != nil {
  			return remote(err)
  		}
  		if spec.ProjectLayerSHA256, err = prepareLayerCopy(ctx, rb, rs.Slug, cfg.Layer, base); err != nil {
  			return err
  		}
  	}
  ```

- In `newImageRenderCmd`, add three hidden flags, and read the copy before loading the checkout:
  ```go
  	var layerBucket, layerSlug, layerSHA string
  	// in RunE, replacing `root, cfg, name, err := loadCheckout(cmd.Context(), workflow)`:
  			var o layerOptions
  			if layerSHA != "" {
  				data, err := readLayerCopy(cmd.Context(), layerBucket, layerSlug, layerSHA)
  				if err != nil {
  					return err
  				}
  				o = layerOptions{Data: data, Where: layerBucket + "/" + config.LayerCopyKey(layerSlug)}
  			}
  			o.Lenient = true
  			root, rf, err := loadCheckoutResolved(cmd.Context(), "", nil, o)
  			if err != nil {
  				return err
  			}
  			cfg := rf.Cfg
  			name, _, err := cfg.SelectWorkflow(workflow)
  			if err != nil {
  				return &ExitError{Code: ExitUserError, Err: err}
  			}
  	// after the existing flags:
  	cmd.Flags().StringVar(&layerBucket, "layer-bucket", "", "the runs bucket holding the repository's copy of the project layer (used by the image build)")
  	cmd.Flags().StringVar(&layerSlug, "layer-slug", "", "the repository's storage slug (used by the image build)")
  	cmd.Flags().StringVar(&layerSHA, "layer-sha256", "", "the project layer's sha256 the build was submitted with (used by the image build)")
  	for _, n := range []string{"layer-bucket", "layer-slug", "layer-sha256"} {
  		_ = cmd.Flags().MarkHidden(n)
  	}
  ```

**`internal/cli/init.go`.** Add `"github.com/dimipaun/fugaro/internal/blobx"` to its imports. In `submitAndWait`, after `bs, err := cloudBuildSpec(...)` and its error check, add:

```go
	if cfg.Layer != nil {
		rb, err := blobx.Open(ctx, lc.RecordBucketURL())
		if err != nil {
			return "", remote(err)
		}
		defer rb.Close()
		if bs.ProjectLayerSHA256, err = prepareLayerCopy(ctx, rb, spec.Slug, cfg.Layer, base); err != nil {
			return "", err
		}
	}
```

`fugaro image refresh` builds through `submitAndWait`, so it is covered. The check job's `submitRebuild` passes `cfg.Layer`'s sum through `cloudBuildSpec`. Its copy is the one it read (Task 15), so it writes nothing.

Create `internal/cli/image_layer.go`:

```go
package cli

import (
	"context"
	"fmt"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/pluginwire"
)

// layerSHAOf is the sum of the project layer cfg was resolved over, "" for
// none: what a build of it is submitted with.
func layerSHAOf(cfg *config.Config) string {
	if cfg == nil || cfg.Layer == nil {
		return ""
	}
	return cfg.Layer.SHA256
}

// prepareLayerCopy makes the repository's copy hold the layer a build is
// about to resolve against (decision L6) and returns its sum for the
// build. A layer needs a base whose fugaro renders with it (layeredSince):
// an older release base is refused before anything is written. Callers
// call it only with a layer; a build without one never touches a copy
// (to retire a layer, publish one with no defaults and no profiles).
func prepareLayerCopy(ctx context.Context, b *blobx.Bucket, slug string, l *config.ProjectLayer, base string) (string, error) {
	if m := baseRefReleaseRE.FindStringSubmatch(base); m != nil && imagePredates(m[2], layeredSince) {
		return "", userErr("the project layer needs a base image whose fugaro reads it (fugaro %s or later), but this build starts from %s, release %s: run fugaro image refresh in the checkout, in your own terminal window",
			layeredSince, pluginwire.Printable(base), m[2])
	}
	if err := writeLayerCopy(ctx, b, slug, l.Raw); err != nil {
		return "", remote(fmt.Errorf("writing the project layer copy %s: %w", config.LayerCopyKey(slug), err))
	}
	return l.SHA256, nil
}

// readLayerCopy reads the repository's copy of the project layer for a
// Cloud Build render, as the build account, and refuses one whose sum is
// not the one the build was submitted with.
func readLayerCopy(ctx context.Context, bucketURL, slug, sha string) ([]byte, error) {
	b, err := blobx.Open(ctx, bucketURL)
	if err != nil {
		return nil, remote(err)
	}
	defer b.Close()
	key := config.LayerCopyKey(slug)
	data, _, err := b.ReadMax(ctx, key, config.LayerMaxBytes)
	if err != nil {
		return nil, remote(fmt.Errorf("reading the project layer copy %s: %w", key, err))
	}
	if got := config.LayerSum(data); got != sha {
		return nil, userErr("the project layer changed while the build was queued (its copy %s has sha256 %s, the build was submitted with %s): run the build again", key, got, pluginwire.Printable(sha))
	}
	return data, nil
}
```

- [ ] **Step 4: Run them and see them pass**

Run: `go test -race ./internal/backend/gcp/ -run TestBuildRequest && go test -race ./images/ -run 'TestRenderStep|TestCloudBuild' && go test -race ./internal/cli/ -run 'TestImage|TestCloudBuildSpec|TestInit.*Build|TestImageRefresh'`
Expected: `ok` for all three. `TestCloudBuildNoSubstitutionsInScripts` stays green, since the script reads only `$$` variables.

- [ ] **Step 5: Commit**

```bash
git add images/derived/cloudbuild.yaml images/cloudbuild_test.go internal/backend/gcp/build.go internal/backend/gcp/build_test.go internal/cli/image.go internal/cli/init.go internal/cli/image_layer.go internal/cli/image_layer_test.go
git commit -m "layered config task 14: image builds resolve over the repository's copy of the project layer, pinned by its sum"
```

### Task 15: The daily check job reads its copy; one sum for every consumer

**Files:**
- Modify: `internal/cli/imagecheck.go` (`readHeadConfig` and its two calls)
- Create: `internal/cli/imagecheck_layer_test.go`, `internal/cli/consumers_test.go`

**Interfaces:**
- Consumes: `resolveFugaroYAML`, `layerOptions{Data, Where, NoBucket}` (Task 9), `embedProjectLayer` (Task 11), `readLayerCopy`, `loadCheckoutResolved`, `writeLayerCopy` (Tasks 10, 13 and 14).
- Produces:
  ```go
  func readHeadConfig(ctx context.Context, tree *imagecheck.GitTree, lc *localcfg.Config, o layerOptions) (*config.Config, error) // was (tree)
  func jobLayerOptions(ctx context.Context, b *blobx.Bucket, slug string) (layerOptions, error)
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/cli/imagecheck_layer_test.go`:

```go
package cli

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/testutil"
)

func cloneOf(t *testing.T, files map[string]string) *imagecheck.GitTree {
	t.Helper()
	testutil.IsolateGit(t)
	remote := testutil.NewRemote(t, files)
	tree, err := imagecheck.Clone(context.Background(), imagecheck.CloneOptions{URL: remote, Branch: "main", Dir: filepath.Join(t.TempDir(), "repo")})
	if err != nil {
		t.Fatal(err)
	}
	return tree
}

func TestCheckJobResolvesOverItsCopy(t *testing.T) {
	f := newCloudFixture(t)
	env := fileEnv(t, f)
	slug := mustSlug("github", "acme/other")
	tree := cloneOf(t, map[string]string{"fugaro.yaml": minimalAnchored})
	ctx := context.Background()
	lo, err := jobLayerOptions(ctx, env.bucket, slug)
	if err != nil || !lo.NoBucket || lo.Data != nil {
		t.Fatalf("no copy: %+v, %v", lo, err)
	}
	if _, err := readHeadConfig(ctx, tree, nil, lo); err == nil || !strings.Contains(err.Error(), "must define at least one workflow") {
		t.Fatalf("a minimal file without a copy: %v", err)
	}
	writeBucketFile(t, f, config.LayerCopyKey(slug), testProjectLayer)
	if lo, err = jobLayerOptions(ctx, env.bucket, slug); err != nil || lo.Data == nil {
		t.Fatalf("a copy: %+v, %v", lo, err)
	}
	cfg, err := readHeadConfig(ctx, tree, nil, lo)
	if err != nil || cfg.Workflows[config.ImplicitWorkflow].Commands.Test != "sh test.sh" || layerSHAOf(cfg) != config.LayerSum([]byte(testProjectLayer)) {
		t.Fatalf("cfg %+v, %v", cfg, err)
	}
	writeBucketFile(t, f, config.LayerCopyKey(slug), strings.Replace(testProjectLayer, "  agent: { auth: api-key }\n", "  agent: { auth: api-key }\n  budget: {}\n", 1))
	lo, _ = jobLayerOptions(ctx, env.bucket, slug)
	if _, err := readHeadConfig(ctx, tree, nil, lo); err == nil || !strings.Contains(err.Error(), "is invalid") {
		t.Fatalf("an invalid copy: %v", err)
	}
}
```

Create `internal/cli/consumers_test.go`:

```go
package cli

import (
	"context"
	"io"
	"testing"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/task"
)

// Review Focus 2: one repository file and one layer give one resolved
// config, whichever consumer resolves it. The runner is pinned to the
// task's path by TestRunnerRecordsTheLayer (Task 7), which compares its
// record with config.Resolve over the same embedded text.
func TestEveryConsumerResolvesTheSameBytes(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	repoYAML := minimalAnchored + "agent:\n  review_rounds: 3\n"
	dir := layerCheckout(t, f, repoYAML)
	env := fileEnv(t, f)
	ctx := context.Background()
	slug := mustSlug("github", "acme/other")
	if err := writeLayerCopy(ctx, env.bucket, slug, []byte(testProjectLayer)); err != nil {
		t.Fatal(err)
	}
	sums := map[string]string{}

	rf, err := resolveFugaroYAML(ctx, []byte(repoYAML), env.lc, layerOptions{})
	if err != nil || len(rf.Problems) > 0 {
		t.Fatal(err, rf.Problems)
	}
	sums["cli (validate, run, config show)"] = rf.Res.ConfigSHA256

	spec := &task.Spec{Version: 1, RunID: "20261008-100000-abcd", Repo: "acme/other", Ref: "main", Task: "x"}
	if err := embedProjectLayer(ctx, env, spec, io.Discard); err != nil || spec.ProjectLayer == nil {
		t.Fatal(err)
	}
	l, ps := config.ParseProjectLayer([]byte(spec.ProjectLayer.YAML), config.LayerAnchor{})
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	c, _, ps := config.Resolve([]byte(repoYAML), l)
	if len(ps) > 0 {
		t.Fatal(ps)
	}
	sums["runner (the task's embedded text)"] = c.SHA256()

	data, err := readLayerCopy(ctx, f.bucket, slug, config.LayerSum([]byte(testProjectLayer)))
	if err != nil {
		t.Fatal(err)
	}
	_, rr, err := loadCheckoutResolved(ctx, dir, nil, layerOptions{Data: data, Where: "copy"})
	if err != nil {
		t.Fatal(err)
	}
	sums["cloud build render (the copy)"] = rr.Res.ConfigSHA256

	lo, err := jobLayerOptions(ctx, env.bucket, slug)
	if err != nil {
		t.Fatal(err)
	}
	head, err := readHeadConfig(ctx, cloneOf(t, map[string]string{"fugaro.yaml": repoYAML}), nil, lo)
	if err != nil {
		t.Fatal(err)
	}
	sums["check job (the copy, over head)"] = head.SHA256()

	want := sums["cli (validate, run, config show)"]
	for who, got := range sums {
		if got != want {
			t.Errorf("%s resolved %s, the CLI %s", who, got, want)
		}
	}
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/cli/ -run 'TestCheckJobResolvesOverItsCopy|TestEveryConsumerResolvesTheSameBytes'`
Expected: FAIL to compile: `undefined: jobLayerOptions`, `too many arguments in call to readHeadConfig`.

- [ ] **Step 3: Implement**

In `internal/cli/imagecheck.go`, replace `readHeadConfig` with:

```go
// readHeadConfig reads head's fugaro.yaml and resolves it over the project
// layer o finds (docs/design/layered-config.md §8): the check job passes
// its copy (jobLayerOptions); fugaro image check run locally reads the
// published layer with the operator's access (lc).
func readHeadConfig(ctx context.Context, tree *imagecheck.GitTree, lc *localcfg.Config, o layerOptions) (*config.Config, error) {
	data, err := tree.ReadFile("fugaro.yaml")
	if err != nil {
		return nil, fmt.Errorf("the base branch's fugaro.yaml: %w", err)
	}
	rf, err := resolveFugaroYAML(ctx, data, lc, o)
	if err != nil {
		return nil, err
	}
	if len(rf.Problems) > 0 {
		msgs := make([]string, len(rf.Problems))
		for i, p := range rf.Problems {
			msgs[i] = p.String()
		}
		return nil, fmt.Errorf("the base branch's fugaro.yaml has %d problem(s): %s", len(rf.Problems), strings.Join(msgs, "; "))
	}
	return rf.Cfg, nil
}

// jobLayerOptions are the check job's: its repository's copy of the project
// layer (decision L6), in the build account's own prefix, read with the
// ordinary read (a prefix-conditioned grant answers a missing object with
// 403, which reads as absent). No copy, no layer; the canonical object is
// never read (the account cannot).
func jobLayerOptions(ctx context.Context, b *blobx.Bucket, slug string) (layerOptions, error) {
	key := config.LayerCopyKey(slug)
	data, _, err := b.ReadMax(ctx, key, config.LayerMaxBytes)
	switch {
	case err == nil:
		return layerOptions{Data: data, Where: key, NoBucket: true}, nil
	case errors.Is(err, blobx.ErrNotExist):
		return layerOptions{NoBucket: true}, nil
	}
	return layerOptions{}, fmt.Errorf("reading the project layer copy %s: %w", key, err)
}
```

In the job path, replace `cfg, err := readHeadConfig(tree)` with:

```go
	lo, err := jobLayerOptions(ctx, bucket, slug)
	if err != nil {
		return failAll(err)
	}
	cfg, err := readHeadConfig(ctx, tree, nil, lo)
```

In `runImageCheckLocal`, replace `cfg, err := readHeadConfig(tree)` with `cfg, err := readHeadConfig(ctx, tree, lc, layerOptions{})`.

`imagecheck.ImageConfigHash` hashes the resolved workflow, so a profile change to `base` or `image.*` reaches the image-config trigger with no change in `internal/imagecheck`. `TestCheckJobResolvesOverItsCopy` covers the read; the existing `TestDecide*` tables cover the trigger.

- [ ] **Step 4: Run them and see them pass**

Run: `go test -race ./internal/cli/ -run 'TestCheckJob|TestEveryConsumer|TestImageCheck'`
Expected: `ok`, with the existing check job tests unchanged.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/imagecheck.go internal/cli/imagecheck_layer_test.go internal/cli/consumers_test.go
git commit -m "layered config task 15: the daily check resolves over its copy; one sum for every consumer"
```

PR 4 ends with one full `go test -race ./internal/cli/... ./internal/backend/gcp/... ./images/...` and a PR titled "layered config 4/6: Cloud Build and the check job".

---

## PR 5: visibility

### Task 16: `doctor` shows the layer and flags drift, stale copies and stale images

**Files:**
- Modify: `internal/cli/doctor.go` (the checkout block)
- Create: `internal/cli/doctor_layer.go`, `internal/cli/doctor_layer_test.go`

**Interfaces:**
- Consumes:
  - `resolveFugaroYAML` (Task 9);
  - `runstore.ListRunIDs` (newest first), `runstore.Open(...).ReadRecord`, `ProjectLayerRecord` (Task 7);
  - `imagecheck.RecordKey`, `ParseRecord`, `ImageConfigHash`;
  - `readOrigin`, `task.Slug`, `cloudEnv.recordBucket`.
- Produces:
  ```go
  func doctorLayerChecks(ctx context.Context, lc *localcfg.Config, root string) []doctorCheck
  func layerDrift(ctx context.Context, b *blobx.Bucket, slug string, l *config.ProjectLayer) []doctorCheck
  func layerCopyCheck(ctx context.Context, b *blobx.Bucket, slug string, l *config.ProjectLayer) []doctorCheck
  func imageConfigChecks(ctx context.Context, rb *blobx.Bucket, slug string, cfg *config.Config, res *config.Resolution) []doctorCheck
  // shortSHA comes from Task 10 (validate.go)
  ```

- [ ] **Step 1: Write the failing test**

Create `internal/cli/doctor_layer_test.go`:

```go
package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/runstore"
)

func layerCheck(cs []doctorCheck, id string) *doctorCheck {
	for i := range cs {
		if cs[i].ID == id {
			return &cs[i]
		}
	}
	return nil
}

func writeRunRecord(t *testing.T, f *cloudFixture, slug, id string, pl *runstore.ProjectLayerRecord) {
	t.Helper()
	data, err := json.Marshal(runstore.Record{Version: 1, RunID: id, Repo: "acme/other", Status: runstore.StatusSucceeded,
		StartedAt: time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC), ProjectLayer: pl})
	if err != nil {
		t.Fatal(err)
	}
	writeBucketFile(t, f, "runs/"+slug+"/"+id+"/result.json", string(data))
}

// Review Focus 1.
func TestDoctorFlagsLayerDrift(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	slug := mustSlug("github", "acme/other")
	lc := fileEnv(t, f).lc
	ctx := context.Background()

	cs := doctorLayerChecks(ctx, lc, dir)
	if c := layerCheck(cs, "project-layer"); c == nil || c.Severity != "info" || !strings.Contains(c.Problem, "project layer aurora generation") {
		t.Fatalf("checks %+v", cs)
	}
	if c := layerCheck(cs, "project-layer-copy"); c == nil || c.Severity != "warning" || !strings.Contains(c.Problem, "has no copy") {
		t.Fatalf("no copy: %+v", cs)
	}

	writeRunRecord(t, f, slug, "20261008-090000-aaaa", nil)
	if c := layerCheck(doctorLayerChecks(ctx, lc, dir), "project-layer-drift"); c == nil || !strings.Contains(c.Problem, "ran without the project layer") {
		t.Fatalf("a run without the layer: %+v", c)
	}
	old := strings.Repeat("a", 64)
	writeRunRecord(t, f, slug, "20261008-091000-bbbb", &runstore.ProjectLayerRecord{SHA256: old, Generation: 1, Applied: true})
	if c := layerCheck(doctorLayerChecks(ctx, lc, dir), "project-layer-drift"); c == nil || !strings.Contains(c.Problem, "used project layer generation 1 (sha256 aaaaaaaaaaaa)") {
		t.Fatalf("a run on an older layer: %+v", c)
	}
	writeRunRecord(t, f, slug, "20261008-092000-cccc", &runstore.ProjectLayerRecord{SHA256: config.LayerSum([]byte(testProjectLayer)), Generation: 2, Applied: true})
	writeBucketFile(t, f, config.LayerCopyKey(slug), testProjectLayer)
	cs = doctorLayerChecks(ctx, lc, dir)
	if layerCheck(cs, "project-layer-drift") != nil || layerCheck(cs, "project-layer-copy") != nil {
		t.Fatalf("current run and copy still flagged: %+v", cs)
	}
}

func TestDoctorFlagsAStaleImage(t *testing.T) {
	f := newCloudFixture(t)
	isolateCache(t)
	publishedLayer(t, f, testProjectLayer)
	dir := layerCheckout(t, f, minimalAnchored)
	slug := mustSlug("github", "acme/other")
	data, _ := json.Marshal(imagecheck.Record{Version: 1, Repo: "acme/other", Workflow: config.ImplicitWorkflow, ImageConfigHash: strings.Repeat("0", 64), BaseRef: release060})
	writeBucketFile(t, f, imagecheck.RecordKey(slug, config.ImplicitWorkflow), string(data))
	c := layerCheck(doctorLayerChecks(context.Background(), fileEnv(t, f).lc, dir), "image-config-"+config.ImplicitWorkflow)
	if c == nil || c.Severity != "warning" || !strings.Contains(c.Problem, "other image settings") || !strings.Contains(c.Fix, "fugaro image build --workflow default") {
		t.Fatalf("check %+v", c)
	}
}
```

- [ ] **Step 2: Run it and see it fail**

Run: `go test ./internal/cli/ -run 'TestDoctorFlags(LayerDrift|AStaleImage)'`
Expected: FAIL to compile: `undefined: doctorLayerChecks`. (The helper is `layerCheck`, since `doctor_signers_test.go` already has a `checkByID`.)

- [ ] **Step 3: Implement**

Create `internal/cli/doctor_layer.go`:

```go
package cli

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"time"

	"github.com/dimipaun/fugaro/internal/blobx"
	"github.com/dimipaun/fugaro/internal/config"
	"github.com/dimipaun/fugaro/internal/imagecheck"
	"github.com/dimipaun/fugaro/internal/localcfg"
	"github.com/dimipaun/fugaro/internal/pluginwire"
	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/task"
)

// doctorLayerChecks are doctor's project layer lines (decision L20), in a
// checkout whose fugaro.yaml resolves:
//   - the layer that applies (info);
//   - whether the repository's last run used it;
//   - whether the repository's copy, which the daily check and Cloud Build
//     read, is current;
//   - whether each workflow's image was built from the image settings it
//     resolves to now.
//
// Best effort: what cannot be read says nothing.
func doctorLayerChecks(ctx context.Context, lc *localcfg.Config, root string) []doctorCheck {
	data, err := readFugaroYAML(filepath.Join(root, "fugaro.yaml"))
	if err != nil {
		return nil
	}
	rf, err := resolveFugaroYAML(ctx, data, lc, layerOptions{Lenient: true})
	if err != nil || rf.Cfg == nil {
		return nil // fugaroYAMLCheck reports it
	}
	l := rf.Layer.Layer
	if l == nil {
		msg := "no project layer applies to this checkout"
		if rf.Layer.Unknown {
			msg = "whether a project layer applies is unknown"
		}
		if rf.Layer.Note != "" {
			msg += ": " + rf.Layer.Note
		}
		return []doctorCheck{{ID: "project-layer", Severity: "info", Problem: msg}}
	}
	out := []doctorCheck{{ID: "project-layer", Severity: "info",
		Problem: fmt.Sprintf("project layer %s generation %d (sha256 %s) applies; fugaro config show prints where each value comes from", l.Project, rf.Layer.Generation, shortSHA(l.SHA256))}}
	oi, ok := readOrigin(ctx, root)
	if !ok {
		return out
	}
	slug, err := task.Slug(rf.Cfg.Git.Provider, oi.Repo)
	if err != nil {
		return out
	}
	b, err := blobx.Open(ctx, lc.BucketURL())
	if err != nil {
		return out
	}
	defer b.Close()
	out = append(out, layerDrift(ctx, b, slug, l)...)
	out = append(out, layerCopyCheck(ctx, b, slug, l)...)
	env := &cloudEnv{lc: lc, bucket: b}
	if rb, err := env.recordBucket(ctx); err == nil {
		out = append(out, imageConfigChecks(ctx, rb, slug, rf.Cfg, rf.Res)...)
	}
	return out
}

// layerDrift compares the repository's last run with the published layer.
func layerDrift(ctx context.Context, b *blobx.Bucket, slug string, l *config.ProjectLayer) []doctorCheck {
	ids, err := runstore.ListRunIDs(ctx, b.Bucket, slug, time.Time{})
	if err != nil || len(ids) == 0 {
		return nil
	}
	id := ids[0] // newest first
	rec, err := runstore.Open(b.Bucket, slug, id).ReadRecord(ctx)
	if err != nil {
		return nil
	}
	fix := "upgrade every teammate's fugaro CLI and CI pin to " + layeredSince + " or later"
	switch pl := rec.ProjectLayer; {
	case pl == nil:
		return []doctorCheck{{ID: "project-layer-drift", Severity: "warning",
			Problem: fmt.Sprintf("the last run %s ran without the project layer: it was launched by a fugaro CLI older than %s, or before the layer was published", id, layeredSince), Fix: fix}}
	case !pl.Applied:
		return []doctorCheck{{ID: "project-layer-drift", Severity: "warning",
			Problem: fmt.Sprintf("the last run %s did not apply the project layer: fugaro.yaml at its ref did not name the layer's project and gcp_project", id),
			Fix:     "merge the gcp_project: line, or launch from the repository's checkout"}}
	case pl.SHA256 != l.SHA256:
		return []doctorCheck{{ID: "project-layer-drift", Severity: "info",
			Problem: fmt.Sprintf("the last run %s used project layer generation %d (sha256 %s); the published one is sha256 %s", id, pl.Generation, pluginwire.Printable(shortSHA(pl.SHA256)), shortSHA(l.SHA256)),
			Fix:     "nothing, if the change was meant: the next run uses the published layer"}}
	}
	return nil
}

// layerCopyCheck compares the repository's copy with the published layer.
func layerCopyCheck(ctx context.Context, b *blobx.Bucket, slug string, l *config.ProjectLayer) []doctorCheck {
	const fix = "run fugaro config publish again, or fugaro image build in this checkout"
	data, _, err := b.ReadMax(ctx, config.LayerCopyKey(slug), config.LayerMaxBytes)
	switch {
	case errors.Is(err, blobx.ErrNotExist):
		return []doctorCheck{{ID: "project-layer-copy", Severity: "warning",
			Problem: "the repository has no copy of the project layer (" + config.LayerCopyKey(slug) + "), so its daily image check and its image builds resolve without it", Fix: fix}}
	case err != nil:
		return nil
	case config.LayerSum(data) != l.SHA256:
		return []doctorCheck{{ID: "project-layer-copy", Severity: "warning",
			Problem: fmt.Sprintf("the repository's copy of the project layer (sha256 %s) is not the published one (%s): its daily image check resolves against the old one", shortSHA(config.LayerSum(data)), shortSHA(l.SHA256)), Fix: fix}}
	}
	return nil
}

// imageConfigChecks names each workflow (with a generated image) whose build
// record's image-config hash is not the hash of what it resolves to now:
// typically a profile's base or image settings changed.
func imageConfigChecks(ctx context.Context, rb *blobx.Bucket, slug string, cfg *config.Config, res *config.Resolution) []doctorCheck {
	var out []doctorCheck
	for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
		if cfg.Workflows[name].Dockerfile != "" {
			continue // its hash covers the Dockerfile's blob, which needs a tree
		}
		data, _, err := rb.Read(ctx, imagecheck.RecordKey(slug, name))
		if err != nil {
			continue
		}
		rec, err := imagecheck.ParseRecord(data)
		if err != nil {
			continue
		}
		want, err := imagecheck.ImageConfigHash(cfg, name, nil)
		if err != nil || rec.ImageConfigHash == want {
			continue
		}
		from := res.SourceOf("workflows." + name + ".base")
		if s := res.SourceOf("workflows." + name + ".image"); s != config.SourceDefault {
			from = s
		}
		out = append(out, doctorCheck{ID: "image-config-" + name, Severity: "warning",
			Problem: fmt.Sprintf("workflow %s's job image was built from other image settings than it resolves to now (its base and image settings come from %s)", name, from),
			Fix:     "the daily image check rebuilds it; to do it now: fugaro image build --workflow " + name})
	}
	return out
}
```

In `internal/cli/doctor.go`'s checkout block, change

```go
		if c, fy := fugaroYAMLCheck(ctx, co.Root, lc); c != nil {
			o.Checks = append(o.Checks, *c)
			o.FugaroYAML = fy
		}
```

to

```go
		if c, fy := fugaroYAMLCheck(ctx, co.Root, lc); c != nil {
			o.Checks = append(o.Checks, *c)
			o.FugaroYAML = fy
			if fy.Valid {
				o.Checks = append(o.Checks, doctorLayerChecks(ctx, lc, co.Root)...)
			}
		}
```

- [ ] **Step 4: Run it and see it pass**

Run: `go test -race ./internal/cli/ -run 'TestDoctor'`
Expected: `ok`. In the existing doctor tests no layer applies, so they gain only the `project-layer` info line where a checkout's `fugaro.yaml` is valid. A test that counts checks exactly is updated to expect it; `go test -run TestDoctor` names any.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/doctor.go internal/cli/doctor_layer.go internal/cli/doctor_layer_test.go
git commit -m "layered config task 16: doctor shows the project layer and flags drift, stale copies and stale images"
```

### Task 17: `ls` and `diagnose` show the layer a run used

**Files:**
- Modify: `internal/runview/runview.go` (`Row`, `Join`), `internal/cli/ls.go` (`printRows`), `internal/cli/diagnose.go` (`printDiagnosis`)
- Test: `internal/runview/runview_test.go`, `internal/cli/ls_layer_test.go` (new)

**Interfaces:**
- Consumes: `runstore.Record.ProjectLayer`, `ConfigSHA256` (Task 7).
- Produces:
  ```go
  // runview.Row gains:
  ProjectLayer *runstore.ProjectLayerRecord `json:"project_layer,omitempty"`
  ConfigSHA256 string                       `json:"config_sha256,omitempty"`
  func layerCell(r runview.Row) string // cli: "gen 3", "gen 3 (not applied)", "-"
  ```

- [ ] **Step 1: Write the failing tests**

Append to `internal/runview/runview_test.go`:

```go
func TestJoinCarriesTheProjectLayer(t *testing.T) {
	r := rec(runstore.StatusSucceeded, nil)
	r.ProjectLayer = &runstore.ProjectLayerRecord{SHA256: strings.Repeat("a", 64), Generation: 3, Applied: true}
	r.ConfigSHA256 = strings.Repeat("b", 64)
	row := Join(Input{Task: spec, Launch: launch, Record: r, Exec: exec(backend.StateSucceeded)}, prices, now)
	if row.ProjectLayer == nil || row.ProjectLayer.Generation != 3 || row.ConfigSHA256 != r.ConfigSHA256 {
		t.Fatalf("row %+v", row)
	}
}
```

Create `internal/cli/ls_layer_test.go`:

```go
package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/dimipaun/fugaro/internal/runstore"
	"github.com/dimipaun/fugaro/internal/runview"
)

func TestLsShowsTheLayerColumnOnlyWhenARunHasOne(t *testing.T) {
	rows := []runview.Row{{Run: "s/20261008-100000-abcd", RunID: "20261008-100000-abcd", Status: "succeeded"}}
	var b bytes.Buffer
	if err := printRows(&b, "aurora", rows, nil, time.Now(), false); err != nil || strings.Contains(b.String(), "LAYER") {
		t.Fatalf("no layer: %q %v", b.String(), err)
	}
	rows[0].ProjectLayer = &runstore.ProjectLayerRecord{SHA256: strings.Repeat("a", 64), Generation: 3, Applied: true}
	rows = append(rows, runview.Row{Run: "s/20261008-110000-abcd", RunID: "20261008-110000-abcd", Status: "succeeded",
		ProjectLayer: &runstore.ProjectLayerRecord{SHA256: strings.Repeat("a", 64), Generation: 3}})
	b.Reset()
	if err := printRows(&b, "aurora", rows, nil, time.Now(), false); err != nil || !strings.Contains(b.String(), "LAYER") ||
		!strings.Contains(b.String(), "gen 3") || !strings.Contains(b.String(), "gen 3 (not applied)") {
		t.Fatalf("with a layer: %q %v", b.String(), err)
	}
}

func TestDiagnosePrintsTheConfigLine(t *testing.T) {
	d := &Diagnosis{Row: runview.Row{Run: "s/20261008-100000-abcd", Status: "succeeded",
		ProjectLayer: &runstore.ProjectLayerRecord{SHA256: strings.Repeat("a", 64), Generation: 3, Applied: true}, ConfigSHA256: strings.Repeat("b", 64)}}
	var b bytes.Buffer
	if err := printDiagnosis(&b, d, false); err != nil || !strings.Contains(b.String(), "Config:   project layer generation 3 (sha256 aaaaaaaaaaaa); resolved sha256 bbbbbbbbbbbb") {
		t.Fatalf("%q %v", b.String(), err)
	}
}
```

- [ ] **Step 2: Run them and see them fail**

Run: `go test ./internal/runview/ -run TestJoinCarriesTheProjectLayer; go test ./internal/cli/ -run 'TestLsShowsTheLayerColumn|TestDiagnosePrintsTheConfigLine'`
Expected: FAIL to compile: `row.ProjectLayer undefined (type Row has no field or method ProjectLayer)`.

- [ ] **Step 3: Implement**

In `internal/runview/runview.go`, add to `Row` after `Recipe`:

```go
	// ProjectLayer is the project layer the run's task carried, and
	// ConfigSHA256 its resolved config's sum (docs/design/layered-config.md
	// §8); both absent before 0.6.0 and for a run that has not started.
	ProjectLayer *runstore.ProjectLayerRecord `json:"project_layer,omitempty"`
	ConfigSHA256 string                       `json:"config_sha256,omitempty"`
```

In `Join`, inside the existing `if r != nil {` block, add as its first lines:

```go
		row.ProjectLayer, row.ConfigSHA256 = r.ProjectLayer, r.ConfigSHA256
```

In `internal/cli/ls.go`'s `printRows`, next to `showRecipe`:

```go
	showLayer := slices.ContainsFunc(rows, func(r runview.Row) bool { return r.ProjectLayer != nil })
```

After the RECIPE header part, add `if showLayer { header += "\tLAYER" }`. After the recipe cell, add `if showLayer { line += "\t" + layerCell(r) }`. Add:

```go
// layerCell is a run's project layer in ls: its generation, "-" for none.
func layerCell(r runview.Row) string {
	pl := r.ProjectLayer
	switch {
	case pl == nil:
		return "-"
	case !pl.Applied:
		return fmt.Sprintf("gen %d (not applied)", pl.Generation)
	}
	return fmt.Sprintf("gen %d", pl.Generation)
}
```

In `internal/cli/diagnose.go`'s `printDiagnosis`, after the `Reason:` line:

```go
	if pl := r.ProjectLayer; pl != nil {
		line := fmt.Sprintf("Config:   project layer generation %d (sha256 %s)", pl.Generation, shortSHA(pl.SHA256))
		if !pl.Applied {
			line += ", not applied"
		}
		if r.ConfigSHA256 != "" {
			line += "; resolved sha256 " + shortSHA(r.ConfigSHA256)
		}
		fmt.Fprintf(&b, "%s\n", oneLine(line))
	} else if r.ConfigSHA256 != "" {
		fmt.Fprintf(&b, "Config:   no project layer; resolved sha256 %s\n", oneLine(shortSHA(r.ConfigSHA256)))
	}
```

- [ ] **Step 4: Run them and see them pass**

Run: `go test -race ./internal/runview/ && go test -race ./internal/cli/ -run 'TestLs|TestDiagnose'`
Expected: `ok` for both.

- [ ] **Step 5: Commit**

```bash
git add internal/runview/runview.go internal/runview/runview_test.go internal/cli/ls.go internal/cli/diagnose.go internal/cli/ls_layer_test.go
git commit -m "layered config task 17: ls and diagnose show the project layer a run used"
```

PR 5 ends with one full `go test -race ./internal/cli/... ./internal/runview/...` and a PR titled "layered config 5/6: visibility".

---

## PR 6: docs and the setup skill

### Task 18: The user doc, cross-references and the scope-table docs test

**Files:**
- Create: `docs/project-layer.md`, `internal/config/docs_scope_test.go`
- Modify: `docs/design/shared-config.md`, `docs/recipes.md`, `docs/gcp-setup.md`, `internal/config/example.yaml`, `README.md`

**Interfaces:**
- Consumes: `config.Scopes`, `Scope.String` (Task 1).
- Produces: `docs/project-layer.md`, with a scope table whose rows equal `config.Scopes`.

- [ ] **Step 1: Write the failing test**

Create `internal/config/docs_scope_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestScopeTableMatchesDocs keeps docs/project-layer.md's scope table equal
// to Scopes: every row "| `key` | layers |", in order, and nothing else
// between the table's markers.
func TestScopeTableMatchesDocs(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "project-layer.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	start, end := strings.Index(doc, "<!-- scope-table:start -->"), strings.Index(doc, "<!-- scope-table:end -->")
	if start < 0 || end < start {
		t.Fatal("docs/project-layer.md has no scope-table markers")
	}
	rowRE := regexp.MustCompile("(?m)^\\| `([^`]+)` \\| ([a-z, ]+) \\|")
	var got []string
	for _, m := range rowRE.FindAllStringSubmatch(doc[start:end], -1) {
		got = append(got, m[1]+" = "+m[2])
	}
	var want []string
	for _, r := range Scopes {
		want = append(want, r.Key+" = "+r.In.String())
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("docs/project-layer.md's scope table differs from config.Scopes:\ngot:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
```

- [ ] **Step 2: Run it and see it fail**

Run: `go test ./internal/config/ -run TestScopeTableMatchesDocs`
Expected: FAIL: `open ../../docs/project-layer.md: no such file or directory`.

- [ ] **Step 3: Write the docs**

Create `docs/project-layer.md` with these sections, in this order.

1. **"What it is".** Write two paragraphs:
   - The project layer is one object per project, `fugaro/project-layer.yaml` in the runs bucket, holding project-wide `defaults:` and named workflow `profiles:` with a `default_profile`.
   - It applies to every repository whose `fugaro.yaml` names the project and its `gcp_project:`, and to no other. A project without one, and a repository without the line, behave as before 0.6.0.
2. **"The one rule".** For each key, the narrowest layer that sets it wins: per-task flag, then repository, then the workflow's profile, then project defaults, then Fugaro default. The budget ceiling is the owner's, outside every layer, and only tightens. Add the sentence: "A workflow takes a profile only when it names one (`workflows.<name>.profile`), or when the file has no `workflows:` at all (then one workflow, `default`, takes `profile:` or the project's `default_profile`). A workflow that names no profile takes nothing from any profile."
3. **"The minimal fugaro.yaml".** Give the three-line example from the design (§6), the three cases that need more (`git.base_branch` when not `main`, `profile:` when the project has no `default_profile`, `git.provider` when the project does not set it), and `fugaro config init --yes`.
4. **"Overriding a profile".** Give the design §6 example (`workflows.api.profile` with one `commands.test` and a repo-only `secrets`). Then the merge table of design §4, verbatim: maps merge, scalars and lists replace, `null` is not set, `[]` clears, `dockerfile:` drops the profile's `image:`.
5. **"Where each key may be set".** An intro line ("Each key may be set only in the layers listed; the project layer refuses the rest, naming the key"), then exactly this block. These are the rows of `config.Scopes`, in its order, as `TestScopeTableMatchesDocs` checks; this block was generated from the Task 1 code:

   ```markdown
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
   ```

   Below the table, write one paragraph on what is outside it. Secrets' values are in Secret Manager and never in any layer. Per-person Claude tokens are a later project. The individual layer is flags and env only.
6. **"The project layer file".** Give the design §6 example. List the reserved keys (`environments`, which arrives with the single base image; and `extends`), the 64 KiB limit, the strict shape rules, and that credential-shaped values are refused.
7. **"Commands".** Give the table of design §7: `config show`, `config layer`, `config publish`, `config init`, `validate --project-layer|--offline`. Add `fugaro run`'s two lines (`project layer: …` and `commands: from profile …`), and `doctor`'s four checks.
8. **"Publishing safely".** State that only an operator's own terminal can publish: it is refused in an agent session. Then describe `--executable-changes` and the banner, the generation precondition, the per-repository copies (`builds/<slug>/project-layer.yaml`) and what `doctor` says when one is stale. End with the threat model in four bullets: who can write, what a hostile layer could do, what bounds it, and the recommended IAM narrowing as future work.
9. **"Rollout (0.6.0)".** Give the five-step order of design §10, verbatim, and what an older CLI does (refuses a minimal file; runs a full file without the defaults).
10. **"Not yet".** List the image catalog (link the design's Phase 2), `extends`, `config extract` and per-person tokens.

Cross-references, one sentence each:
- `docs/design/shared-config.md`, at the end of §1's non-goals: "Repository settings shared by a project live in the project layer: see [layered-config.md](layered-config.md)."
- `docs/recipes.md`, at the end of the project-recipes section: "Repository defaults and workflow profiles use the same publishing model: see [project-layer.md](project-layer.md)."
- `docs/gcp-setup.md`, in the per-repository onboarding steps: "If the project publishes a project layer, `fugaro config init --yes` writes the minimal `fugaro.yaml`; publish or update the layer with `fugaro config publish FILE` (after every teammate's CLI and every job image is on 0.6.0: see [project-layer.md](project-layer.md#rollout-060))."
- `internal/config/example.yaml`, as a comment line under the header (keep `TestExampleIsValid` green: comments only): `# If your project publishes a project layer, three lines are enough: version, project and gcp_project (fugaro config init). See docs/project-layer.md.`
- `README.md`, in the configuration section: one line linking `docs/project-layer.md`.

- [ ] **Step 4: Run it and see it pass**

Run: `go test -race ./internal/config/ && go test -race ./plugin/ ./schemas/`
Expected: `ok` for all, including `TestExampleIsValid` and the plugin docs lint.

- [ ] **Step 5: Commit**

```bash
git add docs/project-layer.md internal/config/docs_scope_test.go docs/design/shared-config.md docs/recipes.md docs/gcp-setup.md internal/config/example.yaml README.md
git commit -m "layered config task 18: docs/project-layer.md, cross-references, the scope table pinned by a test"
```

### Task 19: The setup skill writes the minimal file when the project has a layer

**Files:**
- Modify: `plugin/skills/setup/SKILL.md`, `plugin/skills/setup/reference/decisions.md`
- Test: `plugin/setup_skill_test.go`

**Interfaces:**
- Consumes: `fugaro config layer --json`, `fugaro config init --yes`, `fugaro config show --json`, `fugaro validate --json` (Tasks 10 and 12).
- Produces: a first step in the skill: check for a project layer before investigating.

- [ ] **Step 1: Write the failing test**

Append to `plugin/setup_skill_test.go` (it already imports `strings` and `testing`, and has `setupFiles`):

```go
func TestSetupSkillUsesTheProjectLayer(t *testing.T) {
	files := setupFiles(t)
	skill := files["skills/setup/SKILL.md"]
	for _, want := range []string{
		"fugaro config layer --json",
		"fugaro config init --yes",
		"fugaro config show --json",
		"the investigative path",
	} {
		if !strings.Contains(skill, want) {
			t.Errorf("SKILL.md does not mention %q", want)
		}
	}
	// The layer check comes after doctor (TestSetupSkillMentionsDoctorFirst
	// keeps doctor first).
	if strings.Index(skill, "fugaro doctor --json") > strings.Index(skill, "fugaro config layer --json") {
		t.Error("SKILL.md checks the project layer before doctor")
	}
	if !strings.Contains(files["skills/setup/reference/decisions.md"], "## Minimal file or full file?") {
		t.Error("decisions.md has no section on the minimal file")
	}
}
```

- [ ] **Step 2: Run it and see it fail**

Run: `go test ./plugin/ -run TestSetupSkillUsesTheProjectLayer`
Expected: FAIL: `SKILL.md does not mention "fugaro config layer --json"` (and the other three), and the ordering and decisions.md errors.

- [ ] **Step 3: Implement**

In `plugin/skills/setup/SKILL.md`, insert a new step right after the `fugaro doctor --json` step and before the repository investigation. `TestSetupSkillMentionsDoctorFirst` requires doctor to stay the first command. Title it "Does the project publish a project layer?" and give it this text:

> Run `fugaro config layer --json` in the checkout, after `gcp_project:` is known (ask the user for the Fugaro project and its GCP project if `fugaro.yaml` does not exist yet).
>
> **If it prints a layer,** tell the user which profiles it offers (`profiles`) and which is the default. Ask only whether the repository fits a profile. If it does:
> 1. Write the minimal file with `fugaro config init --yes` (add `--profile NAME` or `--base-branch BRANCH` when needed).
> 2. Run `fugaro config show --json`, and confirm with the user the commands and image it resolved to.
> 3. Run `fugaro validate --json` and `fugaro image build --local --json`, as for any file.
>
> Override a single field under `workflows.default` only for a real quirk.
>
> **If no layer applies, or the repository fits no profile,** follow the investigative path below, unchanged.

In `plugin/skills/setup/reference/decisions.md`, append at the end (after the numbered sections, whose order `TestSetupSkillAsksRecipe` checks) a section `## Minimal file or full file?` with:

> **Minimal file or full file?** With a project layer and a fitting profile, the minimal file keeps the repository on the project's settings: a profile change reaches it without a pull request. A full file is for a repository whose build or image is its own. Never copy a profile's values into `fugaro.yaml`; that would stop it from following the profile.

- [ ] **Step 4: Run it and see it pass**

Run: `go test -race ./plugin/`
Expected: `ok`, including the skills lint, which checks that every command and flag named is real (Tasks 10 and 12 created them).

- [ ] **Step 5: Commit**

```bash
git add plugin/skills/setup/SKILL.md plugin/skills/setup/reference/decisions.md plugin/setup_skill_test.go
git commit -m "layered config task 19: the setup skill writes the minimal fugaro.yaml when the project has a layer"
```

PR 6 ends with one full `go test -race ./plugin/... ./internal/config/... ./schemas/...` and a PR titled "layered config 6/6: docs and the setup skill".

---

## Release

### Task 20: `docs/releases/v0.6.0.md` and `/new-release 0.6.0`

**Files:**
- Create: `docs/releases/v0.6.0.md`

- [ ] **Step 1: Write the Highlights**

Write `docs/releases/v0.6.0.md` in the shape of `docs/releases/v0.5.1.md`. Include:
- **The feature.** The project layer, profiles, the minimal `fugaro.yaml`, `config show`, `config publish`, `config init`, `validate --project-layer|--offline`, and the `doctor`, `ls` and `diagnose` lines.
- **The rollout order** of design §10, as a numbered list with the commands.
- **The caveat,** in bold: every CLI, CI pin and job image must be on 0.6.0 before a minimal or profile-using `fugaro.yaml` is merged. Older ones refuse it.
- **The note on executable keys** and `--executable-changes`.
- **The sequencing note:** profiles that name `go`, `web-node` or `java-services` change in one place when the single base image replaces them.

- [ ] **Step 2: Merge the Highlights, then release**

Merge the Highlights through a PR (every CI check read). Then run `/new-release 0.6.0`, which runs `scripts/release.sh` and verifies the published release. Do not publish a project layer in any live installation as part of this task.

- [ ] **Step 3: Commit**

```bash
git add docs/releases/v0.6.0.md
git commit -m "release: v0.6.0 Highlights"
```

---

## Phase 2: the image catalog (BLOCKED on the base image consolidation release)

These tasks are not in any Phase 1 PR group. Each one is written as a full TDD task, with failing test, code and runs, once the consolidation design is approved. Their interfaces depend on what that design fixes:
- the single base's name (assumed `fugaro` below);
- its unprivileged user and the `mise install` location;
- its pinning;
- the migration document the legacy-kind refusal names.

Design §9 is the specification.

- **P2-1. `environments:` in the project layer.**
  - The `Environment` type is `{Tools map[string]string; Apt, Setup []string; Image string}`.
  - Remove `environments` from `layerReserved`.
  - Validate tool names against mise's registry shape, values as version strings, and `Image` with `baseImageRE(registryHost)`, inside the anchor's `fugaro-base`, with an optional digest.
  - Refuse a name equal to the single base's.
  - Files: `internal/config/layer.go` and `schemas/project-layer.schema.json`, plus the corpora.
- **P2-2. `base:` resolves through the catalog.**
  - `Bases` becomes the single base. The closed enum in `Validate` and in `schemas/fugaro.schema.json` becomes a name pattern plus a resolution check in `Resolve`.
  - The legacy kinds are refused with `Code: "legacy_base"` and the consolidation's migration message.
  - `Workflow` gains a resolved `Env *Environment` (`yaml:"-"`).
  - Files: `internal/config/{config,validate,resolve}.go` and `schemas/fugaro.schema.json`.
- **P2-3. The image template installs the tool set.**
  - With no `mise.toml` or `.tool-versions` in the repository, the generated Dockerfile writes the environment's `tools` to a mise config and runs `mise install` as the image's user.
  - A repository file wins wholesale.
  - `config show` reports the resolved tool versions and their source.
  - Files: `internal/image/*` and `images/derived/Dockerfile.tmpl`.
- **P2-4. `ImageConfigHash` covers the resolved environment** (tools, apt, setup, image reference), so a catalog edit rebuilds at the next daily check. Files: `internal/imagecheck/record.go`, with salt-safe tests for unchanged images.
- **P2-5. `base_images`, `image refresh` and the check job's `BaseImages` key on the single base.**
  - Operator-built environments are skipped by refresh with a note.
  - The base trigger watches an operator-built reference's digest.
  - Files: `internal/cli/image_refresh*.go`, `internal/infra/checkjob.go` and `internal/cli/imagecheck.go`.
- **P2-6. Docs.** Cover `docs/project-layer.md`'s catalog section, the steps to add an operator-built environment (write a Dockerfile `FROM` the single base; build and push to `<region>-docker.pkg.dev/<gcp>/fugaro-base/<name>:<tag>` for both architectures; pin the digest; publish), and the setup skill's profile choice by environment.

---

## Self-review (done before review)

- **Coverage of the design.**

  | Design section | Tasks |
  |---|---|
  | §3 (layers, anchoring) | 4, 9, 11 |
  | §4 (`Resolve`, merge rules, errors naming the layer) | 4 |
  | §5 (scopes, credentials) | 1, 2, 18 |
  | §6 (profiles, minimal file) | 2, 4, 12 |
  | §7 (object, publish, read on every launch, commands) | 8, 9, 12, 13 |
  | §8 (embedding, copies, Cloud Build, check job, sums) | 6, 7, 14, 15 |
  | §9 (Phase 2) | P2-1 to P2-6, blocked; 0.6.0 reserves `environments` (Task 2) |
  | §10 (rollout, `layeredSince`, validate warning) | 10, 11, 13, 14, 18, 20 |
  | §11 (security) | 2, 13; the IAM narrowing is a non-goal |
  | §12 (adoption hooks) | 12, 19 |
  | §14 (tests) | every task |
  | §15 (docs) | 18, 19, 20 |
- **Placeholders.** None in Tasks 1 to 19: every step has its code. Task 18 gives the doc's sections as instructions and its scope table verbatim (generated from Task 1's `Scopes` and checked by the test). Task 20 is prose by nature. Phase 2 is deliberately unwritten until its inputs exist, and says so.
- **Type consistency.** These names are used identically in every task that consumes them:
  - `config.ProjectLayer`, `LayerAnchor`, `Resolution`, `Config.Layer`, `LayerKey`, `LayerCopyKey`, `ImplicitWorkflow`, `CodeNeedsLayer`, `SourceProfile`;
  - `task.ProjectLayer{SHA256, Generation, YAML}` and `runstore.ProjectLayerRecord{SHA256, Generation, Applied}`;
  - `layerOptions{File, Offline, Data, Where, NoBucket}`, `foundLayer` and `resolvedFile`;
  - `layeredSince`.

  `fugaroYAMLCheck` and `readHeadConfig` change signature in Tasks 10 and 15, and every caller is listed in those tasks.
- **Ordering hazards.**
  - Task 10 uses `layeredSince`, which Task 11 defines; Task 10 says how to proceed if it lands first.
  - Task 14 uses `writeLayerCopy` from Task 13; Task 14 says how to proceed if it lands first.
  - PR 2 (the runner) merges before any CLI that embeds a layer (PR 3).
