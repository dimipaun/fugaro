// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The indicator's rendering: its tests (mobile plan, Task P5.3.6; D123, D127;
// the design is docs/mobile/design/voice-evaluation-switch.md §4.2 and §4.5).
//
// The plan's sentences: **the indicator is persistent, not dismissible, and
// shows `local — checking` until the hop's first answer arrives**. The first two
// are `INDICATOR`'s two fields; the third is asserted against the shared
// module's own answer for the same input rather than against a copy of the
// wording, so the two cannot drift apart into two badges.
//
// Nothing below re-implements the shared table: the branch a test exercises is
// made by handing `renderIndicator` the input that reaches it, and what is
// asserted is the field this module adds (`send`), the two it fixes
// (`persistent`, `dismissible`) and the fail-closed badge a refusal gets.

import assert from 'node:assert/strict';
import test from 'node:test';

import { CALL_STATE, ENGINE, POLICY_REASON, REFUSAL, REASON, STATE, STATUS_FAILURE, indicator as decide } from '../../../shared/voice-switch/index.js';
import { INDICATOR, INDICATOR_FIELDS, failClosedIndicator, renderIndicator } from './indicator.js';

const AT = 1700000000000;
const NAME = 'deepgram';
const OTHER = 'assemblyai';

/** A switch that allows remote on one direction. */
const on = (direction) => ({ settings: { [direction]: ENGINE.REMOTE } });
const consent = (...names) => ({ consent: names.map((provider) => ({ provider, at: AT })) });
/** The hop's answer that admits this direction, and its names. */
const admits = (...names) => ({ status: { ok: true, providers: names, direction: { stt: true, tts: true }, reason: null } });
const call = (direction, fields) => ({ lastCallInDirection: { direction, engine: ENGINE.REMOTE, provider: NAME, state: CALL_STATE.DONE, reason: null, ...fields } });

test('an unset setting is local on both directions, and there is nothing to send', () => {
  for (const direction of ['stt', 'tts']) {
    const badge = renderIndicator({ direction });
    assert.equal(badge.state, STATE.LOCAL, direction);
    assert.equal(badge.text, 'local', direction);
    assert.equal(badge.engine, ENGINE.LOCAL, direction);
    assert.equal(badge.provider, null, direction);
    assert.equal(badge.reason, REASON.SWITCH_LOCAL, direction);
    assert.equal(badge.send, false, direction);
    assert.equal(badge.refusal, null, direction);
  }
});

test('the badge is persistent and not dismissible, on every answer', () => {
  assert.deepEqual({ ...INDICATOR }, { persistent: true, dismissible: false });
  const answers = [
    renderIndicator({ direction: 'stt' }),
    renderIndicator({ direction: 'stt', ...on('stt'), ...consent(NAME), ...admits(NAME) }),
    renderIndicator({ direction: 'stt', ...on('stt'), ...consent(NAME) }),
    failClosedIndicator({ direction: 'stt' }, { code: 'whatever', field: null }),
  ];
  for (const badge of answers) {
    assert.equal(badge.persistent, true, badge.text);
    assert.equal(badge.dismissible, false, badge.text);
    assert.ok(Object.isFrozen(badge), badge.text);
  }
});

test('the badge\'s field set is exact, and no field is a way to hide it', () => {
  const badge = renderIndicator({ direction: 'stt' });
  assert.deepEqual(Object.keys(badge), [...INDICATOR_FIELDS]);
  for (const [name, value] of Object.entries(badge)) {
    assert.notEqual(typeof value, 'function', name);
  }
  assert.equal(INDICATOR_FIELDS.includes('dismiss'), false);
  assert.equal(INDICATOR_FIELDS.includes('hide'), false);
  assert.equal(INDICATOR_FIELDS.includes('ok'), false, 'a badge is not a result: it is drawn, always');
});

test('with remote switched on and no project answer yet, the badge says so and sends nothing', () => {
  const badge = renderIndicator({ direction: 'stt', ...on('stt'), ...consent(NAME) });
  assert.equal(badge.state, STATE.CHECKING);
  assert.equal(badge.text, 'local — checking', 'the design §4.2 wording');
  assert.equal(badge.engine, ENGINE.LOCAL, 'the engine it serves in is local while it checks');
  assert.equal(badge.reason, REASON.NO_ANSWER);
  assert.equal(badge.send, false);
  assert.equal(badge.refusal, null);
});

test('the checking badge is the shared module\'s own, word for word', () => {
  const input = { direction: 'stt', ...on('stt'), ...consent(NAME) };
  const shared = decide(input);
  const badge = renderIndicator(input);
  assert.equal(shared.ok, true);
  assert.equal(badge.text, shared.text);
  assert.equal(badge.state, shared.state);
  assert.equal(badge.reason, shared.reason);
});

test('a project answer that admits the direction and the name sends', () => {
  const badge = renderIndicator({ direction: 'stt', ...on('stt'), ...consent(OTHER, NAME), ...admits(OTHER, NAME) });
  assert.equal(badge.state, STATE.REMOTE);
  assert.equal(badge.text, `remote (${OTHER})`, 'the hop\'s own order picks the name');
  assert.equal(badge.engine, ENGINE.REMOTE);
  assert.equal(badge.provider, OTHER);
  assert.equal(badge.inFlight, false);
  assert.equal(badge.reason, null);
  assert.equal(badge.send, true, 'this is the one answer the egress path may act on');
});

test('a name the project admits but the device never agreed to does not send', () => {
  const badge = renderIndicator({ direction: 'stt', ...on('stt'), ...consent(OTHER), ...admits(NAME) });
  assert.equal(badge.state, STATE.LOCAL);
  assert.equal(badge.engine, ENGINE.LOCAL);
  assert.equal(badge.reason, REASON.NO_CONSENT, 'the device is the half that is missing, so it is not `blocked`');
  assert.equal(badge.send, false);
});

test('a project that forces local blocks, and keeps the badge honest', () => {
  const badge = renderIndicator({
    direction: 'stt',
    ...on('stt'),
    ...consent(NAME),
    status: { ok: true, providers: [NAME], direction: { stt: true, tts: true }, reason: POLICY_REASON.FORCE_LOCAL },
  });
  assert.equal(badge.state, STATE.BLOCKED);
  assert.equal(badge.text, 'blocked by project policy');
  assert.equal(badge.engine, ENGINE.LOCAL);
  assert.equal(badge.send, false);
});

test('a hop that could not be reached is local with its reason, and a failed query is still checking', () => {
  const unreachable = renderIndicator({ direction: 'stt', ...on('stt'), ...consent(NAME), status: { ok: false, code: STATUS_FAILURE.UNREACHABLE } });
  assert.equal(unreachable.state, STATE.LOCAL);
  assert.equal(unreachable.reason, STATUS_FAILURE.UNREACHABLE);
  const failed = renderIndicator({ direction: 'stt', ...on('stt'), ...consent(NAME), status: { ok: false, code: STATUS_FAILURE.QUERY_FAILED } });
  assert.equal(failed.state, STATE.CHECKING, 'a query that could not answer is the same fact as no answer');
  assert.equal(failed.send, false);
});

test('a call in flight is shown, and is not a reason to start another', () => {
  const badge = renderIndicator({
    direction: 'stt',
    ...on('stt'),
    ...consent(NAME),
    ...admits(NAME),
    ...call('stt', { state: CALL_STATE.IN_FLIGHT }),
  });
  assert.equal(badge.inFlight, true);
  assert.equal(badge.text, `remote (${NAME}) — in flight`, 'the design\'s own suffix');
  assert.equal(badge.engine, ENGINE.REMOTE, 'the decision is untouched by the call');
  assert.equal(badge.send, false, 'the shared module\'s `inFlight` is what tells a caller not to start a second call');
});

test('a switch turned off mid-call still shows the call that is leaving', () => {
  const badge = renderIndicator({
    direction: 'stt',
    ...consent(NAME),
    ...call('stt', { state: CALL_STATE.IN_FLIGHT }),
  });
  assert.equal(badge.inFlight, true);
  assert.equal(badge.text, `remote (${NAME}) — in flight`, 'the badge tells the truth about now');
  assert.equal(badge.engine, ENGINE.LOCAL, 'the egress answer stays fail-closed');
  assert.equal(badge.send, false);
});

test('a failed call falls to local, with the call\'s own reason', () => {
  const badge = renderIndicator({
    direction: 'stt',
    ...on('stt'),
    ...consent(NAME),
    ...admits(NAME),
    ...call('stt', { state: CALL_STATE.FAILED, reason: 'provider-error' }),
  });
  assert.equal(badge.state, STATE.LOCAL);
  assert.equal(badge.text, 'local');
  assert.equal(badge.engine, ENGINE.LOCAL);
  assert.equal(badge.reason, 'provider-error');
  assert.equal(badge.send, false, '§4.5: every failure falls to local');
});

test('the other direction\'s call is not this direction\'s', () => {
  const badge = renderIndicator({
    direction: 'stt',
    ...on('stt'),
    ...consent(NAME),
    ...admits(NAME),
    ...call('tts', { state: CALL_STATE.IN_FLIGHT }),
  });
  assert.equal(badge.inFlight, false);
  assert.equal(badge.send, true);
});

test('a refused input is drawn, not thrown: the badge is owed and it fails closed', () => {
  const broken = renderIndicator({ direction: 'stt', settings: 'remote' });
  assert.equal(broken.state, STATE.CHECKING);
  assert.equal(broken.engine, ENGINE.LOCAL);
  assert.equal(broken.provider, null);
  assert.equal(broken.inFlight, false);
  assert.equal(broken.reason, REASON.NO_ANSWER, 'there is no answer, which is what the badge says');
  assert.equal(broken.send, false, 'a badge that could not be built is the last one that may be read as permission');
  assert.equal(broken.refusal.code, REFUSAL.NOT_AN_OBJECT);
  assert.equal(broken.refusal.field, 'settings');
  assert.ok(Object.isFrozen(broken.refusal));

  const checking = renderIndicator({ direction: 'stt', ...on('stt'), ...consent(NAME) });
  assert.equal(broken.text, checking.text, 'the fail-closed badge uses the shared module\'s own checking words');
});

test('the refusal travels with the badge, and never carries a value from the input', () => {
  const badge = renderIndicator({ direction: 'stt', consent: [{ provider: 'https://provider.example', at: AT }] });
  assert.equal(badge.refusal.code, REFUSAL.BAD_PROVIDER);
  assert.deepEqual(Object.keys(badge.refusal), ['code', 'field']);
  assert.equal(JSON.stringify(badge).includes('provider.example'), false, 'a refused value is not echoed');
});

test('a badge says which direction was asked about, and only when it is one of the two', () => {
  assert.equal(renderIndicator({ direction: 'tts' }).direction, 'tts');
  assert.equal(renderIndicator({ direction: 'both' }).direction, null);
  assert.equal(renderIndicator({}).direction, null);
  assert.equal(renderIndicator(null).direction, null);
  assert.equal(renderIndicator({ direction: 'stt', settings: 'remote' }).direction, 'stt', 'a refusal still knows the question');
});

test('no input, however hostile, makes the render throw', () => {
  const hostile = [
    undefined,
    null,
    7,
    'stt',
    [],
    { direction: 'stt', settings: new (class Engine {})(), },
    { direction: 'stt', status: [] },
    { direction: 'stt', lastCallInDirection: 'in flight' },
    { direction: 'stt', settings: null, consent: null, status: null, lastCallInDirection: null },
    new Proxy({}, {
      get() {
        throw new Error('revoked');
      },
      ownKeys() {
        throw new Error('revoked');
      },
    }),
  ];
  // The message is the index and not `String(input)`: the last input throws
  // from every trap, `String` among them, and a test that reported a name for
  // it would be the thing that threw.
  for (const [index, input] of hostile.entries()) {
    const badge = renderIndicator(input);
    assert.equal(badge.send, false, `input ${index}`);
    assert.equal(badge.engine, ENGINE.LOCAL, `input ${index}`);
    assert.equal(badge.persistent, true, `input ${index}`);
  }
});

test('`send` is true only for a live route with no call already going', () => {
  const inputs = [
    { direction: 'stt' },
    { direction: 'tts' },
    { direction: 'stt', ...on('stt') },
    { direction: 'stt', ...on('stt'), ...consent(NAME) },
    { direction: 'stt', ...on('stt'), ...consent(NAME), ...admits(NAME) },
    { direction: 'stt', ...on('stt'), ...consent(NAME), ...admits(OTHER, NAME) },
    { direction: 'stt', ...on('stt'), ...consent(NAME), ...admits(NAME), ...call('stt', { state: CALL_STATE.IN_FLIGHT }) },
    { direction: 'stt', ...on('stt'), ...consent(NAME), ...admits(NAME), ...call('stt', { state: CALL_STATE.FAILED, reason: 'provider-error' }) },
    { direction: 'stt', ...on('stt'), ...consent(NAME), status: { ok: false, code: STATUS_FAILURE.EXPIRED_TOKEN } },
    { direction: 'stt', settings: 'remote' },
    { direction: 'stt', consent: 'deepgram' },
  ];
  for (const input of inputs) {
    const badge = renderIndicator(input);
    if (badge.send) {
      assert.equal(badge.engine, ENGINE.REMOTE, JSON.stringify(input));
      assert.ok(badge.provider, JSON.stringify(input));
      assert.equal(badge.reason, null, JSON.stringify(input));
      assert.equal(badge.inFlight, false, JSON.stringify(input));
      assert.equal(badge.refusal, null, JSON.stringify(input));
    }
  }
});

test('rendering does not touch the input it was handed', () => {
  const input = {
    direction: 'stt',
    settings: { stt: ENGINE.REMOTE, tts: ENGINE.LOCAL },
    consent: [{ provider: NAME, at: AT }],
    status: { ok: true, providers: [NAME], direction: { stt: true, tts: false }, reason: null },
    lastCallInDirection: null,
  };
  const snapshot = JSON.stringify(input);
  renderIndicator(input);
  assert.equal(JSON.stringify(input), snapshot);
});
