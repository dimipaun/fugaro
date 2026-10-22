// website/test/lib/dom.mjs — a browser small enough to read, for the gates that run `site.js`.
//
// `website/public/scripts/site.js` is the site's one script and it has four duties now (D183,
// W1.12, W3.2, and the console slider of 2026-10-21), so more than one gate runs it: `copy.mjs`
// holds the copy control to the markup it is written in, and `console-screens.mjs` holds the
// slider's arrows, its count and its autoplay. **Both run the same file in the same stub**, and
// that is this file's whole reason for existing — a second harness would be a second answer to
// "what does the script do", and the two would drift the first time the script changed.
//
// What the stub is: the built page parsed by `lib/html.mjs` and wrapped in the handful of DOM
// calls the script makes — `querySelector(All)`, `getAttribute`/`setAttribute`, `classList`,
// `closest`, `contains`, `getClientRects`, `focus`, `addEventListener`/`dispatch`, `hidden`,
// `textContent` — plus the four pieces of browser behaviour the script's duties depend on and
// nothing more:
//
//   - **the clipboard** (`navigator.clipboard`), which may be absent, may accept or may refuse,
//     because the script reveals the copy control only where one is there to write to;
//   - **the media queries** (`window.matchMedia`), with the answers the cases hand in: the
//     pointer's capability (the menu's hover behaviour) and the visitor's motion preference (the
//     slider's autoplay). A query the case did not name answers `false`, which is what a browser
//     with neither setting answers;
//   - **the timers** (`setTimeout` for the copy status, `setInterval` for the slider's autoplay),
//     recorded rather than run: nothing here fires on its own clock, and a case fires the one it
//     is about by hand;
//   - **a scroll box**: one width for every element, which is all the script asks for — it reads
//     `clientWidth` of the slider's track and nothing else's — and a `scrollLeft` per element
//     that `scrollTo({ left })` moves, so a case can read where a press sent the track.
//
// **It is a stub, and it says so.** No layout, no real event loop, no rendering: what a real
// clipboard does with a real click, what a swipe does with a real trackpad and what a real
// `prefers-reduced-motion` setting does to a real page are the owner's live checks
// (`docs/gcp-live-checklist.md`) and no case here claims to have run one. What it can hold is
// that the script reads the markup the build wrote, writes the state the markup is read for, and
// makes the decisions the cases name — which is the half that would rot in silence.
//
// Standard library only (Global Constraint 4); no network, and no browser.

import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import vm from 'node:vm'
import { SITE, parse, textOf, walk } from './html.mjs'

/** The site's one script, as the file the build copies into `dist/` verbatim. */
export const SCRIPT_SOURCE = readFileSync(path.join(SITE, 'public', 'scripts', 'site.js'), 'utf8')

/** One parsed element, with the DOM surface `site.js` touches. */
export class FakeElement {
  constructor(node, dom) {
    this.node = node
    this.dom = dom
    this.listeners = {}
  }
  get attrs() {
    return this.node.attrs
  }
  getAttribute(name) {
    return this.attrs.has(name) ? this.attrs.get(name) : null
  }
  hasAttribute(name) {
    return this.attrs.has(name)
  }
  setAttribute(name, value) {
    this.attrs.set(name, String(value))
  }
  get hidden() {
    return this.attrs.has('hidden')
  }
  set hidden(value) {
    if (value) this.attrs.set('hidden', '')
    else this.attrs.delete('hidden')
  }
  get open() {
    return this.attrs.has('open')
  }
  set open(value) {
    if (value) this.attrs.set('open', '')
    else this.attrs.delete('open')
  }
  get textContent() {
    return textOf(this.node)
  }
  set textContent(value) {
    this.node.children = [{ text: String(value), parent: this.node }]
  }
  get classList() {
    const classes = (this.attrs.get('class') ?? '').split(/\s+/)
    return { contains: (name) => classes.includes(name) }
  }
  get nextElementSibling() {
    const siblings = this.node.parent.children.filter((child) => child.name !== undefined)
    const next = siblings[siblings.indexOf(this.node) + 1]
    return next ? this.dom.wrap(next) : null
  }
  // The scroll box: one width for every element (the script reads the slider's track and no
  // other element's) and a scroll offset of its own that a move keeps.
  get clientWidth() {
    return this.dom.width
  }
  get scrollLeft() {
    return this.dom.scrollOf(this.node)
  }
  set scrollLeft(value) {
    this.dom.scrolled.set(this.node, Number(value))
  }
  // Recorded as well as applied: where the track ended up is what a case asserts, and what the
  // script asked for is the other half — a move is `behavior: 'instant'` or it is the
  // stylesheet's animation, and nothing in `scrollLeft` afterwards can tell the two apart.
  scrollTo(init) {
    this.dom.scrolls.push({ node: this.node, init })
    if (init && typeof init.left === 'number') this.scrollLeft = init.left
  }
  contains(other) {
    for (let node = other?.node; node; node = node.parent) if (node === this.node) return true
    return false
  }
  matches(selector) {
    return this.dom.matches(this.node, selector)
  }
  closest(selector) {
    for (let node = this.node; node && node.name !== '#document'; node = node.parent) if (this.dom.matches(node, selector)) return this.dom.wrap(node)
    return null
  }
  querySelector(selector) {
    return this.querySelectorAll(selector)[0] ?? null
  }
  querySelectorAll(selector) {
    return [...walk(this.node)].filter((node) => this.dom.matches(node, selector)).map((node) => this.dom.wrap(node))
  }
  // Rendered unless an ancestor is a closed `<details>` — a group's list is the disclosure's
  // own child, and that is the whole of what hides it — or the case says otherwise.
  getClientRects() {
    return this.dom.drawn(this.node) ? [{}] : []
  }
  focus() {
    this.dom.document.activeElement = this
  }
  addEventListener(type, fn) {
    ;(this.listeners[type] ??= []).push(fn)
  }
  // The script takes its pointer listeners off again when the device's answer changes
  // (`decideHover`), so the stub has to be able to forget one.
  removeEventListener(type, fn) {
    const bound = this.listeners[type] ?? []
    const at = bound.indexOf(fn)
    if (at >= 0) bound.splice(at, 1)
  }
  /**
   * Dispatch an event at this element. The event carries a working `preventDefault` — what the
   * script calls on a dot press, whose anchor it takes over — and the same object is returned, so
   * a case can read whether the default was cancelled.
   */
  dispatch(type, event = {}) {
    const sent = {
      type,
      defaultPrevented: false,
      preventDefault() {
        this.defaultPrevented = true
      },
      ...event,
      target: event.target ?? this,
    }
    for (const fn of this.listeners[type] ?? []) fn(sent)
    return sent
  }
}

/** The event object `site.js` constructs (`new CustomEvent(type, { detail })`). */
export class FakeCustomEvent {
  constructor(type, init = {}) {
    this.type = type
    this.detail = init.detail
  }
}

/**
 * A built page as a document `site.js` can run in, with a clipboard the case controls.
 *
 * `clipboard` is `'ok'`, `'refuse'` or `'absent'`; `undrawn` answers which elements a case wants
 * treated as not rendered; `storage` is the `localStorage` the page starts with; `width` is the
 * width every element reports (the slider's track is the one that reads it); `scroll` is where
 * the page's scroll boxes already are when it loads, by selector — a deep link into a slide, or
 * a position the browser restored; `reduce` is the visitor's motion preference; `hidden` is
 * whether the tab is in the background; and `matchMedia: false` is a browser old enough to have
 * none. `script` is the source run in the page, which is the file the site ships unless a case
 * hands in a defect planted in it.
 */
export function fakeDOM(html, { clipboard = 'ok', undrawn = () => false, storage = {}, width = 0, scroll = {}, reduce = false, hidden = false, matchMedia = true, script = SCRIPT_SOURCE } = {}) {
  const root = parse(html)
  const wrapped = new Map()
  const written = []
  const timers = []
  const intervals = []
  // What a script appends to <head>, which is how the gate's one analytics tag would show
  // up; and the storage the consent decision goes to. Both are here because `site.js` is one
  // file with four duties (D183, W3.2, the slider) and this harness runs all of it, not only
  // the copy control's third.
  const appended = []
  const store = new Map(Object.entries(storage))
  const windowListeners = new Map()
  const mediaLists = []
  const byId = new Map()
  const scrolled = new Map()
  const scrolls = []
  for (const el of walk(root)) if (el.attrs.has('id')) byId.set(el.attrs.get('id'), el)
  const dom = {
    width,
    scrolled,
    scrolls,
    scrollOf: (node) => scrolled.get(node) ?? 0,
    wrap(node) {
      if (!wrapped.has(node)) wrapped.set(node, new FakeElement(node, dom))
      return wrapped.get(node)
    },
    // Compound selectors joined by descendant spaces, in a comma list: `tag`, `.class`,
    // `[attr]` — the selectors site.js writes, and a refusal for anything else.
    matches(node, selector) {
      return selector.split(',').some((one) => {
        const parts = one.trim().split(/\s+/)
        if (!compound(node, parts[parts.length - 1])) return false
        let at = node.parent
        for (let i = parts.length - 2; i >= 0; i--) {
          while (at && at.name !== '#document' && !compound(at, parts[i])) at = at.parent
          if (!at || at.name === '#document') return false
          at = at.parent
        }
        return true
      })
    },
    drawn(node) {
      for (let at = node; at && at.name !== '#document'; at = at.parent) {
        if (undrawn(at)) return false
        if (at.attrs.has('hidden')) return false
        if (at.parent?.name === 'details' && at.name !== 'summary' && !at.parent.attrs.has('open')) return false
      }
      return true
    },
  }
  function compound(node, part) {
    if (!/^[a-z]*(\.[\w-]+|\[[\w-]+\])*$/.test(part)) throw new Error(`the fake DOM does not understand the selector part ${part}`)
    const tag = /^[a-z]+/.exec(part)?.[0]
    if (tag && node.name !== tag) return false
    const cls = (node.attrs.get('class') ?? '').split(/\s+/)
    for (const [, c] of part.matchAll(/\.([\w-]+)/g)) if (!cls.includes(c)) return false
    for (const [, a] of part.matchAll(/\[([\w-]+)\]/g)) if (!node.attrs.has(a)) return false
    return true
  }
  const docEl = dom.wrap(root)
  const document = Object.assign(docEl, {
    readyState: 'complete',
    activeElement: null,
    getElementById: (id) => (byId.has(id) ? dom.wrap(byId.get(id)) : null),
    createElement: (name) => ({ name, src: '', async: false }),
    head: { appendChild: (element) => appended.push(element) },
    /** The tab going to the background and coming back, as the page hears it. */
    setHidden(value) {
      Object.defineProperty(this, 'hidden', { value: Boolean(value), configurable: true, writable: true })
      docEl.dispatch('visibilitychange', { target: docEl })
    },
  })
  // `document.hidden` is the page's visibility and it is not an attribute of anything: an own
  // property on the document, so it does not read as the `hidden` attribute of an element.
  Object.defineProperty(document, 'hidden', { value: Boolean(hidden), configurable: true, writable: true })
  dom.document = document
  const navigator = {}
  if (clipboard !== 'absent') {
    navigator.clipboard = {
      writeText(text) {
        written.push(text)
        return clipboard === 'ok' ? Promise.resolve() : Promise.reject(new Error('denied'))
      },
    }
  }
  const window = {
    setTimeout: (fn, ms) => timers.push({ fn, ms }),
    setInterval(fn, ms) {
      const timer = { fn, ms, cancelled: false }
      intervals.push(timer)
      return timer
    },
    clearInterval(timer) {
      if (timer) timer.cancelled = true
    },
    localStorage: {
      getItem: (key) => (store.has(key) ? store.get(key) : null),
      setItem: (key, value) => store.set(key, String(value)),
      removeItem: (key) => store.delete(key),
    },
    addEventListener(type, fn) {
      if (!windowListeners.has(type)) windowListeners.set(type, [])
      windowListeners.get(type).push(fn)
    },
    dispatchEvent(event) {
      for (const fn of windowListeners.get(event.type) ?? []) fn(event)
      return true
    },
  }
  // The visitor's own settings, and the lists the page asked for them on. The answers are not
  // fixed: `setReduce` below is the visitor turning reduced motion on under a page that has
  // already loaded, and a browser reports that to the lists subscribed to *that* query as a
  // `change` — the pointer's own list hears nothing, because the pointer did not move.
  if (matchMedia) {
    window.matchMedia = (query) => {
      const listeners = []
      const list = {
        media: query,
        get matches() {
          return /prefers-reduced-motion/.test(query) ? reduce : false
        },
        addEventListener(type, fn) {
          if (type === 'change') listeners.push(fn)
        },
        // The name a MediaQueryList had for the same subscription before `addEventListener`
        // reached it. A browser old enough to need this one still reports the change.
        addListener(fn) {
          listeners.push(fn)
        },
      }
      mediaLists.push({ listeners, motion: /prefers-reduced-motion/.test(query) })
      return list
    }
  }
  // Where the scroll boxes already are when the page loads: `{ '.screens-track': 2000 }` is a
  // reader arriving on the third slide, by deep link or by a restored position, and it is set
  // before the script runs because that is the only moment it can be — a page's first reading of
  // its own scroll position is the one this is here for.
  for (const [selector, left] of Object.entries(scroll)) {
    const node = [...walk(root)].find((el) => dom.matches(el, selector))
    assert.ok(node, `the page carries no ${selector} to have scrolled`)
    scrolled.set(node, Number(left))
  }
  vm.runInNewContext(script, { window, document, navigator, CustomEvent: FakeCustomEvent })
  return {
    document,
    written,
    timers,
    intervals,
    appended,
    store,
    /** Every `scrollTo` the script made, in order: what it asked the track for, and how. */
    scrolls,
    find: (selector) => document.querySelectorAll(selector),
    /** The timers still running: what a case fires, and what it holds a stop to. */
    running: () => intervals.filter((timer) => !timer.cancelled),
    /** The visitor's motion preference changing under a loaded page. */
    setReduce(value) {
      reduce = Boolean(value)
      for (const { listeners, motion } of mediaLists) if (motion) for (const fn of listeners) fn({ matches: reduce })
    },
    /** Click a consent choice, which is the only way the gate's tag is ever appended. */
    choose(decision) {
      const banner = document.getElementById('consent-banner')
      assert.ok(banner, 'the page carries no consent banner to choose in')
      const control = banner.querySelectorAll('[data-consent]').find((el) => el.attrs.get('data-consent') === decision)
      assert.ok(control, `the banner carries no ${decision} control`)
      control.dispatch('click', { type: 'click' })
    },
  }
}
