# The design: managing every configuration, behind the generic writer and the managed gate

**Status: a design, and nothing is built.** Every decision in this document is **open with a
recommendation**, written to be answered by the owner. **Where this file and
[reconciliation.md](reconciliation.md) disagree, the reconciliation wins.**

The owner's ruling of 2026-11-03 is the whole of the requirements: *"not to touch any of the
configuration files by hand, be it repo, project, or local level, meaning that we should have
management commands in Fugaro just like with settings."* The design is built **behind managed mode**:
every new command carries a row in the managed gate's command set
([managed spec](../managed-agent-converge/spec.md) §3.1), and **D1026** — a `Typed` step is never
admitted by a plan gate ([managed reconciliation](../managed-agent-converge/reconciliation.md:223)) —
is inherited unchanged (§6).

## 1. What "managed" means for a configuration

A configuration that is **managed** is one a command can read, edit and publish where a person today
edits a file by hand. Three properties define it, and each is a rule of this design:

1. **Every key has a writer.** The registry's `Owner` column
   (`internal/settings/registry.go:86`) names the command family that writes a key;
   a key whose owner is not a command that exists, or a sentence saying `setup` or `update` owns it, is
   the defect this design closes (§5).
2. **Every shape has a mechanism.** The generic writer handles scalars and string lists today
   (`internal/cli/settings_set.go:68`); a map entry, an object-list entry and a
   concrete spelling of a wildcard key have none. §2 gives them one, in the generic path, and a
   dedicated command exists only where a rule differs (§4).
3. **Nothing is published by a second command.** The HARD RULE in `CLAUDE.md` — `fugaro update` is the
   whole upgrade — means an edit that must be published to reach a run is published by the same run's
   `update`, not by a follow-up `fugaro config publish`. §4.6 folds the layer file's publish into the
   edit and into `update`.

## 2. The generic writer, extended first

**Recommendation: extend `fugaro settings set|unset` before any dedicated command is written** (D1160).
One mechanism closes gap 1 of the audit (a repository's `workflows.*`, 37 keys) and most of gap 2 (the
project layer file's entries, D1175), and the dedicated commands of §4 are then only the ones whose
*rules* differ, not the ones whose *shape* does.

### 2.1 A concrete key addresses a wildcard row

The registry spells a wildcard key `workflows.*.commands.test`
(`internal/settings/registry.go:223`). `settingsWriteLookup`
(`internal/cli/settings_set.go:328`) reaches a wildcard row only for a spelling
`settingsGenericKeys` names, so a concrete key a person would type — `workflows.web.commands.test` —
resolves to no row and is refused.

**Recommendation (D1161): a concrete key matches the wildcard row whose pattern it fits.** The
resolution is the settings read's own rule, reused rather than re-derived:
`settingsPatternMatch` (`internal/cli/settings_set.go:351`) already matches a
pattern against a concrete key. The write path uses it to find the row, keeps the concrete key in the
document it prints and the diff it shows, and the registry row supplies the layers, the default, the
merge rule and the owner. The alternative — requiring the literal `*` — costs nothing to write and
makes the command unusable by a person, which is the whole point of the ruling.

### 2.2 `--add-entry` and `--remove-entry`

`settings set --add` and `--remove` already add and remove **items of a string-list key**
(`internal/cli/settings_set.go:121`, `:1005`), through `yamledit.AddListItem` and
`yamledit.RemoveListItem` (`internal/yamledit/yamledit.go:234`, `:258`). They do not
reach a **map** (`workflows.*.cache.*.key`, `providers.*.models`) or an **object list**
(`workflows.*.cache` is a sequence of mappings, `workflows.*.secrets` a sequence of
`{name, env}` — `internal/config/config.go:822`, `:828`).

**Recommendation (D1162): one new pair, `--add-entry` / `--remove-entry`, for maps and object lists.**
`--add` stays the string-list flag it is, and the new pair carries the entry's shape:

- for a **map**, `--add-entry key=value` writes one member, `--remove-entry key` removes it;
- for an **object list**, `--add-entry` takes the entry's key field and its fields, and
  `--remove-entry` takes the key field's value, both resolved by
  `yamledit.UpsertSeqItemByKey` / `RemoveSeqItemByKey`
  (`internal/yamledit/yamledit.go:294`, `:351`).

**Recommendation (D1163): where an object list has a key field, the entry is addressed by it and never
by position.** `UpsertSeqItemByKey` already refuses to rewrite the key field, and
`RemoveSeqItemByKey` removes one of a duplicate key by name rather than by index
(`internal/yamledit/yamledit_test.go:809`) — an index is a fact about the file's
order, and the file's order is not the person's intent. The **key field** of each object list is the
registry row's own convention, decided per block in the plan (a `cache` entry by `key`, a `secrets`
entry by `name`), because the registry names leaves and not blocks.

### 2.3 The dry run, the confirmation, the backup and the compare-and-swap

**Recommendation (D1166): an entry write is the same edit as any other.** It is classified by the same
`settingsTypedWrite` call (`internal/cli/settings_set.go:886`), printed by the
same dry-run document (`internal/cli/settings_set.go:1211`), confirmed through
the same `applySettingsEdit` (`internal/cli/settings_write.go:86`), backed up
by the same `backupFile` (`internal/cli/init.go:2602`) and refused when the file moved by
the same byte compare-and-swap (`internal/cli/settings_write.go:150`). There is
**no ETag** on this path today and this design adds none: the settings writer's guard is the re-read, and
the ETag belongs to the cloud writers (`internal/cli/budget.go:1098`, `internal/cli/project_cmd.go:450`). A second guard here
would be a second contract for the same document.

**Recommendation (D1165): the widening classification is unchanged, and the typed project name is still
demanded for a widening.** `settingsTypedWrite` decides whether an edit loosens a ceiling, removes a
floor or turns a `followup.*` key on; the agent-session fence comes first, and `--yes` answers a widening
only outside a coding agent's session (**D846**, [settings reconciliation](../settings-commands/reconciliation.md:218);
**D1107**). An entry write that adds a `secrets`
member or an `allow_data_to` value is a widening by the same rule and takes the same typed name — and, in
a managed project, is refused by the gate because a `Typed` step is never admitted (§6).

### 2.4 The settings-write document moves to a new version

**Recommendation (D1164): `schemas/settings-write.schema.json` gains a version 2, with members added and
none changed.** The document's version is its own contract (**D848**,
`schemas/settings-write.schema.json:20`, whose version is `1` today).
An entry write's document needs members the version-1 shape does not carry — the operation (`add-entry`,
`remove-entry`, `add`, `remove`, `set`), the entry's key field and value, and the members the entry
carries — and none of them changes a member the console reads. **The console's settings edit keeps
working unchanged** (**D1164**'s second half): it reads the version-1 members (`changed`, `widening`,
`why`, `old`, `new`) and a version-2 document still carries every one of them with the same meaning, so
the console spawns `settings set … --yes` behind its own dialog exactly as
[docs/console-settings-edit](../console-settings-edit/README.md) settled, and this design edits no
console file.

## 3. Which dedicated commands remain, and their rules

**Recommendation (D1168): a dedicated command exists only where its rules differ from the generic
writer's.** The test, stated so a later key is decided rather than argued: **the generic writer is the
answer unless the write needs a rule the generic path does not have** — a removal that needs the typed
project name and nothing stands in for it, a value the diff must never print, a check against a cloud
document before the write, or a block whose members are addressed by a key the registry does not name.
The commands below pass that test; every other key of the audit is closed by §2.

### 3.1 `fugaro rigor set` and `fugaro rigor floor` (D1169, D1170)

The registry names `rigor` and `rigor floor` as the owners of seven keys
(`internal/settings/registry.go:266`, `:273`, `:445`, `:682`), and **no `rigor`
command exists**. But the commands are **already designed**, and this design **builds** them rather than
re-designing them: `fugaro rigor set` in [docs/rigor-command](../rigor-command/README.md) (D830–D839,
D900), and `fugaro rigor floor` in the settings design (SD10,
[docs/settings-commands](../settings-commands/README.md)). **Recommendation: the plan's rigor task is the
one that lands them, and it takes their rules from those designs** — the rank rule (at or above the
resolved mode), **D834**'s `hotfix` refusal, **D838**'s duplicate-glob refusal.

**Recommendation (D1170): removing or lowering a `review_floor` entry takes the project's name typed at
a terminal (D846, SD10).** `confirmTypedStrict` (`internal/cli/budget.go:250`) is that
form: no `--yes` stands in for it, and the agent-session fence comes first, so in a managed project the
removal stays agent-refused even though the gate would otherwise admit a narrowing — because a floor
removal is not a plan the gate can read (D1026).

### 3.2 `fugaro secrets rm`, and the secret policy flags. **SEC**

`fugaro secrets` has `set` and `ls` and **no `rm`** (`internal/cli/secrets.go:41`); its
`set` takes no flag for the seven policy keys the registry names it owner of —
`secret_expires`, `secret_rotation_days`, `project_secrets.*.allow_data_to`, `project_secrets.*.proxy`,
`batch.secrets`, `bitbucket_api_roots`, `mailbox.keychain_item`, `verifier_accounts`
(`internal/settings/registry.go:567`, `:644`, `:684`).

**Recommendation (D1171): `fugaro secrets rm NAME` deletes the Secret Manager version and the config
entry, and the policy flags are added to the family as the settings write path's own rules cannot carry
them.** The secret **value** is never a setting and never a diff (§6, and
[managed spec](../managed-agent-converge/spec.md) §3.2): `rm` and the flags write the
**policy**, which the generic writer could reach but whose `allow_data_to`/`proxy` members are a widening
the gate must judge. `rm` is **never admitted by the managed gate**: a removal of a secret is a value
no plan shows (§6).

### 3.3 `fugaro accounts rm`, and `allow_data_to` (D1172)

`fugaro accounts` has `ls`, `add` and `clear` and **no `rm`**
(`internal/cli/accounts.go:25`, `internal/cli/accounts_add.go:41`).
`clear` writes a project-DB mark and no config. The registry names `accounts` the owner of
`claude_accounts.*.allow_data_to` (`internal/settings/registry.go:627`), which `add`
writes and **nothing can remove**.

**Recommendation: `fugaro accounts rm NAME` removes the account definition and its allow-list entry, and
is admitted by the managed gate only when no profile and no repository still references it** (§6;
[managed spec](../managed-agent-converge/spec.md) §4.2). `add` with a widening `allow_data_to` is a
widening (D868) and stays refused in a coding agent's session.

### 3.4 `project`'s job profiles and the `repos.*` fields (D1173)

`fugaro project repos` has `add`, `ls` and `rm` (`internal/cli/project_cmd.go:65`);
nothing edits `job_profiles.*` (7 keys, `internal/settings/registry.go:647`) or a
`repos.*` field other than by enrolling a repository (`repos.*.base_branch`, `.provider`, `.vertex`,
`.workflows`, `.github_app_id`, `internal/settings/registry.go:701`).

**Recommendation: the generic writer takes the `repos.*` scalars and lists** (they are a map of scalars,
which §2 reaches), **and `fugaro project repos set` is added for the two facts a bare scalar cannot
express** — a job profile's block and a repository's `workflows` selection, which are object lists
addressed by name. The alternative — a `fugaro project job` family — is a second command group for keys
the generic writer already reaches; the design refuses it and says so in the plan.

### 3.5 `fugaro image add --image` (prebuilt) (D1174)

`fugaro image add <name>` writes only a Dockerfile or inline source
(`internal/cli/image_catalog.go:295`): it has **no `--image` flag**, so a
**prebuilt** catalog entry — a `config.SourceImage` (`internal/config/catalog.go:69`)
— can be created only by a hand edit, even though the selection and the run already consume it.

**Recommendation: `fugaro image add` gains `--image REF`**, which writes a prebuilt source, exactly as
`--dockerfile` writes a built one. The refusal a repository that also names a catalog entry gets
(**D1155**) is unchanged, and the entry is written through the same `image_edit` path `add` uses.

### 3.6 The project layer file, with publish integrated (D1175, D1176)

The project layer file (`internal/localcfg/localcfg.go:1120`) is today edited by hand
and published by a second command. `fugaro config publish`
(`internal/cli/config_publish.go:27`) reads it, refuses a reserved key, records
the generation (`internal/cli/config_publish.go:255`) and writes the object.

**Recommendation (D1175): every layer entry is a managed entry with a writer** — `profiles.*` (36),
`defaults.*` (agent, git, rigor), `ab.*` (5) and `claude_models` — reached by the generic writer's §2
mechanism. The layer's rows already exist in the registry
(`internal/settings/registry.go:411`), so the change is the allowlist and the entry
mechanism, not a new registry.

**Recommendation (D1176): the edit's own next step publishes.** `fugaro settings set --layer project`
already ends with `next: fugaro update` (`internal/cli/settings_set.go:1070`); the
HARD RULE is that `update` publishes the layer, not that a person runs `fugaro config publish` after it.
`fugaro config publish` therefore **stays** as the operator's own door (and is the door the agent runs
under the gate, [managed spec](../managed-agent-converge/spec.md) §4.2), but no *edit* here requires it:
the edit and the publish are one run's, and the plan names the task that makes `update`'s Layer stage
publish a file every writer here has just written (D831's own task, inherited).

### 3.7 A repository's `workflows.*`, and what `setup` and `config init` write (D1177)

A repository's `fugaro.yaml` `workflows.*` block (37 registry keys,
`internal/config/config.go:572`) has **no writer**: `fugaro config init` writes a
minimal file (`internal/cli/config_init.go:84`) and the setup skill tells a user's
agent to edit the YAML.

**Recommendation: `fugaro workflow add NAME` creates a workflow block, `settings set` with a concrete key
(§2.1) sets its scalars and lists, `--add-entry`/`--remove-entry` (§2.2) set its maps and object lists,
and `fugaro workflow rm NAME` removes the block.** `config init`'s minimal file stays minimal — it is the
file a repository is *anchored* with, not the file a workflow is *defined* in — and `setup` writes what
it writes today. The refusal of an executable-key change without the owner's flag (`image.apt`,
`image.setup`, `commands.*`, `internal/cli/config_publish.go:309`) applies to a
workflow edit exactly as it applies to a layer publish.

## 4. The registry's owner column

**Recommendation (D1178): every non-writable key names a command that exists, or a sentence saying
`setup` or `update` owns it.** The column is read by the settings read
(`internal/cli/settings_read.go:388`) through `settingsFamilyCommands`
(`internal/cli/settings_set.go:43`), and **that map is missing four families** —
`budget`, `setup`, `update`, `config` — so every key they own prints an empty owner today. The fix has two
halves and both are mechanical:

- **`settingsFamilyCommands` gains the missing families**, each naming an existing command:
  `budget → fugaro budget set`, and the installation families → a sentence that names `fugaro setup` or
  `fugaro update` rather than a command that would be read as an editor.
- **Every key that changes owner is listed**, in the plan's own table (§ plan, G10), so the change is
  reviewable as a diff of names and nothing else.

**Recommendation (D1179): `budget.mode`, `budget.per_day_usd`, `budget.per_run_usd` and
`budget.allowed_models` are owned by `fugaro budget set`, and `compute_prices.*` by `fugaro budget
prices`.** The four budget keys are the caps `budget set` already writes into the budget database
(`internal/cli/budget.go:921`) and today default to the owner `settings set`
(`internal/settings/registry.go:324`); `compute_prices` is a price table that
`fugaro budget prices` (`internal/cli/budget.go:1508`) reads and that is a deliberate
choice with no editor today (`internal/settings/registry.go:595`). **No key's meaning
changes** — only the command the column names — so this is a rename of the owner and never a re-meaning
of a key.

## 5. The managed gate's rows

**Recommendation (D1180): every new command gets a row in the managed gate's set, and the rows follow
D1026.** In a managed project
([managed spec](../managed-agent-converge/spec.md) §3.1), the gate admits a command whose whole effect
is visible in a document it produces before it writes; the rows are:

| New command or operation | What the managed gate admits | Refused, and who refuses it |
| --- | --- | --- |
| `settings set\|unset` with a **concrete** key (§2.1) | Admitted for a key the registry knows at a scope inside the project, where the value lowers no floor and widens no ceiling | A widening that takes the typed project name — **D1026**, a `Typed` step is never admitted |
| `settings set\|unset` with `--add-entry` / `--remove-entry` (§2.2) | Admitted when the entry adds no `allow_data_to` member and no `secrets` member | A `secrets` entry and an `allow_data_to` widening: refused by the widening rule, not by the gate's plan |
| `fugaro workflow add\|rm` (§3.7) | Admitted as a `--layer repo` write, the one document an agent session may write today | An executable-key change (`image.apt`, `image.setup`, `commands.*`) without the owner's flag |
| `fugaro rigor set` (§3.1) | Admitted for a mode at or above the resolved mode | `hotfix` (D834), a duplicate glob (D838), and a floor **lowered or removed** — the typed name, D1026 |
| `fugaro accounts rm` (§3.3) | Admitted only when no profile and no repository references the account | Any token entry at all, and `allow_data_to` widened |
| `fugaro secrets rm`, the secret policy flags (§3.2) | **Nothing** — a secret's removal and its policy are values no plan shows | The whole family: refused in a session, gate or not |
| `fugaro project repos set`, `fugaro image add --image` | Admitted within the plan's caps, on the same rules the layer and the catalog already carry | A reserved key, an executable-key change, and an IAM widening |

**Recommendation (D1181): the agent-session refusal stays everywhere outside the gate.** No task here
removes a refusal in a coding agent's session; a command is runnable by an agent only when
`internal/initflow`'s `CanConfirmManaged` (`internal/initflow/stage.go:580`) returns true
under the mark, and the mark's write path is the owner's alone
([managed reconciliation](../managed-agent-converge/reconciliation.md) F4).

## 6. What stays a setup/update-owned installation fact

**Recommendation (D1182): the installation's facts are never hand-edited and never managed by a command
here.** About seventy keys are `setup`'s and `update`'s — `endpoints.*` (22),
`terraform.*`, `batch.*`, `build.*`, `gcp_project`, `region`, `runs_bucket`, `registry`,
`policy_generation`, `layer_generation`
(`internal/settings/registry.go:535`, `:571`, `:581`, `:585`, `:659`, `:545`). They
are not a person's edit; they are the output of the converge. Two of them look like deliberate choices
with no command today — `terraform.alert_email` (`internal/settings/registry.go:586`)
and `batch.machine_type` (`internal/settings/registry.go:572`):

- **A deliberate change is made by the flag that feeds the converge, not by an edit of the file.** The
  converge already takes `--alert-email` (`internal/cli/init.go:368`) and the batch
  machine is the design's own default; the recommendation is that a fact a person must choose gets a
  **flag on `fugaro setup` or `fugaro update`**, reading the same local file it writes today, and the
  registry row's owner says `fugaro setup` or `fugaro update` and nothing else. A hand edit of a file the
  next `update` overwrites is the state this design removes.

## 7. What this design is not

- **It is not a compatibility or skew design.** There are no production users; a shape that must change
  changes directly, and the settings-write schema's version is the one place a change is versioned
  (D1164), because the console reads it (D848).
- **It does not re-open the managed gate's decisions.** D1011–D1026 are cited; a new command gets a row
  (§5) and no decision there is rewritten.
- **It does not give the console an edit of its own.** The console keeps spawning `settings set|unset`
  behind its own dialog, unchanged (D1164).
- **It does not make a secret's value a setting.** `secrets set` keeps its hidden prompt, and no diff
  ever prints a secret (D1171).
- **It does not settle which key field each object list is addressed by** in prose: the registry names
  leaves, so the plan's G-tasks decide each block's key field and pin it with a test.
