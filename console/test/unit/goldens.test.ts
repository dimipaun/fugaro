// The golden specs' shared assertions, held without a window (desktop-console
// plan Task 6.6; design sections 6.1 and 6.2).
//
// The two things `test/support/goldens.ts` provides are asserted here rather
// than in the e2e pass, because both are decisions the specs make *before* and
// *after* a window exists:
//
//   - **the computed-ground assertion** (`groundProblems`, `normalizeColour`)
//     is a pure function of a reading, and the reading it has to fail on is the
//     one a mount with no stylesheet produces. That case is driven here with the
//     exact values such a mount reads — `--ground` undeclared, the body's
//     background `rgba(0, 0, 0, 0)`, the heading's colour the browser's own
//     `rgb(0, 0, 0)` — which is the red step the plan names for this task
//     ("the `<link>` removed from `index.html`"), driven without a display;
//   - **the recording's freshness** (`staleGolden`, `stampMs`) is a question
//     about two files' ages, so it is driven on files this test writes, whose
//     ages it sets: the golden older than the stylesheet, the golden newer, and
//     the golden that is not there at all.
//
// **The styled side of the reading is read out of the shipped palette**, not
// written down: `--ground` and `--text` come from `src/renderer/theme/
// palette.css`, so the case that says "a styled mount passes" is about the
// literals the console actually paints (the same rule `theme-tokens.test.ts`
// holds the contrast pairs to). A palette edited to move `--ground` moves this
// case with it, which is the point.
//
// What is *not* here, stated plainly: the DOM. `readGround` runs inside the
// window and nothing in the unit suite has one — `expectGrounded` is the thin
// glue around it, and the e2e pass is where it is exercised.

import { mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, utimesSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { afterAll, describe, expect, it } from 'vitest'

import {
  GROUND_TOKEN,
  STYLESHEET,
  TEXT_TOKEN,
  THEME_DIR,
  assertGoldenFresh,
  goldenMismatch,
  groundProblems,
  normalizeColour,
  recordCommand,
  recordingRun,
  staleGolden,
  themeStylesheets,
} from '../support/goldens'
import type { GroundReading } from '../support/goldens'

const consoleDir = fileURLToPath(new URL('../../', import.meta.url))

/** scratch is a directory of this test's own, one per case: the freshness cases
 * are about two files' ages, and a case that needed a checkout to be a
 * particular age could not be written at all. A scratch directory is not a git
 * repository, so `stampMs` falls back to the mtimes the case sets. */
const scratchDirs: string[] = []

afterAll(() => {
  for (const dir of scratchDirs) rmSync(dir, { recursive: true, force: true })
})

/** scratch makes a directory of its own for one case, so two cases can never
 * share a theme directory and one case's files can never age another's. */
function scratch(label: string): string {
  const dir = mkdtempSync(join(tmpdir(), `console-goldens-${label}-`))
  scratchDirs.push(dir)
  return dir
}

/** NOW is one reading of the clock for the whole file, in whole seconds: the
 * freshness cases compare two files' ages, and a case that read the clock twice
 * could straddle a second and compare ages that were meant to be equal. */
const NOW = Math.floor(Date.now() / 1000)

/** plant writes a file under `root` with its mtime set to `at` (whole seconds
 * since the epoch) and answers its path. */
function plant(root: string, relative: string, at: number): string {
  const path = join(root, relative)
  mkdirSync(dirname(path), { recursive: true })
  writeFileSync(path, relative.endsWith('.png') ? 'not really a PNG\n' : ':root { --ground: #0A0E16; }\n')
  utimesSync(path, at, at)
  return path
}

/** paletteValue is one colour token's literal as `palette.css` writes it: the
 * file the window is painted from, read rather than restated. The pattern is
 * anchored to the declaration (`^\s*--ground:`), so the prose in that file's own
 * header — which names the tokens without declaring them — cannot match it. */
function paletteValue(token: string): string {
  const palette = themeStylesheets(consoleDir).find((sheet) => sheet.name === 'palette.css')
  if (palette === undefined) throw new Error('the theme directory holds no palette.css')
  const text = readFileSync(join(consoleDir, palette.path), 'utf8')
  const match = new RegExp(`^\\s*${token}:\\s*([^;]+);`, 'm').exec(text)
  const value = match?.[1]
  if (value === undefined) throw new Error(`palette.css declares no ${token}`)
  return value.trim()
}

/** styledReading is the reading a screen drawn in the console's own window
 * produces: the document has loaded `/console.css`, the two tokens are declared,
 * and the two colours the compositor paints are those tokens. */
function styledReading(): GroundReading {
  const ground = paletteValue(GROUND_TOKEN)
  const text = paletteValue(TEXT_TOKEN)
  return {
    sheets: [`app://console/${STYLESHEET}`],
    rules: 4096,
    ground: ground,
    text: text,
    groundPainted: normalizeColour(ground) ?? '',
    textPainted: normalizeColour(text) ?? '',
    textHook: 'h1.live-project',
  }
}

/** UNSTYLED is the reading the same screen produces with the `<link>` removed
 * from the document — the plan's red step, and the state PR #629's baselines
 * were recorded in. Nothing declares a token, the body paints nothing, and the
 * heading is the browser's own black. */
const UNSTYLED: GroundReading = {
  sheets: [],
  rules: 0,
  ground: '',
  text: '',
  groundPainted: 'rgba(0, 0, 0, 0)',
  textPainted: 'rgb(0, 0, 0)',
  textHook: 'h1.live-project',
}

describe('the computed ground', () => {
  it('reads the shipped palette and passes a styled mount', () => {
    // The anchor: the literals below are the console's own, so this case cannot
    // pass on a palette that stopped declaring them.
    expect(paletteValue(GROUND_TOKEN)).toMatch(/^#[0-9A-Fa-f]{6}$/)
    expect(paletteValue(TEXT_TOKEN)).toMatch(/^#[0-9A-Fa-f]{6}$/)
    expect(groundProblems(styledReading())).toEqual([])
  })

  it("fails a mount with no stylesheet: the plan's red step for this task", () => {
    const problems = groundProblems(UNSTYLED)

    // Every way an unstyled mount is wrong is named, and named as itself: a
    // reader is told the document loaded no stylesheet and that the two tokens
    // are undeclared, rather than handed a pixel diff.
    expect(problems.length).toBeGreaterThan(0)
    expect(problems.join('\n')).toContain(`loaded no ${STYLESHEET}`)
    expect(problems.join('\n')).toContain(GROUND_TOKEN)
    expect(problems.join('\n')).toContain(TEXT_TOKEN)
  })

  it('fails a document that links no stylesheet even when the tokens are declared', () => {
    // The other direction, so the check is not satisfied by a token that happens
    // to be declared somewhere: the stylesheet is what has to be loaded.
    const reading = { ...styledReading(), sheets: ['app://console/installations.html'], rules: 0 }
    expect(groundProblems(reading).join('\n')).toContain(`loaded no ${STYLESHEET}`)
  })

  it('fails a styled document whose body paints nothing', () => {
    // The mutation a rule deletion makes: `--ground` is still declared and the
    // stylesheet is still loaded, and the ground is the compositor's.
    const reading = { ...styledReading(), groundPainted: 'rgba(0, 0, 0, 0)' }
    const problems = groundProblems(reading)
    expect(problems).toHaveLength(1)
    expect(problems[0]).toContain("the app's ground paints rgba(0, 0, 0, 0)")
    expect(problems[0]).toContain(paletteValue(GROUND_TOKEN))
  })

  it('fails a screen whose text is not painted from --text', () => {
    const reading = { ...styledReading(), textPainted: 'rgb(0, 0, 0)' }
    const problems = groundProblems(reading)
    expect(problems).toHaveLength(1)
    expect(problems[0]).toContain("the screen's text paints rgb(0, 0, 0)")
    expect(problems[0]).toContain(paletteValue(TEXT_TOKEN))
  })

  it('fails a screen that draws no text element to read', () => {
    // A selector that matches nothing is a screen that changed shape, and it
    // must not read as "the text colour is fine".
    const reading = { ...styledReading(), textPainted: '' }
    expect(groundProblems(reading).join('\n')).toContain('h1.live-project')
  })

  it('normalizes the two forms the two sides are written in', () => {
    expect(normalizeColour('#0A0E16')).toBe('rgb(10, 14, 22)')
    expect(normalizeColour('  #0a0e16 ')).toBe('rgb(10, 14, 22)')
    expect(normalizeColour('#fff')).toBe('rgb(255, 255, 255)')
    expect(normalizeColour('rgb(10, 14, 22)')).toBe('rgb(10, 14, 22)')
    expect(normalizeColour('rgba(10, 14, 22, 1)')).toBe('rgb(10, 14, 22)')
  })

  it('refuses a value that is not a plain colour rather than comparing it', () => {
    // A token written as a `var()` or a keyword cannot be compared with what the
    // compositor reports, and the reading has to say so instead of failing with
    // a difference between two strings that never could have matched.
    for (const value of ['', 'transparent', 'var(--ground)', 'currentColor', 'rgba(10, 14, 22, 0.5)']) {
      expect(normalizeColour(value), value).toBeNull()
    }
    expect(groundProblems({ ...styledReading(), ground: 'var(--something)' }).join('\n')).toContain(
      `${GROUND_TOKEN} is not declared`,
    )
  })
})

describe('the theme directory, as the freshness bar', () => {
  it('is read from the directory, and holds the palette the window is painted from', () => {
    const sheets = themeStylesheets(consoleDir)
    expect(sheets.length).toBeGreaterThan(0)
    for (const sheet of sheets) {
      expect(sheet.path.startsWith(THEME_DIR)).toBe(true)
      expect(sheet.name.endsWith('.css')).toBe(true)
      // Every name is a real file: the bundler refuses a listed file that is
      // missing, and this is the same list.
      expect(statSync(join(consoleDir, sheet.path)).isFile()).toBe(true)
    }
    expect(sheets.map((sheet) => sheet.name)).toContain('palette.css')
  })
})

describe('the tokens the reading is compared against', () => {
  /** declarationSelector is the selector of the rule that declares one token, in
   * a stylesheet with its comments stripped. It is a scan and not a parser, and
   * it does not need to be one: it is asked about `palette.css`'s two top-level
   * token blocks. */
  function declarationSelector(css: string, token: string): string {
    const bare = css.replace(/\/\*[\s\S]*?\*\//g, '')
    const at = bare.search(new RegExp(`(^|[;{\\s])${token}\\s*:`, 'm'))
    if (at < 0) throw new Error(`no rule declares ${token}`)
    const open = bare.lastIndexOf('{', at)
    const close = bare.lastIndexOf('}', at)
    // The selector is what stands between the previous rule's `}` and this
    // rule's `{` -- the match's own index is the whitespace in front of the
    // token, so `at` cannot be used as the start of the selector.
    return bare.slice(close + 1, open).trim()
  }

  it('are declared on the document root, which is where the e2e probe reads them', () => {
    // The half of `readGround` a unit test can hold. It reads `--ground` and
    // `--text` off `document.documentElement` (`getComputedStyle(...).getPropertyValue`),
    // and a custom property declared on `body` or on `[data-theme]` alone — the
    // screens stamp that attribute on their own roots — does not resolve there.
    // So the rule that declares them has to match the document root, or the e2e
    // assertion is red on a perfectly styled tree.
    const palette = themeStylesheets(consoleDir).find((sheet) => sheet.name === 'palette.css')
    if (palette === undefined) throw new Error('the theme directory holds no palette.css')
    const css = readFileSync(join(consoleDir, palette.path), 'utf8')

    for (const token of [GROUND_TOKEN, TEXT_TOKEN]) {
      const selectors = declarationSelector(css, token).split(',').map((one) => one.trim())
      expect(selectors, `${token} is not declared by a rule that matches the document root`).toContain(':root')
    }
  })

  it('refuses a token declared only on a screen root rather than on the document root', () => {
    // The other direction, so the case above is not satisfied by any selector
    // list that happens to hold the string: the reading is about the document
    // root and about nothing else.
    const planted = "[data-theme] {\n  --ground: #0A0E16;\n  --text: #E6EEF5;\n}\n"
    const selectors = declarationSelector(planted, GROUND_TOKEN).split(',').map((one) => one.trim())
    expect(selectors).not.toContain(':root')
  })
})

describe('the re-record command', () => {
  it('is the exact line, and it builds first', () => {
    // `npx playwright test` does not build, and the specs read `dist/`: a
    // command without the build re-records a picture of the previous build.
    expect(recordCommand('live.spec.ts', 'darwin')).toBe(
      'cd console && npm run build && npx playwright test live.spec.ts --update-snapshots',
    )
  })

  it('wraps itself in xvfb-run on Linux, where Electron needs a display', () => {
    expect(recordCommand('spend.spec.ts', 'linux')).toBe(
      'cd console && npm run build && xvfb-run --auto-servernum npx playwright test spend.spec.ts --update-snapshots',
    )
  })
})

describe('a failed comparison', () => {
  it('is re-reported with the file and the exact re-record command', () => {
    // The requirement in one case: a reader handed a pixel diff of a screen that
    // was recorded before the theme landed is told the file, told what such a
    // recording is, and told the command -- and the matcher's own words are kept
    // rather than replaced, so the diff ratio and the artifacts it wrote are
    // still in the report.
    const message = goldenMismatch(
      join(THEME_DIR, '..', '..', '__screenshots__', 'live-light-darwin.png'),
      '  Error: expect(locator).toHaveScreenshot(expected) failed\n  5231 pixels (ratio 0.02 of all image pixels) are different.',
      'live.spec.ts',
    )

    expect(message).toContain('live-light-darwin.png')
    expect(message).toContain('5231 pixels (ratio 0.02 of all image pixels) are different.')
    expect(message).toContain(recordCommand('live.spec.ts'))
    // And not the command the matcher prints by itself, which does not build.
    expect(recordCommand('live.spec.ts')).toContain('npm run build')
  })

  it('names the platform the re-record has to be taken on', () => {
    // The baselines are per platform, so the command is too: the same case on
    // Linux is the xvfb line, and the message a Linux reader sees has to be the
    // one that works there.
    expect(goldenMismatch('/tmp/x.png', 'mismatch', 'run.spec.ts')).toContain(
      recordCommand('run.spec.ts', process.platform),
    )
  })
})

describe('a golden older than the stylesheet', () => {
  it('is refused, with the file, the stylesheet and the exact command', () => {
    const root = scratch('stale')
    const sheet = join(THEME_DIR, 'screens.css')
    // The stylesheet landed after the picture was taken: the state a golden is
    // left in when the theme is edited and nothing is re-recorded. (PR #629's
    // darwin set is *not* this state — its PNGs were committed after the newest
    // theme file, so this guard cannot see them; the support module's header
    // says so.)
    plant(root, sheet, NOW - 60)
    const golden = plant(root, join('__screenshots__', 'live-light-darwin.png'), NOW - 6000)

    const stale = staleGolden(golden, 'live.spec.ts', root)

    expect(stale).not.toBeNull()
    expect(stale).toContain(golden)
    expect(stale).toContain(sheet)
    expect(stale).toContain(recordCommand('live.spec.ts'))
  })

  it('is not refused once the golden is newer than the stylesheet', () => {
    // What the owner's re-recording produces: the same two files, the golden
    // written after the stylesheet. The comparison, not this guard, decides
    // then.
    const root = scratch('fresh')
    plant(root, join(THEME_DIR, 'base.css'), NOW - 6000)
    const golden = plant(root, join('__screenshots__', 'spend', 'light-darwin.png'), NOW - 60)

    expect(staleGolden(golden, 'spend.spec.ts', root)).toBeNull()
  })

  it('is not refused when the two are the same age', () => {
    // A checkout writes both files at one moment, so "not newer" must not be
    // read as "older": the guard fires on strictly older, or it would fire on
    // every fresh clone.
    const root = scratch('same-age')
    plant(root, join(THEME_DIR, 'tokens.css'), NOW - 300)
    const golden = plant(root, join('__screenshots__', 'run', 'dark-darwin.png'), NOW - 300)

    expect(staleGolden(golden, 'run.spec.ts', root)).toBeNull()
  })

  it('is refused when there is no golden at all', () => {
    // The baselines are owner-run and a comparison run writes nothing, so "there
    // is no recording for this platform yet" is a state a reader has to be told
    // about in the same words.
    const root = scratch('missing')
    plant(root, join(THEME_DIR, 'screens.css'), NOW - 60)
    const missing = join(root, '__screenshots__', 'projects', 'dark-linux.png')

    const stale = staleGolden(missing, 'projects.spec.ts', root)

    expect(stale).not.toBeNull()
    expect(stale).toContain(missing)
    expect(stale).toContain(recordCommand('projects.spec.ts'))
  })
})

describe('a recording run, which is the one the preflight stands aside for', () => {
  it('is every mode but the config\'s none -- the matcher\'s own rule', () => {
    // `--update-snapshots` arrives as `changed` (the flag's preset), and `=all`
    // and `=missing` are the other two the CLI accepts; Playwright's `'default'`
    // writes a missing file and fails the test that created it. `'none'` is the
    // config's value here (`playwright.config.ts`) and the only mode that writes
    // nothing, which is the mode the freshness preflight belongs to.
    for (const mode of ['changed', 'all', 'missing', 'default']) {
      expect(recordingRun(mode), mode).toBe(true)
    }
    expect(recordingRun('none')).toBe(false)
  })

  it('is not refused by the freshness preflight, which would block its own fix', () => {
    // The regression this holds: `assertGoldenFresh` throws, and it runs before
    // `toHaveScreenshot`, so a preflight that fired on a recording run would stop
    // the matcher from writing the baseline -- and the message the reader got
    // would name the command that had just failed. On Linux, whose baselines are
    // not committed, no recording could be taken at all.
    const root = scratch('recording')
    plant(root, join(THEME_DIR, 'screens.css'), NOW - 60)
    const missing = join(root, '__screenshots__', 'run', 'dark-linux.png')

    // A comparison run refuses it, with the command ...
    expect(() => assertGoldenFresh(missing, 'run.spec.ts', root, 'none')).toThrow(
      recordCommand('run.spec.ts'),
    )
    // ... and a recording run does not, which is what lets that command write the
    // very file the message names.
    for (const mode of ['changed', 'all', 'missing', 'default']) {
      expect(() => assertGoldenFresh(missing, 'run.spec.ts', root, mode), mode).not.toThrow()
    }
  })

  it('stands aside for a stale golden too, not only a missing one', () => {
    // The other half: a recording run has to be able to replace a picture this
    // guard refuses — here one older than the stylesheet, and by the same token
    // one that is newer than it and still a picture of the tree before it landed
    // (PR #629's set, which only the comparison catches).
    const root = scratch('recording-stale')
    plant(root, join(THEME_DIR, 'screens.css'), NOW - 60)
    const golden = plant(root, join('__screenshots__', 'live-light-darwin.png'), NOW - 6000)

    expect(() => assertGoldenFresh(golden, 'live.spec.ts', root, 'none')).toThrow()
    expect(() => assertGoldenFresh(golden, 'live.spec.ts', root, 'changed')).not.toThrow()
  })
})
