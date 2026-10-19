// The mailbox's message schema and its validation (mobile plan, Task P0.2,
// extended by P6.3).
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
// disagree about what a stored message says; a validator that refuses
// everything with one code would leave a caller unable to tell a replay from a
// malformed write, which is why every refusal is named.
//
// ## What P6.3 adds, and why each field is here
//
//   agent       optional. D161's agent model: one mailbox per account, and the
//               agent a message is addressed to or came from travels in the
//               message. It is the id the consumer advertises in its heartbeat
//               (D161, D163), so it is held to that id's shape — "talk to
//               <agent>" matches this value against the advertised list, and a
//               value the list could never carry is not an agent.
//   kind        optional, `message` · `prompt` · `answer` · `action`, absent
//               meaning the ordinary message. The KIND is what tells the
//               consumer whether it is being asked something, told something or
//               handed a command, and P6.5/P6.7 map it to P1.1's windows
//               (D173: commands six hours, prompts the mailbox TTL).
//   options     optional, a prompt's own choices (§3.2). It is bounded in count
//               and in length because the app reads it ALOUD and the answer
//               names a choice by position ("option two"): a list nobody can
//               hold is not answerable, and two identical options are a choice
//               with two right answers.
//   reply_to    an answer's message id. Required on `answer`, allowed on
//               `action` (an action record answering a destructive prompt says
//               which prompt it settles, which is what lets P6.7 apply the
//               prompt's window rather than the command's), refused elsewhere.
//   action      optional, an action record, required on `action` and refused on
//               every other kind: D169's typed destructive command, which is
//               what makes "a confirmation only the app enforces is prose"
//               (G11) enforceable on the wire.
//   mac         REQUIRED on every message of every kind, inbox and outbox alike
//               (§3.5, D169, D171), and on the heartbeat (O7). This is the
//               field the whole extension exists for; see below.
//
// There is **no expiry field**. Under D168 as amended a prompt expires at
// D118's mailbox TTL, computed by the CONSUMER from `at` and its own clock, so
// an `expires_at` would be the phone's clock deciding — the clock D117 refuses
// to trust — and every name for one is an unknown field.
//
// There is still no version field. The field set IS the version, and with
// kinds it is the (kind, field) table: the rules' subtree idiom denies
// `$other`, and this validator refuses any field it does not name and any
// field on the wrong kind, so a message from a future protocol that carries an
// extra field is refused by the rules *and* here rather than silently accepted
// with the new field dropped. That is what fail-closed version skew looks like,
// without a second number for two artifacts to disagree about.
//
// ## The MAC, and why it is the point of this task
//
// A launcher can mint a custom token for any uid (G13) and can read
// `config/admin/devices` through `firebasedatabase.viewer`, so a minted token
// passes both of the rules' identity conjuncts and can write into a paired
// account's mailbox. What it cannot have is the per-pairing key of D171: that
// travels encrypted to a key the phone holds (D165) and never through the
// database. So the tag over the message's own bytes is the one thing that
// separates a message the phone really spoke from one a launcher invented, and
// it is why **every** inbox message carries one — not only action records: a
// plain `message` or `answer` saying "merge" is exactly the shape an impostor
// would use to answer the orchestrator's own "approve the merge?" prompt
// (§3.5).
//
// What the tag covers is **every field except `mac` itself**, with `acked`
// excluded because the phone writes it after the fact on an outbox message,
// plus the DIRECTION (inbox, outbox or heartbeat) the reader supplies, so a tag
// is valid in one direction only (owner ruling 2026-10-18, one field beyond
// §3.5's list; see DIRECTIONS), and the canonical serialisation is
// `canonicalBytes`' — sorted keys, no
// insignificant whitespace, absent fields omitted, JSON's own escaping only.
// The Go consumer reimplements that serialisation, so the two ends are held
// together by a vectors file (`vectors.json`) rather than by good intentions,
// and the module refuses two things that would make the bytes ambiguous
// between the two languages: the line/paragraph separators U+2028 and U+2029,
// which Go's encoder escapes and JavaScript's does not, and numbers that are
// not integers, which Go and JavaScript can spell differently. One message, one
// byte string.
//
// This module validates a tag's SHAPE and never its value — it has no key —
// and `verifyMac` is the separate, keyed question, in `./mac.js`.
// `shared/presence` gets an unsigned or wrongly signed heartbeat treated as
// offline (P3.6, §3.5).
//
// Pure functions over values, and — deliberately — **this file imports nothing
// at all**. The tag needs HMAC-SHA-256, and the only implementation of it in a
// runtime with no dependencies is a runtime builtin: Node has one, React
// Native's bundler resolves neither it nor a buffer type. So the tag lives in
// `./mac.js`, which imports Node's crypto and which this file never imports,
// and the entry point stays loadable by both of this package's consumers — the
// console and the phone (D119). A static import here would not cost the phone
// the tag alone; it would cost it `validateMessage` and `canonicalBytes` too,
// which are pure logic and which the phone needs most. So: no network, no
// clock, no randomness, no filesystem, no dependency, no import. The phone
// implements the tag itself, against `vectors.json`. `index.test.js` asserts
// that this file has no import, so adding one is a failing test rather than a
// discovery made by a bundler.

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
 * message). The rules must gate `text`'s length at this same number in P6.4;
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
 * The permitted length of an agent id, in UTF-16 code units, and of the ids in
 * a heartbeat's advertised list. The same number for both on purpose: they are
 * the same value — the app matches a message's `agent` against the heartbeat's
 * list, and D163 defaults to that list's first entry — and D117 already fixes
 * 64 as the bound on a short identity string (the boot id, which
 * `shared/presence` holds to the same rule).
 */
export const AGENT_MAX = 64;

/**
 * The permitted length of an agent's spoken name, in UTF-16 code units: the
 * name is said aloud and appears in "list agents", so it is bounded like every
 * other displayed string is not — 64 is a name, not a description.
 */
export const AGENT_NAME_MAX = 64;

/**
 * How many agents one heartbeat may advertise. D161's model is a short list an
 * installation offers; the bound exists so that one heartbeat cannot carry a
 * directory, and so that "list agents" has a bound to be read within.
 */
export const AGENTS_MAX = 32;

/**
 * How many options one prompt may carry, and how long each may be. §3.2 has
 * the app read the options aloud and accept an answer by position, so both
 * bounds are about being answerable rather than about storage.
 */
export const OPTIONS_MAX = 8;
export const OPTION_MAX = 200;

/**
 * The permitted length of an action record's target, in UTF-16 code units: a
 * run id or a repository, bounded like every other registry string (the same
 * 200 as SENDER_MAX, written separately because they are two different values
 * that happen to share a bound today).
 */
export const TARGET_MAX = 200;

/**
 * The lengths of the head SHA a merge record carries: 40 hex characters for
 * Git's SHA-1 object name and 64 for a SHA-256 repository. Both are accepted
 * because the provider decides which; nothing else is, because a head that
 * cannot be compared byte for byte cannot be compared at all.
 */
export const SHA_MIN = 40;
export const SHA_MAX = 64;

/**
 * The length of a MAC in hex characters: 32 bytes, HMAC-SHA-256 (§3.5).
 */
export const MAC_MAX = 64;

/**
 * The length of a boot id, in UTF-16 code units: D117's bound in
 * `shared/presence`, repeated here rather than imported because this module is
 * the phone's lowest layer (plan P0.2 is level 1, P1.4 is level 2) and a
 * level-1 module importing a level-2 one would invert the layering. The rule
 * is one rule, written twice, and the Go consumer writes it a third time; what
 * keeps the three together is that each is a shape, not a number to drift.
 */
export const BOOT_MAX = 64;

/**
 * Attributes a Realtime Database key may not contain, ported from the tree's
 * own key rule (`internal/budget`'s Key): `%` (the escape Unkey parses), the
 * six characters the database itself rejects in a key (`.`, `$`, `#`, `[`,
 * `]`, `/`), and every control character including DEL. `Key(s) === s` is that
 * rule's predicate, and a message id must satisfy it: the id is a node key
 * under the mailbox, so an id the database cannot hold is not an id.
 */
const FORBIDDEN_KEY_CHARS = new Set(['%', '.', '$', '#', '[', ']', '/']);

/**
 * The kind vocabulary: what a message IS, from the consumer's side of it.
 *
 *   message  something said to, or by, an agent (§3.1) — including a launch,
 *            which D162 sends with no read-back
 *   prompt   a question the agent wants answered (§3.2), with or without
 *            options
 *   answer   the answer to a prompt, naming it in `reply_to` and carrying the
 *            chosen words in `text`
 *   action   D169's typed destructive command
 *
 * This is not P1.1's own `KIND` (`command` · `prompt`), which names the two
 * ACCEPTANCE WINDOWS; P6.5 and P6.7 map a message kind to a window, and only
 * they can, because the mapping needs the prompt's TTL and the command window.
 */
export const KINDS = Object.freeze(['message', 'prompt', 'answer', 'action']);

/**
 * The verbs an action record may carry: exactly the three the owner's ruling
 * of 2026-10-17 names (§3.1) — cancel, merge/approve, and confirming an
 * action. A launch is deliberately absent: D162 answered that "kick off …"
 * goes through without a read-back, so it travels as an ordinary `message`
 * and never as an action record. Every other destructive verb — the kill
 * switch, budget writes, deletes — stays refused by voice, and so has no verb
 * here at all.
 */
export const ACTIONS = Object.freeze(['cancel', 'merge', 'confirm']);

/**
 * The directions a tag is made for, one per node a signed record lives in:
 * `inbox` (the phone signs, the consumer reads), `outbox` (the consumer signs,
 * the phone reads) and `heartbeat` (the consumer's liveness record). One
 * per-pairing key signs all three, so the direction is inside the covered bytes
 * — `canonicalBytes` adds it as the field `direction` — and a tag made for one
 * node does not verify in another: a message reflected from the inbox into the
 * outbox, or back, fails (owner ruling of 2026-10-18 on PR #579, one field
 * beyond §3.5's list). It is NOT a stored field: the reader supplies it from
 * the node it read the record from, so the writer never chooses it.
 */
export const DIRECTIONS = Object.freeze(['inbox', 'outbox', 'heartbeat']);

/** The name the direction is covered under, which no stored record may carry. */
const DIRECTION_FIELD = 'direction';

/** The exact fields a message has; any other field is refused. */
export const FIELDS = Object.freeze([
  'id',
  'sender',
  'text',
  'at',
  'mac',
  'acked',
  'agent',
  'kind',
  'options',
  'reply_to',
  'action',
]);

/** The fields a message must have, in the order they are checked. */
export const REQUIRED_FIELDS = Object.freeze(['id', 'sender', 'text', 'at', 'mac']);

/**
 * The fields each kind may carry beyond the shared ones, and the ones it must
 * carry. A field on the wrong kind is `field-not-allowed`, not an unknown
 * field: two agents disagreeing about whether a message is an action is
 * exactly the ambiguity this module exists to refuse.
 */
const KIND_OPTIONAL = Object.freeze({
  message: Object.freeze([]),
  prompt: Object.freeze(['options']),
  answer: Object.freeze(['reply_to']),
  action: Object.freeze(['action', 'reply_to']),
});

const KIND_REQUIRED = Object.freeze({
  message: Object.freeze([]),
  prompt: Object.freeze([]),
  answer: Object.freeze(['reply_to']),
  action: Object.freeze(['action']),
});

/** The kind a message with no `kind` field is. */
const DEFAULT_KIND = 'message';

/** An action record's fields, exactly, and the ones it must have. */
const ACTION_FIELDS = Object.freeze(['id', 'verb', 'target', 'head_sha', 'confirmation']);
const ACTION_REQUIRED = Object.freeze(['id', 'verb', 'target', 'confirmation']);

/** A confirmation's fields, exactly, and the ones it must have. */
const CONFIRMATION_FIELDS = Object.freeze(['id', 'at']);

/** A resolved target's fields: a run id, or a repository and a pull request. */
const RUN_TARGET_FIELDS = Object.freeze(['run']);
const REPO_TARGET_FIELDS = Object.freeze(['repo', 'pr']);

/** A heartbeat's fields, exactly, and the ones it must have (`agents` is optional). */
const HEARTBEAT_FIELDS = Object.freeze(['at', 'boot', 'agents', 'mac']);
const HEARTBEAT_REQUIRED = Object.freeze(['at', 'boot', 'mac']);

/** An advertised agent's fields, exactly. */
const AGENT_FIELDS = Object.freeze(['id', 'name']);

/** The refusal codes. Each names one family of refusal, so a caller can branch
 * on the reason rather than on the absence of a message. */
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
  /** A list holds more entries than its bound allows. */
  TOO_MANY: 'too-many',
  /** A list that is present is empty: a claim that says nothing. */
  TOO_FEW: 'too-few',
  /** A string contains a character that cannot be spoken, displayed or encoded. */
  BAD_CHARACTER: 'bad-character',
  /** A number is not a non-negative epoch-millisecond safe integer. */
  BAD_TIMESTAMP: 'bad-timestamp',
  /** A kind is not one of the four. */
  UNKNOWN_KIND: 'unknown-kind',
  /** An action's verb is not one of the three the ruling names. */
  UNKNOWN_VERB: 'unknown-verb',
  /** A field is present on a kind of message that does not carry it. */
  FIELD_NOT_ALLOWED: 'field-not-allowed',
  /** A resolved target is not one of the two shapes its verb resolves to. */
  BAD_TARGET: 'bad-target',
  /** A head SHA is not a hex object name of either length. */
  BAD_SHA: 'bad-sha',
  /** A mac is not 64 lowercase hex characters: a 32-byte HMAC-SHA-256 tag. */
  BAD_MAC: 'bad-mac',
  /** A list names the same id or option twice, so a choice by name or
   * position has two answers. */
  DUPLICATE_ENTRY: 'duplicate-entry',
});

/**
 * validateMessage decides whether value is a mailbox message.
 *
 * It returns `{ ok: true, message }` with a frozen DEEP copy of the message —
 * the named fields, values verbatim, plain unpolluted objects all the way down
 * — or `{ ok: false, code, field }`, where code is one of REFUSALS and field is
 * the field at fault, as a dotted path (`action.target.run`) when the fault is
 * inside a record and null when the fault is the value itself. It never throws
 * on a value and never repairs one.
 *
 * @param {unknown} value a candidate message, as read from the database or about to be written
 * @returns {{ ok: true, message: object } | { ok: false, code: string, field: string | null }}
 */
export function validateMessage(value) {
  if (!isPlainObject(value)) {
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

  const id = keyField(value.id, 'id');
  if (id.bad) return id.bad;
  const sender = stringField(value.sender, 'sender', SENDER_MAX, false);
  if (sender.bad) return sender.bad;
  const text = stringField(value.text, 'text', TEXT_MAX, true);
  if (text.bad) return text.bad;
  const at = timestampField(value.at, 'at');
  if (at.bad) return at.bad;
  const mac = macField(value.mac);
  if (mac.bad) return mac.bad;

  const message = {
    id: id.value,
    sender: sender.value,
    text: text.value,
    at: at.value,
    mac: mac.value,
  };

  let kind = DEFAULT_KIND;
  if (Object.hasOwn(value, 'kind')) {
    const judged = kindField(value.kind);
    if (judged.bad) return judged.bad;
    kind = judged.value;
    message.kind = kind;
  }

  if (Object.hasOwn(value, 'agent')) {
    const agent = identityField(value.agent, 'agent', AGENT_MAX);
    if (agent.bad) return agent.bad;
    message.agent = agent.value;
  }

  // The (kind, field) table, in the order that names the more useful fault
  // first: a field the kind cannot carry at all, then one it must carry and
  // does not, then the fields themselves.
  for (const field of ['options', 'reply_to', 'action']) {
    if (Object.hasOwn(value, field) && !KIND_OPTIONAL[kind].includes(field)) {
      return refuse(REFUSALS.FIELD_NOT_ALLOWED, field);
    }
  }
  for (const field of KIND_REQUIRED[kind]) {
    if (!Object.hasOwn(value, field)) {
      return refuse(REFUSALS.MISSING_FIELD, field);
    }
  }
  if (Object.hasOwn(value, 'options')) {
    const options = optionsField(value.options);
    if (options.bad) return options.bad;
    message.options = options.value;
  }
  if (Object.hasOwn(value, 'reply_to')) {
    const replyTo = keyField(value.reply_to, 'reply_to');
    if (replyTo.bad) return replyTo.bad;
    message.reply_to = replyTo.value;
  }
  if (Object.hasOwn(value, 'action')) {
    const action = actionField(value.action, kind);
    if (action.bad) return action.bad;
    message.action = action.value;
  }

  if (Object.hasOwn(value, 'acked')) {
    const acked = value.acked;
    if (typeof acked !== 'boolean') {
      return refuse(REFUSALS.WRONG_TYPE, 'acked');
    }
    message.acked = acked;
  }
  return { ok: true, message: deepFreeze(message) };
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

/**
 * validateHeartbeat decides whether value is a consumer's heartbeat — D117's
 * node, signed (P6.3, O7).
 *
 * It is a different record from a message with a different field set: `at`,
 * `boot`, an optional `agents` list, and a `mac` over all three. It returns
 * `{ ok: true, heartbeat }` frozen, or `{ ok: false, code, field }` in the
 * same shape validateMessage answers in.
 *
 * The tag matters here for the same reason it matters on a message, and for
 * one more: the ADVERTISED AGENT LIST is what "list agents" reads out and what
 * D163 defaults to, so an unsigned heartbeat would let any minted token
 * announce itself as the agent the user talks to. An unsigned or wrongly
 * signed heartbeat is one the phone treats as offline and whose list it
 * ignores (§3.5).
 *
 * @param {unknown} value a candidate heartbeat
 * @returns {{ ok: true, heartbeat: object } | { ok: false, code: string, field: string | null }}
 */
export function validateHeartbeat(value) {
  if (!isPlainObject(value)) {
    return refuse(REFUSALS.NOT_AN_OBJECT, null);
  }
  for (const key of Object.keys(value)) {
    if (!HEARTBEAT_FIELDS.includes(key)) {
      return refuse(REFUSALS.UNKNOWN_FIELD, key);
    }
  }
  for (const key of HEARTBEAT_REQUIRED) {
    if (!Object.hasOwn(value, key)) {
      return refuse(REFUSALS.MISSING_FIELD, key);
    }
  }

  const at = timestampField(value.at, 'at');
  if (at.bad) return at.bad;
  const boot = identityField(value.boot, 'boot', BOOT_MAX);
  if (boot.bad) return boot.bad;
  const mac = macField(value.mac);
  if (mac.bad) return mac.bad;

  const heartbeat = { at: at.value, boot: boot.value, mac: mac.value };
  if (Object.hasOwn(value, 'agents')) {
    const agents = agentsField(value.agents);
    if (agents.bad) return agents.bad;
    heartbeat.agents = agents.value;
  }
  return { ok: true, heartbeat: deepFreeze(heartbeat) };
}

/**
 * canonicalBytes is the one serialisation of a record that a MAC covers: the
 * record without `mac` and without `acked`, as JSON with keys sorted by code
 * point, no insignificant whitespace, absent fields omitted (never `null`),
 * strings as UTF-8 with JSON's own escaping only, and numbers as integers
 * (§3.5).
 *
 * It is the protocol's most exact surface. The Go consumer reimplements it and
 * tests against `vectors.json`, so the bytes here are what both ends must
 * produce — and it refuses, by throwing, the three classes the two languages
 * spell differently:
 *
 *   - `undefined` and `null`. A dropped field is two different records with
 *     one canonical form, and one byte string with one tag is exactly what the
 *     tag exists to prevent. Absent is omitted; present is covered.
 *   - a number that is not an integer. Go prints `1e-07` where JavaScript
 *     prints `1e-7` for the same double, so a float in a signed record is a
 *     tag that verifies on one side and not the other. The schema refuses a
 *     non-integer timestamp anyway, and every other number here is a count.
 *   - a string Go escapes and JavaScript does not, or the other way round: an
 *     **unpaired surrogate**, U+2028 and U+2029, in a key exactly as in a
 *     value. The schema refuses all three in every string it validates, so a
 *     caller that validated first cannot arrive here; this is the fence for the
 *     caller that did not.
 *
 * A caller that has validated first cannot reach any of them: `validateMessage`
 * refuses an explicit `undefined`, never writes a `null`, checks that `at`, the
 * confirmation time and a pull request number are integers, and refuses those
 * characters. Throwing, rather than coercing, is the fail-closed reading —
 * signing bytes that differ from the ones a reader will see is the failure mode
 * that matters.
 *
 * The keys are ordered by **code point**, at every depth, which is §3.5's words
 * and the Go consumer's bytewise order — not JavaScript's default UTF-16 order
 * (see `compareKeys`).
 *
 * The **direction** is covered too, as the field `direction` (one of
 * DIRECTIONS), by the owner's ruling of 2026-10-18 — one field beyond §3.5's
 * list. It is an argument, not a field of the record: the reader passes the
 * node it read from, so a tag made for the outbox does not verify as inbox.
 * A record that carries a `direction` of its own is refused by throwing.
 *
 * @param {object} record a validated message or heartbeat
 * @param {string} direction one of DIRECTIONS: the node the record is (to be) stored in
 * @returns {string} the bytes, as a string whose UTF-8 encoding is what is signed
 */
export function canonicalBytes(record, direction) {
  if (!isPlainObject(record)) {
    throw new TypeError('canonicalBytes: a record must be a plain object');
  }
  if (!DIRECTIONS.includes(direction)) {
    throw new TypeError('canonicalBytes: the direction must be one of inbox, outbox, heartbeat');
  }
  if (Object.hasOwn(record, DIRECTION_FIELD)) {
    throw new TypeError('canonicalBytes: a record may not carry its own direction; the reader supplies it');
  }
  // `mac` is the tag itself, and `acked` is the phone's write after the tag was
  // made (§3.5). Every other field is covered, including one a later protocol
  // version adds: absent fields are omitted, so an unknown field signs as itself
  // rather than being silently outside the bytes. The copy has no prototype, so
  // an own `__proto__` key stays a field.
  const covered = Object.create(null);
  for (const key of Object.keys(record)) {
    if (key !== 'mac' && key !== 'acked') covered[key] = record[key];
  }
  covered[DIRECTION_FIELD] = direction;
  return serialiseObject(covered);
}


// refuse builds a refusal verdict.
function refuse(code, field) {
  return { ok: false, code, field };
}

// isPlainObject: an object, and not a null and not an array. A function and a
// primitive are both something else entirely.
function isPlainObject(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

// deepFreeze freezes a validated record all the way down. The copy is a copy
// at every level, not a reference to the caller's nested objects, and it is
// frozen at every level: a validated action record that could be edited after
// its tag was checked would be a record whose tag proves nothing.
function deepFreeze(value) {
  if (value === null || typeof value !== 'object') return value;
  for (const key of Object.keys(value)) {
    deepFreeze(value[key]);
  }
  return Object.freeze(value);
}

// keyField validates an id: a non-empty string that is a database key.
function keyField(value, field) {
  if (typeof value !== 'string') return { bad: refuse(REFUSALS.WRONG_TYPE, field) };
  if (value === '') return { bad: refuse(REFUSALS.EMPTY_STRING, field) };
  if (value.length > ID_MAX) return { bad: refuse(REFUSALS.TOO_LONG, field) };
  for (let i = 0; i < value.length; i++) {
    const code = value.charCodeAt(i);
    if (FORBIDDEN_KEY_CHARS.has(value[i]) || code < 0x20 || code === 0x7f || isSeparator(value, i)) {
      return { bad: refuse(REFUSALS.BAD_KEY, field) };
    }
  }
  if (hasLoneSurrogate(value)) return { bad: refuse(REFUSALS.BAD_KEY, field) };
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
    if ((code < 0x20 && !line) || code === 0x7f || isSeparator(value, i)) {
      return { bad: refuse(REFUSALS.BAD_CHARACTER, field) };
    }
  }
  if (hasLoneSurrogate(value)) return { bad: refuse(REFUSALS.BAD_CHARACTER, field) };
  return { value };
}

// identityField validates a short identity string — an agent id, an
// advertised agent's id, a boot id: non-empty, bounded, and drawn from the
// narrow set D117 fixes for one (letters, digits, `.`, `_`, `:`, `-`). The
// characters outside it are refused as `bad-character`, which is the same
// question the control-character rule asks: can this string be what it claims
// to be? A value that cannot be spoken as an agent's id is not one.
function identityField(value, field, max) {
  // The shared checks come from stringField rather than a second copy of them:
  // a type, a bound and a control character mean the same thing here as
  // anywhere else, and a copy is a second place for them to drift.
  const text = stringField(value, field, max, false);
  if (text.bad) return { bad: text.bad };
  if (!IDENTITY_PATTERN.test(text.value)) return { bad: refuse(REFUSALS.BAD_CHARACTER, field) };
  return { value: text.value };
}

// optionsField validates a prompt's options: a present list is a non-empty
// list of distinct, bounded, single-line labels. Distinct matters because the
// app answers by position and reads the labels aloud — two identical options
// are a choice with two right answers.
function optionsField(value) {
  if (!Array.isArray(value)) return { bad: refuse(REFUSALS.WRONG_TYPE, 'options') };
  if (value.length === 0) return { bad: refuse(REFUSALS.TOO_FEW, 'options') };
  if (value.length > OPTIONS_MAX) return { bad: refuse(REFUSALS.TOO_MANY, 'options') };
  const options = [];
  const seen = new Set();
  for (let i = 0; i < value.length; i++) {
    const option = stringField(value[i], `options[${i}]`, OPTION_MAX, false);
    if (option.bad) return { bad: option.bad };
    if (seen.has(option.value)) return { bad: refuse(REFUSALS.DUPLICATE_ENTRY, `options[${i}]`) };
    seen.add(option.value);
    options.push(option.value);
  }
  return { value: options };
}

// agentsField validates a heartbeat's advertised agent list: a present list is
// a non-empty list of {id, name}, bounded in count, with distinct ids — two
// agents under one id make "talk to <agent>" ambiguous, and D163 picks the
// list's first entry by that id.
function agentsField(value) {
  if (!Array.isArray(value)) return { bad: refuse(REFUSALS.WRONG_TYPE, 'agents') };
  if (value.length === 0) return { bad: refuse(REFUSALS.TOO_FEW, 'agents') };
  if (value.length > AGENTS_MAX) return { bad: refuse(REFUSALS.TOO_MANY, 'agents') };
  const agents = [];
  const seen = new Set();
  for (let i = 0; i < value.length; i++) {
    const entry = value[i];
    if (!isPlainObject(entry)) return { bad: refuse(REFUSALS.NOT_AN_OBJECT, `agents[${i}]`) };
    for (const key of Object.keys(entry)) {
      if (!AGENT_FIELDS.includes(key)) return { bad: refuse(REFUSALS.UNKNOWN_FIELD, `agents[${i}].${key}`) };
    }
    for (const key of AGENT_FIELDS) {
      if (!Object.hasOwn(entry, key)) return { bad: refuse(REFUSALS.MISSING_FIELD, `agents[${i}].${key}`) };
    }
    const id = identityField(entry.id, `agents[${i}].id`, AGENT_MAX);
    if (id.bad) return { bad: id.bad };
    const name = stringField(entry.name, `agents[${i}].name`, AGENT_NAME_MAX, false);
    if (name.bad) return { bad: name.bad };
    if (seen.has(id.value)) return { bad: refuse(REFUSALS.DUPLICATE_ENTRY, `agents[${i}].id`) };
    seen.add(id.value);
    agents.push({ id: id.value, name: name.value });
  }
  return { value: agents };
}

// kindField validates a message's kind, defaulted by the caller when absent:
// a string drawn from the four, refused as `unknown-kind` rather than as a
// wrong type, because "not a kind" and "not a string" are different questions
// and P1.1 answers its own kind field the same way.
function kindField(value) {
  if (typeof value !== 'string') return { bad: refuse(REFUSALS.WRONG_TYPE, 'kind') };
  if (!KINDS.includes(value)) return { bad: refuse(REFUSALS.UNKNOWN_KIND, 'kind') };
  return { value };
}

// actionField validates D169's action record: the verb, the target it
// resolves to, the action id, the confirmation record, and — for a merge — the
// head SHA the read-back named. Every part is required because every part is
// executed: the consumer runs `fugaro cancel RUN` or `gh pr merge <n> --repo
// <owner/name>` from these fields and nothing else, and a record missing any of
// them is one that cannot be checked before it runs.
function actionField(value, kind) {
  if (kind !== 'action') return { bad: refuse(REFUSALS.FIELD_NOT_ALLOWED, 'action') };
  if (!isPlainObject(value)) return { bad: refuse(REFUSALS.NOT_AN_OBJECT, 'action') };
  for (const key of Object.keys(value)) {
    if (!ACTION_FIELDS.includes(key)) return { bad: refuse(REFUSALS.UNKNOWN_FIELD, `action.${key}`) };
  }
  for (const key of ACTION_REQUIRED) {
    if (!Object.hasOwn(value, key)) return { bad: refuse(REFUSALS.MISSING_FIELD, `action.${key}`) };
  }

  const id = keyField(value.id, 'action.id');
  if (id.bad) return { bad: id.bad };

  if (typeof value.verb !== 'string') return { bad: refuse(REFUSALS.WRONG_TYPE, 'action.verb') };
  if (!ACTIONS.includes(value.verb)) return { bad: refuse(REFUSALS.UNKNOWN_VERB, 'action.verb') };
  const verb = value.verb;

  const target = targetField(value.target, verb);
  if (target.bad) return { bad: target.bad };

  // A merge carries the head SHA read back at confirmation, and the consumer
  // re-reads the pull request's head and refuses the merge if it has moved
  // (D169, §3.5). A SHA on any other verb is a field the record has no
  // business carrying: a cancel names a run, and there is no head to compare.
  if (verb === 'merge' && !Object.hasOwn(value, 'head_sha')) {
    return { bad: refuse(REFUSALS.MISSING_FIELD, 'action.head_sha') };
  }
  let headSha;
  if (Object.hasOwn(value, 'head_sha')) {
    if (verb !== 'merge') return { bad: refuse(REFUSALS.FIELD_NOT_ALLOWED, 'action.head_sha') };
    headSha = shaField(value.head_sha);
    if (headSha.bad) return { bad: headSha.bad };
  }

  const action = {
    id: id.value,
    verb,
    target: target.value,
    confirmation: null,
  };
  if (headSha) action.head_sha = headSha.value;

  const confirmation = confirmationField(value.confirmation);
  if (confirmation.bad) return { bad: confirmation.bad };
  action.confirmation = confirmation.value;

  // The keys are built in the schema's own order, not the caller's: the object
  // is a copy, and `canonicalBytes` sorts them anyway, so the order here only
  // decides what a human sees in a stack trace.
  return {
    value: {
      id: action.id,
      verb: action.verb,
      target: action.target,
      ...(action.head_sha === undefined ? {} : { head_sha: action.head_sha }),
      confirmation: action.confirmation,
    },
  };
}

// targetField validates a resolved target: a run id, or a repository and a
// pull request number, and the verb decides which. `confirm` — the ruling's
// third verb, confirming an action — takes either, because the action being
// confirmed can be a cancel or a merge.
//
// The order of the questions is deliberate: what shape is this target, is that
// shape complete and well formed, and only then does it agree with the verb.
// A `{repo}` on a cancel is an INCOMPLETE repository target before it is a
// target the verb cannot use, and reporting the missing pull request number is
// what lets the reader fix the record rather than guess at it.
//
// The shape rules are security rules, not tidiness ones. The consumer passes
// the run id to `fugaro cancel` and the repository to `gh pr merge --repo` as
// ARGV elements, so a value beginning with `-` or `.` is a flag — or a path —
// to whatever parser sees it, and a value carrying a space, a newline, a NUL or
// a slash where a slash does not belong is not a run id or a repository at all.
// Refusing both families here is what stops a record from choosing the
// consumer's command line. The two families are named separately, like every
// other refusal in this module: a character outside the alphabet is
// `bad-character` — the same fault a control character in a body is — and a
// value that is spelled from the alphabet but is not one of the two shapes is
// `bad-target`.
function targetField(value, verb) {
  if (!isPlainObject(value)) return { bad: refuse(REFUSALS.NOT_AN_OBJECT, 'action.target') };

  const isRun = Object.hasOwn(value, 'run');
  const isRepo = Object.hasOwn(value, 'repo') || Object.hasOwn(value, 'pr');

  // A target is one shape or the other, never both: a record naming a run and a
  // pull request is a record the consumer would have to choose between, and a
  // guess about which one the user confirmed is exactly what D169's read-back
  // exists to remove.
  if (isRun && isRepo) return { bad: refuse(REFUSALS.BAD_TARGET, 'action.target') };
  if (!isRun && !isRepo) return { bad: refuse(REFUSALS.BAD_TARGET, 'action.target') };

  if (isRun) {
    for (const key of Object.keys(value)) {
      if (!RUN_TARGET_FIELDS.includes(key)) return { bad: refuse(REFUSALS.UNKNOWN_FIELD, `action.target.${key}`) };
    }
    const run = value.run;
    if (typeof run !== 'string') return { bad: refuse(REFUSALS.WRONG_TYPE, 'action.target.run') };
    if (run === '') return { bad: refuse(REFUSALS.EMPTY_STRING, 'action.target.run') };
    if (run.length > TARGET_MAX) return { bad: refuse(REFUSALS.TOO_LONG, 'action.target.run') };
    if (!RUN_CHARS.test(run)) return { bad: refuse(REFUSALS.BAD_CHARACTER, 'action.target.run') };
    if (!RUN_PATTERN.test(run)) return { bad: refuse(REFUSALS.BAD_TARGET, 'action.target.run') };
    if (verb === 'merge') return { bad: refuse(REFUSALS.BAD_TARGET, 'action.target') };
    return { value: { run } };
  }

  for (const key of Object.keys(value)) {
    if (!REPO_TARGET_FIELDS.includes(key)) return { bad: refuse(REFUSALS.UNKNOWN_FIELD, `action.target.${key}`) };
  }
  for (const key of REPO_TARGET_FIELDS) {
    if (!Object.hasOwn(value, key)) return { bad: refuse(REFUSALS.MISSING_FIELD, `action.target.${key}`) };
  }
  const repo = value.repo;
  if (typeof repo !== 'string') return { bad: refuse(REFUSALS.WRONG_TYPE, 'action.target.repo') };
  if (repo === '') return { bad: refuse(REFUSALS.EMPTY_STRING, 'action.target.repo') };
  if (repo.length > TARGET_MAX) return { bad: refuse(REFUSALS.TOO_LONG, 'action.target.repo') };
  if (!REPO_CHARS.test(repo)) return { bad: refuse(REFUSALS.BAD_CHARACTER, 'action.target.repo') };
  if (!REPO_PATTERN.test(repo)) return { bad: refuse(REFUSALS.BAD_TARGET, 'action.target.repo') };
  const pr = value.pr;
  if (typeof pr !== 'number') return { bad: refuse(REFUSALS.WRONG_TYPE, 'action.target.pr') };
  if (!Number.isSafeInteger(pr) || pr < 1) return { bad: refuse(REFUSALS.BAD_TARGET, 'action.target.pr') };
  // A merge is the only verb that resolves to a repository (D169: the ruling's
  // "cancel" cancels a run and "merge" merges a pull request; `confirm` may be
  // either, because it confirms whichever action the read-back named).
  if (verb === 'cancel') return { bad: refuse(REFUSALS.BAD_TARGET, 'action.target') };
  return { value: { repo, pr } };
}

// confirmationField validates the confirmation: the id of the confirmation and
// the time it was given. The id is the ledger's (the consumer refuses a reused
// one) and the time is what the 6-hour command window is measured from, with
// the 5-minute future tolerance the consumer applies (§3.5). The timeout
// itself is the phone's, P6.2's, and is deliberately not here: the pending time
// is phone-local state.
function confirmationField(value) {
  if (!isPlainObject(value)) return { bad: refuse(REFUSALS.NOT_AN_OBJECT, 'action.confirmation') };
  for (const key of Object.keys(value)) {
    if (!CONFIRMATION_FIELDS.includes(key)) {
      return { bad: refuse(REFUSALS.UNKNOWN_FIELD, `action.confirmation.${key}`) };
    }
  }
  for (const key of CONFIRMATION_FIELDS) {
    if (!Object.hasOwn(value, key)) {
      return { bad: refuse(REFUSALS.MISSING_FIELD, `action.confirmation.${key}`) };
    }
  }
  const id = keyField(value.id, 'action.confirmation.id');
  if (id.bad) return { bad: id.bad };
  const at = timestampField(value.at, 'action.confirmation.at');
  if (at.bad) return { bad: at.bad };
  return { value: { id: id.value, at: at.value } };
}

// shaField validates a merge's head SHA: lowercase hex, at one of the two
// object-name lengths. Lowercase only, for the reason the mac is lowercase
// only — one object name has two spellings otherwise, and the consumer's
// comparison is byte for byte against what the provider returns.
function shaField(value) {
  if (typeof value !== 'string') return { bad: refuse(REFUSALS.BAD_SHA, 'action.head_sha') };
  if (value.length !== SHA_MIN && value.length !== SHA_MAX) {
    return { bad: refuse(REFUSALS.BAD_SHA, 'action.head_sha') };
  }
  if (!HEX_PATTERN.test(value)) return { bad: refuse(REFUSALS.BAD_SHA, 'action.head_sha') };
  return { value };
}

// macField validates a MAC's shape: 64 lowercase hex characters, which is 32
// bytes of HMAC-SHA-256 (§3.5). Length and character set share one code
// because they are one question — is this a tag? — and uppercase is refused
// because hex has two spellings for one tag: accepting both would let one
// message verify in two encodings and make the comparison depend on which end
// wrote it. The value itself is not checked here; that needs a key, and it is
// `verifyMac`'s question.
function macField(value) {
  if (typeof value !== 'string') return { bad: refuse(REFUSALS.WRONG_TYPE, 'mac') };
  if (value === '') return { bad: refuse(REFUSALS.EMPTY_STRING, 'mac') };
  if (value.length !== MAC_MAX || !HEX_PATTERN.test(value)) {
    return { bad: refuse(REFUSALS.BAD_MAC, 'mac') };
  }
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

// isSeparator reports whether s[i] is U+2028 LINE SEPARATOR or U+2029
// PARAGRAPH SEPARATOR. They are refused for a reason the two ends can measure
// and neither can see: Go's `encoding/json` escapes both even with
// `SetEscapeHTML(false)` set, and JavaScript's `JSON.stringify` leaves both as
// themselves. A message carrying one therefore has TWO canonical byte strings,
// one per language, and the tag computed over the phone's does not verify
// against the consumer's — a refusal nobody could diagnose, for a character
// nobody can speak. Refusing it here means one message has one byte string,
// which is the only thing a MAC can mean across two languages.
function isSeparator(s, i) {
  const code = s.charCodeAt(i);
  return code === 0x2028 || code === 0x2029;
}

// serialiseValue writes one value's canonical bytes, refusing the values a
// canonical form cannot survive. It writes the JSON itself rather than
// rebuilding an object and handing it to `JSON.stringify`, because a rebuilt
// object cannot hold the order or the keys the bytes need: JavaScript
// enumerates integer-like keys ("9", "10") numerically and before every other
// key whatever the insertion order, and assigning `__proto__` sets a prototype
// instead of a field — which dropped the field from the bytes. Arrays keep their
// order — an option's position is what "option two" means — and each string,
// key or value, goes through `JSON.stringify` alone, so that JSON's own escaping
// is the only escaping.
function serialiseValue(value) {
  if (value === undefined || value === null) {
    throw new TypeError(
      'canonicalBytes: absent fields are omitted and never null, so a record carrying one has no canonical form',
    );
  }
  switch (typeof value) {
    case 'string':
      return JSON.stringify(canonicalString(value, 'a string'));
    case 'boolean':
      return value ? 'true' : 'false';
    case 'number':
      // Go prints `1e-07` where JavaScript prints `1e-7` for the same double, so
      // a float is a byte string two languages spell differently; an integer
      // past the exact-double line is printed in exponent form here and in full
      // (or not at all) by Go's integer types; and -0 is written "0", so it
      // would sign as a value it is not. Safe integers — every number this
      // schema accepts — agree exactly.
      if (!Number.isSafeInteger(value) || Object.is(value, -0)) {
        throw new TypeError('canonicalBytes: a number must be a safe integer, and not -0');
      }
      return String(value);
    case 'object':
      if (Array.isArray(value)) return `[${value.map(serialiseValue).join(',')}]`;
      return serialiseObject(value);
    default:
      throw new TypeError(`canonicalBytes: a ${typeof value} is not a value a message carries`);
  }
}

// serialiseObject writes a plain object's canonical bytes: its own enumerable
// keys, sorted by code point, each written as a JSON string.
// An object that is not plain data — a Date, a Map, a class instance — has a
// shape decided by its methods rather than its fields, and is refused.
function serialiseObject(value) {
  const proto = Object.getPrototypeOf(value);
  if (proto !== Object.prototype && proto !== null) {
    throw new TypeError('canonicalBytes: only plain objects have a canonical form');
  }
  const members = [];
  for (const key of Object.keys(value).sort(compareKeys)) {
    members.push(`${JSON.stringify(canonicalString(key, 'a key'))}:${serialiseValue(value[key])}`);
  }
  return `{${members.join(',')}}`;
}

/**
 * compareKeys orders two keys by **code point**, which is what §3.5 mandates and
 * what the Go consumer's bytewise map-key order produces (UTF-8 byte order IS
 * code-point order). JavaScript's own `Array.prototype.sort` compares UTF-16
 * **code units**, and the two disagree for exactly one pair of ranges: a key
 * above U+FFFF — an astral character, whose first code unit is a high surrogate
 * in D800..DBFF — sorts BEFORE a key in U+E000..U+FFFF under `.sort()` and
 * AFTER it by code point. The two ends would then emit the keys of one record in
 * opposite orders and compute two different tags for it.
 *
 * Nothing `validateMessage` accepts can reach this — every field name is ASCII —
 * but `canonicalBytes` is exported, its whole contract is byte-equality between
 * the languages, and the hazard costs four lines to remove. For the BMP the two
 * orders are the same, which is why this is narrow enough to have gone unnoticed.
 */
function compareKeys(a, b) {
  let i = 0;
  let j = 0;
  while (i < a.length && j < b.length) {
    const left = a.codePointAt(i);
    const right = b.codePointAt(j);
    if (left !== right) return left < right ? -1 : 1;
    i += left > 0xffff ? 2 : 1;
    j += right > 0xffff ? 2 : 1;
  }
  return a.length - i - (b.length - j);
}

/**
 * canonicalString is the check a string has to pass to have a canonical form at
 * all, for a key exactly as for a value: Go's encoder writes U+FFFD for a lone
 * surrogate, escapes U+2028 and U+2029 even with `SetEscapeHTML(false)`, and
 * writes every other code point as itself, while JavaScript writes "\ud800" for
 * the surrogate and leaves the two separators alone.
 *
 * The schema refuses both classes in every string it validates
 * (`bad-character`, `bad-key`), so a caller that validated first cannot arrive
 * here. This is the second fence, for the caller that did not: `canonicalBytes`
 * is exported and is a general serialisation, and a record whose strings the two
 * languages spell differently has no canonical form to sign.
 */
function canonicalString(value, where) {
  if (hasLoneSurrogate(value)) {
    throw new TypeError(
      `canonicalBytes: ${where} carries an unpaired surrogate, which the two encoders spell differently`,
    );
  }
  for (let i = 0; i < value.length; i++) {
    if (isSeparator(value, i)) {
      throw new TypeError(
        `canonicalBytes: ${where} carries U+2028 or U+2029, which Go's encoder escapes and JavaScript's does not`,
      );
    }
  }
  return value;
}

// timestampField validates at: a non-negative integer below the exact-double
// line, in epoch milliseconds. A numeric string, a float, NaN and Infinity are
// each something else, and each is refused rather than rounded.
function timestampField(value, field) {
  if (typeof value !== 'number') return { bad: refuse(REFUSALS.WRONG_TYPE, field) };
  if (!Number.isSafeInteger(value) || value < 0) return { bad: refuse(REFUSALS.BAD_TIMESTAMP, field) };
  return { value };
}

/** A short identity string: D117's alphabet for a boot id, an agent id. */
const IDENTITY_PATTERN = /^[A-Za-z0-9._:-]+$/;

/**
 * The characters a run id may be spelled from: the identity alphabet, which is
 * `fugaro`'s own run-id shape (`YYYYMMDD-HHMMSS-xxxx`) with room to spare. A
 * character outside it — a space, a newline, a NUL, a slash — is
 * `bad-character`.
 */
const RUN_CHARS = /^[A-Za-z0-9._:-]+$/;

/**
 * A run id as the consumer will pass it to `fugaro cancel`: the alphabet
 * above, and never a leading `-` or `.`, which every argument parser reads as
 * a flag or a path.
 */
const RUN_PATTERN = /^[A-Za-z0-9_:][A-Za-z0-9._:-]*$/;

/**
 * The characters a repository may be spelled from: the identity alphabet plus
 * the separator. Anything else — a space, a newline, a control character — is
 * `bad-character`.
 */
const REPO_CHARS = /^[A-Za-z0-9._:/-]+$/;

/**
 * A repository as the consumer will pass it to `gh pr merge --repo`: exactly
 * `owner/name`, each side starting with a letter, a digit or an underscore so
 * that neither the whole value nor either side begins with a `.` or a `-`.
 */
const REPO_PATTERN = /^[A-Za-z0-9_][A-Za-z0-9._-]*\/[A-Za-z0-9_][A-Za-z0-9._-]*$/;

/** Lowercase hexadecimal, which is the one spelling a tag or a SHA has here. */
const HEX_PATTERN = /^[0-9a-f]+$/;
