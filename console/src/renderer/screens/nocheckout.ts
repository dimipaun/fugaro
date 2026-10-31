// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The Graph's state when the installation has no checkout (console 0.116.1).
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
// words, offers the one action -- the picker's checkout chooser, which is the
// same door the dashboard's rail opens -- and keeps the CLI's sentence
// **verbatim** behind a `details` disclosure, the owner's ruling of 2026-10-25
// applied to this state as well: the raw text is one click away and never in
// the way.
//
// The words are this file's own, and the truth is not weakened: the console
// cannot read a plan from a directory the installation does not name, and the
// sentence says which read it is about rather than leaving the operator to
// guess why the Graph alone is empty.

import { sanitizeRefusal } from '../../shared/sanitize'
import type { SkewTheme } from '../../shared/version'
import type { ElementLike } from '../app'
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

/** NO_CHECKOUT_CHOICE_LABEL is the card's one action, in the words the
 * installation issue view already uses for the same door (`ISSUE_CHECKOUT_LEAD`,
 * `src/renderer/entry.ts`), and `entry-wiring.test.ts` asserts the two are the
 * same word: one action is not two labels. */
export const NO_CHECKOUT_CHOICE_LABEL = 'Choose a checkout'

/** NO_CHECKOUT_DETAILS_LABEL is the disclosure's summary, and it is the issue
 * view's own word for the same disclosure (`ISSUE_DETAILS_LABEL`): what is
 * behind it is the CLI's text, and the word says that and nothing else. */
export const NO_CHECKOUT_DETAILS_LABEL = 'details'

/**
 * NO_CHECKOUT_EMPTY_SENTENCE is what the card says **instead of** its action for
 * an installation the CLI lists no checkout for.
 *
 * The action opens the installation picker, and the picker's only word about a
 * checkout is one `Use <dir>` button per checkout the CLI listed for a row
 * (`src/renderer/installations.ts`): there is no folder dialog and no path to
 * type. So for a row with none, the button opens a window that cannot resolve
 * this state, and a card that offered it would be an action that leads nowhere
 * -- the operator clicks, the window opens, and nothing there is about the
 * missing checkout. The honest thing is the state and the two commands that
 * record one, which is what this sentence says.
 */
export const NO_CHECKOUT_EMPTY_SENTENCE =
  'No checkout is recorded for this installation yet: run `fugaro config init` or a `fugaro run` from a checkout of its repository once, then reopen this.'

/** NoCheckoutOptions is what the caller knows and this screen cannot: whether
 * the installation lists a checkout the picker would offer. */
export interface NoCheckoutOptions {
  /**
   * `choosable` is whether the CLI's own list for this installation holds a
   * checkout, which is exactly the fact the picker's `Use <dir>` buttons are
   * drawn from.
   *
   * It is **not** `checkouts.length > 0` asked of a row this console built
   * itself: the dashboard's fallback row for a list read that failed carries an
   * empty list because nothing was read, not because the CLI listed none, and
   * the card must not say "no checkout is recorded" about a list this console
   * never saw.
   */
  readonly choosable: boolean
}

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

/** make and literal are this file's own drawing helpers, as every screen's are:
 * `literal` is the only writer of a text slot, and every argument it is handed
 * is either this file's own constant or the sentence `sanitizeRefusal` produced
 * (so `screen-literals.test.ts` holds both halves -- no document string reaches
 * it, and no other spelling writes the slot). */
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

/** DrawnNoCheckout is the card and the button the entry wires: the screen draws
 * the action and attaches no listener, because a listener needs the document
 * and only the entry has one (`DrawnNotice`'s rule, `src/shared/notice.ts`). */
export interface DrawnNoCheckout {
  readonly root: ScreenElement
  /** choice is the card's one action, or `null` for an installation that lists
   * no checkout at all: see `NO_CHECKOUT_EMPTY_SENTENCE`. */
  readonly choice: ScreenElement | null
}

/**
 * renderNoCheckout draws the Graph's no-checkout state.
 *
 * The CLI's sentence is drawn behind the disclosure and **not** instead of the
 * card: this file is a state the console can name, so it names it, and it keeps
 * the refusal's own words for the reader who wants the CLI's account of it. The
 * text goes through `sanitizeRefusal` for the reason `degraded.ts` gives -- the
 * remedy survives verbatim, a token a child echoed is still redacted -- and then
 * through `literal`, so markup, control and bidi characters are text and never a
 * tree.
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
  // The action and the sentence that stands in for it are one cell of the card
  // either way: the operator is told what to do next, in the console's own words
  // or in the one command that records a checkout.
  let choice: ScreenElement | null = null
  if (options.choosable) {
    choice = cell(body, factory, 'button', NO_CHECKOUT_CHOICE_CLASS)
    choice.setAttribute('type', 'button')
    literal(choice, NO_CHECKOUT_CHOICE_LABEL)
  } else {
    // It wears the card's own sentence hook (`unavailable-sentence`, the class
    // the CLI's text behind the disclosure uses) rather than a class of its own:
    // it is a sentence in this card, and a hook the theme has no rule for would
    // draw it as bare text beside the styled one.
    literal(cell(body, factory, 'p', NO_CHECKOUT_SENTENCE_CLASS), NO_CHECKOUT_EMPTY_SENTENCE)
  }

  const details = cell(body, factory, 'details', NO_CHECKOUT_DETAILS_CLASS)
  literal(cell(details, factory, 'summary', NO_CHECKOUT_SUMMARY_CLASS), NO_CHECKOUT_DETAILS_LABEL)
  const drawn = sanitizeRefusal(sentence, 'the CLI refusal sentence')
  literal(
    cell(details, factory, 'p', NO_CHECKOUT_SENTENCE_CLASS),
    drawn.ok ? drawn.text : REFUSAL_UNREADABLE,
  )
  return { root, choice }
}
