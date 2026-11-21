// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// §3.8's switches, as the phone holds them before they travel
// (mobile plan, Task P5.2.8; D123, D160; the design is
// docs/mobile/design/push-notifications.md §3.8).
//
// **What is stored here, and what is not.** Five per-category switches and, when
// the owner turns them on, a quiet-hours window: an ordinary JSON record of
// booleans, one zone name and two minutes. It goes through the app's
// **non-secret settings store** (`../accounts/settings.js`) under
// `push-<account id>` and never into the database -- the app cannot write
// `config/admin` (D166), which is why the switches reach the server the same way
// the token does: one registration message (§3.5 (b), §3.8). Nothing in this
// module is a credential (D111 keeps the two items out of that store) and no
// value here is secret-shaped.
//
// **The key is `push-<account id>`, in ONE segment**, for the reason
// `voice-switch/settings.js` records: the settings store refuses a key whose
// `.`-separated segments name a credential, so a key composed as
// `push.<id>` would refuse an installation the owner had named `token`. Two
// accounts' keys cannot collide: the prefix is fixed and an account id carries
// no `.` (`account.js`).
//
// **The record IS the wire fragment, and the schema judges it.** What is stored
// is exactly the object `registration`'s `prefs` field carries -- the same five
// names, the same window -- so the switches that travel are the switches the
// owner set, with no mapping in between to get wrong. Every value is judged by
// `message.js`'s `judgeField('prefs', …)`, which asks
// `shared/mailbox-schema`: a switch that is not a boolean, a missing flag, a
// zone that is not an IANA name and a window with no width are refused by the
// schema's own code and dotted path, so a switch this app accepts is one the
// consumer that applies the message accepts too.
//
// **The defaults are D160's own answer and they exist once.** §3.8: every
// category switch defaults on except cap-near, which D160 answers "available,
// off by default", and quiet hours default off -- a window that is not there.
// A record nobody has written reads as exactly that, and a record this build
// cannot read is a refusal rather than a silent set of defaults, so a screen can
// tell "the owner never chose" from "the app could not find out" (the rule
// `voice-switch/settings.js` states for its own record). `reset()` exists so
// that the second case is recoverable without reinstalling the app.

import {
  PREFS_FIELDS,
  PREFS_REQUIRED,
  QUIET_HOURS_FIELDS,
  REFUSALS,
} from '../../../shared/mailbox-schema/index.js';
import { SETTINGS_CODES, isAccountId } from '../accounts/index.js';
import { judgeField } from './message.js';

/** The prefix each account's record is stored under. */
export const PUSH_SETTINGS_PREFIX = 'push-';

/** The five switches, in the schema's own list -- not a copy of it. */
export const CONTROL_NAMES = Object.freeze([...PREFS_REQUIRED]);

/** The window's three fields, in the schema's own list. */
export const WINDOW_FIELDS = Object.freeze([...QUIET_HOURS_FIELDS]);

/**
 * What an unset switch is: D160's answer, and the design's §3.8 defaults.
 * Frozen and shared by every reader, so "the default" exists once.
 */
export const CONTROL_DEFAULTS = Object.freeze({
  kill_switch: true,
  cap_reached: true,
  consumer_offline: true,
  cap_near: false,
  prompt: true,
});

/** The refusal codes this module adds to the schema's and the store's. */
export const CONTROL_CODES = Object.freeze({
  /** The stored value is not a prefs record: a primitive, an array, an instance. */
  UNREADABLE_RECORD: 'unreadable-record',
  /** The device could not name its time zone, so a window cannot be set. */
  NO_ZONE: 'no-zone',
});

function refuse(code, field) {
  return Object.freeze({ ok: false, code, field });
}

/** A plain object, and not null, an array, or an instance of something. */
function isPlainObject(value) {
  return value !== null && typeof value === 'object' && Object.getPrototypeOf(value) === Object.prototype;
}

/**
 * The settings key one account's record is stored under.
 *
 * Throws on an id it cannot compose, for the reason `voiceSettingsKey` does:
 * two accounts sharing a key is one phone's switches deciding another's. The
 * caller has the account (D114's tuple) or it has no business asking.
 */
export function pushSettingsKey(accountId) {
  if (!isAccountId(accountId)) throw new TypeError('not an account id');
  return `${PUSH_SETTINGS_PREFIX}${accountId}`;
}

/** Judge a record -- the one about to be written, or the one just read. */
function judgeRecord(value) {
  if (!isPlainObject(value)) return refuse(CONTROL_CODES.UNREADABLE_RECORD, 'prefs');
  // The field set is judged from the schema's own list, so a record carrying a
  // switch this build does not have is refused rather than read past -- the
  // same rule the message's field set is held to (`shared/mailbox-schema`: "the
  // field set IS the version").
  for (const key of Object.keys(value)) {
    if (!PREFS_FIELDS.includes(key)) return refuse(REFUSALS.UNKNOWN_FIELD, `prefs.${key}`);
  }
  const refused = judgeField('prefs', value);
  if (refused !== null) return refused;
  return Object.freeze({ ok: true, prefs: Object.freeze({ ...value }) });
}

/**
 * The tail of each account's queue, keyed by the settings store and then by the
 * account id -- `voice-switch/switch.js`'s shape, for its reason: three methods
 * here read the record, await the store and write it back, so two of them at
 * once (two taps on two switches) would each build its record on the one the
 * other replaced, and one of the owner's choices would be lost. The store is
 * the WeakMap key so a dropped store takes its queues with it.
 */
const QUEUES = new WeakMap();

function serialize(settings, accountId, work) {
  let byAccount = QUEUES.get(settings);
  if (byAccount === undefined) {
    byAccount = new Map();
    QUEUES.set(settings, byAccount);
  }
  const previous = byAccount.get(accountId) ?? Promise.resolve();
  const run = previous.then(work, work);
  byAccount.set(
    accountId,
    run.then(
      () => undefined,
      () => undefined,
    ),
  );
  return run;
}

/**
 * §3.8's switches over one account's settings record.
 *
 * @param {object} options
 * @param {object} options.settings the app's `SettingsStore` (or its memory
 *   double): `get(key)`, `set(key, value)`, `delete(key)`
 * @param {string} options.accountId D114's installation id
 * @param {() => (string|null)} [options.zone] the device's IANA time zone, as
 *   the OS holds it. Optional, and its absence is answered rather than trusted:
 *   a window cannot be set without a zone, and the design stores the zone NAME
 *   and never a UTC offset, because an offset drifts onto the wrong hour at
 *   every DST transition (§3.8, Q5)
 * @returns {object} the frozen door
 * @throws {TypeError} for a store, an account id or a zone source that cannot
 *   be used
 */
export function createPushControls({ settings, accountId, zone } = {}) {
  if (settings === null || typeof settings !== 'object'
    || typeof settings.get !== 'function' || typeof settings.set !== 'function') {
    throw new TypeError('createPushControls: a settings store with get() and set() is required');
  }
  const key = pushSettingsKey(accountId);
  if (zone !== undefined && typeof zone !== 'function') {
    throw new TypeError('createPushControls: the zone source must be a function when one is given');
  }

  /** The device's zone, or `null` when it cannot name one. */
  function deviceZone() {
    if (zone === undefined) return null;
    let value;
    try {
      value = zone();
    } catch {
      // A device that will not answer is the same fact as one that answers
      // nothing: there is no zone to store, and a window without a zone is a
      // window in the wrong hours twice a year (§3.8).
      return null;
    }
    return typeof value === 'string' && value !== '' ? value : null;
  }

  /**
   * The record, with the defaults filled for an account that never wrote one.
   * `absent` is the default record and every other refusal is a refusal.
   */
  async function read() {
    let answer;
    try {
      answer = await settings.get(key);
    } catch {
      return refuse(SETTINGS_CODES.STORE_FAILED, 'prefs');
    }
    if (!isPlainObject(answer)) return refuse(SETTINGS_CODES.STORE_FAILED, 'prefs');
    if (!answer.ok) {
      if (answer.code === SETTINGS_CODES.ABSENT) {
        return Object.freeze({ ok: true, prefs: CONTROL_DEFAULTS, stored: false });
      }
      return refuse(answer.code, 'prefs');
    }
    const judged = judgeRecord(answer.value);
    if (!judged.ok) return judged;
    return Object.freeze({ ok: true, prefs: judged.prefs, stored: true });
  }

  /** Write a record the schema has already accepted. */
  async function write(prefs) {
    let written;
    try {
      written = await settings.set(key, { ...prefs });
    } catch {
      return refuse(SETTINGS_CODES.STORE_FAILED, 'prefs');
    }
    if (!isPlainObject(written)) return refuse(SETTINGS_CODES.STORE_FAILED, 'prefs');
    if (!written.ok) return refuse(written.code, 'prefs');
    return Object.freeze({ ok: true, prefs: Object.freeze({ ...prefs }) });
  }

  /** The record with `patch` merged in, judged by the schema as a whole. */
  function judged(prefs, patch) {
    const next = { ...prefs, ...patch };
    const refused = judgeField('prefs', next);
    if (refused !== null) return refused;
    return Object.freeze({ ok: true, prefs: next });
  }

  async function setControl(name, value) {
    if (!CONTROL_NAMES.includes(name)) return refuse(REFUSALS.UNKNOWN_FIELD, `prefs.${String(name)}`);
    const record = await read();
    if (!record.ok) return record;
    const next = judged(record.prefs, { [name]: value });
    if (!next.ok) return next;
    return write(next.prefs);
  }

  async function setQuietHours(window) {
    const inZone = deviceZone();
    if (inZone === null) return refuse(CONTROL_CODES.NO_ZONE, 'prefs.quiet_hours.zone');
    const asked = window === null || typeof window !== 'object' ? {} : window;
    const record = await read();
    if (!record.ok) return record;
    const next = judged(record.prefs, {
      quiet_hours: { zone: inZone, start: asked.start, end: asked.end },
    });
    if (!next.ok) return next;
    return write(next.prefs);
  }

  async function clearQuietHours() {
    const record = await read();
    if (!record.ok) return record;
    const next = { ...record.prefs };
    delete next.quiet_hours;
    return write(next);
  }

  /** The defaults over whatever is there -- the one repair for a record this
   * build cannot read, which `read` refuses rather than silently defaulting. */
  async function reset() {
    return write(CONTROL_DEFAULTS);
  }

  return Object.freeze({
    ok: true,
    key,
    read,
    setControl: (name, value) => serialize(settings, accountId, () => setControl(name, value)),
    setQuietHours: (window) => serialize(settings, accountId, () => setQuietHours(window)),
    clearQuietHours: () => serialize(settings, accountId, () => clearQuietHours()),
    reset: () => serialize(settings, accountId, () => reset()),
  });
}
