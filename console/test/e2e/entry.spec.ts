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
//   - **the spawn log holds `version` reads and nothing else.** The log is the
//     one place a spawn that should not have happened is visible, and this task
//     makes the console drive no CLI at all, so the log is the evidence for it.
//
// **The premise (Task 3.4b; review S3).** Every launch here runs against a
// scratch `--user-data-dir` seeded with one **active installation naming the
// stub**, so the startup handshake reads that binary and no other
// (`resolveHandshakeBinary`) -- whatever `fugaro` the machine has in
// `/usr/local/bin` or `/opt/homebrew/bin`. No case here skips for the machine
// it runs on. The one first-run case uses an unseeded profile of its own, and
// asserts nothing that depends on which `fugaro` the machine resolves.
//
// Nothing here reaches the network and nothing here starts a child that the
// console's own read would not have started: the stub answers `version` and
// refuses every other argv, so a console that asked it for anything else fails
// here rather than passing on an answer it should not have been able to get.

import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { _electron as electron, expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import { NO_ACTIVE_INSTALLATION, REDUCED_REFUSAL } from '../../src/main/ipc'
import { APP_ENTRY_URL, APP_ORIGIN, REFUSAL_ENTRY_URL, SERVED_FILES } from '../../src/main/protocol'
import { SPAWN_LOG_ENV } from '../../src/main/spawnlog'
import type { SpawnLogEntry } from '../../src/main/spawnlog'
import {
  CLI_VERSION_BUILT_AGAINST,
  REDUCED_VIEWS,
  SKEW_HEADLINES,
} from '../../src/shared/version'
import { EXPOSED_OPERATIONS } from '../../src/preload/index'
import { installCliStub } from '../support/cliStub'
import type { CliStub } from '../support/cliStub'
import { dismissFirstRunPicker } from '../support/firstRun'
import { INSTALLATIONS_MENU_LABEL, PICKER_ENTRY_URL } from '../../src/main/picker'
import { sanitizeRefusal } from '../../src/shared/sanitize'

// The console's own directory, from this file's location, as in the other specs:
// `test/e2e/x.spec.ts` is two levels below it, and Playwright loads these specs
// as CommonJS.
const consoleDir = join(__dirname, '..', '..')

/**
 * SHIPPED_KEYS is what `window.fugaro` must carry, read out of the preload's own
 * table: the thirteen methods, plus `version` and `operations`. Read rather than
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
  // unseeded one, whose picker opens beside the window under test and is
  // dismissed before any assertion about that window (`dismissFirstRunPicker`).
  const profile = options.firstRun ?? stub
  const app = await electron.launch({
    args: profile.launchArgs(consoleDir),
    env: { ...stubEnv, ...(options.env ?? {}), [SPAWN_LOG_ENV]: spawnLog },
  })
  const page = await app.firstWindow()
  await page.waitForLoadState('domcontentloaded')
  if (options.firstRun !== undefined) await dismissFirstRunPicker(app, page)
  return {
    app,
    page,
    spawnLog,
    scratch,
    records(): readonly SpawnLogEntry[] {
      let text: string
      try {
        text = readFileSync(spawnLog, 'utf8')
      } catch {
        // Unset, unreadable, never written: an empty log is the honest answer,
        // and the tests that need records assert a length.
        return []
      }
      return text
        .split('\n')
        .filter((line) => line.trim() !== '')
        .map((line) => JSON.parse(line) as SpawnLogEntry)
    },
  }
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
}> {
  return page.evaluate(() => {
    const bridge = (window as unknown as { fugaro?: Record<string, unknown> }).fugaro
    const banner = document.querySelector('section.console-skew')
    const headline = document.querySelector('h1.skew-headline')
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
      // the thirteen methods and the two fields, frozen, at the shipped version.
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
      // And no record of a read of the surface: see the next test for the log
      // itself, which is asserted once for every mode at the end.
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
      // arrive inside it, so its refusal is the no-installation one and not the
      // reduced one. `live.watch` never returns for this console -- there is no
      // installation until 3.4b -- so the call is made and its answer read, which
      // is exactly the point: the operation is *reachable* in a reduced mode, and
      // what refused it is the source (`activeSource` in `src/main/index.ts`),
      // named here so a gate that had swallowed it would fail this rather than
      // pass on a different refusal.
      //
      // **The string pinned is the whole sentence, both halves of it.**
      // `noInstallation` throws `` `${NO_ACTIVE_INSTALLATION}: ${operation} has
      // nothing to read` ``, and `dispatch`/`replyReason` pass that message
      // through verbatim, so the reply is the constant *plus* the source's own
      // suffix naming the operation it refused. `toContain(NO_ACTIVE_INSTALLATION)`
      // alone would also pass for an answer that merely quoted the constant --
      // the suffix is what says this refusal came from the read that was asked
      // for -- so the suffix is written out, and a reworded suffix fails here
      // rather than quietly weakening the claim.
      const live = (await launched.page.evaluate(async () => {
        const api = (
          window as unknown as {
            fugaro: { subscribeLive: (args: unknown) => Promise<unknown> }
          }
        ).fugaro
        return await api.subscribeLive({})
      })) as { readonly ok: boolean; readonly reason?: string }
      expect(live.ok).toBe(false)
      expect(live.reason, 'live.watch must not be withheld by the reduced mode').toBe(
        `${NO_ACTIVE_INSTALLATION}: live.watch has nothing to read`,
      )
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
      // The binary line is the path as the console's sanitizer draws it (a
      // scratch directory's random suffix can read as a token and is redacted,
      // exactly as the picker's row draws it).
      const drawnBinary = sanitizeRefusal(join(stub.dir, 'fugaro'), 'the installation’s binary')
      expect(drawnBinary.ok).toBe(true)
      expect(header).toEqual({
        kind: 'chosen',
        lines: ['e2e', drawnBinary.ok ? drawnBinary.text : '', '0.9.0 · reduced'],
      })
      // And the picker's status line, opened from the menu.
      const picker = await openPickerFromMenu(launched.app)
      await expect(picker.locator('#picker-status')).toHaveText('e2e is the installation this console is showing (reduced).')
    } finally {
      await close(launched)
    }
  })

  test('the spawn log holds the version read and nothing else, in every mode', async () => {
    // Task 3.4a drives no CLI at all (D13's picker is 3.4b), so the only spawn
    // this console makes is the handshake's own `version` read. The log is the
    // evidence: one line per child, and every line's argv is a bare `version`.
    // A console that reached for a `report` or a `watch` -- or one that let a
    // screen's read through -- writes it here, where this test sees it.
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
        const records = launched.records()
        expect(records.length, `${mode}: the handshake read is not in the log`).toBeGreaterThan(0)
        for (const record of records) {
          expect(Object.keys(record).sort(), `${mode}: the record has extra keys`).toEqual([
            'argv',
            'cwd',
            'project',
            'ts',
          ])
          // The argv as spawned: the binary, then `version` and nothing else.
          // The probe fixes exactly these two (see `planFor('cli.version', ...)`
          // in `src/main/spawn.ts`), so a third element is a channel that should
          // not have been reachable in this task.
          expect(record.argv, `${mode}: a spawn that is not the version read`).toHaveLength(2)
          expect(record.argv[1], `${mode}: a spawn that is not the version read`).toBe('version')
          // The active installation's binary and project (Task 3.4b, step 3):
          // the bare `version` spawn records which installation it was for.
          expect(record.argv[0], `${mode}: the handshake read another binary`).toBe(join(stub.dir, 'fugaro'))
          expect(record.project, `${mode}: the spawn log lost the installation's project`).toBe('e2e')
          for (const forbidden of ['report', 'watch', 'budget', 'kill', 'runs', 'diagnose']) {
            expect(record.argv.join(' '), `${mode}: the console ran ${forbidden}`).not.toContain(
              forbidden,
            )
          }
        }
      } finally {
        await close(launched)
      }
    }
  })
  test('a stored binary that is gone is a refusal, never another fugaro, and the picker is still reachable (review B1)', async () => {
    // A second profile whose active installation names a binary that is not
    // there. Before the fix the handshake walked on to the machine's own
    // `fugaro` (or the stub on `PATH`) and drew a dashboard for it.
    const gone = installCliStub(CLI_VERSION_BUILT_AGAINST)
    gone.seedActiveInstallation('gone', join(gone.dir, 'missing', 'fugaro'))
    const scratch = mkdtempSync(join(tmpdir(), 'fugaro-console-entry-'))
    const spawnLog = join(scratch, 'spawn.jsonl')
    const app = await electron.launch({ args: gone.launchArgs(consoleDir), env: { ...gone.env, [SPAWN_LOG_ENV]: spawnLog } })
    try {
      const page = await app.firstWindow()
      await page.waitForLoadState('domcontentloaded')
      expect(page.url()).toBe(REFUSAL_ENTRY_URL)
      expect(app.windows()).toHaveLength(1)
      // Nothing was spawned: not the stub on `PATH`, not the machine's own.
      let logged = ''
      try {
        logged = readFileSync(spawnLog, 'utf8')
      } catch {
        logged = ''
      }
      expect(logged).toBe('')
      // The door out of a refusal is still there (plan 3.4b step 5).
      const picker = await openPickerFromMenu(app)
      const listed = (await picker.evaluate(async () => {
        const api = (window as unknown as { fugaroInstallations: { list: () => Promise<unknown> } }).fugaroInstallations
        return await api.list()
      })) as { ok: boolean; data: { rows: { project: string; active: boolean; mode: string }[] } }
      expect(listed.ok).toBe(true)
      expect(listed.data.rows.map((row) => [row.project, row.active, row.mode])).toEqual([['gone', true, 'refused']])
    } finally {
      await app.close()
      rmSync(scratch, { recursive: true, force: true })
      gone.remove()
    }
  })

  test('the first run reads no ambient project, and opens the picker once', async () => {
    // An unseeded profile: D13's first run. `FUGARO_PROJECT` is planted in the
    // app's environment and must not reach the handshake (plan 3.4b, tests).
    // Which `fugaro` the machine resolves is not asserted -- only what the
    // console sent it.
    const fresh = installCliStub(CLI_VERSION_BUILT_AGAINST)
    try {
      const launched = await launch(fresh.env, { firstRun: fresh, env: { FUGARO_PROJECT: 'planted-ambient-project' } })
      try {
        // `launch` asserted the picker opened, once, and closed it.
        expect(launched.app.windows()).toHaveLength(1)
        const records = launched.records()
        expect(records.length).toBeGreaterThan(0)
        for (const record of records) {
          expect(record.argv[1]).toBe('version')
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
