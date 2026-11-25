// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The registration message, written in the schema's own terms
// (mobile plan, Task P5.2.8; P5.2.1's kind; D113, D169; the design is
// docs/mobile/design/push-notifications.md §3.5 (b) and §3.8).
//
// **Why this file asks the schema instead of restating it.** The values a
// registration carries -- a token, a platform, §3.8's switches and their window
// -- all have shapes, and every one of them is already decided in one place:
// `shared/mailbox-schema/index.js`, whose `registration` kind and `prefs`
// record P5.2.1 added, and which the principal that applies the message (P5.2.4)
// reads with the same call. A second copy of "a platform is `ios` or `android`"
// here would be a second answer, and the expensive kind: an app that accepted a
// platform or a switch the consumer refuses would write a message nothing ever
// applies, so the push node the sender reads would simply never appear and
// nothing would say why. So the app **asks**: `judgeField` hands the schema a
// registration built from a valid skeleton with ONE field replaced and reports
// what the schema says about that field.
//
// **The skeleton is a valid registration, and its validity is what the probe
// rests on.** It carries the text this app really writes (`client.js`'s
// `REGISTRATION_TEXT`, so the probe is the message itself minus the values) and
// a placeholder token and platform, which is what makes a refusal about `prefs`
// decidable: without them the schema would answer `missing-field @ token` and
// every question about a switch would come back as a question about a token. A
// refusal whose field is NOT the one asked about therefore means the skeleton
// itself is refused, which is a bug in this module -- `MESSAGE_CODES.PROBE`, and
// never a sentence about the caller's value.
//
// **The token is a secret-shaped thing and this module never prints one.** A
// token reaches a refusal's `field` and never its code or its message: the two
// vocabularies here are the schema's (`empty-string`, `too-long`,
// `bad-character`, `bad-zone`, `bad-window`, `unknown-platform`), and none of
// them carries a value.

import { validateMessage } from '../../../shared/mailbox-schema/index.js';
// The two constants the write path writes every registration with, taken from
// the mailbox's DOOR and not from `client.js` behind it (`mobile/README.md`'s
// rule): this module asks the schema about a message, it does not build one --
// `registration.js` hands the real values to the client, which builds and signs
// the record. They are named once, in `client.js`, because that is the file that
// writes it.
import { REGISTRATION_KIND, REGISTRATION_TEXT } from '../mailbox/index.js';

export { REGISTRATION_KIND, REGISTRATION_TEXT };

/** The three fields that travel with the kind, in the order they are judged. */
export const REGISTRATION_FIELDS = Object.freeze(['token', 'platform', 'prefs']);

/** The refusal codes this module adds to the schema's. */
export const MESSAGE_CODES = Object.freeze({
  /**
   * The schema refused the skeleton itself, so the field under test was never
   * reached. It is a bug in this module and not a fact about the caller's
   * value, which is why it is a code of its own rather than a refusal passed
   * through with the wrong blame.
   */
  PROBE: 'probe-refused',
});

/**
 * A minimal, valid registration: the message this app writes, with a
 * placeholder token and platform. The `mac` is 64 zeroes because the schema
 * judges a tag's SHAPE and never its value (it was verified by whoever read the
 * record, not by `validateMessage`), so a probe needs no key at all.
 */
const SKELETON = Object.freeze({
  id: 'push-probe',
  sender: 'push-probe',
  text: REGISTRATION_TEXT,
  at: 1,
  mac: '0'.repeat(64),
  kind: REGISTRATION_KIND,
  token: 'push-probe',
  platform: 'ios',
});

function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/** Is `name` one of the fields a registration carries? */
export function isRegistrationField(name) {
  return REGISTRATION_FIELDS.includes(name);
}

/**
 * What the schema says about ONE field of a registration: `null` when it
 * accepts the value, and its refusal otherwise.
 *
 * The caller gets the schema's own code and dotted path (`prefs.quiet_hours.end`
 * for a window whose ends are the same minute), so a screen can name the switch
 * at fault without a vocabulary of its own.
 *
 * @param {string} name one of `REGISTRATION_FIELDS`
 * @param {unknown} value the candidate
 * @returns {{ok: false, code: string, field: string}|null}
 * @throws {TypeError} for a name this module does not carry, which is a wiring
 *   bug: a caller asking about a field the schema has never heard of would be
 *   answered by the skeleton's own `unknown-field` refusal and read it as a bad
 *   value.
 */
export function judgeField(name, value) {
  if (!isRegistrationField(name)) {
    throw new TypeError(`judgeField: ${String(name)} is not a registration field`);
  }
  const judged = validateMessage({ ...SKELETON, [name]: value });
  if (judged.ok) return null;
  if (judged.field === name || (typeof judged.field === 'string' && judged.field.startsWith(`${name}.`))) {
    return judged;
  }
  return refuse(MESSAGE_CODES.PROBE, judged.field);
}

// **There is no second validator here, and that is deliberate.** The whole
// record is judged once, by the write path, with `validateMessage` over the
// bytes that are actually signed (`client.js`'s `register`); a `registrationFields`
// helper on this side would be a second answer to "is this registration legal",
// and the one that decides is the one the consumer will run. What this module
// offers is the per-FIELD question, which is what a screen and §3.8's controls
// need before there is any record to judge.
