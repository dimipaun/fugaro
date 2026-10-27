// SPDX-License-Identifier: LicenseRef-Fugaro-Enterprise
// See mobile/LICENSE.md for the terms.
// The per-account resources' tests (mobile plan, Task P3.1; decision D114).
//
// D114's two isolation sentences are what this file pins, and both are
// properties rather than examples:
//
//   * "**a per-account cache namespace**, no shared token store": two open
//     accounts have two namespaces, neither read can answer with the other's
//     value, and clearing one empties one;
//   * "the switcher **tears down the previous account's listeners and caches
//     before it opens the next**": the ORDER is asserted on the registry's own
//     trace, and the count of listeners left alive afterwards is asserted to
//     be zero -- including when a listener's own `stop()` throws.
//
// The last one is the case worth having: a teardown that gives up on the first
// error leaves a listener alive, which is a subscription still authenticated
// as the previous installation, delivering that installation's data into a
// screen now showing another account. So `close` stops everything it can,
// empties the cache, marks the account closed, and only then reports that
// something inside failed.

import assert from 'node:assert/strict';
import test from 'node:test';

import { CACHE_PREFIX, RESOURCE_CODES, ResourceRegistry, cacheNamespace } from './resources.js';

/** A listener that records that it was stopped. */
function listener(log, name) {
  return { stop: () => log.push(name) };
}

test('the namespace is per account, and composed only from a valid id', () => {
  assert.equal(cacheNamespace('laptop'), `${CACHE_PREFIX}laptop`);
  assert.notEqual(cacheNamespace('laptop'), cacheNamespace('phone'));
  assert.throws(() => cacheNamespace('a.b'), TypeError);
  assert.throws(() => cacheNamespace(''), TypeError);
});

test('opening an account is idempotent-by-refusal, not by silence', () => {
  const registry = new ResourceRegistry();
  assert.equal(registry.open('laptop').ok, true);
  assert.equal(registry.isOpen('laptop'), true);
  assert.deepEqual(registry.openAccounts(), ['laptop']);
  // Opening twice is refused: a caller that opened twice would skip a
  // teardown, and the second open would have replaced the listener set.
  assert.equal(registry.open('laptop').code, RESOURCE_CODES.ALREADY_OPEN);
  assert.equal(registry.open('a.b').code, RESOURCE_CODES.BAD_ACCOUNT);
  assert.deepEqual(registry.openAccounts(), ['laptop']);
});

test('a listener registered for a closed account is refused, so nothing leaks', () => {
  const registry = new ResourceRegistry();
  assert.equal(registry.add('laptop', () => {}).code, RESOURCE_CODES.NOT_OPEN);
  registry.open('laptop');
  assert.equal(registry.add('laptop', null).code, RESOURCE_CODES.BAD_LISTENER);
  assert.equal(registry.add('laptop', {}).code, RESOURCE_CODES.BAD_LISTENER);
  assert.equal(registry.add('laptop', { stop: 7 }).code, RESOURCE_CODES.BAD_LISTENER);
  assert.equal(registry.liveCount('laptop'), 0);
});

test('closing stops every listener, empties the cache and forgets the namespace', () => {
  const registry = new ResourceRegistry();
  registry.open('laptop');
  const log = [];
  registry.add('laptop', listener(log, 'one'));
  // A bare function, which is the shape the Realtime Database SDK's `onValue`
  // returns an unsubscribe in.
  registry.add('laptop', () => log.push('two'));
  assert.equal(registry.liveCount('laptop'), 2);
  const { cache } = registry.cacheFor('laptop');
  cache.set('feed', 'a value');
  assert.equal(cache.size(), 1);

  const closed = registry.close('laptop');
  assert.deepEqual(closed.stopped, 2);
  assert.deepEqual(log.sort(), ['one', 'two']);
  assert.equal(registry.liveCount('laptop'), 0);
  assert.equal(registry.isOpen('laptop'), false);
  assert.equal(cache.size(), 0, 'the cache is emptied, not merely unreachable');
  assert.equal(registry.cacheFor('laptop').code, RESOURCE_CODES.NOT_OPEN);
  assert.equal(registry.close('laptop').code, RESOURCE_CODES.NOT_OPEN);
});

test('a listener whose stop() throws does not leave the account half-closed', () => {
  const registry = new ResourceRegistry();
  registry.open('laptop');
  const log = [];
  registry.add('laptop', () => { throw new Error('socket already gone'); });
  registry.add('laptop', listener(log, 'later'));

  const closed = registry.close('laptop');
  assert.equal(closed.ok, false);
  assert.equal(closed.code, RESOURCE_CODES.TEARDOWN_FAILED);
  assert.equal(closed.failures, 1);
  // ... and the account is closed anyway: no listener alive, no cache, not
  // open. Fail closed in the direction that has nothing subscribed.
  assert.deepEqual(log, ['later']);
  assert.equal(registry.liveCount('laptop'), 0);
  assert.equal(registry.isOpen('laptop'), false);
});

test('no read from one account answers with another account\'s value', () => {
  const registry = new ResourceRegistry();
  registry.open('laptop');
  registry.open('phone');
  const laptop = registry.cacheFor('laptop').cache;
  const phone = registry.cacheFor('phone').cache;

  assert.notEqual(laptop.namespace, phone.namespace);
  laptop.set('feed', 'laptop value');
  phone.set('feed', 'phone value');
  assert.equal(laptop.get('feed'), 'laptop value');
  assert.equal(phone.get('feed'), 'phone value');
  assert.notEqual(laptop.get('feed'), phone.get('feed'));

  // Closing one account does not touch the other's cache.
  registry.close('laptop');
  assert.equal(phone.get('feed'), 'phone value');
  assert.equal(registry.cacheFor('laptop').code, RESOURCE_CODES.NOT_OPEN);
});

test('the trace records close before open, which is the switch D114 requires', () => {
  const trace = [];
  const registry = new ResourceRegistry({ trace });
  registry.open('laptop');
  registry.add('laptop', () => {});
  registry.close('laptop');
  registry.open('phone');
  assert.deepEqual(trace, [
    ['open', 'laptop'],
    ['listener', 'laptop'],
    ['close', 'laptop'],
    ['open', 'phone'],
  ]);
});

test('closeAll closes every open account and reports the ones that failed', () => {
  const registry = new ResourceRegistry();
  registry.open('laptop');
  registry.open('phone');
  registry.add('phone', () => { throw new Error('socket already gone'); });
  const closed = registry.closeAll();
  assert.equal(closed.ok, false);
  assert.equal(closed.failures, 1);
  assert.deepEqual(registry.openAccounts(), []);
  assert.equal(registry.closeAll().ok, true);
});

test('a cache key must be a real key', () => {
  const registry = new ResourceRegistry();
  registry.open('laptop');
  const { cache } = registry.cacheFor('laptop');
  assert.throws(() => cache.set('', 1), TypeError);
  assert.throws(() => cache.set(7, 1), TypeError);
  cache.set('feed', 1);
  assert.deepEqual(cache.keys(), ['feed']);
  assert.equal(cache.delete('feed'), true);
  assert.equal(cache.size(), 0);
});
