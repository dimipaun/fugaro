// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The app's receipt: what a push it did not expect may do, which is one alert
// and nothing else (mobile plan, Task P5.2.9; D111, D113, D160, D169; the
// owner's Q6 ruling of 2026-11-21; the design is
// docs/mobile/design/push-notifications.md §3.1, §3.2, §3.9, §4 rules 1-3, 8,
// 10, 11 and 12).
//
// **A push is a doorbell, and this file is where that is true rather than
// said.** The OS shows a notification's own title and body in the author's
// words, before any Fugaro code runs, and it cannot tell the owner's sender
// from anyone holding the same send credential (§3.9, §5 attacker 2). So a
// forged push can lie on the lock screen, and what this module bounds is the
// consequence: **no action, no route, no write, and no payload text rendered
// anywhere** -- §4 rules 3, 8 and 12, which the design calls the whole claim
// ("a push cannot make the app act").
//
// **What a payload may contribute, exactly one thing.** Q6's answer is
// *category only*: the payload carries the category and **no opaque id**
// (the owner's ruling of 2026-11-21), so "ignore an unknown id" protects
// nothing and §4 rule 11 is the rule that does the work -- **an alert is posted
// only when the mailbox shows a new, MAC-verified change of that category since
// the last one the app saw.** `receive` therefore reads `data.category` and
// **nothing else**: not the title, not the body, not `data.url`, not
// `click_action`, not any other key, and the value it reads must be one of the
// five names in the closed set below or no alert is posted at all. The field
// name is not this module's invention: `internal/cli/push.go` (P5.2.6) sends
// `{"message":{"token":…,"data":{"category":…},"android":{"priority":"high"}}}`
// -- data-only, no title and no body -- so `CATEGORY_FIELD` names the one key
// the two ends share.
//
// **The resolution crosses a port, and the reason is a fence rather than a
// convenience.** A tag is verified with D171's per-pairing key over P6.3's
// canonical bytes, and both are deliberately off the mailbox's door
// (`src/mailbox/index.js`; its test asserts `macOf`, `verifyMac` and
// `canonicalBytes` are not on it) -- "a screen that could compute a tag is a
// screen that could put a key in a variable". So a module in THIS directory
// cannot verify a tag even if it wanted to, and what crosses the boundary is
// the client's ANSWER and never a message, a store or a key: `mailbox` is a
// port with one method, `newChange(category)`, and its contract is the design's
// own sentence -- whether the app's own mailbox, read with the tag verified,
// holds a change of that category the app has not seen. **That contract is not
// built yet, and this module does not pretend otherwise**: the mailbox client's
// only MAC-verifying read is `poll()`, which speaks and acks and therefore
// WRITES (§3.9 rule 12 forbids exactly that on receipt), so a read-only
// verified read is a change to `src/mailbox/` that this task's files do not
// include. Until a task builds it, the port's real adapter is the app's wiring,
// and what is pinned here is the half that must never change: the app posts an
// alert only on a `fresh` answer, and a port that refuses, throws or answers
// anything else produces no alert.
//
// **The sentence, and the half of it that is not here.** Q6 left one thing to
// this task -- whether the sentence travels in the payload or the phone resolves
// it from a key in its own bundle, "depends on what the relay passes through
// (not verified)" -- and the answer is not one answer. **Android's half is the
// phone's**: `ALERT_PHRASES` is a table in this file, the app composes the alert
// after the verified read, and no text the app shows ever comes from a message.
// **iOS's half cannot be**, and this module does not claim it: the system draws
// the visible alert *before* any Fugaro code runs, so the sentence there can only
// be display text the **sender** puts in the payload -- the sentence itself, or
// the localization key the phone's own bundle resolves at display time (§4 rule 1
// binds "the sender's fallback title" as one of the places text may live, and
// APNs' `loc-key` is the key-shaped spelling of that field; neither needs a
// Notification Service Extension, which is why Q6's answer holds). **That half is
// built nowhere yet**: `internal/cli/push.go` (P5.2.6) sends `data` with the
// category and no title and no body, so there is no visible iOS alert today, and
// whether the publisher's relay passes one through is unverified. What this
// module fixes is the **vocabulary the two ends share** -- the five category
// names below, one fixed sentence each -- so that a sentence a sender writes and
// a sentence the app writes cannot drift into two. And the app never reads one
// either way: `receive` takes the category and nothing else, so display text in a
// payload is never state, never a route and never a link, on either platform.
//
// **The iOS exception is stated here rather than assumed.** On iOS an alert
// appears **before any MAC is verified** -- §4 rule 11's own exception, the price
// the owner accepted for no NSE and no D111 amendment. What that costs is the
// rule's subject, and not this module's: on iOS rule 11 still holds for **state**
// (what the app's own screen shows is built from nothing but the MAC-verified
// mailbox), and what the receipt answers on iOS is exactly what it answers on
// Android -- nothing to do, nothing to open, nothing to write, which is why
// `receive` is the same function on both platforms and takes no platform at all.
// The sentence the system shows there is rule 10's closed enumeration all the
// same, and it is no route and no input (rules 3 and 8) -- but it is the
// sender's to write, and not this file's.
//
// **What is deliberately NOT here.** No Notification Service Extension: Q6's
// answer is a visible iOS alert with **no NSE and no keychain access group**,
// so **D111 is not amended** and there is no iOS file to write on this half
// either -- the sentence the system shows on iOS is payload display text the
// sender writes, and what this module holds is the vocabulary it must be written
// from. `ALERT_PHRASES` is the app's own table: the alert Android composes, and
// the words the app's own screens show.
// No tap handler and no route either: a tap is the OS opening the app, the app
// signs in and renders its own screen, and there is no target for a payload to
// choose -- so `src/app/root.js` is untouched by this task, because a tap has
// nowhere to land that the app was not already going to show. And no write of
// any kind: this module holds one port with one read method, has no store, no
// key, no connection and no clock, and its test scans its own source for the
// write verbs the mailbox client owns.
//
// **The phrases are a closed set, and rule 10 is why.** §4 rule 1 binds the
// app's own words as tightly as the payload: no repository, no run, no branch,
// no amount, no currency, no error text -- "a fact from a document and never a
// string from a hostile field" (console v2's D33, quoted by the design). So the
// text is one fixed sentence per category, drawn from the table below and from
// nothing else, and the notification is posted **private** (Android's
// `VISIBILITY_PRIVATE`) with a redacted public line, so a visible notification
// is a category and never a sentence about the owner's work.
//
// **The channel's description carries the one habit that works.** The only
// control against a forged push is the owner opening the app rather than acting
// on the banner (§3.9), so the Android channel's own description says so, and
// it is written once, here.

import { CONTROL_NAMES } from './controls.js';

/**
 * The category vocabulary, as the schema's own list. `controls.js` reads the
 * same names for §3.8's switches -- the message, the node and the payload spell
 * one vocabulary, so a switch the owner flips and a category the sender sends
 * cannot drift into two (`internal/cli/push.go`'s own reason for reading them
 * from the registration message).
 */
export const ALERT_CATEGORIES = CONTROL_NAMES;

/** The one key of the payload this app reads. `internal/cli/push.go` writes it. */
export const CATEGORY_FIELD = 'category';

/**
 * One fixed sentence per category -- §4 rule 10's closed enumeration. Each
 * names the app and nothing about the owner's work: no repository, no run, no
 * branch, no amount and no currency. A category this table is missing gets **no
 * alert** rather than a sentence nobody wrote (`receive` answers
 * `unknown-category`), and the test pins the whole table so a sixth category
 * arrives as a missing name rather than as a blank notification.
 *
 * It is also the **shared vocabulary**: iOS's visible sentence is written by the
 * sender into the payload (the header), so the same five names and the same five
 * sentences have to be what a sender writes, and this list is where a change to
 * one of them is made.
 */
export const ALERT_PHRASES = Object.freeze({
  kill_switch: 'Fugaro: a kill switch is on.',
  cap_reached: 'Fugaro: a spending cap has been reached.',
  consumer_offline: 'Fugaro: the orchestrator has gone offline.',
  cap_near: 'Fugaro: a spending cap is nearly reached.',
  prompt: 'Fugaro: a question is waiting for your answer.',
});

/**
 * The redacted version: what a secure lock screen shows for a private
 * notification, and what the app says when it has nothing category-shaped to
 * say. One sentence for every category, because the point of a public version
 * is that it discloses less.
 */
export const ALERT_PUBLIC = 'Fugaro: open the app to see what changed.';

/** Android's `VISIBILITY_PRIVATE`: the public line above, not the phrase, on a secure lock screen. */
export const ALERT_VISIBILITY = 'private';

/**
 * The Android notification channel, as one descriptor: the wiring creates it
 * with the platform's own API, and the description is §3.9's habit rather than
 * decoration -- the channel's own text is one of the places the app can say
 * that a push is only a doorbell.
 */
export const PUSH_CHANNEL = Object.freeze({
  id: 'fugaro-doorbell',
  name: 'Fugaro',
  description: 'Fugaro notifications only ring the doorbell. Open the app to see what changed.',
});

/** Why the app did what it did, in the words a test and a screen can read. */
export const RECEIPT_CODES = Object.freeze({
  /** The alert was posted: a new, MAC-verified change of the category was resolved. */
  POSTED: 'posted',
  /** The mailbox holds no new verified change of that category, so nothing is posted (§4 rule 11). */
  NOT_RESOLVED: 'not-resolved',
  /** The payload named no category, or one this app does not have: no alert at all. */
  UNKNOWN_CATEGORY: 'unknown-category',
  /** The message is not one this app can read a category out of: no alert at all. */
  BAD_PAYLOAD: 'bad-payload',
  /** The mailbox read refused or threw. Nothing is posted and nothing is claimed. */
  MAILBOX_FAILED: 'mailbox-failed',
});

function isPlainObject(value) {
  return value !== null && typeof value === 'object' && Object.getPrototypeOf(value) === Object.prototype;
}

/**
 * The category a message names, or `null`.
 *
 * The ONE field this app reads out of a push, and every way of not being it
 * answers `null`: a message that is not an object, a `data` that is not a plain
 * object, a `category` that is not a string, and a string that is not one of
 * the five names. Nothing else in the message is touched -- which is what makes
 * "a lure in the title" unable to be rendered as state, as a link or as a
 * route: the title is never read, so there is nothing to render.
 */
function categoryOf(message) {
  if (!isPlainObject(message)) return null;
  const data = message.data;
  if (!isPlainObject(data)) return null;
  const category = data[CATEGORY_FIELD];
  if (typeof category !== 'string') return null;
  return ALERT_CATEGORIES.includes(category) ? category : null;
}

/** The decision shape, so that every answer carries the same names. */
function answer({ ok, post, code, category = null, alert = null }) {
  return Object.freeze({ ok, post, code, category, alert });
}

/** The alert one category is posted as: the app's own words, and nothing from the push. */
function alertFor(category) {
  return Object.freeze({
    category,
    text: ALERT_PHRASES[category],
    public: ALERT_PUBLIC,
    visibility: ALERT_VISIBILITY,
    channel: PUSH_CHANNEL.id,
  });
}

/**
 * The receipt: one call per push the platform hands the app, answering what the
 * app may do about it.
 *
 * @param {object} options
 * @param {object} options.mailbox the app's own mailbox, read-only and
 *   MAC-verified: `newChange(category)` answers `{ok: true, fresh: boolean}` --
 *   whether the mailbox holds a new, verified change of that category -- or a
 *   refusal `{ok: false, code, field}`. It is NOT the mailbox client, whose
 *   only verified read writes (the header)
 * @returns {object} the frozen door
 * @throws {TypeError} for a port that cannot be used, which is a wiring bug: a
 *   receipt that could exist without a mailbox read is one that would answer
 *   "no alert" for a reason nobody chose, at the one moment a push arrived.
 */
export function createPushReceipt({ mailbox } = {}) {
  if (mailbox === null || typeof mailbox !== 'object' || typeof mailbox.newChange !== 'function') {
    throw new TypeError('createPushReceipt: a mailbox read with newChange() is required');
  }

  /**
   * One push, received. **Reads the category, asks the mailbox, and answers a
   * decision** -- no write, no route, no store, and no text out of the message.
   *
   * @param {unknown} message the platform's message; only `data.category` is read
   * @returns {Promise<{ok: boolean, post: boolean, code: string,
   *   category: string|null, alert: object|null}>}
   */
  async function receiveNow(message) {
    const category = categoryOf(message);
    if (category === null) {
      // Which of the two it was is the difference between a sender that drifted
      // and a message that is not one -- and neither is worth an alert, so both
      // answer "nothing", with the code saying which.
      return isPlainObject(message) && isPlainObject(message.data)
        ? answer({ ok: true, post: false, code: RECEIPT_CODES.UNKNOWN_CATEGORY })
        : answer({ ok: true, post: false, code: RECEIPT_CODES.BAD_PAYLOAD });
    }
    let resolved;
    try {
      resolved = await mailbox.newChange(category);
    } catch {
      // The port itself failed. Deliberately not carrying the error through: an
      // SDK's own message can name a path or a project, and this is not a place
      // to print one. Nothing is posted and nothing is claimed about the
      // mailbox.
      return answer({ ok: false, post: false, code: RECEIPT_CODES.MAILBOX_FAILED, category });
    }
    if (!isPlainObject(resolved) || resolved.ok !== true || typeof resolved.fresh !== 'boolean') {
      return answer({ ok: false, post: false, code: RECEIPT_CODES.MAILBOX_FAILED, category });
    }
    if (resolved.fresh !== true) {
      return answer({ ok: true, post: false, code: RECEIPT_CODES.NOT_RESOLVED, category });
    }
    return answer({ ok: true, post: true, code: RECEIPT_CODES.POSTED, category, alert: alertFor(category) });
  }

  return Object.freeze({
    ok: true,
    receive: (message) => receiveNow(message),
  });
}

/**
 * A mailbox read a test (and a wiring without one) can drive: which categories
 * answer `fresh`, or a read that throws.
 *
 * `set` is what makes rule 11's second half testable -- the same push, received
 * again after the app has seen the change, resolves to nothing -- and `fail` is
 * the read that throws, which is a different answer from `set([])` and is
 * tested apart.
 */
export class MemoryMailboxChange {
  #fresh;
  #fail;
  #calls = [];

  /**
   * @param {object} [options]
   * @param {string[]|boolean} [options.fresh] the categories that answer
   *   `fresh`, or `true` for every category
   * @param {boolean} [options.fail] throw instead of answering
   * @throws {TypeError} for anything but an options object: `new
   *   MemoryMailboxChange(true)` would otherwise mean `{fresh: []}` by
   *   destructuring, and a read that is silently never fresh is a receipt that
   *   silently never posts.
   */
  constructor(options = {}) {
    if (options === null || typeof options !== 'object') {
      throw new TypeError('MemoryMailboxChange: options is an object, { fresh, fail }');
    }
    const { fresh = [], fail = false } = options;
    this.#fresh = fresh === true ? true : new Set(fresh);
    this.#fail = fail === true;
  }

  /** Change what the next call answers -- the app saw the change, or saw nothing. */
  set(fresh) {
    this.#fresh = fresh === true ? true : new Set(fresh);
  }

  /** Every category this read was asked about, in order: what the receipt asked for, and nothing else. */
  calls() {
    return Object.freeze([...this.#calls]);
  }

  async newChange(category) {
    this.#calls.push(category);
    if (this.#fail) throw new Error('the mailbox read failed');
    return Object.freeze({ ok: true, fresh: this.#fresh === true || this.#fresh.has(category) });
  }
}
