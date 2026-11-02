// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The IPC allow-list, held to its own rules (desktop-console plan Task 1.2;
// design section 5.1 rule 5, section 9.2's mutation-proof form, D14).
//
// Every property in this file is one a mutation can break, and each is its own
// `it` -- `it.each`-style tables built from the real tables in `src/` -- so a
// mutation names the test that caught it instead of failing a crowd:
//
//   - `budget kill` (or any other row of `NEVER_REACHABLE`, or any writer this
//     tree has) added to `CONSOLE_OPERATIONS` fails the test named for that
//     command, *and* makes `assertReachableSurface` throw, *and* fails the
//     snapshot test that says what each command's classification is. This is
//     the plan's red step;
//   - a command in `CONSOLE_OPERATIONS` that the snapshot does not classify as
//     one the design's allow-list admits fails the test named for it;
//   - a mutating command missing from `NEVER_REACHABLE` fails the completeness
//     test named for it, so the never table cannot fall behind the CLI tree;
//   - `--follow`, `--check-github-app`, `--check-bitbucket-token`, `--strict`,
//     `--csv`, `--watch`, `--plain`, `--poll`, `--ascii` or `--no-color` fixed
//     by an operation fails the flag tests; `--url` together with `--json`
//     fails the pairing
//     test; and each of those flags has a test *named for it* that fails when
//     its never-table row is renamed away, so a row cannot be deleted quietly;
//   - an event from any frame but the console's own top frame -- a subframe, a
//     document on another origin, `app://console.evil.test`, a port, `file://`,
//     a frame that has gone away -- is refused, the handler never runs, and the
//     refusal repeats nothing the sender wrote: removing the sender check, or
//     any one of the three things it checks, fails the tests named for them;
//   - a handler's own message that is a megabyte long, or that carries a
//     control character or an absolute path, is bounded, flattened and redacted;
//     removing any of the three fails the test named for it, and the test that
//     an ordinary CLI sentence comes through verbatim is what keeps the three
//     from eating the case design section 6.3 asks for;
//   - removing `assertReachableSurface(CONSOLE_OPERATIONS)` from
//     `registerConsoleHandlers`, or moving it after the channels are registered,
//     fails "checks the surface before it registers anything"; dropping the
//     allowed-flags half of that guard fails the test with a flag the never
//     table does not list (`--mine`); and a payload wider than
//     `MAX_PAYLOAD_WIDTH` fails the width test;
//   - a preload method that is not on `CONSOLE_OPERATIONS`, or an operation
//     with no method, fails the two-way surface test; a method that lands on a
//     channel other than `channelForOperation(name)` fails the routing test;
//   - a schema that accepts an extra key, a prototype-pollution key, a
//     non-plain object, a path, a command, a flag name, a control character, an
//     invisible format character, an unpaired surrogate, an oversize string, a
//     wrong type or a URL that is not the validated external link fails the
//     table entry that names it; a kind shared between two commands whose flags
//     are not one grammar fails the per-command window table, row by row, and
//     the test that each command carries its own window kind -- the two
//     `--since` flags are two grammars (`reportRange`, `parseSince`), and one
//     shared kind was wrong in both directions at once; a schema that refuses
//     *everything* fails the
//     well-formed table, which is what keeps the refusal tables meaningful;
//   - an unfrozen bridge, a bridge without the table's version, a `length > 1`
//     method, or an `invoke`/`send`/`on` property fails the surface-shape tests;
//   - a `src/` file that gains a second `invoke`, a computed `require`, an
//     `eval`, a `child_process`, or a value import in the preload fails the
//     source scans at the bottom.
//
// Nothing here loads Electron, for the reason `window.test.ts` gives: this
// module's runtime is injected (`ipcMain` to the registrar, `ipcRenderer` to the
// bridge), and in the preload Electron is reached only from a guarded block that
// a Node environment never enters. The fakes are the whole of the substitution.
//
// The CLI side of the tables below was read off the cobra definitions on
// 2026-10-16 (`grep -rn 'Use:' internal/cli/*.go`), not from a design paragraph:
// the snapshot is this test's record of what main's tree actually has, and the
// never table's `where` column records where each entry was read.

import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import {
  ALLOWED_OPERATION_FLAGS,
  COMPATIBILITY_UNREADABLE,
  CONSOLE_OPERATIONS,
  CONSOLE_OPERATION_NAMES,
  IPC_CHANNEL_PREFIX,
  IPC_VERSION,
  MAX_ARGUMENT_KEYS,
  MAX_INSTALLATION_CHECKOUT_LENGTH,
  MAX_INSTALLATION_NAME_LENGTH,
  MAX_PAYLOAD_WIDTH,
  MAX_REPLY_REASON_LENGTH,
  MAX_REPOSITORY_LENGTH,
  MAX_URL_LENGTH,
  NEVER_REACHABLE,
  NO_ACTIVE_INSTALLATION,
  REDUCED_REFUSAL,
  REDUCED_VIEW_OPERATIONS,
  SENDER_REFUSAL,
  assertReachableSurface,
  channelForOperation,
  isConsoleSender,
  isConsoleSenderUrl,
  isSenderOnDocument,
  isNeverReachable,
  openPullRequestHandler,
  operationFor,
  readStoredCompatibility,
  reducedModeHandlers,
  registerConsoleHandlers,
  startupCompatibility,
  validateArguments,
} from '../../src/main/ipc'
import type {
  ConsoleEventLike,
  ConsoleHandler,
  ConsoleHandlers,
  ConsoleOperation,
  ConsoleReply,
  OperationName,
} from '../../src/main/ipc'
import {
  INSTALLATION_OPERATIONS,
  MAX_INSTALLATION_PATH_LENGTH,
  MAX_INSTALLATION_PROJECT_LENGTH,
  validateInstallationArguments,
} from '../../src/main/installations'
import { APP_ENTRY_URL } from '../../src/main/protocol'
import { SPAWN_KIND_FOR, SPAWN_COMMANDS, argvFor } from '../../src/main/spawn'
import {
  BRIDGE_VERSION,
  CONSOLE_BRIDGE_KEY,
  EXPOSED_OPERATIONS,
  createConsoleBridge,
  exposeConsoleBridge,
  hasRendererContext,
} from '../../src/preload/index'
import type {
  ConsoleBridge,
  ExposedMethod,
  ExposedName,
  RendererLike,
} from '../../src/preload/index'
import { RENDERER_UNREADABLE, compatibilityFrom } from '../../src/renderer/app'
import {
  REDUCED_VIEWS,
  classifyCliVersion,
  hidesView,
  unreadableCompatibility,
} from '../../src/shared/version'
import type { CliCompatibility } from '../../src/shared/version'

const SOURCE_ROOT = fileURLToPath(new URL('../../src', import.meta.url))

/**
 * codeOnly is the source of a file with its comments removed. Every scan below
 * is about what the code *does*, and a scan that matched prose would fail on a
 * comment that explains the very rule it is checking -- this file's modules
 * discuss `invoke`, `require` and Electron at length on purpose. Block comments
 * come out first, then `//` lines; the sources scanned are small, and none of
 * them holds a string literal with `//` in it.
 */
function codeOnly(relative: string): string {
  return readFileSync(join(SOURCE_ROOT, relative), 'utf8')
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .replace(/^\s*\/\/.*$/gm, '')
}

/**
 * sourceFiles is every `.ts` file under `console/src/`, as `path` (relative to
 * `src/`, POSIX-separated) and comment-stripped `source`. The scans at the
 * bottom of this file used to take a hand-written list of five names while
 * claiming to cover "anywhere in src/": a sixth file, or a new directory, was
 * silently outside the scan. This is the whole tree, so the claim and the check
 * are the same thing. `test/unit/configread.test.ts` walks the same tree for
 * rule 7's third prohibition.
 */
function sourceFiles(): readonly { readonly path: string; readonly source: string }[] {
  const walk = (directory: string, prefix: string): { path: string; source: string }[] => {
    const found: { path: string; source: string }[] = []
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const relative = prefix === '' ? entry.name : `${prefix}/${entry.name}`
      if (entry.isDirectory()) found.push(...walk(join(directory, entry.name), relative))
      else if (entry.name.endsWith('.ts')) found.push({ path: relative, source: codeOnly(relative) })
    }
    return found
  }
  return walk(SOURCE_ROOT, '')
}

/**
 * dynamicCodeOffences returns, for every file, the reasons the scan refuses it:
 * a `require` with a computed specifier, an `eval`, a `new Function`, or a child
 * process outside `main/spawn.ts`. It is a function over a list rather than a
 * loop in the test so that the test below can hand it the mutation directly, the
 * way `test/unit/configread.test.ts` holds its own planted file.
 */
export function dynamicCodeOffences(
  files: readonly { readonly path: string; readonly source: string }[],
): readonly string[] {
  const found: string[] = []
  for (const file of files) {
    if (/require\(\s*[^'"]/.test(file.source)) found.push(`${file.path}: a computed require`)
    if (/\beval\(/.test(file.source)) found.push(`${file.path}: eval`)
    if (/new Function\(/.test(file.source)) found.push(`${file.path}: new Function`)
    if (file.path !== 'main/spawn.ts' && file.source.includes('child_process')) {
      found.push(`${file.path}: a child process`)
    }
  }
  return found
}

/** recordingIpcMain is Electron's `ipcMain`, as far as this task uses it: a
 * registry of the channels that were handled, so a test can read back which
 * channels exist and deliver a message to one. The listener's event is typed,
 * because the surface's own type says it is: a fake that took `unknown` would
 * let a registration ignore the event and still typecheck. */
function recordingIpcMain() {
  const listeners = new Map<string, (event: ConsoleEventLike, ...args: unknown[]) => unknown>()
  const channels: string[] = []
  return {
    channels,
    listeners,
    handle(channel: string, listener: (event: ConsoleEventLike, ...args: unknown[]) => unknown): void {
      channels.push(channel)
      listeners.set(channel, listener)
    },
  }
}

/**
 * consoleEvent is an IPC message as the console's own window produces one: the
 * sending frame *is* the webContents' main frame, and its URL is the bundled
 * document's own `APP_ENTRY_URL` -- imported from `src/main/protocol.ts` rather
 * than spelled here, so a test cannot pass while the origin the gate checks and
 * the origin the console serves have drifted apart.
 *
 * Every forged event below starts from this one and changes exactly the field it
 * is about, which is what makes each of those tests a test of one check.
 */
function consoleEvent(): ConsoleEventLike {
  const frame = { url: APP_ENTRY_URL }
  return { senderFrame: frame, sender: { mainFrame: frame } }
}

/** fakeRenderer is Electron's `ipcRenderer`, as far as the preload uses it: one
 * `invoke`, which records the channel and the argument object it was given. */
function fakeRenderer(
  respond: (channel: string, args: unknown) => unknown = () => ({ ok: true, data: null }),
): RendererLike & { readonly calls: readonly { readonly channel: string; readonly args: unknown }[] } {
  const calls: { channel: string; args: unknown }[] = []
  return {
    calls,
    async invoke(channel: string, ...args: unknown[]): Promise<unknown> {
      calls.push({ channel, args: args[0] })
      return respond(channel, args[0])
    },
  }
}

/** stubHandlers is the whole surface's handlers, each answering an empty
 * document, so a test of the *plumbing* does not have to write fifteen. */
function stubHandlers(overrides: Record<string, ConsoleHandler> = {}): ConsoleHandlers {
  const handlers: Record<string, ConsoleHandler> = {}
  for (const operation of CONSOLE_OPERATIONS) handlers[operation.name] = () => ({})
  return { ...handlers, ...overrides } as ConsoleHandlers
}

/** flagsOf widens an operation's flags to a plain list of strings: the table's
 * literal types admit only the flags that entry actually fixes, and a test that
 * asks "does this operation fix --follow" is asking about a flag it does not
 * have. */
function flagsOf(operation: ConsoleOperation): readonly string[] {
  return operation.flags
}

/**
 * wiredConsole is the console end to end as far as Task 1.2 builds it: the
 * registrar puts the listeners on a fake `ipcMain`, the bridge is built over a
 * fake `ipcRenderer` that delivers each `invoke` to the listener for that
 * channel, and what a test holds is what a renderer would actually get. It is
 * what makes the "no channel from the renderer" claim testable rather than
 * argued: the only way to a channel in here is through a method of the bridge.
 */
function wiredConsole(overrides: Record<string, ConsoleHandler> = {}) {
  const ipcMain = recordingIpcMain()
  const handlers = stubHandlers(overrides)
  registerConsoleHandlers(ipcMain, handlers)
  const calls: { channel: string; args: unknown }[] = []
  const renderer: RendererLike = {
    async invoke(channel: string, ...args: unknown[]): Promise<unknown> {
      calls.push({ channel, args: args[0] })
      const listener = ipcMain.listeners.get(channel)
      if (listener === undefined) throw new Error(`nothing handles ${channel}`)
      return listener(consoleEvent(), args[0])
    },
  }
  return { bridge: createConsoleBridge(renderer), calls, handlers, ipcMain }
}

/** asCallable widens a bridge method to a function of anything, so a test can
 * hand the preload something a renderer would have to try hard to hand it. */
function asCallable(method: unknown): (args?: unknown) => Promise<ConsoleReply> {
  return method as (args?: unknown) => Promise<ConsoleReply>
}

// ---------------------------------------------------------------------------
// The surface the preload exposes
// ---------------------------------------------------------------------------

/**
 * assertClosedSurface is never called, and it is not a test: it is the
 * compile-time half of rule 5's closed surface, and the thing that checks it is
 * `npm run typecheck` (or the `tsc --noEmit` the review round runs), not vitest.
 *
 * The runtime tests below pin the fifteen names and the fifteen methods against
 * `CONSOLE_OPERATIONS`, and that is exactly why the defect this function guards
 * was silent: the object is built from the table, so no *extra* method can
 * exist at runtime, and a *misspelled* one is not a runtime question at all --
 * it is a line of a screen's code that the compiler is supposed to refuse.
 *
 * Every `@ts-expect-error` below is an error only while `EXPOSED_OPERATIONS`'s
 * literals survive into `ExposedName`, `ExposedMethod` and `ConsoleBridge`. Give
 * the table a type annotation and they all widen to `string`, each directive
 * becomes unused, and `tsc` fails with "Unused '@ts-expect-error' directive" --
 * a red `npm run typecheck` rather than a green test, which is the correct
 * place for a claim about types.
 */
function assertClosedSurface(bridge: ConsoleBridge, method: ExposedMethod): void {
  // The positive half, first: a real method, called the way a screen calls it,
  // answering the one shape every method answers.
  const reply: Promise<ConsoleReply> = bridge.listRuns({ repo: 'acme/app' })
  void reply

  // A misspelling is a compile error and not an `undefined` call inside a
  // screen's render, which is what an index signature would have made it.
  // @ts-expect-error `showBudgte` is not one of the fifteen method names
  void bridge.showBudgte
  // @ts-expect-error there is no generic `invoke`: no method takes a channel
  void bridge.invoke
  // @ts-expect-error and no `send`, `on`, `emit` or `channel` either
  void bridge.send

  // The two unions are unions, in both halves of the table.
  // @ts-expect-error `runs.listt` is not one of the fifteen operation names
  const notAName: ExposedName = 'runs.listt'
  // @ts-expect-error `showBudgte` is not one of the fifteen method names
  const notAMethod: ExposedMethod = 'showBudgte'
  void notAName
  void notAMethod

  // `operations` carries those names, so asking about anything else is refused
  // -- and a screen can switch on one exhaustively, which is what the list is
  // for.
  // @ts-expect-error `run.list` is not a name the list could hold
  void bridge.operations.includes('run.list')

  // And the method names are literals, so a screen can switch on one.
  switch (method) {
    case 'subscribeLive':
      break
    // @ts-expect-error `subscribeLiv` is not one of the fifteen method names
    case 'subscribeLiv':
      break
  }
}

describe('the frozen surface the preload exposes', () => {
  it('declares the table with literal types, so the unions under it are not `string`', () => {
    const source = codeOnly('preload/index.ts')
    const start = source.indexOf('export const EXPOSED_OPERATIONS')
    const end = source.indexOf('export type ExposedName')
    expect(start).toBeGreaterThan(-1)
    expect(end).toBeGreaterThan(start)
    const declaration = source.slice(start, end)
    // The rows must be `as const`, and the declaration must carry no type
    // annotation: an annotation replaces the literals with itself, which is the
    // one edit that makes `ExposedName`, `ExposedMethod` and the bridge's mapped
    // type all `string` at once. The compiler is what checks the consequence
    // (`assertClosedSurface` above); this is the scan that says so out loud in
    // the suite a reader actually runs.
    expect(declaration).toContain('as const')
    expect(declaration.split('\n')[0]).not.toContain(':')
    // `satisfies` is the second half and is not decoration either: measured on
    // 2026-10-16, a row with a third element (`['live.watch', 'subscribeLive',
    // 'extra']`) typechecks clean with the clause removed, because `[number][0]`
    // and `[number][1]` are still the literals a screen wants -- the extra
    // element is simply never looked at.
    expect(declaration).toContain('satisfies')
  })

  it('exposes one method per operation of the table, and no other method', () => {
    const bridge = createConsoleBridge(fakeRenderer())
    const methods = Object.entries(bridge)
      .filter(([, value]) => typeof value === 'function')
      .map(([key]) => key)
      .sort()
    expect(methods).toEqual(CONSOLE_OPERATIONS.map((operation) => operation.method).sort())
    expect(methods).toHaveLength(CONSOLE_OPERATIONS.length)
  })

  it('exposes the operation names, so a screen can see what it is talking to', () => {
    const bridge = createConsoleBridge(fakeRenderer())
    expect([...bridge.operations].sort()).toEqual([...CONSOLE_OPERATION_NAMES].sort())
    expect(bridge.operations).toHaveLength(CONSOLE_OPERATIONS.length)
  })

  it('names no operation twice, in either file', () => {
    const names = CONSOLE_OPERATIONS.map((operation) => operation.name)
    const methods = CONSOLE_OPERATIONS.map((operation) => operation.method)
    expect(new Set(names).size).toBe(names.length)
    expect(new Set(methods).size).toBe(methods.length)
    expect(new Set(EXPOSED_OPERATIONS.map(([name]) => name)).size).toBe(EXPOSED_OPERATIONS.length)
    expect(new Set(EXPOSED_OPERATIONS.map(([, method]) => method)).size).toBe(
      EXPOSED_OPERATIONS.length,
    )
  })

  it('is frozen, and so is the list of names inside it', () => {
    const bridge = createConsoleBridge(fakeRenderer())
    expect(Object.isFrozen(bridge)).toBe(true)
    expect(Object.isFrozen(bridge.operations)).toBe(true)
    expect(() => {
      Object.assign(bridge, { subscribeLive: null })
    }).toThrow(TypeError)
  })

  it('carries the table version, and the two files agree on it', () => {
    expect(BRIDGE_VERSION).toBe(IPC_VERSION)
    expect(createConsoleBridge(fakeRenderer()).version).toBe(IPC_VERSION)
  })

  it('has no generic invoke, no send, no on, and no channel-taking method', () => {
    const bridge = createConsoleBridge(fakeRenderer())
    for (const forbidden of [
      'invoke',
      'send',
      'sendSync',
      'on',
      'once',
      'off',
      'emit',
      'postMessage',
      'require',
      'channel',
    ]) {
      expect(forbidden in bridge, `${forbidden} must not be exposed`).toBe(false)
    }
    for (const [method, value] of Object.entries(bridge)) {
      if (typeof value !== 'function') continue
      expect(value.length, `${method} must take at most one argument (the operation's)`).toBeLessThanOrEqual(1)
    }
  })

  it('puts exactly one key on the renderer world, and it is the frozen bridge', () => {
    const exposed: { key: string; api: unknown }[] = []
    const contextBridge = {
      exposeInMainWorld(key: string, api: unknown): void {
        exposed.push({ key, api })
      },
    }
    const renderer = fakeRenderer()
    exposeConsoleBridge(contextBridge, renderer)
    expect(exposed.map((entry) => entry.key)).toEqual([CONSOLE_BRIDGE_KEY])
    const api = exposed[0]?.api
    expect(Object.isFrozen(api)).toBe(true)
    expect(Object.keys(api as object).sort()).toEqual(
      ['operations', 'version', ...CONSOLE_OPERATIONS.map((operation) => operation.method)].sort(),
    )
    // Nothing was invoked by exposing it: the surface is inert until a screen
    // calls a method.
    expect(renderer.calls).toHaveLength(0)
  })

  it('routes every method to its own operation channel, and to nothing else', async () => {
    const renderer = fakeRenderer()
    const bridge = createConsoleBridge(renderer)
    for (const [method, value] of Object.entries(bridge)) {
      if (typeof value !== 'function') continue
      await asCallable(value)({})
      const call = renderer.calls.at(-1)
      const operation = CONSOLE_OPERATIONS.find((candidate) => candidate.method === method)
      expect(operation, `${method} is not an operation's method`).toBeDefined()
      if (operation === undefined) continue
      expect(call?.channel).toBe(channelForOperation(operation.name))
      expect(call?.channel.startsWith(IPC_CHANNEL_PREFIX)).toBe(true)
    }
    expect(renderer.calls).toHaveLength(CONSOLE_OPERATIONS.length)
  })

  it('forwards the arguments object untouched, and answers the reply it was given', async () => {
    const document = { runs: [{ run_id: '20261016-121029-4a1b' }] }
    const renderer = fakeRenderer(() => ({ ok: true, data: document }))
    const bridge = createConsoleBridge(renderer)
    const args = { repo: 'acme/app' }
    const reply = await asCallable(bridge.subscribeLive)(args)
    expect(renderer.calls.at(-1)?.args).toBe(args)
    expect(reply).toEqual({ ok: true, data: document })
    expect(reply.ok && reply.data).toBe(document)
  })

  it('answers a refusal, never a rejection, when the channel is gone', async () => {
    const bridge = createConsoleBridge({
      async invoke(): Promise<unknown> {
        throw new Error('the window is closing')
      },
    })
    const reply = await asCallable(bridge.subscribeLive)({})
    expect(reply.ok).toBe(false)
    expect(reply.ok === false && reply.reason.length > 0).toBe(true)
  })

  it('refuses a reply that is not a console reply', async () => {
    for (const nonsense of [undefined, null, 42, 'ok', [], { data: 1 }, { ok: 'yes' }]) {
      const bridge = createConsoleBridge(fakeRenderer(() => nonsense))
      const reply = await asCallable(bridge.subscribeLive)({})
      expect(reply.ok, `${JSON.stringify(nonsense) ?? 'undefined'} is not a reply`).toBe(false)
    }
  })

  it('cannot be handed a channel: a string where the arguments object goes is refused by main', async () => {
    const { bridge, calls } = wiredConsole()
    const reply = await asCallable(bridge.subscribeLive)(channelForOperation('live.watch'))
    expect(reply.ok).toBe(false)
    expect(calls.map((call) => call.channel)).toEqual([channelForOperation('live.watch')])
  })

  it('is inert outside a renderer: nothing is exposed and Electron is never touched', () => {
    expect(hasRendererContext(globalThis)).toBe(false)
    expect(hasRendererContext({ window: {}, document: {} })).toBe(true)
    expect(hasRendererContext({ window: {} })).toBe(false)
    expect((globalThis as { fugaro?: unknown }).fugaro).toBeUndefined()
  })
})

// ---------------------------------------------------------------------------
// The allow-list's read-only invariant, against the CLI tree this console drives
// ---------------------------------------------------------------------------

/**
 * CliCommandClass is this test's reading of one command of `internal/cli`:
 *
 *   - `allowed` -- design section 5.1 rule 6's exact-argv allow-list admits it,
 *     which is why an operation may drive it;
 *   - `mutating` -- it writes something: cloud state, the checkout, the
 *     operator's machine. `NEVER_REACHABLE` must name it;
 *   - `secret` -- it handles a credential or a secret value. It need not mutate
 *     anything and the console still must never reach it;
 *   - `retired` -- the tree still defines it, and it does nothing: the old
 *     first-run `init` is a hidden tombstone that applies nothing and exits 1,
 *     naming `fugaro setup` and `fugaro update`. It must be on
 *     `NEVER_REACHABLE` like a writer
 *     -- a verb that refuses every argv is not a read the console could make --
 *     but calling it a write would be a lie about the tree, which is the same
 *     drift the never table exists to catch;
 *   - `unused` -- read-only, and no screen of the console consumes it. It is
 *     simply not on the allow-list, and nothing may drive it.
 */
type CliCommandClass = 'allowed' | 'mutating' | 'secret' | 'retired' | 'unused'

interface CliCommand {
  readonly command: string
  readonly kind: CliCommandClass
  readonly where: string
}

/**
 * CLI_COMMANDS is every leaf command `internal/cli` had on 2026-10-16
 * (`grep -rn 'Use:' internal/cli/*.go`, parents excluded), classified. Its three
 * converge rows were re-read on 2026-10-21, after task W1.2 of
 * `docs/setup-wizard/implementation-plan.md` split the old first-run `init` into
 * `fugaro setup` and `fugaro update` and left it a hidden tombstone. It is the
 * other half of the allow-list's invariant: nothing off this list may be driven
 * (there is no command the console could drive that is not here), and nothing on
 * it that is not `allowed` may be driven either. Keeping the list complete is
 * what makes "the console is read-only" a fact about the surface rather than a
 * claim about the argvs we happened to think of.
 *
 * `coordinator status` was already a leaf of this CLI on that date; it is listed
 * as `allowed` from Bitbucket credentials T1.2, when the project view became the
 * first screen to read it.
 */
const CLI_COMMANDS: readonly CliCommand[] = [
  // The thirteen dashboard commands the design's allow-list admits (fourteen
  // argv: `logs` twice, and console L5b's `runs stats`), and the one document
  // each draws. The picker's own read
  // (`config list --json`) is deliberately not here: it is not a dashboard
  // operation, and it arrived on 2026-10-18, after this snapshot's date.
  { command: 'watch', kind: 'allowed', where: 'internal/cli/watch.go:134' },
  { command: 'budget show', kind: 'allowed', where: 'internal/cli/budget.go:214' },
  { command: 'report', kind: 'allowed', where: 'ee/report/cli/report.go:70' },
  { command: 'runs ls', kind: 'allowed', where: 'internal/cli/runs.go:87' },
  { command: 'runs stats', kind: 'allowed', where: 'internal/cli/runs_stats.go:110' },
  // Plan GV2.1: the graph screen's read, and the one command of this list whose
  // subject is the *checkout* rather than the project -- it walks the graph
  // artifacts under the checkout's own `docs/`, so its cwd is part of the
  // question (`internal/cli/plan_status.go:58`).
  { command: 'plan status', kind: 'allowed', where: 'internal/cli/plan_status.go:58' },
  { command: 'diagnose', kind: 'allowed', where: 'internal/cli/diagnose.go:87' },
  { command: 'logs', kind: 'allowed', where: 'internal/cli/logs.go:45' },
  { command: 'secrets ls', kind: 'allowed', where: 'internal/cli/secrets.go:536' },
  { command: 'accounts ls', kind: 'allowed', where: 'internal/cli/accounts.go:70' },
  { command: 'version', kind: 'allowed', where: 'internal/cli/root.go:84' },
  { command: 'doctor', kind: 'allowed', where: 'internal/cli/doctor.go:159' },
  // Bitbucket credentials T1.2: the project view's read, the one command here
  // whose subject is the coordinator's *stored* view rather than a live read.
  { command: 'coordinator status', kind: 'allowed', where: 'ee/coordinator/cli/coordinator.go:565' },
  // Coordinator v2 C3: the pull request read, next to the stored view's. It is
  // `allowed` and not `unused` because the surface drives it -- and it is the
  // one command here that mints a credential of the host's, which is exactly
  // why its argv is pinned (the repository, the number and `--json`, and no
  // token field anywhere).
  { command: 'coordinator show', kind: 'allowed', where: 'ee/coordinator/cli/coordinator_show.go:46' },
  // **The console's one write** (design §5.1 rule 11, D22). `run` was the
  // first writer in this list and the one no argv could drive; since V2.5.1a it
  // is a command the allow-list admits, in exactly one argv, so it is `allowed`
  // and the test below reads that argv back rather than asserting it is
  // unreachable.
  { command: 'run', kind: 'allowed', where: 'internal/cli/run.go:134' },
  { command: 'cancel', kind: 'mutating', where: 'internal/cli/cancel.go:72' },
  // The converge is two doors since W1.2 (`fugaro setup` is the first run and
  // creates; `fugaro update` re-converges and creates nothing) and both apply
  // Terraform, so both are writers; the word they replaced is a tombstone.
  { command: 'setup', kind: 'mutating', where: 'internal/cli/setup.go:235' },
  { command: 'update', kind: 'mutating', where: 'internal/cli/update.go:58' },
  { command: 'init', kind: 'retired', where: 'internal/cli/retired.go:39 (hidden)' },
  { command: 'upgrade', kind: 'mutating', where: 'internal/cli/upgrade.go:79' },
  { command: 'exec', kind: 'mutating', where: 'internal/cli/exec.go:52 (hidden)' },
  { command: 'verify', kind: 'mutating', where: 'internal/cli/verify.go:16' },
  { command: 'update-skills', kind: 'mutating', where: 'internal/cli/updateskills.go:46' },
  { command: 'config init', kind: 'mutating', where: 'internal/cli/config_init.go:80' },
  { command: 'config publish', kind: 'mutating', where: 'internal/cli/config_publish.go:31' },
  { command: 'recipes publish', kind: 'mutating', where: 'internal/cli/recipes.go:327' },
  { command: 'image build', kind: 'mutating', where: 'internal/cli/image.go:77' },
  { command: 'image gate', kind: 'mutating', where: 'internal/cli/image_record.go:63 (hidden)' },
  { command: 'image record', kind: 'mutating', where: 'internal/cli/image_record.go:148 (hidden)' },
  { command: 'image refresh', kind: 'mutating', where: 'internal/cli/image_refresh.go:330' },
  { command: 'budget set', kind: 'mutating', where: 'internal/cli/budget.go:703' },
  { command: 'budget kill', kind: 'mutating', where: 'internal/cli/budget.go:1121' },
  { command: 'budget resume', kind: 'mutating', where: 'internal/cli/budget.go:1117' },
  { command: 'budget history', kind: 'mutating', where: 'internal/cli/history.go:74 (hidden)' },
  { command: 'secrets set', kind: 'mutating', where: 'internal/cli/secrets.go:97' },
  // Credentials.
  { command: 'image git-credential', kind: 'secret', where: 'internal/cli/gitcred.go:42 (hidden)' },
  // Read-only, and no screen of this console consumes them.
  { command: 'validate', kind: 'unused', where: 'internal/cli/validate.go:353' },
  { command: 'config example', kind: 'unused', where: 'internal/cli/configcmd.go:12' },
  { command: 'config show', kind: 'unused', where: 'internal/cli/config_show.go:56' },
  { command: 'config layer', kind: 'unused', where: 'internal/cli/config_show.go:201' },
  { command: 'image render', kind: 'unused', where: 'internal/cli/image.go:465' },
  { command: 'image selftest', kind: 'unused', where: 'internal/cli/image.go:45 (hidden)' },
  { command: 'image check', kind: 'unused', where: 'internal/cli/imagecheck.go:64' },
  { command: 'image status', kind: 'unused', where: 'internal/cli/imagecheck.go:929' },
  { command: 'recipes ls', kind: 'unused', where: 'internal/cli/recipes.go:257' },
  { command: 'recipes show', kind: 'unused', where: 'internal/cli/recipes.go:292' },
  { command: 'recipes validate', kind: 'unused', where: 'internal/cli/recipes.go:443' },
  { command: 'budget preview', kind: 'unused', where: 'internal/cli/budget_preview.go:70' },
  { command: 'budget prices', kind: 'unused', where: 'internal/cli/budget.go:1221' },
]

/**
 * NOT_ON_THIS_TREE is the rows of `NEVER_REACHABLE` that name commands main's
 * CLI does not have yet. They are in the never table on purpose -- the design's
 * never list and the project-centric plan name them, and a future command of
 * that name must find the allow-list already closed to it -- and they are listed
 * here so that (a) a typo in either table fails, and (b) the day one of them
 * appears in `internal/cli`, this test says so instead of the name quietly
 * becoming real.
 */
const NOT_ON_THIS_TREE: readonly string[] = [
  'secrets get',
  'secrets rm',
  'secrets copy',
  'secrets check',
  'project policy publish',
  'project repos add',
  'project repos rm',
  'project sync',
  'sync',
]

/**
 * DESIGN_ARGV is design section 5.1 rule 6's allow-list, written as the argv each
 * operation fixes: `RUN` and `--repo R` are arguments the schema carries, and
 * `--dir D` on doctor is main-process state rather than a renderer argument, so
 * both are absent from these strings.
 *
 * **It is sixteen argvs, and this surface is the same sixteen.** The thirteenth is
 * `config list --json`, the rail's own read (owner's design ruling of
 * 2026-10-25): until that ruling there were twelve here and it was pinned where
 * the picker's table was, and the ruling made the read the dashboard's own
 * operation. The fourteenth is GV2.1's `plan status --json` (the graph
 * screen's per-task state; `console/docs/json-contract.md` §6.12), the
 * fifteenth is T1.2's `coordinator status --from-store --all --json`, whose two
 * extra words are fixed flags of the row rather than arguments
 * (`console/docs/json-contract.md` §6.13), and the sixteenth is V2.5.1a's
 * launch -- rule 11's exact argv with the words an argument fills
 * (`--run-id <run id>`, `--repo <owner/name>`, `--title=<T>`,
 * `--plan-task=<P>`) and the console's own state (`--project`, `--config`)
 * taken out, which is this file's convention for every row here.
 */
const DESIGN_ARGV: readonly string[] = [
  'watch --json',
  'budget show --json',
  'report --json',
  'runs ls --json',
  'runs stats --json',
  'diagnose --json',
  'logs --json',
  'logs --url',
  'secrets ls --json',
  'accounts ls --json',
  'version',
  'doctor --json',
  'config list --json',
  'plan status --json',
  'coordinator status --from-store --all --json',
  // The pull request read (coordinator v2 C3, D816): `--repo R` and `--pr N`
  // are the operation's two declared arguments, so this file's convention --
  // the command and the flags it *fixes*, with what an argument fills taken
  // out -- leaves the row as it is written here. The design's own row states
  // the whole argv, `--repo <owner/name> --pr <n> --json` and all.
  'coordinator show --json',
  // Rule 11's argv, `--task-file -` and all: the literal `-` is the word that
  // sends the task down the child's stdin rather than onto a command line.
  'run --task-file - --json',
]

/** argvOf is an operation's command and the flags it fixes, as one string. */
function argvOf(operation: ConsoleOperation): string | null {
  if (operation.command === null) return null
  return operation.flags.length === 0
    ? operation.command
    : `${operation.command} ${operation.flags.join(' ')}`
}

/** commandsDriven returns the operations whose command is this one. A command
 * path that *continues* the one asked for is that command's, which is what
 * makes the launch findable under `run`: its `command` is `run --task-file -`,
 * because those two words are the row's own and not flags a screen controls. */
function operationsDriving(command: string): readonly ConsoleOperation[] {
  return CONSOLE_OPERATIONS.filter(
    (operation) =>
      operation.command === command ||
      (operation.command !== null && operation.command.startsWith(`${command} `)),
  )
}

describe("the allow-list's read-only invariant", () => {
  it('is exactly the design section 3.4 rule 2 allow-list, argv for argv', () => {
    const driven = CONSOLE_OPERATIONS.map(argvOf).filter((argv): argv is string => argv !== null)
    expect([...driven].sort()).toEqual([...DESIGN_ARGV].sort())
    expect(driven).toHaveLength(DESIGN_ARGV.length)
  })

  it('has exactly nine operations spawn nothing at all, and names each of them', () => {
    // Task 3.3 added the handshake's answer, Task 3.4b `installation.current`
    // (plan step 6: "added to SHIPPED_SURFACE deliberately"), the owner's
    // design ruling of 2026-10-25 the rail's five store-and-window operations,
    // and K2a `link.openPullRequest`. The number is the claim: these rows are
    // the console's own state, and a tenth operation with no command would be a
    // surface that answers something no CLI read produced.
    const withoutCommand = CONSOLE_OPERATIONS.filter((operation) => operation.command === null)
    expect(withoutCommand.map((operation) => operation.name)).toEqual([
      'startup.compatibility',
      'installation.current',
      'installation.choose',
      'installation.checkout',
      'installation.remove',
      'installation.reveal',
      'installation.open',
      'link.openExternal',
      'link.openPullRequest',
    ])
    // Each of them, by name, so the test fails naming the row rather than on a
    // list that a reader has to diff.
    expect(withoutCommand.map((operation) => operation.method)).toEqual([
      'getCompatibility',
      'getInstallation',
      'chooseInstallation',
      'setInstallationCheckout',
      'removeInstallation',
      'revealInstallation',
      'openInstallations',
      'openExternal',
      'openPullRequest',
    ])
  })

  it('drives every CLI command with at most one operation, except logs', () => {
    // Two operations share `logs` and differ by their flags, which is the point
    // of writing the allow-list by exact argv: `logs RUN --json` and
    // `logs RUN --url` cannot be combined (`internal/cli/logs.go:63`), so the
    // surface must not be able to express that argv.
    const shared = CLI_COMMANDS.map((entry) => entry.command).filter(
      (command) => operationsDriving(command).length > 1,
    )
    expect(shared).toEqual(['logs'])
    expect(operationsDriving('logs').map((operation) => operation.flags.join(' ')).sort()).toEqual([
      '--json',
      '--url',
    ])
  })

  for (const entry of CLI_COMMANDS) {
    it(`drives ${entry.command} only because the design's allow-list admits it`, () => {
      const drivers = operationsDriving(entry.command)
      if (entry.kind === 'allowed') return
      expect(
        drivers.map((operation) => operation.name),
        `${entry.command} (${entry.where}) is ${entry.kind}: nothing in the allow-list may drive it`,
      ).toEqual([])
    })

    it(`knows that ${entry.command} is on the never table, or is simply unused`, () => {
      if (entry.kind === 'unused' || entry.kind === 'allowed') {
        // Neither is forbidden -- the console just has no screen for the
        // unused ones. What keeps them unreachable is that the surface is the
        // table: an operation is the only way to a command.
        expect(NEVER_REACHABLE.some((row) => row.value === entry.command)).toBe(false)
        return
      }
      expect(
        NEVER_REACHABLE.some((row) => row.value === entry.command),
        `${entry.command} (${entry.where}) is ${entry.kind} and must be on NEVER_REACHABLE`,
      ).toBe(true)
      expect(isNeverReachable({ command: entry.command, flags: [] })?.value).toBe(entry.command)
    })
  }

  it('drives `run` from the launch alone, and only by rule 11’s argv', () => {
    // **The turned-around half of the loop above.** Until V2.5.1a every test
    // here asserted that nothing drove `run`; the write is now drivable through
    // exactly one operation, and this names it. `operationsDriving` reads the
    // command path, which for the launch carries its two fixed words, so a
    // second operation under `run` -- or a launch whose words changed -- fails
    // here as well as at the argv pin below.
    const drivers = operationsDriving('run')
    expect(drivers.map((operation) => operation.name)).toEqual(['run.launch'])
    const driver = drivers[0]
    expect(driver === undefined ? null : argvOf(driver)).toBe('run --task-file - --json')
  })

  for (const command of NOT_ON_THIS_TREE) {
    it(`names ${command}, which this tree does not have yet, and does not pretend it does`, () => {
      expect(CLI_COMMANDS.some((entry) => entry.command === command)).toBe(false)
      expect(NEVER_REACHABLE.some((row) => row.value === command)).toBe(true)
    })
  }

  it('names only commands this tree has, or commands explicitly not on it', () => {
    for (const row of NEVER_REACHABLE) {
      // A flag row names a flag; an argv row names one word *of* a command, so
      // the command it belongs to is what has to exist. The positional-TEXT row
      // names no word at all, and its command is the one it is refused on.
      if (row.shape === 'flag') continue
      const known =
        row.shape === 'argv'
          ? CLI_COMMANDS.some((entry) => entry.command === row.command)
          : CLI_COMMANDS.some((entry) => entry.command === row.value)
      expect(known || NOT_ON_THIS_TREE.includes(row.value), `${row.value} (${row.where})`).toBe(true)
    }
  })

  it('gives every row provenance and a reason, and freezes the table', () => {
    expect(Object.isFrozen(NEVER_REACHABLE)).toBe(true)
    expect(NEVER_REACHABLE.length).toBeGreaterThan(30)
    for (const row of NEVER_REACHABLE) {
      expect(row.why.length, row.value).toBeGreaterThan(20)
      expect(row.where.length, row.value).toBeGreaterThan(5)
    }
    const values = NEVER_REACHABLE.map((row) => `${row.shape}:${row.value}`)
    expect(new Set(values).size).toBe(values.length)
  })

  it("accepts what the console drives, and still catches the mutations that matter", () => {
    // The mutation controls for the never table: a matcher that refused
    // everything would pass every test above and be worthless, and one that
    // matched nothing would pass the control below and be worthless too. Both
    // halves are asserted here.
    for (const operation of CONSOLE_OPERATIONS) {
      expect(isNeverReachable(operation), operation.name).toBeNull()
    }
    expect(isNeverReachable({ command: 'doctor', flags: ['--json'] })).toBeNull()
    expect(isNeverReachable({ command: 'runs ls', flags: ['--json'] })).toBeNull()
    expect(isNeverReachable({ command: 'logs', flags: ['--url'] })).toBeNull()
    expect(isNeverReachable({ command: 'version', flags: [] })).toBeNull()

    expect(isNeverReachable({ command: 'budget kill', flags: ['--json'] })?.value).toBe('budget kill')
    // **`run` is argv-shaped now.** `run --json` is not a vector anybody wrote
    // -- the launch's own argv carries `--task-file -` and `--run-id` as well --
    // so it is not refused, and each excluded word is, on its own row.
    expect(isNeverReachable({ command: 'run', flags: ['--json'] })).toBeNull()
    expect(isNeverReachable({ command: 'run', flags: ['--json', '--pr'] })?.value).toBe('--pr')
    expect(isNeverReachable({ command: 'run', flags: ['--json', '--retry'] })?.value).toBe('--retry')
    expect(isNeverReachable({ command: 'run', flags: ['--json', '--no-budget-check'] })?.value).toBe(
      '--no-budget-check',
    )
    // ...and the launch's own command path is not a `run` the table refuses,
    // even though it begins with one: it names none of the excluded words.
    expect(
      isNeverReachable({ command: 'run --task-file -', flags: ['--json'] }),
    ).toBeNull()
    // The words are matched wherever they are spelled, which is what keeps a
    // later edit from moving one out of `flags` and into the command path.
    expect(isNeverReachable({ command: 'run --task-file - --pr', flags: ['--json'] })?.value).toBe('--pr')
    expect(isNeverReachable({ command: 'doctor', flags: ['--json', '--check-github-app'] })?.value).toBe(
      '--check-github-app',
    )
    expect(
      isNeverReachable({ command: 'doctor', flags: ['--json', '--check-bitbucket-token'] })?.value,
    ).toBe('--check-bitbucket-token')
    expect(isNeverReachable({ command: 'logs', flags: ['--json', '--follow'] })?.value).toBe('--follow')
    expect(isNeverReachable({ command: 'runs ls', flags: ['--json', '--watch'] })?.value).toBe('--watch')
    // `runs ls` is not `run`: the command rows match exactly, and the allow-list
    // is by exact argv.
    expect(isNeverReachable({ command: 'runs ls', flags: ['--json'] })).toBeNull()
  })

  it('drives no command whose argv carries a never-table flag', () => {
    for (const operation of CONSOLE_OPERATIONS) {
      for (const flag of operation.flags) {
        expect(
          NEVER_REACHABLE.some((row) => row.shape === 'flag' && row.value === flag),
          `${operation.name} fixes ${flag}`,
        ).toBe(false)
      }
    }
  })

  it('fixes only the four flags the surface may fix, and never --url with --json', () => {
    for (const operation of CONSOLE_OPERATIONS) {
      for (const flag of operation.flags) {
        expect(ALLOWED_OPERATION_FLAGS, `${operation.name} fixes ${flag}`).toContain(flag)
      }
      const flags = flagsOf(operation)
      expect(flags.includes('--url') && flags.includes('--json'), operation.name).toBe(false)
    }
    // `--from-store` and `--all` are T1.2's two, fixed together on
    // `coordinator status` alone: the stored view and the project-wide wrapper
    // are one question, and no screen control turns either off. Widening this
    // list is what a new operation fixing a new flag costs, and the exact-argv
    // pin above is what keeps that from being a free widening.
    expect([...ALLOWED_OPERATION_FLAGS]).toEqual(['--json', '--url', '--from-store', '--all'])
  })
})

// ---------------------------------------------------------------------------
// The never-reachable table, one named test per row (the plan's red step)
// ---------------------------------------------------------------------------

describe('the never-reachable table', () => {
  for (const row of NEVER_REACHABLE) {
    it(`nothing in the allow-list drives ${row.value}`, () => {
      // This is the plan's red step: adding `budget kill` -- or any row here --
      // to CONSOLE_OPERATIONS makes this test, named for it, fail.
      const drives = (operation: ConsoleOperation): boolean => {
        if (row.shape === 'command') return operation.command === row.value
        if (row.shape === 'argv') {
          // A word of a command's argv: it matches wherever the operation
          // spells it, in the command path or in the flags it fixes.
          const under =
            operation.command === row.command ||
            (operation.command !== null && operation.command.startsWith(`${row.command ?? ''} `))
          return under && [...(operation.command?.split(' ') ?? []), ...flagsOf(operation)].includes(row.value)
        }
        return flagsOf(operation).includes(row.value)
      }
      const drivers = CONSOLE_OPERATIONS.filter(drives)
      expect(drivers.map((operation) => operation.name), row.where).toEqual([])
      if (row.shape === 'argv' && row.value === '') {
        // The positional-TEXT row is the one row with no word to match: `run`
        // refuses a positional word *together with* `--task-file`, so the fact
        // is that this row can never fire -- a command-level match would refuse
        // the very argv rule 11 writes. Its subject is checked from the other
        // side, in the launch's own argv test.
        expect(isNeverReachable({ command: 'run', flags: [] }), row.where).toBeNull()
        return
      }
      expect(
        isNeverReachable({
          command: row.shape === 'command' ? row.value : row.shape === 'argv' ? (row.command ?? null) : null,
          flags: row.shape === 'flag' || row.shape === 'argv' ? [row.value] : [],
        }),
      ).toBe(row)
    })
  }

  it('refuses a table with budget kill in it, before a window exists', () => {
    const mutated: readonly ConsoleOperation[] = [
      ...CONSOLE_OPERATIONS,
      {
        name: 'budget.kill',
        method: 'killBudget',
        command: 'budget kill',
        flags: ['--json'],
        summary: 'the mutation this whole file exists to catch',
        args: [],
      },
    ]
    expect(() => assertReachableSurface(mutated)).toThrow(/budget kill/)
    expect(() => assertReachableSurface(mutated)).toThrow(/never reach/)
  })

  it('refuses a second `run` argv, which is the mutation this fence moved', () => {
    // This used to plant an operation named `run.launch` driving `run`, because
    // *every* `run` argv was forbidden and a planted writer was the whole
    // question. The launch is real now, so the same mutation is the one that
    // still matters: a second operation under `run`, carrying a word the design
    // excludes. It fails before any window exists, which is the point of the
    // check being at registration.
    expect(() =>
      assertReachableSurface([
        ...CONSOLE_OPERATIONS,
        {
          name: 'run.retry',
          method: 'retryRun',
          command: 'run',
          flags: ['--json', '--retry'],
          summary: 'a writer nobody ruled on',
          args: [],
        },
      ]),
    ).toThrow(/--retry/)
  })

  it('refuses a table with a flag the never table does not even list', () => {
    // The other half of the flag check, and the one the row above does *not*
    // reach: `--check-github-app` is refused because it is on the never table,
    // so a `--mine` -- a flag no row names, and not one the surface may fix --
    // is what pins the allow-list half of the check (`ALLOWED_OPERATION_FLAGS`).
    const withMine: readonly ConsoleOperation[] = [
      ...CONSOLE_OPERATIONS,
      {
        name: 'runs.list.mine',
        method: 'listMine',
        command: 'runs ls',
        flags: ['--json', '--mine'],
        summary: 'a read that fixes a flag the surface does not fix',
        args: [],
      },
    ]
    expect(() => assertReachableSurface(withMine)).toThrow(/--mine/)
    expect(() => assertReachableSurface(withMine)).toThrow(/not a flag the surface may fix/)
  })

  // One test per flag row, named for the flag: the loop at the top of this
  // describe iterates the table, so a row *deleted* from it takes its test with
  // it and the suite stays green. These fail instead.
  for (const flag of ['--follow', '--check-github-app', '--check-bitbucket-token', '--strict', '--csv', '--watch', '--plain', '--poll', '--ascii', '--no-color']) {
    it(`carries a never-table row for ${flag}`, () => {
      const row = NEVER_REACHABLE.find((candidate) => candidate.shape === 'flag' && candidate.value === flag)
      expect(row, flag).toBeDefined()
      expect(row?.why.length, flag).toBeGreaterThan(20)
      expect(row?.where.length, flag).toBeGreaterThan(3)
    })
  }

  for (const command of ['budget kill', 'budget history', 'secrets set', 'setup', 'update', 'init', 'upgrade', 'exec']) {
    it(`carries a never-table row for ${command}`, () => {
      const row = NEVER_REACHABLE.find(
        (candidate) => candidate.shape === 'command' && candidate.value === command,
      )
      expect(row, command).toBeDefined()
      expect(row?.why.length, command).toBeGreaterThan(20)
    })
  }

  it('refuses a table with a flag that turns a read into something else', () => {
    expect(() =>
      assertReachableSurface([
        ...CONSOLE_OPERATIONS,
        {
          name: 'run.diagnose.public',
          method: 'diagnosePublic',
          command: 'doctor',
          flags: ['--json', '--check-github-app'],
          summary: 'a read that would fetch a private key',
          args: [],
        },
      ]),
    ).toThrow(/--check-github-app/)
  })

  it('refuses a table with two operations of one name', () => {
    const first = CONSOLE_OPERATIONS[0]
    expect(first).toBeDefined()
    if (first === undefined) return
    expect(() => assertReachableSurface([...CONSOLE_OPERATIONS, first])).toThrow(/appears twice/)
  })

  it('accepts the table the console actually has', () => {
    expect(() => assertReachableSurface(CONSOLE_OPERATIONS)).not.toThrow()
  })
})

// ---------------------------------------------------------------------------
// The argument schemas
// ---------------------------------------------------------------------------

/** WELL_FORMED is one accepted payload per operation. The mapped type is why a
 * new operation with no entry here is a compile error, and the table is what
 * keeps the refusal tables honest: a validator that refused everything would
 * pass every test below it. */
const WELL_FORMED: Record<OperationName, Record<string, unknown>> = {
  'live.watch': {},
  'budget.show': { repo: 'acme/app' },
  'spend.report': { by: 'model', since: '7d', until: '2026-10-16', repo: 'acme/app' },
  'runs.list': { repo: 'acme/app', since: '7d', mine: true, pr: 42, all: false },
  // No `--all-users`: the operation has no such key, because the command
  // resolves the view itself (see `CONSOLE_OPERATIONS`' own comment).
  'runs.stats': { since: '7d' },
  // No `path`, no `checkout`, no `dir`: the read's directory is the
  // installation's own, and this payload is the whole of what a renderer may
  // say to it (plan GV2.1, constraint C1).
  'plan.status': { all: false },
  // No `repo`, no `since`, no `path`: T1.2's read is the installation's whole
  // repository set, from the view the coordinator already stored, so this
  // payload is empty on purpose (Bitbucket credentials T1.2).
  'coordinator.status': {},
  // The two arguments the pull request read is asked with (coordinator v2 C3):
  // the repository as the CLI's own canonical `owner/name`, and the number as
  // the plain positive integer the `pullRequest` kind exists for. There is no
  // `url`, no `path` and no token field -- the read mints its own credential in
  // the child and says so in the console's own words.
  'coordinator.show': { repo: 'acme/app', pr: 42 },
  // The five fields, with the two optional ones left out -- which is the argv
  // rule 11 writes for a launch the operator gave no title and no plan task.
  'run.launch': { installation: 'sandbox', repository: 'acme/app', task: 'do the thing' },
  'run.diagnose': { run: '20261016-121029-4a1b' },
  'run.logs': { run: '20261016-121029-4a1b' },
  'run.logLink': { run: '20261016-121029-4a1b' },
  'secrets.inventory': { scope: 'repo', repo: 'acme/app' },
  'accounts.inventory': { repo: 'acme/app' },
  'cli.version': {},
  'startup.compatibility': {},
  'installation.current': {},
  'environment.report': {},
  'link.openExternal': { url: 'https://github.com/acme/app/pull/1' },
  // The three fields the main process builds a pull request link from, and
  // **no URL**: a `url` key here would be a shape this operation does not have
  // (K2a, D815).
  'link.openPullRequest': { provider: 'github', repo: 'acme/app', number: 42 },
  // The installation rail (owner's design ruling of 2026-10-25). The project
  // names and the directory are invented, and the directory is absolute -- the
  // grammar decides the shape and nothing else, and whether the CLI ever listed
  // it is the main process's check against its own document.
  'installation.list': {},
  'installation.choose': { project: 'aurora' },
  'installation.checkout': { project: 'aurora', checkout: '/home/operator/src/aurora' },
  'installation.remove': { project: 'aurora' },
  'installation.reveal': { project: 'aurora' },
  'installation.open': {},
}

const RUN_ID = '20261016-121029-4a1b'

describe('the argument schemas', () => {
  it('accepts the well-formed payload of every operation', () => {
    for (const operation of CONSOLE_OPERATIONS) {
      const decision = validateArguments(operation.name, WELL_FORMED[operation.name])
      expect(decision.ok, `${operation.name}: ${decision.ok ? '' : decision.reason}`).toBe(true)
    }
  })

  it('accepts an absent payload as the empty one, where nothing is required', () => {
    for (const operation of CONSOLE_OPERATIONS) {
      const required = operation.args.filter((spec) => spec.required)
      const decision = validateArguments(operation.name, undefined)
      if (required.length === 0) {
        expect(decision.ok, operation.name).toBe(true)
      } else {
        expect(decision.ok, operation.name).toBe(false)
      }
    }
  })

  it('hands back a frozen object with a null prototype and only declared keys', () => {
    for (const operation of CONSOLE_OPERATIONS) {
      const decision = validateArguments(operation.name, WELL_FORMED[operation.name])
      if (!decision.ok) continue
      expect(Object.isFrozen(decision.args), operation.name).toBe(true)
      expect(Object.getPrototypeOf(decision.args), operation.name).toBeNull()
      const declared = operation.args.map((spec) => spec.key).sort()
      expect(Object.keys(decision.args).sort(), operation.name).toEqual(
        declared.filter((key) => Object.hasOwn(decision.args, key)),
      )
      expect((decision.args as { constructor?: unknown }).constructor, operation.name).toBeUndefined()
    }
  })

  it('declares no key that names a path, a command, a channel or a spawn', () => {
    for (const operation of CONSOLE_OPERATIONS) {
      for (const spec of operation.args) {
        for (const forbidden of ['path', 'dir', 'file', 'command', 'cmd', 'argv', 'channel', 'exec']) {
          expect(spec.key, `${operation.name}.${spec.key}`).not.toBe(forbidden)
        }
      }
    }
  })

  it('keeps every operation within the argument-count bound', () => {
    for (const operation of CONSOLE_OPERATIONS) {
      expect(operation.args.length, operation.name).toBeLessThanOrEqual(MAX_ARGUMENT_KEYS)
    }
  })

  it('gives every argument a kind, a requiredness and a reason', () => {
    for (const operation of CONSOLE_OPERATIONS) {
      expect(operation.summary.length, operation.name).toBeGreaterThan(10)
      for (const spec of operation.args) {
        expect(spec.why.length, `${operation.name}.${spec.key}`).toBeGreaterThan(20)
        expect(typeof spec.required, `${operation.name}.${spec.key}`).toBe('boolean')
      }
    }
  })

  const UNIVERSALLY_REFUSED: readonly (readonly [string, unknown])[] = [
    ['a bare string', 'fugaro:console/live.watch'],
    ['a number', 42],
    ['null', null],
    ['an array', []],
    ['a function', () => 1],
    ['a Date', new Date(0)],
    ['a Map', new Map([['repo', 'acme/app']])],
    ['an own __proto__ key', JSON.parse('{"__proto__":{"repo":"acme/app"}}')],
    ['a replaced prototype', Object.create({ repo: 'acme/app' })],
    ['an own constructor key', { constructor: 'acme/app' }],
    ['an own prototype key', { prototype: 'acme/app' }],
    ['more keys than the bound', Object.fromEntries(Array.from({ length: MAX_ARGUMENT_KEYS + 1 }, (_, i) => [`k${i}`, 1]))],
  ]

  for (const [label, payload] of UNIVERSALLY_REFUSED) {
    it(`refuses ${label}, for every operation`, () => {
      for (const operation of CONSOLE_OPERATIONS) {
        const decision = validateArguments(operation.name, payload)
        expect(decision.ok, `${operation.name} accepted ${label}`).toBe(false)
      }
    })
  }

  it('names the protection key it refused, rather than saying "unknown argument"', () => {
    // The refusal for `__proto__` is the one a reader is most likely to see in a
    // log and least likely to be able to guess: a payload with a prototype key
    // is not a payload with an undeclared key, and the reason says which one it
    // was. Removing the `PROTOTYPE_KEYS` branch would leave `constructor` and
    // `prototype` refused by the "takes no ..." branch and this test failing.
    for (const key of ['__proto__', 'constructor', 'prototype']) {
      const payload = key === '__proto__' ? JSON.parse('{"__proto__":{"repo":"acme/app"}}') : { [key]: 'acme/app' }
      const decision = validateArguments('runs.list', payload)
      expect(decision.ok, key).toBe(false)
      if (decision.ok) continue
      expect(decision.reason, key).toBe(`${key} is never an argument`)
    }
  })

  it('refuses a payload wider than the width bound, by name of the bound', () => {
    const wide = { url: `https://github.com/${'a'.repeat(MAX_PAYLOAD_WIDTH)}` }
    const decision = validateArguments('link.openExternal', wide)
    expect(decision.ok).toBe(false)
    expect(decision.ok === false && decision.reason).toMatch(
      new RegExp(`at most ${MAX_PAYLOAD_WIDTH} characters`),
    )
  })

  it('bounds the width above what the schema’s own per-kind bounds can add up to', () => {
    // The relation, not the number: the width bound must not be able to refuse a
    // payload that every per-kind bound would have accepted. The widest a legal
    // payload can be is one `link` (the largest kind) plus the rest at the
    // largest non-link bound, plus the keys' own names.
    const widestLegal = MAX_URL_LENGTH + (MAX_ARGUMENT_KEYS - 1) * MAX_REPOSITORY_LENGTH
    expect(MAX_PAYLOAD_WIDTH).toBeGreaterThan(widestLegal)
  })

  it('refuses the count-bound payload for the reason that names the bound', () => {
    const payload = Object.fromEntries(
      Array.from({ length: MAX_ARGUMENT_KEYS + 1 }, (_, i) => [`k${i}`, 1]),
    )
    const decision = validateArguments('cli.version', payload)
    expect(decision.ok).toBe(false)
    expect(decision.ok === false && decision.reason).toMatch(/at most 8 arguments/)
  })

  it('never echoes the value it refused, so a refusal cannot be made to repeat a payload', () => {
    const hostile = [
      '/etc/passwd',
      'acme/app‮',
      'budget kill',
      '<script>alert(1)</script>',
      'https://evil.test/​',
    ]
    for (const value of hostile) {
      for (const operation of CONSOLE_OPERATIONS) {
        const decision = validateArguments(operation.name, { repo: value, run: value, url: value })
        expect(decision.ok, `${operation.name} accepted ${value}`).toBe(false)
        if (decision.ok) continue
        expect(decision.reason, value).not.toContain(value)
      }
    }
  })

  it('takes no path, wherever a value goes, for every operation but the rail’s own two kinds', () => {
    const paths = ['/etc/passwd', '../../etc/passwd', '/work/repo/fugaro.yaml', 'file:///etc/passwd']
    for (const operation of CONSOLE_OPERATIONS) {
      for (const spec of operation.args) {
        // The rail's two kinds are the two whose subject is a *name the store
        // holds* rather than a value with a grammar of its own:
        //
        //   - `installationCheckout` is a directory by design -- the rail's
        //     "Choose a checkout" -- and the value is one the CLI itself
        //     listed; this grammar checks its shape and membership of that list
        //     is the main process's check against the document it last read
        //     (`picker.ts`; `installations-ipc.test.ts` holds it);
        //   - `installationName` is a bare word, and what keeps a path-shaped
        //     string harmless is the lookup: a name this console holds no
        //     record for is refused before anything else happens.
        //
        // Each has its own case below rather than a hole here.
        if (spec.kind === 'installationCheckout' || spec.kind === 'installationName') continue
        for (const path of paths) {
          const decision = validateArguments(operation.name, { [spec.key]: path })
          expect(decision.ok, `${operation.name}.${spec.key} accepted ${path}`).toBe(false)
        }
      }
    }
  })

  it('takes a directory for the rail’s checkout kind only when it is absolute, and nothing else', () => {
    // The shape and no more, which is the store's own rule for the field
    // (`isAbsolutePath`): an absolute path is admitted, and whether the CLI ever
    // offered it is decided in the main process against the list it last read.
    for (const path of ['/home/operator/src/aurora', '/tmp/scratch', '/Users/Ann Lee/src/aurora']) {
      expect(validateArguments('installation.checkout', { project: 'aurora', checkout: path }).ok, path).toBe(true)
    }
    for (const path of ['../../etc/passwd', 'file:///etc/passwd', 'src/aurora', '-x', '', '/etc/passwd\u0000']) {
      expect(validateArguments('installation.checkout', { project: 'aurora', checkout: path }).ok, JSON.stringify(path)).toBe(
        false,
      )
    }
  })

  it('takes no command and no flag name, wherever a value goes, for every operation but the rail’s project name', () => {
    const words = ['budget kill', 'run', '--repo', '-x', 'ls -la', 'fugaro run']
    for (const operation of CONSOLE_OPERATIONS) {
      for (const spec of operation.args) {
        // `installationName` is the one kind that is a **bare word**: an
        // installation may be called `run`, and the surface's protection there
        // is not the alphabet but the store -- the name is looked up, and a name
        // this console does not hold is refused (`picker.ts`). Five of the six
        // spellings below are refused by this kind too (a space, a leading `-`),
        // and the case beneath is the one that says which.
        if (spec.kind === 'installationName') continue
        for (const word of words) {
          const decision = validateArguments(operation.name, { [spec.key]: word })
          expect(decision.ok, `${operation.name}.${spec.key} accepted ${word}`).toBe(false)
        }
      }
    }
  })

  it('takes a project name for the rail’s name kind only as one word, and never a flag', () => {
    for (const name of ['aurora', 'aurora-2', 'orbit']) {
      expect(validateArguments('installation.choose', { project: name }).ok, name).toBe(true)
    }
    // A leading `-` is a value that changes a command, and every spelling with a
    // space is a second word: both are refused here, which is what leaves `run`
    // -- a legitimate installation name -- the only word of the six above this
    // kind admits.
    for (const name of ['budget kill', 'ls -la', 'fugaro run', '--repo', '-x', '']) {
      expect(validateArguments('installation.choose', { project: name }).ok, JSON.stringify(name)).toBe(false)
    }
  })

  it('answers for the rail’s two kinds exactly as the store’s own validator does', () => {
    // The rail's rows are the picker's own two kinds moved onto this table, and
    // the two validators are two spellings of one rule: `ipc.ts` cannot import
    // `installations.ts` (that module imports this one), so the bounds and the
    // two predicates are written twice, and this is the test that keeps the two
    // spellings one answer. A drift in either direction is a hole: a dashboard
    // more permissive than the store is a value the store then refuses late, and
    // one stricter refuses a name the CLI itself printed.
    expect(MAX_INSTALLATION_NAME_LENGTH).toBe(MAX_INSTALLATION_PROJECT_LENGTH)
    expect(MAX_INSTALLATION_CHECKOUT_LENGTH).toBe(MAX_INSTALLATION_PATH_LENGTH)

    const storeRow = (name: string): Parameters<typeof validateInstallationArguments>[0] => {
      const row = INSTALLATION_OPERATIONS.find((operation) => operation.name === name)
      if (row === undefined) throw new Error(`the store has no ${name} row`)
      return row
    }

    const names = [
      'aurora',
      'orbit',
      'aurora-2',
      'acme/app',
      'run',
      'Aurora',
      '',
      ' ',
      'aurora ',
      'aurora\t2',
      '-x',
      '--project',
      'budget kill',
      'ls -la',
      'aurora\n',
      'aurora\u0000',
      'aurora\u007f',
      'a'.repeat(MAX_INSTALLATION_PROJECT_LENGTH),
      'a'.repeat(MAX_INSTALLATION_PROJECT_LENGTH + 1),
      'a'.repeat(4097),
    ]
    for (const name of names) {
      const here = validateArguments('installation.choose', { project: name }).ok
      const there = validateInstallationArguments(storeRow('choose'), { project: name }).ok
      expect(here, `the two validators disagree about the project ${JSON.stringify(name)}`).toBe(there)
    }

    const checkouts = [
      '/home/operator/src/aurora',
      '/Users/Ann Lee/src/aurora',
      '/tmp/scratch',
      '/',
      'src/aurora',
      '../../etc/passwd',
      'file:///etc/passwd',
      'C:\\Users\\operator\\src\\aurora',
      '-x',
      '',
      '/etc/passwd\u0000',
      `/home/operator/${'a'.repeat(MAX_INSTALLATION_PATH_LENGTH)}`,
      `/home/operator/${'a'.repeat(MAX_INSTALLATION_PATH_LENGTH + 1)}`,
    ]
    for (const checkout of checkouts) {
      const here = validateArguments('installation.checkout', { project: 'aurora', checkout }).ok
      const there = validateInstallationArguments(storeRow('checkout'), { project: 'aurora', checkout }).ok
      expect(here, `the two validators disagree about the checkout ${JSON.stringify(checkout)}`).toBe(there)
    }
  })

  it('refuses a hidden character in a rail name, where the store’s own rule does not', () => {
    // The one asymmetry between the two validators, and it points one way. Every
    // string on this surface refuses a hidden character -- a control character,
    // a C1 control, an invisible format character, a line separator or an
    // unpaired surrogate (`hiddenCharacterReason`, `ipc.ts`) -- because a value
    // that renders as another value must not cross this boundary at all. The
    // store's rule for a project name is about argv safety and does not carry
    // that clause, so a name it holds can be one this surface refuses.
    //
    // That is reachable only from a hand-edited `installations.json`: a project
    // name is 1 to 40 of `a-z`, `0-9` and `-`, starting and ending with a letter
    // or a digit (`config.ProjectNameRE`), so the CLI cannot print one of these
    // and the store cannot hold a name the CLI did not print. Refusing it is the
    // fail-closed direction -- the rail draws the row and the click reports the
    // refusal -- and the test above is what keeps the *other* direction, a
    // dashboard looser than the store, impossible.
    for (const name of ['aurora\u202e', 'aurora\u200b', '\ufeffaurora', 'aurora\u00ad', 'aurora\u2028']) {
      expect(validateArguments('installation.choose', { project: name }).ok, JSON.stringify(name)).toBe(false)
      expect(
        validateArguments('installation.checkout', { project: 'aurora', checkout: `/home/operator/${name}` }).ok,
        JSON.stringify(name),
      ).toBe(false)
    }
  })

  it('refuses a directory in `plan.status`’s payload, and the refusal is this schema’s own key list', () => {
    // **The security claim of plan GV2.1 (constraint C1), as a test.** The plan
    // read is *about* the installation's checkout: the CLI resolves `docs/` from
    // the child's working directory, so a payload that could name a directory
    // would be a renderer choosing what the console reads -- and a document
    // would then describe a tree the operator never chose. The directory is not
    // a key of this schema, and the operation's argv cannot carry one
    // (`src/main/spawn.ts`'s row has no `--dir` and no positional word).
    //
    // The refusal is the key list's and not a rule about paths anywhere: a
    // payload may carry a path on the operation that *means* to take one
    // (`installation.checkout`, the rail's own door), and that control is below.
    // A mutation that added a `path`, `dir`, `checkout` or `cwd` key to
    // `CONSOLE_OPERATIONS['plan.status'].args` makes this case fail on that
    // word, which is what "the console cannot be asked to read a directory it
    // was handed" means as a test rather than as a review.
    for (const key of ['path', 'dir', 'checkout', 'cwd', 'directory', 'docs']) {
      const decision = validateArguments('plan.status', { ...WELL_FORMED['plan.status'], [key]: '/etc' })
      expect(decision.ok, `plan.status accepted a ${key}`).toBe(false)
      // The *reason* is half the claim: a refusal that came from the value's
      // grammar would still pass the line above while the key was there to be
      // filled in with a directory that happened to parse.
      expect(decision.ok ? '' : decision.reason, key).toBe(`plan.status takes no ${key}`)
    }
    // And the whole of what this operation declares, so a key that *is* admitted
    // and that could carry a directory fails here by name rather than needing a
    // reader to notice it above.
    expect(operationFor('plan.status').args.map((spec) => spec.key)).toEqual(['all'])
    // The near-miss that matters more than the six above: the declared key, with
    // a path in it. `all` is a boolean the argv builder turns into `--all`, and
    // a string here must not become a word.
    expect(validateArguments('plan.status', { all: '/etc' }).ok).toBe(false)
    expect(validateArguments('plan.status', { all: '--all' }).ok).toBe(false)
    // ...and the control, so the refusals above read as this operation's schema
    // rather than as a surface that refuses every path: `installation.checkout`
    // takes one, and its payload is the one place a renderer may spell it.
    expect(
      validateArguments('installation.checkout', {
        project: 'aurora',
        checkout: '/home/operator/src/aurora',
      }).ok,
    ).toBe(true)
  })

  it('pins `coordinator.show`’s two arguments, which are the whole of what it may send', () => {
    // **C3's read is the second operation here that mints a credential of the
    // host's** (the coordinator's own read-only installation token, D816), so
    // its payload is the whole of what a renderer can influence about the argv:
    // the repository and the number, both required, and no third key. A token, a
    // host, a path or a flag name is not a key of this schema, and a key it does
    // not declare is refused by name (`takes no key it does not declare` above);
    // the two it does declare are kinds that refuse what they are not.
    expect(operationFor('coordinator.show').command).toBe('coordinator show')
    expect(operationFor('coordinator.show').flags).toEqual(['--json'])
    expect(operationFor('coordinator.show').args.map((spec) => [spec.key, spec.kind, spec.required])).toEqual([
      ['repo', 'repository', true],
      ['pr', 'pullRequest', true],
    ])
    // Both are required: the CLI refuses an argv missing either (`--repo %q must
    // look like owner/name`, `--pr %d is not a pull request number`), so a
    // payload missing one is refused here, before a child exists.
    expect(validateArguments('coordinator.show', {}).ok).toBe(false)
    expect(validateArguments('coordinator.show', { repo: 'acme/app' }).ok).toBe(false)
    expect(validateArguments('coordinator.show', { pr: 42 }).ok).toBe(false)
    // ...and what each kind refuses: a repository that is not the CLI's own
    // `owner/name`, and a number that is not a pull request number.
    expect(validateArguments('coordinator.show', { repo: 'acme', pr: 42 }).ok).toBe(false)
    expect(validateArguments('coordinator.show', { repo: 'acme/app', pr: 0 }).ok).toBe(false)
    expect(validateArguments('coordinator.show', { repo: 'acme/app', pr: 1.5 }).ok).toBe(false)
    expect(validateArguments('coordinator.show', WELL_FORMED['coordinator.show']).ok).toBe(true)
  })

  it('takes no key it does not declare, for every operation', () => {
    for (const operation of CONSOLE_OPERATIONS) {
      for (const extra of ['path', 'command', 'channel', 'exec', 'spawn', 'shell', 'cwd', 'env']) {
        const decision = validateArguments(operation.name, { ...WELL_FORMED[operation.name], [extra]: 'x' })
        expect(decision.ok, `${operation.name} accepted ${extra}`).toBe(false)
      }
    }
  })

  it('refuses a string carrying a control character, wherever it goes', () => {
    for (const value of ['acme/app\n', 'acme/app\u0000', 'acme/app\r\n', '\u007facme/app']) {
      for (const operation of CONSOLE_OPERATIONS) {
        const decision = validateArguments(operation.name, {
          repo: value,
          run: value,
          by: value,
          scope: value,
          url: value,
        })
        expect(decision.ok, `${operation.name} accepted ${JSON.stringify(value)}`).toBe(false)
      }
    }
  })

  it('refuses a string carrying an invisible format character', () => {
    // Each of these renders as an ordinary string: a bidi override reorders what
    // a repository looks like, a zero-width space makes two different values
    // print identically, a BOM hides at the front of a URL. `new URL` parses the
    // last one happily, which is why this check runs before the parser.
    const invisible = [
      'acme/app‮',
      'acme/app​',
      '﻿acme/app',
      'acme/⁦app',
      'acme­/app',
      'acme⁩/app',
    ]
    for (const value of invisible) {
      for (const operation of CONSOLE_OPERATIONS) {
        const decision = validateArguments(operation.name, { repo: value, run: value, url: value })
        expect(decision.ok, `${operation.name} accepted U+${(value.codePointAt(value.length - 1) ?? 0).toString(16)}`).toBe(false)
      }
    }
  })

  it('refuses an unpaired surrogate', () => {
    for (const operation of CONSOLE_OPERATIONS) {
      const decision = validateArguments(operation.name, { repo: 'acme/\ud800app', url: 'https://github.com/\ud800' })
      expect(decision.ok, operation.name).toBe(false)
    }
  })
})

describe('the repository argument', () => {
  const cases: readonly (readonly [string, boolean, string])[] = [
    ['acme/app', true, 'the canonical form the CLI produces'],
    ['acme/app/nested', true, 'the CLI allows nested groups'],
    ['acme/app-web', true, 'dots, dashes and underscores are in the grammar'],
    ['acme/app.git', false, 'the CLI strips .git; accepting it would be a second spelling'],
    ['ACME/APP', false, 'the CLI lower-cases; a second spelling is a second identity'],
    ['acme/.git', false, 'a repository that reduces to nothing is not one'],
    ['acme/', false, 'an empty last segment'],
    ['/app', false, 'an empty first segment'],
    ['acme', false, 'no owner'],
    ['a/../b', false, 'a traversal segment'],
    ['a/./b', false, 'a self segment'],
    ['-acme/app', false, 'a leading dash would read as a flag'],
    ['.acme/app', false, 'a leading dot is not a repository name'],
    ['acme/аpp', false, 'a Cyrillic letter is not ASCII'],
    ['acme/app ', false, 'trailing whitespace'],
    ['acme/app\x7f', false, 'a control character'],
  ]

  for (const [value, accepted, why] of cases) {
    it(`${accepted ? 'accepts' : 'refuses'} ${JSON.stringify(value)} (${why})`, () => {
      const decision = validateArguments('runs.list', { repo: value })
      expect(decision.ok).toBe(accepted)
    })
  }

  it('accepts a repository at the length bound and refuses one past it', () => {
    const atBound = `${'a'.repeat(198)}/b`
    const pastBound = `${'a'.repeat(200)}/b`
    expect(atBound).toHaveLength(200)
    expect(validateArguments('runs.list', { repo: atBound }).ok).toBe(true)
    expect(validateArguments('runs.list', { repo: pastBound }).ok).toBe(false)
  })

  it('is required where the command cannot resolve a checkout, and optional elsewhere', () => {
    expect(validateArguments('accounts.inventory', {}).ok).toBe(false)
    expect(validateArguments('accounts.inventory', { repo: 'acme/app' }).ok).toBe(true)
    expect(validateArguments('runs.list', {}).ok).toBe(true)
  })
})

describe('the run id argument', () => {
  const cases: readonly (readonly [string, boolean, string])[] = [
    [RUN_ID, true, 'what task.NewRunID builds'],
    ['20261016-121029-4a1bcdef', true, 'a wider hex tail, if the CLI ever widens it'],
    ['20261016-121029-4a1', false, 'three hex digits'],
    ['20261016-121029', false, 'no hex tail'],
    ['20261016-121029-4A1B', false, 'uppercase hex is not what the CLI writes'],
    ['x20261016-121029-4a1b', false, 'a prefix'],
    [`${RUN_ID}\n`, false, 'a trailing newline'],
    ['20261016_121029_4a1b', false, 'underscores'],
    ['../../etc/passwd', false, 'a path'],
    [`${'a'.repeat(80)}-4a1b`, false, 'past the length bound'],
  ]

  for (const [value, accepted, why] of cases) {
    it(`${accepted ? 'accepts' : 'refuses'} ${JSON.stringify(value.slice(0, 40))} (${why})`, () => {
      const decision = validateArguments('run.diagnose', { run: value })
      expect(decision.ok).toBe(accepted)
    })
  }

  it('is required by the three operations that name a run', () => {
    for (const name of ['run.diagnose', 'run.logs', 'run.logLink'] as const) {
      expect(validateArguments(name, {}).ok, name).toBe(false)
      expect(validateArguments(name, { run: RUN_ID }).ok, name).toBe(true)
      expect(validateArguments(name, { run: 'not-a-run-id' }).ok, name).toBe(false)
    }
  })
})

describe('the day and window arguments', () => {
  const days: readonly (readonly [string, boolean])[] = [
    ['2026-10-16', true],
    ['2028-02-29', true],
    ['2026-02-29', false],
    ['2026-02-30', false],
    ['2026-13-01', false],
    ['2026-00-10', false],
    ['2026-10-00', false],
    ['2026-1-16', false],
    ['20261016', false],
    ['2026-10-16T00:00:00Z', false],
    ['today', false],
    ['', false],
  ]

  for (const [value, accepted] of days) {
    it(`${accepted ? 'accepts' : 'refuses'} the day ${JSON.stringify(value)}`, () => {
      expect(validateArguments('spend.report', { until: value }).ok).toBe(accepted)
    })
  }

  // The two `--since` flags are two different grammars (`reportRange` vs
  // `parseSince`), so there is a table per command and neither is written
  // against the other's: a kind shared between them was wrong in both
  // directions at once -- it refused `12w`, which `report`'s own help
  // advertises, and it admitted `2026-10-16`, which `parseSince` has no branch
  // for. Each row below is a value its own command's parser accepts or refuses.
  const reportWindows: readonly (readonly [string, boolean, string])[] = [
    ['2026-10-16', true, 'a UTC day, reportRange’s date branch'],
    ['30d', true, 'report’s own default'],
    ['12w', true, 'twelve weeks -- advertised in report’s own help'],
    ['3660d', true, 'exactly reportMaxDays'],
    ['522w', true, '3654 days, the last week inside reportMaxDays'],
    ['3661d', false, 'one day past reportMaxDays'],
    ['523w', false, '3661 days, past reportMaxDays'],
    ['0d', false, 'reportRange refuses a count below 1'],
    ['0', false, 'report --since has no “no bound”: 0 falls through to its date parser'],
    ['24h', false, 'report takes days and weeks, not hours'],
    ['90m', false, 'nor minutes'],
    ['7', false, 'a bare number is not a day count'],
    ['-7d', false, 'negative'],
    ['7d ', false, 'a trailing space'],
    ['all', false, 'a word'],
  ]

  for (const [value, accepted, why] of reportWindows) {
    it(`${accepted ? 'accepts' : 'refuses'} the report window ${JSON.stringify(value)} (${why})`, () => {
      expect(validateArguments('spend.report', { since: value }).ok).toBe(accepted)
    })
  }

  // `runs ls --since` is `parseSince` (`internal/cli/runs.go:502`) and this
  // table is read off it rather than invented: the CLI's own `TestParseSince`
  // cases (`7d`, `36h`, `90m`, `0` accepted; `""`, `d`, `-1d`, `7w` refused) are
  // all here, with the two forms the console used to accept and the CLI never
  // did -- **a UTC calendar day** and **a zero window** -- on the refusing side,
  // and the duration branch's own boundary (`1h30m`, `1.5h`, `300ms`, `+5h`)
  // on the accepting one. A form the CLI takes and the console refuses is a
  // read the operator cannot make, so the mirror may not be narrower either.
  const runWindows: readonly (readonly [string, boolean, string])[] = [
    ['0', true, 'parseSince’s own “no bound”'],
    ['7d', true, 'its default, and the first form its help lists'],
    ['36h', true, 'a Go duration in hours'],
    ['90m', true, 'a Go duration in minutes'],
    ['1d', true, 'one day'],
    ['2026-10-16', false, 'parseSince has no date branch: a UTC day is not a runs window'],
    ['12w', false, 'Go durations have no week unit'],
    ['0d', false, 'parseSince refuses a day count below 1'],
    ['0h', false, 'nor a duration of zero'],
    ['0m', false, 'nor zero minutes'],
    ['7', false, 'a bare number is neither a form'],
    ['-7d', false, 'negative'],
    ['7d ', false, 'a trailing space'],
    ['all', false, 'a word'],
    // the rest of its `d` branch
    ['+7d', true, 'strconv.Atoi takes a leading plus'],
    ['007d', true, 'and leading zeros'],
    ['7D', false, 'the suffix is lower-case in parseSince'],
    ['7dd', false, 'one unit'],
    ['7d5h', false, 'the `d` suffix is tried first, so this is not a duration'],
    ['1.5d', false, 'the day count is an integer'],
    // ...and the rest of its duration branch, which is Go’s ParseDuration
    ['1h30m', true, 'a compound duration'],
    ['1.5h', true, 'a fractional one'],
    ['300ms', true, 'milliseconds'],
    ['5s', true, 'seconds'],
    ['1us', true, 'microseconds, as `us`'],
    ['1µs', false, 'U+00B5 is a spelling the spawn alphabet refuses, so the schema refuses it first'],
    ['1μs', false, 'and so is U+03BC'],
    ['+5h', true, 'a leading plus'],
    ['0s', false, 'parseSince refuses a duration of zero however it is spelled'],
    ['0h0m', false, 'including a compound zero'],
    ['0.0s', false, 'and a fractional zero'],
    ['-5h', false, 'negative'],
    ['h', false, 'a unit with no number'],
    ['1h 30m', false, 'a space is not part of the grammar'],
    // ...and its magnitude: ParseDuration refuses a sum past 1<<63-1 ns
    // (`time: invalid duration`), and these were accepted at da86190. Every
    // value below was run through Go's time.ParseDuration to read off the edge.
    ['2562047h', true, 'the last whole hour ParseDuration accepts'],
    ['2562048h', false, 'one hour past int64 nanoseconds'],
    ['+2562048h', false, 'with a plus, too'],
    ['2562047h1h', false, 'the sum overflows, not either term'],
    ['153722867m', true, 'the last whole minute'],
    ['153722868m', false, 'one minute past it'],
    ['2562047.7h', true, 'a fraction under the edge'],
    ['2562047.8h', false, 'and one over it'],
    ['3000000h', false, 'far past it'],
    ['999999999h', false, 'a length bound does not bound the magnitude'],
    ['999999999m', false, 'in minutes either'],
    // ...and its length: the bound is the grammar's longest canonical value,
    // not ten characters (review round 2 of PR 514). Every accepted value below
    // is one Go's parseSince accepts (each was run through it).
    ['1000h30m15s', true, 'eleven characters, well inside the magnitude'],
    ['2562047h47m16.854775807s', true, 'the largest duration ParseDuration accepts, spelled out'],
    ['+2562047h47m16.854775807s', true, 'with a plus: the longest canonical form'],
    ['2562047h47m16.854775808s', false, 'one nanosecond past it: the overflow rule still holds'],
    // ...and its day count: `time.Duration(days) * 24 * time.Hour` wraps past
    // 106751 days (int64 nanoseconds), so parseSince refuses more than that
    // rather than reading a negative window, and the schema refuses it first.
    ['106751d', true, 'the last whole day int64 nanoseconds hold'],
    ['+106751d', true, 'with a plus, too'],
    ['106752d', false, 'one day past it: the duration would wrap negative'],
    ['12345678901d', false, 'an eleven-digit day count Atoi reads but the duration cannot hold'],
    ['9223372036854775807d', false, 'the largest day count Atoi reads'],
    ['9223372036854775808d', false, 'one past it: Atoi refuses, and so does the schema'],
    ['+2562047h47m16.8547758070s', false, 'a surplus fraction digit Go would drop: past the longest canonical form, so the length bound refuses it'],
  ]

  for (const [value, accepted, why] of runWindows) {
    it(`${accepted ? 'accepts' : 'refuses'} the runs window ${JSON.stringify(value)} (${why})`, () => {
      expect(validateArguments('runs.list', { since: value }).ok).toBe(accepted)
    })
  }

  it('refuses a calendar day for runs.list while accepting one for report', () => {
    // The two grammars side by side, because they are one word apart in the
    // schema and a validator that read the wrong one passed both tables above
    // before this pair existed.
    expect(validateArguments('runs.list', { since: '2026-10-16' }).ok).toBe(false)
    expect(validateArguments('spend.report', { since: '2026-10-16' }).ok).toBe(true)
    expect(validateArguments('runs.list', { since: '36h' }).ok).toBe(true)
    expect(validateArguments('spend.report', { since: '36h' }).ok).toBe(false)
  })

  it('gives each command its own window kind, so neither can drift into the other', () => {
    const kindsOf = (name: 'spend.report' | 'runs.list'): readonly string[] =>
      CONSOLE_OPERATIONS.filter((operation) => operation.name === name)
        .flatMap((operation) => operation.args.map((spec) => spec.kind))
        .filter((kind) => kind === 'reportSince' || kind === 'runSince')
    expect(kindsOf('spend.report')).toEqual(['reportSince'])
    expect(kindsOf('runs.list')).toEqual(['runSince'])
  })
})

describe('the closed sets', () => {
  const dimensions = ['day', 'week', 'month', 'year', 'repo', 'model', 'person', 'mode']
  for (const value of dimensions) {
    it(`accepts the report dimension ${value}`, () => {
      expect(validateArguments('spend.report', { by: value }).ok).toBe(true)
    })
  }
  for (const value of ['hour', 'Day', 'person ', '', 'repo;model']) {
    it(`refuses the report dimension ${JSON.stringify(value)}`, () => {
      expect(validateArguments('spend.report', { by: value }).ok).toBe(false)
    })
  }

  it('accepts a pull request number and refuses everything that is not one', () => {
    expect(validateArguments('runs.list', { pr: 42 }).ok).toBe(true)
    expect(validateArguments('runs.list', { pr: 1 }).ok).toBe(true)
    for (const value of [0, -1, 1.5, '42', Number.NaN, Number.POSITIVE_INFINITY, 1e9, null]) {
      expect(validateArguments('runs.list', { pr: value }).ok, String(value)).toBe(false)
    }
  })

  it('accepts a boolean for a flag and nothing else', () => {
    expect(validateArguments('runs.list', { mine: true }).ok).toBe(true)
    expect(validateArguments('runs.list', { mine: false }).ok).toBe(true)
    for (const value of ['true', 1, 0, null, {}]) {
      expect(validateArguments('runs.list', { mine: value }).ok, String(value)).toBe(false)
    }
  })

  it('requires the secret inventory to name its scope, and pairs the two halves', () => {
    expect(validateArguments('secrets.inventory', { scope: 'repo', repo: 'acme/app' }).ok).toBe(true)
    expect(validateArguments('secrets.inventory', { scope: 'project-wide' }).ok).toBe(true)
    expect(validateArguments('secrets.inventory', {}).ok).toBe(false)
    expect(validateArguments('secrets.inventory', { repo: 'acme/app' }).ok).toBe(false)
    expect(validateArguments('secrets.inventory', { scope: 'repo' }).ok).toBe(false)
    expect(
      validateArguments('secrets.inventory', { scope: 'project-wide', repo: 'acme/app' }).ok,
    ).toBe(false)
    expect(validateArguments('secrets.inventory', { scope: 'global' }).ok).toBe(false)
  })

  it('refuses the pairs the CLI refuses whatever else is set', () => {
    // `budget show --repo R --all` fails in the CLI (`internal/cli/budget.go:387`)
    // and `runs ls` refuses both `--repo`/`--all` and `--pr`/`--all`
    // (`internal/cli/runs.go:118,121`) before it reads anything else, so the
    // surface must not be able to build those argvs at all: a refusal that
    // arrives after a child has been spawned is the one outcome the schema
    // exists to prevent.
    expect(validateArguments('budget.show', { repo: 'acme/app', all: true })).toEqual({
      ok: false,
      reason: "--repo and --all don't go together",
    })
    expect(validateArguments('runs.list', { repo: 'acme/app', all: true })).toEqual({
      ok: false,
      reason: "--repo and --all don't go together",
    })
    expect(validateArguments('runs.list', { pr: 42, all: true })).toEqual({
      ok: false,
      reason: 'runs ls --pr needs --repo: a pull request number means something only in one repository',
    })
    // The other side of the boundary: either half alone is an argv the CLI
    // accepts, and so is a flag the payload set to `false` -- that is a flag the
    // argv does not carry (`argvFor` emits a boolean only when it is true).
    expect(validateArguments('budget.show', { repo: 'acme/app' }).ok).toBe(true)
    expect(validateArguments('budget.show', { all: true }).ok).toBe(true)
    expect(validateArguments('runs.list', { repo: 'acme/app' }).ok).toBe(true)
    expect(validateArguments('runs.list', { pr: 42 }).ok).toBe(true)
    expect(validateArguments('runs.list', { repo: 'acme/app', all: false }).ok).toBe(true)
    expect(validateArguments('runs.list', { pr: 42, all: false }).ok).toBe(true)
  })

  it('accepts `compute` on `budget show` alone, and a boolean and nothing else', () => {
    // The console half of `budget show --compute` (console 0.115, the Spend
    // caps read): one optional boolean on one row. The CLI admits the flag
    // beside both `--repo` and `--all` (`internal/cli/budget.go`), so the
    // surface admits both pairings -- and refuses neither pair, which is the
    // difference between this key and the two above.
    expect(validateArguments('budget.show', { compute: true }).ok).toBe(true)
    expect(validateArguments('budget.show', { compute: false }).ok).toBe(true)
    expect(validateArguments('budget.show', { repo: 'acme/app', compute: true }).ok).toBe(true)
    expect(validateArguments('budget.show', { all: true, compute: true }).ok).toBe(true)
    for (const value of ['yes', 'true', 1, 0, null, {}, []]) {
      expect(validateArguments('budget.show', { compute: value }).ok, String(value)).toBe(false)
    }
    // No other row gains it, including the two reads whose figures nobody
    // draws: `spend.report` (the rail's and the screen's own curve) and
    // `runs.list`. A key an operation does not declare is refused by name,
    // before a handler sees it and before an argv exists.
    for (const name of ['spend.report', 'runs.list', 'runs.stats', 'live.watch'] as const) {
      expect(validateArguments(name, { compute: true }).ok, name).toBe(false)
    }
    // The prototype keys are refused whatever the row declares, which is the
    // one class of key no schema can admit.
    expect(validateArguments('budget.show', JSON.parse('{"__proto__":{"compute":true}}')).ok).toBe(false)
    expect(validateArguments('budget.show', { constructor: 'yes' }).ok).toBe(false)
  })
})

describe('the spend read’s arguments', () => {
  // Desktop-console plan Task 2.4. The Spend screen's range picker and
  // breakdown switch are wired to `report --json` through the **existing**
  // `runReport` operation, so this task adds no operation, no channel and no
  // flag: it adds three *values* for three fields the table already declares.
  // What follows is the other half of that claim -- that the three fields are
  // closed, that a fourth key is refused, and that no value in them can spell
  // anything on the never-reachable table.

  /** SPEND_KEYS is exactly what `spend.report` declares: the breakdown the
   * switch draws, the range the picker draws, and `--repo`, which the design's
   * allow-list admits on this command and which this screen does not send. */
  const SPEND_KEYS = ['by', 'repo', 'since', 'until']

  it('declares the four keys the screen may send, and drives `report --json` and nothing else', () => {
    const operation = CONSOLE_OPERATIONS.find((one) => one.name === 'spend.report')
    expect(operation).toBeDefined()
    expect(operation?.args.map((spec) => spec.key).sort()).toEqual(SPEND_KEYS)
    expect(operation?.flags).toEqual(['--json'])
    expect(operation?.command).toBe('report')
    expect(operation?.method).toBe('runReport')
    // The argv main builds for the three fields, in the table's own order: the
    // renderer supplies values, never argv.
    expect(
      argvFor(
        'spend.report',
        { by: 'person', since: '12w', until: '2026-10-16' },
        { target: { project: 'fugaro' }, directory: '/tmp' },
      ),
    ).toEqual([
      'report',
      '--json',
      '--by',
      'person',
      '--since',
      '12w',
      '--until',
      '2026-10-16',
      '--project',
      'fugaro',
    ])
  })

  it('accepts the three fields together and refuses a fourth', () => {
    expect(validateArguments('spend.report', { by: 'person', since: '12w', until: '2026-10-16' })).toEqual({
      ok: true,
      args: { by: 'person', since: '12w', until: '2026-10-16' },
    })
    const refused: readonly (readonly [string, Record<string, unknown>])[] = [
      ['--csv, which the design forbids on this command', { by: 'day', csv: true }],
      ['a path', { by: 'day', path: '/etc/passwd' }],
      ['a second repository spelling', { by: 'day', repo: ['acme/app'] }],
      ['a command', { by: 'day', command: 'budget kill' }],
      ['a channel', { by: 'day', channel: 'fugaro:console/spend.report' }],
      ['a spawn', { by: 'day', spawn: 'report' }],
      ['a URL', { by: 'day', url: 'https://example.invalid/' }],
      ['an unknown breakdown', { by: 'hour' }],
      ['a window `report` does not parse', { since: '36h' }],
      ['a day that does not exist', { until: '2026-02-31' }],
      ['a window where a day belongs', { until: '7d' }],
    ]
    for (const [why, payload] of refused) {
      expect(validateArguments('spend.report', payload).ok, why).toBe(false)
    }
  })

  it('cannot reach a row of the never-reachable table through the three fields', () => {
    // The never-reachable table is re-run unchanged (Task 1.2): the fields are
    // values, so the thing to re-check is that no value a control can carry
    // spells a flag the surface must never fix.
    const flags = NEVER_REACHABLE.filter((row) => row.shape === 'flag')
    // `--csv` is the one the design names on this command's own line: `report
    // --json` and never `--csv`, because a second document format is a second
    // parser.
    expect(flags.map((row) => row.value)).toContain('--csv')
    for (const row of flags) {
      // The row is what it always was...
      expect(isNeverReachable({ command: 'report', flags: [row.value] })?.value, row.value).toBe(row.value)
      // ...and no value of the three fields is that flag.
      for (const payload of [{ by: row.value }, { since: row.value }, { until: row.value }]) {
        expect(validateArguments('spend.report', payload).ok, `${row.value} as ${Object.keys(payload)[0]}`).toBe(false)
      }
    }
    expect(() => assertReachableSurface(CONSOLE_OPERATIONS)).not.toThrow()
  })
})

/**
 * SHIPPED_SURFACE is the preload surface written out -- name and method, in the
 * table's order -- and it is the frozen list the plan asks for: it is spelled
 * here rather than read back from `EXPOSED_OPERATIONS`, because a test that
 * compared the table with itself would hold for a table that had just gained a
 * channel. The claim is "these twenty-six, and no others".
 *
 * Task 2.4 asserted the twelve and said the spend controls widened nothing;
 * Task 3.3 added the thirteenth (`startup.compatibility` / `getCompatibility`,
 * the handshake's answer the renderer gates on); Task 3.4b added the fourteenth
 * (`installation.current` / `getInstallation`, the installation the operator
 * chose, which the header names) and left the other thirteen exactly where they
 * were. **Console L5b added the fifteenth** (`runs.stats` / `statsRuns`, the
 * Spend screen's by-person view -- `runReport` could not carry it, because the
 * document is `runs stats --json` and not `report`) and left the other fourteen
 * exactly where they were. **The owner's design ruling of 2026-10-25 added the
 * last six** -- the installation rail's own operations, `installation.list` and
 * the five that follow it -- and left the other fifteen exactly where they were:
 * the rail's read is the picker's `config list --json` moved onto this surface,
 * and the five writers are the picker's own operations reached from the
 * dashboard's document instead of the picker window's. Read that as a diff
 * against the previous version of this list: six rows, in one place, and no
 * method renamed. **Plan GV2.1 added the twenty-second** (`plan.status` /
 * `planStatus`, the plan graph joined to the runs working on it -- the read
 * L5b's reasoning covers, one command further) and left the other twenty-one
 * exactly where they were. **Bitbucket credentials T1.2 added the twenty-third**
 * (`coordinator.status` / `coordinatorStatus`, the project view's read of the
 * coordinator's stored view -- the first operation here whose document the CLI
 * serves from a store rather than from a live walk) and left the other
 * twenty-two exactly where they were. **The console v2 plan's V2.5.1a added the
 * twenty-fourth** (`run.launch` / `launchRun`, the console's one write and the
 * only operation here that is not a read, rule 11) and left the other twenty-
 * three exactly where they were; its part 2b-3b-1 then built the confirmation
 * window into the entry point, so that row's spawn happens on a yes from the
 * window's own top frame and on nothing else. **The coordinator-v2 plan's K2a
 * added the twenty-fifth** (`link.openPullRequest` / `openPullRequest`: the pull
 * request link, built in the main process from three fields and never read out
 * of a document's `url`, D814/D815) and left the other twenty-four exactly where
 * they were. **The coordinator-v2 plan's C3 added the twenty-sixth**
 * (`coordinator.show` / `coordinatorShow`: one pull request as the host has it,
 * the description included, which the stored view drops by design -- D816) and
 * left the other twenty-five exactly where they were. It is the second
 * operation here that mints a credential of the host's, and the only one whose
 * credential is read-only by construction; it is next to `coordinator.status`
 * in this list because the two are the same subject asked two ways.
 */
const SHIPPED_SURFACE: readonly (readonly [string, string])[] = [
  ['live.watch', 'subscribeLive'],
  ['budget.show', 'showBudget'],
  ['spend.report', 'runReport'],
  ['runs.list', 'listRuns'],
  ['runs.stats', 'statsRuns'],
  ['plan.status', 'planStatus'],
  ['run.diagnose', 'diagnoseRun'],
  ['run.logs', 'readRunLogs'],
  ['run.logLink', 'runLogLink'],
  ['secrets.inventory', 'listSecrets'],
  ['accounts.inventory', 'listAccounts'],
  ['cli.version', 'cliVersion'],
  ['startup.compatibility', 'getCompatibility'],
  ['installation.current', 'getInstallation'],
  ['environment.report', 'environmentReport'],
  ['link.openExternal', 'openExternal'],
  ['link.openPullRequest', 'openPullRequest'],
  ['installation.list', 'listInstallations'],
  ['installation.choose', 'chooseInstallation'],
  ['installation.checkout', 'setInstallationCheckout'],
  ['installation.remove', 'removeInstallation'],
  ['installation.reveal', 'revealInstallation'],
  ['installation.open', 'openInstallations'],
  ['coordinator.status', 'coordinatorStatus'],
  ['coordinator.show', 'coordinatorShow'],
  ['run.launch', 'launchRun'],
]

/**
 * surfaceOffences returns, for a proposed surface, the reasons it is not the
 * one this console ships. It is a function over a list rather than a loop in
 * the test for the same reason `dynamicCodeOffences` above is: the test below
 * hands it the mutations directly, so "adding a channel fails a test" is a fact
 * this file demonstrates instead of a property a reader has to take on trust.
 */
export function surfaceOffences(proposed: readonly (readonly [string, string])[]): readonly string[] {
  const found: string[] = []
  const names = proposed.map(([name]) => name)
  if (new Set(names).size !== names.length) found.push('a name appears twice')
  if (proposed.length !== SHIPPED_SURFACE.length) {
    found.push(`${proposed.length} rows, and this console ships ${SHIPPED_SURFACE.length}`)
  }
  for (const [name, method] of SHIPPED_SURFACE) {
    const row = proposed.find(([candidate]) => candidate === name)
    if (row === undefined) found.push(`${name}: not on the surface at all`)
    else if (row[1] !== method) found.push(`${name}: on ${row[1]}, not on ${method}`)
  }
  for (const [name] of proposed) {
    if (!SHIPPED_SURFACE.some(([candidate]) => candidate === name)) {
      found.push(`${name}: a channel this console does not ship`)
    }
  }
  return found
}

describe('the frozen list of operations the preload exposes', () => {
  it('is exactly these twenty-six, with `spend.report` on `runReport`, the handshake on `getCompatibility` and the rail on `listInstallations`', () => {
    expect(EXPOSED_OPERATIONS).toEqual(SHIPPED_SURFACE)
    expect(surfaceOffences(EXPOSED_OPERATIONS)).toEqual([])
    expect(Object.isFrozen(EXPOSED_OPERATIONS)).toBe(true)
    // The table and the preload are the same length, which is the runtime half
    // of the two-way test at the top of this file: an operation added to
    // `CONSOLE_OPERATIONS` with no row here fails *that* test, and a row here
    // with no operation fails this one.
    expect(CONSOLE_OPERATION_NAMES).toHaveLength(EXPOSED_OPERATIONS.length)
  })

  it('fails, naming the row, when a channel is added, removed or changed', () => {
    // The mutation controls for the list above. A checker that refused
    // everything would pass every one of these and be worthless, so the
    // unmutated surface is asserted clean first.
    expect(surfaceOffences(SHIPPED_SURFACE)).toEqual([])

    // A twenty-seventh channel: what each task added must stay one row, and the
    // next task that adds a read cannot do it quietly.
    const twentySeventh = surfaceOffences([...SHIPPED_SURFACE, ['fleet.report', 'fleetReport']])
    expect(twentySeventh).toContain('fleet.report: a channel this console does not ship')
    expect(twentySeventh).toContain('27 rows, and this console ships 26')

    // A write verb, which the allow-list forbids outright: it is refused here
    // for the same reason, one step earlier than `assertReachableSurface`.
    expect(surfaceOffences([...SHIPPED_SURFACE, ['runs.cancel', 'cancelRun']])).toContain(
      'runs.cancel: a channel this console does not ship',
    )

    // This task's own row, deleted and then renamed: the test the task asks
    // for, named for the row rather than for the list.
    const withoutTheNewRow = surfaceOffences(
      SHIPPED_SURFACE.filter(([name]) => name !== 'startup.compatibility'),
    )
    expect(withoutTheNewRow).toContain('startup.compatibility: not on the surface at all')
    expect(withoutTheNewRow).toContain('25 rows, and this console ships 26')
    expect(
      surfaceOffences([
        ...SHIPPED_SURFACE.filter(([name]) => name !== 'startup.compatibility'),
        ['startup.compatibility', 'getCompatibilty'],
      ]),
    ).toContain('startup.compatibility: on getCompatibilty, not on getCompatibility')

    // And a name listed twice, which the real table refuses as well
    // (`an operation appears twice`).
    expect(surfaceOffences([...SHIPPED_SURFACE, ['cli.version', 'cliVersion']])).toContain(
      'a name appears twice',
    )
  })

  it('is frozen, and hands the window a bridge with the twenty-six methods and nothing else', () => {
    // The bridge the window is handed: the twenty-six methods and the names,
    // with no `invoke`, no channel and no method of its own.
    const bridge = createConsoleBridge(fakeRenderer())
    expect(Object.keys(bridge).sort()).toEqual(
      ['operations', 'version', ...SHIPPED_SURFACE.map(([, method]) => method)].sort(),
    )
    expect(Object.isFrozen(bridge)).toBe(true)
  })
})

describe('the one URL-shaped argument', () => {
  const cases: readonly (readonly [string, boolean, string])[] = [
    ['https://github.com/acme/app/pull/1', true, 'a GitHub pull request'],
    ['https://bitbucket.org/acme/app/pull-requests/2', true, 'a Bitbucket pull request'],
    ['https://console.cloud.google.com/logs/query', true, 'the Cloud console'],
    ['http://github.com/acme/app', false, 'plain http'],
    ['javascript:alert(1)', false, 'a javascript URL'],
    ['file:///etc/passwd', false, 'a file URL'],
    ['data:text/html,<script>alert(1)</script>', false, 'a data URL'],
    ['//github.com/acme/app', false, 'a protocol-relative URL'],
    ['github.com/acme/app', false, 'no scheme at all'],
    ['https://user:pass@github.com/acme/app', false, 'credentials in the URL'],
    ['https://github.com:8443/acme/app', false, 'a port'],
    ['https://github.com.evil.test/acme/app', false, 'a host that merely ends in the allowed one'],
    ['https://evil.test/acme/app', false, 'a host off the allow-list'],
    ['https://GITHUB.COM/acme/app', true, 'the parser lower-cases the host, so this is github.com'],
    ['https://github.com/‮repo', false, 'a bidi override in the path'],
    ['', false, 'nothing'],
  ]

  for (const [value, accepted, why] of cases) {
    it(`${accepted ? 'accepts' : 'refuses'} ${JSON.stringify(value)} (${why})`, () => {
      const decision = validateArguments('link.openExternal', { url: value })
      expect(decision.ok).toBe(accepted)
    })
  }

  it('refuses a URL past the length bound before it parses anything', () => {
    const long = `https://github.com/${'a'.repeat(3000)}`
    const decision = validateArguments('link.openExternal', { url: long })
    expect(decision.ok).toBe(false)
    expect(decision.ok === false && decision.reason).toMatch(/longer than/)
  })

  it('hands the handler the parsed URL that was validated, not the string that was sent', () => {
    // The input has to be one whose *parsed* form differs from what was sent, or
    // this test cannot tell the two apart. The first spelling used here,
    // `https://github.com/acme/app?#f`, is returned byte-identical by the WHATWG
    // parser, so asserting it proved nothing about which one reaches the
    // handler. An upper-case host and a `..` segment do not survive the parser
    // unchanged, and `validateExternalUrl` returns `url.href`
    // (`src/main/window.ts`), which is what the handler opens.
    const sent = 'https://GitHub.COM/acme/app/../other'
    const decision = validateArguments('link.openExternal', { url: sent })
    expect(decision.ok).toBe(true)
    if (!decision.ok) return
    expect(decision.args['url']).toBe('https://github.com/acme/other')
    expect(decision.args['url']).not.toBe(sent)
    expect(typeof decision.args['url']).toBe('string')
  })

  it('is the only operation that takes a URL', () => {
    for (const operation of CONSOLE_OPERATIONS) {
      for (const spec of operation.args) {
        if (spec.kind === 'link') expect(operation.name).toBe('link.openExternal')
      }
    }
  })
})

// ---------------------------------------------------------------------------
// The pull request link the main process builds (Coordinator v2 K2a, D814/D815)
// ---------------------------------------------------------------------------
//
// `link.openPullRequest` is the second operation that reaches
// `shell.openExternal`, and the difference between it and the first is the whole
// of its safety: it is handed **three fields and no URL**, and the main process
// spells the link from them. So there are two things to hold here -- that the
// schema cannot be made to carry a URL (a `url` key, or a `repo` that is one),
// and that a triple the builder refuses opens nothing at all.
describe('the pull request link the main process builds (K2a, D814/D815)', () => {
  it('takes exactly the three fields, and refuses a fourth key', () => {
    const decision = validateArguments('link.openPullRequest', {
      ...WELL_FORMED['link.openPullRequest'],
      url: 'https://github.com/acme/app/pull/1',
    })
    expect(decision.ok).toBe(false)
    expect(decision.ok === false && decision.reason).toContain('url')
  })

  it('refuses a payload that carries a URL under any of its own keys', () => {
    // The three keys are the whole of what a renderer may say, and a URL-shaped
    // value is refused wherever it is put: the provider is a closed set of two
    // words, the repository is `owner/name`, and the number is a number. There
    // is no key here a document's `url` could travel in (D815).
    const url = 'https://github.com/acme/app/pull/1'
    for (const key of ['provider', 'repo', 'number']) {
      const decision = validateArguments('link.openPullRequest', {
        ...WELL_FORMED['link.openPullRequest'],
        [key]: url,
      })
      expect(decision.ok, `${key} accepted a URL`).toBe(false)
    }
  })

  it('refuses a repository that walks out of its own path, a URL, and a bare word', () => {
    // The shapes the `repository` kind refuses. `owner/repo-a/pull/1` is not one
    // of them -- three segments is a repository as far as that grammar is
    // concerned -- and it is the case the builder catches, one test below.
    for (const repo of ['owner/../evil', 'https://github.com/acme/app', 'acme', '', 'owner/repo-a/']) {
      const decision = validateArguments('link.openPullRequest', { provider: 'github', repo, number: 1 })
      expect(decision.ok, `repo accepted ${repo}`).toBe(false)
    }
  })

  it('refuses a provider outside the two, and a number that is not a pull request number', () => {
    for (const provider of ['gitlab', 'GitHub', '', 'github.com', 'constructor', '__proto__']) {
      expect(
        validateArguments('link.openPullRequest', { provider, repo: 'acme/app', number: 1 }).ok,
        `provider accepted ${provider}`,
      ).toBe(false)
    }
    for (const number of [0, -1, 1.5, '1', true, null, 2 ** 53]) {
      expect(
        validateArguments('link.openPullRequest', { provider: 'github', repo: 'acme/app', number }).ok,
        `number accepted ${String(number)}`,
      ).toBe(false)
    }
    for (const provider of ['github', 'bitbucket']) {
      expect(validateArguments('link.openPullRequest', { provider, repo: 'acme/app', number: 1 }).ok).toBe(true)
    }
  })

  it('opens nothing at all for a triple the builder refuses, and the opener is never called', async () => {
    // **The headline test of K2a.** The spy is the only way to the browser, and
    // the hostile values below are sent through the real registrar, the real
    // validator and the real bridge -- so what is asserted is the operation, not
    // the handler in isolation. `owner/repo-a/pull/1` is the interesting one: it
    // passes the schema and is refused by `pullRequestUrl`, which is the gate
    // that exists precisely because a `repo` field could be one.
    const opened: string[] = []
    const { bridge } = wiredConsole({
      'link.openPullRequest': openPullRequestHandler(async (url) => {
        opened.push(url)
      }),
    })
    // The first gate refuses these: they are not the three fields at all.
    const refusedByTheSchema = [
      { provider: 'gitlab', repo: 'owner/repo-a', number: 42 },
      { provider: 'github', repo: 'owner/../evil', number: 42 },
      { provider: 'github', repo: 'owner/repo-a', number: 0 },
      { provider: 'github', repo: 'owner/repo-a', number: 1.5 },
    ]
    for (const args of refusedByTheSchema) {
      const reply = await asCallable(bridge.openPullRequest)(args)
      expect(reply.ok, JSON.stringify(args)).toBe(false)
    }
    // And these pass the schema and are refused by the builder -- the second
    // gate, and the reason it exists: a `repo` field can be a path or a host.
    const refusedByTheBuilder = [
      { provider: 'github', repo: 'owner/repo-a/pull/1', number: 42 },
      { provider: 'github', repo: 'github.com/owner/repo-a', number: 42 },
    ]
    for (const args of refusedByTheBuilder) {
      const reply = await asCallable(bridge.openPullRequest)(args)
      expect(reply, JSON.stringify(args)).toEqual({ ok: true, data: null })
    }
    // Neither gate opened anything.
    expect(opened).toEqual([])
  })

  it('opens the built URL exactly once for a good triple, and never the document’s own url', async () => {
    const opened: string[] = []
    const { bridge } = wiredConsole({
      'link.openPullRequest': openPullRequestHandler(async (url) => {
        opened.push(url)
      }),
    })
    // The payload a document's three fields would produce.
    const reply = await asCallable(bridge.openPullRequest)({
      provider: 'bitbucket',
      repo: 'owner/repo-a',
      number: 1458,
    })
    expect(reply).toEqual({ ok: true, data: null })
    expect(opened).toEqual(['https://bitbucket.org/owner/repo-a/pull-requests/1458'])

    // The same three fields with the `url` the same document carries beside
    // them: it is not a key this schema has, so the whole payload is refused
    // rather than a field being opened.
    const withUrl = await asCallable(bridge.openPullRequest)({
      provider: 'bitbucket',
      repo: 'owner/repo-a',
      number: 1458,
      url: 'https://evil.test/owner/repo-a/pull-requests/1458',
    })
    expect(withUrl.ok).toBe(false)
    // Still exactly the one URL, built here: the refused payload opened nothing.
    expect(opened).toEqual(['https://bitbucket.org/owner/repo-a/pull-requests/1458'])
  })
})

describe('the schema as a whole', () => {
  it('never hands a handler a key its operation does not declare', () => {
    const sources = Object.entries(WELL_FORMED)
    for (const operation of CONSOLE_OPERATIONS) {
      for (const [source, payload] of sources) {
        const decision = validateArguments(operation.name, payload)
        if (!decision.ok) continue
        for (const key of Object.keys(decision.args)) {
          expect(
            operation.args.some((spec) => spec.key === key),
            `${operation.name} accepted ${key} from ${source}`,
          ).toBe(true)
        }
      }
    }
  })

  it('throws on an operation name that is not in the table', () => {
    // A name no screen would ever add, so this test cannot be satisfied by the
    // very mutation the rest of the file exists to catch: with `budget.kill` as
    // the unknown name -- which is what this said first -- adding `budget.kill`
    // to the table would have made *this* test pass and hidden the mutation
    // behind it.
    expect(() => validateArguments('nope.not.an.operation' as OperationName, {})).toThrow(
      /not a console operation/,
    )
  })
})

// ---------------------------------------------------------------------------
// The handshake's answer on the wire (desktop-console plan Task 3.3)
// ---------------------------------------------------------------------------
//
// `startup.compatibility` carries a value this console built itself, so the
// interesting questions are not about a hostile document: they are (a) that the
// check accepts exactly what the handshake produces, (b) that a *mutated* store
// -- a mode typo, a view id that is not a withheld view, a `hidden` list that no
// longer agrees with the mode -- answers the console's own refusal sentence
// instead of a value the renderer would gate on, and (c) that the check and the
// renderer's own copy of it (`src/renderer/app.ts`'s `compatibilityFrom`) reach
// the same verdict on the same value, because two implementations that drifted
// would be two policies: main answering a compatibility the renderer refuses.

/** theHandshakeRan is one value per band of `classifyCliVersion`, plus the
 * refusal it builds for a line it cannot read: what the console actually puts
 * in the store at startup. */
const theHandshakeRan: readonly CliCompatibility[] = [
  classifyCliVersion('0.8.1'),
  classifyCliVersion('0.7.4'),
  classifyCliVersion('0.9.0'),
  classifyCliVersion('0.5.9'),
  classifyCliVersion('dev-abc1234'),
  classifyCliVersion('fugaro version 0.8.1'),
  classifyCliVersion(''),
]

/** REDUCED_HANDSHAKE and SUPPORTED_HANDSHAKE are the two shapes the fields
 * below are mutated from: a `reduced` answer that withholds all three views,
 * and an `ok` one that withholds none. Between them they are every pair the
 * mode/hidden check has to tell apart. */
const REDUCED_HANDSHAKE = classifyCliVersion('0.9.0')
const SUPPORTED_HANDSHAKE = classifyCliVersion('0.8.1')

describe("the handshake's answer on the wire", () => {
  it('accepts every value the handshake itself produces, and answers an equal fresh copy', () => {
    for (const compatibility of theHandshakeRan) {
      // Equal, and **not** the same object: the answer is rebuilt from the
      // checked fields (round 2 of the review -- returning the input let an
      // unchecked key ride along), and it is equal field for field, so the
      // rebuild normalises nothing the renderer gates on.
      const inMain = readStoredCompatibility(compatibility)
      const inRenderer = compatibilityFrom({ ok: true, data: compatibility })
      expect(inMain, compatibility.skew).toEqual(compatibility)
      expect(inMain, compatibility.skew).not.toBe(compatibility)
      expect(Object.isFrozen(inMain), compatibility.skew).toBe(true)
      expect(inRenderer, compatibility.skew).toEqual(compatibility)
      expect(inRenderer, compatibility.skew).not.toBe(compatibility)
      expect(Object.isFrozen(inRenderer), compatibility.skew).toBe(true)
    }
    // The refusal the handshake builds for a read that failed carries its
    // `because`, and both checks keep it.
    const unreadable = unreadableCompatibility('the console could not find a fugaro to read')
    expect(readStoredCompatibility(unreadable)).toEqual(unreadable)
    expect(compatibilityFrom({ ok: true, data: unreadable })).toEqual(unreadable)
    // The table is not vacuous: it holds all three modes, so the checks above
    // are not "everything is `ok`".
    expect(new Set(theHandshakeRan.map((compatibility) => compatibility.mode))).toEqual(
      new Set(['ok', 'reduced', 'refused']),
    )
    expect(REDUCED_HANDSHAKE.mode).toBe('reduced')
    expect(REDUCED_HANDSHAKE.hidden).toEqual([...REDUCED_VIEWS])
    expect(SUPPORTED_HANDSHAKE.mode).toBe('ok')
    expect(SUPPORTED_HANDSHAKE.hidden).toEqual([])
  })

  it('refuses a stored value that is not a compatibility, without repeating it', () => {
    const refusals: readonly (readonly [string, unknown])[] = [
      ['null', null],
      ['undefined', undefined],
      ['a string', 'ok'],
      ['a number', 7],
      ['an array', [REDUCED_HANDSHAKE]],
      ['an empty object', {}],
      ['a mode this console does not have', { ...REDUCED_HANDSHAKE, mode: 'observer' }],
      ['a mode that is not a string', { ...REDUCED_HANDSHAKE, mode: 1 }],
      ['an empty skew', { ...REDUCED_HANDSHAKE, skew: '' }],
      ['a skew that is not a string', { ...REDUCED_HANDSHAKE, skew: 3 }],
      ['a console version that is not a string', { ...REDUCED_HANDSHAKE, consoleVersion: 8.5 }],
      ['a floor that is not a string', { ...REDUCED_HANDSHAKE, floor: null }],
      ['a cli version that is neither a string nor null', { ...REDUCED_HANDSHAKE, cliVersion: 0.7 }],
      ['a supported list that is not an array', { ...REDUCED_HANDSHAKE, supported: '0.7' }],
      ['a supported list with a number in it', { ...REDUCED_HANDSHAKE, supported: ['0.7', 8] }],
      ['a hidden list that is not an array', { ...REDUCED_HANDSHAKE, hidden: 'spend' }],
      [
        'a view id that is not a withheld view',
        { ...REDUCED_HANDSHAKE, hidden: ['spend', 'treasury'] },
      ],
      ['the same view twice', { ...REDUCED_HANDSHAKE, hidden: ['spend', 'spend', 'caps', 'kill'] }],
      ['an `ok` mode beside a list that withholds a view', { ...REDUCED_HANDSHAKE, mode: 'ok' }],
      [
        'a supported mode beside a list that withholds one',
        { ...SUPPORTED_HANDSHAKE, hidden: [...REDUCED_VIEWS] },
      ],
      [
        'a refused mode beside an empty list',
        { ...classifyCliVersion('0.5.9'), hidden: [] },
      ],
      ['a plain object with an inherited mode', Object.create(REDUCED_HANDSHAKE)],
      ['a date', new Date(0)],
      ['a because that is a number', { ...unreadableCompatibility('x'), because: 7 }],
      ['a because that is an object', { ...unreadableCompatibility('x'), because: { treasury: 1 } }],
      ['a because that is undefined', { ...unreadableCompatibility('x'), because: undefined }],
      ['an empty because', { ...unreadableCompatibility('x'), because: '' }],
      ['an overlong because', unreadableCompatibility(`treasury ${'x'.repeat(300)}`)],
      ['a because on a value that is not unreadable', { ...REDUCED_HANDSHAKE, because: 'treasury' }],
      ['an overlong cli version', { ...REDUCED_HANDSHAKE, cliVersion: `0.9.0${'0'.repeat(300)}` }],
      ['an overlong skew', { ...REDUCED_HANDSHAKE, skew: 'x'.repeat(300) }],
    ]
    for (const [why, value] of refusals) {
      // The renderer's copy refuses the same values, with its own sentence.
      expect(compatibilityFrom({ ok: true, data: value }).because, why).toBe(RENDERER_UNREADABLE)
      const refused = readStoredCompatibility(value)
      expect(refused.mode, why).toBe('refused')
      expect(refused.skew, why).toBe('unreadable')
      expect(refused.because, why).toBe(COMPATIBILITY_UNREADABLE)
      // The value's own words never reach the refusal: the sentence is the
      // console's constant, and it is what a screen draws.
      expect(JSON.stringify(refused), why).not.toContain('treasury')
      expect(refused.hidden, why).toEqual([...REDUCED_VIEWS])
    }
  })

  it('drops every key it did not check, so nothing unchecked rides along', () => {
    // The reviewer's case: a store that carried the raw line beside the
    // checked fields. The answer is rebuilt, so the key -- and its escape
    // sequence -- is not on the wire and not in the gate.
    const carrying = {
      ...REDUCED_HANDSHAKE,
      rawStdout: '\u001b[31m0.9.0\u001b[0m <h1>treasury</h1>',
      get loud(): string {
        return 'treasury'
      },
    }
    for (const answered of [
      readStoredCompatibility(carrying),
      compatibilityFrom({ ok: true, data: carrying }),
    ]) {
      expect(answered.mode).toBe('reduced')
      expect(Object.keys(answered).sort()).toEqual(
        ['cliVersion', 'consoleVersion', 'floor', 'hidden', 'mode', 'skew', 'supported'],
      )
      expect(JSON.stringify(answered)).not.toContain('\u001b')
      expect(JSON.stringify(answered)).not.toContain('treasury')
      // The arrays are copies too: mutating the input's afterwards changes
      // nothing the gate reads.
      expect(answered.hidden).not.toBe(REDUCED_HANDSHAKE.hidden)
      expect(answered.supported).not.toBe(REDUCED_HANDSHAKE.supported)
    }
  })

  it('holds the renderer’s own check to the same verdict, value for value', () => {
    const values: readonly unknown[] = [
      ...theHandshakeRan,
      null,
      'ok',
      [],
      {},
      { ...REDUCED_HANDSHAKE, mode: 'observer' },
      { ...REDUCED_HANDSHAKE, hidden: ['spend', 'treasury'] },
      { ...REDUCED_HANDSHAKE, mode: 'ok' },
    ]
    let refusals = 0
    for (const value of values) {
      const inMain = readStoredCompatibility(value)
      const inRenderer = compatibilityFrom({ ok: true, data: value })
      const why = JSON.stringify(value) ?? 'undefined'
      expect(inRenderer.mode, why).toBe(inMain.mode)
      expect(inRenderer.skew, why).toBe(inMain.skew)
      if (inMain.because === undefined) {
        // A value both sides read -- including the handshake's own `refused`
        // mode, which is a verdict about the CLI and not an unreadable value:
        // both answer the same fields.
        expect(inRenderer, why).toEqual(inMain)
        continue
      }
      // A value neither side could read: each refuses with its own sentence,
      // and neither carries the value. The renderer's copy is not the main
      // process's message, which is what keeps one mutation from being both
      // failures.
      refusals += 1
      expect(inRenderer.because, why).toBe(RENDERER_UNREADABLE)
      expect(inMain.because, why).toBe(COMPATIBILITY_UNREADABLE)
    }
    // Neither half is vacuous: the table reaches both verdicts, so a checker
    // that refused everything (or accepted everything) fails here.
    expect(refusals).toBeGreaterThan(0)
    expect(refusals).toBeLessThan(values.length)
  })

  it('answers through the handler, and refuses for a store that has not answered', async () => {
    const handler = startupCompatibility(() => SUPPORTED_HANDSHAKE)
    // The handler takes the operation's validated arguments, which for this
    // operation are none: what it is handed changes nothing.
    expect(await handler({})).toEqual(SUPPORTED_HANDSHAKE)
    expect(await handler({ mode: 'ok' })).toEqual(SUPPORTED_HANDSHAKE)

    // A store mutated under the handler answers the console's refusal, not the
    // mutation -- this is the case the check exists for.
    const mutated = startupCompatibility(() => ({ ...REDUCED_HANDSHAKE, hidden: ['spend', 'kill'] }))
    const refused = (await mutated({})) as CliCompatibility
    expect(refused.mode).toBe('refused')
    expect(refused.because).toBe(COMPATIBILITY_UNREADABLE)

    // A store that has not answered yet -- startup has not finished -- throws
    // synchronously, and `registerConsoleHandlers`' `dispatch` catches that
    // exactly as it catches a rejected promise.
    const before = startupCompatibility(() => {
      throw new Error('the handshake has not answered yet')
    })
    expect(() => before({})).toThrow(/has not answered yet/)

    // And the whole way out, which is the shape a renderer sees: the reply is a
    // refusal rather than a rejection, and the answer itself arrives unchanged.
    const { bridge } = wiredConsole({ 'startup.compatibility': handler })
    expect(await bridge.getCompatibility()).toEqual({ ok: true, data: SUPPORTED_HANDSHAKE })
    const failing = wiredConsole({ 'startup.compatibility': before })
    const reply = await failing.bridge.getCompatibility()
    expect(reply.ok).toBe(false)
    expect(reply.ok ? '' : reply.reason).toContain('has not answered yet')
  })

  it('declares no argument, so a renderer has nothing to name', () => {
    const row = CONSOLE_OPERATIONS.find((operation) => operation.name === 'startup.compatibility')
    expect(row).toBeDefined()
    expect(row?.args).toEqual([])
    expect(row?.flags).toEqual([])
    expect(validateArguments('startup.compatibility', { mode: 'ok' }).ok).toBe(false)
    expect(validateArguments('startup.compatibility', undefined).ok).toBe(true)
  })
})

// ---------------------------------------------------------------------------
// The registrar: the surface is complete, and it validates before it calls
// ---------------------------------------------------------------------------

describe('the registrar', () => {
  it('registers one listener per operation, on the operation channel and no other', () => {
    const ipcMain = recordingIpcMain()
    registerConsoleHandlers(ipcMain, stubHandlers())
    expect([...ipcMain.channels].sort()).toEqual(
      CONSOLE_OPERATIONS.map((operation) => channelForOperation(operation.name)).sort(),
    )
  })

  it('validates before the handler: a malformed payload never reaches it', async () => {
    const seen: unknown[] = []
    const { ipcMain } = wiredConsole({
      'runs.list': (args) => {
        seen.push(args)
        return {}
      },
    })
    const listener = ipcMain.listeners.get(channelForOperation('runs.list'))
    expect(listener).toBeDefined()
    const reply = (await listener?.(consoleEvent(), { repo: '../../etc/passwd' })) as ConsoleReply
    expect(reply.ok).toBe(false)
    expect(reply.ok === false && reply.reason).toMatch(/repo/)
    expect(seen).toEqual([])
  })

  it('hands the handler nothing but the validated arguments', async () => {
    let received: unknown
    const { ipcMain } = wiredConsole({
      'runs.list': (args) => {
        received = args
        return { runs: [] }
      },
    })
    const listener = ipcMain.listeners.get(channelForOperation('runs.list'))
    const reply = await listener?.(consoleEvent(), { repo: 'acme/app', mine: true })
    expect(reply).toEqual({ ok: true, data: { runs: [] } })
    expect(received).not.toBeUndefined()
    expect(Object.getPrototypeOf(received)).toBeNull()
    expect(Object.isFrozen(received)).toBe(true)
    expect(Object.keys(received as object).sort()).toEqual(['mine', 'repo'])
  })

  it('answers a refusal, not a rejection, when a handler throws', async () => {
    const { ipcMain } = wiredConsole({
      'cli.version': () => {
        throw new Error('the CLI is not installed')
      },
    })
    const listener = ipcMain.listeners.get(channelForOperation('cli.version'))
    const reply = await listener?.(consoleEvent(), {})
    expect(reply).toEqual({ ok: false, reason: 'the CLI is not installed' })
  })

  it('answers a refusal, not an empty answer, when a handler throws something odd', async () => {
    const { ipcMain } = wiredConsole({
      'cli.version': () => {
        throw { toString: () => 'not an Error' }
      },
    })
    const listener = ipcMain.listeners.get(channelForOperation('cli.version'))
    const reply = (await listener?.(consoleEvent(), {})) as ConsoleReply
    expect(reply.ok).toBe(false)
    expect(reply.ok === false && typeof reply.reason).toBe('string')
  })

  it('bounds a handler’s own message, whatever length it is', async () => {
    // Design section 6.3 wants the CLI's own sentence; a CLI sentence is short,
    // and a handler that threw with a megabyte in it must not be able to put a
    // megabyte in the renderer through a reply the renderer is entitled to
    // trust. This is the `answerReason` half of the bound, on the widest input
    // a handler can produce.
    const { ipcMain } = wiredConsole({
      'cli.version': () => {
        throw new Error('x'.repeat(4 * 1024 * 1024))
      },
    })
    const listener = ipcMain.listeners.get(channelForOperation('cli.version'))
    const reply = (await listener?.(consoleEvent(), {})) as ConsoleReply
    expect(reply.ok).toBe(false)
    if (reply.ok) return
    expect(reply.reason.length).toBeLessThanOrEqual(MAX_REPLY_REASON_LENGTH)
    expect(reply.reason.length).toBeGreaterThan(0)
    expect(reply.reason.endsWith('…')).toBe(true)
  })

  it('takes an absolute path out of a handler’s message and leaves the rest', async () => {
    const { ipcMain } = wiredConsole({
      'cli.version': () => {
        throw new Error('open /home/user/.fugaro/secrets.yaml: permission denied')
      },
    })
    const listener = ipcMain.listeners.get(channelForOperation('cli.version'))
    const reply = (await listener?.(consoleEvent(), {})) as ConsoleReply
    expect(reply.ok).toBe(false)
    if (reply.ok) return
    expect(reply.reason).not.toContain('/home/user')
    expect(reply.reason).not.toContain('secrets.yaml')
    expect(reply.reason).toContain('open')
    expect(reply.reason).toContain('permission denied')
    expect(reply.reason).toContain('<path>')
  })

  // A path is not only ever preceded by a space or a quote. Go's own errors put
  // one straight after a colon (`open:/...` in a wrapped error), Markdown puts
  // one in backticks, a list puts one in brackets, and a URL can carry one as
  // `file://`. Each of these spellings reached the renderer whole before.
  it.each([
    ['after a colon', 'open:/Users/dimi/.config/fugaro/projects/belong.yaml: permission denied'],
    ['in brackets', 'refused [/Users/dimi/.config/fugaro/a]'],
    ['as a file URL', 'cannot load file:///Users/dimi/x'],
    ['after a semicolon', 'x;/etc/passwd'],
    ['as a Windows drive after a colon', 'dir:C:\\Users\\dimi'],
    ['in backticks', 'see `/Users/dimi/x` for details'],
    ['in angle brackets', 'wrote </Users/dimi/x>'],
  ])('takes a path %s out of a handler’s message', async (_label, message) => {
    const { ipcMain } = wiredConsole({
      'cli.version': () => {
        throw new Error(message)
      },
    })
    const listener = ipcMain.listeners.get(channelForOperation('cli.version'))
    const reply = (await listener?.(consoleEvent(), {})) as ConsoleReply
    expect(reply.ok).toBe(false)
    if (reply.ok) return
    expect(reply.reason, message).not.toMatch(/dimi|passwd|Users/)
    expect(reply.reason, message).toContain('<path>')
  })

  it('leaves a web URL in a message whole, rather than redacting from its //', async () => {
    const sentence = 'see https://github.com/acme/app/pull/12 for the run'
    const { ipcMain } = wiredConsole({
      'cli.version': () => {
        throw new Error(sentence)
      },
    })
    const listener = ipcMain.listeners.get(channelForOperation('cli.version'))
    expect(await listener?.(consoleEvent(), {})).toEqual({ ok: false, reason: sentence })
  })

  it('cuts a long message on a character boundary, never inside a surrogate pair', async () => {
    // `slice` cuts UTF-16 code units: a cut that lands between the two halves
    // of an emoji puts an unpaired surrogate on the wire, which is not text.
    for (const pad of [MAX_REPLY_REASON_LENGTH - 3, MAX_REPLY_REASON_LENGTH - 2, MAX_REPLY_REASON_LENGTH - 1]) {
      const { ipcMain } = wiredConsole({
        'cli.version': () => {
          throw new Error(`${'a'.repeat(pad)}\u{1F600}\u{1F600}tail`)
        },
      })
      const listener = ipcMain.listeners.get(channelForOperation('cli.version'))
      const reply = (await listener?.(consoleEvent(), {})) as ConsoleReply
      expect(reply.ok).toBe(false)
      if (reply.ok) return
      expect(reply.reason.length).toBeLessThanOrEqual(MAX_REPLY_REASON_LENGTH)
      expect(reply.reason, String(pad)).not.toMatch(/[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?<![\uD800-\uDBFF])[\uDC00-\uDFFF]/)
      expect(reply.reason.endsWith('…')).toBe(true)
    }
  })

  it('drops bidi overrides and zero-width characters from a refusal', async () => {
    // The validator refuses these in an *argument*; a handler's message is the
    // other half of the wire, and a right-to-left override in it makes a screen
    // show a sentence other than the one that was sent.
    const { ipcMain } = wiredConsole({
      'cli.version': () => {
        throw new Error('refused \u202Egnp.exe\u202C and \u200Bhidden\u2066x\u2069\uFEFF\u00AD done')
      },
    })
    const listener = ipcMain.listeners.get(channelForOperation('cli.version'))
    const reply = (await listener?.(consoleEvent(), {})) as ConsoleReply
    expect(reply.ok).toBe(false)
    if (reply.ok) return
    expect(reply.reason).not.toMatch(/[\u00AD\u061C\u180E\u200B-\u200F\u202A-\u202E\u2060-\u2064\u2066-\u2069\uFEFF]/)
    expect(reply.reason).toBe('refused gnp.exe and hiddenx done')
  })

  it('answers an unpaired surrogate in a message with text, not with half a character', async () => {
    const { ipcMain } = wiredConsole({
      'cli.version': () => {
        throw new Error('bad \uD800 and \uDC00 end')
      },
    })
    const listener = ipcMain.listeners.get(channelForOperation('cli.version'))
    const reply = (await listener?.(consoleEvent(), {})) as ConsoleReply
    expect(reply.ok).toBe(false)
    if (reply.ok) return
    expect(reply.reason).not.toMatch(/[\uD800-\uDFFF]/)
  })

  it('keeps a refusal to one line and drops control characters', async () => {
    const { ipcMain } = wiredConsole({
      'cli.version': () => {
        throw new Error('the CLI said\n\u001b[31mno\u001b[0m\r\nand again')
      },
    })
    const listener = ipcMain.listeners.get(channelForOperation('cli.version'))
    const reply = (await listener?.(consoleEvent(), {})) as ConsoleReply
    expect(reply.ok).toBe(false)
    if (reply.ok) return
    expect(reply.reason).not.toMatch(/[\u0000-\u001f\u007f-\u009f]/)
    expect(reply.reason).toBe('the CLI said [31mno [0m and again')
  })

  it('leaves an ordinary refusal sentence exactly as the handler wrote it', async () => {
    // The control for the three tests above: the bound must not touch a message
    // of the shape the CLI actually produces, or design section 6.3's "the CLI's
    // sentence, verbatim" would be quietly lost.
    const sentence = '--since "x": use a date (YYYY-MM-DD), a number of days (30d) or weeks (12w), at most 3660 days'
    const { ipcMain } = wiredConsole({
      'cli.version': () => {
        throw new Error(sentence)
      },
    })
    const listener = ipcMain.listeners.get(channelForOperation('cli.version'))
    expect(await listener?.(consoleEvent(), {})).toEqual({ ok: false, reason: sentence })
  })

  it('checks the surface before it registers anything', () => {
    // The registration body, as written: `assertReachableSurface` on the *real*
    // table, before the first `ipcMain.handle` call. `assertReachableSurface` is
    // tested above with a mutated table, and this is what says registration
    // consults it -- removing the call, or moving it after the handlers are on
    // the channels, fails here.
    const source = codeOnly('main/ipc.ts')
    const start = source.indexOf('export function registerConsoleHandlers')
    expect(start).toBeGreaterThan(-1)
    const body = source.slice(start, source.indexOf('\n}', start))
    const guard = body.indexOf('assertReachableSurface(CONSOLE_OPERATIONS)')
    const firstHandle = body.indexOf('ipcMain.handle(')
    expect(guard).toBeGreaterThan(-1)
    expect(firstHandle).toBeGreaterThan(guard)
  })

  it('refuses to build a surface with a handler missing', () => {
    const handlers: Record<string, ConsoleHandler> = { ...stubHandlers() }
    delete handlers['budget.show']
    expect(() => registerConsoleHandlers(recordingIpcMain(), handlers as ConsoleHandlers)).toThrow(
      /budget.show has no handler/,
    )
  })

  it('registers every channel in the table and ignores a handler that is not an operation', () => {
    // The control for the missing-handler test above: a table that is fine
    // registers, and a stray key in the handler object changes nothing -- the
    // surface is the table, not the object.
    const ipcMain = recordingIpcMain()
    registerConsoleHandlers(ipcMain, stubHandlers({ 'budget.kill': () => ({}) }))
    expect(ipcMain.channels).toHaveLength(CONSOLE_OPERATIONS.length)
  })

  it('carries a document through, unchanged, from handler to renderer', async () => {
    const document = { budget: { caps: { daily_usd: 20 }, kill: { project: false } } }
    const { bridge } = wiredConsole({ 'budget.show': () => document })
    const reply = await asCallable(bridge.showBudget)({})
    expect(reply).toEqual({ ok: true, data: document })
    expect(reply.ok && reply.data).toBe(document)
  })
})

// ---------------------------------------------------------------------------
// The sender gate: a message from anywhere but the console's own top frame
// ---------------------------------------------------------------------------

/**
 * FORGED_EVENTS is one event per way a message can arrive from somewhere other
 * than the console's own top frame, each of them an event Electron really
 * delivers: a subframe, a document on another origin (another window's
 * webContents is that case -- what makes it not the console is its frame, not
 * its id), a host that merely looks like the console's, and a frame or a
 * webContents whose top frame has gone away. Each row changes exactly one field
 * of `consoleEvent()`, which is what makes each of these tests a test of one
 * check. Why the webContents *id* is not among the fields checked is in
 * `isConsoleSender`'s own comment.
 *
 * The last three are about the event's *shape* rather than its contents, which
 * is the half a type cannot enforce at runtime: what arrives on a channel is
 * whatever the sender's process sent, so `isConsoleSender` checks the fields it
 * reads instead of trusting the annotation.
 */
const FORGED_EVENTS: readonly (readonly [string, unknown])[] = [
  [
    'a subframe of the console’s own document',
    { senderFrame: { url: APP_ENTRY_URL }, sender: { mainFrame: { url: APP_ENTRY_URL } } },
  ],
  [
    'a document on another origin',
    (() => {
      const frame = { url: 'https://evil.test/index.html' }
      return { senderFrame: frame, sender: { mainFrame: frame } }
    })(),
  ],
  [
    'a host that merely starts with the console’s own',
    (() => {
      const frame = { url: 'app://console.evil.test/index.html' }
      return { senderFrame: frame, sender: { mainFrame: frame } }
    })(),
  ],
  [
    'a port on the console’s own host',
    (() => {
      const frame = { url: 'app://console:1234/index.html' }
      return { senderFrame: frame, sender: { mainFrame: frame } }
    })(),
  ],
  [
    'credentials in front of the console’s own host',
    (() => {
      const frame = { url: 'app://user@console/index.html' }
      return { senderFrame: frame, sender: { mainFrame: frame } }
    })(),
  ],
  [
    'a file:// document',
    (() => {
      const frame = { url: 'file:///work/repo/console/src/renderer/index.html' }
      return { senderFrame: frame, sender: { mainFrame: frame } }
    })(),
  ],
  [
    'a webContents whose main frame is gone',
    { senderFrame: { url: APP_ENTRY_URL }, sender: { mainFrame: null } },
  ],
  ['a frame that has gone away', { senderFrame: null, sender: { mainFrame: null } }],
  ['no sending frame at all', { sender: { mainFrame: { url: APP_ENTRY_URL } } }],
  ['no sender at all', { senderFrame: { url: APP_ENTRY_URL } }],
  // A destroyed frame does not answer `null`: Electron's getters throw ("Render
  // frame was disposed before WebFrameMain could be accessed"). A gate that let
  // that throw would reject the invoke instead of refusing it.
  [
    'a frame whose senderFrame getter throws',
    {
      get senderFrame(): never {
        throw new Error('Render frame was disposed before WebFrameMain could be accessed')
      },
      sender: { mainFrame: { url: APP_ENTRY_URL } },
    },
  ],
  [
    'a webContents whose sender getter throws',
    {
      senderFrame: { url: APP_ENTRY_URL },
      get sender(): never {
        throw new Error('Object has been destroyed')
      },
    },
  ],
  [
    'a webContents whose mainFrame getter throws',
    (() => {
      const frame = { url: APP_ENTRY_URL }
      return {
        senderFrame: frame,
        sender: {
          get mainFrame(): never {
            throw new Error('Render frame was disposed before WebFrameMain could be accessed')
          },
        },
      }
    })(),
  ],
  [
    'a frame whose url getter throws',
    (() => {
      const frame = {
        get url(): never {
          throw new Error('Render frame was disposed before WebFrameMain could be accessed')
        },
      }
      return { senderFrame: frame, sender: { mainFrame: frame } }
    })(),
  ],
  ['a null event', null],
  ['an event that is not an object', 'fugaro:console/cli.version'],
]

describe('the sender gate', () => {
  it('accepts the console’s own top frame and answers it', async () => {
    const { ipcMain } = wiredConsole({ 'cli.version': () => ({ version: '1.2.3' }) })
    const listener = ipcMain.listeners.get(channelForOperation('cli.version'))
    expect(await listener?.(consoleEvent(), {})).toEqual({ ok: true, data: { version: '1.2.3' } })
  })

  it('accepts the console’s own document behind a path, a query or a fragment', async () => {
    // The frame URL is the document's URL and not the origin alone, so the check
    // must be about the origin: a fragment is how a screen addresses its own
    // views, and refusing it would be refusing the console's own window.
    for (const url of ['app://console/index.html#spend', 'app://console/index.html?x=1']) {
      const frame = { url }
      expect(isConsoleSender({ senderFrame: frame, sender: { mainFrame: frame } }), url).toBe(true)
    }
  })

  for (const [label, event] of FORGED_EVENTS) {
    it(`refuses a message from ${label}`, async () => {
      let reached = 0
      const { ipcMain } = wiredConsole({
        'runs.list': () => {
          reached += 1
          return {}
        },
      })
      const listener = ipcMain.listeners.get(channelForOperation('runs.list'))
      expect(listener).toBeDefined()
      const reply = (await listener?.(event as ConsoleEventLike, { repo: 'acme/app' })) as ConsoleReply
      expect(reply).toEqual({ ok: false, reason: SENDER_REFUSAL })
      // The point of the gate: nothing downstream ran. A refused message is not
      // validated, not dispatched and not answered with anything it wrote.
      expect(reached, label).toBe(0)
    })
  }

  it('never repeats what the sender wrote into the refusal', () => {
    const forged = { senderFrame: { url: 'https://evil.test/secret' }, sender: { mainFrame: null } }
    expect(isConsoleSender(forged)).toBe(false)
    expect(SENDER_REFUSAL).not.toContain('evil.test')
    expect(SENDER_REFUSAL).not.toContain('secret')
    expect(SENDER_REFUSAL).not.toMatch(/https?:/)
  })

  it('reads the origin, and not a prefix of the string', () => {
    // The rows of FORGED_EVENTS above are the behavioural half of this; this is
    // the pure function's own table, including the spellings a string prefix
    // test would let through.
    const cases: readonly (readonly [unknown, boolean, string])[] = [
      [APP_ENTRY_URL, true, 'the document the window loads'],
      ['app://console/', true, 'the bare origin'],
      ['app://console/index.html#spend', true, 'a fragment'],
      ['APP://CONSOLE/index.html', true, 'Chromium lower-cases a standard scheme’s host'],
      ['app://console.evil.test/index.html', false, 'a host that merely starts with the console’s'],
      ['app://evil.test/console/index.html', false, 'the console’s name in the path of another host'],
      ['app://console:1234/index.html', false, 'a port'],
      ['app://user@console/index.html', false, 'credentials'],
      ['https://console/', false, 'the right host on the wrong scheme'],
      ['app://', false, 'no host at all'],
      ['file:///work/repo/console/src/renderer/index.html', false, 'a file URL'],
      ['', false, 'nothing'],
      [null, false, 'not a string'],
      [42, false, 'a number'],
      [{ url: APP_ENTRY_URL }, false, 'an object that looks like a frame'],
    ]
    for (const [value, accepted, why] of cases) {
      expect(isConsoleSenderUrl(value), `${JSON.stringify(value)} (${why})`).toBe(accepted)
    }
  })

  it('refuses a frame on another document of the same origin', async () => {
    // Task 3.4b serves a second document from `app://console` -- the
    // installation picker -- and the origin check alone cannot tell it from the
    // dashboard. The gate is therefore the *path*, and this is the test that
    // makes the picker's document a sender this surface refuses.
    for (const url of ['app://console/installations.html', 'app://console/installations.html#top', 'app://console/other.html']) {
      const frame = { url }
      expect(isConsoleSender({ senderFrame: frame, sender: { mainFrame: frame } }), url).toBe(true)
      expect(isSenderOnDocument({ senderFrame: frame, sender: { mainFrame: frame } }, APP_ENTRY_URL), url).toBe(false)
    }
    // ...and the dashboard's own document, fragment included, is the one it
    // answers -- asserted here so the test above cannot pass on a gate that
    // refused everything.
    for (const url of [APP_ENTRY_URL, `${APP_ENTRY_URL}#spend`, `${APP_ENTRY_URL}?x=1`]) {
      const frame = { url }
      expect(isSenderOnDocument({ senderFrame: frame, sender: { mainFrame: frame } }, APP_ENTRY_URL), url).toBe(true)
    }
    // A frame whose getter throws refuses, exactly as it does at the origin.
    const throwing = {
      get url(): never {
        throw new Error('Render frame was disposed')
      },
    }
    expect(isSenderOnDocument({ senderFrame: throwing, sender: { mainFrame: throwing } }, APP_ENTRY_URL)).toBe(false)
    expect(isSenderOnDocument(null, APP_ENTRY_URL)).toBe(false)
  })
})

// ---------------------------------------------------------------------------
// The source of the two files: the prohibitions that are about what is written
// ---------------------------------------------------------------------------

describe('the source of the allow-list and the preload', () => {
  it('has exactly one invoke call site, in the preload', () => {
    const preload = codeOnly('preload/index.ts')
    expect(preload.match(/\.invoke\(/g) ?? []).toHaveLength(1)
    expect(preload).toContain('ipcRenderer.invoke(BRIDGE_PREFIX + name, args)')
    expect(preload).not.toContain('.send(')
    expect(preload).not.toContain('.sendSync(')
    expect(preload).not.toContain('.postMessage(')
  })

  it('never invokes from the main process: the main process handles', () => {
    const main = codeOnly('main/ipc.ts')
    expect(main).not.toContain('.invoke(')
    expect(main).not.toContain('ipcRenderer')
    expect(main).toContain('ipcMain.handle(')
  })

  it('confines Electron to the guarded block at the bottom of the preload', () => {
    const preload = codeOnly('preload/index.ts')
    expect(preload).not.toContain('ipcMain')
    expect(preload.match(/require\(/g) ?? []).toHaveLength(1)
    expect(preload).toContain("require('electron')")
    const main = codeOnly('main/ipc.ts')
    expect(main).not.toContain("from 'electron'")
    expect(main).not.toContain('contextBridge')
  })

  it('imports nothing but types in the preload, because a sandboxed preload can load nothing else', () => {
    const preload = codeOnly('preload/index.ts')
    const imports = preload.match(/^import .*$/gm) ?? []
    expect(imports.length).toBeGreaterThan(0)
    for (const statement of imports) {
      expect(statement, 'every import in the preload must be a type import').toMatch(/^import type /)
    }
    expect(preload).not.toMatch(/require\(\s*[^'"]/)
    expect(preload).toContain("../main/ipc'")
  })

  it('has no dynamic require, no eval and no child process anywhere in src/', () => {
    // Every `.ts` file under `console/src/`, walked rather than listed: the name
    // says "anywhere in src/" and this is what makes that true. The one file
    // that may reach `node:child_process` is `main/spawn.ts` (Task 1.3, the one
    // child process the console is allowed), and `spawn.test.ts` asserts that it
    // is the *only* one.
    const files = sourceFiles()
    // The walk found the tree, so the empty-list pass below is not a vacuous
    // one: two files that must be in it, in two different directories.
    const paths = files.map((file) => file.path)
    expect(paths).toContain('main/spawn.ts')
    expect(paths).toContain('preload/index.ts')
    expect(dynamicCodeOffences(files)).toEqual([])
  })

  it('catches the mutation: a planted file with an eval, a require or a child process', () => {
    // A checker that accepted this would pass the test above too. The planted
    // path is one no hand-written list had -- `source/screens/` does not exist
    // yet -- which is exactly the file the old five-name list could not see.
    const planted = {
      path: 'source/screens/live.ts',
      source: [
        "import { spawn } from 'node:child_process'",
        'const run = (s: string): unknown => eval(s)',
        'const load = (m: string): unknown => require(m)',
      ].join('\n'),
    }
    const found = dynamicCodeOffences([...sourceFiles(), planted])
    expect(found).toHaveLength(3)
    expect(found.join('\n')).toContain('source/screens/live.ts')
  })

  it('declares its channel once, in one place', () => {
    const main = codeOnly('main/ipc.ts')
    const preload = codeOnly('preload/index.ts')
    expect(main.match(/fugaro:console\//g) ?? []).toHaveLength(1)
    expect(preload.match(/fugaro:console\//g) ?? []).toHaveLength(1)
    expect(IPC_CHANNEL_PREFIX).toBe('fugaro:console/')
  })
})


// ---------------------------------------------------------------------------
// Reduced mode, at the handler (desktop-console plan Task 3.4a, step 3)
// ---------------------------------------------------------------------------

/**
 * THE_WITHHELD_OPERATIONS is the whole of what a reduced console refuses to
 * fetch, written out as a literal so that the mapping cannot be checked against
 * itself. Every entry is one operation of the surface, and the list is the
 * *answer* to "which channels does step 3 close": `spend.report` and
 * `runs.stats` are the Spend screen's two reads (the report, and the by-person
 * panel's per-person dollars, console L5b -- the same spend figures a reduced
 * console cannot vouch for, D7), `budget.show` is the Caps screen's, and the
 * kill switch has no operation of its own (design C10 -- its fields travel
 * inside `live.watch`, which a reduced console still draws).
 */
const THE_WITHHELD_OPERATIONS: readonly OperationName[] = ['budget.show', 'runs.stats', 'spend.report']

/** theRest is every operation that is *not* withheld: the non-vacuity guard for
 * the tests below, which otherwise would pass on a wrapper that refused the
 * whole surface. */
const THE_OPEN_OPERATIONS: readonly OperationName[] = CONSOLE_OPERATIONS.map(
  (operation) => operation.name,
).filter((name) => name !== 'startup.compatibility' && !THE_WITHHELD_OPERATIONS.includes(name))

/** methodFor is the bridge method an operation is reached by, read from the
 * preload's own table rather than spelled here: a screen calls `runReport`, and
 * the operation it drives is `spend.report`. */
function methodFor(name: OperationName): string {
  const row = EXPOSED_OPERATIONS.find(([candidate]) => candidate === name)
  if (row === undefined) throw new Error(`${name} is not on the surface`)
  return row[1]
}

/** reasonOf is a reply's refusal sentence, for a test that is asserting the
 * sentence rather than the shape: `ConsoleReply` is a union, so reading
 * `reason` is only legal once `ok` is known to be false. */
function reasonOf(reply: ConsoleReply): string {
  if (reply.ok) throw new Error(`expected a refusal, got data: ${JSON.stringify(reply.data)}`)
  return reply.reason
}

/** argsFor is a well-formed payload as the handler's own argument type: the
 * registrar validates before it calls a handler, so a test that calls one
 * directly is handing it what the validator would have. */
function argsFor(name: OperationName): Parameters<ConsoleHandler>[0] {
  return WELL_FORMED[name] as Parameters<ConsoleHandler>[0]
}

/** answeredBy is a source spy: every operation the surface can reach, recording
 * that it was called and answering an empty document. A wrapper that refuses
 * *after* calling the source has not refused anything -- the read already
 * happened -- so "was the source reached" is the assertion, not the reply. */
function answeredBy() {
  const reached: string[] = []
  const handlers = stubHandlers(
    Object.fromEntries(
      CONSOLE_OPERATIONS.map((operation) => [
        operation.name,
        (() => {
          reached.push(operation.name)
          return {}
        }) as ConsoleHandler,
      ]),
    ),
  )
  return { reached, handlers }
}

/** reducedConsole is the surface as `src/main/index.ts` registers it: the whole
 * allow-list, wrapped by the reduced-mode gate, behind the real registrar and the
 * real bridge. What a screen would get is what these tests read. */
function reducedConsole(compatibility: CliCompatibility) {
  const source = answeredBy()
  const ipcMain = recordingIpcMain()
  const handlers = reducedModeHandlers(source.handlers, () => compatibility)
  registerConsoleHandlers(ipcMain, handlers)
  const renderer: RendererLike = {
    async invoke(channel: string, ...args: unknown[]): Promise<unknown> {
      const listener = ipcMain.listeners.get(channel)
      if (listener === undefined) throw new Error(`nothing handles ${channel}`)
      return listener(consoleEvent(), args[0])
    },
  }
  return { bridge: createConsoleBridge(renderer), reached: source.reached }
}

describe('the views a reduced console withholds, at the handler (rule 4)', () => {
  it('withholds exactly the spend and caps reads, and names them', () => {
    // The mapping is closed over the three view ids of the design, and the
    // operations in it are the two reads a screen makes for a withheld view.
    expect(Object.keys(REDUCED_VIEW_OPERATIONS).sort()).toEqual(['caps', 'kill', 'spend'])
    const named = REDUCED_VIEWS.flatMap((view) => REDUCED_VIEW_OPERATIONS[view])
    expect([...named].sort()).toEqual([...THE_WITHHELD_OPERATIONS].sort())
    // `kill` is empty on purpose, and that is the assertion rather than a gap:
    // the kill-switch controls are fields of the live feed (C10), so there is no
    // operation to withhold and a later task that adds one has to say so here.
    expect(REDUCED_VIEW_OPERATIONS.kill).toEqual([])
    // And the live feed is in none of the three lists: a reduced console still
    // draws its live view, kill-switch fields and all.
    expect(named).not.toContain('live.watch')
    for (const view of REDUCED_VIEWS) {
      expect(REDUCED_VIEW_OPERATIONS[view], `${view} names an operation twice`).toHaveLength(
        new Set(REDUCED_VIEW_OPERATIONS[view]).size,
      )
    }
  })

  it('refuses a withheld read without reaching the source, and answers its own sentence', async () => {
    // The compatibility is the one `classifyCliVersion('0.9.0')` builds: mode
    // `reduced`, every view hidden. It is built rather than hand-written so the
    // test cannot pass on a shape the handshake never produces.
    const { bridge, reached } = reducedConsole(REDUCED_HANDSHAKE)
    for (const name of THE_WITHHELD_OPERATIONS) {
      const reply = await asCallable(
        (bridge as unknown as Record<string, unknown>)[methodFor(name)],
      )(WELL_FORMED[name])
      expect(reply, name).toEqual({ ok: false, reason: REDUCED_REFUSAL })
      // The sentence is the console's own and carries nothing of the CLI's: a
      // refusal that looked like a failed read would send the operator to fix a
      // project that is not broken.
      expect(reasonOf(reply)).not.toContain(NO_ACTIVE_INSTALLATION)
    }
    // **The point of the wrapper**: neither read reached the source. A gate
    // applied after `inner` would answer the same sentence and have read the
    // figures anyway, which is the failure this asserts against.
    expect(reached).toEqual([])
  })

  it('lets every other operation through, so a reduced console is still a console', async () => {
    const { bridge, reached } = reducedConsole(REDUCED_HANDSHAKE)
    // The non-vacuity half: twenty-two of the surface's twenty-six operations,
    // all of them reachable in a reduced mode (the handshake is the one the loop
    // skips and its own test covers; `installation.current`, Task 3.4b, is among
    // the nineteen, because a reduced console's header still names its
    // installation, and the rail's own six are too -- a reduced console still
    // has a rail, and the store it reads is not a figure the mode withholds).
    // `plan.status` is one of them: a plan graph is not a spend, a cap or a kill
    // control, so there is no figure for the mode to withhold. `coordinator.status`
    // (T1.2) is another, and for the same reason: the project view is repository
    // state, not money, so the mode has nothing to withhold from it.
    // A wrapper that refused the whole surface would fail here. **The launch is
    // among the twenty-one**: a reduced console withholds figures, and a launch
    // is not a figure -- whether it may be offered at all is rule 11's own
    // floor, which is a comparison against the handshake's version and not this
    // list. **`link.openPullRequest` is too, and for the same reason**: a pull
    // request number is repository state rather than money, so the mode has
    // nothing to withhold from it.
    expect(THE_OPEN_OPERATIONS.length).toBe(22)
    expect(THE_OPEN_OPERATIONS).toContain('installation.current')
    for (const name of THE_OPEN_OPERATIONS) {
      const reply = await asCallable(
        (bridge as unknown as Record<string, unknown>)[methodFor(name)],
      )(WELL_FORMED[name])
      expect(reply, name).toEqual({ ok: true, data: {} })
    }
    // Every open operation reached the source, and no withheld one did.
    expect([...reached].sort()).toEqual([...THE_OPEN_OPERATIONS].sort())
    expect(reached).toContain('live.watch')
    expect(reached).not.toContain('spend.report')
    expect(reached).not.toContain('runs.stats')
    expect(reached).not.toContain('budget.show')
  })

  it('withholds the by-person read with the spend view, because it reads the same spend figures (D7)', async () => {
    // `runs.stats` publishes per-person `counted_usd`/`notional_usd`: the spend
    // a reduced console cannot vouch for, under another name. Withholding
    // `spend.report` and answering this would put the withheld figures on the
    // screen through the by-person panel, so it is in the `spend` list by name.
    expect(REDUCED_VIEW_OPERATIONS.spend).toContain('runs.stats')
    expect(REDUCED_VIEW_OPERATIONS.spend).toContain('spend.report')
    const { bridge, reached } = reducedConsole(REDUCED_HANDSHAKE)
    const reply = await asCallable(
      (bridge as unknown as Record<string, unknown>)[methodFor('runs.stats')],
    )(WELL_FORMED['runs.stats'])
    expect(reply).toEqual({ ok: false, reason: REDUCED_REFUSAL })
    // Refused before the source: the per-person figures were never read.
    expect(reached).toEqual([])
    // And in a supported console it is an ordinary read.
    const source = answeredBy()
    const open = reducedModeHandlers(source.handlers, () => SUPPORTED_HANDSHAKE)
    expect(await open['runs.stats'](argsFor('runs.stats'))).toEqual({})
    expect(source.reached).toEqual(['runs.stats'])
  })

  it('answers the handshake in a reduced mode, because the gate is what the answer is for', async () => {
    // `startup.compatibility` is the one operation the wrapper does not wrap
    // (see `reducedModeHandlers`): the renderer asks it first and draws nothing
    // until it has the answer, so a gate over it would leave a reduced console
    // unable to learn *which* views it may not draw. It is asserted here rather
    // than left to the comment, because the exception is load-bearing.
    const source = answeredBy()
    const wrapped = reducedModeHandlers(source.handlers, () => REDUCED_HANDSHAKE)
    expect(wrapped['startup.compatibility']).toBe(source.handlers['startup.compatibility'])
    expect(wrapped['spend.report']).not.toBe(source.handlers['spend.report'])
    expect(Object.isFrozen(wrapped)).toBe(true)
  })

  it('refuses every read in a refused mode, and still answers the handshake', async () => {
    // Belt-and-braces, and kept deliberately: today a refused console registers
    // no handler at all (`src/main/index.ts`), so nothing here is reachable. It
    // is asserted because that is a property of the *wiring*, and a later task
    // that registered the surface earlier would otherwise open every channel in
    // a console that may draw nothing.
    const refused = classifyCliVersion('0.5.9')
    expect(refused.mode).toBe('refused')
    const source = answeredBy()
    const wrapped = reducedModeHandlers(source.handlers, () => refused)
    for (const operation of CONSOLE_OPERATIONS) {
      if (operation.name === 'startup.compatibility') continue
      await expect(wrapped[operation.name]({}), operation.name).rejects.toThrow(REDUCED_REFUSAL)
    }
    expect(source.reached).toEqual([])
  })

  it('reads the mode per call, so a handler cannot answer for a mode the console is not in', async () => {
    // The reason `current` is a function and not a value: a wrapper built over a
    // copy of the answer keeps answering whatever the copy said. The store is
    // mutable here on purpose -- that is the only way to see the difference --
    // and it is the same staleness `startupCompatibility`'s `read` avoids.
    let store: CliCompatibility = SUPPORTED_HANDSHAKE
    const source = answeredBy()
    const wrapped = reducedModeHandlers(source.handlers, () => store)
    expect(await wrapped['spend.report'](argsFor('spend.report'))).toEqual({})
    store = REDUCED_HANDSHAKE
    await expect(wrapped['spend.report'](argsFor('spend.report'))).rejects.toThrow(REDUCED_REFUSAL)
    // The source was reached once, by the first call, and not by the second.
    expect(source.reached).toEqual(['spend.report'])
  })

  it('refuses only the view that is hidden, not all three', async () => {
    // A hand-written compatibility with one view hidden: the gate reads
    // `hidesView`, so a mode that withholds one view withholds one view's read.
    const oneHidden: CliCompatibility = Object.freeze({
      ...REDUCED_HANDSHAKE,
      hidden: Object.freeze(['spend']) as unknown as CliCompatibility['hidden'],
    })
    expect(hidesView(oneHidden, 'spend')).toBe(true)
    expect(hidesView(oneHidden, 'caps')).toBe(false)
    const source = answeredBy()
    const wrapped = reducedModeHandlers(source.handlers, () => oneHidden)
    await expect(wrapped['spend.report'](argsFor('spend.report'))).rejects.toThrow(REDUCED_REFUSAL)
    expect(await wrapped['budget.show'](argsFor('budget.show'))).toEqual({})
    expect(source.reached).toEqual(['budget.show'])
  })

  it('answers every well-formed payload the same withheld sentence', async () => {
    // The withheld answer is one fixed sentence about the console's *mode* and
    // nothing about what was sent: no argument of the read appears in it, and
    // the same payload that reaches the source in an `ok` mode is refused here
    // with the same words. `spend.report`'s four keys are all optional, so `{}`
    // is a well-formed payload and gets the same answer as the full one.
    const { bridge, reached } = reducedConsole(REDUCED_HANDSHAKE)
    const spend = asCallable((bridge as unknown as Record<string, unknown>)[methodFor('spend.report')])
    expect(await spend(WELL_FORMED['spend.report'])).toEqual({ ok: false, reason: REDUCED_REFUSAL })
    expect(await spend({})).toEqual({ ok: false, reason: REDUCED_REFUSAL })
    expect(await spend(undefined)).toEqual({ ok: false, reason: REDUCED_REFUSAL })
    expect(reached).toEqual([])
  })

  it('refuses a malformed payload for being malformed, before the mode is read', async () => {
    // The order `dispatch` fixes: validate, then hand the handler checked
    // arguments. So a payload with a key the operation does not take is refused
    // with the schema's sentence and not the mode's -- which is the honest
    // answer (nothing was withheld; the payload was wrong), and it is what makes
    // the gate a gate over *reads* rather than a blanket refusal that would hide
    // a caller's mistake behind the console's mode.
    const { bridge, reached } = reducedConsole(REDUCED_HANDSHAKE)
    const spend = asCallable((bridge as unknown as Record<string, unknown>)[methodFor('spend.report')])
    const reply = await spend({ ...WELL_FORMED['spend.report'], treasury: 'acme' })
    expect(reasonOf(reply)).toBe('spend.report takes no treasury')
    expect(reasonOf(reply)).not.toBe(REDUCED_REFUSAL)
    expect(reached).toEqual([])
  })
})
