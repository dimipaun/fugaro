// website/test/lib/colour.mjs — the palette, and the contrast arithmetic over it.
//
// The site's colour rule is one sentence: every colour a page paints comes from the palette
// in force, so that it is one value to change and one pair a gate can compute. Two gates make
// that sentence true and they have to compute a ratio the same way — `website/test/a11y.mjs`,
// which holds every designed pair of every palette to WCAG 2.1 AA, and
// `website/test/how-it-works.mjs`, which holds the run drawing's own labels to the surface
// they are drawn on. `docs/design/console-styling.md` states the rule this file exists for,
// of the console's token test: the arithmetic is one implementation, not two.
//
// A palette value has to be an opaque hex literal, and a value that is not is refused rather
// than skipped: a colour function, an alpha channel or a `var()` indirection is a pair this
// check cannot compute, and a pair it cannot compute is exactly the pair a contrast
// regression would hide in.
//
// Standard library only (Global Constraint 4).

import assert from 'node:assert/strict'
import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { SITE } from './html.mjs'

/** The directory the site's stylesheets live in, read from the repository and not a build. */
const STYLES_DIR = path.join(SITE, 'src', 'styles')

/** The token layer, whose `@import` names the palette in force. */
const TOKENS_FILE = 'tokens.css'

// Comments are stripped before anything is parsed, and that is load-bearing in both
// directions: a palette's header explains the values and may quote them, and a colour
// mentioned in prose is not a colour the page paints. The rule these readers enforce is about
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

/** The bytes of a stylesheet of `website/src/styles/`, refused by name when it is missing. */
export function readStyleFile(fileName) {
  const file = path.join(STYLES_DIR, fileName)
  assert.ok(
    existsSync(file),
    `${fileName} is missing: the site's design tokens live in website/src/styles/ and this check reads them from the repository, not from a build output`,
  )
  return readFileSync(file, 'utf8')
}

/** One named stylesheet's custom properties, as a Map of name (no `--`) to value. */
export function paletteTokens(fileName) {
  return declarations(readStyleFile(fileName))
}

/**
 * The palette in force, as the token layer's own `@import` names it — read rather than typed
 * in here, so that changing the palette is the one line in `tokens.css` and every gate that
 * computes a ratio follows it. `null` when the token layer imports no single local
 * stylesheet, which the caller refuses rather than guessing a file.
 */
export function paletteInForce() {
  const imported = [...stripComments(readStyleFile(TOKENS_FILE)).matchAll(/@import\s+["']\.\/([\w.-]+\.css)["']/g)].map(
    (match) => match[1],
  )
  return imported.length === 1 ? imported[0] : null
}

/** The palette in force, as its tokens. Refused by name when the token layer names no one palette. */
export function paletteTokensInForce() {
  const file = paletteInForce()
  assert.ok(
    file !== null,
    `${TOKENS_FILE} imports ${file === null ? 'no single local stylesheet' : 'something this reader cannot read'}: the site paints with the palette in force, and a gate that cannot name it cannot compute a pair it paints`,
  )
  return paletteTokens(file)
}

/**
 * A palette value as `[r, g, b]`, refused rather than skipped when it is not an opaque hex
 * literal. `name` is what a reader of the failure is looking for — a token (`--text`), or a
 * pair's two ends.
 */
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
