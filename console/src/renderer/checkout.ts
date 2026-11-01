// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The checkout chooser's menu (console 0.118.0 "Plum").
//
// One control, drawn in two places: the installation issue view's actions
// (`entry.ts`'s `drawIssue`) and the Graph's no-checkout card
// (`screens/nocheckout.ts`). Both offer **the same list**, because both are
// answering the same question -- which of this installation's repositories is
// the console to read its plans from -- and the owner's ask is one control for
// it: "an installation can have multiple repos, I should be able to pick one
// with a dropdown".
//
// **What may be offered is decided in the main process and not here.** The list
// this module draws is `InstallationsListRow.checkoutChoices`, built from the
// CLI's own `config list --json` document: the rows it re-validated *and*
// enrolled, one per repository, with the tool-owned and temporary directories
// dropped. A path that is not in that list is refused by the main process before
// anything is written (design section 3.4 rule 7: a `valid: true` row may be a
// child's working directory "and for nothing else"), so this file chooses a
// label and nothing else -- it never composes a path, joins one or invents one.
//
// **The label is text and the value is the path.** Each `<option>` carries the
// path verbatim as its `value`, because that string is what the main process
// matches against its own list and what `installation.checkout` sends back; the
// text a reader sees goes through `sanitizeRefusal`, so a repository or a
// directory named with markup, a control character or a bidi override arrives as
// characters and never as a tree, a second element or an attribute. It is the
// same split `screens/launch.ts` makes for the repository identities, for the
// same reason: a label is display text and a value is a value.

import { sanitizeRefusal } from '../shared/sanitize'
import type { ElementLike } from './app'

/** CheckoutElement and CheckoutFactory are the drawing surface, declared here as
 * every screen declares its own. */
export interface CheckoutElement extends ElementLike {
  readonly tag: string
  append(child: CheckoutElement): void
}

export interface CheckoutFactory {
  create(tag: string): CheckoutElement
}

/** CHECKOUT_SELECT_CLASS is the menu's hook. It is the one this file draws and
 * the theme has a rule for (§2.1 of `docs/design/console-styling.md`). */
export const CHECKOUT_SELECT_CLASS = 'installation-checkout-select'

/** CHECKOUT_SELECT_LABEL is the control's accessible name. A `select` beside a
 * paragraph of prose has no `<label>` to take one from, and a menu a screen
 * reader announces as "combo box" with no name is a control the operator cannot
 * tell from any other. */
export const CHECKOUT_SELECT_LABEL = 'Checkout directory'

/** CHECKOUT_UNREADABLE is what a repository or a directory the console will not
 * draw is replaced by. It is a sentence and not an empty cell, for the reason
 * `putText`'s own mark is: a blank menu entry is one nobody can tell from an
 * entry whose name is blank. */
export const CHECKOUT_UNREADABLE = 'a checkout the console will not name'

/** CheckoutChoice is one menu entry as the main process sent it: the repository
 * the path is a checkout of, the absolute path itself, and how many further
 * directories this installation records for the same repository. */
export interface CheckoutChoice {
  readonly repository: string
  readonly path: string
  readonly more: number
}

/** labelOf is one document string as this file draws it: through the console's
 * sanitizer, or the marker for a value it refuses. */
function labelOf(raw: string, field: string): string {
  const result = sanitizeRefusal(raw, field)
  return result.ok ? result.text : CHECKOUT_UNREADABLE
}

/** shortPath is a directory as a menu entry names it: its last two segments,
 * with an ellipsis for everything above them.
 *
 * A menu entry is a line, and the whole of a deep path is the part an operator
 * does not need: the machine's home directory and its user name are the two
 * things a screenshot should not carry, and two checkouts of one repository
 * almost always differ in their last segment. A path with fewer than two
 * segments is drawn as it is -- there is nothing to cut. */
function shortPath(path: string): string {
  const segments = path.split('/').filter((segment) => segment !== '')
  if (segments.length <= 2) return path
  return `…/${segments.slice(-2).join('/')}`
}

/**
 * checkoutLabel is one menu entry's text: the repository, then the short path,
 * then the count of directories it stands for.
 *
 * The count is a sentence rather than a second entry because the menu is one
 * entry per repository by construction (`checkoutChoicesOf`, `src/main/picker.ts`):
 * an installation with five directories of one repository would otherwise fill
 * the menu with that repository and hide the others.
 */
export function checkoutLabel(choice: CheckoutChoice): string {
  const repository = labelOf(choice.repository, 'the checkout’s repository')
  const path = shortPath(labelOf(choice.path, 'the checkout’s directory'))
  const more = choice.more > 0 ? ` (+${String(choice.more)} more)` : ''
  return `${repository} — ${path}${more}`
}

/**
 * renderCheckoutSelect draws the menu: one `<option>` per choice, in the order
 * the main process sent them.
 *
 * Nothing is pre-selected. The browser takes the first option, which is the
 * installation's own first repository, and a screen that marked one would be
 * choosing a directory the operator has not -- the entry reads the control's
 * value back when the button beside it is pressed, and the value of an untouched
 * `select` is its first option.
 */
export function renderCheckoutSelect<E extends CheckoutElement>(
  factory: { create(tag: string): E },
  choices: readonly CheckoutChoice[],
): E {
  const select = factory.create('select')
  select.setAttribute('class', CHECKOUT_SELECT_CLASS)
  select.setAttribute('aria-label', CHECKOUT_SELECT_LABEL)
  for (const choice of choices) {
    const option = factory.create('option')
    // The path as it came: this attribute is the string the main process matches
    // against the list it built, and a folded or redacted copy would be a
    // directory it refuses.
    option.setAttribute('value', choice.path)
    option.textContent = checkoutLabel(choice)
    select.append(option)
  }
  return select
}
