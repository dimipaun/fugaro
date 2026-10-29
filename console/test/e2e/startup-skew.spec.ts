// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The handshake wired into startup, in a real Electron process (desktop-console
// plan Task 3.3; design section 7.1, D7).
//
// Three files make claims about the same handshake, and it is worth being exact
// about which claim each one can carry:
//
//   - `test/unit/version.test.ts` proves the *parse and the range*: what a line
//     means, and where the floor and the ceiling are;
//   - `test/e2e/skew.spec.ts` proves the *drawing*: it loads the compiled
//     renderer into the running window and calls `renderVersionSkew`, so the
//     refusal screen's words and the banner's are asserted there;
//   - this file proves the *wiring*, and only that: the app is started the way a
//     user starts it (`electron.launch`, no injected script), a stub `fugaro` is
//     put where the console's own resolution will find it, and what is asserted
//     is which **document** the window ended up on and what the **window's own
//     title** was told. Those are the two things the app decides before any
//     renderer code runs, and they are the whole of D7 at this level: a refused
//     version never produces a dashboard window.
//
// **The premise, stated because it decides what can run here.** There is no
// `FUGARO_BIN` and this task does not add one, so the stub is chosen the only
// way the resolution allows: `PATH` (and `HOME`, so `~/.local/bin` is covered)
// point at it. `binDirectories` searches the platform's well-known directories
// (`/opt/homebrew/bin`, `/usr/local/bin` and `~/.local/bin` on macOS;
// `/usr/local/bin`, `/usr/bin` and `~/.local/bin` on Linux) **before** every
// `PATH` entry, so on a machine that has a real `fugaro` in one of those the
// stub never wins and the app reads the
// machine's binary instead. `test/support/cliStub.ts`'s `resolveConsoleBinary`
// asks the app's own question with the app's own arguments, so this file knows
// which of the two it is in and asserts the document that binary calls for.
// The first case is that first-run resolution, and it asserts whatever document
// the binary it found calls for. The stub-driven cases below -- below the
// floor, an unparsable line, an empty one, a hostile one, an in-range one and a
// degraded one -- are about the *document a version calls for*, not about the
// resolution, so since Task 3.4b they run against a second scratch profile
// seeded with an active installation naming the stub: the handshake reads that
// binary and no other, and they run on any machine (review S3).
//
// Nothing here reaches the network and nothing here runs a real `fugaro` read:
// the stub answers `version` and refuses every other argv, so a console that
// asked it for anything else would fail here rather than pass on an answer it
// should not have been able to get.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { APP_ENTRY_URL, REFUSAL_ENTRY_URL } from '../../src/main/protocol'
import { CONSOLE_WINDOW_TITLE } from '../../src/main/window'
import { CLI_VERSION_BUILT_AGAINST, SKEW_HEADLINES, classifyCliVersion } from '../../src/shared/version'
import type { CliCompatibilityMode } from '../../src/shared/version'
import { installCliStub, resolveConsoleBinary } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { expectNoFirstRunPicker } from '../support/firstRun'
import { launchConsole } from '../support/launch'
import type { BinaryResolution } from '../../src/main/spawn'

// The console's own directory, from this file's location, as in the other
// specs: `test/e2e/x.spec.ts` is two levels below it, and Playwright loads
// these specs as CommonJS.
const consoleDir = join(__dirname, '..', '..')

/** withoutComments is a bundled document's bytes, with the prose taken out:
 * every marker below is read from markup, and a marker that could match a
 * comment would be a marker that matches nothing on screen. */
function withoutComments(file: string): string {
  return readFileSync(join(consoleDir, 'src', 'renderer', file), 'utf8').replace(/<!--[\s\S]*?-->/g, '')
}

/**
 * sentenceMarker is the sentence a bundled document carries, **read out of the
 * document itself** rather than retyped here.
 *
 * That is the fix for a real defect in the first version of this file: the
 * dashboard's sentence was retyped with a typographic apostrophe ("console’s")
 * where `index.html` has a straight one ("console's"), so `toContain` failed on
 * the one test that always runs and -- far worse -- every `not.toContain` built
 * on the same constant was true for the wrong reason. The negative half is the
 * D7 claim ("a refused version never draws a dashboard"), so a marker that
 * cannot match would have made the claim unasserted while the suite stayed
 * green. Reading the bytes is what makes the two sides the same string.
 */
function sentenceMarker(file: string): string {
  const paragraph = /<main>[\s\S]*?<p>([^<]+)<\/p>/.exec(withoutComments(file))
  const sentence = paragraph?.[1]?.trim()
  if (sentence === undefined || sentence === '') {
    throw new Error(`${file} carries no <p> in its <main>: this spec's markers are read from the documents`)
  }
  return sentence
}

/** entryScriptOf is the one script a bundled document loads, or null when it
 * loads none -- read from the bytes, for the same reason `sentenceMarker` is. */
function entryScriptOf(file: string): string | null {
  return /<script[^>]*\bsrc="([^"]+)"/.exec(withoutComments(file))?.[1] ?? null
}

/** REFUSAL_MARKER is the sentence `src/renderer/refusal.html` carries: the whole
 * of what a refused console shows, and the half of the pair that is still a
 * sentence on screen. */
const REFUSAL_MARKER = sentenceMarker('refusal.html')

/**
 * DASHBOARD_MARKER is what `src/renderer/index.html` carries and the refusal
 * document does not: **the one script it loads**.
 *
 * It was the dashboard's placeholder sentence ("The console's window is up. It
 * has no data yet.") until Task 3.4c, and it cannot be a sentence any more. That
 * task emptied the dashboard document's body so the entry could fill it: what
 * the dashboard says on screen is now the entry's -- a later task's renderer
 * reading over IPC -- and not the document's, so a sentence read from
 * `index.html` would have been a sentence the window does not show. What
 * survives, and is a stronger claim than the sentence was, is the difference the
 * two documents exist for: this one runs the console's entry, and the refusal
 * document runs nothing at all. `test/unit/csp-document.test.ts` holds both
 * halves of that (exactly one `./console.js` in this document, no script element
 * in the other), and the string is read from the bytes here, so the marker the
 * assertions use cannot drift from the document that carries it.
 */
const DASHBOARD_MARKER = entryScriptOf('index.html')
if (DASHBOARD_MARKER === null) {
  throw new Error(
    'src/renderer/index.html loads no script: this spec has no marker for the dashboard document',
  )
}

/** HOSTILE is the line the plan asks for: an ANSI escape, a markup tag and a
 * version-shaped run, in one line a binary chose. Nothing of it may be live on
 * screen, and nothing of it may reach the window's title. */
const HOSTILE = '\u001b[31m0.8.1\u001b[0m <h1>fugaro says hello</h1>'

interface Launched {
  readonly app: ElectronApplication
  readonly page: Page
}

/** launch starts the app the way a user starts it, with the stub's environment
 * (or the machine's, when a case wants the machine's own binary). */
async function launch(env: Record<string, string>): Promise<Launched> {
  // A scratch profile with no installation (Task 3.4b): this spec's subject is
  // the first run's handshake, rule 4's resolution with nothing chosen, and
  // since the owner's ruling of 2026-10-25 that run opens no picker beside the
  // window under test (`expectNoFirstRunPicker`).
  const app = await launchConsole({ args: stub.launchArgs(consoleDir), env })
  const page = await app.firstWindow()
  await page.waitForLoadState('domcontentloaded')
  await expectNoFirstRunPicker(app, page)
  return { app, page }
}

/** launchSeeded starts the app against the seeded profile: one active
 * installation naming `seeded`'s stub, so the handshake reads exactly that
 * binary and no picker opens. */
async function launchSeeded(): Promise<Launched> {
  const app = await launchConsole({ args: seeded.launchArgs(consoleDir), env: seeded.env })
  const page = await app.firstWindow()
  await page.waitForLoadState('domcontentloaded')
  return { app, page }
}

/** windowTitles is what the operating system's chrome was told. It is read in
 * the main process, because a window title is drawn by the OS and not by this
 * console's writers. */
function windowTitles(app: ElectronApplication): Promise<string[]> {
  return app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map((one) => one.getTitle()))
}

/** pageShape is the loaded document, read from inside the frame: its URL, its
 * text, the script it runs and the shapes a dashboard view would need. */
function pageShape(
  page: Page,
): Promise<{ url: string; text: string; entryScript: string | null; dataShapes: number }> {
  return page.evaluate(() => ({
    url: window.location.href,
    text: document.body.innerText,
    // Which document this is, as `DASHBOARD_MARKER` defines it: the dashboard
    // document is the one that runs the entry, and the refusal document runs no
    // script at all. Read from the page rather than from the URL, so a window
    // that loaded the dashboard's URL and drew the refusal's document would fail
    // here.
    entryScript: document.querySelector('script[src]')?.getAttribute('src') ?? null,
    // Every view this console draws is a list, a table, a `<canvas>` or an
    // `<svg>`; the refusal document has none of them, which is D7's claim on the
    // document rather than on the renderer: there is nothing to draw a figure
    // into.
    dataShapes: document.querySelectorAll('ul, ol, table, canvas, svg').length,
  }))
}

let stub: CliStub
let winner: BinaryResolution
/** seeded is the stub the version cases read, through an active installation. */
let seeded: CliStub

test.describe('the startup handshake, in the real app', () => {
  test.describe.configure({ mode: 'serial' })

  test.beforeAll(async () => {
    stub = installCliStub(CLI_VERSION_BUILT_AGAINST)
    winner = await resolveConsoleBinary(stub.env)
    seeded = installCliStub(CLI_VERSION_BUILT_AGAINST)
    seeded.seedActiveInstallation('e2e')
  })

  test.afterAll(() => {
    stub?.remove()
    seeded?.remove()
  })

  test('opens exactly one window, on the document the fugaro it found calls for', async () => {
    // The case that always runs, whichever binary answers. It is the wiring
    // claim in one test: the app resolved a binary, made a decision, and opened
    // exactly one window on the document that decision calls for -- and the
    // window's title says which console this is.
    const { app, page } = await launch(stub.env)
    try {
      expect(app.windows()).toHaveLength(1)
      const mode: CliCompatibilityMode = winner.ok ? classifyCliVersion(winner.version).mode : 'refused'
      const shape = await pageShape(page)
      expect(await windowTitles(app)).toEqual([
        mode === 'ok'
          ? CONSOLE_WINDOW_TITLE
          : `${CONSOLE_WINDOW_TITLE} — ${SKEW_HEADLINES[mode]}`,
      ])
      if (mode === 'refused') {
        expect(shape.url).toBe(REFUSAL_ENTRY_URL)
        expect(shape.text).toContain(REFUSAL_MARKER)
        expect(shape.entryScript, 'the refused window ran the dashboard’s entry').toBeNull()
        expect(shape.dataShapes).toBe(0)
      } else {
        expect(shape.url).toBe(APP_ENTRY_URL)
        expect(shape.entryScript).toBe(DASHBOARD_MARKER)
        // Both markers, each against the other document: this is the pair that
        // makes the `toBeNull()` above a claim rather than a spelling mistake. A
        // dashboard that carried the refusal's sentence (or a marker that
        // matched nothing) fails here.
        expect(shape.text).not.toContain(REFUSAL_MARKER)
      }
    } finally {
      await app.close()
    }
  })

  test('a stub below the floor shows the refusal document and never the dashboard', async () => {
    seeded.setVersion('0.5.0')
    const { app, page } = await launchSeeded()
    try {
      expect(app.windows()).toHaveLength(1)
      expect(await windowTitles(app)).toEqual([`${CONSOLE_WINDOW_TITLE} — ${SKEW_HEADLINES.refused}`])
      const shape = await pageShape(page)
      expect(shape.url).toBe(REFUSAL_ENTRY_URL)
      expect(shape.url).not.toBe(APP_ENTRY_URL)
      expect(shape.text).toContain(REFUSAL_MARKER)
      expect(shape.entryScript, 'the refused window ran the dashboard’s entry').toBeNull()
      // No list, no table, no canvas: there is nothing in this document a spend
      // figure could be drawn into even if a renderer tried.
      expect(shape.dataShapes).toBe(0)
    } finally {
      await app.close()
    }
  })

  test('a stub with no placeable version is refused too, and draws no dashboard', async () => {
    // The three lines a binary can print that are not a version: a word, an
    // empty line, and a line longer than the console will read. All three are
    // refused rather than "probably fine", which is what rule 4 says about a
    // binary the console cannot identify.
    for (const line of ['banana', '', 'x'.repeat(10_000)]) {
      seeded.setVersion(line)
      const { app, page } = await launchSeeded()
      try {
        const shape = await pageShape(page)
        expect(shape.url, JSON.stringify(line.slice(0, 40))).toBe(REFUSAL_ENTRY_URL)
        expect(
          shape.entryScript,
          `the refused window ran the dashboard's entry on ${JSON.stringify(line.slice(0, 40))}`,
        ).toBeNull()
        // The line itself is nowhere: the refusal document is the console's own
        // sentence, and it never carries what the binary printed. The empty
        // line is the one case this cannot be asked of -- every string contains
        // the empty string, which is what made the first version of this
        // assertion fail on every run -- and it has nothing to draw anyway.
        if (line !== '') {
          expect(shape.text, 'the line is not drawn').not.toContain(line.slice(0, 40))
        }
        expect(shape.text.length).toBeLessThan(1000)
      } finally {
        await app.close()
      }
    }
  })

  test('a hostile line leaves nothing live on the screen, or in the title', async () => {
    seeded.setVersion(HOSTILE)
    const { app, page } = await launchSeeded()
    try {
      const shape = await pageShape(page)
      expect(shape.url).toBe(REFUSAL_ENTRY_URL)
      // The escape sequence, the markup and the version-shaped run: none of them
      // is in the document's text, and no markup element was created from the
      // line (the tag is text, or it is nothing -- never an element).
      expect(shape.text).not.toContain('\u001b')
      expect(shape.text).not.toContain('[31m')
      expect(shape.text).not.toContain('fugaro says hello')
      expect(shape.text).not.toContain('<h1>')
      expect(shape.text).not.toContain('0.8.1')
      expect(shape.text).not.toContain(HOSTILE)
      // The markers are the other half: the page is the refusal document, so the
      // absences above are about a page that drew something, not an empty one.
      expect(shape.text).toContain(REFUSAL_MARKER)
      expect(await page.evaluate(() => document.querySelectorAll('h1').length)).toBe(1)
      // The title is drawn by the operating system and not by the console's
      // writers, so the console puts no binary-chosen string in it at all --
      // which is the reason `consoleWindowTitle` carries the mode and never the
      // version.
      const titles = await windowTitles(app)
      expect(titles).toEqual([`${CONSOLE_WINDOW_TITLE} — ${SKEW_HEADLINES.refused}`])
    } finally {
      await app.close()
    }
  })

  test('a stub inside the range shows the dashboard document', async () => {
    seeded.setVersion(CLI_VERSION_BUILT_AGAINST)
    const { app, page } = await launchSeeded()
    try {
      expect(app.windows()).toHaveLength(1)
      expect(await windowTitles(app)).toEqual([CONSOLE_WINDOW_TITLE])
      const shape = await pageShape(page)
      expect(shape.url).toBe(APP_ENTRY_URL)
      expect(shape.entryScript).toBe(DASHBOARD_MARKER)
    } finally {
      await app.close()
    }
  })

  test('a stub in the degraded band opens the dashboard, and says which console it is', async () => {
    seeded.setVersion('0.9.0')
    const { app, page } = await launchSeeded()
    try {
      expect(app.windows()).toHaveLength(1)
      // The reduced mode is a console, so the dashboard document loads -- and the
      // window's title says which console this is and what it made of the CLI,
      // which is design section 3.4 rule 8 ("the console knows which CLI it is
      // driving, and says so in its header") read as far as this tree can carry
      // it: the header is the title. What the *document* draws for a reduced
      // mode -- the banner, and no withheld view -- is the renderer's, and since
      // Task 3.4a the dashboard document loads the bundled entry, so it is drawn
      // in the app's own document: `entry.spec.ts` asserts the banner, its
      // headline and the three hidden views from inside the page, and
      // `skew.spec.ts` asserts `renderVersionSkew` itself, injected into the same
      // document. This spec stays what it was -- the wiring, and the title.
      expect(await windowTitles(app)).toEqual([`${CONSOLE_WINDOW_TITLE} — ${SKEW_HEADLINES.reduced}`])
      expect((await pageShape(page)).url).toBe(APP_ENTRY_URL)
    } finally {
      await app.close()
    }
  })
})
