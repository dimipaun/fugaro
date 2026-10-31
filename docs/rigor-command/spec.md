# `fugaro rigor`: the spec

The commands below are written to the recommended answer of each decision (D810–D819,
[reconciliation.md](reconciliation.md) §3). A decision answered the other way changes the paragraph
that names it. Repository names are placeholders (`owner/repo-a`, `owner/repo-b`); modes are
`internal/rigor`'s five words (`poc`, `startup`, `pro`, `critical`, `hotfix`).

## 1. The rule this serves

`fugaro update` is the whole upgrade (`CLAUDE.md`, HARD RULE). So:

- `fugaro rigor set` and `unset` **edit local files and read**. Neither writes the runs bucket, a
  job, a secret or anything else in the cloud (D810).
- The project default and the project's area rules reach every run through the **project layer**,
  so the change reaches the cloud through the next `fugaro update`, whose layer stage publishes the
  local layer file (D811). Each write command ends by printing `next: fugaro update` and nothing
  else — no `config publish`, no per-repository `update --repo`.
- `--repo R` writes the repository's own `fugaro.yaml` in its checkout's working tree, which the
  user's own branch and review carry; the command never commits or pushes (D813).

## 2. `fugaro rigor show [--json] [--repo R] [--path P]... [--offline]`

Offline by default in the sense that matters: it reads this machine's project config, the project
layer (the published object, or the local layer cache when the bucket cannot be reached, saying
which it used) and each repository's `fugaro.yaml` at its base branch in this machine's remembered
checkouts. It writes nothing. `--path P` (repeatable) is the same declaration `fugaro run --path`
takes, and it is the only way `show` can answer the area question the way a run will.

The output is four sections.

1. **The project default.** `defaults.rigor.default` as the project layer resolves it, the layer's
   generation and sha256, and **where the value comes from**: `project` (the layer sets it) or
   `default` (the layer sets none, so Fugaro's own default `pro` decides). When the local layer file
   differs from the published object, a `drift` line says so, names both sha256s and adds
   `fugaro update` as the way to publish it — the state the layer stage of D811 closes.
2. **The area rules of both layers.** Every rule the project layer carries and every rule a
   repository of this project carries, each as `layer (project|repo owner/repo-a)`, `name`, `match`
   and `mode`, in the layer's own order. A rule whose mode is below the project default is flagged:
   it is a rule that lowers, which is legal (D21) and worth seeing.
3. **The resolved mode per repository**, over the project's `repos:` entries, one row each. With no
   `--path`, a row is the mode a run that touches no matching path would resolve:

   | Column | What it is |
   | --- | --- |
   | `repository` | `owner/repo-a` |
   | `mode` | the resolved mode |
   | `from` | the layer that won: `area`, `repo`, `project` or `default` — `internal/rigor`'s own vocabulary, minus `request` (no run is being launched) and `reached` (no run has a history) |
   | `area` | the strictest area rule that matched `--path`, with its own name and mode; `-` when `--path` matched none, `(no paths declared)` when `--path` was not given |
   | `source` | `checkout <path>` when the repository's `fugaro.yaml` was read at its base branch from a remembered checkout, or `unknown (no checkout on this machine)` |

   A repository with no checkout on this machine is **never guessed**: its row shows the project
   default plus a marker that its own block could not be read, and says that the answer may be lower
   or higher (D819).
4. **The floors, and what they hold.** The review-level floor the published policy carries
   (`localcfg.Config.ReviewFloor`, D722/G1), per mode, with the line that it is outside every layer
   and this command cannot lower it. Then one line stating that the mode floors (the sensitive-path
   set of D8/D18/D21, `docs/rigor-modes` plan T1.5) are **not built yet**, so nothing is shown for
   them; the task that builds them adds them to this section (E2).

`--json` prints one document, `{"version": 1, "project": ..., "layer": {...}, "default": {...},
"areas": [...], "repositories": [...], "floors": {...}}`, whose members are only ever added to
within a version (D815). It has a JSON Schema in `schemas/` like the other wire formats.

## 3. `fugaro rigor set <mode>`

Edits **the project layer's local source file** and nothing else (D810): `defaults.rigor.default`
becomes `<mode>`. In order:

1. **Validate the vocabulary.** `<mode>` must be one of `internal/rigor.Modes` (`poc`, `startup`,
   `pro`, `critical`, `hotfix`); a near miss ("Pro", "critical ") is refused with the vocabulary in
   the message, as `fugaro run --mode` refuses it.
2. **Refuse `hotfix`.** `hotfix` is not a default in any layer (D814, D12): it is off the axis,
   ranks below every on-axis mode, and is reached by an operator's own `fugaro run --mode hotfix`.
   The refusal names that command and never offers a flag that would write it.
3. **Resolve the file.** `--layer FILE` when given; else the path the local project config records;
   else `~/.config/fugaro/projects/<project>-layer.yaml`. A file that does not exist **and** a
   project with no published layer is created from the local config's own anchors (`version`,
   `project`, `gcp_project`). A file that does not exist while the project **has** a published layer
   is refused: publishing a fresh, nearly empty layer over the project's real one is the one
   mistake this command must not make (D810).
4. **Check the result.** The edited document must `config.ParseProjectLayer` cleanly, against the
   same anchor the publisher uses, and must pass `Area.Validate` on every rule it touches.
5. **Show, confirm, back up, write.** The command prints the line diff, asks for confirmation at a
   terminal (`--yes` confirms outside a terminal), backs up the old file to `<file>.bak-<UTC
   timestamp>` (mode 0600) and writes the new one atomically (temporary file, rename, mode 0600) —
   `backupFile` and `writeFileAtomic` (`internal/cli/init.go`), the pair `fugaro restore` and the
   `models` plan already use.
6. **Hand off.** It prints `next: fugaro update`, which publishes it (D811).

**Lowering is the sensitive direction.** When the new mode ranks below the project's current
resolved default, the confirmation is the **typed project name**, as `fugaro project repos rm`
asks (`confirmTypedStrict`), and `--yes` does not stand in for it (D812). Raising, or setting a
mode equal to the current one, is an ordinary y/N. The command always prints, next to the diff,
the review floor and the sentence that this command cannot lower it.

## 4. `fugaro rigor set <mode> --repo R`

Edits the repository's own `fugaro.yaml` in its checkout's working tree and nothing else (D813),
exactly as `fugaro models use` does (D781 there):

- It resolves the checkout (the current directory when it is that repository's checkout, else the
  remembered one) and refuses when there is none, naming `--layer`-free remedies only.
- It sets `rigor.default: <mode>`, inserting the `rigor:` block when the file has none, keeping
  every comment and every other byte (the same line-edit shape `setGCPProjectLine` uses).
- `hotfix` is refused here too (D814).
- A mode **lower** than what the repository resolves today asks for the typed project name; equal or
  higher asks y/N.
- It prints the diff and asks for confirmation (`--yes` outside a terminal). It **never** commits,
  pushes or opens a pull request: the change reaches runs when the user's branch is merged to the
  base branch, which is what every run reads (D25).
- An agent's session is allowed: this file is inside the repository, and an agent may edit it
  anyway. `show` runs anywhere.

## 5. `fugaro rigor set <mode> --area <glob> [--name NAME]`

Edits an area rule in the project layer's `defaults.rigor.areas`, or — with `--repo R` — in that
repository's own `rigor.areas` (D818):

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
  default asks for the typed project name (D812).

## 6. `fugaro rigor unset [--repo R] [--area <glob>]`

The reverse, through the same check, diff, backup and atomic write (D817):

- `unset --area <glob>` removes that rule from the target layer, and says which layer decides those
  paths again.
- `unset` with no `--area` removes the `default` key of the target layer (`defaults.rigor.default`
  in the project layer, `rigor.default` in a repository's file), so the layer below decides again.
- **It writes nothing in place of what it removes.** Writing `pro` instead would freeze Fugaro's
  built-in default into the owner's configuration and be indistinguishable from a deliberate pin;
  removing the key is the only spelling that says "the layer below decides".
- Removing a project default **raises** the review level only where the removed value was below
  what now decides; removing it where it was a *lowering* is the sensitive direction and asks for
  the typed project name, by the same comparison `set` uses.
- Nothing to remove is a **no-op with a sentence and exit 0** ("the project layer sets no default;
  `pro` (Fugaro's own default) decides"), so a script that unsets twice is not a failed run. When
  the target file itself does not exist, that is the same answer, not an error.

## 7. `--json` stability, and what a consumer may rely on

`fugaro rigor show --json` is a versioned document (D815):

- `"version": 1` at the top, and a JSON Schema at `schemas/rigor-show.schema.json`, loaded and
  exercised by `schemas/schemas_test.go` like every other wire format.
- Within a version, **members are only added**. A renamed or re-meant member is a new member under
  a new name and the old one is kept (`CLAUDE.md`'s no-semantic-field-changes rule); a change that
  removes or retypes one bumps `version`.
- `from` is bounded by `internal/rigor`'s own set: the four layers a resolution can name here
  (`area`, `repo`, `project`, `default`). `request` and `reached` never appear: nothing in this
  command launches a run, so neither can be true of its answer. A reader that sees a value outside
  that set is reading a document from a newer CLI.

## 8. The console

No console task is in this plan (D816). The console reads CLI documents and never reads
`~/.config/fugaro` or the runs bucket itself; when its project view ships (`docs/coordinator` plan
P6, not built), it reads `fugaro rigor show --json` the way it reads every other document, for a
one-line read-only display of the project default per installation. Nothing on that screen writes,
and the shape it reads is documented in `console/docs/json-contract.md` by the task that builds the
screen — never here.

## 9. Migration of a hand-written layer and a hand-written block

A project layer written by hand before these commands exist must keep working, and a repository
whose `fugaro.yaml` already carries a `rigor:` block must be left alone by a command that does not
name it:

- The write edits the YAML document tree, not a re-marshalled struct (D810): comments (including
  the block's own commented examples and any commented entry), key order, quoting style and
  unknown-to-this-command keys are kept byte for byte. `config.ProjectLayer`'s round trip does not
  preserve them, so it is not used for the write.
- The edited document is re-parsed with `config.ParseProjectLayer` and must decode to the old layer
  plus exactly the intended change (a test compares the two decoded layers field by field).
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
  update`'s own layer stage, and it is `update`'s write: the same confirmation, the same
  executable-key gate and the same agent-session refusal every other stage of that command carries.
- **The floors are read and shown, never written.** `set`/`unset` write a default; the security
  floors (D8, D18, D21) and the review floor (D722/G1) stay outside every layer, and a mode written
  below one changes nothing at the floor. When `show` can see a floor it prints it; it never offers
  a flag that writes one.
- **`hotfix` is never a default** in any layer (D814), in any of the four forms.
- **A project layer that changes executable keys is still gated.** `update`'s layer stage publishes
  through the same `publishLayer` the explicit door uses, so a change to `commands.*` or
  `image.*` still needs `--executable-changes`; the command that writes rigor never touches those
  keys, and the stage says so when the file it is about to publish carries a change to them.
