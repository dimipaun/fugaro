# The new operations' contract (C-OPS)

**Status: written with the plan's task C-OPS; its fixtures were reconciled with the CLI goldens and the stub bridge
C-OPS held them in was deleted, by I-OPS. The names, the payloads and the answer shapes below are frozen; everything
else in the plan is still unapproved.**

This document is the contract [implementation-plan.md](implementation-plan.md) asks C-OPS for, and it is what the
tasks that follow read instead of each other's code. The types live in
[`console/src/shared/new-ops.ts`](../../console/src/shared/new-ops.ts), the fixtures the consumers build against are
in [`console/test/support/newOpAnswers.ts`](../../console/test/support/newOpAnswers.ts), the renderer's typed read is
[`console/src/renderer/new-bridge.ts`](../../console/src/renderer/new-bridge.ts), and
[`console/test/unit/new-ops.test.ts`](../../console/test/unit/new-ops.test.ts) holds the three to each other.

Three tasks are written against it: **AL** adds the operations beside it (it imports nothing from the types: what it
must agree with is the CLI's argv and the frame shape, not a module), **SRC** writes the handlers, and the consumers --
SU1, RB4, RS2, RS5, MO-C, SW3a, SW3b -- import the types and, in a test that draws from an answer, the fixtures.
**I-OPS** held those fixtures to the CLI goldens and deleted the stub bridge that had answered from them, so every
consumer reaches the real handlers.

## The nine operations

Every name is the operation's name in `CONSOLE_OPERATIONS` (`console/src/main/ipc.ts`) and the operation's name in
this contract. The **method** is the bridge member the renderer calls: `ipc.ts`'s `method` field, the preload's
bridge property and `newOpsFrom`'s own method, one spelling for one operation. The argv is AL's; it is written here
because the payload is only meaningful beside the command line it becomes.

| Operation | Method | argv (AL; no flag is the renderer's to choose) | Payload keys |
| --- | --- | --- | --- |
| `runs.stream` | `streamRuns` | `runs ls --stream --json --since <w>` | `since` (required) |
| `spend.stream` | `streamSpend` | `report --stream --json` + `by`, `since`, `until`, `repo` | `by`, `since`, `until`, `repo` |
| `budget.stream` | `streamBudget` | `budget show --stream --json` + `repo`, `all`, `compute` | `repo`, `all`, `compute` |
| `whoami` | `whoAmI` | `user ls --me --json` | none |
| `image.catalog.ls` | `listImageCatalog` | `image ls --json` | none |
| `recipes.ls` | `listRecipes` | `recipes ls --json` | none |
| `setup.plan` | `planSetup` | `setup --plan-only --answers-file - --events --json` | `answers` (required) |
| `setup.apply` | `applySetup` | `setup --answers-file - --events --json` | none (`{}`) |
| `settings.editor.open` | `openSettingsEditor` | main's own; the CLI is not spawned by this operation | `key` (required) |

The payload key sets are closed, and they are the whole of what the renderer may send: every other key is refused by
name in main before a child is spawned, and `NEW_OPERATION_PAYLOAD_KEYS` is the table a test reads.

**The three the plan names, and why each is stated as it is.**

- `settings.editor.open` takes `{key}` and nothing else. The key is a key of the document `settings read --json`
  answered, validated in main with `SETTINGS_KEY_RE`; **the layer and the phase are main's**, so a renderer cannot
  name where a write lands or when it is published. This is `settings.edit`'s rule, and it is why the editor is an
  operation of its own rather than a flag on the write.
- `setup.apply` takes `{}`. It reuses the answers and the digest **main** held when it showed the plan window, so a
  renderer cannot slip a different project, region or rigor in at apply time. A second answers object on the wire
  would be a second thing to validate and a second thing to disagree with the window.
- `setup.plan` carries an **answers object and never a path**. Main serialises the five answers into bytes it holds
  in memory and hands those bytes to the child on stdin (`--answers-file -`, the kind `run --task-file -` uses), so
  there is no file for another process of the same user to swap between main's check and the CLI's read. The file the
  child decodes carries `version` beside the five (`SETUP_ANSWERS_FILE_KEYS`); `version` is the schema's, not an
  answer, and it is main's to write.

## The setup answers

The five questions `fugaro setup` asks (`setupQuestions`, `internal/cli/setup.go`), in the order it asks them:
`gcp_project`, `region`, `name`, `rigor`, `bake_repos`. `rigor` is required and has no default; its five words are
`SETUP_RIGOR_MODES`, held equal to the console's own vocabulary for the same five (`RIGOR_MODE_WORDS`,
`console/src/shared/rigor.ts`) by a test — a mode the wizard offered and the screens could not draw would be an answer
nobody could read back. `bake_repos` is a boolean; the other three are text.

## What each side may rely on

These are readings, not conveniences: each one is a case a producer and a consumer could otherwise read differently,
and each one is a test in `new-ops.test.ts`.

1. **An unknown role reads as `none`.** WH1's `role` is one of `viewer`, `operator`, `admin`, `none`; a CLI newer than
   this build may name a role this build has never heard of, and `none` is the one answer that disables every write
   rather than enabling one. A console that refused the document instead would leave every control enabled, which is
   the state the role read exists to prevent. The `scope` words are read the same way, except that an unknown scope is
   `null` rather than a word: nothing in the console enables an action from `scope` (RB4).
2. **A stream without an `end` is interrupted.** The four feeds are line-delimited, so the end of a stream is a line
   and not a length: `end` — or, for setup, a `done` event — is what says the CLI finished. A feed that stops without
   one is a read the console ended (a cap, a killed child, a lost channel) and is drawn as an interrupted read with
   Retry, never as a complete list that happens to be short. A feed that carries an `error` line is **failed**, and
   `end` is `null` for it whatever else the feed carries: a failure exits non-zero, so the run did not finish.
3. **An answers object never names a file.** Stated above; it is a rule, not a shape, which is why it is listed here
   as well as in the payload table.
4. **A line of an unknown `type` refuses the feed by name** (`lines[N].type: unknown-value`), rather than being
   skipped: a line this build cannot read is a line whose absence a screen cannot tell from a shorter day. The one
   exception inside a line is a member a shape may omit — see the next rule.
5. **An omitted member is the CLI saying it has nothing**, which is the console's house reading of `null` and of Go's
   `omitempty`. This is why an `end` line's `version`, `count` and `totals` are each `null` when absent: the caps
   document `budget show --json` carries no `version` at all, so refusing an `end` line without one would turn a
   stream that finished into a stream called interrupted. A member that **is** there and is not its kind is still
   refused by name.
6. **A reply that is not a value, a refusal or a decodable document is `unreadable`**, and never an empty value. A
   screen handed `{}` draws zeros nobody measured.
7. **Text is drawn through a class.** `NEW_OPERATION_TEXT_CLASSES` names, per wire field, the class of the console's
   own closed set (`TEXT_CLASSES`, `console/src/shared/sanitize.ts`) that SRC puts each field through before it
   answers and a screen re-sanitizes at the display layer.

## The answer each operation gives

Every answer arrives inside the console's ordinary reply (`{ok: true, data}` or `{ok: false, reason}`,
`ConsoleReply` in `ipc.ts`); `decodeNewOperationAnswer(name, answer)` reads the `data` half.

- **A feed** (`runs.stream`, `spend.stream`, `budget.stream`, `setup.plan`, `setup.apply`) has no document of its own
  on the wire, so its `data` is `{document: {lines: [...]}}`: the parsed lines, in the order the CLI wrote them.
  Everything a screen reads is derived from those lines in one place — `rows` (`runs.stream`'s rows are read by
  `decodeRunsList`, the console's one reader of a `runs ls --json` row, so the stream and the whole-document read
  cannot draw two different lists), `end`, `failure`; for setup, `events`, `done`, `failure`. A row of a feed this
  console has no reader for (`spend.stream`, `budget.stream`) is held to being an object and nothing more: the screen
  that draws it owns the members it reads.
- **A one-shot read** (`whoami`, `image.catalog.ls`, `recipes.ls`) answers the CLI's own document under `document`,
  unchanged: `{version, role, scope, repos}` (WH1), `{project, entries[]}` (`image ls --json`, `default` read as
  `isDefault`), `{recipes, notes}` (`recipes ls --json`, with `use_when`, `launchable`, `shadowed_by` and `problem`
  beside the human table's columns).
- **`settings.editor.open`** answers the outcome object itself — `{ok, sentence}` — exactly as `settings.edit` does,
  because it spawns nothing and there is no document to have been read.

## The fixtures, and the stub that held them

C-OPS answered the nine operations from `STUB_FIXTURES` in `src/renderer/stubs/bridge-stub.ts`, an inline fixture per
operation, each the `data` half described above, and **I-OPS deleted that file with the fixtures' own module**: the
answers now live in [`console/test/support/newOpAnswers.ts`](../../console/test/support/newOpAnswers.ts), which no
renderer module can reach, and `newOpAnswersBridge()` builds a bridge from them for a test that draws a screen, a
section or a gate. The bridge validates no payload: payload validation is the allow-list's job in main, and a fixture
bridge that checked one would be a second copy of a rule it does not own.

**Six of the nine answers are the CLI's own goldens**, transcribed byte for byte from five recorded files
(`internal/cli/testdata/runs_stream.golden.ndjson`, `whoami.golden.json`, `setup_events.golden.ndjson`,
`budget_stream.golden.ndjson` and `ee/report/cli/testdata/report_stream.golden.ndjson`; the setup file's three runs
give the plan, the apply and a refused `--check`), and `console/test/unit/ops-contract.test.ts` renders each one back
into the CLI's framing (NDJSON lines, or the two-space document) and compares it with the file, so a fixture that
drifts from the recording fails there. The other three — `image.catalog.ls`, `recipes.ls` and the settings editor's
outcome, for which the CLI records no golden — stay C-OPS's plausible values: what is pinned for them is the shape,
and never a byte of CLI output no task recorded. The two places the contract expected to need reconciling came out as
it guessed: the setup feed's `step` members, and `budget.stream`'s `end` line, which carries no version.

No consumer imports a fixture bridge. Each of SU1, RB4, RS2, RS5, MO-C, SW3a and SW3b builds its own fake bridge from
the types, and the two that read the shared answers import them from `test/support` rather than from the renderer.

## The one edit outside C-OPS's own files

Two tests in `console/test/unit/screen-literals.test.ts` hold the lists of the modules they scan as exact sets, on
purpose: "a module added to the renderer is a deliberate edit here". The two new modules that stayed
(`shared/new-ops.ts`, `renderer/new-bridge.ts`) are named in those two lists — the stub's own entry went with the
file, in I-OPS — and that is the whole of the change to that file. C-OPS adds no fixture directory, no build step and
no dependency.
