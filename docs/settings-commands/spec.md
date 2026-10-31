# `fugaro settings`: the spec

The commands below are written to the owner's answers to SD1–SD18 and to this design's own decisions
D840–D854, all answered on 2026-10-31 as recommended ([reconciliation.md](reconciliation.md) §3 and
§4). A decision answered the other way would change the paragraph that names it; none was. D854 —
a stale local copy overwriting a newer publish — is answered with the refusal, and §6 says what the
publishing stages do with it. Repository names are placeholders (`owner/repo-a`, `owner/repo-b`),
projects are `workspace-a`, people are `person-1`.

## 1. The rule this serves

`fugaro update` is the whole upgrade (`CLAUDE.md`, HARD RULE). So:

- **Every write these commands make is a local file edit.** No command in this design writes the
  published policy node, the runs bucket, a job, a secret or anything else in the cloud directly.
  The three cloud writers stay what they are: `fugaro update` (the policy node, the project layer,
  the job environment, Terraform), the runtime controls (`budget set|kill|resume`, `accounts
  clear`, §5.8), and the two explicit doors that survive for scripted use (`project policy publish
  --delete|--seed-repos`, `config publish FILE`).
- **Every local edit ends with one line:** `next: fugaro update`. Never a list of steps, never
  `project policy publish`, never `update --repo` per repository (D787).
- **A change `fugaro update` cannot carry is a gap in `update`, not a step for the user.** The
  publish foundation of §6 exists because of this sentence: today an edit plus `fugaro update`
  publishes nothing from the policy section and nothing of the project layer.

## 2. `fugaro settings` — the read

```
fugaro settings [KEY|GLOB ...] [--repo owner/name | --all-repos]
                [--layer default,project,profile,repo,flag,local,shared,policy,caps,env]
                [--grep RE] [--changed] [--why] [--offline] [--json]
```

One row per (key, scope), where a scope is `project` or `repo:<canonical>`. The text columns are:

```
KEY  VALUE  WINS  DEFAULT  STATE
```

### 2.1 KEY

A stable dotted name from the registry of §3. The names are unique across stores, so a field never
changes meaning (SD6): `budget.mode` is the policy ceiling mode that lives in the local config, the
node and `fugaro.yaml`, while `caps.mode` is the Realtime Database `config/mode` that
`budget set --mode` writes and `update --budget-mode` seeds. The two things called "profiles" are
named distinctly for the same reason (SD14): `layer.profiles.<n>.*` is the project layer's workflow
profile and `job_profiles.<n>.*` is the local config's job profile.

A bare `fugaro settings` prints every key of the registry. Positional arguments are a key, a prefix
(`budget` matches `budget.*`) or a glob (`budget.*_usd`); a name no key matches exits non-zero and
prints the nearest registry names, so a typo is not an empty table.

### 2.2 WINS, and the precedence it names

`WINS` is the level that decided the effective value. The precedence is not invented here: it is
the one the code already implements, stated once in `--help` and once in the JSON Schema.

| Key family | Order, loosest first | Rule |
| --- | --- | --- |
| Resolved `fugaro.yaml` keys | `default` < `project` (layer `defaults:`) < `profile <n>` (layer profile, `workflows.*` only) < `repo` < `flag` (the `InOverride` keys only) | `config.Resolve` and `Resolution.SourceOf` (`internal/config/resolve.go`); `config show` prints the same words |
| Ceiling keys (`budget.{mode,per_run_usd,allowed_models,max_run_tokens}`, `governance.*`) | `default` → `local` (the job environment as of the last `update --repo`) → `policy` (the node) → `project` (layer governance) → `repo` → `branch` (the run's branch) | **Tighten only:** the smaller cap, the stricter mode and the intersection of the lists win (`policy.Merge`, `internal/policy/merge.go`) |
| `review_floor` | `policy` ∪ every lower layer | A union: a lower layer only adds a floor (`localcfg.Config.ReviewFloor`) |
| `rigor.default` | `default` < `project` < `repo` | **`rigor.Resolve` (`internal/rigor/resolve.go`) is the single source of this row**: the read calls it rather than ordering the layers itself, so a run and the table cannot disagree. `rigor.areas` is a union of the two layers |
| `caps.*`, `kill.*` | `caps.defaults` < `caps.repo` | The Realtime Database caps, independent of and enforced beside the ceiling |
| `layer.profiles.*`, `layer.*` | `project` only | The project layer has one writer: the layer file |
| `credentials.*`, `clone_urls.*` | `local` only | New local-config blocks (SD5); `update` publishes them into the node, which no longer holds a value the local file does not |
| Local and installation keys | `local` only | For a key the shared config also carries, `shared` stands in when this machine's file lacks it |
| `env.*` | `env` overrides `local` where both exist | Today only `update_check` has both |

A level that sets a key and loses is not hidden: under `--why` it is printed with its value and
`ignored (looser than <winner>)` or `overridden`, from the same resolution the winner came from.

### 2.3 VALUE

The effective value — **the one a run gets**, not the raw value of any one layer (D849). This is
where `settings` differs from `config show`, which reports a repository's `budget.per_run_usd: 50`
as source `repo` even when the project ceiling clamps it to 10 and only `validate` says so (F17).
Here the row reads `10` with `WINS policy`, and `--why` carries the `50` and the clamp.

- **No secret value is ever printed.** No setting value is a secret today — a provider `secret` and
  an account name are names, and `mailbox.keychain_item` is a keychain item name — but the registry
  carries a `sensitive` flag, and such a key prints `set`/`unset` and nothing more.
  `budget.firebase_api_key` is public and is still redacted by default, printed only with
  `--show-sensitive`, which refuses in a coding agent's session.
- **An unknown member of the published node prints its name and never its value**, the convention
  `init_policy_migrate.go` already follows for a node a newer CLI wrote.
- A key set at no layer prints the default value, never a blank: `WINS default` and
  `VALUE` equal to `DEFAULT`.

### 2.4 DEFAULT

The built-in default, from the registry, as a value plus the constant that holds it — or a sentence
saying what unset means (`unset = off`, `unset = inherit`, `unset = every repository`, `unset = the
project layer's default`, `none`). A default that is a *decision*, not a value — `budget.mode`'s
`unset = observe`, `credentials.fallback`'s `unset = oauth-only` — prints the sentence, because a
value there would be a second place the meaning lives.

### 2.5 STATE

One of a **closed set** (D851), drawn distinctly and never filled with a guess:

| State | Meaning |
| --- | --- |
| `ok` | read from the layer that decides |
| `stale <age>` | a cached copy (the layer cache, the shared-config cache) with its age |
| `unread: <reason>` | no permission, offline, no budget backend, no Firebase project. **Never** a default in its place |
| `unpublished` | the local value differs from what the published node or the published layer carries, so it reaches runs on the next `fugaro update` (SD3, SD4). This is the G1 and G2 detector and the reason the command exists. It is the same sha256 comparison the `rigor` design runs, and **the word is `unpublished` everywhere** — in `settings`, in `rigor show`, in `config layer` and in `doctor` alike; none of them says `drift` |
| `unknown (no checkout on this machine)` | a repository with no checkout on this machine, so its `fugaro.yaml` was not read and its row is not guessed. The wording is the `rigor` design's (its D839), word for word |

`unpublished` compares the local file against the cloud, so it is only ever printed for a scope
whose cloud side was read; with `--offline` it is not claimed at all.

### 2.6 Flags

- `--why` adds one sub-row per level that sets the key: the value, and whether it won, was
  overridden, or was ignored as looser than the winner.
- `--changed` shows the rows whose value differs from DEFAULT — the one-screen answer to "what have
  I changed on this installation".
- `--repo owner/name` narrows the repository scope to one; `--all-repos` iterates the local config's
  `repos:` plus this machine's remembered checkouts (`internal/localcfg/checkouts.go`). A repository
  with no checkout is `unknown (no checkout on this machine)` (the `rigor` design's D839).
- `--layer` restricts the layers read, to the ten names in the synopsis: `default`, `project`,
  `profile`, `repo`, `flag`, `local`, `shared`, `policy`, `caps` and `env`. `default` and `env` are
  always readable; `policy` and `caps` are the two cloud layers; `flag` is the top level of §2.2's
  first row, a run's own command-line override for the `InOverride` keys, and `settings` takes no such
  override itself, so `--layer flag` reads an empty layer rather than being refused — it is a level
  `WINS` can name, and leaving it out would make `--layer`'s vocabulary smaller than the precedence's.
  The one level of §2.2 that is not a `--layer` name is `branch`: only a run has a branch, so it is
  not a store a read selects. `--layer` with a name the registry does not know is refused.
- `--grep RE` filters rows by key or by rendered value. `RE` is a Go regexp, matched with
  `MatchString` against the key and then against each rendered text column: **unanchored**, so a
  pattern matches anywhere in either, and **case-sensitive** unless the pattern says otherwise
  (`(?i)`). A pattern that does not compile is refused with the compiler's own message.
- `--offline` stops after the offline layers; every cloud row then reads `unread: offline` rather
  than disappearing, so the table keeps its shape.
- `--json` prints one versioned document (§2.8).

### 2.7 Rendering order and cost

`settings` follows the console's progressive-render lesson, in a terminal:

1. **Every offline layer is printed at once** — the registry's defaults, the repository files, the
   project layer (its cache, or one bucket read), the local config, the shared config's cache, the
   environment. This is the whole table for a project whose cloud is unreachable, and it is what
   `--offline` leaves on screen.
2. **Then the cloud layers**, in parallel, under a bounded timeout (the five-second precedent of
   `accounts ls`), each row marked with its source's `read_at` and duration, and a layer that
   timed out or was refused reading `unread: <reason>`.

Each layer's reader is the one that already exists — `config.Resolve`, `localcfg`'s selector and
parser, `ParseShared` and its cache, `projectpolicy`'s read, the budget reader — so `settings` adds
no second reader for anything (F6). It reads no Terraform state and calls no Secret Manager access
method (D850).

### 2.8 `--json`

One document, `{"version": 1, "project": …, "generated_at": …, "sources": [{"layer", "read_at",
"duration_ms", "status", "note"}], "settings": [{"key", "scope", "value", "wins", "default":
{"value", "source"}, "state", "levels": [{"layer", "value", "outcome"}]}]}`, whose members are only
ever added within a version (D848). A renamed or re-meant member is a new member under a new name
and the old one is kept; removing or retyping one bumps `version`. The schema is
`schemas/settings.schema.json`, loaded and exercised by `schemas/schemas_test.go` like every other
wire format.

A value the registry marks sensitive is **absent** from the document, not null and not a sentinel:
the row carries `"sensitive": true` and the `set`/`unset` fact, which is the only spelling a
consumer cannot mistake for the value.

## 3. The key and defaults registry

One machine-readable row per setting (SD16, D840), in a new `internal/settings` package:

| Field | What it holds |
| --- | --- |
| `Key` | the stable dotted name of §2.1 |
| `Layers` | where the key may be set — the stores and layers of §2.2's table |
| `Merge` | `replace`, `tighten` or `union`, the rule the row's precedence obeys |
| `Default` | the value and the constant that holds it, or an "unset means" sentence |
| `Sensitive` | whether the value is ever printed |
| `Owner` | the command family that writes it (`settings set`, `models`, `rigor`, `rigor floor`, `accounts`, …) |
| `Doc` | the document that must state it (`docs/project-layer.md`, `docs/multi-model.md`, …) |

`config.Scopes` (`internal/config/scope.go`) stays exactly what it is — the `fugaro.yaml` keys and
the layers they may be set in — and the registry **reads** it rather than replacing it: a
`fugaro.yaml` key appears in both, and a test asserts the two agree row for row. That keeps
`internal/config` free of any knowledge of `localcfg` or `projectpolicy`, which is what lets the
runner and the task format import it (D840).

What the registry becomes the single source of (D841):

- the DEFAULT column and `--changed`;
- the JSON Schema `default` keywords, which exist nowhere today, **pinned by a test** rather than
  generated: a generator is a `go generate` step a release must remember, and its output is a diff
  nobody reads;
- `applyDefaults` (`internal/config/defaults.go`) and the local loader's literals, which read their
  values from it instead of restating them (this is where `"main"` beside `config.DefaultBaseBranch`
  stops being two copies of one default);
- a completeness test in the shape of `TestExecutableKeyValuesCoversEveryExecutableKey`: **every**
  `fugaro.yaml`, project-layer and local-config field has a row, so a key added later without one
  fails the build rather than silently missing from the read.

## 4. `fugaro settings get|set|unset` — the generic write path

### 4.1 `get`

`fugaro settings get KEY [--repo R] [--json]` prints one row in the §2 format and exits 0 whether or
not any layer sets it (a key set nowhere is `WINS default`, not an error). An unknown key exits
non-zero with the nearest registry names. It is a narrower `settings` and shares its resolver.

### 4.2 `set`

```
fugaro settings set KEY VALUE [--layer local|project|repo] [--repo R] [--yes]
fugaro settings set KEY --add V | --remove V [--layer …]     # string-list keys
```

In order:

1. **Find the row.** An unknown key is refused with the nearest names. A key whose `Owner` is
   another family is refused and that family's command is printed — `settings set` never writes a
   route, a price, a rigor mode, a floor, an account, a job profile, a credential or a clone URL
   (D853). A map-valued key has no generic form and is owned by a family, always.
2. **Find the layer.** `--layer` names the target; `default` is never a target. `--layer repo`
   without `--repo` uses the current directory's checkout and refuses when the directory is not one.
   A layer the key may not be set in is refused with the layers it may (the registry's `Layers`).
3. **Check the value.** It must parse as the key's type — an int, a bool, a duration, a mode from a
   closed set, a string-list entry — and the edited document must re-parse strictly
   (`localcfg.Parse` for the local config, `config.ParseProjectLayer` for the layer, the
   repository's own strict parse for `fugaro.yaml`). A value the strict loader refuses is refused
   here, with the loader's own message.
4. **Refuse in a coding agent's session** when the target is outside the repository — `--layer
   local` and `--layer project` write `~/.config/fugaro`, so under `agentenv.Marker` they print the
   diff and the command and write nothing, `--yes` included (SD9). `--layer repo` writes a file
   inside the repository, which an agent may edit anyway, and is allowed — **except for a write that
   asks for the typed project name**: loosening a ceiling or removing a floor is refused in an agent
   session even at `--layer repo`, printing the diff and the command and writing nothing. The typed
   form is the owner's own hand, and the file it would edit decides the ceiling of every run the
   branch starts, so an agent may propose that edit but not make it.
5. **Show, confirm, back up, write.** The line diff, then the confirmation; the old file backed up
   to `<file>.bak-<UTC timestamp>` (mode 0600) and the new one written atomically through the same
   `lineDiff`, `backupFile` and `writeFileAtomic` every other writer uses (F4).
6. **Hand off.** `next: fugaro update`.

**The sensitive directions ask for the typed project name** (`confirmTypedStrict`, D846): loosening
a ceiling key (`budget.*`, `governance.*`), removing a `review_floor` entry, turning a `followup.*`
key on, or writing a key the registry marks as a floor. `--yes` does not stand in for the typed
form, exactly as `fugaro project repos rm` already does not.

**A value is never written as an empty list.** A list key emptied by `--remove` is refused, or the
key is removed when the caller asked for that, because the loader refuses a written-but-empty list
where absent and empty mean different things.

### 4.3 `unset`

`fugaro settings unset KEY [--layer …] [--repo R] [--yes]` removes the key from that layer so the
layer below decides again. It never writes a default in its place (the reason `rigor unset` gives:
a written default is indistinguishable from a deliberate pin and silently wrong the day the built-in
changes). Nothing to remove is a **no-op with a sentence and exit 0**, so an idempotent script does
not fail. Removing a ceiling or a floor asks for the typed project name by the same comparison
`set` uses.

### 4.4 Which keys the generic path owns

Every **scalar or string-list** key of the registry, which is: `budget.{per_run_usd,max_run_tokens,
allowed_models,unreachable_grace}`, `base_branch`, `repos.<r>.base_branch`, `max_parallel`,
`update_check`, `watch.burn_alert_usd_per_hour`, `commit_name`, `commit_email`, `defaults.git.*`,
`defaults.agent.*`, the repository's `agent.*`, `git.*`, `followup.*`, `governance.*` scalars,
`default_profile`, and the new `credentials.*` and `clone_urls.*` scalars whose family is §5.6. A key
with a family is refused with a pointer, never written twice by two paths.

## 5. The dedicated families

Each is the same shape: a **read** — the family's own `show`/`ls`, or the `ls` the command already
has (`fugaro accounts ls`, `fugaro secrets ls`), read-only, `--json`, in the row format of §2 — then
the writes, through the same check/diff/confirm/backup/atomic-write path and the same agent-session
rule. Each refuses in an agent session when it writes outside a repository, and each ends with
`next: fugaro update`.

| Family | Forms | Writes | Covers |
| --- | --- | --- | --- |
| `fugaro rigor floor` | `show [--json]`; `set <mode> <l1,l2,…>`; `unset <mode>` | the local config's `review_floor` | SD10, G7. Lowering asks for the typed project name; an absent key and `{}` mean different things at publish, so `unset` writes `{}` only where the owner asks for a floor of nothing, and otherwise removes the mode's entry |
| `fugaro accounts` | `add NAME --allow REPO…`; `allow NAME REPO`; `deny NAME REPO`; `rm NAME` | the local config's `claude_accounts` | G6. The token stays `secrets set` / `creds set`; this command writes only `allow_data_to` |
| `fugaro secrets allow` | `allow NAME PATTERN…`; `deny NAME PATTERN`; `--proxy` | the local config's `project_secrets` | G6 and SD17. `update --repo` reads the allow-list from the published node instead of the operator copying the entry to every machine |
| `fugaro project profile` | `ls\|show [--json]`; `add NAME --provider P --cpu N --memory M --timeout-class C`; `set NAME KEY VALUE`; `rm NAME` | the local config's job `profiles` | G6 |
| `fugaro project routing` | `show [--json]`; `set claude --via direct\|cascade\|openrouter\|auto [--models …]`; `unset` | the local config's `routing` | G8. The publisher's silence rules are kept: the command refuses to write a `routing` block that turns the cascade off by omission |
| `fugaro project credentials` | `show [--json]`; `set fallback auto\|oauth-only\|none`; `set wait-threshold D`; `set backoff --initial D --cap D`; `set concurrency-per-token N`; `set stop-after-exhaustion N` | the local config's new `credentials:` block | G3, the highest-risk gap |
| `fugaro project clone-url` | `ls\|show [--json]`; `set CANON URL`; `rm CANON` | the local config's new `clone_urls:` block | G3 |
| `fugaro update --no-…` | `--no-coordinator` and the other opt-outs | the local config's `terraform.*` | G11, SD15 |

The two profiles are named distinctly in the keys and in the commands (SD14): `layer.profiles` and
`job_profiles` are separate settings keys today, and a YAML rename is a separate decision this
design does not take.

## 6. The publish foundation: what `fugaro update` gains

Three stages, all inside the one command the user runs, all behind its one confirmation, all
refused in a coding-agent session as every cloud write of `update` already is (SEC):

1. **Policy publish.** The stage that today only migrates the node (`internal/cli/init_policy_migrate.go`)
   gains the publish: it builds the node from the local config with `buildProjectPolicy`, shows the
   diff, confirms, and writes under the ETag, with the migration of an older node in place. This is
   the same stage the `models` design's E1 names (its F8, UC1 part b); **it is one stage and it is
   built once** — whichever work lands it first, the other deletes its copy. `project policy
   publish` stays for `--delete`, `--seed-repos` and scripted use, and gains the agent-session
   refusal it lacks today — the other door SD8 names, `fugaro project repos add`, gains the same
   one, and only its `--github-app-key` form has it today (F16).
2. **Layer publish.** The stage that publishes the project layer's local file —
   `~/.config/fugaro/projects/<project>-layer.yaml`, the path `localcfg.LayerFile` resolves — through
   `publishLayer`, and fans it out through `fanOutLayer`, with the executable-key gate unchanged.
   This is PR #1463's R1, and this design **does not build a second one**: §7 says exactly what is
   shared, and the settings-side task (S4) is only the read that the layer file has a path and is
   `unpublished`.
3. **Provider re-converge.** `fugaro update` re-converges each recorded repository whose job's
   provider copy or ceiling copy differs from what the local config would write now, behind the same
   confirmation (D787, already answered). `fugaro upgrade`'s drift note already computes it.

And one migration, in the same command (SD5, D842, D844):

4. **The node-only blocks get a local source.** The local project config gains two blocks that
   exist nowhere today — `credentials:` and `clone_urls:` — decoded strictly
   like every other block. On the **first** `update` after this release, an installation whose node
   carries either block has its values **imported** into the local file: the stage prints them, asks
   under update's own confirmation, writes the file with the diff/backup/atomic triple, and says
   that from now on the file is the source of truth and `update` publishes it. A declined import
   leaves the file and the node exactly as they were and says so — nothing is lost, and the console
   stays the only writer until the owner accepts or sets a value with §5.6. An installation whose
   node carries neither block gets neither block in its file: no default is seeded into a file
   nobody wrote (D843).

**A stale local copy cannot overwrite a newer publish — D854, answered.** A stage that writes whenever
the file differs from the object would, on a second machine, replace an operator's newer layer or
policy node with this machine's older file, and say nothing. So each stage records the generation the
local file was seeded from or last published at, and when the published generation has moved since,
it **refuses to publish**, shows the three-way state (this machine's file, the generation it was
written from, the object published now), and offers to re-seed the local file from the published one
under confirmation, with a backup. The refusal is a question rather than a wall: a declined re-seed
leaves both sides as they were, and the stage publishes again once the file is reconciled with the
generation it was written from.

The credentials cascade's `fallback: auto` is the reason the import asks rather than writes: the
block decides whether paid credential tiers are spent, and a value the owner never saw must not
become theirs by a silent write.

## 7. What is shared with `fugaro models` and `fugaro rigor`

Four mechanisms, each built once, each named here so no plan builds a second:

| Mechanism | Owner | How this design uses it |
| --- | --- | --- |
| **The shared comment-preserving editor** | `internal/yamledit` — **one package, one owning task** (the editor task, E3), which lands it **first** in the joint order of the implementation plan. Neither this design nor the `models` or `rigor` design builds a second one | `settings get\|set\|unset` calls it for all three documents: `--layer local` (the local project config), `--layer project` (the local layer file) and `--layer repo` (`fugaro.yaml`). It holds generic **locate-and-splice** operations — set a scalar, remove a key, add or remove a list item, upsert or remove a sequence item by key — that parse to *locate* the node and splice the bytes, never re-emitting the document. The plan adds a cross-document test that all three documents preserve comments, key order and every untouched byte the same way (D847) |
| **The diff-and-confirm UX** | `lineDiff`, `backupFile` (`<file>.bak-<timestamp>`, mode 0600) and `writeFileAtomic` (`internal/cli/init.go`), plus `confirmTyped`/`confirmTypedStrict` (`internal/cli/budget.go`) | Every write in §4 and §5 uses all five; the sensitive directions use the typed form, which `--yes` never covers |
| **The coding-agent-session refusal** | `agentMarker`/`agentEnv` over `agentenv.Marker` (`internal/cli/initsecrets.go`), as `config publish` and `recipes publish` already refuse | `settings set\|unset --layer local\|project` and every family that writes outside a repository print the diff and the command and write nothing, `--yes` included (SD9). Reads always run |
| **The `next: fugaro update` line** | the `models` spec §4.6 and the `rigor` spec §3.7 | Every write command here prints that one line and nothing else (SD3, SD4) |

Three things this design shares the other way, and none is a new mechanism of its own: the **editor**
is the editor task's `internal/yamledit`, the update **policy** stage is the `models` plan's E1, and
the update **layer** stage is the `rigor` plan's R1 — so a task here that would write any of the
three is a coordination task, not an implementation (§6, implementation plan).

## 8. Owner-only and agent-session boundaries

- **A read is always allowed**, in an agent session and anywhere: `settings`, `settings get`, and
  every family's `show`. None of them calls a Secret Manager access method, reads Terraform state,
  or prints a sensitive value (§2.3).
- **A local file the user owns may be edited by the user** (SD18). `--layer local` and `--layer
  project` write files in the user's own config home; the boundary that matters stays IAM, at
  publish: `update`'s policy and layer writes are the owner's, and the project layer's launcher
  reachability (SD13) is recorded here and decided elsewhere.
- **A repository file is written in the working tree and never committed** (the `models` D781
  rule): the user's own branch and review carry it to runs.
- **No secret is read, printed or stored** by any form here. A credential block names a secret, it
  never holds one; the values stay `secrets set` and `creds set`.

## 9. Migration of a hand-edited installation

- **Nothing is renamed and nothing is moved.** Every key keeps its name, its file and its meaning;
  the registry records the meaning rather than changing it. A block added to the local config
  (`credentials:`, `clone_urls:`) is new, and the node's old spelling is imported, not
  renamed in place.
- **A hand-edited file survives every write unchanged except the keys the command touches** — the
  byte-preservation property of §7's editor, asserted by the cross-document test.
- **An installation at the previous version converges on one `fugaro update`**: the policy stage
  publishes the node the local config already described, the layer stage publishes the layer file
  the owner already has, and the import brings the two node-only blocks into the local file. This is
  the HARD RULE's own test (implementation plan S3, S4).
- **The two explicit publish doors keep working**, so a script or a runbook that calls
  `fugaro project policy publish` or `fugaro config publish FILE` is not broken by this release; it
  simply stops being the only way.

## 10. The console, and the contract section it will need

No console task is in this plan, and nothing under `console/` is touched by this design. When a
read-only **Settings** view is wanted, it reads `fugaro settings --json` the way the console reads
every other CLI document — spawning `--json --offline` first and `--json` after, as §2.7's two
steps — and the command is added to the console's spawn allow-list at that time.

**The contract section it will need is `console/docs/json-contract.md` §6.15, "`fugaro settings
--json` (CLI 0.15.0, console 0.117.0)"**, in the "other documents" chapter that already carries
6.1–6.14. This design fixes what that section must say and writes none of it: the document version
and the add-only rule, the closed state vocabulary of §2.5, the absent-not-null spelling of a
sensitive value, and the precedence table of §2.2. The task that builds the screen writes the
section, in the same pull request, so the contract and the screen cannot drift.

The mobile app reads nothing of this.
