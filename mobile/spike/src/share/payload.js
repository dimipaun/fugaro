// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// What arrives through the share target, read into the fields U7 is answered
// from (task M0; Check 733's U7; the reconciliation's U7 and the plan's X3).
//
// **What U7 asks.** The iOS share extension and the Android intent filter, the
// package or bundle a share arrives under, and what a text selection carries
// into the app -- including whether a long paste arrives in one piece.
//
// **The route this build uses, and what is stock about it.** Expo's managed
// configuration cannot deliver a shared text on either platform: no Expo API
// reads Android's `Intent.EXTRA_TEXT`, and nothing in Expo adds an iOS share
// extension. The Android intent filter alone is stock (`android.intentFilters`
// in the app config, which is how the app appears in the share sheet) -- but
// appearing in the sheet and *reading* what was shared are two different things,
// and only the second is what the app needs. The extension and the reader come
// from `expo-share-intent`'s config plugin, which adds both halves at prebuild
// with no manual ejecting; the README says what the plugin needs from a console
// (an App Group, a bundle identifier for the extension, and the provider
// accounts' entitlements) and the U7 screen says, on the phone, which route
// this build actually took.
//
// **Everything here is a reading of the payload the plugin hands over**, which
// is why it is a module: the shape the plugin uses is not the shape the draft
// asks about, and the translation -- text, its length, the sending package, and
// whether a long paste is whole -- is what the owner's answer is written from.
// The screen shows the raw plugin state beside this reading, so a payload this
// parser does not understand is visible rather than silently UNVERIFIED.

/**
 * The long paste the probe shares. 20,000 characters is the number the task
 * names, it is a fifth of the 100,000-character "smooth" bound O4 accepted, and
 * a text this size is what an OS truncating a share -- or a bridge losing a
 * chunk at a size boundary -- does not survive.
 */
export const LONG_PASTE_LENGTH = 20000;

/** The marks both ends of the probe carry, so a lost tail is visible and not only countable. */
export const START_MARK = 'SPIKE-733-START';
export const END_MARK = 'SPIKE-733-END';

/**
 * The probe text: a deterministic body with a marker at each end. The marker is
 * what makes "in one piece" checkable: a share that dropped its tail has the
 * length but not the end mark, and a share that re-encoded its newlines has
 * both and a different length.
 */
export function longPaste(length = LONG_PASTE_LENGTH) {
  const body = `-${START_MARK}-`;
  const tail = `-${END_MARK}`;
  // The leading space and the newline are part of the length too; getting that
  // arithmetic wrong would cut the end mark off the probe and make every share
  // of it read as truncated.
  const filler = 'x'.repeat(Math.max(0, length - body.length - tail.length - 2));
  return ` ${body}${filler}\n${tail}`.slice(0, length);
}

/** The fields of the plugin's payload this module reads, and nothing else. */
const SENDER_KEYS = Object.freeze({
  android: ['senderPackage', 'package', 'packageName', 'androidPackage'],
  ios: ['senderBundle', 'bundle', 'bundleIdentifier', 'senderBundleIdentifier'],
});

/** A value from `raw` or from `raw.meta`, as the plugin may put it either place. */
function from(raw, key) {
  const direct = raw?.[key];
  if (typeof direct === 'string' && direct !== '') return direct;
  const meta = raw?.meta?.[key];
  return typeof meta === 'string' && meta !== '' ? meta : null;
}

/** The sending application, when this route exposes it at all. */
function sender(raw, platform) {
  for (const key of SENDER_KEYS[platform] ?? []) {
    const value = from(raw, key);
    if (value !== null) return value;
  }
  return null;
}

/** The text a payload carries: `text` first, and a shared URL's `webUrl` as the other spelling. */
function textOf(raw) {
  if (typeof raw?.text === 'string' && raw.text !== '') return raw.text;
  if (typeof raw?.webUrl === 'string' && raw.webUrl !== '') return raw.webUrl;
  return '';
}

/**
 * One payload, as the U7 answers.
 *
 * `present` is whether anything arrived at all -- a share sheet opened on an
 * app that never received one reads `false` rather than an empty text, because
 * "nothing arrived" and "an empty selection arrived" are different findings.
 * `onePiece` is `null` unless the payload is the long paste this module made:
 * for a short selection the question is not asked, and answering it anyway
 * would put a "yes" beside a length nothing was compared with.
 */
export function parseShare(raw, { platform = 'ios', expected = LONG_PASTE_LENGTH } = {}) {
  if (raw === null || raw === undefined) {
    return { present: false, type: null, text: '', length: 0, sender: null, onePiece: null, expected };
  }
  const text = textOf(raw);
  const length = text.length;
  const isProbe = length === expected || text.includes(START_MARK);
  return {
    present: true,
    type: typeof raw.type === 'string' ? raw.type : null,
    text,
    length,
    sender: sender(raw, platform),
    onePiece: isProbe ? length === expected && text.endsWith(END_MARK) : null,
    expected,
    files: Array.isArray(raw.files) ? raw.files.length : 0,
  };
}

/** The text as one line, for the screen and the block: a 20,000-character paste must not fill a page. */
export function preview(text, { max = 160 } = {}) {
  const flat = String(text ?? '').replace(/\s+/g, ' ').trim();
  if (flat.length <= max) return flat;
  return `${flat.slice(0, max)}… (${String(text).length} characters)`;
}

/** The U7 answers in words, so the screen and the draft say the same thing. */
export function describe(read, { platform = 'ios' } = {}) {
  const where = platform === 'android' ? 'package' : 'bundle';
  const lines = [
    ['What arrived', read.present ? `${read.type ?? 'unknown type'}, ${read.length} characters` : 'nothing (no share has reached the app yet)'],
    ['Text', read.present ? preview(read.text) : 'UNVERIFIED'],
    ['Sending ' + where, read.sender ?? `not exposed by this route (the ${where} a share arrives under is the platform's to give)`],
    [
      `In one piece (${read.expected} characters)`,
      read.onePiece === null
        ? 'UNVERIFIED (the probe text was not the payload)'
        : read.onePiece
          ? 'yes, whole: the length matches and the end mark is there'
          : `no: ${read.length} of ${read.expected} characters, end mark ${read.text.endsWith(END_MARK) ? 'present' : 'missing'}`,
    ],
  ];
  return lines;
}
