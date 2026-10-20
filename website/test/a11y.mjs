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
// test must read the source, not the generator's own output"). The palette files are
// the values; `tokens.css` is the vocabulary; and the cases below hold the two together —
// one palette imported, the same ten colour tokens in all four files, no colour in the
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

// The palette files this check reads: D104's three candidates, which the owner chose
// between on 2026-10-16, and `palette-logo.css`, the fourth, which D180 made the palette in
// force on 2026-10-18. The files are named here rather than discovered by a glob on purpose:
// a palette nobody asked for, or a palette file that lost its name, has to fail this check
// rather than be read as one more candidate.
export const PALETTE_FILES = [
  'palette-violet.css',
  'palette-orange.css',
  'palette-split.css',
  'palette-logo.css',
]
export const PALETTE_IN_FORCE = 'palette-logo.css'

// The colour vocabulary every palette is a value set for, and the roles the contrast pairs
// below depend on. A palette that dropped one of these would make a pair in this file
// unreadable rather than merely unset, so the case that holds the four files together
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

  // Warnings and cost figures, which are text on both grounds and on the hero — the
  // caps-and-costs teaser sits over the gradient, which is why `--warn` is a pair there and
  // not only on the flat grounds — and the dark label on a filled warning chip.
  ['--warn', '--ground', BODY_TEXT],
  ['--warn', '--surface', BODY_TEXT],
  ['--warn', '--gradient-from', BODY_TEXT],
  ['--warn', '--gradient-to', BODY_TEXT],
  ['--ground', '--warn', BODY_TEXT],

  // The accent as a fill and as structure: a filled button's label is text on the accent,
  // and the accent itself is a focus ring and a rule against the ground and the surface.
  ['--ground', '--accent', BODY_TEXT],
  ['--accent', '--ground', LARGE_OR_UI],
  ['--accent', '--surface', LARGE_OR_UI],
  ['--accent', '--gradient-from', LARGE_OR_UI],
  ['--accent', '--gradient-to', LARGE_OR_UI],
]

// The pairs `base.css` paints — the page chrome and the element defaults every page gets.
// Each one is also in PAIRS (a case below holds that), so each is computed for all four
// palettes by the per-palette cases; this list is the record of which pairs the base layer
// depends on, and the cases at the end of this file hold base.css to it in both directions:
// a rule that sets a colour and a background names a pair listed here, and every colour
// token the file paints with appears in some pair here.
export const BASE_PAIRS = [
  // The document: body text, headings and the wordmark on the ground.
  ['--text', '--ground', BODY_TEXT],
  // The quieter tone: nav links, the footer, definitions, captions, blockquotes, markers.
  ['--text-muted', '--ground', BODY_TEXT],
  // Links, and the wordmark on hover.
  ['--accent-on-dark', '--ground', BODY_TEXT],
  // Code chips, code blocks and table headers sit on the surface.
  ['--text', '--surface', BODY_TEXT],
  // The current page in the nav (accent text on a surface pill), and a link in a header cell.
  ['--accent-on-dark', '--surface', BODY_TEXT],
  // The focused skip link and the text selection: the ground as a label on the accent fill.
  ['--ground', '--accent', BODY_TEXT],
  // The focus ring, the current page's underline and the blockquote rule, as UI colours.
  ['--accent', '--ground', LARGE_OR_UI],
  ['--accent', '--surface', LARGE_OR_UI],
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

test('tokens.css imports exactly one palette, and it is the one D180 made the palette in force', () => {
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
    `tokens.css imports ${imports[0]}, which is not one of the four palette files this check reads (${PALETTE_FILES.join(', ')})`,
  )
  assert.equal(
    imported,
    PALETTE_IN_FORCE,
    `tokens.css imports ${imported}, but the palette in force is ${PALETTE_IN_FORCE}: the owner ruled on 2026-10-18 (D180) that the accent is the logo's own hue, sampled from brand/fugaro-logo.png, so palette-logo.css is the one in force. The other three ship as the record of D104's choice; changing the palette in force is this one line`,
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
  // colour belongs in a palette, where the contrast check can read it for all four palette
  // files at once, and a semantic token belongs here, where it is a step rather than a
  // value. A name in both would be a second place a colour could be set.
  for (const name of semantic.keys()) {
    assert.ok(
      !palette.has(name),
      `tokens.css declares --${name}, which ${PALETTE_IN_FORCE} also declares: the palette is the only place a colour is set, so that the contrast check can compute every pair for every candidate`,
    )
  }

  assertColourFree(semantic)
})

test('the four palettes are one vocabulary in four value sets', () => {
  const sets = new Map()
  for (const file of PALETTE_FILES) {
    sets.set(file, new Set(paletteTokens(file).keys()))
  }

  const [first, ...rest] = PALETTE_FILES
  for (const file of rest) {
    for (const name of sets.get(first)) {
      assert.ok(
        sets.get(file).has(name),
        `${file} does not define --${name}, which ${first} does: every palette is a complete value set for the same vocabulary, so a token added to one of them is added to all four (and to the pair set in this file if a page paints it)`,
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

// One case per palette rather than one case over all four, so that a failure names the
// file the reader has to open. All four run whether or not one of them fails, because the
// question the owner is entitled to an answer to is "do all four pass", not "does the
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

// ---- the base stylesheet ------------------------------------------------------------------

// `base.css` paints the document and the chrome with the tokens, and it is held to the same
// rule as the semantic layer — no colour of its own — plus the pair bookkeeping above.
const BASE_FILE = 'base.css'

// Every `property: value;` in a stylesheet, as [property, value] pairs (custom properties
// included). Selectors never match: a selector ends in `{`, which a value cannot contain.
function allDeclarations(css) {
  return [...stripComments(css).matchAll(/(-{0,2}[a-zA-Z][a-zA-Z0-9-]*)\s*:\s*([^;{}]+);/g)].map(([, prop, value]) => [prop, value.trim()])
}

// The innermost `{ ... }` blocks, each as a list of declarations: one per rule.
function rules(css) {
  return [...stripComments(css).matchAll(/([^{}]+)\{([^{}]*)\}/g)].map(([, selector, body]) => ({
    selector: selector.trim(),
    decls: allDeclarations(`${body};`),
  }))
}

const COLOUR_PROPS = /^(color|background|background-color|border(-[a-z]+)*|outline(-color)?|text-decoration(-color)?|fill|stroke)$/
// The palette tokens a value names (spacing, radius and width tokens are not colours).
const tokenIn = (value) =>
  [...value.matchAll(/var\(\s*(--[a-zA-Z0-9-]+)\s*\)/g)].map((m) => m[1]).filter((t) => COLOUR_TOKENS.includes(t.slice(2)))
const sameAs = (a, b) => a[0] === b[0] && a[1] === b[1] && a[2] === b[2]

test('base.css carries no colour literal: every colour is a palette token', () => {
  const decls = allDeclarations(readStyleFile(BASE_FILE))
  assert.ok(decls.length > 0, 'base.css declares nothing: the page chrome is missing')
  assertColourFree(new Map(decls.map(([prop, value], i) => [`${prop} (declaration ${i + 1})`, value])))
})

test('the colour literal scan refuses a hex, a colour function and a named colour in a declaration', () => {
  for (const bad of ['a { color: #fff; }', 'a { color: rgb(1 2 3); }', 'a { background: hsl(0 0% 0%); }', 'a { color: white; }']) {
    const decls = allDeclarations(bad)
    assert.throws(() => assertColourFree(new Map(decls)), undefined, `the scan let ${bad} through`)
  }
})

test('every pair base.css paints is in the contrast set, for all four palettes', () => {
  for (const pair of BASE_PAIRS) {
    assert.ok(
      PAIRS.some((p) => sameAs(p, pair)),
      `${pair[0]} on ${pair[1]} is a pair base.css paints and the contrast set does not check: add it to PAIRS`,
    )
  }

  const listed = new Set(BASE_PAIRS.flatMap(([fg, bg]) => [fg, bg]))
  const texts = new Set(BASE_PAIRS.map(([fg]) => fg))
  for (const { selector, decls } of rules(readStyleFile(BASE_FILE))) {
    let fg = null
    let bg = null
    for (const [prop, value] of decls) {
      if (!COLOUR_PROPS.test(prop)) continue
      for (const token of tokenIn(value)) {
        // --border is a decorative separator, deliberately outside the pair set (see PAIRS).
        if (token === '--border') continue
        assert.ok(listed.has(token), `base.css paints ${token} in "${selector}" (${prop}), which no pair in BASE_PAIRS names`)
      }
      if (prop === 'color') fg = tokenIn(value)[0] ?? null
      if (prop === 'background' || prop === 'background-color') bg = tokenIn(value)[0] ?? null
    }
    if (fg) {
      assert.ok(texts.has(fg), `"${selector}" paints text in ${fg}, which BASE_PAIRS never uses as a text colour`)
    }
    if (fg && bg && fg !== bg) {
      assert.ok(
        BASE_PAIRS.some(([f, b]) => f === fg && b === bg),
        `"${selector}" paints ${fg} on ${bg}, which is not in BASE_PAIRS (and so not checked for contrast)`,
      )
    }
  }
})

// =========================================================================================
// W1.11: the rest of the accessibility contract, over the BUILT pages (website plan, W1.11
// step 3; reconciliation findings 53 and 54, D105).
//
// Everything above reads the stylesheets; everything below reads `dist/`, which
// `npm run check` builds before it runs this file (`FUGARO_GATES_DIST` points it at a copy,
// which is how each refusal was mutation-proved). It is appended rather than woven in, so
// that W1.2's cases above are untouched. Per built page:
//
//   - the document: `<html lang>` is a language tag, `<title>` is not empty, ids are unique;
//   - structure: exactly one `h1`, and headings never skip a level going down (h2 -> h4);
//   - landmarks: one `main`, a page `header` and `footer`, at least one `nav`, and every
//     `nav` named (aria-label or a resolving aria-labelledby), uniquely when there are two;
//   - the skip link: the first thing a keyboard reaches is an in-page link to `main` (or
//     into it);
//   - names: every `img` has an `alt` (empty is a decision: decorative), every `svg` with
//     role="img" has a name, every link and button has a name, every form control a label;
//   - the keyboard: no positive tabindex, no focusable element hidden with aria-hidden, a
//     non-native role=button/link is focusable, and no stylesheet removes the focus outline
//     except for the `:focus:not(:focus-visible)` pattern that keeps it for the keyboard;
//   - reduced motion (D105): every animation, transition and smooth scroll the page's CSS
//     declares is either gated on `prefers-reduced-motion: no-preference` or neutralised by
//     a `prefers-reduced-motion: reduce` rule — the universal one the base layout writes, or
//     one naming the same selector;
//   - contrast over the built CSS: the colour tokens the page ships are the palette in
//     force's (so the pairs checked above are the pairs a reader gets), no shipped stylesheet
//     paints a colour literal outside the token block, and an element that sets both its
//     colour and its background inline (the code blocks) clears AA.
//
// One recorded exception, and it is a DEFECT: the docs page renders the generated CLI
// reference, and cobra's own help text for `fugaro completion bash|zsh` writes `#### Linux:`
// and `#### macOS:` under a `##` command heading — an h2 -> h4 jump. W1.11 may not edit the
// generator (internal/clidoc) or the page (W1.7's), so exactly those headings are accepted on
// that page, and a case fails once they are gone so the exception is deleted with the fix
// (the generator demotes or escapes headings inside a command's long text).

import { readFileSync as readBuilt } from 'node:fs'
import {
  DIST,
  HTMLScanError,
  builtPages,
  cssDeclarations,
  cssRules,
  filesUnder,
  findAll,
  hasAncestor,
  parse,
  servedFile,
  textOf,
  walk,
} from './lib/html.mjs'

// The recorded heading-order defect described above: page, and the ids of the headings
// that are accepted as the jump.
export const HEADING_JUMP_DEFECT = {
  page: 'docs/index.html',
  ids: new Set(['linux', 'macos', 'linux-1', 'macos-1']),
  why: "cobra's completion help writes #### headings under the reference's ## command headings",
}

const FOCUSABLE_NATIVE = (el) =>
  (el.name === 'a' && el.attrs.has('href')) ||
  (el.name === 'area' && el.attrs.has('href')) ||
  ['button', 'select', 'textarea', 'iframe', 'summary'].includes(el.name) ||
  (el.name === 'input' && (el.attrs.get('type') ?? '').toLowerCase() !== 'hidden')

function focusable(el) {
  if (el.attrs.has('disabled')) return false
  const tabindex = el.attrs.get('tabindex')
  if (tabindex !== undefined && Number(tabindex) < 0) return false
  return FOCUSABLE_NATIVE(el) || (tabindex !== undefined && Number(tabindex) >= 0)
}

function nameOf(el, byId) {
  const label = (el.attrs.get('aria-label') ?? '').trim()
  if (label) return label
  const by = (el.attrs.get('aria-labelledby') ?? '').trim()
  if (by) {
    return by
      .split(/\s+/)
      .map((id) => (byId.has(id) ? textOf(byId.get(id)).trim() : ''))
      .join(' ')
      .trim()
  }
  return ''
}

/** The CSS a built page ships: its <style> blocks and the first-party stylesheets it links. */
export function shippedCSS(doc, dist = DIST) {
  const out = []
  for (const el of walk(doc)) {
    if (el.name === 'style') out.push(el.raw ?? '')
    if (el.name === 'link' && /(^|\s)stylesheet(\s|$)/i.test(el.attrs.get('rel') ?? '')) {
      const href = el.attrs.get('href') ?? ''
      if (/^[a-z]+:|^\/\//i.test(href)) continue // origins.mjs refuses a remote one
      const served = servedFile(dist, new URL(href, 'https://x/').pathname)
      if (served) out.push(readBuilt(path.join(dist, served), 'utf8'))
    }
  }
  return out.join('\n')
}

const REDUCE = /prefers-reduced-motion\s*:\s*reduce/i
const NO_PREFERENCE = /prefers-reduced-motion\s*:\s*no-preference/i

/** Motion the CSS declares that a reduce rule does not neutralise. */
export function motionFailures(css, inlineStyles = []) {
  const rules = cssRules(css)
  const reduce = rules.filter((r) => r.conditions.some((c) => REDUCE.test(c)) && r.selector && !r.selector.startsWith('@'))
  const universal = reduce.filter((r) => r.selector.split(',').some((s) => s.trim() === '*'))
  const sets = (rs, prop) => rs.some((r) => cssDeclarations(r.body).some(([p]) => p === prop))
  const neutralised = (selector, kind) =>
    reduce.some(
      (r) =>
        r.selector === selector &&
        cssDeclarations(r.body).some(([p, v]) => (p === kind || p === `${kind}-name` || p === `${kind}-duration`) && /^(none|0m?s|1ms)\b/.test(v)),
    )
  const covered = {
    animation: sets(universal, 'animation-duration') && sets(universal, 'animation-iteration-count'),
    transition: sets(universal, 'transition-duration'),
    'scroll-behavior': sets(universal, 'scroll-behavior'),
  }
  const failures = []
  const consider = (selector, conditions, body) => {
    if (conditions.some((c) => REDUCE.test(c) || NO_PREFERENCE.test(c))) return
    for (const [p, v] of cssDeclarations(body)) {
      let kind = null
      if ((p === 'animation' || p === 'animation-name') && !/^none\b/.test(v)) kind = 'animation'
      if ((p === 'transition' || p === 'transition-duration') && !/^(none|0s|0ms)\b/.test(v)) kind = 'transition'
      if (p === 'scroll-behavior' && /smooth/.test(v)) kind = 'scroll-behavior'
      if (kind && !covered[kind] && !neutralised(selector, kind)) {
        failures.push(`${selector} declares ${p}: ${v}, and no prefers-reduced-motion: reduce rule neutralises it (D105)`)
      }
    }
  }
  for (const r of rules) if (r.selector && !r.selector.startsWith('@')) consider(r.selector, r.conditions, r.body)
  for (const [where, body] of inlineStyles) consider(where, [], body)
  return failures
}

/** Every accessibility failure of one built page. */
export function pageA11yFailures(html, rel, { css = null, dist = DIST, palette = paletteTokens(PALETTE_IN_FORCE) } = {}) {
  const out = []
  const fail = (why) => out.push(`${rel}: ${why}`)
  let doc
  try {
    doc = parse(html)
  } catch (err) {
    if (err instanceof HTMLScanError) return [`${rel}: ${err.message}`]
    throw err
  }
  const all = [...walk(doc)]
  const byId = new Map()
  for (const el of all) {
    if (!el.attrs.has('id')) continue
    const id = el.attrs.get('id')
    if (byId.has(id)) fail(`the id "${id}" is used twice: an anchor or a label can point at only one of them`)
    byId.set(id, el)
  }

  // The document.
  const htmlEl = all.find((el) => el.name === 'html')
  const lang = htmlEl?.attrs.get('lang') ?? ''
  if (!/^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$/.test(lang)) fail(`<html lang="${lang}"> is not a language tag: a screen reader picks its voice from it`)
  const title = all.find((el) => el.name === 'title')
  if (!title || !textOf(title).trim()) fail('the page has no <title>')
  const body = all.find((el) => el.name === 'body')
  if (!body) return [...out, `${rel}: the page has no <body>`]

  // Headings.
  const headings = all.filter((el) => /^h[1-6]$/.test(el.name))
  const h1s = headings.filter((el) => el.name === 'h1').length
  if (h1s !== 1) fail(`the page has ${h1s} h1 elements; it has exactly one`)
  let prev = 1
  for (const h of headings) {
    const level = Number(h.name[1])
    if (level > prev + 1) {
      const known = rel === HEADING_JUMP_DEFECT.page && HEADING_JUMP_DEFECT.ids.has(h.attrs.get('id'))
      if (!known) fail(`a heading jumps from h${prev} to h${level} ("${textOf(h).trim().slice(0, 60)}"): heading levels go down one at a time`)
      if (known) continue // the recorded defect: the next heading is judged against its h2
    }
    prev = level
  }

  // Landmarks.
  const mains = all.filter((el) => el.name === 'main' || el.attrs.get('role') === 'main')
  if (mains.length !== 1) fail(`the page has ${mains.length} main landmarks; it has exactly one`)
  const sectioning = ['article', 'aside', 'main', 'nav', 'section']
  const pageLevel = (name) => all.filter((el) => el.name === name && !sectioning.some((s) => hasAncestor(el, s)))
  if (pageLevel('header').length !== 1) fail(`the page has ${pageLevel('header').length} page-level <header> landmarks; it has exactly one`)
  if (pageLevel('footer').length !== 1) fail(`the page has ${pageLevel('footer').length} page-level <footer> landmarks; it has exactly one`)
  const navs = all.filter((el) => el.name === 'nav')
  if (navs.length === 0) fail('the page has no <nav> landmark')
  const navNames = navs.map((n) => nameOf(n, byId))
  navNames.forEach((name, i) => {
    if (!name) fail(`<nav> number ${i + 1} has no name (aria-label or aria-labelledby): a screen reader lists landmarks by name`)
  })
  if (new Set(navNames).size !== navNames.length) fail(`two <nav> landmarks share a name (${navNames.join(', ')})`)

  // The skip link.
  const firstFocusable = all.find((el) => hasAncestor(el, 'body') && focusable(el))
  const skipTarget = firstFocusable?.name === 'a' ? /^#(.+)$/.exec(firstFocusable.attrs.get('href') ?? '') : null
  const target = skipTarget ? byId.get(decodeURIComponent(skipTarget[1])) : null
  if (!target || !(target === mains[0] || [...walk(mains[0] ?? { children: [] })].includes(target))) {
    fail('the first focusable element is not a skip link to the main content: a keyboard user should not tab through the navigation on every page')
  }

  // Names.
  for (const el of all) {
    if (el.name === 'img' && !el.attrs.has('alt')) fail(`<img src="${el.attrs.get('src') ?? ''}"> has no alt (alt="" marks it decorative)`)
    if (el.name === 'svg' && el.attrs.get('role') === 'img' && !nameOf(el, byId) && !el.children.some((c) => c.name === 'title' && textOf(c).trim())) {
      fail('an <svg role="img"> has no name (aria-label, aria-labelledby or a <title>)')
    }
    if ((el.name === 'a' && el.attrs.has('href')) || el.name === 'button') {
      if (!nameOf(el, byId) && !textOf(el).trim() && !el.attrs.get('title')) fail(`a <${el.name}> has no accessible name${el.attrs.has('href') ? ` (href="${el.attrs.get('href')}")` : ''}`)
    }
    const control =
      ['select', 'textarea'].includes(el.name) ||
      (el.name === 'input' && !['hidden', 'submit', 'button', 'reset', 'image'].includes((el.attrs.get('type') ?? 'text').toLowerCase()))
    if (control) {
      const id = el.attrs.get('id')
      const labelled =
        nameOf(el, byId) ||
        el.attrs.get('title') ||
        hasAncestor(el, 'label') ||
        (id && all.some((l) => l.name === 'label' && l.attrs.get('for') === id))
      if (!labelled) fail(`a <${el.name}${el.attrs.has('name') ? ` name="${el.attrs.get('name')}"` : ''}> has no label`)
    }
  }

  // The keyboard.
  for (const el of all) {
    const tabindex = el.attrs.get('tabindex')
    if (tabindex !== undefined && Number(tabindex) > 0) fail(`<${el.name} tabindex="${tabindex}">: a positive tabindex reorders the keyboard path away from the reading order`)
    if (focusable(el) && (el.attrs.get('aria-hidden') === 'true' || all.some((a) => a.attrs.get('aria-hidden') === 'true' && [...walk(a)].includes(el)))) {
      fail(`a focusable <${el.name}> is inside aria-hidden: the keyboard reaches what a screen reader cannot see`)
    }
    const role = el.attrs.get('role')
    if (['button', 'link', 'tab', 'menuitem', 'checkbox', 'switch'].includes(role) && !FOCUSABLE_NATIVE(el) && tabindex === undefined) {
      fail(`<${el.name} role="${role}"> is not keyboard-reachable (no tabindex)`)
    }
  }

  // The shipped CSS: focus, motion, colour.
  const sheet = css ?? shippedCSS(doc, dist)
  let rules = []
  try {
    rules = cssRules(sheet)
  } catch (err) {
    fail(`the page's CSS could not be read (${err.message})`)
  }
  for (const r of rules) {
    if (!r.selector || r.selector.startsWith('@')) continue
    for (const [p, v] of cssDeclarations(r.body)) {
      if ((p === 'outline' || p === 'outline-style' || p === 'outline-width') && /^(none|0)(px)?\b/.test(v) && !/:not\(\s*:focus-visible\s*\)/.test(r.selector)) {
        fail(`${r.selector} removes the focus outline (${p}: ${v}); keep it for the keyboard (:focus-visible), or scope the removal to :focus:not(:focus-visible)`)
      }
    }
  }
  const inlineStyles = all.filter((el) => el.attrs.has('style')).map((el) => [`<${el.name} style>`, el.attrs.get('style')])
  try {
    for (const why of motionFailures(sheet, inlineStyles)) fail(why)
  } catch (err) {
    fail(`the page's CSS could not be read for motion (${err.message})`)
  }

  const root = new Map()
  for (const r of rules) {
    if (r.selector === ':root' && r.conditions.length === 0) for (const [p, v] of cssDeclarations(r.body)) if (p.startsWith('--')) root.set(p.slice(2), v)
  }
  for (const name of COLOUR_TOKENS) {
    if (!root.has(name)) fail(`the page ships no --${name}: the palette's tokens are what the contrast pairs above are computed from`)
    else if (parseColour(root.get(name), `--${name}`).join() !== parseColour(palette.get(name), `--${name}`).join()) {
      fail(`the page ships --${name}: ${root.get(name)}, but ${PALETTE_IN_FORCE} says ${palette.get(name)}: the contrast checked above is not the contrast a reader gets`)
    }
  }
  for (const r of rules) {
    if (!r.selector || r.selector === ':root' || r.selector.startsWith('@')) continue
    if (HEX_LITERAL.test(r.body) || COLOUR_FUNCTION.test(r.body)) {
      fail(`${r.selector} paints a colour literal (${r.body.slice(0, 80)}): every colour the site paints comes from the palette in force, so the contrast check can see it`)
    }
  }
  for (const [where, style] of inlineStyles) {
    const decl = new Map(cssDeclarations(style))
    const fg = decl.get('color')
    const bg = decl.get('background-color') ?? decl.get('background')
    if (fg && bg && /^#[0-9a-f]{3,6}$/i.test(fg) && /^#[0-9a-f]{3,6}$/i.test(bg)) {
      const ratio = contrastRatio(parseColour(fg, 'color'), parseColour(bg, 'background'))
      if (ratio < BODY_TEXT) fail(`${where}: ${fg} on ${bg} is ${ratio.toFixed(1)}:1, needs ${BODY_TEXT}:1`)
    }
  }
  return out
}

/** Every accessibility failure of a built artifact. */
export function artifactA11yFailures(dist = DIST) {
  const out = []
  for (const page of builtPages(dist)) out.push(...pageA11yFailures(page.html, page.rel, { dist }))
  return out
}

test('every built page meets the accessibility contract: landmarks, headings, names, keyboard, motion, contrast', () => {
  const failures = artifactA11yFailures()
  assert.deepEqual(failures, [], `the built site breaks the accessibility contract (D105):\n  ${failures.join('\n  ')}`)
})

// ---- the flow (P8, D185) ------------------------------------------------------------------
//
// The flow on How It Works and on the homepage is four steps and three arrows. Its text is
// the whole content — a screen reader reads the steps, in order — and its arrows are
// decoration, which is a claim only this file can hold it to: the generic contract above
// refuses a *focusable* element hidden with aria-hidden, and says nothing about art that
// should have been hidden and was not, nor about a flow whose own words are hidden and so
// never reach a reader at all. The motion path is the generic one (every animation in a
// built page's CSS has to be gated or neutralised, D105), so it is not repeated here.

/** The classes on an element, as a list. */
function classList(el) {
  return (el.attrs.get('class') ?? '').split(/\s+/)
}

/** Every flow step of a page, in the order the list gives them. */
export function flowStepsOf(html) {
  const doc = parse(html)
  const flows = findAll(doc, (el) => el.name === 'ol' && classList(el).includes('flow-steps'))
  return flows.flatMap((flow) => [...walk(flow)].filter((el) => el.name === 'li' && classList(el).includes('flow-step')))
}

/**
 * What a screen reader would get wrong in a page's flow, as the lines a reader sees: a step
 * that is not a list item of the flow's own list, a step whose caption is outside it (so the
 * list reads the step names and then, somewhere else, their captions), a step's text hidden
 * from assistive technology, or art inside the flow that is not hidden from one.
 *
 * The hidden-text half is why this check exists rather than a generic one: the contract above
 * refuses a *focusable* element inside `aria-hidden`, and says nothing about a `<figure>` or
 * an `<ol>` that hides the flow's own words — the one thing the flow is for.
 */
export function flowA11yFailures(html) {
  const out = []
  const doc = parse(html)
  const flows = findAll(doc, (el) => el.name === 'ol' && classList(el).includes('flow-steps'))
  /** Whether an element or anything above it is hidden from assistive technology. */
  const hidden = (el) => {
    for (let node = el; node; node = node.parent) if (node.attrs?.get('aria-hidden') === 'true') return true
    return false
  }
  for (const flow of flows) {
    if (hidden(flow)) {
      out.push(
        'the flow is hidden from assistive technology (aria-hidden="true" on the list or on something above it): the steps are the whole content of the picture, and a reader left with an empty figure is exactly what this case is for',
      )
    }
    // The figure is the flow's one name: a figure with neither a figcaption nor an aria-label
    // is a list of four steps announced as nothing in particular.
    const figure = (() => {
      for (let node = flow.parent; node; node = node.parent) if (node.name === 'figure') return node
      return null
    })()
    const named = figure && (figure.attrs.get('aria-label') || figure.attrs.get('aria-labelledby') || findAll(figure, (el) => el.name === 'figcaption').length > 0)
    if (!named) {
      out.push('the flow has no accessible name (its <figure> carries no <figcaption>, aria-label or aria-labelledby): the list of steps is the text alternative, and it needs a name a screen reader announces it by')
    }
    const direct = flow.children.filter((child) => child.name === 'li' && classList(child).includes('flow-step'))
    for (const step of [...walk(flow)].filter((el) => el.name === 'li' && classList(el).includes('flow-step'))) {
      const name = step.attrs.get('data-step') ?? '(a step with no data-step)'
      if (!direct.includes(step)) {
        out.push(`the ${name} step is not an item of the flow's list: the flow is an ordered list, and a step nested anywhere else is read in whatever order the markup happens to give`)
      }
      if (hidden(step)) {
        out.push(`the ${name} step is hidden from assistive technology (aria-hidden="true" on the step or on something above it): the step is what the flow says happens, and hiding it hides a step of the run`)
      }
      const heading = [...walk(step)].find((el) => /^h[1-6]$/.test(el.name))
      const caption = [...walk(step)].find((el) => classList(el).includes('step-caption'))
      if (!heading) out.push(`the ${name} step has no heading: a screen reader lists the steps by their headings`)
      if (!caption) out.push(`the ${name} step carries no caption inside it: the caption is what the step says, and a caption outside the step is read apart from the step it belongs to`)
      for (const el of walk(step)) {
        if (el.name !== 'svg') continue
        if (el.attrs.get('aria-hidden') !== 'true' || el.attrs.get('focusable') !== 'false') {
          out.push(`a <svg> in the ${name} step is not marked as decoration (aria-hidden="true" focusable="false"): the arrows draw the flow and add nothing a screen reader should have to read`)
        }
      }
    }
  }
  return out
}

test('the flow is an ordered list a screen reader reads in order, and its arrows are decoration', () => {
  // The homepage carries the flow now; the How It Works page carries the run's own diagram,
  // whose steps are its stage pills and whose name is on the drawing
  // (`test/how-it-works.mjs`). Every page is still walked, so a page that grew a flow of its
  // own is checked by the same rules, and the homepage is held to four steps or a page that
  // rendered no flow at all would pass a check that read nothing.
  for (const page of builtPages()) {
    const steps = flowStepsOf(page.html)
    const failures = flowA11yFailures(page.html)
    assert.deepEqual(failures, [], `${page.rel}: the flow is not readable in order:\n  ${failures.join('\n  ')}`)
    if (page.rel === 'index.html') {
      assert.equal(steps.length, 4, `${page.rel} carries ${steps.length} flow steps, and D185's flow is the brief's four (P8)`)
    } else {
      assert.equal(steps.length, 0, `${page.rel} carries a flow, and the flow is the homepage's picture of the run (the owner's draft of 2026-10-19 moved the How It Works page to the run's own diagram)`)
    }
  }
})

test('the flow refusals fire on the defect each one names', () => {
  // A page carrying the flow in the shape the component writes it, which passes; each
  // fixture below breaks one thing in it, so the check above cannot pass by reading nothing.
  const flow = ({ captionOutside = false, arrowHidden = true, hiddenAt = '', unnamed = false } = {}) => {
    const arrow = arrowHidden
      ? '<svg class="step-arrow" aria-hidden="true" focusable="false"><path d="M1 1"></path></svg>'
      : '<svg class="step-arrow"><path d="M1 1"></path></svg>'
    // The arrow is the component's own placement: inside the step, after its card.
    const step = (n, body, svg = '', extra = '') => `<li class="flow-step"${extra} data-step="step ${n}"><div class="step-card">${body}</div>${svg}</li>`
    return [
      '<!DOCTYPE html><html lang="en"><head><title>T</title></head><body><main id="main"><h1>T</h1>',
      `<figure class="flow"${hiddenAt === 'figure' ? ' aria-hidden="true"' : ''}>${unnamed ? '' : '<figcaption>Flow</figcaption>'}<ol class="flow-steps"${hiddenAt === 'list' ? ' aria-hidden="true"' : ''}>`,
      step(1, '<h2>One</h2><p class="step-caption">first</p>', arrow, hiddenAt === 'step' ? ' aria-hidden="true"' : ''),
      step(2, captionOutside ? '<h2>Two</h2>' : '<h2>Two</h2><p class="step-caption">second</p>'),
      captionOutside ? '<p class="step-caption">second</p>' : '',
      '</ol></figure></main></body></html>',
    ].join('')
  }

  assert.deepEqual(flowA11yFailures(flow()), [], 'the fixture page does not pass the check it is the control for')
  assert.ok(
    flowA11yFailures(flow({ captionOutside: true })).some((line) => /carries no caption inside it/.test(line)),
    'a caption outside its step passes: the list would read the step names and, apart from them, their captions',
  )
  assert.ok(
    flowA11yFailures(flow({ arrowHidden: false })).some((line) => /is not marked as decoration/.test(line)),
    'an arrow that is not aria-hidden passes: a screen reader would read the picture the steps already say',
  )
  assert.ok(
    flowA11yFailures(flow({ unnamed: true })).some((line) => /no accessible name/.test(line)),
    'a flow whose figure has no figcaption or aria-label passes: the list would be announced with no name',
  )
  // The flow's words are the flow: hidden at the list or at the figure above it, the page is
  // a picture with nothing behind it, and every other gate in this file still passes.
  for (const at of ['list', 'figure', 'step']) {
    assert.ok(
      flowA11yFailures(flow({ hiddenAt: at })).some((line) => /is hidden from assistive technology/.test(line)),
      `a flow hidden with aria-hidden="true" at the ${at} passes: the steps are the whole content, and a reader given an empty figure has been given nothing`,
    )
  }
})

test('the recorded heading-order defect on the docs page is still real, so its exception is still needed', () => {
  const docs = builtPages().find((p) => p.rel === HEADING_JUMP_DEFECT.page)
  assert.ok(docs, `${HEADING_JUMP_DEFECT.page} was not built`)
  const ids = [...docs.html.matchAll(/<h4 id="([^"]+)"/g)].map((m) => m[1]).filter((id) => HEADING_JUMP_DEFECT.ids.has(id))
  assert.ok(
    ids.length > 0,
    `${HEADING_JUMP_DEFECT.page} no longer carries the recorded h2 -> h4 headings: the defect is fixed, so delete HEADING_JUMP_DEFECT in website/test/a11y.mjs and let every jump fail again`,
  )
})

// ---- the refusals, kept ----------------------------------------------------------------

// A page in the layout's shape, which passes; each fixture below breaks one thing in it.
const GOOD_CSS = (() => {
  const palette = paletteTokens(PALETTE_IN_FORCE)
  const tokens = COLOUR_TOKENS.map((n) => `--${n}:${palette.get(n)}`).join(';')
  return `:root{${tokens}}@media (prefers-reduced-motion:reduce){*,:before,:after{animation-duration:1ms!important;animation-iteration-count:1!important;transition-duration:1ms!important;scroll-behavior:auto!important}}`
})()

function fixture({ head = '', header = '<header><a href="/">Site</a><nav aria-label="Sections"><a href="/a/">A</a></nav></header>', main = '<h1>Title</h1><h2>Part</h2><p>x</p>', skip = '<a href="#main">Skip to content</a>', lang = 'en', css = GOOD_CSS } = {}) {
  return [`<!DOCTYPE html><html lang="${lang}"><head><title>T</title><style>${css}</style>${head}</head>`, `<body>${skip}${header}<main id="main">${main}</main><footer><p>f</p></footer></body></html>`]
}

function a11yOf(parts) {
  const html = parts.join('')
  const css = /<style>([\s\S]*?)<\/style>/.exec(html)[1]
  return pageA11yFailures(html, 'fixture.html', { css })
}

test('the fixture page passes, so each refusal below is the one thing it breaks', () => {
  assert.deepEqual(a11yOf(fixture()), [])
})

test('every accessibility refusal fires on the defect it names', () => {
  const cases = [
    ['an img without alt', { main: '<h1>T</h1><img src="/a.png">' }, /has no alt/],
    ['a heading jump', { main: '<h1>T</h1><h2>a</h2><h4>b</h4>' }, /jumps from h2 to h4/],
    ['two h1s', { main: '<h1>T</h1><h1>U</h1>' }, /2 h1 elements/],
    ['no lang', { lang: '' }, /lang=""/],
    ['no nav', { header: '<header><a href="/">Site</a></header>' }, /no <nav>/],
    ['an unnamed nav', { header: '<header><nav><a href="/a/">A</a></nav></header>' }, /has no name/],
    ['no header landmark', { header: '<nav aria-label="S"><a href="/a/">A</a></nav>' }, /0 page-level <header>/],
    ['no skip link', { skip: '' }, /not a skip link/],
    ['a skip link to nowhere', { skip: '<a href="#nope">Skip</a>' }, /not a skip link/],
    ['an unlabelled input', { main: '<h1>T</h1><form action="/s/"><input name="q"></form>' }, /<input name="q"> has no label/],
    ['an empty link', { main: '<h1>T</h1><a href="/a/"></a>' }, /has no accessible name/],
    ['a positive tabindex', { main: '<h1>T</h1><p tabindex="3">x</p>' }, /positive tabindex/],
    ['a div acting as a button', { main: '<h1>T</h1><div role="button">x</div>' }, /not keyboard-reachable/],
    ['a focus outline removed', { css: GOOD_CSS + 'a:focus{outline:none}' }, /removes the focus outline/],
    ['an animation with no reduce path', { css: GOOD_CSS.replace(/@media[\s\S]*$/, '') + '.t{animation:spin 1s infinite}' }, /\.t declares animation/],
    ['a transition the reduce rule does not cover', { css: GOOD_CSS.replace('transition-duration:1ms!important;', '') + '.t{transition:opacity .2s}' }, /\.t declares transition/],
    ['a colour literal in a component', { css: GOOD_CSS + '.t{color:#ff0000}' }, /paints a colour literal/],
    ['a shipped token that is not the palette', { css: GOOD_CSS.replace(/--text:#[0-9a-fA-F]+/, '--text:#777777') }, /ships --text: #777777/],
    ['an inline pair below AA', { main: '<h1>T</h1><pre style="background-color:#24292e;color:#444444" tabindex="0">x</pre>' }, /#444444 on #24292e is/],
    ['a duplicated id', { main: '<h1 id="x">T</h1><p id="x">y</p>' }, /used twice/],
  ]
  for (const [what, opts, expected] of cases) {
    const failures = a11yOf(fixture(opts))
    assert.ok(failures.some((f) => expected.test(f)), `${what} was not refused (${expected}); the check said:\n  ${failures.join('\n  ') || '(nothing)'}`)
  }
})

test('motion gated on no-preference, or neutralised by its own reduce rule, passes', () => {
  const bare = GOOD_CSS.replace(/@media[\s\S]*$/, '')
  assert.deepEqual(motionFailures(bare + '@supports (animation-timeline:view()){@media (prefers-reduced-motion:no-preference){.s{animation:in linear both}}}'), [])
  assert.deepEqual(motionFailures(bare + '.t{animation:spin 1s}@media (prefers-reduced-motion:reduce){.t{animation:none}}'), [])
})
