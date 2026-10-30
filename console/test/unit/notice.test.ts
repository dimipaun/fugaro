// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
//
// The notice card's own structure, drawn from a spec this file owns: the card
// `shared/version.ts` hands its handshake state to, and the one every later
// caller will hand something else. What the version-skew notice *says* is
// `version.test.ts`'s business; what a card *is* -- the ARIA, the disclosure,
// the dismissal, and the `dt`/`dd` rows that replaced a label concatenated with
// its value -- is this file's.

import { describe, expect, it } from 'vitest'

import {
  NOTICE_DISMISS_LABEL,
  NOTICE_HIDE_DETAILS,
  NOTICE_SHOW_DETAILS,
  renderNotice,
  type NoticeSpec,
} from '../../src/shared/notice'

/** FakeScreen is the smallest thing a card may draw into: the same fake
 * `version.test.ts` hands the skew notice, minus the parts a drawing is not
 * allowed to touch. */
class FakeScreen {
  readonly attributes = new Map<string, string>()
  readonly nodes: FakeScreen[] = []
  readonly classes: string[] = []
  textContent: string | null = null

  static readonly FORBIDDEN_ATTRIBUTES = new Set(['href', 'src', 'srcdoc', 'style', 'action'])

  constructor(readonly tag: string) {}

  setAttribute(name: string, value: string): void {
    const lowered = name.toLowerCase()
    if (FakeScreen.FORBIDDEN_ATTRIBUTES.has(lowered) || lowered.startsWith('on')) {
      throw new Error(`a notice may not set the ${name} attribute`)
    }
    if (lowered === 'class') {
      this.classes.length = 0
      this.classes.push(...value.split(/\s+/).filter((one) => one !== ''))
    }
    this.attributes.set(name, value)
  }

  getAttribute(name: string): string | null {
    return this.attributes.get(name) ?? null
  }

  removeAttribute(name: string): void {
    this.attributes.delete(name)
  }

  append(child: FakeScreen): void {
    this.nodes.push(child)
  }
}

const factory = { create: (tag: string): FakeScreen => new FakeScreen(tag) }

function walk(node: FakeScreen): readonly FakeScreen[] {
  const out: FakeScreen[] = [node]
  for (const child of node.nodes) out.push(...walk(child))
  return out
}

function all(node: FakeScreen, className: string): readonly FakeScreen[] {
  return walk(node).filter((one) => one.classes.includes(className))
}

function one(node: FakeScreen, className: string): FakeScreen {
  const found = all(node, className)
  expect(found, `expected exactly one .${className}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no .${className}`)
  return first
}

/** aSpec is a two-row card, its second row a list: the shapes every caller
 * draws, with the text this file chose. */
function aSpec(overrides: Partial<NoticeSpec> = {}): NoticeSpec {
  return {
    severity: 'warning',
    dismissible: true,
    heading: 'h2',
    disclosure: 'toggle',
    title: 'a title',
    reason: 'a reason, in one sentence.',
    details: [
      { label: 'a label', text: 'a value' },
      {
        label: 'a list',
        list: [
          { text: 'first' },
          { text: 'second', classes: ['x'], attributes: { role: 'listitem' } },
        ],
      },
    ],
    classes: ['console-skew'],
    ...overrides,
  }
}

describe('renderNotice', () => {
  it('draws the card, its tone and the label that names it', () => {
    const { root, title } = renderNotice(factory, aSpec())
    expect(root.tag).toBe('section')
    expect(root.classes).toEqual(['notice', 'console-skew'])
    expect(root.getAttribute('data-severity')).toBe('warning')
    // The card names itself by its title, which is the heading a reader lands
    // on when the chip hands the keyboard back.
    expect(root.getAttribute('aria-labelledby')).toBe(title.getAttribute('id'))
    expect(title.getAttribute('id')).not.toBeNull()
    expect(title.getAttribute('tabindex')).toBe('-1')
  })

  it('gives two cards on one document their own ids', () => {
    // The card is reusable, so neither id can be a fixed name: the reduced
    // mode's card is always on the window and a screen may draw its own, and
    // two cards sharing an id would leave each one named by the other's title
    // and its button opening the other's facts.
    const first = renderNotice(factory, aSpec())
    const second = renderNotice(factory, aSpec())
    expect(second.title.getAttribute('id')).not.toBe(first.title.getAttribute('id'))
    expect(second.details.getAttribute('id')).not.toBe(first.details.getAttribute('id'))
    for (const card of [first, second]) {
      expect(card.root.getAttribute('aria-labelledby')).toBe(card.title.getAttribute('id'))
      expect(card.toggle?.getAttribute('aria-controls')).toBe(card.details.getAttribute('id'))
    }
  })

  it('draws the title at the level the caller asked for', () => {
    expect(renderNotice(factory, aSpec()).title.tag).toBe('h2')
    expect(renderNotice(factory, aSpec({ heading: 'h1' })).title.tag).toBe('h1')
  })

  it('draws the mark as decoration, and leaves its shape to the stylesheet', () => {
    const { root } = renderNotice(factory, aSpec())
    const mark = one(root, 'notice-mark')
    expect(mark.tag).toBe('span')
    expect(mark.getAttribute('aria-hidden')).toBe('true')
    expect(mark.textContent, 'the mark is a shape, not a glyph in the text').toBeNull()
  })

  it('draws the reason as one paragraph, with the caller\'s own hook beside it', () => {
    const { root } = renderNotice(factory, aSpec({ reasonClasses: ['skew-reason'] }))
    const reason = one(root, 'notice-reason')
    expect(reason.tag).toBe('p')
    expect(reason.classes).toEqual(['notice-reason', 'skew-reason'])
    expect(reason.textContent).toBe('a reason, in one sentence.')
  })

  it('draws a dl of dt/dd rows, so a label and its value are two elements', () => {
    const { root } = renderNotice(factory, aSpec())
    const facts = one(root, 'notice-facts')
    expect(facts.tag).toBe('dl')
    const tags = walk(facts).map((node) => node.tag)
    // The two rows, and the list's own two items -- and not one run of text
    // spelling "a labela value", which is the defect the `dl` exists to remove.
    expect(tags).toEqual(['dl', 'dt', 'dd', 'dt', 'dd', 'ul', 'li', 'li'])
    expect(walk(facts).filter((node) => node.tag === 'dt').map((node) => node.textContent)).toEqual([
      'a label',
      'a list',
    ])
    const values = walk(facts).filter((node) => node.tag === 'dd')
    expect(values[0]?.textContent).toBe('a value')
  })

  it('draws a list value as a real ul inside its dd, hooks and all', () => {
    const { root } = renderNotice(factory, aSpec())
    const facts = one(root, 'notice-facts')
    const value = walk(facts).filter((node) => node.tag === 'dd')[1] as FakeScreen
    expect(value.nodes.map((node) => node.tag)).toEqual(['ul'])
    expect(value.nodes[0]?.classes).toEqual([])
    const items = walk(value).filter((node) => node.tag === 'li')
    expect(items.map((node) => node.textContent)).toEqual(['first', 'second'])
    expect(items[1]?.classes).toEqual(['x'])
    expect(items[1]?.getAttribute('role')).toBe('listitem')
    // A list row draws no text of its own: its value is the list.
    expect(value.textContent).toBeNull()
  })

  it('folds the details behind a button, and the button carries the ARIA', () => {
    const { toggle, details } = renderNotice(factory, aSpec())
    expect(toggle?.tag).toBe('button')
    expect(toggle?.getAttribute('type')).toBe('button')
    expect(toggle?.getAttribute('aria-expanded')).toBe('false')
    expect(toggle?.getAttribute('aria-controls')).toBe(details.getAttribute('id'))
    expect(details.getAttribute('id')).not.toBeNull()
    expect(details.getAttribute('hidden')).toBe('')
    expect(toggle?.textContent).toBe(NOTICE_SHOW_DETAILS)
    // The two labels are one decision, so the file that draws one draws both.
    expect(NOTICE_HIDE_DETAILS).not.toBe(NOTICE_SHOW_DETAILS)
  })

  it('draws an expanded notice with no button at all', () => {
    // The refused notice is the whole document: there is no second screen to
    // open its facts from, so they are drawn open and no control is created.
    const { root, toggle, details } = renderNotice(factory, aSpec({ disclosure: 'expanded', dismissible: false }))
    expect(toggle).toBeNull()
    expect(details.getAttribute('hidden')).toBeNull()
    expect(walk(root).filter((node) => node.tag === 'button')).toHaveLength(0)
  })

  it('draws no dismiss button when the notice is not dismissible', () => {
    const { dismiss } = renderNotice(factory, aSpec({ dismissible: false }))
    expect(dismiss).toBeNull()
  })

  it('draws a dismiss button whose name says what it does and whose glyph is decoration', () => {
    const { dismiss } = renderNotice(factory, aSpec())
    expect(dismiss?.tag).toBe('button')
    expect(dismiss?.getAttribute('type')).toBe('button')
    expect(dismiss?.getAttribute('aria-label')).toBe(NOTICE_DISMISS_LABEL)
    const glyph = dismiss?.nodes[0]
    expect(glyph?.getAttribute('aria-hidden')).toBe('true')
    expect(glyph?.textContent).toBe('×')
  })

  it('draws the card in one column order: mark, body, dismissal', () => {
    // The grid places them, so the order is the document's and not the
    // stylesheet's: a keyboard reader tabs from the reason to the button to the
    // dismissal, and a reader with no stylesheet reads them in that order too.
    const { root } = renderNotice(factory, aSpec())
    expect(root.nodes.map((node) => node.tag)).toEqual(['span', 'div', 'button'])
    expect(root.nodes[1]?.classes).toEqual(['notice-body'])
  })
})
