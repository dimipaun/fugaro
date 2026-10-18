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
//   2. Every inline executable `<script>` is on an allow-list by the SHA-256 of its exact text
//      (below) — and the list is EMPTY, which is the point: D183 admits no inline script, the
//      site's consent gate and copy control are both code in one first-party file
//      (`/scripts/site.js`), and a page that grew an inline block would need a hash here and in
//      the deployed policy. An empty list is what says "none", and the refusal below still
//      names the digest of whatever appears, so the mechanism stays taught.
//   3. Every absolute URL inside a shipped `.js` file is first-party — the one script file
//      names no URL at all, because the analytics origin reaches its gate through the banner's
//      markup rather than through a constant in the code.
//   4. The analytics exception, in two states (D93, D106; the owner's ruling of 2026-10-18 in
//      the amendment under plan Task W3.2). With `site.yaml`'s `analytics.enabled` false, no
//      page names an analytics origin or carries the gate's configuration at all. With the
//      flag true — the state the site ships in — the one admitted third-party origin is
//      Google's tag origin, it appears in the consent banner's own markup as the gate's
//      configuration (never as a `script[src]`, a preload or a preconnect), no second
//      analytics origin appears anywhere, the page carries no inline script, and the gate that
//      reads that configuration and fetches the script is the site's one script file.
//   5. The deployed policy, in `firebase.json`, admits exactly the origins Google documents
//      for Google Analytics 4 without any Ads features and nothing else — no `'unsafe-inline'`,
//      no `'unsafe-eval'`, no nonce, no hash, no wildcard beyond the two documented ones.
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
  REPO,
  builtPages,
  cssRules,
  cssURLs,
  decodeEntities,
  deployedCSP,
  filesUnder,
  parse,
  siteFacts,
  tokenize,
  walk,
} from './lib/html.mjs'
import { buildCopy } from './lib/build.mjs'

// ---- the allow-lists -------------------------------------------------------------------

// Inline executable scripts the site is allowed to ship, by the base64 SHA-256 of their
// exact text (the same digest a Content-Security-Policy `script-src 'sha256-…'` names).
//
// **The list is empty, and that is the decision (D183, W3.2).** Until W3.2 the site shipped
// two inline scripts when analytics were on — the consent banner's controller and the layout's
// gated loader — and this map carried the banner's digest. Both are now code in the site's one
// first-party script file, `/scripts/site.js`: a file is admitted by the policy's
// `script-src 'self'` and needs no hash, no nonce and no `'unsafe-inline'`, and D183 admits
// exactly one script. So the mechanism below stays — a script the site runs is a decision
// someone reads, and the refusal names the digest of whatever appears — but nothing is on it,
// and a page that grows an inline block fails here and in the policy at once.
export const INLINE_SCRIPTS = new Map()

// The one third-party origin D93 admits, and the analytics origins that are NOT admitted:
// any of these appearing anywhere is a second analytics vendor or a direct collection
// endpoint, which D106's "exactly one" refuses.
export const ANALYTICS_ORIGIN = 'https://www.googletagmanager.com'
const ANALYTICS_HOST = new URL(ANALYTICS_ORIGIN).host
const ANALYTICS_NAMES = /googletagmanager|google-analytics|analytics\.google|gtag\(|dataLayer|firebase/i
// A second analytics origin, by name: the loader may name none of these (the admitted host is
// the one exception, and it is written in the banner's markup, not in any script).
const SECOND_ANALYTICS = /google-analytics|analytics\.google|firebase/i
// The consent decision's key, as the banner's markup carries it (W1.12, W3.2).
const CONSENT_KEY = 'fugaro:analytics-consent'
// The two attributes the banner carries the gate's configuration in, and the site's one
// script file — which is the gate itself (D183, W3.2).
const ANALYTICS_ID_ATTR = 'data-analytics-id'
const ANALYTICS_ORIGIN_ATTR = 'data-analytics-origin'
const SITE_SCRIPT = '/scripts/site.js'
const BANNER_ID = 'consent-banner'

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
  // The elements carrying the gate's configuration: the consent banner, with the property id
  // and the one admitted origin as attributes (W3.2).
  const configs = []

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
      if (name === ANALYTICS_ORIGIN_ATTR) configs.push(el)
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

  // The analytics exception (D93, D106, W3.2).
  if (!analyticsEnabled) {
    if (named.length) out.push(`${rel}: ${named.join(', ')} names analytics while site.yaml's analytics.enabled is false: nothing analytics-shaped may reach a page until the owner turns it on (W3.2), and then only behind the consent gate (W1.12)`)
    if (configs.length) out.push(`${rel}: carries the gate's analytics configuration while site.yaml's analytics.enabled is false: the banner and its configuration are emitted only when the flag is on`)
  } else {
    if (gatedLoaders.length) out.push(`${rel}: carries an inline script naming ${ANALYTICS_HOST}; the gate is code in ${SITE_SCRIPT}, and the site ships no inline script at all (D183, W3.2)`)
    if (configs.length !== 1) {
      out.push(`${rel}: ${configs.length} elements carry ${ANALYTICS_ORIGIN_ATTR}; the gate's configuration is written once, in the consent banner the visitor answers`)
    }
    for (const el of configs) {
      const value = el.attrs.get(ANALYTICS_ORIGIN_ATTR)
      if (value !== ANALYTICS_ORIGIN) {
        out.push(`${rel}: the gate's configuration names ${value}: D106 admits exactly one third-party origin, ${ANALYTICS_ORIGIN}`)
      }
      if (!(el.attrs.get(ANALYTICS_ID_ATTR) ?? '').trim()) {
        out.push(`${rel}: the gate's configuration carries no ${ANALYTICS_ID_ATTR}, so nothing decides which property a visit is reported to`)
      }
      if (el.attrs.get('id') !== BANNER_ID || !el.attrs.has('data-consent-key')) {
        out.push(`${rel}: the gate's configuration is not on the consent banner: the element that asks the question is the element that carries the answer's key and the property it is about`)
      }
      for (const [name, attr] of el.attrs) {
        if (name !== ANALYTICS_ORIGIN_ATTR && SECOND_ANALYTICS.test(attr)) {
          out.push(`${rel}: <${el.name} ${name}> names a second analytics origin (${attr.slice(0, 40)}); D106 admits exactly one, ${ANALYTICS_HOST}`)
        }
      }
    }
    // And the gate itself: the configuration is inert without the file that reads it.
    const scripts = [...walk(doc)].filter((el) => el.name === 'script' && el.attrs.get('src') === SITE_SCRIPT)
    if (scripts.length !== 1) out.push(`${rel}: carries ${scripts.length} ${SITE_SCRIPT} tags; the gate that reads the configuration above is that one file, loaded once (D183, W3.2)`)
  }
  return out
}

/** The gate's contract, read from the one script file rather than from the markup (W3.2). */
export function gateFailures(js, rel, facts) {
  const out = []
  for (const m of js.matchAll(/(?:https?:)?\/\/[a-zA-Z0-9.*-]+\.[a-zA-Z]{2,}[^\s"'`)\\]*/g)) {
    out.push(`${rel}: the gate names ${m[0]}: the origin it may load from reaches it through the banner's markup, so the script itself names no URL at all`)
  }
  if (SECOND_ANALYTICS.test(js)) out.push(`${rel}: the gate names a second analytics origin; D106 admits exactly one`)
  for (const attr of [ANALYTICS_ID_ATTR, ANALYTICS_ORIGIN_ATTR, 'data-consent-key']) {
    if (!js.includes(attr)) out.push(`${rel}: the gate does not read ${attr}, which is where the banner carries what it needs`)
  }
  // The comparison that makes the gate a gate: the decision read out of storage, compared
  // with the one answer that loads anything. A file that reads storage in one place and
  // compares some other value with 'granted' in another is a gate whose two halves never
  // meet, which is why this looks for the two in the same statement rather than for each
  // anywhere. It is a shape, not a proof — `website/test/consent.mjs` runs the file with no
  // decision stored and watches for the request this contract is about.
  if (!/(?:stored\s*\([^)]*\)|getItem\s*\([^)]*\))[^;{}]*===\s*(?:GRANTED|'granted'|"granted")/.test(js)) {
    out.push(`${rel}: the gate never compares the decision it read out of storage with 'granted', so the flag alone would decide whether a Google script loads (D93)`)
  }
  return out
}

// ---- the deployed policy (W3.2) ---------------------------------------------------------

// Every directive `firebase.json` serves and the exact tokens it may carry. The analytics
// entries are **the minimum Google documents for Google Analytics 4 without any Ads
// features**, read on 2026-10-18 from
// <https://developers.google.com/tag-platform/security/guides/csp> ("Google Analytics" →
// "To use Google Analytics without any Ads features"):
//
//   script-src-elem  https://www.googletagmanager.com
//   img-src          https://www.googletagmanager.com https://*.google-analytics.com
//   connect-src      https://www.googletagmanager.com https://*.google-analytics.com
//                    https://*.google.com
//
// Three things about the transcription, each deliberate:
//
//   - the origin goes into `script-src` rather than `script-src-elem`: a policy with no
//     `script-src-elem` falls back to `script-src` for script elements, so the allowance is
//     identical and the policy keeps one script directive;
//   - `https://*.analytics.google.com` is NOT here: older revisions of that page listed it,
//     the current one does not, and this list is "what Google documents" rather than "what
//     was once documented";
//   - `https://*.google.com` in `connect-src` IS here: the documentation requires it for GA4,
//     and the alternative is an analytics script whose collect requests the policy blocks.
//
// Everything else is the policy the site already served, unchanged: `style-src` keeps
// `'unsafe-inline'` because Astro inlines small stylesheets, and there is no `'unsafe-inline'`,
// `'unsafe-eval'`, nonce or hash in `script-src` — the site ships no inline script, so a hash
// would admit nothing and the two unsafe keywords would admit everything.
export const CSP_DIRECTIVES = new Map([
  ['default-src', ["'self'"]],
  ['script-src', ["'self'", ANALYTICS_ORIGIN]],
  ['style-src', ["'self'", "'unsafe-inline'"]],
  ['img-src', ["'self'", 'data:', ANALYTICS_ORIGIN, 'https://*.google-analytics.com']],
  ['font-src', ["'self'"]],
  ['connect-src', ["'self'", ANALYTICS_ORIGIN, 'https://*.google-analytics.com', 'https://*.google.com']],
  ['manifest-src', ["'self'"]],
  ['object-src', ["'none'"]],
  ['base-uri', ["'self'"]],
  ['form-action', ["'self'"]],
  ['frame-ancestors', ["'none'"]],
  ['upgrade-insecure-requests', []],
])

/**
 * Why `policy` is not the policy the site's contract states: every directive present with
 * exactly the tokens it may carry and no others, and no directive missing. An origin added
 * anywhere, an `'unsafe-inline'` or an `'unsafe-eval'`, a nonce, a hash and a wildcard are
 * all the same defect to this reader: a token the contract does not carry.
 */
export function cspFailures(policy) {
  const out = []
  const seen = new Map()
  for (const part of policy.split(';')) {
    const tokens = part.trim().split(/\s+/).filter(Boolean)
    if (tokens.length === 0) continue
    const [name, ...values] = tokens
    if (seen.has(name)) out.push(`${name} is declared twice`)
    seen.set(name, values)
    if (!CSP_DIRECTIVES.has(name)) {
      out.push(`${name} is a directive this contract does not know: teach it what the site may load from`)
      continue
    }
    const allowed = CSP_DIRECTIVES.get(name)
    for (const value of values) {
      if (!allowed.includes(value)) {
        out.push(
          `${name} admits ${value}: the policy may carry only the origins Google documents for Google Analytics 4 without Ads features (${ANALYTICS_ORIGIN}, https://*.google-analytics.com, https://*.google.com) and this site's own, and no 'unsafe-inline', 'unsafe-eval', nonce, hash or wildcard in script-src`,
        )
      }
    }
    for (const value of allowed) {
      if (!values.includes(value)) out.push(`${name} does not carry ${value}, which the site's contract requires`)
    }
  }
  for (const name of CSP_DIRECTIVES.keys()) {
    if (!seen.has(name)) out.push(`the policy carries no ${name} directive`)
  }
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
    if (!/\.(m?js)$/.test(rel)) continue
    const js = readFileSync(path.join(dist, rel), 'utf8')
    out.push(...scriptFileFailures(js, rel, facts))
    // The site's one script file is the consent gate (W3.2): it is held to the gate's own
    // contract here as well as to the origin rule above.
    if (rel === SITE_SCRIPT.replace(/^\//, '')) out.push(...gateFailures(js, rel, facts))
  }
  return out
}

// ---- the gate --------------------------------------------------------------------------

const FACTS = siteFacts()

test('no built page loads anything from a host that is not this site', () => {
  const failures = artifactOriginFailures(DIST, FACTS)
  assert.deepEqual(failures, [], `the built site loads from an origin D106 does not admit:\n  ${failures.join('\n  ')}`)
})

test('with analytics on, the one admitted origin is the banner configuration, and no page ships an inline script', () => {
  // The shipped build IS the flag-on state (W3.2 turned it on in site.yaml), so this case
  // reads the artifact `npm run check` built rather than a copy of it.
  assert.equal(FACTS.analyticsEnabled, true, 'this case is about the state the site ships in, and site.yaml no longer switches analytics on')
  const failures = artifactOriginFailures(DIST, FACTS)
  assert.deepEqual(failures, [], failures.join('\n'))

  for (const page of builtPages(DIST)) {
    const scripts = [...tokenize(page.html)].filter((t) => t.kind === 'start' && t.name === 'script')
    assert.deepEqual(
      scripts.filter((t) => !t.attrs.has('src')).map((t) => t.raw ?? ''),
      [],
      `${page.rel}: the page ships an inline script; D183 admits one script file and no inline block, which is why the policy needs no hash (W3.2)`,
    )
    assert.deepEqual(
      scripts.filter((t) => t.attrs.has('src')).map((t) => t.attrs.get('src')),
      [SITE_SCRIPT],
      `${page.rel}: the site's one script file is ${SITE_SCRIPT}, loaded once (D183)`,
    )
    assert.ok(!/<script[^>]*\bsrc=["']?https:\/\/www\.googletagmanager/i.test(page.html), `${page.rel}: emits the Google script as markup, which loads before any consent`)

    // The gate's configuration, in the banner's markup, written once: the property from
    // site.yaml and the one admitted origin.
    const configs = [...tokenize(page.html)].filter((t) => t.kind === 'start' && t.attrs.has(ANALYTICS_ORIGIN_ATTR))
    assert.equal(configs.length, 1, `${page.rel}: ${configs.length} elements carry ${ANALYTICS_ORIGIN_ATTR}; the gate's configuration is written once`)
    assert.equal(configs[0].name, 'aside', `${page.rel}: the gate's configuration is on a <${configs[0].name}>, not the consent banner`)
    assert.equal(configs[0].attrs.get('id'), BANNER_ID, `${page.rel}: the gate's configuration is not on the consent banner`)
    assert.equal(configs[0].attrs.get(ANALYTICS_ORIGIN_ATTR), ANALYTICS_ORIGIN, `${page.rel}: the gate's origin is not the one D106 admits`)
    assert.equal(
      configs[0].attrs.get(ANALYTICS_ID_ATTR),
      FACTS.analyticsMeasurementID,
      `${page.rel}: the gate's property is not the one site.yaml names, so the page would report to a property nothing declares`,
    )
    assert.equal(configs[0].attrs.get('data-consent-key'), CONSENT_KEY, `${page.rel}: the banner does not carry the consent key the gate reads`)

    // The allow-list is exact, not a prefix and not a pattern: an inline script the site did
    // not admit is refused, with its digest named, which is what makes an empty list a
    // decision rather than a formality.
    const planted = page.html.replace('<main id="main">', '<main id="main"><script>window.planted = true</script>')
    assert.ok(
      pageOriginFailures(planted, page.rel, FACTS).some((f) => /not on the allow-list \(sha256-/.test(f)),
      `${page.rel}: a planted inline script is still admitted, so the allow-list does not hold the site to shipping none`,
    )
  }
})

test('with analytics off, no page and no file of the artifact names the analytics origin', async (t) => {
  // The state the site shipped in before W3.2, built from a copy with the flag cleared: it is
  // the other half of the promise, and the half a flag alone could quietly break.
  const built = buildCopy(t, ({ site }) => {
    const file = path.join(site, 'data', 'site.yaml')
    const before = readFileSync(file, 'utf8')
    const after = before.replace(/^  enabled: true$/m, '  enabled: false').replace(/^  measurement_id: ".*"$/m, '  measurement_id: ""')
    assert.notEqual(after, before, 'the fixture did not switch analytics off in the copy')
    writeFileSync(file, after)
  })
  assert.equal(built.status, 0, `the build with analytics off failed:\n${built.output}`)
  const facts = { ...FACTS, analyticsEnabled: false }
  const failures = artifactOriginFailures(built.dist, facts)
  assert.deepEqual(failures, [], failures.join('\n'))

  for (const page of builtPages(built.dist)) {
    assert.ok(!page.html.includes(ANALYTICS_HOST), `${page.rel} names the analytics origin while the flag is off`)
    assert.ok(!page.html.includes(ANALYTICS_ID_ATTR), `${page.rel} carries the gate's configuration while the flag is off`)
    assert.ok(!page.html.includes(CONSENT_KEY), `${page.rel} carries the consent key while the flag is off`)
  }
  for (const rel of filesUnder(built.dist)) {
    assert.ok(
      !readFileSync(path.join(built.dist, rel), 'utf8').includes(ANALYTICS_HOST),
      `${rel} names the analytics origin while the flag is off: with the flag cleared the artifact is free of it, and the origin reaches the gate only through markup the flag emits`,
    )
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
  measurement_id: ""
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

test('with analytics on, a page that loads Google as markup, carries no configuration, or carries two, is refused', () => {
  const on = { analyticsEnabled: true }
  const banner = (attrs) => `<aside id="${BANNER_ID}" role="region" data-consent-key="${CONSENT_KEY}" ${attrs} hidden></aside>`
  const good = page({
    head: `<script src="${SITE_SCRIPT}" defer></script>`,
    body: banner(`${ANALYTICS_ORIGIN_ATTR}="${ANALYTICS_ORIGIN}" ${ANALYTICS_ID_ATTR}="G-0000000000"`),
  })
  assert.deepEqual(failuresOf(good, on), [])

  // A script element pointing at Google is the defect this whole file exists for: it loads
  // when the browser reaches it, with no decision read and nothing to read one.
  const markup = page({ head: `<script async src="${ANALYTICS_ORIGIN}/gtag/js?id=G-0000000000"></script>` })
  assert.ok(failuresOf(markup, on).some((f) => /www\.googletagmanager\.com is not on the allowed-host list/.test(f)))

  // And an inline block that names the origin is the same defect with a different syntax —
  // refused as an inline script naming the host (D183 admits no inline block at all).
  const inline = page({ head: `<script>var s=document.createElement('script');s.src='${ANALYTICS_ORIGIN}/gtag/js'</script>` })
  assert.ok(
    failuresOf(inline, on).some((f) => /carries an inline script naming www\.googletagmanager\.com/.test(f)),
    failuresOf(inline, on).join('\n'),
  )

  // The configuration is written once: a page with two of them has two answers to which
  // property it reports to.
  const twice = page({ body: banner(`${ANALYTICS_ORIGIN_ATTR}="${ANALYTICS_ORIGIN}" ${ANALYTICS_ID_ATTR}="G-0000000000"`) + banner(`${ANALYTICS_ORIGIN_ATTR}="${ANALYTICS_ORIGIN}" ${ANALYTICS_ID_ATTR}="G-1111111111"`) })
  assert.ok(failuresOf(twice, on).some((f) => /2 elements carry/.test(f)), failuresOf(twice, on).join('\n'))

  // A configuration naming a second analytics origin, or carrying no property, or living
  // somewhere other than the banner the visitor answers.
  const other = page({ body: `<div ${ANALYTICS_ORIGIN_ATTR}="https://www.google-analytics.com" ${ANALYTICS_ID_ATTR}="G-1"></div>` })
  assert.ok(failuresOf(other, on).some((f) => /D106 admits exactly one third-party origin/.test(f)))
  const noID = page({ body: banner(`${ANALYTICS_ORIGIN_ATTR}="${ANALYTICS_ORIGIN}"`) })
  assert.ok(failuresOf(noID, on).some((f) => /carries no data-analytics-id/.test(f)))
  const notBanner = page({ body: `<div id="x" data-consent-key="${CONSENT_KEY}" ${ANALYTICS_ORIGIN_ATTR}="${ANALYTICS_ORIGIN}" ${ANALYTICS_ID_ATTR}="G-1"></div>` })
  assert.ok(failuresOf(notBanner, on).some((f) => /not on the consent banner/.test(f)))

  // A second analytics origin hidden in another attribute of the banner is refused even
  // though the origin attribute itself is right.
  const smuggled = page({ body: banner(`${ANALYTICS_ORIGIN_ATTR}="${ANALYTICS_ORIGIN}" ${ANALYTICS_ID_ATTR}="G-1" data-collect="https://www.google-analytics.com/g/collect"`) })
  assert.ok(failuresOf(smuggled, on).some((f) => /names a second analytics origin/.test(f)), failuresOf(smuggled, on).join('\n'))

  // And the configuration is inert without the file that reads it.
  const noScript = page({ body: banner(`${ANALYTICS_ORIGIN_ATTR}="${ANALYTICS_ORIGIN}" ${ANALYTICS_ID_ATTR}="G-1"`) })
  assert.ok(failuresOf(noScript, on).some((f) => /carries 0 \/scripts\/site\.js tags/.test(f)), failuresOf(noScript, on).join('\n'))
})

test('the gate\'s contract refuses a URL, a second origin, an unread key and a decision nothing compares', () => {
  const gated = `function stored(b){return localStorage.getItem(b.getAttribute('data-consent-key'))}
function analytics(b){var id=b.getAttribute('data-analytics-id');var origin=b.getAttribute('data-analytics-origin')
var s=document.createElement('script');s.src=origin+'/gtag/js?id='+id;if(stored(b)==='granted')document.head.appendChild(s)}`
  assert.deepEqual(gateFailures(gated, 'scripts/site.js', FACTS), [])

  // A URL in the gate is the defect the banner-carries-the-configuration design removes: a
  // hard-coded origin in the script would load Google even on a flag-off build.
  assert.ok(gateFailures(gated.replace("origin+'/gtag/js?id='+id", `'${ANALYTICS_ORIGIN}/gtag/js?id='+id`), 'scripts/site.js', FACTS).some((f) => /names https:\/\/www\.googletagmanager\.com/.test(f)))
  assert.ok(gateFailures(gated.replace('data-consent-key', 'x'), 'scripts/site.js', FACTS).some((f) => /does not read data-consent-key/.test(f)))
  assert.ok(gateFailures(gated.replace('data-analytics-origin', 'x'), 'scripts/site.js', FACTS).some((f) => /does not read data-analytics-origin/.test(f)))
  assert.ok(gateFailures(gated.replace('data-analytics-id', 'x'), 'scripts/site.js', FACTS).some((f) => /does not read data-analytics-id/.test(f)))

  // The consent check itself: with the comparison gone, the flag alone decides whether a
  // Google script loads, which is exactly what the owner's ruling forbids (D93, W3.2).
  const ungated = gated.replace("if(stored(b)==='granted')", '')
  assert.ok(gateFailures(ungated, 'scripts/site.js', FACTS).some((f) => /never compares the decision it read out of storage with 'granted'/.test(f)))

  // And a second analytics origin in the gate.
  assert.ok(gateFailures(gated + `\nvar x='https://www.google-analytics.com/g/collect'`, 'scripts/site.js', FACTS).some((f) => /second analytics origin/.test(f)))
})

test('the deployed policy carries what Google documents for GA4 without Ads features, and nothing else', () => {
  const policy = deployedCSP()
  const failures = cspFailures(policy)
  assert.deepEqual(failures, [], `firebase.json serves a policy this contract does not state:\n  ${failures.join('\n  ')}`)

  // The policy is a promise, so the reader is held to it: each of these is a way the shipped
  // policy could quietly widen, and each is refused with the token named.
  const widen = (from, to) => cspFailures(policy.replace(from, to))
  assert.ok(widen("script-src 'self'", "script-src 'self' 'unsafe-inline'").some((f) => /admits 'unsafe-inline'/.test(f)), 'unsafe-inline in script-src is admitted')
  assert.ok(widen("script-src 'self'", "script-src 'self' 'unsafe-eval'").some((f) => /admits 'unsafe-eval'/.test(f)))
  assert.ok(widen("script-src 'self'", "script-src 'self' 'nonce-abc123'").some((f) => /admits 'nonce-abc123'/.test(f)))
  assert.ok(widen("script-src 'self'", "script-src 'self' 'sha256-abc123'").some((f) => /admits 'sha256-abc123'/.test(f)))
  assert.ok(widen("script-src 'self'", 'script-src *').some((f) => /admits \*/.test(f)))
  assert.ok(widen("img-src 'self' data:", "img-src 'self' data: https://cdn.example.net").some((f) => /img-src admits https:\/\/cdn\.example\.net/.test(f)), 'an extra origin in img-src is admitted')
  assert.ok(widen(`connect-src 'self' ${ANALYTICS_ORIGIN}`, `connect-src 'self' ${ANALYTICS_ORIGIN} https://www.google-analytics.com`).some((f) => /admits https:\/\/www\.google-analytics\.com/.test(f)))
  assert.ok(cspFailures(policy.replace("script-src 'self'", '')).some((f) => /carries no script-src directive/.test(f)))
  assert.ok(cspFailures(`${policy}; report-uri https://x.example.net/`).some((f) => /report-uri is a directive this contract does not know/.test(f)))
  assert.ok(cspFailures(policy.replace("default-src 'self'", "default-src 'self' https://x.example.net")).some((f) => /default-src admits https:\/\/x\.example\.net/.test(f)))

  // The policy is what the artifact is held to, so a page that names an origin the policy
  // would block is the same defect read from the other end: the admitted origin is in it.
  assert.ok(policy.includes(ANALYTICS_ORIGIN), 'the deployed policy does not admit the analytics origin the banner names')
  assert.ok(policy.includes('https://*.google-analytics.com'), 'the deployed policy does not admit the collect endpoint Google documents')
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

test('firebase.json serves exactly one Content-Security-Policy, on every path', () => {
  // `deployedCSP()` reads the first policy it finds, so a second one — a looser policy on a
  // narrower `source`, or a Content-Security-Policy-Report-Only beside it — would be served
  // and never read by the contract above. The deploy config carries one, for `**`, and no
  // other security-policy header at all.
  const config = JSON.parse(readFileSync(path.join(REPO, 'firebase.json'), 'utf8'))
  const policies = []
  for (const entry of config.hosting.headers) {
    for (const header of entry.headers) {
      if (/content-security-policy/i.test(header.key)) policies.push(`${entry.source} ${header.key}`)
    }
  }
  assert.deepEqual(policies, ['** Content-Security-Policy'], `firebase.json serves ${policies.length} security policies (${policies.join('; ')}); the contract reads one, for every path`)
})
