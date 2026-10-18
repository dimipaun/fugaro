// website/test/nav-hover.mjs — the menu opens on hover, on a pointer that hovers (the owner's
// brief of 2026-10-18, "Fugaro Website Changes Spec", item 1).
//
// The defect this file exists for: the top menu's three groups opened only on a click, which on
// a desktop is the one gesture a pointer should not have to make. The change the owner agreed:
// on a device whose pointer hovers, a group's submenu opens when the pointer arrives at the
// group and closes a moment after the pointer leaves it; a click still toggles, the keyboard
// still reaches every link, and a touch screen keeps the tap it has always had.
//
// Three things this file holds, and each of them is a rule rather than a preference:
//
//   1. **The decision is the pointer's capability, never the viewport's width.** The query is
//      `(hover: hover) and (pointer: fine)`: a tablet 1280 px wide has no hover to give, and a
//      phone with a mouse attached has one. The stylesheet scopes the menu's hover states to
//      that query and `public/scripts/site.js` asks the same one with `matchMedia`; this file
//      holds both to it, and refuses a width query in either.
//   2. **The states the script drives, driven.** The submenu opens on the pointer's arrival and
//      on the trigger's focus, closes `CLOSE_AFTER_MS` after the pointer leaves and not before,
//      survives a pointer that crosses the seam between the trigger and the list, is left alone
//      by the click the `<summary>` handles by itself, and says what it is in `aria-expanded`.
//   3. **The touch path does nothing.** With no hover to answer for, the pointer's events are
//      bound to nothing at all: the tap is the whole menu, and no close is ever scheduled.
//
// **There is no browser here.** The page is the built artifact parsed by `lib/html.mjs`, wrapped
// in the DOM calls `site.js` makes plus the browser behaviour the menu depends on — `mouseenter`
// and `mouseleave` over a subtree, the `toggle` event a `<details>` fires when its `open`
// attribute changes, `focusin`, and a `<summary>`'s own activation behaviour (a click toggles
// its group). Those are the browser's, not the site's, and the stub states each one where it
// stands. What a real pointer does on a real screen is the owner's live check: the brief's
// acceptance line — "hovering a top-level item on desktop opens its submenu immediately" — is a
// rendered gesture, and `docs/website/redesign/`'s 70a is where the owner runs it.
//
// It reads the built pages out of `dist/`, which `npm run check` builds first, and the script
// itself out of `public/scripts/site.js`, which the build copies into `dist/` verbatim — so a
// hand-mutation of the script is proved without a rebuild, and `budget.mjs` is the gate that
// counts the bytes of the copy that ships. Standard library only; no network, no browser.

import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import vm from 'node:vm'
import { DIST, SITE, builtPages, parse, textOf, walk } from './lib/html.mjs'

// The one query this behaviour may be decided by: the pointer's capability. Stated here once,
// and held to both the stylesheet and the script below.
const POINTER_QUERY = '(hover: hover) and (pointer: fine)'

const SCRIPT_SOURCE = readFileSync(path.join(SITE, 'public', 'scripts', 'site.js'), 'utf8')

const HOME = builtPages(DIST).find((page) => page.rel === 'index.html')
assert.ok(HOME, 'the build wrote no index.html, so there is no menu to test')

// ---- the stylesheet, as the page carries it ---------------------------------------------

/** The CSS a built page carries: its inline `<style>` blocks and any first-party stylesheet. */
function pageCss(html) {
  const inline = [...html.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/g)].map((m) => m[1])
  const linked = [...html.matchAll(/<link[^>]*rel="stylesheet"[^>]*href="(\/[^"]+)"/g)].map((m) =>
    readFileSync(path.join(DIST, m[1]), 'utf8'),
  )
  return [...inline, ...linked].join('\n')
}

/**
 * The `@media` blocks of a stylesheet, as `{ query, body }`, so a rule can be asserted to be
 * *inside* the query it belongs to rather than merely present in the file. The minifier rewrites
 * the query (`min-width: 48rem` comes out as `(width>=48rem)`), which is why every comparison
 * below squashes whitespace first.
 */
function mediaBlocks(css) {
  const out = []
  const re = /@media[^{]*\{/g
  let match
  while ((match = re.exec(css))) {
    let depth = 1
    let i = re.lastIndex
    for (; i < css.length && depth > 0; i++) {
      if (css[i] === '{') depth++
      else if (css[i] === '}') depth--
    }
    out.push({ query: match[0], body: css.slice(re.lastIndex, i - 1) })
  }
  return out
}

/** A query or a selector compared as CSS compares it: whitespace is not part of either. */
const squash = (text) => text.replace(/\s+/g, '')

/** Every selector in a stylesheet that styles a hover state of the menu. */
function menuHoverSelectors(css) {
  return [...css.matchAll(/([^{}@]*:hover[^{}]*)\{/g)]
    .map((match) => match[1].trim())
    .filter((selector) => selector.includes('.site-nav'))
}

// ---- a browser, small enough to read -----------------------------------------------------

/** The event object `site.js` constructs (`new CustomEvent(type, { detail })`). */
class StubEvent {
  constructor(type, init = {}) {
    this.type = type
    this.detail = init.detail
  }
}

/**
 * A built page as a document `site.js` can run in, with the browser behaviour the menu depends
 * on: `mouseenter`/`mouseleave` over a subtree, the `toggle` event a `<details>` fires when its
 * `open` attribute changes, `focusin`, and a `<summary>`'s activation behaviour.
 *
 * `hover` is the device's answer to the pointer query — a pointer that hovers, or a touch
 * screen's `false`. `matchMedia: false` is a browser old enough to have none. `wide` is the
 * viewport the layout is drawn at: at or above the 48rem breakpoint the drawer is not drawn and
 * the panel is; below it the panel is drawn only while the drawer is open.
 */
function stubPage(html, { hover = true, matchMedia = true, wide = true } = {}) {
  const root = parse(html)
  const wrapped = new Map()
  const timers = []
  const queries = []
  const documentListeners = new Map()
  const windowListeners = new Map()

  const dom = {
    wrap(node) {
      if (!wrapped.has(node)) wrapped.set(node, new Element(node))
      return wrapped.get(node)
    },
    // Compound selectors joined by descendant spaces, in a comma list: `tag`, `.class`,
    // `[attr]` — the selectors `site.js` writes, and a refusal for anything else.
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
    // Whether the element is rendered at all. A closed `<details>` hides its own children, the
    // drawer's panel is drawn only while the drawer is open, and the drawer itself is taken away
    // at the breakpoint — the three rules of `base.css`'s nav that decide what a pointer can
    // reach.
    drawn(node) {
      for (let at = node; at && at.name !== '#document'; at = at.parent) {
        if (at.attrs.has('hidden')) return false
        const cls = (at.attrs.get('class') ?? '').split(/\s+/)
        if (wide && cls.includes('site-nav-drawer')) return false
        if (!wide && cls.includes('site-nav-panel') && !(siblingDrawer(at)?.attrs.has('open') ?? false)) return false
        if (at.parent?.name === 'details' && at.name !== 'summary' && !at.parent.attrs.has('open')) return false
      }
      return true
    },
  }

  function siblingDrawer(panel) {
    const siblings = panel.parent.children.filter((child) => child.name !== undefined)
    return siblings[siblings.indexOf(panel) - 1] ?? null
  }

  function compound(node, part) {
    if (!/^[a-z]*(\.[\w-]+|\[[\w-]+\])*$/.test(part)) throw new Error(`the stub DOM does not understand the selector part ${part}`)
    const tag = /^[a-z]+/.exec(part)?.[0]
    if (tag && node.name !== tag) return false
    const cls = (node.attrs.get('class') ?? '').split(/\s+/)
    for (const [, c] of part.matchAll(/\.([\w-]+)/g)) if (!cls.includes(c)) return false
    for (const [, a] of part.matchAll(/\[([\w-]+)\]/g)) if (!node.attrs.has(a)) return false
    return true
  }

  /** The document's own listeners, which is where a key press and a click outside arrive. */
  function runDocument(type, event, capture) {
    for (const listener of documentListeners.get(type) ?? []) if (listener.capture === capture) listener.handler(event)
  }

  /** One parsed element, with the DOM surface `site.js` touches. */
  class Element {
    constructor(node) {
      this.node = node
      this.listeners = new Map()
    }
    get name() {
      return this.node.name
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
    get classList() {
      const classes = (this.attrs.get('class') ?? '').split(/\s+/)
      return { contains: (name) => classes.includes(name) }
    }
    get parentElement() {
      const parent = this.node.parent
      return parent && parent.name !== '#document' ? dom.wrap(parent) : null
    }
    get nextElementSibling() {
      const siblings = this.node.parent.children.filter((child) => child.name !== undefined)
      const next = siblings[siblings.indexOf(this.node) + 1]
      return next ? dom.wrap(next) : null
    }
    contains(other) {
      for (let node = other?.node; node; node = node.parent) if (node === this.node) return true
      return false
    }
    matches(selector) {
      return dom.matches(this.node, selector)
    }
    closest(selector) {
      for (let node = this.node; node && node.name !== '#document'; node = node.parent) {
        if (dom.matches(node, selector)) return dom.wrap(node)
      }
      return null
    }
    querySelector(selector) {
      return this.querySelectorAll(selector)[0] ?? null
    }
    querySelectorAll(selector) {
      return [...walk(this.node)].filter((node) => dom.matches(node, selector)).map((node) => dom.wrap(node))
    }
    // The state the menu is in, as the attribute every level of it agrees on. A browser fires
    // `toggle` at the element when this attribute changes — that is what the page's
    // `aria-expanded` listener reads — and the stub does the same, in the same breath.
    get open() {
      return this.attrs.has('open')
    }
    set open(value) {
      if (Boolean(value) === this.open) return
      if (value) this.attrs.set('open', '')
      else this.attrs.delete('open')
      this.dispatch('toggle', { target: this })
    }
    getClientRects() {
      return dom.drawn(this.node) ? [{}] : []
    }
    // Focus, as a browser does it: an element that is not rendered cannot take the focus at
    // all, and one that takes it fires `focusin` — the event that bubbles, which is the one the
    // page listens for.
    focus() {
      if (!dom.drawn(this.node)) return
      const previous = document.activeElement
      if (previous === this) return
      document.activeElement = this
      if (previous) previous.dispatch('focusout', { target: previous })
      this.dispatch('focusin', { target: this, bubbles: true })
    }
    addEventListener(type, handler, capture = false) {
      if (!this.listeners.has(type)) this.listeners.set(type, [])
      this.listeners.get(type).push({ handler, capture })
    }
    /**
     * Dispatch an event at this element: the capture listeners from the document down, then the
     * element's own, then — for an event that bubbles — the ancestors on the way back up. A
     * `toggle` does not bubble and a `focusin` does; both still run the capture listeners on
     * the way down, which is how the page hears a group change its state.
     */
    dispatch(type, init = {}) {
      const event = {
        type,
        defaultPrevented: false,
        preventDefault() {
          this.defaultPrevented = true
        },
        ...init,
      }
      if (event.target === undefined) event.target = this
      const ancestors = []
      for (let node = this.node.parent; node && node.name !== '#document'; node = node.parent) ancestors.unshift(dom.wrap(node))
      runDocument(type, event, true)
      for (const ancestor of ancestors) ancestor.run(type, event, true)
      this.run(type, event, false)
      if (event.bubbles) {
        for (const ancestor of [...ancestors].reverse()) ancestor.run(type, event, false)
        runDocument(type, event, false)
      }
      return event
    }
    run(type, event, capture) {
      for (const listener of this.listeners.get(type) ?? []) if (listener.capture === capture) listener.handler.call(this, event)
    }
  }

  const document = {
    readyState: 'complete',
    activeElement: null,
    createElement: (name) => ({ name, src: '', async: false }),
    head: { appendChild: () => {} },
    querySelectorAll: (selector) => [...walk(root)].filter((node) => dom.matches(node, selector)).map((node) => dom.wrap(node)),
    querySelector(selector) {
      return this.querySelectorAll(selector)[0] ?? null
    },
    getElementById(id) {
      return this.querySelectorAll(`[id]`).find((el) => el.getAttribute('id') === id) ?? null
    },
    addEventListener(type, handler, capture = false) {
      if (!documentListeners.has(type)) documentListeners.set(type, [])
      documentListeners.get(type).push({ handler, capture })
    },
    /** Dispatch at the document, as a key press or a click whose target is `target` arrives. */
    dispatch(type, init = {}) {
      const event = {
        type,
        defaultPrevented: false,
        preventDefault() {
          this.defaultPrevented = true
        },
        ...init,
      }
      for (const listener of documentListeners.get(type) ?? []) if (!listener.capture) listener.handler(event)
      return event
    },
  }
  dom.document = document

  const window = {
    setTimeout(handler, ms) {
      const timer = { handler, ms, cancelled: false }
      timers.push(timer)
      return timer
    },
    clearTimeout(timer) {
      if (timer) timer.cancelled = true
    },
    localStorage: { getItem: () => null, setItem: () => {}, removeItem: () => {} },
    addEventListener(type, handler) {
      if (!windowListeners.has(type)) windowListeners.set(type, [])
      windowListeners.get(type).push(handler)
    },
    dispatchEvent() {
      return true
    },
  }
  if (matchMedia) {
    window.matchMedia = (query) => {
      queries.push(query)
      return { media: query, matches: hover && isPointerQuery(query) }
    }
  }

  vm.runInNewContext(SCRIPT_SOURCE, { window, document, navigator: {}, CustomEvent: StubEvent })

  // The pointer, and where it is: what `moveTo` needs to know which events a move fires.
  let pointer = null
  const within = (element, of) => {
    for (let node = element; node; node = node.parentElement) if (node === of) return true
    return false
  }

  const groups = document.querySelectorAll('.site-nav-group details')
  const summaries = groups.map((group) => group.querySelector('summary'))

  return {
    document,
    groups,
    summaries,
    timers,
    queries,
    find: (selector) => document.querySelectorAll(selector),
    /** Focus an element, as Tab or a click does. */
    focus: (element) => element.focus(),
    /**
     * Move the pointer to an element, as a browser does: `mouseleave` on every element the
     * pointer left — the ancestors of where it was that are not ancestors of where it went —
     * and `mouseenter` on every element it arrived at that it was not inside. A pointer that
     * travels from a group's summary into its list is inside the same `<details>` the whole way,
     * so neither fires: that is what keeps a submenu open under a reader on their way to a link.
     */
    moveTo(target) {
      const was = pointer
      pointer = target
      for (let node = was; node; node = node.parentElement) if (!target || !within(target, node)) node.dispatch('mouseleave')
      const arrived = []
      for (let node = target; node; node = node.parentElement) if (!was || !within(was, node)) arrived.unshift(node)
      for (const node of arrived) node.dispatch('mouseenter')
    },
    /**
     * Click an element, as a browser does: the event, then the element's own activation
     * behaviour. For a `<summary>` that is the disclosure the menu is built on — the click
     * toggles the `<details>` it belongs to — and a page that cancelled the click would not get
     * it, which is the defect the case below is written to catch.
     */
    click(element) {
      const event = element.dispatch('click', { bubbles: true, target: element })
      const group = element.name === 'summary' ? element.parentElement : null
      if (group && group.name === 'details' && !event.defaultPrevented) group.open = !group.open
      return event
    },
    /** Press a key at the focused element, which is where a keyboard's events arrive. */
    key(key) {
      const target = document.activeElement ?? document.body
      return document.dispatch('keydown', { key, target })
    },
    /** Fire the timers the page has pending, in order, and forget them. */
    runTimers() {
      for (const timer of timers.filter((t) => !t.cancelled)) {
        timer.cancelled = true
        timer.handler()
      }
    },
    /** What the reader sees: each group, and what it says about itself. */
    state() {
      return groups.map((group, i) => ({
        label: textOf(group.querySelector('summary')).trim(),
        open: group.open,
        expanded: summaries[i].getAttribute('aria-expanded'),
      }))
    },
  }
}

/** Whether a media query asks about the pointer rather than about the viewport's width. */
function isPointerQuery(query) {
  return /hover:\s*hover/.test(query) && /pointer:\s*fine/.test(query)
}

// ---- 1. the query the behaviour is decided by --------------------------------------------

test('the menu’s hover states are scoped to a pointer that hovers, and to nothing else', () => {
  const css = pageCss(HOME.html)
  const pointer = mediaBlocks(css).filter((block) => squash(block.query) === squash(`@media ${POINTER_QUERY}{`))
  assert.equal(
    pointer.length,
    1,
    `the stylesheet carries ${pointer.length} blocks at \`@media ${POINTER_QUERY}\`: the menu's hover states are decided by the pointer's capability, and one query is where that is written`,
  )

  // Every hover state the menu has lives in that block — the trigger, the links in the list and
  // the drawer's toggle — so no width query, and no rule outside any query at all, can give a
  // touch screen a hover mark. This is the assertion that refuses the width-based shortcut:
  // a rule moved into the 48rem block, or written at the top level, is a rule this file finds.
  const all = menuHoverSelectors(css)
  assert.ok(all.length > 0, 'the menu has no hover states at all, so there is nothing for the pointer to open')
  const inPointer = menuHoverSelectors(pointer[0].body).map(squash)
  for (const selector of all) {
    assert.ok(
      inPointer.includes(squash(selector)),
      `${selector} is a menu hover state outside \`@media ${POINTER_QUERY}\`: on a touch screen a hover that sticks after a tap is a mark on a group the reader has not opened, and a width query is the wrong question — a tablet is wide and cannot hover`,
    )
  }

  // And the query the stylesheet scopes those states to is a pointer's, not a viewport's.
  assert.ok(isPointerQuery(pointer[0].query), `the menu's hover states are scoped to ${pointer[0].query}, which is not a question about the pointer`)
  for (const block of mediaBlocks(css)) {
    if (/width/.test(block.query)) {
      assert.equal(
        menuHoverSelectors(block.body).length,
        0,
        `${block.query} carries a menu hover state: the submenu opens on a pointer that hovers, never on a viewport that is wide enough`,
      )
    }
  }
})

// ---- 2. what the pointer does ------------------------------------------------------------

test('the pointer opens a group when it arrives, and moving into the list keeps it open', () => {
  const page = stubPage(HOME.html)
  const [product, reference] = page.groups
  assert.equal(product.open, false, 'the markup opens a group before the reader has done anything')

  page.moveTo(product.querySelector('summary'))
  assert.equal(product.open, true, 'hovering a top-level item did not open its submenu')

  // Onto a link in the list: the pointer is still inside the same `<details>`, so the submenu
  // cannot close under a reader who is on their way to it.
  page.moveTo(product.querySelector('a'))
  assert.equal(product.open, true, 'the submenu closed while the pointer was inside it')
  page.runTimers()
  assert.equal(product.open, true, 'a pending close fired while the pointer was inside the submenu')

  // The group the pointer never went near is untouched.
  assert.equal(reference.open, false, 'hovering one group opened another')

  // And leaving the whole menu closes what the pointer left, after the delay and not before.
  page.moveTo(page.find('main')[0])
  assert.equal(product.open, true, 'the submenu collapsed the instant the pointer left the group')
  page.runTimers()
  assert.equal(product.open, false, 'the submenu stayed open after the pointer had left it')
})

test('a submenu closes a moment after the pointer leaves, and a pointer that comes back keeps it', () => {
  const page = stubPage(HOME.html)
  const group = page.groups[0]
  const summary = group.querySelector('summary')

  page.moveTo(summary)
  page.moveTo(page.find('main')[0])
  const pending = page.timers.filter((timer) => !timer.cancelled)
  assert.equal(pending.length, 1, `the pointer left a group with ${pending.length} closes scheduled: it owes exactly one`)
  assert.ok(
    pending[0].ms >= 100 && pending[0].ms <= 500,
    `the close is scheduled ${pending[0].ms} ms after the pointer leaves: a menu that closes at once cannot be entered — the cursor crosses the seam between the summary and the list — and one that waits half a second hangs over the page`,
  )

  // Back inside the delay, which is the crossing the delay is for: nothing closes.
  page.moveTo(summary)
  page.runTimers()
  assert.equal(group.open, true, 'the close the pointer had already cancelled fired anyway')

  // And a pointer that leaves for good closes it.
  page.moveTo(page.find('main')[0])
  page.runTimers()
  assert.equal(group.open, false, 'a submenu the pointer has left stayed open')

  // A close that lands while the focus is on a link inside the list hands that focus back to
  // the trigger rather than leaving it on an element the browser has stopped drawing — the
  // same courtesy Escape pays, and the reason the focus handed back does not reopen the group.
  const link = group.querySelector('a')
  page.moveTo(summary)
  page.focus(link)
  assert.ok(page.document.activeElement === link, 'the stub would not focus a link inside an open submenu')
  page.moveTo(page.find('main')[0])
  page.runTimers()
  assert.equal(group.open, false, 'the submenu the pointer left stayed open under a focused link')
  assert.ok(
    page.document.activeElement === summary,
    'the close left the focus on a link the browser no longer draws: it belongs back on the trigger',
  )
  assert.equal(group.open, false, 'the focus handed back to the trigger reopened the submenu the pointer had closed')
})

test('the click the menu was built on still toggles a group', () => {
  // On a touch screen, where the click is the whole menu.
  const touch = stubPage(HOME.html, { hover: false })
  const group = touch.groups[0]
  touch.click(group.querySelector('summary'))
  assert.equal(group.open, true, 'a tap no longer opens a submenu')
  touch.click(group.querySelector('summary'))
  assert.equal(group.open, false, 'a tap no longer closes a submenu')

  // And on a hovering pointer, where the browser delivers the pointer's arrival before the
  // click: the pointer opens the group and the click — the `<summary>`'s own toggle, which this
  // file does not touch — closes what is open.
  const page = stubPage(HOME.html)
  const hovering = page.groups[0]
  const summary = hovering.querySelector('summary')
  page.moveTo(summary)
  assert.equal(hovering.open, true, 'the pointer did not open the group the click then acts on')
  page.click(summary)
  assert.equal(hovering.open, false, 'the click did not toggle the group the pointer had opened')
  page.click(summary)
  assert.equal(hovering.open, true, 'a second click did not reopen the group')
})

test('the drawer opens by its own click and not by the pointer: the mobile menu is unchanged', () => {
  const page = stubPage(HOME.html, { wide: false })
  const drawer = page.find('.site-nav-drawer')[0]
  page.moveTo(drawer.querySelector('summary'))
  assert.equal(drawer.open, false, 'the pointer opened the drawer: the narrow header keeps the toggle it has always had')

  const group = page.groups[0]
  drawer.open = true
  page.moveTo(group.querySelector('summary'))
  assert.equal(group.open, true, 'inside an open drawer, the pointer no longer opens a group')
})

// ---- 3. the keyboard ---------------------------------------------------------------------

test('the keyboard opens a group, Escape closes it and gives the focus back, and Tab is left alone', () => {
  const page = stubPage(HOME.html)
  const group = page.groups[0]
  const summary = group.querySelector('summary')

  // Tab reaches the trigger, and the submenu opens under it: without that, the links inside a
  // closed `<details>` are not reachable by Tab at all, and the menu has a level a keyboard
  // cannot enter.
  page.focus(summary)
  assert.equal(group.open, true, 'focusing a group did not open its submenu, so Tab cannot reach the links inside it')

  // Nothing here traps the keyboard: a Tab is the browser's to handle, and the page says so.
  const tab = page.key('Tab')
  assert.equal(tab.defaultPrevented, false, 'a Tab key press was cancelled: the keyboard is trapped in the menu')
  assert.equal(group.open, true, 'a key other than Escape closed the menu')

  // Escape closes it and hands the focus back to the trigger — which does not reopen it.
  page.focus(group.querySelector('a'))
  page.key('Escape')
  assert.equal(group.open, false, 'Escape left the submenu open')
  assert.ok(
    page.document.activeElement === summary,
    'Escape did not give the focus back to the trigger: a focus left on a link that is no longer drawn is a focus the reader has lost',
  )
  assert.equal(group.open, false, 'the focus handed back to the trigger reopened the submenu Escape had just closed')

  // And arriving at the trigger again opens it, as arriving at it the first time did — the
  // guard against reopening was for the focus the close handed back, not for the reader.
  page.focus(page.groups[1].querySelector('summary'))
  page.focus(summary)
  assert.equal(group.open, true, 'the guard against reopening outlived the focus it was written for')
})

test('every way a submenu opens and closes says so in aria-expanded', () => {
  const page = stubPage(HOME.html)
  const group = page.groups[0]
  const summary = group.querySelector('summary')

  // Nothing ships an expanded state: a `<summary>` exposes its parent's by itself, and an
  // attribute written into the markup would be a second copy of it — one that no script can
  // correct for a reader whose browser runs none.
  assert.equal(summary.getAttribute('aria-expanded'), null, 'the markup carries an expanded state the script has not set')

  page.moveTo(summary)
  assert.equal(summary.getAttribute('aria-expanded'), 'true', 'the pointer opened a submenu and the control did not say so')
  page.moveTo(page.find('main')[0])
  page.runTimers()
  assert.equal(summary.getAttribute('aria-expanded'), 'false', 'the submenu closed and the control still said it was open')

  page.click(summary)
  assert.equal(summary.getAttribute('aria-expanded'), 'true', 'a click opened a submenu and the control did not say so')

  page.key('Escape')
  assert.equal(summary.getAttribute('aria-expanded'), 'false', 'Escape closed a submenu and the control still said it was open')

  // The touch path says the same thing, by the one gesture it has.
  const touch = stubPage(HOME.html, { hover: false })
  touch.click(touch.summaries[0])
  assert.equal(touch.summaries[0].getAttribute('aria-expanded'), 'true', 'a tap opened a submenu and the control did not say so')
  touch.click(touch.summaries[0])
  assert.equal(touch.summaries[0].getAttribute('aria-expanded'), 'false', 'a tap closed a submenu and the control still said it was open')
})

// ---- 4. the touch screen -----------------------------------------------------------------

test('a touch screen gets no hover behaviour at all: the tap is the whole menu', () => {
  const page = stubPage(HOME.html, { hover: false })
  const group = page.groups[0]
  const summary = group.querySelector('summary')

  page.moveTo(summary)
  assert.equal(group.open, false, 'a touch screen opened a submenu on hover, which turns the tap that used to open it into a close')
  assert.equal(page.timers.length, 0, 'the touch path scheduled a close it can never need')

  page.moveTo(page.find('main')[0])
  assert.equal(group.open, false, 'a touch screen closed a submenu on hover')
  assert.equal(page.timers.length, 0, 'the touch path scheduled a close on the way out')

  page.click(summary)
  assert.equal(group.open, true, 'the tap that has always opened a submenu no longer does')
  page.click(summary)
  assert.equal(group.open, false, 'the tap that has always closed a submenu no longer does')
})

test('a browser with no matchMedia to ask keeps the click, and no width is ever the question', () => {
  // A browser that cannot answer has no hover to be certain of, and a menu whose behaviour is
  // guessed at is worse than one that keeps the click.
  const ancient = stubPage(HOME.html, { matchMedia: false })
  ancient.moveTo(ancient.summaries[0])
  assert.equal(ancient.groups[0].open, false, 'a browser with no matchMedia got hover behaviour anyway')
  ancient.click(ancient.summaries[0])
  assert.equal(ancient.groups[0].open, true, 'a browser with no matchMedia lost the click as well')

  // And what the script asks is the pointer's capability: one query, and not a width.
  const page = stubPage(HOME.html)
  assert.deepEqual(page.queries, [POINTER_QUERY], `the script asked ${JSON.stringify(page.queries)}: the submenu opens on the pointer that hovers, and the one query that says so is asked once`)
  for (const query of page.queries) {
    assert.ok(!/width/.test(query), `the script asked ${query}: screen width is not the question — a tablet is wide and cannot hover, and a phone with a mouse attached can`)
  }
})
