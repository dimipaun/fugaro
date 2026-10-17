// The mailbox's message schema and its validation (mobile plan, Task P0.2).
//
// The schema is the protocol. It is what P1.1's state machine validates
// against, what P3.6's app writes and what P4.1's consumer reads, and it is
// deliberately small: the plan's critical path runs through this task, and a
// schema that grew into "everything that validates against it" would grow the
// critical path with it. What the schema decides is one question — whether a
// value is a message — and nothing else: the redactor is P1.3's (D118 redacts
// before the write), the dedupe table and the replay window are P1.1's, the
// link rules are P0.3's, and who may write a message is P2.1's rules.
//
// **Fail closed.** An unknown field, a wrong type, an oversize body, an id
// that cannot be a database key, a timestamp that is not an epoch-millisecond
// safe integer and a string that cannot survive JSON are all REFUSALS, each
// with its own code, and none of them is repaired: nothing is trimmed, floored,
// stringified or dropped. A validator that coerced would let two agents
// disagree about what a stored message says; a validator that refused
// everything with one code would leave a caller unable to tell a replay from a
// malformed write, which is why every refusal is named.
//
// The fields, and where each is decided:
//
//   id      string, required. The explicit message id D110 supersedes D49
//           with: P1.1 dedupes on (sender, id). It is a Realtime Database
//           node key — a message lives at <mailbox>/<message>/<id> — so it is
//           held to the tree's own key rule (see FORBIDDEN_KEY_CHARS) and to
//           the key length bound the rules already carry.
//   sender  string, required. The writer's identity, opaque to the schema:
//           P1.1 does not dedupe an id from a different sender against one
//           already seen, and P2.1's rules are what bind this value to the
//           writer's claim. It is a value, not a path, so the key rule above
//           does not apply to it.
//   text    string, required. Spoken text, already redacted by P1.3, bounded
//           by D118's size bound per message. It is not empty: a message that
//           says nothing is an ambiguity, and the phone's offline state exists
//           so that nothing is accepted into the void.
//   at      number, required. When the sender wrote it, in epoch milliseconds
//           — the unit the tree already uses (budget.Day, the token's fx
//           claim) and the only unit the rules can compare against a server
//           timestamp. P1.1's replay window is a function of this value and an
//           injected now; the schema only refuses a value that is not a time.
//   acked   boolean, optional. The ack D110 requires and D113 grants: absent
//           means not acked, and it is the phone's one write besides its own
//           inbox message. It carries no timestamp of its own on purpose — the
//           phone's clock is the one D117 refuses to trust.
//
// There is no version field. The field set IS the version: the rules' subtree
// idiom denies `$other`, so a message from a future protocol that adds a field
// is refused by the rules and by this function rather than silently accepted
// with the new field dropped — which is what fail-closed version skew looks
// like, without a second number for two artifacts to disagree about.
//
// Pure functions over values. No network, no clock, no randomness, no
// filesystem, no dependency: node's own builtins are not even imported.

/**
 * The permitted length of a message id, in UTF-16 code units: the tree's own
 * key bound (`internal/budget/rules`' MaxKeyLength, 100), which is what the
 * rules' validators apply to a key. ASCII generated ids are the same number of
 * bytes, code units and characters, which is what makes one number enough
 * here.
 */
export const ID_MAX = 100;

/**
 * The permitted length of a sender, in UTF-16 code units: the tree's bound on
 * every registry string (`internal/budget/rules`' MaxString, 200).
 */
export const SENDER_MAX = 200;

/**
 * The permitted length of a body, in UTF-16 code units (D118's size bound per
 * message). The rules must gate `text`'s length at this same number in P2.1;
 * the number is chosen here because the schema is the protocol, and it is a
 * bound rather than a target: spoken turns are short, and the bound exists so
 * that one write cannot carry a document.
 */
export const TEXT_MAX = 4096;

/**
 * The largest timestamp the schema accepts, in epoch milliseconds: the largest
 * integer a double represents exactly. The tree keeps its own arithmetic below
 * this line for the same reason (`MaxAmount` in `internal/budget/rules`), and
 * a timestamp past it is not a time any clock can produce.
 */
export const AT_MAX = Number.MAX_SAFE_INTEGER;

/**
 * Characters a Realtime Database key may not contain, ported from the tree's
 * own key rule (`internal/budget`'s Key): `%` (the escape Unkey parses), the
 * six characters the database itself rejects in a key (`.`, `$`, `#`, `[`,
 * `]`, `/`), and every control character including DEL. `Key(s) === s` is that
 * rule's predicate, and a message id must satisfy it: the id is a node key
 * under the mailbox, so an id the database cannot hold is not an id.
 */
const FORBIDDEN_KEY_CHARS = new Set(['%', '.', '$', '#', '[', ']', '/']);

/** The exact fields a message has; any other field is refused. */
export const FIELDS = Object.freeze(['id', 'sender', 'text', 'at', 'acked']);

/** The fields a message must have, in the order they are checked. */
export const REQUIRED_FIELDS = Object.freeze(['id', 'sender', 'text', 'at']);

/**
 * The refusal codes. Each names one family of refusal, so a caller can branch
 * on the reason rather than on the absence of a message.
 */
export const REFUSALS = Object.freeze({
  /** The value is not a plain object: null, an array, a primitive, a function. */
  NOT_AN_OBJECT: 'not-an-object',
  /** A field the schema does not name is present. */
  UNKNOWN_FIELD: 'unknown-field',
  /** A required field is absent. */
  MISSING_FIELD: 'missing-field',
  /** A field holds a value of the wrong type, including an explicit undefined. */
  WRONG_TYPE: 'wrong-type',
  /** A required string is empty. */
  EMPTY_STRING: 'empty-string',
  /** An id contains a character a database key may not contain. */
  BAD_KEY: 'bad-key',
  /** A string is longer than its bound. */
  TOO_LONG: 'too-long',
  /** A string contains a character that cannot be spoken, displayed or encoded. */
  BAD_CHARACTER: 'bad-character',
  /** A number is not a non-negative epoch-millisecond safe integer. */
  BAD_TIMESTAMP: 'bad-timestamp',
});

/**
 * validateMessage decides whether value is a mailbox message.
 *
 * It returns `{ ok: true, message }` with a frozen copy of the message — the
 * five fields, values verbatim, a plain unpolluted object — or
 * `{ ok: false, code, field }`, where code is one of REFUSALS and field is the
 * field at fault (null when the fault is the value itself). It never throws on
 * a value and never repairs one.
 *
 * @param {unknown} value a candidate message, as read from the database or about to be written
 * @returns {{ ok: true, message: object } | { ok: false, code: string, field: string | null }}
 */
export function validateMessage(value) {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) {
    return refuse(REFUSALS.NOT_AN_OBJECT, null);
  }

  // Unknown first: the field set is the version, and a field that is not in it
  // is refused before any of its neighbours is judged. Only own enumerable
  // string keys count — a value arriving from JSON parses `__proto__` into
  // one, and it is an unknown field rather than a prototype.
  for (const key of Object.keys(value)) {
    if (!FIELDS.includes(key)) {
      return refuse(REFUSALS.UNKNOWN_FIELD, key);
    }
  }

  for (const key of REQUIRED_FIELDS) {
    if (!Object.hasOwn(value, key)) {
      return refuse(REFUSALS.MISSING_FIELD, key);
    }
  }

  const id = keyField(value.id);
  if (id.bad) return id.bad;
  const sender = stringField(value.sender, 'sender', SENDER_MAX, false);
  if (sender.bad) return sender.bad;
  const text = stringField(value.text, 'text', TEXT_MAX, true);
  if (text.bad) return text.bad;
  const at = timestampField(value.at);
  if (at.bad) return at.bad;

  const message = { id: id.value, sender: sender.value, text: text.value, at: at.value };
  if (Object.hasOwn(value, 'acked')) {
    if (typeof value.acked !== 'boolean') {
      return refuse(REFUSALS.WRONG_TYPE, 'acked');
    }
    message.acked = value.acked;
  }
  return { ok: true, message: Object.freeze(message) };
}

/**
 * isMessage is validateMessage's verdict as a boolean, for a caller that needs
 * no reason — a filter, a guard, a test's expectation.
 *
 * @param {unknown} value a candidate message
 * @returns {boolean} true when validateMessage would accept it
 */
export function isMessage(value) {
  return validateMessage(value).ok;
}

// refuse builds a refusal verdict.
function refuse(code, field) {
  return { ok: false, code, field };
}

// keyField validates the id: a non-empty string that is a database key.
function keyField(value) {
  if (typeof value !== 'string') return { bad: refuse(REFUSALS.WRONG_TYPE, 'id') };
  if (value === '') return { bad: refuse(REFUSALS.EMPTY_STRING, 'id') };
  if (value.length > ID_MAX) return { bad: refuse(REFUSALS.TOO_LONG, 'id') };
  for (let i = 0; i < value.length; i++) {
    const code = value.charCodeAt(i);
    if (FORBIDDEN_KEY_CHARS.has(value[i]) || code < 0x20 || code === 0x7f) {
      return { bad: refuse(REFUSALS.BAD_KEY, 'id') };
    }
  }
  if (hasLoneSurrogate(value)) return { bad: refuse(REFUSALS.BAD_KEY, 'id') };
  return { value };
}

// stringField validates a required, bounded, display-safe string: max is its
// bound, and lineBreaks says whether the string is a body (which may be
// several lines) rather than a single-line identity.
function stringField(value, field, max, lineBreaks) {
  if (typeof value !== 'string') return { bad: refuse(REFUSALS.WRONG_TYPE, field) };
  if (value === '') return { bad: refuse(REFUSALS.EMPTY_STRING, field) };
  if (value.length > max) return { bad: refuse(REFUSALS.TOO_LONG, field) };
  for (let i = 0; i < value.length; i++) {
    const code = value.charCodeAt(i);
    const line = lineBreaks && (code === 0x09 || code === 0x0a || code === 0x0d);
    if ((code < 0x20 && !line) || code === 0x7f) {
      return { bad: refuse(REFUSALS.BAD_CHARACTER, field) };
    }
  }
  if (hasLoneSurrogate(value)) return { bad: refuse(REFUSALS.BAD_CHARACTER, field) };
  return { value };
}

// hasLoneSurrogate reports whether s contains an unpaired surrogate: half of
// a character that JSON cannot encode, so a value carrying one cannot be
// written at all. A paired pair (an emoji) is a character and is fine.
function hasLoneSurrogate(s) {
  for (let i = 0; i < s.length; i++) {
    const code = s.charCodeAt(i);
    if (code >= 0xd800 && code <= 0xdbff) {
      const next = s.charCodeAt(i + 1);
      if (!(next >= 0xdc00 && next <= 0xdfff)) return true;
      i++;
      continue;
    }
    if (code >= 0xdc00 && code <= 0xdfff) return true;
  }
  return false;
}

// timestampField validates at: a non-negative integer below the exact-double
// line, in epoch milliseconds. A numeric string, a float, NaN and Infinity are
// each something else, and each is refused rather than rounded.
function timestampField(value) {
  if (typeof value !== 'number') return { bad: refuse(REFUSALS.WRONG_TYPE, 'at') };
  if (!Number.isSafeInteger(value) || value < 0) return { bad: refuse(REFUSALS.BAD_TIMESTAMP, 'at') };
  return { value };
}
