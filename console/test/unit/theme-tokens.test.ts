// The theme's tokens and the palette they are painted in (desktop-console plan
// Task 6.1; docs/design/console-styling.md sections 3.1-3.4 and 6.3).
//
// The task's whole subject is that a colour is a number with a requirement attached,
// and that the number beside it in a design document is worth nothing until something
// computes it. So this file **reads `theme/palette.css` for its colour numbers** -- the
// literals the window paints, not a copy of them -- and checks three things about them:
//
//   - every pair the design's section 3.4 tabulates clears the ratio its role requires:
//     4.5:1 for body-size text (1.4.3) and 3:1 for the accent and the control border as
//     UI colours (1.4.11), with the ink on a filled control held to 4.5:1 because a
//     label is text;
//   - the value set is exactly the design's section 3.2 table, name by name and literal
//     by literal, so a value that drifted from the design fails here by name rather than
//     being reviewed as a slightly different blue;
//   - the vocabulary (`theme/tokens.css`) is complete and carries no colour at all, so a
//     step added to one layer and not the other fails by name.
//
// **The one deliberate departure from section 6.3, said here rather than left to be
// found.** The design's vocabulary case compares the dark set's token names against the
// light set's and refuses a token added to one and not the other; the owner ruled on
// 2026-10-19 that the console ships **dark only** (Q1, option (b) -- no light theme, the
// light set deferred), so `palette.css` declares one value set and there is no second one
// to compare against. What replaces that case is the exact-set case below, which is the
// same "a token cannot arrive on one side only" guarantee against the design's table, plus
// the case under it that a light set cannot arrive silently at all. The other two cases
// section 6.3 defers -- the `--border`-is-decorative scan of `base.css` and `screens.css`
// (Task 6.4) and the window-background comparison against the main process's window module
// (Task 6.5) -- are not here, because neither file exists yet and this file is in their
// Files lists for the commit that creates them.
//
// The arithmetic is the WCAG 2.1 relative-luminance definition, written out the way
// website/test/a11y.mjs writes it rather than imported from it: that file is a
// `node:test` module whose cases run on import, so importing it into vitest would execute
// a second suite inside this one. The formula is the same four lines, and the red step
// below (a deliberately failing fixture, `contrastFailures` printing
// `--text-muted on --surface is 3.1:1, needs 4.5:1` -- one decimal, because that is the
// string a11y.mjs's own committed fixture prints) is what keeps the two copies honest
// about being the same check.

import { readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

const THEME = fileURLToPath(new URL('../../src/renderer/theme', import.meta.url))
const PALETTE_FILE = 'palette.css'
const TOKENS_FILE = 'tokens.css'

/** readTheme reads one file of the theme directory, failing by name rather than with an
 * ENOENT from somewhere further along. */
function readTheme(name: string): string {
  try {
    return readFileSync(`${THEME}/${name}`, 'utf8')
  } catch (error) {
    throw new Error(
      `src/renderer/theme/${name} cannot be read: the token layer is two files and this ` +
        `test reads both of them from the tree (${String(error)})`,
    )
  }
}

/** readThemeDirectory reads every stylesheet of the theme directory as a Map of file name
 * to text, in name order. The "a light theme cannot arrive silently" case below walks this
 * rather than the two files this task writes: the directory is the theme, and Tasks 6.3 and
 * 6.4 add two more files to it. */
function readThemeDirectory(): Map<string, string> {
  const names = readdirSync(THEME)
    .filter((name) => name.endsWith('.css'))
    .sort()
  const files = new Map<string, string>()
  for (const name of names) files.set(name, readFileSync(`${THEME}/${name}`, 'utf8'))
  return files
}

/** LIGHT_THEME_SPELLINGS is the four ways a light theme could arrive in a theme file
 * without a task owning it. `source` is a snippet that spells it, planted by the case's
 * positive control; `test` is what the scan looks for, matched against the lower-cased
 * text because CSS keywords and attribute values are case-insensitive -- `LIGHT-DARK(` and
 * `[data-theme="light"]` are the same theme as the lower-case forms, and a check that knew
 * only the lower-case ones would let a light set in through a capital letter. Both quote
 * styles are matched for the same reason: `[data-theme]` is written by the screens, and
 * which quote they use is not this file's business. */
const LIGHT_THEME_SPELLINGS: readonly { readonly spelling: string; readonly source: string; readonly test: RegExp }[] = [
  { spelling: '`light dark`', source: 'color-scheme: light dark;', test: /light\s+dark/ },
  {
    spelling: '`light-dark()`',
    source: '--ground: light-dark(#F2F6FA, #0A0E16);',
    test: /light-dark\s*\(/,
  },
  {
    spelling: '`prefers-color-scheme`',
    source: '@media (prefers-color-scheme: light) { :root { color-scheme: light; } }',
    test: /prefers-color-scheme/,
  },
  {
    spelling: "a `light` data-theme",
    source: "[data-theme='light'] { color-scheme: light; }",
    test: /data-theme\s*=\s*['"]?light['"]?/,
  },
]

/** lightThemeOffences lists every theme file that spells a light theme, one line per file
 * and spelling, as `palette.css spells ->light dark<-: ...`. The scan is per file rather
 * than over the concatenation so the line can name the file a reader has to open. */
function lightThemeOffences(files: ReadonlyMap<string, string>): string[] {
  const offences: string[] = []
  for (const [name, css] of files) {
    const text = stripComments(css).toLowerCase()
    for (const { spelling, test } of LIGHT_THEME_SPELLINGS) {
      if (test.test(text)) {
        offences.push(
          `${name} spells ${spelling}; the console ships one value set, dark (the owner's Q1 ` +
            `ruling of 2026-10-19), so a light theme arrives only with a case that decides it`,
        )
      }
    }
  }
  return offences
}

/** stripComments removes CSS comments before anything is parsed. It is load-bearing in
 * both directions: these files explain their values and may quote them, so a colour named
 * in prose is not a colour the window paints -- and the reverse, the "no light theme"
 * checks below, must not trip on the comment that says what the light set would be. */
function stripComments(css: string): string {
  return css.replace(/\/\*[\s\S]*?\*\//g, '')
}

/** declarations reads a stylesheet's custom properties as a Map of name (leading dashes
 * stripped) to value. A tiny declaration reader rather than a CSS parser: these two files
 * are flat blocks of `--name: value;` and the checks below are about which names exist and
 * what they hold. */
function declarations(css: string): Map<string, string> {
  const out = new Map<string, string>()
  for (const [, name, value] of stripComments(css).matchAll(/--([a-zA-Z0-9-]+)\s*:\s*([^;{}]+);/g)) {
    out.set(name ?? '', (value ?? '').trim())
  }
  return out
}

// ---- colour arithmetic ---------------------------------------------------------------

/** parseColour reads an opaque hex literal. A value that is not one is refused rather
 * than skipped: a colour function, an alpha channel or a `var()` indirection is a pair
 * this file cannot compute, and a pair it cannot compute is exactly the pair a contrast
 * regression would hide in. */
function parseColour(value: string | undefined, name: string): [number, number, number] {
  const hex = /^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/.exec((value ?? '').trim())
  if (hex === null) {
    throw new Error(
      `${name}: ${JSON.stringify(value)} is not an opaque hex colour. Every pair below is ` +
        `computed from these literals, so a colour function, an alpha channel or a var() ` +
        `here would be a pair this test cannot see`,
    )
  }
  const digits = hex[1]?.length === 3 ? [...(hex[1] ?? '')].map((c) => c + c).join('') : (hex[1] ?? '')
  return [0, 2, 4].map((i) => parseInt(digits.slice(i, i + 2), 16)) as [number, number, number]
}

/** channelLuminance is one sRGB channel's linear value, the transfer function WCAG 2.1
 * defines relative luminance over. */
function channelLuminance(channel: number): number {
  const c = channel / 255
  return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
}

/** luminance is WCAG 2.1's relative luminance of an sRGB triple. */
function luminance(rgb: readonly [number, number, number]): number {
  const [r, g, b] = rgb.map(channelLuminance) as [number, number, number]
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

/** contrastRatio is `(L1+0.05)/(L2+0.05)`, lighter one first. */
function contrastRatio(fg: readonly [number, number, number], bg: readonly [number, number, number]): number {
  const a = luminance(fg)
  const b = luminance(bg)
  const [hi, lo] = a >= b ? [a, b] : [b, a]
  return (hi + 0.05) / (lo + 0.05)
}

/** hslHue is the HSL hue of an sRGB triple, in degrees. It is section 3.4's second
 * channel: the five state colours have to be separable by hue, because lightness is the
 * channel a colour-blind operator and a forced-colors mode keep. */
function hslHue(rgb: readonly [number, number, number]): number {
  const [r, g, b] = rgb.map((channel) => channel / 255) as [number, number, number]
  const max = Math.max(r, g, b)
  const min = Math.min(r, g, b)
  const d = max - min
  if (d === 0) return 0
  let h: number
  if (max === r) h = 60 * (((g - b) / d) % 6)
  else if (max === g) h = 60 * ((b - r) / d + 2)
  else h = 60 * ((r - g) / d + 4)
  return h < 0 ? h + 360 : h
}

/** hueGap is the circular distance between two hues: `min(|Δh|, 360-|Δh|)`. */
function hueGap(a: number, b: number): number {
  const d = Math.abs(a - b)
  return d > 180 ? 360 - d : d
}

// ---- the pairs the palette paints ------------------------------------------------------

// WCAG 2.1 AA is two numbers, and a pair belongs to one of them by its role: 4.5:1 is the
// ratio for body-size text (1.4.3) -- a sentence, a label, a figure -- and 3:1 is the
// ratio for the UI colours of 1.4.11, where a colour has to be perceivable to be operated
// rather than read as prose.
const BODY_TEXT = 4.5
const LARGE_OR_UI = 3

/** SURFACES are the three painted backgrounds every token is measured against, in the
 * column order section 3.4 prints them. */
const SURFACES = ['--ground', '--surface', '--surface-alt'] as const

/** TEXT_ON_SURFACE are the tokens section 3.4's dark table measures against all three
 * surfaces at the body-text ratio. `--accent` is on the list because the design's row for
 * it says "4.5 ✓ (and 3 ✓ as a UI boundary)": it clears both, and the boundary ratio is
 * asserted separately below. */
const TEXT_ON_SURFACE = ['--text', '--text-muted', '--accent-ink', '--accent', '--ok', '--warn', '--error', '--halted', '--stale'] as const

/** BOUNDARY_ON_SURFACE are the two colours that mark a control's extent: the accent -- a
 * focus ring, a filled control -- and the strong border. 3:1, and not 4.5, is what a
 * boundary needs. */
const BOUNDARY_ON_SURFACE = ['--accent', '--border-strong'] as const

/** FILLS are the filled controls a label sits on: section 3.4's ink-on-fill table, the
 * accent and the five states. */
const FILLS = ['--accent', '--ok', '--warn', '--error', '--halted', '--stale'] as const

/** STATES are the five state colours, in the order section 3.4's pair table lists them.
 * Every pair of them is at least 50° apart in hue. */
const STATES = ['--ok', '--warn', '--error', '--halted', '--stale'] as const

/** PAIRS is every pair section 3.4's dark table tabulates, as `[foreground, background,
 * required ratio]`. It is the design's table written as a list a loop can walk, and the
 * counting case below holds its length to the table's. */
const PAIRS: readonly (readonly [string, string, number])[] = [
  ...TEXT_ON_SURFACE.flatMap((token) => SURFACES.map((surface) => [token, surface, BODY_TEXT] as const)),
  ...BOUNDARY_ON_SURFACE.flatMap((token) => SURFACES.map((surface) => [token, surface, LARGE_OR_UI] as const)),
  ...FILLS.map((fill) => ['--ink-on-fill', fill, BODY_TEXT] as const),
]

/** contrastFailures lists the pairs of a value set that are below their ratio, as the
 * lines a reader sees -- `--text-muted on --surface is 3.1:1, needs 4.5:1`, one decimal,
 * which is the string website/test/a11y.mjs formats. Exported through the module's own
 * call so the deliberately failing fixture at the bottom of this file runs the same
 * function the palette cases run: the check that refuses a bad palette here is the check
 * the red step was recorded against, and it cannot rot into a comment about a check that
 * no longer refuses anything. */
function contrastFailures(
  tokens: ReadonlyMap<string, string>,
  pairs: readonly (readonly [string, string, number])[] = PAIRS,
): string[] {
  const value = (name: string): string | undefined => tokens.get(name.replace(/^--/, ''))
  const failures: string[] = []
  for (const [fg, bg, need] of pairs) {
    const ratio = contrastRatio(parseColour(value(fg), fg), parseColour(value(bg), bg))
    if (ratio < need) failures.push(`${fg} on ${bg} is ${ratio.toFixed(1)}:1, needs ${need}:1`)
  }
  return failures
}

/** overlap is the intersection of two name lists, in the first list's order. */
function overlap(a: readonly string[], b: ReadonlySet<string>): string[] {
  return a.filter((name) => b.has(name))
}

// ---- the files ------------------------------------------------------------------------

const paletteCss = readTheme(PALETTE_FILE)
const tokensCss = readTheme(TOKENS_FILE)

const palette = declarations(paletteCss)
const tokens = declarations(tokensCss)

/** VALUE_SET is the design's section 3.2 table: the dark set, fourteen tokens, with the
 * `--ink-on-fill` of section 3.4's fill table as the fifteenth declaration. The seven the
 * website's palette-logo.css already ships are here byte for byte (`--warn`'s amber most
 * of all: it is the site's, not this design's invention); `--accent-ink` is that file's
 * `--accent-on-dark` under a name that does not name the wrong one of two grounds; and
 * the six others -- the second surface, the strong border, and the four state colours --
 * are this console's own. */
const VALUE_SET: Readonly<Record<string, string>> = Object.freeze({
  '--ground': '#0A0E16',
  '--surface': '#121A26',
  '--surface-alt': '#18222F',
  '--text': '#E6EEF5',
  '--text-muted': '#9FB2C4',
  '--accent': '#17A8E6',
  '--accent-ink': '#6FD8F5',
  '--border': '#24303F',
  '--border-strong': '#5B7491',
  '--ok': '#4CC38A',
  '--warn': '#F5A524',
  '--error': '#F2648C',
  '--halted': '#C9A0FF',
  '--stale': '#8A9AAB',
  '--ink-on-fill': '#0A0E16',
})

/** VOCABULARY is the design's section 3.1 table: the steps a rule may reach for. Every
 * one of them is a size, a radius, a weight, a duration or a font stack -- there is no
 * colour in the list, and none may be added to it. */
const VOCABULARY = [
  '--space-3xs',
  '--space-2xs',
  '--space-xs',
  '--space-sm',
  '--space-md',
  '--space-lg',
  '--space-xl',
  '--space-2xl',
  '--radius-sm',
  '--radius-md',
  '--radius-pill',
  '--border-width',
  '--rule-width',
  '--font-sans',
  '--font-mono',
  '--font-size-xs',
  '--font-size-sm',
  '--font-size-base',
  '--font-size-md',
  '--font-size-lg',
  '--leading-tight',
  '--leading-normal',
  '--weight-normal',
  '--weight-medium',
  '--weight-semibold',
  '--row-height',
  '--row-height-compact',
  '--focus-ring-width',
  '--focus-ring-offset',
  '--duration-fast',
  '--ease-out',
] as const

// ---- the cases ------------------------------------------------------------------------

describe("the console's theme tokens", () => {
  it('reads both files, so no case below can pass on an empty read', () => {
    // Non-vacuity first: a parser that read nothing would make every set comparison and
    // every loop below true and mean nothing.
    expect(palette.size, `${PALETTE_FILE} declares no custom property`).toBeGreaterThanOrEqual(15)
    expect(tokens.size, `${TOKENS_FILE} declares no custom property`).toBeGreaterThanOrEqual(31)
    // And the values are hex, which is what the pair cases need: `parseColour` throws on
    // anything else, so this says so once rather than fifteen times.
    for (const [name, value] of palette) expect(() => parseColour(value, `--${name}`)).not.toThrow()
  })

  it("is the design's value set, name by name and literal by literal", () => {
    const declared = [...palette.keys()].sort()
    const wanted = Object.keys(VALUE_SET).map((name) => name.replace(/^--/, '')).sort()
    // Both directions: a token the design names and the file does not is a rule that will
    // read an unset variable, and a token the file carries that the design does not is a
    // colour no ratio in section 3.4 was computed for.
    expect(declared, `${PALETTE_FILE} does not declare exactly the design's value set`).toEqual(wanted)
    for (const [name, value] of Object.entries(VALUE_SET)) {
      expect(palette.get(name.replace(/^--/, '')), `${name} has drifted from the design's section 3.2 table`).toBe(value)
    }
    // The ink on a fill is the ground on this set, which is why the dark set's label on a
    // filled control is light-on-dark. It is written as a literal rather than
    // `var(--ground)` so the pair cases can parse it; this is what makes the repetition a
    // checked fact instead of a copy that can drift.
    expect(palette.get('ink-on-fill'), '--ink-on-fill is the dark set\'s --ground').toBe(palette.get('ground'))
  })

  it('clears the ratio every pair of the design keeps requires', () => {
    // The count first: PAIRS is section 3.4's dark table written as a list, and a pair
    // dropped from the list is a pair this case would stop checking. Nine tokens on three
    // surfaces, the two boundaries on three, and the ink on six fills.
    expect(PAIRS).toHaveLength(TEXT_ON_SURFACE.length * SURFACES.length + BOUNDARY_ON_SURFACE.length * SURFACES.length + FILLS.length)
    // And the failures are empty: each line names its own pair and the ratio it computed,
    // so a token edited to break one requirement fails by name rather than by number --
    // the lines are in the failure message for exactly that reason, since a bare `[]` diff
    // would leave a reader to work out which token moved.
    const failures = contrastFailures(palette)
    expect(failures, `below the ratio its role requires: ${failures.join('; ')}`).toEqual([])
  })

  it('keeps --border decorative: below the 3:1 a control boundary needs', () => {
    // Section 3.4 prints --border's ratios unbolded and below 3:1, and that is the whole
    // reason a rule may use it as a hairline and never as a control's extent. A border
    // that crept over 3:1 would be a boundary the design should have named as one, so it
    // fails here rather than being discovered in a screenshot.
    for (const surface of SURFACES) {
      const ratio = contrastRatio(parseColour(palette.get('border'), '--border'), parseColour(palette.get(surface.replace(/^--/, '')), surface))
      expect(ratio, `--border on ${surface} is ${ratio.toFixed(2)}:1`).toBeLessThan(LARGE_OR_UI)
    }
  })

  it('separates the five state colours by hue, so no two fills rely on lightness', () => {
    // Section 3.4's rule: any two of the five state colours are at least 50° apart in hue,
    // **or** at least 3:1 apart in luminance -- the fallback for a future pair the amber-to-
    // red arc crowds. The five present colours take the first arm and none of them comes
    // near the second, which is deliberate: every state colour also clears 4.5:1 as text on
    // all three surfaces, so they all sit in one narrow luminance band, and a state pair
    // that leaned on lightness would collapse to the same grey under forced colors.
    for (const [index, a] of STATES.entries()) {
      for (const b of STATES.slice(index + 1)) {
        const gap = hueGap(hslHue(parseColour(palette.get(a.replace(/^--/, '')), a)), hslHue(parseColour(palette.get(b.replace(/^--/, '')), b)))
        const contrast = contrastRatio(parseColour(palette.get(a.replace(/^--/, '')), a), parseColour(palette.get(b.replace(/^--/, '')), b))
        expect(
          gap >= 50 || contrast >= 3,
          `${STATES.join(', ')}: ${a} and ${b} are ${gap.toFixed(1)}° apart and ${contrast.toFixed(2)}:1 apart; section 3.4's rule needs one of the two`,
        ).toBe(true)
      }
    }
  })

  it('carries the vocabulary, and no colour in it', () => {
    for (const name of VOCABULARY) {
      expect(tokens.has(name.replace(/^--/, '')), `${TOKENS_FILE} does not declare ${name}`).toBe(true)
    }
    // The two layers are disjoint, and that is the whole of "one source of truth per fact":
    // a colour belongs in the palette, where every pair above can read it, and a step
    // belongs in the vocabulary. A name in both would be a second place a colour is set.
    // Both sides are spelled the way `declarations()` reads them, which is without the
    // leading `--`: comparing `VOCABULARY` as written against the palette's keys was a
    // comparison of `--space-3xs` with `ground`, so it was empty whatever the files said.
    expect(
      overlap(VOCABULARY.map((name) => name.replace(/^--/, '')), new Set(palette.keys())),
      'a name is declared in both layers',
    ).toEqual([])
    // A colour spelled in the vocabulary in any of its three forms is an offence: the
    // palette is the only place a colour is written, so that one file's literals are the
    // ones every ratio is computed from.
    const NAMED_COLOURS = /\b(?:aliceblue|aqua|black|blue|crimson|cyan|fuchsia|gold|gray|green|grey|indigo|ivory|khaki|lime|magenta|maroon|navy|olive|orange|orchid|pink|plum|purple|rebeccapurple|red|salmon|silver|tan|teal|tomato|turquoise|violet|wheat|white|yellow)\b/i
    const COLOUR_FUNCTION = /\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color|color-mix)\s*\(/i
    const HEX_LITERAL = /#[0-9a-f]{3,8}\b/i
    for (const [name, value] of tokens) {
      expect(NAMED_COLOURS.test(value) || COLOUR_FUNCTION.test(value) || HEX_LITERAL.test(value), `${TOKENS_FILE}: --${name} is ${JSON.stringify(value)}, which carries a colour`).toBe(false)
    }
    // And nothing but colour is in the palette: a spacing step written here would be a
    // step the value set has no business holding.
    expect(overlap([...palette.keys()], new Set(VOCABULARY.map((name) => name.replace(/^--/, '')))), 'a step is declared in the palette').toEqual([])
  })

  it('ships one value set, dark, and a light one cannot arrive silently', () => {
    // The owner's ruling of 2026-10-19 (Q1, option (b)): dark only, the light set deferred.
    // The four spellings in LIGHT_THEME_SPELLINGS are the four ways a light theme could
    // creep back in without a task owning it -- the OS-driven pair, the per-token colour
    // function, the media query, and a block keyed on the attribute the screens already
    // stamp -- and each is an offence here rather than a review note.
    //
    // **The scan is over the whole theme directory, not over this task's two files**, which
    // is what the README and the design's section 3.6 promise: Task 6.3's `base.css` and
    // Task 6.4's `screens.css` land in the same directory and are read by this case the
    // moment they exist, so a light set arriving in one of them fails here without anyone
    // remembering to add the file to a list.
    const files = readThemeDirectory()
    expect(files.size, `${THEME} holds no stylesheet to scan`).toBeGreaterThanOrEqual(2)
    for (const name of [PALETTE_FILE, TOKENS_FILE]) {
      expect(files.has(name), `${THEME} does not hold ${name}`).toBe(true)
    }

    const joined = [...files.values()].map(stripComments).join('\n')
    expect(joined, `no theme file fixes color-scheme to dark`).toMatch(/color-scheme:\s*dark;/)
    expect(lightThemeOffences(files), 'a theme file spells a light theme').toEqual([])

    // The positive control: the case above is only worth what it refuses, so each spelling
    // is planted in a real file of the real directory and the scan has to name it. A temp
    // file rather than a map built in memory, because the thing under test is the directory
    // walk -- a walk that was narrowed back to two files would still pass an in-memory
    // control while missing `base.css`.
    const planted = `${THEME}/.light-theme-control.css`
    try {
      LIGHT_THEME_SPELLINGS.forEach(({ spelling, source }, index) => {
        writeFileSync(planted, `/* control ${index} */\n:root { color-scheme: light; }\n${source}\n`)
        const offences = lightThemeOffences(readThemeDirectory())
        expect(
          offences.some((line) => line.includes('.light-theme-control.css') && line.includes(spelling)),
          `planting ${spelling} in a file of the theme directory was not reported: ${offences.join('; ')}`,
        ).toBe(true)
      })
    } finally {
      rmSync(planted, { force: true })
    }
    expect(lightThemeOffences(readThemeDirectory()), 'the control file was left behind').toEqual([])
  })

  it('refuses a pair below AA, with the line the red step recorded', () => {
    // The red step, kept: the fixture is a two-token palette, and the pair is named in the
    // case rather than taken from the console's own values -- the real --text-muted on
    // --surface is 8.02:1 on this set, so no real value produces this line. The surface is
    // the one website/test/a11y.mjs's own fixture uses and the grey is the one its
    // committed line prints, so the two files refuse the same pair with the same sentence.
    const fixture = new Map([
      ['surface', '#14141F'],
      ['text-muted', '#646464'],
    ])
    const lines = contrastFailures(fixture, [['--text-muted', '--surface', BODY_TEXT]])
    expect(lines, `the check printed: ${lines.join('; ')}`).toEqual([
      '--text-muted on --surface is 3.1:1, needs 4.5:1',
    ])
  })
})
