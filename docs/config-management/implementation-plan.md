# Implementation plan: management commands for every configuration

**Status: plan only; nothing is implemented; no task approved.** Fourteen tasks, **G0–G13**, each with
its files, its interface, its **red step** and its named tests. Every task is written against the tree
of 2026-11-03 and every citation in it was resolved while writing. **Nothing here starts until the owner
answers the decisions in [reconciliation.md](reconciliation.md) §4** — the tasks name the decision each
one implements, so an unanswered decision is a task that does not run.

## 1. What this plan does not own

- **No decision.** A task implements a decision of the reconciliation; a task whose decision changed
  changes with it, and a task that finds a question the decisions do not answer **reports it** rather
  than deciding it in code (`CLAUDE.md`: a fix round adds no functionality).
- **The managed gate's code.** D1180's rows are documents here; the gate, the marker's versions and the
  audit record are the managed build's (`docs/managed-agent-converge/implementation-plan.md`, M0–M9).
  No task in this plan edits `internal/managed/`.
- **`.github/workflows/`.** No task creates, edits or deletes a workflow file; anything that would need
  one is a note under `docs/pending-workflows/` and a handoff in the release notes.
- **A new dependency.** `go.mod` is unchanged by every task.

## 2. Order, and what forces it

**The generic writer first (D1160), then the blocks it writes, then the dedicated commands, then the
gate's rows.** The order inside a step is a task's own business; the order between steps is not.

| Order | Tasks | What forces it |
| --- | --- | --- |
| 1 | **G0**, **G1**, **G2** | D1160: the primitive. A dedicated command written before these pays for the same mechanism twice |
| 2 | **G3**, **G4** | G0 and G1 (the wildcard allowlist and the entry shapes); G4 is the widest surface of the two and takes the reviewer |
| 3 | **G5**, **G6** | G4 for both — the publish is a layer *write*'s next step and there is no write to publish before it, and G6's own design (R1) publishes the layer the rigor command writes in the same run |
| 4 | **G7**, **G8**, **G9**, **G10**, **G11**, **G12** | G2 (the document's members, for the commands the console can see) and their own block's task from step 2 or 3 |
| 5 | **G13** | Every command's final rules: a gate row written before its command's refusals are settled is a row that has to be rewritten |

**The signature of the order** — the plan's own reading of its dependencies, and what a reader is owed
when a task moves:

**G0 → G1 → G2 → G3 → G4 → G5 → G6 → G7 → G8 → G9 → G10 → G11 → G12 → G13.**

**What the managed build owes this plan.** A command task whose command the gate admits calls the gate
(M0) and, where it writes, M6's audit recorder — and D1014's rule that **a failure to write the record
fails the command** means a command that ships before M6 either writes nothing or fails every apply.
§5 names the tasks exactly.

## 3. Gates (every task)

- `go build ./... && gofmt -l . && go vet ./...` — gofmt prints nothing.
- The task's named tests: `GOFLAGS=-p=2 go test ./internal/<pkg>/ -run '<names>' -count=1 -timeout 40m`.
  **Never the whole `internal/cli` or `internal/runner` package.**
- The **wider pin gate**: for every file the task changes or adds, find the tests that name it
  (`grep -rln '<base name>' --include='*_test.go' internal/ cmd/`) and run each with a `-run` filter
  naming them.
- A doc touched: `GOFLAGS=-p=2 go test ./internal/cli/ -run 'Docs|Backlog' -count=1 -timeout 40m`.
- **A test this plan adds that fails locally stops the task**: never push over it, never weaken, skip or
  delete it. Report it.
- **SEC** on a task marks the surfaces the brief names: a secret, a credential, an `allow_data_to`
  value, the agent fence and the managed gate. A **SEC** task's reviewer is the second pair of eyes the
  brief asks for, and its refusals are read line by line rather than tested only.

## 4. The shared-files table

Files more than one task touches, and the rule for each. **A task that needs a file in this table that
another task owns adds its edit only after that task has merged**, and never re-derives what the owner
landed.

| File | Tasks | What each one does with it |
| --- | --- | --- |
| `internal/cli/settings_set.go` | G0, G1, G2, G3, G4, G6, G10, G12 | G0 widens the allowlist (D1179's two budget keys included) and reuses the wildcard resolver; G1 adds the entry flags and the dispatch; **G2 adds the document's entry members and does not move its version**; G3 adds the `workflows.*` rows and G10 the `repos.*` scalars; **G6 edits no allowlist entry** — the rigor rows are the `rigor` family's, which this file's map already refuses, so it reads only the write path it calls; G12 edits the owner strings. **G0 first, then G1, then G2** — each adds and none rewrites the other's lines |
| `internal/cli/settings_write.go` | G1, G7, G9 | G1 classifies an entry write through the existing helpers; G7 and G9 call `confirmTypedStrict` and `applySettingsEdit` for their own writes. **No task edits this file's fence, its compare-and-swap or its backup** |
| `internal/settings/registry.go` | G0, G3, G4, G6, G10, G12 | G0/G3/G4/G10 add rows for the blocks they own; G6 re-spells the rigor rows only if its design's own spelling differs; **G12 changes owner strings only** and moves none of the four budget keys (D1179). One row, one owner |
| `internal/yamledit/yamledit.go` | G1, G4 | G1 wires the object-list ops that exist; G4 needs the map-member op if the dispatch cannot reach it through a parent path. **G4 adds the op only if G1 did not** — the two read each other's diff before either merges |
| `schemas/settings-write.schema.json` | G2, G8, G9 | **G2 adds the entry members to version 1 — it does not move the version** (D1164); G8 and G9 add the `secrets` and `accounts` operations' members beside them. **G2 first** |
| `internal/cli/config_publish.go` | G4, G5 | G4 reuses the reserved-key and executable-key refusals; **G5 calls `publishLayer` (`internal/cli/config_publish.go:173`) from `update`'s Layer stage and adds no second publisher**. Two tasks, one file, **G4 first** |
| `docs/config-management/` | G13 | G13 is the only task that edits this folder after the design merges, and it changes a *status* line, never a decision |
| `docs/rigor-modes/reconciliation.md` | none after the design | The census row (F14) is moved **in the design's own commit**, not by a task |

## 5. The tasks that need the managed build's M0 and M6

- **M0** (the gate's shape, `internal/managed/converge.go`) is called by every task that adds a command
  the gate admits: **G3, G4, G6, G10, G11**. A task that adds a command with no M0 call is a command
  that stays agent-refused in a managed project, which is the safe direction and not the plan's intent.
- **M6** (the audit record's write) is called by the same five, plus **G9** (an `accounts rm` under the
  gate). D1014's *a failure to write the record fails the command* is why none of them may land before
  it: a command that applies and records nothing is the state §6 of the managed spec exists to end.
- **G0 and G1 add no gate call of their own**, because neither adds a command: they extend
  `fugaro settings set|unset`, whose gate call is the **settings design's own handoff row** in the
  managed plan's §2 (the three handoffs are rows to copy, not tasks) — so the keys and the entry shapes
  the two add are gated by a row that design already owes, and the tasks wait on nothing here.
- **G2, G5, G7, G8, G12 and G13** need **neither** to be written: G2 adds a document's members, G5 rides
  on `update`'s existing Layer stage (M1's own gate call and M6's record), G7's floor removal and G8's
  whole family are refused in an agent's session whatever the gate says (D1170, D1171), G12 renames an
  owner string rather than calling anything, and G13 writes the rows.
- **What a task does when M0 or M6 is not on `main` yet:** it lands the command with its agent refusal
  intact and its row written, and it **reports the missing call** rather than inventing a predicate —
  the managed plan's own §2 says a row that has not landed leaves its command agent-refused, which is
  the same direction.

## G0. The generic writer's wildcard rows and the two budget keys (D1160, D1161, D1179). **SEC**

- **Interface.** `settingsGenericKeys` (`internal/cli/settings_set.go:68`) gains the wildcard rows this
  design manages, **and the two budget keys D1179 gives the generic path** (`budget.mode`,
  `budget.per_day_usd`) — the two whose rows default to the generic owner while the allowlist refuses
  them on purpose (F13), with the list's own comment (`:60`) rewritten beside them;
  `settingsWriteLookup` (`:328`) and `settingsPatternMatch` (`:351`) are **reused unchanged**, and the
  literal `*` refusal (`:306`) stays. A concrete key of a row the allowlist does not carry keeps today's
  refusal, whose sentence names the nearest keys.
- **Decided here, and tested:** writing one concrete key into a block the document does not hold
  **creates the block** (D1161's stated cost), and the created block is refused when its key may not be
  set in that layer (`--layer` and the row's own `Layers`).
- **Red step.** `TestSettingsSetConcreteWildcardKeyWrites` — `settings set workflows.web.commands.test
  --layer repo --dry-run --json` resolves a row and prints the document; it fails today with *no setting
  has that name*. Then `TestSettingsSetWritesBudgetModeAndPerDayUsd` (F13's two keys, refused today
  with *not a key the generic path writes*), `TestSettingsSetLiteralStarIsStillRefused` and
  `TestSettingsSetConcreteKeyOfAnUnlistedRowIsRefused`.
- **Files (≤5).** `internal/cli/settings_set.go`, `internal/cli/settings_set_test.go`, and the test that
  pins the document's shape.
- **SEC.** It is the write path: the classification, the fence and the compare-and-swap are read and
  must be **unchanged** by this diff.

## G1. `--add-entry` and `--remove-entry` (D1162, D1163, D1167). **SEC**

- **Interface.** Two flags on `settingsWriteOptions` (`internal/cli/settings_set.go:121`) beside `--add`
  and `--remove`, dispatched in the same switch (`:1005`) to `yamledit.UpsertSeqItemByKey` /
  `RemoveSeqItemByKey` (`internal/yamledit/yamledit.go:294`, `:351`) for an object list, and to a
  map-member write for a map. **One entry per invocation** (D1167); the entry's key field is the row's
  own, decided per block in this task's table and pinned by its tests.
- **The classification.** An entry write goes through `settingsTypedWrite`
  (`internal/cli/settings_set.go:886`) unchanged, so adding a `secrets` member or an `allow_data_to`
  value is a widening and takes the typed name (D1165).
- **Red step.** `TestSettingsSetAddEntryMapMember` and `TestSettingsSetAddEntryObjectListEntry` — each
  fails today with the *a key with a `*` is a map entry* refusal (`:306`). Then
  `TestSettingsSetRemoveEntryTakesOneOfADuplicateKey`, `TestSettingsSetAddEntryRefusesTwoEntries` and
  `TestSettingsSetAddEntrySecretsMemberIsATypedWidening`.
- **Files (≤6).** `internal/cli/settings_set.go`, `internal/cli/settings_write.go` (the entry's
  classification only), `internal/yamledit/yamledit.go` (only if a map member has no op), and the tests.
- **SEC.** An entry write is how a `secrets` block or an `allow_data_to` list grows; its widening half
  is read by the reviewer against D846 and D1107.

## G2. The settings-write document's entry members (D1164, D1166)

- **Interface.** `settingsWriteDocVersion` (`internal/cli/settings_set.go:1197`) and the schema's const
  (`schemas/settings-write.schema.json:20`) **stay at `1`**, and the entry write's members are **added as
  optional ones**: `operation`, the entry's key field and the members the entry carries. Every version-1
  member keeps its name and its meaning, so the console's edit — which pins version 1
  (`console/src/main/settingsview.ts:42`) and reads `changed`, `widening`, `why`, `old` and `new` — reads
  a document of this shape unchanged, and **no console file is edited by this task**.
- **Red step.** `TestSettingsWriteDocWithoutTheEntryMembersStillDecodes` — the document a plain scalar
  write produces carries none of the new members, still decodes against the schema, and its `version` is
  `1` — which fails today because the members do not exist to be optional. Then
  `TestSettingsWriteDocEntryOperationNamesTheEntry` and the docs test that pins the schema's version.
- **Files (≤5).** `internal/cli/settings_set.go`, `schemas/settings-write.schema.json`, the CLI test and
  the docs test.
- **Not in this task:** a console change, and never a version bump. If the console's own tests refuse the
  added members, that is a **report** and a handoff to the console's owner, never an edit from here; and
  a member that would have to *change* one the console reads is a new version, which is a decision of
  its own rather than a widening of this task.

## G3. A repository's `workflows.*`: `fugaro workflow add` / `rm` (D1177). **SEC**

- **Interface.** A new `fugaro workflow` group with `add NAME` (creates the block with its `--layer repo`
  target) and `rm NAME` (removes it), both through `applySettingsEdit`
  (`internal/cli/settings_write.go:86`) as `--layer repo` writes — the one document an agent session may
  write (`:167`). The block's scalars and lists are G0's and G1's; the executable-key refusal
  (`internal/cli/config_publish.go:309`) applies to this edit as it does to a layer publish.
- **Red step.** `TestWorkflowAddCreatesTheBlock` and `TestWorkflowRmRemovesTheBlock` — each fails today
  because the group does not exist. Then `TestWorkflowAddRefusesAnExecutableKeyWithoutTheOwnersFlag`,
  `TestWorkflowRmRemovesOneWorkflowOfSeveral`, and `TestDocsConfigInitStillWritesAMinimalFile` (D1177's
  second half: `config init`'s output is unchanged, `internal/cli/config_init.go:84`).
- **Files (≤8).** a new `internal/cli/workflow.go`, its test, `internal/cli/settings_set.go` (the
  `workflows.*` rows), `internal/settings/registry.go` (a row only if the block's own keys need one) and
  the pin tests.
- **SEC.** The documents it writes carry `image.apt`, `image.setup` and `commands.*`.

## G4. The project layer file's entries (D1175). **SEC**

- **Interface.** Every entry of `<config home>/fugaro/projects/<name>.layer.yaml`
  (`internal/localcfg/localcfg.go:1120`) — `profiles.*`, `defaults.*` (agent, git, rigor) and `ab.*` —
  written through G0 and G1 against `layerRows` (`internal/settings/registry.go:411`), with
  `config publish`'s reserved-key and executable-key refusals (`internal/cli/config_publish.go:309`)
  applied to the edit's own document. The layer's `--layer project` write keeps ending with
  `next: fugaro update` (`internal/cli/settings_set.go:1070`).
- **Red step.** `TestSettingsSetLayerProfileEntryWritesTheLayerFile` and
  `TestSettingsSetLayerAbEntryWritesTheLayerFile` — each fails today with a refusal naming the generic
  allowlist. Then `TestSettingsSetLayerRefusesAReservedKey` and
  `TestSettingsSetLayerRefusesAnExecutableChangeWithoutTheOwnersFlag`.
- **Files (≤10).** `internal/cli/settings_set.go`, `internal/settings/registry.go`,
  `internal/cli/config_publish.go` (the refusals, reused), and the tests.
- **SEC**, and the reviewer this plan names hardest: the layer file is the document that decides which
  profile a project's repositories run under.

## G5. The layer write publishes through `update` (D1176). **SEC**

- **Interface.** `fugaro update`'s Layer stage publishes a layer file a writer here has just written,
  idempotently: an unchanged file is a no-op that says so, and the stage is **skipped** when there is no
  local layer file. `fugaro config publish` (`internal/cli/config_publish.go:27`) keeps its behaviour and
  its generation record (`:255`).
- **Red step.** `TestUpdatePublishesALayerFileAWriterJustWrote` — a layer file written through the write
  path, then `update`'s Layer stage, then the published object carries the entry — which fails today
  because the stage does not publish a local edit. Then `TestUpdateLayerStageIsANoOpWhenNothingChanged`
  and `TestUpdateLayerStageSkipsWhenNoLayerFile`.
- **Files (≤6).** `internal/cli/init.go` (the Layer stage), `internal/cli/config_publish.go` (its
  `publishLayer` **called, never copied**), a new `internal/cli/update_layer.go` if the call site needs
  one, and the tests. Hermetic: `internal/gcpfake`, `t.TempDir()`.
- **SEC.** It is the step that reaches the bucket: the layer file decides which profile and which
  workflows a project's repositories run under, and publishing it is what makes an edit real for every
  job that follows.

## G6. `fugaro rigor set` (D1169). **SEC**

- **Interface.** The command `docs/rigor-command/` designed (D830–D839, D900) — the rank rule (a mode at
  or above the resolved one), D834's `hotfix` refusal, D838's duplicate-glob refusal — writing through
  `applySettingsEdit`. **Its first step is re-reading that design against the tree**, because this
  folder's plan inherits its rules and does not restate them.
- **Red step.** `TestRigorSetRefusesAModeBelowTheResolvedOne` and `TestRigorSetRefusesHotfix` — each
  fails today because the command does not exist — then `TestRigorSetWritesTheLocalProjectConfig`.
- **Files (≤8).** a new `internal/cli/rigor.go`, its test, `internal/settings/registry.go` (rows only),
  and the pin tests for the rigor design's own docs.
- **SEC.** It writes the rigor mode — the depth and the cost every review of a run of this repository is
  held to — through the write path, and where it lowers a floor its gate row refuses it as a `Typed`
  step (D1180).

## G7. `fugaro rigor floor`, with the typed project name (D1170). **SEC**

- **Interface.** The floor's removal and lowering go through `confirmTypedStrict`
  (`internal/cli/budget.go:250`) with **no `--yes`** standing in (D846, SD10), and the agent-session
  fence comes first — so a managed project's gate never sees the step (D1026).
- **Red step.** `TestRigorFloorRemovalTakesTheProjectNameTyped` — a `--yes` on a removal is **refused**
  with the typed prompt, which fails today because the command does not exist — then
  `TestRigorFloorRefusesAnAgentSession` and `TestRigorFloorAddsAnEntryWithoutTheTypedForm`.
- **Files (≤6).** `internal/cli/rigor.go` (shared with G6), its own test file, and the local-config
  editor call.
- **SEC**: it is the one command here whose step is never admitted under the gate, and its refusal is
  the reason.

## G8. `fugaro secrets rm` and the secret policy flags (D1171). **SEC**

- **Interface.** `fugaro secrets rm NAME` deletes the Secret Manager version and the config entry, and
  the family gains the flags for the policy keys that name it
  (`internal/settings/registry.go:644`, `:684`), each through `applySettingsEdit` with the widening
  half classified (D1165). **No row is added for a secret's value** — it has none and never appears in a
  diff; `secrets set`'s hidden prompt (`internal/cli/secrets.go:276`) is unchanged.
- **Red step.** `TestSecretsRmRemovesTheVersionAndTheEntry` — fails today because the subcommand does not
  exist. Then `TestSecretsRmRefusesAnAgentSession`, `TestSecretsSetProxyIsATypedWidening` and, in
  `TestSecretsNoValueEverAppearsInADiff`, the property that no dry run of any write in this family
  prints a secret's value.
- **Files (≤8).** `internal/cli/secrets.go`, a new `internal/cli/secrets_rm.go` if the family's file is
  already large, the schema (G2's optional members for the operation), and the tests. Hermetic:
  `internal/gcpfake` for the Secret Manager half.
- **SEC.** The whole task. Its red step is written before its first success, and its refusals are read
  line by line.

## G9. `fugaro accounts rm`, and `allow_data_to` (D1172). **SEC**

- **Interface.** `fugaro accounts rm NAME` removes the account's definition and its
  `claude_accounts.*.allow_data_to` entry (`internal/settings/registry.go:627`), refusing while any
  profile or repository still names the account, and naming the referrer it found rather than the key.
  `add` with a widening `allow_data_to` stays refused in an agent's session (D868).
- **Red step.** `TestAccountsRmRefusesWhileAProfileReferencesTheAccount` — fails today because the
  subcommand does not exist — then `TestAccountsRmRemovesTheDefinitionAndItsAllowListEntry`,
  `TestAccountsRmRefusesAnAgentSession` and `TestAccountsAddWideningAllowDataToIsRefusedInASession`.
- **Files (≤8).** `internal/cli/accounts.go`, `internal/cli/accounts_add.go`, a new
  `internal/cli/accounts_rm.go` if needed, the schema members, and the tests.
- **SEC.** `allow_data_to` is the brief's own named surface.

## G10. `fugaro project repos set` (D1173). **SEC**

- **Interface.** The `repos.*` scalars and lists (`internal/settings/registry.go:701`) go to the generic
  writer; `fugaro project repos set` carries the two shapes a scalar cannot express — a `job_profiles.*`
  block (`:647`, seven keys) and a repository's workflow selection — both object lists addressed by
  name (D1163). A separate `job` family is refused by D1173.
- **Red step.** `TestProjectReposSetWritesAJobProfileBlock` — fails today because the subcommand does not
  exist — then `TestProjectReposSetWritesAWorkflowSelection`,
  `TestProjectReposSetRefusesAnUnknownProfile` and `TestSettingsSetReposScalarWritesTheReposBlock`.
- **Files (≤10).** `internal/cli/project_cmd.go`, a new `internal/cli/project_repos_set.go`,
  `internal/settings/registry.go` (rows), and the tests.
- **SEC**: a job profile carries `secrets` and `allow_repos`, which is the fence the gate judges.

## G11. `fugaro image add --image REF` (D1174). **SEC**

- **Interface.** The flag writes a `config.SourceImage` (`internal/config/catalog.go:69`) through the
  path `add` already uses (`internal/cli/image_catalog.go:295`), refusing `--image` together with
  `--dockerfile` / `--inline` rather than resolving one.
- **Red step.** `TestImageAddPrebuiltWritesASourceImage` — fails today because the flag does not exist —
  then `TestImageAddRefusesImageWithDockerfile` and `TestImageLsShowsThePrebuiltEntry`.
- **Files (≤5).** `internal/cli/image_catalog.go`, its test, and the catalog test.
- **SEC.** The catalog entry is what every job's image comes from: the value this flag writes is the
  image a run executes.

## G12. The registry's owner column (D1178, D1179)

- **Interface.** The settings read resolves an owner through a **table of its own** instead of
  `settingsFamilyCommands` (`internal/cli/settings_set.go:43`), which answers the write's refusal and
  cannot be widened without refusing `max_parallel` and `update_check` (F11); the read's table names
  `fugaro settings set` for the default owner, and a command that exists or a sentence naming
  `fugaro setup` / `fugaro update` for every family. D1179's other half — the two budget keys the
  allowlist gains — is G0's, not this task's. The read path
  (`internal/cli/settings_read.go:388`) renders a sentence where a command name was, and that is the one
  rendering decision this task makes.
- **The owner table** is written into this task's test as a fixture — `key → old owner → new owner` —
  so the diff of names is reviewable and a later row cannot change owner silently.
- **Red step.** `TestSettingsReadNamesTheGenericWriterForAGenericKey` — a key the allowlist carries,
  `budget.mode` among them, renders the command that writes it instead of an empty owner (F11, F13) —
  then `TestSettingsReadNamesSetupForAnInstallationKey`,
  `TestSettingsOwnerStringsAreAClosedVocabulary` (every non-writable key's owner is a command that
  exists or one of the setup/update sentences) and `TestSettingsOwnerChangesAreTheOnesThePlanLists`.
- **Files (≤6).** `internal/cli/settings_set.go`, `internal/cli/settings_read.go`,
  `internal/settings/registry.go` (owner strings only), and the tests.
- **Not SEC**: it renames an owner and never changes a key's meaning.

## G13. The managed gate's rows, and the installation's facts (D1180, D1181, D1182). **SEC**

- **Interface.** The spec's §5 table becomes the row set the managed build cites: one row per command and
  operation, saying what the gate admits and who refuses what it does not, with a `Typed` step never
  admitted (D1026) and the secret family refused outright. Plus the **owner table** D1182 asks for: every
  installation fact with no flag today, the converge flag it needs, and the note that a fact without one
  waits for a flag rather than for an editor.
- **Red step.** `TestDocsConfigManagementRowsCoverEveryNewCommand` — the commands this plan adds, taken
  from `settingsFamilyCommands` and the new groups, equal the rows in the spec's §5 table — which fails
  until the table is complete. Then `TestDocsConfigManagementEveryRowNamesItsRefuser` and
  `TestDocsConfigManagementInstallationFactsHaveNoWriter`.
- **Files (≤6).** `docs/config-management/spec.md` (the table's completion), `README.md` (a status line),
  `docs/config-management/implementation-plan.md` (this file's status) and the tests.
- **SEC.** It writes the document a gate's rows are read from.

## 6. Testing, and the two properties that need a test of their own

Beyond each task's red step, two properties are cross-cutting and neither belongs to one task:

- **The write-path property.** *Every* write here goes through `applySettingsEdit`
  (`internal/cli/settings_write.go:86`) — the diff, the confirmation, the fence, the backup and the
  compare-and-swap — and none of the ten commands in §5 carries a second one. The test
  (`internal/cli/docs_config_management_test.go`, `TestDocsConfigManagementNoSecondWritePath`) is a
  **pin on this plan's own words** and not a source check: these documents add no code, so what it
  asserts is that the plan states the property, names the helper, and names the same ten tasks in §5 as
  it writes headings for. Whether each command really reaches the helper is that command's own red step
  to prove when it lands.
- **The owner property.** Every non-writable key's `Owner` is a command that exists or one of the
  setup/update sentences (G12's `TestSettingsOwnerStringsAreAClosedVocabulary`). It is nobody's task in
  particular because it is a property of the registry as a whole, and it is the property the audit's
  sharpest finding (F13) was a hole in.

## 7. The recommended slot

**CLI 0.18.0**, in the same release as the managed-mode build (M0–M9) and behind it — the owner asked
for this design **built behind managed mode**, and §5 is the whole of what that means for these tasks.
Landing the two in one release is what lets the gate's rows be written once, against commands that exist,
rather than adding rows to a set whose commands land a release later.

A later slot costs more than a later date, for one reason and one only: **the configuration a person
cannot manage today is the configuration an agent's session cannot touch at all**, so every release
between this one and that one leaves the three documents of the audit (F6, F7, F13) in the state the
owner's ruling is about — a repository's `workflows.*` with no writer, a layer file published by a second
command, and two budget keys whose registry row names the generic writer while its own allowlist refuses
them.
