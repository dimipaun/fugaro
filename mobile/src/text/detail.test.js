// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// Detail goes to the screen (mobile identity and pairing plan, Task X2; the
// spec's "Detail goes to the screen").
//
// The spec's sentence is *"In voice mode, when output is too dense to follow by
// ear (lists of PR ids, job tables), the agent says it has put the details on
// screen and shows them in the text view."* Both halves are asserted here, and
// against the views rather than the prose:
//
//   * **which replies are dense** -- a list of ids, or a table, and nothing
//     else -- is `detailKind`, a pure function, so the rule is one a test
//     states rather than one a reader reconstructs. The narrowness is the
//     point and is tested as such: a reply wrongly called dense is *hidden*
//     from an owner who is listening, so one prose line makes a list a
//     sentence again, and a table needs its rule row;
//   * **what the voice says** and **what the text view shows** are
//     `detailOnScreen`, the mailbox client's own speaker port (a drop-in for
//     X1's `transcriptSpeaker`): the voice says one sentence, and the detail is
//     a line of the one conversation, drawn by the text view the way
//     `one-transcript.test.js` draws it.
//
// React and react-native are not installed, so the text view is walked as a
// tree over a fake React. The speaker is a local double and the transcript is
// X1's own store; the fixtures are run ids of the shape `fugaro runs ls`
// prints, and no value from any installation.

import assert from 'node:assert/strict';
import test from 'node:test';

import {
  DETAIL_KINDS,
  DETAIL_SPEECH,
  SCREEN,
  TRANSCRIPT_SOURCE,
  TRANSCRIPT_WHO,
  createTextScreen,
  createTranscript,
  detailKind,
  detailOnScreen,
} from './index.js';

const T0 = 1_700_000_000_000;

/** Two run ids of the form a Fugaro branch names, and a PR id. */
const RUN_A = '20261124-204050-3780';
const RUN_B = '20261124-190000-abcd';
const PR = '#1234';

/** A markdown table, as an agent writes one on the text interface. */
const TABLE = [
  '| run | outcome |',
  '| --- | --- |',
  `| ${RUN_A} | ready |`,
  `| ${RUN_B} | draft |`,
].join('\n');

// ---------------------------------------------------------------------------
// The text view, as a tree a phone would render.
// ---------------------------------------------------------------------------

const fakeReact = {
  createElement(type, props, ...children) {
    return { type, props: props ?? null, children };
  },
};

const View = function View() {};
const Text = function Text() {};
const TextInput = function TextInput() {};

const TextScreen = createTextScreen({ React: fakeReact, View, Text, TextInput });

/** Every element in the tree, depth first. */
function walk(node, found = []) {
  if (node === null || typeof node !== 'object') return found;
  found.push(node);
  for (const child of node.children ?? []) walk(child, found);
  return found;
}

/** The conversation as the text view draws it: one `[who, words]` pair a line. */
function drawn(entries) {
  return walk(TextScreen({ conversation: entries }))
    .filter((one) => one.props !== null && one.props.type === SCREEN.LINE)
    .map((line) =>
      line.children.flatMap((child) => child.children.filter((child) => typeof child === 'string')),
    );
}

// ---------------------------------------------------------------------------
// The app's path, with the device replaced.
// ---------------------------------------------------------------------------

/** A transcript and a speaker double that records what was said. */
function harness({ ok = true } = {}) {
  let ids = 0;
  const spoken = [];
  const transcript = createTranscript({ now: () => T0, newId: () => `l-${++ids}` });
  const speaker = {
    async speak(text, speech) {
      spoken.push({ text, refusal: speech?.refusal === true });
      return { ok };
    },
  };
  return { transcript, spoken, port: detailOnScreen({ transcript, speaker }) };
}

// ---------------------------------------------------------------------------
// 1. Which replies are dense, and which are read out.
// ---------------------------------------------------------------------------

test('a list of ids is dense: run ids, PR ids, bulleted or bare', () => {
  assert.equal(detailKind(`${RUN_A}\n${RUN_B}`), DETAIL_KINDS.IDS);
  assert.equal(detailKind(`- ${RUN_A}\n- ${RUN_B}`), DETAIL_KINDS.IDS);
  assert.equal(detailKind(`* ${RUN_A}\n* ${RUN_B}`), DETAIL_KINDS.IDS);
  assert.equal(detailKind(`1. ${RUN_A}\n2. ${RUN_B}`), DETAIL_KINDS.IDS);
  assert.equal(detailKind(`${PR}\n#1235`), DETAIL_KINDS.IDS);
  // A run id and a PR id in one list are still one list of ids.
  assert.equal(detailKind(`${RUN_A}\n${PR}`), DETAIL_KINDS.IDS);
});

test('a table is dense only when its rule row is really there', () => {
  assert.equal(detailKind(TABLE), DETAIL_KINDS.TABLE);
  // A table without the `---` rule is a paragraph with pipes in it.
  assert.equal(detailKind('| run | outcome |\n| a | ready |\n| b | draft |'), null);
  // A horizontal rule between two paragraphs is not a table: no cells.
  assert.equal(detailKind(`above\n---\nbelow`), null);
  // A lone row is not a table.
  assert.equal(detailKind('| run | outcome |'), null);
});

test('everything else is read out loud: one id is a fact, not a list', () => {
  assert.equal(detailKind(RUN_A), null, 'one run id');
  assert.equal(detailKind(`${RUN_A} finished.`), null, 'an id in a sentence');
  assert.equal(detailKind(`${RUN_A}\nand then it finished`), null, 'a list with prose in it');
  assert.equal(detailKind('The run finished.'), null, 'a sentence');
  assert.equal(detailKind('a\nb\nc'), null, 'lines that are not ids');
  assert.equal(detailKind(''), null, 'an empty reply');
});

test('a value that is not text is not dense', () => {
  for (const value of [null, undefined, 42, {}, [RUN_A, RUN_B]]) {
    assert.equal(detailKind(value), null, `${String(value)} is not a reply to hide`);
  }
});

// ---------------------------------------------------------------------------
// 2. The voice says one sentence, and the text view shows the detail.
// ---------------------------------------------------------------------------

test('a dense reply is announced, and the detail is a line the text view draws', async () => {
  const h = harness();

  const said = await h.port.speak(TABLE);

  assert.deepEqual(said, { ok: true }, 'the client\'s own verdict is returned unchanged');
  assert.deepEqual(h.spoken, [{ text: DETAIL_SPEECH, refusal: false }],
    'the voice said it had put the detail on screen, not the table');
  // Two lines, in the order it happened: the agent announced, then showed.
  assert.deepEqual(
    h.transcript.entries().map((one) => [one.who, one.source, one.text]),
    [
      [TRANSCRIPT_WHO.AGENT, TRANSCRIPT_SOURCE.OUTBOX, DETAIL_SPEECH],
      [TRANSCRIPT_WHO.AGENT, TRANSCRIPT_SOURCE.OUTBOX, TABLE],
    ],
  );
  // ... and the text view draws the detail, because the conversation is the
  // list it draws: the table's rows are on the screen.
  const lines = drawn(h.transcript.entries());
  assert.deepEqual(lines[0], ['Agent', DETAIL_SPEECH]);
  assert.equal(lines[1][0], 'Agent');
  assert.equal(lines[1][1], TABLE, 'the whole table, not a truncated one');
});

test('an ordinary reply is read out and recorded, exactly as X1\'s port does', async () => {
  const h = harness();

  await h.port.speak('Run 42 finished.');

  assert.deepEqual(h.spoken, [{ text: 'Run 42 finished.', refusal: false }]);
  assert.deepEqual(h.transcript.entries().map((one) => [one.who, one.source, one.text]),
    [[TRANSCRIPT_WHO.AGENT, TRANSCRIPT_SOURCE.OUTBOX, 'Run 42 finished.']],
    'one line: the sentence, and no second one for a detail there was none of');
  assert.deepEqual(drawn(h.transcript.entries()), [['Agent', 'Run 42 finished.']]);
});

// ---------------------------------------------------------------------------
// 3. Nothing that was not heard is shown, and the app's own words are not dense.
// ---------------------------------------------------------------------------

test('a dense reply the speaker could not speak shows nothing', async () => {
  const h = harness({ ok: false });

  const said = await h.port.speak(TABLE);

  assert.deepEqual(said, { ok: false }, 'the client\'s failure is returned unchanged');
  assert.deepEqual(h.transcript.entries(), [],
    'the message stays unacked and the next poll says it again -- so no table twice');
});

test('the app\'s own refusal sentence is not a dense reply and is not recorded', async () => {
  const h = harness();

  // S6.3: the client says its refusal over this same port, marked as what it
  // is. It is the app telling the owner why nothing was sent, never the agent.
  await h.port.speak('No connection. I have not sent that.', { refusal: true });

  assert.deepEqual(h.spoken, [{ text: 'No connection. I have not sent that.', refusal: true }]);
  assert.deepEqual(h.transcript.entries(), [], 'and it is not a line of the conversation');
});

// ---------------------------------------------------------------------------
// 4. The wiring is checked where the app is wired.
// ---------------------------------------------------------------------------

test('the port refuses a transcript or a speaker that is not one', () => {
  const transcript = createTranscript({ now: () => T0, newId: () => 'l-1' });
  const speaker = { speak: async () => ({ ok: true }) };

  assert.throws(() => detailOnScreen({ speaker }), TypeError, 'no transcript');
  assert.throws(() => detailOnScreen({ transcript }), TypeError, 'no speaker');
  assert.throws(() => detailOnScreen({ transcript, speaker: {} }), TypeError, 'a speaker without speak()');
});
