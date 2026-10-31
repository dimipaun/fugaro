// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Graph's no-checkout card (console 0.116.1).
//
// `test/e2e/no-checkout.spec.ts` draws this card in the console's real window,
// under its real policy; this file is the half that runs everywhere. It holds
// the three things a window cannot check without an installation that records
// no checkout:
//
//   - **the card's cells and its words**, in the order the card declares them,
//     for both themes -- the frame, the headline, the one action, and the CLI's
//     sentence behind the disclosure rather than in place of the card.
//   - **the CLI's sentence is text and never a tree.** The sentence is a
//     refusal's own words, so it is exactly the untrusted text `sanitize.ts`
//     exists for: markup, control characters and the bidi overrides survive as
//     characters a reader sees (after the invisibles are stripped) and never as
//     an element, an attribute or a second cell.
//   - **the constants are not this file's own invention.** The sentence drawn
//     for this state is the one `src/source/source.ts` throws, the action's word
//     is the issue view's `ISSUE_CHECKOUT_LEAD`, its class is that view's own
//     checkout button and the disclosure's word is `ISSUE_DETAILS_LABEL`: a
//     reworded refusal or a renamed hook fails here rather than drawing the raw
//     refusal card again, or a bare button nothing styles.

import { describe, expect, it } from 'vitest'

import {
  ISSUE_CHECKOUT_CLASS,
  ISSUE_CHECKOUT_LEAD,
  ISSUE_DETAILS_LABEL,
} from '../../src/renderer/entry'
import { REFUSAL_UNREADABLE } from '../../src/renderer/screens/degraded'
import {
  NO_CHECKOUT_CHOICE_CLASS,
  NO_CHECKOUT_CHOICE_LABEL,
  NO_CHECKOUT_DETAILS_CLASS,
  NO_CHECKOUT_DETAILS_LABEL,
  NO_CHECKOUT_EMPTY_SENTENCE,
  NO_CHECKOUT_HEADLINE,
  NO_CHECKOUT_KIND,
  NO_CHECKOUT_OPERATION,
  NO_CHECKOUT_SENTENCE,
  NO_CHECKOUT_SENTENCE_CLASS,
  NO_CHECKOUT_SUMMARY_CLASS,
  renderNoCheckout,
} from '../../src/renderer/screens/nocheckout'
import { NO_PLAN_CHECKOUT } from '../../src/source/source'
import { INVISIBLE_CLASS, MAX_TEXT_INPUT_LENGTH } from '../../src/shared/sanitize'
import type { SkewTheme } from '../../src/shared/version'

// ---------------------------------------------------------------------------
// The fake screen.
// ---------------------------------------------------------------------------

/** FakeScreen is the smallest thing a screen may draw into: the same fake
 * `degraded.test.ts` hands the unavailable screens, with the same refusal of
 * every attribute that could make a window *do* something. */
class FakeScreen {
  readonly attributes = new Map<string, string>()
  readonly nodes: FakeScreen[] = []
  textContent: string | null = null

  static readonly FORBIDDEN_ATTRIBUTES = new Set([
    ['h', 'ref'].join(''),
    'src',
    'srcdoc',
    'style',
    'formaction',
    'action',
  ])

  constructor(readonly tag: string) {}

  setAttribute(name: string, value: string): void {
    const lowered = name.toLowerCase()
    if (FakeScreen.FORBIDDEN_ATTRIBUTES.has(lowered) || lowered.startsWith('on')) {
      throw new Error(`a screen may not set the ${name} attribute`)
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

  childNodes(): readonly FakeScreen[] {
    return this.nodes
  }
}

const factory = { create: (tag: string): FakeScreen => new FakeScreen(tag) }

/** walk is every element of a drawn tree, the root first. */
function walk(node: FakeScreen): readonly FakeScreen[] {
  const out: FakeScreen[] = [node]
  for (const child of node.childNodes()) out.push(...walk(child))
  return out
}

/** one is the single cell of a tree carrying a class, asserted to be single. */
function one(root: FakeScreen, className: string): FakeScreen {
  const found = walk(root).filter((node) => (node.attributes.get('class') ?? '').split(/\s+/).includes(className))
  expect(found, `expected exactly one .${className}`).toHaveLength(1)
  const first = found[0]
  if (first === undefined) throw new Error(`no .${className}`)
  return first
}

/** shape is the tree's tags and classes, in document order: what a sentence can
 * only change if it was read as markup rather than as text. */
function shape(root: FakeScreen): readonly string[] {
  return walk(root).map((node) => `${node.tag}.${node.attributes.get('class') ?? ''}`)
}

/** drawn is a tree's text, in document order. */
function drawn(root: FakeScreen): string {
  return walk(root)
    .map((node) => node.textContent ?? '')
    .filter((text) => text !== '')
    .join(' ')
}

/** theCard draws the card for one sentence and theme. `choosable` is the fact
 * the card cannot read for itself: whether the CLI listed a checkout for this
 * installation, which is what the picker's own `Use <dir>` buttons are drawn
 * from. */
function theCard(sentence: string, theme: SkewTheme = 'dark', choosable = true): FakeScreen {
  return renderNoCheckout(factory, sentence, theme, { choosable }).root as FakeScreen
}

const PLAIN_SENTENCE = 'the installation records no checkout'

describe('the Graph’s no-checkout card', () => {
  it('draws the frame, the headline, the one action and the disclosure, in that order', () => {
    for (const theme of ['light', 'dark'] as const) {
      const root = theCard(PLAIN_SENTENCE, theme)
      expect(root.tag).toBe('section')
      expect(root.attributes.get('class')).toBe('console-unavailable')
      expect(root.attributes.get('data-theme')).toBe(theme)
      expect(root.attributes.get('data-kind')).toBe(NO_CHECKOUT_KIND)
      // The frame carries the read this state is about, and no `received-at`: a
      // state of the installation is not a read the console made at a time.
      expect(root.attributes.get('data-operation')).toBe(NO_CHECKOUT_OPERATION)
      expect(root.attributes.get('data-received-at')).toBe('')
      // What makes the card calm: the kind is not the one the theme paints.
      expect(NO_CHECKOUT_KIND).not.toBe('refused')
      expect(shape(root)).toEqual([
        'section.console-unavailable',
        'div.unavailable-body',
        'h1.unavailable-headline',
        'button.installation-issue-checkout',
        'details.installation-issue-details',
        'summary.installation-issue-summary',
        'p.unavailable-sentence',
      ])

      const body = one(root, 'unavailable-body')
      expect(one(body, 'unavailable-headline').textContent).toBe(NO_CHECKOUT_HEADLINE)
      const choice = one(body, NO_CHECKOUT_CHOICE_CLASS)
      expect(choice.tag).toBe('button')
      expect(choice.attributes.get('type'), 'a button with no type submits the form it is in').toBe('button')
      expect(choice.textContent).toBe(NO_CHECKOUT_CHOICE_LABEL)

      const details = one(body, NO_CHECKOUT_DETAILS_CLASS)
      expect(details.tag).toBe('details')
      expect(one(details, NO_CHECKOUT_SUMMARY_CLASS).textContent).toBe(NO_CHECKOUT_DETAILS_LABEL)
      expect(one(details, NO_CHECKOUT_SENTENCE_CLASS).textContent).toBe(PLAIN_SENTENCE)

      // The card is not a refusal screen: nothing here names an operation as a
      // *cell*, which is what the entry's own reader of a refusal looks for.
      expect(walk(root).some((node) => node.attributes.get('class') === 'unavailable-operation')).toBe(false)
    }
  })

  it('draws the CLI’s sentence as text, never as a tree', () => {
    // A refusal is a child process's own words (`sanitize.ts`'s reason for
    // existing): the markup is legal text, and what must not happen is a cell,
    // an attribute or a script coming out of it.
    const hostile = '<img src=x onerror="alert(1)"> <script>alert(2)</script> '
      + '‮gnp‪ \u0000 \u001B[31mred\u001B[0m'
    const root = theCard(hostile)
    const slot = one(one(root, NO_CHECKOUT_DETAILS_CLASS), NO_CHECKOUT_SENTENCE_CLASS)

    // The same tree the plain sentence drew: nothing in the sentence became an
    // element, an attribute or a second cell.
    expect(shape(root)).toEqual(shape(theCard(PLAIN_SENTENCE)))
    expect(drawn(root)).toContain('<img src=x onerror="alert(1)">')
    expect(drawn(root)).toContain('<script>alert(2)</script>')
    // ...and the characters that could reorder or hide what is around them are
    // gone rather than drawn: the slot is one line of visible text.
    expect(new RegExp(INVISIBLE_CLASS, 'gu').test(drawn(root)), 'an invisible character reached the card').toBe(false)
    expect([...slot.attributes], 'the sentence wrote an attribute').toEqual([
      ['class', NO_CHECKOUT_SENTENCE_CLASS],
    ])
    // The card's own words are untouched by it.
    expect(one(root, 'unavailable-headline').textContent).toBe(NO_CHECKOUT_HEADLINE)
    expect(one(root, NO_CHECKOUT_CHOICE_CLASS).textContent).toBe(NO_CHECKOUT_CHOICE_LABEL)
  })

  it('says what to do instead of offering an action the picker cannot answer', () => {
    // The picker's only word about a checkout is one `Use <dir>` per checkout
    // the CLI listed for a row, and it has no folder dialog: for an
    // installation that lists none, the action opens a window that cannot
    // resolve this state. The card says so instead of drawing it.
    const root = theCard(PLAIN_SENTENCE, 'dark', false)
    expect(walk(root).some((node) => node.attributes.get('class') === NO_CHECKOUT_CHOICE_CLASS)).toBe(false)
    expect(shape(root)).toEqual([
      'section.console-unavailable',
      'div.unavailable-body',
      'h1.unavailable-headline',
      'p.unavailable-sentence',
      'details.installation-issue-details',
      'summary.installation-issue-summary',
      'p.unavailable-sentence',
    ])
    expect(drawn(root)).toContain(NO_CHECKOUT_EMPTY_SENTENCE)
    // The card is still not a refusal, and the CLI's own sentence is still
    // behind the disclosure where a reader who wants it finds it.
    expect(root.attributes.get('data-kind')).toBe(NO_CHECKOUT_KIND)
    expect(one(one(root, NO_CHECKOUT_DETAILS_CLASS), NO_CHECKOUT_SENTENCE_CLASS).textContent).toBe(PLAIN_SENTENCE)
    // And the sentence that stands in for the action names the two commands
    // that record a checkout, which is the whole of what the operator can do.
    expect(NO_CHECKOUT_EMPTY_SENTENCE).toContain('fugaro config init')
    expect(NO_CHECKOUT_EMPTY_SENTENCE).toContain('fugaro run')
  })

  it('draws the console’s own sentence when the refusal cannot be read', () => {
    // `sanitizeRefusal` refuses a sentence past its bound; the card then says so
    // in the console's words rather than drawing the value it refused.
    const root = theCard('x'.repeat(MAX_TEXT_INPUT_LENGTH + 1))
    expect(one(one(root, NO_CHECKOUT_DETAILS_CLASS), NO_CHECKOUT_SENTENCE_CLASS).textContent).toBe(
      REFUSAL_UNREADABLE,
    )
  })

  it('takes its sentence, its words and its hooks from the places that already own them', () => {
    // The channel between the CLI's refusal and this card is the sentence
    // itself (`ConsoleReply` carries no code), so the two spells of it have to
    // be one string; the rest are the issue view's own words and hooks, because
    // one action is not two labels.
    expect(NO_CHECKOUT_SENTENCE).toBe(NO_PLAN_CHECKOUT)
    expect(NO_CHECKOUT_CHOICE_LABEL).toBe(ISSUE_CHECKOUT_LEAD)
    expect(NO_CHECKOUT_DETAILS_LABEL).toBe(ISSUE_DETAILS_LABEL)
    expect(NO_CHECKOUT_CHOICE_CLASS).toBe(ISSUE_CHECKOUT_CLASS)
    expect(NO_CHECKOUT_OPERATION).toBe('plan.status')
    expect(NO_CHECKOUT_HEADLINE).toContain('checkout')
  })
})
