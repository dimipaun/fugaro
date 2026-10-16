// The console's Electron main process entry point (desktop-console plan Task
// 1.1).
//
// The plan's Files column for Task 1.1 names `src/main/window.ts` and
// `src/main/protocol.ts`; this file is here because those two are libraries and
// something has to call them. Without an entry point the `app://` scheme is
// never registered, `protocol.handle` never runs, and the e2e spec has no
// application to launch -- the window flags would be a set of options nothing
// constructs. It keeps to the minimum that makes the two modules live, and it
// is the only place in the console that imports Electron at runtime: every
// decision it could get wrong is in `window.ts` or `protocol.ts`, where the
// unit test can reach it under plain Node.
//
// Note where the document is read from. `tsc` emits JavaScript only, so the
// bundle's files stay in the source tree and the handler's root is
// `src/renderer` under the app's own path. Task 4.1's builder is the one that
// gets to decide where a packaged console keeps its renderer files, and it will
// have to pack them; until then there is no packaging step to disagree with.

import { join } from 'node:path'

import { app, BrowserWindow, protocol, session } from 'electron'

import { handleAppRequests, registerAppScheme } from './protocol'
import { createConsoleWindow, installWebContentsSecurity } from './window'

// Before `whenReady` resolves, which is the only window Electron gives for it:
// a scheme registered later is not a standard or secure one.
registerAppScheme(protocol)

// Every webContents Electron ever creates gets the three denials, not just the
// console's own window -- so a webContents that appears for a reason this file
// does not know about is still one that cannot navigate, open a window or
// attach a `<webview>`. `createConsoleWindow` installs them on the window it
// builds as well: a window that is correct on its own does not depend on the
// global hook having been registered.
app.on('web-contents-created', (_event, contents) => {
  installWebContentsSecurity(contents)
})

/**
 * rendererRoot is the directory the protocol handler serves, and the only
 * directory it will ever read a file from.
 */
function rendererRoot(): string {
  return join(app.getAppPath(), 'src', 'renderer')
}

function reportAndQuit(error: unknown): void {
  console.error('[console] the window could not be created:', error)
  app.exit(1)
}

/**
 * main is what `whenReady` resolves into: serve the bundle, then build the
 * window that draws it. `session.defaultSession` is read here rather than at
 * module scope because it does not exist until the app is ready.
 */
async function main(): Promise<void> {
  handleAppRequests(protocol, rendererRoot())
  await createConsoleWindow({ BrowserWindow, session: session.defaultSession })
}

app.whenReady().then(main).catch(reportAndQuit)

// The console is a single window: closing it quits, except on macOS, where the
// convention is that the app stays running and `activate` brings the window
// back.
app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit()
})

app.on('activate', () => {
  if (BrowserWindow.getAllWindows().length === 0) {
    main().catch(reportAndQuit)
  }
})
