// website/test/stacking.mjs — the header and its menu paint above the page's blocks.
//
// **The defect this file exists for** (the owner's reading of 2026-10-19: "How it works: menu
// hidden by the blocks on mobile"): the header and the panel a group hangs below it were in
// the page's own stacking layer, so a block a page draws *after* the header and takes a place
// out of the flow painted over them. The four-step flow's steps were exactly that block —
// `position: relative` is what their arrows were placed against, and they sat on the homepage,
// which was the page that carried the flow — and the owner's ruling of 2026-10-20 retires the
// flow and the component with it. A positioned box with no `z-index` paints with every other
// such box in tree order, and the header is the first of them: an open drawer had each step's
// card painted across it, which is a navigation a reader on a phone cannot use.
//
// **The fix is the header's own layer, not the block's.** Taking the position off the steps
// would have broken the arrows, and a `z-index` on each content component is a rule every
// future component has to remember; `src/styles/base.css` gives `.site-header` the site's
// header layer instead, which makes it a stacking context and paints it, with everything it
// holds — the panel included — above every block a page draws, at every width and on every
// page. `src/styles/tokens.css` names that layer and the one above it (the reader's chrome),
// so the site has one scale rather than a number per component — and the block that stands
// where the flow's steps stood is the homepage's slider, whose panels are positioned content
// on the same page, held to the same rule below.
//
// **What this file holds, over the built CSS of every page:** the scale is declared and in
// order — the header's layer a positive integer, above every layer a page's own rules declare,
// because a `z-index` of 0 paints in the page's own step and the defect would be back; the
// header is positioned and carries the header layer, which is what makes it a
// stacking context; the reader's chrome — the skip link and the consent banner — carries the
// chrome layer and nothing else does; and **no other rule declares a `z-index` at all**. That
// last rule is the one this defect needed: it refuses a content component a layer of its own,
// which is the only way a page block can take the header's place in the paint order.
//
// The gate is a function over one `dist`, so its refusals are kept the way the other gates
// keep theirs: the cases below write a synthetic page with each defect planted beside a
// fixture that keeps every rule, and each rule was mutation-proved by planting it in the
// built site and watching this file fail.
//
// The pages are read from `dist/`, which `npm run check` builds before it runs this file. No
// browser, no network: the checks are over the CSS a browser would receive.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { DIST, builtPages, cssDeclarations, cssRules, parse, walk } from './lib/html.mjs'

// The scale's two names, as `src/styles/tokens.css` declares them. They are read out of the
// built stylesheet rather than out of the source: what a reader's browser receives is what
// this file is about, and a token the build drops is a layer no rule has.
const HEADER = '--z-header'
const CHROME = '--z-chrome'

// The three rules a layer belongs to, and the layer each one is allowed to name. Anything else
// that declares a `z-index` is a page block or a component taking a place in the paint order,
// which is the defect: **a page's own content names no layer at all** — the header's layer is
// not a ceiling a block may sit just under, it is the header's own row — and the two chrome
// rules above it exist because the skip link is drawn over the header it skips past and the
// banner is fixed to the window's foot.
const LAYERS = [
  { name: '.site-header', token: HEADER },
  { name: '.skip-link', token: CHROME },
  { name: '.consent-banner', token: CHROME },
]

const TOKEN = /^--z-[\w-]+$/

/**
 * Whether a selector names the rule's own class, **wherever it sits**: alone (`.site-header`),
 * in a compound in any position (`header.site-header`, `.site-header:hover`, `.site-header[x]`)
 * or after any combinator (`body > .site-header`). The head of the selector is not the only
 * place a rule can take a property off a box — a later rule in any of those shapes wins the
 * cascade just the same — so reading only the head was a hole: `header.site-header{position:static}`
 * un-positioned the header with this file still reporting success. The trailing lookahead keeps
 * a longer class (`.site-header-inner`) from counting as this one; nothing else can match, since
 * `.site-header` with a dot in front of it is the class selector and not a suffix of a name.
 */
function owns(selector, name) {
  const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  return new RegExp(`${escaped}(?![\\w-])`).test(selector)
}

/** The CSS a built page carries: its inline `<style>` blocks and any stylesheet it links. */
function pageCss(html, dist) {
  const inline = walk(parse(html))
    .filter((el) => el.name === 'style')
    .map((el) => el.raw ?? '')
  const linked = [...html.matchAll(/<link[^>]*rel="stylesheet"[^>]*href="(\/[^"]+)"/g)].map((m) =>
    readFileSync(path.join(dist, m[1]), 'utf8'),
  )
  return [...inline, ...linked].join('\n')
}

/** The layer tokens, the `z-index` rules and the header's own positions in a stylesheet. */
function readLayers(css) {
  const tokens = new Map()
  const zIndex = []
  const headerPosition = []
  for (const rule of cssRules(css)) {
    const declarations = cssDeclarations(rule.body)
    for (const [property, value] of declarations) {
      if (TOKEN.test(property)) tokens.set(property, value)
      if (property === 'z-index') zIndex.push({ selector: rule.selector, value })
      if (property === 'position' && owns(rule.selector, '.site-header')) headerPosition.push(value)
    }
  }
  return { tokens, zIndex, headerPosition }
}

// ---- the rule, as a function of one built artifact --------------------------------------

/** Every way a built page's own layering departs from the scale above. */
export function stackingFailures(dist) {
  const out = []
  for (const { rel, html } of builtPages(dist)) {
    const layers = readLayers(pageCss(html, dist))

    // 1. The scale itself: two integer layers, the header positive and the chrome above the
    //    header. A page that carries neither has no layer to put its header on.
    const header = layers.tokens.get(HEADER)
    const chrome = layers.tokens.get(CHROME)
    for (const [token, value] of [
      [HEADER, header],
      [CHROME, chrome],
    ]) {
      if (!/^\d+$/.test(value ?? '')) {
        out.push(
          `${rel} declares no integer ${token} in its stylesheet, so the layering has no scale (src/styles/tokens.css is where it is written)`,
        )
      }
    }
    if (/^\d+$/.test(header ?? '') && /^\d+$/.test(chrome ?? '') && Number(header) >= Number(chrome)) {
      out.push(
        `${rel} puts ${CHROME} at ${chrome} and ${HEADER} at ${header}: the reader's chrome is above the header's row, and the header above the page (src/styles/tokens.css)`,
      )
    }

    // 1b. And the header's layer is a row **above the page's own blocks**, which is the half
    //     of the scale the check above does not reach: `--z-header: 0` is an integer and is
    //     below the chrome, so it reads as a scale — yet a positioned box at `z-index: 0`
    //     paints in the same step as a later positioned block with `z-index: auto`, in tree
    //     order, and the header is the first of them: the step's card covers the open panel
    //     again, exactly the defect this file exists for. The header's layer is therefore a
    //     **positive** integer, and strictly above every layer a page's own rules declare.
    if (/^\d+$/.test(header ?? '')) {
      const value = Number(header)
      if (value < 1) {
        out.push(
          `${rel} puts ${HEADER} at ${header}: a header layer of 0 or less paints the header in the page's own step, in tree order, where a block drawn after it covers the menu (src/styles/tokens.css)`,
        )
      }
      for (const rule of layers.zIndex) {
        if (LAYERS.some((entry) => owns(rule.selector, entry.name))) continue
        const block = Number(rule.value)
        if (Number.isFinite(block) && block >= value) {
          out.push(
            `${rel} puts ${HEADER} at ${header} with ${rule.selector} at ${rule.value}: the header sits strictly above every layer a page's own blocks declare, or a block at the header's row covers the menu (src/styles/tokens.css)`,
          )
        }
      }
    }

    // 2. The header's own rule: positioned, so a `z-index` applies to it at all, and carrying
    //    the header's layer, which is what makes it a stacking context and paints its panel
    //    above every block a page draws after it.
    if (!layers.headerPosition.length) {
      out.push(
        `${rel} does not position .site-header: a z-index on it would do nothing, and the header could not paint above the page's blocks (src/styles/base.css)`,
      )
    } else if (layers.headerPosition.includes('static')) {
      // **Any** rule that takes the position off the header, not only a sheet that never
      // positions it: a later `.site-header` rule — a media block's override is the shape this
      // takes — wins the cascade, and a static box is one a `z-index` does not apply to, so the
      // header would paint in the page's own layer again with this gate still reporting success.
      out.push(
        `${rel} leaves .site-header position: static: a z-index on it would do nothing, and the header could not paint above the page's blocks (src/styles/base.css)`,
      )
    }
    const headerRules = layers.zIndex.filter((rule) => owns(rule.selector, '.site-header'))
    if (!headerRules.length) {
      out.push(
        `${rel} gives .site-header no z-index: the header and the panel its groups hang below paint in the page's own layer, where a block drawn after them covers the menu (src/styles/base.css)`,
      )
    } else {
      for (const rule of headerRules) {
        if (rule.value !== `var(${HEADER})`) {
          out.push(
            `${rel} sets .site-header's z-index to ${rule.value}, want var(${HEADER}): the header's layer is the scale's and not a number of its own (src/styles/tokens.css)`,
          )
        }
      }
    }

    // 3. The chrome's two rules, and nothing else at that layer.
    for (const rule of layers.zIndex.filter((rule) => rule.value === `var(${CHROME})`)) {
      if (!LAYERS.some((layer) => layer.token === CHROME && owns(rule.selector, layer.name))) {
        out.push(
          `${rel} puts ${rule.selector} on the chrome's layer: ${CHROME} belongs to the skip link and the consent banner, and a page that takes it draws itself over the reader's own controls`,
        )
      }
    }
    for (const name of ['.skip-link', '.consent-banner']) {
      if (!layers.zIndex.some((rule) => owns(rule.selector, name) && rule.value === `var(${CHROME})`)) {
        out.push(
          `${rel} gives ${name} no z-index of var(${CHROME}): the reader's chrome is drawn above the page and the header, or it is drawn under them`,
        )
      }
    }

    // 4. And the rule this whole file needed: a page block or a component may not name a layer
    //    at all. A `z-index` on a positioned content block is a second place in the paint
    //    order, and the header is the first one — which is how a step's card came to be drawn
    //    across an open menu.
    for (const rule of layers.zIndex) {
      const layer = LAYERS.find((entry) => owns(rule.selector, entry.name))
      if (!layer) {
        out.push(
          `${rel} gives ${rule.selector} a z-index of ${rule.value}: a page's own blocks paint under the header, and a component that names a layer of its own is what covers the menu (src/styles/base.css puts the header above them)`,
        )
      } else if (rule.value !== `var(${layer.token})`) {
        out.push(`${rel} gives ${rule.selector} a z-index of ${rule.value} where the scale allows var(${layer.token})`)
      }
    }
  }
  return out
}

// ---- the built site ---------------------------------------------------------------------

test('the header paints above the page on every built page', () => {
  assert.deepEqual(stackingFailures(DIST), [])
})

test('the homepage\'s blocks and the run\'s picture name no layer of their own', () => {
  // The block the owner's complaint was about. The retired case read the flow's steps on the
  // built homepage — they were positioned, which is what their arrows were placed against, and
  // they named no `z-index`, which is what the defect was — and it is replaced here by the same
  // two readings of the blocks that took the flow's place (the owner's ruling of 2026-10-20):
  // the homepage's slider, which stands where the flow stood and draws panels of its own, and
  // the run's picture on How It Works, which is a figure full of positioned SVG. It is asserted
  // rather than left to the case above because this is the shape a later "fix" would take: the
  // block's own rule, in the built page, taking a place in the paint order.
  //
  // **The slider is retired by the owner's ruling of 2026-10-23** (the redesign's Step 3): the
  // homepage's positioned block is now the hero's stage, which holds the live card and the
  // watch capture, and the run is a section of its own. The same two readings hold them.
  const pages = builtPages(DIST).filter((entry) => ['index.html', 'how-it-works/index.html'].includes(entry.rel))
  assert.equal(
    pages.length,
    2,
    `dist carries ${pages.length} of the two pages this case is about: \`npm run build\` writes them (npm run check does it first)`,
  )
  // Every class the two blocks draw with, so a rule added for any part of either one is read
  // here: the slider's own row and its dots (the owner's feedback of 2026-10-21, item 3, turned
  // the strip of tabs into the slider, and the two classes the tabs carried are gone with them).
  const blocks =
    /(^|[\s,>+~])\.(hero-stage|hero-shot|hero-shot-frame|hero-watch|hero-watch-crop|hero-watch-image|home-run|home-run-steps|home-run-step|home-run-figure|home-run-image|watch-shot|watch-block|watch-command|watch-note|run-diagram|run-svg|run-words)(\[|[:,]|$)/
  let positioned = 0
  for (const page of pages) {
    const rules = cssRules(pageCss(page.html, DIST)).filter((rule) => blocks.test(rule.selector))
    assert.ok(
      rules.length > 0,
      `${page.rel} carries no rule for the hero's stage, the run section or the run's picture, and this case reads the blocks that stand where the flow's steps stood`,
    )
    for (const rule of rules) {
      assert.ok(
        !cssDeclarations(rule.body).some(([property]) => property === 'z-index'),
        `${rule.selector} declares a z-index: the block is page content and paints under the header, which is what the owner's reading of 2026-10-19 was about (src/styles/base.css puts the header above them)`,
      )
      if (page.rel === 'index.html' && /(^|[\s,>+~])\.hero-stage(\[|[:,]|$)/.test(rule.selector) && cssDeclarations(rule.body).some(([p, v]) => p === 'position' && v !== 'static')) {
        positioned += 1
      }
    }
  }
  assert.ok(
    positioned > 0,
    'no rule positions the homepage\'s hero stage, and the stage is the homepage\'s positioned block: this case reads the positioned box that took the flow\'s place, so a page without one is a page this reading has lost its subject on',
  )
})

// ---- the refusals, kept -----------------------------------------------------------------
//
// One synthetic page per defect, beside a fixture that keeps every rule. The page carries the
// scale, the two chrome rules and the header's own rule; each case replaces one of them, or
// adds a content rule after them, so it fails for its own defect and not for the fixture's.

const SCALE = ':root{--z-header:10;--z-chrome:20}'
const SKIP_LINK = '.skip-link{position:absolute;left:-9999px;z-index:var(--z-chrome)}'
const BANNER = '.consent-banner{position:fixed;bottom:0;z-index:var(--z-chrome)}'
const HEADER_RULE = '.site-header{display:flex;position:relative;z-index:var(--z-header)}'

/** A built page whose stylesheet carries `css` after the scale and the three layered rules. */
function fixturePage(css = '') {
  const style = [SCALE, SKIP_LINK, BANNER, HEADER_RULE, css].join('\n')
  return (
    '<!DOCTYPE html><html lang="en"><head><title>t</title>' +
    `<style>${style}</style></head><body><header class="site-header"></header><main></main></body></html>`
  )
}

function distOf(t, files) {
  const dist = mkdtempSync(path.join(tmpdir(), 'fugaro-stacking-'))
  t.after(() => rmSync(dist, { recursive: true, force: true }))
  for (const [rel, body] of Object.entries(files)) {
    mkdirSync(path.dirname(path.join(dist, rel)), { recursive: true })
    writeFileSync(path.join(dist, rel), body)
  }
  return dist
}

/** The refusals of one fixture page, joined into the message a case matches against. */
function failuresOf(t, html) {
  return stackingFailures(distOf(t, { 'index.html': html })).join('\n')
}

/** The fixture page with the page's own rules given one more rule after them. */
const withRule = (css) => fixturePage(css)

/** The fixture page with another rule after the page's own, and the scale replaced. */
const withScale = (scale, css) => fixturePage(css).replace(SCALE, scale)

/** The fixture page with `find` replaced by `replace` in its stylesheet. */
const withReplaced = (find, replace) => fixturePage().replace(find, replace)

test('a fixture that keeps every rule is not refused', (t) => {
  // Every case below is one rule away from this page, so a case that failed for the fixture's
  // sake rather than for its own defect would be a case proving nothing.
  assert.equal(failuresOf(t, fixturePage()), '')
})

test('a page block that names a layer of its own is refused', (t) => {
  // The defect itself, in the shape a content component takes it: a positioned block on the
  // page with a `z-index` of its own — the header's layer, a raw number, or the chrome's.
  assert.match(
    failuresOf(t, withRule('.easy-step{position:relative;z-index:var(--z-header)}')),
    /gives \.easy-step a z-index of var\(--z-header\)[\s\S]*covers the menu/,
  )
  assert.match(failuresOf(t, withRule('.panel{position:relative;z-index:5}')), /gives \.panel a z-index of 5/)
  assert.match(
    failuresOf(t, withRule('.hero{position:relative;z-index:var(--z-chrome)}')),
    /puts \.hero on the chrome's layer/,
  )
})

test('a header that cannot paint above the page is refused, however it is broken', (t) => {
  // The header's layer, gone: the fix undone, which is the page the owner read.
  assert.match(
    failuresOf(t, withReplaced(';z-index:var(--z-header)', '')),
    /gives \.site-header no z-index[\s\S]*covers the menu/,
  )
  // A layer a z-index cannot apply to: `position: static` is the default, and a layer on a
  // static box is a declaration that does nothing.
  assert.match(failuresOf(t, withReplaced('position:relative', 'position:static')), /leaves \.site-header position: static/)
  assert.match(failuresOf(t, withReplaced('position:relative;', '')), /does not position \.site-header/)
  // And the position taken off *after* the header's own rule, which the cascade gives the last
  // word to: a sheet that positions the header somewhere and un-positions it here is a header a
  // `z-index` does not apply to, and the check may not read it as "somewhere it is positioned".
  assert.match(failuresOf(t, withRule('.site-header{position:static}')), /leaves \.site-header position: static/)
  // A layer of the header's own, as a number rather than the scale's name.
  assert.match(
    failuresOf(t, withReplaced('var(--z-header)', '99')),
    /sets \.site-header's z-index to 99, want var\(--z-header\)/,
  )
})

test('a header rule the class does not lead is refused, wherever the class sits', (t) => {
  // The same un-positioning, written so that `.site-header` is not the head of the selector:
  // an element selector in front of it (`header.site-header`), and a combinator in front of it
  // (`body > .site-header`). Both win the cascade exactly as the head-of-selector rule does,
  // and both were read as "the header is not mentioned at all" while `owns` looked only at the
  // head — a `position: static` on the header this file stayed green on. `owns` matches the
  // class anywhere in the selector now, which is what these two shapes hold it to.
  assert.match(
    failuresOf(t, withRule('header.site-header{position:static}')),
    /leaves \.site-header position: static/,
  )
  assert.match(
    failuresOf(t, withRule('body > .site-header{position:static}')),
    /leaves \.site-header position: static/,
  )
})

test('chrome drawn under the header is refused', (t) => {
  // A skip link the header it skips past is drawn over, and a banner the page's blocks cover.
  assert.match(
    failuresOf(t, withReplaced(SKIP_LINK, '.skip-link{position:absolute;left:-9999px}')),
    /gives \.skip-link no z-index of var\(--z-chrome\)/,
  )
  assert.match(
    failuresOf(t, withReplaced(BANNER, '.consent-banner{position:fixed;bottom:0}')),
    /gives \.consent-banner no z-index of var\(--z-chrome\)/,
  )
})

test('a scale that is not the scale is refused', (t) => {
  // The chrome under the header, and a token the build does not carry at all.
  assert.match(
    failuresOf(t, withReplaced(SCALE, ':root{--z-header:10;--z-chrome:5}')),
    /puts --z-chrome at 5 and --z-header at 10/,
  )
  assert.match(failuresOf(t, withReplaced(SCALE, ':root{--z-chrome:20}')), /declares no integer --z-header/)
  // A layer of zero: an integer, and below the chrome, so the order above reads it as a scale
  // — but a box at `z-index: 0` paints in the page's own step, in tree order, which is the
  // defect. `--z-header:0` in a copy of the built site is this case, and the built gate said
  // ok before this check existed.
  assert.match(
    failuresOf(t, withReplaced(SCALE, ':root{--z-header:0;--z-chrome:20}')),
    /puts --z-header at 0: a header layer of 0 or less[\s\S]*covers the menu/,
  )
  // And a block of a page's own at or over the header's row: the header's layer is strictly
  // above every layer a page's rules declare, not level with the first one under it.
  assert.match(
    failuresOf(t, withScale(':root{--z-header:1;--z-chrome:20}', '.easy-step{position:relative;z-index:5}')),
    /puts --z-header at 1 with \.easy-step at 5: the header sits strictly above/,
  )
})
