// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// A section's skeleton: the shape a panel holds while its read is on its way
// (`src/renderer/skeleton.ts`, and the `--skeleton-*` steps of
// `src/renderer/theme/flight-deck.css` that give it its height).
//
// **Why this file is its own.** The skeleton is the console's and not one
// screen's: `screens/reading.ts` draws one per section for the tab a switch has
// only just opened, and `section.ts` draws one into a section whose own read is
// in flight, so the claims here are the helper's own -- which shapes exist, what
// a block is, that the tree says nothing to a reader, and that the heights are
// steps of the vocabulary rather than five numbers chosen to look right. What a
// tab draws with them is `entry-wiring.test.ts`'s, and the shimmer's two arms
// are `graph-motion.test.ts`'s, because they are motion and that file holds the
// theme's whole motion licence.
//
// The document is a local fake rather than `test/support/fakeDom.ts`: this file
// reads a tag, an attribute and `append`, which is the whole of what
// `SkeletonElement` offers, and a fake with more would be offering a surface the
// helper cannot have.

import { readFileSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import {
  SKELETON_BLOCK_CLASS,
  SKELETON_CLASS,
  SKELETON_SHAPES,
  SPEND_CAPS_SHAPE,
  SPEND_REPORT_SHAPE,
  renderSkeleton,
} from '../../src/renderer/skeleton'
import type { SkeletonBlock, SkeletonElement } from '../../src/renderer/skeleton'
import { READING_WORDS } from '../../src/renderer/screens/reading'

/** FakeElement is one drawn element: a tag, its attributes, its children, and
 * the text slot it is never given -- the last one is recorded so a case can say
 * that no block ever draws a word. */
class FakeElement implements SkeletonElement {
  readonly attributes = new Map<string, string>()
  readonly children: FakeElement[] = []
  textContent: string | null = null

  constructor(readonly tag: string) {}

  setAttribute(name: string, value: string): void {
    this.attributes.set(name, value)
  }

  append(child: SkeletonElement): void {
    this.children.push(child as FakeElement)
  }
}

const factory = { create: (tag: string): FakeElement => new FakeElement(tag) }

/** walk visits every node of a tree, the root first. */
function walk(root: FakeElement): readonly FakeElement[] {
  const out: FakeElement[] = [root]
  for (const child of root.children) out.push(...walk(child))
  return out
}

/** classed is every node of a tree carrying one class, in document order. */
function classed(root: FakeElement, className: string): readonly FakeElement[] {
  return walk(root).filter((one) => (one.attributes.get('class') ?? '').split(/\s+/).includes(className))
}

/** shapes is the tree's wrappers, one per section the tab is made of. */
function wrappers(root: FakeElement): readonly FakeElement[] {
  return classed(root, SKELETON_CLASS)
}

/** KINDS is the block vocabulary as the stylesheet knows it, and the step of the
 * vocabulary each one is measured by. It is a second list on purpose: the five
 * kinds are §2's, and a kind added to `skeleton.ts` without a height below is a
 * block that draws at zero and a section that moves when the read lands. */
const KINDS: Readonly<Record<SkeletonBlock, string>> = Object.freeze({
  line: '--skeleton-line',
  chip: '--skeleton-chip',
  row: '--skeleton-row',
  card: '--skeleton-card',
  band: '--skeleton-band',
})

/** FLIGHT_DECK is the redesign's stylesheet, which is where a block's height,
 * tone and shimmer live. Comments are stripped before it is read, so the block's
 * own prose claims nothing. */
function flightDeck(): string {
  const path = join(__dirname, '..', '..', 'src', 'renderer', 'theme', 'flight-deck.css')
  return readFileSync(path, 'utf8').replace(/\/\*[\s\S]*?\*\//g, '')
}

/** tokens is the vocabulary both token layers declare, name to value: the
 * redesign's own block plus the shared `tokens.css`, which is where the steps a
 * block borrows its height from are set. */
function tokens(): ReadonlyMap<string, string> {
  const declared = new Map<string, string>()
  for (const name of ['tokens.css', 'flight-deck.css']) {
    const css = readFileSync(join(__dirname, '..', '..', 'src', 'renderer', 'theme', name), 'utf8').replace(
      /\/\*[\s\S]*?\*\//g,
      '',
    )
    for (const match of css.matchAll(/(--[a-z0-9-]+):\s*([^;]+);/g)) declared.set(match[1] ?? '', (match[2] ?? '').trim())
  }
  return declared
}

describe('a section’s skeleton', () => {
  it('draws one wrapper per shape, naming the section it stands in for and the theme it is drawn for', () => {
    const root = renderSkeleton(factory, SPEND_REPORT_SHAPE, 'dark')
    expect(root.tag).toBe('div')
    expect(root.attributes.get('class')).toBe(SKELETON_CLASS)
    expect(root.attributes.get('data-theme')).toBe('dark')
    // `data-section` is the hook the rules and the cases read to say **which**
    // section is still out, so a section whose own read has landed can be told
    // from one that has not.
    expect(root.attributes.get('data-section')).toBe('report')
    // And the theme travels as an attribute, like every other root in this
    // console: nothing about the drawing depends on the theme.
    expect(renderSkeleton(factory, SPEND_CAPS_SHAPE, 'light').attributes.get('data-theme')).toBe('light')
  })

  it('draws the shape’s blocks, in the order the content is drawn in', () => {
    const root = renderSkeleton(factory, SPEND_REPORT_SHAPE, 'dark')
    const blocks = classed(root, SKELETON_BLOCK_CLASS)
    expect(blocks.map((one) => one.tag)).toEqual(blocks.map(() => 'div'))
    expect(blocks.map((one) => one.attributes.get('data-shape'))).toEqual([...SPEND_REPORT_SHAPE.blocks])
    expect(root.children).toEqual([...blocks])
    // At least one of each kind is drawn somewhere, so no kind is a name nothing
    // in the table ever uses -- the vocabulary is a licence the shapes take.
    const drawn = new Set(
      Object.values(SKELETON_SHAPES)
        .flat()
        .flatMap((shape) => [...shape.blocks]),
    )
    for (const kind of Object.keys(KINDS)) expect(drawn, `no shape draws a ${kind}`).toContain(kind)
  })

  it('hides the whole tree from a reader, because a block is a shape and not a reading', () => {
    const root = renderSkeleton(factory, SPEND_CAPS_SHAPE, 'dark')
    // `aria-hidden` is on the wrapper, so the blocks go with it: a reader handed
    // a dozen empty boxes has been told nothing, and the sentence that says a
    // read is running is drawn beside them (`screens/reading.ts`).
    expect(root.attributes.get('aria-hidden')).toBe('true')
    for (const node of walk(root)) expect(node.textContent, 'a skeleton drew a word').toBeNull()
  })

  it('holds a shape for every tab the navigation offers, and its own sections within each', () => {
    // The tabs are one list: the skeleton table's key set is what
    // `screens/reading.ts` re-exports as `ReadingTab`, and the words are the
    // navigation's own (`entry-wiring.test.ts` asserts the two tables against
    // `VIEWS`). A tab with no shapes would be a panel that waits with nothing on
    // it at all.
    expect(Object.keys(SKELETON_SHAPES).sort()).toEqual(Object.keys(READING_WORDS).sort())
    for (const [tab, shapes] of Object.entries(SKELETON_SHAPES)) {
      expect(shapes.length, `${tab} draws no section at all`).toBeGreaterThan(0)
      for (const shape of shapes) {
        expect(shape.section, `${tab} has a section with no name`).not.toBe('')
        expect(shape.blocks.length, `${tab}'s ${shape.section} holds no blocks`).toBeGreaterThan(0)
      }
      // And the sections of one tab are distinct: two wrappers under one name
      // would make "which section is this?" unanswerable.
      const named = shapes.map((shape) => shape.section)
      expect(new Set(named).size, `${tab} names one section twice`).toBe(named.length)
    }
  })

  it('is the table Spend’s entry places, so the section that waits wears the shape of what is coming', () => {
    // Spend is the screen whose two reads land apart, so its two shapes are
    // exported and the entry hands the one whose read is still out to `mark`
    // (`entry.ts`). Identity rather than a copy: a shape edited in one place and
    // not the other would be the table and the entry disagreeing about the same
    // section.
    expect(SKELETON_SHAPES.spend[0]).toBe(SPEND_CAPS_SHAPE)
    expect(SKELETON_SHAPES.spend[1]).toBe(SPEND_REPORT_SHAPE)
  })
})

describe('the heights a skeleton holds', () => {
  it('measures every kind of block by a step of the vocabulary, and by no literal', () => {
    const css = flightDeck()
    for (const [kind, step] of Object.entries(KINDS)) {
      const rule = new RegExp(`\\[data-shape='${kind}'\\]\\s*\\{([^}]*)\\}`).exec(css)?.[1] ?? ''
      expect(rule, `no rule measures a ${kind} block: it would draw at no height at all`).not.toBe('')
      // The height is the step and not a number: a skeleton that measured
      // itself would be a second place the page's geometry is written, which is
      // the one thing this helper must not be.
      expect(rule, `a ${kind} block is not measured by ${step}`).toContain(`block-size: var(${step})`)
    }
  })

  it('declares each step once, and borrows the content’s own for the shapes that have one', () => {
    const declared = tokens()
    for (const step of [...Object.values(KINDS), '--skeleton-gap']) {
      expect(declared.get(step), `${step} is not declared`).toBeDefined()
    }
    // **The claim is that the panel does not move**, so the three steps with a
    // size in the content are the content's own: a row is `--row-height`, a chip
    // is its compact form, and the gap between two blocks is the padding a list
    // row carries. The two that have no counterpart (`line` is a run of text and
    // `card` and `band` are boxes this console draws no other way) are the only
    // literals, and they are declared here rather than in the rules above.
    expect(declared.get('--skeleton-row')).toBe('var(--row-height)')
    expect(declared.get('--skeleton-chip')).toBe('var(--row-height-compact)')
    expect(declared.get('--skeleton-gap')).toBe('var(--space-2xs)')
    for (const step of ['--row-height', '--row-height-compact', '--space-2xs']) {
      expect(declared.get(step), `${step} is borrowed but not declared`).toBeDefined()
    }
    // And the wrappers stack: the gap is the container's, so two blocks of one
    // section are spaced the way two rows of the content are.
    expect(flightDeck()).toContain('gap: var(--skeleton-gap)')
  })

  it('leaves the blocks still under reduced motion, and outlined with no animation under forced colors', () => {
    // The two arms of §3.9's amendment of 2026-11-01, and the reason each is a
    // restatement rather than a removal. Under reduced motion the blocks stay and
    // the **sweep** goes: `base.css`'s global freeze alone would leave the
    // gradient wherever its last frame put it, which is a picture of a
    // half-loaded block. Under forced colors there is no animation at all and the
    // shape is the system's own outline (§4.4).
    const css = flightDeck()
    /** arm is the body of the media block that stops the blocks' animation: `@`
     * ends one, because an arm holds no at-rule of its own, and the arm is told
     * from the busy mark's -- which the file writes before it -- by the rule it
     * holds rather than by the selector it mentions. */
    const arm = (query: string): string =>
      [...css.matchAll(new RegExp(`@media \\(${query}\\)([^@]*)`, 'g'))]
        .map((match) => match[1] ?? '')
        .find((body) => /div\.console-skeleton-block\s*\{[^}]*animation:\s*none/.test(body)) ?? ''
    const reduced = arm('prefers-reduced-motion: reduce')
    expect(reduced, 'the skeleton has no reduced-motion arm').not.toBe('')
    expect(reduced).toContain('div.console-skeleton-block')
    expect(reduced).toMatch(/animation:\s*none/)
    expect(reduced).toMatch(/background-image:\s*none/)

    const forced = arm('forced-colors: active')
    expect(forced, 'the skeleton has no forced-colors arm').not.toBe('')
    expect(forced).toContain('div.console-skeleton-block')
    expect(forced).toMatch(/animation:\s*none/)
    // The outline carries the shape once the fill is gone: a block drawn as
    // nothing at all is a panel that says a read is running by leaving a hole.
    expect(forced).toMatch(/border:[^;]*CanvasText/)
  })

  it('draws the wrapper as a column, so the blocks do not sit on one line', () => {
    // A flex row would put a section's blocks side by side, which is a panel
    // laid out like nothing the read will draw.
    expect(flightDeck()).toMatch(/div\.console-skeleton\s*\{[^}]*flex-direction:\s*column/)
  })
})
