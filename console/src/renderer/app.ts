// The display layer: the only code in the console that touches an element
// (desktop-console plan Task 2.0; design section 5.1 rules 1, 2 and 7).
//
// Design section 5.1 rule 7 is one sentence -- "job-written text is text" -- and
// this file is what makes it true rather than intended. A screen does not get to
// choose how a value reaches the document: it calls one of the three writers
// below, and each of them writes exactly one *kind* of thing in exactly one
// way.
//
//   `putText`      a string, always through `textContent`. Never through a
//                  property or method that parses markup, never as an
//                  attribute, never as a fragment. A value the sanitizer
//                  refused is drawn as `n/a` with the console's own sentence in
//                  the element's `title` -- **never the value it refused**,
//                  because a refusal that echoed its input would put the input
//                  on screen through the back door.
//   `putReading`   a number, always through `formatReading`, so a screen cannot
//                  draw a bare `0` where the document said "not known".
//   `putLink`      a destination, always into `data-href` and **never** into
//                  the anchor's navigating attribute. The renderer therefore
//                  cannot navigate even if a screen is written badly later: an
//                  element with `data-href` is inert until the main process
//                  decides, behind Task 1.2's argument allow-list and Task
//                  1.1's `openExternal` path, that the URL may be opened at all.
//
// `sanitize.test.ts` drives all three into a fake element whose every
// markup-parsing property and every navigational or event attribute **throws**,
// so "the text path cannot become markup" is a fact a test holds rather than a
// property a reviewer reads for. The interface below is a structural
// `ElementLike` rather than `HTMLElement` because the unit suite runs under
// plain Node -- there is no `document` -- and because the point is the
// *observation*: the fake records every mutation, so a test can assert that the
// only write that happened was a text write.
//
// **What is deliberately not here.** This file has the writers and nothing
// else: no screen, no entry point, no wiring to the preload bridge, and no
// decision about where the compiled script is served from (`tsc` emits `dist/`
// while `src/main/index.ts` serves `src/renderer`, and one of the two has to
// move). Those are Task 2.1's, and they are named here so that a reader of this
// task does not go looking for a screen that was never in its file list.

import { UNAVAILABLE_MARK, formatReading } from '../shared/availability'
import type { Reading, Unit } from '../shared/availability'
import { isMarkerText, safeUrl, sanitizeText } from '../shared/sanitize'
import type { TextClass } from '../shared/sanitize'

/**
 * ElementLike is the whole of the DOM this file is allowed to touch: a text
 * slot and attributes. It is deliberately narrower than `HTMLElement` -- there
 * is no markup-parsing slot here, no navigating attribute, no style and no
 * event handler -- so a writer that wanted one of those would have to widen
 * this interface first, in a diff a reviewer can see, rather than reach for it
 * quietly.
 */
export interface ElementLike {
  textContent: string | null
  setAttribute(name: string, value: string): void
  getAttribute(name: string): string | null
  removeAttribute(name: string): void
}

/** TITLE_ATTRIBUTE is where an explanation goes: why a value is `n/a`, or why a
 * link is not offered. It is an attribute rather than text because a screen is
 * dense and a sentence under every `n/a` would be a paragraph, but it is *only*
 * ever the console's own sentence -- `sanitize.ts` wrote it, and it never
 * contains the value the console refused to draw. */
const TITLE_ATTRIBUTE = 'title'

/** DESTINATION_ATTRIBUTE is where a URL goes. It is **not** the navigating
 * attribute: an element carrying `data-href` does nothing when clicked, and the
 * main process -- which owns the allow-list -- is the only thing that may turn
 * one into a navigation (design section 5.1 rule 2, Task 1.2's `openExternal`).
 *
 * The literal this constant holds cannot be the navigating attribute's name:
 * `sanitize.test.ts` asserts that no file under `src/renderer/` sets an
 * attribute called `href`, and the constant is how this file sets an attribute
 * whose name a static reader can still check. */
export const DESTINATION_ATTRIBUTE = 'data-href'

/**
 * putText writes one string a job wrote. `textClass` is the class the wire key
 * declares (`schema.ts`'s `WIRE_TEXT_CLASSES`), and `field` is the wire path,
 * so a refusal names the field it came from.
 *
 * The two outcomes are the whole design: safe text is drawn as itself; a
 * refusal is drawn as `n/a` with the reason in the title. There is no third
 * path, and there is no path at all in which the raw value reaches the element.
 */
export function putText(
  element: ElementLike,
  raw: unknown,
  textClass: TextClass = 'sentence',
  field = 'the value',
): void {
  const result = sanitizeText(raw, textClass, field)
  if (result.ok) {
    element.textContent = result.text
    return
  }
  element.textContent = UNAVAILABLE_MARK
  element.setAttribute(TITLE_ATTRIBUTE, result.reason)
}

/**
 * putReading writes one number with its state. Every number on every screen
 * goes through here, and through `formatReading` inside it, so there is no way
 * for a screen to draw a bare `0` for a value the document does not carry: a
 * reading with no value has no number to draw.
 */
export function putReading(element: ElementLike, reading: Reading, unit: Unit = 'count'): void {
  element.textContent = formatReading(reading, unit)
  if (reading.value === null) element.setAttribute(TITLE_ATTRIBUTE, reading.because)
}

/**
 * putLink offers a destination. It takes the URL from `safeUrl` and never from
 * the document directly, so a `javascript:` URL, an `http` URL, a URL with no
 * host or a URL this build cannot parse is **not offered at all** -- the element
 * is left with text and a title, and nothing to click.
 *
 * A refusal is drawn as the reason, in the same shape `putText` uses, and the
 * reason is the console's sentence about the *class* ("it is not https"), never
 * the URL itself: a hostile URL is exactly the string that must not be drawn.
 */
export function putLink(element: ElementLike, raw: unknown, field = 'the link'): void {
  const sanitized = sanitizeText(raw, 'url', field)
  if (sanitized.ok && isMarkerText(sanitized.text)) {
    // A redaction is not a link: it is text, and it is the truth about the
    // field. Drawing its reason instead of the marker would hide that the CLI
    // has a URL for this run and redacted it, which is a different fact from
    // "there is no URL".
    element.textContent = sanitized.text
    element.removeAttribute(DESTINATION_ATTRIBUTE)
    element.setAttribute(TITLE_ATTRIBUTE, `${field} is a redaction, so the console offers no link`)
    return
  }
  const destination = sanitized.ok ? safeUrl(sanitized.text) : null
  if (destination === null) {
    element.textContent = UNAVAILABLE_MARK
    element.removeAttribute(DESTINATION_ATTRIBUTE)
    element.setAttribute(
      TITLE_ATTRIBUTE,
      sanitized.ok ? `${field} is not a link the console will offer` : sanitized.reason,
    )
    return
  }
  element.textContent = destination
  element.setAttribute(TITLE_ATTRIBUTE, destination)
  element.setAttribute(DESTINATION_ATTRIBUTE, destination)
}
