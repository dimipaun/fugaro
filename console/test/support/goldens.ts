// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
// The three things the six golden specs share (desktop-console plan Task 6.6;
// design sections 6.1 and 6.2).
//
// **1. The computed ground — the assertion the suite was missing.** Every one of
// the six specs reads `data-theme` off its screen's root, and that read passes
// with **no stylesheet at all**: the theme travels as an attribute the renderer
// stamps (`root.setAttribute('data-theme', theme)`), so a tree nothing paints
// satisfies it. What no read covered is the *painting*: the ground the app is
// drawn on and the colour its text is drawn in. `readGround` reads both out of
// the live document, `groundProblems` compares them against the design tokens
// the stylesheet declares, and an unstyled mount fails with a sentence naming
// what is missing rather than a pixel diff nobody can read.
//
// **The two elements read here are not the element design section 6.2 names**,
// and the difference is the whole reason the read is written the way it is. 6.2
// names exactly one: the **screen's own root** ("It reads a *computed* background
// off a screen root", `paintedBackground(root)` in its two-set form). A screen's
// root carries `color` and no `background` (design section 2's inventory;
// `section.live` and its siblings are `display: block; color: var(--text)`),
// because the ground is the *document's* — `base.css` paints `--ground` on
// `body` and `--text` is inherited down from it. Reading the screen root's own
// `background-color` would read `rgba(0, 0, 0, 0)` in a perfectly styled window,
// which is why the assertion reads the app's ground off `document.body` and the
// text colour off the screen's own heading — neither of them that root.
//
// **2. Taking a recording (`expectGolden`).** One call, so that every golden in
// the suite fails the same way: on a *comparison* run the freshness preflight of 3
// below runs first, and a comparison that *did* fail is re-reported with the exact
// re-record command appended to Playwright's own message. The command is the point
// — the message `toHaveScreenshot` prints on its own offers
// `npx playwright test <file> --update-snapshots`, which does not build and
// therefore photographs whatever `dist/` happens to hold, and does not work at
// all on Linux without a display.
//
// A **recording** run (`--update-snapshots`, i.e. any mode but the config's
// `'none'`) is the one case the preflight must keep out of: it throws before
// `toHaveScreenshot` is reached, and the matcher is what writes the baseline, so
// a guard that fired on a recording run would refuse to let the command it names
// do its job. `recordingRun` is that decision, and it is the matcher's own
// (`handleMissing` writes whenever the mode is not `'none'`).
//
// **3. The recording's freshness.** The eight baselines are OWNER-RUN
// (`playwright.config.ts` sets `updateSnapshots: 'none'`, so a comparison run
// fails a missing baseline rather than writing one) and the darwin set committed
// by PR #629 is a picture of the tree **before the stylesheet landed**: the four
// specs passed with no CSS in the bundle, so the frames are pictures of unstyled
// markup — the Live, Spend and Projects pairs byte-identical, the Run pair not,
// because that screen's two tests draw different content (a scrub time in the
// light frame, `null` in the dark one). `assertGoldenFresh` is what makes *that*
// state legible: when the golden is older than the stylesheet — by the commit that
// last touched each, which is the only clock a checkout has — the golden test fails
// with the exact re-record command for its own file instead of a comparison
// against a picture of a tree that no longer exists.
//
// **PR #629's darwin set, and what this clock sees of it.** Those PNGs were
// committed on 2026-10-22, *after* the newest file of the theme directory at the
// time, so by commit time they were newer than the stylesheet they predate and
// the timestamp could not see that they were pictures of the unstyled tree: the
// comparison caught them, and that is the case 2 above exists for. **That is no
// longer this tree's state.** The theme has been committed to since — 0.10.2's
// `screens.css` change is the latest commit that touches it — so the committed
// darwin baselines are now *older* than the stylesheet they predate, and
// `assertGoldenFresh` refuses them by name with the re-record command rather than
// letting the comparison run. Either way the answer is the same one, and it is
// the owner's: those frames are pictures of a tree that no longer exists and want
// re-recording; the guard's message is what says so, and it says the same thing
// whichever clock found it.
//
// **Why this module is imported by a unit test as well as by six specs**, and
// why every path in it is an argument rather than a constant derived from this
// file's location: Playwright loads a spec as CommonJS (so `__dirname` is what a
// spec has and `import.meta.url` is not), and vitest loads a unit test as ESM
// (the other way round). A support module both runners import can use neither,
// so `themeStylesheets` and `staleGolden` take the console's directory from
// their caller — the specs pass the `consoleDir` they already compute, and
// `test/unit/goldens.test.ts` passes its own.

import { execFileSync } from 'node:child_process'
import { existsSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'

import { expect } from '@playwright/test'
import type { Locator, Page, TestInfo } from '@playwright/test'

/** STYLESHEET is the one stylesheet every document links and the served root
 * holds: `/console.css`, written by `tools/bundle-renderer/index.ts` from the
 * theme directory, and one of the nine names in `SERVED_FILES`
 * (`src/main/protocol.ts`, design section 5.3). */
export const STYLESHEET = 'console.css'

/** THEME_DIR is the theme directory below the console's root: the directory
 * whose **whole contents** `THEME_STYLESHEETS` names (design section 5.2), and
 * therefore the directory whose newest file a golden has to be newer than. */
export const THEME_DIR = join('src', 'renderer', 'theme')

/** STYLESHEET_SUFFIX is what makes a file in that directory a stylesheet. */
const STYLESHEET_SUFFIX = '.css'

/** GROUND_TOKEN and TEXT_TOKEN are the two tokens the assertion reads, by name:
 * `--ground` is the window's ground (`palette.css`, and the same literal
 * `consoleWindowOptions` paints the Electron window with — Task 6.5) and
 * `--text` is body text (design section 3.2). */
export const GROUND_TOKEN = '--ground'
export const TEXT_TOKEN = '--text'

/** GroundArg is what the in-page probe is given: the two selectors it reads.
 * One object rather than two arguments because Playwright passes a single
 * serializable argument to `page.evaluate`. */
export interface GroundArg {
  /** the host the spec drew the screen into, e.g. `#live-mount` */
  readonly mount: string
  /** the screen's own text element, e.g. `h1.live-project` */
  readonly text: string
}

/** GroundReading is what a mounted screen says about its own painting: the
 * document's stylesheets, the two tokens as the root declares them, and the two
 * colours the compositor would actually paint. */
export interface GroundReading {
  /** every stylesheet the document has loaded, by `href` */
  readonly sheets: readonly string[]
  /** how many rules those sheets hold, in total */
  readonly rules: number
  /** `--ground` as the document root resolves it, trimmed */
  readonly ground: string
  /** `--text` as the document root resolves it, trimmed */
  readonly text: string
  /** `getComputedStyle(document.body).backgroundColor` — the app's ground */
  readonly groundPainted: string
  /** `getComputedStyle(<the screen's text element>).color` */
  readonly textPainted: string
  /** the selector the text element was looked for by, for the message */
  readonly textHook: string
}

/**
 * readGround is the probe the specs evaluate **inside the window**, so it is
 * written to run there and to reference nothing but its argument: Playwright
 * serializes this function by its own source, and a reference to a module
 * constant would arrive in the page as an identifier that does not exist.
 *
 * `document.styleSheets` is read rather than assumed: a document that linked no
 * stylesheet, or linked one the handler refused, is a document whose ground is
 * the compositor's `rgba(0, 0, 0, 0)`, and the two facts are worth telling apart
 * in the failure.
 */
export function readGround(arg: GroundArg): GroundReading {
  const root = getComputedStyle(document.documentElement)
  const body = getComputedStyle(document.body)
  const scope: ParentNode = document.querySelector(arg.mount) ?? document.createDocumentFragment()
  const textElement = scope.querySelector(arg.text)
  const sheets: string[] = []
  let rules = 0
  for (let i = 0; i < document.styleSheets.length; i++) {
    const sheet = document.styleSheets[i]
    if (sheet === undefined) continue
    sheets.push(sheet.href === null ? '' : sheet.href)
    try {
      rules += sheet.cssRules.length
    } catch {
      // A sheet this document may not read: the count stays a floor rather than
      // a total, and the `sheets` list is what the assertion reports.
    }
  }
  return {
    sheets: sheets,
    rules: rules,
    ground: root.getPropertyValue('--ground').trim(),
    text: root.getPropertyValue('--text').trim(),
    groundPainted: body.backgroundColor,
    textPainted: textElement === null ? '' : getComputedStyle(textElement).color,
    textHook: arg.text,
  }
}

/**
 * normalizeColour puts both sides of the comparison in the one form
 * `getComputedStyle` reports — `rgb(r, g, b)` — from the form a token is
 * written in, which is a hex literal (`palette.css` writes `#0A0E16`). `null`
 * for anything else, so a token whose value is not a plain colour is reported as
 * unreadable rather than compared against a string it could never equal.
 */
export function normalizeColour(value: string): string | null {
  const trimmed = value.trim()
  const hex = /^#([0-9a-f]{3}|[0-9a-f]{6})$/i.exec(trimmed)
  if (hex !== null) {
    const digits = hex[1] ?? ''
    const full = digits.length === 3 ? digits.replace(/./g, (one) => one + one) : digits
    const parts = [0, 2, 4].map((at) => Number.parseInt(full.slice(at, at + 2), 16))
    return `rgb(${parts[0]}, ${parts[1]}, ${parts[2]})`
  }
  const rgb = /^rgba?\(\s*(\d{1,3})\s*,\s*(\d{1,3})\s*,\s*(\d{1,3})\s*(?:,\s*(?:1|1\.0+)\s*)?\)$/i.exec(trimmed)
  if (rgb !== null) {
    const parts = [1, 2, 3].map((at) => Number.parseInt(rgb[at] ?? '', 10))
    return `rgb(${parts[0]}, ${parts[1]}, ${parts[2]})`
  }
  return null
}

/**
 * groundProblems is the whole assertion, as a list of sentences: empty when the
 * screen is painted from the shipped tokens, and one line per way it is not.
 * It is a pure function of a reading, which is what makes the unstyled case
 * testable without a window — `test/unit/goldens.test.ts` drives it with the
 * reading a mount with no stylesheet produces.
 *
 * The design's section 6.2 asked for "the computed background differs between
 * the schemes"; the owner's Q1 ruling of 2026-10-19 ships **one value set**, so
 * what the two runs are held to is that each computes the shipped `--ground` —
 * and, in `expectGrounded` below, that they therefore agree with each other.
 */
export function groundProblems(reading: GroundReading): string[] {
  const problems: string[] = []
  const ground = normalizeColour(reading.ground)
  const text = normalizeColour(reading.text)

  if (!reading.sheets.some((href) => href.endsWith(STYLESHEET))) {
    problems.push(
      `the document has loaded no ${STYLESHEET}, so nothing paints the screen: it links no stylesheet, or the one it links was refused (loaded: ${reading.sheets.join(', ') === '' ? 'none' : reading.sheets.join(', ')})`,
    )
  } else if (reading.rules === 0) {
    problems.push(`the document's ${STYLESHEET} holds no rules`)
  }

  if (ground === null) {
    problems.push(
      `${GROUND_TOKEN} is not declared on the document root (read ${JSON.stringify(reading.ground)}): the palette is not being applied`,
    )
  } else if (reading.groundPainted !== ground) {
    problems.push(
      `the app's ground paints ${reading.groundPainted}, not ${GROUND_TOKEN} (${reading.ground} = ${ground})`,
    )
  }

  if (text === null) {
    problems.push(
      `${TEXT_TOKEN} is not declared on the document root (read ${JSON.stringify(reading.text)}): the palette is not being applied`,
    )
  } else if (reading.textPainted === '') {
    problems.push(`the screen draws no ${reading.textHook} element, so there is no text colour to read`)
  } else if (reading.textPainted !== text) {
    problems.push(
      `the screen's text paints ${reading.textPainted}, not ${TEXT_TOKEN} (${reading.text} = ${text})`,
    )
  }

  return problems
}

/** GroundOptions is what `expectGrounded` needs beyond the page: which themes to
 * draw, where the screen is mounted, and which element its text is read from. */
export interface GroundOptions {
  /** the screen's own theme tuple (`LIVE_THEMES` and its three siblings) */
  readonly themes: readonly string[]
  /** the host the spec draws into (`#live-mount`) */
  readonly mount: string
  /** the screen's own heading, e.g. `h1.live-project` */
  readonly text: string
  /** how the failure names the screen, e.g. `the Live screen` */
  readonly screen: string
}

/**
 * expectGrounded draws the screen once per theme and holds every run to the
 * shipped tokens — and, because one value set ships (the owner's Q1 ruling of
 * 2026-10-19), holds the runs to the same ground as each other.
 *
 * **It is not a golden test and `FUGARO_CONSOLE_GOLDENS=skip` does not skip
 * it.** Nothing here reads a PNG: it is the assertion that the stylesheet the
 * console serves is the one the screens are drawn with, and it is the only case
 * in the suite that fails when the stylesheet stops being linked — which is
 * exactly the regression that would otherwise be recorded as a fresh golden.
 */
export async function expectGrounded(
  page: Page,
  draw: (theme: string) => Promise<void>,
  options: GroundOptions,
): Promise<void> {
  const readings: GroundReading[] = []
  for (const theme of options.themes) {
    await draw(theme)
    readings.push(await page.evaluate(readGround, { mount: options.mount, text: options.text }))
  }
  options.themes.forEach((theme, index) => {
    expect(
      groundProblems(readings[index] as GroundReading),
      `${options.screen} in the ${theme} run is not painted from the design tokens`,
    ).toEqual([])
  })
  // One value set ships, so the two runs stamp different `data-theme` values and
  // must paint the same ground. A second value set arriving without a task that
  // owns it fails here rather than in a recording nobody compared.
  const first = readings[0] as GroundReading
  for (let index = 1; index < readings.length; index++) {
    expect(
      (readings[index] as GroundReading).groundPainted,
      `${options.screen} paints ${(readings[index] as GroundReading).groundPainted} in the ${options.themes[index]} run and ${first.groundPainted} in the ${options.themes[0]} run: one value set ships (design section 3.6)`,
    ).toBe(first.groundPainted)
  }
}

/** ThemeStylesheet is one file of the theme directory: its name, and its path
 * below the console's root (which is what the failure message shows). */
export interface ThemeStylesheet {
  readonly name: string
  readonly path: string
}

/**
 * themeStylesheets is the theme directory's whole contents, in name order —
 * the same set `THEME_STYLESHEETS` names (`tools/bundle-renderer/index.ts`) and
 * the set `test/unit/bundle.test.ts` holds the bundler's list equal to. It is
 * read from the directory rather than restated here, so a stylesheet added to
 * the theme moves the freshness bar without an edit to this file.
 */
export function themeStylesheets(consoleDir: string): ThemeStylesheet[] {
  const relative = join(THEME_DIR)
  return readdirSync(join(consoleDir, relative))
    .filter((name) => name.endsWith(STYLESHEET_SUFFIX))
    .sort()
    .map((name) => ({ name: name, path: join(relative, name) }))
}

/**
 * stampMs is when a path was last touched, as milliseconds — **the commit that
 * last changed it**, and the file's own mtime only when git cannot answer (not
 * a repository, a shallow clone, a path git has never seen, an untracked file).
 *
 * The commit and not the mtime, because the mtime is not a fact a checkout
 * carries: `git clone` and `git checkout` write every file at the same moment,
 * so a golden and the stylesheet that supersedes it are the same age on every
 * machine that did not edit one of them by hand. The commit that last touched
 * each is what "the golden is older than the stylesheet" can mean here.
 */
export function stampMs(path: string, consoleDir: string): number {
  const committed = committedAtMs(path, consoleDir)
  if (committed !== null) return committed
  try {
    return statSync(path).mtimeMs
  } catch {
    return 0
  }
}

/** committedAtMs is `git log -1 --format=%ct` for one path, or null when git
 * cannot answer. Every failure is the same failure — no git, no repository, a
 * shallow clone, a path no commit names — and each of them means "fall back to
 * the file's own clock" rather than an error a golden test should report. */
function committedAtMs(path: string, consoleDir: string): number | null {
  try {
    const out = execFileSync('git', ['log', '-1', '--format=%ct', '--', path], {
      cwd: consoleDir,
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
    })
    const seconds = Number.parseInt(out.trim(), 10)
    return Number.isFinite(seconds) ? seconds * 1000 : null
  } catch {
    return null
  }
}

/**
 * recordCommand is the exact command that re-records one spec's goldens on the
 * platform it is run on, and it is the whole point of the failure below: the
 * recording is the owner's, `updateSnapshots: 'none'` writes nothing on its own,
 * and a reader told only "the screenshots do not match" has to reconstruct this
 * line from the specs' headers.
 *
 * `npm run build` is in it because `npx playwright test` does not build: the
 * specs load the compiled renderer and the served bundle out of `dist/`.
 */
export function recordCommand(spec: string, platform: string = process.platform): string {
  const xvfb = platform === 'linux' ? 'xvfb-run --auto-servernum ' : ''
  return `cd console && npm run build && ${xvfb}npx playwright test ${spec} --update-snapshots`
}

/**
 * recordingRun says whether the run a test is part of **writes baselines**
 * instead of only comparing them: `--update-snapshots` (`'changed'` by default,
 * or whatever mode was given) and Playwright's own `'default'` — which writes a
 * missing file and fails the test that created it — against the config's
 * `'none'` (`playwright.config.ts`, pinned by `test/unit/playwright.test.ts`).
 *
 * It is the matcher's own rule, not a second one: `handleMissing` writes whenever
 * the mode is not `'none'`, and the mode the matcher reads is the very
 * `testInfo.config.updateSnapshots` this is given.
 *
 * **Why `expectGolden` asks.** `assertGoldenFresh` *throws*, so a preflight that
 * ran on a recording run would stop the comparison before `toHaveScreenshot` —
 * the thing that writes the baseline — and the reader would be told to run the
 * command that had just failed with that same message. On Linux, whose baselines
 * are not committed, that is a recording that can never be taken at all.
 */
export function recordingRun(updateSnapshots: string): boolean {
  return updateSnapshots !== 'none'
}

/**
 * staleGolden is why a golden cannot be compared, or null when it can. Three
 * states, and each names the file and the command:
 *
 *   - **no golden at all** — nothing has been recorded for this spec on this
 *     platform (the baselines are owner-run, per platform);
 *   - **older than the stylesheet** — the picture predates the rules that paint
 *     the screens, so it is a picture of a tree that no longer exists, and this
 *     is the state the timestamp can see. **PR #629's darwin set is in it now**:
 *     those PNGs were newer than the newest theme file when they were committed
 *     (which is why the comparison, not this function, was what caught them
 *     then — see the header), and the theme has been committed to since, so this
 *     function refuses them by name and the comparison is never reached;
 *   - otherwise null, and the comparison decides.
 *
 * It is a question about a **comparison** run, and `expectGolden` is what decides
 * whether this run is one (`recordingRun`): a recording run is the state that
 * fixes all three, so refusing it would be the loop the recording exists to
 * break.
 */
export function staleGolden(goldenPath: string, spec: string, consoleDir: string): string | null {
  const record = recordCommand(spec)
  if (!existsSync(goldenPath)) {
    return `no golden is recorded at ${goldenPath}: the baselines are owner-run, once per platform, and \`updateSnapshots: 'none'\` refuses to write one during a run. Record it with:\n  ${record}`
  }
  const sheets = themeStylesheets(consoleDir)
  const newest = sheets
    .map((sheet) => ({ sheet: sheet, at: stampMs(join(consoleDir, sheet.path), consoleDir) }))
    .reduce<{ sheet: ThemeStylesheet | null; at: number }>(
      (best, one) => (one.at > best.at ? { sheet: one.sheet, at: one.at } : best),
      { sheet: null, at: 0 },
    )
  const goldenAt = stampMs(goldenPath, consoleDir)
  if (newest.sheet !== null && goldenAt < newest.at) {
    return `${goldenPath} is older than the stylesheet ${newest.sheet.path}: it is a picture of the tree before that file landed, so comparing against it would only report the difference between a styled screen and an unstyled one. Re-record it on this platform and commit the result:\n  ${record}`
  }
  return null
}

/**
 * assertGoldenFresh fails the golden test with the sentence above — the file,
 * why it cannot be compared, and the exact command — **before** the comparison,
 * so the report says what happened rather than printing a pixel diff against a
 * recording of the tree that preceded the stylesheet.
 *
 * `updateSnapshots` is the run's own mode, and a **recording** run is the one
 * case this stands aside for (`recordingRun`): it is the state that fixes every
 * reason a golden cannot be compared, and it is `toHaveScreenshot` — which this
 * would otherwise pre-empt, since it throws before the matcher runs — that writes
 * the file.
 */
export function assertGoldenFresh(
  goldenPath: string,
  spec: string,
  consoleDir: string,
  updateSnapshots: string,
): void {
  if (recordingRun(updateSnapshots)) return
  const stale = staleGolden(goldenPath, spec, consoleDir)
  if (stale !== null) throw new Error(stale)
}

/** GoldenWhere is what `expectGolden` needs beyond the locator and the name: the
 * spec file that owns the recording (which is what the re-record command names),
 * the console's own directory (which is the freshness clock's root), and the
 * running test (which is what resolves the recording's path). */
export interface GoldenWhere {
  /** the spec file that owns the recording, e.g. `live.spec.ts` */
  readonly spec: string
  /** the console's own directory: the specs' `consoleDir` */
  readonly consoleDir: string
  /** the running test: the recording's path is Playwright's own answer, not a
   * path restated here (the specs' `resolves the golden names` cases hold
   * `snapshotPath` to the directory the file is committed in) */
  readonly testInfo: TestInfo
}

/**
 * goldenMismatch is the sentence a failed comparison is re-reported with: the
 * matcher's own message (the diff ratio, the snapshot's name, the artifacts it
 * wrote), then what a recording older than the theme means, then the exact
 * command. A pure function of the three things it needs, so
 * `test/unit/goldens.test.ts` holds the command in it without a window.
 */
export function goldenMismatch(goldenPath: string, reported: string, spec: string): string {
  return (
    `${goldenPath} does not match what this tree draws.\n\n${reported}\n\n` +
    'A recording taken before the stylesheet landed is a picture of a tree that no longer ' +
    'exists, and this is what comparing against one looks like. Re-record it on this platform ' +
    `and commit the result:\n  ${recordCommand(spec)}`
  )
}

/**
 * expectGolden takes one recording, and it is the only way the six specs take
 * one — so that every golden in the suite fails with the same words, and with the
 * command that fixes it.
 *
 * The two failures a bare `toHaveScreenshot` reports badly, and what happens here
 * instead:
 *
 *   - **the recording cannot be compared at all** — there is none, or it predates
 *     the stylesheet that paints the screens. On a comparison run
 *     `assertGoldenFresh` runs first and says which, naming the file and the
 *     command, *before* a comparison against a picture of a tree that no longer
 *     exists can print a pixel diff nobody can read. It is deliberately not run on
 *     a **recording** run (`recordingRun`): the preflight throws, and the write is
 *     `toHaveScreenshot`'s own (`handleMissing` writes whenever the mode is not
 *     `'none'`), so guarding a recording would refuse the command it names — on
 *     Linux, whose baselines are not committed, no recording could be taken;
 *   - **the comparison failed.** The matcher's own message is kept — the diff
 *     ratio and the snapshot's name — and the exact re-record command is appended
 *     to it. The `-actual`/`-expected`/`-diff` images under `test-results/` are
 *     not lost either: the matcher attaches them to its own step as it fails,
 *     which happens before this catch re-reports the error. What the matcher
 *     prints by itself is
 *     `npx playwright test <file> --update-snapshots`, which is not that command:
 *     `npx playwright test` does not build, so a re-record run that way
 *     photographs whatever `dist/` happens to hold rather than this tree, and on
 *     Linux Electron needs a display. A reader handed a pixel diff of a screen
 *     that was recorded before the theme landed has to be told the command in the
 *     same breath, or the diff is all they get.
 *
 * The path is resolved by Playwright (`testInfo.snapshotPath`) rather than
 * rebuilt from the config's template, so the file this preflight looks at is the
 * file the comparison reads. `{arg}` gains no `-<index>` suffix here because no
 * test in the suite takes the same name twice.
 */
export async function expectGolden(
  locator: Locator,
  name: string | readonly string[],
  where: GoldenWhere,
): Promise<void> {
  const segments = typeof name === 'string' ? [name] : name
  const goldenPath = where.testInfo.snapshotPath(...segments)
  // The mode the matcher itself will read, so the preflight and the write can
  // never disagree about which run this is.
  assertGoldenFresh(goldenPath, where.spec, where.consoleDir, where.testInfo.config.updateSnapshots)
  try {
    await expect(locator).toHaveScreenshot(name)
  } catch (error) {
    throw new Error(goldenMismatch(goldenPath, error instanceof Error ? error.message : String(error), where.spec), {
      cause: error,
    })
  }
}
