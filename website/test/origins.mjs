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
//        - images and media: first-party, or a host on `allowed_hosts` — which is the
//          rule W2.4's video slot will meet: a player host that is not on the list fails here.
//        - embedded documents (`iframe`, `frame`, `embed`, `object`): FIRST-PARTY ONLY, and
//          never a host the site LINKS to. This is the correction G2.3 made to G2.2: the
//          campaign's booking link put `calendly.com` on `allowed_hosts` so the plain `<a
//          href>` would resolve, and reading that list for embeds too admitted the same host
//          as a frame on every page of the site. A link target and an embed origin are
//          different questions, and the one place the site frames a vendor is the campaign
//          page's inline booking widget, which the consent gate builds at runtime (D401) —
//          a built page never carries the frame, so a built page may frame nobody.
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
//      page loads, runs or embeds anything analytics-shaped and none carries the gate's
//      configuration. An `<a href>` is the one thing not read by that half: a link is a
//      reference the reader follows, not a request the page makes, and the privacy statement's
//      software part links Google's own Firebase policy in both states (the owner's request of
//      2026-10-19) — every external link is still resolved host by host by `linkcheck.mjs`.
//      With the
//      flag true — the state the site ships in — the one admitted third-party origin is
//      Google's tag origin, it appears in the consent banner's own markup as the gate's
//      configuration (never as a `script[src]`, a preload or a preconnect), no second
//      analytics origin appears anywhere, the page carries no inline script, and the gate that
//      reads that configuration and fetches the script is the site's one script file.
//
//   5. **The campaign pages' own measurement, behind its own consent (G2.3, D401).** The four
//      campaign pages carry three further vendors — Meta's pixel, LinkedIn's insight tag and
//      Calendly's booking widget — under a consent of their own (its own key, its own
//      question), and this file holds the same two halves for them: no page carries a vendor
//      request, a vendor origin outside the gate's own configuration attributes, or a framed
//      document; the configuration is written once, on the four campaign pages and on no other
//      page, and it is complete (the page it was built for, the two ids, the three origins and
//      the booking link); and the gate that reads it is the site's one script file, which
//      names none of those origins itself. The origins reach the loader through the banner's
//      markup exactly as the analytics origin does, so a page that carries none of them loads
//      none of them.
//   6. The deployed policy, in `firebase.json`, admits exactly the origins Google documents
//      for Google Analytics 4 without any Ads features, the exact three the campaign's three
//      vendors document, and nothing else — no `'unsafe-inline'`, no `'unsafe-eval'`, no
//      nonce, no hash, and no wildcard beyond the two Google documents.
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
// The four campaign pages, from the data file rather than from a list here: the paths a
// configuration is allowed to appear on are `website/data/landing.yaml`'s own (G2.1), and a
// fifth campaign is a row in that file (G2.3).
import { loadLanding } from '../src/lib/landing.mjs'

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
// The consent banner's early decision (the owner's ruling of 2026-10-23, the redesign's Step 3:
// JavaScript allowed, same-origin only; the banner must not cover the first screen's content).
// A page that renders the banner loads it after the site's one script; it is a first-party file
// the policy admits as script-src 'self', and it reads storage and nothing else.
const EARLY_SCRIPT = '/scripts/consent-early.js'
const BANNER_ID = 'consent-banner'

// The campaign pages' own gate (G2.3, D401): its consent key, the attributes its configuration
// is written in, and the identifiers `site.js` reads it out of the markup with. The key is the
// same value `src/lib/consent-key.mjs` holds, and a change to it is a change to the gate.
export const MARKETING_CONSENT_KEY = 'fugaro:marketing-consent'
export const MARKETING_KEY_ATTR = 'data-marketing-consent-key'
const MARKETING_PATH_ATTR = 'data-marketing-path'
const META_ID_ATTR = 'data-meta-pixel-id'
const META_ORIGIN_ATTR = 'data-meta-origin'
const LINKEDIN_ID_ATTR = 'data-linkedin-partner-id'
const LINKEDIN_ORIGIN_ATTR = 'data-linkedin-origin'
const CALENDLY_URL_ATTR = 'data-calendly-url'
const CALENDLY_ORIGIN_ATTR = 'data-calendly-origin'
// The attribute's own name, as it is written in markup: `site.js` reads the campaign decision
// through a variable holding this string, and a file that names it is a file that asks the
// campaign's question — which is what `gateFailures` scopes the whole campaign rule to.
const MARKETING_KEY_LITERAL = 'data-marketing-consent-key'
// The eight attributes together, which is what says "this element is the campaign gate's
// configuration": the one the loader keys off is the first, and the rest are what it then reads.
const MARKETING_ATTRS = [
  MARKETING_KEY_ATTR,
  MARKETING_PATH_ATTR,
  META_ID_ATTR,
  META_ORIGIN_ATTR,
  LINKEDIN_ID_ATTR,
  LINKEDIN_ORIGIN_ATTR,
  CALENDLY_URL_ATTR,
  CALENDLY_ORIGIN_ATTR,
]

/**
 * The three vendors' origins, from site.yaml: the campaign page's configuration carries these
 * and the deployed policy admits these, and a page naming one of their hosts anywhere else has
 * wired a request around the gate.
 */
function marketingOrigins(facts) {
  return [facts.marketing.metaOrigin, facts.marketing.linkedinOrigin, facts.marketing.calendlyOrigin]
}

/** The three hosts above, as the strings a scan for a stray request matches on. */
function marketingHosts(facts) {
  return new Set(marketingOrigins(facts).map((origin) => new URL(origin).host))
}

/**
 * The registrable domain of a URL: its host without its leftmost labels. It is how the two
 * ends of an embed are held to one vendor — the widget's `assets.` host and the booking page
 * it loads are the same company, and `site.yaml` names only the first of them because the
 * second is a link the pages already carry (G2.3).
 */
function vendorOf(url) {
  try {
    return new URL(url).host.split('.').slice(-2).join('.')
  } catch {
    return null
  }
}

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
 * 'first-party' (scripts, styles, fonts, forms, CSS), 'media' (images and media) or 'embed'
 * (a framed document).
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
  if (context === 'embed') {
    // Nothing a build emits frames another host. The campaign page's inline booking widget is
    // the site's one third-party frame and the consent gate builds it at runtime, after a
    // grant — so a frame in the markup is a frame nobody consented to, and a host the site
    // LINKS to is exactly the host this must not admit (G2.2's `calendly.com`, D401).
    return `${host} is framed by the page: a built page embeds nothing but itself, because the campaign's booking widget is built by the consent gate after a grant and a link target is not an embed origin (D106, D401)`
  }
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
  // And the campaign gate's configuration, which is the same element on the four campaign
  // pages: the eight attributes are what say an element carries it (G2.3, D401), and the page
  // it was built for is the one only the configuration carries.
  const campaignConfigs = []
  const campaignKeys = []
  const campaignHosts = marketingHosts(facts)

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
      if (name === MARKETING_KEY_ATTR) campaignKeys.push(el)
      if (name === MARKETING_PATH_ATTR) campaignConfigs.push(el)
      // The three vendors' hosts, anywhere but the gate's own configuration attributes: an
      // attribute naming one is a request wired around the gate, whatever element carries it —
      // a preconnect, a `data-` hook a smaller script of someone's reads, a form action.
      if (!MARKETING_ATTRS.includes(name)) {
        for (const host of campaignHosts) {
          if (value.includes(host)) {
            at(el, `${name} names ${host}, one of the campaign gate's three origins: they reach the loader through the gate's own configuration attributes, so a page naming one anywhere else has wired a request around the consent (D401)`)
          }
        }
      }
      // An `<a href>` is a reference the reader follows, not something the page loads or runs,
      // so a LINK is not read here — the privacy statement's software part names Google and
      // links Google's own Firebase policy in both flag states (the owner's request of
      // 2026-10-19), and what this rule refuses with the flag off is the site's measurement
      // reaching a page. Every external link is still resolved, host by host, by
      // `linkcheck.mjs`.
      const link = el.name === 'a' && name === 'href'
      // An `id` is a name inside this document and a `#fragment` is a reference to one, so
      // neither can reach a vendor: the zoom drawing names its shapes for what they draw
      // (`#zoom-firebase` is the Firebase card of the owner's boards, and the label beside it
      // is a text node this scan does not read), and a drawing naming a part of itself is not
      // the site's measurement reaching a page (W3.2).
      const local = name === 'id' || value.trim().startsWith('#')
      if (!analyticsEnabled && !link && !local && ANALYTICS_NAMES.test(value)) named.push(`<${el.name} ${name}>`)
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
        check(el, 'src', 'embed')
        break
      case 'embed':
        check(el, 'src', 'embed')
        break
      case 'object':
        check(el, 'data', 'embed')
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
    if (campaignConfigs.length) out.push(`${rel}: carries the campaign gate's configuration while site.yaml's analytics.enabled is false: the site has one consent banner and it carries both questions, and the flag decides whether it is emitted at all`)
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

  // The campaign gate's configuration (G2.3, D401). A page that carries it carries the whole
  // of it — the loader reads eight attributes and a missing one is a loader that silently does
  // nothing — and the three origins it names are site.yaml's own, so a page cannot point a
  // vendor's script somewhere else. Which pages carry it is held by the case below, over the
  // built artifact: this reads the configuration, that one reads who has it.
  if (campaignConfigs.length !== 0 && campaignConfigs.length !== 1) {
    out.push(`${rel}: ${campaignConfigs.length} elements carry ${MARKETING_PATH_ATTR}; the campaign gate's configuration is written once, in the banner the visitor answers`)
  }
  // Two other elements may carry the campaign's key and nothing else, and each is a page where
  // the decision is read or taken back rather than asked:
  //
  //   - the banner's early-decision tag (`consent-early.js`, the owner's ruling of 2026-10-23),
  //     which is the whole reason a campaign page carries the key where the banner is drawn from
  //     the first frame; and
  //   - the bar that always offers the choice — the privacy page, where a visitor withdraws
  //     (`data-consent-always`). A campaign page's bar is gone once it has been answered, so
  //     without this the campaign decision could be granted and never taken back; the key is
  //     what `site.js` writes the refusal under, and the refusal is the only answer that bar may
  //     give (a page that never named the three vendors grants nothing).
  //
  // Anywhere else, a key without a configuration is a decision nothing reads.
  for (const el of campaignKeys) {
    if (el === campaignConfigs[0]) continue
    const carried = MARKETING_ATTRS.filter((name) => el.attrs.has(name))
    const early = el.name === 'script' && el.attrs.get('src') === EARLY_SCRIPT && carried.length === 1
    const withdrawing = el.name === 'aside' && el.attrs.get('id') === BANNER_ID && el.attrs.get('data-consent-always') === 'true' && carried.length === 1
    if (!early && !withdrawing) {
      out.push(`${rel}: <${el.name}> carries ${MARKETING_KEY_ATTR} and is neither the consent banner, its early decision (${EARLY_SCRIPT}) nor the bar that always offers the choice: the campaign's key belongs to the banner that asks the campaign question, or to the page a visitor withdraws on, and a key anywhere else is a decision nothing reads`)
    }
  }
  for (const el of campaignConfigs) {
    if (!el.attrs.has(MARKETING_KEY_ATTR)) {
      out.push(`${rel}: the campaign gate's configuration carries no ${MARKETING_KEY_ATTR}, so the question it asks stores no answer`)
    }
    const missing = MARKETING_ATTRS.filter((name) => !el.attrs.has(name))
    if (missing.length) {
      out.push(`${rel}: the campaign gate's configuration carries no ${missing.join(', no ')}: the loader reads every one of them, and a missing one is a loader that does nothing`)
    }
    if (el.attrs.get('id') !== BANNER_ID || !el.attrs.has('data-consent-key')) {
      out.push(`${rel}: the campaign gate's configuration is not on the consent banner: the element that asks the campaign's question is the element that carries its key and its configuration`)
    }
    if (el.attrs.get(MARKETING_KEY_ATTR) !== MARKETING_CONSENT_KEY) {
      out.push(`${rel}: the campaign gate stores its decision under ${el.attrs.get(MARKETING_KEY_ATTR)}, and the site's own key is ${MARKETING_CONSENT_KEY}: the two decisions are two questions and two keys (F17, D401)`)
    }
    // The path the loader runs on, which is the page's own route: a configuration built for
    // another page is a bar that loads on the page it was not asked on (F18).
    const here = `/${rel.replace(/index\.html$/, '').replace(/\/$/, '')}`
    if (el.attrs.get(MARKETING_PATH_ATTR) !== here) {
      out.push(`${rel}: the campaign gate's configuration is built for ${el.attrs.get(MARKETING_PATH_ATTR)} and this page is ${here}, so the loader would run on the page it was not built for (F18)`)
    }
    for (const [attr, origin] of [
      [META_ORIGIN_ATTR, facts.marketing.metaOrigin],
      [LINKEDIN_ORIGIN_ATTR, facts.marketing.linkedinOrigin],
      [CALENDLY_ORIGIN_ATTR, facts.marketing.calendlyOrigin],
    ]) {
      if (el.attrs.get(attr) !== origin) {
        out.push(`${rel}: ${attr} is ${el.attrs.get(attr)} and site.yaml names ${origin}: the three loaders' origins are one fact each, and the deployed policy admits those exact hosts (O3)`)
      }
    }
    const booking = el.attrs.get(CALENDLY_URL_ATTR) ?? ''
    if (vendorOf(booking) !== vendorOf(el.attrs.get(CALENDLY_ORIGIN_ATTR))) {
      out.push(`${rel}: the embed's booking link is ${JSON.stringify(booking)}, which is not hosted by the vendor the widget's script comes from (${el.attrs.get(CALENDLY_ORIGIN_ATTR)}): a booking address somewhere else is the widget framing a page nobody admitted (D401, O3)`)
    }
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
  // The campaign's own half of the same contract (G2.3, D401): one file, two questions. Every
  // part of the campaign configuration is read out of the campaign page's banner — the page the
  // loader was built for, the two vendor ids, the three origins their scripts come from and the
  // booking link the embed is made of — so a non-campaign page, which carries none of them,
  // loads none of the three. And the decision is compared with the one answer that loads
  // anything, in one statement, the way the analytics decision is: a second question is not a
  // second rule.
  //
  // It binds a file that reads the campaign decision at all: the rule is that a gate which asks
  // the campaign's question reads the whole configuration the banner carries for it, so this is
  // scoped to the attribute that makes the file a campaign gate, not to scripts in general.
  if (js.includes(MARKETING_KEY_LITERAL)) {
    for (const name of [
      'MARKETING_KEY_ATTR', 'MARKETING_PATH_ATTR', 'META_ID_ATTR', 'META_ORIGIN_ATTR',
      'LINKEDIN_ID_ATTR', 'LINKEDIN_ORIGIN_ATTR', 'CALENDLY_URL_ATTR', 'CALENDLY_ORIGIN_ATTR',
    ]) {
      if (!js.includes(name)) {
        out.push(`${rel}: the gate does not read ${name}: the campaign page's banner carries the whole configuration, and a part of it the loader never reads is a part of it that does nothing (G2.3)`)
      }
    }
    if (!/(?:read|stored|getItem)\s*\([^)]*MARKETING_KEY_ATTR[^;{}]*===\s*(?:GRANTED|'granted'|"granted")/.test(js)) {
      out.push(`${rel}: the gate never compares the campaign decision it read out of storage with 'granted', so a campaign page's three vendors would load on the flag alone (D401)`)
    }
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
//
// **The campaign measurement's entries (G2.3, F18, O3, and the owner's ruling of 2026-10-31).**
// Three more vendors arrive with the campaign pages, each behind the campaign's own consent,
// and each is admitted as the exact host its documentation names: `https://connect.facebook.net`
// (the pixel's script), `https://snap.licdn.com` (the insight tag's), `https://assets.calendly.com`
// (the booking widget's script and stylesheet), `https://www.facebook.com` and
// `https://px.ads.linkedin.com` (the two beacons, which are image requests, and the pixel's own
// collect calls). The one wildcard is the owner's own ruling, made for `img-src` alone and for no
// other directive (O3): `https://*.licdn.com`, the host pattern LinkedIn's Insight Tag
// documentation names for the image its tag loads. `script-src` stays wildcard-free, which
// `brand.mjs` holds separately, and nothing else widens. The policy is written per directive
// because the site's consent does not distinguish between them at the browser's level: a
// visitor who declines loads nothing, so what the policy admits is what a consenting visitor
// may reach, host by host, and nothing else. `frame-src` is the new directive: Calendly's
// widget embeds the booking page in an iframe, and the host is the booking link's own — the
// one `landing.yaml` writes — rather than the assets host its script comes from.
export const CSP_DIRECTIVES = new Map([
  ['default-src', ["'self'"]],
  ['script-src', ["'self'", ANALYTICS_ORIGIN, 'https://connect.facebook.net', 'https://snap.licdn.com', 'https://assets.calendly.com']],
  ['style-src', ["'self'", "'unsafe-inline'", 'https://assets.calendly.com']],
  ['img-src', ["'self'", 'data:', ANALYTICS_ORIGIN, 'https://*.google-analytics.com', 'https://www.facebook.com', 'https://px.ads.linkedin.com', 'https://*.licdn.com']],
  ['font-src', ["'self'"]],
  ['connect-src', ["'self'", ANALYTICS_ORIGIN, 'https://*.google-analytics.com', 'https://*.google.com', 'https://connect.facebook.net', 'https://www.facebook.com', 'https://px.ads.linkedin.com']],
  ['frame-src', ['https://calendly.com']],
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
 * anywhere, an `'unsafe-inline'` or an `'unsafe-eval'`, a nonce, a hash and a wildcard — in a
 * directive that does not carry one, which is every directive but `img-src` — are all the same
 * defect to this reader: a token the contract does not carry.
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
          `${name} admits ${value}: the policy carries exactly the tokens this contract lists for ${name} (${allowed.join(', ')}) — the origins Google documents for Google Analytics 4 without Ads features, the exact three the campaign's vendors document, and this site's own — and no 'unsafe-inline', 'unsafe-eval', nonce, hash or wildcard beyond the three the vendors themselves document (Google's two and, in img-src alone, the owner's ruling of 2026-10-31 on LinkedIn's)`,
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
      [SITE_SCRIPT, EARLY_SCRIPT],
      `${page.rel}: the site's one script file is ${SITE_SCRIPT}, loaded once (D183), and the banner's early decision is ${EARLY_SCRIPT} (the owner's ruling of 2026-10-23)`,
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

test('the campaign gate\'s configuration is on the four campaign pages and on no other page', () => {
  // **This case is the red step G2.3 was written against.** Run over the build before the task
  // existed, it failed twice over: the four campaign pages carried no `data-marketing-path` (so
  // the list below was empty against a list of four), and the contract below required three
  // tokens of the deployed policy that the pre-task `firebase.json` did not carry — the two
  // halves of one defect read from two ends, a configuration nothing carries and a policy that
  // would block the load that configuration makes.
  //
  // The configuration is keyed on `data-marketing-path`, not on the key: the key is also what
  // the page a visitor withdraws on carries, without any of the configuration, so a bar that
  // carries the key is not a page that has the vendors' addresses in it.
  const campaign = builtPages(DIST).filter((page) => page.html.includes(`${MARKETING_PATH_ATTR}="`))
  assert.deepEqual(
    campaign.map((page) => page.rel).sort(),
    loadLanding()
      .variants.map((variant) => `${variant.name}/index.html`)
      .sort(),
    `${campaign.length} built pages carry the campaign gate's configuration, and the pages that carry it are the four campaigns website/data/landing.yaml writes: the three vendors load on those pages and nowhere else (spec §6, F18)`,
  )

  for (const page of campaign) {
    const configs = [...tokenize(page.html)].filter((t) => t.kind === 'start' && t.attrs.has(MARKETING_PATH_ATTR))
    assert.equal(configs.length, 1, `${page.rel}: ${configs.length} elements carry ${MARKETING_PATH_ATTR}; the campaign gate's configuration is written once`)
    assert.equal(configs[0].attrs.get('id'), BANNER_ID, `${page.rel}: the campaign gate's configuration is not on the consent banner`)
    assert.equal(
      configs[0].attrs.get(MARKETING_KEY_ATTR),
      MARKETING_CONSENT_KEY,
      `${page.rel}: the campaign decision is stored under ${configs[0].attrs.get(MARKETING_KEY_ATTR)}; it is a second question under its own key, not a second value under the site's (F17, D401)`,
    )
    for (const [attr, name] of [
      [META_ORIGIN_ATTR, 'metaOrigin'],
      [LINKEDIN_ORIGIN_ATTR, 'linkedinOrigin'],
      [CALENDLY_ORIGIN_ATTR, 'calendlyOrigin'],
    ]) {
      assert.equal(configs[0].attrs.get(attr), FACTS.marketing[name], `${page.rel}: ${attr} is not the origin site.yaml names`)
    }
    // The two ids are carried even while they are empty, and the empty state is the loader
    // switched off: no identifier the owner has not supplied is anywhere in the artifact (R3).
    for (const [attr, name] of [[META_ID_ATTR, 'metaPixelID'], [LINKEDIN_ID_ATTR, 'linkedinPartnerID']]) {
      assert.ok(configs[0].attrs.has(attr), `${page.rel}: the campaign gate's configuration carries no ${attr}, so the loader has nothing to switch on when the owner supplies one`)
      assert.equal(configs[0].attrs.get(attr), FACTS.marketing[name], `${page.rel}: ${attr} is not the id site.yaml carries`)
    }
    assert.equal(
      configs[0].attrs.get(CALENDLY_URL_ATTR),
      loadLanding().calendlyURL,
      `${page.rel}: the embed is built from ${configs[0].attrs.get(CALENDLY_URL_ATTR)}, and landing.yaml writes ${loadLanding().calendlyURL}: the booking link has one home`,
    )
  }

  // And the other half, read from the other end: a page that does not carry the configuration
  // carries none of the three origins and none of the campaign's configuration, so a visitor who
  // never opens a campaign page is unchanged by any of this (F17).
  const hosts = [...marketingHosts(FACTS)]
  const campaignRels = new Set(campaign.map((page) => page.rel))
  for (const page of builtPages(DIST).filter((one) => !campaignRels.has(one.rel))) {
    for (const host of hosts) {
      assert.ok(!page.html.includes(host), `${page.rel} names ${host}, one of the campaign gate's three origins, and it is not a campaign page: the origins reach the loader through the four campaign pages' own banner (F18)`)
    }
    // The key may be here, on one element only: the bar that always offers the choice, which is
    // the page a visitor withdraws on — it carries the decision's key and none of the
    // configuration, and `site.js` writes a refusal with it and never a grant. The key's literal
    // is also a disclosure, and the privacy statement is the one page outside the four that
    // names it, exactly as it names the analytics key.
    if (page.rel !== 'privacy/index.html') {
      assert.ok(!page.html.includes(MARKETING_CONSENT_KEY), `${page.rel} names the campaign decision's key and is neither a campaign page nor the privacy statement that discloses it`)
    }
    for (const el of [...tokenize(page.html)].filter((t) => t.kind === 'start' && t.attrs.has(MARKETING_KEY_ATTR))) {
      assert.equal(
        el.attrs.get('data-consent-always'),
        'true',
        `${page.rel} carries the campaign decision's key on <${el.name}> and is not a campaign page: outside the four, the key belongs to the bar that always offers the choice, which is where a decision is taken back (W1.12). The gate's configuration — ${MARKETING_PATH_ATTR}, the ids and the three origins — is the four pages' alone (F18)`,
      )
    }
    assert.ok(!page.html.includes(MARKETING_PATH_ATTR), `${page.rel} carries the campaign gate's configuration and is not a campaign page`)
  }
  for (const rel of filesUnder(DIST)) {
    if (!/\.(html|css|js|mjs|json|webmanifest|svg|xml|txt)$/.test(rel)) continue
    if (campaign.some((page) => page.rel === rel)) continue
    const body = readFileSync(path.join(DIST, rel), 'utf8')
    for (const host of hosts) {
      assert.ok(!body.includes(host), `${rel} names ${host}: the campaign's origins reach the artifact through the four campaign pages' banner and nowhere else, and the site's one script names no host at all (G2.3)`)
    }
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
marketing:
  meta_pixel_id: ""
  meta_origin: "https://connect.facebook.net"
  linkedin_partner_id: ""
  linkedin_origin: "https://snap.licdn.com"
  calendly_origin: "https://assets.calendly.com"
allowed_hosts:
  - github.com
  - calendly.com
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
    ['an embedded player', { body: '<iframe src="https://www.youtube-nocookie.com/embed/x" title="v"></iframe>' }, /youtube-nocookie\.com is framed by the page/],
    ['a booking embed framed from the link host', { body: '<iframe src="https://calendly.com/dimi-fugaro/demo" title="Book a demo"></iframe>' }, /calendly\.com is framed by the page/],
    ['an <object> pointing at a vendor', { body: '<object data="https://snap.licdn.com/x.svg"></object>' }, /snap\.licdn\.com is framed by the page/],
    ['a vendor script', { head: '<script src="https://connect.facebook.net/en_US/fbevents.js"></script>' }, /connect\.facebook\.net is not on the allowed-host list/],
    ['a vendor preconnect', { head: '<link rel="preconnect" href="https://assets.calendly.com">' }, /assets\.calendly\.com is not on the allowed-host list/],
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

test('with the flag off a link naming a vendor is a reference, and a load of one is still refused', () => {
  // The privacy statement's software part links Google's own Firebase policy — and names
  // Firebase — in both flag states (the owner's request of 2026-10-19). A link is not a load:
  // the reader follows it or does not, and the page fetches nothing from it. Everything that
  // fetches or runs keeps being refused by name, which is the half worth having.
  const link = page({ body: '<p>The app reports to <a href="https://firebase.google.com/support/privacy">Google</a>.</p>' })
  assert.deepEqual(failuresOf(link), [], 'a link naming a vendor is read as an analytics load, so the page cannot point at the holder\'s own policy')

  // An `id` names a thing inside this document and `#name` refers to one, so neither reaches a
  // vendor however it is spelled: the zoom drawing names its Firebase card `zoom-firebase` and
  // points the label beside it at `#zoom-firebase`, and that is a drawing naming a part of
  // itself, not the page loading Google (the boards of 2026-10-22).
  const named = page({ body: '<svg><symbol id="firebase-card"></symbol><use href="#firebase-card"></use></svg>' })
  assert.deepEqual(failuresOf(named), [], 'a drawing naming its own part is read as an analytics load, so an SVG cannot name the Firebase card it draws')

  for (const [what, fixture] of [
    ['a script that loads one', { head: '<script src="https://firebase.google.com/x.js"></script>' }],
    ['an inline script naming one', { body: '<script>/* firebase */</script>' }],
    ['an image pointing at one', { body: '<img src="https://firebase.google.com/x.png" alt="">' }],
  ]) {
    assert.ok(
      failuresOf(page(fixture)).some((f) => /analytics\.enabled is false/.test(f)),
      `${what} is not refused while the flag is off, so the link exception has widened into a load exception`,
    )
  }
})

test('first-party, data: images and the allowed media hosts pass', () => {
  const ok = page({
    head: '<link rel="stylesheet" href="/_astro/a.css"><link rel="preload" as="font" type="font/woff2" href="/fonts/x.woff2" crossorigin><link rel="canonical" href="https://site.example/">',
    body: '<img src="/a.svg" alt="a"><img src="data:image/gif;base64,R0lGODlhAQABAAAAACw=" alt=""><img src="https://github.com/a.png" alt="b"><script type="application/ld+json">{"a":1}</script>',
  })
  assert.deepEqual(failuresOf(ok), [])
})

test('a link to a host the site links to is still a link, and the embed rule does not reach it', () => {
  // The pair the G2.2 correction is about, read from both ends in one place: the campaign page's
  // `Book a demo` is a plain `<a href>` to a host on `allowed_hosts`, and it is not a frame. The
  // case above refuses the same host as an `iframe`; this one keeps the link working, because a
  // visitor who declines the marketing consent keeps a way to book (spec §6, F17, D401).
  const link = page({ body: '<a href="https://calendly.com/dimi-fugaro/demo">Book a demo</a><img src="https://calendly.com/a.png" alt="">' })
  assert.deepEqual(failuresOf(link), [], 'a link to a listed host, or an image from one, is read as an embed')
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

  // And the campaign's three, each of which the four campaign pages' banner names — so a policy
  // that dropped one would block a load a consenting visitor asked for, and this is the half of
  // the plan's red step that reads the deployed file (G2.3, F18, O3).
  for (const origin of marketingOrigins(FACTS)) {
    assert.ok(policy.includes(origin), `the deployed policy does not admit ${origin}, whose script a campaign page's own configuration loads after the visitor accepts`)
  }
  // `frame-src` is the new directive, and the frame it admits is the booking page the campaign's
  // `Book a demo` link points at — one value, from `landing.yaml`, so the widget's frame and the
  // link a visitor who declines keeps cannot point at two different places.
  assert.deepEqual(
    CSP_DIRECTIVES.get('frame-src'),
    [new URL(loadLanding().calendlyURL).origin],
    'the policy frames a page and website/data/landing.yaml writes another: the campaign has one booking address',
  )
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
