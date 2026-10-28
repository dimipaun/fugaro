// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
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

import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import type {
  BrowserWindow,
  BrowserWindowConstructorOptions,
  Session,
  WebContents,
} from 'electron'

import { APP_ENTRY_URL, REFUSAL_ENTRY_URL } from '../../src/main/protocol'
import { privateWorkingDirectory, resolveHandshakeBinary } from '../../src/main/spawn'
import { SPAWN_LOG_ENV, createSpawnLog } from '../../src/main/spawnlog'
import { installStub } from './stub'
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

/**
 * PRELOAD_PATH is the compiled preload the dashboard window loads (Task 3.4a).
 * It is a path under the app's own directory, spelled the way
 * `src/main/index.ts` spells it, and every window dep below carries it: the
 * refusal window is the one that ignores it, and that is
 * `test/unit/window.test.ts`'s assertion.
 */
const PRELOAD_PATH = '/opt/fugaro-console/dist/preload/index.js'

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
      session: aSession, preloadPath: PRELOAD_PATH,
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
      session: aSession, preloadPath: PRELOAD_PATH,
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
      session: aSession, preloadPath: PRELOAD_PATH,
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
      session: aSession, preloadPath: PRELOAD_PATH,
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
        session: aSession, preloadPath: PRELOAD_PATH,
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
      { BrowserWindow: windows.BrowserWindow, session: aSession, preloadPath: PRELOAD_PATH },
      classifyCliVersion(CLI_VERSION_BUILT_AGAINST),
    )
    expect(windows.loaded).toEqual([APP_ENTRY_URL])
    expect(windows.titles[0]).toBe(CONSOLE_WINDOW_TITLE)
  })

  it('opens the refusal document again for a stored refusal', async () => {
    const windows = recordingWindows()
    const stored = classifyCliVersion('0.5.0')
    await openWindowFor({ BrowserWindow: windows.BrowserWindow, session: aSession, preloadPath: PRELOAD_PATH }, stored)
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
      const read = versionReadFrom({ ok: false, path: '/opt/homebrew/bin/fugaro', kind: 'no-binary', reason })
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
      kind: 'no-binary',
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
      windowDeps: () => ({ BrowserWindow: windows.BrowserWindow, session: aSession, preloadPath: PRELOAD_PATH }),
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
      windowDeps: () => ({ BrowserWindow: windows.BrowserWindow, session: aSession, preloadPath: PRELOAD_PATH }),
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
      windowDeps: () => ({ BrowserWindow: windows.BrowserWindow, session: aSession, preloadPath: PRELOAD_PATH }),
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

  it('decides once, before the first document loads, and never again', async () => {
    // Task 3.4a, step 2, from the lifecycle's side: the callback is threaded
    // through `openStartupWindow` to `startupWindow`, so it runs in the one
    // moment the answer exists and no window does. The store the entry point
    // writes is written **once** (`src/main/index.ts`'s `onDecided`), which is
    // what makes the answer a decision rather than a per-window reading -- and
    // `activate` after a close must not run it again, or a console that refused
    // would get a second chance to register handlers on the way back.
    const windows = closableWindows()
    const decided: CliCompatibility[] = []
    const loadedWhenDecided: number[] = []
    const lifecycle = createConsoleLifecycle({
      windowDeps: () => ({ BrowserWindow: windows.BrowserWindow, session: aSession, preloadPath: PRELOAD_PATH }),
      readVersion: async () => ({ ok: true, stdout: '0.6.9\n' }),
      openWindowCount: windows.open,
      onDecided: (compatibility) => {
        decided.push(compatibility)
        // The ordering, from inside the callback: nothing has been loaded yet,
        // so no renderer can have asked for a channel this registration has not
        // put there.
        loadedWhenDecided.push(windows.loaded.length)
      },
    })
    expect((await lifecycle.start()).mode).toBe('reduced')
    expect(decided.map((compatibility) => compatibility.mode)).toEqual(['reduced'])
    expect(loadedWhenDecided).toEqual([0])
    expect(windows.loaded).toEqual([APP_ENTRY_URL])

    // A click while the window is open, and another after closing it: the
    // handshake is not read again and the decision is not taken again.
    await lifecycle.activate()
    windows.closeAll()
    await lifecycle.activate()
    expect(windows.built).toHaveLength(2)
    expect(decided).toHaveLength(1)
    expect(loadedWhenDecided).toEqual([0])
  })
})

describe('a choose in the picker (Task 3.4b, step 5)', () => {
  it('replaces a refused console’s window with the dashboard, decides again, and closes the one it replaced', async () => {
    const windows = recordingWindows()
    const closed = new Set<unknown>()
    const Closable = function (this: unknown, options: BrowserWindowConstructorOptions): object {
      const window = new windows.BrowserWindow(options) as unknown as Record<string, unknown>
      window['close'] = () => {
        closed.add(window)
      }
      window['isDestroyed'] = () => closed.has(window)
      return window
    } as unknown as new (options: BrowserWindowConstructorOptions) => BrowserWindow
    const decided: string[] = []
    let reads = 0
    const lifecycle = createConsoleLifecycle({
      windowDeps: () => ({ BrowserWindow: Closable, session: aSession, preloadPath: PRELOAD_PATH }),
      readVersion: async () => {
        reads += 1
        return { ok: true, stdout: '0.5.0\n' }
      },
      openWindowCount: () => windows.built.filter((window) => !closed.has(window)).length,
      onDecided: (compatibility) => {
        decided.push(compatibility.mode)
      },
    })
    expect((await lifecycle.start()).mode).toBe('refused')
    expect(windows.loaded).toEqual([REFUSAL_ENTRY_URL])

    // The picker ran the chosen installation's handshake and it answered `ok`.
    await lifecycle.choose(classifyCliVersion(CLI_VERSION_BUILT_AGAINST))
    // The same callback the startup ran, so the entry point registers the
    // surface now (it registered nothing for the refusal).
    expect(decided).toEqual(['refused', 'ok'])
    // The dashboard is opened through `openWindowFor`, and the refusal window
    // it replaces is closed: one console window, the right one.
    expect(windows.loaded).toEqual([REFUSAL_ENTRY_URL, APP_ENTRY_URL])
    expect(closed.has(windows.built[0])).toBe(true)
    expect(closed.has(windows.built[1])).toBe(false)
    // The startup's handshake was not read again: the picker read this one.
    expect(reads).toBe(1)

    // And the stored answer is the new one: a window closed and reopened by
    // `activate` is the dashboard, not the refusal it replaced.
    closed.add(windows.built[1])
    await lifecycle.activate()
    expect(windows.loaded).toEqual([REFUSAL_ENTRY_URL, APP_ENTRY_URL, APP_ENTRY_URL])
    expect(decided).toEqual(['refused', 'ok'])
  })

  it('leaves one dashboard window after two rapid chooses (review S6)', async () => {
    const windows = recordingWindows()
    const closed = new Set<unknown>()
    const Closable = function (this: unknown, options: BrowserWindowConstructorOptions): object {
      const window = new windows.BrowserWindow(options) as unknown as Record<string, unknown>
      window['close'] = () => {
        closed.add(window)
      }
      window['isDestroyed'] = () => closed.has(window)
      return window
    } as unknown as new (options: BrowserWindowConstructorOptions) => BrowserWindow
    const lifecycle = createConsoleLifecycle({
      windowDeps: () => ({ BrowserWindow: Closable, session: aSession, preloadPath: PRELOAD_PATH }),
      readVersion: readsVersion(`${CLI_VERSION_BUILT_AGAINST}\n`),
      openWindowCount: () => windows.built.filter((window) => !closed.has(window)).length,
    })
    await lifecycle.start()
    // Two chooses in the same turn: each reads the window it replaces, so run
    // side by side both would replace the *startup's* window and leave two
    // dashboards open.
    await Promise.all([
      lifecycle.choose(classifyCliVersion(CLI_VERSION_BUILT_AGAINST)),
      lifecycle.choose(classifyCliVersion('0.9.0')),
    ])
    const open = windows.built.filter((window) => !closed.has(window))
    expect(windows.built).toHaveLength(3)
    expect(open).toEqual([windows.built[2]])
  })
})

// ---------------------------------------------------------------------------
// The startup handshake's resolution (Task 3.4b, steps 3 and 4; review B1, S4)
// ---------------------------------------------------------------------------

describe('the startup handshake resolves rule 4 for the active installation', () => {
  /** machine is a scratch "machine": a well-known prefix and a PATH entry, each
   * holding a `fugaro` that answers an in-range version. The prefix is what the
   * resolution is allowed to find; the PATH entry is the one nothing may ever
   * ask for (owner's ruling of 2026-10-20, rule 4), so every test here asserts
   * it was never run. */
  function machine() {
    const root = mkdtempSync(join(tmpdir(), 'fugaro-handshake-'))
    const wellKnown = installStub(join(root, 'opt', 'bin'), { commands: { version: { stdout: `${CLI_VERSION_BUILT_AGAINST}\n` } } })
    const onPath = installStub(join(root, 'path'), { commands: { version: { stdout: `${CLI_VERSION_BUILT_AGAINST}\n` } } })
    const logPath = join(root, 'spawn.jsonl')
    const appEnv = {
      PATH: join(root, 'path'),
      // Planted: an ambient project the console must never pick up (D13).
      FUGARO_PROJECT: 'planted-ambient-project',
      [SPAWN_LOG_ENV]: logPath,
    }
    const read = async (installation: Parameters<typeof resolveHandshakeBinary>[0]['installation']) => {
      const log = createSpawnLog(appEnv)
      try {
        return await resolveHandshakeBinary({
          installation,
          appEnv,
          directory: privateWorkingDirectory(),
          log,
          home: root,
          prefixes: [join(root, 'opt', 'bin')],
        })
      } finally {
        log.close()
      }
    }
    const records = (): readonly { argv: string[]; project: string }[] => {
      try {
        return readFileSync(logPath, 'utf8')
          .split('\n')
          .filter((line) => line !== '')
          .map((line) => JSON.parse(line) as { argv: string[]; project: string })
      } catch {
        return []
      }
    }
    return { root, wellKnown, onPath, read, records }
  }

  // The owner's ruling of 2026-10-20 replaces review B1's "no fallthrough": a
  // store's `binary` is an **optional override**, not a pin, because the console
  // resolves the `fugaro` that is installed on this machine and asks the
  // operator for nothing. So a stored path that has gone is not a refusal -- it
  // is the first candidate of rule 4's order that did not answer, and the walk
  // carries on to the well-known locations. What B1 protects is unchanged in
  // substance: the app's own `PATH` is still never asked.
  it('falls through a stored binary that has gone, to the installed one', async () => {
    const m = machine()
    const resolution = await m.read({ binaryPath: join(m.root, 'gone', 'fugaro'), target: { project: 'gone' } })
    expect(resolution).toMatchObject({ ok: true, path: m.wellKnown.binary, source: 'well-known' })
    // The binary that answered is the installed one, and it was asked with the
    // installation's project -- the log still says which installation this
    // `version` was for.
    expect(m.wellKnown.reports().map((report) => report.argv)).toEqual([['version']])
    expect(m.records().map((record) => [record.argv[0], record.argv[1], record.project])).toEqual([
      [m.wellKnown.binary, 'version', 'gone'],
    ])
    // ...and the app's own `PATH` entry was never asked, gone stored path or not.
    expect(m.onPath.reports()).toEqual([])
  })

  it('falls through a stored binary that is not executable, to the installed one', async () => {
    const m = machine()
    const plain = join(m.root, 'plain-fugaro')
    writeFileSync(plain, '#!/bin/sh\necho 0.8.1\n', { mode: 0o644 })
    const resolution = await m.read({ binaryPath: plain, target: { project: 'acme' } })
    expect(resolution).toMatchObject({ ok: true, path: m.wellKnown.binary })
    // The unreadable candidate is not *run* either: a file without an execute
    // bit is not a candidate at all.
    expect(resolution.ok && resolution.version).toBe(CLI_VERSION_BUILT_AGAINST)
    expect(m.onPath.reports()).toEqual([])
  })

  it('reads the stored binary with its project, and the spawn log records that project', async () => {
    const m = machine()
    const own = installStub(join(m.root, 'own'), { commands: { version: { stdout: '0.9.0\n' } } })
    const resolution = await m.read({ binaryPath: own.binary, target: { project: 'acme' } })
    expect(resolution).toMatchObject({ ok: true, path: own.binary, version: '0.9.0', source: 'configured' })
    expect(own.reports().map((report) => report.argv)).toEqual([['version']])
    expect(m.records().map((record) => [record.argv[0], record.argv[1], record.project])).toEqual([
      [own.binary, 'version', 'acme'],
    ])
    expect(m.wellKnown.reports()).toEqual([])
  })

  it('with none, walks rule 4 with no project, and never the ambient FUGARO_PROJECT', async () => {
    const m = machine()
    const resolution = await m.read(null)
    expect(resolution).toMatchObject({ ok: true, path: m.wellKnown.binary })
    const records = m.records()
    expect(records).toHaveLength(1)
    expect(records[0]?.project).toBe('')
    // The child's own environment carries no project either.
    expect(m.wellKnown.reports()[0]?.project ?? '').toBe('')
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
    expect(indexSource).toContain('resolveHandshakeBinary(')
    expect(indexSource).toContain('privateWorkingDirectory()')
    // The resolver's reason quotes what the binary printed, so the entry point
    // hands over the category and never the reason -- **of the reader**, which
    // is what this pair is about, and the reason the two assertions below read
    // `readCliVersion`'s own body rather than the file. `launchLookUp`
    // (V2.5.1a part 2a) does hand `resolution.reason` on, as the `TargetLookup`
    // refusal the launcher's screen draws, and that is the launch's seam in
    // `launch.ts` rather than a version category: a file-wide scan would make
    // the reader's rule a rule about a function it does not govern, and the
    // next legitimate reason would be argued away instead of the reader being
    // held.
    const reader = bodyOf('async function readCliVersion()')
    expect(reader).toContain('versionReadFrom(resolution)')
    expect(reader).not.toContain('.reason')
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

  it('stops the child the console started before the process quits', () => {
    // The one long-lived child this app has is the Live screen's `fugaro watch`,
    // started through the source bound to the active installation. Nothing else
    // stops it: the menu's Quit is Electron's own `role: 'quit'` and
    // `window-all-closed` only calls `app.quit()`, so without a `before-quit`
    // that closes the bound source the child outlives the app that started it --
    // which is the opposite of what `forget` protects (an installation the
    // console no longer shows leaves nothing running) and of why every spawn is
    // written to the spawn log. `CliSource.close()` is bounded (SIGTERM, then
    // SIGKILL after the shutdown grace), so the quit can wait for it.
    const quit = /app\.on\('before-quit'[\s\S]*?\n\}\)/.exec(indexSource)?.[0] ?? ''
    expect(quit, 'the entry point registers no before-quit handler').not.toBe('')
    // The order is the claim: hold the quit back, close what the console
    // started, then let it through. A handler that quit first would leave the
    // close racing the process exit.
    expect(quit).toContain('event.preventDefault()')
    expect(quit).toContain('app.quit()')
    // The call however it is laid out -- the close is chained onto its own
    // lines -- and in that order: held back, closed, then through.
    const closedAt = quit.search(/boundSource\s*\.\s*close\(\)/)
    expect(closedAt, 'the handler does not close the bound source').toBeGreaterThan(-1)
    expect(quit.indexOf('preventDefault')).toBeLessThan(closedAt)
    expect(closedAt).toBeLessThan(quit.indexOf('app.quit()'))
    // And it holds the quit back exactly once: the `app.quit()` above comes back
    // through this handler, so one that prevented every `before-quit` would quit
    // nothing at all.
    expect(quit).toMatch(/if \(closing\) return/)

    // The fatal path is the other way out of this process, and `app.exit` fires
    // no `before-quit`: a failed switch or `activate` would leave the same child
    // running. It closes the source too, and the exit code is unchanged.
    const fatal = bodyOf('function reportAndQuit(')
    expect(fatal).toMatch(/boundSource\s*\.\s*close\(\)/)
    expect(fatal).toContain('app.exit(1)')
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

/** bodyOf is one function's body, read out of the entry point's own text: the
 * assertions below are about what a function does, and a claim about the file as
 * a whole would pass on the right line in the wrong function. */
function bodyOf(declaration: string): string {
  const start = indexSource.indexOf(declaration)
  expect(start, `${declaration} is not in the entry point`).toBeGreaterThan(-1)
  const rest = indexSource.slice(start)
  const end = rest.indexOf('\n}')
  return end === -1 ? rest : rest.slice(0, end)
}

describe('the entry point puts the surface on ipcMain, and only when it may', () => {
  it('registers the whole allow-list, wrapped by the reduced-mode gate', () => {
    // The surface and the gate, in one line a reviewer can read: `gated` is the
    // twenty-two operations, `reducedModeHandlers` is what refuses the withheld
    // views' reads *before* the source is reached, and the second argument is
    // the stored answer read per call rather than a copy of it.
    expect(indexSource).toContain(
      'registerConsoleHandlers(ipcMain, reducedModeHandlers(gated, storedCompatibility))',
    )
    // One registration site, because `ipcMain.handle` throws on a second
    // listener for a channel it already has: a second call anywhere would be a
    // startup failure rather than a no-op.
    expect(indexSource.match(/registerConsoleHandlers\(/g) ?? []).toHaveLength(1)
  })

  it('registers once per process, guarded, because `activate` can run again', () => {
    const register = bodyOf('function registerHandlers()')
    // The guard is the first thing: the set happens after it, never before, so
    // there is no path on which a second call reaches `ipcMain`.
    expect(register).toMatch(/if \(registered\) return/)
    expect(register.indexOf('if (registered) return')).toBeLessThan(
      register.indexOf('registered = true'),
    )
    expect(register).toContain('registered = true')
  })

  it('registers for an `ok` or a `reduced` answer and for nothing else', () => {
    // Step 2, in the one function that decides it. The refused console is the
    // reason this is a *branch* rather than a call: a console below the floor
    // has no renderer entry to talk to, so a handler behind a channel there
    // would be a surface on a window that may draw nothing.
    const onDecided = bodyOf('function onDecided(')
    expect(onDecided).toContain('current = compatibility')
    expect(onDecided).toMatch(/if \(compatibility\.mode === 'refused'\) return/)
    expect(onDecided).toContain('registerHandlers()')
    // The refusal returns *before* the registration, so no ordering change can
    // register a refused console's surface by accident.
    expect(onDecided.indexOf("'refused'")).toBeLessThan(onDecided.indexOf('registerHandlers()'))
    // And `activate` does not come back through it: the decision was taken once,
    // from the answer the lifecycle stored.
    const activate = /app\.on\('activate'[\s\S]*?\n}\)/.exec(indexSource)?.[0] ?? ''
    expect(activate).toContain('lifecycle.activate()')
    expect(activate).not.toContain('onDecided')
    expect(activate).not.toContain('registerHandlers')
  })

  it('joins in the one handler `source.ts` leaves out, which is the half it names', () => {
    // `CliBackedHandlers` in `src/source/source.ts` is the surface *minus*
    // `startup.compatibility`, and its comment says the entry point joins that
    // key in -- which until this task was a promise about a file that did not do
    // it yet. It does it now, and this is the assertion that keeps the comment
    // true: `handlersFor(source, ...)` spread, and the handshake handler added by
    // name beside it.
    const gated = bodyOf('const gated: ConsoleHandlers = {')
    expect(gated).toContain('...handlersFor(')
    expect(gated).toContain("'startup.compatibility': startupCompatibility(")
    // The join is what makes the surface complete, so there is exactly one of
    // each half and they are in the same object.
    expect(indexSource.match(/handlersFor\(/g) ?? []).toHaveLength(1)
    expect(indexSource.match(/startupCompatibility\(/g) ?? []).toHaveLength(1)
    // And the argument is the *function* that reads the store, not the store:
    // the answer is asked for at message time (`startupCompatibility`'s `read`).
    expect(gated).toContain('startupCompatibility(storedCompatibility)')
  })

  it('hands the decision to the lifecycle, which is what runs it at the right moment', () => {
    // `createConsoleLifecycle` is what threads the callback into `startupWindow`
    // (tested above in 'the lifecycle'), so this is the entry point's half of
    // that: the callback is the named function, and it is passed as a dep.
    const lifecycle = bodyOf('const lifecycle = createConsoleLifecycle(')
    expect(lifecycle).toContain('onDecided,')
    expect(lifecycle).toContain('readVersion: readCliVersion')
    expect(lifecycle).toContain('windowDeps,')
  })

  it('serves the build, not the sources: one root, and the preload under the app path', () => {
    // The served root and the preload path are the two places this file names a
    // directory, and both are the *build's* output (Task 3.4a, step 4): the
    // documents and the bundled entry are copied or emitted into `dist/bundle/`,
    // and the preload `tsc` emits is `dist/preload/index.js`. Both are joined to
    // `app.getAppPath()`, so a packaged console reads its own files and not the
    // working directory's.
    const root = bodyOf('function rendererRoot()')
    expect(root).toContain("join(app.getAppPath(), 'dist', 'bundle')")
    const preload = bodyOf('function consolePreloadPath()')
    expect(preload).toContain("join(app.getAppPath(), 'dist', 'preload', 'index.js')")
    // And the sources are never named: a root pointing at `src/renderer` would
    // serve files that only exist in a checkout.
    expect(indexSource).not.toContain("'src'")
    expect(indexSource).not.toContain('src/renderer')
  })

  it('binds the surface to the active installation, and starts no child of its own', () => {
    // Task 3.4c's subject. Until it landed this file held a source that refused
    // every read; now the surface is `createBoundSource`, whose reads resolve
    // the installation the picker made active and build the one `CliSource` for
    // it. What is still true, and is what this test is for: **the entry point
    // starts no child itself**. It names no spawn, and the two binaries it
    // resolves are named below -- everything else goes through the source,
    // where `src/source/cli.ts` is the only place a process is created.
    expect(indexSource).toContain('createBoundSource(')
    // The installation is read at message time, from the picker, and the
    // binding is what turns it into the source's target: a file that read the
    // picker once and cached the answer would keep driving an installation the
    // operator has since replaced.
    expect(indexSource).toContain('installation: activeBinding')
    expect(indexSource).not.toContain('child_process')
    expect(indexSource).not.toContain('spawn(')
    // The two resolutions in the file, and both by name. V2.5.1a part 2a added
    // the second one on purpose: a launch resolves the CLI **the record names**
    // (`resolveLaunchBinary`, `src/main/installations.ts`) -- inside that
    // record's distribution, or at its own `binaryPath` -- instead of walking
    // the host's locations, which is the substitution the record exists to
    // prevent. Naming both keeps the guard this assertion was: a third site, or
    // an operation that resolved a binary for itself, is still a second answer
    // to "which fugaro is this console driving".
    expect([...(indexSource.match(/resolve\w*Binary\(/g) ?? [])].sort()).toEqual([
      'resolveHandshakeBinary(',
      'resolveLaunchBinary(',
    ])
    expect(indexSource).not.toContain('FUGARO_BIN')
    // The kill switch has no operation of its own (C10), so nothing here names
    // one: the field travels inside `live.watch`.
    expect(indexSource).not.toContain('budget.kill')
    // And no code is injected into the window. The screens are drawn by the one
    // bundle the document loads (design section 5.1 rule 1: `script-src 'self'`,
    // no inline script), and `executeJavaScript` is the other way a main process
    // draws -- one that would put the console's own text into the page outside
    // the document's policy and outside the preload's frozen surface. The e2e
    // reads the window for the same claim from the other side (one `<script>`,
    // and the Live screen's rows drawn by it).
    expect(indexSource).not.toContain('executeJavaScript')
  })

  it('answers the header from this run’s resolution, and keeps the record for the source', () => {
    // Review of this branch. `installation.current` was `picker.installation`
    // itself, so a chosen record with no `binaryPath` -- the shape this console
    // writes, because it never stores the path it resolves -- made the header
    // draw nothing while the console was driving a binary the store had never
    // heard of, and a legacy record whose override has gone made it name a path
    // it was not running. The join is `withResolvedBinary` (unit-tested in
    // `test/unit/installations-store.test.ts`); these are the two things only
    // this file can be wrong about: which state the header reads, and that both
    // writers of the resolution are wired to it.
    expect(indexSource).toContain('installation: () => installationState()')
    const state = bodyOf('function installationState()')
    expect(state).toContain('picker?.installation()')
    expect(state).toContain('resolvedBinaries.get(chosen.project)')
    expect(state).toContain('withResolvedBinary(')
    // Both moments a resolution happens, written to the one map: the startup
    // handshake and a choose.
    expect(bodyOf('async function readCliVersion()')).toContain(
      'resolvedBinaries.set(chosen.target.project, resolution.path)',
    )
    expect(indexSource).toContain('resolvedBinaries.set(chosen.project, chosen.resolvedBinaryPath)')
    // And the binding the source is built from carries the store's override and
    // **nothing else**. This is the other end of the same join, and it is the
    // one the map must not reach: `sourceFor` hands the binding's `binaryPath`
    // to the resolver as the *configured* path -- rule 4's first place -- so a
    // run's own resolution arriving here would make `configuredSource` report
    // `configured` for `/usr/local/bin/fugaro`, and would make
    // `InstallationRef.source` say `configured` for every record this console
    // wrote, which is the ordinary case and carries no override at all. The
    // binding is where the record's optional override is what rule 4 is asked
    // about; the source resolves the well-known locations itself, exactly as the
    // startup handshake does.
    const binding = bodyOf('function activeBinding()')
    expect(binding).toContain('binaryPath: chosen.binary')
    expect(
      binding,
      'the source binding reads the run’s resolution, so a well-known path is handed to the resolver as the operator’s own',
    ).not.toContain('resolvedBinaries')
    // The checkout travels too, and it is not decoration: it is the child's
    // working directory, and it is the one field the picker changes on a record
    // the console already holds (`setCheckout` preserves the id, deliberately).
    // `createBoundSource` caches its source by the binding -- id *and* checkout --
    // so a binding that dropped this field would leave the console spawning in
    // the directory the operator just left while the row and the header reported
    // the new one (`test/unit/bound-source.test.ts` is the other half of this).
    expect(binding).toContain('checkout: chosen.checkout')
  })

  it('hands the startup handshake to the picker under the installation’s project, never its id', () => {
    // The compatibility a handshake read is held **by project**: `remember` is
    // `registry.remember(project, …)` -> `compatibility.set(project, value)`, and
    // the readers are `compatibility.get(active.project)` (`current`,
    // `activeCompatibility`) and `compatibility.get(installation.project)`
    // (`rows`). An id is a different string, so remembering under it stores the
    // answer where nothing looks: the dashboard's header draws "version not read
    // · unknown" and the picker's active row says `unknown` on every launch,
    // while the handshake that just read the version is thrown away. The two
    // statements that make that a fact here are the assignment below -- which the
    // same function does correctly two lines earlier for `resolvedBinaries` --
    // and the `remember` call `onDecided` makes with it.
    expect(indexSource).toContain('handshakeProject = chosen === null ? null : chosen.target.project')
    expect(indexSource).not.toMatch(/handshakeProject\s*=\s*[^\n]*\.id/)
    expect(bodyOf('function onDecided(compatibility: CliCompatibility): void')).toContain(
      'picker?.remember(read, compatibility)',
    )
    // And the id is not carried at all: `resolveHandshakeBinary` takes the
    // override and the target, so a stored id would be a field nothing reads and
    // the next writer of this function would have to guess which one `remember`
    // wants (it wants the project).
    expect(bodyOf('function handshakeInstallation()')).not.toContain('chosen.id')
  })

  // The two Task 3.4b cases that stood here were greps of this file's text
  // (`'configuredPath: chosen.binaryPath'` and the like), and they passed with
  // the B1 fallthrough in place (review S4). Their behaviour is proven instead:
  // the resolution in 'the startup handshake reads the active installation, and
  // only it' above (`resolveHandshakeBinary`, which this file calls), and the
  // wiring in a real window -- `test/e2e/entry.spec.ts` (a seeded installation's
  // project in the spawn log, a missing stored binary refused, a planted
  // `FUGARO_PROJECT` never read, the picker opened once on a first run) and
  // `test/e2e/installations.spec.ts` (`installation.current` from the picker,
  // a refused choose leaving the window as it was).

  it('logs the handshake probe, and only the handshake probe', () => {
    // Design section 5.1 rule 10: the console writes the spawn log, when asked.
    // The probe is a spawn like any other, and it is logged with the same
    // mechanism the CLI source uses -- opened for this read and closed as soon
    // as it is done, so the log answers "which binary did the console read"
    // rather than accumulating every read for the process's lifetime.
    const read = bodyOf('async function readCliVersion()')
    expect(read).toContain('createSpawnLog(process.env)')
    expect(read).toMatch(/finally \{[\s\S]*log\.close\(\)/)
    expect(read).toContain('log,')
    // The log is the operator's opt-in and nothing else: no key this file
    // invents, and no mode that skips the read.
    expect(indexSource).not.toContain('FUGARO_CONSOLE_SPAWN_LOG')
  })
})
