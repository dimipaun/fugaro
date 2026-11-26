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
// `init` tombstone and the hidden `exec`, the hidden `budget history` job
// command, `image git-credential`
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
import { validateExternalUrl } from './window'
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

/**
 * MODEL_ID_RE, MODEL_ROUTE_RE and MODEL_PRICE_RE are the models write's three
 * grammars (the console improvements plan's MW1, D2903), and every one of them
 * is checked in main **before any child exists**.
 *
 * **The model ID is the shape the plan states** -- `^[a-z0-9][a-z0-9._/-]{0,127}$`,
 * the same one an image name has -- because the ID is a word the CLI's own
 * `models add` reads as its positional and appends to a route's list: lower-case
 * ASCII, no whitespace, no leading dash and no `=` (so the word can never be
 * read as a flag even before the terminator in front of it does that work), and
 * bounded at 128 characters. It is deliberately **narrower than the CLI's own
 * check** (`config.CheckModelID`, which refuses an alias and little else): the
 * console may refuse a word the CLI would accept, and it must not spell one the
 * CLI would refuse -- which is the direction the whole surface is built in.
 *
 * **The route is a name and not a membership.** Which routes exist is the
 * project's own `providers` block, so main cannot check that the route is one of
 * them -- it checks the shape the config's own shared name rule gives every
 * provider name (lower-case letters, digits and `-`), and the CLI's own sentence
 * names the routes to choose from when the name is not one.
 *
 * **The price is a plain decimal and nothing else.** The flag is a
 * `Float64Var`, and a spelling like `1e-3`, `0x10` or `Infinity` is a value this
 * console has no reason to hand a child; what it sends is the digits an operator
 * typed, bounded to the range the price table carries (`internal/pricing`'s
 * `MaxPerM`, 1000 US dollars per million tokens) so a rate no budget can count is
 * refused here rather than written.
 */
export const MODEL_ID_RE = /^[a-z0-9][a-z0-9._/-]{0,127}$/
export const MODEL_ROUTE_RE = /^[a-z][a-z0-9-]{0,63}$/
export const MODEL_PRICE_RE = /^(?:0|[1-9][0-9]*)(?:\.[0-9]{1,6})?$/
/** MODEL_PRICE_MAX_LENGTH bounds a price argument in characters, checked before
 * the grammar runs so a hostile payload cannot make a regular expression work:
 * the longest value the range admits is `1000.000000`. */
export const MODEL_PRICE_MAX_LENGTH = 11
/** MAX_MODEL_PRICE is the highest rate the price table carries, in US dollars
 * per million tokens (`internal/pricing/pricing.go`'s `MaxPerM`). It is spelled
 * here rather than imported for `MAX_INSTALLATION_NAME_LENGTH`'s reason -- the
 * console imports nothing from the Go tree -- and a price at or below zero is
 * refused by the same check: a model billed at nothing is not a price. */
export const MAX_MODEL_PRICE = 1000

/**
 * IMAGE_NAME_RE, IMAGE_TAG_RE, IMAGE_TAGS_MAX and MAX_IMAGE_DESCRIPTION_LENGTH
 * are the image write's four grammars (the console improvements plan's MW2,
 * D2903), and every one of them is checked in main **before any child exists**.
 *
 * **The entry name is the CLI's own catalog-name rule** -- `ValidCatalogName`,
 * `internal/config/catalog.go:221`, which is `ProjectNameRE`: 1 to 40 of a-z,
 * 0-9 and `-`, starting and ending with a letter or a digit. It is deliberately
 * **narrower than the model ID's shape** (`MODEL_ID_RE` above, whose grammar the
 * plan gives the image name as well): a name reaches a tag of an image in the
 * shared registry and a key of the project's bucket, so the CLI refuses a `.`, a
 * `/` and a 41st character in one -- and a grammar here that admitted them would
 * be a console that spells words its own child then refuses.
 *
 * **A tag is the CLI's own tag rule and not a list of tags.** `catalogTagRE`
 * (`internal/config/catalog.go:236`) is the shape 1 to 64 of a-z, 0-9, `.`, `_`
 * and `-` beginning with a letter or a digit, and the plan's "tags from the CLI's
 * closed list" is a claim about a list that does not exist: a tag is free-form
 * (`docs/project-image-catalog/spec.md`, "a **free-form** list of what the image
 * provides"), and the words the flag's help names -- node, postgres, redis,
 * firebase-emulator, docker -- are examples rather than a set. So main checks the
 * shape, exactly as it checks a route's (`modelRoute`), and whether the tags are
 * the ones a repository needs is the selection's own rule, one process later.
 *
 * **The description is printable and bounded**, which is the CLI's rule for it
 * (`maxCatalogDescriptionLen`, 200) and the reason `hiddenCharacterReason` above
 * is the rest of it: a listing prints a description on one line, so a tab and a
 * newline are refused with every other control character, and a bidi override
 * with every other invisible one.
 */
export const IMAGE_NAME_RE = /^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$/
export const IMAGE_TAG_RE = /^[a-z0-9][a-z0-9._-]{0,63}$/
/** IMAGE_TAGS_MAX is how many tags one list may carry: the CLI's own
 * `maxCatalogTags` (`internal/config/catalog.go:141`). */
export const IMAGE_TAGS_MAX = 32
/** MAX_IMAGE_TAGS_LENGTH and MAX_IMAGE_DOCKERFILE_LENGTH are the console's own
 * bounds on those two arguments, and they are the same bound for the same
 * reason: each is handed to a child as **one argv element**, so what has to be
 * bounded is how large a word this process will make -- `MAX_ARGUMENT_LENGTH` in
 * `src/main/spawn.ts`. They are narrower than the CLI's own limits (a list may
 * be 32 tags of 64 characters there, a definition names a repository of up to
 * `MAX_REPOSITORY_LENGTH` and a path of up to 200), and narrow is the direction
 * this surface is built in: the console may refuse a word the CLI would take and
 * must never spell one it would not. */
export const MAX_IMAGE_TAGS_LENGTH = 200
export const MAX_IMAGE_DOCKERFILE_LENGTH = 200
/** MAX_IMAGE_DESCRIPTION_LENGTH bounds an entry's description, and it is the
 * CLI's own `maxCatalogDescriptionLen` (`internal/config/catalog.go:145`). */
export const MAX_IMAGE_DESCRIPTION_LENGTH = 200

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
 * RUNS_STREAM_SINCE_WINDOWS is the closed set of windows `runs ls --stream
 * --since` may be asked for from here, and **it is the console's own window
 * rather than the command's grammar**. `runs ls` reads `parseSince` (`runSince`,
 * `internal/cli/runs.go`), whose first branch is `0` -- no lower bound at all --
 * and a *live* read of every run an installation ever made, still arriving, is
 * the one window a feed must not be opened on. The window this console makes its
 * `runs ls` reads for is a day (`RUNS_SINCE`, `src/renderer/entry.ts`), so the
 * stream admits exactly that word: the feed and the whole-document listing its
 * screen falls back to cover the same window, and a renderer cannot widen one of
 * the two under another spelling of the same flag. The set is stated once, here,
 * and both the schema and the argv builder read it.
 */
export const RUNS_STREAM_SINCE_WINDOWS: readonly string[] = Object.freeze(['24h'])

/**
 * GRAPH_SCOPE_RE is the closed shape of a `graph.read` scope argument: the two
 * selector words, and an id-carrying scope whose id is **C-ORCH's orchestrator
 * id and nothing else** -- the project's own name rule (`config.ProjectNameRE`),
 * optionally a `~` (`orchestrator.NameSeparator`) and a name
 * (`^[a-z0-9][a-z0-9-]{0,39}$`). The id half is one rule, held by
 * `orchestrator.ValidScopeID` (`internal/orchestrator/naming.go`, F40), the same
 * rule `graph.ParseScope` reads: an id written twice here would drift from the
 * command's own, and an id this process admitted and that command refused would
 * be a child spawned to fail.
 *
 * **A named id is admitted by the grammar and by nothing else.** A scope whose
 * id carries a `~` reaches a child only from a CLI at or above
 * `ORCHESTRATOR_MAP_CLI_FLOOR` (`src/shared/version.ts`), because the named
 * scope grammar and `orchestrator map --json` are one release's; the floor is
 * asked at the source (`createBoundSource`, `src/source/cli.ts`), the one place
 * a scope and an installation's version are read together, and `graphScopeIsNamed`
 * is the one spelling of "this scope names a named orchestrator" both use.
 *
 * Anchored, so nothing before or after it gets in. A leading `-` (which would
 * reach argv as a flag, the plan's H1), a path separator, an uppercase letter,
 * a whitespace character, a `.`, an `@`, a trailing hyphen on the project half
 * and an over-long id are each refused here -- before a child exists -- and
 * `isSafeArgumentValue` refuses the leading `-` and the alphabet a second time
 * on the way into the argv.
 */
const GRAPH_SCOPE_RE =
  /^(?:all|coordinator|(?:orchestrator|local):[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?(?:~[a-z0-9][a-z0-9-]{0,39})?)$/

/**
 * MAX_GRAPH_SCOPE_LENGTH bounds a scope argument before the grammar runs, so a
 * hostile payload cannot make the regular expression work. It is the longest
 * value the pattern admits: `orchestrator:` plus `orchestrator.MaxOrchestratorIDLength`
 * (81: a project name at its 40-character cap, the `~`, and a name at its cap).
 */
export const MAX_GRAPH_SCOPE_LENGTH = 'orchestrator:'.length + 81

/**
 * graphScopeIsNamed reports whether a well-formed scope names a **named**
 * orchestrator -- whether its id half carries the separator. It is one spelling
 * for the one reader that has to tell the two apart: `createBoundSource`'s scope
 * guard (`src/source/cli.ts`), which admits a named scope only at or above
 * `ORCHESTRATOR_MAP_CLI_FLOOR`. It takes a scope `GRAPH_SCOPE_RE` already
 * admitted and looks for the separator the pattern allows in one place only, so
 * it is a reading and not a second grammar.
 */
export function graphScopeIsNamed(scope: string): boolean {
  return scope.includes('~')
}

/**
 * ROADMAP_NODE_RE is the shape of the **document key** a move names: the
 * `<source>:<id>` a graph document's `nodes[].key` is (contract §6.22), which is
 * the identity the drawing carries on every row and the one value a renderer
 * may name a task of the record by.
 *
 * It is a *shape* and not an identity: no pattern could say whether a node of
 * that spelling exists, and this one deliberately does not try -- the id of a
 * local scope's own key carries a colon of its own (`local:o1:t-042`), so a
 * grammar that took a key apart would refuse a key the document wrote. Whether
 * the node exists is the **source's** question, asked of the last document this
 * installation read (`createBoundSource`'s move, the graph read's own F21 rule):
 * a key no answer listed is refused before a child exists and before any word of
 * an argv is built.
 *
 * What the pattern is for is the other half: a key that could never be one, so a
 * payload cannot make this process carry a path, a newline or a megabyte into a
 * refusal sentence. It is `isSafeArgumentValue`'s alphabet (`src/main/spawn.ts`)
 * **and that function's own length bound** (`MAX_ARGUMENT_LENGTH`), even though
 * no node key ever reaches argv -- the value a node key reaches is the
 * document's own `id`, and that one is emitted from main-process state and held
 * to the same bound by `assertMoveWord`. A key this schema admits and that bound
 * would not is a task a person could select and the console would then refuse to
 * spell, with a sentence about an alphabet the key spells perfectly well; so the
 * two are one number, and a key longer than it is refused here, by name and
 * length.
 */
const ROADMAP_NODE_RE = /^[A-Za-z0-9._:@/+-]{1,200}$/

/** MAX_ROADMAP_NODE_LENGTH is the length `ROADMAP_NODE_RE` admits, read out
 * here so the check that runs before the grammar and the pattern it feeds cannot
 * disagree about where a key ends. It is `spawn.ts`'s `MAX_ARGUMENT_LENGTH` for
 * the reason above, and `ipc.test.ts` holds the two together: the longest key
 * this schema admits is the longest value that guard passes. */
const MAX_ROADMAP_NODE_LENGTH = 200

/**
 * ROADMAP_RELEASE_RE is the shape of the release a move names: **the record's
 * own key for a release box**, or the empty string for "the record holds no
 * assignment for this task" (D2114's `none`).
 *
 * The key is opaque: `coordinator.ReleaseID` builds it as a hash (`ee/graph`),
 * so this is the only thing a renderer's value can be checked against here --
 * and, as above, it is not an identity. Membership is the source's, taken from
 * the `releases[]` of the document this installation last read.
 *
 * **The empty string is the unassign and not an absent argument.** A missing
 * `toRelease` is refused by the schema (the field is required), so the two
 * readings cannot be confused, and the word the empty string becomes is the
 * CLI's own `none` -- spelled by the main process (`spawn.ts`), never by a
 * renderer.
 */
const ROADMAP_RELEASE_RE = /^(?:[0-9a-f]{64})?$/

/** MAX_ROADMAP_RELEASE_LENGTH is the length of one release key: the record
 * builds it as a sha-256 hex digest (`coordinator.ReleaseID`), so 64 is the
 * pattern's own bound and, in `kindLimit`, the bound the check runs first. */
const MAX_ROADMAP_RELEASE_LENGTH = 64

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
 *   - `runsStreamSince` -- `runs ls --stream --since`'s window: one of
 *     `RUNS_STREAM_SINCE_WINDOWS` and nothing else, which is the console's own
 *     day rather than `parseSince`'s grammar (`runSince`). It is closed for
 *     `summarySince`'s reason and one more the live read adds: `parseSince`'s
 *     `0` is no lower bound at all, and on a feed that is every run the
 *     installation ever made, still arriving. It is a kind of its own rather
 *     than a reuse of `runSince` for that reason *and*, as `summarySince`'s is,
 *     for the position: this row emits the space form `--since <window>`, the
 *     one `runs ls`'s whole-document row emits, so a stream and the listing its
 *     screen falls back to ask for one window one way;
 *   - `reportDimension` -- `report --by`'s closed set of eight words;
 *   - `secretScope` -- `secrets ls`'s two mutually exclusive scopes;
 *   - `pullRequest` -- a pull request number, a positive integer;
 *   - `flag` -- a boolean, for the flags the allow-list admits;
 *   - `link` -- the one URL, and it must pass `validateExternalUrl`;
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
 * **`graphSource` is the graph read's one argument** (the graph plan's CG9a;
 * its named ids arrived with the console-fixes plan's CG7a): the scope the
 * document is about, held to `GRAPH_SCOPE_RE` -- C-ORCH's own id rule, the one
 * `graph.ParseScope` reads -- and, at the source, to the set a previous read of
 * the same document listed (F21). It is the one kind here whose *shape* is a
 * closed grammar and whose *membership* is the read's own answer, which is why
 * the two checks live apart: this module refuses a spelling no scope could have,
 * and `createBoundSource` refuses an id the CLI never named -- and refuses a
 * **named** id below `ORCHESTRATOR_MAP_CLI_FLOOR`, the release whose CLI reads
 * one at all.
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
  | 'runsStreamSince'
  | 'reportDimension'
  | 'secretScope'
  | 'pullRequest'
  | 'flag'
  | 'link'
  | 'installationName'
  | 'installationCheckout'
  | 'launchTask'
  | 'launchTitle'
  | 'launchPlanTask'
  | 'settingOp'
  | 'settingKey'
  | 'settingValue'
  | 'graphSource'
  | 'roadmapNode'
  | 'roadmapRelease'
  | 'setupAnswers'
  | 'licenceKey'
  // The models write's three (the console improvements plan's MW1, D2903): the
  // model ID the command reads as its positional, the route that claims it, and
  // the two rates a model the built-in catalog does not price needs.
  | 'modelId'
  | 'modelRoute'
  | 'modelPrice'
  // The image write's four (the same plan's MW2, D2903): the entry's name, the
  // positional the command reads, the `owner/name:path` definition it is built
  // from, the tags it provides and the description a listing prints.
  | 'imageName'
  | 'imageDockerfile'
  | 'imageTags'
  | 'imageDescription'

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
  /**
   * `refusedAfterLapse` is the field that makes an operation part of the
   * **refused class** of the design's lapse state (subscriptions plan S6.2;
   * `docs/lapse-read-only/spec.md` §3.2, D436, T12): after the key's grace
   * period this console does not launch a run, does not change a setting and
   * does not move a task of a record, and `lapseHandlers`
   * (`src/main/licence-state.ts`) refuses exactly the rows that carry this
   * field, before the handler -- and so before any child -- is reached.
   *
   * **It is a field of the row and never a list of names beside the table.** The
   * design states the rule and its reason: a later cancel is admitted to the kept
   * set by its own row differing from the launch's in this one field, so a gate
   * that classified by name would refuse a cancel the day it was added. The rows
   * carrying it are the ones that spawn a command and change something --
   * `run.launch`, `settings.edit`, `roadmap.move`, `setup.apply` and `models.add`
   * -- and one row that spawns nothing **as itself** because it drives another
   * one of them: `settings.editor.open` is the settings write's window, and it
   * names the row whose plan its every child comes from (`drivesOperation`
   * below). `assertReachableSurface` refuses the column on every other row that
   * spawns no command, because those rows are the console's own answers (the
   * design's kept set, and the key screen §5.6 keeps ungated as the way out).
   *
   * Every other operation is the design's kept set: every read, including the
   * paid views, and the kill switch's state.
   */
  readonly refusedAfterLapse?: true
  /**
   * `installationArgument` names the payload argument that carries the
   * installation this row acts on, for a row whose target is not the console's
   * own active binding. `run.launch` is the one row with it today: its payload
   * names the installation to launch, and `src/main/launch.ts` resolves that name
   * against this console's records and spawns **that** installation -- handing its
   * child that installation's key, not the binding's.
   *
   * **It is a column for `refusedAfterLapse`'s reason, and it is what keeps the
   * lapse gate honest about which installation it is asking about.** The gate
   * (`src/main/licence-state.ts`) reads the licence state of the installation an
   * operation acts on, and the row -- not the entry point -- says where that
   * installation is spelled: a gate that read the active binding for every row
   * would admit a launch of a lapsed installation while the console was driving a
   * valid one, spawn it, and hand it the lapsed installation's key. A row that
   * acts on the active installation carries no such column, because its payload
   * names no installation at all (`settings.edit` writes the settings document of
   * the installation the console is driving), and `undefined` is then the honest
   * answer to "which installation is this for".
   *
   * `assertReachableSurface` holds the column to a **required `installationName`
   * argument of the row itself**, so the one failure that would be silent --
   * pointing it at a free-text field, whose value no installation answers for,
   * which would read as the no-key state and admit the operation -- fails closed
   * at registration instead.
   */
  readonly installationArgument?: string
  /**
   * `drivesOperation` names the operation whose command a row spawns while
   * fixing no command of its own. `settings.editor.open` is the one row with it:
   * that operation is the settings write's window (SU1, D2902), its `command` is
   * `null` because no payload of it becomes an argv here, and every child it
   * spawns is one phase of `settings.edit`'s own plan, built and spawned through
   * the source (`src/main/settingsedit.ts`, `createSettingsEditor`).
   *
   * **It is a column for `refusedAfterLapse`'s reason, and it is what keeps the
   * lapse's class honest about a write reached through a window.** The class is
   * the operations that take a capability away when the key has lapsed, and a
   * window that drives one of them is one of them: a lapse that refused the write
   * and kept the window would leave the Project tab's pencil on screen with a
   * working write behind it, which is exactly what the design's T12 refuses
   * (`docs/lapse-read-only/spec.md` §3.2).
   *
   * `assertReachableSurface` holds it: the name must be a row of this same
   * table, and it must itself be in the refused class and spawn a command --
   * which is what admits a row that spawns no command to that class at all.
   *
   * **It is a `string` and not an `OperationName`**, for the reason
   * `installationArgument` is one: `OperationName` is derived from this same
   * table (`(typeof CONSOLE_OPERATIONS)[number]['name']`), so a column typed
   * with it would make the table's own type depend on itself. The name is
   * checked at registration instead, which is where a name no row answers has to
   * fail.
   */
  readonly drivesOperation?: string
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
 * CONSOLE_OPERATIONS is the whole surface. Fifty-three operations, and the table
 * is the reviewable artifact: a new read is a new entry here, with its argument
 * schema, and an entry is the only way anything becomes reachable. **The
 * console-fixes plan's I-ORCH removed four** -- `link.openPullRequest`,
 * `coordinator.status`, `coordinator.show` and `coordinator.intents`, none of
 * which any screen reaches since the Coordinator tab became the map -- and an
 * operation removed from here takes its spawn row, its preload row and its
 * bridge method with it: a surface left half in place is one every reader still
 * sees.
 *
 * **The two enrolment reads are this surface's forty-first and forty-second
 * operations** (the repos-management plan's RE5, D2413): `repos.ls` and
 * `repos.status`, the `project repos` group's two read verbs, added before any
 * screen draws them so that the console's reach over the group is settled by
 * the design rather than by the first screen that wants it. The group's writes
 * -- `repos add` and `repos rm` -- are on the never table and stay there; their
 * own rows below say what the pair may and may not be asked.
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
 * `console/docs/json-contract.md` §6.18 describes its document). It is one of
 * the two argvs here whose **group** the console may otherwise not touch -- the
 * other is `user ls --json` below (the roles-and-scopes plan's RS7), whose
 * group's five words are on the never table the same way: `fugaro models`
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
 * no CLI command can be asked for). **`link.openExternal` is the one operation
 * that is no command at all, and it is the whole way out of the console**:
 * it is handed a URL, which goes through `validateExternalUrl` -- `https:`
 * only, no credentials, no port, host on `ALLOWED_EXTERNAL_HOSTS` -- so the
 * renderer can open a link the console would have opened anyway.
 * `installation.current` is the dashboard's
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
  // schema: the command resolves the view itself -- an admin of the
  // installation (the identity named in the local config's `user:`/git, listed
  // in its `terraform.admins`) sees every user's rows, and everyone else sees
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
  // **`all` and `noMerges`, and nothing else.** `--all` is the screen's own
  // control: it lists the closed tasks (merged and cancelled) that the read
  // otherwise counts per release. `--no-merges` is the Graph tab's first paint
  // (IR4 of the console-fixes plan, D3217 as amended): the read spawns no `gh`
  // and answers `merges_read: false`, so the canvas does not wait for up to
  // thirty-two forge lookups -- and it is held behind
  // `PLAN_STATUS_NO_MERGES_CLI_FLOOR` (`src/shared/version.ts`), which no
  // release names yet, so the source refuses the argument below it rather than
  // handing an older CLI a flag it answers `unknown flag` for. Both are
  // booleans and never strings, so a payload cannot spell a flag with either,
  // and the cloud flags (`--project`, `--config`) are the installation's,
  // exactly as `runs ls` carries them -- never `--gcp-project` or `--region`,
  // which are no part of this surface. There is no positional argument:
  // `newPlanStatusCmd` is cobra's `NoArgs`.
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
        why: '`newPlanStatusCmd` has this flag, and it is the screen’s own control: it lists the tasks the read otherwise counts per release, which is narrower than the count the screen already has. The design’s §5.1 table has carried a row for the command since IR4.',
      },
      {
        key: 'noMerges',
        kind: 'flag',
        required: false,
        why: '`newPlanStatusCmd` has this flag (IR2 of the indexed-reads plan), and the Graph tab’s first paint is its one caller: the canvas is drawn from the read that spawns no `gh`, and the plain read follows as its own section to decorate the merged nodes. Below `PLAN_STATUS_NO_MERGES_CLI_FLOOR` the argument reaches no child at all.',
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
  // `orchestrator:../..`, a project half over 40 characters) before a child
  // exists, and `createBoundSource` refuses a scope the previous document of
  // this read did not list (F21) -- a value a renderer invented, or one that
  // names a scope the CLI's own sources array never mentioned, is a refusal and
  // not a spawn. **A named id is the same check's second half** (CG7a): the
  // grammar admits `orchestrator:<project>~<name>`, and the source refuses it
  // below `ORCHESTRATOR_MAP_CLI_FLOOR`, because the CLI that reads a named
  // orchestrator is the release that ships `orchestrator map --json`.
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
        why: '`newGraphCmd` has this flag and its set is closed: `all` (the command’s own default, the union of the scopes that exist), `coordinator` (the stored view), and `orchestrator:<id>` / `local:<id>`, which read one orchestrator’s own state node (CG4 and CG5). An id no scope of that name exists for is a refusal and not an empty graph: the database holds no node for it, and for `local:` this machine holds no cached brief either. The pattern is the CLI’s own (`graph.ParseScope`), so a value this surface admits is a value that command reads. It is optional because the command has a default of its own (`all`), so a caller that names no scope is answered rather than refused.',
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
        why: 'the scope the stream is about, on `graph.read`’s own terms: `all` (the command’s default, the union of the scopes that exist), `coordinator`, or `orchestrator:<id>` / `local:<id>`, which read one orchestrator’s own state node (CG4 and CG5). It is the same closed grammar the read above is held to (`GRAPH_SCOPE_RE`) and, at the source, the same membership rule: a scope no previous document of this installation listed is refused before a child exists (F21), so a renderer cannot hold a stream open on a scope that exists nowhere. It is optional because the command has a default of its own, exactly as the read’s is.',
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
    // The lapse takes this row away and keeps every read (design §3.2 rule 1,
    // T12): a lapsed installation is read-only, and the launch is half of what
    // read-only means.
    refusedAfterLapse: true,
    // **And the installation the lapse is about is this payload's own.** The
    // launcher resolves the name below against this console's records and spawns
    // *that* installation (`launch.ts`), handing its child that installation's
    // key -- so the gate has to ask about the name the renderer sent, not about
    // the binding the console happens to be driving. `settings.edit` and
    // `roadmap.move`, the lapse's other rows, carry no such column: each acts on
    // the installation the console is already driving, which its payload does not
    // name.
    installationArgument: 'installation',
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
  // confirmed write's carries `--no-apply` (D1565, D1572) -- which is not a set
  // of flags this operation *fixes*, so it is the phase the main process decides
  // (`SETTINGS_EDIT_PHASE_WORDS`, `src/main/spawn.ts`) rather than a column
  // here. **The write's word is held behind a floor as well**
  // (`NO_APPLY_CLI_FLOOR`, `src/shared/version.ts`), because no released CLI
  // registers it yet: no child gets it today, and a renderer has no part in that
  // either. A screen chooses none of them: not `--dry-run`, not even `--json`.
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
  // **What this operation does not have**: no `--repo` **in a payload** (the
  // argv carries one for a `repo`-layer row, and it is the plan's own directory
  // rather than a renderer's: RC80, L12, and the row's `repoFlag` in
  // `spawn.ts`), no `--add`/`--remove` (a list edit is a form no control can
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
    // One of the lapse's rows: T12 takes the launch and the configuration change
    // away (design §3.2 rule 1), and the record's move joined them on 2026-11-12
    // (`roadmap.move` below, the console's third write). `settings.read` -- the
    // same command, as a read -- is not this row and keeps answering, which is
    // the design's line between reading a setting and writing one.
    refusedAfterLapse: true,
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
  // The orchestrator map (the console-fixes plan's CG7a; C-ORCH's document,
  // `internal/orchestrator/mapdoc.go`; D3221): `fugaro orchestrator map --json`,
  // the project's **active** orchestrators, the connections between them and the
  // coordinator's own heartbeat. It is the read the Coordinator tab draws, and
  // it sits beside the stored view and the declarations above because it is the
  // same screen's third read -- and the one that replaces the tab's v2 sections
  // (CG7b, D3210).
  //
  // **It has no argument at all**, like the declarations read: the whole argv is
  // the row's own words (`src/main/spawn.ts`), so there is no field a renderer
  // could fill and nothing it could steer. `--json` is fixed rather than a
  // control, and `--project`/`--config` come from the row's `takesProject` like
  // every other project read (rule 3). The document is one project's, so the
  // scope a screen is looking at is not an argument of this read: the map is
  // already the project's active set.
  //
  // **The command is registered from `ee/`** (`ee/orchestrator/cli/map.go`) and
  // no released CLI carries it, so `ORCHESTRATOR_MAP_CLI_FLOOR` is `null` today
  // (`src/shared/version.ts`) and the tab draws the version it needs rather than
  // spawning this row at all. The two ends of that floor are one release: the
  // command, and the named graph scope beside it (`GRAPH_SCOPE_RE`, F40).
  //
  // Like the stored view, this read asks nothing of the host: the map is built
  // from the caller's own database -- the orchestrator nodes, their leases, the
  // claims and the coordinator's heartbeat (`roles/firebasedatabase.viewer`) --
  // so no App key is read, no installation token is minted and no provider is
  // opened.
  {
    name: 'orchestrator.map',
    method: 'orchestratorMap',
    command: 'orchestrator map',
    flags: ['--json'],
    summary: 'the project’s active orchestrators, their connections and the coordinator’s heartbeat',
    args: [],
  },
  // The roadmap record's one write (the dependency-graph plan's GV3.2; the
  // console's third write, after rule 11's launch and rule 12's settings edit):
  // `fugaro coordinator roadmap move <plan>/<task> --to <release|none> --json`,
  // the call that puts a task of the record into a release box or takes it out
  // of one.
  //
  // **A renderer names two keys and no word of the command line.** `node` is the
  // document key of the card that was dragged (`coordinator:plans/x/T1`) and
  // `toRelease` is the record's own key for the box it was dropped on, or the
  // empty string for "no release" -- both **ids the last graph read printed**,
  // and both checked against that document in the source before anything is
  // built (`createBoundSource`'s move, on F21's terms). The three words the CLI
  // reads are the main process's own: the task word is the document's `id` for
  // the node that key names, `--to` is the key the box was named by (or the
  // CLI's `none`), and `--expect` is the `release` the same document recorded
  // for that node -- the compare-and-set's whole safety (GV3.1), which a
  // renderer must not be able to state.
  //
  // **`confirmed` is the one argument of this table that reaches no word of any
  // argv** (`src/main/spawn.ts`'s row declares it with `flag: null`, exactly as
  // `graph.watch`'s `visible` is declared), and it is what makes one operation
  // do the two halves of a confirmed write: without it the answer is the move's
  // **preview** -- the exact argv, the task and the two boxes, and no child --
  // and with it the child runs, refused if the argv the preview drew is not the
  // one this call builds (the record moved under the confirmation). The
  // preview is the same operation rather than a second one because the two
  // answers must be the same words: a table where a screen asked one row for the
  // command line and another to run it could be shown one argv and spawn
  // another, and no test could tell.
  {
    name: 'roadmap.move',
    method: 'moveRoadmapTask',
    command: 'coordinator roadmap move',
    flags: ['--json'],
    summary: 'one task of the record into a release, or out of one',
    refusedAfterLapse: true,
    args: [
      {
        key: 'node',
        kind: 'roadmapNode',
        required: true,
        why: 'the document key of the task the operator dragged: the `nodes[].key` of the graph document this installation last read, which is what the drawing puts on the row (`data-key`, `src/renderer/screens/graph.ts`). It is an **id and not a word**: the positional the CLI reads is the document’s own `id` for the node this key names, emitted by the main process out of the document it checked the key against, so a renderer cannot name a task the record does not hold and cannot spell one either. The kind’s own pattern keeps a payload from carrying something no key could be.',
      },
      {
        key: 'toRelease',
        kind: 'roadmapRelease',
        required: true,
        why: 'the record’s key for the release box the task was dropped on -- a `releases[].key` of the same document -- or the empty string for the unassign, which is the operator dropping a task out of every release (D2114). It is required rather than optional because the two readings must not be confusable: an absent argument would be "the caller said nothing", and the empty string is "the record holds no assignment for this task", which is a value a caller who read the document may state. The word behind `--to` is the main process’s spelling of it (`none` for the empty string), so the record’s own word never comes from a renderer.',
      },
      {
        key: 'confirmed',
        kind: 'flag',
        required: false,
        why: '**the one argument on this surface that reaches no word of any argv**: it decides whether the child runs at all, which is what makes this operation answer a preview and a write both. A caller that leaves it out (or sends `false`) is answered the move’s own command line, the task and the two boxes, and no child is spawned; `true` runs it, and only if the argv this process builds is the argv the preview drew. It is the launch’s `visible` argument read the other way round -- there `false` stops a feed, here `true` is the write -- and it is `false` by default for the same reason Cancel is the focused button of a confirmation: the state a caller reaches by forgetting is the one that writes nothing.',
      },
    ],
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
  // `--gcp-project`, no `--region`. The `project` group's writing words --
  // `policy publish`, `repos add`, `repos rm` and `sync` -- are each on this
  // file's never table; the group's two enrolment reads are the rows below.
  {
    name: 'policy.show',
    method: 'policyShow',
    command: 'project policy show',
    flags: ['--json'],
    summary: 'the published policy node the project’s runs read, and nothing else',
    args: [],
  },
  // **The project's repositories** (the repos-management plan's RE5, D2413):
  // `fugaro project repos ls --json` and `fugaro project repos status --json`,
  // the group's two read verbs and the whole of what the console may ask it.
  // The group's writes stay where they were: `project repos add` and
  // `project repos rm` are on the never table below, and no code in this tree
  // constructs a word of either argv -- the two rows here are the group's reach
  // and nothing more.
  //
  // **No screen consumes the documents yet, and the reach still carries its
  // whole middle.** A row here is a member of `OperationName`, and
  // `ConsoleHandlers` maps that name to a handler one for one -- so an operation
  // listed with no handler is a console that does not type-check and, were that
  // suppressed, a `registerConsoleHandlers` that refuses to register the whole
  // surface. Both are answered: `src/source/source.ts`'s `listRepos` and
  // `reposStatus` read the two commands through `CliSource`
  // (`src/source/cli.ts`) and over the recording `FixtureSource`
  // (`src/source/fixture.ts`), and the e2e stub answers their argv. What is
  // still ahead is the screen that draws them (D2413: the reach lands before
  // the screen does).
  //
  // **Neither takes an argument**, and `repos.status`'s optional positional
  // repository is deliberately not one: the command answers for every
  // repository this project knows when it names none, which is the read a
  // screen would draw, and a caller that could fill the word could aim the read
  // at a repository the installation does not have. Both are reads in the
  // strongest sense: the CLI writes nothing on any path, `--json` included, and
  // reads a credential's presence and never its value (`internal/cli/
  // project_status.go`'s own comment), so both are on `consoleReadPaths` and
  // neither remembers a checkout.
  {
    name: 'repos.ls',
    method: 'listRepos',
    command: 'project repos ls',
    flags: ['--json'],
    summary: 'the project’s repositories as data: the allow-list, this machine’s copies, and what each one runs',
    args: [],
  },
  {
    name: 'repos.status',
    method: 'reposStatus',
    command: 'project repos status',
    flags: ['--json'],
    summary: 'each repository’s enrolment as a doctor: one row per fact that decides whether a run of it can start, each failure naming its fix',
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
  // (`docs/lapse-read-only/spec.md`): a lapsed installation is the one state
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
  // The Project tab's seat estimate (subscriptions plan S4.2; the read behind
  // it is `fugaro seats --json`, S4.1, and the rulings are D439, D433 and
  // T14). It is the console's **thirty-eighth** operation, and it is a read of
  // core data and nothing else: the command reads this machine's own launcher
  // and operator lists and the requesters of the last 90 days of runs, sends
  // nothing, stores nothing and consults no key, licence or server.
  //
  // **It is not on a reduced view.** The reduced mode withholds the screens
  // that read money an operator's runs spent (`REDUCED_VIEW_OPERATIONS`), and
  // this document carries no money at all: a seat is a contract pricing rule of
  // the Subscription Terms and not a figure of this installation's spend, and
  // the estimate the CLI publishes is a count of people. It is not gated by a
  // lapse either, and for the same reason a read is never gated: only the three
  // writes that walk an installation out of a lapse carry
  // `refusedAfterLapse` (`docs/lapse-read-only/spec.md`).
  //
  // **No argument at all**, the strongest form of this rule's closed schema:
  // the command takes no positional word, `--json` is a fixed word of the spawn
  // row and the cloud group's `--project`/`--config` are the installation's --
  // so there is no field a renderer could fill. The window is the ruling's own
  // 90 days and no flag moves it: an estimate over a window the caller chose
  // would be a different count wearing the same name.
  {
    name: 'seats.read',
    method: 'readSeats',
    command: 'seats',
    flags: ['--json'],
    summary: 'the advisory estimate of who launches jobs on this installation, and the identities the estimate cannot count',
    args: [],
  },
  // The Members view's read (the roles-and-scopes plan's RS7, roles.md D691(b):
  // a read-only Members view over the installation's grants node). It is the
  // console's **fortieth** operation, and the read behind it is RS5's
  // `fugaro user ls` (`internal/cli/user_ls.go`).
  //
  // **The `user` group's one reachable argv, and the group is why it is worth
  // saying.** `fugaro user` has six words; the other five change who may do
  // what -- `grant` and `revoke` write the installation's grants node, `add`
  // and `rm` rewrite this machine's own copy of the two IAM lists (F42), and
  // `offboard` drops every roster grant naming the member and has the intake
  // delete the secrets they stored -- so each of the five is on the never table
  // below, as a command and as a word of `user`, and `user ls --json` is the
  // whole of this group's reach. A role is the one thing this surface reads and
  // never moves.
  //
  // **It reads, and it is the console's strongest kind of read**: the command
  // reads this machine's own config and the installation's grants node with the
  // caller's own credential, prints one document and writes nothing -- no
  // cloud object, no cache, no remembered checkout (`consoleReadPaths`,
  // `internal/cli/project.go`). **No role logic lives in the renderer**: the
  // document's own words are what a screen draws, and the resolver that decides
  // what a role means is the CLI's.
  //
  // **Its one argument is the repository a role is asked about, and it is
  // optional.** Without it the document's `effective` list is empty, which is
  // the absence of a named repository rather than an empty roster -- the CLI's
  // own distinction, and the reason this argument is not required. The value is
  // the canonical `owner/name` the other repository arguments here are
  // (`task.CanonicalRepo`), because one repository has one name on this
  // surface; it is never a pattern. The scope of a grant **is** a pattern, and
  // no pattern is expressible here: a caller names a repository and the CLI
  // answers which roles cover it.
  {
    name: 'members.list',
    method: 'listMembers',
    command: 'user ls',
    flags: ['--json'],
    summary: 'the installation’s roster: every stored role and scope pair, the repositories a pattern matches here, and the role each person holds in one repository',
    args: [
      {
        key: 'repo',
        kind: 'repository',
        required: false,
        why: '`newUserLsCmd` has this flag, and it is what the view turns on: the role each person holds in one repository -- the highest role whose scope covers it, with the published policy’s `allow_repos` taken into account -- rather than the whole roster. It is optional because the command has a state of its own without it (the stored pairs, and the repositories of this machine a pattern matches), so a caller that names no repository is answered rather than refused. The grammar is this surface’s own `repository` kind, the canonical `owner/name` every other repository argument here is: a **pattern** is what a grant’s scope holds, never what this argument may hold, so the one word a caller fills here cannot widen what the read is about.',
      },
    ],
  },
  // The pending-steps read (the composite-commands plan's AP8, D1569): the
  // follow-up steps a management command saved for a later `fugaro apply`, the
  // change that asked for each of them, and whether a record is on disk and
  // still stamped against this machine's config. It is the console's
  // **forty-first** operation, and the read behind it is AP3's
  // `fugaro apply --status --json` (`internal/cli/apply.go`).
  //
  // **`--status` is a word of the command path and not a flag this row fixes**,
  // and that is the whole of its safety. `fugaro apply` is a **write** -- it is
  // the chain that runs the steps a management command saved -- and the bare
  // command is the one the console runs after such a command (D1572, the
  // design's own row for it); `--status` is the arm that runs no step, writes no
  // pending file, keeps no cache and remembers no checkout, which is what makes
  // this row a read the surface may carry at all. Spelling it as the `command`
  // rather than as a fixed flag is what keeps the two apart here: a row's
  // `command` is the argv's leading words (`assertSpawnSurface`), so
  // `apply --status` cannot be reached by a caller that dropped the word, and
  // `ALLOWED_OPERATION_FLAGS` -- which is the surface's whole flag vocabulary --
  // gains nothing for it.
  //
  // **It takes no argument at all**: the command is cobra's `NoArgs`, `--status`
  // and `--json` are words of its own row, and the two project flags are the
  // installation's (§3.4 rule 3), so there is no field a renderer could fill.
  // Every string in the document is the project's own text carried as data --
  // a step's name, the key or repository a change was about -- and no screen
  // interprets one.
  //
  // **It carries no spend and no cap**: a pending step is a state and not a
  // figure, so it is in none of `REDUCED_VIEW_OPERATIONS`' lists and a reduced
  // console answers it -- which is what a reduced console's operator needs,
  // since the steps a management command saved are precisely what a lapsed
  // installation has not applied yet. **No lapse refuses it either**
  // (`docs/lapse-read-only/spec.md`): the read writes nothing on this machine
  // and reaches no cloud object.
  {
    name: 'apply.pending',
    method: 'readPendingSteps',
    command: 'apply --status',
    flags: ['--json'],
    summary: 'the follow-up steps a management command saved for a later `fugaro apply`, the change that asked for each of them, and whether a record is on disk and still stamped against the config this machine now has',
    args: [],
  },
  // The console-improvements rows (AL, F24–F26). Nine operations, and every
  // one of them is **inert until used**: the argv is here and the handler is a
  // stub (`src/main/index.ts`), because the CLI release that carries each flag
  // is not the release in this tree. Each row's floor (`src/shared/version.ts`)
  // is a plain `string | null = null` until that release names one, so no child
  // is spawned for any of these today.
  //
  // **None of them has a flag the renderer can choose.** Every word beside the
  // command path is a fixed word of the row's `words` -- `--stream`, `--me`,
  // `--plan-only`, `--answers-file -`, `--events` -- and `flags` carries only
  // `--json`, which is the surface's own. That is the graph watch's shape read
  // across a set rather than a new rule: a mode word that makes a child
  // long-lived, or that decides what a child does, belongs in the command path
  // where nothing a payload says can turn it on or off.
  {
    // The runs list's live form (RS2, `runs ls --stream --json --since <w>`):
    // the same rows the whole-document read draws, one line at a time. The
    // window is the console's own closed set (`RUNS_STREAM_SINCE_WINDOWS`) --
    // the day every `runs ls` here is read for -- and never `parseSince`'s
    // grammar, whose `0` is every run the installation ever made arriving one
    // line at a time. It is **required**, because a stream that opened on the
    // command's default window would be a feed whose first question nobody
    // answered. The row's other columns --
    // `--repo`, `--mine`, `--pr`, `--all`, `--limit`, `--after` -- are absent,
    // so the stream is the window's own list and never a filtered one.
    name: 'runs.stream',
    method: 'streamRuns',
    command: 'runs ls --stream',
    flags: ['--json'],
    summary: 'the run list as it changes: one row per line, and a final line that ends the feed',
    args: [
      {
        key: 'since',
        kind: 'runsStreamSince',
        required: true,
        why: 'the window the feed covers: the console’s own day (`RUNS_STREAM_SINCE_WINDOWS`) and no other word. Required rather than defaulted: a live list whose window nobody chose is the read the console must not make on a screen’s behalf.',
      },
    ],
  },
  {
    // The Spend screen's live form (RS2): `spend.report`'s argv plus
    // `--stream`. The four declared arguments are the whole-document read's
    // own, unchanged and for the same reasons -- so a stream and a report
    // cannot narrow the same figure two ways.
    name: 'spend.stream',
    method: 'streamSpend',
    command: 'report --stream',
    flags: ['--json'],
    summary: 'the spend report as it changes: one line per line of the report, and a final line that ends the feed',
    args: [
      {
        key: 'by',
        kind: 'reportDimension',
        required: false,
        why: 'the breakdown’s dimension, `report`’s own closed set.',
      },
      {
        key: 'since',
        kind: 'reportSince',
        required: false,
        why: 'the range’s start, a day the grammar reads.',
      },
      {
        key: 'until',
        kind: 'reportSince',
        required: false,
        why: 'the range’s end, in the same grammar.',
      },
      {
        key: 'repo',
        kind: 'repository',
        required: false,
        why: 'the one repository the report is about.',
      },
    ],
  },
  {
    // The caps read's live form (RS2, RS4): `budget.show`'s argv plus
    // `--stream`. The three arguments are the whole-document read's own --
    // including `compute`, the opt-in that costs a listing of the runs bucket
    // -- so the stream carries exactly what the read carries.
    name: 'budget.stream',
    method: 'streamBudget',
    command: 'budget show --stream',
    flags: ['--json'],
    summary: 'caps, counters and the effective per-run cap as they change, one line at a time',
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
        why: 'the project-wide read, admitted on this command and refused together with `--repo` by `REFUSED_PAIRS`.',
      },
      {
        key: 'compute',
        kind: 'flag',
        required: false,
        why: 'the opt-in that also lists the runs bucket: refused with `--all`, as it is on the whole-document read.',
      },
    ],
  },
  {
    // The caller's own role (WH1, D2907): `user ls --me --json`. **The flag is
    // a word of the command path and not an argument**, because there is no
    // form of this read that is about anybody else: a payload that could turn
    // `--me` off would be a payload that asked the roster question, and the
    // roster is a screen this console does not have. It is not refused in an
    // agent session for `members.list`'s reason: it reads the caller's own
    // role, resolved by the same gates that answer the writes.
    name: 'whoami',
    method: 'whoAmI',
    command: 'user ls --me',
    flags: ['--json'],
    summary: 'the caller’s own role, the scope it is held at, and the repositories it reaches',
    args: [],
  },
  {
    // The image catalog (MO-C's read): `image ls --json`, the installation's
    // own image catalog. It is the group's one read and takes no argument at
    // all, and five of the group's writers are refused below -- `image add`,
    // `image rm` and `image promote` as commands and as words of `image`,
    // beside the `image build` and `image refresh` rows.
    name: 'image.catalog.ls',
    method: 'listImageCatalog',
    command: 'image ls',
    flags: ['--json'],
    summary: 'the installation’s image catalog: what each kind resolves to and how it was pinned',
    args: [],
  },
  {
    // The recipes (RB4/RS2's read): `recipes ls --json`, the recipes the
    // project may run. It takes no argument at all, and the group's two other
    // words are refused below: `recipes publish` as a command and as a word of
    // `recipes`, and `recipes validate` as a word, for the free `FILE`
    // positional a renderer would fill. Nothing a payload says reaches this
    // argv.
    name: 'recipes.ls',
    method: 'listRecipes',
    command: 'recipes ls',
    flags: ['--json'],
    summary: 'the recipes this project may run, with what each is for and what it needs',
    args: [],
  },
  {
    // The first-run wizard's plan (SW3a, D2904): `setup --plan-only
    // --answers-file - --events --json`. Three things about this row are the
    // whole of its safety.
    //
    // **The answers travel on stdin and never in a word.** `--answers-file -`
    // is a fixed word of the command path, so the only value the child can
    // read is the one main writes to its standard input -- there is no file for
    // another process of the same user to swap between main's check and the
    // CLI's read, and no argv word a path could be spelled into (the kind is
    // `stdin`, `spawn.ts`'s, the same one `run --task-file -` uses).
    //
    // **`--plan-only` is what makes this a read.** Without it the same command
    // creates the GCP project, the installation and a repository's wiring, so
    // the word in the command path is the difference the `setup` never-row
    // turns on -- and that row grants this operation by name (`except` below).
    //
    // **`--events` is what makes it a feed.** The plan's account arrives as
    // newline-delimited events and not as one document, which is why this
    // operation has the setup feed's deadline rather than `READ_TIMEOUT_MS`
    // (`SETUP_DEADLINE_MS`, `spawn.ts`).
    name: 'setup.plan',
    method: 'planSetup',
    command: 'setup --plan-only --answers-file - --events',
    flags: ['--json'],
    summary: 'what a first run would do, as a feed of events and with nothing created',
    args: [
      {
        key: 'answers',
        kind: 'setupAnswers',
        required: true,
        why: 'the wizard’s five answers, validated here key by key and serialised by this process into the document the child decodes: the payload carries an object and never a path, and `version` is main’s to write.',
      },
    ],
  },
  {
    // The first-run wizard's apply (SW3a, D2904): `setup --answers-file -
    // --events --json`, the same two words less `--plan-only`.
    //
    // **The payload is `{}` and that is deliberate.** The answers file this row
    // hands the child is the one main held when it showed the plan window, so a
    // renderer cannot slip a different project, region or rigor in between the
    // window and the write -- a second answers object on the wire would be a
    // second thing to validate and a second thing to disagree with the window.
    // There is no argument of this row at all.
    //
    // **It is one of the lapse's rows.** A converge is a write, so
    // `refusedAfterLapse` takes it away with the launch, the settings write and
    // the record's move (T12), and `setup.plan` -- the same command as a read --
    // is not this row and keeps answering.
    name: 'setup.apply',
    method: 'applySetup',
    command: 'setup --answers-file - --events',
    flags: ['--json'],
    summary: 'the write: converge the installation from the answers main held at plan time, as a feed of events',
    refusedAfterLapse: true,
    args: [],
  },
  {
    // The models write's plan (the console improvements plan's MW1, D2903):
    // `models add --no-apply --dry-run --json --route=<route> --price-in=<usd>
    // --price-out=<usd> --project <name> [--config <path>] -- <model id>`.
    //
    // **It is the CLI's own dry run and nothing else.** `--dry-run` is what
    // makes this row a read: the child computes the change and prints the
    // versioned document (`schemas/models-add-plan.schema.json`) -- the route's
    // claim, the `budget.allowed_models` entry, the `model_prices` entry with
    // both rates, the steps it would take and any refusal the write would print
    // -- and writes no config file, no published node and no saved step. The
    // window the operator confirms is drawn from that document, which is why
    // there is no second description of the change for the console to get wrong.
    //
    // **The payload is four values and every one is held to a grammar before any
    // child exists**: the model ID (`modelId`), the route (`modelRoute`) and the
    // two rates (`modelPrice`), each checked in main against a stated grammar --
    // and each emitted as **one word** of the argv, the rates and the route as
    // `--flag=value` and the ID as the positional behind the row's `--`
    // (`src/main/spawn.ts`), so no value a renderer sends can parse as a flag.
    // A half-given pair of rates is refused by the CLI's own sentence, which
    // names the flag that is missing: the two are one fact and main will not
    // invent the other half.
    //
    // **`--no-apply` is main's**, a word of the command path rather than an
    // argument: the steps that make the change live are saved for `fugaro apply`
    // (D1565), so this write changes the project config and the published
    // documents and runs nothing else.
    name: 'models.add.plan',
    method: 'planModelsAdd',
    command: 'models add --no-apply --dry-run',
    flags: ['--json'],
    summary: 'what claiming a model on a route would change, as the CLI’s own plan document and with nothing written',
    args: [
      {
        key: 'id',
        kind: 'modelId',
        required: true,
        why: 'the model ID `models add` reads as its positional word — the exact ID, never a pattern, and the ID a route claims',
      },
      {
        key: 'route',
        kind: 'modelRoute',
        required: true,
        why: 'the route that claims the model, named by shape rather than by membership: which routes exist is the project’s own `providers` block, and a name this project does not carry is refused by the CLI’s sentence, which lists the routes to choose from',
      },
      {
        key: 'priceIn',
        kind: 'modelPrice',
        required: false,
        why: 'the input rate, in US dollars per million tokens, for a model the built-in price catalog does not price — optional, and paired with `priceOut`: the CLI refuses a half-given pair',
      },
      {
        key: 'priceOut',
        kind: 'modelPrice',
        required: false,
        why: 'the output rate, the other half of the pair above',
      },
    ],
  },
  {
    // The models write (the same plan's MW1, D2903): the row above less
    // `--dry-run --json`, and the console's **fifth write** — after the launch,
    // the settings write, the record's move and the wizard's apply.
    //
    // **It is a widening, and that is the whole reason it is on this surface at
    // all.** The write appends the model to a route's own list in the project
    // config and writes a `model_prices` entry and a `budget.allowed_models`
    // entry for it, so it changes **what every run of the project may name and
    // spend on** — a class of change rule 12's modal exists for. The window the
    // plan opened carries the tick, and `src/main/confirm.ts` refuses a yes on
    // this subject without it.
    //
    // **It declares the plan's own four arguments and no others.** The window
    // showed one argv; the apply runs *that* argv, held by main with the plan
    // document's digest, and never one a renderer sends again
    // (`src/main/modelsadd.ts`): an IPC apply with no plan, or one whose payload
    // does not match the held one, spawns nothing.
    //
    // **It is one of the lapse's rows.** A write is taken away with the launch
    // and the settings write when the licence has lapsed (`refusedAfterLapse`);
    // the plan above is the same command as a read and keeps answering.
    name: 'models.add',
    method: 'addModel',
    command: 'models add --no-apply',
    flags: [],
    summary: 'the write: claim the model the plan showed, priced, and save the steps that publish it for `fugaro apply`',
    refusedAfterLapse: true,
    args: [
      {
        key: 'id',
        kind: 'modelId',
        required: true,
        why: 'the model ID the confirmed plan was about — the same four values the plan carried, so the argv main re-runs is the argv the window drew',
      },
      {
        key: 'route',
        kind: 'modelRoute',
        required: true,
        why: 'the route the confirmed plan claims the model on',
      },
      {
        key: 'priceIn',
        kind: 'modelPrice',
        required: false,
        why: 'the input rate the confirmed plan showed, or absent when the plan let the catalog price the model',
      },
      {
        key: 'priceOut',
        kind: 'modelPrice',
        required: false,
        why: 'the output rate the confirmed plan showed, or absent',
      },
    ],
  },
  {
    // The image write's plan (the console improvements plan's MW2, D2903):
    // `image add --no-apply --dry-run --json --dockerfile=<owner/name:path>
    // --tags=<a,b> --description=<text> --project <name> [--config <path>] --
    // <name>`.
    //
    // **It is the CLI's own dry run and nothing else**, the models plan's
    // property one command over. `--dry-run` is what makes this row a read: the
    // child validates the definition exactly as the real add would -- the
    // entry's name, its tags, its source, the installation's own skew gate and
    // D1573's coding-agent refusal -- and prints the versioned document
    // (`schemas/image-add-plan.schema.json`) with the entry it would add and the
    // steps it would run or save. Nothing is written: no catalog entry, no
    // build, no saved apply step, and no repository redeployed. The window the
    // operator confirms is drawn from that document, so there is no second
    // description of the change for the console to get wrong.
    //
    // **The payload is four values and every one is held to a grammar before any
    // child exists**: the entry's name (`imageName`, the positional), its
    // definition (`imageDockerfile`) and its tags and description (`imageTags`,
    // `imageDescription`) -- each checked in main against the CLI's own rule for
    // it, and each emitted as **one word** of the argv, the definition, the tags
    // and the description as `--flag=value` and the name as the positional behind
    // the row's `--` (`src/main/spawn.ts`), so no value a renderer sends can
    // parse as a flag.
    //
    // **`--no-apply` is main's**, a word of the command path rather than an
    // argument, and here it is the word that keeps the expensive step off this
    // machine: the add's own follow-up steps -- the catalog's **build**, the
    // repository's redeploy and the publish of the node every run is admitted by
    // (D1561) -- are saved for `fugaro apply` (D1565). The build is the one of
    // the console's writes that bills by itself, which is exactly why the console
    // saves it and owes it rather than running it.
    //
    // **`--inline`, `--image` and `--default` are arguments of no operation on
    // this surface**, and `--branch` with them. The CLI refuses three of them --
    // with `--branch` -- only when an agent marker is in the environment
    // (`imageAddAgentRefusal`, D1573), and a console's child carries no such
    // marker, so that refusal would not fire for this surface: what keeps those
    // definitions off the wire is the spawn table, whose every row declares the
    // three `=`-form arguments above and none of them, plus `assertArgvFlags`,
    // which refuses each of the four for every operation that does not declare
    // it. A console that could write `--inline` would be one that writes
    // executable content from a renderer's text.
    name: 'image.add.plan',
    method: 'planImageAdd',
    command: 'image add --no-apply --dry-run',
    flags: ['--json'],
    summary: 'what adding an image catalog entry would change, as the CLI’s own plan document and with nothing written',
    args: [
      {
        key: 'name',
        kind: 'imageName',
        required: true,
        why: 'the entry name `image add` reads as its positional word — the name the catalog is keyed by, and the shape the CLI’s own catalog-name rule gives it',
      },
      {
        key: 'dockerfile',
        kind: 'imageDockerfile',
        required: true,
        why: 'the definition the entry is built from, as `owner/name:path`: a Dockerfile in one of the project’s repositories. It is the one definition this surface admits — `--inline` writes executable content from a renderer’s text and `--image` names an image that already exists, and neither is a word any row of this table may spawn',
      },
      {
        key: 'tags',
        kind: 'imageTags',
        required: false,
        why: 'the tags the image provides, comma-separated — optional, and free-form by shape rather than by membership: the tags a repository needs are the project’s own, so main checks the CLI’s tag grammar and the selection checks the cover',
      },
      {
        key: 'description',
        kind: 'imageDescription',
        required: false,
        why: 'what the entry is for, printed beside it in a listing — optional, one line, printable and bounded',
      },
    ],
  },
  {
    // The image write (the same plan's MW2, D2903): the row above less
    // `--dry-run --json`, and the console's **sixth write** — after the launch,
    // the settings write, the record's move, the wizard's apply and the models
    // write.
    //
    // **It changes a project setting every run is admitted by.** The entry it
    // appends to the installation's image catalog is what the tag-cover rule
    // selects a job's image from, and through the steps `--no-apply` saves it is
    // the build the project's images come from: a project setting, which is the
    // class of change rule 12's modal exists for. It is **not a widening** in
    // the tick's sense — it raises no limit, and MD2's document has no
    // `widening` member for a window to classify it by — so its yes is one
    // deliberate click, as the wizard's is.
    //
    // **It is a project admin's and nothing else.** `image add` refuses anyone
    // but a project admin (`accessGateProject`, `internal/cli/image_catalog.go`),
    // and that is the authority check that survives this console: the window's
    // action is enabled only for a project admin (RB4's gating), and a role that
    // changed between the two is refused by the CLI's own sentence, which the
    // screen shows like any other refusal. The console decides no role of its own
    // (D2907).
    //
    // **It declares the plan's own four arguments and no others.** The window
    // showed one argv; the apply runs *that* argv, held by main with the plan
    // document's digest, and never one a renderer sends again
    // (`src/main/imageadd.ts`): an IPC apply with no plan, or one whose payload
    // does not match the held one, spawns nothing.
    //
    // **It is one of the lapse's rows.** A write is taken away with the launch
    // and the settings write when the licence has lapsed (`refusedAfterLapse`);
    // the plan above is the same command as a read and keeps answering.
    name: 'image.add',
    method: 'addImage',
    command: 'image add --no-apply',
    flags: [],
    summary: 'the write: add the catalog entry the plan showed and save the build that publishes it for `fugaro apply`',
    refusedAfterLapse: true,
    args: [
      {
        key: 'name',
        kind: 'imageName',
        required: true,
        why: 'the entry name the confirmed plan was about — the same four values the plan carried, so the argv main re-runs is the argv the window drew',
      },
      {
        key: 'dockerfile',
        kind: 'imageDockerfile',
        required: true,
        why: 'the definition the confirmed plan showed, word for word',
      },
      {
        key: 'tags',
        kind: 'imageTags',
        required: false,
        why: 'the tags the confirmed plan showed, or absent when the plan set none',
      },
      {
        key: 'description',
        kind: 'imageDescription',
        required: false,
        why: 'the description the confirmed plan showed, or absent',
      },
    ],
  },
  {
    // The settings editor (SU1, D2902): the one operation of the nine whose
    // command is **null**, because it fixes no argv of its own. It opens the
    // main process's own window for one key of the document the settings read
    // answered -- the entry, the validation, the dry run and the confirmation
    // window are that window's -- and it answers the outcome object rather than
    // a document, exactly as `settings.edit` does.
    //
    // **It is the settings write, reached through a window**, so it is in the
    // lapse's refused class beside `settings.edit` and says which row's command
    // its children come from: SU2b wires the Project tab's pencil to this row,
    // and a lapse that refused the write and kept the window would leave the
    // pencil on screen with a working write behind it. Its payload names no
    // installation, exactly as the write's does not: the flow writes the settings
    // document of the installation this console is driving.
    //
    // **`key` is the whole payload.** The layer and the phase are main's,
    // decided from the read's own row and the dry run's document, so a renderer
    // cannot name where or when a write lands -- which is why the editor is an
    // operation of its own rather than a flag on the write. It is the same
    // `settingKey` kind `settings.edit` uses, and the same reading of it: the
    // grammar is a key's shape, and whether the generic path owns a key is the
    // CLI's own allow-list (D1102).
    name: 'settings.editor.open',
    method: 'openSettingsEditor',
    command: null,
    drivesOperation: 'settings.edit',
    refusedAfterLapse: true,
    flags: [],
    summary: 'the write’s window: one setting’s entry, its dry run and the operator’s confirmation',
    args: [
      {
        key: 'key',
        kind: 'settingKey',
        required: true,
        why: 'the setting the window is for — a key of the document the read answered, so the console invents none. The layer and the phase stay main’s.',
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
 * its own argv; `--all` is the project-wide wrapper a read fixes as a word of
 * its row, where no screen control turns it off. Anything else an operation
 * might want to fix is on `NEVER_REACHABLE`'s flag rows. */
export const ALLOWED_OPERATION_FLAGS: readonly string[] = Object.freeze([
  '--json',
  '--url',
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
   * `except` is the operations this row does **not** apply to, by operation
   * name, and it is how one argv on this surface may reach what the table
   * refuses. It exists for something that is the *only* way into a read rather
   * than a second name for one the console already has: `--watch` on `runs ls`
   * is `watch` under another name and stays refused on every command, but
   * `graph --watch` is `graph`'s own streaming form -- there is no `fugaro
   * graph watch` word to use instead -- so `graph.watch` is that row's one
   * name; and `setup` is a write in every form but the first-run wizard's two,
   * so `setup.plan` and `setup.apply` are that row's.
   *
   * **It is a `flag` row's field and a `command` row's alike** (the console
   * improvements plan, AL), **and an `argv` row's too** (the same plan's MW1):
   * the three shapes need it for the same reason, and the alternative -- taking
   * `setup`, or the whole `models` group, off the table and refusing its sibling
   * commands one by one -- would be the denylist this table exists not to be.
   * An `argv` row's exception is read as the argv row is matched: the operation
   * must be one of the surface's, its command path must be the row's command or
   * a longer path under it, and the word must be in that path or in the flags
   * the operation fixes -- so `models.add` may spell `add`, and an operation
   * that spells it anywhere else may not.
   *
   * **It is keyed by operation and never by command.** A name is the whole of
   * what this surface has: a caller that does not say which operation it is
   * asking about gets the row's refusal like any other (`isNeverReachable`
   * below), so `assertReachableSurface` and the per-row tests still refuse the
   * bare word, and a second argv on `graph` would be a second surface row rather
   * than a second member of this list. The check that an exception is *used* is
   * `src/main/spawn.ts`'s: a row whose argv carries the word, or whose command
   * path is the command or a longer path under it, is refused there unless the
   * operation is on this list, and this list admits nothing the spawn table does
   * not build -- and a name that is not an operation at all is refused by
   * `assertReachableSurface` at startup.
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
    // **The row stays, and two names are excepted from it** (the console
    // improvements plan, AL and D2904): the first-run wizard is a screen, and a
    // screen has no terminal -- so the plan the wizard shows and the apply the
    // operator confirms are `setup --plan-only …` and `setup …`, two argvs of
    // this same command. A ban is kept and narrowed rather than a denylist
    // written, which is what makes "the console never converges anything but
    // what the wizard's window showed" a fact of this table: the two names are
    // the whole of the exception, `setup.plan`'s argv carries `--plan-only` and
    // `setup.apply`'s carries none of the creating flags, and both take their
    // answers from main's own bytes on stdin.
    //
    // **A third argv of `setup` is refused**, and that is the direction that
    // matters: a name is a row this file's own table declares, so a later edit
    // cannot widen this exception by adding a word to some argv -- it would
    // have to come back here and name the new operation.
    except: ['setup.plan', 'setup.apply'],
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
  // The recipes group's other two words, in the second shape the models rows
  // below are drawn in: refused **as words** wherever a `recipes` argv spells
  // them, because `recipes ls publish` is not the command `recipes publish`
  // and a command row alone would admit it. `publish` is the write above;
  // `validate` is a read, and the word of this group a renderer could fill in
  // -- it takes a free `FILE` positional, where the surface's one read,
  // `recipes ls`, takes no argument at all.
  {
    shape: 'argv',
    command: 'recipes',
    value: 'publish',
    why: 'the write above, refused as a word of this command as well',
    where: 'internal/cli/recipes.go:327',
  },
  {
    shape: 'argv',
    command: 'recipes',
    value: 'validate',
    why: 'a read, and the one word of this group that takes a **positional** `FILE` a renderer would fill: this surface reads the project’s recipes with `recipes ls`, which takes no argument at all, and a free word a caller chooses is the shape this table keeps off it',
    where: 'internal/cli/recipes.go:453 (Use: "validate FILE")',
  },
  // **The `user` group's writers, in the two shapes the models rows below are
  // drawn in.** `fugaro user ls --json` is the group's one reachable word
  // (`members.list` above, the roles-and-scopes plan's RS7); the five commands
  // here are the words beside it, and no one of them is a read: `grant` and
  // `revoke` write the installation's grants node -- one person, one role, one
  // scope, judged against the grantor's own scope and written conditionally on
  // an ETag (RS5) -- `add` and `rm` rewrite this machine's own copy of the
  // installation's two IAM lists (F42), and `offboard` takes the member off the
  // installation outright: the roster drops every grant naming them and the
  // intake deletes the secrets stored under their prefix (DS2.4, D1935). Each
  // changes who may do what, which is the one thing this surface reads and
  // never moves.
  //
  // The `argv` rows under them refuse the five **words** wherever an operation
  // under `user` spells them, on the models rows' own reasoning: a command row
  // alone would not cover a future operation whose command is `user grant
  // <member>` rather than the command `user grant`. Each `command` row stays
  // above the `argv` row carrying its word, so `isNeverReachable` answers with
  // the narrower one and `console/test/unit/ipc.test.ts` can assert it per row.
  {
    shape: 'command',
    value: 'user grant',
    why: 'a write: it writes one role and scope pair for one person into the installation’s grants node, which is the installation’s record of who may do what and what its next `fugaro update` derives the IAM lists from',
    where: 'internal/cli/user_grant.go:101 (newUserPairCmd(true))',
  },
  {
    shape: 'command',
    value: 'user revoke',
    why: 'a write: it takes one role and scope pair away from one person in that same node, which can leave an installation with one fewer admin than it had',
    where: 'internal/cli/user_grant.go:101 (newUserPairCmd(false))',
  },
  {
    shape: 'command',
    value: 'user add',
    why: 'a write: it rewrites this machine’s own copy of the installation’s operator or admin IAM list, and that copy is what the machine’s next `fugaro update` applies',
    where: 'internal/cli/user.go:129',
  },
  {
    shape: 'command',
    value: 'user rm',
    why: 'a write: it takes a member out of this machine’s own copy of one of those two IAM lists, and that copy is what the machine’s next `fugaro update` applies',
    where: 'internal/cli/user.go:134',
  },
  {
    shape: 'command',
    value: 'user offboard',
    why: 'a write: it drops every grant of the installation’s roster that names the member and has the intake delete the secrets stored under their prefix, which is the group’s most destructive word',
    where: 'internal/cli/offboard.go:106',
  },
  {
    shape: 'argv',
    command: 'user',
    value: 'grant',
    why: 'the write above, refused as a word of this command as well: a `user` argv that spells `grant` is refused before `spawn` is called, whether it spells it in the command path or in the flags it fixes',
    where: 'internal/cli/user_grant.go:101 (newUserPairCmd(true))',
  },
  {
    shape: 'argv',
    command: 'user',
    value: 'revoke',
    why: 'the write above, refused as a word of this command as well',
    where: 'internal/cli/user_grant.go:101 (newUserPairCmd(false))',
  },
  {
    shape: 'argv',
    command: 'user',
    value: 'add',
    why: 'the write above, refused as a word of this command as well',
    where: 'internal/cli/user.go:129',
  },
  {
    shape: 'argv',
    command: 'user',
    value: 'rm',
    why: 'the write above, refused as a word of this command as well',
    where: 'internal/cli/user.go:134',
  },
  {
    shape: 'argv',
    command: 'user',
    value: 'offboard',
    why: 'the write above, refused as a word of this command as well',
    where: 'internal/cli/offboard.go:106',
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
    // **The console's fifth write, and the only way into it** (the console
    // improvements plan's MW1, D2903). The two rows that make the write are the
    // CLI's own dry run and the write itself, and they are the whole of what
    // this row grants: the write goes through the single modal rule 12
    // describes, the operator's yes is the window's own top frame's, and main
    // re-runs the argv it showed rather than one a renderer sends. Every other
    // operation under `models` is refused as before, and a *third* models write
    // -- `models rm` or `models use` -- is refused by its own rows below.
    except: ['models.add.plan', 'models.add'],
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
    // The two rows the command row above grants, and no others (MW1, D2903):
    // an argv row's `except` is read the same way a command row's is -- the
    // operation must be one of the surface's, its command path must be under
    // this row's command, and its argv must carry this row's word
    // (`assertReachableSurface` below) -- so the exception is a fact about those
    // two argvs and not a permission for `add` on any other operation.
    except: ['models.add.plan', 'models.add'],
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
  // **The image group's writers the rows above did not carry, in the two
  // shapes the models and `user` rows are drawn in.** The catalog's own three
  // words -- `add` appends an entry to the installation's image catalog, `rm`
  // drops one, and `promote` copies a repository's existing image definition
  // into the catalog and points the repository at it -- are writes to a
  // project setting a reviewer signed off on, which is why they are here
  // beside the `image build` and `image refresh` rows above rather than
  // reachable. The console's one word of this group stays `image ls --json`
  // (`image.catalog.ls` above), and it takes no argument at all.
  //
  // The `argv` rows under them refuse the seven **words** wherever an
  // operation under `image` spells them, in the command path or in the flags
  // it fixes, on the models rows' own reasoning: a command row alone would
  // admit a future operation whose command is `image add <name>` rather than
  // the command `image add`. Each `command` row stays **above** the `argv` row
  // carrying its word, so `isNeverReachable` answers with the narrower one and
  // `console/test/unit/ipc.test.ts` can assert it per row. The credential
  // helper above is the one word here with no `argv` row, as the `secrets`
  // group's words have none: it is refused as a command, and a word-shaped
  // refusal is for a word an argv could carry into a read.
  {
    shape: 'command',
    value: 'image add',
    why: 'a write: it appends an entry to the installation’s image catalog, a project setting a reviewer signed off on',
    where: 'internal/cli/image_catalog.go:337 (Use: "add <name>")',
    // **The console's sixth write, and the only way into it** (the console
    // improvements plan's MW2, D2903), granted for the models write's reason one
    // group over: the two rows that make the write are the CLI's own dry run and
    // the write itself, the operator's yes is rule 12's own window's top frame's,
    // and main re-runs the argv it showed rather than one a renderer sends. Every
    // other operation under `image`, and every other word of this group -- `rm`
    // and `promote` above, `build`, `gate`, `record`, `refresh` and
    // `git-credential` beside them -- is refused as before.
    except: ['image.add.plan', 'image.add'],
  },
  {
    shape: 'command',
    value: 'image rm',
    why: 'a write: it removes an entry from the installation’s image catalog, which stops every repository the catalog’s map points at it',
    where: 'internal/cli/image_catalog.go:705 (Use: "rm <name>")',
  },
  {
    shape: 'command',
    value: 'image promote',
    why: 'a write: it copies a repository’s image definition into the catalog and rewrites the repository’s workflow to name the entry, in the checkout it runs in',
    where: 'internal/cli/image_promote.go:22 (Use: "promote <repo> [name]")',
  },
  {
    shape: 'argv',
    command: 'image',
    value: 'add',
    why: 'the catalog write above, refused as a word of this command as well: an `image` argv that spells `add` is refused before `spawn` is called, whether it spells it in the command path or in the flags it fixes',
    where: 'internal/cli/image_catalog.go:337 (Use: "add <name>")',
    // The write's two operations, by name, for the models argv row's reason: an
    // exception grants an operation and never a word, so `image ls --json` and
    // `image add --no-apply` are told apart by the operation that built them and
    // not by the `add` in the line. `image rm` and `image promote` keep their own
    // rows below, and no other `image` operation may spell this word.
    except: ['image.add.plan', 'image.add'],
  },
  {
    shape: 'argv',
    command: 'image',
    value: 'rm',
    why: 'the catalog write above, refused as a word of this command as well',
    where: 'internal/cli/image_catalog.go:705 (Use: "rm <name>")',
  },
  {
    shape: 'argv',
    command: 'image',
    value: 'promote',
    why: 'the catalog write above, refused as a word of this command as well',
    where: 'internal/cli/image_promote.go:22 (Use: "promote <repo> [name]")',
  },
  {
    shape: 'argv',
    command: 'image',
    value: 'build',
    why: 'the write above, refused as a word of this command as well',
    where: 'internal/cli/image.go:77',
  },
  {
    shape: 'argv',
    command: 'image',
    value: 'gate',
    why: 'the write above, refused as a word of this command as well',
    where: 'internal/cli/image_record.go:63',
  },
  {
    shape: 'argv',
    command: 'image',
    value: 'record',
    why: 'the write above, refused as a word of this command as well',
    where: 'internal/cli/image_record.go:148',
  },
  {
    shape: 'argv',
    command: 'image',
    value: 'refresh',
    why: 'the write above, refused as a word of this command as well',
    where: 'internal/cli/image_refresh.go:330',
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
    where: 'docs/project-centric/implementation-plan.md Task 1.6 (PR #390): this tree has no `fugaro project` command at all',
  },
  {
    shape: 'command',
    value: 'project repos add',
    why: 'a write: it enrolls a repository in the allow-list',
    where: 'docs/project-centric/implementation-plan.md Task 1.6 (PR #390); not on main 2026-10-16',
  },
  {
    shape: 'command',
    value: 'project repos rm',
    why: 'a write: it un-enrolls a repository. Un-enrollment is a security-relevant act, which is why it stays a typed command in a terminal',
    where: 'docs/project-centric/implementation-plan.md Task 1.6 (PR #390); not on main 2026-10-16',
  },
  {
    shape: 'command',
    value: 'project sync',
    why: 'a write: it adds the missing repositories to the allow-list',
    where: 'docs/project-centric/implementation-plan.md Task 4.1; not on main 2026-10-16',
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
    // graph -- so `graph.watch` is the one operation that may carry it.
    //
    // **It is granted by operation name and by nothing else.** `isNeverReachable`
    // reads this list only for a caller that says which operation it is asking
    // about, and the one caller that does is `assertArgvFlags`, with the argv's
    // own words -- so every other operation's `--watch` is refused exactly as
    // before, and no value can spell the word in its place (`argvFor` refuses a
    // value a caller could read as a flag). What keeps the exception to that one
    // argv is the row rather than a second check here: `graph.watch` is the only
    // operation whose **command path** carries the word, and a spawn row must
    // begin with its operation's own command (`assertSpawnSurface`), which is
    // also what holds the word to `graph`'s argv at startup -- the surface
    // assertions match a flag row against an operation's declared `flags`, and
    // `--watch` is no row's declared flag.
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
 * A row's `except` names the operations that row does not apply to, and it is
 * read only when the caller names the operation it is asking about: the word,
 * or the command, is refused for every other operation and for a caller that
 * names none, so an exception is a fact about one argv on the surface rather
 * than a permission for a word (`graph.watch`'s and `setup`'s rows above). A
 * `command` row's exception is otherwise the strictest thing here: the command
 * row matches the row's own words or a longer path under them, and never a
 * command the row does not name -- so an exception cannot widen into a
 * permission for a sibling command.
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
  const asking = operation.name ?? null
  // An exception is read only for a caller that named the operation it is
  // asking about: a caller that named none is asking about the word or the
  // command itself, and every one of those is refused.
  const excepted = (entry: NeverReachable): boolean =>
    asking !== null && entry.except !== undefined && entry.except.includes(asking)
  for (const entry of NEVER_REACHABLE) {
    // A `command` row is about one command, and it matches an operation whose
    // command path is that command **or a longer path under it** -- the same
    // reading the `argv` rows below take, and for the same reason. The launch's
    // command carries two fixed words of its argv (`run --task-file -`), and so
    // do the first-run wizard's two (`setup --plan-only --answers-file -
    // --events`, `setup --answers-file - --events`): the mode word that decides
    // what the child does belongs in the command path, which is what keeps it
    // out of a payload, and a ban that only matched the bare word would leave
    // every argv carrying one beneath it ungranted-and-unrefused.
    if (
      entry.shape === 'command' &&
      operation.command !== null &&
      (entry.value === operation.command || operation.command.startsWith(`${entry.value} `))
    ) {
      if (!excepted(entry)) return entry
      continue
    }
    if (entry.shape === 'flag' && operation.flags.includes(entry.value)) {
      if (!excepted(entry)) return entry
      continue
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
      if (under && [...operation.command.split(' '), ...operation.flags].includes(entry.value)) {
        // The exception is read here exactly as it is on the two shapes above
        // (MW1): a caller that named no operation, or one the row does not
        // except, gets the refusal -- so `models add` is refused as a word of
        // `models` for every operation but the two the row names.
        if (!excepted(entry)) return entry
      }
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
 * **word** a key can be, and `internal/cli/settings_set.go:537` refuses any key
 * carrying a `*` with its own sentence -- "varies with a name this command does
 * not take" -- before the allow-list is consulted. Tightening the segment here
 * would move that refusal from the CLI's words to a schema refusal of this
 * console's, for a key the CLI was always going to refuse.
 *
 * **It is a shape and not the allow-list.** Whether the generic write path owns
 * a key is `settingsGenericKeys`/`settingsGenericPrefixes`
 * (`internal/cli/settings_set.go:96`, `:251`) -- the CLI's own list, which the
 * console keeps no copy of (D1102). What this grammar does is keep the argv
 * word a key can be: no space, no dash, no `=`, nothing that could be read as a
 * flag or quoted by a shell, so the key never needs the terminator the value
 * does.
 */
const SETTINGS_KEY_RE = /^[a-z0-9_*]+(\.[a-z0-9_*]+)*$/

// The first-run wizard's answers (docs/console-improvements, D2904).

/**
 * SETUP_ANSWER_KEYS is the questions `fugaro setup` answers from one document
 * (`setupAnswerQuestions`, `internal/cli/setup_answers.go`), in the order the
 * CLI asks them: `budget` is the sixth, between the rigor mode and the bake
 * switch, where `setupQuestions` puts its row (T2b of
 * docs/managed-flag-ergonomics, D2210).
 *
 * **The console keeps its own copy rather than importing
 * `src/shared/new-ops.ts`'s**, and that direction is the design's: the contract
 * task's own interface line says the allow-list implements the operations
 * *beside* the contract and does not import them, so `src/shared` carries no
 * authority over what a payload may contain. The two copies are held equal by
 * the plan's own integration step (I-OPS) against the CLI goldens, which is
 * what keeps two spellings of one fact from drifting.
 */
export const SETUP_ANSWER_KEYS: readonly string[] = Object.freeze([
  'gcp_project',
  'region',
  'name',
  'rigor',
  'budget',
  'bake_repos',
])

/**
 * SETUP_BUDGET_MIN and SETUP_BUDGET_MAX bound the wizard's `budget` answer, and
 * they are the alert's own bounds rather than this console's:
 * `managed.MinBudgetAmount` and `managed.MaxBudgetAmount`
 * (`internal/infra/managed/marker.go`), which `Budget.Check` applies and the
 * answers file's schema states (`schemas/setup-answers.schema.json`). An amount
 * outside them is a document the run refuses later, after a child exists, which
 * is exactly what this door is here to prevent.
 */
export const SETUP_BUDGET_MIN = 1
export const SETUP_BUDGET_MAX = 10_000_000

/** SETUP_ANSWERS_VERSION is the answers file's own schema version
 * (`schemas/setup-answers.schema.json`). It is main's to write: the file's
 * `version` is the reader's contract and not one of the wizard's questions, so
 * no renderer member names it. */
export const SETUP_ANSWERS_VERSION = 1

/**
 * SETUP_RIGOR_WORDS is the rigor answer's closed set, and it is the **file's**
 * four (`schemas/setup-answers.schema.json`'s `enum`), not the five the console
 * draws elsewhere: `fugaro run --mode hotfix` is an operator's per-run mode a
 * layer never sets, so an answers document carrying it is refused by the CLI's
 * own decoder -- and a payload that can only fail after a child is spawned is
 * exactly what this schema is here to refuse before one exists.
 */
export const SETUP_RIGOR_WORDS: readonly string[] = Object.freeze(['poc', 'startup', 'pro', 'critical'])

/** MAX_SETUP_ANSWER_LENGTH bounds one answer before its grammar runs. Every
 * question's answer is a word: a GCP project id, a region, a project name, a
 * rigor mode, or a boolean. It is exported because it is a bound of the
 * operation rather than an implementation detail: `ipc.test.ts` holds the
 * refusal to the number itself, so a later widening of it is a test's failure
 * and not a silent one. */
export const MAX_SETUP_ANSWER_LENGTH = 256

/** SETUP_ANSWERS_MAX_LENGTH bounds the serialised answers file. Each member is
 * bounded above, so this is the whole document's own ceiling rather than a
 * bound a single answer could be refused by -- and it is the number main hands
 * to `payloadWidth`'s sibling for this kind (`kindLimit`). */
export const SETUP_ANSWERS_MAX_LENGTH = 4096

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
 *
 * **`budget.stream` carries `budget.show`'s pair and for the same reason** (the
 * console improvements plan, AL): the stream's argv is the read's own plus
 * `--stream`, so `--repo` with `--all` is the same argv the CLI refuses at
 * `internal/cli/budget.go:387` -- and it would be refused after a long-lived
 * child had already started rather than before one existed.
 */
const REFUSED_PAIRS: Readonly<Record<string, readonly (readonly [string, string, string])[]>> = Object.freeze({
  'budget.show': [['repo', 'all', "--repo and --all don't go together"]],
  'budget.stream': [['repo', 'all', "--repo and --all don't go together"]],
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
 * setupAnswersValue validates the first-run wizard's answers and answers with
 * **the file the child reads**, as one string.
 *
 * Two things are deliberate, and both are rule 6's.
 *
 * **The payload carries an object and never a path.** The only path this
 * operation ever sees is the `-` main puts in the argv (`--answers-file -`,
 * `spawn.ts`'s own row words), so no renderer member can name a file -- and a
 * member that is not one of the five known keys is refused by name right here,
 * before any child exists. The object is checked as a plain payload, so the
 * `{__proto__: ...}` shape `isPlainPayload` exists for is refused too.
 *
 * **The value returned is main's own serialisation and not the renderer's
 * object.** Every member is read one key at a time, checked against its own
 * grammar, and written into a document this function builds; the string that
 * comes out is therefore made of values that were each validated, and there is
 * no path for an unchecked member to ride along. `version` is added here: it is
 * the answers file's schema member and not one of the wizard's questions.
 *
 * **`budget` is the one optional member, and the one that is a number.** A
 * document that leaves it out answers nothing and the run takes its own default
 * (100, D2212), so an absent key is written as an absent key rather than as a
 * zero -- 0 is an amount the alert cannot be created with. A number outside
 * [SETUP_BUDGET_MIN, SETUP_BUDGET_MAX] is refused here with this console's own
 * sentence, because the CLI would refuse it after a child had started.
 *
 * The bytes themselves are not made here. They are the caller's (`setup.plan`'s
 * handler, SW3a), which encodes this string and hands it to the child on
 * standard input -- so no file exists for another process of the same user to
 * change between this check and the CLI's read.
 */
function setupAnswersValue(
  spec: ArgumentSpec,
  value: unknown,
): { readonly ok: true; readonly value: ValidatedArgument } | { readonly ok: false; readonly reason: string } {
  if (!isPlainPayload(value)) return refuse(`${spec.key} must be an object of the wizard’s answers`)
  for (const key of Object.keys(value)) {
    // The refusal names the key and never its value: a path a renderer sent is
    // the case this branch is for, and echoing it would print it.
    if (!SETUP_ANSWER_KEYS.includes(key)) return refuse(`${spec.key} takes no ${key}`)
  }
  const answers: Record<string, string | boolean | number> = {}
  for (const key of SETUP_ANSWER_KEYS) {
    const member: unknown = value[key]
    // The budget is the question an operator may leave unanswered: an absent
    // key is written as an absent key, and the CLI reads that as its own
    // default. Every other question is required.
    if (member === undefined) {
      if (key === 'budget') continue
      return refuse(`${spec.key} is missing ${key}`)
    }
    if (key === 'budget') {
      if (
        typeof member !== 'number' ||
        !Number.isInteger(member) ||
        member < SETUP_BUDGET_MIN ||
        member > SETUP_BUDGET_MAX
      ) {
        return refuse(
          `${spec.key}'s budget must be a whole number of units between ${SETUP_BUDGET_MIN} and ${SETUP_BUDGET_MAX}`,
        )
      }
      answers[key] = member
      continue
    }
    if (key === 'bake_repos') {
      if (typeof member !== 'boolean') return refuse(`${spec.key}'s bake_repos must be true or false`)
      answers[key] = member
      continue
    }
    if (typeof member !== 'string') return refuse(`${spec.key}'s ${key} must be a string`)
    if (member === '' || member.length > MAX_SETUP_ANSWER_LENGTH) {
      return refuse(
        `${spec.key}'s ${key} must be a non-empty answer of at most ${MAX_SETUP_ANSWER_LENGTH} characters`,
      )
    }
    const hidden = hiddenCharacterReason(member)
    if (hidden !== null) return refuse(`${spec.key}'s ${key} ${hidden}`)
    // A GCP project id, a region and a project name are words; `/` is the one
    // character none of their grammars has, and refusing it here is what keeps
    // a filesystem path out of the document the child decodes.
    if (member.includes('/') || member.includes('\\')) {
      return refuse(`${spec.key}'s ${key} must not be a path`)
    }
    if (key === 'rigor' && !SETUP_RIGOR_WORDS.includes(member)) {
      return refuse(`${spec.key}'s rigor must be one of ${SETUP_RIGOR_WORDS.join(', ')}`)
    }
    answers[key] = member
  }
  const file: Record<string, unknown> = { version: SETUP_ANSWERS_VERSION }
  // A question nobody answered is left out of the document rather than written
  // empty: `budget` is the only one of those, and the CLI reads its absence as
  // its own default (`setupAnswers.answer`).
  for (const key of SETUP_ANSWER_KEYS) {
    if (key in answers) file[key] = answers[key]
  }
  const text = JSON.stringify(file)
  if (text.length > SETUP_ANSWERS_MAX_LENGTH) {
    return refuse(`${spec.key} must be an answers file of at most ${SETUP_ANSWERS_MAX_LENGTH} characters`)
  }
  return { ok: true, value: text }
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
  // The wizard's answers are the one kind whose payload is an object: it is
  // checked before the kind's length is (the document's own bound is inside).
  if (spec.kind === 'setupAnswers') return setupAnswersValue(spec, value)
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
    case 'runsStreamSince':
      // `runs ls --stream --since`, and **the console's own set rather than the
      // command's grammar**: `runs ls` reads `parseSince` the way `runs stats`
      // does, and its first branch is `0` -- no lower bound -- which on a live
      // feed is every run the installation ever made, still arriving. The window
      // is the day this console reads a run list for
      // (`RUNS_STREAM_SINCE_WINDOWS`), so the feed and the whole-document
      // listing its screen falls
      // back to cover the same window; every other form the command takes
      // (`0`, `7d`, `36h`, a date) is refused before a child exists.
      if (!RUNS_STREAM_SINCE_WINDOWS.includes(value)) {
        return refuse(`${spec.key} must be one of ${RUNS_STREAM_SINCE_WINDOWS.join(', ')}`)
      }
      return { ok: true, value }
    case 'reportDimension':
      if (!REPORT_DIMENSIONS.includes(value)) return refuse(`${spec.key} must be one of ${REPORT_DIMENSIONS.join(', ')}`)
      return { ok: true, value }
    case 'secretScope':
      if (!SECRET_SCOPES.includes(value)) return refuse(`${spec.key} must be ${SECRET_SCOPES.join(' or ')}`)
      return { ok: true, value }
    case 'graphSource':
      // The graph read's scope (CG9a; named ids with CG7a): `all`, `coordinator`,
      // or an id-carrying scope whose id is C-ORCH's orchestrator id -- a project
      // name and, for a named orchestrator, a `~` and a name. This is the half
      // that keeps a value from being a path, a flag or another word of the
      // command -- `-x`, `all --source local:o1`, `orchestrator:../..`, a
      // 41-character project half and a name over 40 characters are all refused
      // here, before a child exists -- and the other half, that the scope is one
      // a previous read of this document named, is the source's
      // (`createBoundSource`, F21).
      if (!GRAPH_SCOPE_RE.test(value)) {
        return refuse(
          `${spec.key} must be all, coordinator, or orchestrator:<id> / local:<id> with an id of a project name, optionally ~ and a name of 1 to 40 characters of a-z, 0-9 and -`,
        )
      }
      return { ok: true, value }
    case 'roadmapNode':
      // The node a move is about, named by the key the drawing carries. **The
      // two words of the move's argv are not this value** -- the task word is
      // the document's own `id` and the `--expect` word is the node's own
      // `release`, both read out of main-process state (`spawn.ts`) -- so what
      // this check does is keep a payload from carrying something a key could
      // not be, and the identity is the source's question, asked of the last
      // document this installation read.
      if (!ROADMAP_NODE_RE.test(value)) {
        return refuse(`${spec.key} must be a node key of the graph the console last read`)
      }
      return { ok: true, value }
    case 'roadmapRelease':
      // The release box the task is moved into, or the empty string for the
      // unassign. The key is the record's own opaque id, so this is the whole
      // of what a shape can say about it; that the box is one the document held
      // is the source's check.
      if (!ROADMAP_RELEASE_RE.test(value)) {
        return refuse(`${spec.key} must be a release key of the record, or empty for no release`)
      }
      return { ok: true, value }
    case 'link': {
      const decision = validateExternalUrl(value)
      if (!decision.ok) return refuse(`${spec.key} ${decision.reason}`)
      return { ok: true, value: decision.url }
    }
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
    case 'modelId': {
      // The word `models add` reads as its positional (`MODEL_ID_RE` above): the
      // exact ID and never a pattern, so what this refuses is what the CLI's own
      // step 1 would refuse one process later -- and a value a route may not
      // claim never reaches an argv at all.
      if (!MODEL_ID_RE.test(value)) {
        return refuse(`${spec.key} must be a model ID: lower-case vendor/model, at most 128 characters`)
      }
      return { ok: true, value }
    }
    case 'modelRoute': {
      // A **shape** and not a membership: which routes a project has is its own
      // `providers` block, so a name this project does not carry is refused by
      // the child's own sentence, which names the routes to choose from. This is
      // the config's shared name rule (lower-case letters, digits and `-`).
      if (!MODEL_ROUTE_RE.test(value)) {
        return refuse(`${spec.key} must be a route name (lower-case letters, digits and '-')`)
      }
      return { ok: true, value }
    }
    case 'modelPrice': {
      if (!MODEL_PRICE_RE.test(value)) {
        return refuse(`${spec.key} must be a price in US dollars per million tokens, as a plain decimal`)
      }
      if (!(Number(value) > 0) || Number(value) > MAX_MODEL_PRICE) {
        return refuse(`${spec.key} must be more than 0 and at most ${MAX_MODEL_PRICE} dollars per million tokens`)
      }
      return { ok: true, value }
    }
    case 'imageName': {
      // The CLI's own catalog-name rule (`IMAGE_NAME_RE` above), and the shape
      // is the whole check: whether the catalog already holds this name is the
      // child's question, and its sentence -- which names the entry -- is the one
      // the screen shows.
      if (!IMAGE_NAME_RE.test(value)) {
        return refuse(`${spec.key} must be a catalog entry name: 1 to 40 of a-z, 0-9 and '-', starting and ending with a letter or a digit`)
      }
      return { ok: true, value }
    }
    case 'imageDockerfile': {
      // `owner/name:path`, cut at the **first** colon exactly as the CLI cuts it
      // (`catalogEntryFromFlags`, `internal/cli/image_catalog.go:672`): a second
      // colon belongs to the path, and a value with no colon, an empty half or a
      // repository that is not `owner/name` is one the CLI would refuse a process
      // later. Both halves are checked here so the console never spells it.
      const colon = value.indexOf(':')
      if (colon < 0) {
        return refuse(`${spec.key} must be a Dockerfile in one of the project's repositories, as owner/name:path`)
      }
      const repo = value.slice(0, colon)
      const path = value.slice(colon + 1)
      if (!REPOSITORY_RE.test(repo) || path === '') {
        return refuse(`${spec.key} must be a Dockerfile in one of the project's repositories, as owner/name:path`)
      }
      // The path's own rules, which are the CLI's for a dockerfile source
      // (`catalogPathMessage`, `internal/config/catalog.go:448`): it is fetched
      // from a repository and read by a build, so it is one clean relative path
      // inside that repository: never absolute, never carrying a `..`, a `.` or
      // an empty segment, and never empty. A control character is already refused
      // for every string argument (`hiddenCharacterReason` above).
      const segments = path.split('/')
      if (
        path.startsWith('/') ||
        segments.includes('..') ||
        segments.includes('.') ||
        segments.includes('') ||
        path === '.'
      ) {
        return refuse(`${spec.key} must name a path inside the repository: relative, with no '..' or '.' segment`)
      }
      return { ok: true, value }
    }
    case 'imageTags': {
      // A **shape per member and not a membership**, which is `modelRoute`'s
      // reading one list over: the CLI's own tag grammar is `IMAGE_TAG_RE` and
      // the set of tags a project uses is the project's own, so a tag this
      // installation holds nothing for is refused by the selection's sentence
      // rather than here. What main refuses is a list that is empty, longer than
      // the CLI's own count, made of a member outside the grammar, or wide
      // enough to be one argv element this process will not hand a child.
      const members = value.split(',')
      if (
        value === '' ||
        members.length > IMAGE_TAGS_MAX ||
        members.some((member) => !IMAGE_TAG_RE.test(member))
      ) {
        return refuse(`${spec.key} must be a comma-separated list of tags: at most ${IMAGE_TAGS_MAX} of a-z, 0-9, '.', '_' and '-', each starting with a letter or a digit`)
      }
      return { ok: true, value }
    }
    case 'imageDescription': {
      // **An empty description is refused rather than sent.** The CLI takes an
      // entry with no description -- `description:` is optional in the catalog --
      // and the way a renderer says so is by omitting the argument, which is what
      // leaves the flag off the argv: an empty `--description=` would be a
      // different argv for the same entry. A tab, a newline and every other
      // invisible character are already refused for every string argument
      // (`hiddenCharacterReason` above), which is the CLI's own rule for a
      // description (a listing prints it on one line).
      if (value === '') {
        return refuse(`${spec.key} must be a description: leave it out for an entry with none`)
      }
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
    case 'runsStreamSince':
      // The longest window in the closed set, read off the set for
      // `summarySince`'s reason.
      return Math.max(...RUNS_STREAM_SINCE_WINDOWS.map((window) => window.length))
    case 'reportDimension':
      return 8
    case 'secretScope':
      return 12
    case 'link':
      return MAX_URL_LENGTH
    case 'modelId':
      // The grammar's own bound: `MODEL_ID_RE` admits at most 128 characters, so
      // a longer word is refused before the expression runs.
      return 128
    case 'modelRoute':
      return 64
    case 'modelPrice':
      return MODEL_PRICE_MAX_LENGTH
    // The image write's four (MW2), each the length bound of the rule above it:
    // the name's is the grammar's own 40 and the description's is the CLI's 200,
    // while the definition's and the list's are the console's own bound on one
    // argv element -- narrower than the CLI's, and so the one that bites.
    case 'imageName':
      return 40
    case 'imageDockerfile':
      return MAX_IMAGE_DOCKERFILE_LENGTH
    case 'imageTags':
      return MAX_IMAGE_TAGS_LENGTH
    case 'imageDescription':
      return MAX_IMAGE_DESCRIPTION_LENGTH
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
    // The move's two (GV3.2): the longest node key `ROADMAP_NODE_RE` admits, and
    // the length of a release key -- the empty string is the unassign, so a
    // bound of 64 is the pattern's own longest value and not a bound a shorter
    // word could be refused by.
    case 'roadmapNode':
      return MAX_ROADMAP_NODE_LENGTH
    case 'roadmapRelease':
      return MAX_ROADMAP_RELEASE_LENGTH
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
 * "Bounded" is meant literally, and it is the two things `boundReason` does: a
 * message is flattened to one line (a CLI sentence is one line; a message with
 * newlines in it is either a stack or something written to look like the
 * console's own output), and it is cut at `MAX_REPLY_REASON_LENGTH`. A path is
 * **not** one of them: the CLI's refusals name flags, values and the operator's
 * own directories, so a path in a refusal is a path a screen would print, and
 * D3213 (2026-11-21) leaves it as written -- the renderer's `sanitizeRefusal` is
 * what masks a secret, wherever one stands. The CLI's own sentences are short
 * enough that neither of the two touches them; a hostile message hits both.
 */
function replyReason(error: unknown): string {
  if (error instanceof Error && error.message !== '') return boundReason(error.message)
  return 'the operation failed'
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
 * half-characters replaced, one line, and a hard length cut on a character
 * boundary. It never returns an empty string -- a refusal with no text is a
 * screen with nothing to draw -- and it never echoes more of the message than
 * the bound allows.
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
  if (flattened.length <= MAX_REPLY_REASON_LENGTH) return flattened
  // The cut is marked, so a reader can tell a truncated sentence from a whole
  // one rather than wondering what the CLI meant to say. It is in UTF-16 code
  // units, like the bound, and it steps back one when it would land between
  // the two halves of a surrogate pair: half an emoji is not text.
  let cut = MAX_REPLY_REASON_LENGTH - 1
  const last = flattened.charCodeAt(cut - 1)
  if (last >= 0xd800 && last <= 0xdbff) cut -= 1
  return `${flattened.slice(0, cut).trimEnd()}…`
}

/**
 * assertReachableSurface throws unless every entry of a table is one the console
 * may drive: its command and its flags are clear of `NEVER_REACHABLE`, the flags
 * it fixes are the surface's own two, the lapse's refused class holds only rows
 * that spawn a command or drive one the lapse itself takes away, and names its
 * target installation only in a required `installationName` argument of its own,
 * and no two entries share a name.
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
    // The lapse's refused class is the operations that change something
    // (`run.launch`, `settings.edit`, `roadmap.move`, `setup.apply` and
    // `models.add`), and the design's kept set includes every answer the console
    // gives out of its own process: a row with no command spawns nothing, so
    // refusing it would take away a read, the key screen (the way out, §5.6), or
    // the console's own surface -- and never a capability the licence granted.
    // The one row that spawns nothing as itself and belongs to the class anyway
    // is a window that drives one of those operations (`drivesOperation`), which
    // is checked just below: it spawns the write's own phases through the source,
    // so a lapse that kept it would keep the write. A later task that wants to
    // refuse one of these has to come back here and say why, which is the point.
    if (operation.refusedAfterLapse === true && operation.command === null && operation.drivesOperation === undefined) {
      throw new Error(
        `${operation.name} is refused after a lapse and spawns no command: the operations the lapse takes away are the ones that spawn a command, and the console's own answers stay`,
      )
    }
    // A row that drives another one's command (`drivesOperation`) may belong to
    // the refused class without a command of its own, and only that way: the name
    // has to be a row of this same table, and for a refused row it has to be a
    // row the lapse itself takes away -- pointing one window at a read would put
    // it in the class on a command nobody loses after a lapse, and a name that
    // matched no row would leave the window working with nothing to refuse.
    if (operation.drivesOperation !== undefined) {
      const drivenName = operation.drivesOperation
      const driven = operations.find((candidate) => candidate.name === drivenName)
      if (driven === undefined) {
        throw new Error(
          `${operation.name} drives ${drivenName}, which is not an operation of this surface`,
        )
      }
      if (operation.refusedAfterLapse === true && (driven.refusedAfterLapse !== true || driven.command === null)) {
        throw new Error(
          `${operation.name} is refused after a lapse and drives ${driven.name}, which the lapse does not take away: a row with no command of its own is in the refused class only through an operation the lapse refuses`,
        )
      }
    }
    // The lapse gate asks about the installation a row's own payload names
    // (`installationArgument`), so the column has to name an argument of this
    // same row that really is one. Anything else fails here rather than at the
    // gate: a column pointing at a free-text field would ask the licence store
    // about a string no installation answers for -- the no-key state, which
    // refuses nothing, so the operation would be admitted while the installation
    // it acts on had lapsed -- and a column naming no argument of the row at all
    // would hand the gate `undefined` and silently ask about the active binding
    // instead. A later row that wants to point this at something else has to
    // come back here and say why, which is the point.
    if (operation.installationArgument !== undefined) {
      const named = operation.args.find((argument) => argument.key === operation.installationArgument)
      if (named === undefined || named.required !== true || named.kind !== 'installationName') {
        throw new Error(
          `${operation.name} names the installation it acts on in ${JSON.stringify(operation.installationArgument)}, which is not a required \`installationName\` argument of this row: the lapse gate reads the target installation out of the row's own payload, and an argument that is not one would have it ask about the wrong installation`,
        )
      }
    }
    if (names.has(operation.name)) {
      throw new Error(`${operation.name} appears twice, so the surface has two operations with one name`)
    }
    names.add(operation.name)
  }
  // Every exception the never table grants names an operation of *this* table,
  // and a `command` row's names one under that same command. It is the check
  // that keeps a name in that table from being a permission for a row that does
  // not exist -- the one thing a name-keyed exception must not become, since a
  // typo would otherwise turn a total ban into a ban that refuses nothing.
  //
  // The command is read the way `isNeverReachable` reads it: the row's own
  // words, or a longer command path under them. `setup`'s exception names two
  // operations whose command path carries the wizard's mode words, and a check
  // that demanded the bare word would refuse the only exception the table has.
  for (const entry of NEVER_REACHABLE) {
    for (const name of entry.except ?? []) {
      const named = operations.find((candidate) => candidate.name === name)
      if (named === undefined) {
        throw new Error(
          `the never table's ${entry.value} row excepts ${name}, which is not an operation of the surface: an exception names a row of this table and nothing else`,
        )
      }
      const under =
        named.command !== null &&
        (named.command === entry.value || named.command.startsWith(`${entry.value} `))
      if (entry.shape === 'command' && !under) {
        throw new Error(
          `the never table's ${entry.value} row excepts ${name}, which drives ${String(named.command)}: a command row's exception names an operation of that command's own argv (the match is the row's words or a longer path under them)`,
        )
      }
      // An `argv` row's exception is held to what that row is matched by
      // (MW1): the named operation's command path must be the row's own command
      // or a longer path under it, **and** the row's word must be in that path
      // or in the flags the operation fixes. Anything else would be an exception
      // nothing uses -- a name that reads as a permission for a word the row
      // refuses everywhere else.
      const carries =
        entry.command !== undefined &&
        named.command !== null &&
        (named.command === entry.command || named.command.startsWith(`${entry.command} `)) &&
        [...named.command.split(' '), ...named.flags].includes(entry.value)
      if (entry.shape === 'argv' && !carries) {
        throw new Error(
          `the never table's ${entry.value} row (on ${String(entry.command)}) excepts ${name}, whose argv does not carry that word: an argv row's exception names an operation of that command's own argv that spells the word`,
        )
      }
    }
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
 *     screen through the by-person panel, and **`spend.stream` is the same
 *     read's live form** (the console improvements plan, AL): the same figures,
 *     one line at a time, so withholding it costs nothing a reduced console had
 *     -- a stream of a report it may not read is the report by another name.
 *     `runs.summary` (console 0.119.0's
 *     jobs-free totals read) carries the same two quantities again, as
 *     `spent_micros` and `notional_micros` over the window and over each
 *     repository, so it is withheld with them for the same reason: a totals
 *     read answered in reduced mode is the withheld figure by another name;
 *   - **`caps` names the Projects tab's reads, and the settings read with
 *     `budget show`.** The tab's own `VIEWS` entry declares the view this list
 *     is the other half of (`src/renderer/entry.ts`, `{ id: 'projects', … view:
 *     'caps' }`), so the section is offered and drawn only where `budget.show`
 *     may be read, and **`budget.stream` is that same read's live form**, so it
 *     is withheld with it (the console improvements plan, AL).
 *     `settings.read` (the settings-edit plan's C1) carries the
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
    spend: Object.freeze(['spend.report', 'spend.stream', 'runs.stats', 'runs.summary'] as const),
    caps: Object.freeze(['budget.show', 'budget.stream', 'settings.read', 'policy.show'] as const),
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
