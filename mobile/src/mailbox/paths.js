// The phone's two write paths and its two read paths (mobile plan, Task P3.6;
// D113, and P2.1's template).
//
// P3.6 is "the only place in the app that writes to the database", and its
// write set is exactly two paths: the message node of its own inbox
// (`mailbox/$mb/inbox/$msg`) and the `acked` child of one outbox message it has
// already spoken (`mailbox/$mb/outbox/$msg/acked`). Its reads are its own
// outbox and the heartbeat. Those five strings are what the whole task is
// about, so they are composed in one file, from one id, by functions that
// refuse an id they cannot compose a path from.
//
// **Why the composition refuses rather than escapes.** The mailbox id is the
// installation's Firebase Auth `uid` -- the rules' macro is `$mb == auth.uid`
// -- and a message id is minted by the client. A path is built by joining
// them, and an id that carries a separator, a dot or a `%` would address a
// node outside the mailbox: `'../config/kill'` is a path the phone must never
// name, whatever the rules would do with it. So `isDatabaseKey` is a port of
// the tree's own key rule (the schema's `keyField`, which ports
// `internal/budget`'s `Key`) and the composers throw for an id that fails it.
// The rule is deliberately the same rule as the schema's, and
// `paths.test.js` asserts the two agree on a list of candidates: a message id
// that the schema accepts and this module refuses (or the reverse) would be a
// message that could be signed and never written.
//
// **The throw carries no value.** A hostile id is exactly the value that must
// not be echoed into a message, a log or a crash report, so the TypeError names
// the field and not what was in it. That is the same rule P1.1's refusals keep
// for a hostile field name.
//
// **The paths are P2.1's template's, and the template is not this module's to
// change.** `internal/budget/rules/rules.json.tmpl` already carries the
// subtree: `mailbox/$mb/inbox/$msg` writable by `MAILBOX && newData.exists()
// && !data.exists()`, `mailbox/$mb/outbox` readable by `MAILBOX` with an
// `acked` child writable by `MAILBOX`, and `mailbox/$mb/heartbeat` readable by
// `MAILBOX` and writable by `CONSUMER`. The phone writes none of the last one:
// the heartbeat is the orchestrator's (D113b).

import { ID_MAX } from '../../../shared/mailbox-schema/index.js';

/** The subtree the rules' template puts every account's mailbox under. */
export const MAILBOX_ROOT = 'mailbox';

/**
 * The characters a Realtime Database key may not contain: `%` (the escape
 * `Unkey` parses), the six the database itself rejects in a key (`.` and the
 * path and index characters `$ # [ ] /`), and every control character
 * including DEL -- checked by code point rather than by membership in a set, so
 * a character nobody typed correctly is refused too.
 */
const FORBIDDEN_KEY_CHARS = new Set(['%', '.', '$', '#', '[', ']', '/']);

/**
 * Is this string usable as a Realtime Database key -- and therefore as a
 * mailbox id or a message id?
 *
 * It is the schema's rule (`keyField`), written a second time because this
 * module runs before anything is validated: the path is composed from an id
 * that has not been through `validateMessage` yet, and a composition that
 * cannot be checked is a composition that can be made to point outside the
 * mailbox. `paths.test.js` asserts the two rules agree.
 *
 * @param {unknown} value
 * @returns {boolean}
 */
export function isDatabaseKey(value) {
  if (typeof value !== 'string') return false;
  if (value === '') return false;
  if (value.length > ID_MAX) return false;
  for (let i = 0; i < value.length; i++) {
    const code = value.charCodeAt(i);
    if (FORBIDDEN_KEY_CHARS.has(value[i]) || code < 0x20 || code === 0x7f) return false;
    if (code === 0x2028 || code === 0x2029) return false;
    // A lone surrogate is not a character: JSON cannot encode it, so a key
    // carrying one is a key the database cannot hold.
    if (code >= 0xd800 && code <= 0xdbff) {
      const next = value.charCodeAt(i + 1);
      if (!(next >= 0xdc00 && next <= 0xdfff)) return false;
      i++;
      continue;
    }
    if (code >= 0xdc00 && code <= 0xdfff) return false;
  }
  return true;
}

/**
 * Every path the phone may name inside one account's mailbox.
 *
 * The mailbox id is the account's Firebase Auth `uid` (the rules' `$mb ==
 * auth.uid`), which is **not** the app's local `accountId` (D114's tuple names
 * the installation; the uid addresses the mailbox). Both are needed by the
 * client, and this module takes the one the database knows.
 *
 * @param {unknown} mailboxId the account's Auth uid
 * @returns {object} frozen paths, and the composers that keep an id inside them
 * @throws {TypeError} for an id that is not a database key -- the refusal
 *   names the field and never the value
 */
export function mailboxPaths(mailboxId) {
  if (!isDatabaseKey(mailboxId)) {
    throw new TypeError('mailboxPaths: the mailbox id must be a database key');
  }
  const mailbox = `${MAILBOX_ROOT}/${mailboxId}`;

  /** Compose one message's path, refusing an id that would leave the mailbox. */
  const node = (family, id) => {
    if (!isDatabaseKey(id)) {
      throw new TypeError(`mailboxPaths: a message id must be a database key (${family})`);
    }
    return `${mailbox}/${family}/${id}`;
  };

  return Object.freeze({
    /** `mailbox/<uid>`: this account's mailbox, and the whole of what it may name. */
    mailbox,
    /** `mailbox/<uid>/inbox`: the phone WRITES here, one message per utterance (D113). */
    inbox: `${mailbox}/inbox`,
    /** `mailbox/<uid>/outbox`: the phone READS here -- the consumer's replies. */
    outbox: `${mailbox}/outbox`,
    /** `mailbox/<uid>/heartbeat`: the phone READS here -- the orchestrator writes it (D113b). */
    heartbeat: `${mailbox}/heartbeat`,
    /** The message node one utterance is written to: `mailbox/<uid>/inbox/<id>`. */
    inboxMessage: (id) => node('inbox', id),
    /** The node an outbox message lives at (the ack's parent). */
    outboxMessage: (id) => node('outbox', id),
    /**
     * The ack's own node: `mailbox/<uid>/outbox/<id>/acked`. The plan's second,
     * explicitly-allowed phone write (D113) -- one boolean, written after the
     * message was spoken, and nowhere else.
     */
    acked: (id) => `${node('outbox', id)}/acked`,
  });
}
