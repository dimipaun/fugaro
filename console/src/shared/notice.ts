// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The notice card: one reusable, dismissible card a screen or the entry can
// draw, and the structure the version-skew state is drawn through (design
// section 2.14).
//
// **Why a card and not a block.** The version-skew state used to be a
// full-width strip at the top of the window: a bold heading, two run-together
// sentences, two label/value lines with no separator between the label and its
// value, and a bullet-less list of the views a reduced mode withholds. Every
// one of those was structural -- the label and the value were two `span`s in a
// `div` with no rule of their own, and the list was a `ul` under a global
// `list-style: none`. Drawing the state through a card whose facts are a real
// `dl` and whose list is a real `ul` removes the defects by construction, and it
// gives the one thing the strip had no room for: a way to put it away.
//
// **Why it is here and not in the renderer.** This module draws and imports no
// renderer module: the element and factory are declared structurally, exactly as
// `shared/version.ts` declares them, so `src/shared` still depends on no screen
// (`renderer/screens/degraded.ts` makes the mirror-image choice from the other
// side). The version-skew state is the console's own -- a fact about a version
// comparison and not about any document -- and the entry that mounts it is the
// only place that can wire it, so the card is drawn here, the listeners are
// added there (`renderer/entry.ts` has `nodeOf`).
//
// **Text goes in through one writer.** `literal` below is the only place a text
// slot is assigned, and every string it is handed is the console's own: the
// caller passes constants of its own or text a sanitizer already drew
// (`versionText`), never a document's value. `test/unit/screen-literals.test.ts`
// reads this file for exactly that reason.

/** NoticeElement is what this file draws into: the six DOM members the
 * renderer's writers use. It is declared structurally rather than imported --
 * the same choice, and the same shape, as `SkewElement` -- so a caller hands its
 * own elements here without a conversion and this module imports nothing from
 * the renderer. */
export interface NoticeElement {
  textContent: string | null
  setAttribute(name: string, value: string): void
  getAttribute(name: string): string | null
  removeAttribute(name: string): void
  readonly tag: string
  append(child: NoticeElement): void
}

/** NoticeFactory is where elements come from. */
export interface NoticeFactory {
  create(tag: string): NoticeElement
}

/** NoticeSeverity is the card's tone. There is no `error`: a notice is a fact
 * an operator has to know, and the reduced mode -- the one notice that ships --
 * is a console that still works, not an alarm. The severity is also drawn as a
 * **shape** (§3.5): the mark is a triangle with a `!` for a warning and a circle
 * with an `i` for information, so a reader who cannot tell the two tones apart
 * reads the same card. */
export type NoticeSeverity = 'info' | 'warning'

/** NoticeHeading is the level the title is drawn at. A notice in the flow is an
 * `h2` under the window's own `h1`; the refused notice is the whole document and
 * keeps the one `h1`. */
export type NoticeHeading = 'h1' | 'h2'

/** NoticeDisclosure is whether the details are folded behind a button or drawn
 * open. A folded notice keeps the technical text one click away; an open one is
 * a notice whose details *are* the point (the refusal screen, where the range
 * and the version line are what the operator acts on). */
export type NoticeDisclosure = 'toggle' | 'expanded'

/** NoticeListItem is one row of a list value: the text, and any hooks the
 * caller's own vocabulary stamps on the `li` (the skew notice marks each row
 * with the view it names). */
export interface NoticeListItem {
  readonly text: string
  readonly classes?: readonly string[]
  readonly attributes?: Readonly<Record<string, string>>
}

/** NoticeDetail is one `dt`/`dd` row. A row's value is a run of text or a real
 * `ul` -- never a label concatenated with its value, which is what the strip
 * this card replaces drew. */
export interface NoticeDetail {
  /** the `dt`: the console's own word for what the row is */
  readonly label: string
  /** hooks the `dt` carries, when the caller's vocabulary has its own */
  readonly labelClasses?: readonly string[]
  /** the `dd`'s one run of text; left out for a row whose value is a `list` */
  readonly text?: string
  /** a real `ul` inside the `dd`, one `li` per entry */
  readonly list?: readonly NoticeListItem[]
  /** hooks the `dd` carries (`skew-sentence`, `skew-because`) */
  readonly classes?: readonly string[]
}

/** NoticeSpec is what a caller says about a notice. */
export interface NoticeSpec {
  readonly severity: NoticeSeverity
  readonly dismissible: boolean
  readonly heading: NoticeHeading
  readonly disclosure: NoticeDisclosure
  /** the card's title, the console's own text */
  readonly title: string
  /** the reason: one plain paragraph in the console's voice, the console's own
   * text. It is what the reader has to know and to do, and the technical text
   * belongs in `details` */
  readonly reason: string
  /** hooks the title carries, when the caller's vocabulary has its own */
  readonly titleClasses?: readonly string[]
  /** hooks the reason paragraph carries, when the caller's vocabulary has its
   * own */
  readonly reasonClasses?: readonly string[]
  readonly details: readonly NoticeDetail[]
  /** extra root classes beside `notice`, e.g. `console-skew` */
  readonly classes: readonly string[]
}

/** DrawnNotice is what a caller needs to wire the card: the root it mounts, the
 * title and toggle a disclosure drives, and the dismiss button (or `null` when
 * the notice is not dismissible). No listener is attached here -- a listener
 * needs the document, which only the entry has. */
export interface DrawnNotice {
  readonly root: NoticeElement
  readonly title: NoticeElement
  /** the region the toggle folds, and the one a dismissal leaves alone */
  readonly details: NoticeElement
  readonly toggle: NoticeElement | null
  readonly dismiss: NoticeElement | null
}

/** NOTICE_SHOW_DETAILS and NOTICE_HIDE_DETAILS are the disclosure button's two
 * labels, and they are constants because the caller flips them: the button's
 * word is the console's own and the two spellings are one decision. */
export const NOTICE_SHOW_DETAILS = 'Show details'
export const NOTICE_HIDE_DETAILS = 'Hide details'

/** NOTICE_DISMISS_LABEL is the dismiss button's accessible name: the button
 * draws a `×`, which is decoration, so the name is what says what it does. */
export const NOTICE_DISMISS_LABEL = 'Dismiss notice'

const NOTICE_ROOT_CLASS = 'notice'
const NOTICE_TITLE_ID = 'notice-title'
const NOTICE_DETAILS_ID = 'notice-details'
const NOTICE_DISMISS_GLYPH = '×'

/** literal writes the console's own vocabulary into an element. Every argument
 * is a string literal, a constant of this file, or text the caller's own
 * sanitizer already drew. */
function literal(element: NoticeElement, text: string): NoticeElement {
  element.textContent = text
  return element
}

/** cell is a fresh element with its classes, appended to a parent. */
function cell(
  parent: NoticeElement,
  factory: NoticeFactory,
  tag: string,
  classes: readonly string[],
): NoticeElement {
  const element = factory.create(tag)
  element.setAttribute('class', classes.join(' '))
  parent.append(element)
  return element
}

/** attribute stamps one attribute per entry. It is a loop rather than a
 * per-row write so a row's hooks are one list the caller owns; the values are
 * the caller's own closed vocabulary (`ReducedView`), never a document's. */
function attribute(element: NoticeElement, name: string, value: string): void {
  element.setAttribute(name, value)
}

/** listItem is one `li` of a list value, with the hooks its caller stamped. */
function listItem(parent: NoticeElement, factory: NoticeFactory, item: NoticeListItem): void {
  const element = cell(parent, factory, 'li', item.classes ?? [])
  for (const [name, value] of Object.entries(item.attributes ?? {})) attribute(element, name, value)
  literal(element, item.text)
}

/** fact is one `dt`/`dd` row of the facts list. */
function fact(parent: NoticeElement, factory: NoticeFactory, detail: NoticeDetail): void {
  literal(cell(parent, factory, 'dt', detail.labelClasses ?? []), detail.label)
  const value = cell(parent, factory, 'dd', detail.classes ?? [])
  if (detail.text !== undefined) literal(value, detail.text)
  if (detail.list === undefined) return
  const list = cell(value, factory, 'ul', [])
  for (const item of detail.list) listItem(list, factory, item)
}

/**
 * renderNotice draws the card and hands back the parts a caller wires. It
 * attaches no listener: a click needs a document, and this module has none.
 *
 * The root is a `section` carrying `notice` and the caller's own extra class, a
 * `span.notice-mark` drawn entirely in CSS (the severity's shape, so the tone is
 * never the only carrier), a `div.notice-body` holding the title, the reason,
 * the disclosure button and the details, and -- when the notice is dismissible --
 * a `button.notice-dismiss`.
 *
 * The details are a `dl.notice-facts`: a real definition list, so a label and
 * its value are two elements rather than one run of text, and a list value is a
 * real `ul` inside its `dd`, so it draws bullets without a rule of its own.
 */
export function renderNotice(factory: NoticeFactory, spec: NoticeSpec): DrawnNotice {
  const root = factory.create('section')
  // One attribute, written twice, and the first write is the one the theme's
  // selector scan can read: `test/unit/theme-selectors.test.ts` licenses a rule
  // by reading the class a `factory.create` is stamped with, and a list it cannot
  // evaluate would leave `section.notice` unlicensed while the card drew it.
  root.setAttribute('class', NOTICE_ROOT_CLASS)
  if (spec.classes.length > 0) {
    root.setAttribute('class', [NOTICE_ROOT_CLASS, ...spec.classes].join(' '))
  }
  root.setAttribute('data-severity', spec.severity)
  root.setAttribute('aria-labelledby', NOTICE_TITLE_ID)

  const mark = cell(root, factory, 'span', ['notice-mark'])
  mark.setAttribute('aria-hidden', 'true')

  const body = cell(root, factory, 'div', ['notice-body'])
  const title = cell(body, factory, spec.heading, ['notice-title', ...(spec.titleClasses ?? [])])
  title.setAttribute('id', NOTICE_TITLE_ID)
  // The chip on the header hands the keyboard back here when it un-hides the
  // notice, and a heading is not focusable without it.
  title.setAttribute('tabindex', '-1')
  literal(title, spec.title)

  literal(cell(body, factory, 'p', ['notice-reason', ...(spec.reasonClasses ?? [])]), spec.reason)

  let toggle: NoticeElement | null = null
  if (spec.disclosure === 'toggle') {
    toggle = cell(body, factory, 'button', ['notice-toggle'])
    toggle.setAttribute('type', 'button')
    toggle.setAttribute('aria-expanded', 'false')
    toggle.setAttribute('aria-controls', NOTICE_DETAILS_ID)
    literal(toggle, NOTICE_SHOW_DETAILS)
  }

  const details = cell(body, factory, 'div', ['notice-details'])
  details.setAttribute('id', NOTICE_DETAILS_ID)
  if (spec.disclosure === 'toggle') details.setAttribute('hidden', '')
  const facts = cell(details, factory, 'dl', ['notice-facts'])
  for (const detail of spec.details) fact(facts, factory, detail)

  let dismiss: NoticeElement | null = null
  if (spec.dismissible) {
    dismiss = cell(root, factory, 'button', ['notice-dismiss'])
    dismiss.setAttribute('type', 'button')
    dismiss.setAttribute('aria-label', NOTICE_DISMISS_LABEL)
    const glyph = cell(dismiss, factory, 'span', [])
    glyph.setAttribute('aria-hidden', 'true')
    literal(glyph, NOTICE_DISMISS_GLYPH)
  }

  return { root, title, details, toggle, dismiss }
}
