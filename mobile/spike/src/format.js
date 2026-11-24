// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The result blocks the "Copy result" buttons put on the clipboard, and the
// rules that keep them pasteable (task M0 of
// docs/mobile-identity-pairing/implementation-plan.md, Check 733).
//
// **What a block is for.** The owner reads a screen on each phone and writes
// the reading into `app-spike-DRAFT.md`, the draft `scripts/live-checks/check733.sh`
// writes. Typing a refusal message off a phone screen is where a reading gets
// mistyped, so each question's screen hands the owner a short plain-text block
// that names the question, the phone and one line per observation, and the
// draft's table rows are what those lines answer.
//
// **Why it is a module and not a template string in a screen.** Three of the
// four questions build a block, and every one of them carries text that did not
// come from this repository: Firebase's own error message, a provider's
// `sub`, a package name an intent arrived under. That text can hold a newline
// (Firebase's messages do), and a value that breaks the line it is on is a line
// the owner pastes in the wrong cell -- so the block is built through one
// function that flattens and bounds every value, and the tests here are what
// make "one line per observation" a fact rather than a hope.
//
// **Nothing here decides what is safe to show.** A caller that hands `value` a
// token has put a token in the block; what keeps tokens out is the caller --
// `src/firebase/idp.js`, whose parsed answer has no token field to hand over,
// and the screens, which print an outcome, a status and Firebase's own error
// text and never the material they sent. What this module does guarantee is
// that no value it is given can break a line or run past the end of one.

/** How long one value may be before it is cut, and the mark it is cut with. */
export const VALUE_MAX = 300;
const ELLIPSIS = '… (cut)';

/** What each question's block is headed with: the Check 733 row it answers. */
export const QUESTION_TITLES = Object.freeze({
  U4: 'U4 provider tokens and two identities at once',
  U5: 'U5 the device signing key',
  U7: 'U7 the share route',
  U10: 'U10 the synced keychain',
});

/**
 * One value, as one line of text: every run of whitespace (a newline from
 * Firebase's message included) becomes a single space, the ends are trimmed,
 * and anything longer than `VALUE_MAX` is cut with a visible mark. A
 * non-string is rendered through `String`, and `undefined`/`null`/`''` all read
 * as `UNVERIFIED` -- the draft's own word for "no reading", so a block pasted
 * into it never leaves a cell looking answered when the observation never
 * happened.
 */
export function plain(value, { max = VALUE_MAX } = {}) {
  if (value === undefined || value === null) return 'UNVERIFIED';
  const text = String(value).replace(/\s+/g, ' ').trim();
  if (text === '') return 'UNVERIFIED';
  if (text.length <= max) return text;
  return text.slice(0, Math.max(0, max - ELLIPSIS.length)) + ELLIPSIS;
}

/** `- Label: value`, the one shape every line of a block takes. */
export function line(label, value) {
  return `- ${plain(label)}: ${plain(value)}`;
}

/**
 * The whole block: a heading naming the check and the question, the phone it
 * was read on, then one line per `[label, value]` pair.
 *
 * `phone` is the owner's own label for the handset ("iPhone", "Pixel 9"), which
 * is what the draft's per-phone sections ask for; `Platform.OS` alone would
 * give two blocks from two Androids that could not be told apart.
 */
export function resultBlock({ question, phone, fields }) {
  const title = QUESTION_TITLES[question] ?? plain(question);
  const rows = (fields ?? []).map(([label, value]) => line(label, value));
  return [
    `Check 733 — ${title}`,
    `Phone: ${plain(phone)}`,
    ...rows,
  ].join('\n');
}

/** The sentence every block ends with, so a pasted line says what was left out. */
export const NO_TOKEN_NOTE =
  'No token, no key and no keychain value is shown or copied: only the outcome, a status and the service\'s own error text.';
