// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// One section's read: the busy mark a control change puts up in the same frame,
// the sequence token that keeps the latest change's answer, and the session
// cache of what a read answered.
//
// **Why this is a module of its own.** The owner's rule of 2026-10-31 -- "we
// need to render parts of screen as soon as we get that data, not wait for it
// all before we show anything" -- is a rule about every tab, and the second half
// of it is about *feedback*: a control change used to look like nothing had
// happened, because the click only started a read and the panel changed when
// the read answered (twenty-three seconds for `report --json` on the owner's
// installation). Both halves are the same small piece of machinery -- a busy
// mark, a token and a cache -- so they live here rather than in the first screen
// that needed them, and the Graph, Projects and Coordinator can adopt them
// without a second implementation. This file draws the mark and decides nothing
// about a screen: which element is a section, what its label says and when a
// read starts are the caller's.
//
// **No `matchMedia`, and no motion the stylesheet cannot answer for.** The
// reduced-motion arm is CSS's (`flight-deck.css`): the mark draws a spinning
// element *and* the static word a reader with the preference sees instead, and
// the stylesheet hides one of the two. A renderer that asked the machine would
// be the second place the console reads a preference, which `entry.ts` rules out
// for the theme and this file keeps for motion.

import type { SkewTheme } from '../shared/version'
import { READING_STILL, READING_STILL_MS } from './screens/reading'
import { renderSkeleton } from './skeleton'
import type { SkeletonShape } from './skeleton'

/** SectionElement is the whole of the DOM this file touches: a text slot, the
 * three attribute writers and `append`. It is `SkewElement`'s shape, declared
 * here rather than imported so the helper depends on no screen -- the same
 * mirror-image choice `screens/degraded.ts` makes from the other side. */
export interface SectionElement {
  textContent: string | null
  setAttribute(name: string, value: string): void
  getAttribute(name: string): string | null
  removeAttribute(name: string): void
  readonly tag: string
  append(child: SectionElement): void
}

export interface SectionFactory {
  create(tag: string): SectionElement
}

/** BUSY_CLASS is the mark's own hook, and BUSY_SLOT_CLASS the empty first child
 * every section draws for it. The slot is what keeps the mark **at the top of
 * the section it belongs to**: a mark appended to the section itself would land
 * under a table nobody scrolls to, and the operator would be told a read is
 * running somewhere they cannot see. */
export const BUSY_CLASS = 'console-busy'
export const BUSY_SLOT_CLASS = 'console-busy-slot'
export const BUSY_SPINNER_CLASS = 'console-busy-spinner'
export const BUSY_STILL_MARK_CLASS = 'console-busy-still-mark'
export const BUSY_LABEL_CLASS = 'console-busy-label'
export const BUSY_STILL_CLASS = 'console-busy-still'

/** BUSY_ATTRIBUTE is on the **section**, and it is the whole of the dimming: the
 * stylesheet fades the section's children except the mark. It is an attribute
 * rather than a class because the same element is already carrying `data-busy`
 * for a reader of the tree, and one fact should not have two spellings. */
export const BUSY_ATTRIBUTE = 'data-busy'

/** BUSY_BUSY_ATTRIBUTE is the accessibility half: a section that is being
 * re-read says so, so a reader who cannot see the spinner is told. */
export const BUSY_BUSY_ATTRIBUTE = 'aria-busy'

/** BUSY_STILL_MARK is the static word the reduced-motion arm shows **instead of**
 * the spinning element. It is this file's own vocabulary and not a document's
 * string, so `literal` is the only writer it needs. */
export const BUSY_STILL_MARK = 'Reading…'

/** BUSY_ROLE is the mark's role. The label is the announcement -- what is being
 * read and, for the Spend screen, the window and the breakdown it is being read
 * for -- and `status` is the polite live region a change belongs in. */
export const BUSY_ROLE = 'status'

/** SectionTimers is the clock, injected so a case can drive the fifteen-second
 * line without waiting for it. The default is the host's own `setTimeout`, which
 * is what a `vi.useFakeTimers()` suite replaces. */
export interface SectionTimers {
  setTimeout(callback: () => void, ms: number): ReturnType<typeof setTimeout>
  clearTimeout(handle: ReturnType<typeof setTimeout>): void
}

export const NODE_TIMERS: SectionTimers = Object.freeze({
  setTimeout: (callback: () => void, ms: number) => setTimeout(callback, ms),
  clearTimeout: (handle: ReturnType<typeof setTimeout>) => {
    clearTimeout(handle)
  },
})

/** literal is this file's one writer of a text slot. Every argument it is handed
 * is this file's own constant or a label the caller built out of the console's
 * own vocabulary, so no document string reaches it. */
function literal(element: SectionElement, text: string): SectionElement {
  element.textContent = text
  return element
}

/** DrawnBusy is the mark and the line a slow read adds to it. `label` is the
 * word naming what is being read, which a second change to the same section
 * rewrites rather than drawing a second mark. */
export interface DrawnBusy {
  readonly root: SectionElement
  readonly label: SectionElement
  readonly still: SectionElement
}

/**
 * renderBusy draws the mark for one read: the spinning element, the static word
 * the reduced-motion arm shows instead of it, the label naming what is being
 * read and, for a section that has nothing drawn yet, the skeleton of the shape
 * it will take.
 *
 * All of them are drawn, always, and the stylesheet decides which of the first
 * two a reader sees: a renderer that chose would need the preference, and the
 * console reads a preference in exactly one place (`base.css`).
 *
 * **The skeleton is drawn only where the caller says the section is empty**, and
 * that is the whole of the rule the owner's complaint of 2026-10-31 has two
 * halves of: a section being **re-read** keeps the figures it already has,
 * dimmed under the mark (so the operator's own view answers the click), while a
 * section that has never been filled holds the shape of what is coming. The two
 * cases are told apart by the caller, which is the only place that knows whether
 * a read has ever landed for that section.
 */
export function renderBusy(
  factory: SectionFactory,
  label: string,
  theme: SkewTheme,
  skeleton?: SkeletonShape,
): DrawnBusy {
  const root = factory.create('div')
  root.setAttribute('class', BUSY_CLASS)
  root.setAttribute('data-theme', theme)
  root.setAttribute('role', BUSY_ROLE)
  const spinner = factory.create('span')
  spinner.setAttribute('class', BUSY_SPINNER_CLASS)
  // The spinner is a mark and not a word: the label beside it is what a reader
  // is told, so announcing an empty element as well would be noise.
  spinner.setAttribute('aria-hidden', 'true')
  root.append(spinner)
  const stillMark = factory.create('span')
  stillMark.setAttribute('class', BUSY_STILL_MARK_CLASS)
  literal(stillMark, BUSY_STILL_MARK)
  root.append(stillMark)
  const word = factory.create('span')
  word.setAttribute('class', BUSY_LABEL_CLASS)
  literal(word, label)
  root.append(word)
  // Under the label and not beside it: the mark is a wrapping row, and the
  // skeleton is the section's own height rather than one more word in the line.
  if (skeleton !== undefined) root.append(renderSkeleton(factory, skeleton, theme))
  const still = factory.create('p')
  still.setAttribute('class', BUSY_STILL_CLASS)
  literal(still, READING_STILL)
  return { root, label: word, still }
}

/** SectionRead is one screen's read lifecycle. */
export interface SectionRead {
  /**
   * start takes the next sequence token for a read that is about to be made.
   *
   * **It draws nothing, and that is deliberate.** On the first entry into a tab
   * the panel holds that tab's placeholder and the section this read belongs to
   * has not been drawn yet, so there is nowhere to put a mark; `mark` is what
   * puts one up, at the moment the section exists. What `start` does is make the
   * order of the operator's changes the order of the tokens, and it is called
   * **before the await** -- which is what keeps a slow earlier answer from
   * overwriting a later one.
   */
  start(): number
  /**
   * mark puts the busy mark up in one section for one token, and arms the line a
   * slow read adds to it.
   *
   * **It draws synchronously**, so the operator's click is answered in the frame
   * it happened in, and it is called with the section the read belongs to: the
   * mark goes at the top of that section, and the section's own `data-busy` and
   * `aria-busy` are what the stylesheet dims the rest of it by. A token that is
   * no longer the newest is not drawn -- the section belongs to the newest
   * change, and the older read is on its way out.
   *
   * `skeleton` is the shape of the section's content, and it is passed **only
   * by a caller that knows the section has nothing drawn under the mark**: the
   * mark of a section being re-read is drawn over the figures already there, and
   * a skeleton over figures would be a second drawing of the same section. A
   * mark that is already up keeps the skeleton it was drawn with -- the section
   * it belongs to is the same section, and its read is still the same read.
   */
  mark(token: number, host: SectionElement, slot: SectionElement, label: string, skeleton?: SkeletonShape): void
  /**
   * settle answers whether this token is the newest one asked for, and clears
   * the busy state when it is. A read that lands with an older token is dropped:
   * a slow earlier read must never overwrite a later one.
   */
  settle(token: number): boolean
  /** end clears the busy state without settling it, for a screen the operator
   * has left: nothing is in flight for it any more. */
  end(): void
}

/**
 * sectionRead builds one screen's read lifecycle.
 *
 * `stillMs` is how long a read may take before the mark says so, and it is the
 * placeholder's own fifteen seconds (`READING_STILL_MS`): the two say the same
 * thing about the same read, and a second number would be a second opinion.
 */
export function sectionRead(options: {
  readonly factory: SectionFactory
  readonly theme: SkewTheme
  readonly stillMs?: number
  readonly timers?: SectionTimers
}): SectionRead {
  const stillMs = options.stillMs ?? READING_STILL_MS
  const timers = options.timers ?? NODE_TIMERS
  let token = 0
  let handle: ReturnType<typeof setTimeout> | null = null
  let held: SectionElement | null = null
  /** heldMark is the mark this section is already wearing. A second change to
   * the same section **rewrites its label** rather than stacking a second mark
   * under the first: the section says what it is reading now, and two lines --
   * one for the window the operator has already replaced -- would be a section
   * arguing with itself. The still line the first read may have added stays
   * with the element, which is true: this section has been reading that long. */
  let heldMark: DrawnBusy | null = null

  const clearTimer = (): void => {
    if (handle === null) return
    timers.clearTimeout(handle)
    handle = null
  }

  const end = (): void => {
    clearTimer()
    heldMark = null
    if (held === null) return
    held.removeAttribute(BUSY_ATTRIBUTE)
    held.removeAttribute(BUSY_BUSY_ATTRIBUTE)
    held = null
  }

  return {
    start(): number {
      token += 1
      return token
    },
    mark(
      mine: number,
      host: SectionElement,
      slot: SectionElement,
      label: string,
      skeleton?: SkeletonShape,
    ): void {
      if (mine !== token) return
      const worn = held === host ? heldMark : null
      end()
      host.setAttribute(BUSY_ATTRIBUTE, 'true')
      host.setAttribute(BUSY_BUSY_ATTRIBUTE, 'true')
      const drawn = worn ?? renderBusy(options.factory, label, options.theme, skeleton)
      if (worn === null) slot.append(drawn.root)
      else literal(drawn.label, label)
      held = host
      heldMark = drawn
      handle = timers.setTimeout(() => {
        handle = null
        // The mark a later change put up is not this read's to write into, and
        // the section the read was for may be gone.
        if (mine !== token || held !== host) return
        drawn.root.append(drawn.still)
      }, stillMs)
    },
    settle(mine: number): boolean {
      if (mine !== token) return false
      end()
      return true
    },
    end,
  }
}

/** STAMP_CLASS is the cached view's own line, STAMP_AT_CLASS the words it says,
 * STAMP_LEAD their first word and STAMP_REFRESH_CLASS the control beside them.
 * The words are this file's own vocabulary, like the busy mark's, so `literal`
 * is the only writer they need. */
export const STAMP_CLASS = 'console-stamp'
export const STAMP_AT_CLASS = 'console-stamp-at'
export const STAMP_REFRESH_CLASS = 'console-stamp-refresh'
export const STAMP_LEAD = 'as of'
export const STAMP_REFRESH_LABEL = 'Refresh'

/** DrawnStamp is the stamp and the control the caller wires: this file draws the
 * action and attaches no listener, because a listener needs a document and only
 * the caller has one. */
export interface DrawnStamp {
  readonly root: SectionElement
  readonly at: SectionElement
  readonly refresh: SectionElement
}

/**
 * renderStamp draws the line a view that came out of the session cache is drawn
 * with: when it was read, and the one control that reads it again.
 *
 * **A cached figure is never silently old.** The stamp is what makes the
 * difference between "this is the view" and "this is the view as of 14:05"
 * visible, and the refresh control is the door the operator's own doubt goes
 * through -- both of them are drawn for every cached view, fresh or not, so a
 * figure that arrived without a read is always labelled as such.
 *
 * **The words are their own element, and that is not decoration.** A DOM node's
 * `textContent` is its whole subtree's, so a `<p>` holding the words *and* the
 * control answers `as of 14:05Refresh` -- to a test reading the hook, and to a
 * reader whose screen reader concatenates them. The words are therefore
 * `span.console-stamp-at` inside the line, which is the one element that says
 * when the view was read, while the line as a whole still reads "as of 14:05
 * Refresh" to a person.
 */
export function renderStamp(factory: SectionFactory, at: number): DrawnStamp {
  const root = factory.create('p')
  root.setAttribute('class', STAMP_CLASS)
  const words = factory.create('span')
  words.setAttribute('class', STAMP_AT_CLASS)
  literal(words, `${STAMP_LEAD} ${stampOf(at)}`)
  root.append(words)
  const refresh = factory.create('button')
  refresh.setAttribute('class', STAMP_REFRESH_CLASS)
  refresh.setAttribute('type', 'button')
  literal(refresh, STAMP_REFRESH_LABEL)
  root.append(refresh)
  return { root, at: words, refresh }
}

/** CACHE_LIMIT is how many views a session keeps. It is small on purpose: the
 * point is the view the operator just left and comes back to, and every entry is
 * a whole `report --json` document (the owner's is about 248 KB for a day of
 * runs, and the report is the larger of Spend's two). */
export const CACHE_LIMIT = 8

/** CACHE_FRESH_MS is how long a cached view may be served without a read. Past
 * it the view is still drawn -- instantly, which is the whole point -- but the
 * stamp says when it was read and a fresh read starts at once, so a figure the
 * operator is looking at is never silently older than five minutes. */
export const CACHE_FRESH_MS = 5 * 60_000

/** Cached is one cached answer with the console clock it was read at. */
export interface Cached<T> {
  readonly value: T
  readonly at: number
}

/** ResultCache is a bounded, in-memory, insertion-ordered map. */
export interface ResultCache<T> {
  get(key: string): Cached<T> | null
  put(key: string, value: T, at: number): void
  clear(): void
  readonly size: number
}

/**
 * resultCache builds one screen's session cache.
 *
 * **It is memory and nothing else.** No storage, no disk, no IPC: the cache is
 * an optimisation for the operator's own session and a wrong entry costs one
 * redraw, so there is nothing here worth persisting -- and persisting a spend
 * document would be writing the installation's figures somewhere the console has
 * no channel to protect.
 *
 * The bound is on entries and not on bytes: `CACHE_LIMIT` whole documents, and
 * the oldest goes out. Insertion order is the eviction order (`Map` keeps it),
 * and a `put` for a key already held replaces the value and moves it to the end,
 * which is what "the views the operator actually uses" means.
 *
 * A limit below one is read as one rather than honoured: this is exported for
 * other screens, and a cache of nothing is a cache that answers every `get` with
 * `null` while its `put` still looks like it stored something -- the one shape
 * that would make a caller's "I already read this" untrue without saying so.
 */
export function resultCache<T>(limit: number = CACHE_LIMIT): ResultCache<T> {
  const entries = new Map<string, Cached<T>>()
  const bound = Math.max(1, Math.trunc(limit))
  return {
    get(key: string): Cached<T> | null {
      return entries.get(key) ?? null
    },
    put(key: string, value: T, at: number): void {
      entries.delete(key)
      entries.set(key, { value, at })
      while (entries.size > bound) {
        const oldest = entries.keys().next()
        if (oldest.done === true) break
        entries.delete(oldest.value)
      }
    },
    clear(): void {
      entries.clear()
    },
    get size(): number {
      return entries.size
    },
  }
}

/**
 * cacheKey is one cache key out of its parts.
 *
 * The separator is a NUL, which no part this console builds can contain: the
 * parts are an installation's project name, the picker's window, a breakdown
 * word and a boolean, and a separator that could appear inside a part would let
 * two different views share one key -- which is a cached figure drawn under the
 * wrong controls, the one failure a cache must not have.
 */
export function cacheKey(parts: readonly string[]): string {
  return parts.join('\u0000')
}

/**
 * stampOf is the "as of HH:MM" a cached view is drawn with.
 *
 * It is the **local** clock and not the console's RFC 3339 UTC stamp: the stamp
 * is for the operator's eye beside a refresh control, and a figure they read at
 * 14:05 should not be stamped 13:05. Two digits of each, so the string is one
 * width and the screen does not jump when the hour rolls over.
 */
export function stampOf(at: number): string {
  const when = new Date(at)
  const pad = (value: number): string => String(value).padStart(2, '0')
  return `${pad(when.getHours())}:${pad(when.getMinutes())}`
}
