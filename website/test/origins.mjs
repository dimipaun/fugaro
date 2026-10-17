// website/test/origins.mjs — nothing phones home (website plan, W1.11 step 2; reconciliation
// finding 55, decisions D93 and D106, Global Constraint 8).
//
// The promise: a reader's browser that loads this site makes requests to this site and to
// nobody else — no font CDN, no icon CDN, no embedded player, no pixel — with exactly one
// exception the owner chose (D93): Google's Analytics script, loaded only after the visitor
// consents. This file is the mechanical form of that promise, over the BUILT artifact
// (`dist/`), because the built HTML is what a browser fetches from; a source file that looks
// clean can still bundle into a page that is not.
//
// What it checks, per built page and per first-party stylesheet and script in `dist/`:
//
//   1. Every element and attribute that makes the browser fetch something — `script[src]`,
//      `link` with a fetching `rel` (stylesheet, preload, modulepreload, prefetch,
//      preconnect, dns-prefetch, prerender, icon, manifest), `img`/`source` `src` and
//      `srcset`, `video`/`audio`/`track`, `iframe`/`embed`/`object`, `form[action]`,
//      `formaction`, SVG `image`/`use`/`feImage` hrefs, and every CSS `url()`, `@import` and
//      `@font-face` source in a `<style>`, a `style` attribute or a shipped `.css` file — names
//      an origin the check admits:
//        - scripts, stylesheets, preloads, fonts, forms and CSS urls: FIRST-PARTY ONLY (a
//          relative URL, or the canonical origin from site.yaml). This is deliberately
//          stricter than "on the allowed list": `allowed_hosts` lists the hosts the site LINKS
//          to (github.com, raw.githubusercontent.com), and a link target is not a host the
//          project may execute or style a page from (D106: no CDN).
//        - images, media and embeds: first-party, or a host on `allowed_hosts` — which is the
//          rule W2.4's video slot will meet: a player host that is not on the list fails here.
//      `http:`, protocol-relative `//host`, `javascript:` and (for anything executable)
//      `data:` URLs are refused outright, and so are `<base>`, `<meta http-equiv=refresh>`,
//      `ping`, `iframe[srcdoc]` and every inline `on*=` handler.
//   2. Every inline executable `<script>` is on an allow-list by its SHA-256 (below). The list
//      is empty today because the site ships no script at all; a task that adds one (W1.12's
//      consent banner) adds its hash here, in the same commit, as a decision someone reads.
//   3. Every absolute URL inside a shipped `.js` file is first-party.
//   4. The analytics exception, in two states (D93, D106). With `site.yaml`'s
//      `analytics.enabled` false — today — no page names an analytics origin or carries the
//      loader at all. With it true (a build of a temp copy with the flag flipped), the one
//      admitted third-party origin is Google's tag origin, it appears ONLY inside the base
//      layout's consent-gated inline loader (never as a `script[src]`, a preload or a
//      preconnect), no second analytics origin appears, and the loader reads the consent
//      decision before it creates anything. W1.12 owns the banner and the gate's behaviour
//      and widens this file; this file holds the layout's contract: one origin, one gated
//      loader, nothing fetched by markup.
//
// The red step this file was written against: the check, run over a page carrying a Google
// Fonts stylesheet, has to report `fonts.googleapis.com is not on the allowed-host list` —
// the plan's exact line — and the fixture cases below keep that refusal, and the others,
// running on every check.
//
// It reads `dist/`, which `npm run check` builds before it runs this file (set
// `FUGARO_GATES_DIST` to point it at a copy, which is how each refusal was mutation-proved).
// Standard library only; no network.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import {
  DIST,
  HTMLScanError,
  builtPages,
  cssRules,
  cssURLs,
  decodeEntities,
  filesUnder,
  parse,
  siteFacts,
  tokenize,
  walk,
} from './lib/html.mjs'
import { buildCopy } from './lib/build.mjs'

// ---- the allow-lists -------------------------------------------------------------------

// Inline executable scripts the site is allowed to ship, by the base64 SHA-256 of their
// exact text (the same digest a Content-Security-Policy `script-src 'sha256-…'` names), each
// with the reason it exists. Empty: the site ships no script today. The analytics loader is
// not listed here — it is admitted by the analytics contract below, which holds its shape
// rather than its bytes, because W1.12 rewrites it and W3.2 changes its measurement id.
export const INLINE_SCRIPTS = new Map([
  // ['sha256-…', 'what it is, and the task that added it'],
])

// The one third-party origin D93 admits, and the analytics origins that are NOT admitted:
// any of these appearing anywhere is a second analytics vendor or a direct collection
// endpoint, which D106's "exactly one" refuses.
export const ANALYTICS_ORIGIN = 'https://www.googletagmanager.com'
const ANALYTICS_HOST = new URL(ANALYTICS_ORIGIN).host
const ANALYTICS_NAMES = /googletagmanager|google-analytics|analytics\.google|gtag\(|dataLayer|firebase/i
// The consent decision's key, as the base layout writes it (W1.3, owned by W1.12).
const CONSENT_KEY = 'fugaro:analytics-consent'

// `link` relations that make the browser fetch, and the ones that are only a reference.
const FETCHING_RELS = new Set([
  'stylesheet', 'preload', 'modulepreload', 'prefetch', 'preconnect', 'dns-prefetch', 'prerender',
  'icon', 'shortcut', 'apple-touch-icon', 'mask-icon', 'manifest',
])
const REFERENCE_RELS = new Set(['canonical', 'alternate', 'sitemap', 'author', 'license', 'me', 'next', 'prev', 'help', 'home'])

// Executable script types; anything else that is not a JSON data block is refused.
const EXECUTABLE = new Set(['', 'text/javascript', 'application/javascript', 'module'])
const DATA_BLOCK = new Set(['application/ld+json', 'application/json'])

// ---- classifying one URL ---------------------------------------------------------------

/**
 * Why `raw` may not be loaded in `context`, or null when it may. `context` is one of
 * 'first-party' (scripts, styles, fonts, forms, CSS) or 'media' (images, media, embeds).
 */
export function refuse(raw, context, facts) {
  // Read the URL the way a browser's URL parser does before deciding it is relative: it
  // drops ASCII tabs and newlines anywhere and treats a backslash as a slash, so
  // `/\cdn.example.net/x.js` and `/\t/cdn.example.net/x.js` are protocol-relative too.
  const url = raw.trim().replace(/[\t\n\r]/g, '').replace(/\\/g, '/')
  if (url === '') return 'an empty URL'
  if (url.startsWith('//')) return `${url} is protocol-relative: it names a host without saying which, and the site loads nothing from another host`
  const scheme = /^([a-zA-Z][a-zA-Z0-9+.-]*):/.exec(url)
  if (!scheme) return null // relative: this site
  const s = scheme[1].toLowerCase()
  if (s === 'data') {
    return context === 'media' || context === 'css' ? null : `a data: URL as an executable or a document (${url.slice(0, 40)}…)`
  }
  if (s !== 'https') return `${url.slice(0, 60)} uses ${s}: — the site loads over https only, and never a javascript: URL`
  let host
  try {
    host = new URL(url).host
  } catch {
    return `${url} is not a URL`
  }
  if (host === facts.canonicalHost) return null
  if (context === 'media' && facts.allowedHosts.has(host)) return null
  if (context !== 'media' && facts.allowedHosts.has(host)) {
    return `${host} is on the allowed-host list as a link target, but a script, stylesheet, font, preload or form may only come from this site (D106: no CDN)`
  }
  return `${host} is not on the allowed-host list`
}

function srcsetURLs(value) {
  return value
    .split(',')
    .map((part) => part.trim().split(/\s+/)[0])
    .filter(Boolean)
}

/** The CSS a stylesheet loads, and why each is refused (css context). */
function cssFailures(css, where, facts) {
  const out = []
  let rules
  try {
    rules = cssRules(css)
  } catch (err) {
    return [`${where}: the stylesheet could not be read (${err.message}), and a stylesheet the check cannot read is one it cannot vouch for`]
  }
  for (const rule of rules) {
    if (rule.importURL !== undefined) {
      const why = refuse(rule.importURL, 'first-party', facts)
      if (why) out.push(`${where}: @import ${why}`)
    }
  }
  for (const u of cssURLs(css)) {
    const why = refuse(u, 'css', facts)
    if (why) out.push(`${where}: url(${u}) ${why.startsWith(u) ? why.slice(u.length).trim() : why}`.replace(/\s+$/, ''))
  }
  return out
}

/** Every origin failure of one built page. `rel` names it in the failure lines. */
export function pageOriginFailures(html, rel, facts, { analyticsEnabled = facts.analyticsEnabled } = {}) {
  const out = []
  let doc
  try {
    doc = parse(html)
  } catch (err) {
    if (err instanceof HTMLScanError) return [`${rel}: ${err.message}`]
    throw err
  }
  const at = (el, why) => out.push(`${rel}: <${el.name}> ${why}`)
  const check = (el, attr, context) => {
    if (!el.attrs.has(attr)) return
    const value = el.attrs.get(attr)
    const urls = attr === 'srcset' || attr === 'imagesrcset' ? srcsetURLs(value) : [value]
    for (const u of urls) {
      const why = refuse(u, context, facts)
      if (why) at(el, `${attr}: ${why}`)
    }
  }
  const gatedLoaders = []
  // Where an analytics name appears in markup (attributes, script and style text) — prose
  // that mentions a vendor is not a load, so text nodes are not read for this.
  const named = []

  for (const el of walk(doc)) {
    for (const [name, value] of el.attrs) {
      if (/^on/.test(name)) at(el, `carries an inline ${name}= handler: the site's script, if any, is a listed file or a listed inline script, never a handler attribute`)
      if (/^\s*javascript:/i.test(value)) at(el, `${name} is a javascript: URL`)
      if (name === 'ping') at(el, 'carries ping=, a tracking request on every click')
      if (name === 'style') {
        // A style attribute is a declaration list, not a stylesheet: only its url()s load.
        for (const u of cssURLs(value)) {
          const why = refuse(u, 'css', facts)
          if (why) at(el, `style="…url(${u})…": ${why}`)
        }
      }
      if (!analyticsEnabled && ANALYTICS_NAMES.test(value)) named.push(`<${el.name} ${name}>`)
    }
    switch (el.name) {
      case 'base':
        at(el, 'rebases every relative URL on the page to somewhere this check cannot see; the site has no <base>')
        break
      case 'meta':
        if ((el.attrs.get('http-equiv') ?? '').toLowerCase() === 'refresh') at(el, 'http-equiv=refresh navigates the reader away by itself')
        break
      case 'script': {
        if (!analyticsEnabled && ANALYTICS_NAMES.test(el.raw ?? '')) named.push('<script> text')
        if (el.attrs.has('src')) {
          check(el, 'src', 'first-party')
          break
        }
        const type = (el.attrs.get('type') ?? '').toLowerCase()
        if (DATA_BLOCK.has(type)) break
        if (!EXECUTABLE.has(type)) {
          at(el, `has type="${type}", which is neither an executable script this check reads nor a JSON data block`)
          break
        }
        const body = el.raw ?? ''
        if (body.includes(ANALYTICS_HOST)) {
          gatedLoaders.push(body)
          break
        }
        const digest = `sha256-${createHash('sha256').update(body, 'utf8').digest('base64')}`
        if (!INLINE_SCRIPTS.has(digest)) {
          at(el, `is an inline script that is not on the allow-list (${digest}): add it to INLINE_SCRIPTS in website/test/origins.mjs with the reason it exists, or ship no script`)
        }
        for (const m of body.matchAll(/https?:\/\/[^\s"'`)\\]+/g)) {
          const why = refuse(m[0], 'first-party', facts)
          if (why) at(el, `inline script names ${why}`)
        }
        break
      }
      case 'style':
        if (!analyticsEnabled && ANALYTICS_NAMES.test(el.raw ?? '')) named.push('<style> text')
        out.push(...cssFailures(el.raw ?? '', `${rel}: <style>`, facts))
        break
      case 'link': {
        const rels = (el.attrs.get('rel') ?? '').toLowerCase().split(/\s+/).filter(Boolean)
        if (rels.length === 0) at(el, 'has no rel, so the check cannot tell whether it loads anything')
        for (const r of rels) {
          if (FETCHING_RELS.has(r)) {
            check(el, 'href', 'first-party')
            check(el, 'imagesrcset', 'first-party')
          } else if (!REFERENCE_RELS.has(r)) {
            at(el, `rel="${r}" is a relation this check does not know: teach it whether the browser fetches it`)
          }
        }
        break
      }
      case 'img':
      case 'source':
        check(el, 'src', 'media')
        check(el, 'srcset', 'media')
        break
      case 'video':
        check(el, 'src', 'media')
        check(el, 'poster', 'media')
        break
      case 'audio':
      case 'track':
        check(el, 'src', 'media')
        break
      case 'iframe':
      case 'frame':
        if (el.attrs.has('srcdoc')) at(el, 'srcdoc embeds a document this check does not read')
        check(el, 'src', 'media')
        break
      case 'embed':
        check(el, 'src', 'media')
        break
      case 'object':
        check(el, 'data', 'media')
        break
      case 'form':
        check(el, 'action', 'first-party')
        break
      case 'button':
        check(el, 'formaction', 'first-party')
        break
      case 'input':
        check(el, 'formaction', 'first-party')
        check(el, 'src', 'media')
        break
      case 'image':
      case 'use':
      case 'feimage':
        check(el, 'href', 'media')
        check(el, 'xlink:href', 'media')
        break
      default:
        break
    }
  }

  // The analytics exception (D93, D106).
  if (!analyticsEnabled) {
    if (named.length) out.push(`${rel}: ${named.join(', ')} names analytics while site.yaml's analytics.enabled is false: nothing analytics-shaped may reach a page until the owner turns it on (W3.2), and then only behind the consent gate (W1.12)`)
  } else {
    if (gatedLoaders.length > 1) out.push(`${rel}: carries ${gatedLoaders.length} inline scripts naming ${ANALYTICS_HOST}; the layout has exactly one gated loader`)
    for (const body of gatedLoaders) out.push(...loaderFailures(body, rel, facts))
  }
  return out
}

/** The base layout's contract for the one admitted third-party script, read from its text. */
export function loaderFailures(body, rel, facts) {
  const out = []
  for (const m of body.matchAll(/https?:\/\/[^\s"'`)\\]+/g)) {
    const host = new URL(m[0]).host
    if (host !== ANALYTICS_HOST && host !== facts.canonicalHost) {
      out.push(`${rel}: the analytics loader names ${host}: D106 admits exactly one third-party origin, ${ANALYTICS_HOST}`)
    }
  }
  if (/google-analytics|analytics\.google|firebase/i.test(body)) {
    out.push(`${rel}: the analytics loader names a second analytics origin; D106 admits exactly one`)
  }
  if (!body.includes(CONSENT_KEY)) {
    out.push(`${rel}: the analytics loader does not read the consent decision (${CONSENT_KEY}), so the flag alone would decide whether a Google script loads (D93)`)
  }
  if (!/===\s*['"]granted['"]/.test(body)) out.push(`${rel}: the analytics loader never compares the decision with 'granted'`)
  return out
}

/** Absolute URLs in a shipped first-party JavaScript file that are not first-party. */
export function scriptFileFailures(js, rel, facts) {
  const out = []
  for (const m of js.matchAll(/(?:https?:)?\/\/[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}[^\s"'`)\\]*/g)) {
    const why = refuse(m[0], 'first-party', facts)
    if (why) out.push(`${rel}: ${why}`)
  }
  return out
}

/** Every origin failure of a built artifact. */
export function artifactOriginFailures(dist, facts, opts) {
  const out = []
  for (const page of builtPages(dist)) out.push(...pageOriginFailures(page.html, page.rel, facts, opts))
  for (const rel of filesUnder(dist)) {
    if (rel.endsWith('.css')) out.push(...cssFailures(readFileSync(path.join(dist, rel), 'utf8'), rel, facts))
    if (/\.(m?js)$/.test(rel)) out.push(...scriptFileFailures(readFileSync(path.join(dist, rel), 'utf8'), rel, facts))
  }
  return out
}

// ---- the gate --------------------------------------------------------------------------

const FACTS = siteFacts()

test('no built page loads anything from a host that is not this site', () => {
  const failures = artifactOriginFailures(DIST, FACTS)
  assert.deepEqual(failures, [], `the built site loads from an origin D106 does not admit:\n  ${failures.join('\n  ')}`)
})

test('with analytics switched on, the one admitted third-party origin is loaded only by the consent-gated loader', async (t) => {
  // site.yaml as W3.2 will leave it: the flag and a measurement id together (the boundary
  // test requires them to agree). The id is a synthetic, obviously-fake one.
  const built = buildCopy(t, ({ site }) => {
    const file = path.join(site, 'data', 'site.yaml')
    const before = readFileSync(file, 'utf8')
    const after = before.replace(/^  enabled: false$/m, '  enabled: true').replace(/^  measurement_id: ""$/m, '  measurement_id: "G-0000000000"')
    assert.notEqual(after, before, 'the fixture did not switch analytics on in the copy')
    writeFileSync(file, after)
  })
  assert.equal(built.status, 0, `the build with analytics on failed:\n${built.output}`)
  const facts = { ...FACTS, analyticsEnabled: true }
  const failures = artifactOriginFailures(built.dist, facts)
  assert.deepEqual(failures, [], failures.join('\n'))

  // And the loader is actually there on every page — a contract nothing satisfies would pass.
  for (const page of builtPages(built.dist)) {
    const loaders = [...tokenize(page.html)].filter((t) => t.kind === 'start' && t.name === 'script' && (t.raw ?? '').includes(ANALYTICS_HOST))
    assert.equal(loaders.length, 1, `${page.rel}: expected exactly one consent-gated analytics loader with the flag on, found ${loaders.length}`)
    assert.ok(!/<script[^>]*\bsrc=["']?https:\/\/www\.googletagmanager/i.test(page.html), `${page.rel}: emits the Google script as markup, which loads before any consent`)
  }
})

// ---- the refusals, kept ----------------------------------------------------------------

// A minimal page in the shape the layout writes, with one thing planted in its head or body.
function page({ head = '', body = '' } = {}) {
  return `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><title>t</title>${head}</head><body><main id="main"><h1>t</h1>${body}</main></body></html>`
}

const FIXTURE_FACTS = siteFacts(`canonical_url: https://site.example
analytics:
  enabled: false
allowed_hosts:
  - github.com
  - site.example
`)

function failuresOf(html, opts) {
  return pageOriginFailures(html, 'fixture.html', FIXTURE_FACTS, opts)
}

test('the red step: a Google Fonts stylesheet is refused with the plan\'s line', () => {
  const failures = failuresOf(page({ head: '<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Inter">' }))
  assert.equal(failures.length, 1, failures.join('\n'))
  assert.match(failures[0], /fonts\.googleapis\.com is not on the allowed-host list/)
})

test('every way a page can fetch from another host is refused', () => {
  const cases = [
    ['a remote script', { head: '<script src="https://cdn.example.net/x.js"></script>' }, /cdn\.example\.net is not on the allowed-host list/],
    ['a remote font preload', { head: '<link rel="preload" as="font" href="https://fonts.gstatic.com/s/inter.woff2" crossorigin>' }, /fonts\.gstatic\.com/],
    ['a preconnect', { head: '<link rel="preconnect" href="https://fonts.gstatic.com">' }, /fonts\.gstatic\.com/],
    ['a remote @font-face', { head: '<style>@font-face{font-family:x;src:url(https://fonts.gstatic.com/x.woff2)}</style>' }, /fonts\.gstatic\.com/],
    ['a CSS @import', { head: '<style>@import url("https://fonts.googleapis.com/css?family=x");</style>' }, /@import .*fonts\.googleapis\.com/],
    ['a remote background in a style attribute', { body: '<div style="background:url(https://pixel.example.net/p.gif)"></div>' }, /pixel\.example\.net/],
    ['an embedded player', { body: '<iframe src="https://www.youtube-nocookie.com/embed/x" title="v"></iframe>' }, /youtube-nocookie\.com is not on the allowed-host list/],
    ['a tracking pixel', { body: '<img src="https://pixel.example.net/p.gif" alt="">' }, /pixel\.example\.net/],
    ['an image in srcset', { body: '<img src="/a.png" srcset="/a.png 1x, https://img.example.net/a@2x.png 2x" alt="">' }, /img\.example\.net/],
    ['a script from an allowed link host', { head: '<script src="https://github.com/x.js"></script>' }, /link target.*D106/],
    ['a protocol-relative script', { head: '<script src="//cdn.example.net/x.js"></script>' }, /protocol-relative/],
    ['a backslash protocol-relative script', { head: '<script src="/\\cdn.example.net/x.js"></script>' }, /protocol-relative/],
    ['a tab-split protocol-relative script', { head: '<script src="/&#9;/cdn.example.net/x.js"></script>' }, /protocol-relative/],
    ['a plain-http image', { body: '<img src="http://site.example/a.png" alt="">' }, /http:/],
    ['a form posting elsewhere', { body: '<form action="https://collect.example.net/"><label>e<input name="e"></label></form>' }, /collect\.example\.net/],
    ['a <base>', { head: '<base href="https://cdn.example.net/">' }, /<base>/],
    ['a meta refresh', { head: '<meta http-equiv="refresh" content="0;url=https://elsewhere.example.net/">' }, /refresh/],
    ['an inline handler', { body: '<a href="/" onclick="track()">x</a>' }, /onclick/],
    ['a javascript: link', { body: '<a href="javascript:alert(1)">x</a>' }, /javascript: URL/],
    ['a ping', { body: '<a href="/" ping="https://collect.example.net/">x</a>' }, /ping/],
    ['an unlisted inline script', { body: '<script>console.log(1)</script>' }, /not on the allow-list \(sha256-/],
    ['a data: script', { head: '<script src="data:text/javascript,alert(1)"></script>' }, /data: URL/],
    ['an unknown link relation', { head: '<link rel="pingback" href="https://x.example.net/">' }, /does not know/],
    ['the analytics origin while the flag is off', { head: '<link rel="dns-prefetch" href="/">' + '<script>/* googletagmanager */</script>' }, /analytics\.enabled is false/],
  ]
  for (const [what, fixture, expected] of cases) {
    const failures = failuresOf(page(fixture))
    assert.ok(failures.some((f) => expected.test(f)), `${what} was not refused as expected (${expected}); the check said:\n  ${failures.join('\n  ') || '(nothing)'}`)
  }
})

test('first-party, data: images and the allowed media hosts pass', () => {
  const ok = page({
    head: '<link rel="stylesheet" href="/_astro/a.css"><link rel="preload" as="font" type="font/woff2" href="/fonts/x.woff2" crossorigin><link rel="canonical" href="https://site.example/">',
    body: '<img src="/a.svg" alt="a"><img src="data:image/gif;base64,R0lGODlhAQABAAAAACw=" alt=""><img src="https://github.com/a.png" alt="b"><script type="application/ld+json">{"a":1}</script>',
  })
  assert.deepEqual(failuresOf(ok), [])
})

test('the analytics loader contract refuses a second origin, an ungated loader and markup that loads Google directly', () => {
  const on = { analyticsEnabled: true }
  const gated = `(function(){function d(){return localStorage.getItem('${CONSENT_KEY}')}function load(){var s=document.createElement('script');s.src='${ANALYTICS_ORIGIN}/gtag/js';document.head.appendChild(s)}if(d()==='granted')load()})()`
  assert.deepEqual(failuresOf(page({ head: `<script>${gated}</script>` }), on), [])

  const ungated = `var s=document.createElement('script');s.src='${ANALYTICS_ORIGIN}/gtag/js';document.head.appendChild(s)`
  assert.ok(failuresOf(page({ head: `<script>${ungated}</script>` }), on).some((f) => /consent decision/.test(f)))

  const second = gated.replace('/gtag/js', '/gtag/js?x=https://www.google-analytics.com/g/collect')
  assert.ok(failuresOf(page({ head: `<script>${second}</script>` }), on).some((f) => /second analytics origin|names www\.google-analytics\.com/.test(f)))

  const markup = page({ head: `<script async src="${ANALYTICS_ORIGIN}/gtag/js?id=G-0000000000"></script>` })
  assert.ok(failuresOf(markup, on).some((f) => /www\.googletagmanager\.com is not on the allowed-host list/.test(f)))
})

test('a shipped stylesheet or script file naming another host is refused', () => {
  const css = '@font-face{font-family:x;src:url(https://fonts.gstatic.com/x.woff2)}'
  assert.ok(cssFailures(css, '_astro/a.css', FIXTURE_FACTS).some((f) => /fonts\.gstatic\.com/.test(f)))
  assert.ok(scriptFileFailures('import("https://cdn.example.net/m.js")', '_astro/a.js', FIXTURE_FACTS).some((f) => /cdn\.example\.net/.test(f)))
  assert.deepEqual(scriptFileFailures('import("/_astro/b.js")', '_astro/a.js', FIXTURE_FACTS), [])
})

// ---- the scanner the gates stand on ----------------------------------------------------

test('the scanner reads attributes in every quoting style and decodes them', () => {
  const [, el] = tokenize('<p><a href="/a?x=1&amp;y=2" data-x=\'q"\' hidden data-y=bare>x</a></p>')
  assert.equal(el.name, 'a')
  assert.equal(el.attrs.get('href'), '/a?x=1&y=2')
  assert.equal(el.attrs.get('data-x'), 'q"')
  assert.equal(el.attrs.get('hidden'), '')
  assert.equal(el.attrs.get('data-y'), 'bare')
  assert.equal(decodeEntities('&#x2F;&#47;&lt;'), '//<')
})

test('the scanner keeps script and style content raw', () => {
  const doc = parse('<div><script>if (a < b && "</div>") {}</script><style>a>b{}</style></div>')
  const [script, style] = [...walk(doc)].slice(1)
  assert.equal(script.raw, 'if (a < b && "</div>") {}')
  assert.equal(style.raw, 'a>b{}')
})

test('the scanner refuses what it cannot read, rather than skipping it', () => {
  const bad = [
    ['<a href="/x>x</a>', /unterminated quoted value/],
    ['<a href="/" href="/y">x</a>', /duplicated attribute/],
    ['<div><span></div>', /mismatched end tag/],
    ['<p>a < b</p>', /stray "<"/],
    ['<!-- never closed', /unterminated comment/],
    ['<script>x', /never closed/],
    ['<a href="/x&bogus;">x</a>', /unknown named entity/],
    ['<div>', /never closed/],
    ['<img src="/a"></img>', /void element/],
  ]
  for (const [html, expected] of bad) {
    assert.throws(() => parse(html), expected, `the scanner accepted ${html}`)
  }
})

test('the stylesheet reader follows nested conditions and @import', () => {
  const rules = cssRules('@import "/a.css";@supports (x:y){@media (prefers-reduced-motion:no-preference){.a{animation:x 1s}@keyframes x{0%{opacity:0}to{opacity:1}}}}.b{color:red}')
  assert.equal(rules[0].importURL, '/a.css')
  assert.deepEqual(rules[1].conditions, ['@supports (x:y)', '@media (prefers-reduced-motion:no-preference)'])
  assert.equal(rules[1].selector, '.a')
  assert.equal(rules[2].selector, '@keyframes x')
  assert.deepEqual(rules[3].conditions, [])
  assert.throws(() => cssRules('.a{color:red'), /unbalanced/)
})
