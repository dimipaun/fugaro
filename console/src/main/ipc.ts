// The IPC allow-list (desktop-console plan Task 1.2, SECURITY-SENSITIVE; design
// section 5.1 rule 5).
//
// The console has no IPC bridge. It has a fixed table of named operations --
// one per read the console performs, twelve of them for the eleven documents of
// design section 2.1 plus the one validated link -- and three prohibitions that
// are the whole of the rule:
//
//   - **no `invoke(channel, ...)`**: nothing anywhere in the console takes a
//     channel from a caller. `channelForOperation` derives a channel from a name
//     in *this file's* table and from nothing else, so any channel that reaches
//     `ipcRenderer.invoke` was computed here;
//   - **no channel name from the renderer**: `src/preload/index.ts` exposes one
//     method per operation -- `subscribeLive`, `runReport`, `openExternal` and
//     the rest of the table's `method` column -- and the renderer passes
//     *arguments* to a method, never a name, and never a channel;
//   - **no dynamic `require`**: nothing here loads code, or a module, by a
//     computed specifier. (The preload's one `require('electron')` is a literal,
//     guarded by the fact that a preload script always runs in a renderer -- see
//     that file.)
//
// **The arguments are validated in the MAIN process, and this is where.** The
// preload hands the renderer's object to `ipcRenderer.invoke` untouched; the
// listener this module registers validates it against the operation's closed
// schema *before* any handler sees it. Closed means: every key must be declared
// by the schema (an extra key is refused by name), `__proto__`, `constructor`
// and `prototype` are refused by name and a payload whose prototype is not
// `Object.prototype`/`null` is refused whole, a value of the wrong type is
// refused, and every string is bounded and refused when it carries a control
// character, a format character (a bidi override, a zero-width space, a BOM), an
// unpaired surrogate, or anything outside its grammar. The validated arguments
// are rebuilt into a fresh null-prototype object holding only the declared keys,
// so nothing a renderer sent is ever read back by prototype lookup.
//
// **The sender is checked before the arguments are, and that check is not
// optional.** A message is not trustworthy because it arrived on the right
// channel: any frame the application has loaded can invoke any registered
// channel, so a channel name alone is not a capability. `isConsoleSender` is the
// gate -- the sending frame must be the console's *own top frame*, on
// `app://console`, and nothing else -- and a message that fails it is answered
// with a fixed refusal that repeats nothing the sender wrote. This is the
// Electron security checklist's item 17 ("validate the sender of all IPC
// messages"); `src/main/protocol.ts` is where the origin is defined, and it is
// imported rather than spelled again so the two cannot drift.
//
// What an operation may not take matters as much as what it may: **no path**
// (the installation's directory and `doctor --dir` are main-process state, never
// a renderer argument, so there is no argument in this table of kind "path"),
// **no command** (the CLI verb is a constant in this table) and **no URL beyond
// the one validated external link**, which is the design's own
// `validateExternalUrl` from `src/main/window.ts` rather than a second check
// that could drift from it -- imported, not re-implemented.
//
// The second half of the file is the mutation-proof half. `NEVER_REACHABLE` is
// the table of commands and flags the console must never reach: every write
// subcommand on this tree, the `secrets` commands, `init`, `upgrade` and the
// hidden `exec`, the hidden `budget history` job command, `image git-credential`
// (it prints a credential), the project-centric phase 1 commands that write or
// publish, and the flags that make a read-only command into something else
// (`--follow`, `--check-github-app`, `--strict`, `--csv`, `--watch`, the watch
// renderer's `--plain`/`--poll`/`--ascii`/`--no-color`). Two things read it:
// `registerConsoleHandlers` refuses to register a table that drives one of them
// -- fail closed at startup, before the window exists -- and
// `test/unit/ipc.test.ts` fails a test *named for the entry* when one appears in
// the allow-list. The plan's red step is `budget kill` added to the allow-list,
// and it must make a named test red rather than pass a review.
//
// Two things are deliberately NOT here, and the plan's Files column is why:
//
//   - **the handler implementations.** This task defines what a handler may be
//     asked for and nothing about how it answers. Task 1.3 spawns the CLI behind
//     these operations; `registerConsoleHandlers` takes the handlers as an
//     argument so that the surface is complete and testable today and gains its
//     one implementation without an edit here.
//   - **the window's `preload` option.** Task 1.1 asserts its absence by name
//     (`test/unit/window.test.ts`), and attaching a bridge over handlers that do
//     not exist yet would be the one edit that makes this surface reachable
//     before there is anything behind it. The wiring is one line in
//     `src/main/window.ts` and belongs with the task that brings the handlers.

import { APP_HOST, APP_SCHEME } from './protocol'
import { validateExternalUrl } from './window'

/**
 * IPC_VERSION is the version of the surface `src/preload/index.ts` exposes to
 * the renderer, and it is carried *in* that surface rather than only in this
 * file, so a screen can tell which console it is talking to. It changes when the
 * shape of the surface changes -- an operation renamed, an argument's kind
 * changed, a method removed -- and not when a screen is added behind an
 * operation that already exists. Nothing about a version bump widens the
 * allow-list: that is an edit to `CONSOLE_OPERATIONS` below and to the test.
 */
export const IPC_VERSION = 1

/**
 * IPC_CHANNEL_PREFIX is the one prefix every channel name starts with. The
 * channel is an implementation detail of the main process and the preload: the
 * renderer never sees one, which is why this is exported for the tests that hold
 * that claim rather than for a caller.
 */
export const IPC_CHANNEL_PREFIX = 'fugaro:console/'

/** MAX_ARGUMENT_KEYS bounds how many keys a payload may even declare. */
export const MAX_ARGUMENT_KEYS = 8

/**
 * MAX_PAYLOAD_WIDTH bounds what a whole payload may add up to: every key's name
 * plus every scalar value's length. It is a second bound rather than the only
 * one -- each kind has its own (`kindLimit`), and a payload that declares more
 * than `MAX_ARGUMENT_KEYS` keys is refused before this -- and it exists because
 * the per-key bounds are only read one key at a time: without it a payload could
 * be eight arguments each just inside its own bound and still be far larger than
 * any screen has a reason to send.
 *
 * It is set *above* the widest payload the schema's own bounds admit
 * (`MAX_URL_LENGTH` plus seven `MAX_REPOSITORY_LENGTH`s, which no operation in
 * the table can actually reach -- the widest operation has four arguments), so
 * it cannot refuse something the per-kind bounds would have accepted. The test
 * asserts that relation rather than the number.
 *
 * What it deliberately does not do is bound the *deserialisation*: Electron
 * deserialises an `invoke`'s arguments before this module's listener is called,
 * so a payload larger than this one has already been copied by the time it can
 * be refused. The bound is on what this process then does with it.
 */
export const MAX_PAYLOAD_WIDTH = 4096

/**
 * MAX_REPLY_REASON_LENGTH bounds a refusal's text when it came from an error
 * rather than from this file. Design section 6.3 wants the CLI's own sentence
 * shown to the user, and a CLI sentence is short; a *hostile* one is not, and a
 * handler that threw with a megabyte in `error.message` would put that megabyte
 * in the renderer through a reply the renderer is entitled to trust. Nothing
 * legitimate is anywhere near this bound.
 */
export const MAX_REPLY_REASON_LENGTH = 2048

/** MAX_REPOSITORY_LENGTH bounds an `owner/name` argument in characters. It is
 * checked before the grammar runs, so a hostile payload cannot make a regular
 * expression work: every repository that matches the grammar is ASCII anyway,
 * and the bound bites first. */
export const MAX_REPOSITORY_LENGTH = 200

/** MAX_RUN_ID_LENGTH bounds a run id argument. */
export const MAX_RUN_ID_LENGTH = 64

/** MAX_URL_LENGTH bounds the one URL-shaped argument before it is parsed. */
export const MAX_URL_LENGTH = 2048

/**
 * REPORT_MAX_DAYS is `report --since`'s own bound on a range -- the CLI's
 * `reportMaxDays` (`internal/cli/report.go:40`), which exists because a query
 * reads a document per repository per day. A window past it is a value `report`
 * refuses, so the console refuses it here rather than forward it and let the
 * CLI's error be the answer.
 */
export const REPORT_MAX_DAYS = 3660

/** REPORT_MAX_WEEKS is that same bound in the other unit `report --since`
 * accepts: 522 weeks is 3654 days, and 523 is 3661 -- one day past the bound. */
export const REPORT_MAX_WEEKS = 522

/**
 * ArgumentKind is the closed set of things an operation's argument may be. Every
 * kind is a value the CLI's own flags already accept *in that position*, and
 * none of them is a path, a command, a flag name or a free-form string: a
 * payload with nowhere to put a path is a payload that cannot name a file.
 *
 * "In that position" is why a kind is per command and not per flag name: two
 * commands' `--since` are two grammars, and a kind shared between them would be
 * wrong in both directions at once -- admitting a value one command rejects, and
 * refusing one the other's own help advertises. Where a kind is *narrower* than
 * its flag, it is narrower on purpose and says so; where it is not, it accepts
 * exactly what the flag accepts. The test file carries a table per kind against
 * the command that kind belongs to, so a kind that drifts from its flag fails a
 * named test rather than a prose claim.
 *
 *   - `repository` -- `owner/name`, the canonical repository path the CLI's own
 *     `CanonicalRepo` produces (`internal/task/task.go`), lower-case ASCII;
 *   - `runId` -- a run id as `task.NewRunID` builds one,
 *     `YYYYMMDD-HHMMSS-xxxx`;
 *   - `day` -- a UTC calendar day, `YYYY-MM-DD`;
 *   - `reportSince` -- `report --since`'s own forms: a UTC day, `Nd`, or `Nw`,
 *     bounded by the same `reportMaxDays` the CLI bounds the range with
 *     (`internal/cli/report.go:40`, `reportRange` at `report.go:93`). A window
 *     in hours or minutes is *not* one of its forms, and neither is `0`;
 *   - `runSince` -- `runs ls --since`'s own forms, which are `parseSince`'s
 *     exactly (`internal/cli/runs.go:496`): `0` for no bound, `Nd` with at
 *     least one day, or a *positive Go duration* in any of `time.ParseDuration`'s
 *     units (`36h`, `90m`, `1h30m`, `1.5h`, `300ms`). **A UTC calendar day is
 *     not one of them** -- `parseSince` has no date branch, so `--since
 *     2026-10-16` is a `runs ls` refusal and this grammar refuses it before a
 *     child exists. The two commands disagree about `--since` and one kind
 *     cannot say both: `report` refuses `3h` and a bare `0` (its `reportRange`
 *     has no duration branch), and `runs ls` refuses `12w` and every date;
 *   - `reportDimension` -- `report --by`'s closed set of seven words;
 *   - `secretScope` -- `secrets ls`'s two mutually exclusive scopes;
 *   - `pullRequest` -- a pull request number, a positive integer;
 *   - `flag` -- a boolean, for the flags the allow-list admits;
 *   - `link` -- the one URL, and it must pass `validateExternalUrl`.
 */
export type ArgumentKind =
  | 'repository'
  | 'runId'
  | 'day'
  | 'reportSince'
  | 'runSince'
  | 'reportDimension'
  | 'secretScope'
  | 'pullRequest'
  | 'flag'
  | 'link'

/** ArgumentSpec is one field of an operation's closed argument schema. */
export interface ArgumentSpec {
  /** `key` is the argument's name, and the only spelling that is accepted. */
  readonly key: string
  /** `kind` is what the value must be -- see `ArgumentKind`. */
  readonly kind: ArgumentKind
  /**
   * `required` says whether the argument must be present. A *required*
   * argument that is absent, `undefined` or of the wrong type is refused;
   * an optional one is simply left out of the validated arguments.
   */
  readonly required: boolean
  /** `why` is why this argument is allowed to exist at all. */
  readonly why: string
}

/** ValidatedArgument is what a handler may receive: a string, a boolean, or a
 * pull request number. Nothing else -- no object, no array, no path. */
export type ValidatedArgument = string | boolean | number

/** ValidatedArguments is the closed, frozen, null-prototype object a handler
 * receives instead of the renderer's payload. */
export type ValidatedArguments = Readonly<Record<string, ValidatedArgument>>

/** ArgumentDecision is `validateArguments`'s answer: the validated arguments, or
 * the reason the payload was refused. */
export type ArgumentDecision =
  | { readonly ok: true; readonly args: ValidatedArguments }
  | { readonly ok: false; readonly reason: string }

/**
 * ConsoleOperation is one named operation: the versioned name the surface is
 * made of, the method the preload exposes for it, the read-only CLI command it
 * drives, the flags it fixes, and its closed argument schema.
 */
export interface ConsoleOperation {
  /** `name` is the operation's stable identifier (`live.watch`). */
  readonly name: string
  /** `method` is the name of the preload method the renderer calls. */
  readonly method: string
  /**
   * `command` is the CLI command path this operation drives -- and it is `null`
   * for exactly one operation, `link.openExternal`, which spawns nothing at all.
   * It is a constant: no renderer, no argument and no job-written string ever
   * reaches it, and `registerConsoleHandlers` refuses a table that names
   * anything on `NEVER_REACHABLE`.
   */
  readonly command: string | null
  /**
   * `flags` is the flags this operation fixes, and the surface may fix exactly
   * two: `--json` (a document) and `--url` (the one link). The flags a *user*
   * chooses -- `--project`, `--config`, `--repo R`, `--since`, `--dir` -- are
   * assembled from validated arguments by Task 1.3, and the ones that are
   * refused outright are on `NEVER_REACHABLE` with a flag shape.
   */
  readonly flags: readonly string[]
  /** `summary` is what the operation is for, in one line. */
  readonly summary: string
  /** `args` is the closed schema: the keys, and nothing but the keys. */
  readonly args: readonly ArgumentSpec[]
}

/**
 * CONSOLE_OPERATIONS is the whole surface. Twelve operations, and the table is
 * the reviewable artifact: a new read is a new entry here, with its argument
 * schema, and an entry is the only way anything becomes reachable.
 *
 * The eleven CLI commands are design section 2.1's list (which is
 * `docs/json-contract.md`'s list, read off the cobra commands), and the twelfth
 * is the one path that is not a command: `openExternal`, whose URL argument goes
 * through `validateExternalUrl` -- `https:` only, no credentials, no port, host
 * on `ALLOWED_EXTERNAL_HOSTS` -- so the renderer's only way out of the console
 * is a link the console would have opened anyway.
 *
 * Two operations share one command and differ by their flags, deliberately:
 * `run.logs` is `logs RUN --json` and `run.logLink` is `logs RUN --url`. They
 * cannot be combined -- `runLogs` refuses `--url` together with `--json`
 * (`internal/cli/logs.go`) -- so the surface must not be able to express that
 * argv, and two operations with one flag each cannot.
 */
export const CONSOLE_OPERATIONS = [
  {
    name: 'live.watch',
    method: 'subscribeLive',
    command: 'watch',
    flags: ['--json'],
    summary: 'the live feed: one document per change',
    args: [
      {
        key: 'repo',
        kind: 'repository',
        required: false,
        why: 'the design allow-list admits `--repo R` on this command: it narrows the feed, and the document with it.',
      },
    ],
  },
  {
    name: 'budget.show',
    method: 'showBudget',
    command: 'budget show',
    flags: ['--json'],
    summary: 'caps, counters, kill state and the effective per-run cap',
    args: [
      {
        key: 'repo',
        kind: 'repository',
        required: false,
        why: 'the design allow-list admits `--repo R` on this command.',
      },
      {
        key: 'all',
        kind: 'flag',
        required: false,
        why: 'the design allow-list admits `--all` on this command.',
      },
    ],
  },
  {
    name: 'spend.report',
    method: 'runReport',
    command: 'report',
    flags: ['--json'],
    summary: 'spend over time, by day, week, month, year, repo, model or person',
    args: [
      {
        key: 'by',
        kind: 'reportDimension',
        required: false,
        why: '`--by` is the breakdown switch the Spend screen draws; its seven words are closed.',
      },
      {
        key: 'since',
        kind: 'reportSince',
        required: false,
        why: '`--since` is the range picker, in `report`’s own forms: a day, `30d` or `12w`.',
      },
      {
        key: 'until',
        kind: 'day',
        required: false,
        why: '`--until` closes the range, and it is a UTC day.',
      },
      {
        key: 'repo',
        kind: 'repository',
        required: false,
        why: 'the design allow-list admits `--repo R` on this command.',
      },
    ],
  },
  {
    name: 'runs.list',
    method: 'listRuns',
    command: 'runs ls',
    flags: ['--json'],
    summary: 'the run list, and the whole of the no-Firebase degraded view',
    args: [
      {
        key: 'repo',
        kind: 'repository',
        required: false,
        why: 'the design allow-list admits `--repo R` on this command.',
      },
      {
        key: 'since',
        kind: 'runSince',
        required: false,
        why: 'the design allow-list admits `--since` on this command, in `runs ls`’s own forms: 7d, 36h, 90m, 1h30m or 0 — never a date.',
      },
      {
        key: 'mine',
        kind: 'flag',
        required: false,
        why: 'the design allow-list admits `--mine` on this command.',
      },
      {
        key: 'pr',
        kind: 'pullRequest',
        required: false,
        why: 'the design allow-list admits `--pr` on this command; it needs `--repo`, which the CLI enforces.',
      },
      {
        key: 'all',
        kind: 'flag',
        required: false,
        why: 'the design allow-list admits `--all` on this command.',
      },
    ],
  },
  {
    name: 'run.diagnose',
    method: 'diagnoseRun',
    command: 'diagnose',
    flags: ['--json'],
    summary: "one run's diagnostic view, whose row carries the run's PR fields",
    args: [
      {
        key: 'run',
        kind: 'runId',
        required: true,
        why: 'the one thing `diagnose RUN` needs, and it is an id the feed itself carried.',
      },
    ],
  },
  {
    name: 'run.logs',
    method: 'readRunLogs',
    command: 'logs',
    flags: ['--json'],
    summary: "a bounded window of one run's structured log lines",
    args: [
      {
        key: 'run',
        kind: 'runId',
        required: true,
        why: '`logs RUN` — and the console-side line cap and timeout of design section 2.3 are the main process’s, not an argument.',
      },
    ],
  },
  {
    name: 'run.logLink',
    method: 'runLogLink',
    command: 'logs',
    flags: ['--url'],
    summary: "the Cloud console link for one run's execution, one line of text",
    args: [
      {
        key: 'run',
        kind: 'runId',
        required: true,
        why: '`logs RUN --url`, which is its own argv and is never combined with `--json`.',
      },
    ],
  },
  {
    name: 'secrets.inventory',
    method: 'listSecrets',
    command: 'secrets ls',
    flags: ['--json'],
    summary: 'secret names, scope and version counts — never a value',
    args: [
      {
        key: 'scope',
        kind: 'secretScope',
        required: true,
        why: 'the design requires `--repo R` or `--project-wide` to be passed explicitly: unqualified, the command resolves this checkout’s origin, and a Finder-launched app has no checkout.',
      },
      {
        key: 'repo',
        kind: 'repository',
        required: false,
        why: 'the repository half of that pair, required by `validateArguments` when the scope is `repo`.',
      },
    ],
  },
  {
    name: 'accounts.inventory',
    method: 'listAccounts',
    command: 'accounts ls',
    flags: ['--json'],
    summary: 'the Claude accounts a repository may use, from the local config only',
    args: [
      {
        key: 'repo',
        kind: 'repository',
        required: true,
        why: '`accounts ls` resolves this checkout’s origin without `--repo`, which a Finder-launched app does not have.',
      },
    ],
  },
  {
    name: 'cli.version',
    method: 'cliVersion',
    command: 'version',
    flags: [],
    summary: 'the handshake: which CLI the console is driving',
    args: [],
  },
  {
    name: 'environment.report',
    method: 'environmentReport',
    command: 'doctor',
    flags: ['--json'],
    summary: 'the environment view: the project, the GCP project and its checks',
    args: [],
  },
  {
    name: 'link.openExternal',
    method: 'openExternal',
    command: null,
    flags: [],
    summary: "the one path to the user's browser, and the only URL-shaped argument",
    args: [
      {
        key: 'url',
        kind: 'link',
        required: true,
        why: 'the design’s validated external link: `https:` only, no credentials, no port, host on the allow-list. It spawns nothing.',
      },
    ],
  },
] as const satisfies readonly ConsoleOperation[]

/** OperationName is the closed union of the surface's operation names. */
export type OperationName = (typeof CONSOLE_OPERATIONS)[number]['name']

/** BridgeMethod is the closed union of the preload's method names. */
export type BridgeMethod = (typeof CONSOLE_OPERATIONS)[number]['method']

/** CONSOLE_OPERATION_NAMES is the surface's names, frozen, for the preload's
 * `operations` list and for the tests that hold the two in step. */
export const CONSOLE_OPERATION_NAMES: readonly OperationName[] = Object.freeze(
  CONSOLE_OPERATIONS.map((operation) => operation.name),
)

/** ALLOWED_OPERATION_FLAGS is the whole of what an operation may fix. `--json`
 * asks for the document the console draws; `--url` is `logs RUN --url`, which is
 * its own argv. Anything else an operation might want to fix is on
 * `NEVER_REACHABLE`'s flag rows. */
export const ALLOWED_OPERATION_FLAGS: readonly string[] = Object.freeze(['--json', '--url'])

/**
 * operationFor returns the table entry for a name, and throws on a name that is
 * not in the table. The throw is the point: this is the only lookup in the
 * console, and a caller that got a name from anywhere but the table is a bug
 * worth failing on rather than a read worth performing.
 */
export function operationFor(name: OperationName): ConsoleOperation {
  const operation = CONSOLE_OPERATIONS.find((candidate) => candidate.name === name)
  if (operation === undefined) {
    throw new Error(`${String(name)} is not a console operation: the surface is this file's table`)
  }
  return operation
}

/**
 * channelForOperation is the only place a channel name is built, and it derives
 * one from the table rather than accepting one. The preload calls it; the
 * renderer has no way to reach it, and no argument anywhere in the console is a
 * channel.
 */
export function channelForOperation(name: OperationName): string {
  return IPC_CHANNEL_PREFIX + operationFor(name).name
}

/** NeverReachableShape says what kind of thing an entry forbids. */
export type NeverReachableShape = 'command' | 'flag'

/** NeverReachable is one row of the never table. */
export interface NeverReachable {
  /** `shape` distinguishes a whole command from a flag on an allowed one. */
  readonly shape: NeverReachableShape
  /** `value` is the command path, or the flag. */
  readonly value: string
  /** `why` is why the console must never reach it. */
  readonly why: string
  /** `where` is the symbol or document it was read from. */
  readonly where: string
}

/**
 * NEVER_REACHABLE is the table the whole mutation-proof half turns on. It is read
 * off the code rather than off a design paragraph -- every `where` names the
 * file:line each command was defined in, grepped out of `internal/cli` on
 * 2026-10-16 -- and the five rows whose `where` names a plan are commands that
 * do not exist on this tree at all, kept here so that the day one of them
 * appears, adding it to the allow-list is already a red test.
 *
 * The flag rows are the reason this table is not only about verbs: `doctor`,
 * `logs` and `watch` are all read-only with no flags and each has a flag that
 * makes it something else, which is exactly why design section 3.4 rule 2 says
 * the allow-list is by exact argv and not by command name.
 */
export const NEVER_REACHABLE: readonly NeverReachable[] = Object.freeze([
  {
    shape: 'command',
    value: 'budget kill',
    why: 'a write: it turns a kill switch on, which halts running work and spends nothing further',
    where: 'internal/cli/budget.go:1115 (newBudgetKillCmd(true))',
  },
  {
    shape: 'command',
    value: 'budget resume',
    why: 'a write: it clears a kill switch, which lets halted work run again',
    where: 'internal/cli/budget.go:1115 (newBudgetKillCmd(false))',
  },
  {
    shape: 'command',
    value: 'budget set',
    why: 'a write: caps, the mode and the reserve bound',
    where: 'internal/cli/budget.go:703',
  },
  {
    shape: 'command',
    value: 'budget history',
    why: 'the hidden job command: it sweeps the run registry, records crashed runs, deletes old run users and moves finished days into Firestore',
    where: 'internal/cli/history.go:74 (Hidden: true)',
  },
  {
    shape: 'command',
    value: 'cancel',
    why: 'a write: it cancels a run',
    where: 'internal/cli/cancel.go:72',
  },
  {
    shape: 'command',
    value: 'run',
    why: 'a write: it launches a run, and a run spends money',
    where: 'internal/cli/run.go:134',
  },
  {
    shape: 'command',
    value: 'init',
    why: "a write: it creates a repository's Fugaro wiring",
    where: 'internal/cli/init.go:103',
  },
  {
    shape: 'command',
    value: 'upgrade',
    why: "a write: it replaces the CLI's own wiring and refreshes images",
    where: 'internal/cli/upgrade.go:79',
  },
  {
    shape: 'command',
    value: 'exec',
    why: "the hidden verb that runs an arbitrary command in a run's context — never a command from outside the console",
    where: 'internal/cli/exec.go:52 (Hidden: true)',
  },
  {
    shape: 'command',
    value: 'verify',
    why: "it runs a workflow's own build, test and lint commands in the checkout: the console executes nothing but the CLI's read-only verbs",
    where: 'internal/cli/verify.go:16',
  },
  {
    shape: 'command',
    value: 'update-skills',
    why: 'a write: it installs and updates the skills on the operator’s machine',
    where: 'internal/cli/updateskills.go:46',
  },
  {
    shape: 'command',
    value: 'config init',
    why: 'a write: it writes a Fugaro configuration',
    where: 'internal/cli/config_init.go:80',
  },
  {
    shape: 'command',
    value: 'config publish',
    why: 'a write: it publishes a layered configuration to the project',
    where: 'internal/cli/config_publish.go:31',
  },
  {
    shape: 'command',
    value: 'recipes publish',
    why: 'a write: it publishes a recipe',
    where: 'internal/cli/recipes.go:327',
  },
  {
    shape: 'command',
    value: 'image build',
    why: 'a write: it builds and pushes an image',
    where: 'internal/cli/image.go:77',
  },
  {
    shape: 'command',
    value: 'image gate',
    why: 'a write: it records a gate verdict',
    where: 'internal/cli/image_record.go:63',
  },
  {
    shape: 'command',
    value: 'image record',
    why: 'a write: it records a build record',
    where: 'internal/cli/image_record.go:148',
  },
  {
    shape: 'command',
    value: 'image refresh',
    why: 'a write: it refreshes the project’s images',
    where: 'internal/cli/image_refresh.go:330',
  },
  {
    shape: 'command',
    value: 'image git-credential',
    why: 'it prints a git credential — the console never handles a secret value, not even one it would only pass through',
    where: 'internal/cli/gitcred.go:42',
  },
  {
    shape: 'command',
    value: 'secrets set',
    why: 'a secret-value write: it stores a secret the console has no business holding, printing or passing on',
    where: 'internal/cli/secrets.go:97',
  },
  {
    shape: 'command',
    value: 'secrets get',
    why: 'reads a secret value. It does not exist on this tree — only `secrets set` and `secrets ls` do (internal/cli/secrets.go:60) — and it is listed so that a future command of that name cannot arrive in the allow-list quietly',
    where: 'design section 5.1 rule 6 (the never table); not on main 2026-10-16',
  },
  {
    shape: 'command',
    value: 'secrets rm',
    why: 'a secret-value write, and not on this tree (internal/cli/secrets.go:60 has only set and ls); listed so that a command of that name cannot arrive in the allow-list quietly',
    where: 'design section 5.1 rule 6 (the never table); not on main 2026-10-16',
  },
  {
    shape: 'command',
    value: 'secrets copy',
    why: 'a secret-value write, and not on this tree; listed so that a command of that name cannot arrive in the allow-list quietly',
    where: 'design section 5.1 rule 6 (the never table); not on main 2026-10-16',
  },
  {
    shape: 'command',
    value: 'secrets check',
    why: 'a secret-value read, and not on this tree; listed so that a command of that name cannot arrive in the allow-list quietly',
    where: 'design section 5.1 rule 6 (the never table); not on main 2026-10-16',
  },
  {
    shape: 'command',
    value: 'project policy publish',
    why: 'a write: it publishes the fleet’s policy',
    where: 'docs/plans/2026-10-14-project-centric.md Task 1.6 (PR #390): this tree has no `fugaro project` command at all',
  },
  {
    shape: 'command',
    value: 'project repos add',
    why: 'a write: it enrolls a repository in the allow-list',
    where: 'docs/plans/2026-10-14-project-centric.md Task 1.6 (PR #390); not on main 2026-10-16',
  },
  {
    shape: 'command',
    value: 'project repos rm',
    why: 'a write: it un-enrolls a repository. Un-enrollment is a security-relevant act, which is why it stays a typed command in a terminal',
    where: 'docs/plans/2026-10-14-project-centric.md Task 1.6 (PR #390); not on main 2026-10-16',
  },
  {
    shape: 'command',
    value: 'project sync',
    why: 'a write: it adds the missing repositories to the allow-list',
    where: 'docs/plans/2026-10-14-project-centric.md Task 4.1; not on main 2026-10-16',
  },
  {
    shape: 'command',
    value: 'sync',
    why: 'named by the design’s own never table as a writer; no `fugaro sync` exists on this tree',
    where: 'design section 5.1 rule 6 (the never table); not on main 2026-10-16',
  },
  {
    shape: 'flag',
    value: '--follow',
    why: 'an unbounded streaming tail that outlives the screen, and a log body the console promised not to embed',
    where: 'internal/cli/logs.go:55',
  },
  {
    shape: 'flag',
    value: '--check-github-app',
    why: "it reads a GitHub App's private key out of Secret Manager with the operator's own credentials: a name-based allow-list would let this through, and the app would be the thing that fetched a private key",
    where: 'internal/cli/doctor.go:198',
  },
  {
    shape: 'flag',
    value: '--strict',
    why: 'its failure mode is the checkout’s plugin wiring, which is not what the environment view draws',
    where: 'internal/cli/doctor.go:199; design section 3.4 rule 5',
  },
  {
    shape: 'flag',
    value: '--csv',
    why: 'a second document format the console does not read: the surface asks for `--json`',
    where: 'internal/cli/report.go:87',
  },
  {
    shape: 'flag',
    value: '--watch',
    why: '`watch` under another name; the console uses `watch`',
    where: 'internal/cli/runs.go:107',
  },
  {
    shape: 'flag',
    value: '--plain',
    why: 'the interactive renderer’s output, not a document',
    where: 'internal/cli/watch.go:162',
  },
  {
    shape: 'flag',
    value: '--poll',
    why: 'a reading mode the console chooses itself, never a renderer argument',
    where: 'internal/cli/watch.go:163',
  },
  {
    shape: 'flag',
    value: '--ascii',
    why: 'a terminal rendering choice; the console draws its own screen',
    where: 'internal/cli/watch.go:165',
  },
  {
    shape: 'flag',
    value: '--no-color',
    why: 'a terminal rendering choice; the console draws its own screen',
    where: 'internal/cli/watch.go:166',
  },
])

/**
 * isNeverReachable answers with the entry that forbids an operation's command
 * and flags, or `null` when the operation is one the console may drive. An
 * operation's command is matched *exactly* -- the allow-list is by exact argv,
 * and a command path is not a prefix of another command path here (`runs ls` is
 * not `run`) -- and its flags are matched one by one, so `doctor --json` is
 * allowed while `doctor --json --check-github-app` is not.
 */
export function isNeverReachable(operation: {
  readonly command: string | null
  readonly flags: readonly string[]
}): NeverReachable | null {
  for (const entry of NEVER_REACHABLE) {
    if (entry.shape === 'command' && entry.value === operation.command) return entry
    if (entry.shape === 'flag' && operation.flags.includes(entry.value)) return entry
  }
  return null
}

// The two grammars below are the CLI's own, not invented here: a repository is
// the canonical `owner/name` `task.CanonicalRepo` produces -- lower-case ASCII,
// nested groups allowed (`internal/task/task.go`) -- and a run id is what
// `task.NewRunID` builds, `YYYYMMDD-HHMMSS` plus four hex digits. The run id's
// hex tail is bounded at sixteen rather than four so that a future CLI that
// widens it does not make the console refuse a real run, and a repository's
// leading segment cannot be `.` or `..`, which the grammar refuses by requiring
// an alphanumeric first character.
const REPOSITORY_RE = /^[a-z0-9][a-z0-9._-]*(?:\/[a-z0-9][a-z0-9._-]*)+$/
const RUN_ID_RE = /^\d{8}-\d{6}-[0-9a-f]{4,16}$/
const DAY_RE = /^(\d{4})-(\d{2})-(\d{2})$/
// The two `--since` flags are two different grammars, and each one is written
// as its own kind so neither can admit a value its own command refuses. `report`
// takes days and weeks but not hours (`reportRange`, `internal/cli/report.go:93`
// -- an `h` suffix falls through to the date parser there); `runs ls` takes
// `parseSince`'s own forms (`internal/cli/runs.go:496`), which is what the two
// patterns below are: its grammar rather than a summary of it. `parseSince`
// tries the `d` suffix *first* (so `7d5h` is not a duration), then hands
// anything else to Go's `time.ParseDuration`, whose units are the eight below --
// `ns`, `us`, `µs` (U+00B5), `μs` (U+03BC), `ms`, `s`, `m`, `h` -- each with a
// number that may be fractional, and whose sign may be leading.
const SINCE_DAYS_RE = /^\+?\d+$/
const GO_DURATION_RE =
  /^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:ns|us|µs|μs|ms|s|m|h)(?:(?:\d+(?:\.\d*)?|\.\d+)(?:ns|us|µs|μs|ms|s|m|h))*$/
// `report`'s `--since` is its own grammar, and the digits are bounded by the
// CLI's own `reportMaxDays` above rather than by this pattern alone: `3660d` is
// the last day count `reportRange` accepts, and `523w` is one week past it.
const REPORT_SINCE_RE = /^(\d{1,4})([dw])$/
const REPORT_DIMENSIONS: readonly string[] = Object.freeze([
  'day',
  'week',
  'month',
  'year',
  'repo',
  'model',
  'person',
])
const SECRET_SCOPES: readonly string[] = Object.freeze(['project-wide', 'repo'])

/** PROTOTYPE_KEYS are the three names a payload may never carry. They are not
 * arguments of any operation, so the closed-schema check would refuse them
 * anyway; naming them means the refusal says what it is. */
const PROTOTYPE_KEYS: readonly string[] = Object.freeze(['__proto__', 'constructor', 'prototype'])

/**
 * REFUSED_PAIRS is every pair of arguments the CLI refuses *whatever else is
 * set*, keyed by operation, with the CLI's own sentence. Each pair is a
 * condition the CLI evaluates before it reads anything from an operator's
 * config -- `o.repo != "" && o.all` (`internal/cli/budget.go:387`,
 * `internal/cli/runs.go:118`) and `prSet && o.all` (`internal/cli/runs.go:121`)
 * -- which is what makes it decidable here. The surface must not be able to
 * express an argv that can only fail after a child has been spawned: the same
 * principle the `logs` `--json`/`--url` split states, and the reason
 * `secrets.inventory`'s scope pairing below is checked at all.
 *
 * `runs ls --pr` *without* `--repo` is deliberately not here: whether that is
 * refused depends on how many repositories the local config lists, and the
 * console does not read the operator's config (rule 7).
 */
const REFUSED_PAIRS: Readonly<Record<string, readonly (readonly [string, string, string])[]>> = Object.freeze({
  'budget.show': [['repo', 'all', "--repo and --all don't go together"]],
  'runs.list': [
    ['repo', 'all', "--repo and --all don't go together"],
    ['pr', 'all', 'runs ls --pr needs --repo: a pull request number means something only in one repository'],
  ],
})

/**
 * hiddenCharacterReason refuses the strings that look like one thing and are
 * another: a bidi override that reorders what a path or a host looks like, a
 * zero-width space that makes two different strings render identically, a BOM,
 * a soft hyphen, a control character, or an unpaired surrogate. It runs before
 * the grammar for every string argument and before the URL parser for a `link`,
 * because `new URL('https://github.com/​repo')` parses happily and would
 * otherwise reach `validateExternalUrl` with an invisible character in it.
 */
function hiddenCharacterReason(value: string): string | null {
  for (let index = 0; index < value.length; index++) {
    const code = value.charCodeAt(index)
    if (code < 0x20 || code === 0x7f) {
      return `a control character (U+${code.toString(16).toUpperCase().padStart(4, '0')})`
    }
    if (code >= 0x80 && code <= 0x9f) {
      return `a C1 control character (U+${code.toString(16).toUpperCase().padStart(4, '0')})`
    }
  }
  for (const character of value) {
    const code = character.codePointAt(0)
    if (code === undefined) continue
    if (code >= 0xd800 && code <= 0xdfff) return 'an unpaired surrogate'
    if (isFormatCharacter(code)) {
      return `an invisible format character (U+${code.toString(16).toUpperCase().padStart(4, '0')})`
    }
  }
  return null
}

/** isFormatCharacter is Unicode's `Cf` range as it matters here: the characters
 * that change how a string is read without being read. */
function isFormatCharacter(code: number): boolean {
  if (code === 0x00ad || code === 0x061c || code === 0x180e) return true
  if (code >= 0x200b && code <= 0x200f) return true
  if (code >= 0x202a && code <= 0x202e) return true
  if (code >= 0x2060 && code <= 0x2064) return true
  if (code >= 0x2066 && code <= 0x2069) return true
  if (code === 0xfeff || code === 0xfff9 || code === 0xfffa || code === 0xfffb) return true
  return false
}

/** isCalendarDay is `day`'s grammar plus a real date: `2026-02-30` is refused,
 * because a range picker that sends it would get a CLI error the screen cannot
 * explain. */
function isCalendarDay(value: string): boolean {
  const match = DAY_RE.exec(value)
  if (match === null) return false
  const year = Number(match[1])
  const month = Number(match[2])
  const day = Number(match[3])
  if (month < 1 || month > 12 || day < 1) return false
  return day <= daysInMonth(year, month)
}

/**
 * isRunsSince mirrors `parseSince` (`internal/cli/runs.go:496`) branch for
 * branch, and it exists because the console used to accept two things that
 * function always refuses: **a UTC calendar day** (there is no date branch) and
 * **a zero window** (`0d`, `0h`, `0m` -- `days < 1` and `d <= 0`). Both were
 * refused by the CLI after a child had been spawned, which is the one outcome
 * the schema exists to prevent.
 *
 * The branches, in the CLI's order:
 *
 *   1. `"0"` exactly -- no bound;
 *   2. a `d` suffix, whose digits `strconv.Atoi` reads and which must be at
 *      least one. The console's ten-character bound on a string argument is
 *      what keeps those digits inside `Atoi`'s range, so no integer bound is
 *      repeated here;
 *   3. anything else is a Go duration, and `parseSince` refuses `d <= 0`. A
 *      leading `-` is that case, and so is a duration whose every digit is
 *      zero (`0s`, `0h0m`); `+5h` is a positive duration the CLI accepts.
 */
function isRunsSince(value: string): boolean {
  if (value === '0') return true
  if (value.endsWith('d')) {
    const days = value.slice(0, -1)
    return SINCE_DAYS_RE.test(days) && Number(days) >= 1
  }
  if (!GO_DURATION_RE.test(value)) return false
  if (value.startsWith('-')) return false
  return /[1-9]/.test(value)
}

/** daysInMonth is the calendar's, leap years included. */
function daysInMonth(year: number, month: number): number {
  if (month === 2) return isLeapYear(year) ? 29 : 28
  const lengths = [31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31]
  return lengths[month - 1] ?? 0
}

/** isLeapYear is the Gregorian rule, written out rather than borrowed from
 * `Date`, so the check is the same in every time zone. */
function isLeapYear(year: number): boolean {
  return year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0)
}

/** isPlainPayload refuses anything that is not a plain object: an array, a
 * function, a class instance, a `Date`, a `Map`, and -- the case this exists for
 * -- an object whose prototype has been replaced, which is how a payload written
 * as `{__proto__: {...}}` arrives. */
function isPlainPayload(value: unknown): value is Record<string, unknown> {
  if (typeof value !== 'object' || value === null) return false
  if (Array.isArray(value)) return false
  const prototype: unknown = Object.getPrototypeOf(value)
  return prototype === Object.prototype || prototype === null
}

/** refuse builds the one refusal shape, so every failure names the operation's
 * argument and the reason and never echoes the value a renderer sent. */
function refuse(reason: string): { readonly ok: false; readonly reason: string } {
  return { ok: false, reason }
}

/**
 * validateValue checks one argument against its kind. The strings it accepts are
 * ASCII by construction (every grammar is), it bounds each kind's length before
 * matching, and the `link` kind is the only one that is not a grammar: it is
 * `validateExternalUrl`, the same function the open-external path uses, and the
 * value stored is the URL that function returned -- the parsed, normalised form
 * -- so that what is used is what was validated.
 */
function validateValue(
  operation: string,
  spec: ArgumentSpec,
  value: unknown,
): { readonly ok: true; readonly value: ValidatedArgument } | { readonly ok: false; readonly reason: string } {
  if (spec.kind === 'flag') {
    if (typeof value !== 'boolean') return refuse(`${spec.key} must be true or false`)
    return { ok: true, value }
  }
  if (spec.kind === 'pullRequest') {
    if (typeof value !== 'number' || !Number.isInteger(value) || value < 1 || value > 9_999_999) {
      return refuse(`${spec.key} must be a pull request number`)
    }
    return { ok: true, value }
  }
  if (typeof value !== 'string') return refuse(`${spec.key} must be a string`)
  const limit = kindLimit(spec.kind)
  if (value.length > limit) return refuse(`${spec.key} is longer than ${limit} characters`)
  const hidden = hiddenCharacterReason(value)
  if (hidden !== null) return refuse(`${spec.key} carries ${hidden}`)
  switch (spec.kind) {
    case 'repository':
      if (!REPOSITORY_RE.test(value)) return refuse(`${spec.key} must be a repository as owner/name`)
      // The CLI strips a trailing `.git` before it resolves a repository
      // (`task.CanonicalRepo`), so a string that carries one is a second
      // spelling of a repository that already has a canonical one. The surface
      // carries the canonical spelling only: one repository has one name here,
      // which is the identity the budget and kill-switch reads are keyed by.
      if (value.endsWith('.git')) return refuse(`${spec.key} must be the canonical repository, without .git`)
      return { ok: true, value }
    case 'runId':
      if (!RUN_ID_RE.test(value)) return refuse(`${spec.key} must be a run id`)
      return { ok: true, value }
    case 'day':
      if (!isCalendarDay(value)) return refuse(`${spec.key} must be a UTC day as YYYY-MM-DD`)
      return { ok: true, value }
    case 'reportSince': {
      // `report --since`: a UTC day, `30d`, or `12w`, and the window is bounded
      // exactly where `reportRange` bounds it. `24h` and `0` are not forms this
      // command has -- both fall through to its date parser and are refused.
      if (isCalendarDay(value)) return { ok: true, value }
      const window = REPORT_SINCE_RE.exec(value)
      if (window === null) {
        return refuse(`${spec.key} must be a UTC day, a number of days like 30d, or weeks like 12w`)
      }
      const weeks = window[2] === 'w'
      const days = Number(window[1]) * (weeks ? 7 : 1)
      if (days < 1 || days > REPORT_MAX_DAYS) {
        return refuse(
          `${spec.key} must be a window of at most ${REPORT_MAX_DAYS} days (${REPORT_MAX_WEEKS} weeks)`,
        )
      }
      return { ok: true, value }
    }
    case 'runSince':
      // `runs ls --since`, which is `parseSince` and nothing else: `0`, `Nd`
      // with at least one day, or a positive Go duration in any of
      // `time.ParseDuration`'s units. The point of mirroring it exactly is that
      // a form the CLI takes is a form the operator can ask for, and a form the
      // CLI refuses (`2026-10-16`, `0d`, `0h`, `0m`, `7w`) is refused here --
      // before a child exists, rather than by a sentence from a spawned CLI.
      if (!isRunsSince(value)) {
        return refuse(
          `${spec.key} must be 0, a number of days like 7d, or a Go duration like 36h, 90m or 1h30m`,
        )
      }
      return { ok: true, value }
    }
    case 'reportDimension':
      if (!REPORT_DIMENSIONS.includes(value)) return refuse(`${spec.key} must be one of ${REPORT_DIMENSIONS.join(', ')}`)
      return { ok: true, value }
    case 'secretScope':
      if (!SECRET_SCOPES.includes(value)) return refuse(`${spec.key} must be ${SECRET_SCOPES.join(' or ')}`)
      return { ok: true, value }
    case 'link': {
      const decision = validateExternalUrl(value)
      if (!decision.ok) return refuse(`${spec.key} ${decision.reason}`)
      return { ok: true, value: decision.url }
    }
    default:
      return refuse(`${operation} does not take ${spec.key}`)
  }
}

/** kindLimit is each kind's length bound, checked before any grammar runs so
 * that a hostile payload cannot make a regular expression work. */
function kindLimit(kind: ArgumentKind): number {
  switch (kind) {
    case 'repository':
      return MAX_REPOSITORY_LENGTH
    case 'runId':
      return MAX_RUN_ID_LENGTH
    case 'day':
      return 10
    case 'reportSince':
      // A day is `2026-10-16`; the longest window is `3660d`. The bound is the
      // longer of the two, and the grammar decides which one a value is.
      return 10
    case 'runSince':
      // The longest accepted form is a duration like `1h30m15s`; the bound is
      // also what keeps a `Nd` day count inside `strconv.Atoi`'s range, which
      // is why `isRunsSince` needs no integer bound of its own.
      return 10
    case 'reportDimension':
      return 8
    case 'secretScope':
      return 12
    case 'link':
      return MAX_URL_LENGTH
    default:
      return 0
  }
}

/**
 * payloadWidth is how wide a payload is for `MAX_PAYLOAD_WIDTH`: every own key's
 * name, plus every value's length. It is O(keys) and it never walks into a
 * value:
 *
 *   - a string counts its own length;
 *   - a number or a boolean counts a fixed eight characters, which is more than
 *     either can need (a boolean is at most five, a safe integer at most twenty
 *     digits) and keeps the cost independent of the value;
 *   - anything else -- an array, a nested object, a function, a symbol -- counts
 *     the same eight and is refused a moment later by its kind's own type check,
 *     because every kind in the table takes a string, a number or a boolean and
 *     nothing else. Reading an array's `length` would be O(1) as well, but a
 *     nested *object* has no width that is not a traversal, so nothing is
 *     counted for it and the type check is what refuses it.
 *
 * The bound this feeds is therefore a bound on the scalars a payload may carry
 * and not on the memory the message occupies: Electron deserialises an `invoke`'s
 * arguments before this listener is called, which is stated where the constant
 * is declared rather than left to be discovered.
 */
function payloadWidth(payload: object, keys: readonly string[]): number {
  let width = 0
  for (const key of keys) {
    width += key.length
    const value: unknown = (payload as Record<string, unknown>)[key]
    width += typeof value === 'string' ? value.length : 8
  }
  return width
}

/**
 * validateArguments is the main process's gate: the one place a renderer's
 * payload is looked at, and the only way a handler is reached. It answers with
 * the validated arguments -- a frozen, null-prototype object holding only the
 * schema's keys -- or with the reason it refused, which is written for a screen
 * to show and names the argument rather than repeating what was sent.
 *
 * It is called with a name from the table and never with a name from a payload:
 * `registerConsoleHandlers` closes over the entries, and the preload's methods
 * close over their own. A caller that passes a name outside the table is a
 * programming error and throws rather than answering.
 */
export function validateArguments(name: OperationName, raw: unknown): ArgumentDecision {
  const operation = operationFor(name)
  const supplied: unknown = raw === undefined ? {} : raw
  if (!isPlainPayload(supplied)) {
    return refuse(`${operation.name} takes a plain object of arguments`)
  }
  const keys = Object.keys(supplied)
  if (keys.length > MAX_ARGUMENT_KEYS) {
    return refuse(`${operation.name} takes at most ${MAX_ARGUMENT_KEYS} arguments`)
  }
  // The width of the whole payload, before a single value is looked at: the
  // per-kind bounds below are read one key at a time, so this is what keeps a
  // payload of eight arguments each just inside its own bound from being eight
  // times larger than anything a screen sends.
  if (payloadWidth(supplied, keys) > MAX_PAYLOAD_WIDTH) {
    return refuse(`${operation.name} takes a payload of at most ${MAX_PAYLOAD_WIDTH} characters`)
  }

  const specs = operation.args.filter((spec) => keys.includes(spec.key) || spec.required)
  const declared = new Set(operation.args.map((spec) => spec.key))
  for (const key of keys) {
    if (PROTOTYPE_KEYS.includes(key)) return refuse(`${key} is never an argument`)
    if (!declared.has(key)) return refuse(`${operation.name} takes no ${key}`)
  }

  const args: Record<string, ValidatedArgument> = Object.create(null) as Record<string, ValidatedArgument>
  for (const spec of specs) {
    const present = Object.hasOwn(supplied, spec.key) && supplied[spec.key] !== undefined
    if (!present) {
      if (spec.required) return refuse(`${spec.key} is required`)
      continue
    }
    const decision = validateValue(operation.name, spec, supplied[spec.key])
    if (!decision.ok) return decision
    args[spec.key] = decision.value
  }

  // The pairs the CLI refuses whatever else is set: an argv that can only fail
  // is one the surface must not be able to build, because the failure arrives
  // after a child process has already been spawned. `reachesArgv` and not mere
  // presence, because `all: false` is a flag the payload did not set -- the
  // reading `argvFor` gives a boolean (spawn.ts) -- and refusing that would
  // refuse a payload whose argv has no `--all` in it at all.
  const reachesArgv = (key: string): boolean => args[key] !== undefined && args[key] !== false
  for (const [first, second, sentence] of REFUSED_PAIRS[operation.name] ?? []) {
    if (reachesArgv(first) && reachesArgv(second)) return refuse(sentence)
  }

  // The pairing the design states for the secret inventory: the command resolves
  // "this checkout's origin" when it is given neither scope, and a
  // Finder-launched app has no checkout, so the surface must not be able to ask
  // for an unqualified read or for both halves at once.
  if (operation.name === 'secrets.inventory') {
    const scope = args['scope']
    const repo = args['repo']
    if (scope === 'repo' && repo === undefined) return refuse('repo is required when scope is repo')
    if (scope === 'project-wide' && repo !== undefined) {
      return refuse('repo and scope project-wide are mutually exclusive')
    }
  }

  return { ok: true, args: Object.freeze(args) }
}

/** ConsoleReply is what every operation answers with: the document, or the
 * reason it could not be read. Errors do not cross the boundary as exceptions --
 * a rejected promise a renderer cannot interpret is a worse answer than a
 * refusal it can draw. */
export type ConsoleReply =
  | { readonly ok: true; readonly data: unknown }
  | { readonly ok: false; readonly reason: string }

/**
 * ConsoleHandler is one operation's implementation. It receives the validated
 * arguments -- never the renderer's payload -- and answers with data that is
 * already rendered: the CLI's document, drawn as it arrived, with no URL, path
 * or command left for the renderer to build.
 */
export type ConsoleHandler = (args: ValidatedArguments) => unknown | Promise<unknown>

/** ConsoleHandlers is the whole set, one per operation. The mapped type is why
 * a missing handler is a compile error rather than an operation that answers
 * "not implemented" in production. */
export type ConsoleHandlers = { readonly [Name in OperationName]: ConsoleHandler }

/**
 * SENDER_REFUSAL is what a message from anywhere but the console's own top frame
 * is answered with. It is one fixed sentence and it is not formatted with
 * anything the sender supplied: a forged message does not get to write its URL,
 * its frame's title or anything else into a reply that a screen might draw.
 */
export const SENDER_REFUSAL = 'the console refuses a message that did not come from its own window'

/** SenderFrameLike is the slice of Electron's `WebFrameMain` the gate reads. */
export interface SenderFrameLike {
  /** `url` is the frame's current URL, which Electron reports as a string. */
  readonly url?: unknown
}

/** SenderContentsLike is the slice of Electron's `WebContents` the gate reads:
 * its top frame, which is what `senderFrame` must *be* rather than merely
 * resemble. There is deliberately no `id` here; `isConsoleSender` explains why
 * the webContents id is not part of this gate. */
export interface SenderContentsLike {
  readonly mainFrame?: SenderFrameLike | null
}

/**
 * ConsoleEventLike is the slice of Electron's `IpcMainInvokeEvent` the gate
 * reads. It is a structural type rather than the Electron one for the reason the
 * rest of this file gives -- the unit test hands it forged events under plain
 * Node -- and it is `unknown`-valued on purpose: what arrives here is not typed
 * by anything this process controls, and every field is checked before it is
 * used.
 *
 * `senderFrame` is `null` in Electron when the frame that sent the message has
 * been destroyed or has navigated away, which is a case that must be *refused*
 * rather than treated as "no sender information, carry on".
 */
export interface ConsoleEventLike {
  readonly senderFrame?: SenderFrameLike | null
  readonly sender?: SenderContentsLike | null
}

/**
 * isConsoleSenderUrl reports whether a frame URL is on the console's own origin:
 * the `app:` scheme, the one host `src/main/protocol.ts` serves, and no port and
 * no credentials in front of it.
 *
 * The comparison is on the *origin*, by hand, and not on a prefix of the string
 * or on `URL.origin`. A prefix test would admit `app://console.evil.test/`, and
 * `URL.origin` is `"null"` under Node for a scheme it does not treat as special
 * -- the console registers `app:` as standard in Electron and the WHATWG parser
 * lower-cases a standard scheme's host, but this module is unit-tested under
 * Node and a security check whose answer depends on which runtime asks is not a
 * check. Lower-casing the host here gives the same answer on both, and it is the
 * answer Chromium gives: `APP://CONSOLE/` is the same origin as `app://console/`.
 *
 * Anything after the authority -- a path, a query, a fragment -- is allowed:
 * every frame this application serves on that origin came out of its own bundle,
 * and `resolveBundleRequest` is what decides which paths exist.
 */
export function isConsoleSenderUrl(raw: unknown): boolean {
  if (typeof raw !== 'string' || raw === '') return false
  let url: URL
  try {
    url = new URL(raw)
  } catch {
    return false
  }
  return (
    url.protocol === `${APP_SCHEME}:` &&
    url.hostname.toLowerCase() === APP_HOST &&
    url.port === '' &&
    url.username === '' &&
    url.password === ''
  )
}

/**
 * isConsoleSender is the sender gate: it answers whether a message came from the
 * console's own top frame, and it refuses everything else.
 *
 * Three things must hold, and each of them is a real message Electron will
 * deliver:
 *
 *   - the event has a sending frame at all. `senderFrame` is null once that
 *     frame is gone, and a null frame has no origin to check;
 *   - `event.sender.mainFrame` *is* that frame. `ipcMain.handle` is registered
 *     on the process, not on a window, so every frame of every document in the
 *     application can reach it -- a subframe of a page the console did not
 *     write would otherwise pass an origin test by sitting inside a frame that
 *     did. Electron's own guidance is that an IPC handler must confirm the
 *     sender is its own main frame, and this is that confirmation: identity, not
 *     equality of URL strings;
 *   - and the frame's URL is on the console's own origin (`isConsoleSenderUrl`).
 *
 * What this does *not* do is keep a list of allowed webContents ids, and the
 * reason is that it would be state that can only go stale without adding a
 * check: handlers are registered at startup, before any window exists, and macOS
 * `activate` builds a new window afterwards, so a captured id is wrong by
 * design. It would also decide nothing the frame identity above does not already
 * decide. A frame is on `app://console` only if this process served it out of
 * its own bundle, and `loadURL(APP_ENTRY_URL)` in `createConsoleWindow` is the
 * only call that does that -- navigation away is denied, and the scheme is
 * handled in-process -- so "the console's own top frame" and "the console's own
 * window" are the same set, and every other frame is refused.
 */
export function isConsoleSender(event: ConsoleEventLike | null | undefined): boolean {
  if (event === null || event === undefined) return false
  const frame = event.senderFrame
  if (frame === null || frame === undefined) return false
  const sender = event.sender
  if (sender === null || sender === undefined) return false
  if (sender.mainFrame !== frame) return false
  return isConsoleSenderUrl(frame.url)
}

/** IpcMainLike is the slice of Electron's `ipcMain` this module uses. It is an
 * argument rather than an import for the reason `src/main/window.ts` gives: the
 * unit test drives every decision here under plain Node, and a module that
 * imported Electron could not be tested there at all.
 *
 * The listener's first parameter is typed, and that is part of the security
 * property rather than a tidiness: an untyped event is one a later edit can
 * ignore, and ignoring it is exactly how a handler ends up answering any frame
 * in the application. */
export interface IpcMainLike {
  handle(
    channel: string,
    listener: (event: ConsoleEventLike, ...args: unknown[]) => unknown,
  ): void
}

/**
 * replyReason turns anything a handler threw into the refusal's text, bounded
 * and string-only. The message is the CLI's own when the handler passed one
 * through -- design section 6.3 says a refused read shows the CLI's sentence
 * verbatim -- and a generic one otherwise, so a thrown non-Error cannot put an
 * object on the wire.
 *
 * "Bounded" is meant literally, and it is the three things `boundReason` does: a
 * message is flattened to one line (a CLI sentence is one line; a message with
 * newlines in it is either a stack or something written to look like the
 * console's own output), its absolute paths are replaced (the CLI's refusals
 * name flags and values, never the machine's directories, and a path in a
 * refusal is a path a screen would print), and it is cut at
 * `MAX_REPLY_REASON_LENGTH`. The CLI's own sentences are short enough that none
 * of the three touches them; a hostile message hits all three.
 */
function replyReason(error: unknown): string {
  if (error instanceof Error && error.message !== '') return boundReason(error.message)
  return 'the operation failed'
}

/**
 * ABSOLUTE_PATH_RE matches an absolute path in a message: a POSIX one (`/etc`,
 * `/home/me/x`), a home-relative one (`~/x`), or a Windows one with a drive
 * letter or a UNC prefix. It is anchored on a separator that follows whitespace,
 * a quote or the start of the string, so a flag like `--repo owner/name` keeps
 * its slash, and it stops at the first character that cannot be part of a path
 * in a sentence.
 *
 * A colon is deliberately *not* one of the anchoring characters: `https://x/y`
 * is a URL and not a path, and replacing from its `//` would turn the message
 * into `https:<path>`, which reads as a corrupted sentence rather than as a
 * redaction. A drive letter is still matched, because the anchor in front of it
 * is a space, a quote or the start of the message in every message that has one.
 */
const ABSOLUTE_PATH_RE = /(^|[\s'"(=,])(?:~[\\/]|[A-Za-z]:[\\/]|\\\\|\/)[^\s'")]*/g

/** PATH_PLACEHOLDER is what an absolute path in a message becomes. It names
 * *that* a path was there, which is what a reader needs, and not which one. */
const PATH_PLACEHOLDER = '$1<path>'

/**
 * boundReason is the one place a handler-authored message is made safe to put in
 * front of a user: control characters out, absolute paths out, one line, and a
 * hard length. It never returns an empty string -- a refusal with no text is a
 * screen with nothing to draw -- and it never echoes more of the message than
 * the bound allows.
 */
function boundReason(message: string): string {
  const flattened = message
    .replace(/[\u0000-\u001f\u007f-\u009f]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()
  if (flattened === '') return 'the operation failed'
  const redacted = flattened.replace(ABSOLUTE_PATH_RE, PATH_PLACEHOLDER)
  if (redacted.length <= MAX_REPLY_REASON_LENGTH) return redacted
  // The cut is marked, so a reader can tell a truncated sentence from a whole
  // one rather than wondering what the CLI meant to say.
  return `${redacted.slice(0, MAX_REPLY_REASON_LENGTH - 1).trimEnd()}…`
}

/**
 * assertReachableSurface throws unless every entry of a table is one the console
 * may drive: its command and its flags are clear of `NEVER_REACHABLE`, the flags
 * it fixes are the surface's own two, and no two entries share a name.
 *
 * It takes the table as an argument so that the guard is a pure function of one
 * and the test can hand it the mutation directly -- a table with `budget kill`
 * in it -- instead of the assertion being reachable only by editing this file.
 * `registerConsoleHandlers` calls it with the real table at startup, so the same
 * code path decides both; there is no second implementation to drift.
 */
export function assertReachableSurface(operations: readonly ConsoleOperation[]): void {
  const names = new Set<string>()
  for (const operation of operations) {
    const forbidden = isNeverReachable(operation)
    if (forbidden !== null) {
      throw new Error(
        `${operation.name} drives ${forbidden.value}, which the console must never reach: ${forbidden.why} (${forbidden.where})`,
      )
    }
    for (const flag of operation.flags) {
      if (!ALLOWED_OPERATION_FLAGS.includes(flag)) {
        throw new Error(`${operation.name} fixes ${flag}, which is not a flag the surface may fix`)
      }
    }
    if (names.has(operation.name)) {
      throw new Error(`${operation.name} appears twice, so the surface has two operations with one name`)
    }
    names.add(operation.name)
  }
}

/**
 * registerConsoleHandlers puts one listener per operation on `ipcMain`, and it
 * is the last place the allow-list is checked: an operation that drives anything
 * on `NEVER_REACHABLE`, a handler that is not a function, and a table whose
 * entries do not correspond one-to-one with the surface all throw here, at
 * startup, before a window exists. Failing closed at registration is the point --
 * a console that cannot be built with `budget kill` in it cannot be talked into
 * running it later.
 *
 * The listener validates first and answers a refusal to a payload that does not
 * match the schema; the handler only ever sees validated arguments. A handler
 * that throws is answered as a refusal too, never as a rejected promise.
 */
export function registerConsoleHandlers(ipcMain: IpcMainLike, handlers: ConsoleHandlers): void {
  assertReachableSurface(CONSOLE_OPERATIONS)
  for (const operation of CONSOLE_OPERATIONS) {
    // The type says this cannot be missing; the check is here because the type
    // is not what runs at startup. A table entry with no handler behind it is a
    // console that would answer "no handler registered for ..." to a user, and
    // refusing to build is the cheaper failure.
    const handler = handlers[operation.name]
    if (typeof handler !== 'function') {
      throw new Error(`${operation.name} has no handler: the console registers the whole surface or none of it`)
    }
    // The sender check comes first and is not negotiable: a message from any
    // frame but the console's own top frame is answered with one fixed sentence
    // and never reaches validation, the handler, or anything that would parse
    // what it carried. `dispatch` is then the only path to a handler, and it is
    // the only caller of `validateArguments`.
    ipcMain.handle(channelForOperation(operation.name), (event: ConsoleEventLike, raw: unknown) =>
      isConsoleSender(event) ? dispatch(operation.name, handler, raw) : { ok: false, reason: SENDER_REFUSAL },
    )
  }
}

/** dispatch is the listener's body: validate, answer a refusal rather than a
 * rejection, and hand a handler nothing but validated arguments. */
async function dispatch(
  name: OperationName,
  handler: ConsoleHandler,
  raw: unknown,
): Promise<ConsoleReply> {
  const decision = validateArguments(name, raw)
  if (!decision.ok) return { ok: false, reason: decision.reason }
  try {
    return { ok: true, data: await handler(decision.args) }
  } catch (error) {
    return { ok: false, reason: replyReason(error) }
  }
}
