// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
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
// **What is deliberately not here.** This file has the writers, and -- since
// Task 3.3 -- the *gate* every screen is mounted through: no screen of its own,
// no dashboard, and no decision about where the compiled script is served from.
// That decision was Task 3.4a's, and it moved the root rather than the compiler:
// `npm run build` now writes `dist/bundle/` (`tools/bundle-renderer/index.ts`)
// and `src/main/index.ts` serves that (`rendererRoot`), so `tsc`'s `dist/` is
// never what a window is handed. The screens are Task 2.1's, and they are named
// here so that a reader of this task does not go looking for a screen that was
// never in its file list.
//
// ## The gate (Task 3.3)
//
// Design section 5.1 rule 4 and D7 say the renderer must not draw a view the
// handshake withheld -- and that it must draw nothing at all before it has an
// answer, because a screen that draws first and asks later has already drawn.
// So the renderer's startup is one call: `startConsole` asks the main process
// for the compatibility (`startup.compatibility`, the one channel that answers
// the console's own state), and hands back a gate. A screen is reachable only
// through `gate.drawScreen`, which refuses a withheld view, and a document that
// got no answer -- a rejected promise, a reply that is not a `ConsoleReply`, a
// value that is not a compatibility -- is treated as the refusal, which is the
// only answer that draws no figures.
//
// The check on that value is written **twice on purpose**: `src/main/ipc.ts`'s
// `readStoredCompatibility` checks what it is about to answer with, and
// `compatibilityFrom` below checks what it was handed. Neither is the other's
// substitute: the renderer's copy is what still holds if the main process's
// check is dropped, mutated or bypassed, and a single shared checker would make
// both places fail as one. `test/unit/ipc.test.ts` holds the two to the same
// verdict over the same table of hostile values, so two implementations cannot
// drift into two policies.

import { UNAVAILABLE_MARK, formatReading } from '../shared/availability'
import type { Reading, Unit } from '../shared/availability'
import { isMarkerText, safeUrl, sanitizeText } from '../shared/sanitize'
import type { TextClass } from '../shared/sanitize'
import { REDUCED_VIEWS, hidesView, renderVersionSkew, unreadableCompatibility } from '../shared/version'
import type { CliCompatibility, ReducedView, SkewElement, SkewFactory, SkewTheme } from '../shared/version'

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

// ---------------------------------------------------------------------------
// The startup gate (desktop-console plan Task 3.3; design section 5.1 rule 4,
// D7).
// ---------------------------------------------------------------------------

/**
 * CompatibilityBridge is the one bridge method this file calls. It is a
 * structural type rather than `src/preload/index.ts`'s `ConsoleBridge` because
 * the preload is a *value* module the renderer cannot import (the sandboxed
 * preload may not require a relative path, and a bundler is a later task's
 * dependency), and because this file needs exactly one method of the twenty-four:
 * naming that one is what makes "the entry point asks for the handshake and
 * nothing else" readable here.
 */
export interface CompatibilityBridge {
  getCompatibility(): Promise<unknown>
}

/** MountLike is where a drawing goes: the element the entry appends to. */
export interface MountLike {
  append(child: SkewElement): void
}

/** RENDERER_UNREADABLE is the renderer's own sentence for an answer it will not
 * gate on -- a rejected call, a reply that is not a `ConsoleReply`, a value that
 * is not a compatibility. It is a constant of this file's, and the value it
 * refused is never carried into it. */
export const RENDERER_UNREADABLE = 'the console could not read its own handshake answer'

/** RENDERER_FIELD_LIMIT bounds every free string the renderer accepts in a
 * compatibility (`skew`, `cliVersion`, `because`). It is the renderer's own copy
 * of the main process's `MAX_COMPATIBILITY_FIELD_LENGTH`, written out rather
 * than imported for the reason in this file's header. */
export const RENDERER_FIELD_LIMIT = 200

/** StartConsoleDeps is everything the gate needs: the bridge, where the drawings
 * go, the factory the drawings come from, and the theme they are drawn for. */
export interface StartConsoleDeps {
  readonly bridge: CompatibilityBridge
  readonly mount: MountLike
  readonly factory: SkewFactory
  readonly theme: SkewTheme
}

/** ScreenGate is what a screen is mounted through: the compatibility the console
 * decided on, and the one way to draw. */
export interface ScreenGate {
  readonly compatibility: CliCompatibility
  /**
   * drawScreen draws one view, or does not: `refused` when the console refused
   * (nothing is drawn, and the refusal screen is already on the document),
   * `withheld` when this mode withholds that view (the banner `startConsole`
   * drew names it; the screen's own drawing is not called at all), and `drawn`
   * when the screen may be mounted.
   *
   * The `view` is a `ReducedView` and not a `string`, so the three views a mode
   * can withhold are the three a screen can name -- a fourth id would be a
   * compile error rather than a view that is silently never withheld.
   */
  drawScreen(view: ReducedView, draw: () => void): 'drawn' | 'withheld' | 'refused'
}

/**
 * compatibilityFrom is the renderer's own check on the answer, and it is the
 * mirror of `src/main/ipc.ts`'s `readStoredCompatibility`: same fields, same
 * verdicts, written here rather than imported (see this file's header). The
 * refusal it answers with is `unreadableCompatibility`, so a document that got
 * an answer it could not read draws the refusal screen and no figures -- never a
 * dashboard on the strength of a value nobody checked. What it answers for a
 * value it accepts is a fresh, frozen object built from the checked fields, so
 * no unchecked key reaches the gate.
 */
export function compatibilityFrom(answer: unknown): CliCompatibility {
  const refused = (): CliCompatibility => unreadableCompatibility(RENDERER_UNREADABLE)
  if (typeof answer !== 'object' || answer === null) return refused()
  const reply = answer as Record<string, unknown>
  if (reply['ok'] !== true) return refused()
  const value: unknown = reply['data']
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return refused()
  const prototype: unknown = Object.getPrototypeOf(value)
  if (prototype !== Object.prototype && prototype !== null) return refused()

  const record = value as Record<string, unknown>
  const mode = record['mode']
  if (mode !== 'ok' && mode !== 'reduced' && mode !== 'refused') return refused()
  const skew = record['skew']
  if (typeof skew !== 'string' || skew === '' || skew.length > RENDERER_FIELD_LIMIT) return refused()
  const consoleVersion = record['consoleVersion']
  const floor = record['floor']
  if (typeof consoleVersion !== 'string' || typeof floor !== 'string') return refused()
  const cliVersion = record['cliVersion']
  if (cliVersion !== null && typeof cliVersion !== 'string') return refused()
  if (typeof cliVersion === 'string' && cliVersion.length > RENDERER_FIELD_LIMIT) return refused()
  const supported: unknown = record['supported']
  if (!Array.isArray(supported) || supported.some((minor: unknown) => typeof minor !== 'string')) {
    return refused()
  }
  const hidden = record['hidden']
  if (!Array.isArray(hidden)) return refused()
  const views = hidden as readonly unknown[]
  if (views.some((view) => !REDUCED_VIEWS.includes(view as ReducedView))) return refused()
  if (new Set(views).size !== views.length) return refused()

  // `because`: absent, or a short string on an `unreadable` value -- the same
  // verdict as the main process's check, written out again here.
  const hasBecause = Object.prototype.hasOwnProperty.call(record, 'because')
  const because = record['because']
  if (hasBecause) {
    if (typeof because !== 'string' || because === '') return refused()
    if (because.length > RENDERER_FIELD_LIMIT || skew !== 'unreadable') return refused()
  }

  // A fresh object from the checked fields only: a key this check did not read
  // is not carried into the gate, whatever the main process sent.
  const compatibility: CliCompatibility = {
    mode,
    skew: skew as CliCompatibility['skew'],
    cliVersion,
    consoleVersion,
    supported: Object.freeze([...(supported as string[])]),
    floor,
    hidden: Object.freeze([...(views as ReducedView[])]),
    ...(hasBecause ? { because: because as string } : {}),
  }
  for (const view of REDUCED_VIEWS) {
    if (hidesView(compatibility, view) !== (mode !== 'ok')) return refused()
  }
  return Object.freeze(compatibility)
}

/**
 * startConsole is the renderer's first move and its only one before a screen
 * exists: ask for the handshake's answer, draw the console's own statement about
 * the CLI (the refusal screen, or the reduced mode's banner) if there is
 * anything to say, and hand back the gate.
 *
 * Nothing else is drawn here, and the gate is why: a screen the caller mounts is
 * mounted through `drawScreen`, which is where a withheld view stops. That is
 * the second half of the defence design section 5.1 rule 4 asks for -- the main
 * process built a refusal *document* for a refused console, and the renderer
 * independently refuses to draw a withheld view in the document it was given.
 */
export async function startConsole(deps: StartConsoleDeps): Promise<ScreenGate> {
  let answer: unknown
  try {
    answer = await deps.bridge.getCompatibility()
  } catch {
    answer = null
  }
  const compatibility = compatibilityFrom(answer)
  if (compatibility.mode !== 'ok') {
    // One drawing for both non-`ok` modes, which is what `renderVersionSkew`
    // is: the whole screen when the console refused, the banner a screen is
    // mounted under when it is reduced.
    deps.mount.append(renderVersionSkew(deps.factory, compatibility, deps.theme))
  }
  return {
    compatibility,
    drawScreen(view: ReducedView, draw: () => void): 'drawn' | 'withheld' | 'refused' {
      if (compatibility.mode === 'refused') return 'refused'
      if (hidesView(compatibility, view)) return 'withheld'
      draw()
      return 'drawn'
    },
  }
}
