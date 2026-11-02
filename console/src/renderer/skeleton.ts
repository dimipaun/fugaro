// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// A section's skeleton: the blocks that hold the shape of the content a read has
// not answered with yet.
//
// **Why this exists.** The owner, 2026-10-31: "when changing tabs in the Console
// we should show a skeleton while we load the data, no?" The tab switch already
// says what it is waiting for in words (`screens/reading.ts`), and the busy mark
// already says a single section is being re-read (`section.ts`); what neither
// drew was the **shape** of what is coming, and a panel of grey words over an
// empty page is a panel that jumps when the read lands. A skeleton is that
// shape: token-coloured blocks where the figures, the rows and the cards will
// be, so the page an operator is looking at while a read runs is laid out like
// the page they will get.
//
// **It is shape and never content.** Nothing here draws a word, a figure or a
// state, and the whole tree is `aria-hidden`: a reader who cannot see it is owed
// the sentence that says a read is running (`reading.ts`'s status text, and the
// fifteen-second line under it), not a description of boxes. The blocks are
// drawn from `SKELETON_SHAPES` by kind, and the CSS (`flight-deck.css`, the
// skeleton block) is where a kind becomes a height, a tone and the slow
// shimmer -- one place, so a screen cannot paint a skeleton its own way.
//
// **It is placed by the caller, and it decides nothing.** Which section a
// skeleton belongs to, whether the section has anything drawn under it already,
// and which read will replace it are the caller's: this file answers one element
// per section shape, in the same frame the shape is asked for.

import type { SkewTheme } from '../shared/version'

/** SkeletonElement is the whole of the DOM this file touches: the attribute
 * writer and `append`. It is the shape both factories already are -- a screen's
 * `ScreenFactory` and the section helper's `SectionFactory` -- declared here
 * rather than imported so this file depends on neither. */
export interface SkeletonElement {
  setAttribute(name: string, value: string): void
  append(child: SkeletonElement): void
}

/** SkeletonFactory is generic in the element it makes, and that is not
 * decoration: a caller appends what this file returns to a tree of its own
 * elements (`ScreenElement`, `SectionElement`), and a helper that answered the
 * narrowest possible type would make every caller cast the skeleton back to the
 * shape it just handed in. The default is the narrow one, for a test's own
 * factory. */
export interface SkeletonFactory<T extends SkeletonElement = SkeletonElement> {
  create(tag: string): T
}

/** SKELETON_CLASS wraps one section's blocks and SKELETON_BLOCK_CLASS is one
 * block: the two hooks a rule selects, and the whole of what the stylesheet
 * needs. The attributes beside them are drawn as literals, the way every screen
 * draws the attribute its own rules key on -- `screens.css` writes
 * `tr.spend-row[data-partial='true']` against `spend.ts`'s literal, and a
 * constant in between is one indirection the selector scan cannot follow. */
export const SKELETON_CLASS = 'console-skeleton'
export const SKELETON_BLOCK_CLASS = 'console-skeleton-block'

/** SkeletonBlock is the vocabulary of shapes, and it is deliberately small: a
 * kind says what will be *there*, not what it will say.
 *
 *   - `line` — a run of text: a heading, a label, a figure's word;
 *   - `chip` — a control or a mode chip, the small raised things in a row;
 *   - `row` — one row of a table or a list, the shape most sections are made of;
 *   - `card` — one repository card, tall and bounded;
 *   - `band` — one band of the graph's canvas, wide and short.
 */
export type SkeletonBlock = 'line' | 'chip' | 'row' | 'card' | 'band'

/** SkeletonShape is one section's skeleton: the name the section is known by,
 * and the blocks that hold its shape in the order the content is drawn in. */
export interface SkeletonShape {
  readonly section: string
  readonly blocks: readonly SkeletonBlock[]
}

/**
 * SPEND_CAPS_SHAPE and SPEND_REPORT_SHAPE are Spend's two sections, and they are
 * exported because the **entry** places them: the caps are their own read
 * (`budget show`) and the report its own, so the section whose read is still out
 * wears the shape of what it will hold while the other one is already filled.
 */
export const SPEND_CAPS_SHAPE: SkeletonShape = Object.freeze({
  section: 'caps',
  // The caps panel's own shape: the heading, the mode chip, and a row per cap.
  blocks: Object.freeze<SkeletonBlock[]>(['line', 'chip', 'row', 'row']),
})

export const SPEND_REPORT_SHAPE: SkeletonShape = Object.freeze({
  section: 'report',
  // The report's: the title, the controls, the totals strip and the breakdown
  // table's rows.
  blocks: Object.freeze<SkeletonBlock[]>(['line', 'chip', 'row', 'row', 'row', 'row', 'row']),
})

/**
 * PROJECTS_SHAPE is the Projects screen's one section, and it is the whole
 * screen's shape: this tab's content is a single read (`budget show`), so the
 * section the entry marks is the screen, and the blocks below are the header,
 * the caps table and a row per panel after it.
 *
 * The counts are approximate on purpose -- the panels' heights come from a
 * document the read has not answered with yet -- and they are here so the page
 * does not jump when it lands: the header line, then the caps table's own
 * heading and rows, then one block per panel the screen draws below it.
 */
export const PROJECTS_SHAPE: SkeletonShape = Object.freeze({
  section: 'projects',
  blocks: Object.freeze<SkeletonBlock[]>(['line', 'line', 'row', 'row', 'row', 'row', 'row', 'row']),
})

/**
 * COORDINATOR_SHAPE is the shape of what the Coordinator screen's body holds
 * while its read is out: a repository's name and counts, then the rows of the
 * records and collisions under them. The header above that body -- the source
 * line and the console's sentence about the view's age -- is drawn before the
 * read answers, so it is not part of this shape: a skeleton over content that is
 * already on screen would be a second drawing of the same section.
 *
 * It is one repository's shape and not one per repository, because the document
 * says how many there are only once it lands; the shape is what the panel looks
 * like while that is still unknown.
 */
export const COORDINATOR_SHAPE: SkeletonShape = Object.freeze({
  section: 'coordinator',
  blocks: Object.freeze<SkeletonBlock[]>(['line', 'row', 'row', 'row', 'row']),
})

/**
 * COORDINATOR_QUEUE_SHAPE and COORDINATOR_TOTALS_SHAPE are the Coordinator
 * screen's two sections above that body (console 0.118.0, K1): the action queue
 * -- the line that counts what is waiting and a row per item -- and the fleet's
 * totals strip. They are exported because the **entry** places them, like
 * Spend's two: the queue is built from four reads (`coordinator.status`,
 * `runs ls`, `plan status`, `budget show`) and the totals from three of them, and
 * a section whose own read is still out wears the shape of what it will hold
 * while the others are already filled.
 *
 * The section names are distinct from the body's, so "which section is still
 * out" is answerable from a tree alone.
 */
export const COORDINATOR_QUEUE_SHAPE: SkeletonShape = Object.freeze({
  section: 'queue',
  blocks: Object.freeze<SkeletonBlock[]>(['line', 'row', 'row', 'row']),
})

export const COORDINATOR_TOTALS_SHAPE: SkeletonShape = Object.freeze({
  section: 'fleet',
  blocks: Object.freeze<SkeletonBlock[]>(['row']),
})

/** SkeletonTab is the five views of the navigation, which is the whole list of
 * tabs a skeleton can be drawn for -- the same five `reading.ts`'s
 * `READING_WORDS` names and `entry.ts`'s `VIEWS` offers. It is declared here and
 * re-exported there as `ReadingTab`, so the tabs of the navigation are one list
 * rather than two that agree today. */
export type SkeletonTab = 'live' | 'graph' | 'spend' | 'projects' | 'coordinator'

/** SKELETON_SHAPES is one tab's sections -- what the panel holds while that
 * tab's reads are out. A tab that is offered and has no shapes fails
 * `skeleton.test.ts` rather than drawing an empty panel.
 *
 * Live is here for the one case it waits on a read at all: a console that has
 * never polled has no frame to redraw, and its placeholder is a read's like any
 * other. Every other entry into Live redraws the frame this console already
 * holds, and a skeleton over a frame would be a screen taking a drawing away to
 * say that a drawing is coming. */
export const SKELETON_SHAPES: Readonly<Record<SkeletonTab, readonly SkeletonShape[]>> = Object.freeze({
  live: Object.freeze([
    Object.freeze({ section: 'head', blocks: Object.freeze<SkeletonBlock[]>(['line', 'chip']) }),
    Object.freeze({ section: 'repos', blocks: Object.freeze<SkeletonBlock[]>(['row', 'row', 'row', 'row']) }),
  ]),
  graph: Object.freeze([
    Object.freeze({ section: 'bands', blocks: Object.freeze<SkeletonBlock[]>(['band', 'band', 'band']) }),
    Object.freeze({ section: 'cards', blocks: Object.freeze<SkeletonBlock[]>(['card', 'card', 'card']) }),
  ]),
  spend: Object.freeze([SPEND_CAPS_SHAPE, SPEND_REPORT_SHAPE]),
  projects: Object.freeze([PROJECTS_SHAPE]),
  coordinator: Object.freeze([COORDINATOR_QUEUE_SHAPE, COORDINATOR_TOTALS_SHAPE, COORDINATOR_SHAPE]),
})

/** block draws one block of a kind: a `div` carrying the block class and the
 * kind, which is the whole of what a rule needs to give it a height and a
 * tone. */
function block<T extends SkeletonElement>(factory: SkeletonFactory<T>, kind: SkeletonBlock): T {
  const drawn = factory.create('div')
  drawn.setAttribute('class', SKELETON_BLOCK_CLASS)
  drawn.setAttribute('data-shape', kind)
  return drawn
}

/**
 * renderSkeleton draws one section's skeleton: a `div.console-skeleton` holding
 * its blocks.
 *
 * **The root is `aria-hidden="true"`, blocks and all**, because none of it is
 * anything: a reader who cannot see the panel is told by the sentence beside it
 * that a read is running, and one handed a dozen empty boxes has been told
 * nothing at all. `data-section` is the hook the CSS and the tests read to say
 * *which* section is still out, and it is not a state.
 *
 * The heights the blocks reserve are the stylesheet's (`flight-deck.css`, the
 * `--skeleton-*` steps), not this file's: a skeleton that measured itself would
 * be a second place the page's geometry is written, and the point of the shape
 * is that the section does not move when the read lands.
 */
export function renderSkeleton<T extends SkeletonElement>(
  factory: SkeletonFactory<T>,
  shape: SkeletonShape,
  theme: SkewTheme,
): T {
  const root = factory.create('div')
  root.setAttribute('class', SKELETON_CLASS)
  root.setAttribute('data-theme', theme)
  root.setAttribute('data-section', shape.section)
  root.setAttribute('aria-hidden', 'true')
  for (const kind of shape.blocks) root.append(block(factory, kind))
  return root
}
