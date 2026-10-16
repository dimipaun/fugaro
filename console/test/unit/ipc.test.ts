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
//   - `--follow`, `--check-github-app`, `--strict`, `--csv`, `--watch`,
//     `--plain`, `--poll`, `--ascii` or `--no-color` fixed by an operation
//     fails the flag tests; `--url` together with `--json` fails the pairing
//     test;
//   - a preload method that is not on `CONSOLE_OPERATIONS`, or an operation
//     with no method, fails the two-way surface test; a method that lands on a
//     channel other than `channelForOperation(name)` fails the routing test;
//   - a schema that accepts an extra key, a prototype-pollution key, a
//     non-plain object, a path, a command, a flag name, a control character, an
//     invisible format character, an unpaired surrogate, an oversize string, a
//     wrong type or a URL that is not the validated external link fails the
//     table entry that names it; a schema that refuses *everything* fails the
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

import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import {
  ALLOWED_OPERATION_FLAGS,
  CONSOLE_OPERATIONS,
  CONSOLE_OPERATION_NAMES,
  IPC_CHANNEL_PREFIX,
  IPC_VERSION,
  MAX_ARGUMENT_KEYS,
  NEVER_REACHABLE,
  assertReachableSurface,
  channelForOperation,
  isNeverReachable,
  registerConsoleHandlers,
  validateArguments,
} from '../../src/main/ipc'
import type {
  ConsoleHandler,
  ConsoleHandlers,
  ConsoleOperation,
  ConsoleReply,
  OperationName,
} from '../../src/main/ipc'
import {
  BRIDGE_VERSION,
  CONSOLE_BRIDGE_KEY,
  EXPOSED_OPERATIONS,
  createConsoleBridge,
  exposeConsoleBridge,
  hasRendererContext,
} from '../../src/preload/index'
import type { RendererLike } from '../../src/preload/index'

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

/** recordingIpcMain is Electron's `ipcMain`, as far as this task uses it: a
 * registry of the channels that were handled, so a test can read back which
 * channels exist and deliver a message to one. */
function recordingIpcMain() {
  const listeners = new Map<string, (event: unknown, ...args: unknown[]) => unknown>()
  const channels: string[] = []
  return {
    channels,
    listeners,
    handle(channel: string, listener: (event: unknown, ...args: unknown[]) => unknown): void {
      channels.push(channel)
      listeners.set(channel, listener)
    },
  }
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
 * document, so a test of the *plumbing* does not have to write twelve. */
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
      return listener({}, args[0])
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

describe('the frozen surface the preload exposes', () => {
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
 *   - `unused` -- read-only, and no screen of the console consumes it. It is
 *     simply not on the allow-list, and nothing may drive it.
 */
type CliCommandClass = 'allowed' | 'mutating' | 'secret' | 'unused'

interface CliCommand {
  readonly command: string
  readonly kind: CliCommandClass
  readonly where: string
}

/**
 * CLI_COMMANDS is every leaf command `internal/cli` had on 2026-10-16
 * (`grep -rn 'Use:' internal/cli/*.go`, parents excluded), classified. It is the
 * other half of the allow-list's invariant: nothing off this list may be driven
 * (there is no command the console could drive that is not here), and nothing on
 * it that is not `allowed` may be driven either. Keeping the list complete is
 * what makes "the console is read-only" a fact about the surface rather than a
 * claim about the eleven commands we happened to think of.
 */
const CLI_COMMANDS: readonly CliCommand[] = [
  // The eleven the design's allow-list admits, and the one document each draws.
  { command: 'watch', kind: 'allowed', where: 'internal/cli/watch.go:134' },
  { command: 'budget show', kind: 'allowed', where: 'internal/cli/budget.go:214' },
  { command: 'report', kind: 'allowed', where: 'internal/cli/report.go:53' },
  { command: 'runs ls', kind: 'allowed', where: 'internal/cli/runs.go:87' },
  { command: 'diagnose', kind: 'allowed', where: 'internal/cli/diagnose.go:87' },
  { command: 'logs', kind: 'allowed', where: 'internal/cli/logs.go:45' },
  { command: 'secrets ls', kind: 'allowed', where: 'internal/cli/secrets.go:536' },
  { command: 'accounts ls', kind: 'allowed', where: 'internal/cli/accounts.go:70' },
  { command: 'version', kind: 'allowed', where: 'internal/cli/root.go:84' },
  { command: 'doctor', kind: 'allowed', where: 'internal/cli/doctor.go:159' },
  // Writers.
  { command: 'run', kind: 'mutating', where: 'internal/cli/run.go:134' },
  { command: 'cancel', kind: 'mutating', where: 'internal/cli/cancel.go:72' },
  { command: 'init', kind: 'mutating', where: 'internal/cli/init.go:103' },
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
 */
const DESIGN_ARGV: readonly string[] = [
  'watch --json',
  'budget show --json',
  'report --json',
  'runs ls --json',
  'diagnose --json',
  'logs --json',
  'logs --url',
  'secrets ls --json',
  'accounts ls --json',
  'version',
  'doctor --json',
]

/** argvOf is an operation's command and the flags it fixes, as one string. */
function argvOf(operation: ConsoleOperation): string | null {
  if (operation.command === null) return null
  return operation.flags.length === 0
    ? operation.command
    : `${operation.command} ${operation.flags.join(' ')}`
}

/** commandsDriven returns the operations whose command is this one. */
function operationsDriving(command: string): readonly ConsoleOperation[] {
  return CONSOLE_OPERATIONS.filter((operation) => operation.command === command)
}

describe("the allow-list's read-only invariant", () => {
  it('is exactly the design section 3.4 rule 2 allow-list, argv for argv', () => {
    const driven = CONSOLE_OPERATIONS.map(argvOf).filter((argv): argv is string => argv !== null)
    expect([...driven].sort()).toEqual([...DESIGN_ARGV].sort())
  })

  it('has exactly one operation spawn nothing at all, and it is the link', () => {
    const withoutCommand = CONSOLE_OPERATIONS.filter((operation) => operation.command === null)
    expect(withoutCommand.map((operation) => operation.name)).toEqual(['link.openExternal'])
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

  for (const command of NOT_ON_THIS_TREE) {
    it(`names ${command}, which this tree does not have yet, and does not pretend it does`, () => {
      expect(CLI_COMMANDS.some((entry) => entry.command === command)).toBe(false)
      expect(NEVER_REACHABLE.some((row) => row.value === command)).toBe(true)
    })
  }

  it('names only commands this tree has, or commands explicitly not on it', () => {
    for (const row of NEVER_REACHABLE) {
      if (row.shape === 'flag') continue
      const known = CLI_COMMANDS.some((entry) => entry.command === row.value)
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
    expect(isNeverReachable({ command: 'run', flags: ['--json'] })?.value).toBe('run')
    expect(isNeverReachable({ command: 'doctor', flags: ['--json', '--check-github-app'] })?.value).toBe(
      '--check-github-app',
    )
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

  it('fixes only the two flags the surface may fix, and never --url with --json', () => {
    for (const operation of CONSOLE_OPERATIONS) {
      for (const flag of operation.flags) {
        expect(ALLOWED_OPERATION_FLAGS, `${operation.name} fixes ${flag}`).toContain(flag)
      }
      const flags = flagsOf(operation)
      expect(flags.includes('--url') && flags.includes('--json'), operation.name).toBe(false)
    }
    expect([...ALLOWED_OPERATION_FLAGS]).toEqual(['--json', '--url'])
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
      const drivers = CONSOLE_OPERATIONS.filter((operation) =>
        row.shape === 'command'
          ? operation.command === row.value
          : flagsOf(operation).includes(row.value),
      )
      expect(drivers.map((operation) => operation.name), row.where).toEqual([])
      expect(isNeverReachable({ command: row.shape === 'command' ? row.value : null, flags: row.shape === 'flag' ? [row.value] : [] })).toBe(row)
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

  it('refuses a table with a command this tree does not have at all', () => {
    expect(() =>
      assertReachableSurface([
        ...CONSOLE_OPERATIONS,
        {
          name: 'run.launch',
          method: 'launchRun',
          command: 'run',
          flags: ['--json'],
          summary: 'a writer',
          args: [],
        },
      ]),
    ).toThrow(/run/)
  })

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
  'runs.list': { repo: 'acme/app', since: '2026-10-16', mine: true, pr: 42, all: false },
  'run.diagnose': { run: '20261016-121029-4a1b' },
  'run.logs': { run: '20261016-121029-4a1b' },
  'run.logLink': { run: '20261016-121029-4a1b' },
  'secrets.inventory': { scope: 'repo', repo: 'acme/app' },
  'accounts.inventory': { repo: 'acme/app' },
  'cli.version': {},
  'environment.report': {},
  'link.openExternal': { url: 'https://github.com/acme/app/pull/1' },
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

  it('takes no path, wherever a value goes, for every operation', () => {
    const paths = ['/etc/passwd', '../../etc/passwd', '/work/repo/fugaro.yaml', 'file:///etc/passwd']
    for (const operation of CONSOLE_OPERATIONS) {
      for (const spec of operation.args) {
        for (const path of paths) {
          const decision = validateArguments(operation.name, { [spec.key]: path })
          expect(decision.ok, `${operation.name}.${spec.key} accepted ${path}`).toBe(false)
        }
      }
    }
  })

  it('takes no command and no flag name, wherever a value goes', () => {
    const words = ['budget kill', 'run', '--repo', '-x', 'ls -la', 'fugaro run']
    for (const operation of CONSOLE_OPERATIONS) {
      for (const spec of operation.args) {
        for (const word of words) {
          const decision = validateArguments(operation.name, { [spec.key]: word })
          expect(decision.ok, `${operation.name}.${spec.key} accepted ${word}`).toBe(false)
        }
      }
    }
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

  const windows: readonly (readonly [string, boolean])[] = [
    ['2026-10-16', true],
    ['7d', true],
    ['24h', true],
    ['30m', true],
    ['0', true],
    ['7', false],
    ['7w', false],
    ['-7d', false],
    ['7d ', false],
    ['all', false],
  ]

  for (const [value, accepted] of windows) {
    it(`${accepted ? 'accepts' : 'refuses'} the window ${JSON.stringify(value)}`, () => {
      expect(validateArguments('runs.list', { since: value }).ok).toBe(accepted)
    })
  }
})

describe('the closed sets', () => {
  const dimensions = ['day', 'week', 'month', 'year', 'repo', 'model', 'person']
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
    const decision = validateArguments('link.openExternal', { url: 'https://github.com/acme/app?#f' })
    expect(decision.ok).toBe(true)
    if (!decision.ok) return
    expect(decision.args['url']).toBe('https://github.com/acme/app?#f')
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
    const reply = (await listener?.({}, { repo: '../../etc/passwd' })) as ConsoleReply
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
    const reply = await listener?.({}, { repo: 'acme/app', mine: true })
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
    const reply = await listener?.({}, {})
    expect(reply).toEqual({ ok: false, reason: 'the CLI is not installed' })
  })

  it('answers a refusal, not an empty answer, when a handler throws something odd', async () => {
    const { ipcMain } = wiredConsole({
      'cli.version': () => {
        throw { toString: () => 'not an Error' }
      },
    })
    const listener = ipcMain.listeners.get(channelForOperation('cli.version'))
    const reply = (await listener?.({}, {})) as ConsoleReply
    expect(reply.ok).toBe(false)
    expect(reply.ok === false && typeof reply.reason).toBe('string')
  })

  it('refuses to build a surface with a handler missing', () => {
    const handlers: Record<string, ConsoleHandler> = { ...stubHandlers() }
    delete handlers['budget.show']
    expect(() => registerConsoleHandlers(recordingIpcMain(), handlers as ConsoleHandlers)).toThrow(
      /budget.show has no handler/,
    )
  })

  it('registers the twelve channels and ignores a handler that is not an operation', () => {
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
    const files = [
      'main/ipc.ts',
      'preload/index.ts',
      'main/window.ts',
      'main/protocol.ts',
      'main/index.ts',
    ]
    for (const file of files) {
      const source = codeOnly(file)
      expect(source, file).not.toMatch(/require\(\s*[^'"]/)
      expect(source, file).not.toMatch(/\beval\(/)
      expect(source, file).not.toMatch(/new Function\(/)
      expect(source, file).not.toContain('child_process')
      expect(source, file).not.toContain('node:child_process')
    }
  })

  it('declares its channel once, in one place', () => {
    const main = codeOnly('main/ipc.ts')
    const preload = codeOnly('preload/index.ts')
    expect(main.match(/fugaro:console\//g) ?? []).toHaveLength(1)
    expect(preload.match(/fugaro:console\//g) ?? []).toHaveLength(1)
    expect(IPC_CHANNEL_PREFIX).toBe('fugaro:console/')
  })
})
