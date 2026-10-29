// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The phone's mailbox client (mobile plan, Task P3.6; D111, D113, D113b,
// D118; functional-spec-reconciliation.md §3.5).
//
// **This is the only place in the app that writes to the database**, and the
// whole task is what it does NOT do:
//
//   * it writes **two paths and no others** (D113): the message node of its own
//     inbox -- one message per utterance, with a fresh id -- and the `acked`
//     child of an outbox message it has already spoken. The heartbeat is not
//     its write (D113b): it only reads it.
//   * it reads **its own outbox and the heartbeat**, under the (b1) grant, and
//     nothing else.
//   * **every message it writes carries a `mac`** over P6.3's canonical bytes,
//     made with D171's per-pairing key, read from platform secure storage
//     through `SecretStore.use` for the duration of one call and never held,
//     logged or stored in the clear (D111).
//   * it **verifies the tag of every outbox message and of the heartbeat**
//     before it speaks a word or acks (the tension (b) resolution): a message
//     whose tag does not verify is dropped, never spoken and never acked, and
//     an unsigned or wrongly signed heartbeat is `offline` with its advertised
//     agent list ignored.
//   * it **acks a message only after it has spoken it**, and a speak that
//     failed leaves the message unacked so the next pass tries again.
//   * **it never writes an unsigned message, and never attempts one**: with no
//     key in the store the send is refused before the store sees a write.
//   * it **re-delivers an unacked message after a reconnect** -- the same
//     record, id and `at` included -- so the consumer's ledger answers it as a
//     duplicate and does not act on it twice.
//   * **it never sends into the void** (P6.10, D167): with no connection, or
//     with the consumer stale or offline, nothing is written, the owner is told
//     why, and the utterance is kept **only** as unsent text to resend; a
//     destructive action record is never queued at all. See `gate.js` for the
//     decision and "The offline gate" below for what this file does with it.
//
// **What is injected, and why nothing is reached for.** The client reads no
// clock (`now` is D117's server-anchored now, supplied by the app), mints no
// id of its own (`newId`), touches no device (`store`, `speaker`) and derives
// no key (`keys`). The one thing it takes as configuration rather than as a
// function is D118's mailbox TTL, and the number it carries is the plan's 72
// hours. Nothing here reads `Date.now`, `Math.random`, a file, a global or a
// network, which is what makes every test in this directory a test of a
// decision rather than of a machine.
//
// **The two identities, which are not the same identity.** D114's account
// tuple names the *installation*: its `id` is the key the Keychain item is
// composed from. The *mailbox* is addressed by the Firebase Auth `uid`, which
// is what the rules' `$mb == auth.uid` macro means and what `sender` must
// equal in P2.1's template. So the client takes the account tuple and uses
// both, and takes neither from a caller who could pair them wrongly.
//
// **What it reports rather than decides.** A failed write, an expired retry, a
// dropped message and a failed ack are all in the return value of `send`,
// `flush` and `poll`, each with the code the module that made the decision
// uses: the schema's, the store's, P1.4's or this module's. Nothing is
// swallowed and nothing is turned into a boolean the caller cannot question.
//
// **The offline gate** (P6.10, D167) is the second thing this file owns, and it
// is the one that says no. Its decision is `gate.js`'s -- a pure function of
// "is there a connection" and "what did the last poll see" -- and what this
// file does with it is three separate rules:
//
//   * `send`, `flush` and `poll` ask the **connection** gate first, before any
//     read, write, signature or speak. The SDK queues an offline write and
//     sends it on reconnect, so the write has to not be issued at all.
//   * `send` asks the **presence** gate too, because a sentence the owner just
//     spoke is the one thing D167 is about. `flush` and `poll` do not: presence
//     is what a poll *observes*, and gating a poll on its own observation would
//     be circular. Re-delivering an already-written message is not the hazard --
//     the consumer's ledger answers it as a duplicate, and P1.1's window
//     refuses it if it is too old.
//   * a gated utterance is kept as `unsent()` text and **not** in the outbox
//     queue: `flush` never sends it, because "delivered hours later" is exactly
//     what D167 rules out. The owner resends it, which is a new utterance with a
//     new id. A gated **action** is not kept at all -- an action record is never
//     queued, and resending one means confirming it again (P6.2).
//
// The presence the gate reads is the LAST poll's heartbeat, re-judged at the
// current time rather than the verdict it was given then: a heartbeat that was
// fresh at the last poll and is five minutes old now is `stale` now, which is
// the whole point of asking again. **And the connection is asked twice in one
// `send`** -- once before the id and the key, and once immediately before the
// write, because the Keychain in between is an await and a connection can go
// while it is up (the PR #663 review's M1: the first version asked only at the
// top, which is before the await and therefore not "at the write").
//
// **The port is optional and its default is the refusing one.** A client built
// without a `connection` gets `connection.js`'s `UNWIRED_CONNECTION`, which
// answers "no", so it refuses with `no-connection` and says so rather than
// typing out of a port that was never wired (the review's BLOCKER: the first
// version required the port and threw in seventy-seven of this app's own
// tests). Fail closed, in the constructor as in the gate.

import { validateAccount } from '../accounts/account.js';
import { SECRET_ITEMS } from '../accounts/secret.js';
import { SECURE_STORE_CODES } from '../accounts/secure-store.js';
import {
  TEXT_MAX,
  canonicalBytes,
  validateHeartbeat,
  validateMessage,
} from '../../../shared/mailbox-schema/index.js';
import {
  MAX_INTERVAL_MS,
  MIN_INTERVAL_MS,
  STATE,
  evaluatePresence,
} from '../../../shared/presence/index.js';
import { UNWIRED_CONNECTION } from './connection.js';
import { evaluateConnection, evaluateGate, offlineSpeech } from './gate.js';
import { macOf, verifyMac } from './mac.js';
import { isDatabaseKey, mailboxPaths } from './paths.js';
import { outboundRedactor } from './redact.js';
import { STORE_CODES } from './store.js';

/**
 * D118's mailbox TTL: how long a message the phone is still trying to deliver
 * stays worth retrying. It is the plan's number (O5, "mailbox TTL 72 h") and
 * the same one P1.1 is configured with as its longest horizon (a prompt's TTL).
 * A message still unanswered at the TTL is dropped and reported (`expired`),
 * never retried for ever.
 *
 * **It is not the window the consumer accepts these records for, and saying so
 * was wrong** (the PR #604 review). P1.1's window is per-kind (D173): a prompt
 * is accepted inside the prompt TTL, a command inside `windowMs` -- the 6-hour
 * replay window, its own default. Every record this client RETRIES carries no
 * `kind`, so the consumer reads it as a command, and a retry more than six
 * hours after the utterance is still made by the phone and refused at the other
 * end as a `replay`. (An action record does carry `kind: 'action'` -- P6.10,
 * D169 -- but `send` writes it once and forgets it, so it is never retried and
 * never meets this window on a second attempt; it is judged by the consumer's
 * action rules instead.) The phone cannot know better: `windowMs` is the consumer's
 * setting (P1.1 takes it per state) and the phone is not its reader. So the 72
 * h here bounds the PHONE's effort and matches the mailbox's retention -- the
 * facts of it, in the plan's O5 and D118 -- and `client.test.js` holds both
 * numbers to their own modules rather than asserting one window.
 */
export const MAILBOX_TTL_MS = 72 * 60 * 60 * 1_000;

/** D111's item name for the per-pairing key, checked against P3.1's list. */
export const MAC_KEY_ITEM = 'macKey';

/** The refusal codes this module adds to the schema's, the store's and P1.4's. */
export const CLIENT_CODES = Object.freeze({
  /** The utterance is not text, or is empty. */
  BAD_TEXT: 'bad-text',
  /** The utterance is longer than the schema's body bound. */
  TOO_LONG: 'too-long',
  /** The redactor refused the text, so nothing was written. */
  REDACT_FAILED: 'redact-failed',
  /** The clock is not a positive safe integer: nothing can be stamped. */
  BAD_CLOCK: 'bad-clock',
  /** The id source produced something that is not a database key. */
  BAD_ID: 'bad-id',
  /** The id source produced an id this client has already minted. */
  ID_CONFLICT: 'id-conflict',
  /** The account has no MAC key: it is not paired, and nothing is written. */
  NO_KEY: 'no-key',
  /** The key is there and could not be read: nothing is written or spoken. */
  KEY_UNREADABLE: 'key-unreadable',
  /**
   * The record cannot be signed at all: a field of it has no single canonical
   * byte string, so the two ends would compute two different tags for it. A
   * distinct answer from `key-unreadable`, because the key is not the problem
   * and the fix is not a re-pairing.
   */
  UNSIGNABLE: 'unsignable',
  /** An outbox record's `sender` is not this mailbox's uid (P2.1's template). */
  WRONG_SENDER: 'wrong-sender',
  /** An outbox message's or the heartbeat's tag did not verify. */
  FORGED: 'forged',
  /** A node held something that is not the record its key names. */
  MISMATCHED: 'mismatched-id',
});

/**
 * Why presence is not a verified state. When the heartbeat verified and P1.4
 * would judge it, `reason` is P1.4's own (`REASON`: fresh, missed-intervals,
 * long-silence, future-skew or no-heartbeat); in every other case it is one of
 * these, because none of them is one of P1.4's answers -- "we could not trust
 * the heartbeat" and "P1.4 would not read it" are not states P1.4 has.
 *
 * **P1.4's refusal codes never leave this module as a `reason`.** A refusal is
 * not a reason on either side of the boundary: P1.4's own README says a refusal
 * is never coerced into a state, and a caller of `poll` reads `reason` as one of
 * the two lists above (see `PRESENCE_REASON.REFUSED` for the case that made
 * this explicit).
 */
export const PRESENCE_REASON = Object.freeze({
  /** Nothing is stored at the heartbeat node. */
  NO_HEARTBEAT: 'no-heartbeat',
  /** The heartbeat node could not be read. */
  UNREADABLE: 'heartbeat-unreadable',
  /** The heartbeat is not a heartbeat: the schema refused its shape. */
  INVALID: 'heartbeat-invalid',
  /** The heartbeat's tag did not verify under this pairing's key. */
  UNSIGNED: 'heartbeat-unsigned',
  /**
   * The heartbeat is signed and schema-legal, and P1.4 refused it as its own
   * input -- `at: 0` is the case that reaches this: the schema's
   * `timestampField` refuses a *negative* timestamp and not zero, and P1.4
   * reads zero as the shape of an unset counter and refuses it
   * (`invalid-heartbeat-timestamp`). `verified` is still true (the tag is the
   * orchestrator's) and the state is `offline`: a heartbeat P1.4 will not judge
   * is not evidence that anything is running.
   */
  REFUSED: 'heartbeat-refused',
});

/** The expired retry's code, in the report's `expired` list. */
export const EXPIRED = 'expired';

/**
 * The direction inside every tag this client makes: the node it writes to
 * (P6.3's `DIRECTIONS`, and D113's one message path). It is named once here
 * because the pre-signing check below asks `canonicalBytes` with the same
 * direction the tag is made for.
 */
const WRITE_DIRECTION = 'inbox';

function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

function freezeList(items) {
  return Object.freeze(items);
}

/**
 * Does this value have ONE canonical byte string -- the question `macOf` is
 * about to ask, put before the key is touched?
 *
 * `canonicalBytes` is the authority and is **asked** rather than reimplemented:
 * it throws for a value the two languages would spell differently (U+2028 and
 * U+2029, which Go's encoder escapes and JavaScript's does not; an unpaired
 * surrogate) and for a value with no canonical form at all. A record carrying
 * one cannot be signed by anyone, at either end.
 *
 * It does **not** refuse a control character: `JSON.stringify` escapes those
 * the same way in both languages, so U+0007 signs and the schema is what
 * refuses it (`bad-character`). Two different questions, two different codes.
 */
function signable(value) {
  try {
    canonicalBytes(value, WRITE_DIRECTION);
    return true;
  } catch (error) {
    // `canonicalBytes` refuses a value by THROWING a TypeError, and that is the
    // only thing this catch may read as "cannot be signed": a ReferenceError or
    // a TypeError from anywhere else would be a bug in this module, and reading
    // it as a refusal would hide it behind a sentence about the owner's text.
    if (error instanceof TypeError) return false;
    throw error;
  }
}

/**
 * Which field of a record has no canonical byte string -- in practice `text`,
 * for a sentence holding U+2028 or an unpaired surrogate -- or `null` when
 * every field of it can be signed.
 *
 * The whole record is asked first and the fields are asked only to NAME the
 * culprit: a refusal that could not say which field it was about would send the
 * owner through their whole sentence looking for a character nothing displays.
 *
 * `text` is the only field a caller supplies: the id and the account's uid have
 * already been through `isDatabaseKey` (which refuses the same characters), and
 * `at` is a checked safe integer. The loop is still over the record rather than
 * over those three by name, so a field a later version adds is judged with the
 * rest instead of signing as itself.
 */
function unsignableField(record) {
  if (signable(record)) return null;
  for (const field of Object.keys(record)) {
    if (!signable({ [field]: record[field] })) return field;
  }
  // Unreachable while the record is a flat object of scalars built here, and
  // answered as "the text" rather than as `null` if it ever is reached: the
  // text is the only field a caller supplies.
  return 'text';
}

/**
 * P6.3's signed heartbeat as P1.4's rule reads it.
 *
 * **The one mapping between the two modules, named and tested.** P1.4's
 * `evaluatePresence` takes `{at, bootId}` and refuses any other field
 * (`heartbeat-unknown-field`); the schema's `validateHeartbeat` accepts
 * `{at, boot, agents?, mac}`, which is the shape P2.1's template already
 * gates. Neither module owns the other, no landed task writes the mapping, and
 * `shared/mailbox-schema/README.md` records the difference as a **conflict for
 * the owner** with this mapping named as one resolution. Until the owner rules,
 * this is where the phone crosses between them -- so that a signed heartbeat is
 * not read as `offline` because a field is spelled differently, which is the
 * failure P1.4's README warns about.
 *
 * Only `at` and `boot` cross: `agents` is a display list, and `mac` is the tag
 * the caller has already verified.
 *
 * @param {unknown} heartbeat a validator-accepted heartbeat
 * @returns {{ at: number, bootId: string }}
 * @throws {TypeError} for anything that is not an object with both fields
 */
export function toPresenceHeartbeat(heartbeat) {
  if (heartbeat === null || typeof heartbeat !== 'object' || Array.isArray(heartbeat)) {
    throw new TypeError('toPresenceHeartbeat: a heartbeat object is required');
  }
  // The FIELDS are checked too, and this is the `@throws` above taken
  // literally: mapping an object without them would hand P1.4 `{ at: undefined,
  // bootId: undefined }`, which is not a heartbeat and not a refusal either --
  // it would come back out of `evaluatePresence` as a state the caller cannot
  // tell from a real one. The TYPES are the schema's (`at` a timestamp, `boot`
  // an identity string); the VALUES are P1.4's to judge, which is why this
  // checks the shape and not the range.
  if (typeof heartbeat.at !== 'number' || typeof heartbeat.boot !== 'string') {
    throw new TypeError('toPresenceHeartbeat: a heartbeat\'s `at` and `boot` are required');
  }
  return { at: heartbeat.at, bootId: heartbeat.boot };
}

/**
 * Read one outbox subtree into a judgement per node: the schema's verdict, and
 * the order the phone speaks in (oldest first, then by id, so two passes over
 * one outbox do the same thing).
 *
 * A node whose key is not its record's `id` is reported rather than repaired:
 * P2.1's template holds the two equal (`newData.val() == $msg`), and a node
 * where they disagree is the shape a spliced or renamed write has.
 */
function readOutbox(value) {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return [];
  const entries = [];
  for (const [id, record] of Object.entries(value)) {
    if (record === null || typeof record !== 'object' || Array.isArray(record) || record.id !== id) {
      entries.push({
        id,
        record,
        valid: refuse(CLIENT_CODES.MISMATCHED, 'id'),
      });
      continue;
    }
    entries.push({ id, record, valid: validateMessage(record) });
  }
  entries.sort((a, b) => {
    const at = (a.valid.ok ? a.record.at : 0) - (b.valid.ok ? b.record.at : 0);
    if (at !== 0) return at;
    return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
  });
  return entries;
}

/**
 * Build the phone's mailbox client.
 *
 * Every argument is a port or a value source (see the header). A missing or
 * unusable one is a **wiring bug** and throws rather than being answered with a
 * refusal a caller could ignore: the app builds this once, at the point it has
 * an account and a pairing, and a client that could exist without a key store
 * is a client that would fail later, at the first utterance, with the owner
 * waiting.
 *
 * @param {object} options
 * @param {object} options.account D114's tuple (P3.1's `validateAccount` shape)
 * @param {object} options.keys a `SecretStore`: `use(accountId, item, fn)`
 * @param {object} options.store the database port: `write(path, value)`, `read(path)`
 * @param {object} [options.connection] the connection port (P6.10):
 *   `isOnline()`. **Optional, and the default refuses**: a client built without
 *   one uses `UNWIRED_CONNECTION` and answers `no-connection`, because the
 *   unsafe default is the one that sends into the SDK's queue
 * @param {object} options.speaker the audio port (P3.6's stub of P3.4): `speak(text)`
 * @param {() => number} options.now D117's server-anchored now, in ms
 * @param {() => string} options.newId a fresh message id per call
 * @param {string[]} options.secrets P1.3's secret list (D118)
 * @param {number} options.heartbeatIntervalMs D117's interval, in ms (P1.4's floor applies)
 * @param {number} [options.mailboxTtlMs] D118's TTL; defaults to `MAILBOX_TTL_MS`
 * @returns {object} the frozen client
 * @throws {TypeError} for a port, a value source, an account or a secret list
 *   that cannot be used -- and never for anything a later call could refuse
 */
export function createMailboxClient(options = {}) {
  if (options === null || typeof options !== 'object' || Array.isArray(options)) {
    throw new TypeError('createMailboxClient: an options object is required');
  }
  const account = validateAccount(options.account);
  if (!account.ok) {
    // The tuple is a value the owner typed, so its refusal is P3.1's, carried
    // as properties rather than through the message: the message must not echo
    // what was in the tuple.
    const error = new TypeError('createMailboxClient: the account tuple is not usable');
    error.code = account.code;
    error.field = account.field;
    throw error;
  }
  // P6.10's second port. It is OPTIONAL and its default is FAIL-CLOSED (the PR
  // #663 review's BLOCKER): a caller that has not wired one gets
  // `UNWIRED_CONNECTION`, which answers "no", so the refusal is `no-connection`
  // and the owner is told -- instead of a `TypeError` at construction, which is
  // what the first version did and which threw in seventy-seven of this app's
  // own tests, or an online default, which would send into the SDK's queue
  // exactly when nobody is looking. A connection that IS given must still be
  // usable: a half-wired port is a wiring bug and still throws here.
  const { keys, store, connection = UNWIRED_CONNECTION, speaker, now, newId, secrets } = options;
  if (keys === null || typeof keys !== 'object' || typeof keys.use !== 'function') {
    throw new TypeError('createMailboxClient: a secret store with use() is required');
  }
  if (store === null || typeof store !== 'object' || typeof store.write !== 'function'
    || typeof store.read !== 'function') {
    throw new TypeError('createMailboxClient: a store with write() and read() is required');
  }
  if (connection === null || typeof connection !== 'object' || typeof connection.isOnline !== 'function') {
    throw new TypeError('createMailboxClient: a connection must carry isOnline() when one is given');
  }
  if (speaker === null || typeof speaker !== 'object' || typeof speaker.speak !== 'function') {
    throw new TypeError('createMailboxClient: a speaker with speak() is required');
  }
  if (typeof now !== 'function') throw new TypeError('createMailboxClient: a clock is required');
  if (typeof newId !== 'function') throw new TypeError('createMailboxClient: an id source is required');
  if (!Array.isArray(secrets) || secrets.some((secret) => typeof secret !== 'string')) {
    throw new TypeError('createMailboxClient: the secrets must be a list of strings');
  }
  // The item name is D111's, and P3.1's `secret.js` owns the list. Asking the
  // list rather than trusting the constant is what keeps the two from drifting
  // apart silently: a rename there fails here, at the wiring, and not at the
  // first utterance, with the owner waiting.
  if (!SECRET_ITEMS.includes(MAC_KEY_ITEM)) {
    throw new TypeError(`createMailboxClient: ${MAC_KEY_ITEM} is not one of the store's items`);
  }
  const mailboxTtlMs = options.mailboxTtlMs === undefined ? MAILBOX_TTL_MS : options.mailboxTtlMs;
  if (!Number.isSafeInteger(mailboxTtlMs) || mailboxTtlMs <= 0) {
    throw new TypeError('createMailboxClient: mailboxTtlMs must be a positive integer');
  }
  const intervalMs = options.heartbeatIntervalMs;
  // D117's interval is the owner's setting, with P1.4's floor and ceiling. It
  // is judged here so that a bad one is a wiring failure and not a refusal that
  // surfaces from a poll -- the rule the missing ports get.
  if (!Number.isSafeInteger(intervalMs) || intervalMs < MIN_INTERVAL_MS || intervalMs > MAX_INTERVAL_MS) {
    throw new TypeError('createMailboxClient: heartbeatIntervalMs is outside D117\'s band');
  }

  const paths = mailboxPaths(account.account.authUid);
  const redact = outboundRedactor(secrets);
  const from = account.account.authUid;
  const accountId = account.account.id;

  /** Every id this client has minted, so an id source cannot reuse one. */
  const minted = new Set();
  /** The messages the outbox has not answered: id -> { record, landed }. */
  const outbound = new Map();
  /** The outbox ids this client has spoken, and the ones it has acked. */
  const spoken = new Set();
  const acked = new Set();
  /**
   * P6.10's two pieces of state, and both are deliberately small.
   *
   * `unsent` is the utterances the gate refused, as `{ text, at, code }` -- the
   * REDACTED text (D118: what is kept is what would have been written), never an
   * id and never a record, because an id would make it look like something the
   * outbox might still send. Nothing here is ever written or re-sent by this
   * client: D167's "only as unsent text the user can resend" is a rule about the
   * owner resending it, as a new utterance with a new id.
   *
   * `observation` is the last poll's look at the heartbeat -- the raw result of
   * the read, the validated heartbeat, and the tag verdict -- not the presence
   * object it produced: presence has to be judged at the time of the question,
   * and a verdict computed five minutes ago is a five-minute-old answer.
   */
  const unsent = [];
  let observation = null;

  /** The material is used inside the callback and never comes back out. */
  const withKey = (fn) => keys.use(accountId, MAC_KEY_ITEM, fn);

  // keyRefusal turns the store's answer about the key into this module's:
  // `absent` is "this phone is not paired", and everything else is "the key is
  // there and could not be used", which is a different thing to tell the owner.
  function keyRefusal(about) {
    const code = about.code === SECURE_STORE_CODES.ABSENT
      ? CLIENT_CODES.NO_KEY
      : CLIENT_CODES.KEY_UNREADABLE;
    return refuse(code, 'keys');
  }

  /** D117's now, or `null` if the injected clock is not a time. */
  function clock() {
    const value = now();
    return Number.isSafeInteger(value) && value > 0 ? value : null;
  }

  /**
   * What the consumer looked like at the last poll, re-judged NOW.
   *
   * `null` means no poll has run, and P6.10's gate reads that as its own refusal
   * (`consumer-unobserved`: nothing has looked, so nothing is known -- the
   * review's M2, and `gate.js` says why it fails closed). Everything else goes
   * back through `presenceOf` with the current time, so a heartbeat that
   * was fresh when it was read and has since gone quiet is `stale` or `offline`
   * at the moment the owner speaks -- which is the question, and not "was the
   * consumer up five minutes ago".
   */
  function consumerPresence(nowMs) {
    if (observation === null) return null;
    return presenceOf(observation.beat, observation.heartbeat, observation.verified, nowMs);
  }

  /**
   * The gate's decision, and the two things that follow from it when it says no.
   *
   * The owner is **told** -- through the one port this directory has for saying
   * anything, the speaker -- and the sentence is `gate.js`'s, so the voice screen
   * and this refusal cannot say two different things about one reason. A speak
   * that failed does not change the refusal: the code is what a screen switches
   * on, and `told` reports the truth about whether the owner heard it.
   *
   * `keepText` is false for an action record, and that is D167's "a destructive
   * action record is never queued at all": there is nothing to resend, because
   * resending a destructive command means confirming it again (P6.2).
   *
   * @returns {Promise<object>} the frozen refusal `send` returns
   */
  async function gateRefusal(verdict, { text, at, keepText }) {
    const message = offlineSpeech(verdict.code);
    const said = await speaker.speak(message);
    if (keepText) unsent.push(Object.freeze({ text, at, code: verdict.code }));
    return Object.freeze({
      ok: false,
      code: verdict.code,
      field: verdict.field,
      message,
      told: said.ok === true,
    });
  }

  /**
   * Send one utterance: **one** message, with a fresh id, redacted, signed,
   * written to this mailbox's inbox and nowhere else.
   *
   * The order is the rule. The text is redacted before it is signed (D118), the
   * record is asked whether it HAS a canonical byte string before the key is
   * touched (`unsignable` -- a character the two languages spell differently is
   * not a key problem), the tag is computed inside the key's callback (so no
   * code here ever holds the key), and the finished record is validated by the
   * schema before it is written -- so a record the rules or the consumer would
   * refuse is never attempted, and no unsigned record ever reaches the store.
   *
   * **The gate is here, before anything is minted or signed** (P6.10, D167):
   * the text is redacted first (so what is kept as unsent text and what is
   * signed are the same string, with no secret in either), then the clock is
   * read (the gate cannot judge a heartbeat's age without one), then the gate.
   * Nothing is written, no id is minted and the key is not touched.
   *
   * @param {unknown} text what the owner said
   * @param {{action?: object}} [options] pass `action` to send D169's action
   *   record: the message carries `kind: 'action'`, and it is **never queued**
   *   -- not in the outbox, not in `pending()`, not in `unsent()`
   * @returns {Promise<{ok: true, id: string, at: number}
   *   | {ok: false, code: string, field: string|null}>}
   */
  async function send(text, options) {
    if (typeof text !== 'string' || text === '') return refuse(CLIENT_CODES.BAD_TEXT, 'text');
    if (text.length > TEXT_MAX) return refuse(CLIENT_CODES.TOO_LONG, 'text');
    const body = redact(text);
    if (!body.ok) return body;
    const at = clock();
    if (at === null) return refuse(CLIENT_CODES.BAD_CLOCK, 'now');

    const opts = options === undefined || options === null ? {} : options;
    if (typeof opts !== 'object' || Array.isArray(opts)) {
      throw new TypeError('send: the options must be an object');
    }
    // An ACTION is a record carrying D169's action, and it is decided here
    // rather than after the schema has judged it: whether this record may be
    // queued is this file's rule, and it must not depend on how well-formed the
    // record turns out to be. What the action record must CONTAIN is P6.3's, and
    // the schema refuses a malformed one after the tag is made.
    const isAction = Object.hasOwn(opts, 'action');

    const verdict = evaluateGate({ online: connection.isOnline(), presence: consumerPresence(at) });
    if (!verdict.ok) {
      return gateRefusal(verdict, { text: body.text, at, keepText: !isAction });
    }

    const id = newId();
    if (!isDatabaseKey(id)) return refuse(CLIENT_CODES.BAD_ID, 'newId');

    // `mac` is outside the bytes it covers, so the tag is made over the record
    // without it and attached after: one definition of the signed bytes, the
    // schema's.
    const base = Object.freeze(isAction
      ? { id, sender: from, text: body.text, at, kind: 'action', action: opts.action }
      : { id, sender: from, text: body.text, at });
    // **Before the key is touched, and never as `key-unreadable`.** A field with
    // no single canonical byte string makes `canonicalBytes` throw, and inside
    // the key's callback that throw is indistinguishable from a Keychain that
    // would not open -- so the owner would be sent to re-pair over a character
    // in their own sentence. Nothing is signed, nothing is written, and no id is
    // recorded as used (which is why this is asked before `minted` is touched).
    const unsignable = unsignableField(base);
    if (unsignable !== null) return refuse(CLIENT_CODES.UNSIGNABLE, unsignable);

    // An id already minted is not reused: the rules refuse the second write
    // (`!data.exists()`), the consumer keeps the FIRST message, and this
    // utterance would be lost silently. That is the id source's bug, and it is
    // caught before anything is signed or written. The id is RECORDED as used
    // only at the write below, so a send that dies between here and there -- the
    // key, the schema, the gate -- leaves the id free rather than burning it.
    if (minted.has(id)) return refuse(CLIENT_CODES.ID_CONFLICT, 'newId');

    const tag = await withKey((key) => macOf(base, key, WRITE_DIRECTION));
    if (!tag.ok) return keyRefusal(tag);
    const record = Object.freeze({ ...base, mac: tag.result });
    const valid = validateMessage(record);
    if (!valid.ok) return refuse(valid.code, valid.field);

    // **The gate is asked AGAIN here, at the write** (the PR #663 review's M1).
    // The check at the top of `send` is what keeps the id, the tag and the store
    // call from happening at all, and it is not the last word: the `withKey`
    // await above is a Keychain, which on a device is a prompt and can take
    // seconds, and a connection can go while it is up. A write handed over after
    // that is a write the SDK HOLDS and sends on reconnect -- the one thing D167
    // rules out -- so the gate is asked once more where it decides anything:
    // immediately before the write. Presence is re-judged too, at the write-time
    // clock (`at` when the clock has stopped being a time, because the record's
    // own stamp is already fixed and a broken clock is not this check's news).
    const atWrite = clock();
    const still = evaluateGate({
      online: connection.isOnline(),
      presence: consumerPresence(atWrite === null ? at : atWrite),
    });
    if (!still.ok) {
      return gateRefusal(still, { text: body.text, at, keepText: !isAction });
    }
    minted.add(id);

    const written = await store.write(paths.inboxMessage(id), record);
    // `exists` is a landed write: the template denies a re-write of a node that
    // is there, so a retry after a lost acknowledgement -- the commonest
    // reconnect case -- is denied rather than accepted, and the message the
    // consumer will read IS this one.
    if (written.ok || written.code === STORE_CODES.EXISTS) {
      // **An action is written and then forgotten**, never remembered for a
      // retry: D167's "a destructive action record is never queued at all". It
      // is not in `outbound`, so `flush` cannot re-send it, `pending()` cannot
      // offer it and nothing here will ever write it a second time.
      if (!isAction) outbound.set(id, { record, landed: true });
      return Object.freeze({ ok: true, id, at });
    }
    // It did not land. The record is kept VERBATIM for `flush` -- never
    // re-minted, never re-stamped (P1.1's caller duty: "never retry an id with
    // a new `at`") -- and the refusal carries the code the screen switches on.
    // It is in `pending()`, NOT in `unsent()`: the gate accepted this one, and
    // the client re-delivers it itself (see `pending()` above).
    // An ACTION is the exception, and the exception is the rule above: a write
    // that did not land is reported and dropped, and the owner confirms again.
    if (!isAction) outbound.set(id, { record, landed: false });
    return refuse(written.code, written.field);
  }

  // flushMutating re-writes every message the outbox has not answered, oldest
  // first, with the SAME record -- id and `at` included. It is the reconnect
  // half of the client, and it mutates `outbound`, which is why `flush` and
  // `poll` share it rather than calling each other.
  async function flushMutating(nowMs) {
    const written = [];
    const failed = [];
    const expired = [];
    const outstanding = [];
    for (const [id, entry] of [...outbound]) {
      // D118's TTL: past it the consumer's own window has certainly closed --
      // whatever the kind, and for a command (what this client writes) well
      // before it -- so the phone stops trying, and says so rather than
      // dropping the utterance silently.
      if (nowMs - entry.record.at > mailboxTtlMs) {
        outbound.delete(id);
        expired.push(Object.freeze({ id, code: EXPIRED }));
        continue;
      }
      const again = await store.write(paths.inboxMessage(id), entry.record);
      if (again.ok || again.code === STORE_CODES.EXISTS) {
        entry.landed = true;
        written.push(id);
      } else {
        failed.push(Object.freeze({ id, code: again.code }));
      }
      outstanding.push(id);
    }
    return Object.freeze({
      written: freezeList(written),
      failed: freezeList(failed),
      expired: freezeList(expired),
      outstanding: freezeList(outstanding),
    });
  }

  /**
   * Re-deliver what the outbox has not answered.
   *
   * The whole record is re-sent, byte for byte, so the consumer's ledger
   * answers the second arrival as a **duplicate** of the first and does not act
   * on it twice (P1.1) -- and an early `flush` costs nothing: a message that is
   * already there is denied by the rules and read here as landed.
   *
   * **With no connection there is no flush** (P6.10): every re-send is a write
   * the SDK would hold and fire on reconnect, which is the queue D167 rules out
   * -- not only for a new sentence but for one already in the outbox. The
   * records stay in `outbound`, so the next flush with a connection re-delivers
   * them unchanged.
   *
   * @returns {Promise<object>} the flush report, or a refusal for a bad clock
   */
  async function flush() {
    const nowMs = clock();
    if (nowMs === null) return refuse(CLIENT_CODES.BAD_CLOCK, 'now');
    // **The connection half only** (`evaluateConnection`, not `evaluateGate`):
    // a re-send has no observation question to ask, and asking it would refuse
    // every flush on a phone that had not polled -- the circle M2's fail-closed
    // default would otherwise close.
    const verdict = evaluateConnection(connection.isOnline());
    if (!verdict.ok) return refuse(verdict.code, verdict.field);
    return Object.freeze({ ok: true, ...(await flushMutating(nowMs)) });
  }

  /**
   * One pass over everything the phone can see: re-deliver what is unanswered,
   * read its own outbox and the heartbeat, verify both, speak what the outbox
   * holds, ack what was spoken, and answer whether the orchestrator is live.
   *
   * **The reads are exactly two, and they are this method's.** An unreadable
   * outbox is a refusal (nothing is spoken). An unreadable heartbeat is not:
   * the replies half still happens, and presence is `offline` with
   * `heartbeat-unreadable` -- never `live`, because a heartbeat nobody could
   * read is not evidence that anything is running.
   *
   * @returns {Promise<object>} the poll report (see `README.md`), or a refusal
   */
  async function poll() {
    const nowMs = clock();
    if (nowMs === null) return refuse(CLIENT_CODES.BAD_CLOCK, 'now');
    // **No connection, no pass at all** (P6.10): the re-sends would go into the
    // SDK's queue, and so would every ack -- the phone would ack a message it
    // read out of the SDK's own cache and has not heard the consumer write. The
    // gate's presence half is deliberately NOT asked here: presence is what this
    // pass OBSERVES, and refusing to look until the answer is good is a circle.
    // The connection half is the one that has to hold, and it is the SDK's own
    // flag.
    const online = evaluateConnection(connection.isOnline());
    if (!online.ok) return refuse(online.code, online.field);
    const flushed = await flushMutating(nowMs);

    const out = await store.read(paths.outbox);
    if (!out.ok) return refuse(out.code, 'outbox');
    const listed = readOutbox(out.value);

    const beat = await store.read(paths.heartbeat);
    const heartbeat = beat.ok ? validateHeartbeat(beat.value) : null;

    // One read of the key per poll, and the material stays inside the callback:
    // what comes back is a verdict per outbox message and one for the heartbeat.
    const verdicts = await withKey((key) => Object.freeze({
      outbox: freezeList(listed.map((entry) => verifyMac(entry.record, key, 'outbox'))),
      heartbeat: heartbeat !== null && heartbeat.ok
        ? verifyMac(heartbeat.heartbeat, key, 'heartbeat')
        : false,
    }));
    if (!verdicts.ok) return keyRefusal(verdicts);

    // P6.10: the one thing this pass leaves behind. It is the RAW look -- the
    // read, the validated heartbeat and the tag verdict -- and not the presence
    // it produced, because the gate has to judge it again at the moment the owner
    // speaks. A poll that read an unsigned heartbeat therefore leaves an
    // observation the gate reads as `offline`, with `verified: false`, exactly
    // as this pass reported it.
    observation = Object.freeze({ beat, heartbeat, verified: verdicts.result.heartbeat });

    const dropped = [];
    const speakable = [];
    listed.forEach((entry, index) => {
      if (!entry.valid.ok) {
        dropped.push(Object.freeze({ id: entry.id, code: entry.valid.code, field: entry.valid.field }));
        return;
      }
      // P2.1's template holds both families to `sender == $mb`; the phone checks
      // it too rather than trusting the rules to have done it.
      if (entry.record.sender !== from) {
        dropped.push(Object.freeze({ id: entry.id, code: CLIENT_CODES.WRONG_SENDER, field: 'sender' }));
        return;
      }
      if (verdicts.result.outbox[index] !== true) {
        dropped.push(Object.freeze({ id: entry.id, code: CLIENT_CODES.FORGED, field: 'mac' }));
        return;
      }
      // A verified reply answers the message it names (`reply_to`, which the
      // schema requires of an `answer` and allows on an `action`), so the phone
      // stops re-delivering it. Only a VERIFIED reply does that: an unverified
      // one must not be a forger's way of making the phone give up.
      if (typeof entry.record.reply_to === 'string') outbound.delete(entry.record.reply_to);
      // `acked` is this phone's own write, in a previous run or before a
      // restart: a message it has already spoken is not spoken again.
      if (entry.record.acked === true) return;
      speakable.push(entry);
    });

    const unspoken = [];
    const spokenNow = [];
    const ackedNow = [];
    const ackFailed = [];
    for (const { id, record } of speakable) {
      if (!spoken.has(id)) {
        const said = await speaker.speak(record.text);
        if (!said.ok) {
          // NOT spoken, so NOT acked: the message stays unacked in the store
          // and the next pass tries again.
          unspoken.push(Object.freeze({ id, code: said.code }));
          continue;
        }
        spoken.add(id);
        spokenNow.push(id);
      }
      if (!acked.has(id)) {
        const ack = await store.write(paths.acked(id), true);
        if (ack.ok || ack.code === STORE_CODES.EXISTS) {
          acked.add(id);
          ackedNow.push(id);
        } else {
          ackFailed.push(Object.freeze({ id, code: ack.code }));
        }
      }
    }

    return Object.freeze({
      ok: true,
      written: flushed.written,
      failed: flushed.failed,
      expired: flushed.expired,
      outstanding: flushed.outstanding,
      spoken: freezeList(spokenNow),
      acked: freezeList(ackedNow),
      ackFailed: freezeList(ackFailed),
      unspoken: freezeList(unspoken),
      dropped: freezeList(dropped),
      presence: presenceOf(beat, heartbeat, verdicts.result.heartbeat, nowMs),
    });
  }

  // presenceOf is the heartbeat's half of a poll. A heartbeat that could not be
  // read, could not be validated or did not verify is `offline` and carries no
  // agent list -- the plan's "an unsigned heartbeat is treated as offline", and
  // the reason the tag covers `agents`: a heartbeat nobody signed must not be
  // able to announce itself as the agent the owner talks to.
  function presenceOf(beat, heartbeat, verified, nowMs) {
    if (!beat.ok) {
      return Object.freeze({
        state: STATE.OFFLINE,
        verified: false,
        reason: PRESENCE_REASON.UNREADABLE,
        agents: null,
        ageMs: null,
      });
    }
    if (heartbeat === null || !heartbeat.ok) {
      const empty = beat.value === null || beat.value === undefined;
      return Object.freeze({
        state: STATE.OFFLINE,
        verified: false,
        reason: empty ? PRESENCE_REASON.NO_HEARTBEAT : PRESENCE_REASON.INVALID,
        agents: null,
        ageMs: null,
      });
    }
    if (verified !== true) {
      return Object.freeze({
        state: STATE.OFFLINE,
        verified: false,
        reason: PRESENCE_REASON.UNSIGNED,
        agents: null,
        ageMs: null,
      });
    }
    const state = evaluatePresence({
      heartbeat: toPresenceHeartbeat(heartbeat.heartbeat),
      now: nowMs,
      intervalMs,
    });
    if (!state.ok) {
      // REACHABLE, and not only in theory: `at: 0` is schema-legal and P1.4
      // refuses it as an unset counter, so a heartbeat that passed every check
      // this module makes can still be something P1.4 will not read. The state
      // is `offline` and never `live` -- fail closed -- and the reason is this
      // module's own code, NOT `state.code`: P1.4's refusal codes are not
      // reasons, and a caller that switches on `reason` over the two lists
      // `PRESENCE_REASON` and P1.4's `REASON` would otherwise meet a string
      // neither of them contains.
      return Object.freeze({
        state: STATE.OFFLINE,
        verified: true,
        reason: PRESENCE_REASON.REFUSED,
        agents: null,
        ageMs: null,
      });
    }
    return Object.freeze({
      state: state.state,
      verified: true,
      reason: state.reason,
      ageMs: state.ageMs,
      agents: heartbeat.heartbeat.agents ?? null,
    });
  }

  /**
   * What the outbox has not answered: the utterances still being delivered and
   * the ones already there whose reply has not arrived. `landed: false` is a
   * record the phone HOLDS and is still retrying; `landed: true` is in flight.
   * The text is the REDACTED text, because that is what will be written.
   *
   * **This is not D167's "unsent text the user can resend" -- `unsent()` is.**
   * The two lists are different things and the difference is the gate: what is
   * here was ACCEPTED for delivery (the connection was up when it was made) and
   * this client keeps re-delivering it on its own until the outbox answers or
   * D118's TTL passes; what is in `unsent()` was REFUSED at the gate, is never
   * written by this client at all, and waits for the owner to say it again. A
   * screen resending from here would be a second write of a record already in
   * flight.
   *
   * @returns {ReadonlyArray<{id: string, at: number, text: string, landed: boolean}>}
   */
  function pending() {
    return freezeList([...outbound].map(([id, entry]) => Object.freeze({
      id,
      at: entry.record.at,
      text: entry.record.text,
      landed: entry.landed,
    })));
  }

  /**
   * D167's "unsent text the user can resend": the utterances the offline gate
   * refused, oldest first, as `{ text, at, code }`. The text is the REDACTED
   * text; the code is `gate.js`'s, so a screen can say the same thing the phone
   * said out loud.
   *
   * **Nothing here is ever sent by this client**, and that is the difference
   * between this list and `pending()`. `pending()` is what the outbox has not
   * answered and `flush` will re-deliver; this is what was never written at all,
   * and D167 keeps it that way on purpose -- a command delivered on reconnect is
   * a command that runs at a time nobody chose. The owner resends it by speaking
   * again, which is a new utterance with a new id; a destructive action is not
   * here at all, because resending one means confirming it again (P6.2).
   *
   * @returns {ReadonlyArray<{text: string, at: number, code: string}>}
   */
  function unsentText() {
    return freezeList([...unsent]);
  }

  // serial runs one mutation at a time, in call order, and is the whole of the
  // client's concurrency control. It is here rather than inside a method
  // because the thing it protects is a PROPERTY OF THE PAIR: two `poll`s in
  // flight together both read an outbox message whose ack has not landed, both
  // speak it, and the owner hears the sentence twice -- which is exactly what
  // the "write the ack only after speaking" order exists to prevent. A screen
  // that polls on a timer and again on reconnect does that without meaning to.
  // `send` is in the chain for the same class of reason: its `minted` set, its
  // `outbound` entry and the TTL flush all assume one pass at a time.
  //
  // The chain never carries a rejection of its own: the caller gets the call's
  // own outcome, and the next caller gets a settled queue whether or not the
  // last one refused.
  let queue = Promise.resolve();
  function serial(fn) {
    const run = queue.then(() => fn(), () => fn());
    queue = run.then(() => undefined, () => undefined);
    return run;
  }

  return Object.freeze({
    ok: true,
    paths,
    send: (text, options) => serial(() => send(text, options)),
    flush: () => serial(() => flush()),
    poll: () => serial(() => poll()),
    pending,
    unsent: unsentText,
  });
}
