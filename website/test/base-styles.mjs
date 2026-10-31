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
// **The third subject is the layout of the pages' own pictures** (changes spec of 2026-10-18,
// item 4). It was the four-step flow's: `ArchitectureDiagram.astro`'s cards were a quarter of
// the row each, laid out from 48 rem, where a card's content box was 100 px wide and the words
// its own step names were made of did not fit in that — a diagram that cannot print its own
// labels is the picture failing at the one thing it is for. The flow is retired (the owner's
// ruling of 2026-10-20: the component is deleted, and the pages show the homepage's three-tab
// slider and the run's drawing), and the same two readings are here for what replaced it: **the
// room a step name is printed in** (`sliderLayoutFailures` reads the slider's grid, its list's
// cap and the gutter between `main` and the list, against the longest word of the three names)
// and **the arrows of the run's drawing**, which leave each box and land on the next
// (`runArrowFailures` reads the cards, the gaps between them and each head's three corners).
//
// **Two more readings of the same kind, both of them defects a browser shows rather than a
// preference: an arrow that points the way the run does not travel, and a command block's copy
// control sitting at the left of the block it belongs to.** The first was `transform:
// rotate(90deg)` on an arrow drawn pointing down, which turned it leftwards — from each step
// back at the one before it — and it is now the drawing's own geometry: a head at the wrong end
// of a shaft, or a shaft drawn from the later box back to the earlier one. The second is
// `justify-content: space-between` on a row whose first child in the markup is the shared
// control, which puts the control at the left of its command, the other way round from
// `base.css`'s rule and from every other command block on the site. Both are read from the
// built page as arithmetic or as a cascade, because both are invisible to a check that only
// counts elements: `slider.mjs` counts the three panels and `copy.mjs` clicks the control, and
// neither of them says which way an arrow points or where the control is drawn.
//
// **And the run under the picture was one vertical timeline** (the owner's feedback of
// 2026-10-19: "The run, step by step section does not look good"). Five steps laid out as a
// `repeat(auto-fit, minmax(18rem, 1fr))` grid read three beside two with a hole in the last
// row on a desktop and two-by-two on a tablet, with nothing joining one step to the next — a
// list of facts rather than a run. `runTimelineFailures` read the shape that timeline claimed:
// one column in force at 360, 768 and 1280 px, a connector on the steps that have one below
// them and none on the last, and no width or `nowrap` in the section wider than the phone's
// content box, which is the overflow a browser would show at 360 px. The owner's approved
// draft of 2026-10-19 replaced the timeline with the diagram, and its shape — every line of
// the drawing measured against the box it is drawn in — is held by `test/how-it-works.mjs`'s
// `runDiagramFailures`; what is left of the run's layout here is the room its blocks take.
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
//   - **the menu is the group labels in the header's one row, at every width.** The owner's brief
//     of 2026-10-19 (item 1) took the single "Menu" toggle away, so the group labels are
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
//     the page gets a sideways scroll. **The row carried the Home link as well when the
//     owner's feedback of 2026-10-21 put it in the menu** — a fourth item, about 45 px at
//     this size — so the breakpoint is the wider one that item needs (the text leaves the row
//     from about 432 px down, where the table's three-item row left it from 390 px down) and
//     the row's own spacing below it is where the Home item is paid for: the phone block takes
//     the 8 px between the wordmark and the menu to 4 and the items' own 4 px gap to none.
//     **The same feedback then dropped the Reference group**, so the row carries three items
//     again and the cut is left at the wider one it was measured at rather than re-tightened:
//     it is the safe direction, and re-measuring the row is the owner's live check 70a's. Out of
//     the row is not out of the page, though: the mark
//     beside the text is decoration with an empty `alt`, so the text is the home link's only
//     name and no `.site-wordmark-name` rule — in the phone block **or in the base rule above
//     it**, which is where a `display:none` would hide it at every width — may hide it from a
//     screen reader;
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
  if (!/\.site-nav-groups\{[^}]*display:flex/.test(css)) fail("the group labels are not laid out in the header's row")
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

  const phone = mediaBlocks(css).filter((block) => /27rem/.test(block.query))
  if (phone.length === 0) {
    fail("the stylesheet has no phone breakpoint, so the wordmark's text is drawn beside the labels at 375 px")
  }
  const hidden = phone.map((block) => /\.site-wordmark-name\{([^}]*)\}/.exec(block.body)).find(Boolean)?.[1]
  if (phone.length && !hidden) fail("the wordmark's text is drawn beside the labels at 375 px, where the row has no room for it")
  if (hidden && !/position:absolute/.test(hidden)) {
    fail("the wordmark's text is taken out of the row without being taken out of the flow, so the row is still as wide as the word")
  }
  // And the row's own spacing below the breakpoint, which is where the Home item the owner's
  // feedback of 2026-10-21 put in the menu was paid for on the narrowest phones. The row
  // carries three items again since that day's later feedback dropped the Reference group, so
  // the two rules are headroom rather than the whole of the payment — kept, because the cut
  // beside them is still the wider one measured with the item in the row, and a phone block
  // that leaves the base gap between the wordmark and the menu, and the items' own 4 px,
  // standing is a row the measurement never saw.
  const phoneRule = (selector) => phone.map((block) => new RegExp(`\\${selector}\\{([^}]*)\\}`).exec(block.body)).find(Boolean)?.[1]
  if (phone.length && phoneRule('.site-header') === undefined) {
    fail("the phone block leaves the header's row at the gap it has above the breakpoint, where the cut the row was measured at (27 rem, with the Home item the owner's feedback of 2026-10-21 put in it) leaves a 320 px phone's content box none to spare")
  }
  if (phone.length && phoneRule('.site-nav-groups') === undefined) {
    fail("the phone block leaves the menu's items 4 px apart, and the row's cut is the wider one measured with the Home item in it, where a 320 px phone's content box has none to spare")
  }
  // **Every** `.site-wordmark-name` rule, not only the phone block's: the base rule is the one
  // that is in force at 1280 px, so a `display:none` there takes the name away at every width
  // while the phone block still reads as the correct clipping, and a check that only read
  // inside the block would pass it (the review of 2026-10-19). The mark beside the text is
  // decoration with an empty `alt`, so the text is the link's only name and this is not a
  // layout preference.
  for (const rule of cssRules(css)) {
    if (rule.selector !== '.site-wordmark-name') continue
    if (/display:none|visibility:hidden/.test(rule.body ?? '')) {
      fail("the wordmark's text is hidden from a screen reader as well as from the eye, and the logo beside it is decoration with an empty alt: the link to the home page is left naming nothing")
    }
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

  // The menu's Home link carries the inline padding of the labels it shares the header's row
  // with, at both widths — and the wide declaration is read **after** the phone's. Both halves
  // are load-bearing, and the second is the one that is easy to get wrong: `.site-nav a` sets the
  // whole `padding` shorthand on every link in the menu, and `.site-nav-home > a` is one class
  // and one type, the same specificity, so the last rule read wins. A `padding-inline` written
  // into the wide block *above* the base rule — which is where the labels' own wide
  // padding lives — is read first and then overwritten by that shorthand, and the link keeps its
  // phone padding at every width while the labels beside it open to 8 px. That is exactly what
  // this rule did until the review of 2026-10-21 caught it, and nothing held either declaration.
  const inlinePadding = (body) => {
    const own = /(?:^|;)padding-inline:([^;]+)/.exec(`;${body}`)
    if (own) return own[1].trim()
    const all = /(?:^|;)padding:([^;]+)/.exec(`;${body}`)
    if (!all) return null
    const parts = all[1].trim().split(/\s+/)
    return parts.length === 1 ? parts[0] : parts[1]
  }
  const homeRules = cssRules(css)
    .map((one, at) => ({ at, wide: (one.conditions ?? []).some((c) => /48rem/.test(c)), body: one.body ?? '', selector: one.selector }))
    .filter((one) => one.selector === '.site-nav-home>a')
  const summaryRules = cssRules(css)
    .map((one) => ({ wide: (one.conditions ?? []).some((c) => /48rem/.test(c)), body: one.body ?? '', selector: one.selector }))
    .filter((one) => one.selector === '.site-nav-group>details>summary')
  const labelAt = (isWide) => inlinePadding(summaryRules.find((one) => one.wide === isWide)?.body ?? '')
  if (homeRules.length === 0) {
    fail("the stylesheet gives the menu's Home link no padding of its own, so it is drawn with the 8 px a link inside a group's list gets and stands wider than the labels beside it in the header's row")
  }
  for (const isWide of [false, true]) {
    const where = isWide ? 'at a wide viewport' : 'at a phone'
    const home = homeRules.filter((one) => one.wide === isWide).at(-1)
    if (home === undefined) {
      fail(`the menu's Home link carries no inline padding ${where}, so it falls back to the whole \`padding\` shorthand \`.site-nav a\` sets and stands at a different width from the labels beside it in the header's row`)
      continue
    }
    const mine = inlinePadding(home.body)
    const label = labelAt(isWide)
    if (mine !== label) {
      fail(`the menu's Home link is drawn with an inline padding of ${JSON.stringify(mine)} ${where} where the labels beside it in the header's row get ${JSON.stringify(label)}: the row is measured as one row at one spacing, and a link that disagrees with the labels it shares it with is an item drawn at a width nobody measured`)
    }
  }
  const homeBase = homeRules.find((one) => !one.wide)
  const homeWide = homeRules.find((one) => one.wide)
  if (homeBase !== undefined && homeWide !== undefined && homeWide.at < homeBase.at) {
    fail("the wide viewport's inline padding for the menu's Home link is declared before the phone's, and `.site-nav a` above both sets the whole `padding` shorthand: at one class and one type the last rule read wins, so the wide declaration is dead code and the link keeps its phone padding at every width while the labels beside it open to 8 px")
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

/**
 * Today's stylesheet with **one named rule** rewritten — `selector` is a fragment of the rule's
 * selector, `edit` gets the rule's own text and has to change it. The fixture for a case about
 * one of several rules that declare the same thing, or about the selector itself: the
 * homepage's top-level blocks each write `padding:0 var(--space-md) var(--space-3xl)` — the same
 * declaration, in the rule of the file that renders each one — so a case about the benefits'
 * padding has nothing to tell it apart from the install's (`plant` takes the first or the last
 * occurrence of a fragment, and *which* rule that is depends on the order Astro emits the
 * components in, which is not something a case should rest on).
 */
function plantRule(selector, edit) {
  const css = TODAY()
  const at = css.indexOf(selector)
  assert.ok(at !== -1, `this fixture rewrites the rule whose selector carries ${JSON.stringify(selector)}, and the built stylesheet no longer carries it`)
  const end = css.indexOf('}', at)
  assert.ok(end !== -1, `the rule whose selector carries ${JSON.stringify(selector)} has no body in the built stylesheet`)
  const rule = css.slice(at, end + 1)
  const rewritten = edit(rule)
  assert.notEqual(rewritten, rule, `this fixture rewrites the rule whose selector carries ${JSON.stringify(selector)}, and the edit changed nothing`)
  return css.slice(0, at) + rewritten + css.slice(end + 1)
}

test('the layout rule passes on the built stylesheet, so each refusal below is the one edit it names', () => {
  assert.deepEqual(navLayoutFailures(TODAY()), [])
})

test('every layout refusal fires on the defect it names', () => {
  const cases = [
    // The row, and the labels in it.
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
      "the labels stacked instead of in the header's row",
      plant('.site-nav-groups{gap:var(--space-3xs);display:flex}', '.site-nav-groups{gap:var(--space-3xs)}'),
      /not laid out in the header's row/,
    ],
    [
      "a group's list left in the header's flow",
      // The panel's `transform-origin` sits in the same rule since the owner-approved motion
      // opportunities 2026-10-23 (the panel grows from its trigger).
      plant('background:var(--surface);transform-origin:top;position:absolute;top:100%;left:0;right:0}', 'background:var(--surface);transform-origin:top;top:100%;left:0;right:0}'),
      /in the header's flow/,
    ],
    [
      'a phone drawer that is a column under one label',
      plant('position:absolute;top:100%;left:0;right:0}', 'position:absolute;top:100%;left:auto;right:0}'),
      /not the header's width/,
    ],
    [
      "a rule that hides a group's list",
      plant('background:var(--surface);transform-origin:top;position:absolute', 'background:var(--surface);transform-origin:top;display:none;position:absolute'),
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
      TODAY().replaceAll('(width<=27rem)', '(width<=99rem)'),
      /no phone breakpoint/,
    ],
    // The Home item the owner's feedback of 2026-10-21 put in the row was paid for out of the
    // row's own spacing below the breakpoint, so both halves of that are held: the rule that
    // takes the gap between the wordmark and the menu to the phone's 4 px, and the one that
    // takes the items' own 4 px gap to none. Both stay with the wider cut.
    [
      "the row's spacing left at the gap the breakpoint found",
      plant('.site-header{gap:var(--space-3xs)}', ''),
      /leaves the header's row at the gap it has above the breakpoint/,
    ],
    [
      "the menu's items left 4 px apart below the breakpoint",
      plant('.site-nav-groups{gap:0}', ''),
      /leaves the menu's items 4 px apart/,
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
    // And the same hiding in the base rule, which is in force at every width: the phone block
    // is untouched here, so a check that read only inside it would pass this stylesheet while
    // the home link names nothing at 1280 px.
    [
      "the wordmark's text hidden at every width by the base rule",
      plant('.site-wordmark-name{text-overflow:ellipsis', '.site-wordmark-name{display:none;text-overflow:ellipsis'),
      /naming nothing/,
    ],
    [
      "the wordmark's text made invisible at every width by the base rule",
      plant('.site-wordmark-name{text-overflow:ellipsis', '.site-wordmark-name{visibility:hidden;text-overflow:ellipsis'),
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
      // `transform-origin` joined the rule with the owner-approved motion opportunities 2026-10-23.
      plant('border-radius:var(--radius-md);transform-origin:100% 0;left:auto;right:0}', 'border-radius:var(--radius-sm);transform-origin:100% 0;left:0;right:0}'),
      /hangs across the header/,
    ],
    [
      "a group's dropdown opening rightwards off the viewport",
      plant('border-radius:var(--radius-md);transform-origin:100% 0;left:auto;right:0}', 'border-radius:var(--radius-sm);transform-origin:100% 0;left:auto;right:auto}'),
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

/**
 * A CSS length as pixels: a token, a `rem`/`px` length, or a `calc(var(--x) * n)` — and a
 * `calc(var(--x) / n)`, which is how a length that is half of a token is written (the run's
 * connector is placed at half the marker's width, `left: calc(var(--space-lg) / 2)`), so that a
 * value the stylesheet writes in the reading's own vocabulary is one the reading can measure.
 */
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
  const divide = /^calc\(\s*var\(--([a-z0-9-]+)\)\s*\/\s*([\d.]+)\s*\)$/.exec(value)
  if (divide) return (tokens.get(divide[1]) ?? NaN) / Number(divide[2])
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

/** The font size a list is set in: what its `ch` lengths are measured against. The list's own
 *  rule when it declares one — read through the element's first class, so a list whose type is
 *  set by its own rule measures its `ch` cap against that size rather than the body's — the
 *  body's size otherwise, and 16 px when neither is declared. */
function elementFontPx(css, element) {
  const tokens = lengthTokens(css)
  const rules = allRules(css)
  const klass = (element.classes[0] ?? '').replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
  const own = rules
    .filter((r) => klass !== '' && new RegExp(`\\.${klass}(?![\\w-])`).test(r.selector))
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
 * the caller refuses rather than reads past. It takes the list's element (`listElement`), a
 * descriptor read out of the built page, because the cap it reads is `base.css`'s and reaches
 * every `ul`, `ol` and `dl` on the site.
 */
export function listMaxWidth(css, width, element) {
  const rules = allRules(css)
  const tokens = tokenText(css)
  const fontPx = elementFontPx(css, element)
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

// ---- How It Works: one left edge, and room under the title (review round 3, item 3d) ------
//
// The page is one picture and the run in words under it, and `main` gives its top-level blocks
// one gutter: the `h1`, the section the drawing lives in and the words section. What the review
// of round 3 found on the page's previous body was a block adding a gutter of its own on top of
// that one, so the detail of the picture was indented relative to the title and the picture
// above it — two left edges on one page, 24 px apart. The other half of the same finding is the
// gap between the title and the picture: `base.css` gives every `h1` 12 px under it, which is a
// heading's margin rather than the room a page's opening picture is met with.
//
// The same two claims are read here on the page the owner's draft of 2026-10-19 asks for, and
// the defect they refuse is the one the review measured: a block of the page that insets itself
// inside the gutter, and a title left sitting against the block under it. Both are read out of
// the built page rather than left to the eye — the inline component of every diagram block's
// padding comes out of the cascade at the desktop width, and the title's own rule is read for
// the room it declares under itself. The vertical rhythm between the picture and the words is
// not this check's business: that is a rhythm the owner's eye owns (70a-70c).
//
// **The bold redesign of 2026-10-20 moved the title into the page's opening** (the owner's
// approved board `website/design-ref/HowItWorksV3.dc.html`): the `h1` is the hero's headline,
// the diagram and its words are the blocks under it, and the room the title declares is what
// separates the headline from the lead under it. The claim and the room are the ones this check
// has always read — the class the page declares it on, and the two pixels it is at least — and
// what moved is which block follows the title.

/** The classes of the blocks the diagram is drawn in: the page's own, and no other page's. */
const RUN_BLOCKS = ['run-picture', 'run-diagram', 'run-words']

/**
 * Every way the How It Works page's blocks do not share `main`'s left edge, or its title sits
 * against the block under it, for one built page's stylesheet and markup. Returns nothing on
 * a page that carries no diagram, so it can be run over every built page.
 */
export function runDiagramFailures(css, html) {
  const out = []
  const classesOf = (el) => (el.attrs.get('class') ?? '').split(/\s+/).filter(Boolean)
  const doc = parse(html)
  const figure = [...walk(doc)].find((el) => el.name === 'figure' && classesOf(el).includes('run-diagram'))
  if (!figure) return out
  const title = [...walk(doc)].find((el) => classesOf(el).includes('page-title'))
  const rules = allRules(css)
  const tokens = lengthTokens(css)
  const declared = (klass, property) =>
    rules
      .filter((r) => new RegExp(`\\.${klass}(?![\\w-])`).test(r.selector) && appliesAt(80 * 16)(r))
      .map((r) => declarations(r)[property])
      .filter((value) => value !== undefined)
      .pop()

  // The gutter a block adds inside `main`'s own. Any of the four declarations can do it, and
  // the shorthand's inline half is the one this reads (`padding: 40px 0 64px` is the site's own
  // vertical-only shape and is not an inset).
  for (const klass of RUN_BLOCKS) {
    for (const property of ['padding', 'padding-inline', 'padding-left', 'padding-right']) {
      const value = declared(klass, property)
      if (value === undefined) continue
      const inline = /^padding-(left|right)$/.test(property) ? value : inlineToken(value)
      const inset = toPx(inline, tokens)
      if (Number.isNaN(inset)) {
        out.push(
          `the \`.${klass}\` block's inline padding cannot be read from \`${property}: ${value}\`, and an inset this check cannot turn into pixels is one nothing checked (\`NaN\` passes the comparison below) — teach \`website/test/base-styles.mjs\` the shape, or write the declaration in one it reads (review round 3, item 3d)`,
        )
      } else if (inset > 0) {
        out.push(
          `the \`.${klass}\` block insets its own content by ${Math.round(inset)} px inside the gutter \`main\` already gives it (\`${property}: ${value}\`), and the page's \`h1\` above it starts at that gutter: the block is ${Math.round(inset)} px right of the title, which is the second left edge the review of round 3 measured (review round 3, item 3d)`,
        )
      }
    }
  }

  if (!title) {
    out.push(
      "the How It Works page's `h1` carries no `page-title` class, so the room under it cannot be read: the title and the block under it are the page's opening, and what separates them is this page's own rule rather than a heading's default margin (review round 3, item 3d)",
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
      `the How It Works title has ${Math.round(room)} px under it, and the page's opening follows it: \`base.css\`'s heading margin is a heading's margin, and the title a reader meets first is followed by the page's own block at the room of a section opening — at least ${wanted} px (\`--space-lg\`) (review round 3, item 3d)`,
    )
  }
  return out
}

test('the picture and the words under it share the title’s left edge, and the title has room', () => {
  const failures = []
  let checked = 0
  for (const { rel, html } of builtPages()) {
    if (!html.includes('run-diagram')) continue
    checked += 1
    failures.push(...runDiagramFailures(pageCss(html), html).map((line) => `${rel}: ${line}`))
  }
  assert.ok(checked > 0, 'no built page carries the run diagram, and the owner\'s draft of 2026-10-19 puts it on How It Works')
  assert.deepEqual(failures, [], `the How It Works page has two left edges:\n  ${failures.join('\n  ')}`)
})

test('every run-diagram refusal fires on the defect it names', () => {
  const page = readFileSync(path.join(DIST, 'how-it-works', 'index.html'), 'utf8')
  const css = pageCss(page)
  /**
   * The page's own rule for a selector, as the built stylesheet writes it — Astro scopes a
   * component's rule with its own `[data-astro-cid-…]`, so the fixture has to replace the
   * declaration inside the rule the page carries rather than a rule written here.
   */
  const ruleText = (selector) => {
    const pattern = new RegExp(`(?:^|[},])\\s*(${selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')})(\\[[^\\]]*\\])?\\{([^}]*)\\}`)
    const match = pattern.exec(css)
    assert.ok(match, `the How It Works stylesheet carries no \`${selector}\` rule, and the cases below are about the page's own rules`)
    return match[0]
  }
  /** One declaration of that rule replaced, with the rest of the page's stylesheet kept. */
  const change = (selector, from, to) => {
    const text = ruleText(selector)
    assert.ok(text.includes(from), `\`${selector}\` no longer declares ${JSON.stringify(from)}, and this fixture replaces it`)
    return css.replace(text, text.replace(from, to))
  }
  const cases = [
    // The defect the review of round 3 measured, in the shape the page's own markup takes: a
    // block that gives itself a gutter inside `main`'s.
    [
      'the words section indented inside the page gutter',
      change('.run-words', 'margin-top:var(--space-2xl)', 'margin-top:var(--space-2xl);padding-left:var(--space-md)'),
      /insets its own content by 24 px/,
    ],
    // And the same defect written as a shorthand, which is how the page it was found on wrote
    // it: the drawing's own vertical room, rewritten as the shorthand that insets both edges.
    [
      'the drawing inset by a shorthand',
      change('.run-diagram', 'padding-block:var(--space-sm)', 'padding:var(--space-md)'),
      /insets its own content by 24 px/,
    ],
    // A title left with a heading's margin under it, which is where the page's own block under
    // it started 12 px below the headline.
    ['the title with a heading’s margin under it', change('.page-title', 'margin-bottom:var(--space-2xl)', 'margin-bottom:var(--space-xs)'), /px under it, and the page's opening/],
    // A page whose title lost the class the room is declared on: the rule is the page's, and a
    // title that no longer carries it is a title nothing measured. The headline carries the
    // page's class beside `base.css`'s display step, so the class is dropped from the list
    // rather than the whole attribute replaced.
    [
      'a title without the page’s own class',
      css,
      /carries no `page-title` class/,
      page.replace(/class="([^"]*)\bpage-title\b ?([^"]*)"/, 'class="$1$2"'),
    ],
  ]
  for (const [what, stylesheet, expected, html = page] of cases) {
    assert.deepEqual(
      runDiagramFailures(stylesheet, html).map((line) => (expected.test(line) ? 'fired' : line)),
      ['fired'],
      `${what}: the refusal did not fire, and this is what it said instead`,
    )
  }

  // The control: the page as it is built passes, and a page with no diagram is not its subject.
  assert.deepEqual(runDiagramFailures(css, page), [], 'the built page does not pass the shape its own cases are drawn from')
  assert.deepEqual(runDiagramFailures(pageCss(builtPages().find((entry) => entry.rel === 'index.html').html), builtPages().find((entry) => entry.rel === 'index.html').html), [], 'the homepage has no diagram, and this check has no subject there')
})

// ---- the run's arrows point the way the run travels (the drawing that took the flow's place)
//
// The retired gate this one stands for read the flow's four cards and the arrow each step left:
// a shaft drawn in the gap beside a card, placed on the side the next step is on, pointing the
// way the run travels — and it refused an arrow drawn the other way (`transform: rotate(90deg)`
// on an arrow drawn pointing down, which turned it back at the step before it and read the four
// steps in the opposite order). `ArchitectureDiagram.astro` is deleted with the flow (the
// owner's ruling of 2026-10-20) and the run is now a drawing: its four boxes are cards — a row
// of them in the wide drawing, a column in the tall one — with one arrow between each pair.
//
// Nothing in the repository read those six arrows. `test/how-it-works.mjs` reads the round trip
// of the fix (`[fix ─► review(2)]`, `docs/design/v1.md` §4): that the loop is drawn, that it
// joins the two stages it is between, and that its head's point leads its base. The straight
// arrows between the four boxes — the picture's biggest claim, and the one a reader follows to
// read the run at all — had no gate under them, so this reads them where the flow's gate read
// its own: which way they point, and which box each one lands on.
//
// Everything below is derived from the drawing itself rather than typed here: the cards' own
// rects, the axis they are laid out along (a row differs in `x` at one `y`, a column in `y` at
// one `x`), the gap between two of them, and the three corners of each head. Three claims, and
// each is the defect rather than a preference:
//
//   - **one arrow per gap, and it stays in its gap.** Each arrow is placed in the gap between
//     two cards that are beside each other — the shafts are drawn inset from the card's edge in
//     one drawing and flush with it in the other, so the reading is the gap the arrow's two
//     ends fall in rather than the pixel they start at. A gap with two arrows is a stage the
//     run takes twice; a gap with none is a stage it never reaches; an arrow that falls in no
//     gap reaches across a card;
//   - **each arrow runs the way the cards do.** The drawing's cards are the run's four boxes in
//     order, so the arrow between two of them leaves the earlier card and lands on the later
//     one — a head at the other end of the same shaft, or a shaft drawn from the later card
//     back to the earlier, is the same picture read backwards (`pull request → your agent`);
//   - **the head continues the shaft it ends.** Its base sits on the shaft's own axis and at
//     the shaft's end, its point is ahead of that base the way the shaft runs, and the head's
//     own axis is parallel to the shaft's — so a head drawn off to one side of the shaft, or
//     turned round on it, is refused. An arrowhead is where the arrow points.
//
// The two drawings are one picture at two scales, so they carry the same number of arrows; each
// drawing is read on its own axis, which is what makes this a reading of the drawing rather
// than of a width.

/**
 * One of the run's drawings, as the built page carries it: the `<svg>` tag's own attributes and
 * the markup inside it, which is what the readings below are made of. `null` on a page that
 * carries no such drawing — those pages are not this check's subject rather than failures of it.
 */
function runDrawing(html, klass) {
  const tag = new RegExp(`<svg\\b[^>]*class="[^"]*\\b${klass.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(?![-\\w])[^"]*"[^>]*>`).exec(html)
  if (tag === null) return null
  const open = html.indexOf(tag[0])
  const close = html.indexOf('</svg>', open)
  if (close === -1) return null
  return { attrs: tag[0], markup: html.slice(open + tag[0].length, close) }
}

/** Whether two coordinates are the same point of a drawing, to the tenth of a unit the
 *  component writes its own numbers to. */
const samePoint = (a, b) => Math.abs(a - b) <= 0.5

/** How far a point lies off the line through `from` and `to`, in units. */
function offAxis(point, from, to) {
  const length = Math.hypot(to.x - from.x, to.y - from.y)
  if (length === 0) return Infinity
  return Math.abs((to.x - from.x) * (from.y - point.y) - (from.x - point.x) * (to.y - from.y)) / length
}

/** The three corners of a path drawn as an arrowhead, as `{ apex, base }` — the point of the
 *  triangle is the corner opposite its longest side, which is the base the other two sit on —
 *  or `null` when the `d` is not three corners at all. */
function headCorners(d) {
  const points = [...d.matchAll(/(-?[\d.]+)[\s,]+(-?[\d.]+)/g)].map((m) => ({ x: Number(m[1]), y: Number(m[2]) }))
  if (points.length !== 3) return null
  const [a, b, c] = points
  const side = (p, q) => Math.hypot(p.x - q.x, p.y - q.y)
  const lengths = [side(b, c), side(a, c), side(a, b)]
  const opposite = lengths.indexOf(Math.max(...lengths))
  return { apex: points[opposite], base: points.filter((_, index) => index !== opposite) }
}

/** The run a drawing draws: the cards its markup puts down, the arrow shafts, and the heads,
 *  each head with its own `d`, so one this reader cannot read is reported rather than dropped. */
function runArrowsOf(drawing) {
  const cards = [...drawing.markup.matchAll(/<rect class="run-card"[^>]*?x="(-?[\d.]+)"[^>]*?y="(-?[\d.]+)"[^>]*?width="([\d.]+)"[^>]*?height="([\d.]+)"/g)].map((m) => ({
    x: Number(m[1]),
    y: Number(m[2]),
    w: Number(m[3]),
    h: Number(m[4]),
  }))
  const shafts = [...drawing.markup.matchAll(/<line class="run-arrow-line" x1="(-?[\d.]+)" y1="(-?[\d.]+)" x2="(-?[\d.]+)" y2="(-?[\d.]+)"/g)].map((m) => ({
    from: { x: Number(m[1]), y: Number(m[2]) },
    to: { x: Number(m[3]), y: Number(m[4]) },
  }))
  const heads = [...drawing.markup.matchAll(/<path class="run-arrow-head" d="([^"]+)"/g)].map((m) => ({ d: m[1], corners: headCorners(m[1]) }))
  return { cards, shafts, heads }
}

/**
 * Every way the run's drawing fails to point the way the run travels, for one built page's
 * markup. Returns nothing on a page that carries no drawing, so it can be run over every page.
 */
export function runArrowFailures(html) {
  if (runDrawing(html, 'run-wide') === null && runDrawing(html, 'run-tall') === null) return []
  const out = []
  const counts = []
  for (const klass of ['run-wide', 'run-tall']) {
    const drawing = runDrawing(html, klass)
    if (drawing === null) {
      out.push(
        `the page carries no \`${klass}\` drawing, and the run is drawn twice — a row of boxes for a desktop and a column for a phone — so both are the picture whose arrows this reads (the owner's ruling of 2026-10-20)`,
      )
      continue
    }
    const { cards, shafts, heads } = runArrowsOf(drawing)
    counts.push({ klass, shafts: shafts.length })
    if (cards.length < 2) {
      out.push(
        `the ${klass} drawing draws ${cards.length} box card(s), and the run is four boxes with an arrow between each pair: without two cards there is no gap for an arrow to travel, and the box it lands on cannot be read (the owner's ruling of 2026-10-20)`,
      )
      continue
    }
    // The axis the drawing lays its cards along, read off the cards themselves: a row differs
    // in `x` at one `y`, a column in `y` at one `x`. A drawing that does neither is refused
    // rather than read on a guess.
    const along =
      cards.every((card) => samePoint(card.y, cards[0].y))
        ? 'x'
        : cards.every((card) => samePoint(card.x, cards[0].x))
          ? 'y'
          : null
    if (along === null) {
      out.push(
        `the ${klass} drawing's cards are not laid out along one axis (their own rects are at ${cards.map((card) => `${card.x},${card.y}`).join(' / ')}), and the run's boxes are a row or a column: the arrows between them run whichever way the cards do, which is what says which box one lands on (the owner's ruling of 2026-10-20)`,
      )
      continue
    }
    const alongBox = along === 'x' ? 'w' : 'h'
    const ordered = [...cards].sort((a, b) => a[along] - b[along])
    /** The edges of the gap between each pair of cards beside each other, along the axis: the
     *  earlier card's far edge and the later card's near one, in the drawing's own units. */
    const gaps = ordered.slice(0, -1).map((card, index) => {
      const next = ordered[index + 1]
      return { start: card[along] + card[alongBox], end: next[along], at: index }
    })
    const inGap = (value, gap) => value >= gap.start - 0.5 && value <= gap.end + 0.5

    if (shafts.length !== gaps.length || heads.length !== gaps.length) {
      out.push(
        `the ${klass} drawing has ${cards.length} cards, ${shafts.length} \`run-arrow-line\` shafts and ${heads.length} \`run-arrow-head\` heads, and each gap between two of the run's boxes carries one arrow — a shaft and the head that ends it: a gap with two arrows is a stage the run takes twice, and a gap with none is a stage it never reaches (the retired arrows gate, changes spec 2026-10-18 item 4; the owner's ruling of 2026-10-20)`,
      )
      continue
    }

    const taken = new Map()
    let refused = false
    shafts.forEach((shaft, index) => {
      if (refused) return
      const head = heads[index]
      const tip = head.corners === null ? null : head.corners.apex[along]
      const matched = gaps.filter((gap) => inGap(shaft.from[along], gap) && tip !== null && samePoint(tip, gap.end))
      const where = `runs from ${along} ${shaft.from[along].toFixed(1)} to ${along} ${shaft.to[along].toFixed(1)}`
      if (matched.length !== 1) {
        out.push(
          `the ${klass} drawing's arrow ${index + 1} ${where} and its head's point is ${head.corners === null ? 'a path this check cannot read as three corners' : `at ${along} ${head.corners.apex[along].toFixed(1)}`}, and the arrow between two boxes of the run is drawn in the gap between them, arriving at the later one: an arrow that is in no gap reaches across a box, and one whose point is not at the far end of its gap points at the box the run came from (the retired arrows gate, changes spec 2026-10-18 item 4; the owner's ruling of 2026-10-20)`,
        )
        refused = true
        return
      }
      const gap = matched[0]
      if (taken.has(gap.at)) {
        out.push(
          `the ${klass} drawing's gap between the cards at ${along} ${gap.start.toFixed(1)} and ${along} ${gap.end.toFixed(1)} carries two arrows (${taken.get(gap.at) + 1} and ${index + 1}), and the run steps from one box to the next once: a second arrow in the same gap reads the run as taking that step twice (the retired arrows gate; the owner's ruling of 2026-10-20)`,
        )
        refused = true
        return
      }
      taken.set(gap.at, index)

      // The shaft runs the way the cards do: from the earlier card towards the later one, which
      // is the direction the gap's own two edges are in.
      if (shaft.to[along] - shaft.from[along] <= 0) {
        out.push(
          `the ${klass} drawing's arrow ${index + 1} ${where}, and the gap it is drawn in runs the other way — from the card at ${along} ${gap.start.toFixed(1)} to the next at ${along} ${gap.end.toFixed(1)}: an arrow drawn from the later box back to the earlier one reads the run backwards, which is the defect the retired gate refused when a rotation turned a step's arrow back at the step before it (the retired arrows gate, changes spec 2026-10-18 item 4; the owner's ruling of 2026-10-20)`,
        )
        refused = true
        return
      }

      // The head continues the shaft it ends: its base at the shaft's end and on the shaft's own
      // axis, its point ahead of the base the way the shaft runs, and the head's own axis
      // parallel to the shaft's.
      if (head.corners === null) {
        out.push(
          `the ${klass} drawing's arrow ${index + 1} ends in an arrowhead this check cannot read as three corners (${JSON.stringify(head.d)}), and an arrowhead is a point on a base: without the corners there is no way to say which way it points (the retired arrows gate; the owner's ruling of 2026-10-20)`,
        )
        refused = true
        return
      }
      const { apex, base } = head.corners
      const middle = { x: (base[0].x + base[1].x) / 2, y: (base[0].y + base[1].y) / 2 }
      const length = Math.hypot(shaft.to.x - shaft.from.x, shaft.to.y - shaft.from.y)
      const unit = { x: (shaft.to.x - shaft.from.x) / length, y: (shaft.to.y - shaft.from.y) / length }
      const reach = Math.hypot(apex.x - middle.x, apex.y - middle.y)
      const ahead = (apex.x - middle.x) * unit.x + (apex.y - middle.y) * unit.y
      const turn = reach === 0 ? Infinity : Math.abs((apex.x - middle.x) * unit.y - (apex.y - middle.y) * unit.x) / reach
      const off = Math.max(offAxis(middle, shaft.from, shaft.to), Math.hypot(middle.x - shaft.to.x, middle.y - shaft.to.y))
      if (off > 6 || ahead <= 0 || turn > 0.2) {
        out.push(
          `the ${klass} drawing's arrow ${index + 1} draws its head with its base ${off.toFixed(1)} units from the end of its own shaft (at ${middle.x.toFixed(1)},${middle.y.toFixed(1)}, the shaft ending at ${shaft.to.x.toFixed(1)},${shaft.to.y.toFixed(1)}), its point ${ahead > 0 ? `${ahead.toFixed(1)} units ahead of that base` : `${Math.abs(ahead).toFixed(1)} units behind it`}, and its own axis ${turn === Infinity ? 'of no length at all' : turn > 0.2 ? `turned ${(Math.asin(Math.min(1, turn)) * (180 / Math.PI)).toFixed(0)}° off the shaft's` : "along the shaft's"}: an arrowhead is the end of its shaft and points the way the shaft runs, and a head laid beside or across it points somewhere the run does not go (the retired arrows gate, changes spec 2026-10-18 item 4; the owner's ruling of 2026-10-20)`,
        )
        refused = true
      }
    })
  }

  // The two drawings are one picture at two scales: the same steps, and so the same number of
  // arrows between them.
  if (counts.length > 1 && new Set(counts.map(({ shafts }) => shafts)).size > 1) {
    out.push(
      `the page's drawings carry ${counts.map(({ klass, shafts }) => `${shafts} arrows in the ${klass} one`).join(' and ')}, and they are one picture at two scales: a run that steps between a different number of boxes on a phone is a second run (the owner's draft of 2026-10-19)`,
    )
  }
  return out
}

test('the run’s arrows leave each box and land on the next, pointing the way the run travels', () => {
  const failures = []
  let checked = 0
  for (const { rel, html } of builtPages()) {
    if (runDrawing(html, 'run-wide') === null) continue
    checked += 1
    failures.push(...runArrowFailures(html).map((line) => `${rel}: ${line}`))
  }
  assert.ok(checked > 0, 'no built page carries the run drawing, and the owner\'s draft of 2026-10-19 puts it on How It Works')
  assert.deepEqual(failures, [], `the run's arrows do not point the way the run travels:\n  ${failures.join('\n  ')}`)
})

test('every run-arrow refusal fires on the defect it names', () => {
  const page = readFileSync(path.join(DIST, 'how-it-works', 'index.html'), 'utf8')
  /** The shapes of one drawing's arrows, as the built page draws them: every case below is one
   *  edit to the page's own markup, so a component that drew its arrows differently would fail
   *  this fixture loudly rather than leave it passing by accident. */
  const shapes = (klass) => {
    const drawing = runDrawing(page, klass)
    assert.ok(drawing, `the built page carries no \`${klass}\` drawing, and these cases are about the drawing's own arrows`)
    return {
      markup: drawing.markup,
      shafts: [...drawing.markup.matchAll(/<line class="run-arrow-line"[^>]*\/>/g)].map((m) => m[0]),
      heads: [...drawing.markup.matchAll(/<path class="run-arrow-head" d="[^"]+"\s*\/>/g)].map((m) => m[0]),
    }
  }
  /** The corners of a head, as the triangle this check reads: the point and the base it is on. */
  const partsOf = (head) => headCorners(/d="([^"]+)"/.exec(head)[1])
  /** The same head, drawn with those corners. */
  const redraw = (head, corners) => head.replace(/d="[^"]+"/, `d="M ${corners.map((point) => `${point.x} ${point.y}`).join(' L ')} Z"`)
  const shift = (points, by) => points.map((point) => ({ x: point.x + by.x, y: point.y + by.y }))
  /** The same head turned round on its own base: its point mirrored to the other side, which is
   *  the retired gate's `rotate(90deg)` told in the drawing's own geometry. */
  const turnRound = ({ apex, base }) => {
    const middle = { x: (base[0].x + base[1].x) / 2, y: (base[0].y + base[1].y) / 2 }
    return [{ x: 2 * middle.x - apex.x, y: 2 * middle.y - apex.y }, base[0], base[1]]
  }
  /** An arrow moved along the drawing's own axis by `dx`, both of its shapes together. */
  const movedArrow = (shaft, head, dx) => [
    shaft.replace(/x1="(-?[\d.]+)"/, (m, x) => `x1="${Number(x) + dx}"`).replace(/x2="(-?[\d.]+)"/, (m, x) => `x2="${Number(x) + dx}"`),
    redraw(head, shift([partsOf(head).apex, ...partsOf(head).base], { x: dx, y: 0 })),
  ]

  const wide = shapes('run-wide')
  const tall = shapes('run-tall')
  const wideHead = partsOf(wide.heads[0])
  const tallHead = partsOf(tall.heads[0])
  const across = movedArrow(wide.shafts[0], wide.heads[0], 60)
  const cases = [
    // The head turned round on its own shaft: the arrow now points back at the box the run came
    // from, which is the defect the retired gate refused.
    [
      'the head turned round on its shaft',
      () => withDrawing('run-wide', (markup) => markup.replace(wide.heads[0], redraw(wide.heads[0], turnRound(wideHead)))),
      /points at the box the run came from/,
    ],
    // The same head with its base drawn past its point: the point trails the base on the shaft.
    [
      'the head’s base drawn past its point',
      () => withDrawing('run-wide', (markup) => markup.replace(wide.heads[0], redraw(wide.heads[0], [wideHead.apex, ...shift(wideHead.base, { x: 30, y: 0 })]))),
      /units behind it/,
    ],
    // A head drawn beside the shaft it ends rather than at the end of it.
    [
      'the head drawn beside its shaft',
      () => withDrawing('run-wide', (markup) => markup.replace(wide.heads[0], redraw(wide.heads[0], shift([wideHead.apex, ...wideHead.base], { x: 0, y: 20 })))),
      /draws its head with its base .* units from the end of its own shaft/,
    ],
    // A head this reader cannot read as a triangle at all: a point with no base.
    [
      'a head that is not three corners',
      () => withDrawing('run-wide', (markup) => markup.replace(wide.heads[0], redraw(wide.heads[0], [wideHead.apex, wideHead.base[0]]))),
      /cannot read as three corners/,
    ],
    // A shaft drawn the other way: from the later card back to the earlier one, which is the
    // same picture read backwards.
    [
      'a shaft drawn from the later box back',
      () => withDrawing('run-wide', (markup) => markup.replace(wide.shafts[0], wide.shafts[0].replace(/x1="(-?[\d.]+)"([^>]*)x2="(-?[\d.]+)"/, 'x1="$3"$2x2="$1"'))),
      /the gap it is drawn in runs the other way/,
    ],
    // Two arrows in one gap, and none in the next: the drawing still carries one arrow per pair
    // of cards, so only the gaps say that the run does not take one of its steps twice.
    [
      'two arrows in one gap',
      () => withDrawing('run-wide', (markup) => markup.replace(wide.shafts[1], wide.shafts[0]).replace(wide.heads[1], wide.heads[0])),
      /carries two arrows/,
    ],
    // An arrow taken away: a stage the run never reaches.
    [
      'an arrow missing from a gap',
      () => withDrawing('run-wide', (markup) => markup.replace(wide.shafts[1], '').replace(wide.heads[1], '')),
      /and each gap between two of the run's boxes carries one arrow/,
    ],
    // An arrow moved into the middle of a card: it is in no gap, so it reaches across one.
    [
      'an arrow drawn across a card',
      () => withDrawing('run-wide', (markup) => markup.replace(wide.shafts[0], across[0]).replace(wide.heads[0], across[1])),
      /reaches across a box/,
    ],
    // A card moved off the row: the drawing's boxes are no longer along one axis, so which box
    // an arrow lands on is not a fact the picture states.
    [
      'a card off the row',
      () => withDrawing('run-wide', (markup) => markup.replace(/(<rect class="run-card"[^>]*?y=")(-?[\d.]+)(")/, (m, before, y, after) => `${before}${Number(y) + 60}${after}`)),
      /not laid out along one axis/,
    ],
    // The tall drawing with an arrow taken away: the two drawings are one picture at two
    // scales, and the phone's is missing a step of the run.
    [
      'the two drawings with a different number of arrows',
      () => page.replace(tall.shafts[0], '').replace(tall.heads[0], ''),
      /one picture at two scales/,
    ],
    // The phone's drawing with its own head turned round, read on that drawing's own axis.
    [
      'the tall drawing’s head turned round',
      () => page.replace(tall.heads[0], redraw(tall.heads[0], turnRound(tallHead))),
      /points at the box the run came from/,
    ],
  ]
  /** The page with one drawing's markup replaced by a version of it. */
  const withDrawing = (klass, instead) => {
    const { markup } = shapes(klass)
    assert.ok(page.includes(markup), `the built page no longer carries the ${klass} drawing's markup, and these cases replace it`)
    return page.replace(markup, instead(markup))
  }
  for (const [what, instead, expected] of cases) {
    const planted = instead()
    assert.notEqual(planted, page, `${what} was not planted: the fixture's pattern did not match the built page`)
    const failures = runArrowFailures(planted)
    assert.ok(
      failures.some((line) => expected.test(line)),
      `${what}: the refusal did not fire, and this is what it said instead:\n  ${failures.join('\n  ') || '(nothing)'}`,
    )
  }

  // The control: the page as it is built passes, and a page with no drawing is not its subject.
  assert.deepEqual(runArrowFailures(page), [], 'the built page does not pass the reading its own cases are drawn from')
  const home = builtPages().find((entry) => entry.rel === 'index.html').html
  assert.deepEqual(runArrowFailures(home), [], 'the homepage draws no run, and this check has no subject there')
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
// **A rule is read against the element it claims to style, scope attribute and all** (review
// round 6). Astro gives every element a `data-astro-cid-…` for the component that rendered it
// and appends the page's own to the selectors the page's `<style>` declares, so a page's rule
// reaches the elements the page renders and no other. `.home-benefits` was declared on the
// homepage and rendered by `Benefits.astro`: the rule was dead, the section kept no gutter and
// no room below it, and this reading — which matched a rule to a block by class name — took the
// dead rule's `padding` for the block's and passed at all three widths. So a selector that
// names a scope the element does not carry is not in force, and a block the stylesheet names
// and no rule reaches is a refusal of its own, because the pair reading below can only see a
// block that declares a padding *somewhere*.
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
 * sentences a reader of the failure gets — and, before the pairs, every top-level block the
 * stylesheet names and no rule can reach (the round-6 reading: a block whose rule is scoped to
 * something the element is not). A page with no `main`, or with one block, has no pair to read
 * and is not a failure here.
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
   * The scope attributes an element carries: Astro writes one `data-astro-cid-…` per component
   * that rendered it, and appends the page's own to every compound selector the page's `<style>`
   * declares. So a selector that names a scope the element does not carry **cannot match the
   * element**, however its class name reads — and that is the shape the review of round 6 found:
   * `.home-benefits` was declared on the homepage and rendered by `Benefits.astro`, and a
   * reading that matched on the class alone took the dead rule's padding for the section's.
   */
  const scopesOf = (el) => new Set([...el.attrs.keys()].filter((name) => name.startsWith('data-astro-cid-')))

  /** True when one selector — not a list of them — can match an element carrying `scopes`: every
   *  `[data-astro-cid-…]` it names is one of the element's. A selector with none is unscoped
   *  (`base.css`, and the rules these cases plant) and matches anything. */
  const reaches = (selector, scopes) =>
    [...selector.matchAll(/\[(data-astro-cid-[^\]]+)\]/g)].every(([, scope]) => scopes.has(scope))

  /** True when a rule names one of `classes` in a selector that can reach an element carrying
   *  `scopes`. A selector list is read one selector at a time: it matches if *any* of them does. */
  const names = (rule, classes, scopes) =>
    rule.selector
      .split(',')
      .some(
        (part) =>
          classes.some((klass) => new RegExp(`\\.${klass}(?![\\w-])`).test(part)) && reaches(part, scopes),
      )

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
    const scopes = scopesOf(block)
    const inForce = rules.filter((r) => names(r, classes, scopes) && appliesAt(width)(r))
    let top
    let bottom
    let field = false
    for (const rule of inForce) {
      for (const [property, value] of cssDeclarations(rule.decls)) {
        if ((property === 'background' || property === 'background-color') && !/^(transparent|none)$/.test(value.trim())) field = true
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
      field,
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

  /**
   * **A block the stylesheet names and no rule reaches**, which is a failure of its own whatever
   * the pairs below read (review round 6). The pair reading can only see a block that declares a
   * vertical padding somewhere, and a block whose padding sits in a rule that cannot match it
   * declares nothing at all — `vertical` returns `null`, and `null` on either side of a pair
   * skips that pair **in silence**. That is how every gate passed over a section that padded
   * nothing on any width: the class was named by `index.astro`'s rule, the `<section>` carried
   * `Benefits.astro`'s scope, and a block the page means to style reads exactly like a block
   * that has no padding to give. The refusal names the rule, the scope it is written for and the
   * scope the element carries, because the two are one line apart in a source file and the fix
   * is to move the rule, not to change the class.
   */
  for (const block of blocks) {
    const classes = classesOf(block)
    if (classes.length === 0) continue
    const scopes = scopesOf(block)
    const namedBy = rules.filter((r) =>
      r.selector.split(',').some((part) => classes.some((klass) => new RegExp(`\\.${klass}(?![\\w-])`).test(part))),
    )
    if (namedBy.length === 0 || namedBy.some((r) => names(r, classes, scopes))) continue
    const selectors = [...new Set(namedBy.map((r) => r.selector))]
    const wanted = [
      ...new Set(
        selectors.flatMap((selector) => [...selector.matchAll(/\[(data-astro-cid-[^\]]+)\]/g)].map(([, scope]) => scope)),
      ),
    ]
    out.push(
      `the homepage's ${nameOf(block)} is named by the stylesheet (\`${selectors.join('`, `')}\`) and no selector of ${namedBy.length === 1 ? 'that rule' : 'those rules'} can reach the element: the element carries ${scopes.size ? [...scopes].join(', ') : 'no scope attribute'} where the rule is scoped to ${wanted.join(', ') || 'no scope at all'}. A rule the element cannot be matched by is a rule the block never gets, so the block keeps no gutter and no room below it — and the pair reading above skips it in silence, because a block with no padding of its own reads like a block that has no padding to give (review round 6: a page's scoped rule reaches the elements the page renders, and this block is rendered by a component that carries its own scope)`,
    )
  }

  for (let i = 1; i < blocks.length; i += 1) {
    const above = blocks[i - 1]
    const below = blocks[i]
    for (const width of RHYTHM_WIDTHS) {
      const a = vertical(above, width)
      const b = vertical(below, width)
      if (a === null || b === null) continue
      // **A field of colour pads inside itself** (the owner's ruling of 2026-10-23, the redesign's
      // Step 3: a section may be a whole field of colour). A block that paints its own ground
      // (the run band on the panel tone, the install section on the accent) carries its room
      // inside that ground, so its padding is part of the field a reader sees and not blank page
      // added to the gap: the gap between it and its neighbour is the neighbour's padding alone.
      if (a.field || b.field) continue
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
    // The homepage is the page whose `main` holds the hero (the owner's ruling of 2026-10-23
    // retired the slider this used to look for).
    if (!html.includes('class="hero"')) continue
    checked += 1
    failures.push(...homeRhythmFailures(pageCss(html), html).map((line) => `${rel}: ${line}`))
  }
  assert.ok(checked > 0, 'no built page renders the homepage’s blocks, so the rhythm of the page was measured nowhere')
  assert.deepEqual(failures, [], `the homepage pads one gap twice:\n  ${failures.join('\n  ')}`)
})

test('the rhythm refusal fires on the doubled gap, and the pairs that keep the rhythm pass', () => {
  const html = readFileSync(path.join(DIST, 'index.html'), 'utf8')
  const cases = [
    // The defect as it shipped (review round 5): a block's own `--space-lg` on top of the room
    // the block above it already carries. The blocks moved with the owner's ruling of
    // 2026-10-23 (the redesign's Step 3): every top-level block of the page writes
    // `padding:0 var(--frame-gutter) var(--space-4xl)` but the two fields of colour, so the
    // plant is made on the cost section, under the "what you get" block: 144 px of the block
    // above and the planted 32 px.
    [
      'a cost section padding the gap the block above it already pads',
      plantRule('.home-cost[data-astro-cid-', (rule) => rule.replace('padding:0 var(--frame-gutter) var(--space-4xl)', 'padding:var(--space-lg) var(--frame-gutter) var(--space-4xl)')),
      [/\.home-get ends with 144 px under it and \.home-cost opens with 32 px over it, so the gap between them is 176 px/],
    ],
    // And the same edit on the block under the hero, which is the pair the hero's own bottom
    // padding makes.
    [
      'the benefits padding the gap the hero above it already pads',
      plantRule('.home-benefits[data-astro-cid-', (rule) => rule.replace('padding:0 var(--frame-gutter) var(--space-4xl)', 'padding:var(--space-lg) var(--frame-gutter) var(--space-4xl)')),
      [/\.hero ends with 144 px under it and \.home-benefits opens with 32 px over it, so the gap between them is 176 px/],
    ],
    // **And what the reading does not refuse**: a field of colour padding its own top. The run
    // band paints the panel tone, so the room over its heading is inside the band, and the gap
    // above the band is the benefits' padding alone. A check that read every padding as blank
    // page would refuse the field's own room as a doubled gap.
    [
      'a field of colour padding its own top, which is inside its field and not a gap',
      `${TODAY()}.home-run{padding-top:var(--space-4xl)}`,
      [],
    ],
    // **The case the three widths are for.** The same doubled gap, written inside a rule that is
    // in force on a phone and nowhere else: at 1280 and 768 the page's rhythm is right and at
    // 375 the cost section pads a gap the block above it already pads. Two details of the
    // plant: `(width<=48rem)` would be in force at 768 as well, because 48 rem **is** 768 px, so
    // the band here is a phone's own 30 rem; and `appliesAt` reads ranges written with `>=`/`<=`
    // rather than `<`, so the plant is written in the vocabulary the reader has.
    [
      'a cost section that pads the gap only on a phone',
      `${TODAY()}@media screen and (width<=30rem){.home-cost{padding:var(--space-lg) var(--frame-gutter) var(--space-4xl)}}`,
      [/the gap between them is 176 px/],
      [375],
    ],
    // **The same doubled gap written as a longhand**, which is the shape the check could not
    // see before review round 5's follow-up: a rule that pads one edge in a rule of its own.
    [
      'a cost section that pads the gap with a longhand',
      `${TODAY()}.home-cost{padding-top:var(--space-lg)}`,
      [/the gap between them is 176 px/],
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

test('the rhythm reading refuses a block whose rule cannot reach it', () => {
  const html = readFileSync(path.join(DIST, 'index.html'), 'utf8')
  // The hero's scope is the other component's (the owner's ruling of 2026-10-23 retired the
  // slider this case used to borrow it from).
  const sliderScope = /class="hero"[^>]*data-astro-cid-(\w+)/.exec(html)?.[1]
  assert.ok(sliderScope, 'the built homepage no longer carries the hero’s scope on `.hero`, and this case re-scopes a rule to it')

  // **The defect of round 6, as it shipped**: a block declared by a component other than the one
  // that renders it, so its rules carry that component's scope (`[data-astro-cid-…]`), the
  // element carries its own, and the rules reach neither. The plant is that edit, made to a block
  // the reading walks and to every rule that names it — the install block, whose two rules are
  // both declared by `HomeInstall.astro`, re-scoped to the component that renders the slider.
  // **Every** rule naming the block has to move for the refusal to fire, which is the reading's
  // own rule and not a way around it: the refusal says the stylesheet names a block no rule
  // reaches, and one rule that still reaches it means the block is reached. Round 6 found the
  // shape on the benefits section when `index.astro` declared the rule; the draft of 2026-10-19
  // puts that section inside a panel, where the reading does not walk it —
  // `website/test/slider.mjs` holds the panel's own rule to the scope the section carries — so
  // the case is written on a block the reading can still see.
  const installScope = /class="home-install"[^>]*data-astro-cid-(\w+)/.exec(html)?.[1]
  assert.ok(installScope, 'the built homepage no longer carries a scope on `.home-install`, and this case re-scopes its rules away from it')
  assert.notEqual(installScope, sliderScope, 'the install block and the slider now carry one scope, and this case tells them apart')
  const rescoped = TODAY().replaceAll(`[data-astro-cid-${installScope}]`, `[data-astro-cid-${sliderScope}]`)
  assert.notEqual(rescoped, TODAY(), 'the built homepage’s stylesheet no longer names the install block’s scope, and this case re-scopes the rules that do')
  // And the other half of the same defect, from the element's side: the block with the scope
  // attribute its component gives it removed, which is a rule that names a scope nothing on the
  // element carries. Either way the block is one the stylesheet names and no rule reaches.
  const unscoped = html.replace(/<section class="home-install"([^>]*)>/, (tag, rest) => `<section class="home-install"${rest.replace(/ data-astro-cid-\w+/g, '')}>`)
  assert.notEqual(unscoped, html, 'the built homepage’s install block no longer carries a scope attribute, and this case takes one off')

  const cases = [
    ['every rule naming the block scoped to the component that renders the hero rather than to the one that renders the block', rescoped, html, new RegExp(`scoped to data-astro-cid-${sliderScope}`)],
    ['a block whose scope attribute is gone', TODAY(), unscoped, /the element carries no scope attribute/],
  ]
  for (const [what, css, page, expected] of cases) {
    const failures = homeRhythmFailures(css, page)
    assert.equal(failures.length, 1, `${what}: the reading refused ${failures.length} thing(s) where this case plants one:\n  ${failures.join('\n  ') || '(nothing)'}`)
    assert.match(failures[0], /^the homepage's \.home-install is named by the stylesheet/, `${what}: the refusal does not say which block it is about, or does not begin with it:\n  ${failures[0]}`)
    assert.match(failures[0], expected, `${what}: the refusal does not name the scopes it read:\n  ${failures[0]}`)
  }

  // And the two halves restore the reading: the defect is the scope mismatch and not the class,
  // so a rule that names the class *and* carries the element's scope is in force — the shipped
  // page passing is what the test above asserts.
  assert.deepEqual(homeRhythmFailures(TODAY(), html), [], 'the homepage as it ships carries a block its rules cannot reach')
})

// ---- the watch slot reserves no space until the image lands (review round 3, item 3b) ------

/**
 * Every way the `fugaro watch` slot breaks the two rules that make it a slot rather than a
 * placeholder. It is empty until a later task adds the screenshot — that is item 3's own
 * arrangement and PR #622's image — and the review of round 3 found the cost of getting it
 * wrong: about 140 px of blank page between the figure and the command bar under it. So:
 *
 *   - **nothing draws a box around an empty slot.** `:empty { display: none }` is what takes
 *     it out of the page while it holds nothing, and any rule that gives the slot itself a
 *     height, a padding, a border, a background or a margin is the blank rectangle coming
 *     back. Rules about what is *inside* the slot (a `>` in the selector) are the other half
 *     and are let through;
 *   - **the pictures that land in it are sized already.** The slot renders the site's shared
 *     slider — the same `ScreenshotSlider.astro` the console page's slider is, since
 *     2026-10-21 — and the rule that sizes a slide's picture is that component's own. This
 *     reader finds it by the class the slot's own `<img>` elements carry in the built page,
 *     so the slot's four pictures are held to the same reading whichever component draws them;
 *   - **and the picture that landed is sized so it scales.** The screenshot is a 1459 x 441
 *     logical capture of a terminal, and the owner's brief for putting it on the page asks for
 *     the page at 1280, 768 and 375 px: a fixed width is the picture running past a phone's
 *     edge — the column is 327 px at 375 — and a fixed height is the same picture squashed into
 *     a box that is not its shape. So the rule that sizes it takes its width from the column it
 *     is in (a percentage of it, or a percentage `max-width`) and lets the height follow the
 *     file's own ratio (`height: auto` or an `aspect-ratio`). This is what "it scales down
 *     without horizontal scroll" is made of, and it is the same reading `test/base-styles.mjs`
 *     takes everywhere else: the built stylesheet's own declarations.
 */
export function watchSlotFailures(css, html) {
  const out = []
  const slot = [...walk(parse(html))].find((el) => el.attrs.get('data-slot') === 'watch-screenshot')
  if (!slot) return out // the markup's slot is `test/home.mjs`'s subject, not this one's
  const own = allRules(css).filter((rule) => /\.watch-shot(?![\w-])/.test(rule.selector))
  const empty = own.find((rule) => rule.selector.includes(':empty'))
  if (empty === undefined || declarations(empty)['display'] !== 'none') {
    out.push(
      "no rule takes the watch screenshot's slot out of the page while it is empty (`.watch-shot:empty { display: none }`): the slot holds nothing until the image lands, and an empty element that still takes space is blank page between the conversation and the command bar (review round 3, item 3b)",
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
  // The pictures the slot renders, read from the markup rather than written here: since the
  // slot became the shared slider it holds a `<img>` per slide, and the rule that sizes them is
  // the slider's own (`ScreenshotSlider.astro`), which every slide of both pages' sliders
  // shares. The classes are read from the built page, so a reading that named one would not
  // follow the component if it renamed its own.
  const pictures = [...walk(slot)].filter((el) => el.name === 'img')
  const classes = [
    ...new Set(pictures.flatMap((el) => (el.attrs.get('class') ?? '').split(/\s+/).filter(Boolean))),
  ]
  const sizes = allRules(css).filter((rule) =>
    classes.some((name) => new RegExp(`\\.${name}(?![\\w-])`).test(rule.selector)),
  )
  if (pictures.length === 0 || sizes.length === 0) {
    out.push(
      `no rule sizes the pictures the watch screenshot's slot renders (${
        pictures.length === 0
          ? 'the slot holds no image at all'
          : `the slot's images carry ${classes.map((name) => `\`${name}\``).join(', ')} and no rule names ${classes.length === 1 ? 'it' : 'them'}`
      }): the slot renders the site's shared slider, whose own rule is what sizes a slide's picture, and a picture nothing sizes is a picture laid out at its file's own pixels (review round 3, item 3b)`,
    )
    return out
  }
  // The width the picture is laid out at, as the widest declaration any of those rules makes
  // about it: a percentage of the column is one that tracks the column at every viewport.
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
  // And none of the slider's own rules gives the picture a fixed length at all — the check above
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

test('the watch slot reserves no space until the image lands, and the image scales in it', () => {
  // The slot as it ships, first: the cases below are the real rules with one thing taken out,
  // and a case that never read the shipped stylesheet would pass on a page whose slot had no
  // rules at all. The homepage is the page the slot is on (the "what you get" section since the
  // owner's ruling of 2026-10-23), and `watchSlotFailures` returns early on a page
  // with no slot, so this assertion is also what holds the slot to the artifact.
  const home = readFileSync(path.join(DIST, 'index.html'), 'utf8')
  const css = pageCss(home)
  assert.deepEqual(watchSlotFailures(css, home), [], 'the watch slot as it ships breaks one of the rules the cases below are about')

  // The slot's own rule and the slider's rule that sizes the pictures in it, each planted: the
  // one that takes an empty slot out of the page, and the one the slot's `<img>` elements are
  // laid out by.
  const nothingPainted = css.replace(
    /\.watch-shot\[data-astro-cid-[a-z0-9]+\]:empty\{display:none\}/,
    '.watch-shot[data-astro-cid-6fbtmsuw]:empty{display:block}',
  )
  assert.notEqual(nothingPainted, css, "this fixture turns the slot's `:empty` rule into one that still draws it, and the built stylesheet no longer carries that rule")
  const noImageRule = css.replace(/\.screen-image\[data-astro-cid-[a-z0-9]+\]\{[^}]*\}/, '')
  assert.notEqual(noImageRule, css, "this fixture removes the rule that sizes the pictures in the slot, and the built stylesheet no longer carries one")
  // And the picture's own sizing, each planted the way the defect reads on the page: a width
  // in pixels is the screenshot wider than the phone's column, and a height in pixels is the
  // same screenshot squashed into a box that is not its shape.
  const fixedWidth = css.replace(/(\.screen-image\[data-astro-cid-[a-z0-9]+\]\{[^}]*?)width:100%/, '$1width:1459px')
  const fixedHeight = css.replace(/(\.screen-image\[data-astro-cid-[a-z0-9]+\]\{[^}]*?)height:auto/, '$1height:441px')
  assert.notEqual(fixedWidth, css, "this fixture fixes the watch screenshot's width in pixels, and the built stylesheet no longer sizes it with a percentage")
  assert.notEqual(fixedHeight, css, "this fixture fixes the watch screenshot's height in pixels, and the built stylesheet no longer lets it follow the file's ratio")
  // The same defect one breakpoint down: the base rule still tracks the column, and a media
  // query answers a phone with a fixed width instead — the shape a "just for small screens"
  // override takes, and the one a check that read only the declarations in front of it would
  // miss.
  const imgSelector = /(\.screen-image\[data-astro-cid-[a-z0-9]+\])/.exec(css)?.[1]
  assert.ok(imgSelector, "this fixture needs the rule that sizes the pictures in the watch slot, and the built stylesheet no longer carries one to read its selector from")
  const phoneWidth = `${css}\n@media (max-width:40rem){${imgSelector}{width:900px}}`
  const cases = [
    ['the slot left in the page while it is empty', nothingPainted, /takes the watch screenshot's slot out of the page/],
    ['the screenshot fixed in width inside a breakpoint', phoneWidth, /a length in units is a box the picture does not grow out of/],
    ['a box drawn around the empty slot', `${css}\n.watch-shot{min-height:140px}`, /gives the watch screenshot's slot a `min-height/],
    ['the rule that sizes the slot\'s pictures removed', noImageRule, /no rule sizes the pictures the watch screenshot's slot renders/],
    ['the screenshot given a fixed width', fixedWidth, /the picture is a 1459 px-wide capture of a terminal/],
    ['the screenshot given a fixed height', fixedHeight, /the picture's height has to follow the file's own ratio/],
  ]
  for (const [what, stylesheet, expected] of cases) {
    const failures = watchSlotFailures(stylesheet, home)
    assert.ok(
      failures.some((line) => expected.test(line)),
      `${what}: the refusal did not fire, and this is what it said instead:\n  ${failures.join('\n  ') || '(nothing)'}`,
    )
  }
})

// ---- the homepage's slider (retired) -------------------------------------------------------
//
// The readings of the homepage's three-panel slider (its step names, its fragment rules) are
// retired with the slider by the owner's ruling of 2026-10-23 (the redesign's Step 3, lever 1:
// the run is ONE section). The run section's list is held by `test/a11y.mjs`, and the shared
// screenshot slider by `test/console-screens.mjs`.

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
 * to put the control in a row *above* their command while the watch bar put it beside
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
  assert.equal(
    blocks.length,
    2,
    `the homepage carries ${blocks.length} command block(s) with a control and a command, and it renders two: the install block at the foot of the page and the watch block in the slider's third panel (the owner's approved draft of 2026-10-19; changes spec 2026-10-18, items 3 and 4)`,
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
    sideBySide.some((block) => block.klass === 'watch-block' && block.direction === 'row-reverse' && block.atStart),
    `the check finds no side-by-side command block on the homepage (it found ${JSON.stringify(sideBySide)}), so the cases below would pass without reading the block they are about (changes spec 2026-10-18, item 3)`,
  )
  assert.ok(
    sideBySide.some((block) => block.klass === 'command-block'),
    `the check does not read the install block's own markup (it found ${JSON.stringify(sideBySide)}), and item 3c is about the placement inside it`,
  )
  assert.deepEqual(copyControlPlacementFailures(css, home), [], 'the placement rule fails on the built homepage, so the case below is not proving what it names')

  // The defect as it shipped in the watch bar: a plain row, so the control — first in the
  // markup — is drawn at the left of the command. `.command-block`'s `row-reverse` is the
  // site's only one, and both of the homepage's blocks carry that class, so removing it leaves
  // every command block on the page a plain row and the check has to refuse all of them rather
  // than the watch block alone.
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
  // site — Getting Started's install cards, the slider's watch command — is reached by
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
// reachable, and the page carries exactly one `aria-current="page"` — on the menu link to the
// route being read, which since the owner's feedback of 2026-10-21 is the menu's own Home link
// on the home page and never the wordmark, which links the same route and carries no mark of
// its own), and once for the owner's brief of 2026-10-19 (item 1), which took the single
// "Menu" toggle away: the group labels are the menu, nothing stands in front of them,
// and a group's list of links is the `<details>`'s own child, because a closed `<details>` is
// the one thing that hides it. **The feedback of 2026-10-21 (item 2) put Home in front of the
// groups** — "I think we need a 'Home' as the first menu" — so the menu's list opens
// with a link to `/` named `Home`, on every viewport, and the groups follow it. **And the same
// day's feedback dropped the Reference group** — Console under *Product*, Docs and Changelog
// under *Project* — so the menu is two groups, and the rule below says so.
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

  // The way home on a phone: the wordmark's text is in its own element, because that element is
  // what the phone breakpoint takes out of the header's row (the owner's ruling of 2026-10-19:
  // the mark stays, the text goes). Loose text in the link is text nothing can hide.
  //
  // It is a rule of the chrome rather than of the menu, so it is read on both shapes of page: the
  // campaign mode's header is the wordmark and nothing else, and that one link is the page's whole
  // way home.
  const wordmarkRule = () => {
    const wordmark = all.find((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('site-wordmark'))
    const wordmarkName = wordmark && [...walk(wordmark)].find((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('site-wordmark-name'))
    if (!wordmark) {
      fail('the page carries no wordmark link to the home page')
    } else if (!wordmarkName || !textOf(wordmarkName).trim()) {
      fail("the wordmark's text is not in its own element (.site-wordmark-name), so the phone breakpoint has nothing to take out of the header's row and the row is wider than the viewport at 375 px")
    }
  }

  // **A campaign page carries no menu at all** (go-to-market plan, Task G2.2; spec §6). The four
  // landing pages `/save`, `/control`, `/scale` and `/crew` are one message for one audience, so
  // the site's nine sections are not a choice a visitor who arrived from an ad is offered: the
  // page's own calls to action are, and the footer keeps the three links a reader looks for.
  // The mode is read off the page's own chrome rather than off a list of routes, so a campaign
  // page that grew the menu back is refused here instead of passing because its path is listed —
  // and what this branch leaves behind is the menu's own checks, not the wordmark's: the header
  // is the wordmark alone, so that link is read here like any other page's.
  if (all.some((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('campaign-header'))) {
    if (navs.length !== 0) {
      return [`${rel}: the page is a campaign page and carries ${navs.length} section navs; a campaign page has none — the campaign mode's header is the wordmark alone (spec §6, G2.2)`]
    }
    wordmarkRule()
    return out
  }

  if (navs.length !== 1) return [`${rel}: the page carries ${navs.length} section navs; it has one`]
  const nav = navs[0]
  if (!(nav.attrs.get('aria-label') ?? '').trim()) fail('the section nav has an empty label: a screen reader lists landmarks by name')

  // The drawer the menu replaced. A toggle standing in front of the labels is the thing
  // the owner's brief of 2026-10-19 took away, and its markup coming back — in any of the three
  // parts it was made of — is a control between the reader and a section they can already see.
  for (const gone of ['site-nav-drawer', 'site-nav-toggle', 'site-nav-panel']) {
    if (all.some((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes(gone))) {
      fail(`the page still carries a .${gone}: the menu is the group labels in the header's row, with no toggle in front of them`)
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

  // The menu itself: the way home and then the groups, as a list directly in the nav.
  // Nothing between the nav and the items is what there is to say about a narrow viewport — the
  // items are the menu.
  //
  // **Home is the first item** (the owner's feedback of 2026-10-21: "I think we need a 'Home'
  // as the first menu"), and it is a link rather than a group: home is one page and has nothing
  // under it, so a disclosure in front of it would be a control that opens nothing. It is read
  // off the list's own children and not off the whole nav, because "first" is a fact about the
  // order a reader meets them in, which is the order they are drawn in.
  const items = [...walk(nav)]
    .filter((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('site-nav-groups'))
    .flatMap((list) => list.children.filter((child) => child.name !== undefined))
  const [home] = items
  const homeLink = (home?.attrs.get('class') ?? '').split(/\s+/).includes('site-nav-home')
    ? home.children.find((child) => child.name === 'a')
    : undefined
  if (homeLink === undefined) {
    fail("the menu does not open with a Home link (a .site-nav-home item holding an <a>): the owner's feedback of 2026-10-21 asks for Home as the first item of the menu, on a phone and on a wide viewport alike")
  } else {
    const href = homeLink.attrs.get('href') ?? ''
    if (href !== '/') fail(`the menu's Home link points at ${JSON.stringify(href)}, and the way home is "/" (the owner's feedback of 2026-10-21)`)
    const name = textOf(homeLink).trim()
    if (name !== 'Home') fail(`the menu's first item is named ${JSON.stringify(name)}, and the owner's feedback of 2026-10-21 names it "Home"`)
    if (home.children.some((child) => child.name === 'ul' || child.name === 'details')) {
      fail('the menu\'s Home item holds a list or a disclosure: home is one page, and the item in front of the groups is a link')
    }
  }
  const groups = [...walk(nav)].filter((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('site-nav-group'))
  if (items.length !== groups.length + 1) {
    fail(`the menu's list carries ${items.length} item(s): the Home link and then one item per group, which is ${groups.length + 1} for the ${groups.length} group(s) the menu draws — an item that is neither Home nor a group is a section the menu does not have (D182; the owner's feedback of 2026-10-21)`)
  }
  // Two groups, not three: D182 wrote three, and the owner's feedback of 2026-10-21 dropped the
  // Reference group — Console under Product, Docs and Changelog under Project.
  if (groups.length !== 2) fail(`the menu carries ${groups.length} groups: the nav is two groups (Product and Project) since the owner's feedback of 2026-10-21 dropped the Reference group (D182)`)
  // The count is not the contract on its own: the two groups are **named**, and each lists the
  // routes the owner's feedback of 2026-10-21 leaves it with. A rename in `NAV_GROUPS`
  // (Project → Company) keeps the count at two and would leave every gate green while D182's
  // amendment, the fixture below and check 70a's steps all tell the reader to look for
  // "Product" and "Project" — so the names and the routes are held here, with the count.
  // Why GCP moved from Product to Project, after Security, by the owner's explicit approval of
  // 2026-10-23 (its label and URL unchanged).
  const GROUPS_WANTED = [
    ['Product', ['/how-it-works/', '/getting-started/', '/caps-and-costs/', '/console/', '/mobile/']],
    ['Project', ['/docs/', '/security/', '/why-gcp/', '/faq/', '/community/', '/changelog/']],
  ]
  groups.forEach((group, at) => {
    const wanted = GROUPS_WANTED[at]
    if (wanted === undefined) return
    const [label, routes] = wanted
    const details = group.children.find((el) => el.name === 'details')
    const summary = details && details.children.find((el) => el.name === 'summary')
    const list = details && details.children.find((el) => el.name === 'ul')
    const drawn = summary ? textOf(summary).trim() : ''
    if (drawn !== label) {
      fail(`the menu's group ${at + 1} is labelled ${JSON.stringify(drawn)}, and the two groups are ${JSON.stringify(GROUPS_WANTED.map(([want]) => want))}: the owner's feedback of 2026-10-21 names them (Console under Product, the rest under Project — D182)`)
    }
    const hrefs = list ? [...walk(list)].filter((el) => el.name === 'a').map((el) => el.attrs.get('href') ?? '') : []
    if (hrefs.join(' ') !== routes.join(' ')) {
      fail(`the *${label}* group lists ${JSON.stringify(hrefs)}, and the owner's feedback of 2026-10-21 leaves it ${JSON.stringify(routes)} (D182)`)
    }
  })
  // One `name` across the groups makes them an exclusive set, without script: opening one
  // dropdown closes the one already open, so two never hang over each other on a wide row.
  const names = new Set(groups.flatMap((group) => group.children.filter((el) => el.name === 'details').map((el) => el.attrs.get('name') ?? '')))
  if (groups.length > 0 && (names.size !== 1 || names.has(''))) {
    fail(`the menu's groups carry the names ${JSON.stringify([...names])}: they share one non-empty name, so opening one dropdown closes the other`)
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
        fail("a group has no links: each of the nav's two groups (Product, Project) lists the sections it holds")
      }
    }
  }

  wordmarkRule()

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

// The two groups, as the layout writes them (the owner's feedback of 2026-10-21 dropped the
// Reference group: Console under *Product*, Docs and Changelog under *Project*), so a fixture
// is the real shape and not a smaller one the rule happens to accept.
const FIXTURE_GROUPS = [
  ['Product', [['/how-it-works/', 'How it works'], ['/getting-started/', 'Getting started'], ['/caps-and-costs/', 'Caps and costs'], ['/console/', 'Console'], ['/mobile/', 'Mobile']]],
  ['Project', [['/docs/', 'Docs'], ['/security/', 'Security'], ['/why-gcp/', 'Why GCP'], ['/faq/', 'FAQ'], ['/community/', 'Community'], ['/changelog/', 'Changelog']]],
]

// The wordmark, as the layout writes it: the logo is decoration beside the name, and the name
// is the element the phone breakpoint takes out of the row.
const FIXTURE_WORDMARK =
  '<a class="site-wordmark" href="/" rel="home"><img src="/logo.png" alt="" width="47" height="32" /><span class="site-wordmark-name">Fugaro</span></a>'

function fixtureNav({ label = 'Sections', current = '/docs/', home = { href: '/', name: 'Home' }, homeFirst = true } = {}) {
  const groups = FIXTURE_GROUPS.map(([group, items]) => {
    const links = items
      .map(([href, text]) => `<li><a href="${href}"${href === current ? ' aria-current="page"' : ''}>${text}</a></li>`)
      .join('')
    return `<li class="site-nav-group"><details name="site-nav-group"><summary>${group}</summary><ul>${links}</ul></details></li>`
  }).join('')
  // The menu's first item is the way home (the owner's feedback of 2026-10-21), and it is the
  // item marked current on the home page: `current: '/'` marks the Home link, which is the one
  // link on the page to the route being read.
  const wayHome =
    home === null ? '' : `<li class="site-nav-home"><a href="${home.href}"${current === '/' ? ' aria-current="page"' : ''}>${home.name}</a></li>`
  return `<nav class="site-nav" aria-label="${label}"><ul class="site-nav-groups">${homeFirst ? wayHome + groups : groups + wayHome}</ul></nav>`
}

function fixturePage({ nav = fixtureNav(), wordmark = FIXTURE_WORDMARK } = {}) {
  return `<!DOCTYPE html><html lang="en"><head><title>t</title></head><body><header class="site-header">${wordmark}${nav}</header></body></html>`
}

function navRefusals(html, rel = 'docs/index.html') {
  return navFailures(html, rel)
}

// A campaign page in the shape the layout's campaign mode writes it (G2.2): the chrome class
// and the wordmark, and no menu. The refusals below grow the menu back onto it and take the
// wordmark out of it.
const CAMPAIGN_HEADER = `<header class="campaign-header">${FIXTURE_WORDMARK}</header>`
const campaignPage = (header = CAMPAIGN_HEADER) => `<!DOCTYPE html><html lang="en"><head><title>t</title></head><body>${header}</body></html>`

test('the menu passes its own rule, so each refusal below is the one thing it breaks', () => {
  assert.deepEqual(navRefusals(fixturePage()), [])
  // The home page: the menu's own Home link is the one element marked current, and the wordmark
  // — the other link to `/` — carries no mark of its own (the owner's feedback of 2026-10-21).
  assert.deepEqual(navRefusals(fixturePage({ nav: fixtureNav({ current: '/' }) }), 'index.html'), [])
  // A campaign page is the second shape that passes, and it passes by having no menu and by
  // carrying the wordmark that header is made of: the page is one message for one audience and
  // its chrome is the wordmark alone (spec §6, G2.2).
  assert.deepEqual(navRefusals(campaignPage(), 'save/index.html'), [])
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
    // The menu's first item: the owner's feedback of 2026-10-21 asks for Home at the front of
    // it, so a menu without it, a menu that has it last, a Home link pointing somewhere else and
    // a Home link named something else are each refused.
    ['a menu with no Home item at all', fixturePage({ nav: fixtureNav({ home: null }) }), /does not open with a Home link/],
    ['the Home item left at the end of the menu', fixturePage({ nav: fixtureNav({ homeFirst: false }) }), /does not open with a Home link/],
    ['a Home link that points elsewhere', fixturePage({ nav: fixtureNav({ home: { href: '/docs/', name: 'Home' } }) }), /Home link points at "\/docs\/"/],
    ['a first item named something else', fixturePage({ nav: fixtureNav({ home: { href: '/', name: 'Start' } }) }), /first item is named "Start"/],
    [
      'a Home item holding a list of its own',
      fixturePage({ nav: fixtureNav().replace('<a href="/">Home</a>', '<a href="/">Home</a><ul><li><a href="/faq/">FAQ</a></li></ul>') }),
      /holds a list or a disclosure/,
    ],
    [
      'an item that is neither Home nor a group',
      fixturePage({ nav: fixtureNav().replace('</ul></nav>', '<li class="site-nav-extra"><a href="/privacy/">Privacy</a></li></ul></nav>') }),
      /the menu's list carries 4 item\(s\)/,
    ],
    // The two groups are named and hold the owner's feedback's links, so a rename and a route
    // that leaves its group are each refused even though the count is still two.
    ['a group renamed', fixturePage({ nav: fixtureNav().replace('<summary>Project</summary>', '<summary>Company</summary>') }), /group 2 is labelled "Company"/],
    [
      'a route dropped out of its group',
      fixturePage({ nav: fixtureNav().replace('<li><a href="/changelog/">Changelog</a></li>', '') }),
      /the \*Project\* group lists/,
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
    [
      'a campaign page that grew the menu back',
      campaignPage(`<header class="campaign-header">${FIXTURE_WORDMARK}${fixtureNav()}</header>`),
      /is a campaign page and carries 1 section navs/,
    ],
    // The wordmark is the campaign header's whole content, so the two defects a menu page is
    // refused for are refused here too: the campaign branch leaves the menu's checks behind, not
    // this one (G2.2).
    ['a campaign page whose header carries no wordmark', campaignPage('<header class="campaign-header"></header>'), /no wordmark link to the home page/],
    [
      "a campaign page whose wordmark text is loose in the link",
      campaignPage('<header class="campaign-header"><a class="site-wordmark" href="/" rel="home">Fugaro</a></header>'),
      /not in its own element/,
    ],
  ]
  for (const [what, html, expected] of cases) {
    const failures = navRefusals(html)
    assert.ok(failures.some((f) => expected.test(f)), `${what} was not refused (${expected}); the check said:\n  ${failures.join('\n  ') || '(nothing)'}`)
  }
})

test('the menu rule holds the home page to its Home link as the one current mark', () => {
  // The home page's `/` link is the menu's first item since the owner's feedback of 2026-10-21,
  // so that link is what the rule marks: with no mark on the page at all, and with the mark on
  // something else, it refuses.
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
  // And the wordmark may not carry the mark as well: two links to `/` marked current is two
  // claims about which page is being read, which is the refusal the plant below names.
  assert.ok(
    navRefusals(
      fixturePage({
        nav: fixtureNav({ current: '/' }),
        wordmark: FIXTURE_WORDMARK.replace('rel="home"', 'rel="home" aria-current="page"'),
      }),
      'index.html',
    ).some((f) => /marks 2 elements/.test(f)),
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

// The copy confirmation is drawn over its button, out of the row (the owner's preview feedback
// on #1003, 2026-10-24): in the flow it took width from the command, wrapped above the button
// and moved the block each time a command was copied.
export function copyStatusFailures(css) {
  const out = []
  const last = (selector, property) =>
    allRules(css)
      .filter((rule) => rule.selector.split(',').some((part) => part.trim() === selector))
      .map(declarations)
      .map((d) => d[property])
      .filter(Boolean)
      .pop() ?? ''
  if (last('.copy-status', 'position') !== 'absolute') out.push('the copy confirmation is in the row, so showing it takes width from the command and moves the block')
  if (!['relative', 'absolute'].includes(last('.command-controls', 'position'))) out.push('the control box is not positioned, so the confirmation is not anchored to its button')
  if (last('.copy-status:empty', 'opacity') !== '0') out.push('an empty confirmation is drawn, and it covers the button it sits on')
  return out
}

test('the copy confirmation sits over its button and never moves the command', () => {
  const css = TODAY()
  assert.deepEqual(copyStatusFailures(css), [], copyStatusFailures(css).join('\n'))
  const inFlow = css.replace(/(\.copy-status\{[^}]*?)position:absolute;?/, '$1')
  assert.notEqual(inFlow, css, 'this fixture removes the confirmation\'s positioning, and the built stylesheet no longer carries it')
  assert.ok(copyStatusFailures(inFlow).some((line) => /takes width from the command/.test(line)), 'a confirmation in the row was not refused')
})

// ---- the campaign pages' content lands on the chrome's left edge (G2.2) --------------------
//
// The four campaign pages are a run of full-width bands — the spec's five sections and the
// screens band of the owner's feedback of 2026-10-30 (D412's finding 3) — each padding its own
// content onto the
// page's one pair of margins with `--frame-gutter`, the expression the site's own header and footer
// carry, so the headline starts on the wordmark's left edge and the two margins are equal. That
// holds only while `main` contributes nothing: the gutter is a percentage of the box it is used
// in, so a `main` that pads the bands or caps their width resolves it against a box that is not
// the page and every band's content lands one gutter step right of the chrome — 16 px on a phone
// and 24 px at 1280, which is the second left edge the review of round 3 measured on How It
// Works. It is read the way that one is: the pixels the chrome's edge is at and the pixels a
// band's content is at, at a phone and at a desktop width, out of the built page's own
// stylesheet rather than left to the eye.
//
// **Both halves of the pair pass, and the pair in force at once is what is refused.** `main`
// dropped with the bands carrying the gutter is what the site writes; `main` kept with the bands
// carrying none is the other shape that puts their content on the chrome's edge. A page where
// both are in force is the defect.

/** The bands a campaign page is built from — the spec's five sections and the screens band the
 *  owner's feedback of 2026-10-30 (D412's finding 3) put between the steps and the closing
 *  field — and
 *  the two chrome blocks its mode draws. The list is in the order a reader meets them, which is
 *  what the spacing case below reads the gaps between; every one of them is a full-width band
 *  that pads its own content onto the page's one gutter. */
const CAMPAIGN_BLOCKS = ['campaign-hero', 'campaign-proof', 'campaign-steps', 'campaign-social-proof', 'campaign-screens', 'campaign-calls']
const CAMPAIGN_CHROME = ['campaign-header', 'campaign-footer']

/** The two widths the reading is taken at: a phone, and a desktop. The campaign mode's own
 * breakpoint is 48 rem, so the two sit on either side of it and each shape is read. */
const EDGE_WIDTHS = [375, 1280]

/**
 * A selector list split on its own commas. The commas inside `:has(…)` separate that
 * pseudo-class's arguments rather than the rule's selectors, and the rule this reading is drawn
 * to — `main:has(> .hero, > [data-security-page], > .campaign-hero)` — is three arguments and
 * one selector.
 */
function selectorParts(selector) {
  const parts = []
  let depth = 0
  let start = 0
  for (let at = 0; at < selector.length; at += 1) {
    const char = selector[at]
    if (char === '(') depth += 1
    else if (char === ')') depth -= 1
    else if (char === ',' && depth === 0) {
      parts.push(selector.slice(start, at).trim())
      start = at + 1
    }
  }
  parts.push(selector.slice(start).trim())
  return parts.filter(Boolean)
}

/**
 * A gutter declaration as pixels, resolved against the box that carries it. The site writes it
 * as `max(var(--space-sm), calc((100% - var(--page-max)) / 2 + var(--space-sm)))`, redefines it
 * at the wide breakpoint with `--space-md`, and a band writes `var(--frame-gutter)`: the token
 * in force at a width is the one read, and the box width is the caller's, because `100%` is the
 * width of whichever box the value is used in. A shape this reader does not know is `NaN`, which
 * the caller refuses rather than compares.
 */
function gutterPx(css, value, boxWidth, width, tokens) {
  const shape =
    /^max\(\s*var\(--([a-z0-9-]+)\)\s*,\s*calc\(\s*\(100%\s*-\s*var\(--page-max\)\)\s*\/\s*2\s*\+\s*var\(--([a-z0-9-]+)\)\s*\)\s*\)$/
  const resolve = (text) => {
    const m = shape.exec((text ?? '').trim())
    if (m === null) return NaN
    const floor = tokens.get(m[1])
    const margin = tokens.get(m[2])
    const page = tokens.get('page-max')
    if ([floor, margin, page].some((px) => px === undefined)) return NaN
    return Math.max(floor, (boxWidth - page) / 2 + margin)
  }
  if ((value ?? '').trim() !== 'var(--frame-gutter)') return resolve(value)
  const declared = allRules(css)
    .filter((rule) => selectorParts(rule.selector).some((part) => [':root', 'html'].includes(part)) && appliesAt(width)(rule))
    .map((rule) => declarations(rule)['--frame-gutter'])
    .filter((text) => text !== undefined)
    .pop()
  return resolve(declared)
}

/**
 * Every way a campaign page's content does not land on the chrome's left edge, for one built
 * page's stylesheet and markup. Returns nothing on a page that carries no campaign section, so
 * it can be run over every built page.
 */
export function campaignLeftEdgeFailures(css, html) {
  const out = []
  const classesOf = (el) => (el.attrs.get('class') ?? '').split(/\s+/).filter(Boolean)
  const doc = parse(html)
  const bands = [...walk(doc)].filter((el) => classesOf(el).some((name) => CAMPAIGN_BLOCKS.includes(name)))
  if (bands.length === 0) return out

  const rules = allRules(css)
  const tokens = lengthTokens(css)
  const declared = (klass, property, width) =>
    rules
      .filter((rule) => new RegExp(`\\.${klass}(?![\\w-])`).test(rule.selector) && appliesAt(width)(rule))
      .map((rule) => declarations(rule)[property])
      .filter((value) => value !== undefined)
      .pop()

  const main = [...walk(doc)].find((el) => el.name === 'main')
  if (main === undefined) return ['the campaign page carries no <main>, so there is no box its bands pad inside (G2.2)']
  const held = new Set(main.children.filter((child) => child.name !== undefined).flatMap(classesOf))
  // A `main:has(> .x)` rule is in force only on a page whose `main` holds an `.x`: the campaign's
  // rule matches here and the homepage's hero rule does not, which is the whole reason this
  // reading is taken per built page rather than off the stylesheet alone.
  const matches = (part) => {
    if (part === 'main') return true
    const has = /^main:has\((.+)\)$/.exec(part)
    return has !== null && has[1].split(',').some((condition) => held.has(condition.trim().replace(/^>\s*/, '').replace(/^\./, '')))
  }
  const mainRules = rules
    .map((rule) => ({ rule, parts: selectorParts(rule.selector).filter(matches) }))
    .filter((entry) => entry.parts.length > 0)
    .map((entry) => ({ ...entry.rule, specific: entry.parts.some((part) => part !== 'main') }))

  // What `main`'s own box is at a width: a `:has()` selector is the more specific of the two
  // ranks, so its declarations win, and within a rank the last applicable declaration is the one
  // a browser applies.
  const mainAt = (width, property) => {
    let value
    for (const rank of [false, true]) {
      for (const rule of mainRules) {
        if (rule.specific !== rank || !appliesAt(width)(rule)) continue
        const text = declarations(rule)[property]
        if (text !== undefined) value = text
      }
    }
    return value
  }
  const sidePx = (value, boxWidth, width) => {
    const gutter = gutterPx(css, value, boxWidth, width, tokens)
    return Number.isFinite(gutter) ? gutter : toPx(value, tokens)
  }

  for (const width of EDGE_WIDTHS) {
    const chrome = gutterPx(css, declared('site-header', 'padding-inline', width), width, width, tokens)
    const mainInline = mainAt(width, 'padding-inline') ?? inlineToken(mainAt(width, 'padding') ?? '0')
    const inset = toPx(mainInline, tokens)
    const ceiling = mainAt(width, 'max-width')
    const box = Math.min(width, ceiling === undefined || ceiling.trim() === 'none' ? Infinity : toPx(ceiling, tokens))
    if (![chrome, box, inset].every(Number.isFinite)) {
      out.push(
        `the campaign page's left edge at ${width} px cannot be read out of its stylesheet: the chrome's gutter is \`${declared('site-header', 'padding-inline', width) ?? '(nothing declared)'}\`, \`main\`'s inline room is \`${mainInline}\` and its ceiling is \`${ceiling ?? '(nothing declared)'}\`, and an edge this check cannot turn into pixels is one nothing measured — write the gutter in the shape the site writes it (G2.2)`,
      )
      continue
    }
    const content = box - 2 * inset
    const mainLeft = (width - box) / 2 + inset

    for (const klass of CAMPAIGN_CHROME) {
      const value = declared(klass, 'padding-inline', width)
      if (value === undefined) continue
      const edge = sidePx(value, width, width)
      if (!Number.isFinite(edge) || Math.abs(edge - chrome) > 0.5) {
        out.push(
          `at ${width} px the campaign chrome \`.${klass}\` pads its content to ${Number.isFinite(edge) ? Math.round(edge) : value} px and the site's own header pads to ${Math.round(chrome)} px: a campaign page keeps the site's chrome, so its wordmark and its footer links start where the site's header's own content does (G2.2)`,
        )
      }
    }

    for (const klass of CAMPAIGN_BLOCKS) {
      if (!bands.some((el) => classesOf(el).includes(klass))) continue
      const inline = declared(klass, 'padding-inline', width) ?? inlineToken(declared(klass, 'padding', width) ?? '0')
      const room = sidePx(inline, content, width)
      if (!Number.isFinite(room)) {
        out.push(`at ${width} px the \`.${klass}\` band's inline room cannot be read from \`${inline}\`, and a room this check cannot turn into pixels is one nothing measured (G2.2)`)
        continue
      }
      if (Math.abs(mainLeft + room - chrome) > 0.5) {
        out.push(
          `at ${width} px \`main\` starts the campaign bands at ${Math.round(mainLeft)} px and \`.${klass}\` pads ${Math.round(room)} px more, so their content lands at ${Math.round(mainLeft + room)} px while the chrome is at ${Math.round(chrome)} px: the bands are as wide as the page and carry the page's one gutter, so \`main\` gives them neither a gutter nor a ceiling of its own — the two in force at once is the second left edge (G2.2)`,
        )
      }
    }
  }
  return out
}

test('the campaign pages put their content on the chrome’s left edge', () => {
  // The page's own markup, not its stylesheet: every page carries `.campaign-hero` in the CSS
  // now — `base.css` names it in the rule that drops `main`'s gutter (`main:has(> .campaign-hero)`)
  // — and the four pages are the ones that render the section.
  const pages = builtPages().filter(({ html }) =>
    [...walk(parse(html))].some((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('campaign-hero')),
  )
  const seen = pages.map(({ rel }) => rel)
  assert.equal(seen.length, 4, `the build produced ${seen.length} campaign page(s) — ${seen.join(', ') || '(none)'} — and the spec's four paths are four (G2.2)`)
  const failures = pages.flatMap(({ rel, html }) => campaignLeftEdgeFailures(pageCss(html), html).map((line) => `${rel}: ${line}`))
  assert.deepEqual(failures, [], `a campaign page has two left edges:\n  ${failures.join('\n  ')}`)
})

test('the campaign left-edge refusal fires on the two in force at once, and passes on either half alone', () => {
  const page = readFileSync(path.join(DIST, 'save', 'index.html'), 'utf8')
  const css = pageCss(page)
  assert.deepEqual(campaignLeftEdgeFailures(css, page), [], `the built campaign page does not pass the shape its own cases are drawn from:\n  ${campaignLeftEdgeFailures(css, page).join('\n  ')}`)

  // The defect as it shipped before this check existed: `main` keeps the gutter and the ceiling
  // the bare rule gives it, so each band's `--frame-gutter` resolves against a narrower box.
  const before = css.replace(/main:has\([^)]*campaign-hero[^)]*\)\{[^}]*\}/g, '')
  assert.notEqual(before, css, 'this fixture removes the campaign `main` rule, and the built stylesheet no longer carries it')
  const refused = campaignLeftEdgeFailures(before, page)
  assert.ok(
    refused.some((line) => /at 1280 px `main` starts the campaign bands at 56 px/.test(line)) &&
      refused.some((line) => /at 375 px `main` starts the campaign bands at 16 px/.test(line)),
    `the second left edge at both widths was not refused; the check said:\n  ${refused.join('\n  ') || '(nothing)'}`,
  )

  // The other half of the pair: `main` keeps its own gutter and its ceiling, and the bands carry
  // none of their own. It is the alternative the finding names, and it passes — what is refused
  // is the two in force at once, not either of them.
  const unguttered = CAMPAIGN_BLOCKS.reduce(
    (text, klass) => text.replace(new RegExp(`\\.${klass}\\[[^}]*\\}`), (rule) => rule.replace(/padding-inline:var\(--frame-gutter\);?/, '')),
    before,
  )
  assert.notEqual(unguttered, before, 'this fixture strips the bands’ own gutter, and the built stylesheet no longer carries one')
  assert.deepEqual(
    campaignLeftEdgeFailures(unguttered, page),
    [],
    `a page whose bands carry no gutter inside \`main\`'s own has the one left edge too:\n  ${campaignLeftEdgeFailures(unguttered, page).join('\n  ')}`,
  )
})

// ---- the campaign pages' own design pass (the owner's feedback of 2026-10-30) ---------------
//
// The four campaign pages are one template and one mode of the layout, and the owner read them
// on the live preview at 1440, 768 and 390 px. Three of the five findings are arithmetic a
// browser shows and no browser is needed to check:
//
//   1. **The headline was set for a band, not for a column.** `.display-xl` is
//      `clamp(44px, 7.2vw, 104px)` — 104 px at 1440 — and the campaign hero put it in a copy
//      column beside the picture, so the longest headline (48 characters) wrapped to five lines
//      at 768, where the two-column hero starts and the copy column is at its narrowest. The
//      headline now takes the whole width of the band and is capped, and the case below reads
//      the two lines at each of the three widths the owner measured at, with the site's own
//      advance table (`textWidth`) and the band's own content box. The second case reads the
//      other end of the same rule: the cap alone would let the headline shrink under the type
//      around it, so the headline is held above the page's every other heading and above the
//      body's larger step at each width.
//   2. **The page was 1.8 screens with bands of nothing between the sections.** The gaps the
//      owner asked for are 120–240 px on a desktop, and no single band may pad more than half a
//      screen of nothing: both are read below, out of the cascade, because a band's own padding
//      and the band above it are two declarations in two files.
//   3. **The motion has to be the reader's choice.** Every transition and animation the
//      campaign's own classes declare is inside a `prefers-reduced-motion: no-preference`
//      query, so a reader who asked their system for less motion is served the page without the
//      movement rather than with it neutralised, and the layout's universal `reduce` rule is
//      still there underneath. The sections' entrance rides `site.js`'s own `[data-reveal-block]`
//      — which holds nothing back when the script does not run — and the hero's is the first
//      frame (`@starting-style`), so nothing on the page is ever hidden by a script that failed.
//      Two of the readings are about that entrance being the campaign's own: the block that
//      staggers the hero's parts, and the row of a step, which the script marks along with the
//      heading and the line inside it and which must therefore not travel with them.
//
// What is deliberately not read here: whether the result *looks* right. That is the owner's
// eye on the preview (G2.5), and the readings below are the part of it a machine can hold.

/** The three widths the owner read the live preview at (the feedback of 2026-10-30). */
const CAMPAIGN_WIDTHS = [390, 768, 1440]

/** The four built campaign pages: the ones that render the campaign's hero. */
function campaignPages() {
  return builtPages().filter(({ html }) =>
    [...walk(parse(html))].some((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('campaign-hero')),
  )
}

/** The bands of one built campaign page, in the order a reader meets them. */
function campaignBands(html) {
  return [...walk(parse(html))]
    .map((el) => ({ el, classes: (el.attrs.get('class') ?? '').split(/\s+/).filter(Boolean) }))
    .filter(({ classes }) => classes.some((name) => CAMPAIGN_BLOCKS.includes(name)))
    .map(({ el, classes }) => ({ el, klass: classes.find((name) => CAMPAIGN_BLOCKS.includes(name)) }))
}

/**
 * A CSS length as pixels at a viewport width: a palette or type token, a `rem`/`px` length, a
 * `vw` length, or a `clamp()` of three of those — the three shapes the display scale and the
 * campaign's headline are written in. A shape this reader does not know is `NaN`, which the
 * cases below refuse rather than compare: a size nothing could measure is a size nothing held
 * to two lines.
 */
function lengthAtWidth(value, tokens, width) {
  const text = (value ?? '').trim()
  const clamp = /^clamp\(([\s\S]+)\)$/.exec(text)
  if (clamp !== null) {
    const parts = splitTop(clamp[1], ',')
    if (parts.length !== 3) return NaN
    const [floor, preferred, ceiling] = parts.map((part) => lengthAtWidth(part, tokens, width))
    if ([floor, preferred, ceiling].some((px) => Number.isNaN(px))) return NaN
    return Math.min(Math.max(floor, preferred), ceiling)
  }
  const vw = /^([\d.]+)vw$/.exec(text)
  if (vw !== null) return (Number(vw[1]) / 100) * width
  return toPx(text, tokens)
}

/**
 * How many lines a headline takes in a column, by the site's own advance table: the words are
 * placed first-fit, which is the algorithm a browser's line breaker is, and a word wider than
 * the column takes a line of its own rather than being broken. First-fit is optimal in the
 * number of lines, so this is the count a browser would show and not an average of one.
 */
function headlineLines(headline, columnPx, fontPx) {
  const space = textWidth(' ', fontPx)
  let lines = 1
  let used = 0
  for (const word of headline.split(/\s+/).filter(Boolean)) {
    const wordPx = textWidth(word, fontPx)
    if (used === 0) used = wordPx
    else if (used + space + wordPx <= columnPx) used += space + wordPx
    else {
      lines += 1
      used = wordPx
    }
  }
  return lines
}

/**
 * The headline's own rule, as `{ size, column }` at a viewport width: the font size in force on
 * `.campaign-hero-headline`, and the column it is printed in — the band's content box, less the
 * cap the rule declares when that cap is the narrower of the two. `null` when a value cannot be
 * read, which the caller refuses.
 */
function headlineColumn(css, width) {
  const rules = allRules(css)
  const tokens = lengthTokens(css)
  const mine = rules.filter((rule) => appliesAt(width)(rule) && /\.campaign-hero-headline(?![\w-])/.test(rule.selector))
  const last = (property) =>
    mine
      .map((rule) => declarations(rule)[property])
      .filter((value) => value !== undefined)
      .pop()
  const size = lengthAtWidth(last('font-size'), tokens, width)
  if (!Number.isFinite(size)) return null
  const cap = last('max-width')
  const capPx = cap === undefined || cap.trim() === 'none' ? Infinity : capPixels(cap, tokenText(css), tokens, size)
  if (Number.isNaN(capPx)) return null
  // The band's content box: the page's one gutter on each side, and `main` gives a campaign page
  // neither a gutter nor a ceiling of its own (`campaignLeftEdgeFailures` above reads that pair).
  const gutter = gutterPx(css, 'var(--frame-gutter)', width, width, tokens)
  if (!Number.isFinite(gutter)) return null
  return { size, column: Math.min(width - 2 * gutter, capPx) }
}

/**
 * The rules that set a band's own box, for one class and one viewport width. A rule that merely
 * *names* the class is not one of them, and the two that name one without being it are both in
 * the built stylesheet: `main:has(> .campaign-hero) { padding: 0 }` sets `main`'s padding, and
 * `.campaign-steps [data-reveal] { … }` sets a descendant's. A reader that took either for the
 * band's own would report a band padding nothing declares — the `main` rule above lands after
 * the component's in the built stylesheet, so the hero's 112 px would come out as 0.
 */
function bandRules(css, klass, width) {
  const targets = (selector) =>
    selector
      .split(',')
      .map((part) => part.replace(/\[data-astro-cid-[^\]]*\]/g, '').trim())
      .some((part) => new RegExp(`\\.${klass}(?![\\w-])(?=[\\[:.]|$)`).test(part))
  return allRules(css).filter((rule) => appliesAt(width)(rule) && targets(rule.selector))
}

/**
 * A band's block padding at a viewport width, as `[start, end]` in pixels: the `padding-block`
 * shorthand, the block components of a `padding` shorthand, or the two longhands — whichever the
 * cascade puts last, which is how a browser reads them. `null` when nothing declares one.
 */
function blockPadding(css, klass, width) {
  const tokens = lengthTokens(css)
  let out = null
  for (const rule of bandRules(css, klass, width)) {
    const decls = declarations(rule)
    if (decls['padding-block'] !== undefined) {
      const parts = shorthandParts(decls['padding-block'])
      out = [toPx(parts[0], tokens), toPx(parts[1] ?? parts[0], tokens)]
    }
    if (decls.padding !== undefined) {
      const parts = shorthandParts(decls.padding)
      out = [toPx(parts[0], tokens), toPx(parts.length >= 3 ? parts[2] : parts[0], tokens)]
    }
    if (decls['padding-block-start'] !== undefined) out = [toPx(decls['padding-block-start'], tokens), out === null ? NaN : out[1]]
    if (decls['padding-block-end'] !== undefined) out = [out === null ? NaN : out[0], toPx(decls['padding-block-end'], tokens)]
  }
  return out
}

const CAMPAIGN_REDUCE = /prefers-reduced-motion\s*:\s*reduce/i
const CAMPAIGN_NO_PREFERENCE = /prefers-reduced-motion\s*:\s*no-preference/i
/** The site's pointer query, which is what makes a hover a hover: a touch screen has none. */
const CAMPAIGN_POINTER = /hover\s*:\s*hover/

/**
 * The font size in force on the elements a selector pattern names, at a viewport width: the last
 * `font-size` the cascade declares on a rule whose selector matches, read by the same length
 * reader the headline's own rule is. `null` when nothing declares one or the value is a shape
 * nothing can measure.
 */
function declaredFontSize(css, pattern, width) {
  const tokens = lengthTokens(css)
  const declared = allRules(css)
    .filter((rule) => appliesAt(width)(rule) && pattern.test(rule.selector))
    .map((rule) => declarations(rule)['font-size'])
    .filter((value) => value !== undefined)
  const size = lengthAtWidth(declared.pop(), tokens, width)
  return Number.isFinite(size) ? size : null
}

/**
 * A selector's specificity as `[ids, classes, types]` — the tuple the cascade compares in that
 * order — for the shapes the campaign's own styles are written in: classes, attributes, one
 * element name, descendant combinators and Astro's scope attribute on the last compound.
 * `:not(…)` counts what is inside it, which is where a bare `[data-revealed]` lives, and a
 * pseudo-class counts as a class. The campaign's travel and the rule that neutralises it are the
 * two selectors this reads; a selector it cannot place is not compared by `outranks` at all.
 */
function specificity(selector) {
  const count = (text) => [
    (text.match(/#[\w-]+/g) ?? []).length,
    (text.match(/\.[\w-]+/g) ?? []).length + (text.match(/\[[^\]]*\]/g) ?? []).length + (text.match(/:(?!not\b)[\w-]+/g) ?? []).length,
    (
      text
        .replace(/\[[^\]]*\]/g, ' ')
        .replace(/\.[\w-]+/g, ' ')
        .replace(/::?[\w-]+(\([^)]*\))?/g, ' ')
        .match(/[a-z][\w-]*/gi) ?? []
    ).length,
  ]
  return selector
    .split(',')
    .map((part) => {
      const inner = [...part.matchAll(/:not\(([^)]*)\)/g)].map((match) => match[1]).join(' ')
      const outer = part.replace(/:not\([^)]*\)/g, ' ')
      const [a, b] = [count(outer), count(inner)]
      return [a[0] + b[0], a[1] + b[1], a[2] + b[2]]
    })
    .reduce((best, next) => (outranks(next, best) ? next : best))
}

/** Whether one specificity tuple beats another, which is what decides a rule that ties on origin. */
function outranks(a, b) {
  if (a[0] !== b[0]) return a[0] > b[0]
  if (a[1] !== b[1]) return a[1] > b[1]
  return a[2] > b[2]
}

test('the campaign hero\'s headline is at most two lines at 390, 768 and 1440 px', () => {
  const pages = campaignPages()
  assert.equal(pages.length, 4, `the build produced ${pages.length} campaign page(s) — ${pages.map(({ rel }) => rel).join(', ') || '(none)'} — and the spec's four paths are four (G2.2)`)

  for (const { rel, html } of pages) {
    const css = pageCss(html)
    const headline = textOf([...walk(parse(html))].find((el) => el.name === 'h1')).trim()
    assert.ok(headline !== '', `${rel} carries no headline text to measure`)
    for (const width of CAMPAIGN_WIDTHS) {
      const read = headlineColumn(css, width)
      assert.ok(
        read !== null,
        `at ${width} px the campaign headline's rule cannot be read: its size is \`${(allRules(css).filter((rule) => appliesAt(width)(rule) && /\.campaign-hero-headline(?![\w-])/.test(rule.selector)).map((rule) => declarations(rule)['font-size']).filter(Boolean).pop()) ?? '(nothing declared)'}\`, and a size or a column this check cannot turn into pixels is one nothing held to two lines (the owner's feedback of 2026-10-30)`,
      )
      const { size, column } = read
      const lines = headlineLines(headline, column, size)
      assert.ok(
        lines <= 2,
        `${rel}'s headline ${JSON.stringify(headline)} takes ${lines} lines at ${width} px: it is set at ${Math.round(size * 10) / 10} px in a column of ${Math.round(column)} px, and the owner asked for two lines at every width — the measure is the band's and the size is capped rather than left at \`display-xl\`'s 7.2 vw (the owner's feedback of 2026-10-30)`,
      )
    }
  }
})

test('the campaign page\'s headline is the largest type on the page at 390, 768 and 1440 px', () => {
  const pages = campaignPages()
  assert.equal(pages.length, 4, `the build produced ${pages.length} campaign page(s), and the headline's lead is read on the spec's four (G2.2)`)

  // The type the headline has to lead, by the class each is written on. The hero's button is not
  // one of them: a control's label is not display type, and a 56 px target with a 20 px label is
  // the shape the site's own button is.
  const OTHERS = [
    ['the supporting line under it', /\.campaign-hero-line(?![\w-])/],
    ['the steps section\'s heading', /\.campaign-steps-title(?![\w-])/],
    ['a step\'s name', /\.campaign-step-name(?![\w-])/],
    ['the closing band\'s heading', /\.campaign-calls-title(?![\w-])/],
  ]

  for (const { rel, html } of pages) {
    const css = pageCss(html)
    const tokens = lengthTokens(css)
    const floor = toPx('var(--font-size-lg)', tokens)
    for (const width of CAMPAIGN_WIDTHS) {
      const read = headlineColumn(css, width)
      assert.ok(read !== null, `${rel}'s headline rule cannot be read at ${width} px, so nothing holds it above the page's other type (the owner's feedback of 2026-10-30)`)
      const size = Math.round(read.size * 10) / 10
      assert.ok(
        read.size >= floor,
        `${rel}'s headline is ${size} px at ${width} px, under the body's larger step (${floor} px): the owner's two-line rule caps the size, and the floor under the cap is what keeps the page's \`h1\` from becoming its smallest type — the cap alone says nothing about how small the headline may get (the owner's feedback of 2026-10-30)`,
      )
      for (const [what, pattern] of OTHERS) {
        const other = declaredFontSize(css, pattern, width)
        assert.ok(
          other !== null,
          `${rel} declares no \`font-size\` for ${what}, so nothing holds the headline above it at ${width} px`,
        )
        assert.ok(
          read.size > other,
          `${rel}'s headline is ${size} px at ${width} px and ${what} is ${Math.round(other * 10) / 10} px: the page's \`h1\` is the largest type on the page at every width the owner measured at. The two-line rule alone does not give that — at 390 px a 48-character headline is two lines only up to 22.7 px, and \`.display-lg\`'s 32 px floor would put every section heading above it — so the campaign's own section type scales under the headline's slope (4.4 vw and 3.6 vw under 5.5 vw) rather than at the display scale's fixed floor (the owner's feedback of 2026-10-30)`,
        )
      }
    }
  }
})

test('the campaign steps\' travel does not reach the row it holds', () => {
  const pages = campaignPages()
  assert.equal(pages.length, 4, `the build produced ${pages.length} campaign page(s), and the steps' travel is read on the spec's four (G2.2)`)

  for (const { rel, html } of pages) {
    const css = pageCss(html)
    const rules = cssRules(css)
    const inNoPreference = (rule) => rule.conditions.some((condition) => CAMPAIGN_NO_PREFERENCE.test(condition))

    const travel = rules.find(
      (rule) => inNoPreference(rule) && /\.campaign-steps(?![\w-])/.test(rule.selector) && /translateY\(\s*[\d.]+px\s*\)/.test(rule.body),
    )
    assert.ok(
      travel,
      `${rel} carries no travel rule for \`.campaign-steps\` inside a \`prefers-reduced-motion: no-preference\` block: the section's parts rise into place, and the owner asked for 16–32 px of it (the owner's feedback of 2026-10-30)`,
    )
    const travelPx = Number(/translateY\(\s*([\d.]+)px\s*\)/.exec(travel.body)[1])
    assert.ok(
      travelPx >= 16 && travelPx <= 32,
      `${rel}'s steps travel ${travelPx} px: the owner asked for 16–32 px (the owner's feedback of 2026-10-30)`,
    )

    // The row is what `site.js` marks `data-reveal` along with the heading and the line inside
    // it, so a travel rule that reaches the row moves the same text twice. The neutraliser is
    // read, and then the cascade is: order alone is not enough, because the travel rule names a
    // descendant of the band and the neutraliser names the row itself.
    const neutral = rules.find(
      (rule) =>
        inNoPreference(rule) &&
        /\.campaign-step(?![\w-])/.test(rule.selector) &&
        /(^|;|\s)transform:\s*none/.test(rule.body) &&
        /(^|;|\s)opacity:\s*1/.test(rule.body),
    )
    assert.ok(
      neutral,
      `${rel} carries no neutralising rule for \`.campaign-step\` inside a \`no-preference\` block: \`site.js\` marks the row \`data-reveal\` along with the \`h3\` and the \`p\` inside it, so a row that travels with its own parts is a 48 px rise — twice the 16–32 px the owner asked for (the owner's feedback of 2026-10-30)`,
    )
    assert.ok(
      outranks(specificity(neutral.selector), specificity(travel.selector)),
      `${rel}'s row neutraliser "${neutral.selector}" (${specificity(neutral.selector).join(',')}) does not outrank the travel rule "${travel.selector}" (${specificity(travel.selector).join(',')}): the row travels anyway and its text rises twice, which is what the first cut of this section did — a comment that says the row is neutralised is not the cascade saying it (the owner's feedback of 2026-10-30)`,
    )
  }
})

test('the campaign page\'s bands are spaced, and no band is a screen of nothing', () => {
  const SCREEN = 900
  const pages = campaignPages()
  assert.equal(pages.length, 4, `the build produced ${pages.length} campaign page(s), and the spacing below is read on the spec's four (G2.2)`)

  for (const { rel, html } of pages) {
    const css = pageCss(html)
    const bands = campaignBands(html)
    assert.ok(bands.length >= 2, `${rel} draws ${bands.length} campaign band(s), so there is no pair of sections to measure a gap between`)
    const pads = bands.map(({ klass }) => ({ klass, pad: blockPadding(css, klass, 1440) }))
    for (const { klass, pad } of pads) {
      assert.ok(
        pad !== null && pad.every((px) => Number.isFinite(px)),
        `the \`.${klass}\` band's block padding at 1440 px cannot be read out of its stylesheet, and a padding nothing measured is a gap nothing held to a range`,
      )
      for (const px of pad) {
        assert.ok(
          px <= SCREEN / 2,
          `the \`.${klass}\` band pads ${px} px above or below its content at 1440 px: half of the 900 px screen the owner measured at is the most a band may spend on nothing, or the page reads as bands of empty ground with sections between them (the owner's feedback of 2026-10-30)`,
        )
      }
    }
    for (let i = 1; i < pads.length; i += 1) {
      const gap = pads[i - 1].pad[1] + pads[i].pad[0]
      assert.ok(
        gap >= 120 && gap <= 240,
        `at 1440 px the gap between \`.${pads[i - 1].klass}\` and \`.${pads[i].klass}\` is ${gap} px: the owner asked for 120–240 px between the sections, which is the room the page reads as sections at and not as one block (the owner's feedback of 2026-10-30)`,
      )
    }
  }
})

test('the campaign page\'s motion is the reader\'s choice, and nothing is hidden without a script', () => {
  const pages = campaignPages()
  assert.equal(pages.length, 4, `the build produced ${pages.length} campaign page(s), and the motion below is read on the spec's four (G2.2)`)

  for (const { rel, html } of pages) {
    const css = pageCss(html)
    const rules = cssRules(css)
    const moving = rules.filter(
      (rule) =>
        rule.selector &&
        /\.campaign-[a-z-]/.test(rule.selector) &&
        cssDeclarations(rule.body).some(
          ([property, value]) =>
            ((property === 'transition' || property === 'transition-duration') && !/^(none|0s|0ms)\b/.test(value)) ||
            ((property === 'animation' || property === 'animation-name') && !/^none\b/.test(value)),
        ),
    )
    assert.ok(
      moving.length > 0,
      `${rel} declares no transition and no animation on any campaign class: the buttons answer a hover, a press and a focus, and the sections rise into place — none of that is written down (the owner's feedback of 2026-10-30)`,
    )
    for (const rule of moving) {
      assert.ok(
        rule.conditions.some((condition) => CAMPAIGN_NO_PREFERENCE.test(condition)),
        `${rel}: "${rule.selector}" declares motion outside a \`prefers-reduced-motion: no-preference\` block: a reader who asked their system for less motion is owed the same page without the movement, and the campaign's own motion is gated on that answer rather than left for a rule elsewhere to neutralise (D105, the owner's feedback of 2026-10-30)`,
      )
    }

    // The layout's own fallback, which is what the reader who asked for less motion gets.
    const universal = rules.filter(
      (rule) =>
        rule.selector &&
        rule.conditions.some((condition) => CAMPAIGN_REDUCE.test(condition)) &&
        rule.selector.split(',').some((part) => part.trim() === '*'),
    )
    for (const property of ['transition-duration', 'animation-duration', 'animation-iteration-count']) {
      assert.ok(
        universal.some((rule) => cssDeclarations(rule.body).some(([name]) => name === property)),
        `${rel} carries no universal \`prefers-reduced-motion: reduce\` rule setting ${property}: the campaign's motion is gated, and the page's own fallback for everything else is the layout's (D105)`,
      )
    }

    // The entrance itself, twice over: the sections ride the site's own script, which holds a
    // part back only from JavaScript and only while it is below the fold, and the hero's is the
    // first frame the browser draws, which no script and no scroll is needed for.
    for (const klass of ['campaign-steps', 'campaign-calls']) {
      const band = [...walk(parse(html))].find((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes(klass))
      assert.ok(band, `${rel} renders no \`.${klass}\` band to read an entrance off`)
      assert.ok(
        band.attrs.has('data-reveal-block'),
        `${rel}'s \`.${klass}\` band carries no \`data-reveal-block\`: the entrance is \`site.js\`'s own, and it marks a part of the block \`data-reveal\` only when the script runs and the part is below the first screen — with no script the section is simply there (the owner's feedback of 2026-10-30)`,
      )
    }
    // The hero's own entrance, and not the layout's: `base.css` carries an `@starting-style` for
    // every inner page's `h1`, so a case that looked only for the at-rule inside a
    // `no-preference` block would pass with the campaign hero's own entrance deleted. The block
    // below is the one that names the campaign's parts, and the travel is read out of that.
    //
    // `.campaign-hero-shot` is the hero's picture on the pages as shipped — the console screen a
    // variant names — and `.campaign-hero-art` is the drawing that takes the same slot on a page
    // whose variant names no screen: the two are one part of the stagger, and either class is
    // what a hero's picture rises with (`website/test/landing-pages.mjs` reads both pictures).
    const starting = rules.filter(
      (rule) =>
        rule.selector === '@starting-style' &&
        rule.conditions.some((condition) => CAMPAIGN_NO_PREFERENCE.test(condition)) &&
        /\.campaign-hero-(line|action|shot|art)(?![\w-])/.test(rule.body),
    )
    assert.ok(
      starting.length > 0,
      `${rel} carries no \`@starting-style\` block of its own inside a \`prefers-reduced-motion: no-preference\` block: the layout's is every inner page's \`h1\`, so the hero's stagger — the sentence 100 ms after the headline, the button 200 ms after that, the picture 300 ms after that — is written in the campaign's styles or it is nowhere (the owner's feedback of 2026-10-30)`,
    )
    for (const klass of ['campaign-hero-line', 'campaign-hero-action', 'campaign-hero-shot', 'campaign-hero-art']) {
      assert.ok(
        starting.some((rule) => rule.body.includes(`.${klass}`)),
        `${rel}'s campaign \`@starting-style\` blocks name no \`.${klass}\`: the hero's parts stagger in, and a part that is not in a block is a part that is simply there on the first frame (the owner's feedback of 2026-10-30)`,
      )
    }
    const rising = starting.flatMap((rule) => [...rule.body.matchAll(/translateY\((-?[\d.]+)px\)/g)].map((m) => Number(m[1])))
    assert.ok(rising.length > 0, `${rel}'s \`@starting-style\` block moves nothing: the entrance rises from somewhere, and the owner asked for 16–32 px of it (the owner's feedback of 2026-10-30)`)
    for (const px of rising) {
      assert.ok(
        px >= 16 && px <= 32,
        `${rel}'s hero entrance rises ${px} px: the owner asked for 16–32 px, which is enough to be seen as an entrance and short enough not to be a journey (the owner's feedback of 2026-10-30)`,
      )
    }

    // The feedback on the controls, which is the third half of the finding: a hover, a press and
    // a focus. The first two are the campaign's own — the hero's button and the closing band's
    // three answer both — and the third is the site's ring, with one addition: the closing band
    // is a field of the accent, and the site's ring is the accent, so on that band the campaign
    // names the ring's colour rather than leaving a ring nothing can see.
    const button = /\.campaign-(hero-button|calls-primary|calls-secondary)/
    for (const [state, pattern] of [
      ['hover', /:hover/],
      ['press', /:active/],
    ]) {
      assert.ok(
        rules.some((rule) => button.test(rule.selector) && pattern.test(rule.selector)),
        `${rel} writes no ${state} rule for a campaign button: the owner asked the buttons to answer a hover and a press, and a control that does not is one a reader cannot tell they are on (the owner's feedback of 2026-10-30)`,
      )
    }
    assert.ok(
      rules.some((rule) => rule.selector.includes(':focus-visible') && /(^|;|\s)outline\s*:/.test(rule.body)),
      `${rel} carries no \`:focus-visible\` rule with an outline: every link on the page keeps the site's focus ring, and the campaign's buttons are links`,
    )
    assert.ok(
      rules.some(
        (rule) =>
          /\.campaign-calls/.test(rule.selector) &&
          rule.selector.includes(':focus-visible') &&
          /outline-color:\s*var\(--[a-z-]+\)/.test(rule.body),
      ),
      `${rel} draws the focus ring on the closing band's buttons in the site's own colour: that band is a field of the accent and the site's ring is the accent, so a ring in it is a ring a reader cannot see (the owner's feedback of 2026-10-30)`,
    )

    // The movement itself — the other half of D412's finding 1, and until now the half no gate
    // read. The owner asked the controls for "a small lift or icon nudge" and not for a second
    // colour, so what an edit may not take away in silence is the 2 px each plate rises and the
    // 2 px each mark travels the way its own action goes. Each one is read where it is written:
    // inside the campaign's own `no-preference` block *and* inside the site's pointer query,
    // because the owner's other bound is that a reader who asked their system for less motion —
    // or who is on a touch screen, where there is no hover to answer — is served the band still.
    const MOVES = [
      { what: "the hero's booking plate", control: /\.campaign-hero-button/, icon: null, axis: 'y', px: -2 },
      { what: "the hero's calendar mark", control: /\.campaign-hero-button/, icon: 'calendar', axis: 'y', px: -2 },
      { what: "the closing band's booking plate", control: /\.campaign-calls-primary/, icon: null, axis: 'y', px: -2 },
      { what: "the closing band's two quiet actions", control: /\.campaign-calls-secondary/, icon: null, axis: 'y', px: -2 },
      { what: "the closing band's calendar mark", control: /\.campaign-calls-primary/, icon: 'calendar', axis: 'y', px: -2 },
      { what: "the install's arrow", control: /\.campaign-calls-secondary/, icon: 'download', axis: 'y', px: 2 },
      { what: "the chat's bubble", control: /\.campaign-calls-secondary/, icon: 'chat', axis: 'x', px: 2 },
    ]
    /** The distance one rule moves by on one axis, as the built stylesheet writes it: `translateY`
     *  for the vertical moves and `translateX` — which a minifier may write as the one-argument
     *  `translate` — for the sideways one. `null` when the rule declares neither. */
    const movedBy = (body, axis) => {
      const patterns =
        axis === 'x' ? [/translateX\(\s*(-?[\d.]+)px\s*\)/, /translate\(\s*(-?[\d.]+)px\s*\)/] : [/translateY\(\s*(-?[\d.]+)px\s*\)/]
      for (const pattern of patterns) {
        const found = pattern.exec(body)
        if (found) return Number(found[1])
      }
      return null
    }
    const isMove = (rule, selector, move) => {
      if (!move.control.test(selector) || !selector.includes(':hover')) return false
      if (move.icon === null) {
        if (/\.campaign-icon/.test(selector)) return false
      } else if (!new RegExp(`\\.campaign-icon\\[data-icon=['"]?${move.icon}['"]?\\]`).test(selector)) return false
      if (!rule.conditions.some((condition) => CAMPAIGN_NO_PREFERENCE.test(condition))) return false
      if (!rule.conditions.some((condition) => CAMPAIGN_POINTER.test(condition))) return false
      return movedBy(rule.body, move.axis) === move.px
    }
    for (const move of MOVES) {
      assert.ok(
        rules.some((rule) => (rule.selector ?? '').split(',').some((selector) => isMove(rule, selector.trim(), move))),
        `${rel} writes no ${Math.abs(move.px)} px ${move.axis === 'x' ? 'sideways' : 'vertical'} move for ${move.what} on hover, inside both a \`prefers-reduced-motion: no-preference\` block and the site's pointer query: the owner asked each control for a small lift or an icon nudge rather than a colour, and a reader who asked their system for less motion — or who has no hover to give — is owed the same band still (D412's finding 1, the owner's feedback of 2026-10-30)`,
      )
    }
  }
})
