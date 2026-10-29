// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The launch path, end to end (desktop-console plan V2.5.1c; design section 5.1
// rule 11).
//
// V2.5.1a built the main process's half -- the frozen launch, the confirmation
// window and the one spawn a yes reaches -- and V2.5.1b built the Live view's
// launch control. Both shipped with their own tests: the launcher's rules and
// the window's channels are unit-tested against fakes, and the control is a
// view test. What no test had, and what this file is, is the **whole path in one
// running app**: the operator's click in the Live view, the confirmation window
// that opens beside it, the answer, and the child that is or is not spawned --
// with the real preload, the real IPC gate, the real `app://` document and the
// real spawner in between. It is the one place the pieces can disagree.
//
//   - **A declined launch spawns nothing.** Cancel, and the window's own close,
//     both reach `DECLINED_CANCELLED` and the control draws the cancel sentence.
//     The stub's `runs.log` and the console's spawn log are the two witnesses,
//     and both must be empty of a launch: a window that spawns on its way out is
//     a spend nobody confirmed.
//   - **Closing the parent closes the child.** Rule 11 leaves this one to a real
//     window -- the confirmation is a modal child of the dashboard, and no
//     hermetic test can show Electron's cascade -- so it is observed here: the
//     dashboard is closed under an open window, the child goes with it, nothing
//     is spawned.
//   - **A confirmed launch spawns exactly one row, and the row is rule 11's.**
//     The spawn log's argv is compared word for word -- the frozen row, the
//     console's own run id, the repository as the installation records it, the
//     title the operator typed -- and the task text is asserted **absent** from
//     it by a marker word, because the task reaches the child on stdin and must
//     reach nothing else (not the process table, not the log). The stub's own
//     `runs.log` is where that claim is answered from the other side: the marker
//     is there, on the child's stdin, and nowhere else.
//   - **A yes inside `CONFIRM_READY_DELAY_MS` decides nothing.** The refusal is
//     `CONFIRM_NOT_YET`, nothing is spawned, the window stays open, and the
//     click after the delay is the one that counts: one spawn in total.
//   - **The refusals that are not a decline.** A launch the installation records
//     no base branch for is refused in main's own words with no window at all,
//     and a second answer for a window already answered spawns nothing more.
//   - **A re-chosen installation closes the window as a cancel** (rule 11). The
//     design's words are "removed or re-chosen": the dashboard is modality-
//     disabled while the window is open, so what a spec can drive is the
//     dashboard's own bridge -- `installation.choose` for the project the launch
//     is for -- which is the path `index.ts` wires `cancelFor` into. What the
//     case *observes* is the rule's outcome and not which mechanism produced it
//     (the re-choose replaces the dashboard, and the child goes with its
//     parent); the mechanism itself is pinned in the unit suite, and the
//     parent-close case above is the one that closes a parent with no
//     `cancelFor` in play.
//
// **How the answers are made, and why not by a sleep.** The window's readiness
// delay is measured in the main process from the window's own `show` and
// `focus`, and a spec cannot wait for a wall-clock delay without guessing at one.
// So each case waits on a **state** instead: `confirmWhenReady` presses the
// window's own button, reads whether the window is still there, and presses it
// again -- which is what the operator does, and which is correct whatever the
// delay turns out to be. The one case that needs a yes *inside* the delay has
// the main process emit the window's own `focus` (a re-focus starts the delay
// over) and answer from the window's page in the same evaluation, so the yes
// arrives inside the delay by construction rather than by timing.
//
// **Nothing here is answered by the dashboard.** The confirmation window is the
// only sender its channels accept (`confirm.ts`'s gate is the window's own
// `webContents`), so every yes and no below comes from the window's page or from
// its own button -- the property the whole feature exists for, exercised through
// the real gate rather than asserted about it.
//
// Hermetic: the only CLI is `test/fixtures/bin/fugaro` in a scratch directory,
// the only profile is a scratch `--user-data-dir`, no network is reached, and
// the console's spawn log is the operator's own opt-in at a scratch path.

import { mkdtempSync, readFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'

import { expect, test } from '@playwright/test'
import type { ElectronApplication, Page } from '@playwright/test'

import {
  CONFIRM_ENTRY_URL,
  CONFIRM_NOT_YET,
  CONFIRM_WINDOW_TITLE,
} from '../../src/main/confirm'
import { DECLINED_CANCELLED } from '../../src/main/launch'
import { recordedBaseBranch } from '../../src/main/launchrules'
import { APP_ENTRY_URL } from '../../src/main/protocol'
import { SPAWN_LOG_ENV } from '../../src/main/spawnlog'
import type { SpawnLogEntry } from '../../src/main/spawnlog'
import { CONFIRM_BRIDGE_KEY } from '../../src/preload/confirm'
import { CANCEL_LABEL, CONFIRM_LABEL } from '../../src/renderer/confirm'
import {
  LAUNCH_DECLINED_REASON,
  LAUNCH_DECLINED_WORD,
  LAUNCH_IDS,
  LAUNCH_LAUNCHED_WORD,
  LAUNCH_OPEN_CLASS,
  LAUNCH_OUTCOME_CLASS,
  LAUNCH_SUBMIT_CLASS,
  refusalText,
} from '../../src/renderer/screens/launch'
import { LAUNCH_CLI_FLOOR } from '../../src/shared/version'
import { installCliStub } from '../support/cliStub'
import type { CliStub, SeededLaunchRecords } from '../support/cliStub'
import { launchConsole } from '../support/launch'

const consoleDir = join(__dirname, '..', '..')

/** PROJECT is the installation every case launches on, and REPOSITORY the
 * recorded identity the launch is cut for: the CLI's own
 * `<provider>/<owner>/<name>` spelling, which the argv carries as `owner/name`.
 * No real repository, owner or person is named anywhere in this file. */
const PROJECT = 'e2e'
const REPOSITORY = 'github/owner/repo-a'
const LAUNCHED_REPOSITORY = 'owner/repo-a'
const BRANCH = 'main'

/** CHECKOUT_PATH is the directory the installation recorded for REPOSITORY, as
 * `config list --json` prints it: every row of a real document carries the
 * absolute path it holds, whether or not it still passes re-validation, and the
 * console is handed a document that says a checkout was recorded. No machine the
 * console runs on has this directory -- the launch reads the repository and the
 * branch from the row and never touches the path. */
const CHECKOUT_PATH = '/Users/owner/repo-a'

/** TITLE is what the operator types, and MARKER is the one word of the task that
 * must never leave the child's stdin: the spawn log is searched for it whole. */
const TITLE = 'a launch of the recorded repository'
const MARKER = 'zz-launch-marker-7'
const TASK = `${MARKER} read the plan and start\nand a second line, so the task is more than one line`

/** RECORDS is the launch's half of the installations document: a repository with
 * a recorded base branch, which is what makes a launch possible at all. */
const RECORDS: SeededLaunchRecords = Object.freeze({
  baseBranch: BRANCH,
  checkouts: Object.freeze([{ repository: REPOSITORY, baseBranch: BRANCH, path: CHECKOUT_PATH }]),
})

/** Launch is one running console: the app, its dashboard, the stub behind it and
 * the two files that record what the launch did. */
interface Running {
  readonly app: ElectronApplication
  readonly page: Page
  readonly stub: CliStub
  readonly spawnLog: string
  /** spawns is the spawn log's own records, read afresh: the console appends to
   * it as it works. */
  spawns(): readonly SpawnLogEntry[]
  /** runs is the stub's scratch record of its own invocations: one line per
   * child, or `''` when the stub was never run with a launch. */
  runs(): string
  close(): Promise<void>
}

/** readSpawnLog parses the log at `path`, and answers nothing when there is no
 * file -- unset, unreadable, never written -- which is the honest answer and why
 * the cases that assert no launch assert the length. */
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

/** launchLines is the spawn log's launch rows: the console's one write is the
 * only child whose argv names the `run` operation, so this is what a spec counts
 * a spawn with (the version probe, the live watch, the rail's list and the Live
 * screen's rigor join are all reads, and a launch is not). */
function launchLines(spawns: readonly SpawnLogEntry[]): readonly SpawnLogEntry[] {
  return spawns.filter((entry) => entry.argv[1] === 'run')
}

/**
 * start launches the console against a fresh stub and a fresh profile, with the
 * spawn log switched on at a scratch path.
 *
 * `records` is what the stub's `config list --json` says about the installation:
 * `RECORDS` for the cases that launch, and a document with no base branch for
 * the one that must be refused before any window opens.
 *
 * The stub reports `LAUNCH_CLI_FLOOR` and **not** `CLI_VERSION_BUILT_AGAINST`,
 * which is what the other specs hand it: the floor is the oldest CLI a launch
 * may be offered to, and a stub below it draws `LAUNCH_NEEDS_CLI` in the
 * control's place, so every case here would fail looking for a button that the
 * version gate is right not to draw.
 */
/**
 * CLOSED_PAGE is the family of errors that mean **the window a promise was
 * awaited in went away**, rather than that the promise failed: Playwright's own
 * "Target page, context or browser has been closed" and "Execution context was
 * destroyed", the application's "Application has been closed", and the
 * renderer's rejection when the frame an `invoke` was sent from is gone.
 *
 * It is a list rather than a regex because each phrase is a sentence from a
 * different layer (Playwright's, Electron's, the IPC's), and a reader checking
 * this spec's swallows wants to see which ones are covered.
 */
const CLOSED_PAGE = [
  'has been closed',
  'Target closed',
  'Execution context was destroyed',
  'Render frame was disposed',
  'WebFrameMain',
  'object has been destroyed',
]

/**
 * swallowClosedPage awaits a promise this spec expects to be **cut off** by a
 * window going away, and rethrows anything else.
 *
 * A blanket `.catch(() => undefined)` in its place hides a spec error as well:
 * a `ReferenceError` raised inside an evaluated function -- which is how a
 * constant this file's renderer half does not have would arrive -- would look
 * exactly like the window closing, and the case would pass having proved
 * nothing.
 */
async function swallowClosedPage(promise: Promise<unknown>): Promise<void> {
  try {
    await promise
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error)
    if (CLOSED_PAGE.some((phrase) => message.includes(phrase))) return
    throw error
  }
}

async function start(records: SeededLaunchRecords = RECORDS): Promise<Running> {
  const stub = installCliStub(LAUNCH_CLI_FLOOR)
  const scratch = mkdtempSync(join(tmpdir(), 'fugaro-console-launch-'))
  const spawnLog = join(scratch, 'spawn.jsonl')
  stub.seedActiveInstallation(PROJECT)
  stub.seedInstallationsList([PROJECT], [], records)
  const app = await launchConsole({
    args: stub.launchArgs(consoleDir),
    env: { ...stub.env, [SPAWN_LOG_ENV]: spawnLog },
  })
  const page = await app.firstWindow({ timeout: 15_000 })
  await page.waitForLoadState('domcontentloaded')
  expect(page.url()).toBe(APP_ENTRY_URL)
  return {
    app,
    page,
    stub,
    spawnLog,
    spawns: () => readSpawnLog(spawnLog),
    runs(): string {
      try {
        return readFileSync(join(stub.dir, 'runs.log'), 'utf8')
      } catch {
        return ''
      }
    },
    async close(): Promise<void> {
      // The app may already be gone: the parent-close case takes the whole
      // console down with it (`window-all-closed`), and closing an exited
      // process is not a failure of this spec's. The scratch directories are
      // removed either way, which is the half the spec owns.
      await swallowClosedPage(app.close())
      rmSync(scratch, { recursive: true, force: true })
      stub.remove()
    },
  }
}

/**
 * askForLaunch drives the Live view's launch control the way an operator does:
 * the button that reveals the form, the recorded repository, the task, the
 * title, and the submit that asks the main process for a confirmation window.
 * Nothing here answers that window.
 */
async function askForLaunch(page: Page): Promise<void> {
  await page.locator(`.${LAUNCH_OPEN_CLASS}`).click()
  await page.locator(`#${LAUNCH_IDS.repository}`).selectOption(REPOSITORY)
  await page.locator(`#${LAUNCH_IDS.task}`).fill(TASK)
  await page.locator(`#${LAUNCH_IDS.title}`).fill(TITLE)
  await page.locator(`.${LAUNCH_SUBMIT_CLASS}`).click()
}

/**
 * confirmWindow is the confirmation window among the app's windows. It is found
 * by the document it loads and not by its title: the title is the
 * `BrowserWindow`'s own (`CONFIRM_WINDOW_TITLE`, which the document is refused
 * the right to take over) and is asserted separately, from the main process,
 * where it is a fact.
 */
async function confirmWindow(app: ElectronApplication): Promise<Page> {
  let found: Page | null = null
  await expect
    .poll(
      () => {
        found = app.windows().find((page) => page.url() === CONFIRM_ENTRY_URL) ?? null
        return found === null ? 'none' : 'there'
      },
      { timeout: 15_000, message: 'the confirmation window never opened' },
    )
    .toBe('there')
  if (found === null) throw new Error('the confirmation window went away between the poll and this line')
  return found
}

/** windowTitles is every open window's title, in the main process's view: the
 * one place a `BrowserWindow` title can be read, since a document's `<title>` is
 * a different string. */
function windowTitles(app: ElectronApplication): Promise<string[]> {
  return app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows().map((window) => window.getTitle()))
}

/**
 * doubleAnswer answers the confirmation window twice from its own page, in the
 * same tick, and does not wait for either reply.
 *
 * It is the operator's double click as the main process sees one: two answers
 * for one window, the first of which decides. A pair that is refused -- the
 * window is not ready yet, which is the same refusal the first click gets --
 * decides nothing, so the loop is the operator's next double click, and the
 * case's assertions are on what the pair did rather than on which click it was.
 */
async function doubleAnswer(confirm: Page): Promise<void> {
  // The window is closed by the answer it took, and the page goes with it:
  // this call is expected to be cut off rather than to resolve.
  await swallowClosedPage(
    confirm.evaluate(
      async ({ key, closed }) => {
        const bridge = (window as unknown as Record<string, { answer: (yes: boolean) => Promise<unknown> }>)[key]
        if (bridge === undefined) throw new Error('the confirmation document has no bridge')
        for (let attempt = 0; attempt < 200; attempt += 1) {
          const first = bridge.answer(true)
          const second = bridge.answer(true)
          // The second answer is the one this loop does not follow: its reply is
          // a refusal the main process sends to a window that is going away.
          // The swallow is written out here rather than called: this function
          // runs in the renderer, where the spec's helper does not exist, and
          // the phrases come in as an argument for the same reason.
          void Promise.resolve(second).catch((error: unknown) => {
            const message = error instanceof Error ? error.message : String(error)
            if (!closed.some((phrase: string) => message.includes(phrase))) throw error
          })
          const taken = await Promise.race([
            Promise.resolve(first).then((reply) => (reply as { ok?: boolean } | null)?.ok === true),
            new Promise<boolean>((resolve) => setTimeout(() => resolve(false), 250)),
          ])
          if (taken) return
          await new Promise((resolve) => setTimeout(resolve, 25))
        }
      },
      { key: CONFIRM_BRIDGE_KEY, closed: CLOSED_PAGE },
    ),
  )
}

/**
 * confirmWhenReady presses the window's own confirming button until the main
 * process takes the answer, and answers when the window is gone.
 *
 * The readiness delay is main's, measured from the window's own `show`/`focus`,
 * and a yes inside it is refused with `CONFIRM_NOT_YET` and **decides nothing**:
 * the window stays open on the same frozen launch and the next click is the one
 * that counts. That is exactly what this loop is -- the operator's click, then
 * the same click again while the window is still there -- and it never waits on
 * a delay this spec guessed at. A click that lands while the window is going
 * away is the answer that was just taken; it is caught and the window's own
 * state decides.
 */
async function confirmWhenReady(confirm: Page): Promise<void> {
  await expect
    .poll(
      async () => {
        if (confirm.isClosed()) return 'answered'
        // A click that lands while the window is going away is the answer that
        // was just taken, and the window's own state decides.
        await swallowClosedPage(confirm.getByRole('button', { name: CONFIRM_LABEL }).click({ timeout: 3_000 }))
        return confirm.isClosed() ? 'answered' : 'waiting'
      },
      {
        timeout: 20_000,
        intervals: [100],
        message: 'the confirming click was never taken by the confirmation window',
      },
    )
    .toBe('answered')
}

/**
 * answerEarly answers the window with a yes that is **inside** its readiness
 * delay, and answers the main process's reply with it.
 *
 * One main-process evaluation does both halves: it emits the window's own
 * `focus` (which is the event the delay is measured from, and a re-focus starts
 * it over) and then runs the window's page's own `answer(true)`, so the yes
 * arrives a renderer round trip after the delay was reset and inside it by
 * construction -- no timer of this spec's stands between the two. Emitting
 * `focus` is what a spec has instead of a window manager: the shipped path is
 * Electron's own event for a window that has taken the keyboard, and rule 11's
 * readiness question is about the delay, not about who raised it.
 */
function answerEarly(app: ElectronApplication): Promise<{ ok: boolean; reason?: string }> {
  return app.evaluate(
    async ({ BrowserWindow }, source) => {
      const target = BrowserWindow.getAllWindows().find((window) => window.getTitle() === source.title)
      if (target === undefined) throw new Error('no confirmation window is open')
      target.emit('focus')
      return (await target.webContents.executeJavaScript(source.expression)) as { ok: boolean; reason?: string }
    },
    { title: CONFIRM_WINDOW_TITLE, expression: `window.${CONFIRM_BRIDGE_KEY}.answer(true)` },
  )
}

/** closeFromMain closes the confirmation window the way the operator's close box
 * does: the window goes away without an answer, and `confirm.ts`'s `closed`
 * listener is what turns that into the cancel. */
function closeFromMain(app: ElectronApplication): Promise<boolean> {
  return app.evaluate(({ BrowserWindow }, title) => {
    const target = BrowserWindow.getAllWindows().find((window) => window.getTitle() === title)
    if (target === undefined) return false
    target.close()
    return true
  }, CONFIRM_WINDOW_TITLE)
}

/** closeDashboardFromMain closes the dashboard the way its own close box does:
 * the window goes away, and rule 11's cascade -- the confirmation is a modal
 * **child** of this window -- is Electron's to perform, with nothing in this
 * console wired for it. */
function closeDashboardFromMain(app: ElectronApplication): Promise<boolean> {
  return app.evaluate(({ BrowserWindow }, url) => {
    const target = BrowserWindow.getAllWindows().find((window) => window.webContents.getURL() === url)
    if (target === undefined) return false
    target.close()
    return true
  }, APP_ENTRY_URL)
}

/**
 * oneLaunchRow waits for the console's one launch row and answers it.
 *
 * An answer and the child it decides are **not the same instant**: the window
 * closes on the answer, and the launch that answer resolves re-reads the
 * installation's records before the spawner is reached, so the log is read
 * until it carries the row and never once. What it waits for is **exactly** one
 * and not "at least one": a second row is a second spend, and the cases that
 * answer twice are the ones this has to be able to catch.
 */
async function oneLaunchRow(running: Running): Promise<SpawnLogEntry> {
  let row: SpawnLogEntry | undefined
  await expect
    .poll(
      () => {
        const rows = launchLines(running.spawns())
        row = rows[0]
        return rows.length
      },
      { timeout: 20_000, intervals: [100], message: 'the launch never spawned exactly one row' },
    )
    .toBe(1)
  if (row === undefined) throw new Error('the launch row went away between the poll and this line')
  return row
}

/** noSpawn is the whole of "nothing was launched": the stub was never asked to
 * run anything and the console never spawned a launch row. */
function expectNoSpawn(running: Running): void {
  expect(running.runs()).toBe('')
  expect(launchLines(running.spawns())).toHaveLength(0)
}

/** runIDOf is the run id the console generated for the one launch row: the
 * `--run-id` it put on the argv, which is also what the reply must echo. */
function runIDOf(spawn: SpawnLogEntry | undefined): string {
  const argv = spawn?.argv ?? []
  const at = argv.indexOf('--run-id')
  const id = at === -1 ? undefined : argv[at + 1]
  if (id === undefined) throw new Error(`the launch row carries no --run-id: ${JSON.stringify(argv)}`)
  return id
}

test.describe('the launch path, in the real app', () => {
  test.describe.configure({ timeout: 90_000 })

  test('a confirmed launch spawns exactly one row, rule 11’s, and the control draws the run', async () => {
    test.skip(
      process.platform === 'darwin',
      "the yes-delay starts on the window's show/focus events, which a macOS runner may never emit; the owner-run smoke launch on Electron 45 (Check 123 and the show/focus-arrive check) decides whether that is real, and if it is, the fix is a product change reviewed on its own",
    )
    const running = await start()
    try {
      const { app, page, stub } = running
      await askForLaunch(page)
      const confirm = await confirmWindow(app)
      // The window is the console's own: it says which window it is -- the title
      // the document is refused the right to take over -- beside the dashboard's,
      // and the page found above loads the confirmation document and not the
      // dashboard's.
      const titles = await windowTitles(app)
      expect(titles).toHaveLength(2)
      expect(titles).toContain(CONFIRM_WINDOW_TITLE)
      expect(page.url()).toBe(APP_ENTRY_URL)

      await confirmWhenReady(confirm)

      // Exactly one child, and its argv is rule 11's row word for word: the
      // frozen words, the id the console generated, the repository as *this
      // console* converted it, the title the operator typed, and the project.
      const spawned = await oneLaunchRow(running)
      const argv = spawned.argv
      expect(argv).toEqual([
        join(stub.dir, 'fugaro'),
        'run',
        '--task-file',
        '-',
        '--json',
        '--run-id',
        runIDOf(spawned),
        '--repo',
        LAUNCHED_REPOSITORY,
        `--title=${TITLE}`,
        '--project',
        PROJECT,
      ])
      // Every word of the row is a word this launch has a value for: no
      // `--plan-task=` and no `--config`, which the operator did not fill in and
      // this installation does not record.
      expect(argv.filter((word) => word.startsWith('--plan-task='))).toEqual([])
      expect(argv).not.toContain('--config')

      // The task reached the child on stdin and nowhere else: the stub's own
      // record of the invocation has the marker, and the spawn log -- the whole
      // file, every read on it included -- does not.
      expect(readFileSync(running.spawnLog, 'utf8')).not.toContain(MARKER)
      const runs = running.runs()
      expect(runs.split('\n').filter((line) => line.trim() !== '')).toHaveLength(1)
      expect(runs).toContain(MARKER)
      expect(runs).toContain(`--repo ${LAUNCHED_REPOSITORY}`)

      // The window is gone and the dashboard drew the answer the CLI gave: the
      // run the console generated, drawn as the CLI's own reply reported it.
      expect(app.windows().filter((window) => !window.isClosed())).toHaveLength(1)
      await expect(page.locator(`.${LAUNCH_OUTCOME_CLASS}`)).toContainText(LAUNCH_LAUNCHED_WORD)
      await expect(page.locator(`.${LAUNCH_OUTCOME_CLASS}`)).toContainText(runIDOf(spawned))
      // One row in total, still: the argv above is the only child this
      // confirmation ever reached the spawner with.
      expect(launchLines(running.spawns())).toHaveLength(1)
    } finally {
      await running.close()
    }
  })

  test('a yes inside the readiness delay decides nothing, and the click after it launches once', async () => {
    const running = await start()
    try {
      const { app, page } = running
      await askForLaunch(page)
      const confirm = await confirmWindow(app)
      await confirm.waitForLoadState('load')

      const early = await answerEarly(app)
      expect(early).toEqual({ ok: false, reason: CONFIRM_NOT_YET })
      // Nothing was spawned by it, and the window is still the operator's to
      // answer: a refused yes is not a decline.
      expectNoSpawn(running)
      expect(confirm.isClosed()).toBe(false)

      // The same window, the click after the delay: the one that counts.
      await confirmWhenReady(confirm)
      await oneLaunchRow(running)
      await expect(page.locator(`.${LAUNCH_OUTCOME_CLASS}`)).toContainText(LAUNCH_LAUNCHED_WORD)
      // One spawn in total: the refused yes spawned nothing, and the click that
      // was taken is the only row the spawn log ever carried.
      expect(launchLines(running.spawns())).toHaveLength(1)
      expect(running.runs().split('\n').filter((line) => line.trim() !== '')).toHaveLength(1)
    } finally {
      await running.close()
    }
  })

  test('a second answer for the same window spawns nothing more, and the first one’s result stands', async () => {
    test.skip(
      process.platform === 'darwin',
      "the yes-delay starts on the window's show/focus events, which a macOS runner may never emit; the owner-run smoke launch on Electron 45 (Check 123 and the show/focus-arrive check) decides whether that is real, and if it is, the fix is a product change reviewed on its own",
    )
    const running = await start()
    try {
      const { app, page } = running
      await askForLaunch(page)
      const confirm = await confirmWindow(app)
      await confirm.waitForLoadState('load')

      await doubleAnswer(confirm)

      const spawned = await oneLaunchRow(running)
      await expect.poll(() => confirm.isClosed(), { timeout: 10_000 }).toBe(true)

      // One spawn in total -- the second answer reached the main process and
      // decided nothing more -- and the dashboard draws the *first* answer's
      // result, which is the launch that happened.
      expect(running.runs().split('\n').filter((line) => line.trim() !== '')).toHaveLength(1)
      await expect(page.locator(`.${LAUNCH_OUTCOME_CLASS}`)).toContainText(LAUNCH_LAUNCHED_WORD)
      await expect(page.locator(`.${LAUNCH_OUTCOME_CLASS}`)).toContainText(runIDOf(spawned))
      expect(launchLines(running.spawns())).toHaveLength(1)
    } finally {
      await running.close()
    }
  })

  test('Cancel spawns nothing and draws the cancel sentence', async () => {
    const running = await start()
    try {
      const { app, page } = running
      await askForLaunch(page)
      const confirm = await confirmWindow(app)
      await confirm.getByRole('button', { name: CANCEL_LABEL }).click()
      await expect.poll(() => confirm.isClosed(), { timeout: 10_000 }).toBe(true)

      expectNoSpawn(running)
      expect(app.windows().filter((window) => !window.isClosed())).toHaveLength(1)
      // The sentence the control draws is the console's own cancel, and it is
      // the same string `launch.ts` answers with: the renderer's mirror of it
      // (`LAUNCH_DECLINED_REASON`) is what tells a decline from every other
      // refusal, and the two must not drift.
      expect(LAUNCH_DECLINED_REASON).toBe(DECLINED_CANCELLED)
      await expect(page.locator(`.${LAUNCH_OUTCOME_CLASS}`)).toContainText(LAUNCH_DECLINED_WORD)
    } finally {
      await running.close()
    }
  })

  test('closing the window spawns nothing, exactly as Cancel does', async () => {
    const running = await start()
    try {
      const { app, page } = running
      await askForLaunch(page)
      const confirm = await confirmWindow(app)
      expect(await closeFromMain(app)).toBe(true)
      await expect.poll(() => confirm.isClosed(), { timeout: 10_000 }).toBe(true)

      expectNoSpawn(running)
      await expect(page.locator(`.${LAUNCH_OUTCOME_CLASS}`)).toContainText(LAUNCH_DECLINED_WORD)
    } finally {
      await running.close()
    }
  })

  test('a repository with no recorded base branch is refused in main’s words, with no window at all', async () => {
    // The installation's own `base_branch` and the checkout's are both there and
    // both empty, which is §6.8's "a key that is there and records none" -- the
    // other state from "no key at all", whose sentence names an older CLI.
    const records: SeededLaunchRecords = {
      baseBranch: '',
      checkouts: [{ repository: REPOSITORY, baseBranch: '', path: CHECKOUT_PATH }],
    }
    const running = await start(records)
    try {
      const { app, page } = running
      await askForLaunch(page)

      // Main's own sentence, read off the function that makes it and drawn the
      // way the control draws a refusal -- so the expected text is the product's
      // and not this spec's retyping of it.
      const refused = recordedBaseBranch(
        { baseBranch: '', repositories: [{ repository: REPOSITORY, baseBranch: '' }] },
        REPOSITORY,
      )
      expect(refused.ok).toBe(false)
      const sentence = refused.ok ? '' : refusalText(refused.reason)
      expect(sentence).not.toBe('')
      await expect(page.locator(`.${LAUNCH_OUTCOME_CLASS}`)).toContainText(sentence)

      // Nothing was asked of the CLI and no window was opened: the refusal is
      // made while the launch is built, which is *before* the confirmer is
      // called, so there is no window to answer and nothing to answer it with.
      expectNoSpawn(running)
      expect(app.windows().filter((window) => !window.isClosed())).toHaveLength(1)
    } finally {
      await running.close()
    }
  })

  test('closing the dashboard takes the confirmation window with it, and spawns nothing', async () => {
    const running = await start()
    try {
      const { app, page } = running
      await askForLaunch(page)
      const confirm = await confirmWindow(app)
      await confirm.waitForLoadState('load')

      // Rule 11's own last line: "closing the parent closes the child". The
      // design reserves this one for a real window, because no hermetic unit
      // test can show Electron's cascade -- `test/unit/confirm.test.ts` pins
      // that the window is *given* a parent, and this is the same claim
      // observed: nothing in the console closes the child here, the parent's
      // own destruction does.
      expect(await closeDashboardFromMain(app)).toBe(true)
      await expect
        .poll(() => confirm.isClosed(), { timeout: 10_000, intervals: [100], message: 'the child outlived the dashboard' })
        .toBe(true)

      // A window that goes away without an answer is the cancel and never a
      // launch: a dashboard closed under a confirmation must not spend.
      expectNoSpawn(running)
      // What the console does after that is Electron's `window-all-closed` and
      // not this file's to assert: it quits on Linux and Windows and stays up
      // on macOS (`index.ts`), so a case that demanded either would be red on
      // one of the two platforms this suite runs on.
    } finally {
      await running.close()
    }
  })

  test('a re-chosen installation closes the window as a cancel and spawns nothing', async () => {
    const running = await start()
    try {
      const { app, page } = running
      await askForLaunch(page)
      const confirm = await confirmWindow(app)
      await confirm.waitForLoadState('load')

      // Rule 11's "an installation removed or re-chosen while the window is
      // open closes the window as a cancel". The window is modal over the
      // dashboard, so a click in the dashboard is not something a spec can make:
      // what it makes is the dashboard's own `installation.choose` for the
      // project the launch is for, which is the call `index.ts` wires
      // `launchRunner.cancelFor` into before the lifecycle re-chooses.
      //
      // What this case proves is the **observable** half of that rule -- the
      // window closes and nothing is spawned -- and not which of two mechanisms
      // closed it: the re-choose replaces the dashboard with a new window, and
      // the confirmation is a modal child of the old one, so the parent's own
      // cascade (the case above) takes it down whether or not `cancelFor` is
      // wired. That the wiring itself is there is pinned in
      // `test/unit/entry-wiring.test.ts`, and its behaviour in
      // `test/unit/launch.test.ts`; the parent-close case above is the one that
      // closes a parent with no `cancelFor` in play.
      // The dashboard this ran in is replaced by the re-choose once the new
      // window is up, so the answer may never reach it.
      void swallowClosedPage(
        page.evaluate(
          // The project goes in as an argument: the function runs in the
          // renderer, where this file's constants do not exist, and a
          // ReferenceError there is rethrown by the swallow rather than
          // passing for the window closing.
          async (project) => {
            const api = (window as unknown as { fugaro: { chooseInstallation: (args: unknown) => Promise<unknown> } })
              .fugaro
            return await api.chooseInstallation({ project })
          },
          PROJECT,
        ),
      )

      await expect
        .poll(() => confirm.isClosed(), { timeout: 20_000, intervals: [100], message: 'the re-choose did not close the window' })
        .toBe(true)
      // Nobody answered it: the window went away, and a window that goes away
      // without an answer is the cancel.
      expectNoSpawn(running)
    } finally {
      await running.close()
    }
  })
})
