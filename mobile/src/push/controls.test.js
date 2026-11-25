// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// §3.8's switches, as the phone holds them: their tests (mobile plan, Task
// P5.2.8; D123, D160).
//
// The plan's third verification sentence lives next door (the switches travel
// in the same message, `registration.test.js`); what is tested here is the
// record they travel FROM: that an untouched account reads as D160's defaults,
// that a record this build cannot read is a refusal rather than a plausible
// set of defaults, that a switch the schema would refuse is refused at the
// moment it is set, and that two flips at once do not lose one another.

import assert from 'node:assert/strict';
import test from 'node:test';

import { REFUSALS } from '../../../shared/mailbox-schema/index.js';
import { MemorySettingsStore, SETTINGS_CODES, SETTINGS_KEY_PREFIX, SettingsStore } from '../accounts/index.js';
import {
  CONTROL_CODES,
  CONTROL_DEFAULTS,
  CONTROL_NAMES,
  PUSH_SETTINGS_PREFIX,
  WINDOW_FIELDS,
  createPushControls,
} from './controls.js';

const ACCOUNT = 'belong';
const OTHER = 'laptop';
const ZONE = 'Europe/Berlin';

const rawKey = (id) => `${SETTINGS_KEY_PREFIX}${PUSH_SETTINGS_PREFIX}${id}`;

function controls(options = {}) {
  const port = new MemorySettingsStore();
  const store = new SettingsStore(port);
  return {
    port,
    store,
    door: createPushControls({
      settings: store,
      accountId: options.accountId ?? ACCOUNT,
      ...(options.zone === undefined ? {} : { zone: options.zone }),
    }),
  };
}

test('an account that never touched a switch reads D160’s defaults, and the record is not stored', async () => {
  const { door, port } = controls();
  const record = await door.read();
  assert.equal(record.ok, true);
  assert.deepEqual({ ...record.prefs }, { ...CONTROL_DEFAULTS });
  assert.equal(record.stored, false, 'nothing has been written: the defaults are not a record');
  assert.deepEqual(port.keys(), [], 'and no key was created by reading');
  // D160, as the one place the defaults exist: cap-near is the one that is off.
  assert.equal(CONTROL_DEFAULTS.cap_near, false);
  for (const name of CONTROL_NAMES) {
    if (name !== 'cap_near') assert.equal(CONTROL_DEFAULTS[name], true, name);
  }
  assert.equal(CONTROL_DEFAULTS.quiet_hours, undefined, 'quiet hours default off: there is no window');
  assert.equal(CONTROL_NAMES.length, 5);
});

test('the record is per account, under one settings key each', async () => {
  const port = new MemorySettingsStore();
  const store = new SettingsStore(port);
  const mine = createPushControls({ settings: store, accountId: ACCOUNT });
  const theirs = createPushControls({ settings: store, accountId: OTHER });
  assert.equal(mine.key, `${PUSH_SETTINGS_PREFIX}${ACCOUNT}`);
  assert.equal(theirs.key, `${PUSH_SETTINGS_PREFIX}${OTHER}`);
  await mine.setControl('cap_near', true);
  await theirs.setControl('prompt', false);
  assert.deepEqual(port.keys().sort(), [rawKey(ACCOUNT), rawKey(OTHER)].sort());
  assert.equal((await mine.read()).prefs.cap_near, true);
  assert.equal((await theirs.read()).prefs.prompt, false);
  assert.equal((await theirs.read()).prefs.cap_near, false, 'one phone’s switch is not another account’s');
  // An id the key cannot be composed from is a wiring bug, and never a key
  // shared between two accounts.
  for (const bad of [undefined, null, '', 42, 'a.b', 'a/token']) {
    assert.throws(() => createPushControls({ settings: store, accountId: bad }), TypeError, String(bad));
  }
});

test('a switch the schema would refuse is refused when it is set', async () => {
  const { door } = controls();
  const refused = await door.setControl('kill_switch', 'yes');
  assert.equal(refused.ok, false);
  assert.equal(refused.code, REFUSALS.WRONG_TYPE);
  assert.equal(refused.field, 'prefs.kill_switch');
  assert.equal((await door.read()).stored, false, 'a refused switch writes nothing');
  const unknown = await door.setControl('not_a_switch', true);
  assert.equal(unknown.code, REFUSALS.UNKNOWN_FIELD);
  assert.equal(unknown.field, 'prefs.not_a_switch');
});

test('a switch set for the first time lands over the defaults, and the record carries all five', async () => {
  const { door, port } = controls();
  const set = await door.setControl('cap_near', true);
  assert.equal(set.ok, true);
  assert.deepEqual({ ...set.prefs }, { ...CONTROL_DEFAULTS, cap_near: true });
  const stored = JSON.parse(await port.getItem(rawKey(ACCOUNT)));
  assert.deepEqual({ ...stored }, { ...CONTROL_DEFAULTS, cap_near: true });
  // Every one of the five is on the record, because the schema requires all of
  // them (PREFS_REQUIRED) -- a switch the app left out would be refused at the
  // far end.
  for (const name of CONTROL_NAMES) assert.equal(typeof stored[name], 'boolean', name);
});

test('two flips at once do not lose one another', async () => {
  const { door } = controls();
  await Promise.all([
    door.setControl('cap_near', true),
    door.setControl('prompt', false),
    door.setControl('consumer_offline', false),
  ]);
  const record = await door.read();
  assert.deepEqual(
    { ...record.prefs },
    { ...CONTROL_DEFAULTS, cap_near: true, prompt: false, consumer_offline: false },
  );
});

test('quiet hours need a zone from the device, and a window with a width', async () => {
  // No zone port at all: the window cannot be set, and the refusal says which
  // field is missing rather than storing an offset that would drift at a DST
  // change (§3.8, Q5).
  const noZone = controls();
  const refused = await noZone.door.setQuietHours({ start: 1320, end: 420 });
  assert.equal(refused.ok, false);
  assert.equal(refused.code, CONTROL_CODES.NO_ZONE);
  assert.equal(refused.field, 'prefs.quiet_hours.zone');
  // A zone source that answers nothing, throws, or answers a non-string is the
  // same fact as no source.
  for (const zone of [() => null, () => '', () => { throw new Error('no zone'); }, () => 42]) {
    const one = controls({ zone });
    assert.equal((await one.door.setQuietHours({ start: 1320, end: 420 })).code, CONTROL_CODES.NO_ZONE);
  }

  const { door } = controls({ zone: () => ZONE });
  const set = await door.setQuietHours({ start: 1320, end: 420 });
  assert.equal(set.ok, true);
  assert.deepEqual({ ...set.prefs.quiet_hours }, { zone: ZONE, start: 1320, end: 420 });
  assert.deepEqual([...WINDOW_FIELDS], ['zone', 'start', 'end']);
  // The zone stored is the NAME the device answered, and the window is the
  // device's own zone: a caller cannot name one (§3.8: the zone comes from the
  // OS, so a phone that travels carries the zone it is in).
  const odd = await door.setQuietHours({ zone: 'Pacific/Auckland', start: 1320, end: 420 });
  assert.equal(odd.prefs.quiet_hours.zone, ZONE);

  // A window with no width is refused by the schema, with its own field.
  const flat = await door.setQuietHours({ start: 420, end: 420 });
  assert.equal(flat.ok, false);
  assert.equal(flat.code, REFUSALS.BAD_WINDOW);
  assert.equal(flat.field, 'prefs.quiet_hours.end');
  // ... and one end outside the day.
  assert.equal((await door.setQuietHours({ start: 1440, end: 420 })).code, REFUSALS.BAD_WINDOW);

  // Turning them off leaves a record the schema accepts, with no window key.
  const cleared = await door.clearQuietHours();
  assert.equal(cleared.ok, true);
  assert.equal('quiet_hours' in cleared.prefs, false);
  assert.equal('quiet_hours' in (await door.read()).prefs, false);
});

test('a record this build cannot read is a refusal, and reset is the one repair', async () => {
  const port = new MemorySettingsStore();
  const store = new SettingsStore(port);
  const door = createPushControls({ settings: store, accountId: ACCOUNT });
  // A record with a switch this build does not have: "the field set IS the
  // version", so it is refused rather than read past.
  port.setItem(rawKey(ACCOUNT), JSON.stringify({ ...CONTROL_DEFAULTS, future_switch: true }));
  const refused = await door.read();
  assert.equal(refused.ok, false);
  assert.equal(refused.code, REFUSALS.UNKNOWN_FIELD);
  assert.equal(refused.field, 'prefs.future_switch');
  // A record that is not an object at all is the same class of answer.
  for (const stored of ['true', '[]', '"on"', '{}', 'null']) {
    port.setItem(rawKey(ACCOUNT), stored);
    const read = await door.read();
    assert.equal(read.ok, false, stored);
    assert.equal(read.code === CONTROL_CODES.UNREADABLE_RECORD || read.code === REFUSALS.MISSING_FIELD, true, stored);
  }
  // The repair writes the defaults, and the record is readable again.
  port.setItem(rawKey(ACCOUNT), JSON.stringify({ ...CONTROL_DEFAULTS, future_switch: true }));
  assert.equal((await door.reset()).ok, true);
  const after = await door.read();
  assert.equal(after.ok, true);
  assert.deepEqual({ ...after.prefs }, { ...CONTROL_DEFAULTS });
});

test('a store that cannot answer is a refusal, and never a default', async () => {
  const failing = {
    get: async () => { throw new Error('the store is gone'); },
    set: async () => { throw new Error('the store is gone'); },
  };
  const door = createPushControls({ settings: failing, accountId: ACCOUNT });
  const read = await door.read();
  assert.equal(read.ok, false);
  assert.equal(read.code, SETTINGS_CODES.STORE_FAILED);
  assert.equal((await door.setControl('prompt', false)).code, SETTINGS_CODES.STORE_FAILED);
  // A store that answers something which is not a settings answer is the same
  // class of fact.
  for (const answer of [null, undefined, {}, 'absent', 42]) {
    const odd = createPushControls({ settings: { get: async () => answer, set: async () => answer }, accountId: ACCOUNT });
    assert.equal((await odd.read()).ok, false, JSON.stringify(answer));
  }
  // Half a store is a wiring bug.
  for (const bad of [null, {}, { get: async () => ({ ok: false, code: 'absent' }) }]) {
    assert.throws(() => createPushControls({ settings: bad, accountId: ACCOUNT }), TypeError);
  }
  assert.throws(() => createPushControls({ settings: failing, accountId: ACCOUNT, zone: 'Europe/Berlin' }), TypeError);
});
