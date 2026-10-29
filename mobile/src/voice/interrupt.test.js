// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The interrupt switch, and the one decision it gates (mobile plan, Task P3.5;
// D127).
//
// D127 is the whole of this file: the switch is **a local phone setting** --
// never written to the database, never a message, never a field of one -- and
// whether a proactive update may interrupt is a **pure function** of the
// digest's severity and whether the user is being listened to or is already
// hearing something.
//
// The plan's fourth verification sentence is the rule this file holds to:
// *the digest's highest severity is the only thing that interrupts when the
// switch is on*. It is narrower than P6.5's `evaluateInterrupt` table, which
// also interrupts on a cap being reached and on a live-to-offline transition --
// see the module's README section and the pull request: P3.5's dependency list
// does not include P6.5, and the smaller safe thing is to hold the sentence the
// plan writes for this task.
//
// The order of the judgements is part of the rule, because it is what a screen
// shows: the switch first (nothing interrupts at all), then the severity (is
// this the digest's highest?), then the boundary (is the user speaking, or is
// the assistant already?). The reason says which one stopped it.

import assert from 'node:assert/strict';
import test from 'node:test';

import { DigestError } from '../../../shared/digest/index.js';
import { INTERRUPT_REASON, INTERRUPT_SWITCH, interruptDecision } from './interrupt.js';

/** The decision, with every argument named, so a test reads as the rule. */
function decide(overrides = {}) {
  return interruptDecision({
    switch: INTERRUPT_SWITCH.ON,
    severity: 'kill',
    highest: 'kill',
    speaking: false,
    turn: false,
    ...overrides,
  });
}

test('the plan, sentence four: with the switch on, the digest\'s highest severity interrupts at a boundary', () => {
  assert.deepEqual({ ...decide() }, { interrupt: true, reason: INTERRUPT_REASON.BOUNDARY });
  assert.equal(Object.isFrozen(decide()), true, 'a verdict handed out is a verdict frozen');
});

test('with the switch off nothing interrupts, whatever the severity is', () => {
  // The first half of the plan's first sentence: a proactive update is queued,
  // not spoken. The switch is judged BEFORE the severity, so the reason names
  // the switch rather than the ladder -- which is what a screen shows the owner
  // when they ask why nothing was said.
  assert.deepEqual(
    { ...decide({ switch: INTERRUPT_SWITCH.OFF }) },
    { interrupt: false, reason: INTERRUPT_REASON.SWITCH_OFF },
  );
  // ... and the severity does not get a vote on the reason either: with the
  // switch off, an update below the ladder's top is still refused for the
  // switch. A screen showing "not the digest's highest" here would be showing
  // the owner a reason their own setting does not change.
  assert.deepEqual(
    { ...decide({ switch: INTERRUPT_SWITCH.OFF, severity: 'info', highest: 'kill' }) },
    { interrupt: false, reason: INTERRUPT_REASON.SWITCH_OFF },
  );
  assert.equal(decide({ switch: INTERRUPT_SWITCH.OFF, speaking: true, turn: true }).reason, INTERRUPT_REASON.SWITCH_OFF);
});

test('with the switch on, a severity below the digest\'s highest does not interrupt', () => {
  // The plan's fourth sentence, as a ladder: only the top of the digest is
  // allowed to break in. Every rung below it is refused, and the reason says
  // the ladder is what stopped it, not the boundary.
  for (const severity of ['failed', 'fast_burn', 'info']) {
    assert.deepEqual(
      { ...decide({ severity, highest: 'kill' }) },
      { interrupt: false, reason: INTERRUPT_REASON.NOT_HIGHEST },
      `${severity} is below kill`,
    );
  }
  // ... and the ladder is judged by rank and not by the word: `kill` interrupts
  // when it is the digest's highest, whatever the digest's other items are.
  assert.equal(decide({ severity: 'kill', highest: 'kill' }).interrupt, true);
  // An item MORE severe than the digest's own top cannot happen (the digest's
  // `severity` IS its most severe item), and if it did it would not interrupt:
  // the rule is equality with the digest's top, not "at least as severe".
  assert.deepEqual(
    { ...decide({ severity: 'kill', highest: 'failed' }) },
    { interrupt: false, reason: INTERRUPT_REASON.NOT_HIGHEST },
  );
});

test('the severity is judged before the boundary, so a low severity says so even while the user speaks', () => {
  // The order, asserted rather than assumed: with the switch on and the user
  // mid-sentence, a below-the-top update is refused for its severity. A screen
  // that showed "the user is speaking" here would be showing a reason that
  // would not change when they stopped.
  assert.deepEqual(
    { ...decide({ severity: 'info', highest: 'kill', speaking: true, turn: true }) },
    { interrupt: false, reason: INTERRUPT_REASON.NOT_HIGHEST },
  );
});

test('an in-progress utterance is never spoken over', () => {
  // The plan's second sentence, as the guard it is: the assistant speaking is
  // the boundary, and the update waits for it. `speaking` is judged before the
  // user's turn because the two cannot both be true in the pipeline's own state
  // machine, and the assistant's utterance is the one this rule is named for.
  assert.deepEqual(
    { ...decide({ speaking: true }) },
    { interrupt: false, reason: INTERRUPT_REASON.SPEAKING },
  );
  assert.equal(decide({ speaking: true, turn: true }).reason, INTERRUPT_REASON.SPEAKING);
});

test('the user\'s turn is a boundary too: nothing interrupts while they are being listened to', () => {
  assert.deepEqual({ ...decide({ turn: true }) }, { interrupt: false, reason: INTERRUPT_REASON.USER_TURN });
  assert.equal(decide({ turn: false }).interrupt, true, 'and the turn ending is the boundary');
});

test('a switch that is neither off nor on, and a severity off the ladder, are wiring bugs and they throw', () => {
  // A `throw` here is the repository's rule for a programming error: the switch
  // comes from a constant and the severity comes from P1.3's digest, so a value
  // outside either is a caller that will never work -- and it must not be
  // answered with "do not interrupt", which would hide it behind a queue that
  // never drains.
  assert.throws(() => decide({ switch: 'sometimes' }), TypeError);
  assert.throws(() => decide({ switch: undefined }), TypeError);
  assert.throws(() => decide({ severity: 'critical' }), DigestError, 'P1.3 refuses an off-ladder severity');
  assert.throws(() => decide({ highest: undefined }), DigestError);
  // The two ladder values are the digest's own, and both are accepted.
  assert.equal(decide({ severity: 'failed', highest: 'failed' }).interrupt, true);
});

test('the switch has exactly two values, and the reasons are a closed set', () => {
  assert.deepEqual(INTERRUPT_SWITCH, { OFF: 'off', ON: 'on' });
  assert.equal(Object.isFrozen(INTERRUPT_SWITCH), true);
  // Every reason the decision can answer with, in the order the judgements are
  // made. A screen switches on this list, so a reason added here is a screen
  // change -- which is the point of it being a closed set.
  assert.deepEqual(INTERRUPT_REASON, {
    SWITCH_OFF: 'switch-off',
    NOT_HIGHEST: 'not-highest',
    SPEAKING: 'speaking',
    USER_TURN: 'user-turn',
    BOUNDARY: 'boundary',
  });
  assert.equal(Object.isFrozen(INTERRUPT_REASON), true);
});
