// website/test/a11y.mjs — the accessibility check over the design tokens (website plan, W1.2;
// reconciliation findings 53 and 54, decisions D104 and D105, and U5).
//
// This file's first case is contrast: every pair the site paints is computed from the
// palette files themselves, and every one of them has to clear the WCAG 2.1 ratio its role
// requires. The check reads the stylesheets and nothing else — no browser, no computed
// style, no built page — which is what makes it the first case of the site's accessibility
// contract rather than a report on a rendered artifact: a palette that cannot pass here
// cannot be shipped by a component that happens to render correctly today.
//
// It is deliberately the *source* that is read and not a copy of it (Review Focus: "the
// test must read the source, not the generator's own output"). The three palette files are
// the values; `tokens.css` is the vocabulary; and the cases below hold the two together —
// one palette imported, the same ten colour tokens in all three files, no colour in the
// semantic layer, and every designed pair above its ratio.
//
// The red step this file was written against, and which its last case keeps: run the check
// against a deliberately failing pair and it reports
// `--text-muted on --surface is 3.1:1, needs 4.5:1` — the line the task's red step
// recorded, produced by the same function the palette cases use, so the refusal cannot rot
// into a comment about a check that no longer refuses anything.
//
// W1.11 extends this file (the plan's §7 puts it in L5) with the rest of the accessibility
// contract — the reduced-motion path, keyboard reachability and focus visibility, the
// landmark structure, one `h1` per page, and alt text — all of which need the built HTML.
// The helpers below are exported so that task reuses this file's colour arithmetic rather
// than writing a second copy of it that could disagree with this one.
//
// It runs under plain `node --test website/test/a11y.mjs`: `node:test`, `node:assert` and
// the standard library only, and no network, so it works before and after W1.1's toolchain
// lands (Global Constraint 4).

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const STYLES_DIR = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', 'src', 'styles')
const TOKENS_FILE = 'tokens.css'

// The three candidates D104 put to the owner, and the one the owner chose on 2026-10-16.
// The files are named here rather than discovered by a glob on purpose: a fourth palette
// nobody asked for, or a palette file that lost its name, has to fail this check rather
// than be read as one more candidate.
export const PALETTE_FILES = ['palette-violet.css', 'palette-orange.css', 'palette-split.css']
export const PALETTE_IN_FORCE = 'palette-split.css'

// The colour vocabulary every palette is a value set for, and the roles the contrast pairs
// below depend on. A palette that dropped one of these would make a pair in this file
// unreadable rather than merely unset, so the case that holds the three files together
// requires every one of them.
const COLOUR_TOKENS = [
  'ground',
  'surface',
  'text',
  'text-muted',
  'accent',
  'accent-on-dark',
  'border',
  'warn',
  'gradient-from',
  'gradient-to',
]

// The gradient has exactly two stops, and that is a promise the contrast pairs rest on:
// text over the hero is text over `--gradient-from` or over `--gradient-to`, so a third
// stop would be a colour the pair set does not cover (U5).
const GRADIENT_STOPS = ['gradient-from', 'gradient-to']

// One representative of each family of the semantic layer, which is the vocabulary a page
// reads. A representative rather than the whole list, because a page may add a spacing step
// without touching this file, and a family that lost its last token may not.
const REQUIRED_SEMANTIC_TOKENS = [
  'space-sm',
  'space-md',
  'space-lg',
  'radius-md',
  'font-sans',
  'font-mono',
  'font-size-base',
  'font-size-lg',
  'leading-normal',
  'weight-semibold',
  'measure',
  'page-max',
  'focus-ring-width',
  'focus-ring-offset',
  'duration-base',
]

// ---- reading the stylesheets -----------------------------------------------------------

// Comments are stripped before anything is parsed, and that is load-bearing in both
// directions: a palette's header explains the values and may quote them, and a colour
// mentioned in prose is not a colour the page paints. The rule this file enforces is about
// declarations.
export function stripComments(css) {
  return css.replace(/\/\*[\s\S]*?\*\//g, '')
}

// The custom properties a stylesheet declares, as a Map of name (without the leading `--`)
// to value. A tiny declaration reader rather than a CSS parser: the token files are flat
// `:root` blocks of `--name: value;` and nothing else, and the checks below are about which
// names exist and what they hold.
export function declarations(css) {
  const out = new Map()
  for (const [, name, value] of stripComments(css).matchAll(/--([a-zA-Z0-9-]+)\s*:\s*([^;{}]+);/g)) {
    out.set(name, value.trim())
  }
  return out
}

function readStyleFile(fileName) {
  const file = path.join(STYLES_DIR, fileName)
  assert.ok(
    existsSync(file),
    `${fileName} is missing: the site's design tokens live in website/src/styles/ and this check reads them from the repository, not from a build output`,
  )
  return readFileSync(file, 'utf8')
}

export function paletteTokens(fileName) {
  return declarations(readStyleFile(fileName))
}

function tokensCss() {
  return readStyleFile(TOKENS_FILE)
}

// The stylesheets a palette import names, in order, as written in the file.
function importedFiles(css) {
  return [...stripComments(css).matchAll(/@import\s+["']([^"']+)["']/g)].map((m) => m[1])
}

// ---- colour arithmetic -----------------------------------------------------------------

// A palette value has to be an opaque hex literal, and a value that is not is refused
// rather than skipped: a colour function, an alpha channel or a `var()` indirection is a
// pair this check cannot compute, and a pair it cannot compute is exactly the pair a
// contrast regression would hide in.
export function parseColour(value, name) {
  assert.equal(
    typeof value,
    'string',
    `${name} is not declared: every palette is a complete value set, and a token that is missing from one of them cannot be checked`,
  )
  const hex = /^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/.exec(value.trim())
  assert.ok(
    hex,
    `${name}: ${JSON.stringify(value)} is not an opaque hex colour. The contrast check computes every pair from these literals, so a colour function, an alpha channel or a var() here would be a pair it cannot see`,
  )
  const digits = hex[1].length === 3 ? [...hex[1]].map((c) => c + c).join('') : hex[1]
  return [0, 2, 4].map((i) => parseInt(digits.slice(i, i + 2), 16))
}

// WCAG 2.1 relative luminance, and the contrast ratio built from it.
function channelLuminance(channel) {
  const c = channel / 255
  return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
}

export function luminance(rgb) {
  const [r, g, b] = rgb.map(channelLuminance)
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

export function contrastRatio(fg, bg) {
  const a = luminance(fg)
  const b = luminance(bg)
  const [hi, lo] = a >= b ? [a, b] : [b, a]
  return (hi + 0.05) / (lo + 0.05)
}

// ---- the pairs the site paints ---------------------------------------------------------

// WCAG 2.1 AA is two numbers, and a pair belongs to one of them by its role. 4.5:1 is the
// ratio for body text (1.4.3) — a sentence, a link, a figure, a button's label — and 3:1 is
// the ratio for large text and for the UI colours of 1.4.11, where a colour has to be
// perceivable to be operated rather than read as prose.
const BODY_TEXT = 4.5
const LARGE_OR_UI = 3

// `--border` is deliberately absent. It is a decorative separator: WCAG 1.4.11 applies to a
// component or a graphic a reader has to perceive in order to operate the page, and a
// hairline dividing two blocks is neither. The boundaries that do carry meaning are the
// accent — a focus ring, a filled control — and every one of those is a pair below.
//
// The gradient stops are pairs like any other background (U5): the hero is where the site's
// largest type sits, so exempting it would be exempting the one place a contrast failure is
// most visible.
export const PAIRS = [
  // Body copy on the ground, on a card, and on each stop of the hero's gradient.
  ['--text', '--ground', BODY_TEXT],
  ['--text', '--surface', BODY_TEXT],
  ['--text', '--gradient-from', BODY_TEXT],
  ['--text', '--gradient-to', BODY_TEXT],

  // The quieter tone beside it, which is still prose and still has to be readable.
  ['--text-muted', '--ground', BODY_TEXT],
  ['--text-muted', '--surface', BODY_TEXT],
  ['--text-muted', '--gradient-from', BODY_TEXT],
  ['--text-muted', '--gradient-to', BODY_TEXT],

  // The accent as text: every link, and every heading that carries the accent. This is the
  // token whose whole job is to be the legible form of `--accent`.
  ['--accent-on-dark', '--ground', BODY_TEXT],
  ['--accent-on-dark', '--surface', BODY_TEXT],
  ['--accent-on-dark', '--gradient-from', BODY_TEXT],
  ['--accent-on-dark', '--gradient-to', BODY_TEXT],

  // Warnings and cost figures, which are text on both grounds, and the dark label on a
  // filled warning chip.
  ['--warn', '--ground', BODY_TEXT],
  ['--warn', '--surface', BODY_TEXT],
  ['--ground', '--warn', BODY_TEXT],

  // The accent as a fill and as structure: a filled button's label is text on the accent,
  // and the accent itself is a focus ring and a rule against the ground and the surface.
  ['--ground', '--accent', BODY_TEXT],
  ['--accent', '--ground', LARGE_OR_UI],
  ['--accent', '--surface', LARGE_OR_UI],
  ['--accent', '--gradient-from', LARGE_OR_UI],
  ['--accent', '--gradient-to', LARGE_OR_UI],
]

// The pairs of a palette that are below their ratio, as the lines a reader sees. Exported
// with a `pairs` parameter so that the red-step case at the bottom of this file can run the
// same function over a pair that is meant to fail.
export function contrastFailures(tokens, pairs = PAIRS) {
  // `tokens` is a Map of name to value with the leading dashes stripped, which is how
  // `declarations` reads a stylesheet; the pair table spells a token the way a stylesheet
  // does, so the dashes come off here and nowhere else.
  const value = (name) => tokens.get(name.replace(/^--/, ''))
  const failures = []
  for (const [fg, bg, need] of pairs) {
    const ratio = contrastRatio(parseColour(value(fg), fg), parseColour(value(bg), bg))
    if (ratio < need) {
      failures.push(`${fg} on ${bg} is ${ratio.toFixed(1)}:1, needs ${need}:1`)
    }
  }
  return failures
}

// ---- the palette in force --------------------------------------------------------------

test('tokens.css imports exactly one palette, and it is the one D104 chose', () => {
  const css = tokensCss()
  const imports = importedFiles(css)

  assert.deepEqual(
    imports.length,
    1,
    `tokens.css imports ${imports.length} stylesheets (${imports.join(', ')}): the palette in force is selected by one import and by nothing else, so a second one would make which palette applies depend on the bundler's order`,
  )

  const imported = path.basename(imports[0])
  assert.ok(
    PALETTE_FILES.includes(imported),
    `tokens.css imports ${imports[0]}, which is not one of D104's three candidates (${PALETTE_FILES.join(', ')})`,
  )
  assert.equal(
    imported,
    PALETTE_IN_FORCE,
    `tokens.css imports ${imported}, but the palette in force is ${PALETTE_IN_FORCE}: the owner answered D104 on 2026-10-16 with palette C — violet for structure and links, orange for warnings and cost. The other two palettes ship as the documented alternatives; changing the palette in force is this one line`,
  )

  // A CSS `@import` after the first rule is dropped by the browser, so the palette would
  // silently not apply — a page in the browser's default colours rather than a failed
  // build. The import therefore has to precede every declaration in the file.
  const stripped = stripComments(css)
  const importAt = stripped.search(/@import/)
  const firstDeclaration = stripped.search(/--[a-zA-Z0-9-]+\s*:/)
  assert.ok(
    importAt >= 0 && (firstDeclaration === -1 || importAt < firstDeclaration),
    'tokens.css declares a token before its @import: CSS drops an @import that follows a rule, so the palette in force would not apply at all',
  )
})

test('tokens.css is the semantic layer, and shares no name with the palette', () => {
  const semantic = declarations(tokensCss())
  const palette = paletteTokens(PALETTE_IN_FORCE)

  for (const name of REQUIRED_SEMANTIC_TOKENS) {
    assert.ok(
      semantic.has(name),
      `tokens.css does not declare --${name}: the semantic layer is the vocabulary a page reads (spacing, radius, type, layout, focus and motion), and a page that cannot name a step cannot use one`,
    )
  }

  // The two layers are disjoint, and that is the whole of "one source of truth per fact": a
  // colour belongs in a palette, where the contrast check can read it for all three
  // candidates at once, and a semantic token belongs here, where it is a step rather than a
  // value. A name in both would be a second place a colour could be set.
  for (const name of semantic.keys()) {
    assert.ok(
      !palette.has(name),
      `tokens.css declares --${name}, which ${PALETTE_IN_FORCE} also declares: the palette is the only place a colour is set, so that the contrast check can compute every pair for every candidate`,
    )
  }

  assertColourFree(semantic)
})

test('the three palettes are one vocabulary in three value sets', () => {
  const sets = new Map()
  for (const file of PALETTE_FILES) {
    sets.set(file, new Set(paletteTokens(file).keys()))
  }

  const [first, ...rest] = PALETTE_FILES
  for (const file of rest) {
    for (const name of sets.get(first)) {
      assert.ok(
        sets.get(file).has(name),
        `${file} does not define --${name}, which ${first} does: every palette is a complete value set for the same vocabulary, so a token added to one of them is added to all three (and to the pair set in this file if a page paints it)`,
      )
    }
    for (const name of sets.get(file)) {
      assert.ok(
        sets.get(first).has(name),
        `${file} defines --${name}, which ${first} does not: a palette cannot invent a token the semantic layer does not have a role for`,
      )
    }
  }

  for (const name of COLOUR_TOKENS) {
    assert.ok(
      sets.get(first).has(name),
      `--${name} is not defined by any palette: it is a role the site paints, so it is a pair the contrast check below has to be able to compute`,
    )
  }

  const stops = [...sets.get(first)].filter((name) => name.startsWith('gradient-'))
  assert.deepEqual(
    stops.sort(),
    [...GRADIENT_STOPS].sort(),
    `the palettes declare ${stops.length} gradient stops (${stops.sort().join(', ')}): the hero's gradient has exactly two, and text over the hero is checked against each of them (U5)`,
  )
})

// ---- contrast, per palette -------------------------------------------------------------

// One case per palette rather than one case over all three, so that a failure names the
// file the reader has to open. All three run whether or not one of them fails, because the
// question the owner is entitled to an answer to is "do all three pass", not "does the
// first one".
for (const file of PALETTE_FILES) {
  test(`${file}: every pair the site paints meets its WCAG ratio`, () => {
    for (const line of contrastFailures(paletteTokens(file))) {
      assert.fail(`${file}: ${line}`)
    }
  })
}

// ---- the red step, kept -----------------------------------------------------------------

// The task's red step was run against a deliberately failing pair, and the exact line it
// printed is the line this case requires — from the same `contrastFailures` the palette
// cases call, so the check that refused it then is the check that runs now. The fixture is
// a two-token palette: the surface the site actually uses, and a grey that sits at 3.1:1 on
// it, which is a value a designer would reach for and which is below AA for body text.
test('the contrast check refuses a pair below AA, with the line the red step recorded', () => {
  const failing = new Map([
    ['surface', '#14141F'],
    ['text-muted', '#646464'],
  ])

  assert.deepEqual(contrastFailures(failing, [['--text-muted', '--surface', BODY_TEXT]]), [
    '--text-muted on --surface is 3.1:1, needs 4.5:1',
  ])
})

// ---- no colour in the semantic layer ----------------------------------------------------

// The common CSS named colours — the spellings a person actually types. The realistic
// mistake is a hex literal or a colour function, and both of those are caught by rules that
// do not depend on a list at all; this one closes the third door.
const NAMED_COLOURS = new Set([
  'aliceblue', 'aqua', 'aquamarine', 'azure', 'beige', 'black', 'blue', 'brown', 'chocolate',
  'coral', 'crimson', 'cyan', 'fuchsia', 'gold', 'gray', 'green', 'grey', 'indigo', 'ivory',
  'khaki', 'lavender', 'lime', 'magenta', 'maroon', 'navy', 'olive', 'orange', 'orchid',
  'pink', 'plum', 'purple', 'rebeccapurple', 'red', 'salmon', 'silver', 'tan', 'teal',
  'tomato', 'turquoise', 'violet', 'wheat', 'white', 'yellow',
])

const COLOUR_FUNCTION = /\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color|color-mix|device-cmyk)\s*\(/i
const HEX_LITERAL = /#[0-9a-f]{3,8}\b/i

// A semantic value may not carry a colour in any of the three spellings. Called by the case
// above, and exported so W1.11 can hold the same rule over a component's stylesheet.
export function assertColourFree(tokens) {
  for (const [name, value] of tokens) {
    assert.ok(
      !HEX_LITERAL.test(value),
      `--${name}: ${JSON.stringify(value)} carries a hex colour. Every colour the site paints comes from the palette in force, so that it is one value to change and one pair the contrast check can read`,
    )
    assert.ok(
      !COLOUR_FUNCTION.test(value),
      `--${name}: ${JSON.stringify(value)} carries a colour function. Every colour the site paints comes from the palette in force (see the note above)`,
    )
    const words = value.toLowerCase().split(/[\s,()]+/)
    for (const word of words) {
      assert.ok(
        !NAMED_COLOURS.has(word),
        `--${name}: ${JSON.stringify(value)} carries the named colour ${word}. Every colour the site paints comes from the palette in force (see the note above)`,
      )
    }
  }
}
