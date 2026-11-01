// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The renderer entry, in a real Electron window (desktop-console plan Task 3.4a,
// steps 1, 2, 4 and 5; design section 5.1 rules 1, 3, 5 and D7).
//
// Everything this file asserts is a claim about a *running* app, and each of
// them is here rather than in the unit suite for the same reason: the unit
// suite proves the parts (`test/unit/bundle.test.ts` runs the bundled entry in a
// `vm` realm, `test/unit/csp-document.test.ts` reads the served documents,
// `test/unit/preload-surface.test.ts` runs the built preload against a recording
// loader) and none of those is a browser. Chromium is what enforces the CSP,
// `contextBridge` is what copies the bridge into the renderer's world, and the
// `app://` handler is what serves the one script the document loads. So this is
// where those three become facts:
//
//   - **the dashboard document loads the bundled entry from its own origin.**
//     One `<script>`, resolved to `app://console/console.js`, and what is on
//     `window` afterwards is the preload's frozen bridge -- which is the only
//     way the renderer can ask anything, and proof that both the preload and the
//     script ran.
//   - **nothing else is on `window`.** No `require`, no `process`, no
//     `ipcRenderer`, and no way to name a channel. `sandbox: true` and
//     `contextIsolation: true` are the flags; this is what they mean from inside
//     the page.
//   - **the CSP is live.** An inline `<script>` injected into the page does not
//     run and raises a `securitypolicyviolation` -- a claim only a real browser
//     can make, and the one that would go on passing if the policy were only a
//     `<meta>` tag nobody enforced.
//   - **a refused console loads the other document**, which has no script at
//     all and no bridge, and **no handler is behind any channel** (D7 at the
//     document level; `test/unit/startup.test.ts` pins the wiring that keeps the
//     surface off `ipcMain`).
//   - **a reduced console loads the dashboard, draws the banner, and refuses a
//     withheld read** with the console's own sentence -- over real IPC, through
//     the real preload, in a real window.
//   - **the spawn log holds the handshake, the live watch and the Live
//     screen's rigor join, and no other read.** The log is the one place a spawn
//     that should not have happened is visible, and a healthy console makes three
//     kinds: the handshake's own `version` probe, the Live screen's `watch
//     --json`, and the Live screen's rigor join (`runs ls --json`, re-read at
//     most once per `LIVE_RIGOR_TTL_MS`), each planned with the chosen
//     installation's binary and `--project`. A refused console mounts nothing,
//     so its log is the probe alone.
//   - **the Live screen draws the synthetic recording.** The whole chain in one
//     window: the entry mounts Live, the poll asks `live.watch`, the main
//     process spawns the installation's own `fugaro watch --json`, and the
//     fixture's repository and runs appear in the page -- drawn by the one
//     script the document loaded, with no code injected into the window.
//   - **a run row is answered in the frame it was clicked in.** The click and
//     the reading of the panel happen in one task, while the run's two reads are
//     real IPC calls that cannot answer inside it: the run screen's frame, both
//     of its sections marked busy, is what the click itself draws.
//
// **The premise (Task 3.4b; review S3).** Every launch here runs against a
// scratch `--user-data-dir` seeded with one **active installation naming the
// stub**, so the startup handshake reads that binary and no other
// (`resolveHandshakeBinary`) -- whatever `fugaro` the machine has in
// `/usr/local/bin` or `/opt/homebrew/bin`. Two cases are the exception, on
// purpose: the two that assert which binary a run with **no usable stored path**
// drives seed a record whose path has gone, or one with no path at all, so rule
// 4 walks the well-known prefixes and the machine decides. Those two ask the
// console's own resolver first (`resolveConsoleBinary`) and skip when this
// machine's answer is not one the console can drive -- a skip that says which
// `fugaro` answered rather than failing on a fact about the machine. The one
// first-run case uses an unseeded profile of its own and asserts nothing about
// which `fugaro` the machine resolves.
//
// **Drawn, never raw.** The header's line is `src/renderer/entry.ts`'s `textOf`,
// which is `sanitizeRefusal`: the path the console draws is the sanitized one,
// while the same value arrives raw over IPC. A scratch stub directory's random
// suffix reads as a token and is redacted, so `/tmp/fugaro-cli-stub-Ab3xY9/...`
// is drawn as `[redacted-token]...`. The two cases above therefore compare the
// header against `sanitizeRefusal(drove, ...).text` and keep the raw `drove` for
// the spawn log and the IPC-level assertions -- comparing the drawn line to the
// raw path passes only where the resolved binary happens to live outside a
// scratch directory.
//
// Nothing here reaches the network and nothing here starts a child that the
// console's own read would not have started: the stub answers `version`, and
// answers `watch`/`doctor` **only** for the seeded project -- any other argv
// exits 2 -- so a console that asked it for something else, or asked for the
// right thing about the wrong project, fails here rather than passing on an
// answer it should not have been able to get. `watch` prints
// `test/fixtures/synthetic.ndjson`, so what the Live screen draws is a committed
// recording rather than a shape invented in the spec.

import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { INSTALLATIONS_FILE_NAME } from '../../src/main/installations'
import { NO_ACTIVE_INSTALLATION, REDUCED_REFUSAL } from '../../src/main/ipc'
import { APP_ENTRY_URL, APP_ORIGIN, REFUSAL_ENTRY_URL, SERVED_FILES } from '../../src/main/protocol'
import { SPAWN_LOG_ENV } from '../../src/main/spawnlog'
import type { SpawnLogEntry } from '../../src/main/spawnlog'
import {
  CLI_VERSION_BUILT_AGAINST,
  REDUCED_VIEWS,
  SKEW_HEADLINES,
  classifyCliVersion,
} from '../../src/shared/version'
import { EXPOSED_OPERATIONS } from '../../src/preload/index'
import { REDUCED_CHIP_LABEL, REDUCED_CHIP_REASON_ID } from '../../src/renderer/entry'
import { installCliStub, resolveConsoleBinary } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { expectNoFirstRunPicker } from '../support/firstRun'
import { launchConsole } from '../support/launch'
import { INSTALLATIONS_MENU_LABEL, PICKER_ENTRY_URL } from '../../src/main/picker'
import { sanitizeRefusal } from '../../src/shared/sanitize'

// The console's own directory, from this file's location, as in the other specs:
// `test/e2e/x.spec.ts` is two levels below it, and Playwright loads these specs
// as CommonJS.
const consoleDir = join(__dirname, '..', '..')

/**
 * SHIPPED_KEYS is what `window.fugaro` must carry, read out of the preload's own
 * table: the twenty-two methods, plus `version` and `operations`. Read rather than
 * spelled, because the claim here is "the bridge in the page is the surface this
 * console ships" -- a retyped list would only prove the retyping.
 */
const SHIPPED_KEYS: readonly string[] = [
  'operations',
  'version',
  ...EXPOSED_OPERATIONS.map(([, method]) => method),
].sort()

interface Launched {
  readonly app: ElectronApplication
  readonly page: Page
  readonly spawnLog: string
  /** scratch is the directory the spawn log lives in, removed by `close`. */
  readonly scratch: string
  /** records is the spawn log as parsed records, read *after* the handshake:
   * the read that produced them is what opened and closed it. */
  records(): readonly SpawnLogEntry[]
}

/**
 * launch starts the app the way a user starts it, with the stub's environment
 * and one addition: `FUGARO_CONSOLE_SPAWN_LOG` names a scratch file, which is
 * the operator's own opt-in and the only way the spawn log is ever written
 * (design section 5.1 rule 10).
 */
async function launch(
  stubEnv: Record<string, string>,
  options: { readonly firstRun?: CliStub; readonly env?: Record<string, string> } = {},
): Promise<Launched> {
  const scratch = mkdtempSync(join(tmpdir(), 'fugaro-console-entry-'))
  const spawnLog = join(scratch, 'spawn.jsonl')
  // The seeded profile (see the header), or -- for the first-run case -- an
  // unseeded one, which opens no picker beside the window under test
  // (`expectNoFirstRunPicker`, owner's design ruling of 2026-10-25).
  const profile = options.firstRun ?? stub
  const app = await launchConsole({
    args: profile.launchArgs(consoleDir),
    env: { ...stubEnv, ...(options.env ?? {}), [SPAWN_LOG_ENV]: spawnLog },
  })
  // A console that exits before it opens a window would leave `firstWindow`
  // waiting until the test's own timeout; this says so, with the exit code.
  const page = await app.firstWindow({ timeout: 15_000 }).catch((error: unknown) => {
    throw new Error(
      `the console opened no window (exit code ${String(app.process().exitCode)}): ${error instanceof Error ? error.message : String(error)}`,
    )
  })
  await page.waitForLoadState('domcontentloaded')
  if (options.firstRun !== undefined) await expectNoFirstRunPicker(app, page)
  return {
    app,
    page,
    spawnLog,
    scratch,
    records(): readonly SpawnLogEntry[] {
      return readSpawnLog(spawnLog)
    },
  }
}

/** readSpawnLog parses the log at `path`, and answers an empty list when there
 * is no file: unset, unreadable, never written -- the honest answer, and why the
 * specs that need records assert a length. It is read *after* the handshake,
 * because the read that produced the records is what opened and closed the file.
 * The specs beside `launch` use it directly, since they hold the app rather than
 * the wrapper. */
function readSpawnLog(path: string): readonly SpawnLogEntry[] {
  let text: string
  try {
    text = readFileSync(path, 'utf8')
  } catch {
    return []
  }
  return text
    .split('\n')
    .filter((line) => line.trim() !== '')
    .map((line) => JSON.parse(line) as SpawnLogEntry)
}

/** close ends the app and the scratch tree beside it. */
async function close(launched: Launched): Promise<void> {
  await launched.app.close()
  rmSync(launched.scratch, { recursive: true, force: true })
}

/** pageShape is the document as the renderer's own world sees it: the URL, the
 * one script element and where its `src` resolves to, what is on `window`, and
 * the banner if there is one. Everything here is read *from inside the page*,
 * which is the whole point -- the main process can describe what it built, and
 * only the page can say what a renderer actually got. */
function pageShape(page: Page): Promise<{
  url: string
  scripts: readonly string[]
  scriptUrls: readonly string[]
  bridgeKeys: readonly string[]
  bridgeFrozen: boolean
  bridgeVersion: number | null
  bridgeOperations: readonly string[]
  globals: Record<string, string>
  mode: string | null
  bannerHeadline: string | null
  hiddenViews: readonly string[]
  bodyChildren: readonly string[]
  bodyText: string
}> {
  return page.evaluate(() => {
    const bridge = (window as unknown as { fugaro?: Record<string, unknown> }).fugaro
    const banner = document.querySelector('section.console-skew')
    // Class-only on purpose: the title is an `h1` on the refusal screen and an
    // `h2` on a reduced console's card, and a spec that spelled the tag would
    // read one of the two and call the other a missing headline.
    const headline = document.querySelector('.skew-headline')
    const globals: Record<string, string> = {}
    for (const name of ['require', 'process', 'module', 'ipcRenderer', 'Buffer', 'global']) {
      globals[name] = typeof (window as unknown as Record<string, unknown>)[name]
    }
    return {
      url: window.location.href,
      scripts: [...document.querySelectorAll('script')].map((element) => element.outerHTML),
      scriptUrls: [...document.querySelectorAll('script')].map((element) => element.src),
      bridgeKeys: bridge === undefined ? [] : Object.keys(bridge).sort(),
      bridgeFrozen: bridge !== undefined && Object.isFrozen(bridge),
      bridgeVersion: bridge === undefined ? null : (bridge['version'] as number),
      bridgeOperations:
        bridge === undefined ? [] : [...(bridge['operations'] as readonly string[])],
      globals,
      mode: banner?.getAttribute('data-mode') ?? null,
      bannerHeadline: headline?.textContent ?? null,
      hiddenViews: [...document.querySelectorAll('li.skew-hidden')].map(
        (element) => element.getAttribute('data-hidden') ?? '',
      ),
      // What the document's body holds, which is the entry's own drawing and
      // nothing else: the placeholder Task 1.1's document carried would be a
      // child here that no screen of this console draws.
      bodyChildren: [...document.body.children].map(
        (element) => element.getAttribute('class') ?? element.tagName.toLowerCase(),
      ),
      bodyText: document.body.textContent ?? '',
    }
  })
}

/**
 * openPickerFromMenu clicks the application menu's `Installations…` item -- the
 * one door to the picker (C9) -- and answers the picker's page.
 */
async function openPickerFromMenu(app: ElectronApplication): Promise<Page> {
  const opened = app.waitForEvent('window')
  const clicked = await app.evaluate(({ Menu }, label) => {
    const menu = Menu.getApplicationMenu()
    for (const top of menu?.items ?? []) {
      for (const item of top.submenu?.items ?? []) {
        if (item.label === label) {
          item.click()
          return true
        }
      }
    }
    return false
  }, INSTALLATIONS_MENU_LABEL)
  expect(clicked, `the application menu has no ${INSTALLATIONS_MENU_LABEL} item`).toBe(true)
  const picker = await opened
  await picker.waitForLoadState('load')
  expect(picker.url()).toBe(PICKER_ENTRY_URL)
  return picker
}

/** installationOf is the dashboard's `installation.current`, read through the
 * real preload from inside the page. */
function installationOf(page: Page): Promise<Record<string, unknown>> {
  return page.evaluate(async () => {
    const api = (window as unknown as { fugaro: { getInstallation: () => Promise<unknown> } }).fugaro
    return (await api.getInstallation()) as Record<string, unknown>
  })
}

let stub: CliStub

test.describe('the renderer entry, in the real window', () => {
  test.describe.configure({ mode: 'serial' })

  test.beforeAll(() => {
    stub = installCliStub(CLI_VERSION_BUILT_AGAINST)
    stub.seedActiveInstallation('e2e')
  })

  test.afterAll(() => {
    stub?.remove()
  })

  test('loads the bundled entry from the console’s own origin, and the frozen bridge with it', async () => {
    stub.setVersion(CLI_VERSION_BUILT_AGAINST)
    const launched = await launch(stub.env)
    try {
      const shape = await pageShape(launched.page)
      // The document, then the one script the document is allowed to load. The
      // resolved `src` is the assertion that matters: it is same-origin, it is
      // the file `SERVED_FILES` lists, and it is not a `file:` or `data:` URL.
      expect(shape.url).toBe(APP_ENTRY_URL)
      expect(shape.scripts).toHaveLength(1)
      expect(shape.scripts[0]).toContain('src="./console.js"')
      expect(shape.scripts[0]).toContain('defer')
      // The resolved `src`: same-origin, and the file the served root lists.
      // `APP_ORIGIN` rather than `new URL(...).origin`, which is the string
      // `'null'` for a scheme the URL standard does not treat as special.
      expect(shape.scriptUrls).toEqual([`${APP_ORIGIN}/console.js`])
      expect(SERVED_FILES).toContain('/console.js')
      // The bridge `src/preload/index.ts` exposes, as the renderer received it:
      // the twenty-two methods and the two fields, frozen, at the shipped version.
      // Two files have to be right for this to hold -- the preload that exposed
      // it and the entry that ran beside it -- because the preload only reaches
      // the dashboard window (`dashboardWebPreferences`).
      expect(shape.bridgeKeys).toEqual(SHIPPED_KEYS)
      expect(shape.bridgeFrozen).toBe(true)
      expect(shape.bridgeVersion).toBe(1)
      expect([...shape.bridgeOperations].sort()).toEqual(
        EXPOSED_OPERATIONS.map(([name]) => name).sort(),
      )
    } finally {
      await close(launched)
    }
  })

  test('gives the renderer nothing but that one object', async () => {
    stub.setVersion(CLI_VERSION_BUILT_AGAINST)
    const launched = await launch(stub.env)
    try {
      const shape = await pageShape(launched.page)
      // Every Node-shaped global is absent from the page's world. This is what
      // `sandbox: true` and `contextIsolation: true` buy, and it is read in the
      // renderer rather than trusted from the window's options: the unit suite
      // pins the options (`test/unit/window.test.ts`) and only a browser can say
      // what they left behind.
      for (const [name, kind] of Object.entries(shape.globals)) {
        expect(kind, `window.${name} is ${kind}`).toBe('undefined')
      }
      // And the bridge itself carries no generic escape hatch: no `invoke` to
      // point at a channel, no listener registrar, nothing that takes a channel
      // name.
      const bridge = (await launched.page.evaluate(() => {
        const api = (window as unknown as { fugaro?: Record<string, unknown> }).fugaro
        const found: string[] = []
        for (const forbidden of [
          'invoke',
          'send',
          'sendSync',
          'on',
          'once',
          'addListener',
          'removeListener',
          'postMessage',
        ]) {
          if (api !== undefined && forbidden in api) found.push(forbidden)
        }
        return found
      })) as readonly string[]
      expect(bridge).toEqual([])
    } finally {
      await close(launched)
    }
  })

  test('does not run an inline script, and says so through the CSP', async () => {
    stub.setVersion(CLI_VERSION_BUILT_AGAINST)
    const launched = await launch(stub.env)
    try {
      // The claim the policy is for. An inline `<script>` appended to the page is
      // refused by `script-src 'self'` -- no `'unsafe-inline'` -- and Chromium
      // reports the refusal rather than failing silently, so this is asserted
      // twice: the injected code did not run, and a `securitypolicyviolation`
      // fired naming `script-src`. A `<meta>`-only policy that nobody enforced,
      // or a policy that had gained `'unsafe-inline'`, fails here.
      const violation = await launched.page.evaluate(
        () =>
          new Promise<string>((resolve) => {
            const timer = setTimeout(() => {
              resolve('no violation')
            }, 5000)
            document.addEventListener(
              'securitypolicyviolation',
              (event) => {
                clearTimeout(timer)
                resolve(event.violatedDirective)
              },
              { once: true },
            )
            const element = document.createElement('script')
            element.textContent = 'globalThis.__inlineRan = true'
            document.body.appendChild(element)
          }),
      )
      expect(violation).toContain('script-src')
      expect(
        await launched.page.evaluate(
          () => typeof (globalThis as unknown as { __inlineRan?: unknown }).__inlineRan,
        ),
      ).toBe('undefined')
    } finally {
      await close(launched)
    }
  })

  test('a refused console loads the refusal document: no script, and no bridge', async () => {
    stub.setVersion('0.5.0')
    const launched = await launch(stub.env)
    try {
      expect(launched.app.windows()).toHaveLength(1)
      const shape = await pageShape(launched.page)
      expect(shape.url).toBe(REFUSAL_ENTRY_URL)
      expect(shape.url).not.toBe(APP_ENTRY_URL)
      // The two halves of step 1 and step 2 read from the page: the refusal
      // window has no preload, so there is no bridge for it to reach, and the
      // document loads no script at all -- so even a handler that had been
      // registered has no code here that could call it.
      expect(shape.scripts).toEqual([])
      expect(shape.bridgeKeys).toEqual([])
      expect(shape.globals['require']).toBe('undefined')
      // **And the log holds no read.** A refused console drives nothing: the
      // handshake's own `version` probe is the one spawn for the installation,
      // and no screen's read follows it (Task 3.4c -- a console that mounted a
      // screen here would have to read through a source it never built). The
      // rail's boot read comes before the handshake and names no project
      // (`version` to find the binary, then `config list --json`).
      const records = launched.records()
      expect(
        records.filter((record) => record.project === '').map((record) => record.argv.slice(1)),
      ).toEqual([['version'], ['config', 'list', '--json']])
      expect(
        records.filter((record) => record.project !== '').map((record) => record.argv.slice(1)),
      ).toEqual([['version']])
    } finally {
      await close(launched)
    }
  })

  test('a reduced console draws the banner and refuses a withheld read over real IPC', async () => {
    stub.setVersion('0.9.0')
    const launched = await launch(stub.env)
    try {
      const shape = await pageShape(launched.page)
      // The dashboard document, with the renderer's own drawing in it: the
      // bundled entry ran, asked the handshake, and drew the banner. This is the
      // one place the *bundle* is proven to execute in a browser -- the unit
      // suite runs it in a `vm` realm, and this is the real document.
      expect(shape.url).toBe(APP_ENTRY_URL)
      expect(shape.mode).toBe('reduced')
      expect(shape.bannerHeadline).toBe(SKEW_HEADLINES.reduced)
      expect([...shape.hiddenViews].sort()).toEqual([...REDUCED_VIEWS].sort())
      // The withheld read, over the real preload and the real registrar: the
      // handler refuses *before* the source, the source refuses everything
      // anyway, and the sentence is the console's own.
      const spend = (await launched.page.evaluate(async () => {
        const api = (
          window as unknown as {
            fugaro: { runReport: (args: unknown) => Promise<unknown> }
          }
        ).fugaro
        return await api.runReport({})
      })) as { readonly ok: boolean; readonly reason?: string }
      expect(spend).toEqual({ ok: false, reason: REDUCED_REFUSAL })
      // The live feed is *not* withheld (design C10): the kill-switch fields
      // arrive inside it, so a reduced console still reads it. Since Task 3.4c
      // the main process binds a real source to the active installation, so the
      // read reaches the stub's own `watch` and answers with a document -- and
      // that is the claim: the operation is *reachable* in a reduced mode, and
      // what answered it was the installation's `fugaro` and not a gate that had
      // swallowed the read.
      const live = (await launched.page.evaluate(async () => {
        const api = (
          window as unknown as {
            fugaro: { subscribeLive: (args: unknown) => Promise<unknown> }
          }
        ).fugaro
        return await api.subscribeLive({})
      })) as { readonly ok: boolean; readonly reason?: string; readonly data?: unknown }
      expect(live.ok, `live.watch was refused in a reduced mode: ${String(live.reason)}`).toBe(true)
      expect(live.reason, 'live.watch must not be withheld by the reduced mode').toBeUndefined()
      const snapshot = live.data as {
        readonly document?: { readonly repos?: readonly { readonly repo: string }[] }
        readonly receivedAt?: unknown
      }
      // The committed synthetic frame, which is what the stub's `watch` prints.
      expect(snapshot.document?.repos?.[0]?.repo, 'the live snapshot carries no document').toBe(
        'r-4c25cf20/r-f1aa9214',
      )
      expect(typeof snapshot.receivedAt, 'the snapshot carries no arrival stamp').toBe('string')

      // And the navigation offers Live, Graph and Coordinator: Spend is the
      // withheld view, and Projects is the `caps` view, which this mode
      // withholds with it. A Spend entry here would be a door to a screen whose
      // read the gate refuses. The Graph is offered because `plan.status` is
      // not a view this mode withholds: it draws no spend and no caps.
      // Coordinator is not withheld either: its read is repository state and
      // not money (design section 5.1 rule 5, `VIEWS` in
      // `src/renderer/entry.ts`).
      const nav = await launched.page.evaluate(() =>
        [...document.querySelectorAll('nav.console-views button.console-view')].map((button) =>
          button.getAttribute('data-view'),
        ),
      )
      expect(nav).toEqual(['live', 'graph', 'coordinator'])
      // And nothing behind the withheld views was read: the spawn log holds the
      // handshake's `version` and the live `watch`, and no `report`.
      const reducedLog = launched.records()
      expect(reducedLog.filter((record) => record.argv.includes('report'))).toEqual([])
      expect(reducedLog.filter((record) => record.argv.includes('budget'))).toEqual([])
      // The header's read says reduced too, with the version the startup
      // handshake read -- never a default `ok` (review S1) -- and the dashboard
      // draws it.
      const installation = await installationOf(launched.page)
      expect(installation).toEqual({
        ok: true,
        data: {
          chosen: true,
          id: '0123456789abcdef',
          project: 'e2e',
          binary: join(stub.dir, 'fugaro'),
          cliVersion: '0.9.0',
          mode: 'reduced',
        },
      })
      const header = await launched.page.evaluate(() => {
        const section = document.querySelector('section.console-installation')
        return {
          kind: section?.getAttribute('data-installation') ?? null,
          lines: [...(section?.querySelectorAll('p') ?? [])].map((line) => line.textContent),
        }
      })
      // The binary line is the path **as written**: a filesystem path is not a
      // token, and the scratch tree's `mkdtemp` suffix -- six characters mixing
      // case and digits -- is exactly what a header that redacted it would be
      // redacting. That is the owner's report of 2026-10-21, and
      // `sanitize-path-runs.test.ts` holds the sanitizer's half of it; the
      // screen's half is here, and it is an exact line rather than a redacted
      // one, so a return of `[redacted-token]` fails this spec.
      const binary = join(stub.dir, 'fugaro')
      const drawnBinary = sanitizeRefusal(binary, 'the installation’s binary')
      expect(drawnBinary.ok ? drawnBinary.text : null).toBe(binary)
      expect(header).toEqual({
        kind: 'chosen',
        lines: ['e2e', binary, '0.9.0 · reduced'],
      })
      // And the picker's status line, opened from the menu.
      const picker = await openPickerFromMenu(launched.app)
      await expect(picker.locator('#picker-status')).toHaveText('e2e is the installation this console is showing (reduced).')
    } finally {
      await close(launched)
    }
  })

  test('a reduced console puts the card away, and the header chip keeps saying why', async () => {
    // The dismissal and the chip, in the real window: design section 2.14 makes
    // the card dismissible **because** the header says the mode for as long as
    // the mode is, and the two are one mechanism -- the chip is what un-hides
    // the card, and the card is what the chip stands for.
    stub.setVersion('0.9.0')
    const launched = await launch(stub.env)
    try {
      const { page } = launched
      await expect(page.locator('button.console-reduced-chip')).toHaveCount(1)
      const chip = await page.evaluate(() => {
        const button = document.querySelector('button.console-reduced-chip')
        const describedBy = button?.getAttribute('aria-describedby') ?? null
        const reason = describedBy === null ? null : document.getElementById(describedBy)
        return {
          text: button?.textContent ?? null,
          severity: button?.getAttribute('data-severity') ?? null,
          title: button?.getAttribute('title') ?? null,
          describedBy,
          reason: reason?.textContent ?? null,
          // Not `aria-hidden`: the chip's own word is two words long, and the
          // headline is what a reader who only reaches the chip is owed.
          reasonHidden: reason?.getAttribute('aria-hidden') ?? null,
        }
      })
      expect(chip).toEqual({
        text: REDUCED_CHIP_LABEL,
        severity: 'warning',
        title: SKEW_HEADLINES.reduced,
        describedBy: REDUCED_CHIP_REASON_ID,
        reason: SKEW_HEADLINES.reduced,
        reasonHidden: null,
      })
      // Escape is not a way out: the card is not a dialog and carries no key
      // handling of its own.
      await page.keyboard.press('Escape')
      const afterEscape = await page.evaluate(() => ({
        hidden: document.querySelector('section.console-skew')?.hasAttribute('hidden') ?? null,
      }))
      expect(afterEscape, 'Escape dismissed the notice').toEqual({ hidden: false })

      // The dismissal is the session's and nothing else: the card is `hidden`,
      // it stays the body's first child, the chip keeps its place, and the
      // keyboard moves to the control that brings the card back.
      await page.locator('button.notice-dismiss').click()
      const dismissed = await pageShape(page)
      expect(dismissed.bodyChildren[0]).toBe('notice console-skew')
      const putAway = await page.evaluate(() => ({
        hidden: document.querySelector('section.console-skew')?.hasAttribute('hidden') ?? null,
        chips: document.querySelectorAll('button.console-reduced-chip').length,
        focused: document.activeElement === document.querySelector('button.console-reduced-chip'),
      }))
      expect(putAway).toEqual({ hidden: true, chips: 1, focused: true })
      // And Escape does not bring it back either: the chip is a button, and a
      // button is activated by a click or by Enter and Space.
      await page.keyboard.press('Escape')
      expect(
        await page.evaluate(() => document.querySelector('section.console-skew')?.hasAttribute('hidden') ?? null),
        'Escape un-hid the notice',
      ).toBe(true)

      // Activating the chip hands the card back, with the keyboard on it.
      await page.locator('button.console-reduced-chip').click()
      const restored = await page.evaluate(() => {
        const title = document.querySelector('.skew-headline')
        return {
          hidden: document.querySelector('section.console-skew')?.hasAttribute('hidden') ?? null,
          focused: document.activeElement === title,
          tabindex: title?.getAttribute('tabindex') ?? null,
        }
      })
      expect(restored).toEqual({ hidden: false, focused: true, tabindex: '-1' })

      // A fresh window draws the card again: the dismissal was the renderer's
      // memory and nothing was stored. A second launch rather than a reload,
      // because the console refuses every navigation the renderer asks for
      // (`denyNavigation`), so "reload" in this app is a new window.
      await page.locator('button.notice-dismiss').click()
    } finally {
      await close(launched)
    }
    const again = await launch(stub.env)
    try {
      // The card is drawn before the boot's `listInstallations` round trip and
      // the header's chip after it, so `domcontentloaded` alone can see the one
      // without the other.
      await expect(again.page.locator('button.console-reduced-chip')).toHaveCount(1)
      const drawn = await again.page.evaluate(() => ({
        hidden: document.querySelector('section.console-skew')?.hasAttribute('hidden') ?? null,
        chips: document.querySelectorAll('button.console-reduced-chip').length,
      }))
      expect(drawn, 'the dismissal outlived the window').toEqual({ hidden: false, chips: 1 })
    } finally {
      await close(again)
    }
  })

  test('the spawn log holds the handshake, the live watch and the rigor join, and no other read, in every mode', async () => {
    // The log is the evidence for what this console drives, and it is read in
    // all three modes. Before the handshake the console makes the installation
    // rail's one boot read (owner's design ruling of 2026-10-25): `config list
    // --json`, after the `version` probe that finds the binary to ask, both with
    // no project because the list is what names one -- in every mode, since the
    // read decides which installation the handshake is for. Then a healthy
    // console makes three kinds of spawn: the
    // handshake's own `version` probe, the Live screen's `watch --json`, and the
    // Live screen's rigor join, `runs ls --json` (the mode on a Live row is
    // joined by run id, since `watch --json` carries no rigor block) -- each
    // planned by the bound source with the installation's `--project` and the
    // installation's binary. A `report`, a `budget`, any other `runs` read or a
    // `diagnose` here is a read some screen made that it should not have, and
    // this is where it is seen.
    for (const [version, mode] of [
      [CLI_VERSION_BUILT_AGAINST, 'ok'],
      ['0.9.0', 'reduced'],
      ['0.5.0', 'refused'],
    ] as const) {
      stub.setVersion(version)
      const launched = await launch(stub.env)
      try {
        const shape = await pageShape(launched.page)
        // The mode is the premise of the claim about the log, so it is asserted
        // rather than assumed: a spec that read the log for a mode the app never
        // reached would be asserting nothing. The *document* is what says which
        // mode this was -- a refused console's document loads no script, so
        // nothing in it draws a banner and there is no `data-mode` to read --
        // and the banner is checked where the renderer is the thing that ran.
        expect(shape.url, `the ${mode} case`).toBe(
          mode === 'refused' ? REFUSAL_ENTRY_URL : APP_ENTRY_URL,
        )
        expect(shape.mode, `the ${mode} case`).toBe(mode === 'reduced' ? 'reduced' : null)
        // A console that refused mounts nothing, so its `watch` is not merely
        // absent from this list -- the list is one line long.
        const expected: readonly string[][] =
          mode === 'refused'
            ? [['version']]
            : [
                ['version'],
                ['watch', '--json', '--project', 'e2e'],
                ['runs', 'ls', '--json', '--since', '24h', '--project', 'e2e'],
              ]
        // The watch is written by the entry's *first poll*, which is a message
        // round and a spawn behind the window's `domcontentloaded` -- so a mode
        // that mounts waits for it rather than racing it. A mode that mounts
        // nothing never writes one, and for that mode the wait is the loop
        // below: one record, the probe.
        if (mode !== 'refused') {
          await expect
            .poll(() => launched.records().some((record) => record.argv[1] === 'watch'), {
              message: `${mode}: the live read never reached the spawn log`,
            })
            .toBe(true)
        }
        const all = launched.records()
        expect(all.length, `${mode}: the handshake read is not in the log`).toBeGreaterThan(0)
        const boot = all.filter((record) => record.project === '')
        expect(
          boot.map((record) => record.argv.slice(1)),
          `${mode}: the rail's boot read is not one version probe and one config list`,
        ).toEqual([['version'], ['config', 'list', '--json']])
        for (const record of boot) expect(record.argv[0]).toBe(join(stub.dir, 'fugaro'))
        const records = all.filter((record) => record.project !== '')
        for (const record of records) {
          expect(Object.keys(record).sort(), `${mode}: the record has extra keys`).toEqual([
            'argv',
            'cwd',
            'project',
            'ts',
          ])
          // The active installation's binary (Task 3.4b, step 3) is the argv's
          // first element in every record: the handshake's probe and the source's
          // reads both resolve through the installation the operator chose.
          expect(record.argv[0], `${mode}: a spawn used another binary`).toBe(join(stub.dir, 'fugaro'))
          expect(record.project, `${mode}: the spawn log lost the installation's project`).toBe('e2e')
          // The argv after the binary is one of the three this console plans --
          // compared whole, so a flag added to either is a failure here rather
          // than a substring nobody looked at.
          expect(expected, `${mode}: the console ran ${record.argv.join(' ')}`).toContainEqual(
            record.argv.slice(1),
          )
        }
      } finally {
        await close(launched)
      }
    }
  })

  test('the Live screen draws the synthetic frame, and the console injects no code to do it', async () => {
    // The whole chain in one window: the entry mounts Live, the poll asks
    // `live.watch`, the main process spawns the installation's own `fugaro`
    // `watch --json`, and the stub prints the committed synthetic recording --
    // so what the page shows is the fixture's repository and its runs, drawn by
    // the bundle the document loaded and by nothing else.
    stub.setVersion(CLI_VERSION_BUILT_AGAINST)
    const launched = await launch(stub.env)
    try {
      const shape = await pageShape(launched.page)
      expect(shape.url).toBe(APP_ENTRY_URL)
      // The drawing is the document's own script's. One script element, resolved
      // to the bundle, and no inline code: a screen that appeared because the
      // main process had injected it would show up here as a second element or
      // as an inline one (`src/main/index.ts` names no `executeJavaScript`, and
      // `test/unit/startup.test.ts` holds it to that).
      expect(shape.scripts).toHaveLength(1)
      expect(shape.scriptUrls).toEqual([`${APP_ORIGIN}/console.js`])
      // And the window holds the entry's drawing and nothing the document
      // shipped: the redesign's header (its switcher holds the installation
      // line and the rail, owner's ruling of 2026-10-28) and the page holding
      // the panel, in that order. Task 1.1's placeholder -- "The console's
      // window is up. It has no data yet." -- would be a third child here, a
      // second `<h1>` and a sentence this task made false.
      expect(shape.bodyChildren).toEqual(['console-header', 'console-page'])
      expect(shape.bodyText).not.toContain('It has no data yet')

      const panel = launched.page.locator('div.console-panel')
      await expect(panel.locator('section.live-repo')).toHaveCount(1)
      await expect(panel.locator('span.live-repo-name')).toHaveText('r-4c25cf20/r-f1aa9214')
      // The last frame of the recording carries both runs.
      await expect(panel.locator('tr.live-run')).toHaveCount(2)
      await expect(panel.locator('tr.live-run td.live-run-id')).toHaveText([
        '20261015-120000-aa01',
        '20261015-120400-aa03',
      ])
      // The navigation, and Live as the view on screen.
      await expect(launched.page.locator('nav.console-views button.console-view')).toHaveText([
        'Live',
        'Graph',
        'Spend',
        'Project',
        'Coordinator',
      ])
      await expect(
        launched.page.locator('nav.console-views button.console-view[aria-current="page"]'),
      ).toHaveText('Live')
      // And the read behind that drawing is the installation's own binary and
      // project, in the log -- not merely inferred from the screen.
      const watch = launched
        .records()
        .filter((record) => record.argv[1] === 'watch')
        .map((record) => record.argv)
      expect(watch.length, 'the live read is not in the spawn log').toBeGreaterThan(0)
      for (const argv of watch) {
        expect(argv).toEqual([join(stub.dir, 'fugaro'), 'watch', '--json', '--project', 'e2e'])
      }
    } finally {
      await close(launched)
    }
  })
  test('a click on a run row draws the run’s frame in the click’s own frame, and fills it after', async () => {
    // The owner's report of 2026-11-01: clicking a run id looked as though it
    // had not registered, because the screen waited for `diagnose` and `logs`
    // before drawing anything. The click and the reading of the panel below
    // happen in **one task**, from the page's own world: the two reads are real
    // IPC calls and cannot answer inside the task that started them, so what
    // this reads is the frame the click itself drew -- the heading, both
    // sections marked busy with their own skeletons, and no figure.
    stub.setVersion(CLI_VERSION_BUILT_AGAINST)
    const launched = await launch(stub.env)
    try {
      const panel = launched.page.locator('div.console-panel')
      const row = panel.locator('tr.live-run').first()
      await expect(row.locator('td.live-run-id')).toHaveText('20261015-120000-aa01')

      const frame = await launched.page.evaluate(() => {
        const row = document.querySelector('tr.live-run')
        ;(row as HTMLElement | null)?.click()
        const drawn = document.querySelector('div.console-panel')?.firstElementChild ?? null
        const main = drawn?.querySelector('div.run-main') ?? null
        const logs = drawn?.querySelector('section.run-logs') ?? null
        return {
          drawn: drawn?.getAttribute('class') ?? null,
          heading: drawn?.querySelector('h1.run-id')?.textContent ?? null,
          wayBack: drawn?.querySelector('button.run-crumb-live')?.textContent ?? null,
          mainBusy: main?.getAttribute('aria-busy') ?? null,
          logsBusy: logs?.getAttribute('aria-busy') ?? null,
          skeletons: drawn?.querySelectorAll('div.console-skeleton').length ?? -1,
          labels: [...(drawn?.querySelectorAll('span.console-busy-label') ?? [])].map(
            (one) => one.textContent ?? '',
          ),
          runLogLines: drawn?.querySelectorAll('p.run-log-line').length ?? -1,
        }
      })
      expect(frame.drawn, 'the click drew no run screen').toBe('run')
      expect(frame.heading).toBe('20261015-120000-aa01')
      expect(frame.wayBack).toBe('Live')
      expect(frame.mainBusy).toBe('true')
      expect(frame.logsBusy).toBe('true')
      expect(frame.skeletons).toBe(2)
      expect(frame.labels).toContain('Reading the diagnosis of 20261015-120000-aa01')
      expect(frame.labels).toContain('Reading the log window of 20261015-120000-aa01')
      expect(frame.runLogLines, 'a log window was drawn before its read answered').toBe(0)

      // And the frame is what stays: the reads answer afterwards -- this stub
      // refuses anything but `watch` and `doctor`, so each section ends in its
      // own refusal naming its own command -- and no busy mark outlives them.
      await expect(panel.locator('h1.run-id')).toHaveText('20261015-120000-aa01')
      await expect(panel.locator('div.console-busy')).toHaveCount(0)
      await expect(panel.locator('div.run-main > div.console-busy-slot .unavailable-operation')).toHaveText('run.diagnose')
      await expect(panel.locator('section.run-logs .unavailable-operation')).toHaveText('run.logs')
    } finally {
      await close(launched)
    }
  })

  test('the chrome keeps its marks under forced colors, the rail’s rows with them (design section 6.4)', async () => {
    // Section 4.4's promise, read from inside a page the system is painting in
    // forced colors: the current view is still marked, the chrome's boundary is
    // still a boundary, and every state keeps its word. It is a **smoke, not a
    // golden** (design section 6.4) -- the pixels under forced colors are the
    // platform's and are not this console's to pin -- so what is asserted is what
    // section 4.4 promises and nothing about how it looks.
    //
    // The chrome's half is this spec's because `entry.spec.ts` already drives the
    // dashboard the chrome is drawn into; the screens' half is Task 6.4's, in each
    // screen's own spec. The rail is chrome, and section 4.4's rule 1 -- a state
    // keeps its word -- is what its three state words are for, so the rail's row
    // is read here beside the nav's.
    stub.setVersion(CLI_VERSION_BUILT_AGAINST)
    const launched = await launch(stub.env)
    try {
      // Drawn first, then the media emulated: what forced colors is asked about is
      // the chrome this console drew, not a page that never saw it.
      await expect(launched.page.locator('div.console-panel section.live-repo')).toHaveCount(1)
      await expect(launched.page.locator('aside.console-rail span.console-rail-state')).toHaveText(
        '● running',
      )

      await launched.page.emulateMedia({ forcedColors: 'active', colorScheme: 'light' })
      // The emulation is on before anything is read, and `colorScheme` is pinned
      // so the run does not depend on the machine's own preference.
      expect(await launched.page.evaluate(() => matchMedia('(forced-colors: active)').matches)).toBe(
        true,
      )

      const chrome = await launched.page.evaluate(() => {
        const read = (element: Element | null) => {
          if (element === null) return null
          const computed = getComputedStyle(element)
          return {
            background: computed.backgroundColor,
            bottomWidth: computed.borderBottomWidth,
            bottomColor: computed.borderBottomColor,
            leftWidth: computed.borderLeftWidth,
            visibility: computed.visibility,
          }
        }
        const marked = document.querySelector(
          'nav.console-views button.console-view[aria-current="page"]',
        )
        const plain = document.querySelector(
          'nav.console-views button.console-view:not([aria-current="page"])',
        )
        const row = document.querySelector('aside.console-rail button.console-rail-row')
        return {
          markedText: marked?.textContent ?? null,
          marked: read(marked),
          plain: read(plain),
          row: read(row),
          stateWord: row?.querySelector('span.console-rail-state')?.textContent ?? null,
        }
      })

      // The current view is still marked, and still by the same attribute: the
      // mark is not a colour the system took away.
      expect(chrome.markedText, 'the current view lost its mark under forced colors').toBe('Live')
      expect(chrome.marked?.visibility).toBe('visible')
      // The chrome's boundary is still a boundary: a border the system forces to
      // one of its own colours is a border, and a width of zero would not be.
      expect(chrome.marked?.bottomWidth, 'the current view’s boundary collapsed').not.toBe('0px')
      expect(chrome.marked?.bottomColor).not.toBe('rgba(0, 0, 0, 0)')
      // And the mark that was a *fill* survives the palette: section 4.4's second
      // rule maps the current view to `Highlight` with `forced-color-adjust: none`,
      // so it is still painted unlike the views beside it.
      expect(chrome.plain, 'the nav has no unmarked view to compare the mark against').not.toBeNull()
      expect(chrome.marked?.background, 'the marked view is painted like its neighbours').not.toBe(
        chrome.plain?.background,
      )
      // The rail's rows are read with them: its hairline is still a hairline, its
      // 3 px left border is still 3 px, and the state's word and its glyph are
      // both still in the DOM -- which is the whole of what rule 1 asks for.
      expect(chrome.row?.visibility).toBe('visible')
      expect(chrome.row?.bottomWidth, 'the rail row’s boundary collapsed').not.toBe('0px')
      expect(chrome.row?.leftWidth).toBe('3px')
      expect(chrome.stateWord, 'the rail’s state word did not survive forced colors').toBe(
        '● running',
      )
    } finally {
      await close(launched)
    }
  })

  test('the installation rail draws a row for the installation it can read, and none for one it cannot', async () => {
    // Design section 4.1's rail, in a real window: one row per installation the
    // CLI listed or this console holds -- here the stored `e2e`, which the
    // stub's empty list does not name -- carrying its name, its project, its
    // state word and its glyph; and, on a profile with nothing at all, **no row**
    // while the rail itself is still drawn. The rail's one read is the boot read
    // (`config list --json`, before the handshake), and the spawn log below is
    // where that is visible.
    stub.setVersion(CLI_VERSION_BUILT_AGAINST)
    const launched = await launch(stub.env)
    try {
      const rail = launched.page.locator('aside.console-rail')
      await expect(rail, 'the dashboard draws no installation rail').toHaveCount(1)
      await expect(rail.locator('button.console-rail-row')).toHaveCount(1)
      await expect(rail.locator('span.console-rail-name')).toHaveText('e2e')
      await expect(rail.locator('span.console-rail-project')).toHaveText('e2e')
      // The state word and the glyph, together: the rail opens `running` because
      // the console is reading this installation now, and the Live poll it is
      // reading is the one the panel below draws.
      await expect(rail.locator('span.console-rail-state')).toHaveText('● running')
      await expect(rail.locator('span.console-rail-state')).toHaveAttribute('data-state', 'running')
      // The spend is `n/a` until the console's own `budget show` read fills it:
      // the rail reads nothing itself, and the dashboard's mount reads no budget
      // (that read is the Spend screen's).
      await expect(rail.locator('span.console-rail-spend')).toHaveText('n/a')
      // The head, the foot row and the hint are the rail's whatever it has to put
      // in its list, and the foot row is the design's own control.
      await expect(rail.locator('button.console-rail-add')).toHaveText('+ Add installation')
      await expect(rail.locator('p.console-rail-hint')).toHaveText('Switch with the shortcut keys 1 2 3')
      // And the rail asked for nothing: the log holds the handshake's probe, the
      // live watch and -- when it has landed -- the Live screen's rigor join
      // (`runs ls --json`), which is what a healthy console spawns -- a row per
      // stored installation would have added a read here. The watch is written by the
      // entry's *first poll*, which is a message round and a spawn behind the rail
      // being drawn -- the row's `running` is the word the rail opens with, not
      // evidence that a read has landed -- so this waits for the record the way
      // the spawn-log case above does rather than reading a log still being
      // written.
      await expect
        .poll(() => launched.records().some((record) => record.argv[1] === 'watch'), {
          message: 'the live read never reached the spawn log',
        })
        .toBe(true)
      // The rigor join is the Live screen's, asked for after its first frame, so
      // whether it has reached the log yet is a race this case does not decide:
      // it is admitted by its whole argv and by nothing shorter.
      const all = launched.records()
      expect(
        all.filter((record) => record.argv[1] === 'config').map((record) => record.argv.slice(1)),
        'the rail read the list more than once',
      ).toEqual([['config', 'list', '--json']])
      const records = all.filter((record) => record.project !== '')
      const reads = [...new Set(records.map((record) => record.argv[1]))].sort()
      expect(
        reads.filter((read) => read !== 'runs'),
        'the rail made a read of its own',
      ).toEqual(['version', 'watch'])
      for (const record of records.filter((each) => each.argv[1] === 'runs')) {
        expect(record.argv.slice(1), 'the rail made a read of its own').toEqual([
          'runs',
          'ls',
          '--json',
          '--since',
          '24h',
          '--project',
          'e2e',
        ])
      }
    } finally {
      await close(launched)
    }

    // The other half: a first run on a machine with no installation. The CLI's
    // list is empty and the console holds no record, so there is no row to draw
    // -- and an invented one would be a lie about what this console knows.
    const fresh = installCliStub(CLI_VERSION_BUILT_AGAINST)
    try {
      const blank = await launch(fresh.env, { firstRun: fresh })
      try {
        expect(blank.page.url(), 'the first run drew something other than the dashboard').toBe(
          APP_ENTRY_URL,
        )
        expect(await installationOf(blank.page)).toEqual({
          ok: true,
          data: { chosen: false, sentence: NO_ACTIVE_INSTALLATION },
        })
        const rail = blank.page.locator('aside.console-rail')
        await expect(rail, 'the rail is not drawn at all with nothing chosen').toHaveCount(1)
        await expect(rail.locator('button.console-rail-row')).toHaveCount(0)
        await expect(rail.locator('button.console-rail-add')).toHaveText('+ Add installation')
        // And the installation line says the same thing in words, which is the
        // pair a reader checks: an empty rail and a sentence naming why.
        await expect(blank.page.locator('section.console-installation p.installation-none')).toHaveText(
          NO_ACTIVE_INSTALLATION,
        )
      } finally {
        await close(blank)
      }
    } finally {
      fresh.remove()
    }
  })

  test('a stored binary that has gone falls through, and the header names the binary this run drives (review B1)', async () => {
    // This spec used to pin the opposite rule -- a stored override that is gone
    // is a refusal, never another `fugaro` -- and the owner's ruling of
    // 2026-10-20 reversed it: a stored `binaryPath` is only rule 4's **first
    // candidate**, so the walk carries on to the well-known prefixes and the
    // console drives whichever binary it resolved. What the old spec was really
    // written against is the silence, not the fall-through: a dashboard that
    // names the gone path while the console runs another file. That is what this
    // asserts now.
    const gone = installCliStub(CLI_VERSION_BUILT_AGAINST)
    try {
      const missing = join(gone.dir, 'missing', 'fugaro')
      gone.seedActiveInstallation('gone', missing)
      // Which `fugaro` this machine gives the app, asked with the console's own
      // resolver and the environment this launch passes to it. **Not** the
      // scratch stub spelled out: the override having gone means rule 4 walks
      // `/opt/homebrew/bin`, `/usr/local/bin`, `~/.local/bin`, `/usr/bin`, and a
      // machine with a real `fugaro` in one of the first two drives *that* -- so
      // a hardcoded `~/.local/bin/fugaro` would fail here for a fact about the
      // machine, in the one spec whose subject is the resolution itself.
      const winner = await resolveConsoleBinary(gone.env)
      const mode = winner.ok ? classifyCliVersion(winner.version).mode : 'refused'
      test.skip(mode !== 'ok', `no fugaro this console drives was resolved (${winner.ok ? winner.version : 'none found'})`)
      const drove = winner.ok ? winner.path : ''
      // The line the header draws is the path as the console's sanitizer *draws*
      // it, never the raw string: `src/renderer/entry.ts`'s `textOf` is
      // `sanitizeRefusal`, and a scratch directory's random suffix reads as a
      // token, so `/tmp/fugaro-cli-stub-Ab3xY9/home/.local/bin/fugaro` is drawn
      // as `[redacted-token].local/bin/fugaro`. On a machine with a real
      // `fugaro` in `/usr/local/bin` the two strings coincide, which is why this
      // spec passed there and failed on CI, where the scratch stub is what the
      // walk finds (the same idiom as the reduced-mode case above).
      const drawnDrove = sanitizeRefusal(drove, 'the installation’s binary')
      expect(drawnDrove.ok, 'this run resolved a binary the sanitizer refuses').toBe(true)
      const scratch = mkdtempSync(join(tmpdir(), 'fugaro-console-entry-'))
      const spawnLog = join(scratch, 'spawn.jsonl')
      const app = await launchConsole({ args: gone.launchArgs(consoleDir), env: { ...gone.env, [SPAWN_LOG_ENV]: spawnLog } })
      try {
        const page = await app.firstWindow()
        await page.waitForLoadState('domcontentloaded')
        expect(page.url(), 'the fall-through did not reach the dashboard').toBe(APP_ENTRY_URL)
        expect(app.windows()).toHaveLength(1)
        // The header, in the words the operator reads: the record's project, and
        // the binary **this run resolved** -- not the path the record still holds.
        const section = page.locator('section.console-installation')
        await expect(section.locator('p.installation-project')).toHaveText('gone')
        await expect(section.locator('p.installation-binary')).toHaveText(drawnDrove.ok ? drawnDrove.text : '')
        // And the same file at the IPC level, where the reply is raw: the state
        // names the binary this run resolved, not the gone path the record still
        // holds. This is the half the drawn line cannot say, because the
        // sanitizer rewrites both strings.
        const state = await installationOf(page)
        expect((state['data'] as Record<string, unknown>)['binary']).toBe(drove)
        expect((state['data'] as Record<string, unknown>)['binary']).not.toBe(missing)
        // And the file the header names is the file that ran: every record in the
        // log has it first, including the handshake's own `version`.
        await expect
          .poll(() => readSpawnLog(spawnLog).filter((record) => record.argv[0] !== drove).length, {
            message: 'the console spawned something other than the binary the header named',
          })
          .toBe(0)
        expect(readSpawnLog(spawnLog).length).toBeGreaterThan(0)
        // The picker is still reachable, and the row it draws is this
        // installation -- now the mode the binary it drove calls for, because the
        // handshake read one the console can drive (`mode` above, from the same
        // resolution this spec expects the header to name).
        const picker = await openPickerFromMenu(app)
        const listed = (await picker.evaluate(async () => {
          const api = (window as unknown as { fugaroInstallations: { list: () => Promise<unknown> } }).fugaroInstallations
          return await api.list()
        })) as { ok: boolean; data: { rows: { project: string; active: boolean; mode: string }[] } }
        expect(listed.ok).toBe(true)
        expect(listed.data.rows.map((row) => [row.project, row.active, row.mode])).toEqual([['gone', true, mode]])
      } finally {
        await app.close()
        rmSync(scratch, { recursive: true, force: true })
      }
    } finally {
      gone.remove()
    }
  })

  test('an installation this console chose carries no binary, and the header still names the one it drives', async () => {
    // The shape **this console writes** (owner's ruling of 2026-10-20): a record
    // with an id and a project and no `binaryPath` field at all, because the
    // console resolves the installed `fugaro` itself and never stores the path it
    // found. Every other seed in this suite writes the legacy shape, an override
    // naming the CLI, so without this spec the dashboard's own line about the
    // binary it drives is only ever exercised with a store that answers it.
    const bare = installCliStub(CLI_VERSION_BUILT_AGAINST)
    try {
      bare.seedActiveInstallation('bare', null)
      const seeded = readFileSync(join(bare.userData, INSTALLATIONS_FILE_NAME), 'utf8')
      // The seed itself, first: the field is absent rather than `null` or `''`.
      expect(seeded).not.toContain('binaryPath')
      expect(JSON.parse(seeded)).toEqual({
        version: 1,
        active: '0123456789abcdef',
        installations: [{ id: '0123456789abcdef', project: 'bare' }],
      })
      // Which `fugaro` this machine gives the app, from the console's own
      // resolver rather than spelled (see the B1 case above): a record with no
      // override is exactly the case where the well-known order decides, and
      // `/opt/homebrew/bin` and `/usr/local/bin` come before the scratch stub.
      const winner = await resolveConsoleBinary(bare.env)
      const mode = winner.ok ? classifyCliVersion(winner.version).mode : 'refused'
      test.skip(mode !== 'ok', `no fugaro this console drives was resolved (${winner.ok ? winner.version : 'none found'})`)
      const drove = winner.ok ? winner.path : ''
      // Drawn, not raw: see the B1 case above -- `textOf` sanitizes the path, so
      // a resolution that landed in a scratch directory is drawn with its random
      // suffix redacted, and comparing the raw string to it would fail on a
      // machine whose scratch stub won the walk.
      const drawnDrove = sanitizeRefusal(drove, 'the installation’s binary')
      expect(drawnDrove.ok, 'this run resolved a binary the sanitizer refuses').toBe(true)
      const scratch = mkdtempSync(join(tmpdir(), 'fugaro-console-entry-'))
      const spawnLog = join(scratch, 'spawn.jsonl')
      const app = await launchConsole({ args: bare.launchArgs(consoleDir), env: { ...bare.env, [SPAWN_LOG_ENV]: spawnLog } })
      try {
        const page = await app.firstWindow()
        await page.waitForLoadState('domcontentloaded')
        expect(page.url()).toBe(APP_ENTRY_URL)
        const section = page.locator('section.console-installation')
        await expect(section.locator('p.installation-project')).toHaveText('bare')
        // The line the defect drew nothing on: the store has no path to give, so
        // it can only be right if the header reads this run's resolution.
        await expect(section.locator('p.installation-binary')).toHaveText(drawnDrove.ok ? drawnDrove.text : '')
        await expect(section.locator('p.installation-version')).toContainText(winner.ok ? winner.version : '')
        await expect
          .poll(() => readSpawnLog(spawnLog).filter((record) => record.argv[0] !== drove).length, {
            message: 'the console spawned something other than the binary the header named',
          })
          .toBe(0)
        // Non-vacuity: the log is not empty, so the assertion above is about a
        // spawn that happened rather than about a file nobody wrote.
        expect(readSpawnLog(spawnLog).length).toBeGreaterThan(0)
      } finally {
        await app.close()
        rmSync(scratch, { recursive: true, force: true })
      }
    } finally {
      bare.remove()
    }
  })

  test('the first run reads no ambient project, and opens no picker', async () => {
    // An unseeded profile: D13's first run. `FUGARO_PROJECT` is planted in the
    // app's environment and must not reach the handshake (plan 3.4b, tests).
    // Which `fugaro` the machine resolves is not asserted -- only what the
    // console sent it.
    const fresh = installCliStub(CLI_VERSION_BUILT_AGAINST)
    try {
      const launched = await launch(fresh.env, { firstRun: fresh, env: { FUGARO_PROJECT: 'planted-ambient-project' } })
      try {
        // `launch` asserted that no picker opened beside this window.
        expect(launched.app.windows()).toHaveLength(1)
        const records = launched.records()
        expect(records.length).toBeGreaterThan(0)
        for (const record of records) {
          // The handshake's probe, and the rail's boot read of the (empty) list.
          expect([['version'], ['config', 'list', '--json']]).toContainEqual(record.argv.slice(1))
          expect(record.project, 'the handshake took a project nobody chose').toBe('')
        }
        if (launched.page.url() === APP_ENTRY_URL) {
          const installation = await installationOf(launched.page)
          expect(installation).toEqual({ ok: true, data: { chosen: false, sentence: NO_ACTIVE_INSTALLATION } })
          await expect(launched.page.locator('section.console-installation p.installation-none')).toHaveText(
            NO_ACTIVE_INSTALLATION,
          )
        }
      } finally {
        await close(launched)
      }
    } finally {
      fresh.remove()
    }
  })
})
