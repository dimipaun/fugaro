# Reconciliation: the audit, the corrections, the decisions

**Status: open. Nothing here is approved, and no task in
[implementation-plan.md](implementation-plan.md) may start until the owner answers §4.** This document
holds every claim [spec.md](spec.md) makes against the tree: the audit it rests on (§1), the places the
brief's own reading of the tree was wrong (§2), the number block (§3), the decisions **D1160–D1183**
(§4) and the residual this design refuses to hide (§5). **Where this file and the spec disagree, this
file wins.**

## 1. The audit, against the tree

**How it was taken.** Grep first, then read: the registry whole (`internal/settings/registry.go`, 752
lines), the write path line by line (`internal/cli/settings_set.go`,
`internal/cli/settings_write.go`), the editors (`internal/yamledit/yamledit.go`), the three documents
a person still hand-edits (a repository's `fugaro.yaml`, the project layer file, the local project
config) and the command files that would own them. The key listing was regenerated rather than
remembered — `go run ./cmd/fugaro settings --json` — which **refuses to run on a machine with no
project config** — a sentence it prints and exits on — so the figures below
are counted from the registry in this tree through the same predicate the write path uses, which is
what the guard at the end of this section re-runs.

- **F1 — the registry is 374 rows and the generic writer carries 164 of them.** 88 by name
  (`internal/cli/settings_set.go:81`), made of the thirteen §4.4 names (the four budget scalars,
  `base_branch`, `max_parallel`, `update_check`, `commit_name`, `commit_email`, `layer.default_profile`
  and D873's three account keys), the two budget keys D1179 hands this path, and the wildcard blocks
  D1161 gives it — a repository's 37 `workflows.*` rows and the project layer's 36 `profiles.*` ones;
  the layer's five `ab.*` rows are listed beside them, but the models family owns them, so this count
  does not carry them — and 76 through the nine prefixes
  (`internal/cli/settings_set.go:195`: `agent.`, `git.`, `followup.`, `governance.`, `watch.`,
  `layer.defaults.`, `layer.governance.`, `credentials.`, `clone_urls.`). **210 rows have no generic
  writer**, and the audit's whole subject is which of them are a defect and which are the
  installation's own plumbing (F13, D1182).
- **F2 — `fugaro rigor` does not exist.** There is no `newRigorCmd` in `internal/cli`, no `rigor` cobra
  command and no `floor` subcommand, while the registry names `rigor` (`internal/settings/registry.go:266`)
  and `rigor floor` (`:682`) as the owners of keys. Two of the registry's owner strings name a command a
  person cannot run.
- **F3 — the rigor keys it would own are seven.** `rigor.default` (`internal/settings/registry.go:266`),
  `rigor.areas` (`:273`), the four `layer.defaults.rigor.*` rows (`:445`: `mode`, `area_mode`, and the
  two glob lists) and `governance.review_floor_calls`' neighbour `review_floor` (`:682`). They are the
  local project config and the layer file, and both are hand-edited.
- **F4 — `secrets` has no `rm` and no policy flags.** The family is `set` and `ls`
  (`internal/cli/secrets.go:41`), and its `set` takes a name and a value. The seven policy keys that
  name it as owner — `secret_expires`, `secret_rotation_days`, `project_secrets.*.allow_data_to`,
  `project_secrets.*.proxy` (`internal/settings/registry.go:644`), `batch.secrets`, `bitbucket_api_roots`,
  `mailbox.keychain_item`, `verifier_accounts` (`:684`) — have no flag and no command.
- **F5 — `accounts` has no `rm`.** The family is `ls`, `add` (`internal/cli/accounts_add.go:41`) and
  `clear` (`internal/cli/accounts.go:25`), and `clear` writes a project-database mark rather than a
  config. `claude_accounts.*.allow_data_to` (`internal/settings/registry.go:627`) is written by `add`
  and removable by nothing.
- **F6 — a repository's `workflows.*` has no writer at all.** 37 keys
  (`internal/config/scope.go:102`), the registry rows at `internal/settings/registry.go:223`, the struct
  at `internal/config/config.go:572`, its blocks at `:627` — `commands` (`:792`), `cache` (`:822`),
  `secrets` (`:828`), `resources` (`:879`), `timeouts` (`:1005`). `fugaro config init` writes a
  four-line file (`internal/cli/config_init.go:84`) and the setup skill writes the rest **with the
  user**, in the user's own agent session (`plugin/skills/setup/SKILL.md:13`).
- **F7 — the project layer file is hand-edited and published by a second command.** The file is
  `<config home>/fugaro/projects/<name>.layer.yaml` (`internal/localcfg/localcfg.go:1120`), its rows are
  `layerRows` (`internal/settings/registry.go:411`), and `fugaro config publish`
  (`internal/cli/config_publish.go:27`) is the command that reads it, checks the generation
  (`:255`) and writes the object. Two steps where the owner's ruling wants one.
- **F8 — the editors for the missing shapes already exist and the write path does not call them.**
  `yamledit.UpsertSeqItemByKey` and `RemoveSeqItemByKey` (`internal/yamledit/yamledit.go:294`, `:351`)
  address an object-list entry by its key field, and the write path's dispatch
  (`internal/cli/settings_set.go:1107`) reaches only `SetScalar`, `AddListItem` and `RemoveListItem`.
  The gap is one dispatch and one flag pair, not a new editor.
- **F9 — the settings-write document is version 1 with ten required members, and the console reads it.**
  `schemas/settings-write.schema.json:20` pins `"const": 1`, and `version`, `key`, `layer`, `path`,
  `changed`, `widening`, `why`, `old`, `new`, `refusal` are required. Its own description carries the
  rule this design works under: **members are only added within a version (D848)**. The console's
  settings edit is the consumer ([docs/console-settings-edit](../console-settings-edit/README.md)).
- **F10 — a prebuilt image entry cannot be created by a command.** `fugaro image add <name>`
  (`internal/cli/image_catalog.go:295`) takes `--dockerfile`, `--inline`, `--tags`, `--branch`,
  `--description`, `--default` and **no `--image`**, while `config.SourceImage`
  (`internal/config/catalog.go:69`) is a source a run already consumes and the rows name the `project`
  family (`internal/settings/registry.go:690`).
- **F11 — the owner column is read through the map the write path refuses with, and that map is missing
  three families.** `settingsFamilyCommands` (`internal/cli/settings_set.go:43`) maps `models`, `rigor`,
  `rigor floor`, `accounts`, `secrets` and `project`; `setup`, `update` and `config` are absent, so the
  rows they own read with no family beside them (`internal/cli/settings_read.go:388`). The read is not
  the only caller: the same map is what refuses a write and points the key at its family
  (`internal/cli/settings_set.go:402`). So a family cannot simply be added — `config` owns `max_parallel`
  (`internal/settings/registry.go:560`) and `setup` owns `update_check` (`:566`), and **both keys are in
  the allowlist and are written by the generic path today**, so an entry for either family would refuse
  two keys `settings set` writes. The map also carries no entry for the default owner
  (`internal/settings/registry.go:324`), so the column is empty for every key the generic path writes,
  which is the shape F13's two rows are read through. D1178 decides what the read resolves an owner
  through instead of widening a map that answers a second question.
- **F12 — a concrete key reaches a wildcard row only when the allowlist names that row.**
  `settingsWriteLookup` (`internal/cli/settings_set.go:430`) matches `settingsPatternMatch` (`:453`)
  across the registry's wildcard rows, bounded to the rows `settingsGenericKeys` carries by name — which
  is how `stages.*.claude_account` (`:99`) is reachable, and why a wildcard row the allowlist does not
  carry stays unreachable however it is spelled. The literal `*` spelling keeps its own refusal
  (`internal/cli/settings_set.go:406`). **D1161 is the decision that widens the named rows**: to a
  repository's `workflows.*` (`internal/settings/registry.go:223`, the row this finding was written
  about) and the layer's `profiles.*`.
- **F13 — `budget.mode` and `budget.per_day_usd` name no owner and default to `settings set`, and the
  generic path refused them on purpose.** The rows are `internal/settings/registry.go:210` and `:213`,
  the default owner is `:324`, and the allowlist's own comment said so in as many words until D1179
  rewrote it (`internal/cli/settings_set.go:65`). So they were **unwritable and owned by a command that
  will not write them**: the sharpest single defect of the audit. The other two keys of the same block —
  `budget.per_run_usd` and `budget.allowed_models` — *are* in the allowlist
  (`internal/cli/settings_set.go:81`), so the four rows of a repository's own budget block were split
  across two answers for no reason a reader can see; D1179 makes them one.
- **F14 — the census row this folder moves is a running total of other folders' guards.**
  `docs/rigor-modes/reconciliation.md:732` counts the folders that ship their own
  `internal/cli/docs_*_test.go` and reads **92**; this folder's pin makes it **93**, and the row says in
  its own text that a folder writing one moves it in its own commit.

**The guard.** These figures are counted by the predicate the write path uses, and the test this folder
ships re-counts the one that matters (`internal/cli/docs_config_management_test.go`,
`TestDocsConfigManagementAllowlistCounts`): a registry that grew a row, or an allowlist that grew a key, fails rather than leaving
this section reading as checked.

## 2. Where the brief's own reading was wrong

The brief asked this design to reconcile against the code and to say so when the code disagrees. Eight
of its assumptions do; each is corrected here and the correction is what the decisions in §4 are built
on.

- **C1 — there is no ETag on the settings write path.** The brief asks for "the same … ETag". The guard
  is a **byte compare-and-swap**: the file is re-read before the write and a moved file is refused with
  nothing written (`internal/cli/settings_write.go:150`, the re-read at `:146`). An ETag belongs to the
  cloud writers — the budget database's `GetETag` (`internal/cli/budget.go:1098`) and the published
  policy node's (`internal/cli/project_cmd.go:450`). A second guard here would be a second contract for
  one document, so D1166 keeps the one that exists.
- **C2 — `--add` and `--remove` already exist.** The brief asks for them as new. They are on the shared
  option struct (`internal/cli/settings_set.go:221`, `:222`) and they write **string-list** items
  through `yamledit.AddListItem` / `RemoveListItem` (`:1005`). What does not exist is an entry write for
  a map or an object list, which is what D1162 adds and what F8 shows the editor already supports.
- **C3 — the wildcard resolver already exists.** The brief reads the wildcard gap as a parsing gap. It
  is an allowlist gap: `settingsWriteLookup` (`internal/cli/settings_set.go:430`) already resolves a
  concrete key against a wildcard row, for the rows the allowlist names (F12). Nothing in the resolver
  changes; the decision is which rows the allowlist gains (D1161).
- **C4 — `budget.mode` and `budget.per_day_usd` do not carry an empty owner.** Their rows store
  `settings set`, the registry's default owner (`internal/settings/registry.go:324`). What reads as an
  empty owner is the *rendering*: the column is `settingsFamilyCommands[row.Owner]`
  (`internal/cli/settings_read.go:388`) and that map has no entry for the default owner, so it is empty
  for every key the generic path writes (F11). The hole the brief describes is real; its cause is F11
  beside F13, not a row whose owner string is missing.
- **C5 — `compute_prices` already names an owner.** The brief lists it as a key that "needs one"; the
  rows name `setup` (`internal/settings/registry.go:595`). Its real state is worse and more interesting:
  it is a **local-config map with its own validation** (`internal/localcfg/localcfg.go:118`, the region
  and price checks at `:1652`) whose only editor is a hand edit, and the published-config design treats
  it as a mergeable map ([docs/design/shared-config.md](../design/shared-config.md)). D1179 decides it
  on that fact rather than on a missing owner string.
- **C6 — the managed marker's next version is 3, not a "V4", and this design writes no marker at all.**
  The brief asks this design for a new mark version. The version beside `MarkerVersion = 1` and
  `MarkerVersionBudget = 2` (`internal/infra/managed/marker.go:58`) is 3, not a "V4", and it is the
  **managed build's** — that plan's M5 adds the constant and its field, and the marker's read is what
  learns it. Nothing in this design bumps a marker version and no task here touches the mark: §1 of this
  folder's plan says **no task in it edits `internal/managed/`**, and the mark's write path stays the
  owner's alone (D1181). A task that changed a marker version would be a managed-build task, not one of
  G0–G13.
- **C7 — `job_profiles.*` and `image_catalog.entries.*` are already `project`-family keys.** The rows
  say so (`internal/settings/registry.go:647`, `:690`), so the two commands the brief asks for extend a
  family that exists rather than creating one, and they are refused by the generic path today for
  exactly that reason (`internal/cli/settings_set.go:402`).
- **C8 — the secret policy keys are already `secrets`-family keys** (`internal/settings/registry.go:644`),
  so the flags of D1171 extend `secrets` and do not need a new owner row. The one thing they do need is
  the rule that a secret's **value** is never a setting: it has no row in the registry at all, and this
  design adds none.

## 3. The number block

The block was read the way this repository requires: **by fetching each branch and reading it**, not by
trusting a neighbour's table.

- **On `main`**, the highest written decision is **D1159**
  (`docs/project-image-catalog/reconciliation.md`, that work's block D1140–D1159).
- **In the open work streams**, re-checked on **2026-11-03** by fetching each branch: the highest number
  any of them writes is again **D1159**, from the same project-image-catalog folder `main` already
  carries. The open docs designs among them (`docs/workspaces`, `docs/binary-allow-rule`,
  `docs/periodic-reviews`, `docs/mcp-server`, `docs/plugin-user-scope`, `docs/run-without-checkout`,
  `docs/run-limit-carry-over`) all sit below D1027, and the managed-mode **code** branches write no new
  decision. **D1160 is free.**

**This work takes D1160–D1189: D1160–D1183 are written below, and D1184–D1189 are reserved and
unused.** Every decision is **open, with a recommendation** — the owner has answered none of them, and
the plan's tasks start only when he has. A later task that needs a number re-reads this section's facts
rather than its conclusion.

## 4. The decisions

- **D1160 (the generic writer goes first, before any dedicated command) — OPEN, recommended.**
  Recommendation: the plan's first three tasks extend `fugaro settings set|unset` — the concrete
  spelling of a wildcard key (D1161), the entry flags (D1162) and the document's version (D1164) — and
  no dedicated command is written until they have landed. Every attempt to write a dedicated command
  first pays for the same primitive twice and gives two shapes for one kind of edit.
  Cost of the alternative: writing `fugaro workflow set` against today's writer means it either carries
  its own YAML editor — the copy `internal/yamledit` exists to prevent — or blocks on exactly the three
  tasks it was meant to precede, with a worse ordering than the one recommended.
  Cost of the recommendation: the first three tasks land no user-visible command, so the plan's early
  commits are felt only by the reviewer; that is the price of one primitive instead of six.

- **D1161 (a concrete key addresses a wildcard row) — OPEN, recommended.**
  Recommendation: the allowlist gains the wildcard blocks this design manages — a repository's
  `workflows.*`, the layer's `profiles.*` and `ab.*` — and each concrete spelling resolves through the
  resolver that exists (`internal/cli/settings_set.go:430`). The row keeps the key as written,
  which is the path the edit lands on, and the registry spelling is what the allowlist is asked about —
  the shape `stages.*.claude_account` (`:84`) already has.
  Cost of the alternative: requiring the literal `*` needs no decision and makes the command unusable by
  a person, which is the ruling's whole subject. Cost of the recommendation: a wildcard row in the
  allowlist is a key whose *set* of concrete spellings nothing enumerates — a person who writes a
  workflow name the file does not hold creates a block by writing one key into it, which the plan has to
  decide and test (G4), and which a literal `*` would have avoided by refusing.

- **D1162 (one new flag pair, `--add-entry` / `--remove-entry`, for maps and object lists) — OPEN,
  recommended.**
  Recommendation: `--add` and `--remove` keep writing string-list items
  (`internal/cli/settings_set.go:1107`) and the new pair carries an entry: for a **map**,
  `--add-entry key=value` and `--remove-entry key`; for an **object list**, the entry's key field and
  its fields, resolved by `yamledit.UpsertSeqItemByKey` and `RemoveSeqItemByKey`
  (`internal/yamledit/yamledit.go:294`, `:351`).
  Cost of the alternative: overloading `--add` to mean a list item here and a map member there makes one
  flag two shapes, and a person who gets the shape wrong gets an edit on the wrong block rather than a
  refusal. Cost of the recommendation: four flags where two did, and a value syntax
  (`key=value`) the command line has nowhere else in this family — the plan names both in `--help` and
  in the dry run's document (G2).

- **D1163 (an object-list entry is addressed by its key field, never by position) — OPEN, recommended.**
  Recommendation: the key field is each block's own — a `cache` entry by `key`, a `secrets` entry by
  `name` — decided per block in the plan and tested there. `RemoveSeqItemByKey` already removes one of a
  duplicate key by name rather than by index (`internal/yamledit/yamledit_test.go:809`), and
  `UpsertSeqItemByKey` already refuses to rewrite the key field.
  Cost of the alternative: indexing an entry is a fact about the file's order, and the file's order is
  not the person's intent — a reordered list silently rewrites the wrong entry, which is the failure the
  editor's own test was written against. Cost of the recommendation: a block whose entries carry no key
  field cannot be addressed by the new pair at all, and the plan has to say so with a refusal rather
  than guess (G2).

- **D1164 (the settings-write document keeps version 1 and gains its entry members as optional ones, and
  the console keeps working unchanged) — OPEN, recommended.**
  Recommendation: `settingsWriteDocVersion` (`internal/cli/settings_set.go:1299`) and the schema's const
  (`schemas/settings-write.schema.json:20`) **stay at `1`**, and the entry write's members — `operation`,
  the entry's key field, the entry's members — are **added as optional members**, none of them required.
  The document's own description is the rule being followed: members are only ever added within a version
  (D848), and removing or retyping one bumps it. The console pins the version it reads —
  `SETTINGS_VIEW_DOC_VERSION = 1` (`console/src/main/settingsview.ts:42`) refuses any document at another
  version (`:181`) — so a version 2 would break **every** settings edit the console makes, which is the
  edit it spawns `settings set … --yes` behind; keeping version 1 and adding optional members is what
  makes "the console keeps working unchanged" true rather than hoped for. An old document that carries
  none of the new members still decodes and still draws. This design edits no console file.
  Cost of the alternative: a version bump is a version the console must be taught in the same release,
  and the console is released independently of the CLI — so the bump buys nothing here and breaks the one
  reader that exists. Cost of the recommendation: version 1 can only grow by optional members, so a
  member whose *meaning* must change takes a new version and a console edit with it, and a reader of the
  schema cannot tell a member that is always present from one only an entry write prints.

- **D1165 (an entry write is classified, confirmed and fenced exactly as a scalar write is) — OPEN,
  recommended.**
  Recommendation: the entry pair goes through the same `settingsTypedWrite` classification
  (`internal/cli/settings_set.go:988`) and the same `applySettingsEdit`
  (`internal/cli/settings_write.go:86`), so a widening is still the typed project name (**D846**,
  `docs/settings-commands/reconciliation.md:218`), `--yes` still answers it only outside a coding
  agent's session (**D1107**, `internal/cli/settings_write.go:127`) and the agent-session fence
  (`:103`) still comes first. Adding a `secrets` member or an `allow_data_to` value is a widening by the
  same rule, which is what makes D1180's rows possible at all.
  Cost of the alternative: a second classification for entries is a second place a ceiling can be
  loosened without the typed name, and the fence would have to be repeated at the new entry point — the
  copy that always drifts. Cost of the recommendation: an entry write that is plainly additive still
  pays for a diff and a confirmation, which is the point of one write path and is what the console's dry
  run already shows.

- **D1166 (the dry run, the backup and the byte compare-and-swap are the existing ones; no ETag is
  added) — OPEN, recommended.**
  Recommendation: `--dry-run` prints the one document (`internal/cli/settings_set.go:1313`), the write
  backs the file up through `backupFile` (`internal/cli/init.go:2602`) and refuses a file that moved
  through the compare-and-swap (`internal/cli/settings_write.go:150`). A field never changes meaning;
  the entry fields are members **added** to the document (D1164).
  Cost of the alternative: adding an ETag to a local file's write is a second guard for one document,
  and it would disagree with the guard every other settings write uses; the brief asked for it and the
  correction is C1. Cost of the recommendation: the guard is byte-equality, so a file that changed and
  changed back is written — a difference nobody has been able to observe in this path, and the refusal
  message tells the person to read it again rather than guessing.

- **D1167 (an entry write carries one entry, never a block of YAML on the command line) — OPEN,
  recommended.**
  Recommendation: `--add-entry` takes one entry per invocation and `--remove-entry` removes one; a
  person who needs several repeats the command, and the dry run makes the succession visible. YAML text
  pasted into a flag is a second parser between the person and the file, and the editor exists so that
  comments and order survive — a pasted block would arrive through a path that never reads them.
  Cost of the alternative: accepting a YAML document means the write path validates a document it did
  not produce, which is the class of bug (`internal/yamledit`'s whole reason) this design would be
  re-adding. Cost of the recommendation: a block with five entries takes five invocations, which is
  slower for the person and honest about what the command writes.

- **D1168 (a dedicated command exists only where its rules differ from the generic writer's) — OPEN,
  recommended.**
  Recommendation: the test is stated and applied: the generic writer is the answer unless the write needs
  a rule the generic path cannot carry — a removal the typed project name alone may confirm, a value the
  diff must never print, a check against a cloud document before the write, or a block whose members are
  addressed by a key the registry does not name. `rigor` (D1169), `secrets` (D1171), `accounts` (D1172)
  and the two `project` blocks (D1173) pass it; every other key of the audit is closed by §2 of the spec.
  Cost of the alternative: a dedicated command per block is a command per key group — six to ten new
  families, each with its own confirmation, dry run and refusal, and a person with no way to guess which
  of them owns a key. Cost of the recommendation: a person's question "where is the command for X" has
  the answer "the generic writer" for most X, which the registry's owner column has to make findable
  (D1178).

- **D1169 (`fugaro rigor set` is built from the rigor design, not designed again here) — OPEN,
  recommended.**
  Recommendation: the plan's task lands the command [docs/rigor-command](../rigor-command/README.md)
  already designed (D830–D839, D900) — the rank rule that a mode is at or above the resolved one,
  D834's `hotfix` refusal and D838's duplicate-glob refusal — and this design adds only the rows the
  managed gate needs (D1180). Its `rigor.areas` globs and `layer.defaults.rigor.*` are the layer and
  local keys the generic writer reaches once D1161 lands the wildcard rows.
  Cost of the alternative: re-designing the command here duplicates a settled design and puts two
  answers to the same question in the tree, which is how a repository grows a second convention. Cost of
  the recommendation: this folder's plan depends on another folder's design being right, so its first
  task is to re-read that design against the tree rather than to trust the citation (G7).

- **D1170 (lowering or removing a `review_floor` entry takes the project's name typed at a terminal) —
  OPEN, recommended.**
  Recommendation: `fugaro rigor floor` removes and lowers through `confirmTypedStrict`
  (`internal/cli/budget.go:250`) with no `--yes` standing in, and the agent-session fence comes first, so
  a managed project's gate never sees the step at all (D1180). Removal is not a plan a document can show:
  what it deletes is a floor, and D1026's rule — a `Typed` step is never admitted
  (`docs/managed-agent-converge/reconciliation.md:223`) — is what keeps the decision in the owner's own
  hands.
  Cost of the alternative: a `--yes`-able floor removal is a removal an agent can perform in a loop
  where each step looks additive, which is the whole class of change the typed form exists for. Cost of
  the recommendation: a person removing a stale floor at a terminal types a project name they already
  know, and a script that wants it unattended cannot have it — accepted, because that script is exactly
  what this refusal is for.

- **D1171 (`fugaro secrets rm` and the secret policy flags; a secret's value is never a setting) — OPEN,
  recommended. SEC.**
  Recommendation: `fugaro secrets rm NAME` deletes the Secret Manager version and the config entry, and
  the family gains the policy flags for the keys that name it (`internal/settings/registry.go:644`,
  `:684`) — including `project_secrets.*.allow_data_to` and `proxy`, which are a widening when they
  widen. No row for a secret's value is added: a value has no registry row and never appears in a diff,
  and a person who needs to set one still uses `secrets set`'s hidden prompt
  (`internal/cli/secrets.go:276`).
  Cost of the alternative: handing the policy keys to the generic writer alone makes `allow_data_to`
  widen through a path whose dry run prints the old and the new value, which is the shape D846 reserves
  for the typed name; and a secret with no `rm` is a secret nobody can withdraw, which is the security
  hole this task closes. Cost of the recommendation: a new destructive command in the family whose
  refusal must be written before its first success (the plan's red step), and one more command the
  managed gate has to refuse outright rather than judge.

- **D1172 (`fugaro accounts rm`, and `allow_data_to` on `add`) — OPEN, recommended. SEC.**
  Recommendation: `fugaro accounts rm NAME` removes the account's definition and its
  `claude_accounts.*.allow_data_to` entry (`internal/settings/registry.go:627`) — a removal whose
  referrers are checked rather than assumed — and that check reads differently on its two paths. In a
  coding agent's session the removal is refused outright (D1180's row): the refs and records that check
  reads are the session's own, so a clean read is its own word for what origin carries. On the owner's
  own run the gate is not consulted at all, and what stops the removal is the command's own refusal
  while a profile still lists the account, while a repository whose committed `fugaro.yaml` still names
  it is warned about before the confirmation and the removal goes ahead — that file belongs to a
  repository no command here can write (D783). `add` with a widening
  `allow_data_to` stays refused in a coding agent's session.
  Cost of the alternative: an account nothing can remove is an allow-list entry that outlives the
  account it was written for, and the person's only repair is an edit of a `fugaro.yaml`-adjacent file —
  the hand edit the ruling removes. Cost of the recommendation: the removal has to know every referrer —
  a profile that still lists the account refuses it, and a repository that still names it is the warning
  the owner reads — which is a check with tests of its own and wording that has to name the referrer
  rather than the key.

- **D1173 (`fugaro project repos set` for a job profile and a repository's own blocks; the `repos.*`
  scalars go to the generic writer) — OPEN, recommended.**
  Recommendation: `repos.*`' scalars and lists (`internal/settings/registry.go:701`) are the generic
  writer's, and one new subcommand carries the two shapes a bare scalar cannot express — a
  `job_profiles.*` block (`:647`, seven keys) and a repository's workflow selection — both object lists
  addressed by name (D1163). A separate `fugaro project job` family is refused: the keys already name
  `project` (`:647`), so a second group would give one owner two commands.
  Cost of the alternative: a second family for keys whose owner string already names the first is a
  registry that disagrees with the CLI, which readers hit before they hit the code. Cost of the
  recommendation: `project repos` grows a subcommand whose flags differ from its siblings' `ls`/`add`/`rm`
  (`internal/cli/project_cmd.go:65`), so its help has to teach a shape the family does not otherwise
  have.

- **D1174 (`fugaro image add` gains `--image REF` for a prebuilt entry) — OPEN, recommended.**
  Recommendation: the flag writes a `config.SourceImage` (`internal/config/catalog.go:69`) exactly as
  `--dockerfile` writes a built one, through the `image_edit` path `add` already uses
  (`internal/cli/image_catalog.go:295`), and the refusal a repository that also names a catalog entry
  gets stays as it is.
  Cost of the alternative: without the flag a prebuilt entry is creatable only by hand, and the catalog
  is a document `fugaro image ls` reads — so the command's own listing can show an entry its own `add`
  cannot write. Cost of the recommendation: two flags on one subcommand that are mutually exclusive by
  meaning, which the command has to refuse together rather than resolve.

- **D1175 (the project layer file's entries are managed entries with a writer) — OPEN, recommended.**
  Recommendation: every entry of the layer file — `profiles.*`, `defaults.*` (agent, git, rigor) and
  `ab.*` — is written by the generic writer through D1161 and D1162; the rows exist
  (`internal/settings/registry.go:411`), so the work is the allowlist, the entry shapes and the tests,
  not a new registry. `fugaro config publish`'s reserved-key refusal
  (`internal/cli/config_publish.go:309`) applies to the edit's own document.
  Cost of the alternative: leaving the layer hand-edited leaves the one file that decides which profile
  a project's repositories run under as the only configuration with no writer, which is the ruling's
  subject. Cost of the recommendation: the layer file is the widest single surface this design touches —
  the plan gives it its own task and its own reviewer, and the edit's publish path (D1176) has to be
  right before a person can trust it.

- **D1176 (the edit's own next step publishes; `fugaro config publish` stays, but no edit needs it) —
  OPEN, recommended.**
  Recommendation: a layer write ends with `next: fugaro update`
  (`internal/cli/settings_set.go:1172`) and `update`'s Layer stage publishes the file every writer here
  has just written — the HARD RULE's own command, not a second one. `config publish` remains the
  operator's door and the door the agent runs under the gate
  ([managed spec](../managed-agent-converge/spec.md) §4.2), so nothing documented is removed; what
  changes is that no *edit* requires it.
  Cost of the alternative: leaving the two-step means every managed edit is followed by a command the
  HARD RULE forbids as an operator step, and a person who forgets it has edited a file that no run sees
  — the failure that has no symptom until a job starts with the old layer. Cost of the recommendation:
  `update` grows a publish it did not have for a file it may not have written, so its Layer stage has to
  be idempotent and test the empty case (G8).

- **D1177 (a repository's `workflows.*` gets `workflow add`, the generic writer and `workflow rm`; what
  `setup` and `config init` write stays what they write) — OPEN, recommended.**
  Recommendation: `fugaro workflow add NAME` creates a block, `settings set` with a concrete key
  (`workflows.web.commands.test`) sets its scalars and lists, the entry pair sets its maps and object
  lists, and `fugaro workflow rm NAME` removes the block — all of them `--layer repo` writes, the one
  document an agent session may write (`internal/cli/settings_write.go:167`). `config init`'s minimal
  file stays minimal and `setup` keeps writing what it writes: it is the file a repository is anchored
  with, not the file a workflow is defined in.
  Cost of the alternative: teaching `config init` to write a full workflow means the anchor command
  becomes a definition command, and every later change to a default workflow's shape is a change to
  `config init`'s output. Cost of the recommendation: a repository's first workflow is two commands
  (`config init`, then `workflow add`), which the setup skill has to say and the plan has to keep true
  (G9).

- **D1178 (every non-writable key names a command that exists, or a sentence saying `setup` or `update`
  owns it) — OPEN, recommended.**
  Recommendation: **the rule**, **the mechanism** and **the table**. The rule: a key the allowlist
  carries is `settings set`'s and its row says so; every other key names a command that exists or a
  sentence naming `fugaro setup` or `fugaro update`. The mechanism: the read resolves an owner through a
  table of its own instead of `settingsFamilyCommands` (`internal/cli/settings_set.go:43`), which answers
  the *write*'s refusal and cannot be widened — `config` owns `max_parallel` (`internal/settings/registry.go:560`) and `setup`
  owns `update_check` (`:566`), and both are keys the generic path writes today (F11). **The table**:
  every key whose owner string changes is listed in the plan's own table (G12), so the change is
  reviewable as a diff of names. The owner column is a read path
  (`internal/cli/settings_read.go:388`) and not a write path, so this is a rename of a name and never a
  meaning.
  Cost of the alternative: resolving through the write's own map makes a read-only question a change to
  what a command refuses, and adding `setup` or `config` to it refuses two keys `settings set` writes
  today — the trap D1179 fell into with `budget`. Cost of the recommendation: a second table is a second
  place a family is spelled, so the plan has to keep the two in step with a test (G12) rather than
  claiming one map answers both questions.

- **D1179 (the four budget keys stay the generic writer's, and the allowlist gains the two it refuses;
  `compute_prices` goes to the generic writer too) — OPEN, recommended.**
  Recommendation: `budget.mode`, `budget.per_day_usd`, `budget.per_run_usd` and `budget.allowed_models`
  (`internal/config/scope.go:91`, four `InRepo` rows) are a repository's own tightening of a ceiling and
  **stay the generic writer's**. Two of them are already in the allowlist
  (`internal/cli/settings_set.go:81`: `budget.per_run_usd`, `budget.allowed_models`); the other two are
  **added to it**, which is the whole change F13 needs, so a key `settings set` writes and a key its
  allowlist carries are the same set (D1160). `fugaro budget set` keeps writing the budget **database**'s
  caps under its ETag-guarded write (`internal/cli/budget.go:923`) and owns no registry row: handing it
  these four would refuse, at a stroke, two keys the generic path writes today. `compute_prices.*`
  (`internal/settings/registry.go:595`) — a local-config map with its own validation
  (`internal/localcfg/localcfg.go:118`) — becomes a generic-writer entry (D1162) as well, because it is a
  deliberate local override no run's ceiling turns on.
  Cost of the alternative: naming `fugaro budget set` for the four is two writers of one ceiling with the
  registry's owner column naming the remote one, and it breaks the two keys the generic path already
  carries — a fix that refuses what it was meant to free. Cost of the recommendation: `budget set` and
  `settings set` both move a ceiling, so the read has to say which document each writes (the file the
  repository's runs read, the database the installation enforces), which is D1178's table's job.

- **D1180 (every new command carries a row in the managed gate's command set, and the rows follow
  D1026) — OPEN, recommended.**
  Recommendation: the spec's §5 table is the row set, one row per command and operation, each saying
  what the gate admits in a managed project and who refuses what it does not; a removal of a secret, a
  floor lowered or removed, and a widening that takes the typed name are refused as `Typed` steps and
  never admitted (D1026, `docs/managed-agent-converge/reconciliation.md:223`); the gate can only narrow,
  so no row here widens what an agent may do. The rows go into this folder's documents and are cited by
  the managed build's tasks, which do not restate them.
  Cost of the alternative: shipping a command with no gate row leaves the agent's own decision to the
  command's refusals — which is how a managed project's fence gets a hole nobody reviews, and the
  managed folder's whole design is built on the class winning. Cost of the recommendation: every later
  change to a command's rules re-reads its row, so the two folders are coupled by citation and a change
  in one that misses the other is a docs test away from being caught rather than a hole.

- **D1181 (the agent-session refusal stays everywhere outside the gate) — OPEN, recommended.**
  Recommendation: no task here removes a refusal in a coding agent's session; a command is runnable by
  an agent only where `internal/initflow`'s `CanConfirmManaged` (`internal/initflow/stage.go:580`)
  returns true under the mark, and the mark's write path stays the owner's alone (the managed folder's
  own F4). The refusal is not a managed-project feature and nothing in this design moves it.
  Cost of the alternative: relaxing a refusal because a gate exists is two fences replaced by one, and
  the one that remains is the one a plan document decides — a document the agent itself produces. Cost of
  the recommendation: a person in a coding agent's session still can't fix a broken secret from it, and
  must end the session and run the command at their own terminal, which is slower and is the point.

- **D1182 (the installation's own facts stay setup/update-owned; a deliberate change is a flag on
  `setup` or `update`) — OPEN, recommended.**
  Recommendation: the roughly seventy keys whose owner is `setup` or `update` — `endpoints.*`
  (`internal/settings/registry.go:659`), `terraform.*` (`:585`), `batch.*` (`:571`), `build.*` (`:581`),
  `gcp_project` and `runs_bucket` (`:540`), the generations (`:545`) — are never hand-edited and get no
  command here; where a person must choose one, the choice is a **flag on the converge** that feeds it,
  as `terraform.alert_email` (`:586`) already has one (`internal/cli/init.go:368`) and the batch machine
  type (`:572`) has in the design it comes from. An edit of a file the next `update` overwrites is the
  state this design removes, and it is the state most likely to be mistaken for a setting.
  Cost of the alternative: giving these keys a generic writer would let a person set an endpoint the
  installation's Terraform does not serve — a configuration that reads as valid and fails at the first
  job. Cost of the recommendation: a fact that has no flag still cannot be changed by any command, so the
  plan's owner table has to point at the converge rather than at an editor, and a person wanting one of
  those facts waits for a flag (G12).

- **D1183 (no compatibility or skew shim; the settings-write document keeps the one version there is) —
  OPEN, recommended.**
  Recommendation: there are no production users and this design adds no shim, no dual read and no
  migration: a shape that must change changes directly. The settings-write document is the one shape
  that carries a version at all, and this design **does not move it**: its new members are added as
  optional ones inside version 1 (D1164, D848), because the console pins that version
  (`console/src/main/settingsview.ts:42`) and refuses any other (`:181`) — a bump is a console release
  the CLI cannot make. No key is re-meant: the owner column's values change (D1178, D1179) and a key's
  own meaning does not.
  Cost of the alternative: a shim for a pre-1.0 CLI with no users is code that will be deleted before it
  is ever exercised, and it hides the very disagreements (F13, C4) this design exists to expose. Cost of
  the recommendation: version 1 may only grow by optional members while the console reads it, so a
  change to a member's *meaning* is a version bump and a console change in the same release, and an
  installation one version behind still meets the only answer the repository has: `fugaro update`, the
  HARD RULE's own command.

## 5. The residual this design refuses to hide

- **The registry's owner column is prose after this design, and prose cannot be tested for truth.** A
  sentence saying `fugaro setup` owns a key is a string a test can compare; that the sentence is
  *correct* — that setup really writes it — is not, until something enumerates what each command writes.
  D1178's table is the beginning of one and not the thing itself; a later work could make the registry's
  owner column a closed vocabulary with a compile-time list, and this design does not.
- **`--add-entry` is one entry per command, so a large block is many invocations.** D1167 accepts it.
  The mediocrity is real: a person importing a workflow's cache and secrets types the block's entries one
  at a time, and the honest repair (a file of entries the command reads) is a second input format this
  design did not take on.
- **The managed gate's rows are documents here, and the gate's code is the managed build's.** If the
  managed build lands M6's helper with a different vocabulary than §5 uses, the rows must be re-read
  against it rather than translated; this folder cannot enforce that, and says so instead of promising
  it.
- **The figures in §1 are counted in this tree and will move.** F1's 89 and F14's 92–93 are true today;
  the guard re-counts the ones that matter, and a figure in the prose of a document is only ever as
  fresh as the commit that wrote it, which is why the row's own command is quoted beside it.
- **This design does not decide the key field of every object list.** It decides the *rule* (D1163) and
  leaves the per-block choice to the plan's tasks, because the registry names leaves and not blocks —
  and a rule decided far from the block it applies to is how two blocks end up addressed two ways.
