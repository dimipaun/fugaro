// The startup wiring (desktop-console plan Task 3.3; design section 7.1, D7).
//
// Task 3.2 built the handshake and left it uncalled: `src/main/index.ts` built
// the dashboard window with no version read at all, and a refusal window would
// have loaded the dashboard document. This file is the record of the wiring
// that fixes that, and every `it` in it is one mutation away from failing:
//
//   - putting the version read after the window is built fails "runs the
//     handshake before any window exists" -- the reader records how many
//     windows were already constructed when it was called;
//   - building the dashboard document for a refused console fails "a refused
//     version loads the refusal document and never the dashboard's", which is
//     the D7 claim the task exists for, and which is asserted as *the document
//     that was loaded* rather than as a decision that was made;
//   - treating an unreadable version as anything but a refusal fails the two
//     readers that cannot answer (a throw, an empty line);
//   - re-running the handshake on `activate` -- or opening the dashboard there
//     -- fails the reuse test, whose reader counts its calls;
//   - deleting the handshake from the entry point fails the source scan at the
//     bottom, which is the only way to hold `src/main/index.ts` to anything at
//     all: it imports the Electron *runtime* (`app`, `BrowserWindow`,
//     `protocol`, `session`), so it cannot be imported here, and reading it as
//     text is what makes "the entry point is wired" a checked claim instead of
//     a review of the diff.
//
// Everything below the scan runs the real functions from `src/main/window.ts` --
// the same ones the entry point calls -- with a recording `BrowserWindow` in
// place of Electron's. `index.ts` itself holds no decision this file could
// test: `openStartupWindow` is the order, `openWindowFor` is the two doors, and
// the entry point is the call.

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import type {
  BrowserWindow,
  BrowserWindowConstructorOptions,
  Session,
  WebContents,
} from 'electron'

import { APP_ENTRY_URL, REFUSAL_ENTRY_URL } from '../../src/main/protocol'
import {
  CONSOLE_WINDOW_TITLE,
  HANDSHAKE_NOT_FOUND,
  HANDSHAKE_UNREADABLE,
  createConsoleLifecycle,
  openStartupWindow,
  openWindowFor,
  runVersionHandshake,
  versionReadFrom,
} from '../../src/main/window'
import type { ConsoleWindowDeps, VersionRead } from '../../src/main/window'
import { RENDERER_UNREADABLE, startConsole } from '../../src/renderer/app'
import type { ScreenGate } from '../../src/renderer/app'
import { CLI_VERSION_BUILT_AGAINST, SKEW_HEADLINES, classifyCliVersion } from '../../src/shared/version'
import type { CliCompatibility, SkewElement, SkewFactory } from '../../src/shared/version'

/**
 * assertNoReader is never called and it is not a test: it is the compile-time
 * half of "`activate` cannot read a version", and what checks it is
 * `tsc --noEmit` -- the same shape `ipc.test.ts`'s `assertClosedSurface` uses.
 * If `ConsoleWindowDeps` ever gains a reader, this directive becomes unused and
 * the typecheck fails, which is the right place for a claim about a type.
 */
function assertNoReader(deps: ConsoleWindowDeps): void {
  // @ts-expect-error `openWindowFor`'s deps hold no reader at all
  void deps.readVersion
  void deps.BrowserWindow
}

/**
 * recordingWindows is the smallest stand-in for Electron that the window
 * builders run against. It is local rather than shared with `window.test.ts`,
 * which models more of Electron (the `page-title-updated` rule, the document's
 * own title, the permission handlers) because *it* asserts the hardening; this
 * file asserts the order and the document, so it records exactly those two
 * things and nothing else.
 */
function recordingWindows(): {
  BrowserWindow: new (options: BrowserWindowConstructorOptions) => BrowserWindow
  loaded: string[]
  built: unknown[]
  titles: (string | undefined)[]
} {
  const loaded: string[] = []
  const built: unknown[] = []
  const titles: (string | undefined)[] = []
  const WindowClass = function (options: BrowserWindowConstructorOptions): object {
    titles.push(options.title)
    const webContents = {
      on() {},
      setWindowOpenHandler() {},
    } as unknown as WebContents
    const window = {
      webContents,
      title: typeof options.title === 'string' ? options.title : '',
      on() {},
      once() {},
      show() {},
      async loadURL(url: string) {
        loaded.push(url)
      },
    }
    built.push(window)
    return window
  } as unknown as new (options: BrowserWindowConstructorOptions) => BrowserWindow
  return { BrowserWindow: WindowClass, loaded, built, titles }
}

/** aSession is Electron's `session` as far as `denyEveryPermission` uses it. */
const aSession = {
  setPermissionRequestHandler() {},
  setPermissionCheckHandler() {},
} as unknown as Session

/** readsVersion is a reader that answers one fixed line. */
const readsVersion =
  (stdout: string) =>
  async (): Promise<VersionRead> => ({ ok: true, stdout })

describe('the startup path', () => {
  it('runs the handshake before any window exists', async () => {
    const windows = recordingWindows()
    const atRead: number[] = []
    await openStartupWindow({
      BrowserWindow: windows.BrowserWindow,
      session: aSession,
      readVersion: async () => {
        atRead.push(windows.built.length)
        return { ok: true, stdout: `${CLI_VERSION_BUILT_AGAINST}\n` }
      },
    })
    // The whole claim, in one number: when the version was read, no window had
    // been constructed. A handshake that ran after the window was built would
    // have put a dashboard on screen before it knew which CLI it was driving.
    expect(atRead).toEqual([0])
    expect(windows.built).toHaveLength(1)
    expect(windows.loaded).toEqual([APP_ENTRY_URL])
  })

  it('a refused version loads the refusal document and never the dashboard’s', async () => {
    const windows = recordingWindows()
    const opened = await openStartupWindow({
      BrowserWindow: windows.BrowserWindow,
      session: aSession,
      readVersion: readsVersion('0.5.0\n'),
    })
    expect(opened.compatibility.mode).toBe('refused')
    // Exactly one window, and it is the refusal document. Asserted as the
    // document rather than as a flag, because a console that built the
    // dashboard window and asked the renderer not to draw in it would satisfy
    // every other assertion in this file.
    expect(windows.built).toHaveLength(1)
    expect(windows.loaded).toEqual([REFUSAL_ENTRY_URL])
    expect(windows.loaded).not.toContain(APP_ENTRY_URL)
    expect(windows.titles[0]).toContain(SKEW_HEADLINES.refused)
  })

  it('a reduced version loads the dashboard document, with the banner in the title', async () => {
    const windows = recordingWindows()
    const opened = await openStartupWindow({
      BrowserWindow: windows.BrowserWindow,
      session: aSession,
      readVersion: readsVersion('0.9.0\n'),
    })
    expect(opened.compatibility.mode).toBe('reduced')
    expect(windows.loaded).toEqual([APP_ENTRY_URL])
    expect(windows.titles[0]).toContain(SKEW_HEADLINES.reduced)
  })

  it('a reader that throws is a refusal, and opens the refusal document', async () => {
    const windows = recordingWindows()
    const opened = await openStartupWindow({
      BrowserWindow: windows.BrowserWindow,
      session: aSession,
      readVersion: async () => {
        throw new Error('the spawn failed')
      },
    })
    expect(opened.compatibility.mode).toBe('refused')
    expect(opened.compatibility.skew).toBe('unreadable')
    expect(windows.loaded).toEqual([REFUSAL_ENTRY_URL])
  })

  it('a reader that answered nothing parsable is a refusal too', async () => {
    for (const stdout of ['', '\n', 'banana\n']) {
      const windows = recordingWindows()
      const opened = await openStartupWindow({
        BrowserWindow: windows.BrowserWindow,
        session: aSession,
        readVersion: readsVersion(stdout),
      })
      // "Cannot place the CLI" is not "probably fine": the console draws no
      // figures for a version it could not read.
      expect(opened.compatibility.mode, JSON.stringify(stdout)).toBe('refused')
      expect(windows.loaded, JSON.stringify(stdout)).toEqual([REFUSAL_ENTRY_URL])
    }
  })
})

describe('the window the stored answer opens (the activate path)', () => {
  it('reuses the answer it was given, and reads nothing', async () => {
    // The store is read once at startup and the mode does not change while the
    // app runs, so `activate` opens a window rather than running the handshake
    // again: a dock-icon click must not be able to change which console the
    // operator is looking at. What makes that structural rather than a habit is
    // the deps `openWindowFor` takes -- a `BrowserWindow` and a `session`, and
    // no reader.
    const windows = recordingWindows()
    await openWindowFor(
      { BrowserWindow: windows.BrowserWindow, session: aSession },
      classifyCliVersion(CLI_VERSION_BUILT_AGAINST),
    )
    expect(windows.loaded).toEqual([APP_ENTRY_URL])
    expect(windows.titles[0]).toBe(CONSOLE_WINDOW_TITLE)
  })

  it('opens the refusal document again for a stored refusal', async () => {
    const windows = recordingWindows()
    const stored = classifyCliVersion('0.5.0')
    await openWindowFor({ BrowserWindow: windows.BrowserWindow, session: aSession }, stored)
    expect(windows.loaded).toEqual([REFUSAL_ENTRY_URL])
    expect(windows.loaded).not.toContain(APP_ENTRY_URL)
    expect(windows.titles[0]).toContain(SKEW_HEADLINES.refused)
  })
})

// ---------------------------------------------------------------------------
// What a failed read may say (round 2: never the binary's own words)
// ---------------------------------------------------------------------------

/** HOSTILE_LINE is the reviewer's case: an escape, markup and a version-shaped
 * run, as a stub would print it. */
const HOSTILE_LINE = '\u001b[31m0.8.1\u001b[0m <h1>fugaro says hello</h1>'

describe('a failed read crosses as a category, never as text', () => {
  it('drops the resolver’s reason, which carries what the binary printed', async () => {
    // The resolver's reason for an unparsable line quotes the line, and its
    // reason for a non-zero exit quotes stderr: both are what the reviewer saw
    // reach `compatibility.because` verbatim.
    const quoting: readonly string[] = [
      `the binary printed ${JSON.stringify(HOSTILE_LINE)}, which is not a version`,
      `the binary exited 2 for \`version\`: ${HOSTILE_LINE}`,
    ]
    for (const reason of quoting) {
      const read = versionReadFrom({ ok: false, path: '/opt/homebrew/bin/fugaro', reason })
      expect(read).toEqual({ ok: false, failure: 'unreadable' })
      const compatibility = await runVersionHandshake({ readVersion: async () => read })
      expect(compatibility.mode).toBe('refused')
      expect(compatibility.because).toBe(HANDSHAKE_UNREADABLE)
      const wire = JSON.stringify(compatibility)
      expect(wire).not.toContain('\\u001b')
      expect(wire).not.toContain('fugaro says hello')
      expect(wire).not.toContain('<h1>')
    }
  })

  it('says "not found" in its own words, and names no path it looked at', async () => {
    const read = versionReadFrom({
      ok: false,
      path: null,
      reason: 'no fugaro binary was found: looked at /Users/someone/secret-dir/fugaro',
    })
    expect(read).toEqual({ ok: false, failure: 'not-found' })
    const compatibility = await runVersionHandshake({ readVersion: async () => read })
    expect(compatibility.because).toBe(HANDSHAKE_NOT_FOUND)
    expect(JSON.stringify(compatibility)).not.toContain('secret-dir')
  })

  it('a reader that answers outside its type still produces only the console’s sentence', async () => {
    const compatibility = await runVersionHandshake({
      readVersion: async () => ({ ok: false, failure: HANDSHAKE_LINE_AS_FAILURE }) as unknown as VersionRead,
    })
    expect(compatibility.because).toBe(HANDSHAKE_UNREADABLE)
    // And the old shape -- a free `reason` -- is not read at all.
    const legacy = await runVersionHandshake({
      readVersion: async () => ({ ok: false, reason: HOSTILE_LINE }) as unknown as VersionRead,
    })
    expect(legacy.because).toBe(HANDSHAKE_UNREADABLE)
  })

  it('a successful read carries the parsed version, and a hostile line is refused with no trace', async () => {
    const compatibility = await runVersionHandshake({ readVersion: readsVersion(HOSTILE_LINE) })
    expect(compatibility.mode).toBe('refused')
    expect(compatibility.cliVersion).toBeNull()
    expect(JSON.stringify(compatibility)).not.toContain('fugaro says hello')
  })
})

/** HANDSHAKE_LINE_AS_FAILURE is a failure "category" that is really a line. */
const HANDSHAKE_LINE_AS_FAILURE = HOSTILE_LINE

// ---------------------------------------------------------------------------
// The lifecycle: the startup once, and `activate` (round 2: behaviour, not a
// source match)
// ---------------------------------------------------------------------------

/** closableWindows is `recordingWindows` with a `close()` and a count of the
 * windows that are still open, which is what `activate` asks. */
function closableWindows(): ReturnType<typeof recordingWindows> & {
  open(): number
  closeAll(): void
} {
  const windows = recordingWindows()
  const closed = new Set<unknown>()
  return {
    ...windows,
    open: () => windows.built.filter((window) => !closed.has(window)).length,
    closeAll: () => {
      for (const window of windows.built) closed.add(window)
    },
  }
}

/** deferred is a promise this file resolves by hand. */
function deferred<T>(): { promise: Promise<T>; resolve(value: T): void } {
  let resolve: (value: T) => void = () => undefined
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

describe('the lifecycle', () => {
  it('a refused console reopened by `activate` is the refusal window again, and never a dashboard', async () => {
    const windows = closableWindows()
    let reads = 0
    const lifecycle = createConsoleLifecycle({
      windowDeps: () => ({ BrowserWindow: windows.BrowserWindow, session: aSession }),
      readVersion: async () => {
        reads += 1
        return { ok: true, stdout: '0.5.0\n' }
      },
      openWindowCount: windows.open,
    })
    expect((await lifecycle.start()).mode).toBe('refused')
    expect(windows.loaded).toEqual([REFUSAL_ENTRY_URL])

    // The window is open: a dock-icon click opens nothing.
    await lifecycle.activate()
    expect(windows.built).toHaveLength(1)

    // Closed, then clicked: one refusal window, the handshake not read again.
    windows.closeAll()
    await lifecycle.activate()
    expect(windows.built).toHaveLength(2)
    expect(windows.open()).toBe(1)
    expect(windows.loaded).toEqual([REFUSAL_ENTRY_URL, REFUSAL_ENTRY_URL])
    expect(windows.loaded).not.toContain(APP_ENTRY_URL)
    expect(reads).toBe(1)

    // Two clicks at once after another close: still one window.
    windows.closeAll()
    await Promise.all([lifecycle.activate(), lifecycle.activate()])
    expect(windows.open()).toBe(1)
    expect(windows.loaded).not.toContain(APP_ENTRY_URL)
  })

  it('is single-flight: `activate` during the handshake waits for it, and opens no second window', async () => {
    const windows = closableWindows()
    const answer = deferred<VersionRead>()
    let reads = 0
    const lifecycle = createConsoleLifecycle({
      windowDeps: () => ({ BrowserWindow: windows.BrowserWindow, session: aSession }),
      readVersion: () => {
        reads += 1
        return answer.promise
      },
      openWindowCount: windows.open,
    })
    const starting = lifecycle.start()
    // The handshake is running and no window exists: the click that comes now
    // is the one the first version answered with a second `startConsole`.
    const clicked = lifecycle.activate()
    const again = lifecycle.start()
    expect(again).toBe(starting)
    answer.resolve({ ok: true, stdout: `${CLI_VERSION_BUILT_AGAINST}\n` })
    await Promise.all([starting, clicked, again])
    expect(reads).toBe(1)
    expect(windows.built).toHaveLength(1)
    expect(windows.loaded).toEqual([APP_ENTRY_URL])
  })

  it('opens nothing on an `activate` before the startup has begun', async () => {
    // `main` starts the console once the bundle is served; a click before that
    // must not start a handshake (or load a document) ahead of the handler.
    const windows = closableWindows()
    let reads = 0
    const lifecycle = createConsoleLifecycle({
      windowDeps: () => ({ BrowserWindow: windows.BrowserWindow, session: aSession }),
      readVersion: async () => {
        reads += 1
        return { ok: true, stdout: `${CLI_VERSION_BUILT_AGAINST}\n` }
      },
      openWindowCount: windows.open,
    })
    await lifecycle.activate()
    expect(reads).toBe(0)
    expect(windows.built).toHaveLength(0)
  })
})

// ---------------------------------------------------------------------------
// The renderer's half of the same handshake
// ---------------------------------------------------------------------------

/**
 * FakeScreen and drawnFactory are a recording DOM, local to this file for the
 * same reason `recordingWindows` is: `version.test.ts` asserts what
 * `renderVersionSkew` draws, and what this section asserts is *whether* it was
 * drawn and what the gate did afterwards.
 */
class FakeScreen {
  textContent: string | null = null
  readonly attributes = new Map<string, string>()
  readonly nodes: FakeScreen[] = []
  constructor(readonly tag: string) {}
  setAttribute(name: string, value: string): void {
    this.attributes.set(name, value)
  }
  getAttribute(name: string): string | null {
    return this.attributes.get(name) ?? null
  }
  removeAttribute(name: string): void {
    this.attributes.delete(name)
  }
  append(child: SkewElement): void {
    this.nodes.push(child as FakeScreen)
  }
}

const drawnFactory: SkewFactory = { create: (tag: string) => new FakeScreen(tag) }

/** drawnTree is every node of a tree, the root first. */
function drawnTree(node: SkewElement): readonly FakeScreen[] {
  const root = node as FakeScreen
  return [root, ...root.nodes.flatMap((child) => drawnTree(child))]
}

/** aMount is where the entry point's drawings go, and it counts them. */
function aMount(): { append(child: SkewElement): void; drawings: FakeScreen[] } {
  const drawings: FakeScreen[] = []
  return {
    drawings,
    append(child: SkewElement) {
      drawings.push(child as FakeScreen)
    },
  }
}

/** answers is a bridge that answers whatever it is handed, once. */
function answers(answer: unknown) {
  return { getCompatibility: async (): Promise<unknown> => answer }
}

const okHandshake = classifyCliVersion(CLI_VERSION_BUILT_AGAINST)
const reducedHandshake = classifyCliVersion('0.9.0')
const refusedHandshake = classifyCliVersion('0.5.0')

/** REJECTS is a bridge whose call fails, the way a dead channel does. */
const rejects = { getCompatibility: async (): Promise<unknown> => Promise.reject(new Error('gone')) }

describe('the renderer’s gate', () => {
  it('draws nothing at all until the answer has arrived', async () => {
    // The order this task is about, on the renderer's side. A screen that drew
    // first and asked later would already have drawn, so the entry point's
    // first act is the question -- and the mount stays empty until it is
    // answered.
    const mount = aMount()
    const slow = {
      getCompatibility: async (): Promise<unknown> => {
        // A handshake answer that takes a tick to arrive, so the assertion
        // below is made while the console still has no answer.
        await new Promise((resolve) => setTimeout(resolve, 0))
        return { ok: true, data: okHandshake }
      },
    }
    const started = startConsole({ bridge: slow, mount, factory: drawnFactory, theme: 'dark' })
    expect(mount.drawings).toHaveLength(0)
    const gate = await started
    // An `ok` handshake has nothing to draw: the dashboard is mounted by the
    // caller, through the gate.
    expect(mount.drawings).toHaveLength(0)
    expect(gate.compatibility.mode).toBe('ok')
  })

  it('lets an `ok` console draw every view', async () => {
    const mount = aMount()
    const gate = await startConsole({
      bridge: answers({ ok: true, data: okHandshake }),
      mount,
      factory: drawnFactory,
      theme: 'dark',
    })
    const drew: string[] = []
    for (const view of ['spend', 'caps', 'kill'] as const) {
      expect(gate.drawScreen(view, () => drew.push(view)), view).toBe('drawn')
    }
    expect(drew).toEqual(['spend', 'caps', 'kill'])
  })

  it('draws the banner in a reduced mode, and refuses to draw a withheld view', async () => {
    const mount = aMount()
    const gate = await startConsole({
      bridge: answers({ ok: true, data: reducedHandshake }),
      mount,
      factory: drawnFactory,
      theme: 'dark',
    })
    expect(mount.drawings).toHaveLength(1)
    const banner = mount.drawings[0]
    expect(banner?.getAttribute('data-mode')).toBe('reduced')
    expect(banner?.getAttribute('data-skew')).toBe('newer')
    expect(banner?.getAttribute('data-theme')).toBe('dark')
    expect(
      drawnTree(banner as SkewElement).some((node) => node.textContent === SKEW_HEADLINES.reduced),
    ).toBe(true)
    // The three views `hidesView` withholds are the three the gate refuses, and
    // the screen's own drawing is not called at all -- the second half of the
    // defence, in the document rather than in the main process.
    for (const view of ['spend', 'caps', 'kill'] as const) {
      let called = false
      expect(gate.drawScreen(view, () => (called = true)), view).toBe('withheld')
      expect(called, view).toBe(false)
    }
  })

  it('draws the refusal screen and gates every view off for a refused console', async () => {
    const mount = aMount()
    const gate = await startConsole({
      bridge: answers({ ok: true, data: refusedHandshake }),
      mount,
      factory: drawnFactory,
      theme: 'dark',
    })
    expect(gate.compatibility.mode).toBe('refused')
    expect(mount.drawings).toHaveLength(1)
    const screen = mount.drawings[0]
    expect(screen?.getAttribute('data-mode')).toBe('refused')
    expect(
      drawnTree(screen as SkewElement).some((node) => node.textContent === SKEW_HEADLINES.refused),
    ).toBe(true)
    for (const view of ['spend', 'caps', 'kill'] as const) {
      let called = false
      expect(gate.drawScreen(view, () => (called = true)), view).toBe('refused')
      expect(called, view).toBe(false)
    }
  })

  it.each([
    ['a rejected call', rejects],
    ['the console’s own refusal reply', answers({ ok: false, reason: 'the console is not reachable' })],
    ['no answer at all', answers(null)],
    ['an answer that is not a compatibility', answers({ ok: true, data: { mode: 'probably' } })],
    [
      'a compatibility whose hidden list disagrees with its mode',
      answers({ ok: true, data: { ...okHandshake, hidden: ['spend', 'caps', 'kill'] } }),
    ],
  ])('treats %s as the refusal, which is the answer that draws no figures', async (_why, bridge) => {
    const mount = aMount()
    const gate = await startConsole({ bridge, mount, factory: drawnFactory, theme: 'light' })
    expect(gate.compatibility.mode).toBe('refused')
    expect(gate.compatibility.skew).toBe('unreadable')
    // The renderer's own sentence, and nothing the answer carried.
    expect(gate.compatibility.because).toBe(RENDERER_UNREADABLE)
    expect(mount.drawings).toHaveLength(1)
    expect(mount.drawings[0]?.getAttribute('data-mode')).toBe('refused')
    expect(gate.drawScreen('spend', () => undefined)).toBe('refused')
  })

  it('gates on a frozen copy of the checked fields, which a later write cannot change', async () => {
    // `compatibilityFrom` rebuilds the answer from the fields it checked, so
    // the gate's question (`hidesView`) is asked of a value nothing else holds:
    // mutating what the bridge handed over afterwards does not reopen a view.
    const mount = aMount()
    const handed = { ...reducedHandshake, hidden: [...reducedHandshake.hidden] }
    const gate: ScreenGate = await startConsole({
      bridge: answers({ ok: true, data: handed }),
      mount,
      factory: drawnFactory,
      theme: 'light',
    })
    expect(gate.compatibility).toEqual(reducedHandshake)
    expect(gate.compatibility).not.toBe(handed)
    expect(Object.isFrozen(gate.compatibility)).toBe(true)
    handed.hidden.length = 0
    ;(handed as { mode: string }).mode = 'ok'
    expect((gate.compatibility as CliCompatibility).hidden).toContain('spend')
    expect(gate.drawScreen('spend', () => undefined)).toBe('withheld')
  })
})

// ---------------------------------------------------------------------------
// The entry point: read as text, because it cannot be imported
// ---------------------------------------------------------------------------

const indexSource = readFileSync(
  fileURLToPath(new URL('../../src/main/index.ts', import.meta.url)),
  'utf8',
)
  .replace(/\/\*[\s\S]*?\*\//g, '')
  .replace(/^\s*\/\/.*$/gm, '')

describe('the entry point is wired to the handshake', () => {
  it('calls the startup, and never builds a dashboard window itself', () => {
    // The positive half and the negative one. `createConsoleWindow` is the
    // dashboard's door; an entry point that called it directly would be one
    // that could open a dashboard without an answer, which is what this task
    // removed.
    expect(indexSource).toContain('createConsoleLifecycle(')
    expect(indexSource).toContain('readVersion: readCliVersion')
    expect(indexSource).not.toContain('openWindowFor(')
    expect(indexSource).not.toContain('createConsoleWindow')
    expect(indexSource).not.toContain('APP_ENTRY_URL')
  })

  it('reads the version through the resolver, not through a path of its own', () => {
    // Rule 4's resolution and the CLI's own `version` line, from the one module
    // that owns spawning. A version read that built its own command line here
    // would be a second spawner outside `spawn.ts` -- which `ipc.test.ts`'s
    // source scans also refuse.
    expect(indexSource).toContain('resolveBinary(')
    expect(indexSource).toContain('privateWorkingDirectory()')
    // The resolver's reason quotes what the binary printed, so the entry point
    // hands over the category and never the reason.
    expect(indexSource).toContain('versionReadFrom(resolution)')
    expect(indexSource).not.toContain('.reason')
    expect(indexSource).not.toContain('child_process')
  })

  it('hands `activate` to the lifecycle, whose behaviour is tested above', () => {
    // The behaviour -- a refused console reopens as the refusal, the startup
    // is single-flight -- is `createConsoleLifecycle`'s, driven in 'the
    // lifecycle' above. What is left to hold here is that the entry point
    // uses it, and keeps no store of its own beside it.
    expect(indexSource).toContain('lifecycle.activate()')
    expect(indexSource).not.toContain('startupWindow(')
    expect(indexSource).not.toMatch(/let startup\b/)
  })

  it('runs the startup from main, after the bundle is served exactly once', () => {
    const main = /async function main\(\)[\s\S]*?\n}/.exec(indexSource)?.[0] ?? ''
    expect(main).toContain('serveBundle()')
    expect(main).toContain('await lifecycle.start()')
    // `serveBundle` registers a protocol handler that throws on a second call,
    // so it belongs to `main` and not to the repeatable `activate` path. The
    // pattern is anchored to a call on a line of its own, so the function's own
    // declaration is not counted as one.
    expect(indexSource.match(/^\s*serveBundle\(\)$/gm) ?? []).toHaveLength(1)
  })
})
