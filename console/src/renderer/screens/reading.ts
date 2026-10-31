// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The placeholder the panel holds while a tab's read is on its way.
//
// `show` marks the tab and starts that tab's read, and a read is a whole round
// to the CLI -- up to the ninety-second read timeout (`src/main/spawn.ts`) --
// so the switch itself used to change nothing on screen: the panel kept the
// last tab's drawing, refusal included, under a navigation that had already
// moved on. Two of the five views draw from what this file already holds (Live
// redraws its frame, the Graph redraws its picture) and never wait; the three
// that read on entry are the ones that need this, and so are the two that have
// nothing to redraw yet.
//
// **The placeholder is not a screen.** It draws no figure, no panel and no
// state, and it is gone the moment the read lands -- so it is `unavailable`'s
// frame with a kind of its own rather than a sixth screen: `refused` is the
// kind that carries the warn rule, and waiting is not a failure. The section
// reuses that frame's hooks (`section.console-unavailable`, `div.unavailable-
// body`, `p.unavailable-sentence`) so the panel's geometry is the one every
// other state already draws (`p.unavailable-sentence` is the theme's own prose
// rule), and the class of the kind is not in §2's inventory: no rule selects
// `[data-kind='reading']` and none may without being added there first.

import type { SkewTheme } from '../../shared/version'
import type { ElementLike } from '../app'

/** ScreenElement and ScreenFactory are the drawing surface, declared here as
 * every screen declares its own: the entry's `DomScreenFactory` is assignable
 * to it, and so is the test document's. */
export interface ScreenElement extends ElementLike {
  readonly tag: string
  append(child: ScreenElement): void
  childNodes(): readonly ScreenElement[]
}

export interface ScreenFactory {
  create(tag: string): ScreenElement
}

/** ReadingTab is the five views of the navigation, which is the whole list of
 * things a placeholder can be for: the four reads it covers and the Live frame
 * a console that has not polled yet does not hold. */
export type ReadingTab = 'live' | 'graph' | 'spend' | 'projects' | 'coordinator'

/** ReadingWords is the tab's own name, and it is the navigation's word and not
 * a second copy of it: `VIEWS` labels the button the operator just pressed, and
 * the placeholder would be asking them again to read a name that disagrees with
 * the tab it is under. `entry-wiring.test.ts` asserts this table against `VIEWS`
 * -- the same five tabs, the same five words -- so a renamed tab cannot leave
 * the words behind. */
export const READING_WORDS: Readonly<Record<ReadingTab, string>> = Object.freeze({
  live: 'Live',
  graph: 'Graph',
  spend: 'Spend',
  projects: 'Projects',
  coordinator: 'Coordinator',
})

/** READING_LEAD is the sentence's first word. It says *reading* rather than
 * "loading": what is slow is the CLI being asked, not the console drawing. */
export const READING_LEAD = 'Reading'

/** READING_STILL is the one line a long read adds. It is the second thing the
 * panel says and never a spinner, a bar or a percentage: the console knows only
 * that the read has not answered, and a progress figure would be invented --
 * "this can take a while on a slow connection" is what is true, and it is the
 * whole of what is true. */
export const READING_STILL = 'still reading… (this can take a while on a slow connection)'

/** READING_STILL_MS is how long a read may take before it says so. Fifteen
 * seconds is longer than any read the console makes over a working connection
 * and shorter than the ninety-second timeout, so the line appears for the reads
 * an operator would otherwise be left staring at and never for the ones that
 * were always going to answer. */
export const READING_STILL_MS = 15_000

/** READING_ROLE is the role the placeholder and its still line are drawn with:
 * the polite live region a "this is being read" announcement belongs in. It is
 * `status` and not `alert` -- nothing has failed -- and the theme paints no
 * rule for it: it is for the reader, not for the eye. */
export const READING_ROLE = 'status'

/** READING_KIND is the frame's kind. It is not a state of the screen: the
 * attribute is what tells a reader of the tree which of the panel's states this
 * is, and `refused` is the only kind a rule paints (design section 2.8). */
export const READING_KIND = 'reading'

/** literal is this file's one writer of a text slot, as every screen's is: the
 * argument is this file's own constant or the tab's own name, and nothing a
 * document or a child process produced ever reaches it. */
function literal(element: ScreenElement, text: string): ScreenElement {
  element.textContent = text
  return element
}

/** DrawnReading is the placeholder, the still line drawn with it, and the body
 * the entry appends that line to. The line is drawn here and **appended** by the
 * timer: the placeholder's whole vocabulary is written in one place, and what a
 * fifteen-second read decides is only whether the sentence the console already
 * has appears (`placeReading`, `src/renderer/entry.ts`). The body and not the
 * root, so the line lands under the sentence it answers without the entry
 * knowing the tree's shape. */
export interface DrawnReading {
  readonly root: ScreenElement
  readonly body: ScreenElement
  readonly still: ScreenElement
}

/** renderReading draws the placeholder for one tab. */
export function renderReading(factory: ScreenFactory, tab: ReadingTab, theme: SkewTheme): DrawnReading {
  const root = factory.create('section')
  root.setAttribute('class', 'console-unavailable')
  root.setAttribute('data-theme', theme)
  root.setAttribute('data-kind', READING_KIND)
  root.setAttribute('data-received-at', '')
  // `status` is the polite live region a tab switch belongs in: the sentence is
  // drawn and *then* the answer arrives, so a reader that cannot see the panel
  // is told what the console is waiting for rather than left with the previous
  // tab's screen.
  root.setAttribute('role', READING_ROLE)
  const body = factory.create('div')
  body.setAttribute('class', 'unavailable-body')
  root.append(body)
  const sentence = factory.create('p')
  sentence.setAttribute('class', 'unavailable-sentence')
  literal(sentence, `${READING_LEAD} ${READING_WORDS[tab]}…`)
  body.append(sentence)
  const still = factory.create('p')
  still.setAttribute('class', 'unavailable-sentence')
  // The line the timer appends is a second announcement and not a replacement
  // for the first: it is a live region of its own, so it is read when it
  // arrives rather than only when the placeholder was drawn.
  still.setAttribute('role', READING_ROLE)
  literal(still, READING_STILL)
  return { root, body, still }
}
