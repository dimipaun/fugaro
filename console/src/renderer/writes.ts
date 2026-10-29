// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See console/LICENSE.md for the terms.
// The redesign draws write actions the console does not perform: Halt fleet,
// Halt project, Kill and Revive, Reap, Pause task, Retry run, Add cap. The
// console's one approved write is launch-from-console (V2.5.1, not built), so
// the owner's ruling of 2026-10-28 is that each is drawn as designed and
// refused: `aria-disabled`, the sentence below as its description, and no
// listener, no IPC channel, no preload member and no spawn entry behind it.
// Every screen draws its write buttons through `disabledWrite`, so the refusal
// is one function a reviewer reads rather than one per screen.

/** WRITE_UNAVAILABLE is what a drawn write says about itself. */
export const WRITE_UNAVAILABLE = 'Not available in this release'

/** WriteElement is the part of an element `disabledWrite` touches. */
export interface WriteElement {
  setAttribute(name: string, value: string): void
  append(child: WriteElement): void
  textContent: string | null
}

/** WriteFactory is the element factory a screen or the entry draws with. */
export interface WriteFactory<E extends WriteElement> {
  create(tag: string): E
}

/**
 * disabledWrite draws one write action the console does not perform: a real
 * `button` that stays in the tab order, so a keyboard reader meets it and hears
 * why it does nothing, with `aria-disabled` rather than `disabled` for the same
 * reason. `icon`, when given, is drawn before the label and is decorative.
 *
 * It binds nothing. A caller that adds a listener to the button it answers with
 * is adding a write the owner has not approved.
 */
export function disabledWrite<E extends WriteElement>(
  factory: WriteFactory<E>,
  className: string,
  label: string,
  icon: E | null = null,
): E {
  const button = factory.create('button')
  button.setAttribute('class', className)
  button.setAttribute('type', 'button')
  button.setAttribute('aria-disabled', 'true')
  button.setAttribute('title', WRITE_UNAVAILABLE)
  button.setAttribute('aria-label', `${label}: ${WRITE_UNAVAILABLE.toLowerCase()}`)
  if (icon !== null) {
    icon.setAttribute('aria-hidden', 'true')
    button.append(icon)
  }
  const text = factory.create('span')
  text.textContent = label
  button.append(text)
  return button
}
