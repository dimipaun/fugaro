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
import { CONSOLE_WINDOW_TITLE, openStartupWindow, openWindowFor } from '../../src/main/window'
import type { ConsoleWindowDeps, VersionRead } from '../../src/main/window'
import { CLI_VERSION_BUILT_AGAINST, SKEW_HEADLINES, classifyCliVersion } from '../../src/shared/version'

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
    expect(indexSource).toContain('await openStartupWindow(')
    expect(indexSource).toContain('readVersion: readCliVersion')
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
    expect(indexSource).not.toContain('child_process')
  })

  it('keeps the answer and opens the window it calls for on activate', () => {
    // `activate` is the only other place a window is opened, and it goes
    // through `openWindowFor` with the stored compatibility.
    expect(indexSource).toContain('openWindowFor(windowDeps(), startup)')
    expect(indexSource).not.toContain('startupWindow(')
    // The store is written by the startup and read by nothing but the two
    // window paths.
    const writes = indexSource.match(/startup = /g) ?? []
    expect(writes).toHaveLength(1)
  })

  it('runs the startup from main, after the bundle is served exactly once', () => {
    const main = /async function main\(\)[\s\S]*?\n}/.exec(indexSource)?.[0] ?? ''
    expect(main).toContain('serveBundle()')
    expect(main).toContain('await startConsole()')
    // `serveBundle` registers a protocol handler that throws on a second call,
    // so it belongs to `main` and not to the repeatable `activate` path. The
    // pattern is anchored to a call on a line of its own, so the function's own
    // declaration is not counted as one.
    expect(indexSource.match(/^\s*serveBundle\(\)$/gm) ?? []).toHaveLength(1)
  })
})
