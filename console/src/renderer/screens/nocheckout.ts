// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Graph's state when the installation has no checkout (console 0.116.1,
// and the chooser console 0.118.0 "Plum" added to it).
//
// The Graph is the console's only read with a precondition: `plan.status` reads
// the plan out of the installation's **checkout** (`src/source/cli.ts`), and an
// installation with none throws `NO_PLAN_CHECKOUT` before the CLI is asked. The
// read is refused, so the entry drew it as it draws every refusal -- "the CLI
// refused this read", the read's name, and the CLI's sentence -- and a reader
// who has just chosen an installation saw an error where the truth is that
// nothing is wrong: the plans have nowhere to be read from, and there is one
// thing to do about it.
//
// **So this card is not a refusal, and it is not the entry's refusal drawing.**
// It keeps the refusal's frame and its own calm tone (no `refused` kind, and so
// no warn rule on the frame), says what the state is in the console's own
// words, offers the one action, and keeps the CLI's sentence **verbatim** behind
// a `details` disclosure, the owner's ruling of 2026-10-25 applied to this state
// as well: the raw text is one click away and never in the way.
//
// **The one action is the chooser, and it is here rather than in the picker
// window** (owner's ask of 2026-10-31: "an installation can have multiple repos,
// I should be able to pick one with a dropdown"). An installation with several
// repositories is the ordinary case, so the card draws the menu the issue view
// draws -- `src/renderer/checkout.ts`, one entry per repository the CLI
// re-validated and enrolled -- and sends the directory it has selected through
// the same `installation.checkout` the picker window uses. When exactly one
// checkout exists the console chooses it by itself and says so once, because a
// menu of one entry is a control with nothing to choose; when the CLI listed
// none the card says what records one; and when this console never read the
// CLI's list at all it keeps the picker's door, because "no checkout is
// recorded" is a claim it cannot make.
//
// The words are this file's own, and the truth is not weakened: the console
// cannot read a plan from a directory the installation does not name, and the
// sentence says which read it is about rather than leaving the operator to
// guess why the Graph alone is empty.

import { sanitizeRefusal } from '../../shared/sanitize'
import type { SkewTheme } from '../../shared/version'
import type { ElementLike } from '../app'
import { checkoutLabel, renderCheckoutSelect } from '../checkout'
import type { CheckoutChoice } from '../checkout'
import { REFUSAL_UNREADABLE } from './degraded'

/** ScreenElement and ScreenFactory are the drawing surface, declared here as
 * every screen declares its own. */
export interface ScreenElement extends ElementLike {
  readonly tag: string
  append(child: ScreenElement): void
  childNodes(): readonly ScreenElement[]
}

export interface ScreenFactory {
  create(tag: string): ScreenElement
}

/** NO_CHECKOUT_KIND is the frame's kind. `refused` is the kind the theme paints
 * (design section 2.8); this one is a state of the installation and not a
 * failure of the read, so it draws the frame's geometry and none of its tone. */
export const NO_CHECKOUT_KIND = 'no-checkout'

/** NO_CHECKOUT_HEADLINE is the card. It is two sentences because the state is
 * two facts: what the Graph reads (a checkout, not a database, not a run list)
 * and what this installation has (none chosen). A single "no checkout" would
 * leave the operator to work out which of the console's five views it breaks. */
export const NO_CHECKOUT_HEADLINE =
  'The Graph reads your plans from a repository checkout. This installation has none chosen.'

/** NO_CHECKOUT_CHOICE_LABEL is the card's word for the chooser, in the words the
 * installation issue view already uses for the same door (`ISSUE_CHECKOUT_LEAD`,
 * `src/renderer/entry.ts`), and `entry-wiring.test.ts` asserts the two are the
 * same word: one action is not two labels. It is the `picker` state's button and
 * the lead above the menu, which are the same word for the same door. */
export const NO_CHECKOUT_CHOICE_LABEL = 'Choose a checkout'

/** NO_CHECKOUT_USE_LABEL is the button under the menu, and it is the issue
 * view's own label for the same control (`ISSUE_CHECKOUT_LABEL`): one action is
 * not two labels. The two files cannot import from each other -- `entry.ts` is
 * what draws this card -- so the word is stated here and the equality is a test
 * (`no-checkout.test.ts`). */
export const NO_CHECKOUT_USE_LABEL = 'Use this checkout'

/** NO_CHECKOUT_AUTO_SENTENCE is what the card says when the installation records
 * **exactly one** usable checkout: the console chooses it by itself and says so
 * once, because a menu with one entry is a control that can only be pressed one
 * way. The entry appends the entry's own label, so the sentence names the
 * directory the Graph is reading rather than merely counting it. */
export const NO_CHECKOUT_AUTO_SENTENCE =
  'This installation records one checkout, so the Graph is reading it:'

/** NO_CHECKOUT_DETAILS_LABEL is the disclosure's summary, and it is the issue
 * view's own word for the same disclosure (`ISSUE_DETAILS_LABEL`): what is
 * behind it is the CLI's text, and the word says that and nothing else. */
export const NO_CHECKOUT_DETAILS_LABEL = 'details'

/**
 * NO_CHECKOUT_EMPTY_SENTENCE is what the card says **instead of** its action for
 * an installation the CLI lists no usable checkout for.
 *
 * The action is a chooser drawn from the CLI's own list, and a card that offered
 * it for a row with nothing in that list would be an action that leads nowhere.
 * The honest thing is the state and the two commands that record a checkout,
 * which is what this sentence says.
 */
export const NO_CHECKOUT_EMPTY_SENTENCE =
  'No checkout is recorded for this installation yet: run `fugaro config init` or a `fugaro run` from a checkout of its repository once, then reopen this.'

/** NoCheckoutOptions is what the caller knows and this screen cannot: which of
 * this installation's checkouts the console may offer, and whether it read the
 * CLI's list at all. */
export interface NoCheckoutOptions {
  /**
   * `choices` is the menu's own list -- `RailRow.checkoutChoices`, one entry per
   * repository the CLI re-validated **and** enrolled (`src/main/picker.ts`).
   *
   * It is **not** `row.checkouts`, which is the picker window's list and carries
   * a path with no repository to label it by. An empty list here is one of the
   * states this card is drawn for, and a path that is not in this list is one the
   * main process refuses before anything is written.
   */
  readonly choices: readonly CheckoutChoice[]
  /**
   * `listed` is whether the console read the CLI's list at all. The dashboard's
   * fallback row for a list read that failed carries an empty `choices` because
   * nothing was read, not because the CLI listed none, and the card must not say
   * "no checkout is recorded" about a list this console never saw: for that one
   * it keeps the picker's door, which is the only action it can honestly offer.
   */
  readonly listed: boolean
  /**
   * `auto` is whether the console may choose the single checkout by itself.
   *
   * The entry clears it for a project it has already chosen for: the sentence
   * this state draws says the Graph **is reading** the directory, and a console
   * that said that a second time about a directory it had already sent and not
   * been answered for would be repeating a claim that did not hold. With `auto`
   * false the one checkout is drawn as the menu it is, and the operator presses
   * the button -- which is the same control, offered deliberately.
   */
  readonly auto: boolean
}

/** NoCheckoutAction is what the card drew for the operator to do, and it is the
 * whole of what the entry has to wire: a closed four, because there is no fifth
 * thing this state can be. */
export type NoCheckoutAction =
  /** the picker window's door: the list was not read, so the card claims nothing */
  | { readonly kind: 'picker'; readonly button: ScreenElement }
  /** the CLI listed none: the console's sentence, and no control at all */
  | { readonly kind: 'empty' }
  /** exactly one usable checkout: the console is choosing it by itself */
  | { readonly kind: 'auto'; readonly choice: CheckoutChoice }
  /** the menu, and the button that sends the directory it has selected */
  | { readonly kind: 'choose'; readonly select: ScreenElement; readonly button: ScreenElement }

/** NO_CHECKOUT_OPERATION is the read this state is about. It names the operation
 * in the tree rather than on the card: nothing is drawn from it, and a reader of
 * the document is owed the fact that this is the Graph's own precondition
 * instead of a state of the installation as a whole. */
export const NO_CHECKOUT_OPERATION = 'plan.status'

/** NO_CHECKOUT_SENTENCE is `src/source/source.ts`'s `NO_PLAN_CHECKOUT`, which
 * is the sentence the refused `plan.status` read carries when the installation
 * records no checkout, and the entry draws this card for exactly that sentence
 * and nothing else.
 *
 * It is **stated here rather than imported** for the reason `degraded.ts` gives
 * for its own shape: the renderer may not reach a module that spawns, and
 * `source.ts` imports the main process's spawn and IPC modules. The sentence is
 * the whole of the channel between the two -- a refused read crosses the
 * boundary as text (`ConsoleReply` carries a `reason` and no code) -- so the two
 * constants have to be one string, and `entry-wiring.test.ts` asserts they are:
 * a reworded refusal on the CLI's side fails that test rather than silently
 * bringing the raw refusal card back. */
export const NO_CHECKOUT_SENTENCE =
  'this installation records no checkout, and the plan read is about its docs/ directory'

/** NO_CHECKOUT_CHOICE_CLASS is the action's hook. It is the issue view's own
 * checkout-button class (§2.1) rather than a new one: the theme has a rule for
 * a checkout control and this is that control, and a hook nothing styles would
 * draw the card's one action as bare text. */
export const NO_CHECKOUT_CHOICE_CLASS = 'installation-issue-checkout'

/** NO_CHECKOUT_DETAILS_CLASS, NO_CHECKOUT_SUMMARY_CLASS and
 * NO_CHECKOUT_SENTENCE_CLASS are the disclosure's three hooks, likewise the
 * issue view's and the refusal screen's own (§2.1, §2.8). */
export const NO_CHECKOUT_DETAILS_CLASS = 'installation-issue-details'
export const NO_CHECKOUT_SUMMARY_CLASS = 'installation-issue-summary'
export const NO_CHECKOUT_SENTENCE_CLASS = 'unavailable-sentence'

/** NO_CHECKOUT_LEAD_CLASS is the word above the menu, and it is the issue
 * view's own lead class for the same control (`ISSUE_CHECKOUT_LEAD_CLASS`): the
 * card draws the same chooser, so it draws it with the same hook. */
export const NO_CHECKOUT_LEAD_CLASS = 'installation-issue-checkout-lead'

/** make and literal are this file's own drawing helpers, as every screen's are:
 * `literal` is the only writer of a text slot, and every argument it is handed
 * is either this file's own constant, the sentence `sanitizeRefusal` produced,
 * or an entry's own label (`checkoutLabel`, `src/renderer/checkout.ts`, which is
 * a sanitizer's answer for a repository and a directory) -- so
 * `screen-literals.test.ts` holds both halves: no document string reaches it
 * unread, and no other spelling writes the slot. */
function make(factory: ScreenFactory, tag: string, ...classes: string[]): ScreenElement {
  const element = factory.create(tag)
  element.setAttribute('class', classes.join(' '))
  return element
}

function literal(element: ScreenElement, text: string): ScreenElement {
  element.textContent = text
  return element
}

/** cell is a cell of this file's own making, appended to a parent. */
function cell(parent: ScreenElement, factory: ScreenFactory, tag: string, ...classes: string[]): ScreenElement {
  const element = make(factory, tag, ...classes)
  parent.append(element)
  return element
}

/** DrawnNoCheckout is the card and the action the entry wires: the screen draws
 * the controls and attaches no listener, because a listener needs the document
 * and only the entry has one (`DrawnNotice`'s rule, `src/shared/notice.ts`). */
export interface DrawnNoCheckout {
  readonly root: ScreenElement
  readonly action: NoCheckoutAction
}

/**
 * renderNoCheckout draws the Graph's no-checkout state.
 *
 * **Four states, and the operator is never shown a control that cannot answer
 * the one question this card asks** -- which of the installation's repositories
 * the Graph is to read from:
 *
 *   - the console did not read the CLI's list: the picker's door, and no claim
 *     about what the installation records;
 *   - the CLI listed no usable checkout: the console's own sentence, and the two
 *     commands that record one;
 *   - exactly one, and `auto`: the console chooses it by itself and says so
 *     once, rather than drawing a menu with a single entry;
 *   - otherwise: the menu, and the button that sends what it has selected.
 *
 * The CLI's sentence is drawn behind the disclosure and **not** instead of the
 * card: this file is a state the console can name, so it names it, and it keeps
 * the refusal's own words for the reader who wants the CLI's account of it. The
 * text goes through `sanitizeRefusal` for the reason `degraded.ts` gives -- the
 * remedy survives verbatim, a token a child echoed is still redacted -- and then
 * through `literal`, so markup, control and bidi characters are text and never a
 * tree. A menu entry's label is composed by `src/renderer/checkout.ts`, which
 * makes the same split for the same reason.
 */
export function renderNoCheckout(
  factory: ScreenFactory,
  sentence: string,
  theme: SkewTheme,
  options: NoCheckoutOptions,
): DrawnNoCheckout {
  const root = make(factory, 'section', 'console-unavailable')
  root.setAttribute('data-theme', theme)
  root.setAttribute('data-kind', NO_CHECKOUT_KIND)
  root.setAttribute('data-received-at', '')
  root.setAttribute('data-operation', NO_CHECKOUT_OPERATION)

  const body = cell(root, factory, 'div', 'unavailable-body')
  literal(cell(body, factory, 'h1', 'unavailable-headline'), NO_CHECKOUT_HEADLINE)

  let action: NoCheckoutAction
  const only = options.auto && options.choices.length === 1 ? options.choices[0] : undefined
  if (!options.listed) {
    const button = cell(body, factory, 'button', NO_CHECKOUT_CHOICE_CLASS)
    button.setAttribute('type', 'button')
    literal(button, NO_CHECKOUT_CHOICE_LABEL)
    action = { kind: 'picker', button }
  } else if (options.choices.length === 0) {
    // It wears the card's own sentence hook (`unavailable-sentence`, the class
    // the CLI's text behind the disclosure uses) rather than a class of its own:
    // it is a sentence in this card, and a hook the theme has no rule for would
    // draw it as bare text beside the styled one.
    literal(cell(body, factory, 'p', NO_CHECKOUT_SENTENCE_CLASS), NO_CHECKOUT_EMPTY_SENTENCE)
    action = { kind: 'empty' }
  } else if (only !== undefined) {
    // The one case the console decides for the operator, and the card is where
    // it says so: a menu of one entry is a control with nothing to choose, and
    // the sentence names the directory the Graph is reading.
    literal(
      cell(body, factory, 'p', NO_CHECKOUT_SENTENCE_CLASS),
      `${NO_CHECKOUT_AUTO_SENTENCE} ${checkoutLabel(only)}`,
    )
    action = { kind: 'auto', choice: only }
  } else {
    literal(cell(body, factory, 'p', NO_CHECKOUT_LEAD_CLASS), NO_CHECKOUT_CHOICE_LABEL)
    const select = renderCheckoutSelect(factory, options.choices)
    body.append(select)
    const button = cell(body, factory, 'button', NO_CHECKOUT_CHOICE_CLASS)
    button.setAttribute('type', 'button')
    literal(button, NO_CHECKOUT_USE_LABEL)
    action = { kind: 'choose', select, button }
  }

  const details = cell(body, factory, 'details', NO_CHECKOUT_DETAILS_CLASS)
  literal(cell(details, factory, 'summary', NO_CHECKOUT_SUMMARY_CLASS), NO_CHECKOUT_DETAILS_LABEL)
  const drawn = sanitizeRefusal(sentence, 'the CLI refusal sentence')
  literal(
    cell(details, factory, 'p', NO_CHECKOUT_SENTENCE_CLASS),
    drawn.ok ? drawn.text : REFUSAL_UNREADABLE,
  )
  return { root, action }
}
