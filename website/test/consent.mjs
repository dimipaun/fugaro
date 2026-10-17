// website/test/consent.mjs — the consent gate (website plan, Task W1.12; reconciliation
// findings 16, 18 and 55, decisions D93 and D106, Global Constraint 8).
//
// D93's answer, in one line: the owner chose Firebase Analytics, and **nothing is requested
// from the analytics origin until the visitor consents**. This file is the mechanical form of
// that promise, and it is the other half of W1.11's origins check: `origins.mjs` proves that
// the analytics origin is the only third party the site may name and that it may only be
// named inside the layout's gated loader; this file proves that the gate itself holds —
// before a decision, after a refusal, after a withdrawal, and when the browser refuses to
// store the choice at all.
//
// **It runs the page the way a browser does, because a gate is a property of what a page
// ASKS FOR.** The built HTML's markup fetches (a `script[src]`, a `link rel=preconnect`, an
// `img`) are recorded first, then the page's own inline scripts are run in document order
// against a stub `window` and `document` that record every request a script makes, every
// cookie written, and where focus goes. A check that read the loader's text and agreed with
// it would be a check of the comment; this one watches the network calls.
//
// The four cases D93's answer requires (the plan's step 5), plus the state the site ships in:
//
//   1. **No analytics request before consent** — and none after a refusal.
//   2. **Consent is withdrawable** — the privacy page always offers the choice again,
//      declining overwrites a stored grant, and the next page view requests nothing.
//   3. **No cookie**, before consent or after it: the scripts this site ships write none.
//   4. **The banner is accessible and keyboard-operable** — an accessible name, two equally
//      prominent native buttons, Escape declining, focus that lands somewhere real when the
//      banner goes, and no interception of the keyboard.
//
// And the shipped state, which is the one a reader meets today: with `site.yaml`'s flag false
// there is no banner, no loader, no origin and no script anywhere in the artifact, and the
// privacy page says in one sentence that the site collects nothing.
//
// The red step, and what it turned out to be. The plan's red step reads: "Write the 'no
// request before consent' case against a page that emits the analytics tag unconditionally.
// Expected: FAIL — page requests googletagmanager.com before consent." In this tree that page
// cannot be built: W1.11 (#530) already rewrote the layout's loader so it fetches nothing
// until consent, so the case passes by REFUSING the defect — over three ways of planting it
// (a tag in the markup, a tag a script appends, a preconnect) and once more as a mutation of
// the real page: strip the gate out of the loader the site builds and the plan's line comes
// back. What actually failed before this task's code existed was the other half of D93 —
// consent itself. With no banner there was no way to give it, so the analytics script could
// never load on any page, and the privacy statement did not exist. The nine failures this
// file printed in that state are recorded in the commit that added it.
//
// It reads `dist/`, which `npm run check` builds before it runs this file, and it builds a
// second copy of the site with `site.yaml`'s flag flipped on — the state W3.2 leaves behind —
// because the gate is invisible while the flag is off and a check that only ever ran against
// the off state would prove nothing about it. Standard library only; no network, no browser.

import { after, before, test } from 'node:test'
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { DIST, REPO, builtPages, filesUnder, find, findAll, parse, textOf, tokenize, walk } from './lib/html.mjs'
import { copyRepository } from './lib/build.mjs'

// ---- the facts of the gate ---------------------------------------------------------------

// The one third-party origin D93 admits, the key the decision is stored under, and the page
// that states the whole arrangement. The origin and the key are the same two values
// `origins.mjs` and `Base.astro` hold; a change to either is a change to the gate.
export const ANALYTICS_ORIGIN = 'https://www.googletagmanager.com'
const ANALYTICS_HOST = new URL(ANALYTICS_ORIGIN).host
export const CONSENT_KEY = 'fugaro:analytics-consent'
const PRIVACY_PATH = '/privacy/'

// The measurement id the flag-on copy is built with. Synthetic, and obviously so: W3.2 sets
// the real property, and nothing in this repository holds one.
const MEASUREMENT_ID = 'G-0000000000'

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

/** The element tree of a page, as the objects the scripts above see. */
function stubDOM(html) {
  const state = { activeElement: null }
  const elements = []
  const byId = new Map()

  const document = {
    head: { appendChild: (element) => state.requests.push(element.src) },
    createElement: (name) => ({ name, src: '' }),
    getElementById: (id) => byId.get(id) ?? null,
    querySelectorAll: (selector) => {
      assert.equal(
        selector,
        '[data-consent]',
        `a page script asks for ${selector}: this stub reads exactly one selector, so a new one has to be taught here rather than silently matching nothing`,
      )
      return elements.filter((el) => el.attrs.has('data-consent'))
    },
    addEventListener: () => {},
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
      // The one selector the site's scripts use, over the element's own subtree.
      querySelectorAll(selector) {
        assert.equal(selector, '[data-consent]', `a page script asks for ${selector} on an element: this stub reads only [data-consent]`)
        const out = []
        const walkFrom = (node) => {
          for (const child of node.children) {
            if (child.attrs.has('data-consent')) out.push(child)
            walkFrom(child)
          }
        }
        walkFrom(this)
        return out
      },
    }
    elements.push(el)
    const id = node.attrs.get('id')
    if (id !== undefined) byId.set(id, el)
    for (const child of node.children ?? []) {
      if (child.name === undefined) continue
      el.children.push(build(child))
    }
    return el
  }

  const root = parse(html)
  for (const child of root.children ?? []) if (child.name !== undefined) build(child)

  // `document.cookie`: the scripts may not write one, and this is how a write would show up.
  const cookies = []
  Object.defineProperty(document, 'cookie', {
    get: () => cookies.join('; '),
    set: (value) => cookies.push(String(value)),
  })
  document.cookies = cookies

  state.requests = []
  return { document, elements, byId, state }
}

/**
 * Run a page: its markup fetches, then its inline scripts in document order, against a stub
 * window and document. `readable` and `writable` are the two halves of a browser that
 * refuses storage — a browser can refuse either, and the gate has to survive both.
 */
export function runPage(html, { storage = {}, readable = true, writable = true } = {}) {
  const { document, elements, byId, state } = stubDOM(html)
  const requests = state.requests
  const store = { ...storage }
  const windowListeners = new Map()

  const window = {
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

  // Then the page's own scripts, in the order a browser reaches them.
  for (const token of tokenize(html)) {
    if (token.kind !== 'start' || token.name !== 'script' || token.attrs.has('src')) continue
    if (!EXECUTABLE.has((token.attrs.get('type') ?? '').toLowerCase())) continue
    new Function('window', 'document', 'CustomEvent', token.raw ?? '')(window, document, StubEvent)
  }

  const analyticsRequests = () => requests.filter((url) => url.includes(ANALYTICS_HOST))
  return {
    requests,
    cookies: document.cookies,
    store,
    document,
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
export function gateFailures(html, { storage = {}, readable = true, writable = true, act = null } = {}) {
  const out = []
  const page = runPage(html, { storage, readable, writable })
  const grantedAtLoad = page.store[CONSENT_KEY] === 'granted'
  const atLoad = page.analyticsRequests().length

  if (!grantedAtLoad && atLoad > 0) out.push('page requests googletagmanager.com before consent')
  if (grantedAtLoad && atLoad !== 1) {
    out.push(`page requests googletagmanager.com ${atLoad} times with a grant stored; it loads once`)
  }
  if (page.cookies.length > 0) out.push(`page writes document.cookie (${page.cookies[0].split('=')[0]})`)

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

const SHIPPED = builtPages(DIST)

// The flag-on copy: `site.yaml` as W3.2 will leave it — the flag and a measurement id
// together, which the boundary test and the content pipeline both require to agree — built
// in a temp copy of the repository, with its own node_modules caches (lib/build.mjs).
let TEMP = null
let FLAG_ON = null

before(() => {
  TEMP = mkdtempSync(path.join(tmpdir(), 'fugaro-consent-'))
  const site = copyRepository(path.join(TEMP, 'repo'))
  const file = path.join(site, 'data', 'site.yaml')
  const before_ = readFileSync(file, 'utf8')
  const after_ = before_
    .replace(/^  enabled: false$/m, '  enabled: true')
    .replace(/^  measurement_id: ""$/m, `  measurement_id: "${MEASUREMENT_ID}"`)
  assert.notEqual(after_, before_, 'the fixture did not switch analytics on in the copy, so every case below would run against the off state')
  writeFileSync(file, after_)

  const result = spawnSync(process.execPath, [path.join(site, 'node_modules', 'astro', 'bin', 'astro.mjs'), 'build'], {
    cwd: site,
    encoding: 'utf8',
    env: { ...process.env, ASTRO_TELEMETRY_DISABLED: '1' },
  })
  assert.equal(result.status, 0, `the build with analytics switched on failed:\n${result.stdout ?? ''}${result.stderr ?? ''}`)
  FLAG_ON = { site, dist: path.join(site, 'dist'), pages: builtPages(path.join(site, 'dist')) }
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

const SHIPPED_HOME = pageOf(SHIPPED, 'index.html')

// ---- 1. the state the site ships in ------------------------------------------------------

test('with analytics off, the built site carries no analytics surface at all: no origin, no loader, no banner, no script', () => {
  const ANALYTICS_SHAPED = /googletagmanager|dataLayer|gtag\(|fugaro:analytics-consent|consent-banner/i
  for (const page of SHIPPED) {
    assert.ok(
      !ANALYTICS_SHAPED.test(page.html),
      `${page.rel} names the analytics surface while site.yaml's analytics.enabled is false: the banner and the loader are emitted only when the flag is on, so that the artifact a reader is served today names no third party and loads no script (D93, W1.12 step 3)`,
    )
    assert.ok(
      !/<script/i.test(page.html),
      `${page.rel} ships a script while analytics are off: the consent banner's controller and the gated loader are the site's only scripts, and neither is emitted until the flag is on`,
    )
  }
  const text = ['.html', '.css', '.js', '.json', '.webmanifest', '.svg', '.xml', '.txt']
  for (const rel of filesUnder(DIST).filter((rel) => text.some((ext) => rel.endsWith(ext)))) {
    assert.ok(
      !readFileSync(path.join(DIST, rel), 'utf8').includes(ANALYTICS_HOST),
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
  for (const page of FLAG_ON.pages) {
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

test('the banner appears on a first visit, accepting loads the script once, and declining loads nothing', () => {
  const html = pageOf(FLAG_ON.pages, 'index.html')

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
  const html = pageOf(FLAG_ON.pages, 'index.html')

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

test('consent is withdrawable: the privacy page always offers the choice, and declining there stops the load', () => {
  const privacy = pageOf(FLAG_ON.pages, 'privacy/index.html')
  const page = runPage(privacy, { storage: { [CONSENT_KEY]: 'granted' } })
  assert.ok(page.banner, 'the privacy page carries no consent banner')
  assert.equal(
    page.banner.hidden,
    false,
    'the privacy page hides the choice from a visitor who already accepted, so consent cannot be withdrawn from the one page that says how (W1.12 step 3)',
  )

  page.click('denied')
  assert.equal(page.store[CONSENT_KEY], 'denied', 'declining on the privacy page did not overwrite the stored grant')

  const next = runPage(pageOf(FLAG_ON.pages, 'index.html'), { storage: page.store })
  assert.deepEqual(next.analyticsRequests(), [], 'the withdrawn consent still loaded the analytics script on the next page view')
  assert.equal(next.banner.hidden, true, 'the withdrawn visitor is asked again on the next page, although a decision is stored')
})

test('a browser that refuses storage is treated as undecided: nothing loads, and the banner says so', () => {
  const html = pageOf(FLAG_ON.pages, 'index.html')

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

// ---- 3. no cookie -----------------------------------------------------------------------

test('the site sets no cookie, before consent or after it', () => {
  const html = pageOf(FLAG_ON.pages, 'index.html')
  const page = runPage(html)
  page.click('granted')
  assert.deepEqual(page.cookies, [], 'a script this site ships wrote document.cookie: the site stores one decision and sets no cookie')

  for (const p of [...SHIPPED, ...FLAG_ON.pages]) {
    assert.ok(
      !/document\.cookie/.test(p.html),
      `${p.rel} names document.cookie: no script the site ships reads or writes a cookie (W1.12 step 5)`,
    )
  }
})

// ---- 4. the banner is accessible, keyboard-operable, and traps nothing -------------------

test('the banner is labelled, its two choices are equal, and it is reachable by keyboard before the content', () => {
  for (const page of FLAG_ON.pages) {
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

    // Equal prominence, mechanically: the same element, the same class list, the same
    // size — nothing marks one of the two as the one to press.
    assert.equal(
      controls[0].attrs.get('class'),
      controls[1].attrs.get('class'),
      `${page.rel}: the two consent choices are styled differently, so one of them is easier to press than the other`,
    )
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
})

test('the shipped scripts never take the keyboard: no preventDefault, no stopPropagation, no focus trap', () => {
  for (const page of FLAG_ON.pages) {
    for (const token of tokenize(page.html)) {
      if (token.kind !== 'start' || token.name !== 'script' || token.attrs.has('src')) continue
      const body = token.raw ?? ''
      assert.ok(!/preventDefault|stopPropagation|stopImmediatePropagation/.test(body), `${page.rel}: a script the site ships intercepts an event, which is how a focus trap starts`)
    }
  }
})

// ---- the privacy statement, and the claims it makes -------------------------------------

test('the privacy page states what is collected in the state the site is built in', () => {
  const off = textOf(parse(pageOf(SHIPPED, 'privacy/index.html')))
  assert.match(off, /collects nothing/i, 'with analytics off the privacy page does not say that the site collects nothing')
  assert.ok(!/Google Analytics|Firebase|googletagmanager/i.test(off), 'the privacy page names the analytics vendor while analytics are switched off')

  const on = textOf(parse(pageOf(FLAG_ON.pages, 'privacy/index.html')))
  assert.match(on, /Google Analytics/i, 'the privacy page does not name the tool the site would load (D93)')
  assert.match(on, /identifiers/i, 'the privacy page does not state that the tool sets identifiers (D93)')
  assert.match(on, /after you accept|after consent/i, 'the privacy page does not state that analytics load only after consent')
  assert.match(on, /Decline/, 'the privacy page does not say how consent is withdrawn')
  assert.match(on, new RegExp(CONSENT_KEY), 'the privacy page does not say where the choice is stored')
})

test('the claims the privacy page makes about the site are held to the artifact', () => {
  for (const page of [...SHIPPED, ...FLAG_ON.pages]) {
    for (const el of walk(parse(page.html))) {
      assert.ok(
        !['form', 'input', 'textarea', 'select'].includes(el.name),
        `${page.rel} carries a <${el.name}>: the privacy page says the site asks for nothing and has no form, and this is that claim held to the built pages`,
      )
    }
  }
})

// ---- the policy the artifact is served with ---------------------------------------------

test('the deployed policy admits no remote script while the site ships none', () => {
  const firebase = JSON.parse(readFileSync(path.join(REPO, 'firebase.json'), 'utf8'))
  const headers = firebase.hosting.headers.flatMap((entry) => entry.headers)
  const csp = headers.find((header) => header.key.toLowerCase() === 'content-security-policy')
  assert.ok(csp, 'firebase.json carries no Content-Security-Policy header')

  assert.deepEqual(
    remoteScriptSources(csp.value),
    [],
    "firebase.json's script-src admits a remote origin while the built site ships no script at all: W3.2 is the task that widens this policy, in the commit that turns analytics on, and the hashes it has to name are the ones website/test/origins.mjs lists",
  )
  for (const page of SHIPPED) {
    assert.ok(!/<script/i.test(page.html), `${page.rel} ships a script the policy above does not admit`)
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

test("the red step, on the page the site actually builds: remove the gate from the loader and the plan's line comes back", () => {
  const html = pageOf(FLAG_ON.pages, 'index.html')
  const gate = /if \(decide\(\) === 'granted'\) load\(\)/
  assert.match(html, gate, 'the built loader no longer carries the one line this mutation removes')
  const ungated = html.replace(gate, 'load()')
  assert.deepEqual(
    gateFailures(ungated),
    ['page requests googletagmanager.com before consent'],
    "the plan's red step does not come back when the gate is removed from the loader the site builds, so this check is not watching the gate it claims to watch",
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
  assert.equal(SHIPPED_HOME.includes('consent-banner'), false, 'the shipped home page carries the banner while the flag is off')
})
