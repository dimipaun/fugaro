// D13's first run, as the end-to-end specs meet it (desktop-console plan Task
// 3.4b, step 4).
//
// A console launched with no installation chosen opens its dashboard or its
// refusal window -- whichever the handshake allows -- and then, once, the
// installation picker. The specs that test that first-run path (the startup
// handshake, the renderer entry) launch with an empty scratch profile, so the
// picker opens in every one of their launches. `dismissFirstRunPicker` asserts
// that it did, and that it is the picker's own document, and then closes it, so
// the assertions those specs already make about **the console's** window --
// exactly one, on the document the handshake calls for, with its title -- are
// made about that window alone and stay exactly as strict as they were.

import { expect } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { PICKER_ENTRY_URL } from '../../src/main/protocol'

/** dismissFirstRunPicker waits for the picker D13's first run opens beside
 * `consoleWindow`, checks it is the picker's document, and closes it. */
export async function dismissFirstRunPicker(app: ElectronApplication, consoleWindow: Page): Promise<void> {
  const picker = app.windows().find((window) => window !== consoleWindow) ?? (await app.waitForEvent('window'))
  // `load`, not `domcontentloaded`: the picker is closed below, and a window
  // closed while its document is still loading is a different path (the
  // aborted load) from the one these specs are about.
  await picker.waitForLoadState('load')
  expect(picker.url(), 'the first run opened a second window that is not the picker').toBe(PICKER_ENTRY_URL)
  expect(consoleWindow.url()).not.toBe(PICKER_ENTRY_URL)
  await app.evaluate(({ BrowserWindow }, url) => {
    for (const window of BrowserWindow.getAllWindows()) {
      if (window.webContents.getURL() === url) window.close()
    }
  }, PICKER_ENTRY_URL)
  await expect
    .poll(() => app.windows().length, {
      message: `closing the picker took the console window with it (app exit code ${String(app.process().exitCode)})`,
    })
    .toBe(1)
}
