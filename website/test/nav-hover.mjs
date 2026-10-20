// website/test/nav-hover.mjs — the menu opens on hover, on a pointer that hovers (the owner's
// brief of 2026-10-18, "Fugaro Website Changes Spec", item 1).
//
// The defect this file exists for: the top menu's three groups opened only on a click, which on
// a desktop is the one gesture a pointer should not have to make. The change the owner agreed:
// on a device whose pointer hovers, a group's submenu opens when the pointer arrives at the
// group and closes a moment after the pointer leaves it; a click still toggles, the keyboard
// still reaches every link, and a touch screen keeps the tap it has always had.
//
// Four things this file holds, and each of them is a rule rather than a preference:
//
//   1. **The decision is the pointer's capability, never the viewport's width.** The query is
//      `(hover: hover) and (pointer: fine)`: a tablet 1280 px wide has no hover to give, and a
//      phone with a mouse attached has one. The stylesheet scopes the menu's hover states to
//      that query and `public/scripts/site.js` asks the same one with `matchMedia`; this file
//      holds both to it, and refuses a width query in either.
//   2. **The states the script drives, driven.** The submenu opens on the pointer's arrival and
//      on the keyboard's focus, closes `CLOSE_AFTER_MS` after the pointer leaves and not before,
//      survives a pointer that crosses the seam between the trigger and the list, is left alone
//      by the click the `<summary>` handles by itself, says what it is in `aria-expanded`, and
//      hands the focus back whenever a close would strand it — the pointer's own close, Escape,
//      and the browser's close of a group whose sibling in the same `name` set opened — and
//      hands nothing back for a close the **reader** asked for from somewhere else: a press on
//      the page's own text moves the focus to the body without a `focusin` to hear, and the focus
//      stays where they put it rather than on the trigger of the menu they just dismissed.
//      **The focus a click or a tap moves is not the keyboard's, and opens nothing.** The press
//      moves the focus to the trigger on the way to the click, so a page that opened the group on
//      that focus would have the click close it again: a first tap that does nothing, and a click
//      on a closed group whose trigger did not hold the focus that does nothing either.
//      **And the keyboard's way in is not the pointer's to give.** It is bound whatever the
//      device answers, because the keyboard is the device's and not the pointer's: a phone or a
//      tablet with a keyboard attached answers the hover query no, and the menu still has a
//      second level Tab must be able to enter. The guard that keeps a close from reopening the
//      group it handed the focus back to lasts exactly the focus it was written for, so a reader
//      who takes the focus out of the menu and comes back to the same trigger opens it again.
//   3. **A finger never hovers, whatever the device says.** The query answers for the device's
//      *primary* pointer, and a touch laptop answers it yes; a tap on one of those still sends a
//      `pointerenter` of its own, carrying `pointerType: 'touch'`, and a tap that opened a group
//      on the way in and then closed it again with its own click would be a submenu no finger
//      could open. So the events decide and not the query alone: a mouse and a pen hover, a
//      finger does not, and a device that answered no has no hover listeners at all — the tap is
//      the whole menu there, and no close is ever scheduled.
//   4. **The answer is watched, not sampled once.** A pointer can arrive after the page did — a
//      keyboard docked to a tablet brings a trackpad, a mouse is plugged into a phone — and one
//      taken away leaves a device with no hover to give. The query's `change` is subscribed to,
//      and each answer binds or unbinds the hover behaviour, without a reload.
//
// The rules above are also one function over the script source (`menuFailures`) and one over the
// stylesheet (`hoverScopeFailures`), which is what lets the file's refusals be kept the way the
// other gates keep theirs: a case plants each defect in the shipped script and in the built
// stylesheet, one at a time, and reads the sentence for that defect back. A gate whose refusals
// have never been seen to fire is a gate nobody knows the shape of.
//
// **There is no browser here.** The page is the built artifact parsed by `lib/html.mjs`, wrapped
// in the DOM calls `site.js` makes plus the browser behaviour the menu depends on — `pointerenter`
// and `pointerleave` over a subtree, each carrying the pointer type that caused it, the `toggle`
// event a `<details>` fires when its `open` attribute changes, `focusin`, the focus a pointer's
// press moves — onto the control it presses where that can take one, and onto nothing where it
// cannot, which is a page's own text — the exclusive set a shared `name` makes (one group
// opening closes its siblings, which drops the focus a closed `<details>` was holding), the
// `change` a `MediaQueryList` reports when the device's answer changes, and a `<summary>`'s own
// activation behaviour (a click toggles its group). Those are the browser's, not the site's, and
// the stub states each one where it stands. What a real pointer does on a real screen is the
// owner's live check: the brief's acceptance line — "hovering a top-level item on desktop opens
// its submenu immediately" — is a rendered gesture, and **Check 70a's step 6 in
// `docs/gcp-live-checklist.md` is where the owner runs it** (the register, not this file: no step
// of that check is run by an agent).
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
 * on: `pointerenter`/`pointerleave` over a subtree, each carrying the pointer type that caused
 * it, the `toggle` event a `<details>` fires when its `open` attribute changes, `focusin`, the
 * focus a press moves — onto what was pressed when that can take one, and onto nothing at all
 * when it cannot — the exclusive set a shared `name` makes, a `<summary>`'s activation
 * behaviour, and the `change` event a `MediaQueryList` fires when the device's answer to the
 * pointer query moves.
 *
 * `hover` is the device's answer to the pointer query — a pointer that hovers, or a touch
 * screen's `false`; `setPointerHover` changes it under a page that has already loaded.
 * `matchMedia: false` is a browser old enough to have none. `script` is the source run in the
 * page, which is the file the site ships unless the refusals below hand in a defect planted in
 * it.
 *
 * The viewport's width is not a parameter: since the owner's brief of 2026-10-19 (item 1) the
 * menu is the same three `<details>` groups at every width — the three labels in the header's
 * row — and the one thing a width decided, whether a panel is drawn behind a toggle, is gone
 * with the toggle.
 */
function stubPage(html, { hover = true, matchMedia = true, script = SCRIPT_SOURCE } = {}) {
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
    // Whether the element is rendered at all. One rule decides it, and it is the element's own:
    // a closed `<details>` hides what it holds. Nothing in `base.css` hides a group's list at any
    // width — the list is the `<details>`'s own child, and the disclosure is the whole of the
    // mechanism.
    drawn(node) {
      for (let at = node; at && at.name !== '#document'; at = at.parent) {
        if (at.attrs.has('hidden')) return false
        if (at.parent?.name === 'details' && at.name !== 'summary' && !at.parent.attrs.has('open')) return false
      }
      return true
    },
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
    // `aria-expanded` listener reads — and the stub does the same, in the same breath. That is
    // a divergence, and a deliberate one: a browser queues the event as a task, and the stub
    // firing it here is what lets a case see the state a close leaves behind — the page's own
    // listeners run with the attribute already moved, as a queued event would find it. Nothing
    // the page does may depend on which of the two it is given: the guard against reopening is
    // cleared by the next focus to arrive anywhere else, so it expires the same way under either
    // model — which is what the case holding that guard to its focus is written against.
    get open() {
      return this.attrs.has('open')
    }
    set open(value) {
      if (Boolean(value) === this.open) return
      if (value) this.attrs.set('open', '')
      else {
        this.attrs.delete('open')
        // A closed `<details>` stops drawing its own content in the same breath, and a focus
        // standing in that content — on a link, say — cannot stay where it is: a browser drops it
        // (to `<body>`) before the `toggle` below, which is the only event the page hears about
        // this close. The stub drops it here for the same reason, which leaves the page no way to
        // know where the focus was but to have remembered it as it arrived.
        const active = document.activeElement
        if (active && this.contains(active) && !dom.drawn(active.node)) document.activeElement = null
      }
      this.dispatch('toggle', { target: this })
      // A `<details>` carrying a `name` is one of an exclusive set: a browser closes every sibling
      // that carries the same name when one of them opens. No script asked for that close and the
      // page still has to answer for it — the group that closed may have been holding the
      // reader's focus — so the stub models it, the siblings' own `toggle` events and all.
      if (value) {
        const name = this.attrs.get('name')
        if (name) {
          for (const other of document.querySelectorAll('details[name]')) {
            if (other !== this && other.getAttribute('name') === name) other.open = false
          }
        }
      }
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
    removeEventListener(type, handler) {
      const bound = this.listeners.get(type) ?? []
      const at = bound.findIndex((entry) => entry.handler === handler)
      if (at >= 0) bound.splice(at, 1)
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

  /**
   * The element a browser would move the focus to if this one were pressed: the pressed element
   * itself when it can take a mouse's focus — a link with an `href`, a `<summary>`, a button, a
   * form control, anything with a `tabindex` — and otherwise the nearest ancestor that can. A
   * paragraph of the page's own text has neither, which is the case that matters below.
   */
  function pressTarget(node) {
    for (let at = node; at && at.name !== '#document'; at = at.parent) {
      if (at.attrs.has('tabindex')) return at
      if (at.name === 'a' && at.attrs.has('href')) return at
      if (['summary', 'button', 'input', 'select', 'textarea'].includes(at.name)) return at
    }
    return null
  }

  /**
   * A pointer's press and lift on an element, and the focus the press moves. The focus is not
   * decoration: a browser moves it to the control on the way to the click, so the page's `focusin`
   * runs *before* the click does. A stub that skipped it would let a page that opens a group on
   * `focusin` pass a case in which the click that follows closes the group again — which is the
   * defect the cases below are written to catch, and one only a real press can show.
   *
   * And a press only moves the focus where the browser would: onto what can take one, or, where
   * nothing pressed can — a paragraph, a heading, the page's own text — onto nothing at all. That
   * second half is not a detail: the element that had the focus is blurred and no `focusin` is
   * fired anywhere, so a page that remembered where the focus was has nothing to correct it with
   * and the memory goes stale exactly where a close may follow.
   */
  function press(element, type) {
    element.dispatch('pointerdown', { pointerType: type, bubbles: true, target: element })
    const target = pressTarget(element.node)
    if (target) dom.wrap(target).focus()
    else {
      const previous = document.activeElement
      if (previous) {
        document.activeElement = null
        previous.dispatch('focusout', { target: previous })
      }
    }
    element.dispatch('pointerup', { pointerType: type, bubbles: true, target: element })
  }

  /**
   * The click event, and then the element's own activation behaviour: a `<summary>` toggles its
   * group, and a page that cancelled the click does not get it.
   */
  function activate(element) {
    const event = element.dispatch('click', { bubbles: true, target: element })
    const group = element.name === 'summary' ? element.parentElement : null
    if (group && group.name === 'details' && !event.defaultPrevented) group.open = !group.open
    return event
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
    removeEventListener(type, handler) {
      const bound = documentListeners.get(type) ?? []
      const at = bound.findIndex((entry) => entry.handler === handler)
      if (at >= 0) bound.splice(at, 1)
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
  // The device's answer to the pointer query, and the lists the page asked for it on. The answer
  // is not fixed: `setPointerHover` below is a mouse plugged in or taken away, and a browser
  // reports that to every subscribed list as a `change` carrying the new answer.
  let pointerAnswer = hover
  const mediaLists = []
  if (matchMedia) {
    window.matchMedia = (query) => {
      queries.push(query)
      const listeners = []
      const list = {
        media: query,
        get matches() {
          return pointerAnswer && isPointerQuery(query)
        },
        addEventListener(type, handler) {
          if (type === 'change') listeners.push(handler)
        },
        // The name a MediaQueryList had for the same subscription before `addEventListener`
        // reached it. A browser old enough to need this one still reports the change.
        addListener(handler) {
          listeners.push(handler)
        },
      }
      mediaLists.push({ list, listeners })
      return list
    }
  }

  vm.runInNewContext(script, { window, document, navigator: {}, CustomEvent: StubEvent })

  // Where each kind of pointer is: a mouse stays where it was left while a finger taps
  // somewhere else, so each type has a position of its own and a move fires only the enter and
  // leave events of the pointer that moved.
  const pointers = new Map()
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
    /**
     * Focus an element as the keyboard does: the key press that moves the focus, and then the
     * focus. Both halves matter — the page tells a keyboard's focus from a pointer's by the press
     * that brought it, so a focus with no key press before it is the pointer's and opens nothing.
     * A click is `click` below, which brings its own press and its own focus.
     */
    focus(element) {
      document.dispatch('keydown', { key: 'Tab', target: document.activeElement })
      return element.focus()
    },
    /**
     * Move the pointer to an element, as a browser does: `pointerleave` on every element the
     * pointer left — the ancestors of where it was that are not ancestors of where it went —
     * and `pointerenter` on every element it arrived at that it was not inside. A pointer that
     * travels from a group's summary into its list is inside the same `<details>` the whole way,
     * so neither fires: that is what keeps a submenu open under a reader on their way to a link.
     *
     * The type is the pointer's own — `'mouse'` unless a case says otherwise, `'pen'` for a
     * stylus, `'touch'` for a finger — and each event carries it, because the device's answer to
     * the query says nothing about which pointer is moving. A `target` of `null` is the pointer
     * leaving the page: every element it was inside is left.
     */
    moveTo(target, type = 'mouse') {
      const was = pointers.get(type) ?? null
      pointers.set(type, target)
      for (let node = was; node; node = node.parentElement) if (!target || !within(target, node)) node.dispatch('pointerleave', { pointerType: type })
      const arrived = []
      for (let node = target; node; node = node.parentElement) if (!was || !within(was, node)) arrived.unshift(node)
      for (const node of arrived) node.dispatch('pointerenter', { pointerType: type })
    },
    /**
     * A finger's tap, as a touch screen delivers it: the pointer's arrival carrying
     * `pointerType: 'touch'`, the press and the lift with the focus the press moves, the pointer
     * leaving as the finger is destroyed, and then the click the browser synthesises from all of
     * it — the one the `<summary>` acts on. A hover-capable touch screen sends every one of these
     * too, which is the case the script's pointer-type check exists for.
     */
    tap(element) {
      this.moveTo(element, 'touch')
      press(element, 'touch')
      this.moveTo(null, 'touch')
      return activate(element)
    },
    /**
     * Change the device's answer to the pointer query, as a browser does when a mouse is plugged
     * in or taken away: every list the page asked on reports the new answer to its `change`
     * listeners, which is the one subscription the script has for it.
     */
    setPointerHover(value) {
      pointerAnswer = value
      for (const { list, listeners } of mediaLists) {
        for (const handler of listeners) handler({ type: 'change', media: list.media, matches: list.matches })
      }
    },
    /**
     * Click an element, as a browser does: the pointer's press, which moves the focus to the
     * element, the lift, and then the click event with the element's own activation behaviour —
     * for a `<summary>` the disclosure the menu is built on, a click toggling its `<details>`.
     * The press is part of the gesture here because the page can tell a pointer's press from a
     * key press, and does: the focus a click brings is the pointer's, and one that opened a group
     * would be closed again by the click that brought it.
     */
    click(element) {
      press(element, 'mouse')
      return activate(element)
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

/**
 * Every way a stylesheet gives the menu a hover state a touch screen would keep, as the
 * sentences a reader of the failure gets. One function over one stylesheet, so the defect can be
 * planted in the built CSS and each refusal seen to fire (the refusals below).
 *
 * Three rules, and they are the stylesheet's half of the header's first point: the menu's hover
 * states live in exactly one `@media (hover: hover) and (pointer: fine)` block, that block is the
 * only place any of them lives, and no width query carries one — the width-based shortcut this
 * file exists to refuse.
 *
 * The block is found by comparing its query with the one this file names, whitespace aside, which
 * is a stronger statement than asking whether the query *looks* like a pointer's: `@media
 * (hover: hover) and (pointer: fine)` is the query, character for character, and a rule that
 * asked the weaker question would be a rule no defect could ever reach — the equality has already
 * answered it.
 */
function hoverScopeFailures(css) {
  const out = []
  const pointer = mediaBlocks(css).filter((block) => squash(block.query) === squash(`@media ${POINTER_QUERY}{`))
  if (pointer.length !== 1) {
    out.push(
      `the stylesheet carries ${pointer.length} blocks at \`@media ${POINTER_QUERY}\`: the menu's hover states are decided by the pointer's capability, and one query is where that is written`,
    )
    return out
  }

  // Every hover state the menu has lives in that block — the trigger, the links in the list and
  // the drawer's toggle — so no rule outside any query at all, and none in a width query, can give
  // a touch screen a hover mark. A rule moved into the 48rem block, or written at the top level,
  // is a rule this function finds.
  const all = menuHoverSelectors(css)
  if (all.length === 0) {
    out.push('the menu has no hover states at all, so there is nothing for the pointer to open')
    return out
  }
  const inPointer = menuHoverSelectors(pointer[0].body).map(squash)
  for (const selector of all) {
    if (!inPointer.includes(squash(selector))) {
      out.push(
        `${selector} is a menu hover state outside \`@media ${POINTER_QUERY}\`: on a touch screen a hover that sticks after a tap is a mark on a group the reader has not opened, and a width query is the wrong question — a tablet is wide and cannot hover`,
      )
    }
  }

  // And no width query carries one of them: the viewport's size is the wrong question, and it is
  // the one a stylesheet reaches for first.
  for (const block of mediaBlocks(css)) {
    if (/width/.test(block.query) && menuHoverSelectors(block.body).length > 0) {
      out.push(`${block.query} carries a menu hover state: the submenu opens on a pointer that hovers, never on a viewport that is wide enough`)
    }
  }

  return out
}

// ---- 1. the query the behaviour is decided by --------------------------------------------

test('the menu’s hover states are scoped to a pointer that hovers, and to nothing else', () => {
  const failures = hoverScopeFailures(pageCss(HOME.html))
  assert.deepEqual(failures, [], `the menu's hover states are not the ones the pointer decides:\n  ${failures.join('\n  ')}`)
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

})

test('a pointer that leaves a submenu the keyboard is in does not close it', () => {
  // The pointer is not the only reader a group has. A keyboard reader tabs to the trigger —
  // which opens the group — and on into the list; a pointer that crosses the group and leaves
  // while they are reading is asking to close a submenu somebody is still using, and the close
  // it asks for waits for that reader to go instead of landing under them.
  const page = stubPage(HOME.html)
  const group = page.groups[0]
  const summary = group.querySelector('summary')
  const link = group.querySelector('a')

  page.focus(summary)
  page.focus(link)
  assert.equal(group.open, true, 'the keyboard did not open the group it was reading')

  page.moveTo(summary)
  page.moveTo(page.find('main')[0])
  page.runTimers()
  assert.equal(group.open, true, 'a pointer leaving closed a submenu the keyboard was standing in')

  // The reader goes, and the close the pointer asked for lands then — a moment after the focus
  // has left rather than the instant it does, because the delay belongs to the pointer either
  // way, and a reader who comes back inside it is the reader the delay is for.
  const away = page.find('footer a')[0]
  assert.ok(away, 'the built homepage has nothing outside the menu for the focus to leave to')
  page.focus(away)
  assert.equal(group.open, true, 'the submenu closed the instant the focus left it, with no delay at all')
  page.runTimers()
  assert.equal(group.open, false, 'the submenu the reader had left stayed open')
})

test('a submenu the pointer has just left stays open when the keyboard arrives in the delay', () => {
  // The delay the pointer's close waits is a delay for a reader coming back, and a reader who
  // comes back by keyboard is the same reader: the close is cancelled and the group is held by
  // the focus instead — which is what Escape is then for.
  const page = stubPage(HOME.html)
  const group = page.groups[0]
  const summary = group.querySelector('summary')

  page.moveTo(summary)
  page.moveTo(page.find('main')[0])
  assert.equal(
    page.timers.filter((timer) => !timer.cancelled).length,
    1,
    'the pointer left without scheduling the close there is nothing to cancel',
  )

  page.focus(summary)
  page.runTimers()
  assert.equal(group.open, true, 'the close the pointer asked for fired under the keyboard reader who had arrived inside the delay')

  page.key('Escape')
  assert.equal(group.open, false, 'Escape left the submenu open')
  assert.ok(page.document.activeElement === summary, 'Escape did not leave the focus on the trigger it closed from')
})

test('the click the menu was built on still toggles a group', () => {
  // On a touch screen, where the click is the whole menu.
  const touch = stubPage(HOME.html, { hover: false })
  const group = touch.groups[0]
  touch.tap(group.querySelector('summary'))
  assert.equal(group.open, true, 'a tap no longer opens a submenu')
  touch.tap(group.querySelector('summary'))
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

test('a click opens a group whose trigger did not already hold the focus', () => {
  // The pointer is resting on Product — a click closed the group a moment ago, so no arrival
  // fires again — and the reader's focus is on another group's trigger. A click moves the focus
  // to the trigger it presses on the way to the click, and a group opened by that focus would be
  // closed again by the click that brought it: the press would do nothing at all.
  const page = stubPage(HOME.html)
  const [product, reference] = page.groups
  const summary = product.querySelector('summary')

  page.moveTo(summary)
  page.click(summary)
  assert.equal(product.open, false, 'the click did not close the group the pointer had opened')
  page.focus(reference.querySelector('summary'))
  assert.equal(reference.open, true, 'the focus arriving at a trigger did not open its group')
  assert.equal(product.open, false, 'the group the focus opened did not close its sibling in the same name set')

  page.click(summary)
  assert.equal(
    product.open,
    true,
    'a click on a closed group whose trigger did not hold the focus opened nothing: the focus the press moved opened the group and the click that followed closed it again',
  )
  assert.equal(reference.open, false, 'the click that opened a group did not close the sibling in its name set')
  assert.ok(page.document.activeElement === summary, 'the click did not move the focus to the trigger it pressed')
})

test('the three labels are the menu: nothing stands in front of them, and a tap opens one', () => {
  // The owner's brief of 2026-10-19 (item 1) took the single "Menu" toggle away. What is left
  // in the header's row is the three group labels themselves, so a section is one tap from the
  // first screen rather than two — and the markup that made the second tap necessary is gone
  // rather than merely unstyled.
  const page = stubPage(HOME.html, { hover: false })
  assert.equal(page.find('.site-nav-drawer').length, 0, 'the page still carries the drawer toggle the owner took away')
  assert.equal(page.find('.site-nav-panel').length, 0, 'the page still carries the panel the toggle opened')
  assert.equal(page.find('.site-nav-toggle').length, 0, 'the page still carries the drawer toggle’s summary')

  // Every label is drawn with nothing opened first, and each one holds its own panel of links.
  for (const summary of page.summaries) {
    assert.ok(summary.getClientRects().length, `the label ${JSON.stringify(textOf(summary.node).trim())} is not drawn until something else is opened`)
  }
  assert.equal(page.find('.site-nav-group ul').length, 3, 'the menu is not three labels each holding its own list')

  // A tap is the whole of the mechanism on a touch screen — a device that answers the pointer
  // query no, which is the phone the brief is about: the finger's arrival leaves the group
  // alone, and the click the `<summary>` handles by itself opens it.
  const group = page.groups[0]
  const summary = group.querySelector('summary')
  page.moveTo(summary)
  assert.equal(group.open, false, 'the pointer opened a group: a touch screen has no hover to give, and the click is the control')
  page.tap(summary)
  assert.equal(group.open, true, 'a tap on a label did not open its panel')
  assert.ok(
    [...group.querySelectorAll('a')].every((link) => link.getClientRects().length),
    'the links inside the panel a tap opened are not drawn, so the tap reached nothing',
  )
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

test('the guard against reopening lasts only for the focus the close handed back', () => {
  const page = stubPage(HOME.html)
  const group = page.groups[0]
  const summary = group.querySelector('summary')

  page.focus(summary)
  page.focus(group.querySelector('a'))
  page.key('Escape')
  assert.equal(group.open, false, 'Escape left the submenu open')
  assert.ok(page.document.activeElement === summary, 'Escape did not leave the focus on the trigger it closed from')

  // The reader leaves the menu altogether — the focus goes to the footer, outside every group,
  // where no group's own listener can hear it — and comes back to the same trigger. That
  // arrival is a reader's, and it opens the group: the guard was written for the focus the
  // close handed back, and it cannot outlive the reader walking away from it.
  const away = page.find('footer a')[0]
  assert.ok(away, 'the built homepage has nothing outside the menu for the focus to leave to')
  page.focus(away)
  page.focus(summary)
  assert.equal(
    group.open,
    true,
    'the guard against reopening outlived the focus it was written for: a reader who left the menu and came back cannot reach the links inside',
  )
})

test('the keyboard opens a group on a device with no hovering pointer', () => {
  // A phone or a tablet with a keyboard attached: the query answers no, so the hover behaviour
  // is never bound — and the menu still has a second level, which Tab has to be able to enter.
  // The keyboard is the device's, not the pointer's, so this is bound whatever the query says.
  const page = stubPage(HOME.html, { hover: false })
  const group = page.groups[0]
  page.focus(group.querySelector('summary'))
  assert.equal(
    group.open,
    true,
    'a device with no hovering pointer has a menu the keyboard cannot enter: the links inside a closed group are unreachable by Tab',
  )

  // And the same after a pointer is taken away mid-session, which unbinds the hover behaviour
  // and must not take the keyboard with it.
  const page2 = stubPage(HOME.html)
  page2.setPointerHover(false)
  const group2 = page2.groups[1]
  page2.focus(group2.querySelector('summary'))
  assert.equal(group2.open, true, 'taking the pointer away took the keyboard’s way into the menu with it')
})

test('a group a sibling closes hands the reader’s focus back to its own trigger', () => {
  // A keyboard reader is standing on a link inside Product when a pointer arrives at Reference:
  // no key was pressed and the reader moved nothing, but the exclusive set closes Product, the
  // browser stops drawing the list the focus is in, and the focus goes to `<body>`. Escape and
  // the pointer's own close both hand the focus back; this close is the browser's, and the page
  // hears only the `toggle` — so the focus has to have been remembered as it arrived, because by
  // then `document.activeElement` is no longer it.
  const page = stubPage(HOME.html)
  const [product, reference] = page.groups
  const summary = product.querySelector('summary')
  const link = product.querySelector('a')

  page.focus(summary)
  page.focus(link)
  assert.ok(page.document.activeElement === link, 'the stub would not focus a link inside an open group')

  page.moveTo(reference.querySelector('summary'))
  assert.equal(product.open, false, 'the sibling in the same name set did not close the open group')
  assert.equal(reference.open, true, 'the pointer did not open the group it arrived at')
  assert.ok(
    page.document.activeElement === summary,
    'a group closed under the reader’s focus left the focus where the browser dropped it: it belongs back on the trigger, as Escape and the pointer’s own close leave it',
  )
  assert.equal(product.open, false, 'the focus handed back to the trigger reopened the group the sibling had closed')
})

test('a click on the page closes the menu and leaves the focus where the reader put it', () => {
  // The other half of the rule above, and the one a close the reader asks for must not answer:
  // the reader has the focus on a link inside Product and presses the page's own text, which
  // cannot take a focus — so the browser moves the focus to the body and fires no `focusin`
  // anywhere, and the page's memory of where the focus was goes stale. The press closes the menu,
  // and the close must leave the focus where the reader put it rather than pulling it back to the
  // trigger of the menu they have just dismissed.
  const page = stubPage(HOME.html)
  const group = page.groups[0]
  const summary = group.querySelector('summary')

  page.focus(summary)
  page.focus(group.querySelector('a'))
  assert.ok(page.document.activeElement === group.querySelector('a'), 'the stub would not focus a link inside an open group')

  const text = page.find('main p')[0]
  assert.ok(text, 'the built homepage has no paragraph outside the menu, so there is nothing to press')
  page.click(text)
  assert.equal(group.open, false, 'a click on the page outside the menu did not close the open group')
  assert.ok(
    page.document.activeElement !== summary,
    'the click that dismissed the menu handed the focus back to the trigger: the reader asked for the close and the focus belongs where they put it, not on a trigger they were leaving',
  )
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
  touch.tap(touch.summaries[0])
  assert.equal(touch.summaries[0].getAttribute('aria-expanded'), 'true', 'a tap opened a submenu and the control did not say so')
  touch.tap(touch.summaries[0])
  assert.equal(touch.summaries[0].getAttribute('aria-expanded'), 'false', 'a tap closed a submenu and the control still said it was open')
})

// ---- 4. the touch screen, and the pointers that are not a mouse --------------------------

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

  page.tap(summary)
  assert.equal(group.open, true, 'the tap that has always opened a submenu no longer does')
  page.tap(summary)
  assert.equal(group.open, false, 'the tap that has always closed a submenu no longer does')
})

test('a finger never hovers, on a device that answers the hover query yes as well', () => {
  // A touch laptop: the query matches — it has a trackpad — and it also has a screen a finger
  // uses. Every one of a tap's events arrives here, and the tap's own `pointerenter` comes
  // before its click: a script that took that arrival for a hover would open the group and then
  // let the click the `<summary>` acts on close it again, which is a submenu no finger can open.
  const page = stubPage(HOME.html)
  const group = page.groups[0]
  const summary = group.querySelector('summary')

  page.tap(summary)
  assert.equal(
    group.open,
    true,
    'a tap on a device that also answers the hover query opened nothing: the tap’s own pointerenter was taken for a hover and the click that followed closed what it had opened',
  )
  assert.equal(summary.getAttribute('aria-expanded'), 'true', 'a tap opened a submenu and the control did not say so')

  page.tap(summary)
  assert.equal(group.open, false, 'a second tap did not close the submenu the first one opened')
  assert.equal(summary.getAttribute('aria-expanded'), 'false', 'a tap closed a submenu and the control still said it was open')
  assert.equal(page.timers.length, 0, 'a finger scheduled a close: nothing about a tap waits for a pointer to leave')

  // The mouse on the same machine still hovers: the check is the event's pointer, not the
  // device's one answer for everything a reader might pick up.
  page.moveTo(summary)
  assert.equal(group.open, true, 'a mouse on a touch laptop no longer opens a submenu: the device answers the query yes and its mouse is a mouse')
  page.moveTo(page.find('main')[0])
  page.runTimers()
  assert.equal(group.open, false, 'the submenu the mouse left stayed open')
})

test('a pen hovers like a mouse', () => {
  const page = stubPage(HOME.html)
  const group = page.groups[0]
  const summary = group.querySelector('summary')

  page.moveTo(summary, 'pen')
  assert.equal(group.open, true, 'a stylus arriving at a group did not open its submenu: the query that admits it asks for a fine pointer, and a pen is one')
  page.moveTo(page.find('main')[0], 'pen')
  assert.equal(group.open, true, 'the submenu collapsed the instant the pen left the group')
  page.runTimers()
  assert.equal(group.open, false, 'the submenu the pen left stayed open')
})

test('a pointer that arrives after the page did is answered without a reload', () => {
  // A phone, at load: there is no hover to answer for, so nothing is bound and the tap is the
  // whole menu.
  const page = stubPage(HOME.html, { hover: false })
  const group = page.groups[0]
  const summary = group.querySelector('summary')

  page.moveTo(summary)
  assert.equal(group.open, false, 'a device with no hovering pointer opened a submenu anyway')
  page.moveTo(page.find('main')[0])

  // A mouse is plugged in. The device's answer changes, and the page — already loaded — has to
  // hear it: a query asked once at load and never again leaves this reader with no hover at all.
  page.setPointerHover(true)
  page.moveTo(summary)
  assert.equal(group.open, true, 'the pointer that arrived after the page did got no hover: the query was asked once at load and never again')
  page.moveTo(page.find('main')[0])
  page.runTimers()
  assert.equal(group.open, false, 'the submenu the newly arrived pointer left stayed open')

  // And taken away again: the hover behaviour goes with it and the click stays, because the
  // click is the `<summary>`'s own and never belonged to the hover behaviour.
  page.setPointerHover(false)
  page.moveTo(summary)
  assert.equal(group.open, false, 'the hover behaviour outlived the pointer that hovered: the query’s change did not take it off')
  page.tap(summary)
  assert.equal(group.open, true, 'the tap went with the hover behaviour when the pointer left')
  assert.equal(page.timers.filter((timer) => !timer.cancelled).length, 0, 'a device with no hovering pointer scheduled a close')
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

// ---- the refusals, kept ------------------------------------------------------------------

/**
 * Every way a menu script breaks the promises this file holds it to, as the sentences a reader of
 * the failure gets. One function over one script source, so each defect can be planted in the
 * shipped file and the refusal seen to fire — a gate whose refusals have never been seen to fire
 * is a gate nobody knows the shape of.
 *
 * The promises are the four points in the header, each in the form a page can be observed in:
 * what opens a group and on which pointer, how long a close waits, what the control says about
 * itself, where a stranded focus ends up, and whether the device's answer is watched or sampled
 * once. They are the cases above said compactly, and the cases above are the detail; what this
 * function is for is the fixtures below, which plant one defect at a time and read the sentence
 * back.
 */
function menuFailures(script) {
  const out = []
  const pageOf = (options = {}) => stubPage(HOME.html, { script, ...options })
  const awayFrom = (page) => page.find('main')[0]

  // A device that answers the hover query no has no hover behaviour at all: the pointer's
  // arrival opens nothing, and nothing is ever scheduled. A menu that hovered there would turn
  // the tap that has always opened a submenu into a close.
  {
    const page = pageOf({ hover: false })
    const group = page.groups[0]
    page.moveTo(group.querySelector('summary'))
    if (group.open) {
      out.push(
        'a device that answers the hover query no opened a submenu on hover: the tap that used to open it is a close there, and a menu a finger cannot open is no menu',
      )
    } else if (page.timers.length > 0) {
      out.push('a device with no hovering pointer scheduled a close it can never need')
    }
  }

  // The half of that rule the query alone cannot answer: a device can answer yes and still have a
  // finger on it, and the tap sends a pointer of its own before its click does. An arrival taken
  // for a hover opens the group, and the click the `<summary>` acts on closes it again.
  {
    const page = pageOf()
    const group = page.groups[0]
    page.tap(group.querySelector('summary'))
    if (!group.open) {
      out.push(
        'a tap on a device that answers the hover query opened nothing: the tap’s own pointer arrival was taken for a hover, and the click that followed closed what it had opened',
      )
    }
  }

  // A close is scheduled a moment after the pointer leaves, and not at once: the cursor crosses
  // the seam between the summary and the list, and clips the corner of a neighbouring group on
  // the way. Half a second is the other end of the same rule.
  {
    const page = pageOf()
    const group = page.groups[0]
    page.moveTo(group.querySelector('summary'))
    page.moveTo(awayFrom(page))
    const pending = page.timers.filter((timer) => !timer.cancelled)
    if (pending.length !== 1) {
      out.push(`the pointer left a group with ${pending.length} closes scheduled: it owes exactly one`)
    } else if (pending[0].ms < 100 || pending[0].ms > 500) {
      out.push(
        `the close is scheduled ${pending[0].ms} ms after the pointer leaves: a menu that closes at once cannot be entered — the cursor crosses the seam between the trigger and the list — and one that waits half a second hangs over the page`,
      )
    }
  }

  // The control says what it is, at every step, in the one attribute a reader of the DOM looks
  // for it in — and the markup ships none, so the state can never go stale under a script that
  // never runs.
  {
    const page = pageOf()
    const group = page.groups[0]
    const summary = group.querySelector('summary')
    page.moveTo(summary)
    if (summary.getAttribute('aria-expanded') !== 'true') {
      out.push('the pointer opened a submenu and the control did not say so in aria-expanded')
    } else {
      page.moveTo(awayFrom(page))
      page.runTimers()
      if (summary.getAttribute('aria-expanded') !== 'false') out.push('the submenu closed and the control still said it was open')
    }
  }

  // The focus a click or a tap moves opens nothing. The press moves the focus to the trigger on
  // the way to the click, and a group opened by that focus is closed again by the click that
  // brought it: the first tap on a group would do nothing, and a click on a closed group whose
  // trigger did not already hold the focus would do nothing either.
  {
    const page = pageOf()
    const [product, reference] = page.groups
    const summary = product.querySelector('summary')
    page.moveTo(summary)
    page.click(summary) // the pointer is still resting on the trigger, so no arrival fires again
    page.focus(reference.querySelector('summary'))
    page.click(summary)
    if (!product.open) {
      out.push(
        'a click on a closed group whose trigger did not hold the focus opened nothing: the focus the press moved opened the group, and the click that followed closed it again',
      )
    }
  }

  // The device's answer is watched rather than sampled once: a pointer plugged in after the page
  // loaded brings the hover behaviour with it, without a reload.
  {
    const page = pageOf({ hover: false })
    page.setPointerHover(true)
    const group = page.groups[0]
    page.moveTo(group.querySelector('summary'))
    if (!group.open) {
      out.push('the pointer that arrived after the page did got no hover: the device’s answer was sampled once at load and never asked again')
    }
  }

  // A close this file did not ask for is answered too. Opening a group closes its siblings in the
  // `name` set, the browser stops drawing the list the focus was standing in and drops that focus
  // before the `toggle` the page hears about the close — so the focus has to have been remembered
  // as it arrived, and it belongs back on the trigger.
  {
    const page = pageOf()
    const [product, reference] = page.groups
    const summary = product.querySelector('summary')
    page.focus(summary)
    page.focus(product.querySelector('a'))
    page.moveTo(reference.querySelector('summary'))
    if (page.document.activeElement !== summary) {
      out.push('a group the browser closed under the reader’s focus left the focus where it was dropped: it belongs back on the trigger, as Escape and the pointer’s own close leave it')
    }
  }

  // And the other half of that rule, which is the one a close the *reader* asks for must not
  // answer: a press on the page's own text cannot take a focus, so the browser moves it to the
  // body and fires no `focusin` anywhere — and the trigger of the menu the reader has just
  // dismissed is not where they are.
  {
    const page = pageOf()
    const group = page.groups[0]
    const summary = group.querySelector('summary')
    page.focus(summary)
    page.focus(group.querySelector('a'))
    page.click(page.find('main p')[0])
    if (page.document.activeElement === summary) {
      out.push(
        'a click on the page outside the menu handed the focus back to the trigger of the menu it closed: the reader asked for the close, and the focus belongs where they put it',
      )
    }
  }

  // The pointer is not the only reader a group has. A pointer that leaves a submenu the keyboard
  // is standing in has asked for a close that is not owed yet — it is owed when the focus goes —
  // and a close that lands under the reader takes the list away from somebody reading it.
  {
    const page = pageOf()
    const group = page.groups[0]
    const summary = group.querySelector('summary')
    page.focus(summary)
    page.focus(group.querySelector('a'))
    page.moveTo(summary)
    page.moveTo(awayFrom(page))
    page.runTimers()
    if (!group.open) {
      out.push(
        'a pointer leaving closed a submenu the keyboard was standing in: the close it asked for is owed when the focus goes, not under the reader',
      )
    } else {
      page.focus(page.find('footer a')[0])
      page.runTimers()
      if (group.open) out.push('a submenu the reader had left stayed open: the close the pointer asked for never landed')
    }
  }

  // And the reader who comes back inside the delay the close waits is the reader the delay is
  // for: the close is cancelled, and the focus holds the group open in its place.
  {
    const page = pageOf()
    const group = page.groups[0]
    const summary = group.querySelector('summary')
    page.moveTo(summary)
    page.moveTo(awayFrom(page))
    page.focus(summary)
    page.runTimers()
    if (!group.open) {
      out.push(
        'the close the pointer asked for fired under the keyboard reader who had arrived inside the delay: the delay is for the reader who comes back, and coming back by keyboard is coming back',
      )
    }
  }

  // The keyboard's way into a group is not the pointer's to give. A device that answers the hover
  // query no — a phone, a tablet — has a keyboard the moment one is attached, and a reader who
  // Tabs to a trigger has to reach the links inside it: a group that opens only where a pointer
  // hovers is a menu with a level that cannot be entered at all.
  {
    const page = pageOf({ hover: false })
    const group = page.groups[0]
    page.focus(group.querySelector('summary'))
    if (!group.open) {
      out.push(
        'a device with no hovering pointer has a menu the keyboard cannot enter: a group that opens only where the pointer hovers leaves the links inside it unreachable by Tab',
      )
    }
  }

  // The guard against reopening is written for the one focus a close hands back to the trigger,
  // and it lasts exactly that long. A reader who leaves the menu altogether — the focus goes
  // outside every group, where no group's own listener hears it — and comes back to the same
  // trigger has arrived at it as a reader: the group opens for them as it did the first time.
  {
    const page = pageOf()
    const group = page.groups[0]
    const summary = group.querySelector('summary')
    page.focus(summary)
    page.focus(group.querySelector('a'))
    page.key('Escape')
    page.focus(page.find('footer a')[0] ?? awayFrom(page))
    page.focus(summary)
    if (!group.open) {
      out.push(
        'a group stayed shut for a reader who left the menu and came back to its trigger: the guard against reopening outlived the focus it was written for',
      )
    }
  }

  return out
}

test('the menu the site ships keeps every rule, so each refusal below is the one thing it breaks', () => {
  const failures = menuFailures(SCRIPT_SOURCE)
  assert.deepEqual(failures, [], `the shipped script breaks the menu's own rules:\n  ${failures.join('\n  ')}`)
})

// The defects, planted in the shipped script one at a time. Each says what it is, the edit that
// plants it, and the refusals it must bring back — the same script with nothing planted earns
// none of them.
const DEFECTS = [
  {
    what: 'a script whose pointer arrival opens the group whatever pointer arrived',
    plant: (source) => source.replace(/(on\(group, 'pointerenter', function \(event\) \{\n)          if \(!hoveringPointer\(event\)\) return\n/, '$1'),
    refuses: [/a tap on a device that answers the hover query opened nothing/],
  },
  {
    what: 'a script that asks the device once at load and never again',
    plant: (source) =>
      source.replace(/\n      if \(query\.addEventListener\) query\.addEventListener\('change', decideHover\)\n      else if \(query\.addListener\) query\.addListener\(decideHover\)/, ''),
    refuses: [/the pointer that arrived after the page did got no hover/],
  },
  {
    what: 'a script whose close waits no time at all',
    plant: (source) => source.replace('var CLOSE_AFTER_MS = 200', 'var CLOSE_AFTER_MS = 0'),
    refuses: [/the close is scheduled 0 ms after the pointer leaves/],
  },
  {
    what: 'a script that opens and closes a group without ever saying what it is',
    plant: (source) => source.replace(/\n        if \(summary\) summary\.setAttribute\('aria-expanded', group\.open \? 'true' : 'false'\)/, ''),
    refuses: [/the pointer opened a submenu and the control did not say so in aria-expanded/],
  },
  {
    what: 'a script that opens the group on the focus a press moves',
    plant: (source) => source.replace(/\n          if \(pointerFocus\) return/, ''),
    refuses: [
      /a tap on a device that answers the hover query opened nothing/,
      /a click on a closed group whose trigger did not hold the focus opened nothing/,
    ],
  },
  {
    what: 'a script that leaves the focus where the browser dropped it',
    plant: (source) =>
      source.replace(/\n        if \(!group\.open && summary && focused && focused !== summary && holds\(group, focused\)\) \{\n          restored = summary\n          summary\.focus\(\)\n        \}/, ''),
    refuses: [/a group the browser closed under the reader’s focus left the focus where it was dropped/],
  },
  {
    what: 'a script that binds the hover behaviour whatever the device answers',
    plant: (source) => source.replace('if (query && query.matches) {', 'if (query) {'),
    refuses: [/a device that answers the hover query no opened a submenu on hover/],
  },
  {
    what: 'a script that hands the focus back for a close the reader asked for',
    plant: (source) =>
      source.replace(
        /\n      var pressed = event\.target && event\.target\.closest \? event\.target\.closest\(GROUP_DETAILS\) : null\n      if \(focused && !\(pressed && holds\(pressed, focused\)\)\) focused = null/,
        '',
      ),
    refuses: [/a click on the page outside the menu handed the focus back to the trigger/],
  },
  {
    what: 'a script whose pointer closes a group the keyboard is standing in',
    plant: (source) =>
      source.replace(
        'if (pointerLeft && !readerIsInside()) closesAt = window.setTimeout(close, CLOSE_AFTER_MS)',
        'if (pointerLeft) closesAt = window.setTimeout(close, CLOSE_AFTER_MS)',
      ),
    // Two rules, and both are this one defect: a close scheduled without asking who is reading
    // lands under the reader, whether it was scheduled when the pointer left or re-scheduled a
    // moment later when the focus arrived.
    refuses: [
      /a pointer leaving closed a submenu the keyboard was standing in/,
      /the close the pointer asked for fired under the keyboard reader who had arrived inside the delay/,
    ],
  },
  {
    what: 'a script whose pointer’s close is not cancelled by the reader arriving inside the delay',
    plant: (source) =>
      source.replace(
        /\n          stopClosing\(\)\n          closeWhenNobodyIsReading\(\)\n        \}\)/,
        '\n          closeWhenNobodyIsReading()\n        })',
      ),
    refuses: [/the close the pointer asked for fired under the keyboard reader who had arrived inside the delay/],
  },
  {
    what: 'a script that binds the keyboard’s way into a group with the pointer’s hover',
    // The keyboard's half of the menu written where the pointer's half is, which is the shape a
    // device with no hovering pointer cannot enter: the hover behaviour is never bound there, and
    // the links inside a closed group go with it.
    plant: (source) =>
      source
        .replace('\n    keyboardGroups()\n', '\n')
        .replace(
          "        on(group, 'pointerenter', function (event) {",
          "        on(group, 'focusin', function (event) {\n          var fromClosing = restored\n          restored = null\n          if (!summary || event.target !== summary || fromClosing === summary) return\n          if (pointerFocus) return\n          group.open = true\n        })\n\n        on(group, 'pointerenter', function (event) {",
        ),
    refuses: [/a device with no hovering pointer has a menu the keyboard cannot enter/],
  },
  {
    what: 'a script whose guard against reopening outlives the focus it was written for',
    plant: (source) => source.replace(/\n      if \(restored && event\.target !== restored\) restored = null/, ''),
    refuses: [/a group stayed shut for a reader who left the menu and came back to its trigger/],
  },
]

test('every menu refusal fires on the defect it names', () => {
  for (const defect of DEFECTS) {
    const planted = defect.plant(SCRIPT_SOURCE)
    assert.notEqual(planted, SCRIPT_SOURCE, `${defect.what}: the planted edit changed nothing, so this fixture proves nothing`)
    const failures = menuFailures(planted)
    assert.equal(
      failures.length,
      defect.refuses.length,
      `${defect.what}: the gate reported ${failures.length} thing(s), want ${defect.refuses.length}:\n  ${failures.join('\n  ')}`,
    )
    for (const [i, pattern] of defect.refuses.entries()) {
      assert.match(failures[i], pattern, `${defect.what}: the ${i + 1} refusal is not the one it names: ${failures[i]}`)
    }
  }
})

test('the stylesheet refusals fire on the defect each one names', () => {
  const css = pageCss(HOME.html)
  const one = (failures, what) => {
    assert.equal(failures.length, 1, `${what} was not refused once:\n  ${failures.join('\n  ')}`)
    return failures[0]
  }

  // The hover states moved out of the pointer's query into a width one: nothing asks about the
  // pointer any more, and a tablet 1280 px wide is handed a hover it cannot give.
  const atWidth = css.replace(/@media[^{]*\(\s*hover\s*:\s*hover\s*\)[^{]*\{/, '@media (min-width: 48rem) {')
  assert.notEqual(atWidth, css, 'the planted query change changed nothing, so this fixture proves nothing')
  assert.match(one(hoverScopeFailures(atWidth), 'a menu whose hover states live in a width query'), /carries 0 blocks at `@media \(hover: hover\) and \(pointer: fine\)`/)

  // A stylesheet with the query and no hover state under it: the menu would have nothing for a
  // pointer to open, which is the other half of the same rule.
  assert.match(
    one(hoverScopeFailures(`@media ${POINTER_QUERY} { .other:hover { color: red } }`), 'a stylesheet with no menu hover state'),
    /the menu has no hover states at all/,
  )

  // The rule written at the top level, where no query guards it: on a touch screen a hover that
  // sticks after a tap is a mark on a group the reader has not opened.
  assert.match(
    one(hoverScopeFailures(`${css}\n.site-nav-group details summary:hover { color: red }\n`), 'a hover state outside every query'),
    /is a menu hover state outside `@media \(hover: hover\) and \(pointer: fine\)`/,
  )

  // And the same rule inside the width query, which is the shortcut this file exists to refuse:
  // a tablet is wide and cannot hover, and a phone with a mouse attached can. This one earns both
  // sentences — it is outside the pointer's query and inside a width one.
  const narrow = hoverScopeFailures(`${css}\n@media (min-width: 48rem) { .site-nav-group details summary:hover { color: red } }\n`)
  assert.equal(narrow.length, 2, `a hover state inside the width query was not refused twice:\n  ${narrow.join('\n  ')}`)
  assert.match(narrow[0], /is a menu hover state outside/)
  assert.match(narrow[1], /carries a menu hover state: the submenu opens on a pointer that hovers, never on a viewport that is wide enough/)
})
