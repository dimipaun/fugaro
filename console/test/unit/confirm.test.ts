// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The launch confirmation window's main-process module, its own preload and the
// sender gate (design section 5.1 rule 11; console V2.5.1a part 2b-1).
//
// The design's sentence is that the confirmation is a window the main process
// owns, and the property this file exists to hold is the one that makes it a
// confirmation at all: **only the confirmation window's own top frame can answer
// it**. A frame of the dashboard, a subframe of the confirmation document and
// anything else that reaches `confirm:answer` decide nothing and are answered
// with the fixed refusal -- the headline test below drives that through the real
// launcher, so "refused" is observable as "a child that was not started" rather
// than as a reply a test could have got wrong.
//
// The second property is the same one from the other side: **the window never
// receives the frozen launch.** `ConfirmView` is a deep-frozen, strings-and-
// numbers copy -- no `plan`, no `env`, no `cwd` and no `taskBytes`, which is a
// `Uint8Array` `Object.freeze` cannot reach into.

import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { describe, expect, it, vi } from 'vitest'

import type { Session } from 'electron'

import {
  CONFIRM_RATE_APP,
  CONFIRM_RATE_PER_INSTALLATION,
  CONFIRM_RATE_WINDOW_MS,
  DECLINED_CANCELLED,
  LAUNCH_COOL_DOWN_MS,
  MAX_IN_FLIGHT,
  confirmView,
  createLaunchRunner,
} from '../../src/main/launch'
import type {
  ConfirmView,
  FrozenLaunch,
  LaunchConfirmer,
  LaunchOutcome,
  LaunchRunner,
  LaunchTarget,
} from '../../src/main/launch'
import {
  CONFIRM_ALREADY_ANSWERED,
  CONFIRM_ANSWER_REFUSAL,
  CONFIRM_CHANNEL_PREFIX,
  CONFIRM_ENTRY_PATH,
  CONFIRM_ENTRY_URL,
  CONFIRM_NOT_YET,
  CONFIRM_READY_DELAY_MS,
  CONFIRM_WINDOW_TITLE,
  confirmChannel,
  confirmPreloadPath,
  createConfirmWindow,
} from '../../src/main/confirm'
import { SENDER_REFUSAL } from '../../src/main/ipc'
import type { ConsoleEventLike, IpcMainLike } from '../../src/main/ipc'
import type { ChildSpawner, SpawnOptionsLike, SpawnedChildLike } from '../../src/main/spawn'
import { displayTaskText } from '../../src/main/launchrules'
import { consoleWebPreferences } from '../../src/main/window'

const PROJECT = 'sandbox'
const OTHER_PROJECT = 'staging'
const RECORDED_REPOSITORY = 'github/owner/repo-a'
const REPOSITORY = 'owner/repo-a'
const BRANCH = 'main'
const BINARY = '/usr/local/bin/fugaro'
const CLI_VERSION = '0.11.2'
const TASK = 'summarise the alerting backlog'
const APP_PATH = '/opt/fugaro-console'
const NOW = Date.UTC(2026, 9, 28, 15, 26, 18)
const RUN_ID = '20261028-152618-a2fc'

// ---------------------------------------------------------------------------
// The fakes: the slices of Electron this module reads, under plain Node.
// ---------------------------------------------------------------------------

type Handler = (...args: unknown[]) => void

/** fakeContents is the slice of Electron's `WebContents` this module uses: its
 * `mainFrame`, the listeners `installWebContentsSecurity` and this module attach,
 * and a way for a test to fire one. */
function fakeContents(url: string) {
  const handlers = new Map<string, Handler[]>()
  const contents = {
    mainFrame: { url },
    on(name: string, handler: Handler): void {
      handlers.set(name, [...(handlers.get(name) ?? []), handler])
    },
    setWindowOpenHandler: (): void => undefined,
  }
  return {
    ...contents,
    fire(name: string, ...args: unknown[]): void {
      for (const handler of handlers.get(name) ?? []) handler(...args)
    },
  }
}

/** fakeConfirmWindow is one `BrowserWindow` this module builds. `close()` marks
 * the close as asked for and does **not** fire `closed`, because Electron does
 * not either: a test fires that itself, which is what keeps "a second answer
 * before the window is gone" and "a close decides no" two separate states.
 *
 * `destroy()` is Electron's own destruction, and it is what makes `webContents`
 * throw: the real getter does exactly that once the window is destroyed, and
 * `closed` fires only afterwards. A test that fires `closed` after `destroy()`
 * is the state in which a `closed` listener that read the getter again would
 * throw instead of deciding. */
function fakeConfirmWindow(options: Record<string, unknown> = {}) {
  const windowHandlers = new Map<string, Handler[]>()
  const contents = fakeContents(CONFIRM_ENTRY_URL)
  let closeRequested = false
  let destroyed = false
  let shown = 0
  const window = {
    options,
    get webContents() {
      if (destroyed) throw new Error('Object has been destroyed')
      return contents
    },
    loaded: [] as string[],
    destroy(): void {
      destroyed = true
    },
    isDestroyed: (): boolean => destroyed || closeRequested,
    closeRequested: (): boolean => closeRequested,
    shown: (): number => shown,
    loadURL: async (url: string): Promise<void> => {
      window.loaded.push(url)
    },
    on(name: string, handler: Handler): void {
      windowHandlers.set(name, [...(windowHandlers.get(name) ?? []), handler])
    },
    once(name: string, handler: Handler): void {
      windowHandlers.set(name, [...(windowHandlers.get(name) ?? []), handler])
    },
    show: (): void => {
      shown += 1
    },
    focus: (): void => undefined,
    close: (): void => {
      closeRequested = true
    },
    fire(name: string, ...args: unknown[]): void {
      for (const handler of windowHandlers.get(name) ?? []) handler(...args)
    },
  }
  return window
}

type FakeWindow = ReturnType<typeof fakeConfirmWindow>

/** fakeIpcMain records the channels `createConfirmWindow` registers, so a test
 * can call one with an event it forged. */
function fakeIpcMain(): IpcMainLike & {
  readonly listeners: Map<string, (event: ConsoleEventLike, ...args: unknown[]) => Promise<unknown>>
} {
  const listeners = new Map<string, (event: ConsoleEventLike, ...args: unknown[]) => Promise<unknown>>()
  return {
    listeners,
    handle(channel, listener) {
      listeners.set(channel, listener as (event: ConsoleEventLike, ...args: unknown[]) => Promise<unknown>)
    },
  }
}

type FakeSession = {
  readonly value: Session
  readonly requestHandler: () => RequestHandler | null
  readonly checkHandler: () => (() => boolean) | null
  readonly installed: { request: number; check: number }
}

type RequestHandler = (
  contents: unknown,
  permission: string,
  callback: (granted: boolean) => void,
) => void

/** fakeSession records both of the handlers `denyEveryPermission` installs, and
 * how many times each was installed: the confirmation window denies every
 * permission on its **own** session rather than relying on the handlers the
 * other windows put on `session.defaultSession`, so a test has to be able to say
 * which session was denied and that it was denied once. */
function fakeSession(): FakeSession {
  let request: RequestHandler | null = null
  let check: (() => boolean) | null = null
  const installed = { request: 0, check: 0 }
  const session = {
    setPermissionRequestHandler(handler: RequestHandler): void {
      request = handler
      installed.request += 1
    },
    setPermissionCheckHandler(handler: () => boolean): void {
      check = handler
      installed.check += 1
    },
  }
  return {
    value: session as unknown as Session,
    requestHandler: () => request,
    checkHandler: () => check,
    installed,
  }
}

/** confirmOver builds the confirmer over fakes, and keeps the windows and the
 * session it built them with.
 *
 * `now` is the clock the window's own delay is measured on, and it is a test's
 * rather than the process's for the same reason the rate limits' is
 * (`runnerOver`): the delay is time a case moves rather than waits. Passing
 * `null` is the one case that asks for the **shipped default** instead, which is
 * what `reads the delay off a clock that only moves forward` drives: a helper
 * that always supplied a clock would keep every case green through a default
 * reverted to `Date.now`. */
function confirmOver(
  options: { readonly parent?: () => FakeWindow | null; readonly now?: (() => number) | null } = {},
): {
  readonly confirmer: LaunchConfirmer
  readonly ipcMain: ReturnType<typeof fakeIpcMain>
  readonly windows: FakeWindow[]
  readonly session: FakeSession
} {
  const ipcMain = fakeIpcMain()
  const windows: FakeWindow[] = []
  const session = fakeSession()
  const BrowserWindow = function BrowserWindow(this: unknown, opts: Record<string, unknown>) {
    const window = fakeConfirmWindow(opts)
    windows.push(window)
    return window
  } as unknown as new (options: unknown) => never
  const confirmer = createConfirmWindow({
    BrowserWindow: BrowserWindow as never,
    ipcMain,
    parent: (options.parent ?? (() => null)) as never,
    appPath: APP_PATH,
    entryUrl: CONFIRM_ENTRY_URL,
    session: session.value,
    ...(options.now === null ? {} : { now: options.now ?? ((): number => NOW) }),
  })
  return { confirmer, ipcMain, windows, session }
}

/** aClock is a clock a case moves by hand: `CONFIRM_READY_DELAY_MS` is measured
 * from the window's own `show` and `focus`, so a case advances time rather than
 * waiting it out. */
function aClock(start: number = NOW): { readonly now: () => number; readonly advance: (ms: number) => void } {
  let at = start
  return {
    now: () => at,
    advance: (ms: number): void => {
      at += ms
    },
  }
}

/** appears runs what Electron runs when the window comes up: the `ready-to-show`
 * handler calls `window.show()`, which emits `show`, and the window then emits
 * `focus`. The delay is measured from those two events, so a case that answers a
 * yes without this is answering a window that never appeared. */
function appears(window: FakeWindow): void {
  window.fire('ready-to-show')
  window.fire('show')
  window.fire('focus')
}

/** ready leaves the window as the delay leaves it: on screen, and past the delay.
 * A case whose subject is not the delay drives this before its yes, so that the
 * yes is an answer; the delay's own cases (`describe('the delay before a yes is
 * an answer')`) drive `appears` and the clock themselves. */
function ready(window: FakeWindow, clock: { readonly advance: (ms: number) => void }): void {
  appears(window)
  clock.advance(CONFIRM_READY_DELAY_MS)
}

/** ask delivers one message over one channel with a forged event: the whole of
 * what reaches the gate from a renderer. */
async function ask(
  ipcMain: ReturnType<typeof fakeIpcMain>,
  channel: string,
  event: ConsoleEventLike,
  ...args: unknown[]
): Promise<unknown> {
  const listener = ipcMain.listeners.get(channel)
  if (listener === undefined) throw new Error(`${channel} was not registered`)
  return await listener(event, ...args)
}

/** fromWindow is the event a message from a window's own top frame carries. */
function fromWindow(window: FakeWindow): ConsoleEventLike {
  return { sender: window.webContents, senderFrame: window.webContents.mainFrame }
}

/** recordingDecider is the `decide` a test hands `open`, with every call kept. */
function recordingDecider(): {
  readonly calls: readonly (readonly [boolean, string | undefined])[]
  readonly decide: (yes: boolean, reason?: string) => void
} {
  const calls: (readonly [boolean, string | undefined])[] = []
  return {
    calls,
    decide: (yes: boolean, reason?: string) => {
      calls.push([yes, reason])
    },
  }
}

/** keyEvent is the `(event, input)` pair `before-input-event` carries. */
function keyEvent(): { prevented: boolean; preventDefault(): void } {
  return {
    prevented: false,
    preventDefault(): void {
      this.prevented = true
    },
  }
}

// ---------------------------------------------------------------------------
// The launcher, for the one property that can only be observed as a non-spawn.
// ---------------------------------------------------------------------------

/** runnerOver builds the real launcher over this test's own records and one
 * spawner, so a refused answer's consequence is a child that was not started.
 *
 * The records answer any name with a target of that name -- the console's own
 * shape, and what the limit cases need to reach a second installation -- and the
 * limits' clock is `NOW` unless a case says otherwise, since the rate windows
 * and the cool-down are only reachable by moving it. */
function runnerOver(
  confirmer: LaunchConfirmer,
  spawner: ChildSpawner,
  options: { readonly now?: () => number } = {},
): LaunchRunner {
  const target: LaunchTarget = {
    project: PROJECT,
    binaryPath: BINARY,
    cliVersion: CLI_VERSION,
    records: { baseBranch: BRANCH, repositories: [{ repository: RECORDED_REPOSITORY, baseBranch: '' }] },
    fingerprint: '["record-1","sandbox","/usr/local/bin/fugaro","","",""]',
  }
  return createLaunchRunner({
    confirmer,
    lookUp: (project: string) =>
      Promise.resolve(
        project === ''
          ? { ok: false as const, reason: 'no installation named' }
          : { ok: true as const, target: project === PROJECT ? target : { ...target, project } },
      ),
    stillRecorded: () => true,
    appEnv: { HOME: '/home/person-1' },
    spawner,
    timers: {
      setTimeout: () => 0 as unknown as ReturnType<typeof setTimeout>,
      clearTimeout: () => undefined,
    },
    now: options.now ?? (() => NOW),
    wallClock: () => NOW,
    random: countingRandom(),
  })
}

/** countingRandom is the id's four hex digits, one draw per attempt: the first
 * is `0xa2fc`, so a case's first launch is `RUN_ID`, and every later launch of
 * that runner gets an id of its own. A source that answered one value for ever
 * would make the launcher refuse every launch after the first as an id it
 * already holds, which is a different property (`launch.test.ts` holds it). */
function countingRandom(): () => number {
  let tail = 0xa2fc
  return () => {
    const value = tail
    tail += 1
    return value / 0x10000
  }
}

/** countingSpawner counts the children started and answers none of them: the
 * claim here is about how many were started, not about how one ended. */
function countingSpawner(): ChildSpawner & { readonly calls: readonly (readonly [string, readonly string[]])[] } {
  const calls: (readonly [string, readonly string[]])[] = []
  const quietChild: SpawnedChildLike = {
    stdout: { on: () => undefined },
    stderr: { on: () => undefined },
    stdin: { write: () => undefined, end: () => undefined, on: () => undefined },
    on: () => undefined,
    kill: () => true,
  }
  const spawner = ((command: string, args: readonly string[], _options: SpawnOptionsLike) => {
    calls.push([command, args])
    return quietChild
  }) as unknown as ChildSpawner
  Object.defineProperty(spawner, 'calls', { get: () => calls })
  return spawner as ChildSpawner & { readonly calls: readonly (readonly [string, readonly string[]])[] }
}

/** launchReply is `fugaro run --json`'s document for one call's own argv: the
 * run id and the repository are read back off the words the console emitted, so
 * a reply is about this launch by construction. */
function launchReply(argv: readonly string[]): string {
  const flag = (name: string): string => {
    const at = argv.indexOf(name)
    return at === -1 ? '' : (argv[at + 1] ?? '')
  }
  return JSON.stringify({
    project: PROJECT,
    run: 'run-1',
    repo: flag('--repo'),
    run_id: flag('--run-id'),
    branch: BRANCH,
    ref: 'refs/heads/main',
    execution: 'execution-1',
    log_url: 'https://example.invalid/run-1',
    status: 'launched',
  })
}

/** replyingSpawner starts a child that answers at once and closes, so a launch
 * settles: the cool-down an **accepted** launch leaves on its installation is
 * only reachable once the launch before it is done. */
function replyingSpawner(): ChildSpawner & { readonly calls: readonly (readonly [string, readonly string[]])[] } {
  const calls: (readonly [string, readonly string[]])[] = []
  const spawner = ((command: string, args: readonly string[], _options: SpawnOptionsLike) => {
    calls.push([command, args])
    const stdout: ((chunk: string) => void)[] = []
    const closes: ((code: number | null, signal: string | null) => void)[] = []
    const child = {
      stdout: {
        on: (_event: string, listener: (chunk: string) => void): void => {
          stdout.push(listener)
        },
      },
      stderr: { on: () => undefined },
      stdin: { write: () => undefined, end: () => undefined, on: () => undefined },
      on: (event: string, listener: (code: number | null, signal: string | null) => void): undefined => {
        if (event === 'close') closes.push(listener)
        return undefined
      },
      kill: () => true,
    }
    const reply = launchReply(args)
    queueMicrotask(() => {
      for (const listener of stdout) listener(reply)
      for (const listener of closes) listener(0, null)
    })
    return child
  }) as unknown as ChildSpawner
  Object.defineProperty(spawner, 'calls', { get: () => calls })
  return spawner as ChildSpawner & { readonly calls: readonly (readonly [string, readonly string[]])[] }
}

/** untilSettled waits out one launch's microtasks: the records read, the window
 * and the answer. A fixed number of turns rather than a poll, as
 * `launch.test.ts` does, so the assertions are about order. */
async function untilSettled(): Promise<void> {
  for (let turn = 0; turn < 12; turn += 1) await Promise.resolve()
}

/** aLaunch runs one launch of this test's own request through `runner`. */
function aLaunch(runner: LaunchRunner): Promise<LaunchOutcome> {
  return runner.launch({ installation: PROJECT, repository: RECORDED_REPOSITORY, task: TASK })
}

/** aLaunchIn runs one launch of `project` through `runner`. */
function aLaunchIn(runner: LaunchRunner, project: string): Promise<LaunchOutcome> {
  return runner.launch({ installation: project, repository: RECORDED_REPOSITORY, task: TASK })
}

describe('the confirmation window’s sender gate', () => {
  it('refuses an answer from the dashboard, and one from a subframe, and spawns nothing', async () => {
    // The headline. `answer(true)` arrives from two senders that are *not* this
    // window's own top frame -- the dashboard's `webContents`, and a subframe of
    // the confirmation window whose `senderFrame` is not what `mainFrame`
    // answers -- and each is refused, decides nothing, and leaves the launcher
    // with no child started. Drop the `mainFrame` identity check and the second
    // one is accepted: the test fails on `spawner.calls` (one child where zero
    // is required).
    const { confirmer, ipcMain, windows } = confirmOver()
    const spawner = countingSpawner()
    const runner = runnerOver(confirmer, spawner)
    const launched = aLaunch(runner)
    await untilSettled()

    const window = windows[0] as FakeWindow
    expect(window).toBeDefined()

    // (1) The dashboard's own `webContents`. Its `mainFrame` *is* the frame that
    // sent the message, so a gate that compared frames alone would admit it --
    // what refuses it is that this `webContents` is not one this module opened.
    const dashboard = fakeContents(CONFIRM_ENTRY_URL)
    expect(
      await ask(ipcMain, confirmChannel('answer'), { sender: dashboard, senderFrame: dashboard.mainFrame }, true),
    ).toStrictEqual({ ok: false, reason: SENDER_REFUSAL })

    // (2) A subframe of the confirmation window: the right `sender`, a frame that
    // is not its `mainFrame`.
    const subframe = { url: CONFIRM_ENTRY_URL }
    expect(
      await ask(ipcMain, confirmChannel('answer'), { sender: window.webContents, senderFrame: subframe }, true),
    ).toStrictEqual({ ok: false, reason: SENDER_REFUSAL })

    expect(spawner.calls).toStrictEqual([])

    // The window is still waiting for a real answer, and the operator closing it
    // is the cancel: no child, and the console's own sentence for it.
    window.fire('closed')
    const outcome = await launched
    expect((outcome as { readonly ok: boolean }).ok).toBe(false)
    expect((outcome as { readonly reason: string }).reason).toBe(DECLINED_CANCELLED)
    expect(spawner.calls).toStrictEqual([])
  })

  it('decides yes once for the right sender, and a second answer decides nothing more', async () => {
    const clock = aClock()
    const { confirmer, ipcMain, windows } = confirmOver({ now: clock.now })
    const spawner = countingSpawner()
    const runner = runnerOver(confirmer, spawner)
    void aLaunch(runner)
    await untilSettled()

    const window = windows[0] as FakeWindow
    ready(window, clock)
    expect(await ask(ipcMain, confirmChannel('answer'), fromWindow(window), true)).toStrictEqual({
      ok: true,
      data: { answered: true },
    })
    expect(window.closeRequested()).toBe(true)
    await untilSettled()
    expect(spawner.calls).toHaveLength(1)

    // A second answer from the same window, before its `closed` arrives: refused
    // by name, and no second child.
    expect(await ask(ipcMain, confirmChannel('answer'), fromWindow(window), true)).toStrictEqual({
      ok: false,
      reason: CONFIRM_ALREADY_ANSWERED,
    })
    await untilSettled()
    expect(spawner.calls).toHaveLength(1)

    // And the `closed` that follows a decision is not an answer either: the
    // launch's own child is the one this window started, and it is still the only
    // one. (`spawner.calls` is the claim; the child itself never answers here.)
    window.fire('closed')
    await untilSettled()
    expect(spawner.calls).toHaveLength(1)
    window.fire('closed')
    await untilSettled()
    expect(spawner.calls).toHaveLength(1)
  })

  it('refuses an answer that is not a boolean, and decides nothing', async () => {
    const { confirmer, ipcMain, windows } = confirmOver()
    const decider = recordingDecider()
    confirmer.open(viewFixture(), decider.decide)

    const window = windows[0] as FakeWindow
    for (const raw of ['yes', 1, null, undefined, {}]) {
      expect(await ask(ipcMain, confirmChannel('answer'), fromWindow(window), raw)).toStrictEqual({
        ok: false,
        reason: CONFIRM_ANSWER_REFUSAL,
      })
    }
    expect(decider.calls).toStrictEqual([])
    expect(window.closeRequested()).toBe(false)
  })

  it('answers fields() only to the window that asked, and only with its own view', async () => {
    const { confirmer, ipcMain, windows } = confirmOver()
    const first = viewFixture({ runId: '20261028-152618-0001' })
    const second = viewFixture({ runId: '20261028-152618-0002' })
    confirmer.open(first, recordingDecider().decide)
    confirmer.open(second, recordingDecider().decide)

    const windowA = windows[0] as FakeWindow
    const windowB = windows[1] as FakeWindow

    // Two windows open at once, each reading only its own view.
    expect(await ask(ipcMain, confirmChannel('fields'), fromWindow(windowA))).toStrictEqual({ ok: true, data: first })
    expect(await ask(ipcMain, confirmChannel('fields'), fromWindow(windowB))).toStrictEqual({ ok: true, data: second })

    // A `webContents` this module never opened reads nothing at all.
    const stranger = fakeContents(CONFIRM_ENTRY_URL)
    expect(
      await ask(ipcMain, confirmChannel('fields'), { sender: stranger, senderFrame: stranger.mainFrame }),
    ).toStrictEqual({ ok: false, reason: SENDER_REFUSAL })
  })

  it('refuses a message with no sender, no frame, or a frame whose getter throws', async () => {
    const { ipcMain } = confirmOver()
    expect(await ask(ipcMain, confirmChannel('fields'), {})).toStrictEqual({ ok: false, reason: SENDER_REFUSAL })
    expect(
      await ask(ipcMain, confirmChannel('fields'), {
        sender: fakeContents(CONFIRM_ENTRY_URL),
        senderFrame: null,
      }),
    ).toStrictEqual({ ok: false, reason: SENDER_REFUSAL })

    // Electron's destroyed-frame getters *throw*; a gate that let that escape
    // would answer a rejected invoke rather than a refusal.
    const exploding = {
      get senderFrame(): never {
        throw new Error('Render frame was disposed')
      },
    }
    expect(await ask(ipcMain, confirmChannel('fields'), exploding as ConsoleEventLike)).toStrictEqual({
      ok: false,
      reason: SENDER_REFUSAL,
    })
  })
})

describe('the confirmation window, and how it is cancelled', () => {
  it('is a modal window over the hardened preferences and the confirm preload, and nothing else', async () => {
    const parent = fakeConfirmWindow()
    const { confirmer, windows, session } = confirmOver({ parent: () => parent })
    confirmer.open(viewFixture(), recordingDecider().decide)
    await untilSettled()

    const window = windows[0] as FakeWindow
    expect(window.options['modal']).toBe(true)
    expect(window.options['parent']).toBe(parent)
    expect(window.options['show']).toBe(false)
    expect(window.options['title']).toBe(CONFIRM_WINDOW_TITLE)
    // The window is built on the very session the denial was installed on: the
    // two names have to be the same object, or a caller that passed a non-default
    // session would deny a session this window never uses.
    expect((window.options['webPreferences'] as { session?: unknown }).session).toBe(session.value)
    expect(window.options['webPreferences']).toStrictEqual({
      ...consoleWebPreferences(),
      preload: confirmPreloadPath(APP_PATH),
      session: session.value,
    })
    // One load, of the confirm document, and shown only when it says it is ready.
    expect(window.loaded).toStrictEqual([CONFIRM_ENTRY_URL])
    expect(window.shown()).toBe(0)
    window.fire('ready-to-show')
    expect(window.shown()).toBe(1)
  })

  it('leaves the parent off when there is no dashboard window, and is still modal', async () => {
    const { confirmer, windows } = confirmOver()
    confirmer.open(viewFixture(), recordingDecider().decide)
    await untilSettled()

    const window = windows[0] as FakeWindow
    expect(Object.prototype.hasOwnProperty.call(window.options, 'parent')).toBe(false)
    expect(window.options['modal']).toBe(true)
  })

  it('denies every permission on its own session, installed once at creation', async () => {
    // Defence in depth: the window must not rely on the request and check
    // handlers the dashboard installs on `session.defaultSession`. The denial and
    // the window are the **same** session (`confirmOptions` sets it), so a caller
    // that passes a non-default session denies the session its window uses. The
    // handlers are put in place when the confirmer is built -- not per `open` --
    // so a second window reuses the ones the first was covered by.
    const { confirmer, windows, session } = confirmOver()
    expect(session.installed).toStrictEqual({ request: 1, check: 1 })

    const request = session.requestHandler()
    expect(request, 'the session has no permission request handler').not.toBeNull()
    let granted: boolean | null = null
    request?.(null, 'media', (answer) => {
      granted = answer
    })
    expect(granted).toBe(false)
    expect(session.checkHandler()?.()).toBe(false)

    confirmer.open(viewFixture(), recordingDecider().decide)
    confirmer.open(viewFixture(), recordingDecider().decide)
    await untilSettled()
    expect(windows).toHaveLength(2)
    // Every window is built on the session whose permissions were denied.
    for (const window of windows) {
      expect((window.options['webPreferences'] as { session?: unknown }).session).toBe(session.value)
    }
    // Still the two handlers from creation: `open` installs nothing of its own.
    expect(session.installed).toStrictEqual({ request: 1, check: 1 })
    expect(session.requestHandler()).toBe(request)
  })

  it('is never built through dialog, which cannot show a 16 KiB task scrollably', () => {
    // The design names the window rather than a message box, and the mutation
    // this catches is a later edit reaching for the easy one. What is read is the
    // module's *code*: its own comments say why the box is not used.
    const source = readFileSync(fileURLToPath(new URL('../../src/main/confirm.ts', import.meta.url)), 'utf8')
    const electronImports = source.match(/import[^']*'electron'/g) ?? []
    expect(electronImports).toHaveLength(1)
    expect(electronImports.join('\n')).not.toContain('dialog')
    // And nothing in the code -- as opposed to the comments that say why the box
    // is not used -- names it at all.
    const code = source
      .split('\n')
      .filter((line) => !line.trimStart().startsWith('//'))
      .join('\n')
    expect(code).not.toContain('dialog')
  })

  it('decides no, and prevents the key, on an Enter or Escape key-down', () => {
    const { confirmer, windows } = confirmOver()
    const decider = recordingDecider()
    confirmer.open(viewFixture(), decider.decide)
    const window = windows[0] as FakeWindow

    for (const key of ['Enter', 'Escape']) {
      const event = keyEvent()
      window.webContents.fire('before-input-event', event, { type: 'keyDown', key, modifiers: ['shift'] })
      expect(event.prevented, `${key} was not prevented`).toBe(true)
    }
    // Both keys are the window's own way out and both are prevented -- and the
    // first one decides: a window that has already been cancelled does not decide
    // a second time, whatever is pressed on it afterwards. Cancel takes no
    // sentence of its own; the launcher's cancel sentence is what is shown.
    expect(decider.calls).toStrictEqual([[false, undefined]])
    expect(window.closeRequested()).toBe(true)
  })

  it('ignores a key that is not a cancel, and any key-up', () => {
    const { confirmer, windows } = confirmOver()
    const decider = recordingDecider()
    confirmer.open(viewFixture(), decider.decide)
    const window = windows[0] as FakeWindow

    const event = keyEvent()
    window.webContents.fire('before-input-event', event, { type: 'keyDown', key: 'a' })
    window.webContents.fire('before-input-event', event, { type: 'keyUp', key: 'Enter' })
    window.webContents.fire('before-input-event', event, { type: 'keyUp', key: 'Escape' })
    expect(event.prevented).toBe(false)
    expect(decider.calls).toStrictEqual([])
    expect(window.closeRequested()).toBe(false)
  })

  it('decides no exactly once when the window is closed', () => {
    const { confirmer, windows } = confirmOver()
    const decider = recordingDecider()
    confirmer.open(viewFixture(), decider.decide)
    const window = windows[0] as FakeWindow

    window.fire('closed')
    // A second `closed` decides nothing: the run id's answer is spent.
    window.fire('closed')
    expect(decider.calls).toStrictEqual([[false, undefined]])
  })

  it('does not decide again when a decided window is closed', async () => {
    const clock = aClock()
    const { confirmer, ipcMain, windows } = confirmOver({ now: clock.now })
    const decider = recordingDecider()
    confirmer.open(viewFixture(), decider.decide)
    const window = windows[0] as FakeWindow
    ready(window, clock)

    await ask(ipcMain, confirmChannel('answer'), fromWindow(window), true)
    window.fire('closed')
    expect(decider.calls).toStrictEqual([[true, undefined]])
  })

  it('decides no when the window is closed and destroyed, without reading a getter that throws', async () => {
    const { confirmer, ipcMain, windows } = confirmOver()
    const decider = recordingDecider()
    confirmer.open(viewFixture(), decider.decide)
    const window = windows[0] as FakeWindow
    // The `webContents` the module keyed its entry by, read while the window is
    // alive -- which is the only way to name it once the window is destroyed.
    const contents = window.webContents

    // Electron's own order, and the reason the entry keeps its `webContents`:
    // the window is destroyed first, `closed` fires afterwards, and by then
    // `window.webContents` throws. A `closed` listener that read the getter again
    // would throw here -- before it deleted the entry or decided -- and the
    // launch would never settle: no child, and the run id the console's own
    // in-flight maps hold for the life of the process.
    window.destroy()
    expect(() => window.webContents).toThrow('Object has been destroyed')
    expect(() => {
      window.fire('closed')
    }).not.toThrow()
    expect(decider.calls).toStrictEqual([[false, undefined]])

    // And the entry is gone from the map rather than merely answered: the
    // destroyed window's own `webContents` reaches neither channel.
    expect(
      await ask(ipcMain, confirmChannel('fields'), { sender: contents, senderFrame: contents.mainFrame }),
    ).toStrictEqual({ ok: false, reason: SENDER_REFUSAL })
  })

  it('closes an open window as a cancel when the run is named, and decides no once', () => {
    const { confirmer, windows } = confirmOver()
    const decider = recordingDecider()
    confirmer.open(viewFixture(), decider.decide)
    const window = windows[0] as FakeWindow

    confirmer.close(RUN_ID)
    expect(decider.calls).toStrictEqual([[false, undefined]])
    expect(window.closeRequested()).toBe(true)

    // The close the decision was, and the second close of the same run: neither
    // decides again.
    confirmer.close(RUN_ID)
    window.fire('closed')
    expect(decider.calls).toStrictEqual([[false, undefined]])
  })

  it('decides nothing when the named run has no window open', async () => {
    const clock = aClock()
    const { confirmer, ipcMain, windows } = confirmOver({ now: clock.now })
    const decider = recordingDecider()
    confirmer.open(viewFixture(), decider.decide)
    const window = windows[0] as FakeWindow
    ready(window, clock)

    // A run id nothing is open for -- the answer already arrived, or the window
    // never opened. Rule 11's close is not a way to decide a spent run id.
    confirmer.close('20261028-152618-b7d1')
    expect(decider.calls).toStrictEqual([])
    expect(window.closeRequested()).toBe(false)

    // Nor is it a second decision once the operator has answered.
    await ask(ipcMain, confirmChannel('answer'), fromWindow(window), true)
    confirmer.close(RUN_ID)
    expect(decider.calls).toStrictEqual([[true, undefined]])
  })

  it('decides no when the served root answers the document with a 404', async () => {
    const { confirmer, windows } = confirmOver()
    const decider = recordingDecider()
    confirmer.open(viewFixture(), decider.decide)
    const window = windows[0] as FakeWindow

    // What `serveAppRequest` returns for a path the bundle does not hold: a 404
    // `textResponse` with a body, which `loadURL` **resolves** -- so the window
    // would show `no such file in the bundle` and stay open for ever if the
    // navigation's status were not read.
    window.webContents.fire('did-navigate', {}, CONFIRM_ENTRY_URL, 404, 'Not Found')
    expect(decider.calls).toStrictEqual([[false, undefined]])
    expect(window.closeRequested()).toBe(true)

    // And a response code Electron could not read is not a refusal: `-1` is what
    // it reports when there is no code to report, and a document that loaded must
    // not be cancelled by a check that could not tell.
    const other = confirmOver()
    const kept = recordingDecider()
    other.confirmer.open(viewFixture(), kept.decide)
    const live = other.windows[0] as FakeWindow
    live.webContents.fire('did-navigate', {}, CONFIRM_ENTRY_URL, -1, '')
    live.webContents.fire('did-navigate', {}, CONFIRM_ENTRY_URL, 200, 'OK')
    expect(kept.calls).toStrictEqual([])
    expect(live.closeRequested()).toBe(false)

    // A 404 after a decision is not a second one.
    window.fire('closed')
    expect(decider.calls).toStrictEqual([[false, undefined]])
  })

  it('decides nothing on a response code it could not read, and the close declines with no child started', async () => {
    // The same fail-closed pin as the 404 above, but observed at the only place
    // that matters: the child that was **not** started. A `-1` -- a response code
    // Electron could not read -- decides nothing by itself, so the window is
    // still open; it is the close (or Escape) that declines, and a declined
    // launch starts no child.
    const closed = confirmOver()
    const closedSpawner = countingSpawner()
    void aLaunch(runnerOver(closed.confirmer, closedSpawner))
    await untilSettled()
    const closedWindow = closed.windows[0] as FakeWindow

    closedWindow.webContents.fire('did-navigate', {}, CONFIRM_ENTRY_URL, -1, '')
    expect(closedWindow.closeRequested()).toBe(false)
    closedWindow.fire('closed')
    await untilSettled()
    expect(closedSpawner.calls).toStrictEqual([])

    const escaped = confirmOver()
    const escapedSpawner = countingSpawner()
    void aLaunch(runnerOver(escaped.confirmer, escapedSpawner))
    await untilSettled()
    const escapedWindow = escaped.windows[0] as FakeWindow

    escapedWindow.webContents.fire('did-navigate', {}, CONFIRM_ENTRY_URL, -1, '')
    const event = keyEvent()
    escapedWindow.webContents.fire('before-input-event', event, { type: 'keyDown', key: 'Escape' })
    expect(event.prevented).toBe(true)
    await untilSettled()
    expect(escapedSpawner.calls).toStrictEqual([])
  })

  it('does not show a window that was already decided', () => {
    const { confirmer, windows } = confirmOver()
    const decider = recordingDecider()
    confirmer.open(viewFixture(), decider.decide)
    const window = windows[0] as FakeWindow

    // The 404 can arrive before the first paint: `show` is on `ready-to-show`,
    // which is later, and a closed window must not be shown by it.
    window.webContents.fire('did-navigate', {}, CONFIRM_ENTRY_URL, 404, 'Not Found')
    window.fire('ready-to-show')
    expect(window.shown()).toBe(0)
  })

  it('drops the window’s view once it is gone', async () => {
    const { confirmer, ipcMain, windows } = confirmOver()
    confirmer.open(viewFixture(), recordingDecider().decide)
    const window = windows[0] as FakeWindow
    window.fire('closed')
    expect(await ask(ipcMain, confirmChannel('fields'), fromWindow(window))).toStrictEqual({
      ok: false,
      reason: SENDER_REFUSAL,
    })
  })
})

describe('the delay before a yes is an answer', () => {
  it('refuses a yes the moment the window appears, and spawns nothing', async () => {
    // The bug this holds shut: the window is built after the records are read,
    // so it can appear under the pointer a few hundred milliseconds after the
    // click that asked for it -- and a double-click in the dashboard lands its
    // second click on the confirming button of a window nobody has seen yet.
    // The measure is the main process's own, taken from the window's `show` and
    // `focus`, because a timer in the page is a timer started by the document
    // whose late arrival is the whole problem.
    const clock = aClock()
    const { confirmer, ipcMain, windows } = confirmOver({ now: clock.now })
    const spawner = countingSpawner()
    void aLaunch(runnerOver(confirmer, spawner))
    await untilSettled()
    const window = windows[0] as FakeWindow

    // Not yet: a window that has not appeared has no answer to give.
    expect(await ask(ipcMain, confirmChannel('answer'), fromWindow(window), true)).toStrictEqual({
      ok: false,
      reason: CONFIRM_NOT_YET,
    })

    appears(window)
    // And the click already on its way lands the instant the window does.
    expect(await ask(ipcMain, confirmChannel('answer'), fromWindow(window), true)).toStrictEqual({
      ok: false,
      reason: CONFIRM_NOT_YET,
    })
    await untilSettled()
    expect(spawner.calls).toStrictEqual([])
    // A refusal, and not a decision: the window is still open on its frozen
    // launch, and the click that comes after it is the one that counts.
    expect(window.closeRequested()).toBe(false)
    expect(window.shown()).toBe(1)

    clock.advance(CONFIRM_READY_DELAY_MS)
    expect(await ask(ipcMain, confirmChannel('answer'), fromWindow(window), true)).toStrictEqual({
      ok: true,
      data: { answered: true },
    })
    await untilSettled()
    expect(spawner.calls).toHaveLength(1)
  })

  it('restarts the delay when the window is focused again', async () => {
    // A window the operator comes back to is a window the operator is looking
    // at again: the delay runs from the last time it was shown or focused, not
    // from the first.
    const clock = aClock()
    const { confirmer, ipcMain, windows } = confirmOver({ now: clock.now })
    const spawner = countingSpawner()
    void aLaunch(runnerOver(confirmer, spawner))
    await untilSettled()
    const window = windows[0] as FakeWindow
    appears(window)

    clock.advance(CONFIRM_READY_DELAY_MS - 1)
    expect(await ask(ipcMain, confirmChannel('answer'), fromWindow(window), true)).toStrictEqual({
      ok: false,
      reason: CONFIRM_NOT_YET,
    })

    window.fire('focus')
    clock.advance(CONFIRM_READY_DELAY_MS - 1)
    expect(await ask(ipcMain, confirmChannel('answer'), fromWindow(window), true)).toStrictEqual({
      ok: false,
      reason: CONFIRM_NOT_YET,
    })
    expect(spawner.calls).toStrictEqual([])

    clock.advance(1)
    expect(await ask(ipcMain, confirmChannel('answer'), fromWindow(window), true)).toStrictEqual({
      ok: true,
      data: { answered: true },
    })
    await untilSettled()
    expect(spawner.calls).toHaveLength(1)
  })

  it('makes a window that is never focused answerable anyway, since focus only extends the delay', async () => {
    // The delay runs from `show` **or** `focus`, whichever came last: the window
    // is answerable once it has been on screen for the delay, and a focus is not
    // required to answer it. Requiring one would make a window Electron never
    // focuses -- a platform behaviour this suite asserts rather than observes --
    // one that no answer can ever reach, where every launch it was opened for is
    // refused. What a focus does is push the delay further out.
    const clock = aClock()
    const { confirmer, ipcMain, windows } = confirmOver({ now: clock.now })
    const spawner = countingSpawner()
    void aLaunch(runnerOver(confirmer, spawner))
    await untilSettled()
    const window = windows[0] as FakeWindow
    window.fire('ready-to-show')
    window.fire('show')

    clock.advance(CONFIRM_READY_DELAY_MS - 1)
    expect(await ask(ipcMain, confirmChannel('answer'), fromWindow(window), true)).toStrictEqual({
      ok: false,
      reason: CONFIRM_NOT_YET,
    })

    clock.advance(1)
    expect(await ask(ipcMain, confirmChannel('answer'), fromWindow(window), true)).toStrictEqual({
      ok: true,
      data: { answered: true },
    })
    await untilSettled()
    expect(spawner.calls).toHaveLength(1)
  })

  it('cancels on the instant, whatever the delay still has to run', async () => {
    // Cancel is the default button and the way out: it takes no delay, because
    // the thing the delay protects is a spend and not an exit. A window that
    // made the operator wait to decline would be one that traps them in the
    // moment the window appears under their pointer.
    const clock = aClock()
    const { confirmer, ipcMain, windows } = confirmOver({ now: clock.now })
    const spawner = countingSpawner()
    const launched = aLaunch(runnerOver(confirmer, spawner))
    await untilSettled()
    const window = windows[0] as FakeWindow
    appears(window)

    expect(await ask(ipcMain, confirmChannel('answer'), fromWindow(window), false)).toStrictEqual({
      ok: true,
      data: { answered: true },
    })
    expect((await launched).reason).toBe(DECLINED_CANCELLED)
    expect(spawner.calls).toStrictEqual([])
  })

  it('reads the delay off a clock that only moves forward, never from the wall clock', async () => {
    // The delay is an elapsed-time bound, like the launcher's limits
    // (`launch.test.ts` reads the same clock for the same reason): the wall clock
    // is a value the operator, NTP or a machine waking from sleep can move, and a
    // `readyAt` read from `Date.now` is an hour in the past the moment the clock
    // is set forward -- which makes an appeared window answerable on the instant,
    // the spend the delay exists to refuse. The shipped default is therefore a
    // monotonic reading (`performance.now`).
    //
    // The confirmer is built here with **no** clock of the test's (`now: null`),
    // so what this case exercises is the production default itself and not the
    // helper's stand-in for it: a default reverted to `Date.now` would pass every
    // case whose clock the helper supplies.
    let at = NOW
    const realNow = performance.now
    ;(performance as { now: () => number }).now = (): number => at
    const realDate = Date.now
    const moved = vi.spyOn(Date, 'now').mockImplementation(() => realDate() + 3_600_000)
    try {
      const { confirmer, ipcMain, windows } = confirmOver({ now: null })
      const spawner = countingSpawner()
      void aLaunch(runnerOver(confirmer, spawner))
      await untilSettled()
      const window = windows[0] as FakeWindow
      appears(window)

      // An hour of wall clock, and the yes is still not an answer to this
      // window.
      expect(await ask(ipcMain, confirmChannel('answer'), fromWindow(window), true)).toStrictEqual({
        ok: false,
        reason: CONFIRM_NOT_YET,
      })
      await untilSettled()
      expect(spawner.calls).toStrictEqual([])

      // And the clock the default does read still ends the delay: the refusal
      // above is the wall clock being ignored, not a window that can never be
      // answered.
      at += CONFIRM_READY_DELAY_MS
      expect(await ask(ipcMain, confirmChannel('answer'), fromWindow(window), true)).toStrictEqual({
        ok: true,
        data: { answered: true },
      })
      await untilSettled()
      expect(spawner.calls).toHaveLength(1)
    } finally {
      moved.mockRestore()
      ;(performance as { now: () => number }).now = realNow
    }
  })
})

describe('the launch, through the real confirmation window', () => {
  it('spawns nothing for a yes from any sender but the window’s own top frame, and one child for the real one', async () => {
    // The shipped chain, end to end: the surface the entry registers, the real
    // `createConfirmWindow`, and the real launcher over it. "The operator said
    // yes" is the only thing that reaches `spawn`, and it is a message that
    // arrives from the window's own top frame.
    const clock = aClock()
    const { confirmer, ipcMain, windows } = confirmOver({ now: clock.now })
    const spawner = countingSpawner()
    const runner = runnerOver(confirmer, spawner)
    void aLaunch(runner)
    await untilSettled()
    const window = windows[0] as FakeWindow
    expect(window).toBeDefined()

    // The dashboard's own `webContents`, a subframe of this window, and another
    // window's `webContents`: each is refused, and none of them decides.
    const dashboard = fakeContents(CONFIRM_ENTRY_URL)
    expect(
      await ask(ipcMain, confirmChannel('answer'), { sender: dashboard, senderFrame: dashboard.mainFrame }, true),
    ).toStrictEqual({ ok: false, reason: SENDER_REFUSAL })
    expect(
      await ask(ipcMain, confirmChannel('answer'), { sender: window.webContents, senderFrame: { url: CONFIRM_ENTRY_URL } }, true),
    ).toStrictEqual({ ok: false, reason: SENDER_REFUSAL })
    const another = fakeContents(CONFIRM_ENTRY_URL)
    expect(
      await ask(ipcMain, confirmChannel('answer'), { sender: another, senderFrame: another.mainFrame }, true),
    ).toStrictEqual({ ok: false, reason: SENDER_REFUSAL })
    await untilSettled()
    expect(spawner.calls).toStrictEqual([])
    expect(window.closeRequested()).toBe(false)

    // The window's own top frame, and the one yes: exactly one child. The window
    // has appeared and settled by now (`appears`), because a yes before that is
    // refused by the delay rather than accepted -- the case below is that one.
    appears(window)
    clock.advance(CONFIRM_READY_DELAY_MS)
    expect(await ask(ipcMain, confirmChannel('answer'), fromWindow(window), true)).toStrictEqual({
      ok: true,
      data: { answered: true },
    })
    await untilSettled()
    expect(spawner.calls).toHaveLength(1)
  })

  it('spawns nothing, and answers the cancel sentence, for a no on the window’s own channel', async () => {
    const { confirmer, ipcMain, windows } = confirmOver()
    const spawner = countingSpawner()
    const launched = aLaunch(runnerOver(confirmer, spawner))
    await untilSettled()

    expect(
      await ask(ipcMain, confirmChannel('answer'), fromWindow(windows[0] as FakeWindow), false),
    ).toStrictEqual({ ok: true, data: { answered: true } })
    expect((await launched).reason).toBe(DECLINED_CANCELLED)
    expect(spawner.calls).toStrictEqual([])
  })

  it('spawns nothing for Escape and for Enter, the window’s own two ways out', async () => {
    for (const key of ['Escape', 'Enter']) {
      const { confirmer, windows } = confirmOver()
      const spawner = countingSpawner()
      const launched = aLaunch(runnerOver(confirmer, spawner))
      await untilSettled()
      const window = windows[0] as FakeWindow

      const event = keyEvent()
      window.webContents.fire('before-input-event', event, { type: 'keyDown', key })
      expect(event.prevented, key).toBe(true)
      expect((await launched).reason).toBe(DECLINED_CANCELLED)
      expect(spawner.calls, key).toStrictEqual([])
    }
  })

  it('spawns nothing when the window is closed', async () => {
    const { confirmer, windows } = confirmOver()
    const spawner = countingSpawner()
    const launched = aLaunch(runnerOver(confirmer, spawner))
    await untilSettled()

    ;(windows[0] as FakeWindow).fire('closed')
    expect((await launched).reason).toBe(DECLINED_CANCELLED)
    expect(spawner.calls).toStrictEqual([])
  })

  it('spawns nothing when the project’s launch is cancelled while its window is open', async () => {
    const { confirmer, windows } = confirmOver()
    const spawner = countingSpawner()
    const runner = runnerOver(confirmer, spawner)
    const launched = aLaunch(runner)
    await untilSettled()
    const window = windows[0] as FakeWindow

    runner.cancelFor(PROJECT)
    expect(window.closeRequested()).toBe(true)
    expect((await launched).reason).toBe(DECLINED_CANCELLED)
    expect(spawner.calls).toStrictEqual([])
  })

  it('spawns nothing, and opens no window, when the project’s launch is cancelled while it is still being built', async () => {
    const { confirmer, windows } = confirmOver()
    const spawner = countingSpawner()
    const runner = runnerOver(confirmer, spawner)
    // Before the launch's first `await`: the project's entry is the reservation
    // token, and the window that would open a moment later is exactly the one
    // rule 11 says must not be there.
    const launched = aLaunch(runner)
    runner.cancelFor(PROJECT)

    expect((await launched).reason).toBe(DECLINED_CANCELLED)
    expect(windows).toStrictEqual([])
    expect(spawner.calls).toStrictEqual([])
  })

  it('opens no second window for the same installation, and refuses a third app-wide', async () => {
    const { confirmer, windows } = confirmOver()
    const spawner = countingSpawner()
    const runner = runnerOver(confirmer, spawner)

    const first = aLaunch(runner)
    await untilSettled()
    expect(windows).toHaveLength(1)

    const same = await aLaunch(runner)
    expect(same.ok).toBe(false)
    expect(same.reason).toContain('already in flight')
    expect(windows).toHaveLength(1)

    const second = aLaunchIn(runner, OTHER_PROJECT)
    await untilSettled()
    expect(windows).toHaveLength(2)

    const third = await aLaunchIn(runner, 'third')
    expect(third.ok).toBe(false)
    expect(third.reason).toContain(`at most ${String(MAX_IN_FLIGHT)} at a time`)
    expect(windows).toHaveLength(2)
    expect(spawner.calls).toStrictEqual([])

    for (const open of windows) open.fire('closed')
    void first
    void second
  })

  it('asks one installation to confirm at most three launches a minute', async () => {
    let at = NOW
    const { confirmer, windows } = confirmOver()
    const spawner = countingSpawner()
    const runner = runnerOver(confirmer, spawner, { now: () => at })

    for (let attempt = 0; attempt < CONFIRM_RATE_PER_INSTALLATION; attempt += 1) {
      const launched = aLaunch(runner)
      await untilSettled()
      ;(windows[windows.length - 1] as FakeWindow).fire('closed')
      expect((await launched).reason).toBe(DECLINED_CANCELLED)
    }
    expect(windows).toHaveLength(CONFIRM_RATE_PER_INSTALLATION)

    const refused = await aLaunch(runner)
    expect(refused.ok).toBe(false)
    expect(refused.reason).toContain(`${String(CONFIRM_RATE_PER_INSTALLATION)} launches`)
    expect(windows).toHaveLength(CONFIRM_RATE_PER_INSTALLATION)

    // The window is on the request and not on the answer, and it is a window:
    // once the minute it was taken in has passed, the installation is asked
    // again.
    at += CONFIRM_RATE_WINDOW_MS
    const later = aLaunch(runner)
    await untilSettled()
    expect(windows).toHaveLength(CONFIRM_RATE_PER_INSTALLATION + 1)
    ;(windows[windows.length - 1] as FakeWindow).fire('closed')
    expect((await later).reason).toBe(DECLINED_CANCELLED)
    expect(spawner.calls).toStrictEqual([])
  })

  it('asks the whole app to confirm at most six launches a minute', async () => {
    const { confirmer, windows } = confirmOver()
    const spawner = countingSpawner()
    const runner = runnerOver(confirmer, spawner)

    for (let attempt = 0; attempt < CONFIRM_RATE_APP; attempt += 1) {
      const launched = aLaunchIn(runner, `installation-${String(attempt)}`)
      await untilSettled()
      ;(windows[windows.length - 1] as FakeWindow).fire('closed')
      expect((await launched).reason).toBe(DECLINED_CANCELLED)
    }
    expect(windows).toHaveLength(CONFIRM_RATE_APP)

    const refused = await aLaunchIn(runner, 'one-too-many')
    expect(refused.ok).toBe(false)
    expect(refused.reason).toContain(`${String(CONFIRM_RATE_APP)} launches`)
    expect(windows).toHaveLength(CONFIRM_RATE_APP)
    expect(spawner.calls).toStrictEqual([])
  })

  it('pauses an installation for ten seconds after an accepted launch', async () => {
    let at = NOW
    const clock = aClock()
    const { confirmer, ipcMain, windows } = confirmOver({ now: clock.now })
    const spawner = replyingSpawner()
    const runner = runnerOver(confirmer, spawner, { now: () => at })

    const launched = aLaunch(runner)
    await untilSettled()
    ready(windows[0] as FakeWindow, clock)
    expect(
      await ask(ipcMain, confirmChannel('answer'), fromWindow(windows[0] as FakeWindow), true),
    ).toStrictEqual({ ok: true, data: { answered: true } })
    expect((await launched).ok).toBe(true)
    expect(spawner.calls).toHaveLength(1)

    const paused = await aLaunch(runner)
    expect(paused.ok).toBe(false)
    expect(paused.reason).toContain('10 seconds')

    at += LAUNCH_COOL_DOWN_MS
    const later = aLaunch(runner)
    await untilSettled()
    ready(windows[1] as FakeWindow, clock)
    expect(
      await ask(ipcMain, confirmChannel('answer'), fromWindow(windows[1] as FakeWindow), true),
    ).toStrictEqual({ ok: true, data: { answered: true } })
    expect((await later).ok).toBe(true)
    expect(spawner.calls).toHaveLength(2)
  })

  it('drops the window’s own entry once it is gone, however it was answered', async () => {
    // Cancel is a default and an answer is spent, and the window is gone from
    // the module's map as well as from the screen: a later message from that
    // `webContents` is refused like any stranger's, and a close of its run id
    // decides nothing a second time.
    for (const way of ['yes', 'no', 'closed'] as const) {
      const clock = aClock()
      const { confirmer, ipcMain, windows } = confirmOver({ now: clock.now })
      const decider = recordingDecider()
      confirmer.open(viewFixture(), decider.decide)
      const window = windows[0] as FakeWindow
      const sender = window.webContents
      // `closed` decides whatever the delay is; a yes has to be an answer.
      if (way === 'yes') ready(window, clock)

      if (way === 'closed') {
        window.fire('closed')
      } else {
        await ask(ipcMain, confirmChannel('answer'), fromWindow(window), way === 'yes')
        // Electron's own order: the decision closes the window, and the `closed`
        // that follows is what takes it out of the map.
        window.fire('closed')
      }
      expect(decider.calls, way).toStrictEqual([[way === 'yes', undefined]])

      expect(
        await ask(ipcMain, confirmChannel('fields'), { sender, senderFrame: sender.mainFrame }),
        way,
      ).toStrictEqual({ ok: false, reason: SENDER_REFUSAL })
      expect(
        await ask(ipcMain, confirmChannel('answer'), { sender, senderFrame: sender.mainFrame }, true),
        way,
      ).toStrictEqual({ ok: false, reason: SENDER_REFUSAL })

      confirmer.close(RUN_ID)
      expect(decider.calls, way).toStrictEqual([[way === 'yes', undefined]])
    }
  })
})

describe('the view the window is shown', () => {
  it('is deep-frozen, and carries nothing but strings and numbers', () => {
    const view = confirmView(frozenLaunch())

    expect(Object.isFrozen(view)).toBe(true)
    expect(Object.isFrozen(view.argv)).toBe(true)
    expect(Object.keys(view).sort()).toStrictEqual([
      'argv',
      'installation',
      'planTask',
      'recordedRepository',
      'ref',
      'repository',
      'runId',
      'taskByteLength',
      'taskText',
      'title',
    ])

    // The frozen launch's own fields the window must never be shown: the plan,
    // the child's environment and working directory, and the task's bytes -- a
    // `Uint8Array`, which `Object.freeze` does not freeze at all.
    for (const forbidden of ['plan', 'env', 'cwd', 'taskBytes']) {
      expect(Object.keys(view), `the view names ${forbidden}`).not.toContain(forbidden)
    }
    for (const value of Object.values(view)) {
      expect(value).not.toBeInstanceOf(Uint8Array)
      // Everything else is a string, a number, null or an array of strings.
      const plain =
        typeof value === 'string' ||
        typeof value === 'number' ||
        value === null ||
        (Array.isArray(value) && value.every((word) => typeof word === 'string'))
      expect(plain, `the view carries ${String(value)}`).toBe(true)
    }
  })

  it('carries the operator’s own fields, and the exact words the child is spawned with', () => {
    const launch = frozenLaunch({ title: 'the backlog', planTask: 'backlog/triage' })
    const view = confirmView(launch)

    expect(view.runId).toBe(launch.runId)
    expect(view.installation).toBe(PROJECT)
    expect(view.recordedRepository).toBe(RECORDED_REPOSITORY)
    expect(view.repository).toBe(REPOSITORY)
    expect(view.ref).toBe(BRANCH)
    expect(view.title).toBe('the backlog')
    expect(view.planTask).toBe('backlog/triage')
    // The argv is the whole command line, the program word included, field by
    // field: what the window shows is what is spawned.
    expect(view.argv).toStrictEqual([BINARY, ...launch.argv.slice(1)])
  })

  it('shows the whole task with the format characters stripped, and its real byte size', () => {
    const task = 'fix the ​zero-width break and the ‮override'
    const view = confirmView(frozenLaunch({ task }))

    expect(view.taskText).toBe(displayTaskText(task))
    expect(view.taskText).toContain('zero-width break')
    expect(view.taskText).not.toContain('​')
    expect(view.taskText).not.toContain('‮')
    // The size is the **trimmed task's** UTF-8 length: the child is given those
    // bytes, and the number is a fact about them rather than about the display.
    expect(view.taskByteLength).toBe(new TextEncoder().encode(task).byteLength)
  })

  it('is built from the launcher’s own frozen launch, not from a record read again', async () => {
    const { confirmer, ipcMain, windows } = confirmOver()
    const runner = runnerOver(confirmer, countingSpawner())
    void aLaunch(runner)
    await untilSettled()

    const window = windows[0] as FakeWindow
    const reply = (await ask(ipcMain, confirmChannel('fields'), fromWindow(window))) as {
      readonly ok: true
      readonly data: ConfirmView
    }
    expect(reply.data.installation).toBe(PROJECT)
    expect(reply.data.repository).toBe(REPOSITORY)
    expect(reply.data.ref).toBe(BRANCH)
    expect(reply.data.taskText).toBe(TASK)
    expect(reply.data.argv).toStrictEqual([
      BINARY,
      'run',
      '--task-file',
      '-',
      '--json',
      '--run-id',
      reply.data.runId,
      '--repo',
      REPOSITORY,
      '--project',
      PROJECT,
    ])
    expect(reply.data.runId).toBe(RUN_ID)
  })
})

describe('the confirm channels', () => {
  it('has one prefix, and the two names the preload mirrors', () => {
    expect(CONFIRM_CHANNEL_PREFIX).toBe('confirm:')
    expect(confirmChannel('fields')).toBe('confirm:fields')
    expect(confirmChannel('answer')).toBe('confirm:answer')
    expect(CONFIRM_ENTRY_PATH).toBe('/confirm.html')
    expect(CONFIRM_ENTRY_URL).toBe('app://console/confirm.html')
    expect(confirmPreloadPath(APP_PATH)).toBe(`${APP_PATH}/dist/preload/confirm.js`)
  })
})

// ---------------------------------------------------------------------------
// The fixtures.
// ---------------------------------------------------------------------------

/** frozenLaunch is a `FrozenLaunch` shaped like the launcher's own, for the
 * `confirmView` cases: no launcher runs there, because the claim is about what
 * the conversion carries rather than about how the object was reached. */
function frozenLaunch(
  overrides: {
    readonly runId?: string
    readonly title?: string | null
    readonly planTask?: string | null
    readonly task?: string
  } = {},
): FrozenLaunch {
  const task = overrides.task ?? TASK
  const argv = Object.freeze([
    BINARY,
    'run',
    '--task-file',
    '-',
    '--json',
    '--run-id',
    overrides.runId ?? RUN_ID,
    '--repo',
    REPOSITORY,
    '--project',
    PROJECT,
  ])
  return Object.freeze({
    runId: overrides.runId ?? RUN_ID,
    project: PROJECT,
    recordedRepository: RECORDED_REPOSITORY,
    repository: REPOSITORY,
    branch: BRANCH,
    title: overrides.title ?? null,
    planTask: overrides.planTask ?? null,
    taskText: task,
    taskBytes: new TextEncoder().encode(task),
    fingerprint: '["record-1","sandbox","/usr/local/bin/fugaro","","",""]',
    argv,
    cwd: '/tmp/fugaro-console-501/cwd',
    env: Object.freeze({ HOME: '/home/person-1' }),
    plan: Object.freeze({
      operation: 'run.launch',
      command: BINARY,
      prefix: Object.freeze([]),
      argv,
      wrapper: null,
      cwd: '/tmp/fugaro-console-501/cwd',
      env: Object.freeze({ HOME: '/home/person-1' }),
      project: PROJECT,
      backoff: Object.freeze({ minMs: 250, maxMs: 5_000 }),
    }),
  }) as FrozenLaunch
}

/** viewFixture is a `ConfirmView` for the cases that are about the window rather
 * than about the conversion. */
function viewFixture(overrides: { readonly runId?: string } = {}): ConfirmView {
  return confirmView(frozenLaunch(overrides))
}
