// The text screen's tree (mobile plan, Task P6.9; D167, D170).
//
// The plan writes this task's first two verification claims as rendering ones:
// "pending prompts and recent summaries render per installation". Both halves of
// that sentence are asserted here:
//
//   * the tree carries each installation's questions and recent lines, and
//     carries each installation's OWN -- a prompt belonging to one is absent
//     from the other's section, and nothing is counted across them (D170's "no
//     summed total" is the negative half of "per installation", and it is the
//     half a careless refactor breaks);
//   * the summaries are newest first and capped, in the rule rather than in the
//     store, so "which five lines are on screen" is a fact this file states.
//
// React and react-native are not installed (the Expo toolchain is not available
// in this environment), so all four primitives are fakes and the assertion is
// against the tree this repository builds. The walk reads every string the tree
// holds, which is also how "the screen draws no secret" stays checkable: a tree
// is what ends up in a screenshot and a crash report.
//
// Nothing secret-shaped is below at all -- the fixtures are an installation id
// the owner would have typed (D114) and its questions.

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  DESTRUCTIVE_WORD,
  INPUT_HINT,
  NO_PROMPTS,
  NO_SUMMARIES,
  RECENT_LABEL,
  RECENT_SUMMARY_LIMIT,
  SCREEN,
  TEXT_TITLE,
  TYPE_CUE,
  createTextScreen,
  recentSummaries,
} from './index.js';

/** The smallest React that can build a tree we can read. */
function fakeReact() {
  return {
    createElement(type, props, ...children) {
      return { type, props: props ?? null, children };
    },
  };
}

const View = function View() {};
const Text = function Text() {};
const TextInput = function TextInput() {};

/** Every element in the tree, depth first, as `{ type, props, children }`. */
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

/** The strings one named element (a `SCREEN` name, in `props.type`) carries. */
function stringsOf(screen, name, props = {}) {
  return walk(screen(props))
    .filter((one) => one.props !== null && one.props.type === name)
    .flatMap((one) => one.children.filter((child) => typeof child === 'string'));
}

/** The strings one element and everything under it would show. */
function renderOf(node) {
  return walk(node)
    .filter((one) => one.type === Text)
    .flatMap((one) => one.children.filter((child) => typeof child === 'string'));
}

function makeScreen() {
  return createTextScreen({ React: fakeReact(), View, Text, TextInput });
}

/** A P6.5 listing entry, shaped as `shared/prompts`' `pending` returns it. */
function waiting(id, text, options = {}) {
  return Object.freeze({
    prompt: Object.freeze({ id, text, at: options.at ?? 1_700_000_000_000 }),
    destructive: options.destructive === true,
    safe: options.safe ?? null,
    announcedAt: null,
    stage: 'first',
    due: true,
  });
}

const LAPTOP = Object.freeze({
  account: 'laptop',
  pending: [waiting('p-1', 'Deploy to staging?'), waiting('p-2', 'Merge PR 7?', { destructive: true })],
  summaries: [
    { id: 's-1', at: 1_700_000_000_000, text: 'the build is green' },
    { id: 's-2', at: 1_700_000_100_000, text: 'the merge landed' },
  ],
});

const DESKTOP = Object.freeze({
  account: 'desktop',
  pending: [waiting('p-3', 'Restart the runner?')],
  summaries: [{ id: 's-3', at: 1_700_000_200_000, text: 'the tests passed' }],
});

// ---------------------------------------------------------------------------
// Wiring.
// ---------------------------------------------------------------------------

test('the screen refuses to be built without its primitives', () => {
  const base = { React: fakeReact(), View, Text, TextInput };
  for (const field of ['React', 'View', 'Text', 'TextInput']) {
    const broken = { ...base };
    delete broken[field];
    assert.throws(() => createTextScreen(broken), TypeError, field);
  }
  assert.throws(() => createTextScreen({ ...base, React: {} }), TypeError);
  assert.throws(() => createTextScreen(null), TypeError);
});

test('an empty screen is still a screen: the title and the keyboard', () => {
  const screen = makeScreen();
  const shown = render(screen);
  assert.equal(shown.includes(TEXT_TITLE), true);
  const inputs = walk(screen({})).filter((one) => one.type === TextInput);
  assert.equal(inputs.length, 1);
  assert.equal(inputs[0].props.placeholder, INPUT_HINT);
  assert.equal(inputs[0].props.value, '', 'the draft starts empty and lives in the caller');
});

// ---------------------------------------------------------------------------
// Pending prompts and recent summaries, per installation.
// ---------------------------------------------------------------------------

test('each installation shows its own questions and its own recent lines', () => {
  const screen = makeScreen();
  const shown = render(screen, { installations: [LAPTOP, DESKTOP] });
  for (const line of [
    'laptop',
    'Deploy to staging?',
    'Merge PR 7?',
    'the build is green',
    'the merge landed',
    'desktop',
    'Restart the runner?',
    'the tests passed',
  ]) {
    assert.equal(shown.includes(line), true, line);
  }
  // And one installation's section is one element: the tree is grouped, so a
  // question cannot be drawn under the wrong name. Each section's own subtree
  // holds its installation's lines and none of the other's.
  const sections = walk(screen({ installations: [LAPTOP, DESKTOP] }))
    .filter((one) => one.props !== null && one.props.type === SCREEN.INSTALLATION);
  assert.equal(sections.length, 2);
  const laptop = renderOf(sections[0]);
  const desktop = renderOf(sections[1]);
  assert.deepEqual(laptop.filter((one) => one === DESKTOP.account), []);
  assert.deepEqual(desktop.filter((one) => one === LAPTOP.account), []);
  for (const line of ['Deploy to staging?', 'Merge PR 7?', 'the build is green']) {
    assert.equal(laptop.includes(line), true, line);
    assert.equal(desktop.includes(line), false, line);
  }
  assert.equal(desktop.includes('Restart the runner?'), true);
  assert.equal(laptop.includes('Restart the runner?'), false);
});

test('nothing is summed across installations, and no total is drawn', () => {
  const screen = makeScreen();
  const shown = render(screen, { installations: [LAPTOP, DESKTOP] });
  // Three questions across two installations; D170 forbids the sum, so the
  // string "3" must not be on screen at all.
  assert.equal(shown.includes('3'), false, 'a summed total must not be drawn');
  assert.equal(shown.includes('2'), false, 'nor a per-screen count');
});

test('a destructive question is drawn as one, beside the question', () => {
  const screen = makeScreen();
  const marks = stringsOf(screen, SCREEN.DESTRUCTIVE, { installations: [LAPTOP] });
  assert.deepEqual(marks, [DESTRUCTIVE_WORD]);
  // The safe question in the other installation carries no mark.
  assert.deepEqual(stringsOf(screen, SCREEN.DESTRUCTIVE, { installations: [DESKTOP] }), []);
});

test('an installation with nothing waiting, and one with no history, each say so', () => {
  const screen = makeScreen();
  const quiet = { account: 'tablet', pending: [], summaries: [] };
  const shown = render(screen, { installations: [quiet] });
  assert.equal(shown.includes(NO_PROMPTS), true);
  assert.equal(shown.includes(NO_SUMMARIES), true);
  assert.equal(shown.includes(RECENT_LABEL), true);
});

test('the unanswered count is asked of the caller, not inferred: a section draws what it is given', () => {
  // A section's questions are drawn in the order P6.5 gave them, unchanged --
  // the screen is not a second sort of the queue's own listing order.
  const screen = makeScreen();
  const lines = stringsOf(screen, SCREEN.PROMPT, { installations: [LAPTOP] });
  assert.deepEqual(lines, ['Deploy to staging?', 'Merge PR 7?']);
});

// ---------------------------------------------------------------------------
// recentSummaries: the rule, not the store.
// ---------------------------------------------------------------------------

test('the summaries are the newest ones first, and no more than the cap', () => {
  const many = Array.from({ length: 9 }, (unused, index) => ({
    id: `s-${index}`,
    at: 1_700_000_000_000 + index * 1_000,
    text: `line ${index}`,
  }));
  const shown = recentSummaries(many);
  assert.equal(shown.length, RECENT_SUMMARY_LIMIT);
  assert.deepEqual(
    shown.map((one) => one.text),
    ['line 8', 'line 7', 'line 6', 'line 5', 'line 4'],
  );
  // The list it was given is not reordered in place: the caller's array is the
  // caller's.
  assert.equal(many[0].text, 'line 0');
});

test('summaries that are not summaries are dropped rather than drawn as blanks', () => {
  const shown = recentSummaries([
    { id: 's-1', at: 1, text: 'good' },
    { id: 's-2', at: 2, text: '' },
    { id: 's-3', at: 'yesterday', text: 'no timestamp' },
    { id: 's-4', text: 'no at at all' },
    null,
    'a string',
  ]);
  assert.deepEqual(shown.map((one) => one.text), ['good']);
});

test('two summaries stamped the same have one order, not the store\'s', () => {
  const at = 1_700_000_000_000;
  const first = recentSummaries([
    { id: 'b', at, text: 'b' },
    { id: 'a', at, text: 'a' },
  ]);
  const reversed = recentSummaries([
    { id: 'a', at, text: 'a' },
    { id: 'b', at, text: 'b' },
  ]);
  assert.deepEqual(first.map((one) => one.text), reversed.map((one) => one.text));
});

test('a limit that is not a count is no summaries at all, not a crash', () => {
  const list = [{ id: 's-1', at: 1, text: 'good' }];
  for (const limit of [-1, 1.5, 'five', null]) {
    assert.deepEqual(recentSummaries(list, limit), [], String(limit));
  }
  assert.deepEqual(recentSummaries('not a list'), []);
  assert.equal(recentSummaries(list, 0).length, 0);
});

// ---------------------------------------------------------------------------
// The read-back, the unsent text, and the notice.
// ---------------------------------------------------------------------------

test('a read-back is drawn with the TYPED cue, never P6.2\'s spoken one', () => {
  const screen = makeScreen();
  const readBack = 'Cancel run run-42. Say "Fugaro, confirm" to confirm.';
  const shown = render(screen, { confirmation: { readBack, deadline: 1_700_000_060_000 } });
  assert.equal(shown.includes(readBack), true);
  assert.equal(shown.includes(TYPE_CUE), true);
  assert.equal(TYPE_CUE.includes('Fugaro, confirm'), true);
  // And with nothing waiting, there is no cue and no read-back at all.
  const quiet = render(screen);
  assert.equal(quiet.includes(TYPE_CUE), false);
  assert.equal(stringsOf(screen, SCREEN.CONFIRMATION).length, 0);
});

test('the UNSENT text is shown, and a message the database already has is not', () => {
  const screen = makeScreen();
  const shown = render(screen, {
    unsent: [
      { id: 'm-1', at: 1_700_000_000_000, text: 'ship the notes', landed: false },
      { id: 'm-2', at: 1_700_000_001_000, text: 'and the changelog', landed: true },
    ],
  });
  // D167(a) and P3.6's `pending()` are about the text the database has NOT
  // received: drawing a landed message the same way would invite the owner to
  // retype a sentence the consumer is already acting on.
  assert.deepEqual(stringsOf(screen, SCREEN.UNSENT, {
    unsent: [
      { id: 'm-1', at: 1_700_000_000_000, text: 'ship the notes', landed: false },
      { id: 'm-2', at: 1_700_000_001_000, text: 'and the changelog', landed: true },
    ],
  }), ['ship the notes']);
  assert.equal(shown.includes('and the changelog'), false);
});

test('a malformed unsent entry is dropped rather than drawn as a blank row', () => {
  const screen = makeScreen();
  const strings = stringsOf(screen, SCREEN.UNSENT, {
    unsent: [null, {}, { id: 'm-1', text: '' }, { id: 'm-2', text: 'real', landed: false }, 'a string'],
  });
  assert.deepEqual(strings, ['real']);
});

// ---------------------------------------------------------------------------
// A malformed entry is a row this screen refuses to draw, not a render crash.
// ---------------------------------------------------------------------------

test('a malformed prompt entry is dropped rather than crashing the screen', () => {
  const screen = makeScreen();
  // The fallback screen crashing is worse than any blank row: it is the screen
  // that exists for when the voice path is not working.
  for (const pending of [[null], [{}], [{ prompt: null }], [{ prompt: {} }], [{ prompt: { id: '' } }], 'x']) {
    const props = { installations: [{ account: 'a', pending, summaries: [] }] };
    assert.doesNotThrow(() => render(screen, props));
    assert.deepEqual(stringsOf(screen, SCREEN.PROMPT, props), []);
    // The section is still drawn, and says nothing is waiting.
    assert.equal(render(screen, props).includes('a'), true);
    assert.equal(render(screen, props).includes(NO_PROMPTS), true);
  }
  // And a malformed installation is skipped rather than crashing.
  assert.doesNotThrow(() => render(screen, { installations: [null, 'x', LAPTOP] }));
  assert.equal(render(screen, { installations: [null, 'x', LAPTOP] }).includes('Deploy to staging?'), true);
});

test('a prompt with a sentence is drawn by its sentence, and one without by its id', () => {
  const screen = makeScreen();
  const props = {
    installations: [
      {
        account: 'a',
        pending: [waiting('p-1', 'Deploy to staging?'), { prompt: { id: 'p-2' }, destructive: true }],
        summaries: [],
      },
    ],
  };
  assert.deepEqual(stringsOf(screen, SCREEN.PROMPT, props), ['Deploy to staging?', 'p-2']);
  assert.deepEqual(stringsOf(screen, SCREEN.DESTRUCTIVE, props), [DESTRUCTIVE_WORD]);
});

test('a notice is one line, and no notice is no line', () => {
  const screen = makeScreen();
  assert.deepEqual(stringsOf(screen, SCREEN.NOTICE, { notice: 'no-action-write' }), ['no-action-write']);
  assert.deepEqual(stringsOf(screen, SCREEN.NOTICE, {}), []);
  assert.deepEqual(stringsOf(screen, SCREEN.NOTICE, { notice: '' }), []);
});

test('the keyboard is the caller\'s: the draft and the handler are passed through', () => {
  const screen = makeScreen();
  const typed = [];
  const root = screen({ draft: 'half a sentence', onChangeText: (text) => typed.push(text) });
  const input = walk(root).find((one) => one.type === TextInput);
  assert.equal(input.props.value, 'half a sentence');
  input.props.onChangeText('more');
  assert.deepEqual(typed, ['more']);
});

test('the screen draws no secret, and no installation but the ones it was given', () => {
  const screen = makeScreen();
  const shown = render(screen, { installations: [LAPTOP, DESKTOP] });
  const joined = shown.join('\n');
  for (const token of ['firebaseProjectId', 'fugaro-dev', 'authUid', 'uid-', 'macKey', 'synthetic']) {
    assert.equal(joined.includes(token), false, token);
  }
});
