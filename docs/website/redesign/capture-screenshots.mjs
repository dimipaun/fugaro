// docs/website/redesign/capture-screenshots.mjs — the script that produced
// docs/website/redesign/current/ (the "before" set of the redesign brief).
//
// It is committed for one reason: the screenshots are evidence, so how they were
// taken has to be re-runnable rather than remembered. It is NOT part of the site's
// build and nothing in website/ imports it; the site's own package.json is
// deliberately untouched by it (Global Constraint 1: this line of work is docs
// only, and the site's dependency set does not change because a screenshot was
// taken).
//
// How to run it (from the repository root; Playwright is installed OUT OF TREE, so
// website/package.json never changes):
//
//   (cd website && npm ci && npm run build)
//   npm install --prefix /tmp/fugaro-pw playwright@1.49.1
//   /tmp/fugaro-pw/node_modules/.bin/playwright install chromium
//   PLAYWRIGHT_PREFIX=/tmp/fugaro-pw node docs/website/redesign/capture-screenshots.mjs docs/website/redesign/current
//
// `PLAYWRIGHT_PREFIX` is the directory Playwright was installed into: an ES module
// resolves a bare `playwright` from its own directory and ignores NODE_PATH, so the
// script loads it from there explicitly. `REPO_ROOT` defaults to the repository this
// file is in.
//
// How it was run on 2026-10-18, in a container with no Node and no root: Node
// v22.22.0 unpacked to /tmp, Playwright 1.49.1 installed in /tmp/pw with its own
// Chromium, and Chromium's 21 missing shared libraries extracted from .deb files into
// /tmp/sysroot and named through LD_LIBRARY_PATH, FONTCONFIG_PATH and XDG_DATA_DIRS.
// None of that is needed on a machine where `playwright install chromium` works.
//
//   * It serves website/dist over 127.0.0.1 (never file://, because the site's
//     own asset paths are absolute) and shoots every route at 1280×900 and
//     390×844, full page, with `reducedMotion: 'reduce'` so the site's
//     reduced-motion rules are the ones in the picture: this is a set of still
//     images, and a still of an animation is a lie about the page.
//
//   * The output DIRECTORY is an argument, and that is the point of it:
//
//       ... capture-screenshots.mjs docs/website/redesign/current   # the "before" set
//       ... capture-screenshots.mjs docs/website/redesign/after     # the "after" set
//
//     The "before" set is the baseline the "after" set is compared against, so a
//     script that wrote both to one hard-coded directory would overwrite the
//     baseline on the second run — the redesign plan's part P9 asks for the
//     "after" set, and this is the argument that makes that safe. The second
//     argument is where its measurements are written as JSON. Neither directory's
//     images are committed: `docs/website/redesign/{current,after}/*.jpg` are in
//     the root .gitignore, and this script is how they are made again.
//
//   * The measurements are part of the output, not a by-product: header height and
//     nav rows (Check 70a), the `h1`'s bottom, the lead paragraph's bottom and the
//     first command's bottom (Check 70b — P5's fold claim), the word count of `main`
//     and the count of command blocks and copy controls. P5's verification is read
//     out of the JSON this script writes, at 390 × 844: every route's `h1Bottom`, the
//     homepage's `leadBottom` and `firstCommandBottom`, and Getting Started's.
//
// Two things the set records honestly rather than hiding:
//
//   * Three pages are taller than Chromium can capture in one image — /docs/ is
//     79,707 px at 1280 and 134,171 px at 390, /changelog/ 17,076 and 32,122,
//     /security/ 15,314 and 30,500. A full-page capture of those hangs until it
//     times out, which is itself a finding about those pages. They are captured as
//     their TOP 8000 px only, and the file name says so:
//     `docs-desktop-part01of10.jpg` is the first of ten such bands, so nobody
//     mistakes it for the whole page. The measured height of every page is in
//     docs/website/redesign/reconciliation.md §6.
//   * The band captures are JPEG quality 62 and the full-page ones quality 78
//     (about 8 MB for the set; a lossless set was 36 MB). Visual comparison is the
//     owner's design step; no agent looked at any of these images.

import { createServer } from 'node:http'
import { readFileSync, statSync, mkdirSync, writeFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const { chromium } = createRequire(
  process.env.PLAYWRIGHT_PREFIX ? path.join(path.resolve(process.env.PLAYWRIGHT_PREFIX), 'noop.js') : import.meta.url,
)('playwright')

const REPO = path.resolve(process.env.REPO_ROOT ?? path.join(path.dirname(fileURLToPath(import.meta.url)), '../../..'))
const DIST = path.join(REPO, 'website/dist')
// Where the images go: the first argument, relative to the repository root. The
// default is the "before" set, and `docs/website/redesign/after` is the "after"
// set — two directories, never one, so neither run can overwrite the other.
const OUT = path.resolve(REPO, process.argv[2] ?? 'docs/website/redesign/current')
const BAND = 8000
const QUALITY_FULL = 78
const QUALITY_BAND = 62

mkdirSync(OUT, { recursive: true })

const TYPES = {
  '.html': 'text/html', '.css': 'text/css', '.js': 'text/javascript', '.png': 'image/png',
  '.svg': 'image/svg+xml', '.json': 'application/json', '.webmanifest': 'application/manifest+json',
  '.ico': 'image/x-icon', '.txt': 'text/plain', '.woff2': 'font/woff2', '.xml': 'application/xml',
}

const server = createServer((req, res) => {
  let p = decodeURIComponent(req.url.split('?')[0])
  if (p.endsWith('/')) p += 'index.html'
  try {
    const body = readFileSync(path.join(DIST, p))
    res.writeHead(200, { 'content-type': TYPES[path.extname(p)] ?? 'application/octet-stream' })
    res.end(body)
  } catch {
    res.writeHead(404).end('not found')
  }
})
await new Promise((resolve) => server.listen(8099, '127.0.0.1', resolve))

const ROUTES = ['/', '/how-it-works/', '/getting-started/', '/caps-and-costs/', '/console/', '/docs/',
  '/security/', '/changelog/', '/faq/', '/community/', '/privacy/']
const VIEWPORTS = [{ name: 'desktop', width: 1280, height: 900 }, { name: 'mobile', width: 390, height: 844 }]

const browser = await chromium.launch()
const measured = []

for (const vp of VIEWPORTS) {
  const context = await browser.newContext({
    viewport: { width: vp.width, height: vp.height },
    deviceScaleFactor: 1,
    reducedMotion: 'reduce',
  })
  const page = await context.newPage()
  page.setDefaultTimeout(240000)

  for (const route of ROUTES) {
    await page.goto(`http://127.0.0.1:8099${route}`, { waitUntil: 'load' })
    await page.waitForTimeout(300)
    const slug = route === '/' ? 'home' : route.replace(/\//g, '')

    // What the page measures, in the same pass as the picture of it.
    const m = await page.evaluate(() => {
      const q = (s) => document.querySelector(s)
      const box = (el) => (el ? el.getBoundingClientRect() : null)
      const navRows = new Set()
      document.querySelectorAll('.site-nav a').forEach((a) => {
        navRows.add(Math.round(a.getBoundingClientRect().top + window.scrollY))
      })
      const h1 = q('h1')
      return {
        headerHeight: Math.round(box(q('.site-header'))?.height ?? 0),
        navRows: navRows.size,
        navLinks: document.querySelectorAll('.site-nav a').length,
        height: document.documentElement.scrollHeight,
        scrollWidth: document.documentElement.scrollWidth,
        h1: h1?.innerText ?? null,
        h1Bottom: h1 ? Math.round(box(h1).bottom + window.scrollY) : null,
        // The two numbers P5's fold claim is about, beside the `h1`'s: the bottom of the
        // page's lead paragraph and the bottom of the first command it prints. The claim is
        // that the `h1`, the one-line promise and the first command are inside a 390 × 844
        // first screen — on the homepage, where the promise is the first paragraph and the
        // first command is the first `pre` — and that Getting Started's first screen is its
        // first command. `null` where the page prints no command at all (eight of the
        // eleven routes). Both are measured from the top of the document, so they are read
        // against the viewport's height the same way `h1Bottom` is.
        leadBottom: q('main p') ? Math.round(box(q('main p')).bottom + window.scrollY) : null,
        firstCommandBottom: q('main pre') ? Math.round(box(q('main pre')).bottom + window.scrollY) : null,
        words: (q('main')?.innerText ?? '').trim().split(/\s+/).filter(Boolean).length,
        preCount: document.querySelectorAll('pre').length,
        copyButtons: document.querySelectorAll('[data-copy], .copy-button, button.copy').length,
        inlineScriptBytes: [...document.querySelectorAll('script:not([src])')]
          .reduce((n, s) => n + s.textContent.length, 0),
      }
    })

    const files = []
    if (m.height <= BAND) {
      const name = `${slug}-${vp.name}-fullpage.jpg`
      await page.screenshot({
        path: path.join(OUT, name), fullPage: true, type: 'jpeg',
        quality: QUALITY_FULL, animations: 'disabled', caret: 'hide',
      })
      files.push({ name, bytes: statSync(path.join(OUT, name)).size })
    } else {
      const bands = Math.ceil(m.height / BAND)
      const name = `${slug}-${vp.name}-part01of${String(bands).padStart(2, '0')}.jpg`
      await page.setViewportSize({ width: vp.width, height: BAND })
      await page.screenshot({
        path: path.join(OUT, name), type: 'jpeg',
        quality: QUALITY_BAND, animations: 'disabled', caret: 'hide',
      })
      await page.setViewportSize({ width: vp.width, height: vp.height })
      files.push({ name, bytes: statSync(path.join(OUT, name)).size })
    }

    measured.push({ route, viewport: vp.name, width: vp.width, ...m, files })
  }
  await context.close()
}

await browser.close()
server.close()

const out = process.argv[3] ?? '/tmp/website-screenshots.json'
writeFileSync(out, `${JSON.stringify(measured, null, 1)}\n`)
for (const r of measured) {
  console.log(`${r.route} ${r.viewport} w=${r.scrollWidth} h=${r.height} header=${r.headerHeight} `
    + `navRows=${r.navRows} h1Bottom=${r.h1Bottom} leadBottom=${r.leadBottom} `
    + `firstCommandBottom=${r.firstCommandBottom} words=${r.words} pre=${r.preCount} `
    + `copyButtons=${r.copyButtons} inlineJs=${r.inlineScriptBytes}`)
}
console.log(`measurements written to ${out}`)
