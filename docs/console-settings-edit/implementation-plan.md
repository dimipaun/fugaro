# Editing the Project tab's settings from the console: implementation plan

**Nothing here is approved.** Every task below starts only when the owner approves it, and an answer
to a decision in [reconciliation.md](reconciliation.md) is not an approval. This document adds no
code, no schema, no console source, no fixture and no workflow: it names the files the tasks will
touch and edits none of them.

**The dependency that orders everything: S5 must merge first.** Every edit here spawns `fugaro
settings set|unset`, which is pull request **#1525**, open in review. Nothing in this plan can be
built, tested or even written against a CLI that does not have it, and the four prerequisites below
are changes **to that same command**, so they land on top of it rather than beside it. If the owner
takes **D1107**'s fallback (**B**), the widening half of C3 and C4 and all of P4 fall away and the
rest of the plan is unchanged.

## 1. The order, and what forces it

```
        S5 #1525 merges
              │
   ┌──────────┼──────────┬──────────┐
   P1         P2         P3         P4          ← level 0, four independent CLI tasks
   │          │          │          │
   └────┬─────┘          │          │
        C1 ──────────────┘          │           ← the read, and the pencils' row set
        │                           │
        C2                          │           ← the pencil and the dialog's content
        │                           │
        C3 ─────────────────────────┘           ← the write, the dry run, the widening hook
        │
        C4                                       ← the hermetic e2e and the fixture
        │
        C5                                       ← the documents the fences live in
        │
        C6                                       ← the release notes, console 0.119.0
```

**Why P1 is first and is not optional.** `fugaro settings` registers no `--project` and no
`--config` and fills neither `Project`, `Config` nor `Checkout` in its `localcfg.SelectInput`
(**F12**), while the console's child carries no `FUGARO_PROJECT`/`FUGARO_CONFIG` and no terminal
environment beyond `CHILD_ENV_KEYS` (**F4**). Until P1 lands, **every** `settings` argv the console
could spawn is refused on a machine with two installations, and on a machine with one it silently
reads the only installation — which is the worse failure, because it looks like it worked.

**Why P2 is first and not later.** The console's row set is the CLI's answer and it keeps no list of
its own (**D1102**), so there is no pencil to draw before the read carries `writable`, `owner` and
`target_layer`. Drawing the pencils from a copy of `settingsGenericKeys` first and replacing it
later is the fourth-copy defect the settings design's own F12 exists to prevent, and it would ship.

**Why P3 and P4 are separable.** P3 gives the console a machine-readable widening classification
(**D1106**), which is needed by **every** edit — it is what decides the dialog's shape. P4 gives it
the ability to **perform** a widening (**D1107**). A console that can only narrow is a coherent,
shippable state under D1107's option B, so P4 is a task whose absence degrades the feature rather
than blocking it, and the plan says so rather than pretending the two are one task.

**Why C5 is not first.** Three of the four documents C5 changes are the **fences themselves** —
`docs/design/desktop-console.md` §5.1 rules 5 and 6 and the new rule 12. A fence document written
before the code it fences is a description of an intention; the console rule requires the count, the
table and the code to move in **the same commit**, so C5's changes to those three are made inside C1,
C2 and C3 and C5 holds only what is left (§6 below says which).

## 2. Shared files

These files are touched by more than one task here **and** by console work outside this design, so
each task that writes one says so, and a merge conflict here is a semantic conflict rather than a
textual one.

| File | What this design does to it | Who else owns it |
| --- | --- | --- |
| `console/src/main/ipc.ts` | `CONSOLE_OPERATIONS` gains `settings.read` (C1) and `settings.edit` (C3); §5.1 rule 5's count moves 24 → 25 → 26 in the same commits; the never table is unchanged | every console feature; the file is the console's largest reviewable artifact |
| `console/src/main/spawn.ts` | `argvFor`'s table gains two rows with `takesProject: true` (C1, C3) | every console read |
| `console/src/main/confirm.ts` | generalized from one view to a union, so the launch window and the settings window are one implementation (**D1115**, C3) | the launch (rule 11) |
| `console/src/preload/index.ts`, `console/src/preload/confirm.ts` | the written-out method list gains the two methods (C1, C3); the confirm preload gains the tick's field (C3) | every console feature; `console/test/unit/preload-surface.test.ts` pins the duplication |
| `console/src/renderer/entry.ts` | the Project tab's section wiring and the pencil's event handling (C1, C2) | every screen; the file is the console's second-largest |
| `console/src/renderer/theme/screens.css` | the new panels' class hooks and the pencil's states (C2) | every screen; `docs/design/console-styling.md` §2.5 names them |
| `console/test/fixtures/bin/fugaro` | two new arms: `settings … --json` and `settings set\|unset …`, the second recording its argv (C4) | every console e2e |
| `internal/cli/settings.go`, `settings_read.go`, `settings_set.go`, `settings_write.go` | P1, P2, P3, P4 — all four are changes to the command S5 lands | S5 (#1525), which owns the files until it merges |

## 3. Gates (every task)

**CLI tasks (P1–P4):** `go build ./... && gofmt -l . && go vet ./...` (gofmt prints nothing); the
task's named tests with `GOFLAGS=-p=2 go test ./internal/cli -run '<names>' -count=1`; and
`GOFLAGS=-p=2 go test ./internal/cli -run Docs -count=1 -timeout 40m` whenever a doc or a schema
changed. **Never the whole `internal/cli` package.**

**Console tasks (C1–C4):** `npm run typecheck`; `npm run test:unit -- --maxWorkers=2`; `npm run
test:e2e`. Goldens are **skipped in CI** (`FUGARO_CONSOLE_GOLDENS: skip`) and are re-recorded only by
the coordinator on the owner's machine — **no task here re-records one**.

**The docs test:** `internal/cli/docs_console_settings_edit_test.go` pins this folder. It runs under
the `Docs` filter, which is the last gate of the run that lands any of these tasks.

**Security flag.** A task marked **SEC** carries `SECURITY-SENSITIVE: independent review mandatory`
and may not merge without that review. Every task that moves §5.1 rule 5 or rule 6 is **SEC** by the
console rule; P1 and P4 are **SEC** on their own merits (§4 says why each).

## 4. Prerequisite CLI tasks

### P1. `fugaro settings` can be aimed at an installation (F12). **SEC** — **size M**

- **Files:** `internal/cli/settings.go` (`--project` and `--config` on the group, spelled and
  documented exactly as §3.4 rule 3 spells them for every other command, and passed down to the two
  writes), `internal/cli/settings_read.go` (`readSettings` fills `Project`, `Config` and `Checkout`
  in its `localcfg.SelectInput` from the flags), `internal/cli/settings_set.go`
  (`settingsSelection` does the same for the write path), `internal/cli/settings_aim_test.go` (new),
  and `internal/localcfg/select.go` **only if** the selector's step order has to change — which this
  plan does not expect and which the task must not do without saying so, because that file is shared
  with every command that resolves a project.
- **Interface:** `fugaro settings [KEY|GLOB ...] --project NAME [--config PATH]` and `fugaro settings
  set|unset KEY … --project NAME [--config PATH]`; the same two flags, the same precedence, the same
  `--config`-wins-over-`--project` rule `localcfg.Select` already implements at steps 1 and 2.
- **Red step:** `TestSettingsAimedAtAnInstallationReadsThatInstallationsFile` fails on today's tree —
  with two project configs in a `t.TempDir()` home and no `FUGARO_PROJECT`, the command refuses.
- **Tests:** `TestSettingsAimedAtAnInstallationReadsThatInstallationsFile`,
  `TestSettingsSetAimedAtAnInstallationWritesThatFile`, `TestSettingsAimedAtAnUnknownInstallationIsRefused`,
  `TestSettingsAimedAtTwoInstallationsIsRefused` (the `--project` and `--config` that disagree),
  `TestSettingsAimDoesNotChangeTheUnaimedRead`.
- **Why SEC:** the flag decides **which file a write lands in**. A selector that fell through to
  step 6 on a machine with one installation would write the wrong project's config with no error,
  which is the failure mode this whole plan is most exposed to.
- **Hermetic:** `t.TempDir()` config home, two seeded project configs, `internal/testutil`. No cloud,
  no network.

### P2. The read carries which rows are writable, by whom, and in which layer (**D1102**). **size S**

- **Files:** `internal/cli/settings_render.go` (`settingsJSON` gains three members — `writable`, a
  boolean; `owner`, the command family that writes the key; and `target_layer`, the layer
  `settings set` would write with no `--layer`), `internal/cli/settings_read.go` (`projectRow` and
  `repoRow` fill them), `schemas/settings.schema.json` (three members, added within version 1 —
  **no version bump**, D848), `internal/cli/settings_render_test.go`, `schemas/schemas_test.go`.
- **Where the facts come from, and why they cannot drift:** `writable` is `settingsGenericKey(row.Key)`
  — the same function `settings set` refuses on — and `owner` is `settingsFamilyCommands[row.Owner]`.
  Both are read from the S5 file rather than restated, so the read's answer and the write's refusal
  are one expression. `target_layer` is `settingsDefaultLayer(row)`.
- **The one case that needs a decision inside the task:** a key the generic path does not own **and**
  whose `Owner` names no family (installation plumbing, the settings design's own case) gets
  `writable: false` and an empty `owner`; the console then says the generic path does not write it and
  names nothing (**D1111**). The task asserts that shape rather than inventing a name for it.
- **Tests:** `TestSettingsJSONMarksTheGenericPathsRowsWritable`,
  `TestSettingsJSONNamesTheFamilyThatOwnsTheOthers`,
  `TestSettingsJSONOwnerMatchesTheRefusalSettingsSetPrints` (the row's `owner` is the command
  `settings set` names when it refuses that key — the anti-drift pin),
  `TestSettingsJSONTargetLayerMatchesWhatSetWrites` (a write with no `--layer` lands in the layer the
  read named), `TestSettingsJSONIsStillVersionOne`.
- **Why not SEC:** it adds members to a read's document and moves no fence. It is still reviewed, as
  everything in this pull request is.

### P3. `settings set|unset --dry-run --json`, and the classification the dialog turns on (**D1106**). **SEC** — **size M**

- **Files:** `internal/cli/settings_set.go` (the flag on both commands, and the document),
  `internal/cli/settings_write.go` (`applySettingsEdit` gains the dry-run arm: it resolves the
  target, builds the edit, classifies it and **returns before the confirmation, before the backup
  and before the write**), `schemas/settings-write.schema.json` (new: the dry-run document),
  `schemas/schemas_test.go`, `internal/cli/settings_dryrun_test.go` (new).
- **Interface:** `fugaro settings set KEY VALUE --layer L --dry-run --json` prints
  `{version, key, layer, path, changed, widening, why, old, new, refusal}`, writes nothing and leaves
  no backup. `--dry-run` without `--json` prints the same facts as the table the command already
  prints. `--dry-run` with `--yes` is refused (the two answer different questions).
- **The classification is S5's, not a second copy:** `widening` is the value of the existing
  `settingsTypedWrite` decision — the same call that decides whether the typed name is demanded — so
  a key added to `settingsCeilingKey` or a list rule changed in `settingsListSubset` changes the
  console's dialog with no console edit.
- **Red step:** `TestSettingsSetDryRunWritesNothing` fails on S5's tree (the flag does not exist) and
  `TestSettingsSetDryRunClassifiesAWideningAsAWidening` fails while the classification is only
  reachable through a prompt.
- **Tests:** `TestSettingsSetDryRunWritesNothing` (the file's bytes and mtime are unchanged, and no
  backup file appears), `TestSettingsSetDryRunClassifiesAWideningAsAWidening`,
  `TestSettingsSetDryRunClassifiesANarrowingAsANarrowing`,
  `TestSettingsSetDryRunRefusesASensitiveKey`, `TestSettingsSetDryRunRefusesAKeyTheGenericPathDoesNotOwn`,
  `TestSettingsSetDryRunJSONMatchesSchema`, `TestSettingsUnsetDryRunWritesNothing`,
  `TestSettingsSetDryRunNeedsNoTerminal` (**the property the console depends on**: the dry run is
  answerable with stdin closed, because the console's child has no terminal).
- **The parser question, measured here:** the task measures **U1** — that `set KEY --layer L -- --v`
  hands the value through as `args[1]` — and, if it does not, makes it true by construction
  (`Flags().SetInterspersed(false)`, or accepting the value only after `--`), because **D1104**'s
  whole safety argument rests on it.
- **Why SEC:** the flag is the difference between the console's dialog offering a narrowing and
  offering a widening, and getting it wrong in the widening direction is a limit raised behind a
  one-click dialog.

### P4. `--console-confirmed`: the widening hook, a deliberate weakening of D846 on one path (**D1107** A). **SEC** — **size M**

- **Files:** `internal/cli/settings_write.go` (`applySettingsEdit` accepts the flag in place of the
  typed name **only** when the console's marker variable is present in its environment),
  `internal/cli/settings_set.go` (the flag on `set` and `unset`), `console/src/main/spawn.ts`
  (`CHILD_ENV_KEYS` gains the one marker key — **this is the change to §5.1 rule 8's "nothing
  added"**, and it is why the task is SEC and why the console side of it is one line in C3),
  `internal/cli/settings_console_confirm_test.go` (new),
  `console/test/unit/spawn.test.ts` (the key list, whose count is pinned),
  `docs/settings-commands/reconciliation.md` (**the D846 entry gains the sentence that one path is
  no longer covered by it**).
- **The refusal set, which is what makes it narrow:** the flag is refused when the marker is absent;
  refused when `agentenv.Marker(os.Getenv)` reports a coding agent's session — the fence D846 exists
  for, unchanged; refused when the key is not one the typed path would have demanded it for (so the
  flag cannot be used to skip an ordinary confirmation); and refused together with `--dry-run`.
- **What it is honestly worth, written into the code's own comment and into rule 12:** it is **a UX
  gate, not a security boundary**. A local user who can run `fugaro` can set the marker variable
  themselves, and can edit the same file with an editor. What the hook preserves is the fence that
  matters — an agent's session cannot raise its own ceiling — and what it buys is that a widening
  from the console is one argv, built by the allow-list, behind a window whose yes comes from its own
  top frame. **The task writes that sentence; it does not imply otherwise anywhere.**
- **Tests:** `TestSettingsSetRefusesConsoleConfirmedWithoutTheMarker`,
  `TestSettingsSetRefusesConsoleConfirmedInAnAgentSession` (each of `agentenv.Markers`, one case
  each), `TestSettingsSetAcceptsConsoleConfirmedWithTheMarker`,
  `TestSettingsSetRefusesConsoleConfirmedOnANonWideningEdit`,
  `TestSettingsSetConsoleConfirmedStillWritesTheSameBytes` (the hook changes who confirms, never what
  is written), `TestSettingsSetStillDemandsTheTypedNameAtATerminal` (no marker, no flag: S5's
  behaviour is untouched), and — on the console side, in the same task —
  `console/test/unit/spawn.test.ts`'s child-environment vector, which carries the one new key and no
  other.
- **Why SEC:** it is the one task in this plan that weakens a rule, and the review is the point.
- **If the owner refuses it:** the task is not built, D1107's **B** applies, and C3's widening path
  draws the command to copy instead of a Confirm button.

## 5. Console tasks

### C1. The settings section, its read, and the pencils' row set (F1, F10, F15, D1102, D1114, D1117). **SEC** — **size L**

- **Files:** `console/src/main/ipc.ts` (`settings.read` in `CONSOLE_OPERATIONS`, its exact-argv row,
  and **rule 5's count 24 → 25 in the same commit**), `console/src/main/spawn.ts` (`argvFor`'s row,
  `takesProject: true`), `console/src/preload/index.ts` (the method),
  `console/src/renderer/screens/projects.ts` (the model: rows, panels, the pencil's subjects),
  `console/src/renderer/entry.ts` (the section, its own token and its parallel start),
  `console/src/renderer/theme/screens.css` (the panels' hooks, per `console-styling.md` §2.5),
  `console/test/unit/ipc.test.ts`, `console/test/unit/spawn.test.ts`,
  `console/test/unit/preload-surface.test.ts`, `console/test/unit/projects.test.ts`,
  `docs/design/desktop-console.md` §5.1 **rule 5 and rule 6, in this commit**,
  `console/docs/json-contract.md` §6.15 (the settings document, with the three fields P2 adds).
- **The argv, word by word:** `settings --json --project <name> [--config <path>]` — the group's own
  read, with the project flags §3.4 rule 3 gives every cloud command. **No `--offline`**: the console
  reads `state: unread: …` for the layers that could not be read and draws the row as unread, which
  is the honest answer and the one the operator can act on.
- **What the screen does with the document:** one row per setting, drawn in the panels the ask names;
  a row with `writable: true` and a non-empty `target_layer` gets a pencil; a row with
  `writable: false` gets the family name from `owner` in S5's own words and no pencil; a row with
  `sensitive: true` is drawn **with no value at all** (the CLI omits it) and no pencil (**D1111**).
- **Red step:** `projects.test.ts` fails while the section does not exist, and
  `spawn.test.ts`'s settings-read vector fails while the argv has no `--project`.
- **Tests:** the two allow-list tests (`ipc.test.ts`'s count and its never-entry test;
  `spawn.test.ts`'s exact vector), `preload-surface.test.ts` (the method is in both lists),
  `projects.test.ts` (the row set: a writable row has a pencil, an owned row names its family, a
  sensitive row has neither a value nor a pencil), `entry-wiring.test.ts` (the section starts in
  parallel with `budget show` and paints on its own token).
- **SEC because** it moves rule 5 and rule 6, and the two documents move in the same commit.
- **Hermetic:** the fake CLI (C4's arms land with this task's unit fixture, so the unit test reads a
  fixture document rather than a spawned child).

### C2. The pencil, and the dialog's content (D1108, D1109, D1113, D1116). **SEC** — **size M**

- **Files:** `console/src/renderer/screens/projects.ts` (the pencil: a real `<button>` with an
  accessible name naming the key), `console/src/renderer/entry.ts` (focus returns to the pencil when
  the window closes), `console/src/renderer/theme/screens.css` (the pencil's states — never by colour
  alone), `console/src/main/settingsview.ts` (new: the view the window draws, built from a dry-run
  document and the read's row, with no Electron in it, so it is unit-testable the way
  `confirmView` is), `console/test/unit/settingsview.test.ts` (new),
  `console/test/unit/projects.test.ts`.
- **The view carries, and nothing else:** the key; the old value; the new value; the layer written
  (`target_layer`); the layer that wins (`wins`); and, **only when they differ**, one sentence saying
  the edit will not change what runs (**D1113**) — which is a comparison of two fields the CLI
  printed, and which the task replaces with the dry run's own field if P3 grows one. Plus, for a
  widening, the CLI's `why` sentence; and, for every edit, the exact argv, field by field.
- **The two shapes (D1109):** a narrowing or a non-widening edit is one click with Confirm as the
  default button. A widening has **Cancel as the default button** and Confirm **disabled until the
  tick** labelled "I understand this raises a limit". The shape is chosen by the dry run's `widening`
  field, never by the console.
- **Red step:** `settingsview.test.ts` fails while the view does not exist; its widening cases fail
  while the Confirm button is enabled without the tick.
- **Tests:** `settingsview.test.ts` — the six fields, the "will not change what runs" sentence
  present exactly when the layers differ and absent when they do not, a sensitive row **never**
  reaching a view, the widening shape (Cancel default, Confirm disabled, enabled after the tick), the
  narrowing shape (one click), and the argv shown matching the argv the builder produces.
  `projects.test.ts` — the pencil's accessible name names its key, it is reachable in the tab order,
  and focus returns to it.
- **SEC because** the dialog is the confirmation rule 11's pattern is copied for; the review is what
  decides whether the copy kept the gate.

### C3. The write: `settings.edit`, the dry run, and the widening hook (D1103, D1104, D1105, D1112, D1114, D1115). **SEC** — **size L**

- **Files:** `console/src/main/ipc.ts` (`settings.edit`, its exact-argv row, **rule 5's count 25 → 26
  in the same commit**, and the never table re-read rather than edited),
  `console/src/main/spawn.ts` (the row; `CHILD_ENV_KEYS` gains P4's marker key **only if P4 landed**),
  `console/src/main/confirm.ts` (**generalized from one view to a union**, D1115, with
  `LaunchConfirmer`'s seam widened to take either view and its `close()` kept as the tab-leave and
  installation-change cancel), `console/src/main/launch.ts` (the launch's own view moves behind the
  union, unchanged in behaviour), `console/src/preload/index.ts` and `console/src/preload/confirm.ts`,
  `console/src/renderer/entry.ts`, `console/test/unit/confirm.test.ts`,
  `console/test/unit/confirm-view.test.ts`, `console/test/unit/ipc.test.ts`,
  `console/test/unit/spawn.test.ts`, `docs/design/desktop-console.md` §5.1 **rule 5, rule 6 and the
  new rule 12, in this commit**.
- **The two spawns per confirmed edit, in this order:** first
  `settings set KEY --layer <L> --dry-run --json --project <name> [--config <path>]` (or the `unset`
  form) — nothing is written, and its document is what the window draws; then, on a yes from the
  window's own top frame,
  `settings set KEY --layer <L> --yes [--console-confirmed] -- <VALUE> --project <name> [--config <path>]`.
- **Why `--` and why the layer is explicit (D1104, D1105):** the value is a **positional** argument,
  so the terminator is the only thing that keeps a value spelled `-draft` or `--layer` from being read
  as a flag; and the layer is passed because **what the window showed is what is spawned** — the
  console never lets the child choose a layer the operator did not see. List edits use the `=` form
  (`--add=V`, `--remove=V`), where the value is part of the flag's own word.
- **The value is never a shell word:** it is one element of the argv array, spawned with `shell:
  false`, exactly as `argvFor` already does for every row. On the WSL route the wrapper's own
  metacharacter check refuses such a value, and the window shows the command to copy instead.
- **The refusal path (D1112):** a non-zero exit shows the CLI's sentence verbatim, bounded by
  `MAX_REPLY_REASON_LENGTH`, beside a **Read again** control. **No silent retry, ever** — the
  compare-and-swap exists because another writer moved the file.
- **Red step:** `spawn.test.ts`'s settings-edit vector fails while the row builds the value
  as a bare positional, and `ipc.test.ts`'s count test fails while the operation is absent.
- **Tests:** the exact vector for a narrowing, a widening, an unset and a list edit;
  `TestSettingsEditIsRefusedWithoutAConfirmedWindow` (the spawn does not happen when the window
  answered no, when it closed, or when the answer came from another frame — the property
  `confirm.test.ts` already pins for the launch, asserted for the second view);
  `TestSettingsEditDoesNotRetryOnARefusal`; `TestSettingsWindowClosesAsACancelOnTabLeave`; the count
  and the never-entry tests; and, under P4, `TestSettingsEditPassesConsoleConfirmedOnlyForAWidening`.
- **SEC because** it moves rule 5 and rule 6 and it is the second write the console makes; rule 12
  and the count move in the same commit.

### C4. The hermetic end to end, and the fixture (D1118). **size M**

- **Files:** `console/test/fixtures/bin/fugaro` (two arms: `settings --json` answering a fixture
  document, and `settings set|unset` answering P3's dry-run document for the first call and a success
  sentence for the second, **appending its argv to the same `reads.log` the stub already writes**
  through its own `log` helper),
  `console/test/e2e/projects.spec.ts` (the pencil, the dialog, the widening's tick, the refusal),
  `console/test/fixtures/settings.json` (new, with its `.license`), `console/test/unit/fixture.test.ts`
  and `console/test/unit/stub.test.ts` (the stub's own pins), `console/test/unit/goldens.test.ts`
  (the golden list, which gains the panels' new content).
- **The property the spec exists for:** the argv the stub **recorded** equals the argv the window
  **showed** — the console's own version of rule 11's "what the window shows is what is spawned",
  asserted from outside the process rather than from a unit test's expectation.
- **No goldens are re-recorded here.** `__screenshots__/projects/{light,dark}-<platform>.png` change
  because the tab gains a section; they are re-recorded by the coordinator on the owner's machine
  (`npx playwright test projects.spec.ts --update-snapshots`) **after** the code is reviewed, and CI
  skips them. The task states that in its own notes so a later reader does not read a green e2e run
  as evidence the goldens are current.
- **Hermetic:** the stub is the CLI; the fixture documents are files; no cloud, no network, no real
  config home.

### C5. The documents the fences live in (D1121). **SEC** — **size S**

- **Files:** `docs/design/desktop-console.md` §5.1 (**rule 5's count and rule 6's two rows move in
  C1's and C3's commits, not here**; what is left for this task is the prose that is not a fence —
  §3.4's project-flag paragraph gaining `settings`, and rule 12's cross-reference into
  `docs/settings-commands/` for the D846 weakening), `docs/design/console-styling.md` §2.5 (the new
  panels' class hooks and the pencil's states, which C2's CSS implements),
  `console/docs/json-contract.md` §6.15 (finished against the shipped screen, per D848's own rule
  that the console's work writes the console's document), and
  `docs/settings-commands/reconciliation.md`'s D846 entry **only if P4 landed**.
- **Why it is a task of its own and still SEC:** three of these documents are pinned by docs tests
  (`go test ./internal/cli -run Docs` and the console's own document tests), so a wording change here
  turns a test red somewhere else, and the task is where that is noticed. It is not a fence commit —
  the fences moved with their code — but it may not be skipped, because a rule 12 that names a
  behaviour the code does not have is worse than no rule 12.

### C6. The release notes, console 0.119.0 (D1120). **size S**

- **Files:** `docs/releases/console/v0.119.0.md` (new) and `website/data/release-summaries.yaml`
  (one line, no backticks).
- **What the notes say, and what they must not:** the feature in the owner's words; the widening
  dialog's tick, described as **the console's own confirmation of a CLI weakening** rather than as a
  security control; and, under **Known limits**, that launching is no longer the console's only
  write, and that an edit does not change a run until `fugaro update` publishes it — the CLI's own
  sentence, unchanged.
- **`### For operators`: none, and this task says so in the notes' own body.** Nothing here is a
  post-deploy step: the console edits files a person could edit with an editor, the CLI prerequisites
  add flags and members rather than changing a meaning, and the policy node's shape is untouched. A
  release whose `### For operators` is empty is a release that asks nothing of the operator, which is
  the outcome the hard rule wants.
- **Verification:** `website/`'s own check (`npm run check`) if the summaries file's shape is pinned
  there, and the console release tag `console-v0.119.0` at release time, not here.

## 6. The critical path, and what it costs

**S5 (#1525) merges → P1 → C1 → C2 → C3 → C4 → C5 → C6.** P2 runs beside P1 and is needed by C1; P3
runs beside them and is needed by C3; **P4 runs beside them and is needed only by C3's widening
path**, which is why its refusal degrades the feature rather than stopping the plan. The longest
chain is five tasks and two of them are L.

**Two things a reader should weigh before approving this.** First, **the plan is larger than the
ask looks**: eleven tasks, seven of them SEC, for a pencil — and the reason is that the console
cannot be told which rows are writable, whether an edit widens, or which installation it is looking
at, and each of those is a CLI change rather than a console one. Second, **the cheapest version of
this feature is not this one**: D1107's option **B** (narrowings only, widenings shown as a command
to copy) drops P4 and the widening half of C3 and C4, and it is the version to approve if the owner
would rather have the pencil in console 0.119.0 without a rule weakened for it. The owner's D1100
answer says widenings are in scope on day one, so this plan is built for that; the fallback is
written down because it is a real option and not a consolation.

## 7. What is deliberately not in this plan

- **The caps and kill-switch panels' values.** `budget set|kill|resume` write the Realtime Database
  through a different command family with its own immediacy rule (**U4**). This plan reads the ask's
  "each of those values" as the `settings` registry's rows, and the owner should confirm that
  reading; if he means the caps too, it is a second design.
- **Roles, and any permission model of the console's own** (**D1110**). "Administrator" stays the
  CLI's own outcome.
- **`fugaro update` from the console** (**D1119**). The console shows the CLI's `next:` line and
  never runs it.
- **A managed-project refusal on `settings set`** (**F9**). S5 has none; adding one is a change to
  S5, and if the owner wants it, it goes in P1's commit rather than into a console rule.
- **Any console code that writes a file.** The console spawns the CLI; the second editor is the
  thing this design exists to avoid (**D1103**).
