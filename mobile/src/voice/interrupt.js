// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The interrupt switch, and the pure decision it gates (mobile plan, Task
// P3.5; D127, and P1.3's ladder).
//
// **D127 is two rulings in one.** The switch is a **local phone setting**: it
// is never written to the database, never a field of a message and never a
// claim the orchestrator can read -- the owner's phone deciding whether its own
// assistant may break in is not something another installation gets a vote on.
// And what the switch gates is a **pure function** of the digest's severity
// (P1.3) and whether the user is being listened to or is already hearing
// something: no clock, no state, no port. That is what makes "the interrupt
// policy is testable with the fakes alone" (D126, Global Constraint 10) true
// rather than aspirational.
//
// **The plan's sentence, and where it is narrower than P6.5.** P3.5's
// verification says *the digest's highest severity is the only thing that
// interrupts when the switch is on*. `shared/prompts/` (P6.5, merged before
// this task) carries a wider `evaluateInterrupt` table -- a cap being reached,
// a cap being near, and the live-to-offline transition also interrupt there.
// P3.5's dependency list names P1.2, P1.3, P1.4, P3.4 and P3.6 and **not**
// P6.5, so this file holds the sentence the plan writes for this task and
// nothing else; when P6.5's queue is wired to this screen, the wider table is
// the thing to compose with, deliberately and with its own tests. That
// disagreement between the plan's text and the merged code is stated in
// `mobile/README.md` and in the pull request rather than resolved quietly here.
//
// **The order of the judgements is part of the answer.** The switch first
// (nothing interrupts at all), then the severity (is this the digest's top?),
// then the boundary (is the user speaking, or is the assistant?). Each one is
// the reason the decision answers with, and a screen shows that reason -- so
// "why was nothing said" has an answer the owner can read rather than a queue
// that silently never drains.

import { severityRank } from '../../../shared/digest/index.js';

/**
 * D127's local setting. Two values, and no third: a switch that could be
 * "sometimes" is a switch no screen can render and no test can pin.
 */
export const INTERRUPT_SWITCH = Object.freeze({
  OFF: 'off',
  ON: 'on',
});

/**
 * Why the decision answered what it did. A closed set, in the order the
 * judgements are made: the first four are the ways a proactive update does not
 * interrupt, and `boundary` is the one way it does.
 */
export const INTERRUPT_REASON = Object.freeze({
  /** The owner turned the switch off: queued, and not spoken over them. */
  SWITCH_OFF: 'switch-off',
  /** The switch is on and this is not the digest's most severe item. */
  NOT_HIGHEST: 'not-highest',
  /** An utterance is in flight; nothing is ever spoken over it. */
  SPEAKING: 'speaking',
  /** The user's turn is open: they are being listened to. */
  USER_TURN: 'user-turn',
  /** The switch is on, this is the digest's top, and nothing else is happening. */
  BOUNDARY: 'boundary',
});

/**
 * Whether a proactive update may be spoken now, and why not when it may not.
 *
 * The four arguments are all of the rule: the setting, the update's severity,
 * the digest's most severe severity, and the two facts about the moment. It
 * reads no clock and holds no state -- two calls with equal arguments answer
 * equally.
 *
 * **A wiring bug throws.** The switch comes from `INTERRUPT_SWITCH` and the two
 * severities come from P1.3's digest, so a value outside either is a caller
 * that will never work; answering it with `interrupt: false` would hide it
 * behind a queue that never drains, which is the failure this module exists to
 * make impossible to miss. P1.3's own ladder refuses an unknown severity the
 * same way (`severityRank`), and that refusal is passed through as itself.
 *
 * @param {object} input
 * @param {string} input.switch `INTERRUPT_SWITCH.OFF` or `.ON`
 * @param {string} input.severity the update's severity, on P1.3's ladder
 * @param {string} input.highest the digest's `severity` -- its most severe item
 * @param {boolean} input.speaking whether an utterance is in flight
 * @param {boolean} input.turn whether the user's turn is open
 * @returns {{ interrupt: boolean, reason: string }} a frozen verdict
 * @throws {TypeError} for a switch that is neither of the two values
 * @throws {import('../../../shared/digest/index.js').DigestError} for a
 *   severity that is not on P1.3's ladder
 */
export function interruptDecision({ switch: setting, severity, highest, speaking, turn } = {}) {
  if (setting !== INTERRUPT_SWITCH.OFF && setting !== INTERRUPT_SWITCH.ON) {
    throw new TypeError('the interrupt switch is INTERRUPT_SWITCH.OFF or .ON');
  }
  // Both are ranked before either is compared: an off-ladder value is refused
  // rather than ranked as `info`, which is P1.3's own rule and the reason this
  // module imports the ladder instead of writing the order down again.
  const mine = severityRank(severity);
  const top = severityRank(highest);
  const decide = (interrupt, reason) => Object.freeze({ interrupt, reason });
  if (setting === INTERRUPT_SWITCH.OFF) return decide(false, INTERRUPT_REASON.SWITCH_OFF);
  if (mine !== top) return decide(false, INTERRUPT_REASON.NOT_HIGHEST);
  if (speaking === true) return decide(false, INTERRUPT_REASON.SPEAKING);
  if (turn === true) return decide(false, INTERRUPT_REASON.USER_TURN);
  return decide(true, INTERRUPT_REASON.BOUNDARY);
}
