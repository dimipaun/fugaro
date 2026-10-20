// The screenshot run's entry point (owner ruling 2026-10-20): it launches the
// **built** console against a **fake** CLI and the fixture documents beside the
// e2e suite's, captures one PNG per screen, and writes the manifest that says
// what it captured.
//
// `scripts/console-screenshots.sh` is the command an owner runs; this file is
// what that command runs. It is one file of three: `isolation.ts` is the screen
// the run has to pass before a window exists (the binary, the three paths, the
// environment), `fixture.ts` is the fake CLI and the store it seeds, `png.ts` is
// the check on what came back, and `manifest.ts` is the document the run leaves.
//
// **What this run is, exactly.** The console it launches is the real one -- its
// startup handshake, its resolver, its `watch` supervisor, its protocol handler
// and its screens -- launched through the same Playwright/Electron path the e2e
// suite uses (`test/support/launch.ts`). What it is *not* is a console pointed at
// an installation: every read it makes is answered by the fake child in this
// run's scratch tree, from `test/fixtures/screenshots/`, and §1 and §2 of
// `isolation.ts` are the two checks that make that a fact rather than an
// intention. Nothing here writes to a real console profile, reads the operator's
// `~/.config`, or runs a real `fugaro`.
//
// **It needs a display.** Electron opens a real window, so this is a run on a
// Mac (or any machine with a desktop session) and never a run in CI: with no
// display the launch fails, which is what the header of
// `scripts/console-screenshots.sh` says and what the exit code here is.
//
// Everything is committed to being loud. A read the fake did not prepare exits
// 2 and the screen draws a refusal -- so the run *also* compares the window's
// own size to the size the manifest declares, refuses a blank rectangle
// (`png.ts`), and prints the fake's spawn report at the end. A picture that
// arrived from anything but this run's fake is the one failure this file exists
// to make impossible to miss.

import { existsSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'

import type { ElectronApplication, Page } from '@playwright/test'

import { resolveConsoleBinary } from '../../test/support/cliStub'
import { launchConsole } from '../../test/support/launch'
import { installScreenshotFixture, FIXTURES_RELATIVE } from './fixture'
import type { ScreenshotFixture } from './fixture'
import { assertFixtureBinary, assertIsolated, launchEnvironment, newIsolation, realPaths } from './isolation'
import type { Isolation } from './isolation'
import { CAPTURE_WINDOW, MANIFEST_FILE_NAME, buildManifest, serializeManifest, sha256Of } from './manifest'
import type { ScreenshotEntry } from './manifest'
import { assertUsableCapture } from './png'
import { CAPTURE_PIXELS, LIVE_SCREEN, OMISSIONS, PICKER_SCREEN, RUN_ID, RUN_SCREEN, SPEND_SCREEN } from './screens'
import type { OneScreen } from './screens'
import { APP_ENTRY_URL, PICKER_ENTRY_URL } from '../../src/main/protocol'

/** OUTPUT_DIRECTORY is where the captures and the manifest land, from the
 * console's root: `console/screenshots/`, git-ignored, and never committed -- a
 * picture of the console is an artefact of a machine and a day, and the website
 * owner copies the one they want into `website/` deliberately. */
export const OUTPUT_DIRECTORY = 'screenshots'

/** READY_TIMEOUT_MS is how long one screen is given to be drawn. A screen that
 * has not appeared in thirty seconds is not slow, it is not coming: the reads
 * it makes are three local child processes that answer in milliseconds. */
export const READY_TIMEOUT_MS = 30_000

/** CLI_HEADER is the first thing the run prints: what it is about to do, and
 * the one thing it needs that a command line cannot check. */
export const CLI_HEADER = `Fugaro console screenshots -- fixture data only, needs a display

  This launches the built console in a real window and captures it at
  ${String(CAPTURE_WINDOW.width)}x${String(CAPTURE_WINDOW.height)} (x${String(CAPTURE_WINDOW.deviceScaleFactor)} pixels), dark theme.
  Every read it makes is answered by a fake fugaro built from
  ${FIXTURES_RELATIVE.join('/')}/; nothing it does can reach a real installation.

`

/** consoleRoot finds the console package from this file's own location. It is a
 * walk rather than a fixed `../..` because the same file is compiled to
 * `.screenshots/tools/screenshots/` and read as TypeScript by the unit suite,
 * and those two are at different depths. */
export function consoleRoot(from: string): string {
  let at = resolve(from)
  for (;;) {
    if (existsSync(join(at, 'package.json'))) return at
    const up = dirname(at)
    if (up === at) throw new Error(`the screenshot run cannot find the console package above ${from}`)
    at = up
  }
}

/** A launch, with its window sized and its first screen drawn. */
interface Launched {
  readonly app: ElectronApplication
  readonly page: Page
}

/** launchFixtureConsole starts the built console against the fake, with one
 * fresh profile seeded with the fixture's installations, and answers the window
 * the run captures from.
 *
 * The launch is the e2e suite's own path (`launchConsole`, which resolves the
 * Electron runtime and retries ETXTBSY) with the e2e suite's own arguments plus
 * the one switch that decides the raster size. `--user-data-dir` is a fresh
 * directory under this run's scratch tree, which is where the seeded
 * `installations.json` goes: the console's real profile is never opened, and
 * `assertIsolated` is what holds that to be true rather than intended.
 */
async function launchFixtureConsole(options: {
  readonly consoleDir: string
  readonly fixture: ScreenshotFixture
  readonly isolation: Isolation
  readonly env: Readonly<Record<string, string>>
  readonly active: string | null
  readonly expect: string
}): Promise<Launched> {
  const { consoleDir, fixture, isolation, env, active, expect } = options
  const profile = fixture.newProfile()
  fixture.seedInstallations(profile, active)
  assertIsolated({ env, isolation, userDataDir: profile, real: realPaths() })
  const args = [
    consoleDir,
    `--user-data-dir=${profile}`,
    `--force-device-scale-factor=${String(CAPTURE_WINDOW.deviceScaleFactor)}`,
  ]
  const app = await launchConsole({ args, env })
  const page = await waitForWindow(app, expect)
  await resizeWindows(app, expect)
  return { app, page }
}

/** waitForWindow answers the window whose document is `url`, waiting for it if
 * the app has not opened it yet. */
async function waitForWindow(app: ElectronApplication, url: string): Promise<Page> {
  const there = app.windows().find((window) => window.url() === url)
  if (there !== undefined) {
    await there.waitForLoadState('load')
    return there
  }
  const deadline = Date.now() + READY_TIMEOUT_MS
  for (;;) {
    const next = await app.waitForEvent('window', { timeout: Math.max(1, deadline - Date.now()) }).catch(() => null)
    if (next === null) throw new Error(`the console opened no window on ${url} within ${String(READY_TIMEOUT_MS)} ms`)
    await next.waitForLoadState('load')
    if (next.url() === url) return next
    if (Date.now() > deadline) {
      throw new Error(`the console opened windows, and none of them is ${url}`)
    }
  }
}

/** resizeWindows sets every window's **content** size to the capture size, so
 * the PNG is the size the manifest declares whatever the console's own window
 * size is (the dashboard opens at 1180x780, the picker at 720x520).
 *
 * `setContentSize` and not `setSize`: the platform's own title bar and frame are
 * not part of `page.screenshot` (see `CAPTURE_WINDOW`'s `titleBar: 'excluded'`),
 * so the content area is the whole of what is captured and therefore the size
 * that has to be exact. */
async function resizeWindows(app: ElectronApplication, url: string): Promise<void> {
  await app.evaluate(
    ({ BrowserWindow }, options) => {
      for (const window of BrowserWindow.getAllWindows()) {
        if (window.webContents.getURL() === options.url) {
          window.setContentSize(options.width, options.height)
        }
      }
    },
    { url, width: CAPTURE_WINDOW.width, height: CAPTURE_WINDOW.height },
  )
}

/** capture takes one picture and answers its manifest entry, or throws with the
 * file and the reason. */
async function capture(page: Page, screen: OneScreen, outDir: string): Promise<ScreenshotEntry> {
  await page.waitForSelector(screen.drawn, { timeout: READY_TIMEOUT_MS, state: 'attached' })
  const bytes = new Uint8Array(await page.screenshot({ type: 'png' }))
  const image = assertUsableCapture(bytes, CAPTURE_PIXELS, screen.file)
  writeFileSync(join(outDir, screen.file), bytes)
  return {
    file: screen.file,
    width: image.width,
    height: image.height,
    sha256: sha256Of(bytes),
    alt: screen.alt,
  }
}

/** CaptureOutcome is what a run leaves behind: the manifest it wrote, and the
 * fake's own report of every spawn -- which `main` prints, so a run's log ends
 * with the commands the console actually ran rather than with this file's
 * account of them. */
export interface CaptureOutcome {
  readonly manifest: string
  readonly outDir: string
  /** spawned is the fake's `argv` per spawn, in order. */
  readonly spawned: readonly string[]
}

/** runCapture is the whole run: it answers the manifest it wrote. */
export async function runCapture(options: {
  readonly consoleDir: string
  readonly outDir: string
  readonly ambient: Readonly<Record<string, string | undefined>>
}): Promise<CaptureOutcome> {
  const { consoleDir, outDir, ambient } = options
  const fixtureDirectory = join(consoleDir, ...FIXTURES_RELATIVE)
  const isolation = newIsolation()
  const fixture = installScreenshotFixture({ fixtureDirectory, isolation })
  const env = launchEnvironment(ambient, isolation)
  const real = realPaths(ambient)

  // The two checks the run cannot make from inside a window: the environment it
  // is about to launch with, and the binary the console's own resolver will
  // find. They are made before the first launch, so a machine with a real
  // `fugaro` in a well-known prefix stops the run instead of being photographed.
  assertIsolated({ env, isolation, userDataDir: null, real })
  const resolved = await resolveConsoleBinary(env)
  assertFixtureBinary(resolved.ok ? resolved.path : null, fixture.binary, 'the console’s startup handshake')

  mkdirSync(outDir, { recursive: true })
  const screens: ScreenshotEntry[] = []
  const active = fixture.installations[0]
  if (active === undefined) throw new Error(`${FIXTURES_RELATIVE.join('/')} names no installation to point the dashboard at`)

  const dashboard = await launchFixtureConsole({
    consoleDir,
    fixture,
    isolation,
    env,
    active: active.id,
    expect: APP_ENTRY_URL,
  })
  try {
    screens.push(await capture(dashboard.page, LIVE_SCREEN, outDir))
    await openRun(dashboard.page)
    screens.push(await capture(dashboard.page, RUN_SCREEN, outDir))
    await openSpend(dashboard.page)
    screens.push(await capture(dashboard.page, SPEND_SCREEN, outDir))
  } finally {
    await dashboard.app.close()
  }

  // The picker's own launch: the same three installations, none of them chosen,
  // which is D13's first run -- the one state in which the console opens the
  // picker by itself, with no menu click and no bridge call this tool would have
  // to invent.
  const firstRun = await launchFixtureConsole({
    consoleDir,
    fixture,
    isolation,
    env,
    active: null,
    expect: PICKER_ENTRY_URL,
  })
  try {
    screens.push(await capture(firstRun.page, PICKER_SCREEN, outDir))
  } finally {
    await firstRun.app.close()
  }

  const manifest = buildManifest({ screens, omitted: OMISSIONS })
  const text = serializeManifest(manifest)
  writeFileSync(join(outDir, MANIFEST_FILE_NAME), text)
  // The fake's own account of every spawn is read **before** the scratch tree
  // goes (it is a file in that tree), and the tree goes only here, on the path
  // where the run finished: a run that threw leaves it behind as evidence
  // (`isolation.ts`'s `Isolation.scratch` says so), and its Chromium profiles
  // are megabytes this run has no reason to leave on a machine that succeeded.
  const spawned = fixture.reports().map((report) => report.argv.join(' '))
  rmSync(isolation.scratch, { recursive: true, force: true })
  return { manifest: text, outDir, spawned }
}

/** openRun opens the run the picture is of, by clicking the Live screen's own
 * row: the run detail is reached from a row (a run is what names it) and the
 * console's row carries the id the read below is made with. */
async function openRun(page: Page): Promise<void> {
  await page.waitForSelector(`.live-run .live-run-id:text-is("${RUN_ID}")`, { timeout: READY_TIMEOUT_MS })
  await page.locator('.live-run').filter({ hasText: RUN_ID }).first().click()
}

/** openSpend switches the navigation to Spend. The button is the console's own
 * (`button.console-view[data-view="spend"]`), so the screen is reached the way
 * an operator reaches it. */
async function openSpend(page: Page): Promise<void> {
  await page.click('button.console-view[data-view="spend"]')
}

/** main is the command: it prints the header, runs the capture, prints where the
 * pictures are, and exits non-zero on anything that went wrong. */
async function main(): Promise<number> {
  process.stdout.write(CLI_HEADER)
  const consoleDir = consoleRoot(__dirname)
  const outDir = join(consoleDir, OUTPUT_DIRECTORY)
  try {
    const outcome = await runCapture({ consoleDir, outDir, ambient: process.env })
    process.stdout.write(`captures:  ${outcome.outDir}\nmanifest:  ${join(outcome.outDir, MANIFEST_FILE_NAME)}\n\n`)
    process.stdout.write(outcome.manifest)
    // The fake's own account of every spawn, printed last: the evidence that the
    // console read this run's fixture CLI and nothing else, in the child's own
    // words rather than this file's.
    const commands = new Set(outcome.spawned)
    process.stdout.write(`\nthe fixture CLI answered ${String(commands.size)} distinct command(s):\n`)
    for (const command of [...commands].sort()) process.stdout.write(`  ${command}\n`)
    return 0
  } catch (error) {
    process.stderr.write(`\nthe screenshot run failed: ${error instanceof Error ? error.message : String(error)}\n`)
    return 1
  }
}

// Only when run as a command. Nothing imports this file -- it launches Electron
// and needs a display, so nothing can -- and the build is CommonJS
// (`tsconfig.screenshots.json`), which is what `require` and `module` are here.
if (require.main === module) {
  main().then(
    (code) => {
      process.exitCode = code
    },
    (error: unknown) => {
      process.stderr.write(`\nthe screenshot run failed: ${error instanceof Error ? error.message : String(error)}\n`)
      process.exitCode = 1
    },
  )
}
