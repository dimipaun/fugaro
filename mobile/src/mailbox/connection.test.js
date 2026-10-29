// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The connection port's double (mobile plan, Task P6.10; D167).
//
// Small on purpose: it is one boolean and a log of the questions asked of it.
// The log is the part with a rule behind it -- "the gate was asked BEFORE
// anything was written" is an ordering `client.test.js` asserts, and it can only
// assert it if the port remembers being asked.

import assert from 'node:assert/strict';
import test from 'node:test';

import { MemoryConnection } from './connection.js';

test('the double is connected unless a test says otherwise', () => {
  // A test that is about signing, acking or the outbox must not silently be a
  // test of the offline gate.
  assert.equal(new MemoryConnection().isOnline(), true);
  assert.equal(new MemoryConnection({}).isOnline(), true);
  assert.equal(new MemoryConnection({ online: undefined }).isOnline(), true);
  assert.equal(new MemoryConnection({ online: true }).isOnline(), true);
  assert.equal(new MemoryConnection({ online: false }).isOnline(), false);
});

test('the constructor is no more generous than the gate', () => {
  // `setOnline`'s rule -- only `true` is connected -- is the constructor's too,
  // and it was not: `options.online !== false` reported `{online: 0}`,
  // `{online: null}` and `{online: ''}` as ONLINE, so a test written with a
  // non-boolean falsy silently built a connected phone and tested the gate's
  // inverse. `undefined` is the one value that means "not given".
  for (const value of [0, 1, -1, 'online', 'true', '', {}, [], null, Number.NaN]) {
    assert.equal(new MemoryConnection({ online: value }).isOnline(), false, String(value));
  }
  // And the default is a real default, not a hole: no options at all is online.
  assert.equal(new MemoryConnection().isOnline(), true);
});

test('the connection can be cut and restored, and only `true` is online', () => {
  const connection = new MemoryConnection();
  assert.equal(connection.setOnline(false).isOnline(), false);
  assert.equal(connection.setOnline(true).isOnline(), true);
  assert.equal(connection.setOnline().isOnline(), true);
  // **Only `true` is connected**, which is `evaluateGate`'s own rule written a
  // second time so that the double cannot be more generous than the gate: a test
  // that says `setOnline(1)` is a test of an offline phone.
  for (const value of [0, 1, -1, 'online', 'true', {}, [], null, Number.NaN]) {
    assert.equal(connection.setOnline(value).isOnline(), false, String(value));
  }
  // `setOnline` answers the port itself, so a test can write it in one line.
  assert.equal(connection.setOnline(true), connection);
});

test('every question is remembered, in order', () => {
  const connection = new MemoryConnection();
  connection.isOnline();
  connection.setOnline(false);
  connection.isOnline();
  connection.isOnline();
  assert.deepEqual(connection.asked(), [true, false, false]);
  assert.equal(Object.isFrozen(connection.asked()), true);
  // The log is a copy: reading it does not consume it, and the port keeps
  // answering the same question the same way.
  assert.deepEqual(connection.asked(), [true, false, false]);
  assert.equal(connection.isOnline(), false);
});
