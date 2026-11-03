# `fugaro settings`: implementation plan

**The design is approved; the tasks queue for CLI 0.15.0 (and console 0.117.0).** SD1–SD18 were
answered by the owner on 2026-10-31 ([reconciliation.md](reconciliation.md) §3), and so were this
plan's own decisions (§4): **all of D840–D854 were answered that day, every one as recommended,
D854 — the stale local copy — included, its answer being the generation recorded**, which is what S3
builds. A decision answered the other way would change the tasks that cite it; none was. Each task
touches at most 12 files, names its tests, starts with a red step, and ends at the gates below. **The
first slice is S0–S3.**

## Order, and what this plan does not own

- **External dependency E1 — the `fugaro update` policy-publish stage.** It is the `models` design's
  E1 (`docs/models-commands/reconciliation.md` F8) and belongs to the update-converge work. **S3 is
  that stage**: if E1 lands first, S3 is deleted and the read simply stops saying the policy is
  unpublished.
- **External dependency E2 — the project layer's local file and its update stage.** PR #1463's R0
  (the file has a path) and R1 (the `Layer` stage publishes it). **S4 is only the read side** (the
  local file's path, and the `unpublished` state) and writes no stage; a second layer stage would be
  two writers for one object.
- **External dependency E3 — the shared comment-preserving editor, `internal/yamledit`.** One package
  of generic locate-and-splice operations (set a scalar, remove a key, add or remove a list item,
  upsert or remove a sequence item by key) used for the local config, the local layer file and
  `fugaro.yaml`. Its scope also carries **the one operation S7 cannot do without**: setting a key to
  an **empty mapping** — `review_floor: {}`, written as a mapping node and never as the string
  `"{}"` — and expanding a flow `{}` back into a block mapping when the first entry is written again
  (S7's `set` after the last `unset`). `internal/yamledit` does not exist yet; the package and the
  task that lands it are named, and neither is assumed present. **Its owning task is the editor
  task, and it lands first in the joint order below**; S5–S9 call it, and **no task here writes a
  second editor** (D847).
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
| 2 | S2, S4, S5 | S1 (S2, S5); S3 (S4, S5); E3 (S5); R1 (S5) |
| 3 | S6, S7, S8, S9 | S5, and S3 for S6, S7, S8 and S9 |
| 4 | S10, S11 | S1–S9 (S10); S1–S3 and S5 (S11, plus R0 and R1 for the layer half of the loop) |

**The joint order**, agreed with the `models` and `rigor` designs and the same list in all three:
**editor → policy stage (S3) → settings read (S0–S2, S4) → layer stage (R0, R1) → set (S5) → rigor
(R2–R5) and models.**

| # | What lands | Owner | Why it sits here |
| --- | --- | --- | --- |
| 1 | The one editor, `internal/yamledit` | the editor task (E3) | every later task splices a file with it, so landing it first is what keeps anyone from writing a second one meanwhile |
| 2 | `update` publishes the policy node | **S3**, the `models` design's E1 | it is the first slice's second half, it is what lets the read stop saying `unpublished` for the policy, and S6–S9 write keys only this stage publishes |
| 3 | The settings read | **S0–S2, S4** | useful before every writer, and it is what makes G1, G2 and G5 visible today |
| 4 | The layer stage, and the local layer file it publishes | the `rigor` design's **R0, R1** | `settings set --layer project` (S5) needs both the file and its publisher |
| 5 | `fugaro settings get\|set\|unset` | **S5** | the first writer over all three documents; it comes after 2 and 4 because its `next: fugaro update` line is false until both stages publish |
| 6 | `fugaro rigor …` (R2–R5) and the `models` writes | the `rigor` and `models` designs | they reuse the editor and both stages, and their keys reach runs through the stage of 2 |

### The files the two designs share

The joint order says *when*; this table says *who writes which file*, so that a task in either plan
knows whether it is creating a file, adding to one another task created, or calling one it must not
change. Where a row names the `rigor` design's task it is quoting PR #1463's plan, which states the
same sentence from its side; neither plan may write "the file exists by then".

| File | Who writes it, in order | Who else touches it | What keeps it one file |
| --- | --- | --- | --- |
| `internal/yamledit/*.go` | the editor task (**E3**, position 1) creates it | S5, S6, S7, S8, S9 add an operation **only if one is missing**; the `rigor` design's **R2, R4, R5** call the same package | one editor, generic operations (set a scalar, remove a key, add or remove a list item, upsert or remove a sequence item by key), and D847's cross-document test that all three documents preserve comments, key order and untouched bytes the same way. A second splice written by hand fails the review, not a test |
| `internal/settings/registry.go` | **S0** creates it (position 3's first task) | S1, S2 and S4 add the cloud and `layer.*` rows; S6–S9 add a row for a key they introduce | one row per key, and every row names the constant that holds the default rather than restating it — `internal/rigor`'s `rigor.Default` for the mode floors, `config`'s constants for the rest. The `rigor` design **reads** this file and writes none of it: its keys' rows land here |
| `internal/cli/settings_read.go`, `settings_render.go`, `settings_cloud.go` | **S1**, **S2**, **S4** (positions 3 and 4 of the order) | this design only | the read has one implementation and the dedicated families (`rigor show`, `config layer`, `accounts ls`, `secrets ls`) print their own narrow tables. What crosses to the `rigor` design is a rule, not code: the `unpublished` word and `unknown (no checkout on this machine)` |
| `internal/cli/settings_write.go` | **S5** creates it (position 5) | S6–S9 (this plan) and the `rigor` design's **R4 and R5**, which call it — #1463's plan names it "the shared write helper settings S5 lands" | the five helpers — the diff, the confirmation with the typed project name, the agent-session refusal, `backupFile`, `writeFileAtomic` — exist once. It is why R4 and R5 sit after S5 in the joint order: a second copy is the thing their plans and this one both forbid |
| `internal/cli/project_cmd.go` | **S3** (the agent refusal `project policy publish` lacks, SD8) | **S6** (`buildProjectPolicy` takes `credentials.*` and `clone_urls` from the local config), **S9** (the routing silence rules, shared with the publisher); the `models` design's E1 owns the policy node's build | one `buildProjectPolicy`. `TestUpdateStageAndTheExplicitDoorBuildTheSameNode` is the guard: the stage and the explicit door produce the same bytes from one local config, so a second build path cannot appear quietly |
| `internal/cli/init_policy_publish.go` | **S3** creates it | this design only | it is the policy node's half of the pair. The layer's half is #1463's `internal/cli/init_layer_stage.go`: two objects, two stages, and neither file holds the other's write |
| `internal/cli/init_stages.go` | **S3** adds the publish to the `Policy` stage's work | the `rigor` design's **R1**, which adds its `Layer` stage to the same list | **the one file both designs edit.** The stages are registered in one list, each design adds its own entry and reorders nothing; S3 is position 2 and R1 is position 4, so whoever lands second rebases onto a list that already has the other's entry. Two stages, not one stage doing both writes: publishing the layer from the policy stage would put the layer's seeding rules under the policy stage's confirmation |
| `internal/cli/rigor.go` | **whichever of the `rigor` design's R3 and this design's S7 lands first** creates the group | the other adds its own subcommand — `floor` for S7, `set`/`unset`/`show` for R3 | both plans state this sentence; neither may write "the group exists by then". The group holds no key logic of its own, so it is a registration list and not a second place for a rule |
| `internal/rigor/*.go` | the `rigor` design's own tasks | nobody here writes it: S0's rows **read** `rigor.Default` and the mode floors as the constants they already are | the floor's semantics live where the resolver is. A default copied into a registry row would be the fourth copy F12 is about |

## Gates (every task)

`go build ./... && gofmt -l . && go vet ./...` (gofmt prints nothing); the task's named tests with
`GOFLAGS=-p=2 go test ./internal/<pkg>/ -run '<names>' -count=1`; `GOFLAGS=-p=2 go test ./internal/cli/
-run Docs -count=1 -timeout 40m` when a doc changed; `go test ./internal/eeboundary/... -count=1`.
Never the whole `internal/cli` package.

## S0. The key and defaults registry (SD16, D840, D841)

Without it the DEFAULT column, `--changed` and the schema `default` keywords have no source but a
fourth hand-kept copy (F12).

- **Files (6):** `internal/settings/registry.go` (new), `internal/settings/registry_test.go` (new),
  `internal/settings/defaults_test.go` (new), `internal/config/defaults.go` (the **named default
  constants** live here, beside `config.DefaultBaseBranch`, and `applyDefaults` reads them instead of
  restating them), `internal/config/defaults_test.go`, `internal/localcfg/localcfg.go` (the same for
  the local loader's literals, which become the same constants).
- **Interface:** `settings.Row{Key, Layers, Merge, Default, Sensitive, Owner, Doc}`, `settings.Rows()`,
  `settings.Lookup(key) (Row, bool)`, `settings.DefaultOf(key) (value string, source string)`. The
  `fugaro.yaml` rows are built by reading `config.Scopes`, so the two cannot drift.
- **Where the constants live, and why not in the registry:** `internal/settings` imports
  `internal/config` for `config.Scopes`, so `applyDefaults` importing `internal/settings` would be an
  import cycle — and so would the local loader's, once the registry carries row knowledge of
  `localcfg`. The arrow runs one way: the constants sit in `config`, `applyDefaults` and the local
  loader read them, the registry reads the same constants for its `Default` field, and
  `TestRegistryDefaultsMatchApplyDefaults` pins the row to what `applyDefaults` actually produces.
  Without that pin the DEFAULT column and a real run's value could part company with nothing red.
- **Red step:** `TestRegistryCoversEveryConfigField` fails while the registry does not exist. The
  second half of the task — the two copies of `"main"`, in `applyDefaults` and in
  `config.DefaultBaseBranch`, collapsing into one constant — is carried by
  `TestRegistryDefaultsMatchApplyDefaults`, which is green only once both sides read that constant.
- **Tests:** `TestRegistryCoversEveryConfigField` (every `fugaro.yaml`, project-layer and
  local-config field has a row), `TestRegistryFugaroYAMLKeysMatchConfigScopes` (row for row with
  `config.Scopes`), `TestRegistryKeysAreUnique`, `TestRegistryDefaultsMatchApplyDefaults` (the
  registry's `Default` for every row equals the value `applyDefaults` and the local loader leave in
  place), `TestRegistryOwnerNamesACommand`, `TestRegistryMarksEverySensitiveKey`.
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
  [--changed] [--why] [--offline] [--json]`, columns `KEY SCOPE VALUE WINS DEFAULT STATE` (spec §2).
- **Red step:** `TestSettingsPrintsKeyValueWinsDefaultState` fails while the command does not exist.
- **Tests:** `TestSettingsPrintsKeyValueWinsDefaultState`,
  `TestSettingsShowsTheDefaultWhenNoLayerSetsIt`, `TestSettingsChangedShowsOnlyNonDefaults`,
  `TestSettingsUnknownKeyNamesTheNearest`,
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

## S3. `fugaro update` publishes the policy node, and every door that writes it is refused to an agent (SD3, SD8). **SEC**

The stage writes the node every run of the project is admitted by, and it decides what a run may
spend. It is E1, and it is the first slice's second half. **All four** doors SD8's parity names are
here (F16): `project policy publish`, which writes the node, and `project repos add`,
`project repos rm` and `project sync`, which write its `allow_repos` — `sync` through the same
ETag-guarded write as `add`, and the only refusal any of them carries today is
`sync --github-app-key`'s, which guards the key read and not the write.

- **Files (9):** `internal/cli/init_policy_publish.go` (new: build from the local config with
  `buildProjectPolicy`, the diff, the confirmation, the ETag write), `internal/cli/init_policy_publish_test.go`
  (new), `internal/cli/init_stages.go` (the publish becomes part of the `Policy` stage's work),
  `internal/cli/init_policy_migrate.go` (the shared build-and-write path, so the stage and the
  explicit door cannot drift), `internal/cli/project_cmd.go` (the agent refusal `project policy
  publish` lacks today, SD8), `internal/cli/project_cmd_test.go`, `internal/cli/project_enroll.go`
  (the same refusal for `project repos add`, `project repos rm` and `project sync`, none of which
  carries one today — `sync`'s `--github-app-key` refusal is `refuseKeyReadInAgent` on the key read),
  `internal/cli/project_enroll_test.go`, `docs/policy-migration.md` (§ on who
  publishes, and the one command).
- **Red step:** `TestUpdatePublishesThePolicyNodeWhenItIsUnpublished` fails while no stage builds the node
  from the local config.
- **Tests:** `TestUpdatePublishesThePolicyNodeWhenItIsUnpublished`, `TestUpdatePolicyStageWritesNothingWhenTheNodeMatches`,
  `TestUpdatePolicyStageWritesUnderTheETag` (the write carries the ETag the read returned),
  `TestUpdatePolicyStageRefusesWhenTheNodeMovedUnderIt` (a node another writer changed between the
  read and the write is a conflict, not an overwrite: nothing is written and the sentence says whose
  generation won),
  `TestUpdatePolicyStageRefusesWhenThePublishedGenerationIsNewer` and
  `TestUpdatePolicyStageAdoptsTheGenerationWhenNothingIsRecorded` (D854's across-runs guard: a
  generation recorded in the local config that the published object has moved past is a refusal and
  not a publish, and the first update on an installation with no baseline adopts the generation it
  read instead of asking),
  `TestUpdatePolicyStageAppliesNothingInAnAgentSessionEvenWithYes`,
  `TestUpdateStageAndTheExplicitDoorBuildTheSameNode` (both go through `buildProjectPolicy` and
  produce the same bytes from one local config — the guard against a second build path appearing
  later),
  `TestInstallationAtThePreviousVersionConvergesPolicyStage` (an installation whose node predates
  this release ends the update with it published — the HARD RULE's own test; **the `…PolicyStage`
  suffix is what keeps it apart from R1's `TestInstallationAtThePreviousVersionConverges`**, the same
  test for the layer, since both live in package `cli` and one name cannot be declared twice),
  `TestProjectPolicyPublishIsRefusedInAnAgentSession`, `TestProjectPolicyPublishKeepsDeleteAndSeedRepos`,
  `TestProjectAllowListDoorsAreRefusedInAnAgentSession` (SD8's allow-list half, F16: `repos add`,
  `repos rm` and `sync` each write nothing and print the sentence, and `sync` is refused with and
  without `--github-app-key`).
- **The publish half holds #1460's adoption gate, and it holds the same one the migration half
  does.** `policyMigrationFor` (`internal/cli/init_policy_migrate.go`) asks `budgetDBUnadopted`
  before it reads the node and returns `policyMigrationNotOurs` for a database in a Firebase project
  other than the installation's own that was never adopted; the stage turns that into
  `initflow.Skipped`, not an error. The publish is additive work in that same stage, so it must not
  become a second writer with its own, weaker gate — a database the rules stage and the migration
  both skip would otherwise come out writable the moment publishing lands, which is exactly the
  drift #1460's review found between two gates worded alike. The publish therefore reaches the node
  through the same `policyMigrationFor` call (one gate read, one ETag, one write path), and
  `TestUpdatePolicyPublishSkipsTheDatabaseTheRulesStageSkips` pins it: red on removal, because a
  stage that publishes from a path of its own writes the node into a database this installation
  never adopted. `policyNodeUnreachable`'s skip (no budget backend at all) is the other half of the
  same test, the way `TestUpgradePolicyStepSkipsWhereItCannotWrite` already reads it.
- **Hermetic:** `internal/gcpfake`, a fake Realtime Database, `t.TempDir()`. No apply, no deploy, no
  cloud call.
- **Its own `### For operators` entry, drafted in this task's PR body** — the first slice's step, and
  S10's entry does not repeat it: "`fugaro update` now publishes the policy node from the local
  config; `project policy publish` is refused in a coding agent's session". Operators read that line
  in `docs/releases/vX.Y.Z.md`, whose `### For operators` section is the only place a post-deploy step
  may live (`docs/releases/README.md`), and that file is written by the release PR
  (`scripts/release.sh`) — so this task **carries the wording in its own PR body, for the release PR
  to copy**, and its files list stays the files it edits: a task cannot name a notes file whose
  version it does not know, and `docs/releases/**` is another work stream's (above).
- **The within-one-run guard is not in question, and it is this task's**: the write carries the
  generation the read returned, so a publish someone else slipped in between them is a conflict and
  not an overwrite — `TestUpdatePolicyStageRefusesWhenTheNodeMovedUnderIt`, above. The layer stage
  carries the same guard as R1's `TestUpdateLayerStageRefusesWhenTheGenerationMovedSinceTheSeed`.
- **D854 is answered — the generation is recorded** (owner ruling 2026-10-31;
  [reconciliation.md](reconciliation.md) §4) — and this task builds its policy half. The stage
  records the generation the local file was seeded from or last published at (`policy_generation`
  on `localcfg.Config`, in the project's local config), refuses to publish over a generation that
  has moved since, prints the three-way state, and offers to re-seed the local file from the
  published one under confirmation with a backup —
  `TestUpdatePolicyStageRefusesWhenThePublishedGenerationIsNewer` and its
  `…AdoptsTheGenerationWhenNothingIsRecorded` twin are the two halves, the second pinning the first
  run on an installation that has no baseline (it adopts: refusing once would ask a question with no
  information in it). **Rejected: publish on difference anyway** — it loses the newer publish on the
  second machine, and the ETag guard and the diff/backup/confirm triple cover only a publish this
  run itself raced.

## S4. The layer side of the read (SD4, D845)

The read side only: the layer's local file has a path — `~/.config/fugaro/projects/<project>.layer.yaml`,
resolved by `localcfg.LayerFile` — and a local file that differs from the published object is
`unpublished`. The stage itself is E2 and is not written here. **`localcfg.LayerFile` is not on
`main`**: the rigor design's R0 lands it (its E2 above), so this task's red step is red against a
function that does not exist yet, and S4 waits for R0 the way the needs table says.

- **Files (4):** `internal/cli/settings_layer.go` (new: `localcfg.LayerFile`, the published object,
  the `unpublished` comparison), `internal/cli/settings_layer_test.go` (new),
  `internal/cli/settings_cloud.go`, `internal/settings/registry.go` (the `layer.*` rows).
- **Red step:** `TestSettingsMarksAPublishedLayerThatIsUnpublished` fails while the local
  layer file's path is not resolved.
- **Tests:** `TestSettingsMarksAPublishedLayerThatIsUnpublished`,
  `TestSettingsLayerRowsNameTheLocalFileAndBothShas`,
  `TestSettingsWithNoLocalLayerFileReadsThePublishedOneAndSaysSo`.
- **Hermetic:** `internal/gcpfake` for the published object, `t.TempDir()` for the file.

## S5. `fugaro settings get|set|unset` (SD2, SD9, D846, D847, D852, D853). **SEC**

It edits the local project config, the project layer file and a repository's `fugaro.yaml`: three
documents that decide where a repository's code goes and what a run may cost, and one of them
outside every repository, which is why the agent-session refusal is here.

- **Files (7):** `internal/cli/settings_set.go` (new), `internal/cli/settings_set_test.go` (new),
  `internal/cli/settings_write.go` (new: diff, confirmation, agent refusal, `backupFile`,
  `writeFileAtomic` — shared with S6–S9 **and with the `rigor` design's R4 and R5**, which write
  their floors and modes through the same helpers rather than a fourth copy of the triple),
  `internal/cli/settings_write_test.go` (new),
  `internal/cli/settings.go`, `internal/yamledit/*.go` (only if an operation this task needs is
  missing from the shared editor — the editor task owns that package and lands it first) and
  `internal/yamledit/edit_test.go` (D847's cross-document test, same condition).
- **Red step:** `TestSettingsSetRefusesAKeyOwnedByAnotherFamily` fails while the subcommand does not
  exist.
- **Tests:** `TestSettingsSetRefusesAKeyOwnedByAnotherFamily`,
  `TestSettingsSetRefusesALayerTheKeyMayNotBeSetIn`, `TestSettingsSetRefusesAValueTheLoaderRefuses`,
  `TestSettingsSetRepoLayerWithoutRepoUsesTheCurrentCheckout` (spec §4.2 step 2: with no `--repo` the
  write lands in the current checkout's `fugaro.yaml`),
  `TestSettingsSetRepoLayerOutsideACheckoutIsRefused` (the other half of that step: no `--repo` and no
  checkout is refused rather than guessed),
  `TestSettingsSetRefusesInAnAgentSessionEvenWithYes`, `TestSettingsSetRepoLayerIsAllowedInAnAgentSession`,
  `TestSettingsSetLooseningACeilingAsksForTheTypedProjectName` (and `--yes` does not stand in),
  `TestSettingsSetRepoLayerRefusesALooseningInAnAgentSession` (spec §4.2 step 4: the typed-name
  carve-out — a ceiling loosened at `--layer repo` is refused in an agent session, `--yes` included),
  `TestSettingsSetBacksUpAndWritesAtomically`, `TestSettingsSetKeepsTheRestOfTheFile`,
  `TestSettingsAddAndRemoveOnAListKey`, `TestSettingsRefusesEmptyingAListKey`,
  `TestSettingsUnsetRemovesTheKeyRatherThanWritingTheDefault`,
  `TestSettingsUnsetOfACeilingAsksForTheTypedProjectName` (spec §4.3: removal crosses the same
  boundary as loosening, so `unset` of a ceiling or a floor asks for the typed name and `--yes` does
  not stand in),
  `TestSettingsUnsetOfNothingIsAnExitZeroSentence`, `TestSettingsSetPrintsOnlyFugaroUpdateNext`,
  `TestSettingsSetWritesNothingInTheCloud`, `TestTheThreeEditorsPreserveBytesTheSameWay` (D847's
  cross-document test), and `TestSettingsGetOnAKeySetNowhereIsTheDefaultRowAndExitZero` — `get` is
  this task's subcommand and not S1's, so its "a key set nowhere prints the default row and exits 0"
  case (spec §4.1, D852) is asserted here, even though the row it prints is the one S1's read builds.
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
- **Tests:** `TestProjectCredentialsSetWritesTheLocalBlock` (`set fallback`),
  `TestProjectCredentialsRefusesAnUnknownFallback`,
  `TestProjectCredentialsSetWritesWaitThreshold` (`set wait-threshold`, a duration),
  `TestProjectCredentialsSetWritesBackoffInitialAndCap` (`set backoff --initial --cap`, both keys in
  one write),
  `TestProjectCredentialsSetWritesConcurrencyPerToken` (`set concurrency-per-token`),
  `TestProjectCredentialsSetWritesStopAfterExhaustion` (`set stop-after-exhaustion`),
  `TestProjectCredentialsShowPrintsTheBlockAndNoSecret` (the read: the row format of spec §2, the
  fallback named, nothing sensitive),
  `TestProjectCloneURLSetWritesTheLocalBlock`, `TestProjectCloneURLLsListsEveryCanonical`,
  `TestUpdateImportsTheNodesCredentialsIntoTheLocalFile` (and `clone_urls`),
  `TestUpdateImportDeclinedLeavesBothSidesUntouched`,
  `TestUpdateSeedsNothingWhenTheNodeCarriesNeitherBlock`,
  `TestBuildProjectPolicyPublishesTheLocalBlocksAndNotTheNodesCopy`,
  `TestProjectCredentialsRefusesInAnAgentSessionEvenWithYes`,
  `TestProjectCloneURLRefusesInAnAgentSessionEvenWithYes`,
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
  `TestRigorFloorSetLoweringAsksForTheTypedProjectName`,
  `TestRigorFloorShowPrintsTheFloorPerMode` (the read: every mode with its levels, `{}` shown as a
  floor of nothing rather than as absent),
  `TestRigorFloorUnsetRemovesTheModeEntry`, `TestRigorFloorUnsetOfTheLastModeKeepsTheEmptyFloor` (the
  subtle half: a floor of nothing is written `{}`, where an absent key means the layer below decides
  — the two are not the same at publish, so the last `unset` leaves the key standing rather than
  deleting it; spec §5's row states the same rule in the same words),
  `TestRigorFloorUnsetOfNothingIsAnExitZeroSentence`, `TestRigorFloorRefusesAnUnknownLevel`,
  `TestRigorFloorRefusesInAnAgentSession`, `TestRigorFloorKeepsTheRestOfTheFile`,
  `TestRigorFloorPrintsOnlyFugaroUpdateNext`.
- **The `{}` is an empty mapping, and that is a spelling the editor has to carry.** `review_floor: {}`
  is YAML's only spelling of an empty mapping — block style has no empty form, and `review_floor:`
  with nothing after it is a null scalar, not a mapping. So the last `unset` cannot be written with
  the editor's `SetScalar`, which would put the two characters `{}` in as a **string** the loader
  then reads as a scalar and refuses, and the editor refuses to edit *inside* a flow `{}`. Two
  operations make S7 work, and both belong to **the editor task (E3), not to this one**: setting a
  key to an empty mapping node (`review_floor: {}`, the owner's comment above it preserved) and
  expanding a flow `{}` back into a block mapping when `set` writes the first entry again — without
  the second, a `set` after the last `unset` has nothing it can write into. S7 calls both and writes
  neither: a splice written by hand here would be the second editor D847 forbids. E3's scope in
  "Order, and what this plan does not own" names them, so the editor task is written knowing one
  caller needs them.
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
- **Tests:** `TestAccountsAddCreatesTheAccountWithItsAllowList` (the first form: an account that does
  not exist yet is created with the repositories it may spend from),
  `TestAccountsAllowAppendsTheRepository`, `TestAccountsDenyRemovesIt`,
  `TestAccountsRmRemovesTheAccount`, `TestSecretsAllowWritesThePatternList`,
  `TestSecretsAllowProxySetsTheFlag`, `TestUpdateRepoReadsTheAllowListFromTheNode`,
  `TestAccountsLsShowsWhatAccountsAddAndAllowWrote` and
  `TestSecretsLsShowsWhatSecretsAllowWrote` (the read path: `accounts ls` and `secrets ls` are the
  family's reads, so these assert the writes are what those commands print, and that no token is),
  `TestAccountsAllowRefusesInAnAgentSession`, `TestSecretsAllowRefusesInAnAgentSession`,
  `TestAccountsAllowPrintsOnlyFugaroUpdateNext`.
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
  `TestProjectProfileRmRefusesWhileAWorkflowUsesIt`, `TestProjectProfileLsListsTheProfiles` (the
  read), `TestProjectRoutingSetWritesTheVia`, `TestProjectRoutingUnsetRemovesTheBlock`,
  `TestProjectRoutingShowPrintsTheRoute` (the read),
  `TestProjectRoutingRefusesSilenceThatTurnsTheCascadeOff`,
  `TestProjectProfileRefusesInAnAgentSession`, `TestProjectRoutingRefusesInAnAgentSession`,
  `TestProjectProfilePrintsOnlyFugaroUpdateNext`.
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

## S11. The end-to-end converge test (SD3, SD4, D849)

The whole design is one loop: set a value, see it `unpublished`, run `fugaro update`, see it `ok`.
Every task above tests one half of it, and the halves are exactly what can drift apart, so the loop
itself is a task: it is the test the HARD RULE is about (the published change that silently does
nothing, G1 and G2).

- **Files (1):** `internal/cli/settings_converge_test.go` (new).
- **Red step:** `TestSetReadUnpublishedUpdateThenConverged` fails while the read command and the update
  stage do not both exist. It is the one test that spans two tasks, so it is written with the first
  slice and stays red until S5 can write the value it converges.
- **Tests:** `TestSetReadUnpublishedUpdateThenConverged` — one hermetic installation (a fake Realtime
  Database and `internal/gcpfake`): `settings set` a ceiling key at `--layer local`, read it back as
  `unpublished` with the local value in `VALUE`, run the `fugaro update` policy stage against the fake,
  read again and see `ok`, the same value, and `WINS policy` — the node now governs and the fixture's
  deployed job carries no ceiling of its own (`WINS job` would be the deployed copy's; §2.2 spells the
  two levels apart on purpose); then the same loop for a project-layer key
  once S4 and R1 are in.
  `TestConvergePrintsOnlyFugaroUpdateNext` (the loop's whole output names `next: fugaro update` once and
  no second command).
- **Hermetic:** `internal/gcpfake`, a fake Realtime Database, `t.TempDir()` config home. No cloud call.

## Live checks

None. Every behaviour above is a local file edit, an offline read, or a stage of `fugaro update`
whose cloud writes go through the publish paths the installation's existing update checks already
exercise — the same `buildProjectPolicy`, `publishLayer` and `fanOutLayer` the explicit doors use,
with the same confirmation and the same ETag. No new operator step is added, so no check number is
taken.
