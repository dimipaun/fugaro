// The confirmation ruling's policy half (mobile identity and pairing plan, Task
// J3; decision D2786).
//
// The machine's own suite (`index.test.js`) proves the *procedure*: a costly
// start is read back, confirmed and bound to its mode. This file proves the
// **policy** the procedure asks: which starts are costly, when the biometric is
// asked for, where the defaults come from, and that this module's reading of an
// unreadable policy is the stricter one.

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  DEFAULT_BIOMETRIC_ON_COSTLY,
  DEFAULT_COSTLY_MODES,
  MAX_COSTLY_MODES,
  MAX_MODE_LENGTH,
  costlyStart,
  isCostlyStart,
} from './policy.js';

// The same two, through the module's own entry point: a surface reads the
// policy from `index.js`, so what it re-exports must be what this file tests.
import {
  costlyStart as viaIndex,
  DEFAULT_COSTLY_MODES as defaultsViaIndex,
  MAX_COSTLY_MODES as maxModesViaIndex,
} from './index.js';

test('the entry point re-exports the policy surface, and it is the same surface', () => {
  assert.equal(viaIndex, costlyStart, 'index.js must hand the same function, not a copy');
  assert.deepEqual([...defaultsViaIndex], [...DEFAULT_COSTLY_MODES]);
  assert.equal(maxModesViaIndex, MAX_COSTLY_MODES);
});

test('the defaults are P6\'s mobile policy values, frozen', () => {
  // `internal/projectpolicy`'s `mobile.costly_modes` default and
  // `mobile.biometric_on_costly` default: pro and critical, biometric off.
  assert.deepEqual([...DEFAULT_COSTLY_MODES], ['pro', 'critical']);
  assert.equal(DEFAULT_BIOMETRIC_ON_COSTLY, false);
  assert.ok(Object.isFrozen(DEFAULT_COSTLY_MODES));
  // And the bounds are C2's own for the same two values.
  assert.equal(MAX_COSTLY_MODES, 32);
  assert.equal(MAX_MODE_LENGTH, 64);
});

test('a start in a costly mode is costly, and one outside the list is not', () => {
  const modes = ['pro', 'critical'];
  assert.equal(isCostlyStart('pro', modes), true);
  assert.equal(isCostlyStart('critical', modes), true);
  // The modes a project does not call costly: D162's plain start.
  for (const mode of ['poc', 'startup', 'hotfix', 'PRO', 'pro ']) {
    assert.equal(isCostlyStart(mode, modes), false, `${mode} is not in the list`);
  }
  // An empty list makes every start plain -- a project that calls nothing
  // costly is a project that has said so (D2786 is about *named* costly modes).
  assert.equal(isCostlyStart('pro', []), false);
});

test('a list this module cannot read makes every start costly, never none', () => {
  // The fail-closed reading: `costlyStart` is only ever handed a list the state
  // reader has already accepted, so this is what a caller that skipped the
  // reader gets -- and it must be the stricter answer.
  for (const broken of [undefined, null, 'pro', 42, {}, { pro: true }]) {
    assert.equal(isCostlyStart('poc', broken), true, `${String(broken)} must read as costly`);
  }
});

test('the biometric is asked for on a costly start only, and a switch that is not false asks for it', () => {
  const modes = ['pro', 'critical'];

  // Off: a costly start is read back, with no biometric.
  assert.deepEqual(costlyStart('pro', modes, false), { costly: true, biometric: false });
  // On: a costly start asks for it (P5.1g's path).
  assert.deepEqual(costlyStart('pro', modes, true), { costly: true, biometric: true });

  // The switch is named `biometric_on_costly`: a *plain* start never asks for
  // it, whatever the switch says.
  assert.deepEqual(costlyStart('poc', modes, true), { costly: false, biometric: false });
  assert.deepEqual(costlyStart('poc', modes, false), { costly: false, biometric: false });

  // An unreadable switch asks for it, which is the stricter reading.
  for (const broken of [undefined, null, 'yes', 1, {}]) {
    assert.equal(costlyStart('pro', modes, broken).biometric, true, `${String(broken)} must ask for the biometric`);
  }

  // A plain start with an unreadable switch asks for nothing: there is no
  // read-back to attach a biometric to.
  assert.deepEqual(costlyStart('poc', modes, undefined), { costly: false, biometric: false });
});

test('the answer is frozen and is a function of the three values alone', () => {
  const answer = costlyStart('pro', ['pro'], true);
  assert.ok(Object.isFrozen(answer));
  assert.deepEqual(costlyStart('pro', ['pro'], true), answer);
  // The same inputs twice, and the list is not kept: editing it afterwards
  // cannot change a later answer, because the answer is computed, not stored.
  const modes = ['pro'];
  assert.equal(isCostlyStart('pro', modes), true);
  modes.length = 0;
  assert.equal(isCostlyStart('pro', []), false);
});
