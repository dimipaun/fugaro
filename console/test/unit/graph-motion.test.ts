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
// as text and the drawn node for the one hook the motion keys on: a critical
// card carries `data-critical`, which is the fact the pulse's rule selects and
// the reason the hook was added at all (`data-state` is the node's **first**
// word, so a critical task's own `data-state` says `implementing`).

import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'

import { describe, expect, it } from 'vitest'

import { decodePlanStatus } from '../../src/shared/schema'
import type { PlanStatusView } from '../../src/shared/schema'
import { LayoutClient } from '../../src/renderer/graph/layout-client'
import type { WorkerLike } from '../../src/renderer/graph/layout-client'
import { GraphScreen, SVG_NAMESPACE_URI } from '../../src/renderer/screens/graph'
import type { GraphFactory, ScreenElement } from '../../src/renderer/screens/graph'
import { FakeDocument, everyNode } from '../support/fakeDom'
import type { FakeNode } from '../support/fakeDom'

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

/** selectedBy is the text of every selector of the rules `matches` picks, so a
 * claim about what an arm covers is one string a reader can check. */
function selectors(rules: readonly Rule[], matches: (rule: Rule) => boolean): string {
  return rules
    .filter(matches)
    .map((rule) => rule.selector)
    .join('\n')
}

/** RUNNING is the three state words a running card carries, which are the three
 * the comet is drawn for (`data-state` is the node's first word). */
const RUNNING: readonly string[] = Object.freeze(['implementing', 'in review', 'fixing'])

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
  it('declares exactly three keyframes, and every one of them is the graph’s', () => {
    const found = themeKeyframes()
    // The count is the design's ("Three keyframes, no more"), and the names are
    // what each one is for: a comet on a running card, a pulse on a critical one
    // and a flow along a critical edge.
    expect(found.map((one) => one.name).sort()).toEqual(['graph-comet', 'graph-flow', 'graph-pulse'])
    for (const keyframe of found) {
      expect(keyframe.file, `${keyframe.name} is not the graph screen’s`).toBe('screens.css')
      expect(keyframe.steps.length, `${keyframe.name} has no steps`).toBeGreaterThan(1)
    }
  })

  it('animates nothing outside the graph screen, in any stylesheet of the theme', () => {
    // **The case that matters.** The design's section 3.9 amendment lets exactly
    // one screen move, and everything above it in that section still holds for
    // every other screen -- including the rule that no figure animates anywhere.
    // A rule that animates a row, a bar or a figure fails here by name, which is
    // the only way a claim about the *rest* of the console can be tested.
    const animating = animationRules().filter(animates)
    expect(animating.length, 'no rule animates anything, so this case proves nothing').toBeGreaterThan(0)
    const outside = animating
      .filter((rule) => !rule.selector.includes('section.graph'))
      .map((rule) => `${rule.file}: ${rule.selector}`)
    expect(outside, 'a rule animates something the graph screen does not draw').toEqual([])
  })

  it('moves transform and opacity, and takes the one documented exception for the flow', () => {
    // A flow cannot be drawn with either: the path's `d` is an attribute the
    // layout answered, so `stroke-dashoffset` is the only property that moves a
    // dash along it. The exception is bounded to the flow's own keyframes, so a
    // comet or a pulse that reached for it fails here.
    const allowed = new Set(['transform', 'opacity', 'stroke-dashoffset'])
    for (const keyframe of themeKeyframes()) {
      for (const step of keyframe.steps) {
        for (const one of step) {
          expect(allowed.has(one.property), `${keyframe.name} animates ${one.property}`).toBe(true)
          if (one.property === 'stroke-dashoffset') {
            expect(keyframe.name, 'only the flow may animate a dash offset').toBe('graph-flow')
          }
        }
      }
    }
    // And the flow really is drawn that way rather than merely allowed to be: an
    // exception nothing takes is a licence with nothing behind it.
    expect(themeKeyframes().find((one) => one.name === 'graph-flow')?.steps.flat().map((one) => one.property)).toContain(
      'stroke-dashoffset',
    )
  })

  it('draws every animated state with the hook the rules select, on the graph screen only', () => {
    const animating = animationRules().filter(animates)
    const bySelector = selectors(animating, () => true)
    // The comet: the three running words, which is what `data-state` holds for a
    // task being worked on.
    for (const word of RUNNING) expect(bySelector, `the comet is not drawn for ${word}`).toContain(`[data-state='${word}']`)
    expect(bySelector).toContain('graph-node-comet')
    // The pulse and the flow: the two facts of the critical path, and the hook
    // the pulse keys on is the one this task adds to the node.
    expect(bySelector).toContain('graph-node-pulse')
    expect(bySelector).toContain("[data-critical='true']")
    expect(bySelector).toContain('graph-edge-path')
    // And nothing animates a word: the motion is on a card's boundary, its
    // halo and an edge, never on the text a reader reads (design section 3.5).
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
    // The static mark each state is left as: the comet rests as the lit top edge
    // of a running card, the halo is drawn solid, and the flow's dashes go so the
    // critical edge is the 3 px line it is drawn as. The first two are the base
    // rules -- a state that never animated is drawn the same way -- and the
    // dashes are the arm's own, because a dasharray set for the animation would
    // otherwise be left behind as a dashed critical path.
    expect(declared('graph-node-comet', 'transform')).toBe('none')
    expect(declared('graph-node-pulse', 'opacity')).toBe('1')
    expect(
      selectors(arm, (rule) => rule.declarations.some((one) => one.property === 'stroke-dasharray' && one.value === 'none')),
    ).toContain('graph-edge-path')
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
      expect(rule.selector, 'a forced-colors animation rule is not the graph screen’s').toContain('section.graph')
    }
  })
})

// ---------------------------------------------------------------------------
// The hook the pulse keys on
// ---------------------------------------------------------------------------

/** NodeSurface is one fake node as a `ScreenElement`: a text slot and
 * attributes. It is `graph.test.ts`'s own pair, kept here because a test file
 * cannot import another one's cases. */
class NodeSurface implements ScreenElement {
  constructor(readonly node: FakeNode) {}

  get tag(): string {
    return this.node.tagName.toLowerCase()
  }

  get textContent(): string | null {
    return this.node.textContent
  }

  set textContent(value: string | null) {
    this.node.textContent = value
  }

  setAttribute(name: string, value: string): void {
    this.node.setAttribute(name, value)
  }

  getAttribute(name: string): string | null {
    return this.node.getAttribute(name)
  }

  removeAttribute(name: string): void {
    this.node.removeAttribute(name)
  }

  append(child: ScreenElement): void {
    this.node.appendChild((child as NodeSurface).node)
  }

  childNodes(): readonly ScreenElement[] {
    return this.node.children.map((child) => new NodeSurface(child))
  }
}

/** Surface is the two-namespace factory. */
class Surface implements GraphFactory {
  constructor(private readonly document: FakeDocument) {}

  create(tag: string): ScreenElement {
    return new NodeSurface(this.document.createElement(tag))
  }

  createNS(tag: string): ScreenElement {
    return new NodeSurface(this.document.createElementNS(SVG_NAMESPACE_URI, tag))
  }
}

/** PlacingWorker is the layout protocol without a browser: it places every child
 * and routes every edge the client asked about, because a reply that leaves one
 * out is refused and the screen would draw its list instead of a picture. */
class PlacingWorker implements WorkerLike {
  private readonly listeners = new Map<string, ((event: { data?: unknown; message?: string }) => void)[]>()

  postMessage(message: unknown): void {
    const request = message as {
      readonly id?: number
      readonly cmd?: string
      readonly graph?: {
        readonly children: readonly { readonly id: string; readonly width: number; readonly height: number }[]
        readonly edges: readonly { readonly id: string }[]
      }
    }
    if (request.cmd !== 'layout' || request.graph === undefined) return
    const data = {
      children: request.graph.children.map((child, index) => ({
        id: child.id,
        x: index * 300,
        y: 0,
        width: child.width,
        height: child.height,
      })),
      edges: request.graph.edges.map((edge) => ({
        id: edge.id,
        sections: [{ startPoint: { x: 0, y: 0 }, endPoint: { x: 1, y: 1 } }],
      })),
    }
    for (const listener of this.listeners.get('message') ?? []) listener({ data: { id: request.id, data } })
  }

  addEventListener(type: string, listener: (event: { data?: unknown; message?: string }) => void): void {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener])
  }

  terminate(): void {}
}

const DOCUMENT: PlanStatusView = decoded(JSON.parse(readFileSync(join('test', 'fixtures', 'plan-status.json'), 'utf8')))

function decoded(raw: unknown): PlanStatusView {
  const result = decodePlanStatus(raw)
  if (!result.ok) throw new Error(result.skew.because)
  return result.value
}

/** drawnNodes is the fixture's graph as the screen draws it, one entry per card:
 * the node's own `data-key`, its `data-state` and its `data-critical`. */
async function drawnNodes(): Promise<readonly FakeNode[]> {
  const document = new FakeDocument()
  const screen = new GraphScreen({
    factory: new Surface(document),
    client: new LayoutClient(() => new PlacingWorker()),
    theme: 'light',
  })
  const element = await screen.load(DOCUMENT, [])
  if (element === null) throw new Error('the screen drew nothing')
  return everyNode((element as NodeSurface).node).filter((each) => each.getAttribute('class') === 'graph-node')
}

describe('the critical node’s own hook', () => {
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
})
