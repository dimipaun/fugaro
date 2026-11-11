// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The overview's switcher (mobile plan, Task P6.8; D114, D164, and P6.1's
// commands).
//
// P6.8's last verification bullet is this file's subject: "switching by voice
// (P6.1's commands) tears down the previous account first (D114)". It is
// asserted the only way it can be a fact rather than a hope -- over a real
// `AccountStore`, with a listener registered on the open account, and the
// registry's trace read afterwards:
//
//   * the trace shows `close` before `open`;
//   * the previous account's listener has been stopped and its cache is gone;
//   * exactly one account is open at the end.
//
// The utterance goes through P6.1's real router, not a hand-built result: the
// grammar is the router's, and a test that faked the route would be testing
// this module against its own idea of what "Fugaro, switch to phone" means.
//
// Nothing here reads a device: the store is over in-memory ports and every
// value is synthetic.

import assert from 'node:assert/strict';
import test from 'node:test';

import { COMMAND, ROUTE, routeUtterance } from '../../../shared/command/index.js';
import { ResourceRegistry } from '../accounts/resources.js';
import { MemorySecureStore, SecretStore } from '../accounts/secure-store.js';
import { MemorySettingsStore, SettingsStore } from '../accounts/settings.js';
import { AccountStore } from '../accounts/store.js';
import { SWITCH_CODES, createSwitcher } from './switcher.js';

const LAPTOP = Object.freeze({ id: 'laptop', firebaseProjectId: 'fp-laptop', authUid: 'uid-laptop', fugaroProject: 'aurora' });
const PHONE = Object.freeze({ id: 'phone', firebaseProjectId: 'fp-phone', authUid: 'uid-phone', fugaroProject: 'aurora' });

/** A store over fresh in-memory ports, with the registry's trace. */
function makeStore({ trace } = {}) {
  const registry = new ResourceRegistry(trace === undefined ? {} : { trace });
  const store = new AccountStore({
    secrets: new SecretStore(new MemorySecureStore()),
    settings: new SettingsStore(new MemorySettingsStore()),
    registry,
  });
  return { store, registry };
}

/** Route one utterance the way the screens do: against the switcher's own
 * list. */
async function route(switcher, utterance) {
  const listed = await switcher.projects();
  assert.equal(listed.ok, true);
  return routeUtterance({ utterance, projects: listed.projects });
}

test('the router’s list is the store’s own, and an installation is its label', async () => {
  const { store } = makeStore();
  await store.add(LAPTOP);
  await store.add(PHONE);
  const switcher = createSwitcher({ store });
  assert.deepEqual(await switcher.projects(), {
    ok: true,
    projects: [
      { id: 'laptop', spokenName: 'laptop' },
      { id: 'phone', spokenName: 'phone' },
    ],
  });
  // D114: the list is typed by the owner, so a fresh store's is empty -- and an
  // empty list is a real answer, not a refusal.
  const fresh = createSwitcher({ store: makeStore().store });
  assert.deepEqual(await fresh.projects(), { ok: true, projects: [] });
});

test('“Fugaro, switch to phone” tears the previous account down before it opens the next', async () => {
  const trace = [];
  const { store, registry } = makeStore({ trace });
  await store.add(LAPTOP);
  await store.add(PHONE);
  const switcher = createSwitcher({ store });

  await store.switchTo('laptop');
  const stopped = [];
  registry.add('laptop', () => stopped.push('laptop-listener'));
  assert.equal(registry.liveCount('laptop'), 1);

  const result = await route(switcher, 'Fugaro, switch to phone');
  // The router's own half: the wake word was heard, the name resolved against
  // the store's list, and the utterance does **not** leave the app.
  assert.equal(result.ok, true);
  assert.equal(result.route, ROUTE.APP_COMMAND);
  assert.equal(result.command, COMMAND.SWITCH_PROJECT);
  assert.equal(result.forwarded, false);
  assert.deepEqual(result.project, { id: 'phone', spokenName: 'phone' });

  const switched = await switcher.switchTo(result);
  assert.equal(switched.ok, true);
  assert.equal(switched.changed, true);
  assert.equal(switched.account.id, 'phone');
  assert.equal(store.current(), 'phone');
  // **D114, the ordering.** The previous account's listener is stopped, its
  // cache is gone, and the trace says the close came first.
  assert.deepEqual(stopped, ['laptop-listener']);
  assert.equal(registry.liveCount('laptop'), 0);
  assert.equal(registry.cacheFor('laptop').code, 'not-open');
  assert.deepEqual(trace, [
    ['open', 'laptop'],
    ['listener', 'laptop'],
    ['close', 'laptop'],
    ['open', 'phone'],
  ]);
  assert.deepEqual(registry.openAccounts(), ['phone'], 'never two installations live at once');
});

test('a switch to the account that is already open changes nothing and tears nothing down', async () => {
  const trace = [];
  const { store, registry } = makeStore({ trace });
  await store.add(PHONE);
  const switcher = createSwitcher({ store });
  await store.switchTo('phone');
  registry.add('phone', () => {});
  const again = await switcher.switchTo(await route(switcher, 'Fugaro, switch to phone'));
  assert.equal(again.ok, true);
  assert.equal(again.changed, false);
  assert.equal(registry.liveCount('phone'), 1, 'the account that stays open keeps its listeners');
});

test('a name the store does not hold is refused before anything is opened or closed', async () => {
  const trace = [];
  const { store, registry } = makeStore({ trace });
  await store.add(LAPTOP);
  const switcher = createSwitcher({ store });
  await store.switchTo('laptop');
  registry.add('laptop', () => {});
  trace.length = 0;

  // The router is the first gate: a name that is not configured is not
  // understood, and it is not forwarded either (D164's fail-closed half).
  const unmatched = await route(switcher, 'Fugaro, switch to attic');
  assert.equal(unmatched.ok, true);
  assert.equal(unmatched.route, ROUTE.NOT_UNDERSTOOD);
  assert.equal(unmatched.forwarded, false);
  assert.equal(unmatched.reason, 'name-not-configured');

  // The second gate is here, for the window between routing and switching: the
  // installation was in the list a moment ago and is gone now. The current
  // account is not torn down for an account that cannot be opened -- a
  // teardown followed by a failed open is exactly the "no account open" state
  // `store.switchTo` refuses to reach by accident.
  const stale = await switcher.switchTo({ ok: true, route: ROUTE.APP_COMMAND, forwarded: false, command: COMMAND.SWITCH_PROJECT, project: { id: 'attic', spokenName: 'attic' } });
  assert.equal(stale.ok, false);
  assert.equal(stale.code, SWITCH_CODES.NO_SUCH_ACCOUNT);
  assert.equal(store.current(), 'laptop');
  assert.deepEqual(trace, [], 'nothing was opened, closed or stopped');
  assert.equal(registry.liveCount('laptop'), 1);
});

test('only a switch is a switch: agent text, other commands and refusals pass through', async () => {
  const { store } = makeStore();
  await store.add(LAPTOP);
  await store.add(PHONE);
  const switcher = createSwitcher({ store });
  await store.switchTo('laptop');

  // No wake word: agent text. The switcher must not act on it -- and, more
  // importantly, must not switch on it.
  const agent = await route(switcher, 'switch to phone');
  assert.equal(agent.route, ROUTE.AGENT_TEXT);
  assert.equal(agent.forwarded, true);
  const onAgent = await switcher.switchTo(agent);
  assert.equal(onAgent.ok, false);
  assert.equal(onAgent.code, SWITCH_CODES.NOT_A_SWITCH);
  assert.equal(store.current(), 'laptop', 'the account did not change');

  // A command that is another screen's ("what's pending") is not a switch
  // either, and this module says so rather than guessing.
  const pending = await route(switcher, "Fugaro, what's pending");
  assert.equal(pending.command, COMMAND.WHATS_PENDING);
  assert.equal((await switcher.switchTo(pending)).code, SWITCH_CODES.NOT_A_SWITCH);

  // The router's own refusal is carried unchanged, code and field: a caller
  // that gets `invalid-aliases` should see the router's word for it.
  const bad = await switcher.switchTo({ ok: false, code: 'invalid-projects', field: 'projects[0].id' });
  assert.equal(bad.code, 'invalid-projects');
  assert.equal(bad.field, 'projects[0].id');
  // ... and a non-result is refused rather than treated as one.
  assert.equal((await switcher.switchTo(null)).code, SWITCH_CODES.NOT_A_ROUTE);
  assert.equal((await switcher.switchTo('switch to phone')).code, SWITCH_CODES.NOT_A_ROUTE);
});

test('“what project am I on” answers from the store, and says so when nothing is open', async () => {
  const { store } = makeStore();
  await store.add(LAPTOP);
  const switcher = createSwitcher({ store });
  assert.deepEqual(await switcher.whereAmI(), { ok: true, account: null });
  await store.switchTo('laptop');
  const where = await switcher.whereAmI();
  assert.equal(where.ok, true);
  assert.deepEqual(where.account, LAPTOP);
  assert.deepEqual(switcher.handles, [COMMAND.SWITCH_PROJECT, COMMAND.WHERE_AM_I]);
});

test('the switcher refuses to be built without a store', () => {
  assert.throws(() => createSwitcher(), TypeError);
  assert.throws(() => createSwitcher({ store: null }), TypeError);
  // A store without the two methods this module calls is not a store: the
  // refusal is at construction, not at the first utterance.
  assert.throws(() => createSwitcher({ store: { list: async () => ({ ok: true, accounts: [] }) } }), TypeError);
});
