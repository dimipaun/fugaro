// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// D13's first run, as the end-to-end specs meet it.
//
// A console launched with no installation chosen opens its dashboard or its
// refusal window -- whichever the handshake allows -- and **nothing else**: the
// owner's design ruling of 2026-10-25 removed the picker window the first run
// used to open beside it (the console never shows a picker screen; the rail's
// `+` and the menu item are the two doors to it). `expectNoFirstRunPicker`
// holds the specs that launch with an empty scratch profile to that: once the
// console's window has loaded, it is the only window and it is not the picker.

import { expect } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { PICKER_ENTRY_URL } from '../../src/main/protocol'

/** expectNoFirstRunPicker waits for `consoleWindow` to load and checks it is the
 * app's only window and not the picker's document. */
export async function expectNoFirstRunPicker(app: ElectronApplication, consoleWindow: Page): Promise<void> {
  await consoleWindow.waitForLoadState('load')
  expect(consoleWindow.url()).not.toBe(PICKER_ENTRY_URL)
  const urls = await app.evaluate(({ BrowserWindow }) =>
    BrowserWindow.getAllWindows().map((window) => window.webContents.getURL()),
  )
  expect(urls, 'the first run opened a window beside the console’s own').toHaveLength(1)
  expect(urls).not.toContain(PICKER_ENTRY_URL)
}
