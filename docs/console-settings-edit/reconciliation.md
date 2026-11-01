# Editing the Project tab's settings from the console: reconciliation against the code

Every claim below names the file and the symbol it was read from, so a reader checks it with
`git grep` rather than trusting a line number the next edit above them invalidates. **Where this
document disagrees with [spec.md](spec.md), it wins.**

## 1. What was reconciled against

- **`origin/main` at `d1585a6f`** (the merge of #1532), read with `grep -n`, offset reads and
  `git show`. Every `console/…` and `internal/…` citation below is a line of that commit unless it
  says otherwise.
- **S5, `fugaro settings get|set|unset`, pull request #1525**, branch
  `fugaro/20261101-151752-0d66`, head `f7871b3a`, **open in review and not merged**. It is read
  through `git show FETCH_HEAD:<path>`, and every citation into it is marked **S5 #1525**. It is the
  command every edit in this design spawns, so it is the single most load-bearing input here.
- **`docs/settings-commands/`** — its [spec.md](../settings-commands/spec.md) §4.2, §4.3 and §4.4
  (the write path, `unset`, and the allow-list of keys the generic path owns),
  [reconciliation.md](../settings-commands/reconciliation.md) **D846** (which writes ask for the
  typed project name) and [implementation-plan.md](../settings-commands/implementation-plan.md)
  **S5** (the task that lands the command), plus the joint order in
  [its README](../settings-commands/README.md).
- **`docs/design/desktop-console.md`** §3.4 (~122) and §5.1 (~175): rule 5 (the IPC allow-list),
  rule 6 (the exact-argv spawn allow-list and its never table) and rule 11 (the launch: the one
  write, its confirmation window, the frozen spawn object).
- The console's own code: `console/src/main/confirm.ts`, `console/src/main/launch.ts`,
  `console/src/main/ipc.ts`, `console/src/main/spawn.ts`,
  `console/src/renderer/screens/projects.ts`, `console/src/renderer/writes.ts`, and the unit and
  e2e tests named in the findings.

**Not read:** any open pull request other than #1525. Where this design needs a number or a fact that
lives in another open design (#1514's D1063/D1074 are the two this tree cites), it says so rather
than guessing.

## 2. What exists

**F1: Exists. The Project tab, and what it draws today.** `PROJECTS_SOURCE_LINES`
(`console/src/renderer/screens/projects.ts:92`) names three commands — `fugaro budget show --json`,
`fugaro accounts ls --json`, `fugaro secrets ls --json` — and `PROJECT_PANEL_KEYS` (`:139`) draws
seven panels: `caps`, `kill`, `repositories`, `accounts`, `secrets`, `policy`, `enrollment`. The last
two are `not-built` on purpose (`POLICY_SOURCE_LINE`, `:104`; `ENROLLMENT_SOURCE_LINE`, `:108`), and
the screen's own header says it makes **one** read, `budget show`, after the jobs panel and its
`runs ls --json` were removed (owner's report of 2026-10-31). *Consequence:* the values the owner
means by "each of those values" are **not on this tab today**. The pencil's row set is a new read,
and the panels that carry pencils are new panels — this design adds a section, it does not decorate
five existing ones.

**F2: Exists. Rule 11's confirmation window, and the seam it fills.** `createConfirmWindow`
(`console/src/main/confirm.ts:266`) builds one modal `BrowserWindow` over `app://console/confirm.html`
with a preload of its own (`confirmChannel`, `:80`), a readiness delay
(`CONFIRM_READY_DELAY_MS`, `:145`, with `CONFIRM_NOT_YET`, `:151`), a gate that accepts an answer only
from **that window's own `mainFrame`** (the file's own property 2), and cancel-by-default. Its seam
is `LaunchConfirmer` (`console/src/main/launch.ts:354`), its input is `ConfirmView` (`:291`, built by
`confirmView`, `:312`), and what it is built from is `FrozenLaunch` (`:230`) — the whole spawn, built
once and frozen, so that **what the window shows is what is spawned**. *Consequence:* this design
copies that pattern rather than inventing a second one, and **D1116** says how far the copy is
shared code.

**F3: Exists. The two allow-lists are exact, closed and mutation-proof.** `CONSOLE_OPERATIONS`
(`console/src/main/ipc.ts:417`) is twenty-four operations; `ALLOWED_OPERATION_FLAGS` (`:979`) admits
four fixed flags; `NEVER_REACHABLE` (`:1051`) is the never table, with the `argv` shape that lets one
exact `run` vector through while refusing every sibling (`NeverReachableShape`, `:1019`); and
`assertReachableSurface` (`:2538`) refuses at startup a table that drives a never entry. The spawn
side is `argvFor` (`console/src/main/spawn.ts:980`), the only place an argv is built, and
`console/test/unit/ipc.test.ts` fails a test named for a never entry when one reaches the allow-list.
*Consequence:* the two operations this design adds, the argv rows they drive and the count in rule 5
all move in **one commit each** (**D1115**), and the count is not decoration — it is what a reader
checks against the table.

**F4: Exists. The console's child environment is fixed, and no coding-agent marker can reach it.**
`CHILD_ENV_KEYS` (`console/src/main/spawn.ts:360`) is the whole environment a child receives: `PATH`,
`HOME`, `TMPDIR`, `LANG`, `LC_ALL`, five `XDG_*`, plus `WSLENV`/`WSL_UTF8` on the WSL route. The
marker list is `agentenv.Markers` (`internal/agentenv/agentenv.go:28`): `CLAUDECODE`,
`CLAUDE_CODE_ENTRYPOINT`, `CLAUDE_CODE_SSE_PORT`, `CLAUDE_CODE_REMOTE`, `CURSOR_AGENT`, `AI_AGENT`.
*Consequence, and it cuts both ways:* S5's agent-session refusal (`applySettingsEdit`, **S5 #1525**
`internal/cli/settings_write.go:72`, reading `agentMarker`) can never fire for a console spawn —
which is correct, because the console is not a coding agent's session — and **the console cannot use
that refusal as the reason a row has no pencil**. The human confirmation is the window (F2), not the
marker. **F9** and **D1111** are the other half of this.

**F5: Exists. The write path is one function, and its sentences are the console's to show.**
`applySettingsEdit` (**S5 #1525** `internal/cli/settings_write.go:72`) is the one write path: the line
diff, the confirmation, the agent-session refusal, the compare-and-swap re-read
(`readSettingsCurrent`, `:140`), the backup and the atomic write. Its ordinary confirmation is
`confirmSettingsWrite` (`:151`) — `--yes` answers it — and its typed one is `confirmTypedStrict`
(`internal/cli/budget.go:239`), which **no flag answers**. Every successful write ends with
`next: fugaro update` (`runSettingsSet`, **S5 #1525** `internal/cli/settings_set.go:678`).
*Consequence:* the console spawns this and reads its sentences; it writes no document of its own, and
it never re-implements the diff (**D1103**).

**F6: Exists. Sensitive rows are marked by the CLI, in the document the console would read.**
`setting.Sensitive` (`internal/cli/settings_read.go:48`, on `main` since #1484) becomes
`settingsJSON.Sensitive` (`internal/cli/settings_render.go:45`, `json:"sensitive,omitempty"`), and the
write path refuses a sensitive key outright (`settingsWriteRow`, **S5 #1525**
`internal/cli/settings_set.go:239`). The registry marks them (`Row.Sensitive`,
`internal/settings/registry.go:80`; `sensitiveKeys` in the same file). *Consequence:* **D1111** is
satisfiable from the CLI's own output, and the console keeps no list of sensitive keys — which is the
same rule as F7's.

**F7: Exists. The CLI owns the allow-list of writable rows, and the registry owns the owner of
each.** `settingsGenericKeys` (**S5 #1525** `internal/cli/settings_set.go:62`) is the exact key
allow-list, `settingsGenericPrefixes` (`:83`) the blocks handed over whole, `settingsFamilyCommands`
(`:39`) maps a registry `Owner` to the family that writes its keys, and `settingsDefaultLayer`
(`:263`) picks the narrowest layer a key may be set in. `Row` carries `Owner`, `Sensitive` and
`Layers` (`internal/settings/registry.go:80`). *Consequence:* the console's row set is the CLI's
answer, and **D1102** is about how it gets it.

**F8: Exists. The registry's `--json` document, field by field.** `settingsJSON`
(`internal/cli/settings_render.go:38`, on `main` since #1484) carries `key`, `scope`, `value` (a pointer, absent for a
sensitive row), `wins`, `default` (`{value, source, unset}`), `state`, `sensitive` and `levels`
(`settingsLevelJS`, `:58`), under a document with `version`, `project`, `generated_at` and
`sources` (`:19`). **`wins` is the layer that decides the key today** — the column **D1108** needs
for "which layer currently wins". *Consequence:* the read half of the confirmation window's content
already exists; what does not is the writability half (**F10**).

**F9: Exists, and it is the refusal the brief expected to find. There is no managed-project refusal
in S5.** The brief for this design names "agent sessions and managed projects (S5's refusals)". S5 as
it stands in #1525 has the **agent-session** refusal and nothing else: `applySettingsEdit` tests
`agentMarker(os.Getenv)` and no managed state, and `settingsResolveTarget`
(**S5 #1525** `internal/cli/settings_set.go:291`) resolves a file, not a project posture. The commands
that do refuse a managed project are `fugaro managed flag` and `fugaro allow-delete`
(`internal/cli/managed_flag.go`, `internal/cli/allow_delete.go`), and managed projects are gated off
(`docs/design/managed-projects.md`). *Consequence:* the console's refusal set is **smaller than the
brief assumes**, and **D1111** records the set as it is rather than inventing a refusal. If the owner
wants a managed-project refusal on `settings set`, it is a change to S5 and it belongs in **P1**'s
commit, not in a console task.

**F10: Missing. Nothing in `settings --json` says which rows are writable, or by whom.** The
registry's `Owner` and the allow-list of F7 are **CLI-side facts with no reader outside
`internal/cli`**: `settingsJSON` (F8) has no `owner`, no `writable`, no `layers` and no
`target_layer`. `settingsWriteRow` (**S5 #1525** `settings_set.go:239`) prints "X is written by
`fugaro rigor set`, not by the generic path" — a sentence on a **refusal**, reachable only by asking
the CLI to write something it will not write. *Consequence:* **the console cannot draw the pencil
from the CLI's output as S5 stands.** Either it keeps a copy of `settingsGenericKeys` (which the
brief forbids, and which is the fourth-copy defect the settings design's own F12 exists to prevent),
or the read grows the field. **D1102**, and prerequisite **P2**.

**F11: Missing. `settings set|unset` has no machine-readable output, no dry run, and no
classification.** The flags are `--layer`, `--repo`, `--yes`, `--add` and `--remove` and nothing else
(`newSettingsSetCmd`, **S5 #1525** `settings_set.go:161`; `newSettingsUnsetCmd`, `:202`). Widening is
decided inside `settingsTypedWrite` (**S5 #1525** `settings_set.go:633`) — via `settingsCeilingKey`
(`:503`), `settingsLoosens` (`:592`) and `settingsListSubset` (`:615`) — and is **never printed as a
field**; it becomes a prompt. The only outputs are prose on stdout and `userErr`'s exit code.
*Consequence:* the console cannot ask "is this a widening?" and must not re-derive it — the
classification rules are S5's, and a copy in TypeScript is a second answer that will drift the day
D846's set changes. **D1106**, and prerequisite **P3**.

**F12: Conflicts. `fugaro settings` cannot be aimed at an installation.** `newSettingsCmd`
(`internal/cli/settings.go:35`, on `main` since #1484) registers no `--project` and no `--config`; the flags it
has are `--repo`, `--all-repos`, `--layer`, `--grep`, `--changed`, `--why`, `--offline`, `--json`
(`settingsOptions`, `:21`). Both the read and the write build a `localcfg.SelectInput` with
**`Project`, `Config` and `Checkout` all empty** (`readSettings`, **S5 #1525**
`internal/cli/settings_read.go:146`, whose `SelectInput` is at `:148`; `settingsSelection`,
**S5 #1525** `settings_set.go:273`) — so `localcfg.Select`
(`internal/localcfg/select.go:104`) reaches step 6, "exactly one project config", with steps 1–5 all
unavailable. The console's child carries no `FUGARO_PROJECT` and no `FUGARO_CONFIG` (F4) and its
working directory is the installation's checkout (`createBoundSource`, cited in `ipc.ts`'s
`plan.status` comment at `:587`), which `Select` would consult at step 3 **only if `Checkout` were
filled — and it is not**. *Consequence, and it is the largest finding here:* on a machine with the
owner's two installations, **every** `fugaro settings` argv the console could spawn is refused with
"the projects are: …", and on a machine with one it silently reads whichever installation is the only
one. **No console task in this plan can start before P1 lands.** The other allow-listed rows do not
have this problem because `addCloudFlags` (`internal/cli/cloud.go:53`) gives them `--project` and
`--config` and §3.4 rule 3 makes the console pass both.

**F13: Missing. `settings set` prints no document, so a write has no decoded result.** The read has
`--json`; the write has prose and an exit code. *Consequence:* the console's result handling for an
edit is "exit code plus the CLI's own sentence" unless **P3** gives the write a small result document.
**D1106** recommends one, because a refusal the console must show verbatim is exactly the case
`MAX_REPLY_REASON_LENGTH` (`ipc.ts:139`) already bounds.

**F14: Exists. The compare-and-swap refusal, and its sentence.** `applySettingsEdit` re-reads the file
through `readSettingsCurrent` and refuses with "`%s` changed since it was read: nothing was written;
read it again and repeat the edit" (**S5 #1525** `internal/cli/settings_write.go:72`). *Consequence:*
**D1113** has a sentence to show and no retry to write.

**F15: Exists. The progressive-rendering rule, and the section machinery it names.**
`.claude/rules/console.md`'s four-point rule of 2026-10-31 (independent sections, parallel reads,
paint on arrival, a sequence token per section) is implemented in `console/src/renderer/section.ts`
(`sectionRead`, `renderBusy`, `renderStamp`, `resultCache`), and the Project screen's own shell is
`section.projects > div.projects-body > div.console-busy-slot`
(`docs/design/console-styling.md` §2.5). *Consequence:* the settings read is a **section of its own**,
started in parallel with `budget show` and painted when it answers — the tab paints before the
writable-rows read lands, which is the rule **D1118** states.

**F16: Exists. The test surfaces an implementation lands in.** The fake CLI is a shell stub,
`console/test/fixtures/bin/fugaro`, whose header lists the commands it answers (`version`,
`plan status --json`, `doctor --json`, `config list --json`, `watch --json`, …); the unit tests that
hold the two allow-lists are `console/test/unit/ipc.test.ts`, `console/test/unit/spawn.test.ts` and
`console/test/unit/confirm.test.ts`; the Project screen's model and tree are
`console/test/unit/projects.test.ts`; and the goldens are
`console/test/e2e/__screenshots__/projects/{light,dark}-<platform>.png`, driven by
`console/test/e2e/projects.spec.ts` and re-recorded only by the owner
(`npx playwright test projects.spec.ts --update-snapshots`; CI sets `FUGARO_CONSOLE_GOLDENS: skip`).
*Consequence:* **D1119** has named files to point at, and every console task in the plan is
hermetic — the stub answers `settings`, and no test reaches a cloud.

## 3. The shape of the gap, in one paragraph

Every fact the pencil needs — **which rows are writable**, **who owns the rest**, **which layer a
write would land in**, **whether the edit widens** — is decided inside `internal/cli` and, with one
exception, is not printed. The exception is `sensitive` (F6), which is already in the read's
document. The console therefore cannot be given a pencil that is honest about itself without four CLI
changes: it must be able to **name its installation** (F12/P1), it must be told **which rows are
writable and by whom** (F10/P2), it must be told **whether an edit widens** and **which layer it
writes** (F11/P3), and — if the owner takes **D1107**'s recommendation — it must have a way to
confirm a widening at all, because the CLI's own widening confirmation needs a terminal the console
does not have (F5/P4). None of the four is a console change, and three of them are small.

## 4. Owner decisions

Recommendation first in each, with the reason and the cost of the alternative. **D1100–D1139 are this
work's block. D1100 and D1101 are ANSWERED by the owner on 2026-11-01, in the ask itself; D1102–D1121
are written and every one is `OPEN`; D1122–D1139 are reserved and unused.**

An answer here is not an approval: the plan's tasks start only when the owner approves them.

- **D1100 (ANSWERED by the owner, 2026-11-01). Is the feature in scope, and does day one include
  widenings?** *The owner's answer, from [spec.md](spec.md):* **yes to the feature, and all edits,
  including widenings, from day one — not narrowing-first.** The alternatives (no; or narrowings
  first, widenings later) are not on the table. *What this settles:* the console must be able to
  perform a widening on day one, which is what makes **D1107** load-bearing rather than a
  nice-to-have: S5's widening path needs a terminal, and the console has none (F5, F12).

- **D1101 (ANSWERED by the owner, 2026-11-01). What confirmation does an edit take?** *The owner's
  answer, from [spec.md](spec.md):* **a simple confirmation dialog for every edit; the typed project
  name is "too much".** *What this settles:* the console never draws the typed project name, and
  **D1109** designs the dialog he asked for — old value, new value, the layer written, the layer that
  wins, and for a widening one extra deliberate tick. The rejected alternative is option **C** of
  **D1107**, which is this design's own first sketch and the form the owner ruled out.

- **D1102 (OPEN, recommended: the CLI's read carries the writability facts). Which rows get a
  pencil, and where does the console learn it?** *Options:* **(a)** `settings --json`'s row gains
  three fields — `writable` (a boolean: the generic path owns this key), `owner` (the command family
  that writes it, or the empty string), and `target_layer` (the layer `settings set` would write with
  no `--layer`, i.e. `settingsDefaultLayer`'s answer); **(b)** the console keeps its own copy of
  `settingsGenericKeys` and `settingsGenericPrefixes`; **(c)** the console draws a pencil on every
  row and lets the CLI refuse the ones it will not write. *Recommendation:* **(a)**, and the reason is
  the settings design's own F12 argument turned around: a list kept in TypeScript is a fourth copy of
  a fact that already has one home, and it drifts the day a key is added to the registry — silently,
  in the direction of offering an edit that will be refused, or hiding one that would work. **(c)** is
  worse than **(b)**: it puts a button on every row and makes a refusal the ordinary path, which
  teaches the operator that the console's refusals are noise. *Cost of (a):* a wire-format addition to
  a document the console reads, which is an addition of members and therefore no version bump
  (`schemas/settings.schema.json`'s `version` stays 1); it is **P2**. *Where it is written:* the
  console's own contract section, `console/docs/json-contract.md` §6.15, which the settings design's
  D848 already reserved for the Settings view.

- **D1103 (OPEN, recommended: one spawn of `settings set|unset` per confirmed window). Which command
  does a pencil spawn?** *Options:* **(a)** `fugaro settings set KEY VALUE …` or
  `fugaro settings unset KEY …`, spawned once per confirmed window, through the allow-list; **(b)** the
  console writes the file itself; **(c)** the console batches several edits into one child. *Recommendation:*
  **(a)**, because the brief's rule 1 is that the console has no editing code of its own and **(b)** is
  the whole of what that forbids: a second editor would have to reproduce `internal/yamledit`'s
  comment-preserving splice, the strict re-parse, the backup and the atomic write, and it would be the
  copy that drifts. **(c)** is refused because the CLI's confirmation, its compare-and-swap and its
  `next: fugaro update` line are per edit, and a batch would show the operator one dialog for a set of
  writes whose individual diffs they have not read. *Cost of (a):* one child per edit, which is a
  second of latency on a machine where the console already spawns children for every read.

- **D1104 (OPEN, recommended: the value is its own argv element, and the value word is separated by
  `--`). How does a value reach the CLI, and how is flag injection prevented?** *Options:* **(a)**
  `settings set KEY --layer L --yes -- <VALUE>`, with the value as a **separate argv element** after
  the `--` terminator, and list edits in the `=` form (`--add=V`, `--remove=V`); **(b)** the value
  after the key with no terminator, relying on the value never beginning with `-`; **(c)** the value
  on the child's stdin. *Recommendation:* **(a)**. The reason is the one rule 11 already gives for
  `--title=` and `--plan-task=`: **a value that follows `=` is part of the flag's own word, and
  nothing that follows can be read as a flag** — but `VALUE` is a **positional** argument here
  (`Use: "set KEY VALUE"`, **S5 #1525** `settings_set.go:163`), so there is no `=` to hide behind and
  the terminator is the equivalent. `settings set commit_name --layer local --yes -- -draft` sets the
  name `-draft`; without the `--`, pflag reads `-draft` as an unknown shorthand and the edit fails —
  or worse, a value spelled `--layer` silently retargets the write. **(b)** is the form this row must
  never build. **(c)** is refused because S5 reads stdin for its confirmation, not for its value
  (`confirmSettingsWrite`, **S5 #1525** `settings_write.go:151`), so a value on stdin would have to
  arrive before the confirmation that the console has not shown yet. *Every word is a fixed literal
  of the console's own, and the value is passed as an array element with `shell: false`* — the
  property `argvFor` (`console/src/main/spawn.ts:980`) already guarantees and
  `console/test/unit/spawn.test.ts` already pins. *On the WSL route the wrapper's own
  `distroShellUnsafe` check refuses a word with shell metacharacters, so a value containing one is
  refused there and the edit is offered as a command to copy instead (D1107's option **B** behaviour,
  applied per word).*

- **D1105 (OPEN, recommended: the console passes `--layer` explicitly, and only after the CLI named
  it). Which layer does a pencil write, and who chooses?** *Options:* **(a)** the console passes no
  `--layer` and lets S5 pick the narrowest layer the key may be set in (`settingsDefaultLayer`,
  **S5 #1525** `settings_set.go:263`); **(b)** the console passes `--layer` explicitly, from the
  `target_layer` field **D1102(a)** adds, and shows it in the window; **(c)** the console offers the
  operator a layer chooser. *Recommendation:* **(b)**, because rule 11's whole property is that **what
  the window shows is what is spawned** — a window that says "written to the local config" while the
  child is free to choose a different layer is showing the operator something it does not know.
  **(a)** is the same argv with less honesty; **(c)** is a product decision this design does not take,
  and it would put a layer the operator cannot reason about behind a control. *Cost of (b):* one more
  field in the read (**P2**), and the console must refuse to open a window for a row whose
  `target_layer` is empty.

- **D1106 (OPEN, recommended: a dry-run classification, `settings set|unset … --dry-run --json`). How
  does the console know whether an edit widens?** *Options:* **(a)** a `--dry-run --json` on the two
  write commands, printing a closed document — `{version, key, layer, path, changed, widening, why,
  old, new, refusal}` — writing nothing and touching no backup; **(b)** the console re-derives
  widening from the registry's key list and the old/new values; **(c)** the console performs the write
  and reads the CLI's prompt behaviour from the exit code. *Recommendation:* **(a)**. **(b)** is the
  option the brief forbids and the one that is wrong on the merits: widening is
  `settingsCeilingKey` (`:503`) **and** `settingsLoosens` (`:592`) **and** `settingsListSubset`
  (`:615`) **and** `settingsTypedWrite` (`:633`), including the deliberate "a form this Fugaro does
  not know how to compare is treated as a loosening" rule — a TypeScript re-implementation would
  answer *narrowing* where the CLI answers *widening*, which is the one direction that must never be
  got wrong. **(c)** is refused because a widening's failure mode is a refusal **after** the operator
  confirmed a dialog that could not have confirmed it. *The dry run is also what gives the window its
  "which layer is written" and "old value" fields from one place, so the window and the child cannot
  disagree.* *Exit codes:* `0` with a document for a classification; non-zero with the CLI's own
  sentence for a refusal (a key the generic path does not write, a layer the key may not be set in, a
  value the loader refuses, a sensitive key) — and the console shows that sentence and **offers no
  pencil** for the row from then on, for this session. *Cost of (a):* a new flag and a new document
  on two commands, and the schema entry that pins it; it is **P3**.

- **D1107 (OPEN, recommended: option A, and it is a deliberate weakening of D846 on one path). S5's
  widening path needs the project's name typed at a terminal, and the console has no terminal. What
  happens to a widening?** *The problem, stated plainly:* `settingsTypedWrite` (**S5 #1525**
  `settings_set.go:633`) marks a loosened ceiling, a removed ceiling and a `followup.*` key turned on
  as the typed form; `applySettingsEdit` then calls `confirmTypedStrict`
  (`internal/cli/budget.go:239`), which requires a terminal (`stdinIsTerminal`) and compares the typed
  line against the project's name, and which **no flag answers** — `--yes` is not consulted on that
  path at all. The console's child gets `stdio` with stdin `'ignore'` on every read and `'pipe'` on
  exactly one row (`run.launch`, rule 11), and it has no terminal to offer. So **the console cannot
  perform a widening through S5 as designed**, and the owner's D1100 answer says widenings are in
  scope on day one.
  - **Option A (recommended). A narrow CLI hook meaning "the operator confirmed this widening in the
    console's own dialog".** Concretely: one new flag on `settings set|unset`, `--console-confirmed`,
    which stands in for the typed name **only when the child's environment carries the console's own
    marker variable**, and which is **refused whenever `agentenv.Marker` reports a coding agent's
    session** — so an agent's shell cannot use it, exactly as it cannot use `--layer repo` for a
    typed write today. Terminal use is unchanged: with no marker, `--console-confirmed` is refused and
    the typed name is still what confirms. **How it is authenticated, and what it is honestly worth.**
    It is bound to **one environment variable the console writes into the child**, which means adding
    a key to `CHILD_ENV_KEYS` (`console/src/main/spawn.ts:360`) — itself a change to §5.1 rule 8's
    "nothing added", so it is SECURITY-SENSITIVE and it is **P4**. **It is not a security boundary,
    and this design says so rather than implying otherwise:** a local user who can run `fugaro`
    directly can also set that variable in their own shell, so the hook buys them nothing they could
    not have by running the command with the variable set — and nothing at all, in fact, because such
    a user can already edit the same files with an editor. What it is worth is what it was built for:
    **it keeps the console's widening from being one stray click**, because the flag is only ever
    passed by the one argv the console's allow-list builds, behind a window whose yes comes from its
    own top frame, and it keeps the *agent-session* fence intact, which is the fence that matters —
    the threat D846 answers is a coding agent widening its own ceiling, not a person with a terminal.
    *What it does not do:* it does not make the console's confirmation a security control; it makes it
    a **UX gate over a deliberate, recorded weakening of D846 on one path**. *Cost of A:* D846 stops
    being true of `settings set` on one path, and that sentence has to be written into
    `docs/settings-commands/reconciliation.md`'s D846 entry and into `desktop-console.md`'s new rule
    12 by the task that lands P4.
  - **Option B. The console edits narrowings and non-widening rows only; a widening shows the exact
    CLI command to copy into a terminal.** *Cost:* the owner's D1100 answer is "all edits, including
    widenings, from day one", so B does not meet the ask; it is recorded because it is the only option
    that needs **no** CLI change and no weakening, and because it is the right **fallback** if P4 is
    refused. Under B the widening row still gets a pencil, and the window it opens says what to run.
  - **Option C. The console draws the typed project name itself and passes it to the CLI's
    typed-confirmation input.** *Cost:* it is the design the owner already ruled out — D1101, "too
    much" — and it is worse than A on its own terms, because it puts the project's name in an argv
    word or on the child's stdin, which is a value the console would then have to protect, while A
    puts one boolean-shaped word there.
  - **Recommendation: A, with B as the recorded fallback if the owner refuses P4.** *Whichever he
    chooses, the console's widening dialog is the same* (**D1109**): the extra tick is the console's
    own, and it is not the typed name.

- **D1108 (OPEN, recommended: the six fields below, and nothing else). What does the confirmation
  window show?** *Recommendation:* **the key**; **the old value** (from the dry run's `old`, and the
  read's `value` when the key is unset); **the new value**; **the layer written** (from
  `target_layer`, D1105); **the layer that currently wins** (from the read's `wins`, F8); and **one
  sentence, present only when the edit will not change what runs** — which is **D1114**. Plus, for a
  widening, the CLI's own `why` sentence from the dry run (D1106) and the tick (D1109); and for every
  edit, the exact argv, field by field, as rule 11's window does. *Why these six:* they are the
  smallest set that answers "what am I changing, where does it land, and will it matter" — the three
  questions a person actually has — and every one of them is a value the CLI produced rather than one
  the console computed. *What it must not show:* a diff of the file (the CLI prints one and the
  console does not re-render it), any other key, and any value of a sensitive row (D1111).

- **D1109 (OPEN, recommended: cancel is the default; a widening needs one extra tick). How does the
  dialog behave for a widening and for a narrowing?** *Recommendation:* one window, two shapes.
  **A narrowing or a non-widening edit is one click**: Confirm is the default button and a single
  deliberate click confirms. **A widening is two**: **Cancel is the default button** — so Escape,
  Enter and the window's own close all cancel — and Confirm is **disabled until a checkbox labelled
  "I understand this raises a limit" is ticked**, in a control with an accessible name that says what
  is being raised. *Why a tick rather than a second dialog or a hold-to-confirm:* it is exactly what
  the owner asked for (D1101) and it defeats the failure it exists for — a stray double-click cannot
  both focus the window and tick a box and press Confirm, and unlike a delay it does not depend on a
  timing constant that a fast machine defeats (the case rule 11's own `CONFIRM_READY_DELAY_MS`
  paragraph records rather than claims away). *The tick is not a substitute for
  `CONFIRM_READY_DELAY_MS`:* both apply, the delay as it does for the launch window, and the tick
  resets whenever the window is re-shown or re-focused. *The console does not decide which shape a
  window takes:* it is the dry run's `widening` field (D1106), so a rule change in S5 changes the
  dialog with no console edit. *Under D1107's option B,* a widening window draws the command to copy
  and its Confirm button is **absent**, not disabled.

- **D1110 (OPEN, recommended: no roles are invented; "administrator" is the CLI's own permission
  outcome). Who may edit?** *The problem:* the ask says "administrators", the console has no roles,
  and the roles UX is not approved. *Options:* **(a)** define "can edit" as "whoever can write the
  local config and the cloud project today", which is the CLI's own outcome — the operator on this
  machine, editing their own files, with the cloud half decided by IAM at publish; **(b)** invent a
  local role concept in the console; **(c)** hide every pencil until a roles design lands.
  *Recommendation:* **(a)**. It is the settings design's own SD18 answer ("anyone may edit their own
  file; the boundary stays IAM at publish") applied to a second surface, and it invents nothing: a
  permission refusal is the CLI's own sentence, shown verbatim and bounded
  (`MAX_REPLY_REASON_LENGTH`, `ipc.ts:150`), with the section marked `unavailable` — never a
  console-authored "you are not an administrator", which would be a role claim the console cannot
  make. **(b)** is refused because a role the console invents is a role the CLI does not enforce, and
  **(c)** refuses the owner's ask. *Cost of (a):* the console's "administrator" is a word in the
  owner's ask and not a word in any code, and the design says so rather than papering over it.

- **D1111 (OPEN, recommended: the pencil set is the CLI's `writable` answer, minus sensitive rows).
  Which rows the console never offers, and how it knows.** *Recommendation:* **four** exclusions, and
  every one of them is read, never decided:
  1. **A row the generic path does not own** — `writable: false` in the read (D1102) — is drawn with
     **no pencil**, and the row says which command owns it, from `owner`: S5's own family names
     (`settingsFamilyCommands`, **S5 #1525** `settings_set.go:39`, whose values are `fugaro models`,
     `fugaro rigor set`, `fugaro rigor floor`, `fugaro accounts`, `fugaro secrets`, `fugaro project`).
     A row with `writable: false` and an empty `owner` says the generic path does not write it and
     names nothing, which is the honest answer for an installation-plumbing key.
  2. **A sensitive row** — `sensitive: true` (F6) — is **never offered, never drawn in a
     confirmation and never in a diff**. The console keeps no list: the field is the CLI's.
  3. **A row whose `target_layer` is empty** — a key S5 would not write with no `--layer` — is
     refused a pencil for the same reason.
  4. **A managed project, and a coding agent's session**, are the two refusals the brief names, and
     the design records what is actually true of each (F4, F9): the agent-session refusal **cannot
     fire for a console spawn** — the child's environment is `CHILD_ENV_KEYS` and carries no marker —
     and **S5 has no managed-project refusal at all**, so there is nothing for the console to read
     and nothing for it to hide. If the owner wants one, it is a change to S5 (**P1**'s commit), and
     the console's rule then becomes a fifth read field rather than a fifth console rule.
  *Cost:* on the day this ships, a managed installation sees pencils it would not see under a stricter
  reading of the brief. That is stated here rather than discovered later.

- **D1112 (OPEN, recommended: the CLI's sentence, and a "read again" control). What does a refused or
  stale write do?** *Recommendation:* the window closes, and the section shows **the CLI's own
  sentence** — for a stale file, "`<path>` changed since it was read: nothing was written; read it
  again and repeat the edit" (F14) — beside a **Read again** control that re-runs the read and, when
  the operator asks for it, re-opens the pencil against the fresh row. **The console never retries a
  write on its own**: the compare-and-swap exists because another writer moved the file, and a silent
  retry would replace a human's decision with the console's. *A refusal is never drawn as a success:*
  the reply's `ok` is what the screen branches on, and a non-zero exit with no document is a refusal
  even when its sentence is empty (in which case the section shows the exit code and the command).

- **D1113 (OPEN, recommended: the dry run's own comparison, shown as a sentence). When does the
  console tell the operator that the edit will not change what runs?** *The case:* a local edit under
  a project layer that wins for that key, or a project-layer edit under a repository `fugaro.yaml`
  that wins — the edit is written, and no run behaves differently. *Options:* **(a)** the console
  compares the layer it is writing against the read's `wins` and shows one sentence when they differ;
  **(b)** the console says nothing and lets the operator work it out; **(c)** the CLI's dry run
  carries the sentence. *Recommendation:* **(a)**, with the sentence drawn in the window **before**
  the confirm — the operator is entitled to know that the write is a record of intent rather than a
  change of behaviour, and the comparison is between two fields the CLI printed (`target_layer` and
  `wins`), so it is a comparison and not a rule. **(c)** would be better still if P3's document
  carries it, and the plan says so: **if `settings set --dry-run --json` grows a `shadowed` field, the
  console uses it and drops its own comparison.** The sentence is not a refusal: the edit proceeds.
  *Cost of (a):* one comparison in TypeScript whose rule is "two layer names are equal or they are
  not", which is small enough not to be a second copy of anything.

- **D1114 (OPEN, recommended: two new operations, `settings.read` and `settings.edit`, and the rule 5
  count moves with them). The IPC and preload surface.** *Options:* **(a)** two operations — one read
  that spawns `settings --json`, one write that spawns the dry run and then the write, both behind the
  console's own confirmation window; **(b)** one operation that does both; **(c)** reuse an existing
  operation. *Recommendation:* **(a)**. **(c)** is impossible: no existing operation's argv is
  `settings`-shaped, and the surface is a table of exact argvs, so a read that reused
  `budget.show` would spawn the wrong command. **(b)** is refused because the read is a section that
  runs on tab open and the write is a confirmed window — two different lifetimes, and an operation
  that both reads and writes cannot be reasoned about from its schema. *What moves, in one commit per
  operation:* `CONSOLE_OPERATIONS` (`ipc.ts:417`) gains the entry; the rule 5 count goes **24 → 26**
  (the design paragraph's own number, which the plan's C1 and C3 each move once); `NEVER_REACHABLE`
  (`:1051`) is unchanged for the read and **loses nothing** for the write — no row moves, because
  `settings` is not on the never table today and the two rows are new entries on rule 6's allow-list
  table, not promotions out of a refusal; `ALLOWED_OPERATION_FLAGS` (`:979`) gains nothing (the
  fixed words `--json`, `--yes`, `--dry-run` and `--layer` are the rows' own `words`, and `--layer`
  is the one to argue about below); the preload's written-out list gains the two methods
  (`console/src/preload/index.ts`, whose duplication is closed by
  `console/test/unit/ipc.test.ts`); and `argvFor`'s table (`spawn.ts:980`) gains the two rows with
  `takesProject: true`. *The one flag that needs care:* **`--layer`.** It is a fixed word of the
  write's row only in the sense that its **value** varies with the row — so it is either (i) an
  argument of the operation whose value the main process computes from the row it already validated,
  or (ii) a closed enum argument the renderer may send, checked against `local`/`project`/`repo`.
  *Recommendation:* **(i)**, because the layer comes from the CLI's own `target_layer` (D1105) and a
  renderer that can name a layer can name a layer the CLI did not offer. If the owner prefers (ii) it
  is a closed three-value enum and the row is no less safe, and the plan's C3 says which it built.
  **`--yes` is a fixed word of the row**, and the sentence that makes it safe is rule 11's: the
  console's own window is the confirmation, and the spawn happens only on a yes from that window's top
  frame. *Cost of (a):* two entries in the largest reviewable artifact in the console, and an
  independent review, which is what the console rule requires.

- **D1115 (OPEN, recommended: one confirmer, two views — not a second window module). How is the
  confirmation window built?** *Options:* **(a)** generalize `confirm.ts` so that the window is one
  implementation and the view is a union — `ConfirmView` (the launch, `launch.ts:291`) and a new
  `SettingsConfirmView` — with `LaunchConfirmer`'s seam generalized to a confirmer that takes either
  view; **(b)** a second module `settingsconfirm.ts` copying `confirm.ts`; **(c)** reuse the launch
  window unchanged, passing a settings-shaped payload through `ConfirmView`. *Recommendation:* **(a)**.
  **(c)** is refused outright: `ConfirmView`'s fields are the launch's, and a settings payload
  squeezed into them would be a window whose labels lie. **(b)** is the copy that drifts — the
  readiness delay, the own-top-frame gate, the cancel default and the parent/modal wiring are four
  security-relevant behaviours, and a second file is four chances to get one of them subtly
  different, with `confirm.test.ts` covering only the first. *Cost of (a):* an edit to a file the
  launch's own tasks own, in the same commit as the settings window's, which is why the plan's shared
  files table names it. *What must not change:* the gate. The window answers only from **its own
  top frame, by identity and not by URL** (`confirm.ts`'s property 2), and the settings window
  inherits it because it is the same window.

- **D1116 (OPEN, recommended: a `button` with an accessible name naming the key). The pencil
  itself.** *Recommendation:* the pencil is a real `<button>` — not a span with a click handler —
  with `aria-label` naming the key it edits (for example "Edit `budget.per_run_iso`" — the key is
  drawn through the sanitizer's own class, not interpolated), reachable in the tab order, and
  **focus returns to it when the window closes**, whichever way it closed. Its state is **never by
  colour alone**: a disabled or refused pencil carries the reason as text (and `aria-disabled`, the
  shape `disabledWrite` already uses at `console/src/renderer/writes.ts:36` — though this pencil is
  not one of those: it is a live control once it is offered). *Why:* the console's accessibility
  rules are drawn from the same DOM-hook vocabulary `docs/design/console-styling.md` §2.5 pins, and a
  keyboard reader must be able to reach every edit and hear which key it is for.

- **D1117 (OPEN, recommended: a section of its own, in parallel, with a per-section sequence token).
  Rendering, and no stale tabs.** *Recommendation:* the settings read is a **section** in the Project
  tab, started **in parallel** with `budget show --json` and painted when it answers, so the tab
  paints before the writable-rows read lands (F15, `.claude/rules/console.md`). A tab switch
  mid-write **never draws on the new tab**: the window is the main process's and is closed as a cancel
  when the tab is left or the installation changes, which is rule 11's own "an installation removed or
  re-chosen while the window is open closes the window as a cancel" applied to the second write —
  `LaunchConfirmer.close(runId)` (`launch.ts:354`) is the seam, and the settings confirmer needs the
  same one. The section's own cache is `section.ts`'s (`resultCache`), stamped `as of HH:MM`, and a
  view older than `CACHE_FRESH_MS` is drawn **and** re-read.

- **D1118 (OPEN, recommended: the fake CLI grows two arms, and the goldens are re-recorded by the
  owner). Testing.** *Recommendation:* the hermetic surface is — **(a)** a unit test for the argv
  builder, asserting the **exact vector** for a narrowing, a widening, an unset and a list edit, with
  the `--` terminator (D1104) and the fixed words in the fixed order; **(b)** a unit test for the
  window's content, built from a fixture dry-run document, asserting the six fields, the absence of a
  sensitive row, and the widening shape (Cancel default, Confirm disabled until the tick); **(c)** an
  e2e spec over the **existing** fake CLI (`console/test/fixtures/bin/fugaro`, F16), whose stub gains
  a `settings --json --project P` arm and a `settings set|unset` arm that records its argv to a file
  the spec reads — so the test proves the console spawned the vector it showed; **(d)** the two
  allow-list tests (`console/test/unit/ipc.test.ts`, `spawn.test.ts`) move with the operations and the
  count. *The goldens:* `console/test/e2e/__screenshots__/projects/{light,dark}-<platform>.png`
  change, because the tab gains a section. **They are re-recorded by the coordinator on the owner's
  machine, never by an agent run** (`npx playwright test projects.spec.ts --update-snapshots`), and
  CI skips them (`FUGARO_CONSOLE_GOLDENS: skip`). *No test reaches a cloud, a real config home or the
  network:* the stub is the CLI, and the fixture documents are files in `console/test/fixtures/`.

- **D1119 (OPEN, recommended: the console shows the line and never runs the command). `next: fugaro
  update`.** *Recommendation:* every successful edit's section shows the CLI's own `next: fugaro
  update` line (F5) as **text**, with the reason: `fugaro update` applies Terraform and republishes
  the policy node and the layer, and it is on `NEVER_REACHABLE` as a writer (`update`, `ipc.ts`'s
  never table). The console does not run it, does not offer to run it, and does not link it. *Cost:*
  an operator who edits a setting and does not run `update` has written a file that changes nothing
  yet, and the console says so in the CLI's own words rather than hiding it — which is the settings
  design's own G1/G2 gap, unchanged by this work.

- **D1120 (OPEN, recommended: `console 0.119.0`, and no `### For operators` entry). The release
  slot.** *Recommendation:* the console tasks land in **console 0.119.0**; the CLI prerequisites land
  in whichever CLI release follows **S5** (#1525), and they cannot be earlier than it. **No
  `### For operators` entry** is needed: the console edits files a person could edit with an editor,
  the CLI prerequisites add flags and fields rather than changing a meaning, and nothing here is a
  post-deploy step. The one thing the notes must say is in **Known limits**: launching is no longer
  the console's only write, and the widening dialog's tick is the console's own confirmation of a
  deliberate weakening of the CLI's typed-name rule (D1107).

- **D1121 (OPEN, recommended: the four documents the implementing tasks change, named now and edited
  then).** *Recommendation:* the tasks that move a fence change the documents that state it, in the
  same commit: **`docs/design/desktop-console.md` §5.1 rule 5** (the operation count, 24 → 26),
  **rule 6** (the two new allow-list rows, with the exact argvs), and a **new rule 12** for the second
  write — the settings confirmation window, its own-top-frame gate, its cancel default, its widening
  tick, and the honest sentence that D1107's hook is a UX gate and not a boundary;
  **`console/docs/json-contract.md` §6.15** (the settings document, including the fields **D1102**
  adds — the section D848 reserved); **`docs/design/console-styling.md` §2.5** (the new panels, the
  pencil's hooks and the window's own document); and **`docs/settings-commands/reconciliation.md`'s
  D846 entry** if P4 lands, because D846 then has one path it is not true of. `docs/` tests pin
  several of these sentences, so each task runs `go test ./internal/cli -run Docs` and, for the
  console documents, the console's own doc tests. *None of them is edited by this design.*

## 5. UNVERIFIED

- **U1. Whether `--` before the value works with S5's parser as written.** `settings set` is
  `cobra.ArbitraryArgs` with `RunE` reading `args[0]`/`args[1]` (**S5 #1525** `settings_set.go:178`),
  and pflag terminates flag parsing at `--`, so `set KEY --layer local --yes -- -draft` should give
  `args = [KEY, -draft]`. **Not measured here** — this design adds no test and runs no command — and
  **P3's task measures it and, if it does not hold, changes S5's parser to `Flags().SetInterspersed(false)`
  or accepts the value only after `--`**, which is the shape that makes the property true by
  construction rather than by pflag's default.
- **U2. Whether a second `BrowserWindow` for a settings confirmation can be opened while a launch
  confirmation is open.** Rule 11 rate-limits launch windows (`CONFIRM_RATE_WINDOW_MS`,
  `CONFIRM_RATE_PER_INSTALLATION`, `CONFIRM_RATE_APP`, `launch.ts:60`). This design assumes the
  settings window is a **separate confirmer with its own in-flight bound of one per installation** and
  that the two do not share a rate bucket; **no test here shows it**, and C3's task asserts the
  behaviour rather than assuming it.
- **U3. Whether `settings --json` at the size of a full registry read is a comfortable section.**
  The read is local files plus two cloud reads (the layer cache and the policy node) and the console
  would spawn it with `--offline` or not — **this design recommends the console pass no `--offline`**
  and reads `state: unread: …` for the layers that could not be read, but the cost of the cloud half
  on the owner's installation is **not measured here**. C1's task measures it, the way the Project
  tab's `runs ls --json` removal was measured.
- **U4. Whether the owner wants the pencil on the `caps` and `kill` panels as well.** Those panels
  are drawn from `budget show --json` and their values are **Realtime Database** writes
  (`fugaro budget set|kill|resume`, `internal/cli/budget.go`), which are a different command family
  with a different write path and their own immediacy rule (the settings design's F8). This design
  assumes **not** — the pencil covers the `settings` registry's rows — and the ask's "each of those
  values" is read as "the settings of this project". **The owner should confirm this reading**; if he
  means the caps too, it is a second design and not a task here.
- **U5. The exact CLI release the prerequisites land in.** They cannot precede #1525's merge and
  this design does not read that pull request's own release slot (the settings design queues for CLI
  0.15.0 and console 0.117.0, which the console has already passed). Named in the plan as a
  dependency rather than a number.
