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

import { COMMAND, MAX_ENTRIES, ROUTE, routeUtterance } from '../../../shared/command/index.js';
import { AccountStore, MemorySecureStore, MemorySettingsStore, ResourceRegistry, SecretStore, SettingsStore } from '../accounts/index.js';
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

test('a label the voice cannot address is left out rather than poisoning the list', async () => {
  // The router refuses a **whole** list for one entry it cannot match
  // (`checkEntries`), and nothing under `mobile/src/` removes an account -- so a
  // store written before `session.js` judged labels (a `_`, or a `--`) would
  // leave every installation on the phone unreachable by voice, the `where-am-i`
  // route with them, and no in-app way back. This is the reading end of the same
  // rule: an id that folds to nothing can neither be spoken nor matched, so
  // leaving it out loses nothing that could have been routed, and the
  // installation is still in the overview and still switchable by press.
  const { store } = makeStore();
  await store.add(LAPTOP);
  await store.add({ id: '_', firebaseProjectId: 'fp-underscore', authUid: 'uid-underscore', fugaroProject: 'aurora' });
  const switcher = createSwitcher({ store });
  const listed = await switcher.projects();
  assert.equal(listed.ok, true);
  assert.deepEqual(listed.projects, [{ id: 'laptop', spokenName: 'laptop' }]);
  // **Not poisoned**, which is the whole point: the router still resolves a
  // name against this list, and the switch it routes still happens.
  const result = await route(switcher, 'Fugaro, switch to laptop');
  assert.equal(result.command, COMMAND.SWITCH_PROJECT);
  assert.equal((await switcher.switchTo(result)).ok, true);
  assert.equal(store.current(), 'laptop');
  // ... and the route that has no name in it is answered too.
  const where = routeUtterance({ utterance: 'Fugaro, what project am I on', projects: listed.projects });
  assert.equal(where.command, COMMAND.WHERE_AM_I);
  // What was left out is only the voice: the account is still held, and a press
  // still switches to it.
  assert.equal((await store.list()).accounts.length, 2);
  assert.equal((await store.switchTo('_')).ok, true);
  assert.equal(store.current(), '_');
});

test('two labels the voice folds into one name are refused, not chosen between', async () => {
  // `Laptop` beside `laptop` is one spoken name, and which of the two "Fugaro,
  // switch to laptop" meant is not this module's to decide -- so it refuses
  // rather than picking, with the router's own word for the condition. The
  // router would refuse the same list as a whole (`ambiguous-spoken-name`); this
  // module is where the owner can still see why.
  const { store } = makeStore();
  await store.add(LAPTOP);
  await store.add({ id: 'Laptop', firebaseProjectId: 'fp-laptop-2', authUid: 'uid-laptop-2', fugaroProject: 'aurora' });
  const switcher = createSwitcher({ store });
  const listed = await switcher.projects();
  assert.equal(listed.ok, false);
  assert.equal(listed.code, SWITCH_CODES.AMBIGUOUS_SPOKEN_NAME);
  assert.equal(listed.field, 'projects');
  // The code is the router's own, so a caller that acts on either vocabulary
  // sees one word for one thing -- and `routeUtterance` refuses the same two
  // entries with it.
  const folded = routeUtterance({
    utterance: 'Fugaro, switch to laptop',
    projects: [
      { id: 'Laptop', spokenName: 'Laptop' },
      { id: 'laptop', spokenName: 'laptop' },
    ],
  });
  assert.equal(folded.ok, false);
  assert.equal(folded.code, SWITCH_CODES.AMBIGUOUS_SPOKEN_NAME);
  assert.equal(folded.field, 'projects');
});

test('a list longer than the router will read is refused, not handed over', async () => {
  // The router refuses a list of more than `MAX_ENTRIES` entries **whole**
  // (`checkEntries`), so the poisoning above comes back through the list's
  // *length*: one account past the bound would make every "switch to <label>"
  // and every "where-am-i" refused for every installation, with no repair in
  // this build. The bound is the router's own constant, and the list it is
  // checked against is the one this module hands over.
  const { store } = makeStore();
  const added = [];
  for (let i = 0; i < MAX_ENTRIES; i += 1) {
    const id = `acct-${String(i).padStart(2, '0')}`;
    added.push(id);
    assert.equal((await store.add({ id, firebaseProjectId: `fp-${i}`, authUid: `uid-${i}`, fugaroProject: 'aurora' })).ok, true, id);
  }
  const switcher = createSwitcher({ store });
  const full = await switcher.projects();
  assert.equal(full.ok, true);
  assert.equal(full.projects.length, MAX_ENTRIES, 'the bound itself is not off by one');
  assert.equal((await routeUtterance({ utterance: 'Fugaro, switch to acct-00', projects: full.projects })).command, COMMAND.SWITCH_PROJECT);

  // One more account and the list is one the router refuses whole. The switcher
  // refuses it first, with the router's own word for it, so a caller is told the
  // *list* is the problem rather than every installation answering "not
  // configured" one at a time -- and nothing is handed over that the router
  // would refuse.
  await store.add({ id: 'acct-99', firebaseProjectId: 'fp-99', authUid: 'uid-99', fugaroProject: 'aurora' });
  const over = await switcher.projects();
  assert.equal(over.ok, false);
  assert.equal(over.code, SWITCH_CODES.TOO_MANY_PROJECTS);
  assert.equal(over.field, 'projects');
  // ... and it is the router's own refusal for the same list, which is what the
  // guard stands in front of.
  const raw = routeUtterance({
    utterance: 'Fugaro, switch to acct-00',
    projects: [...added, 'acct-99'].map((id) => ({ id, spokenName: id })),
  });
  assert.equal(raw.ok, false);
  assert.equal(raw.code, SWITCH_CODES.TOO_MANY_PROJECTS);

  // The bound is on the list handed over, not on the store: an id that folds to
  // nothing is left out above and does not count against it, so the same 33
  // accounts with one unaddressable label are still all readable.
  const { store: withUnspeakable } = makeStore();
  for (let i = 0; i < MAX_ENTRIES; i += 1) {
    await withUnspeakable.add({ id: `acct-${String(i).padStart(2, '0')}`, firebaseProjectId: `fp-${i}`, authUid: `uid-${i}`, fugaroProject: 'aurora' });
  }
  await withUnspeakable.add({ id: '_', firebaseProjectId: 'fp-underscore', authUid: 'uid-underscore', fugaroProject: 'aurora' });
  const listed = await createSwitcher({ store: withUnspeakable }).projects();
  assert.equal(listed.ok, true);
  assert.equal(listed.projects.length, MAX_ENTRIES);
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

test('a result that is not the app’s own switch is refused clause by clause', async () => {
  // The results above come out of the real router, and every one of them is
  // refused by more than one clause of the guard at once -- agent text is the
  // wrong route *and* has no command *and* is forwarded, so the guard could lose
  // any one of its three clauses and this file would still be green. What the
  // guard is for is the caller that hands over the wrong result (the screens
  // route their own transcripts and pass the results on: `text/input.js`,
  // `voice/dialogue.js`), so each result here differs from a real switch in
  // exactly one field and no clause is carried by another.
  const trace = [];
  const { store, registry } = makeStore({ trace });
  await store.add(LAPTOP);
  await store.add(PHONE);
  const switcher = createSwitcher({ store });
  await store.switchTo('laptop');
  registry.add('laptop', () => {});

  const real = Object.freeze({
    ok: true,
    route: ROUTE.APP_COMMAND,
    forwarded: false,
    command: COMMAND.SWITCH_PROJECT,
    project: Object.freeze({ id: 'phone', spokenName: 'phone' }),
  });
  // The control: this one switches, so the three refusals below are about the
  // clause each one breaks and not about a hand-built result as such.
  const switched = await switcher.switchTo({ ...real });
  assert.equal(switched.ok, true);
  assert.equal(switched.account.id, 'phone');
  await store.switchTo('laptop');
  registry.add('laptop', () => {});
  trace.length = 0;

  const forgeries = [
    // Agent text wearing the switch command: the case the header's paragraph is
    // about, and the one clause nothing else would catch.
    ['an app command that is forwarded', { ...real, forwarded: true }],
    ['a command that is another screen’s', { ...real, command: COMMAND.WHATS_PENDING }],
    ['a route that is not the app’s', { ...real, route: ROUTE.AGENT_TEXT }],
  ];
  for (const [why, result] of forgeries) {
    const refused = await switcher.switchTo(result);
    assert.equal(refused.ok, false, why);
    assert.equal(refused.code, SWITCH_CODES.NOT_A_SWITCH, why);
    assert.equal(refused.field, 'command', why);
    assert.equal(store.current(), 'laptop', why);
  }
  // A refused result is refused before the store is reached at all: nothing was
  // opened, closed or stopped, and the open account kept its listeners.
  assert.deepEqual(trace, [], 'nothing was opened, closed or stopped');
  assert.equal(registry.liveCount('laptop'), 1);
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
