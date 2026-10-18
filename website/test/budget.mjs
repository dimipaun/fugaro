// website/test/budget.mjs — the byte budget (website plan, W1.11 step 4; reconciliation
// finding 56, D105).
//
// D105 asks for the performance contract "as numbers a test enforces": a static-first page
// with no client JavaScript except deliberate islands, and a JavaScript budget. The plan
// names the budgets but gives no figures, so the figures below are A DELIBERATE CHOICE FOR
// THE OWNER, recorded where the plan says they live — `website/README.md`, "The gates" —
// which this file reads, so the README's numbers are the enforced ones and cannot drift
// from them.
//
// The README's numbers are DELIBERATELY LOOSE (owner's ruling, 2026-10-17: "ignore them for
// now and optimize later"): each is three times the tight derivation below, until a later
// optimisation pass tightens them back towards these figures. The tight derivation is the
// build of 2026-10-18 — the one that ships the site's script (P6), its menu behaviour (P3),
// the homepage's run animation (P7), the How It Works flow (P8) and, re-derived for W3.2, the
// consent gate's analytics bootstrap in that same script — plus 25 %, which is D192's rule and the rule the
// README's loose figures are three times:
//
//   - JavaScript per page: 25,508 bytes. The one script the site ships,
//     `public/scripts/site.js` (P6, P3, D183, W3.2's gate, and the cookie deletion the owner's
//     ruling of 2026-10-18 added to a withdrawal), is 20,406 bytes; × 1.25, rounded up. Every
//     page loads it exactly once, so the file's own size IS the whole of a page's JavaScript —
//     which is why the case below reads the artifact rather than trusting this line, and why
//     D192 requires the README's figure to be re-derived in the same commit that changes the
//     script.
//   - Page weight per page: 284,534 bytes. The heaviest page on 2026-10-18, `docs/index.html`,
//     weighed 227,627 bytes: its HTML (the whole generated CLI reference) plus the first-party
//     files it fetches — the script above and the logo and icons the layout links; × 1.25,
//     rounded up. One ceiling for every page rather than one per page, so a page that grows
//     with the product (the changelog, the reference) is held to the same line as the rest
//     instead of to its own history.
//   - Whole site: 980,408 bytes. Every file in `dist/` on that build summed to 784,326
//     bytes (21 files: eleven pages, the script, the brand icons and the manifest);
//     × 1.25, rounded up.
//
// What is counted, all of it raw (uncompressed) bytes, because that is what the artifact
// holds and what a test can compute without guessing a server's compression:
//   - a page's weight is its HTML (inline `<style>` and `<script>` included) plus every
//     first-party file it makes the browser fetch: linked stylesheets, scripts, module
//     preloads, preloaded fonts, and images — each counted once per page;
//   - a page's JavaScript is its executable inline scripts plus the script files and module
//     preloads it references;
//   - the whole site is every file in `dist/`.
// A failure names the page and its sizes. Lighthouse (D105's performance ≥ 95 target) is
// the owner-run check, not this one: there is no CI on pull requests, and the tests are
// hermetic (F3, F58).
//
// It reads `dist/`, which `npm run check` builds first (`FUGARO_GATES_DIST` points it at a
// copy, which is how the refusal was mutation-proved). Standard library only; no network.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, existsSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { DIST, SITE, builtPages, filesUnder, find, parse, servedFile, walk } from './lib/html.mjs'

// ---- the numbers, from the README ------------------------------------------------------

const BUDGET_LINES = {
  js: /^[ \t]*- \*\*JavaScript per page:\*\* ([\d,]+) bytes\b/m,
  page: /^[ \t]*- \*\*Page weight per page:\*\* ([\d,]+) bytes\b/m,
  site: /^[ \t]*- \*\*Whole site:\*\* ([\d,]+) bytes\b/m,
  lead: /^[ \t]*- \*\*Words in a page's lead:\*\* ([\d,]+) words\b/m,
  words: /^[ \t]*- \*\*Words per page:\*\* ([\d,]+) words\b/m,
}

/** The budgets as website/README.md states them: bytes, and the word ceilings of D188. */
export function readmeBudgets(readme = readFileSync(path.join(SITE, 'README.md'), 'utf8')) {
  const out = {}
  for (const [key, re] of Object.entries(BUDGET_LINES)) {
    const m = re.exec(readme)
    assert.ok(m, `website/README.md does not state the ${key} budget in the line this check reads (${re}): the budget is a fact in the README, not an aspiration in a test`)
    out[key] = Number(m[1].replace(/,/g, ''))
  }
  return out
}

// ---- measuring -------------------------------------------------------------------------

const EXECUTABLE = new Set(['', 'text/javascript', 'application/javascript', 'module'])

/** A page's weight and JavaScript, with the parts that make them up. */
export function measurePage(html, rel, dist) {
  const doc = parse(html)
  const fetched = new Map() // served file -> 'js' | 'other'
  let inlineJS = 0
  let inlineCSS = 0
  const take = (url, kind) => {
    if (!url || /^[a-z][a-z0-9+.-]*:|^\/\//i.test(url)) return // remote or data: — origins.mjs's
    const pageURL = new URL('/' + rel.replace(/index\.html$/, ''), 'https://x/')
    const served = servedFile(dist, new URL(url, pageURL).pathname)
    if (served && !fetched.has(served)) fetched.set(served, kind)
  }
  for (const el of walk(doc)) {
    if (el.name === 'style') inlineCSS += Buffer.byteLength(el.raw ?? '')
    if (el.name === 'script') {
      if (el.attrs.has('src')) take(el.attrs.get('src'), 'js')
      else if (EXECUTABLE.has((el.attrs.get('type') ?? '').toLowerCase())) inlineJS += Buffer.byteLength(el.raw ?? '')
    }
    if (el.name === 'link') {
      const rels = (el.attrs.get('rel') ?? '').toLowerCase().split(/\s+/)
      if (rels.includes('modulepreload')) take(el.attrs.get('href'), 'js')
      else if (rels.includes('stylesheet') || rels.includes('preload') || rels.includes('icon')) take(el.attrs.get('href'), 'other')
    }
    if (el.name === 'img' || el.name === 'source') take(el.attrs.get('src'), 'other')
  }
  const html_ = Buffer.byteLength(html)
  let files = 0
  let fileJS = 0
  for (const [served, kind] of fetched) {
    const size = statSync(path.join(dist, served)).size
    files += size
    if (kind === 'js') fileJS += size
  }
  return { rel, html: html_, inlineCSS, inlineJS, files, weight: html_ + files, js: inlineJS + fileJS }
}

/** Every budget failure of a built artifact, against `budgets`. */
export function budgetFailures(dist, budgets) {
  const out = []
  for (const page of builtPages(dist)) {
    const m = measurePage(page.html, page.rel, dist)
    if (m.js > budgets.js) {
      out.push(`${m.rel} ships ${m.js} bytes of JavaScript (${m.inlineJS} inline + ${m.js - m.inlineJS} in files); the budget is ${budgets.js} bytes per page (website/README.md)`)
    }
    if (m.weight > budgets.page) {
      out.push(`${m.rel} weighs ${m.weight} bytes (HTML ${m.html}, of which inline CSS ${m.inlineCSS} and inline JS ${m.inlineJS}; fetched files ${m.files}); the budget is ${budgets.page} bytes per page (website/README.md)`)
    }
  }
  const total = filesUnder(dist).reduce((sum, rel) => sum + statSync(path.join(dist, rel)).size, 0)
  if (total > budgets.site) out.push(`the whole site is ${total} bytes; the budget is ${budgets.site} bytes (website/README.md)`)
  return out
}

// ---- the word ceilings, from the README (D188) -----------------------------------------
//
// D188's other half: a page's bytes are not the only thing a reader is asked to carry, and
// "the gist above the fold, with very few words" is a promise a byte budget cannot keep.
// The two ceilings below are the words a reader reads, counted over the built page's `main`
// region — the page and not its chrome, so the navigation and the footer are not a page's
// fault.
//
// What counts as a word is what a reader sees: every text run inside `main`, with the
// script and style bodies (raw text, never rendered) and anything `hidden` left out, and an
// image's `alt` standing in for the image, which is how `textOf` in `test/lib/html.mjs`
// already reads a page. A command block is a command a reader copies, and it is counted
// like any other visible text: the ceiling is a claim about the page, not about its prose.
//
// The three reference pages D188 exempts — `/docs/`, `/security/` and `/changelog/` — are
// exempt from the **page** ceiling because each renders a document of this repository
// rather than a page of the site; their leads are still held to the lead ceiling, since a
// lead is a page's own and every page has one.
const PAGE_EXEMPT = new Set(['/docs/', '/security/', '/changelog/'])

/** The words of a piece of text, the way a reader counts them. */
function words(text) {
  return text.trim().split(/\s+/).filter(Boolean).length
}

/** The text a reader sees inside a node: no raw script or style body, nothing hidden. */
function visibleText(node) {
  if (node.text !== undefined) return node.text
  if (node.name === 'img') return node.attrs.get('alt') ?? ''
  if (node.raw !== undefined) return ''
  if (node.attrs?.has('hidden')) return ''
  return (node.children ?? []).map(visibleText).join(' ')
}

/**
 * `main`'s descendants in document order, as a flat list: every element, and every text run
 * and image where it stands. A hidden subtree is left out entirely, which is what keeps a
 * `hidden` control from being counted as a word a reader reads.
 */
function flatten(node, out = []) {
  for (const child of node.children ?? []) {
    if (child.text !== undefined || child.name === 'img') {
      out.push(child)
      continue
    }
    if (child.name === undefined) continue
    if (child.attrs?.has('hidden')) continue
    out.push(child)
    flatten(child, out)
  }
  return out
}

/** True when `node` is `ancestor` or sits inside it. */
function within(node, ancestor) {
  for (let n = node; n; n = n.parent) if (n === ancestor) return true
  return false
}

/** True when `node` comes after `other` in `main`'s document order. */
function comesAfter(main, node, other) {
  const items = flatten(main)
  return items.indexOf(node) > items.indexOf(other)
}

/**
 * The visible text of `main` between two of its elements, in document order. The caller
 * checks the two are in that order before calling — a page's headings can be the other way
 * round, and that is a shape a reader measures rather than one it refuses — so a call that
 * cannot find them is a defect in the reader and not in the page.
 */
function textBetween(main, from, to) {
  const items = flatten(main)
  const start = items.indexOf(from)
  const end = items.indexOf(to)
  assert.ok(start !== -1 && end > start, 'the lead reader could not find the headings it reads between, which it checks for before this call: this is a defect in the reader, not in the page')
  return items
    .slice(start + 1, end)
    .filter((item) => !within(item, from))
    .map((item) => (item.text !== undefined ? item.text : item.name === 'img' ? item.attrs.get('alt') ?? '' : ''))
    .join(' ')
}

/**
 * The words of a built page: the whole of `main`, and its lead.
 *
 * The lead is the visible text between the page's `h1` and its first `h2` — the paragraph a
 * page opens with before its first section, which is the text D188's 40-word ceiling is
 * about. That is the whole of the definition, and it is the plan's own: **the region between
 * the two headings in document order**, wherever either heading sits. It used to be read
 * from the `h1`'s container instead when the first `h2` was outside it — the shape the
 * homepage had, where the hero's section held the `h1` and the flow's step names were the
 * first `h2`s under it — and that reading measured the homepage's lead as 24 words where the
 * plan's measures 134: the promise, the install commands, the terminal and everything up to
 * the flow. A gate whose number depends on which end it is read from is two gates (P4's
 * reviewer asked which one this is, and P5 answered it). The homepage now carries its
 * install section's own `h2` above those blocks, so both readings agree on every built page
 * — 24 words — and the strict one is the one enforced, on this page and on any page that
 * grows blocks between its `h1` and its first `h2` later.
 *
 * A page with no `h2` at all has no such region, and its lead is the paragraph run under the
 * `h1` — the text D188's ceiling is about, read from the other end. A page whose first `h2`
 * comes *before* its `h1` has no such region either — the region would have to be read
 * backwards, and the text in front of a page's `h1` is not the page's lead — so it is
 * measured the same way, as the paragraph run under the `h1`, rather than refused. The
 * whitespace a
 * template leaves between the `h1` and the paragraph under it is a text node and not a
 * paragraph: it is skipped, because reading it as the end of the opening would measure the
 * lead of every indented page — every built page — as zero words.
 *
 * A page with no `main` is not measured here: `a11y.mjs` is the gate that refuses a page
 * without the landmark, and this one says nothing about a region it cannot find.
 */
export function pageWords(html) {
  const main = find(parse(html), 'main')
  if (main === null) return null
  const h1 = find(main, 'h1')
  if (h1 === null) return { words: words(visibleText(main)), lead: 0 }
  const firstH2 = find(main, 'h2')
  // The region between the headings exists only when the `h2` comes after the `h1`: a page
  // whose `h2` comes first is a page with no such region rather than a page this reader
  // refuses, and `textBetween` measures forwards, so the order is checked here and the
  // paragraph run under the `h1` answers for the rest.
  if (firstH2 !== null && comesAfter(main, firstH2, h1)) {
    return { words: words(visibleText(main)), lead: words(textBetween(main, h1, firstH2)) }
  }
  const siblings = h1.parent.children
  const opening = []
  for (const sibling of siblings.slice(siblings.indexOf(h1) + 1)) {
    // A style or script body is not text, a hidden subtree is not rendered, and a comment is
    // not text either: none of them ends the opening paragraph run, which is what keeps the
    // lead the same paragraph whatever the page puts around it.
    if (sibling.name === 'style' || sibling.name === 'script' || sibling.attrs?.has('hidden')) continue
    if (sibling.raw !== undefined) continue
    // A template's newline is a text node between the `h1` and the paragraph under it, and a
    // newline is not a paragraph: a run of whitespace is skipped rather than taken for the
    // end of the opening, which is what keeps a page whose markup is indented — every built
    // page — from measuring a lead of zero words. Text that is not whitespace is text a
    // reader reads, so it is part of the lead and does not end the run.
    if (sibling.text !== undefined) {
      if (sibling.text.trim() !== '') opening.push(sibling.text)
      continue
    }
    if (sibling.name !== 'p') break
    opening.push(visibleText(sibling))
  }
  return { words: words(visibleText(main)), lead: words(opening.join(' ')) }
}

/** Every word-ceiling failure of a built artifact, against `budgets`. */
export function wordFailures(dist, budgets) {
  const out = []
  for (const page of builtPages(dist)) {
    const counts = pageWords(page.html)
    if (counts === null) continue
    if (counts.lead > budgets.lead) {
      out.push(`${page.rel} opens with a ${counts.lead}-word lead; the ceiling is ${budgets.lead} words in a page's lead (website/README.md)`)
    }
    if (!PAGE_EXEMPT.has(page.url) && counts.words > budgets.words) {
      out.push(`${page.rel} is ${counts.words} words; the ceiling is ${budgets.words} words per page (website/README.md), and the pages D188 exempts are ${[...PAGE_EXEMPT].join(', ')}`)
    }
  }
  return out
}

// ---- the gate --------------------------------------------------------------------------

test('every page, and the whole site, is within the byte budget the README states', () => {
  const failures = budgetFailures(DIST, readmeBudgets())
  assert.deepEqual(failures, [], `the built site is over its byte budget:\n  ${failures.join('\n  ')}`)
})

// ---- the one script, counted once per page (P6, D183) -----------------------------------

// The site's JavaScript is one file, and this is the case that says so about the ARTIFACT
// rather than about the budget line above it: every page loads `public/scripts/site.js`
// exactly once, however many times it references it, and the artifact holds no other script
// at all. A page that grew a second script — or an inline one — fails here first, with the
// page named, before it fails the budget with a number.
const SCRIPT = 'scripts/site.js'

test('the site ships one script file, and every page counts its bytes once', (t) => {
  const file = path.join(DIST, SCRIPT)
  assert.ok(
    existsSync(file),
    `${SCRIPT} is not in the built artifact: the copy control is driven by one first-party script file (D183, P6), and the budget above counts it as every page's JavaScript`,
  )
  const bytes = statSync(file).size

  const scripts = filesUnder(DIST).filter((rel) => /\.m?js$/.test(rel))
  assert.deepEqual(
    scripts,
    [SCRIPT],
    `the artifact holds ${scripts.length} script files (${scripts.join(', ')}): the site ships one, and the JavaScript budget is a statement about that one file's bytes`,
  )

  const pages = builtPages(DIST)
  assert.ok(pages.length > 0, 'the build produced no page, so this case would check nothing')
  for (const page of pages) {
    const m = measurePage(page.html, page.rel, DIST)
    assert.equal(
      m.js,
      bytes,
      `${page.rel} counts ${m.js} bytes of JavaScript; the site's script is ${bytes} bytes and a page loads it once, however many times it names it`,
    )
    assert.equal(
      m.inlineJS,
      0,
      `${page.rel} ships ${m.inlineJS} bytes of inline JavaScript: the site's one script is a file, which the deployed policy admits as script-src 'self' rather than by a hash`,
    )
  }

  // The other half of "once", as a fixture rather than as an observation about today's
  // layout: a page that names the same file twice — a preload beside the script tag, which
  // is what a bundler emits — is one download and one charge.
  const dist = distOf(t, {
    'index.html': shell(`<script src="/${SCRIPT}"></script><link rel="modulepreload" href="/${SCRIPT}">`),
    [SCRIPT]: 'x'.repeat(bytes),
  })
  assert.equal(
    measurePage(readFileSync(path.join(dist, 'index.html'), 'utf8'), 'index.html', dist).js,
    bytes,
    'a page that references the script twice is charged for it twice, and the two references are one download',
  )
})

test('every page is within the word ceilings the README states', () => {
  const failures = wordFailures(DIST, readmeBudgets())
  assert.deepEqual(failures, [], `the built site is over its word ceilings:\n  ${failures.join('\n  ')}`)
})

// ---- the refusals, kept ----------------------------------------------------------------

function distOf(t, files) {
  const dist = mkdtempSync(path.join(tmpdir(), 'fugaro-budget-'))
  t.after(() => rmSync(dist, { recursive: true, force: true }))
  for (const [rel, body] of Object.entries(files)) {
    mkdirSync(path.dirname(path.join(dist, rel)), { recursive: true })
    writeFileSync(path.join(dist, rel), body)
  }
  return dist
}

const shell = (head, body = '') => `<!DOCTYPE html><html lang="en"><head><title>t</title>${head}</head><body>${body}</body></html>`

test('an over-budget page is refused with its name and its sizes', (t) => {
  const dist = distOf(t, {
    'index.html': shell('', 'x'.repeat(2000)),
    'small/index.html': shell(''),
  })
  const f = budgetFailures(dist, { js: 0, page: 1000, site: 1_000_000 })
  assert.equal(f.length, 1, f.join('\n'))
  assert.match(f[0], /^index\.html weighs 20\d\d bytes \(HTML 20\d\d, .*\); the budget is 1000 bytes per page/)
})

test('an island over the JavaScript budget is refused, inline or as a file, counted once per page', (t) => {
  const dist = distOf(t, {
    'index.html': shell('<script type="module" src="/_astro/island.js"></script><link rel="modulepreload" href="/_astro/island.js">', '<script>let a=1</script>'),
    '_astro/island.js': 'x'.repeat(500),
    'data/index.html': shell('<script type="application/ld+json">{"a":1}</script>'),
  })
  const f = budgetFailures(dist, { js: 100, page: 1_000_000, site: 1_000_000 })
  assert.equal(f.length, 1, f.join('\n'))
  assert.match(f[0], /^index\.html ships 507 bytes of JavaScript \(7 inline \+ 500 in files\)/)
})

test('a linked stylesheet counts toward the page, and every file toward the site', (t) => {
  const dist = distOf(t, {
    'index.html': shell('<link rel="stylesheet" href="/_astro/a.css">'),
    '_astro/a.css': 'a{}'.repeat(400),
    'orphan.bin': 'y'.repeat(3000),
  })
  const f = budgetFailures(dist, { js: 0, page: 1000, site: 3000 })
  assert.equal(f.length, 2, f.join('\n'))
  assert.match(f[0], /fetched files 1200/)
  assert.match(f[1], /^the whole site is \d+ bytes; the budget is 3000 bytes/)
})

test('the README numbers themselves still refuse: one byte over each fails', (t) => {
  const b = readmeBudgets()
  const over = shell('', 'x'.repeat(b.page + 1))
  const dist = distOf(t, {
    'index.html': over,
    'js/index.html': shell('', `<script>${'x'.repeat(b.js + 1)}</script>`),
    'pad.bin': 'y'.repeat(b.site),
  })
  const f = budgetFailures(dist, b)
  assert.ok(f.some((l) => /^index\.html weighs \d+ bytes .* the budget is \d+ bytes per page/.test(l)), f.join('\n'))
  assert.ok(f.some((l) => /^js\/index\.html ships \d+ bytes of JavaScript/.test(l)), f.join('\n'))
  assert.ok(f.some((l) => /^the whole site is \d+ bytes/.test(l)), f.join('\n'))
})

test('the README budget lines are read strictly', () => {
  assert.deepEqual(
    readmeBudgets(
      '- **JavaScript per page:** 0 bytes.\n- **Page weight per page:** 1,234 bytes.\n- **Whole site:** 5,678 bytes.\n' +
        "- **Words in a page's lead:** 40 words.\n- **Words per page:** 250 words.\n",
    ),
    { js: 0, page: 1234, site: 5678, lead: 40, words: 250 },
  )
  assert.throws(() => readmeBudgets('- **JavaScript per page:** zero\n'), /does not state the js budget/)
  assert.throws(
    () =>
      readmeBudgets(
        '- **JavaScript per page:** 0 bytes.\n- **Page weight per page:** 1,234 bytes.\n- **Whole site:** 5,678 bytes.\n' +
          "- **Words in a page's lead:** 40 words.\n- **Words per page:** a few\n",
      ),
    /does not state the words budget/,
  )
})

// ---- the numbers are the derivation, not a figure picked loose (D192) -------------------

// D192: each README number is three times the build it was derived from plus 25 %, and the
// README says which build. These read the derivation the README writes beside each number and
// hold the number to it, so raising a ceiling means re-deriving it from a build the artifact
// can be measured against — not editing the headline figure. The script is one file edited on
// purpose, so its stated size is its size; the page and the site may grow under the × 3, and
// may shrink up to a tenth before the stated build is too far above this one to stand.
const DERIVATIONS = {
  js: /\*\*JavaScript per page:\*\* ([\d,]+) bytes\. Three times ([\d,]+): the one script[\s\S]*?\(([\d,]+) bytes/,
  page: /\*\*Page weight per page:\*\* ([\d,]+) bytes\. Three times ([\d,]+): the heaviest page[\s\S]*? at ([\d,]+) bytes/,
  site: /\*\*Whole site:\*\* ([\d,]+) bytes\. Three times ([\d,]+) \(every file in `dist\/`, ([\d,]+)\s+bytes/,
}

/** Every way the README's numbers depart from their own derivation, given what is built. */
export function derivationFailures(readme, measured) {
  const out = []
  const n = (s) => Number(s.replace(/,/g, ''))
  for (const [key, re] of Object.entries(DERIVATIONS)) {
    const m = re.exec(readme)
    if (!m) {
      out.push(`website/README.md does not write the ${key} budget as "N bytes. Three times T … (B bytes": D192 derives each number from a named build`)
      continue
    }
    const [budget, tight, build] = [n(m[1]), n(m[2]), n(m[3])]
    if (budget !== 3 * tight) out.push(`the ${key} budget is ${budget} bytes and three times ${tight} is ${3 * tight} (D192)`)
    if (tight !== Math.ceil(build * 1.25)) out.push(`the ${key} tight figure is ${tight} bytes and ${build} plus 25 % is ${Math.ceil(build * 1.25)} (D192)`)
    if (key === 'js' && build !== measured.js) out.push(`the README derives the JavaScript budget from a ${build}-byte site.js and the built one is ${measured.js} bytes: re-derive it in the commit that changes the script (D192)`)
    if (key !== 'js' && build > measured[key] * 1.1) out.push(`the README derives the ${key} budget from a ${build}-byte build and this one is ${measured[key]} bytes: a stated build that far above the real one is a loosened budget (D192)`)
  }
  return out
}

function measuredNow() {
  const pages = builtPages(DIST).map((p) => measurePage(p.html, p.rel, DIST))
  return {
    js: statSync(path.join(DIST, SCRIPT)).size,
    page: Math.max(...pages.map((m) => m.weight)),
    site: filesUnder(DIST).reduce((sum, rel) => sum + statSync(path.join(DIST, rel)).size, 0),
  }
}

test("the README's budgets are D192's derivation from a build this one can be measured against", () => {
  const readme = readFileSync(path.join(SITE, 'README.md'), 'utf8')
  assert.deepEqual(derivationFailures(readme, measuredNow()), [])
})

test('a budget raised without re-deriving it is refused', () => {
  const readme = readFileSync(path.join(SITE, 'README.md'), 'utf8')
  const now = measuredNow()
  const js = /\*\*JavaScript per page:\*\* ([\d,]+) bytes/.exec(readme)[1]
  assert.match(derivationFailures(readme.replace(`**JavaScript per page:** ${js} bytes`, '**JavaScript per page:** 100,000 bytes'), now).join('\n'), /three times/)
  assert.match(derivationFailures(readme, { ...now, js: now.js + 1 }).join('\n'), /re-derive it/)
  assert.match(derivationFailures(readme, { ...now, site: Math.floor(now.site / 2) }).join('\n'), /loosened budget/)
})

// ---- the word ceilings' own refusals, kept ----------------------------------------------
//
// D188's two numbers are the whole of the gate above, so each of them needs a case that
// proves it can fail: a page one word over the ceiling, and a lead one word over it. A
// fixture page is written word by word (`n` words, one per space) so that "one over" is a
// number this file chooses rather than a page somebody later edits.

/** A fixture page whose `main` carries `leadWords` words in its lead and `pageWords` in all. */
function fixture(leadWords, pageWords) {
  // The two headings are one word each ("t" and "s") and a reader reads them, so they are part
  // of `pageWords`: the fixture's total is exact, which is what makes "one word over" a number
  // this file chose rather than one a later edit moves.
  const rest = pageWords - leadWords - 2
  assert.ok(rest >= 0, 'a fixture page cannot carry fewer words in its body than its headings and its lead')
  return shell(
    '',
    `<main><h1>t</h1><p>${Array.from({ length: leadWords }, () => 'w').join(' ')}</p>` +
      `<h2>s</h2><p>${Array.from({ length: rest }, () => 'w').join(' ')}</p></main>`,
  )
}

test('a page one word over the page ceiling is refused, and one word under it is not', (t) => {
  const budgets = { ...readmeBudgets(), lead: 1000 }
  const dist = distOf(t, {
    'over/index.html': fixture(0, budgets.words + 1),
    'under/index.html': fixture(0, budgets.words),
  })
  const f = wordFailures(dist, budgets)
  assert.equal(f.length, 1, f.join('\n'))
  assert.match(f[0], new RegExp(`^over/index\\.html is ${budgets.words + 1} words; the ceiling is ${budgets.words} words per page`))
})

test('a lead one word over the lead ceiling is refused, and one word under it is not', (t) => {
  const budgets = { ...readmeBudgets(), words: 100000 }
  // The total carries the two headings as well as the lead, so a page whose lead is exactly
  // at the ceiling is `lead + 2` words long and one whose lead is one over is `lead + 3`.
  const dist = distOf(t, {
    'over/index.html': fixture(budgets.lead + 1, budgets.lead + 3),
    'under/index.html': fixture(budgets.lead, budgets.lead + 2),
  })
  const f = wordFailures(dist, budgets)
  assert.equal(f.length, 1, f.join('\n'))
  assert.match(f[0], new RegExp(`^over/index\\.html opens with a ${budgets.lead + 1}-word lead; the ceiling is ${budgets.lead} words`))
})

test('a page with no h2 measures the paragraph under its h1, through the newline between them', (t) => {
  // A page whose `main` has no `h2` at all has no region between two headings, so its lead is
  // the run of paragraphs under its `h1` — the same text D188's ceiling is about, read from
  // the other end. Astro indents the template it compiles,
  // so the `h1` and the paragraph are separated by a text node holding a newline and spaces —
  // which is not a paragraph, and not the end of the opening run. A reader that stopped at it
  // would measure the lead as zero words and the lead ceiling would pass on this page without
  // reading it, so the newline is written into the fixture and the lead is measured through
  // it: one word over the ceiling still fails, one word under it still does not.
  const budgets = { ...readmeBudgets(), words: 100000 }
  const page = (n) =>
    shell('', `<main>\n  <h1>t</h1>\n  <p>${Array.from({ length: n }, () => 'w').join(' ')}</p>\n</main>`)
  assert.deepEqual(pageWords(page(3)), { words: 4, lead: 3 })

  const dist = distOf(t, {
    'over/index.html': page(budgets.lead + 1),
    'under/index.html': page(budgets.lead),
  })
  const f = wordFailures(dist, budgets)
  assert.equal(f.length, 1, f.join('\n'))
  assert.match(f[0], new RegExp(`^over/index\\.html opens with a ${budgets.lead + 1}-word lead`))
})

test('the lead is the h1 -> first h2 region wherever the first h2 sits, and what follows it is not the lead', (t) => {
  // The homepage's shape before P5 put an `h2` above its install block: the hero's section
  // holds the `h1`, and the first `h2` of `main` is a step of a figure that sits outside that
  // section. The whole region between the two headings is the lead — the hero's own paragraph
  // and the blocks that follow it, which is where a page's install commands and terminal were —
  // because that is the region the plan defines the lead as, and a page that puts its commands
  // between its `h1` and its first `h2` is a page whose lead they are part of. The run of
  // paragraphs under the `h1` is not the answer here: it is shorter than the region — 38 words
  // where the region is 40 on the `under/` fixture, 39 where it is 41 on `over/` — so a
  // reader that stopped at the hero's own container would pass this fixture and
  // let the page's whole gist through unmeasured (the reading P5 replaced, which measured the
  // built homepage's 134-word lead as 24).
  const budgets = { ...readmeBudgets(), words: 100000 }
  const x = (n) => Array.from({ length: n }, () => 'x').join(' ')
  const page = (n) =>
    shell(
      '',
      `<main>\n <section class="hero">\n  <h1>t</h1>\n  <p>${x(n - 2)}</p>\n </section>\n` +
        ` <div class="hero-install"><p>${x(2)}</p></div>\n` +
        ` <ol><li><h2>step</h2></li><li><p>${x(200)}</p></li></ol>\n</main>`,
    )
  // The lead is the region between the headings — the hero's paragraph and the block that
  // follows the hero's section — and the 200 words after the `h2` are a section of their own
  // and are not part of it.
  assert.deepEqual(pageWords(page(6)), { words: 1 + 6 + 1 + 200, lead: 6 })

  const dist = distOf(t, { 'over/index.html': page(budgets.lead + 1), 'under/index.html': page(budgets.lead) })
  const f = wordFailures(dist, budgets)
  assert.equal(f.length, 1, f.join('\n'))
  assert.match(f[0], new RegExp(`^over/index\\.html opens with a ${budgets.lead + 1}-word lead`))
})

test('a page whose first h2 comes before its h1 is measured, not refused', (t) => {
  // The two headings can be the other way round — a page whose `main` opens with a section
  // heading and puts its `h1` under it, which is a shape a template can produce and the
  // reader has no business about. There is no region between them to read (the text in front
  // of a page's `h1` is not its lead), so the lead is the paragraph run under the `h1`, the
  // same answer as a page with no `h2` — the point being that this case measures rather than
  // aborts. A reader that routed every page with an `h2` into the region reader would assert
  // here and take the whole run down with a message about the reader, which is the failure
  // this case is about.
  const budgets = { ...readmeBudgets(), words: 100000 }
  const x = (n) => Array.from({ length: n }, () => 'x').join(' ')
  const page = (n) =>
    shell('', `<main>\n <section>\n  <h2>a section</h2>\n  <h1>t</h1>\n  <p>${x(n - 1)}</p>\n </section>\n</main>`)

  assert.deepEqual(pageWords(page(11)), { words: 2 + 1 + 10, lead: 10 })

  const dist = distOf(t, { 'over/index.html': page(42), 'under/index.html': page(41) })
  const f = wordFailures(dist, budgets)
  assert.equal(f.length, 1, f.join('\n'))
  assert.match(f[0], new RegExp(`^over/index\\.html opens with a ${budgets.lead + 1}-word lead`))
})


test('the three reference pages are exempt from the page ceiling, and their leads are not', (t) => {
  const budgets = readmeBudgets()
  const long = fixture(budgets.lead + 1, budgets.words + 1)
  const dist = distOf(t, {
    'docs/index.html': long,
    'security/index.html': long,
    'changelog/index.html': long,
    'faq/index.html': long,
  })
  const f = wordFailures(dist, budgets)
  // The four pages are over both ceilings, and the three D188 exempts only answer for the
  // lead: five failures, not eight.
  assert.equal(f.length, 5, f.join('\n'))
  for (const route of ['docs', 'security', 'changelog']) {
    assert.ok(
      !f.some((line) => line.startsWith(`${route}/index.html is`)),
      `${route}/index.html is D188's own exemption and the page ceiling was applied to it:\n${f.join('\n')}`,
    )
    assert.ok(
      f.some((line) => line.startsWith(`${route}/index.html opens with a`)),
      `${route}/index.html is not exempt from the lead ceiling, and it was not measured against it:\n${f.join('\n')}`,
    )
  }
  assert.ok(f.some((line) => line.startsWith('faq/index.html is')), 'a page that is not exempt was not measured against the page ceiling')
})

test('the word reader counts what a reader reads, and nothing else', (t) => {
  // The lead is the text between the `h1` and the first `h2`, and not the section under it:
  // a lead that counted the whole page would make the two ceilings one ceiling. The headings
  // are words too — the reader counts the `h1`'s and the `h2`'s own text, which is why the
  // whole page here is seven words and not five.
  const lead = pageWords(shell('', '<main><h1>t</h1><p>one two</p><h2>s</h2><p>three four five</p></main>'))
  assert.deepEqual(lead, { words: 7, lead: 2 })

  // A page with no `h2` has no lead region, and its lead is the paragraphs that follow its
  // `h1` rather than everything the page carries after it.
  assert.deepEqual(
    pageWords(shell('', '<main><h1>t</h1><p>one two</p><div><p>three four five</p></div></main>')),
    { words: 6, lead: 2 },
  )

  // A script or style body is not text a reader reads, a `hidden` subtree is not rendered,
  // and an image's `alt` stands in for the image — the reader of `test/lib/html.mjs`.
  assert.deepEqual(
    pageWords(shell('', '<main><h1>t</h1><style>p{color:red}</style><script>let a=1</script><p hidden>not counted</p><p>one two</p><img src="/x.png" alt="three four"></main>')),
    { words: 5, lead: 2 },
  )

  // A page with no `main` is not this gate's subject: `a11y.mjs` refuses the landmark.
  assert.equal(pageWords(shell('', '<h1>t</h1>')), null)
})

