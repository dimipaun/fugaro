// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Graph's no-checkout card (console 0.116.1, and the chooser 0.118.0
// "Plum" put in it).
//
// `test/e2e/no-checkout.spec.ts` draws this card in the console's real window,
// under its real policy; this file is the half that runs everywhere. It holds
// the three things a window cannot check without an installation that records
// no checkout:
//
//   - **the card's cells and its words**, in the order the card declares them,
//     for both themes -- the frame, the headline, the action this installation's
//     own list calls for, and the CLI's sentence behind the disclosure rather
//     than in place of the card.
//   - **the CLI's sentence is text and never a tree.** The sentence is a
//     refusal's own words, so it is exactly the untrusted text `sanitize.ts`
//     exists for: markup, control characters and the bidi overrides survive as
//     characters a reader sees (after the invisibles are stripped) and never as
//     an element, an attribute or a second cell. A menu entry's repository and
//     directory are the CLI's strings too, and the same holds for them.
//   - **the constants are not this file's own invention.** The sentence drawn
//     for this state is the one `src/source/source.ts` throws, the action's
//     words are the issue view's `ISSUE_CHECKOUT_LEAD` and
//     `ISSUE_CHECKOUT_LABEL`, its class and the lead's are that view's own, and
//     the disclosure's word is `ISSUE_DETAILS_LABEL`: a reworded refusal or a
//     renamed hook fails here rather than drawing the raw refusal card again, or
//     a bare button nothing styles.
//
// The four states are one test each, because the whole point of the card is that
// the operator is never shown a control that cannot answer the question it asks:
// the list was not read, the list was read and is empty, exactly one checkout,
// and more than one.

import { describe, expect, it } from 'vitest'

import {
  ISSUE_CHECKOUT_CLASS,
  ISSUE_CHECKOUT_LABEL,
  ISSUE_CHECKOUT_LEAD,
  ISSUE_CHECKOUT_LEAD_CLASS,
  ISSUE_DETAILS_LABEL,
} from '../../src/renderer/entry'
import { CHECKOUT_SELECT_CLASS, checkoutLabel } from '../../src/renderer/checkout'
import type { CheckoutChoice } from '../../src/renderer/checkout'
import { REFUSAL_UNREADABLE } from '../../src/renderer/screens/degraded'
import {
  NO_CHECKOUT_AUTO_SENTENCE,
  NO_CHECKOUT_CHOICE_CLASS,
  NO_CHECKOUT_CHOICE_LABEL,
  NO_CHECKOUT_DETAILS_CLASS,
  NO_CHECKOUT_DETAILS_LABEL,
  NO_CHECKOUT_EMPTY_SENTENCE,
  NO_CHECKOUT_HEADLINE,
  NO_CHECKOUT_KIND,
  NO_CHECKOUT_LEAD_CLASS,
  NO_CHECKOUT_OPERATION,
  NO_CHECKOUT_SENTENCE,
  NO_CHECKOUT_SENTENCE_CLASS,
  NO_CHECKOUT_SUMMARY_CLASS,
  NO_CHECKOUT_USE_LABEL,
  renderNoCheckout,
} from '../../src/renderer/screens/nocheckout'
import type { NoCheckoutAction, NoCheckoutOptions } from '../../src/renderer/screens/nocheckout'
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

/** cells is what a parent holds **directly**, in order: the card's own sequence
 * of cells, with the disclosure's contents left inside it. */
function cells(parent: FakeScreen): readonly string[] {
  return parent.childNodes().map((node) => `${node.tag}.${node.attributes.get('class') ?? ''}`)
}

/** drawn is a tree's text, in document order. */
function drawn(root: FakeScreen): string {
  return walk(root)
    .map((node) => node.textContent ?? '')
    .filter((text) => text !== '')
    .join(' ')
}

const ORBIT: CheckoutChoice = { repository: 'github/owner/orbit', path: '/home/operator/src/orbit', more: 0 }
const AURORA: CheckoutChoice = { repository: 'github/owner/aurora', path: '/home/operator/src/aurora', more: 0 }

/** theCard draws the card for one sentence, theme and installation. The default
 * is the ordinary one: the CLI listed two checkouts, so the card draws the menu
 * the issue view draws. */
function theCard(
  sentence: string,
  theme: SkewTheme = 'dark',
  options: Partial<NoCheckoutOptions> = {},
): { readonly root: FakeScreen; readonly action: NoCheckoutAction } {
  const drawn = renderNoCheckout(factory, sentence, theme, {
    choices: [ORBIT, AURORA],
    listed: true,
    auto: true,
    ...options,
  })
  return { root: drawn.root as FakeScreen, action: drawn.action }
}

const PLAIN_SENTENCE = 'the installation records no checkout'

/** theDisclosure is the CLI's sentence, which every state of the card keeps
 * behind the disclosure and in the same place. */
function theDisclosure(root: FakeScreen): FakeScreen {
  const details = one(root, NO_CHECKOUT_DETAILS_CLASS)
  expect(details.tag).toBe('details')
  expect(one(details, NO_CHECKOUT_SUMMARY_CLASS).textContent).toBe(NO_CHECKOUT_DETAILS_LABEL)
  return one(details, NO_CHECKOUT_SENTENCE_CLASS)
}

describe('the Graph’s no-checkout card', () => {
  it('draws the frame, the headline, the menu and the disclosure, in that order', () => {
    for (const theme of ['light', 'dark'] as const) {
      const { root, action } = theCard(PLAIN_SENTENCE, theme)
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

      const body = one(root, 'unavailable-body')
      // The lead, the menu and the button, and then the disclosure: the action
      // is above the CLI's own account of the state, never in place of it.
      expect(cells(body)).toEqual([
        'h1.unavailable-headline',
        `p.${NO_CHECKOUT_LEAD_CLASS}`,
        `select.${CHECKOUT_SELECT_CLASS}`,
        `button.${NO_CHECKOUT_CHOICE_CLASS}`,
        `details.${NO_CHECKOUT_DETAILS_CLASS}`,
      ])
      expect(one(body, 'unavailable-headline').textContent).toBe(NO_CHECKOUT_HEADLINE)
      expect(one(body, NO_CHECKOUT_LEAD_CLASS).textContent).toBe(NO_CHECKOUT_CHOICE_LABEL)
      const button = one(body, NO_CHECKOUT_CHOICE_CLASS)
      expect(button.tag).toBe('button')
      expect(button.attributes.get('type'), 'a button with no type submits the form it is in').toBe('button')
      expect(button.textContent).toBe(NO_CHECKOUT_USE_LABEL)

      // The action the entry wires is the menu and the button together, and the
      // menu is the one this card drew: the entry reads its value back.
      expect(action.kind).toBe('choose')
      if (action.kind !== 'choose') throw new Error('the card drew no menu')
      expect(action.select).toBe(one(body, CHECKOUT_SELECT_CLASS))
      expect(action.button).toBe(button)

      const menu = one(body, CHECKOUT_SELECT_CLASS) as FakeScreen
      expect(menu.childNodes().map((option) => option.attributes.get('value'))).toEqual([
        ORBIT.path,
        AURORA.path,
      ])
      expect(menu.childNodes().map((option) => option.textContent)).toEqual([
        checkoutLabel(ORBIT),
        checkoutLabel(AURORA),
      ])

      expect(theDisclosure(root).textContent).toBe(PLAIN_SENTENCE)
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
    const { root } = theCard(hostile)

    // The same tree the plain sentence drew: nothing in the sentence became an
    // element, an attribute or a second cell.
    expect(shape(root)).toEqual(shape(theCard(PLAIN_SENTENCE).root))
    expect(drawn(root)).toContain('<img src=x onerror="alert(1)">')
    expect(drawn(root)).toContain('<script>alert(2)</script>')
    // ...and the characters that could reorder or hide what is around them are
    // gone rather than drawn: the slot is one line of visible text.
    expect(new RegExp(INVISIBLE_CLASS, 'gu').test(drawn(root)), 'an invisible character reached the card').toBe(false)
    const slot = theDisclosure(root)
    expect([...slot.attributes], 'the sentence wrote an attribute').toEqual([
      ['class', NO_CHECKOUT_SENTENCE_CLASS],
    ])
    // The card's own words are untouched by it.
    expect(one(root, 'unavailable-headline').textContent).toBe(NO_CHECKOUT_HEADLINE)
    expect(one(root, NO_CHECKOUT_CHOICE_CLASS).textContent).toBe(NO_CHECKOUT_USE_LABEL)
  })

  it('draws a hostile repository and directory as menu text, never as a tree', () => {
    // The two strings in a menu entry are the CLI's own -- the identity it
    // recorded and the path it printed -- so the card draws them through the
    // same sanitizer the sentence goes through.
    const hostile: CheckoutChoice = {
      repository: '<img src=x onerror="alert(1)">/owner/<script>alert(2)</script>',
      path: '/home/operator/src/‮evil‬/scratch\u0000name',
      more: 0,
    }
    const { root } = theCard(PLAIN_SENTENCE, 'dark', { choices: [hostile, AURORA] })

    expect(shape(root)).toEqual(shape(theCard(PLAIN_SENTENCE).root))
    expect(drawn(root)).toContain('<img src=x onerror="alert(1)">')
    expect(drawn(root)).toContain('<script>alert(2)</script>')
    expect(new RegExp(INVISIBLE_CLASS, 'gu').test(drawn(root)), 'an invisible character reached the menu').toBe(false)
    expect(drawn(root)).not.toContain('\u0000')
    const menu = one(root, CHECKOUT_SELECT_CLASS)
    const option = menu.childNodes()[0]
    // The label is the console's own words about the two strings, and the value
    // is the path exactly as the CLI printed it: that is the string the main
    // process matches against its own list.
    expect(option?.attributes.get('value')).toBe(hostile.path)
  })

  it('keeps the picker’s door when this console never read the CLI’s list', () => {
    // The dashboard's fallback row carries an empty list because nothing was
    // read, not because the CLI listed none: the card cannot say "no checkout is
    // recorded" about a list it never saw, so it offers the one action it can
    // honestly offer and claims nothing.
    const { root, action } = theCard(PLAIN_SENTENCE, 'dark', { choices: [], listed: false })
    const body = one(root, 'unavailable-body')
    expect(cells(body)).toEqual([
      'h1.unavailable-headline',
      `button.${NO_CHECKOUT_CHOICE_CLASS}`,
      `details.${NO_CHECKOUT_DETAILS_CLASS}`,
    ])
    expect(one(body, NO_CHECKOUT_CHOICE_CLASS).textContent).toBe(NO_CHECKOUT_CHOICE_LABEL)
    expect(drawn(root)).not.toContain(NO_CHECKOUT_EMPTY_SENTENCE)
    expect(action.kind).toBe('picker')
    expect(theDisclosure(root).textContent).toBe(PLAIN_SENTENCE)
  })

  it('says what records a checkout instead of offering an action that leads nowhere', () => {
    // The action is a menu drawn from the CLI's own list, and a card that
    // offered it for a row with nothing in that list would be a control with
    // nothing behind it.
    const { root, action } = theCard(PLAIN_SENTENCE, 'dark', { choices: [] })
    const body = one(root, 'unavailable-body')
    expect(cells(body)).toEqual([
      'h1.unavailable-headline',
      `p.${NO_CHECKOUT_SENTENCE_CLASS}`,
      `details.${NO_CHECKOUT_DETAILS_CLASS}`,
    ])
    // The second cell and not a `one(body, …)`: the disclosure below it wears
    // the same hook, and the card's own sentence is the one above the fold.
    expect(body.childNodes()[1]?.textContent).toBe(NO_CHECKOUT_EMPTY_SENTENCE)
    expect(action.kind).toBe('empty')
    // The card is still not a refusal, and the CLI's own sentence is still
    // behind the disclosure where a reader who wants it finds it.
    expect(root.attributes.get('data-kind')).toBe(NO_CHECKOUT_KIND)
    expect(theDisclosure(root).textContent).toBe(PLAIN_SENTENCE)
    // And the sentence that stands in for the action names the two commands
    // that record a checkout, which is the whole of what the operator can do.
    expect(NO_CHECKOUT_EMPTY_SENTENCE).toContain('fugaro config init')
    expect(NO_CHECKOUT_EMPTY_SENTENCE).toContain('fugaro run')
  })

  it('chooses the one checkout by itself, says so once, and draws no menu of one entry', () => {
    // A menu with a single entry is a control that can only be pressed one way,
    // so the console presses it: the card says which directory the Graph is
    // reading, in the entry's own words for it, and offers no control.
    const { root, action } = theCard(PLAIN_SENTENCE, 'dark', { choices: [ORBIT] })
    const body = one(root, 'unavailable-body')
    expect(cells(body)).toEqual([
      'h1.unavailable-headline',
      `p.${NO_CHECKOUT_SENTENCE_CLASS}`,
      `details.${NO_CHECKOUT_DETAILS_CLASS}`,
    ])
    expect(body.childNodes()[1]?.textContent).toBe(`${NO_CHECKOUT_AUTO_SENTENCE} ${checkoutLabel(ORBIT)}`)
    // Once: the sentence is the card's, and the card draws it in one cell.
    expect(drawn(root).split(NO_CHECKOUT_AUTO_SENTENCE)).toHaveLength(2)
    expect(action.kind).toBe('auto')
    if (action.kind !== 'auto') throw new Error('the card chose nothing')
    expect(action.choice).toEqual(ORBIT)
    expect(theDisclosure(root).textContent).toBe(PLAIN_SENTENCE)
  })

  it('draws the one checkout as the menu it is when the console has already chosen', () => {
    // The sentence says the Graph **is reading** the directory, and a console
    // that had already sent it and been answered with the same refusal must not
    // say that again: the one entry becomes a menu the operator presses.
    const { root, action } = theCard(PLAIN_SENTENCE, 'dark', { choices: [ORBIT], auto: false })
    const body = one(root, 'unavailable-body')
    expect(cells(body)).toEqual([
      'h1.unavailable-headline',
      `p.${NO_CHECKOUT_LEAD_CLASS}`,
      `select.${CHECKOUT_SELECT_CLASS}`,
      `button.${NO_CHECKOUT_CHOICE_CLASS}`,
      `details.${NO_CHECKOUT_DETAILS_CLASS}`,
    ])
    expect(drawn(root)).not.toContain(NO_CHECKOUT_AUTO_SENTENCE)
    expect(one(body, CHECKOUT_SELECT_CLASS).childNodes().map((option) => option.attributes.get('value'))).toEqual([
      ORBIT.path,
    ])
    expect(action.kind).toBe('choose')
  })

  it('draws the console’s own sentence when the refusal cannot be read', () => {
    // `sanitizeRefusal` refuses a sentence past its bound; the card then says so
    // in the console's words rather than drawing the value it refused.
    const { root } = theCard('x'.repeat(MAX_TEXT_INPUT_LENGTH + 1))
    expect(theDisclosure(root).textContent).toBe(REFUSAL_UNREADABLE)
  })

  it('takes its sentence, its words and its hooks from the places that already own them', () => {
    // The channel between the CLI's refusal and this card is the sentence
    // itself (`ConsoleReply` carries no code), so the two spells of it have to
    // be one string; the rest are the issue view's own words and hooks, because
    // one action is not two labels.
    expect(NO_CHECKOUT_SENTENCE).toBe(NO_PLAN_CHECKOUT)
    expect(NO_CHECKOUT_CHOICE_LABEL).toBe(ISSUE_CHECKOUT_LEAD)
    expect(NO_CHECKOUT_USE_LABEL).toBe(ISSUE_CHECKOUT_LABEL)
    expect(NO_CHECKOUT_DETAILS_LABEL).toBe(ISSUE_DETAILS_LABEL)
    expect(NO_CHECKOUT_CHOICE_CLASS).toBe(ISSUE_CHECKOUT_CLASS)
    expect(NO_CHECKOUT_LEAD_CLASS).toBe(ISSUE_CHECKOUT_LEAD_CLASS)
    expect(NO_CHECKOUT_OPERATION).toBe('plan.status')
    expect(NO_CHECKOUT_HEADLINE).toContain('checkout')
  })
})
