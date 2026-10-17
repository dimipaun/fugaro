// website/test/budget.mjs — the byte budget (website plan, W1.11 step 4; reconciliation
// finding 56, D105).
//
// D105 asks for the performance contract "as numbers a test enforces": a static-first page
// with no client JavaScript except deliberate islands, and a JavaScript budget. The plan
// names the budgets but gives no figures, so the figures below are A DELIBERATE CHOICE FOR
// THE OWNER, derived rather than invented, and recorded where the plan says they live —
// `website/README.md`, "The gates" — which this file reads, so the README's numbers are the
// enforced ones and cannot drift from them:
//
//   - JavaScript per page: 0 bytes. The built site ships no script at all today (no
//     island, no inline script; the terminal and the diagram are CSS/SVG), and 0 × 1.25 is
//     0. The first task that ships a script — W1.12's consent banner — raises this number
//     in the README in the same commit, by the size it adds plus 25%, which is the point:
//     a script is a decision someone states, not a byte count that creeps.
//   - Page weight per page: 214,233 bytes. The heaviest page on 2026-10-17 was
//     `docs/index.html` (171,386 bytes: the whole generated CLI reference, inline CSS
//     included); × 1.25, rounded up. One ceiling for every page rather than one per page,
//     so a page that grows with the product (the changelog, the reference) is held to the
//     same line as the rest instead of to its own history.
//   - Whole site: 418,054 bytes. Every file in `dist/` on 2026-10-17 summed to 334,443
//     bytes (ten pages; no separate stylesheet, script, font or image); × 1.25, rounded up.
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
import { mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
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

test('the README budget lines are read strictly', () => {
  assert.deepEqual(
    readmeBudgets('- **JavaScript per page:** 0 bytes.\n- **Page weight per page:** 1,234 bytes.\n- **Whole site:** 5,678 bytes.\n'),
    { js: 0, page: 1234, site: 5678 },
  )
  assert.throws(() => readmeBudgets('- **JavaScript per page:** zero\n'), /does not state the js budget/)
})
