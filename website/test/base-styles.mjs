// website/test/base-styles.mjs — the page chrome reaches every built page.
//
// The defect this file exists for: `tokens.css` was in every built page and nothing applied
// it, so a browser showed a white page under the palette's pale text, an unstyled bullet
// list for a nav, serif body text and default blue links. `src/styles/base.css` is the layer
// that applies the tokens; these cases hold the *built* pages to carrying it, after the
// tokens, with the chrome it styles (a labelled nav, keyboard-reachable links, the current
// page marked). The colour side — no literal in base.css, every pair it paints checked for
// all three palettes — is in `a11y.mjs`, beside the contrast arithmetic it reuses.
//
// **The third subject is the flow's own layout** (changes spec of 2026-10-18, item 4):
// `ArchitectureDiagram.astro`'s four cards are a quarter of the row each, and the row was
// laid out from 48 rem, where a card's content box is 100 px wide and the words its own step
// names are made of do not fit in that. A diagram that cannot print its own labels is the
// picture failing at the one thing it is for, so the arithmetic is here, beside the other
// layout rules: a card's content box at the breakpoint and at the page's maximum width,
// against the width of the longest word the flow's names are built from.
//
// **Two more readings of the same kind, both of them defects a browser shows rather than a
// preference: the arrows point the way the run travels, and a command block's copy control
// sits at the right of the block it belongs to.** The first is `transform: rotate(90deg)` on
// an arrow drawn pointing down, which turns it leftwards — from each step back at the one
// before it — with no test in the repository reading the direction at all. The second is
// `justify-content: space-between` on a row whose first child in the markup is the shared
// control, which puts the control at the left of its command, the other way round from
// `base.css`'s rule and from every other command block on the site. Both are read from the
// built page as arithmetic or as a cascade, because both are invisible to a check that only
// counts elements: `home.mjs` counts the flow's three arrows and `copy.mjs` clicks the
// control, and neither of them says which way the one points or where the other is drawn.
//
// The pages are read from `dist/`, which `npm run check` builds before it runs this file.
// No browser, no network: the checks are over the HTML and CSS a browser would receive.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync, readdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { cssDeclarations, cssRules, parse, textOf, walk } from './lib/html.mjs'

const SITE = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const DIST = path.join(SITE, 'dist')

function builtPages() {
  assert.ok(existsSync(path.join(DIST, 'index.html')), 'dist/index.html is missing: run `npm run build` (npm run check does it first)')
  const pages = []
  const walk = (dir) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const full = path.join(dir, entry.name)
      if (entry.isDirectory()) walk(full)
      else if (entry.name === 'index.html') pages.push(full)
    }
  }
  walk(DIST)
  return pages.map((file) => ({ rel: path.relative(DIST, file), html: readFileSync(file, 'utf8') }))
}

// The CSS a page carries: its inline `<style>` blocks, and any first-party stylesheet it
// links (Astro inlines today — `build.inlineStylesheets: 'always'` — but the check does not
// depend on which of the two it emits).
function pageCss(html) {
  const inline = [...html.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((m) => m[1])
  const linked = [...html.matchAll(/<link[^>]*rel="stylesheet"[^>]*href="(\/[^"]+)"/g)].map((m) =>
    readFileSync(path.join(DIST, m[1]), 'utf8'),
  )
  return [...inline, ...linked].join('\n')
}

test('every built page carries the base stylesheet, after the tokens it applies', () => {
  for (const { rel, html } of builtPages()) {
    const css = pageCss(html)
    const tokensAt = css.search(/--ground:\s*#/)
    const body = /body\{background:var\(--ground\);color:var\(--text\);font-family:var\(--font-sans\)/.exec(css)
    assert.ok(tokensAt >= 0, `${rel} carries no palette tokens`)
    assert.ok(body, `${rel} carries no body{background:var(--ground);color:var(--text);...} rule: the tokens are declared and never applied`)
    assert.ok(tokensAt < body.index, `${rel} applies the tokens before it declares them`)
    for (const rule of [/\.site-nav-groups\{[^}]*display:flex/, /\.site-nav a\[aria-current=page\]\{/, /:focus-visible\{outline:var\(--focus-ring-width\) solid var\(--accent\)/, /\.site-footer\{/]) {
      assert.match(css, rule, `${rel} is missing the base rule ${rule}`)
    }
  }
})

// The `@media` blocks of a stylesheet, as `{ query, body }`, so a rule can be asserted to be
// *inside* the breakpoint it belongs to rather than merely present somewhere in the file. The
// minifier rewrites the query (`min-width: 48rem` comes out as `(width>=48rem)`) and strips
// the spaces around combinators, so both are matched loosely below.
function mediaBlocks(css) {
  const out = []
  const re = /@media[^{]*\{/g
  let match
  while ((match = re.exec(css))) {
    let depth = 1
    let i = re.lastIndex
    for (; i < css.length && depth > 0; i++) {
      if (css[i] === '{') depth++
      else if (css[i] === '}') depth--
    }
    out.push({ query: match[0], body: css.slice(re.lastIndex, i - 1) })
  }
  return out
}

// The layout the header and the menu are held to, as a function over one built page's CSS, so
// that each part of it is proved by the fixture below rather than only observed on today's
// stylesheet. It states four things, and each of them is a defect rather than a preference:
//
//   - **the menu is three labels in the header's one row, at every width.** The owner's brief
//     of 2026-10-19 (item 1) took the single "Menu" toggle away, so the three group labels are
//     the menu at 375 px as much as at 1280: `.site-nav-groups` is a row at every width, no
//     rule for the drawer it replaced survives, and nothing hides a group's list — a closed
//     `<details>` is the one thing that does that, and a stylesheet that hides a list too is a
//     menu that cannot open;
//   - **the header is one row that cannot wrap** (RF3 measured 177 px of three wrapped nav
//     rows on a 390 px first screen). A group's list hangs out of that row's flow rather than
//     joining it, at both widths: a phone's drawer across the header's width, a wide
//     viewport's dropdown under its own label;
//   - **a phone's row fits.** The wordmark's text is taken out of the row below the phone
//     breakpoint (the owner's ruling: the mark stays, the text goes), and the gap between the
//     wordmark and the labels is the phone's 8 px rather than the wide layout's 32 px. Those
//     two are what the row measures against at 320 px, and neither is decoration: with the
//     text drawn, or with the wide gap, the row is wider than the header's content box and
//     the page gets a sideways scroll;
//   - **opening anything must not move the page.** A group's list is out of the header's flow,
//     so opening *Product* at 1280 px must not grow the header and push the page down under
//     the reader; and on a wide viewport the dropdown hangs from the group's **right** edge,
//     because a list opening rightwards from the last group runs past the viewport between the
//     breakpoint and the page's maximum width, where the header's padding is still narrow.
//
// Whether the row *fits* 390 px, and whether a real browser's `<details>` behaves as the
// markup says, are rendered measurements no hermetic test can take; they are the owner's live
// check 70a (P3), which asks for exactly these things in a browser.
export function navLayoutFailures(css, rel = '') {
  const out = []
  const fail = (why) => out.push(rel ? `${rel}: ${why}` : why)

  // The row, and the labels in it.
  if (!/\.site-header\{[^}]*flex-wrap:nowrap/.test(css)) fail('the header wraps, so the menu can become a second row')
  if (!/\.site-header\{[^}]*position:relative/.test(css)) fail("the header is not the box a phone's drawer is positioned against")
  if (!/\.site-nav-groups\{[^}]*display:flex/.test(css)) fail("the three group labels are not laid out in the header's row")
  if (!/\.site-nav-group ul\{[^}]*position:absolute/.test(css)) {
    fail("a group's list is in the header's flow, so opening one grows the header's row and pushes the page down")
  }
  if (!/\.site-nav-group ul\{[^}]*left:0/.test(css) || !/\.site-nav-group ul\{[^}]*right:0/.test(css)) {
    fail("a group's list is not the header's width on a phone, so the drawer is a column under one label instead of the menu")
  }
  for (const [, decls] of css.matchAll(/\.site-nav-group ul\{([^}]*)\}/g)) {
    if (/display:none/.test(decls)) {
      fail("a rule hides a group's list, and the list is the menu: a closed `<details>` is what hides it, and a stylesheet that hides it too leaves nothing that can open it")
    }
  }
  // The toggle this menu replaced. Its styles outliving it would be a second control in the
  // markup's absence, and the first sign that the drawer was half removed.
  if (/\.site-nav-(?:toggle|panel|drawer)\b/.test(css)) {
    fail('the stylesheet still styles the drawer the menu no longer has')
  }

  // The row at a phone's width: the text out of it, and the gap the phone's rather than the
  // wide layout's. `min-height` is the target size the owner's brief asks the menu to be
  // measured against — 44 px, on the labels and on the links inside a list.
  if (!/\.site-nav-group>details>summary\{[^}]*min-height:44px/.test(css)) {
    fail("a group's label is a tap target smaller than the 44 px the owner's brief asks for")
  }
  if (!/\.site-nav a\{[^}]*min-height:44px/.test(css)) {
    fail("a link inside a group's list is a tap target smaller than the 44 px the owner's brief asks for")
  }
  // The base header rule and not the wide block's, which is where the 32 px belongs: the
  // stylesheet is read as rules, so the one with no condition over it is the one at 320 px.
  const baseHeader = cssRules(css).find((r) => !(r.conditions ?? []).length && r.selector === '.site-header')
  if (!baseHeader || /var\(--space-lg\)/.test(baseHeader.body ?? '')) {
    fail("the header's gap between the wordmark and the labels is the wide layout's 32 px at 320 px, where the row has none to spare")
  }

  const phone = mediaBlocks(css).filter((block) => /24\.375rem/.test(block.query))
  if (phone.length === 0) {
    fail("the stylesheet has no phone breakpoint, so the wordmark's text is drawn beside the labels at 375 px")
  }
  const hidden = phone.map((block) => /\.site-wordmark-name\{([^}]*)\}/.exec(block.body)).find(Boolean)?.[1]
  if (phone.length && !hidden) fail("the wordmark's text is drawn beside the labels at 375 px, where the row has no room for it")
  if (hidden && !/position:absolute/.test(hidden)) {
    fail("the wordmark's text is taken out of the row without being taken out of the flow, so the row is still as wide as the word")
  }
  if (hidden && /display:none|visibility:hidden/.test(hidden)) {
    fail("the wordmark's text is hidden from a screen reader as well as from the eye, and the logo beside it is decoration with an empty alt: the link to the home page is left naming nothing")
  }

  const wide = mediaBlocks(css).filter((block) => /48rem/.test(block.query))
  if (wide.length === 0) {
    fail('the stylesheet has no 48rem breakpoint, so the menu has one layout at every width')
    return out
  }
  // The wide block's rules, read as declarations rather than as text: a rule is matched by its
  // selector and a declaration by `property:value`, so a case below can take one declaration
  // out of a real stylesheet and leave the rest of it standing.
  const rule = (name) => wide.map((block) => new RegExp(`\\${name}\\{([^}]*)\\}`).exec(block.body)).find(Boolean)?.[1]
  const has = (name, declaration) => (rule(name) ?? '').split(';').filter(Boolean).some((d) => declaration.test(d))

  if (!has('.site-header', /^gap:var\(--space-2xs\) var\(--space-lg\)$/)) {
    fail('the wide viewport keeps the phone\'s 8 px between the wordmark and the labels, where the wide layout has room for 32 px')
  }
  if (!has('.site-nav-group', /^position:relative$/)) {
    fail("a group is not the box its dropdown is positioned against, so at a wide viewport the list is still anchored to the header and is the header's width")
  }
  // The base rule puts the list out of the header's flow and this block must leave it there:
  // a wide rule that names a `position` of its own is a rule that takes it back in.
  if (/position:(?:static|relative)/.test(rule('.site-nav-group ul') ?? '')) {
    fail("a group's dropdown is in the header's flow, so opening one at a wide viewport grows the header's row and pushes the page down")
  }
  if (!has('.site-nav-group ul', /^left:auto$/)) {
    fail("a group's dropdown keeps the phone's full width at a wide viewport, so it hangs across the header instead of under its label")
  }
  // And out of flow *rightwards of nothing*: the menu sits at the right of the header, so a
  // list opening to the right of the last group runs past the viewport between the breakpoint
  // and the page's maximum width, where the header's padding is still narrow — a sideways
  // scroll on a laptop for opening a menu.
  if (!has('.site-nav-group ul', /^right:0$/)) {
    fail("a group's dropdown opens from its group's left edge, so the last group's list runs past the viewport between the breakpoint and the page's maximum width and gives the page a sideways scroll")
  }
  return out
}

test('the header cannot become two rows, and the wide row is where the menu is', () => {
  const failures = []
  for (const { rel, html } of builtPages()) failures.push(...navLayoutFailures(pageCss(html), rel))
  assert.deepEqual(failures, [], `the built stylesheet breaks the layout P3 states:\n  ${failures.join('\n  ')}`)
})

// ---- the layout refusals, kept -----------------------------------------------------------

// Today's stylesheet, as the fixture that passes: every plant below is one edit to it, so a
// case is the real rule with the one thing it names taken out, rather than a smaller
// stylesheet the rule happens to accept. `plant` refuses to build a fixture it cannot make —
// a `replace` that matches nothing would otherwise leave a passing stylesheet behind and the
// case would fail for the wrong reason (or worse, pass).
const TODAY = () => pageCss(readFileSync(path.join(DIST, 'index.html'), 'utf8'))

/**
 * Today's stylesheet with one fragment replaced — the fixture every case below is made of.
 * `which` picks the occurrence: the first by default, and `'last'` for the fragments the
 * flow's three layouts each write. The arrow's turn is the one that needs it: `rotate(-90deg)`
 * is written twice, once for the two-by-two's row arrows and once for the row's, and a case
 * about the row has to edit the row's — the first occurrence is the two-by-two's, which the
 * check reads at 35 rem and not at 72.
 */
function plant(from, to, which = 'first') {
  const css = TODAY()
  assert.ok(css.includes(from), `this fixture is built by removing ${JSON.stringify(from)} from the built stylesheet, and the stylesheet no longer carries it`)
  if (which === 'last') {
    const at = css.lastIndexOf(from)
    return css.slice(0, at) + to + css.slice(at + from.length)
  }
  return css.replace(from, to)
}

test('the layout rule passes on the built stylesheet, so each refusal below is the one edit it names', () => {
  assert.deepEqual(navLayoutFailures(TODAY()), [])
})

test('every layout refusal fires on the defect it names', () => {
  const cases = [
    // The row, and the three labels in it.
    [
      'a header that can wrap',
      plant('flex-wrap:nowrap;display:flex;position:relative}', 'display:flex;position:relative}'),
      /header wraps/,
    ],
    [
      'a header that is not the box a phone drawer hangs from',
      plant('flex-wrap:nowrap;display:flex;position:relative}', 'flex-wrap:nowrap;display:flex}'),
      /not the box a phone's drawer/,
    ],
    [
      "the three labels stacked instead of in the header's row",
      plant('.site-nav-groups{gap:var(--space-3xs);display:flex}', '.site-nav-groups{gap:var(--space-3xs)}'),
      /not laid out in the header's row/,
    ],
    [
      "a group's list left in the header's flow",
      plant('background:var(--ground);position:absolute;top:100%;left:0;right:0}', 'background:var(--ground);top:100%;left:0;right:0}'),
      /in the header's flow/,
    ],
    [
      'a phone drawer that is a column under one label',
      plant('position:absolute;top:100%;left:0;right:0}', 'position:absolute;top:100%;left:auto;right:0}'),
      /not the header's width/,
    ],
    [
      "a rule that hides a group's list",
      plant('background:var(--ground);position:absolute', 'background:var(--ground);display:none;position:absolute'),
      /hides a group's list/,
    ],
    [
      'the drawer this menu replaced, styled again',
      plant('.site-nav-groups{gap:var(--space-3xs)', '.site-nav-drawer{display:none}.site-nav-groups{gap:var(--space-3xs)'),
      /still styles the drawer/,
    ],
    [
      'a label smaller than the tap target the brief asks for',
      plant('box-sizing:border-box;min-height:44px;padding:var(--space-3xs)', 'box-sizing:border-box;padding:var(--space-3xs)'),
      /label is a tap target smaller/,
    ],
    [
      'a link smaller than the tap target the brief asks for',
      plant('.site-nav a{box-sizing:border-box;min-height:44px', '.site-nav a{box-sizing:border-box'),
      /link inside a group's list is a tap target/,
    ],
    [
      "the wide layout's gap left in the phone's row",
      plant('align-items:center;gap:var(--space-2xs);padding-block', 'align-items:center;gap:var(--space-2xs) var(--space-lg);padding-block'),
      /32 px at 320 px/,
    ],
    // The phone's row: the text out of it, and the text out of the eye only.
    [
      'no phone breakpoint at all',
      TODAY().replaceAll('(width<=24.375rem)', '(width<=99rem)'),
      /no phone breakpoint/,
    ],
    [
      "a phone breakpoint that leaves the wordmark's text in the row",
      plant('.site-wordmark-name{clip-path', '.site-wordmark-name-kept{clip-path'),
      /drawn beside the labels at 375 px/,
    ],
    [
      "the wordmark's text hidden without leaving the row's flow",
      plant(
        '.site-wordmark-name{clip-path:inset(50%);white-space:nowrap;width:1px;height:1px;position:absolute;overflow:hidden}',
        '.site-wordmark-name{clip-path:inset(50%);white-space:nowrap;width:1px;height:1px;overflow:hidden}',
      ),
      /without being taken out of the flow/,
    ],
    [
      "the wordmark's text hidden from a screen reader as well",
      plant('.site-wordmark-name{clip-path:inset(50%)', '.site-wordmark-name{display:none;clip-path:inset(50%)'),
      /naming nothing/,
    ],
    // The wide viewport.
    [
      'no 48rem breakpoint at all',
      TODAY().replaceAll('(width>=48rem)', '(width>=99rem)'),
      /no 48rem breakpoint/,
    ],
    [
      "the wide viewport keeping the phone's gap",
      plant('@media (width>=48rem){.site-header{gap:var(--space-2xs) var(--space-lg)}', '@media (width>=48rem){.site-header{gap:var(--space-2xs)}'),
      /room for 32 px/,
    ],
    [
      "a group that is not the box its dropdown hangs from",
      plant('padding-inline:var(--space-2xs)}.site-nav-group{position:relative}', 'padding-inline:var(--space-2xs)}.site-nav-group{position:static}'),
      /not the box its dropdown/,
    ],
    [
      "a wide dropdown left in the header's flow",
      plant('min-width:max-content;padding:var(--space-2xs)', 'position:static;min-width:max-content;padding:var(--space-2xs)'),
      /dropdown is in the header's flow/,
    ],
    [
      "a wide dropdown that is still the header's width",
      plant('border-radius:var(--radius-sm);left:auto;right:0}', 'border-radius:var(--radius-sm);left:0;right:0}'),
      /hangs across the header/,
    ],
    [
      "a group's dropdown opening rightwards off the viewport",
      plant('border-radius:var(--radius-sm);left:auto;right:0}', 'border-radius:var(--radius-sm);left:auto;right:auto}'),
      /sideways scroll/,
    ],
  ]
  for (const [what, css, expected] of cases) {
    const failures = navLayoutFailures(css)
    assert.ok(failures.some((f) => expected.test(f)), `${what} was not refused (${expected}); the rule said:\n  ${failures.join('\n  ') || '(nothing)'}`)
  }
})

test('the layout imports the stylesheets statically, tokens first', () => {
  const layout = readFileSync(path.join(SITE, 'src', 'layouts', 'Base.astro'), 'utf8')
  assert.ok(!/import\.meta\.glob\(/.test(layout), 'Base.astro still loads the styles through import.meta.glob: both files exist, so a missing one must fail the build')
  const tokens = layout.indexOf("import '../styles/tokens.css'")
  const base = layout.indexOf("import '../styles/base.css'")
  assert.ok(tokens >= 0 && base >= 0, 'Base.astro does not import tokens.css and base.css')
  assert.ok(tokens < base, 'Base.astro imports base.css before tokens.css')
})

// ---- the flow can print its own step names (changes spec item 4) -------------------------

/**
 * Every rule of a stylesheet, as `{ query, selector, decls }`, in document order, with the
 * `@media`/`@supports` prelude a rule sits inside — so a rule can be held to where it applies
 * rather than to being present somewhere in the file. The reading itself is `lib/html.mjs`'s
 * `cssRules`, the reader the other gates use: this only renames its parts, so that a
 * stylesheet one gate can read is a stylesheet every gate reads the same way.
 */
function allRules(css) {
  return cssRules(css).map((rule) => ({
    query: (rule.conditions ?? []).join(' '),
    selector: rule.selector ?? '',
    decls: rule.body ?? '',
  }))
}

/** A rule's declarations as `{ property: value }`, the last one winning, as a browser reads them. */
function declarations(rule) {
  return Object.fromEntries(cssDeclarations(rule.decls))
}

/**
 * True when a rule is in force at a viewport width: no `@media`, or a width range the width
 * falls in. **Both ends of a range are read.** The flow has three layouts and each is a range
 * of its own — the column below 35 rem, the two-by-two from 35 rem to 71.98 rem, the row from
 * 72 rem — and a reader that honoured only `min-width` would apply the two-by-two's rules at
 * 1280 px as well, where they are not in force at all: the last of them would win the reads
 * below and the row would be measured out of the wrong block. The end that made the defect
 * visible is the row's own: it has to be measured with the padding, the gap and the arrow box
 * of the block that actually lays it out.
 *
 * A query with no width in it at all — `prefers-reduced-motion`, `@supports` — is a rule the
 * width does not decide, and it is reported as not applying: the two animation rules are the
 * ones that matter here, and they are about motion rather than about a width.
 */
function appliesAt(width) {
  return (rule) => {
    if (!rule.query) return true
    const min = /width\s*>=\s*([\d.]+)rem/.exec(rule.query)
    const max = /width\s*<=\s*([\d.]+)rem/.exec(rule.query)
    if (min === null && max === null) return false
    if (min !== null && Number(min[1]) * 16 > width) return false
    if (max !== null && Number(max[1]) * 16 < width) return false
    return true
  }
}

/** A CSS length as pixels: a token, a `rem`/`px` length, or `calc(var(--x) * n)`. */
function toPx(value, tokens) {
  if (value === undefined) return NaN
  // A bare zero is zero pixels, and it is the one length CSS lets a rule write without a unit:
  // a shorthand that names a side `0` — `padding: var(--space-2xl) 0 var(--space-3xl)` — is a
  // padding this reader would otherwise read as `NaN`, which passes every comparison.
  if (/^0(?:px|rem|em|%)?$/.test(value.trim())) return 0
  const variable = /^var\(--([a-z0-9-]+)\)$/.exec(value)
  if (variable) return tokens.get(variable[1]) ?? NaN
  const calc = /^calc\(\s*var\(--([a-z0-9-]+)\)\s*\*\s*([\d.]+)\s*\)$/.exec(value)
  if (calc) return (tokens.get(calc[1]) ?? NaN) * Number(calc[2])
  const rem = /^([\d.]+)rem$/.exec(value)
  if (rem) return Number(rem[1]) * 16
  const px = /^([\d.]+)px$/.exec(value)
  return px ? Number(px[1]) : NaN
}

/**
 * The first component of a shorthand declaration, with a `calc()` kept whole. A `border:
 * calc(var(--border-width) * 2) solid var(--accent)` is a two-pixel border whose first
 * component is the `calc(…)`; splitting the shorthand on whitespace takes `calc(var(--border-width)`
 * instead, which is not a length at all — and a length read as `NaN` is a length nothing
 * checked (see the refusal for the arithmetic's inputs in `flowLayoutFailures`).
 */
function firstToken(value) {
  if (!value) return value
  const text = value.trim()
  if (!text.startsWith('calc(')) return text.split(/\s+/)[0]
  const end = closingParen(text, text.indexOf('('))
  return end === -1 ? text : text.slice(0, end + 1)
}

/**
 * A shorthand declaration's components, in order, with any `calc()` kept whole. Built on
 * `firstToken`, so `padding: var(--space-md) var(--space-2xs)` is two components and a
 * `calc()` in one of them stays one.
 */
function shorthandParts(value) {
  const out = []
  let rest = (value ?? '').trim()
  while (rest) {
    const token = firstToken(rest)
    if (!token) break
    out.push(token)
    rest = rest.slice(token.length).trim()
  }
  return out
}

/**
 * The growth factor a `flex` shorthand declares, as a number, or `NaN` when the shorthand is
 * not one this reader knows. The growth factor is the shorthand's first component when that
 * is a number (`flex: 1`, `flex: 1 1 auto`, the row's `flex: 1 1 0`), and the shorthand's own
 * keywords otherwise: `auto` is `1 1 auto` and grows, `none` is `0 0 auto` and does not, and
 * `initial` is `0 1 auto`. The keywords are not a nicety — a minifier writes the card's
 * `flex: 1 1 auto` as `flex: auto`, so a reader that only took the first component as a
 * number would read the page as it ships as a page whose cards do not grow.
 */
function growthOf(value) {
  if (value === undefined) return undefined
  const keyword = { auto: 1, none: 0, initial: 0 }[value.trim()]
  if (keyword !== undefined) return keyword
  const first = Number(shorthandParts(value)[0])
  return Number.isFinite(first) ? first : NaN
}

/**
 * The **inline** component of a shorthand — the room a `padding` takes from the sides, and
 * the gap a two-value `gap` puts between columns — which is the second component when there
 * is one and the first when the shorthand names a single value. The horizontal half is the
 * one that matters to the arithmetic below: a card's block padding is the reader's breathing
 * room and costs the name nothing, while its inline padding comes straight out of the box the
 * name has to print in.
 *
 * **Four values are `top right bottom left`**, so the room a side takes is the *last* one —
 * the left edge, the one a line of the card's content starts at — and not the second, which
 * is the right edge. Reading the second made a card whose left padding is what the name is up
 * against (`padding: var(--space-md) 0 var(--space-md) var(--space-3xl)`) look like a card
 * that takes no room at all at its sides, which is a name that fits the arithmetic and does
 * not fit the page. The arithmetic below spends what this returns on **both** sides, which is
 * an over-estimate when the two differ — and an over-estimate is the direction to be wrong in,
 * since it refuses a row that might not fit a name rather than passing one that does not.
 */
function inlineToken(value) {
  const parts = shorthandParts(value)
  if (parts.length === 4) return parts[3]
  return parts.length > 1 ? parts[1] : parts[0]
}

/** The tokens a stylesheet declares, as `name -> px`. */
function lengthTokens(css) {
  const out = new Map()
  for (const m of css.matchAll(/--([a-z0-9-]+):\s*([\d.]+)(rem|px)\b/g)) out.set(m[1], Number(m[2]) * (m[3] === 'rem' ? 16 : 1))
  return out
}

/**
 * An upper bound on how wide a run of text is, in ems, for the stack the site sets —
 * `--font-sans`, which is the reader's system UI sans. The table names the characters whose
 * advance is not the default, and every value is generous rather than measured: the arithmetic
 * below is used to say a name **fits**, and an upper bound that says it fits holds in any
 * browser, while a measurement taken from one machine's font is a claim about that machine
 * (which is also why no browser is needed to check this).
 */
const ADVANCE = new Map([
  ['i', 0.32], ['j', 0.34], ['l', 0.32], ['f', 0.4], ['r', 0.45], ['t', 0.45],
  ['m', 0.95], ['w', 0.85], ['M', 0.95], ['W', 1], [' ', 0.35],
])
const DEFAULT_ADVANCE = 0.68

/** The width of a run of text at `fontSize` px, as the upper bound above. */
export function textWidth(text, fontSize) {
  let ems = 0
  for (const ch of text) ems += ADVANCE.get(ch) ?? DEFAULT_ADVANCE
  return ems * fontSize
}

/**
 * The margin the longest step name must have left over on top of `textWidth`'s estimate, in
 * pixels, where the four cards stand in a row. The estimate is already an upper bound — every
 * character at least 0.68 em, and the wide ones at their own wider value — so this is the
 * second margin rather than the only one: a system font a little wider than the table, a
 * rounding in the browser, a hinting that adds a fraction of a pixel per glyph. Four pixels
 * is a third of a character, which is enough that the name is not *exactly* at the box's edge
 * when the estimate is right, and small enough that it never decides the layout on its own —
 * this reading gives the name **234 px** against an estimated 225 at the width the row starts
 * at, and the page's own box there is 246 px: this arithmetic counts `main`'s gutter as well as
 * the wrapper's, and the homepage's `main:has(> .hero)` shortcut drops `main`'s, so the number
 * read here is the narrower one — which is the direction a margin on a *fits* question should
 * err in. The row is the one layout where a name that wraps is the defect, so it is the one
 * place the margin is required rather than merely positive.
 */
const NAME_MARGIN = 4

/** The longest word of a name: what a card has to hold when the name wraps. */
function longestWord(names) {
  return names
    .flatMap((name) => name.split(/\s+/))
    .sort((a, b) => b.length - a.length)
    .at(0) ?? ''
}

// ---- the cap the measure puts on the flow's own list (changes spec item 4) ---------------
//
// `base.css` holds prose to the measure — `:where(p, ul, ol, dl, blockquote, figcaption)
// { max-width: var(--measure) }` — and the flow's four steps are an `<ol>`. That cap was
// invisible to every check in this file: the arithmetic below sizes a card from the page's
// own column, and a browser gives the list **the smaller of the column and the cap**. The
// measure is 68 ch, about 578 px at the body's 17 px, where the four cards and their three
// arrows want about 720: the row was drawn inside a 578 px box, the cards came out about
// 143 px wide, `Ephemeral cloud job` broke as `Ephemera / l cloud job`, and the picture item
// 4 is about was the narrowest thing in its own section. The declaration that fixes it is one
// line (`max-width: none` on `.flow-steps`), and what this reader adds is the reason it
// cannot be dropped quietly again: it resolves the cascade for that element's `max-width` —
// the rules that reach an `<ol class="flow-steps">`, in force at the width, highest
// specificity first and the last one winning a tie — turns the winner into pixels, and
// refuses any cap narrower than the row it lays out needs.
//
// The reader knows the selectors this site writes and no more: an element name, classes, an
// id, `:where()`, `:is()`, `:not()`, and state pseudo-classes (`:hover` and its kind, which a
// browser does not apply at rest). Anything else a rule could reach the list through — an
// attribute selector, a `::pseudo-element`, an ancestor part this reader cannot check against
// a page it is not given — is **reported rather than skipped**: a cap nobody could read is a
// cap nobody checked, which is how this defect shipped in the first place.

/**
 * The flow's list, as this reader models it: the `<ol class="flow-steps">` the component
 * writes, with no attributes beyond its class. The gate below reads the real element — name,
 * classes, id and every attribute it carries, Astro's scoping attribute included — out of the
 * built page and hands it to the reader, so `[data-astro-cid-…]` is answered rather than
 * guessed at; this is the fallback for a caller that has only a stylesheet.
 */
const FLOW_LIST = { name: 'ol', classes: ['flow-steps'], id: null, attrs: new Map() }

/** Pseudo-classes that are a state rather than a shape: a browser does not apply them at rest. */
const STATE_PSEUDO = new Set(['hover', 'focus', 'focus-visible', 'focus-within', 'active', 'visited', 'target'])

/**
 * A `ch` in pixels, at the element's own font size. `ch` is the advance of the font's `0`,
 * which no stylesheet states and no hermetic test can measure — the site sets `--font-sans`,
 * the reader's own system UI font. The value below is deliberately **below** what a digit
 * measures in the sans stacks the tokens name (they run about 0.55–0.6 em), so a cap that
 * fails here fails whatever the reader's font is: the failure is a statement about the
 * smallest cap the value could mean, not about one machine's fonts.
 */
const CH = 0.5

/** The parts of a list — a selector list, a value list — split on `separator` outside any parentheses. */
function splitTop(text, separator) {
  const out = []
  let depth = 0
  let start = 0
  for (let i = 0; i < text.length; i += 1) {
    const ch = text[i]
    if (ch === '(' || ch === '[') depth += 1
    else if (ch === ')' || ch === ']') depth -= 1
    else if (depth === 0 && ch === separator) {
      out.push(text.slice(start, i))
      start = i + 1
    }
  }
  out.push(text.slice(start))
  return out.map((part) => part.trim()).filter(Boolean)
}

/** A selector's compounds — one per element — with the combinators between them taken out. */
function compoundsOf(selector) {
  const out = []
  let depth = 0
  let current = ''
  for (const ch of selector) {
    if (ch === '(' || ch === '[') depth += 1
    else if (ch === ')' || ch === ']') depth -= 1
    if (depth === 0 && (ch === ' ' || ch === '>' || ch === '+' || ch === '~')) {
      if (current) out.push(current)
      current = ''
      continue
    }
    current += ch
  }
  if (current) out.push(current)
  return out
}

/** The index of the `)` that closes the `(` at `open`, or -1. */
function closingParen(selector, open) {
  let depth = 0
  for (let i = open; i < selector.length; i += 1) {
    if (selector[i] === '(') depth += 1
    else if (selector[i] === ')') {
      depth -= 1
      if (depth === 0) return i
    }
  }
  return -1
}

/** The index of the `]` that closes the `[` at `open`, with quoted values skipped, or -1. */
function closingBracket(selector, open) {
  let quote = ''
  for (let i = open + 1; i < selector.length; i += 1) {
    const ch = selector[i]
    if (quote) {
      if (ch === quote) quote = ''
      continue
    }
    if (ch === '"' || ch === "'") quote = ch
    else if (ch === ']') return i
  }
  return -1
}

/**
 * How an attribute selector reads against the element this reader was given. The element's own
 * attributes are carried in, rather than assumed: a component's stylesheet is scoped by the
 * attribute Astro adds to the elements it styles (`.flow-steps[data-astro-cid-…]`), so a
 * reader that could not answer for an attribute would report every component rule as
 * unreadable — and one that assumed every attribute selector matched would read a rule about
 * an element it has never seen as a cap on this one.
 */
function readAttribute(selector, element) {
  const parsed = /^\[\s*([\w-]+)\s*(?:([~^$*|]?=)\s*(?:"([^"]*)"|'([^']*)'|([^\]\s]+))\s*([iIsS])?\s*)?\]$/.exec(selector)
  if (!parsed) return { unreadable: `the attribute selector ${JSON.stringify(selector)}` }
  const [, name, operator, doubleQuoted, singleQuoted, bare, flag] = parsed
  const actual = element.attrs?.get(name)
  if (operator === undefined) return { matches: actual !== undefined }
  if (actual === undefined) return { matches: false }
  const insensitive = (flag ?? '').toLowerCase() === 'i'
  const have = insensitive ? actual.toLowerCase() : actual
  const want = insensitive ? (doubleQuoted ?? singleQuoted ?? bare ?? '').toLowerCase() : doubleQuoted ?? singleQuoted ?? bare ?? ''
  const answers = {
    '=': have === want,
    '~=': have.split(/\s+/).includes(want),
    '^=': have.startsWith(want),
    '$=': have.endsWith(want),
    '*=': have.includes(want),
    '|=': have === want || have.startsWith(`${want}-`),
  }
  return answers[operator] === undefined ? { unreadable: `the attribute operator ${JSON.stringify(operator)}` } : { matches: answers[operator] }
}

const IDENT = /^[a-zA-Z][\w-]*/

/**
 * How one compound of a selector reads against the flow's list: `{ matches, ratio }`, where
 * `ratio` is the compound's specificity as `[ids, classes, elements]` — or `{ unreadable }`
 * when the compound carries something this reader cannot evaluate **and could still match**.
 * A compound that already cannot match the element (another name, another class) answers
 * `matches: false` whatever else it carries, which is what keeps a rule about somebody else's
 * `::marker` out of this reader's way.
 */
function readCompound(compound, element) {
  const ratio = [0, 0, 0]
  let matches = true
  let i = 0
  const unreadable = (why) => (matches ? { unreadable: why } : { matches: false, ratio })
  if (compound[i] === '*') i += 1
  else {
    const name = IDENT.exec(compound.slice(i))
    if (name) {
      if (name[0].toLowerCase() !== element.name) matches = false
      ratio[2] += 1
      i += name[0].length
    }
  }
  while (i < compound.length) {
    const ch = compound[i]
    if (ch === '.') {
      const name = IDENT.exec(compound.slice(i + 1))
      if (!name) return unreadable(`a class in ${JSON.stringify(compound)} this reader cannot read`)
      if (!element.classes.includes(name[0])) matches = false
      ratio[1] += 1
      i += 1 + name[0].length
      continue
    }
    if (ch === '#') {
      const name = IDENT.exec(compound.slice(i + 1))
      if (!name) return unreadable(`an id in ${JSON.stringify(compound)} this reader cannot read`)
      if (element.id !== name[0]) matches = false
      ratio[0] += 1
      i += 1 + name[0].length
      continue
    }
    if (ch === '[') {
      const end = closingBracket(compound, i)
      if (end === -1) return unreadable(`the unbalanced ${JSON.stringify(compound)}`)
      const attribute = readAttribute(compound.slice(i, end + 1), element)
      if (attribute.unreadable) return unreadable(attribute.unreadable)
      if (!attribute.matches) matches = false
      ratio[1] += 1
      i = end + 1
      continue
    }
    if (ch === ':') {
      const double = compound[i + 1] === ':'
      const name = IDENT.exec(compound.slice(i + (double ? 2 : 1)))
      if (!name) return unreadable(`a pseudo-class in ${JSON.stringify(compound)} this reader cannot read`)
      const pseudo = name[0].toLowerCase()
      const after = i + (double ? 2 : 1) + name[0].length
      if (compound[after] === '(') {
        const end = closingParen(compound, after)
        if (end === -1) return unreadable(`the unbalanced ${JSON.stringify(compound)}`)
        if (double) return unreadable(`the pseudo-element ${JSON.stringify(compound.slice(i, end + 1))}`)
        if (pseudo !== 'where' && pseudo !== 'is' && pseudo !== 'not') {
          return unreadable(`the functional pseudo-class ${JSON.stringify(`:${pseudo}()`)} in ${JSON.stringify(compound)}`)
        }
        const readings = splitTop(compound.slice(after + 1, end), ',').map((arg) => readSelector(arg, element))
        if (readings.some((r) => r.unreadable)) return unreadable(`the ${JSON.stringify(`:${pseudo}()`)} in ${JSON.stringify(compound)}, whose own arguments this reader cannot evaluate`)
        // An empty `:where()`/`:is()` matches nothing — and an empty selector list is an
        // invalid selector, so the rule it is written in never applies at all.
        const any = readings.length > 0 && readings.some((r) => r.matches)
        // `:where()` contributes nothing to the specificity; `:is()` and `:not()` take their
        // most specific argument's, which is what makes `:where(ul, ol)` a zero the class
        // `.flow-steps` beats and a `:is(ol)` worth the element name it carries.
        const strongest = readings.map((r) => r.ratio).reduce((a, b) => (moreSpecific(b, a) ? b : a), [0, 0, 0])
        if (pseudo === 'not') {
          if (any) matches = false
          ratio[0] += strongest[0]
          ratio[1] += strongest[1]
          ratio[2] += strongest[2]
        } else if (pseudo === 'is') {
          if (!any) matches = false
          ratio[0] += strongest[0]
          ratio[1] += strongest[1]
          ratio[2] += strongest[2]
        } else if (!any) matches = false
        i = end + 1
        continue
      }
      if (double) return unreadable(`the pseudo-element ${JSON.stringify(`::${pseudo}`)}`)
      if (STATE_PSEUDO.has(pseudo)) {
        matches = false
        ratio[1] += 1
        i = after
        continue
      }
      return unreadable(`the pseudo-class ${JSON.stringify(`:${pseudo}`)} in ${JSON.stringify(compound)}`)
    }
    return unreadable(`the part of ${JSON.stringify(compound)} that begins at ${JSON.stringify(ch)}`)
  }
  return { matches, ratio }
}

/**
 * How a whole selector reads against the flow's list. A selector with more than one compound
 * reaches the element through an ancestor — `figure > ol` — and an ancestor is a fact about a
 * page this reader is not given, so it is reported rather than assumed either way.
 */
function readSelector(selector, element) {
  const compounds = compoundsOf(selector)
  const subject = compounds.at(-1)
  if (!subject) return { unreadable: `the empty selector ${JSON.stringify(selector)}` }
  const reading = readCompound(subject, element)
  // A rule about somebody else's element — `.site-footer ul`, `figure > ol` where there is no
  // such figure — is not a rule about this one, and saying so here is what keeps an unrelated
  // rule out of the refusals below. Only a selector that *does* reach the list and reaches it
  // through an ancestor is unreadable.
  if (reading.unreadable || !reading.matches) return reading
  if (compounds.length > 1) {
    return { unreadable: `${JSON.stringify(selector)}, whose ancestor part this reader cannot check against a page` }
  }
  return reading
}

/** True when `a` is the more specific of two `[ids, classes, elements]` ratios. */
function moreSpecific(a, b) {
  for (let i = 0; i < 3; i += 1) {
    if (a[i] !== b[i]) return a[i] > b[i]
  }
  return false
}

/** The values a stylesheet declares its tokens as, for following a `var()` to what it means. */
function tokenText(css) {
  const out = new Map()
  for (const m of css.matchAll(/--([a-z0-9-]+):\s*([^;{}]+)/g)) out.set(m[1], m[2].trim())
  return out
}

/** The font size the flow's list is set in: what its `ch` lengths are measured against. */
function elementFontPx(css) {
  const tokens = lengthTokens(css)
  const rules = allRules(css)
  const own = rules
    .filter((r) => /\.flow-steps(?![\w-])/.test(r.selector))
    .map((r) => declarations(r)['font-size'])
    .filter(Boolean)
    .pop()
  const body = rules
    .filter((r) => /(^|[,\s>])body(?![\w-])/.test(r.selector))
    .map((r) => declarations(r)['font-size'])
    .filter(Boolean)
    .pop()
  return toPx(own ?? body ?? '', tokens) || 16
}

/** A `max-width` value as pixels: `none` is no cap at all, and `NaN` is a value this reader cannot measure. */
function capPixels(value, tokens, pxTokens, fontPx) {
  const seen = new Set()
  let text = value.trim()
  for (;;) {
    const variable = /^var\(--([a-z0-9-]+)\)$/.exec(text)
    if (!variable) break
    if (seen.has(variable[1])) return NaN
    seen.add(variable[1])
    const next = tokens.get(variable[1])
    if (next === undefined) return NaN
    text = next
  }
  if (text === 'none') return Infinity
  const px = toPx(text, pxTokens)
  if (!Number.isNaN(px)) return px
  const ch = /^([\d.]+)ch$/.exec(text)
  return ch ? Number(ch[1]) * CH * fontPx : NaN
}

/**
 * The `max-width` in force on a list at a viewport width: `{ px, value, selector, query }`,
 * where `px` is `Infinity` for `none` or for no rule at all — or `{ unreadable }` when a rule
 * that reaches the element carries a value or a selector this reader cannot evaluate, which
 * the caller refuses rather than reads past. It is the flow's list by default and takes any
 * other list's element (`listElement`), because the cap it reads is `base.css`'s and reaches
 * every `ul`, `ol` and `dl` on the site.
 */
export function listMaxWidth(css, width, element = FLOW_LIST) {
  const rules = allRules(css)
  const tokens = tokenText(css)
  const fontPx = elementFontPx(css)
  const unreadable = []
  let best = null
  for (const rule of rules) {
    if (!appliesAt(width)(rule)) continue
    // Only a rule that declares a `max-width` can be a cap, and only those are read: a rule
    // about something else — `:root`'s tokens, a heading's size — is not this check's subject,
    // and reporting *its* selector as unreadable would be noise about a rule that cannot
    // squeeze anything.
    const value = declarations(rule)['max-width']
    if (value === undefined) continue
    let ratio = null
    for (const part of splitTop(rule.selector, ',')) {
      const reading = readSelector(part, element)
      if (reading.unreadable) {
        unreadable.push(`${reading.unreadable}${rule.query ? ` inside ${rule.query}` : ''}`)
        continue
      }
      if (!reading.matches) continue
      if (ratio === null || moreSpecific(reading.ratio, ratio)) ratio = reading.ratio
    }
    if (ratio === null) continue
    if (best === null || !moreSpecific(best.ratio, ratio)) best = { value, ratio, selector: rule.selector, query: rule.query }
  }
  // A rule that reaches this element and whose selector or value this reader cannot read is
  // reported rather than skipped: it may be the one that caps the list.
  if (unreadable.length > 0) return { unreadable: unreadable[0] }
  if (best === null) return { px: Infinity, value: 'none', selector: '', query: '' }
  const px = capPixels(best.value, tokens, lengthTokens(css), fontPx)
  if (Number.isNaN(px)) {
    return { unreadable: `${JSON.stringify(best.value)} on ${JSON.stringify(best.selector)}${best.query ? ` inside ${best.query}` : ''}` }
  }
  return { px, value: best.value, selector: best.selector, query: best.query }
}

/**
 * The flow's layout rules, as the stylesheet declares them: the row (the block that lays the
 * list out as a row of four) and the two-by-two (the block that gives it two columns), each
 * with the width its own query starts it at. The phone's column is neither — it is what a
 * list with no other layout is — so there is nothing to find for it.
 *
 * Read in one place and handed to both callers, because three checks need the same blocks:
 * the name arithmetic needs the row, the two-by-two's word arithmetic needs the grid, and the
 * arrows need both to know which shapes to check at which widths. A block that is not there is
 * `null`, and what that means is the caller's own refusal: a missing row means the picture is
 * not drawn, a missing two-by-two only means the check about it has nothing to measure.
 */
function flowLayouts(css) {
  const rules = allRules(css)
  const find = (test) => rules.find((r) => /\.flow-steps(?![\w-])/.test(r.selector) && r.query && test(r))
  const read = (rule) => {
    if (rule === undefined) return null
    const from = /width\s*>=\s*([\d.]+)rem/.exec(rule.query)
    return { rule, from: from === null ? null : Number(from[1]) * 16 }
  }
  return {
    row: read(find((r) => /flex-direction:row/.test(r.decls))),
    grid: read(find((r) => declarations(r)['grid-template-columns'] !== undefined)),
  }
}

/**
 * The shape the flow is drawn in at a viewport width: `'column'` (one card under another),
 * `'grid'` (the two-by-two) or `'row'` (four cards side by side). The browser's own reading of
 * the stylesheet — every rule that reaches the list and is in force at the width, in document
 * order, the last one that states a layout winning — so a band's own edges are read rather
 * than assumed from the breakpoints the three blocks name.
 *
 * The list starts as a column because that is what it is with no other layout: the base rule
 * declares it, and the phone's shape is not a rule of its own. A rule that lays the list out
 * in a grid is the two-by-two; `flex-direction: row` is the four-across row. Nothing else in
 * this component states a layout for `.flow-steps`, which is why a rule this reader does not
 * recognise cannot hide a fourth shape: it would have to declare one of these two properties.
 */
export function flowLayoutAt(css, width) {
  let shape = 'column'
  for (const rule of allRules(css)) {
    if (!/\.flow-steps(?![\w-])/.test(rule.selector)) continue
    if (!appliesAt(width)(rule)) continue
    const decls = declarations(rule)
    if (decls['grid-template-columns'] !== undefined) shape = 'grid'
    else if (decls['flex-direction'] === 'row') shape = 'row'
    else if (decls['flex-direction'] === 'column') shape = 'column'
  }
  return shape
}

/**
 * Every way the flow's four-across row fails to render the step names it prints, for one
 * built page's stylesheet and the names that page carries. Five things are checked, and each
 * is the defect rather than a preference:
 *
 *   - **the name may wrap.** A step name held on one line is a name that overflows its card
 *     the moment the card is narrower than the name, and the cards are a quarter of the row;
 *   - **a card is wide enough for the longest word its names are made of.** With the name free
 *     to wrap, the limit is the longest single word ("Ephemeral", in the spec's item 4's own
 *     step names) at the size the name is set: the flow's own breakpoint has to be a width at
 *     which the row has that much room, and inside a card the number may not take it away;
 *   - **the longest name is one line in the row.** This is the defect the review of this
 *     branch measured at 1280 px: `Ephemeral cloud job` broke onto two lines while the three
 *     names beside it fitted on one, so that card grew and the row's bottom edge went ragged.
 *     The estimate is this file's own per-character table (`textWidth`) at the size the
 *     stylesheet sets, it is an upper bound rather than a measurement, and the check asks for
 *     a margin on top of it (`NAME_MARGIN`) — see the constant;
 *   - **the four cards are one height.** The steps stretch to the row, each card grows to fill
 *     its step (`flex: 1 1 auto` in the column-flex step), and the card's content starts at its
 *     top. Three declarations and three refusals: a row whose steps are `align-items: center`
 *     gives each card its own content's height (how card 4 came out about 30 px shorter than
 *     card 3), a card with no growth factor of its own does the same inside a step that is the
 *     row's tallest, and a card that packs its content down itself spreads the number, the name
 *     and the caption;
 *   - **the two-by-two's columns hold the longest word.** Between a phone and the full row the
 *     steps are `1 2 / 3 4`, so a card is half the column rather than a quarter — a different
 *     box, and the one a 768 px screen reads. It is checked at the width the two-by-two starts
 *     at, where its cards are narrowest.
 *
 * The width a card gets is the arithmetic the layout does: `main` is the page's column
 * (`--page-max` wide, centred, with its gutter), the classes between `main` and the figure
 * (`wrappers`, read off the page) add the gutters a page puts above the flow, the row is four
 * cards with an arrow between each pair, and a card's content box is what its padding and
 * border leave. Every one of those numbers is read from the stylesheet rather than typed here,
 * so the check follows a token that moves. Where a shortcut could make the page's own gutter
 * smaller — the homepage's `main:has(> .hero)` drops `main`'s padding, and `.home-flow` puts it
 * back — the arithmetic takes the padding the rules declare and ignores the shortcut, so the
 * width it works from is a floor rather than the page's own.
 */
export function flowLayoutFailures(css, names, wrappers = [], element = FLOW_LIST) {
  const out = []
  const rules = allRules(css)
  const tokens = lengthTokens(css)
  const declsOf = (klass, where = () => true) => rules.filter((r) => new RegExp(`\\.${klass}(?![\\w-])`).test(r.selector) && where(r)).map(declarations)

  // The block that lays the flow out as a row of four: without it there is no row to check,
  // and the picture the spec asks for is missing rather than too narrow.
  const row = flowLayouts(css).row
  if (!row) {
    out.push("the flow has no four-across row: item 4's picture is the four steps travelling left to right, and no rule lays them out as a row")
    return out
  }
  if (row.from === null) {
    out.push(`the flow's four-across row is not inside a min-width breakpoint (${JSON.stringify(row.rule.query)}): the width a card gets cannot be read, and a row that starts at no width is a row that starts at every width`)
    return out
  }
  const at = row.from

  // The name may wrap, at every width: one rule holding it on a line is the whole defect.
  for (const rule of rules) {
    if (!new RegExp('\\.step-name(?![\\w-])').test(rule.selector)) continue
    if ((declarations(rule)['white-space'] ?? '').startsWith('nowrap')) {
      out.push(`the step name is held on one line (${rule.query ? `inside ${rule.query}: ` : ''}white-space: nowrap), so a name wider than the card it sits in runs out of the card rather than wrapping`)
    }
  }
  if (names.length === 0) return out

  const nameSize = toPx(declsOf('step-name').map((d) => d['font-size']).filter(Boolean).pop(), tokens)
  const number = toPx(declsOf('step-number').map((d) => d['width']).filter(Boolean).pop(), tokens)
  const headGap = toPx(declsOf('step-head').map((d) => d['gap']).filter(Boolean).pop(), tokens)
  const gap = toPx(declarations(row.rule)['gap'], tokens)

  /**
   * The last value a class's own rules declare for a property, among the rules in force at a
   * width — the way a browser reads one property of one element, with the later rule winning.
   * The width is the point: each of the flow's three layouts sets the card's padding, its
   * border and the arrow's box differently, so "the card's padding" is a number with a width
   * attached, and reading one number for all of them is how a rule that is not in force at the
   * width being measured could decide what that width measures.
   */
  const declared = (klass, property, width) =>
    rules
      .filter((r) => new RegExp(`\\.${klass}(?![\\w-])`).test(r.selector) && appliesAt(width)(r))
      .map((r) => declarations(r)[property])
      .filter((value) => value !== undefined)
      .pop()

  /**
   * The card's inline padding at a width — this is the number that comes off the box the name
   * has to print in, and it is why the shorthand is read for its inline component rather than
   * its first: the row's cards carry `var(--space-md) var(--space-2xs)`, and a reader that
   * took the block padding for the inline one would think the name had 32 px more than it has.
   */
  const cardPadAt = (width) => toPx(inlineToken(declared('step-card', 'padding', width)), tokens)
  /** The card's border at a width, from the first component of the shorthand. */
  const borderAt = (width) => toPx(firstToken(declared('step-card', 'border', width)), tokens)
  /** The arrow's declared box at a width, whether or not that box takes room in the flow. */
  const arrowBoxAt = (width) => toPx(declared('step-arrow', 'width', width), tokens)
  /**
   * The width the arrow takes out of a step's share of the row: its box, or **nothing** when it
   * is absolutely positioned in the gap between two cards. That is what it is in both layouts
   * where the cards share the width, and it is the defect this reading was added for: the arrow
   * used to sit in the row's own flow, 32 px wide inside three of the four steps, so the
   * fourth step's card was a quarter of the row wider than the other three.
   */
  const arrowWidthAt = (width) => {
    const position = declared('step-arrow', 'position', width)
    return position === 'absolute' || position === 'fixed' ? 0 : arrowBoxAt(width)
  }

  // Every number the arithmetic below is made of, refused here when this reader could not turn
  // it into pixels rather than carried into the sums. `NaN` is false against every comparison,
  // so a length read as `NaN` makes both refusals below silent: the row would be reported as
  // fitting because the check stopped measuring it. That is not hypothetical — the card's
  // border became `calc(var(--border-width) * 2)` when its edge grew, the reader took the
  // first whitespace-separated word of the shorthand and read no length from it, and for as
  // long as that lasted this check proved nothing about the names.
  const lengths = [
    ['the size the step names are set in (`.step-name`’s font-size)', nameSize],
    ['the step number’s box (`.step-number`’s width)', number],
    ['the gap between the number and the name (`.step-head`’s gap)', headGap],
    ['the card’s padding (`.step-card`’s padding)', cardPadAt(at)],
    ['the card’s border (`.step-card`’s border)', borderAt(at)],
    ["the arrow's box (`.step-arrow`'s width)", arrowBoxAt(at)],
    ["the row's gap (`.flow-steps`'s gap inside its row breakpoint)", gap],
  ].filter(([, px]) => Number.isNaN(px))
  if (lengths.length > 0) {
    return [
      `the flow-layout arithmetic cannot be read: ${lengths.map(([what]) => what).join(', ')} is not a length this check can turn into pixels, and a length it cannot read is a length nothing checked (\`NaN\` passes every comparison below) — teach \`website/test/base-styles.mjs\` the shape, or write the declaration in one it reads (changes spec 2026-10-18, item 4)`,
    ]
  }

  /** The inline padding a rule declares: `inlineToken` of its `padding` shorthand. */
  const paddingInline = (rule) => toPx(inlineToken(declarations(rule)['padding']), tokens)

  /**
   * `main`'s gutter at a width: the last of its rules that applies there — the base rule and
   * every breakpoint at or below the width. `main:has(…)`'s `padding: 0` is left out on
   * purpose (see above): the wrappers below carry the gutter that shortcut takes away.
   */
  const mainPad = (width) =>
    rules
      .filter((r) => /(^|[,\s])main(?![\w-])/.test(r.selector) && !r.selector.includes(':has(') && appliesAt(width)(r))
      .map(paddingInline)
      .filter((value) => !Number.isNaN(value))
      .pop() ?? 0

  /** The gutter each wrapper class between `main` and the figure adds. */
  const wrapperPad = wrappers.reduce((sum, klass) => {
    const value = rules
      .filter((r) => new RegExp(`\\.${klass}(?![\\w-])`).test(r.selector))
      .map(paddingInline)
      .filter((px) => !Number.isNaN(px))
      .pop()
    return sum + (value ?? 0)
  }, 0)

  // The width the four-across row itself gets at a viewport: the page's column — `--page-max`
  // less the page's gutters — or the cap the measure puts on the list, whichever is narrower.
  // The second half is the reading the arithmetic above was missing, and the defect it missed:
  // `.flow-steps` is a `<ol>`, `base.css` holds lists to the measure, and a browser gives the
  // row the smaller of the two boxes rather than the column the card was sized against.
  const column = (width) => Math.min(width, tokens.get('page-max')) - 2 * mainPad(width) - 2 * wrapperPad
  const rowWidth = (width) => {
    const cap = listMaxWidth(css, width, element)
    return Math.min(column(width), Number.isNaN(cap.px) ? 0 : cap.px)
  }

  // The card's content box at a viewport: the row's width, shared between four cards, three
  // arrows and three gaps, less the card's own padding and border — every one of those read at
  // the width being measured, because the row block and the two-by-two block set them
  // differently and only one of the two is in force at a time.
  const contentBox = (width) => (rowWidth(width) - 3 * gap - 3 * arrowWidthAt(width)) / 4 - 2 * cardPadAt(width) - 2 * borderAt(width)

  // The head is a row (the number beside the name) until a rule turns it into a column, which
  // is how the name's own box is read: the number is `flex: none`, so a row takes its width and
  // its gap out of the name at every card width.
  const headIsColumn = (width) =>
    rules
      .filter((r) => /\.step-head(?![\w-])/.test(r.selector) && appliesAt(width)(r))
      .map((r) => declarations(r)['flex-direction'] ?? '')
      .filter(Boolean)
      .pop() === 'column'

  // The name the row has to fit on one line: the widest of the four, by the same per-character
  // estimate everything else in this file uses. Chosen by estimated width rather than by
  // character count, because the table is not one width per character — `Ephemeral cloud job`
  // is the widest of item 4's four and not the longest by letters.
  const longestName = (names_) => names_.reduce((a, b) => (textWidth(b, nameSize) > textWidth(a, nameSize) ? b : a), '')

  // What the row needs of its own box: four cards wide enough for **the longest name on one
  // line** (plus their own padding and border), the three arrows between them and the three
  // gaps. A box narrower than this is a row the browser has to squeeze the picture into — and
  // the longest name rather than the longest word, because the row is exactly the layout where
  // the name is not allowed to wrap.
  const cardNeeds = (names_) => textWidth(longestName(names_), nameSize) + 2 * cardPadAt(at) + 2 * borderAt(at)
  const rowNeeds = (names_) => 4 * cardNeeds(names_) + 3 * (arrowWidthAt(at) + gap)

  for (const width of [at, 80 * 16]) {
    const content = contentBox(width)
    const nameBox = headIsColumn(width) ? content : content - number - headGap
    const needs = textWidth(longestWord(names), nameSize)
    const cap = listMaxWidth(css, width, element)
    if (cap.unreadable) {
      out.push(
        `at ${width / 16} rem the flow's list is reached by a rule this check cannot read — ${cap.unreadable} — and a cap nobody can read is a cap nobody checked: teach \`website/test/base-styles.mjs\`'s cascade reader the construct, or write the rule in a shape it reads`,
      )
    } else if (cap.px < rowNeeds(names)) {
      out.push(
        `at ${width / 16} rem the flow's list is capped at ${Math.round(cap.px)} px (max-width: ${cap.value}` +
          `${cap.selector ? `, from ${JSON.stringify(cap.selector)}` : ''}${cap.query ? ` inside ${cap.query}` : ''}), and the four cards with their three arrows need ${Math.round(rowNeeds(names))} px: ` +
          `the list is an \`<ol>\`, base.css holds lists to the measure, and the cap squeezes the picture into a narrower box than it is drawn for (changes spec 2026-10-18, item 4)`,
      )
    }
    if (nameBox < needs) {
      out.push(
        `at ${width / 16} rem the flow's four-across row gives a step name ${Math.round(nameBox)} px and the longest word of the names it prints (${JSON.stringify(longestWord(names))}, at ${nameSize} px) needs ${Math.round(needs)} px: the row cannot render its own step names (changes spec 2026-10-18, item 4)`,
      )
    }

    // The name on one line. This is the defect the review of this branch measured at 1280 px:
    // `Ephemeral cloud job` broke onto two lines while the three names beside it fitted on one,
    // so that card grew and the row's bottom edge went ragged. The estimate is `textWidth`'s
    // per-character table at the size the stylesheet sets, it is an upper bound, and `NAME_MARGIN`
    // is the second margin on top of it.
    const nameNeeds = textWidth(longestName(names), nameSize)
    if (nameBox < nameNeeds + NAME_MARGIN) {
      out.push(
        `at ${width / 16} rem the flow's four-across row gives a step name ${Math.round(nameBox)} px and the longest of the four names it prints (${JSON.stringify(longestName(names))}, ${Math.round(nameNeeds)} px by this check's per-character estimate at ${nameSize} px, and at least ${NAME_MARGIN} px of margin on top of it) does not fit on one line: ` +
          `a name that wraps is a card taller than the three beside it, which is the ragged row the review of this branch measured (changes spec 2026-10-18, item 4)`,
      )
    }
  }

  // ---- the four cards are one height ------------------------------------------------------
  //
  // A row of cards whose bottoms do not line up reads as a broken row. The defect that shipped
  // was `.flow-step { align-items: center }` inside the row's own breakpoint: the step's cross
  // axis is its card, so the card took its own content's height, and the fourth card — the
  // shortest caption of the four — ended about 30 px above the third's. What the row needs is
  // `stretch` on the list (so the steps are one height) and on the step (so its card fills it),
  // with the card's own content packed from its top rather than spread down it.
  for (const width of [at, 80 * 16]) {
    for (const [klass, what] of [
      ['flow-steps', "the flow's row container"],
      ['flow-step', 'a step'],
    ]) {
      const align = declared(klass, 'align-items', width)
      if (align !== undefined && align !== 'stretch') {
        out.push(
          `at ${width / 16} rem ${what} aligns its items to \`${align}\`, and a flow whose items are aligned to anything but \`stretch\` gives each card its own content's height: the four cards of a row are one height, and the row's bottom edge is ragged otherwise (changes spec 2026-10-18, item 4)`,
        )
      }
    }
    // And the card's own growth, which is the property that makes the four one height: a step
    // is a column-flex box, so the axis the cards are sized along is its main axis and what
    // fills it is the card's `flex-grow` — `.step-card { flex: 1 1 auto }` — while the two
    // `align-items` above decide the other axis and the packing below decides where the
    // content sits. A card that does not grow keeps its own content's height inside a step
    // that is the row's tallest, which is the ragged bottom edge the review measured, and the
    // loop above cannot see it: nothing about `align-items` says whether the item grew.
    const cardFlex = declared('step-card', 'flex', width)
    const cardGrow = declared('step-card', 'flex-grow', width)
    const grow = cardGrow ?? growthOf(cardFlex)
    if (grow === undefined || Number.isNaN(grow)) {
      out.push(
        `at ${width / 16} rem the step card's growth factor cannot be read (\`flex: ${cardFlex ?? '(nothing declared)'}\`, \`flex-grow: ${cardGrow ?? '(nothing declared)'}\`), and a growth factor this check cannot read is one it cannot hold the four cards' height to: teach \`website/test/base-styles.mjs\` the shape, or write the declaration in one it reads (changes spec 2026-10-18, item 4)`,
      )
    } else if (grow <= 0) {
      out.push(
        `at ${width / 16} rem the step card does not grow (\`flex: ${cardFlex ?? '(nothing declared)'}\`, \`flex-grow: ${cardGrow ?? '(nothing declared)'}\`), and a card is a flex item of a step that is the row's tallest: without a growth factor of its own it takes its own content's height and the four cards' bottoms do not line up (changes spec 2026-10-18, item 4)`,
      )
    }
    const packing = declared('step-card', 'align-content', width) ?? declared('step-card', 'align-items', width)
    if (packing === undefined || !['start', 'flex-start'].includes(packing)) {
      out.push(
        `at ${width / 16} rem the step card packs its content with \`${packing ?? '(nothing declared)'}\`: a card is stretched to the height of the row's tallest step, so without \`align-content: start\` its number, its name and its caption are spread down it instead of starting at its top (changes spec 2026-10-18, item 4)`,
      )
    }
  }

  // ---- and the two-by-two, where a card is half the column rather than a quarter ----------
  //
  // Between a phone and the full row the steps are `1 2 / 3 4`, so the box a name has to print
  // in is a different box — half the column, its own padding, its own gap — and it is the one a
  // 768 px screen reads. Checked at the width the two-by-two starts at, where its cards are at
  // their narrowest; the longest word, because at half a column the name is free to wrap.
  const grid = flowLayouts(css).grid
  if (grid !== null && grid.from !== null) {
    const columns = declarations(grid.rule)['grid-template-columns']
    const count = columns.split(/\s+/).filter(Boolean).length
    const columnGap = toPx(inlineToken(declarations(grid.rule)['gap']), tokens)
    const pad = cardPadAt(grid.from)
    const edge = borderAt(grid.from)
    const unreadable = [columnGap, pad, edge].some((px) => Number.isNaN(px)) || count < 2
    if (unreadable) {
      out.push(
        `the two-by-two's arithmetic cannot be read: \`${columns}\` with a gap of \`${declarations(grid.rule)['gap'] ?? ''}\` on ${JSON.stringify(grid.rule.selector)} is not a grid this check can turn into pixels, and a box it cannot measure is a box nothing checked (changes spec 2026-10-18, item 4)`,
      )
    } else {
      const box = (rowWidth(grid.from) - (count - 1) * columnGap) / count - 2 * pad - 2 * edge
      const wordNeeds = textWidth(longestWord(names), nameSize)
      if (box < wordNeeds) {
        out.push(
          `at ${grid.from / 16} rem the flow is a two-by-two and a card there is ${Math.round(box)} px wide, and the longest word of the step names (${JSON.stringify(longestWord(names))}, at ${nameSize} px) needs ${Math.round(wordNeeds)} px: the picture a 768 px screen reads cannot print its own step names (changes spec 2026-10-18, item 4)`,
        )
      }
    }
  }
  return out
}

// ---- the flow's three bands, at the widths a reader meets them (review round 3, item 3e) ---
//
// The flow is three layouts in three bands of width, and until now only the ends of them were
// checked: `flowLayoutFailures` reads the block that starts the row and the block that starts
// the two-by-two, and measures the boxes they make, but nothing held the *bands* — where each
// one begins and ends, and what a reader at a width inside one of them actually gets. The
// review of round 3 asked for the middle band by name: "at 768 the diagram is a tall stack
// over 400 px", with the flow rendered as "a 2×2 grid ... stacking to one column only below
// about 560 px". A stack of four cards is what a stylesheet with no two-by-two at all gives,
// and the row blocks being present would not notice its absence.
//
// So this reads the shape in force at six widths rather than the blocks: the two pixels either
// side of the phone's end (559 and 560, which is the review's "about 560 px" read exactly —
// 35 rem), the width the review measured (768), the two either side of the row's start (1151
// and 1152, since the two-by-two ends at 71.98 rem), and the desktop width the other half of
// the round measured (1280).

/** The widths the flow's bands are read at, and the shape each one is supposed to give. */
const FLOW_BANDS = [
  [559, 'column', 'a phone: the four steps are one column of cards, and the third band has not begun'],
  [560, 'grid', "the two-by-two's first pixel — the review's “about 560 px”, which is 35 rem, and the width above which four stacked cards are the tall stack it measured"],
  [768, 'grid', 'the width the review measured: `1 2` over `3 4`, four cards in two rows rather than four stacked over 400 px'],
  [1151, 'grid', "the two-by-two's last pixel, since the row starts at 72 rem"],
  [1152, 'row', 'the four-across row, one pixel after the two-by-two ends'],
  [1280, 'row', 'the desktop width: the four steps stand side by side, left to right, and one band is in force'],
]

/**
 * Every way the flow's bands fail to give the reader at a width the picture the review asked
 * for, as the lines a failure lists. Two things are checked, and neither is a preference:
 *
 *   - **each band's shape**, at the widths `FLOW_BANDS` names — a phone's stack, the two-by-two
 *     a 768 px screen reads, the row. A missing two-by-two is the defect the review measured
 *     (four cards stacked over 400 px) and it leaves no trace in the blocks that are present,
 *     so it is read where a reader meets it;
 *   - **no band without a width.** A rule that lays the list out inside a query with no
 *     viewport width in it — `@supports`, `prefers-reduced-motion`, or no query at all beyond
 *     the base column — is a layout for every width, which is a fourth band nobody chose and
 *     one that would silently beat the three.
 */
export function flowBandFailures(css) {
  const out = []
  for (const rule of allRules(css)) {
    if (!/\.flow-steps(?![\w-])/.test(rule.selector)) continue
    const decls = declarations(rule)
    if (decls['grid-template-columns'] === undefined && decls['flex-direction'] === undefined) continue
    if (!rule.query) continue
    if (/width\s*[<>]=?\s*[\d.]+rem/.test(rule.query)) continue
    out.push(
      `the flow's list is laid out inside \`${rule.query}\`, a query with no viewport width in it: the flow has three bands (one column below 35 rem, the two-by-two to 71.98 rem, the row above that), and a layout that starts at no width is a layout that starts at every width (review round 3, item 3e; changes spec 2026-10-18, item 4)`,
    )
  }
  for (const [width, shape, why] of FLOW_BANDS) {
    const at = flowLayoutAt(css, width)
    if (at === shape) continue
    out.push(
      `at ${width} px the flow's four steps are a \`${at}\` and not a \`${shape}\`: ${why} (review round 3, item 3e; changes spec 2026-10-18, item 4)`,
    )
  }
  return out
}

// ---- How It Works: one left edge, and room under the title (review round 3, item 3d) ------
//
// The page is the picture and then the detail of it, and `main` gives all three of its
// top-level blocks one gutter: the `h1`, the flow's figure (which insets itself by nothing —
// `max-width` and `margin` only) and the detail section. What the review of round 3 found was
// the third block adding a gutter of its own on top of that one, so "The run, step by step"
// and the five steps under it were indented 24 px relative to the title and the picture above
// them — two left edges on one page, 24 px apart. The other half of the same finding is the
// gap between the title and the picture: `base.css` gives every `h1` 12 px under it, which is
// a heading's margin rather than the room a page's opening picture is met with.
//
// Both are read here rather than left to the eye: the section's inline padding comes out of
// the cascade at the desktop width, and the title's own rule is read for the room it declares
// under itself. The vertical padding of the section is not this check's business — the space
// between the picture and the detail of it is a rhythm the owner's eye owns (70a-70c).

/**
 * Every way the How It Works page's three top-level blocks do not share `main`'s left edge, or
 * its title sits against the picture under it, for one built page's stylesheet and markup.
 * Returns nothing on a page that carries no detail section (the homepage's content spec item 4
 * removed it there), so it can be run over every built page.
 */
export function runDetailFailures(css, html) {
  const out = []
  const classesOf = (el) => (el.attrs.get('class') ?? '').split(/\s+/).filter(Boolean)
  const section = [...walk(parse(html))].find((el) => classesOf(el).includes('run-detail'))
  if (!section) return out
  const title = [...walk(parse(html))].find((el) => classesOf(el).includes('page-title'))
  const rules = allRules(css)
  const tokens = lengthTokens(css)
  const declared = (klass, property) =>
    rules
      .filter((r) => new RegExp(`\\.${klass}(?![\\w-])`).test(r.selector) && appliesAt(80 * 16)(r))
      .map((r) => declarations(r)[property])
      .filter((value) => value !== undefined)
      .pop()

  const padding = declared('run-detail', 'padding')
  const inset = toPx(inlineToken(padding), tokens)
  if (Number.isNaN(inset)) {
    out.push(
      `the How It Works detail section's inline padding cannot be read from \`padding: ${padding ?? '(nothing declared)'}\`, and a padding this check cannot turn into pixels is one nothing checked (\`NaN\` passes the comparison below) — teach \`website/test/base-styles.mjs\` the shape, or write the declaration in one it reads (review round 3, item 3d)`,
    )
  } else if (inset > 0) {
    out.push(
      `the How It Works detail section insets its own content by ${Math.round(inset)} px inside the gutter \`main\` already gives it (\`padding: ${padding}\`), and the page's \`h1\` above it and the flow's figure between them start at that gutter: "The run, step by step" and the steps under it are ${Math.round(inset)} px right of the title and of the picture (review round 3, item 3d)`,
    )
  }

  if (!title) {
    out.push(
      "the How It Works page's `h1` carries no `page-title` class, so the room under it cannot be read: the title and the picture under it are the page's opening, and what separates them is this page's own rule rather than a heading's default margin (review round 3, item 3d)",
    )
    return out
  }
  const room = toPx(declared('page-title', 'margin-bottom'), tokens)
  const wanted = tokens.get('space-lg')
  if (Number.isNaN(room)) {
    out.push(
      `the How It Works title's bottom margin cannot be read from \`${declared('page-title', 'margin-bottom') ?? '(nothing declared)'}\`, and a margin this check cannot turn into pixels is one nothing checked (review round 3, item 3d)`,
    )
  } else if (room < wanted) {
    out.push(
      `the How It Works title has ${Math.round(room)} px under it, and the page opens with the picture item 4 draws: \`base.css\`'s heading margin is a heading's margin, and the picture the page is about is met with the room of a section opening — at least ${wanted} px (\`--space-lg\`) (review round 3, item 3d)`,
    )
  }
  return out
}

test('the detail under the picture shares the picture’s left edge, and the title has room', () => {
  const failures = []
  let checked = 0
  for (const { rel, html } of builtPages()) {
    if (!html.includes('run-detail')) continue
    checked += 1
    failures.push(...runDetailFailures(pageCss(html), html).map((line) => `${rel}: ${line}`))
  }
  assert.ok(checked > 0, 'no built page carries the run-detail section, and item 4 puts the detailed explanation under the picture on How It Works')
  assert.deepEqual(failures, [], `the How It Works page has two left edges:\n  ${failures.join('\n  ')}`)
})

test('every run-detail refusal fires on the defect it names', () => {
  const page = readFileSync(path.join(DIST, 'how-it-works', 'index.html'), 'utf8')
  const css = pageCss(page)
  /** The How It Works stylesheet with one fragment replaced — `plant` reads the homepage. */
  const change = (from, to) => {
    assert.ok(css.includes(from), `this fixture replaces ${JSON.stringify(from)}, and How It Works' stylesheet no longer carries it`)
    return css.replace(from, to)
  }
  const cases = [
    // The defect the review of round 3 measured: the section's own gutter on top of `main`'s.
    [
      'the section indented inside the page gutter',
      change('padding:var(--space-2xl) 0 var(--space-3xl)', 'padding:var(--space-2xl) var(--space-md) var(--space-3xl)'),
      /insets its own content by 24 px/,
    ],
    // And the title left with a heading's margin under it, which is where the picture started
    // 12 px below it.
    ['the title with a heading’s margin under it', change('margin-bottom:var(--space-xl)', 'margin-bottom:var(--space-xs)'), /px under it, and the page opens with the picture/],
    // A page whose title lost the class the room is declared on: the rule is the page's, and a
    // title that no longer carries it is a title nothing measured.
    ['a title without the page’s own class', css, /carries no `page-title` class/, page.replace('class="page-title"', '')],
  ]
  for (const [what, stylesheet, expected, html = page] of cases) {
    assert.deepEqual(
      runDetailFailures(stylesheet, html).map((line) => (expected.test(line) ? 'fired' : line)),
      ['fired'],
      `${what}: the refusal did not fire, and this is what it said instead`,
    )
  }
})

// ---- the homepage's vertical rhythm: one gap, not two (review round 5) ---------------------
//
// The homepage's top-level blocks carry their own gutter and the room below themselves — the
// hero's `--space-lg`, the two figures' `--space-3xl`, the install section's tail — because
// `main:has(> .hero)` drops `main`'s padding on this page and each block puts it back. That is
// what makes the space between two blocks the **previous** block's bottom padding, and the
// review of the built site measured what a second padding on the *next* block does to it: the
// install section carried `--space-lg` of its own on top of the flow's `--space-3xl`, so the
// band between the flow's last card and the "Install" heading was 7 rem + 2 rem = 144 px of
// blank page — the widest gap on the page, and twice the rhythm every other pair of blocks
// keeps. Whichever of the two blocks owns a gap, only one of them may pad it.
//
// It is read from the built page rather than from the sources: the top-level blocks of the
// homepage's `main`, in document order, and each one's own `padding` out of the cascade.
// Nothing here is about a *particular* pair — a page whose blocks all pad both sides of their
// gaps is the same defect four times, and the refusal names the pair it found.
//
// **Read at three widths**, which are the three the reviews of this branch used: 1280, 768 and
// a phone's 375. The padding that fixes the gap is not inside a breakpoint, so the gap is 112 px
// at all three — and that is the thing three readings hold, rather than one reading at the
// desktop width: a rule that gave a block a top padding *only* below some width would double one
// gap on that band and nowhere else, and a check that read one width would call the page's
// rhythm right while a phone showed two paddings under one heading. The third case below plants
// exactly that rule and proves the width is what decides it.

/** The viewport widths the homepage's rhythm is read at: the desktop the record measures, the
 * tablet the flow's two-by-two is drawn at, and a phone. */
export const RHYTHM_WIDTHS = [80 * 16, 48 * 16, 375]

/**
 * Every pair of the homepage's top-level blocks that both pad the gap between them, as the
 * sentences a reader of the failure gets. A page with no `main`, or with one block, has no
 * pair to read and is not a failure here.
 */
export function homeRhythmFailures(css, html) {
  const out = []
  const classesOf = (el) => (el.attrs.get('class') ?? '').split(/\s+/).filter(Boolean)
  const main = [...walk(parse(html))].find((el) => el.name === 'main')
  if (main === undefined) return out
  const blocks = [...walk(main)].filter((el) => el.parent === main)
  const rules = allRules(css)
  const tokens = lengthTokens(css)

  /**
   * A block's own vertical padding, out of the declarations in force at `width` on any of its
   * classes, read in the order a browser applies them: the block shorthand's first component
   * is the top and its third the bottom (the second when the shorthand names two values, the
   * first when it names one), and `padding-top`/`padding-bottom` override the shorthand for
   * the edge they name — including a rule that pads one edge and writes no shorthand at all,
   * which is how a block pads a gap without the word `padding` ever appearing. Reading the
   * shorthand alone made that block look like one that pads nothing, and the doubled gap the
   * review measured was the one a shorthand declared: the check could only see the shape it
   * was written for. `null` when nothing declares a vertical padding — a block with no
   * padding of its own is a block that cannot pad the gap. The width is a parameter because
   * the rules that decide it are read out of the cascade at that width: a block that pads the
   * gap on a phone and not on a desktop is the defect on the phone, and the reading has to be
   * taken there to see it.
   */
  const vertical = (block, width) => {
    const classes = classesOf(block)
    const inForce = rules.filter(
      (r) => classes.some((klass) => new RegExp(`\\.${klass}(?![\\w-])`).test(r.selector)) && appliesAt(width)(r),
    )
    let top
    let bottom
    for (const rule of inForce) {
      for (const [property, value] of cssDeclarations(rule.decls)) {
        if (property === 'padding') {
          const parts = shorthandParts(value)
          top = parts[0]
          bottom = parts.length > 2 ? parts[2] : parts[1] ?? parts[0]
        } else if (property === 'padding-top') {
          top = value
        } else if (property === 'padding-bottom') {
          bottom = value
        }
      }
    }
    if (top === undefined || bottom === undefined) return null
    return {
      top: toPx(top, tokens),
      bottom: toPx(bottom, tokens),
      // What a failure quotes: the two values the edges were read from, so a reader of the
      // sentence can find the rule to fix — one value when a shorthand set both, and both
      // when the edges came from declarations of their own.
      declared: top === bottom ? top : `top: ${top}, bottom: ${bottom}`,
    }
  }

  /** The name a block is reported by: its classes, as a reader finds them in the markup. */
  const nameOf = (block) => classesOf(block).map((klass) => `.${klass}`).join('') || block.name

  for (let i = 1; i < blocks.length; i += 1) {
    const above = blocks[i - 1]
    const below = blocks[i]
    for (const width of RHYTHM_WIDTHS) {
      const a = vertical(above, width)
      const b = vertical(below, width)
      if (a === null || b === null) continue
      if (Number.isNaN(a.bottom) || Number.isNaN(b.top)) {
        out.push(
          `at ${width} px the homepage's ${nameOf(above)} and ${nameOf(below)} carry a padding the rhythm check cannot turn into pixels (${JSON.stringify(a.declared)} and ${JSON.stringify(b.declared)}), and the room between two blocks is read from those two numbers: teach \`website/test/base-styles.mjs\` the shape, or write the declaration in one it reads (review round 5)`,
        )
        continue
      }
      if (a.bottom > 0 && b.top > 0) {
        out.push(
          `at ${width} px the homepage's ${nameOf(above)} ends with ${Math.round(a.bottom)} px under it and ${nameOf(below)} opens with ${Math.round(b.top)} px over it, so the gap between them is ${Math.round(a.bottom + b.top)} px — twice the rhythm the page's other blocks keep, because each of these two blocks pads the one gap (review round 5)`,
        )
      }
    }
  }

  return out
}

test('the homepage’s blocks pad the gap between them once, and not twice', () => {
  const failures = []
  let checked = 0
  for (const { rel, html } of builtPages()) {
    if (!html.includes('class="home-flow"')) continue
    checked += 1
    failures.push(...homeRhythmFailures(pageCss(html), html).map((line) => `${rel}: ${line}`))
  }
  assert.ok(checked > 0, 'no built page renders the homepage’s blocks, so the rhythm of the page was measured nowhere')
  assert.deepEqual(failures, [], `the homepage pads one gap twice:\n  ${failures.join('\n  ')}`)
})

test('the rhythm refusal fires on the doubled gap, and the pairs that keep the rhythm pass', () => {
  const html = readFileSync(path.join(DIST, 'index.html'), 'utf8')
  const cases = [
    // The defect as it shipped: the install section's own `--space-lg` on top of the flow's
    // `--space-3xl` under it — the 144 px band the review measured between the last card and
    // the "Install" heading. `'first'`: `HomeInstall.astro`'s rule is emitted before
    // `index.astro`'s in the built page, so the install's padding is the earlier occurrence and
    // the figures' the later one.
    [
      'an install section padding the gap the flow above it already pads',
      plant('padding:0 var(--space-md) var(--space-3xl)', 'padding:var(--space-lg) var(--space-md) var(--space-3xl)'),
      [/the gap between them is 144 px/],
    ],
    // And the same edit on the page's own rule for the two figures, which is one rule for both
    // of them: the chat's top padding over the hero's bottom one, and the flow's over the
    // chat's — two pairs padded twice by the one declaration, and both refused. It is the same
    // rule at every width, so it is refused at each of the three — which is what the count
    // below holds this case and the one above to.
    [
      'the figures padding the gaps the blocks above them already pad',
      plant('padding:0 var(--space-md) var(--space-3xl)', 'padding:var(--space-lg) var(--space-md) var(--space-3xl)', 'last'),
      [/the gap between them is 64 px/, /the gap between them is 144 px/],
    ],
    // **The case the three widths are for.** The same doubled gap, written inside a rule that is
    // in force on a phone and nowhere else: at 1280 and 768 the page's rhythm is right and at
    // 375 the install section pads a gap the flow above it already pads. A check that read one
    // width — the desktop width, which is what this one read before review round 5's follow-up —
    // passes this stylesheet, and the phone is where the reader sees two paddings under one
    // heading. Two details of the plant: `(width<=48rem)` would be in force at 768 as well,
    // because 48 rem **is** 768 px, so the band here is a phone's own 30 rem; and `appliesAt`
    // reads ranges written with `>=`/`<=` rather than `<`, so the plant is written in the
    // vocabulary the reader has.
    [
      'an install section that pads the gap only on a phone',
      `${TODAY()}@media screen and (width<=30rem){.home-install{padding:var(--space-lg) var(--space-md) var(--space-3xl)}}`,
      [/the gap between them is 144 px/],
      [375],
    ],
    // **The same doubled gap written as a longhand**, which is the shape the check could not
    // see: it read the `padding` shorthand out of the cascade and nothing else, so a rule that
    // pads one edge — `padding-top`, which is how a block that keeps the shorthand for its
    // sides writes its own top — left the block looking like one that cannot pad the gap at
    // all, and the 144 px band went unmeasured. The plant appends the rule rather than editing
    // the section's own, because a longhand in a rule of its own is exactly the case: the
    // declaration that pads the gap and the shorthand that does not are in two rules, and the
    // reading has to take the last one in force for each edge.
    [
      'an install section that pads the gap with a longhand',
      `${TODAY()}.home-install{padding-top:var(--space-lg)}`,
      [/the gap between them is 144 px/],
    ],
  ]
  for (const [what, css, expected, widths = RHYTHM_WIDTHS] of cases) {
    const failures = homeRhythmFailures(css, html)
    for (const pattern of expected) {
      for (const width of widths) {
        assert.ok(
          failures.some((line) => line.startsWith(`at ${width} px `) && pattern.test(line)),
          `${what}: at ${width} px the refusal ${pattern} did not fire, and this is what the check said instead:\n  ${failures.join('\n  ') || '(nothing)'}`,
        )
      }
    }
    const planted = expected.length * widths.length
    assert.equal(failures.length, planted, `${what}: the check refused ${failures.length} gap(s) where this case plants ${planted}:\n  ${failures.join('\n  ')}`)
  }

  // And the page as it ships: the pairs above, with the gap padded once, are not refused — so
  // the two cases are the edit and not the check.
  assert.deepEqual(homeRhythmFailures(TODAY(), html), [], 'the homepage as it ships pads one of its gaps twice')
})

// ---- the chat is one card, one conversation (review round 3, item 3a) ----------------------
//
// The chat was three cards across the page's column — one turn each, `auto-fit` with an 18 rem
// minimum — and the review of round 3 found what that draws: dead space inside each card, with
// the reader's one short line floating in the middle of a card whose height the two long ones
// set. What a conversation is instead is **one card with one column of lines**, the speaker at
// the left of every line, at the width a line of prose is set at everywhere else on the site.
// The four claims the arithmetic below holds are the ones that make it that shape:
//
//   1. **the list is one column** — a row of anything (`repeat(auto-fit, …)`, more than one
//      track, a `display: flex` row) is the shape the review refused;
//   2. **the list is the card** — the border, the radius and the background are the list's,
//      and a turn with a card of its own is the three-cards defect back again;
//   3. **the speakers are one column** — the label column is a definite width every row
//      shares, and the lines start at one edge down the whole conversation;
//   4. **the two speakers are on opposite sides** — the figure is a chat, so each turn's
//      body is drawn as a bubble, content-sized and tinted against the card, and the reader's
//      sits at one side of the card while the agent's sits at the other (review round 5).
//      A bubble is not the card-per-turn the third claim's era refused: those were three
//      cards laid out across the page and stretched to the row's tallest, and this is one
//      content-sized box inside a row of the one card.
//
// The third is why the column is a fixed length rather than `max-content`, and it is the one
// claim that is easy to get wrong in a way that looks right: `max-content` on a grid container
// is resolved **per row**, against that row's own label, so `You` and `Your agent` give two
// different columns and the two lines under them start 60 px apart. A content-sized column
// therefore aligns nothing, and this refuses one — and then refuses a definite column too
// narrow for the widest label in the markup, estimated with `textWidth` at the size the labels
// are set in, so a longer speaker name fails here rather than wrapping inside the card.
//
// The cap is read rather than assumed, as it is for the flow's list: the list is an `<ol>`,
// `base.css` holds lists to the measure, and the measure is what makes the card a readable
// width. A rule that opts the list out of it (`max-width: none`) puts the conversation back
// across the page, which is what the failure names.

/** The margin the widest speaker label must have left in the column, in px. See `NAME_MARGIN`. */
const LABEL_MARGIN = 4

/** The chat’s turns on one built page, as `{ who }` — the speaker each row is labelled with. */
export function chatSpeakers(html) {
  return [...walk(parse(html))]
    .filter((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('chat-turn'))
    .map((el) => {
      const who = [...walk(el)].find((child) => (child.attrs.get('class') ?? '').split(/\s+/).includes('turn-who'))
      return who ? textOf(who).replace(/\s+/g, ' ').trim() : ''
    })
}

/**
 * Every way the chat fails to be one card holding one conversation, at the width the desktop
 * layout is read at, for one built page’s stylesheet and markup.
 */
export function chatConversationFailures(css, html) {
  const out = []
  const speakers = chatSpeakers(html)
  if (speakers.length < 3) {
    return [
      `the page prints ${speakers.length} chat turn(s), and the figure this check sizes is item 3’s three-turn chat: the conversation it asks for cannot be read from fewer turns`,
    ]
  }
  const rules = allRules(css)
  const tokens = lengthTokens(css)
  const declared = (klass, property, width) =>
    rules
      .filter((r) => new RegExp(`\\.${klass}(?![\\w-])`).test(r.selector) && appliesAt(width)(r))
      .map((r) => declarations(r)[property])
      .filter((value) => value !== undefined)
      .pop()

  const DESKTOP = 80 * 16
  const PHONE = 390

  // ---- 1. the list is one column of lines --------------------------------------------------
  const columns = declared('chat-turns', 'grid-template-columns', DESKTOP)
  const display = declared('chat-turns', 'display', DESKTOP) ?? 'block'
  if (display === 'flex' && (declared('chat-turns', 'flex-direction', DESKTOP) ?? 'row').startsWith('row')) {
    out.push(
      "the chat's turns are a flex row, and a row of cards across the page is the shape the review of round 3 refused: three turns of a quarter of the page each leave dead space inside the cards, with the reader's one short line floating in the middle of a card the long ones sized (changes spec 2026-10-18, item 3a)",
    )
  }
  if (columns !== undefined) {
    const tracks = splitTop(columns, ' ')
    const repeats = /repeat\(/.test(columns)
    if (repeats || tracks.length !== 1) {
      out.push(
        `the chat's turns are laid out as ${repeats ? `\`${columns}\`` : `${tracks.length} columns`}, and the conversation is one column of lines in one card: a row of cards is what the review of round 3 found — dead space inside each card, and the reader's one short line floating in the middle of it (changes spec 2026-10-18, item 3a)`,
      )
    }
  }

  // ---- 2. the list is the card, and a turn is a row of it ----------------------------------
  if (declared('chat-turns', 'background', DESKTOP) === undefined || declared('chat-turns', 'border', DESKTOP) === undefined) {
    out.push(
      "the conversation's own list draws no card (`background` and `border` are the card, and one of them is missing): the turns are rows of one card rather than cards of their own (changes spec 2026-10-18, item 3a)",
    )
  }
  for (const klass of ['chat-turn', 'turn-who', 'turn-line', 'turn-status', 'turn-results']) {
    for (const property of ['background', 'border', 'border-radius']) {
      const value = declared(klass, property, DESKTOP)
      if (value === undefined) continue
      out.push(
        `a .${klass} draws a card of its own (\`${property}: ${value}\`), and the conversation is one card with the turns as its rows: a card per turn is the shape the review of round 3 refused, whatever the width it is drawn at (changes spec 2026-10-18, item 3a)`,
      )
    }
  }

  // ---- 3. the card is a readable width, not the page ---------------------------------------
  const cap = listMaxWidth(css, DESKTOP, listElement(html, 'chat-turns', 'item 3 puts the chat on the homepage'))
  if (cap.unreadable) {
    return [
      `the chat’s turns are reached by a rule this check cannot read — ${cap.unreadable} — and a cap nobody can read is a cap nobody checked: teach the cascade reader in \`website/test/base-styles.mjs\` the construct, or write the rule in a shape it reads`,
    ]
  }
  const measurePx = capPixels(tokenText(css).get('measure') ?? '', tokenText(css), lengthTokens(css), elementFontPx(css))
  if (Number.isNaN(measurePx)) {
    out.push("the measure this site sets prose to cannot be read from the stylesheet, so 'a readable width' is not a number this check can hold the chat's card to")
  } else if (cap.px > measurePx) {
    out.push(
      `the chat's turns are drawn at ${Number.isFinite(cap.px) ? `${Math.round(cap.px)} px` : 'no cap at all'} (max-width: ${cap.value}` +
        `${cap.selector ? `, from ${JSON.stringify(cap.selector)}` : ''}${cap.query ? ` inside ${cap.query}` : ''}), and a line of this site's prose is set at the measure, ${Math.round(measurePx)} px: the conversation is one card at a readable width rather than a figure stretched across the page (changes spec 2026-10-18, item 3a)`,
    )
  }

  // ---- 4. the speakers are one column ------------------------------------------------------
  const turnColumns = declared('chat-turn', 'grid-template-columns', DESKTOP)
  if (turnColumns === undefined) {
    out.push(
      `no rule gives a turn two columns at ${DESKTOP / 16} rem: the speaker is a column of its own at the left of the line it introduces, and without it every line starts wherever its own name ends (changes spec 2026-10-18, item 3a)`,
    )
  } else {
    const tracks = splitTop(turnColumns, ' ')
    const label = tracks[0] ?? ''
    const content = /^(?:max-content|min-content|auto|fit-content\(.*\)|[\d.]+fr)$/.test(label) || label.includes('fr')
    const labelPx = content ? NaN : toPx(label, tokens)
    const labelSize = toPx(declared('turn-who', 'font-size', DESKTOP), tokens)
    const widest = speakers.reduce((a, b) => (textWidth(b, labelSize) > textWidth(a, labelSize) ? b : a), '')
    const need = textWidth(widest, labelSize) + LABEL_MARGIN
    if (tracks.length !== 2) {
      out.push(
        `a turn is laid out as ${tracks.length} column${tracks.length === 1 ? '' : 's'} (\`${turnColumns}\`), and a turn of the conversation is two: its speaker, and what they said (changes spec 2026-10-18, item 3a)`,
      )
    } else if (content) {
      out.push(
        `the speaker column is \`${label}\`, which a browser sizes per row against that row's own label: \`${speakers[0]}\` and the longest of the labels (${JSON.stringify(widest)}) then give two different columns, and the two lines under them start at two different places — a content-sized column aligns nothing (changes spec 2026-10-18, item 3a)`,
      )
    } else if (Number.isNaN(labelPx) || Number.isNaN(labelSize) || Number.isNaN(need)) {
      out.push(
        `the speaker column's arithmetic cannot be read: \`${label}\` at a label size of \`${declared('turn-who', 'font-size', DESKTOP) ?? '(nothing declared)'}\` is not a length this check can turn into pixels, and a length it cannot read is a length nothing checked (\`NaN\` passes every comparison) — teach \`website/test/base-styles.mjs\` the shape, or write the declaration in one it reads (changes spec 2026-10-18, item 3a)`,
      )
    } else if (labelPx < need) {
      out.push(
        `the speaker column is ${Math.round(labelPx)} px and the widest speaker label in the markup (${JSON.stringify(widest)}, at ${labelSize} px, estimated at ${Math.round(textWidth(widest, labelSize))} px) needs ${Math.round(need)} px with the margin this check asks for: the labels are one column, so the column has to hold the widest of them on one line (changes spec 2026-10-18, item 3a)`,
      )
    }
    for (const klass of ['turn-body']) {
      const column = declared(klass, 'grid-column', DESKTOP)
      if (column === undefined || !/^2(\s*\/\s*-?1)?$/.test(column.trim())) {
        out.push(
          `a .${klass} is not in the second column of its turn (\`grid-column: ${column ?? '(nothing declared)'}\`), so what the speaker said starts under its own label rather than at the edge every line of the conversation shares (changes spec 2026-10-18, item 3a)`,
        )
      }
    }
  }

  // ---- 6. the two speakers are on opposite sides -------------------------------------------
  //
  // The figure is a chat (review round 5), so the reader's turn and the agent's are drawn on
  // opposite sides of the card. The reading is the bubble's own `justify-self`, per speaker:
  // the turn's body is where the alternation is written, and a rule set that leaves both
  // speakers on one side — or stretches the body to the whole column, which is a band rather
  // than a message — is the defect each refusal below names.
  const bodySideAt = (speaker) => {
    // The speaker's own rule: one that reaches a turn of that speaker — the turn's class and
    // its `data-turn` attribute, whose bracket group may sit before or after the scope
    // attribute the build adds — and then the turn's body inside it.
    const token = new RegExp(`data-turn=(?:"|')?${speaker}(?:"|')?(?=[\\]\\s]|$)`)
    const own = rules
      .filter((r) => /\.chat-turn\b/.test(r.selector) && /\.turn-body(?![\w-])/.test(r.selector) && token.test(r.selector))
      .filter((r) => appliesAt(DESKTOP)(r))
      .map((r) => declarations(r)['justify-self'])
      .filter((value) => value !== undefined)
      .pop()
    // The base is the rule that reaches **every** turn's body — the speaker's own rule is a
    // different one, and it is read above rather than here: a fallback that took the last
    // rule matching `.turn-body`, which is the reader's own, would report both speakers on
    // the reader's side and refuse a correct stylesheet. What is left is the initial value:
    // `normal`, which for a grid item means `stretch`.
    const base = rules
      .filter(
        (r) =>
          new RegExp(`\\.turn-body(?![\\w-])`).test(r.selector) &&
          !/data-turn/.test(r.selector) &&
          appliesAt(DESKTOP)(r),
      )
      .map((r) => declarations(r)['justify-self'])
      .filter((value) => value !== undefined)
      .pop()
    return own ?? base ?? 'normal'
  }
  const sides = { you: bodySideAt('you'), agent: bodySideAt('agent') }
  const atEnd = (value) => /^(?:end|right|self-end|flex-end)$/.test(String(value).trim())
  for (const [speaker, side] of Object.entries(sides)) {
    if (/^(?:normal|stretch|self-stretch)$/.test(String(side).trim())) {
      out.push(
        `the ${speaker === 'you' ? "reader's" : "agent's"} turn has no side of its own (\`justify-self: ${side}\` on its body, which for a grid item stretches it to the whole column): a turn's body is a bubble — the shape of what was said, on one side of the card or the other — and a box that fills the column is a band with a message in it (review round 5)`,
      )
    }
  }
  if (atEnd(sides.you) === atEnd(sides.agent)) {
    out.push(
      `the reader's turn and the agent's are drawn on the same side (their bodies' \`justify-self\` is ${JSON.stringify(sides.you)} and ${JSON.stringify(sides.agent)}): the figure is a chat, and a chat alternates — what the reader asked on one side, and what the agent did on the other (review round 5)`,
    )
  }

  // ---- 5. on a phone the label goes above the line ------------------------------------------
  //
  // The label column is a fixed length, and a fixed length that suits a 1056 px card takes a
  // third of a 390 px one. Below the layout's own breakpoint a turn is one column, so the
  // speaker is a line above what they said; this is the end that says the desktop rule is
  // inside a breakpoint rather than in force everywhere.
  const phoneColumns = declared('chat-turn', 'grid-template-columns', PHONE)
  if (phoneColumns !== undefined && splitTop(phoneColumns, ' ').length > 1) {
    out.push(
      `at ${PHONE} px a turn is laid out as \`${phoneColumns}\`, ${splitTop(phoneColumns, ' ').length} columns on a ${PHONE} px screen: the speaker column is a fixed length sized for a card of a thousand pixels, and below the layout's own breakpoint the label goes above the line it introduces (changes spec 2026-10-18, item 3a)`,
    )
  }
  return out
}

// ---- the watch slot reserves no space until the image lands (review round 3, item 3b) ------

/**
 * Every way the `fugaro watch` slot breaks the two rules that make it a slot rather than a
 * placeholder. It is empty until a later task adds the screenshot — that is item 3's own
 * arrangement and PR #622's image — and the review of round 3 found the cost of getting it
 * wrong: about 140 px of blank page between the chat and the command bar under it. So:
 *
 *   - **nothing draws a box around an empty slot.** `:empty { display: none }` is what takes
 *     it out of the page while it holds nothing, and any rule that gives the slot itself a
 *     height, a padding, a border, a background or a margin is the blank rectangle coming
 *     back. Rules about what is *inside* the slot (`> img`) are the other half and are let
 *     through;
 *   - **the image that lands in it is sized already.** A rule for the slot's own `img` is here,
 *     so the later task's change is one `<img>` line in the markup and no CSS that nobody has
 *     looked at;
 *   - **and the image that landed is sized so it scales.** The screenshot is a 1459 x 441
 *     logical capture of a terminal, and the owner's brief for putting it on the page asks for
 *     the page at 1280, 768 and 375 px: a fixed width is the picture running past a phone's
 *     edge — the column is 327 px at 375 — and a fixed height is the same picture squashed into
 *     a box that is not its shape. So the rule that sizes it takes its width from the column it
 *     is in (a percentage of it, or a percentage `max-width`) and lets the height follow the
 *     file's own ratio (`height: auto` or an `aspect-ratio`). This is what "it scales down
 *     without horizontal scroll" is made of, and it is the same reading `test/base-styles.mjs`
 *     takes everywhere else: the built stylesheet's own declarations.
 */
export function chatSlotFailures(css, html) {
  const out = []
  const slot = [...walk(parse(html))].find((el) => el.attrs.get('data-slot') === 'watch-screenshot')
  if (!slot) return out // the markup's slot is `test/home.mjs`'s subject, not this one's
  const own = allRules(css).filter((rule) => /\.chat-shot(?![\w-])/.test(rule.selector))
  const empty = own.find((rule) => rule.selector.includes(':empty'))
  if (empty === undefined || declarations(empty)['display'] !== 'none') {
    out.push(
      "no rule takes the watch screenshot's slot out of the page while it is empty (`.chat-shot:empty { display: none }`): the slot holds nothing until the image lands, and an empty element that still takes space is blank page between the conversation and the command bar (review round 3, item 3b)",
    )
  }
  for (const rule of own) {
    if (rule.selector.includes(':empty')) continue
    if (rule.selector.includes('>')) continue // a rule about the image inside the slot
    const declarations_ = declarations(rule)
    for (const property of ['height', 'min-height', 'aspect-ratio', 'padding', 'border', 'background', 'margin']) {
      const value = declarations_[property]
      if (value === undefined) continue
      out.push(
        `\`${rule.selector}\` gives the watch screenshot's slot a \`${property}: ${value}\`${rule.query ? ` inside ${rule.query}` : ''}, and the slot is empty until the image lands: a slot with a box of its own is blank page in the meantime, which is what the review of round 3 measured at about 140 px (review round 3, item 3b)`,
      )
    }
  }
  const sizes = own.filter((rule) => /(^|[^\w-])img(?![\w-])/.test(rule.selector))
  if (sizes.length === 0) {
    out.push(
      "no rule sizes the image that goes into the watch screenshot's slot (`.chat-shot > img`): the image is a separate task's to add, and the rule that draws it belongs beside the slot so that adding it is one line of markup (review round 3, item 3b)",
    )
    return out
  }
  // The width the picture is laid out at, as the widest declaration any rule of the slot's own
  // makes about it: a percentage of the column is one that tracks the column at every viewport.
  const percents = sizes.flatMap((rule) => {
    const declared = declarations(rule)
    return ['width', 'max-width']
      .map((property) => declared[property])
      .filter((value) => value !== undefined)
  })
  const tracks = percents.find((value) => /^\d+(\.\d+)?%$/.test(value) && Number.parseFloat(value) <= 100)
  if (tracks === undefined) {
    out.push(
      `the rule that sizes the watch screenshot declares ${
        percents.length === 0 ? 'no width at all' : `\`width\`/\`max-width\` of ${percents.map((v) => `\`${v}\``).join(' and ')}`
      }, and the picture is a 1459 px-wide capture of a terminal: at 375 px the column it sits in is 327 px, so a width that is not a percentage of that column is the screenshot running past the phone's edge, which is the horizontal scroll the owner's brief for this asset asks the page not to have (owner spec 2026-10-18, item 5)`,
    )
  }
  // And no rule of the slot's own gives the picture a fixed length at all — the check above
  // reads the declarations there are, and this one reads the ones a breakpoint could add: a
  // `width` in pixels inside a media query overrides the percentage above at exactly the
  // viewports that query answers, which is where the overflow would live.
  for (const rule of sizes) {
    const declared = declarations(rule)
    for (const property of ['width', 'max-width', 'min-width', 'height', 'min-height']) {
      const value = declared[property]
      if (value === undefined || !/^\d+(\.\d+)?(px|rem|em|ch|vh|vw|pt)$/.test(value)) continue
      out.push(
        `\`${rule.selector}\` gives the watch screenshot a \`${property}: ${value}\`${rule.query ? ` inside ${rule.query}` : ''}, and a length in units is a box the picture does not grow out of: the capture is 1459 logical pixels wide in a column that is 327 px at 375, so the picture's own width has to come from the column (a percentage) and its height from the file's ratio (owner spec 2026-10-18, item 5)`,
      )
    }
  }
  const ratios = sizes.flatMap((rule) => {
    const declared = declarations(rule)
    return ['height', 'aspect-ratio']
      .map((property) => declared[property])
      .filter((value) => value !== undefined)
  })
  if (!ratios.some((value) => value === 'auto' || /^\d+(\.\d+)?\s*\/\s*\d+(\.\d+)?$/.test(value))) {
    out.push(
      `the rule that sizes the watch screenshot declares ${ratios.length === 0 ? 'no height at all' : `\`height\`/\`aspect-ratio\` of ${ratios.map((v) => `\`${v}\``).join(' and ')}`}, and the picture's height has to follow the file's own ratio: a fixed height with a width that shrinks with the column is the screenshot squashed out of its shape at every viewport narrower than the one the number was typed at (owner spec 2026-10-18, item 5)`,
    )
  }
  return out
}

/** The step names one built page prints, in the flow's order. */
function stepNames(html) {
  return [...walk(parse(html))]
    .filter((el) => el.name === 'h2' && (el.attrs.get('class') ?? '').split(/\s+/).includes('step-name'))
    .map((el) => textOf(el).replace(/\s+/g, ' ').trim())
}

/**
 * A list on one built page, as `listMaxWidth` reads it: its name, its classes, its id and every
 * attribute it carries. The attributes matter because Astro scopes a component's stylesheet by
 * adding one (`data-astro-cid-…`), and a rule the cascade reader could not answer for is a rule
 * it has to refuse. `what` is the sentence that says where the list is supposed to be, so the
 * assertion names the page rather than the class.
 */
export function listElement(html, klass, what) {
  const list = [...walk(parse(html))].find(
    (el) => (el.name === 'ol' || el.name === 'ul') && (el.attrs.get('class') ?? '').split(/\s+/).includes(klass),
  )
  assert.ok(list, `the page carries no <ol class="${klass}">, so the cap on it cannot be read: ${what}`)
  return {
    name: list.name,
    classes: (list.attrs.get('class') ?? '').split(/\s+/).filter(Boolean),
    id: list.attrs.get('id') ?? null,
    attrs: list.attrs,
  }
}

/** The flow's list element on one built page. */
export function flowListElement(html) {
  return listElement(html, 'flow-steps', 'item 4 puts the flow on the homepage and on How It Works')
}

/** The classes between `main` and the flow's figure on one built page: the gutters it sits in. */
function flowWrappers(html) {
  const figure = [...walk(parse(html))].find((el) => el.name === 'figure' && (el.attrs.get('class') ?? '').split(/\s+/).includes('flow'))
  const out = []
  for (let node = figure?.parent; node && node.name !== 'main'; node = node.parent) {
    out.push(...(node.attrs?.get('class') ?? '').split(/\s+/).filter(Boolean))
  }
  return out
}

test('the four-across flow has room for the step names it prints', () => {
  const failures = []
  let checked = 0
  for (const { rel, html } of builtPages()) {
    const names = stepNames(html)
    if (names.length === 0) continue
    checked += 1
    failures.push(...flowLayoutFailures(pageCss(html), names, flowWrappers(html), flowListElement(html)).map((line) => `${rel}: ${line}`))
    failures.push(...flowBandFailures(pageCss(html)).map((line) => `${rel}: ${line}`))
  }
  assert.ok(checked > 0, 'no built page carries the flow, and item 4 puts it on the homepage and on How It Works')
  assert.deepEqual(failures, [], `the flow cannot render its own step names:\n  ${failures.join('\n  ')}`)
})

test('the flow is a stack, a two-by-two and a row — each in its own band of width', () => {
  const css = pageCss(readFileSync(path.join(DIST, 'index.html'), 'utf8'))
  assert.deepEqual(flowBandFailures(css), [], 'the band rule fails on the built stylesheet, so the cases below are not proving what they name')

  const cases = [
    // The defect the review of round 3 measured, and the one this gate exists for: no
    // two-by-two at all, which leaves the four cards as the tall stack the review counted at
    // 768 px. The blocks that start the two-by-two and the row are both still there — this is
    // the shape whose absence nothing else in the file would notice.
    [
      'a flow with no two-by-two at all',
      css.replace(/@media \(width>=35rem\) and \(width<=71\.98rem\)\{\.flow-steps[^{]*\{[^}]*\}/, ''),
      /at 768 px the flow's four steps are a `column`/,
    ],
    // And the same band one rem early: the stack ends at 35 rem, which is the review's "about
    // 560 px", and a two-by-two that begins at 30 rem gives a 559 px reader it instead.
    [
      'a two-by-two beginning below the phone’s own end',
      css.replace('(width>=35rem) and (width<=71.98rem)', '(width>=30rem) and (width<=71.98rem)'),
      /at 559 px the flow's four steps are a `grid`/,
    ],
    // A band with no width in its query at all: the two-by-two moved out of the widths it is
    // for, so it is a layout for every width — a fourth band nobody chose, and one that takes
    // the phone's own stack away.
    [
      'a two-by-two in a query with no viewport width in it',
      css.replace('(width>=35rem) and (width<=71.98rem)', '(min-resolution: 2dppx)'),
      /a query with no viewport width in it/,
    ],
    // The row itself, starting past the desktop width the review measured.
    // The row's own band starting past the desktop width the review measured, which leaves the
    // three bands with a hole in them: the two-by-two has ended and the row has not begun, so a
    // 1280 px reader gets the phone's stack back.
    [
      'a row that starts after the desktop width',
      css.replace('@media (width>=72rem){.flow-steps', '@media (width>=90rem){.flow-steps'),
      /at 1280 px the flow's four steps are a `column`/,
    ],
    // The phone's own column given a second column of its own: the base rule lays the list
    // out, and laying it out as a grid there is a two-by-two at every width below the band.
    [
      'a phone’s stack given two columns',
      css.replace(
        /\.flow-steps(\[data-astro-cid-[a-z0-9]+\])\{flex-direction:column;max-width:none/,
        '.flow-steps$1{grid-template-columns:1fr 1fr;max-width:none',
      ),
      /at 559 px the flow's four steps are a `grid`/,
    ],
  ]
  for (const [what, planted, expected] of cases) {
    assert.notEqual(planted, css, `${what} was not planted: the case's pattern did not match the built stylesheet`)
    const failures = flowBandFailures(planted)
    assert.ok(failures.some((line) => expected.test(line)), `${what} was not refused (${expected}); the rule said:\n  ${failures.join('\n  ') || '(nothing)'}`)
  }
})

test('the chat is one card holding one conversation, at a readable width', () => {
  const failures = []
  let checked = 0
  for (const { rel, html } of builtPages()) {
    if (!html.includes('class="chat-turn"')) continue
    checked += 1
    failures.push(...chatConversationFailures(pageCss(html), html).map((line) => `${rel}: ${line}`))
    failures.push(...chatSlotFailures(pageCss(html), html).map((line) => `${rel}: ${line}`))
  }
  assert.ok(checked > 0, 'no built page carries the chat, and item 3 puts it on the homepage')
  assert.deepEqual(failures, [], `the chat is not the one conversation card the page asks for:\n  ${failures.join('\n  ')}`)
})

test('every chat-conversation refusal fires on the defect it names', () => {
  const home = readFileSync(path.join(DIST, 'index.html'), 'utf8')
  const css = pageCss(home)

  /** Today's stylesheet with one declaration added to the first rule whose selector matches. */
  const declaring = (pattern, property) => {
    const next = css.replace(pattern, (match) => `${match.slice(0, -1)};${property}}`)
    assert.notEqual(next, css, `this fixture adds \`${property}\` to the first rule matching ${pattern}, and the built stylesheet no longer carries one`)
    return next
  }
  const onChatTurns = (property) => declaring(/\.chat-turns\[data-astro-cid-[a-z0-9]+\]\{[^}]*\}/, property)

  const cases = [
    // The defect the review of round 3 found: the turns are three cards across the page.
    ['the row of cards comes back', onChatTurns('grid-template-columns:repeat(auto-fit,minmax(18rem,1fr))'), /laid out as `repeat\(auto-fit/, home],
    // And the other way of writing the same row: a flex row of the same cards.
    ['the turns are a flex row', onChatTurns('display:flex;flex-direction:row'), /a flex row/, home],
    // And the cap opted out of, which puts the conversation back across the page.
    ['the measure opted out of', onChatTurns('max-width:none'), /a line of this site's prose is set at the measure/, home],
    // A card per turn, which is the same defect drawn with borders.
    [
      'a card per turn',
      declaring(/\.chat-turn\[data-astro-cid-[a-z0-9]+\]\{[^}]*\}/, 'background:var(--surface)'),
      /draws a card of its own/,
      home,
    ],
    // The speaker column sized by the row's own content, which aligns nothing.
    [
      'a content-sized speaker column',
      plant('grid-template-columns:7rem minmax(0,1fr)', 'grid-template-columns:max-content minmax(0,1fr)'),
      /a content-sized column aligns nothing/,
      home,
    ],
    // And a definite column too narrow for the widest label — the mutation-proved end of the
    // arithmetic above: the widest speaker label in the markup no longer fits on one line.
    [
      'a speaker column too narrow for the labels',
      plant('grid-template-columns:7rem minmax(0,1fr)', 'grid-template-columns:2rem minmax(0,1fr)'),
      /the widest speaker label in the markup/,
      home,
    ],
    // A line that starts under its own label rather than at the shared edge. The body is one
    // element, so one rule carries what a speaker said and one refusal names it.
    ['a line outside the second column', plant('{grid-column:2}', '{grid-column:1}'), /is not in the second column/, home],
    // And the two ends of the chat's alternation (review round 5): the reader's bubble moved
    // onto the agent's side, and a body left with no side at all — stretched to the column.
    ['both speakers on one side', plant('justify-self:end', 'justify-self:start'), /drawn on the same side/, home],
    ['a turn with no side of its own', plant('justify-self:start', 'justify-self:stretch'), /stretches it to the whole column/, home],
    // The desktop column in force on a phone, where it would take a third of the screen.
    [
      'the two columns in force at 390 px',
      declaring(/\.chat-turn\[data-astro-cid-[a-z0-9]+\]\{[^}]*\}/, 'grid-template-columns:7rem minmax(0,1fr)'),
      /below the layout's own breakpoint/,
      home,
    ],
    // And the page's own side: fewer than the three turns item 3's figure draws.
    ['a page whose chat is not the figure’s three turns', css, /prints 2 chat turn\(s\)/, home.replace('class="chat-turn"', 'class="chat-turn-solo"')],
  ]
  for (const [what, stylesheet, expected, html, count = 1] of cases) {
    assert.deepEqual(
      chatConversationFailures(stylesheet, html).map((line) => (expected.test(line) ? 'fired' : line)),
      Array(count).fill('fired'),
      `${what}: the refusal did not fire, and this is what it said instead`,
    )
  }

  // The slot's two rules, each planted: the rule that takes an empty slot out of the page, and
  // the rule that sizes the image a later task adds.
  const nothingPainted = css.replace(
    /\.chat-shot\[data-astro-cid-[a-z0-9]+\]:empty\{display:none\}/,
    '.chat-shot[data-astro-cid-6fbtmsuw]:empty{display:block}',
  )
  assert.notEqual(nothingPainted, css, "this fixture turns the slot's `:empty` rule into one that still draws it, and the built stylesheet no longer carries that rule")
  const noImageRule = css.replace(/\.chat-shot\[data-astro-cid-[a-z0-9]+\]>img[^{]*\{[^}]*\}/, '')
  assert.notEqual(noImageRule, css, "this fixture removes the rule that sizes the image in the slot, and the built stylesheet no longer carries one")
  // And the picture's own sizing, each planted the way the defect reads on the page: a width
  // in pixels is the screenshot wider than the phone's column, and a height in pixels is the
  // same screenshot squashed into a box that is not its shape.
  const fixedWidth = css.replace(/(\.chat-shot\[data-astro-cid-[a-z0-9]+\]>img\[[^\]]*\]\{)width:100%/, '$1width:1459px')
  const fixedHeight = css.replace(/(\.chat-shot\[data-astro-cid-[a-z0-9]+\]>img\[[^\]]*\]\{width:100%;)height:auto/, '$1height:441px')
  assert.notEqual(fixedWidth, css, "this fixture fixes the watch screenshot's width in pixels, and the built stylesheet no longer sizes it with a percentage")
  assert.notEqual(fixedHeight, css, "this fixture fixes the watch screenshot's height in pixels, and the built stylesheet no longer lets it follow the file's ratio")
  // The same defect one breakpoint down: the base rule still tracks the column, and a media
  // query answers a phone with a fixed width instead — the shape a "just for small screens"
  // override takes, and the one a check that read only the declarations in front of it would
  // miss.
  const imgSelector = /(\.chat-shot\[data-astro-cid-[a-z0-9]+\]>img\[[^\]]*\])/.exec(css)?.[1]
  assert.ok(imgSelector, "this fixture needs the rule that sizes the image in the watch slot, and the built stylesheet no longer carries one to read its selector from")
  const phoneWidth = `${css}\n@media (max-width:40rem){${imgSelector}{width:900px}}`
  const slotCases = [
    ['the slot left in the page while it is empty', nothingPainted, /takes the watch screenshot's slot out of the page/],
    ['the screenshot fixed in width inside a breakpoint', phoneWidth, /a length in units is a box the picture does not grow out of/],
    ['a box drawn around the empty slot', `${css}\n.chat-shot{min-height:140px}`, /gives the watch screenshot's slot a `min-height/],
    ['the image rule removed with the slot still empty', noImageRule, /no rule sizes the image that goes into the watch screenshot's slot/],
    ['the screenshot given a fixed width', fixedWidth, /the picture is a 1459 px-wide capture of a terminal/],
    ['the screenshot given a fixed height', fixedHeight, /the picture's height has to follow the file's own ratio/],
  ]
  for (const [what, stylesheet, expected] of slotCases) {
    const failures = chatSlotFailures(stylesheet, home)
    assert.ok(
      failures.some((line) => expected.test(line)),
      `${what}: the refusal did not fire, and this is what it said instead:\n  ${failures.join('\n  ') || '(nothing)'}`,
    )
  }
})

test('every flow-layout refusal fires on the defect it names', () => {
  const home = readFileSync(path.join(DIST, 'index.html'), 'utf8')
  const names = stepNames(home)
  const wrappers = flowWrappers(home)
  const element = flowListElement(home)
  assert.ok(names.length === 4, `the homepage prints ${names.length} step names, and the flow is the spec's four steps`)
  const cases = [
    // The defect this reader was written for, and the one the picture shipped with: the
    // measure — `base.css`'s `:where(p, ul, ol, dl, blockquote, figcaption) { max-width:
    // var(--measure) }` — reaching the flow's own list, because the list is an `<ol>`. A card
    // is sized against the page's column and the browser gives the row 578 px, so the four
    // cards and their arrows are drawn in a box narrower than they need.
    [
      'the measure’s cap on the flow’s list',
      plant('flex-direction:column;max-width:none', 'flex-direction:column'),
      /is capped at \d+ px/,
    ],
    // A length the reader cannot turn into pixels is refused rather than carried into the sums:
    // `clamp()` is a font size a browser resolves and this check cannot, and reading it as
    // `NaN` would make every comparison below pass without measuring anything.
    [
      'a step-name font size this reader cannot turn into pixels',
      // Anchored at the class, because `font-size: var(--font-size-lg)` with the same weight
      // beside it is written by four of the site's rules and only one of them is the step name;
      // `TODAY().replace` rather than `plant` so the fixture names the rule and not a hash the
      // next build of the component would change.
      TODAY().replace(
        /(\.step-name\[data-astro-cid-[a-z0-9]+\]\{[^}]*?)font-size:var\(--font-size-lg\)/,
        '$1font-size:clamp(1rem,2vw,1.5rem)',
      ),
      /is not a length this check can turn into pixels/,
    ],
    // A cap written in a shape the reader cannot evaluate is refused rather than skipped: a
    // selector it cannot read may be the one that squeezes the list.
    [
      'a rule that reaches the list in a shape this reader cannot read',
      TODAY().replace(/\.flow-steps(\[data-astro-cid-)/, '.flow-steps:nth-child(2)$1'),
      /reached by a rule this check cannot read/,
    ],
    // The defect as it shipped: the name on one line, in a card a quarter of the row wide.
    [
      'a step name held on one line',
      plant('color:var(--text);overflow-wrap:anywhere;margin:0}', 'color:var(--text);white-space:nowrap;overflow-wrap:anywhere;margin:0}'),
      /held on one line/,
    ],
    // And the layout that made it overflow: the four-across row starting at 48 rem, where a
    // card is a quarter of a column of 768 px and cannot print its longest name on one line.
    [
      'the four-across row starting before a card can hold a name',
      TODAY().replace(/(width>=72rem)\)\{\.flow-steps/, '(width>=48rem){.flow-steps'),
      /does not fit on one line/,
    ],
    // The name's own size, which is the other half of that arithmetic: the step up the type
    // scale this branch took the name *down* from, put back. `Ephemeral cloud job` at 24 px is
    // wider than a quarter of the row, so this is the defect the review measured at 1280 px —
    // and the case is the mutation the name's size is proved by.
    [
      'a step name one step up the type scale, which no longer fits the row',
      TODAY().replace(
        /(\.step-name\[data-astro-cid-[a-z0-9]+\]\{[^}]*?)font-size:var\(--font-size-lg\)/,
        '$1font-size:var(--font-size-xl)',
      ),
      /does not fit on one line/,
    ],
    // The number is `flex: none` *beside* the name, which is the phone's own head: from 35 rem
    // up the head is a column and the number costs the name nothing. Planting the row's head
    // back as a row is the box the number takes out of the name.
    [
      'a step number that takes the name’s width',
      plant('flex-direction:column;align-items:flex-start}', 'align-items:flex-start}'),
      /does not fit on one line/,
    ],
    // The ragged row itself, which is the second defect the review measured: the steps aligned
    // to their centre, so each card takes its own content's height instead of the row's.
    [
      'a row whose steps are centred rather than stretched',
      plant('align-items:stretch;gap:var(--space-sm);flex-direction:row', 'align-items:center;gap:var(--space-sm);flex-direction:row'),
      /aligns its items to `center`/,
    ],
    // And the other half of it: the cards stretch to their step, so a card whose own content is
    // spread down it is the same ragged look with the row's height in every card.
    [
      'a card that spreads its content down the row’s height',
      plant('align-content:start;gap:var(--space-xs)', 'align-content:space-between;gap:var(--space-xs)'),
      /packs its content with `space-between`/,
    ],
    // The card's own growth factor, which is the third thing that makes the four one height and
    // the one the two `align-items` readings cannot see: the step is a *column*-flex box, so
    // the axis the cards share the row's height along is its main axis, and a card that does not
    // grow takes its own content's height inside a step that is the tallest of the four. `none`
    // is the shorthand that says so, and the reader has to read it: a browser resolves it to
    // `0 0 auto`, which no first-component-as-a-number reader sees.
    // The anchor is the card's own run of declarations rather than `flex:auto` alone, which a
    // browser reads as `1 1 auto`: the minified stylesheet writes the same shorthand for the
    // command block's `<pre>`, and a fixture that replaced the first occurrence would edit that
    // one and leave this case asserting a page it never changed.
    [
      'a card that does not grow to its step’s height',
      plant('background:var(--surface);flex:auto;display:grid', 'background:var(--surface);flex:none;display:grid'),
      /the step card does not grow/,
    ],
    // And the same card padded by a four-value shorthand, whose room at the sides is the *left*
    // value: `top right bottom left` is what the shorthand means, and the right value is `0`,
    // so a reader that took the second component would call this a card with no inline padding
    // and pass a name that has 7 rem of it on one side.
    [
      'a card whose four-value padding is read from the wrong side',
      plant('padding:var(--space-md) var(--space-2xs)', 'padding:var(--space-md) 0 var(--space-md) var(--space-3xl)'),
      /cannot render its own step names/,
    ],
    // The two-by-two's own arithmetic: its cards are half the column, and padding wide enough
    // takes the longest word out of one.
    [
      'a two-by-two whose cards are padded past their own names',
      plant('padding:var(--space-md) var(--space-sm)', 'padding:var(--space-md) var(--space-3xl)'),
      /a card there is \d+ px wide/,
    ],
    // The arrows, which are the rest of the same picture: the one that shipped wrong turns the
    // drawn-down arrow a quarter turn clockwise, which points it back along the row. The first
    // occurrence is the two-by-two's turn and the last is the row's, so each is a case of its
    // own — and a check that read one arrow for the whole page would pass both.
    [
      'the two-by-two’s row arrows turned the wrong way',
      plant('transform:rotate(-90deg)', 'transform:rotate(90deg)'),
      /points left at 35 rem/,
    ],
    [
      'the row’s arrows turned the wrong way',
      plant('transform:rotate(-90deg)', 'transform:rotate(90deg)', 'last'),
      /points left at 72 rem/,
    ],
    [
      'the two-by-two’s row arrows never turned at all',
      plant(';transform:rotate(-90deg)', ''),
      /points down at 35 rem/,
    ],
    [
      'a row whose arrows were never turned at all',
      plant(';transform:rotate(-90deg)', '', 'last'),
      /points down at 72 rem/,
    ],
    // The one arrow the two-by-two draws downwards: left alone it is right (the drawing points
    // down), and turned with the other two it points along the row instead of down the side.
    [
      'the two-by-two’s downward arrow turned along its row',
      plant(
        'margin:calc(var(--space-lg) / 2 - var(--space-2xs)) 0 0 calc(-1 * var(--space-2xs));top:100%;left:50%}',
        'transform:rotate(-90deg);margin:calc(var(--space-lg) / 2 - var(--space-2xs)) 0 0 calc(-1 * var(--space-2xs));top:100%;left:50%}',
      ),
      /points right at 35 rem/,
    ],
    [
      'an arrow turned sideways in the column',
      plant('{width:1.5rem;height:2rem;margin:var(--space-2xs) auto;display:block}', '{transform:rotate(-90deg);width:1.5rem;height:2rem;margin:var(--space-2xs) auto;display:block}'),
      /points right below the flow’s first breakpoint/,
    ],
    // **The defect of review round 4, and the reason this gate reads the box as well as the
    // angle:** the two-by-two drawn `1 2` over `3 4`, which is what the stylesheet says with
    // the two placements taken out and the grid left to its own row-major order. The wrap
    // arrow is still below step 2 and still points down — and step 3 is not below step 2 any
    // more, it is in the next row and the other column, so the arrow lands on step 4. Every
    // check in this file passed on that picture for as long as it was shipped.
    [
      'the two-by-two left in its own row-major order, with the wrap arrow below step 2',
      TODAY().replace(/\{grid-area:2\/2\}/, '{}').replace(/\{grid-area:2\/1\}/, '{}'),
      /step 2 and step 3, its successor, are not beside each other/,
    ],
    // The other half of the same reading: the steps beside each other and the arrow on the
    // wrong side of its card. Step 3's arrow moved to its right, where step 4 is not.
    [
      'a wrap arrow drawn on the far side of the step it leaves',
      TODAY().replace(
        /\.flow-step\[data-astro-cid-[a-z0-9]+\]:nth-child\(3\) \.step-arrow\[data-astro-cid-[a-z0-9]+\]\{[^}]*\}/,
        '.flow-step:nth-child(3) .step-arrow{top:50%;left:100%;margin:calc(-1 * var(--space-2xs)) 0 0 calc(var(--space-md) / 2 - var(--space-2xs));transform:rotate(-90deg)}',
      ),
      /leaves the right of its card/,
    ],
    // And a box this reader cannot place against the card at all: step 2's arrow moved to the
    // middle of its card, where it is over the card rather than in a gap beside it.
    [
      'an arrow centred over its own card',
      TODAY().replace(
        /(\.flow-step\[data-astro-cid-[a-z0-9]+\]:nth-child\(2\) \.step-arrow\[data-astro-cid-[a-z0-9]+\]\{[^}]*?)top:100%/,
        '$1top:50%',
      ),
      /is drawn in a box this check cannot place against its card/,
    ],
    // And a placement the cell reader cannot evaluate: a `span` is a cell this check would have
    // to guess, and a guessed cell is every check below it guessed too.
    [
      'a step placed with a span this reader cannot count',
      TODAY().replace('{grid-area:2/2}', '{grid-area:2/span 1}'),
      /the cells the four steps occupy .* cannot be read/,
    ],
  ]
  for (const [what, css, expected] of cases) {
    const failures = [...flowLayoutFailures(css, names, wrappers, element), ...arrowDirectionFailures(css, home)]
    assert.ok(failures.some((f) => expected.test(f)), `${what} was not refused (${expected}); the rule said:\n  ${failures.join('\n  ') || '(nothing)'}`)
  }
  assert.deepEqual(flowLayoutFailures(TODAY(), names, wrappers, element), [], 'the flow-layout rule fails on the built stylesheet, so the cases above are not proving what they name')
  assert.deepEqual(arrowDirectionFailures(TODAY(), home), [], 'the arrow-direction rule fails on the built page, so the cases above are not proving what they name')
})

// ---- the arrows point the way the run travels (changes spec item 4) ----------------------

/**
 * The direction the flow's arrow is drawn in, as a vector in the SVG's own coordinates (y
 * grows downwards, as it does in the box a path is drawn in): the **shaft**, the straight line
 * down the middle of the picture. The shaft rather than the head, because a head is a chevron
 * — two strokes meeting at a point — and a rotation turns the whole picture together, so the
 * shaft's own direction is the thing a rotation swings. `null` rather than a guess when the
 * markup has no arrow or no straight shaft: this check's whole subject is the arrow's
 * direction, so a picture it cannot read is a picture it has to refuse.
 */
function arrowDrawnVector(html) {
  const svg = [...walk(parse(html))].find((el) => el.name === 'svg' && hasClass(el, 'step-arrow'))
  return svg ? arrowShaft(svg) : null
}

/** The drawn direction of one arrow `<svg>`: the same reading, for the step that carries it. */
function arrowShaft(svg) {
  for (const path of walk(svg)) {
    const d = (path.attrs.get('d') ?? '').replace(/\s+/g, ' ').trim()
    const shaft = /^M\s*(-?[\d.]+)\s+(-?[\d.]+)\s+V\s*(-?[\d.]+)$/.exec(d)
    if (shaft) return { x: 0, y: Math.sign(Number(shaft[3]) - Number(shaft[2])) }
  }
  return null
}

/** Whether an element carries a class, by the space-separated list its `class` attribute holds. */
function hasClass(el, name) {
  return (el.attrs.get('class') ?? '').split(/\s+/).includes(name)
}

/**
 * The flow's steps on one built page, each with the arrow it carries: `step` is its place in
 * the run, `drawn` whether the step has an arrow at all (the last one has none — it is where
 * the run ends), and `vector` the direction that arrow is drawn in, or `null` when its shaft
 * is not a straight `M … V …` line this check can read. Read per step rather than once for the
 * page, because the three layouts address the arrows separately: the two-by-two turns two of
 * them along its rows and leaves the second pointing down.
 */
function flowArrows(html) {
  const steps = [...walk(parse(html))].filter((el) => el.name === 'li' && hasClass(el, 'flow-step'))
  return steps.map((step, index) => {
    const svg = [...walk(step)].find((el) => el.name === 'svg' && hasClass(el, 'step-arrow'))
    return { step: index + 1, drawn: svg !== undefined, vector: svg === undefined ? null : arrowShaft(svg) }
  })
}

/** A vector turned by a CSS `rotate()` angle. Positive is clockwise, on a y-downs axis. */
function turn(vector, degrees) {
  const radians = (degrees * Math.PI) / 180
  return {
    x: vector.x * Math.cos(radians) - vector.y * Math.sin(radians),
    y: vector.x * Math.sin(radians) + vector.y * Math.cos(radians),
  }
}

/** Which way a vector points, by its larger component. */
function points(vector) {
  if (Math.abs(vector.x) >= Math.abs(vector.y)) return vector.x >= 0 ? 'right' : 'left'
  return vector.y >= 0 ? 'down' : 'up'
}

/**
 * Which cell each step occupies in one of the flow's three shapes, in the run's order, as
 * `[{ column, row }, …]` with 1-based numbers — or `{ unreadable }` when the stylesheet
 * states something this reader cannot turn into a cell.
 *
 * The phone's column and the four-across row are not read out of the stylesheet: they are the
 * run's order drawn one way or the other, so the cells are `1, 2, 3, 4` down one column or
 * across one row. **The two-by-two is read**, because that is where the run wraps and where
 * the drawing and the DOM order can disagree: the grid's own `grid-template-columns` gives the
 * number of columns, each step's `grid-area`/`grid-row`/`grid-column` (whatever the minifier
 * left of them) gives the cells the stylesheet places by hand, and the steps it does not place
 * are auto-placed the way a browser does it — the first free cell in row-major order, in the
 * run's order. A placement in a shape this reader cannot evaluate (`span 2`, a named line, a
 * negative index, half a placement) is refused rather than guessed: a cell this reader gets
 * wrong is a cell every check below gets wrong with it.
 */
export function flowCells(css, shape, width, steps = 4) {
  const numbers = Array.from({ length: steps }, (_, i) => i + 1)
  if (shape === 'column') return numbers.map((n) => ({ column: 1, row: n }))
  if (shape === 'row') return numbers.map((n) => ({ column: n, row: 1 }))

  const rules = allRules(css)
  const list = rules.find(
    (r) => /\.flow-steps(?![\w-])/.test(r.selector) && declarations(r)['grid-template-columns'] !== undefined,
  )
  if (list === undefined) return { unreadable: 'no rule states the two-by-two’s `grid-template-columns`' }
  const tracks = declarations(list)['grid-template-columns'].trim().split(/\s+/)
  if (tracks.length < 2 || tracks.some((track) => !/^[\d.]+(?:fr|rem|px)$/.test(track))) {
    return { unreadable: `the two-by-two’s tracks, \`${declarations(list)['grid-template-columns']}\`, are not a list of lengths this check can count` }
  }

  /** The declarations in force on step `n`'s own `<li>` at the width: the base rule, then the step's own. */
  const declsOf = (n) => {
    const out = {}
    for (const rule of rules) {
      if (!appliesAt(width)(rule)) continue
      const nth = /\.flow-step(?![\w-])[^{]*:nth-child\(\s*(\d+)\s*\)/.exec(rule.selector)
      const base = /\.flow-step(?![\w-])(?![^{]*:nth-child)/.test(rule.selector)
      if (nth ? Number(nth[1]) !== n : !base) continue
      Object.assign(out, declarations(rule))
    }
    return out
  }

  /** A step's stated cell, `null` when it states none, or `{ unreadable }` when it is a shape this reader cannot read. */
  const stated = (decls) => {
    const area = decls['grid-area']
    const row = decls['grid-row']
    const column = decls['grid-column']
    if (area !== undefined) {
      const parts = area.split('/').map((part) => part.trim())
      if (parts.length === 2 && parts.every((part) => /^\d+$/.test(part))) {
        return { row: Number(parts[0]), column: Number(parts[1]) }
      }
      return { unreadable: `\`grid-area: ${area}\` on a step` }
    }
    if (row === undefined && column === undefined) return null
    if (!/^\d+$/.test((row ?? '').trim()) || !/^\d+$/.test((column ?? '').trim())) {
      return { unreadable: `\`grid-row: ${row ?? '(nothing)'}; grid-column: ${column ?? '(nothing)'}\` on a step` }
    }
    return { row: Number(row), column: Number(column) }
  }

  const cells = {}
  const taken = new Set()
  for (const n of numbers) {
    const at = stated(declsOf(n))
    if (at !== null && at.unreadable !== undefined) return at
    if (at === null) continue
    cells[n] = at
    taken.add(`${at.row}/${at.column}`)
  }
  // The browser's own auto-placement, for the steps the stylesheet does not place: the first
  // free cell, scanning row by row and left to right, in the run's order.
  for (const n of numbers) {
    if (cells[n] !== undefined) continue
    let row = 1
    let column = 1
    while (taken.has(`${row}/${column}`)) {
      column += 1
      if (column > tracks.length) {
        column = 1
        row += 1
      }
    }
    cells[n] = { row, column }
    taken.add(`${row}/${column}`)
  }
  return numbers.map((n) => cells[n])
}

/**
 * Which way step `b` sits from step `a`, as the **side of `a`'s card** it is on: `'right'`,
 * `'left'`, `'bottom'` or `'top'`, and `null` when the two are not neighbours at all — a step
 * in the next row and another column, which no arrow drawn out of one of `a`'s four sides can
 * point at. The sides are the card's rather than the compass's, because that is the thing the
 * stylesheet writes: an arrow is drawn at a `left`, a `top` or in the flow below its card.
 * `SIDE_OF_ARROW` below is the same four, in the words an arrow's own direction is read in.
 */
function towards(a, b) {
  if (a.row === b.row && b.column === a.column + 1) return 'right'
  if (a.row === b.row && b.column === a.column - 1) return 'left'
  if (a.column === b.column && b.row === a.row + 1) return 'bottom'
  if (a.column === b.column && b.row === a.row - 1) return 'top'
  return null
}

/** The side of a card a step sits on, in the words an arrow's direction is read in. */
const SIDE_OF_ARROW = { right: 'right', left: 'left', bottom: 'down', top: 'up' }

/** How a step that sits on one side of another is described, for the refusals below. */
const WHERE_NEXT = { right: 'to its right', left: 'to its left', bottom: 'below it', top: 'above it' }

/**
 * Every way the flow's arrows fail to point at the step after theirs, for one built page: the
 * direction is read for **each step** and at **each of the three layouts**, because the arrows
 * are addressed per step and the run travels differently in each shape.
 *
 *   - **The phone's column**: one step under the next, so every arrow points down. A sideways
 *     arrow in a column draws an order the list does not have.
 *   - **The two-by-two**: the run wraps, so the arrows follow the wrap. Where the next step is
 *     is not a rule of this file but a reading of the grid — `flowCells` — so a two-by-two that
 *     puts step 3 beside step 2 and step 4 under it (or any other arrangement) is checked
 *     against its own placements rather than against `1 2 / 3 4`.
 *   - **The row**: the run travels left to right and each arrow points right. This is the one
 *     that shipped wrong: the arrow is drawn pointing down, `rotate(90deg)` is clockwise, and
 *     the arrow between each pair therefore pointed back at the step before it — four steps
 *     drawn in reverse by the one mark that says which way the run goes. A positive angle is
 *     clockwise on the axis the picture is drawn on, so the turn that points right is negative
 *     (`rotate(-90deg)`, or `270deg` written the long way round).
 *
 * **Two things are read for each arrow, and the second is the one that was missing.** The
 * *direction* it points is the markup's own shaft turned by the rotation in force; the *box it
 * is drawn in* is where its own `top`/`left`/margins put it, which is what says which of its
 * card's four sides the arrow leaves from. Both have to agree with the side the next step is
 * on. Reading only the direction is what let the two-by-two's wrap arrow ship drawn below step
 * 2 — pointing down, and down was what the run does next — while step 3 sat in the *other*
 * column: the arrow landed on step 4, the picture read `1 -> 2 -> 4`, and every check here
 * passed, because an angle alone cannot tell the two cells apart (review round 4).
 *
 * The width each layout is read at comes out of the stylesheet — the row's own breakpoint and
 * the two-by-two's — rather than out of this file, so a layout that moves its breakpoint is
 * read where it now starts. The rotation and the box are the last rules **that reach that
 * step's arrow** among those in force, which is how a browser reads them. An arrow whose
 * `transform` is neither `none` nor a plain `rotate(<angle>)`, and one whose box is not on a
 * side of its card this reader can name, are refused rather than passed: both would be a guess,
 * and which step the arrow points at is the subject.
 */
export function arrowDirectionFailures(css, html) {
  const out = []
  const rules = allRules(css)
  const arrows = flowArrows(html)
  if (arrows.every(({ vector }) => vector === null)) {
    out.push(
      "the flow draws no arrow this check can read (an SVG classed `step-arrow` with a straight `M … V …` shaft), and the arrow between two steps is what says which way the run travels (changes spec 2026-10-18, item 4)",
    )
    return out
  }

  const arrowRules = rules.filter((r) => /\.step-arrow(?![\w-])/.test(r.selector))

  /**
   * Whether one of those rules reaches the arrow of step `n`. The layouts address single
   * arrows — `:nth-child(2)` is the two-by-two's arrow down its right-hand side, the odd
   * indices its two along the rows — so a rule that says so is about that arrow and not about
   * the four. A selector with no `:nth-child()` in it reaches every step; one whose argument
   * this reader cannot evaluate (`2n+1`, say) is read the same way, because the shapes the flow
   * writes are `odd`, `even` and a single number, and a rule about every arrow is the base rule
   * that sets its box.
   */
  const reachesStep = (selector, step) => {
    const nth = /:nth-child\(\s*([^)]*)\)/.exec(selector)
    if (!nth) return true
    const arg = nth[1].trim()
    if (arg === 'odd') return step % 2 === 1
    if (arg === 'even') return step % 2 === 0
    return /^\d+$/.test(arg) ? Number(arg) === step : true
  }

  /** Every declaration in force on step `n`'s arrow at a width, the last rule that states one winning. */
  const declsAt = (width, step) => {
    const out = {}
    for (const rule of arrowRules) {
      if (!appliesAt(width)(rule)) continue
      if (!reachesStep(rule.selector, step)) continue
      Object.assign(out, declarations(rule))
    }
    return out
  }

  /** The `transform` in force on step `n`'s arrow at a width, in degrees: `null` when it is not a rotation. */
  const rotationAt = (width, step) => {
    const value = (declsAt(width, step)['transform'] ?? 'none').trim()
    if (value === 'none' || value === '') return 0
    const rotate = /^rotate\(\s*(-?[\d.]+)deg\s*\)$/.exec(value)
    return rotate ? Number(rotate[1]) : null
  }

  /**
   * Which side of its own card step `n`'s arrow leaves from at a width: the box the
   * stylesheet draws, read rather than the angle. An arrow left in the flow sits under its
   * card (the phone's column, and what the base rule gives every layout); one positioned
   * absolutely is on the side its own `top`/`left` and margin put it, and `null` when the
   * combination is one this reader cannot name — the box would be inside the card or centred
   * over it, and which neighbour it points at would be a guess.
   */
  const sideAt = (width, step) => {
    const decls = declsAt(width, step)
    if (decls['position'] !== 'absolute') return 'bottom'
    const left = (decls['left'] ?? '').trim()
    const top = (decls['top'] ?? '').trim()
    // The box's near edge against the card's own edge is what makes the side: `left: 100%` is
    // the card's right edge, `top: 100%` its bottom, and `left: 0` with a negative margin is
    // the card's left edge with the box pushed out into the gap beside it.
    if (left === '100%') return 'right'
    if (top === '100%') return 'bottom'
    const negative = (value) => /^-\s*[\d.]/.test(value) || /^calc\(\s*-/.test(value)
    if (left === '0' || left === '0px' || negative(left)) {
      const marginLeft = (decls['margin-left'] ?? shorthandParts(decls['margin'] ?? '')[3] ?? '').trim()
      return negative(marginLeft) || negative(left) ? 'left' : null
    }
    return null
  }

  // The layouts the arrows are read at, and the shape each one draws the four steps in. The
  // column is below the flow's first breakpoint, and it is hard-coded the way it always was: it
  // is what the flow is with neither of the other two blocks in force, so there is no rule to
  // read a start from — the two-by-two is the block that ends it.
  const layouts = flowLayouts(css)
  const bands = [
    { width: 390, shape: 'column', where: 'below the flow’s first breakpoint, where the four steps are a column' },
  ]
  if (layouts.grid !== null && layouts.grid.from !== null) {
    bands.push({ width: layouts.grid.from, shape: 'grid', where: `at ${layouts.grid.from / 16} rem, where the four steps are a two-by-two` })
  }
  if (layouts.row !== null && layouts.row.from !== null) {
    for (const width of [layouts.row.from, 80 * 16]) {
      bands.push({ width, shape: 'row', where: `at ${width / 16} rem, where the four steps are a row` })
    }
  }
  if (layouts.row === null) return out // `flowLayoutFailures` names the missing row; there is no row to read here

  for (const band of bands) {
    // Where each step is in this shape, which is what "the next step" means here: the run's
    // order is the DOM's, and the shape is the stylesheet's, and the two can disagree — the
    // two-by-two is where they do.
    const cells = flowCells(css, band.shape, band.width, arrows.length)
    if (cells.unreadable !== undefined) {
      out.push(
        `the cells the four steps occupy ${band.where} cannot be read — ${cells.unreadable} — and which step an arrow points at is a cell this check has to know (changes spec 2026-10-18, item 4; review round 4)`,
      )
      continue
    }

    for (const { step, drawn, vector } of arrows) {
      // A step with no arrow is the flow's own shape — the last step is where the run ends —
      // and the count of arrows is `website/test/home.mjs`'s check rather than this one's.
      if (!drawn) continue
      if (vector === null) {
        out.push(
          `the arrow out of step ${step} is not a shaft this check can read (a straight \`M … V …\` line), so the direction it points is a guess (changes spec 2026-10-18, item 4)`,
        )
        continue
      }
      const expected = towards(cells[step - 1], cells[step])
      const next = step + 1
      if (expected === null) {
        const at = (cell) => `row ${cell.row}, column ${cell.column}`
        out.push(
          `step ${step} and step ${next}, its successor, are not beside each other ${band.where} (step ${step} is at ${at(cells[step - 1])}, step ${next} at ${at(cells[step])}): the arrow out of step ${step} leaves one of its card's sides, and the step it lands on is whichever cell is on that side — with the two-by-two drawn \`1 2\` over \`3 4\` the arrow below step 2 lands on step 4 and the picture reads \`1 -> 2 -> 4\`, with step 3 skipped and step 4 carrying two arrows (changes spec 2026-10-18, item 4; review round 4)`,
        )
        continue
      }
      const rotation = rotationAt(band.width, step)
      if (rotation === null) {
        out.push(
          `the arrow out of step ${step}'s transform, ${band.where}, is not a rotation this check can read, so the direction it leaves the arrow pointing is a guess (changes spec 2026-10-18, item 4)`,
        )
        continue
      }
      const side = sideAt(band.width, step)
      if (side === null) {
        out.push(
          `the arrow out of step ${step} is drawn in a box this check cannot place against its card ${band.where}, so the side it leaves from — and the step it lands on — is a guess (changes spec 2026-10-18, item 4; review round 4)`,
        )
        continue
      }
      if (side !== expected) {
        out.push(
          `the arrow out of step ${step} leaves the ${side} of its card ${band.where}, and step ${next} is ${WHERE_NEXT[expected]}: the arrow is drawn in the gap between two steps, so an arrow on the wrong side points at a step that is not the next one (changes spec 2026-10-18, item 4; review round 4)`,
        )
      }
      const way = points(turn(vector, rotation))
      const travels = SIDE_OF_ARROW[expected]
      if (way === travels) continue
      const how = rotation === 0 ? 'no rotation at all' : `transform: rotate(${rotation}deg), which turns the arrow the markup draws pointing down ${rotation > 0 ? 'clockwise' : 'anticlockwise'}`
      out.push(
        `the arrow out of step ${step} points ${way} ${band.where} (${how}), and there the run travels ${travels}: item 4's run goes left to right along a row and down a column, and an arrow drawn the other way reads the four steps in the opposite order (changes spec 2026-10-18, item 4)`,
      )
    }
  }
  return out
}

test('the flow’s arrows point the way the run travels', () => {
  const failures = []
  let checked = 0
  for (const { rel, html } of builtPages()) {
    if (arrowDrawnVector(html) === null) continue
    checked += 1
    failures.push(...arrowDirectionFailures(pageCss(html), html).map((line) => `${rel}: ${line}`))
  }
  assert.ok(checked > 0, 'no built page carries the flow’s arrow, and item 4 draws one between each pair of the four steps')
  assert.deepEqual(failures, [], `the flow’s arrows do not point the way the run travels:\n  ${failures.join('\n  ')}`)
})

// ---- a copy control sits at the right of the block it belongs to (base.css) --------------

/**
 * Every `[data-copy]` block on one built page that holds both a control and a command, as
 * `{ klass, display, direction, atStart }`: the blocks "where the control is drawn" is a
 * question at all. A block carrying one of the two — or a command inside its own control —
 * is `test/copy.mjs`'s subject, not this one, and is skipped.
 *
 * `atStart` is whether the control ends up first along the row's main axis — from `order`
 * where the two differ, and from the markup where they are equal, which is the cascade a
 * browser applies. The classes are read off the built page and the declarations off the built
 * stylesheet, so the check follows the block rather than a list of block names kept here.
 */
export function copyBlocks(css, html, width = 80 * 16) {
  const rules = allRules(css)
  const classesOf = (el) => (el.attrs.get('class') ?? '').split(/\s+/).filter(Boolean)

  /** The declarations in force for an element with these classes at a width, last one winning. */
  const effective = (classes) => {
    const out = {}
    for (const rule of rules) {
      if (!appliesAt(width)(rule)) continue
      const selects = rule.selector.split(',').map((selector) => selector.trim())
      if (!selects.some((selector) => classes.some((klass) => new RegExp(`(^|[\\s>+~])\\.${klass}(?![\\w-])`).test(selector)))) continue
      Object.assign(out, declarations(rule))
    }
    return out
  }

  const blocks = []
  for (const block of [...walk(parse(html))].filter((el) => el.attrs.has('data-copy'))) {
    const control = [...walk(block)].find((el) => classesOf(el).includes('command-controls'))
    const insideControl = (el) => {
      for (let node = el.parent; node && node !== block; node = node.parent) if (node === control) return true
      return false
    }
    const command = [...walk(block)].find((el) => (el.name === 'pre' || el.name === 'code') && !insideControl(el))
    if (!control || !command) continue // the markup's shape is `copy.mjs`'s subject, not this one

    const layout = effective(classesOf(block))
    const display = layout['display'] ?? 'block'
    const direction = layout['flex-direction'] ?? 'row'

    const orderOf = (el) => {
      const value = effective(classesOf(el))['order']
      return value === undefined ? 0 : Number(value)
    }
    const order = [...walk(block)]
    const atStart =
      orderOf(control) === orderOf(command)
        ? order.indexOf(control) < order.indexOf(command)
        : orderOf(control) < orderOf(command)
    blocks.push({ klass: classesOf(block)[0] ?? '(unclassed)', display, direction, atStart })
  }
  return blocks
}

/** The command blocks on one built page that draw their control and their command on one row. */
export function sideBySideCopyBlocks(css, html, width = 80 * 16) {
  return copyBlocks(css, html, width).filter((block) => block.display === 'flex' && block.direction.startsWith('row'))
}

/**
 * Every command block on one built page that draws its control on the wrong side of the
 * command it belongs to. `base.css` states the rule — a command block is one row, with the
 * control beside the command at its top right — and every block on the site follows it by
 * reading one rule: `.command-block` is `display: flex` with `flex-direction: row-reverse`,
 * because the control comes **first in the markup** (it is `src/lib/copy-control.mjs`'s,
 * shared with every other block) and a plain row would therefore draw the control at the left
 * of the command and push the command to the right edge. `order: 1` on the control says the
 * same thing from the other end and is let through.
 *
 * The review of round 3 (item 3c) is why this is one rule and not two: the install cards used
 * to put the control in a row *above* their command while the chat's watch bar put it beside
 * one, and two placements for one control read as two different components. So the blocks
 * this returns are now every command block on the page, and a block that stops sharing the
 * row is a second placement the case below fails on rather than a shape this rule ignores.
 */
export function copyControlPlacementFailures(css, html) {
  const out = []
  for (const { klass, direction, atStart } of sideBySideCopyBlocks(css, html)) {
    const reversed = direction === 'row-reverse'
    if (reversed ? !atStart : atStart) {
      out.push(
        `the copy control of a .${klass} block is drawn before the command it belongs to (display: flex, flex-direction: ${direction}, and the control is first in the markup), where base.css puts the control beside the command at its top right and every command block on the site reads that way (changes spec 2026-10-18, item 3c)`,
      )
    }
  }
  return out
}

test('every command block that shares a row with its control draws the control at the right', () => {
  const failures = []
  for (const { rel, html } of builtPages()) failures.push(...copyControlPlacementFailures(pageCss(html), html).map((line) => `${rel}: ${line}`))
  assert.deepEqual(failures, [], `a command block draws its control on the wrong side:\n  ${failures.join('\n  ')}`)
})

test('the copy-control placement rule reads every command block, and refuses the one that shipped wrong', () => {
  const home = readFileSync(path.join(DIST, 'index.html'), 'utf8')
  const css = TODAY()
  const blocks = copyBlocks(css, home)
  const sideBySide = sideBySideCopyBlocks(css, home)
  assert.ok(
    blocks.length >= 3,
    `the homepage carries ${blocks.length} command block(s) with a control and a command, and it renders the two install cards and the chat's watch bar (changes spec 2026-10-18, items 3 and 4)`,
  )
  // One placement for all of them: the review of round 3 found the install cards putting the
  // control above their command while the watch bar put it beside one, so a block that does
  // not share the row any more is that defect coming back, not a shape this rule skips.
  assert.deepEqual(
    sideBySide.map((block) => block.klass).sort(),
    blocks.map((block) => block.klass).sort(),
    `the homepage's command blocks ${JSON.stringify(blocks.map((block) => `${block.klass}:${block.display}/${block.direction}`))} do not all read the same way, and one control has one placement on this site (changes spec 2026-10-18, item 3c)`,
  )
  assert.ok(
    sideBySide.some((block) => block.klass === 'chat-watch' && block.direction === 'row-reverse' && block.atStart),
    `the check finds no side-by-side command block on the homepage (it found ${JSON.stringify(sideBySide)}), so the cases below would pass without reading the block they are about (changes spec 2026-10-18, item 3)`,
  )
  assert.ok(
    sideBySide.some((block) => block.klass === 'command-block'),
    `the check does not read the install cards' own blocks (it found ${JSON.stringify(sideBySide)}), and item 3c is about the placement inside them`,
  )
  assert.deepEqual(copyControlPlacementFailures(css, home), [], 'the placement rule fails on the built homepage, so the case below is not proving what it names')

  // The defect as it shipped in the watch bar: a plain row, so the control — first in the
  // markup — is drawn at the left of the command. `.command-block`'s `row-reverse` is the
  // site's only one, so removing it leaves every command block on the homepage a plain row
  // and the check has to refuse all of them rather than the bar alone.
  const notReversed = plant('flex-direction:row-reverse;', '')
  const failures = copyControlPlacementFailures(notReversed, home)
  assert.equal(
    failures.length,
    sideBySide.length,
    `removing the one \`row-reverse\` left ${sideBySide.length} command block(s) drawing their control first, and the check refused ${failures.length}:\n  ${failures.join('\n  ') || '(nothing)'}`,
  )
  assert.ok(
    failures.some((f) => /drawn before the command it belongs to/.test(f)),
    `a row that is not reversed was not refused; the check said:\n  ${failures.join('\n  ') || '(nothing)'}`,
  )

  // And the other way of saying the same thing, which the check has to let through: the row
  // left alone, with the control ordered after the command. `order` is what puts the control
  // at main-end, which in a plain row is the right — the arrangement base.css asks for.
  const ordered = notReversed.replace('.command-controls{justify-content:flex-end;', '.command-controls{justify-content:flex-end;order:1;')
  assert.ok(ordered !== notReversed, 'this fixture moves the control to the end of the row with `order: 1` on .command-controls, and the built stylesheet no longer carries that rule')
  assert.deepEqual(
    copyControlPlacementFailures(ordered, home),
    [],
    'a row whose control is ordered after its command is refused, and that is how base.css states the control belongs on the right',
  )
})

// ---- an install command wraps rather than being cut off (changes spec item 4) ------------

/**
 * Every way the homepage's install commands can be cut off rather than read, for one built
 * page's stylesheet.
 *
 * The commands are `website/data/install.yaml`'s, and one of them is a single 44-character
 * token: `go install github.com/dimipaun/fugaro/cmd/fugaro@latest`. Set the way a command is
 * usually set — `white-space: pre` — that token runs past the card it sits in, and the reader
 * gets `…@late`: the defect the review of this branch found on the homepage at both sizes. So
 * the rule here is the one the page follows, and it has two parts, because a wrapping
 * declaration alone is not enough for a line with no spaces in it:
 *
 *   - **the command wraps** — `pre-wrap`, `pre-line` or the default rather than `pre` or
 *     `nowrap` — so its own line structure is kept and a line longer than the card still
 *     comes round;
 *   - **a long token has somewhere to break** — `overflow-wrap: anywhere`/`break-word`, or
 *     `word-break: break-all` — because `github.com/dimipaun/fugaro/cmd/fugaro@latest` has no
 *     break opportunity a wrapping algorithm has to take, and a token with nowhere to break
 *     runs past the card's edge however willing the block is to wrap.
 *
 * A block that scrolls instead — `white-space: pre` with `overflow-x: auto` — is refused
 * rather than allowed: whether a reader can see that a command block scrolls (a fade, or the
 * overlay scrollbars most platforms draw only while scrolling) is a rendered question no
 * hermetic check can answer, and a command a reader cannot tell is incomplete is the defect
 * the rule is about.
 */
export function installCommandFailures(css) {
  const out = []
  // Every rule that styles a command block's own `<pre>`, **wherever the block is**. The
  // homepage's install commands carry a class of their own, and every other command block on the
  // site — Getting Started's install cards, the chat's watch command — is reached by
  // `base.css`'s `.command-block > pre`. Reading only the homepage's class left the site's other
  // install commands held by nothing, which is where this rule's own defect then reappeared: the
  // `pre` gives up the width of the copy control beside it, so the page whose whole job is the
  // install pushed `go install …@latest` out of its card first.
  const rules = allRules(css).filter(
    (rule) => /\.home-install-command(?![\w-])/.test(rule.selector) || /\.command-block\s*>\s*pre(?![\w-])/.test(rule.selector),
  )
  if (rules.length === 0) return out
  const declared = (property) => rules.map(declarations).map((d) => d[property]).filter(Boolean).pop() ?? ''
  // A `<pre>`'s own default when the stylesheet declares nothing: the command keeps the single
  // line it is written on, which is what the first refusal below is about.
  const whiteSpace = declared('white-space') || 'pre'
  const wrap = [declared('overflow-wrap'), declared('word-break')]

  if (!whiteSpace.split(/\s+/).some((value) => value === 'pre-wrap' || value === 'pre-line' || value === 'normal')) {
    out.push(
      `the install command is set with white-space: ${whiteSpace}, which keeps every line the command is written on and does not wrap it: \`go install github.com/dimipaun/fugaro/cmd/fugaro@latest\` is one 44-character token in a card near 22 rem wide, so a block that cannot wrap it either clips it at the card's edge or scrolls it out of sight (changes spec 2026-10-18, item 4)`,
    )
  }
  if (!wrap.includes('anywhere') && !wrap.includes('break-word') && !wrap.includes('break-all')) {
    out.push(
      `the install command wraps but nothing breaks its long token (overflow-wrap: ${declared('overflow-wrap') || '(nothing declared)'}, word-break: ${declared('word-break') || '(nothing declared)'}): \`go install github.com/dimipaun/fugaro/cmd/fugaro@latest\` has no break opportunity a wrapping algorithm has to take, and a token with nowhere to break runs past the card's edge (changes spec 2026-10-18, item 4)`,
    )
  }
  return out
}

test('every command block on the site wraps rather than being cut off', () => {
  const failures = []
  let checked = 0
  for (const { rel, html } of builtPages()) {
    // A command block *element*: the marker is the `[data-copy]` attribute the control needs,
    // written the way markup writes it — the stylesheet every page carries names the class and
    // the attribute, so a substring test on either would say every page has a command block.
    if (!html.includes('data-copy ')) continue
    checked += 1
    failures.push(...installCommandFailures(pageCss(html)).map((line) => `${rel}: ${line}`))
  }
  assert.ok(checked > 0, 'no built page carries a command block, and the homepage offers the two ways to install the CLI (changes spec 2026-10-18, item 4)')
  assert.deepEqual(failures, [], `a command is cut off rather than read:\n  ${failures.join('\n  ')}`)
})

test('every command-block refusal fires on the defect it names', () => {
  const css = TODAY()
  assert.ok(css.includes('command-block'), 'the homepage carries no command block, so the cases below would pass without reading the block they are about')
  assert.deepEqual(installCommandFailures(css), [], 'the install-command rule fails on the built stylesheet, so the cases below are not proving what they name')

  // The defect as it shipped: a `<pre>` that keeps the command on its line and scrolls the
  // rest out of the card, which is the `…@late` the review of this branch found.
  const clipped = css.replace('white-space:pre-wrap;overflow-wrap:anywhere', 'white-space:pre;overflow-x:auto')
  assert.notEqual(clipped, css, 'this fixture rewrites the install command’s own declarations, and the built stylesheet no longer carries them')
  const clippedFailures = installCommandFailures(clipped)
  assert.ok(
    clippedFailures.some((f) => /does not wrap it/.test(f)),
    `the command that shipped clipped was not refused; the check said:\n  ${clippedFailures.join('\n  ') || '(nothing)'}`,
  )

  // And the other half of the rule: a block that wraps but leaves the long token nowhere to
  // break, which is the same defect one declaration later.
  const unbreakable = css.replace('white-space:pre-wrap;overflow-wrap:anywhere', 'white-space:pre-wrap')
  assert.notEqual(unbreakable, css, 'this fixture rewrites the install command’s own declarations, and the built stylesheet no longer carries them')
  const unbreakableFailures = installCommandFailures(unbreakable)
  assert.ok(
    unbreakableFailures.some((f) => /nothing breaks its long token/.test(f)),
    `a command that wraps without a break opportunity was not refused; the check said:\n  ${unbreakableFailures.join('\n  ') || '(nothing)'}`,
  )

  // **And the same defect on a page the homepage's class never reached**, which is the review's
  // finding: Getting Started's install cards carry no `home-install-command` — the assertion
  // below says so — so a check that read only that class returned nothing for the page whose
  // whole job is the install, while `base.css`'s `.command-block > pre` is the rule that decides
  // every command on it. Planting the clipped `pre` there is refused now, and would not have
  // been refused before the widening.
  const install = pageCss(readFileSync(path.join(DIST, 'getting-started', 'index.html'), 'utf8'))
  assert.ok(!install.includes('.home-install-command'), 'the install page carries the homepage’s install-command class, so this case would not prove the widening it is about')
  const installClipped = install.replace('white-space:pre-wrap;overflow-wrap:anywhere', 'white-space:pre;overflow-x:auto')
  assert.notEqual(installClipped, install, 'this fixture rewrites the command block’s own declarations, and the built stylesheet no longer carries them')
  const installFailures = installCommandFailures(installClipped)
  assert.ok(
    installFailures.some((f) => /does not wrap it/.test(f)),
    `the install page's clipped command was not refused; the check said:\n  ${installFailures.join('\n  ') || '(nothing)'}`,
  )
})

// ---- the menu (P3, D182) ---------------------------------------------------------------

// The rule the menu is held to, as a function over one built page, so that each part of it
// can be proved by a fixture below rather than only observed on today's markup. It is the
// contract RF4 names, rewritten twice: once for the two-level menu (the nav is a labelled
// landmark, its top-level items are `<details>` disclosures, every link in it is keyboard
// reachable, and the page carries exactly one `aria-current="page"` — on the wordmark when the
// page being read is the home page, D182 having made the logo the link home, on the menu link
// otherwise), and once for the owner's brief of 2026-10-19 (item 1), which took the single
// "Menu" toggle away: the three group labels are the menu, nothing stands in front of them,
// and a group's list of links is the `<details>`'s own child, because a closed `<details>` is
// the one thing that hides it.
//
// The wordmark's text is checked here too, beside the menu it has to share a row with: the
// phone breakpoint takes `.site-wordmark-name` out of that row, so a wordmark whose text is
// loose in the link is a row that overflows at 375 px with nothing in the stylesheet able to
// say which part of it gives way.

/** The route a built page's relative path serves. */
function routeOf(rel) {
  const dir = path.dirname(rel)
  return dir === '.' ? '/' : `/${dir}/`
}

/** The `aria-current="page"` marks on a page, as `{ name, href }`. */
function currents(doc) {
  return [...walk(doc)]
    .filter((el) => el.attrs.get('aria-current') === 'page')
    .map((el) => ({ name: el.name, href: el.attrs.get('href') ?? '' }))
}

/** Every menu failure of one built page, as the lines a reader sees. */
export function navFailures(html, rel) {
  const out = []
  const fail = (why) => out.push(`${rel}: ${why}`)
  const doc = parse(html)
  const all = [...walk(doc)]

  const navs = all.filter((el) => el.name === 'nav' && (el.attrs.get('class') ?? '').split(/\s+/).includes('site-nav'))
  if (navs.length !== 1) return [`${rel}: the page carries ${navs.length} section navs; it has one`]
  const nav = navs[0]
  if (!(nav.attrs.get('aria-label') ?? '').trim()) fail('the section nav has an empty label: a screen reader lists landmarks by name')

  // The drawer the menu replaced. A toggle standing in front of the three labels is the thing
  // the owner's brief of 2026-10-19 took away, and its markup coming back — in any of the three
  // parts it was made of — is a control between the reader and a section they can already see.
  for (const gone of ['site-nav-drawer', 'site-nav-toggle', 'site-nav-panel']) {
    if (all.some((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes(gone))) {
      fail(`the page still carries a .${gone}: the menu is the three group labels in the header's row, with no toggle in front of them`)
    }
  }
  // And nothing names a region outside the nav any more: the one `aria-controls` the menu had
  // was the toggle's, naming the panel that was its sibling rather than its child. A group's
  // list is its own `<details>`'s child, so a control that names something else is a control
  // describing a structure the menu no longer has.
  for (const el of walk(nav)) {
    const controls = el.attrs.get('aria-controls')
    if (controls !== undefined) {
      fail(`a <${el.name}> in the nav names ${JSON.stringify(controls)} with aria-controls: nothing the menu draws is outside the control that opens it`)
    }
  }

  // The menu itself: the three groups, as a list directly in the nav. Nothing between the nav
  // and the groups is what there is to say about a narrow viewport — the labels are the menu.
  const groups = [...walk(nav)].filter((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('site-nav-group'))
  if (groups.length !== 3) fail(`the menu carries ${groups.length} groups: the nav is three groups of three (D182)`)
  // One `name` across the groups makes them an exclusive set, without script: opening one
  // dropdown closes the one already open, so two never hang over each other on a wide row.
  const names = new Set(groups.flatMap((group) => group.children.filter((el) => el.name === 'details').map((el) => el.attrs.get('name') ?? '')))
  if (groups.length > 0 && (names.size !== 1 || names.has(''))) {
    fail(`the menu's groups carry the names ${JSON.stringify([...names])}: they share one non-empty name, so opening one dropdown closes the other two`)
  }
  for (const group of groups) {
    for (const item of group.children.filter((child) => child.name !== undefined)) {
      if (item.name !== 'details') {
        fail(`a top-level menu item is a <${item.name}>, and every top-level item is a <details> disclosure a keyboard can open`)
        continue
      }
      const summary = item.children.find((el) => el.name === 'summary')
      if (!summary || !textOf(summary).trim()) fail('a menu group has no <summary> with words on it')
      // The list is the `<details>`'s own child, and that is the whole of the mechanism: a
      // closed `<details>` hides what it holds, in every engine, with no stylesheet involved.
      // A list moved out of its details — as the drawer's panel once sat outside its toggle —
      // is a menu drawn open on every page at every width.
      const list = item.children.find((el) => el.name === 'ul')
      if (!list) {
        fail("a group's list of links is not inside its own <details>, so the group is drawn open wherever it appears: a closed <details> is the one thing that hides a list")
      } else if (![...walk(list)].some((el) => el.name === 'a')) {
        fail('a group has no links: the nav is three groups of three (D182)')
      }
    }
  }

  // The way home on a phone: the wordmark's text is in its own element, because that element is
  // what the phone breakpoint takes out of the header's row (the owner's ruling of 2026-10-19:
  // the mark stays, the text goes). Loose text in the link is text nothing can hide.
  const wordmark = all.find((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('site-wordmark'))
  const wordmarkName = wordmark && [...walk(wordmark)].find((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('site-wordmark-name'))
  if (!wordmark) {
    fail('the page carries no wordmark link to the home page')
  } else if (!wordmarkName || !textOf(wordmarkName).trim()) {
    fail("the wordmark's text is not in its own element (.site-wordmark-name), so the phone breakpoint has nothing to take out of the header's row and the row is wider than the viewport at 375 px")
  }

  // Keyboard reachability, and the current page.
  const links = [...walk(nav)].filter((el) => el.name === 'a')
  if (links.length < 2) fail(`the menu carries ${links.length} links: it lists the site's sections`)
  for (const link of links) {
    if (!link.attrs.has('href')) fail('a menu link has no href, so it is not keyboard-reachable')
    const tabindex = link.attrs.get('tabindex')
    if (tabindex !== undefined && Number(tabindex) < 0) fail('a menu link is taken out of the tab order')
  }

  const route = routeOf(rel)
  const marked = currents(doc)
  const listsRoute = [...walk(nav)].some((el) => el.attrs.get('href') === route) || route === '/'
  if (listsRoute) {
    if (marked.length !== 1) {
      fail(`the page marks ${marked.length} elements as the current page; it marks exactly one`)
    } else if (marked[0].href !== route) {
      fail(`the page marks ${JSON.stringify(marked[0].href)} as the current page and its own route is ${JSON.stringify(route)}`)
    }
  }
  return out
}

test('the section nav is a labelled landmark of keyboard-reachable links, with the current page marked', () => {
  const failures = []
  for (const { rel, html } of builtPages()) failures.push(...navFailures(html, rel))
  assert.deepEqual(failures, [], `the menu breaks the contract RF4 states:\n  ${failures.join('\n  ')}`)
})

// ---- the refusals, kept ------------------------------------------------------------------

// The three groups, as the layout writes them, so a fixture is the real shape and not a
// smaller one the rule happens to accept.
const FIXTURE_GROUPS = [
  ['Product', [['/how-it-works/', 'How it works'], ['/getting-started/', 'Getting started'], ['/caps-and-costs/', 'Caps and costs']]],
  ['Reference', [['/docs/', 'Docs'], ['/console/', 'Console'], ['/changelog/', 'Changelog']]],
  ['Project', [['/security/', 'Security'], ['/faq/', 'FAQ'], ['/community/', 'Community']]],
]

// The wordmark, as the layout writes it: the logo is decoration beside the name, and the name
// is the element the phone breakpoint takes out of the row.
const FIXTURE_WORDMARK =
  '<a class="site-wordmark" href="/" rel="home"><img src="/logo.png" alt="" width="47" height="32" /><span class="site-wordmark-name">Fugaro</span></a>'

function fixtureNav({ label = 'Sections', current = '/docs/' } = {}) {
  const groups = FIXTURE_GROUPS.map(([group, items]) => {
    const links = items
      .map(([href, text]) => `<li><a href="${href}"${href === current ? ' aria-current="page"' : ''}>${text}</a></li>`)
      .join('')
    return `<li class="site-nav-group"><details name="site-nav-group"><summary>${group}</summary><ul>${links}</ul></details></li>`
  }).join('')
  return `<nav class="site-nav" aria-label="${label}"><ul class="site-nav-groups">${groups}</ul></nav>`
}

function fixturePage({ nav = fixtureNav(), wordmark = FIXTURE_WORDMARK } = {}) {
  return `<!DOCTYPE html><html lang="en"><head><title>t</title></head><body><header class="site-header">${wordmark}${nav}</header></body></html>`
}

function navRefusals(html, rel = 'docs/index.html') {
  return navFailures(html, rel)
}

test('the menu passes its own rule, so each refusal below is the one thing it breaks', () => {
  assert.deepEqual(navRefusals(fixturePage()), [])
  // The home page: the wordmark is the link home and the one element marked current.
  assert.deepEqual(
    navRefusals(
      fixturePage({
        nav: fixtureNav({ current: '' }),
        wordmark: FIXTURE_WORDMARK.replace('rel="home"', 'rel="home" aria-current="page"'),
      }),
      'index.html',
    ),
    [],
  )
})

test('every menu refusal fires on the defect it names', () => {
  const cases = [
    ['an unlabelled nav', fixturePage({ nav: fixtureNav({ label: '' }) }), /empty label/],
    [
      'the drawer toggle, back in the header',
      fixturePage({ nav: fixtureNav().replace('<ul class="site-nav-groups">', '<details class="site-nav-drawer"><summary class="site-nav-toggle">Menu</summary></details><ul class="site-nav-groups">') }),
      /still carries a \.site-nav-drawer/,
    ],
    [
      'the panel the drawer opened, back around the groups',
      fixturePage({
        nav: fixtureNav()
          .replace('<ul class="site-nav-groups">', '<div class="site-nav-panel" id="site-nav-panel"><ul class="site-nav-groups">')
          .replace('</ul></nav>', '</ul></div></nav>'),
      }),
      /still carries a \.site-nav-panel/,
    ],
    [
      'a control naming a region outside itself',
      fixturePage({ nav: fixtureNav().replace('<summary>Product</summary>', '<summary aria-controls="site-nav-panel">Product</summary>') }),
      /names "site-nav-panel" with aria-controls/,
    ],
    ['a group with no summary', fixturePage({ nav: fixtureNav().replace('<summary>Product</summary>', '') }), /no <summary> with words on it/],
    ['groups that are not one exclusive set', fixturePage({ nav: fixtureNav().replace('<details name="site-nav-group"><summary>Product', '<details><summary>Product') }), /share one non-empty name/],
    [
      "a group's list moved out of its own <details>",
      fixturePage({
        nav: fixtureNav()
          .replace('<summary>Product</summary><ul>', '<summary>Product</summary></details><ul>')
          .replace('</ul></details></li>', '</ul></li>'),
      }),
      /list of links is not inside its own <details>/,
    ],
    ['a group with no links', fixturePage({ nav: fixtureNav().replace(/<ul>.*?<\/ul>/, '<ul></ul>') }), /no links/],
    ['a top-level item that is not a control', fixturePage({ nav: fixtureNav().replace('<li class="site-nav-group">', '<li class="site-nav-group"><span>Product</span>') }), /top-level menu item is a <span>/],
    ['a link with no href', fixturePage({ nav: fixtureNav().replace('<a href="/docs/" aria-current="page">', '<a aria-current="page">') }), /no href/],
    ['a link out of the tab order', fixturePage({ nav: fixtureNav().replace('<a href="/faq/">', '<a href="/faq/" tabindex="-1">') }), /out of the tab order/],
    ['no current page marked', fixturePage({ nav: fixtureNav({ current: '' }) }), /marks 0 elements/],
    ['two current pages marked', fixturePage({ nav: fixtureNav().replace('<a href="/console/">', '<a href="/console/" aria-current="page">') }), /marks 2 elements/],
    ['the wrong page marked current', fixturePage({ nav: fixtureNav({ current: '/console/' }) }), /its own route is "\/docs\/"/],
    [
      "the wordmark's text loose in the link",
      fixturePage({ wordmark: '<a class="site-wordmark" href="/" rel="home">Fugaro</a>' }),
      /not in its own element/,
    ],
    ['no wordmark at all', fixturePage({ wordmark: '' }), /no wordmark link to the home page/],
  ]
  for (const [what, html, expected] of cases) {
    const failures = navRefusals(html)
    assert.ok(failures.some((f) => expected.test(f)), `${what} was not refused (${expected}); the check said:\n  ${failures.join('\n  ') || '(nothing)'}`)
  }
})

test('the menu rule holds the home page to the wordmark as its one current mark', () => {
  // The home page lists no `/` link, so the rule reads the wordmark: with no mark at all, and
  // with the mark on something else, it refuses.
  assert.ok(navRefusals(fixturePage({ nav: fixtureNav({ current: '' }) }), 'index.html').some((f) => /marks 0 elements/.test(f)))
  assert.ok(
    navRefusals(
      fixturePage({
        nav: fixtureNav({ current: '' }),
        wordmark: '<a class="site-wordmark" href="/docs/" rel="home" aria-current="page">Fugaro</a>',
      }),
      'index.html',
    ).some((f) => /its own route is "\/"/.test(f)),
  )
})

test('the skip link is the first thing in the body and points at main', () => {
  for (const { rel, html } of builtPages()) {
    const body = /<body[^>]*>\s*(<a[^>]*>)/.exec(html)
    assert.ok(body, `${rel}: the body does not open with a link`)
    assert.match(body[1], /class="skip-link"/, `${rel}: the first link is not the skip link`)
    assert.match(body[1], /href="#main"/, `${rel}: the skip link does not point at #main`)
    assert.match(html, /<main id="main"/, `${rel}: there is no main#main for the skip link to reach`)
  }
})
