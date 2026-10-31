# `fugaro rigor`: the spec

**Design approved; the tasks queue for CLI 0.15.0 (and console 0.117.0).** The commands below are
written to the answer of each decision (D830–D839, [reconciliation.md](reconciliation.md) §3):
**D830** (where `set` writes) and **D831** (the `Layer` stage in `update`) are **answered** — the
owner's SD4 ruling of 2026-10-31 — and **D832–D839 are answered on 2026-10-31, every one as
recommended**. One decision the plan **cites** is **not** this design's — **D854**, whether the
`Layer` stage **records** the object generation it reads at the seed, which is the settings design's
— and the owner **answered it on 2026-10-31: the generation is recorded**. So `update` carries two
guards, and R1 states both: the **within-run** one, which refuses a publish that moved since the
stage's own read and needs no record, and the **across-runs** one, which reads the record
(`layer_generation` on the local project config) and refuses to publish over a newer published
generation. Repository names are placeholders (`owner/repo-a`, `owner/repo-b`); modes are
`internal/rigor`'s five words (`poc`, `startup`, `pro`, `critical`, `hotfix`).

## 1. The rule this serves

`fugaro update` is the whole upgrade (`CLAUDE.md`, HARD RULE). So:

- **There is one local layer file**, `~/.config/fugaro/projects/<project>-layer.yaml`, and **no flag
  and no config key names it** (D830, answered): the path is the project's own name under the
  config home's `projects/` directory, which is where the project's config already lives. `fugaro
  rigor set` and `unset` edit that file and read; neither writes the runs bucket, a job, a secret or
  anything else in the cloud.
- The project default and the project's area rules reach every run through the **project layer**,
  so the change reaches the cloud through the next `fugaro update`, whose `Layer` stage publishes
  the local layer file (D831, answered). The **first** `update` after this ships **seeds** the file
  from the published layer when it is absent, so an installation that has never had a local file
  gets one that is exactly the layer in effect before anything is edited. Each write command ends by
  printing `next: fugaro update` and nothing else — no `config publish`, no per-repository
  `update --repo`.
- `--repo R` writes the repository's own `fugaro.yaml` in its checkout's working tree, which the
  user's own branch and review carry; the command never commits or pushes (D833).

## 2. `fugaro rigor show [--json] [--repo R] [--path P]... [--offline]`

Offline by default in the sense that matters: it reads this machine's project config, the local
layer file (§1's one path, when it exists), the published project layer (or the local layer cache
when the bucket cannot be reached, saying which it used) and each repository's `fugaro.yaml` at its
base branch in this machine's remembered checkouts. It writes nothing. `--path P` (repeatable) is the same declaration `fugaro run --path`
takes, and it is the only way `show` can answer the area question the way a run will.

**Every mode this document prints comes from `rigor.Resolve`** (`internal/rigor/resolve.go`), the one
function that orders the modes and settles the precedence between area, repository, project and
default (`docs/project-layer.md` §2's one rule). `show` calls it per repository, and the settings
row calls the same function — there is no second resolver and no re-derived precedence anywhere in
either design, so a mode `show` prints and a mode a run reaches can only differ by the inputs the
caller passed (which is why a missing checkout is §2.3's absent member and not a guess).

The output is four sections.

1. **The project default.** `defaults.rigor.default` as the project layer resolves it, the layer's
   generation and sha256, and **where the value comes from**: `project` (the layer sets it) or
   `default` (the layer sets none, so Fugaro's own default `pro` decides). When the local layer file
   differs from the published object, an `unpublished` line says so, names both sha256s and adds
   `fugaro update` as the way to publish it — the state the layer stage of D831 closes. The word is
   the shared one: `fugaro config layer` and doctor call the same state, decided by the same sha256
   comparison (README, "the rulings the rigor and settings designs share"). **One state, two causes,
   and the line says which.** Differing bytes are either a **local edit** — this machine's file was
   written from the generation the published object still has, so the owner's change is what to
   publish — or a **stale copy from another machine** — the published object has moved past the
   generation this file was written from (the record `layer_generation` on the local project config,
   D854 answered), so publishing it reverts someone else's publish and the file is what to re-seed.
   The line names the cause and the remedy that fits it; the sha256 comparison alone cannot.
2. **The area rules of both layers, as far as this machine can read them.** Every rule the project
   layer carries, and every rule carried by a repository of this project that has a **checkout
   here** — each as `layer (project|repo owner/repo-a)`, `name`, `match` and `mode`, in the layer's
   own order. The
   repository half is bounded exactly as §2.3 is: a repository's rules come from its `fugaro.yaml` at
   its base branch in a remembered checkout, so a repository with no checkout on this machine
   contributes **no rows here** — it is §2.3's `unknown` row and its sentence, never a rule this
   section did not read. The project half is always complete, because the project layer is read for
   the project rather than for a checkout (D839). A rule whose mode is below the project default is
   flagged: it is a rule that lowers, which is legal (D21) and worth seeing.
3. **The resolved mode per repository**, over the project's `repos:` entries, one row each. With no
   `--path`, a row is the mode a run that touches no matching path would resolve:

   | Column | What it is |
   | --- | --- |
   | `repository` | `owner/repo-a` |
   | `mode` | the resolved mode |
   | `from` | the layer that won: `area`, `repo`, `project` or `default` — `internal/rigor`'s own vocabulary, minus `request` (no run is being launched) and `reached` (no run has a history) |
   | `area` | the strictest area rule that matched `--path`, with its own name and mode; `-` when `--path` matched none, `(no paths declared)` when `--path` was not given |
   | `source` | `checkout <path>` when the repository's `fugaro.yaml` was read at its base branch from a remembered checkout, or `unknown (no checkout on this machine)` |

   A repository with no checkout on this machine is **never guessed** (D839). Its row reads
   `unknown | -` in the `mode` and `from` columns, with the sentence "no checkout on this machine:
   this repository's own `fugaro.yaml` was not read, so its mode may be lower or higher than the
   project default" — the project default is §2.1's line, not a value printed in this row as if it
   were the repository's answer. In `--json` the repository member's `mode` and `from` are
   **absent**, not `null` and not a sentinel word, and `source` is the string `"unknown"` (it is
   `"checkout"`, with the row's `path` member, when the file was read); the schema therefore requires
   `repository` and `source` on a repository row and leaves `mode` and `from` optional, and a
   consumer that finds them absent prints the same sentence the text output prints. Absent is the
   only spelling that cannot be read as an answer: `null` and `"unknown"` are both values a later
   version could give a meaning, and a reader would then read this machine's missing checkout as the
   repository's mode.
4. **The floors, and what they hold.** The review-level floor the published policy carries
   (`localcfg.Config.ReviewFloor`, D722/G1), per mode, with the line that it is outside every layer
   and this command cannot lower it — and, naming where a floor is changed, **`fugaro rigor floor`
   (SD10: `show`, `set`, `unset`), which is a subcommand of this family and not a flag on `set`**
   (SD10; reconciliation §4). Then one line stating that the mode floors (the sensitive-path set of
   D8/D18/D21, `docs/rigor-modes` plan T1.5) are **not built yet**, so nothing is shown for them;
   the task that builds them adds them to this section (E2).

`--json` prints one document, `{"version": 1, "project": ..., "layer": {...}, "default": {...},
"areas": [...], "repositories": [...], "floors": {...}}`, whose members are only ever added to
within a version (D835). It has a JSON Schema in `schemas/` like the other wire formats.

`project` is the project's name, and `default`, `areas`, `repositories` and `floors` are §2.1–§2.4
in that order, with the members those sections name. **`layer` is the one member with no text
section of its own**, and it is not decoration: it says *which* layers were read and how they stand
relative to each other, which is the input the four sections are resolved from and what §2.1 reports
in prose as its `unpublished` line. It carries five members:

| Member | What it is |
| --- | --- |
| `published` | the project layer this document was resolved against: `{"generation": N, "sha256": "..."}` — the same two values §2.1 prints in text. **Absent**, not `null`, when the project has never published one, the rule §2.3 already sets for `mode` and `from` |
| `local` | §1's local layer file: `{"path": "...", "sha256": "..."}`. **Absent**, not `null`, when the file does not exist |
| `source` | where `published` was read from: `"bucket"` or `"cache"` — the two the paragraph above names, the distinction §2.1 carries in text as the cache note |
| `note` | the read's own warning, verbatim: the resolver's `foundLayer.Note` (`internal/cli/layer_resolve.go`), `""` when the bucket answered. On `"cache"` it is the sentence saying so and how old the copy is ("using the cached project layer of `<project>`, `<N>` days old"); when the value is unknown it is the reason, not a guess. It is the text §2.1 prints beside the value, so a consumer reading `"source": "cache"` can say **why** the bucket was not the source instead of reporting a bare enum |
| `unpublished` | `true` only when both `published` and `local` are present **and** their `sha256`s differ; `false` otherwise, because with either absent there is nothing to compare. §2.1's `unpublished` line prints exactly when this is `true`, so the two never disagree — and `fugaro config layer` and doctor decide the same state from the same two sha256s. **The boolean is the state, not its cause**, and a consumer that wants the cause compares `published.generation` with the local project config's recorded `layer_generation` (D854 answered): equal means a **local edit** — the owner's change, to publish — and a recorded value older than the published one means a **stale copy from another machine**, to re-seed, which is the distinction §2.1's line prints in text |

The member is provenance and nothing else: `show` neither writes nor fetches (D839), so `layer`
describes the bytes it read, and a consumer can tell a stale local file from a stale published
object without a second command. `local` being absent is the state `set` refuses on (§3 step 4) and
`update` seeds (D831), so the two readings are the same fact told by two commands.

## 3. `fugaro rigor set <mode>`

Edits **the one local layer file** and nothing else (D830, answered): `defaults.rigor.default`
becomes `<mode>`. In order:

1. **Validate the vocabulary.** `<mode>` must be one of `internal/rigor.Modes` (`poc`, `startup`,
   `pro`, `critical`, `hotfix`); a near miss ("Pro", "critical ") is refused with the vocabulary in
   the message, as `fugaro run --mode` refuses it.
2. **Refuse `hotfix`.** `hotfix` is not a default in any layer (D834, D12): it is off the axis,
   ranks below every on-axis mode, and is reached by an operator's own `fugaro run --mode hotfix`.
   The refusal names that command and never offers a flag that would write it. This refusal is owed
   on its own terms: D12's base-branch opt-in does not exist today (F13), and nothing here closes
   that gate or may be read as evidence that it exists.
3. **Write nothing in a coding agent's session.** The target file is outside every repository, in
   the owner's own config home, so `set` and `unset` of the project layer are refused where
   `fugaro config publish` is refused (`agentenv.Marker`, F15), and in the same words the `models`
   commands use (SD9, D789 there): the command **prints the diff and the command it would have run,
   and writes nothing** — `--yes` included, since `--yes` answers a prompt and not this refusal.
   `show` runs anywhere, and §4's `--repo R` form writes a repository's own file and is allowed: an
   agent may edit the tree it was given.
4. **Resolve the file.** It is §1's one path, `~/.config/fugaro/projects/<project>-layer.yaml`:
   there is no `--layer` flag and no config key for it (D830, answered), so there is nothing to
   resolve and nothing to record **of the path** — D854's *generation* record is another question
   and a different one, answered by the owner on 2026-10-31 (the generation is recorded) and R1's to
   build; it is not a record *of this path*. Two absences are not the same absence:
   - the file does not exist and the project has **no** published layer: it is created as a
     **project layer document**, in the published layer's own format, carrying the layer's three
     anchor keys (`version`, `project`, `gcp_project` — `layerTopKeys` in `internal/config/layer.go`)
     **plus the `defaults:` block the command is there to write** — the created document already
     carries `defaults.rigor.default: <mode>`, so what is published is the change that was asked for
     and not an empty shell. **The template is the layer format, not the local project config.** The
     three anchor *values* come from the installation's local project config
     (`internal/localcfg.Config`: its `Version`, `Name` and `GCPProject`), and nothing else of that
     file does: it holds keys the layer does not have (`region`, `runs_bucket`, `base_images`,
     `image_source`, `backend`), and the layer parser refuses any top-level key outside its own set
     (`config.ParseProjectLayer`'s "is not a project layer key"), so copying the local config into
     this file would write a document that does not parse at all. The three anchors alone would
     parse — the layer validator (`internal/config/layer.go`'s `validateLayer`) requires no
     `defaults:` at all — which is exactly the trap: they would publish a layer whose only content
     is a project name, and the owner's edit would then have to be a *second* command. Because there
     is nothing to lose, the file is created with the edit already in it;
   - the file does not exist while the project **has** a published layer: that is a refusal, not a
     creation. Publishing a fresh, nearly empty layer over the project's real one is the one mistake
     this command must not make, and the remedy is named in the message: run **`fugaro update`**,
     whose `Layer` stage seeds the file from the published layer (D831), and then set the mode. (A
     `config layer pull` that only fetched the published object would be a second door for the same
     job; this design does not add one and does not name a command it cannot offer.)
5. **Check the result.** The edited document must `config.ParseProjectLayer` cleanly, against the
   same anchor the publisher uses, and must pass `Area.Validate` on every rule it touches.
6. **Show, confirm, back up, write.** The command prints the line diff, asks for confirmation at a
   terminal (`--yes` confirms outside a terminal), backs up the old file to `<file>.bak-<UTC
   timestamp>` (mode 0600) and writes the new one atomically (temporary file, rename, mode 0600) —
   through the **shared write helper** the settings design's S5 lands in
   **`internal/cli/settings_write.go`** (README, the joint order), not through a copy of it. The
   behaviour is not new: `lineDiff`, `backupFile` and `writeFileAtomic`
   (`internal/cli/init.go`) already do exactly this and `fugaro restore` already calls them, so S5's
   task is to make that one path every writer uses — the diff, the confirmation, the agent-session
   refusal, the backup and the atomic write in one place — and this command is one of its callers.
   Rigor builds no `rigor_write.go` of its own: a second copy of the confirmation and the refusal is
   how the two drift apart, and D830's whole point is that there is one of each.
7. **Hand off.** It prints `next: fugaro update`, which publishes it (D831).

**Lowering is the sensitive direction.** When the new mode ranks below the project's current
resolved default, the confirmation is the **typed project name**, as `fugaro project repos rm`
asks (`confirmTypedStrict`), and `--yes` does not stand in for it (D832). Raising, or setting a
mode equal to the current one, is an ordinary y/N. The command always prints, next to the diff, the
review floor, the sentence that this command cannot lower it, and the command that can —
**`fugaro rigor floor`** (SD10).

## 4. `fugaro rigor set <mode> --repo R`

Edits the repository's own `fugaro.yaml` in its checkout's working tree and nothing else (D833),
exactly as `fugaro models use` does (D781 there):

- It resolves the checkout (the current directory when it is that repository's checkout, else the
  remembered one) and refuses when there is none, naming remedies that need no flag.
- It sets `rigor.default: <mode>`, inserting the `rigor:` block when the file has none, keeping
  every comment and every other byte (the same line-edit shape `setGCPProjectLine` uses).
- `hotfix` is refused here too (D834).
- A mode **lower** than what the repository resolves today asks for the typed project name; equal or
  higher asks y/N.
- **A coding agent's session cannot answer the typed name, and it is not asked to.** The file is
  inside the repository and an agent may edit it, so the write is not refused the way §3's is — but a
  lowering here needs a confirmation only the owner can give, so in that session the command prints
  the diff and the command it would have run and **writes nothing** (SD9, F15's wording in §3 step
  3); `--yes` is not an answer to a typed name either way (D832). Raising, or setting an equal mode,
  is an ordinary y/N an agent's session can answer.
- It prints the diff and asks for confirmation (`--yes` outside a terminal). It **never** commits,
  pushes or opens a pull request: the change reaches runs when the user's branch is merged to the
  base branch, which is what every run reads (D25).
- `show` runs anywhere.

## 5. `fugaro rigor set <mode> --area <glob> [--name NAME]`

Edits an area rule in the project layer's `defaults.rigor.areas`, or — with `--repo R` — in that
repository's own `rigor.areas` (D838):

- The rule is addressed by its **glob**. A glob the target layer already carries has its `mode`
  replaced; a glob it does not carry is **added**, and then `--name` is required, because a rule
  that cannot be named cannot be explained to the person reading the pull request it governed
  (`Area`'s own reason). `--name` on an existing rule renames it.
- A second rule with the same glob in one layer is refused by the command: the loader does not
  check it today, and two rules that match one path resolve by `Strictest`, silently discarding the
  weaker one's intent (F14).
- `hotfix` is refused.
- The glob must pass `Area.Validate` — repository-relative, no `..`, a `doublestar` pattern, at most
  `MaxAreaPattern` characters; the name must pass `areaNameRE`, at most `MaxAreaName` characters —
  and the layer must carry at most `MaxAreas` rules.
- Confirmation, backup and atomic write are `set <mode>`'s. A rule whose mode is below the project
  default asks for the typed project name (D832).

## 6. `fugaro rigor unset [--repo R] [--area <glob>]`

The reverse, through the same check, diff, backup and atomic write (D837):

- `unset --area <glob>` removes that rule from the target layer, and says which layer decides those
  paths again.
- `unset` with no `--area` removes the `default` key of the target layer (`defaults.rigor.default`
  in the project layer, `rigor.default` in a repository's file), so the layer below decides again.
- **It writes nothing in place of what it removes.** Writing `pro` instead would freeze Fugaro's
  built-in default into the owner's configuration and be indistinguishable from a deliberate pin;
  removing the key is the only spelling that says "the layer below decides".
- **Which way an `unset` moves the mode depends on where the removed value sat.** Removing a value
  that sat **above** what now decides **lowers** it — `critical` removed while the layer below says
  `pro` means the project reviews less — and that is the sensitive direction: it asks for the typed
  project name, by the same comparison `set` uses. Removing a value that sat **below** what now
  decides **raises** it — `startup` removed while `pro` now decides means the project reviews more
  — and that is an ordinary y/N. The comparison is `Rank`'s, the same one `set` uses for its own
  lowering; this command does not have a second one.
- Nothing to remove is a **no-op with a sentence and exit 0** ("the project layer sets no default;
  `pro` (Fugaro's own default) decides"), so a script that unsets twice is not a failed run. When
  the target file itself does not exist, that is the same answer, not an error.

## 7. `--json` stability, and what a consumer may rely on

`fugaro rigor show --json` is a versioned document (D835):

- `"version": 1` at the top, and a JSON Schema at `schemas/rigor-show.schema.json`, loaded and
  exercised by `schemas/schemas_test.go` like every other wire format.
- Within a version, **members are only added**. A renamed or re-meant member is a new member under
  a new name and the old one is kept (`CLAUDE.md`'s no-semantic-field-changes rule); a change that
  removes or retypes one bumps `version`.
- `from` is bounded by `internal/rigor`'s own set: the four layers a resolution can name here
  (`area`, `repo`, `project`, `default`). `request` and `reached` never appear: nothing in this
  command launches a run, so neither can be true of its answer. A reader that sees a value outside
  that set is reading a document from a newer CLI.
- A repository whose own block could not be read carries **no** `mode` and **no** `from` member
  (§2.3): absence is the third state this document has, beside the four words and a value from a
  newer CLI, and it is the only spelling that cannot be read as an answer. `source` is present on
  every repository member, so a consumer always has the machine-readable reason for the absence.

## 8. The console

No console task is in this plan (D836). The console reads CLI documents and never reads
`~/.config/fugaro` or the runs bucket itself; when its project view ships (`docs/coordinator` plan
P6, not built), it reads `fugaro rigor show --json` the way it reads every other document, for a
one-line read-only display of the project default per installation. Nothing on that screen writes,
and the shape it reads is documented in `console/docs/json-contract.md` by the task that builds the
screen — never here.

## 9. Migration of a hand-written layer and a hand-written block

A project layer written by hand before these commands exist must keep working, and a repository
whose `fugaro.yaml` already carries a `rigor:` block must be left alone by a command that does not
name it:

- **Not a re-marshalled struct, and not a re-emitted tree either (D830).** A struct round trip
  (`config.ProjectLayer`) drops comments and key order outright. A `yaml.Node` tree round trip keeps
  comments and key order but re-emits the parts it did not change with the encoder's own quoting and
  indentation, so it is not byte-preserving either. The write is therefore a **splice**: the document
  is parsed into a node tree only to *locate* the mapping or the sequence item to change, and the
  bytes that node occupies are replaced in place, leaving every other byte — comments (the block's
  own commented examples and any commented entry included), key order, quoting style, blank lines and
  unknown-to-this-command keys — exactly as it was.
- **One editor, three documents: `internal/yamledit`.** The splice is not written for rigor: it is
  **the** shared comment-preserving YAML editor this CLI has, the package `internal/yamledit` whose
  generic operations and owning task are named once in the README ("the joint order, and the rulings
  the rigor and settings designs share"), and the settings work and the `models` plan call the same
  package — never a second copy under a private name. The three documents it serves are the **local
  project config** (`~/.config/fugaro/projects/<project>.yaml`), the **local layer file** (§1's one
  path) and a repository's **`fugaro.yaml`**; the same `rigor:` shape appears in the last two, at
  `defaults.rigor` and at `rigor`, so what differs between them is the path the editor is pointed at,
  plus whether the edit is a scalar (`default`) or a sequence item (`areas`) — a distinction the node
  tree decides and a line-oriented editor cannot make.
- **Rigor adds a caller, not an editor.** `EditRigor(data []byte, at []string, ops ...RigorOp)
  ([]byte, error)` is a **thin caller** of `internal/yamledit`: it maps this work's vocabulary
  (`SetDefault`, `RemoveDefault`, `SetArea`, `RenameArea`, `RemoveArea`) onto the package's generic
  operations and hands it the anchor, and the generic half — locate the node, splice the bytes, keep
  every other byte — has exactly one home. `setGCPProjectLine` (`internal/cli/init_fugaroyaml.go`) is
  the precedent for the splice on the simplest case, one scalar on one line; it is not a second
  strategy, and neither R5 nor `EditRigor` re-implements the locator. **The editor task owns
  `internal/yamledit`**; R2 owns `EditRigor`, R4 and R5 are two of its callers, and their tests differ
  in what they assert — bytes preserved around a scalar versus around a list item — rather than in the
  write path they use.
- The edited document is re-parsed — `config.ParseProjectLayer` for the project layer, the
  repository file's own config parse for `--repo R` — and must decode to the old document plus
  exactly the intended change (a test compares the two decoded layers field by field). This is one of
  **two** checks, not one: parsing proves the result is *valid*, and it cannot prove the comments
  survived — comments are discarded before a struct ever sees them. Preservation is asserted
  separately, by comparing the bytes around the edit (R2's red step is exactly that pair: the same
  fixture through a struct round trip loses its comments, while the splice keeps them).
- A hand-written `defaults.rigor` with an area rule this command did not create round-trips
  unchanged through a `set` of the default and through an `unset` of another rule, including its
  name, its glob and its comments. A repository's `rigor:` block is never touched by a project-level
  `set`, and vice versa.
- Key order follows the file: `set` writes into the existing `rigor:` mapping, adding `default` after
  the keys already there rather than reordering them, and creating the mapping only when the file
  has none.

## 10. Safety boundaries this work does not move

- **No secret** is read, printed or stored: no key, token or credential is named by any of these
  forms.
- **No cloud write** by `show`, `set` or `unset`. The one cloud write this design adds is `fugaro
  update`'s own `Layer` stage, and it is `update`'s write: the same confirmation, the same
  executable-key gate and the same agent-session refusal every other stage of that command carries.
  The **seeding** half of that stage is not a cloud write at all: it reads the published layer and
  writes §1's local file, inside `update`, where the agent-session refusal is already in force.
- **The floors are read and shown here, never written here.** `set`/`unset` write a default; the
  security floors (D8, D18, D21) and the review floor (D722/G1) stay outside every layer, and a mode
  written below one changes nothing at the floor. When `show` can see a floor it prints it, and both
  `show` and `set` name **`fugaro rigor floor`** as the command that changes one (SD10); this
  command family's `set` never grows a floor flag.
- **`hotfix` is never a default** in any layer (D834), in any of the four forms. What `hotfix` is
  today is a **mode value and nothing more**: it resolves like `pro`, no layer defaults to it, and no
  form here writes it. The gate D12 names does not exist (F13), **this design does not build it**,
  and nothing in this folder may be read as evidence that it does: it stays its own future design
  task, with its own independent review, and nothing lands before the owner's answer.
- **A project layer that changes executable keys is still gated.** `update`'s layer stage publishes
  through the same `publishLayer` the explicit door uses, so a change to `commands.*` or
  `image.*` still needs `--executable-changes`; the command that writes rigor never touches those
  keys, and the stage says so when the file it is about to publish carries a change to them.
