// SPDX-License-Identifier: LicenseRef-Fugaro-Subscription
// See mobile/LICENSE.md for the terms.
// The two device-local settings, and the record they live in (mobile plan, Task
// P5.3.6; D123, D127, D111; the design is
// docs/mobile/design/voice-evaluation-switch.md §4.1 and §4.6).
//
// **What this file stores, and where.** Two settings per account -- `stt` and
// `tts`, each `local` (the default) or `remote` -- plus the consent list
// `consent.js` holds. They go through the app's **non-secret settings store**
// (`../accounts/settings.js`, over `expo-async-storage.js`) and never into the
// database: D127's precedent is that a phone-side preference is a local
// setting, and the device node the design's Q3 named is not the place for it
// because the app cannot read its own device node at all (§4.6). A record read
// or written here is an ordinary JSON value -- a name, a date, one of two
// words -- which is exactly what that store is for (D111 keeps the two
// credential items out of it, and nothing in this module is one).
//
// **The key is `voice-<account id>`, in ONE segment, and the reason is a fence
// this module must not trip.** `settings.js`'s `#judgeKey` refuses a settings
// key whose `.`-separated segments name a credential (`token`, `macKey`,
// `secret`, ...) -- the fence that once refused an installation the owner had
// named `token`, and that the `account.<id>` branch was added to fix. A
// per-account key composed as `voice.<id>` would put the owner's own word back
// in front of that list: an installation called `token` could not turn its
// switch on. The prefix and the id in one segment (`voice-token`) is judged as
// one word, which no segment of the fence matches, so every account id the
// store accepts as an account id works here too. Two accounts' keys cannot
// collide, because the prefix is fixed and an account id carries no `.`
// (`account.js`), and `settings.test.js` pins both halves.
//
// **Every default is local, and every failure falls the same way.** An account
// that has never written a record reads as `{ stt: 'local', tts: 'local' }`
// with no consent -- the design's "an unset switch is local" (§4.1) -- and a
// store that cannot be read is a refusal rather than a record of defaults, so
// the caller can tell "the owner never chose" from "the app could not find
// out". What an unreadable record judges to is not decided here: the stored
// value is validated by `shared/voice-switch`'s own readers (`indicator.js`
// says why), so a record carrying `'auto'`, a consent with no date or a field
// this module does not have is refused rather than half-read.

import { SETTINGS_CODES, isAccountId } from '../accounts/index.js';
import { DIRECTION, ENGINE, REFUSAL, indicator } from '../../../shared/voice-switch/index.js';

/** The prefix the per-account record is stored under. */
export const VOICE_SETTINGS_PREFIX = 'voice-';

/**
 * The fields a stored record has, exactly. The field set **is** the version:
 * a record carrying anything else is refused rather than read past, so a later
 * change to this shape is a change to this list.
 */
export const VOICE_RECORD_FIELDS = Object.freeze(['settings', 'consent']);

/** The two directions, in the order the app considers them. */
export const DIRECTIONS = Object.freeze([DIRECTION.STT, DIRECTION.TTS]);

/**
 * What an unset switch is: `local` on both directions. Frozen and shared by
 * every reader, so "the default" exists once.
 */
export const DEFAULT_VOICE_SETTINGS = Object.freeze({
  [DIRECTION.STT]: ENGINE.LOCAL,
  [DIRECTION.TTS]: ENGINE.LOCAL,
});

/** The refusal codes this module adds to the two vocabularies it borrows. */
export const VOICE_CODES = Object.freeze({
  /** The stored value is not a record: an array, a primitive, an instance. */
  UNREADABLE_RECORD: 'unreadable-record',
  /** A field the record does not have. Spelled as the shared module spells it. */
  UNKNOWN_FIELD: REFUSAL.UNKNOWN_FIELD,
});

/** A frozen refusal, in the shape this repository's modules use. */
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
 * Throws on an id it cannot compose, for the reason `cacheNamespace` does: two
 * accounts sharing a key is one installation's switch and consent answering for
 * another's. The caller has the account (D114's tuple) or it has no business
 * asking this question.
 */
export function voiceSettingsKey(accountId) {
  if (!isAccountId(accountId)) throw new TypeError('not an account id');
  return `${VOICE_SETTINGS_PREFIX}${accountId}`;
}

/** A frozen copy of a consent list, with one plain object per entry. */
function freezeConsent(consent) {
  return Object.freeze(consent.map((entry) => Object.freeze({ provider: entry.provider, at: entry.at })));
}

/**
 * Judge a record -- the one this module is about to write, or the one it has
 * just read -- and answer with it, defaults filled in.
 *
 * The stored value's *shape* is checked here (a plain object, and no field this
 * module does not name); its *values*, every one of them, are judged by
 * `shared/voice-switch`: a switch that is not `local`/`remote`, a consent list
 * that is not a list, a name that is not a name, a date that is not one. One
 * reader, one rule, and the same rule the badge will apply to the very same
 * values a moment later.
 */
function judgeRecord(value) {
  if (!isPlainObject(value)) return refuse(VOICE_CODES.UNREADABLE_RECORD, 'record');
  for (const key of Object.keys(value)) {
    if (!VOICE_RECORD_FIELDS.includes(key)) return refuse(VOICE_CODES.UNKNOWN_FIELD, 'record');
  }
  if (value.settings !== undefined && !isPlainObject(value.settings)) {
    return refuse(VOICE_CODES.UNREADABLE_RECORD, 'record.settings');
  }

  const probe = indicator({
    direction: DIRECTION.STT,
    settings: value.settings,
    consent: value.consent,
  });
  if (!probe.ok) return refuse(probe.code, probe.field);

  const settings = { ...DEFAULT_VOICE_SETTINGS };
  if (value.settings !== undefined) {
    for (const direction of DIRECTIONS) {
      const engine = value.settings[direction];
      if (engine !== undefined) settings[direction] = engine;
    }
  }
  return Object.freeze({
    ok: true,
    settings: Object.freeze(settings),
    consent: freezeConsent(value.consent ?? []),
  });
}

/**
 * Read one account's record, or the defaults when it has never written one.
 *
 * **`absent` is the default record and every other refusal is a refusal.** The
 * settings store answers `absent` for a key nobody has written, which is the
 * ordinary state of a fresh account; a `store-failed`, an `unreadable-value` or
 * a key the store will not even judge is a different fact, and reporting it as
 * "local, and no consent" would hide a broken store behind a plausible setting.
 */
export async function readVoiceRecord(store, accountId) {
  const key = voiceSettingsKey(accountId);
  let answer;
  try {
    answer = await store.get(key);
  } catch {
    return refuse(SETTINGS_CODES.STORE_FAILED, 'settings');
  }
  if (!isPlainObject(answer)) return refuse(SETTINGS_CODES.STORE_FAILED, 'settings');
  if (!answer.ok) {
    if (answer.code === SETTINGS_CODES.ABSENT) {
      return Object.freeze({
        ok: true,
        settings: DEFAULT_VOICE_SETTINGS,
        consent: Object.freeze([]),
      });
    }
    return refuse(answer.code, 'settings');
  }
  return judgeRecord(answer.value);
}

/**
 * Write one account's record, and answer with what was written.
 *
 * The record is judged before the store sees it, so a caller cannot leave half
 * a switch on disk, and what comes back is the judged record rather than the
 * caller's object -- the same two fields, with the defaults filled in.
 */
export async function writeVoiceRecord(store, accountId, record) {
  const key = voiceSettingsKey(accountId);
  const judged = judgeRecord(record);
  if (!judged.ok) return judged;
  const value = {
    settings: { ...judged.settings },
    consent: judged.consent.map((entry) => ({ provider: entry.provider, at: entry.at })),
  };
  let written;
  try {
    written = await store.set(key, value);
  } catch {
    return refuse(SETTINGS_CODES.STORE_FAILED, 'settings');
  }
  if (!isPlainObject(written)) return refuse(SETTINGS_CODES.STORE_FAILED, 'settings');
  if (!written.ok) return refuse(written.code, 'settings');
  return judged;
}
