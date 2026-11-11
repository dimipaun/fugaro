// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The overview module's door (mobile plan, Task P6.8).
//
// The exported surface is asserted as an **exact set**, because it is a
// decision: every name here is something a screen, the entry or a test may
// reach, and a name added without this list changing is a name nobody chose.
//
// The rest of the file is the door's own job -- binding the store to the
// switcher and the primitives to the screen in one call -- plus the two
// properties that make the door worth having: a missing port is refused at
// construction rather than at the first utterance, and the tree it builds
// carries no credential and no summed total.

import assert from 'node:assert/strict';
import test from 'node:test';

import * as overview from './index.js';
import { AccountStore, MemorySecureStore, MemorySettingsStore, ResourceRegistry, SecretStore, SettingsStore } from '../accounts/index.js';

/** The surface, sorted: the module's contract with the rest of the app. */
const SURFACE = Object.freeze([
  'EMPTY_SENTENCE',
  'FIGURES',
  'NO_DAY',
  'OVERVIEW_TITLE',
  'REFUSAL_SENTENCE',
  'SCREEN',
  'SWITCH_CODES',
  'SWITCH_WORD',
  'TILE_CODES',
  'TILE_FIELDS',
  'TILE_FIGURES',
  'UNKNOWN_LIVENESS_SENTENCE',
  'UNKNOWN_PROJECT',
  'ageText',
  'createOverview',
  'createOverviewScreen',
  'createSwitcher',
  'freshnessSentence',
  'hasFigure',
  'overviewTiles',
  'tileOf',
]);

const View = function View() {};
const Text = function Text() {};
const Pressable = function Pressable() {};

const React = { createElement: (type, props, ...children) => ({ type, props: props ?? null, children }) };

/** An `AccountStore` over fresh in-memory ports: enough for construction. */
function store() {
  return new AccountStore({
    secrets: new SecretStore(new MemorySecureStore()),
    settings: new SettingsStore(new MemorySettingsStore()),
    registry: new ResourceRegistry(),
  });
}

function ports(over = {}) {
  return { store: store(), React, View, Text, Pressable, ...over };
}

/** Every string the tree would show. */
function render(node, found = []) {
  if (node === null || typeof node !== 'object') return found;
  if (node.type === Text) found.push(...node.children.filter((child) => typeof child === 'string'));
  for (const child of node.children ?? []) render(child, found);
  return found;
}

test('the door exports exactly the surface this file names', () => {
  assert.deepEqual(Object.keys(overview).sort(), [...SURFACE].sort());
});

test('createOverview binds the store to the switcher and the primitives to the screen', () => {
  const { switcher, Screen } = overview.createOverview(ports());
  assert.equal(typeof switcher.projects, 'function');
  assert.equal(typeof switcher.switchTo, 'function');
  assert.equal(typeof switcher.whereAmI, 'function');
  assert.equal(typeof Screen, 'function');
  // The screen draws the module's own shapes: the empty list first, since that
  // is what a fresh installation has.
  const empty = overview.overviewTiles({ installations: [] });
  const shown = render(Screen({ overview: empty, current: null }));
  assert.equal(shown.includes(overview.OVERVIEW_TITLE), true);
  assert.equal(shown.includes(overview.EMPTY_SENTENCE), true);
  assert.equal(shown.some((one) => one.includes('total')), false);
});

test('the door’s tree carries no credential and no total across installations', () => {
  const { Screen } = overview.createOverview(ports());
  const installations = [
    {
      account: { id: 'laptop', firebaseProjectId: 'fp-1', authUid: 'uid-1', fugaroProject: 'aurora' },
      snapshot: {
        liveness: 'live',
        document: null,
        day: '2026-11-11',
        dayChanged: false,
        broken: false,
        drawable: { total: { spent: { state: 'measured', value: 10 } } },
        degraded: false,
        silenceMs: 1_000,
        ageMs: 1_000,
        note: null,
        ingests: 1,
      },
    },
    {
      account: { id: 'phone', firebaseProjectId: 'fp-2', authUid: 'uid-2', fugaroProject: 'aurora' },
      snapshot: {
        liveness: 'live',
        document: null,
        day: '2026-11-11',
        dayChanged: false,
        broken: false,
        drawable: { total: { spent: { state: 'measured', value: 32.5 } } },
        degraded: false,
        silenceMs: 1_000,
        ageMs: 1_000,
        note: null,
        ingests: 1,
      },
    },
  ];
  const shown = render(Screen({ overview: overview.overviewTiles({ installations }), current: 'laptop' }));
  // 42.5 is the two spends added up (D170), and a credential is a value a
  // screenshot must never hold.
  assert.equal(shown.some((one) => one.includes('42.5')), false);
  assert.equal(shown.some((one) => one.includes('test-device-token')), false);
  assert.equal(shown.some((one) => one.includes('macKey')), false);
});

test('a missing port is refused at construction, not at the first utterance', () => {
  assert.throws(() => overview.createOverview(ports({ store: null })), TypeError);
  assert.throws(() => overview.createOverview({ React, View, Text, Pressable }), TypeError);
  for (const name of ['React', 'View', 'Text', 'Pressable']) {
    assert.throws(() => overview.createOverview(ports({ [name]: undefined })), TypeError, `${name} is required`);
  }
});
