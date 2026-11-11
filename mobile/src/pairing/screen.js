// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The pairing screen's tree (mobile plan, Task P6.8; the pairing plan's §7.2).
//
// §7.2's steps a person takes part in are two: showing the code so the operator
// can type it into the CLI, and saying what happened when the pairing did not
// complete. This file draws exactly those, plus the two fields that start a
// pairing -- the owner's label for the installation (D114: the list is the
// owner's, so the name is too) and the payload's text.
//
// **Why the text field is here and the camera is not.** The scanned payload is
// a string: this screen takes it, and how it was obtained -- a camera, a paste,
// a share sheet -- is not the screen's business. A real QR reader needs a
// camera dependency and a native build, which this task does not add (and may
// not: `mobile/app.config.js`, `eas.json` and the native project are outside
// its file list). So the screen accepts the string and the camera is the one
// seam left open, reported rather than stubbed: a screen with a "scan" button
// that opened nothing would be a button that lies.
//
// **Why a function of React and react-native, like every other screen here.**
// Nothing may `import 'react'` or `'react-native'`: the Expo toolchain is not
// installed in this environment and no test may need a device (Global
// Constraint 10). The primitives are arguments and `screen.test.js` walks the
// tree this builds against a fake React.
//
// **What the screen never shows.** No token, no MAC key, no key material and no
// part of the envelope: the snapshot it draws carries none of them
// (`session.js`'s reason), and the walk in `screen.test.js` is what keeps that
// true as the tree grows -- it sees every string this file draws.

import { SESSION_CODES, SESSION_STATES } from './session.js';

/** The element type names the screen uses, in one place so a test can name
 * them too. */
export const SCREEN = Object.freeze({
  ROOT: 'fugaro.pairing',
  TITLE: 'fugaro.pairing.title',
  PROJECT: 'fugaro.pairing.project',
  CODE: 'fugaro.pairing.code',
  CODE_LABEL: 'fugaro.pairing.code.label',
  CODE_HINT: 'fugaro.pairing.code.hint',
  STATE: 'fugaro.pairing.state',
  LABEL_INPUT: 'fugaro.pairing.label',
  TEXT_INPUT: 'fugaro.pairing.payload',
  PAIR: 'fugaro.pairing.pair',
  CANCEL: 'fugaro.pairing.cancel',
});

/** The screen's title. A constant, never a value from an installation. */
export const PAIRING_TITLE = 'Pair this phone';

/** What the code is for, in one line, because a code with no instruction is a
 * string the operator has to guess the use of. */
export const CODE_HINT = 'Type this code into fugaro pair on your computer.';

/** The label above the code. */
export const CODE_LABEL = 'Your code';

/** What the label field asks for. */
export const LABEL_HINT = 'Name for this installation';

/** What the payload field asks for. */
export const PAYLOAD_HINT = 'The pairing code from fugaro pair';

/** The button that starts a pairing. */
export const PAIR_WORD = 'Pair';

/** The button that gives up on one. */
export const CANCEL_WORD = 'Cancel';

/** What the screen says while nothing has been scanned. */
export const IDLE_SENTENCE = 'Nothing is paired yet.';

/** What the screen says while it is waiting for the delivery. */
export const WATCHING_SENTENCE = 'Waiting for your computer to confirm the code.';

/** What the screen says when the installation is paired. */
export const DELIVERED_SENTENCE = 'Paired.';

/** What the screen says when the pairing node went away before a delivery
 * (§7.2: say the pairing expired, and show the code path again). */
export const EXPIRED_SENTENCE = 'The pairing expired. Start a new one on your computer.';

/** One sentence per refusal this screen can be handed, so a refusal the user
 * cannot read is not a refusal. The two that a person can act on name the
 * action; the rest say the payload was not one this app accepts, which is the
 * same sentence the CLI's own output would earn. */
const REFUSAL_SENTENCES = Object.freeze({
  [SESSION_CODES.EXPIRED]: EXPIRED_SENTENCE,
  [SESSION_CODES.BAD_ACCOUNT]: 'Give this installation a name first: letters, digits, "-" and "_".',
  [SESSION_CODES.REQUEST_REFUSED]: 'The phone could not reach the installation. Check the connection and try again.',
  [SESSION_CODES.SIGN_IN_REFUSED]: 'The pairing token was not accepted. Start a new pairing on your computer.',
  [SESSION_CODES.IDENTITY_MISMATCH]: 'The installation answered as something else. Start a new pairing on your computer.',
  [SESSION_CODES.WATCH_REFUSED]: 'The phone could not watch for the delivery. Check the connection and try again.',
  [SESSION_CODES.STORE_REFUSED]: 'The phone could not store the credential. Nothing was saved.',
  [SESSION_CODES.PORT_FAILED]: 'Something on the phone failed. Nothing was saved.',
  [SESSION_CODES.ALREADY_STARTED]: 'A pairing is already in progress.',
});

/** The sentence for one refusal code: a refusal of the payload's own reading is
 * always the same sentence, because a person looking at a QR they did not
 * generate cannot act on the difference between a bad uid and a bad URL. */
export function refusalSentence(code) {
  if (typeof code === 'string' && Object.prototype.hasOwnProperty.call(REFUSAL_SENTENCES, code)) {
    return REFUSAL_SENTENCES[code];
  }
  return 'That pairing code was not one this app can use.';
}

/**
 * What the screen says for a session snapshot: one sentence, chosen from the
 * state, and a refusal's own sentence when there is one.
 *
 * A pure function so a test can pin the sentences without walking a tree, and
 * so the screen has no branch that could say two things at once.
 */
export function stateSentence(snapshot) {
  if (snapshot === null || typeof snapshot !== 'object') return IDLE_SENTENCE;
  if (snapshot.state === SESSION_STATES.DELIVERED) return DELIVERED_SENTENCE;
  if (snapshot.state === SESSION_STATES.EXPIRED) return EXPIRED_SENTENCE;
  // A starting session says the waiting sentence too: the code is already on
  // screen (§7.2 shows it before the token is exchanged, so it is drawn for the
  // whole of the write) and what the person does next is the same -- type it
  // into their computer. What the two states do not share is the button: a
  // starting session still draws Pair, and a press there is answered by the
  // session's own "already in progress" (`session.js`), which is why the
  // sentence is the one thing it inherits.
  if (snapshot.state === SESSION_STATES.STARTING || snapshot.state === SESSION_STATES.WATCHING) {
    return WATCHING_SENTENCE;
  }
  if (snapshot.state === SESSION_STATES.REFUSED && snapshot.refusal !== null && snapshot.refusal !== undefined) {
    return refusalSentence(snapshot.refusal.code);
  }
  return IDLE_SENTENCE;
}

/**
 * The screen. Its props are values and callbacks and nothing else:
 *
 *   * `session` -- `session.js`'s snapshot;
 *   * `label` and `text` -- the two fields' current contents, held by the
 *     caller (this screen has no state of its own);
 *   * `onLabel`, `onText`, `onPair`, `onCancel` -- the callbacks.
 */
export function createPairingScreen({ React, View, Text, TextInput, Pressable }) {
  if (React === null || typeof React !== 'object' || typeof React.createElement !== 'function') {
    throw new TypeError('createPairingScreen needs React (with createElement)');
  }
  for (const [name, primitive] of [['View', View], ['Text', Text], ['TextInput', TextInput], ['Pressable', Pressable]]) {
    if (typeof primitive !== 'function' && typeof primitive !== 'object') {
      throw new TypeError(`createPairingScreen needs react-native's ${name}`);
    }
  }

  const el = React.createElement;

  return function PairingScreen(props = {}) {
    const session = props.session === null || typeof props.session !== 'object' ? null : props.session;
    const children = [el(Text, { key: 'title', type: SCREEN.TITLE }, PAIRING_TITLE)];

    // The code is the one thing the operator needs from this screen while a
    // pairing is running, so it is drawn above everything else and only when
    // there is one: an empty code box is a code the operator would wait for.
    if (session !== null && typeof session.code === 'string' && session.code !== '') {
      children.push(el(Text, { key: 'code-label', type: SCREEN.CODE_LABEL }, CODE_LABEL));
      children.push(el(Text, { key: 'code', type: SCREEN.CODE }, session.code));
      children.push(el(Text, { key: 'code-hint', type: SCREEN.CODE_HINT }, CODE_HINT));
    }

    // The project is what the operator recognises the installation by. It is
    // the scanned name and not the delivered one: the delivered record is what
    // the app stores, and showing it before the delivery would be showing a
    // value nothing has judged yet.
    if (session !== null && typeof session.project === 'string' && session.project !== '') {
      children.push(el(Text, { key: 'project', type: SCREEN.PROJECT }, session.project));
    }

    children.push(el(Text, { key: 'state', type: SCREEN.STATE }, stateSentence(session)));

    const watching = session !== null && session.state === SESSION_STATES.WATCHING;
    if (watching) {
      children.push(
        el(
          Pressable,
          { key: 'cancel', type: SCREEN.CANCEL, onPress: typeof props.onCancel === 'function' ? props.onCancel : undefined },
          el(Text, { key: 'cancel-word', type: SCREEN.CANCEL }, CANCEL_WORD),
        ),
      );
      return el(View, { type: SCREEN.ROOT }, ...children);
    }

    children.push(
      el(TextInput, {
        key: 'label',
        type: SCREEN.LABEL_INPUT,
        onChangeText: typeof props.onLabel === 'function' ? props.onLabel : undefined,
        placeholder: LABEL_HINT,
        value: typeof props.label === 'string' ? props.label : '',
      }),
    );
    children.push(
      el(TextInput, {
        key: 'payload',
        type: SCREEN.TEXT_INPUT,
        onChangeText: typeof props.onText === 'function' ? props.onText : undefined,
        placeholder: PAYLOAD_HINT,
        value: typeof props.text === 'string' ? props.text : '',
      }),
    );
    children.push(
      el(
        Pressable,
        { key: 'pair', type: SCREEN.PAIR, onPress: typeof props.onPair === 'function' ? props.onPair : undefined },
        el(Text, { key: 'pair-word', type: SCREEN.PAIR }, PAIR_WORD),
      ),
    );
    return el(View, { type: SCREEN.ROOT }, ...children);
  };
}
