// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The token port: its tests (mobile plan, Task P5.2.8).
//
// The property this file is really about is the plan's second verification
// sentence, at the layer that decides it: **a token that cannot be obtained is
// not written as an empty field**. There is no answer from this module that
// carries an empty token -- a source that says nothing, refuses, throws or
// answers a value the schema rejects becomes a refusal with a code, and the
// caller skips the write (`registration.js`). The test asserts that on the
// SHAPE as well as on the code: nothing here ever answers `{ok: true}` with
// anything but a non-empty string.

import assert from 'node:assert/strict';
import test from 'node:test';

import { REFUSALS } from '../../../shared/mailbox-schema/index.js';
import { RELAY_CODES } from './relay.js';
import { TOKEN_CODES, MemoryTokenSource, readPushToken } from './token.js';

const TOKEN = 'not-a-real-token-0123456789';

/** A source that answers whatever it is given. */
function source(answer) {
  return { token: async () => answer };
}

test('a source that answers a token answers one', async () => {
  const read = await readPushToken(new MemoryTokenSource({ token: TOKEN }));
  assert.equal(read.ok, true);
  assert.equal(read.token, TOKEN);
});

test('the three ways of having no token are three codes, and none of them is a written field', async () => {
  // A source that answers "nothing" -- the port's own `ok: false`.
  assert.deepEqual({ ...(await readPushToken(source({ ok: false, code: TOKEN_CODES.NO_TOKEN }))) }, {
    ok: false,
    code: TOKEN_CODES.NO_TOKEN,
    field: 'token',
  });
  // ... and the shapes a naive source answers instead of a refusal. Each is
  // read as "no token", and never as a token.
  for (const answer of [null, undefined, '', { ok: true, token: '' }, { ok: true }]) {
    const read = await readPushToken(source(answer));
    assert.equal(read.ok, false, JSON.stringify(answer));
    assert.equal(read.code, TOKEN_CODES.NO_TOKEN, JSON.stringify(answer));
    assert.equal('token' in read, false, 'a refusal carries no token field at all');
  }
});

test('a source that throws is a broken device, and a source that refuses is not', async () => {
  const thrown = await readPushToken(new MemoryTokenSource({ fail: true }));
  assert.equal(thrown.ok, false);
  assert.equal(thrown.code, TOKEN_CODES.TOKEN_FAILED, 'a throw is not "this phone has no token"');
  // P5.2.8 and Q7's answer: a source with a reason of its own says so, and that
  // code is passed through rather than flattened into `no-token` -- the two have
  // different fixes. `relay.js` is the real one, and this is its code.
  const relayFailed = await readPushToken(source({ ok: false, code: RELAY_CODES.RELAY_FAILED, field: 'relay' }));
  assert.equal(relayFailed.code, RELAY_CODES.RELAY_FAILED);
});

test('a token the schema would refuse is refused, and the code is the schema’s', async () => {
  const read = await readPushToken(source({ ok: true, token: 't'.repeat(257) }));
  assert.equal(read.ok, false);
  assert.equal(read.code, REFUSALS.TOO_LONG);
  assert.equal(read.field, 'token');
});

test('the source is a port, and half a port is a wiring bug', async () => {
  for (const bad of [null, undefined, {}, { token: 'not-a-function' }, () => TOKEN]) {
    await assert.rejects(() => readPushToken(bad), TypeError);
  }
});

test('the memory source rotates, and says "none" differently from "failed"', async () => {
  const memory = new MemoryTokenSource({ token: TOKEN });
  assert.equal((await readPushToken(memory)).token, TOKEN);
  // §3.5's ordinary case: a token FCM rotated arrives on the next open.
  memory.set('not-a-real-token-rotated');
  assert.equal((await readPushToken(memory)).token, 'not-a-real-token-rotated');
  memory.set(null);
  assert.equal((await readPushToken(memory)).code, TOKEN_CODES.NO_TOKEN);
});
