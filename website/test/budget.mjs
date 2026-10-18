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
// build of 2026-10-18 — the one that ships the site's script (P6) and its menu behaviour (P3)
// — plus 25 %, which is D192's rule and the rule the README's loose figures are three times:
//
//   - JavaScript per page: 6,797 bytes. The one script the site ships,
//     `public/scripts/site.js` (P6, P3, D183), is 5,437 bytes; × 1.25, rounded up. Every page
//     loads it exactly once, so the file's own size IS the whole of a page's JavaScript —
//     which is why the case below reads the artifact rather than trusting this line.
//   - Page weight per page: 264,327 bytes. The heaviest page on 2026-10-18, `docs/index.html`,
//     weighed 211,461 bytes: its HTML 183,545 bytes (the whole generated CLI reference, inline
//     CSS included) plus the 27,916 bytes of first-party files it fetches — the script above
//     and the logo and icons the layout links; × 1.25, rounded up. One ceiling for every page
//     rather than one per page, so a page that grows with the product (the changelog, the
//     reference) is held to the same line as the rest instead of to its own history.
//   - Whole site: 919,769 bytes. Every file in `dist/` on that build summed to 735,815
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
import { DIST, SITE, builtPages, filesUnder, parse, servedFile, walk } from './lib/html.mjs'

// ---- the numbers, from the README ------------------------------------------------------

const BUDGET_LINES = {
  js: /^[ \t]*- \*\*JavaScript per page:\*\* ([\d,]+) bytes\b/m,
  page: /^[ \t]*- \*\*Page weight per page:\*\* ([\d,]+) bytes\b/m,
  site: /^[ \t]*- \*\*Whole site:\*\* ([\d,]+) bytes\b/m,
}

/** The three budgets as website/README.md states them. */
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
    readmeBudgets('- **JavaScript per page:** 0 bytes.\n- **Page weight per page:** 1,234 bytes.\n- **Whole site:** 5,678 bytes.\n'),
    { js: 0, page: 1234, site: 5678 },
  )
  assert.throws(() => readmeBudgets('- **JavaScript per page:** zero\n'), /does not state the js budget/)
})
