// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The wire node shapes (mobile plan, Task P3.2; D112's answer, tension (b1)).
//
// The plan's named rule -- "a missing field becomes `unavailable` and never
// `0`" -- is a rule about a *figure*, and this module is where the figures come
// from, so the cases here are the pairs the tree's two kinds of field turn on:
//
//   * **a cap is a pointer**: unset (absent, `null`, no node) is `unavailable`,
//     never a cap of zero, and `/config/caps/defaults` is the fallback
//     `Caps.RepoDaily()` names.
//   * **a counter is a plain field with `omitempty`**: absent is zero, which is
//     what the CLI's own document carries -- but a node that is *present* and
//     unreadable is `unavailable`, because the phone can tell the two apart and
//     §10.13 rule 5 says a missing field is never `0`.

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  MAX_INSTANT_MS,
  MICROS_PER_USD,
  NODE_CODES,
  capMicros,
  readCaps,
  readCounters,
  readHeartbeat,
  readKill,
  UNREADABLE_KILL,
  usd,
} from './nodes.js';

const CAP = {
  global: { dailyMicros: 50 * MICROS_PER_USD, perRunMicros: 2 * MICROS_PER_USD },
  defaults: { repoDailyMicros: 10 * MICROS_PER_USD, repoPerRunMicros: MICROS_PER_USD },
  repo: { dailyMicros: 5 * MICROS_PER_USD, repo: 'example/one' },
};

test('micro-dollars become dollars, and a value that is not an amount does not', () => {
  assert.equal(MICROS_PER_USD, 1_000_000);
  assert.equal(usd(0), 0, 'a measured zero is a reading');
  assert.equal(usd(12_500_000), 12.5);
  assert.equal(usd(1), 0.000001, 'a sub-dollar amount is not rounded away');
  for (const bad of [-1, 1.5, NaN, Infinity, '12', null, undefined, {}, []]) {
    assert.equal(usd(bad), null, String(bad));
  }
});

test('an unset cap is unavailable, never a cap of zero', () => {
  assert.equal(capMicros(CAP.repo, 'dailyMicros'), 5 * MICROS_PER_USD);
  assert.equal(capMicros({ dailyMicros: 0 }, 'dailyMicros'), 0, 'a cap of zero is a real cap, and it denies everything');
  // Every way a cap can be unset reads the same way.
  for (const node of [{}, { dailyMicros: null }, null, undefined, 'a string', [], { dailyMicros: -1 }, { dailyMicros: '5' }]) {
    assert.equal(capMicros(node, 'dailyMicros'), null, JSON.stringify(node));
  }
  // ... and never as zero, which is the assertion the plan's rule makes.
  assert.notEqual(capMicros({}, 'dailyMicros'), 0);
});

test('a repository cap falls back to the defaults, and then to nothing at all', () => {
  assert.equal(readCaps(CAP).caps.daily, 5, 'the repository\'s own node wins');
  assert.equal(readCaps({ global: CAP.global, defaults: CAP.defaults }).caps.daily, 10, 'else the defaults');
  // The project's day cap is not a repository's fallback: it is the project's.
  assert.equal(readCaps({ global: CAP.global }).caps.daily, null);
  assert.equal(readCaps({ global: CAP.global }).caps.globalDaily, 50);
  assert.equal(readCaps({}).caps.globalDaily, null);
  // `unavailable`, not a cap of zero.
  assert.notEqual(readCaps({}).caps.daily, 0);
  assert.notEqual(readCaps({}).caps.globalDaily, 0);
  // The per-run caps are not read: the document carries no per-run figure, so
  // there is nothing here to get wrong about the tree's `min` of the two sides.
  assert.deepEqual(Object.keys(readCaps(CAP).caps).sort(), ['daily', 'globalDaily']);
});

test('a counter is zero when it is not there, and unavailable when it cannot be read', () => {
  // Absent: the node is not there, so the day counted nothing. This is the
  // CLI's own reading (`internal/watch/build.go`'s `counters()`). Whether the
  // stream has *answered* is a different fact, and `reader.js` tracks it.
  for (const absent of [undefined, null]) {
    const read = readCounters(absent);
    assert.equal(read.ok, true);
    assert.deepEqual(read.counters, { counted: 0, spent: 0, notional: 0 });
  }
  // A node with a missing field is the same zero (`Counters.Counted` has
  // `omitempty`).
  const partial = readCounters({ calls: 3, byModel: { 'a-model': { micros: 1 } } });
  assert.deepEqual(partial.counters, { counted: 0, spent: 0, notional: 0 });
  // Present and readable.
  const read = readCounters({ counted: 12_500_000, spent: 12_000_000, notional: 40 * MICROS_PER_USD, calls: 2 });
  assert.deepEqual(read.counters, { counted: 12.5, spent: 12, notional: 40 });
  // A negative count is clamped, as `nonneg` does: a counter cannot be owed.
  assert.equal(readCounters({ counted: -5 }).counters.counted, 0);
  // Present and *unreadable* is unavailable, never zero -- the phone knows
  // more than the CLI did, and §10.13 rule 5 says never `0`.
  for (const bad of ['12', {}, [], true, NaN, Infinity, 1.5]) {
    const judged = readCounters({ counted: bad });
    assert.equal(judged.ok, true, String(bad));
    assert.equal(judged.counters.counted, null, String(bad));
    assert.notEqual(judged.counters.counted, 0, String(bad));
  }
  // A node that is not an object at all is refused, not read as empty.
  for (const bad of ['a string', 7, [], true]) {
    assert.equal(readCounters(bad).code, NODE_CODES.NOT_AN_OBJECT, String(bad));
  }
});

test('a kill switch that is not there is off, and one that cannot be read is on', () => {
  // `State.kill`'s rule, enforced rather than only documented: a switch that
  // cannot be read is a switch that might be on, and the safe direction for a
  // stop switch is to believe it.
  for (const unreadable of ['a string', 7, [], true, { on: 'yes' }, { on: 1 }]) {
    assert.deepEqual(readKill(unreadable).kill, { on: true, by: null, at: null, reason: UNREADABLE_KILL }, JSON.stringify(unreadable));
  }
  assert.equal(readKill(undefined).kill, null);
  assert.equal(readKill(null).kill, null);
  assert.equal(readKill({}).kill, null, 'a node with no `on` is not a switch that is on');
  assert.equal(readKill({ on: false, by: 'operator' }).kill, null);
  const on = readKill({ on: true, by: 'operator', at: 1_700_000_000_000, reason: 'cap' });
  assert.deepEqual(on.kill, { on: true, by: 'operator', at: '2023-11-14T22:13:20.000Z', reason: 'cap' });
  // A field that is missing or unusable is a missing field, never a made-up
  // one -- and the epoch is not an instant the switch was thrown at.
  const bare = readKill({ on: true, at: 0 }).kill;
  assert.deepEqual(bare, { on: true, by: null, at: null, reason: null });
  assert.equal(readKill({ on: true, at: '1700000000000' }).kill.at, null);
});

test('an instant past what a JS Date can render is a node this app cannot read', () => {
  // A nanosecond timestamp is a real thing to find in a tree, and an `int64`
  // reaches 9.22e18: Go decodes it and prints a year far past any calendar,
  // while `new Date(1.7e18).toISOString()` **throws** a RangeError. A reader
  // that reached that call would throw while assembling a document from a node
  // it had already stored -- so every later assembly would throw too.
  assert.throws(() => new Date(1_700_000_000_000_000_000).toISOString(), RangeError, 'the call this guards');
  const nanoseconds = readKill({ on: true, by: 'operator', at: 1_700_000_000_000_000_000, reason: 'cap' }).kill;
  // The node is there and its switch is readable, but its instant is not, and a
  // switch this app cannot read fully is a switch it believes: ON, with the
  // reason saying why. What it must **not** do is claim to have read the node
  // and quietly drop the instant.
  assert.deepEqual(nanoseconds, { on: true, by: null, at: null, reason: UNREADABLE_KILL });
  // The ceiling itself is fine, and one past it is not.
  assert.equal(readKill({ on: true, at: MAX_INSTANT_MS }).kill.at, new Date(MAX_INSTANT_MS).toISOString());
  assert.deepEqual(readKill({ on: true, at: MAX_INSTANT_MS + 1 }).kill, { on: true, by: null, at: null, reason: UNREADABLE_KILL });
  assert.deepEqual(readKill({ on: true, at: -MAX_INSTANT_MS - 1 }).kill, { on: true, by: null, at: null, reason: UNREADABLE_KILL });
  // ... and the same bound holds for a heartbeat's instant, which is read as a
  // number and never rendered here.
  assert.equal(readHeartbeat({ at: 1_700_000_000_000_000_000 }).heartbeat.at, null);
  assert.equal(readHeartbeat({ at: 1_700_000_000_000 }).heartbeat.at, 1_700_000_000_000);
});

test('the heartbeat is read, and no presence verdict is drawn from it', () => {
  // P4.2 (check 84) owns what staleness means; this only reads the node.
  assert.deepEqual(readHeartbeat(undefined).heartbeat, { present: false, at: null, boot: null, agents: null });
  assert.deepEqual(readHeartbeat(null).heartbeat, { present: false, at: null, boot: null, agents: null });
  const heard = readHeartbeat({ at: 1_700_000_000_000, boot: 'b1', agents: [{ id: 'a' }, { id: 'b' }], mac: 'x' });
  assert.deepEqual(heard.heartbeat, { present: true, at: 1_700_000_000_000, boot: 'b1', agents: 2 });
  // A heartbeat with no usable instant is present but gives no instant: it is
  // not "offline" and it is not "now".
  const silent = readHeartbeat({ boot: 'b1' }).heartbeat;
  assert.equal(silent.present, true);
  assert.equal(silent.at, null);
  assert.notEqual(silent.at, 0);
  assert.equal(readHeartbeat('a string').code, NODE_CODES.NOT_AN_OBJECT);
});

test('the module reads leniently, and refuses only what it cannot read at all', () => {
  // A tree that grows a field does not break the feed: `byModel` and `calls`
  // are on the wire and this module does not read them.
  const grown = readCounters({ counted: MICROS_PER_USD, byModel: { m: { micros: 1, in: 2 } }, calls: 9, future: 'x' });
  assert.equal(grown.ok, true);
  assert.equal(grown.counters.counted, 1);
  // ... and the caps nodes, which carry a `repo` name this reader does not use.
  assert.equal(readCaps({ repo: { dailyMicros: MICROS_PER_USD, repo: 'example/one', future: 1 } }).ok, true);
});
