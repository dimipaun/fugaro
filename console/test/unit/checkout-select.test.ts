// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The checkout chooser's menu (console 0.118.0 "Plum").
//
// The menu is one control drawn in two places -- the installation issue view and
// the Graph's no-checkout card -- and this file holds the half both share: what
// an entry says, and what it carries.
//
// The two facts it exists for are the two halves of one `<option>`:
//
//   - **the label is text.** A repository identity and a directory are both
//     strings the CLI printed, so markup, control characters and bidi overrides
//     arrive as characters a reader sees and never as an element, a second cell
//     or an attribute (`sanitize.ts`'s reason for existing).
//   - **the value is the path verbatim.** That string is what the main process
//     matches against the list it built, so a folded or redacted copy would be a
//     directory it refuses -- the same split `screens/launch.ts` makes for the
//     repository identities.
//
// The project names and paths are invented (`owner/orbit`, `/home/operator/src`).

import { describe, expect, it } from 'vitest'

import {
  CHECKOUT_SELECT_CLASS,
  CHECKOUT_SELECT_LABEL,
  CHECKOUT_UNREADABLE,
  checkoutLabel,
  renderCheckoutSelect,
} from '../../src/renderer/checkout'
import { INVISIBLE_CLASS, MAX_TEXT_INPUT_LENGTH } from '../../src/shared/sanitize'

// ---------------------------------------------------------------------------
// The fake screen: the same smallest surface `no-checkout.test.ts` draws into,
// with the same refusal of every attribute that could make a window do
// something.
// ---------------------------------------------------------------------------

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
}

const factory = { create: (tag: string): FakeScreen => new FakeScreen(tag) }

function walk(node: FakeScreen): readonly FakeScreen[] {
  const out: FakeScreen[] = [node]
  for (const child of node.nodes) out.push(...walk(child))
  return out
}

/** drawn is a tree's text, in document order. */
function drawn(root: FakeScreen): string {
  return walk(root)
    .map((node) => node.textContent ?? '')
    .filter((text) => text !== '')
    .join(' ')
}

/** shape is the tree's tags and classes, in document order: what a label can
 * only change if it was read as markup rather than as text. */
function shape(root: FakeScreen): readonly string[] {
  return walk(root).map((node) => `${node.tag}.${node.attributes.get('class') ?? ''}`)
}

const PLAIN = { repository: 'github/owner/orbit', path: '/home/operator/src/orbit', more: 0 }

describe('the checkout chooser’s menu', () => {
  it('draws one option per choice, with the path as the value and a short label as the text', () => {
    const select = renderCheckoutSelect(factory, [
      PLAIN,
      { repository: 'github/owner/aurora', path: '/home/operator/src/aurora', more: 0 },
    ]) as FakeScreen

    expect(select.tag).toBe('select')
    expect(select.attributes.get('class')).toBe(CHECKOUT_SELECT_CLASS)
    // A control beside a paragraph of prose has no `<label>` to take a name
    // from, so it carries one.
    expect(select.attributes.get('aria-label')).toBe(CHECKOUT_SELECT_LABEL)
    expect(shape(select)).toEqual(['select.installation-checkout-select', 'option.', 'option.'])
    const options = walk(select).filter((node) => node.tag === 'option')
    expect(options.map((option) => option.attributes.get('value'))).toEqual([
      '/home/operator/src/orbit',
      '/home/operator/src/aurora',
    ])
    // The label is the repository and the **last two** segments: the machine's
    // home directory and its user name are the part a menu entry does not need.
    expect(options.map((option) => option.textContent)).toEqual([
      'github/owner/orbit — …/src/orbit',
      'github/owner/aurora — …/src/aurora',
    ])
  })

  it('says how many directories a repository stands for, and only when there is more than one', () => {
    expect(checkoutLabel({ ...PLAIN, more: 1 })).toBe('github/owner/orbit — …/src/orbit (+1 more)')
    expect(checkoutLabel({ ...PLAIN, more: 4 })).toBe('github/owner/orbit — …/src/orbit (+4 more)')
    // A negative or a fractional count is not a count this console wrote; it is
    // drawn as none rather than as a sentence about a number of directories.
    expect(checkoutLabel({ ...PLAIN, more: -1 })).toBe('github/owner/orbit — …/src/orbit')
    // A shallow path has nothing to cut, and a label is not made worse by the
    // ellipsis it would need.
    expect(checkoutLabel({ repository: 'github/owner/orbit', path: '/src/orbit', more: 0 })).toBe(
      'github/owner/orbit — /src/orbit',
    )
  })

  it('draws a hostile repository and directory as text, never as a tree', () => {
    // Both strings are the CLI's own -- the identity it recorded and the path it
    // printed -- so both are exactly the untrusted text this console sanitizes.
    const hostile = {
      repository: '<img src=x onerror="alert(1)">/owner/<script>alert(2)</script>',
      path: '/home/operator/src/‮evil‬/scratch\u0000name',
      more: 0,
    }
    const select = renderCheckoutSelect(factory, [hostile]) as FakeScreen

    // The same tree the plain choice drew: nothing in either string became an
    // element, an attribute or a second cell.
    expect(shape(select)).toEqual(shape(renderCheckoutSelect(factory, [PLAIN]) as FakeScreen))
    const option = walk(select).filter((node) => node.tag === 'option')[0]
    expect(option?.textContent).toContain('<img src=x onerror="alert(1)">')
    expect(option?.textContent).toContain('<script>alert(2)</script>')
    // ...and the characters that could reorder or hide what is around them are
    // gone rather than drawn: the label is one line of visible text.
    expect(new RegExp(INVISIBLE_CLASS, 'gu').test(drawn(select)), 'an invisible character reached the menu').toBe(false)
    expect(drawn(select)).not.toContain('\u0000')
    // The only attributes either element carries are the menu's own three and
    // the option's one: the repository and the directory wrote none.
    expect([...select.attributes]).toEqual([
      ['class', CHECKOUT_SELECT_CLASS],
      ['aria-label', CHECKOUT_SELECT_LABEL],
    ])
    expect([...(option?.attributes ?? [])]).toEqual([['value', hostile.path]])
  })

  it('names a value it refuses rather than drawing an empty entry', () => {
    // `sanitizeRefusal` refuses a string past its bound; the entry then says so
    // in the console's own words, because a blank menu entry is one nobody can
    // tell from an entry whose name is blank.
    const long = 'x'.repeat(MAX_TEXT_INPUT_LENGTH + 1)
    const label = checkoutLabel({ repository: long, path: long, more: 0 })
    expect(label).toContain(CHECKOUT_UNREADABLE)
    expect(label).not.toContain('x'.repeat(10))
    // ...and the value is still the path exactly as it arrived, so a directory
    // whose *label* the console will not draw is still one it can send back.
    const select = renderCheckoutSelect(factory, [{ repository: long, path: long, more: 0 }]) as FakeScreen
    const option = walk(select).filter((node) => node.tag === 'option')[0]
    expect(option?.attributes.get('value')).toBe(long)
  })

  it('draws no option at all for an empty list, and the control still carries its own name', () => {
    // The empty case is the card's and not this module's -- a menu with no entry
    // is never drawn -- but a control with nothing in it must still be a control
    // a screen reader can name, or the caller has drawn a hole.
    const select = renderCheckoutSelect(factory, []) as FakeScreen
    expect(shape(select)).toEqual(['select.installation-checkout-select'])
    expect(select.attributes.get('aria-label')).toBe(CHECKOUT_SELECT_LABEL)
  })
})
