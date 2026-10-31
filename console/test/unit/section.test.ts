// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The section helper: the busy mark a control change puts up in the same frame,
// the sequence token that keeps the latest change's answer, and the session
// cache of what a read answered (`src/renderer/section.ts`).
//
// **Why this file is apart from `spend.test.ts`.** The helper is the owner's
// rule of 2026-10-31 written once for every tab, and Spend is only its first
// caller: the claims here are the helper's own -- what the mark draws, when the
// fifteen-second line arrives, which token wins, what the cache evicts and when
// a cached view is old enough to be re-read -- and they hold whatever screen is
// drawing it. The entry's own use of it is `entry-wiring.test.ts`'s.
//
// The document is a local fake rather than `test/support/fakeDom.ts`: this file
// reads a text slot, four attributes and `append`, and nothing else of a node is
// reachable through `SectionElement`, so a fake with anything more would be
// offering a surface the helper cannot have.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import {
  BUSY_ATTRIBUTE,
  BUSY_BUSY_ATTRIBUTE,
  BUSY_CLASS,
  BUSY_LABEL_CLASS,
  BUSY_ROLE,
  BUSY_SPINNER_CLASS,
  BUSY_STILL_CLASS,
  BUSY_STILL_MARK,
  BUSY_STILL_MARK_CLASS,
  BUSY_SLOT_CLASS,
  CACHE_FRESH_MS,
  CACHE_LIMIT,
  NODE_TIMERS,
  STAMP_CLASS,
  STAMP_REFRESH_CLASS,
  STAMP_REFRESH_LABEL,
  cacheKey,
  renderBusy,
  renderStamp,
  resultCache,
  sectionRead,
  stampOf,
} from '../../src/renderer/section'
import type { SectionElement } from '../../src/renderer/section'
import { READING_STILL, READING_STILL_MS } from '../../src/renderer/screens/reading'

/** FakeSection is one drawn element: a tag, a text slot, its attributes and its
 * children, which is the whole of `SectionElement`. */
class FakeSection implements SectionElement {
  readonly attributes = new Map<string, string>()
  readonly children: FakeSection[] = []
  textContent: string | null = null

  constructor(readonly tag: string) {}

  setAttribute(name: string, value: string): void {
    this.attributes.set(name, value)
  }

  getAttribute(name: string): string | null {
    return this.attributes.get(name) ?? null
  }

  removeAttribute(name: string): void {
    this.attributes.delete(name)
  }

  append(child: SectionElement): void {
    this.children.push(child as FakeSection)
  }
}

const factory = { create: (tag: string): SectionElement => new FakeSection(tag) }

/** walk visits every node of a tree, the root first. */
function walk(node: SectionElement): readonly FakeSection[] {
  const fake = node as FakeSection
  const out: FakeSection[] = [fake]
  for (const child of fake.children) out.push(...walk(child))
  return out
}

/** classed is every node of a tree carrying one class, in document order. */
function classed(node: SectionElement, className: string): readonly FakeSection[] {
  return walk(node).filter((one) => (one.attributes.get('class') ?? '').split(/\s+/).includes(className))
}

describe('the busy mark', () => {
  it('draws the ring, the word the reduced-motion arm shows instead, and the label', () => {
    const drawn = renderBusy(factory, 'Reading spend for 7d, by day…', 'dark')
    const root = drawn.root as FakeSection
    expect(root.tag).toBe('div')
    expect(root.attributes.get('class')).toBe(BUSY_CLASS)
    expect(root.attributes.get('data-theme')).toBe('dark')
    expect(root.attributes.get('role')).toBe(BUSY_ROLE)
    // The ring is a mark and not a word: the label beside it is what a reader is
    // told, so the empty element is hidden from the accessibility tree.
    const spinner = classed(root, BUSY_SPINNER_CLASS)[0]
    expect(spinner?.tag).toBe('span')
    expect(spinner?.attributes.get('aria-hidden')).toBe('true')
    // **Both are drawn, and the stylesheet picks one** (`flight-deck.css`, the
    // reduced-motion arm): the renderer asks the machine for no preference, so
    // the static word is here for the arm that shows it.
    const stillMark = classed(root, BUSY_STILL_MARK_CLASS)[0]
    expect(stillMark?.tag).toBe('span')
    expect(stillMark?.textContent).toBe(BUSY_STILL_MARK)
    const label = classed(root, BUSY_LABEL_CLASS)[0]
    expect(label?.tag).toBe('span')
    expect(label?.textContent).toBe('Reading spend for 7d, by day…')
    expect(root.children.map((child) => child.attributes.get('class'))).toEqual([
      BUSY_SPINNER_CLASS,
      BUSY_STILL_MARK_CLASS,
      BUSY_LABEL_CLASS,
    ])
    // The fifteen-second line is drawn with the mark and **appended by the
    // timer**, so a read that answers in time never shows it.
    expect(classed(root, BUSY_STILL_CLASS)).toEqual([])
    expect(drawn.still.textContent).toBe(READING_STILL)
  })
})

describe('one section’s read', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })
  afterEach(() => {
    vi.useRealTimers()
  })

  /** drawnSection is a section with its own slot, the shape every screen draws
   * for one of its reads. */
  function drawnSection(): { readonly host: FakeSection; readonly slot: FakeSection } {
    const host = new FakeSection('section') as FakeSection
    const slot = new FakeSection('div') as FakeSection
    slot.setAttribute('class', BUSY_SLOT_CLASS)
    host.append(slot)
    return { host, slot }
  }

  it('marks the section synchronously, and adds the fifteen-second line only when the read outlives it', () => {
    const read = sectionRead({ factory, theme: 'light' })
    const { host, slot } = drawnSection()
    const token = read.start()
    read.mark(token, host, slot, 'Reading spend for 7d, by day…')

    // Nothing awaited: the click's own frame has the mark and the section's
    // busy state.
    expect(host.getAttribute(BUSY_ATTRIBUTE)).toBe('true')
    expect(host.getAttribute(BUSY_BUSY_ATTRIBUTE)).toBe('true')
    const mark = classed(host, BUSY_CLASS)[0]
    expect(mark, 'the mark did not land in the section').toBeDefined()
    expect(slot.children[0]).toBe(mark)

    vi.advanceTimersByTime(READING_STILL_MS - 1)
    expect(classed(host, BUSY_STILL_CLASS), 'the still line arrived early').toEqual([])
    vi.advanceTimersByTime(1)
    expect(classed(host, BUSY_STILL_CLASS).map((one) => one.textContent)).toEqual([READING_STILL])
  })

  it('settles only the newest token, and drops an answer whose token is older', () => {
    const read = sectionRead({ factory, theme: 'light' })
    const { host, slot } = drawnSection()
    const first = read.start()
    read.mark(first, host, slot, 'Reading spend for 7d, by day…')
    const second = read.start()
    read.mark(second, host, slot, 'Reading spend for 30d, by day…')

    // The read asked for first answers last: it is not the section's any more,
    // and settling it would take the second read's mark down.
    expect(read.settle(first)).toBe(false)
    expect(host.getAttribute(BUSY_ATTRIBUTE)).toBe('true')
    expect(classed(host, BUSY_LABEL_CLASS).map((one) => one.textContent)).toEqual(['Reading spend for 30d, by day…'])

    expect(read.settle(second)).toBe(true)
    expect(host.getAttribute(BUSY_ATTRIBUTE)).toBeNull()
    expect(host.getAttribute(BUSY_BUSY_ATTRIBUTE)).toBeNull()
  })

  it('draws nothing for a token a later change has already superseded', () => {
    const read = sectionRead({ factory, theme: 'light' })
    const { host, slot } = drawnSection()
    const first = read.start()
    const second = read.start()
    read.mark(first, host, slot, 'Reading spend for 7d, by day…')
    expect(host.getAttribute(BUSY_ATTRIBUTE)).toBeNull()
    expect(classed(host, BUSY_CLASS)).toEqual([])
    // And the newest change still draws.
    read.mark(second, host, slot, 'Reading spend for 30d, by day…')
    expect(classed(host, BUSY_CLASS)).toHaveLength(1)
  })

  it('ends without settling: the mark goes, the section is not the read’s any more, and no line arrives', () => {
    const read = sectionRead({ factory, theme: 'light' })
    const { host, slot } = drawnSection()
    const token = read.start()
    read.mark(token, host, slot, 'Reading spend for 7d, by day…')
    read.end()
    expect(host.getAttribute(BUSY_ATTRIBUTE)).toBeNull()
    expect(host.getAttribute(BUSY_BUSY_ATTRIBUTE)).toBeNull()
    // A mark taken down by leaving the tab leaves its timer behind if the
    // timer is not cleared: the line would land in a section nobody is looking
    // at, and the *next* visit's read would find it there.
    vi.advanceTimersByTime(READING_STILL_MS * 2)
    expect(classed(host, BUSY_STILL_CLASS)).toEqual([])
    // And the token is not settled by leaving: an answer for a screen the
    // operator has left is dropped by the caller's own staleness check, and a
    // helper that answered `true` here would let it draw.
    expect(read.settle(token)).toBe(true)
  })

  it('takes its clock from the injected timers, so a host with no `setTimeout` is not needed', () => {
    const armed: { callback: () => void; ms: number }[] = []
    const read = sectionRead({
      factory,
      theme: 'light',
      timers: {
        setTimeout: (callback, ms) => {
          armed.push({ callback, ms })
          return 1 as unknown as ReturnType<typeof setTimeout>
        },
        clearTimeout: () => undefined,
      },
    })
    const { host, slot } = drawnSection()
    read.mark(read.start(), host, slot, 'Reading spend for 7d, by day…')
    expect(armed.map((one) => one.ms)).toEqual([READING_STILL_MS])
    // The default clock is the host's own, which is what a fake-timer suite
    // replaces -- and it is the same fifteen seconds the placeholder's line uses.
    expect(typeof NODE_TIMERS.setTimeout).toBe('function')
  })
})

describe('the stamp a cached view is drawn with', () => {
  it('says when the view was read, and draws the one control that reads it again', () => {
    const drawn = renderStamp(factory, new Date(2026, 9, 31, 9, 5).getTime())
    const root = drawn.root as FakeSection
    expect(root.tag).toBe('p')
    expect(root.attributes.get('class')).toBe(STAMP_CLASS)
    expect(root.textContent).toBe('as of 09:05')
    const refresh = drawn.refresh as FakeSection
    expect(refresh.tag).toBe('button')
    expect(refresh.attributes.get('class')).toBe(STAMP_REFRESH_CLASS)
    // `type` is written rather than left to the document's default: a `button`
    // inside a form without it is a submit, and this one is a redraw.
    expect(refresh.attributes.get('type')).toBe('button')
    expect(refresh.textContent).toBe(STAMP_REFRESH_LABEL)
    expect(root.children[0]).toBe(refresh)
  })

  it('pads both fields, so the stamp is one width and the screen does not jump', () => {
    expect(stampOf(new Date(2026, 0, 2, 4, 7).getTime())).toBe('04:07')
    expect(stampOf(new Date(2026, 11, 31, 23, 59).getTime())).toBe('23:59')
    expect(stampOf(new Date(2026, 5, 1, 0, 0).getTime())).toBe('00:00')
  })
})

describe('the session cache', () => {
  it('keeps a view per key, and re-putting one moves it to the end rather than duplicating it', () => {
    const cache = resultCache<string>()
    cache.put('a', 'first', 1)
    cache.put('b', 'second', 2)
    cache.put('a', 'again', 3)
    expect(cache.size).toBe(2)
    expect(cache.get('a')).toEqual({ value: 'again', at: 3 })
    expect(cache.get('b')).toEqual({ value: 'second', at: 2 })
    expect(cache.get('nothing')).toBeNull()
  })

  it('is bounded: the oldest entry goes out and the newest is always kept', () => {
    const cache = resultCache<string>(3)
    for (const key of ['a', 'b', 'c', 'd']) cache.put(key, key, 1)
    expect(cache.size).toBe(3)
    expect(cache.get('a')).toBeNull()
    expect(['b', 'c', 'd'].map((key) => cache.get(key)?.value)).toEqual(['b', 'c', 'd'])
    // The bound is small on purpose: every entry is a whole report, and the
    // point is the view the operator just left and comes back to.
    expect(CACHE_LIMIT).toBeLessThan(32)
  })

  it('forgets everything when the installation changes', () => {
    const cache = resultCache<string>()
    cache.put('a', 'first', 1)
    cache.clear()
    expect(cache.size).toBe(0)
    expect(cache.get('a')).toBeNull()
  })

  it('keys on the parts it was handed, so two different views never share one key', () => {
    // The separator is a NUL, and no part this console builds contains one: an
    // installation's project name, the picker's window word, a breakdown word
    // and a boolean. The failure this rules out is a cached figure drawn under
    // controls that did not ask for it, so the case is every pair of views the
    // three controls can tell apart.
    expect(cacheKey(['acme', '7d', 'day', 'false'])).toBe('acme\u00007d\u0000day\u0000false')
    expect(cacheKey(['acme', '7d', 'day', 'false'])).not.toBe(cacheKey(['acme', '7d', 'day']))
    expect(cacheKey(['acme', '7d'])).not.toBe(cacheKey(['acme', '30d']))
    expect(cacheKey(['acme', '7d', 'day', 'false'])).not.toBe(cacheKey(['acme', '7d', 'day', 'true']))
    expect(cacheKey(['acme', '7d', 'day', 'false'])).not.toBe(cacheKey(['acme', '7d', 'week', 'false']))
    expect(cacheKey(['acme', '7d', 'day', 'false'])).not.toBe(cacheKey(['orbit', '7d', 'day', 'false']))
  })

  it('serves a view without a read for five minutes and not a minute longer', () => {
    // The rule the operator was promised: a view this session already read is
    // drawn instantly, and one older than five minutes is drawn **and** re-read
    // rather than served silently.
    expect(CACHE_FRESH_MS).toBe(5 * 60_000)
  })
})
