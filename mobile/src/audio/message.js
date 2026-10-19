// From a transcript to a mailbox message (mobile plan, Task P3.4).
//
// **The plan's verification names this rule in one line** -- *a fake transcript
// becomes a message* -- and the rule is one function, because the thing that
// decides whether a transcript is a message is **not this module**. It is the
// shared schema (`../../shared/mailbox-schema/`, P0.2), which P1.1 validates
// against, which P3.6 signs and writes, and which P4.1's consumer reads. D119
// gives the app exactly one sibling it may reach outside `mobile/`, and this is
// the one place in `src/audio/` that reaches it: a second, looser rule here
// would be a second protocol, and two artifacts disagreeing about what a
// message is is the failure the schema exists to prevent.
//
// **So the honest shape of this function is small.** It composes the four
// fields the schema requires of a draft (the fifth, `mac`, is P3.6's) -- `id`, `sender`, `text`, `at` -- from values it
// is given, and hands the result to `validateMessage`. It does not trim, does
// not redact, does not generate the id, does not read a clock, and does not
// add a field. What it adds is one thing the schema cannot: a **refusal of a
// field it does not know**, with the schema's own `unknown-field` code, so
// that a caller reaching for a field a draft does not carry learns it from the
// protocol rather than from a silently dropped key.
//
// **`kind` is refused because of this module's scope, not the protocol's.**
// The protocol carries `kind` -- P6.3 (approved 2026-10-18, merged) put it in
// the schema's field set, alongside `agent`, `options`, `reply_to` and the
// action record -- and it is an **optional** field: a message without one is
// the default `message`, and the schema accepts a `prompt` or an `answer` too.
// A draft composes the schema's *required* fields and nothing else
// (`DRAFT_FIELDS`), and the one thing P3.4 makes is a plain utterance: the
// voice pipeline turns a final transcript into a `message`, which is what an
// absent `kind` means. Choosing another kind is not this module's business --
// a prompt is P6.5's queue and a typed command is P6.9's text screen -- so a
// caller that hands `kind` to `messageDraft` is handing it a field a draft
// does not have, and hears so.
//
// **What a draft is not.** It is not signed and not written: both belong to
// other modules -- the `mac` to the protocol (P6.3), the signing and the write
// to the client (P3.6, merged, and not wired to this pipeline yet) -- and P1.3's
// redactor is applied where the message is written (D118: text is redacted
// *before it is written*, and the writer is the one that writes). A draft is a
// value on its way to a screen or to the client; if it were the thing that
// reached the database, the redaction rule would be enforced here instead -- and
// it is not, deliberately, because a module that redacts is a module whose
// caller can believe it need not.
//
// **A transcript the schema refuses is not a message.** The case that matters
// is the common one: the microphone heard a cough, and the engine's final
// transcript is the empty string. The schema refuses an empty body, and this
// function passes that refusal through as itself -- so §5's *nothing is
// accepted into the void* is a consequence of the protocol rather than a rule
// restated here (and `pipeline.js` has a test for exactly that turn).

import { MAC_MAX, REFUSALS, REQUIRED_FIELDS, validateMessage } from '../../../shared/mailbox-schema/index.js';
import { refuse } from './refusal.js';

/**
 * The fields a draft is composed of: the schema's four required fields, in the
 * schema's order. `FIELDS` in the shared schema is the protocol; this list is
 * what a caller must supply, and the two are asserted to agree by this
 * module's tests, so a fifth required field cannot be added to the protocol
 * without a failing test here.
 */
export const DRAFT_FIELDS = Object.freeze(REQUIRED_FIELDS.filter((field) => field !== 'mac'));

/**
 * The schema now requires a `mac` on every message (P6.3), and a draft is
 * unsigned: P3.6's client signs it where it is written. So the draft is judged
 * by the schema with a well-formed stand-in tag (the right length and
 * alphabet), which the schema checks for shape only, and the stand-in is
 * dropped from the result: the draft that comes back carries no `mac`.
 */
const UNSIGNED_STAND_IN = '0'.repeat(MAC_MAX);

/**
 * Compose a mailbox message from one final transcript.
 *
 * Returns the schema's verdict: `{ ok: true, message }` with a frozen message,
 * or `{ ok: false, code, field }` with one of the schema's own refusal codes
 * (`empty-string` for a transcript that says nothing, `bad-timestamp` for an
 * `at` that is not a time, `unknown-field` for a field this draft does not
 * have, `missing-field` for one of the four that is absent). Nothing is
 * repaired on the way out, and the draft's presence is preserved: an absent
 * field is absent to the schema too, so the refusal names the fault the caller
 * has to fix.
 *
 * @param {object} draft
 * @param {string} draft.text the engine's final transcript, verbatim
 * @param {string} draft.sender the writing identity (P3.6 supplies the account's)
 * @param {string} draft.id a **fresh** id per message (P3.6 supplies the source)
 * @param {number} draft.at epoch milliseconds (the server-anchored clock, D117)
 * @returns {{ ok: true, message: object } | { ok: false, code: string, field: string | null }}
 */
export function messageDraft(draft) {
  if (draft === null || typeof draft !== 'object' || Array.isArray(draft)) {
    return refuse(REFUSALS.NOT_AN_OBJECT, null);
  }
  for (const key of Object.keys(draft)) {
    if (!DRAFT_FIELDS.includes(key)) return refuse(REFUSALS.UNKNOWN_FIELD, key);
  }
  // The candidate is the draft's own fields -- and only the ones the draft
  // actually has. Writing `id: undefined` for an absent id would turn the
  // schema's `missing-field` into a `wrong-type`, and the refusal a caller
  // branches on would then name the wrong fault.
  const candidate = {};
  for (const field of DRAFT_FIELDS) {
    if (Object.hasOwn(draft, field)) candidate[field] = draft[field];
  }
  const verdict = validateMessage({ ...candidate, mac: UNSIGNED_STAND_IN });
  if (!verdict.ok) return refuse(verdict.code, verdict.field);
  const { mac: _stoodIn, ...message } = verdict.message;
  return Object.freeze({ ok: true, message: Object.freeze(message) });
}
