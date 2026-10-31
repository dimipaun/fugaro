// website/test/consent.mjs — the consent gate (website plan, Task W1.12; reconciliation
// findings 16, 18 and 55, decisions D93 and D106, Global Constraint 8).
//
// D93's answer, in one line: the owner chose Google Analytics 4, and **nothing is requested
// from the analytics origin until the visitor consents**. This file is the mechanical form of
// that promise, and it is the other half of W1.11's origins check: `origins.mjs` proves that
// the analytics origin is the only third party the site may name and that the site's one
// script file may name no origin at all; this file proves that the gate itself holds —
// before a decision, after a refusal, after a withdrawal, and when the browser refuses to
// store the choice at all — by RUNNING that file and watching what it asks for.
//
// **It runs the page the way a browser does, because a gate is a property of what a page
// ASKS FOR.** The built HTML's markup fetches (a `script[src]`, a `link rel=preconnect`, an
// `img`) are recorded first, then the page's scripts — its inline blocks, and the site's one
// script FILE, `/scripts/site.js`, read out of the artifact being run — are executed in
// document order against a stub `window` and `document` that record every request a script
// makes, every `dataLayer` call it queues, every cookie written, and where focus goes. A
// check that read the gate's text and agreed with it would be a check of the comment; this
// one watches the network calls and the calls it makes instead of them.
//
// **The shipped state is the flag-ON state (W3.2), and the flag-off state is the copy.** The
// site used to ship with `analytics.enabled: false` and build a flag-on copy to test the
// gate; W3.2 turned the flag on, so the two have swapped: the artifact `npm run check` builds
// is the gated one, and the copy is built with the flag cleared, to hold the other half of
// the promise — that clearing it leaves an artifact byte-for-byte free of the origin.
//
// The four cases D93's answer requires (the plan's step 5), plus the state the site ships in:
//
//   1. **No analytics request before consent** — and none after a refusal.
//   2. **Consent is withdrawable** — the privacy page always offers the choice again,
//      declining overwrites a stored grant, the next page view requests nothing, and the
//      withdrawal deletes the cookies the accepted tag set (owner's ruling, 2026-10-18).
//   3. **No cookie the site set**, before consent or after it: the site writes no cookie of
//      its own, and the one thing it does with cookies is delete GA4's after a withdrawal —
//      which is what the section below runs the shipped file against a jar to prove.
//   4. **The banner is accessible and keyboard-operable** — an accessible name, two equally
//      prominent native buttons, Escape declining, focus that lands somewhere real when the
//      banner goes, and no interception of the keyboard.
//
// And the privacy defaults W3.2 pinned, which are a property of the same file: the one
// `config` call the gate queues names the property site.yaml names, sends a page view and
// nothing else, and turns Google Signals and ads personalisation off. `dataLayer` is where
// that is visible, and the case below reads it there rather than trusting the source.
//
// The red step, and what it turned out to be. The plan's red step reads: "Write the 'no
// request before consent' case against a page that emits the analytics tag unconditionally.
// Expected: FAIL — page requests googletagmanager.com before consent." In this tree that page
// cannot be built: W1.11 (#530) already rewrote the layout's loader so it fetches nothing
// until consent, so the case passes by REFUSING the defect — over three ways of planting it
// (a tag in the markup, a tag a script appends, a preconnect) and once more as a mutation of
// the real gate: strip the consent comparison out of `/scripts/site.js` as the site builds it
// and the plan's line comes back. What actually failed before W1.12's code existed was the
// other half of D93 — consent itself. With no banner there was no way to give it, so the
// analytics script could never load on any page, and the privacy statement did not exist.
//
// It reads `dist/`, which `npm run check` builds before it runs this file, and it builds a
// second copy of the site with `site.yaml`'s flag cleared, because a gate that only ever ran
// against the state it ships in would prove nothing about the flag that turns it off.
// Standard library only; no network, no browser.

import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import {
  DIST,
  REPO,
  SITE,
  builtPages,
  cssDeclarations,
  cssRules,
  filesUnder,
  find,
  findAll,
  parse,
  servedFile,
  siteFacts,
  textOf,
  tokenize,
  walk,
} from './lib/html.mjs'
import { copyRepository } from './lib/build.mjs'
import { controlTargetFailures } from './lib/controls.mjs'
import { loadLanding } from '../src/lib/landing.mjs'

// ---- the facts of the gate ---------------------------------------------------------------

// The one third-party origin D93 admits, the key the decision is stored under, and the page
// that states the whole arrangement. The origin and the key are the same two values
// `origins.mjs` and `Base.astro` hold; a change to either is a change to the gate.
export const ANALYTICS_ORIGIN = 'https://www.googletagmanager.com'
const ANALYTICS_HOST = new URL(ANALYTICS_ORIGIN).host
export const CONSENT_KEY = 'fugaro:analytics-consent'
const PRIVACY_PATH = '/privacy/'

// The site's one script file (D183), which is the gate itself since W3.2 moved the banner's
// controller and the gated loader out of the markup and into it.
const SITE_SCRIPT = '/scripts/site.js'
// The campaign pages' own consent (G2.3, D401): its key, its three origins — read from
// `site.yaml`, which is where the loader reads them too — and the page the cases below run on.
// The three are in the order `firebase.json`'s `script-src` lists them, which is the order
// site.yaml writes them in.
export const MARKETING_CONSENT_KEY = 'fugaro:marketing-consent'
const CAMPAIGN = siteFacts().marketing
const CAMPAIGN_ORIGINS = [CAMPAIGN.metaOrigin, CAMPAIGN.linkedinOrigin, CAMPAIGN.calendlyOrigin]
// The four campaign pages `website/data/landing.yaml` writes, and the one the cases below use.
// The path is the browser's own spelling — the pages are directories, so a visitor is at
// `/save/` — which is also the spelling the gate's path check has to read.
const CAMPAIGN_PAGE = 'save/index.html'
const CAMPAIGN_PATH = '/save/'
// The pixel id, read from `site.yaml` rather than written here: the owner supplied it on
// 2026-10-31, so the built page carries it and the cases below run the page as served — exactly
// as they do for the partner id. A literal in this file would be the second copy of the id that
// the file's own rule refuses.
const META_ID = CAMPAIGN.metaPixelID
// The consent banner's early decision (the owner's ruling of 2026-10-23, the redesign's Step 3:
// JavaScript allowed, same-origin only; the banner must not cover the first screen's content).
// A page that renders the banner loads it after the site's one script; it is a first-party file
// the policy admits as script-src 'self', and it reads storage and nothing else.
const EARLY_SCRIPT = '/scripts/consent-early.js'

// The measurement id, read from `site.yaml` rather than written here: the id lives in exactly
// one place (W3.2), and a literal in this file would be the second copy the brief forbids.
// It is a public identifier — it is in every page's markup — and not a secret.
const MEASUREMENT_ID = siteFacts().analyticsMeasurementID
assert.ok(MEASUREMENT_ID.length > 0, 'site.yaml carries no analytics measurement id, so there is no gate to test')

// `link` relations that make a browser fetch before any script runs.
const FETCHING_RELS = new Set([
  'stylesheet', 'preload', 'modulepreload', 'prefetch', 'preconnect', 'dns-prefetch', 'prerender',
  'icon', 'shortcut', 'apple-touch-icon', 'mask-icon', 'manifest',
])

// Executable script types, which is what this stub runs.
const EXECUTABLE = new Set(['', 'text/javascript', 'application/javascript', 'module'])

// ---- a browser, small enough to read -----------------------------------------------------

/** The event object the banner's script constructs (`new CustomEvent(type, { detail })`). */
class StubEvent {
  constructor(type, init = {}) {
    this.type = type
    this.detail = init.detail
  }
}

// The selectors the site's one script file asks the document for, and what each one means
// here. A selector this map does not carry is refused rather than matched to nothing: a stub
// that answers "no elements" to a query it does not understand is a stub that silently
// disables the code under test.
const DOCUMENT_SELECTORS = new Map([
  ['[data-consent]', (el) => el.attrs.has('data-consent')],
  ['[data-copy]', (el) => el.attrs.has('data-copy')],
  ['[data-copy-button]', (el) => el.attrs.has('data-copy-button')],
  ['[data-copy-status]', (el) => el.attrs.has('data-copy-status')],
  // The site's sliders: one block per page, found by its own attribute and by nothing else —
  // the console page's screenshot slider and the homepage's. `site.js` asks for this on every
  // page it starts on, and the pages these cases run are the built ones, which include both.
  // Nothing below reads a slider — a slider has nothing to do with consent — but the query is
  // made on every page, and the stub's rule is that a selector is taught here rather than
  // answered with nothing. What is inside a block is asked of the block, not of the document
  // (the map below).
  ['[data-slider]', (el) => el.attrs.has('data-slider')],
  ['.site-nav details[open]', (el) => el.name === 'details' && el.attrs.has('open')],
  // The keyboard's way into a group asks for the groups themselves, whatever the device answers
  // (the pointer's half above asks for the open ones). Every shipped page carries the menu, and
  // this is the query that proves the keyboard's half is not behind the pointer's: the stub
  // answers no `matchMedia`, so nothing hover-bound is ever bound here, and the groups are.
  ['.site-nav-group details', (el) => el.name === 'details'],
])

// The selectors the site's one script file asks an *element* for, on the same terms as the
// document's map above: refused rather than matched to nothing. The menu's keyboard half reads
// each group's own control to know what opens it.
const ELEMENT_SELECTORS = new Map([
  ['[data-consent]', (el) => el.attrs.has('data-consent')],
  ['summary', (el) => el.name === 'summary'],
  // The two sliders' own hooks, read inside the block the script finds each by — both pages'
  // sliders are one component since 2026-10-21, so both carry the same four: the track the
  // console side scrolls (and the homepage side reads to know it is not a track), the slides
  // it counts, the dots it marks, and the bar it reveals. Every one of them is asked for on a
  // page that carries a slider — the built pages these cases run are the homepage and the
  // console page, one of each — so every one of them is taught here. The slides and the dots
  // are asked of the block rather than of the document, which is why they are here and not in
  // the map above; the two step controls, which both sliders read, are taught below.
  ['[data-slider-track]', (el) => el.attrs.has('data-slider-track')],
  ['[data-slider-bar]', (el) => el.attrs.has('data-slider-bar')],
  ['[data-slider-counter]', (el) => el.attrs.has('data-slider-counter')],
  ['[data-slider-slide]', (el) => el.attrs.has('data-slider-slide')],
  ['[data-slider-dot]', (el) => el.attrs.has('data-slider-dot')],
  ['[data-slider-prev]', (el) => el.attrs.has('data-slider-prev')],
  ['[data-slider-next]', (el) => el.attrs.has('data-slider-next')],
])

// The address a stubbed page is "at", and the page it was reached from: both carry a query
// string and a fragment with something in them that must never reach Google, so a gate that
// reports the whole address is caught by every case that reads what it queued.
const PAGE_LOCATION = Object.freeze({
  href: 'https://site.example/docs/?token=leaked-query#leaked-fragment',
  origin: 'https://site.example',
  hostname: 'site.example',
  pathname: '/docs/',
  search: '?token=leaked-query',
  hash: '#leaked-fragment',
})
const PAGE_REFERRER = 'https://site.example/?q=leaked-referrer#leaked-referrer-fragment'

// The same page on a host with a label to spare, whose registrable parent (`site.example`) is
// the domain Google's tag may have written cookies to instead of the host. A withdrawal has
// to reach a cookie left there too, and this is the only address a case can prove it from.
const SUBDOMAIN_LOCATION = Object.freeze({
  href: 'https://www.site.example/docs/?token=leaked-query#leaked-fragment',
  origin: 'https://www.site.example',
  hostname: 'www.site.example',
  pathname: '/docs/',
  search: '?token=leaked-query',
  hash: '#leaked-fragment',
})

// A campaign page, at the address a browser serves it from, carrying the same query string and
// fragment the other location does: the gate reports a vendor the page's path and nothing else,
// so a case that reads what it queued catches a query that leaked through the same way.
const CAMPAIGN_LOCATION = Object.freeze({
  ...PAGE_LOCATION,
  href: `https://site.example${CAMPAIGN_PATH}?token=leaked-query#leaked-fragment`,
  pathname: CAMPAIGN_PATH,
})

/** The host a location names, which is where a cookie with no `Domain` attribute lives. */
function hostOf(location) {
  return location.hostname ?? new URL(location.origin).hostname
}

/** The registrable parent of a host, as `site.js` derives it: the leftmost label dropped. */
function parentOf(host) {
  const labels = String(host).split('.')
  return labels.length > 2 ? labels.slice(1).join('.') : null
}

/**
 * The path a cookie with no `Path` attribute lands on, which RFC 6265 calls the default-path:
 * the request's own directory, and `/` when there is no directory to name. It is not a
 * footnote — a write of `name=; expires=…` with no `Path` lands HERE, not on `/`, so a
 * deletion that forgets `Path=/` does not match a cookie Google set at `/`, and the jar below
 * is keyed on the path so that the case can see it.
 */
function defaultPathOf(pathname) {
  const path = String(pathname ?? '')
  if (!path.startsWith('/')) return '/'
  const rightmost = path.lastIndexOf('/')
  return rightmost <= 0 ? '/' : path.slice(0, rightmost)
}

/** Whether a cookie on `cookiePath` is sent with a request for `requestPath` (RFC 6265 §5.4). */
function pathMatches(cookiePath, requestPath) {
  if (cookiePath === requestPath) return true
  if (!requestPath.startsWith(cookiePath)) return false
  if (cookiePath.endsWith('/')) return true
  return requestPath.charAt(cookiePath.length) === '/'
}

/**
 * One `Set-Cookie` string, as the jar below reads it: the name, the value it carries, the
 * domain it lands on (the host by default), the path it lands on (`Path`, or the request's own
 * directory when the write names none) and whether it is an expiry in the past — which is how
 * a cookie is DELETED, and the only kind of write a withdrawal may make.
 */
function parseCookie(raw, host, defaultPath) {
  const [pair, ...attrs] = String(raw).split(';')
  const at = pair.indexOf('=')
  const name = (at === -1 ? pair : pair.slice(0, at)).trim()
  if (!name) return null
  const value = at === -1 ? '' : pair.slice(at + 1).trim()
  let domain = host
  let path = defaultPath
  let expires = null
  for (const attr of attrs) {
    const eq = attr.indexOf('=')
    const key = (eq === -1 ? attr : attr.slice(0, eq)).trim().toLowerCase()
    const val = eq === -1 ? '' : attr.slice(eq + 1).trim()
    if (key === 'domain') domain = val.replace(/^\./, '').toLowerCase()
    if (key === 'path') path = val.startsWith('/') ? val : defaultPath
    if (key === 'expires') expires = val
    if (key === 'max-age') expires = Number(val) <= 0 ? 'past' : null
  }
  const past = expires === 'past' || (expires !== null && Date.parse(expires) <= Date.now())
  return { name, value, domain, path, expired: past }
}

/** The element tree of a page, as the objects the scripts above see. */
function stubDOM(html, location = PAGE_LOCATION) {
  const state = { activeElement: null }
  const elements = []
  const byId = new Map()
  const documentListeners = new Map()

  const document = {
    // The document has been parsed by the time a `defer` script runs, which is when the
    // site's file sees it: `start()` runs immediately rather than waiting for an event this
    // stub would have to fire.
    readyState: 'complete',
    head: { appendChild: (element) => state.requests.push(element.src) },
    createElement: (name) => ({ name, src: '', async: false }),
    getElementById: (id) => byId.get(id) ?? null,
    get activeElement() {
      return state.activeElement
    },
    querySelectorAll: (selector) => {
      const matches = DOCUMENT_SELECTORS.get(selector)
      assert.ok(
        matches,
        `a page script asks the document for ${selector}: this stub reads ${[...DOCUMENT_SELECTORS.keys()].join(', ')}, so a new one has to be taught here rather than silently matching nothing`,
      )
      return elements.filter(matches)
    },
    addEventListener(type, handler) {
      if (!documentListeners.has(type)) documentListeners.set(type, [])
      documentListeners.get(type).push(handler)
    },
    fire(type, event) {
      for (const handler of documentListeners.get(type) ?? []) handler(event)
    },
  }

  const build = (node) => {
    const el = {
      name: node.name,
      attrs: new Map(node.attrs),
      // The `hidden` attribute, as the property the scripts set: a banner the markup hides
      // is one a script has to show.
      hidden: node.attrs.has('hidden'),
      handlers: new Map(),
      children: [],
      parent: null,
      addEventListener(type, handler) {
        if (!this.handlers.has(type)) this.handlers.set(type, [])
        this.handlers.get(type).push(handler)
      },
      fire(type, event) {
        for (const handler of this.handlers.get(type) ?? []) handler(event)
      },
      getAttribute(name) {
        return this.attrs.has(name) ? this.attrs.get(name) : null
      },
      hasAttribute(name) {
        return this.attrs.has(name)
      },
      setAttribute(name, value) {
        this.attrs.set(name, value)
      },
      focus() {
        state.activeElement = this
      },
      getClientRects: () => [],
      classList: { contains: (name) => (node.attrs.get('class') ?? '').split(/\s+/).includes(name) },
      // The selectors the site's scripts use, over the element's own subtree.
      querySelectorAll(selector) {
        const matches = ELEMENT_SELECTORS.get(selector)
        assert.ok(
          matches,
          `a page script asks for ${selector} on an element: this stub reads ${[...ELEMENT_SELECTORS.keys()].join(', ')}, so a new one has to be taught here rather than silently matching nothing`,
        )
        const out = []
        const walkFrom = (node) => {
          for (const child of node.children) {
            if (matches(child)) out.push(child)
            walkFrom(child)
          }
        }
        walkFrom(this)
        return out
      },
      // The nearest ancestor-or-self matching a selector, which is the rule the sliders are
      // read by: an element belongs to a block only when `closest('[data-slider]')` is that
      // block (`site.js`'s `own()`), and that is what keeps one slider's own slides out of the
      // other's reading since the homepage's "See it" panel became the shared component. A
      // selector neither map above teaches is refused here for the same reason as everywhere
      // else in this stub: matching nothing is not the same as answering.
      closest(selector) {
        const matches = DOCUMENT_SELECTORS.get(selector) ?? ELEMENT_SELECTORS.get(selector)
        assert.ok(
          matches,
          `a page script asks an element for the closest ${selector}: this stub reads ${[...DOCUMENT_SELECTORS.keys(), ...ELEMENT_SELECTORS.keys()].join(', ')}, so a new one has to be taught here rather than silently matching nothing`,
        )
        for (let node = this; node !== null; node = node.parent) if (matches(node)) return node
        return null
      },
      querySelector(selector) {
        return this.querySelectorAll(selector)[0] ?? null
      },
    }
    elements.push(el)
    const id = node.attrs.get('id')
    if (id !== undefined) byId.set(id, el)
    for (const child of node.children ?? []) {
      if (child.name === undefined) continue
      const built = build(child)
      built.parent = el
      el.children.push(built)
    }
    return el
  }

  const root = parse(html)
  for (const child of root.children ?? []) if (child.name !== undefined) build(child)
  // The `<html>` element, which is where the banner's early decision leaves its one mark
  // (`data-consent-ask`, `consent-early.js`): the page is drawn from the attribute, so a case
  // has to be able to read it off the element the script set it on. A page whose markup has no
  // `<html>` gets the same holder, since the script sets the attribute rather than reading it.
  document.documentElement =
    elements.find((el) => el.name === 'html') ??
    { attrs: new Map(), getAttribute: (name) => null, setAttribute(name, value) { this.attrs.set(name, value) }, removeAttribute(name) { this.attrs.delete(name) } }

  // `document.cookie`, as a browser presents it: the cookies the page may read — the host's
  // own and the parent domain's — a write replacing the cookie of the same name and domain,
  // and an expiry in the past removing it. The raw writes are kept beside the jar because the
  // two answer different questions: what a script DID (`cookies`) and what a visitor's
  // browser is left HOLDING (`jar`, and `held()` below). The site's withdrawal deletes
  // cookies, which is a write in the first list and nothing at all in the second.
  const host = hostOf(location)
  const parent = parentOf(host)
  const requestPath = location.pathname ?? '/'
  const defaultPath = defaultPathOf(requestPath)
  const cookies = [] // every `Set-Cookie` string a script wrote, in order
  const reads = [] // every read of `document.cookie`, which is work a refusal owes nobody
  // The cookies in force: { name, value, domain, path }. A cookie is a (name, domain, path)
  // triple, which is why a delete that names the wrong path does not remove one.
  const jar = []

  /** Put one parsed cookie into the jar, as a browser applies a `Set-Cookie` string. */
  const applyCookie = (parsed) => {
    if (parsed === null) return
    // A page may only set a cookie on its own host or on a domain it is under. Anything else
    // is what a browser ignores, and the jar ignores it the same way.
    if (parsed.domain !== host && parsed.domain !== parent) return
    const at = jar.findIndex((c) => c.name === parsed.name && c.domain === parsed.domain && c.path === parsed.path)
    if (parsed.expired) {
      if (at !== -1) jar.splice(at, 1)
      return
    }
    if (at === -1) jar.push({ name: parsed.name, value: parsed.value, domain: parsed.domain, path: parsed.path })
    else jar[at].value = parsed.value
  }

  /** The cookies this page can read: the host's and the parent's, on a path it is under. */
  const visible = () => jar.filter((c) => pathMatches(c.path, requestPath))

  Object.defineProperty(document, 'cookie', {
    get: () => {
      reads.push(true)
      return visible()
        .map((c) => `${c.name}=${c.value}`)
        .join('; ')
    },
    set: (value) => {
      cookies.push(String(value))
      applyCookie(parseCookie(value, host, defaultPath))
    },
  })
  document.cookies = cookies
  document.cookieReads = reads
  document.jar = jar
  /**
   * The names the page is holding, which is what a reader of `document.cookie` would see — so
   * a cookie set at `/` that a delete matched at `/privacy` is still named here, which is the
   * defect the path-keyed jar exists to catch.
   */
  document.held = () => visible().map((c) => c.name)
  /**
   * The names a script WROTE that the browser is still holding: a cookie the page set and did
   * not remove. A deletion is a write and leaves nothing, which is the whole difference
   * between the site's withdrawal and the defect `gateFailures` refuses.
   */
  document.survivingWrites = () => {
    const out = []
    for (const raw of cookies) {
      const parsed = parseCookie(raw, host, defaultPath)
      if (parsed === null || parsed.expired) continue
      if (jar.some((c) => c.name === parsed.name && c.domain === parsed.domain && c.path === parsed.path)) out.push(parsed.name)
    }
    return [...new Set(out)]
  }

  state.requests = []
  return { document, elements, byId, state }
}

/**
 * Run a page: its markup fetches, then its scripts in document order, against a stub window
 * and document. `readable` and `writable` are the two halves of a browser that refuses
 * storage — a browser can refuse either, and the gate has to survive both.
 *
 * `dist` is the artifact the page came from: the site's one script file is read out of it and
 * run in the page's place, which is what makes this a test of the gate the site ships rather
 * than of a copy of it written here. The banner's early decision (`consent-early.js`) is read
 * out of the same artifact and run where the page puts it, so the mark it leaves on `<html>`
 * is a fact the cases can read rather than a file no case ever runs. `script` overrides the
 * site's own source, for the mutation cases that need that file with one line changed.
 *
 * `cookies` are the cookies the browser is ALREADY holding when the page loads — what a
 * visitor who consented on an earlier page view brings with them. They are in the jar from
 * the start and are not in the page's own writes, because the page did not write them.
 */
export function runPage(html, { dist = DIST, script = null, storage = {}, readable = true, writable = true, location = PAGE_LOCATION, referrer = PAGE_REFERRER, cookies = [], motion = false } = {}) {
  const { document, elements, byId, state } = stubDOM(html, location)
  document.referrer = referrer
  for (const cookie of cookies) document.cookie = cookie
  document.cookies.length = 0 // planted, not written by the page
  document.cookieReads.length = 0 // and not read by it either
  const requests = state.requests
  const store = { ...storage }
  const windowListeners = new Map()
  const timers = []

  const window = {
    location,
    localStorage: {
      getItem(key) {
        if (!readable) throw new Error('the browser refuses storage')
        return Object.prototype.hasOwnProperty.call(store, key) ? store[key] : null
      },
      setItem(key, value) {
        if (!writable) throw new Error('the browser refuses storage')
        store[key] = String(value)
      },
      removeItem(key) {
        if (!writable) throw new Error('the browser refuses storage')
        delete store[key]
      },
    },
    addEventListener(type, handler) {
      if (!windowListeners.has(type)) windowListeners.set(type, [])
      windowListeners.get(type).push(handler)
    },
    dispatchEvent(event) {
      for (const handler of windowListeners.get(event.type) ?? []) handler(event)
      return true
    },
    setTimeout(handler, delay) {
      timers.push({ handler, delay })
      return timers.length
    },
  }

  // A browser that can run the banner's collapse (owner-approved motion opportunities
  // 2026-10-23): it answers media queries (no hover, no preference about motion) and can take
  // an attribute off an element. Without `motion` the stub is the old browser that is answered
  // at once, which is what every other case here reads.
  if (motion) {
    window.matchMedia = () => ({ matches: false, addEventListener() {}, addListener() {} })
    for (const el of elements) {
      el.removeAttribute = function (name) {
        this.attrs.delete(name)
      }
    }
  }

  // What the browser asks for before a single script runs: a script element, a link that
  // fetches, an image. A loader tag in the markup is a request before consent, and this is
  // where it is caught.
  for (const el of elements) {
    if (el.name === 'script' && el.attrs.has('src')) requests.push(el.attrs.get('src'))
    if (el.name === 'link') {
      const rels = (el.attrs.get('rel') ?? '').toLowerCase().split(/\s+/)
      if (rels.some((rel) => FETCHING_RELS.has(rel))) requests.push(el.attrs.get('href') ?? '')
    }
    if (['img', 'iframe', 'embed', 'source', 'video', 'audio', 'track'].includes(el.name) && el.attrs.has('src')) {
      requests.push(el.attrs.get('src'))
    }
  }

  // Then the page's own scripts, in the order a browser reaches them: an inline block is run
  // as it stands, and a `src` naming the site's one script file — or the banner's early
  // decision, which is the file that decides the first frame — is read out of the artifact and
  // run in its place. Nothing else is fetched — a page that named another script would have to
  // be a page this stub could read, and `origins.mjs` is the gate that refuses one.
  for (const token of tokenize(html)) {
    if (token.kind !== 'start' || token.name !== 'script') continue
    if (!EXECUTABLE.has((token.attrs.get('type') ?? '').toLowerCase())) continue
    const src = token.attrs.get('src')
    if (src !== undefined) {
      if (src !== SITE_SCRIPT && src !== EARLY_SCRIPT) continue
      const source =
        src === SITE_SCRIPT && script ? script : readFileSync(path.join(dist, src.replace(/^\//, '')), 'utf8')
      // `document.currentScript` is the element the browser is running, which is how the early
      // decision reads its own tag's keys: the element the page built for this very tag, so the
      // keys the layout wrote are the keys the case sees.
      document.currentScript = elements.find((el) => el.name === 'script' && el.attrs.get('src') === src) ?? null
      new Function('window', 'document', 'navigator', 'CustomEvent', source)(window, document, {}, StubEvent)
      document.currentScript = null
      continue
    }
    new Function('window', 'document', 'navigator', 'CustomEvent', token.raw ?? '')(window, document, {}, StubEvent)
  }

  const analyticsRequests = () => requests.filter((url) => url.includes(ANALYTICS_HOST))
  return {
    requests,
    // Every `Set-Cookie` string the page's scripts wrote, in order — including the deletions
    // a withdrawal makes, which is why `held()` is the list a cookie case reads.
    cookies: document.cookies,
    cookieReads: document.cookieReads,
    held: document.held,
    store,
    document,
    window,
    elements,
    byId,
    banner: byId.get('consent-banner') ?? null,
    status: byId.get('consent-banner-status') ?? null,
    analyticsRequests,
    activeElement: () => state.activeElement,
    /** Click one of the banner's two controls, as a keyboard or a pointer would. */
    click(decision) {
      const banner = byId.get('consent-banner')
      assert.ok(banner, 'the page carries no consent banner to click in')
      const control = banner.querySelectorAll('[data-consent]').find((el) => el.attrs.get('data-consent') === decision)
      assert.ok(control, `the banner carries no ${decision} control`)
      control.fire('click', { type: 'click' })
    },
    /** Run every timer the page has set so far, in the order it set them. */
    runTimers() {
      for (const timer of timers.splice(0)) timer.handler()
    },
    /** Press Escape, which is how a banner is dismissed. */
    escape() {
      for (const handler of windowListeners.get('keydown') ?? []) handler({ type: 'keydown', key: 'Escape' })
    },
  }
}

/**
 * Why a page is not consent-gated, as the lines a reader sees; empty means the gate held.
 * `act` runs after the page has loaded, from the state the page loaded in, and returns the
 * decision the visitor made: a request that arrives *because* of a refusal is reported, and
 * one that arrives because of a grant is expected exactly once.
 */
export function gateFailures(html, { dist = DIST, script = null, storage = {}, readable = true, writable = true, act = null } = {}) {
  const out = []
  const page = runPage(html, { dist, script, storage, readable, writable })
  const grantedAtLoad = page.store[CONSENT_KEY] === 'granted'
  const atLoad = page.analyticsRequests().length

  if (!grantedAtLoad && atLoad > 0) out.push('page requests googletagmanager.com before consent')
  if (grantedAtLoad && atLoad !== 1) {
    out.push(`page requests googletagmanager.com ${atLoad} times with a grant stored; it loads once`)
  }
  // A cookie the page SET and left behind. Deleting one is a write too — an expiry in the
  // past, which leaves nothing — so the jar is what decides, not the number of writes.
  const set = page.document.survivingWrites()
  if (set.length > 0) out.push(`page writes document.cookie (${set[0]})`)

  if (act) {
    const before = page.analyticsRequests().length
    const decision = act(page)
    const added = page.analyticsRequests().length - before
    if (decision === 'granted' && added !== 1) out.push(`page requests googletagmanager.com ${added} times after the visitor accepted; it loads once`)
    if (decision === 'denied' && added > 0) out.push('page requests googletagmanager.com after the visitor declined')
    if (decision === 'granted' && page.store[CONSENT_KEY] !== 'granted') out.push('the visitor accepted and the choice was not stored')
  }
  return out
}

/** Accept, as the visitor does: the banner's own control, not a call into its script. */
export const accept = (page) => {
  page.click('granted')
  return 'granted'
}

/** Decline, and dismiss, the two ways a visitor refuses. */
export const decline = (page) => {
  page.click('denied')
  return 'denied'
}
export const dismiss = (page) => {
  page.escape()
  return 'denied'
}

// ---- the policy the artifact is served with ---------------------------------------------

/** The remote origins a Content-Security-Policy's `script-src` admits, if any. */
export function remoteScriptSources(policy) {
  const directive = /(?:^|;)\s*script-src([^;]*)/.exec(policy)
  if (!directive) return ['the policy has no script-src directive, so a browser falls back to default-src']
  return directive[1]
    .trim()
    .split(/\s+/)
    .filter((token) => token !== '' && !/^'/.test(token))
}

// ---- the artifact, in the two states ----------------------------------------------------

// The state the site ships in, which W3.2 is the task that made it the gated one: the flag
// and a measurement id together in `site.yaml`, which the boundary test and the content
// pipeline both require to agree.
const SHIPPED = builtPages(DIST)

// And the other half: the same site with the flag cleared, built in a temp copy of the
// repository, with its own node_modules caches (lib/build.mjs).
let TEMP = null
let FLAG_OFF = null

before(() => {
  TEMP = mkdtempSync(path.join(tmpdir(), 'fugaro-consent-'))
  const site = copyRepository(path.join(TEMP, 'repo'))
  const file = path.join(site, 'data', 'site.yaml')
  const before_ = readFileSync(file, 'utf8')
  const after_ = before_
    .replace(/^  enabled: true$/m, '  enabled: false')
    .replace(/^  measurement_id: ".*"$/m, '  measurement_id: ""')
  assert.notEqual(after_, before_, 'the fixture did not clear the analytics flag in the copy, so every case below would run against the state the site ships in')
  writeFileSync(file, after_)

  const result = spawnSync(process.execPath, [path.join(site, 'node_modules', 'astro', 'bin', 'astro.mjs'), 'build'], {
    cwd: site,
    encoding: 'utf8',
    env: { ...process.env, ASTRO_TELEMETRY_DISABLED: '1' },
  })
  assert.equal(result.status, 0, `the build with analytics switched off failed:\n${result.stdout ?? ''}${result.stderr ?? ''}`)
  FLAG_OFF = { site, dist: path.join(site, 'dist'), pages: builtPages(path.join(site, 'dist')) }
})

after(() => {
  if (TEMP) rmSync(TEMP, { recursive: true, force: true })
})

/** One page of a built artifact, by its path relative to `dist/`. */
function pageOf(pages, rel) {
  const page = pages.find((p) => p.rel === rel)
  assert.ok(page, `${rel} was not built`)
  return page.html
}

/**
 * Every `<script>` a built page carries, as the source it loads: its `src`, or the marker
 * `(inline)` for a script whose code is in the page. The site's shipped state is "one file
 * and no inline script" and this is the reading that says so — a page's script count on its
 * own would not distinguish the copy control's file from a second, unadmitted inline block.
 */
function scriptSources(html) {
  return [...html.matchAll(/<script\b([^>]*)>/g)].map((match) => /\bsrc="([^"]+)"/.exec(match[1])?.[1] ?? '(inline)')
}

const SHIPPED_HOME = pageOf(SHIPPED, 'index.html')

// ---- 1. the state the site ships in ------------------------------------------------------

test('the shipped site carries the gate: one script file, the banner with its configuration, and no inline script anywhere', () => {
  assert.equal(siteFacts().analyticsEnabled, true, 'this case is about the state the site ships in, and site.yaml no longer switches analytics on')
  for (const page of SHIPPED) {
    // The site's one script, and it is a file of its own (D183): the gate, the copy control
    // and the menu are all in it. There is no inline block on any page, which is what lets
    // the deployed policy carry no hash and no `'unsafe-inline'` (W3.2).
    assert.deepEqual(
      scriptSources(page.html),
      [SITE_SCRIPT, EARLY_SCRIPT],
      `${page.rel} ships scripts the site does not admit: the site's one script is its own ${SITE_SCRIPT}, and W3.2 moved the banner's controller and the gated loader into it; the one other file is the banner's early decision, ${EARLY_SCRIPT} (the owner's ruling of 2026-10-23)`,
    )

    const doc = parse(page.html)
    const banners = findAll(doc, (el) => el.attrs.get('id') === 'consent-banner')
    assert.equal(banners.length, 1, `${page.rel} carries ${banners.length} consent banners; the gate's configuration is written once (W3.2)`)
    const banner = banners[0]
    assert.equal(banner.attrs.get('data-consent-key'), CONSENT_KEY, `${page.rel}: the banner does not carry the consent key the gate reads`)
    assert.equal(
      banner.attrs.get('data-analytics-id'),
      MEASUREMENT_ID,
      `${page.rel}: the banner does not carry the property site.yaml names, so the gate would report to a property nothing declares`,
    )
    assert.equal(banner.attrs.get('data-analytics-origin'), ANALYTICS_ORIGIN, `${page.rel}: the banner does not carry the one origin D106 admits`)
    assert.equal(
      findAll(doc, (el) => el.attrs.has('data-analytics-origin')).length,
      1,
      `${page.rel}: more than one element carries the gate's configuration`,
    )
    assert.equal(findAll(doc, (el) => el.attrs.has('data-consent')).length, 2, `${page.rel}: the banner does not offer exactly two choices`)

    // The banner's own stylesheet ships, and it is inert: its selectors match nothing when
    // the banner is not rendered, and it names no origin, no script and no fetch.
    const styles = findAll(doc, (el) => el.name === 'style').map((el) => el.raw ?? '').join('\n')
    assert.ok(!/url\(|@import|@font-face|https?:/i.test(styles), `${page.rel} ships a stylesheet that fetches something`)
  }

  // The origin reaches the artifact only through the markup the flag emits: the site's one
  // script file names no URL at all, so a page is the only place it can appear, and it
  // appears there in the banner's own attribute.
  const text = ['.html', '.css', '.js', '.json', '.webmanifest', '.svg', '.xml', '.txt']
  for (const rel of filesUnder(DIST).filter((rel) => text.some((ext) => rel.endsWith(ext)))) {
    const body = readFileSync(path.join(DIST, rel), 'utf8')
    if (!body.includes(ANALYTICS_HOST)) continue
    assert.ok(rel.endsWith('.html'), `${rel} names the analytics origin: the site's script file names no URL, so only a page's markup may, and only the banner's configuration attribute does`)
  }
  assert.ok(
    !readFileSync(path.join(DIST, SITE_SCRIPT.replace(/^\//, '')), 'utf8').includes(ANALYTICS_HOST),
    `${SITE_SCRIPT} names the analytics origin: the gate reads it from the banner's markup, which is what keeps a flag-off artifact free of it`,
  )
})

test('with the flag cleared, the built site carries no analytics: no origin, no loader, no script, no banner', () => {
  const ANALYTICS_SHAPED = /googletagmanager|dataLayer|gtag\(|fugaro:analytics-consent/i
  for (const page of FLAG_OFF.pages) {
    assert.ok(
      !ANALYTICS_SHAPED.test(page.html),
      `${page.rel} names the analytics surface while site.yaml's analytics.enabled is false: the banner and its configuration are emitted only when the flag is on, so that the artifact a reader is served with the flag off names no third party and loads no script (D93, W1.12 step 3)`,
    )
    // The site's one script ships either way — it is the copy control's and the menu's too
    // (D183) — and it names no origin and fetches nothing when there is no banner to find.
    assert.deepEqual(
      scriptSources(page.html),
      [SITE_SCRIPT],
      `${page.rel} ships scripts while analytics are off: the site's one script is its own ${SITE_SCRIPT}, and the banner's configuration is emitted only when the flag is on`,
    )
    const doc = parse(page.html)
    assert.equal(findAll(doc, (el) => el.attrs.get('id') === 'consent-banner').length, 0, `${page.rel} renders the consent banner while the flag is off`)
    assert.equal(findAll(doc, (el) => el.attrs.has('data-consent')).length, 0, `${page.rel} renders a consent control while the flag is off`)
  }
  const text = ['.html', '.css', '.js', '.json', '.webmanifest', '.svg', '.xml', '.txt']
  for (const rel of filesUnder(FLAG_OFF.dist).filter((rel) => text.some((ext) => rel.endsWith(ext)))) {
    assert.ok(
      !readFileSync(path.join(FLAG_OFF.dist, rel), 'utf8').includes(ANALYTICS_HOST),
      `${rel} carries the analytics origin while the flag is off: with analytics switched off the artifact is byte-for-byte free of it (W1.12, D106)`,
    )
  }
})

test('every built page links the privacy statement, which is where consent is given and withdrawn', () => {
  for (const page of SHIPPED) {
    assert.match(
      page.html,
      new RegExp(`href="${PRIVACY_PATH}"`),
      `${page.rel} does not link ${PRIVACY_PATH}: the statement the site owes a reader (finding 16) is reachable from every page`,
    )
  }
})

// ---- 2. the gate, with analytics switched on --------------------------------------------

test('with analytics on, no built page requests anything from the analytics origin before consent, and none after a refusal', () => {
  for (const page of SHIPPED) {
    const failures = gateFailures(page.html)
    assert.deepEqual(failures, [], `${page.rel} is not consent-gated:\n  ${failures.join('\n  ')}`)

    // The visitor's decision, from the state the page loaded in, through the gate's own
    // lines: accepting loads exactly the one script and stores the choice; refusing — once,
    // and again over a stored refusal — loads nothing at all.
    const accepted = gateFailures(page.html, { act: accept })
    assert.deepEqual(accepted, [], `${page.rel} does not load the analytics script exactly once on an acceptance:\n  ${accepted.join('\n  ')}`)

    const refused = gateFailures(page.html, { act: decline })
    assert.deepEqual(refused, [], `${page.rel} loads something on a refusal:\n  ${refused.join('\n  ')}`)

    const refusedTwice = gateFailures(page.html, { storage: { [CONSENT_KEY]: 'denied' }, act: decline })
    assert.deepEqual(refusedTwice, [], `${page.rel} loads something when a refusal is refused again:\n  ${refusedTwice.join('\n  ')}`)
  }
})

test('the gate asks Google for one page view and nothing else, with the advertising features off (W3.2)', () => {
  const html = pageOf(SHIPPED, 'index.html')

  // Nothing is queued before the visitor chooses: the `dataLayer` the gate builds is a
  // consequence of the grant, not a thing that exists on the page either way.
  const undecided = runPage(html)
  assert.deepEqual(undecided.window.dataLayer ?? [], [], 'the gate queued a dataLayer call before the visitor chose')

  const page = runPage(html)
  page.click('granted')
  const calls = (page.window.dataLayer ?? []).map((entry) => Array.from(entry))
  assert.deepEqual(
    calls.map((call) => call.slice(0, 2).map(String)),
    [
      ['consent', 'default'],
      ['consent', 'update'],
      ['js', String(calls[2]?.[1])],
      ['config', MEASUREMENT_ID],
    ],
    `the gate queues ${calls.length} dataLayer calls (${calls.map((c) => String(c[0])).join(', ')}); consent mode's default first, the grant, one "js" and one "config" is what "a page view per page and nothing else" means`,
  )
  // Consent mode's default is the first thing queued, and it denies everything: the grant
  // then lifts analytics storage alone, so no advertising storage or signal is ever allowed.
  assert.deepEqual(
    calls[0][2],
    { ad_storage: 'denied', ad_user_data: 'denied', ad_personalization: 'denied', analytics_storage: 'denied' },
    "the gate's consent default does not deny every storage and advertising signal before anything else is queued",
  )
  assert.deepEqual(calls[1][2], { analytics_storage: 'granted' }, 'the grant lifts more than analytics storage')
  assert.deepEqual(
    calls[3][2],
    {
      allow_google_signals: false,
      allow_ad_personalization_signals: false,
      page_location: 'https://site.example/docs/',
      page_referrer: 'https://site.example/',
    },
    'the config call does not name the property site.yaml names with Google Signals and ads personalisation off and the page reported by its bare address',
  )
  // The page is reported by its path: no query string and no fragment, of this page or of the
  // one the visitor came from, reaches Google.
  assert.ok(!/leaked/.test(JSON.stringify(calls)), `the gate reports a query string or a fragment to Google: ${JSON.stringify(calls[3])}`)
  // The one thing the gate must never queue: an event of its own. A `page_view` is what the
  // `config` call sends; an `event` call is a second thing reported, and the site sends one.
  for (const call of calls) {
    assert.ok(
      !['event', 'set', 'get', 'set_user_properties', 'set_user_id'].includes(String(call[0])),
      `the gate queues a ${call[0]} call: the site asks for one page view per page and nothing else`,
    )
  }
  assert.ok(
    !/user_id|user_pseudo_id|client_id/.test(JSON.stringify(calls)),
    'the gate names a user id or a client id: the site reports a page view, not a person',
  )

  // And the request itself: one script, from the one admitted origin, naming the property.
  assert.deepEqual(
    page.analyticsRequests(),
    [`${ANALYTICS_ORIGIN}/gtag/js?id=${MEASUREMENT_ID}`],
    'the gate did not load exactly the one Google script, from the one admitted origin, for the property site.yaml names',
  )

  // The mutation this case is written against: the advertising features switched back on.
  const source = readFileSync(path.join(DIST, SITE_SCRIPT.replace(/^\//, '')), 'utf8')
  const adFeaturesOn = source.replace('allow_google_signals: false', 'allow_google_signals: true')
  assert.notEqual(adFeaturesOn, source, 'the mutation found no `allow_google_signals: false` to switch on')
  const mutated = runPage(html, { script: adFeaturesOn })
  mutated.click('granted')
  const mutatedConfig = (mutated.window.dataLayer ?? []).map((entry) => Array.from(entry)).find((call) => call[0] === 'config')
  assert.notDeepEqual(mutatedConfig, calls[3], 'the advertising features can be switched on in the gate without this case noticing')
})

test('the measurement id is written in one place, and the artifact carries it only where site.yaml put it', () => {
  // The property is a public identifier — it is in every page's markup — but it is not a
  // value to scatter: one place to change it, and one place to read it from. The scan is
  // over everything the build *reads* (code, configuration, data), plus the deploy config;
  // `README.md` is the one file allowed to name it, because the note beside the budget has
  // to be able to say which property the site reports to — and what it names there is held
  // to the same value below, so a stale second id is a failure rather than a footnote.
  const roots = ['src', 'data', 'public', 'test', 'content']
  const carriers = []
  for (const root of roots) {
    const dir = path.join(SITE, root)
    for (const rel of filesUnder(dir)) {
      const full = path.join(dir, rel)
      if (!/\.(astro|ts|js|mjs|json|ya?ml|md|css|html|txt)$/.test(rel)) continue
      if (readFileSync(full, 'utf8').includes(MEASUREMENT_ID)) carriers.push(`${root}/${rel}`)
    }
  }
  for (const rel of ['astro.config.mjs', 'package.json', 'tsconfig.json']) {
    if (readFileSync(path.join(SITE, rel), 'utf8').includes(MEASUREMENT_ID)) carriers.push(rel)
  }
  if (readFileSync(path.join(REPO, 'firebase.json'), 'utf8').includes(MEASUREMENT_ID)) carriers.push('../firebase.json')
  assert.deepEqual(
    carriers,
    ['data/site.yaml'],
    `the measurement id is written in ${carriers.length} places (${carriers.join(', ')}); it belongs in site.yaml alone, which is what the layout and this file both read it from (W3.2)`,
  )

  const readme = readFileSync(path.join(SITE, 'README.md'), 'utf8')
  const named = [...new Set([...readme.matchAll(/\bG-[A-Z0-9]{8,}\b/g)].map((m) => m[0]))]
  for (const id of named) {
    assert.equal(id, MEASUREMENT_ID, `website/README.md names the measurement id ${id}, and site.yaml carries ${MEASUREMENT_ID}: the note and the property disagree`)
  }

  // And in the artifact it reaches a page only through the banner's own attribute: no script
  // and no stylesheet carries it, so the gate cannot report to a property the markup does not
  // name.
  for (const rel of filesUnder(DIST)) {
    const body = readFileSync(path.join(DIST, rel), 'utf8')
    if (!body.includes(MEASUREMENT_ID)) continue
    assert.ok(
      rel.endsWith('.html') && body.includes(`data-analytics-id="${MEASUREMENT_ID}"`),
      `${rel} carries the measurement id outside the banner's configuration attribute`,
    )
  }
})

test('the banner appears on a first visit, accepting loads the script once, and declining loads nothing', () => {
  const html = pageOf(SHIPPED, 'index.html')

  const first = runPage(html)
  assert.ok(first.banner, 'the built page carries no consent banner with analytics switched on')
  assert.equal(first.banner.hidden, false, 'the banner is not offered on a first visit, when nothing has been chosen')
  assert.deepEqual(first.analyticsRequests(), [], 'the page requested the analytics script before the visitor chose')

  first.click('granted')
  assert.equal(first.store[CONSENT_KEY], 'granted', 'accepting did not store the choice')
  assert.deepEqual(
    first.analyticsRequests(),
    [`${ANALYTICS_ORIGIN}/gtag/js?id=${MEASUREMENT_ID}`],
    'accepting did not load exactly the one analytics script, from the one admitted origin',
  )
  assert.equal(first.banner.hidden, true, 'the banner stayed on the page after the visitor chose')
  assert.equal(first.activeElement()?.attrs.get('id'), 'main', 'focus was left nowhere when the banner went: it moves to the page content')

  const refused = runPage(html)
  refused.click('denied')
  assert.equal(refused.store[CONSENT_KEY], 'denied', 'declining did not store the decision')
  assert.deepEqual(refused.analyticsRequests(), [], 'the page requested the analytics script after the visitor declined')

  const remembered = runPage(html, { storage: { [CONSENT_KEY]: 'denied' } })
  assert.equal(remembered.banner.hidden, true, 'the banner is offered again to a visitor who already refused')
  assert.deepEqual(remembered.analyticsRequests(), [], 'a stored refusal still loaded the analytics script')

  const granted = runPage(html, { storage: { [CONSENT_KEY]: 'granted' } })
  assert.equal(granted.banner.hidden, true, 'the banner is offered again to a visitor who already accepted')
  assert.equal(granted.analyticsRequests().length, 1, 'a stored grant did not load the analytics script')
})

test('dismissing the banner declines, and a dismissal cannot overwrite a stored grant', () => {
  const html = pageOf(SHIPPED, 'index.html')

  const dismissed = runPage(html)
  dismissed.escape()
  assert.equal(dismissed.store[CONSENT_KEY], 'denied', 'dismissing the banner did not record the default, which is a refusal')
  assert.deepEqual(dismissed.analyticsRequests(), [], 'dismissing the banner loaded the analytics script')
  assert.equal(dismissed.banner.hidden, true, 'the banner stayed on the page after it was dismissed')

  // A visitor who has already accepted is not asked again, so Escape on that page is not a
  // decision at all: the site must not read it as one.
  const granted = runPage(html, { storage: { [CONSENT_KEY]: 'granted' } })
  assert.equal(granted.analyticsRequests().length, 1, 'a stored grant did not load the analytics script')
  granted.escape()
  assert.equal(granted.store[CONSENT_KEY], 'granted', 'Escape overwrote a stored grant with a decision the visitor never made')
})

test('the banner collapses before it is hidden, and the decision is already made while it does', () => {
  // owner-approved motion opportunities 2026-10-23: the drawing waits for the collapse, the
  // decision does not.
  const html = pageOf(SHIPPED, 'index.html')

  const page = runPage(html, { motion: true })
  assert.equal(page.banner.hidden, false, 'the banner is not offered on a first visit')
  page.click('granted')
  assert.equal(page.store[CONSENT_KEY], 'granted', 'the choice was not stored the moment it was made')
  assert.equal(page.analyticsRequests().length, 1, 'accepting did not load the analytics script at once')
  assert.equal(page.activeElement()?.attrs.get('id'), 'main', 'focus did not move to the content at once')
  assert.ok(page.banner.attrs.has('data-consent-closing'), 'the banner was not marked closing, so nothing collapses it')
  page.click('denied')
  page.escape()
  assert.equal(page.store[CONSENT_KEY], 'granted', 'a second press while the banner collapsed was taken as a new decision')
  page.banner.fire('transitionend', { type: 'transitionend', target: page.banner, propertyName: 'opacity' })
  assert.equal(page.banner.hidden, true, 'the banner stayed on the page after its collapse ended')
  assert.ok(!page.banner.attrs.has('data-consent-closing'), 'the closing mark outlived the banner')

  // The timer is the floor for a collapse whose end is never reported.
  const silent = runPage(html, { motion: true })
  silent.click('denied')
  assert.equal(silent.store[CONSENT_KEY], 'denied', 'declining did not store the decision at once')
  assert.equal(silent.banner.hidden, false, 'the banner vanished before its collapse ran')
  silent.runTimers()
  assert.equal(silent.banner.hidden, true, 'a collapse with no transitionend left the banner on the page')
})

test('consent is withdrawable: the privacy page always offers the choice, and declining there stops the load', () => {
  const privacy = pageOf(SHIPPED, 'privacy/index.html')
  const page = runPage(privacy, { storage: { [CONSENT_KEY]: 'granted' } })
  assert.ok(page.banner, 'the privacy page carries no consent banner')
  assert.equal(
    page.banner.hidden,
    false,
    'the privacy page hides the choice from a visitor who already accepted, so consent cannot be withdrawn from the one page that says how (W1.12 step 3)',
  )

  assert.equal(page.analyticsRequests().length, 1, 'a stored grant did not load the analytics script on the privacy page')
  const queued = page.window.dataLayer.length
  page.click('denied')
  assert.equal(page.store[CONSENT_KEY], 'denied', 'declining on the privacy page did not overwrite the stored grant')

  // Withdrawal stops the page that is already measuring: Google's documented opt-out flag is
  // set and consent is updated to denied, and nothing new is requested.
  assert.equal(page.window[`ga-disable-${MEASUREMENT_ID}`], true, 'withdrawing did not set Google\'s opt-out flag, so the page already measuring keeps sending')
  assert.deepEqual(
    page.window.dataLayer.slice(queued).map((entry) => Array.from(entry)),
    [['consent', 'update', { analytics_storage: 'denied' }]],
    'withdrawing did not update consent to denied on the page that had loaded Google\'s script',
  )
  assert.equal(page.analyticsRequests().length, 1, 'withdrawing requested something more from Google')

  // And a grant given again on the same page lifts both, without loading the script twice.
  page.click('granted')
  assert.equal(page.window[`ga-disable-${MEASUREMENT_ID}`], false, 'a renewed grant left the opt-out flag set')
  assert.deepEqual(Array.from(page.window.dataLayer.at(-1)), ['consent', 'update', { analytics_storage: 'granted' }])
  assert.equal(page.analyticsRequests().length, 1, 'a renewed grant loaded Google\'s script a second time')
  page.click('denied')

  const next = runPage(pageOf(SHIPPED, 'index.html'), { storage: page.store })
  assert.deepEqual(next.analyticsRequests(), [], 'the withdrawn consent still loaded the analytics script on the next page view')
  assert.equal(next.banner.hidden, true, 'the withdrawn visitor is asked again on the next page, although a decision is stored')
})

test('a browser that refuses storage is treated as undecided: nothing loads, and the banner says so', () => {
  const html = pageOf(SHIPPED, 'index.html')

  const unreadable = runPage(html, { readable: false })
  assert.deepEqual(unreadable.analyticsRequests(), [], 'the page loaded the analytics script although the stored decision could not be read')
  assert.equal(unreadable.banner.hidden, false, 'the banner is not offered when the stored decision cannot be read')

  const unwritable = runPage(html, { readable: false, writable: false })
  assert.equal(unwritable.banner.hidden, false, 'the banner is not offered when storage is unavailable')
  unwritable.click('granted')
  assert.deepEqual(unwritable.analyticsRequests(), [], 'the page loaded the analytics script although the choice could not be stored')
  assert.equal(unwritable.status?.hidden, false, 'the banner does not tell the visitor that the choice could not be stored')
  assert.equal(unwritable.banner.hidden, false, 'the banner was dismissed although nothing was remembered, so the visitor would never be asked again')
})

test('a refusal that cannot be stored is still honoured: the tag it stops stops sending', () => {
  // The other half of the case above, and the half it could not see: a browser may READ storage
  // and refuse the WRITE (a full quota, an old private mode). The two are not the same answer —
  // a decision that cannot be filed is not a decision the visitor did not make — and on a page
  // whose storage is readable, the tag this press stops is running there, so a refusal the site
  // cannot remember is a refusal it can still honour. A grant is the other way round: it is
  // honoured only where it is kept, because nothing would remember it on the next page.
  const page = runPage(pageOf(SHIPPED, 'privacy/index.html'), { storage: { [CONSENT_KEY]: 'granted' }, writable: false })
  assert.equal(page.analyticsRequests().length, 1, 'the stored grant did not load the analytics script, so the withdrawal below would prove nothing')
  const queued = page.window.dataLayer.length

  page.click('denied')
  assert.equal(page.store[CONSENT_KEY], 'granted', 'the storage double kept the decision although it was told to refuse every write, so this case proves nothing')
  assert.equal(
    page.window[`ga-disable-${MEASUREMENT_ID}`],
    true,
    'a refusal the browser would not store left Google measuring the page the visitor had just declined',
  )
  assert.deepEqual(
    page.window.dataLayer.slice(queued).map((entry) => Array.from(entry)),
    [['consent', 'update', { analytics_storage: 'denied' }]],
    'a refusal that could not be stored did not tell the tag it stopped that consent was denied',
  )
  assert.equal(page.banner.hidden, false, 'the banner was dismissed although the refusal could not be remembered')
  assert.equal(page.status?.hidden, false, 'the banner does not tell the visitor that the choice could not be stored')
})

test('the banner is asked for before the first frame, on the campaign pages too', () => {
  // `consent-early.js` draws the bar in the page's flow before `site.js` runs (the owner's
  // ruling of 2026-10-23), and it does it by setting one attribute on `<html>` — so the whole
  // of its behaviour is a fact about the page it ran on, and G2.3 gave it a second key to read
  // there. This is the case that runs it: the stub executes the file's own source out of the
  // artifact, where the page put it, so a key the layout forgot to write, or wrote under
  // another name than this file reads, is a bar missing from the first frame on the four
  // pages that ask the campaign question — and no other case would notice.
  const campaign = pageOf(SHIPPED, CAMPAIGN_PAGE)
  const tag = findAll(parse(campaign), (el) => el.name === 'script' && el.attrs.get('src') === EARLY_SCRIPT)[0]
  assert.ok(tag, `the campaign page carries no ${EARLY_SCRIPT}, so the bar is not in its place from the first frame`)
  assert.equal(tag.attrs.get('data-consent-key'), CONSENT_KEY, 'the campaign page\'s early decision reads no key of the site\'s, so a visitor who has not answered the site\'s question is shown no bar')
  assert.equal(
    tag.attrs.get('data-marketing-consent-key'),
    MARKETING_CONSENT_KEY,
    'the campaign page\'s early decision carries no campaign key, so a visitor who answered the site\'s question and not the campaign\'s is shown no bar',
  )
  assert.ok(!tag.attrs.has('data-consent-always'), 'the campaign page always offers the choice, which is the privacy page\'s arrangement and not this page\'s')

  const asked = (storage) => runPage(campaign, { location: CAMPAIGN_LOCATION, storage })
  const mark = (storage) => asked(storage).document.documentElement.getAttribute('data-consent-ask')

  assert.equal(mark({}), '', 'the early decision left no mark on <html> for a visitor who has answered nothing, so the bar is not drawn in its place from the first frame')
  assert.equal(
    mark({ [CONSENT_KEY]: 'granted' }),
    '',
    'a visitor who answered the site\'s question but not the campaign\'s is given no bar, so the campaign question is never asked',
  )
  assert.equal(mark({ [CONSENT_KEY]: 'granted', [MARKETING_CONSENT_KEY]: 'granted' }), null, 'the early decision marks a page whose every question is answered, so the bar is drawn under a visitor who has already decided')
  assert.equal(
    runPage(pageOf(SHIPPED, 'docs/index.html'), { storage: { [CONSENT_KEY]: 'granted' } }).document.documentElement.getAttribute('data-consent-ask'),
    null,
    'a page that asks nothing is marked as if it asked something, so the bar is drawn under a visitor who has decided',
  )
})

// ---- 3. cookies: none before consent, and none left after a withdrawal -------------------
//
// W1.12's step was "no cookie before consent": the site stores one decision and sets nothing
// of its own. The owner's ruling of 2026-10-18 added the other half — withdrawing consent
// deletes the cookies Google's tag set while the visitor had accepted — so this section holds
// both: no write and no read before a grant, and the whole of what GA4 leaves gone after a
// withdrawal, on the host and on the parent domain, with every other cookie untouched.

// The names GA4 writes for this site, one of them DERIVED from the measurement id the way the
// ruling says (`_ga_` plus the id without its `G-` prefix). The test derives it here from
// `site.yaml`'s own value, so a script that wrote a fixed name instead would fail the case
// below rather than agree with a copy of the same literal typed in this file.
const GA_CLIENT = '_ga'
const GA_CONTAINER = `_ga_${MEASUREMENT_ID.replace(/^G-/, '')}`
const GA_THROTTLE = `_gat_gtag_${MEASUREMENT_ID}`

/**
 * What GA4 leaves in a consented visitor's browser, and two cookies that are not Google's.
 * Every one names `Path=/`, which is the path the tag sets its own at: the deletion has to
 * match it, and a cookie planted at the page's own directory would not prove that it does.
 */
function plantedCookies({ domain = null } = {}) {
  const suffix = domain ? `; path=/; domain=${domain}` : '; path=/'
  return [
    `${GA_CLIENT}=GA1.1.111.222${suffix}`,
    `${GA_CONTAINER}=GS1.1.333${suffix}`,
    `_gid=GA1.2.444${suffix}`,
    `_gat=1${suffix}`,
    `${GA_THROTTLE}=1${suffix}`,
    `_gate=1${suffix}`,
    `theme=dark${suffix}`,
  ]
}

// The site's own cookies, which a withdrawal must leave exactly where they are: one whose name
// merely begins with the throttle cookie's letters, and one with nothing to do with Google.
const KEPT_NAMES = ['_gate', 'theme']

/** The names a case expects a withdrawal to remove, and the ones it must leave alone. */
const GA_NAMES = [GA_CLIENT, GA_CONTAINER, '_gid', '_gat', GA_THROTTLE]

function assertForgotten(page, where) {
  const left = page.held()
  for (const name of GA_NAMES) {
    assert.ok(!left.includes(name), `${where}: ${name} survived the withdrawal, so the identifiers the accepted script left are still in the browser (owner's ruling, 2026-10-18)`)
  }
  for (const name of KEPT_NAMES) {
    assert.ok(left.includes(name), `${where}: the withdrawal deleted ${name}, which is not a cookie Google set`)
  }
}

test('the site sets no cookie of its own, before consent or after it', () => {
  const html = pageOf(SHIPPED, 'index.html')
  const page = runPage(html)
  page.click('granted')
  assert.deepEqual(page.document.survivingWrites(), [], 'a script this site ships left a cookie behind: the site stores one decision and sets no cookie of its own')

  for (const p of [...SHIPPED]) {
    assert.ok(
      !/document\.cookie/.test(p.html),
      `${p.rel} names document.cookie: the cookie work W3.2's withdrawal does is in the site's one script file, and no page carries an inline block`,
    )
  }
})

test('withdrawing deletes every cookie GA4 set, on the host and on the parent domain', () => {
  const html = pageOf(SHIPPED, 'privacy/index.html')
  const page = runPage(html, { storage: { [CONSENT_KEY]: 'granted' }, cookies: plantedCookies() })
  assert.ok(page.held().includes(GA_CLIENT), 'the cookie double did not hold the cookies a consented visitor brings, so the case below would pass on an empty browser')

  page.click('denied')
  assertForgotten(page, 'on the host')

  // And the attribute the deletion depends on, read off the writes themselves: a write of
  // `name=; expires=…` with no `Path` lands on the page's own directory, not on `/`, and would
  // match no cookie GA4 set — so every write names the path, beside the expiry.
  for (const write of page.cookies) {
    assert.match(write, /;\s*path=\//i, `the withdrawal wrote "${write.trim()}" without Path=/, which in a browser lands on the page's own directory and deletes nothing`)
  }
})

test('a cookie GA4 set on the registrable parent domain is deleted there too', () => {
  const html = pageOf(SHIPPED, 'privacy/index.html')
  // The host has a label to spare here, so the parent domain GA4 may have written to is a
  // real one (`site.example`), and the cookie below is only reachable from the page while it
  // is set on the parent: a withdrawal that only ever tried the host would leave it behind.
  const page = runPage(html, {
    storage: { [CONSENT_KEY]: 'granted' },
    cookies: plantedCookies({ domain: 'site.example' }),
    location: SUBDOMAIN_LOCATION,
  })
  assert.equal(page.document.jar.filter((c) => c.domain === 'site.example').length, plantedCookies().length, 'the parent-domain cookies are not in the browser the case runs against')

  page.click('denied')
  assertForgotten(page, 'on the parent domain')
  assert.deepEqual(
    page.document.jar.filter((c) => c.domain === 'site.example' && GA_NAMES.includes(c.name)).map((c) => c.name),
    [],
    'the withdrawal never reached the parent domain, so a cookie GA4 set there is still in the browser',
  )
})

test('the container cookie name is derived from the measurement id the banner carries', () => {
  const html = pageOf(SHIPPED, 'privacy/index.html').replace(
    `data-analytics-id="${MEASUREMENT_ID}"`,
    'data-analytics-id="G-OTHERTEST9"',
  )
  assert.ok(html.includes('data-analytics-id="G-OTHERTEST9"'), 'the fixture did not change the banner\'s measurement id, so the case below proves nothing')

  const page = runPage(html, {
    storage: { [CONSENT_KEY]: 'granted' },
    cookies: ['_ga_OTHERTEST9=GS1.1.1; path=/', `${GA_CONTAINER}=GS1.1.2; path=/`, 'theme=dark; path=/'],
  })
  page.click('denied')
  const left = page.held()
  assert.ok(!left.includes('_ga_OTHERTEST9'), 'the withdrawal did not delete `_ga_` plus the id the banner carries: the name is not derived from it')
  assert.ok(left.includes(GA_CONTAINER), 'the withdrawal deleted the container cookie of an id the banner does not carry, so the name is not the one this property uses')
  assert.ok(left.includes('theme'), 'the withdrawal deleted a cookie that is not Google\'s')
})

test('nothing is read or deleted before consent was ever given', () => {
  const html = pageOf(SHIPPED, 'index.html')
  const fresh = runPage(html, { cookies: plantedCookies() })
  assert.deepEqual(fresh.cookies, [], 'a page deleted cookies although the visitor never consented: there is nothing to delete before a grant, and the work is owed to nobody')
  assert.equal(fresh.cookieReads.length, 0, 'a page read document.cookie before consent was ever given')
  assert.ok(fresh.held().includes(GA_CLIENT), 'the page removed a cookie before consent was ever given, which is not the site\'s to do')

  // A refusal on a first visit is a refusal, not a withdrawal: nothing was consented to, and
  // nothing is deleted.
  fresh.click('denied')
  assert.deepEqual(fresh.cookies, [], 'declining on a first visit deleted cookies although nothing had been consented to')
  assert.equal(fresh.cookieReads.length, 0, 'declining on a first visit read document.cookie')
  assert.ok(fresh.held().includes(GA_CLIENT), 'declining on a first visit removed a cookie, which a visitor who never accepted did not leave through this site')
})

test('accepting and withdrawing again on the same page deletes what the second grant left', () => {
  const html = pageOf(SHIPPED, 'privacy/index.html')
  const page = runPage(html, { storage: { [CONSENT_KEY]: 'granted' }, cookies: plantedCookies() })
  page.click('denied')
  assertForgotten(page, 'on the first withdrawal')

  // A withdrawal after a decline is idempotent: nothing is left to delete, so the second one
  // writes nothing, and it neither throws nor takes the unrelated cookie with it.
  const before = page.cookies.length
  page.click('denied')
  assert.equal(page.cookies.length, before, 'the second withdrawal wrote to document.cookie although nothing Google set was left')
  assertForgotten(page, 'on the second withdrawal')

  // And a grant given again leaves the browser to be cleared again, which is the state a
  // visitor with the banner on the privacy page actually reaches.
  page.click('granted')
  for (const cookie of plantedCookies()) page.document.cookie = cookie
  page.document.cookies.length = 0
  page.click('denied')
  assertForgotten(page, 'after a renewed grant')
})

// ---- 3b. the campaign's own consent, and the three loads behind it (G2.3, D401) ----------
//
// The second question the banner asks on the four campaign pages, and the three vendors it
// covers. The plan's promise is the same shape as D93's: **no request to a vendor before that
// purpose is granted, and none anywhere but the page the loader was built for** — including
// after an analytics-only consent, which is the state a visitor who accepted on the homepage
// arrives in. The withdrawal is the third case, because a second purpose is a second answer.

/** The requests a page made to the campaign's three, in the order it made them. */
function campaignRequests(page) {
  return page.requests.filter((url) => CAMPAIGN_ORIGINS.some((origin) => String(url).startsWith(origin)))
}

/** The campaign page the cases below run, as the artifact serves it. */
const CAMPAIGN_HTML = pageOf(SHIPPED, CAMPAIGN_PAGE)

/**
 * The same page with one of the two vendors' ids as a case wants it: the file's own rule is
 * that an empty id is the loader switched off (R3), and both ids are the owner's now (ruling of
 * 2026-10-31), so the switched-off state is one a case writes into the markup rather than one
 * the artifact ships. Nothing else about the page changes: the origins, the booking link and the
 * path are the ones the build wrote.
 */
function withVendorId(attribute, id, html) {
  const written = new RegExp(`${attribute}="[^"]*"`)
  assert.match(html, written, `the built campaign page carries no ${attribute} for a case to write a value into`)
  return html.replace(written, `${attribute}="${id}"`)
}

/**
 * The cookies the campaign withdrawal deletes, read out of the script the site ships rather
 * than restated here — the same way the privacy page's claim about them is held. A name the
 * script stops deleting is a case that must fail, and one it starts deleting is a case that
 * owes an assertion; a copy of the list in this file would let the two drift apart in silence.
 */
function marketingCookies() {
  const script = readFileSync(path.join(DIST, SITE_SCRIPT.replace(/^\//, '')), 'utf8')
  const list = /var MARKETING_COOKIES = \[([\s\S]*?)\]/.exec(script)
  assert.ok(list, `${SITE_SCRIPT} no longer carries the list of cookies the campaign withdrawal deletes, so the cases below have nothing to hold`)
  const names = [...list[1].matchAll(/'([^']+)'/g)].map((m) => m[1])
  assert.ok(names.length > 0, `${SITE_SCRIPT} deletes no campaign cookie, so a withdrawal that leaves the tags' identifiers behind would pass`)
  return names
}

test('no page asks any of the three vendors anything before the marketing consent is granted', () => {
  // A first visit to a campaign page: the banner is offered and nothing of the three is
  // fetched, though the page carries all three addresses in its own banner.
  const first = runPage(CAMPAIGN_HTML, { location: CAMPAIGN_LOCATION })
  assert.deepEqual(campaignRequests(first), [], 'a campaign page fetched a vendor before the visitor answered the campaign question')
  assert.ok(first.byId.get('calendly-embed').hidden, 'the booking frame is in the page before the visitor granted the marketing consent')
  assert.equal(first.banner.hidden, false, 'the campaign page does not offer the banner on a first visit')

  // A visitor who granted analytics on an earlier page: the campaign question is still open,
  // the banner asks it again, and the three are still asked nothing — accepting analytics is
  // not accepting the campaign (F17).
  const analyticsOnly = runPage(CAMPAIGN_HTML, { location: CAMPAIGN_LOCATION, storage: { [CONSENT_KEY]: 'granted' } })
  assert.deepEqual(campaignRequests(analyticsOnly), [], 'an analytics-only consent loaded a campaign vendor')
  assert.equal(analyticsOnly.analyticsRequests().length, 1, 'the page did not load the analytics script the visitor had already accepted')
  assert.equal(analyticsOnly.banner.hidden, false, 'a visitor who has answered only the site\'s question is not asked the campaign one')

  // A page that is not a campaign page at all: same script, no configuration, so nothing of
  // the three can load there even after a grant (the origins are not in it).
  const elsewhere = runPage(pageOf(SHIPPED, 'index.html'), { storage: { [CONSENT_KEY]: 'granted', [MARKETING_CONSENT_KEY]: 'granted' } })
  assert.deepEqual(campaignRequests(elsewhere), [], 'a non-campaign page loaded a campaign vendor')
})

test('the marketing grant loads the three vendors on the page they were built for, and nowhere else', () => {
  const page = runPage(CAMPAIGN_HTML, { location: CAMPAIGN_LOCATION })
  page.click('granted')

  assert.deepEqual(
    campaignRequests(page),
    [
      `${CAMPAIGN.metaOrigin}/en_US/fbevents.js`,
      `${CAMPAIGN.linkedinOrigin}/li.lms-analytics/insight.min.js`,
      `${CAMPAIGN.calendlyOrigin}/assets/external/widget.js`,
    ],
    'the campaign grant did not load exactly the pixel, the insight tag and the booking widget, in that order, from the origins the banner carries',
  )
  assert.equal(page.store[MARKETING_CONSENT_KEY], 'granted', 'the campaign decision was not stored under its own key')
  assert.equal(page.store[CONSENT_KEY], 'granted', 'the press that answered the campaign question did not answer the site\'s, on a page where both were open')

  // The vendors are told the page without its query and without its fragment (F22), and the
  // pixel is told nothing else: the automatic collection off, then its id, then one `PageView`.
  // The order is Meta's own — `autoConfig` is read while the pixel is configured, which is what
  // `init` does — and the page's events are the three the handlers send rather than the button
  // clicks and page metadata Meta would collect for it.
  const page_location = `https://site.example${CAMPAIGN_PATH}`
  const fbq = (page.window.fbq.queue ?? []).map((entry) => Array.from(entry))
  assert.deepEqual(
    fbq,
    [
      ['set', 'autoConfig', false, META_ID],
      ['init', META_ID],
      ['track', 'PageView', { page_location }],
    ],
    `the pixel was handed ${JSON.stringify(fbq)}: the automatic collection off before the id, and then one page view on the page it was built for`,
  )
  assert.deepEqual(
    (page.window.lintrk.q ?? []).map((entry) => Array.from(entry)),
    [['track', { page_location }]],
    'the insight tag was handed an address other than the page\'s path',
  )
  assert.ok(!/leaked/.test(JSON.stringify([fbq, page.window.lintrk.q])), `a vendor was handed the page's query string or fragment: ${JSON.stringify([fbq, page.window.lintrk.q])}`)

  // And the embed: the container the calls-to-action section emits hidden is unhidden with the
  // booking address on it, which is what Calendly's own script builds the frame from.
  const embed = page.byId.get('calendly-embed')
  assert.equal(embed.hidden, false, 'the booking frame stayed hidden after the visitor accepted')
  assert.equal(
    embed.attrs.get('data-url'),
    loadLanding().calendlyURL,
    'the booking frame was handed something other than the booking link website/data/landing.yaml writes',
  )

  // The path check: the same page, served from another path of the site, loads nothing —
  // a bar copied onto a page it was not built for is a bar with no vendors behind it (F18).
  const stale = runPage(CAMPAIGN_HTML, { location: PAGE_LOCATION, storage: { [MARKETING_CONSENT_KEY]: 'granted' } })
  assert.deepEqual(campaignRequests(stale), [], 'a campaign page served from another path loaded the three vendors')

  // And the gate is the same one that `origins.mjs` holds to a contract: no URL is typed in
  // the script, so the request above can only come from the banner's own configuration.
  const script = readFileSync(path.join(DIST, SITE_SCRIPT.replace(/^\//, '')), 'utf8')
  for (const origin of CAMPAIGN_ORIGINS) {
    assert.ok(!script.includes(new URL(origin).host), `${SITE_SCRIPT} names ${new URL(origin).host}: the three reach the loader through the campaign page's own banner`)
  }
})

test('an empty vendor id is a loader that does not load, and site.yaml carries neither as empty', () => {
  // Both ids are the owner's now (ruling of 2026-10-31: the partner id, and the pixel id the
  // owner supplied on the same day), so the shipped page loads all three — which is what the
  // case above reads — and the switched-off state is the one the file's own rule describes: an
  // empty id is the loader switched off (R3). It is written into the markup here, because the
  // artifact no longer ships it, and what it has to keep true is that the two loaders are wired
  // around it: nothing is fetched, and no queue is created, for a vendor whose id is empty.
  assert.notEqual(CAMPAIGN.metaPixelID, '', 'site.yaml carries no pixel id: the owner supplied one on 2026-10-31, and an empty value is the loader switched off (R3)')
  assert.notEqual(CAMPAIGN.linkedinPartnerID, '', 'site.yaml carries no partner id, so the shipped page has no insight tag and this case could not tell a switched-off loader from a missing one')

  const blanked = withVendorId(
    'data-linkedin-partner-id',
    '',
    withVendorId('data-meta-pixel-id', '', CAMPAIGN_HTML),
  )
  const page = runPage(blanked, { location: CAMPAIGN_LOCATION })
  page.click('granted')
  assert.deepEqual(
    campaignRequests(page),
    [`${CAMPAIGN.calendlyOrigin}/assets/external/widget.js`],
    'with both ids empty the page still asked a vendor for something other than the booking widget: the widget has no id to need, and the two tags are switched off by theirs',
  )
  assert.ok(!page.window.fbq, 'the page queued a pixel call while the id it was built with is empty, so a switched-off loader is not switched off')
  assert.ok(!page.window.lintrk, 'the page queued an insight-tag call while the id it was built with is empty')
})

test('withdrawing the marketing consent stops the three and deletes the cookies the two tags set', () => {
  // Both tags run as scripts in this page's own origin, so every cookie either one writes here
  // is first-party and is this site's to remove — the pixel's two and the six LinkedIn
  // documents, which is the list the privacy statement prints. `_gate` is the control: a name
  // that merely begins like a cookie of theirs.
  const page = runPage(CAMPAIGN_HTML, {
    location: CAMPAIGN_LOCATION,
    storage: { [CONSENT_KEY]: 'granted', [MARKETING_CONSENT_KEY]: 'granted' },
    cookies: [...marketingCookies().map((name) => `${name}=planted; path=/`), '_gate=1; path=/'],
  })
  assert.equal(campaignRequests(page).length, 3, 'the stored campaign grant did not load the three vendors, so the withdrawal below would prove nothing')
  for (const name of marketingCookies()) {
    assert.ok(page.held().includes(name), `the cookie double did not hold ${name}, so its deletion below would prove nothing`)
  }

  page.click('denied')
  assert.equal(page.store[MARKETING_CONSENT_KEY], 'denied', 'withdrawing did not overwrite the stored campaign grant')
  assert.equal(page.store[CONSENT_KEY], 'granted', 'a press about the campaign rewrote the site\'s answer, which the visitor gave on another page')
  assert.deepEqual(
    (page.window.fbq.queue ?? []).map((entry) => Array.from(entry)).filter((call) => call[0] === 'consent'),
    [['consent', 'revoke']],
    'withdrawing did not tell the pixel to stop',
  )
  const left = page.held()
  for (const name of marketingCookies()) {
    assert.ok(!left.includes(name), `${name} survived the withdrawal, so a first-party identifier a tag set is still in the browser (the plan's withdrawal removes the cookies the trackers set)`)
  }
  assert.ok(left.includes('_gate'), 'the withdrawal deleted a cookie that is neither tag\'s')

  // The next page view asks the three nothing: the decision is stored, and it is a refusal.
  const next = runPage(CAMPAIGN_HTML, { location: CAMPAIGN_LOCATION, storage: page.store })
  assert.deepEqual(campaignRequests(next), [], 'the withdrawn consent still loaded a vendor on the next page view')
})

test('the privacy page withdraws the campaign consent too, because the campaign bar is gone by then', () => {
  // Where a withdrawal is REACHABLE, which is the half the case above cannot see: the campaign
  // bar is offered only while the campaign question is open, so a visitor who accepted on /save
  // finds no control on /save again, and the page that says how consent is withdrawn is the one
  // whose bar is always offered (W1.12). G2.3's consent is withdrawn there or nowhere, and the
  // privacy page's bar carries the campaign key without the campaign configuration so that the
  // refusal it offers takes back both answers — the three still load on no page after it.
  const granted = { [CONSENT_KEY]: 'granted', [MARKETING_CONSENT_KEY]: 'granted' }
  const page = runPage(pageOf(SHIPPED, 'privacy/index.html'), {
    storage: granted,
    cookies: [...marketingCookies().map((name) => `${name}=planted; path=/`), '_gate=1; path=/'],
  })
  assert.ok(page.banner, 'the privacy page carries no consent banner')
  assert.equal(
    page.banner.hidden,
    false,
    'the privacy page hides the bar from a visitor who answered both questions, so the campaign consent cannot be withdrawn from the one page that says how',
  )
  assert.ok(page.held().includes('li_sugr'), 'the cookie double did not hold the insight tag\'s cookie, so the deletion below would prove nothing')
  assert.deepEqual(campaignRequests(page), [], 'the privacy page loaded one of the campaign\'s three vendors: it carries the decision\'s key and none of the origins')

  page.click('denied')
  assert.equal(page.store[MARKETING_CONSENT_KEY], 'denied', 'declining on the privacy page left the campaign grant standing, so the three load again on the next campaign page')
  assert.equal(page.store[CONSENT_KEY], 'denied', 'declining on the privacy page did not overwrite the site\'s own grant')
  const left = page.held()
  for (const name of marketingCookies()) {
    assert.ok(!left.includes(name), `${name} survived a withdrawal made on the privacy page, where neither tag ever ran`)
  }
  assert.ok(left.includes('_gate'), 'the withdrawal deleted a cookie that is neither tag\'s')

  const next = runPage(CAMPAIGN_HTML, { location: CAMPAIGN_LOCATION, storage: page.store })
  assert.deepEqual(campaignRequests(next), [], 'the consent withdrawn on the privacy page still loaded a vendor on the next campaign page view')
})

test('a bar that carries the campaign key without the question grants nothing with it', () => {
  // The other half of the rule above: only the four campaign pages may answer the campaign
  // question with a yes, so a visitor is never reported to Meta or LinkedIn by a press made on
  // a page that never named them. The privacy page's bar is the one that carries the key
  // without the configuration, and its Accept is the site's answer alone.
  //
  // **What this leaves open, and it is a decision and not an oversight** (raised in review of
  // G2.3): a campaign page's bar is offered only while the campaign question is open, so once
  // the answer is `denied` — given there, or taken back on the privacy page — no page asks the
  // campaign question again, and the three cannot be turned back on without clearing this
  // browser's storage. The site's own question is the other way round: the privacy page's bar
  // always offers it, so an analytics refusal is revocable. Making the campaign answer
  // round-trippable means either asking it again on the four campaign pages after a refusal
  // (asking a visitor who said no) or letting this page's Accept grant it (a yes the page never
  // asked for, and one whose three loaders that page does not even carry) — both are product
  // decisions, neither is taken here, and the owner has them in this PR.
  const page = runPage(pageOf(SHIPPED, 'privacy/index.html'), { storage: {} })
  page.click('granted')
  assert.equal(page.store[CONSENT_KEY], 'granted', 'accepting on the privacy page did not answer the site\'s question')
  assert.ok(
    !(MARKETING_CONSENT_KEY in page.store),
    'accepting on the privacy page granted the campaign measurement, which that page never asked about',
  )
  assert.deepEqual(campaignRequests(page), [], 'accepting on the privacy page loaded one of the campaign\'s three vendors')
})



/**
 * The CSS a built page ships: its own `<style>` blocks and the first-party stylesheets it links.
 * The case below reads the consent pair's own rules out of it, rather than trusting a class list
 * kept here, so a modifier that started changing a size would fail that case.
 */
function pageCSS(page) {
  const out = []
  for (const el of walk(parse(page.html))) {
    if (el.name === 'style') out.push(el.raw ?? '')
    if (el.name === 'link' && /(^|\s)stylesheet(\s|$)/i.test(el.attrs.get('rel') ?? '')) {
      const href = el.attrs.get('href') ?? ''
      if (/^[a-z]+:|^\/\//i.test(href)) continue
      const served = servedFile(DIST, new URL(href, 'https://x/').pathname)
      if (served) out.push(readFileSync(path.join(DIST, served), 'utf8'))
    }
  }
  return out.join('\n')
}

test('the banner is labelled, its two choices are equal, and it is reachable by keyboard before the content', () => {
  for (const page of SHIPPED) {
    const doc = parse(page.html)
    const banner = findAll(doc, (el) => el.attrs.get('id') === 'consent-banner')[0]
    assert.ok(banner, `${page.rel} carries no element with id="consent-banner"`)

    const label = (banner.attrs.get('aria-label') ?? '').trim()
    assert.ok(label.length > 0, `${page.rel}: the banner has no accessible name: a screen reader lists regions by name`)
    assert.equal(banner.attrs.get('role'), 'region', `${page.rel}: the banner is not a named region`)

    const controls = findAll(banner, (el) => el.attrs.has('data-consent'))
    assert.equal(controls.length, 2, `${page.rel}: the banner carries ${controls.length} choices; consent and refusal are two, and equally offered`)
    assert.deepEqual(
      controls.map((el) => el.attrs.get('data-consent')).sort(),
      ['denied', 'granted'],
      `${page.rel}: the banner's choices are not one grant and one refusal`,
    )

    for (const control of controls) {
      assert.equal(control.name, 'button', `${page.rel}: a consent choice is a <${control.name}>, not a native button`)
      assert.equal(control.attrs.get('type'), 'button', `${page.rel}: a consent button without type="button" submits whatever form it is ever put in`)
      assert.ok(!control.attrs.has('autofocus'), `${page.rel}: a consent choice takes focus on load, which is the dark pattern this banner must not have`)
      assert.ok(!control.attrs.has('tabindex'), `${page.rel}: a consent choice carries a tabindex, and both are natively reachable`)
      assert.ok(!control.attrs.has('hidden') && !control.attrs.has('disabled'), `${page.rel}: a consent choice is hidden or disabled`)
      for (const [name] of control.attrs) {
        assert.ok(!/^on/.test(name), `${page.rel}: a consent choice carries an inline ${name}= handler`)
      }
      const text = textOf(control).trim()
      assert.ok(text.length > 0, `${page.rel}: a consent choice has no accessible name`)
    }

    // Equal prominence, mechanically. Both choices are drawn by one shared class, and the
    // stylesheet below is read to hold the two modifiers that tell the primary from the
    // secondary to paint and nothing else: a modifier that also changed a height, a padding or
    // a type size would make one of the two the easier one to press, which is the rule.
    for (const control of controls) {
      assert.ok(
        (control.attrs.get('class') ?? '').split(/\s+/).includes('consent-banner__button'),
        `${page.rel}: a consent choice does not carry the pair's shared class, so nothing gives the two the same box`,
      )
    }
    assert.match(textOf(controls[0]).trim(), /accept/i, `${page.rel}: the accepting choice does not say what it accepts`)
    assert.match(textOf(controls[1]).trim(), /decline/i, `${page.rel}: the refusing choice is not named as a refusal`)

    // What is measured, in the banner, and where the statement lives.
    const bannerText = textOf(banner)
    assert.match(bannerText, /Google Analytics/i, `${page.rel}: the banner does not name the tool it asks about`)
    assert.match(bannerText, /identifiers/i, `${page.rel}: the banner does not say that the tool sets identifiers (D93)`)
    assert.ok(find(banner, 'a')?.attrs.get('href') === PRIVACY_PATH, `${page.rel}: the banner does not link ${PRIVACY_PATH}`)

    // A keyboard user meets it before the page content: after the skip link, before <main>.
    const order = [...tokenize(page.html)].filter((t) => t.kind === 'start' && ['a', 'aside', 'main'].includes(t.name))
    const skip = order.findIndex((t) => t.name === 'a' && (t.attrs.get('class') ?? '') === 'skip-link')
    const at = order.findIndex((t) => t.name === 'aside' && t.attrs.get('id') === 'consent-banner')
    const main = order.findIndex((t) => t.name === 'main')
    assert.ok(skip >= 0 && at > skip && main > at, `${page.rel}: the banner is not between the skip link and <main>, so the tab order reaches the page content first`)
  }

  // And the pair's own rules, read once: one unconditional rule draws the box both choices
  // share — the target height and the padding — and the two modifiers that tell the primary
  // from the secondary set paint and nothing else. That is the whole of the difference the
  // owner asked for (the feedback of 2026-10-30: a clean pair, the accept primary and the
  // decline secondary, both the same size and both always drawn). The box is looked for by
  // what it declares, not by a class name alone: `:active` and the hover rules are the pair's
  // too, and a rule that only scales or repaints on a press is not the box.
  const page = SHIPPED.find((p) => p.html.includes('consent-banner__button'))
  assert.ok(page, 'no built page carries the consent pair for its rules to be read from')
  const css = pageCSS(page)
  const pair = cssRules(css).filter(
    (rule) => typeof rule.selector === 'string' && rule.selector.includes('.consent-banner__button'),
  )
  const box = pair.filter(
    (rule) =>
      rule.conditions.length === 0 &&
      !rule.selector.includes('--') &&
      cssDeclarations(rule.body).some(([property]) => property === 'min-height') &&
      cssDeclarations(rule.body).some(([property]) => property === 'padding'),
  )
  assert.ok(
    box.length > 0,
    `${page.rel}: no rule in force at every width declares both the target height and the padding of the consent pair, so nothing gives the two choices the same box — and no other gate measures the banner's buttons against the target size`,
  )
  // The target size itself, read by the same reading the hero's and the caps page's controls
  // are held to (`lib/controls.mjs`), because this is the only place it is measured at all.
  assert.deepEqual(
    controlTargetFailures(css, [['consent-banner__button', "the consent pair's choices"]]),
    [],
    `${page.rel}: a consent choice is not the 44 px target the owner's brief measures every control against`,
  )
  const PAINT = new Set(['background', 'background-color', 'color', 'border-color', 'box-shadow'])
  for (const rule of pair.filter((one) => one.selector.includes('.consent-banner__button--'))) {
    for (const [property] of cssDeclarations(rule.body)) {
      assert.ok(
        PAINT.has(property),
        `${page.rel}: the rule "${rule.selector}" changes ${property}, so the two consent choices are not the same box — the pair may differ in how it is filled and in nothing else, or one of them is the easier one to press`,
      )
    }
  }
})

test('the banner asks one question about everything the site may load, and names no vendor that is not loaded', () => {
  // The owner's ruling of 2026-10-29, on the finding that the privacy page's bar writes a
  // refusal into `fugaro:marketing-consent` while its words asked about analytics alone: the bar
  // is worded for measurement as a whole — cookies, Google Analytics, and the marketing trackers
  // and the booking widget the campaign pages carry — on every page. The owner's feedback of
  // 2026-10-30 cut it to two sentences, and left this rule standing: a vendor is NAMED only while
  // the site is configured to load it, and the rest are described as the class they are.
  // `site.yaml` ships both vendor ids empty (R3) and a loader with no id fetches nothing, so
  // naming them would be a sentence the artifact does not keep. The pair is read in both
  // directions, so supplying an id brings the name back with it.
  const facts = siteFacts()
  const campaignVendors = [
    ['Meta', facts.marketing.metaPixelID],
    ['LinkedIn', facts.marketing.linkedinPartnerID],
    ['Calendly', loadLanding().calendlyURL],
  ]
  for (const page of SHIPPED) {
    const banner = findAll(parse(page.html), (el) => el.attrs.get('id') === 'consent-banner')[0]
    assert.ok(banner, `${page.rel} carries no banner for the wording below to be read from`)
    const text = textOf(banner)
    for (const named of ['cookies', 'Google Analytics']) {
      assert.ok(
        text.includes(named),
        `${page.rel}: the banner does not name ${named}, so a choice made in it does not cover everything the site may load (the owner's ruling of 2026-10-29)`,
      )
    }
    assert.match(text, /marketing trackers/i, `${page.rel}: the banner no longer describes the campaign pages' measurement, so a choice in it does not cover what those pages load`)
    for (const [vendor, configured] of campaignVendors) {
      const names = text.includes(vendor)
      assert.equal(
        names,
        configured.length > 0,
        configured.length > 0
          ? `${page.rel}: the banner does not name ${vendor}, which the site is configured to load, so a choice made in it does not cover everything the site may load`
          : `${page.rel}: the banner names ${vendor}, and nothing in this repository supplies the value that would load it: a loader with no id fetches nothing, and the wording has to stay true while that is so (the owner's feedback of 2026-10-30)`,
      )
    }
    assert.match(text, /until you accept/i, `${page.rel}: the banner does not say the load waits for the choice`)
  }

  // The bar the finding was about. The privacy page is the one page whose bar carries the
  // campaign decision's key without the campaign's configuration (`Base.astro`), so a refusal
  // there writes the campaign answer down as a refusal — taking back a grant given on a campaign
  // page — and its accept is the analytics question alone. The bar's two sentences are the same
  // on every page since the owner's feedback of 2026-10-30, so both claims are made by the
  // statement beside it, which the bar links: a reader who pressed "Accept all" on the page that
  // states the campaign question must not be left thinking they agreed to it.
  const privacy = textOf(parse(pageOf(SHIPPED, 'privacy/index.html')))
  assert.match(
    privacy,
    /takes the campaign answer back/i,
    'the privacy statement no longer says what the bar\'s refusal covers: that bar carries the campaign decision\'s key and writes it down as a refusal, and the wording has to say so',
  )
  assert.match(
    privacy,
    /leaves the campaign question where it stands/i,
    'the privacy statement no longer says the bar\'s accept is the analytics question alone, so a reader who accepted there could think the campaign trackers were agreed to',
  )
})

test('the shipped scripts never take the keyboard: no preventDefault, no stopPropagation, no focus trap', () => {
  for (const page of SHIPPED) {
    for (const token of tokenize(page.html)) {
      if (token.kind !== 'start' || token.name !== 'script' || token.attrs.has('src')) continue
      const body = token.raw ?? ''
      assert.ok(!/preventDefault|stopPropagation|stopImmediatePropagation/.test(body), `${page.rel}: a script the site ships intercepts an event, which is how a focus trap starts`)
    }
  }
})

// ---- the privacy statement, and the claims it makes -------------------------------------

/**
 * The part of the privacy page that is about the SITE: everything inside `main` before the
 * second `h2`, which is where "The software" begins (the page carries two named parts — the
 * owner's request of 2026-10-19 — and only the first is about what the site collects).
 *
 * The vendor rule below is scoped to this part and not to the whole page, and the scoping is
 * the correction the software part forced: the phone app signs in to the reader's OWN Firebase
 * project, so the page names Firebase in the software part by design, and a scan of the whole
 * page would read that as the site's measurement naming a vendor it does not have. The rule is
 * unchanged for the part it was written about.
 */
function sitePartOf(html) {
  const main = find(parse(html), 'main')
  if (main === null) return ''
  const nodes = [...walk(main)]
  const h2s = nodes.filter((el) => el.name === 'h2')
  if (h2s.length < 2) return textOf(main)
  // The two part headings have to be SIBLINGS, at the same level, and this reader refuses a page
  // where they are not: the site's part is everything between the first heading and the second,
  // and that reading only means anything if the second one closes the first. A page whose
  // "The software" heading is nested inside "The website" — or sits beside it in a container of
  // its own — has no site part this reader can name, so it is refused rather than guessed at.
  if (h2s[0].parent !== h2s[1].parent) {
    throw new Error(
      `the privacy page's two parts do not share a parent, so there is no site part to read: the part headings are siblings, one after the other — a nested or separately wrapped "The software" makes the site's scope unreadable (see website/test/privacy.mjs, which refuses the same page for the same reason)`,
    )
  }
  // Document order, not sibling index: what the reader meets after the first heading and before
  // the second is the site's part, whatever markup sits between the two.
  return nodes
    .slice(nodes.indexOf(h2s[0]) + 1, nodes.indexOf(h2s[1]))
    .map(textOf)
    .join(' ')
}

test('the privacy page states what is collected in the state the site is built in', () => {
  const off = sitePartOf(pageOf(FLAG_OFF.pages, 'privacy/index.html'))
  assert.match(off, /collects nothing/i, 'with analytics off the privacy page does not say that the site collects nothing')
  assert.ok(!/Google Analytics|Firebase|googletagmanager/i.test(off), 'the privacy page names the analytics vendor while analytics are switched off')

  const on = textOf(parse(pageOf(SHIPPED, 'privacy/index.html')))
  assert.match(on, /Google Analytics/i, 'the privacy page does not name the tool the site would load (D93)')
  assert.match(on, /identifiers/i, 'the privacy page does not state that the tool sets identifiers (D93)')
  assert.match(on, /after you accept|after consent/i, 'the privacy page does not state that analytics load only after consent')
  assert.match(on, /Decline/, 'the privacy page does not say how consent is withdrawn')
  assert.match(on, new RegExp(CONSENT_KEY), 'the privacy page does not say where the choice is stored')
})

test('the vendor rule reads the site part, and a vendor named in it is still refused', () => {
  // The scoping above is a correction and not a weakening: a vendor named in the site's own part
  // is the thing the rule was written for, and a whole-page scan is what made the page's software
  // part — where Firebase is the reader's own project, not the site's measurement — a finding.
  const shell = (site, software) =>
    `<!DOCTYPE html><html lang="en"><head><title>t</title></head><body><main><h1>Privacy</h1><h2>The website</h2><p>${site}</p><h2>The software</h2><p>${software}</p></main></body></html>`

  assert.match(sitePartOf(shell('Google Analytics counts visits.', 'Your own Firebase project.')), /Google Analytics/, 'a vendor named in the site part is not read, so the rule could not refuse one')
  assert.ok(!/Google Analytics|Firebase|googletagmanager/i.test(sitePartOf(shell('This site collects nothing.', 'Your own Firebase project.'))), 'the vendor rule reads the software part, which is where the reader\'s own Firebase project is named')
  assert.equal(sitePartOf('<!DOCTYPE html><html lang="en"><head><title>t</title></head><body><p>no main</p></body></html>'), '', 'a page with no main is not a site part, and this reader must not invent one')

  // The software part now names Google as the holder of the app's reports, in the shipped page's
  // own words, and the site's part stays clean: naming Google or Firebase in the software part is
  // allowed, and the rule still refuses each of them named as what the SITE loads.
  const software = `The app reports to Google, which holds it as Firebase's processor, under Google's Firebase privacy policy.`
  const off2 = sitePartOf(shell('This site collects nothing.', software))
  assert.ok(!/Google|Firebase|googletagmanager/i.test(off2), 'naming Google in the software part makes the site part read as the site\'s vendor')
  assert.match(sitePartOf(shell('Google Analytics counts visits.', software)), /Google Analytics/, 'a vendor named in the site part is still read when the software part also names one')
  for (const vendor of ['Google Analytics', 'Firebase', 'googletagmanager']) {
    assert.ok(!sitePartOf(shell('This site collects nothing.', `The app reports through ${vendor}.`)).includes(vendor), `${vendor} named in the software part is read into the site part, so the page cannot say what the app does without tripping the site's own rule`)
  }
})

test('a page whose two part headings do not share a parent is refused, not guessed at', () => {
  // The reader used to take the first heading's siblings and drop the last one, which reads a
  // page where "The software" sits inside "The website" as though the site's part were the whole
  // of main minus one heading. That is the defect this case exists for: a nested part has no
  // scope to read, so the reader refuses the page.
  const nested = `<!DOCTYPE html><html lang="en"><head><title>t</title></head><body><main><h1>Privacy</h1><h2>The website</h2><div><p>site</p><h2>The software</h2><p>software</p></div></main></body></html>`
  assert.throws(() => sitePartOf(nested), /two parts do not share a parent/, 'a page whose second part is nested under the first is read as though it had a site part')

  const sectioned = `<!DOCTYPE html><html lang="en"><head><title>t</title></head><body><main><h1>Privacy</h1><section><h2>The website</h2><p>site</p></section><section><h2>The software</h2><p>software</p></section></main></body></html>`
  assert.throws(() => sitePartOf(sectioned), /two parts do not share a parent/, 'a page whose two parts are each in a container of their own is read as though it had a site part')

  // The control: the shipped shape — two siblings — is read, and reads the site's part only.
  const siblings = `<!DOCTYPE html><html lang="en"><head><title>t</title></head><body><main><h1>Privacy</h1><h2>The website</h2><p>Google Analytics counts visits.</p><h2>The software</h2><p>Firebase is yours.</p></main></body></html>`
  assert.match(sitePartOf(siblings), /Google Analytics/, 'the sibling shape the page is built in is not read at all')
})

/**
 * The options of the site's one `gtag('config', …)` call, as the script writes them: read out
 * of the file rather than assumed, because the point of the case below is that the privacy
 * page and the call move together.
 */
function configOptions(source) {
  const call = /gtag\(\s*'config'\s*,\s*id\s*,\s*\{([^}]*)\}\s*\)/.exec(source)
  assert.ok(call, "the site's script makes no `gtag('config', id, {…})` call this reader can find, so the page's stated defaults have nothing to be held to")
  const options = new Map()
  for (const m of call[1].matchAll(/([A-Za-z_][A-Za-z0-9_]*)\s*:\s*(true|false)/g)) options.set(m[1], m[2] === 'true')
  return options
}

test("the privacy defaults the page states are the ones the site's script makes", () => {
  const text = textOf(parse(pageOf(SHIPPED, 'privacy/index.html')))
  const script = readFileSync(path.join(DIST, SITE_SCRIPT.replace(/^\//, '')), 'utf8')
  const options = configOptions(script)

  // Both directions, because either half can drift out from under the other: an option the
  // script sets is a sentence the page owes the reader, and an option the script stops setting
  // is a sentence the page may not keep. The pair is the whole of what the call sets, so a
  // third option fails here until the page discloses it — which is the only way a setting can
  // reach the measurement without the statement changing with it.
  assert.deepEqual(
    [...options].sort(),
    [
      ['allow_ad_personalization_signals', false],
      ['allow_google_signals', false],
    ],
    "the script's config call sets an option this case does not hold the privacy page to: add the sentence that discloses it, or take the option out (W3.2)",
  )
  assert.match(text, /Google Signals/i, 'the script turns Google Signals off and the privacy page no longer says so')
  assert.match(text, /ads personalisation off/i, 'the script turns ads personalisation off and the privacy page no longer says so')

  // The two defaults whose other half is the script's text rather than its options table.
  assert.match(text, /one page view per page/i, 'the privacy page no longer says how much is asked for')
  // The events are the one thing this site asks GA4 for beyond the page view (G2.4), and the
  // page names all three — so the pair is read in both directions, exactly as the options are:
  // an event the script can send that the page does not name is a measurement the reader was
  // not told about, and a name the page keeps that the script no longer sends is a sentence the
  // page may not keep. The names are read off the two call shapes the one helper has.
  const events = [...script.matchAll(/(?:keyEvent|press)\([^)]*['"]([a-z]+_[a-z]+)['"]/g)].map((m) => m[1]).sort()
  assert.deepEqual(
    events,
    ['demo_booked', 'install_click', 'whatsapp_click'],
    `the script sends ${JSON.stringify(events)}: the campaign pages' three key events are the whole of what this site asks GA4 for beyond the page view (G2.4)`,
  )
  for (const name of events) {
    // A plain substring: the page's text is read with the tags taken out, so two words that meet
    // across a tag boundary — "and<code>whatsapp_click</code>" — arrive here with no space
    // between them, and a word-boundary anchor would fail on the page that reads correctly.
    assert.ok(text.includes(name), `the script sends the ${name} event and the privacy page does not name it`)
  }
  assert.ok(
    !/gtag\(\s*['"]event['"]\s*,\s*['"]/.test(script),
    'the script queues an `event` call whose name is written at the call site: every event this site sends is one the privacy page names, and this case reads them off the one helper that sends them',
  )
  assert.match(text, /no user id/i, 'the privacy page no longer says it sends no user id')
  assert.ok(
    !/user_id|user_pseudo_id/.test(script),
    'the script names a user id and the privacy page says it sends none: the page is the claim and this file is the fact',
  )

  // The two claims W3.2's review added, each held to the line of the script that makes it
  // true: the page is reported by its path alone, and declining stops this page as well.
  assert.match(text, /path, without any query/i, 'the privacy page no longer says only the path is reported')
  assert.ok(/page_location:\s*bare\(/.test(script), 'the privacy page says only the path is reported and the script no longer strips the address')
  assert.match(text, /stops\s+reporting at once, on this page too/i, 'the privacy page no longer says declining stops the page already measuring')
  assert.ok(/'ga-disable-'/.test(script), "the privacy page says declining stops this page and the script no longer sets Google's opt-out flag")

  // And the sentence the owner's ruling of 2026-10-18 put there: withdrawing takes the cookies
  // Google set with it. The page is the claim and the file is the fact, so the two are held to
  // each other in both directions, the way the defaults above are.
  assert.match(
    text,
    /deletes Google's cookies/i,
    "the privacy page no longer says that withdrawing deletes the cookies Google set (owner's ruling, 2026-10-18)",
  )
  assert.ok(
    /document\.cookie/.test(script),
    "the privacy page says withdrawing deletes Google's cookies and the script never touches document.cookie",
  )

  // And the vendor the page names: the gate loads from the origin the banner's own markup
  // carries, so the origin has to be Google's while the page says Google Analytics. A gate
  // pointed at another vendor would leave the page's first sentence standing and untrue.
  const banner = findAll(parse(pageOf(SHIPPED, 'index.html')), (el) => el.attrs.has('data-analytics-origin'))[0]
  assert.ok(banner, 'the built home page carries no banner configuration to read the gate\'s origin from')
  const origin = banner.attrs.get('data-analytics-origin')
  assert.match(
    origin,
    /^https:\/\/[a-z0-9.-]*google[a-z0-9.-]*\.(?:com|com\.[a-z]{2})$/,
    `the gate loads its script from ${origin} while the privacy page names Google Analytics: the name and the origin have to be the same vendor (W3.2)`,
  )
})

test('the privacy statement names the campaign vendors the site is configured to load, and no others', () => {
  // The page is the claim and this file is the fact (W3.2), and G2.3 adds three claims to it:
  // which vendors the four campaign pages may load, which pages those are, and what withdrawing
  // deletes. Each is held to the artifact that makes it true rather than to a list kept here, so
  // a vendor, a page or a cookie that moves takes the sentence with it — including the two
  // vendors the owner has not supplied an id for, which the page describes without naming (the
  // owner's feedback of 2026-10-30).
  const text = textOf(parse(pageOf(SHIPPED, 'privacy/index.html')))
  const script = readFileSync(path.join(DIST, SITE_SCRIPT.replace(/^\//, '')), 'utf8')

  const facts = siteFacts()
  // A vendor is named only where the site is configured to load it (the owner's feedback of
  // 2026-10-30), and both directions are held: the ids `site.yaml` ships empty name nothing, and
  // a value supplied there is a name the page owes. The ones that are not named are still
  // covered as a class, which is what the campaign question asks about.
  for (const [vendor, configured] of [
    ['Meta', facts.marketing.metaPixelID],
    ['LinkedIn', facts.marketing.linkedinPartnerID],
    ['Calendly', loadLanding().calendlyURL],
  ]) {
    assert.equal(
      text.includes(vendor),
      configured.length > 0,
      configured.length > 0
        ? `the privacy page does not name ${vendor}, which the campaign pages are configured to load (G2.3, D401)`
        : `the privacy page names ${vendor} while nothing supplies the value that would load it: a vendor with no id is not loaded, and the page may not say it is (the owner's feedback of 2026-10-30)`,
    )
  }
  assert.match(text, /marketing trackers/i, 'the privacy page describes the campaign pages\' measurement as nothing at all, so a reader cannot tell what their question covers')
  for (const variant of loadLanding().variants) {
    assert.ok(text.includes(variant.path), `the privacy page does not name ${variant.path}, one of the four campaign pages the second question is asked on, so a reader cannot tell where it applies`)
  }
  assert.match(text, new RegExp(MARKETING_CONSENT_KEY), 'the privacy page does not say where the campaign answer is kept')
  assert.match(text, /apart from the analytics answer/i, 'the privacy page does not say the campaign answer is separate from the analytics one')

  // Withdrawing deletes the first-party cookies the vendors set, and the list is read out of
  // the script rather than restated: a cookie the script stops deleting is a sentence the page
  // may not keep, and one it starts deleting is a sentence the page owes.
  const cookies = /var MARKETING_COOKIES = \[([^\]]*)\]/.exec(script)
  assert.ok(cookies, `${SITE_SCRIPT} no longer carries the list of cookies the campaign withdrawal deletes, so the page's claim about it has nothing to be held to`)
  const named = [...cookies[1].matchAll(/'([^']+)'/g)].map((m) => m[1])
  assert.ok(named.length > 0, `${SITE_SCRIPT} deletes no campaign cookie, and the privacy page says withdrawing deletes Meta's`)
  for (const cookie of named) {
    assert.ok(text.includes(cookie), `the privacy page does not name ${cookie}, which withdrawing deletes`)
  }
  assert.ok(/'consent',\s*'revoke'/.test(script), "the privacy page says withdrawing stops the pixel and the script no longer revokes its consent")

  // And the limit the page states rather than hides: what it cannot remove is a vendor's own
  // cookie on the vendor's domain, and a script already fetched runs until the reader reloads.
  assert.match(text, /keeps running until you reload/i, 'the privacy page no longer says a script already fetched keeps running until the reader reloads')
  assert.match(text, /the vendor's to remove/i, "the privacy page no longer says a cookie on the vendor's own domain is the vendor's to remove")
})

test('the claims the privacy page makes about the site are held to the artifact', () => {
  for (const page of [...SHIPPED]) {
    for (const el of walk(parse(page.html))) {
      // The one field the site carries: the homepage hero's live card, where a reader types a
      // task and sees it drawn as jobs (the owner's ruling of 2026-10-23, the redesign's Step 3,
      // lever 3). It is not a form and submits nothing: it has no `name`, no `<form>` around it,
      // and the script that reads it sends and stores nothing (held below). Any other field, and
      // any `<form>`, is still the claim broken.
      if (el.name === 'textarea' && page.rel === 'index.html' && el.attrs.get('id') === 'hero-task') {
        assert.ok(!el.attrs.has('name') && !el.attrs.has('form'), `${page.rel}: the hero's task field carries a name or a form, so it is shaped to be submitted`)
        for (let up = el.parent; up; up = up.parent) assert.notEqual(up.name, 'form', `${page.rel}: the hero's task field sits in a <form>`)
        continue
      }
      assert.ok(
        !['form', 'input', 'textarea', 'select'].includes(el.name),
        `${page.rel} carries a <${el.name}>: the privacy page says the site asks for nothing and has no form, and this is that claim held to the built pages`,
      )
    }
  }
  // The task field's half of the claim: the script the site ships never sends anything a reader
  // typed. It opens no request of its own at all (the one request it makes is the gated Google
  // loader, a `<script>` element), and the live card's section reads the field and touches no
  // storage.
  const source = readFileSync(path.join(DIST, SITE_SCRIPT.replace(/^\//, '')), 'utf8')
  for (const sender of ['fetch(', 'XMLHttpRequest', 'sendBeacon', 'WebSocket', 'EventSource']) {
    assert.ok(!source.includes(sender), `${SITE_SCRIPT} uses ${sender}: the site sends nothing a reader types`)
  }
  const live = source.slice(source.indexOf('function liveRun()'), source.indexOf('function entrances()'))
  assert.ok(live.length > 0, `${SITE_SCRIPT} no longer carries the live card's section this case reads`)
  assert.ok(!/localStorage|sessionStorage|document\.cookie|indexedDB/.test(live), "the hero's live card touches storage: it keeps nothing a reader types")
})

// ---- the policy the artifact is served with ---------------------------------------------

test("the deployed policy admits exactly the origins the gate loads from, and the site's one script is its own file", () => {
  const firebase = JSON.parse(readFileSync(path.join(REPO, 'firebase.json'), 'utf8'))
  const headers = firebase.hosting.headers.flatMap((entry) => entry.headers)
  const csp = headers.find((header) => header.key.toLowerCase() === 'content-security-policy')
  assert.ok(csp, 'firebase.json carries no Content-Security-Policy header')

  // `origins.mjs` reads the whole policy against the contract Google's documentation states.
  // What this file adds is the cross-check the gate needs: the policy admits exactly the
  // origins the gate loads from — Google's, and the campaign's three (G2.3, D401). A policy
  // narrower than the gate blocks a load that was consented to; a wider one admits a request
  // nothing consented to.
  assert.deepEqual(
    remoteScriptSources(csp.value),
    [ANALYTICS_ORIGIN, ...CAMPAIGN_ORIGINS],
    'firebase.json\'s script-src does not admit exactly the origins the gate loads from',
  )
  assert.ok(!/script-src[^;]*'unsafe-inline'/.test(csp.value), "firebase.json's script-src carries 'unsafe-inline'")
  for (const page of SHIPPED) {
    assert.deepEqual(
      scriptSources(page.html),
      [SITE_SCRIPT, EARLY_SCRIPT],
      `${page.rel} ships a script the policy above does not admit: the site's scripts are its own files (the one script and the banner's early decision, the owner's ruling of 2026-10-23), and an inline script would need a hash the policy does not carry`,
    )
  }
})

// ---- the refusals, kept ------------------------------------------------------------------

// A minimal page in the shape the layout writes, with one thing planted in its head or body.
function page({ head = '', body = '' } = {}) {
  return `<!DOCTYPE html><html lang="en"><head><meta charset="utf-8"><title>t</title>${head}</head><body><a class="skip-link" href="#main">Skip to content</a><main id="main"><h1>t</h1>${body}</main></body></html>`
}

test("the red step: a page that emits the analytics tag unconditionally prints the plan's line", () => {
  const cases = [
    ['a tag in the markup', { head: `<script async src="${ANALYTICS_ORIGIN}/gtag/js?id=${MEASUREMENT_ID}"></script>` }],
    ['a tag a script appends', { body: `<script>var s = document.createElement('script'); s.src = '${ANALYTICS_ORIGIN}/gtag/js?id=${MEASUREMENT_ID}'; document.head.appendChild(s)</script>` }],
    ['a preconnect', { head: `<link rel="preconnect" href="${ANALYTICS_ORIGIN}">` }],
  ]
  for (const [what, fixture] of cases) {
    assert.deepEqual(
      gateFailures(page(fixture)),
      ['page requests googletagmanager.com before consent'],
      `${what} was not refused with the plan's line`,
    )
  }
})

test("the red step, on the script the site actually builds: remove the consent comparison and the plan's line comes back", () => {
  const html = pageOf(SHIPPED, 'index.html')
  const source = readFileSync(path.join(DIST, SITE_SCRIPT.replace(/^\//, '')), 'utf8')
  const gate = /if \(stored\(banner\) === GRANTED\) load\(\)/
  assert.match(source, gate, 'the gate the site builds no longer carries the one line this mutation removes')
  const ungated = source.replace(gate, 'load()')
  assert.notEqual(ungated, source, 'the mutation changed nothing')
  assert.deepEqual(
    gateFailures(html, { script: ungated }),
    ['page requests googletagmanager.com before consent'],
    "the plan's red step does not come back when the consent comparison is removed from the file the site builds, so this check is not watching the gate it claims to watch",
  )

  // And the same mutation with a refusal stored, which is the state a returning visitor is
  // in: the loader the flag-on build ships must not load for them either.
  assert.deepEqual(
    gateFailures(html, { script: ungated, storage: { [CONSENT_KEY]: 'denied' } }),
    ['page requests googletagmanager.com before consent'],
    'a loader with no consent comparison loads nothing when a refusal is stored, so the mutation above was not the one it claimed',
  )
})

test('the gate refuses a loader that reads a decision but never checks it, and a decision it cannot keep', () => {
  const reads = `(function () { try { window.localStorage.getItem('${CONSENT_KEY}') } catch (err) {} var s = document.createElement('script'); s.src = '${ANALYTICS_ORIGIN}/gtag/js'; document.head.appendChild(s) })()`
  assert.deepEqual(
    gateFailures(page({ head: `<script>${reads}</script>` })),
    ['page requests googletagmanager.com before consent'],
    'a loader that reads the decision and loads anyway was not refused',
  )

  const keeps = `(function () { var s = document.createElement('script'); s.src = '${ANALYTICS_ORIGIN}/gtag/js'; document.head.appendChild(s) })()`
  assert.deepEqual(
    gateFailures(page({ head: `<script>${keeps}</script>` }), { storage: { [CONSENT_KEY]: 'denied' } }),
    ['page requests googletagmanager.com before consent'],
    'a loader that ignores a stored refusal was not refused',
  )
})

// A minimal banner, wired. The shape a dark pattern takes is what a banner's controls DO,
// so the fixture wires them and the gate watches the requests that follow.
function wiredBanner(onGranted, onDenied) {
  return `<aside id="consent-banner" role="region" aria-label="Analytics consent" hidden>
    <button type="button" data-consent="granted">Accept analytics</button>
    <button type="button" data-consent="denied">Decline</button></aside>
  <script>var banner = document.getElementById('consent-banner'); banner.hidden = false;
  var controls = banner.querySelectorAll('[data-consent]');
  for (var i = 0; i < controls.length; i++) { (function (control) {
    control.addEventListener('click', function () {
      if (control.getAttribute('data-consent') === 'granted') { ${onGranted} } else { ${onDenied} }
    })
  })(controls[i]) }</script>`
}

const LOAD = `var s = document.createElement('script'); s.src = '${ANALYTICS_ORIGIN}/gtag/js'; document.head.appendChild(s)`
const KEEP = `try { window.localStorage.setItem('${CONSENT_KEY}', 'granted') } catch (err) {}`
const REFUSE = `try { window.localStorage.setItem('${CONSENT_KEY}', 'denied') } catch (err) {}`

test('the gate refuses a banner that measures on a refusal, and one that loads without keeping the choice', () => {
  // The dark pattern: declining loads the script anyway.
  assert.deepEqual(
    gateFailures(page({ body: wiredBanner('', LOAD) }), { act: decline }),
    ['page requests googletagmanager.com after the visitor declined'],
    'a banner that loads the script on Decline was not refused',
  )

  // The forgetful banner: it honours the grant for this page view and does not keep it, so
  // the visitor is asked again next time and the answer they gave is not the one stored.
  const forgetful = gateFailures(page({ body: wiredBanner(LOAD, REFUSE) }), { act: accept })
  assert.deepEqual(
    forgetful,
    ['the visitor accepted and the choice was not stored'],
    `a banner that loads the script without storing the choice was not reported: ${forgetful.join('; ')}`,
  )

  // And the honest banner, for contrast: the same fixture, storing what it was told.
  assert.deepEqual(
    gateFailures(page({ body: wiredBanner(`${LOAD}; ${KEEP}`, REFUSE) }), { act: accept }),
    [],
    'a banner that loads once, stores the choice, and loads nothing on a refusal was reported',
  )
})

test('the gate refuses a cookie written by the page', () => {
  assert.deepEqual(
    gateFailures(page({ head: `<script>document.cookie = 'x=1'</script>` })),
    ['page writes document.cookie (x)'],
    'a cookie written by the page was not refused',
  )
})

test('the policy check refuses a script-src that names a remote origin', () => {
  assert.deepEqual(remoteScriptSources("default-src 'self'; script-src 'self'; connect-src 'self'"), [])
  assert.deepEqual(
    remoteScriptSources("default-src 'self'; script-src 'self' https://www.googletagmanager.com 'sha256-abc'; connect-src 'self'"),
    ['https://www.googletagmanager.com'],
  )
  assert.deepEqual(remoteScriptSources("default-src 'self'"), ['the policy has no script-src directive, so a browser falls back to default-src'])
})

// ---- the stub the gate stands on ---------------------------------------------------------

test('the stub records what a page asks for, before and after its scripts run', () => {
  const markup = runPage(page({ head: `<link rel="preconnect" href="${ANALYTICS_ORIGIN}"><img src="/a.png" alt="">` }))
  assert.deepEqual(markup.analyticsRequests(), [ANALYTICS_ORIGIN], 'a markup fetch is not recorded')
  assert.deepEqual(markup.cookies, [], 'the stub recorded a cookie that was never written')

  const clicked = runPage(page({ body: `<script>document.getElementById('main').addEventListener('click', function () { document.cookie = 'a=1' })</script>` }))
  assert.deepEqual(clicked.cookies, [], 'a cookie was recorded before anything ran')
  clicked.byId.get('main').fire('click', { type: 'click' })
  assert.deepEqual(clicked.cookies, ['a=1'], 'a cookie written by a script was not recorded')

  assert.deepEqual(gateFailures(page()), [], 'an empty page was reported as ungated')
  assert.equal(
    SHIPPED_HOME.includes('data-consent'),
    true,
    'the shipped home page carries no consent control, so the case above ran against a page with no gate in it',
  )
})
