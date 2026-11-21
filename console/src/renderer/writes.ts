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
//
// **The second reason a control is refused is the caller's own role** (RB4,
// D2907). `disabledWrite` takes the decision `gating.ts` answered for the
// action the control performs, and a role that cannot use it draws the role
// sentence instead of the release one -- "Halt fleet needs the admin role at
// project scope and your role is viewer at project scope" -- because a control
// an operator is not allowed to use is a fact about them and not about this
// build. With no role read the tooltip is unchanged (`gating.ts`'s "no answer"
// case), and with a role that *can* use it the control still says it is not
// available in this release: the two reasons are independent, and a role that
// permits a write does not make this build perform one.

import type { GateDecision } from './gating'

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

/** roleReason is the decision's own clause as the tooltip draws it, or `null`
 * when there is no role refusal to draw: an allowed decision, and a decision
 * that carries no reason, both leave the control saying `WRITE_UNAVAILABLE`. */
function roleReason(decision: GateDecision | null): string | null {
  if (decision === null || decision.allowed) return null
  return decision.reason === '' ? null : decision.reason
}

/**
 * disabledWrite draws one write action the console does not perform: a real
 * `button` that stays in the tab order, so a keyboard reader meets it and hears
 * why it does nothing, with `aria-disabled` rather than `disabled` for the same
 * reason. `icon`, when given, is drawn before the label and is decorative.
 * `decision`, when given, is `gating.ts`'s answer for the action this control
 * performs (`can(action)`), and a role that cannot use it is the control's
 * reason rather than this release's.
 *
 * It binds nothing. A caller that adds a listener to the button it answers with
 * is adding a write the owner has not approved.
 */
export function disabledWrite<E extends WriteElement>(
  factory: WriteFactory<E>,
  className: string,
  label: string,
  icon: E | null = null,
  decision: GateDecision | null = null,
): E {
  const button = factory.create('button')
  button.setAttribute('class', className)
  button.setAttribute('type', 'button')
  button.setAttribute('aria-disabled', 'true')
  const role = roleReason(decision)
  // The role sentence is the label plus the clause (`<label> needs the admin
  // role ...`), so a tooltip read on its own still names the control; the
  // release sentence is already a sentence and is drawn as it always was.
  button.setAttribute('title', role === null ? WRITE_UNAVAILABLE : `${label} ${role}`)
  button.setAttribute(
    'aria-label',
    `${label}: ${role === null ? WRITE_UNAVAILABLE.toLowerCase() : role}`,
  )
  if (icon !== null) {
    icon.setAttribute('aria-hidden', 'true')
    button.append(icon)
  }
  const text = factory.create('span')
  text.textContent = label
  button.append(text)
  return button
}
