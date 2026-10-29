// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See console/LICENSE.md for the terms.
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
//   - the vocabulary (`theme/tokens.css`) is exactly the design's section 3.1 table and
//     carries no colour at all, so a step added to one layer and not the other fails by
//     name in either direction.
//
// **The one deliberate departure from section 6.3, said here rather than left to be
// found.** The design's vocabulary case compares the dark set's token names against the
// light set's and refuses a token added to one and not the other; the owner ruled on
// 2026-10-19 that the console ships **dark only** (Q1, option (b) -- no light theme, the
// light set deferred), so `palette.css` declares one value set and there is no second one
// to compare against. What replaces that case is the exact-set case below, which is the
// same "a token cannot arrive on one side only" guarantee against the design's table, plus
// the case under it that a light set cannot arrive silently at all. **And the two cases
// section 6.3 defers to a task that writes what they read are both here now**: the
// `--border`-is-decorative scan arrived with **Task 6.4**, which created `screens.css` (the
// second of the two stylesheets it reads; `base.css` is Task 6.3's), and the
// **window-background comparison** against the main process's window module arrived with
// **Task 6.5**, which is the task that gave `consoleWindowOptions` its `backgroundColor`
// literal -- the case reads that literal out of the options the window is built with, so
// before 6.5 there was nothing for it to compare against.
//
// The arithmetic is the WCAG 2.1 relative-luminance definition, written out the way
// website/test/a11y.mjs writes it rather than imported from it: that file is a
// `node:test` module whose cases run on import, so importing it into vitest would execute
// a second suite inside this one. The formula is the same four lines, and the red step
// below (a deliberately failing fixture, `contrastFailures` printing
// `--text-muted on --surface is 3.1:1, needs 4.5:1` -- one decimal, because that is the
// string a11y.mjs's own committed fixture prints) is what keeps the two copies honest
// about being the same check.

import { mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { describe, expect, it } from 'vitest'

import { consoleWindowOptions } from '../../src/main/window'

const THEME = fileURLToPath(new URL('../../src/renderer/theme', import.meta.url))
const PALETTE_FILE = 'palette.css'
const TOKENS_FILE = 'tokens.css'

/** LINE_SHEETS are the two stylesheets that draw rules: `tokens.css` and `palette.css` are
 * values and declare no border at all, so the `--border`-is-decorative scan reads these two
 * (design §6.3 names them, and they are the files Tasks 6.3 and 6.4 wrote). */
const LINE_SHEETS: readonly string[] = Object.freeze(['base.css', 'screens.css'])

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

/** readThemeDirectory reads every stylesheet of a directory as a Map of file name to text,
 * in name order. The "a light theme cannot arrive silently" case below walks this rather
 * than the files one task writes: the directory is the theme, and the tasks after this one
 * add their own files to it.
 *
 * **The directory is a parameter, and the positive controls hand it a temporary one.** The
 * controls have to plant a file for the walk to find, and planting it in the real
 * `src/renderer/theme/` would write into the tree every other test reads: `secret-hygiene`
 * walks `src` and reads each entry, and the design's §6.2 case scans this very directory for
 * `.css` files, so a file that exists for the length of one control is a file another
 * worker can read half-written or fail to find. Nothing in this suite writes inside `src`. */
function readThemeDirectory(directory: string): Map<string, string> {
  const names = readdirSync(directory)
    .filter((name) => name.endsWith('.css'))
    .sort()
  const files = new Map<string, string>()
  for (const name of names) files.set(name, readFileSync(join(directory, name), 'utf8'))
  return files
}

/** withTempThemeDirectory hands `body` a temporary copy of the theme directory — the real
 * stylesheets, in a directory of its own — and removes it afterwards whatever happens. The
 * copy holds the real files, so a control exercises the same walk the case does: a walk that
 * had been narrowed back to the two known names would miss a planted file and fail its own
 * control, which is the point of running the control at all. */
function withTempThemeDirectory<T>(body: (directory: string) => T): T {
  const directory = mkdtempSync(join(tmpdir(), 'fugaro-theme-control-'))
  try {
    for (const [name, css] of readThemeDirectory(THEME)) writeFileSync(join(directory, name), css)
    return body(directory)
  } finally {
    rmSync(directory, { recursive: true, force: true })
  }
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

/** RETRACTED_REASONS are the sentences the design's §3.2 retracted: `--accent-ink` was
 * first justified as the site's `--accent-on-dark` renamed away from naming the wrong one
 * of two grounds, and the owner's Q1 ruling of 2026-10-19 ships one ground, so §3.2 says
 * the rename "stands on the name alone". `test` is what the scan looks for, matched against
 * the lower-cased comment text. */
const RETRACTED_REASONS: readonly { readonly spelling: string; readonly source: string; readonly test: RegExp }[] = [
  { spelling: 'the wrong one of two grounds', source: 'under a name that does not name the wrong one of two grounds', test: /wrong one of (?:the )?two grounds/ },
  { spelling: 'two grounds', source: 'the console has two grounds, so "on-dark" would name the wrong one of them', test: /\btwo grounds\b/ },
]

/** commentOffences lists every theme file whose **comments** carry a reason the design has
 * retracted, as `palette.css's comments say ->two grounds<-: ...`. It is the mirror of the
 * checks below rather than another one of them: every scan in this file strips comments
 * first — a colour named in prose is not a colour the window paints, and a comment that
 * spells `light-dark()` is not a light theme — so a comment is exactly where a reason that
 * is no longer true survives unread, quoting the sentence the design took back. The reader
 * of a shipped file is who the comment is for, and it is the reader who is told the wrong
 * reason. */
function commentOffences(files: ReadonlyMap<string, string>): string[] {
  const offences: string[] = []
  for (const [name, css] of files) {
    const comments = css.match(/\/\*[\s\S]*?\*\//g)?.join('\n').toLowerCase() ?? ''
    for (const { spelling, test } of RETRACTED_REASONS) {
      if (test.test(comments)) {
        offences.push(
          `${name}'s comments say ${spelling}; the design's §3.2 retracted that reason — with one ` +
            `value set shipping there is one ground, and the rename stands on the name alone`,
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

// ---- the `--border`-is-decorative scan ------------------------------------------------

/** RULE is one CSS rule's selector and body, on text whose comments are stripped. A rule
 * inside an `@media` block is what it matches: the at-rule's own wrapper has a `{` before
 * any `}` and so is not a match, which is exactly the behaviour wanted -- the scan is about
 * the declarations, and every declaration in this file is inside some rule. */
const RULE = /([^{}]+)\{([^{}]*)\}/g

/** CONTROL_SELECTOR is a selector that names something the operator activates: an anchor,
 * a button, a field, or one of the installation picker's control ids
 * (`installations.html` styles its controls by id rather than by class, design section
 * 2.2). A **box** drawn on one of these is the control's extent, and the token that draws
 * an extent is `--border-strong`. The box is load-bearing: the rail's rows are buttons and
 * the design draws the `--border` hairline *between* them (`border-bottom` on
 * `button.console-rail-row`), which is a separator rather than an extent -- a one-sided
 * border is a rule, and only an all-sides one is a box. */
const CONTROL_SELECTOR =
  /(?:^|[\s,>+~])(?:a|button|input|select|textarea)(?![\w-])|#picker-(?:add-submit|pick-[a-z]+|project)/

/** BorderUse is one declaration that reads `--border`: where it is, and what it draws. */
interface BorderUse {
  readonly selector: string
  readonly property: string
  readonly value: string
}

/** borderUses is every declaration in a stylesheet whose value is `var(--border)` exactly
 * -- not `--border-width` and not `--border-strong`, which are different tokens with
 * different roles. */
function borderUses(css: string, name: string): readonly BorderUse[] {
  const out: BorderUse[] = []
  for (const [, selector, body] of stripComments(css).matchAll(RULE)) {
    for (const declaration of (body ?? '').split(';')) {
      const colon = declaration.indexOf(':')
      if (colon === -1) continue
      const property = declaration.slice(0, colon).trim().toLowerCase()
      const value = declaration.slice(colon + 1).trim()
      if (!/var\(--border\)/.test(value)) continue
      if (property === '') throw new Error(`${name}: a declaration with no property reads --border`)
      out.push({ selector: selector ?? '', property, value })
    }
  }
  return out
}

/** borderOffences is every place a stylesheet uses `--border` where the design says it may
 * not (section 3.4's row: "not admissible as a control boundary -- 1 px hairlines only").
 * Three clauses, and each is a different way for a decorative token to start carrying a
 * meaning: an `outline`, which is a boundary wherever it is drawn; a width other than
 * `--rule-width`, which is a hairline's; and a box (`border` or `border-color`) on a
 * control, whose extent the design gives to `--border-strong`. */
function borderOffences(files: ReadonlyMap<string, string>): string[] {
  const offences: string[] = []
  for (const [name, css] of files) {
    for (const { selector, property, value } of borderUses(css, name)) {
      const where = `${name}: ${selector.trim()} { ${property}: ${value} }`
      if (property.startsWith('outline')) {
        offences.push(`${where} -- an outline is a boundary, and --border is decoration`)
      } else if (!value.includes('var(--rule-width)')) {
        offences.push(`${where} -- --border is a 1 px hairline and its width is --rule-width`)
      } else if ((property === 'border' || property === 'border-color') && CONTROL_SELECTOR.test(selector)) {
        offences.push(`${where} -- a box on a control, whose extent is --border-strong`)
      }
    }
  }
  return offences
}

// ---- where `--ok` may be painted -------------------------------------------------------

/** OK_SELECTORS are the only selectors a rule may paint `--ok` on, and the list is the
 * design's licence rather than the code's usage: section 3.5 rule 4 gives the tone
 * **exactly two** readings -- "measured and inside the cap" and the rail's `running` row
 * (section 3.2's role cell says the same two) -- and forbids it decoratively (section 6.1
 * rule 3), so an entry here is a reading the design names and nothing else.
 *
 * **The two entries are those two readings, and the file's own history is why the
 * distinction is written down.** The list once held a third, a Projects panel the CLI could
 * `read`; the rule that painted it `--ok` was transcribed into this list rather than
 * derived from the design, so the case whose whole purpose is to keep `--ok` to its
 * licensed readings accepted a reading the design licenses nowhere -- a "read" panel is not
 * "measured and inside the cap" and is not the rail's `running` row. The panel's rule draws
 * `--accent` now (a rule is structure, which is what that token's role cell gives it), and
 * a reading added to rule 4 means amending the design in the same edit and adding its
 * selector here. */
const OK_SELECTORS: readonly string[] = Object.freeze([
  "div.live-bar[data-state='measured']",
  "span.console-rail-state[data-state='running']",
])

/** okSelectors is every selector of every rule whose body paints `--ok`, in name order. A
 * rule whose body uses the token for a `border-color` is a painted `--ok` too: the
 * sparkline's measured bar is a rule rather than a text colour. */
function okSelectors(css: string): readonly string[] {
  const out: string[] = []
  for (const [, selector, body] of stripComments(css).matchAll(RULE)) {
    if (!/var\(--ok\)/.test(body ?? '')) continue
    for (const one of (selector ?? '').split(',')) out.push(one.trim())
  }
  return out.filter((one) => one !== '').sort()
}

// ---- where `--accent-ink` may be painted ------------------------------------------------

/** INK_PROPERTIES are the declarations a rule may read `--accent-ink` in, and the list is
 * the design's role cell rather than a preference: section 3.2 gives the token one job --
 * "the accent **as text** -- links, an accent heading" -- while the cell above it gives the
 * **rules** to `--accent` ("structure: rules, the focus ring, a filled control, the cap bar
 * below its threshold"), and section 4.1 draws the rail's selected row's 3 px left border in
 * that token. So a border, an outline or a fill painted `--accent-ink` is the text tone used
 * for structure: the same misreading that put `--ok` on a stage and a panel. */
const INK_PROPERTIES: readonly string[] = Object.freeze(['color'])

/** accentInkOffences is every declaration that reads `--accent-ink` for something other than
 * text, as `screens.css: li.live-run-queued { border-left: 3px solid var(--accent-ink) }`. It
 * is the mirror of `okSelectors`: that one asks *where* a state tone is painted and holds a
 * list of selectors, while this one asks *what* an ink tone is painted as and reads every
 * declaration of both files, so a border added later is caught without anyone remembering to
 * name it here. */
function accentInkOffences(files: ReadonlyMap<string, string>): string[] {
  const offences: string[] = []
  for (const [name, css] of files) {
    for (const [, selector, body] of stripComments(css).matchAll(RULE)) {
      for (const declaration of (body ?? '').split(';')) {
        const colon = declaration.indexOf(':')
        if (colon === -1) continue
        const property = declaration.slice(0, colon).trim().toLowerCase()
        const value = declaration.slice(colon + 1).trim()
        if (!/var\(--accent-ink\)/.test(value)) continue
        if (INK_PROPERTIES.includes(property)) continue
        offences.push(
          `${name}: ${(selector ?? '').trim()} { ${property}: ${value} } -- --accent-ink is ` +
            `the accent as text (design section 3.2), and structure is --accent's`,
        )
      }
    }
  }
  return offences
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
 * `--accent-on-dark` under a name that says what the token is for -- the accent as text
 * (§3.2: with one ground shipping, the rename stands on the name alone); and
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
    // dropped from the list is a pair this case would stop checking. **The number is the
    // literal**, not the same expression PAIRS is built from -- `TEXT_ON_SURFACE.length *
    // SURFACES.length + ...` is true by construction, so it held whatever the three lists
    // held, and a pair dropped from any of them passed it. 39 is section 3.4's dark table
    // counted from the table: the nine text tokens on the three surfaces (27), the two
    // boundaries on the three (6), and `--ink-on-fill` on the six fills (6). The table's
    // tenth measured row is `--border-strong`, which is the second of those boundaries and
    // not a text token, and its `--border` row is the decorative hairline the case below
    // holds below 3:1 rather than a pair this one measures.
    expect(PAIRS).toHaveLength(39)
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

  it('uses --border only as a hairline, on no control and in no outline', () => {
    // The design's §6.3 case, and the reason it is this task's: it reads `base.css` and
    // `screens.css` rather than `palette.css`, and both of those exist as of Task 6.4.
    // Section 3.4 prints `--border`'s ratios below 3:1 and unbolded -- "not admissible as
    // a control boundary, 1 px hairlines only" -- so the token's whole licence is a rule
    // between rows, and the three clauses below are the three ways a hairline stops being
    // one: it is drawn as an outline, it is drawn thicker than `--rule-width`, or it is
    // drawn around something the operator activates, whose extent is `--border-strong`.
    const sheets = new Map(LINE_SHEETS.map((name): [string, string] => [name, readTheme(name)]))
    const uses = [...sheets].flatMap(([name, css]) => borderUses(css, name))
    expect(
      uses.length,
      `${LINE_SHEETS.join(' and ')} draw no --border hairline at all, so this case is vacuous`,
    ).toBeGreaterThan(10)
    const offences = borderOffences(sheets)
    expect(offences, `--border used where the design does not allow it: ${offences.join('; ')}`).toEqual([])

    // And the three refusals are real, planted: a scan that refused nothing would pass the
    // case above, and one that refused everything would pass it too. Four offences, three
    // of them for the three clauses and the fourth a second reading of the width clause; a
    // decorative hairline on a row is not one of them, and neither is a hairline between a
    // control and its neighbour -- the shape the rail's own rows are drawn in.
    const planted = [
      'li.row { border-bottom: var(--rule-width) solid var(--border); }',
      'button.rail-row { border-bottom: var(--rule-width) solid var(--border); }',
      'button.go { border: var(--rule-width) solid var(--border); }',
      '#picker-project { border: var(--border-width) solid var(--border); }',
      '.panel { border: var(--border-width) solid var(--border); }',
      '.panel:focus-visible { outline: 2px solid var(--border); }',
    ].join('\n')
    const plantedOffences = borderOffences(new Map([['planted.css', planted]]))
    expect(plantedOffences.map((one) => one.split(' -- ')[1])).toEqual([
      'a box on a control, whose extent is --border-strong',
      '--border is a 1 px hairline and its width is --rule-width',
      '--border is a 1 px hairline and its width is --rule-width',
      'an outline is a boundary, and --border is decoration',
    ])
    expect(borderOffences(new Map([['clean.css', 'li.row { border-bottom: var(--rule-width) solid var(--border); }']]))).toEqual([])
  })

  it('paints --ok on the readings section 3.5 rule 4 licenses, and nowhere else', () => {
    // The regression this keeps out is a colour that *looks* like a state and is not one:
    // `span.run-stage-passed { color: var(--ok) }` painted a stage green, and that class
    // holds **both** of the stage's words -- `passed` and `failed`, in one class with no
    // attribute (design section 2.6) -- so the rule read a failed stage green. `--ok` is
    // reserved for measured-and-inside-cap and the rail's running (section 3.5 rule 4,
    // "never decoratively" per section 6.1 rule 3); a stage is neither reading, and no
    // tone can be right for a class that carries two states.
    const painted = [...LINE_SHEETS].flatMap((name) => okSelectors(readTheme(name)))
    expect(painted, 'the scan read no --ok use at all, so this case is vacuous').not.toEqual([])
    expect(painted.sort(), 'a rule paints --ok where section 3.5 rule 4 does not license it').toEqual(
      [...OK_SELECTORS].sort(),
    )

    // And the clause is real: a planted state rule that is not one of the two readings is
    // refused, which is what the case above would have said about the stage rule -- and
    // about the Projects panel that held a third entry here, which is the reading the
    // design licenses nowhere.
    const planted = okSelectors('span.run-stage-passed { color: var(--ok); }')
    expect(planted).toEqual(['span.run-stage-passed'])
    expect(
      planted.filter((one) => !OK_SELECTORS.includes(one)),
      'a planted --ok use the licence list accepted',
    ).toEqual(['span.run-stage-passed'])
    expect(
      okSelectors("section.projects-panel[data-state='read'] { border-left: 3px solid var(--ok); }").filter(
        (one) => !OK_SELECTORS.includes(one),
      ),
      "a read Projects panel painted --ok passed the licence list; the design's two readings are the cap bar and the rail's running row",
    ).toEqual(["section.projects-panel[data-state='read']"])
  })

  it('paints --accent-ink as text, and draws no rule or fill with it', () => {
    // The regression this keeps out is the mirror of the one above: `--accent-ink` is the
    // accent **as text** (design section 3.2's role cell) and `--accent` is the accent as
    // **structure**, so a rule that paints a border, an outline or a fill with the ink token
    // has used the text tone for structure. `li.live-run-queued`'s left border did exactly
    // that; the token it wants is `--accent`, which is what section 4.1 draws the rail's
    // selected row's own left border in.
    const files = new Map(LINE_SHEETS.map((name) => [name, readTheme(name)]))
    const inkUses = [...files.values()].flatMap((css) => stripComments(css).match(/var\(--accent-ink\)/g) ?? [])
    expect(inkUses.length, 'the scan read no --accent-ink use at all, so this case is vacuous').toBeGreaterThan(0)
    expect(accentInkOffences(files), 'a rule draws something that is not text with --accent-ink').toEqual([])

    // And the clause is real: the planted border is refused, and the same border in
    // `--accent` is not -- so the case is about the token and not about borders.
    expect(
      accentInkOffences(new Map([['planted.css', "li.live-run-queued { border-left: 3px solid var(--accent-ink); }"]])),
    ).toEqual([
      'planted.css: li.live-run-queued { border-left: 3px solid var(--accent-ink) } -- --accent-ink is the accent as text (design section 3.2), and structure is --accent\'s',
    ])
    expect(
      accentInkOffences(new Map([['planted.css', 'li.live-run-queued { border-left: 3px solid var(--accent); }']])),
    ).toEqual([])
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
    // And the other direction, which is the half the file header promises and which the
    // loop above cannot see: every name `tokens.css` declares is in the design's section
    // 3.1 table. One direction alone is "the design's steps are all present", and a step
    // added to `tokens.css` on its own -- a spacing step nothing in the design names, a
    // colour spelled as a fourth thing, a typo's twin -- passed every case here. It is
    // the same exact-set assertion the palette has, on the layer that had none.
    expect(
      [...tokens.keys()].sort(),
      `${TOKENS_FILE} declares names the design's section 3.1 vocabulary does not`,
    ).toEqual(VOCABULARY.map((name) => name.replace(/^--/, '')).sort())
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
    const files = readThemeDirectory(THEME)
    expect(files.size, `${THEME} holds no stylesheet to scan`).toBeGreaterThanOrEqual(2)
    for (const name of [PALETTE_FILE, TOKENS_FILE]) {
      expect(files.has(name), `${THEME} does not hold ${name}`).toBe(true)
    }

    const joined = [...files.values()].map(stripComments).join('\n')
    expect(joined, `no theme file fixes color-scheme to dark`).toMatch(/color-scheme:\s*dark;/)
    // The real theme directory passes both lookings: no file spells a light theme, and no
    // file's comments carry the reason §3.2 took back.
    expect(lightThemeOffences(files), 'a theme file spells a light theme').toEqual([])
    expect(commentOffences(files), "a theme file's comments justify a value with a retracted reason").toEqual([])

    // The positive controls, each in a temporary copy of the directory: the cases above are
    // only worth what they refuse, so every spelling is planted in a file of that copy and
    // the scan has to name it. A copy rather than a map built in memory, because the thing
    // under test is the directory walk -- a walk narrowed back to two files would still pass
    // an in-memory control while missing `base.css` -- and a copy rather than the real
    // directory, because a file written inside `src` for the length of a control is a file
    // the rest of the suite (`secret-hygiene`'s walk, Task 6.2's own scan of this directory)
    // can read half-written or fail to find.
    withTempThemeDirectory((directory) => {
      const planted = join(directory, '.light-theme-control.css')
      LIGHT_THEME_SPELLINGS.forEach(({ spelling, source }, index) => {
        writeFileSync(planted, `/* control ${index} */\n:root { color-scheme: light; }\n${source}\n`)
        const offences = lightThemeOffences(readThemeDirectory(directory))
        expect(
          offences.some((line) => line.includes('.light-theme-control.css') && line.includes(spelling)),
          `planting ${spelling} in a file of the theme directory was not reported: ${offences.join('; ')}`,
        ).toBe(true)
      })
      // And the same for the comment scan: the retracted reason planted in the comments of a
      // file of the directory has to be reported, in the name of the file that carries it.
      const commentControl = join(directory, '.retracted-control.css')
      RETRACTED_REASONS.forEach(({ spelling, source }) => {
        writeFileSync(commentControl, `/* ${source} */\n:root { color-scheme: dark; }\n`)
        const offences = commentOffences(readThemeDirectory(directory))
        expect(
          offences.some((line) => line.includes('.retracted-control.css') && line.includes(spelling)),
          `planting ${spelling} in a file's comments was not reported: ${offences.join('; ')}`,
        ).toBe(true)
      })
      rmSync(planted, { force: true })
      rmSync(commentControl, { force: true })
      // And the copy is clean again once the controls are removed, so neither assertion
      // above is passing on a file of the control's own leaving.
      expect(lightThemeOffences(readThemeDirectory(directory)), 'the control file was left behind').toEqual([])
      expect(commentOffences(readThemeDirectory(directory)), 'the control file was left behind').toEqual([])
    })
  })

  it('paints the window the ground the stylesheet paints, so the first frame is not a flash', () => {
    // Task 6.5, and the case design section 6.3 deferred to it: "the literal arrives in
    // `src/main/window.ts` here, so this is the first task at which there is anything to
    // compare it against". `backgroundColor` is what the platform paints in the frame
    // between the window being created and the document's first paint, so a value that
    // drifted from `--ground` is a white (or stale) flash on every launch -- a defect no
    // screenshot of a *painted* window can see, which is why it is checked here.
    //
    // The literal is read **out of the options the window is built with**, not out of the
    // exported constant: a `backgroundColor` dropped from `consoleWindowOptions` fails this
    // case as surely as one that drifted, and the constant is an implementation detail of
    // how it is spelled.
    //
    // **One literal, one direction** -- the ruled scope. One value set ships (the owner's Q1
    // ruling of 2026-10-19, design section 3.6), so there is one `--ground` for the window to
    // paint; the two-set design's second literal and its `nativeTheme` read are the record of
    // option (a), not a pair of assertions this case owes.
    const ground = palette.get('ground')
    expect(ground, 'palette.css declares no --ground: the window has nothing to be equal to').toBeDefined()
    const painted = consoleWindowOptions().backgroundColor
    expect(
      painted,
      `the window paints ${JSON.stringify(painted)} behind the document and --ground is ${JSON.stringify(ground)}`,
    ).toBe(ground)
    // The comparison is the literal and not the colour: `#0a0e16` is the same colour in
    // lower case, and the equality above is what a reader comparing the two files by eye
    // cannot do.
    expect(ground).toBe('#0A0E16')
  })

  it('keeps the focus ring one :focus-visible rule, drawn from the tokens', () => {
    // The focus ring is one of the requirements Task 6.5's brief names and the rule itself
    // is Task 6.3's (`base.css`); what this case holds is that it stays the single rule
    // design section 4.3 writes, because the mutation it guards is one character away and
    // changes who can see it at all: `:focus` draws a ring on a mouse click, which is the
    // thing the rule is written as `:focus-visible` to avoid.
    //
    // Three claims, and the third is the one a rewrite would lose first: the ring's width
    // and offset come from the two tokens (`tokens.css`), and its colour is `--accent` --
    // the token section 3.4 measures against the ground for exactly this role, 7.16:1.
    const css = stripComments(readTheme('base.css'))
    const focusRules = [...css.matchAll(RULE)]
      .map(([, selector, body]) => ({ selector: (selector ?? '').trim(), body: body ?? '' }))
      .filter(({ selector }) => selector.includes(':focus'))
    expect(
      focusRules.map(({ selector }) => selector),
      'base.css does not draw exactly one focus rule',
    ).toEqual([':focus-visible'])
    const ring = focusRules[0]?.body ?? ''
    expect(ring, 'the focus ring is not the design section 4.3 rule').toContain(
      'outline: var(--focus-ring-width) solid var(--accent)',
    )
    expect(ring, 'the focus ring does not read the offset token').toContain(
      'outline-offset: var(--focus-ring-offset)',
    )

    // And no file of the theme selects `:focus` alone, however it is spelled -- the scan is
    // over the directory rather than over `base.css`, so a later layer cannot add the second
    // rule this case exists to refuse.
    const bare: string[] = []
    for (const [name, text] of readThemeDirectory(THEME)) {
      for (const _ of stripComments(text).matchAll(/:focus(?!-visible)/g)) bare.push(name)
    }
    expect(bare, 'a theme file selects :focus rather than :focus-visible').toEqual([])

    // The probe: the scan above is only worth what it finds, so the two spellings are read
    // off a snippet that carries them.
    expect([...':focus-visible'.matchAll(/:focus(?!-visible)/g)]).toHaveLength(0)
    expect([...'a:focus { outline: 0 }'.matchAll(/:focus(?!-visible)/g)]).toHaveLength(1)
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
