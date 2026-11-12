// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The overview screen's tree (mobile plan, Task P6.8; D170).
//
// The plan's sentence is "the overview renders one tile per installation with
// its freshness and **no summed total**", so this file asserts three things:
//
//   * one section per installation, in the order the tiles came in;
//   * each section carries its own freshness and its own figures, printed by
//     `availability.js`'s `shown()` -- a day break is `—`, a value nothing
//     measured is `n/a`, and neither becomes a number on the way to the screen;
//   * **no string in the tree is a total across installations.** The walk below
//     collects every string the tree would show and asserts that the sum of the
//     two fixtures' spends is not among them, which is the check a header row
//     added in a later task would fail.
//
// Every liveness state `feed/view.js` can answer gets a sentence: the test
// walks `view.js`'s own `LIVENESS` list, so a state added to the feed without a
// sentence here fails rather than rendering a blank row.
//
// React and react-native are not installed (the Expo toolchain is unavailable
// here), so the four primitives are fakes and the assertions are against the
// tree this repository builds. Every value is synthetic.

import assert from 'node:assert/strict';
import test from 'node:test';

import { LIVENESS, STATE } from '../feed/index.js';
import {
  EMPTY_SENTENCE,
  FIGURES,
  NO_DAY,
  OVERVIEW_TITLE,
  REFUSAL_SENTENCE,
  SCREEN,
  SWITCH_WORD,
  UNKNOWN_LIVENESS_SENTENCE,
  UNKNOWN_PROJECT,
  ageText,
  createOverviewScreen,
  freshnessSentence,
} from './screen.js';
import { overviewTiles } from './tiles.js';

function fakeReact() {
  return {
    createElement(type, props, ...children) {
      return { type, props: props ?? null, children };
    },
  };
}

const View = function View() {};
const Text = function Text() {};
const Pressable = function Pressable() {};

function walk(node, found = []) {
  if (node === null || typeof node !== 'object') return found;
  found.push(node);
  for (const child of node.children ?? []) walk(child, found);
  return found;
}

/** Every string the tree would show, in order. */
function render(screen, props = {}) {
  return walk(screen(props))
    .filter((one) => one.type === Text)
    .flatMap((one) => one.children.filter((child) => typeof child === 'string'));
}

/** The elements one named `type` carries. */
function elementsOf(screen, name, props = {}) {
  return walk(screen(props)).filter((one) => one.props !== null && one.props.type === name);
}

function stringsOf(screen, name, props = {}) {
  return elementsOf(screen, name, props).flatMap((one) => one.children.filter((child) => typeof child === 'string'));
}

const screen = createOverviewScreen({ React: fakeReact(), View, Text, Pressable });

function fig(state, value = null) {
  return { state, value };
}

function snapshot(over = {}) {
  const total = {
    spent: fig(STATE.MEASURED, 10),
    dailyCap: fig(STATE.MEASURED, 50),
    percent: fig(STATE.MEASURED, 20),
    runs: fig(STATE.MEASURED, 3),
  };
  return {
    liveness: 'live',
    document: { day: '2026-11-11', total },
    day: '2026-11-11',
    dayChanged: false,
    broken: false,
    drawable: { total, repos: [], figures: [] },
    degraded: false,
    silenceMs: 2_000,
    ageMs: 2_000,
    note: null,
    ingests: 2,
    ...over,
  };
}

function account(id, project) {
  return { id, firebaseProjectId: `fp-${id}`, authUid: `uid-${id}`, fugaroProject: project };
}

/** Two installations, one current, as the store would list them. */
function twoTiles(over = {}) {
  return overviewTiles({
    installations: [
      {
        account: account('laptop', 'aurora'),
        snapshot: snapshot({ drawable: { total: { spent: fig(STATE.MEASURED, 10), dailyCap: fig(STATE.MEASURED, 50), percent: fig(STATE.MEASURED, 20), runs: fig(STATE.MEASURED, 3) } } }),
      },
      {
        account: account('phone', 'aurora'),
        snapshot: snapshot({ liveness: 'stale', ageMs: 125_000, drawable: { total: { spent: fig(STATE.MEASURED, 32.5) } }, ...over }),
      },
    ],
  });
}

test('the screen draws one section per installation, with its own figures', () => {
  const props = { overview: twoTiles(), current: 'laptop' };
  assert.deepEqual(stringsOf(screen, SCREEN.TITLE, props), [OVERVIEW_TITLE]);
  const names = stringsOf(screen, SCREEN.NAME, props);
  assert.deepEqual(names, ['laptop (current)', 'phone']);
  assert.deepEqual(stringsOf(screen, SCREEN.PROJECT, props), ['aurora', 'aurora']);
  assert.deepEqual(stringsOf(screen, SCREEN.FRESHNESS, props), ['live, read 2s ago', 'quiet, read 2m ago']);
  assert.deepEqual(stringsOf(screen, SCREEN.DAY, props), ['2026-11-11', '2026-11-11']);
  // Each figure row names the installation's own figure, in the order FIGURES
  // lists them, and a total that omitted one says `n/a` rather than a zero.
  const rows = stringsOf(screen, SCREEN.FIGURE, props);
  assert.deepEqual(rows, [
    'spend 10',
    'daily cap 50',
    'of the cap 20%',
    'runs today 3',
    'spend 32.5',
    'daily cap n/a',
    'of the cap n/a',
    'runs today n/a',
  ]);
  assert.equal(elementsOf(screen, SCREEN.TILE, props).length, 2);
  // The current installation offers no switch; the other one does, and it
  // carries the id it switches to.
  const switches = elementsOf(screen, SCREEN.SWITCH, props);
  assert.equal(switches.length, 1);
  assert.equal(switches[0].props.accountId, 'phone');
});

test('the screen draws no summed total anywhere (D170)', () => {
  const props = { overview: twoTiles(), current: 'laptop' };
  const shown = render(screen, props);
  // Both installations' spends are on screen, so the absence of their sum
  // below is about the sum and not about a tree that draws nothing.
  assert.equal(shown.some((one) => one.includes('spend 10')), true);
  assert.equal(shown.some((one) => one.includes('spend 32.5')), true);
  // 42.5 is 10 + 32.5, the two installations' spends. It is the number a header
  // row would print, and it is nowhere in the tree -- not as a string, and not
  // as any part of one.
  assert.equal(shown.includes('42.5'), false);
  assert.equal(shown.some((one) => one.includes('42.5')), false);
  // The same for the caps, the percentages and the run counts.
  assert.equal(shown.some((one) => one.includes('100')), false, '50 + 50 caps');
  assert.equal(shown.some((one) => one.includes('40')), false, '20 + 20 percent');
  assert.equal(shown.some((one) => one.includes('6') && one.includes('runs')), false, '3 + 3 runs');
  // And there is no element a total could be drawn in: every text element is
  // one of the named ones, each of which belongs to a single tile or to the
  // screen's own two states.
  const named = new Set(Object.values(SCREEN));
  const texts = walk(screen(props)).filter((one) => one.type === Text);
  for (const one of texts) {
    assert.equal(one.props === null || named.has(one.props.type), true, `unnamed text element: ${JSON.stringify(one.props)}`);
  }
});

test('a day break is a mark and an unmeasured day is n/a, never a zero', () => {
  const broken = overviewTiles({
    installations: [{ account: account('laptop', 'aurora'), snapshot: snapshot({ broken: true, dayChanged: true, drawable: null }) }],
  });
  const rows = stringsOf(screen, SCREEN.FIGURE, { overview: broken, current: null });
  assert.deepEqual(rows, ['spend —', 'daily cap —', 'of the cap —', 'runs today —']);
  // The mark survives the percentage suffix: `—%` is not a thing.
  assert.equal(rows.includes('of the cap —%'), false);

  // **A feed that has read nothing is not a broken series.** `view.js` sets
  // `broken` for *any* snapshot without a measurement, `no-document` included
  // (it is `!degraded && !measured`), so a screen that printed the mark on
  // `broken` alone would draw `—` for a phone that was paired a second ago --
  // a broken series where there is no series at all. `liveness` is what tells
  // the two apart, and this is the case that pins it.
  const nothing = overviewTiles({
    installations: [
      {
        account: account('laptop', 'aurora'),
        snapshot: snapshot({ liveness: 'no-document', document: null, day: null, broken: true, drawable: null, ageMs: null }),
      },
    ],
  });
  assert.deepEqual(stringsOf(screen, SCREEN.FIGURE, { overview: nothing, current: null }), [
    'spend n/a',
    'daily cap n/a',
    'of the cap n/a',
    'runs today n/a',
  ]);
  assert.deepEqual(stringsOf(screen, SCREEN.DAY, { overview: nothing, current: null }), [NO_DAY]);
});

test('every liveness state the feed can answer has a sentence', () => {
  for (const state of LIVENESS) {
    const sentence = freshnessSentence({ liveness: state, ageMs: null });
    assert.equal(typeof sentence, 'string');
    assert.notEqual(sentence, '', `${state} says nothing`);
    assert.notEqual(sentence, UNKNOWN_LIVENESS_SENTENCE, `${state} has no sentence of its own`);
  }
  // A state this build does not know is not a blank row.
  assert.equal(freshnessSentence({ liveness: 'something-new', ageMs: null }), UNKNOWN_LIVENESS_SENTENCE);
  assert.equal(freshnessSentence(null), UNKNOWN_LIVENESS_SENTENCE);
  // A tile whose project name is missing says so rather than drawing `null`.
  const noProject = overviewTiles({
    installations: [{ account: { id: 'laptop', firebaseProjectId: 'fp-1', authUid: 'uid-1' }, snapshot: snapshot() }],
  });
  assert.deepEqual(stringsOf(screen, SCREEN.PROJECT, { overview: noProject, current: null }), [UNKNOWN_PROJECT]);
});

test('the age is the feed’s, printed in the unit a phone reads', () => {
  assert.equal(ageText(2_000), '2s ago');
  assert.equal(ageText(125_000), '2m ago');
  assert.equal(ageText(7_200_000), '2h ago');
  // Not a duration this phone can state: nothing read yet, and a document
  // stamped after this phone's own clock.
  assert.equal(ageText(null), '');
  assert.equal(ageText(undefined), '');
  assert.equal(ageText(-60_000), '');
  assert.equal(ageText(NaN), '');
  assert.equal(ageText(Infinity), '');
  assert.equal(freshnessSentence({ liveness: 'live', ageMs: -60_000 }), 'live');
});

test('the screen’s own states: an empty list, a refusal, and a press', () => {
  // No installation yet: the sentence says where one comes from.
  assert.deepEqual(stringsOf(screen, SCREEN.EMPTY, { overview: overviewTiles({ installations: [] }), current: null }), [EMPTY_SENTENCE]);
  assert.deepEqual(stringsOf(screen, SCREEN.TILE, { overview: overviewTiles({ installations: [] }), current: null }), []);

  // A list that could not be read: one sentence, and the code on the element.
  const refused = elementsOf(screen, SCREEN.REFUSAL, { overview: { ok: false, code: 'bad-snapshot', field: 'liveness' }, current: null });
  assert.equal(refused.length, 1);
  assert.deepEqual(refused[0].children, [REFUSAL_SENTENCE]);
  assert.equal(refused[0].props.code, 'bad-snapshot');
  // A screen with no list at all is the entry's first render: the same
  // sentence, and no crash.
  assert.deepEqual(stringsOf(screen, SCREEN.REFUSAL, {}), [REFUSAL_SENTENCE]);

  // The press is the caller's, and a screen with no callback still builds.
  const pressed = [];
  const props = { overview: twoTiles(), current: 'laptop', onSwitch: (id) => pressed.push(id) };
  elementsOf(screen, SCREEN.SWITCH, props)[0].props.onPress();
  assert.deepEqual(pressed, ['phone']);
  assert.equal(elementsOf(screen, SCREEN.SWITCH, { overview: twoTiles(), current: 'laptop' })[0].props.onPress, undefined);
  // The word on the control, and the figure list, are the screen's own.
  assert.deepEqual(stringsOf(screen, SCREEN.SWITCH_LABEL, props), [SWITCH_WORD]);
  assert.deepEqual(FIGURES.map((one) => one.field), ['spend', 'dailyCap', 'percent', 'runs']);
});

test('the screen refuses to be built without React or a primitive', () => {
  assert.throws(() => createOverviewScreen({ View, Text, Pressable }), TypeError);
  assert.throws(() => createOverviewScreen({ React: fakeReact(), Text, Pressable }), TypeError);
  assert.throws(() => createOverviewScreen({ React: fakeReact(), View, Text }), TypeError);
  assert.throws(() => createOverviewScreen({ React: fakeReact(), View, Pressable }), TypeError);
});
