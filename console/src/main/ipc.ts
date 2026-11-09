// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The IPC allow-list (desktop-console plan Task 1.2, SECURITY-SENSITIVE; design
// section 5.1 rule 5).
//
// The console has no IPC bridge. It has a fixed table of named operations --
// one per read the console performs, the CLI reads among them for the documents
// the dashboard draws out of design section 2.1, plus the two links (the one
// validated URL the console is handed, and the pull request link it builds from
// three fields of a document, K2a, D815) -- and three prohibitions that are the
// whole of the rule. (That section's list holds a thirteenth row, the picker's
// own `fugaro config list --json`; it belongs to the installations picker window
// and is not an operation of this table -- `src/preload/installations.ts` is its
// surface.)
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
// subcommand on this tree -- including the converge, `setup` and `update`, which
// create a project and apply Terraform -- the `secrets` commands, the retired
// `init` tombstone, `upgrade` and the hidden `exec`, the hidden
// `budget history` job command, `image git-credential`
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

import { isAbsolute } from 'node:path'

import { invisibleRe, isInvisibleCodePoint, isSeparatorCodePoint } from '../shared/sanitize'
import { REDUCED_VIEWS, hidesView, unreadableCompatibility } from '../shared/version'
import type { CliCompatibility, ReducedView } from '../shared/version'
import { APP_ENTRY_URL, APP_HOST, APP_SCHEME } from './protocol'
import { PULL_REQUEST_PROVIDERS, pullRequestUrl, validateExternalUrl } from './window'
import { checkLaunchPlanTask, checkLaunchTask, checkLaunchTitle } from './launchrules'
import { MAX_LICENCE_KEY_LENGTH } from './licence'

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

/**
 * SETTINGS_VALUE_MAX_LENGTH bounds the one argument of this surface that is
 * **not** held to the renderer's own alphabet: a setting's new value (the
 * settings-edit plan's C3, D1104). It is the console's own bound -- the CLI
 * takes a value of any length -- and it is here rather than in the grammar
 * because the value is passed behind a `--` terminator as one argv element, so
 * what has to be bounded is how large a word this process will hand a child.
 * `MAX_PAYLOAD_WIDTH` still bounds the whole payload, so this is the widest
 * single value a screen may send and not the only bound on it. */
export const SETTINGS_VALUE_MAX_LENGTH = 1024

/** MAX_RUN_ID_LENGTH bounds a run id argument. */
export const MAX_RUN_ID_LENGTH = 64

/** MAX_URL_LENGTH bounds the one URL-shaped argument before it is parsed. */
export const MAX_URL_LENGTH = 2048

/**
 * MAX_INSTALLATION_NAME_LENGTH and MAX_INSTALLATION_CHECKOUT_LENGTH bound the
 * rail's two kinds, and they are the same two numbers the store's own field
 * bounds use (`MAX_INSTALLATION_PROJECT_LENGTH` and
 * `MAX_INSTALLATION_PATH_LENGTH` in `src/main/installations.ts`).
 *
 * They are **spelled here rather than imported**: `installations.ts` imports
 * this module (`NO_ACTIVE_INSTALLATION`), so importing it back would be a cycle
 * -- and the grammar is the store's, which is why a test
 * (`test/unit/ipc.test.ts`) holds the two spellings to the same number and the
 * two validators to the same verdict, value for value, rather than this file
 * promising it in prose.
 */
export const MAX_INSTALLATION_NAME_LENGTH = 200
export const MAX_INSTALLATION_CHECKOUT_LENGTH = 1024

/**
 * REPORT_MAX_DAYS is `report --since`'s own bound on a range -- the CLI's
 * `reportMaxDays` (`ee/report/cli/report.go:39`), which exists because a query
 * reads a document per repository per day. A window past it is a value `report`
 * refuses, so the console refuses it here rather than forward it and let the
 * CLI's error be the answer.
 */
export const REPORT_MAX_DAYS = 3660

/** REPORT_MAX_WEEKS is that same bound in the other unit `report --since`
 * accepts: 522 weeks is 3654 days, and 523 is 3661 -- one day past the bound. */
export const REPORT_MAX_WEEKS = 522

/**
 * RUNS_LIST_MAX_LIMIT bounds the page `runs ls --limit=<n>` may be asked for
 * from here, and **it is the console's own bound rather than the CLI's**: the
 * command takes any count `strconv.Atoi` reads up to int, and a page exists
 * because reading and holding a whole window is what the job list was built to
 * avoid (the console coordinator-v2 reconciliation's D818 and its F5 behind it).
 * A renderer that could ask for more than this could ask for the window by
 * another name -- `runs ls --since 0` is one argv and no page at all -- so the
 * ceiling is stated once, here, and both the schema and the argv builder read
 * it: the second would otherwise let `1e21` through as the word `1e+21`, which
 * cobra's `IntVar` refuses only after a child exists.
 */
export const RUNS_LIST_MAX_LIMIT = 200

/**
 * SUMMARY_SINCE_WINDOWS is the closed set of windows `runs summary --since` may
 * be asked for from here, and **it is the console's own set rather than the
 * CLI's**. The command takes every form `parseSince` reads -- `7d`, `36h`,
 * `90m`, a year of days -- but the screens this read exists for need exactly
 * two: the day they open on, and the week the budget's own live window covers,
 * which is also the command's default (`summaryDefaultSince`, `internal/cli/
 * runs_summary.go`). The read is per UTC day, so the CLI enumerates the window's
 * days and refuses a span with no lower bound; a renderer that could ask for any
 * window could ask for the whole history under another name. The set is
 * therefore stated once, here, and both the schema and the argv builder read it.
 *
 * `7d` is first because it is the command's own default: a caller that asks for
 * no window gets it, and one that asks names one of the two.
 */
export const SUMMARY_SINCE_WINDOWS: readonly string[] = Object.freeze(['7d', '1d'])

/**
 * GRAPH_SCOPE_RE is the closed shape of a `graph.read` scope argument: the two
 * selector words, and an id-carrying scope whose id is one lower-case letter or
 * digit followed by up to 39 more of those or a hyphen. It is **the CLI's own
 * pattern, spelled the same way** (`graph.ParseScope`'s `scopeIDPattern`,
 * `ee/graph/key.go`), because the value reaches argv from a renderer and both
 * ends have to refuse the same strings: an id this process admitted and that
 * command refused would be a child spawned to fail.
 *
 * Anchored, so nothing before or after it gets in. A leading `-` (which would
 * reach argv as a flag, the plan's H1), a path separator, an uppercase letter,
 * a whitespace character, a `.`, an `@` and a 41-character id are each refused
 * here -- before a child exists -- and `isSafeArgumentValue` refuses the leading
 * `-` and the alphabet a second time on the way into the argv.
 */
const GRAPH_SCOPE_RE = /^(?:all|coordinator|(?:orchestrator|local):[a-z0-9][a-z0-9-]{0,39})$/

/**
 * MAX_GRAPH_SCOPE_LENGTH bounds a scope argument before the grammar runs, so a
 * hostile payload cannot make the regular expression work. It is the longest
 * value the pattern admits: `orchestrator:` plus a 40-character id.
 */
export const MAX_GRAPH_SCOPE_LENGTH = 'orchestrator:'.length + 40

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
 *     (`ee/report/cli/report.go:39`, `reportRange` at `report.go:116`). A window
 *     in hours or minutes is *not* one of its forms, and neither is `0`;
 *   - `runSince` -- `runs ls --since`'s own forms, which are `parseSince`'s
 *     exactly (`internal/cli/runs.go:502`): `0` for no bound, `Nd` with one to 106751
 *     days, or a *positive Go duration* in any of `time.ParseDuration`'s
 *     units (`36h`, `90m`, `1h30m`, `1.5h`, `300ms`). **A UTC calendar day is
 *     not one of them** -- `parseSince` has no date branch, so `--since
 *     2026-10-16` is a `runs ls` refusal and this grammar refuses it before a
 *     child exists. The two commands disagree about `--since` and one kind
 *     cannot say both: `report` refuses `3h` and a bare `0` (its `reportRange`
 *     has no duration branch), and `runs ls` refuses `12w` and every date;
 *   - `runLimit` -- `runs ls --limit`'s own page size: a positive integer, at
 *     most `RUNS_LIST_MAX_LIMIT`. **`0` is not one of its values** even though
 *     the CLI takes it (`--limit 0` is "every row of the window"), because that
 *     is exactly what omitting the argument means here: a payload that asks for
 *     it asks for nothing, and a value that is a string (`"50"`), fractional
 *     (`1.5`) or past the cap is refused before a child exists rather than by a
 *     spawned CLI's flag parser;
 *   - `runAfter` -- `runs ls --after`'s cursor: a run id in `runId`'s own
 *     grammar, and it is the id the same read printed (`runview.Row.run_id`).
 *     It is a separate kind from `runId` for the reason the two `--since`s are
 *     separate kinds: the *position* differs -- this one is emitted as the `=`
 *     form `--after=<id>`, one word, so a value can never read as a flag;
 *   - `summarySince` -- `runs summary --since`'s window: one of
 *     `SUMMARY_SINCE_WINDOWS` and nothing else. It is a closed set rather than
 *     `parseSince`'s grammar (`runSince`) because the screens this read serves
 *     need two windows and no more, and it is its own kind rather than a reuse
 *     of `runSince` because the *position* differs as well: the row emits it as
 *     the `=` form `--since=<window>`, one word, so a value can never read as a
 *     flag;
 *   - `reportDimension` -- `report --by`'s closed set of eight words;
 *   - `secretScope` -- `secrets ls`'s two mutually exclusive scopes;
 *   - `pullRequest` -- a pull request number, a positive integer;
 *   - `flag` -- a boolean, for the flags the allow-list admits;
 *   - `link` -- the one URL, and it must pass `validateExternalUrl`;
 *   - `gitProvider` -- the closed set of two words the coordinator stores a
 *     repository's host as, in the stored view's own `provider` field
 *     (`github`, `bitbucket`: `PULL_REQUEST_PROVIDERS`, `src/main/window.ts`).
 *     It is read from the builder's own table rather than spelled again here,
 *     so a provider the schema admits is one `pullRequestUrl` has a host for.
 *     It is the one kind whose value is a document field rather than a CLI
 *     flag's, and it exists because the pull request link is **built** from
 *     that field instead of read out of the document's `url` (D814, D815);
 *   - `installationName` -- one installation's project name, the handle the CLI
 *     itself printed for it (`isProjectName`, `src/main/installations.ts`): not
 *     empty, at most `MAX_INSTALLATION_NAME_LENGTH`, no whitespace, no control
 *     character, no leading `-`. It is a bare word, and **what makes it a name
 *     rather than a path is the store and not this grammar**: the value is
 *     looked up among the records this console holds and one it does not hold
 *     is refused (`picker.ts`), so a path-shaped string the grammar admits has
 *     no record to reach anything through;
 *   - `installationCheckout` -- a directory **the rail offered** for that
 *     installation (`isAbsolutePath`): an absolute path, at most
 *     `MAX_INSTALLATION_PATH_LENGTH`, no control character, no leading `-`. The
 *     shape is all this grammar can decide; whether the rail offered the
 *     directory -- one the CLI listed, or the console's own recorded checkout,
 *     and in either case not a temporary or tool-owned path -- is the main
 *     process's check (`offeredCheckouts`, `picker.ts`), because a validator
 *     that guessed would be the place a made-up path got through.
 *
 * The last two are the only path-shaped kinds on this surface, and they are the
 * installation rail's (owner's design ruling of 2026-10-25). Both are the
 * grammar `src/main/installations.ts` already applied to the picker's own
 * operations: they are moved onto the dashboard's table with the rail, not
 * invented here, and `test/unit/installationstate.test.ts`'s sibling
 * `test/unit/ipc.test.ts` holds the two implementations to the same verdicts
 * value for value.
 *
 * **The last three are the settings write's** (the settings-edit plan's C3, and
 * they are the only kinds on this surface whose operation is a write of a
 * *document* rather than of a run). `settingOp` is the edit's subcommand, a
 * closed set of two (`set`, `unset`); `settingKey` is a key of the read's own
 * document, a dotted identifier whose shape this module checks and whose
 * *membership* the CLI owns (D1102 -- the console keeps no key list of its
 * own); and `settingValue` is the operator's new value, the one argument here
 * that is passed to the child behind a `--` terminator and the one that is not
 * held to the renderer's alphabet, because a value may be any text at all
 * (D1104). Its bound is `SETTINGS_VALUE_MAX_LENGTH`, its hidden-character rule
 * is the common one, and `settingsEdit`'s pairing rule in `validateArguments`
 * is what ties the three together: a `set` carries a value and an `unset` does
 * not.
 *
 * **`graphSource` is the graph read's one argument** (the graph plan's CG9a):
 * the scope the document is about, held to `GRAPH_SCOPE_RE` -- the CLI's own
 * anchored pattern -- and, at the source, to the set a previous read of the same
 * document listed (F21). It is the one kind here whose *shape* is a closed
 * grammar and whose *membership* is the read's own answer, which is why the two
 * checks live apart: this module refuses a spelling no scope could have, and
 * `createBoundSource` refuses an id the CLI never named.
 *
 * **`licenceKey` is the licence section's one argument** (subscriptions plan
 * S2.7), and it is the only argument on this surface whose value is a
 * **credential**: the key the operator pasted. Its bound is the one the key
 * format shares with the console's store (`MAX_LICENCE_KEY_LENGTH`,
 * `src/main/licence.ts`), and its own rule is the paste's: whitespace around a
 * key is an artifact of copying it and is stripped, and nothing else about the
 * value is judged here -- the format, the id and the signature are the
 * verifier's (S2.4), whose sentence the screen shows. **It is never an argv, and
 * this is the only kind that never becomes one at all**: the operation it
 * belongs to spawns nothing, and what the key reaches is the child's
 * *environment* and never a command line (S2.1's hand-off, §5.1 rule 8).
 */
export type ArgumentKind =
  | 'repository'
  | 'runId'
  | 'day'
  | 'reportSince'
  | 'runSince'
  | 'runLimit'
  | 'runAfter'
  | 'summarySince'
  | 'reportDimension'
  | 'secretScope'
  | 'pullRequest'
  | 'flag'
  | 'link'
  | 'gitProvider'
  | 'installationName'
  | 'installationCheckout'
  | 'launchTask'
  | 'launchTitle'
  | 'launchPlanTask'
  | 'settingOp'
  | 'settingKey'
  | 'settingValue'
  | 'graphSource'
  | 'licenceKey'

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
   * for every operation that spawns nothing: the two reads of the console's own
   * state (`link.openExternal`, `startup.compatibility`), the active
   * installation (`installation.current`), and the rail's five store-and-window
   * operations (`installation.choose`, `.checkout`, `.remove`, `.reveal`,
   * `.open`).
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
  /**
   * `payloadWidth` replaces `MAX_PAYLOAD_WIDTH` for the one operation whose
   * legitimate payload is larger than that bound: `run.launch`, whose task text
   * may be 16 KiB. It is a column rather than a widening of the global bound,
   * because the bound every other read is held to is a fact about those reads.
   */
  readonly payloadWidth?: number
}

/**
 * LAUNCH_TASK_RAW_LIMIT, LAUNCH_TITLE_RAW_LIMIT and LAUNCH_PLAN_TASK_RAW_LIMIT
 * are the **raw** bounds on the launch's three strings, and they are four times
 * the content caps rule 11 sets (`launchrules.ts`'s 16 KiB, 100 runes and 128
 * bytes). They are not the caps: the caps are the rules', and they are checked
 * after these, on the *trimmed* value. What these do is bound the work this
 * process does on a string a renderer chose -- a raw task string of many
 * megabytes is refused by its length before anything measures or trims it --
 * and they are deliberately generous enough that no legitimate value can hit
 * them: a task is at most 16 KiB *after* its surrounding whitespace is trimmed,
 * so four times that is far above anything the schema admits.
 *
 * **The launch's own payload width is the other half of the same rule.**
 * `MAX_PAYLOAD_WIDTH` is 4096 and the task alone may legitimately be 16 KiB, so
 * `run.launch` carries its own width (`payloadWidth` on the operation) rather
 * than widening the bound every read is held to.
 */
export const LAUNCH_TASK_RAW_LIMIT = 4 * 16 * 1024
export const LAUNCH_TITLE_RAW_LIMIT = 4 * 100
export const LAUNCH_PLAN_TASK_RAW_LIMIT = 4 * 128
/** LAUNCH_PAYLOAD_WIDTH bounds the launch's whole payload: its four raw limits,
 * the two names, the five keys' names and a margin. It is a bound on a payload
 * this console wrote a schema for, so it is the sum of what that schema admits. */
export const LAUNCH_PAYLOAD_WIDTH =
  LAUNCH_TASK_RAW_LIMIT + LAUNCH_TITLE_RAW_LIMIT + LAUNCH_PLAN_TASK_RAW_LIMIT + 4096

/**
 * CONSOLE_OPERATIONS is the whole surface. Thirty-seven operations, and the table
 * is the reviewable artifact: a new read is a new entry here, with its argument
 * schema, and an entry is the only way anything becomes reachable.
 *
 * The thirteen CLI argvs are design section 2.1's list -- its thirteenth row,
 * `fugaro config list --json`, arrived with the installation rail (owner's
 * design ruling of 2026-10-25) and was §5.1 rule 6's thirteenth argv before
 * that -- and that list is `docs/json-contract.md`'s, read off the cobra
 * commands. The twelfth of those commands is `runs.stats` (console L5b, the
 * Spend screen's by-person view), which the table narrowed to this task's own
 * argument: see its comment below.
 *
 * **GV2.1's `plan status --json` is this surface's fourteenth argv**: section
 * 2.1's fourteenth row and §5.1 rule 6's `plan status --json` row (the
 * console-v2 plan's GV1.6 ships the document, GV2.1 is this operation, and
 * `console/docs/json-contract.md` §6.12 describes it). `plan.status`'s own
 * comment below says why the directory it reads in never reaches it.
 *
 * **CG9a's `graph --json` is this surface's twenty-first argv** (the graph
 * plan's CG9a; the CLI half is CG3, `ee/graph/cli/graph.go`, and
 * `console/docs/json-contract.md` §6.22 describes its document). It is the
 * Graph screen's other read and the one that needs no checkout, and it is the
 * only argv here whose one argument is held to a closed grammar *and* to a
 * membership a previous read of the same document listed (F21) -- its own row
 * below says why.
 *
 * **CG9b's `graph --watch --json` is this surface's twenty-second argv** (the
 * graph plan's CG9b): the same document, streamed. It is the first argv here
 * whose child is meant to live past its answer, which is why it is the first row
 * whose command path carries a **mode** (`graph --watch`) rather than only the
 * command's own words, and why its row below says what stops it: the Graph tab
 * leaving the screen, which the renderer states with the one argument of this
 * surface that reaches no argv at all.
 *
 * **T1.2's `coordinator status --from-store --all --json` is this surface's
 * fifteenth argv** (Bitbucket credentials T1.2, D364): the project view, read
 * from the coordinator's stored view, which is the one read here that needs no
 * credential of the host's. Its own comment below says why `--from-store` and
 * `--all` are fixed words of its argv rather than arguments, and
 * `console/docs/json-contract.md` §6.13 describes its document.
 *
 * **`runs summary --json` is this surface's nineteenth argv** (console
 * 0.119.0's jobs-free totals read; the CLI half is B4, #1580, and
 * `console/docs/json-contract.md` §6.17 describes its document). It is the one
 * read here that answers from the budget database and the spend history alone,
 * so its child opens no runs bucket, no compute backend and no provider. Its own
 * comment below says why its one argument is the console's closed set of windows
 * and why it is withheld with the Spend screen's reads.
 *
 * **`models ls --json` is this surface's twentieth argv** (console 0.119.0's
 * read-only models panel; the CLI half is M1, #1553, and
 * `console/docs/json-contract.md` §6.18 describes its document). It is the one
 * argv here whose **group** the console may otherwise not touch: `fugaro models`
 * has four other words -- `search`, `add`, `rm` and `use` -- and each is on the
 * never table below, the three writers as commands and all four as words of
 * `models`, so this row is the group's whole reachable argv. It takes no
 * argument at all and its child reaches no bucket, no database and no host.
 *
 * **The managed screens' two reads are the newest argvs on this surface** (the
 * managed screens' plan, CM4, D1472 to D1477 and D1480 and D1481): `managed
 * status --json --project <name> [--config <path>]` and
 * `project policy show --json --project <name> [--config <path>]`. The first is
 * one managed project's marks and the bounded subset of its newest audit record
 * -- never the record's `argv`, which is the disclosure bound the CLI's own
 * `--json` document keeps (`console/docs/json-contract.md` §6.21) -- and the
 * second is the published policy node as `{project, policy}` and nothing else.
 * **Neither takes an argument**, which is the strongest form of this file's
 * rule, and neither declares a flag a screen could turn off: the two project
 * flags are the installation's, settled by `src/main/spawn.ts`'s `takesProject`
 * (rule 3), so no word of either argv comes from a renderer. Both are read-only
 * -- they are the Project tab's sections, and this work adds no third write
 * (D1471) -- and both are on `consoleReadPaths`, so neither remembers a
 * checkout.
 *
 * The thirteenth **operation** (the row, not an argv) is the startup
 * handshake's answer (`startup.compatibility`, the console's own state, which
 * no CLI command can be asked for). **The two `link.` operations are the ones
 * that are no command at all, and together they are the whole way out of the
 * console.** `openExternal` is handed a URL, which goes through
 * `validateExternalUrl` -- `https:` only, no credentials, no port, host on
 * `ALLOWED_EXTERNAL_HOSTS` -- so the renderer can open a link the console would
 * have opened anyway. `openPullRequest` (K2a, D814/D815) is handed **no URL at
 * all**: three fields of a document, from which the main process builds the
 * link itself, so the first gate is a build rather than a check and a `url`
 * field a job wrote has nowhere to enter. `installation.current` is the dashboard's
 * read of the installation the operator chose, or D13's no-installation state;
 * its handler is `handlersFor`'s **optional** `installation` dep, so a build
 * without a store answers it with that state rather than failing to register.
 * The rail's six (see their comment below) are one read of the CLI's list and
 * five operations on the console's own store and window.
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
      {
        key: 'compute',
        kind: 'flag',
        required: false,
        why: 'the design allow-list admits `--compute` on this command, and only there: it costs a listing of the runs bucket, so the Spend screen’s caps read is the read that asks for it, and only of a CLI at or above `BUDGET_COMPUTE_CLI_FLOOR` (`src/shared/version.ts`). Like `--repo` and `--all` it is optional, and no caller passes it yet.',
      },
    ],
  },
  // spend.report is the one read the Spend screen's controls make, and the
  // wiring (Task 2.4) added no field to it. The range picker sends `since`
  // (it draws four windows; no control sets the end of the range yet, so
  // `until` is accepted here for a later control and no reachable screen state
  // sends it), the breakdown switch sends `by`, and `repo` is unchanged from the
  // design's allow-list: **these four keys and no others**. A fifth key -- a
  // `csv` flag, a path, a command, a URL, a free-form string -- is refused by
  // name here, before a handler sees it, and `test/unit/ipc.test.ts` fails a
  // test per hostile payload rather than passing a review. The screen supplies
  // *values*; `argvFor` below builds the argv, so no renderer ever hands this
  // console a word of a command line. (The screen's half of that wiring is
  // `src/renderer/screens/spend.ts`; the entry point that calls it is a later
  // task's, and this schema is what bounds it when it arrives.)
  {
    name: 'spend.report',
    method: 'runReport',
    command: 'report',
    flags: ['--json'],
    summary: 'spend over time, by day, week, month, year, repo, model, person or mode',
    args: [
      {
        key: 'by',
        kind: 'reportDimension',
        required: false,
        why: '`--by` is the breakdown switch the Spend screen draws; its eight words are closed.',
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
      // The page and its cursor (D818, the console coordinator-v2 plan's C2).
      // They are the two arguments the job list was waiting for: the read is
      // about 248 KB for a day of runs, so the screen asks for a page and walks
      // it with the cursor rather than holding a window it cannot draw.
      //
      // **A page shrinks the bytes and not the seconds.** Since #1545 `--limit`
      // cuts what is *printed*: the command still reads the whole window it
      // prints from, so a day of runs costs its six seconds whether the screen
      // asks for fifty rows or all of them. What the page buys is a document
      // the console can hold and a first row it can draw -- the six seconds are
      // the read's and are paid either way. Both words are emitted as one
      // `=`-form word each (`spawn.ts`), which is what keeps a value from ever
      // reading as a flag.
      //
      // **`--limit`'s ceiling is the console's** (`RUNS_LIST_MAX_LIMIT`) and
      // not the CLI's: the command takes any count it can parse, and a
      // renderer that could ask for more than the cap could ask for the window
      // under another name. `--after` is the id the same read printed, held to
      // the run id's own grammar; an id the window does not hold is the CLI's
      // refusal and not this one's, because only the CLI can know what the
      // window held.
      {
        key: 'limit',
        kind: 'runLimit',
        required: false,
        why: 'the design allow-list admits `--limit=N` on this command: the newest N rows of the window, at most RUNS_LIST_MAX_LIMIT. It needs CLI 0.16.0, the release that carries #1545: in the 0.15 line `--limit` means "read only the newest N", so its totals are the page’s and not the window’s. The caller is the Coordinator screen’s job list (`showCoordinator`, `src/renderer/entry.ts`), which passes it only above `RUNS_LIST_PAGE_CLI_FLOOR`; the queue’s own runs part passes neither word.',
      },
      {
        key: 'after',
        kind: 'runAfter',
        required: false,
        why: 'the design allow-list admits `--after=RUN-ID` on this command: the cursor pages on the row’s own run_id. It needs CLI 0.16.0, the release that carries #1545: a 0.15.x CLI refuses the word outright. The caller is the Coordinator screen’s job list (`showCoordinator`, `src/renderer/entry.ts`), which sends the last row it drew and only above `RUNS_LIST_PAGE_CLI_FLOOR`.',
      },
    ],
  },
  // The Spend screen's by-person view (console L5b). It is the twelfth command
  // the console spawns and it is the one read whose *rows the CLI scopes to the
  // caller*, so its schema is the narrowest thing that command can be asked for
  // from here: **`since` and nothing else**.
  //
  // What is deliberately absent is the point. `--all-users` is a flag of
  // `newRunsStatsCmd` that shows every user's row, and it is not a key of this
  // schema: the command resolves the view itself -- an operator of the
  // installation (the identity named in the local config's `user:`/git, listed
  // in its `terraform.operators`) sees every user's rows, and everyone else sees
  // their own row alone (`internal/cli/runs_stats.go`, `userStatsViewOwn`) --
  // and a console that could pass the flag could widen a private view to
  // everyone's with one click. `--by` is fixed to `user` by the command's own
  // default, `--max-runs` is its own bound (5000, and the document says when it
  // truncated), and `--repo`/`--all` narrow a read this screen draws for the
  // whole project. A payload carrying one of them is refused by name here,
  // before a child exists, and `test/unit/ipc.test.ts` fails a test per hostile
  // payload rather than passing a review.
  //
  // `since` is `runs stats`'s own grammar and not `report`'s (`runSince`, the
  // `parseSince` kind `runs.list` declares): `7d`, `36h`, `90m`, `0`, and never
  // a UTC day or `12w`. The screen translates the range picker's window into it
  // rather than passing the picker's word through -- `src/renderer/screens/
  // spend.ts`'s `personSince` -- so a word this command would refuse is a word
  // that never reaches the schema at all.
  {
    name: 'runs.stats',
    method: 'statsRuns',
    command: 'runs stats',
    flags: ['--json'],
    summary: 'what each person started in a window, and what those runs cost',
    args: [
      {
        key: 'since',
        kind: 'runSince',
        required: false,
        why: 'the design allow-list admits `--since` on this command, in `runs stats`’s own forms (parseSince): 7d, 36h, 90m or 0 — never a date, and never `report`’s `12w`.',
      },
    ],
  },
  // The Project, Coordinator and Spend tabs' totals read (console 0.119.0's
  // jobs-free totals read, `fugaro runs summary --json`, the CLI half of which
  // is B4, #1580). It is the read those tabs paint **first**: run counts per
  // repository and per state, and the spend, from the budget database's day
  // nodes and the spend history's daily documents, **without reading a run
  // object or listing the runs bucket** -- so its child opens no bucket, no
  // compute backend and no provider, and it is the one read here whose cost does
  // not grow with the window.
  //
  // **`since` is the console's own closed set and not the command's grammar.**
  // `runs summary` reads a window in `parseSince`'s forms the way `runs stats`
  // does, but this surface admits exactly `SUMMARY_SINCE_WINDOWS` -- the day a
  // screen opens on and the week the budget's own live window covers, which is
  // also the command's default -- because a window is the one thing about this
  // read that a renderer chooses and the whole history is the one window the
  // read exists to make unnecessary. The word is emitted as the `=` form
  // `--since=<window>` (`src/main/spawn.ts`'s `summarySince` kind): one word, so
  // a window can never be read as a flag.
  //
  // **It is the third operation on the surface that carries spend figures**
  // (`spend.report` and `runs.stats` are the other two), so it is withheld with
  // them in the reduced mode: `spent_micros` and `notional_micros` are the same
  // quantities under another name (`REDUCED_VIEW_OPERATIONS` below).
  {
    name: 'runs.summary',
    method: 'summaryRuns',
    command: 'runs summary',
    flags: ['--json'],
    summary: 'the window’s runs counted by repository and by state, and what they spent',
    args: [
      {
        key: 'since',
        kind: 'summarySince',
        required: false,
        why: 'the design allow-list admits `--since` on this command, in the console’s own closed set of windows (SUMMARY_SINCE_WINDOWS): 7d, which is the command’s default and the window the budget’s live counters cover, or 1d for a screen that opens on today. The command itself needs CLI 0.16.0, the release that carries #1580 — `RUNS_SUMMARY_CLI_FLOOR`, `src/shared/version.ts` — because in the 0.15 line it does not exist at all. No caller passes this argument yet: the screens that will are drawn by a later task, and each will pass one of these two words.',
      },
    ],
  },
  // The Plan screen's read (plan GV2.1). It is the thirteenth CLI command the
  // console spawns, and the one read whose subject is the **checkout** rather
  // than the project: `plan status` walks the graph artifacts under the
  // checkout's own `docs/` and joins them to the runs of that checkout's
  // repository, so the child has to run *in* the checkout.
  //
  // Its flags are the command's own, read off `newPlanStatusCmd` the way every
  // other row's are, and §5.1 rule 6's row permits exactly `--all` among them.
  //
  // **The directory is therefore not a key of this schema, and never a word of
  // its argv.** The main process sets the child's working directory from the
  // installation's recorded checkout (`createBoundSource`, `src/source/cli.ts`),
  // and an installation that has none is refused before a child exists: a
  // renderer-supplied path would be a way for a document to name a directory the
  // operator never chose (the plan's constraint C1, "the renderer never sends a
  // path"). `argvFor` builds every word of the argv from the spawn table, and no
  // row can emit `--dir` for this command.
  //
  // **`all` and nothing else.** `--all` is the screen's own control: it lists
  // the closed tasks (merged and cancelled) that the read otherwise counts per
  // release. It is a boolean and never a string, so a payload cannot spell a
  // flag with it, and the cloud flags (`--project`, `--config`) are the
  // installation's, exactly as `runs ls` carries them -- never `--gcp-project`
  // or `--region`, which are no part of this surface. There is no positional
  // argument: `newPlanStatusCmd` is cobra's `NoArgs`.
  {
    name: 'plan.status',
    method: 'planStatus',
    command: 'plan status',
    flags: ['--json'],
    summary: "every plan's tasks under the checkout's docs/, joined to the runs working on them",
    args: [
      {
        key: 'all',
        kind: 'flag',
        required: false,
        why: '`newPlanStatusCmd` has this flag, and it is the screen’s own control: it lists the tasks the read otherwise counts per release, which is narrower than the count the screen already has. It is the one argument this operation declares, and the design’s §5.1 table has not carried a row for the command yet.',
      },
    ],
  },
  // The Graph screen's second read (the graph plan's CG9a, `console/docs/
  // json-contract.md` §6.22): `fugaro graph --json`, the project's dependency
  // graph, which is the read that needs **no checkout** -- its scope today is
  // the coordinator's stored view, read from the project's own database through
  // the caller's own credential -- so an installation that records no directory
  // still draws it. It sits beside `plan.status` because it is the same screen.
  //
  // **Its one argument is the scope, and it is the only argument on this
  // surface held to a grammar *and* to a membership.** `GRAPH_SCOPE_RE` refuses
  // every spelling no scope could have (`-x`, `all --source local:o1`,
  // `orchestrator:../..`, a 41-character id) before a child exists, and
  // `createBoundSource` refuses a scope the previous document of this read did
  // not list (F21) -- a value a renderer invented, or one that names a scope
  // the CLI's own sources array never mentioned, is a refusal and not a spawn.
  // The read is also the one place the console may name a *paid* command
  // (`ee/graph`, D1362), which is why it takes no other word: the `--watch`
  // stream that holds a child open is a different argv and its own row
  // (`graph.watch`, below, which CG9b landed).
  {
    name: 'graph.read',
    method: 'graphRead',
    command: 'graph',
    flags: ['--json'],
    summary: 'the dependency graph as one versioned document, with no checkout needed',
    args: [
      {
        key: 'source',
        kind: 'graphSource',
        required: false,
        why: '`newGraphCmd` has this flag and its set is closed: `all` (the command’s own default, the union of the scopes that exist), `coordinator` (the stored view), and `orchestrator:<id>` / `local:<id>` for the scopes a later release adds -- both of which today answer "no orchestrator is installed in this build", which is a refusal and not an empty graph. The pattern is the CLI’s own (`graph.ParseScope`), so a value this surface admits is a value that command reads. It is optional because the command has a default of its own (`all`), so a caller that names no scope is answered rather than refused.',
      },
    ],
  },
  // The Graph's own live feed (the graph plan's CG9b; `console/docs/
  // json-contract.md` §6.22 and §10, the design's H4 to H6): `fugaro graph
  // --watch --json`, the same versioned document the read above prints, printed
  // again as a whole on every change.
  //
  // **It is the surface's first long-lived child**, and three things follow from
  // that which no read above needed. It is held **only while the Graph tab is on
  // screen** (`visible` below, H6), so the console does not pay for a
  // subscription nobody is looking at. It is counted with the live feeds against
  // `MAX_LIVE_FEEDS` (`src/source/cli.ts`), because a long-lived child is what
  // that ceiling exists for. And `--watch` is a word of its **command path**
  // rather than a member of its `flags`: the surface fixes nothing here that a
  // screen can turn off, and the never table's `--watch` row carries this one
  // operation as its `except` -- the graph has no `watch` word for the console to
  // use instead, which is what makes its streaming form a row of its own rather
  // than a second name for another read.
  {
    name: 'graph.watch',
    method: 'graphWatch',
    command: 'graph --watch',
    flags: ['--json'],
    summary: 'the dependency graph, re-printed as one whole document on every change',
    args: [
      {
        key: 'source',
        kind: 'graphSource',
        required: false,
        why: 'the scope the stream is about, on `graph.read`’s own terms: `all` (the command’s default, the union of the scopes that exist), `coordinator`, or `orchestrator:<id>` / `local:<id>` for the scopes a later release adds. It is the same closed grammar the read above is held to (`GRAPH_SCOPE_RE`) and, at the source, the same membership rule: a scope no previous document of this installation listed is refused before a child exists (F21), so a renderer cannot hold a stream open on a scope that exists nowhere. It is optional because the command has a default of its own, exactly as the read’s is.',
      },
      {
        key: 'visible',
        kind: 'flag',
        required: false,
        why: '**the one argument on this surface that reaches no word of any argv** (`src/main/spawn.ts`’s row declares it with `flag: null`, the way the launch’s task text is declared): it decides whether the child runs at all. A caller sends `visible: false` when the Graph tab is left or the window is hidden, and the handler stops the feed instead of polling it -- so the console holds a `graph --watch` child only while somebody is looking at the graph (H6). It is `false` and never absent because absent means "poll it": the two states are the whole of what a screen has to say about a feed it started.',
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
    name: 'run.launch',
    method: 'launchRun',
    // The command path carries the row's two fixed words as well as its name,
    // because rule 11's argv has `--task-file -` in it -- the literal word `-`
    // is what sends the task down the child's stdin instead of onto the command
    // line -- and those two words are not flags a *screen* controls, which is
    // all `flags` holds (and all `ALLOWED_OPERATION_FLAGS` admits). Rule 11's
    // argv is therefore this pair as one string: `run --task-file - --json`.
    command: 'run --task-file -',
    flags: ['--json'],
    summary: 'the console’s one write: one launch of `fugaro run` for a named installation',
    // The five fields, and nothing else: no `--pr`, no `--retry`, no `--recipe`,
    // no mode, no path, no ref, no workflow, no budget escape. Every one of them
    // is on `NEVER_REACHABLE`'s argv rows below, because a closed schema is not
    // enough on its own -- `argvFor` builds the vector from this schema, so a
    // field that is not here cannot reach a child.
    payloadWidth: LAUNCH_PAYLOAD_WIDTH,
    args: [
      {
        key: 'installation',
        kind: 'installationName',
        required: true,
        why: 'which installation launches — a name the main process’s own records carry, checked against them before any window is asked.',
      },
      {
        key: 'repository',
        kind: 'repository',
        required: true,
        why: 'the checkout to run in — a `checkouts[]` identity the installation’s own `config list --json` read carried, so the console never invents a repository.',
      },
      {
        key: 'task',
        kind: 'launchTask',
        required: true,
        why: 'the task itself, written to the child’s stdin as `--task-file -` — it is in no argv word and in no log line.',
      },
      {
        key: 'title',
        kind: 'launchTitle',
        required: false,
        why: '`--title=<T>`, one word, `=` form only, and only when the operator filled it in.',
      },
      {
        key: 'planTask',
        kind: 'launchPlanTask',
        required: false,
        why: '`--plan-task=<P>`, one word, `=` form only, and only when the operator filled it in.',
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
  // The Project tab's read-only models panel (console 0.119.0; the owner's
  // ruling of 2026-11-02, and SECURITY-SENSITIVE because a new row on this
  // surface always is). `fugaro models ls --json`: the selected project's
  // routes, the models a run can name today and each recorded repository's
  // pins. The panel is a later task; this is the main process's half.
  //
  // **It is the group's only word the console may drive.** `fugaro models` has
  // five subcommands -- `ls`, `search`, `add`, `rm` and `use`
  // (`internal/cli/models.go:33`) -- and the other four are on the never table
  // below as words of this command: `add`, `rm` and `use` edit the project's or
  // a repository's own config file, and `search` takes a positional `<text>`
  // a renderer would fill, which is the shape this table exists to keep off the
  // surface. So the console has **no write path into `models`**, and the one
  // read is the row above.
  //
  // **No argument at all**, the strongest form of this rule's closed schema:
  // the command is cobra's `NoArgs`, `--json` is a fixed word of the spawn row
  // and the cloud group's `--project`/`--config` are the installation's -- so
  // there is no field a renderer could fill. The read is **offline**: the CLI's
  // own comment says it reaches no bucket, no database and no host, so this
  // child mints nothing of the host's, opens no provider and spends nothing,
  // and its document is what the local files already say.
  //
  // **It carries no spend and no cap**, so it is deliberately not in
  // `REDUCED_VIEW_OPERATIONS` below: the model *prices* it draws are the
  // catalog's own per-million-token figures rather than what this project
  // spent, and the reduced mode withholds the screens that read money the
  // operator's runs spent. `spent_micros`, `notional_micros` and a cap are
  // words this document does not have.
  //
  // It needs CLI 0.16.0 (`MODELS_LIST_CLI_FLOOR`, `src/shared/version.ts`):
  // `models ls` landed after v0.15.0 (#1553), so a 0.15.x CLI has no such verb
  // and answers `unknown command "ls"`.
  {
    name: 'models.list',
    method: 'listModels',
    command: 'models ls',
    flags: ['--json'],
    summary: 'the project’s routes, the models a run can name, and each repository’s pins',
    args: [],
  },
  // The settings read (the settings-edit plan's C1, D1102): the project's
  // settings, each with the value a run gets, the layer that decided it, the
  // built-in default and its state -- and the three writability facts
  // (`writable`, `owner`, `target_layer`) the Project tab's settings section
  // draws its pencils from. **The console keeps no list of its own**: D1102 is
  // the ruling that the CLI's read carries those three, so there is no fourth
  // copy of the generic path's allowlist here to drift the day a key is added
  // to the registry.
  //
  // **No argument at all.** The command takes positional keys or globs and
  // seven flags of its own, and no renderer chooses any of them -- the row's
  // `args` is empty, its two words are fixed, and `--project`/`--config` are
  // the installation's (`src/main/spawn.ts`). So no word of this argv is
  // caller-influenced and the read is not a way to steer a child anywhere.
  //
  // **A sensitive row is never drawn** (the owner's ruling of 2026-11-01,
  // D1111). The document carries no `value` member for one at all
  // (`settingsJSON`), and the screen drops the row whole: no key, no value, no
  // pencil. That is the screen's rule and not this table's; what belongs here
  // is that the row's *edit* is `settings.edit`, the row below (C3), and this
  // operation reads.
  {
    name: 'settings.read',
    method: 'readSettings',
    command: 'settings',
    flags: ['--json'],
    summary: 'the project’s settings, what decides each one, and which of them the console may edit',
    args: [],
  },
  // **The settings write, and the console's second write** (the settings-edit
  // plan's C3, D1103): one key of one document, set or unset, at the layer the
  // read's own row named, after the operator has read the change in the
  // confirmation window (rule 12).
  //
  // **`flags` is empty, and that is the one place this row differs from every
  // read beside it.** The dry run's argv carries `--dry-run --json` and a
  // confirmed write's carries `--yes` -- which is not a set of flags this
  // operation *fixes*, so it is the phase the main process decides
  // (`SETTINGS_EDIT_PHASE_WORDS`, `src/main/spawn.ts`) rather than a column
  // here. A screen chooses none of them: not `--dry-run`, not `--yes`, not even
  // `--json`.
  //
  // **The schema is three keys and no more**, and the layer is not one of them:
  // it is the read's own `target_layer`, held in this process's copy of the
  // document and emitted from `argvFor`'s own `settingsEdit` (D1105, D1114), so
  // no payload of a renderer's can name one. `op` and `key` are the two the
  // screen knows -- it drew the pencil for that row -- and `value` is the
  // operator's own text: one argv element, so it is in the process table on the
  // host route and in the spawn log when that log is on (rule 10), which is the
  // trade rule 6's row states. Nothing keeps a credential out of it but D1111's
  // rule: a sensitive row is dropped whole by the read, so it has no pencil and
  // reaches this operation not at all.
  //
  // **What this operation does not have**: no `--repo` (the row whose
  // `target_layer` is `repo` -- the layer the read names for the keys a
  // repository's own config file carries -- is edited where the child's own
  // working directory says, because that is what the CLI resolves `--layer repo`
  // from with no `--repo`: the one at the checkout `git rev-parse
  // --show-toplevel` finds for `.`, or its own refusal when that
  // directory is not one (`internal/cli/settings_set.go`). The child's directory
  // is the installation's recorded checkout when it has one and
  // `privateWorkingDirectory` otherwise, so this surface names no repository and
  // never checks that the directory is one; whether such a row should be refused
  // instead is the settings write's own open question, not a rule here), no
  // `--add`/`--remove` (a list edit is a form no control can
  // choose: the read carries no member that says a key is list-shaped), and no
  // third subcommand: `settings get` is a read of one key this screen does not
  // need and `set`/`unset` are this row's own two, so no other word of the group
  // is reachable. **Unlike `models`, none of them is on `NEVER_REACHABLE`**: the
  // group's other words are off this surface rather than refused by the table,
  // and no settings row moved when this write landed (D1114, the design's
  // rule 6).
  {
    name: 'settings.edit',
    method: 'editSetting',
    command: 'settings',
    flags: [],
    summary: 'the second write: one setting set or unset, at the layer the read named, after the operator confirms it',
    args: [
      {
        key: 'op',
        kind: 'settingOp',
        required: true,
        why: 'which of the command’s two subcommands this is. A closed set of two, because the word decides what the child does, and the third subcommand (`get`) is a read this screen does not need.',
      },
      {
        key: 'key',
        kind: 'settingKey',
        required: true,
        why: 'the setting to write — a key of the document the read answered, so the console invents none. Its membership in the generic path’s allow-list is the CLI’s (D1102): a key the generic path does not own is refused by the child’s own sentence, which the console shows.',
      },
      {
        key: 'value',
        kind: 'settingValue',
        required: false,
        why: 'the operator’s new value, passed behind a `--` terminator as one argv element (D1104): a value spelled `--layer` is the case the terminator exists for. Absent for an `unset`, which is what clearing the screen’s box sends.',
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
  // The startup handshake's other half (Task 3.3): the *answer* the console
  // reached before it built any window, so the renderer can gate on it instead
  // of drawing screens a refused or reduced console may not draw. It is the one
  // read that is not a read of the CLI -- it is the console's own state, which
  // is why it has no command and no flags -- and it takes no argument at all, so
  // a renderer cannot ask about a compatibility other than the one this console
  // is running in. `readStoredCompatibility` below is the closed-schema check
  // its handler runs, and it is what keeps a *stored* value from being answered
  // verbatim if it is not one of the three modes.
  {
    name: 'startup.compatibility',
    method: 'getCompatibility',
    command: null,
    flags: [],
    summary: 'the handshake’s answer: this console’s mode, and the views it withholds',
    args: [],
  },
  // The fifteenth operation (Task 3.4b) is the second read of the console's own
  // state, and it is the one the header needs: **which installation is active**.
  // It is not a read of the CLI -- the store holds it -- and it takes no argument
  // at all, so a renderer cannot ask about an installation other than the one
  // this console is running. Its answer names a project and a binary path and
  // **never an id a renderer could pass back**: choosing is the picker's
  // operation (`fugaro:installations/choose`), on the picker's own channel, which
  // a frame on this document cannot reach.
  {
    name: 'installation.current',
    method: 'getInstallation',
    command: null,
    flags: [],
    summary: 'the installation the operator chose, or D13’s no-installation state',
    args: [],
  },
  // The rail's own surface (owner's design ruling of 2026-10-25). **The
  // console never shows a picker screen**: the dashboard's rail lists every
  // installation the CLI found, a click on a row switches the console to it,
  // and a row that cannot be read opens its issue in the main area. That is a
  // read and four writes of the console's own store plus one window, and all
  // six of them are the operations `src/main/installations.ts` already
  // declared for the picker window -- moved onto this table with the rail, so
  // that the dashboard's document can reach them.
  //
  // `installation.list` is the one of the six that spawns: `fugaro config
  // list --json`, the read §5.1 rule 6's allow-list already carries as its
  // thirteenth argv (`internal/cli/config_list.go`; `console/docs/json-
  // contract.md` §6.8). It is a read of the machine and not of a project, so
  // its spawn row takes no `--project` and no `--dir`.
  //
  // The other five spawn nothing at all: they read and write
  // `<userData>/installations.json` (`installation.choose`, `.remove`), run
  // the console's own `doctor` against the CLI's list of checkouts
  // (`.checkout`), hand a directory this process computed to the file manager
  // (`.reveal`), or open the picker window the rail's `+` door is
  // (`.open`). None of them takes a path a renderer chose: `.checkout` takes a
  // directory **the rail offered** -- one the CLI listed in the document it last
  // read, or the console's own recorded checkout, never a temporary or
  // tool-owned path -- and `.reveal` takes a project name and computes the path
  // itself.
  {
    name: 'installation.list',
    method: 'listInstallations',
    command: 'config list',
    flags: ['--json'],
    summary: 'every installation this machine has, with the rail’s own mark for each',
    args: [],
  },
  {
    name: 'installation.choose',
    method: 'chooseInstallation',
    command: null,
    flags: [],
    summary: 'switch the console to one installation the rail lists',
    args: [
      {
        key: 'project',
        kind: 'installationName',
        required: true,
        why: 'the handle the CLI printed for the row the operator clicked; the store is keyed by it, and no id crosses the boundary.',
      },
    ],
  },
  {
    name: 'installation.checkout',
    method: 'setInstallationCheckout',
    command: null,
    flags: [],
    summary: 'pair one installation with a checkout directory, and check it',
    args: [
      {
        key: 'project',
        kind: 'installationName',
        required: true,
        why: 'the installation the checkout belongs to, by the name the CLI printed.',
      },
      {
        key: 'checkout',
        kind: 'installationCheckout',
        required: false,
        why: 'a directory out of that installation’s own `checkouts` list (what the CLI listed, plus the console’s own recorded checkout, minus temporary and tool-owned paths), which the main process checks for membership. Absent means "no checkout", which is a state an installation may be in (design section 3.4 rule 5).',
      },
    ],
  },
  {
    name: 'installation.remove',
    method: 'removeInstallation',
    command: null,
    flags: [],
    summary: 'drop this console’s own record of one installation',
    args: [
      {
        key: 'project',
        kind: 'installationName',
        required: true,
        why: 'the row the operator asked to forget. It removes the console’s record and nothing on disk.',
      },
    ],
  },
  {
    name: 'installation.reveal',
    method: 'revealInstallation',
    command: null,
    flags: [],
    summary: 'open one installation’s directory in the file manager',
    args: [
      {
        key: 'project',
        kind: 'installationName',
        required: true,
        why: 'a project name and **not a path**: the directory the file manager is handed is computed in this process from its own record (`explorerPathFor`), so a renderer cannot name a directory for it to open.',
      },
    ],
  },
  {
    name: 'installation.open',
    method: 'openInstallations',
    command: null,
    flags: [],
    summary: 'the rail’s + door: the window that adds an installation by checkout path',
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
  // The clickable pull request number (Coordinator v2 K2a, D814 and D815).
  //
  // **This operation takes no URL, and that is its whole design.** A pull
  // request document carries a `url` field, and no field of any document ever
  // reaches this operation: it takes the three fields the URL is *built* from
  // -- the repository's provider, its slug and the number -- and the main
  // process builds the link itself with `pullRequestUrl` (`src/main/window.ts`),
  // which is the first gate rule 2 asks for. What is built is then handed to
  // `openExternalValidated`, the console's one call to `shell.openExternal` and
  // its second gate, so the host allow-list is applied to a URL this process
  // spelled rather than to a string a job wrote. A `null` build -- an unknown
  // provider, a slug that is not `owner/name`, a number that is not a positive
  // integer -- is a refusal that opens nothing and shows the number as text.
  //
  // It is a second operation rather than a second argument kind on
  // `link.openExternal` because the two are two different things to say: that
  // operation hands over a URL the console was given, and this one takes fields
  // and has no way to express a URL at all. The three keys are closed like
  // every other schema's, and the two providers are the builder's own set
  // rather than a list spelled here.
  {
    name: 'link.openPullRequest',
    method: 'openPullRequest',
    command: null,
    flags: [],
    summary: 'a pull request number as a link: the URL is built here, from three fields, and never read',
    args: [
      {
        key: 'provider',
        kind: 'gitProvider',
        required: true,
        why: 'the host the link is built on, one of the two the builder knows. It is a word from the coordinator’s stored view (C1’s additive `provider` field), never an inference from a URL a job wrote (D814).',
      },
      {
        key: 'repo',
        kind: 'repository',
        required: true,
        why: 'the repository the pull request belongs to, as the CLI’s own canonical `owner/name`. The narrower pattern the link may be built from — exactly two segments, no `..`, no third `/` — is `pullRequestUrl`’s, and a value that only passes this kind is still refused there.',
      },
      {
        key: 'number',
        kind: 'pullRequest',
        required: true,
        why: 'the pull request number, a positive integer. It is a segment of the path the main process spells, so it can never carry one.',
      },
    ],
  },
  // The project view (Bitbucket credentials T1.2, D364). `fugaro coordinator
  // status --from-store --all --json` is the one read that answers for **every
  // repository of the installation at once**, and it is the console's only read
  // that needs no credential of the host's: the coordinator's stored view is the
  // caller's own database read (`roles/firebasedatabase.viewer`), so the console
  // asks it with no App key, no Bitbucket credential and no host read. A
  // repository the coordinator has never reconciled answers the runs half with
  // `"stored": false` rather than refusing, which is what makes this read safe
  // on an installation whose coordinator was never turned on.
  //
  // **`--from-store` and `--all` are fixed, and they are not arguments.** `--all`
  // is the wrapper the document's `repos` array needs: without it the read
  // answers one repository's view, and refuses when the local config lists none
  // or several -- neither of which is the question a project view asks. Both are
  // words of the argv rather than an argument schema because the screen has no
  // control that would turn either off, which is the same reason
  // `installation.list` fixes `--json`.
  //
  // **`--repo` is deliberately no part of this operation.** The read is about the
  // project's whole repository set, and a renderer that could name one repository
  // could ask a question this screen does not draw. `--since` is absent for the
  // reason the screen claims no age: the stored view is a document that has
  // already been written, so a window would filter a figure nothing recomputes.
  {
    name: 'coordinator.status',
    method: 'coordinatorStatus',
    command: 'coordinator status',
    flags: ['--from-store', '--all', '--json'],
    summary: 'every repository of the installation, from the coordinator’s stored view',
    args: [],
  },
  // The pull request read (coordinator v2 C3, D816): `fugaro coordinator show
  // --repo <owner/name> --pr <n> --json`, one pull request as the host has it
  // **with its description**, which the stored view above drops by design (a
  // body is unbounded text written by whoever opened the pull request, and one
  // per record would ride in the document every reader of the view fetches).
  //
  // **It is the second operation here whose child mints a credential of the
  // host's.** The first is `run.launch` above, and the two are not alike: a
  // launched run holds the App's private key and mints installation tokens that
  // carry `contents: write` and `pull_requests: write`, because it pushes and
  // opens the pull request (`internal/gitprov/github/apptoken.go`), while the set
  // this operation's child mints is the read-only one: pull requests read and
  // metadata read, no `write` of any kind
  // (`github.CoordinatorTokenPermissions`), minted for the one repository and
  // revoked when the command is done. That is why the two arguments below are
  // the whole of what a renderer can influence about this argv: there is no key
  // for a token, a host, a path, an endpoint or a flag, and the row in
  // `src/main/spawn.ts` declares the same two keys and no other word. Under
  // `FUGARO_CREDENTIALS_SERVICE_URL` the command refuses -- the service holds
  // the App's key and this read is not a run -- which the screen draws as its
  // own refusal.
  {
    name: 'coordinator.show',
    method: 'coordinatorShow',
    command: 'coordinator show',
    flags: ['--json'],
    summary: 'one pull request as the host has it, the description included',
    args: [
      {
        key: 'repo',
        kind: 'repository',
        required: true,
        why: 'the repository the pull request is in, as the CLI’s own canonical `owner/name`. The read resolves the repository’s provider from it — the local config’s entry, else the checkout’s own config file, the same resolution the stored view above makes — and mints the installation token for it, so it is the one word that decides whose credential is used. It is a repository, never a host, a URL or a path.',
      },
      {
        key: 'pr',
        kind: 'pullRequest',
        required: true,
        why: 'the pull request number, a positive integer, which the CLI’s `IntVar --pr` reads as digits. A pull request the host does not have is the host’s own failure and reaches the screen as a refusal, never as an empty document.',
      },
    ],
  },
  // The declarations read (coordinator v2 C4, D827): `fugaro coordinator intents
  // --all --json`, what the fleet's writers have stored, newest first.
  //
  // **It has no argument at all**, which is the strongest form of this file's
  // rule: the whole argv is the row's own words (`src/main/spawn.ts`), so there
  // is no field a renderer could fill and no way to steer the child anywhere.
  // `--all` is fixed rather than a control, the way `coordinator status`'s two
  // are: the screen draws every repository the local config lists, and no
  // control turns that off.
  //
  // Like the stored view beside it, this read asks nothing of the host: the
  // declarations are the caller's own database node (`roles/firebasedatabase.
  // viewer`), so no App key is read, no installation token is minted, no
  // provider is opened and no host is reached. `declared_by` is a *declaration*
  // -- the string a writer put in its own record -- and never an identity the
  // host or this console establishes (`src/shared/schema.ts`).
  {
    name: 'coordinator.intents',
    method: 'coordinatorIntents',
    command: 'coordinator intents',
    flags: ['--all', '--json'],
    summary: 'what the fleet’s writers have declared, newest first',
    args: [],
  },
  // The Project tab's managed state and its audit trail (the managed screens'
  // plan, CM4; D1472 to D1477, and rule 6 carries its row). It is the console's
  // first read of a **managed** project as such: the two marks and whether they
  // agree, the read's own reason when they do not, the version and the
  // generation, and the bounded subset of the newest audit record -- D1476's
  // list, and **never `argv`**. That bound is the command's, not this file's
  // (`fugaro managed status --json` prints the subset and nothing more, contract
  // §6.21), which is the point: a disclosure bound a screen could widen is not a
  // bound. The screen that draws it also draws no "revoked" state, because the
  // CLI persists none (D1475).
  //
  // **It takes no argument at all.** `fugaro managed status` has no positional
  // word, and the two project flags are main-process state rather than arguments
  // (`src/main/spawn.ts`'s `takesProject`, rule 3), so `args` here is empty on
  // purpose and not for want of a screen: a caller that could name a project, a
  // generation or a day would be naming where this console reads somebody else's
  // project.
  {
    name: 'managed.status',
    method: 'managedStatus',
    command: 'managed status',
    flags: ['--json'],
    summary: 'one project’s managed marks, and the newest audit record’s bounded subset',
    args: [],
  },
  // The published policy node (the same plan's CM4; D1480 and D1481, and rule 6
  // carries its row). The Project tab's policy panel has had no source at all
  // (F4); this is the read that gives it one, and the panel stops saying it
  // cannot read when the same plan's CM10 flips it.
  //
  // **It is `project policy show --json` and not `project show --json`**, and
  // the difference is the whole reason for the row: the former prints
  // `{project, policy}` and nothing else (F18), while the latter also carries
  // the **installation's** caps -- dollars that belong to the Spend screen and
  // to the tab's own `caps` panel, and that a decoder here would have to drop
  // (D1481). What the node does carry is the *project's* own `budget` block, and
  // **no screen draws it**: those figures are configured ceilings, drawn where
  // the `caps` panel draws one and nowhere else, so the policy notes read the
  // node, drop the block and draw no figure of it (the managed screens'
  // reconciliation, §6 rule 3 -- which also retires this comment's earlier claim
  // that the tab draws the block "as the policy it is"; no screen of that work
  // does, review of 2026-11-07). **The read is not moneyless all the same**, and
  // that is what the reduced mode withholds: the ceiling travels in the
  // **document**, so this read is on the tab's own view list
  // (`REDUCED_VIEW_OPERATIONS`'s `caps`, below) with `budget.show` and
  // `settings.read` -- the same ruling that puts the settings read there for its
  // `budget.*` rows. The view entry is untouched: the tab's view is `caps`
  // already (F9), which is what D1487 decided, and this is the operation's half.
  //
  // Like its sibling above it takes no argument at all: no positional word, no
  // `--gcp-project`, no `--region`. The `project` group's other words --
  // `policy publish`, `repos add`, `repos rm` and `sync` -- are each on this
  // file's never table, so this is the group's whole reachable argv.
  {
    name: 'policy.show',
    method: 'policyShow',
    command: 'project policy show',
    flags: ['--json'],
    summary: 'the published policy node the project’s runs read, and nothing else',
    args: [],
  },
  // **The licence section's two operations** (subscriptions plan S2.7; the
  // design is `docs/licensing/licence-key.md`, "Storage and hand-off (F41)"),
  // and they are two of the eleven rows on this table that **spawn nothing at
  // all** -- the first two of those that keep a **credential**: the console
  // keeps the key in its own store (`src/main/licence-store.ts`), so a paste is
  // a verify and a write of a file this process owns rather than a child's argv.
  // `command: null` is that fact, and it is also why neither row carries a flag
  // -- there is no argv for a flag to be part of -- and why neither is on
  // `SPAWN_COMMANDS` (`src/main/spawn.ts`).
  //
  // **Neither carries the key back to the renderer, and that is rule 8's own
  // reason.** `licence.read` answers what the *verifier* sees: the claims and
  // the state (`valid`, `grace`, `expired`), or the sentence that refuses the
  // key. The one door the key text leaves this process by is the child's
  // environment (`licenceKeyFor`, `src/main/index.ts`), which is S2.1's
  // hand-off and not a document's to hold: a reply carrying the key would be a
  // key in a renderer, and a screen that held one would draw it.
  //
  // **Neither names an installation.** The active one is main-process state,
  // for `installation.current`'s reason exactly: the store answers for the
  // installation this console is driving, so a renderer can neither paste a key
  // for nor read the key of an installation other than that one.
  //
  // **The screen that draws them is never gated**
  // (`docs/design/lapse-read-only.md`): a lapsed installation is the one state
  // in which the operator has to be able to reach the box that takes a renewal,
  // so these two are not on a reduced view and answer in every mode.
  {
    name: 'licence.read',
    method: 'readLicence',
    command: null,
    flags: [],
    summary: 'the active installation’s licence key as the verifier sees it: its claims and state, or the refusal',
    args: [],
  },
  {
    name: 'licence.save',
    method: 'saveLicence',
    command: null,
    flags: [],
    summary: 'the key the operator pasted, verified before it is kept: a key that does not verify is refused and changes nothing',
    args: [
      {
        key: 'key',
        kind: 'licenceKey',
        required: true,
        why: 'the pasted key — the one argument on this surface whose value is a credential, and the only one the console never answers back. It is bounded before anything decodes it and passed nowhere but the verifier; what it can reach is the child’s environment and never an argv.',
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
 * its own argv; `--from-store` is `coordinator status`'s, and `--all` is the
 * project-wide wrapper that read fixes together with it and that
 * `coordinator intents` fixes on its own -- in both cases no screen control
 * turns it off. Anything else an operation might want
 * to fix is on `NEVER_REACHABLE`'s flag rows. */
export const ALLOWED_OPERATION_FLAGS: readonly string[] = Object.freeze([
  '--json',
  '--url',
  '--from-store',
  '--all',
])

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

/** NeverReachableShape says what kind of thing an entry forbids.
 *
 * `command` is a whole command path, `flag` is one flag on any command, and
 * **`argv`** is one flag on one command -- the shape rule 11's fence needs, and
 * the reason it needs it: `run` is no longer forbidden as a *command*, because
 * exactly one of its argument vectors is the console's one write. A row that
 * named the bare command would refuse that one too, and a row that named only
 * the flag would refuse `--retry` on every command rather than on the one whose
 * sibling is the launch. */
export type NeverReachableShape = 'command' | 'flag' | 'argv'

/** NeverReachable is one row of the never table. */
export interface NeverReachable {
  /** `shape` distinguishes a whole command from a flag on an allowed one. */
  readonly shape: NeverReachableShape
  /** `value` is the command path, the flag, or -- for an `argv` row -- the flag
   * that one command may not carry. */
  readonly value: string
  /** `why` is why the console must never reach it. */
  readonly why: string
  /** `where` is the symbol or document it was read from. */
  readonly where: string
  /** `command` is the command an `argv` row is about, and is absent from the
   * other two shapes: a `command` row *is* the command, and a `flag` row is
   * about every command at once. */
  readonly command?: string
  /**
   * `except` is the operations a `flag` row does **not** apply to, by operation
   * name, and it is how one argv on this surface may carry a word the table
   * refuses everywhere else. It exists for a word that is the *only* way into a
   * command's stream rather than a second name for a read the console already
   * has: `--watch` on `runs ls` is `watch` under another name and stays refused
   * on every command, but `graph --watch` is `graph`'s own streaming form --
   * there is no `fugaro graph watch` word to use instead -- so `graph.watch` is
   * the one name here.
   *
   * **It is keyed by operation and never by command.** A name is the whole of
   * what this surface has: a caller that does not say which operation it is
   * asking about gets the row's refusal like any other (`isNeverReachable`
   * below), so `assertReachableSurface` and the per-row tests still refuse the
   * bare word, and a second argv on `graph` would be a second surface row rather
   * than a second member of this list. The check that an exception is *used* is
   * `src/main/spawn.ts`'s: a row whose argv carries the word is refused there
   * unless the operation is on this list, and this list admits nothing the spawn
   * table does not build.
   */
  readonly except?: readonly string[]
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
  // `run` itself is not on this table, and that is the whole of V2.5.1a's fence
  // move: `run` is the console's one write (D22), and it is allowed **as exactly
  // one argument vector** -- the `run.launch` operation above, whose words are
  // fixed by design section 3.4 rule 11 and whose schema has five fields and no
  // flag among them. What stays refused is every *sibling* of that vector, and
  // these rows are argv-shaped rather than command-shaped so that they refuse
  // the siblings without refusing the one: `value` is the flag, `command` is the
  // command it may not ride on, and `run.launch`'s own flags (`--json`) are on
  // none of them.
  {
    shape: 'argv',
    command: 'run',
    value: '--pr',
    why: 'a follow-up launch on an existing pull request’s branch: no decision in this tree authorizes it, and the owner ruled on 2026-10-27 that it stays off the launch row until he revisits it (rule 11’s open points)',
    where: 'internal/cli/run.go:265',
  },
  {
    shape: 'argv',
    command: 'run',
    value: '--retry',
    why: 'relaunches the stored task of a run that never started, and the owner has not ruled it in: D34’s “not yet” (V2.5.2, unscheduled)',
    where: 'internal/cli/run.go:263',
  },
  {
    shape: 'argv',
    command: 'run',
    value: '--attempts',
    why: 'launches two to four independent runs of one task as a best-of-N batch, so it multiplies one spend by up to four',
    where: 'internal/cli/run.go:271',
  },
  {
    shape: 'argv',
    command: 'run',
    value: '--batch',
    why: 'the label those best-of-N runs are grouped under in `fugaro runs ls`; it means nothing without `--attempts`, and a group label the app invented would be the app naming rows in the operator’s run list',
    where: 'internal/cli/run.go:259',
  },
  {
    shape: 'argv',
    command: 'run',
    value: '--no-budget-check',
    why: 'skips the launch pre-check of the budget -- kill switches, missing caps, no headroom -- which is the check D22’s answer rests on',
    where: 'internal/cli/run.go:274',
  },
  {
    shape: 'argv',
    command: 'run',
    value: '--token',
    why: 'chooses the Claude credential a run uses; a credential is not screen state',
    where: 'internal/cli/run.go:267',
  },
  {
    shape: 'argv',
    command: 'run',
    value: '--recipe',
    why: 'picks the recipe (task loop) a run is held to, a reviewed configuration fact or project-layer one rather than screen state',
    where: 'internal/cli/run.go:266',
  },
  {
    shape: 'argv',
    command: 'run',
    value: '--mode',
    why: 'the rigor mode, another reviewed configuration fact a renderer must not name',
    where: 'internal/cli/run.go:269',
  },
  {
    shape: 'argv',
    command: 'run',
    value: '--path',
    why: 'declares a path the run will touch, matched against the rigor.areas rules: a path is what the surface must never carry',
    where: 'internal/cli/run.go:270',
  },
  {
    shape: 'argv',
    command: 'run',
    value: '--ref',
    why: 'the ref is the CLI’s own resolution through every config layer, and the console requires a *recorded* base branch instead of naming one; a ref from the renderer is a spend cut from a branch nobody wrote down',
    where: 'internal/cli/run.go:256',
  },
  {
    shape: 'argv',
    command: 'run',
    value: '--workflow',
    why: 'chooses which workflow runs, another configuration fact rather than screen state',
    where: 'internal/cli/run.go:257',
  },
  {
    shape: 'argv',
    command: 'run',
    value: '--total-timeout',
    why: 'overrides the workflow’s own total timeout, which is a run’s ceiling and a reviewed configuration fact',
    where: 'internal/cli/run.go:264',
  },
  {
    shape: 'argv',
    command: 'run',
    value: '--no-compression',
    why: 'turns agent.compression off for one run, overriding the project’s reviewed configuration: a change that is reviewed where it lives',
    where: 'internal/cli/run.go:272',
  },
  {
    shape: 'argv',
    command: 'run',
    value: '--attach',
    why: 'uploads files into the run’s own inputs/: a path from the renderer by another name',
    where: 'internal/cli/run.go:268',
  },
  {
    shape: 'argv',
    command: 'run',
    value: '--gcp-project',
    why: 'would let a renderer name a cloud project, which is the boundary §3.4 rule 3 draws: the project comes from the installation’s record',
    where: 'internal/cli/cloud.go:55',
  },
  {
    shape: 'argv',
    command: 'run',
    value: '--region',
    why: 'would let a renderer name a region, for the same reason as `--gcp-project`',
    where: 'internal/cli/cloud.go:56',
  },
  {
    shape: 'argv',
    command: 'run',
    value: '',
    why: 'a positional TEXT task: `run` takes at most one (`cobra.MaximumNArgs(1)`) and refuses it together with `--task-file`, so a positional task is not a form the launch row can take — the task goes to the child on stdin and is in no argv word',
    where: 'internal/cli/run.go:206 (Use: "run [TEXT]"), :251',
  },
  {
    shape: 'command',
    value: 'setup',
    why: 'a write: the first-run converge creates the GCP project, the installation and a repository’s Fugaro wiring, and it applies Terraform',
    where: 'internal/cli/setup.go:235 (the setup door of internal/cli/init.go’s newInitCmdWith)',
  },
  {
    shape: 'command',
    value: 'update',
    why: 'a write: the re-converge applies Terraform over an existing installation, and it may destroy what a removed flag built',
    where: 'internal/cli/update.go:58 (the update door of internal/cli/init.go’s newInitCmdWith)',
  },
  {
    shape: 'command',
    value: 'init',
    why: 'the retired converge: a hidden tombstone that applies nothing and exits 1, naming `fugaro setup` and `fugaro update`. It stays on this table because the console may not drive it either — a verb that refuses every argv is not a read the console could make',
    where: 'internal/cli/retired.go:39 (Hidden: true)',
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
  // **The models group's writers, and the two shapes its fence is drawn in.**
  // `fugaro models ls --json` is the group's one reachable word
  // (`models.list` above, console 0.119.0); the three commands here are the
  // writes beside it -- `add` claims a model on a route and prices it, `rm`
  // drops its claim, price and allow-list entry, and `use` points one
  // repository's own config file at a model in its working tree
  // (`internal/cli/models_add.go:27`, `models_rm.go:20`, `models_use.go:24`).
  // Each edits a configuration a reviewer signed off on, which is exactly the
  // kind of act this table keeps off a screen.
  //
  // **Two shapes, and the order between them is deliberate.** The three
  // `command` rows refuse the command paths themselves, and the `argv` rows
  // under them refuse the four *words* wherever an operation under `models`
  // spells them -- the command path or the flags -- which is what covers a
  // mutation the command rows do not: a future operation whose command is
  // `models add <id>` (the shape `run --task-file -` has) is not the command
  // `models add` and would slip past a command row alone. Each `command` row
  // must stay **above** the `argv` row carrying its word: an operation driving
  // `models add` matches both, and `isNeverReachable` answers with the first,
  // which `console/test/unit/ipc.test.ts` asserts per row.
  {
    shape: 'command',
    value: 'models add',
    why: 'a write: it appends the model to a route’s own list in the project config and writes a model_prices entry and a budget.allowed_models entry for it, so it changes what every run of the project may name and spend on',
    where: 'internal/cli/models_add.go:27',
  },
  {
    shape: 'command',
    value: 'models rm',
    why: 'a write: it drops the model’s claim, its price and its allow-list entry from the project config, which stops every run that names it',
    where: 'internal/cli/models_rm.go:20',
  },
  {
    shape: 'command',
    value: 'models use',
    why: 'a write: it rewrites `agent.models.<role>` in a repository’s own config file in its working tree, and the model it points a role at is a reviewed configuration fact',
    where: 'internal/cli/models_use.go:24',
  },
  {
    shape: 'argv',
    command: 'models',
    value: 'add',
    why: 'the write above, refused as a word of this command as well: a `models` argv that spells `add` is refused before `spawn` is called, whether it spells it in the command path or in the flags it fixes',
    where: 'internal/cli/models_add.go:27',
  },
  {
    shape: 'argv',
    command: 'models',
    value: 'rm',
    why: 'the write above, refused as a word of this command as well',
    where: 'internal/cli/models_rm.go:20',
  },
  {
    shape: 'argv',
    command: 'models',
    value: 'use',
    why: 'the write above, refused as a word of this command as well',
    where: 'internal/cli/models_use.go:24',
  },
  {
    shape: 'argv',
    command: 'models',
    value: 'search',
    why: 'a read, and the one word of this group that takes a **positional** `<text>` a renderer would fill: this surface reads the project’s models with `models ls`, which takes no argument at all, and a free word a caller chooses is the shape this table keeps off it — the CLI’s own search is a terminal’s verb',
    where: 'internal/cli/models_search.go:21 (Use: "search <text>")',
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
    value: '--check-bitbucket-token',
    why: 'it reads the credentials service’s bundle — a Bitbucket token — with the operator’s own credentials and asks Bitbucket to accept it: the same reason as `--check-github-app`, a secret value this process would be the one to fetch',
    where: 'internal/cli/doctor.go:301',
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
    where: 'ee/report/cli/report.go:110',
  },
  {
    shape: 'flag',
    value: '--watch',
    why: '`watch` under another name; the console uses `watch`',
    where: 'internal/cli/runs.go:107',
    // The one exception on this table (the graph plan's CG9b): `graph --watch
    // --json` is the graph's own stream and not a second name for another read
    // -- `fugaro graph` has no `watch` word and no other command prints the
    // graph -- so `graph.watch` is the one operation that may carry it. Every
    // other command's `--watch` is refused exactly as before, and the exception
    // is one operation's: the word is matched against `operation.flags` *and*
    // against the argv's command path (`src/main/spawn.ts`'s `assertArgvFlags`),
    // so what is admitted is the row that builds `graph --watch --json` and
    // nothing else.
    except: ['graph.watch'],
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
 *
 * A flag row's `except` names the operations that row does not apply to, and it
 * is read only when the caller names the operation it is asking about: the word
 * itself is refused for every other operation and for a caller that names none,
 * so an exception is a fact about one argv on the surface rather than a
 * permission for a word (`graph.watch`'s row above).
 */
export function isNeverReachable(operation: {
  /** `name` is the operation asking, and it is **optional on purpose**: the
   * table's flag rows are blanket refusals, and only a caller that says which
   * operation it is asking about can be one of a row's `except` names. A caller
   * that names none -- a test asking whether the bare word is reachable at all --
   * gets the refusal, which is what keeps "the word alone is never allowed" the
   * answer here. */
  readonly name?: string | null
  readonly command: string | null
  readonly flags: readonly string[]
}): NeverReachable | null {
  for (const entry of NEVER_REACHABLE) {
    if (entry.shape === 'command' && entry.value === operation.command) return entry
    if (entry.shape === 'flag' && operation.flags.includes(entry.value)) {
      const name = operation.name ?? null
      if (name === null || entry.except === undefined || !entry.except.includes(name)) return entry
    }
    // An `argv` row is about one word on one command, and it matches an
    // operation whose command path is that command *or* a longer path under it,
    // because the launch's own command carries two fixed words of its argv
    // (`run --task-file -`). The word itself is looked for in the whole vector
    // the operation states -- the command's words and the flags it fixes -- so
    // an excluded flag spelled into either half is refused, and the launch,
    // which names none of them and fixes one `--json`, is clear of every row.
    // The empty value is the positional-TEXT row: no operation can name a
    // positional word at all, so nothing here can match it, and the argv the
    // surface actually builds is what `console/test/unit/ipc.test.ts` reads it
    // against.
    if (entry.shape === 'argv' && entry.value !== '' && entry.command !== null && operation.command !== null) {
      const under = entry.command === operation.command || operation.command.startsWith(`${entry.command} `)
      if (under && [...operation.command.split(' '), ...operation.flags].includes(entry.value)) return entry
    }
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
/** MAX_PROVIDER_LENGTH bounds the `gitProvider` kind before its membership
 * check runs, and it is the longest word the builder's own set carries. */
const MAX_PROVIDER_LENGTH = Math.max(...PULL_REQUEST_PROVIDERS.map((name) => name.length))
const RUN_ID_RE = /^\d{8}-\d{6}-[0-9a-f]{4,16}$/
const DAY_RE = /^(\d{4})-(\d{2})-(\d{2})$/
// The two `--since` flags are two different grammars, and each one is written
// as its own kind so neither can admit a value its own command refuses. `report`
// takes days and weeks but not hours (`reportRange`, `ee/report/cli/report.go:116`
// -- an `h` suffix falls through to the date parser there); `runs ls` takes
// `parseSince`'s own forms (`internal/cli/runs.go:502`), which is what the two
// patterns below are: its grammar rather than a summary of it. `parseSince`
// tries the `d` suffix *first* (so `7d5h` is not a duration), then hands
// anything else to Go's `time.ParseDuration`, whose units are `ns`, `us`,
// `µs` (U+00B5), `μs` (U+03BC), `ms`, `s`, `m`, `h` -- each with a number that
// may be fractional, and whose sign may be leading.
//
// The console admits six of those eight: the two non-ASCII spellings of a
// microsecond are left out on purpose. The spawn side's argument alphabet
// (`ARGUMENT_RE`, `src/main/spawn.ts`) is ASCII, and a value this schema admits
// and that alphabet refuses would be refused after the click instead of before
// it. `us` is the same unit and stays, so nothing the operator can ask the CLI
// for is lost -- only a second spelling of it, and the spawn boundary stays
// ASCII-only rather than being widened for a unit nobody windows runs by.
const SINCE_DAYS_RE = /^\+?\d+$/
const GO_DURATION_RE =
  /^[+-]?(?:\d+(?:\.\d*)?|\.\d+)(?:ns|us|ms|s|m|h)(?:(?:\d+(?:\.\d*)?|\.\d+)(?:ns|us|ms|s|m|h))*$/
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
  'mode',
])
const SECRET_SCOPES: readonly string[] = Object.freeze(['project-wide', 'repo'])

/** SETTINGS_OP_WORDS is the settings write's two subcommands, and the whole of
 * them: `spawn.ts`'s `settingOp` case holds the same two, so the word that
 * decides what a child does is closed in both the renderer's gate and the
 * argv's. */
const SETTINGS_OP_WORDS: readonly string[] = Object.freeze(['set', 'unset'])

/** MAX_SETTING_KEY_LENGTH bounds a setting's key before its grammar runs, so a
 * hostile payload cannot make the expression work. Every key the registry holds
 * is far shorter: the longest is a `rigor.areas.N.<member>` spelling. */
const MAX_SETTING_KEY_LENGTH = 120

/**
 * SETTINGS_KEY_RE is a key's shape: dot-separated segments of lowercase
 * letters, digits and `_`, with `*` allowed for the registry's one wildcard row
 * (`stages.*.claude_account`), and no leading or trailing dot.
 *
 * **`*` is admitted anywhere a letter is, on purpose.** A standalone segment is
 * the spelling the registry uses, but `stages.rev*iew.x` passes this grammar
 * too, and that is the honest shape of the claim: this expression bounds the
 * **word** a key can be, and `internal/cli/settings_set.go:305` refuses any key
 * carrying a `*` with its own sentence -- "varies with a name this command does
 * not take" -- before the allow-list is consulted. Tightening the segment here
 * would move that refusal from the CLI's words to a schema refusal of this
 * console's, for a key the CLI was always going to refuse.
 *
 * **It is a shape and not the allow-list.** Whether the generic write path owns
 * a key is `settingsGenericKeys`/`settingsGenericPrefixes`
 * (`internal/cli/settings_set.go:68`, `:92`) -- the CLI's own list, which the
 * console keeps no copy of (D1102). What this grammar does is keep the argv
 * word a key can be: no space, no dash, no `=`, nothing that could be read as a
 * flag or quoted by a shell, so the key never needs the terminator the value
 * does.
 */
const SETTINGS_KEY_RE = /^[a-z0-9_*]+(\.[a-z0-9_*]+)*$/

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

/** isFormatCharacter is the shared set of invisible characters
 * (`INVISIBLE_CLASS` in `src/shared/sanitize.ts`: every `Cf`, every `Co`, every
 * default-ignorable code point, every bidi control) plus the line and paragraph
 * separators: the characters that change how a string is read without being
 * read. It is the same definition the recorder and the display layer strip, so
 * an argument cannot carry a character one of them would remove. */
function isFormatCharacter(code: number): boolean {
  return isInvisibleCodePoint(code) || isSeparatorCodePoint(code)
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
 * LONGEST_RUNS_SINCE is the longest canonical runs window the CLI accepts: the
 * largest duration `time.ParseDuration` reads (`1<<63-1` nanoseconds), spelled
 * out with its optional plus. Every other canonical form is shorter -- the
 * largest `Nd` count `strconv.Atoi` reads is `+9223372036854775807d`, 21
 * characters -- so its length is `runSince`'s bound. It used to be ten, which
 * refused `1000h30m15s` and an eleven-digit day count the CLI takes (review
 * round 2 of PR 514). A value longer than this is one padded with leading
 * zeros, surplus fraction digits or repeated units; the CLI would read it, and
 * the console refuses it before the grammar, as the price of a bound.
 */
const LONGEST_RUNS_SINCE = '+2562047h47m16.854775807s'

/**
 * isRunsSince mirrors `parseSince` (`internal/cli/runs.go:502`) branch for
 * branch, and it exists because the console used to accept two things that
 * function always refuses: **a UTC calendar day** (there is no date branch) and
 * **a zero window** (`0d`, `0h`, `0m` -- `days < 1` and `d <= 0`). Both were
 * refused by the CLI after a child had been spawned, which is the one outcome
 * the schema exists to prevent.
 *
 * The branches, in the CLI's order:
 *
 *   1. `"0"` exactly -- no bound;
 *   2. a `d` suffix, whose digits `strconv.Atoi` reads, and which must be at
 *      least one and at most `maxSinceDays` (106751): past it
 *      `time.Duration(days) * 24 * time.Hour` would wrap int64 nanoseconds, so
 *      the CLI refuses it and the schema refuses it here first;
 *   3. anything else is a Go duration, and `parseSince` refuses `d <= 0`. A
 *      leading `-` is that case, and so is a duration whose every digit is
 *      zero (`0s`, `0h0m`); `+5h` is a positive duration the CLI accepts.
 */
/** MAX_SINCE_DAYS is `parseSince`'s `maxSinceDays` (`internal/cli/runs.go`):
 * `floor((1<<63-1) / (24h in ns))`. */
const MAX_SINCE_DAYS = 106751n

function isRunsSince(value: string): boolean {
  if (value === '0') return true
  if (value.endsWith('d')) {
    const days = value.slice(0, -1)
    if (!SINCE_DAYS_RE.test(days)) return false
    // `parseSince`'s `maxSinceDays`: the last day count whose duration fits
    // int64 nanoseconds. BigInt, so a count of any length is compared exactly.
    const count = BigInt(days.replace(/^\+/, ''))
    return count >= 1n && count <= MAX_SINCE_DAYS
  }
  if (!GO_DURATION_RE.test(value)) return false
  if (value.startsWith('-')) return false
  const nanos = goDurationNanos(value)
  return nanos !== null && nanos > 0n
}

/** GO_UNIT_NANOS is `time.ParseDuration`'s unit map, for the six ASCII units
 * `GO_DURATION_RE` admits. */
const GO_UNIT_NANOS: Readonly<Record<string, bigint>> = Object.freeze({
  ns: 1n,
  us: 1_000n,
  ms: 1_000_000n,
  s: 1_000_000_000n,
  m: 60_000_000_000n,
  h: 3_600_000_000_000n,
})

/** isAsciiDigit is Go's `c >= '0' && c <= '9'` on a UTF-16 code unit. */
function isAsciiDigit(code: number): boolean {
  return code >= 48 && code <= 57
}

/** GO_INT64_EDGE is `1<<63`, the bound every overflow check in
 * `time.ParseDuration` is written against. */
const GO_INT64_EDGE = 1n << 63n

/**
 * goDurationNanos is `time.ParseDuration`'s arithmetic, step for step
 * (`src/time/format.go`), for a value `GO_DURATION_RE` already admitted: the
 * integer part through `leadingInt` (refused past `1<<63`), the fraction through
 * `leadingFraction` (whose digits past the precision are dropped, not refused,
 * and whose scale is a float64), `v > 1<<63/unit` refused, the fraction added in
 * float64 and truncated, each term and the running sum refused past `1<<63`, and
 * a positive total refused past `1<<63-1`. It returns null where Go returns
 * `time: invalid duration`, so a window the CLI would refuse after the spawn is
 * refused here, before it -- a length bound keeps no *magnitude* inside an
 * int64 of nanoseconds (`999999999h`; review round 2 of PR 478).
 */
function goDurationNanos(value: string): bigint | null {
  let rest = value
  let negative = false
  if (rest.startsWith('-') || rest.startsWith('+')) {
    negative = rest.startsWith('-')
    rest = rest.slice(1)
  }
  let total = 0n
  while (rest !== '') {
    // leadingInt
    let i = 0
    let whole = 0n
    for (; i < rest.length && isAsciiDigit(rest.charCodeAt(i)); i += 1) {
      if (whole > GO_INT64_EDGE / 10n) return null
      whole = whole * 10n + BigInt(rest.charCodeAt(i) - 48)
      if (whole > GO_INT64_EDGE) return null
    }
    rest = rest.slice(i)
    // leadingFraction
    let fraction = 0n
    let scale = 1
    if (rest.startsWith('.')) {
      rest = rest.slice(1)
      let j = 0
      let overflow = false
      for (; j < rest.length && isAsciiDigit(rest.charCodeAt(j)); j += 1) {
        if (overflow) continue
        if (fraction > (GO_INT64_EDGE - 1n) / 10n) {
          overflow = true
          continue
        }
        const next = fraction * 10n + BigInt(rest.charCodeAt(j) - 48)
        if (next > GO_INT64_EDGE) {
          overflow = true
          continue
        }
        fraction = next
        scale *= 10
      }
      rest = rest.slice(j)
    }
    // The two-letter units are tried first, so `ms` is never read as `m`.
    const unitMatch = /^(?:ns|us|ms|s|m|h)/.exec(rest)
    if (unitMatch === null) return null
    const unitName = unitMatch[0]
    const unit = GO_UNIT_NANOS[unitName]
    if (unit === undefined) return null
    rest = rest.slice(unitName.length)
    if (whole > GO_INT64_EDGE / unit) return null
    let term = whole * unit
    if (fraction > 0n) {
      term += BigInt(Math.trunc(Number(fraction) * (Number(unit) / scale)))
      if (term > GO_INT64_EDGE) return null
    }
    total += term
    if (total > GO_INT64_EDGE) return null
  }
  if (negative) return -total
  if (total > GO_INT64_EDGE - 1n) return null
  return total
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
  if (spec.kind === 'runLimit') {
    // The digits cobra's `IntVar` reads, and nothing else: `0` is the CLI's own
    // "every row of the window", which is what omitting the argument already
    // means here, and the cap is this surface's (`RUNS_LIST_MAX_LIMIT`).
    if (typeof value !== 'number' || !Number.isInteger(value) || value < 1 || value > RUNS_LIST_MAX_LIMIT) {
      return refuse(`${spec.key} must be a page size between 1 and ${RUNS_LIST_MAX_LIMIT}`)
    }
    return { ok: true, value }
  }
  if (spec.kind === 'licenceKey') {
    // **Handled before the common string path, and the trim is why.** A key
    // pasted out of a document or an email arrives with whitespace around it
    // -- a trailing newline is the ordinary case -- and the common
    // hidden-character rule refuses a control character, so a value checked in
    // that order would refuse the most ordinary paste of all with a sentence
    // about U+000A. What is judged is therefore the **trimmed** value: the
    // whitespace a copy adds carries no meaning (the verifier trims for the
    // same reason, `verifyLicenceKey`), and a **hidden** character inside a key
    // is still refused, by the same rule and in the same words as every other
    // argument. A plain space inside one is not hidden and is admitted: the
    // format is what refuses it, one step later and in its own words.
    //
    // **The format is deliberately not checked here.** Whether the three parts
    // decode, whether the id is one this build holds and whether the signature
    // covers the payload are `licence.ts`'s (`verifyLicenceKey`, S2.4), and its
    // sentence -- "a licence key is three base64url parts" -- is the one the
    // screen draws. A grammar here would be a second copy of the format and a
    // second thing to drift from it.
    if (typeof value !== 'string') return refuse(`${spec.key} must be a string`)
    const pasted = value.trim()
    if (pasted.length > MAX_LICENCE_KEY_LENGTH) {
      return refuse(`${spec.key} is longer than ${MAX_LICENCE_KEY_LENGTH} characters`)
    }
    if (pasted === '') return refuse(`${spec.key} must be a key`)
    const hidden = hiddenCharacterReason(pasted)
    if (hidden !== null) return refuse(`${spec.key} carries ${hidden}`)
    // The trimmed text, so what is verified is what is stored and what a child
    // is handed: one spelling of the key, from the paste to the environment.
    return { ok: true, value: pasted }
  }
  if (typeof value !== 'string') return refuse(`${spec.key} must be a string`)
  const limit = kindLimit(spec.kind)
  if (value.length > limit) return refuse(`${spec.key} is longer than ${limit} characters`)
  // **The launch's three are the one exception to the hidden-character rule,
  // and only because their own rule already covers it.** `hiddenCharacterReason`
  // refuses every control character, which is right for a value with a grammar
  // and wrong for the task text: a task is prose and multi-line by design, and
  // the newline is a character it is *made* of. The title and the plan task are
  // held to the same clause by `launchrules.ts`'s own checker (a control
  // character, a format character, a line break beyond the first, a spoofing
  // sequence), which runs below -- so `--title`/`--plan-task` do not lose the
  // check, they get a stricter one, and the task text is refused for a NUL and
  // for anything that is not valid UTF-8 there rather than here.
  const launchKind =
    spec.kind === 'launchTask' || spec.kind === 'launchTitle' || spec.kind === 'launchPlanTask'
  if (!launchKind) {
    const hidden = hiddenCharacterReason(value)
    if (hidden !== null) return refuse(`${spec.key} carries ${hidden}`)
  }
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
    case 'runAfter':
      // The same grammar in a different position: the cursor is an id this same
      // read printed, and a value that is not one (`--watch`, a path, a
      // truncated id) is refused before a child exists. The `=` form the row
      // emits is what makes a value's own leading dashes impossible to read as
      // a flag, and this grammar is what makes them impossible to send.
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
    case 'summarySince':
      // `runs summary --since`, and **the console's own set rather than the
      // command's grammar**: the command reads `parseSince`'s forms the way
      // `runs stats` does, and this surface admits exactly the windows the
      // screens need (`SUMMARY_SINCE_WINDOWS`). A window outside it -- a longer
      // one, a date, a Go duration, `0` -- is refused before a child exists, so
      // the read the operation exists for cannot be turned into a whole-history
      // read by another spelling of the same flag.
      if (!SUMMARY_SINCE_WINDOWS.includes(value)) {
        return refuse(`${spec.key} must be one of ${SUMMARY_SINCE_WINDOWS.join(', ')}`)
      }
      return { ok: true, value }
    case 'reportDimension':
      if (!REPORT_DIMENSIONS.includes(value)) return refuse(`${spec.key} must be one of ${REPORT_DIMENSIONS.join(', ')}`)
      return { ok: true, value }
    case 'secretScope':
      if (!SECRET_SCOPES.includes(value)) return refuse(`${spec.key} must be ${SECRET_SCOPES.join(' or ')}`)
      return { ok: true, value }
    case 'graphSource':
      // The graph read's scope (CG9a): `all`, `coordinator`, or an id-carrying
      // scope whose id matches the CLI's own pattern. This is the half that
      // keeps a value from being a path, a flag or another word of the command
      // -- `-x`, `all --source local:o1`, `orchestrator:../..` and a 41-character
      // id are all refused here, before a child exists -- and the other half,
      // that the scope is one a previous read of this document named, is the
      // source's (`createBoundSource`, F21).
      if (!GRAPH_SCOPE_RE.test(value)) {
        return refuse(
          `${spec.key} must be all, coordinator, or orchestrator:<id> / local:<id> with an id of 1 to 40 characters of a-z, 0-9 and -`,
        )
      }
      return { ok: true, value }
    case 'link': {
      const decision = validateExternalUrl(value)
      if (!decision.ok) return refuse(`${spec.key} ${decision.reason}`)
      return { ok: true, value: decision.url }
    }
    case 'gitProvider':
      // A closed set of two words and nothing else: this is the value the
      // *builder* keys its host table by, so a provider it does not know is a
      // link that cannot be built and a refusal here rather than a `null` one
      // step later.
      if (!PULL_REQUEST_PROVIDERS.includes(value)) {
        return refuse(`${spec.key} must be one of ${PULL_REQUEST_PROVIDERS.join(', ')}`)
      }
      return { ok: true, value }
    case 'installationName':
      // `isProjectName` (`src/main/installations.ts`), spelled here for the
      // reason the two bounds above give: the store's own rule, which is what
      // the file is keyed by, and stricter than `isSafeInstallationValue`.
      if (!isInstallationName(value)) return refuse(`${spec.key} must be an installation’s project name`)
      return { ok: true, value }
    case 'installationCheckout':
      // A shape check and nothing more, exactly as the store's own validator
      // says: whether the CLI actually listed this directory is checked against
      // the document the main process last read, which this grammar cannot know.
      if (!isInstallationCheckout(value)) {
        return refuse(`${spec.key} must be an absolute directory path`)
      }
      return { ok: true, value }
    case 'launchTask': {
      // Part 1's own rule (`launchrules.ts`, the only place the caps and the
      // trim live): the trimmed text, the 16 KiB cap, valid UTF-8, no NUL.
      const decision = checkLaunchTask(value)
      return decision.ok ? { ok: true, value: decision.value.text } : refuse(`${spec.key} ${decision.reason}`)
    }
    case 'launchTitle': {
      const decision = checkLaunchTitle(value)
      // A `null` cannot happen here: the value is a string, because every
      // string kind is refused above when it is not one. An empty title is a
      // title the operator did not fill in, and the renderer leaves the key out
      // for that -- so an empty string is a refusal rather than a silent
      // "no title", which is what the checker says in its own sentence.
      if (!decision.ok) return refuse(`${spec.key} ${decision.reason}`)
      return decision.value === null ? refuse(`${spec.key} must be a title`) : { ok: true, value: decision.value }
    }
    case 'launchPlanTask': {
      const decision = checkLaunchPlanTask(value)
      if (!decision.ok) return refuse(`${spec.key} ${decision.reason}`)
      return decision.value === null ? refuse(`${spec.key} must be a plan task`) : { ok: true, value: decision.value }
    }
    case 'settingOp': {
      // The closed set of two, checked here as well as in `spawn.ts`'s own
      // `settingOp` case: this is the renderer's gate and that one is the
      // argv's, and the word decides which subcommand a child runs.
      if (!SETTINGS_OP_WORDS.includes(value)) {
        return refuse(`${spec.key} must be one of ${SETTINGS_OP_WORDS.join(', ')}`)
      }
      return { ok: true, value }
    }
    case 'settingKey': {
      // The key's **shape** and not its membership: whether the generic path
      // owns a key is the CLI's own allow-list (D1102), so a key outside it is
      // refused by the child's own sentence, which the console shows verbatim.
      if (!SETTINGS_KEY_RE.test(value)) return refuse(`${spec.key} must be a setting's key`)
      return { ok: true, value }
    }
    case 'settingValue': {
      // **An empty value is refused rather than written.** Clearing a key is
      // `unset`, the other subcommand -- and the screen sends that one when the
      // operator's box is empty -- so an empty string here would be a write of
      // the empty value, which is a different thing a run would read.
      if (value === '') return refuse(`${spec.key} must be a value: clearing a key is unset`)
      return { ok: true, value }
    }
    default:
      return refuse(`${operation} does not take ${spec.key}`)
  }
}

/**
 * isInstallationName and isInstallationCheckout are the rail's two grammars,
 * and they are `src/main/installations.ts`'s `isProjectName` and
 * `isAbsolutePath(value, MAX_INSTALLATION_PATH_LENGTH)` written out: a project
 * name is non-empty, bounded, free of whitespace and of control characters and
 * does not begin with `-`; a checkout is an absolute path under the same rules.
 *
 * They are duplicated rather than imported because `installations.ts` imports
 * this module, and a test holds the two implementations to the same verdict on
 * the same values so the duplication cannot drift in silence.
 */
function isInstallationName(value: string): boolean {
  return (
    value !== '' &&
    value.length <= MAX_INSTALLATION_NAME_LENGTH &&
    isSafeInstallationValue(value) &&
    !/\s/.test(value)
  )
}

function isInstallationCheckout(value: string): boolean {
  return (
    value !== '' &&
    value.length <= MAX_INSTALLATION_CHECKOUT_LENGTH &&
    isSafeInstallationValue(value) &&
    isAbsolute(value)
  )
}

/** isSafeInstallationValue is `spawn.ts`'s own guard, spelled here for the same
 * reason: a string the operator's machine produced -- a project name, a
 * directory -- may carry a space, and a space is harmless with `shell: false`
 * and an array argv; what it may not carry is a NUL, a control character, an
 * absurd length or a leading `-`. */
function isSafeInstallationValue(value: string): boolean {
  if (value === '' || value.length > 4096) return false
  if (value.startsWith('-')) return false
  // eslint-disable-next-line no-control-regex
  return !/[\u0000-\u001f\u007f]/.test(value)
}

/** kindLimit is each kind's length bound, checked before any grammar runs so
 * that a hostile payload cannot make a regular expression work. */
function kindLimit(kind: ArgumentKind): number {
  switch (kind) {
    case 'repository':
      return MAX_REPOSITORY_LENGTH
    case 'runId':
    case 'runAfter':
      return MAX_RUN_ID_LENGTH
    case 'day':
      return 10
    case 'reportSince':
      // A day is `2026-10-16`; the longest window is `3660d`. The bound is the
      // longer of the two, and the grammar decides which one a value is.
      return 10
    case 'runSince':
      // The grammar's longest canonical value (`LONGEST_RUNS_SINCE`). It is a
      // length bound only: the magnitude of a duration and of a `Nd` count is
      // decided by `isRunsSince`, as `time.ParseDuration` and `strconv.Atoi`
      // decide it.
      return LONGEST_RUNS_SINCE.length
    case 'summarySince':
      // The longest window in the closed set, read off the set rather than
      // spelled as a digit: membership is what decides, and a third window
      // longer than these two cannot be refused by the bound before its own
      // membership check sees it.
      return Math.max(...SUMMARY_SINCE_WINDOWS.map((window) => window.length))
    case 'reportDimension':
      return 8
    case 'secretScope':
      return 12
    case 'link':
      return MAX_URL_LENGTH
    case 'gitProvider':
      // The longest word in the closed set, and a bound rather than a grammar:
      // membership is what decides, so a hostile payload cannot make the check
      // work on a long string. It is read off the set rather than spelled as a
      // digit, so a third provider longer than these two cannot be refused by
      // the bound before its own grammar sees it.
      return MAX_PROVIDER_LENGTH
    case 'installationName':
      return MAX_INSTALLATION_NAME_LENGTH
    case 'installationCheckout':
      return MAX_INSTALLATION_CHECKOUT_LENGTH
    // The launch's three (design §5.1 rule 11). Each bound is a **small
    // multiple of the content cap** the rule itself sets -- four times the
    // task's 16 KiB, four times the title's 100 runes, four times the plan
    // task's 128 bytes -- and it is here, in the check that runs before any
    // grammar, so that a renderer cannot make this process work on a string
    // four hundred times the size of the thing it is going to refuse. The
    // constants that name the real caps are `launchrules.ts`'s, and they are
    // what refuses a value inside the multiple.
    case 'launchTask':
      return LAUNCH_TASK_RAW_LIMIT
    case 'launchTitle':
      return LAUNCH_TITLE_RAW_LIMIT
    case 'launchPlanTask':
      return LAUNCH_PLAN_TASK_RAW_LIMIT
    // The settings write's three (C3). The subcommand's bound is the longest of
    // the two words (`unset`); the key's is `MAX_SETTING_KEY_LENGTH`, checked
    // before its grammar runs; the value's is the console's own
    // `SETTINGS_VALUE_MAX_LENGTH`, which is the widest single argument this
    // surface admits -- so a payload of several is bounded by the whole-payload
    // check above this one and not by these.
    case 'settingOp':
      return 5
    case 'settingKey':
      return MAX_SETTING_KEY_LENGTH
    case 'settingValue':
      return SETTINGS_VALUE_MAX_LENGTH
    // The graph read's scope (CG9a): the longest value its own pattern admits,
    // so the grammar never runs on a string longer than one it could accept.
    case 'graphSource':
      return MAX_GRAPH_SCOPE_LENGTH
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
  if (payloadWidth(supplied, keys) > (operation.payloadWidth ?? MAX_PAYLOAD_WIDTH)) {
    return refuse(
      `${operation.name} takes a payload of at most ${String(operation.payloadWidth ?? MAX_PAYLOAD_WIDTH)} characters`,
    )
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

  // The settings write's pairing (C3): a `set` is a value and an `unset` is no
  // value at all. Both halves are refused here rather than by the child, because
  // either one reaching an argv is a child spawned to answer a command that
  // cannot mean anything -- and the screen's own rule is the same one, one box
  // over: an empty box is the `unset` it sends (`renderer/entry.ts`).
  if (operation.name === 'settings.edit') {
    const op = args['op']
    const value = args['value']
    if (op === 'set' && value === undefined) return refuse('value is required to set a setting')
    if (op === 'unset' && value !== undefined) return refuse('value goes with set: unset removes the key')
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

// ---------------------------------------------------------------------------
// The startup handshake's answer on the wire (desktop-console plan Task 3.3).
// ---------------------------------------------------------------------------

/**
 * COMPATIBILITY_UNREADABLE is the console's own sentence for a stored
 * compatibility the check below will not answer with. It is a constant of this
 * console's -- like `HANDSHAKE_UNREADABLE` in `src/main/window.ts` and
 * `SENDER_REFUSAL` above -- so an unreadable value never puts anything it
 * carried on the wire, not even the value's own words.
 */
export const COMPATIBILITY_UNREADABLE = 'the console stored a compatibility it could not read'

/** MAX_COMPATIBILITY_FIELD_LENGTH bounds every free string a compatibility
 * carries on the wire (`skew`, `cliVersion`, `because`). The longest the
 * console builds is a sentence of its own well under it; the version line the
 * resolver accepts is at most 200 characters (`parseVersionLine` in
 * `src/main/spawn.ts`), and this is that bound. */
export const MAX_COMPATIBILITY_FIELD_LENGTH = 200

/**
 * readStoredCompatibility is the closed-schema check `startup.compatibility`'s
 * handler runs on the value it is about to answer with: the store is in this
 * process and the value comes from the handshake, so this is not a defence
 * against a document -- it is the guard that says what the operation's contract
 * *is*, so that a mutation to the store (a mode typo, a view id that is not on
 * `REDUCED_VIEWS`, a `hidden` list that no longer agrees with `hidesView`, a
 * version that is not a string) answers the console's own refusal sentence
 * instead of handing the renderer a compatibility it would gate on wrongly.
 *
 * The renderer's half is `src/renderer/app.ts`'s own check, and it is
 * deliberately a second implementation rather than an import: the renderer must
 * refuse an answer that is not a compatibility **even if this one let it
 * through**, and a shared checker would make the two failures one failure.
 * `test/unit/ipc.test.ts` holds the two to the same verdicts over the same
 * table of hostile values, so "two implementations" cannot drift into "two
 * policies".
 *
 * Every failure is the same refusal (`unreadableCompatibility(
 * COMPATIBILITY_UNREADABLE)`), and the value's own contents are never carried
 * into it. A value that passes is answered as a **fresh, frozen object built
 * from the checked fields only** (round 2 of the Task 3.3 review: the first
 * version returned its input, so an unchecked key rode along verbatim).
 */
export function readStoredCompatibility(value: unknown): CliCompatibility {
  const unreadable = (): CliCompatibility => unreadableCompatibility(COMPATIBILITY_UNREADABLE)
  if (!isPlainPayload(value)) return unreadable()

  const mode = value['mode']
  if (mode !== 'ok' && mode !== 'reduced' && mode !== 'refused') return unreadable()
  // `skew` is drawn as a `data-skew` attribute and never as a sentence, and its
  // closed list lives in a type (`CliSkewName`) rather than in a value, so what
  // is checked here is that it is a short string -- the same verdict the
  // renderer's own check reaches, which `test/unit/ipc.test.ts` holds both to.
  const skew = value['skew']
  if (typeof skew !== 'string' || skew === '' || skew.length > MAX_COMPATIBILITY_FIELD_LENGTH) {
    return unreadable()
  }
  const consoleVersion = value['consoleVersion']
  const floor = value['floor']
  if (typeof consoleVersion !== 'string' || typeof floor !== 'string') return unreadable()
  const cliVersion = value['cliVersion']
  if (cliVersion !== null && typeof cliVersion !== 'string') return unreadable()
  if (typeof cliVersion === 'string' && cliVersion.length > MAX_COMPATIBILITY_FIELD_LENGTH) {
    return unreadable()
  }
  const supported: unknown = value['supported']
  if (!Array.isArray(supported) || supported.some((minor: unknown) => typeof minor !== 'string')) {
    return unreadable()
  }

  const hidden = value['hidden']
  if (!Array.isArray(hidden)) return unreadable()
  const views = hidden as readonly unknown[]
  if (views.some((view) => !REDUCED_VIEWS.includes(view as ReducedView))) return unreadable()
  if (new Set(views).size !== views.length) return unreadable()

  // `because` is the one free sentence the value can carry, so it is held to
  // the most: absent, or a string; short; and only on the one kind of value
  // that is built with one (`unreadableCompatibility`, whose skew is
  // `unreadable`). Since round 2 every `because` the console stores is one of
  // its own constants, and the drawing passes it through `sanitizeText` too
  // (`unreadableReason` in `src/shared/version.ts`) -- this check is what keeps
  // a wrongly typed or overlong one off the wire altogether.
  const hasBecause = Object.prototype.hasOwnProperty.call(value, 'because')
  const because = value['because']
  if (hasBecause) {
    if (typeof because !== 'string' || because === '') return unreadable()
    if (because.length > MAX_COMPATIBILITY_FIELD_LENGTH) return unreadable()
    if (skew !== 'unreadable') return unreadable()
  }

  // The answer is a **fresh object built from the checked fields only**: a key
  // the check did not look at (a `rawStdout`, a getter, anything a later edit to
  // the store adds) is not carried, because carrying the input object would make
  // this a check on some fields and a pass-through for the rest.
  const checked: CliCompatibility = {
    mode,
    skew: skew as CliCompatibility['skew'],
    cliVersion,
    consoleVersion,
    supported: Object.freeze([...(supported as string[])]),
    floor,
    hidden: Object.freeze([...(views as ReducedView[])]),
    ...(hasBecause ? { because: because as string } : {}),
  }

  // The one check that is about the *pair* rather than a field: the mode and the
  // hidden list have to be the two halves of one answer (`hidesView` is the
  // renderer's question, and it is answered from `hidden`, so a mode that says
  // `ok` beside a list that withholds spend would gate one way in main and the
  // other way in the renderer). `compatibility()` in `src/shared/version.ts`
  // builds every branch as either `[]` (ok) or all of `REDUCED_VIEWS`, so this
  // is what that invariant is worth on the wire.
  for (const view of REDUCED_VIEWS) {
    if (hidesView(checked, view) !== (mode !== 'ok')) return unreadable()
  }

  return Object.freeze(checked)
}

/**
 * startupCompatibility is the handler for the one operation that reads the
 * console's own state: the value is handed in, checked, and answered -- and a
 * value the check refuses becomes a refusal the renderer draws, not an
 * exception.
 *
 * `read` is a function rather than the value because the answer is asked for at
 * message time: the store is written when the handshake answers
 * (`src/main/index.ts`, which registers these handlers in the same breath) and
 * again when the operator chooses an installation in the picker (Task 3.4b), and
 * a handler built over a snapshot would keep answering whatever the snapshot
 * was, which is exactly the kind of stale answer the handshake exists to
 * prevent. It throws for a store that has not answered yet -- an operation called before
 * startup finished is a console that does not exist yet -- and `dispatch` turns
 * that throw into the reply.
 */
export function startupCompatibility(read: () => unknown): ConsoleHandler {
  return () => readStoredCompatibility(read())
}

/**
 * openPullRequestHandler is `link.openPullRequest`'s implementation, and it
 * lives here for the same reason `startupCompatibility` does: it is not a read
 * of the CLI, so it is not a row of `handlersFor`, and the entry point -- which
 * cannot be imported under a test, because it loads Electron's runtime -- is
 * not the place a rule this security-sensitive is written.
 *
 * The opener is an argument rather than a call to `shell.openExternal` so that
 * what the handler does with a hostile triple is a thing a test can watch: the
 * only way to reach the browser from here is to call `open`, and the handler
 * calls it **only** with a URL `pullRequestUrl` built. A `null` build returns
 * without calling it at all, which is the refusal D815 asks for -- nothing
 * opens, and the number stays text.
 *
 * The three fields are narrowed to their types before the builder sees them,
 * the way `openExternalFromShell` narrows its one argument: the validator has
 * already refused anything that is not a string or a number, so this is a
 * narrowing and not a second check, and a value that is somehow neither is a
 * refusal rather than a build from something the schema did not describe.
 */
export function openPullRequestHandler(open: (url: string) => Promise<unknown>): ConsoleHandler {
  return async (args) => {
    const provider = args['provider']
    const repo = args['repo']
    const number = args['number']
    if (typeof provider !== 'string' || typeof repo !== 'string' || typeof number !== 'number') {
      return null
    }
    const url = pullRequestUrl(provider, repo, number)
    if (url === null) return null
    await open(url)
    return null
  }
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
 * isSenderOnDocument is the sender gate, narrowed to **one document**.
 *
 * `isConsoleSenderUrl` answers about the *origin*, and it has to: a fragment is
 * how a screen addresses its own views, and refusing `app://console/index.html#spend`
 * would be refusing the console's own window. But the origin is not one
 * document: the dashboard, the installation picker (Task 3.4b) and the launch
 * confirmation window (V2.5.1a) are all served from it, and an origin-only check
 * cannot tell them apart. A picker frame that invoked
 * `runs.list`, or a dashboard frame that invoked `fugaro:installations/remove`,
 * would both pass an origin check, so the check is on the **path** the frame is
 * on as well.
 *
 * The path is compared after the query and the fragment are stripped, so
 * `app://console/index.html#spend` is still the dashboard and
 * `app://console/installations.html?x=1` is still the picker. A frame on a
 * document this console does not serve -- there is none today, because
 * `resolveBundleRequest` serves only what is in the bundle -- matches neither
 * and is refused.
 */
export function isSenderOnDocument(event: ConsoleEventLike | null | undefined, entryUrl: string): boolean {
  try {
    if (!isConsoleSender(event)) return false
    const frame = event?.senderFrame
    const raw = frame?.url
    if (typeof raw !== 'string' || raw === '') return false
    return new URL(raw).pathname === new URL(entryUrl).pathname
  } catch {
    return false
  }
}

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
  // Every read below goes through Electron getters, and a destroyed frame's
  // getters *throw* ("Render frame was disposed before WebFrameMain could be
  // accessed") rather than answer null. A gate that let that escape would turn
  // a refusal into a rejected invoke, so anything that throws here refuses.
  try {
    if (event === null || event === undefined) return false
    const frame = event.senderFrame
    if (frame === null || frame === undefined) return false
    const sender = event.sender
    if (sender === null || sender === undefined) return false
    if (sender.mainFrame !== frame) return false
    return isConsoleSenderUrl(frame.url)
  } catch {
    return false
  }
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
 * letter or a UNC prefix. It is anchored on a separator that cannot be part of
 * a word -- the start of the message, whitespace, a quote, a backtick, an
 * opening bracket, `=`, `,`, `;` or `:` -- so a flag like `--repo owner/name`
 * keeps its slash, and it stops at the first character that closes a path in a
 * sentence: whitespace, a quote, a backtick, `)`, `]` or `>`.
 *
 * A colon *is* an anchor, because Go's own wrapped errors put a path straight
 * after one (`open:/Users/me/.config/...`), and so is a drive letter after one
 * (`dir:C:\Users\me`). What keeps a URL from being cut at its `//` -- which
 * would turn `https://x/y` into `https:<path>`, a corrupted sentence rather than
 * a redaction -- is the first alternative: a `scheme://` URL is matched whole,
 * first, and kept, except a `file://` one, which *is* a path and is replaced
 * whole. `replacePath` is the replacer that makes that choice. The scheme is
 * bounded and must start a word, so a long run of letters is scanned once
 * rather than once per position.
 */
const ABSOLUTE_PATH_RE =
  /((?<![A-Za-z0-9+.-])[A-Za-z][A-Za-z0-9+.-]{0,31}:\/\/[^\s'"`)\]>]*)|(^|[\s'"`([<{=,;:])(?:~[\\/]|[A-Za-z]:[\\/]|\\\\|\/)[^\s'"`)\]>]*/g

/** PATH_PLACEHOLDER is what an absolute path in a message becomes. It names
 * *that* a path was there, which is what a reader needs, and not which one. */
const PATH_PLACEHOLDER = '<path>'

function replacePath(_match: string, url: string | undefined, anchor: string | undefined): string {
  if (url !== undefined) return /^file:/i.test(url) ? PATH_PLACEHOLDER : url
  return `${anchor ?? ''}${PATH_PLACEHOLDER}`
}

/** INVISIBLE_RE is the shared set of invisible characters
 * (`src/shared/sanitize.ts`), the ones `hiddenCharacterReason` refuses in an
 * argument -- bidi overrides and isolates, zero-width characters, the BOM, the
 * soft hyphen, the variation selectors, the interlinear annotation characters
 * -- which a refusal drops for the same reason: a right-to-left override makes
 * a screen show a sentence other than the one that was sent. */
const INVISIBLE_RE = invisibleRe()

/** LONE_SURROGATE_RE is half a character: a high surrogate with no low one
 * after it, or a low one with no high one before it. */
const LONE_SURROGATE_RE = /[\ud800-\udbff](?![\udc00-\udfff])|(?<![\ud800-\udbff])[\udc00-\udfff]/g

/**
 * boundReason is the one place a handler-authored message is made safe to put in
 * front of a user: control characters out, invisible format characters out,
 * half-characters replaced, absolute paths out, one line, and a hard length cut
 * on a character boundary. It never returns an empty string -- a refusal with
 * no text is a screen with nothing to draw -- and it never echoes more of the
 * message than the bound allows.
 */
function boundReason(message: string): string {
  // Every pass below is linear, and the input is first cut to a few times the
  // bound so that a megabyte-long message costs what a short one does: nothing
  // past this point can reach the reply anyway.
  const flattened = message
    .slice(0, MAX_REPLY_REASON_LENGTH * 4)
    .replace(/[\u0000-\u001f\u007f-\u009f]+/g, ' ')
    .replace(INVISIBLE_RE, '')
    .replace(LONE_SURROGATE_RE, '\ufffd')
    .replace(/\s+/g, ' ')
    .trim()
  if (flattened === '') return 'the operation failed'
  const redacted = flattened.replace(ABSOLUTE_PATH_RE, replacePath)
  if (redacted.length <= MAX_REPLY_REASON_LENGTH) return redacted
  // The cut is marked, so a reader can tell a truncated sentence from a whole
  // one rather than wondering what the CLI meant to say. It is in UTF-16 code
  // units, like the bound, and it steps back one when it would land between
  // the two halves of a surrogate pair: half an emoji is not text.
  let cut = MAX_REPLY_REASON_LENGTH - 1
  const last = redacted.charCodeAt(cut - 1)
  if (last >= 0xd800 && last <= 0xdbff) cut -= 1
  return `${redacted.slice(0, cut).trimEnd()}…`
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
    // The gate is `isSenderOnDocument`, not `isConsoleSender`: the origin is not
    // enough now that the picker is served from it too (Task 3.4b). Every
    // channel under the `fugaro:console/` prefix answers a frame on the dashboard and nothing
    // else, and the picker's own channels are gated the same way in
    // `src/main/picker.ts` -- so neither document can reach the other's surface.
    ipcMain.handle(channelForOperation(operation.name), (event: ConsoleEventLike, raw: unknown) =>
      isSenderOnDocument(event, APP_ENTRY_URL) ? dispatch(operation.name, handler, raw) : { ok: false, reason: SENDER_REFUSAL },
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

// ---------------------------------------------------------------------------
// Reduced mode, at the handler (desktop-console plan Task 3.4a, step 3; design
// section 5.1 rule 4, C10).
// ---------------------------------------------------------------------------

/**
 * NO_ACTIVE_INSTALLATION is the console's own sentence for the one state the
 * dashboard can be in with nothing behind it: **no installation is active**, so
 * there is no CLI for the console to read and no source to read it from (design
 * D13, the installation picker of Task 3.4b).
 *
 * It is a constant of this file, and not a sentence the entry point invents,
 * because the renderer's `degraded` screen draws a refusal's *category* and the
 * console's own sentences are the ones it knows how to draw. Until an
 * installation is chosen in the picker (Task 3.4b) every read of the CLI is
 * answered with this, which is the truth rather than a stub: this console really
 * is driving nothing. It is also the sentence `installation.current` carries in
 * D13's no-installation state (`src/main/installations.ts` re-exports it), so the
 * header and a refused read say the same thing about the same state -- one
 * constant, where 3.4a and 3.4b each wrote one in parallel.
 */
export const NO_ACTIVE_INSTALLATION = 'no installation has been chosen'

/**
 * REDUCED_VIEW_OPERATIONS is the closed mapping from a withheld view to the
 * operations that read it (plan step 3).
 *
 * It is the main process's half of design section 5.1 rule 4: the renderer
 * refuses to *draw* a withheld view (`hidesView`, `src/renderer/app.ts`), and
 * this is what refuses to *fetch* one. Neither half is the other's substitute --
 * a renderer bug cannot get a figure the main process will not send, and a main
 * process bug cannot put one on a screen the renderer will not draw -- and both
 * are decided from the same value, the compatibility the handshake produced.
 *
 * The mapping is derived from the operation table rather than from the screens:
 * a screen is written in the renderer and names no channel, so the only thing
 * that can say which operation reads spend is this table. Three consequences a
 * reviewer should check:
 *
 *   - **`spend` names both of the Spend screen's reads, and the totals read
 *     with them.** `runs.stats` (the by-person panel, console L5b) publishes
 *     per-person `counted_usd` and `notional_usd` -- the same spend figures the
 *     reduced mode withholds (D7), under another name -- so withholding
 *     `spend.report` while answering it would put the withheld dollars on
 *     screen through the by-person panel. `runs.summary` (console 0.119.0's
 *     jobs-free totals read) carries the same two quantities again, as
 *     `spent_micros` and `notional_micros` over the window and over each
 *     repository, so it is withheld with them for the same reason: a totals
 *     read answered in reduced mode is the withheld figure by another name;
 *   - **`caps` names the Projects tab's reads, and the settings read with
 *     `budget show`.** The tab's own `VIEWS` entry declares the view this list
 *     is the other half of (`src/renderer/entry.ts`, `{ id: 'projects', … view:
 *     'caps' }`), so the section is offered and drawn only where `budget.show`
 *     may be read. `settings.read` (the settings-edit plan's C1) carries the
 *     same kind of figure under a different name: the registry's `budget.*` and
 *     `agent.max_budget_usd` rows are the project's configured caps, and the
 *     reduced mode withholds caps -- a settings table answered here would put a
 *     limit on screen through the third section that the caps panel is refused.
 *     `policy.show` (the managed screens' plan's CM4) carries the project's own
 *     `budget` block -- `mode`, `per_run_usd`, `allowed_models`,
 *     `max_run_tokens` -- which is that same configured ceiling again, published
 *     rather than resolved, so the read is withheld with them: D1481 picked this
 *     command over `project show --json` to keep the **installation's** caps off
 *     this surface, and the node's own block is another document's cap rather
 *     than no cap at all. D1487 is not contradicted by it: what that decision
 *     settled is that these *views* add no entry of their own to `VIEWS` -- the
 *     tab's view is already `caps` (F9) -- while an operation's entry in this
 *     list is the main process's half of rule 4, a different list; the read
 *     reaches no screen in a reduced console either way, and the two halves stay
 *     two rather than one standing in for the other;
 *   - **`kill` is empty, and that is the answer rather than a gap.** There is no
 *     kill-switch operation in the surface: the controls live in the live feed's
 *     own fields (design C10, and `src/shared/version.ts`'s `HIDDEN_VIEW_WORDS`
 *     says so on the screen), so withholding `kill` withholds nothing *here*.
 *     `live.watch` is deliberately not in any of the three lists: a reduced
 *     console still draws its live view, with the kill-switch fields the feed
 *     carries. `test/unit/ipc.test.ts` asserts both by name, so a later task that
 *     adds a kill operation has to come here and say so;
 *   - **every operation not named here is reachable in reduced mode**, which is
 *     the direction that keeps this a list of three views and not a second,
 *     quieter refusal: a reduced console is a console, and what it withholds is
 *     exactly what the handshake said it could not vouch for.
 */
export const REDUCED_VIEW_OPERATIONS: Readonly<Record<ReducedView, readonly OperationName[]>> =
  Object.freeze({
    spend: Object.freeze(['spend.report', 'runs.stats', 'runs.summary'] as const),
    caps: Object.freeze(['budget.show', 'settings.read', 'policy.show'] as const),
    kill: Object.freeze([] as const),
  })

/**
 * REDUCED_REFUSAL is what a withheld view's operation answers: one fixed
 * sentence of the console's own, with nothing from the CLI in it.
 *
 * It is deliberately **not** the CLI's refusal. A source's refusal is the CLI's
 * sentence about a read that failed, and this is not a failed read: the console
 * did not ask, because the version it is driving is one whose spend figures it
 * cannot vouch for. A sentence that looked like the CLI's would tell the operator
 * to go and fix a project that is not broken.
 */
export const REDUCED_REFUSAL = 'this console withholds that view for the fugaro it is driving'

/** withheld throws the reduced mode's refusal. A handler refuses by throwing and
 * `dispatch` turns the throw into the reply, which is the one path every refusal
 * on this surface takes. */
function withheld(): never {
  throw new Error(REDUCED_REFUSAL)
}

/**
 * reducedModeHandlers wraps the whole surface so that a withheld view's
 * operations refuse **before the source is reached** (plan step 3).
 *
 * `current` is read per call and not captured once: the mode is the handshake's
 * answer, and a wrapper built over a copy of it would keep answering whatever the
 * copy said -- the same staleness `startupCompatibility`'s `read` exists to
 * avoid, and the reason both take a function.
 *
 * **`startup.compatibility` is not wrapped, and that is load-bearing.** It is
 * the operation every other one's gate is decided from -- the renderer asks it
 * first and draws nothing until it has the answer -- so a wrapper that refused it
 * in a refused console would leave the renderer with an unreadable answer and no
 * way to learn *why*: the refusal screen would say the console could not read its
 * own handshake. It is also the one operation that reads no view, which is why it
 * is in none of `REDUCED_VIEW_OPERATIONS`' three lists; the exception is written
 * out here, by name, so that it is a decision rather than an omission.
 *
 * The `refused` branch is belt-and-braces and it is deliberately kept: today a
 * refused console registers no handler at all (`src/main/index.ts`), so no
 * wrapped handler is ever reachable in that mode. It is here because that is a
 * property of the *wiring*, and a later task that registers the surface earlier
 * -- before the handshake, or on `activate` -- would otherwise make every read
 * reachable in a console that may draw nothing at all.
 */
export function reducedModeHandlers(
  handlers: ConsoleHandlers,
  current: () => CliCompatibility,
): ConsoleHandlers {
  const wrapped: Record<OperationName, ConsoleHandler> = { ...handlers }
  for (const operation of CONSOLE_OPERATIONS) {
    if (operation.name === 'startup.compatibility') continue
    const inner = handlers[operation.name]
    wrapped[operation.name] = async (args) => {
      const compatibility = current()
      if (compatibility.mode === 'refused') withheld()
      for (const view of REDUCED_VIEWS) {
        if (!REDUCED_VIEW_OPERATIONS[view].includes(operation.name)) continue
        if (hidesView(compatibility, view)) withheld()
      }
      return inner(args)
    }
  }
  return Object.freeze(wrapped)
}
