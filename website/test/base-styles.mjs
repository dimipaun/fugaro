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
// The pages are read from `dist/`, which `npm run check` builds before it runs this file.
// No browser, no network: the checks are over the HTML and CSS a browser would receive.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { existsSync, readFileSync, readdirSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { parse, textOf, walk } from './lib/html.mjs'

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
// stylesheet. It states three things, and each of them is a defect rather than a preference:
//
//   - the header is one row that cannot wrap, with the drawer positioned against it (RF3
//     measured 177 px of three wrapped nav rows on a 390 px first screen);
//   - **the wide viewport is where the menu is.** The base rule hides the panel and the
//     drawer's `open` attribute is the one thing that shows it again — and that block is
//     exactly where the drawer itself is taken away, so no `open` attribute can arrive up
//     there. A 48rem block that moves the panel (`position: static`) without displaying it
//     therefore leaves the menu `display: none` at every viewport at or above the breakpoint:
//     the wordmark, and no way to reach a section. Each of the three wide rules is asserted,
//     because the layout is the three of them together;
//   - **opening anything must not move the page.** The drawer's panel is an overlay rather
//     than a block in the header's row, and a group's own dropdown is out of that row's flow
//     for the same reason: opening *Product* at 1280 px must not grow the header and push the
//     page down under the reader.
//
// Whether the row *fits* 390 px, and whether a real browser's `<details>` behaves as the
// markup says, are rendered measurements no hermetic test can take; they are the owner's live
// check 70a (P3), which asks for exactly these things in a browser.
export function navLayoutFailures(css, rel = '') {
  const out = []
  const fail = (why) => out.push(rel ? `${rel}: ${why}` : why)

  if (!/\.site-header\{[^}]*flex-wrap:nowrap/.test(css)) fail('the header wraps, so the menu can become a second row')
  if (!/\.site-header\{[^}]*position:relative/.test(css)) fail('the header is not the box the drawer is positioned against')
  if (!/\.site-nav-panel\{[^}]*position:absolute/.test(css)) fail("the drawer is in the header's flow, so opening it makes the header taller")
  if (!/\.site-nav-drawer\[open\]\s*~\s*\.site-nav-panel\{[^}]*display:block/.test(css)) fail("the drawer's open state does not show the panel")

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

  // The one the menu dies of: the wide block has to display the panel, not only reposition it.
  if (!has('.site-nav-panel', /^display:(?!none)/)) {
    fail(
      "the 48rem block hides the drawer and never displays the panel — the base rule's display:none still stands there, and the drawer whose open attribute would undo it is the element that block takes away, so the menu is invisible at every viewport at or above 48rem (P3, D182)",
    )
  }
  if (!has('.site-nav-drawer', /^display:none$/)) fail("the drawer's toggle is still shown on a wide viewport, where nothing needs opening")
  if (!has('.site-nav-panel', /^position:static$/)) fail('the wide panel is still an overlay, hanging over the page below the header')
  if (!has('.site-nav-groups', /^display:flex$/)) fail('the wide viewport does not lay the three groups out in one row')
  if (!has('.site-nav-group ul', /^position:absolute$/)) {
    fail("a group's dropdown is in the header's flow, so opening one at a wide viewport grows the header's row and pushes the page down (the drawer's panel is out of flow for the same reason)")
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

function plant(from, to) {
  const css = TODAY()
  assert.ok(css.includes(from), `this fixture is built by removing ${JSON.stringify(from)} from the built stylesheet, and the stylesheet no longer carries it`)
  return css.replace(from, to)
}

test('the layout rule passes on the built stylesheet, so each refusal below is the one edit it names', () => {
  assert.deepEqual(navLayoutFailures(TODAY()), [])
})

test('every layout refusal fires on the defect it names', () => {
  const cases = [
    [
      'a wide block that repositions the panel but never displays it',
      plant('display:block;position:static', 'position:static'),
      /never displays the panel/,
    ],
    [
      'no 48rem breakpoint at all',
      TODAY().replaceAll('(width>=48rem)', '(width>=99rem)'),
      /no 48rem breakpoint/,
    ],
    [
      'the wide row still shows the drawer toggle',
      plant('.site-nav-drawer{display:none}', '.site-nav-drawer{display:block}'),
      /toggle is still shown on a wide viewport/,
    ],
    [
      'a wide panel that is still an overlay',
      plant('display:block;position:static', 'display:block'),
      /still an overlay/,
    ],
    [
      'the wide groups not laid out in one row',
      plant('gap:var(--space-3xs) var(--space-2xs);display:flex}', 'gap:var(--space-3xs) var(--space-2xs)}'),
      /out in one row/,
    ],
    [
      "a group's dropdown left in the header's flow",
      plant('background:var(--ground);position:absolute;top:100%;right:0}', 'background:var(--ground);top:100%;right:0}'),
      /dropdown is in the header's flow/,
    ],
    [
      "a group's dropdown opening rightwards off the viewport",
      plant('position:absolute;top:100%;right:0}', 'position:absolute;top:100%;left:0}'),
      /sideways scroll/,
    ],
    [
      'a header that can wrap',
      plant('flex-wrap:nowrap;display:flex;position:relative}', 'display:flex;position:relative}'),
      /header wraps/,
    ],
    [
      'a drawer panel that is not an overlay',
      plant('display:none;position:absolute;top:100%', 'display:none;top:100%'),
      /drawer is in the header's flow/,
    ],
    [
      'a panel the drawer cannot show',
      plant('.site-nav-drawer[open]~.site-nav-panel{display:block}', '.site-nav-drawer[open]~.site-nav-panel{border-color:red}'),
      /open state does not show the panel/,
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

// ---- the menu (P3, D182) ---------------------------------------------------------------

// The rule the menu is held to, as a function over one built page, so that each part of it
// can be proved by a fixture below rather than only observed on today's markup. It is the
// contract RF4 names, rewritten for the two-level menu: the nav is still a labelled landmark,
// its top-level items are links or `<summary>` elements, every link in it is keyboard
// reachable, and the page still carries exactly one `aria-current="page"` — on the wordmark
// when the page being read is the home page (D182 makes the logo the link home), on the menu
// link otherwise.

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

  // The drawer: the toggle and the panel it opens. The panel is the toggle's next element
  // sibling, which is the structure `base.css`'s sibling combinator reads — if it stops being
  // one, the wide layout still works and the narrow drawer silently never opens.
  const elements = nav.children.filter((child) => child.name !== undefined)
  const drawer = [...walk(nav)].find((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('site-nav-drawer'))
  const panel = [...walk(nav)].find((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('site-nav-panel'))
  if (!drawer) fail('the nav has no drawer toggle (a <details class="site-nav-drawer">): a narrow viewport shows the menu behind one control')
  if (!panel) fail('the nav has no panel (a .site-nav-panel) holding the groups')
  if (drawer && panel && (panel.parent !== nav || elements.indexOf(panel) !== elements.indexOf(drawer) + 1)) {
    fail("the panel is not the drawer toggle's next sibling, which is the wire base.css shows it with: as a child of the toggle the narrow drawer would never open, because a closed <details> hides its own children")
  }
  if (drawer) {
    const summary = drawer.children.find((el) => el.name === 'summary')
    if (!summary || !textOf(summary).trim()) fail('the drawer toggle is not a <summary> with words on it')
    // The panel is the toggle's sibling rather than its child, so nothing but this attribute
    // tells assistive technology which region the toggle's expanded state is about.
    const controls = summary?.attrs.get('aria-controls') ?? ''
    if (!panel || !controls || panel.attrs.get('id') !== controls) {
      fail(`the drawer toggle's aria-controls is ${JSON.stringify(controls)} and the panel's id is ${JSON.stringify(panel?.attrs.get('id') ?? '')}: the panel is outside the <details>, so the toggle has to name the region it opens`)
    }
  }

  // The menu itself: groups of a summary and a list of links. Every top-level item is a link
  // or a summary — a plain span that looks like a control is one a keyboard cannot open.
  const groups = panel ? [...walk(panel)].filter((el) => (el.attrs.get('class') ?? '').split(/\s+/).includes('site-nav-group')) : []
  if (panel && groups.length === 0) fail('the menu has no groups: the nav is three groups of three (D182)')
  // One `name` across the groups makes them an exclusive set, without script: opening one
  // dropdown closes the one already open, so two never hang over each other on a wide row.
  const names = new Set(groups.flatMap((group) => group.children.filter((el) => el.name === 'details').map((el) => el.attrs.get('name') ?? '')))
  if (groups.length > 0 && (names.size !== 1 || names.has(''))) {
    fail(`the menu's groups carry the names ${JSON.stringify([...names])}: they share one non-empty name, so opening one dropdown closes the other two`)
  }
  for (const group of groups) {
    for (const item of group.children.filter((child) => child.name !== undefined)) {
      if (item.name !== 'details') {
        fail(`a top-level menu item is a <${item.name}>, and every top-level item is a link or a <summary> (a disclosure a keyboard can open)`)
        continue
      }
      const summary = item.children.find((el) => el.name === 'summary')
      if (!summary || !textOf(summary).trim()) fail('a menu group has no <summary> with words on it')
    }
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

const FIXTURE_DRAWER = '<details class="site-nav-drawer"><summary class="site-nav-toggle" aria-controls="site-nav-panel">Menu</summary></details>'

function fixtureNav({ label = 'Sections', current = '/docs/', drawer = FIXTURE_DRAWER } = {}) {
  const groups = FIXTURE_GROUPS.map(([group, items]) => {
    const links = items
      .map(([href, text]) => `<li><a href="${href}"${href === current ? ' aria-current="page"' : ''}>${text}</a></li>`)
      .join('')
    return `<li class="site-nav-group"><details name="site-nav-group"><summary>${group}</summary><ul>${links}</ul></details></li>`
  }).join('')
  return `<nav class="site-nav" aria-label="${label}">${drawer}<div class="site-nav-panel" id="site-nav-panel"><ul class="site-nav-groups">${groups}</ul></div></nav>`
}

function fixturePage({ nav = fixtureNav(), wordmark = '<a class="site-wordmark" href="/" rel="home">Fugaro</a>' } = {}) {
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
        wordmark: '<a class="site-wordmark" href="/" rel="home" aria-current="page">Fugaro</a>',
      }),
      'index.html',
    ),
    [],
  )
})

test('every menu refusal fires on the defect it names', () => {
  const cases = [
    ['an unlabelled nav', fixturePage({ nav: fixtureNav({ label: '' }) }), /empty label/],
    ['no drawer toggle', fixturePage({ nav: fixtureNav({ drawer: '' }) }), /no drawer toggle/],
    ['the panel inside the toggle', fixturePage({ nav: fixtureNav().replace('</details><div class="site-nav-panel" id="site-nav-panel">', '<div class="site-nav-panel" id="site-nav-panel">').replace('</ul></div></nav>', '</ul></div></details></nav>') }), /next sibling/],
    ['a drawer toggle that does not name its panel', fixturePage({ nav: fixtureNav().replace(' aria-controls="site-nav-panel"', '') }), /aria-controls is ""/],
    ['a drawer toggle naming another region', fixturePage({ nav: fixtureNav().replace(' id="site-nav-panel"', ' id="elsewhere"') }), /panel's id is "elsewhere"/],
    ['groups that are not one exclusive set', fixturePage({ nav: fixtureNav().replace('<details name="site-nav-group"><summary>Product', '<details><summary>Product') }), /share one non-empty name/],
    ['a group with no summary', fixturePage({ nav: fixtureNav().replace('<summary>Product</summary>', '') }), /no <summary> with words on it/],
    ['a top-level item that is not a control', fixturePage({ nav: fixtureNav().replace('<li class="site-nav-group">', '<li class="site-nav-group"><span>Product</span>') }), /top-level menu item is a <span>/],
    ['a link with no href', fixturePage({ nav: fixtureNav().replace('<a href="/docs/" aria-current="page">', '<a aria-current="page">') }), /no href/],
    ['a link out of the tab order', fixturePage({ nav: fixtureNav().replace('<a href="/faq/">', '<a href="/faq/" tabindex="-1">') }), /out of the tab order/],
    ['no current page marked', fixturePage({ nav: fixtureNav({ current: '' }) }), /marks 0 elements/],
    ['two current pages marked', fixturePage({ nav: fixtureNav().replace('<a href="/console/">', '<a href="/console/" aria-current="page">') }), /marks 2 elements/],
    ['the wrong page marked current', fixturePage({ nav: fixtureNav({ current: '/console/' }) }), /its own route is "\/docs\/"/],
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
