// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The publisher's relay: its tests (mobile plan, Task P5.2.8; the owner's Q7
// ruling of 2026-11-21; the design's §3.3, §3.5 (b) and §9).
//
// **The assertion this file exists for is which token comes out.** The phone's
// device token goes to the relay, the relay answers a token of its own, and the
// token the mailbox would carry is the relay's -- a registration that carried
// the device token would put the phone's own address in the installation's
// mailbox, which is the thing §3.3's "installations hold a publisher token
// instead" rules out. The relay's protocol is the publisher's and is not faked
// as if it were a real one: what is faked is the app's own port,
// `relay.register({ endpoint, deviceToken })`, which the entry implements.

import assert from 'node:assert/strict';
import test from 'node:test';

import { RELAY_CODES, relayTokenSource } from './relay.js';
import { MemoryTokenSource, TOKEN_CODES, readPushToken } from './token.js';

/** Synthetic, and shaped like the SDK's: this file registers neither. */
const DEVICE_TOKEN = 'not-a-real-device-token-0123456789';
const RELAY_TOKEN = 'not-a-real-relay-token-9876543210';
/** The publisher's relay, as the wiring names it. Never a host this file owns. */
const ENDPOINT = 'https://relay.invalid/push/register';

/**
 * The app's own port over the publisher's relay, with a call log.
 *
 * `mode` is what the relay does: answer a token of its own, answer the device
 * token back, answer a refusal, answer nothing at all, or throw.
 */
function port({ mode = 'answers', token = RELAY_TOKEN } = {}) {
  const calls = [];
  return {
    calls,
    relay: {
      register: async (request) => {
        calls.push(request);
        if (mode === 'throws') throw new Error('the transport refused');
        if (mode === 'silent') return undefined;
        if (mode === 'refuses') return { ok: false, code: 'relay-refused' };
        if (mode === 'echoes') return { ok: true, token: request.deviceToken };
        return { ok: true, token };
      },
    },
  };
}

/** A source over a device source and a relay port, with the endpoint named. */
function source({ device = new MemoryTokenSource({ token: DEVICE_TOKEN }), relay, endpoint = ENDPOINT } = {}) {
  return relayTokenSource({ device, relay, endpoint });
}

test('the phone’s token goes to the relay, and the relay’s token is what is registered', async () => {
  const p = port();
  const read = await readPushToken(source({ relay: p.relay }));
  assert.equal(read.ok, true);
  assert.equal(read.token, RELAY_TOKEN, 'the registration carries the relay’s token');
  assert.equal(read.token === DEVICE_TOKEN, false, 'and never the phone’s own address');
  // The relay is named and given the device token: the two things this module
  // passes, and all it passes -- the account and the app's own identity are the
  // adapter's business, where the app's credentials already are.
  assert.deepEqual(p.calls, [{ endpoint: ENDPOINT, deviceToken: DEVICE_TOKEN }]);
});

test('a relay that answers the phone’s own token back registers nothing', async () => {
  // The echo case: the relay has not registered this installation, it has
  // handed back the one value §3.3 says the mailbox must not hold.
  const echoed = await readPushToken(source({ relay: port({ mode: 'echoes' }).relay }));
  assert.equal(echoed.ok, false);
  assert.equal(echoed.code, RELAY_CODES.RELAY_FAILED);
  // The control: the same port answering a token of its own is registered, so
  // the assertion above is about the token and not about the fake.
  assert.equal((await readPushToken(source({ relay: port().relay }))).ok, true);
});

test('a relay that refuses, throws, answers nothing or answers an empty token is a refusal', async () => {
  for (const [mode, options] of [['refuses'], ['throws'], ['silent'], ['answers', { token: '' }]]) {
    const read = await readPushToken(source({ relay: port({ mode, ...options }).relay }));
    assert.equal(read.ok, false, mode);
    assert.equal(read.code, RELAY_CODES.RELAY_FAILED, mode);
    // `readPushToken` answers about the token, so the field it reports is the
    // schema's `token` rather than the relay this module names: a screen
    // branches on the code, and the code is the relay's.
    assert.equal(read.field, 'token', mode);
    assert.equal(JSON.stringify(read).includes(DEVICE_TOKEN), false, `${mode}: no token in the refusal`);
  }
});

test('a phone with no token never reaches the relay, and keeps its own code', async () => {
  const p = port();
  const read = await readPushToken(source({ device: new MemoryTokenSource({ token: null }), relay: p.relay }));
  assert.equal(read.ok, false);
  assert.equal(read.code, TOKEN_CODES.NO_TOKEN, 'the device’s own answer, not the relay’s');
  assert.deepEqual(p.calls, [], 'the relay was not asked to register a phone that has no token');
  // ... and a device that wrongs its port is reported as the broken device it
  // is: `readPushToken`'s `token-failed`, never a registration.
  const wrong = await readPushToken(source({ device: { token: async () => 42 }, relay: port().relay }));
  assert.equal(wrong.code, TOKEN_CODES.TOKEN_FAILED);
});

test('the relay is named or the source is not built, and the endpoint is what is passed', async () => {
  const p = port();
  const device = new MemoryTokenSource({ token: DEVICE_TOKEN });
  for (const bad of [undefined, null, '', 42, {}]) {
    assert.throws(
      () => relayTokenSource({ device, relay: p.relay, endpoint: bad }),
      TypeError,
      `endpoint=${String(bad)}`,
    );
  }
  // The endpoint the source was built with is the one the adapter is handed:
  // a source that defaulted it would have a second answer to "which relay".
  const other = 'https://relay-2.invalid/push/register';
  await readPushToken(source({ relay: p.relay, endpoint: other }));
  assert.deepEqual(p.calls, [{ endpoint: other, deviceToken: DEVICE_TOKEN }]);
});

test('half a port is refused at construction rather than at the first registration', () => {
  const whole = port();
  for (const bad of [
    {},
    { relay: whole.relay },
    { device: new MemoryTokenSource() },
    { device: {}, relay: whole.relay },
    { device: new MemoryTokenSource(), relay: {} },
  ]) {
    assert.throws(() => relayTokenSource({ ...bad, endpoint: ENDPOINT }), TypeError, JSON.stringify(Object.keys(bad)));
  }
});
