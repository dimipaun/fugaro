// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// The launch confirmation window's main-process module (design section 5.1 rule
// 11; console V2.5.1a part 2b-1, and the seam `launch.ts`'s `LaunchConfirmer`
// names).
//
// **What this module is.** One `BrowserWindow`, built for one answer: a modal
// window loading `app://console/confirm.html` over the `app://` scheme with a
// preload of its own, showing the frozen launch's display-only fields and two
// buttons. `dialog.showMessageBox` is deliberately not used -- a native box
// cannot show a 16 KiB task scrollably, and the design names the window for
// exactly that reason.
//
// **The two properties that make it a confirmation rather than a prompt.**
//
//   1. **Cancel is the default, and it is enforced here.** Escape, Enter and the
//      window's own close all decline; only a deliberate answer on the window's
//      own channel confirms. A renderer cannot open, close, re-focus or re-open
//      this window, and the keyboard cannot turn a cancel into a yes.
//   2. **Only this window's own top frame can answer it.** Both channels are
//      registered **once**, at startup, and each message is checked against the
//      `webContents` of an open confirmation window and that `webContents`'s own
//      `mainFrame`, **by identity and not by URL**: the dashboard document and
//      the confirmation document are served from one origin, so an origin check
//      -- or a URL check, or a check that merely compared frames -- would let the
//      dashboard answer for the operator. `ipc.ts`'s `isSenderOnDocument` is the
//      dashboard's and the picker's gate and cannot express this: it asks whether
//      a frame is on a *document*, and what this needs is whether the sender is
//      *this window*.
//
// What the window is shown is `ConfirmView`, a deep-frozen copy built once by
// `confirmView` -- never the `FrozenLaunch`, which carries the plan, the child's
// environment and the task's `Uint8Array`.

import { join } from 'node:path'

import type { BrowserWindow, BrowserWindowConstructorOptions } from 'electron'

import type { ConfirmView, LaunchConfirmer } from './launch'
import type { ConsoleEventLike, IpcMainLike } from './ipc'
import { SENDER_REFUSAL } from './ipc'
import { APP_ORIGIN } from './protocol'
import { consoleWebPreferences, denyPageTitleUpdate, installWebContentsSecurity } from './window'

/** CONFIRM_CHANNEL_PREFIX is the whole of this window's IPC surface, and it is a
 * prefix of its own: neither the dashboard's `fugaro:console/` nor the picker's
 * `fugaro:installations/` may reach these two channels, and no `confirm:` channel
 * appears on either of their preloads. */
export const CONFIRM_CHANNEL_PREFIX = 'confirm:'

/** ConfirmChannelName is the closed set of channels: two, and one method behind
 * each. */
export type ConfirmChannelName = 'fields' | 'answer'

/** confirmChannel is the only place a confirm channel is built. */
export function confirmChannel(name: ConfirmChannelName): string {
  return CONFIRM_CHANNEL_PREFIX + name
}

/** CONFIRM_ENTRY_PATH is the window's document, and CONFIRM_ENTRY_URL the URL it
 * loads. Both are this module's rather than `protocol.ts`'s, and the document
 * itself lands in part 2b-2: until then `SERVED_FILES` does not name it, the
 * shipped app never opens this window (`index.ts` wires `DECLINING_CONFIRMER`),
 * and the load's failure is the cancel rather than a window nobody can see. */
export const CONFIRM_ENTRY_PATH = '/confirm.html'
export const CONFIRM_ENTRY_URL = `${APP_ORIGIN}${CONFIRM_ENTRY_PATH}`

/** CONFIRM_WINDOW_TITLE is the window's title, and the document may not take it
 * over: this module refuses the page's own `page-title-updated` beside the three
 * denials, the way `buildHardenedWindow` does for every other window, so the
 * title keeps naming what the window is. */
export const CONFIRM_WINDOW_TITLE = 'Fugaro console — confirm the launch'

/** CONFIRM_WINDOW_SIZE is the window's size, and it is larger than the picker's
 * because the task text is shown whole: the operator scrolls a 16 KiB task here,
 * and the operator reads it rather than the app summarising it. */
export const CONFIRM_WINDOW_SIZE = Object.freeze({ width: 720, height: 640 })

/** confirmPreloadPath is where the build puts this window's preload, derived from
 * the app's own root the way `pickerPreloadPath` is: `tsconfig.build.json`
 * compiles it to `dist/preload/confirm.js` beside the dashboard's and the
 * picker's, and the path is an argument rather than a constant so a unit test
 * can hold it without a build on disk. */
export function confirmPreloadPath(appPath: string): string {
  return join(appPath, 'dist', 'preload', 'confirm.js')
}

/** CONFIRM_ANSWER_REFUSAL is what a message that is not a boolean is answered
 * with. It carries nothing the sender supplied. */
export const CONFIRM_ANSWER_REFUSAL = 'a confirmation is answered with a yes or a no and nothing else'

/** CONFIRM_ALREADY_ANSWERED is what a second answer for one window is answered
 * with: the first valid answer is the one that decides, and a repeat decides
 * nothing more. */
export const CONFIRM_ALREADY_ANSWERED = 'this confirmation has already been answered'

/** ConfirmWindowDeps is everything the confirmation window is built from, and it
 * is injected for the reason every other window's dependencies are: the unit test
 * drives all of this under plain Node. */
export interface ConfirmWindowDeps {
  readonly BrowserWindow: new (options: BrowserWindowConstructorOptions) => BrowserWindow
  readonly ipcMain: IpcMainLike
  /** parent is the dashboard window when there is one, so this window is modal
   * over it rather than over the desktop. A console with no dashboard -- the
   * refusal screen, a startup that has not opened one yet -- passes none. */
  readonly parent: () => BrowserWindow | null
  /** the app's own root, from which `confirmPreloadPath` derives the preload. */
  readonly appPath: string
  /** the document this window loads, which is `CONFIRM_ENTRY_URL` in the shipped
   * app and an argument here so a test can read which URL was loaded. */
  readonly entryUrl: string
}

/** confirmOptions is the confirmation window's construction options: the
 * dashboard's own hardened `consoleWebPreferences()` with **one** addition, its
 * preload, and the modality that makes it a window the dashboard waits behind.
 * There is no relaxation here and none to make. */
export function confirmOptions(
  preloadPath: string,
  parent: BrowserWindow | null,
): BrowserWindowConstructorOptions {
  return {
    width: CONFIRM_WINDOW_SIZE.width,
    height: CONFIRM_WINDOW_SIZE.height,
    show: false,
    title: CONFIRM_WINDOW_TITLE,
    modal: true,
    // Absent rather than `null` when there is no dashboard: Electron reads a
    // `parent` it cannot resolve as an error, and a console with no window open
    // still has to be able to ask.
    ...(parent === null ? {} : { parent }),
    webPreferences: { ...consoleWebPreferences(), preload: preloadPath },
  }
}

/** ConfirmEntry is one open window: the view it may read, the `decide` its answer
 * resolves, and whether it has answered yet. It lives until the window is gone. */
interface ConfirmEntry {
  readonly view: ConfirmView
  readonly decide: (yes: boolean, reason?: string) => void
  readonly window: BrowserWindow
  answered: boolean
}

/**
 * createConfirmWindow builds the confirmer `launch.ts`'s seam expects.
 *
 * The two channels are registered **once, here** -- before any window exists --
 * because that is what makes the gate a check rather than a list: what the
 * handler reads is the open windows' own `webContents`, so a window that has
 * never been opened has nothing to reach with, and a window that is gone is gone
 * from the map as well as from the screen.
 */
export function createConfirmWindow(deps: ConfirmWindowDeps): LaunchConfirmer {
  const entries = new Map<object, ConfirmEntry>()

  /** entryFor is the whole gate: the message's sender is the `webContents` of an
   * open window, and the frame that sent it is that `webContents`'s own
   * `mainFrame`. Every read is inside the `try` because Electron's getters on a
   * destroyed frame *throw*, and a gate that let that escape would answer a
   * rejected invoke instead of a refusal. */
  function entryFor(event: ConsoleEventLike | null | undefined): ConfirmEntry | null {
    try {
      if (event === null || event === undefined) return null
      const frame = event.senderFrame
      const sender = event.sender
      if (frame === null || frame === undefined) return null
      if (sender === null || sender === undefined) return null
      const entry = entries.get(sender)
      if (entry === undefined) return null
      return sender.mainFrame === frame ? entry : null
    } catch {
      return null
    }
  }

  /** finish is the one path to a decision, and it is reachable at most once per
   * window: an answer already given is not given again, and the close that
   * follows it is not a cancel. */
  function finish(entry: ConfirmEntry, yes: boolean): void {
    if (entry.answered) return
    entry.answered = true
    entry.decide(yes)
    entry.window.close()
  }

  deps.ipcMain.handle(confirmChannel('fields'), (event) => {
    const entry = entryFor(event)
    if (entry === null) return { ok: false, reason: SENDER_REFUSAL }
    return { ok: true, data: entry.view }
  })

  deps.ipcMain.handle(confirmChannel('answer'), (event, raw) => {
    const entry = entryFor(event)
    if (entry === null) return { ok: false, reason: SENDER_REFUSAL }
    if (typeof raw !== 'boolean') return { ok: false, reason: CONFIRM_ANSWER_REFUSAL }
    if (entry.answered) return { ok: false, reason: CONFIRM_ALREADY_ANSWERED }
    finish(entry, raw)
    return { ok: true, data: { answered: true } }
  })

  return Object.freeze({
    open(view: ConfirmView, decide: (yes: boolean, reason?: string) => void): void {
      const window = new deps.BrowserWindow(
        confirmOptions(confirmPreloadPath(deps.appPath), deps.parent()),
      )
      const entry: ConfirmEntry = { view, decide, window, answered: false }
      entries.set(window.webContents, entry)

      // The three denials every window of this app gets: no navigation, no
      // popup and no `<webview>`, on this window's own `webContents`.
      installWebContentsSecurity(window.webContents)

      // And the title stays this process's: Electron applies a page's `<title>`
      // over the one the window was constructed with, and this window's title
      // says which window it is.
      window.on('page-title-updated', (event) => {
        denyPageTitleUpdate(event)
      })

      // Cancel is the default, and this is where it is enforced: Enter and
      // Escape are the window's own two ways out, both declined, whatever
      // modifiers they carry. Only a click on the confirming button -- which
      // arrives as `answer(true)` on the channel above -- confirms.
      window.webContents.on('before-input-event', (event, input) => {
        if (input.type !== 'keyDown') return
        if (input.key !== 'Enter' && input.key !== 'Escape') return
        event.preventDefault()
        finish(entry, false)
      })

      // `closed` is every path by which the window goes away -- the operator's
      // close box, a parent that closed, a decision above -- and a window that
      // went away without an answer is the operator declining.
      window.on('closed', () => {
        entries.delete(window.webContents)
        finish(entry, false)
      })

      window.once('ready-to-show', () => {
        window.show()
      })

      // A document that did not load is a cancel rather than a window left open:
      // `show` is on `ready-to-show`, so a failed load would leave a window
      // nobody can see and nobody can close, holding this launch's run id and its
      // installation's slot for the life of the process.
      void window.loadURL(deps.entryUrl).catch(() => {
        finish(entry, false)
      })
    },
  })
}
