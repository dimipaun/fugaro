// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The graph's motion (console GV2.4; design section 3.9's amendment of
// 2026-10-24, and section 2.11's inventory of the screen).
//
// The graph is the one screen the design lets move, and this file is the
// stylesheet's half of that: the amendment's rules are claims about
// `src/renderer/theme/*.css`, and the negative one -- **no animation outside the
// graph screen** -- is the claim that keeps the rest of the console still. The
// other half is `graph.spec.ts`, because an `animation-name` is a computed value
// and only a browser has one.
//
// `graph.test.ts` next door is the screen's own. This file reads the stylesheets
// as text, and then reads the **drawn** screen for the two hooks the motion keys
// on, because each of them is a fact in two files at once: a critical card
// carries `data-critical` (`data-state` is the node's **first** word, so a
// critical task's own `data-state` says `implementing`), and a running card draws
// the comet for the same words the stylesheet's comet rules select.

import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import type { PlanStatusView } from '../../src/shared/schema'
import { LayoutClient } from '../../src/renderer/graph/layout-client'
import { GraphScreen } from '../../src/renderer/screens/graph'
import { FakeDocument, everyNode } from '../support/fakeDom'
import type { FakeNode } from '../support/fakeDom'
import { FakeWorker, NodeSurface, Surface, decoded } from '../support/graphHarness'

const CONSOLE_ROOT = join(__dirname, '..', '..')
const THEME = join(CONSOLE_ROOT, 'src', 'renderer', 'theme')

// ---------------------------------------------------------------------------
// The stylesheets, as blocks
// ---------------------------------------------------------------------------

/** Block is one `{ ... }` of a stylesheet: the text before its brace (a
 * selector, or an at-rule's prelude) and the text inside it, with the preludes it
 * sits inside. A rule inside an `@media` block carries the media query; a
 * keyframe step carries the `@keyframes` it belongs to. */
interface Block {
  readonly prelude: string
  readonly body: string
  readonly parents: readonly string[]
}

/** blocks reads a stylesheet into its blocks. A brace-counting walk rather than
 * a regex, because both things this file is about nest: a keyframe step is a
 * block inside a block, and every graph rule sits inside a media query. */
function blocks(css: string): readonly Block[] {
  const text = css.replace(/\/\*[\s\S]*?\*\//g, '')
  const found: Block[] = []
  const open: { readonly prelude: string; readonly start: number; readonly parents: readonly string[] }[] = []
  let start = 0
  for (let at = 0; at < text.length; at += 1) {
    const character = text[at] ?? ''
    if (character === '{') {
      open.push({ prelude: text.slice(start, at).trim(), start: at + 1, parents: open.map((frame) => frame.prelude) })
      start = at + 1
    } else if (character === '}') {
      const frame = open.pop()
      if (frame === undefined) throw new Error(`${THEME}: a stylesheet closes a block it never opened`)
      found.push({ prelude: frame.prelude, body: text.slice(frame.start, at), parents: frame.parents })
      start = at + 1
    }
  }
  if (open.length > 0) throw new Error(`${THEME}: a stylesheet leaves a block open`)
  return found
}

/** Declaration is one `property: value` of a block. */
interface Declaration {
  readonly property: string
  readonly value: string
}

/** declarations reads one block body's declarations. Every rule this file reads
 * is flat -- no rule of the theme nests another -- so a body is a `;` list. */
function declarations(body: string): readonly Declaration[] {
  const found: Declaration[] = []
  for (const part of body.split(';')) {
    const at = part.indexOf(':')
    if (at === -1) continue
    found.push({ property: part.slice(0, at).trim(), value: part.slice(at + 1).trim() })
  }
  return found
}

/** Rule is one style rule: what it selects, what it declares, and the at-rules it
 * sits inside. */
interface Rule {
  readonly file: string
  readonly selector: string
  readonly media: readonly string[]
  readonly declarations: readonly Declaration[]
}

/** KEYFRAMES is the at-rule whose steps are not selectors. */
const KEYFRAMES = /^@keyframes\s+([a-z][a-z0-9-]*)$/

/** stylesheets is every stylesheet of the theme directory, read by name: the
 * scan is about the layer the bundler serves, so a file added to it later is
 * read here without anyone remembering to list it. */
function stylesheets(): ReadonlyMap<string, string> {
  const found = new Map<string, string>()
  for (const entry of readdirSync(THEME)) {
    if (entry.endsWith('.css')) found.set(entry, readFileSync(join(THEME, entry), 'utf8'))
  }
  if (found.size < 4) throw new Error(`${THEME} holds ${String(found.size)} stylesheets, which is fewer than the theme`)
  return found
}

/** rulesOf is one stylesheet's style rules and its keyframes, separated: a block
 * whose prelude is an at-rule is not a selector, and a keyframe step is not one
 * either. */
function rulesOf(file: string, css: string): { readonly rules: readonly Rule[]; readonly keyframes: readonly { name: string; body: string }[] } {
  const rules: Rule[] = []
  const keyframes: { name: string; body: string }[] = []
  for (const block of blocks(css)) {
    const insideKeyframes = block.parents.some((parent) => KEYFRAMES.test(parent))
    if (insideKeyframes) continue
    const name = KEYFRAMES.exec(block.prelude)?.[1]
    if (name !== undefined) {
      keyframes.push({ name, body: block.body })
      continue
    }
    if (block.prelude.startsWith('@')) continue
    rules.push({
      file,
      selector: block.prelude,
      media: block.parents.filter((parent) => parent.startsWith('@')),
      declarations: declarations(block.body),
    })
  }
  return { rules, keyframes }
}

/** themeRules is every stylesheet's rules, and `themeKeyframes` every keyframes
 * block, each naming the file it was read from. */
function themeRules(): readonly Rule[] {
  const found: Rule[] = []
  for (const [file, css] of stylesheets()) found.push(...rulesOf(file, css).rules)
  return found
}

function themeKeyframes(): readonly { readonly file: string; readonly name: string; readonly steps: readonly (readonly Declaration[])[] }[] {
  const found: { file: string; name: string; steps: readonly (readonly Declaration[])[] }[] = []
  for (const [file, css] of stylesheets()) {
    for (const keyframe of rulesOf(file, css).keyframes) {
      const steps: (readonly Declaration[])[] = []
      for (const step of blocks(keyframe.body)) steps.push(declarations(step.body))
      found.push({ file, name: keyframe.name, steps })
    }
  }
  return found
}

/** animationRules is every rule that declares an `animation` or an
 * `animation-name`, which is the whole of what "this animates" means in a
 * stylesheet. `animation-duration` alone is not one: `base.css`'s global freeze
 * is a duration and an iteration count, and it is what the graph's own arm is
 * written beside. */
function animationRules(): readonly Rule[] {
  return themeRules().filter((rule) =>
    rule.declarations.some((one) => one.property === 'animation' || one.property === 'animation-name'),
  )
}

/** animates is whether a rule turns an animation on: `animation: none` is the
 * arm that turns it off, and the two are the same property. */
function animates(rule: Rule): boolean {
  return rule.declarations.some(
    (one) => (one.property === 'animation' || one.property === 'animation-name') && one.value !== 'none',
  )
}

/** inArm is the rules of one stylesheet that declare an animation inside a media
 * query, which is how the reduced-motion and forced-colors arms are found. */
function inArm(arm: RegExp): readonly Rule[] {
  return animationRules().filter((rule) => rule.media.some((query) => arm.test(query)))
}

/** REDUCED and FORCED are the two arms of the graph's motion. */
const REDUCED = /prefers-reduced-motion:\s*reduce/
const FORCED = /forced-colors:\s*active/

/** armMisses is the selectors an arm animates but does not stop: the arm has to
 * turn an animation off **on the selector that turned it on**, because a media
 * query adds no specificity -- `section.graph svg .graph-node-comet` loses to
 * `section.graph svg .graph-node[data-state='implementing'] .graph-node-comet`
 * wherever either is written, so a shorter selector in the arm is a mark that
 * keeps moving in a mode that has no motion. */
function armMisses(arm: RegExp): readonly string[] {
  const off = inArm(arm)
    .filter((rule) => !animates(rule))
    .flatMap((rule) => rule.selector.split(',').map((one) => one.trim()))
  return animationRules()
    .filter(animates)
    .flatMap((rule) => rule.selector.split(',').map((one) => one.trim()))
    .filter((selector) => !off.includes(selector))
}

/** selectedBy is the text of every selector of the rules `matches` picks, so a
 * claim about what an arm covers is one string a reader can check. */
function selectors(rules: readonly Rule[], matches: (rule: Rule) => boolean): string {
  return rules
    .filter(matches)
    .map((rule) => rule.selector)
    .join('\n')
}

/** RUNNING is the four state words a running card carries, which are the four the
 * comet is drawn for (`data-state` is the node's first word). Three are the stages
 * a person watches and the fourth is `queued`, which is what a run in a stage this
 * screen has no word for draws -- and `queued` is only ever a running task. */
const RUNNING: readonly string[] = Object.freeze(['implementing', 'in review', 'fixing', 'queued'])

/** DASHES is the two keyframes that carry a dash along a path: the flow along a
 * critical edge and the comet around a running card. Both are dashes on a path
 * whose `d` is the layout's answer, so neither can be moved by `transform` or
 * `opacity`, and neither keyframe may touch anything else. */
const DASHES: readonly string[] = Object.freeze(['graph-comet', 'graph-flow'])

/** BUSY_SPINNER is the one selector outside the graph screen the theme animates:
 * the ring of the busy mark (`renderer/section.ts`, drawn into a section's own
 * slot while its read is in flight), and BUSY_KEYFRAME is the single turn it is
 * animated by. Design section 3.9's amendment of 2026-10-31 is where the
 * exception is bounded -- one element, one keyframe, `transform` and nothing
 * else, no animation at all under forced colors, and under reduced motion the
 * element is not drawn at all -- and the cases below are that paragraph's four
 * claims, so the exception cannot widen without one of them failing. */
const BUSY_SPINNER = 'span.console-busy-spinner'
const BUSY_KEYFRAME = 'console-busy-turn'

/** SKELETON_BLOCK is the second selector outside the graph screen the theme
 * animates: one block of a section's skeleton (`renderer/skeleton.ts`, drawn
 * while the section's own read is in flight), and SKELETON_KEYFRAME is the
 * single sweep it is animated by. Design section 3.9's amendment of 2026-11-01
 * is where that exception is bounded -- one element, one keyframe,
 * `background-position` and nothing else, no animation at all under forced
 * colors, and static blocks under reduced motion -- and the cases below are its
 * claims, so the licence cannot widen without one of them failing. */
const SKELETON_BLOCK = 'div.console-skeleton-block'
const SKELETON_KEYFRAME = 'console-skeleton-shimmer'

/** OUTSIDE_THE_GRAPH is every selector the theme animates that is not the graph
 * screen's, keyed to its keyframe: the two bounded exceptions the two
 * amendments add. */
const ANIMATED_OUTSIDE: ReadonlyMap<string, string> = new Map([
  [BUSY_SPINNER, BUSY_KEYFRAME],
  [SKELETON_BLOCK, SKELETON_KEYFRAME],
])

/** GRAPH_PREFIX is the prefix every forced-colors graph rule is written with and
 * every graph rule in the theme carries, the list's own included. */
const GRAPH_PREFIX = 'section.graph '

/** GRAPH_MARK is a selector that names something this screen draws. */
const GRAPH_MARK = /section\.graph\b|\.graph-/

/** restated is a selector as the rule it restates reads: with the graph's own
 * prefix taken off, so a rule and the arm written for it are one string. */
function restated(selector: string): string {
  return selector.startsWith(GRAPH_PREFIX) ? selector.slice(GRAPH_PREFIX.length) : selector
}

/**
 * specificity is a selector's weight as a browser compares it: how many ids, how
 * many classes -- an attribute selector and a pseudo-class included -- and how
 * many types, a pseudo-element included. Nothing in this theme selects `*`, an
 * id in a graph selector or a pseudo-element there, so the three counts are the
 * whole of what the comparison below needs.
 */
function specificity(selector: string): readonly number[] {
  const elements = (selector.match(/::[a-z-]+/g) ?? []).length
  let rest = selector.replace(/::[a-z-]+/g, ' ')
  const ids = (rest.match(/#[\w-]+/g) ?? []).length
  rest = rest.replace(/#[\w-]+/g, ' ')
  const classes = (rest.match(/\[[^\]]*\]|\.[\w-]+|:[a-z-]+(\([^)]*\))?/g) ?? []).length
  rest = rest.replace(/\[[^\]]*\]|\.[\w-]+|:[a-z-]+(\([^)]*\))?/g, ' ')
  return [ids, classes, elements + (rest.match(/[A-Za-z][\w-]*/g) ?? []).length]
}

/** atLeast is one weight at least another's, id by id, class by class, type by
 * type: a selector that ties wins where it is written later, and these arms are
 * the last rules of `screens.css`. */
function atLeast(one: readonly number[], other: readonly number[]): boolean {
  return one.every((count, at) => count >= (other[at] ?? 0))
}

/** declared is the one value a rule selecting `className` gives a property, or
 * `null` when no rule gives it one. The last rule wins, as it does in the
 * cascade, so a base rule and an arm that both set a property read as the arm. */
function declared(className: string, property: string): string | null {
  let value: string | null = null
  for (const rule of themeRules()) {
    if (!rule.selector.includes(`.${className}`)) continue
    for (const one of rule.declarations) if (one.property === property) value = one.value
  }
  return value
}

// ---------------------------------------------------------------------------
// The three motions, and nothing else
// ---------------------------------------------------------------------------

describe('the graph’s motion in the stylesheets', () => {
  it('declares the graph’s three keyframes and the two exceptions beside them', () => {
    const found = themeKeyframes()
    // The count is the design's ("Three keyframes, no more", plus the busy mark's
    // single turn the amendment of 2026-10-31 adds and the skeleton's single
    // shimmer the amendment of 2026-11-01 adds), and the names are what each one
    // is for: a comet on a running card, a pulse on a critical one, a flow along
    // a critical edge, the ring a section's busy mark turns by, and the sweep a
    // skeleton block waits under.
    expect(found.map((one) => one.name).sort()).toEqual(
      [BUSY_KEYFRAME, SKELETON_KEYFRAME, 'graph-comet', 'graph-flow', 'graph-pulse'].sort(),
    )
    for (const keyframe of found) {
      // Each one belongs to the file of the screen it is drawn on: the graph's
      // three to `screens.css` beside the screen, the two exceptions to
      // `flight-deck.css` beside the chrome that draws them.
      expect(keyframe.file, `${keyframe.name} is not drawn by the file it belongs to`).toBe(
        keyframe.name === BUSY_KEYFRAME || keyframe.name === SKELETON_KEYFRAME ? 'flight-deck.css' : 'screens.css',
      )
      expect(keyframe.steps.length, `${keyframe.name} has no steps`).toBeGreaterThan(1)
    }
  })

  it('animates nothing outside the graph screen and the two exceptions, in any stylesheet of the theme', () => {
    // **The case that matters.** The design's section 3.9 amendment lets exactly
    // one screen move, and everything above it in that section still holds for
    // every other screen -- including the rule that no figure animates anywhere.
    // A rule that animates a row, a bar or a figure fails here by name, which is
    // the only way a claim about the *rest* of the console can be tested. The two
    // exceptions the later amendments add -- the busy mark's ring and a skeleton
    // block's sweep -- are allowed **by selector**: those two, and nothing beside
    // them.
    const animating = animationRules().filter(animates)
    expect(animating.length, 'no rule animates anything, so this case proves nothing').toBeGreaterThan(0)
    const outside = animating
      .filter((rule) => !rule.selector.includes('section.graph') && !ANIMATED_OUTSIDE.has(rule.selector))
      .map((rule) => `${rule.file}: ${rule.selector}`)
    expect(outside, 'a rule animates something no screen of the theme draws').toEqual([])
    // And each exception is taken rather than merely granted: a licence nothing
    // uses is a licence with nothing behind it.
    for (const [selector, keyframe] of ANIMATED_OUTSIDE) {
      expect(animationRules().some((rule) => rule.selector === selector), `${selector}’s exception is not taken`).toBe(true)
      expect(
        animationRules().some(
          (rule) => rule.selector === selector && rule.declarations.some((one) => one.value.includes(keyframe)),
        ),
        `${selector} is animated by another keyframe`,
      ).toBe(true)
    }
  })

  it('moves opacity, a dash along each of the two paths, and the two exceptions’ own properties, and nothing else', () => {
    // The pulse is an opacity. The other two are dashes: a path's `d` is an
    // attribute the layout answered, so `stroke-dashoffset` is the one property
    // that carries a dash along it -- the flow's along the critical edge and the
    // comet's around the card. The exception is bounded to those two keyframes by
    // name, so a third state that reached for it fails here, and so does a comet
    // that started moving something else. The busy mark's turn is the second
    // bounded licence, and it is `transform` for `console-busy-turn` alone.
    const allowed = new Set(['opacity', 'stroke-dashoffset'])
    // The two exceptions, each bounded by name to a single property: the busy
    // mark's ring turns by a `transform`, and a skeleton block's sweep is the
    // `background-position` of its own gradient. Neither set may grow.
    const turning = new Set(['transform'])
    const sweeping = new Set(['background-position'])
    const LICENCES: ReadonlyMap<string, ReadonlySet<string>> = new Map([
      [BUSY_KEYFRAME, turning],
      [SKELETON_KEYFRAME, sweeping],
    ])
    for (const keyframe of themeKeyframes()) {
      const licence = LICENCES.get(keyframe.name) ?? allowed
      for (const step of keyframe.steps) {
        for (const one of step) {
          expect(licence.has(one.property), `${keyframe.name} animates ${one.property}`).toBe(true)
          if (one.property === 'stroke-dashoffset') {
            expect(DASHES, `${keyframe.name} may not animate a dash offset`).toContain(keyframe.name)
          }
        }
      }
    }
    // And both dashes really are drawn that way rather than merely allowed to be:
    // an exception nothing takes is a licence with nothing behind it.
    for (const name of DASHES) {
      expect(
        themeKeyframes()
          .find((one) => one.name === name)
          ?.steps.flat()
          .map((one) => one.property),
        `${name} is not a dash after all`,
      ).toContain('stroke-dashoffset')
    }
    // The pulse is the one that is an opacity, so the set above is not just the
    // dashes with a licence to spare.
    expect(
      themeKeyframes()
        .find((one) => one.name === 'graph-pulse')
        ?.steps.flat()
        .map((one) => one.property),
    ).toEqual(['opacity', 'opacity', 'opacity'])
    // And the busy mark's licence is taken the same way: a transform on the turn
    // and on nothing else, declared by the ring and by no other selector.
    expect(
      new Set(
        themeKeyframes()
          .find((one) => one.name === BUSY_KEYFRAME)
          ?.steps.flat()
          .map((one) => one.property),
      ),
      'the busy mark animates something other than its own turn',
    ).toEqual(turning)
    const ring = animationRules().filter((rule) => rule.selector === BUSY_SPINNER)
    expect(
      ring.some((rule) => rule.declarations.some((one) => one.value.includes(BUSY_KEYFRAME))),
      'the ring is turned by another keyframe',
    ).toBe(true)
    // And the skeleton's licence the same way: a `background-position` on the
    // sweep and on nothing else, declared by the block and by no other selector.
    expect(
      new Set(
        themeKeyframes()
          .find((one) => one.name === SKELETON_KEYFRAME)
          ?.steps.flat()
          .map((one) => one.property),
      ),
      'the skeleton animates something other than its own sweep',
    ).toEqual(sweeping)
    const block = animationRules().filter((rule) => rule.selector === SKELETON_BLOCK)
    expect(
      block.some((rule) => rule.declarations.some((one) => one.value.includes(SKELETON_KEYFRAME))),
      'the skeleton block is swept by another keyframe',
    ).toBe(true)
  })

  it('draws every animated state with the hook the rules select, on the graph screen only', () => {
    const animating = animationRules().filter(animates)
    const bySelector = selectors(animating, () => true)
    // The comet: the four running words, which is what `data-state` holds for a
    // task being worked on.
    for (const word of RUNNING) expect(bySelector, `the comet is not drawn for ${word}`).toContain(`[data-state='${word}']`)
    expect(bySelector).toContain('graph-node-comet')
    // The pulse and the flow: the two facts of the critical path, and the hook
    // the pulse keys on is the one this task adds to the node.
    expect(bySelector).toContain('graph-node-pulse')
    expect(bySelector).toContain("[data-critical='true']")
    expect(bySelector).toContain('graph-edge-path')
    // And nothing animates a word: the motion is on a card's boundary, the glow
    // behind it and an edge, never on the text a reader reads (design section 3.5).
    for (const text of ['graph-node-title', 'graph-node-word', 'graph-node-pr', 'graph-node-run', 'graph-node-mark']) {
      expect(bySelector, `${text} is animated`).not.toContain(text)
    }
  })

  it('gives each animated state a static style of its own under reduced motion', () => {
    // `base.css`'s global freeze stops an animation where it stands, which leaves
    // a comet wherever its last frame put it -- a picture of something that is not
    // happening (F36). So the graph restates each state under its own arm, and
    // the global freeze stays exactly where it is.
    const arm = inArm(REDUCED)
    expect(arm.length, 'the graph’s reduced-motion arm declares nothing').toBeGreaterThan(0)
    const still = selectors(arm, (rule) => !animates(rule))
    for (const word of RUNNING) expect(still, `reduced motion leaves ${word} moving`).toContain(`[data-state='${word}']`)
    expect(still).toContain('graph-node-comet')
    expect(still).toContain('graph-node-pulse')
    expect(still).toContain("[data-critical='true']")
    expect(still).toContain('graph-edge-path')
    // The static mark each state is left as: the comet's streak rests where its
    // dash starts on the card's border, the glow sits at its resting opacity, and
    // the flow's dashes go so the critical edge is the 3 px line it is drawn as.
    // The first two are the base rules -- a state that never animated is drawn the
    // same way -- and the dashes are the arm's own, because a dasharray set for
    // the animation would otherwise be left behind as a dashed critical path.
    expect(declared('graph-node-comet', 'stroke-dashoffset')).toBe('0')
    expect(declared('graph-node-comet', 'stroke-dasharray')).toBe('12 88')
    expect(declared('graph-node-pulse', 'opacity')).toBe('0.45')
    expect(
      selectors(arm, (rule) => rule.declarations.some((one) => one.property === 'stroke-dasharray' && one.value === 'none')),
    ).toContain('graph-edge-path')
    // And every one of the three is turned off where it was turned on, so the
    // still style above is the one a browser computes rather than one a longer
    // selector in the motion block overrules.
    expect(armMisses(REDUCED), 'reduced motion leaves an animated selector running').toEqual([])
    // The busy mark's arm is the design's "not drawn at all" rather than a
    // restatement, because the mark has no static drawing to be left as: a frozen
    // ring is a picture of half a circle rather than of reading. So the element
    // goes and the static word takes its place -- and both are read here, because
    // a `display: none` with no word behind it would be a mark that says nothing.
    expect(declared('console-busy-spinner', 'display')).toBe('none')
    expect(declared('console-busy-still-mark', 'display')).toBe('inline')
    // The skeleton's arm is a restatement rather than a removal: the blocks stay,
    // and what goes is the sweep -- the animation stopped where it was started
    // and the gradient taken off, because a freeze would leave the sweep wherever
    // its last frame put it, which is a picture of a half-loaded block rather
    // than of a section whose read has not answered.
    expect(declared('console-skeleton-block', 'animation')).toBe('none')
    expect(declared('console-skeleton-block', 'background-image')).toBe('none')
    // And the freeze the arm is written beside is still the global one: the graph
    // restates its own states, it does not replace the rule every other screen
    // still needs.
    const base = stylesheets().get('base.css') ?? ''
    expect(base, 'base.css no longer freezes animation globally').toContain('animation-duration: 0.01ms !important')
    expect(base).toContain('animation-iteration-count: 1 !important')
  })

  it('draws no animation at all under forced colors', () => {
    // Design section 4.4: forced colors keeps the mark and the border, and the
    // arm here is the same drawing the reduced-motion arm leaves behind.
    const arm = inArm(FORCED)
    expect(arm.length, 'the forced-colors arm declares nothing about the graph’s motion').toBeGreaterThan(0)
    const still = selectors(arm, (rule) => !animates(rule))
    expect(still).toContain('graph-node-comet')
    expect(still).toContain('graph-node-pulse')
    expect(still).toContain("[data-critical='true']")
    expect(still).toContain('graph-edge-path')
    for (const rule of arm) {
      // Every forced-colors animation rule is the graph screen's or one of the
      // two exceptions': the arm stops the turn and the sweep there, and it
      // draws no animation anywhere else in the theme.
      const mine =
        rule.selector.includes('section.graph') || rule.selector === BUSY_SPINNER || rule.selector === SKELETON_BLOCK
      expect(mine, 'a forced-colors animation rule is neither the graph screen’s nor an exception’s').toBe(true)
    }
    expect(armMisses(FORCED), 'forced colors leaves an animated selector running').toEqual([])
    // And the skeleton's blocks are §4.4's own drawing there: the system's
    // ground, the system's ink as an outline, and no animation at all.
    expect(declared('console-skeleton-block', 'animation')).toBe('none')
    expect(declared('console-skeleton-block', 'background-color')).toBe('Canvas')
    expect(declared('console-skeleton-block', 'border')).toContain('CanvasText')
  })
})

// ---------------------------------------------------------------------------
// The forced-colors arm: a restatement has to win
// ---------------------------------------------------------------------------

describe('the graph’s forced-colors arm', () => {
  it('writes every graph rule on a selector at least as specific as the rule it restates', () => {
    // **An arm is a restatement, and a restatement has to win.** A media query
    // adds no specificity, so a rule in the arm overrides the rule it restates
    // only where its selector is at least as specific: the graph's rules carry
    // `section.graph` (flight-deck.css's canvas rules, the list's rows, the
    // motion block above) and an arm rule written without it is one class short
    // of every one of them. That is not hypothetical -- the selected edge's
    // `Highlight` was written on `svg .graph-edge[data-selected='true']
    // .graph-edge-path`, which loses to the base rule's reading tone, so the
    // mark that says "these are the selected task's" was the first thing this
    // mode, which has no colour left to say it with, took away.
    //
    // The pair is a rule and the rule with the prefix taken off -- the same
    // element chain, written the two ways -- and a pair counts only where the
    // two set the same property, because that is what a restatement is.
    const arm = themeRules().filter((rule) => rule.media.some((query) => FORCED.test(query)))
    const graph = arm.flatMap((rule) =>
      rule.selector
        .split(',')
        .map((one) => one.trim())
        .filter((one) => GRAPH_MARK.test(one))
        .map((selector) => ({ rule, selector })),
    )
    expect(graph.length, 'the arm restates nothing this screen draws, so this case proves nothing').toBeGreaterThan(0)

    const outside = themeRules().filter((rule) => !rule.media.some((query) => FORCED.test(query)))
    const pairs: string[] = []
    for (const { rule, selector } of graph) {
      for (const other of outside) {
        for (const theirs of other.selector.split(',').map((one) => one.trim())) {
          if (restated(theirs) !== restated(selector)) continue
          const shared = other.declarations.filter((one) =>
            rule.declarations.some((mine) => mine.property === one.property),
          )
          if (shared.length === 0) continue
          pairs.push(`${selector} over ${other.file}: ${theirs}`)
          expect(
            atLeast(specificity(selector), specificity(theirs)),
            `${selector} (${specificity(selector).join('-')}) loses to ${other.file}'s ${theirs} (${specificity(theirs).join('-')}), which it restates`,
          ).toBe(true)
        }
      }
    }
    expect(pairs.length, 'no arm rule restates a rule, so this case proves nothing').toBeGreaterThan(0)
  })

  it('reads the weight of the two selectors this arm was fixed for', () => {
    // The parser above is a claim of its own, and a comparison between two
    // selectors it read as equal would pass for the wrong reason. These two are
    // the pair the fix is about: the unprefixed one the arm was written with,
    // and the prefixed one that wins. Two classes and a type against four
    // classes and two, which is the gap that made the first one lose.
    expect(specificity('svg .graph-edge[data-selected=\'true\'] .graph-edge-path')).toEqual([0, 3, 1])
    expect(specificity('section.graph svg .graph-edge[data-selected=\'true\'] .graph-edge-path')).toEqual([0, 4, 2])
  })
})

// ---------------------------------------------------------------------------
// The hooks the motion keys on
// ---------------------------------------------------------------------------

const RAW: unknown = JSON.parse(readFileSync(join('test', 'fixtures', 'plan-status.json'), 'utf8'))

const DOCUMENT: PlanStatusView = decoded(RAW)

/** drawnNodes is a document's graph as the screen draws it, one entry per card:
 * the node's own `data-state`, `data-critical` and whatever it drew inside. */
async function drawnNodes(document: PlanStatusView = DOCUMENT): Promise<readonly FakeNode[]> {
  const dom = new FakeDocument()
  const screen = new GraphScreen({
    factory: new Surface(dom),
    client: new LayoutClient(() => new FakeWorker()),
    theme: 'light',
  })
  const element = await screen.load(document, [])
  if (element === null) throw new Error('the screen drew nothing')
  return everyNode((element as NodeSurface).node).filter((each) => each.getAttribute('class') === 'graph-node')
}

/** withRunningStages is the fixture with its first four tasks running, one in
 * each of the four stages a running task can draw a word for -- `implement`,
 * `review`, `fix` and one the screen has no word for -- so that every word the
 * comet is drawn for is on the picture at once. The other tasks keep the states
 * they had, which is where the cards with no comet come from. */
function withRunningStages(): PlanStatusView {
  const copy = JSON.parse(JSON.stringify(RAW)) as { plans: { nodes: Record<string, unknown>[] }[] }
  const nodes = copy.plans[0]?.nodes ?? []
  const stages = ['implement', 'review', 'fix', 'plan']
  nodes.slice(0, stages.length).forEach((node, at) => {
    node['state'] = 'running'
    node['stage'] = stages[at]
  })
  return decoded(copy)
}

describe('the hooks the motion keys on', () => {
  it('carries data-critical on every card, true on the path and false off it', async () => {
    // `data-state` is the node's **first** word, so a task on the critical path
    // that is running says `implementing` there and its `critical` word is the
    // second one: the hook the pulse's rule selects has to be an attribute of its
    // own. The fixture's `task-two` is running **and** on the path, which is the
    // case a rule keyed on `data-state` could not see.
    const nodes = await drawnNodes()
    expect(nodes.length).toBeGreaterThan(0)
    const critical = nodes.filter((each) => each.getAttribute('data-critical') === 'true')
    const plain = nodes.filter((each) => each.getAttribute('data-critical') === 'false')
    expect(critical.length, 'no card is on the critical path').toBeGreaterThan(0)
    expect(plain.length, 'every card is on the critical path, so the hook says nothing').toBeGreaterThan(0)
    // And the hook is the node's own fact rather than the first word's: the
    // critical cards are not the ones whose state word is `critical`.
    for (const node of nodes) {
      expect(node.getAttribute('data-state'), 'a card’s data-state is not its first word').not.toBe('critical')
    }
    const words = (node: FakeNode): readonly string[] =>
      everyNode(node)
        .map((each) => each.getAttribute('data-word'))
        .filter((each): each is string => each !== null)
    for (const node of critical) expect(words(node)).toContain('critical')
    for (const node of plain) expect(words(node)).not.toContain('critical')
  })

  it('draws the comet for exactly the states the stylesheet animates it on', async () => {
    // The comet is in two files: the words the screen draws it for, in `graph.ts`,
    // and the selectors the stylesheet animates, in `screens.css`. A word that
    // drifted out of either half would put the streak out for that state with
    // every other case in this file still green -- the same silent extinction
    // `theme-selectors.test.ts` exists to prevent on the stylesheet's side. So the
    // picture is read card by card against the rules, over a document that puts
    // all four running words on it at once.
    const animated = new Set<string>()
    for (const rule of animationRules().filter((one) => animates(one) && one.selector.includes('graph-node-comet'))) {
      for (const match of rule.selector.matchAll(/\[data-state='([^']*)'\]/g)) animated.add(match[1] ?? '')
    }
    expect(animated.size, 'the stylesheet animates the comet for no state at all').toBeGreaterThan(0)

    const nodes = await drawnNodes(withRunningStages())
    const drawn = new Set<string>()
    for (const node of nodes) {
      const state = node.getAttribute('data-state') ?? ''
      const comet = everyNode(node).some((each) => each.getAttribute('class') === 'graph-node-comet')
      expect(comet, `the screen and the stylesheet disagree about the comet in state ${state}`).toBe(animated.has(state))
      if (comet) drawn.add(state)
    }
    // Both directions, and neither set is empty: a card with no comet in a state
    // the stylesheet animates fails above, and a word the screen never draws fails
    // here.
    expect([...drawn].sort()).toEqual([...animated].sort())
    expect(drawn.size, 'the document drew fewer running words than the four there are').toBe(RUNNING.length)
    expect(nodes.length, 'every card is running, so the cards with no comet prove nothing').toBeGreaterThan(drawn.size)
  })
})
