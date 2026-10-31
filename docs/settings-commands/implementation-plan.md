# `fugaro settings`: implementation plan

**The design is approved; the tasks queue for CLI 0.15.0.** SD1–SD18 were answered by the owner on
2026-10-31 ([reconciliation.md](reconciliation.md) §3) and D840–D854 are this plan's recommendations
(§4), **D854 still open** — a stale local copy overwriting a newer publish, which S3 carries if the
owner accepts it. A decision answered the other way changes the tasks that cite it. Each task touches
at most 12 files, names its tests, starts with a red step, and ends at the gates below. **The first
slice is S0–S3.**

## Order, and what this plan does not own

- **External dependency E1 — the `fugaro update` policy-publish stage.** It is the `models` design's
  E1 (`docs/models-commands/reconciliation.md` F8) and belongs to the update-converge work. **S3 is
  that stage**: if E1 lands first, S3 is deleted and the read simply stops saying the policy is
  unpublished. **S5 may merge before it but must not ship in a release without it**, or the owner's
  next step after `settings set` is `fugaro project policy publish` — the second command the HARD
  RULE forbids.
- **External dependency E2 — the project layer's local file and its update stage.** PR #1463's R0
  (the file has a path) and R1 (the `Layer` stage publishes it). **S4 is only the read side** (the
  local file's path, and the `unpublished` state) and writes no stage; a second layer stage would be
  two writers for one object.
- **External dependency E3 — the shared comment-preserving editor, `internal/yamledit`.** One package
  of generic locate-and-splice operations (set a scalar, remove a key, add or remove a list item,
  upsert or remove a sequence item by key) used for the local config, the local layer file and
  `fugaro.yaml`. **Its owning task is the editor task, and it lands first in the joint order below**;
  S5–S9 call it, and **no task here writes a second editor** (D847).
- **Files other work owns:** registering the commands in `internal/cli/root.go`, the generated
  `website/content/cli-reference.md`, `plugin/**`, `console/**` and `docs/releases/**` are each
  another work stream's at the time of writing. S1, S10 and every task that adds a command name them,
  and the task that touches them waits for the owner's go for those files.
- **The `docs/settings-commands/` folder itself** is this design. No task here rewrites
  `audit.md`; the audit is the evidence and stays as it is.

| Level | Tasks | Needs |
| --- | --- | --- |
| 0 | S0 | nothing |
| 1 | S1, S3 | S0 (S1) |
| 2 | S2, S4, S5 | S1 (S2, S5); S3 (S4); E3 (S5); R1 (S5) |
| 3 | S6, S7, S8, S9 | S5, and S3 for S6, S7, S8 and S9 |
| 4 | S10 | S1–S9; E1 and E2 merged before the release |

**The joint order**, agreed with the `models` and `rigor` designs and the same list in all three:
**editor → policy stage (S3) → settings read (S0–S2, S4) → layer stage (R0, R1) → set (S5) → rigor
(R2–R5) and models.**

| # | What lands | Owner | Why it sits here |
| --- | --- | --- | --- |
| 1 | The one editor, `internal/yamledit` | the editor task (E3) | every later task splices a file with it, so landing it first is what keeps anyone from writing a second one meanwhile |
| 2 | `update` publishes the policy node | **S3**, the `models` design's E1 | it is the first slice's second half, it is what lets the read stop saying `unpublished` for the policy, and S6–S9 write keys only this stage publishes |
| 3 | The settings read | **S0–S2, S4** | useful before every writer, and it is what makes G1, G2 and G5 visible today |
| 4 | The layer stage, and the local layer file it publishes | the `rigor` design's **R0, R1** | `settings set --layer project` (S5) needs both the file and its publisher |
| 5 | `fugaro settings get\|set\|unset` | **S5** | the first writer over all three documents; it must not ship before 2 and 4, or its `next: fugaro update` line is false |
| 6 | `fugaro rigor …` (R2–R5) and the `models` writes | the `rigor` and `models` designs | they reuse the editor and both stages, and their keys reach runs through the stage of 2 |

## Gates (every task)

`go build ./... && gofmt -l . && go vet ./...` (gofmt prints nothing); the task's named tests with
`GOFLAGS=-p=2 go test ./internal/<pkg>/ -run '<names>' -count=1`; `GOFLAGS=-p=2 go test ./internal/cli/
-run Docs -count=1 -timeout 40m` when a doc changed; `go test ./internal/eeboundary/... -count=1`.
Never the whole `internal/cli` package.

## S0. The key and defaults registry (SD16, D840, D841)

Without it the DEFAULT column, `--changed` and the schema `default` keywords have no source but a
fourth hand-kept copy (F12).

- **Files (5):** `internal/settings/registry.go` (new), `internal/settings/registry_test.go` (new),
  `internal/settings/defaults_test.go` (new), `internal/config/defaults.go` (`applyDefaults` reads
  its values from the registry instead of restating them), `internal/config/defaults_test.go`.
- **Interface:** `settings.Row{Key, Layers, Merge, Default, Sensitive, Owner, Doc}`, `settings.Rows()`,
  `settings.Lookup(key) (Row, bool)`, `settings.DefaultOf(key) (value string, source string)`. The
  `fugaro.yaml` rows are built by reading `config.Scopes`, so the two cannot drift.
- **Red step:** `TestRegistryCoversEveryConfigField` fails while the registry does not exist, and
  `TestApplyDefaultsUsesTheRegistry` is shown red against the literals it replaces (the reason the
  task exists: `"main"` is written twice today, in `applyDefaults` and in `config.DefaultBaseBranch`).
- **Tests:** `TestRegistryCoversEveryConfigField` (every `fugaro.yaml`, project-layer and
  local-config field has a row), `TestRegistryFugaroYAMLKeysMatchConfigScopes` (row for row with
  `config.Scopes`), `TestRegistryKeysAreUnique`, `TestRegistryDefaultsMatchApplyDefaults`,
  `TestRegistryOwnerNamesACommand`, `TestRegistryMarksEverySensitiveKey`.
- **Hermetic:** pure Go, no filesystem.

## S1. `fugaro settings`, the read over the offline layers (SD1, D839, D849, D851, D852)

- **Files (9):** `internal/cli/settings.go` (new: the command group), `internal/cli/settings_read.go`
  (new: one row per key and scope, from the registry and the offline readers), `internal/cli/settings_read_test.go`
  (new), `internal/cli/settings_render.go` (new: the table, the state set, the redaction),
  `internal/cli/settings_render_test.go` (new), `schemas/settings.schema.json` (new),
  `schemas/schemas_test.go` (the new schema loads and the command's output validates),
  `internal/cli/root.go` (registration; owned elsewhere, see "Order"), `internal/settings/registry.go`
  (only if a row the read prints is missing).
- **Interface:** `fugaro settings [KEY|GLOB ...] [--repo R | --all-repos] [--layer ...] [--grep RE]
  [--changed] [--why] [--offline] [--json]`, columns `KEY VALUE WINS DEFAULT STATE` (spec §2).
- **Red step:** `TestSettingsPrintsKeyValueWinsDefaultState` fails while the command does not exist.
- **Tests:** `TestSettingsPrintsKeyValueWinsDefaultState`,
  `TestSettingsShowsTheDefaultWhenNoLayerSetsIt`, `TestSettingsChangedShowsOnlyNonDefaults`,
  `TestSettingsUnknownKeyNamesTheNearest`, `TestSettingsGetOnAKeySetNowhereIsTheDefaultRowAndExitZero`,
  `TestSettingsNeverPrintsASensitiveValue`, `TestSettingsRepoLayerReadsTheRememberedCheckout`,
  `TestSettingsAllReposShowsUnknownWithoutACheckout`, `TestSettingsGrepFiltersRows`,
  `TestSettingsJSONMatchesSchema`, `TestSettingsWritesNothing`.
- **Hermetic:** `t.TempDir()` config home, a fake checkout made with `git init` in the temp dir, a
  seeded layer cache, `internal/testutil`.

## S2. The cloud layers, `unpublished`, and `--why` (SD3, D849, D851)

The `unpublished` state is the whole point of the read: it is what says a local edit will reach runs
on the next `update` (G1, G2).

- **Files (6):** `internal/cli/settings_cloud.go` (new: the policy node and the Realtime Database
  caps and kills, read through the readers that already exist), `internal/cli/settings_cloud_test.go`
  (new), `internal/cli/settings_read.go`, `internal/cli/settings_render.go`,
  `internal/cli/settings_read_test.go`, `internal/settings/registry.go` (the cloud rows).
- **Red step:** `TestSettingsMarksAnUnpublishedLocalValue` fails while no cloud layer is read.
- **Tests:** `TestSettingsMarksAnUnpublishedLocalValue`, `TestSettingsUnreadIsNeverADefault` (no
  budget backend and no permission both render `unread: <reason>`),
  `TestSettingsOfflineLeavesTheTableShape`, `TestSettingsWhyShowsTheIgnoredLooserValue` (a clamped
  `budget.per_run_usd` prints the effective value and the ignored one), `TestSettingsCapsRowsComeFromTheBudgetReader`,
  `TestSettingsCloudLayersAreBoundedInTime` (a source that never answers is `unread`, not a hang).
- **Hermetic:** `internal/gcpfake` for the runs bucket, a fake Realtime Database reader behind the
  existing seam, `t.TempDir()`. No cloud call.

## S3. `fugaro update` publishes the policy node, and `project policy publish` is refused to an agent (SD3, SD8). **SEC**

The stage writes the node every run of the project is admitted by, and it decides what a run may
spend. It is E1, and it is the first slice's second half.

- **Files (7):** `internal/cli/init_policy_publish.go` (new: build from the local config with
  `buildProjectPolicy`, the diff, the confirmation, the ETag write), `internal/cli/init_policy_publish_test.go`
  (new), `internal/cli/init_stages.go` (the publish becomes part of the `Policy` stage's work),
  `internal/cli/init_policy_migrate.go` (the shared build-and-write path, so the stage and the
  explicit door cannot drift), `internal/cli/project_cmd.go` (the agent refusal `project policy
  publish` lacks today, SD8), `internal/cli/project_cmd_test.go`, `docs/policy-migration.md` (§ on
  who publishes, and the one command).
- **Red step:** `TestUpdatePublishesThePolicyNodeWhenItDrifts` fails while no stage builds the node
  from the local config.
- **Tests:** `TestUpdatePublishesThePolicyNodeWhenItDrifts`, `TestUpdatePolicyStageWritesNothingWhenTheNodeMatches`,
  `TestUpdatePolicyStageAppliesNothingInAnAgentSessionEvenWithYes`,
  `TestInstallationAtThePreviousVersionConverges` (an installation whose node predates this release
  ends the update with it published — the HARD RULE's own test),
  `TestProjectPolicyPublishIsRefusedInAnAgentSession`, `TestProjectPolicyPublishKeepsDeleteAndSeedRepos`.
- **Hermetic:** `internal/gcpfake`, a fake Realtime Database, `t.TempDir()`. No apply, no deploy, no
  cloud call.
- **Its own `### For operators`, in this task's release notes** — the first slice's step, and S10's
  entry does not repeat it: "`fugaro update` now publishes the policy node from the local config;
  `project policy publish` is refused in a coding agent's session".
- **If D854 is answered yes** (it is open): the same task records the generation the local file was
  seeded from or last published at and refuses to publish over a generation that has moved since,
  showing the three-way state and offering the re-seed —
  `TestUpdateRefusesToPublishOverANewerGeneration`. The layer stage (R1) carries the same guard.

## S4. The layer side of the read (SD4, D845)

The read side only: the layer's local file has a path — `~/.config/fugaro/projects/<project>-layer.yaml`,
resolved by `localcfg.LayerFile` — and a local file that differs from the published object is
`unpublished`. The stage itself is E2 and is not written here.

- **Files (4):** `internal/cli/settings_layer.go` (new: `localcfg.LayerFile`, the published object,
  the `unpublished` comparison), `internal/cli/settings_layer_test.go` (new),
  `internal/cli/settings_cloud.go`, `internal/settings/registry.go` (the `layer.*` rows).
- **Red step:** `TestSettingsMarksAPublishedLayerThatDriftsFromTheLocalFile` fails while the local
  layer file's path is not resolved.
- **Tests:** `TestSettingsMarksAPublishedLayerThatDriftsFromTheLocalFile`,
  `TestSettingsLayerRowsNameTheLocalFileAndBothShas`,
  `TestSettingsWithNoLocalLayerFileReadsThePublishedOneAndSaysSo`.
- **Hermetic:** `internal/gcpfake` for the published object, `t.TempDir()` for the file.

## S5. `fugaro settings get|set|unset` (SD2, SD9, D846, D847, D852, D853). **SEC**

It edits the local project config, the project layer file and a repository's `fugaro.yaml`: three
documents that decide where a repository's code goes and what a run may cost, and one of them
outside every repository, which is why the agent-session refusal is here.

- **Files (7):** `internal/cli/settings_set.go` (new), `internal/cli/settings_set_test.go` (new),
  `internal/cli/settings_write.go` (new: diff, confirmation, agent refusal, `backupFile`,
  `writeFileAtomic` — shared with S6–S9), `internal/cli/settings_write_test.go` (new),
  `internal/cli/settings.go`, `internal/yamledit/*.go` (only if an operation this task needs is
  missing from the shared editor — the editor task owns that package and lands it first) and
  `internal/yamledit/edit_test.go` (D847's cross-document test, same condition).
- **Red step:** `TestSettingsSetRefusesAKeyOwnedByAnotherFamily` fails while the subcommand does not
  exist.
- **Tests:** `TestSettingsSetRefusesAKeyOwnedByAnotherFamily`,
  `TestSettingsSetRefusesALayerTheKeyMayNotBeSetIn`, `TestSettingsSetRefusesAValueTheLoaderRefuses`,
  `TestSettingsSetRefusesInAnAgentSessionEvenWithYes`, `TestSettingsSetRepoLayerIsAllowedInAnAgentSession`,
  `TestSettingsSetLooseningACeilingAsksForTheTypedProjectName` (and `--yes` does not stand in),
  `TestSettingsSetRepoLayerRefusesALooseningInAnAgentSession` (spec §4.2 step 4: the typed-name
  carve-out — a ceiling loosened at `--layer repo` is refused in an agent session, `--yes` included),
  `TestSettingsSetBacksUpAndWritesAtomically`, `TestSettingsSetKeepsTheRestOfTheFile`,
  `TestSettingsAddAndRemoveOnAListKey`, `TestSettingsRefusesEmptyingAListKey`,
  `TestSettingsUnsetRemovesTheKeyRatherThanWritingTheDefault`,
  `TestSettingsUnsetOfNothingIsAnExitZeroSentence`, `TestSettingsSetPrintsOnlyFugaroUpdateNext`,
  `TestSettingsSetWritesNothingInTheCloud`, `TestTheThreeEditorsPreserveBytesTheSameWay` (D847's
  cross-document test).
- **Hermetic:** `t.TempDir()` config home, a fake checkout with `git init`, `internal/gcpfake` for the
  layer, fixture files with comments and unknown keys (placeholder names only).

## S6. `fugaro project credentials` and `fugaro project clone-url`, with the import (SD5, D842, D843, D844). **SEC**

The highest-risk gap (G3): the block decides whether a run spends the paid credential tiers, and
today the only writer is the Firebase console.

- **Files (10):** `internal/cli/project_credentials.go` (new), `internal/cli/project_credentials_test.go`
  (new), `internal/cli/project_cloneurl.go` (new), `internal/cli/project_cloneurl_test.go` (new),
  `internal/localcfg/localcfg.go` (the two blocks and their strict decoding, with the node's shapes),
  `internal/localcfg/localcfg_test.go`, `internal/cli/project_cmd.go` (`buildProjectPolicy` takes the
  two blocks from the local config, keeping the node's copy only until the import has run),
  `internal/cli/init_policy_publish.go` (the import), `internal/cli/init_policy_publish_test.go`,
  `internal/cli/project_cmd_test.go`.
- **Red step:** `TestProjectCredentialsSetWritesTheLocalBlock` fails while the block does not exist.
- **Tests:** `TestProjectCredentialsSetWritesTheLocalBlock`,
  `TestProjectCredentialsRefusesAnUnknownFallback`, `TestProjectCloneURLSetWritesTheLocalBlock`,
  `TestUpdateImportsTheNodesCredentialsIntoTheLocalFile` (and `clone_urls`),
  `TestUpdateImportDeclinedLeavesBothSidesUntouched`,
  `TestUpdateSeedsNothingWhenTheNodeCarriesNeitherBlock`,
  `TestBuildProjectPolicyPublishesTheLocalBlocksAndNotTheNodesCopy`,
  `TestProjectCredentialsRefusesInAnAgentSessionEvenWithYes`,
  `TestProjectCredentialsPrintsOnlyFugaroUpdateNext`.
- **Hermetic:** a fake Realtime Database carrying a `credentials` block and a `clone_urls` map,
  `t.TempDir()` config home.

## S7. `fugaro rigor floor` (SD10, G7). **SEC**

A floor, and its semantics are subtle: an absent key and a written `{}` mean different things at
publish.

- **Files (5):** `internal/cli/rigor_floor.go` (new), `internal/cli/rigor_floor_test.go` (new),
  `internal/cli/rigor.go` (**the group: whichever of the `rigor` design's R3 and this task lands
  first creates it, and the other uses it**), `internal/yamledit/*.go` (the `review_floor` operations,
  only if they are missing from the shared editor), `internal/cli/settings_write.go` (the shared write
  path).
- **Red step:** `TestRigorFloorSetWritesTheModeFloor` fails while the subcommand does not exist.
- **Tests:** `TestRigorFloorSetWritesTheModeFloor`,
  `TestRigorFloorSetLoweringAsksForTheTypedProjectName`, `TestRigorFloorUnsetRemovesTheModeEntry`,
  `TestRigorFloorUnsetOfNothingIsAnExitZeroSentence`, `TestRigorFloorRefusesAnUnknownLevel`,
  `TestRigorFloorRefusesInAnAgentSession`, `TestRigorFloorKeepsTheRestOfTheFile`.
- **Hermetic:** `t.TempDir()` config home, a fixture with the owner's comments.

## S8. `fugaro accounts`, `fugaro secrets allow`, and SD17. **SEC**

The allow-lists decide which repository may spend a credential, and SD17 removes the "copy this entry
to every operator machine" step.

- **Files (7):** `internal/cli/accounts_allow.go` (new), `internal/cli/accounts_allow_test.go` (new),
  `internal/cli/secrets_allow.go` (new), `internal/cli/secrets_allow_test.go` (new),
  `internal/yamledit/*.go` (the `allow_data_to` and `project_secrets` operations),
  `internal/cli/upgrade_cloud.go` (`update --repo` reads `project_secrets` from the node),
  `internal/cli/upgrade_cloud_test.go`.
- **Red step:** `TestAccountsAllowAppendsTheRepository` fails while the subcommand does not exist.
- **Tests:** `TestAccountsAllowAppendsTheRepository`, `TestAccountsDenyRemovesIt`,
  `TestAccountsRmRemovesTheAccount`, `TestSecretsAllowWritesThePatternList`,
  `TestSecretsAllowProxySetsTheFlag`, `TestUpdateRepoReadsTheAllowListFromTheNode`,
  `TestAccountsAllowRefusesInAnAgentSession`.
- **Hermetic:** a fake Realtime Database, `t.TempDir()` config home.

## S9. `fugaro project profile` and `fugaro project routing` (G6, G8). **SEC**

A routing mistake sends Claude calls down a provider route, which is a data-egress issue, and the
publisher refuses silence for the same reason.

- **Files (6):** `internal/cli/project_profile.go` (new), `internal/cli/project_profile_test.go`
  (new), `internal/cli/project_routing.go` (new), `internal/cli/project_routing_test.go` (new),
  `internal/yamledit/*.go` (the `profiles` and `routing` operations), `internal/cli/project_cmd.go`
  (the routing silence rules, shared with the publisher rather than restated).
- **Red step:** `TestProjectProfileAddWritesAJobProfile` fails while the subcommand does not exist.
- **Tests:** `TestProjectProfileAddWritesAJobProfile`, `TestProjectProfileSetChangesOneKey`,
  `TestProjectProfileRmRefusesWhileAWorkflowUsesIt`, `TestProjectRoutingSetWritesTheVia`,
  `TestProjectRoutingUnsetRemovesTheBlock`,
  `TestProjectRoutingRefusesSilenceThatTurnsTheCascadeOff`,
  `TestProjectProfileRefusesInAnAgentSession`.
- **Hermetic:** `t.TempDir()` config home, fixture files.

## S10. Documentation, skill and release notes

- **Files (7):** `docs/project-layer.md` (§5 and §7 lead with the commands; the hand edit stays as
  the reference), `docs/policy-migration.md` (who publishes the node), `docs/settings-commands/README.md`
  (the status line), `docs/releases/vX.Y.Z.md` (`### For operators`: "run `fugaro update`" and what
  it will ask — including the import of a node-only `credentials` or `clone_urls` block, and that
  declining it is safe; **the policy stage's own line is S3's, above, and is not repeated here**),
  `website/data/release-summaries.yaml`, the generated
  `website/content/cli-reference.md` (regenerated, not hand-edited), `plugin/skills/working/SKILL.md`
  (the settings command family; owned elsewhere, see "Order").
- **Red step:** the plugin's command test fails while a skill names `fugaro settings` before S1 is
  merged; it turns green on S1's branch.
- **Tests:** `GOFLAGS=-p=2 go test ./internal/cli/ -run Docs`, the plugin tests, `npm run check` in
  `website/`.

## Live checks

None. Every behaviour above is a local file edit, an offline read, or a stage of `fugaro update`
whose cloud writes go through the publish paths the installation's existing update checks already
exercise — the same `buildProjectPolicy`, `publishLayer` and `fanOutLayer` the explicit doors use,
with the same confirmation and the same ETag. No new operator step is added, so no check number is
taken.
